package cli

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// openCatalogProvider is OpenProvider, a seam for the copy a catalog build
// makes before it submits: a repository-sourced definition is read with the
// project's existing credentials, which a test has no account for, so it
// hands the copy a file-backed provider here.
var openCatalogProvider = OpenProvider

// runImageBuildCatalogOrDerived is `fugaro image build` with no --workflow:
// with a catalog published, the entry the operator named (or every entry that
// has a Dockerfile) is built, and a project with no catalog and no name keeps
// the command's meaning exactly by going on to the per-workflow path
// (D1144).
func runImageBuildCatalogOrDerived(cmd *cobra.Command, name string, o imageBuildOptions) error {
	ctx := cmd.Context()
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	// A Cloud Build is billable: the project's name typed at a real
	// terminal, never --json, a pipe or a coding agent's session. A run that
	// can't take it is refused first, before the local config, the bucket or
	// anything else in the cloud is touched.
	conds := initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), JSON: o.asJSON, Agent: agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))}
	if !initflow.CanConfirm(initflow.Typed, conds) {
		return catalogBuildNotConfirmable(conds, o, name)
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	cat, catErr := effectiveCatalog(ctx, env)
	if catErr != nil {
		// The published node is what decides between two different things
		// here — the entry the operator named and the checkout's derived
		// image — so a node this machine cannot read is said out loud rather
		// than taken for an empty catalog.
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: could not read the project's published policy (%s), so this machine's own configuration is what says whether the project has an image catalog\n", oneLine(catErr.Error()))
	}
	if cat != nil && len(cat.Entries) > 0 {
		defer env.Close()
		for _, w := range env.lc.Warnings() {
			fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
		}
		return runImageBuildCatalog(cmd, o, env, cat, name, conds)
	}
	// No catalog and no name is the per-workflow build exactly, and that
	// path opens the cloud itself: this read is closed again rather than
	// passed on, which is what leaves it as it stands.
	env.Close()
	if name != "" {
		return userErr("this project has no image catalog, so there is no entry %s to build: fugaro image add %s writes one and fugaro update publishes it, while fugaro image build --workflow builds a workflow's derived image", pluginwire.Printable(name), pluginwire.Printable(name))
	}
	return runImageBuildCloud(cmd, o)
}

// catalogBuildNotConfirmable is the refusal of a Cloud Build that cannot be
// confirmed under conds, for the catalog arm: the same sentence the
// per-workflow build refuses with, routing the operator at the entry they
// named.
func catalogBuildNotConfirmable(conds initflow.Conditions, o imageBuildOptions, name string) error {
	if conds.Agent != "" {
		return userErr("%s", initflow.AgentRefusal(conds.Agent))
	}
	cmd := "fugaro image build"
	if o.repo != "" {
		cmd += " --repo " + quoteWord(o.repo)
	}
	if name != "" {
		cmd += " " + quoteWord(name)
	}
	return userErr("%s", buildNotConfirmableRoute(cmd))
}

// runImageBuildCatalog submits one Cloud Build per entry of cat (D1144):
// each reads the Dockerfile the entry's source resolved to and this command
// copied into the project's bucket, builds it as the checkout's repository's
// build account with that repository's provider credential and workflow
// secrets, and promotes the image into the installation's shared registry
// (D1152).
//
// Everything a build is named from — the build account, the credential, the
// secrets and the record's slug — is the checkout's repository's, so an
// entry whose Dockerfile lives in another repository is refused rather than
// submitted: its build would run as an account that does not hold that
// repository's credential. Building one from anywhere is a decision this
// command does not take on its own.
//
// conds is the reading runImageBuildCatalogOrDerived refused on — the terminal,
// --json and the coding agent's marker — and it is passed rather than read
// again so that the session this arm was admitted for and the session its
// refusal is scoped to are one reading of the same facts.
func runImageBuildCatalog(cmd *cobra.Command, o imageBuildOptions, env *cloudEnv, cat *config.Catalog, name string, conds initflow.Conditions) error {
	ctx := cmd.Context()
	lc := env.lc
	root, rf, err := loadCheckoutResolved(ctx, "", lc, layerOptions{})
	if err != nil {
		return err
	}
	cfg := rf.Cfg
	// A catalog entry is not a workflow (D1144), and nothing a catalog build
	// reads is per workflow: the base kind is the one there is (below), the
	// smoke's spec is the same for every workflow (image.SpecForCloud reads
	// that same constant and only checks that the name it is given exists),
	// and the secrets are the repository's (catalogBuildSecrets). So a
	// repository that declares several workflows is not asked to pick one to
	// build an entry with: the first name is the one the spec is rendered for.
	// loadCheckoutResolved has already refused a file with no workflow, so
	// there is always one.
	workflow := slices.Sorted(maps.Keys(cfg.Workflows))[0]
	origin, err := originRepo(ctx)
	if err != nil {
		return err
	}
	repo := o.repo
	if repo == "" {
		repo = origin
	}
	if a, err1 := task.CanonicalRepo(origin); err1 != nil {
		return userErr("%v", err1)
	} else if b, err2 := task.CanonicalRepo(repo); err2 != nil {
		return userErr("%v", err2)
	} else if a != b {
		return userErr("--repo %s is not this checkout's origin (%s); run from %s's checkout", repo, origin, repo)
	}
	entries, err := catalogBuildEntries(cmd, cat, name, repo)
	if err != nil {
		return err
	}
	repoURL, err := originURL(ctx)
	if err != nil {
		return err
	}
	// There is one base kind (config.Bases holds it alone, and every workflow
	// reports it), so no base here is any workflow's: a catalog image is built
	// FROM an arbitrary base by its own Dockerfile (D1151), and this one is
	// the base the build itself runs on.
	kind := config.BaseKind
	base := o.base
	if base == "" {
		base = lc.BaseImage(kind)
	}
	if base == "" {
		if base, err = image.BaseRef(Version); err != nil {
			return userErr("%v", err)
		}
	}
	// The base image reaches the build's FROM line as it stands, and the
	// build step runs on it: a reference under the installation's
	// remote (pull-through) repository that is not a pin this tool wrote is
	// refused here, not taken as the operator's own image (the per-workflow
	// path's rule, design §3.6, §10.7).
	host, err := infra.RegistryHost(lc)
	if err != nil {
		return userErr("%v", err)
	}
	for _, k := range slices.Sorted(maps.Keys(lc.BaseImages)) {
		if foreignProxyRef(lc.BaseImages[k], host, k, baseOwner(lc)) {
			return foreignProxyRefusal("the base image for "+k, lc.BaseImages[k], host, k, baseOwner(lc))
		}
	}
	if foreignProxyRef(base, host, kind, baseOwner(lc)) {
		return foreignProxyRefusal("the base image for "+kind, base, host, kind, baseOwner(lc))
	}
	specLC := *lc
	specLC.BaseImages = maps.Clone(lc.BaseImages)
	if specLC.BaseImages == nil {
		specLC.BaseImages = map[string]string{}
	}
	specLC.BaseImages[kind] = cmp.Or(specLC.BaseImages[kind], base)
	// The App ID reaches infra.Repo as an input rather than being left to its
	// own lookup of the local config alone: a repository whose ID is only in
	// the environment has one too (catalogGitHubAppID), and the spec's
	// credential and the provider this command reads the Dockerfile with must
	// be named from the same answer.
	appID := ""
	if cfg.Git.Provider == gitprov.KindGitHub {
		if appID, err = catalogGitHubAppID(lc, repo, os.Getenv); err != nil {
			return err
		}
	}
	rs, err := infra.Repo(infra.Inputs{LC: &specLC, Repo: repo, Cfg: cfg, FileSetsBaseBranch: fileSetsBaseBranch(rf), RepoURL: repoURL, GitHubAppID: appID})
	if err != nil {
		return userErr("%v", err)
	}
	if note := buildRecordNote(lc); note != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", note)
	}
	gitID, err := gitSecretID(ctx, lc, rs, "the catalog build of "+rs.Name)
	if err != nil {
		return err
	}
	// The commit every entry's build is pinned to: the checkout's own HEAD.
	// A build reads one object and resolves no ref of its own (PIC3's copy
	// answers with bytes and no commit), so the operator's commit is what
	// this submission states, and what the image's revision label and the
	// entry's record then carry.
	commit, err := gitRead(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return userErr("reading the checkout's commit: %v", err)
	}
	selftest, err := image.SpecForCloud(cfg, workflow, commit, repoURL)
	if err != nil {
		return userErr("%v", err)
	}
	selftestJSON, err := json.Marshal(selftest)
	if err != nil {
		return userErr("%v", err)
	}
	secrets, err := catalogBuildSecrets(cfg)
	if err != nil {
		return err
	}
	// The image is spelled from the host the installation's registries are
	// under and not from a region of this command's own, so a build pushes
	// exactly where a job's catalog image is pulled from, whichever one the
	// local config names (infra's catalogImage reads the same host).
	region, ok := strings.CutSuffix(host, "-docker.pkg.dev/"+lc.GCPProject)
	if !ok {
		return userErr("registry host %s is not <region>-docker.pkg.dev/<project>", host)
	}
	registry := host + "/" + gcp.SharedRegistryRepoID()
	b, err := gcp.NewBuilder(ctx, env.gcp, lc.BuildRegion())
	if err != nil {
		return remote(err)
	}
	switch exists, err := b.RegistryExists(ctx, registry); {
	case errors.Is(err, gcp.ErrRegistryUnchecked):
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: could not check that the shared image registry %s exists (%s); submitting the build anyway: if the registry is missing, the build fails when it pushes\n", registry, oneLine(err.Error()))
	case err != nil:
		return remote(err)
	case !exists:
		return userErr("project %s has no shared image registry %s, where a catalog image is pushed. fugaro update creates it: run that from this checkout first", lc.GCPProject, registry)
	}
	// The plan this command is about, in one value: every entry this invocation
	// builds is named in it, so what the person is shown (the typed name's own
	// banner, or the note the gate prints) and what each build's document
	// carries are one plan (image_build_managed.go).
	entryNames := make([]string, len(entries))
	for i, e := range entries {
		entryNames[i] = e.Name
	}
	plan := buildPlan{
		Repo: repo, Origin: origin, Entries: entryNames, Kind: kind, Base: base,
		Account: rs.BuildServiceAccountEmail, Registry: registry, Machine: lc.Build.MachineType,
		What: catalogBuildBanner(entries, lc.Build.MachineType, rs.BuildServiceAccountEmail, registry),
	}
	// The typed name, once, for every entry this command builds: it is what an
	// unmanaged project is asked and what a session the gate does not stand in
	// for has always been asked. A managed project's own terminal reads nothing
	// here — each build is judged and noted on its own below, with D1013's
	// bound and the kill switch re-read immediately before it.
	m := openBuildManaged(ctx, env, conds)
	if !m.standsIn(conds) {
		if err := confirmBuild(cmd, lc.Name, lc.GCPProject, plan.What); err != nil {
			return err
		}
	}
	// The copies are written only now, after the confirmation (typed, or the
	// gate's own in a managed project): like the layer copy the per-workflow
	// path writes, a declined (or unconfirmable) build must leave no trace in
	// the bucket.
	rb, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	// The provider's credential comes from where it actually is -- the local
	// config, the repository's github-app-key secret and the project's
	// bitbucket-token secret -- rather than from the process environment,
	// which holds none of it (catalogProviderEnv). That read, like the copies
	// below, waits for the confirmation: a declined build reads no
	// secret. The terminal fact is the command's own stdin, where the
	// confirmation above was typed, so the helper's coding-agent refusal asks
	// the question this command already answered.
	providerEnv, providerHint, err := catalogProviderEnv(ctx, cmd.ErrOrStderr(), lc, rs, entries, os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	if err != nil {
		return err
	}
	_, opener, err := openCatalogProvider(providerEnv, func(w string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
	})
	if err != nil {
		return userErr("%v", err)
	}
	for _, e := range entries {
		// The gate's own answer about THIS entry, read and judged here rather
		// than once per command (D1013, D1024): the bound and the kill switch
		// are read before this build, and the verdict is spent by the record
		// this build writes after its submission. It comes before the copy so
		// that an entry the gate refused leaves no trace in the bucket.
		if err := m.admitEntry(ctx, cmd, conds, cat, e, rs.Slug, plan); err != nil {
			return err
		}
		key, err := config.CatalogDockerfileKey(e.Name)
		if err != nil {
			return userErr("%v", err)
		}
		if err := writeCatalogDockerfile(ctx, rb, opener, env.repoProvider(e.Source.Repo, cfg), e); err != nil {
			// A provider opened without the credential its environment did
			// not carry fails here with its own sentence, which is left
			// exactly as it is; the hint says where the token actually is
			// and what reading it needs (catalogProviderEnv). No other
			// failure carries it: providers.ErrNoCredential is only ever the
			// missing token.
			if providerHint != "" && errors.Is(err, providers.ErrNoCredential) {
				return fmt.Errorf("%w%s", err, providerHint)
			}
			return err
		}
		// What the build pins and records is the copy itself: its bytes and
		// its object generation, read back from the one object the build
		// reads, so the request states what the bucket holds and not what
		// this process resolved a moment earlier.
		data, gen, err := rb.Read(ctx, key)
		if err != nil {
			return remote(err)
		}
		sum := sha256.Sum256(data)
		imageRef, err := gcp.SharedCatalogImage(region, lc.GCPProject, e.Name)
		if err != nil {
			return userErr("%v", err)
		}
		res, err := b.SubmitCatalog(ctx, gcp.CatalogBuildSpec{
			Entry: e.Name, Slug: rs.Slug,
			GitProvider: rs.Provider, RepoURL: rs.RepoURL, BaseBranch: cmp.Or(rs.BaseBranch, "main"),
			GitSecretID: gitID, GitUser: rs.GitUser, GitHubAppID: rs.GitHubAppID,
			Base: base, Image: imageRef,
			Definition:     gcp.CatalogDefinition{SourceCommit: commit, Generation: gen, SHA256: hex.EncodeToString(sum[:])},
			ServiceAccount: rs.BuildServiceAccountEmail, MachineType: lc.Build.MachineType,
			WorkflowSecrets: secrets,
			Bucket:          lc.RecordBucketURL(), Selftest: selftestJSON, NoSmoke: o.noSmoke,
		})
		switch {
		case errors.Is(err, gcp.ErrBadBuildSpec):
			return userErr("%v", err)
		case err != nil:
			return remote(err)
		}
		// The submission IS the write this build's record is about: once it is
		// out the build happened and keeps billing, so the record is written
		// whatever the wait after it did (D1014 on a write that cannot leave a
		// trail). The count moves with it, so the entry after this one is
		// judged against the bound with this build in it.
		if err := m.recorded(ctx); err != nil {
			return err
		}
		if !o.noWait {
			fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: Cloud Build build %s of %s submitted; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
			done, waitErr := b.Wait(ctx, res.ID, 0)
			if done.Image == "" {
				done.Image = res.Image
			}
			if done.LogURL == "" {
				done.LogURL = res.LogURL
			}
			res = done
			if waitErr != nil {
				return remote(waitErr)
			}
		}
		switch {
		case o.noWait:
			fmt.Fprintf(cmd.OutOrStdout(), "submitted Cloud Build build %s of %s; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
		case res.Digest == "":
			// SUCCESS covers the promotion, so the content tag and latest
			// are there; only the digest report is missing.
			fmt.Fprintf(cmd.OutOrStdout(), "built %s, digest unknown: Cloud Build reported no pushed image (Cloud Build build %s)\n", oneLine(res.Image), oneLine(res.ID))
		default:
			fmt.Fprintf(cmd.OutOrStdout(), "built %s@%s (Cloud Build build %s)\n", oneLine(strings.TrimSuffix(res.Image, ":latest")), oneLine(res.Digest), oneLine(res.ID))
		}
	}
	return nil
}

// catalogBuildEntries is the entries one command builds: the one the
// operator named, or every entry that has a Dockerfile to build. An entry a
// prebuilt image answers is named and passed over, and one whose Dockerfile
// lives in another repository is passed over too (see
// runImageBuildCatalog): a name asks for it and is told where to run, while
// a whole-catalog build goes on with the rest.
func catalogBuildEntries(cmd *cobra.Command, cat *config.Catalog, name, repo string) ([]config.Entry, error) {
	if name != "" {
		e, ok := catalogEntryNamed(cat, name)
		if !ok {
			return nil, userErr("this project's image catalog has no entry %s: fugaro image ls lists the entries it has", pluginwire.Printable(name))
		}
		if err := catalogBuildEntryGuard(e, repo); err != nil {
			return nil, err
		}
		return []config.Entry{e}, nil
	}
	buildable := make([]config.Entry, 0, len(cat.Entries))
	for _, e := range cat.Entries {
		switch {
		case e.Source.Kind == config.SourceImage:
			fmt.Fprintf(cmd.ErrOrStderr(), "note: the catalog entry %s is the prebuilt image %s, so it has nothing to build; passing it over\n",
				pluginwire.Printable(e.Name), pluginwire.Printable(e.Source.Image))
		case e.Source.Kind == config.SourceDockerfile && !sameRepo(e.Source.Repo, repo):
			fmt.Fprintf(cmd.ErrOrStderr(), "note: the catalog entry %s is built from %s's Dockerfile; passing it over, since its build runs as that repository's build account. Run this from %s's checkout to build it\n",
				pluginwire.Printable(e.Name), pluginwire.Printable(e.Source.Repo), pluginwire.Printable(e.Source.Repo))
		default:
			buildable = append(buildable, e)
		}
	}
	if len(buildable) == 0 {
		return nil, userErr("every entry of this project's image catalog either is a prebuilt image or is built from another repository's Dockerfile: fugaro image ls lists them, and a build runs from the checkout of the repository a Dockerfile is in")
	}
	return buildable, nil
}

// catalogEntryGuardKind is the one rule both paths that build a catalog entry
// answer to, as a value rather than a sentence: an entry a prebuilt image
// answers has nothing to build, and an entry whose Dockerfile is another
// repository's is built by that repository's build account or not at all. The
// rule is one function so the scheduled path cannot drift from the manual one;
// the two answer it with different routes, because the person reading is in a
// different place — a checkout, where running from the other repository's
// checkout is the fix, or the nightly job's log, where fugaro update is.
type catalogEntryGuardKind int

const (
	catalogEntryBuildable catalogEntryGuardKind = iota
	catalogEntryPrebuilt
	catalogEntryOtherRepo
)

func catalogEntryGuardOf(e config.Entry, repo string) catalogEntryGuardKind {
	switch {
	case e.Source.Kind == config.SourceImage:
		return catalogEntryPrebuilt
	case e.Source.Kind == config.SourceDockerfile && !sameRepo(e.Source.Repo, repo):
		return catalogEntryOtherRepo
	}
	return catalogEntryBuildable
}

// catalogBuildEntryGuard is the manual path's refusal: the operator is at a
// checkout, so the route it names is the other repository's.
func catalogBuildEntryGuard(e config.Entry, repo string) error {
	switch catalogEntryGuardOf(e, repo) {
	case catalogEntryPrebuilt:
		return userErr("the catalog entry %s is the prebuilt image %s: there is nothing to build", pluginwire.Printable(e.Name), pluginwire.Printable(e.Source.Image))
	case catalogEntryOtherRepo:
		return catalogBuildWrongRepo(e, repo)
	}
	return nil
}

// catalogBuildWrongRepo is the refusal of one entry that this checkout cannot
// build: its Dockerfile is another repository's, and a build runs as the
// build account of the repository it was submitted from, holding that
// repository's credential and secrets.
func catalogBuildWrongRepo(e config.Entry, repo string) error {
	return userErr("the catalog entry %s is built from %s's Dockerfile, and a build runs as the build account of the repository it is submitted from, with that repository's credential and secrets: run this from %s's checkout (or from %s, which is already there)",
		pluginwire.Printable(e.Name), pluginwire.Printable(e.Source.Repo), pluginwire.Printable(e.Source.Repo), pluginwire.Printable(repo))
}

// catalogEntryNamed is the entry of cat named name, if it has one. The name
// came from the command line, so it is compared as given: a catalog cannot
// hold another spelling of it (config.ValidateCatalog's allowlist).
func catalogEntryNamed(cat *config.Catalog, name string) (config.Entry, bool) {
	for _, e := range cat.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return config.Entry{}, false
}

// catalogBuildSecrets is what a catalog build mounts as BuildKit file
// secrets: the repository's build secrets, which are the ones its workflows
// declare. An entry is not a workflow (D1144), so no one workflow's list is
// the entry's, and a build that took one workflow's would go without a secret
// another declares. The same pair declared twice is one mount; two workflows
// that mount different secrets on one variable are refused, rather than the
// build silently getting one of the two (CatalogBuildSpec.check refuses a
// variable used twice for the same reason).
func catalogBuildSecrets(cfg *config.Config) ([]config.Secret, error) {
	var out []config.Secret
	owner := []string{} // parallel to out: the workflow whose declaration added it
	at := map[string]int{}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		for _, ws := range cfg.Workflows[name].Secrets {
			i, ok := at[ws.Env]
			if !ok {
				at[ws.Env] = len(out)
				owner = append(owner, name)
				out = append(out, ws)
				continue
			}
			if out[i] != ws {
				return nil, userErr("workflows %s and %s mount different secrets as %s (%s and %s): a catalog build mounts the repository's secrets, so one variable cannot carry two",
					pluginwire.Printable(owner[i]), pluginwire.Printable(name), pluginwire.Printable(ws.Env),
					pluginwire.Printable(out[i].Name), pluginwire.Printable(ws.Name))
			}
		}
	}
	return out, nil
}

// catalogBuildBanner is what a catalog build's confirmation says it does:
// the entries it submits one build per, the machine each runs on, the
// account and the registry they push to.
func catalogBuildBanner(entries []config.Entry, machineType, account, registry string) string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	what := "the catalog entry " + names[0]
	if len(names) > 1 {
		what = fmt.Sprintf("%d catalog entries (%s)", len(names), strings.Join(names, ", "))
	}
	return fmt.Sprintf("submits a Cloud Build for %s on %s, as %s: each build reads the Dockerfile copied into the project's bucket, smoke-tests the candidate and promotes the image into %s and records it",
		what, machineType, account, registry)
}
