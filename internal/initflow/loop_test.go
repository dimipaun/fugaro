package initflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fake is a stage with no cloud behind it: it logs every call into the
// shared log, so a test reads the order and the absence of calls.
type fake struct {
	name   string
	log    *[]string
	status Status
	// statusAfter, when set, is what Check says once Apply has run.
	applied     bool
	applyErr    error
	verifyErr   error
	unchanged   bool // Apply finds nothing to do
	left        Left
	missing     []string
	planSummary string
	planEmpty   bool
	gotEnv      *Env
}

func (f *fake) Name() string { return f.name }
func (f *fake) rec(what string) {
	*f.log = append(*f.log, what+" "+f.name)
}
func (f *fake) Check(context.Context) (Status, error) {
	f.rec("check")
	if f.status.State == "" {
		return Status{State: Todo}, nil
	}
	return f.status, nil
}
func (f *fake) Plan(context.Context, Env) (Plan, error) {
	f.rec("plan")
	return Plan{Detail: f.planSummary, NothingToDo: f.planEmpty}, nil
}
func (f *fake) Apply(_ context.Context, env Env) (Outcome, error) {
	f.rec("apply")
	f.gotEnv = &env
	if f.applyErr != nil {
		return Outcome{}, f.applyErr
	}
	f.applied = true
	if f.unchanged {
		return Outcome{Detail: "No changes"}, nil
	}
	return Outcome{Changed: true, Detail: "applied"}, nil
}
func (f *fake) Verify(context.Context) error { f.rec("verify"); return f.verifyErr }
func (f *fake) Left() Left {
	if f.left.Text != "" {
		return f.left
	}
	return Left{Stage: f.name, Kind: LeftPrompt, Text: "run fugaro init in your terminal"}
}
func (f *fake) Missing() []string { return f.missing }

func fakes(log *[]string, names ...string) []Stage {
	var out []Stage
	for _, n := range names {
		out = append(out, &fake{name: n, log: log})
	}
	return out
}

func yesOpts() Options {
	return Options{Yes: true, Terminal: true, Out: &bytes.Buffer{}}
}

func count(log []string, prefix string) int {
	n := 0
	for _, l := range log {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func states(r *Result) map[string]State {
	m := map[string]State{}
	for _, s := range r.Stages {
		m[s.Name] = s.State
	}
	return m
}

func TestStageOrderMatchesDependencies(t *testing.T) {
	names := Names()
	if len(names) != 10 {
		t.Fatalf("the design has ten stages, got %v", names)
	}
	pos := map[string]int{}
	for i, n := range names {
		pos[n] = i
	}
	for _, n := range names {
		for _, d := range Needs(n) {
			p, ok := pos[d]
			if !ok || p >= pos[n] {
				t.Errorf("%s needs %s, which does not come before it", n, d)
			}
		}
	}
	// The order the dogfooding run found by hand.
	want := []string{Preflight, Project, Services, Installation, Firebase, Images, Installation2, Secrets, Plugin, Repository}
	if !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestValidateOrdersAndRejects(t *testing.T) {
	var log []string
	shuffled := fakes(&log, Secrets, Installation, Preflight)
	got, err := Validate(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name())
	}
	if !slices.Equal(names, []string{Preflight, Installation, Secrets}) {
		t.Errorf("not in canonical order: %v", names)
	}
	if _, err := Validate(fakes(&log, "nonsense")); err == nil {
		t.Error("an unknown stage was accepted")
	}
	if _, err := Validate(fakes(&log, Preflight, Preflight)); err == nil {
		t.Error("a stage registered twice was accepted")
	}
}

func TestConvergeFromNothing(t *testing.T) {
	// No state, no config, nothing done: every stage applies once, in the
	// design's order, each verified before the next starts.
	var log []string
	stages := fakes(&log, Repository, Secrets, Installation2, Images, Firebase, Installation, Preflight)
	res, err := Run(context.Background(), stages, yesOpts())
	if err != nil {
		t.Fatal(err)
	}
	var applies []string
	for _, l := range log {
		if strings.HasPrefix(l, "apply ") {
			applies = append(applies, strings.TrimPrefix(l, "apply "))
		}
	}
	want := []string{Preflight, Installation, Firebase, Images, Installation2, Secrets, Repository}
	// Secrets is not covered by --yes: with a terminal it is applied with
	// Yes false, and the stage itself prompts.
	if !slices.Equal(applies, want) {
		t.Errorf("applied %v, want %v", applies, want)
	}
	for i, l := range log {
		if strings.HasPrefix(l, "apply ") && (i+1 >= len(log) || log[i+1] != "verify "+strings.TrimPrefix(l, "apply ")) {
			t.Errorf("%s is not followed by its verify: %v", l, log)
		}
	}
	if res.ExitCode() != 0 {
		t.Errorf("exit %d", res.ExitCode())
	}
	for _, s := range res.Stages {
		if s.State != Changed {
			t.Errorf("%s is %s", s.Name, s.State)
		}
	}
}

func TestRerunPlansNothing(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	for _, s := range stages {
		s.(*fake).status = Status{State: Done}
	}
	res, err := Run(context.Background(), stages, yesOpts())
	if err != nil {
		t.Fatal(err)
	}
	if n := count(log, "apply") + count(log, "plan") + count(log, "verify"); n != 0 {
		t.Errorf("a rerun with nothing to do did work: %v", log)
	}
	if res.ExitCode() != 0 {
		t.Errorf("exit %d", res.ExitCode())
	}
	for _, s := range res.Stages {
		if s.State != Done {
			t.Errorf("%s is %s, want done", s.Name, s.State)
		}
	}
}

func TestRerunReportsNoChangesPerStage(t *testing.T) {
	// A stage whose check cannot tell (it plans when applied) applies, finds
	// nothing, and is reported done with "No changes", exit 0.
	var log []string
	stages := fakes(&log, Preflight, Installation)
	stages[1].(*fake).unchanged = true
	res, err := Run(context.Background(), stages, yesOpts())
	if err != nil {
		t.Fatal(err)
	}
	got := res.Stages[1]
	if got.State != Done || got.Detail != "No changes" {
		t.Errorf("installation = %+v", got)
	}
	if res.ExitCode() != 0 {
		t.Errorf("exit %d", res.ExitCode())
	}
}

func TestStageFailureStopsLoop(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase, Images)
	stages[1].(*fake).applyErr = &StageError{Err: errors.New("terraform apply: quota exceeded\nDetails: {...}"), Fix: "raise the quota, then rerun fugaro init"}
	res, err := Run(context.Background(), stages, yesOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode() != 2 {
		t.Errorf("exit %d, want 2", res.ExitCode())
	}
	if res.Failed == nil || res.Failed.Stage != Installation || res.Failed.Fix != "raise the quota, then rerun fugaro init" {
		t.Fatalf("failed = %+v", res.Failed)
	}
	if strings.Contains(res.Failed.Error, "\n") {
		t.Errorf("the error is not one line: %q", res.Failed.Error)
	}
	for _, l := range log {
		if strings.HasSuffix(l, " "+Firebase) && !strings.HasPrefix(l, "check") || strings.HasSuffix(l, " "+Images) && !strings.HasPrefix(l, "check") {
			t.Errorf("a stage after the failure ran: %v", log)
		}
	}
	st := states(res)
	if st[Installation] != Failed || st[Firebase] != Blocked || st[Images] != Blocked {
		t.Errorf("states %v", st)
	}
}

func TestVerifyFailureFailsStageAndStopsLoop(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[1].(*fake).verifyErr = errors.New("the bucket is not there")
	res, _ := Run(context.Background(), stages, yesOpts())
	if res.ExitCode() != 2 || res.Failed.Stage != Installation {
		t.Fatalf("%+v", res)
	}
	if count(log, "apply "+Firebase) != 0 {
		t.Error("the next stage ran before its predecessor verified")
	}
}

func TestResumeAfterFailedStage(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[1].(*fake).applyErr = errors.New("boom")
	res, _ := Run(context.Background(), stages, yesOpts())
	if res.ExitCode() != 2 {
		t.Fatalf("exit %d", res.ExitCode())
	}
	// The user fixes it and reruns: what was done is done, the rest runs.
	log = nil
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).applyErr = nil
	res, _ = Run(context.Background(), stages, yesOpts())
	if res.ExitCode() != 0 {
		t.Fatalf("exit %d: %+v", res.ExitCode(), res.Failed)
	}
	if count(log, "apply "+Preflight) != 0 || count(log, "apply "+Installation) != 1 || count(log, "apply "+Firebase) != 1 {
		t.Errorf("resume did the wrong work: %v", log)
	}
}

func TestBlockedOnUserStopsWithLeftForYou(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Secrets, Repository)
	stages[2].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Secrets, Kind: LeftPrompt, Text: "run fugaro init in your terminal: github-app-key, claude-oauth-token (hidden prompts)"}}
	res, _ := Run(context.Background(), stages, yesOpts())
	if len(res.Left) != 1 || res.Left[0].Stage != Secrets || res.Left[0].Kind != LeftPrompt {
		t.Fatalf("left = %+v", res.Left)
	}
	if st := states(res); st[Secrets] != NeedsYou || st[Repository] != Blocked || st[Installation] != Changed {
		t.Errorf("states %v", st)
	}
	if count(log, "apply "+Repository) != 0 {
		t.Error("the stage after needs-you ran")
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit %d: an incomplete converge must not look finished to a script", res.ExitCode())
	}
}

func TestNoApplyWithoutConfirmation(t *testing.T) {
	// Not a terminal, no --yes, not --non-interactive: nothing is applied,
	// and the refusal names the way out.
	var log []string
	stages := fakes(&log, Preflight, Installation)
	_, err := Run(context.Background(), stages, Options{Out: &bytes.Buffer{}})
	var nt *NoTerminalError
	if !errors.As(err, &nt) {
		t.Fatalf("err = %v", err)
	}
	if nt.Stage != Preflight && nt.Stage != Installation {
		t.Errorf("stage %q", nt.Stage)
	}
	if !strings.Contains(err.Error(), "terminal") || strings.Contains(err.Error(), "\n") {
		t.Errorf("message %q", err)
	}
	if count(log, "apply") != 0 {
		t.Errorf("applied without a confirmation: %v", log)
	}
}

func TestNoTerminalRefusesToPrompt(t *testing.T) {
	// With a terminal nothing is refused; without one the stage is never
	// given the chance to prompt.
	var log []string
	f := &fake{name: Installation, log: &log}
	_, err := Run(context.Background(), []Stage{f}, Options{Out: &bytes.Buffer{}, Terminal: false})
	if err == nil || f.gotEnv != nil {
		t.Fatalf("err=%v env=%v", err, f.gotEnv)
	}
	f2 := &fake{name: Installation, log: &log}
	if _, err := Run(context.Background(), []Stage{f2}, Options{Out: &bytes.Buffer{}, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	if f2.gotEnv == nil || !f2.gotEnv.Interactive || f2.gotEnv.Yes {
		t.Errorf("env %+v: a terminal run lets the stage prompt, and --yes was not given", f2.gotEnv)
	}
}

func TestNonInteractiveWithoutYesIsPlanOnly(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation)
	res, err := Run(context.Background(), stages, Options{NonInteractive: true, Terminal: true, Out: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	if count(log, "apply") != 0 || !res.PlanOnly || res.Note == "" {
		t.Errorf("apply without --yes under --non-interactive: log=%v planOnly=%v note=%q", log, res.PlanOnly, res.Note)
	}
	if res.ExitCode() != 0 {
		t.Errorf("exit %d", res.ExitCode())
	}
}

func TestNonInteractiveNeverPrompts(t *testing.T) {
	var log []string
	f := &fake{name: Installation, log: &log}
	opts := Options{NonInteractive: true, Yes: true, Terminal: true, Out: &bytes.Buffer{}}
	if _, err := Run(context.Background(), []Stage{f}, opts); err != nil {
		t.Fatal(err)
	}
	if f.gotEnv == nil || f.gotEnv.Interactive {
		t.Errorf("env %+v: --non-interactive must tell the stage it may not prompt", f.gotEnv)
	}
}

func TestNonInteractiveListsAllMissingFlags(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[1].(*fake).missing = []string{"--gcp-project (the GCP project ID)", "--region"}
	stages[2].(*fake).missing = []string{"--firebase (the Firebase project)"}
	_, err := Run(context.Background(), stages, Options{NonInteractive: true, Yes: true, Out: &bytes.Buffer{}})
	var mi *MissingInputsError
	if !errors.As(err, &mi) {
		t.Fatalf("err = %v", err)
	}
	if len(mi.Flags) != 3 {
		t.Errorf("flags %v", mi.Flags)
	}
	for _, f := range []string{"--gcp-project", "--region", "--firebase"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("%q missing from %q", f, err)
		}
	}
	if len(log) != 0 {
		t.Errorf("a stage ran before the missing flags were reported: %v", log)
	}
	// Interactive runs do not refuse: the stages prompt for their inputs.
	if _, err := Run(context.Background(), stages, Options{Yes: true, Terminal: true, Out: &bytes.Buffer{}}); err != nil {
		t.Errorf("interactive: %v", err)
	}
}

func TestPlanOnlyChangesNothing(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).planSummary = "14 to create, 0 to change, 0 to delete"
	opts := yesOpts()
	opts.PlanOnly = true
	res, err := Run(context.Background(), stages, opts)
	if err != nil {
		t.Fatal(err)
	}
	if count(log, "apply") != 0 || count(log, "verify") != 0 {
		t.Errorf("--plan-only changed something: %v", log)
	}
	// Only the stage that can be planned now is planned; the one after it
	// says it plans after.
	if count(log, "plan "+Installation) != 1 || count(log, "plan "+Firebase) != 0 {
		t.Errorf("plans: %v", log)
	}
	if res.Stages[1].Detail != "14 to create, 0 to change, 0 to delete" || res.Stages[1].State != Todo {
		t.Errorf("installation %+v", res.Stages[1])
	}
	if res.Stages[2].State != Todo || res.Stages[2].After != Installation {
		t.Errorf("firebase %+v", res.Stages[2])
	}
	if res.ExitCode() != 0 || !res.PlanOnly {
		t.Errorf("exit %d planonly %v", res.ExitCode(), res.PlanOnly)
	}
}

func TestYesDoesNotCoverCreateOrBilling(t *testing.T) {
	// The project stage (creation, billing) and the secrets stage are never
	// given --yes: in a terminal they are applied with Yes false so they
	// prompt; without one they are never applied, they are left for you.
	for _, name := range []string{Project, Secrets} {
		var log []string
		f := &fake{name: name, log: &log, left: Left{Stage: name, Kind: LeftCommand, Text: "fugaro init --create-project --gcp-project ID"}}
		opts := Options{Yes: true, Terminal: true, Out: &bytes.Buffer{}}
		if _, err := Run(context.Background(), []Stage{f}, opts); err != nil {
			t.Fatal(err)
		}
		if f.gotEnv == nil || f.gotEnv.Yes {
			t.Errorf("%s: --yes reached the stage: %+v", name, f.gotEnv)
		}

		log = nil
		f = &fake{name: name, log: &log, left: Left{Stage: name, Kind: LeftCommand, Text: "fugaro init --create-project --gcp-project ID"}}
		opts = Options{Yes: true, NonInteractive: true, Terminal: true, Out: &bytes.Buffer{}}
		res, err := Run(context.Background(), []Stage{f}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if f.gotEnv != nil || count(log, "apply") != 0 {
			t.Errorf("%s: applied under --yes --non-interactive: %v", name, log)
		}
		if len(res.Left) != 1 || res.Left[0].Stage != name || res.ExitCode() != 1 {
			t.Errorf("%s: left %+v exit %d", name, res.Left, res.ExitCode())
		}

		// No terminal at all, --yes given: still never applied.
		log = nil
		f = &fake{name: name, log: &log}
		res, err = Run(context.Background(), []Stage{f}, Options{Yes: true, Out: &bytes.Buffer{}})
		if err != nil {
			t.Fatal(err)
		}
		if f.gotEnv != nil || len(res.Left) != 1 {
			t.Errorf("%s: no terminal: env %v left %v", name, f.gotEnv, res.Left)
		}
	}
	// A stage --yes does cover gets it.
	var log []string
	f := &fake{name: Installation, log: &log}
	Run(context.Background(), []Stage{f}, yesOpts())
	if f.gotEnv == nil || !f.gotEnv.Yes {
		t.Errorf("installation did not get --yes: %+v", f.gotEnv)
	}
}

func TestStageDecliningIsNotAFailure(t *testing.T) {
	// A stage that asks and is not answered (the typed name was wrong) stops
	// the loop with what to do, and the rest is blocked.
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[1].(*fake).applyErr = &NeedsYouError{Left: Left{Stage: Installation, Kind: LeftPrompt, Text: "rerun fugaro init and type the project's name"}}
	res, _ := Run(context.Background(), stages, yesOpts())
	if res.Failed != nil || res.ExitCode() != 1 || len(res.Left) != 1 {
		t.Errorf("%+v exit %d", res, res.ExitCode())
	}
	if states(res)[Firebase] != Blocked {
		t.Error("firebase ran or is not blocked")
	}
}

func TestPredecessorNeedsYouBlocksApply(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation)
	stages[0].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Preflight, Kind: LeftCommand, Text: "unset GOOGLE_PROJECT"}}
	opts := yesOpts()
	Run(context.Background(), stages, opts)
	if count(log, "apply") != 0 || count(log, "check "+Installation) > 1 {
		t.Errorf("%v", log)
	}
}

func TestSkippedPredecessorDoesNotBlock(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Firebase, Repository)
	stages[1].(*fake).status = Status{State: Skipped, Detail: "no --firebase given"}
	stages[2].(*fake).status = Status{State: Skipped, Detail: "no fugaro.yaml on the default branch"}
	res, _ := Run(context.Background(), stages, yesOpts())
	if res.ExitCode() != 0 || states(res)[Repository] != Skipped {
		t.Errorf("%+v", res)
	}
}

func TestOutputsContainNoSecretValues(t *testing.T) {
	const secret = "s3cr3t-value-XYZ"
	var log []string
	stages := fakes(&log, Preflight, Installation, Secrets)
	stages[1].(*fake).applyErr = fmt.Errorf("apply failed using token %s\nmore", secret)
	var out bytes.Buffer
	opts := Options{Yes: true, Terminal: true, Out: &out, Redact: []string{secret}}
	res, _ := Run(context.Background(), stages, opts)
	js, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secret) || strings.Contains(string(js), secret) || strings.Contains(res.Failed.Error, secret) {
		t.Errorf("a secret value reached the output:\n%s\n%s", out.String(), js)
	}
}

func TestLeftTextIsOneLine(t *testing.T) {
	var log []string
	stages := fakes(&log, Secrets)
	stages[0].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Secrets, Kind: LeftCommand, Text: "fugaro secrets set a \\\n  --file x\nsecond"}}
	res, _ := Run(context.Background(), stages, yesOpts())
	if strings.ContainsAny(res.Left[0].Text, "\n\\") {
		t.Errorf("left text %q", res.Left[0].Text)
	}
}

func TestJSONIsStable(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Secrets)
	stages[0].(*fake).status = Status{State: Done}
	stages[2].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Secrets, Kind: LeftPrompt, Text: "run fugaro init in your terminal"}}
	opts := yesOpts()
	opts.Project, opts.GCPProject, opts.Region = "aurora", "aurora-prod", "us-east5"
	res, _ := Run(context.Background(), stages, opts)
	got, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "project": "aurora",
  "gcp_project": "aurora-prod",
  "region": "us-east5",
  "plan_only": false,
  "stages": [
    {
      "name": "preflight",
      "state": "done"
    },
    {
      "name": "installation",
      "state": "changed",
      "detail": "applied"
    },
    {
      "name": "secrets",
      "state": "needs-you"
    }
  ],
  "left_for_you": [
    {
      "stage": "secrets",
      "kind": "prompt",
      "text": "run fugaro init in your terminal"
    }
  ]
}
`
	if string(got) != want {
		t.Errorf("json:\n%s\nwant:\n%s", got, want)
	}
	// left_for_you is an array even when empty.
	log = nil
	res, _ = Run(context.Background(), fakes(&log, Preflight), yesOpts())
	js, _ := res.JSON()
	var m map[string]any
	if err := json.Unmarshal(js, &m); err != nil {
		t.Fatal(err)
	}
	if l, ok := m["left_for_you"].([]any); !ok || len(l) != 0 {
		t.Errorf("left_for_you = %v", m["left_for_you"])
	}
}

func TestPlanViewIsHonestAboutDependents(t *testing.T) {
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).planSummary = "14 to create"
	opts := Options{PlanOnly: true, Out: &out, Project: "aurora", GCPProject: "aurora-prod", Region: "us-east5"}
	if _, err := Run(context.Background(), stages, opts); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fugaro init: project aurora (GCP aurora-prod, us-east5)", "[done]", "[will do]", "installation", "14 to create", "after installation"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%q not in the view:\n%s", want, out.String())
		}
	}
}

func TestCheckErrorFailsStage(t *testing.T) {
	var log []string
	f := &fake{name: Preflight, log: &log}
	g := &checkErr{fake: f}
	res, _ := Run(context.Background(), []Stage{g, &fake{name: Installation, log: &log}}, yesOpts())
	if res.ExitCode() != 2 || res.Failed.Stage != Preflight || count(log, "apply") != 0 {
		t.Errorf("%+v %v", res.Failed, log)
	}
}

type checkErr struct{ *fake }

func (c *checkErr) Check(context.Context) (Status, error) { return Status{}, errors.New("cannot read") }

type selfConfirming struct{ *fake }

func (selfConfirming) SelfConfirming() {}

func TestSelfConfirmingStageRefusesItself(t *testing.T) {
	// An engine that shows its plan and refuses without a terminal is let
	// run (so the plan is shown); the loop does not confirm for it.
	var log []string
	f := selfConfirming{&fake{name: Installation, log: &log}}
	if _, err := Run(context.Background(), []Stage{f}, Options{Out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if f.gotEnv == nil || f.gotEnv.Yes || f.gotEnv.Interactive {
		t.Errorf("env %+v", f.gotEnv)
	}
	// A stage --yes does not cover cannot use this: never run without a terminal.
	g := selfConfirming{&fake{name: Secrets, log: &log}}
	res, _ := Run(context.Background(), []Stage{g}, Options{Out: &bytes.Buffer{}})
	if g.gotEnv != nil || len(res.Left) != 1 {
		t.Errorf("secrets ran without a terminal: %+v", g.gotEnv)
	}
}

func TestPlanOnlyEmptyPlanIsDone(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).planEmpty = true
	opts := yesOpts()
	opts.PlanOnly = true
	res, _ := Run(context.Background(), stages, opts)
	if res.Stages[1].State != Done || res.Stages[1].Detail != "No changes" {
		t.Errorf("installation %+v", res.Stages[1])
	}
	// Nothing waits for it: the next stage is the one planned.
	if count(log, "plan "+Firebase) != 1 || res.Stages[2].After != "" {
		t.Errorf("firebase %+v, log %v", res.Stages[2], log)
	}
}
