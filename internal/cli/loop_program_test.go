package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/spf13/cobra"
)

// loop_program_test.go pins the command name the converge loop prints
// (initflow's Options.Program, task W1.1 of
// docs/setup-wizard/implementation-plan.md): internal/cli/init_stages.go's
// options() is the only place this package builds the loop's Options, and
// every line the loop prints names the front door the user is running — the
// plan view's header, the result line, the failure's fix and the cancelled
// run's fix, the last two in the human account and in --json's failed.fix.
//
// It is a pin on the CLI's own output rather than on the loop:
// internal/initflow's TestLoopNamesTheCallerProgram covers the mechanism (a
// caller that names a program gets that name), and it would pass just as well
// if this package named none and every user read the loop's bare default.
//
// There is one front door today, `fugaro init`, and it is the string every
// line here has to carry: W1.2 splits the command into `fugaro setup` and
// `fugaro update`, and this file is renamed with it.
const loopProgramWant = "fugaro init"

func TestConvergeOutputNamesTheFrontDoor(t *testing.T) {
	const region = "us-east5"
	var out, js bytes.Buffer
	r := &initRun{cmd: &cobra.Command{}, o: &initOptions{yes: true, asJSON: true}, w: &out}
	r.cmd.SetOut(&js)
	r.projectName, r.gcpProject = initProjectName, initProject
	e := &initEngine{r: r, lc: &localcfg.Config{Name: initProjectName, Region: region}}

	// One applying run that stops at a failed stage: the header, the result
	// line and the failure's fix are all printed by the loop in this one run.
	stage := &loopNameStage{name: initflow.Installation, err: errors.New("terraform apply: quota exceeded")}
	res, err := initflow.Run(context.Background(), []initflow.Stage{stage}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed == nil {
		t.Fatalf("the stage did not fail: %+v", res)
	}
	human := out.String()
	for _, want := range []string{
		loopProgramWant + ": project " + initProjectName + " (GCP " + initProject + ", " + region + ")",
		loopProgramWant + ": result",
		"fix what the error says and rerun " + loopProgramWant + ": it resumes at " + initflow.Installation,
	} {
		if !strings.Contains(human, want) {
			t.Errorf("%q is not in the run's account:\n%s", want, human)
		}
	}
	// The loop's own bare default ("fugaro") is what a caller that names no
	// program gets; the CLI is not such a caller.
	for _, fallback := range []string{"fugaro: project", "fugaro: result", "rerun fugaro:"} {
		if strings.Contains(human, fallback) {
			t.Errorf("the run fell back to the loop's default name (%q):\n%s", fallback, human)
		}
	}

	// The same run's --json result: failed.fix is the text a CI run reads,
	// and it names the front door like the human account does.
	if err := e.outcome(context.Background(), res, nil); err == nil {
		t.Fatal("a failed stage did not reach the caller as an error")
	}
	var got struct {
		Failed *initflow.Failure `json:"failed"`
	}
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatalf("the run's --json result is not JSON: %v\n%s", err, js.String())
	}
	if want := "fix what the error says and rerun " + loopProgramWant + ": it resumes at " + initflow.Installation; got.Failed == nil || got.Failed.Fix != want {
		t.Errorf("--json failed.fix = %+v, want %q", got.Failed, want)
	}

	// A cancelled run is a failure too, and its fix is the other fix text.
	out.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err = initflow.Run(ctx, []initflow.Stage{stage}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed == nil || !strings.Contains(out.String(), "fix: rerun "+loopProgramWant+": it resumes at "+initflow.Installation) {
		t.Errorf("the cancelled run's account does not name the front door: %+v\n%s", res.Failed, out.String())
	}
}

// loopNameStage is a stage with nothing behind it: a check that finds work to
// do and an apply that fails with the error the test hands it, which is all
// the loop needs to print its header, its result line and a fix.
type loopNameStage struct {
	name string
	err  error
}

func (s *loopNameStage) Name() string { return s.name }
func (s *loopNameStage) Check(context.Context) (initflow.Status, error) {
	return initflow.Status{State: initflow.Todo, Detail: "work to do"}, nil
}
func (s *loopNameStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	return initflow.Plan{Detail: "one apply"}, nil
}
func (s *loopNameStage) Apply(context.Context, initflow.Env) (initflow.Outcome, error) {
	return initflow.Outcome{}, s.err
}
func (s *loopNameStage) Verify(context.Context) error { return nil }
func (s *loopNameStage) Left() initflow.Left {
	return initflow.Left{Stage: s.name, Kind: initflow.LeftCommand, Text: "run it yourself"}
}
