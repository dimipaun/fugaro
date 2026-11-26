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
// 2026-11-05, D2700's install of a missing unit). It is what makes `fugaro
// update` the whole upgrade: a release that changes the unit's template
// re-renders the unit this machine already has, with no second command and no
// question, and a machine that has none gets one here — at a person's own
// terminal, in one line of the plan and with nothing asked — so that the one
// command a person runs turns the daemon on everywhere their project's people
// work.

// updateUnitEngine is the engine an update's own run has: the door is what makes
// initStages hand this stage over, and the stage itself reads nothing else of it.
func updateUnitEngine(t *testing.T, o *initOptions) (*initEngine, *syncBuf) {
	t.Helper()
	o.door = updateDoor.name
	return stageEngine(t, "", o)
}

// releaseUpdate makes this process the fugaro a release installed: a versioned
// build, at a path a person installed it to. The update door re-points an
// installed unit only at one of those — a login unit must not be pointed at a
// build that will not be there tomorrow — so every test that expects the step to
// write the file says which kind of run it is, and the test that pins what a
// build that will not be there tomorrow does says so itself, with the version
// and the path it means spelled out rather than taken from the test binary.
func releaseUpdate(t *testing.T) string {
	t.Helper()
	oldVersion, oldExe := Version, orchestratorExecutable
	Version = "1.2.3"
	installed := "/usr/local/bin/fugaro"
	orchestratorExecutable = func() (string, error) { return installed, nil }
	t.Cleanup(func() { Version, orchestratorExecutable = oldVersion, oldExe })
	return installed
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
	releaseUpdate(t)
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

// TestUpdateInstallsMissingUnit: a machine with no unit gets one from `fugaro
// update` (D2700). It is setup's own write — the same renderer, the same
// installer, the same reload — because a machine that skipped `setup`, a
// teammate's machine or one whose unit was removed is exactly what "install
// everywhere" is for: the person's next terminal update turns the daemon on, and
// the per-project lease keeps one of those daemons live. Nothing is asked: the
// plan line is the whole of what the person is shown.
func TestUpdateInstallsMissingUnit(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	releaseUpdate(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	u := currentUnit(t)
	if got := stateOf(res, initflow.Unit); got != initflow.Changed {
		t.Fatalf("the step is %s, want changed: there is no unit on this machine and this run may write one\n%+v", got, res.Stages)
	}
	data, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatalf("the unit was not written: %v", err)
	}
	if string(data) != u.Text {
		t.Errorf("%s is not what this release renders:\n--- on disk\n%s\n--- rendered\n%s", u.Path, data, u.Text)
	}
	if !strings.Contains(out, "installs the login unit at "+u.Path) {
		t.Errorf("the run's plan does not name the unit and the path it is written at:\n%s", out)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "installed "+u.Path) || !strings.Contains(d, "loaded it with") {
		t.Errorf("the stage's own line after the write is %q, want the file and the manager it was loaded with", d)
	}
	if len(f.reloads) != 1 || f.reloads[0].Path != u.Path {
		t.Errorf("the service manager was reloaded with %v, want one call for %s", f.reloads, u.Path)
	}
	if len(res.Left) != 0 {
		t.Errorf("the step left something for the user: %v", res.Left)
	}
	if askedSomething(out) {
		t.Errorf("the step asked a question, and D2700's install asks nothing:\n%s", out)
	}
}

// TestUpdateWithoutTerminalWritesNoUnit: the install is fenced by a person at
// the terminal, the same fence setup's unit stage uses. A pipe, --json or
// --non-interactive writes no unit and the step says so in one line — the unit
// is code that runs at every login, and the machine is left exactly as it was
// for the person's own next update to install from.
func TestUpdateWithoutTerminalWritesNoUnit(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	noTerminal(t)
	releaseUpdate(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
		t.Fatalf("the step is %s with no terminal, want skipped: nobody can read the line a unit is written on\n%+v", got, res.Stages)
	}
	d := res.Stages[0].Detail
	if !strings.Contains(d, "nobody at it") || !strings.Contains(d, "update") {
		t.Errorf("the plan line does not say why nothing was written and where the command that does it is: %q", d)
	}
	if len(res.Left) != 0 {
		t.Errorf("the step left something for the user: %v", res.Left)
	}
	if !strings.Contains(out, "nobody at it") {
		t.Errorf("the run's plan does not carry the one line about the unit:\n%s", out)
	}
	if _, err := os.Stat(currentUnit(t).Path); !os.IsNotExist(err) {
		t.Errorf("a run nobody was at wrote a unit (stat: %v)", err)
	}
	if len(f.reloads) != 0 {
		t.Errorf("a run nobody was at reached the service manager: %v", f.reloads)
	}
}

// TestUpdateInAgentSessionWritesNoUnit: a coding agent's session writes no unit
// either, and the skip is the marker's own sentence — the one an agent reads and
// passes on to the person, whose next terminal `fugaro update` installs it
// (D2700, and T8's rule for the re-render read the same way).
func TestUpdateInAgentSessionWritesNoUnit(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	releaseUpdate(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	// After the engine: stageEngine clears every marker for the test process, so
	// the session this test means is set once the run's own environment is.
	t.Setenv("CLAUDECODE", "1")
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
		t.Fatalf("the step is %s in an agent's session, want skipped:\n%+v", got, res.Stages)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "CLAUDECODE") {
		t.Errorf("the skip does not carry the marker's own sentence: %q", d)
	}
	if askedSomething(out) {
		t.Errorf("an agent's run asked a question:\n%s", out)
	}
	if _, err := os.Stat(currentUnit(t).Path); !os.IsNotExist(err) {
		t.Errorf("an agent's session wrote a unit (stat: %v)", err)
	}
	if len(f.reloads) != 0 {
		t.Errorf("an agent's session reached the service manager: %v", f.reloads)
	}
}

// TestUpdateDryRunWritesNoUnit: the dry run `fugaro update` has is --plan-only,
// and it writes no unit either — the plan names the unit it would install, and
// the file, the manager and the login are all untouched. The plan is the whole
// of what such a run produces, which is what makes it safe to run anywhere.
func TestUpdateDryRunWritesNoUnit(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	releaseUpdate(t)
	e, _ := updateUnitEngine(t, &initOptions{planOnly: true})
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	u := currentUnit(t)
	if got := stateOf(res, initflow.Unit); got == initflow.Changed {
		t.Fatalf("--plan-only wrote the unit:\n%+v\n%s", res.Stages, out)
	}
	if !strings.Contains(out, "installs the login unit at "+u.Path) {
		t.Errorf("the plan does not name the unit a real run would install:\n%s", out)
	}
	if _, err := os.Stat(u.Path); !os.IsNotExist(err) {
		t.Errorf("--plan-only wrote a unit (stat: %v)", err)
	}
	if len(f.reloads) != 0 {
		t.Errorf("--plan-only reached the service manager: %v", f.reloads)
	}
}

// TestUpdateWithoutServiceManagerStillSucceeds: a machine with neither launchd
// nor a systemd user unit — no login unit to write and no manager to load one
// with — is one line and a run that goes on (D2700 as corrected). Nothing is
// written, nothing is left for the user, and the update is not a failure: the
// daemon is a feature of the platforms that carry it, and `fugaro orchestrator
// run` in the foreground is the answer everywhere else.
func TestUpdateWithoutServiceManagerStillSucceeds(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	releaseUpdate(t)
	old := orchestratorGOOS
	orchestratorGOOS = "windows"
	t.Cleanup(func() { orchestratorGOOS = old })
	e, _ := updateUnitEngine(t, &initOptions{})
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
		t.Fatalf("the step is %s on a machine with no service manager, want skipped:\n%+v", got, res.Stages)
	}
	d := res.Stages[0].Detail
	for _, want := range []string{"windows", "not supported"} {
		if !strings.Contains(d, want) {
			t.Errorf("the one line does not say %q, so the reader cannot tell why the machine has no unit: %q", want, d)
		}
	}
	if len(res.Left) != 0 {
		t.Errorf("the step left something for the user: %v", res.Left)
	}
	if askedSomething(out) {
		t.Errorf("the step asked a question:\n%s", out)
	}
	if _, err := os.Stat(currentUnit(t).Path); !os.IsNotExist(err) {
		t.Errorf("a platform with no service manager got a unit (stat: %v)", err)
	}
	if len(f.reloads) != 0 {
		t.Errorf("a platform with no service manager reached one: %v", f.reloads)
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

// TestUpdateRerendersAUnitThatNamesAnotherPathOfTheSameInstall: the render
// carries the stable binary path, so a unit written under another prefix — a
// moved Homebrew installation, a machine whose fugaro moved — is one the step
// re-renders, and the plan line says which two paths it is choosing between.
func TestUpdateRerendersAUnitThatNamesAnotherPathOfTheSameInstall(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	installed := releaseUpdate(t)
	e, _ := updateUnitEngine(t, &initOptions{})
	u := currentUnit(t)
	moved := "/opt/homebrew/opt/fugaro/bin/fugaro"
	path := unitFileAt(t, strings.ReplaceAll(u.Text, installed, moved), time.Now())
	res, out := runUnitStage(t, e, newUpdateUnitStage(e))
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
	for _, want := range []string{moved, installed} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan line does not name %s, so the person cannot see which program the unit is being pointed at:\n%s", want, out)
		}
	}
}

// TestUpdateNeverRepointsAUnitAtABuildThatWillNotBeThereTomorrow: a unit runs its
// program at every login and restarts it when it stops, so the one path a
// re-render may not write is the binary this update happens to be running — a
// checkout's `go build`, `go run`'s temporary binary, a copy in a scratch
// directory. The step writes nothing, leaves the installed unit exactly as it
// was, and names both paths and the command that points the unit at another
// program deliberately. This is the hazard the review of this work named: the
// unit would otherwise be re-pointed at a temporary binary that a `go clean`, a
// reboot or a checkout's deletion takes away, and both doors compare the file's
// text, so nothing would ever repair it.
func TestUpdateNeverRepointsAUnitAtABuildThatWillNotBeThereTomorrow(t *testing.T) {
	// The two shapes a person runs an update *from* without installing one, one
	// per clause of the rule: a `dev` build, wherever it was built, and a build
	// in the system's temporary directory, whatever version it carries. Both
	// paths are literal, so the test says what it means on any machine — the
	// test binary's own path is neither (it is a `dev` build inside the
	// temporary directory, which only one of the two clauses would catch).
	for _, tc := range []struct {
		name, version, exe string
	}{
		{"a development build in a checkout", "dev", "/home/someone/src/fugaro/fugaro"},
		{"a build in the temporary directory", "1.2.3", filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "fugaro")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unitHome(t)
			f := fakeUnitService(t)
			typed(t)
			oldVersion, oldExe := Version, orchestratorExecutable
			Version, orchestratorExecutable = tc.version, func() (string, error) { return tc.exe, nil }
			t.Cleanup(func() { Version, orchestratorExecutable = oldVersion, oldExe })
			e, _ := updateUnitEngine(t, &initOptions{})
			installed := "/opt/homebrew/opt/fugaro/bin/fugaro"
			u := currentUnit(t)
			want := strings.ReplaceAll(u.Text, tc.exe, installed)
			path := unitFileAt(t, want, time.Now())
			res, out := runUnitStage(t, e, newUpdateUnitStage(e))
			if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
				t.Fatalf("the step is %s running from %s (version %s), want skipped:\n%+v", got, tc.exe, tc.version, res.Stages)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != want {
				t.Errorf("the step wrote the unit (err %v):\n%s", err, data)
			}
			if len(f.reloads) != 0 {
				t.Errorf("the step reloaded the service although it wrote nothing: %v", f.reloads)
			}
			if len(res.Left) != 0 {
				t.Errorf("the step left something for the user: %v", res.Left)
			}
			if askedSomething(out) {
				t.Errorf("the step asked a question:\n%s", out)
			}
			for _, want := range []string{installed, tc.exe, "orchestrator install"} {
				if !strings.Contains(out, want) {
					t.Errorf("the plan line does not name %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestUpdateWritesNoFirstUnitFromABuildThatWillNotBeThereTomorrow is the same
// rule one step earlier, where the machine has NO login unit at all: D2700 has
// the update door write a machine's first one, and this is the guard that keeps
// that write from naming a program that will not be there tomorrow. An update run
// from a checkout's `go build`, from `go run`'s temporary binary or from a copy in
// a scratch directory would otherwise leave a login unit restarting a program a
// `go clean`, a reboot or a deleted checkout takes away — and, both doors
// comparing the file's text, nothing would ever repair it. Nothing is written,
// the machine is left exactly as it was for a released fugaro's own update to
// install from, and the plan line names the binary this run is and the command
// that writes the first unit deliberately.
func TestUpdateWritesNoFirstUnitFromABuildThatWillNotBeThereTomorrow(t *testing.T) {
	// The two shapes a person runs an update *from* without installing one, one
	// per clause of unitMayNameThisBinary: a `dev` build wherever it sits, and a
	// build in the system's temporary directory, whatever version it carries.
	for _, tc := range []struct {
		name, version, exe string
	}{
		{"a development build in a checkout", "dev", "/home/someone/src/fugaro/fugaro"},
		{"a build in the temporary directory", "1.2.3", filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "fugaro")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unitHome(t)
			f := fakeUnitService(t)
			typed(t)
			oldVersion, oldExe := Version, orchestratorExecutable
			Version, orchestratorExecutable = tc.version, func() (string, error) { return tc.exe, nil }
			t.Cleanup(func() { Version, orchestratorExecutable = oldVersion, oldExe })
			e, _ := updateUnitEngine(t, &initOptions{})
			u := currentUnit(t)
			res, out := runUnitStage(t, e, newUpdateUnitStage(e))
			if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
				t.Fatalf("the step is %s running from %s (version %s) with no unit on the machine, want skipped:\n%+v", got, tc.exe, tc.version, res.Stages)
			}
			if d := res.Stages[0].Detail; !strings.Contains(d, "leaves this machine without the login unit at "+u.Path) || !strings.Contains(d, tc.exe) {
				t.Errorf("the skip does not name the machine left without a unit and the program this run is: %q", d)
			}
			if _, err := os.Stat(u.Path); !os.IsNotExist(err) {
				t.Errorf("the step wrote a first unit from %s (stat: %v)", tc.exe, err)
			}
			if len(f.reloads) != 0 {
				t.Errorf("the step reloaded the service although it wrote nothing: %v", f.reloads)
			}
			if len(res.Left) != 0 {
				t.Errorf("the step left something for the user: %v", res.Left)
			}
			if askedSomething(out) {
				t.Errorf("the step asked a question:\n%s", out)
			}
			for _, want := range []string{u.Path, tc.exe, "orchestrator install", "nothing was written here"} {
				if !strings.Contains(out, want) {
					t.Errorf("the plan line does not name %q:\n%s", want, out)
				}
			}
		})
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
	releaseUpdate(t)
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
	releaseUpdate(t)
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
	releaseUpdate(t)
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
	releaseUpdate(t)
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
