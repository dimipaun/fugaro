package cli

// report_writers_test.go is CL7b: the project and config writers — `config
// publish`, `allow-delete`, the managed family, `restore` and the budget's
// writers — print through the reporter. Each of them opens its own log under
// its own command name (D1352), and its account on the screen is one line per
// unit of work with the reporter's closing line last (D1354).
//
// This file is the batch's red step: on the tree before the move every test
// here fails, because those commands print free-form lines and keep no log at
// all (onlyLog finds no directory to read).

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/managed"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// closing is the reporter's last line: the three outcomes of G3, with the
// run's own elapsed time.
var closing = regexp.MustCompile(`^(Done|Failed|Left for you) in [0-9.]+s, [0-9]+ steps?$`)

// reported is the account one migrated command owes: the run ends with the
// reporter's closing line, and every unit of work it named has a step line of
// its own. The step names are single tokens, which is what the reporter's own
// readers (stepName) take them to be.
func reported(t *testing.T, out string, steps ...string) {
	t.Helper()
	lines := reportLines(out)
	if len(lines) == 0 {
		t.Fatal("the command printed nothing at all")
	}
	if last := lines[len(lines)-1]; !closing.MatchString(last) {
		t.Errorf("the run does not end with the reporter's closing line, it ends with %q:\n%s", last, out)
	}
	for _, name := range steps {
		if !stepNamed(out, name) {
			t.Errorf("no step line names %q:\n%s", name, out)
		}
	}
}

// stepNamed is whether the account carries a step line naming name. It is not
// stepLineOf: a typed confirmation prints "Type <name> to confirm: " and reads
// a line, and the newline that separates the step from it is the terminal's
// own echo — which a test's pipe does not have, so the step line lands at the
// end of the prompt's line rather than the start of its own.
//
// The character after the name is what a result line puts there: a space before
// a detail, a colon before a failure's or a left-for-you step's text, and the
// end of the line for a step with neither.
func stepNamed(out, name string) bool {
	for _, mark := range []string{markDone, markFailed, markLeft, markSkipped} {
		for _, tail := range []string{" ", ":", "\n"} {
			if strings.Contains(out, mark+" "+name+tail) {
				return true
			}
		}
	}
	return false
}

// writesALog is D1352's half of the batch: the run kept a log filed under its
// own command name, which is also what a failure's "full output:" line points
// at, and the log holds the run's own account — its step lines and the closing
// line last. A run that kept no log has no directory to read and fails here; a
// run that kept an empty one would point a person at nothing, which is what
// this asserts against rather than merely that a file exists.
func writesALog(t *testing.T, cmdName string) string {
	t.Helper()
	_, content := onlyLog(t, cmdName)
	lines := reportLines(content)
	if len(lines) == 0 {
		t.Fatalf("%s kept an empty log: the failure block would send a person to a file with nothing in it", cmdName)
	}
	if !hasAStepLine(content) {
		t.Errorf("%s's log holds no step line, so the run's own account is not in it:\n%s", cmdName, content)
	}
	if !hasAClosingLine(lines) {
		t.Errorf("%s's log holds no closing line, so the run's own account is not in it:\n%s", cmdName, content)
	}
	return content
}

// hasAClosingLine is whether any of a run's lines is the reporter's closing
// one.
func hasAClosingLine(lines []string) bool {
	for _, l := range lines {
		if closing.MatchString(l) {
			return true
		}
	}
	return false
}

// hasAStepLine is whether s carries any step's result line: one of the four
// marks, a space and a name.
func hasAStepLine(s string) bool {
	for _, mark := range []string{markDone, markFailed, markLeft, markSkipped} {
		if strings.Contains(s, mark+" ") {
			return true
		}
	}
	return false
}

// screenWatch is a run's screen, watched as it is written: it keeps what was on
// it just before the first block the run printed itself, once a step's line has
// been drawn. The line in flight is written with a leading carriage return and
// a command's own lines are not, so "the first write that is not a frame, after
// a frame" is that block — and where the frame was left underneath it is
// exactly what a person would have seen.
type screenWatch struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	frame  bool
	before string
	found  bool
}

func (w *screenWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case bytes.HasPrefix(p, []byte("\r")):
		w.frame = true
	case w.frame && !w.found:
		w.found, w.before = true, w.buf.String()
	}
	return w.buf.Write(p)
}

func (w *screenWatch) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// after is everything the run wrote from that block on.
func (w *screenWatch) after() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()[len(w.before):]
}

// notDrawnOver is G1's assertion for a block a command prints itself while a
// step is in flight: what was on the screen just before it has to be the
// sequence that takes the line in flight down — the reporter's own "\r\x1b[K" —
// or the end of a line. A frame of the spinner redraws its own line with a
// carriage return, so anything else means the block was drawn onto it.
func notDrawnOver(t *testing.T, w *screenWatch, what string) {
	t.Helper()
	if !w.found {
		t.Fatalf("the run never printed %s:\n%q", what, w.String())
	}
	if strings.HasSuffix(w.before, "\x1b[K") || strings.HasSuffix(w.before, "\n") {
		return
	}
	before := w.before
	if len(before) > 60 {
		before = before[len(before)-60:]
	}
	t.Errorf("the line in flight was left on the screen under %s: the screen just before it holds %q", what, before)
}

// ---------------------------------------------------------------- publish

func TestConfigPublishDefaultIsShort(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	out, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The layer is one step and every repository it fans out to is one of its
	// own, rather than the indented "  acme/app: copied to …" prose they were.
	reported(t, out, "layer", "acme/app")
}

func TestConfigPublishWritesALog(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	if _, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "config-publish")
}

// ----------------------------------------------------------- allow-delete

func TestAllowDeleteDefaultIsShort(t *testing.T) {
	allowDeleteRig(t)
	out, _, err := execute(t, allowDeleteArgs(webApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "delete-list")
	if !strings.Contains(out, "may now delete or replace 1 object(s)") {
		t.Errorf("the step does not say what the write approved:\n%s", out)
	}
}

func TestAllowDeleteWritesALog(t *testing.T) {
	allowDeleteRig(t)
	if _, _, err := execute(t, allowDeleteArgs(webApproval)...); err != nil {
		t.Fatal(err)
	}
	// The addresses the step's line points at are the command's own print and
	// go in the log with it (D1352): the entry is what a later refusal is read
	// against, and the log is where a run's account is read back.
	log := writesALog(t, "allow-delete")
	if !strings.Contains(log, webApproval.address) {
		t.Errorf("the log does not hold the list the step says it wrote:\n%s", log)
	}
}

// TestAllowDeleteAFailedWriteIsAFailedStep: the list's write is the run's one
// step and the step is opened before it, so a bucket that refuses the write
// ends it with a cross and the run says it failed — the failure is a step on
// the screen and not an error under a line claiming the list was written (G8).
// The run's own log holds the same failure, which is what the path under it
// sends a person to read (D1352).
//
// The write is refused the way a real one is: a credential with no write grant
// at the bucket's root answers 403 (the fake's DenyWrites), which is the shape
// every launcher's session meets.
func TestAllowDeleteAFailedWriteIsAFailedStep(t *testing.T) {
	r := allowDeleteRig(t)
	r.gcs.DenyWrites(initRunsBucket, managed.AllowDeleteObject)
	out, errOut, err := execute(t, allowDeleteArgs(webApproval)...)
	if err == nil {
		t.Fatalf("the command reported a write the bucket refused:\n%s%s", out, errOut)
	}
	if !strings.Contains(out, markFailed+" delete-list:") {
		t.Errorf("the refused write is not a failed step:\n%s%s", out, errOut)
	}
	reported(t, out, "delete-list")
	if !strings.Contains(out, "full output: ") {
		t.Errorf("the failure does not say where the run's own output is:\n%s", out)
	}
	// And the log is the run the screen shows: the same failed step and the
	// same closing line (the path above points at it).
	log := writesALog(t, "allow-delete")
	if !strings.Contains(log, markFailed+" delete-list:") {
		t.Errorf("the log does not hold the failed step:\n%s", log)
	}
}

// ------------------------------------------------------------ managed flag

func TestManagedFlagDefaultIsShort(t *testing.T) {
	managedFlagRig(t)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "flag")
	if !strings.Contains(out, "read back") {
		t.Errorf("the step does not say the marker was read back:\n%s", out)
	}
}

func TestManagedFlagWritesALog(t *testing.T) {
	managedFlagRig(t)
	terminal(t, true)
	if _, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "managed-flag")
}

// ---------------------------------------------------------- managed unflag

func TestManagedUnflagDefaultIsShort(t *testing.T) {
	r := managedUnflagRig(t)
	if !managedFlagIsManaged(t, r) {
		t.Fatal("the rig's project does not read as managed before the command")
	}
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "unflag")
	// What each half of the mark came off as stays on the screen beside the
	// step, because "was not there" is the difference between a project this
	// command converged and one it did nothing to.
	for _, want := range []string{"removed", "read back", "is not managed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command does not say %q:\n%s", want, out)
		}
	}
}

func TestManagedUnflagWritesALog(t *testing.T) {
	managedUnflagRig(t)
	terminal(t, true)
	if _, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...); err != nil {
		t.Fatal(err)
	}
	// The command's own lines are in the log as well as on the screen: it
	// prints them itself, and the log is the run's full output (D1352). The
	// halves are the ones it prints while the step is in flight, so a
	// relocation that kept them on the screen alone would leave the log
	// holding a step that points at nothing.
	log := writesALog(t, "managed-unflag")
	if !strings.Contains(log, "  marker object gs://") || !strings.Contains(log, "removed") {
		t.Errorf("the log does not hold what the run printed about the two halves:\n%s", log)
	}
}

// TestManagedUnflagPrintsTheHalvesOnTheirOwnLine: what each half of the mark
// came off as is printed while the unflag's step is in flight, so the line in
// flight is taken down for it (G1). It is the half of the account an owner
// cannot lose — "was not there" is the difference between a project this
// command converged and one it did nothing to — and a spinner's carriage
// return drawn over it is that difference smeared off the screen. The fixture
// is a terminal, which is the only place a line in flight is drawn at all
// (G4).
func TestManagedUnflagPrintsTheHalvesOnTheirOwnLine(t *testing.T) {
	managedUnflagRig(t)
	terminal(t, true)
	writerAsTerminal(t)
	// Without this the test would pass on a machine whose TERM happens to be
	// dumb, where nothing is drawn to draw over (G4).
	t.Setenv("TERM", "xterm-256color")
	watch := &screenWatch{}
	cmd := NewRootCmd()
	cmd.SetIn(strings.NewReader(initProject + "\n"))
	// One writer for both, which is what a terminal is: the reporter draws on
	// stdout and the halves go to the same screen.
	cmd.SetOut(watch)
	cmd.SetErr(watch)
	cmd.SetArgs(NormalizeArgs(managedUnflagArgs()))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("the unflag: %v\n%s", err, watch.String())
	}
	notDrawnOver(t, watch, "what each half of the mark came off as")
	for _, want := range []string{"removed", "read back", "is not managed"} {
		if !strings.Contains(watch.String(), want) {
			t.Errorf("the command does not say %q:\n%s", want, watch.String())
		}
	}
}

// ----------------------------------------------------------- managed apply

func TestManagedApplyDefaultIsShort(t *testing.T) {
	_, root, _ := managedOpenGateRig(t)
	out, errOut, err := execute(t, "managed", "apply", root)
	if err != nil && out == "" {
		t.Fatalf("the run printed no account at all: %v\n%s", err, errOut)
	}
	reported(t, out, "apply")
}

func TestManagedApplyWritesALog(t *testing.T) {
	_, root, _ := managedOpenGateRig(t)
	_, _, _ = execute(t, "managed", "apply", root)
	writesALog(t, "managed-apply")
}

// managedApplyOnScreen runs `managed apply` with one writer standing in for a
// person's terminal — stdout and stderr are the same screen there — and returns
// what it wrote.
func managedApplyOnScreen(t *testing.T, root string) (*screenWatch, error) {
	t.Helper()
	writerAsTerminal(t)
	// Without this the test would pass on a machine whose TERM happens to be
	// dumb, where nothing is drawn to draw over (G4).
	t.Setenv("TERM", "xterm-256color")
	watch := &screenWatch{}
	cmd := NewRootCmd()
	cmd.SetIn(strings.NewReader(initProjectName + "\n"))
	cmd.SetOut(watch)
	cmd.SetErr(watch)
	cmd.SetArgs(NormalizeArgs([]string{"managed", "apply", root}))
	return watch, cmd.Execute()
}

// TestManagedApplyAsksOnItsOwnLine: the managed apply's question is asked in
// the middle of its one step, after the plan has been gated, so the line in
// flight is taken down before the question is written and drawn again once it
// is answered (G1). A question a person reads through a spinner is one nobody
// can answer, and this is the question that decides whether a plan is applied.
func TestManagedApplyAsksOnItsOwnLine(t *testing.T) {
	noAgentSession(t)
	_, root := inventoryApplyRig(t)
	stubManagedAssets(t, &fakeManagedAssets{assets: []managedmode.Asset{
		{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
		{Name: "//storage.googleapis.com/fmgd-data", Type: "storage.googleapis.com/Bucket"},
	}})
	terminal(t, true)
	watch, err := managedApplyOnScreen(t, root)
	notDrawnOver(t, watch, "the question it asks the person at the terminal")
	if after := watch.after(); !strings.Contains(after, "Type "+initProjectName+" to confirm: ") {
		t.Fatalf("the command never asked the person at the terminal (err %v):\n%s", err, watch.String())
	}
	// The answer puts the line back, so the apply is still the one step the
	// person is watching while it runs: Resume is the only thing that draws
	// again — the frames are stopped until it does — so a redraw after the
	// question is what says it happened.
	if after := watch.after(); !strings.Contains(after, "\r") {
		t.Errorf("the answer did not put the line in flight back:\n%q", after)
	}
}

// TestManagedApplyDoesNotDrawOverItsOwnOutput: the apply's step is open across
// everything the apply itself prints — how it is about to apply, and D59's
// warning when the whole-project read fell back — so each of those writes takes
// the line in flight down first (G1). The warning is the one this fixture
// reaches: the run is refused at the confirmation, which is where the fallback
// is said, and it is a sentence every session reads (Request.confirm).
func TestManagedApplyDoesNotDrawOverItsOwnOutput(t *testing.T) {
	noAgentSession(t)
	_, root := inventoryApplyRig(t)
	const foreign = "//compute.googleapis.com/projects/" + initProject + "/zones/us-east5-a/instances/somebody-elses"
	stubManagedAssets(t, &fakeManagedAssets{assets: []managedmode.Asset{
		{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
		{Name: foreign, Type: "compute.googleapis.com/Instance"},
	}})
	watch, err := managedApplyOnScreen(t, root)
	if err == nil {
		t.Fatalf("the command applied without a confirmation:\n%s", watch.String())
	}
	notDrawnOver(t, watch, "the apply's own output")
	if !strings.Contains(watch.after(), "this run falls back to confirmations") || !strings.Contains(watch.after(), foreign) {
		t.Fatalf("the run never said the read fell back (err %v):\n%s", err, watch.String())
	}
}

// ----------------------------------------------------------------- restore

func TestRestoreDefaultIsShort(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	recordRestoreApply(t, r)
	_, root := restoreCheckout(t)
	out, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
	if err != nil {
		t.Fatalf("the restore: %v\n%s", err, out)
	}
	reported(t, out, "config", "project", "checkout", "account")
}

func TestRestoreWritesALog(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	recordRestoreApply(t, r)
	_, root := restoreCheckout(t)
	if _, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "restore")
}

// ------------------------------------------------------------ budget writes

func TestBudgetSetDefaultIsShort(t *testing.T) {
	newBudgetFixture(t, "")
	out, _, err := execute(t, "budget", "set", "--global", "--daily", "60", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, nodeCG)
	if !strings.Contains(stepLineOf(reportLines(out), nodeCG), "$60") {
		t.Errorf("the step does not carry the value that was written:\n%s", out)
	}
}

func TestBudgetSetWritesALog(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "set", "--global", "--daily", "60", "--yes"); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "budget-set")
}

func TestBudgetKillDefaultIsShort(t *testing.T) {
	newBudgetFixture(t, "")
	out, _, err := execute(t, "budget", "kill", "--all", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "project")
	if !strings.Contains(out, "killed") {
		t.Errorf("the step does not say the project was killed:\n%s", out)
	}
}

func TestBudgetKillWritesALog(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "kill", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "budget-kill")
}

func TestBudgetResumeDefaultIsShort(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "kill", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "budget", "resume", "--all", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "project")
	if !strings.Contains(out, "resumed") {
		t.Errorf("the step does not say the project was resumed:\n%s", out)
	}
}

func TestBudgetResumeWritesALog(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "kill", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "budget", "resume", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "budget-resume")
}

// TestNoStepCommandPrintsAnsiOffATerminal: a step-printing command's marks are
// plain where nothing is a terminal, whatever the environment says (G4). The
// reporter decides that once; this is the batch's own guard that no migrated
// command paints its own output.
func TestNoStepCommandPrintsAnsiOffATerminal(t *testing.T) {
	newBudgetFixture(t, "")
	for _, args := range [][]string{
		{"budget", "set", "--global", "--daily", "60", "--yes"},
		{"budget", "kill", "--all", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, errOut, err := execute(t, args...)
			if err != nil {
				t.Fatalf("%v\n%s%s", err, out, errOut)
			}
			for _, s := range []string{out, errOut} {
				if strings.Contains(s, "\x1b") {
					t.Errorf("`fugaro %s` printed ANSI off a terminal:\n%q", strings.Join(args, " "), s)
				}
			}
			if !strings.Contains(out, markDone) && !strings.Contains(out, markSkipped) {
				t.Errorf("`fugaro %s` printed no step line:\n%s", strings.Join(args, " "), out)
			}
		})
	}
}

// TestTheWriterCommandsAreNamedWhatTheLogsAre: the log's name is the command
// name validateLogName accepts, so a name with a character the file name rule
// refuses would leave a run with no log and no error (OpenLog's refusal is
// silent by design, G5). Spelled here rather than read out of the reporter's
// own regexp, so a rename that broke the rule fails here.
func TestTheWriterCommandsAreNamedWhatTheLogsAre(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
	for _, name := range []string{
		"config-publish", "allow-delete", "managed-apply", "managed-flag",
		"managed-unflag", "restore", "budget-set", "budget-kill", "budget-resume",
	} {
		if !re.MatchString(name) {
			t.Errorf("fugaro %s files its log under %q, which is not a name the log rule takes", strings.ReplaceAll(name, "-", " "), name)
		}
	}
}
