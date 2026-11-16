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
