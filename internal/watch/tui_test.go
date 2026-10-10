package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// tm is a model fed by hand: updates and keys go straight to Update.
type tm struct {
	t     *testing.T
	m     *model
	now   time.Time
	execs []Request
	stops int
	out   func(Request) Outcome
}

func newTM(t *testing.T) *tm {
	x := &tm{t: t, now: t0}
	x.out = func(r Request) Outcome {
		return Outcome{Written: true, Notice: "wrote " + string(rune('0'+int(r.Kind)))}
	}
	x.m = newModel(context.Background(), TUIOptions{
		Project: "aurora", Width: 100, Height: 30, NoColor: true, Now: func() time.Time { return x.now },
		Exec: func(_ context.Context, r Request) Outcome { x.execs = append(x.execs, r); return x.out(r) },
		Stop: func() { x.stops++ },
		// A temp file: the real default (AckPath's) must never be touched by a
		// test, and every test gets its own, so an ack in one never leaks into
		// another.
		AckPath: filepath.Join(t.TempDir(), "watch-acks.json"),
	})
	return x
}

func (x *tm) upd(us ...Update) {
	x.t.Helper()
	x.m.Update(supMsg{us: us})
	x.m.Update(renderMsg{})
}

func ev(src Source, path, data string, at time.Time) Update {
	return Update{Kind: UpdEvent, Src: src, Ev: rtdb.Event{Type: "put", Path: path, Data: json.RawMessage(data)}, Now: at}
}

// seedTM loads two repositories: acme/app (spent more) and acme/lib.
func (x *tm) seed() {
	at := x.now
	x.upd(
		Update{Kind: UpdDay, Day: budget.Day(at), Now: at},
		ev(SrcConfig, "/", `{"mode":"enforce","caps":{"global":{"dailyMicros":50000000}}}`, at),
		ev(SrcGlobal, "/", `{"spent":9000000,"counted":9000000}`, at),
		ev(SrcRepos, "/", `{"acme%2Fapp":{"spent":6000000,"counted":6000000},"acme%2Flib":{"spent":3000000,"counted":3000000}}`, at),
		ev(SrcAgents, "/", `{"acme%2Fapp":{"r1":`+agent("acme/app", "build the", "")+`},"acme%2Flib":{"r2":`+agent("acme/lib", "fix the o", "")+`}}`, at),
	)
}

func (x *tm) key(s string) tea.Cmd {
	x.t.Helper()
	var k tea.KeyMsg
	switch s {
	case "up":
		k = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		k = tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		k = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "pgdown":
		k = tea.KeyMsg{Type: tea.KeyPgDown}
	case "space":
		k = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	_, cmd := x.m.Update(k)
	return cmd
}

func (x *tm) typeStr(s string) {
	for _, r := range s {
		x.key(string(r))
	}
}

func (x *tm) screen() string { return x.m.View() }

func isQuit(c tea.Cmd) bool {
	if c == nil {
		return false
	}
	_, ok := c().(tea.QuitMsg)
	return ok
}

func TestQuitClosesListeners(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		x := newTM(t)
		x.seed()
		if cmd := x.key(k); !isQuit(cmd) || x.stops != 1 {
			t.Fatalf("%s: quit %v stops %d", k, isQuit(cmd), x.stops)
		}
	}
	// Esc with a prompt open cancels the prompt only; Ctrl-C always leaves.
	x := newTM(t)
	x.seed()
	x.key("K")
	if cmd := x.key("esc"); isQuit(cmd) || x.stops != 0 || x.m.flow.Active() {
		t.Fatal("Esc in a prompt must only cancel it")
	}
	x.key("K")
	if cmd := x.key("ctrl+c"); !isQuit(cmd) || x.stops != 1 {
		t.Fatal("Ctrl-C leaves from a prompt")
	}
	if len(x.execs) != 0 {
		t.Fatal("leaving wrote something")
	}
}

func TestQuitStopsTheSupervisor(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1")
	r := start(t, f, newClk(t0), Options{}, false)
	// the rig reads Updates itself; a second consumer here would race, so
	// check the other half: Stop cancels and the channel closes.
	x := newTM(t)
	x.m.o.Stop = r.cancel
	x.seed()
	if !isQuit(x.key("q")) {
		t.Fatal("no quit")
	}
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor did not stop after Stop")
	}
}

func TestSelectionAndFold(t *testing.T) {
	x := newTM(t)
	x.seed()
	s := x.screen()
	if !strings.Contains(s, "▸ acme/app") || !strings.Contains(s, "build the") {
		t.Fatalf("first repository selected:\n%s", s)
	}
	x.key("down") // onto acme/app's one run
	if s = x.screen(); !strings.Contains(s, "▸ r1") || strings.Contains(s, "▸ acme/app") {
		t.Fatalf("down onto the run:\n%s", s)
	}
	x.key("down") // onto the acme/lib header
	if s = x.screen(); !strings.Contains(s, "▸ acme/lib") {
		t.Fatalf("down onto the next header:\n%s", s)
	}
	x.key("down") // acme/lib's one run
	x.key("down") // at the end: stays
	x.key("up")   // back onto the acme/lib header
	x.key("space")
	if s = x.screen(); strings.Contains(s, "fix the o") || !strings.Contains(s, "[1 runs hidden]") {
		t.Fatalf("fold:\n%s", s)
	}
	x.key("up")
	x.key("?")
	if s = x.screen(); !strings.Contains(s, "kill the WHOLE PROJECT") || strings.Contains(s, "build the") {
		t.Fatalf("help:\n%s", s)
	}
	x.key("?")
	if !strings.Contains(x.screen(), "build the") {
		t.Fatal("help closes")
	}
}

// Space on a run toggles only that run's detail, not the block's fold, and
// does nothing while help covers the screen (design generic-tool §10.2).
func TestSpaceTogglesSelectedRunOnly(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.key("down") // onto acme/app's one run, r1
	r1 := Cursor{Slug: "acme%2Fapp", Run: "r1"}
	if x.m.cur != r1 {
		t.Fatalf("cursor = %+v, want %+v", x.m.cur, r1)
	}
	x.key("space")
	if !x.m.expanded[r1] || x.m.collapsed["acme%2Fapp"] || len(x.m.expanded) != 1 {
		t.Fatalf("expanded %v collapsed %v", x.m.expanded, x.m.collapsed)
	}
	x.m.help = true
	x.key("space")
	if !x.m.expanded[r1] {
		t.Fatal("space with help open changed the view")
	}
	x.m.help = false
	x.key("up") // back onto the acme/app header
	x.key("space")
	if !x.m.collapsed["acme%2Fapp"] {
		t.Fatal("space on a header did not fold")
	}
}

// Space is a toggle: pressing it twice on the same row, run or header,
// returns to where it started.
func TestSpaceTwicePerRowTogglesBack(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.key("down") // onto acme/app's one run, r1
	r1 := Cursor{Slug: "acme%2Fapp", Run: "r1"}
	x.key("space")
	if !x.m.expanded[r1] {
		t.Fatal("first space did not expand the run")
	}
	x.key("space")
	if x.m.expanded[r1] {
		t.Fatal("second space on the same run must collapse its detail again")
	}
	x.key("up") // the acme/app header
	x.key("space")
	if !x.m.collapsed["acme%2Fapp"] {
		t.Fatal("first space did not fold the header")
	}
	x.key("space")
	if x.m.collapsed["acme%2Fapp"] {
		t.Fatal("second space on the same header must unfold it again")
	}
}

// move and space both set follow, so the frame scrolls to keep the cursor
// (and an expanded run's detail) in view even over a small viewport
// (render.go's viewport "keep the selection visible" logic, exercised here
// end to end through the model).
func TestMoveAndSpaceFollowScroll(t *testing.T) {
	x := newTM(t)
	x.m.h, x.m.o.Height = 16, 16
	var runs []string
	for i := 0; i < 20; i++ {
		runs = append(runs, fmt.Sprintf("%q:%s", fmt.Sprintf("r%02d", i), agent("acme/app", fmt.Sprintf("job %d", i), "")))
	}
	at := x.now
	x.upd(
		Update{Kind: UpdDay, Day: budget.Day(at), Now: at},
		ev(SrcConfig, "/", `{}`, at), ev(SrcGlobal, "/", `{}`, at), ev(SrcRepos, "/", `{}`, at),
		ev(SrcAgents, "/", `{"acme%2Fapp":{`+strings.Join(runs, ",")+`}}`, at),
	)
	for i := 0; i < 25; i++ { // past the end: move clamps, follow keeps scrolling
		x.key("down")
	}
	last := Cursor{Slug: "acme%2Fapp", Run: "r19"}
	if x.m.cur != last {
		t.Fatalf("cur = %+v, want the last run", x.m.cur)
	}
	if !strings.Contains(x.screen(), "▸ r19") {
		t.Fatalf("move did not scroll to follow the cursor:\n%s", x.screen())
	}
	x.key("space") // expand the last run, near the bottom of a tall list
	if !strings.Contains(x.screen(), "deadline") {
		t.Fatalf("space did not scroll to keep the expanded detail visible:\n%s", x.screen())
	}
}

// expanded is keyed by Cursor, independent of the cursor's own position
// (design: "expanded is the truth"); when a run disappears, its entry must
// be pruned, not kept forever or resurrected if a later run reuses the id.
// The cursor itself must fall back cleanly to the header that remains (one
// row, in this repository, after the run goes) rather than get stuck on a
// stale Cursor.
func TestExpandedPrunedWhenRunVanishes(t *testing.T) {
	x := newTM(t)
	x.seed()      // acme/app has run r1; acme/lib has run r2
	x.key("down") // acme/app's run, r1
	r1 := Cursor{Slug: "acme%2Fapp", Run: "r1"}
	if x.m.cur != r1 {
		t.Fatalf("cur = %+v, want %+v", x.m.cur, r1)
	}
	x.key("space")
	if !x.m.expanded[r1] {
		t.Fatal("not expanded")
	}
	// r1 finishes: the agents tree no longer lists it, but acme/app still
	// has spend of its own, so its header stays (one row: the header only).
	x.upd(ev(SrcAgents, "/", `{"acme%2Flib":{"r2":`+agent("acme/lib", "fix the o", "")+`}}`, x.now))
	if x.m.expanded[r1] {
		t.Fatal("expanded entry for a vanished run must be pruned")
	}
	if x.m.cur.Slug != "acme%2Fapp" || x.m.cur.Run != "" {
		t.Fatalf("cursor should fall back to the app header, got %+v", x.m.cur)
	}
}

// A project with no repositories, and one with a single repository that has
// no runs (the header is the only row), must not panic and must leave the
// cursor sane: regression coverage for a stale or zero/one-row cursor.
func TestModelHandlesZeroThenOneRow(t *testing.T) {
	x := newTM(t)
	x.upd(Update{Kind: UpdDay, Day: budget.Day(x.now), Now: x.now}) // no agents, no repos, no kill: zero rows
	x.key("down")
	x.key("up")
	x.key("space")
	if x.m.cur != (Cursor{}) {
		t.Fatalf("cur = %+v, want the zero Cursor with no rows", x.m.cur)
	}
	x.upd(ev(SrcRepos, "/", `{"acme%2Fapp":{"spent":1000000}}`, x.now)) // one row: the header, no runs yet
	x.key("down")                                                       // only row: stays
	if x.m.cur.Slug != "acme%2Fapp" || x.m.cur.Run != "" {
		t.Fatalf("cur = %+v, want the only header", x.m.cur)
	}
	x.key("space")
	if !x.m.collapsed["acme%2Fapp"] {
		t.Fatal("space on the only row (a header) must still fold it")
	}
}

func TestKillRepoThroughTheModel(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.key("down") // acme/app's run
	x.key("down") // acme/lib
	x.key("k")
	if s := x.screen(); !strings.Contains(s, "kill repository acme/lib? y/Enter = yes") {
		t.Fatalf("prompt:\n%s", s)
	}
	cmd := x.key("y")
	if cmd == nil || !x.m.flow.InFlight() {
		t.Fatal("y submits")
	}
	if !strings.Contains(x.screen(), "killing…") {
		t.Fatalf("no killing line:\n%s", x.screen())
	}
	msg := cmd()
	if len(x.execs) != 1 || x.execs[0].Kind != KillRepo || x.execs[0].Target.Slug != "acme%2Flib" || x.execs[0].Project != "aurora" {
		t.Fatalf("exec %+v", x.execs)
	}
	x.m.Update(msg)
	if x.m.flow.InFlight() {
		t.Fatal("Done not called")
	}
	if s := x.screen(); !strings.Contains(s, "killing…") || !strings.Contains(s, "wrote") {
		t.Fatalf("killing line stays until the stream shows it:\n%s", s)
	}
	// The stream delivers the switch: the line goes, the banner appears.
	x.upd(ev(SrcConfig, "/kill", `{"repos":{"acme%2Flib":{"on":true,"by":"me","at":1,"reason":"from fugaro watch"}}}`, x.now))
	s := x.screen()
	if strings.Contains(s, "killing…") || !strings.Contains(s, "KILLED by me") {
		t.Fatalf("after the stream:\n%s", s)
	}
}

func TestKillingLineTimesOut(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.key("k")
	_, _ = x.m.Update(x.key("y")())
	_ = x
	x.now = x.now.Add(6 * time.Second)
	x.upd(Update{Kind: UpdTick, Now: x.now})
	if strings.Contains(x.screen(), "killing…") {
		t.Fatal("killing… must clear after 5 s")
	}
}

// The a key toggles the finished-run filter between its default ("active +
// recent") and --all, and the footer reflects it at once (design
// generic-tool §10.3).
func TestKeyAToggleFilterAll(t *testing.T) {
	x := newTM(t)
	x.seed()
	if x.m.filter.All {
		t.Fatal("the filter starts off (not --all)")
	}
	if !strings.Contains(x.screen(), "showing active + recent") {
		t.Fatalf("screen:\n%s", x.screen())
	}
	x.key("a")
	if !x.m.filter.All || !strings.Contains(x.screen(), "showing all") {
		t.Fatalf("a did not switch to --all:\n%s", x.screen())
	}
	x.key("a")
	if x.m.filter.All || !strings.Contains(x.screen(), "showing active + recent") {
		t.Fatalf("a did not switch back:\n%s", x.screen())
	}
}

// The x key acknowledges the selected row's failure when it names a failed
// finished run, and the next rebuild hides it (design generic-tool §10.3).
// On anything else (a live run, a header, a successful finished run) it does
// nothing.
func TestKeyXAcksSelectedFailedFinishedRun(t *testing.T) {
	x := newTM(t)
	x.m.o.Queued = func() ([]QueuedRun, []FinishedRun, string) {
		return nil, []FinishedRun{{Run: "f1", Slug: "acme/app", Status: "failed", FinishedAt: x.now.Add(-time.Hour)}}, ""
	}
	x.seed()
	if !strings.Contains(x.screen(), "f1") {
		t.Fatalf("the failure is not shown before x:\n%s", x.screen())
	}
	x.key("down") // onto acme/app's live run, r1
	x.key("x")    // x on a live run: no-op
	if !strings.Contains(x.screen(), "f1") {
		t.Fatal("x on a live run hid the failure")
	}
	x.key("down") // onto acme/app's finished run, f1
	if x.m.cur.Run != "f1" {
		t.Fatalf("cur = %+v, want the finished row", x.m.cur)
	}
	x.key("x")
	if !x.m.acks.Has(budget.Key("acme/app"), "f1") {
		t.Fatal("x did not record the acknowledgement")
	}
	if strings.Contains(x.screen(), "f1") {
		t.Fatalf("acknowledged failure still shown:\n%s", x.screen())
	}
}

// A ready PR stays in "Ready for your review" past --keep (6h by default):
// the filter bounds how much history the dashboard shows, not what still
// needs a look (design generic-tool §10.1).
func TestReadyForReviewSurvivesTheFilter(t *testing.T) {
	x := newTM(t)
	x.m.o.Queued = func() ([]QueuedRun, []FinishedRun, string) {
		return nil, []FinishedRun{{Run: "r-old", Slug: "acme/app", Title: "Old but ready", Status: "succeeded",
			Outcome: "ready", PRURL: "https://github.com/acme/app/pull/9", PRNumber: 9, FinishedAt: x.now.Add(-10 * time.Hour)}}, ""
	}
	x.seed()
	s := x.screen()
	if strings.Contains(s, "r-old") {
		t.Fatalf("the 10h-old row should not be in the regular finished list:\n%s", s)
	}
	if !strings.Contains(s, "Ready for your review (1)") || !strings.Contains(s, "https://github.com/acme/app/pull/9") {
		t.Fatalf("the ready PR must still be listed:\n%s", s)
	}
}

func TestKeyMappingLowercaseRepoCapitalProject(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"k", "kill repository acme/app?"}, {"K", "kill the WHOLE PROJECT"},
		{"r", "resume repository acme/app. Type acme/app"}, {"R", "resume the WHOLE PROJECT aurora. Type aurora"},
	}
	for _, c := range cases {
		x := newTM(t)
		x.seed()
		x.key(c.key)
		if s := x.screen(); !strings.Contains(s, c.want) {
			t.Errorf("%s: want %q in\n%s", c.key, c.want, s)
		}
	}
}

func TestKillProjectNeedsNameThenReason(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.key("K")
	x.typeStr("aurora")
	if cmd := x.key("enter"); cmd != nil {
		t.Fatal("the reason prompt comes first")
	}
	if !strings.Contains(x.screen(), "reason (Enter") {
		t.Fatal(x.screen())
	}
	x.typeStr("runaway")
	cmd := x.key("enter")
	if cmd == nil {
		t.Fatal("no write")
	}
	cmd()
	if len(x.execs) != 1 || x.execs[0].Kind != KillAll || x.execs[0].Reason != "runaway" {
		t.Fatalf("%+v", x.execs)
	}
}

// Blocks keep a stable order by name (design generic-tool G23): a spend
// change while a prompt is open must not move acme/lib out from under the
// cursor, the way a spend-ordered dashboard once would have.
func TestSelectionSurvivesResortTUI(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.key("down") // acme/app's run
	x.key("down") // acme/lib
	x.key("k")
	x.upd(ev(SrcRepos, "/", `{"acme%2Fapp":{"spent":6000000},"acme%2Flib":{"spent":90000000}}`, x.now))
	if x.m.view.Repos[0].Slug != "acme%2Fapp" || x.m.view.Repos[1].Slug != "acme%2Flib" {
		t.Fatalf("blocks must keep their order by name, not re-sort by spend: %+v", x.m.view.Repos)
	}
	cmd := x.key("y")
	cmd()
	if len(x.execs) != 1 || x.execs[0].Target.Slug != "acme%2Flib" {
		t.Fatalf("the key acted on %+v, not the repository selected when it was pressed", x.execs)
	}
	// And the cursor itself followed the repository, not the row.
	if !strings.Contains(x.screen(), "▸ acme/lib") {
		t.Fatal(x.screen())
	}
}

func TestKeysDisabledWhenStaleOrOffline(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.now = x.now.Add(60 * time.Second)
	x.upd(Update{Kind: UpdTick, Now: x.now})
	x.key("K")
	s := x.screen()
	if x.m.flow.Active() || !strings.Contains(s, "stale") || !strings.Contains(s, "off while the data is offline or stale") {
		t.Fatalf("stale:\n%s", s)
	}
	// A prompt left open while the data goes stale does not submit.
	y := newTM(t)
	y.seed()
	y.key("k")
	y.now = y.now.Add(60 * time.Second)
	y.upd(Update{Kind: UpdTick, Now: y.now})
	if cmd := y.key("y"); cmd != nil || len(y.execs) != 0 {
		t.Fatal("a y on stale data wrote")
	}
}

func TestTUIViewerRefusalShown(t *testing.T) {
	x := newTM(t)
	x.out = func(Request) Outcome { return Outcome{Denied: true, Notice: budget.AdminDeniedText("aurora")} }
	x.seed()
	x.key("k")
	x.m.Update(x.key("y")())
	s := x.screen()
	if !strings.Contains(s, "you are not a budget admin") || strings.Contains(s, "killing…") {
		t.Fatalf("%s", s)
	}
}

func TestHungOrPanickingExecFreesTheFlow(t *testing.T) {
	oldT, oldG := ExecuteTimeout, execGrace
	ExecuteTimeout, execGrace = 10*time.Millisecond, 10*time.Millisecond
	defer func() { ExecuteTimeout, execGrace = oldT, oldG }()

	x := newTM(t)
	release := make(chan struct{})
	defer close(release)
	x.m.o.Exec = func(ctx context.Context, r Request) Outcome { <-release; return Outcome{} }
	x.seed()
	x.key("k")
	msg := x.key("y")()
	x.m.Update(msg)
	if x.m.flow.InFlight() || !strings.Contains(x.screen(), "did not answer in time") {
		t.Fatalf("a hung write wedged the flow:\n%s", x.screen())
	}
	x.key("k") // keys work again
	if !x.m.flow.Active() {
		t.Fatal("keys still refused after a hung write")
	}

	z := newTM(t)
	z.m.o.Exec = func(context.Context, Request) Outcome { panic("boom") }
	z.seed()
	z.key("k")
	z.m.Update(z.key("y")())
	if z.m.flow.InFlight() || !strings.Contains(z.screen(), "failed unexpectedly") {
		t.Fatalf("a panic wedged the flow:\n%s", z.screen())
	}
}

func TestRedrawsAreCoalesced(t *testing.T) {
	x := newTM(t)
	x.seed()
	before := x.screen()
	var us []Update
	for i := 0; i < 500; i++ {
		us = append(us, ev(SrcGlobal, "/spent", `12000000`, x.now))
	}
	x.m.Update(supMsg{us: us})
	if x.screen() != before {
		t.Fatal("events must not redraw by themselves")
	}
	if !x.m.dirty {
		t.Fatal("events mark the model dirty")
	}
	x.m.Update(renderMsg{})
	if x.screen() == before || x.m.dirty {
		t.Fatal("the render tick draws once")
	}
}

func TestWindowResizeAndTooNarrow(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.m.Update(tea.WindowSizeMsg{Width: 38, Height: 20})
	if x.screen() != tooNarrow {
		t.Fatalf("%q", x.screen())
	}
	if !isQuit(x.key("q")) {
		t.Fatal("q must work on a narrow screen")
	}
}

// A resize clears the screen before the redraw: a terminal that reflowed the
// old frame would otherwise leave a stale copy of its last lines behind.
func TestWindowResizeClearsScreen(t *testing.T) {
	x := newTM(t)
	x.seed()
	_, cmd := x.m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd == nil {
		t.Fatal("a resize must clear the screen")
	}
	if got, want := cmd(), tea.ClearScreen(); got != want {
		t.Fatalf("resize command returned %#v, want a clear-screen", got)
	}
}

func TestRepoNarrowing(t *testing.T) {
	x := newTM(t)
	x.m.o.RepoKey, x.m.o.Repo = "acme%2Flib", "acme/lib"
	x.seed()
	s := x.screen()
	if strings.Contains(s, "acme/app") || !strings.Contains(s, "narrowed to repository acme/lib") {
		t.Fatalf("%s", s)
	}
}

func TestHostileTextNeverReachesScreen(t *testing.T) {
	x := newTM(t)
	at := x.now
	hostile, _ := json.Marshal(map[string]any{"repo": "evil\x1b[2Jrepo", "title": "t\x1b]0;pwn\x07itle\u202e\u200b\nnext",
		"stage": "\x1b[31mred", "requestedBy": "a", "startedAt": ms(at)})
	x.upd(
		Update{Kind: UpdDay, Day: budget.Day(at), Now: at},
		ev(SrcConfig, "/", `{}`, at), ev(SrcGlobal, "/", `{}`, at), ev(SrcRepos, "/", `{}`, at),
		ev(SrcAgents, "/", `{`+strconv.Quote(budget.Key("evil\x1b[2Jrepo"))+`:{"r1":`+string(hostile)+`}}`, at))
	s := x.screen()
	if strings.ContainsAny(s, "\x1b\x07\u202e\u200b") || !strings.Contains(s, "evilrepo") {
		t.Fatalf("%q", s)
	}
}

func TestUseASCIIAndNoColor(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		env   map[string]string
		flag  bool
		ascii bool
	}{
		{nil, false, false}, {nil, true, true},
		{map[string]string{"LANG": "en_US.UTF-8"}, false, false},
		{map[string]string{"LANG": "C"}, false, true},
		{map[string]string{"LC_ALL": "en_US.utf8", "LANG": "C"}, false, false},
		{map[string]string{"LC_ALL": "POSIX", "LANG": "en_US.UTF-8"}, false, true},
	} {
		if got := UseASCII(c.flag, env(c.env)); got != c.ascii {
			t.Errorf("%v flag %v: %v", c.env, c.flag, got)
		}
	}
	for _, c := range []struct {
		env  map[string]string
		flag bool
		want bool
	}{{nil, false, false}, {nil, true, true}, {map[string]string{"NO_COLOR": "1"}, false, true}, {map[string]string{"TERM": "dumb"}, false, true}, {map[string]string{"TERM": "xterm"}, false, false}} {
		if got := UseNoColor(c.flag, env(c.env)); got != c.want {
			t.Errorf("%v: %v", c.env, got)
		}
	}
}

// ------------------------------------------------------------ the program

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// TestProgramSmoke runs the real tea.Program on a fake terminal against a
// fake RTDB, a real Supervisor and the real Execute: the scripted keys kill a
// repository, and q leaves with everything stopped.
func TestProgramSmoke(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	seed(f, budget.Day(t0), "r1")
	tr := &http.Transport{DisableKeepAlives: true}
	db, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "tok" }},
		rtdb.WithStreamBackoff(5*time.Millisecond, 20*time.Millisecond), rtdb.WithHTTPClient(&http.Client{Transport: tr}))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	supCtx, supCancel := context.WithCancel(ctx)
	defer supCancel()
	c := newClk(t0)
	sup := Start(supCtx, db, Options{Now: c.Now, Tick: 20 * time.Millisecond})

	pr, pw := io.Pipe()
	defer pw.Close()
	out := &syncBuf{}
	var stopped atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- RunTUI(ctx, TUIOptions{
			In: pr, Out: out, Project: "aurora", Updates: sup.Updates(), Width: 100, Height: 30, NoColor: true,
			Exec:    func(ctx context.Context, req Request) Outcome { return Execute(ctx, db, req, "tester@x.io") },
			Stop:    func() { stopped.Store(true); supCancel() },
			AckPath: filepath.Join(t.TempDir(), "watch-acks.json"),
		})
	}()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; screen so far:\n%s", what, out.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor("the first data", func() bool { return strings.Contains(out.String(), "t-r1") })
	pw.Write([]byte("k"))
	waitFor("the kill prompt", func() bool { return strings.Contains(out.String(), "kill repository lib? y/Enter = yes") })
	pw.Write([]byte("y"))
	waitFor("the kill switch in the database", func() bool {
		n, _ := f.Value("config/kill/repos/lib").(map[string]any)
		return n != nil && n["on"] == true && n["by"] == "tester@x.io"
	})
	waitFor("the banner from the stream", func() bool { return strings.Contains(out.String(), "KILLED by tester@x.io") })
	pw.Write([]byte("q"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTUI: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the program did not exit on q")
	}
	if !stopped.Load() {
		t.Fatal("Stop was not called")
	}
	select { // the supervisor stopped: its channel closes
	case _, ok := <-drain(sup.Updates()):
		_ = ok
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor is still running")
	}
	if f.Value("agents/lib/r1") == nil {
		t.Fatal("leaving must not touch the runs")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("timed out")
	}
}

// drain reads ch until it closes, then reports once.
func drain(ch <-chan Update) <-chan struct{} {
	d := make(chan struct{})
	go func() {
		for range ch {
		}
		close(d)
	}()
	return d
}
