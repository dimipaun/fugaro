package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// XDG_CONFIG_HOME or XDG_CACHE_HOME inside the operating system's temporary
// directory is warned about, by name and path; unset, or elsewhere, never.
func TestTempXDGWarnings(t *testing.T) {
	tmp := filepath.Join(os.TempDir(), "leftover")
	for name, tc := range map[string]struct {
		config, cache string
		want          []string // the variable each warning names, in order
	}{
		"both unset":      {},
		"both elsewhere":  {config: "/home/u/.config", cache: "/home/u/.cache"},
		"config in temp":  {config: tmp, want: []string{"XDG_CONFIG_HOME"}},
		"cache in temp":   {cache: tmp, want: []string{"XDG_CACHE_HOME"}},
		"both in temp":    {config: tmp, cache: filepath.Join(tmp, "c"), want: []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME"}},
		"the temp dir":    {config: os.TempDir(), want: []string{"XDG_CONFIG_HOME"}},
		"a sibling name":  {config: "/tmpx/cfg", cache: "/home/u/.cache"},
		"relative, unset": {config: "relative/dir"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.config)
			t.Setenv("XDG_CACHE_HOME", tc.cache)
			got := tempXDGProblems(os.Getenv)
			if len(got) != len(tc.want) {
				t.Fatalf("%q, want one for each of %v", got, tc.want)
			}
			for i, v := range tc.want {
				p := got[i]
				if !strings.Contains(p, v) || !strings.Contains(p, os.Getenv(v)) || !strings.Contains(p, "unset it if you did not mean this") {
					t.Errorf("%q: want %s, its path and the advice", p, v)
				}
			}
		})
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{"/var/folders/ab/cd/T/x", "/private/var/folders/ab/cd/T/x"} {
			t.Setenv("XDG_CONFIG_HOME", p)
			t.Setenv("XDG_CACHE_HOME", "")
			if got := tempXDGProblems(os.Getenv); len(got) != 1 {
				t.Errorf("%s: %q", p, got)
			}
		}
	}
}

// init prints it at its start, into the result's warnings (so --json has it);
// doctor lists it as an information-severity check.
func TestTempXDGInInitAndDoctor(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.TempDir(), "leftover"))
	t.Setenv("XDG_CACHE_HOME", "/home/u/.cache")
	e, out := stageEngine(t, "", &initOptions{})
	e.r.warnTempXDG()
	if len(e.r.res.Warnings) != 1 || !strings.Contains(e.r.res.Warnings[0], "XDG_CONFIG_HOME") || !strings.Contains(out.String(), "warning: ") {
		t.Fatalf("warnings %q, output %q", e.r.res.Warnings, out.String())
	}
	cs := tempXDGChecks(os.Getenv)
	if len(cs) != 1 || cs[0].OK || cs[0].Severity != "info" || cs[0].ID != "xdg-temp-dir" || !strings.Contains(cs[0].Problem, "XDG_CONFIG_HOME") {
		t.Fatalf("checks %+v", cs)
	}
	if checksFail(cs, true) {
		t.Error("an information line fails doctor")
	}
	// --print-vars keeps stdout the tfvars alone.
	e, out = stageEngine(t, "", &initOptions{printVars: true})
	e.r.warnTempXDG()
	if out.String() != "" {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
	t.Setenv("XDG_CONFIG_HOME", "/home/u/.config")
	e, _ = stageEngine(t, "", &initOptions{})
	e.r.warnTempXDG()
	if len(e.r.res.Warnings) != 0 || len(tempXDGChecks(os.Getenv)) != 0 {
		t.Error("warned about a directory outside the temporary one")
	}
}
