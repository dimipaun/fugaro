package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// catalogProducerLocal is the local config the round trip below is about: two
// repositories, one per provider, and a catalog with one entry of each source
// kind. The Dockerfile-sourced ones name their own repository; the inline and
// the prebuilt one have no repository of their own.
const catalogProducerLocal = `version: 1
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
  acme/app: { provider: bitbucket, workflows: [web] }
  acme/other: { provider: github, github_app_id: "12345", workflows: [app] }
image_catalog:
  entries:
    - name: tools
      tags: [node]
      source: { kind: dockerfile, repo: acme/app, path: .fugaro/tools.Dockerfile }
    - name: runtime
      tags: [node]
      source: { kind: dockerfile, repo: acme/other, path: .fugaro/runtime.Dockerfile }
    - name: scratch
      tags: [node]
      source: { kind: inline, inline: "FROM scratch\n" }
    - name: prebuilt
      tags: [node]
      source: { kind: image, image: "docker.io/library/node:22" }
`

// catalogCheckVars is the catalog check variable of the installation tfvars for
// yaml, decoded as the installation module's var.catalog_check spells it, and
// the local config it was built from. A nil answer is an installation whose
// tfvars carry no catalog_check at all.
type catalogCheckVars struct {
	AccountID       string            `json:"account_id"`
	Job             string            `json:"job"`
	Image           string            `json:"image"`
	SchedulerJob    string            `json:"scheduler_job"`
	SchedulerRegion string            `json:"scheduler_region"`
	Env             map[string]string `json:"env"`
	Definitions     struct {
		Title      string `json:"title"`
		Expression string `json:"expression"`
	} `json:"definitions_condition"`
	Repositories []struct {
		Slug    string `json:"slug"`
		Records struct {
			Title      string `json:"title"`
			Expression string `json:"expression"`
		} `json:"records_condition"`
	} `json:"repositories"`
}

// installationCatalogCheck renders the installation tfvars for yaml and returns
// the catalog_check block and its local config.
func installationCatalogCheck(t *testing.T, yaml string, o infra.InstallOptions) (*catalogCheckVars, *localcfg.Config) {
	t.Helper()
	lc, err := localcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("the local config: %v", err)
	}
	spec, err := infra.Installation(lc, o)
	if err != nil {
		t.Fatalf("infra.Installation: %v", err)
	}
	vars, err := infra.InstallationVars(spec)
	if err != nil {
		t.Fatalf("infra.InstallationVars: %v", err)
	}
	var doc struct {
		CatalogCheck *catalogCheckVars `json:"catalog_check"`
	}
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatalf("the installation tfvars: %v", err)
	}
	return doc.CatalogCheck, lc
}

// TestCatalogCheckSpecRoundTripsThroughItsReader: the spec fugaro update writes
// into the project's catalog check job is the spec that job reads. Both halves
// declare the document themselves — internal/infra renders it, internal/cli's
// readCatalogJobEnv parses it — so this round trip is the only thing that keeps
// the two in step, and without it the scheduled check (D1148) is inert: the job
// refuses to start, once a night, on bytes the installation wrote.
//
// Mutation (run, restore): change infra.CatalogCheckSpecVersion — or the
// producer's `version` tag — and this test fails on the version it read back.
func TestCatalogCheckSpecRoundTripsThroughItsReader(t *testing.T) {
	cc, lc := installationCatalogCheck(t, catalogProducerLocal, infra.InstallOptions{})
	if cc == nil {
		t.Fatal("the installation tfvars carry no catalog_check, and this project's image catalog has entries to build: without the job, nothing refreshes them (D1148), and this run is the producer that writes its FUGARO_CATALOG_CHECK_SPEC")
	}
	raw := cc.Env[infra.CatalogCheckSpecEnv]
	if raw == "" {
		t.Fatalf("the catalog check job's environment carries no %s: the job refuses to start without it (the module's own validation says so)", infra.CatalogCheckSpecEnv)
	}
	// The job's own environment, read back the way the job reads it.
	for _, name := range []string{infra.ProjectEnv, infra.GCPProjectEnv, infra.RegionEnv, infra.BucketEnv} {
		if cc.Env[name] == "" {
			t.Fatalf("the catalog check job's environment carries no %s: %v", name, cc.Env)
		}
		t.Setenv(name, cc.Env[name])
	}
	t.Setenv(catalogCheckSpecEnv, raw)

	e, err := readCatalogJobEnv()
	if err != nil {
		t.Fatalf("the installed catalog check job would refuse the spec this installation writes: %v", err)
	}
	// The version is a wire value: the reader accepts the document it knows
	// (version 1) and refuses anything below it, so a producer that bumped its
	// own version without the reader's is caught here rather than once a night
	// in a job that starts nothing.
	if e.spec.Version != 1 {
		t.Fatalf("the reader read version %d, and this release's document is version 1 (infra.CatalogCheckSpecVersion): an installation and the job it deploys disagree about the bytes between them", e.spec.Version)
	}
	if e.spec.Catalog == nil || len(e.spec.Catalog.Entries) != 4 {
		t.Fatalf("the reader read the catalog %+v, want the four entries of the local config", e.spec.Catalog)
	}
	if e.name != lc.Name || e.gcpProject != lc.GCPProject || e.region != lc.Region {
		t.Fatalf("the reader read %s/%s/%s, want the installation's %s/%s/%s", e.name, e.gcpProject, e.region, lc.Name, lc.GCPProject, lc.Region)
	}
	if got, want := e.maxImageBuilds, int(managed.DefaultImageBuilds); got != want {
		t.Fatalf("the run's build cap is %d, want M5's default %d: the producer writes no cap for an installation that never read one", got, want)
	}
	if got, want := e.spec.BaseImages[config.BaseKind], lc.BaseImage(config.BaseKind); got != want {
		t.Fatalf("the spec's %s base image is %q, want the local config's %q", config.BaseKind, got, want)
	}

	// Every entry of the catalog is assigned exactly once, and the assignment
	// is what the reader's own guard demands: a Dockerfile-sourced entry to the
	// repository its source names, an entry with no repository of its own to
	// the project's first.
	assigned := map[string]string{}
	var repos []string
	for _, r := range e.spec.Repositories {
		repos = append(repos, r.Repo)
		for _, name := range r.Entries {
			if prev, dup := assigned[name]; dup {
				t.Errorf("the producer assigns the entry %s to both %s and %s: the job refuses a spec that assigns one twice, submitting nothing", name, prev, r.Repo)
			}
			assigned[name] = r.Repo
		}
	}
	want := map[string]string{"tools": "acme/app", "runtime": "acme/other", "scratch": "acme/app", "prebuilt": "acme/app"}
	for name, repo := range want {
		if got := assigned[name]; got != repo {
			t.Errorf("the producer assigns the entry %s to %q, want %q", name, got, repo)
		}
	}
	if len(e.spec.Repositories) != 2 {
		t.Fatalf("the spec names %d repositories (%v), want both of the local config's", len(e.spec.Repositories), repos)
	}

	// And each repository's own facts are the ones the job cross-checks before
	// it submits anything: this fugaro's slug, and the build account that slug
	// has.
	for _, r := range e.spec.Repositories {
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil {
			t.Fatalf("the spec's repository %s: %v", r.Repo, err)
		}
		if r.Slug != slug {
			t.Errorf("the spec names %s as %s, and this fugaro names it %s: the job refuses the whole run over it", r.Repo, r.Slug, slug)
		}
		if want := gcp.BuildServiceAccountID(slug) + "@" + lc.GCPProject + ".iam.gserviceaccount.com"; r.BuildServiceAccount != want {
			t.Errorf("the spec builds %s as %s, want %s", r.Repo, r.BuildServiceAccount, want)
		}
		if r.GitSecretID == "" || r.GitUser == "" || r.RepoURL == "" || r.BaseBranch == "" || r.Workflow == "" {
			t.Errorf("the spec's repository %s is incomplete: %+v", r.Repo, r)
		}
	}

	// The installation's own half: the job the module creates, the image it
	// runs, and the two IAM conditions the plan review reads byte for byte.
	if cc.AccountID != tf.CatalogCheckAccountID {
		t.Errorf("the catalog check account is %q, want %q", cc.AccountID, tf.CatalogCheckAccountID)
	}
	if !strings.HasPrefix(cc.Job, "fugarochk-") {
		t.Errorf("the job is named %q: the module refuses one that does not start with fugarochk- (ls and max_parallel count the fugaro- ones, and the alert policies match that prefix)", cc.Job)
	}
	if cc.Image != lc.BaseImage(config.BaseKind) {
		t.Errorf("the job's image is %q, want the installation's fugaro image %q", cc.Image, lc.BaseImage(config.BaseKind))
	}
	if cc.SchedulerJob == "" || cc.SchedulerRegion == "" {
		t.Errorf("the job's schedule is %q in %q, want both set: the module refuses either empty", cc.SchedulerJob, cc.SchedulerRegion)
	}
	bucket := strings.TrimPrefix(cc.Env[infra.BucketEnv], "gs://")
	if got, want := cc.Definitions, gcp.CatalogReadCondition(bucket); got.Expression != want || got.Title != gcp.CatalogReadConditionTitle(tf.CatalogCheckAccountID) {
		t.Errorf("the definitions grant's condition is %+v, want the catalog read of %s titled for %s", got, want, tf.CatalogCheckAccountID)
	}
	if len(cc.Repositories) != len(e.spec.Repositories) {
		t.Fatalf("the tfvars carry %d repositories, and the spec assigns %d", len(cc.Repositories), len(e.spec.Repositories))
	}
	for _, r := range cc.Repositories {
		if r.Records.Expression != gcp.CatalogRecordsCondition(bucket, r.Slug) || r.Records.Title != gcp.CatalogRecordsConditionTitle(r.Slug) {
			t.Errorf("the records grant of %s is %+v, want %s under the title for %s", r.Slug, r.Records, gcp.CatalogRecordsCondition(bucket, r.Slug), r.Slug)
		}
	}
}

// TestCatalogCheckSpecCarriesTheManagedBuildBound: one run's build cap is M5's
// managed bound (the marker's max_image_builds), which so bounds a managed
// creation's image builds, and an installation that has read no bound writes
// none: the job then applies the default the reader shares with M5.
func TestCatalogCheckSpecCarriesTheManagedBuildBound(t *testing.T) {
	cc, _ := installationCatalogCheck(t, catalogProducerLocal, infra.InstallOptions{ImageBuilds: 7})
	if cc == nil {
		t.Fatal("the installation tfvars carry no catalog_check")
	}
	var doc struct {
		MaxImageBuilds int `json:"max_image_builds"`
	}
	if err := json.Unmarshal([]byte(cc.Env[infra.CatalogCheckSpecEnv]), &doc); err != nil {
		t.Fatalf("%s: %v", infra.CatalogCheckSpecEnv, err)
	}
	if doc.MaxImageBuilds != 7 {
		t.Fatalf("the spec's max_image_builds is %d, want the managed bound 7", doc.MaxImageBuilds)
	}
	for _, name := range []string{infra.ProjectEnv, infra.GCPProjectEnv, infra.RegionEnv, infra.BucketEnv} {
		t.Setenv(name, cc.Env[name])
	}
	t.Setenv(catalogCheckSpecEnv, cc.Env[infra.CatalogCheckSpecEnv])
	e, err := readCatalogJobEnv()
	if err != nil {
		t.Fatalf("readCatalogJobEnv: %v", err)
	}
	if e.maxImageBuilds != 7 {
		t.Fatalf("the run's build cap is %d, want the managed bound 7", e.maxImageBuilds)
	}
}

// TestCatalogCheckSpecVersionIsRefusedWhenItIsNotOne: the version is the
// document's own gate, and a job whose bytes say something else is one nothing
// may decide the catalog from: it starts nothing rather than deciding every
// entry of the project from a document it does not understand.
func TestCatalogCheckSpecVersionIsRefusedWhenItIsNotOne(t *testing.T) {
	cc, _ := installationCatalogCheck(t, catalogProducerLocal, infra.InstallOptions{})
	if cc == nil {
		t.Fatal("the installation tfvars carry no catalog_check")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(cc.Env[infra.CatalogCheckSpecEnv]), &doc); err != nil {
		t.Fatalf("%s: %v", infra.CatalogCheckSpecEnv, err)
	}
	doc["version"] = 0
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{infra.ProjectEnv, infra.GCPProjectEnv, infra.RegionEnv, infra.BucketEnv} {
		t.Setenv(name, cc.Env[name])
	}
	t.Setenv(catalogCheckSpecEnv, string(raw))
	if _, err := readCatalogJobEnv(); err == nil || !strings.Contains(err.Error(), catalogCheckSpecEnv) {
		t.Fatalf("err = %v, want a spec of version 0 refused, naming %s", err, catalogCheckSpecEnv)
	}
}

// TestInstallOptionsReadsTheJobsPausedSettingFromTheLayer: the setting's whole
// path, from the owner's layer file to the installation's own variable. The two
// hops are tested apart — jobsPaused reads the file, internal/infra's own tests
// take the options — so this is the line between them, and the one a change
// could drop with neither of the others noticing.
func TestInstallOptionsReadsTheJobsPausedSettingFromTheLayer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	lc, err := localcfg.Parse([]byte(catalogProducerLocal))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what   string
		layer  string // "" writes no layer file at all
		paused bool
	}{
		{"no layer file", "", true},
		{"a layer that says nothing about jobs", "version: 1\nproject: aurora\ngcp_project: proj-1234\n", true},
		{"a layer that starts the check", "version: 1\nproject: aurora\ngcp_project: proj-1234\njobs:\n  catalog_check: { paused: false }\n", false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			path := localLayerPath(t)
			if tc.layer == "" {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.layer), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			spec, err := installOptions(&initOptions{}, lc)
			if err != nil {
				t.Fatalf("installOptions: %v", err)
			}
			if spec.CatalogCheck == nil {
				t.Fatal("the installation plans no catalog check, and this local config produces one")
			}
			if spec.CatalogCheck.Paused != tc.paused {
				t.Errorf("the installation's catalog check carries paused %v, want %v: the layer is what decides it", spec.CatalogCheck.Paused, tc.paused)
			}
		})
	}
}

// TestTheInstallationTakesTheJobsSettingFromThePublishedLayer: the half of R13's
// setting a local layer file cannot always answer. The installation stage
// applies before the layer stage, and that stage does not always leave the
// file's bytes in place: it seeds a file that is not there FROM the published
// object (D830), and it re-seeds one the object has moved past (D854's stale
// state, where the file's bytes are discarded for the object's). Reading the
// file alone in either state would apply paused — and stop a project-wide
// schedule the published layer has started — until the next update put it back.
// The published object is the project's answer there, and in every other state
// the file wins, whatever it says: it is the one this run publishes.
//
// The read is the layer stage's own (layerPlanFor, and through it
// layerPublishedRead), so the two agree about the bucket and about which state
// this run is in, and a config whose endpoints are fakes reads nothing here
// either.
func TestTheInstallationTakesTheJobsSettingFromThePublishedLayer(t *testing.T) {
	const (
		starts = "version: 1\nproject: aurora\ngcp_project: proj-1234\njobs:\n  catalog_check: { paused: false }\n"
		silent = "version: 1\nproject: aurora\ngcp_project: proj-1234\n"
	)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	lc, err := localcfg.Parse([]byte(catalogProducerLocal))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what      string
		file      string // "" writes no local layer file, "unreadable" one this CLI does not read
		published string // "" publishes no layer at all
		readErr   bool
		// recorded is the generation this machine's config says its layer file
		// was written from; "" is the first run, which has none to compare
		// against. The stub publishes generation 7, so anything else is stale.
		recorded string
		want     bool
		reads    bool // whether the run reads the bucket at all
	}{
		{"no local file, the published layer starts the check", "", starts, false, "", false, true},
		{"no local file, the published layer says nothing", "", silent, false, "", true, true},
		{"no local file, no published layer", "", "", false, "", true, true},
		{"no local file, the bucket cannot be read", "", starts, true, "", true, true},
		{"no local file, a published layer this CLI does not read", "", "version: 1\njobz: {}\n", false, "", true, true},
		{"the local file says nothing, the published one starts the check", silent, starts, false, "", true, true},
		{"the local file starts the check, the published one says nothing", starts, silent, false, "", false, true},
		{"the local file is one this CLI does not read", "unreadable", starts, false, "", true, false},
		{"the local file is stale and says nothing, the published one starts the check", silent, starts, false, "3", false, true},
		{"the local file is stale and starts the check, the published one says nothing", starts, silent, false, "3", true, true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			lc.LayerGeneration = tc.recorded
			path := localLayerPath(t)
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			switch tc.file {
			case "":
			case "unreadable":
				unreadableLayerFile(t, path, "symlink")
			default:
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// The installation's spec as the door builds it — from this file —
			// and then the run's own correction of it, in that order.
			spec, err := installOptions(&initOptions{}, lc)
			if err != nil {
				t.Fatalf("installOptions: %v", err)
			}
			if spec.CatalogCheck == nil {
				t.Fatal("the installation plans no catalog check, and this local config produces one")
			}
			var read bool
			old := layerPublishedRead
			t.Cleanup(func() { layerPublishedRead = old })
			layerPublishedRead = func(context.Context, *localcfg.Config, string) ([]byte, int64, bool, error) {
				read = true
				if tc.readErr {
					return nil, 0, false, errors.New("the bucket answered with a refusal")
				}
				if tc.published == "" {
					return nil, 0, false, nil
				}
				return []byte(tc.published), 7, true, nil
			}
			got := (&initRun{}).publishedJobsPaused(context.Background(), lc, spec)
			if read != tc.reads {
				t.Errorf("the bucket was read: %v, want %v", read, tc.reads)
			}
			if got.CatalogCheck == nil {
				t.Fatal("the spec lost its catalog check")
			}
			if got.CatalogCheck.Paused != tc.want {
				t.Errorf("the installation applies paused %v, want %v", got.CatalogCheck.Paused, tc.want)
			}
		})
	}

	// An installation with no catalog check reads nothing: there is no value to
	// correct, and the read would be a bucket call this run does not owe.
	old := layerPublishedRead
	t.Cleanup(func() { layerPublishedRead = old })
	layerPublishedRead = func(context.Context, *localcfg.Config, string) ([]byte, int64, bool, error) {
		t.Fatal("the bucket was read for an installation with no catalog check")
		return nil, 0, false, nil
	}
	if got := (&initRun{}).publishedJobsPaused(context.Background(), lc, infra.InstallationSpec{}); got.CatalogCheck != nil {
		t.Errorf("a spec with no catalog check came back with one: %+v", got.CatalogCheck)
	}
}
