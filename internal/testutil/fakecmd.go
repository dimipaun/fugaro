package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A fake command (a `gh` a test puts first on PATH) used to be a fresh
// executable shell script written by each test. Two things make exec'ing a
// freshly written file flaky, both against a deadline the code under test
// enforces (internal/watch gives gh 3s):
//
//   - macOS checks every new executable on its first exec: 0.5-1s on an idle
//     machine, 3-4s on a loaded one. The lookup timed out, gh never ran, and
//     the test failed deterministically on a busy machine.
//   - Linux refuses to exec a file some process still holds open for writing
//     (ETXTBSY). A process forked by any other goroutine between the test's
//     write and its close inherits the descriptor, so the exec fails at once.
//
// So no test writes an executable. One wrapper is written and run once, in
// TestMain (PrepareFakeCommands), before any test can fork and while nothing
// has a deadline; a fake command is then a symlink to that wrapper beside a
// plain, never-exec'd file holding the test's script, which the wrapper
// sources. A symlink to a file already run costs no check, and a file that
// is only read cannot be busy.

// fakeCmdWrapper is the shared executable every fake command links to: it
// sources <dir>/<name>.sh, where <dir>/<name> is the path it was run as. $0
// is that path (the symlink's, not the wrapper's), and the positional
// parameters are passed through to the sourced script unchanged.
const fakeCmdWrapper = "#!/bin/sh\n. \"${0%/*}/${0##*/}.sh\"\n"

var fakeCmdPath string

// PrepareFakeCommands writes the shared wrapper and runs it once. Call it from
// TestMain, before m.Run, in any package that uses FakeCommand; the returned
// function removes it.
func PrepareFakeCommands() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "fugaro-fakecmd-")
	if err != nil {
		return nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	wrapper := filepath.Join(dir, "fakecmd")
	if err := os.WriteFile(wrapper, []byte(fakeCmdWrapper), 0o755); err != nil {
		cleanup()
		return nil, err
	}
	// Pay the first-exec check now, through a link, the way every test will.
	warm := filepath.Join(dir, "warm")
	if err := os.WriteFile(warm+".sh", []byte("echo warm\n"), 0o644); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Symlink(wrapper, warm); err != nil {
		cleanup()
		return nil, err
	}
	if out, err := exec.Command(warm).CombinedOutput(); err != nil || string(out) != "warm\n" {
		cleanup()
		return nil, fmt.Errorf("fake command wrapper does not run: %v: %q", err, out)
	}
	fakeCmdPath = wrapper
	return cleanup, nil
}

// FakeCommand puts a fake name first on PATH for the rest of the test. Its
// body is script, run by /bin/sh with the command's arguments as $1...; every
// invocation first appends its argv ("$*") as one line to the returned file,
// so a test can see both what ran and that nothing ran at all (the file is
// absent until the first call).
func FakeCommand(t *testing.T, name, script string) (calls string) {
	t.Helper()
	if fakeCmdPath == "" {
		t.Fatal("testutil.FakeCommand: call testutil.PrepareFakeCommands from TestMain first")
	}
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	body := "printf '%s\\n' \"$*\" >> '" + calls + "'\n" + script
	if err := os.WriteFile(filepath.Join(dir, name+".sh"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeCmdPath, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}
