package cli

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The scratch directories are temporary too, on every system but Windows;
// a sibling name is not.
func TestTempXDGScratchDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /tmp")
	}
	t.Setenv("XDG_CACHE_HOME", "")
	for p, want := range map[string]int{"/tmp/x": 1, "/private/tmp/x": 1, "/var/tmp/x": 1, "/tmp": 1, "/tmpx/cfg": 0, "/var/tmpx": 0} {
		t.Setenv("XDG_CONFIG_HOME", p)
		if got := tempXDGProblems(os.Getenv); len(got) != want {
			t.Errorf("%s: %q, want %d", p, got, want)
		}
	}
	// The line says it once: the advice is the fix, not the problem.
	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")
	cs := tempXDGChecks(os.Getenv)
	if len(cs) != 1 || strings.Contains(cs[0].Problem, "unset it") || !strings.HasSuffix(cs[0].Fix, "unset it if you did not mean this") {
		t.Errorf("checks %+v", cs)
	}
}

// A second project config written beside another gets one note; the first
// config, and a rewrite of an existing one, get none.
func TestSecondProjectNote(t *testing.T) {
	isolateProjects(t, t.TempDir())
	e, out := stageEngine(t, "", &initOptions{yes: true})
	r := e.r
	write := func(name string, old []byte) {
		t.Helper()
		lc, err := newProjectConfig(name, "proj-1234", "us-east5")
		if err != nil {
			t.Fatal(err)
		}
		path, _ := localcfg.ProjectPath(os.Getenv, name)
		if err := r.writeLocalConfig(lc, path, old, true); err != nil {
			t.Fatal(err)
		}
	}
	const note = "two project configs now exist; commands outside a checkout need FUGARO_PROJECT=<name> or --project <name>"
	write("aurora", nil)
	if strings.Contains(out.String(), note) {
		t.Fatalf("the first config got the note:\n%s", out.String())
	}
	write("borealis", nil)
	if n := strings.Count(out.String(), note); n != 1 {
		t.Fatalf("the second config: note %d times:\n%s", n, out.String())
	}
	path, _ := localcfg.ProjectPath(os.Getenv, "aurora")
	data, _ := os.ReadFile(path)
	write("aurora", data)
	if n := strings.Count(out.String(), note); n != 1 {
		t.Fatalf("a rewrite repeated the note: %d", n)
	}
}
