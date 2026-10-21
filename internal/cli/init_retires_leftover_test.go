package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/task"
)

// M-3. `retireLeftover` is the one thing that names what a stop between the
// apply and the API job delete owes the user: the addresses that left
// Terraform's state are alive in GCP with nothing managing them, and a run
// that stopped for any reason after the apply has to say so (design §6). It
// had a unit test of its own and nothing else, so every call site's *wiring*
// was unpinned: unwiring one -- returning the step's error unwrapped -- left
// the whole suite green, and the offerBuilds one was known to survive.
//
// So each call site gets a CLI-level test here that fails the step it guards
// and asserts the residue is printed. The four below are the ones whose step
// can be made to fail through the run's own fakes: the version reads
// (SecretVersions), the built-record read (NeedsBuild), the build itself
// (offerBuilds) and the re-plan that deploys the images a build just made
// (planRepo). resolveForBuild's own failure cannot be provoked this way --
// its strict layer read succeeds in every rig, and the checkout whose layer
// it would refuse is refused earlier -- so it is covered by the source-shape
// test at the end of this file, the same way this package already pins the
// ordering inside that step.

// retireBuildRig is the retirement rig with the build path reachable: the
// repository's secrets have versions (without them NeedsBuild skips every
// workflow, so neither the build nor the re-plan is reached), a fake Cloud
// Build stands in for the real one (the rig has no cloudbuild endpoint, so a
// real build would leave the machine), and the caller answers the build's own
// typed confirmation in stdin.
func retireBuildRig(t *testing.T, o retireOpts) (*retireRig, *fakeBuilder) {
	t.Helper()
	fb := useFakeBuilder(t)
	r := newRetireRig(t, o)
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, logical := range []string{"bitbucket-token", "claude-oauth-token"} {
		r.sm.Seed(gcp.SecretID(slug, logical), map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: logical,
		}, []byte("v"))
	}
	return r, fb
}

// The four words a residue line always carries, whichever step stopped the
// run: that the state removal happened, which addresses it took out (the two
// the retirement removes from the state -- the kept container no apply
// destroys and the job the API finishes), that the job is still there to
// delete, and the command that
// finishes the job. The step's own failure keeps its own words in the error
// (retireLeftover returns it unchanged), so each test asserts that separately.
func assertResidue(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{
		"the run stopped after the state removal",
		"nothing managing them",
		retireKeptAddr, retireJobAddr,
		"the Cloud Run job " + retireJobName,
		retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the residue does not say %q:\n%s", want, text)
		}
	}
}

// SecretVersions (init.go's step 6, the first read after the retirement's
// apply). Its failure cannot be injected by secret ID: Readiness reads the
// versions of the same secrets earlier in the same run. So the count of a
// successful run's version reads arms the failure for the last of them, which
// is the one this step makes.
func TestInitRepoRetireNamesTheStopAtTheSecretVersionsRead(t *testing.T) {
	count := newRetireRig(t, retireOpts{protection: true})
	if _, _, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build"); err != nil {
		t.Fatalf("the run to count the version reads failed: %v", err)
	}
	total := count.sm.VersionsListCalls()
	if total == 0 {
		t.Fatal("the run read no secret versions at all")
	}

	r := newRetireRig(t, retireOpts{protection: true})
	r.sm.FailVersionsAfter = total - 1
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	assertResidue(t, out+errOut)
	if !strings.Contains(err.Error(), "listing the versions of secret") {
		t.Errorf("the error is not the version read's: %v", err)
	}
}

// NeedsBuild (the built-record read of the repository's runs bucket): with
// no record, the workflow needs its first image built, which is what brings
// the run past this step and into the build.
func TestInitRepoRetireNamesTheStopAtTheBuiltRecordRead(t *testing.T) {
	r, _ := retireBuildRig(t, retireOpts{protection: true})
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	r.gcs.Put(initRunsBucket, imagecheck.RecordKey(slug, "app"), []byte("{}"))
	r.gcs.FailObjectGetsAfter(initRunsBucket, imagecheck.RecordKey(slug, "app"), 1)
	out, errOut, runErr := executeStdin(t, "aurora\naurora\naurora\n", "setup", "--repo", "--retire")
	if ExitCode(runErr) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(runErr), runErr, out, errOut)
	}
	assertResidue(t, out+errOut)
	if !strings.Contains(runErr.Error(), imagecheck.RecordKey(slug, "app")) {
		t.Errorf("the error is not the record read's: %v", runErr)
	}
}

// offerBuilds: the build is offered, confirmed, submitted and fails. The
// first apply has already happened, so the residue is owed here too -- this
// is the call site the review found could be unwired without a test failing.
func TestInitRepoRetireNamesTheStopAtTheFirstBuild(t *testing.T) {
	r, fb := retireBuildRig(t, retireOpts{protection: true})
	fb.waitErr = errors.New("the build failed")
	out, errOut, runErr := executeStdin(t, "aurora\naurora\naurora\n", "setup", "--repo", "--retire")
	if ExitCode(runErr) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(runErr), runErr, out, errOut)
	}
	assertResidue(t, out+errOut)
	if len(fb.specs) != 1 {
		t.Errorf("builds submitted = %d, want the one the step offered", len(fb.specs))
	}
	if applies := r.applies(t); len(applies) != 1 {
		t.Errorf("applies = %v, want the retirement's one apply before the build", applies)
	}
}

// The re-plan: the build succeeded, so planRepo runs again to deploy the
// image, and that plan fails. It is the last call site before the job
// delete, and the one a stop in leaves the newest images undeployed.
func TestInitRepoRetireNamesTheStopAtTheRePlan(t *testing.T) {
	r, _ := retireBuildRig(t, retireOpts{protection: true})
	r.script["plan"] = []map[string]any{
		{"exit": 2}, // the retirement's plan
		{"exit": 1, "stderr": "Error: the re-plan could not be made\n"},
	}
	r.save(t)
	out, errOut, runErr := executeStdin(t, "aurora\naurora\naurora\n", "setup", "--repo", "--retire")
	if ExitCode(runErr) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(runErr), runErr, out, errOut)
	}
	assertResidue(t, out+errOut)
	if !strings.Contains(runErr.Error(), "the re-plan could not be made") {
		t.Errorf("the error is not the re-plan's: %v", runErr)
	}
}

// resolveForBuild's call site, the one step of the five whose failure no
// fake can provoke (see the file's comment): the wiring is pinned where it
// is written, in the function the run's own build-offer step lives in. This
// fails the moment the wrapper is dropped, which is exactly the change the
// behavioural tests above cannot see for this site -- and it is the shape
// this package already uses for the ordering inside that step
// (TestCloudBuildConfirmationStaysOutsideTheCoveredPath).
//
// Each of the five is named by the call that produces the error, so a step
// that stops being wrapped is named in the failure, not just counted.
func TestRetireLeftoverWrapsEveryStepBetweenTheApplyAndTheDelete(t *testing.T) {
	b, err := os.ReadFile("init.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (r *initRun) repoEngine(")
	if i < 0 {
		t.Fatal("repoEngine not found")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	// The five steps, in the order the run reaches them after the apply: the
	// version reads, the built-record read, the layer resolution for the
	// build, the build offer, and the re-plan that deploys what was built.
	// Each is named by the statement the run makes the call in (not by the
	// bare call, which the planRepo of step 3-5 also carries) and by how many
	// lines below it the wrap is allowed to sit: immediately for four of
	// them, and nine for the build offer, whose wrap comes after the block
	// that writes the local config before the run ends.
	for _, step := range []struct {
		stmt   string
		within int
	}{
		{"versions, err := infra.SecretVersions(", 2},
		{"names, err := infra.NeedsBuild(", 2},
		{"if buildCfg, err = resolveForBuild(", 1},
		{"built, err := r.offerBuilds(", 9},
		{"if _, missing, err = r.planRepo(", 1},
	} {
		at := strings.Index(body, step.stmt)
		if at < 0 {
			t.Errorf("%s is no longer in the run's build stage: the retirement's own steps changed shape", step.stmt)
			continue
		}
		// The step's own statement and the step.within lines under it: the
		// error it returns must be handed to the leftover's printer there.
		call := body[strings.LastIndex(body[:at], "\n")+1:]
		end := lineEnd(call, step.within+1)
		if end < 0 {
			t.Errorf("%s lost the lines the run's own step is written in: the shape changed", step.stmt)
			continue
		}
		call = call[:end]
		if !strings.Contains(call, "r.retireLeftover(spec, ") {
			t.Errorf("%s is not wrapped in r.retireLeftover(spec, ...): a stop here no longer names what is left in GCP:\n%s", step.stmt, call)
		}
	}
}

// lineEnd is the offset just past the nth line of s, or -1 when it has fewer.
func lineEnd(s string, n int) int {
	off := 0
	for i := 0; i < n; i++ {
		j := strings.Index(s[off:], "\n")
		if j < 0 {
			return -1
		}
		off += j + 1
	}
	return off
}
