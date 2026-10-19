package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// recentRunID mints a run ID inside runs ls's default --since window: lsNow
// minus offset, stamped YYYYMMDD-HHMMSS with the 4-hex suffix runstore
// requires. A test whose listing is filtered by recency must mint its run
// IDs through this (or runIDAt, the same in whole days) rather than
// hard-code a date: a fixed 2026 date passes only until the 7-day window
// slides past it, and then fails on every commit by the calendar, not by a
// change. That is this repository's time bomb:
// TestLsNamedRunIsPendingNotError ran a 2026-10-12 run and ls dropped it on
// 2026-10-19. TestNoFixedRunIDsInRecencyTests guards the rule.
func recentRunID(t *testing.T, offset time.Duration, suffix string) string {
	t.Helper()
	return lsNow().UTC().Add(-offset).Format("20060102-150405") + "-" + suffix
}

// TestLsWindowFollowsTheClock proves ls's --since window is measured from
// lsNow, the seam recentRunID mints against, and not from a wall clock the
// tests cannot move: it mints a run and lists it with the clock 30 and 400
// days ahead, and 400 days behind, and gets it back every time. The last
// case is the discriminating one: a run 400 days old is dropped by the real
// clock, so only the injected clock could have listed it. A test that
// hard-codes its date cannot make this claim, which is why the guard above
// bans it.
func TestLsWindowFollowsTheClock(t *testing.T) {
	defer func(old func() time.Time) { lsNow = old }(lsNow)
	real := time.Now().UTC()
	listAt := func(t *testing.T, at time.Time) {
		t.Helper()
		f := newCloudFixture(t)
		f.run.Project, f.run.Region = "proj-1234", "us-east5"
		lsNow = func() time.Time { return at }
		id := recentRunID(t, time.Minute, "feed")
		seedRun(t, f, id, "", "someone@example.com", false)
		if got, errOut := lsJSON(t); len(got.Runs) != 1 || got.Runs[0].RunID != id {
			t.Fatalf("ls at %s = %+v, want %s listed (%q)", at.Format(time.RFC3339), got.Runs, id, errOut)
		}
	}
	t.Run("30 days ahead", func(t *testing.T) { listAt(t, real.Add(30*24*time.Hour)) })
	t.Run("400 days ahead", func(t *testing.T) { listAt(t, real.Add(400*24*time.Hour)) })
	t.Run("400 days behind", func(t *testing.T) { listAt(t, real.Add(-400*24*time.Hour)) })

	// The same 400-day-old run is dropped once the clock is real: what
	// listed it above was the injected clock, not luck.
	f := newCloudFixture(t)
	f.run.Project, f.run.Region = "proj-1234", "us-east5"
	lsNow = func() time.Time { return real.Add(-400 * 24 * time.Hour) }
	id := recentRunID(t, time.Minute, "feed")
	seedRun(t, f, id, "", "someone@example.com", false)
	lsNow = time.Now
	if got, _ := lsJSON(t); len(got.Runs) != 0 {
		t.Fatalf("ls at the real clock = %+v, want the 400-day-old run dropped", got.Runs)
	}
}

// TestNoFixedRunIDsInRecencyTests is the guard for recentRunID: it parses
// this package's own test sources and fails a test that both drives a
// default-window listing (runs ls / lsJSON / executeWatch with no
// --since) and hard-codes a run ID. Such a test is date-lucky: it passes
// until now − 7d passes the ID's stamp. A deliberate fixed ID (one the
// test needs to sit OUTSIDE the window, or a rendered row) is exempted
// with a // runid-guard:allow comment on its line.
func TestNoFixedRunIDsInRecencyTests(t *testing.T) {
	runIDLit := regexp.MustCompile(`\b\d{8}-\d{6}-[0-9a-f]{4}\b`)
	// A listing through the default --since window. A line carrying an
	// explicit --since is a window the test chose, not the clock's.
	defaultListing := regexp.MustCompile(`"runs", "ls"|lsJSON\(|executeWatch\(`)
	fset := token.NewFileSet()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no test sources found: the guard would pass vacuously")
	}
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lines := strings.Split(string(src), "\n")
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if fn.Name.Name == "TestNoFixedRunIDsInRecencyTests" {
				continue
			}
			start := fset.Position(fn.Pos()).Line
			end := fset.Position(fn.End()).Line
			listing := false
			var offenders []string
			for i := start; i <= end && i <= len(lines); i++ {
				line := lines[i-1]
				if defaultListing.MatchString(line) && !strings.Contains(line, "--since") {
					listing = true
				}
				if id := runIDLit.FindString(line); id != "" && !strings.Contains(line, "runid-guard:allow") {
					offenders = append(offenders, fmt.Sprintf("%s:%d: %s", name, i, id))
				}
			}
			if !listing {
				continue
			}
			for _, o := range offenders {
				t.Errorf("%s lists through the default --since window yet hard-codes a run ID, so it passes only until the window passes that date; mint it with recentRunID, or mark the line // runid-guard:allow:\n\t%s",
					fn.Name.Name, o)
			}
		}
	}
}
