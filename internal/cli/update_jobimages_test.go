package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// The owner's report of 2026-11-24, as the test that pins it: the project's
// map names a repository's workflows one catalog entry, its deployed job runs
// another entry's image, and a plain `fugaro update` moves the job — no
// checkout of that repository, no `fugaro update --repo`, no second command.
//
// The map is changed between the fixture's two entries and the job is deployed
// on the first, which is the state the owner's installation was in when the
// update before this one said "No changes" about a job that was never checked
// at all.
func TestUpdateMovesARepositoryJobToTheMappedEntry(t *testing.T) {
	r := newInitRig(t)
	installRepoJobImages(t, r, "sandbox-web-gh")
	e := repoJobImagesEngine(t, r)
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	job := gcp.JobName(slug, "web")
	r.run.SetJob(job, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "web"}, oldEntryImage)
	useRepoFilesProvider(t, "acme/sandbox", repoFilesYAML)

	e.reconcileRepoJobs(t.Context())

	if n := len(r.run.Patches()); n != 1 {
		t.Fatalf("%d patches, want the job's image moved\n%s", n, e.r.w.(*syncBuf).String())
	}
	if got := e.r.w.(*syncBuf).String(); !strings.Contains(got, "acme/sandbox web: job image catalog-sandbox-web:latest -> catalog-sandbox-web-gh:latest") {
		t.Errorf("the run does not say which job moved and onto what:\n%s", got)
	}
}

// And the other direction, so the test above cannot pass on a step that moves
// every job it can see: a job already running the entry the map names is left
// alone, and the run says nothing about it — the plain update of a project
// whose map nobody changed writes nothing and prints no line.
func TestUpdateLeavesAJobRunningTheMappedEntry(t *testing.T) {
	r := newInitRig(t)
	installRepoJobImages(t, r, "sandbox-web-gh")
	e := repoJobImagesEngine(t, r)
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	r.run.SetJob(gcp.JobName(slug, "web"), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "web"}, newEntryImage)
	useRepoFilesProvider(t, "acme/sandbox", repoFilesYAML)

	e.reconcileRepoJobs(t.Context())

	if n := len(r.run.Patches()); n != 0 {
		t.Fatalf("%d patches, want none: the job already runs the entry the map names", n)
	}
	if got := e.r.w.(*syncBuf).String(); strings.Contains(got, "job image") {
		t.Errorf("a job that needed no move was reported as one:\n%s", got)
	}
}

// The door's own rule: this is the upgrade's step and the converge's tail, so
// a `fugaro setup` that has just created an installation — and a run that
// applies nothing (--plan-only) — moves no job. The fixture is the first
// test's, which fails without this guard.
func TestUpdateMovesNoJobOutsideItsOwnDoor(t *testing.T) {
	for _, o := range []*initOptions{{door: setupDoor.name}, {door: updateDoor.name, planOnly: true}} {
		r := newInitRig(t)
		installRepoJobImages(t, r, "sandbox-web-gh")
		e := repoJobImagesEngine(t, r, o)
		slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
		r.run.SetJob(gcp.JobName(slug, "web"), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "web"}, oldEntryImage)
		useRepoFilesProvider(t, "acme/sandbox", repoFilesYAML)

		e.reconcileRepoJobs(t.Context())

		if n := len(r.run.Patches()); n != 0 {
			t.Errorf("%s: %d patches, want none", o.doorName(), n)
		}
	}
}

// A repository whose job runs an image that is not a workflow's, and the
// workflow the map names: the job the step reads carries the entry it was
// deployed on, named as the owner's did.
var (
	oldEntryImage = "us-east5-docker.pkg.dev/proj-1234/" + gcp.SharedRegistryRepoID() + "/catalog-sandbox-web:latest"
	newEntryImage = "us-east5-docker.pkg.dev/proj-1234/" + gcp.SharedRegistryRepoID() + "/catalog-sandbox-web-gh:latest"
)

// repoFilesYAML is the repository's own default-branch fugaro.yaml, as the
// step reads it through the provider: no checkout anywhere.
const repoFilesYAML = "version: 1\nproject: " + initProjectName + "\n" +
	"git: { provider: bitbucket }\n" +
	"workflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// installRepoJobImages gives the fixture's project a catalog of the two
// entries and a map naming the one the test says: mapped is the entry
// acme/sandbox's web workflow resolves to, which is what the step must move
// the job onto.
func installRepoJobImages(t *testing.T, r *initRig, mapped string) {
	t.Helper()
	r.appendConfig(t, `image_catalog:
  entries:
    - name: sandbox-web
      tags: [web]
      source: { kind: inline, inline: "FROM scratch\n" }
    - name: sandbox-web-gh
      tags: [web]
      source: { kind: inline, inline: "FROM scratch\n" }
image_map:
  acme/sandbox: `+mapped+"\n")
}

// repoJobImagesEngine is the engine the step runs in: the update door's, with
// the installation's outputs the converge has read by the time the step runs,
// which is what a spec is built from.
func repoJobImagesEngine(t *testing.T, r *initRig, o ...*initOptions) *initEngine {
	t.Helper()
	opts := &initOptions{door: updateDoor.name}
	if len(o) > 0 {
		opts = o[0]
	}
	e := rigEngine(t, r, opts)
	e.r.res.Outputs = &infra.InstallationOutputs{
		ProjectName: initProjectName, RunsBucket: initRunsBucket,
		RegistryHost: "us-east5-docker.pkg.dev/proj-1234", LogView: initLogView,
	}
	return e
}

// useRepoFilesProvider hands the step a git provider that answers without a
// network: one repository, whose default branch holds yaml.
func useRepoFilesProvider(t *testing.T, repo, yaml string) {
	t.Helper()
	p := &fake.Provider{Repo: repo, State: fake.State{Files: map[string]string{"fugaro.yaml": yaml}}}
	old := openRepoJobFiles
	openRepoJobFiles = func([]string, func(string)) (string, gitprov.Opener, error) {
		return "", gitprov.Static(p), nil
	}
	t.Cleanup(func() { openRepoJobFiles = old })
}

// The same case as TestUpdateMovesARepositoryJobToTheMappedEntry, driven the
// way the owner met it: one plain `fugaro update`, at a terminal, with no
// --repo and no second command. The step's own test above says what it does;
// this one says that a converge reaches it — the tail hook is wired into the
// loop, the door and the session guards let it through, and the job moves
// before the command ends.
func TestAPlainUpdateMovesARepositoryJobToTheMappedEntry(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	installRepoJobImages(t, r, "sandbox-web-gh")
	slug, label := checkJobOwner(t, "bitbucket", "acme/sandbox")
	r.run.SetJob(gcp.JobName(slug, "web"), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: "web"}, oldEntryImage)
	useRepoFilesProvider(t, "acme/sandbox", repoFilesYAML)
	fakeTerminal(t)

	out, err := updateRun(t, r, "update")
	if err != nil {
		t.Fatalf("a plain update failed: %v\n%s", err, out)
	}
	if n := len(r.run.Patches()); n != 1 {
		t.Fatalf("%d patches, want the job's image moved\n%s", n, out)
	}
	if !strings.Contains(out, "acme/sandbox web: job image catalog-sandbox-web:latest -> catalog-sandbox-web-gh:latest") {
		t.Errorf("the update does not say which job moved and onto what:\n%s", out)
	}
}
