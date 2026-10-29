// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	fcli "github.com/dimipaun/fugaro/internal/cli"
)

// `coordinator status` is a recency-windowed read: it lists the runs of a
// repository's bucket over the default window, measured back from runs ls's
// clock. The two tests here are the case the core's own guard carried for it
// (`{cmd: "coordinator status", window: lsDefaultSince, seam: lsNow, …}`, in
// internal/cli/runid_test.go's recencyWindows) — the command and its tests moved
// to this package in E2.1b, so the entry could only fail there as stale
// (runid_test.go's own guard fails when no test in its package drives an entry's
// command) and the case moved with the code. The core's guard still scans the
// core's tests; this is what holds the moved half.

// TestCoordinatorStatusCarriesRunsLssWindow: the window is the core's own
// lsDefaultSince, not a copy that could drift from runs ls's, and it is
// measured back from the core's ls clock (fcli.LsNow). A window kept as a
// literal here, or measured from time.Now(), would list a different set of runs
// than `runs ls` does on the same bucket, which is the drift this pins.
func TestCoordinatorStatusCarriesRunsLssWindow(t *testing.T) {
	if got, want := coordinatorDefaultSince, fcli.LsDefaultSince(); got != want {
		t.Errorf("coordinator status's default window = %q, want runs ls's %q (fcli.LsDefaultSince)", got, want)
	}
	window, err := fcli.ParseSince(coordinatorDefaultSince)
	if err != nil {
		t.Fatalf("fcli.ParseSince(%q): %v", coordinatorDefaultSince, err)
	}
	if window <= 0 {
		t.Fatalf("the default window %q reads as %v: a bound that is not a bound", coordinatorDefaultSince, window)
	}
	// The instant is now minus the window, off the core's clock. The tolerance
	// is for the reading itself — the two calls are a moment apart — not for a
	// different clock: a time.Now() here would be the same instant too, which is
	// why the clock's identity is pinned by the minting rule below rather than
	// by this comparison.
	if got, want := coordinatorSince(window), fcli.LsNow().Add(-window); got.Sub(want) > time.Minute || want.Sub(got) > time.Minute {
		t.Errorf("coordinatorSince(%v) = %v, want the window back from the core's ls clock, %v", window, got, want)
	}
	if got := coordinatorSince(0); !got.IsZero() {
		t.Errorf("coordinatorSince(0) = %v, want the zero instant: --since 0 is no bound at all", got)
	}
}

// runIDLit is the run-ID shape a fixed stamp takes: `fugaro run` writes the
// mint time into the ID, which is what a dated literal is.
var runIDLit = regexp.MustCompile(`\b\d{8}-\d{6}-[0-9a-f]{4}\b`)

// runIDAllow marks a fixed ID that is deliberate and cannot age out: the same
// marker the core's guard reads, so a test that moves keeps its exemption.
const runIDAllow = "runid-guard:allow"

// sinceAbsRE matches an invocation that opens the window with an ABSOLUTE
// --since (an instant the test chose) rather than the default one. A relative
// span ("7d") is the same window off the same clock and opens nothing.
var sinceAbsRE = regexp.MustCompile(`"--since",\s*"(?:0|\d{4}-\d{2}-\d{2})`)

// TestNoFixedRunIDsInTheMovedTests is the anti-date-luck rule for this
// package's own tests, in the shape the moved command needs: a test that drives
// `coordinator status` without opening the window must not hard-code a run ID,
// because the default window ages such an ID out of the view and the test then
// fails on a date nobody chose. Such a test mints its IDs from the same clock
// (recentRunID, this package's fixture), which is what makes it pass tomorrow.
//
// A fixed ID is exempt when its line carries the runid-guard:allow marker, and
// a test is exempt when its own body opens the window with an absolute --since
// — the two exemptions the core's guard also carries, and the reason its
// remaining literal stamps (the fixture seeds at a fixed 2026-10-13) pass.
func TestNoFixedRunIDsInTheMovedTests(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no test sources found: the guard would pass vacuously")
	}
	fset := token.NewFileSet()
	drivers, checked := 0, 0
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lines := strings.Split(string(src), "\n")
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			start, end := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
			if end > len(lines) {
				end = len(lines)
			}
			body := lines[start-1 : end]
			text := strings.Join(body, "\n")
			if !strings.Contains(text, `"coordinator", "status"`) && !strings.Contains(text, `"status"`) {
				continue // not a test of this command
			}
			drivers++
			if sinceAbsRE.MatchString(text) {
				continue // the window is the test's own instant
			}
			for i, line := range body {
				if strings.Contains(line, runIDAllow) {
					continue
				}
				if m := runIDLit.FindString(line); m != "" {
					checked++
					t.Errorf("%s:%d: %s hard-codes the run ID %s in a test that drives coordinator status without an absolute --since: the default window ages it out, so this test passes only until the clock reaches it — mint the ID from the same clock (recentRunID), open the window, or mark the line // %s", name, start+i, fn.Name.Name, m, runIDAllow)
				}
			}
		}
	}
	if drivers == 0 {
		t.Fatal("no test in this package drives coordinator status: the guard would pass vacuously")
	}
}
