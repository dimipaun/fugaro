package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// What the check job talks to. Production leaves them as they are; tests
// point them at fakes.
var (
	// checkOpenBucket opens the runs bucket (FUGARO_BUCKET).
	checkOpenBucket = blobx.Open
	// checkEndpoints are the check job's API endpoints (the real ones).
	checkEndpoints gcp.Endpoints
	// checkRegistry reads image digests: Artifact Registry with ADC, any
	// other registry anonymously.
	checkRegistry = func() *imagecheck.Registry { return &imagecheck.Registry{} }
	// checkLocalEnv is git's environment for the local check's clone.
	checkLocalEnv = os.Environ
)

// notInstalledReason is the reason a workflow head's fugaro.yaml and the
// installed check job disagree about gets.
const notInstalledReason = "run fugaro init --repo"

type imageCheckOptions struct {
	cloud                      cloudOptions
	workflow                   string
	job, force, dryRun, asJSON bool
}

func newImageCheckCmd() *cobra.Command {
	var o imageCheckOptions
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Decide whether each workflow's image needs a rebuild (the daily check; locally it only prints the decision)",
		Long: "Evaluate the rebuild triggers of the repository's workflows against the head of\n" +
			"its base branch: a blobless clone, the build record in the runs bucket, and the\n" +
			"base image's digest.\n\n" +
			"Run from a checkout, it uses your own git access and ADC and only prints each\n" +
			"workflow's decision: it never submits a build (--dry-run is accepted, and is what\n" +
			"it always does). Start a rebuild with fugaro image build.\n\n" +
			"With --job, as the repository's daily check job runs it (it refuses to submit\n" +
			"unless CLOUD_RUN_JOB or CLOUD_RUN_EXECUTION is set, as Cloud Run sets them, and\n" +
			"in a coding agent's session; --dry-run is always allowed), it reads the job's\n" +
			"spec from " + infra.CheckSpecEnv + ", submits a Cloud Build rebuild when a trigger fires\n" +
			"(not with --dry-run), writes builds/<slug>/<workflow>/check.json, and logs one\n" +
			"JSON line per workflow. It exits 2 when the check itself failed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.job {
				return runImageCheckJob(cmd, o)
			}
			return runImageCheckLocal(cmd, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.job, "job", false, "run as the repository's daily check job (reads "+infra.CheckSpecEnv+"; submits rebuilds)")
	f.StringVar(&o.workflow, "workflow", "", "check only this workflow")
	f.BoolVar(&o.force, "force", false, "decide rebuild whatever the triggers say (with --job: a build is submitted)")
	f.BoolVar(&o.dryRun, "dry-run", false, "never submit a build (locally the check never does anyway)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output (locally; the job always logs JSON)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// checkTarget is one repository's check: what every workflow's decision
// is made against.
type checkTarget struct {
	slug     string
	bucket   *blobx.Bucket
	builder  *gcp.Builder
	registry *imagecheck.Registry
	tree     *imagecheck.GitTree
	cfg      *config.Config // head's fugaro.yaml
	rs       infra.RepoSpec
	// bases are the base images a build is given, by base kind, and
	// baseDigests their digests now (a kind is missing when its digest
	// couldn't be read).
	bases, baseDigests map[string]string
	// salt is the template salt of a build this check would submit; ""
	// leaves it out of the comparison.
	salt  string
	force bool
	now   time.Time
	warn  func(string)
}

// readBaseDigests reads the digest of each base image, leaving out (with a
// warning) the kind whose digest can't be read: its base trigger is skipped.
func (c *checkTarget) readBaseDigests(ctx context.Context) {
	c.baseDigests = map[string]string{}
	for _, kind := range slices.Sorted(maps.Keys(c.bases)) {
		d, err := c.registry.Digest(ctx, c.bases[kind])
		if err != nil {
			c.warn(fmt.Sprintf("could not read the %s base image's digest (%s); its base trigger is skipped", kind, oneLine(err.Error())))
			continue
		}
		c.baseDigests[kind] = d
	}
}

// evaluation is one workflow's decision and what it was made from.
type evaluation struct {
	d          imagecheck.Decision
	prev       *imagecheck.CheckState
	prevRaw    []byte
	prevGen    int64
	prevExists bool
	// prevUnreadable: check.json exists but doesn't parse. It is left as
	// it is, for a human to look at.
	prevUnreadable bool
	// lastStatus is the status of prev's build as read now, "" if none.
	lastStatus string
}

// evaluate reads what workflow name's decision needs and decides. An
// error is a failure to read, which the caller reports as check-failed.
func (c *checkTarget) evaluate(ctx context.Context, name string) (evaluation, error) {
	var ev evaluation
	recKey := imagecheck.RecordKey(c.slug, name)
	var rec *imagecheck.Record
	data, _, err := c.bucket.Read(ctx, recKey)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
	case err != nil:
		return ev, fmt.Errorf("reading %s: %w", recKey, err)
	default:
		if rec, err = imagecheck.ParseRecord(data); err != nil {
			// The build's gate replaces an unreadable record.
			c.warn(fmt.Sprintf("%s is unreadable (%v); treating it as missing", recKey, err))
			rec = nil
		}
	}
	checkKey := imagecheck.CheckKey(c.slug, name)
	ev.prevRaw, ev.prevGen, err = c.bucket.Read(ctx, checkKey)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
	case err != nil:
		return ev, fmt.Errorf("reading %s: %w", checkKey, err)
	default:
		ev.prevExists = true
		if ev.prev, err = imagecheck.ParseCheckState(ev.prevRaw); err != nil {
			// It holds the back-off state: without it a build known to fail
			// could be submitted again. Fail safe, and leave it for a human.
			ev.prevUnreadable = true
			return ev, fmt.Errorf("%s is unreadable (%v), and holds the last rebuild's state: nothing is submitted until it is fixed or deleted", checkKey, err)
		}
	}
	if p := ev.prev; p != nil && p.BuildID != "" {
		if imagecheck.FinalBuild(p.LastBuildStatus) {
			ev.lastStatus = p.LastBuildStatus
		} else {
			ev.lastStatus, err = c.builder.Status(ctx, p.BuildID)
			switch {
			case errors.Is(err, gcp.ErrBuildNotFound):
				c.warn(fmt.Sprintf("the last rebuild %s of %s is unknown to Cloud Build", p.BuildID, name))
				ev.lastStatus = ""
			case err != nil:
				return ev, err
			}
		}
	}
	kind := c.cfg.Workflows[name].Base
	in := imagecheck.Inputs{
		Config: c.cfg, Workflow: name, Record: rec, LastCheck: ev.prev, LastBuildStatus: ev.lastStatus,
		Head: c.tree.Head(), Tree: c.tree, Changed: c.tree.Changed,
		BaseRef: c.bases[kind], BaseDigest: c.baseDigests[kind], TemplateSalt: c.salt,
		Now: c.now, Rebuild: c.cfg.Workflows[name].Rebuild.Defaults(), Force: c.force,
	}
	image := gcp.ImageName(c.rs.RegistryPath, c.slug, name)
	switch d, err := c.registry.Digest(ctx, image+":latest"); {
	case errors.Is(err, imagecheck.ErrManifestUnknown):
		in.LatestMissing = true
	case err != nil:
		c.warn(fmt.Sprintf("could not read %s:latest (%s)", image, oneLine(err.Error())))
	default:
		in.LatestDigest = d
	}
	if rec != nil {
		if in.BuiltCommitReachable, err = c.tree.IsAncestor(rec.SourceCommit); err != nil {
			return ev, fmt.Errorf("is the built commit %s on the branch: %w", rec.SourceCommit, err)
		}
	}
	ev.d = imagecheck.Decide(ctx, in)
	return ev, nil
}

// checkedWorkflows are head's workflows whose rebuild.check is not off.
func checkedWorkflows(cfg *config.Config) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		if cfg.Workflows[name].Rebuild.Defaults().Check != "off" {
			out = append(out, name)
		}
	}
	return out
}

// readHeadConfig reads head's fugaro.yaml and resolves it over the project
// layer o finds (docs/design/layered-config.md §8): the check job passes
// its copy (jobHeadConfig); fugaro image check run locally reads the
// published layer with the operator's access (lc).
func readHeadConfig(ctx context.Context, tree *imagecheck.GitTree, lc *localcfg.Config, o layerOptions) (*config.Config, error) {
	data, err := tree.ReadFile("fugaro.yaml")
	if err != nil {
		return nil, fmt.Errorf("the base branch's fugaro.yaml: %w", err)
	}
	return resolveHeadData(ctx, data, lc, o)
}

// resolveHeadData is readHeadConfig's resolve step, split out so
// jobHeadConfig can read the layer copy only once it knows whether head's
// file is even anchored, without reading fugaro.yaml from tree twice.
func resolveHeadData(ctx context.Context, data []byte, lc *localcfg.Config, o layerOptions) (*config.Config, error) {
	rf, err := resolveFugaroYAML(ctx, data, lc, o)
	if err != nil {
		return nil, err
	}
	if len(rf.Problems) > 0 {
		msgs := make([]string, len(rf.Problems))
		for i, p := range rf.Problems {
			msgs[i] = p.String()
		}
		return nil, fmt.Errorf("the base branch's fugaro.yaml has %d problem(s): %s", len(rf.Problems), strings.Join(msgs, "; "))
	}
	return rf.Cfg, nil
}

// jobLayerOptions are the check job's: its repository's copy of the project
// layer (decision L6), in the build account's own prefix, read with the
// ordinary read, not the strict variant: the account's objectUser grant is
// conditioned on the builds/<slug>/ prefix, so it lacks
// storage.objects.list, and GCS answers a GET of a genuinely missing
// object with 403 for that reason alone (blobx.ReadMax, and
// TestReadTreatsForbiddenAsAbsent, map any 403 to ErrNotExist for exactly
// this account shape) — there is no live 403 case this switch could still
// distinguish from "missing" by using ReadMaxStrict instead, and doing so
// would misreport "no layer published" as a failure for every repository
// without one. An oversized copy (over LayerMaxBytes) is always an error:
// it is present and published, so it never reads as "no layer", and names
// the fix (republish within the limit). The caller (jobHeadConfig) must
// call this only once it knows head's own file is anchored: an unanchored
// repository's check must never touch builds/<slug>/project-layer.yaml at
// all, so a copy broken for an unrelated reason can't fail a check that
// was never going to use it.
func jobLayerOptions(ctx context.Context, b *blobx.Bucket, slug string) (layerOptions, error) {
	key := config.LayerCopyKey(slug)
	data, _, err := b.ReadMax(ctx, key, config.LayerMaxBytes)
	switch {
	case err == nil:
		return layerOptions{Data: data, Where: key, NoBucket: true}, nil
	case errors.Is(err, blobx.ErrNotExist):
		return layerOptions{NoBucket: true}, nil
	case errors.Is(err, blobx.ErrTooLarge):
		return layerOptions{}, fmt.Errorf("the project layer copy %s is over the %d KiB limit: run fugaro config publish to republish it within the limit", key, config.LayerMaxBytes>>10)
	}
	return layerOptions{}, fmt.Errorf("reading the project layer copy %s: %w", key, err)
}

// jobHeadConfig is the check job's readHeadConfig: head's fugaro.yaml,
// resolved over its repository's copy of the project layer
// (jobLayerOptions) — read only when head's own file is anchored
// (decision L9, anchorOf): an unanchored repository is resolved exactly as
// it always was, with no copy ever read for it.
//
// A missing copy of an anchored file is not always an error: a build
// without a layer never touches one (decision L6), so "no copy" can be a
// repository whose project simply never published a layer, same as
// before this file was anchored at all. The file is first resolved
// without a layer: one that cannot resolve that way (a minimal file with
// no workflows: of its own, which needs the layer's default_profile)
// fails naming the fix, instead of the harder to place "must define at
// least one workflow"; one that resolves fine without a layer (its own
// full workflows:) goes on exactly as before, past one warn line, so a
// project that meant to publish a layer but whose copy went missing is
// never silent about it. Every other copy outcome (found, invalid,
// oversized, unreadable) behaves exactly as readHeadConfig's did.
func jobHeadConfig(ctx context.Context, bucket *blobx.Bucket, slug string, tree *imagecheck.GitTree, warn func(string)) (*config.Config, error) {
	data, err := tree.ReadFile("fugaro.yaml")
	if err != nil {
		return nil, fmt.Errorf("the base branch's fugaro.yaml: %w", err)
	}
	if !isAnchoredFile(data) {
		return resolveHeadData(ctx, data, nil, layerOptions{NoBucket: true})
	}
	lo, err := jobLayerOptions(ctx, bucket, slug)
	if err != nil {
		return nil, err
	}
	if lo.Data != nil {
		return resolveHeadData(ctx, data, nil, lo)
	}
	// jobLayerOptions found no copy (ErrNotExist, decision L6).
	key := config.LayerCopyKey(slug)
	cfg, rerr := resolveHeadData(ctx, data, nil, layerOptions{NoBucket: true})
	if rerr != nil {
		return nil, fmt.Errorf("no project layer copy at %s: run fugaro config publish", key)
	}
	warn(fmt.Sprintf("no project layer copy at %s: if the project publishes a layer, run fugaro config publish; this check ran without it", key))
	return cfg, nil
}

// checkLogLine is the check job's log line for one workflow: Cloud Run
// takes a JSON line on stdout as a structured entry, with its severity.
type checkLogLine struct {
	Severity        string   `json:"severity"`
	Event           string   `json:"event"`
	Repo            string   `json:"repo"`
	Workflow        string   `json:"workflow"`
	Decision        string   `json:"decision"`
	Reasons         []string `json:"reasons"`
	BuildID         string   `json:"build_id"`
	LastBuildStatus string   `json:"last_build_status,omitempty"`
	Error           string   `json:"error,omitempty"`
	DryRun          bool     `json:"dry_run,omitempty"`
	Message         string   `json:"message"`
}

type checkLogger struct {
	w      io.Writer
	repo   string
	dryRun bool
}

// log writes one workflow's line. rebuild-failed-last, check-failed and a
// last rebuild that failed are ERROR, which the alert matches.
func (l *checkLogger) log(workflow, decision string, reasons []string, buildID, lastStatus, errMsg string) {
	sev := "INFO"
	switch {
	case decision == imagecheck.RebuildFailedLast || decision == imagecheck.CheckFailed,
		// A failed last rebuild matters while a rebuild is still due;
		// once a later build cleared the triggers (skip), it is history.
		decision == imagecheck.Rebuild && imagecheck.FailedBuild(lastStatus):
		sev = "ERROR"
	case decision == imagecheck.NotInstalled:
		sev = "WARNING"
	}
	msg := l.repo + " " + workflow + ": " + decision
	if len(reasons) > 0 {
		msg += " (" + strings.Join(reasons, ", ") + ")"
	}
	switch {
	case errMsg != "":
		msg += ": " + errMsg
	case decision == imagecheck.Rebuild && buildID != "" && !l.dryRun:
		msg += "; submitted Cloud Build build " + buildID
	case decision == imagecheck.RebuildFailedLast:
		msg += "; the last rebuild " + buildID + " ended " + lastStatus + " on these same inputs without clearing the triggers, so it is not submitted again until something changes (fugaro image check --force overrides)"
	}
	line := checkLogLine{
		Severity: sev, Event: "image-check", Repo: l.repo, Workflow: workflow, Decision: decision,
		Reasons: nonNil(reasons), BuildID: buildID, LastBuildStatus: lastStatus, Error: errMsg, DryRun: l.dryRun, Message: msg,
	}
	data, err := json.Marshal(line)
	if err != nil {
		data = []byte(`{"severity":"ERROR","event":"image-check","message":"unloggable line"}`)
	}
	fmt.Fprintln(l.w, string(data))
}

// nextState is the check.json after decision d: the last submitted build
// and its inputs carry over from prev.
func nextState(prev *imagecheck.CheckState, d imagecheck.Decision, lastStatus string, now time.Time) *imagecheck.CheckState {
	s := &imagecheck.CheckState{Version: imagecheck.CheckVersion, CheckedAt: now, Decision: d.Decision,
		Reasons: nonNil(d.Reasons), Error: d.Error}
	if prev != nil {
		s.BuildID, s.BuildInputs, s.LastBuildAt = prev.BuildID, prev.BuildInputs, prev.LastBuildAt
		s.LastBuildStatus = cmp.Or(lastStatus, prev.LastBuildStatus)
	}
	return s
}

// writeState writes s over what evaluate read, generation-matched: a
// concurrent check's write makes this one fail rather than be lost.
func writeState(ctx context.Context, b *blobx.Bucket, key string, s *imagecheck.CheckState, ev evaluation) error {
	data, err := s.Marshal()
	if err != nil {
		return err
	}
	if ev.prevExists {
		_, err = b.ReplaceIf(ctx, key, data, ev.prevGen, ev.prevRaw)
	} else {
		_, err = b.Create(ctx, key, data, "application/json")
	}
	if errors.Is(err, blobx.ErrConflict) || errors.Is(err, blobx.ErrExists) {
		return fmt.Errorf("%s changed while this check ran (another check?); not overwriting it", key)
	}
	if errors.Is(err, blobx.ErrForbidden) {
		return checkWriteErr("gs://"+b.GCSName, key, err)
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// jobEnv are the check job's own variables, as the repository's spec
// sets them.
type jobEnv struct {
	spec                       infra.CheckJobSpec
	name                       string // the Fugaro project, FUGARO_PROJECT
	gcpProject, region, bucket string // bucket: the runs bucket's name
}

func readJobEnv() (jobEnv, error) {
	var e jobEnv
	raw := os.Getenv(infra.CheckSpecEnv)
	if raw == "" {
		return e, fmt.Errorf("%s is not set: fugaro image check --job runs in the repository's check job, which fugaro init --repo sets up", infra.CheckSpecEnv)
	}
	if err := json.Unmarshal([]byte(raw), &e.spec); err != nil {
		return e, fmt.Errorf("%s: %w", infra.CheckSpecEnv, err)
	}
	s := e.spec
	if s.Repo == "" || s.Provider == "" || s.RepoURL == "" || s.BaseBranch == "" || len(s.Workflows) == 0 ||
		s.Registry == "" || s.BuildServiceAccount == "" || s.BuildRegion == "" || len(s.BaseImages) == 0 {
		return e, fmt.Errorf("%s is incomplete: run fugaro init --repo", infra.CheckSpecEnv)
	}
	e.name, e.gcpProject, e.region = os.Getenv("FUGARO_PROJECT"), os.Getenv("FUGARO_GCP_PROJECT"), os.Getenv("FUGARO_REGION")
	u, err := url.Parse(os.Getenv("FUGARO_BUCKET"))
	if e.name == "" || e.gcpProject == "" || e.region == "" || err != nil || u.Scheme != "gs" || u.Host == "" || strings.Trim(u.Path, "/") != "" {
		return e, errors.New("FUGARO_PROJECT (the project's name), FUGARO_GCP_PROJECT, FUGARO_REGION and FUGARO_BUCKET (gs://<runs bucket>) must be set, as the check job's spec sets them; run fugaro init --repo")
	}
	e.bucket = u.Host
	return e, nil
}

// repoSpec is the repository's spec as this job's own environment gives
// it: the names come from Go, as fugaro init --repo gave them, and the
// project's name is the job's FUGARO_PROJECT.
func (e jobEnv) repoSpec(cfg *config.Config) (infra.RepoSpec, *localcfg.Config, error) {
	s := e.spec
	host, _, _ := strings.Cut(s.Registry, "/"+e.gcpProject+"/")
	// The spec names the base of the kinds the job was installed for. A kind
	// head's fugaro.yaml has since added has no base here, and its workflow
	// is reported as not installed rather than checked, so the job's own
	// image stands in for the spec's sake.
	bases := maps.Clone(s.BaseImages)
	stand := bases[slices.Sorted(maps.Keys(bases))[0]]
	for _, w := range cfg.Workflows {
		if bases[w.Base] == "" {
			bases[w.Base] = stand
		}
	}
	lc := &localcfg.Config{
		Version: 1, Name: e.name, GCPProject: e.gcpProject, Region: e.region, RunsBucket: e.bucket, BaseImages: bases,
		Build: localcfg.Build{MachineType: s.MachineType, Region: s.BuildRegion},
		Repos: map[string]localcfg.Repo{s.Repo: {Provider: s.Provider, BaseBranch: s.BaseBranch, GitHubAppID: os.Getenv(providers.EnvGitHubAppID)}},
	}
	rs, err := infra.Repo(infra.Inputs{LC: lc, Repo: s.Repo, Cfg: cfg, RepoURL: s.RepoURL,
		Installation: infra.InstallationOutputs{RegistryHost: host + "/" + e.gcpProject}})
	return rs, lc, err
}

// jobGitEnv is git's environment in the check job: the job's own, less
// the provider secret, plus a credential for the repository's host only.
// A GitHub App mints a token that can only read the repository.
func jobGitEnv(ctx context.Context, s infra.CheckJobSpec) ([]string, error) {
	if err := gcp.CheckRepoURL(s.Provider, s.RepoURL); err != nil {
		return nil, err
	}
	var user, token string
	switch s.Provider {
	case gitprov.KindBitbucket:
		user, token = bitbucket.GitUsername, os.Getenv(providers.EnvBitbucketToken)
		if token == "" {
			return nil, fmt.Errorf("%s is not set: the job's bitbucket-token secret isn't mounted (does it have a version?)", providers.EnvBitbucketToken)
		}
	case gitprov.KindGitHub:
		owner, repo, ok := gitprov.SplitRepo(s.Repo)
		appID, pemKey := os.Getenv(providers.EnvGitHubAppID), os.Getenv(providers.EnvGitHubAppKey)
		if !ok || appID == "" || pemKey == "" {
			return nil, fmt.Errorf("%s and %s must both be set: the job's github-app-key secret isn't mounted (does it have a version?)", providers.EnvGitHubAppID, providers.EnvGitHubAppKey)
		}
		key, err := github.ParsePrivateKey([]byte(pemKey))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", providers.EnvGitHubAppKey, err)
		}
		if token, _, err = github.MintInstallationToken(ctx, github.Options{Owner: owner, Repo: repo, AppID: appID, PrivateKey: key, BaseURL: gitCredGitHubAPI},
			github.BuildTokenPermissions()); err != nil {
			return nil, err
		}
		user = github.GitUsername
	default:
		return nil, fmt.Errorf("the git provider %q is not %s or %s", s.Provider, gitprov.KindBitbucket, gitprov.KindGitHub)
	}
	vars, err := gitops.CredentialVars(gitops.CredentialURL(s.RepoURL), user, token)
	if err != nil {
		return nil, err
	}
	vars["GIT_TERMINAL_PROMPT"] = "0"
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return k == providers.EnvBitbucketToken || k == providers.EnvGitHubAppKey
	})
	return gitops.WithVars(env, vars), nil
}

// runImageCheckJob is fugaro image check --job.
func runImageCheckJob(cmd *cobra.Command, o imageCheckOptions) error {
	ctx := cmd.Context()
	// The job submits billable rebuilds with no one to type: it runs in Cloud
	// Run, where CLOUD_RUN_JOB (or CLOUD_RUN_EXECUTION) is set and no coding
	// agent's marker is. Anywhere else, or in a session with an agent marker,
	// it submits nothing (--dry-run still decides). Not a barrier: an
	// environment can be set by hand.
	if !o.dryRun {
		if m := agentMarker(os.Getenv); m != "" {
			return userErr("%s", initflow.AgentRefusal(m))
		}
		if os.Getenv("CLOUD_RUN_JOB") == "" && os.Getenv("CLOUD_RUN_EXECUTION") == "" {
			return userErr("fugaro image check --job submits billable rebuilds and runs only as the repository's Cloud Run job (CLOUD_RUN_JOB is not set here); from a checkout, fugaro image check only prints the decision, and fugaro image build starts a rebuild, which you confirm by typing")
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	lg := &checkLogger{w: cmd.OutOrStdout(), dryRun: o.dryRun}
	e, err := readJobEnv()
	if err != nil {
		lg.log("", imagecheck.CheckFailed, nil, "", "", err.Error())
		return remote(err)
	}
	s := e.spec
	lg.repo = s.Repo
	workflows := s.Workflows
	if o.workflow != "" {
		if !slices.Contains(workflows, o.workflow) {
			return userErr("the check job doesn't check workflow %q (it checks %s)", o.workflow, strings.Join(workflows, ", "))
		}
		workflows = []string{o.workflow}
	}
	slug, slugErr := task.Slug(s.Provider, s.Repo)

	// A failure before any workflow is decided fails all of them: each is
	// logged, and its check.json says so, best-effort.
	var bucket *blobx.Bucket
	failAll := func(err error) error {
		msg := oneLine(err.Error())
		for _, w := range workflows {
			lg.log(w, imagecheck.CheckFailed, nil, "", "", msg)
			if bucket == nil || o.dryRun {
				continue
			}
			key := imagecheck.CheckKey(slug, w)
			var ev evaluation
			ev.prevRaw, ev.prevGen, err = bucket.Read(ctx, key)
			ev.prevExists = err == nil
			if ev.prevExists {
				if ev.prev, err = imagecheck.ParseCheckState(ev.prevRaw); err != nil {
					continue // left for a human, as evaluate leaves it
				}
			}
			state := nextState(ev.prev, imagecheck.Decision{Workflow: w, Decision: imagecheck.CheckFailed, Error: msg}, "", now)
			if err := writeState(ctx, bucket, key, state, ev); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: %s\n", oneLine(err.Error()))
			}
		}
		return remote(fmt.Errorf("the image check of %s failed: %s", s.Repo, msg))
	}
	if slugErr != nil {
		return failAll(slugErr)
	}
	if bucket, err = checkOpenBucket(ctx, "gs://"+e.bucket); err != nil {
		return failAll(err)
	}
	defer bucket.Close()
	gitEnv, err := jobGitEnv(ctx, s)
	if err != nil {
		return failAll(err)
	}
	tmp, err := os.MkdirTemp("", "fugaro-check-")
	if err != nil {
		return failAll(err)
	}
	defer os.RemoveAll(tmp)
	tree, err := imagecheck.Clone(ctx, imagecheck.CloneOptions{URL: s.RepoURL, Branch: s.BaseBranch, Dir: filepath.Join(tmp, "repo"), Env: gitEnv})
	if err != nil {
		return failAll(err)
	}
	cfg, err := jobHeadConfig(ctx, bucket, slug, tree, func(msg string) { fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", msg) })
	if err != nil {
		return failAll(err)
	}
	rs, lc, err := e.repoSpec(cfg)
	if err != nil {
		return failAll(err)
	}
	// The job's spec came from the names Go gave at fugaro init --repo;
	// this fugaro must give the same ones, or it would build elsewhere.
	if rs.RegistryPath != s.Registry || rs.BuildServiceAccountEmail != s.BuildServiceAccount {
		return failAll(fmt.Errorf("the installed check job names the registry %s and build account %s, but this fugaro names %s and %s: %s",
			s.Registry, s.BuildServiceAccount, rs.RegistryPath, rs.BuildServiceAccountEmail, notInstalledReason))
	}
	builder, err := gcp.NewBuilder(ctx, gcp.Options{GCPProject: e.gcpProject, Region: e.region, Endpoints: checkEndpoints}, s.BuildRegion)
	if err != nil {
		return failAll(err)
	}
	t := &checkTarget{
		slug: slug, bucket: bucket, builder: builder, registry: checkRegistry(), tree: tree, cfg: cfg, rs: rs,
		bases: s.BaseImages, salt: gcp.TemplateSalt(Version), force: o.force, now: now,
		warn: func(msg string) { fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", msg) },
	}
	t.readBaseDigests(ctx)

	// Head's workflows the job doesn't check, though head says it should.
	for _, name := range checkedWorkflows(cfg) {
		if !slices.Contains(s.Workflows, name) && (o.workflow == "" || o.workflow == name) {
			lg.log(name, imagecheck.NotInstalled, []string{notInstalledReason}, "", "", "")
		}
	}
	var failed []string
	for _, name := range workflows {
		if !slices.Contains(checkedWorkflows(cfg), name) {
			// Gone from head, or its check turned off: the installed
			// schedule no longer matches, so nothing is built.
			lg.log(name, imagecheck.NotInstalled, []string{notInstalledReason}, "", "", "")
			continue
		}
		ev, err := t.evaluate(ctx, name)
		if err != nil {
			ev.d = imagecheck.Decision{Workflow: name, Decision: imagecheck.CheckFailed, Error: oneLine(err.Error())}
		}
		state := nextState(ev.prev, ev.d, ev.lastStatus, now)
		if ev.d.Decision == imagecheck.Rebuild && !o.dryRun {
			id, status, err := submitRebuild(ctx, builder, e.gcpProject, rs, cfg, name, s, lc.RecordBucketURL())
			if err != nil {
				ev.d.Decision, ev.d.Error = imagecheck.CheckFailed, "submitting the rebuild: "+oneLine(err.Error())
				state = nextState(ev.prev, ev.d, ev.lastStatus, now)
			} else {
				inputs := ev.d.Inputs
				state.BuildID, state.BuildInputs, state.LastBuildStatus, state.LastBuildAt = id, &inputs, status, &now
			}
		}
		if !o.dryRun && !ev.prevUnreadable {
			if err := writeState(ctx, bucket, imagecheck.CheckKey(slug, name), state, ev); err != nil {
				msg := oneLine(err.Error())
				if ev.d.Decision == imagecheck.Rebuild && state.BuildID != "" {
					// The build runs, but the next check can't back off from
					// it or wait for it: say which build it is.
					msg = "submitted Cloud Build build " + state.BuildID + ", but could not record it in check.json: " + msg
				}
				ev.d.Decision, ev.d.Error = imagecheck.CheckFailed, cmp.Or(ev.d.Error, msg)
			}
		}
		lg.log(name, ev.d.Decision, ev.d.Reasons, state.BuildID, ev.lastStatus, ev.d.Error)
		if ev.d.Decision == imagecheck.CheckFailed {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return remote(fmt.Errorf("the image check of %s failed for %s", s.Repo, strings.Join(failed, ", ")))
	}
	return nil
}

// submitRebuild submits workflow name's rebuild, the request fugaro image
// build sends, generated now by this fugaro. The check never skips the
// smoke test: a request without it is refused before it is sent.
func submitRebuild(ctx context.Context, b *gcp.Builder, project string, rs infra.RepoSpec, cfg *config.Config, name string, s infra.CheckJobSpec, recordBucket string) (id, status string, err error) {
	kind := cfg.Workflows[name].Base
	base := s.BaseImages[kind]
	if base == "" {
		return "", "", fmt.Errorf("the installed check job has no %s base image: %s", kind, notInstalledReason)
	}
	spec, err := cloudBuildSpec(rs, cfg, name, base, s.MachineType, recordBucket)
	if err != nil {
		return "", "", err
	}
	req, err := gcp.BuildRequest(project, spec)
	if err != nil {
		return "", "", err
	}
	if !slices.ContainsFunc(req.Steps, func(st *cloudbuild.BuildStep) bool { return st.Id == "smoke" }) {
		return "", "", errors.New("the build request has no smoke step")
	}
	res, err := b.Submit(ctx, spec)
	if err != nil {
		return "", "", err
	}
	return res.ID, res.Status, nil
}

// runImageCheckLocal is fugaro image check from a checkout: the same
// triggers, with the operator's git access and ADC. It only prints.
func runImageCheckLocal(cmd *cobra.Command, o imageCheckOptions) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	lc := env.lc
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
	}
	// Passes lc, already selected from --config/--project/--gcp-project
	// (openCloud, above): loadCheckoutConfig's own flag-blind
	// selectedProjectConfig fallback could pick a different installation
	// (or none), which would read the wrong project layer bucket, or none
	// at all, and resolve this file against a stale cache or "unknown"
	// instead of the one this command actually points at.
	_, rf, err := loadCheckoutResolved(ctx, "", lc, layerOptions{Lenient: true})
	if err != nil {
		return err
	}
	checkoutCfg := rf.Cfg
	for _, name := range checkedWorkflows(checkoutCfg) {
		if kind := checkoutCfg.Workflows[name].Base; lc.BaseImage(kind) == "" {
			return userErr("the local config has no base_images.%s, which the daily check builds %s from; set it first", kind, name)
		}
	}
	repo, err := originRepo(ctx)
	if err != nil {
		return err
	}
	repoURL, err := originURL(ctx)
	if err != nil {
		return err
	}
	rs0, err := infra.Repo(infra.Inputs{LC: lc, Repo: repo, Cfg: checkoutCfg, RepoURL: repoURL})
	if err != nil {
		return userErr("%v", err)
	}
	cloneURL, err := rawOrigin(ctx)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "fugaro-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	branch := cmp.Or(rs0.BaseBranch, "main")
	gitEnv := gitops.WithVars(checkLocalEnv(), map[string]string{"GIT_TERMINAL_PROMPT": "0"})
	tree, err := imagecheck.Clone(ctx, imagecheck.CloneOptions{URL: cloneURL, Branch: branch, Dir: filepath.Join(tmp, "repo"), Env: gitEnv})
	if err != nil {
		return remote(fmt.Errorf("%w (the check clones origin's %s with your own git access)", err, branch))
	}
	cfg, err := readHeadConfig(ctx, tree, lc, layerOptions{})
	if err != nil {
		return userErr("%v", err)
	}
	rs, err := infra.Repo(infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, RepoURL: repoURL})
	if err != nil {
		return userErr("%v", err)
	}
	workflows := checkedWorkflows(cfg)
	if o.workflow != "" {
		if _, ok := cfg.Workflows[o.workflow]; !ok {
			return userErr("the base branch's fugaro.yaml has no workflow %q", o.workflow)
		}
		workflows = []string{o.workflow}
	}
	builder, err := gcp.NewBuilder(ctx, env.gcp, lc.BuildRegion())
	if err != nil {
		return remote(err)
	}
	records, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	t := &checkTarget{
		slug: rs.Slug, bucket: records, builder: builder, registry: checkRegistry(), tree: tree, cfg: cfg, rs: rs,
		// The job's template salt is its base image's fugaro's, which
		// this fugaro can't know; the salt is left out here.
		bases: lc.BaseImages, force: o.force, now: time.Now().UTC(),
		warn: func(msg string) { fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", msg) },
	}
	t.readBaseDigests(ctx)
	type result struct {
		imagecheck.Decision
		LastBuildID     string `json:"last_build_id,omitempty"`
		LastBuildStatus string `json:"last_build_status,omitempty"`
	}
	var results []result
	failed := false
	for _, name := range workflows {
		ev, err := t.evaluate(ctx, name)
		if err != nil {
			ev.d = imagecheck.Decision{Workflow: name, Decision: imagecheck.CheckFailed, Error: oneLine(err.Error())}
		}
		r := result{Decision: ev.d, LastBuildStatus: ev.lastStatus}
		r.Reasons = nonNil(r.Reasons)
		if ev.prev != nil {
			r.LastBuildID = ev.prev.BuildID
		}
		results = append(results, r)
		failed = failed || ev.d.Decision == imagecheck.CheckFailed
	}
	w := cmd.OutOrStdout()
	if o.asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		ds := make([]result, len(results))
		copy(ds, results)
		if err := enc.Encode(map[string]any{"project": lc.Name, "repo": repo, "head": tree.Head(), "decisions": ds}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "%s at %s (%s):\n", oneLine(repo), oneLine(tree.Head()), oneLine(branch))
		for _, r := range results {
			line := r.Workflow + ": " + r.Decision.Decision
			if len(r.Reasons) > 0 {
				line += " (" + strings.Join(r.Reasons, ", ") + ")"
			}
			switch r.Decision.Decision {
			case imagecheck.CheckFailed:
				line += ": " + r.Error
			case imagecheck.Rebuild:
				line += "; the daily check would submit a rebuild. This command never builds: fugaro image build --workflow " + r.Workflow + " starts one (billable)"
			case imagecheck.RebuildFailedLast:
				line += "; the last rebuild " + r.LastBuildID + " ended " + r.LastBuildStatus + " on these same inputs without clearing the triggers, so the daily check won't submit it again until something changes"
			}
			fmt.Fprintln(w, "  "+oneLine(line))
		}
	}
	if failed {
		return remote(errors.New("the image check failed for at least one workflow"))
	}
	return nil
}

// rawOrigin is the checkout's origin URL as git has it, which the local
// check clones with the operator's own git access.
func rawOrigin(ctx context.Context) (string, error) {
	c := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	c.WaitDelay = 5 * time.Second
	out, err := c.Output()
	if err != nil {
		return "", userErr("no origin remote in this checkout")
	}
	return strings.TrimSpace(string(out)), nil
}

// nonNil is ss, or an empty list for nil, so JSON says [] not null.
func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// imageStatusOut is fugaro image status --json.
type imageStatusOut struct {
	Project   string           `json:"project"` // the Fugaro project
	Workflows []imageStatusRow `json:"workflows"`
}

type imageStatusRow struct {
	Repo     string `json:"repo"`
	Workflow string `json:"workflow"`
	Slug     string `json:"slug"`
	imagecheck.Status
}

func newImageStatusCmd() *cobra.Command {
	var (
		o      cloudOptions
		repo   string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show each workflow's image: when it was built and from what, and its last daily check",
		Long: "For each workflow of the local config's repositories (or --repo's), print what\n" +
			"the runs bucket records: the image's build time and age, source commit and base\n" +
			"digest (builds/<slug>/<workflow>/image.json), and the last daily check's time,\n" +
			"decision and reasons, and the last rebuild's status (check.json).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runImageStatus(cmd, o, repo, asJSON) },
	}
	cmd.Flags().StringVar(&repo, "repo", "", "only this repository (owner/name, as the local config's repos has it)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o)
	return cmd
}

func runImageStatus(cmd *cobra.Command, o cloudOptions, only string, asJSON bool) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o)
	if err != nil {
		return err
	}
	defer env.Close()
	repos := slices.Sorted(maps.Keys(env.lc.Repos))
	if only != "" {
		if _, ok := env.localRepo(only); !ok {
			return userErr("%s is not in the local config's repos; fugaro init --repo adds it", only)
		}
		repos = []string{only}
	}
	records, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	now := time.Now().UTC()
	out := imageStatusOut{Project: env.lc.Name, Workflows: []imageStatusRow{}}
	for _, repo := range repos {
		local, _ := env.localRepo(repo)
		slug, err := env.repoSlug(repo, func() *config.Config { return checkoutConfig(ctx, repo) })
		if err != nil {
			return err
		}
		workflows := local.Workflows
		if len(workflows) == 0 {
			if cfg := checkoutConfig(ctx, repo); cfg != nil {
				workflows = slices.Sorted(maps.Keys(cfg.Workflows))
			}
		}
		for _, w := range workflows {
			st, err := imagecheck.ReadStatus(ctx, records, slug, w, now)
			if err != nil {
				return remote(err)
			}
			out.Workflows = append(out.Workflows, imageStatusRow{Repo: repo, Workflow: w, Slug: slug, Status: st})
		}
	}
	w := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, r := range out.Workflows {
		fmt.Fprintf(w, "%s %s\n", oneLine(r.Repo), oneLine(r.Workflow))
		if im := r.Image; im != nil {
			fmt.Fprintf(w, "  image: built %s (%s ago) from commit %s, base %s, build %s\n",
				im.BuiltAt.Format(time.RFC3339), ageString(time.Duration(im.AgeS)*time.Second), oneLine(im.SourceCommit),
				oneLine(cmp.Or(im.BaseDigest, "unknown")), oneLine(cmp.Or(im.BuildID, "unknown")))
		} else {
			fmt.Fprintln(w, "  image: no record yet (fugaro image build writes the first one)")
		}
		if c := r.Check; c != nil {
			line := "  check: " + c.CheckedAt.Format(time.RFC3339) + " " + c.Decision
			if len(c.Reasons) > 0 {
				line += " (" + strings.Join(c.Reasons, ", ") + ")"
			}
			if c.Error != "" {
				line += ": " + c.Error
			}
			fmt.Fprintln(w, oneLine(line))
			if c.BuildID != "" {
				at := ""
				if c.LastBuildAt != nil {
					at = ", submitted " + c.LastBuildAt.Format(time.RFC3339)
				}
				fmt.Fprintf(w, "  last rebuild: %s %s%s\n", oneLine(c.BuildID), oneLine(cmp.Or(c.LastBuildStatus, "status unknown")), at)
			}
		} else {
			fmt.Fprintln(w, "  check: none yet (the daily schedule stays paused until the image has a record)")
		}
		for _, e := range r.Errors {
			fmt.Fprintf(w, "  warning: %s\n", oneLine(e))
		}
	}
	return nil
}

// ageString is d in whole hours under two days, else in days.
func ageString(d time.Duration) string {
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}
