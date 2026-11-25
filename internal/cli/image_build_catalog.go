package cli

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
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
	// What this session is, as the managed door reads it: the owner at their
	// own terminal and a coding agent's session are the two the gate is read
	// for, a pipe is not judged (openBuildManaged), and --json never reaches
	// this far (runImageBuild refuses it for a Cloud Build).
	conds := initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), JSON: o.asJSON, Agent: agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))}
	env, err := openBuildCloud(ctx, o)
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
		return userErr("this project has no image catalog, so there is no entry %s to build: fugaro image add %s writes one and makes it live, while fugaro image build --workflow builds a workflow's derived image", pluginwire.Printable(name), pluginwire.Printable(name))
	}
	if repoImagesOff {
		return repoImagesDisabled(cmd.OutOrStdout())
	}
	return runImageBuildCloud(cmd, o)
}

// runImageBuildCatalog submits one Cloud Build per entry of cat (D1144):
// each reads the Dockerfile the entry's source resolved to and this command
// copied into the project's bucket, builds it with the checkout's
// repository's provider credential, and promotes the image into the
// installation's shared registry (D1152). The build runs as the project's
// build identity (B6), which is the installation's account and not the
// repository's: an entry may name repositories whose build account the
// retirement of their roots has destroyed, so the account a catalog build
// runs as has to outlive them. No definition the entry's own text holds is
// handed a secret (B9, D2311): such an entry is refused at publish if it asks
// for one, and a build handed none cannot satisfy a mount the refusal failed
// to read. The credential reaches the build step only for a definition that
// bakes (gcp.CatalogBuildSpec.Baked), whose bake stage clones at
// FUGARO_COMMIT — that build step is the last to read the credential, so the
// request keeps the secret and the volume on it.
//
// The credential and the record's slug are the checkout's repository's, so an
// entry whose Dockerfile lives in another repository is refused rather than
// submitted: its build would clone with an account that does not hold that
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
	// Building a catalog entry builds the project's own image (F19's catalog),
	// and the entry it builds is read from this machine's config: the build is
	// the project admin's. The per-workflow build beside it is the repository's
	// own and is not this gate's (D1900 keeps it off entirely).
	if err := accessGateProject(ctx, lc, nil, "nothing was built", "building a catalog entry"); err != nil {
		return err
	}
	root, rf, err := loadCheckoutResolved(ctx, "", lc, layerOptions{})
	if err != nil {
		return err
	}
	cfg := rf.Cfg
	// A catalog entry is not a workflow (D1144), and nothing a catalog build
	// reads is per workflow: the base kind is the one there is (below), the
	// smoke's spec is the same for every workflow (image.SpecForCloud reads
	// that same constant and only checks that the name it is given exists),
	// and no workflow's secrets reach a step of it — a build step is handed
	// none at all (B9, D2311), and the clone step's credential is the
	// repository's. So a repository that declares several workflows is not
	// asked to pick one to build an entry with: the first name is the one the
	// spec is rendered for.
	// loadCheckoutResolved has already refused a file with no workflow, so
	// there is always one.
	workflow := slices.Sorted(maps.Keys(cfg.Workflows))[0]
	origin, err := originRepo(ctx)
	if err != nil {
		return err
	}
	repo := o.repo
	if repo, err = repoArg(ctx, o.cloud, repo); err != nil {
		return err
	}
	if repo == "" {
		repo = origin
	}
	canon, err1 := task.CanonicalRepo(origin)
	if err1 != nil {
		return userErr("%v", err1)
	}
	if b, err2 := task.CanonicalRepo(repo); err2 != nil {
		return userErr("%v", err2)
	} else if canon != b {
		return userErr("--repo %s is not this checkout's origin (%s); run from %s's checkout", repo, origin, repo)
	}
	entries, err := catalogBuildEntries(cmd, cat, name, repo, o.noSmoke)
	if err != nil {
		return err
	}
	// Whether this command builds anything at all: an entry a prebuilt image
	// answers is only smoked, and a command that submits nothing but smokes
	// reads no credential, opens no provider, copies no Dockerfile and pushes
	// nothing. What is read only for a build is read only when one is in the
	// set, so a smoke is not refused over a build's own inputs.
	builds := slices.ContainsFunc(entries, func(e config.Entry) bool { return e.Source.Kind != config.SourceImage })
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
	// the environment has one too (repoGitHubAppID), and the spec's
	// credential and the provider this command reads the Dockerfile with must
	// be named from the same answer.
	appID := ""
	if cfg.Git.Provider == gitprov.KindGitHub {
		if appID, err = repoGitHubAppID(lc, repo, os.Getenv); err != nil {
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
	// The commit an entry's build is pinned to when the entry names no
	// context: the checkout's own HEAD. A build reads one object and resolves
	// no ref of its own (PIC3's copy answers with bytes and no commit), so the
	// operator's commit is what this submission states, and what the image's
	// revision label and the entry's record then carry. An entry that names a
	// context states the commit its copy resolved instead (D1395), below.
	commit, err := gitRead(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return userErr("reading the checkout's commit: %v", err)
	}
	// A build of an entry that names no context clones exactly that commit —
	// the clone step fetches it by id, the definition having been read at it —
	// so a commit the remote does not have is a billable build that fails on
	// the fetch two minutes in, after a definition was copied and a lease
	// taken. It is refused here instead: before the credential, the copy, the
	// lease and the submission, all of which come later. A prebuilt entry's run
	// is the smoke of its own reference and clones nothing, and a context
	// entry's build is pinned to the commit its copy resolved (D1395), so
	// neither reads the checkout's HEAD and neither is refused over it.
	if slices.ContainsFunc(entries, func(e config.Entry) bool { return e.Source.Kind != config.SourceImage && e.Source.Context == "" }) {
		if err := refuseUnpushedCommit(ctx, root, commit); err != nil {
			return err
		}
	}
	// The smoke a prebuilt entry runs is the tools tier alone: the checkout's
	// own spec (SpecForCloud) is about a container this command built — it
	// checks the repository's checkout, the hardening and the versions the
	// build stamped — and none of it holds for a reference somebody else
	// published. What such a reference can be asked is whether the critical
	// tools are there, which is the whole of a smoke-only run.
	prebuiltSelftest, err := json.Marshal(image.SelftestSpec{ToolsOnly: true, Tools: "critical"})
	if err != nil {
		return userErr("%v", err)
	}
	gitID := ""
	if builds {
		gitID, err = gitSecretID(ctx, lc, rs, nodePolicy(ctx, &env.policy, lc), "the catalog build of "+rs.Name)
		if err != nil {
			return err
		}
	}
	// The bake this build may compose into an entry's Dockerfile (the image
	// setup skill's bake step, IS8b): the project's repo-to-image map and its
	// bake switch, read the way the catalog itself was read for this command —
	// this machine's own project config over the published node's (D1906,
	// D1901) — and under the same rule for a node that cannot be read, which
	// the warning above has already said out loud. Nothing here is decided per
	// entry: which repository a build bakes is the checkout this command is
	// running in, and each entry only answers whether it is that repository's
	// image.
	published, _, _ := env.policy.read(ctx, lc)
	var publishedMap map[string]string
	var publishedBake *bool
	var publishedCloneURLs map[string]string
	if published != nil {
		publishedMap, publishedBake, publishedCloneURLs = published.ImageMap, published.BakeRepos, published.CloneURLs
	}
	imageMap := lc.ImageMapEffective(publishedMap)
	bakeRepos := lc.BakeReposEffective(publishedBake)
	// The project's repositories, each resolved to where it clones from: the map
	// is what says which repositories the entry being built serves, and the bake
	// of each of them needs the slug its checkout is written at and the URL it
	// clones from. A repository the project records no provider for, or whose
	// provider and name make no slug or URL this build could clone — the case of
	// a config written by hand — is not one of these, and the map putting it on
	// an entry is refused by name rather than baked from a URL this command
	// would have to guess (imagebake.Decide).
	//
	// The two blocks a repository is read out of are keyed differently, and both
	// spellings are canonical: `repos` and `image_map` by the repository alone
	// (task.CanonicalRepo, "acme/app"), and `clone_urls` by the provider and the
	// repository together (projectpolicy.Canonical, "github/acme/app", which is
	// the key the block is published under and the key its own validation holds
	// every entry to). A lookup that mixed them would read every repository as
	// one the project gives no URL, which is not an error anywhere: CloneURL
	// answers the provider's usual host for "", so the build would clone from a
	// URL the project did not choose.
	cloneURLs := lc.CloneURLsEffective(publishedCloneURLs)
	var projectRepos []imagebake.Repo
	for name, r := range lc.Repos {
		canonical, err := task.CanonicalRepo(name)
		if err != nil || r.Provider == "" {
			continue
		}
		slug, err := task.Slug(r.Provider, canonical)
		if err != nil {
			continue
		}
		key, err := projectpolicy.Canonical(r.Provider, canonical)
		if err != nil {
			continue
		}
		url, err := repourl.CloneURL(r.Provider, canonical, cloneURLs[key])
		if err != nil {
			continue
		}
		projectRepos = append(projectRepos, imagebake.Repo{
			Repo: canonical, Slug: slug, URL: url, Branch: cmp.Or(r.BaseBranch, "main"),
		})
	}
	// What the map and the switch decide, and what they do not (imagebake.
	// Decide): every repository the map puts on an entry is baked into that
	// entry's image when the image is one this build's own repository runs, so an
	// installation that maps nothing composes nothing at all — byte-for-byte the
	// definition it had. No path under fugaro update writes a definition at all
	// (O1), which is what keeps it a one-command upgrade.
	bakeRepo := imagebake.Repo{Repo: canon, Slug: rs.Slug, URL: repoURL, Branch: cmp.Or(rs.BaseBranch, "main")}
	bakeRef := bakeReference(cfg, cat, rs.Slug, catalog.Backend(lc.Backend()))
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
	// A smoke-only run pushes nothing, so the registry it never touches is not
	// read for it: a project whose shared registry is missing can still smoke
	// a prebuilt reference, and refusing that over a registry it does not use
	// would make the smoke the one thing a broken installation cannot check.
	if builds {
		switch exists, err := b.RegistryExists(ctx, registry); {
		case errors.Is(err, gcp.ErrRegistryUnchecked):
			fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: could not check that the shared image registry %s exists (%s); submitting the build anyway: if the registry is missing, the build fails when it pushes\n", registry, oneLine(err.Error()))
		case err != nil:
			return remote(err)
		case !exists:
			return userErr("project %s has no shared image registry %s, where a catalog image is pushed. fugaro update creates it: run that from this checkout first", lc.GCPProject, registry)
		}
	}
	// The plan this command is about, in one value: every entry this invocation
	// builds is named in it, so what the person is shown (the banner below) and
	// what each build's document carries are one plan (image_build_managed.go).
	entryNames := make([]string, len(entries))
	for i, e := range entries {
		entryNames[i] = e.Name
	}
	plan := buildPlan{
		Repo: repo, Origin: origin, Entries: entryNames, Kind: kind, Base: base,
		Account: gcp.BuildIdentityEmail(lc.GCPProject), Registry: registry, Machine: lc.Build.MachineType,
		What: catalogBuildBanner(entries, lc.Build.MachineType, gcp.BuildIdentityEmail(lc.GCPProject), registry),
	}
	// What this command is about to build, printed once for the whole
	// invocation: the banner the typed confirmation used to show under it. What
	// bounds the builds now is the lease taken per entry below, and in a managed
	// project each entry is judged and recorded on its own (admitEntry), which
	// is why the gate's own sentence is printed per entry rather than here.
	fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: %s\n", plan.What)
	m := openBuildManaged(ctx, env, conds)
	// The copies are written only after each entry's lease is taken, below:
	// like the layer copy the per-workflow path writes, a build no cap has room
	// for must leave no trace in the bucket.
	rb, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	// The provider's credential comes from where it actually is -- the local
	// config, the repository's github-app-key secret and the project's
	// bitbucket-token secret -- rather than from the process environment,
	// which holds none of it (catalogProviderEnv). It is read before the copies
	// below. The gate's per-entry admission and the lease are the two
	// judgements that come later than this read, so an entry either of them
	// then refuses has had its credential read already: the copy that verdict
	// is about is what needs one. The terminal fact is the command's own
	// stdin, so the helper's coding-agent refusal asks the same question
	// openBuildManaged asked above.
	providerEnv, providerHint, err := catalogProviderEnv(ctx, cmd.ErrOrStderr(), lc, rs, entries, os.Getenv, stdinIsTerminal(cmd.InOrStdin()), nodePolicy(ctx, &env.policy, lc))
	if builds && err != nil {
		return err
	}
	var opener gitprov.Opener
	if builds {
		_, opener, err = openCatalogProvider(providerEnv, func(w string) {
			fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
		})
		if err != nil {
			return userErr("%v", err)
		}
	}
	// This command's run (D1352, D1354): one step per entry built, and one log
	// under this command's own name, both opened at the first entry that
	// proceeds — after the confirmation above, the credential read and this
	// entry's own judgement by the gate — so a run refused before that point
	// keeps no log, not even an empty one.
	//
	// rep is the run the steps print through: the one the caller handed in
	// (o.run), or this command's own, opened below. It is what makes `fugaro
	// apply`'s catalog build one of its own run's steps, in its own run's one
	// log, rather than a second `image-build` run opened beside it — and a run
	// handed in belongs to its caller, so it is never closed here (that
	// command's own defer closes it). began is "a step of this run has begun",
	// which is what the two refusals below read: a borrowed run is open before
	// the first entry proceeds, and a refusal before that point is still the
	// whole of what the command says (D1354).
	var sr *stepRun
	rep := o.run
	var began bool
	defer func() {
		if sr != nil {
			sr.close()
		}
	}()
	// One price lookup for the whole command: every entry's lease is priced
	// from the same catalog read (buildPricerCache's own note).
	pricers := &buildPricerCache{}
	for _, e := range entries {
		// The gate's own answer about THIS entry, read and judged here rather
		// than once per command (D1013, D1024): the kill switch is read before
		// this build, and the verdict is spent by the record this build writes
		// after its submission. It comes before the lease and the copy so that
		// an entry the gate refused leaves no trace in the bucket.
		if err := m.admitEntry(ctx, cmd, conds, cat, e, rs.Slug, plan); err != nil {
			if !began {
				// Nothing of this run has run yet, so there is no account to
				// end: the refusal is the whole of what the command says
				// (D1354).
				return err
			}
			// An earlier entry of this run was built, so the account is open
			// and its closing line is this run's verdict: a command that exits
			// non-zero may not end on a "Done" (G3). The entry the gate refused
			// is the step the run failed on.
			return failStep(rep.rep, rep.rep.Step(e.Name), err)
		}
		// T8's lease, taken before anything of this build is written: the copy
		// below is what the build reads, and a build no cap has room for is
		// refused before the bucket learns there was one. What the lease
		// reserved is settled after the build, and given back when the
		// submission failed. It is charged to the checkout's own repository —
		// the same rs.Slug the build is submitted under — because that is whose
		// cap and whose day counter this build is counted against.
		lease, err := openBuildLease(ctx, cmd.ErrOrStderr(), lc, rs.Name, rs.Slug, pricers)
		if err != nil {
			if !began {
				return err
			}
			return failStep(rep.rep, rep.rep.Step(e.Name), err)
		}
		if rep == nil {
			sr = stepRunFor(cmd, "image-build", cmd.OutOrStdout())
			rep = sr
		}
		began = true
		step := rep.rep.Step(e.Name)
		// The build's own progress is the run's output and the log's, and the
		// sentence this command has always printed on stdout is the step's own
		// line now: one line per entry on the default view, the detail in the
		// log (D1352, D1354). A run whose log could not be opened keeps printing
		// its progress where it always did (G5, G8).
		w := streamOut(rep.rep, rep.log, cmd.ErrOrStderr())
		// What the request is named from and what it carries is the same value
		// either way; the two arms differ in everything a build adds to it. A
		// prebuilt entry's run is the smoke of the reference the entry names
		// (F10/IS11): nothing is copied, nothing is built, nothing is pushed,
		// and the request is submitted with none of a build's own inputs,
		// which CatalogBuildSpec.checkPrebuilt refuses one by one.
		// The address of the service this build's credential step asks for the
		// entry's git credential (IS8, D3330): the installation's own record of
		// it, the same value a repository's build request carries (image.go's
		// buildServiceURL). A Dockerfile entry whose installation records none
		// is refused here and not submitted, for buildServiceURL's reason: the
		// request names no secret any more, so a build without the address
		// would hold no credential at all and fail at its credential step long
		// after it was billed. A prebuilt entry clones nothing and carries no
		// address (checkPrebuilt reads neither).
		serviceURL := configuredCredentialsServiceURL(lc)
		if e.Source.Kind != config.SourceImage && serviceURL == "" {
			return failStep(rep.rep, step, withLease(userErr("a catalog entry's build clones its source through the installation's credentials service, and this config records no address for it (credentials_service.url): the request names no secret at all, so a build submitted without the address would hold no credential and fail at its credential step long after it was billed. Run %s at the installation's own checkout — that records the service's address — and then run this again", selfCommand()+" update"), lease.release(ctx)))
		}
		spec := gcp.CatalogBuildSpec{
			Entry: e.Name, Slug: rs.Slug,
			ServiceAccount: gcp.BuildIdentityEmail(lc.GCPProject), MachineType: lc.Build.MachineType,
			Bucket: lc.RecordBucketURL(), NoSmoke: o.noSmoke,
			CredentialsServiceURL: serviceURL,
			// The record carries the template this build ran, so a release that
			// changes how catalog images are built reaches the entry the next
			// time the check reads it (F33, D1402).
			TemplateSalt: gcp.TemplateSalt(Version),
		}
		if e.Source.Kind == config.SourceImage {
			spec.Prebuilt, spec.Selftest = e.Source.Image, prebuiltSelftest
		} else {
			key, err := config.CatalogDockerfileKey(e.Name)
			if err != nil {
				return failStep(rep.rep, step, withLease(userErr("%v", err), lease.release(ctx)))
			}
			// The bake this entry's definition may carry, decided here where the
			// entry's name is: every repository the project's map puts on it, when
			// this is the image this build's own repository runs (imagebake.
			// Decide).
			bakePlan := imagebake.Decide(imagebake.Request{
				Entry: e.Name, Repo: bakeRepo, Reference: bakeRef,
				Map: imageMap, Bake: bakeRepos,
				Repos: projectRepos,
			})
			noteBakePlan(cmd, e.Name, bakePlan)
			src, dropped, err := writeCatalogDockerfile(ctx, rb, opener, env.repoProvider(e.Source.Repo, cfg), e, bakePlan)
			if err != nil {
				// A provider opened without the credential its environment did
				// not carry fails here with its own sentence, which is left
				// exactly as it is; the hint says where the token actually is
				// and what reading it needs (catalogProviderEnv). No other
				// failure carries it: providers.ErrNoCredential is only ever the
				// missing token.
				if providerHint != "" && errors.Is(err, providers.ErrNoCredential) {
					return failStep(rep.rep, step, withLease(fmt.Errorf("%w%s", err, providerHint), lease.release(ctx)))
				}
				return failStep(rep.rep, step, withLease(err, lease.release(ctx)))
			}
			// The copy lost the checkout a previous build composed into it, if
			// any: the plan above is this build's own bake and says nothing
			// about a copy it composes none into, and a job of the entry finds
			// no checkout in the image from this build on.
			if dropped != "" {
				noteBakeDropped(cmd.ErrOrStderr(), lc, e.Name, dropped)
			}
			// A test seam between the copy above and the read-back below
			// (catalogCopyRace): another publisher's copy of this entry can
			// land in that window, which is what the check the read-back ends
			// with refuses.
			catalogCopyRace(ctx, rb, e)
			// What the build pins and records is the copy itself: its bytes and
			// its object generation, read back from the one object the build
			// reads, so the request states what the bucket holds and not what
			// this process resolved a moment earlier.
			data, gen, err := rb.Read(ctx, key)
			if err != nil {
				return failStep(rep.rep, step, withLease(remote(err), lease.release(ctx)))
			}
			sum := sha256.Sum256(data)
			imageRef, err := gcp.SharedCatalogImage(region, lc.GCPProject, e.Name)
			if err != nil {
				return failStep(rep.rep, step, withLease(userErr("%v", err), lease.release(ctx)))
			}
			spec.GitProvider, spec.RepoURL, spec.BaseBranch = rs.Provider, rs.RepoURL, cmp.Or(rs.BaseBranch, "main")
			spec.GitSecretID, spec.GitUser, spec.GitHubAppID = gitID, rs.GitUser, rs.GitHubAppID
			spec.Base, spec.Image = base, imageRef
			spec.Definition = gcp.CatalogDefinition{SourceCommit: commit, Generation: gen, SHA256: hex.EncodeToString(sum[:])}
			// The copy is the definition the build runs, so what it bakes is
			// read from the copy's own bytes and not from the plan above: the
			// clone step clones the checkouts its stages copy
			// (gcp.CatalogBuildSpec.Baked).
			if spec.Baked, err = catalogBakedRepos(data, e.Name); err != nil {
				return withLease(userErr("%v", err), lease.release(ctx))
			}
			// An entry that names a context carries the identity the copy
			// staged beside the Dockerfile: the commit both were read at, and
			// the digest of the tree the build runs on. The two are read back
			// from the bucket, like the definition's own generation and bytes
			// above, so the request states what the build will find rather than
			// what this process resolved a moment earlier. The record's
			// source_commit is that same commit and not the checkout's: the
			// definition was read at it (D1395), and the two fields are one
			// commit under two names so a reader can tell which is which
			// (D1400). An entry with no context keeps the checkout's commit and
			// stages no such object (F23).
			if e.Source.Context != "" {
				commitKey, err := config.CatalogContextCommitKey(e.Name)
				if err != nil {
					return withLease(userErr("%v", err), lease.release(ctx))
				}
				hashKey, err := config.CatalogContextHashKey(e.Name)
				if err != nil {
					return withLease(userErr("%v", err), lease.release(ctx))
				}
				stagedCommit, _, err := rb.Read(ctx, commitKey)
				if err != nil {
					return withLease(remote(err), lease.release(ctx))
				}
				stagedHash, _, err := rb.Read(ctx, hashKey)
				if err != nil {
					return withLease(remote(err), lease.release(ctx))
				}
				// The three reads above are three objects and not one, so what
				// they answered is checked against what this command staged
				// before any of it reaches the request: a definition read here
				// beside another publisher's commit and digest would be built
				// on a context it was never resolved with.
				if err := refuseMixedCatalogCopy(e, src, data, string(stagedCommit), string(stagedHash)); err != nil {
					return withLease(err, lease.release(ctx))
				}
				spec.Context = e.Source.Context
				spec.ContextCommit, spec.ContextSHA256 = string(stagedCommit), string(stagedHash)
				spec.Definition.SourceCommit = spec.ContextCommit
			}
			// The smoke is rendered from the commit this build bakes — the
			// entry's own where it names a context, the checkout's HEAD
			// otherwise — because the smoke checks the baked checkout against
			// the commit the spec names (image.checkCheckout): one rendered
			// from the checkout's HEAD for a context entry would fail unless
			// the operator happened to be standing on the source branch's head.
			smoke, err := image.SpecForCloud(cfg, workflow, spec.Definition.SourceCommit, repoURL)
			if err != nil {
				return withLease(userErr("%v", err), lease.release(ctx))
			}
			// The smoke reads the checkout where this image actually put it: a
			// composed definition bakes at imagebake.BakedDir, and a smoke still
			// looking at /work/repo would fail the build this command just
			// composed -- or, worse, pass on a checkout the image never baked.
			if dir, baked := imagebake.BakedDir(data); baked && builds {
				smoke.RepoDir = dir
			}
			selftestJSON, err := json.Marshal(smoke)
			if err != nil {
				return withLease(userErr("%v", err), lease.release(ctx))
			}
			spec.Selftest = selftestJSON
		}
		res, err := b.SubmitCatalog(ctx, spec)
		switch {
		case errors.Is(err, gcp.ErrBadBuildSpec):
			return failStep(rep.rep, step, withLease(userErr("%v", err), lease.release(ctx)))
		case err != nil:
			return failStep(rep.rep, step, withLease(remote(err), lease.release(ctx)))
		}
		// The submission IS the write this build's record is about: once it is
		// out the build happened and keeps billing, so the record is written
		// whatever the wait after it did (D1014 on a write that cannot leave a
		// trail). A record that could not be written is the failure the person
		// reads, and the day still counts what the build spent: the build is
		// out and nothing here can take it back.
		//
		// Waited is false here and that is what this path's own sentence asks
		// for: this return happens before the wait below, in both modes, so the
		// whole reservation is what the day counts — the same figure a --no-wait
		// build's settle records, and for the same reason (buildLease.settle).
		// Settling it as a build this command watched would record the moment
		// between the submission and this failure at the lease's minute rate and
		// give the rest of the reservation back, which is money the build Cloud
		// Build is running is spending and headroom under the caps for a loop of
		// builds that fail here.
		// The sentence that names this build, built before anything after the
		// submission can fail — the record and the settle below are both of
		// them, and with --no-wait nothing waited for the build either, so
		// nothing else in this run will say which one it is (G8: what a failure
		// has to be read with, on the screen and in the log).
		submitted := fmt.Sprintf("submitted Cloud Build build %s of %s; log: %s", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
		if err := m.recorded(ctx); err != nil {
			return failStep(rep.rep, step, withLease(err, lease.settle(ctx, false)), submitted)
		}
		if !o.noWait {
			fmt.Fprintf(w, "fugaro: Cloud Build build %s of %s submitted; log: %s\n", oneLine(res.ID), oneLine(res.Image), oneLine(res.LogURL))
			done, waitErr := b.Wait(ctx, res.ID, 0)
			if done.Image == "" {
				done.Image = res.Image
			}
			if done.LogURL == "" {
				done.LogURL = res.LogURL
			}
			res = done
			if waitErr != nil {
				// Watched only when the build really ended: a wait that failed
				// on a status read, or on a cancelled context, leaves a build
				// that may still be running, and the whole reservation is what
				// the day counts for one (buildLease.settle, and the same
				// decision image.go makes for its own wait).
				return failStep(rep.rep, step, withLease(remote(waitErr), lease.settle(ctx, gcp.TerminalStatus(done.Status))))
			}
		}
		// The lease closes before the step is reported done: a settle that
		// fails is the command's exit, and a command that exits non-zero may not
		// end on a "Done" (G3). The build stays named by it either way.
		if err := lease.settle(ctx, !o.noWait); err != nil {
			return failStep(rep.rep, step, err, submitted)
		}
		// A prebuilt entry is smoked and never built, and the sentence says so:
		// what the run produced is the digest of the reference the entry names,
		// which is what its record then carries and what the doctor check reads
		// back (doctor_layer.go). The reference is spelled as the entry spells
		// it — there is no ":latest" of this command's own to trim.
		prebuilt := e.Source.Kind == config.SourceImage
		switch {
		case o.noWait:
			what := "of"
			if prebuilt {
				what = "smoking"
			}
			step.Done(fmt.Sprintf("submitted Cloud Build build %s %s %s; log: %s", oneLine(res.ID), what, oneLine(res.Image), oneLine(res.LogURL)))
		case res.Digest == "":
			// SUCCESS covers the promotion, so the content tag and latest
			// are there; only the digest report is missing.
			if prebuilt {
				step.Done(fmt.Sprintf("smoked %s, digest unknown: Cloud Build reported no digest for the reference (Cloud Build build %s)", oneLine(res.Image), oneLine(res.ID)))
			} else {
				step.Done(fmt.Sprintf("built %s, digest unknown: Cloud Build reported no pushed image (Cloud Build build %s)", oneLine(res.Image), oneLine(res.ID)))
			}
		case prebuilt:
			step.Done(fmt.Sprintf("smoked %s@%s (Cloud Build build %s)", oneLine(res.Image), oneLine(res.Digest), oneLine(res.ID)))
		default:
			step.Done(fmt.Sprintf("built %s@%s (Cloud Build build %s)", oneLine(strings.TrimSuffix(res.Image, ":latest")), oneLine(res.Digest), oneLine(res.ID)))
		}
	}
	return nil
}

// commitCheckRemoteTimeout bounds the remote read below: a remote that does not
// answer must not hold a command that is otherwise ready to submit, and the
// read is a small one (one ref advertisement) wherever the host does answer.
const commitCheckRemoteTimeout = 20 * time.Second

// refuseUnpushedCommit refuses a build whose clone cannot fetch the commit it
// is pinned to. The clone step fetches that commit by id, so a commit the
// remote does not have is a build Cloud Build bills and fails: the owner hit
// exactly this on 2026-11-23, running `fugaro image build go` on a branch with
// a local wip commit, where nothing had been force-pushed and the failing
// build said one had. The refusal asks nothing and waits for nothing
// (CLAUDE.md, Fugaro asks for nothing): it names the commit and the two ways
// out.
func refuseUnpushedCommit(ctx context.Context, root, commit string) error {
	// Only a definite no refuses: a remote this machine cannot read is not an
	// answer about the commit, and refusing over it would refuse a build that
	// would have worked.
	if found, known := commitOnRemote(ctx, root, commit); !known || found {
		return nil
	}
	return userErr("the commit %s exists only in this checkout, and a build clones the commit it is pinned to rather than a branch: push the branch, or switch to a pushed commit (git switch main && git pull --ff-only)", commit)
}

// commitOnRemote reports whether the checkout's remote has commit, so that a
// clone of it can fetch the commit a build names, and whether the question was
// answered at all.
//
// The checkout's own remote-tracking refs answer first, and without the
// network: a commit one of refs/remotes/origin/* contains is on the remote
// however far back in that branch's history it sits, and this is the state a
// checkout it was pushed from is in.
func commitOnRemote(ctx context.Context, root, commit string) (found, known bool) {
	if out, err := gitRead(ctx, root, "branch", "-r", "--contains="+commit); err == nil && out != "" {
		return true, true
	}
	// Nothing local contains it, which is not an answer on its own: the branch
	// it is on may have been pushed from another checkout, or fetched into this
	// one without a remote-tracking ref of it (a pull request's head). So the
	// remote's own refs settle it — and an answer that did not come back is not
	// evidence about the commit, so a remote this machine cannot read leaves
	// the build to fail on its own terms rather than refusing one that would
	// have worked.
	found, err := remoteHasCommit(ctx, root, "origin", commit)
	if err != nil {
		return false, false
	}
	return found, true
}

// remoteHasCommit asks the remote itself whether it publishes commit as a ref
// (git ls-remote: a read of the refs, and no write into the checkout). It is a
// var because a command test's fixture has no remote git can reach; its error
// is inconclusive rather than an answer (commitOnRemote).
var remoteHasCommit = func(ctx context.Context, root, remote, commit string) (bool, error) {
	rctx, cancel := context.WithTimeout(ctx, commitCheckRemoteTimeout)
	defer cancel()
	out, err := gitRead(rctx, root, "ls-remote", "--quiet", remote)
	if err != nil {
		return false, err
	}
	return remotePublishes(out, commit), nil
}

// remotePublishes reports whether git ls-remote's output has commit as a ref:
// one "<sha>\t<ref>" line per ref, a peeled annotated tag's line included, so
// a branch or a tag pointing at the commit answers.
func remotePublishes(out, commit string) bool {
	for _, line := range strings.Split(out, "\n") {
		if sha, _, _ := strings.Cut(line, "\t"); sha == commit {
			return true
		}
	}
	return false
}

// catalogBuildEntries is the entries one command submits: the one the
// operator named, or every entry the catalog has. An entry a prebuilt image
// answers has nothing to build but is not passed over either — its run is the
// smoke of that reference against the runtime contract (F10/IS11) — so a name
// asks for the smoke. --no-smoke asks for a build without its smoke, and a
// prebuilt entry has nothing else: one named with it is refused, and a
// whole-catalog pass passes it over rather than submitting an empty run. An
// entry whose Dockerfile lives in another repository is passed over too (see
// runImageBuildCatalog): a name asks for it and is told where to run, while a
// whole-catalog build goes on with the rest.
func catalogBuildEntries(cmd *cobra.Command, cat *config.Catalog, name, repo string, noSmoke bool) ([]config.Entry, error) {
	if name != "" {
		e, ok := catalogEntryNamed(cat, name)
		if !ok {
			return nil, userErr("this project's image catalog has no entry %s: fugaro image ls lists the entries it has", pluginwire.Printable(name))
		}
		if err := catalogBuildEntryGuard(e, repo, noSmoke); err != nil {
			return nil, err
		}
		return []config.Entry{e}, nil
	}
	buildable := make([]config.Entry, 0, len(cat.Entries))
	for _, e := range cat.Entries {
		switch {
		case e.Source.Kind == config.SourceImage && noSmoke:
			fmt.Fprintf(cmd.ErrOrStderr(), "note: the catalog entry %s is the prebuilt image %s, and --no-smoke leaves a smoke-only entry nothing to submit; passing it over\n",
				pluginwire.Printable(e.Name), pluginwire.Printable(e.Source.Image))
		case e.Source.Kind == config.SourceDockerfile && !sameRepo(e.Source.Repo, repo):
			fmt.Fprintf(cmd.ErrOrStderr(), "note: the catalog entry %s is built from %s's Dockerfile; passing it over, since its build runs as that repository's build account. Run this from %s's checkout to build it\n",
				pluginwire.Printable(e.Name), pluginwire.Printable(e.Source.Repo), pluginwire.Printable(e.Source.Repo))
		default:
			buildable = append(buildable, e)
		}
	}
	if len(buildable) == 0 {
		why := "every entry of this project's image catalog is built from another repository's Dockerfile, and a build runs from the checkout the Dockerfile is in"
		if noSmoke {
			why = "every entry of this project's image catalog is built from another repository's Dockerfile or is a prebuilt image, and --no-smoke leaves a prebuilt entry nothing to submit"
		}
		return nil, userErr("%s: fugaro image ls lists the entries this project has", why)
	}
	return buildable, nil
}

// catalogEntryGuardKind is the one rule both paths that submit a catalog entry
// answer to, as a value rather than a sentence: an entry a prebuilt image
// answers has nothing to build (its run is the smoke of that reference), and
// an entry whose Dockerfile is another repository's is built by that
// repository's build account or not at all. The rule is one function so the
// scheduled path cannot drift from the manual one; the two answer it with
// different routes, because the person reading is in a different place — a
// checkout, where running from the other repository's checkout is the fix, or
// the nightly job's log, where fugaro update is.
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

// catalogBuildEntryGuard is the manual path's refusal, for the entries a
// submission can leave out: the operator is at a checkout, so the route it
// names for another repository's entry is the other repository's. A prebuilt
// entry is admitted — its run is the smoke — unless --no-smoke asks for it,
// which would leave a run with nothing in it.
func catalogBuildEntryGuard(e config.Entry, repo string, noSmoke bool) error {
	switch catalogEntryGuardOf(e, repo) {
	case catalogEntryPrebuilt:
		if noSmoke {
			return userErr("the catalog entry %s is the prebuilt image %s, and the smoke of that reference is the whole of its run: --no-smoke would submit nothing", pluginwire.Printable(e.Name), pluginwire.Printable(e.Source.Image))
		}
	case catalogEntryOtherRepo:
		return catalogBuildWrongRepo(e, repo)
	}
	return nil
}

// catalogBuildWrongRepo is the refusal of one entry that this checkout cannot
// build: its Dockerfile is another repository's, and a build submitted from
// here clones with the checkout's repository's credential rather than that
// repository's.
func catalogBuildWrongRepo(e config.Entry, repo string) error {
	return userErr("the catalog entry %s is built from %s's Dockerfile, and a build submitted from here clones with the credential of the repository it is submitted from: run this from %s's checkout (or from %s, which is already there)",
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

// catalogBuildBanner is what a catalog build says it does: the entries it
// submits one build per, the machine each runs on, the account and the registry
// they push to. An entry a prebuilt image answers is smoked and never built, so
// a set that holds one says what that run is. It is printed once for the whole
// invocation, and the same sentence is the What of every entry's document
// (admitEntry), so what the person read and what the gate judged are one plan.
func catalogBuildBanner(entries []config.Entry, machineType, account, registry string) string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	what := "the catalog entry " + names[0]
	if len(names) > 1 {
		what = fmt.Sprintf("%d catalog entries (%s)", len(names), strings.Join(names, ", "))
	}
	head := fmt.Sprintf("submits a Cloud Build for %s on %s, as %s: ", what, machineType, account)
	builds := slices.ContainsFunc(entries, func(e config.Entry) bool { return e.Source.Kind != config.SourceImage })
	prebuilt := slices.ContainsFunc(entries, func(e config.Entry) bool { return e.Source.Kind == config.SourceImage })
	switch {
	case !builds:
		return head + "each run smoke-tests the prebuilt image its entry names against the runtime contract, records the digest it smoked, and builds and pushes nothing"
	case !prebuilt:
		return head + "each build reads the Dockerfile copied into the project's bucket, smoke-tests the candidate and promotes the image into " + registry + " and records it"
	default:
		return head + "each Dockerfile entry's build reads the Dockerfile copied into the project's bucket, smoke-tests the candidate and promotes the image into " + registry + " and records it, and each prebuilt entry is smoke-tested against the runtime contract alone"
	}
}

// bakeReference is the catalog entry the checkout's own jobs run, "" when they
// run none or a different one each: a repository whose jobs run an entry runs
// that entry's image whatever the project's map says (T1 reading (b)), so the
// bake belongs in that entry's image and not in this one.
//
// The entry is the deploy's own answer to the same question, because the bake
// belongs in the image the repository's jobs actually start — a checkout baked
// into an image they never pull is work nobody reads. catalog.JobImage is the
// one resolver internal/infra's imageRequest asks for the deployed job's image,
// asked here without the project's map, so what it names is the entry the
// workflow's own reference or the catalog's default gives it. A pin (image_name:) and a workflow's own needs:
// are both references, which is what F4 and T1 (b) call them: reading only the
// pin would bake a repository that declares needs: into the entry the map puts
// it on, an image its own jobs never start.
//
// Naming the entry is not the same as baking into it: imagebake.Decide still
// asks the project's map to put this repository on that entry and to put no
// other repository there, because the map is the only evidence a build has
// about any repository but the checkout's own (O3 (b)) — the reference says
// which image this repository's jobs start, and the map is what a build can
// read about the rest.
//
// A workflow that runs the base image or a shared dependency layer names no
// entry and is skipped: such a job holds no repository source and a checkout
// baked for it would be read by nobody. A workflow the rule cannot answer —
// a pin the catalog does not hold, no default and no covering candidate —
// contributes nothing, and a bake is not the place to guess: what such a
// workflow runs is what fugaro validate and the job spec's own refusals
// settle, before a build is composed at all.
func bakeReference(cfg *config.Config, cat *config.Catalog, slug string, be catalog.Backend) string {
	names := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		sel, err := catalog.JobImage(catalog.ImageRequest{Catalog: cat, Canonical: slug, Workflow: cfg.Workflows[name], Backend: be})
		if err != nil || sel.Source != catalog.SourceCatalog {
			continue
		}
		names[sel.Entry.Name] = true
	}
	if len(names) != 1 {
		return ""
	}
	for name := range names {
		return name
	}
	return ""
}

// noteBakePlan is the line a build prints about its bake decision: the two
// things a person cannot read off the Dockerfile are which repositories the
// image carries a checkout of and where each of them was written (D2301). Which
// plans are worth a line is the plan's own answer (imagebake.Plan.Notable), so
// the rule that decides the bake and the rule that decides what is said about it
// cannot drift apart.
func noteBakePlan(cmd *cobra.Command, entry string, p imagebake.Plan) {
	if !p.Notable {
		return
	}
	if len(p.Baked) == 0 {
		// Note-only gate: the switch reads an absent bake_repos as true, but a
		// build that never wrote it asked for nothing, so it has no refusal to
		// explain.
		if !p.Asked {
			return
		}
		line := fmt.Sprintf("note: %s: no checkout baked (%s)", pluginwire.Printable(entry), p.Reason)
		if p.Fix != "" {
			line += ". " + p.Fix
		}
		fmt.Fprintln(cmd.ErrOrStderr(), line)
		return
	}
	// What the line claims is what the image carries and where. That a job of a
	// repository reads its own checkout is not a promise this command has to
	// hedge: IS8's reader is in main (runner.openBakedCheckout, over
	// runner.BakedDir), so a job whose image carries a checkout opens the
	// per-slug path instead of cloning, and the one it opens is the one its own
	// slug names.
	repos := make([]string, len(p.Baked))
	for i, r := range p.Baked {
		dir, err := imagebake.Path(r.Slug)
		if err != nil {
			return // Decide refused this slug, so this cannot happen; a note is not worth a refusal of the build
		}
		repos[i] = pluginwire.Printable(r.Repo) + " at " + dir
	}
	if len(p.Baked) == 1 {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: the catalog entry %s bakes the checkout of %s, so the image carries that repository's source\n",
			pluginwire.Printable(entry), repos[0])
		return
	}
	// Each repository travels with the directory its checkout is at, rather than
	// one list of names and one of paths a reader has to zip up by position.
	fmt.Fprintf(cmd.ErrOrStderr(), "note: the catalog entry %s bakes the checkouts of %s, so the image carries the source of every repository the project's map puts on it\n",
		pluginwire.Printable(entry), strings.Join(repos, ", "))
}
