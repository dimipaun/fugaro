package watch

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/budget"
)

var update = flag.Bool("update", false, "rewrite the golden frames")

func init() { rw.EastAsianWidth = false } // the goldens do not depend on the locale

// golden compares got with testdata/<name>.golden.
func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	got = strings.ReplaceAll(got, "\x1b", "<ESC>") + "\n"
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Fatalf("%s differs.\n--- want\n%s--- got\n%s", p, want, got)
	}
}

func frame(v View, w, h int, mod ...func(*RenderOptions)) string {
	o := RenderOptions{Width: w, Height: h, Project: "aurora", Keys: true}
	for _, f := range mod {
		f(&o)
	}
	return Render(v, o).String()
}

func mkRun(id, title, stage string, extra func(*RunRow)) RunRow {
	r := RunRow{Run: id, Slug: "x", Title: title, Stage: stage, Round: "1", Verify: "unit", Models: "opus / sonnet",
		Auth: "api_key", Spent: 1_250_000, HasSpent: true, Age: 12*time.Minute + 3*time.Second, HasAge: true}
	if extra != nil {
		extra(&r)
	}
	return r
}

// fixture is a project with a busy repository (a silent and a near-deadline
// run, an oauth run), a killed one with a lost run, and a quiet one.
func fixture() View {
	bar := func(used, cap budget.Micros) Bar {
		return Bar{Has: true, Used: used, Cap: cap, Percent: float64(used) / float64(cap) * 100}
	}
	return View{
		Now: t0, Day: "2026-10-03", Mode: "enforce", Conn: Connection{Kind: ConnLive, Age: time.Second},
		Project: ProjectLine{Counted: 41 * usd, Spent: 40 * usd, Notional: 3 * usd, Bar: bar(41*usd, 50*usd),
			Burn: Burn{Known: true, PerMin: 120_000, Fast: true}, Runs: 4, RunHours: 0.8},
		Repos: []RepoBlock{
			{Slug: "acme__app", Name: "acme/app", Counted: 30 * usd, Spent: 29 * usd, Notional: 3 * usd, Bar: bar(30*usd, 40*usd),
				Burn: Burn{Known: true, PerMin: 90_000, Fast: true}, Runs: []RunRow{
					mkRun("r-aaaa11", "Fix the flaky login test", "coding", func(r *RunRow) { r.Round = "2"; r.Health = HealthSilent }),
					mkRun("r-bbbb22", "Add pagination to the list endpoint", "review", func(r *RunRow) {
						r.Auth, r.Notional, r.Deadline = "oauth", true, DeadlineNear
					}),
				}},
			{Slug: "acme__lib", Name: "acme/lib", Counted: 11 * usd, Spent: 11 * usd, Bar: bar(11*usd, 10*usd),
				Burn: Burn{}, Kill: KillState{On: true, By: "dimi@x.io", At: t0.Add(-5 * time.Minute), Reason: "runaway loop"},
				Runs: []RunRow{mkRun("r-cccc33", "Refactor the parser", "verify", func(r *RunRow) {
					r.Health, r.Deadline, r.Halted = HealthLost, DeadlineOver, "killed"
				})}},
			{Slug: "acme__docs", Name: "acme/docs", Bar: Bar{}, Burn: Burn{Known: true}, Runs: []RunRow{
				mkRun("r-dddd44", "Typos", "coding", func(r *RunRow) { r.HasSpent, r.Spent, r.HasAge = false, 0, false }),
			}},
		},
	}
}

func TestFrameWide(t *testing.T) {
	golden(t, "wide_120", frame(fixture(), 120, 0))
	golden(t, "wide_120_selected_docs_folded", frame(fixture(), 120, 0, func(o *RenderOptions) {
		o.Selected, o.Collapsed = Cursor{Slug: "acme__docs"}, map[string]bool{"acme__app": true}
	}))
}

func TestFrameMedium(t *testing.T) { golden(t, "medium_80", frame(fixture(), 80, 0)) }
func TestFrameNarrow(t *testing.T) { golden(t, "narrow_50", frame(fixture(), 50, 0)) }

// At tier 1 (width < 70) a selected run is marked on its own two-line row,
// not just a selected header: runRows' cur glyph must reach the narrow
// layout too, not only the wide table (TestFrameSelectedRunDetail is tier 3).
func TestFrameNarrowSelectedRun(t *testing.T) {
	sel := Cursor{Slug: "acme__app", Run: "r-aaaa11"}
	got := frame(fixture(), 50, 0, func(o *RenderOptions) { o.Selected = sel })
	if !strings.Contains(got, "▸ r-aaaa11") {
		t.Fatalf("selected run not marked at tier 1:\n%s", got)
	}
	if strings.Contains(got, "▸ acme/app") {
		t.Fatalf("header must not also be marked:\n%s", got)
	}
	golden(t, "narrow_50_selected_run", got)
}
func TestFrameTooNarrow(t *testing.T) {
	got := frame(fixture(), 38, 0)
	golden(t, "toonarrow_38", got)
	if got != tooNarrow {
		t.Fatalf("%q", got)
	}
}

func TestFrameLive(t *testing.T) {
	v := fixture()
	v.Conn = Connection{Kind: ConnPolling, Age: 2 * time.Second}
	golden(t, "polling_80", frame(v, 80, 0))
}

func TestFrameStale(t *testing.T) {
	v := fixture()
	v.Conn = Connection{Kind: ConnStale, Age: 47 * time.Second}
	golden(t, "stale_80", frame(v, 80, 0))
}

func TestFrameOffline(t *testing.T) {
	v := fixture()
	v.Conn = Connection{Kind: ConnOffline, Reason: "connection reset", Age: 95 * time.Second}
	golden(t, "offline_100", frame(v, 100, 0))
	// Greyed with colour on: faint, no other colour.
	got := frame(v, 100, 0, func(o *RenderOptions) { o.Color = true })
	if !strings.Contains(got, "\x1b[2m") || strings.Contains(got, "\x1b[1;7;31m") {
		t.Fatal("an offline frame is greyed, banners included:\n" + got)
	}
}

func TestFrameRefused(t *testing.T) {
	v := fixture()
	v.Conn = Connection{Kind: ConnRefused, Reason: "access refused"}
	v.Repos, v.Project = nil, ProjectLine{}
	golden(t, "refused_80", frame(v, 80, 0))
}

func TestFrameKilledRepo(t *testing.T) {
	v := fixture()
	v.Project.Kill = KillState{On: true, By: "ops@x.io", At: t0.Add(-time.Hour), Reason: "budget review"}
	golden(t, "killed_project_100", frame(v, 100, 0))
}

func TestFrameQueued(t *testing.T) {
	v := fixture()
	v = MergeQueued(v, Config{}, []QueuedRun{
		{Run: "r-eeee55", Slug: "acme__app", Workflow: "web", Recipe: "fast", RequestedBy: "a@b.c", LaunchedAt: t0.Add(-2 * time.Minute)},
		{Run: "r-ffff66", Slug: "acme__app", LaunchedAt: t0.Add(-QueuedStuckAfter - time.Minute)},
		{Run: "r-gggg77", Slug: "acme__new", Workflow: "web", LaunchedAt: t0.Add(-30 * time.Second)},
	}, "", t0)
	golden(t, "queued_100", frame(v, 100, 0))
}

func TestFrameQueuedNote(t *testing.T) {
	v := fixture()
	v = MergeQueued(v, Config{}, nil, "runs bucket unreachable: permission denied", t0)
	golden(t, "queued_note_80", frame(v, 80, 0))
}

func TestFrameEmptyProject(t *testing.T) {
	v := View{Now: t0, Day: "2026-10-03", Mode: "observe", Conn: Connection{Kind: ConnLive}}
	golden(t, "empty_80", frame(v, 80, 0))
	v.Conn = Connection{Kind: ConnOffline, Reason: "connecting"}
	golden(t, "connecting_80", frame(v, 80, 0))
}

func TestFramePromptOpen(t *testing.T) {
	golden(t, "prompt_kill_60", frame(fixture(), 60, 0, func(o *RenderOptions) {
		o.Footer = []string{"kill the WHOLE PROJECT: halts every run of aurora and refuses new ones. Type aurora to confirm (Esc cancels): aur_"}
	}))
}

func manyAgents(n int) View {
	v := fixture()
	v.Repos = v.Repos[:1]
	v.Repos[0].Runs = nil
	for i := 0; i < n; i++ {
		v.Repos[0].Runs = append(v.Repos[0].Runs, mkRun(fmt.Sprintf("r-%04d", i), fmt.Sprintf("job number %d", i), "coding", nil))
	}
	v.Repos = append(v.Repos, fixture().Repos[1])
	v.Project.Runs = n + 1
	return v
}

func TestFrameManyAgentsScrolls(t *testing.T) {
	v := manyAgents(200)
	fr := Render(v, RenderOptions{Width: 100, Height: 24, Project: "aurora"})
	if len(fr.Lines) != 24 {
		t.Fatalf("%d lines, want 24", len(fr.Lines))
	}
	golden(t, "many_top_100", fr.String())
	if !strings.Contains(fr.String(), "more below") || fr.MaxScroll < 150 {
		t.Fatalf("no scroll: %+v", fr.MaxScroll)
	}
	mid := Render(v, RenderOptions{Width: 100, Height: 24, Project: "aurora", Scroll: 100})
	golden(t, "many_middle_100", mid.String())
	if !strings.Contains(mid.String(), "more above") || !strings.Contains(mid.String(), "more below") {
		t.Fatal("a middle frame shows both indicators")
	}
	end := Render(v, RenderOptions{Width: 100, Height: 24, Project: "aurora", Scroll: 1 << 20})
	if end.Scroll != end.MaxScroll || strings.Contains(end.String(), "more below") || len(end.Lines) > 24 {
		t.Fatalf("end frame: scroll %d max %d lines %d", end.Scroll, end.MaxScroll, len(end.Lines))
	}
	// Following the selection scrolls back to the second repository's header.
	sel := Render(v, RenderOptions{Width: 100, Height: 24, Project: "aurora", Selected: Cursor{Slug: "acme__lib"}, Follow: true})
	if !strings.Contains(sel.String(), "acme/lib") || len(sel.Lines) > 24 {
		t.Fatalf("selected repository not shown:\n%s", sel)
	}
}

// Expanding a run near the bottom of a scrolled viewport must bring its
// detail into view, not just its own row: following must track the bottom
// of the selection (the detail's last line), not only its top.
func TestFrameFollowKeepsExpandedDetailVisible(t *testing.T) {
	v := manyAgents(30)
	last := v.Repos[0].Runs[len(v.Repos[0].Runs)-1]
	sel := Cursor{Slug: v.Repos[0].Slug, Run: last.Run}
	for _, w := range []int{50, 80, 120} {
		got := frame(v, w, 14, func(o *RenderOptions) {
			o.Selected, o.Expanded, o.Follow = sel, map[Cursor]bool{sel: true}, true
		})
		if !strings.Contains(got, "deadline") {
			t.Fatalf("width %d: expanded detail scrolled out of view:\n%s", w, got)
		}
	}
}

// A stale cursor (naming a run that is not, or no longer, in the block) must
// mark nothing and must not panic, whether the block has no runs or one.
func TestFrameStaleCursorZeroAndOneRun(t *testing.T) {
	zero := View{Repos: []RepoBlock{{Slug: "a", Name: "a"}}}
	if got := frame(zero, 80, 0, func(o *RenderOptions) { o.Selected = Cursor{Slug: "a", Run: "gone"} }); strings.Contains(got, "▸") {
		t.Fatalf("a stale run cursor on an empty block must mark nothing:\n%s", got)
	}
	one := View{Repos: []RepoBlock{{Slug: "a", Name: "a", Runs: []RunRow{{Run: "r1"}}}}}
	if got := frame(one, 80, 0, func(o *RenderOptions) { o.Selected = Cursor{Slug: "a", Run: "gone"} }); strings.Contains(got, "▸ r1") {
		t.Fatalf("a stale run cursor must not mark the real run:\n%s", got)
	}
}

func TestWideRunesClipByWidth(t *testing.T) {
	v := fixture()
	long := strings.Repeat("日本語", 30) + "🚀🚀🚀"
	v.Repos[0].Name = "東京/リポジトリ🚀"
	v.Repos[0].Runs[0].Title = long
	v.Repos[0].Runs[0].Stage = "コーディング中です"
	v.Repos[0].Runs[1].Models = long
	for _, w := range []int{120, 100, 80, 70, 50, 40} {
		for _, color := range []bool{false, true} {
			got := frame(v, w, 0, func(o *RenderOptions) { o.Color = color })
			for _, l := range strings.Split(got, "\n") {
				if n := dw(stripSGR(l)); n > w {
					t.Fatalf("width %d: line is %d columns: %q", w, n, l)
				}
			}
		}
	}
	// In the wide table the title cell is cut to its column by columns, so
	// the neighbouring cell starts where the header says.
	got := frame(v, 120, 0)
	hdr := strings.Split(got, "\n")[4]
	col := strings.Index(hdr, "STAGE")
	var row string
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "r-aaaa11") {
			row = l
		}
	}
	if strings.Index(row, "コーディ") < 0 {
		t.Fatalf("stage cell missing:\n%s", got)
	}
	if dw(row[:strings.Index(row, "コーディ")]) != dw(hdr[:col]) {
		t.Fatalf("columns do not line up:\n%s\n%s", hdr, row)
	}
}

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripSGR(s string) string { return sgr.ReplaceAllString(s, "") }

func TestNoColorHasNoEscapes(t *testing.T) {
	v := fixture()
	v.Project.Kill.On = true
	for _, w := range []int{120, 80, 50, 38} {
		if got := frame(v, w, 0); strings.Contains(got, "\x1b") {
			t.Fatalf("width %d has an escape: %q", w, got)
		}
	}
}

func TestColourFrameShape(t *testing.T) {
	v := fixture()
	got := frame(v, 100, 0, func(o *RenderOptions) { o.Color = true })
	golden(t, "colour_100", got)
	// Only SGR sequences, each closed by a reset on its line; killed banners
	// are reverse video (readable on light and dark).
	rest := sgr.ReplaceAllString(got, "")
	if strings.Contains(rest, "\x1b") {
		t.Fatal("something other than SGR")
	}
	if !strings.Contains(got, "\x1b[1;7;31m") {
		t.Fatal("killed banner is not reverse video")
	}
	for _, l := range strings.Split(got, "\n") {
		if strings.Count(l, "\x1b[") != 2*strings.Count(l, "\x1b[0m") && strings.Count(l, "\x1b[") != strings.Count(l, "\x1b[0m")*2 {
			t.Fatalf("an unclosed colour on %q", l)
		}
	}
}

func TestAsciiMode(t *testing.T) {
	v := fixture()
	v.Conn = Connection{Kind: ConnStale, Age: 50 * time.Second}
	for _, w := range []int{120, 80, 50} {
		got := frame(v, w, 0, func(o *RenderOptions) { o.ASCII = true; o.Footer = []string{"killing..."} })
		for _, l := range strings.Split(got, "\n") {
			for _, r := range l {
				if r > 0x7e {
					t.Fatalf("width %d: non-ASCII %q in %q", w, r, l)
				}
			}
		}
	}
	golden(t, "ascii_80", frame(fixture(), 80, 0, func(o *RenderOptions) { o.ASCII = true }))
}

func TestNotionalAndBurnAndFlagsShown(t *testing.T) {
	got := frame(fixture(), 120, 0)
	for _, want := range []string{"NOTIONAL", "FAST", "SILENT", "LOST", "OVER", "near deadline", "KILLED by dimi@x.io", "no cap", "4 runs", "0.8 run-hours", "r2", "burn …"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(got, "computeUsd") || strings.Contains(got, "compute $") {
		t.Fatal("no compute dollars in M9c")
	}
}

func TestSlugNeverRendered(t *testing.T) {
	v := fixture()
	v.Repos[0].Slug = "SLUG-SECRET-KEY"
	if got := frame(v, 120, 0); strings.Contains(got, "SLUG-SECRET-KEY") {
		t.Fatal("the wire slug was drawn")
	}
}

// On a short terminal the connection line and the project kill banner come
// first, so the renderer can never cut them.
func TestFrameShortTerminalPinsStatusAndBanner(t *testing.T) {
	v := fixture()
	v.Conn = Connection{Kind: ConnOffline, Reason: "connection lost", Age: 50 * time.Second}
	v.Project.Kill = KillState{On: true, By: "ops@x.io", At: t0.Add(-time.Hour), Reason: "budget review"}
	got := frame(v, 100, 6)
	if n := strings.Count(got, "\n") + 1; n > 6 {
		t.Fatalf("%d lines for 6 rows:\n%s", n, got)
	}
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[0], "OFFLINE") && !strings.Contains(strings.ToLower(lines[0]), "offline") || !strings.Contains(lines[1], "PROJECT KILLED") {
		t.Fatalf("status and banner must lead:\n%s", got)
	}
	golden(t, "short_6_killed_100", got)
	for h := 1; h <= 12; h++ {
		if n := strings.Count(frame(v, 100, h), "\n") + 1; n > h {
			t.Fatalf("height %d: %d lines", h, n)
		}
	}
}

func TestFrameShowsRecipe(t *testing.T) {
	v := fixture()
	v.Repos[0].Runs[0].Recipe = "claude-solo"
	if out := frame(v, 140, 0); !strings.Contains(out, "opus / sonnet · claude-solo") {
		t.Fatalf("frame:\n%s", out)
	}
}

func TestFrameRecipeMixedRowsAligned(t *testing.T) {
	v := fixture()
	v.Repos[0].Runs[0].Recipe = "claude-solo"
	out := frame(v, 140, 0)
	var a, b string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "r-aaaa11"):
			a = l
		case strings.Contains(l, "r-bbbb22"):
			b = l
		}
	}
	if a == "" || b == "" || !strings.Contains(a, "claude-solo") || strings.Contains(b, "claude") {
		t.Fatalf("frame:\n%s", out)
	}
	if ia, ib := utf8.RuneCountInString(a[:strings.Index(a, "unit")]), utf8.RuneCountInString(b[:strings.Index(b, "unit")]); ia != ib {
		t.Fatalf("verify column at %d and %d:\n%s", ia, ib, out)
	}
}

// An expanded run shows its detail: stage and round, verify, the PR, spent,
// the deadline and the models with the recipe (design generic-tool §10.2).
// The design also lists the last action and the token counts; the registry
// has neither field yet (plan Task 3, held out of this release), so this
// golden has no lines for them.
func TestFrameSelectedRunDetail(t *testing.T) {
	v := fixture()
	v.Repos[0].Runs[0].PRURL = "https://github.com/acme/app/pull/42"
	v.Repos[0].Runs[0].DeadlineAt = t0.Add(45 * time.Minute).UnixMilli()
	sel := Cursor{Slug: v.Repos[0].Slug, Run: v.Repos[0].Runs[0].Run}
	got := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if !strings.Contains(got, "▸ r-aaaa11") {
		t.Fatalf("selected run not marked:\n%s", got)
	}
	golden(t, "detail-120", got)
}

// Every expanded run shows its detail, not only the selected one: expanded
// is the truth (orchestrator ruling), so two expanded runs in the same
// block both show their lines even though only one can be selected.
func TestFrameEveryExpandedRunShowsDetail(t *testing.T) {
	v := fixture()
	a, b := v.Repos[0].Runs[0], v.Repos[0].Runs[1]
	ca, cb := Cursor{Slug: v.Repos[0].Slug, Run: a.Run}, Cursor{Slug: v.Repos[0].Slug, Run: b.Run}
	got := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = ca, map[Cursor]bool{ca: true, cb: true}
	})
	n := 0
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "    deadline") { // the detail line, not a FLAGS mention of "deadline"
			n++
		}
	}
	if n != 2 {
		t.Fatalf("want both runs' detail (2 deadline lines), got %d:\n%s", n, got)
	}
}

// At tier 2 the detail models line must still show the recipe suffix, and a
// notional (oauth) run's spent line must still show " NOTIONAL" (design
// generic-tool §10.2): both suffixes are easy to drop by accident at a
// narrower width.
func TestFrameSelectedRunDetailTier2Notional(t *testing.T) {
	v := fixture()
	run := &v.Repos[0].Runs[1] // r-bbbb22: oauth, notional, near deadline
	run.Recipe = "claude-solo"
	sel := Cursor{Slug: v.Repos[0].Slug, Run: run.Run}
	got := frame(v, 80, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if !strings.Contains(got, "NOTIONAL") {
		t.Fatalf("detail spent line dropped NOTIONAL:\n%s", got)
	}
	if !strings.Contains(got, "claude-solo") {
		t.Fatalf("detail models line dropped the recipe:\n%s", got)
	}
	golden(t, "detail-80-notional", got)
}

// The key line and the help screen must describe what space actually does
// now that the cursor selects runs, not only repositories: it expands a
// run's detail as well as folding a repository.
func TestKeyLineAndHelpNameRunsAndDetail(t *testing.T) {
	got := frame(fixture(), 120, 0, func(o *RenderOptions) { o.Keys = true })
	if !strings.Contains(got, "space fold/expand") {
		t.Fatalf("key line must say fold/expand:\n%s", got)
	}
	help := frame(fixture(), 120, 0, func(o *RenderOptions) { o.Help = true })
	if !strings.Contains(help, "select a repository or run") {
		t.Fatalf("help must say the cursor selects a repository or a run:\n%s", help)
	}
	if !strings.Contains(help, "fold the selected repository, or show/hide the selected run's detail") {
		t.Fatalf("help must describe both of space's behaviours:\n%s", help)
	}
}

func TestFrameRecipeLongNameClipped(t *testing.T) {
	v := fixture()
	long := strings.Repeat("abcdefghij", 3)
	v.Repos[0].Runs[0].Recipe = long
	out := frame(v, 160, 0)
	if strings.Contains(out, long) || !strings.Contains(out, long[:20]) {
		t.Fatalf("frame:\n%s", out)
	}
}
