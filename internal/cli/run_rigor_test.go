package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
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
