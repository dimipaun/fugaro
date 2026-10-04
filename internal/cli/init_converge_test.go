package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type convergeJSON struct {
	Applied bool `json:"applied"`
	Stages  []struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	} `json:"stages"`
	Left []map[string]string `json:"left_for_you"`
}

func (c convergeJSON) state(name string) string {
	for _, s := range c.Stages {
		if s.Name == name {
			return s.State
		}
	}
	return ""
}

// From nothing (no state bucket yet, a plan with changes): the converge
// creates, applies and reports each stage; the Firebase stage is skipped
// without --firebase.
func TestConvergeFromNothing(t *testing.T) {
	r := newInitRig(t)
	out, _, err := executeStdin(t, "", "init", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.state("preflight") != "done" || res.state("installation") != "changed" || res.state("firebase") != "skipped" || !res.Applied {
		t.Errorf("stages %+v", res.Stages)
	}
	if res.Left == nil || len(res.Left) != 0 {
		t.Errorf("left_for_you = %v: want an empty array", res.Left)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Errorf("calls %q", r.calls(t))
	}
}

// A rerun with nothing to do says "No changes" for the stage and exits 0.
func TestRerunPlansNothing(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	out, _, err := executeStdin(t, "", "init", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "apply")) != 0 || !strings.Contains(out, "[done]") || !strings.Contains(out, "No changes") {
		t.Errorf("calls %q\n%s", r.calls(t), out)
	}
}

// --non-interactive never prompts, even at a terminal: without --yes the
// run only plans, and the typed name waiting on stdin is never read.
func TestNonInteractiveNeverPromptsAndNeedsYes(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	in := initProjectName + "\n"
	out, _, err := executeStdin(t, in, "init", "--non-interactive")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(r.ran(t, "apply")) != 0 || strings.Contains(out, "Type "+initProjectName+" to apply") {
		t.Errorf("a prompt or an apply under --non-interactive:\ncalls %q\n%s", r.calls(t), out)
	}
	if !strings.Contains(out, "--non-interactive without --yes only plans") {
		t.Errorf("the run does not say it only planned:\n%s", out)
	}
	// With --yes it applies, still without a prompt.
	out, _, err = executeStdin(t, in, "init", "--non-interactive", "--yes")
	if err != nil || len(r.ran(t, "apply")) != 1 || strings.Contains(out, "Type "+initProjectName+" to apply") {
		t.Fatalf("%v, calls %q\n%s", err, r.calls(t), out)
	}
}

// A confirmation that is needed and cannot be had says so in one line, at
// a terminal or not, under --non-interactive: the state bucket's creation
// is such a step.
func TestNonInteractiveRefusesBucketCreationWithoutYes(t *testing.T) {
	r := newInitRig(t)
	fakeTerminal(t)
	_, _, err := executeStdin(t, initProjectName+"\n", "init", "--non-interactive", "--plan-only")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--yes") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("calls %q", r.calls(t))
	}
}

// The Firestore location's confirmation never reads stdin under
// --non-interactive.
func TestNonInteractiveNeverReadsFirestoreLocation(t *testing.T) {
	rd := &readSpy{Reader: strings.NewReader("us-east5\n")}
	r := &initRun{o: &initOptions{nonInteractive: true}, w: &strings.Builder{}, in: bufio.NewReader(rd)}
	err := r.confirmLocation()
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if rd.read {
		t.Error("stdin was read")
	}
}

type readSpy struct {
	io.Reader
	read bool
}

func (s *readSpy) Read(p []byte) (int, error) { s.read = true; return s.Reader.Read(p) }

// --plan-only still goes through the loop and prints the plan view, with
// the engine's own plan, and changes nothing.
func TestConvergePlanOnlyShowsView(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	out, _, err := executeStdin(t, "", "init", "--plan-only", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if res.state("installation") != "will-do" || res.state("firebase") != "skipped" || len(r.ran(t, "apply")) != 0 {
		t.Errorf("stages %+v, calls %q", res.Stages, r.calls(t))
	}
}
