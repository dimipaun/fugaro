package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const sandboxCatalogYAML = `image_catalog:
  entries:
    - name: sandbox-web
      tags: [node]
      source: { kind: inline, inline: "FROM scratch" }
`

func writeEntryBaseRecord(t *testing.T, f *cloudFixture, slug, entry, baseRef string) {
	t.Helper()
	data, err := json.Marshal(imagecheck.CatalogRecordState{Version: imagecheck.CatalogRecordVersion, Entry: entry, BaseRef: baseRef})
	if err != nil {
		t.Fatal(err)
	}
	key, err := gcp.CatalogRecordKey(slug, entry)
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, key, string(data))
}

// A repository that runs a catalog entry is judged by the entry's build
// record. Its per-workflow record can never be refreshed while repo-level
// images are disabled, so judging it refused every launch of such a
// repository for good.
//
// Mutation (drop the launchCatalogEntry branch in checkImageSinceDev): the
// first row is refused on the stale per-workflow record.
func TestImageSinceJudgesTheCatalogEntryRecordOfACatalogImage(t *testing.T) {
	const (
		stale   = "ghcr.io/dimipaun/fugaro-base:0.22.1"
		current = "ghcr.io/dimipaun/fugaro-base:0.24.0"
	)
	spec := &task.Spec{Version: 1, RunID: "20261103-100000-a1b2", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "Add a feature"}
	for _, tc := range []struct {
		name   string
		entry  string
		refuse bool
	}{
		{"the entry's record is current, the workflow's is stale", current, false},
		{"the entry's record is stale", stale, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			f.appendConfig(t, sandboxCatalogYAML)
			dockerCheckout(t, needsNode)
			writeBuildRecord(t, f, appSlug, "web", stale)
			writeEntryBaseRecord(t, f, appSlug, "sandbox-web", tc.entry)
			env := fileEnv(t, f)
			env.be = deployedJob{codec: backend.CloudRunCodec(), image: catalogImageOf("sandbox-web")}
			err := checkImageSince(context.Background(), env, appSlug, spec, "", "0.24.0", "the published policy node (version 7)", "policy version 7", "", io.Discard)
			switch {
			case !tc.refuse && err != nil:
				t.Fatalf("a launch whose catalog entry is built on a current base was refused: %v", err)
			case tc.refuse && err == nil:
				t.Fatal("a launch whose catalog entry is built on an old base was admitted")
			case tc.refuse:
				for _, want := range []string{"sandbox-web", "0.22.1", "fugaro image build sandbox-web"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
				if strings.Contains(err.Error(), "fugaro update") {
					t.Errorf("the refusal sends the owner to a command that cannot rebuild the entry: %v", err)
				}
			}
		})
	}
}

// A repository whose image is its own is judged by its per-workflow record as
// ever, whatever catalog the project has.
func TestImageSinceKeepsTheWorkflowRecordForARepoImage(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: "20261103-100000-a1b2", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "Add a feature"}
	f := newCloudFixture(t)
	dockerCheckout(t, needsNode)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.22.1")
	env := fileEnv(t, f)
	env.be = deployedJob{codec: backend.CloudRunCodec(), image: "us-east5-docker.pkg.dev/proj-1234/acme-app/web:latest"}
	err := checkImageSince(context.Background(), env, appSlug, spec, "", "0.24.0", "x", "y", "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "fugaro update") {
		t.Fatalf("err = %v, want the per-workflow refusal naming fugaro update", err)
	}
}

// mappedToSandboxWeb is the project's repo-to-image map naming the entry the
// map tests below deploy the job with (D1906).
const mappedToSandboxWeb = "image_map:\n  acme/app: sandbox-web\n"

// cloneRepoYAML is a fugaro.yaml whose web workflow runs the installation's
// base image (checkout: clone): the file's own answer for the job, which the
// project's map does not move onto an entry (JobImage's order, T1 b).
const cloneRepoYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { checkout: clone, commands: { build: sh b.sh, test: sh t.sh } }\n"

// launchOutsideAnyCheckout puts the test in a directory that is not a
// checkout of anything: the `fugaro run --repo` a person or the desktop
// console makes from anywhere, which is the launch with no workflow file to
// read.
func launchOutsideAnyCheckout(t *testing.T) {
	t.Helper()
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
}

// A repository the project's map gives a catalog entry is judged by that
// entry's record even when the launch holds no checkout of the repository:
// the map is what the deploy-time spec builder read when it gave the job the
// entry's image (D1906), so the entry is the only record there is to judge —
// the per-workflow record is one `fugaro image build --workflow` and `image
// refresh` refuse to write while repo-level images are disabled (D1900), and
// reading it refused every launch of a catalog repository from outside its
// checkout, in the wording of a record that can never exist.
//
// Mutation (drop the map fallback in deployedCatalogEntry): both refusing
// rows are refused on the stale per-workflow record instead, in the
// per-repository wording naming fugaro update.
func TestImageSinceJudgesTheEntryRecordOfAMappedRepoWithoutACheckout(t *testing.T) {
	const (
		stale   = "ghcr.io/dimipaun/fugaro-base:0.22.1"
		current = "ghcr.io/dimipaun/fugaro-base:0.24.0"
	)
	spec := &task.Spec{Version: 1, RunID: "20261124-100000-a1b2", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "Add a feature"}
	for _, tc := range []struct {
		name      string
		entryBase string // "" writes no record for the entry at all
		refuse    bool
	}{
		{"the entry's record is current", current, false},
		{"the entry's record is stale", stale, true},
		{"the entry has no record", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			launchOutsideAnyCheckout(t)
			f.appendConfig(t, sandboxCatalogYAML)
			f.appendConfig(t, mappedToSandboxWeb)
			// The per-workflow record is stale in every row: nothing refreshes
			// it while repo-level images are disabled, and reading it is the
			// bug this pins.
			writeBuildRecord(t, f, appSlug, "web", stale)
			if tc.entryBase != "" {
				writeEntryBaseRecord(t, f, appSlug, "sandbox-web", tc.entryBase)
			}
			env := fileEnv(t, f)
			env.be = deployedJob{codec: backend.CloudRunCodec(), image: catalogImageOf("sandbox-web")}
			err := checkImageSince(context.Background(), env, appSlug, spec, "", "0.24.0", "the published policy node (version 7)", "policy version 7", "", io.Discard)
			switch {
			case !tc.refuse && err != nil:
				t.Fatalf("a launch whose catalog entry is built on a current base was refused: %v", err)
			case tc.refuse && err == nil:
				t.Fatal("a launch whose catalog entry is older than the gate was admitted")
			case tc.refuse:
				for _, want := range []string{"the project's catalog entry sandbox-web", "fugaro image build sandbox-web"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
				if strings.Contains(err.Error(), "fugaro update") {
					t.Errorf("the refusal sends the owner to a command that cannot rebuild the entry: %v", err)
				}
			}
		})
	}
}

// A repository the project's map does not name, launched with no checkout, is
// judged by its own record as ever: what the map does not claim about the
// repository, a launcher with no file cannot claim either, and the deployed
// image's being an entry's is not the answer — a repository the project gives
// its own image has one, and the record describing that image is the one to
// read.
func TestImageSinceKeepsTheWorkflowRecordForAnUnmappedRepoWithoutACheckout(t *testing.T) {
	const (
		stale   = "ghcr.io/dimipaun/fugaro-base:0.22.1"
		current = "ghcr.io/dimipaun/fugaro-base:0.24.0"
	)
	spec := &task.Spec{Version: 1, RunID: "20261124-100000-a1b2", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "Add a feature"}
	for _, tc := range []struct {
		name     string
		repoBase string
		refuse   bool
	}{
		{"this repository's record is current", current, false},
		{"this repository's record is stale", stale, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			launchOutsideAnyCheckout(t)
			f.appendConfig(t, sandboxCatalogYAML)
			writeBuildRecord(t, f, appSlug, "web", tc.repoBase)
			// The entry's record is stale: a launcher that read it instead
			// would refuse the first row, which is the map's absence ignored.
			writeEntryBaseRecord(t, f, appSlug, "sandbox-web", stale)
			env := fileEnv(t, f)
			env.be = deployedJob{codec: backend.CloudRunCodec(), image: catalogImageOf("sandbox-web")}
			err := checkImageSince(context.Background(), env, appSlug, spec, "", "0.24.0", "x", "y", "", io.Discard)
			switch {
			case !tc.refuse && err != nil:
				t.Fatalf("a launch whose own record is current was refused: %v", err)
			case tc.refuse && err == nil:
				t.Fatal("a launch whose own record is stale was admitted")
			case tc.refuse:
				if !strings.Contains(err.Error(), "fugaro update") {
					t.Errorf("the refusal does not name fugaro update, so it is not the per-workflow one: %v", err)
				}
				if strings.Contains(err.Error(), "the project's catalog entry") {
					t.Errorf("an unmapped repository was judged by a catalog entry's record: %v", err)
				}
			}
		})
	}
}

// A launch with a checkout of the repository is decided by the file, the map
// or no map: a workflow that runs the installation's base image
// (checkout: clone) keeps this repository's own record even where the
// project's map names the entry whose image the fixture's job carries — the
// map is the project's answer for a repository whose file says nothing about
// the image, and the fallback that reads it without a checkout must not
// override a file this launcher has (JobImage's own order, T1 b).
func TestImageSinceKeepsTheFileDecidingWithACheckout(t *testing.T) {
	const (
		stale   = "ghcr.io/dimipaun/fugaro-base:0.22.1"
		current = "ghcr.io/dimipaun/fugaro-base:0.24.0"
	)
	spec := &task.Spec{Version: 1, RunID: "20261124-100000-a1b2", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "Add a feature"}
	f := newCloudFixture(t)
	f.appendConfig(t, sandboxCatalogYAML)
	f.appendConfig(t, mappedToSandboxWeb)
	baseBranchCheckout(t, "acme/app", cloneRepoYAML, "main")
	writeBuildRecord(t, f, appSlug, "web", stale)
	writeEntryBaseRecord(t, f, appSlug, "sandbox-web", current)
	env := fileEnv(t, f)
	env.be = deployedJob{codec: backend.CloudRunCodec(), image: catalogImageOf("sandbox-web")}
	err := checkImageSince(context.Background(), env, appSlug, spec, "", "0.24.0", "x", "y", "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "fugaro update") {
		t.Fatalf("err = %v, want the per-workflow refusal naming fugaro update, the file's own answer kept", err)
	}
}

// mappedEntryName reads the map under the repository's canonical form, which
// is the form the map's keys are validated in and the form the resolver looks
// a repository up by: a `--repo` spelled in another case must reach the same
// entry, and an empty name is no mapping.
func TestMappedEntryNameReadsTheMapCanonically(t *testing.T) {
	m := map[string]string{"acme/app": "sandbox-web", "acme/blank": ""}
	for _, tc := range []struct {
		name, repo string
		want       string
		ok         bool
	}{
		{"as the map is written", "acme/app", "sandbox-web", true},
		{"in another case", "Acme/App", "sandbox-web", true},
		{"not in the map", "acme/other", "", false},
		{"a name that is no repository", "not a repo", "", false},
		{"a blank name is no mapping", "acme/blank", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := mappedEntryName(m, tc.repo)
			if got != tc.want || ok != tc.ok {
				t.Errorf("mappedEntryName(%v, %q) = %q, %v; want %q, %v", m, tc.repo, got, ok, tc.want, tc.ok)
			}
		})
	}
	if _, ok := mappedEntryName(nil, "acme/app"); ok {
		t.Error("a project that maps nothing named an entry")
	}
}
