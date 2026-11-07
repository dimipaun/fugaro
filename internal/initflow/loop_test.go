package initflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/report"
)

// fake is a stage with no cloud behind it: it logs every call into the
// shared log, so a test reads the order and the absence of calls.
type fake struct {
	name   string
	log    *[]string
	status Status
	// statusAfter, when set, is what Check says once Apply has run.
	applied   bool
	applyErr  error
	verifyErr error
	unchanged bool // Apply finds nothing to do
	// onApply, when set, runs inside Apply, before it returns: what the run's
	// own writer holds at the moment this stage starts its work, which is
	// where a stage that takes minutes is watched from.
	onApply func()
	// outLeft is the step Apply leaves for the user: a stage that applied
	// what this session may do and left the rest (Outcome.Left).
	outLeft     *Left
	left        Left
	missing     []string
	planSummary string
	planEmpty   bool
	gotEnv      *Env
	planEnv     *Env
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
func (f *fake) Plan(_ context.Context, env Env) (Plan, error) {
	f.rec("plan")
	f.planEnv = &env
	return Plan{Detail: f.planSummary, NothingToDo: f.planEmpty}, nil
}
func (f *fake) Apply(_ context.Context, env Env) (Outcome, error) {
	f.rec("apply")
	f.gotEnv = &env
	if f.onApply != nil {
		f.onApply()
	}
	if f.applyErr != nil {
		return Outcome{}, f.applyErr
	}
	f.applied = true
	if f.unchanged {
		return Outcome{Detail: "No changes", Left: f.outLeft}, nil
	}
	return Outcome{Changed: true, Detail: "applied", Left: f.outLeft}, nil
}
func (f *fake) Verify(context.Context) error { f.rec("verify"); return f.verifyErr }
func (f *fake) Left() Left {
	if f.left.Text != "" {
		return f.left
	}
	return Left{Stage: f.name, Kind: LeftPrompt, Text: "run fugaro update in your terminal"}
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

// outputLines is a view's non-empty lines: every line of it is one of the
// run's own, so an empty one is a bug and not a line.
func outputLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// reporterTo builds the reporter a run's steps print through, over the same
// writer the loop's own lines go to: the CLI points the two at one terminal,
// and these tests read one view. Colour is off and the writer is not a
// terminal, so nothing a rule would not draw reaches a buffer.
func reporterTo(out *bytes.Buffer, o report.Options) *report.Reporter {
	o.Out, o.NoColor = out, true
	return report.New(o)
}

// marks are the reporter's own markers, read from a reporter rather than
// copied: the tests below assert the loop's lines carry them, so a change to
// a mark stays a change in one place.
type marks struct{ done, skipped, failed, left string }

func reporterMarks(t *testing.T) marks {
	t.Helper()
	probe := func(end func(*report.Step)) string {
		var b bytes.Buffer
		r := report.New(report.Options{Out: &b, NoColor: true})
		end(r.Step("probe"))
		return strings.Fields(strings.TrimSpace(b.String()))[0]
	}
	return marks{
		done:    probe(func(s *report.Step) { s.Done("") }),
		skipped: probe(func(s *report.Step) { s.Skipped("") }),
		failed:  probe(func(s *report.Step) { s.Failed(errors.New("x"), nil) }),
		left:    probe(func(s *report.Step) { s.Left("") }),
	}
}

// steps is the run's own account in a view: the lines the reporter drew,
// which are the ones a marker starts. The plan view above them is the review
// screen and is not a step.
func (m marks) steps(view string) []string {
	var out []string
	for _, l := range outputLines(view) {
		if m.isStep(l) {
			out = append(out, l)
		}
	}
	return out
}

func (m marks) isStep(line string) bool {
	for _, mark := range []string{m.done, m.skipped, m.failed, m.left} {
		if strings.HasPrefix(line, mark+" ") {
			return true
		}
	}
	return false
}

// line is the step line a stage's own result was printed on, or "" when the
// view has none. The name is matched whole, so a stage is never found by
// another one's prefix (installation2 is not installation).
func (m marks) line(view, stage string) string {
	for _, l := range m.steps(view) {
		for _, mark := range []string{m.done, m.skipped, m.failed, m.left} {
			rest, ok := strings.CutPrefix(l, mark+" "+stage)
			if ok && (rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, ":")) {
				return l
			}
		}
	}
	return ""
}

func TestStageOrderMatchesDependencies(t *testing.T) {
	names := Names()
	// Ten stages of the installation's own plus the credentials service's
	// owner-run apply (C1.20), the deployed security rules generation
	// (init_rules_stage.go), the published policy node's migration in place
	// (init_policy_migrate.go), the project layer and its one local file
	// (init_layer_stage.go), the checkout's own steps inside fugaro update
	// (init_checkout_upgrade.go) and the compute backend's provisioning, which
	// the design puts last (compute-backend §8.1 task 6).
	if len(names) != 16 {
		t.Fatalf("the design has sixteen stages, got %v", names)
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
	want := []string{Preflight, Project, Services, Installation, Firebase, Images, Installation2, Secrets, Plugin, Credentials, Repository, Rules, Policy, Layer, Checkout, Compute}
	if !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
	// The layer is published before the checkout's images are refreshed off
	// it: the ordering test above only says the entry precedes its stage, and
	// this says the checkout stage actually carries it.
	if !slices.Contains(Needs(Checkout), Layer) {
		t.Errorf("the checkout stage does not need %s: %v; its cloud step builds this checkout's images off the project layer", Layer, Needs(Checkout))
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
	stages[1].(*fake).applyErr = &StageError{Err: errors.New("terraform apply: quota exceeded\nDetails: {...}"), Fix: "raise the quota, then rerun fugaro update"}
	res, err := Run(context.Background(), stages, yesOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode() != 2 {
		t.Errorf("exit %d, want 2", res.ExitCode())
	}
	if res.Failed == nil || res.Failed.Stage != Installation || res.Failed.Fix != "raise the quota, then rerun fugaro update" {
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
	stages[2].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Secrets, Kind: LeftPrompt, Text: "run fugaro update in your terminal: github-app-key, claude-oauth-token (hidden prompts)"}}
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
	stages[2].(*fake).missing = []string{"--firebase-project (the Firebase project)"}
	_, err := Run(context.Background(), stages, Options{NonInteractive: true, Yes: true, Out: &bytes.Buffer{}})
	var mi *MissingInputsError
	if !errors.As(err, &mi) {
		t.Fatalf("err = %v", err)
	}
	if len(mi.Flags) != 3 {
		t.Errorf("flags %v", mi.Flags)
	}
	for _, f := range []string{"--gcp-project", "--region", "--firebase-project"} {
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
		f := &fake{name: name, log: &log, left: Left{Stage: name, Kind: LeftCommand, Text: "fugaro setup --create-project --gcp-project ID"}}
		opts := Options{Yes: true, Terminal: true, Out: &bytes.Buffer{}}
		if _, err := Run(context.Background(), []Stage{f}, opts); err != nil {
			t.Fatal(err)
		}
		if f.gotEnv == nil || f.gotEnv.Yes {
			t.Errorf("%s: --yes reached the stage: %+v", name, f.gotEnv)
		}

		log = nil
		f = &fake{name: name, log: &log, left: Left{Stage: name, Kind: LeftCommand, Text: "fugaro setup --create-project --gcp-project ID"}}
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
	stages[1].(*fake).applyErr = &NeedsYouError{Left: Left{Stage: Installation, Kind: LeftPrompt, Text: "rerun fugaro setup and type the project's name"}}
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
	stages[1].(*fake).status = Status{State: Skipped, Detail: "no --firebase-project given"}
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

func TestProgramNameIsRedacted(t *testing.T) {
	// The caller's Program reaches the header, both fix texts, the human
	// account and --json, so it goes through the same redaction every other
	// line does: a value the process holds is not printed because a caller
	// put it in the name of the program.
	const secret = "s3cr3t-in-program"
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight)
	stages[0].(*fake).applyErr = errors.New("boom")
	res, err := Run(context.Background(), stages, Options{Yes: true, Terminal: true, Out: &out,
		Program: "fugaro run " + secret, Project: "aurora", Redact: []string{secret}})
	if err != nil {
		t.Fatal(err)
	}
	js, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secret) || strings.Contains(string(js), secret) || strings.Contains(res.Failed.Fix, secret) {
		t.Errorf("a held value reached the output through Program:\n%s\n%s", out.String(), js)
	}
	if !strings.Contains(out.String(), "fugaro run [redacted]") {
		t.Errorf("the program name was dropped rather than redacted:\n%s", out.String())
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
	stages[2].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Secrets, Kind: LeftPrompt, Text: "run fugaro update in your terminal"}}
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
      "text": "run fugaro update in your terminal"
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
	for _, want := range []string{"fugaro: project aurora (GCP aurora-prod, us-east5)", "[done]", "[will do]", "installation", "14 to create", "after installation"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%q not in the view:\n%s", want, out.String())
		}
	}
}

func TestLoopNamesTheCallerProgram(t *testing.T) {
	// The loop is behind both front doors and cannot know which one is
	// running (O2): the view's header, the result line and both fix texts
	// name the caller's Program, and the loop names no command of its own.
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Installation)
	stages[1].(*fake).applyErr = errors.New("terraform apply: quota exceeded")
	opts := Options{Yes: true, Terminal: true, Out: &out, Program: "fugaro update",
		Project: "aurora", GCPProject: "aurora-prod", Region: "us-east5"}
	res, err := Run(context.Background(), stages, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fugaro update: project aurora (GCP aurora-prod, us-east5)") {
		t.Errorf("the view does not name the caller's program:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "fugaro update: result") {
		t.Errorf("the result line does not name the caller's program:\n%s", out.String())
	}
	if res.Failed == nil || res.Failed.Fix != "fix what the error says and rerun fugaro update: it resumes at "+Installation {
		t.Errorf("the failure's fix does not name the caller's program: %+v", res.Failed)
	}
	// A cancelled run is a failure as well, and its fix names it too.
	out.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err = Run(ctx, fakes(&log, Preflight), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed == nil || res.Failed.Fix != "rerun fugaro update: it resumes at "+Preflight {
		t.Errorf("the cancelled run's fix does not name the caller's program: %+v", res.Failed)
	}
}

func TestLoopDefaultProgramIsUnchanged(t *testing.T) {
	// A caller that names no program gets the program alone: the loop must
	// not invent a verb it cannot know (O2), and a caller that forgets the
	// field is not silently given the wrong one.
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight)
	stages[0].(*fake).applyErr = errors.New("boom")
	res, err := Run(context.Background(), stages, Options{Yes: true, Terminal: true, Out: &out,
		Project: "aurora", GCPProject: "aurora-prod", Region: "us-east5"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fugaro: project aurora (GCP aurora-prod, us-east5)", "fugaro: result"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%q not in the default program's view:\n%s", want, out.String())
		}
	}
	if res.Failed == nil || res.Failed.Fix != "fix what the error says and rerun fugaro: it resumes at "+Preflight {
		t.Errorf("the default program's failure fix: %+v", res.Failed)
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

func TestValidateRejectsNilStage(t *testing.T) {
	if _, err := Validate([]Stage{nil}); err == nil {
		t.Error("a nil stage was accepted")
	}
}

func TestPanickingStageFailsInsteadOfCrashing(t *testing.T) {
	var log []string
	f := &panicky{fake: &fake{name: Installation, log: &log}}
	res, err := Run(context.Background(), []Stage{f, &fake{name: Firebase, log: &log}}, yesOpts())
	if err != nil || res.Failed == nil || res.Failed.Stage != Installation || !strings.Contains(res.Failed.Error, "panicked") {
		t.Fatalf("%v %+v", err, res.Failed)
	}
	if count(log, "apply "+Firebase) != 0 {
		t.Error("the stage after a panic ran")
	}
}

type panicky struct{ *fake }

func (panicky) Apply(context.Context, Env) (Outcome, error) { panic("boom") }

func TestCheckMayNotReturnChangedFailedOrUnknown(t *testing.T) {
	for _, st := range []State{Changed, Failed, "", "weird"} {
		var log []string
		f := &fake{name: Installation, log: &log}
		g := &stateStage{fake: f, st: st}
		res, err := Run(context.Background(), []Stage{g}, yesOpts())
		if err != nil || res.Failed == nil || !strings.Contains(res.Failed.Error, "Check returned the state") || count(log, "apply") != 0 {
			t.Errorf("state %q: %v %+v %v", st, err, res.Failed, log)
		}
	}
}

type stateStage struct {
	*fake
	st State
}

func (s *stateStage) Check(context.Context) (Status, error) { return Status{State: s.st}, nil }

func TestCancelledContextStopsWithAClearResult(t *testing.T) {
	var log []string
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Run(ctx, fakes(&log, Preflight, Installation), yesOpts())
	if err != nil || res.Failed == nil || !strings.Contains(res.Failed.Error, "cancelled") || count(log, "apply") != 0 {
		t.Fatalf("%v %+v %v", err, res.Failed, log)
	}
}

func TestBlockedStageHoldsBackLaterPlans(t *testing.T) {
	var log []string
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).status = Status{State: Blocked, Detail: "waits for the API"}
	opts := yesOpts()
	opts.PlanOnly = true
	res, _ := Run(context.Background(), stages, opts)
	if count(log, "plan") != 0 || res.Stages[2].After != Installation {
		t.Errorf("planned past a blocked stage: %v %+v", log, res.Stages[2])
	}
}

func TestNoTerminalComesWithThePartialResult(t *testing.T) {
	var log []string
	res, err := Run(context.Background(), fakes(&log, Preflight), Options{Out: &bytes.Buffer{}})
	var nt *NoTerminalError
	if !errors.As(err, &nt) || res == nil || len(res.Stages) != 0 && res.Failed != nil {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestOneLineStripsEscapesAndBlankLead(t *testing.T) {
	got := oneLine("\n\n  \x1b]0;pwned\x07oops \x1b[31mred\x1b[0m\x9b\nsecond")
	if strings.ContainsAny(got, "\x1b\x07\x9b\n") || !strings.HasPrefix(got, "]0;pwnedoops") {
		t.Errorf("%q", got)
	}
	if got := oneLine("\n\nreal line\nnext"); got != "real line" {
		t.Errorf("%q", got)
	}
}

func TestCauseIsRedactedButKeepsItsType(t *testing.T) {
	const secret = "tok-123"
	var log []string
	f := &fake{name: Installation, log: &log, applyErr: &StageError{Err: errors.New("bad " + secret)}}
	res, _ := Run(context.Background(), []Stage{f}, Options{Yes: true, Terminal: true, Out: &bytes.Buffer{}, Redact: []string{secret}})
	if strings.Contains(res.Cause.Error(), secret) {
		t.Errorf("the cause carries the secret: %v", res.Cause)
	}
	var se *StageError
	if !errors.As(res.Cause, &se) {
		t.Error("the cause lost its type")
	}
}

// The conditions of a run, as the matrix names them.
type mode struct {
	name string
	opts Options
}

func modes() []mode {
	out := func() io.Writer { return &bytes.Buffer{} }
	return []mode{
		{"--yes", Options{Yes: true, Terminal: true, Out: out()}},
		{"--non-interactive", Options{NonInteractive: true, Terminal: true, Out: out()}},
		{"--non-interactive --yes", Options{NonInteractive: true, Yes: true, Terminal: true, Out: out()}},
		{"--json", Options{JSON: true, Terminal: true, Out: out()}},
		{"--json --yes", Options{JSON: true, Yes: true, Terminal: true, Out: out()}},
		{"no terminal", Options{Out: out()}},
		{"no terminal --yes", Options{Yes: true, Out: out()}},
		{"agent env", Options{Terminal: true, Agent: "CLAUDECODE", Out: out()}},
		{"agent env --yes", Options{Yes: true, Terminal: true, Agent: "CLAUDECODE", Out: out()}},
		{"agent env --yes --non-interactive", Options{Yes: true, NonInteractive: true, Terminal: true, Agent: "CLAUDECODE", Out: out()}},
		{"terminal", Options{Terminal: true, Out: out()}},
	}
}

// selfStage is an engine-like stage: it is let run without a terminal and
// refuses itself unless it was given --yes or may prompt.
type selfStage struct {
	*fake
	refused bool
}

func (*selfStage) SelfConfirming() {}
func (s *selfStage) Apply(ctx context.Context, env Env) (Outcome, error) {
	if !env.Yes && !env.Interactive {
		s.refused = true
		return Outcome{}, errors.New("needs a terminal or --yes")
	}
	return s.fake.Apply(ctx, env)
}

// TestApplyMatrix: every stage in every mode, and what could apply. The
// truth the help and docs/gcp-setup.md state: --yes covers every stage but the
// project, billing and the secrets; --non-interactive alone only plans; a
// coding agent's environment applies nothing at all, whatever flags it passes.
func TestApplyMatrix(t *testing.T) {
	for _, name := range Names() {
		for _, m := range modes() {
			t.Run(name+"/"+m.name, func(t *testing.T) {
				var log []string
				f := &selfStage{fake: &fake{name: name, log: &log}}
				var st Stage = f
				if !YesCovers(name) {
					st = f.fake // never self-confirming: a stage --yes does not cover
				}
				res, _ := Run(context.Background(), []Stage{st}, m.opts)
				applied := count(log, "apply") > 0
				var want bool
				switch {
				case m.opts.Agent != "":
					want = false
				case !YesCovers(name):
					// A typed confirmation at a real terminal: never --yes (the
					// stage is told it was not given), --non-interactive, --json or a pipe.
					want = m.opts.Terminal && !m.opts.NonInteractive && !m.opts.JSON
				case m.opts.Yes:
					want = true
				case m.opts.NonInteractive:
					want = false // only plans
				default:
					want = m.opts.Terminal // typed at the terminal, else refused
				}
				if applied != want {
					t.Fatalf("applied=%v want %v (log %v, result %+v)", applied, want, log, res)
				}
				if m.opts.Agent != "" {
					if res == nil || res.ExitCode() == 0 && !m.opts.PlanOnly {
						t.Fatalf("an agent's run applied nothing and reported success: %+v", res)
					}
					for _, lf := range res.Left {
						if strings.Contains(lf.Text, "--yes") || slices.ContainsFunc(lf.Commands, func(c string) bool { return strings.Contains(c, "--yes") }) {
							t.Errorf("an agent session was told to pass --yes: %+v", lf)
						}
					}
				}
			})
		}
	}
}

func TestAgentEnvLeavesTheOneLineTypedRoute(t *testing.T) {
	var log []string
	f := &fake{name: Installation, log: &log}
	res, err := Run(context.Background(), []Stage{f}, Options{Yes: true, Terminal: true, Agent: "CLAUDECODE", Out: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	if count(log, "apply") != 0 || len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("log %v left %+v exit %d", log, res.Left, res.ExitCode())
	}
	var detail string
	for _, s := range res.Stages {
		if s.Name == Installation {
			detail = s.Detail
		}
	}
	for _, want := range []string{"your own terminal window", "not through a coding agent", "CLAUDECODE"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q lacks %q", detail, want)
		}
	}
}

func TestAgentEnvStillPlans(t *testing.T) {
	var log []string
	f := &fake{name: Installation, log: &log, planSummary: "2 to create"}
	res, err := Run(context.Background(), []Stage{f}, Options{PlanOnly: true, Terminal: true, Agent: "CLAUDECODE", Out: &bytes.Buffer{}})
	if err != nil || count(log, "apply") != 0 || count(log, "plan") != 1 || res.ExitCode() != 0 {
		t.Fatalf("err %v log %v exit %d", err, log, res.ExitCode())
	}
}

func TestAgentEnvAnEmptyEnginePlanIsDone(t *testing.T) {
	// A converged installation is "No changes" for an agent too: the stage
	// plans (read-only) and finds nothing to apply.
	var log []string
	f := &selfStage{fake: &fake{name: Installation, log: &log, planEmpty: true}}
	res, err := Run(context.Background(), []Stage{f}, Options{Yes: true, Terminal: true, Agent: "CLAUDECODE", Out: &bytes.Buffer{}})
	if err != nil || count(log, "apply") != 0 || res.ExitCode() != 0 {
		t.Fatalf("err %v log %v res %+v", err, log, res)
	}
	if states(res)[Installation] != Done {
		t.Errorf("states %v", states(res))
	}
}

// localStage is a stage whose writes are all on this machine (LocalUnderAgent).
type localStage struct{ *fake }

func (*localStage) LocalUnderAgent() {}

// A LocalUnderAgent stage is the one thing that applies in a coding agent's
// session: its own writes are local, so the marker does not stop them, and the
// refusal of what reaches outside stays the stage's (an AgentError from it is
// the same needs-you).
func TestLocalUnderAgentStageAppliesInAnAgentSession(t *testing.T) {
	var log []string
	f := &localStage{fake: &fake{name: Installation, log: &log}}
	opts := Options{Yes: true, Terminal: true, Agent: "CLAUDECODE", Out: &bytes.Buffer{}}
	res, err := Run(context.Background(), []Stage{f}, opts)
	if err != nil || res.Failed != nil {
		t.Fatalf("err %v res %+v", err, res)
	}
	if count(log, "apply") != 1 || states(res)[Installation] != Changed {
		t.Errorf("it did not apply: log %v states %v", log, states(res))
	}

	// A refusal from inside it (its cloud half) is needs-you, not a failure,
	// and nothing else runs.
	var log2 []string
	g := &localStage{fake: &fake{name: Installation, log: &log2, applyErr: &AgentError{Marker: "CLAUDECODE"}}}
	res, err = Run(context.Background(), []Stage{g}, opts)
	if err != nil || res.Failed != nil || len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("err %v res %+v", err, res)
	}
	if !strings.Contains(res.Stages[0].Detail, "coding agent") {
		t.Errorf("the refusal's own words are missing: %+v", res.Stages[0])
	}

	// The same stage without a marker takes the ordinary path.
	var log3 []string
	h := &localStage{fake: &fake{name: Installation, log: &log3}}
	res, err = Run(context.Background(), []Stage{h}, yesOpts())
	if err != nil || count(log3, "apply") != 1 {
		t.Fatalf("err %v log %v", err, log3)
	}
}

func TestAgentErrorFromAStageIsNeedsYou(t *testing.T) {
	// A refusal inside an engine (its confirmation) is the same needs-you.
	var log []string
	f := &fake{name: Installation, log: &log, applyErr: &AgentError{Marker: "CURSOR_AGENT"}}
	res, err := Run(context.Background(), []Stage{f}, Options{Yes: true, Terminal: true, Out: &bytes.Buffer{}})
	if err != nil || res.Failed != nil || len(res.Left) != 1 {
		t.Fatalf("err %v res %+v", err, res)
	}
}

// A step the managed gate refused is left for the user as a refusal and NEVER
// as the stage's own prompt. The stage's Left is the one thing this reader
// must not be handed: it asks them to type the project's name at a prompt that
// does not come, in a run that has already stopped in the gate's sentence, and
// no confirmation reaches past a refusal (D1012) — the reader who followed it
// would be sent to do the one thing the gate exists to make impossible, which
// is the deadlock CV1 is about (every new repository of the project blocked,
// with a prompt nobody can answer).
//
// Both sessions the gate is asked in are driven, because they are two paths to
// the same Left: a coding agent's session (agentStage) and the owner's own
// terminal (the ruling of 2026-11-01). The control beside them is the other
// half of the rule — a step that is genuinely the user's (a NeedsYouError with
// a prompt) keeps the stage's own text, its kind and its command, so a change
// that turned every left into a refusal fails here too.
func TestARefusedStepIsNeverTheStagesOwnPrompt(t *testing.T) {
	const reason = `the managed gate did not admit this step: nothing is written here (D1012) — refused by cover: module.installation.google_storage_bucket_iam_member.catalog_check_records["acme-app-1"]`
	gate := func(string) bool { return true }
	for _, tc := range []struct {
		name string
		opts func(*Options)
	}{
		{"a coding agent's session", func(o *Options) { o.Agent = "CLAUDECODE"; o.Yes = true }},
		{"the owner's own terminal", func(o *Options) { o.Terminal = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log []string
			var out bytes.Buffer
			f := &fake{name: Installation, log: &log,
				left:     Left{Kind: LeftPrompt, Text: "type the project's name to apply", Commands: []string{"fugaro update"}},
				applyErr: &GatedError{Reason: reason, Err: errors.New(reason)}}
			o := Options{Out: &out, ManagedGate: gate}
			tc.opts(&o)
			res, err := Run(context.Background(), []Stage{f}, o)
			if err != nil || res.Failed != nil {
				t.Fatalf("err %v res %+v", err, res)
			}
			if len(res.Left) != 1 {
				t.Fatalf("the refused step left %d left-for-you, want one: %+v", len(res.Left), res.Left)
			}
			lf := res.Left[0]
			if lf.Kind != LeftRefused {
				t.Errorf("the refused step is a %q, want %q: the stage's own kind asks this reader for a confirmation the gate never reaches", lf.Kind, LeftRefused)
			}
			if cmds := lf.PrintedCommands(); len(cmds) != 0 {
				t.Errorf("the refused step printed the commands %q: no command helps, so listing one is the same mistake as the prompt", cmds)
			}
			// What the reader has to be told: which gate refused it, the
			// gate's own sentence (the resource it would not admit is what an
			// issue needs), that there is nothing to type, and that the cause
			// is the cover and not their project.
			if !strings.Contains(lf.Text, "managed gate refused this step") {
				t.Errorf("the refusal does not say the gate refused it: %q", lf.Text)
			}
			if !strings.Contains(lf.Text, reason) {
				t.Errorf("the refusal does not quote the gate's own sentence, so nothing in it names what was refused:\n%q", lf.Text)
			}
			if !strings.Contains(lf.Text, "Nothing here is yours to type") {
				t.Errorf("the refusal does not say there is nothing to type: %q", lf.Text)
			}
			for _, bad := range []string{"type the project's name", "fugaro update"} {
				if strings.Contains(lf.Text, bad) {
					t.Errorf("the refusal tells the reader to %q, which is what the gate makes impossible: %q", bad, lf.Text)
				}
			}
			// And it is what the run printed, under the kind that says so.
			want := "left for you (" + Installation + ", " + string(LeftRefused) + "): "
			if !strings.Contains(out.String(), want) {
				t.Errorf("the printed block is not %q:\n%s", want, out.String())
			}
			if strings.Contains(out.String(), "left for you ("+Installation+", prompt)") {
				t.Errorf("the refused step printed the stage's own prompt:\n%s", out.String())
			}
			if n := count(log, "apply"); n == 0 {
				t.Error("the stage never reached its apply, so nothing here was judged")
			}
		})

		// The control: the same gate, a step that IS the user's.
		t.Run(tc.name+" — a step that is the user's keeps its own text", func(t *testing.T) {
			prompt := Left{Kind: LeftPrompt, Text: "type the project's name at the review to apply all of its steps", Commands: []string{"fugaro update"}}
			var log []string
			var out bytes.Buffer
			f := &fake{name: Installation, log: &log, left: prompt, applyErr: &NeedsYouError{Left: prompt}}
			o := Options{Out: &out, ManagedGate: gate}
			tc.opts(&o)
			res, err := Run(context.Background(), []Stage{f}, o)
			if err != nil || res.Failed != nil || len(res.Left) != 1 {
				t.Fatalf("err %v res %+v", err, res)
			}
			lf := res.Left[0]
			if lf.Kind != LeftPrompt || lf.Text != prompt.Text {
				t.Errorf("a step left to the user was rewritten to %+v, want the stage's own %+v", lf, prompt)
			}
			if cmds := lf.PrintedCommands(); len(cmds) != 1 || cmds[0] != "fugaro update" {
				t.Errorf("the user's own step lost the command that finishes it: %q", cmds)
			}
			if !strings.Contains(out.String(), "left for you ("+Installation+", prompt): "+prompt.Text) {
				t.Errorf("the printed block is not the stage's own prompt:\n%s", out.String())
			}
		})
	}
}

func TestNoTerminalErrorDropsTheYesHintForAnAgent(t *testing.T) {
	plain := (&NoTerminalError{Stage: "installation"}).Error()
	if !strings.Contains(plain, "--yes") {
		t.Errorf("%q: without an agent the hint stays", plain)
	}
	agent := (&NoTerminalError{Stage: "installation", Agent: "CLAUDECODE"}).Error()
	if strings.Contains(agent, "--yes") || !strings.Contains(agent, "coding agent") {
		t.Errorf("%q", agent)
	}
}

// The zero value of ConfirmClass is Typed, the strictest: a caller that names
// no class — a zero value, a field nobody set, a class left out of a table —
// refuses a confirmation rather than giving one, the direction every mistake in
// this package falls (as with Managed's zero value, and Covers and Removes
// being methods rather than fields). --yes at a real terminal is the sharpest
// way to say it: the permissive class takes it, the strictest does not.
func TestZeroConfirmClassIsTheStrictest(t *testing.T) {
	if ConfirmClass(0) != Typed {
		t.Fatalf("the zero ConfirmClass is %d, want Typed (%d): a caller that names no class must not be handed the permissive one", ConfirmClass(0), Typed)
	}
	all := Conditions{Yes: true, Terminal: true}
	if got, want := CanConfirm(ConfirmClass(0), all), CanConfirm(Typed, all); got != want || got {
		t.Errorf("CanConfirm(zero class, --yes at a terminal) = %v, want %v (the Typed rule)", got, want)
	}
	if !CanConfirm(YesOrTyped, all) {
		t.Error("YesOrTyped no longer takes --yes at a terminal")
	}
	if YesOrTyped == Typed {
		t.Error("the two classes are no longer distinct")
	}
}

func TestCanConfirm(t *testing.T) {
	type c = Conditions
	term := c{Terminal: true}
	for _, tc := range []struct {
		name  string
		class ConfirmClass
		cond  Conditions
		want  bool
	}{
		{"typed at a terminal", Typed, term, true},
		{"typed, --yes", Typed, c{Terminal: true, Yes: true}, false},
		{"typed, --non-interactive", Typed, c{Terminal: true, NonInteractive: true}, false},
		{"typed, --json", Typed, c{Terminal: true, JSON: true}, false},
		{"typed, no terminal", Typed, c{}, false},
		{"typed, agent", Typed, c{Terminal: true, Agent: "CLAUDECODE"}, false},
		{"yes covers", YesOrTyped, c{Yes: true}, true},
		{"yes is not enough for an agent", YesOrTyped, c{Yes: true, Terminal: true, Agent: "AI_AGENT"}, false},
		{"typed route for the covered class", YesOrTyped, term, true},
		{"covered class, nothing", YesOrTyped, c{}, false},
	} {
		if got := CanConfirm(tc.class, tc.cond); got != tc.want {
			t.Errorf("%s: CanConfirm = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The refusal for a coding agent's session is text an agent reads: it carries
// no recipe for getting past the check, except the IDE extension's marker, for
// the person whose own terminal it is.
func TestAgentRefusalHint(t *testing.T) {
	for _, m := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT", IDEMarker} {
		msg := AgentRefusal(m)
		if !strings.Contains(msg, m+" is set") || strings.Contains(msg, "--yes") {
			t.Errorf("%s: %q", m, msg)
		}
		if got := strings.Contains(msg, "unset"); got != (m == IDEMarker) {
			t.Errorf("%s: the unset hint is for %s only: %q", m, IDEMarker, msg)
		}
	}
	if msg := AgentRefusal(IDEMarker); !strings.Contains(msg, "if this is your own IDE terminal") {
		t.Errorf("not worded for the person: %q", msg)
	}
	if msg := (&NoTerminalError{Stage: "x", Agent: "CLAUDECODE"}).Error(); strings.Contains(msg, "unset") {
		t.Errorf("%q", msg)
	}
}

// A plan-only run never hands --yes to a stage's plan: it must not create
// what the plan needs (the state bucket, an API) on the flag's word.
func TestPlanOnlyNeverPassesYes(t *testing.T) {
	var log []string
	f := &fake{name: Installation, log: &log}
	if _, err := Run(context.Background(), []Stage{f}, Options{Yes: true, PlanOnly: true, Terminal: true, Out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if f.planEnv == nil || f.planEnv.Yes || count(log, "apply") != 0 {
		t.Fatalf("plan env %+v, log %v", f.planEnv, log)
	}
}

// The run's one confirmation covers the ordinary stages and nothing that is
// money, permanent, a secret or someone else's repository.
func TestRunCovers(t *testing.T) {
	want := map[string]bool{Installation: true, Firebase: true, Images: true, Installation2: true, Repository: true, Rules: true}
	for _, name := range Names() {
		if got := RunCovers(name); got != want[name] {
			t.Errorf("RunCovers(%s) = %v, want %v", name, got, want[name])
		}
		if RunCovers(name) && !YesCovers(name) {
			t.Errorf("%s: the run confirmation covers a stage --yes does not", name)
		}
	}
	for _, never := range []string{Project, Secrets, Plugin, Preflight, "unknown"} {
		if RunCovers(never) {
			t.Errorf("RunCovers(%s) is true", never)
		}
	}
}

// A stage's own fix text is as untrusted as its error: the process may hold a
// value the stage does not know it holds, and the fix is printed in the human
// account ("stopped at <stage>; fix: …") and carried in --json's failed.fix,
// so a secret in it reaches a terminal and a CI log unless fail() redacts it.
func TestStageFixTextIsRedacted(t *testing.T) {
	const secret = "s3cr3t-in-a-fix"
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight)
	stages[0].(*fake).applyErr = &StageError{
		Err: errors.New("the state bucket is not there"),
		Fix: "run gcloud auth with " + secret + " unset, then rerun",
	}
	res, err := Run(context.Background(), stages, Options{Yes: true, Terminal: true, Out: &out, Redact: []string{secret}})
	if err != nil {
		t.Fatal(err)
	}
	js, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed == nil {
		t.Fatalf("the stage did not fail: %+v", res)
	}
	if strings.Contains(out.String(), secret) || strings.Contains(string(js), secret) || strings.Contains(res.Failed.Fix, secret) {
		t.Errorf("a held value reached the output through a stage's fix:\n%s\n%s", out.String(), js)
	}
	if want := "fix: run gcloud auth with [redacted] unset, then rerun"; !strings.Contains(out.String(), want) {
		t.Errorf("%q is not in the account:\n%s", want, out.String())
	}
}

// --yes covers the stages the canonical order declares and nothing else. A
// name no slot declares — a typo, a stage a later task registers before its
// slot lands, a name from another version of this package — is refused, so the
// mistake falls in the direction that asks rather than the one that confirms.
func TestYesCoversOnlyDeclaredStages(t *testing.T) {
	for _, name := range []string{"", "unknown", "install", "installation ", "installation2x", "Installation"} {
		if YesCovers(name) {
			t.Errorf("YesCovers(%q) is true: --yes may not reach a name the order does not declare", name)
		}
		if RunCovers(name) {
			t.Errorf("RunCovers(%q) is true", name)
		}
	}
	for _, name := range []string{Preflight, Installation, Services, Repository} {
		if !YesCovers(name) {
			t.Errorf("YesCovers(%s) is false: the order declares it and it is not noYes", name)
		}
	}
}

// Over every mode: the one confirmation is taken by a person at a real
// terminal and by nothing else, exactly like a Typed step.
func TestCanConfirmRunMatrix(t *testing.T) {
	for _, m := range modes() {
		t.Run(m.name, func(t *testing.T) {
			c := Conditions{Yes: m.opts.Yes && !m.opts.PlanOnly, NonInteractive: m.opts.NonInteractive, JSON: m.opts.JSON, Terminal: m.opts.Terminal, Agent: m.opts.Agent}
			want := c.Terminal && !c.Yes && !c.NonInteractive && !c.JSON && c.Agent == ""
			if got := CanConfirmRun(c); got != want {
				t.Errorf("CanConfirmRun(%+v) = %v, want %v", c, got, want)
			}
			if CanConfirmRun(c) != CanConfirm(Typed, c) {
				t.Errorf("the run confirmation is not the Typed rule")
			}
		})
	}
}

func TestOnPreviewSeesTheStagesOfAnApplyingRunOnly(t *testing.T) {
	for _, planOnly := range []bool{false, true} {
		var log []string
		var seen []StageResult
		f := &selfStage{fake: &fake{name: Installation, log: &log}}
		_, err := Run(context.Background(), []Stage{f}, Options{Yes: true, PlanOnly: planOnly, Terminal: true, Out: &bytes.Buffer{},
			OnPreview: func(rs []StageResult) { seen = rs }})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(seen) == 1 && seen[0].Name == Installation && seen[0].State == Todo; got == planOnly {
			t.Errorf("plan-only %v: preview seen %+v", planOnly, seen)
		}
	}
}

// TestBillingPauseLetsLocalStagesFinish: a stage that pauses for billing
// (BillingPausedError) is not a failure and does not stop the run: it is
// recorded as billing-pending with its Left, every later stage that needs
// billing is waiting-for-billing (never applied, never an error), and a stage
// that needs none (the plugin wiring, a local file) still applies. The result
// is a step left for you (exit 1, the left-for-you convention), and --json
// carries the two stable statuses.
func TestBillingPauseLetsLocalStagesFinish(t *testing.T) {
	var log []string
	st := fakes(&log, Preflight, Project, Installation, Images, Secrets, Plugin, Repository)
	pause := Left{Kind: LeftCommand, Text: "billing is not linked yet", Commands: []string{"gcloud billing projects link p --billing-account=<billing-account-id>"}}
	st[1].(*fake).applyErr = &BillingPausedError{Left: pause, Detail: "created; billing pending"}
	st[0].(*fake).status = Status{State: Done}
	res, err := Run(context.Background(), st, yesOpts())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]State{}
	for _, s := range res.Stages {
		got[s.Name] = s.State
	}
	want := map[string]State{Preflight: Done, Project: BillingPending, Installation: WaitingForBilling, Images: WaitingForBilling,
		Secrets: WaitingForBilling, Plugin: Changed, Repository: WaitingForBilling}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: %q, want %q (all: %+v)", name, got[name], w, res.Stages)
		}
	}
	for _, name := range []string{Installation, Images, Secrets, Repository} {
		if slices.Contains(log, "apply "+name) {
			t.Errorf("%s was applied while billing is pending", name)
		}
	}
	if res.Failed != nil || res.ExitCode() != 1 || len(res.Left) != 1 || res.Left[0].Stage != Project || len(res.Left[0].Commands) != 1 {
		t.Errorf("failed %+v, exit %d, left %+v", res.Failed, res.ExitCode(), res.Left)
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"billing-pending"`, `"waiting-for-billing"`} {
		if !strings.Contains(string(b), s) {
			t.Errorf("--json lacks the status %s:\n%s", s, b)
		}
	}
}

// convergeStages is a run with one stage already done, one to apply, one the
// user still owes and one that waits for it: the four states a default view
// has to tell apart, in the canonical order the loop runs them in.
func convergeStages(log *[]string) []Stage {
	st := fakes(log, Preflight, Installation, Secrets, Repository)
	st[0].(*fake).status = Status{State: Done}
	st[2].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Secrets, Kind: LeftPrompt, Text: "run fugaro update in your terminal"}}
	return st
}

func TestLoopDefaultViewIsOneLinePerStage(t *testing.T) {
	// The run's account is one line per stage and one closing line: the
	// "[state] stage" words and the "result" header are the plan view's and
	// go with it, and nothing but the reporter's own lines carries a result.
	var log []string
	var out bytes.Buffer
	opts := yesOpts()
	opts.Out = &out
	opts.Report = reporterTo(&out, report.Options{})
	if _, err := Run(context.Background(), convergeStages(&log), opts); err != nil {
		t.Fatal(err)
	}
	m := reporterMarks(t)
	got := m.steps(out.String())
	if len(got) != 4 {
		t.Fatalf("the account is %d step lines, want one per stage:\n%s", len(got), out.String())
	}
	for i, name := range []string{Preflight, Installation, Secrets, Repository} {
		if !strings.Contains(got[i], name) {
			t.Errorf("step %d is %q, want the stage %s:\n%s", i, got[i], name, out.String())
		}
	}
	if !strings.HasPrefix(got[0], m.done) || !strings.HasPrefix(got[1], m.done) {
		t.Errorf("a stage that is done or changed does not carry the tick:\n%s", out.String())
	}
	if !strings.HasPrefix(got[2], m.left) {
		t.Errorf("the stage the user owes does not carry the left-for-you marker:\n%s", out.String())
	}
	if !strings.HasPrefix(got[3], m.skipped) {
		t.Errorf("a stage that never ran does not carry its own marker:\n%s", out.String())
	}
	if strings.Contains(strings.Join(got, "\n"), "[") || strings.Contains(out.String(), ": result") {
		t.Errorf("the plan view's words are in the run's account:\n%s", out.String())
	}
	lines := outputLines(out.String())
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Left for you in ") || !strings.HasSuffix(last, ", 4 steps") {
		t.Errorf("the account does not end in the run's closing line: %q\n%s", last, out.String())
	}
}

// TestAStepLineIsOnScreenBeforeTheNextStageRuns: a stage's line prints when
// that stage ends, not in one block at the end of the run. The stage that
// takes minutes — a Terraform apply, an image build — is the case that
// matters: a terminal that has said nothing since the stage before it looks
// like a hang, and a person who interrupts an apply over it leaves Terraform a
// lock and a half-written state behind, which `fugaro update` then has to
// repair.
func TestAStepLineIsOnScreenBeforeTheNextStageRuns(t *testing.T) {
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Installation)
	m := reporterMarks(t)
	stages[1].(*fake).onApply = func() {
		if line := m.line(out.String(), Preflight); line == "" {
			t.Errorf("the second stage's work started with the first stage's line not on the screen:\n%q", out.String())
		}
		if line := m.line(out.String(), Installation); line != "" {
			t.Errorf("the second stage has a result on the screen before it ran: %q\n%q", line, out.String())
		}
	}
	stages[1].(*fake).applyErr = errors.New("the apply failed")
	opts := yesOpts()
	opts.Out = &out
	opts.Report = reporterTo(&out, report.Options{})
	if _, err := Run(context.Background(), stages, opts); err != nil {
		t.Fatal(err)
	}
	// The stage's own failure is on the screen with it: the run does not wait
	// for the end of a three-minute apply to say it stopped.
	if line := m.line(out.String(), Installation); !strings.HasPrefix(line, m.failed+" "+Installation) {
		t.Errorf("the failing stage's line is %q, want the failure marker:\n%s", line, out.String())
	}
}

func TestLoopLeftForYouIsNotATick(t *testing.T) {
	// A stage left for the user is never a green tick (G3), and what to run
	// stays on the screen under it.
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Secrets)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).status = Status{State: NeedsYou, Left: &Left{Stage: Secrets, Kind: LeftPrompt,
		Text: "run fugaro update in your terminal", Commands: []string{"fugaro secrets set github-app-key"}}}
	opts := yesOpts()
	opts.Out = &out
	opts.Report = reporterTo(&out, report.Options{})
	res, err := Run(context.Background(), stages, opts)
	if err != nil {
		t.Fatal(err)
	}
	m := reporterMarks(t)
	line := m.line(out.String(), Secrets)
	if !strings.HasPrefix(line, m.left+" "+Secrets) {
		t.Errorf("the stage the user owes is %q, want the reporter's left-for-you marker:\n%s", line, out.String())
	}
	if strings.HasPrefix(line, m.done) {
		t.Errorf("a stage left for the user printed the tick:\n%s", out.String())
	}
	for _, want := range []string{
		"left for you (secrets, prompt): run fugaro update in your terminal",
		"    fugaro secrets set github-app-key",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the left-for-you block lost %q:\n%s", want, out.String())
		}
	}
	if o := opts.Report.Summary(); o.State != report.Left {
		t.Errorf("the run reported %v, want Left: a step left for the user is exit 1, never a done", o.State)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit %d, want 1", res.ExitCode())
	}
}

func TestLoopFailureShowsContextAndFix(t *testing.T) {
	// A failure shows the stage, the error, the lines of context under it and
	// the fix — on the screen, because a coding agent's session often cannot
	// read the log (G8) — and the closing line says the run failed.
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[1].(*fake).applyErr = &StageError{
		Err: errors.New("\nterraform apply exited 1: Error: quota exceeded\n  on main.tf line 3\n  with google_project_service.this"),
		Fix: "raise the quota, then rerun fugaro update",
	}
	opts := yesOpts()
	opts.Out = &out
	opts.Report = reporterTo(&out, report.Options{})
	res, err := Run(context.Background(), stages, opts)
	if err != nil {
		t.Fatal(err)
	}
	m := reporterMarks(t)
	got := out.String()
	line := m.line(got, Installation)
	if !strings.HasPrefix(line, m.failed+" "+Installation+": ") {
		t.Errorf("the failed stage is %q, want the reporter's cross and its error:\n%s", line, got)
	}
	for _, want := range []string{
		"terraform apply exited 1: Error: quota exceeded",
		"on main.tf line 3",
		"with google_project_service.this",
		"stopped at " + Installation + "; fix: raise the quota, then rerun fugaro update",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the failure block lost %q:\n%s", want, got)
		}
	}
	if lines := outputLines(got); !strings.HasPrefix(lines[len(lines)-1], "Failed in ") {
		t.Errorf("the account does not end in a failed run's closing line:\n%s", got)
	}
	// The error starts with a blank line, which oneLine trims: the message is
	// the first line that is not blank, and it prints once, not twice.
	if n := strings.Count(got, "quota exceeded"); n != 1 {
		t.Errorf("the message printed %d times, want once:\n%s", n, got)
	}
	if res.Failed == nil || strings.Contains(res.Failed.Error, "\n") {
		t.Errorf("the result's error is not one line: %+v", res.Failed)
	}
}

func TestLoopFailureContextIsRedacted(t *testing.T) {
	// The failure block prints the error's lines under the message (G8), so
	// they go through the same redaction every other printed line does: a
	// value the process holds is not on the screen because the stage put it
	// in a line of context, and --json never carried it.
	const secret = "s3cr3t-in-context"
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Installation)
	stages[1].(*fake).applyErr = errors.New("apply failed\nDetails: token=" + secret + " rejected\nretry later")
	opts := yesOpts()
	opts.Out = &out
	opts.Redact = []string{secret}
	opts.Report = reporterTo(&out, report.Options{})
	res, err := Run(context.Background(), stages, opts)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, secret) {
		t.Errorf("a held value reached the screen through the failure context:\n%s", got)
	}
	if !strings.Contains(got, "Details: token=[redacted] rejected") || !strings.Contains(got, "retry later") {
		t.Errorf("the failure block did not print the error's context, redacted:\n%s", got)
	}
	js, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), secret) {
		t.Errorf("a held value reached --json:\n%s", js)
	}
}

func TestLoopAStageThatLeftSomethingIsNotATick(t *testing.T) {
	// A stage that applied what this session may do and left the rest for the
	// user (Outcome.Left) is not a tick: the result carries the left-for-you
	// entry and the run exits 1, so the step and the closing line say so (G3).
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Checkout)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).outLeft = &Left{Stage: Checkout, Kind: LeftCommand,
		Text: "run fugaro upgrade's cloud half in your terminal"}
	opts := yesOpts()
	opts.Out = &out
	opts.Report = reporterTo(&out, report.Options{})
	res, err := Run(context.Background(), stages, opts)
	if err != nil {
		t.Fatal(err)
	}
	m := reporterMarks(t)
	got := out.String()
	line := m.line(got, Checkout)
	if !strings.HasPrefix(line, m.left+" "+Checkout) {
		t.Errorf("the stage that left something is %q, want the left-for-you marker:\n%s", line, got)
	}
	if strings.HasPrefix(line, m.done) {
		t.Errorf("a stage with a step left for the user printed the tick:\n%s", got)
	}
	if !strings.Contains(got, "left for you (checkout, command): run fugaro upgrade's cloud half in your terminal") {
		t.Errorf("the left-for-you block is missing:\n%s", got)
	}
	if res.ExitCode() != 1 {
		t.Errorf("exit %d, want 1: the run has something only the user can do", res.ExitCode())
	}
	if o := opts.Report.Summary(); o.State != report.Left {
		t.Errorf("the run reported %v, want Left", o.State)
	}
	if lines := outputLines(got); !strings.HasPrefix(lines[len(lines)-1], "Left for you in ") {
		t.Errorf("the account does not end in a left-for-you closing line:\n%s", got)
	}
}

func TestLoopRefusalPrintsNoClosingLine(t *testing.T) {
	// A refusal is the caller's error, not the account's outcome: the stages
	// that ran print their lines, and there is no closing line, because
	// "Done in ..." under a run that refused is the one thing it must not say.
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Installation)
	stages[0].(*fake).status = Status{State: Done}
	opts := Options{Out: &out}
	opts.Report = reporterTo(&out, report.Options{})
	res, err := Run(context.Background(), stages, opts)
	var refuse *NoTerminalError
	if !errors.As(err, &refuse) {
		t.Fatalf("err is %v, want the refusal of %s", err, Installation)
	}
	got := out.String()
	m := reporterMarks(t)
	if steps := m.steps(got); len(steps) != 1 || !strings.Contains(steps[0], Preflight) {
		t.Errorf("the account is %q, want the one stage that ran:\n%s", steps, got)
	}
	for _, closing := range []string{"Done in ", "Failed in ", "Left for you in "} {
		if strings.Contains(got, closing) {
			t.Errorf("a refused run printed %q:\n%s", closing, got)
		}
	}
	if res == nil || len(res.Stages) != 1 {
		t.Errorf("result %+v, want the partial account of the stage that ran: the caller still has it to print", res)
	}
}

func TestLoopVerboseStreamsEverything(t *testing.T) {
	// --verbose is today's behaviour: the run's own output — the Terraform
	// stream the caller wires to Reporter.Stream — reaches the terminal, and
	// the account keeps every line. The default view streams nothing: it is
	// one line per step.
	for _, verbose := range []bool{false, true} {
		var log []string
		var out bytes.Buffer
		opts := yesOpts()
		opts.Out = &out
		rep := reporterTo(&out, report.Options{Verbose: verbose})
		opts.Report = rep
		if _, err := fmt.Fprint(rep.Stream(), "terraform: creating...\n"); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(context.Background(), convergeStages(&log), opts); err != nil {
			t.Fatal(err)
		}
		m := reporterMarks(t)
		if got := m.steps(out.String()); len(got) != 4 {
			t.Errorf("--verbose %v: the account is %d step lines, want one per stage:\n%s", verbose, len(got), out.String())
		}
		lines := outputLines(out.String())
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Left for you in ") {
			t.Errorf("--verbose %v: the account does not end in the closing line: %q", verbose, last)
		}
		if !strings.Contains(out.String(), "left for you (secrets, prompt): run fugaro update in your terminal") {
			t.Errorf("--verbose %v: the left-for-you block is missing:\n%s", verbose, out.String())
		}
		streamed := strings.Contains(out.String(), "terraform: creating...")
		if streamed != verbose {
			t.Errorf("--verbose %v: the run's own output streamed=%v, want %v:\n%s", verbose, streamed, verbose, out.String())
		}
	}
}

func TestLoopPlanOnlyViewUnchanged(t *testing.T) {
	// The plan view is not a run: it keeps its one line per stage and the
	// "[state] stage" words the tests and the docs quote, and it reports no
	// step, so a reporter adds no closing line to it.
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Installation, Firebase)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).planSummary = "14 to create"
	opts := yesOpts()
	opts.PlanOnly, opts.Out = true, &out
	opts.Project, opts.GCPProject, opts.Region = "aurora", "aurora-prod", "us-east5"
	opts.Report = reporterTo(&out, report.Options{})
	if _, err := Run(context.Background(), stages, opts); err != nil {
		t.Fatal(err)
	}
	want := `fugaro: project aurora (GCP aurora-prod, us-east5)
  [done]       preflight
  [will do]    installation     14 to create
  [will do]    firebase         after installation
`
	if out.String() != want {
		t.Errorf("the plan view changed:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestLoopNoReporterPrintsAsToday(t *testing.T) {
	// A nil reporter is the loop's own account, word for word: every caller
	// that has not moved onto the reporter prints exactly what it printed
	// before, closing line and all.
	var log []string
	var out bytes.Buffer
	opts := yesOpts()
	opts.Out = &out
	opts.Project, opts.GCPProject, opts.Region = "aurora", "aurora-prod", "us-east5"
	if _, err := Run(context.Background(), convergeStages(&log), opts); err != nil {
		t.Fatal(err)
	}
	want := `fugaro: project aurora (GCP aurora-prod, us-east5)
  [done]       preflight
  [will do]    installation
  [needs you]  secrets
  [will do]    repository       after installation
fugaro: result
fugaro: project aurora (GCP aurora-prod, us-east5)
  [done]       preflight
  [changed]    installation     applied
  [needs you]  secrets
  [blocked]    repository       waits for secrets
left for you (secrets, prompt): run fugaro update in your terminal
`
	if out.String() != want {
		t.Errorf("the account without a reporter changed:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestLoopBillingStagesAreNotTicks(t *testing.T) {
	// Billing pending is the user's to resolve and waiting for billing is a
	// stage that cannot run until they do: neither is a green tick, and a
	// stage that needs no billing applies as it always did.
	var log []string
	var out bytes.Buffer
	stages := fakes(&log, Preflight, Project, Installation, Images, Secrets, Plugin, Repository)
	stages[0].(*fake).status = Status{State: Done}
	stages[1].(*fake).applyErr = &BillingPausedError{
		Left:   Left{Kind: LeftCommand, Text: "billing is not linked yet", Commands: []string{"gcloud billing projects link p --billing-account=<billing-account-id>"}},
		Detail: "created; billing pending",
	}
	opts := yesOpts()
	opts.Out = &out
	opts.Report = reporterTo(&out, report.Options{})
	if _, err := Run(context.Background(), stages, opts); err != nil {
		t.Fatal(err)
	}
	m := reporterMarks(t)
	got := out.String()
	if steps := m.steps(got); len(steps) != len(stages) {
		t.Fatalf("the account is %d step lines, want one per stage:\n%s", len(steps), got)
	}
	for _, stage := range []string{Project, Installation, Images, Secrets, Repository} {
		line := m.line(got, stage)
		if !strings.HasPrefix(line, m.left+" "+stage) {
			t.Errorf("%s is %q, want the reporter's left-for-you marker:\n%s", stage, line, got)
		}
	}
	if line := m.line(got, Plugin); !strings.HasPrefix(line, m.done+" "+Plugin) {
		t.Errorf("the stage that needs no billing is %q, want the tick:\n%s", line, got)
	}
	if !strings.Contains(got, "left for you (project, command): billing is not linked yet") {
		t.Errorf("the left-for-you block is missing:\n%s", got)
	}
	if lines := outputLines(got); !strings.HasPrefix(lines[len(lines)-1], "Left for you in ") {
		t.Errorf("the account does not end in a left-for-you closing line:\n%s", got)
	}
}
