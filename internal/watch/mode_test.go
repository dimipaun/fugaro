package watch

import (
	"testing"
	"time"
)

// A live row carries the rigor mode the run resolved and the layer that won it
// (rigor-modes plan T1.7; budget.AgentEntry.Mode and .From, which the run fills
// from its own record, so the row and the record's Mode/From are one value).
// A row whose entry has neither -- one written under rules that predate the
// child, which the run then goes without -- shows the same "-" every other
// absent value shows, in the row and in --json.
func TestWatchShowsALiveRunsMode(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{
	 "r1":`+agent("acme/app", "resolved at bootstrap", `,"mode":"critical","from":"area"`)+`,
	 "r2":`+agent("acme/app", "an entry from older rules", "")+`,
	 "r3":`+agent("acme/app", "a mode that tries an escape", `,"mode":"pro\u001b[31m","from":"repo"`)+`}}`, t0)
	v := Build(s, t0, Config{})

	byRun := map[string]RunRow{}
	for _, r := range v.Repos[0].Runs {
		byRun[r.Run] = r
	}
	if len(byRun) != 3 {
		t.Fatalf("runs = %+v, want the three of acme/app", v.Repos[0].Runs)
	}
	if byRun["r1"].Mode != "critical" || byRun["r1"].From != "area" {
		t.Fatalf("r1 = %+v, want critical from area", byRun["r1"])
	}
	if byRun["r3"].Mode != "pro" || byRun["r3"].From != "repo" {
		t.Fatalf("r3 = %+v, want the escape sequence dropped", byRun["r3"])
	}
	if byRun["r2"].Mode != "-" || byRun["r2"].From != "-" {
		t.Fatalf("r2 = %+v, want the - every other absent value shows", byRun["r2"])
	}

	// --json carries the same two values: the console draws the mode from them.
	doc := BuildJSON("aurora", v)
	got := map[string]JSONRun{}
	for _, r := range doc.Runs {
		got[r.Run] = r
	}
	if got["r1"].Mode != "critical" || got["r1"].From != "area" {
		t.Fatalf("r1 = %+v, want critical from area in --json too", got["r1"])
	}
	if got["r2"].Mode != "-" || got["r2"].From != "-" {
		t.Fatalf("r2 = %+v, want the - every other absent value shows", got["r2"])
	}
}

// A queued run has no registry entry at all -- nothing has written it yet -- so
// its mode is absent the way its stage and verify are: "-", in the row and in
// --json. A queued row left at the zero value would write "mode":"" instead, a
// value no mode or layer word is, and in the console's frame that is a value the
// scrub refuses, so the recorded-fixture workflow would fail on any recording
// that happened to catch a run still waiting.
func TestWatchShowsAQueuedRunsModeAsAbsent(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{"r1":`+agent("acme/app", "a live run", `,"mode":"critical","from":"area"`)+`}}`, t0)
	v := MergeQueued(Build(s, t0, Config{}), Config{},
		[]QueuedRun{{Run: "r-queued1", Slug: "acme__app", Workflow: "web", LaunchedAt: t0.Add(-2 * time.Minute)}}, "", t0)

	var row RunRow
	found := false
	for _, b := range v.Repos {
		for _, r := range b.Runs {
			if r.Run == "r-queued1" {
				row, found = r, true
			}
		}
	}
	if !found || !row.Queued {
		t.Fatalf("queued row missing from %+v", v.Repos)
	}
	if row.Mode != "-" || row.From != "-" {
		t.Fatalf("queued row = Mode:%q From:%q, want the - every other absent value shows", row.Mode, row.From)
	}

	jr, found := JSONRun{}, false
	for _, r := range BuildJSON("aurora", v).Runs {
		if r.Run == "r-queued1" {
			jr, found = r, true
		}
	}
	if !found {
		t.Fatal("the queued run is not in the --json runs[]")
	}
	if jr.Mode != "-" || jr.From != "-" {
		t.Fatalf("queued run in --json = Mode:%q From:%q, want the - every other absent value shows", jr.Mode, jr.From)
	}

	// The contract is about the whole frame, not this one row: every run in
	// runs[] carries a value for both keys, so no row kind can be added that
	// leaves one at the zero value and hands a reader an empty string.
	for _, r := range BuildJSON("aurora", v).Runs {
		if r.Mode == "" || r.From == "" {
			t.Fatalf("run %s = Mode:%q From:%q, want a value for both keys", r.Run, r.Mode, r.From)
		}
	}
}
