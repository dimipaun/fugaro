package watch

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The screen of `fugaro watch`: a pure function from a View (plus what the
// user has done to the screen: selection, folds, scroll) to a frame of text
// lines. No terminal and no Bubble Tea in here, so every state is a golden
// file. Everything in a View was sanitised by Build; this file only adds its
// own glyphs, spaces and (when colour is on) SGR colour codes. Every width is
// a DISPLAY width (wide, CJK and emoji characters take two columns), never a
// rune count.

// MinWidth is the narrowest screen drawn; below it a one-line notice shows.
const MinWidth = 40

const tooNarrow = "terminal too narrow (need 40 columns)"

// rw measures display width. It follows the locale for ambiguous-width
// characters, like the terminal does.
var rw = runewidth.NewCondition()

func dw(s string) int { return rw.StringWidth(s) }

// RenderOptions are the screen's inputs besides the View.
type RenderOptions struct {
	Width, Height int // columns and rows; Height 0 means unlimited (no scrolling)
	Project       string
	ASCII         bool // ! # . > instead of the warning sign, block characters and cursor
	Color         bool // SGR colour; off for NO_COLOR, TERM=dumb and --no-color
	Selected      Cursor
	Expanded      map[Cursor]bool // a run's detail lines show when Expanded[cursor-of-that-run]
	Collapsed     map[string]bool
	Scroll        int
	Follow        bool // bring the selected repository into view
	Help          bool
	Footer        []string // the prompt, notice or killing line; wrapped here, never clipped
	Keys          bool     // list the kill and resume keys in the help line
	Repo          string   // --repo as given
	// FilterAll is the finished-run filter's current state (design
	// generic-tool §10.3): false shows "showing active + recent · a: all",
	// true shows "showing all · a: recent". Always drawn, at every width.
	FilterAll bool
	// Ready is ReadyRowsOf the view before the finished-run filter hid
	// anything: a ready PR stays listed past --keep and --keep-count, since
	// those bound how much history to show, not what still needs a look
	// (design generic-tool §10.1). The caller computes it, since by the time
	// Render runs the row's own block may already be gone from v.
	Ready []ReadyRow
	// PRNote is a one-line reason the ready list's pull request states could
	// not be read from the operator's gh (not installed, not answering); it
	// is drawn faint above the key line, never once per row.
	PRNote string
}

// Frame is a drawn screen.
type Frame struct {
	Lines     []string
	Scroll    int // the scroll offset actually used, for the caller to keep
	MaxScroll int
}

// String is the frame as one text, lines joined by newlines.
func (f Frame) String() string { return strings.Join(f.Lines, "\n") }

// SelectedIndex is the index in v.Repos of the repository selected by slug; the
// first when slug is empty or gone, -1 with no repositories.
func SelectedIndex(v View, slug string) int {
	for i, r := range v.Repos {
		if r.Slug == slug {
			return i
		}
	}
	if len(v.Repos) > 0 {
		return 0
	}
	return -1
}

type glyphs struct{ warn, full, empty, cur, sep, ell, up, down string }

func glyphsOf(ascii bool) glyphs {
	if ascii {
		return glyphs{"!", "#", ".", ">", " | ", "...", "^", "v"}
	}
	return glyphs{"⚠", "▓", "░", "▸", " · ", "…", "↑", "↓"}
}

// seg is a piece of a line with its colour code ("" for none).
type seg struct{ t, c string }

const (
	cBold   = "1"
	cOK     = "32"
	cInfo   = "36"
	cWarn   = "1;33"
	cBad    = "1;31"
	cBanner = "1;7;31" // reverse video: readable on any background
	cFaint  = "2"
)

type rend struct {
	w   int
	g   glyphs
	col bool // colour on
	dim bool // the data is not live: draw the body greyed
}

func (r *rend) paint(c, t string) string {
	if !r.col || r.dim || c == "" || t == "" {
		return t
	}
	return "\x1b[" + c + "m" + t + "\x1b[0m"
}

// clipW cuts s to w display columns, marking the cut.
func (r *rend) clipW(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if dw(s) <= w {
		return s
	}
	return rw.Truncate(s, w, r.g.ell)
}

// fit is s clipped or padded to exactly w columns.
func (r *rend) fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return rw.FillRight(r.clipW(s, w), w)
}

// line joins segs into one line no wider than the screen, trailing blanks
// dropped.
func (r *rend) line(segs ...seg) string {
	last := -1
	for i, s := range segs {
		if strings.TrimSpace(s.t) != "" {
			last = i
		}
	}
	var b strings.Builder
	rem := r.w
	for i := 0; i <= last && rem > 0; i++ {
		t := segs[i].t
		if i == last {
			t = strings.TrimRight(t, " ")
		}
		t = r.clipW(t, rem)
		rem -= dw(t)
		b.WriteString(r.paint(segs[i].c, t))
	}
	out := b.String()
	if r.dim && r.col && out != "" {
		out = "\x1b[" + cFaint + "m" + out + "\x1b[0m"
	}
	return out
}

func (r *rend) join(sep string, parts []seg) []seg {
	var out []seg
	for i, p := range parts {
		if i > 0 {
			out = append(out, seg{sep, ""})
		}
		out = append(out, p)
	}
	return out
}

// wrap breaks s into lines of at most w display columns, at spaces where it
// can (a prompt is never clipped: the word to type must stay readable).
func wrap(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	var out []string
	for dw(s) > w {
		cut, cw, lastSpace := 0, 0, -1
		for i, c := range s {
			n := rw.RuneWidth(c)
			if cw+n > w {
				break
			}
			cw += n
			cut = i + len(string(c))
			if c == ' ' {
				lastSpace = i
			}
		}
		if cut == 0 { // a character wider than the screen
			break
		}
		if lastSpace > 0 {
			out = append(out, s[:lastSpace])
			s = s[lastSpace+1:]
			continue
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return append(out, s)
}

func tierOf(w int) int {
	switch {
	case w >= 100:
		return 3
	case w >= 70:
		return 2
	case w >= MinWidth:
		return 1
	}
	return 0
}

// Render draws v.
func Render(v View, o RenderOptions) Frame {
	if o.Width <= 0 {
		o.Width = 80
	}
	r := &rend{w: o.Width, g: glyphsOf(o.ASCII), col: o.Color}
	tier := tierOf(o.Width)
	if tier == 0 {
		return Frame{Lines: []string{r.clipW(tooNarrow, o.Width)}}
	}
	live := v.Conn.Kind == ConnLive || v.Conn.Kind == ConnPolling

	// Header: identity, then the connection.
	var top []string
	proj := o.Project
	if proj == "" {
		proj = "-"
	}
	hdr := []seg{{"fugaro watch", cBold}, {r.g.sep + proj + r.g.sep + v.Mode + r.g.sep + dashIfEmpty(v.Day), ""}}
	if tier == 1 {
		hdr = []seg{{proj, cBold}, {r.g.sep + v.Mode + r.g.sep + dashIfEmpty(v.Day), ""}}
	}
	top = append(top, r.line(hdr...))
	top = append(top, r.line(r.connSegs(v)...))
	if o.Repo != "" {
		top = append(top, r.line(seg{"narrowed to repository " + o.Repo, cFaint}))
	}
	if v.QueuedNote != "" {
		top = append(top, r.line(seg{r.g.warn + " " + v.QueuedNote, cWarn}))
	}

	// From here the body is greyed when the data is not live.
	r.dim = !live
	top = append(top, r.scope(tier, "TOTAL", v.Project.Bar, v.Project.Counted, v.Project.Spent, v.Project.Notional, v.Project.Burn, "")...)
	top = append(top, r.line(seg{fmt.Sprintf("%-6s %d runs%s%.1f run-hours", "", v.Project.Runs, r.g.sep, v.Project.RunHours), ""}))
	if k := v.Project.Kill; k.On {
		top = append(top, r.line(seg{killText(k, r.g.warn, "PROJECT KILLED"), cBanner}))
	}

	if tier >= 2 && !o.Help {
		top = append(top, r.colHeader(tier))
	}

	// Body.
	var body []string
	selTop, selBottom := 0, 0
	// The selection falls back to the first block's header when it names no
	// block in v (SelectedIndex's fallback, kept for a cursor the caller has
	// not resolved yet: an empty Selected, or one from a stale view).
	sel := o.Selected
	if si := SelectedIndex(v, sel.Slug); si >= 0 && v.Repos[si].Slug != sel.Slug {
		sel = Cursor{Slug: v.Repos[si].Slug}
	}
	if o.Help {
		body = r.helpLines(o.Keys)
	} else {
		for _, b := range v.Repos {
			before := len(body)
			lines, rowTop, rowBottom := r.repo(tier, b, sel, o.Expanded, o.Collapsed[b.Slug])
			body = append(body, lines...)
			if rowTop >= 0 {
				selTop, selBottom = before+rowTop, before+rowBottom
			}
		}
		if len(v.Repos) == 0 {
			msg := "no repository has spend, runs or a kill switch today"
			if v.Conn.Reason == "connecting" {
				msg = "waiting for the first data..."
				if !o.ASCII {
					msg = "waiting for the first data…"
				}
			}
			body = append(body, r.line(seg{msg, cFaint}))
		}
	}
	if !o.Help && len(o.Ready) > 0 {
		body = append(body, r.readyForReview(o.Ready)...)
	}

	// Footer: the prompt or notice (wrapped), then the key line (which leads
	// with the finished-run filter's state, design generic-tool §10.3, so it
	// is never the part a narrow width clips away).
	r.dim = false
	var foot []string
	for _, f := range o.Footer {
		for _, l := range wrap(f, o.Width) {
			foot = append(foot, r.line(seg{l, cWarn}))
		}
	}
	if o.PRNote != "" {
		foot = append(foot, r.line(seg{o.PRNote, cFaint}))
	}
	if !live && o.Keys {
		foot = append(foot, r.line(seg{"kill and resume keys are off until the data is live", cFaint}))
	}
	foot = append(foot, r.line(seg{r.keyLine(tier, o.Keys, live, o.FilterAll), cFaint}))

	// A terminal too short for the header, a body of three rows and the footer
	// would have the renderer cut the top lines, exactly the connection line
	// and the project kill banner: pin those first and drop the rest.
	if o.Height > 0 && o.Height < len(top)+len(foot)+3 {
		pinned := []string{top[1]}
		if k := v.Project.Kill; k.On {
			pinned = append(pinned, r.line(seg{killText(k, r.g.warn, "PROJECT KILLED"), cBanner}))
		}
		pinned = append(pinned, foot...)
		pinned = append(pinned, r.line(seg{fmt.Sprintf("terminal too short (%d rows): enlarge it for the full view", o.Height), cFaint}))
		if len(pinned) > o.Height {
			pinned = pinned[:o.Height]
		}
		return Frame{Lines: pinned}
	}

	// Viewport.
	bodyH := len(body)
	if o.Height > 0 {
		bodyH = max(o.Height-len(top)-len(foot), 3)
	}
	lines, scroll, maxScroll := r.viewport(body, bodyH, o.Scroll, selTop, selBottom, o.Follow && !o.Help)
	out := append(append(top, lines...), foot...)
	return Frame{Lines: out, Scroll: scroll, MaxScroll: maxScroll}
}

// viewport shows body[scroll:] in h rows, with one row each for "more above"
// and "more below" when the body does not fit. selTop and selBottom are the
// first and last line of the selected row, the latter past the end of an
// expanded run's detail: following must keep both ends in view, not just the
// top (an expanded run near the bottom must not have its detail scrolled
// away while only its own row stays on screen).
func (r *rend) viewport(body []string, h, scroll, selTop, selBottom int, follow bool) ([]string, int, int) {
	total := len(body)
	if total <= h {
		return body, 0, 0
	}
	maxScroll := total - (h - 1)
	rows := func(s int) int {
		n := h
		if s > 0 {
			n--
		}
		if s+n < total {
			n--
		}
		return n
	}
	scroll = min(max(scroll, 0), maxScroll)
	if follow {
		if selTop < scroll {
			scroll = selTop
		} else if n := rows(scroll); selBottom >= scroll+n {
			// Bring the selection's bottom into view, leaving the two rows
			// below it the fudge asks for when the window has them to spare.
			want := min(max(selBottom-(h-3), 0), maxScroll)
			// The top of the selection wins when the window at selTop
			// already reaches the bottom: an expanded run keeps its own row
			// on screen whenever both ends can be shown at once. When they
			// cannot — a run whose detail is taller than the rows the two
			// "more" lines leave — the bottom wins, because the detail is
			// what the row was expanded for; the run's own row goes off the
			// top, and the "more above" row is what says so.
			if want > selTop && selBottom < selTop+rows(selTop) {
				want = selTop
			}
			scroll = want
		}
	}
	n := rows(scroll)
	var out []string
	if scroll > 0 {
		out = append(out, r.line(seg{fmt.Sprintf("%s %d more above", r.g.up, scroll), cFaint}))
	}
	out = append(out, body[scroll:scroll+n]...)
	if below := total - (scroll + n); below > 0 {
		out = append(out, r.line(seg{fmt.Sprintf("%s %d more below", r.g.down, below), cFaint}))
	}
	return out, scroll, maxScroll
}

func (r *rend) connSegs(v View) []seg {
	c := v.Conn
	switch c.Kind {
	case ConnStale, ConnOffline:
		t := ConnText(c, r.g.warn)
		if c.Age > 0 {
			t += fmt.Sprintf("%slast data %s ago", r.g.sep, durText(c.Age))
		}
		return []seg{{t, cWarn}}
	case ConnRefused:
		return []seg{{ConnText(c, r.g.warn) + r.g.sep + "q quits", cBad}}
	case ConnPolling:
		return []seg{{"polling", cInfo}}
	}
	return []seg{{"live", cOK}}
}

func (r *rend) keyLine(tier int, keys, live, filterAll bool) string {
	sel := "↑↓ select"
	if r.g.ell == "..." { // --ascii
		sel = "Up/Down select"
	}
	// The filter note leads (design generic-tool §10.3): line() clips later
	// segments first, so this is the one part of the line a narrow width
	// never drops.
	parts := []string{r.filterNote(filterAll)}
	switch {
	case tier == 3 && keys:
		parts = append(parts, sel, "space fold/expand", "x dismiss", "PgUp/PgDn scroll", "k/K kill repo/project", "r/R resume", "? help", "q quit")
	case tier == 3:
		parts = append(parts, sel, "space fold/expand", "x dismiss", "PgUp/PgDn scroll", "? help", "q quit")
	case tier == 2 && keys:
		parts = append(parts, sel, "space fold/expand", "x dismiss", "k/K kill", "r/R resume", "? help", "q quit")
	case tier == 2:
		parts = append(parts, sel, "space fold/expand", "x dismiss", "? help", "q quit")
	default:
		parts = append(parts, sel, "? help", "q quit")
	}
	return strings.Join(parts, r.g.sep)
}

func (r *rend) helpLines(keys bool) []string {
	rows := [][2]string{
		{"Up Down", "select a repository or run"},
		{"PgUp PgDn", "scroll"},
		{"space", "fold the selected repository, or show/hide the selected run's detail"},
		{"a", "toggle finished runs: recent / all"},
		{"x", "dismiss the selected finished run at once"},
	}
	if keys {
		rows = append(rows,
			[2]string{"k", "kill the selected repository"},
			[2]string{"K", "kill the WHOLE PROJECT (type its name)"},
			[2]string{"r", "resume the selected repository (type its name)"},
			[2]string{"R", "resume the WHOLE PROJECT (type its name)"})
	}
	rows = append(rows, [2]string{"?", "this help"}, [2]string{"q Esc Ctrl-C", "leave; the cloud jobs keep running"})
	out := []string{r.line(seg{"Keys", cBold})}
	for _, kv := range rows {
		out = append(out, r.line(seg{"  " + r.fit(kv[0], 13), cBold}, seg{kv[1], ""}))
	}
	// The rule behind the a and x keys, where a viewer looks for the keys
	// themselves: a finished run leaves the list on its own, whatever its
	// status, and a brings the ones it took away back (owner spec 2026-10-22,
	// items 3 and 5). The two flags that move the window are named, since a
	// screen left open is where a person looks for them; both reach a failed
	// row like any other, so raising --keep never hides a failure a success
	// of the same age still shows.
	out = append(out, r.line(seg{"A finished run leaves the list " + durText(FinishedKeep) + " after it finishes; a shows them all again.", cFaint}))
	// The flags that move the window, on a line of their own: both reach a
	// failed row like any other, and a failure is never kept for a shorter
	// time than a success, so raising --keep shows more failures, not fewer
	// (0.10.2 fix of #950).
	out = append(out, r.line(seg{"--keep and --keep-count move that window; failed runs included, never for less.", cFaint}))
	if keys {
		out = append(out, r.line(seg{"Kill and resume are off while the data is stale or offline.", cFaint}))
	}
	return out
}

// barStr is a bar of w cells filled to pct.
func (r *rend) barStr(pct float64, w int) string {
	n := int(pct/100*float64(w) + 0.5)
	n = min(max(n, 0), w)
	return strings.Repeat(r.g.full, n) + strings.Repeat(r.g.empty, w-n)
}

func pctColour(p float64) string {
	switch {
	case p >= 100:
		return cBad
	case p >= warnPercent:
		return cWarn
	}
	return cOK
}

// figures are the money of the project or one repository, most important
// first (a narrow screen loses the tail): the cap bar, counted against the
// cap, the burn rate, then spent and the labelled notional dollars.
func (r *rend) figures(tier int, b Bar, counted, spent, notional budget.Micros, burn Burn, label bool) []seg {
	var parts []seg
	if b.Has && tier >= 2 {
		w := 10
		if tier == 3 {
			w = 16
		}
		parts = append(parts, seg{r.barStr(b.Percent, w), pctColour(b.Percent)})
	}
	switch {
	case b.Has && tier >= 2:
		lbl := ""
		if label {
			lbl = "counted "
		}
		parts = append(parts, seg{fmt.Sprintf("%s%s of %s (%.0f%%%s)", lbl, USD(b.Used), USD(b.Cap), b.Percent, capWarn(b.Percent, r.g.warn)), ""})
	case b.Has:
		parts = append(parts, seg{fmt.Sprintf("%.0f%% of %s", b.Percent, USD(b.Cap)), pctColour(b.Percent)})
	case tier >= 2:
		parts = append(parts, seg{"counted " + USD(counted) + ", no cap", ""})
	default:
		parts = append(parts, seg{USD(counted) + ", no cap", ""})
	}
	bt, bc := "burn "+r.g.ell, ""
	if burn.Known {
		bt = "burn " + USD(burn.PerMin) + "/min"
		if burn.Fast {
			bt += " " + r.g.warn + " FAST"
			bc = cWarn
		}
	}
	parts = append(parts, seg{bt, bc})
	st := "spent " + USD(spent)
	if tier == 1 {
		st = USD(spent)
	}
	if notional > 0 {
		st += " + " + USD(notional) + " NOTIONAL"
	}
	parts = append(parts, seg{st, ""})
	out := make([]seg, 0, 2*len(parts))
	for i, p := range parts {
		if i > 0 {
			sep := r.g.sep
			if i == 1 && b.Has && tier >= 2 {
				sep = " " // the bar and its figure read as one
			}
			out = append(out, seg{sep, ""})
		}
		out = append(out, p)
	}
	return out
}

func (r *rend) scope(tier int, label string, b Bar, counted, spent, notional budget.Micros, burn Burn, _ string) []string {
	segs := append([]seg{{r.fit(label, 7), cBold}}, r.figures(tier, b, counted, spent, notional, burn, true)...)
	return []string{r.line(segs...)}
}

// runCols are the widths of the run table (tiers 2 and 3).
type runCols struct{ run, title, stage, round, age, spend, by, flags int }

// launcherCol is the width of the table's BY column: who started a run, in
// the short form a column has room for (launcherText). Nine columns hold a
// name, the local part of an address, or a service account's name (ci-runner)
// whole; a longer identity is clipped like every other over-long cell, and the
// expanded detail has it whole.
const launcherCol = 9

// launcherColNarrow is tier 2's BY column: that tier starts at 70 columns,
// where the flags cell behind the launcher needs the column more than the
// launcher does (launcherCol is what tier 3 spends).
const launcherColNarrow = 8

// colsOf is the run table's column widths. Every cell but the title has a
// fixed width, so the title takes what is left, never less than its own
// minimum; a row whose fixed cells and title floor do not fit is cut off at
// the screen's edge, which takes the flags with it. So the fixed widths are
// budgeted to fit the width their tier starts at: 70 columns for tier 2, 120
// for tier 3 (the width the widest golden draws).
// TestRunRowsFitAtEachTiersEntryWidth pins that.
//
// The BY column is the one cell the budgets had to absorb, and it comes out
// of the cells with room to give: run is exactly shortRunLen, because the
// tail form (shortRun) is never longer, so the column the old eight-wide run
// cell kept for an ellipsis was dead weight; tier 3's flags gives up one of
// its thirty columns; and the rest is the title's, the elastic cell: five of
// the fifteen columns it had at 120 columns of screen, where the row was
// clipping it anyway. Tier 2 starts at 70 columns and has no slack anywhere
// else, so it gives up five of its flags column's eighteen columns and one of
// its own BY cell's. Both tiers end up whole where they were: a tier-2 row
// fits at the 70 columns its tier starts at, and a tier-3 row at 120. Dropping
// the trailing VERIFY and MODELS columns (owner request 2026-10-24) returns
// their columns to the title, the cell the clip was eating: at 120 columns the
// tier-3 title is 33 wide where its floor of 10 was binding.
//
// The trailing constants leave the elastic row one column short of the
// screen's edge, so a cell is clipped by its own width and never by the
// screen: a tail cell keeps its last column whatever the row holds.
func colsOf(tier, w int) runCols {
	if tier == 3 {
		c := runCols{run: shortRunLen, stage: 9, round: 3, age: 7, spend: 15, by: launcherCol, flags: 29}
		c.title = max(10, w-(2+c.run+c.stage+c.round+c.age+c.spend+c.by+c.flags+9))
		return c
	}
	c := runCols{run: shortRunLen, stage: 8, round: 3, age: 7, spend: 9, by: launcherColNarrow, flags: 13}
	c.title = max(8, w-(2+c.run+c.stage+c.round+c.age+c.spend+c.by+c.flags+8))
	return c
}

func rowFlags(run RunRow, warn string, tier int) string {
	f := flags(run, warn)
	// The waiting sentence is the longest flag there is — 59 columns, past
	// every FLAGS column this table has (tier 3's is 30) — and its instant is
	// the part a clip would eat, taking any flag behind it (SILENT, a halt)
	// with it. So no tier's row carries it: the row says only that the run is
	// waiting, and the detail has the room for the instant and the sentence
	// (design §10.2, the same reason LOST drops its parenthetical). Tier 3
	// shortens it for the same reason the narrower tiers do, which is the
	// column and not the tier.
	repl := []string{"waiting for a credential, expected resume " + stampText(run.WaitingUntil), "waiting"}
	if tier < 3 {
		repl = append(repl, "LOST (sweeper pending)", "LOST", "OVER deadline", "OVER", "near deadline", "near")
	}
	f = strings.NewReplacer(repl...).Replace(f)
	if tier < 3 && run.Notional {
		if f != "" {
			f = "NOTIONAL, " + f
		} else {
			f = "NOTIONAL"
		}
	}
	return f
}

func flagColour(run RunRow) string {
	switch {
	case run.Queued && run.Stuck:
		return cWarn
	case run.Queued:
		return ""
	case run.Health == HealthLost || run.Deadline == DeadlineOver:
		return cBad
	case run.Health == HealthSilent || run.Deadline == DeadlineNear || run.Halted != "":
		return cWarn
	case run.Waiting:
		// Amber, not red: a run waiting for a credential is held up, not
		// failing — the cascade resumes it by itself (D152).
		return cWarn
	}
	return ""
}

// repo is a repository block: its header, its kill banner, its runs, and
// every expanded run's detail (expanded is the truth: a run shows its
// detail whenever its cursor is in the map, whether or not it is also the
// selected row). selTop and selBottom are the first and last line of the
// selected row within the returned lines (selBottom past an expanded
// selected run's own detail), or -1 when sel names no row of this block.
func (r *rend) repo(tier int, b RepoBlock, sel Cursor, expanded map[Cursor]bool, collapsed bool) (lines []string, selTop, selBottom int) {
	headerSel := sel.Slug == b.Slug && sel.Run == ""
	cur, nameC := " ", ""
	if headerSel {
		cur, nameC = r.g.cur, cBold
	}
	var name seg
	switch tier {
	case 3:
		name = seg{r.fit(b.Name, 24), nameC}
	case 2:
		name = seg{r.fit(b.Name, 18), nameC}
	default:
		name = seg{r.clipW(b.Name, 14) + " ", nameC}
	}
	hiddenCount := len(b.Runs) + len(b.Finished)
	segs := append([]seg{{cur + " ", nameC}, name, {" ", ""}}, r.figures(tier, b.Bar, b.Counted, b.Spent, b.Notional, b.Burn, false)...)
	if collapsed && hiddenCount > 0 {
		segs = append(segs, seg{fmt.Sprintf(" [%d runs hidden]", hiddenCount), cFaint})
	}
	out := []string{r.line(segs...)}
	selTop, selBottom = -1, -1
	if headerSel {
		selTop, selBottom = 0, 0
	}
	if b.Kill.On {
		out = append(out, r.line(seg{"  " + killText(b.Kill, r.g.warn, "KILLED"), cBanner}))
	}
	if collapsed {
		return out, selTop, selBottom
	}
	c := colsOf(tier, r.w)
	for _, run := range b.Runs {
		runSel := sel.Slug == b.Slug && sel.Run != "" && sel.Run == run.Run
		idx := len(out)
		out = append(out, r.runRows(tier, c, run, runSel)...)
		last := len(out) - 1
		if expanded[Cursor{Slug: b.Slug, Run: run.Run}] {
			out = append(out, r.detail(run)...)
			last = len(out) - 1
		}
		if runSel {
			selTop, selBottom = idx, last
		}
	}
	for _, run := range b.Finished {
		runSel := sel.Slug == b.Slug && sel.Run != "" && sel.Run == run.Run
		idx := len(out)
		out = append(out, r.finishedRow(run, runSel))
		if runSel {
			selTop, selBottom = idx, idx
		}
	}
	return out, selTop, selBottom
}

// finishedRow is one line for a run whose result.json already exists (design
// generic-tool §10.3): the run id, its outcome and, once it has one, its PR
// link, coloured to flag a failure or a review-ready success. The RUN cell is
// the same short form every row of the table shows (shortRun, owner request
// 2026-10-24): the id's leading characters are the launch stamp, so a whole
// id here read unlike every row above it. Unlike an active run, a finished row
// cannot be expanded — repo draws the detail for b.Runs alone — so the whole
// id, the only form `fugaro logs RUN` and `fugaro diagnose RUN` accept, is not
// on this screen for a finished run at all: `--json` and `--plain` print it
// (design generic-tool §10.3).
func (r *rend) finishedRow(run RunRow, selected bool) string {
	cur := " "
	if selected {
		cur = r.g.cur
	}
	word, c := strings.ToUpper(run.Stage), ""
	if run.Failed {
		c = cBad
	}
	switch run.Outcome {
	case "ready":
		word, c = "ready for review", cOK
	case "draft":
		word += ", passed review"
	case "artifacts":
		// A mode: artifact run succeeded, and its deliverable is in the
		// runs bucket: the bare stage word (SUCCEEDED) would be the one
		// thing an owner must not read here, since it did not do what a
		// succeeded implement run does (design artifact-runs.md §3).
		word, c = "artifacts, no PR", cOK
	}
	segs := []seg{{cur + " " + shortRun(run.Run) + " ", ""}, {dash(run.Title) + " ", cBold}, {word, c}}
	if run.PRURL != "" {
		segs = append(segs, seg{" " + run.PRURL, ""})
	}
	segs = append(segs, seg{r.g.sep + ageText(run) + " ago", cFaint})
	return r.line(segs...)
}

// filterNote is the dashboard's current finished-run filter, always shown
// (design generic-tool §10.3): "a" toggles between the two.
func (r *rend) filterNote(all bool) string {
	if all {
		return "showing all" + r.g.sep + "a: recent"
	}
	return "showing active + recent" + r.g.sep + "a: all"
}

// ReadyRow is one row of the "Ready for your review" section: a finished
// run with its repository already resolved to a display name, since by the
// time it is drawn the run's own block may be gone from the (filtered) view
// (design generic-tool §10.1: a ready item outlives the recency filter).
type ReadyRow struct {
	Repo string
	Run  RunRow
}

// ReadyRowsOf resolves ReadyForReview(v) to its repositories' display names,
// while every block is still in v (before a filter can drop one).
func ReadyRowsOf(v View) []ReadyRow {
	var out []ReadyRow
	for _, row := range ReadyForReview(v) {
		out = append(out, ReadyRow{Repo: repoNameOf(v, row.Slug), Run: row})
	}
	return out
}

func repoNameOf(v View, slug string) string {
	for _, b := range v.Repos {
		if b.Slug == slug {
			return b.Name
		}
	}
	return "-"
}

// readyForReview is the "Ready for your review" section (design
// generic-tool §10.1): a header with the count, then one line per run with
// its repository, PR number and link, and its title.
func (r *rend) readyForReview(ready []ReadyRow) []string {
	out := []string{"", r.line(seg{fmt.Sprintf("Ready for your review (%d)", len(ready)), cBold})}
	for _, item := range ready {
		row := item.Run
		pr := "-"
		if row.PRURL != "" {
			pr = row.PRURL
		}
		out = append(out, r.line(
			seg{"  " + r.fit(item.Repo, 18) + " ", ""},
			seg{r.fit(fmt.Sprintf("#%d", row.PRNumber), 6) + " ", ""},
			seg{pr + "  ", cInfo},
			seg{dash(row.Title), cFaint},
		))
	}
	return out
}

// shortRunLen is how many characters of a run id a table shows: the last four
// (shortRun).
const shortRunLen = 4

// shortRun is a run's id as a table cell shows it: its LAST four characters.
// A run id is a launch stamp plus a random suffix (20261023-021512-abcd), so
// any prefix is shared — the first eight columns are the day, the first
// thirteen the minute — and two runs started together would read the same.
// The suffix is what tells them apart (owner spec 2026-10-22, item 1). The
// expanded detail and --json carry the id whole, so this is what the column
// shows, never all the screen knows. An id of four characters or fewer is its
// own short form.
func shortRun(run string) string {
	r := []rune(run)
	if len(r) <= shortRunLen {
		return run
	}
	return string(r[len(r)-shortRunLen:])
}

func (r *rend) runRows(tier int, c runCols, run RunRow, selected bool) []string {
	rnd := "-"
	if run.Round != "-" {
		rnd = "r" + run.Round
	}
	fl := rowFlags(run, r.g.warn, tier)
	fc := flagColour(run)
	cur := " "
	if selected {
		cur = r.g.cur
	}
	if tier == 1 {
		l1 := r.line(seg{cur + " " + r.fit(shortRun(run.Run), c.run) + " ", ""}, seg{run.Title, cBold})
		segs := []seg{{"    " + run.Stage + " " + rnd + " " + ageText(run), ""}}
		if fl != "" {
			segs = append(segs, seg{" " + fl, fc})
		}
		sp := "-"
		if run.HasSpent {
			sp = USD(run.Spent) // the NOTIONAL word is in the flags here
		}
		segs = append(segs, seg{" " + sp, ""})
		return []string{l1, r.line(segs...)}
	}
	sp := runSpend(run)
	if tier < 3 {
		sp = "-"
		if run.HasSpent {
			sp = USD(run.Spent)
		}
	}
	segs := []seg{
		{cur + " " + r.fit(shortRun(run.Run), c.run) + " ", ""}, {r.fit(run.Title, c.title) + " ", cBold},
		{r.fit(run.Stage, c.stage) + " ", ""}, {r.fit(rnd, c.round) + " ", ""},
		{r.fit(ageText(run), c.age) + " ", ""}, {r.fit(sp, c.spend) + " ", ""},
		{r.fit(launcherText(run), c.by) + " ", ""},
		{r.fit(fl, c.flags), fc},
	}
	return []string{r.line(segs...)}
}

// detail is a selected run's expanded lines (design generic-tool §10.2):
// stage and round, verify, the PR, spent (or notional) dollars, the stage
// deadline, and the models with the recipe. It also names the launcher whole:
// the table's BY column holds the part of an address before its @ (launcherText),
// and several people start runs on one project at once, so the whole identity —
// what tells a service account from a person — is what the expanded row is for.
// The design also lists the last action and the token counts; the registry has
// no action or tokens field yet (that is plan Task 3, held out of this
// release), so those two lines are left out rather than guessed.
func (r *rend) detail(run RunRow) []string {
	rnd := "-"
	if run.Round != "-" {
		rnd = "r" + run.Round
	}
	pr := "-"
	if run.PRURL != "" {
		pr = run.PRURL
	}
	sp := "-"
	if run.HasSpent {
		sp = USD(run.Spent)
		if run.Notional {
			sp += " NOTIONAL"
		}
	}
	dl := "-"
	if run.DeadlineAt > 0 {
		dl = stampText(run.DeadlineAt)
	}
	models := run.Models
	if run.Recipe != "" {
		models += " · " + run.Recipe
	}
	// The launcher is shown whole here, where the BY column of the run table
	// and of the plain frames holds only the part before an address's @: the
	// detail has the room, and the whole value is what tells a person from an
	// automated launch (a service account's full address). A run whose
	// launch.json recorded no launcher shows the dash, like every other line
	// with nothing to say.
	rows := [][2]string{
		// The whole run id: the table's RUN column shows only its last four
		// characters (shortRun), so this is where the id a viewer needs for
		// `fugaro logs <run>` and the like is on the screen at all.
		{"run", run.Run},
		{"stage", run.Stage + " " + rnd},
		{"by", dashIfEmpty(run.LaunchedBy)},
		{"verify", run.Verify},
		{"PR", pr},
		{"spent", sp},
		// Which credential the run is spending on: a name, never a value
		// (C1.21), and the dash for a run that has reported none — which is
		// "not recorded", not "no credential" (RunRow.Credential).
		{"credential", dashIfEmpty(run.Credential)},
		{"deadline", dl},
		{"models", models},
	}
	// A waiting run says so, and until when: the row's FLAGS has neither the
	// room for the instant nor, at any tier, the sentence at all
	// (rowFlags).
	if run.Waiting {
		rows = append(rows, [2]string{"waiting", "expected resume " + stampText(run.WaitingUntil)})
	}
	out := make([]string, 0, len(rows))
	for _, kv := range rows {
		// 10 columns: the widest label ("credential", the name of the
		// credential the run is spending on) must not be clipped.
		out = append(out, r.line(seg{"    " + r.fit(kv[0], 10), cFaint}, seg{" " + kv[1], ""}))
	}
	return out
}

func (r *rend) colHeader(tier int) string {
	c := colsOf(tier, r.w)
	return r.line(seg{"  " + r.fit("RUN", c.run) + " " + r.fit("TITLE", c.title) + " " + r.fit("STAGE", c.stage) + " " +
		r.fit("R", c.round) + " " + r.fit("AGE", c.age) + " " + r.fit("SPENT", c.spend) + " " +
		r.fit("BY", c.by) + " " + r.fit("FLAGS", c.flags), cFaint})
}
