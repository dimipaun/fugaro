package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	cleanup, err := PrepareFakeCommands()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// The regression: a fake gh written as a fresh executable by each test failed
// the watch PR-state tests on a loaded macOS machine (the first exec of a new
// file is checked, and took longer than watch's 3s gh timeout) and, once, on
// Linux CI (ETXTBSY: a concurrently forked child still held the write
// descriptor). A fake command must therefore never be an executable the test
// just wrote: it is a link to the one wrapper TestMain already ran, and the
// test's own file is only ever read.
func TestFakeCommandWritesNoExecutable(t *testing.T) {
	calls := FakeCommand(t, "gh", "echo OPEN\n")
	dir := filepath.Dir(calls)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				t.Fatal(err)
			}
			if target != fakeCmdPath {
				t.Errorf("%s links to %s, want the prepared wrapper %s", e.Name(), target, fakeCmdPath)
			}
			continue
		}
		if fi.Mode().Perm()&0o111 != 0 {
			t.Errorf("%s is an executable the test wrote (%v): its first exec is what flaked", e.Name(), fi.Mode())
		}
	}
	if fi, err := os.Lstat(filepath.Join(dir, "gh")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("gh must be a link to the prepared wrapper: %v %v", fi, err)
	}
}

// The fake still behaves as a command: found on PATH, its arguments reach the
// script as $1..., and every call is recorded in order.
func TestFakeCommandRunsScriptAndRecordsArgv(t *testing.T) {
	calls := FakeCommand(t, "gh", "case \"$3\" in 1) echo MERGED ;; *) echo \"OPEN $#\" ;; esac\nexit 3\n")
	bin, err := exec.LookPath("gh")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(bin) != filepath.Dir(calls) {
		t.Fatalf("LookPath found %s, not the fake", bin)
	}
	for _, tc := range []struct{ n, want string }{{"1", "MERGED\n"}, {"2", "OPEN 4\n"}} {
		out, err := exec.Command(bin, "pr", "view", tc.n, "--json").Output()
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
			t.Fatalf("exit: %v, want status 3 from the script", err)
		}
		if string(out) != tc.want {
			t.Errorf("gh pr view %s printed %q, want %q", tc.n, out, tc.want)
		}
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(b)), "pr view 1 --json\npr view 2 --json"; got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}
