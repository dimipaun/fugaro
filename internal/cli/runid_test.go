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

// mintRunID is the one mint of run IDs in this package's tests: d before now,
// unique via suffix. now is the clock of the window the id has to land inside,
// so a test that pins that clock mints and lists against the same time.
func mintRunID(now func() time.Time, d time.Duration, suffix string) string {
	return now().UTC().Add(-d).Format("20060102-150405") + "-" + suffix
}

// mintRunIDAt is the same in whole days, the shape this package's fixtures
// seed with.
func mintRunIDAt(now func() time.Time, daysAgo int, hhmmss, hex string) string {
	return now().UTC().AddDate(0, 0, -daysAgo).Format("20060102") + "-" + hhmmss + "-" + hex
}

// wallRunID and wallRunIDAt are mintRunID and mintRunIDAt against the wall
// clock. That is the clock of one family of windows: run --pr and run --retry
// are windowed by resolvePR, which reads time.Now() itself (followUpLookback)
// and offers no seam, so their tests mint from it — the guard's recencyWindows
// names these two helpers for those entry points. Minting by lsNow or watchNow
// instead would put the ID outside the follow-up window the moment either of
// those clocks is pinned, which is the mistake runID's doc warns about.
func wallRunID(t *testing.T, offset time.Duration, suffix string) string {
	t.Helper()
	return mintRunID(time.Now, offset, suffix)
}

func wallRunIDAt(daysAgo int, hhmmss, hex string) string {
	return mintRunIDAt(time.Now, daysAgo, hhmmss, hex)
}

// recentRunID is mintRunID against lsNow, with *testing.T's helper reporting.
// Each window family has its own helper, and a test must use the one whose
// clock the command under test reads (the guard's mint field says which):
//
//	runs ls, runs ls --pr, stats runs   lsNow      recentRunID, runIDAt (runs_test.go)
//	watch                               watchNow   runID (watch_queued_test.go)
//	run --pr, run --retry               wall       wallRunID, wallRunIDAt
//
// A test whose listing is filtered by recency must mint its run IDs through
// one of them rather than hard-code a date: a fixed 2026 date passes only
// until the window slides past it, and then fails on every commit by the
// calendar, not by a change. That is this repository's time bomb:
// TestLsNamedRunIsPendingNotError ran a 2026-10-12 run and ls dropped it on
// 2026-10-19. TestNoFixedRunIDsInRecencyTests guards the rule.
func recentRunID(t *testing.T, offset time.Duration, suffix string) string {
	t.Helper()
	return mintRunID(lsNow, offset, suffix)
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

// TestLsHelpSpellsTheDefaultWindow: lsDefaultSince exists so the default
// window has one spelling — the guard reads it, and the copy the user reads has
// to be built from it too, or a change of window leaves the help text (and
// --since's own default line) advertising a window ls no longer uses.
func TestLsHelpSpellsTheDefaultWindow(t *testing.T) {
	cmd := newLsCmd()
	f := cmd.Flag("since")
	if f == nil {
		t.Fatal("runs ls has no --since flag")
	}
	if f.DefValue != lsDefaultSince {
		t.Errorf("--since defaults to %q, want lsDefaultSince (%q)", f.DefValue, lsDefaultSince)
	}
	for what, text := range map[string]string{"Long description": cmd.Long, "--since usage": f.Usage} {
		if !strings.Contains(text, lsDefaultSince) {
			t.Errorf("runs ls's %s does not name the default window %q:\n%s", what, lsDefaultSince, text)
		}
	}
}

// TestWatchWindowFollowsTheClock is TestLsWindowFollowsTheClock for watch:
// its windows (the queued scan's queuedLookback and finishedLookback, and the
// degraded view's degradedSince) are measured from watchNow, the seam runID
// mints against, not from a wall clock the tests cannot move. The two 400-day
// cases are the discriminating ones: a run 400 days from the pinned clock is
// outside every watch window (the widest is finishedLookback, 24h), so only
// the injected clock could have listed it.
func TestWatchWindowFollowsTheClock(t *testing.T) {
	defer func(old func() time.Time) { watchNow = old }(watchNow)
	real := time.Now().UTC()

	// The queued scan: a run with a claim, listed by the runs bucket's own
	// mint-time listing.
	queuedAt := func(t *testing.T, mintAt, listAt time.Time) (string, string) {
		t.Helper()
		f := newBudgetFixture(t, "")
		seedWatch(f, "mine")
		watchNow = func() time.Time { return mintAt }
		id := runID(time.Minute, "feed")
		f.writeTask(t, appSlug, id, "")
		f.writeClaim(t, appSlug, id, mintAt.Add(-time.Minute))
		watchNow = func() time.Time { return listAt }
		out, _, err := execute(t, "watch", "--once", "--json")
		if err != nil {
			t.Fatal(err)
		}
		return id, out
	}
	// The degraded view: no budget backend, so watch reads run records with
	// ls's own listing, windowed by degradedSince.
	degradedAt := func(t *testing.T, mintAt, listAt time.Time) (string, string) {
		t.Helper()
		f := newCloudFixture(t)
		f.run.Project, f.run.Region = "proj-1234", "us-east5"
		watchNow = func() time.Time { return mintAt }
		id := runID(time.Minute, "deed")
		seedRun(t, f, id, "", "someone@example.com", false)
		watchNow = func() time.Time { return listAt }
		out, _, err := execute(t, "watch", "--once", "--plain")
		if err != nil {
			t.Fatal(err)
		}
		return id, out
	}
	for _, tc := range []struct {
		name       string
		mint, list time.Time
		wantListed bool
	}{
		{"30 days ahead", real.Add(30 * 24 * time.Hour), real.Add(30 * 24 * time.Hour), true},
		{"400 days ahead", real.Add(400 * 24 * time.Hour), real.Add(400 * 24 * time.Hour), true},
		{"400 days behind", real.Add(-400 * 24 * time.Hour), real.Add(-400 * 24 * time.Hour), true},
		// The same 400-day-old run is dropped once the clock is real: what
		// listed it above was the injected clock, not a window wide enough
		// to hold it by luck.
		{"400 days behind, listed at the real clock", real.Add(-400 * 24 * time.Hour), real, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, view := range []struct {
				name string
				at   func(*testing.T, time.Time, time.Time) (string, string)
			}{{"queued scan", queuedAt}, {"degraded", degradedAt}} {
				id, out := view.at(t, tc.mint, tc.list)
				if got := strings.Contains(out, id); got != tc.wantListed {
					t.Fatalf("%s: run %s listed = %v, want %v at %s:\n%s",
						view.name, id, got, tc.wantListed, tc.list.Format(time.RFC3339), out)
				}
			}
		})
	}
}

// invocation is one command a test drives, as the command line's own
// arguments: execute(t, "run", "--pr", "7") is [run --pr 7], and the helpers
// that wrap a command (lsJSON, executeWatch, wantRefused) bring their
// command's own arguments with them. An argument list a test builds in a
// table and drives indirectly (execute(t, args...)) is read from the table's
// own []string literal; elements that are not string literals (a variable's
// run ID, say) are left out, as they are for a direct call. Reading arguments
// rather than source lines keeps a flag named in an expected-error string
// from counting as a flag passed, and follows an argument list split over
// several lines.
type invocation struct {
	file string
	line int
	args []string
	// forwarded is true for a call that passes an argument list through from
	// its own caller (append(...), a variadic spread): the command it names
	// is the caller's, whose flags are not in this call's literals.
	forwarded bool
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

// fixedWindow reports whether the invocation reads a window the test chose
// rather than one measured back from the clock: no --since at all, or an
// absolute one. Everything parseSince accepts that is not an instant ("30d",
// "36h", "90m") is a span off the same clock the default window is, so it
// leaves the ID just as date-lucky — only the date it ages out on moves.
func (i invocation) fixedWindow() bool {
	for n, a := range i.args {
		v := ""
		switch {
		case a == "--since" && n+1 < len(i.args):
			v = i.args[n+1]
		case strings.HasPrefix(a, "--since="):
			v = strings.TrimPrefix(a, "--since=")
		default:
			continue
		}
		if !absoluteSince(v) {
			return false
		}
	}
	return true
}

// sinceValue is --since's own value, "" when the invocation passes none: the
// window the failure message must name when the value is a relative one.
func (i invocation) sinceValue() string {
	for n, a := range i.args {
		switch {
		case a == "--since" && n+1 < len(i.args):
			return i.args[n+1]
		case strings.HasPrefix(a, "--since="):
			return strings.TrimPrefix(a, "--since=")
		}
	}
	return ""
}

// absoluteSince reports whether a --since value names an instant rather than a
// span: "0" is no bound at all, and a date (2006-01-02) or a timestamp
// (RFC3339) is a window the caller chose. A relative value is not an
// exemption, however long it is.
func absoluteSince(s string) bool {
	if s == "0" {
		return true
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
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

// isStringSlice reports whether t is []string or [][]string: a test's table
// of command lines.
func isStringSlice(t ast.Expr) bool {
	at, ok := t.(*ast.ArrayType)
	if !ok {
		return false
	}
	switch el := at.Elt.(type) {
	case *ast.Ident:
		return el.Name == "string"
	case *ast.ArrayType:
		return isStringSlice(el)
	}
	return false
}

// litInvs reads the command lines out of a string-slice composite literal:
// [][]string{{"runs", "ls"}, {"run", "--retry", ref}} is two commands and
// []string{"watch", "--once"} is one. Elements that are not string literals
// (a variable holding a run ID) are left out, as they are for a direct call,
// so a table's command still reads as its command and flags.
func litInvs(cl *ast.CompositeLit) [][]string {
	var out [][]string
	var flat []string
	for _, e := range cl.Elts {
		switch v := e.(type) {
		case *ast.CompositeLit:
			out = append(out, litInvs(v)...)
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				continue
			}
			if s, err := strconv.Unquote(v.Value); err == nil {
				flat = append(flat, s)
			}
		}
	}
	if len(flat) > 0 {
		out = append(out, flat)
	}
	return out
}

// stringLits reads every string literal in an expression. A command's
// argument list can be built inside the call — lsJSON(t, append([]string{
// "--since", "0"}, args...)...) — and the window the caller chose is one of
// those literals.
func stringLits(e ast.Expr) []string {
	var out []string
	ast.Inspect(e, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// forwards reports whether a call passes an argument list through from its own
// caller: a variadic spread (args...) or a list built at the call (append(...,
// args...)). Such a call's own literals are not the whole command line — the
// flags the caller chose are missing from it — so a callee's forwarded call is
// not read as an invocation; the test's own call of that callee is, built
// literals and all.
func forwards(c *ast.CallExpr) bool {
	if c.Ellipsis.IsValid() {
		return true
	}
	for _, a := range c.Args {
		calls := false
		ast.Inspect(a, func(n ast.Node) bool {
			if _, ok := n.(*ast.CallExpr); ok {
				calls = true
			}
			return true
		})
		if calls {
			return true
		}
	}
	return false
}

// drivenWindow is one recency-windowed entry point a test drives, with the
// invocation that drives it.
type drivenWindow struct {
	w   recencyWindow
	inv invocation
}

// window is the window the invocation really reads: --since's own value when
// the invocation passes a relative one (an absolute one is never reported),
// else the production constant's value.
func (dw drivenWindow) window() string {
	if v := dw.inv.sinceValue(); v != "" {
		return v + " (--since)"
	}
	return dw.w.window
}

// mintOffer is the half of the failure message that says which helper mints
// against this window's clock.
func (dw drivenWindow) mintOffer() string {
	if len(dw.w.mint) == 0 {
		return "mint it from the clock this window reads"
	}
	return "mint it with " + strings.Join(dw.w.mint, "/")
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
	// window is the production constant the window lives in, read here for
	// its value rather than spelled out: rename it, or change its length,
	// and this guard fails to build or names the window the code now has,
	// instead of guarding a window that is no longer there.
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
	// mint names the test helper a test driving this window must mint its IDs
	// through: the one that reads THIS window's clock (mintRunID's now
	// argument). Minting through another clock is the cross-clock mistake the
	// runID doc warns about — the ID then falls outside the window as soon as
	// that clock is pinned. Empty when nothing mints against this window's
	// clock (a report test's fixture pins budgetNow and seeds dates it picks).
	// Each name must be a function declared in this package's tests: the
	// guard fails if one is renamed away.
	mint []string
	// since is true when the entry point takes an explicit --since: an
	// ABSOLUTE one (an instant the test chose) means the clock cannot age the
	// test's IDs out. A relative one ("30d") is the same window off the same
	// clock as the default, so it exempts nothing: see invocation.fixedWindow.
	since bool
}

var recencyWindows = []recencyWindow{
	{cmd: "runs ls (default --since)", window: lsDefaultSince, run: []string{"runs", "ls"},
		seam: regexp.MustCompile(`lsNow\s*=`), mint: []string{"recentRunID", "runIDAt"}, since: true},
	{cmd: "runs ls --pr", window: prLookback, run: []string{"runs", "ls", "--pr"},
		seam: regexp.MustCompile(`lsNow\s*=`), mint: []string{"recentRunID", "runIDAt"}, since: true},
	// watch: the queued scan lists run IDs by mint time over queuedLookback
	// and finishedLookback (watch_queued.go), and the degraded view reads
	// degradedSince (watch.go). All three read watchNow (the scanner through
	// its own now field, which newQueuedScanner defaults to watchNow). Only
	// runs bucket objects have a mint time: the frame's agent rows come from
	// the /agents registry, which no window ages.
	{cmd: "watch", window: queuedLookback.String() + "/" + finishedLookback.String() + "/" + degradedSince.String(),
		run: []string{"watch"}, needs: runsBucketWrite, mint: []string{"runID"},
		seam: regexp.MustCompile(`watchNow\s*=|\.now\s*=\s*func\(`)},
	// run --pr and run --retry are windowed by resolvePR, which reads
	// time.Now() itself: no seam exists, so a test cannot pin that window and
	// must mint from the same wall clock (wallRunID/wallRunIDAt).
	{cmd: "run --pr", window: followUpLookback.String(), run: []string{"run", "--pr"},
		unless: reviewModePR, mint: []string{"wallRunID", "wallRunIDAt"}},
	// run --retry reads followUpLookback only through recheckFollowUp, for a
	// stored task that is a follow-up (run.go's reused = spec.IsFollowUp);
	// any other retry reads its task by run ID and reads no window at all.
	{cmd: "run --retry (of a stored follow-up)", window: followUpLookback.String(), run: []string{"run", "--retry"},
		needs: regexp.MustCompile(`followUpSpec\(|prRecord\(|\bPR:\s*[1-9]`), mint: []string{"wallRunID", "wallRunIDAt"}},
	{cmd: "stats runs", window: statsDefaultSince, run: []string{"stats", "runs"},
		mint: []string{"recentRunID", "runIDAt"}, since: true},
	{cmd: "report", window: reportDefaultSince, run: []string{"report"},
		seam: regexp.MustCompile(`budgetNow\s*=`), since: true},
	{cmd: "budget preview", window: previewHistoryWindow.String(), run: []string{"budget", "preview"},
		seam: regexp.MustCompile(`budgetNow\s*=`)},
}

// runsBucketWrite matches a call that puts a run in the runs bucket, whose ID
// therefore carries a mint time a windowed listing can age out.
var runsBucketWrite = regexp.MustCompile(`writeTask\(|writeClaim\(|writeRecord\(|seedSpec\(|seedRun\(|seedRoot\(|seedFollowUp\(|seedPRRuns\(|CreateTask\(|RequestCancel\(`)

// registryKey matches a line whose run ID is an /agents registry key (the only
// shape a fixed ID takes in seedWatch, the fixture most watch tests share).
// The frame's agent rows come from the RTDB registry, which no window ages: a
// key carries no mint time to fall out of a listing, so a fixed ID there is
// not a date bomb whatever today is. Only the line is skipped; the same ID on
// a line that seeds the runs bucket is not.
var registryKey = regexp.MustCompile(`PathAgent\(`)

// TestNoFixedRunIDsInRecencyTests is the guard for recentRunID: it parses
// this package's own test sources and fails a test that drives any
// recency-windowed entry point (recencyWindows: runs ls, watch, run --pr,
// run --retry, stats runs, report, budget preview) and hard-codes a run ID.
// Such a test is date-lucky: it passes until the clock's window passes the
// ID's stamp.
//
// The ID may be anywhere the test reaches: its own body, or the body of a
// fixture it calls (seedPRRuns, seedWatch, seedRun — this package's dominant
// style), and the driver may be the test's own call or one a helper makes. A
// command line the test builds in a table ([][]string{{"runs", "ls"}, ...}
// driven by execute(t, args...)) is read as a command too.
//
// Three things are not read as a bomb:
//   - a test that pins the window's own clock (grep seam): its window follows
//     the pinned time, so its fixed IDs cannot age out. The pin may be in a
//     fixture the test calls.
//   - a fixed ID on a line that is only an /agents registry key
//     (registryKey): a registry row's ID is a key, not a run with a mint time
//     for a listing to age out.
//   - what a callee's forwarded call says (forwards): a helper that passes its
//     own argument list through does not name the window its caller chose, so
//     the caller's own invocation of it is what this guard reads.
//
// An explicit --since is an exemption only when it is an instant ("0",
// YYYY-MM-DD, RFC3339). A relative one (--since 30d) is the same window off
// the same clock, so the guard reads it as the window it is, and the failure
// message names it.
//
// A deliberate fixed ID in a test that none of those cover is exempted with a
// // runid-guard:allow comment on its line.
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
		start int // the line the function's body starts on
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
			dc := &decl{fn: fn, file: name, start: start}
			for i := start; i <= end && i <= len(lines); i++ {
				dc.body = append(dc.body, lines[i-1])
			}
			// parents says what each node sits in, so a composite literal that
			// is a call's own argument (an append-built command line) is not
			// read as one standing on its own.
			parents := map[ast.Node]ast.Node{}
			var stack []ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if len(stack) > 0 {
					parents[n] = stack[len(stack)-1]
				}
				stack = append(stack, n)
				return true
			})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CompositeLit:
					// A command line the function builds in a table and drives
					// indirectly (for _, args := range [][]string{...} {
					// execute(t, args...) }).
					if !isStringSlice(v.Type) {
						return true
					}
					if _, arg := parents[v].(*ast.CallExpr); arg {
						return true // read below, as part of that call's arguments
					}
					for _, args := range litInvs(v) {
						dc.invs = append(dc.invs, invocation{file: name, line: fset.Position(v.Pos()).Line, args: args})
					}
					return true
				case *ast.CallExpr:
					id, ok := v.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					dc.calls = append(dc.calls, id.Name)
					// A test drives a command through one of these helpers:
					// the command line is the call's arguments' string
					// literals, with the helper's own command in front.
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
					for _, a := range v.Args {
						cmd = append(cmd, stringLits(a)...)
					}
					dc.invs = append(dc.invs, invocation{file: name, line: fset.Position(v.Pos()).Line,
						args: cmd, forwarded: forwards(v)})
					return true
				}
				return true
			})
			decls = append(decls, dc)
			byName[fn.Name.Name] = append(byName[fn.Name.Name], dc)
		}
	}

	// reach returns a function and every function it can reach through calls,
	// in breadth-first order. A run ID a test only holds in a helper it calls
	// (this package's dominant style: seedPRRuns, seedWatch, seedRun) is the
	// test's own ID, and a command a helper drives is a command the test
	// drives, so both the scan for fixed IDs and the scan for invocations run
	// over the reachable set, not just the test body.
	reach := func(tested *decl) []*decl {
		seen := map[string]bool{tested.fn.Name.Name: true}
		out := []*decl{tested}
		for i := 0; i < len(out); i++ {
			for _, callee := range out[i].calls {
				if seen[callee] {
					continue
				}
				seen[callee] = true
				out = append(out, byName[callee]...)
			}
		}
		return out
	}

	// pinned says whether a function puts seam w's clock under the test's
	// control, directly or through a function it calls (a fixture that pins
	// budgetNow, say). A test's window then follows the pinned clock, so its
	// fixed run IDs cannot age out.
	pinned := func(tested *decl, w recencyWindow) bool {
		if w.seam == nil {
			return false
		}
		for _, d := range reach(tested) {
			for _, line := range d.body {
				if w.seam.MatchString(line) {
					return true
				}
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
		for _, m := range w.mint {
			if len(byName[m]) == 0 {
				t.Errorf("%s: the guard's mint helper %q is not a function in this package's tests: it was renamed away, and the message would point a future test at a helper that no longer exists",
					w.cmd, m)
			}
		}
	}

	for _, d := range decls {
		name := d.fn.Name.Name
		if !strings.HasPrefix(name, "Test") || name == "TestNoFixedRunIDsInRecencyTests" {
			continue
		}
		rd := reach(d)
		var windows []drivenWindow
	windows:
		for _, w := range recencyWindows {
			if pinned(d, w) {
				continue
			}
			shown := w.needs == nil
			if w.needs != nil {
				for _, x := range rd {
					for _, line := range x.body {
						if w.needs.MatchString(line) {
							shown = true
						}
					}
				}
			}
			if !shown {
				continue
			}
			for _, x := range rd {
				for _, inv := range x.invs {
					if x != d && inv.forwarded {
						continue // its arguments came from the caller: read them there
					}
					if !inv.drives(w.run...) || (w.unless != nil && w.unless(inv)) {
						continue
					}
					if w.since && inv.fixedWindow() {
						continue
					}
					windows = append(windows, drivenWindow{w, inv})
					continue windows
				}
			}
		}
		if len(windows) == 0 {
			continue
		}
		// held names the helper a fixed ID lives in, when it is not the
		// test's own body: the fix belongs there, so the message points
		// there.
		held := func(x *decl) string {
			if x.fn.Name.Name == name {
				return ""
			}
			return " (held in " + x.fn.Name.Name + ")"
		}
		for _, x := range rd {
			for i, line := range x.body {
				id := runIDLit.FindString(line)
				if id == "" || strings.Contains(line, "runid-guard:allow") || registryKey.MatchString(line) {
					continue
				}
				for _, dw := range windows {
					t.Errorf("%s:%d: %s hard-codes run ID %s%s and drives %s at %s:%d, whose window is %s measured back from the real clock: it passes only until that window passes the ID's date; %s, or mark the line // runid-guard:allow:\n\t%s",
						x.file, x.start+i, name, id, held(x), dw.w.cmd, dw.inv.file, dw.inv.line,
						dw.window(), dw.mintOffer(), strings.TrimSpace(line))
				}
			}
		}
	}
}
