package cli

// init_report_test.go is CL5b: `fugaro setup` and `fugaro update` print their
// converge through the reporter — one line per stage, a closing line, the log
// path on a failure — and the run's own output, Terraform's above all, goes to
// the log beside it (D1352, D1354). What the converge owns and must keep
// printing exactly as it did is here too: the confirmations, which are prompts
// and never step lines (G1), and the account a coding agent's session sees,
// which is the same one (G11).

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
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
	first := -1
	for i, l := range lines {
		if stepName(l) != "" {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatalf("the run printed no step at all:\n%s", out)
	}
	account := lines[first:]
	last := account[len(account)-1]
	if !strings.HasPrefix(last, "Done in ") || !strings.HasSuffix(last, " steps") {
		t.Errorf("the closing line is %q, want the reporter's summary", last)
	}
	seen := map[string]bool{}
	for _, l := range account[:len(account)-1] {
		name := stepName(l)
		if name == "" {
			t.Errorf("the account holds a line that is not a step: %q\n%s", l, out)
			continue
		}
		if seen[name] {
			t.Errorf("%s is reported twice:\n%s", name, out)
		}
		seen[name] = true
	}
	if !seen["installation"] || !seen["preflight"] {
		t.Errorf("the account does not name the stages the run ran: %v\n%s", seen, out)
	}
	if len(seen) < 6 {
		t.Errorf("the account is %d stages, want update's own list:\n%s", len(seen), out)
	}
	// The rig has something to apply, so the installation's step is a tick
	// carrying what it did, not a skip or a plan.
	if l := stepLineOf(account, "installation"); !strings.HasPrefix(l, markDone+" installation ") || !strings.Contains(l, "creates") {
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

	if strings.Join(stepNames(outPerson), " ") != strings.Join(stepNames(outAgent), " ") {
		t.Errorf("the marker changed the account's steps:\nperson:\n%s\nagent:\n%s", outPerson, outAgent)
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
	if !strings.Contains(all, "CLAUDECODE is set") || !strings.Contains(all, "left for you (installation") {
		t.Errorf("the refusal is not the one it was before the account moved:\n%s", all)
	}
}
