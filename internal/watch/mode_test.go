package watch

import (
	"testing"
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
