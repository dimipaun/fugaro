package cli

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// updateDefaultEntryTests is where S10's own tests live: `fugaro update` writes
// the project's default catalog entry — the pinned base image, as a prebuilt
// entry marked default (D2305) — so that the shared job of every profile has an
// image the project's own catalog names (internal/infra's projectJobImage
// resolves exactly that entry for them, F21) and so that the project has a
// default an operator can curate a baked image into.
//
// The entry is the image of every job whose repository names none, so it is
// written only in the state where writing it cannot make this same update
// refuse a job it converges today (internal/infra's unbuiltOnBaked, D1191): a
// repository no profile admits, and the checkout's own workflows that would be
// handed the entry and cannot run it. The rig is update_converge_test.go's: an
// installation a release behind, in a checkout of its own repository, with one
// GitHub repository (acme/app) and one Bitbucket repository (acme/sandbox)
// enrolled and no profiles block at all.

// bakedWorkflowYAML is the checkout of the two tests below that need the
// entry's own write to be the thing under test rather than the refusal it
// waits on: both workflows write a dockerfile: of their own, so the catalog's
// default is set aside for them (D1900) and neither is a job the entry would
// be handed to. It is the shape of
// TestUpdateConvergesAPrebuiltDefaultBesideAWorkflowsOwnImage, for the same
// reason.
const bakedWorkflowYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { dockerfile: .fugaro/app.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { dockerfile: .fugaro/api.Dockerfile, commands: { build: sh build.sh, test: sh test.sh } }\n"

// ownImageDockerfile is the Dockerfile those workflows name, as the repository
// behind had it: `fugaro validate` reads a workflow's dockerfile: as a file
// that is there, so a checkout without them is not the shape these tests are
// about.
const ownImageDockerfile = "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN git clone \"$REPO_URL\" /work/repo\n"

// ownImageRig is the converge rig over a checkout whose workflows write an
// image of their own, so that the entry is written for the two repositories the
// project enrols and the only variable left between the tests below is the one
// each is about.
func ownImageRig(t *testing.T) *convergeRig {
	t.Helper()
	r := newConvergeRigYAML(t, currentNode(), bakedWorkflowYAML)
	for _, wf := range []string{"app", "api"} {
		checkoutFile(t, filepath.Join(r.root, ".fugaro", wf+".Dockerfile"), ownImageDockerfile)
	}
	return r
}

// writtenDefaultEntry is the default entry of the local config `fugaro update`
// left on the machine, and whether the config carries one at all.
func writtenDefaultEntry(t *testing.T) (config.Entry, bool) {
	t.Helper()
	cat := updatedConfig(t).ImageCatalog
	if cat == nil {
		return config.Entry{}, false
	}
	return catalog.Default(*cat)
}

// TestUpdateWritesTheDefaultEntryOnlyWhenEveryRepoIsAdmitted is S10's red step
// at the write: no step wrote a default entry, so the shared jobs of every
// profile ran the base image with nothing naming it. The project's repositories
// are both admitted — acme/app by the GitHub default profile this same run
// writes and acme/sandbox by its Bitbucket one (S5, D2310) — so the entry is
// written, it is the base image this run pinned, it reaches the published node
// (which is where a run reads the project's catalog) and the shared jobs of
// every profile are deployed on it.
//
// The entry's image is compared against the base image the same run recorded,
// and not against the fixture's: the base moves in every update (its images
// stage pins this release's), and an entry left on the release behind it is one
// every shared job of the project would run — the runner image a
// `policy-vs-jobs` warning is about.
func TestUpdateWritesTheDefaultEntryOnlyWhenEveryRepoIsAdmitted(t *testing.T) {
	r := ownImageRig(t)
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	entry, ok := writtenDefaultEntry(t)
	if !ok {
		t.Fatalf("after one fugaro update, this machine's config marks no default catalog entry, so the shared jobs of this project have no image of the project's own to run:\n%s\n%s",
			out, readFileString(t, installationConfigPath()))
	}
	if entry.Name != defaultCatalogEntryName {
		t.Errorf("the default entry is named %q, want %q", entry.Name, defaultCatalogEntryName)
	}
	if entry.Source.Kind != config.SourceImage {
		t.Errorf("the default entry's source kind is %q, want %q: the entry is the pinned base image, and nothing builds it (D2305)",
			entry.Source.Kind, config.SourceImage)
	}
	if want := updatedConfig(t).BaseImage(config.BaseKind); entry.Source.Image != want {
		t.Errorf("the default entry's image is %s, want the base image this run recorded, %s: the entry is the pinned base image (D2305), and one left on the release behind is the runner image every shared job of the project would run\n%s",
			entry.Source.Image, want, out)
	}
	// The published node is what a run reads the project's catalog from, so an
	// entry this run wrote and did not publish is an entry no job of the
	// project can resolve.
	node := convergeNode(t, r)
	if node.ImageCatalog == nil {
		t.Fatalf("the policy this update published carries no image catalog, so the entry the local config holds reached no run:\n%s", out)
	}
	published, ok := catalog.Default(*node.ImageCatalog)
	if !ok || published.Name != entry.Name || published.Source != entry.Source {
		t.Errorf("the published node's default entry is %+v (%v), want the one this run wrote to the local config, %+v: a run reads the profile's job image out of the policy, not out of this machine's file",
			published, ok, entry)
	}
	// And the shared job of every profile is deployed on it: the entry is the
	// image of a job whose repository names none (F21), which is every job the
	// profile deploys until an operator curates a baked image into the entry.
	jobs := installationProjectJobs(t, r)
	if len(jobs) == 0 {
		t.Fatalf("this update deployed no shared job at all: %v", r.tfvars(t)["project_jobs"])
	}
	for _, name := range slices.Sorted(maps.Keys(jobs)) {
		job, _ := jobs[name].(map[string]any)
		if job == nil {
			t.Fatalf("the job %s is %T, want the deployment's own object", name, jobs[name])
		}
		if got, _ := job["image"].(string); got != entry.Source.Image {
			t.Errorf("the shared job %s runs %s, want the default entry's image %s: a profile has no image field (R1), so the catalog's default is the only image its jobs can be deployed on",
				name, got, entry.Source.Image)
		}
	}
	if !strings.Contains(out, "wrote the default catalog entry "+defaultCatalogEntryName) {
		t.Errorf("the update does not say what it wrote, and a write into the operator's own file says what it wrote:\n%s", out)
	}
}

// TestUpdateLeavesTheEntryAndSaysWhyForAnUnadmittedRepo: the entry is the image
// of every job whose repository names no image, so a repository no profile
// admits — which keeps the per-repository job its own root deploys — would be
// handed it, and a bare `checkout: baked` workflow of that repository is a job
// the deployer refuses (unbuiltOnBaked, D1191). The write therefore waits for
// the whole list of repositories the project lists, and says which one holds it
// back.
//
// The checkout's own workflows are the ones that could not be refused (the
// fixture above writes an image of their own), so the reason this run prints is
// the repository's and not the workflow's: what is under test is the admission
// half of the guard.
func TestUpdateLeavesTheEntryAndSaysWhyForAnUnadmittedRepo(t *testing.T) {
	const unadmitted = "github/acme/other"
	r := ownImageRig(t)
	// The operator's own GitHub profile, which admits the checkout's repository
	// and not the one this test adds: an existing profile is never rewritten
	// (R1), so the update writes no GitHub default to admit it with.
	rewriteRigConfig(t, r.imagesRig, func(lc *localcfg.Config) {
		lc.Profiles = map[string]config.JobProfile{projectpolicy.DefaultProfileName: {
			Provider: "github", CPU: 2, Memory: "4Gi", TimeoutClass: "2h",
			AllowRepos: []string{"github/acme/app"},
		}}
		lc.Repos["acme/other"] = localcfg.Repo{Provider: "github", Workflows: []string{"app"}, GitHubAppID: "12345"}
	})
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-vs-jobs"})

	if entry, ok := writtenDefaultEntry(t); ok {
		t.Errorf("this update wrote the default entry %+v, and %s is admitted by no profile: that repository keeps the per-repository job its own root deploys, and the entry would be the image of a job the deployer refuses where the workflow writes none of its own (unbuiltOnBaked)\n%s",
			entry, unadmitted, out)
	}
	if !strings.Contains(out, "no default catalog entry was written") {
		t.Errorf("this update wrote no default entry and says nothing about it, so the operator reads a project on the images it runs and not a project whose write was held back:\n%s", out)
	}
	if !strings.Contains(out, unadmitted) {
		t.Errorf("the line this update prints about the entry does not name %s, the repository that keeps it from being written:\n%s", unadmitted, out)
	}
}

// TestUpdatePreviousVersionWithABakedWorkflowConvergesInOneUpdate is the HARD
// RULE at this write's own refusal: an installation of the previous version,
// in a checkout whose workflows are bare `checkout: baked` ones, converges with
// `fugaro update` alone — and converges by NOT writing the entry, because the
// deployer would refuse those workflows the image the entry would be (D1191).
//
// It is the shape a project is in today: `checkout: baked` is the default and
// its workflows name no image, so an update that wrote the entry on the pinned
// base image without waiting would stop at the repository stage of this very
// run, and every later update of that installation with it. What the run does
// instead is leave the project exactly as it is, say which workflow holds the
// write back, and converge: the workflows keep the per-repository images their
// records show, and a later update writes the entry once a workflow names an
// image of its own (or the repository's job is one the project's default may
// serve).
func TestUpdatePreviousVersionWithABakedWorkflowConvergesInOneUpdate(t *testing.T) {
	r := newConvergeRig(t, staleNode(t))
	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if got := convergeNode(t, r).Version; got != projectpolicy.Version {
		t.Fatalf("the published node is version %d after one update, want this binary's %d", got, projectpolicy.Version)
	}
	if entry, ok := writtenDefaultEntry(t); ok {
		t.Errorf("this update wrote the default entry %+v into a project whose workflows are bare baked ones: the repository stage of this same run is what refuses such a workflow the entry's image (unbuiltOnBaked, D1191), so the write is the one thing that would have stopped this update\n%s",
			entry, out)
	}
	if p := convergeNode(t, r); p.ImageCatalog != nil {
		t.Errorf("the policy this update published carries a catalog (%+v), want none: this machine's config names none, and an entry is the only thing this run would have written there", p.ImageCatalog)
	}
	if !strings.Contains(out, "no default catalog entry was written") {
		t.Errorf("this update wrote no default entry and says nothing about it, so the person reads a project on the images it runs and not a project whose write was held back:\n%s", out)
	}
	for _, want := range []string{"unbuiltOnBaked", "github/acme/app"} {
		if !strings.Contains(out, want) {
			t.Errorf("the line this update prints about the entry does not name %q, which is the reason the write waits and the repository it is about:\n%s", want, out)
		}
	}
}
