package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Left []leftJSON `json:"left_for_you"`
}

// leftJSON is one left-for-you entry; a list (the commands) reads as its
// lines joined by newlines.
type leftJSON map[string]string

func (l *leftJSON) UnmarshalJSON(b []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*l = leftJSON{}
	for k, v := range raw {
		if list, ok := v.([]any); ok {
			var lines []string
			for _, x := range list {
				lines = append(lines, fmt.Sprint(x))
			}
			(*l)[k] = strings.Join(lines, "\n")
		} else {
			(*l)[k] = fmt.Sprint(v)
		}
	}
	return nil
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
// without --firebase-project. --json confirms no step (T7,
// docs/no-prompt-policy), so the run is read off its own account at a real
// terminal instead: one line per stage, and a closing line that is a Done
// because nothing is left for the user.
func TestConvergeFromNothing(t *testing.T) {
	r := newInitRig(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "setup")
	if err != nil {
		t.Fatal(err)
	}
	account := reportLines(out)
	// Done and changed are the same tick to the reporter, so what says the
	// installation changed is the tick over the one apply below.
	if l := stepLineOf(account, "installation"); !strings.HasPrefix(l, markDone+" installation ") {
		t.Errorf("the installation is not a tick: %q\n%s", l, out)
	}
	if l := stepLineOf(account, "firebase"); !strings.HasPrefix(l, markSkipped+" firebase") {
		t.Errorf("firebase is not skipped without --firebase-project: %q\n%s", l, out)
	}
	if l := stepLineOf(account, "preflight"); !strings.HasPrefix(l, markDone+" preflight") {
		t.Errorf("the preflight is not a tick: %q\n%s", l, out)
	}
	if last := account[len(account)-1]; !strings.HasPrefix(last, "Done in ") {
		t.Errorf("the closing line is %q, want the Done summary: nothing is left for the user", last)
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
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "setup")
	if err != nil {
		t.Fatal(err)
	}
	// The stage line's reason and the "No changes" note are the run's own
	// account of a stage nobody is asked about, so they are in its log (§9.2).
	whole := out + newestLog(t, "setup")
	if len(r.ran(t, "apply")) != 0 || !strings.Contains(whole, markDone+" done") || !strings.Contains(whole, "No changes") {
		t.Errorf("calls %q\n%s", r.calls(t), whole)
	}
}

// A run at a real terminal confirms each step by being there: the plan it
// planned is applied, nothing is typed at it, and no line of stdin is read.
func TestATerminalConfirmsTheRunWithNothingTyped(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	spy := &readSpy{Reader: strings.NewReader(initProjectName + "\n")}
	out, _, err := executeStdinReader(t, spy, "setup")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(r.ran(t, "apply")) != 1 {
		t.Errorf("calls %q", r.calls(t))
	}
	if strings.Contains(out, "Type "+initProjectName+" to apply") || spy.read {
		t.Errorf("the run asked for a typed answer (read %v):\n%s", spy.read, out)
	}
}

// A confirmation that cannot be had says so in one line, and that line names
// the terminal it needs rather than a flag: the state bucket's creation is
// such a step, and a plan-only run creates nothing on any word.
func TestTheStateBucketCreationNeedsARealTerminal(t *testing.T) {
	r := newInitRig(t)
	noTerminal(t)
	_, _, err := executeStdin(t, "", "setup", "--plan-only")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "needs a real terminal") ||
		strings.Contains(err.Error(), "--yes") || strings.Contains(err.Error(), "\n") {
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
	var ny *initflow.NeedsYouError
	if !errors.As(err, &ny) || strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err %v: the location is the user's, and --yes does not give it", err)
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
	out, _, err := executeStdin(t, "", "setup", "--plan-only", "--json")
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
		return e.outcome(context.Background(), res, err)
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
	out, _, err := executeStdin(t, "", "setup", "--json")
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

// --firebase-project runs through the loop: the installation stage is skipped (its
// applies are the Firebase engine's), the Firebase stage applies, and
// there are still three applies. --json confirms no step (T11,
// docs/no-prompt-policy), so the stages are read off the account at a real
// terminal: the location is typed nowhere (T7), and the person being there
// is the whole of its answer.
func TestFirebaseRunsThroughTheLoop(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	fakeTerminal(t)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	account := reportLines(out)
	if l := stepLineOf(account, "installation"); !strings.HasPrefix(l, markSkipped+" installation") {
		t.Errorf("installation is not skipped: %q\n%s", l, out)
	}
	if l := stepLineOf(account, "firebase"); !strings.HasPrefix(l, markDone+" firebase") {
		t.Errorf("firebase is not a tick: %q\n%s", l, out)
	}
	if l := stepLineOf(account, "preflight"); !strings.HasPrefix(l, markDone+" preflight") {
		t.Errorf("preflight is not a tick: %q\n%s", l, out)
	}
	if got := r.applies(t); !slices.Equal(got, []string{"installation", "firebase", "installation"}) {
		t.Errorf("applies %v", got)
	}
}

// --forget and --config-only never enter the loop: the run prints no stage
// line at all. --json confirms no step (T11, docs/no-prompt-policy), so the
// account is read at a real terminal, where the person being there is the
// confirmation each of their own writes takes.
func TestForgetAndConfigOnlyDoNotEnterTheLoop(t *testing.T) {
	for _, tc := range []struct{ flag, door string }{
		{"--config-only", "setup"},
		// --forget is the update door's flag (W1.2): setup refuses it, and a
		// rollback is a re-converge, so this row runs the door that takes it.
		{"--forget", "update"},
	} {
		flag := tc.flag
		r := newInitRig(t)
		r.stateBucket()
		r.setPlan(t, forgetPlan()...)
		fakeTerminal(t)
		out, _, err := executeStdin(t, "", tc.door, flag)
		if err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, out)
		}
		if names := stepNames(out); len(names) != 0 {
			t.Errorf("%s went through the loop: %v\n%s", flag, names, out)
		}
	}
}

// --forget's two confirmations come from the terminal a person is at: the
// rollback applies and the state entry is removed with nothing typed, and
// no line of stdin is read.
func TestForgetTakesItsConfirmationsFromTheTerminal(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, forgetPlan()...)
	fakeTerminal(t)
	spy := &readSpy{Reader: strings.NewReader(initProjectName + "\n")}
	_, _, err := executeStdinReader(t, spy, "update", "--forget")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ran(t, "state rm")) != 1 || len(r.ran(t, "apply")) < 1 {
		t.Errorf("calls %q", r.calls(t))
	}
	if spy.read {
		t.Error("stdin was read")
	}
}
