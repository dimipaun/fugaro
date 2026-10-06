package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// tempXDGVars are the variables that say where the config and the cache are.
var tempXDGVars = []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME"}

// tempRoots are the operating system's temporary directories: os.TempDir()
// and, on macOS, the /var/folders trees it lives in (also through /private).
func tempRoots() []string {
	roots := []string{filepath.Clean(os.TempDir())}
	if runtime.GOOS == "darwin" {
		roots = append(roots, "/var/folders", "/private/var/folders")
	}
	if runtime.GOOS != "windows" {
		roots = append(roots, "/tmp", "/private/tmp", "/var/tmp")
	}
	return roots
}

// inTempDir says whether the absolute path p is a temporary directory or
// inside one.
func inTempDir(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	p = filepath.Clean(p)
	for _, r := range tempRoots() {
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// tempXDGProblems says, for each of XDG_CONFIG_HOME and XDG_CACHE_HOME that
// points into the temporary directory, what is wrong: what is written there
// is easily lost (a live installation's config once went into a leftover
// one).
func tempXDGProblems(getenv func(string) string) []string {
	var out []string
	for _, v := range tempXDGVars {
		if p := getenv(v); inTempDir(p) {
			out = append(out, fmt.Sprintf("%s is %s, inside the temporary directory: what fugaro writes there is easily lost", v, p))
		}
	}
	return out
}

const tempXDGAdvice = "unset it if you did not mean this"

// tempXDGChecks are tempXDGProblems as doctor's information lines.
func tempXDGChecks(getenv func(string) string) []doctorCheck {
	var out []doctorCheck
	for _, p := range tempXDGProblems(getenv) {
		out = append(out, doctorCheck{ID: "xdg-temp-dir", Severity: "info", Problem: p, Fix: tempXDGAdvice})
	}
	return out
}

// warnTempXDG warns about them at the start of init. --print-vars keeps
// stdout the tfvars alone, so its warning goes to stderr.
func (r *initRun) warnTempXDG() {
	for _, p := range tempXDGProblems(os.Getenv) {
		p += "; " + tempXDGAdvice
		if r.o.printVars {
			fmt.Fprintln(r.cmd.ErrOrStderr(), "warning: "+p)
			continue
		}
		r.warn(p)
	}
}
