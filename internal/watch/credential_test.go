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
	 "r3":`+agent("acme/app", "nothing pending", "")+`,
	 "r4":`+agent("acme/app", "waiting and halted", fmt.Sprintf(`,"waiting_until":%d,"halted":"budget"`, ms(resume)))+`}}`, t0)
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

	// The screen: FLAGS says the run is waiting — the whole sentence is the
	// plain table's, which has no columns to fit — and the detail has room for
	// the instant at every width.
	sel := Cursor{Slug: "acme%2Fapp", Run: "r1"}
	f := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if got := rowStartingWith(f, "▸ r1 "); !strings.Contains(got, "waiting") || strings.Contains(got, "expected resume") {
		t.Fatalf("the waiting run's FLAGS does not say it is waiting, and only that:\n%s", got)
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
	row := rowStartingWith(narrow, "▸ r1 ")
	if row == "" || !strings.HasSuffix(row, "waiting") || strings.Contains(row, "expected resume") {
		t.Fatalf("the narrow row does not shorten the waiting flag:\n%s", narrow)
	}
	if !strings.Contains(narrow, "    waiting    expected resume 2026-10-03 12:15Z") {
		t.Fatalf("the narrow frame's detail lost the instant:\n%s", narrow)
	}

	// Tier 3 is the widest table there is and its FLAGS column is still 30
	// columns, against a 59-column sentence: the row shortens it there too,
	// because the alternative is not the sentence but its first half with an
	// ellipsis, and any flag behind it — r4's halt — gone with the instant the
	// clip ate. The detail has the room, at every tier.
	wide := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = Cursor{Slug: "acme%2Fapp", Run: "r4"}, map[Cursor]bool{{Slug: "acme%2Fapp", Run: "r4"}: true}
	})
	r4 := rowStartingWith(wide, "▸ r4 ")
	if r4 == "" {
		t.Fatalf("tier 3 has no row for r4:\n%s", wide)
	}
	if !strings.Contains(r4, "waiting, halted: budget") {
		t.Fatalf("the tier-3 row does not shorten the waiting flag, or drops what follows it:\n%s", r4)
	}
	if strings.Contains(r4, "expected resume") {
		t.Fatalf("the tier-3 row carries the sentence its column cannot hold:\n%s", r4)
	}
	if !strings.Contains(wide, "    waiting    expected resume 2026-10-03 12:15Z") {
		t.Fatalf("the tier-3 frame's detail lost the instant:\n%s", wide)
	}
	if got := rowStartingWith(wide, "▸ r1 "); strings.Contains(got, "expected resume") {
		t.Fatalf("the tier-3 row of a waiting run carries the sentence:\n%s", got)
	}
}

// The waiting sentence's width is a figure three places outside this file
// state, and it has to be the width this test measures: rowFlags' comment in
// render.go, the tier-3 comment above, and console/docs/json-contract.md's
// `waiting_until` row — which is the published console contract, so a reader
// outside this repository decides a layout from it.
//
// The review found all three saying 55 for a sentence that is 59, and the
// reason nothing caught it is that the conclusion each draws is the same at
// either figure: 59 is past every FLAGS column this table has (30 at its
// widest), so the row shortens the sentence and the detail carries it, which is
// what the tests around this one pin. A figure that nothing measures drifts, so
// this measures it — the prefix and the stamp are the two halves, and if
// stampText's layout ever changes, the three places that state the sum are what
// have to change with it.
func TestTheWaitingSentenceIsAsWideAsTheThreePlacesSayIt(t *testing.T) {
	const stated = 59
	resume := t0.Add(15 * time.Minute)
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{
	 "r1":`+agent("acme/app", "waiting for a turn", fmt.Sprintf(`,"waiting_until":%d`, ms(resume)))+`}}`, t0)
	v := Build(s, t0, Config{})

	// The flag itself, built the way the plain table builds it — not a copy of
	// the sentence, which could drift from the one the three comments are about.
	got := flags(v.Repos[0].Runs[0], "!")
	if len(got) != stated {
		t.Fatalf("the waiting flag is %d columns (%q); rowFlags' comment, the tier-3 comment and console/docs/json-contract.md say %d",
			len(got), got, stated)
	}
}

// rowStartingWith is the frame's line that begins with prefix — one run's row,
// at a width whose columns pad it — or "" when there is none.
func rowStartingWith(f, prefix string) string {
	for _, l := range strings.Split(f, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}
