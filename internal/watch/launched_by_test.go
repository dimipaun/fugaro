package watch

// Who started a run, on watch's rows.
//
// Several people start runs on one project at once, so every listing says
// whose a run is: the plain frame's BY column and the expanded detail line
// show it, and watch --json carries the same value as fugaro runs ls --json
// (the console reads both documents). The launcher is text a launcher wrote —
// untrusted, like every other bucket-derived string — so it is sanitised on
// the way in (safetext.Field) and never trusted for anything else
// (runview.Row.LaunchedBy, and the display allow-list test beside it).

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// launcherView is one repository with a live run and a finished run, both
// carrying the launcher by (through the paths a real frame takes: the finished
// row through MergeFinished, the live row through MergeFacts, since a live
// row's launcher is only ever launch.json's).
func launcherView(by string) View {
	v := View{Now: t0, Day: "2026-10-03", Mode: "enforce", Conn: Connection{Kind: ConnLive},
		Repos: []RepoBlock{{Slug: "acme__app", Name: "acme/app", Runs: []RunRow{
			mkRun("r-live", "Fix the flaky login test", "coding", nil),
		}}}}
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-done", Slug: "acme__app", Title: "Old work", Status: "succeeded",
			LaunchedBy: by, FinishedAt: t0.Add(-time.Hour)},
	}, t0)
	return MergeFacts(v, Facts{{Slug: "acme__app", Run: "r-live"}: {LaunchedBy: by}})
}

// plainFrame is v as the plain text frame.
func plainFrame(t *testing.T, v View) string {
	t.Helper()
	var b strings.Builder
	if err := RenderPlain(&b, "aurora", v, nil, PlainOptions{ASCII: true}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// tableCells are a plain-table line's cells: tabwriter output, so cells are
// separated by two or more spaces.
var tableCells = regexp.MustCompile(`  +`)

// byCellOf is the value under header's BY column on the plain frame's line
// mentioning id, how many cells that line and its header have, and the line
// itself.
func byCellOf(t *testing.T, frame, header, id string) (cell string, cells, hcells int, line string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	h := -1
	for i, l := range lines {
		if strings.Contains(l, header) {
			h = i
			break
		}
	}
	if h < 0 {
		t.Fatalf("no %q table in the plain frame:\n%s", header, frame)
	}
	hf := tableCells.Split(strings.TrimRight(lines[h], " "), -1)
	col := -1
	for i, c := range hf {
		if strings.TrimSpace(c) == "BY" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("the %q table has no BY column: %q\n%s", header, lines[h], frame)
	}
	for _, l := range lines[h+1:] {
		if !strings.Contains(l, id) {
			continue
		}
		rf := tableCells.Split(strings.TrimRight(l, " "), -1)
		if col >= len(rf) {
			return "", len(rf), len(hf), l
		}
		return strings.TrimSpace(rf[col]), len(rf), len(hf), l
	}
	t.Fatalf("no %q row in the plain frame:\n%s", id, frame)
	return "", 0, 0, ""
}

// TestPlainShowsTheLauncherShortForm: both of the plain frame's run tables
// have a BY column, and it shows the short form a column has room for (the
// part before an address's @), never the whole address. Mutation caught:
// dropping the column, or printing the full address in it.
func TestPlainShowsTheLauncherShortForm(t *testing.T) {
	got := plainFrame(t, launcherView("dana@example.com"))
	if cell, _, _, _ := byCellOf(t, got, "RUN", "r-live"); cell != "dana" {
		t.Errorf("the active table's BY cell = %q, want the short form %q:\n%s", cell, "dana", got)
	}
	if cell, _, _, _ := byCellOf(t, got, "FINISHED", "r-done"); cell != "dana" {
		t.Errorf("the finished table's BY cell = %q, want the short form %q:\n%s", cell, "dana", got)
	}
	if strings.Contains(got, "dana@example.com") {
		t.Errorf("the whole address reached the text frame:\n%s", got)
	}
	// An automated launch shows its identity as it is: a service account name
	// has no @ to cut at, so the whole value is the short form.
	sa := plainFrame(t, launcherView("ci-runner"))
	if cell, _, _, _ := byCellOf(t, sa, "RUN", "r-live"); cell != "ci-runner" {
		t.Errorf("a service account's BY cell = %q, want the whole value:\n%s", cell, sa)
	}
}

// TestPlainLauncherIsADashWhenNotRecorded: a run whose launch.json recorded no
// launcher (a backfill, a local run, or `fugaro logs`'s synthetic launch) shows a dash,
// in both tables — a fact, not a blank cell and not an invented name.
// Mutation caught: falling back to another identity, or leaving the cell
// empty.
func TestPlainLauncherIsADashWhenNotRecorded(t *testing.T) {
	got := plainFrame(t, launcherView(""))
	for _, c := range []struct{ header, id string }{{"RUN", "r-live"}, {"FINISHED", "r-done"}} {
		cell, _, _, _ := byCellOf(t, got, c.header, c.id)
		if cell != "-" {
			t.Errorf("%s row %s's BY cell = %q, want a dash:\n%s", c.header, c.id, cell, got)
		}
	}
}

// TestPlainLauncherIsSanitised: the launcher is written by whoever launched the
// run, so it is untrusted text like a recipe name or an OAuth account and must
// clear the same bar: no escape, bidi or line-separator rune, no extra table
// cell or line, no unbounded width. Mutation caught: putting the raw value in
// the cell.
func TestPlainLauncherIsSanitised(t *testing.T) {
	huge := strings.Repeat("x", 10_000)
	hostile := []string{
		"esc\x1b[2Jroot@example.com",
		"osc\x1b]0;PWNED\x07root@example.com",
		"nl\nroot@example.com",
		"tab\troot@example.com",
		"bidi\u202eroot@example.com",
		"sep\u2028root@example.com",
		"para\u2029root@example.com",
		huge + "@example.com",
	}
	// The frame a recorded launcher draws, to compare line counts against:
	// a value that grew a line of its own would show up as a taller frame.
	lines := strings.Count(plainFrame(t, launcherView("dana@example.com")), "\n")
	for _, by := range hostile {
		got := plainFrame(t, launcherView(by))
		for _, r := range got {
			if r == '\x1b' || r == 0x07 || r == 0x9b || (r >= 0x202a && r <= 0x202e) || r == 0x2028 || r == 0x2029 {
				t.Fatalf("launcher %q: hostile rune %U survived:\n%q", by, r, got)
			}
		}
		if strings.Contains(got, "PWNED") {
			t.Fatalf("launcher %q: an escape sequence's payload reached the frame:\n%q", by, got)
		}
		if n := strings.Count(got, "\n"); n != lines {
			t.Fatalf("launcher %q: the frame is %d lines, want %d (a newline in the value split it):\n%q", by, n, lines, got)
		}
		// A tab in the value would open a cell of its own and shift every
		// column after it; a value can never add cells, only widen them.
		for _, c := range []struct{ header, id string }{{"RUN", "r-live"}, {"FINISHED", "r-done"}} {
			cell, cells, hcells, line := byCellOf(t, got, c.header, c.id)
			if strings.ContainsRune(line, '\t') {
				t.Fatalf("launcher %q: a tab survived into the %s row: %q", by, c.header, line)
			}
			if cells > hcells {
				t.Fatalf("launcher %q: %s row has %d cells, header has %d (a tab opened a column):\n%q", by, c.header, cells, hcells, got)
			}
			if n := utf8.RuneCountInString(cell); n > safetext.FieldMax {
				t.Fatalf("launcher %q: %s BY cell is %d runes, past the cap:\n%q", by, c.header, n, got)
			}
		}
		if n := utf8.RuneCountInString(got); n > 4_000 {
			t.Fatalf("launcher %q: the frame is %d runes, not bounded:\n%.120q...", by, n, got)
		}
	}
}

// TestMergeFactsSanitisesTheLauncher: the launcher is sanitised where it is
// stamped onto the row — the one place a live run's launcher arrives — not only
// where it happens to be printed. Mutation caught: assigning f.LaunchedBy as
// it is.
func TestMergeFactsSanitisesTheLauncher(t *testing.T) {
	for _, by := range []string{"esc\x1b[2Jroot", "nl\nroot", "bidi\u202eroot", "nul\x00root", "tab\troot"} {
		v := launcherView("")
		v = MergeFacts(v, Facts{{Slug: "acme__app", Run: "r-live"}: {LaunchedBy: by}})
		got := v.Repos[0].Runs[0].LaunchedBy
		if strings.ContainsAny(got, "\x1b\x00\t\n") || strings.ContainsRune(got, 0x202e) {
			t.Errorf("launcher %q came through as %q", by, got)
		}
		if !strings.Contains(got, "root") {
			t.Errorf("launcher %q was erased (%q) rather than sanitised", by, got)
		}
	}
	// A 10 KB value is capped, and the cap is safetext's own (FieldMax), so
	// watch --json and fugaro runs ls --json read one launch.json the same.
	v := launcherView("")
	v = MergeFacts(v, Facts{{Slug: "acme__app", Run: "r-live"}: {LaunchedBy: strings.Repeat("x", 10_000)}})
	if n := utf8.RuneCountInString(v.Repos[0].Runs[0].LaunchedBy); n != safetext.FieldMax {
		t.Errorf("a 10 KB launcher came through as %d runes, want safetext.FieldMax (%d)", n, safetext.FieldMax)
	}
}

// TestQueuedRowCarriesTheLauncher: a queued run — one known only from the runs
// bucket, with no registry entry yet — carries its launcher too, so a run shows
// whose it is from the moment it is launched.
func TestQueuedRowCarriesTheLauncher(t *testing.T) {
	q := QueuedRun{Run: "r-q", Slug: "acme__app", Workflow: "web", LaunchedBy: "dana@example.com",
		LaunchedAt: t0.Add(-time.Minute)}
	if got := queuedRow(q, t0).LaunchedBy; got != "dana@example.com" {
		t.Errorf("queued row's launcher = %q", got)
	}
	q.LaunchedBy = "esc\x1b[2Jroot@example.com"
	got := queuedRow(q, t0).LaunchedBy
	if strings.ContainsAny(got, "\x1b") || !strings.Contains(got, "root") {
		t.Errorf("queued row's launcher = %q, want it sanitised and not erased", got)
	}
}

// TestJSONLauncherIsTheFullRecordedIdentity: --json carries the whole recorded
// identity — not the short form the text columns show — for a live run and for
// a finished one, since a reader that follows a run into finished[] must not
// lose who launched it. Mutation caught: emitting the short form, or dropping
// the key from finished[].
func TestJSONLauncherIsTheFullRecordedIdentity(t *testing.T) {
	const by = "dana.person+fugaro@example.com"
	doc := BuildJSON("aurora", launcherView(by))
	if len(doc.Runs) != 1 || doc.Runs[0].LaunchedBy != by {
		t.Errorf("runs[0].launched_by = %+v, want %q", doc.Runs, by)
	}
	if len(doc.Finished) != 1 || doc.Finished[0].LaunchedBy != by {
		t.Errorf("finished[0].launched_by = %+v, want %q", doc.Finished, by)
	}
	if len(doc.Ready) != 0 {
		t.Errorf("the fixture is not ready: %+v", doc.Ready)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Runs     []map[string]any `json:"runs"`
		Finished []map[string]any `json:"finished"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Runs[0]["launched_by"] != by || m.Finished[0]["launched_by"] != by {
		t.Fatalf("the wire document does not carry the full launcher: %s", b)
	}
}

// TestJSONLauncherKeyIsAbsentWhenNotRecorded: no launcher, no key — never "",
// never an invented value. The console's contract says the key is absent for a
// run whose launch recorded no launcher, and a key that were always there
// would make that indistinguishable from an empty identity.
func TestJSONLauncherKeyIsAbsentWhenNotRecorded(t *testing.T) {
	doc := BuildJSON("aurora", launcherView(""))
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "launched_by") {
		t.Fatalf("an absent launcher still emitted the key: %s", b)
	}
}

// TestDetailNamesTheLauncher: the interactive screen's compact run table has no
// room for a launcher column, so the expanded detail names it — the whole
// identity, which is what tells a person from an automated launch — and a run
// whose launch.json recorded none shows the same dash as every other empty
// line.
func TestDetailNamesTheLauncher(t *testing.T) {
	v := fixture()
	run := &v.Repos[0].Runs[0]
	run.LaunchedBy = "ci-runner@proj.iam.gserviceaccount.com"
	sel := Cursor{Slug: v.Repos[0].Slug, Run: run.Run}
	got := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if line := detailValue(got, "by"); line != "ci-runner@proj.iam.gserviceaccount.com" {
		t.Fatalf("the detail's launcher line = %q, want the whole identity:\n%s", line, got)
	}
	// The other runs of the fixture record none.
	empty := fixture()
	sel = Cursor{Slug: empty.Repos[0].Slug, Run: empty.Repos[0].Runs[0].Run}
	got = frame(empty, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if line := detailValue(got, "by"); line != "-" {
		t.Fatalf("a run with no launcher's detail line = %q, want a dash:\n%s", line, got)
	}
}

// detailValue is the value of an expanded detail's key line, "" when the frame
// has no such line.
func detailValue(frame, key string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "    "+key) {
			return strings.TrimSpace(strings.TrimPrefix(l, "    "+key))
		}
	}
	return ""
}

// TestPlainLauncherCellIsClippedToTheColumn: a long launcher with no @ (a CI
// identity of 10 KB) is capped at safetext.FieldMax by the sanitiser, which is
// far wider than a column; the plain tables clip it like runs ls does (64
// runes, an ellipsis marking the cut), so one value cannot widen the table.
// Mutation caught: printing the sanitised value unclipped.
func TestPlainLauncherCellIsClippedToTheColumn(t *testing.T) {
	got := plainFrame(t, launcherView(strings.Repeat("ö", 10_000)))
	for _, c := range []struct{ header, id string }{{"RUN", "r-live"}, {"FINISHED", "r-done"}} {
		cell, _, _, _ := byCellOf(t, got, c.header, c.id)
		if n := utf8.RuneCountInString(cell); n != launcherCellRunes+1 || !strings.HasSuffix(cell, "…") {
			t.Errorf("%s BY cell is %d runes, want %d ending in an ellipsis: %q", c.header, n, launcherCellRunes+1, cell)
		}
	}
	// Exactly the cap is not cut.
	exact := strings.Repeat("y", launcherCellRunes)
	got = plainFrame(t, launcherView(exact))
	if cell, _, _, _ := byCellOf(t, got, "RUN", "r-live"); cell != exact {
		t.Errorf("a value of exactly %d runes was changed: %q", launcherCellRunes, cell)
	}
}

// TestPlainLauncherCellCutsAtTheBoundary: one rune past the cap is cut, the cap
// itself is not. Mutation caught: a threshold off by one.
func TestPlainLauncherCellCutsAtTheBoundary(t *testing.T) {
	over := strings.Repeat("z", launcherCellRunes+1)
	got := plainFrame(t, launcherView(over))
	want := strings.Repeat("z", launcherCellRunes) + "…"
	if cell, _, _, _ := byCellOf(t, got, "RUN", "r-live"); cell != want {
		t.Errorf("a value one rune over the cap shows %q, want %q", cell, want)
	}
}

// tuiRowCell is the value under the header's col column on the frame line
// whose RUN cell is shortID, for the interactive screen's fixed-width table:
// columns are space padded, so the header's own offsets are the row's. next is
// the column the wanted one is followed by, which is where its cell ends. A
// row that stops before that offset — its own last cell is empty, and line()
// drops the blanks — is cut at its own end.
//
// The offsets are counted in runes, not bytes: a row's title is clipped with
// an ellipsis, which is three bytes wide, and a byte offset taken from the
// header would land two columns into the wrong cell.
func tuiRowCell(t *testing.T, frame, shortID, col, next string) string {
	t.Helper()
	var hdr string
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "TITLE") && strings.Contains(l, col) {
			hdr = l
			break
		}
	}
	if hdr == "" {
		t.Fatalf("the frame has no run-table header with a %q column:\n%s", col, frame)
	}
	hr := []rune(hdr)
	start, end := runeIndex(hr, col), runeIndex(hr, next)
	if start < 0 || end <= start {
		t.Fatalf("the header %q has no %q column before %q", hdr, col, next)
	}
	for _, l := range strings.Split(frame, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || (f[0] != shortID && !(f[0] == "▸" && f[1] == shortID)) {
			continue
		}
		r := []rune(l)
		return strings.TrimSpace(string(r[min(start, len(r)):min(end, len(r))]))
	}
	t.Fatalf("no run row %q in the frame:\n%s", shortID, frame)
	return ""
}

// runeIndex is strings.Index in runes: the first rune offset of sub in r, or
// -1.
func runeIndex(r []rune, sub string) int {
	i := strings.Index(string(r), sub)
	if i < 0 {
		return -1
	}
	return len([]rune(string(r)[:i]))
}

// TestWideTableShowsTheLauncherColumn: the interactive screen's run table
// carries a BY column like the plain frames do — the short form a column has
// room for (the part before an address's @), so a viewer can tell whose run a
// row is without expanding it — while the expanded detail keeps the whole
// recorded identity (owner spec 2026-10-22, item 2). Mutation caught: dropping
// the column, printing the full address in it, or losing the detail's line.
func TestWideTableShowsTheLauncherColumn(t *testing.T) {
	got := frame(launcherView("dana@example.com"), 120, 0)
	if cell := tuiRowCell(t, got, "live", "BY", "FLAGS"); cell != "dana" {
		t.Errorf("the live row's BY cell = %q, want the short form %q:\n%s", cell, "dana", got)
	}
	if strings.Contains(got, "dana@example.com") {
		t.Errorf("the whole address reached the unexpanded table:\n%s", got)
	}
	// An automated launch has no @ to cut at, so the whole value is the short
	// form; a run whose launch recorded none shows the dash the detail shows.
	sa := frame(launcherView("ci-runner"), 120, 0)
	if cell := tuiRowCell(t, sa, "live", "BY", "FLAGS"); cell != "ci-runner" {
		t.Errorf("a service account's BY cell = %q, want the whole value:\n%s", cell, sa)
	}
	none := frame(launcherView(""), 120, 0)
	if cell := tuiRowCell(t, none, "live", "BY", "FLAGS"); cell != "-" {
		t.Errorf("a run with no recorded launcher's BY cell = %q, want a dash:\n%s", cell, none)
	}
}

// TestDetailCarriesTheWholeLauncherOnTheColumnScreen: with a BY column on the
// table, the expanded detail still names the launcher whole — the column is
// the short form, the detail is what tells a person from an automated launch.
func TestDetailCarriesTheWholeLauncherOnTheColumnScreen(t *testing.T) {
	const by = "ci-runner@proj.iam.gserviceaccount.com"
	v := launcherView(by)
	sel := Cursor{Slug: "acme__app", Run: "r-live"}
	got := frame(v, 120, 0, func(o *RenderOptions) { o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true} })
	if line := detailValue(got, "by"); line != by {
		t.Fatalf("the detail's launcher line = %q, want the whole identity:\n%s", line, got)
	}
	if cell := tuiRowCell(t, got, "live", "BY", "FLAGS"); cell == by {
		t.Fatalf("the table's BY cell is the whole identity:\n%s", got)
	}
}
