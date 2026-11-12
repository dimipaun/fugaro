package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/service"
)

// The unit's own command tests. Everything here runs against a temporary home
// and a fake service manager: the real one would run launchctl or systemctl,
// and no test may, so the interface is what is replaced (orchestratorService)
// and the seam the two converge steps share (orchestratorReload) is only ever
// told about a unit, never given a command to run.

// unitFake is a service.Manager that counts instead of doing: the two calls
// that would reach a machine's own service manager, and nothing else.
type unitFake struct {
	platform string
	reloads  []service.Unit
	removes  []service.Unit
	// reloadErr is the manager refusing to read the file, which is the one
	// state a written unit can be left in.
	reloadErr error
	// removeErr is the manager refusing to unload it: the one answer that has
	// to stop the file's deletion.
	removeErr error
}

func (f *unitFake) Platform() string { return f.platform }

func (f *unitFake) Render(func(string) string, string) (service.Unit, error) {
	return service.Unit{}, errors.New("the fake renders nothing: every test comes through service.Render")
}

func (f *unitFake) Reload(_ context.Context, u service.Unit) error {
	f.reloads = append(f.reloads, u)
	return f.reloadErr
}

func (f *unitFake) Remove(_ context.Context, u service.Unit) error {
	f.removes = append(f.removes, u)
	return f.removeErr
}

// fakeUnitService puts the fake behind the command family's own seam.
func fakeUnitService(t *testing.T) *unitFake {
	t.Helper()
	f := &unitFake{platform: "test"}
	old := orchestratorService
	orchestratorService = func() (service.Manager, error) { return f, nil }
	t.Cleanup(func() { orchestratorService = old })
	return f
}

// unitHome points HOME and the three XDG directories at dir, and returns dir:
// the unit's path on Linux is XDG_CONFIG_HOME/systemd/user/<name>.service, and
// a test that walked the developer's own home would be reading their machine.
func unitHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "home", "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "home", "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "home", "state"))
	return dir
}

// currentUnit is the unit this release renders on this machine, and where it
// goes: what a test compares the file on disk with, computed through the same
// renderer the command uses.
func currentUnit(t *testing.T) service.Unit {
	t.Helper()
	// Through the seam, not os.Executable: a test that says which binary this
	// process is (releaseUpdate, the update door's rule) has to get the unit the
	// command renders for it, and not the one the test binary would render.
	exe, err := orchestratorExecutable()
	if err != nil {
		t.Fatal(err)
	}
	u, err := service.Render(os.Getenv, runtime.GOOS, stableBinaryPath(exe))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestTheClosedStdinWorldHasNoLoginUnit: the harness's world isolates the XDG
// directories, which is where Linux puts the unit, and macOS puts it under the
// person's own home — so a world that isolates only the first would hand the
// harness's uninstall leaf the developer's real unit and delete it. This pins
// the world itself, on both platforms, rather than the one leaf that reads it.
func TestTheClosedStdinWorldHasNoLoginUnit(t *testing.T) {
	real, err := os.UserHomeDir()
	if err != nil {
		real = ""
	}
	closedStdinWorld(t)
	old := orchestratorGOOS
	t.Cleanup(func() { orchestratorGOOS = old })
	for _, platform := range []string{"darwin", "linux"} {
		orchestratorGOOS = platform
		u, err := orchestratorUnit()
		if err != nil {
			t.Fatalf("the world cannot render its own unit on %s: %v", platform, err)
		}
		if real != "" && strings.HasPrefix(u.Path, real+string(filepath.Separator)) {
			t.Errorf("the closed-stdin world's %s unit is at %s, under the developer's own home: a leaf would read this machine rather than the world",
				platform, u.Path)
		}
		was, ok, err := unitTextOnDisk(u)
		if err != nil {
			t.Fatalf("reading %s on %s: %v", u.Path, platform, err)
		}
		if ok {
			t.Errorf("the closed-stdin world already has a login unit at %s on %s (%d bytes), so a leaf would read the machine rather than the world",
				u.Path, platform, len(was))
		}
	}
}

// TestInstallWritesOnlyUnderTheUserHome: the unit's file is the person's own,
// under their own home, and the run writes nothing anywhere else.
func TestInstallWritesOnlyUnderTheUserHome(t *testing.T) {
	dir := unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	out, _, err := executeStdin(t, "install\n", "orchestrator", "install")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	u := currentUnit(t)
	if !strings.HasPrefix(u.Path, filepath.Join(dir, "home")) {
		t.Fatalf("the unit's path %s is not under the person's home", u.Path)
	}
	data, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatalf("the unit was not written: %v", err)
	}
	if string(data) != u.Text {
		t.Errorf("%s is not what this release renders:\n--- on disk\n%s\n--- rendered\n%s", u.Path, data, u.Text)
	}
	if !strings.Contains(out, u.Path) {
		t.Errorf("the run does not name the file it writes:\n%s", out)
	}
	// Every file this run left, anywhere, is under the person's home: the
	// property the whole command family is built on — no system path, no
	// root, nothing outside ~.
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasPrefix(p, filepath.Join(dir, "home")) {
			t.Errorf("the run wrote %s, which is outside the person's home", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reloads) != 1 || f.reloads[0].Path != u.Path {
		t.Errorf("the service manager was reloaded with %v, want one call for %s", f.reloads, u.Path)
	}
	if len(f.removes) != 0 {
		t.Errorf("install removed something: %v", f.removes)
	}
}

// TestInstallNamesTheCommandThatLoadsAUnitTheManagerRefused: the file is written
// before the manager is asked to read it, so a reload that fails leaves a unit
// that is this release's own text — and that is the one state neither converge
// step repairs, because the text on disk is current and a reload with nothing to
// reload is exactly what keeps a repeated `fugaro update` quiet (T7). The
// failure is the only place that state is named, so it names the command that
// loads the file whatever its text.
func TestInstallNamesTheCommandThatLoadsAUnitTheManagerRefused(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	f.reloadErr = errors.New("the manager refused")
	typed(t)
	out, _, err := executeStdin(t, "install\n", "orchestrator", "install")
	if err == nil {
		t.Fatalf("install succeeded although the manager refused to load the unit:\n%s", out)
	}
	if !strings.Contains(err.Error(), "orchestrator install") {
		t.Errorf("the failure does not name the command that loads the file again: %v", err)
	}
	if !strings.Contains(err.Error(), currentUnit(t).Path) {
		t.Errorf("the failure does not name the file it left behind: %v", err)
	}
	if data, readErr := os.ReadFile(currentUnit(t).Path); readErr != nil || string(data) != currentUnit(t).Text {
		t.Errorf("the written unit is not what this release renders (read %v):\n%s", readErr, data)
	}
}

func TestInstallRefusedUnderAnAgentMarker(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	t.Setenv("CLAUDECODE", "1")
	out, _, err := executeStdin(t, "install\n", "orchestrator", "install")
	if err == nil {
		t.Fatalf("install under a coding agent's marker succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	if _, statErr := os.Stat(currentUnit(t).Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a unit was written under a coding agent's marker (stat: %v)", statErr)
	}
	if len(f.reloads) != 0 || len(f.removes) != 0 {
		t.Errorf("a coding agent's session reached the service manager: reloads %v, removes %v", f.reloads, f.removes)
	}
}

// TestWindowsIsRefusedNamed: Windows has no launchd and no systemd user unit
// (D1232), and the refusal names the platform rather than failing somewhere
// later with a path or a missing command.
func TestWindowsIsRefusedNamed(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	old := orchestratorGOOS
	orchestratorGOOS = "windows"
	t.Cleanup(func() { orchestratorGOOS = old })
	for _, cmd := range [][]string{
		{"orchestrator", "install"},
		{"orchestrator", "uninstall"},
		{"orchestrator", "status"},
	} {
		out, _, err := executeStdin(t, "install\n", cmd...)
		if err == nil {
			t.Fatalf("%v on windows succeeded:\n%s", cmd, out)
		}
		for _, want := range []string{"windows", "not supported"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: the refusal does not name %q: %v", cmd, want, err)
			}
		}
	}
	if len(f.reloads) != 0 || len(f.removes) != 0 {
		t.Errorf("a refused platform reached the service manager: %v %v", f.reloads, f.removes)
	}
}

func TestUninstallRemovesWhatInstallWrote(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	if _, _, err := executeStdin(t, "install\n", "orchestrator", "install"); err != nil {
		t.Fatal(err)
	}
	u := currentUnit(t)
	statusOut, _, err := execute(t, "orchestrator", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(statusOut, "is installed and current") {
		t.Errorf("status does not report an installed, current unit:\n%s", statusOut)
	}
	out, _, err := execute(t, "orchestrator", "uninstall")
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, statErr := os.Stat(u.Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the unit is still there after uninstall (stat: %v)", statErr)
	}
	if len(f.removes) != 1 || f.removes[0].Path != u.Path {
		t.Errorf("the service manager was told to remove %v, want one call for %s", f.removes, u.Path)
	}
	if !strings.Contains(out, u.Path) {
		t.Errorf("uninstall does not name the file it removed:\n%s", out)
	}
	// A second uninstall says there is nothing to remove rather than failing,
	// and a machine that never had a unit is the same answer.
	out, _, err = execute(t, "orchestrator", "uninstall")
	if err != nil {
		t.Fatalf("uninstall with no unit: %v", err)
	}
	if !strings.Contains(out, "nothing to remove") {
		t.Errorf("uninstall with no unit does not say so:\n%s", out)
	}
	if len(f.removes) != 1 {
		t.Errorf("uninstall with no unit reached the service manager: %v", f.removes)
	}
	statusOut, _, err = execute(t, "orchestrator", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(statusOut, "is not installed") {
		t.Errorf("status does not report the missing unit:\n%s", statusOut)
	}
}

// TestUninstallLeavesTheFileWhenTheManagerRefusesToUnload: the manager is asked
// before the file goes, and a manager that reports the unload as failed is
// believed — the unit may still be loaded, so the file that names it stays where
// it is and the run says why. Deleting it first would take away the only thing
// that names the daemon still running.
func TestUninstallLeavesTheFileWhenTheManagerRefusesToUnload(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	if _, _, err := executeStdin(t, "install\n", "orchestrator", "install"); err != nil {
		t.Fatal(err)
	}
	u := currentUnit(t)
	f.removeErr = errors.New("the manager refused")
	out, _, err := execute(t, "orchestrator", "uninstall")
	if err == nil {
		t.Fatalf("uninstall succeeded although the manager refused to unload the unit:\n%s", out)
	}
	if !strings.Contains(err.Error(), "unloading the login unit") || !strings.Contains(err.Error(), u.Name) {
		t.Errorf("the failure does not name the unload that failed: %v", err)
	}
	if data, readErr := os.ReadFile(u.Path); readErr != nil || string(data) != u.Text {
		t.Errorf("the file was deleted or changed although the manager refused (read %v)", readErr)
	}
	if len(f.removes) != 1 {
		t.Errorf("the manager was asked to remove %v, want one call", f.removes)
	}
}

// TestInstallRerendersAndRestartsAPreviousVersionUnit: the previous release's
// template is on disk. One `fugaro orchestrator install` re-renders the file
// from this release's template and restarts the service, which is what the
// command promises to mean, and it is also what `fugaro update` does to an
// installed unit.
func TestInstallRerendersAndRestartsAPreviousVersionUnit(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	u := currentUnit(t)
	previous := "[Unit]\nDescription=Fugaro orchestrator (0.20.0)\n\n[Service]\nExecStart=/usr/local/bin/fugaro orchestrator run --binary /usr/local/bin/fugaro\n"
	if err := os.MkdirAll(filepath.Dir(u.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.Path, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "install\n", "orchestrator", "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != u.Text {
		t.Errorf("the unit was not re-rendered from this release's template:\n--- on disk\n%s\n--- rendered\n%s", data, u.Text)
	}
	if len(f.reloads) != 1 {
		t.Errorf("the service was reloaded %d times, want once: %v", len(f.reloads), f.reloads)
	}
	if !strings.Contains(out, "changed") {
		t.Errorf("the run does not say why it re-rendered:\n%s", out)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	u := currentUnit(t)
	if _, _, err := executeStdin(t, "install\n", "orchestrator", "install"); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "install\n", "orchestrator", "install")
	if err != nil {
		t.Fatalf("the second install: %v", err)
	}
	data, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != u.Text {
		t.Errorf("the second install left a different file:\n%s", data)
	}
	if len(f.reloads) != 2 {
		t.Errorf("two installs reloaded the service %d times, want each to: %v", len(f.reloads), f.reloads)
	}
	if !strings.Contains(out, "already this release's template") {
		t.Errorf("the second install does not report that nothing changed but the restart:\n%s", out)
	}
}

// TestInstallUnitPointsAtTheStableBinaryPath is the CLI's half of the path
// rule: the unit names the package manager's stable link and never a versioned
// Cellar path, and it names the stable path — not the running process's own
// resolved executable, which on Linux is the versioned path an upgrade deletes.
func TestInstallUnitPointsAtTheStableBinaryPath(t *testing.T) {
	for _, tc := range []struct{ exe, want string }{
		{"/opt/homebrew/Cellar/fugaro/1.2.3/bin/fugaro", "/opt/homebrew/opt/fugaro/bin/fugaro"},
		{"/usr/local/Cellar/fugaro/0.21.0/bin/fugaro", "/usr/local/opt/fugaro/bin/fugaro"},
		{"/home/linuxbrew/.linuxbrew/Cellar/fugaro/1.2.3/bin/fugaro", "/home/linuxbrew/.linuxbrew/opt/fugaro/bin/fugaro"},
		// Already stable, or not Homebrew's at all: used as it is, because
		// inventing a link would name a file nobody wrote.
		{"/opt/homebrew/opt/fugaro/bin/fugaro", "/opt/homebrew/opt/fugaro/bin/fugaro"},
		{"/usr/local/bin/fugaro", "/usr/local/bin/fugaro"},
		{"/home/someone/go/bin/fugaro", "/home/someone/go/bin/fugaro"},
		// A directory that merely carries the name is not a package manager's:
		// rewriting it would name an opt link nobody wrote, and the unit's
		// KeepAlive would restart a program that does not exist.
		{"/home/someone/Cellar/fugaro/1.2.3/bin/fugaro", "/home/someone/Cellar/fugaro/1.2.3/bin/fugaro"},
		{"/srv/builds/Cellar/fugaro/1.2.3/bin/fugaro", "/srv/builds/Cellar/fugaro/1.2.3/bin/fugaro"},
		// And a Homebrew at some other prefix is that case too: nothing here
		// can tell it from an ordinary directory, so its path is kept — the
		// unit is re-rendered from the running binary on every `fugaro update`,
		// which is what keeps it right.
		{"/opt/brew/Cellar/fugaro/1.2.3/bin/fugaro", "/opt/brew/Cellar/fugaro/1.2.3/bin/fugaro"},
		// A directory that merely carries the name is not a version: without
		// the version segment there is nothing to strip.
		{"/opt/homebrew/Cellar/fugaro/bin", "/opt/homebrew/Cellar/fugaro/bin"},
	} {
		if got := stableBinaryPath(tc.exe); got != tc.want {
			t.Errorf("stableBinaryPath(%s) = %s, want %s", tc.exe, got, tc.want)
		}
	}

	unitHome(t)
	fakeUnitService(t)
	typed(t)
	if _, _, err := executeStdin(t, "install\n", "orchestrator", "install"); err != nil {
		t.Fatal(err)
	}
	u := currentUnit(t)
	data, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stable := stableBinaryPath(exe)
	if n := strings.Count(string(data), stable); n != 2 {
		t.Errorf("the unit names %s %d times, want twice (the program and --binary):\n%s", stable, n, data)
	}
	if strings.Contains(string(data), "Cellar") {
		t.Errorf("the unit names a versioned Cellar path, which an upgrade removes:\n%s", data)
	}
}

// stageSource is one stage file's text, read from the package directory the
// tests run in.
func stageSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
