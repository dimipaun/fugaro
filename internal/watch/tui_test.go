package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
		t.Fatalf("first repository (most spend) selected:\n%s", s)
	}
	x.key("down")
	if s = x.screen(); !strings.Contains(s, "▸ acme/lib") || strings.Contains(s, "▸ acme/app") {
		t.Fatalf("down:\n%s", s)
	}
	x.key("down") // at the end: stays
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

func TestKillRepoThroughTheModel(t *testing.T) {
	x := newTM(t)
	x.seed()
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

func TestSelectionSurvivesResortTUI(t *testing.T) {
	x := newTM(t)
	x.seed()
	x.key("down") // acme/lib
	x.key("k")
	// While the prompt is open lib overtakes app in spend: it moves to the top.
	x.upd(ev(SrcRepos, "/", `{"acme%2Fapp":{"spent":6000000},"acme%2Flib":{"spent":90000000}}`, x.now))
	if x.m.view.Repos[0].Slug != "acme%2Flib" {
		t.Fatal("expected a re-sort")
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
			Exec: func(ctx context.Context, req Request) Outcome { return Execute(ctx, db, req, "tester@x.io") },
			Stop: func() { stopped.Store(true); supCancel() },
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
