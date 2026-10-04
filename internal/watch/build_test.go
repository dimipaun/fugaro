package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func ms(t time.Time) int64 { return t.UnixMilli() }

const usd = budget.Micros(1_000_000)

func put(t *testing.T, s *State, src Source, path, data string, at time.Time) {
	t.Helper()
	if err := s.Apply(src, rtdb.Event{Type: "put", Path: path, Data: json.RawMessage(data)}, at); err != nil {
		t.Fatal(err)
	}
}

func newState(t *testing.T) *State {
	s := NewState()
	s.SetDay(budget.Day(t0))
	return s
}

func agent(repo, title string, extra string) string {
	return fmt.Sprintf(`{"repo":%q,"title":%q,"requestedBy":"a@b.c","startedAt":%d,"updatedAt":%d%s}`,
		repo, title, ms(t0.Add(-10*time.Minute)), ms(t0), extra)
}

func TestBuildGroupsByRepo(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{
	 "acme%2Fapp":{"r2":`+agent("acme/app", "second", `,"auth":"api-key","spent":2000000`)+`,"r1":`+agent("acme/app", "first", `,"coder":"opus","reviewer":"sonnet","round":2,"stage":"coding","verify":"tests"`)+`},
	 "lib":{"x":`+agent("lib", "t", "")+`}}`, t0)
	put(t, s, SrcRepos, "/", `{"acme%2Fapp":{"counted":3000000,"spent":2000000},"lib":{"spent":500000}}`, t0)
	put(t, s, SrcGlobal, "/", `{"counted":3000000,"spent":2500000}`, t0)
	put(t, s, SrcConfig, "/", `{"mode":"enforce","caps":{"global":{"dailyMicros":10000000},"defaults":{"repoDailyMicros":6000000},"repos":{"lib":{"dailyMicros":1000000}}}}`, t0)
	v := Build(s, t0, Config{})
	if v.Mode != "enforce" || v.Day != "2026-10-03" {
		t.Fatalf("mode/day %q %q", v.Mode, v.Day)
	}
	if len(v.Repos) != 2 || v.Repos[0].Name != "acme/app" || v.Repos[0].Slug != "acme%2Fapp" {
		t.Fatalf("repos %+v", v.Repos)
	}
	a := v.Repos[0]
	if len(a.Runs) != 2 || a.Runs[0].Run != "r1" || a.Runs[1].Run != "r2" {
		t.Fatalf("runs %+v", a.Runs)
	}
	r1 := a.Runs[0]
	if r1.Round != "2" || r1.Stage != "coding" || r1.Models != "opus / sonnet" || r1.Verify != "tests" {
		t.Fatalf("r1 %+v", r1)
	}
	if a.Bar.Cap != 6*usd || !a.Bar.Has || int(a.Bar.Percent) != 50 {
		t.Fatalf("default cap bar %+v", a.Bar)
	}
	if lib := v.Repos[1]; lib.Bar.Cap != usd || int(lib.Bar.Percent) != 0 && lib.Bar.Used != 0 {
		t.Fatalf("lib bar %+v", lib.Bar)
	}
	p := v.Project
	if p.Runs != 3 || p.Counted != 3*usd || p.Bar.Cap != 10*usd {
		t.Fatalf("project %+v", p)
	}
	if p.RunHours < 0.49 || p.RunHours > 0.51 { // 3 runs * 10 min
		t.Fatalf("run hours %v", p.RunHours)
	}
}

// TestRepoNameFromConfig: a slug that isn't a readable "owner/name" once
// decoded (long repositories are hashed into a short slug, design §3.2) must
// show the project's own repo name, as `fugaro budget show` does, not the
// opaque slug; a repo the config doesn't name falls back to the slug.
func TestRepoNameFromConfig(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"dimipaun-fugaro-a6023ed":{"a":`+agent("evil-free-text", "t", "")+`},"other-slug":{"b":`+agent("x", "t", "")+`}}`, t0)
	v := Build(s, t0, Config{Names: map[string]string{"dimipaun-fugaro-a6023ed": "dimipaun/fugaro"}})
	var named, unnamed *RepoBlock
	for i := range v.Repos {
		switch v.Repos[i].Slug {
		case "dimipaun-fugaro-a6023ed":
			named = &v.Repos[i]
		case "other-slug":
			unnamed = &v.Repos[i]
		}
	}
	if named == nil || named.Name != "dimipaun/fugaro" {
		t.Fatalf("named repo = %+v, want dimipaun/fugaro", named)
	}
	if unnamed == nil || unnamed.Name != "other-slug" {
		t.Fatalf("unnamed repo = %+v, want the slug as a fallback", unnamed)
	}
	// The job-written registry repo field is never trusted as a name.
	if named.Name == "evil-free-text" || unnamed.Name == "x" {
		t.Fatalf("the agent's own repo field must never name a block: %+v %+v", named, unnamed)
	}
}

func TestCapBarMissingCap(t *testing.T) {
	s := newState(t)
	put(t, s, SrcRepos, "/", `{"r":{"counted":5000000}}`, t0)
	put(t, s, SrcGlobal, "/", `{"counted":5000000}`, t0)
	v := Build(s, t0, Config{})
	if v.Project.Bar.Has || v.Repos[0].Bar.Has {
		t.Fatalf("no cap must not be a bar: %+v %+v", v.Project.Bar, v.Repos[0].Bar)
	}
	// A zero cap is a cap (everything is over it), not "no cap".
	put(t, s, SrcConfig, "/", `{"caps":{"global":{"dailyMicros":0}}}`, t0)
	if b := Build(s, t0, Config{}).Project.Bar; !b.Has || b.Percent < 100 {
		t.Fatalf("zero cap %+v", b)
	}
}

func TestNotionalNeverInCapBar(t *testing.T) {
	s := newState(t)
	put(t, s, SrcRepos, "/", `{"r":{"counted":1000000,"spent":1000000,"notional":9000000}}`, t0)
	put(t, s, SrcGlobal, "/", `{"counted":1000000,"spent":1000000,"notional":9000000}`, t0)
	put(t, s, SrcConfig, "/", `{"caps":{"global":{"dailyMicros":10000000},"defaults":{"repoDailyMicros":10000000}}}`, t0)
	put(t, s, SrcAgents, "/", `{"r":{"o":`+agent("r", "t", `,"auth":"oauth","spent":9000000`)+`}}`, t0)
	v := Build(s, t0, Config{})
	for _, b := range []Bar{v.Project.Bar, v.Repos[0].Bar} {
		if b.Used != usd || int(b.Percent) != 10 {
			t.Fatalf("bar %+v includes notional", b)
		}
	}
	if v.Repos[0].Notional != 9*usd || v.Project.Notional != 9*usd {
		t.Fatal("notional must still be reported")
	}
	if r := v.Repos[0].Runs[0]; !r.Notional || r.Spent != 9*usd {
		t.Fatalf("oauth run %+v", r)
	}
}

// TestOtherRowReconciles: the project's counter and each repository's are
// separate RTDB nodes (design §6.2), written together but read as two
// independent streams. A repository the view never learned to attribute
// (one whose own node a run's spend never reached, was pruned, or hasn't
// streamed in) must not make the total look unexplained: an "other" row
// carries the remainder, so the visible rows always add up to the total.
func TestOtherRowReconciles(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"lib":{"a":`+agent("lib", "t", "")+`}}`, t0)
	put(t, s, SrcRepos, "/", `{"lib":{"counted":1000000,"spent":1000000,"notional":200000}}`, t0)
	put(t, s, SrcGlobal, "/", `{"counted":1560000,"spent":1000000,"notional":756000}`, t0)
	v := Build(s, t0, Config{})
	if len(v.Repos) != 2 {
		t.Fatalf("repos %+v", v.Repos)
	}
	var other *RepoBlock
	for i := range v.Repos {
		if v.Repos[i].Slug == "" {
			other = &v.Repos[i]
		}
	}
	if other == nil {
		t.Fatal("no other row for the unattributed remainder")
	}
	if other.Counted != 560_000 || other.Spent != 0 || other.Notional != 556_000 {
		t.Fatalf("other row = %+v, want the project minus lib's share", other)
	}
	// Nothing to reconcile: no other row at all.
	s2 := newState(t)
	put(t, s2, SrcRepos, "/", `{"lib":{"counted":1000000,"spent":1000000}}`, t0)
	put(t, s2, SrcGlobal, "/", `{"counted":1000000,"spent":1000000}`, t0)
	v2 := Build(s2, t0, Config{})
	for _, r := range v2.Repos {
		if r.Slug == "" {
			t.Fatalf("spurious other row: %+v", r)
		}
	}
}

func spend(t *testing.T, s *State, at time.Time, spent int64) {
	put(t, s, SrcGlobal, "/", fmt.Sprintf(`{"spent":%d}`, spent), at)
}

func TestBurnWindow(t *testing.T) {
	min := func(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Minute))) }
	s := newState(t)

	// Empty window.
	if _, ok := s.Burn("", t0); ok {
		t.Fatal("no samples, no rate")
	}
	spend(t, s, min(0), 0)
	if _, ok := s.Burn("", min(0.5)); ok {
		t.Fatal("first 60 s show no rate")
	}
	spend(t, s, min(2), 2_000_000)
	if per, ok := s.Burn("", min(2)); !ok || per != 1_000_000 {
		t.Fatalf("rate %v %v, want $1/min", per, ok)
	}
	// A flat stretch dilutes the rate: the window ends at now.
	if per, ok := s.Burn("", min(4)); !ok || per != 500_000 {
		t.Fatalf("flat rate %v %v", per, ok)
	}
	// Samples older than five minutes leave the window.
	spend(t, s, min(8), 2_000_000)
	spend(t, s, min(9), 4_000_000)
	// Window at minute 9 holds the samples of minutes 8 and 9: $2 in 1 min.
	if per, ok := s.Burn("", min(9)); !ok || per != 2_000_000 {
		t.Fatalf("windowed rate %v %v", per, ok)
	}

	// A counter that goes back (midnight, a rewrite) restarts the window;
	// it is never a negative rate.
	spend(t, s, min(10), 100)
	if per, ok := s.Burn("", min(10.5)); ok {
		t.Fatalf("after a reset: %v", per)
	}
	if per, ok := s.Burn("", min(12)); !ok || per < 0 {
		t.Fatalf("after warmup: %v %v", per, ok)
	}

	// New day and reconnect start empty.
	s.SetDay(s.Day + 1)
	if _, ok := s.Burn("", min(13)); ok {
		t.Fatal("SetDay clears the window")
	}
	spend(t, s, min(13), 1)
	s.Reconnected()
	if _, ok := s.Burn("", min(20)); ok {
		t.Fatal("Reconnected clears the window")
	}
}

func TestBurnFast(t *testing.T) {
	s := newState(t)
	put(t, s, SrcConfig, "/", `{"caps":{"global":{"dailyMicros":80000000}}}`, t0) // $10/h default alert
	put(t, s, SrcAgents, "/", `{"r":{"a":`+agent("r", "t", "")+`}}`, t0)
	spend(t, s, t0, 0)
	spend(t, s, t0.Add(2*time.Minute), 4_000_000) // $2/min = $120/h
	v := Build(s, t0.Add(2*time.Minute), Config{})
	if !v.Project.Burn.Known || !v.Project.Burn.Fast {
		t.Fatalf("%+v", v.Project.Burn)
	}
	hi := 200 * usd
	if Build(s, t0.Add(2*time.Minute), Config{BurnAlertPerHour: &hi}).Project.Burn.Fast {
		t.Fatal("configured alert ignored")
	}
	// No cap, no alert: never fast.
	s2 := newState(t)
	put(t, s2, SrcAgents, "/", `{"r":{"a":`+agent("r", "t", "")+`}}`, t0)
	spend(t, s2, t0, 0)
	spend(t, s2, t0.Add(2*time.Minute), 4_000_000)
	if Build(s2, t0.Add(2*time.Minute), Config{}).Project.Burn.Fast {
		t.Fatal("fast without a cap")
	}
}

func TestStuckClassification(t *testing.T) {
	at := func(d time.Duration) int64 { return ms(t0.Add(d)) }
	tests := []struct {
		name  string
		extra string // entry JSON fields
		upd   time.Duration
		want  Health
		dl    Deadline
	}{
		{"fresh", ``, -10 * time.Second, HealthOK, DeadlineOK},
		{"edge 60s is ok", ``, -60 * time.Second, HealthOK, DeadlineOK},
		{"silent", ``, -61 * time.Second, HealthSilent, DeadlineOK},
		{"edge 180s silent", ``, -180 * time.Second, HealthSilent, DeadlineOK},
		{"lost", ``, -181 * time.Second, HealthLost, DeadlineOK},
		{"amber at 80%", fmt.Sprintf(`,"stageStartedAt":%d,"stageDeadline":%d`, at(-81*time.Second), at(19*time.Second)), 0, HealthOK, DeadlineNear},
		{"not yet amber", fmt.Sprintf(`,"stageStartedAt":%d,"stageDeadline":%d`, at(-79*time.Second), at(21*time.Second)), 0, HealthOK, DeadlineOK},
		{"over", fmt.Sprintf(`,"stageStartedAt":%d,"stageDeadline":%d`, at(-200*time.Second), at(-time.Second)), 0, HealthOK, DeadlineOver},
		{"deadline without stage start", fmt.Sprintf(`,"stageDeadline":%d`, at(time.Hour)), 0, HealthOK, DeadlineOK},
		{"over without stage start", fmt.Sprintf(`,"stageDeadline":%d`, at(-time.Second)), 0, HealthOK, DeadlineOver},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newState(t)
			e := fmt.Sprintf(`{"repo":"r","requestedBy":"x","startedAt":%d,"updatedAt":%d%s}`, at(-time.Hour), at(tt.upd), tt.extra)
			put(t, s, SrcAgents, "/", `{"r":{"a":`+e+`}}`, t0)
			r := Build(s, t0, Config{}).Repos[0].Runs[0]
			if r.Health != tt.want || r.Deadline != tt.dl {
				t.Fatalf("got health %v deadline %v, want %v %v", r.Health, r.Deadline, tt.want, tt.dl)
			}
		})
	}
	// No heartbeat yet: judged from the start.
	s := newState(t)
	put(t, s, SrcAgents, "/", fmt.Sprintf(`{"r":{"a":{"repo":"r","requestedBy":"x","startedAt":%d}}}`, at(-5*time.Minute)), t0)
	if r := Build(s, t0, Config{}).Repos[0].Runs[0]; r.Health != HealthLost {
		t.Fatalf("no updatedAt: %v", r.Health)
	}
	// No timestamps at all: no flag.
	s = newState(t)
	put(t, s, SrcAgents, "/", `{"r":{"a":{"repo":"r","requestedBy":"x"}}}`, t0)
	if r := Build(s, t0, Config{}).Repos[0].Runs[0]; r.Health != HealthOK || r.HasAge {
		t.Fatalf("no timestamps: %+v", r)
	}
}

func TestHostileTitlesSanitized(t *testing.T) {
	hostile := []string{
		"a\x1b[2Jb", "a\x1b]52;c;ZXZpbA==\x07b", "a\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "a\u009b2Jb",
		"x‮evil", "a​b", "line1\nline2", "a\rb", "a\x00b", "bad\xffutf",
		strings.Repeat("A", 10_000), strings.Repeat("\x1b[31m", 3000) + "z", strings.Repeat("é", 10_000),
	}
	for i, h := range hostile {
		s := newState(t)
		entry := func(f string) string {
			j, _ := json.Marshal(h)
			return fmt.Sprintf(`{"repo":%s,"title":%s,"stage":%s,"verify":%s,"coder":%s,"reviewer":%s,"auth":%s,"halted":%s,"requestedBy":"x"}`, j, j, j, j, j, j, j, j)
		}
		j, _ := json.Marshal(h)
		put(t, s, SrcAgents, "/", fmt.Sprintf(`{%s:{%s:%s}}`, j2(h), j2(h), entry("")), t0)
		put(t, s, SrcConfig, "/", fmt.Sprintf(`{"kill":{"global":{"on":true,"by":%s,"reason":%s,"at":1},"repos":{%s:{"on":true,"by":%s,"reason":%s}}},"mode":%s}`, j, j, j2(h), j, j, j), t0)
		s.Apply(SrcConfig, rtdb.Event{Type: "error", Err: errors.New(h)}, t0)
		v := Build(s, t0, Config{})

		var all []string
		all = append(all, v.Mode, v.Conn.Reason, v.Project.Kill.By, v.Project.Kill.Reason)
		for _, b := range v.Repos {
			all = append(all, b.Name, b.Kill.By, b.Kill.Reason) // Slug is the raw wire key, for paths only
			for _, r := range b.Runs {
				all = append(all, r.Run, r.Title, r.Stage, r.Round, r.Verify, r.Models, r.Auth, r.Halted)
			}
		}
		for _, f := range all {
			for _, r := range f {
				if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x200b || r == 0x202e {
					t.Fatalf("case %d: %q holds %U", i, f, r)
				}
			}
			if n := len([]rune(f)); n > maxText+1 {
				t.Fatalf("case %d: %d runes", i, n)
			}
			if strings.Contains(f, "]52;") || strings.Contains(f, "[2J") && strings.Contains(f, "\x1b") {
				t.Fatalf("case %d: escape survived in %q", i, f)
			}
		}
	}
}

// j2 is h as a JSON object key (RTDB keys cannot hold control characters on
// the wire, but a hostile writer's key reaches Build as whatever Unkey gives).
func j2(h string) string { j, _ := json.Marshal(budget.Key(h)); return string(j) }

func TestAbsentFieldsShowDash(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"r":{"a":{"repo":"r","requestedBy":"x"}}}`, t0)
	r := Build(s, t0, Config{}).Repos[0].Runs[0]
	for name, v := range map[string]string{"title": r.Title, "stage": r.Stage, "round": r.Round, "verify": r.Verify, "models": r.Models, "auth": r.Auth} {
		if v != "-" {
			t.Errorf("%s = %q, want -", name, v)
		}
	}
	if r.HasSpent || r.Halted != "" || r.Notional {
		t.Fatalf("%+v", r)
	}
}

func TestKilledFirst(t *testing.T) {
	s := newState(t)
	put(t, s, SrcRepos, "/", `{"big":{"spent":9000000},"mid":{"spent":5000000},"dead":{"spent":1}}`, t0)
	put(t, s, SrcConfig, "/", `{"kill":{"repos":{"dead":{"on":true,"by":"me","at":1000,"reason":"why"},"off":{"on":false}}}}`, t0)
	v := Build(s, t0, Config{})
	var order []string
	for _, b := range v.Repos {
		order = append(order, b.Slug)
	}
	if strings.Join(order, ",") != "dead,big,mid" {
		t.Fatalf("order %v (a switch that is off adds no block)", order)
	}
	if k := v.Repos[0].Kill; !k.On || k.By != "me" || k.Reason != "why" || !k.At.Equal(time.UnixMilli(1000)) {
		t.Fatalf("%+v", k)
	}
	put(t, s, SrcConfig, "/", `{"kill":{"global":{"on":true,"by":"op"}}}`, t0)
	if v := Build(s, t0, Config{}); !v.Project.Kill.On || v.Project.Kill.By != "op" {
		t.Fatalf("%+v", v.Project.Kill)
	}
}

func TestMalformedNodesDoNotPanic(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"r":{"a":"string","b":{"round":"x"},"c":{"repo":"r","spent":-5}}}`, t0)
	put(t, s, SrcRepos, "/", `{"r":{"counted":"nan"}}`, t0)
	put(t, s, SrcGlobal, "/", `{"counted":-9}`, t0)
	put(t, s, SrcConfig, "/", `{"mode":5,"caps":{"global":"x"},"kill":{"global":3}}`, t0)
	v := Build(s, t0, Config{})
	if v.Project.Counted != 0 || v.Mode != "observe" || len(v.Repos) != 1 || len(v.Repos[0].Runs) != 1 || v.Repos[0].Runs[0].Spent != 0 {
		t.Fatalf("%+v", v)
	}
}

func TestConnection(t *testing.T) {
	s := newState(t)
	if c := Build(s, t0, Config{}).Conn; c.Kind != ConnOffline || c.Reason != "connecting" {
		t.Fatalf("before first event: %+v", c)
	}
	alive(s, t0)
	if c := Build(s, t0.Add(45*time.Second), Config{}).Conn; c.Kind != ConnLive || c.Age != 45*time.Second {
		t.Fatalf("%+v", c)
	}
	if c := Build(s, t0.Add(46*time.Second), Config{}).Conn; c.Kind != ConnStale {
		t.Fatalf("%+v", c)
	}
	s.Polling = true
	alive(s, t0.Add(time.Minute))
	if c := Build(s, t0.Add(time.Minute), Config{}).Conn; c.Kind != ConnPolling {
		t.Fatalf("%+v", c)
	}
	s.Apply(SrcAgents, rtdb.Event{Type: "error", Err: fmt.Errorf("x: %w", rtdb.ErrUnavailable)}, t0.Add(time.Minute))
	if c := Build(s, t0.Add(time.Minute), Config{}).Conn; c.Kind != ConnOffline || c.Age != 0 {
		t.Fatalf("%+v", c)
	}
	s.Apply(SrcAgents, rtdb.Event{Type: "error", Err: fmt.Errorf("x: %w", rtdb.ErrPermission)}, t0.Add(time.Minute))
	if c := Build(s, t0.Add(time.Minute), Config{}).Conn; c.Kind != ConnRefused {
		t.Fatalf("%+v", c)
	}
	// Any later event clears it; the first put rebuilds the tree.
	alive(s, t0.Add(2*time.Minute))
	if c := Build(s, t0.Add(2*time.Minute), Config{}).Conn; c.Kind != ConnPolling {
		t.Fatalf("%+v", c)
	}
}

// What finished during an outage is gone after the next put "/".
func TestStatePutAfterReconnectReplacesTree(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"r":{"a":`+agent("r", "t", "")+`,"b":`+agent("r", "t", "")+`}}`, t0)
	s.Apply(SrcAgents, rtdb.Event{Type: "error", Err: rtdb.ErrUnavailable}, t0)
	put(t, s, SrcAgents, "/", `{"r":{"b":`+agent("r", "t", "")+`}}`, t0.Add(time.Minute))
	if rs := Build(s, t0.Add(time.Minute), Config{}).Repos[0].Runs; len(rs) != 1 || rs[0].Run != "b" {
		t.Fatalf("%+v", rs)
	}
}

func TestBadEventDataIsReturned(t *testing.T) {
	s := newState(t)
	if err := s.Apply(SrcAgents, rtdb.Event{Type: "put", Path: "/", Data: json.RawMessage(`{`)}, t0); err == nil {
		t.Fatal("want an error")
	}
}

func alive(s *State, at time.Time) {
	for src := SrcConfig; src <= SrcAgents; src++ {
		s.Apply(src, rtdb.Event{Type: "keep-alive"}, at)
	}
}

// A keep-alive on one stream must not hide a dead one.
func TestHealthIsPerSource(t *testing.T) {
	s := newState(t)
	alive(s, t0)
	s.Apply(SrcAgents, rtdb.Event{Type: "error", Err: rtdb.ErrUnavailable}, t0.Add(10*time.Second))
	for i := 0; i < 5; i++ { // config keeps sending keep-alives
		at := t0.Add(time.Duration(11+i) * time.Second)
		s.Apply(SrcConfig, rtdb.Event{Type: "keep-alive"}, at)
		if c := Build(s, at, Config{}).Conn; c.Kind != ConnOffline {
			t.Fatalf("agents errored, config alive: %+v", c)
		}
	}
	// A silent stream goes stale although the others keep talking.
	s = newState(t)
	alive(s, t0)
	for _, src := range []Source{SrcConfig, SrcGlobal, SrcRepos} {
		s.Apply(src, rtdb.Event{Type: "keep-alive"}, t0.Add(40*time.Second))
	}
	if c := Build(s, t0.Add(50*time.Second), Config{}).Conn; c.Kind != ConnStale {
		t.Fatalf("agents silent 50s: %+v", c)
	}
	// Only that source's own event clears its error.
	s.Apply(SrcAgents, rtdb.Event{Type: "error", Err: rtdb.ErrUnavailable}, t0.Add(51*time.Second))
	s.Apply(SrcAgents, rtdb.Event{Type: "put", Path: "/", Data: json.RawMessage("null")}, t0.Add(52*time.Second))
	alive(s, t0.Add(52*time.Second))
	if c := Build(s, t0.Add(52*time.Second), Config{}).Conn; c.Kind != ConnLive {
		t.Fatalf("%+v", c)
	}
}

func TestAuthRevokedIsNotProofOfLife(t *testing.T) {
	s := newState(t)
	alive(s, t0)
	s.Apply(SrcAgents, rtdb.Event{Type: "auth_revoked"}, t0.Add(time.Second))
	c := Build(s, t0.Add(time.Second), Config{}).Conn
	if c.Kind != ConnOffline || !strings.Contains(c.Reason, "revoked") {
		t.Fatalf("%+v", c)
	}
}

// The block's name is the wire key decoded, never the job-written repo field.
func TestRepoNameIgnoresJobWrittenField(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fa":{"r1":`+agent("acme/prod", "t", "")+`}}`, t0)
	v := Build(s, t0, Config{})
	if len(v.Repos) != 1 || v.Repos[0].Name != "acme/a" {
		t.Fatalf("%+v", v.Repos)
	}
}

// An unreadable switch is shown as on, and its repository stays on screen.
func TestMalformedKillIsShownOn(t *testing.T) {
	s := newState(t)
	put(t, s, SrcConfig, "/", `{"kill":{"global":3,"repos":{"x":"junk"}}}`, t0)
	v := Build(s, t0, Config{})
	if !v.Project.Kill.On || !v.Project.Kill.Unreadable || !strings.Contains(v.Project.Kill.Reason, "unreadable kill switch (runs treat it as ON)") {
		t.Fatalf("%+v", v.Project.Kill)
	}
	if len(v.Repos) != 1 || !v.Repos[0].Kill.On {
		t.Fatalf("block vanished: %+v", v.Repos)
	}
	if got := killText(v.Repos[0].Kill, "!", "KILLED"); !strings.Contains(got, "unreadable kill switch") {
		t.Fatal(got)
	}
}

func TestCleanDropsInvisibles(t *testing.T) {
	for _, in := range []string{"a\tb", "a\u00adb", "a\U000e0041b", "a\u3164b", "a\u2028b", "a\u200bb", "a\u0085b", "a\x1b[2Jb"} {
		if got := clean(in); got != "ab" && got != "a[2Jb" {
			t.Errorf("clean(%q) = %q", in, got)
		}
	}
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"r":{"a":`+agent("r", "ti\ttle", "")+`}}`, t0)
	if got := Build(s, t0, Config{}).Repos[0].Runs[0].Title; got != "title" {
		t.Fatalf("title %q", got)
	}
}

// TestBurnHiddenWithNoLiveRuns: a rolling window still has a positive slope
// for a while after every run of a repository (or the project) stops - a
// trailing value decaying toward zero, not a burn rate - and must not be
// shown once nothing is running.
func TestBurnHiddenWithNoLiveRuns(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"r":{"a":`+agent("r", "t", "")+`}}`, t0)
	put(t, s, SrcRepos, "/", `{"r":{"spent":0}}`, t0)
	put(t, s, SrcGlobal, "/", `{"spent":0}`, t0)
	at := t0.Add(2 * time.Minute)
	put(t, s, SrcRepos, "/", `{"r":{"spent":4000000}}`, at)
	put(t, s, SrcGlobal, "/", `{"spent":4000000}`, at)
	live := Build(s, at, Config{})
	if !live.Project.Burn.Known || !live.Repos[0].Burn.Known {
		t.Fatalf("burn must be known while a run is live: project %+v repo %+v", live.Project.Burn, live.Repos[0].Burn)
	}

	// The run ends (a kill, or it finishes): the registry entry goes away,
	// but the window still spans the spend that happened a moment ago.
	put(t, s, SrcAgents, "/", `{}`, at)
	v := Build(s, at, Config{})
	if v.Project.Runs != 0 || len(v.Repos[0].Runs) != 0 {
		t.Fatalf("no runs must be live: project %+v repo %+v", v.Project, v.Repos[0])
	}
	if v.Project.Burn.Known {
		t.Fatalf("project burn must hide once no run is live: %+v", v.Project.Burn)
	}
	if v.Repos[0].Burn.Known {
		t.Fatalf("repo burn must hide once no run is live: %+v", v.Repos[0].Burn)
	}
}

// A poll delivers a full tree every time and must not clear the burn windows.
func TestPollingKeepsBurnWindows(t *testing.T) {
	s := newState(t)
	s.Handle(Update{Kind: UpdPolling, On: true})
	for i := 0; i <= 12; i++ {
		at := t0.Add(time.Duration(i*5) * time.Second)
		s.Handle(Update{Kind: UpdEvent, Src: SrcGlobal, Now: at,
			Ev: rtdb.Event{Type: "put", Path: "/", Data: json.RawMessage(fmt.Sprintf(`{"spent":%d}`, i*1000000))}})
	}
	if _, ok := s.Burn("", t0.Add(60*time.Second)); !ok {
		t.Fatal("burn unknown under polling")
	}
}
