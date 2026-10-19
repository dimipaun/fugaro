package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// recentRunID is runID with *testing.T's helper reporting: runID, in
// watch_queued_test.go, is the one mint, and it reads lsNow, the clock the
// listings read. A test whose listing is filtered by recency must mint its
// run IDs through one of them (or runIDAt, the same in whole days) rather
// than hard-code a date: a fixed 2026 date passes only until the window
// slides past it, and then fails on every commit by the calendar, not by a
// change. That is this repository's time bomb: TestLsNamedRunIsPendingNotError
// ran a 2026-10-12 run and ls dropped it on 2026-10-19.
// TestNoFixedRunIDsInRecencyTests guards the rule.
func recentRunID(t *testing.T, offset time.Duration, suffix string) string {
	t.Helper()
	return runID(offset, suffix)
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

// invocation is one command a test drives, as the command line's own
// arguments: execute(t, "run", "--pr", "7") is [run --pr 7], and the helpers
// that wrap a command (lsJSON, executeWatch, wantRefused) bring their
// command's own arguments with them. Reading arguments rather than source
// lines keeps a flag named in an expected-error string from counting as a
// flag passed, and follows an argument list split over several lines.
type invocation struct {
	line int
	args []string
}

// drives reports whether the invocation passes every argument of want, whole.
func (i invocation) drives(want ...string) bool {
	for _, a := range want {
		if !slices.Contains(i.args, a) {
			return false
		}
	}
	return true
}

// noSince reports whether the invocation chose its own window with --since.
func (i invocation) noSince() bool {
	for _, a := range i.args {
		if a == "--since" || strings.HasPrefix(a, "--since=") {
			return false
		}
	}
	return true
}

// reviewModePR is true for run --pr with a review-mode --recipe: reviewSpec
// reviews the named pull requests directly and never follows a PR's own runs,
// so the followUpLookback window is never read. The recipe names come from
// the production catalog, not from this guard.
func reviewModePR(i invocation) bool {
	for n, a := range i.args {
		if a == "--recipe" && n+1 < len(i.args) {
			if _, _, ok := recipe.ReviewPRRange(i.args[n+1]); ok {
				return true
			}
		}
	}
	return false
}

// recencyWindow is one entry point whose listing is filtered by a window
// measured back from the real clock, plus the production constant that holds
// the window. A test that drives one and hard-codes a run ID passes only
// until the window slides past that ID's date, and then fails on every commit
// by the calendar, not by a change: that is the time bomb #651 was (runs ls's
// default 7d window dropped TestLsNamedRunIsPendingNotError's 2026-10-12 run
// on 2026-10-19).
type recencyWindow struct {
	// cmd names the entry point in the guard's failure message.
	cmd string
	// window is the production constant the window lives in, so the guard
	// points at the code the window comes from.
	window string
	// run is the command line that drives the entry point, as whole
	// arguments. An invocation matching it is on the window's path unless
	// unless says otherwise.
	run []string
	// unless, when set, reports that this invocation takes a path reading no
	// window at all.
	unless func(invocation) bool
	// needs, when set, is a fact the test's body must also show for the
	// window to apply: a command's window is read on some of its paths only
	// (only a stored follow-up's --retry reads followUpLookback, and only a
	// runs bucket object has a mint time for watch to window).
	needs *regexp.Regexp
	// seam matches an assignment to the clock the window reads, as a test
	// pins it. nil when the code reads time.Now() directly: then no test can
	// put the window under its control, and a fixed ID here is always a bomb.
	seam *regexp.Regexp
	// since is true when the entry point takes an explicit --since: a test
	// passing one chose its window, so the clock cannot age it out.
	since bool
}

var recencyWindows = []recencyWindow{
	{cmd: "runs ls (default --since)", window: "lsDefaultSince", run: []string{"runs", "ls"}, seam: regexp.MustCompile(`lsNow\s*=`), since: true},
	{cmd: "runs ls --pr", window: "prLookback", run: []string{"runs", "ls", "--pr"}, seam: regexp.MustCompile(`lsNow\s*=`), since: true},
	// watch: the queued scan lists run IDs by mint time over queuedLookback
	// and finishedLookback (watch_queued.go), and the degraded view reads
	// degradedSince (watch.go). Both clocks are time.Now(); only the
	// scanner's own now field is a seam. Only runs bucket objects have a
	// mint time: the frame's agent rows come from the /agents registry,
	// which no window ages.
	{cmd: "watch", window: "queuedLookback/finishedLookback/degradedSince", run: []string{"watch"},
		needs: runsBucketWrite, seam: regexp.MustCompile(`\bnow\s*=`)},
	{cmd: "run --pr", window: "followUpLookback", run: []string{"run", "--pr"}, unless: reviewModePR},
	// run --retry reads followUpLookback only through recheckFollowUp, for a
	// stored task that is a follow-up (run.go's reused = spec.IsFollowUp);
	// any other retry reads its task by run ID and reads no window at all.
	{cmd: "run --retry (of a stored follow-up)", window: "followUpLookback", run: []string{"run", "--retry"},
		needs: regexp.MustCompile(`followUpSpec\(|prRecord\(|\bPR:\s*[1-9]`)},
	{cmd: "stats runs", window: "statsDefaultSince", run: []string{"stats", "runs"}, since: true},
	{cmd: "report", window: "reportDefaultSince", run: []string{"report"}, seam: regexp.MustCompile(`budgetNow\s*=`), since: true},
	{cmd: "budget preview", window: "previewHistoryWindow", run: []string{"budget", "preview"}, seam: regexp.MustCompile(`budgetNow\s*=`)},
}

// runsBucketWrite matches a call that puts a run in the runs bucket, whose ID
// therefore carries a mint time a windowed listing can age out.
var runsBucketWrite = regexp.MustCompile(`writeTask\(|writeClaim\(|writeRecord\(|seedSpec\(|seedRun\(|seedRoot\(|seedFollowUp\(|seedPRRuns\(|CreateTask\(|RequestCancel\(`)

// TestNoFixedRunIDsInRecencyTests is the guard for recentRunID: it parses
// this package's own test sources and fails a test that drives any
// recency-windowed entry point (recencyWindows: runs ls, watch, run --pr,
// run --retry, stats runs, report, budget preview) and hard-codes a run ID.
// Such a test is date-lucky: it passes until the clock's window passes the
// ID's stamp. A test that pins the window's own clock is not date-lucky (its
// window follows the pinned time), so the guard follows assignments to the
// seam into the fixtures those tests use. A deliberate fixed ID in a test
// that cannot pin the clock is exempted with a // runid-guard:allow comment
// on its line.
func TestNoFixedRunIDsInRecencyTests(t *testing.T) {
	runIDLit := regexp.MustCompile(`\b\d{8}-\d{6}-[0-9a-f]{4}\b`)
	fset := token.NewFileSet()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no test sources found: the guard would pass vacuously")
	}
	type decl struct {
		fn    *ast.FuncDecl
		file  string
		body  []string
		invs  []invocation
		calls []string
	}
	var decls []*decl
	byName := map[string][]*decl{}
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
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			start, end := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
			dc := &decl{fn: fn, file: name}
			for i := start; i <= end && i <= len(lines); i++ {
				dc.body = append(dc.body, lines[i-1])
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := c.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				dc.calls = append(dc.calls, id.Name)
				// A test drives a command through one of these helpers: the
				// command line is the call's string literals, with the
				// helper's own command in front.
				var cmd []string
				switch id.Name {
				case "execute", "wantRefused":
				case "lsJSON":
					cmd = []string{"runs", "ls"}
				case "executeWatch":
					cmd = []string{"runs", "ls", "--watch"}
				default:
					return true
				}
				for _, a := range c.Args {
					if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							cmd = append(cmd, v)
						}
					}
				}
				dc.invs = append(dc.invs, invocation{line: fset.Position(c.Pos()).Line, args: cmd})
				return true
			})
			decls = append(decls, dc)
			byName[fn.Name.Name] = append(byName[fn.Name.Name], dc)
		}
	}

	// pinned says whether a function puts seam w's clock under the test's
	// control, directly or through a function it calls (a fixture that pins
	// budgetNow, say). A test's window then follows the pinned clock, so its
	// fixed run IDs cannot age out.
	pinned := func(tested *decl, w recencyWindow) bool {
		if w.seam == nil {
			return false
		}
		seen := map[string]bool{tested.fn.Name.Name: true}
		queue := []*decl{tested}
		for len(queue) > 0 {
			d := queue[0]
			queue = queue[1:]
			for _, line := range d.body {
				if w.seam.MatchString(line) {
					return true
				}
			}
			for _, callee := range d.calls {
				if seen[callee] {
					continue
				}
				seen[callee] = true
				queue = append(queue, byName[callee]...)
			}
		}
		return false
	}

	// Every window must be one some test actually drives, or its pattern has
	// gone stale and the guard would pass over that family silently.
	driven := map[string]bool{}
	for _, d := range decls {
		for _, inv := range d.invs {
			for _, w := range recencyWindows {
				if inv.drives(w.run...) {
					driven[w.cmd] = true
				}
			}
		}
	}
	for _, w := range recencyWindows {
		if !driven[w.cmd] {
			t.Errorf("no test invocation drives %s (%v): the guard's pattern for its %s window is stale",
				w.cmd, w.run, w.window)
		}
	}

	for _, d := range decls {
		name := d.fn.Name.Name
		if !strings.HasPrefix(name, "Test") || name == "TestNoFixedRunIDsInRecencyTests" {
			continue
		}
		var windows []recencyWindow
		for _, w := range recencyWindows {
			if pinned(d, w) {
				continue
			}
			shown := w.needs == nil
			for _, line := range d.body {
				if w.needs != nil && w.needs.MatchString(line) {
					shown = true
				}
			}
			if !shown {
				continue
			}
			for _, inv := range d.invs {
				if !inv.drives(w.run...) || (w.unless != nil && w.unless(inv)) {
					continue
				}
				if w.since && !inv.noSince() {
					continue
				}
				windows = append(windows, w)
				break
			}
		}
		if len(windows) == 0 {
			continue
		}
		for i, line := range d.body {
			id := runIDLit.FindString(line)
			if id == "" || strings.Contains(line, "runid-guard:allow") {
				continue
			}
			for _, w := range windows {
				t.Errorf("%s:%d: %s drives %s (its window is %s, measured back from the real clock) yet hard-codes run ID %s, so it passes only until that window passes the ID's date; mint it with recentRunID/runIDAt, or mark the line // runid-guard:allow:\n\t%s",
					d.file, fset.Position(d.fn.Pos()).Line+i, name, w.cmd, w.window, id, strings.TrimSpace(line))
			}
		}
	}
}
