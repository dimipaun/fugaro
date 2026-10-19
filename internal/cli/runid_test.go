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
