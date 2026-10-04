package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// A stage --yes does not cover never sees r.o.yes, whatever the flag says:
// the adapter enforces the loop's decision on the engine's own option.
func TestEngineStageNeverSeesYesUnlessCovered(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{{initflow.Secrets, false}, {initflow.Project, false}, {initflow.Installation, true}} {
		r := &initRun{o: &initOptions{yes: true}, w: io.Discard}
		var saw, sawPlan *bool
		st := &engineStage{e: &initEngine{r: r}, name: tc.name, skip: func() string { return "" },
			run: func(context.Context) error {
				y := r.o.yes
				if saw == nil {
					saw = &y
				} else {
					sawPlan = &y
				}
				return nil
			}}
		res, err := initflow.Run(context.Background(), []initflow.Stage{st}, initflow.Options{Yes: true, Terminal: true, Out: io.Discard})
		if err != nil || res.Failed != nil {
			t.Fatalf("%s: %v %+v", tc.name, err, res.Failed)
		}
		if saw == nil || *saw != tc.want {
			t.Errorf("%s: o.yes during Apply = %v, want %v", tc.name, saw, tc.want)
		}
		_ = sawPlan
		if !r.o.yes {
			t.Errorf("%s: o.yes not restored", tc.name)
		}
	}
}

// An engine's uncoded error exits 1 as it always did; a coded one keeps its
// code; needs-you is 1; a refusal is 1.
func TestConvergeKeepsEngineExitCodes(t *testing.T) {
	run := func(stErr error) error {
		r := &initRun{o: &initOptions{}, w: io.Discard, cmd: NewRootCmd()}
		e := &initEngine{r: r}
		st := &engineStage{e: e, name: initflow.Installation, skip: func() string { return "" },
			run: func(context.Context) error { return stErr }}
		res, err := initflow.Run(context.Background(), []initflow.Stage{st}, initflow.Options{Yes: true, Terminal: true, Out: io.Discard})
		return e.outcome(res, err)
	}
	if got := ExitCode(run(errors.New("plain engine error"))); got != 1 {
		t.Errorf("uncoded engine error exits %d, want 1", got)
	}
	if got := ExitCode(run(remote(errors.New("cloud")))); got != 2 {
		t.Errorf("remote error exits %d, want 2", got)
	}
	if got := ExitCode(run(userErr("no"))); got != 1 {
		t.Errorf("user error exits %d, want 1", got)
	}
	if err := run(nil); err != nil {
		t.Errorf("success: %v", err)
	}
}

// The CLI's own JSON (initResult), golden: stages, left_for_you, failed and
// note are printed on every outcome, a failure included.
func TestInitResultJSONGolden(t *testing.T) {
	var out strings.Builder
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	r := &initRun{o: &initOptions{asJSON: true}, cmd: cmd, w: io.Discard}
	r.res.Project, r.res.GCPProject = "aurora", "proj-1234"
	r.res.Stages = []initflow.StageResult{{Name: "preflight", State: initflow.Done}, {Name: "installation", State: initflow.Failed, Detail: "boom"}}
	r.res.Failed = &initflow.Failure{Stage: "installation", Error: "boom", Fix: "rerun"}
	r.res.Note = "a note"
	if err := r.printResult(); err != nil {
		t.Fatal(err)
	}
	want := `{
  "project": "aurora",
  "gcp_project": "proj-1234",
  "applied": false,
  "stages": [
    {
      "name": "preflight",
      "state": "done"
    },
    {
      "name": "installation",
      "state": "failed",
      "detail": "boom"
    }
  ],
  "left_for_you": [],
  "failed": {
    "stage": "installation",
    "error": "boom",
    "fix": "rerun"
  },
  "note": "a note"
}
`
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

// With --json a failed converge still prints its account on stdout.
func TestInitJSONOnFailure(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["apply"] = map[string]any{"exit": 1}
	r.save(t)
	out, _, err := executeStdin(t, "", "init", "--yes", "--json")
	if err == nil {
		t.Fatal("the apply failed, yet init succeeded")
	}
	var res convergeJSON
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("no JSON on failure: %v\n%q", jerr, out)
	}
	if res.state("installation") != "failed" {
		t.Errorf("stages %+v", res.Stages)
	}
}

// The redact hook reaches the loop.
func TestSecretsHeldReachTheLoopsRedaction(t *testing.T) {
	old := secretsHeld
	secretsHeld = func() []string { return []string{"s3cret"} }
	t.Cleanup(func() { secretsHeld = old })
	r := &initRun{o: &initOptions{}, w: io.Discard, cmd: NewRootCmd()}
	e := &initEngine{r: r, lc: &localcfg.Config{}}
	got := e.options().Redact
	if len(got) == 0 || got[0] != "s3cret" {
		t.Errorf("redact = %q", got)
	}
	// The slots a stage fills with a value it holds are in the same list.
	release, err := e.hold([]byte("held-value"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "held-value") {
		t.Errorf("a held value is not in the loop's list: %q", got)
	}
	release()
	if slices.Contains(got, "held-value") {
		t.Errorf("a released value is still in the loop's list: %q", got)
	}
}

// --firebase runs through the loop: the installation stage is skipped (its
// applies are the Firebase engine's), the Firebase stage applies, and
// there are still three applies.
func TestFirebaseRunsThroughTheLoop(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--budget-mode", "observe", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if res.state("installation") != "skipped" || res.state("firebase") != "changed" || res.state("preflight") != "done" {
		t.Errorf("stages %+v", res.Stages)
	}
	if got := r.applies(t); !slices.Equal(got, []string{"installation", "firebase", "installation"}) {
		t.Errorf("applies %v", got)
	}
}

// --forget and --config-only never enter the loop: no stages in their JSON.
func TestForgetAndConfigOnlyDoNotEnterTheLoop(t *testing.T) {
	for _, flag := range []string{"--config-only", "--forget"} {
		r := newInitRig(t)
		r.stateBucket()
		r.setPlan(t, forgetPlan()...)
		out, _, err := executeStdin(t, "", "init", flag, "--yes", "--json")
		if err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, out)
		}
		var res convergeJSON
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, out)
		}
		if len(res.Stages) != 0 {
			t.Errorf("%s went through the loop: %+v", flag, res.Stages)
		}
	}
}

// --non-interactive on --forget and --config-only: ask() and confirm() never
// read stdin, even at a terminal.
func TestNonInteractiveForgetRefusesWithoutYes(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, forgetPlan()...)
	fakeTerminal(t)
	_, _, err := executeStdin(t, initProjectName+"\n", "init", "--forget", "--non-interactive")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("calls %q", r.calls(t))
	}
	run := &initRun{o: &initOptions{nonInteractive: true}, cmd: NewRootCmd(), w: io.Discard, in: bufio.NewReader(strings.NewReader(initProjectName + "\n"))}
	if ok, err := run.ask("x"); ok || err != nil {
		t.Errorf("ask under --non-interactive = %v, %v", ok, err)
	}
	if err := run.confirm("x", "nothing"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("confirm: %v", err)
	}
}
