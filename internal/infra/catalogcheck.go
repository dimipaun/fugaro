package infra

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	// internal/infra/managed is aliased because this package's own tests declare a
	// package-level `managed` (discover_test.go's label map, which a file-block
	// import of that name may not shadow): the bound's spellings are still read
	// from the package that owns them rather than restated here.
	inframanaged "github.com/dimipaun/fugaro/internal/infra/managed"
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
const CatalogCheckSpecVersion = 1

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
// The job's whole assignment is Env's CatalogCheckSpecEnv: the job's account
// holds six grants and no project role at all, so it can read neither the
// published catalog (a database node its account has no role for) nor anything
// else it would have to decide itself (D1148).
type CatalogCheckSpec struct {
	AccountID       string `json:"account_id"`
	Job             string `json:"job"`
	Image           string `json:"image"`
	SchedulerJob    string `json:"scheduler_job"`
	SchedulerRegion string `json:"scheduler_region"`
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
// the job's build records live under, and the IAM condition scoping the grant
// to that slug's catalog area.
type CatalogCheckRepository struct {
	Slug             string    `json:"slug"`
	RecordsCondition Condition `json:"records_condition"`
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
	// settings), not the job's own.
	MachineType string `json:"machine_type"`
	BuildRegion string `json:"build_region"`
	// MaxImageBuilds is this run's build cap: how many Cloud Builds one run of
	// the job may submit, whatever the catalog says changed. It is M5's bound
	// (managed.MaxImageBuilds' range), and zero is the default the reader
	// shares with M5, so an installation that has read no bound writes no cap.
	// It is the bound's own type rather than the int the reader declares: JSON
	// has one number type, and the narrowing belongs nowhere in between.
	MaxImageBuilds int64 `json:"max_image_builds,omitempty"`
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
	// Entries are the catalog entries this repository builds: a
	// Dockerfile-sourced one the repository its source names, an entry with no
	// repository of its own the project's first.
	Entries []string `json:"entries"`
}

// CatalogCheck is the project's catalog check job for lc, or nil when the
// project has nothing to check: no effective catalog, no entry whose
// repository this installation can build with, or no base image recorded for
// the job (and the builds) to run FROM. bucket is the installation's
// validated runs bucket, and imageBuilds is M5's managed bound — the marker's
// max_image_builds, or zero when the installation has read none, which the job
// then holds itself to M5's default of one build per run (the reader's own
// default, the one an absent max_image_builds means).
//
// The job is what makes the catalog check run at all: nothing else submits a
// build for an entry nobody pushed by hand, so the installation either deploys
// this job (and its schedule) or leaves every entry of the project's catalog
// exactly as it is.
func CatalogCheck(lc *localcfg.Config, bucket string, imageBuilds int64) (*CatalogCheckSpec, error) {
	cat, repos, err := catalogCheckFor(lc)
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
	if imageBuilds != 0 {
		if err := inframanaged.CheckImageBuilds(imageBuilds); err != nil {
			// A count outside M5's range would deploy a job whose own reader
			// refuses it: the installation would look updated and check nothing,
			// once a night, silently.
			return nil, userErr("%v", err)
		}
	}
	region := lc.SchedulerRegion
	if region == "" {
		r, err := gcp.SchedulerRegion(lc.Region)
		if err != nil {
			return nil, userErr("%v", err)
		}
		region = r
	}
	raw, err := json.Marshal(CatalogCheckJobSpec{
		Version:        CatalogCheckSpecVersion,
		Catalog:        cat,
		Repositories:   repos,
		BaseImages:     maps.Clone(lc.BaseImages),
		MachineType:    lc.Build.MachineType,
		BuildRegion:    lc.BuildRegion(),
		MaxImageBuilds: imageBuilds,
	})
	if err != nil {
		return nil, err
	}
	env := platformEnvFor(lc, lc.Name, bucket)
	env[CatalogCheckSpecEnv] = string(raw)
	out := &CatalogCheckSpec{
		AccountID:       tf.CatalogCheckAccountID,
		Job:             CatalogCheckJob,
		Image:           image,
		SchedulerJob:    CatalogCheckSchedulerJob,
		SchedulerRegion: region,
		Env:             env,
		DefinitionsCondition: Condition{
			Title:      gcp.CatalogReadConditionTitle(tf.CatalogCheckAccountID),
			Expression: gcp.CatalogReadCondition(bucket),
		},
		Repositories: make([]CatalogCheckRepository, 0, len(repos)),
	}
	for _, r := range repos {
		out.Repositories = append(out.Repositories, CatalogCheckRepository{
			Slug: r.Slug,
			RecordsCondition: Condition{
				Title:      gcp.CatalogRecordsConditionTitle(r.Slug),
				Expression: gcp.CatalogRecordsCondition(bucket, r.Slug),
			},
		})
	}
	return out, nil
}

// catalogCheckFor is CatalogCheck's own head: the effective catalog and the
// repositories its entries build in, both nil for a project with nothing to
// check. CatalogBuilds asks it too, so the two can never disagree about
// whether this installation's catalog is one it builds.
func catalogCheckFor(lc *localcfg.Config) (*config.Catalog, []CatalogCheckRepoSpec, error) {
	cat := lc.ImageCatalogEffective(nil)
	if cat == nil {
		return nil, nil, nil
	}
	repos, err := catalogCheckRepos(lc, cat)
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
	_, repos, err := catalogCheckFor(lc)
	return err == nil && len(repos) > 0
}

// catalogCheckRepos are the repositories the catalog's entries build in, each
// with the entries assigned to it: a Dockerfile-sourced entry goes to the
// repository its source names, and an entry with no repository of its own (an
// inline definition, a prebuilt image) to the project's first. Every entry is
// assigned at most once — the job refuses a spec that assigns one twice, since
// the second build would race the first for the entry's record.
//
// A repository the installation cannot build with is left out, and an entry
// whose repository is left out is assigned nowhere: the job then names it once
// a night instead of building it as some other repository's account. The
// reasons a repository is left out are the ones the repository root refuses or
// skips for: no provider kind the config names, no workflow to render the
// smoke's spec for, no git credential for its provider, a GitHub repository
// with no App ID, or a name no provider could address.
func catalogCheckRepos(lc *localcfg.Config, cat *config.Catalog) ([]CatalogCheckRepoSpec, error) {
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
		r, ok, err := catalogCheckRepo(lc, name)
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
	for _, e := range cat.Entries {
		i := 0 // the project's first repository: the one an entry with none of its own goes to
		if e.Source.Kind == config.SourceDockerfile {
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
func catalogCheckRepo(lc *localcfg.Config, name string) (CatalogCheckRepoSpec, bool, error) {
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
	if projectID, ok := ProjectSecretCandidate(lc, logical, name); ok {
		id = projectID
	} else {
		id = gcp.SecretID(slug, logical)
	}
	branch, _ := config.EffectiveBaseBranch("", false, r.BaseBranch, lc.BaseBranch)
	workflows := slices.Sorted(slices.Values(r.Workflows))
	return CatalogCheckRepoSpec{
		Repo: name, Slug: slug, Provider: r.Provider, RepoURL: url, BaseBranch: branch,
		GitSecretID: id, GitUser: gitUser, GitHubAppID: r.GitHubAppID,
		Workflow:            workflows[0],
		BuildServiceAccount: serviceAccountEmail(gcp.BuildServiceAccountID(slug), lc.GCPProject),
	}, true, nil
}
