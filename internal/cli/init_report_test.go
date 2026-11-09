package cli

// init_report_test.go is CL5b: `fugaro setup` and `fugaro update` print their
// converge through the reporter — one line per stage, a closing line, the log
// path on a failure — and the run's own output, Terraform's above all, goes to
// the log beside it (D1352, D1354). What the converge owns and must keep
// printing exactly as it did is here too: the confirmations, which are prompts
// and never step lines (G1), and the account a coding agent's session sees,
// which is the same one (G11).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/report"
)

// The marks a step line may start with, in the ASCII alphabet a test's buffer
// gets: colour is off off a terminal (G4), so --ascii's fallback is not in
// play and the symbols are. The list is the reporter's own four, spelled here
// because a test that read them out of the package would pass with any of them
// wrong.
const (
	markDone    = "✔"
	markFailed  = "✖"
	markLeft    = "!"
	markSkipped = "–"
	markWaiting = "↳"
)

// convergeNoise is what the fake Terraform prints on stdout and stderr: the
// progress output a converge used to show in full, and the reason a run keeps
// a log at all.
const convergeNoise = "TERRAFORM-PROGRESS-NOISE"

// applyingRig is updateRig with something to apply: the installation's plan
// reports one change, and every Terraform call prints convergeNoise, so the
// run has both a step that applies and a stream that says so.
func applyingRig(t *testing.T) *initRig {
	t.Helper()
	r := updateRig(t)
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "create"))
	r.script["plan"] = map[string]any{"exit": 2, "stdout": convergeNoise + "\n"}
	r.script["apply"] = map[string]any{"exit": 0, "stdout": convergeNoise + "\n"}
	r.save(t)
	return r
}

// reportLines is a run's account as the screen shows it: every non-empty
// line, which for the default view is one per step plus the closing line.
func reportLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// stepName is the stage a step line names, or "" for a line that is not one.
func stepName(line string) string {
	for _, m := range []string{markDone, markFailed, markLeft, markSkipped} {
		rest, ok := strings.CutPrefix(line, m+" ")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(rest, ":")
		if i := strings.IndexByte(name, ' '); i >= 0 {
			name = name[:i]
		}
		return name
	}
	return ""
}

// stepLineOf is the one line of account that is the named stage's step.
func stepLineOf(account []string, name string) string {
	for _, l := range account {
		if stepName(l) == name {
			return l
		}
	}
	return ""
}

// The things in a run's output that are the run's own and no other run's: the
// elapsed time in the closing line, and the path under a failure or a step
// left for the user — a log is named after the second and the pid, and two
// runs of one process inside one second collide on the name, so the second
// run has no log at all (CL3's OpenLog). A test that compares two runs of one
// rig — the plain run and the same run with one flag's line — normalises
// exactly these and nothing else, because two runs never agree on them.
var (
	elapsedRE    = regexp.MustCompile(`(?m)^(Done|Left for you|Failed) in [0-9.]+s,`)
	fullOutputRE = regexp.MustCompile(`(?m)^ +full output: .*\n`)
)

// withoutRunOwn is s with those taken out.
func withoutRunOwn(s string) string {
	s = elapsedRE.ReplaceAllString(s, "$1 in <the run's own>,")
	return fullOutputRE.ReplaceAllString(s, "")
}

// logDir is where a run's log is filed (D1351).
func logDir(t *testing.T) string {
	t.Helper()
	base, err := localcfg.UserDir(os.Getenv, runtime.GOOS, localcfg.DirState)
	if err != nil {
		t.Fatalf("the run has no state directory to keep a log in: %v", err)
	}
	return filepath.Join(base, "fugaro", "logs")
}

// onlyLog reads back the one log a run of cmdName left. One run, one log: a
// second file under this command's name is a bug, and a run that left none
// kept nothing for the failure it may print a path to.
func onlyLog(t *testing.T, cmdName string) (string, string) {
	t.Helper()
	dir := logDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the run left no log directory: %v", err)
	}
	re := regexp.MustCompile("^" + regexp.QuoteMeta(cmdName) + `-\d{14}-\d+\.log$`)
	var names []string
	for _, e := range entries {
		if re.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 {
		t.Fatalf("the run left %d logs of %s, want one: %v", len(names), cmdName, names)
	}
	path := filepath.Join(dir, names[0])
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, string(b)
}

// TestUpdateDefaultOutputIsShort: the run's own account is one line per stage
// and the closing line, and the Terraform stream that used to be part of it is
// in the log instead (D1352). The plan view printed before the confirmation is
// CL5a's — the review screen, which is not a step — and everything from the
// first step line on is the account.
func TestUpdateDefaultOutputIsShort(t *testing.T) {
	applyingRig(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if strings.Contains(out+errOut, convergeNoise) {
		t.Errorf("the default view streamed Terraform's own output:\n%s%s", out, errOut)
	}
	lines := reportLines(out)
	if n := len(stepNames(out)); n < 6 {
		t.Fatalf("the run printed %d steps, want update's own list:\n%s", n, out)
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Done in ") || !strings.HasSuffix(last, " steps") {
		t.Errorf("the closing line is %q, want the reporter's summary", last)
	}
	seen := map[string]bool{}
	for _, name := range stepNames(out) {
		if seen[name] {
			t.Errorf("%s is reported twice:\n%s", name, out)
		}
		seen[name] = true
	}
	if !seen["installation"] || !seen["preflight"] {
		t.Errorf("the account does not name the stages the run ran: %v\n%s", seen, out)
	}
	// The rig has something to apply, so the installation's step is a tick
	// carrying what it did, not a skip or a plan.
	if l := stepLineOf(lines, "installation"); !strings.HasPrefix(l, markDone+" installation ") || !strings.Contains(l, "creates") {
		t.Errorf("the stage that applied does not say so: %q\n%s", l, out)
	}
	path, log := onlyLog(t, "update")
	if !strings.Contains(log, convergeNoise) {
		t.Errorf("the log does not hold Terraform's output:\n%s", log)
	}
	if strings.Contains(out, "full output: "+path) {
		t.Errorf("a run that succeeded printed a log path:\n%s", out)
	}
}

// TestUpdatePlanOnlyKeepsThePlanOnTheTerminal: --plan-only takes the path it
// took before the converge's reporter (the spec's "the paths they take today").
// The plan's own text is the one thing the command exists to show, and a
// plan-only run that succeeds names no log path the way a failure does — a
// failure names the log, and that log has to hold the plan — so the reporter's
// stream alone would leave the plan's text out of reach.
func TestUpdatePlanOnlyKeepsThePlanOnTheTerminal(t *testing.T) {
	applyingRig(t)
	out, errOut, err := executeStdin(t, "", "update", "--plan-only")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "--plan-only: nothing applied") {
		t.Errorf("the run did not stop on its plan-only line:\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, convergeNoise) {
		t.Errorf("--plan-only swallowed the run's own output:\n%s%s", out, errOut)
	}
	// The plan view is the whole of what a plan-only run accounts for — the
	// steps are the converge's, and this run never converges — and the run's
	// own stream does not leak into it.
	if strings.Contains(out, convergeNoise) {
		t.Errorf("--plan-only put the run's own output in its account:\n%s", out)
	}
	if rows := planViewRows(out); rows < 6 {
		t.Errorf("--plan-only printed %d plan-view rows, want update's own list:\n%s", rows, out)
	}
	// A failure names the log, and the log holds the output it names: the plan
	// is in both places for a run that stopped on one.
	if _, log := onlyLog(t, "update"); !strings.Contains(log, convergeNoise) {
		t.Errorf("the log does not hold the output a failure would point at:\n%s", log)
	}
}

// TestPlanOnlyKeepsTheWholeReasonInTheLog: the plan view's row is one clause on
// the screen and the whole reason in the run's log (report.Brief), and a
// plan-only run is where that matters most — it prints no step for any stage, so
// the view is the whole of its account, and a reason the screen cut has nowhere
// else to be whole. The reason a stage leaves the person is an instruction, and
// half an instruction is not one.
func TestPlanOnlyKeepsTheWholeReasonInTheLog(t *testing.T) {
	r := newProjectRig(t)
	r.existing(true)
	r.bill.AddAccount(billingAcc, true)
	out, _, _ := r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc, "--plan-only")
	_, log := onlyLog(t, "setup")
	cut := 0
	for _, l := range reportLines(out) {
		i := strings.Index(l, "…")
		if !strings.HasPrefix(l, "  ") || i < 0 {
			continue
		}
		cut++
		head := strings.TrimRight(l[:i], " ")
		whole := logRowOf(log, head)
		if whole == "" {
			t.Errorf("the log does not hold the row the screen cut:\n%s\n%s", l, log)
			continue
		}
		if len(whole) <= len(l) {
			t.Errorf("the log's row is no longer than the screen's, so what the screen cut is nowhere whole:\n%s\n%s", l, whole)
		}
	}
	if cut == 0 {
		t.Fatalf("the plan view cut nothing, so this test proves nothing:\n%s", out)
	}
}

// logRowOf is the plan-view row a screen line was cut from: the row in the run's
// log that begins where the screen's row does, before the ellipsis.
func logRowOf(log, head string) string {
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, head) {
			return l
		}
	}
	return ""
}

// noLogStateDir makes this test's state directory one OpenLog cannot keep a log
// in — a link where its own directory belongs, which is one of the refusals CL3
// names and the one no uid can write around (G5) — and returns the directory
// behind the link, so a test can check that nothing was filed there.
func noLogStateDir(t *testing.T) string {
	t.Helper()
	state, err := localcfg.UserDir(os.Getenv, runtime.GOOS, localcfg.DirState)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.RemoveAll(filepath.Join(state, "fugaro")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(state, "fugaro")); err != nil {
		t.Fatal(err)
	}
	return elsewhere
}

// TestUpdateWithoutALogKeepsItsWholeAccount: the brief account is one clause on
// the screen *because* the whole of the reason is in the log (report.Brief), so
// a run that could not open one cuts nothing: the step's reason is the whole of
// it, and the plan's own account — the addresses of what the person is about to
// confirm — reaches the terminal, where it is the only place left for it
// (D1352, G5, G8).
func TestUpdateWithoutALogKeepsItsWholeAccount(t *testing.T) {
	applyingRig(t)
	noLogStateDir(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	all := out + errOut
	if !strings.Contains(all, "create module.installation.google_storage_bucket.runs") {
		t.Errorf("the run kept Terraform's own plan out of the only account it has:\n%s", all)
	}
	if strings.Contains(all, "…") {
		t.Errorf("a run with no log cut a reason it had nowhere to keep whole:\n%s", all)
	}
	if strings.Contains(all, "full output: ") {
		t.Errorf("a run that opened no log printed a path to one:\n%s", all)
	}
}

// TestUpdateFailurePrintsContextAndLogPath: a failed step prints its name, the
// error, the lines of context behind it and the path of the log the rest is in
// — on the screen, because an agent's session with a read-only home cannot
// open the log at all (G8) — and the run still names the stage it stopped at
// and the fix.
func TestUpdateFailurePrintsContextAndLogPath(t *testing.T) {
	r := applyingRig(t)
	r.script["apply"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n  with google_project_service.quota\n"}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil {
		t.Fatalf("the apply failed, yet the run succeeded:\n%s", out)
	}
	all := out + errOut
	path, log := onlyLog(t, "update")
	for _, want := range []string{
		markFailed + " installation",
		"Error: quota exceeded",
		"with google_project_service.quota",
		"full output: " + path,
		"stopped at installation",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the failure block does not print %q:\n%s", want, all)
		}
	}
	if !strings.Contains(log, convergeNoise) {
		t.Errorf("the log does not hold what the run printed before it failed:\n%s", log)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the path the failure prints is not a file: %v", err)
	}
}

// TestUpdateWithoutALogKeepsTheStreamOnScreen: a converge whose log could not
// be opened at all — a state directory that is a link, foreign-owned or
// read-only, which is G5's "a run with no log is still a run" — has nowhere to
// keep the run's own output. It must not swallow it either: the stream goes to
// stderr, where every run wrote before CL5b, because the output behind a
// failure is the last thing a run may drop (G8).
func TestUpdateWithoutALogKeepsTheStreamOnScreen(t *testing.T) {
	applyingRig(t)
	elsewhere := noLogStateDir(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	all := out + errOut
	if !strings.Contains(all, convergeNoise) {
		t.Errorf("the run opened no log and dropped its own output with it:\n%s", all)
	}
	if !strings.Contains(all, markDone+" installation") {
		t.Errorf("the run with no log lost its account too:\n%s", all)
	}
	if strings.Contains(all, "full output: ") {
		t.Errorf("a run that opened no log printed a path to one:\n%s", all)
	}
	// The run's workdir lives under the same state directory and is none of
	// this test's business; a log behind the link is: the refusal is what
	// makes the run fall back to stderr at all.
	var logs []string
	_ = filepath.WalkDir(elsewhere, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".log") {
			logs = append(logs, p)
		}
		return nil
	})
	if len(logs) != 0 {
		t.Errorf("the run kept a log behind the link it should have refused: %v", logs)
	}
}

// TestUpdateVerboseStreamsTerraform: --verbose is the run's own output on the
// screen as well as in the log, which is what it always was (G10).
func TestUpdateVerboseStreamsTerraform(t *testing.T) {
	applyingRig(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes", "-v")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out+errOut, convergeNoise) {
		t.Errorf("--verbose did not stream the run's own output:\n%s%s", out, errOut)
	}
	_, log := onlyLog(t, "update")
	if !strings.Contains(log, convergeNoise) {
		t.Errorf("--verbose kept the output off the log:\n%s", log)
	}
}

// TestUpdateLogHoldsTheFullTerraformOutput: the log is the whole of what the
// run's children printed — plan and apply, stdout and stderr — under the
// command's name, and the screen says none of it.
func TestUpdateLogHoldsTheFullTerraformOutput(t *testing.T) {
	r := applyingRig(t)
	r.script["apply"] = map[string]any{"exit": 0, "stderr": "STDERR-NOISE\n"}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	path, log := onlyLog(t, "update")
	if !regexp.MustCompile(`^update-\d{14}-\d+\.log$`).MatchString(filepath.Base(path)) {
		t.Errorf("the log is named %q, want the update door's own name (D1352)", filepath.Base(path))
	}
	for _, want := range []string{convergeNoise, "STDERR-NOISE"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not hold %q:\n%s", want, log)
		}
		if strings.Contains(out+errOut, want) {
			t.Errorf("the screen shows what the log is for: %q", want)
		}
	}
	if len(r.ran(t, "apply")) == 0 {
		t.Errorf("the run applied nothing, so the log proves nothing: %q", r.calls(t))
	}
}

// TestUpdateLogRedactsHeldSecretsInTheTerraformStream: the log is redacted
// with the values the converge holds, read at the moment each line is written.
// Terraform's stream is raw today (F6) — only the loop's stage details and
// errors pass a redactor — so this is the wiring that closes it: a value the
// process held from the start, and the slot a stage fills mid-run, both reach
// the sink.
//
// A value split across two writes is the redactor's own contract, pinned by
// CL3's TestRedactsASecretSplitAcrossWrites: what this test can pin is that
// the sink the converge opens is built on that redactor and on the live slots.
func TestUpdateLogRedactsHeldSecretsInTheTerraformStream(t *testing.T) {
	const held = "s3cret"
	old := secretsHeld
	secretsHeld = func() []string { return []string{held} }
	t.Cleanup(func() { secretsHeld = old })
	r := applyingRig(t)
	r.script["apply"] = map[string]any{"exit": 0, "stdout": "applying with " + held + " in the line\n"}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	_, log := onlyLog(t, "update")
	if strings.Contains(log, held) {
		t.Errorf("a held value reached the log:\n%s", log)
	}
	if !strings.Contains(log, report.Redacted) {
		t.Errorf("the log does not hold the redaction marker:\n%s", log)
	}
	if strings.Contains(out+errOut, held) {
		t.Errorf("a held value reached the screen: %s%s", out, errOut)
	}
}

// TestUpdateLogRedactsAValueAStageHoldsMidRun: the sink is redacted with the
// converge's own slots, read at each line, and not with a copy of them taken
// when the log opened. The one case where the two differ is a value a stage
// takes mid-run — initEngine.hold, which the secrets stage calls while it
// stores one (initsecrets.go) — so it goes in the log as the marker and never
// as itself, and a value the stage takes after an earlier one was released is
// covered just the same, since the slots are read as they are at that line.
//
// The value is printed through the stage's own stream, which is what TF.Out is
// (initRun.progress): a Terraform that echoes a value it was handed writes
// exactly there.
func TestUpdateLogRedactsAValueAStageHoldsMidRun(t *testing.T) {
	const (
		first  = "held-through-the-apply"
		second = "held-through-the-next-apply"
	)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	run := newInitRun(cmd, &initOptions{})
	e := &initEngine{r: run}
	run.rep, run.repLog = reporterFor(cmd, "update", e.redaction())
	if run.repLog == nil {
		t.Fatalf("the run opened no log, so this test has none to read")
	}
	t.Cleanup(func() { _ = run.repLog.Close() })
	// hold is the stage: it takes one value, prints it while holding it, and
	// gives the slot back on its way out, exactly as the secrets stage does
	// around its store.
	hold := func(v string) error {
		release, err := e.hold([]byte(v))
		if err != nil {
			return err
		}
		defer release()
		_, err = fmt.Fprintln(run.progress(), "terraform prints "+v+" in its line")
		return err
	}
	st := &engineStage{e: e, name: initflow.Installation, skip: func() string { return "" },
		run: func(context.Context) error {
			if err := hold(first); err != nil {
				return err
			}
			return hold(second)
		}}
	if _, err := initflow.Run(context.Background(), []initflow.Stage{st}, initflow.Options{
		Yes: true, Out: io.Discard, Report: run.rep,
	}); err != nil {
		t.Fatalf("the converge: %v\n%s", err, out.String())
	}
	_, log := onlyLog(t, "update")
	for _, v := range []string{first, second} {
		if strings.Contains(log, v) {
			t.Errorf("a value the stage held reached the log: %q\n%s", v, log)
		}
	}
	if n := strings.Count(log, report.Redacted); n != 2 {
		t.Errorf("the log holds %d redaction markers, want one per held value:\n%s", n, log)
	}
}

// TestUpdateJSONUnchanged: --json is a contract (F8). stdout is one document
// and nothing else — no step line, no ANSI, no carriage return — and the
// human account goes to stderr, where the converge's own writer already went.
func TestUpdateJSONUnchanged(t *testing.T) {
	applyingRig(t)
	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	var res convergeJSON
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatalf("stdout is not one document: %v\n%q", jerr, out)
	}
	if strings.ContainsAny(out, "\x1b\r") || strings.Contains(out, markDone) {
		t.Errorf("a human line reached stdout under --json:\n%q", out)
	}
	if len(res.Stages) == 0 || res.state("installation") == "" {
		t.Errorf("the document does not carry the run's stages: %+v", res.Stages)
	}
	if !strings.Contains(errOut, markDone+" installation") {
		t.Errorf("the account is not on stderr under --json:\n%s", errOut)
	}
	if _, log := onlyLog(t, "update"); !strings.Contains(log, convergeNoise) {
		t.Errorf("a --json run of a mutating command kept no log of its output")
	}
}

// writerAsTerminal makes the reporter treat a test's buffer as a terminal, so
// the rules that draw a line in flight are in play (G4's opposite half).
func writerAsTerminal(t *testing.T) {
	t.Helper()
	old := writerIsTerminal
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { writerIsTerminal = old })
}

// promptWatch is a run's stdout, watched as it is written: it keeps the bytes
// that were on the screen at the moment a prompt was printed.
type promptWatch struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	at     string
	before string
	found  bool
}

func (w *promptWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if !w.found && strings.Contains(w.buf.String(), w.at) {
		w.found = true
		w.before, _, _ = strings.Cut(w.buf.String(), w.at)
	}
	return n, err
}

func (w *promptWatch) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestConfirmPromptsAreNeverSuppressed: the typed confirmation of an apply is
// a prompt, not a step: it is on the screen whether or not the reporter is
// drawing, and nothing is drawn on the line the run is about to read stdin
// on — a line in flight would swallow it (G1).
func TestConfirmPromptsAreNeverSuppressed(t *testing.T) {
	applyingRig(t)
	fakeTerminal(t)
	writerAsTerminal(t)
	// The reporter draws a line in flight only where colour is allowed, and a
	// `TERM=dumb` console is not (G4): without this the "nothing in flight"
	// half of this test would pass on a machine whose TERM happens to be dumb,
	// which is what a build box's is.
	t.Setenv("TERM", "xterm-256color")
	watch := &promptWatch{at: "Type " + initProjectName + " to apply to GCP project " + initProject + ": "}
	cmd := NewRootCmd()
	var errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(initProjectName + "\n"))
	cmd.SetOut(watch)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeArgs([]string{"update"}))
	err := cmd.Execute()
	out := watch.String()
	if !watch.found {
		t.Fatalf("the typed confirmation never reached the screen (err %v):\n%s%s", err, out, errOut.String())
	}
	if !strings.HasSuffix(watch.before, "\n") {
		t.Errorf("a line was in flight when the run read stdin, so the prompt is drawn over it: %q", watch.before)
	}
	if err != nil {
		t.Fatalf("the typed name did not confirm the apply: %v\n%s%s", err, out, errOut.String())
	}
	if !strings.Contains(out, "installation") {
		t.Errorf("the run printed no account after the confirmation:\n%s", out)
	}
	// The account is the reporter's: the confirmation above was the only
	// reason this run printed at all before it.
	if !strings.Contains(out, markDone) {
		t.Errorf("the run's steps are not the reporter's:\n%s", out)
	}
}

// TestTheRunsOwnWriterTakesTheLineInFlightDown: the converge's own writer is
// where a person's lines go, and the reporter's line in flight is down before
// the first byte of one — a frame drawn on the line a prompt is written on
// swallows the prompt and the answer typed under it (G1). The answer puts the
// line back: a stage that prompts in the middle of its minutes is still a
// stage somebody is watching, and one is open across every stage (the loop).
func TestTheRunsOwnWriterTakesTheLineInFlightDown(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader("aurora\n"))
	run := newInitRun(cmd, &initOptions{})
	run.rep = report.New(report.Options{Out: &out, Getenv: os.Getenv,
		IsTerminal: func(io.Writer) bool { return true }})
	step := run.rep.Step("installation")
	if _, err := fmt.Fprint(run.w, "Type aurora: "); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasSuffix(got, "\r\x1b[KType aurora: ") {
		t.Errorf("the run's own line was written over the line in flight: %q", got)
	}
	if _, err := run.in.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasSuffix(got, "installation") {
		t.Errorf("the answer did not put the line in flight back: %q", got)
	}
	step.Done("")
}

// TestTheLineStaysDownThroughAWholeQuestion: what a question prints through the
// run's own writer while it is open — the policy stage's diff and the sentences
// above its prompt — is part of the question, so the step's line may not come
// back between its lines: the block and the prompt under it are read as one
// (initRun.asking, G1). The step's line is drawn again once the answer is in.
func TestTheLineStaysDownThroughAWholeQuestion(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	fakeTerminal(t)
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader(initProjectName + "\n"))
	run := newInitRun(cmd, &initOptions{})
	run.rep = report.New(report.Options{Out: &out, Getenv: os.Getenv,
		IsTerminal: func(io.Writer) bool { return true }})
	step := run.rep.Step("policy")

	lc := &localcfg.Config{Name: initProjectName}
	if err := run.asking(func() error {
		fmt.Fprint(run.w, "the policy node this run writes:\n  allow_repos: [aurora]\n")
		return confirmRemoval(cmd, lc, false, policyReseedWhat)
	}); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	at := strings.Index(got, "\r\x1b[Kthe policy node this run writes:")
	if at < 0 {
		t.Fatalf("the question's block was not written on a line of its own:\n%q", got)
	}
	want := "\r\x1b[Kthe policy node this run writes:\n  allow_repos: [aurora]\n" +
		"⚠ CONFIRM (project " + initProjectName + "): " + policyReseedWhat + "\n" +
		"Type " + initProjectName + " to confirm: "
	if !strings.HasPrefix(got[at:], want) {
		t.Errorf("the question is not one block with its prompt under it:\n got %q\nwant %q", got[at:], want)
	}
	if !strings.HasSuffix(got, "policy") {
		t.Errorf("the answer did not put the step's line back: %q", got)
	}
	step.Done("")
}

// TestAMidStageConfirmationIsNeverDrawnOver is the round-3 finding, on the
// layer stage's re-seed: the confirmation a stage asks is the shared
// confirmRemoval, which holds the command rather than the run. Asked with a step
// of the run's own open (a step is open across every stage), it landed on the
// line the spinner had drawn — the frame and then the banner, with no clear
// between them — and the redraw every 100ms kept painting the frame over the
// prompt and over the name typed under it (G1).
func TestAMidStageConfirmationIsNeverDrawnOver(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	clearAgentMarkers(t)
	fakeTerminal(t)
	writerAsTerminal(t)
	r := newLayerStageRig(t, testProjectLayer, localEdit, "3")
	run := r.s.e.r
	run.rep = report.New(report.Options{Out: r.out, Getenv: os.Getenv, IsTerminal: writerIsTerminal})
	// The loop opens the stage's step as it enters it (initflow's converge),
	// which is what makes this a prompt asked mid-stage and not one before the
	// first step — the shape TestConfirmPromptsAreNeverSuppressed covers.
	step := run.rep.Step(initflow.Layer)
	answerAQuestionMidStage(t, run, r.out, "⚠ CONFIRM (project "+initProjectName+"): ", func() error {
		_, err := r.s.Apply(context.Background(), initflow.Env{Interactive: true})
		return err
	})
	step.Done("")
}

// TestThePolicyStagesConfirmationIsNeverDrawnOver is the finding's own repro:
// the policy migration prints its diff through the run's own writer and asks
// confirmRemoval from inside the step the loop opened for the stage, so the
// banner's line is the one the frame was drawn on.
func TestThePolicyStagesConfirmationIsNeverDrawnOver(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	clearAgentMarkers(t)
	fakeTerminal(t)
	writerAsTerminal(t)
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), v3Node(nil))
	h := newMigrationRig(t, f, &initOptions{})
	run := h.s.e.r
	run.rep = report.New(report.Options{Out: h.out, Getenv: os.Getenv, IsTerminal: writerIsTerminal})
	step := run.rep.Step(initflow.Policy)
	answerAQuestionMidStage(t, run, h.out, "⚠ CONFIRM (project "+initProjectName+"): ", func() error {
		_, err := h.s.Apply(context.Background(), initflow.Env{Interactive: true})
		return err
	})
	step.Done("")
}

// TestTheWorkspaceStagesConfirmationIsNeverDrawnOver is the same finding on the
// update door's second stage that asks a confirmation of its own: the workspace
// publish prints its diff and asks the shared confirmation (confirmRemoval,
// confirmTypedStrict), and asked through the command's own writer with no
// question in flight it landed on the line the spinner had drawn, with the
// redraw painting the frame over the CONFIRM block and over the project's name
// typed under it (G1). The write this test confirms is a removal — the published
// node lists a member the record no longer declares — so the question is the
// ordinary y/N one and the person types the project's name at it.
func TestTheWorkspaceStagesConfirmationIsNeverDrawnOver(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	fakeTerminal(t)
	writerAsTerminal(t)
	// The workspace's own project is the one the shared confirmation helper
	// types the name of (initProjectName), so this home is the workspace-alpha
	// one's installation with the owner's table under that name: the record this
	// stage publishes is named after the project that declares it.
	h := newWSUpdateHome(t, "")
	h.add(t, initProjectName, "aurora-gcp", wsOwnerTable)
	h.selectProject(t, initProjectName)
	wsPublishedNode(t, h.project(t, initProjectName), initProjectName,
		[]any{wsPublishedMember("member-b", "owner/repo-a", "owner/repo-b"), wsPublishedMember("member-c", "owner/repo-c")}, 0)
	r := newWSUpdateRig(t, h)
	run := r.s.e.r
	run.rep = report.New(report.Options{Out: r.out, Getenv: os.Getenv, IsTerminal: writerIsTerminal})
	step := run.rep.Step(initflow.Workspace)
	answerAQuestionMidStage(t, run, r.out, "⚠ CONFIRM (project "+initProjectName+"): ", func() error {
		_, err := r.s.Apply(context.Background(), initflow.Env{Interactive: true})
		return err
	})
	step.Done("")
}

// answerAQuestionMidStage drives ask — the stage's call, which asks one question
// of the person — from another goroutine and asserts what the screen shows. The
// answer is on a pipe, so the test decides when the person has read the block
// and typed their name, and the question is the typed one these stages ask
// (confirmTyped's, which confirmRemoval and confirmTypedStrict are). head is the
// question's own banner as that stage writes it: the converge's stages ask with
// the project's name alone, and the checkout stage's cloud step asks with the
// one a run of its own writes (askClass, which names the GCP project too).
func answerAQuestionMidStage(t *testing.T, run *initRun, out *syncBuf, head string, ask func() error) {
	t.Helper()
	answer, typed := io.Pipe()
	defer typed.Close()
	// The answer is wired in where a question may be read, both of them: the
	// command's own stdin (the shared confirmation helpers read it there) and
	// the run's reader, which production builds over that same stdin
	// (newInitRun) and which a run this one drives is given (refreshOptions.in).
	run.cmd.SetIn(answer)
	run.in = bufio.NewReader(answerReader{r: run, in: answer})
	done := make(chan error, 1)
	go func() { done <- ask() }()

	prompt := "Type " + initProjectName + " to confirm: "
	waitForOutput(t, out, prompt)
	shown := out.String()
	at := strings.Index(shown, head)
	if at < 0 {
		t.Fatalf("the stage asked no confirmation, so this test proves nothing:\n%q", shown)
	}
	if before := shown[:at]; !strings.HasSuffix(before, "\r\x1b[K") && !strings.HasSuffix(before, "\n") {
		t.Errorf("the spinner drew the confirmation over its own line: %q", before)
	}
	rest := shown[at:]
	nl := strings.Index(rest, "\n")
	if nl < 0 {
		t.Fatalf("the confirmation's banner never ended, so the prompt under it cannot be read as its own line:\n%q", rest)
	}
	// The banner's line ends, and the run's writer puts the step in flight back
	// the moment it does (a complete line is a line somebody watches), so one
	// frame and the clear that takes it down may stand between the banner and
	// the prompt — erased before the prompt is written. A line of anything else
	// there is what this refuses: the question is read as one block.
	between := rest[nl+1:]
	if i := strings.LastIndex(between, "\r\x1b[K"); i >= 0 && !strings.Contains(between[:i], "\n") {
		between = between[i+len("\r\x1b[K"):]
	}
	if !strings.HasPrefix(between, prompt) {
		t.Errorf("the confirmation is not the prompt's own line, with nothing drawn between them:\n%q", rest)
	}

	// A redraw every 100ms is what the finding measured, and three intervals of
	// quiet are what says the spinner is down. The prompt is on the screen
	// already, so anything written here is a frame over the answer being typed.
	quiet := len(shown)
	time.Sleep(spinWatch)
	if after := out.String(); len(after) != quiet {
		t.Errorf("the spinner redrew over the prompt being answered: %q", after[quiet:])
	}

	if _, err := fmt.Fprintln(typed, initProjectName); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the stage: %v\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the stage is still asking after the answer:\n%q", out.String())
	}
	// The answer puts the stage's line back: what is on the screen after the
	// question is a line in flight again, not the stage's last line left
	// standing — which is what a run that stopped drawing for the rest of the
	// stage's minutes would show.
	got := out.String()
	if strings.HasSuffix(got, "\n") {
		t.Errorf("the stage's line did not come back after the answer: %q", tail(got, 80))
	}
}

// tail is the last n bytes of s, or all of it.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// spinWatch is three of the reporter's redraw intervals (spinInterval is
// 100ms): the length of quiet that says its spinner is down, because a spinner
// that is up cannot stay silent for it.
const spinWatch = 300 * time.Millisecond

// waitForOutput blocks until a run driven from another goroutine has written
// at, or fails the test: the screen this test has to be looking at.
func waitForOutput(t *testing.T, out *syncBuf, at string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(out.String(), at) {
		if time.Now().After(deadline) {
			t.Fatalf("%q was never written:\n%q", at, out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// stepNames is the stages a run's steps name, in the order the account gives
// them.
func stepNames(out string) []string {
	var names []string
	for _, l := range reportLines(out) {
		if n := stepName(l); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// planViewRows counts the plan view's own rows: the indented "mark status
// stage" lines a plan-only run ends on, which are not steps (nothing was
// opened for them) and so are not counted by stepNames.
func planViewRows(out string) int {
	n := 0
	for _, l := range reportLines(out) {
		if strings.HasPrefix(l, "  "+markDone) || strings.HasPrefix(l, "  "+markSkipped) ||
			strings.HasPrefix(l, "  "+markWaiting) || strings.HasPrefix(l, "  "+markFailed) ||
			strings.HasPrefix(l, "  "+markLeft) {
			n++
		}
	}
	return n
}

// namedLog reads back the log a run's own output points at — the path under a
// failure or a step left for the user — for a test that has to read the whole
// of a reason the step's line carries one clause of (report.Brief).
func namedLog(t *testing.T, out string) string {
	t.Helper()
	m := fullOutputRE.FindString(out)
	if m == "" {
		t.Fatalf("the run names no log, so a reason it shortened is out of reach:\n%s", out)
	}
	b, err := os.ReadFile(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m), "full output:")))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newestLog reads back the newest log the runs of cmdName in this test have
// left so far — the run that has just finished. onlyLog refuses a test that
// runs the command twice, and a run whose reason the screen shortened keeps the
// whole of it in its log (report.Brief).
func newestLog(t *testing.T, cmdName string) string {
	t.Helper()
	dir := logDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the run left no log directory: %v", err)
	}
	re := regexp.MustCompile("^" + regexp.QuoteMeta(cmdName) + `-\d{14}-\d+\.log$`)
	newest := ""
	for _, e := range entries {
		if re.MatchString(e.Name()) && e.Name() > newest {
			newest = e.Name()
		}
	}
	if newest == "" {
		t.Fatalf("no log of %s: every run keeps one", cmdName)
	}
	b, err := os.ReadFile(filepath.Join(dir, newest))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// planRowOf is the plan view's row for one stage, without the view's own
// indent: the row starts with a marker and the status word, which is what a
// test asserting one stage's state reads. It is "" when the view has none.
func planRowOf(out, stage string) string {
	for _, l := range reportLines(out) {
		row, ok := strings.CutPrefix(l, "  ")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(row) {
			if f == stage {
				return row
			}
		}
	}
	return ""
}

// TestUpdateUnderAgentMarkerPrintsTheSameSteps: a coding agent's session sees
// the same account a person's does — the same stages, as the reporter's steps
// — and the marker changes no cloud write (G11). What the marker still changes
// is what the run may do: the installation is left for the user, as it was
// before CL5b, and nothing is applied.
func TestUpdateUnderAgentMarkerPrintsTheSameSteps(t *testing.T) {
	clearAgentMarkers(t)
	person := applyingRig(t)
	outPerson, _, errPerson := executeStdin(t, "", "update", "--yes")
	if errPerson != nil {
		t.Fatalf("the person's run: %v\n%s", errPerson, outPerson)
	}
	if len(person.ran(t, "apply")) == 0 {
		t.Fatalf("the person's run applied nothing, so the refusal below proves nothing: %q", person.calls(t))
	}

	t.Setenv("CLAUDECODE", "1")
	agent := applyingRig(t)
	outAgent, errAgentOut, errAgent := executeStdin(t, "", "update", "--yes")

	// The marker changes what the run may do, not which stages it reached: the
	// stages the agent's account names are the person's own, up to the stage
	// the run stopped at. A run that stops leaves every stage behind it saying
	// the same thing, and those are one waiting line (report.Waits) rather than
	// a line each, so the agent's account names fewer of them than the
	// person's -- the ones it does name are in the same order.
	personStages := stepNames(outPerson)
	agentStages := stepNames(outAgent)
	if len(agentStages) == 0 || len(personStages) < len(agentStages) {
		t.Fatalf("the runs' accounts name %v and %v, want the agent's the person's own up to the stop:\nperson:\n%s\nagent:\n%s",
			personStages, agentStages, outPerson, outAgent)
	}
	if strings.Join(personStages[:len(agentStages)], " ") != strings.Join(agentStages, " ") {
		t.Errorf("the marker changed the account's steps:\nperson: %v\nagent: %v", personStages, agentStages)
	}
	for _, out := range []string{outPerson, outAgent} {
		lines := reportLines(out)
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Done in ") && !strings.HasPrefix(last, "Left for you in ") {
			t.Errorf("the run does not end in the reporter's closing line: %q", last)
		}
	}
	if got := ExitCode(errAgent); got != ExitUserError {
		t.Errorf("the agent's run exits %d, want %d for a step it left for the user", got, ExitUserError)
	}
	if n := len(agent.ran(t, "apply")); n != 0 {
		t.Errorf("the marker permitted %d applies, want none", n)
	}
	all := outAgent + errAgentOut
	if !strings.Contains(all, "left for you (installation") || !strings.Contains(all, markLeft+" installation: ") {
		t.Errorf("the refusal is not the one it was before the account moved:\n%s", all)
	}
	if !strings.Contains(namedLog(t, all), "CLAUDECODE is set") {
		t.Errorf("the log does not hold the whole of the reason the step's line shortened:\n%s", all)
	}
}
