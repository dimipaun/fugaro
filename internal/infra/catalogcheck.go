package infra

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// CatalogCheckSpecEnv is where the project's catalog check job reads its
// assignment (D1148): the JSON CatalogCheckJobSpec below, rendered at the
// installation's tfvars into var.catalog_check.env. internal/cli's own copy of
// the document is the reader (image_check_catalog.go's readCatalogJobEnv),
// which refuses to start without it, and a test there renders these bytes and
// reads them back through it: the two spellings of the wire format are kept in
// step by that round trip, as CheckSpecEnv's are.
const CatalogCheckSpecEnv = "FUGARO_CATALOG_CHECK_SPEC"

// CatalogCheckSpecVersion is the version of the document CatalogCheckSpecEnv
// carries. The reader refuses a version below one, and it is what a later
// release bumps when the document's meaning changes (a field never changes
// meaning in place).
//
// Version 2 is the document without max_image_builds, which D1013's count was
// (D1543, D1554): the job's own per-run cap is a constant of its binary now
// (internal/cli's catalogRunBuildCap), and what bounds a build's cost is the
// lease it takes. Version 1 stays readable and the key in it is ignored —
// which is what makes the two directions of a mixed installation work while
// `fugaro update` moves the job's image: the reader here decodes into a struct
// with no such key, and the release behind's job binary reads a version-2
// document, finds no key and holds itself to the default of one build per run
// it always did.
const CatalogCheckSpecVersion = 2

// The catalog check job's two names (D1148). The job's must start with
// fugarochk- for CheckJobName's reason: fugaro ls and max_parallel count the
// fugaro-* ones as workflow jobs, and the alert policies match ^fugarochk-
// (alert.tf). The Scheduler job's may start with fugaro-: no listing counts a
// Scheduler job (CoordinatorSchedulerJob's reason).
const (
	CatalogCheckJob          = "fugarochk-catalog"
	CatalogCheckSchedulerJob = "fugaro-catalog-check"
)

// CatalogCheckSpec is the installation root's var.catalog_check: the project's
// one scheduled catalog check job, or nil on an installation whose project
// publishes no catalog (the module's own null, which plans exactly what the
// installation planned before the job existed).
//
// The job's whole assignment is Env's CatalogCheckSpecEnv: the published
// catalog is the project's policy node, whose rules grant a read to a
// Fugaro-minted client token alone, and the job's account holds no token — the
// roles it does hold are the build submitter's and the database role its own
// build leases are written with (catalogcheck.tf, D1554), neither of which is
// a read the job's code may take — so the catalog has to reach it here, as do
// the repositories and anything else it would otherwise have to decide itself
// (D1148).
type CatalogCheckSpec struct {
	AccountID       string `json:"account_id"`
	Job             string `json:"job"`
	Image           string `json:"image"`
	SchedulerJob    string `json:"scheduler_job"`
	SchedulerRegion string `json:"scheduler_region"`
	// FirebaseProject is the installation's Firebase project, and it is here
	// because the job's lease is written into that project's database
	// (D1554): the module creates the job account's own database role in it
	// and binds it there, and a binding cannot name a project the plan does
	// not know (credentials.tf renders its own the same way). Empty on an
	// installation that has no Firebase backend at all, where the job submits
	// its builds unleased and says so, and where there is no database for a
	// role to be granted on.
	FirebaseProject string `json:"firebase_project,omitempty"`
	// Paused stops the schedule. The module's variable defaults it to true and
	// applies whatever it is given on every apply, which is why this field has
	// to exist: paused was a variable no run could set, so the operator note
	// the 0.18.0 job shipped offered a `gcloud scheduler jobs resume` that the
	// next `fugaro update` reverted. It is now the layer's
	// jobs.catalog_check.paused, which the owner writes and update applies.
	Paused bool `json:"paused"`
	// Env is the job's environment: the platform variables every job has
	// (platformEnvFor, the same bytes the repository check job's env carries)
	// and CatalogCheckSpecEnv's document.
	Env map[string]string `json:"env"`
	// DefinitionsCondition is the job account's read-only grant on the copied
	// catalog definitions (builds/catalog/), byte for byte the condition
	// gcp builds and the installation module writes.
	DefinitionsCondition Condition `json:"definitions_condition"`
	// Repositories is one entry per repository whose build records and check
	// state the job may reach (builds/<slug>/catalog/), each under its own
	// condition: one repository's entries and no other's.
	Repositories []CatalogCheckRepository `json:"repositories"`
}

// CatalogCheckRepository is one repository's records grant: the storage slug
// the job's build records live under, the IAM condition scoping the grant to
// that slug's catalog area, and the provider credential a build of one of its
// entries clones with.
type CatalogCheckRepository struct {
	Slug             string    `json:"slug"`
	RecordsCondition Condition `json:"records_condition"`
	// GitSecretID is the repository's provider credential — the secret a build
	// of one of its entries resolves through AvailableSecrets, and the same
	// value CatalogCheckRepoSpec carries for the job's own submissions
	// (catalogCheckRepo resolves it through the one predicate the repository's
	// jobs resolve it through). It is here because the build runs as the
	// PROJECT's build identity (B6, D2301) and not as the repository's own
	// build account: the identity holds one secretAccessor per repository whose
	// entries this project builds (modules/installation/build_identity.tf),
	// which is the reach modules/repo/build.tf's own build grant gave that
	// account. Empty for a repository whose provider credential this
	// installation cannot name, where no build of its entries is submitted at
	// all (catalogCheckRepo's own refusals).
	GitSecretID string `json:"git_secret_id,omitempty"`
	// OwnGitSecretID is the repository's own container when it holds a live
	// version and the plan names the project's copy above: a build resolves the
	// own copy first, so the identity is granted it as well. A second grant and
	// never a replacement of the first, which a managed project's deploy
	// account could not apply without the owner's delete approval.
	OwnGitSecretID string `json:"own_git_secret_id,omitempty"`
}

// CatalogCheckJobSpec is the document CatalogCheckSpecEnv carries: the whole
// run's assignment. The catalog and the repositories are in it because the job
// can read neither on its own (D1148), and it is deliberately the same shape
// internal/cli's reader declares — both halves spell the wire format, and the
// round-trip test is what keeps them in step.
type CatalogCheckJobSpec struct {
	Version int `json:"version"`
	// Catalog is the project's effective image catalog, config.Catalog's own
	// shape, read from the local config alone: an installation's tfvars are
	// computed from the local config, and the published node is a copy of it
	// (D1140).
	Catalog *config.Catalog `json:"catalog"`
	// Repositories are the repositories the catalog's entries build in, each
	// with the facts a build of one of them is submitted with.
	Repositories []CatalogCheckRepoSpec `json:"repositories"`
	// BaseImages are the local config's base images by kind: the job's own
	// image is the one config.BaseKind names, and every entry's build is built
	// FROM it.
	BaseImages map[string]string `json:"base_images"`
	// MachineType and BuildRegion are Cloud Build's (the local config's build
	// settings), not the job's own: the machine an entry's build runs on, and
	// the region it is submitted to, which is also what a build's lease is
	// priced at (internal/cli's openCatalogJobLease).
	MachineType string `json:"machine_type"`
	BuildRegion string `json:"build_region"`
	// FirebaseProject is the installation's Firebase project, the one the
	// job's build leases are written into (D1554): the job proves the database
	// it was handed is that project's own before it writes a lease, with the
	// same predicate every other writer into that database asks
	// (requireOwnBudgetDB), and that predicate is given a project and a URL
	// and not a URL it is meant to read the project out of. It is the same
	// value the spec above carries for the module, written beside it from one
	// local: the job's own assignment and the grant that lets it write are two
	// halves of one fact, and a release that wrote them differently would
	// deploy a job that leases nothing or a role nobody holds.
	FirebaseProject string `json:"firebase_project,omitempty"`
}

// CatalogCheckRepoSpec is one repository's part of the assignment: the facts a
// build of one of its entries is submitted with, and the entries it builds.
// It carries no WorkflowSecrets: those are the repository's workflows' own
// declarations, which live in the checkout's fugaro.yaml and are read at the
// manual path's checkout, not here.
type CatalogCheckRepoSpec struct {
	Repo        string `json:"repo"`
	Slug        string `json:"slug"`
	Provider    string `json:"provider"`
	RepoURL     string `json:"repo_url"`
	BaseBranch  string `json:"base_branch"`
	GitSecretID string `json:"git_secret_id,omitempty"`
	GitUser     string `json:"git_user,omitempty"`
	GitHubAppID string `json:"github_app_id,omitempty"`
	// Workflow is the name the build's smoke spec is rendered for: an entry is
	// not a workflow, and image.SpecForCloud reads a config only to check that
	// the name it is given exists, so this is the repository's own first.
	Workflow            string `json:"workflow"`
	BuildServiceAccount string `json:"build_service_account"`
	// grantedGitSecretID is the credential the build identity's grant names
	// whether or not the repository's own copy won (see catalogCheckRepo); the
	// job's wire format never carries it.
	grantedGitSecretID string
	// Entries are the catalog entries this repository builds: a
	// Dockerfile-sourced one the repository its source names, an inline one the
	// repository the project's map puts it on, and one whose repository this
	// installation cannot name the project's first.
	Entries []string `json:"entries"`
}

// CatalogCheck is the project's catalog check job for lc, or nil when the
// project has nothing to check: no effective catalog, no entry whose
// repository this installation can build with, or no base image recorded for
// the job (and the builds) to run FROM. bucket is the installation's
// validated runs bucket.
//
// Nothing here bounds how many builds one run may submit any more: D1013's
// count is gone (D1543), the job holds itself to its own per-run cap
// (catalogRunBuildCap, internal/cli), and what bounds each build's cost is the
// lease it takes through the same estimator a checkout's build goes through
// (D1554). The budget backend that lease is written into is the
// installation's own, and the variable is set below.
//
// paused is the schedule's own state, already resolved: the layer's
// jobs.catalog_check.paused, or true wherever it wrote none (InstallOptions'
// CatalogCheckPaused and jobPaused). It is a parameter rather than a field the
// caller sets afterwards so that every field of the spec is built here, where
// the plan review reads it.
//
// The job is what makes the catalog check run at all: nothing else submits a
// build for an entry nobody pushed by hand, so the installation either deploys
// this job (and its schedule) or leaves every entry of the project's catalog
// exactly as it is.
func CatalogCheck(lc *localcfg.Config, bucket string, paused bool, containers map[string]string) (*CatalogCheckSpec, error) {
	return catalogCheckSpec(lc, bucket, paused, containers, nil)
}

// catalogCheckSpec is CatalogCheck with the repositories whose own provider
// credential container holds a live version (LiveGitSecrets), which a build
// resolves ahead of the project's copy and so the identity must be granted.
func catalogCheckSpec(lc *localcfg.Config, bucket string, paused bool, containers map[string]string, own map[string]bool) (*CatalogCheckSpec, error) {
	cat, repos, err := catalogCheckFor(lc, containers, own)
	if err != nil {
		return nil, err
	}
	if cat == nil {
		return nil, nil
	}
	if len(repos) == 0 {
		// The catalog's entries all name repositories of this installation, and
		// no repository of it can build with: a job deployed for them would be a
		// nightly failure with nothing to submit, and the module's repositories
		// list must not be empty. Leaving the job out is the same state the
		// installation was in before the job existed.
		return nil, nil
	}
	image := lc.BaseImage(config.BaseKind)
	if image == "" {
		// The installation records no base image, and this job runs FROM it:
		// the module refuses an empty one, and a job whose image this run
		// guessed would check the catalog from whatever the guess names. The
		// job is deployed by the first apply after the base image is pinned —
		// the images stage pins it in the same run and the installation root
		// is applied again behind it (#1843) — and an installation that stays
		// without one is the state it was in before the job existed.
		return nil, nil
	}
	if !gcp.ValidBucketName(bucket) {
		return nil, userErr("the runs bucket %q has a byte outside a-z, 0-9, '.', '_' and '-'", bucket)
	}
	region := lc.SchedulerRegion
	if region == "" {
		r, err := gcp.SchedulerRegion(lc.Region)
		if err != nil {
			return nil, userErr("%v", err)
		}
		region = r
	}
	// The installation's Firebase project, "" when there is none: the module
	// binds the job account's database role in it, and the job names it in its
	// own spec, so the lease the job takes is written into the database the
	// grant is on. One local read twice, because the two are one fact.
	firebaseProject := ""
	if b := lc.Budget; b != nil {
		firebaseProject = b.FirebaseProject
	}
	raw, err := json.Marshal(CatalogCheckJobSpec{
		Version:         CatalogCheckSpecVersion,
		Catalog:         cat,
		Repositories:    repos,
		BaseImages:      maps.Clone(lc.BaseImages),
		MachineType:     lc.Build.MachineType,
		BuildRegion:     lc.BuildRegion(),
		FirebaseProject: firebaseProject,
	})
	if err != nil {
		return nil, err
	}
	env := platformEnvFor(lc, lc.Name, bucket)
	// The credentials service's own URL, which is where the builds this job
	// submits are answered their git credential (IS8, D3330): a catalog
	// entry's build clones its source through the service and no longer
	// through a secret of its own, so the step that fetches that credential
	// reads this address from the job's environment exactly as a repository
	// check's does (imagecheck.go's checkServiceURL, and projectjobs.go's own
	// copy of this fact for a shared job). Absent on an installation whose
	// local config carries no service block, where a submitted build is
	// refused instead of running with nothing to clone with.
	if cs := lc.CredentialsService; cs != nil && cs.URL != "" {
		env[providers.EnvCredentialsServiceURL] = cs.URL
	}
	// The budget backend, which this job leases each build's cost through: the
	// same variable every other job of the installation carries (projectJobEnv
	// sets it the same way), and absent on an installation that has none — the
	// job then submits its builds under no lease and says so in one line, which
	// is what the manifest's own header argues for rather than refusing a
	// nightly check nobody is watching (internal/cli's openCatalogJobLease).
	// The web API key is deliberately not here: the lease is an IAM write by the
	// job's own service account, and no call this job makes needs a client
	// token.
	if b := lc.Budget; b != nil && b.RTDBURL != "" {
		env[RTDBURLEnv] = b.RTDBURL
	}
	env[CatalogCheckSpecEnv] = string(raw)
	out := &CatalogCheckSpec{
		AccountID:       tf.CatalogCheckAccountID,
		Job:             CatalogCheckJob,
		Image:           image,
		SchedulerJob:    CatalogCheckSchedulerJob,
		SchedulerRegion: region,
		FirebaseProject: firebaseProject,
		Paused:          paused,
		Env:             env,
		DefinitionsCondition: Condition{
			Title:      gcp.CatalogReadConditionTitle(tf.CatalogCheckAccountID),
			Expression: gcp.CatalogReadCondition(bucket),
		},
		Repositories: make([]CatalogCheckRepository, 0, len(repos)),
	}
	for _, r := range repos {
		own := ""
		if r.GitSecretID != r.grantedGitSecretID {
			own = r.GitSecretID
		}
		out.Repositories = append(out.Repositories, CatalogCheckRepository{
			OwnGitSecretID: own,
			Slug:           r.Slug,
			GitSecretID:    r.grantedGitSecretID,
			RecordsCondition: Condition{
				Title:      gcp.CatalogRecordsConditionTitle(r.Slug),
				Expression: gcp.CatalogRecordsCondition(bucket, r.Slug),
			},
		})
	}
	return out, nil
}

// WithPlannedProjectSecrets rebuilds the spec's catalog check job against the
// project containers the installation root actually plans (spec.ProjectSecrets,
// once discovery has unioned the live ones in), so the git credential each
// repository's build reads is a container that exists. The job's paused state
// is the spec's own; a spec with no job is returned as it is.
func (s InstallationSpec) WithPlannedProjectSecrets(lc *localcfg.Config) (InstallationSpec, error) {
	if s.CatalogCheck == nil {
		return s, nil
	}
	cc, err := catalogCheckSpec(lc, s.RunsBucket, s.CatalogCheck.Paused, s.ProjectSecrets, s.OwnGitSecrets)
	if err != nil {
		return s, err
	}
	s.CatalogCheck = cc
	return s, nil
}

// LiveGitSecrets reports which of the catalog's repositories hold a live
// version in their own provider-credential container, keyed by that
// container's secret ID. A build resolves the repository's own copy first and
// the project's only when it has none (resolveSecretID), so the build identity's
// grant has to follow the same fact or a build reads a secret it cannot.
func LiveGitSecrets(ctx context.Context, c *Clients, lc *localcfg.Config) (map[string]bool, error) {
	repos, err := CatalogRepos(lc)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	tg := target{project: lc.GCPProject}
	for _, r := range repos {
		logical, ok := GitSecret(r.Provider)
		if !ok {
			continue
		}
		id := gcp.SecretID(r.Slug, logical)
		has, err := hasEnabledVersion(ctx, c.Secrets, tg, id)
		if err != nil {
			return nil, err
		}
		if has {
			out[id] = true
		}
	}
	return out, nil
}

// catalogCheckFor is CatalogCheck's own head: the effective catalog and the
// repositories its entries build in, both nil for a project with nothing to
// check. CatalogBuilds asks it too, so the two can never disagree about
// whether this installation's catalog is one it builds.
func catalogCheckFor(lc *localcfg.Config, containers map[string]string, own map[string]bool) (*config.Catalog, []CatalogCheckRepoSpec, error) {
	cat := lc.ImageCatalogEffective(nil)
	if cat == nil {
		return nil, nil, nil
	}
	repos, err := catalogCheckRepos(lc, cat, containers, own)
	if err != nil {
		return nil, nil, err
	}
	return cat, repos, nil
}

// CatalogBuilds reports whether the effective image catalog has an entry this
// installation builds: the condition CatalogCheck deploys its job under, so
// false exactly when the job is nil for this reason.
//
// The run that has to PIN the base image the job runs FROM asks it (the images
// stage, #1843): the job's spec is built from the local config, and the spec
// the installation root was planned with was built before that pin, so that
// run applies the root again once the base image is recorded — and only when
// the catalog is one this installation builds.
//
// The error catalogCheckRepos can return is a repositories key whose clone URL
// the URL rules refuse, and localcfg's own validation of a repos key rules that
// out: an error here reads as false. A run that ever reached it failed on that
// refusal already, with the sentence that belongs to it, in the spec the run
// builds before its first apply (CatalogCheck).
func CatalogBuilds(lc *localcfg.Config) bool {
	_, repos, err := catalogCheckFor(lc, nil, nil)
	return err == nil && len(repos) > 0
}

// CatalogHasBuildEntries reports whether the effective image catalog has an
// entry a build is submitted for (catalog.BuildsImage): a Dockerfile-sourced or
// an inline definition. It is the condition a caller maintaining the base image
// a build is built FROM is after, and CatalogBuilds is not it: a catalog of
// prebuilt images is one the check job is deployed for — it names those entries
// and passes them over — so CatalogBuilds is true for a catalog this
// installation builds nothing from.
//
// The project whose only entry is the default one `fugaro update` writes
// (D2305, S10) is that catalog, and the distinction is what keeps that write
// from moving base_images.base: the entry's image IS the recorded base pin, so
// a run that took it for a reason to pin would read its own record back as one.
func CatalogHasBuildEntries(lc *localcfg.Config) bool {
	cat := lc.ImageCatalogEffective(nil)
	if cat == nil {
		return false
	}
	for _, e := range cat.Entries {
		if catalog.BuildsImage(e) {
			return true
		}
	}
	return false
}

// CatalogRepos is CatalogBuilds' read as a value: the repositories the
// effective catalog's entries build in, each with the entries assigned to it,
// nil for a project with no catalog to build.
//
// A caller that has to name the checkout an entry's rebuild belongs to reads
// the same assignment the check job is deployed with (the images stage of
// `fugaro update`, which rebuilds the entries of the checkout it runs in and
// names the rest), so the two can never disagree about whose build account an
// entry's build runs as.
func CatalogRepos(lc *localcfg.Config) ([]CatalogCheckRepoSpec, error) {
	_, repos, err := catalogCheckFor(lc, nil, nil)
	return repos, err
}

// catalogCheckRepos are the repositories the catalog's entries build in, each
// with the entries assigned to it: a Dockerfile-sourced entry goes to the
// repository its source names, an inline definition to the repository this
// machine's map puts it on, and an entry whose repository this installation
// cannot name, a prebuilt image among them, to the project's first. Every
// entry is assigned at most once — the job refuses a spec that
// assigns one twice, since the second build would race the first for the
// entry's record.
//
// A repository the installation cannot build with is left out, and an entry
// whose repository is left out is assigned nowhere: the job then names it once
// a night instead of building it as some other repository's account. The
// reasons a repository is left out are the ones the repository root refuses or
// skips for: no provider kind the config names, no workflow to render the
// smoke's spec for, no git credential for its provider, a GitHub repository
// with no App ID, or a name no provider could address.
func catalogCheckRepos(lc *localcfg.Config, cat *config.Catalog, containers map[string]string, own map[string]bool) ([]CatalogCheckRepoSpec, error) {
	var out []CatalogCheckRepoSpec
	// at resolves a canonical repository name to the repository that answers for
	// it: the first of the sorted keys naming it that this installation can build
	// with, which is localcfg.Config.RepoFor's read (sorted keys, first match)
	// narrowed to the keys that can answer at all — two keys can name one
	// repository, only the fold says so, and a key this installation cannot build
	// with must not hide a buildable one beside it. A name no buildable key names
	// resolves to nothing.
	at := map[string]int{}
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		canonical, err := task.CanonicalRepo(name)
		if err != nil {
			continue
		}
		r, ok, err := catalogCheckRepo(lc, name, containers, own)
		if err != nil {
			return nil, err
		}
		if !ok {
			if _, seen := at[canonical]; !seen {
				at[canonical] = -1
			}
			continue
		}
		if i, seen := at[canonical]; seen && i >= 0 {
			continue
		}
		at[canonical] = len(out)
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, nil
	}
	// The map is what a build of an inline entry reads to decide whose checkout
	// it bakes and which repository runs the entry (imagebake.Decide, D1906), so
	// it is what the rebuild has to run as: a definition that bakes a checkout
	// is only reproducible by the repository whose checkout it is.
	//
	// It is THIS machine's map and not the published node's, because that is
	// what this function has: a check job's spec is rendered from the
	// installation root's own inputs (catalogCheckFor takes the local config
	// alone), while a build reads the map over the published one
	// (localcfg.RepoImagesEffective over the policy node). A project whose map
	// lives only on the published node — a second machine published it — then
	// assigns an inline entry to the project's first repository here while the
	// build baked the mapped repository's checkout into it. They disagree in
	// the direction that is safe and self-correcting: the entry falls to the
	// project's first repository, and the job refuses a definition baked for
	// another repository before a build is submitted (internal/cli's catalog
	// check), naming the `fugaro image map set` that makes the two agree and
	// the `fugaro update` that re-renders this spec. Nothing is therefore
	// built under an account that does not reach the checkout a definition
	// bakes; the cost is a night of refusals until the map is set here.
	//
	// A key is read only when this installation serves it (R9): a repo_images
	// key whose provider is not the provider this installation's own repository
	// of that name is on serves nothing, so it is skipped rather than let it
	// assign an entry to a repository that would run it as the wrong account.
	images, fromRepoImages := lc.RepoImagesEffective(nil)
	for _, e := range cat.Entries {
		i := 0 // the project's first repository: the one an entry with none of its own goes to
		switch e.Source.Kind {
		case config.SourceDockerfile:
			canonical, err := task.CanonicalRepo(e.Source.Repo)
			if err != nil {
				continue
			}
			var found bool
			if i, found = at[canonical]; !found {
				// No repository of this installation names the entry's source,
				// so there is no account to build it as: the job names the
				// entry instead of assigning it anywhere.
				continue
			}
		case config.SourceInline:
			// An entry with no repository of its own: the map is the only thing
			// this installation can read that says which repository runs it, so
			// the first mapped repository that can answer is the one its
			// rebuild runs as. A prebuilt image is left with the first
			// repository: the job passes it over without a build.
			for _, key := range slices.Sorted(maps.Keys(images)) {
				if images[key] != e.Name {
					continue
				}
				name, provider := key, ""
				if fromRepoImages {
					p, repo, ok := strings.Cut(key, "/")
					if !ok {
						continue
					}
					name, provider = repo, p
				}
				canonical, err := task.CanonicalRepo(name)
				if err != nil {
					continue
				}
				j, found := at[canonical]
				if !found || j < 0 {
					continue
				}
				if provider != "" && out[j].Provider != provider {
					continue
				}
				i = j
				break
			}
		}
		if i < 0 {
			continue
		}
		out[i].Entries = append(out[i].Entries, e.Name)
	}
	var kept []CatalogCheckRepoSpec
	for _, r := range out {
		if len(r.Entries) > 0 {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// catalogCheckRepo is one repository's part of the assignment, and whether this
// installation can build its entries at all.
func catalogCheckRepo(lc *localcfg.Config, name string, containers map[string]string, own map[string]bool) (CatalogCheckRepoSpec, bool, error) {
	r := lc.Repos[name]
	if !slices.Contains(config.Providers, r.Provider) || len(r.Workflows) == 0 {
		return CatalogCheckRepoSpec{}, false, nil
	}
	slug, err := task.Slug(r.Provider, name)
	if err != nil {
		// Unreachable: localcfg refuses a repos key that is not an owner/name.
		return CatalogCheckRepoSpec{}, false, nil
	}
	url, err := repoURL(r.Provider, name, "")
	if err != nil {
		return CatalogCheckRepoSpec{}, false, err
	}
	logical, ok := GitSecret(r.Provider)
	if !ok {
		return CatalogCheckRepoSpec{}, false, nil
	}
	var id, gitUser string
	switch r.Provider {
	case gitprov.KindBitbucket:
		gitUser = bitbucket.GitUsername
	case gitprov.KindGitHub:
		gitUser = githubGitUser
		if !appIDRE.MatchString(r.GitHubAppID) {
			// The repository root refuses this too (--github-app-id): a GitHub
			// build with no App ID cannot mint the installation token it clones
			// with.
			return CatalogCheckRepoSpec{}, false, nil
		}
	default:
		return CatalogCheckRepoSpec{}, false, nil
	}
	// The credential is the one the repository's own jobs mount, resolved by
	// the one predicate they resolve it through: the project's copy when the
	// name is project-scoped for this repository, the repository's own
	// container otherwise (infra.ProjectSecretCandidate, RepoSpec.MountSecretID).
	//
	// The project's copy only when the installation's plan holds its
	// container: the candidate predicate says a name is eligible, not that a
	// container exists, and a grant on one that does not fails the whole apply
	// with a 404. containers is the set the installation root plans (the
	// config's own names unioned with the live ones, spec.ProjectSecrets), so a
	// copy `fugaro secrets set` made live counts; nil is the config's names
	// alone, which is all a caller without discovery knows.
	if containers == nil {
		containers = projectSecretContainers(lc)
	}
	repoID := gcp.SecretID(slug, logical)
	if projectID, ok := ProjectSecretCandidate(lc, logical, name); ok && containers[logical] == projectID {
		id = projectID
	} else {
		id = repoID
	}
	// A repository's own container that holds a live version wins over the
	// project's copy, as every live resolution decides it (resolveSecretID):
	// the build reads that one. The planned copy keeps its grant (granted), so
	// the plan only ever adds the second one.
	granted := id
	if own[repoID] {
		id = repoID
	}
	branch, _ := config.EffectiveBaseBranch("", false, r.BaseBranch, lc.BaseBranch)
	workflows := slices.Sorted(slices.Values(r.Workflows))
	return CatalogCheckRepoSpec{
		Repo: name, Slug: slug, Provider: r.Provider, RepoURL: url, BaseBranch: branch,
		GitSecretID: id, GitUser: gitUser, GitHubAppID: r.GitHubAppID,
		grantedGitSecretID:  granted,
		Workflow:            workflows[0],
		BuildServiceAccount: serviceAccountEmail(gcp.BuildServiceAccountID(slug), lc.GCPProject),
	}, true, nil
}
