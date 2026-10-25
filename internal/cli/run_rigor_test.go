package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// release0111 is a job image new enough to read a task's rigor request
// (rigorRequestSince): the release this work ships in.
const release0111 = "ghcr.io/dimipaun/fugaro-base:0.11.1"

// runWithRigor launches one run of acme/app under run id id with the given
// extra flags on an image that reads a rigor request, and returns its stored
// task.
func runWithRigor(t *testing.T, f *cloudFixture, id string, extra []string, text string) *task.Spec {
	t.Helper()
	writeBuildRecord(t, f, appSlug, "web", release0111)
	args := append([]string{"run", "--repo", "acme/app", "--run-id", id}, extra...)
	args = append(args, text)
	if _, _, err := execute(t, args...); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return readSpec(t, f, id)
}

// TestRunModeAndPathAreStoredInTheTask: the run's own rigor request is the
// one input that may raise the mode its configuration resolves and never
// lower it (D6), and it travels to the runner in the task, where the
// resolver reads it as Request.Mode and Request.Paths (docs/rigor-modes
// implementation-plan T1.6, D22).
//
// Mutation (drop Rigor/Paths from buildAttemptSpec's spec literal): the
// stored task carries neither, and this test fails on its first assertion.
func TestRunModeAndPathAreStoredInTheTask(t *testing.T) {
	f := newCloudFixture(t)
	spec := runWithRigor(t, f, "20261025-120000-abcd",
		[]string{"--mode", "critical", "--path", "internal/payments/x.go", "--path", "docs/**"},
		"Fix the payments path.")
	if spec.Rigor != "critical" {
		t.Fatalf("rigor = %q, want the flag's mode", spec.Rigor)
	}
	if want := []string{"internal/payments/x.go", "docs/**"}; !reflect.DeepEqual(spec.Paths, want) {
		t.Fatalf("paths = %v, want %v", spec.Paths, want)
	}
	// And it is on the wire under its own keys: the runner reads it from
	// there, and the recipe's own mode is a different field.
	var doc map[string]any
	if err := json.Unmarshal([]byte(taskJSON(t, f, "20261025-120000-abcd")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["rigor"] != "critical" {
		t.Fatalf("task.json rigor = %v", doc["rigor"])
	}
	paths, _ := doc["paths"].([]any)
	if len(paths) != 2 || paths[0] != "internal/payments/x.go" {
		t.Fatalf("task.json paths = %v", doc["paths"])
	}
	if _, present := doc["mode"]; present {
		t.Errorf("the request was written as the recipe's own mode field: %v", doc["mode"])
	}
}

// TestRunWithoutAModeAsksForNone: the flag absent means no request at all —
// not a request for the built-in default. The run resolves from its layers
// alone (Pro for a repository that configures nothing, D19), which is the
// only reading under which a launcher that says nothing cannot outvote a
// repository that did.
func TestRunWithoutAModeAsksForNone(t *testing.T) {
	f := newCloudFixture(t)
	spec := runWithRigor(t, f, "20261025-120100-abcd", nil, "Fix the flaky test.")
	if spec.Rigor != "" || len(spec.Paths) != 0 {
		t.Fatalf("a launch that asked for nothing carries rigor %q and paths %v", spec.Rigor, spec.Paths)
	}
	if wire := taskJSON(t, f, "20261025-120100-abcd"); strings.Contains(wire, `"rigor"`) || strings.Contains(wire, `"paths"`) {
		t.Fatalf("a launch that asked for nothing writes the fields: %s", wire)
	}
}

// TestRunRefusesAModeOutsideTheVocabulary: the vocabulary is closed (D20), so
// a near miss is a launcher's mistake refused before anything is written —
// never a value the resolver reads as an unknown mode and quietly ignores,
// which is the fail-open direction.
func TestRunRefusesAModeOutsideTheVocabulary(t *testing.T) {
	newCloudFixture(t)
	for _, mode := range []string{"Poc", "PRO", "critical ", "sizzle", "none"} {
		_, _, err := execute(t, "run", "--repo", "acme/app", "--mode", mode, "Fix the flaky test.")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "mode") {
			t.Errorf("--mode %q: err = %v, want exit 1 naming the mode", mode, err)
		}
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--path", "   ", "Fix the flaky test.")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "path") {
		t.Errorf("--path with a blank path: err = %v, want exit 1 naming the path", err)
	}
}

// TestRunAcceptsHotfix: hotfix is in the vocabulary and off the axis — it
// ranks below every on-axis mode (internal/rigor's Rank), so the launcher
// accepts it and it is the resolver that refuses to let it lower anything.
// A launcher that refused the word would make D12's one route to it, an
// operator asking for it, unreachable from the CLI.
func TestRunAcceptsHotfix(t *testing.T) {
	f := newCloudFixture(t)
	spec := runWithRigor(t, f, "20261025-120200-abcd", []string{"--mode", "hotfix"}, "Fix production.")
	if spec.Rigor != "hotfix" {
		t.Fatalf("rigor = %q, want the flag's mode", spec.Rigor)
	}
}

// TestRunRefusesTooManyDeclaredPaths: the list is bounded (task.
// MaxDeclaredPaths) because every path in it is matched against every area
// rule the base branch declares, and the bound is a refusal rather than a
// truncation: the paths a truncation dropped are exactly the ones that would
// have raised the mode.
func TestRunRefusesTooManyDeclaredPaths(t *testing.T) {
	newCloudFixture(t)
	args := []string{"run", "--repo", "acme/app"}
	for range task.MaxDeclaredPaths + 1 {
		args = append(args, "--path", "internal/x.go")
	}
	args = append(args, "Fix the flaky test.")
	_, _, err := execute(t, args...)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "path") {
		t.Fatalf("err = %v, want exit 1 refusing a list over the bound", err)
	}
}

// TestRunRigorRequestIsGatedOnTheJobImage: the request is a new task field,
// and task.Parse decodes with DisallowUnknownFields, so a runner older than
// rigorRequestSince refuses the whole spec at bootstrap — bytes in the bucket
// under a run that never starts. The launcher therefore refuses the launch,
// and a launch that asks for nothing is never gated (its spec is
// byte-identical to what the previous release wrote).
func TestRunRigorRequestIsGatedOnTheJobImage(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release085)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--mode", "critical", "Fix the flaky test.")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "rigor") {
		t.Fatalf("err = %v, want the launch refused on an image that predates the field", err)
	}
	writeBuildRecord(t, f, appSlug, "web", release0111)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "Fix the flaky test."); err != nil {
		t.Fatalf("a launch with no request was gated: %v", err)
	}
}

// TestRunRigorRequestIsGatedOnACloneWorkflow: a checkout: clone workflow has
// no build record to judge, so the same gate is judged against
// base_images.base itself — the other half of the check, and the half a
// mutation can drop without the test above noticing.
//
// Mutation (drop the asksForRigor block from finishLaunch's clone branch):
// the launch goes through on an image whose runner refuses the whole task at
// bootstrap, which is the failure the gate exists to prevent.
func TestRunRigorRequestIsGatedOnACloneWorkflow(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "base_images: {base: ghcr.io/dimipaun/fugaro-base:0.7.5}\n")
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n" +
		"  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "--mode", "critical", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "the run's own rigor request (--mode, --path) needs a job image whose runner knows the rigor request (fugaro "+rigorRequestSince+" or later)") ||
		!strings.Contains(err.Error(), "ghcr.io/dimipaun/fugaro-base:0.7.5, release 0.7.5") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestFollowUpCarriesTheRigorRequest: a --pr follow-up builds its spec in
// newFollowUpSpec, a second builder, so the request has to be written there
// too — otherwise the flags are accepted, the launch reports success, and the
// job reads a task that asks for nothing.
//
// Mutation (drop Rigor/Paths from newFollowUpSpec's spec literal): the
// follow-up stores neither and this test fails on its first assertion.
func TestFollowUpCarriesTheRigorRequest(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	writeBuildRecord(t, f, appSlug, "web", release0111)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID,
		"--mode", "critical", "--path", "internal/payments/x.go", "also rename x"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, fuID)
	if spec.Rigor != "critical" || !reflect.DeepEqual(spec.Paths, []string{"internal/payments/x.go"}) {
		t.Fatalf("follow-up task carries rigor %q and paths %v, want the flags'", spec.Rigor, spec.Paths)
	}
}

// TestReviewLaunchCarriesTheRigorRequest: a mode: review recipe builds its
// spec in reviewSpec, the third builder. The request is the launcher's on
// every launch shape the flags are accepted on, so a review is no exception.
//
// Mutation (drop Rigor/Paths from reviewSpec's spec literal): the review
// stores neither and this test fails.
func TestReviewLaunchCarriesTheRigorRequest(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release0111)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261025-120300-abcd",
		"--recipe", "review-only", "--pr", "7", "--mode", "critical", "--path", "internal/payments/x.go"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261025-120300-abcd")
	if spec.Rigor != "critical" || !reflect.DeepEqual(spec.Paths, []string{"internal/payments/x.go"}) {
		t.Fatalf("review task carries rigor %q and paths %v, want the flags'", spec.Rigor, spec.Paths)
	}
}

// TestRunRigorRefusedWithRetry: --retry launches the stored task as it is, so
// it takes no task flags at all — a request given there would be accepted and
// then silently dropped, which is worse than a refusal.
func TestRunRigorRefusedWithRetry(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "it takes no --mode", "run", "--retry", "20261018-120000-abcd", "--mode", "critical")
	wantRefused(t, f, "it takes no --path", "run", "--retry", "20261018-120000-abcd", "--path", "internal/x.go")
}

// TestStoredFollowUpRefusesADifferentRigorRequest: a repeated --run-id names
// a stored task, and the rigor request is part of that task — the stored spec
// is what the job reads, so a raise this launch asked for and the comparison
// did not check would be dropped while the launch reported the run as already
// launched: a mode nobody resolved, reported as success.
//
// Mutation (drop the two rigor terms from storedFollowUp's comparison): the
// second launch reports the first as the same task and the stored request
// stays what it was.
func TestStoredFollowUpRefusesADifferentRigorRequest(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	writeBuildRecord(t, f, appSlug, "web", release0111)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID,
		"--mode", "startup", "--path", "docs/x.md", "also rename x"); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID,
		"--mode", "critical", "also rename x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v, want the different task refusal", err)
	}
	// A different list of paths is a different task too, and so is the same
	// mode with another path beside it.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID,
		"--mode", "startup", "--path", "docs/y.md", "also rename x"); ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the different task refusal for the other path", err)
	}
	// The same request is the same task: the repeated launch reports the
	// existing one instead of starting another.
	out, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID,
		"--mode", "startup", "--path", "docs/x.md", "also rename x")
	if err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	// And a launch that asks for nothing makes no claim about it: the stored
	// request stands, exactly as the stored title does.
	out, _, err = execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x")
	if err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	spec := readSpec(t, f, fuID)
	if spec.Rigor != "startup" || !reflect.DeepEqual(spec.Paths, []string{"docs/x.md"}) {
		t.Fatalf("stored task carries rigor %q and paths %v, want the first launch's", spec.Rigor, spec.Paths)
	}
}
