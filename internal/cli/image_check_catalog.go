package cli

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// catalogCheckSpecEnv is where the project's catalog check job reads its spec:
// the project's own Cloud Run job, one run of which decides every entry of the
// project's image catalog (D1148). fugaro update writes it, as it writes the
// repository check job's infra.CheckSpecEnv.
const catalogCheckSpecEnv = "FUGARO_CATALOG_CHECK_SPEC"

// catalogNotInstalledReason is the reason an entry the published catalog has
// and the installed job's spec does not gets, and the one a spec whose names
// disagree with this fugaro's gets: the installation's catalog check job is
// behind the project's catalog, and the operator's one command fixes it.
const catalogNotInstalledReason = "run fugaro update"

// catalogCheckSpec is the catalog check job's spec, as fugaro update renders it
// into FUGARO_CATALOG_CHECK_SPEC.
//
// It carries the catalog and the repositories because the job can read neither
// on its own: the published catalog lives in the project's policy node, whose
// rules grant a read to a Fugaro-minted token alone (internal/budget/rules), so
// a service account's IAM read of it would be a project-level database role,
// and this job's account holds no project-level role at all (D1148). The
// catalog therefore reaches the job here, as the published catalog the
// operator's own fugaro update read, and the repositories come with it: which
// entries each one builds, and the facts a build of them is submitted with.
type catalogCheckSpec struct {
	Version int `json:"version"`
	// Catalog is the project's published image catalog, config.Catalog's own
	// shape. An entry the spec assigns and the catalog no longer has, and an
	// entry the catalog has that no repository is assigned, are both reported:
	// the installed job and the project's catalog have to agree.
	Catalog *config.Catalog `json:"catalog"`
	// Repositories are the project's repositories, each with the entries it
	// builds. That assignment is what makes the same-repo guard
	// (catalogBuildEntryGuard) load-bearing here: an entry whose Dockerfile
	// has moved to another repository is refused rather than built as the
	// account of the repository the spec still lists it under.
	Repositories []catalogCheckRepo `json:"repositories"`
	// BaseImages are the installation's base images by kind (the local config's
	// base_images), and every entry's image is built FROM the one
	// config.BaseKind names.
	BaseImages map[string]string `json:"base_images"`
	// MachineType is the machine a build runs on, and BuildRegion the Cloud
	// Build region it is submitted to (the local config's build settings).
	MachineType string `json:"machine_type"`
	BuildRegion string `json:"build_region"`
	// MaxImageBuilds is this run's build cap: how many Cloud Builds one run of
	// this job may submit, whatever the catalog says changed. It is M5's own
	// bound (managed.MaxImageBuilds' range), zero is the default
	// (managed.DefaultImageBuilds), and the managed marker's max_image_builds
	// is what fugaro update renders here — one setting, so a project that
	// bounds a managed creation's image builds bounds this job's too.
	MaxImageBuilds int `json:"max_image_builds,omitempty"`
}

// catalogCheckRepo is one repository's part of the catalog check job's spec:
// the facts a build of one of its entries is submitted with, and the entries it
// builds.
type catalogCheckRepo struct {
	// Repo is the repository (owner/name) and Slug the storage slug this
	// fugaro derives for it (task.Slug): the record, the check state and the
	// build's own record all live under builds/<slug>/, and the build runs as
	// that slug's build account.
	Repo string `json:"repo"`
	Slug string `json:"slug"`
	// Provider, RepoURL and BaseBranch are the repository as a build clones
	// it, and GitSecretID, GitUser and GitHubAppID the credential it clones
	// with — the repository's own, never the job's.
	Provider    string `json:"provider"`
	RepoURL     string `json:"repo_url"`
	BaseBranch  string `json:"base_branch"`
	GitSecretID string `json:"git_secret_id,omitempty"`
	GitUser     string `json:"git_user,omitempty"`
	GitHubAppID string `json:"github_app_id,omitempty"`
	// Workflow is the name the smoke's spec is rendered for. A catalog entry
	// is not a workflow, and image.SpecForCloud reads a config only to check
	// that the name it is given exists, so this is the repository's own first
	// workflow, as the manual path passes for the same reason.
	Workflow string `json:"workflow"`
	// BuildServiceAccount is the email of the repository's build account: the
	// one account a build of this repository's entries may run as.
	BuildServiceAccount string `json:"build_service_account"`
	// WorkflowSecrets are the repository's build secrets, the ones its
	// workflows declare (catalogBuildSecrets' answer for it).
	WorkflowSecrets []config.Secret `json:"workflow_secrets,omitempty"`
	// Entries are the catalog entries this repository builds.
	Entries []string `json:"entries"`
}

// catalogJobEnv is the job's spec and the cloud names its own environment
// carries.
type catalogJobEnv struct {
	spec                             catalogCheckSpec
	name, gcpProject, region, bucket string
	maxImageBuilds                   int
}

// readCatalogJobEnv reads the job's spec and its own variables, refusing an
// incomplete one the way readJobEnv refuses the repository job's: a job that
// cannot tell what to check says so rather than deciding nothing.
func readCatalogJobEnv() (catalogJobEnv, error) {
	var e catalogJobEnv
	raw := os.Getenv(catalogCheckSpecEnv)
	if raw == "" {
		return e, fmt.Errorf("%s is not set: fugaro image check --job --catalog runs in the project's catalog check job, which fugaro update sets up", catalogCheckSpecEnv)
	}
	if err := json.Unmarshal([]byte(raw), &e.spec); err != nil {
		return e, fmt.Errorf("%s: %w", catalogCheckSpecEnv, err)
	}
	s := e.spec
	if s.Version < 1 || s.Catalog == nil || len(s.Repositories) == 0 || len(s.BaseImages) == 0 || s.MachineType == "" || s.BuildRegion == "" {
		return e, fmt.Errorf("%s is incomplete: %s", catalogCheckSpecEnv, catalogNotInstalledReason)
	}
	for _, r := range s.Repositories {
		if r.Repo == "" || r.Slug == "" || r.Provider == "" || r.RepoURL == "" || r.BaseBranch == "" ||
			r.Workflow == "" || r.BuildServiceAccount == "" || len(r.Entries) == 0 {
			return e, fmt.Errorf("%s: the repository %s is incomplete: %s", catalogCheckSpecEnv, pluginwire.Printable(r.Repo), catalogNotInstalledReason)
		}
	}
	e.maxImageBuilds = cmp.Or(s.MaxImageBuilds, int(managed.DefaultImageBuilds))
	if err := managed.CheckImageBuilds(int64(e.maxImageBuilds)); err != nil {
		return e, fmt.Errorf("%s: %v", catalogCheckSpecEnv, err)
	}
	e.name, e.gcpProject, e.region = os.Getenv("FUGARO_PROJECT"), os.Getenv("FUGARO_GCP_PROJECT"), os.Getenv("FUGARO_REGION")
	u, err := url.Parse(os.Getenv("FUGARO_BUCKET"))
	if e.name == "" || e.gcpProject == "" || e.region == "" || err != nil || u.Scheme != "gs" || u.Host == "" || strings.Trim(u.Path, "/") != "" {
		return e, fmt.Errorf("FUGARO_PROJECT (the project's name), FUGARO_GCP_PROJECT, FUGARO_REGION and FUGARO_BUCKET (gs://<runs bucket>) must be set, as the catalog check job's spec sets them; %s", catalogNotInstalledReason)
	}
	e.bucket = u.Host
	return e, nil
}

// catalogCheckTarget is one run's read of the catalog: the clients every
// entry's decision and submission goes through, and the base every entry's
// image is built FROM.
type catalogCheckTarget struct {
	spec       catalogCheckSpec
	gcpProject string
	region     string
	maxBuilds  int
	force      bool
	now        time.Time
	bucket     *blobx.Bucket
	builder    *gcp.Builder
	registry   *imagecheck.Registry
	// base is the installation's base image, and baseDigest its digest now
	// ("" when it could not be read: then every entry's base trigger is
	// skipped, the way a workflow's is).
	base, baseDigest string
	warn             func(string)
}

// readBaseDigest reads the base image's digest, leaving it out (with a
// warning) when it cannot be read: the base trigger is skipped for every entry.
func (t *catalogCheckTarget) readBaseDigest(ctx context.Context) {
	d, err := t.registry.Digest(ctx, t.base)
	if err != nil {
		t.warn(fmt.Sprintf("could not read the base image's digest (%s); every entry's base trigger is skipped", oneLine(err.Error())))
		return
	}
	t.baseDigest = d
}

// catalogEvaluation is one entry's decision and what it was made from.
type catalogEvaluation struct {
	d          imagecheck.CatalogDecision
	rec        *imagecheck.CatalogRecordState
	prev       *imagecheck.CatalogCheckState
	prevRaw    []byte
	prevGen    int64
	prevExists bool
	// prevUnreadable: the check state exists but does not parse. It is left as
	// it is, for a human to look at, and nothing is submitted for the entry.
	prevUnreadable bool
	// lastStatus is the status of prev's build as read now, "" if none.
	lastStatus string
	// definitionSHA256 and definitionGeneration are the copied definition the
	// build would read: the sha256 of the object's bytes and its generation,
	// read back from the one object the build itself reads.
	definitionSHA256     string
	definitionGeneration int64
}

// evaluate reads what entry e's decision needs and decides it. An error is a
// failure to read or a refusal; the caller reports it as check-failed and
// submits nothing.
func (t *catalogCheckTarget) evaluate(ctx context.Context, e config.Entry, r catalogCheckRepo, slug string) (catalogEvaluation, error) {
	var ev catalogEvaluation
	// The guard comes first, before anything is read or submitted: an entry
	// whose Dockerfile is another repository's is built by that repository's
	// build account or not at all (catalogBuildEntryGuard, the manual path's
	// rule, and the reason a spec rendered before the catalog moved an entry is
	// refused rather than obeyed).
	if err := catalogBuildEntryGuard(e, r.Repo); err != nil {
		return ev, err
	}
	recKey, err := gcp.CatalogRecordKey(slug, e.Name)
	if err != nil {
		return ev, err
	}
	data, _, err := t.bucket.Read(ctx, recKey)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
	case err != nil:
		return ev, fmt.Errorf("reading %s: %w", recKey, err)
	default:
		rec, perr := imagecheck.ParseCatalogRecordState(data)
		if perr != nil {
			// A workflow's unreadable record is replaced by the build's own
			// gate, so its check may treat it as missing. Nothing repairs an
			// entry's record, and it is what says which definition the image
			// was built from and which commit a rebuild is pinned to: an
			// unreadable one is not a missing one, and nothing is submitted for
			// this entry until it is fixed.
			return ev, fmt.Errorf("%s is unreadable (%v), and holds the definition the image was built from and the commit a rebuild is pinned to: nothing is submitted for %s until it is fixed or deleted", recKey, perr, pluginwire.Printable(e.Name))
		}
		ev.rec = rec
	}
	// The definition the build would read, and the generation it pins. A copy
	// that is missing is not "nothing changed" — a build submitted now would
	// have nothing to build FROM — so it is an error here, naming the one
	// command that writes it.
	key, err := config.CatalogDockerfileKey(e.Name)
	if err != nil {
		return ev, err
	}
	def, gen, err := t.bucket.ReadMax(ctx, key, catalog.MaxDockerfileBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return ev, fmt.Errorf("the catalog entry %s has no copied definition at %s, which is what its build reads: fugaro image build %s from %s's checkout copies it there and builds it once, and this job rebuilds it from then on",
			pluginwire.Printable(e.Name), key, pluginwire.Printable(e.Name), pluginwire.Printable(r.Repo))
	case err != nil:
		return ev, fmt.Errorf("reading %s: %w", key, err)
	}
	sum := sha256.Sum256(def)
	ev.definitionSHA256, ev.definitionGeneration = hex.EncodeToString(sum[:]), gen

	checkKey := imagecheck.CatalogCheckKey(slug, e.Name)
	ev.prevRaw, ev.prevGen, err = t.bucket.Read(ctx, checkKey)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
	case err != nil:
		return ev, fmt.Errorf("reading %s: %w", checkKey, err)
	default:
		ev.prevExists = true
		if ev.prev, err = imagecheck.ParseCatalogCheckState(ev.prevRaw); err != nil {
			// It holds the back-off state: without it a build known to fail
			// could be submitted again. Fail safe, and leave it for a human.
			ev.prevUnreadable = true
			return ev, fmt.Errorf("%s is unreadable (%v), and holds the last rebuild's state: nothing is submitted for %s until it is fixed or deleted", checkKey, err, pluginwire.Printable(e.Name))
		}
	}
	if p := ev.prev; p != nil && p.BuildID != "" {
		if imagecheck.FinalBuild(p.LastBuildStatus) {
			ev.lastStatus = p.LastBuildStatus
		} else {
			ev.lastStatus, err = t.builder.Status(ctx, p.BuildID)
			switch {
			case errors.Is(err, gcp.ErrBuildNotFound):
				t.warn(fmt.Sprintf("the last rebuild %s of %s is unknown to Cloud Build", p.BuildID, e.Name))
				ev.lastStatus = ""
			case err != nil:
				return ev, err
			}
		}
	}
	ev.d = imagecheck.DecideCatalog(imagecheck.CatalogInputs{
		Entry: e.Name, Record: ev.rec, LastCheck: ev.prev, LastBuildStatus: ev.lastStatus,
		DefinitionSHA256: ev.definitionSHA256, BaseRef: t.base, BaseDigest: t.baseDigest,
		// The lockfiles trigger is the one trigger this job cannot evaluate: it
		// is decided from the baked repository's tree, and this job has no
		// checkout of it and no git credential to read one with. An entry whose
		// repository's dependency files moved is therefore not rebuilt by the
		// schedule; fugaro image build <entry> from that checkout is what picks
		// that up.
		Lockfiles: false,
		MaxAge:    config.DefaultMaxAge, Now: t.now, Force: t.force,
	})
	return ev, nil
}

// submit submits entry e's rebuild as repository r's build account: the request
// fugaro image build <entry> sends, with the definition this run read and the
// commit the entry's record names. The account is r's, and
// CatalogBuildSpec.check refuses any other, so the job's own account is never
// the one a build runs as.
func (t *catalogCheckTarget) submit(ctx context.Context, e config.Entry, r catalogCheckRepo, slug string, ev catalogEvaluation) (id, status string, err error) {
	rec := ev.rec
	if rec == nil {
		// The definition trigger can fire with no record — the copy moved and
		// nothing recorded building it — and a build must state the commit its
		// image bakes: this job clones nothing, so the record is the only place
		// it could read one. An entry that has never been built is therefore
		// the operator's own build, once.
		return "", "", fmt.Errorf("the catalog entry %s has no build record in the runs bucket, and a rebuild is pinned to the commit its last build recorded: fugaro image build %s from %s's checkout builds it once, and the schedule rebuilds it from then on",
			pluginwire.Printable(e.Name), pluginwire.Printable(e.Name), pluginwire.Printable(r.Repo))
	}
	imageRef, err := gcp.SharedCatalogImage(t.region, t.gcpProject, e.Name)
	if err != nil {
		return "", "", err
	}
	selftest, err := catalogSmokeSpec(r, rec.SourceCommit)
	if err != nil {
		return "", "", err
	}
	res, err := t.builder.SubmitCatalog(ctx, gcp.CatalogBuildSpec{
		Entry: e.Name, Slug: slug,
		GitProvider: r.Provider, RepoURL: r.RepoURL, BaseBranch: r.BaseBranch,
		GitSecretID: r.GitSecretID, GitUser: r.GitUser, GitHubAppID: r.GitHubAppID,
		Base: t.base, Image: imageRef,
		Definition:     gcp.CatalogDefinition{SourceCommit: rec.SourceCommit, Generation: ev.definitionGeneration, SHA256: ev.definitionSHA256},
		ServiceAccount: r.BuildServiceAccount, MachineType: t.spec.MachineType,
		WorkflowSecrets: r.WorkflowSecrets,
		Bucket:          "gs://" + t.bucket.GCSName, Selftest: selftest,
	})
	if err != nil {
		return "", "", err
	}
	return res.ID, res.Status, nil
}

// catalogSmokeSpec is the smoke a catalog build of r's entries runs: the spec
// image.SpecForCloud renders for the manual path, so the two submit the same
// one. The config is a stub because an entry is not a workflow and
// SpecForCloud reads a config only to check that the name it is given exists
// (it reads the same constant for every workflow).
func catalogSmokeSpec(r catalogCheckRepo, commit string) ([]byte, error) {
	spec, err := image.SpecForCloud(&config.Config{Workflows: map[string]config.Workflow{r.Workflow: {}}}, r.Workflow, commit, r.RepoURL)
	if err != nil {
		return nil, err
	}
	return json.Marshal(spec)
}

// catalogCheckLogLine is the catalog check job's log line for one entry:
// checkLogLine's shape under the entry's own names, since an entry is not a
// workflow. Cloud Run takes a JSON line on stdout as a structured entry, with
// its severity.
type catalogCheckLogLine struct {
	Severity        string   `json:"severity"`
	Event           string   `json:"event"`
	Entry           string   `json:"entry"`
	Repo            string   `json:"repo,omitempty"`
	Decision        string   `json:"decision"`
	Reasons         []string `json:"reasons"`
	BuildID         string   `json:"build_id"`
	LastBuildStatus string   `json:"last_build_status,omitempty"`
	Error           string   `json:"error,omitempty"`
	DryRun          bool     `json:"dry_run,omitempty"`
	Message         string   `json:"message"`
}

type catalogCheckLogger struct {
	w        io.Writer
	dryRun   bool
	maxBuild int
}

// log writes one entry's line. rebuild-failed-last, check-failed, a last
// rebuild that failed and a rebuild the run's cap stopped are ERROR, which the
// alert matches; an entry the installed job does not know is WARNING, the
// severity checkLogger gives a workflow head and the job disagree about.
func (l *catalogCheckLogger) log(entry, repo, decision string, reasons []string, buildID, lastStatus, errMsg string) {
	capped := slices.Contains(reasons, imagecheck.ReasonBuildCap)
	sev := "INFO"
	switch {
	case decision == imagecheck.RebuildFailedLast || decision == imagecheck.CheckFailed,
		// A failed last rebuild matters while a rebuild is still due.
		decision == imagecheck.Rebuild && imagecheck.FailedBuild(lastStatus),
		capped:
		sev = "ERROR"
	case decision == imagecheck.NotInstalled:
		sev = "WARNING"
	}
	msg := entry
	if repo != "" {
		msg += " (" + repo + ")"
	}
	msg += ": " + decision
	if len(reasons) > 0 {
		msg += " (" + strings.Join(reasons, ", ") + ")"
	}
	switch {
	case errMsg != "":
		msg += ": " + errMsg
	case decision == imagecheck.Rebuild && buildID != "" && !l.dryRun && !capped:
		msg += "; submitted Cloud Build build " + buildID
	case capped:
		msg += fmt.Sprintf("; this run's cap of %d build(s) is reached, so this rebuild is left for the next run", l.maxBuild)
	case decision == imagecheck.RebuildFailedLast:
		msg += "; the last rebuild " + buildID + " ended " + lastStatus + " on these same inputs without clearing the triggers, so it is not submitted again until something changes (fugaro image check --force overrides)"
	}
	line := catalogCheckLogLine{
		Severity: sev, Event: "catalog-check", Entry: entry, Repo: repo, Decision: decision,
		Reasons: nonNil(reasons), BuildID: buildID, LastBuildStatus: lastStatus, Error: errMsg, DryRun: l.dryRun, Message: msg,
	}
	data, err := json.Marshal(line)
	if err != nil {
		data = []byte(`{"severity":"ERROR","event":"catalog-check","message":"unloggable line"}`)
	}
	fmt.Fprintln(l.w, string(data))
}

// catalogNextState is the entry's check.json after decision d: the last
// submitted build and its inputs carry over from prev, the way nextState
// carries a workflow's.
func catalogNextState(prev *imagecheck.CatalogCheckState, d imagecheck.CatalogDecision, lastStatus string, now time.Time) *imagecheck.CatalogCheckState {
	s := &imagecheck.CatalogCheckState{Version: imagecheck.CatalogCheckVersion, CheckedAt: now, Decision: d.Decision,
		Reasons: nonNil(d.Reasons), Error: d.Error}
	if prev != nil {
		s.BuildID, s.BuildInputs, s.LastBuildAt = prev.BuildID, prev.BuildInputs, prev.LastBuildAt
		s.LastBuildStatus = cmp.Or(lastStatus, prev.LastBuildStatus)
	}
	return s
}

// catalogAssignment is one entry this run checks: the name the spec assigns,
// the entry the catalog has under that name (found reports whether it has one
// at all), and the repository the spec assigns it to.
type catalogAssignment struct {
	name  string
	entry config.Entry
	found bool
	repo  catalogCheckRepo
	slug  string
}

// runImageCheckCatalogJob is fugaro image check --job --catalog: one run of the
// project's own catalog check job (D1148). It decides every entry of the
// project's image catalog with imagecheck.DecideCatalog's triggers and submits
// at most one Cloud Build per entry that fires — as that entry's own
// repository's build account, never as the job's own — writing each entry's
// decision to builds/<slug>/catalog/<entry>/check.json, which is the state its
// next run's back-off is read from.
//
// It builds nothing it could not read: an entry's record, its copied definition
// or its check state that cannot be read is an error for that entry
// (check-failed, nothing submitted), never "no record, so rebuild". The run's
// own build cap (the spec's max_image_builds, one by default) bounds how many
// builds one run may submit, so a bug in a decision cannot spend a project's
// budget in one night: the entries it leaves are recorded with the build-cap
// reason and are the next run's.
func runImageCheckCatalogJob(cmd *cobra.Command, o imageCheckOptions) error {
	ctx := cmd.Context()
	now := time.Now().UTC().Truncate(time.Second)
	lg := &catalogCheckLogger{w: cmd.OutOrStdout(), dryRun: o.dryRun}
	e, err := readCatalogJobEnv()
	if err != nil {
		lg.log("", "", imagecheck.CheckFailed, nil, "", "", oneLine(err.Error()))
		return remote(err)
	}
	lg.maxBuild = e.maxImageBuilds
	s := e.spec
	warn := func(msg string) { fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", msg) }

	// The spec's assignment, and the two cross-checks that say the installed
	// job and this fugaro agree: the slug is this fugaro's own derivation
	// (task.Slug), and the build account is the one that slug's repository has.
	// A mismatch is the whole job's rather than one entry's: every build it
	// would submit could run as the wrong account, so nothing is submitted.
	var work []catalogAssignment
	assigned := map[string]bool{}
	for _, r := range s.Repositories {
		slug, serr := task.Slug(r.Provider, r.Repo)
		if serr != nil {
			return catalogFailAll(cmd, ctx, lg, e, nil, fmt.Errorf("the spec's repository %s: %w", pluginwire.Printable(r.Repo), serr), now, o.dryRun)
		}
		if slug != r.Slug {
			return catalogFailAll(cmd, ctx, lg, e, nil, fmt.Errorf("the installed catalog check job names the repository %s as %s, but this fugaro names it %s: %s",
				pluginwire.Printable(r.Repo), pluginwire.Printable(r.Slug), slug, catalogNotInstalledReason), now, o.dryRun)
		}
		if want := gcp.BuildServiceAccountID(slug) + "@" + e.gcpProject + ".iam.gserviceaccount.com"; r.BuildServiceAccount != want {
			return catalogFailAll(cmd, ctx, lg, e, nil, fmt.Errorf("the installed catalog check job builds %s as %s, but %s's build account is %s: %s",
				pluginwire.Printable(r.Repo), pluginwire.Printable(r.BuildServiceAccount), pluginwire.Printable(r.Repo), want, catalogNotInstalledReason), now, o.dryRun)
		}
		for _, name := range r.Entries {
			entry, found := catalogEntryNamed(s.Catalog, name)
			work = append(work, catalogAssignment{name: name, entry: entry, found: found, repo: r, slug: slug})
			assigned[name] = true
		}
	}

	bucket, err := checkOpenBucket(ctx, "gs://"+e.bucket)
	if err != nil {
		return catalogFailAll(cmd, ctx, lg, e, nil, err, now, o.dryRun)
	}
	defer bucket.Close()
	builder, err := gcp.NewBuilder(ctx, gcp.Options{GCPProject: e.gcpProject, Region: e.region, Endpoints: checkEndpoints}, s.BuildRegion)
	if err != nil {
		return catalogFailAll(cmd, ctx, lg, e, bucket, err, now, o.dryRun)
	}
	base := s.BaseImages[config.BaseKind]
	if base == "" {
		return catalogFailAll(cmd, ctx, lg, e, bucket, fmt.Errorf("the installed catalog check job has no %s base image: %s", config.BaseKind, catalogNotInstalledReason), now, o.dryRun)
	}
	t := &catalogCheckTarget{
		spec: s, gcpProject: e.gcpProject, region: e.region, maxBuilds: e.maxImageBuilds,
		force: o.force, now: now, bucket: bucket, builder: builder, registry: checkRegistry(),
		base: base, warn: warn,
	}
	t.readBaseDigest(ctx)

	var failed []string
	submitted := 0
	for _, w := range work {
		if !w.found {
			// The installed job is behind the catalog: an entry it was
			// installed for is gone from the published catalog, so there is
			// nothing to decide and nothing to record.
			lg.log(w.name, w.repo.Repo, imagecheck.NotInstalled, []string{catalogNotInstalledReason}, "", "", "")
			continue
		}
		if w.entry.Source.Kind == config.SourceImage {
			// A prebuilt image has nothing to build (CheckCatalog passes it
			// over for the same reason), so it is named rather than silently
			// dropped: a spec listing an entry that needs no build was rendered
			// from an older catalog.
			fmt.Fprintf(cmd.ErrOrStderr(), "note: the catalog entry %s is the prebuilt image %s, so it has nothing to build; passing it over\n",
				pluginwire.Printable(w.entry.Name), pluginwire.Printable(w.entry.Source.Image))
			continue
		}
		ev, err := t.evaluate(ctx, w.entry, w.repo, w.slug)
		if err != nil {
			ev.d = imagecheck.CatalogDecision{Entry: w.name, Decision: imagecheck.CheckFailed, Error: oneLine(err.Error())}
		}
		// A rebuild the run's cap stops is left for the next run: it is
		// recorded as a rebuild with the cap note, so the entry stays due and
		// the line is an ERROR.
		capped := ev.d.Decision == imagecheck.Rebuild && !o.dryRun && submitted >= t.maxBuilds
		if capped {
			ev.d.Reasons = append(ev.d.Reasons, imagecheck.ReasonBuildCap)
		}
		state := catalogNextState(ev.prev, ev.d, ev.lastStatus, now)
		if ev.d.Decision == imagecheck.Rebuild && !o.dryRun && !capped {
			id, status, serr := t.submit(ctx, w.entry, w.repo, w.slug, ev)
			if serr != nil {
				ev.d.Decision, ev.d.Error = imagecheck.CheckFailed, "submitting the rebuild: "+oneLine(serr.Error())
				state = catalogNextState(ev.prev, ev.d, ev.lastStatus, now)
			} else {
				submitted++
				inputs := ev.d.Inputs
				state.BuildID, state.BuildInputs, state.LastBuildStatus, state.LastBuildAt = id, &inputs, status, &now
			}
		}
		if !o.dryRun && !ev.prevUnreadable {
			if err := catalogWriteState(ctx, bucket, imagecheck.CatalogCheckKey(w.slug, w.name), state, ev); err != nil {
				msg := oneLine(err.Error())
				if ev.d.Decision == imagecheck.Rebuild && state.BuildID != "" && !capped {
					// The build runs, but the next check cannot back off from it
					// or wait for it: say which build it is.
					msg = "submitted Cloud Build build " + state.BuildID + ", but could not record it in check.json: " + msg
				}
				ev.d.Decision, ev.d.Error = imagecheck.CheckFailed, cmp.Or(ev.d.Error, msg)
			}
		}
		// A capped entry's build_id is the last build a check submitted, not
		// one this run did: the line says so rather than implying a submission.
		logID := state.BuildID
		if capped {
			logID = ""
		}
		lg.log(w.name, w.repo.Repo, ev.d.Decision, ev.d.Reasons, logID, ev.lastStatus, ev.d.Error)
		if ev.d.Decision == imagecheck.CheckFailed {
			failed = append(failed, w.name)
		}
	}
	// Every entry of the published catalog that no repository is assigned is
	// named: nothing refreshes it, and it has no repository whose check.json
	// could hold the decision, so the log line is the only record of it there
	// is.
	for _, ce := range s.Catalog.Entries {
		if ce.Source.Kind == config.SourceImage || assigned[ce.Name] {
			continue
		}
		lg.log(ce.Name, "", imagecheck.NotInstalled, []string{catalogNotInstalledReason}, "", "", "")
	}
	if len(failed) > 0 {
		return remote(fmt.Errorf("the catalog check failed for %s", strings.Join(failed, ", ")))
	}
	return nil
}

// catalogFailAll is a failure before any entry is decided: every entry the
// spec assigns is logged, and its check.json says so, best-effort. bucket is
// nil when the failure happened before the runs bucket was opened.
func catalogFailAll(cmd *cobra.Command, ctx context.Context, lg *catalogCheckLogger, e catalogJobEnv, bucket *blobx.Bucket, fail error, now time.Time, dryRun bool) error {
	msg := oneLine(fail.Error())
	for _, r := range e.spec.Repositories {
		slug, slugErr := task.Slug(r.Provider, r.Repo)
		for _, name := range r.Entries {
			lg.log(name, r.Repo, imagecheck.CheckFailed, nil, "", "", msg)
			if bucket == nil || dryRun || slugErr != nil {
				continue
			}
			key := imagecheck.CatalogCheckKey(slug, name)
			var ev catalogEvaluation
			var err error
			ev.prevRaw, ev.prevGen, err = bucket.Read(ctx, key)
			ev.prevExists = err == nil
			if ev.prevExists {
				if ev.prev, err = imagecheck.ParseCatalogCheckState(ev.prevRaw); err != nil {
					continue // left for a human, as evaluate leaves it
				}
			}
			state := catalogNextState(ev.prev, imagecheck.CatalogDecision{Entry: name, Decision: imagecheck.CheckFailed, Error: msg}, "", now)
			if werr := catalogWriteState(ctx, bucket, key, state, ev); werr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", oneLine(werr.Error()))
			}
		}
	}
	return remote(fmt.Errorf("the catalog check failed: %s", msg))
}

// catalogWriteState writes an entry's check.json over what evaluate read,
// generation-matched, the way writeState writes a workflow's.
func catalogWriteState(ctx context.Context, b *blobx.Bucket, key string, s *imagecheck.CatalogCheckState, ev catalogEvaluation) error {
	data, err := s.Marshal()
	if err != nil {
		return err
	}
	return writeStateAt(ctx, b, key, data, ev.prevExists, ev.prevGen, ev.prevRaw)
}
