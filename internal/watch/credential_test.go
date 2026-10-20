package watch

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// --json carries the credential a run is spending on, and the instant a run
// waiting for one expects to resume at (credentials-service plan, task C1.21;
// budget.AgentEntry.Credential and .WaitingUntil). Names and states only,
// never a value: the entry's own field is the account's name, and it is
// sanitised here like every other database-derived string.
func TestWatchJSONCurrentCredential(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{
	 "r1":`+agent("acme/app", "on a named account", `,"credential":"acme-personal"`)+`,
	 "r2":`+agent("acme/app", "has not reported one", "")+`,
	 "r3":`+agent("acme/app", "a name that tries an escape", `,"credential":"acme\u001b[31mpersonal"`)+`}}`, t0)
	v := Build(s, t0, Config{})
	doc := BuildJSON("aurora", v)

	got := map[string]string{}
	for _, r := range doc.Runs {
		got[r.Run] = r.Credential
	}
	if len(got) != 3 {
		t.Fatalf("runs = %+v, want the three of acme/app", doc.Runs)
	}
	if got["r1"] != "acme-personal" {
		t.Fatalf("r1 credential = %q, want the account's name", got["r1"])
	}
	if got["r2"] != "" {
		t.Fatalf("r2 credential = %q, want it absent: a run that reported none is never given one", got["r2"])
	}
	if got["r3"] != "acmepersonal" {
		t.Fatalf("r3 credential = %q, want the escape sequence dropped", got["r3"])
	}

	// The screen names it too, in the one place a row has room: the expanded
	// detail (render.go).
	sel := Cursor{Slug: "acme%2Fapp", Run: "r1"}
	f := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if !strings.Contains(f, "    credential acme-personal") {
		t.Fatalf("the expanded run's detail does not name its credential:\n%s", f)
	}
}

// A run waiting for a credential is shown as the plan words it — "waiting for
// a credential, expected resume T" — from the entry's waiting_until, and the
// wait ends by itself: an instant already past is not a run that is waiting
// (D152's "waiting, expected resume time", task C1.21).
func TestWatchShowsWaitingUntil(t *testing.T) {
	resume := t0.Add(15 * time.Minute)
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{
	 "r1":`+agent("acme/app", "waiting for a turn", fmt.Sprintf(`,"credential":"acme-personal","waiting_until":%d`, ms(resume)))+`,
	 "r2":`+agent("acme/app", "its wait has passed", fmt.Sprintf(`,"waiting_until":%d`, ms(t0.Add(-time.Minute))))+`,
	 "r3":`+agent("acme/app", "nothing pending", "")+`}}`, t0)
	v := Build(s, t0, Config{})

	byRun := map[string]RunRow{}
	for _, r := range v.Repos[0].Runs {
		byRun[r.Run] = r
	}
	if !byRun["r1"].Waiting || byRun["r1"].WaitingUntil != ms(resume) {
		t.Fatalf("r1 = %+v, want it waiting until %s", byRun["r1"], resume)
	}
	if byRun["r2"].Waiting {
		t.Fatalf("r2 = %+v, want it not waiting: its instant has passed", byRun["r2"])
	}
	if byRun["r3"].Waiting || byRun["r3"].WaitingUntil != 0 {
		t.Fatalf("r3 = %+v, want no wait at all", byRun["r3"])
	}

	// The text views say what it is waiting for, and until when.
	const want = "waiting for a credential, expected resume 2026-10-03 12:15Z"
	if got := flags(byRun["r1"], "!"); !strings.Contains(got, want) {
		t.Fatalf("flags = %q, want %q in it", got, want)
	}
	if got := flags(byRun["r2"], "!"); strings.Contains(got, "waiting") {
		t.Fatalf("flags = %q, want no waiting flag for a run whose instant has passed", got)
	}

	// --json carries the instant in the document's own convention for one
	// (RFC 3339, UTC), and only while the run is still waiting: presence is
	// the state, so a reader that sees the field knows the run is waiting.
	doc := BuildJSON("aurora", v)
	until := map[string]string{}
	for _, r := range doc.Runs {
		until[r.Run] = r.WaitingUntil
	}
	if until["r1"] != "2026-10-03T12:15:00Z" {
		t.Fatalf("r1 waiting_until = %q, want 2026-10-03T12:15:00Z", until["r1"])
	}
	if until["r2"] != "" || until["r3"] != "" {
		t.Fatalf("waiting_until = %q %q, want both absent", until["r2"], until["r3"])
	}

	// The screen: FLAGS says it, and the detail has room for the instant even
	// at a width whose FLAGS column cannot hold the whole sentence.
	sel := Cursor{Slug: "acme%2Fapp", Run: "r1"}
	f := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if !strings.Contains(f, "waiting for a credential") {
		t.Fatalf("the waiting run's FLAGS does not say what it waits for:\n%s", f)
	}
	if !strings.Contains(f, "    waiting    expected resume 2026-10-03 12:15Z") {
		t.Fatalf("the waiting run's detail does not say when it resumes:\n%s", f)
	}
	// A held-up run is amber, not healthy-looking and not red: the cascade
	// resumes it by itself (D152).
	if flagColour(byRun["r1"]) != cWarn {
		t.Fatalf("flagColour = %q, want the warning colour", flagColour(byRun["r1"]))
	}

	// At a narrow width the FLAGS column cannot hold the sentence, and its
	// instant is the part a clip would eat: the row says only that the run is
	// waiting, and the detail carries the rest.
	narrow := frame(v, 80, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	row := ""
	for _, l := range strings.Split(narrow, "\n") {
		if strings.HasPrefix(l, "▸ r1 ") {
			row = l
		}
	}
	if row == "" || !strings.HasSuffix(row, "waiting") || strings.Contains(row, "expected resume") {
		t.Fatalf("the narrow row does not shorten the waiting flag:\n%s", narrow)
	}
	if !strings.Contains(narrow, "    waiting    expected resume 2026-10-03 12:15Z") {
		t.Fatalf("the narrow frame's detail lost the instant:\n%s", narrow)
	}
}
