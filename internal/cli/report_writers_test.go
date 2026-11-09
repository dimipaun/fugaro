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
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// clearLine is the reporter's own write that takes the line in flight down
// (stopSpinner), and the one a command's writer makes for it before printing a
// block (Pause).
const clearLine = "\r\x1b[K"

// screenWatch is a run's screen, watched as it is written. It reads the two
// writes the reporter makes to its own line — a frame, which draws the step in
// flight with a carriage return, and clearLine, which takes it down — and
// everything else is a block the run printed itself. It keeps what was on the
// screen just before the first of those, and every block that landed on a line
// that was still up: the second is what a person would have seen drawn through
// (G1).
type screenWatch struct {
	mu  sync.Mutex
	buf bytes.Buffer
	// frame is whether the line in flight is up on the screen right now, and
	// drew whether one ever was (the blocks before that are not a step's).
	frame  bool
	drew   bool
	before string
	found  bool
	over   []string
}

func (w *screenWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case bytes.HasPrefix(p, []byte(clearLine)):
		w.frame = false
	case bytes.HasPrefix(p, []byte("\r")):
		w.frame, w.drew = true, true
	default:
		if w.frame {
			block := string(p)
			if len(block) > 80 {
				block = block[:80] + "…"
			}
			w.over = append(w.over, block)
		}
		if w.drew && !w.found {
			w.found, w.before = true, w.buf.String()
		}
	}
	return w.buf.Write(p)
}

func (w *screenWatch) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
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

// nothingDrawnOver is G1's assertion for a whole run: not one block the run
// printed landed on a line that was still up. The fixtures it is used with
// write inside an open step — terraform's own stream, the question an apply
// asks — and each of those has to take the line down first, which is a
// property of the run rather than of one block in it.
func nothingDrawnOver(t *testing.T, w *screenWatch) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, block := range w.over {
		t.Errorf("this was written onto the line in flight: %q", block)
	}
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

// TestAllowDeleteDroppedEntriesAreInTheLog: the entries a write leaves out are
// what the person is agreeing to lose, and they are the run's own account of
// what it is about to write rather than a question to whoever is at the
// terminal. They go through the run's own writer, so a run read back afterwards
// holds what was removed and not only that something was (D1352).
func TestAllowDeleteDroppedEntriesAreInTheLog(t *testing.T) {
	r := allowDeleteRig(t)
	// Seeded rather than left by an earlier command: two runs of one command in
	// one second are two logs of one name, and onlyLog wants one.
	r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, []byte(allowDeleteJSON(initProject, dataApproval, webApproval)))
	terminal(t, true)
	out, _, err := executeStdin(t, initProjectName+"\n", allowDeleteArgs(webApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, dataApproval.arg()) {
		t.Fatalf("the run never named the approval it drops:\n%s", out)
	}
	log := writesALog(t, "allow-delete")
	if !strings.Contains(log, dataApproval.arg()) || !strings.Contains(log, "this list leaves out 1 of them") {
		t.Errorf("the log holds the step and not the entry the write drops:\n%s", log)
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

// tfStream is the fake terraform's own progress: the stream a person watches
// while an apply's step is open, and the one write of the run that is not the
// run's own account of anything (tf.TF.Out, not Request.Out).
const tfStream = "fake terraform: planning the root\n"

// managedApplyStream makes the fake terraform print tfStream on its init: the
// rig's plan carries the exit code the gate's "changed" is read from, and init
// is a stream of terraform's like any other — the one that runs first, with the
// apply's step already open.
func managedApplyStream(t *testing.T, r *initRig) {
	t.Helper()
	r.script["init"] = map[string]any{"stderr": tfStream}
	r.save(t)
}

// TestManagedApplyAsksOnItsOwnLine: the managed apply's question is asked in
// the middle of its one step, after the plan has been gated, so the line in
// flight is taken down before the question is written and drawn again once it
// is answered (G1). A question a person reads through a spinner is one nobody
// can answer, and this is the question that decides whether a plan is applied.
func TestManagedApplyAsksOnItsOwnLine(t *testing.T) {
	noAgentSession(t)
	r, root := inventoryApplyRig(t)
	stubManagedAssets(t, &fakeManagedAssets{assets: []managedmode.Asset{
		{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
		{Name: "//storage.googleapis.com/fmgd-data", Type: "storage.googleapis.com/Bucket"},
	}})
	managedApplyStream(t, r)
	terminal(t, true)
	watch, err := managedApplyOnScreen(t, root)
	// Everything this run writes with the step open — terraform's own stream,
	// which is not the run's own lines, and the question — takes the line down
	// first, so the progress reads as terraform printed it rather than through
	// a frame.
	nothingDrawnOver(t, watch)
	question := "Type " + initProjectName + " to confirm: "
	out := watch.String()
	at := strings.Index(out, question)
	if at < 0 {
		t.Fatalf("the command never asked the person at the terminal (err %v):\n%s", err, out)
	}
	// The answer puts the line back, so the apply is still the one step the
	// person is watching while it runs: Resume is the only thing that draws
	// again — the frames are stopped until it does — so a redraw after the
	// question is what says it happened.
	if after := out[at+len(question):]; !strings.Contains(after, "\r") {
		t.Errorf("the answer did not put the line in flight back:\n%q", after)
	}
	if !strings.Contains(out, tfStream) {
		t.Errorf("terraform's own progress never reached the screen:\n%s", out)
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
	r, root := inventoryApplyRig(t)
	const foreign = "//compute.googleapis.com/projects/" + initProject + "/zones/us-east5-a/instances/somebody-elses"
	stubManagedAssets(t, &fakeManagedAssets{assets: []managedmode.Asset{
		{Name: "//storage.googleapis.com/" + initRunsBucket, Type: "storage.googleapis.com/Bucket"},
		{Name: foreign, Type: "compute.googleapis.com/Instance"},
	}})
	managedApplyStream(t, r)
	watch, err := managedApplyOnScreen(t, root)
	if err == nil {
		t.Fatalf("the command applied without a confirmation:\n%s", watch.String())
	}
	nothingDrawnOver(t, watch)
	if !strings.Contains(watch.String(), "this run falls back to confirmations") || !strings.Contains(watch.String(), foreign) {
		t.Fatalf("the run never said the read fell back (err %v):\n%s", err, watch.String())
	}
	// Terraform's own progress is watched and not kept: this command has no
	// redaction source, so D1352 gives it a log of the stage lines only
	// (streamThrough), and what the failure's "full output:" points at is the
	// run's own account of itself.
	if _, log := onlyLog(t, "managed-apply"); strings.Contains(log, tfStream) {
		t.Errorf("terraform's raw stream is in the run's log:\n%s", log)
	} else if !strings.Contains(watch.String(), tfStream) {
		t.Errorf("terraform's own progress never reached the screen:\n%s", watch.String())
	}
}

// TestManagedApplyKeepsNoTerraformTailInItsLog: the line a failed apply's step
// ends with names the failure and stops there. terraform's own stderr tail is a
// provider's bytes, and this command has no redaction source, so its log keeps
// its own lines and never that output (F6, D1352) — while the screen keeps the
// tail twice over: terraform streamed it as it ran (TF.Out), and the error the
// command returns carries it into the "Error:" line under the closing one.
//
// The tail is a sentence of its own, so "the log does not hold it" is read
// directly rather than inferred from the absence of terraform's whole stream.
func TestManagedApplyKeepsNoTerraformTailInItsLog(t *testing.T) {
	const tail = "Error: creating a bucket: googleapi: Error 403: the provider refused fmgd-data"
	isolateState(t)
	r, root, _ := managedOpenGateRig(t)
	r.script["init"] = map[string]any{"exit": 1, "stderr": tail}
	r.save(t)
	out, errOut, err := execute(t, "managed", "apply", root)
	if err == nil {
		t.Fatalf("the apply finished though its terraform init failed:\n%s%s", out, errOut)
	}
	screen := out + errOut
	if !strings.Contains(screen, markFailed+" apply") {
		t.Errorf("the failed apply is not the step that failed:\n%s", screen)
	}
	if !strings.Contains(screen, tail) {
		t.Errorf("terraform's own tail never reached the screen:\n%s", screen)
	}
	_, log := onlyLog(t, "managed-apply")
	if strings.Contains(log, tail) {
		t.Errorf("terraform's own stderr tail is in the run's log:\n%s", log)
	}
	if !strings.Contains(log, "terraform init exited 1") {
		t.Errorf("the log's line does not name the failure the step ended with:\n%s", log)
	}
}

// ----------------------------------------------------------------- restore

func TestRestoreDefaultIsShort(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	recordRestoreApply(t, r)
	_, root := restoreCheckout(t)
	out, err := restoreStepsWith(t, r, createStdin, root)
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
	if _, err := restoreStepsWith(t, r, createStdin, root); err != nil {
		t.Fatal(err)
	}
	log := writesALog(t, "restore")
	// D66's list is the lines under the step line that names it, and the run's
	// log is where the step line points a failure: a step line ending in
	// "NOT restored:" with nothing under it is a list nobody can read.
	if !strings.Contains(log, "NOT restored:") || !strings.Contains(log, "\n  - the runs that were in flight") {
		t.Errorf("the log ends at the step line's list and holds none of the list:\n%s", log)
	}
}

// TestRestoreKeepsNoTerraformTailInItsLog: the restore's log is the run's own
// stage lines and never a provider's output (F6, D1352). The creating child
// fails on terraform, whose error carries the command's own stderr tail
// (tf.ExitError, through the creating run's stage error), and this command has
// no redaction source: the project step's line names the failure and not the
// tail, while the screen keeps the tail — terraform streamed it to the child's
// stderr, and this run's error prints it again.
func TestRestoreKeepsNoTerraformTailInItsLog(t *testing.T) {
	const tail = "Error: creating a bucket: googleapi: Error 403: the provider refused the new installation"
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	recordRestoreApply(t, r)
	_, root := restoreCheckout(t)
	r.script["apply"] = map[string]any{"exit": 1, "stderr": tail}
	r.save(t)
	out, err := restoreStepsWith(t, r, createStdin, root)
	if err == nil {
		t.Fatalf("the restore finished though the creating run's apply failed:\n%s", out)
	}
	// The child run's step line carries one clause of terraform's own error and
	// names the child's log, which is where the whole of it is (report.Brief) —
	// this command's log holds no provider output, which the rest of this test
	// is about, so the tail is read from the log the failing step points at.
	if !strings.Contains(out, "terraform apply exited 1: Error: creating a bucket") {
		t.Errorf("terraform's own error never reached the screen:\n%s", out)
	}
	if _, setupLog := onlyLog(t, "setup"); !strings.Contains(setupLog, tail) {
		t.Errorf("terraform's own tail reached neither the screen nor the log the failing step names:\n%s", out)
	}
	_, log := onlyLog(t, "restore")
	if strings.Contains(log, tail) {
		t.Errorf("terraform's own stderr tail is in the run's log:\n%s", log)
	}
	if !strings.Contains(log, "terraform apply exited 1") {
		t.Errorf("the log's line does not name the failure the step ended with:\n%s", log)
	}
}

// TestRestoreEndsItsApplyStepWhenTheApplyFailsBeforeItsPlan: the apply a
// restore ends in opens its step before it reads anything, so a run the apply
// turns away before the plan — the project number, the budget gate, the
// owner's list, the caller's own credential, the workdir, the root copy, the
// plugin cache, terraform on PATH — ends that step, and the restore's closing
// line is a failure. Without it the restore had already marked config, project
// and checkout done, so the same run closed with "Done in …, 3 steps" on the
// screen and in its log while the command exited 1 (G3).
//
// The caller read is the one stubbed: it is one of the eight, it is the last of
// the run's own reads, and managedCaller is the seam the package's own tests
// use for it. Nothing the restore does before the apply reads it — the creating
// path is the setup flow, not this one — so the refusal lands where the finding
// says it does, between the opener and the plan.
func TestRestoreEndsItsApplyStepWhenTheApplyFailsBeforeItsPlan(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	oldCaller := managedCaller
	managedCaller = func(context.Context, *localcfg.Config) (string, oauth2.TokenSource, error) {
		return "", nil, userErr("no credential for this run")
	}
	t.Cleanup(func() { managedCaller = oldCaller })
	_, root := restoreCheckout(t)
	out, err := restoreStepsWith(t, r, createStdin, root)
	if err == nil {
		t.Fatalf("the restore finished though the apply's caller read failed:\n%s", out)
	}
	if !strings.Contains(out, markFailed+" apply") {
		t.Errorf("the apply that failed before its plan is not the step that failed:\n%s", out)
	}
	// The creating child is a run of its own and prints its own closing line to
	// this terminal, so the question is the run's last line and not whether the
	// words appear.
	if lines := reportLines(out); !strings.HasPrefix(lines[len(lines)-1], "Failed in ") {
		t.Errorf("the run's closing line is missing, or is not the failure it was:\n%s", out)
	}
	_, log := onlyLog(t, "restore")
	if !strings.Contains(log, markFailed+" apply") {
		t.Errorf("the run's log holds no failed apply step:\n%s", log)
	}
	if !strings.Contains(log, "no credential for this run") {
		t.Errorf("the log's line does not name the failure the apply ended with:\n%s", log)
	}
	if lines := reportLines(log); !strings.HasPrefix(lines[len(lines)-1], "Failed in ") {
		t.Errorf("the log's closing line is missing, or is not the failure it was:\n%s", log)
	}
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

// TestTheWriterCommandsPrintNoAnsiOffATerminal: a step-printing command's
// marks are plain where nothing is a terminal, whatever the environment says
// (G4), and it still reports its step. The reporter decides the first; this is
// the batch's own guard that no migrated command paints its own output, and it
// is the counterpart of CL7c's TestNoStepCommandPrintsAnsiOffATerminal
// (report_run_test.go), which is about the commands that report no step at all.
func TestTheWriterCommandsPrintNoAnsiOffATerminal(t *testing.T) {
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
