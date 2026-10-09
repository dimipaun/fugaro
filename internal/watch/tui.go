package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The Bubble Tea model of `fugaro watch` (plan W14, D13). It is a thin shell:
// the supervisor's Updates are folded into a State (State.Handle), Build makes
// the View, Render draws it, and the kill and resume keys go to the pure Flow
// of keys.go. Redraws are coalesced: events only mark the model dirty and a
// 250 ms tick rebuilds the frame, so a storm of events costs one Build per
// quarter second (key presses rebuild at once).

const (
	// RenderEvery is the redraw period (~4 frames per second).
	RenderEvery = 250 * time.Millisecond
	// killingFor is how long "killing…" shows after a write, unless the
	// stream delivers the change first.
	killingFor = 5 * time.Second
	// drainMax bounds how many queued updates one message carries.
	drainMax = 256
)

// execGrace is how long past ExecuteTimeout the model waits for an Exec that
// ignores its context, before it gives up and frees the flow.
var execGrace = 2 * time.Second

// TUIOptions is everything the screen needs.
type TUIOptions struct {
	In  io.Reader
	Out io.Writer

	Project string
	Updates <-chan Update // the supervisor's stream; closing it ends the screen
	Config  Config
	RepoKey string // --repo's wire key, "" for all
	Repo    string // --repo as given
	// Queued is the latest queued-run and finished-run rows and degrade
	// note, read fresh on every rebuild; nil when the project has no
	// queued-run source. The screen does not yet show the finished rows
	// (plan generic-tool Task 5/6 wire them in).
	Queued func() ([]QueuedRun, []FinishedRun, string)

	ASCII, NoColor bool
	// Exec runs a confirmed kill or resume (Execute against the database).
	// Nil: the screen has no kill keys.
	Exec func(ctx context.Context, req Request) Outcome
	// Stop is called when the user leaves, to cancel the supervisor; the
	// cloud jobs are not touched.
	Stop func()

	// Width and Height are the size until the terminal reports one.
	Width, Height int
	// AltScreen uses the terminal's alternate screen.
	AltScreen bool
	// Now is the clock of the "killing…" timeout and of the first frame
	// (default: the local one).
	Now func() time.Time
}

// ---------------------------------------------------------------- messages

type supMsg struct {
	us     []Update
	closed bool
}

type renderMsg struct{}

type actionDoneMsg struct {
	req Request
	out Outcome
}

// pending is a write the stream has not shown yet.
type pending struct {
	req   Request
	until time.Time
}

type model struct {
	o   TUIOptions
	ctx context.Context

	st   *State
	flow *Flow
	now  time.Time // the supervisor's (server-adjusted) time, from the last update

	w, h      int
	sel       string
	collapsed map[string]bool
	scroll    int
	follow    bool
	help      bool

	view   View
	dirty  bool
	frame  string
	notice string
	pend   *pending
	closed bool
	act    Kind // the kind of the write in flight or pending
}

func newModel(ctx context.Context, o TUIOptions) *model {
	m := &model{o: o, ctx: ctx, st: NewState(), flow: NewFlow(o.Project), collapsed: map[string]bool{}, w: o.Width, h: o.Height, dirty: true}
	if m.w <= 0 {
		m.w = 80
	}
	if m.h <= 0 {
		m.h = 24
	}
	m.now = m.clock()
	m.rebuild()
	return m
}

func (m *model) clock() time.Time {
	if m.o.Now != nil {
		return m.o.Now().UTC()
	}
	return time.Now().UTC()
}

// ------------------------------------------------------------------ tea

func (m *model) Init() tea.Cmd { return tea.Batch(m.wait(), m.tick()) }

func (m *model) tick() tea.Cmd {
	return tea.Tick(RenderEvery, func(time.Time) tea.Msg { return renderMsg{} })
}

// wait blocks for the supervisor's next update, then takes whatever else is
// already queued so one message carries a burst.
func (m *model) wait() tea.Cmd {
	ch := m.o.Updates
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return supMsg{closed: true}
		}
		us := []Update{u}
		for len(us) < drainMax {
			select {
			case u, ok := <-ch:
				if !ok {
					return supMsg{us: us, closed: true}
				}
				us = append(us, u)
			default:
				return supMsg{us: us}
			}
		}
		return supMsg{us: us}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case supMsg:
		for _, u := range msg.us {
			_ = m.st.Handle(u) // a bad event leaves the tree as it was
			if !u.Now.IsZero() {
				m.now = u.Now
			}
			m.dirty = true
		}
		if msg.closed {
			m.closed = true
			return m, m.leave() // the supervisor stopped: nothing more will come
		}
		return m, m.wait()
	case renderMsg:
		if m.dirty {
			m.rebuild()
		}
		return m, m.tick()
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.rebuild()
		// A terminal reflows the old frame on a resize and the renderer's
		// line count no longer matches it: clear, then redraw the whole frame.
		return m, tea.ClearScreen
	case actionDoneMsg:
		m.flow.Done()
		m.notice = msg.out.Notice
		if msg.out.Written {
			m.pend = &pending{req: msg.req, until: m.clock().Add(killingFor)}
		}
		m.rebuild()
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) View() string { return m.frame }

// leave stops the supervisor and ends the program; cloud jobs keep running.
func (m *model) leave() tea.Cmd {
	if m.o.Stop != nil {
		m.o.Stop()
	}
	return tea.Quit
}

// live is true when the data may be acted on: fresh, not stale or offline.
func (m *model) live() bool {
	if m.dirty {
		m.rebuild()
	}
	k := m.view.Conn.Kind
	return k == ConnLive || k == ConnPolling
}

// rebuild recomputes the view and the frame.
func (m *model) rebuild() {
	v := Build(m.st, m.now, m.o.Config)
	if m.o.Queued != nil {
		rows, _, note := m.o.Queued() // finished rows: not shown yet (plan generic-tool Task 5/6)
		v = MergeQueued(v, m.o.Config, rows, note, m.now)
	}
	if m.o.RepoKey != "" {
		v = FilterRepo(v, m.o.RepoKey)
	}
	m.view = v
	if i := SelectedIndex(v, m.sel); i >= 0 {
		m.sel = v.Repos[i].Slug // the selection is by slug, so it survives a re-sort
	}
	m.settle()

	foot := m.footer()
	fr := Render(v, RenderOptions{
		Width: m.w, Height: m.h, Project: m.o.Project, ASCII: m.o.ASCII, Color: !m.o.NoColor,
		Selected: m.sel, Collapsed: m.collapsed, Scroll: m.scroll, Follow: m.follow, Help: m.help,
		Footer: foot, Keys: m.o.Exec != nil, Repo: m.o.Repo,
	})
	m.scroll, m.follow = fr.Scroll, false
	m.frame = fr.String()
	m.dirty = false
}

// settle drops a pending write once the stream shows it or time is up.
func (m *model) settle() {
	if m.pend == nil {
		return
	}
	p := m.pend
	shown := false
	if p.req.Kind.all() {
		shown = m.view.Project.Kill.On == p.req.Kind.kill()
	} else {
		shown = !p.req.Kind.kill() // a resumed repository may vanish from the view
		for _, r := range m.view.Repos {
			if r.Slug == p.req.Target.Slug {
				shown = r.Kill.On == p.req.Kind.kill()
			}
		}
	}
	if shown || m.clock().After(p.until) {
		m.pend = nil
	}
}

// footer is the prompt, else the progress or last notice.
func (m *model) footer() []string {
	if m.flow.Active() {
		return []string{m.flow.Prompt() + "_"}
	}
	var out []string
	if m.flow.InFlight() || m.pend != nil {
		word := "killing"
		if !m.act.kill() {
			word = "resuming"
		}
		ell := "…"
		if m.o.ASCII {
			ell = "..."
		}
		out = append(out, word+ell)
	}
	switch {
	case m.flow.Notice() != "":
		out = append(out, m.flow.Notice())
	case m.notice != "":
		out = append(out, m.notice)
	}
	return out
}

// ------------------------------------------------------------------ keys

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Type == tea.KeyCtrlC {
		return m, m.leave()
	}
	defer m.rebuild() // a key shows at once, not at the next tick
	if m.flow.Active() {
		return m.promptKey(k)
	}
	if k.Alt {
		return m, nil
	}
	switch k.Type {
	case tea.KeyEsc:
		if m.help {
			m.help = false
			return m, nil
		}
		return m, m.leave()
	case tea.KeyUp:
		m.move(-1)
	case tea.KeyDown:
		m.move(1)
	case tea.KeyPgUp:
		m.scroll = max(m.scroll-max(m.h/2, 1), 0)
	case tea.KeyPgDown:
		m.scroll += max(m.h/2, 1)
	case tea.KeySpace:
		m.fold()
	case tea.KeyRunes:
		if k.Paste {
			return m, nil
		}
		switch string(k.Runes) {
		case "q":
			return m, m.leave()
		case "?":
			m.help = !m.help
		case " ":
			m.fold()
		case "k":
			return m, m.start(KillRepo)
		case "K":
			return m, m.start(KillAll)
		case "r":
			return m, m.start(ResumeRepo)
		case "R":
			return m, m.start(ResumeAll)
		}
	}
	return m, nil
}

func (m *model) move(d int) {
	i := SelectedIndex(m.view, m.sel)
	if i < 0 {
		return
	}
	i = min(max(i+d, 0), len(m.view.Repos)-1)
	m.sel, m.follow = m.view.Repos[i].Slug, true
}

func (m *model) fold() {
	if i := SelectedIndex(m.view, m.sel); i >= 0 {
		s := m.view.Repos[i].Slug
		m.collapsed[s] = !m.collapsed[s]
		m.follow = true
	}
}

// start opens a prompt on the repository selected NOW (the Flow captures the
// target; a later re-sort cannot retarget it).
func (m *model) start(kind Kind) tea.Cmd {
	m.notice = ""
	if m.o.Exec == nil {
		return nil
	}
	var t Target
	if i := SelectedIndex(m.view, m.sel); i >= 0 {
		t = Target{Slug: m.view.Repos[i].Slug, Name: m.view.Repos[i].Name}
	}
	m.flow.Start(kind, t, m.live())
	return nil
}

func (m *model) promptKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	live := m.live()
	var req *Request
	switch k.Type {
	case tea.KeyEsc:
		m.flow.Esc()
	case tea.KeyEnter:
		req = m.flow.Enter(live)
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.flow.Backspace()
	case tea.KeySpace:
		req = m.flow.Type(" ", live)
	case tea.KeyRunes:
		s := string(k.Runes)
		// A pasted lone y is not a keystroke answering the y/n prompt.
		if k.Paste && (s == "y" || s == "Y") {
			break
		}
		req = m.flow.Type(s, live)
	}
	if req == nil {
		return m, nil
	}
	m.act = req.Kind
	return m, m.exec(*req)
}

// exec runs a confirmed request in a command. It always answers: a panic or a
// call that ignores its context cannot keep the flow in flight forever.
func (m *model) exec(req Request) tea.Cmd {
	run, ctx := m.o.Exec, m.ctx
	return func() tea.Msg {
		ch := make(chan Outcome, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- Outcome{Err: fmt.Errorf("panic: %v", r), Notice: "the write failed unexpectedly; check with fugaro budget show"}
				}
			}()
			ch <- run(ctx, req)
		}()
		select {
		case o := <-ch:
			return actionDoneMsg{req, o}
		case <-time.After(ExecuteTimeout + execGrace):
			return actionDoneMsg{req, Outcome{Err: context.DeadlineExceeded, Notice: "the write did not answer in time; check with fugaro budget show before retrying"}}
		}
	}
}

// ------------------------------------------------------------------- run

// UseASCII is true when the output should avoid box-drawing, block and
// symbol characters: --ascii, or a locale that is not UTF-8.
func UseASCII(flag bool, getenv func(string) string) bool {
	if flag {
		return true
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := getenv(k); v != "" {
			v = strings.ToLower(v)
			return !(strings.Contains(v, "utf-8") || strings.Contains(v, "utf8"))
		}
	}
	return false // no locale set: assume a modern terminal
}

// UseNoColor is true for --no-color, NO_COLOR (any value) and TERM=dumb.
func UseNoColor(flag bool, getenv func(string) string) bool {
	if flag || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return true
	}
	return false
}

// RunTUI runs the interactive screen until the user leaves, ctx ends or the
// supervisor's stream closes. Leaving calls o.Stop; the cloud jobs keep going.
func RunTUI(ctx context.Context, o TUIOptions) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newModel(ctx, o)
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	if o.In != nil {
		opts = append(opts, tea.WithInput(o.In))
	}
	if o.Out != nil {
		opts = append(opts, tea.WithOutput(o.Out))
	} else {
		opts = append(opts, tea.WithOutput(os.Stdout))
	}
	if o.AltScreen {
		opts = append(opts, tea.WithAltScreen())
	}
	_, err := tea.NewProgram(m, opts...).Run()
	if err != nil && ctx.Err() != nil {
		return nil // the context ended: a clean exit
	}
	return err
}
