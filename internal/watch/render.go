package watch

import (
	"fmt"
	"strings"
	"time"

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
	// recW is the room the widest recipe name needs after the models in the
	// tier-3 table; 0 when no run shows one.
	recW int
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
	r := &rend{w: o.Width, g: glyphsOf(o.ASCII), col: o.Color, recW: recipeWidth(v)}
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
			scroll = min(max(selBottom-(h-3), 0), maxScroll)
			if scroll > selTop { // never scroll the top of the selection off screen
				scroll = selTop
			}
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
		parts = append(parts, sel, "space fold/expand", "x ack", "PgUp/PgDn scroll", "k/K kill repo/project", "r/R resume", "? help", "q quit")
	case tier == 3:
		parts = append(parts, sel, "space fold/expand", "x ack", "PgUp/PgDn scroll", "? help", "q quit")
	case tier == 2 && keys:
		parts = append(parts, sel, "space fold/expand", "x ack", "k/K kill", "r/R resume", "? help", "q quit")
	case tier == 2:
		parts = append(parts, sel, "space fold/expand", "x ack", "? help", "q quit")
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
	case p >= 80:
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
		parts = append(parts, seg{fmt.Sprintf("%s%s of %s (%.0f%%)", lbl, USD(b.Used), USD(b.Cap), b.Percent), ""})
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
type runCols struct{ run, title, stage, round, age, spend, flags, verify, models int }

func colsOf(tier, w, recW int) runCols {
	if tier == 3 {
		c := runCols{run: 8, stage: 9, round: 3, age: 7, spend: 15, flags: 30, verify: 9, models: 14 + recW}
		c.title = max(10, w-(2+c.run+c.stage+c.round+c.age+c.spend+c.flags+c.verify+c.models+8))
		return c
	}
	c := runCols{run: 8, stage: 8, round: 3, age: 7, spend: 9, flags: 18}
	c.title = max(8, w-(2+c.run+c.stage+c.round+c.age+c.spend+c.flags+6))
	return c
}

func rowFlags(run RunRow, warn string, tier int) string {
	f := flags(run, warn)
	if tier < 3 {
		f = strings.NewReplacer("LOST (sweeper pending)", "LOST", "OVER deadline", "OVER", "near deadline", "near").Replace(f)
	}
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
	c := colsOf(tier, r.w, r.recW)
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
// link, coloured to flag a failure or a review-ready success.
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
	}
	segs := []seg{{cur + " " + run.Run + " ", ""}, {dash(run.Title) + " ", cBold}, {word, c}}
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
		l1 := r.line(seg{cur + " " + r.fit(run.Run, 8) + " ", ""}, seg{run.Title, cBold})
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
		{cur + " " + r.fit(run.Run, c.run) + " ", ""}, {r.fit(run.Title, c.title) + " ", cBold},
		{r.fit(run.Stage, c.stage) + " ", ""}, {r.fit(rnd, c.round) + " ", ""},
		{r.fit(ageText(run), c.age) + " ", ""}, {r.fit(sp, c.spend) + " ", ""},
		{r.fit(fl, c.flags), fc},
	}
	if tier == 3 {
		models := run.Models
		if run.Recipe != "" {
			models += " · " + r.clipW(run.Recipe, 24)
		}
		segs = append(segs, seg{" " + r.fit(run.Verify, c.verify) + " " + models, ""})
	}
	return []string{r.line(segs...)}
}

// detail is a selected run's expanded lines (design generic-tool §10.2):
// stage and round, verify, the PR, spent (or notional) dollars, the stage
// deadline, and the models with the recipe. The design also lists the last
// action and the token counts; the registry has no action or tokens field
// yet (that is plan Task 3, held out of this release), so those two lines
// are left out rather than guessed.
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
		dl = time.UnixMilli(run.DeadlineAt).UTC().Format("2006-01-02 15:04Z")
	}
	models := run.Models
	if run.Recipe != "" {
		models += " · " + run.Recipe
	}
	rows := [][2]string{
		{"stage", run.Stage + " " + rnd},
		{"verify", run.Verify},
		{"PR", pr},
		{"spent", sp},
		{"deadline", dl},
		{"models", models},
	}
	out := make([]string, 0, len(rows))
	for _, kv := range rows {
		out = append(out, r.line(seg{"    " + r.fit(kv[0], 9), cFaint}, seg{" " + kv[1], ""}))
	}
	return out
}

// recipeWidth is the width of " · <recipe>" for the widest recipe name among
// the runs, capped; 0 when no run has one.
func recipeWidth(v View) int {
	w := 0
	for _, b := range v.Repos {
		for _, run := range b.Runs {
			if run.Recipe != "" {
				w = max(w, 3+min(rw.StringWidth(run.Recipe), 24))
			}
		}
	}
	return w
}

func (r *rend) colHeader(tier int) string {
	c := colsOf(tier, r.w, r.recW)
	segs := []seg{
		{"  " + r.fit("RUN", c.run) + " " + r.fit("TITLE", c.title) + " " + r.fit("STAGE", c.stage) + " " +
			r.fit("R", c.round) + " " + r.fit("AGE", c.age) + " " + r.fit("SPENT", c.spend) + " " + r.fit("FLAGS", c.flags), cFaint},
	}
	if tier == 3 {
		segs = append(segs, seg{" " + r.fit("VERIFY", c.verify) + " MODELS", cFaint})
	}
	return r.line(segs...)
}
