package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRun runs the fake in process, with script as its FAKE_TERRAFORM_SCRIPT
// and a private log, and reports what it printed and its exit code. Every
// call goes through one environment, so a scripted list is consumed in order
// exactly as it is across the invocations of a real run; the sequence counts
// live beside the log, as they do for a real caller.
func fakeRun(t *testing.T, script string) func(args ...string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FAKE_TERRAFORM_SCRIPT", script)
	t.Setenv("FAKE_TERRAFORM_LOG", filepath.Join(dir, "calls.jsonl"))
	t.Setenv("FAKE_TERRAFORM_SEQ", "")
	return func(args ...string) (stdout, stderr string, code int) { return call(t, dir, args...) }
}

func call(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	outF, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	errF, err := os.CreateTemp(dir, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outF, errF
	code = run(args)
	os.Stdout, os.Stderr = oldOut, oldErr
	for _, f := range []*os.File{outF, errF} {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	out, err := os.ReadFile(outF.Name())
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile(errF.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(e), code
}

// The state a retirement reads is `show -json` with no plan file, and the
// plan is `show -json <planfile>`: one script answers them differently, so a
// test can pin what the run did with the state it read before the plan.
func TestTheStateShowIsScriptedApartFromThePlanShow(t *testing.T) {
	script := `{"show -json": {"stdout": "STATE"}, "show": {"stdout": "PLAN"}}`
	f := fakeRun(t, script)
	if out, _, code := f("show", "-json"); out != "STATE" || code != 0 {
		t.Errorf("show -json printed %q (exit %d), want the state", out, code)
	}
	if out, _, code := f("show", "-json", "/tmp/plan"); out != "PLAN" || code != 0 {
		t.Errorf("show -json <plan> printed %q (exit %d), want the plan", out, code)
	}
}

// A script written before the state was read names only "show", and must go
// on answering both forms: every CLI test's plan script is written that way.
func TestAScriptThatNamesOnlyShowAnswersBothForms(t *testing.T) {
	f := fakeRun(t, `{"show": {"stdout": "PLAN"}}`)
	for _, args := range [][]string{{"show", "-json"}, {"show", "-json", "/tmp/plan"}} {
		if out, _, _ := f(args...); out != "PLAN" {
			t.Errorf("%q printed %q, want the script's own answer", args, out)
		}
	}
}

// A list of steps is consumed in order, and its last step repeats: that is
// what lets one script answer the second `show -json` differently from the
// first, or fail a `state rm` on one address and pass on the next.
func TestAListOfStepsAnswersEachCallInTurn(t *testing.T) {
	f := fakeRun(t, `{"show -json": [{"stdout": "BEFORE"}, {"stdout": "AFTER"}]}`)
	for i, want := range []string{"BEFORE", "AFTER", "AFTER"} {
		if out, _, _ := f("show", "-json"); out != want {
			t.Errorf("call %d printed %q, want %q", i+1, out, want)
		}
	}
}

// The counts are per key, so a failing `state rm` and a succeeding one do not
// disturb what the plan's own calls answer.
func TestTheCountsArePerKey(t *testing.T) {
	f := fakeRun(t, `{"state rm": [{"exit": 1, "stderr": "No matching objects found"}, {"exit": 0}],
	                   "show -json": [{"stdout": "S1"}, {"stdout": "S2"}]}`)
	if _, stderr, code := f("state", "rm", "a"); code != 1 || stderr == "" {
		t.Errorf("first state rm: exit %d, stderr %q; want the scripted failure", code, stderr)
	}
	if _, _, code := f("state", "rm", "b"); code != 0 {
		t.Errorf("second state rm: exit %d, want the second step's success", code)
	}
	if out, _, _ := f("show", "-json"); out != "S1" {
		t.Errorf("show -json printed %q, want the first step: the counts are per key", out)
	}
}

// A key's root variant wins, and a list of no steps is a script bug rather
// than a silent empty answer.
func TestAStepListedForOneRootWinsAndAnEmptyListIsRefused(t *testing.T) {
	if out, _, _ := fakeRun(t, `{"plan@firebase": {"stdout": "ROOT"}, "plan": {"stdout": "OTHER"}}`)("plan"); out != "OTHER" {
		t.Errorf("plan in the test's own directory printed %q, want the plain key", out)
	}
	out, stderr, code := fakeRun(t, `{"plan": []}`)("plan")
	if code == 0 || out != "" || !strings.Contains(stderr, "empty list") {
		t.Errorf("an empty step list: exit %d, stdout %q, stderr %q; want a refusal", code, out, stderr)
	}
}
