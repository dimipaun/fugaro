package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// The update door's half of the login unit's step (O9, T7's ruling of
// 2026-11-05). It is what makes `fugaro update` the whole upgrade: a release
// that changes the unit's template re-renders the unit this machine already has,
// with no second command and no question — and a machine that never installed
// one gets none from here.

// updateUnitEngine is the engine an update's own run has: the door is what makes
// initStages hand this stage over, and the stage itself reads nothing else of it.
func updateUnitEngine(t *testing.T, o *initOptions) (*initEngine, *syncBuf) {
	t.Helper()
	o.door = updateDoor.name
	return stageEngine(t, "", o)
}

// unitFileAt writes text at the unit's own path and returns it, with the file's
// time pinned so that a test can tell a write from a leave-alone.
func unitFileAt(t *testing.T, text string, at time.Time) string {
	t.Helper()
	u := currentUnit(t)
	if err := os.MkdirAll(filepath.Dir(u.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.Path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(u.Path, at, at); err != nil {
		t.Fatal(err)
	}
	return u.Path
}

// previousTemplate is a unit a previous release wrote: not this release's text,
// and not a template this checkout can render.
const previousTemplate = "[Unit]\nDescription=Fugaro orchestrator (0.20.0)\n\n[Service]\nExecStart=/usr/local/bin/fugaro orchestrator run --binary /usr/local/bin/fugaro\n"

func TestUpdateRerendersAnInstalledUnitWhenTheTemplateChanged(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	path := unitFileAt(t, previousTemplate, time.Now())
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	u := currentUnit(t)
	if got := stateOf(res, initflow.Unit); got != initflow.Changed {
		t.Fatalf("the step is %s, want changed:\n%+v", got, res.Stages)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != u.Text {
		t.Errorf("the unit was not re-rendered from the current template:\n--- on disk\n%s\n--- rendered\n%s", data, u.Text)
	}
	if len(f.reloads) != 1 || f.reloads[0].Path != path {
		t.Errorf("the service manager was reloaded with %v, want one call for %s", f.reloads, path)
	}
	if len(res.Left) != 0 {
		t.Errorf("the step left something for the user: %v", res.Left)
	}
	if askedSomething(out) {
		t.Errorf("the step asked a question, and one fugaro update is the whole upgrade:\n%s", out)
	}
}

// TestUpdateNeverInstallsAUnit: no unit on this machine is a project that did
// not choose the daemon or a person who skipped setup. `update` writes none, and
// the plan line says so — that boundary is the ruling's, not an oversight.
func TestUpdateNeverInstallsAUnit(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
		t.Fatalf("the step is %s, want skipped: there is nothing installed to re-render\n%+v", got, res.Stages)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "never installs one") || !strings.Contains(d, "orchestrator install") {
		t.Errorf("the plan line does not say why nothing was installed: %q", d)
	}
	if !strings.Contains(out, "no login unit is installed") {
		t.Errorf("the run's plan does not say there is no unit:\n%s", out)
	}
	if _, err := os.Stat(currentUnit(t).Path); !os.IsNotExist(err) {
		t.Errorf("update wrote a unit (stat: %v)", err)
	}
	if len(f.reloads) != 0 {
		t.Errorf("update reloaded a service that is not there: %v", f.reloads)
	}
}

// TestUpdateLeavesACurrentUnitAlone: the text on disk is what this release
// renders, so there is nothing to write and nothing to reload — a repeated
// update is quiet, which is what makes the step safe to run on every release.
func TestUpdateLeavesACurrentUnitAlone(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	path := unitFileAt(t, currentUnit(t).Text, at)
	res, _ := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Done {
		t.Fatalf("the step is %s, want done:\n%+v", got, res.Stages)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "No changes") {
		t.Errorf("the stage's line is %q, want No changes", d)
	}
	if len(f.reloads) != 0 {
		t.Errorf("a current unit was reloaded: %v", f.reloads)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(at) {
		t.Errorf("the file was written (%s, want %s): a re-render that churns an identical file would make every update noisy", st.ModTime(), at)
	}
}

// TestUpdateRerendersWhenTheBinaryPathChanged: the render carries the stable
// binary path, so a unit written under another prefix — a moved Homebrew
// installation — is one the step re-renders.
func TestUpdateRerendersWhenTheBinaryPathChanged(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	u := currentUnit(t)
	moved := "/opt/homebrew/opt/fugaro/bin/fugaro"
	path := unitFileAt(t, strings.ReplaceAll(u.Text, stableBinaryPath(exePath(t)), moved), time.Now())
	res, _ := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Changed {
		t.Fatalf("the step is %s, want changed:\n%+v", got, res.Stages)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != u.Text || strings.Contains(string(data), moved) {
		t.Errorf("the unit still names the path it was written with:\n%s", data)
	}
	if len(f.reloads) != 1 {
		t.Errorf("the service was reloaded %d times, want once", len(f.reloads))
	}
}

// TestUpdateUnitStepAsksNothingAndIsLeftForThePerson: the step asks nothing at a
// terminal (T8), and it is still never covered by --yes, a unit being code that
// runs at login: a run that cannot put a person at the terminal leaves the unit
// where it is and says which command does it. A coding agent's session writes
// nothing at all, which is what keeps the drift on the doctor's report until a
// person runs the command.
//
// What the step does NOT do is ask: the plan line is a line of the plan the
// update showed at the person's own terminal, and re-rendering a unit they
// installed themselves is not a new write of code into their login. An extra
// confirmation here would be the operator step the ruling exists to remove.
func TestUpdateUnitStepAsksNothingAndIsLeftForThePerson(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	if initflow.YesCovers(initflow.Unit) {
		t.Error("--yes covers the login unit's step: a unit runs code at every login")
	}
	e, _ := updateUnitEngine(t, &initOptions{yes: true, nonInteractive: true})
	path := unitFileAt(t, previousTemplate, time.Now())
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.NeedsYou {
		t.Fatalf("the step is %s at --yes with nobody at the terminal, want needs-you:\n%+v", got, res.Stages)
	}
	if len(res.Left) != 1 || !strings.HasSuffix(res.Left[0].Text, " update") {
		t.Errorf("the step's left-for-you is %v, want the command that does it in the person's own terminal", res.Left)
	}
	if askedSomething(out) {
		t.Errorf("the step asked a question:\n%s", out)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != previousTemplate {
		t.Errorf("--yes wrote the unit (err %v):\n%s", err, data)
	}
	if len(f.reloads) != 0 {
		t.Errorf("--yes reached the service manager: %v", f.reloads)
	}

	// And an agent's session: the same file, the marker's own sentence, and no
	// write.
	e, _ = updateUnitEngine(t, &initOptions{})
	t.Setenv("CLAUDECODE", "1")
	res, _ = runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
		t.Fatalf("the step is %s in an agent's session, want skipped:\n%+v", got, res.Stages)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "CLAUDECODE") {
		t.Errorf("the skip does not carry the marker's own sentence: %q", d)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != previousTemplate {
		t.Errorf("an agent's session wrote the unit (err %v):\n%s", err, data)
	}
}

// TestUpdateUnitStepWritesNothingWithoutAPersonTheStepCovers: the stage asks its
// own question as well as leaning on the loop. `--ask` at a terminal is the one
// mode where the loop does reach Apply with a person the step's class does not
// cover (the step it cannot ask about is left for the person like every other
// uncovered step), so it is the path that exercises the stage's own guard: the
// step ends as needs-you, the installed unit is left exactly as it was, and the
// manager is never handed a file nothing wrote. An agent's session is the other
// half, above.
func TestUpdateUnitStepWritesNothingWithoutAPersonTheStepCovers(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	terminal(t, true)
	e, _ := updateUnitEngine(t, &initOptions{ask: true})
	path := unitFileAt(t, previousTemplate, time.Now())
	res, _ := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.NeedsYou {
		t.Fatalf("the step is %s under --ask at a terminal, want needs-you:\n%+v", got, res.Stages)
	}
	if len(res.Left) != 1 || !strings.HasSuffix(res.Left[0].Text, " update") {
		t.Errorf("the step's left-for-you is %v, want the command that does it in the person's own terminal", res.Left)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != previousTemplate {
		t.Errorf("the step wrote the unit without a person it covers (err %v):\n%s", err, data)
	}
	if len(f.reloads) != 0 {
		t.Errorf("the step reloaded the service although it wrote nothing: %v", f.reloads)
	}
}

// TestUpdateUnitStepTouchesNoSystemPath: the write is the person's own file under
// their own home and the service manager is asked at their own login level —
// never a system path, and never a sudo.
func TestUpdateUnitStepTouchesNoSystemPath(t *testing.T) {
	dir := unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	path := unitFileAt(t, previousTemplate, time.Now())
	_, out := runUnitStage(t, e, newUpdateUnitStage(e))
	if !strings.HasPrefix(path, filepath.Join(dir, "home")) {
		t.Fatalf("the unit's path %s is not under the person's home", path)
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasPrefix(p, filepath.Join(dir, "home")) {
			t.Errorf("the step wrote %s, which is outside the person's home", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The plan line the person reads says what the write is: their own file
	// under their own home, and no root, no sudo and no network behind it.
	for _, want := range []string{"no system path", "no root", "no sudo", "no network"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan line does not say %q:\n%s", want, out)
		}
	}
	if len(f.reloads) != 1 || f.reloads[0].Path != path {
		t.Errorf("the service manager was reloaded with %v, want the person's own unit at %s", f.reloads, path)
	}
}

// TestUpdateUnitStepUsesTheInstallersRenderer: there is one renderer and one
// installer, and the update step is a caller of them rather than a second copy.
// The file it leaves is byte-for-byte what service.Render produces (currentUnit
// goes through it), the fake service manager's own Render is never called — it
// answers an error, so a step that rendered through the manager would fail — and
// the step's source carries no template and no write of its own.
func TestUpdateUnitStepUsesTheInstallersRenderer(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	path := unitFileAt(t, previousTemplate, time.Now())
	runUnitStage(t, e, newUpdateUnitStage(e))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != currentUnit(t).Text {
		t.Errorf("the step rendered something other than service.Render's own text:\n%s", data)
	}
	if len(f.reloads) != 1 || f.reloads[0].Text != string(data) {
		t.Errorf("the reload did not go through the installer's own call: %v", f.reloads)
	}
	for _, name := range []string{"update_unit_stage.go", "init_orchestrator_stage.go"} {
		src := stageSource(t, name)
		if !strings.Contains(src, "installUnit(") {
			t.Errorf("%s does not call the one installer", name)
		}
		for _, own := range []string{"os.WriteFile", "KeepAlive", "Restart=always", "launchctl", "systemctl"} {
			if strings.Contains(src, own) {
				t.Errorf("%s writes its own %s: the template and the reload belong to the service package and to orchestrator_install.go", name, own)
			}
		}
	}
}

// exePath is the binary this test process runs as — what the unit's render is
// built from, and so the path a test that changes the prefix has to name.
func exePath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}
