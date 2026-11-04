package infra

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/task"
)

// catalogCheckLocal is the local config the producer's tests are about: two
// repositories it can build with (one per provider, one of them declaring two
// workflows), one it cannot (no provider kind), and a catalog with an entry of
// each source kind — one of them naming a repository the config does not have.
const catalogCheckLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
registry_host: us-east5-docker.pkg.dev/proj-1234
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
repos:
  acme/app: { provider: bitbucket, workflows: [web, smoke] }
  acme/none: { workflows: [web] }
  acme/other: { provider: github, github_app_id: "12345", workflows: [app] }
image_catalog:
  entries:
    - name: tools
      tags: [node]
      source: { kind: dockerfile, repo: acme/app, path: .fugaro/tools.Dockerfile }
    - name: runtime
      tags: [node]
      source: { kind: dockerfile, repo: acme/other, path: .fugaro/runtime.Dockerfile }
    - name: stale
      tags: [node]
      source: { kind: dockerfile, repo: acme/gone, path: .fugaro/stale.Dockerfile }
    - name: scratch
      tags: [node]
      source: { kind: inline, inline: "FROM scratch\n" }
    - name: prebuilt
      tags: [node]
      source: { kind: image, image: "docker.io/library/node:22" }
`

// catalogCheckJob is the assignment of the job CatalogCheck renders for yaml,
// decoded from the environment variable the job reads it from.
func catalogCheckJob(t *testing.T, yaml string, imageBuilds int64) (*CatalogCheckSpec, CatalogCheckJobSpec) {
	t.Helper()
	lc := parseLC(t, yaml)
	cc, err := CatalogCheck(lc, "fugaro-runs-proj-1234", imageBuilds)
	if err != nil {
		t.Fatalf("CatalogCheck: %v", err)
	}
	if cc == nil {
		t.Fatal("CatalogCheck planned no job, want the project's catalog check job")
	}
	raw := cc.Env[CatalogCheckSpecEnv]
	if raw == "" {
		t.Fatalf("the job's environment carries no %s", CatalogCheckSpecEnv)
	}
	var spec CatalogCheckJobSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("%s: %v", CatalogCheckSpecEnv, err)
	}
	return cc, spec
}

// repoOf is the spec's entry for repo, and the entries it builds.
func repoOf(t *testing.T, spec CatalogCheckJobSpec, repo string) CatalogCheckRepoSpec {
	t.Helper()
	for _, r := range spec.Repositories {
		if r.Repo == repo {
			return r
		}
	}
	t.Fatalf("the spec names no repository %s: %+v", repo, spec.Repositories)
	return CatalogCheckRepoSpec{}
}

// TestCatalogCheckAssignsEveryEntry: an entry goes to the repository that can
// build it — its Dockerfile's repository, or the project's first for an entry
// that has none of its own — and one no repository of this installation names
// is assigned nowhere rather than to a repository whose account would build
// another repository's Dockerfile.
func TestCatalogCheckAssignsEveryEntry(t *testing.T) {
	cc, spec := catalogCheckJob(t, catalogCheckLocal, 0)
	if spec.Version != CatalogCheckSpecVersion {
		t.Errorf("the spec is version %d, want %d", spec.Version, CatalogCheckSpecVersion)
	}
	if spec.Catalog == nil || len(spec.Catalog.Entries) != 5 {
		t.Fatalf("the spec's catalog is %+v, want the local config's five entries", spec.Catalog)
	}
	var named []string
	for _, r := range spec.Repositories {
		named = append(named, r.Repo)
	}
	if want := []string{"acme/app", "acme/other"}; !slices.Equal(named, want) {
		t.Fatalf("the spec names the repositories %v, want %v: a repository with no provider kind, and one with no workflow, are ones no build of this installation can run as", named, want)
	}
	app, other := repoOf(t, spec, "acme/app"), repoOf(t, spec, "acme/other")
	if want := []string{"tools", "scratch", "prebuilt"}; !slices.Equal(app.Entries, want) {
		t.Errorf("acme/app builds %v, want %v (its own Dockerfile's entry, then the entries with no repository of their own, in the catalog's order)", app.Entries, want)
	}
	if want := []string{"runtime"}; !slices.Equal(other.Entries, want) {
		t.Errorf("acme/other builds %v, want %v", other.Entries, want)
	}
	for _, r := range spec.Repositories {
		for _, name := range r.Entries {
			if name == "stale" {
				t.Errorf("%s builds the entry stale, whose Dockerfile is acme/gone's: no repository of this installation names it", r.Repo)
			}
		}
	}

	// Each repository's own facts, which the job cross-checks before it
	// submits anything and passes to the build it submits.
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if app.Slug != slug || app.BuildServiceAccount != gcp.BuildServiceAccountID(slug)+"@proj-1234.iam.gserviceaccount.com" {
		t.Errorf("acme/app is %s built as %s, want %s and its own build account", app.Slug, app.BuildServiceAccount, slug)
	}
	if app.Provider != gitprov.KindBitbucket || app.GitUser != bitbucket.GitUsername ||
		app.RepoURL != "https://bitbucket.org/acme/app.git" || app.BaseBranch != "main" {
		t.Errorf("acme/app's build facts are %+v, want the repository's own", app)
	}
	// The credential is the container the repository's own jobs mount: this
	// config stores no bitbucket token project-wide, and that name is
	// per-repository by default (config.PerRepositorySecret).
	if want := gcp.SecretID(slug, config.BitbucketSecret); app.GitSecretID != want {
		t.Errorf("acme/app's git credential is %s, want its own container %s", app.GitSecretID, want)
	}
	// The smoke's spec is rendered for the first of the repository's workflow
	// names, as the manual path renders it for the same reason: an entry is not
	// a workflow.
	if app.Workflow != "smoke" {
		t.Errorf("acme/app's workflow is %q, want the first of its sorted names (smoke)", app.Workflow)
	}
	if other.GitHubAppID != "12345" || other.GitUser != githubGitUser {
		t.Errorf("acme/other's build facts are %+v, want its App ID and the App's own git user", other)
	}

	// The job's own half, and the two conditions the plan review reads byte
	// for byte.
	if cc.AccountID != tf.CatalogCheckAccountID || !strings.HasPrefix(cc.Job, "fugarochk-") {
		t.Errorf("the job is %s as %s, want %s as a fugarochk- job", cc.Job, cc.AccountID, tf.CatalogCheckAccountID)
	}
	if cc.Image != "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc" {
		t.Errorf("the job's image is %q, want the installation's fugaro image", cc.Image)
	}
	if got, want := cc.DefinitionsCondition.Expression, gcp.CatalogReadCondition("fugaro-runs-proj-1234"); got != want {
		t.Errorf("the definitions condition is %q, want %q", got, want)
	}
	if got, want := cc.DefinitionsCondition.Title, gcp.CatalogReadConditionTitle(tf.CatalogCheckAccountID); got != want {
		t.Errorf("the definitions title is %q, want %q", got, want)
	}
	if len(cc.Repositories) != 2 {
		t.Fatalf("the tfvars carry the records grant of %d repositories, want one per repository the spec names", len(cc.Repositories))
	}
	for _, r := range cc.Repositories {
		if want := gcp.CatalogRecordsCondition("fugaro-runs-proj-1234", r.Slug); r.RecordsCondition.Expression != want {
			t.Errorf("the records condition of %s is %q, want %q", r.Slug, r.RecordsCondition.Expression, want)
		}
		if want := gcp.CatalogRecordsConditionTitle(r.Slug); r.RecordsCondition.Title != want {
			t.Errorf("the records title of %s is %q, want %q", r.Slug, r.RecordsCondition.Title, want)
		}
	}
	// The job reads its whole assignment from its environment, and an
	// installation that carries the spec without the platform variables would
	// deploy a job that cannot name the project it checks.
	for _, name := range []string{BucketEnv, BackendEnv, GCPProjectEnv, ProjectEnv, RegionEnv} {
		if cc.Env[name] == "" {
			t.Errorf("the job's environment carries no %s: %v", name, cc.Env)
		}
	}
}

// TestCatalogCheckUsesTheProjectCopyOfAProjectScopedCredential: an operator who
// stores a provider's credential once for the project stores it in the
// project's own container, and the repository's build mounts that one — the
// precedence the repository module's Terraform merge takes
// (RepoSpec.MountSecretID).
func TestCatalogCheckUsesTheProjectCopyOfAProjectScopedCredential(t *testing.T) {
	lc := parseLC(t, catalogCheckLocal+`project_secrets:
  bitbucket-token: { allow_data_to: [acme/app] }
`)
	cc, err := CatalogCheck(lc, "fugaro-runs-proj-1234", 0)
	if err != nil {
		t.Fatalf("CatalogCheck: %v", err)
	}
	if cc == nil {
		t.Fatal("CatalogCheck planned no job")
	}
	var spec CatalogCheckJobSpec
	if err := json.Unmarshal([]byte(cc.Env[CatalogCheckSpecEnv]), &spec); err != nil {
		t.Fatalf("%s: %v", CatalogCheckSpecEnv, err)
	}
	app := repoOf(t, spec, "acme/app")
	if want := gcp.ProjectSecretID("aurora", config.BitbucketSecret); app.GitSecretID != want {
		t.Errorf("acme/app's git credential is %s, want the project's copy %s", app.GitSecretID, want)
	}
	// ... and the GitHub App key is the same answer with no entry at all: a
	// provider key is project-scoped by default (config.ProjectScopeByDefault),
	// so both repositories mount the project's copy.
	if other := repoOf(t, spec, "acme/other"); other.GitSecretID != gcp.ProjectSecretID("aurora", config.GitHubAppKeySecret) {
		t.Errorf("acme/other's git credential is %s, want the project's copy of %s", other.GitSecretID, config.GitHubAppKeySecret)
	}
}

// TestCatalogCheckIsNilWhenThereIsNothingToCheck: an installation whose project
// publishes no catalog, and one whose catalog names only repositories it does
// not have, plan exactly what they planned before the job existed — the
// module's own null, whose repositories list must not be empty.
func TestCatalogCheckIsNilWhenThereIsNothingToCheck(t *testing.T) {
	for _, tc := range []struct {
		what, yaml string
	}{
		{"no catalog at all", ""},
		{"entries whose repository the config does not have", `image_catalog:
  entries:
    - name: stale
      tags: [node]
      source: { kind: dockerfile, repo: acme/gone, path: .fugaro/stale.Dockerfile }
`},
	} {
		t.Run(tc.what, func(t *testing.T) {
			lc := parseLC(t, m4LocalConfig+m5Additions+tc.yaml)
			cc, err := CatalogCheck(lc, "fugaro-runs-proj-1234", 0)
			if err != nil {
				t.Fatalf("CatalogCheck: %v", err)
			}
			if cc != nil {
				t.Fatalf("CatalogCheck planned %+v, want no job", cc)
			}
			spec, err := Installation(lc, InstallOptions{})
			if err != nil {
				t.Fatalf("Installation: %v", err)
			}
			vars, err := InstallationVars(spec)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(vars), "catalog_check") {
				t.Errorf("the installation tfvars name catalog_check, want the key left out:\n%s", vars)
			}
		})
	}
}

// TestCatalogCheckRefusesAMissingBaseImage: the job runs the installation's own
// fugaro image and every build it submits is FROM it, so an installation that
// has a catalog to build and no such image cannot be given a job — the same
// refusal the repository's own daily check makes for the kinds its workflows
// build FROM.
func TestCatalogCheckRefusesAMissingBaseImage(t *testing.T) {
	lc := parseLC(t, strings.Replace(catalogCheckLocal,
		"base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }\n", "", 1))
	_, err := CatalogCheck(lc, "fugaro-runs-proj-1234", 0)
	if err == nil || !strings.Contains(err.Error(), "base_images."+config.BaseKind) {
		t.Fatalf("err = %v, want the missing base image named with the key that fixes it", err)
	}
	// ... and with no catalog to build, no job and no refusal: the image is the
	// job's own need.
	lc = parseLC(t, m4LocalConfig)
	if cc, err := CatalogCheck(lc, "fugaro-runs-proj-1234", 0); err != nil || cc != nil {
		t.Fatalf("CatalogCheck with no catalog = %+v, %v, want no job and no error", cc, err)
	}
}

// TestCatalogCheckCarriesTheManagedBound: one run's build cap is M5's managed
// bound, and a bound outside M5's range is refused rather than written into a
// job the reader would refuse to start.
func TestCatalogCheckCarriesTheManagedBound(t *testing.T) {
	for _, tc := range []struct {
		builds int64
		want   int
	}{{0, 0}, {7, 7}, {1000, 1000}} {
		_, spec := catalogCheckJob(t, catalogCheckLocal, tc.builds)
		if spec.MaxImageBuilds != tc.want {
			t.Errorf("the managed bound %d is written as max_image_builds %d, want %d (zero is M5's default, which the job applies itself)", tc.builds, spec.MaxImageBuilds, tc.want)
		}
	}
	lc := parseLC(t, catalogCheckLocal)
	if _, err := CatalogCheck(lc, "fugaro-runs-proj-1234", 1001); err == nil || !strings.Contains(err.Error(), "1001") {
		t.Fatalf("err = %v, want an out-of-range build bound refused", err)
	}
	if _, err := CatalogCheck(lc, "Fugaro Runs", 0); err == nil {
		t.Fatal("a runs bucket outside [a-z0-9._-] was accepted: the condition built from it would be one no comparison expects")
	}
}
