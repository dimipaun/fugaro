package watch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

var repoT = Target{Slug: "acme__app", Name: "acme/app"}

// step is one input to the flow; the sequence's last Request is checked.
type step struct{ typ, key string } // key: "enter", "esc", "bs", "" (typing typ)

func run(f *Flow, steps []step) (reqs []*Request) {
	for _, s := range steps {
		var r *Request
		switch s.key {
		case "enter":
			r = f.Enter(true)
		case "esc":
			f.Esc()
		case "bs":
			f.Backspace()
		default:
			r = f.Type(s.typ, true)
		}
		if r != nil {
			reqs = append(reqs, r)
		}
	}
	return
}

func TestKillAllNeedsTypedWord(t *testing.T) {
	cases := []struct {
		name  string
		steps []step
		want  bool // a Request comes out
	}{
		{"project name then reason", []step{{typ: "aurora"}, {key: "enter"}, {key: "enter"}}, true},
		{"wrong word", []step{{typ: "kill"}, {key: "enter"}, {key: "enter"}}, false},
		{"the old plan word is not the name", []step{{typ: "kill"}, {key: "enter"}}, false},
		{"empty", []step{{key: "enter"}, {key: "enter"}}, false},
		{"whitespace only", []step{{typ: "   "}, {key: "enter"}, {key: "enter"}}, false},
		{"case differs", []step{{typ: "Aurora"}, {key: "enter"}, {key: "enter"}}, false},
		{"trimmed like the CLI", []step{{typ: "  aurora "}, {key: "enter"}, {key: "enter"}}, true},
		{"repo name is not the project", []step{{typ: "acme/app"}, {key: "enter"}, {key: "enter"}}, false},
		{"prefix", []step{{typ: "auror"}, {key: "enter"}, {key: "enter"}}, false},
		{"backspace fixes", []step{{typ: "aurorax"}, {key: "bs"}, {key: "enter"}, {key: "enter"}}, true},
		{"esc at the word", []step{{typ: "aurora"}, {key: "esc"}, {key: "enter"}}, false},
		{"esc at the reason", []step{{typ: "aurora"}, {key: "enter"}, {key: "esc"}, {key: "enter"}}, false},
		{"y does not confirm a project kill", []step{{typ: "y"}, {key: "enter"}, {key: "enter"}}, false},
		{"paste with newline does not submit", []step{{typ: "aurora\n"}, {key: "enter"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := NewFlow("aurora")
			if n := f.Start(KillAll, repoT, true); n != "" {
				t.Fatal(n)
			}
			reqs := run(f, c.steps)
			if (len(reqs) == 1) != c.want || len(reqs) > 1 {
				t.Fatalf("requests = %d, want %v", len(reqs), c.want)
			}
			if c.want {
				r := reqs[0]
				if r.Kind != KillAll || r.Target != (Target{}) || r.Project != "aurora" || r.Reason != DefaultReason {
					t.Fatalf("request = %+v", r)
				}
			}
			if wantActive := !c.want && !strings.HasPrefix(c.name, "esc"); wantActive != f.Active() {
				t.Fatalf("active = %v", f.Active())
			}
		})
	}
}

func TestKillAllReason(t *testing.T) {
	f := NewFlow("aurora")
	f.Start(KillAll, Target{}, true)
	r := run(f, []step{{typ: "aurora"}, {key: "enter"}, {typ: "run\x1b[2Jaway‮\n"}, {key: "enter"}})
	if len(r) != 1 || r[0].Reason != "run[2Jaway" {
		t.Fatalf("%+v", r)
	}
	// An over-long reason is cut.
	f = NewFlow("aurora")
	f.Start(KillAll, Target{}, true)
	r = run(f, []step{{typ: "aurora"}, {key: "enter"}, {typ: strings.Repeat("é", 500)}, {key: "enter"}})
	if len(r) != 1 || len([]rune(r[0].Reason)) != MaxReason {
		t.Fatalf("reason length: %+v", r)
	}
}

func TestKillReasonSanitized(t *testing.T) {
	got := CleanLine("a\x1b]52;c;AAAA\x07b\r\nc​d e\x00\xff", 0)
	if strings.ContainsAny(got, "\x1b\x07\r\n\x00​ ") || strings.ContainsRune(got, 0xfffd) {
		t.Fatalf("%q", got)
	}
}

func TestKillRepoNeedsY(t *testing.T) {
	cases := []struct {
		name  string
		steps []step
		want  bool
	}{
		{"y", []step{{typ: "y"}}, true},
		{"Y", []step{{typ: "Y"}}, true},
		{"enter", []step{{key: "enter"}}, true},
		{"n", []step{{typ: "n"}, {key: "enter"}}, false},
		{"esc", []step{{key: "esc"}, {key: "enter"}}, false},
		{"other key ignored then n", []step{{typ: "x"}, {typ: "n"}}, false},
		{"a paste containing y is not a y", []step{{typ: "xyz"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := NewFlow("aurora")
			f.Start(KillRepo, repoT, true)
			if p := f.Prompt(); !strings.Contains(p, "kill repository acme/app?") {
				t.Fatalf("prompt = %q", p)
			}
			reqs := run(f, c.steps)
			if (len(reqs) == 1) != c.want {
				t.Fatalf("requests = %d", len(reqs))
			}
			if c.want && (reqs[0].Kind != KillRepo || reqs[0].Target != repoT) {
				t.Fatalf("%+v", reqs[0])
			}
		})
	}
}

func TestResumeNeedsRepoName(t *testing.T) {
	cases := []struct {
		name  string
		typed string
		want  bool
	}{
		{"the repository", "acme/app", true},
		{"padded", " acme/app\t", true},
		{"the project name is the wrong scope", "aurora", false},
		{"resume", "resume", false},
		{"y", "y", false},
		{"case", "ACME/APP", false},
		{"empty", "", false},
		{"blank", "  ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := NewFlow("aurora")
			f.Start(ResumeRepo, repoT, true)
			reqs := run(f, []step{{typ: c.typed}, {key: "enter"}})
			if (len(reqs) == 1) != c.want {
				t.Fatalf("requests = %d", len(reqs))
			}
			if c.want && (reqs[0].Kind != ResumeRepo || reqs[0].Target != repoT) {
				t.Fatalf("%+v", reqs[0])
			}
		})
	}
}

func TestResumeAllNeedsProjectName(t *testing.T) {
	for typed, want := range map[string]bool{"aurora": true, " aurora": true, "resume": false, "acme/app": false, "Aurora": false, "": false} {
		f := NewFlow("aurora")
		f.Start(ResumeAll, repoT, true)
		reqs := run(f, []step{{typ: typed}, {key: "enter"}})
		if (len(reqs) == 1) != want {
			t.Errorf("typed %q: requests = %d", typed, len(reqs))
		}
		if want && reqs[0].Target != (Target{}) {
			t.Errorf("an all-project resume carried a repository: %+v", reqs[0])
		}
	}
}

func TestWrongWordCanRetry(t *testing.T) {
	f := NewFlow("aurora")
	f.Start(ResumeAll, Target{}, true)
	reqs := run(f, []step{{typ: "nope"}, {key: "enter"}, {typ: "aurora"}, {key: "enter"}})
	if len(reqs) != 1 {
		t.Fatal("the retry after a wrong word did not go through")
	}
}

func TestKeysDisabledWhenOffline(t *testing.T) {
	for _, k := range []Kind{KillAll, KillRepo, ResumeAll, ResumeRepo} {
		f := NewFlow("aurora")
		if f.Start(k, repoT, false) == "" || f.Active() {
			t.Errorf("kind %d started while offline", k)
		}
	}
	// A prompt left open while the data goes stale cannot be submitted.
	f := NewFlow("aurora")
	f.Start(KillRepo, repoT, true)
	if r := f.Enter(false); r != nil || f.Active() || f.InFlight() {
		t.Fatalf("submitted while offline: %v", r)
	}
	f.Start(ResumeRepo, repoT, true)
	f.Type("acme/app", true)
	if r := f.Enter(false); r != nil || f.Active() {
		t.Fatal("typed resume went through while offline")
	}
}

func TestStartRefusals(t *testing.T) {
	f := NewFlow("aurora")
	if f.Start(KillRepo, Target{}, true) == "" {
		t.Error("repo action with no selection started")
	}
	if f.Start(Kind(0), repoT, true) == "" || f.Start(Kind(9), repoT, true) == "" {
		t.Error("an unknown kind started")
	}
	if NewFlow("").Start(KillAll, Target{}, true) == "" {
		t.Error("started with no project name")
	}
	f.Start(KillRepo, repoT, true)
	if f.Start(ResumeAll, Target{}, true) == "" {
		t.Error("a second prompt replaced the open one")
	}
}

func TestSecondKeyWaitsForWrite(t *testing.T) {
	f := NewFlow("aurora")
	f.Start(KillRepo, repoT, true)
	if f.Enter(true) == nil {
		t.Fatal("no request")
	}
	// Double submit: nothing more comes out of the closed prompt.
	if f.Enter(true) != nil || f.Type("y", true) != nil {
		t.Fatal("a second submit produced a request")
	}
	if n := f.Start(KillAll, Target{}, true); n == "" || f.Active() {
		t.Fatal("a second key started while the write was in flight")
	}
	f.Done()
	if f.Start(KillAll, Target{}, true) != "" {
		t.Fatal("could not start after Done")
	}
}

func TestSelectionSurvivesResort(t *testing.T) {
	f := NewFlow("aurora")
	sel := Target{Slug: "acme__app", Name: "acme/app"}
	f.Start(ResumeRepo, sel, true)
	sel = Target{Slug: "other__thing", Name: "other/thing"} // the cursor moved; the caller's value changed
	if !strings.Contains(f.Prompt(), "acme/app") || strings.Contains(f.Prompt(), "other/thing") {
		t.Fatalf("prompt = %q", f.Prompt())
	}
	// Typing the NEW row's name does not confirm the captured target.
	if r := run(f, []step{{typ: sel.Name}, {key: "enter"}}); len(r) != 0 {
		t.Fatal("confirmed with the wrong repository's name")
	}
	r := run(f, []step{{typ: "acme/app"}, {key: "enter"}})
	if len(r) != 1 || r[0].Target.Slug != "acme__app" {
		t.Fatalf("%+v", r)
	}
}

func TestPromptEscCancels(t *testing.T) {
	for _, k := range []Kind{KillAll, KillRepo, ResumeAll, ResumeRepo} {
		f := NewFlow("aurora")
		f.Start(k, repoT, true)
		f.Esc()
		if f.Active() || f.Prompt() != "" || f.InFlight() || f.Enter(true) != nil {
			t.Errorf("kind %d: Esc left a prompt", k)
		}
	}
}

func TestPromptSanitizesRepoName(t *testing.T) {
	f := NewFlow("aurora")
	f.Start(KillRepo, Target{Slug: "x", Name: "evil\x1b[2J‮good"}, true)
	if p := f.Prompt(); strings.ContainsAny(p, "\x1b‮") {
		t.Fatalf("%q", p)
	}
}

// ------------------------------------------------------------ Execute

func newDB(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func node(t *testing.T, f *gcpfake.RTDB, path string) map[string]any {
	t.Helper()
	m, _ := f.Value(path).(map[string]any)
	return m
}

func TestExecuteKillAndResume(t *testing.T) {
	f, c := newDB(t)
	ctx := context.Background()
	o := Execute(ctx, c, Request{Kind: KillRepo, Project: "aurora", Target: repoT, Reason: "loop"}, "me@x")
	k := node(t, f, budget.PathKillRepo("acme__app"))
	if !o.Written || k["on"] != true || k["by"] != "me@x" || k["reason"] != "loop" || k["at"] == nil {
		t.Fatalf("%+v %v", o, k)
	}
	if f.Value(budget.PathKillGlobal) != nil {
		t.Fatal("a repository kill touched the global switch")
	}
	// Scope mixup: a project kill writes the global node only.
	Execute(ctx, c, Request{Kind: KillAll, Project: "aurora", Reason: "r"}, "me@x")
	if node(t, f, budget.PathKillGlobal)["on"] != true {
		t.Fatal("no global switch")
	}
	// Resuming the repository warns while the global switch is on.
	o = Execute(ctx, c, Request{Kind: ResumeRepo, Project: "aurora", Target: repoT, Reason: DefaultReason}, "me@x")
	if !o.Written || !strings.Contains(o.Notice, "still on") || node(t, f, budget.PathKillRepo("acme__app"))["on"] != false {
		t.Fatalf("%+v", o)
	}
	if node(t, f, budget.PathKillGlobal)["on"] != true {
		t.Fatal("a repository resume cleared the global switch")
	}
	o = Execute(ctx, c, Request{Kind: ResumeAll, Project: "aurora", Reason: DefaultReason}, "me@x")
	if !o.Written || node(t, f, budget.PathKillGlobal)["on"] != false {
		t.Fatalf("%+v", o)
	}
}

func TestResumeWarnsGlobalStillOn(t *testing.T) {
	f, c := newDB(t)
	f.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "boss\x1b[2J@x", "at": 1})
	f.Set(budget.PathKillRepo("acme__app"), map[string]any{"on": true, "by": "x", "at": 1})
	o := Execute(context.Background(), c, Request{Kind: ResumeRepo, Project: "aurora", Target: repoT, Reason: "r"}, "me@x")
	if !strings.Contains(o.Notice, "project-wide kill switch is still on") || strings.ContainsRune(o.Notice, 0x1b) {
		t.Fatalf("%q", o.Notice)
	}
}

func TestAlreadyKilledNoop(t *testing.T) {
	f, c := newDB(t)
	f.Set(budget.PathKillRepo("acme__app"), map[string]any{"on": true, "by": "boss@x", "at": 5, "reason": "first"})
	o := Execute(context.Background(), c, Request{Kind: KillRepo, Project: "aurora", Target: repoT, Reason: "second"}, "me@x")
	k := node(t, f, budget.PathKillRepo("acme__app"))
	if !o.Already || o.Written || k["by"] != "boss@x" || k["reason"] != "first" || !strings.Contains(o.Notice, "already killed (by boss@x") {
		t.Fatalf("%+v %v", o, k)
	}
	o = Execute(context.Background(), c, Request{Kind: ResumeAll, Project: "aurora", Reason: "r"}, "me@x")
	if !o.Already || f.Value(budget.PathKillGlobal) != nil {
		t.Fatalf("resume of an absent switch wrote: %+v", o)
	}
}

func TestViewerRefusalShown(t *testing.T) {
	f, c := newDB(t)
	f.DenyNext(1)
	o := Execute(context.Background(), c, Request{Kind: KillAll, Project: "aurora", Reason: "r"}, "me@x")
	if !o.Denied || o.Written || o.Notice != budget.AdminDeniedText("aurora") || !strings.Contains(o.Notice, "not a budget admin for project aurora") {
		t.Fatalf("%+v", o)
	}
	if f.Value(budget.PathKillGlobal) != nil {
		t.Fatal("a denied write changed the switch")
	}
}

// raceDB loses the first conditional write to another admin.
type raceDB struct {
	*rtdb.Client
	f     *gcpfake.RTDB
	races int
	puts  int
}

func (r *raceDB) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	r.puts++
	if r.races > 0 {
		r.races--
		r.f.Set(path, map[string]any{"on": false, "by": "rival@x", "at": r.puts})
	}
	return r.Client.PutIfMatch(ctx, path, etag, v)
}

func TestConflictRereads(t *testing.T) {
	f, c := newDB(t)
	db := &raceDB{Client: c, f: f, races: 1}
	o := Execute(context.Background(), db, Request{Kind: KillAll, Project: "aurora", Reason: "r"}, "me@x")
	if !o.Written || db.puts != 2 || node(t, f, budget.PathKillGlobal)["by"] != "me@x" {
		t.Fatalf("%+v puts=%d node=%v", o, db.puts, f.Value(budget.PathKillGlobal))
	}
	// Endless contention ends, with nothing claimed.
	f2, c2 := newDB(t)
	db = &raceDB{Client: c2, f: f2, races: 100}
	o = Execute(context.Background(), db, Request{Kind: KillAll, Project: "aurora", Reason: "r"}, "me@x")
	if o.Written || !errors.Is(o.Err, budget.ErrKillContention) || db.puts != budget.SetAttempts {
		t.Fatalf("%+v puts=%d node=%v", o, db.puts, f.Value(budget.PathKillGlobal))
	}
}

func TestSetKillHooksAndNoopSkipsConfirm(t *testing.T) {
	f, c := newDB(t)
	f.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "b", "at": 1})
	called := false
	res, err := budget.SetKill(context.Background(), c, budget.PathKillGlobal, true, "me", "r", budget.KillHooks{Confirm: func(budget.Kill) error { called = true; return nil }})
	if err != nil || !res.Already || called {
		t.Fatalf("%+v %v called=%v", res, err, called)
	}
	boom := errors.New("declined")
	_, err = budget.SetKill(context.Background(), c, budget.PathKillGlobal, false, "me", "r", budget.KillHooks{Confirm: func(budget.Kill) error { return boom }})
	if err != boom || node(t, f, budget.PathKillGlobal)["on"] != true {
		t.Fatalf("err=%v", err)
	}
}

func TestExecuteKeepsJSONShape(t *testing.T) {
	f, c := newDB(t)
	Execute(context.Background(), c, Request{Kind: KillAll, Project: "aurora", Reason: "why"}, "me@x")
	b, _ := json.Marshal(f.Value(budget.PathKillGlobal))
	var k budget.Kill
	if err := json.Unmarshal(b, &k); err != nil || !k.On || k.By != "me@x" || k.Reason != "why" || k.At == 0 {
		t.Fatalf("%s %v", b, err)
	}
}

func TestYWhileStaleCancels(t *testing.T) {
	f := NewFlow("aurora")
	f.Start(KillRepo, repoT, true)
	if r := f.Type("y", false); r != nil || f.Active() || f.InFlight() {
		t.Fatalf("y submitted while not live: %v", r)
	}
}

func TestUnprintableNamesRefused(t *testing.T) {
	if NewFlow("au\u202erora").Start(KillAll, Target{}, true) == "" {
		t.Error("project name with a BiDi control accepted")
	}
	if NewFlow("aurora").Start(ResumeRepo, Target{Slug: "x", Name: "a\x1b[2Jb"}, true) == "" {
		t.Error("repo name with an escape accepted")
	}
}

type hungDB struct{ budget.KillDB }

func (hungDB) GetETag(ctx context.Context, _ string, _ any) (string, bool, error) {
	<-ctx.Done()
	return "", false, ctx.Err()
}
func (hungDB) Get(ctx context.Context, _ string, _ any) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

func TestExecuteBoundedByTimeout(t *testing.T) {
	old := ExecuteTimeout
	ExecuteTimeout = 50 * time.Millisecond
	defer func() { ExecuteTimeout = old }()
	done := make(chan Outcome, 1)
	go func() {
		done <- Execute(context.Background(), hungDB{}, Request{Kind: KillAll, Project: "aurora", Reason: "r"}, "me")
	}()
	select {
	case o := <-done:
		if o.Err == nil || o.Written {
			t.Fatalf("%+v", o)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Execute did not return on a hung database")
	}
}
