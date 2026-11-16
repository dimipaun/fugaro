package cli

import (
	"context"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// mappedCatalogYAML is the project whose map names one entry for acme/app
// (D1906) in a catalog that marks no default: the map is therefore the only
// thing that can give a workflow of this repository an image, and the tag-cover
// rule the only thing that can take one away from it.
// The entries are ones Fugaro builds: a baked job's image has to hold the
// repository's checkout, which a prebuilt reference cannot promise (D1191).
const mappedCatalogYAML = `image_catalog:
  entries:
    - name: minimal
      tags: [node]
      source: { kind: inline, inline: "FROM scratch\n" }
    - name: postgres
      tags: [node, postgres]
      source: { kind: inline, inline: "FROM scratch\n" }
image_map:
  acme/app: postgres
`

// mappedJob is a backend whose deployed job carries whatever image the test
// put in it: the launcher reads the deployed job, and the two workflows this
// fixture judges are deployed with different images one after the other.
type mappedJob struct {
	backend.Backend
	codec backend.Codec
	image string
}

func (b *mappedJob) Codec() backend.Codec { return b.codec }

func (b *mappedJob) JobImage(context.Context, string) (string, bool, error) {
	return b.image, true, nil
}

// TestJobImageCallersAgree is F21: the three callers of the selection — the
// deploy-time job spec (internal/infra), doctor and the launcher — ask one
// resolver, so all three name the same entry for the same workflow. A caller
// left on its own reading of the keys would judge a job by another entry the
// moment the project's map moves it (D1906): a run refused for an image it
// does not have, or asked for a Docker socket it never needed.
//
// The repository's two workflows are the two ways the map is beaten and the
// way it is not: web keeps an image definition, which is ignored (D1900), so
// the map gives its job the entry postgres; api needs a tag, which is a
// catalog reference and beats the map (T1 b), so the tag-cover rule gives its
// job minimal.
func TestJobImageCallersAgree(t *testing.T) {
	const yaml = `version: 1
project: aurora
git: { provider: github }
workflows:
  web: { image: { apt: [jq] }, commands: { build: sh b.sh, test: sh t.sh } }
  api: { needs: [node], commands: { build: sh b.sh, test: sh t.sh } }
`
	f := newCloudFixture(t)
	baseBranchCheckout(t, "acme/app", yaml, "main")
	// The launcher's own environment: the fixture config with the catalog and
	// the map appended, read the way the launcher reads it. The deployed
	// image is the test's to move, because the two workflows are judged one
	// after the other (deployedJob answers for one job name).
	be := &mappedJob{codec: backend.CloudRunCodec()}
	env := catalogLaunchEnv(t, f, mappedCatalogYAML, be)
	lc := env.lc
	lc.Repos["acme/app"] = localcfg.Repo{Provider: "github", BaseBranch: "main", Workflows: []string{"web", "api"}, GitHubAppID: "123456"}
	cfg, problems := config.Parse([]byte(yaml))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	spec, err := infra.Repo(infra.Inputs{LC: lc, Repo: "acme/app", Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	cat := lc.ImageCatalogEffective(nil)
	imap := lc.ImageMapEffective(nil)
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	// The image the deploy would give a workflow whose own keys decide:
	// the per-repository tag, which no catalog entry is.
	derived := gcp.ImageName(gcp.DefaultRegistryHost("us-east5", "proj-1234")+"/"+gcp.RegistryRepoID(slug), slug, "web") + ":latest"

	for _, tt := range []struct {
		workflow string
		entry    string
	}{
		{"web", "postgres"},
		{"api", "minimal"},
	} {
		t.Run(tt.workflow, func(t *testing.T) {
			ws := spec.Workflows[tt.workflow]
			if ws.CatalogEntry != tt.entry {
				t.Fatalf("the deploy-time spec runs entry %q, want %q: the job spec is where the image is fixed (F7)", ws.CatalogEntry, tt.entry)
			}
			// doctor's answer, asked of the same project: the check that names
			// a workflow's image is the one that must not disagree.
			e, ok := workflowCatalogEntry(cat, imap, false, "acme/app", cfg.Workflows[tt.workflow], catalog.Backend(lc.Backend()))
			if !ok || e.Name != tt.entry {
				t.Fatalf("doctor reads the workflow as entry %q (judged=%v), want %q: the image-config and image-catalog checks are the ones that disagree", e.Name, ok, tt.entry)
			}
			// The launcher, judging the image the deploy gave this job: the
			// entry it reads back is the deploy's, so nothing is refused.
			be.image = ws.Image
			launch := &task.Spec{Version: 1, RunID: "20261110-005814-c38c", Repo: "acme/app", Ref: "main", Workflow: tt.workflow, Task: "Add a feature"}
			ask, err := catalogDockerAsk(context.Background(), env, slug, launch)
			if err != nil {
				t.Fatalf("the launcher refused the deployed image %s of workflow %s: %v", ws.Image, tt.workflow, err)
			}
			if ask {
				t.Errorf("the launcher asked for a Docker socket for workflow %s, whose entry carries no docker tag", tt.workflow)
			}
			// And a job still deployed with the image this project builds for
			// the repository is the state the launch refuses, naming the one
			// command that redeploys it: the three callers cannot disagree
			// about which jobs the project's images give an image to.
			be.image = derived
			if _, err := catalogDockerAsk(context.Background(), env, slug, launch); err == nil {
				t.Fatalf("the launcher judged a job deployed with the repository's own image %s as if the project gave it entry %s: a caller that does not read the map", derived, tt.entry)
			}
		})
	}
}
