// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// `fugaro report` is a windowed read: its --since/--until window is measured
// back from the budget commands' clock, and its tests pin that clock. The two
// tests here are the case the core's own guard carried for it
// (`{cmd: "report", window: reportDefaultSince, seam: budgetNow, …}`, in
// internal/cli/runid_test.go's recencyWindows) — the command and its tests
// moved to this package in E2.2b, so the entry could only fail there as stale
// (runid_test.go's own guard fails when no test in its package drives an
// entry's command) and the case moved with the code. The core's guard still
// scans the core's tests; this is what holds the moved half.

// TestReportWindowFollowsTheCommandsOwnClock: the window is resolved against
// the clock the command was built with — the core's budget clock in
// production, which fcli.BudgetNow answers and NewReportCmd installs — and not
// against a wall clock of its own. A command reading time.Now() here would put
// a run of the last minutes in no row on the machine that reads it and in a row
// on the one that reads a moment later, which is the drift this pins.
func TestReportWindowFollowsTheCommandsOwnClock(t *testing.T) {
	if got := NewReportCmd().Flags().Lookup("since").DefValue; got != reportDefaultSince {
		t.Errorf("report's default --since = %q, want %q", got, reportDefaultSince)
	}
	// The default window, resolved by reportRange off a fixed clock: 30 days
	// back from that day, both ends on it.
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	from, to, err := reportRange(reportDefaultSince, "", budget.Day(at))
	if err != nil {
		t.Fatal(err)
	}
	if to != budget.Day(at) || to-from+1 != 30 {
		t.Errorf("reportRange(%q) = %s..%s, want the 30 days up to %s", reportDefaultSince, budget.DayDate(from), budget.DayDate(to), at.Format("2006-01-02"))
	}

	// And the whole command: a tree built with this clock resolves `--since 1d`
	// to this clock's day, whatever the real date is.
	newCloudFixture(t)
	old := reportDeps.Now
	reportDeps.Now = func() time.Time { return at }
	t.Cleanup(func() { reportDeps.Now = old })
	out, _, err := execute(t, "report", "--json", "--since", "1d")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Since string `json:"since"`
		Until string `json:"until"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	if doc.Since != "2026-10-20" || doc.Until != "2026-10-20" {
		t.Errorf("report --since 1d under a 2026-10-20 clock = %s..%s, want that one day: the command read a clock of its own", doc.Since, doc.Until)
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
// `report` without opening the window must not hard-code a run ID, because the
// default window ages such an ID out of the view and the test then fails on a
// date nobody chose. Such a test mints its IDs from the same clock, which is
// what makes it pass tomorrow.
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
			if !strings.Contains(text, `"report"`) {
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
					t.Errorf("%s:%d: %s hard-codes the run ID %s in a test that drives report without an absolute --since: the default window ages it out, so this test passes only until the clock reaches it — mint the ID from the same clock, open the window, or mark the line // %s", name, start+i, fn.Name.Name, m, runIDAllow)
				}
			}
		}
	}
	if drivers == 0 {
		t.Fatal("no test in this package drives report: the guard would pass vacuously")
	}
}
