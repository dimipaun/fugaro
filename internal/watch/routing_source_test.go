package watch

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWatchPrintsADashForAuth is CS9's view of D3070 (model-credentials-
// routing): a run the project's routing table serves writes nothing in its
// registry entry's `auth`, and every view prints the dash for it, while the
// entry's new `source` says what the run's running stage goes out on.
//
// The dash is not a new spelling: `auth` keeps its member and its meaning, and
// the dash is the same "nothing to say" every other absent value of the row
// already prints — which is why a release-behind console drawing the row as it
// always did draws no auth mode rather than an empty one. What replaces it is
// `source`, the kind of the source that served the stage and its name after a
// colon when the kind names one, and `fallback` beside it when the stage was
// served by something other than the first of its chain.
//
// Mutation (leaving the member off RunRow or off JSONRun): the row and the
// document show the dash for every run, including the two the table serves.
func TestWatchPrintsADashForAuth(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{
	 "r1":`+agent("acme/app", "a run the table serves", `,"source":"pool:team"`)+`,
	 "r2":`+agent("acme/app", "a run that fell back", `,"source":"anthropic","fallback":true`)+`,
	 "r3":`+agent("acme/app", "a run the table does not serve", `,"auth":"api-key"`)+`,
	 "r4":`+agent("acme/app", "a source that tries an escape", `,"source":"pool:\u001b[31mteam"`)+`}}`, t0)
	v := Build(s, t0, Config{})

	rows := map[string]RunRow{}
	for _, r := range v.Repos[0].Runs {
		rows[r.Run] = r
	}
	if len(rows) != 4 {
		t.Fatalf("runs = %+v, want the four of acme/app", v.Repos[0].Runs)
	}
	// A run the table serves writes no auth, and its source says what it goes
	// out on. A run that fell back carries the same member with the flag.
	if rows["r1"].Auth != "-" || rows["r1"].Source != "pool:team" || rows["r1"].Fallback {
		t.Fatalf("r1 = %+v, want no auth, pool:team and no fallback", rows["r1"])
	}
	if rows["r2"].Auth != "-" || rows["r2"].Source != "anthropic" || !rows["r2"].Fallback {
		t.Fatalf("r2 = %+v, want no auth, anthropic and the fallback flag", rows["r2"])
	}
	// A run the table does not serve keeps its auth and shows the dash for the
	// source: absent is "not recorded", never "no source".
	if rows["r3"].Auth != "api-key" || rows["r3"].Source != "-" || rows["r3"].Fallback {
		t.Fatalf("r3 = %+v, want api-key and the dash for a run with no source", rows["r3"])
	}
	// The member is text a run wrote: the escape sequence is stripped like
	// every other database-derived string of the row.
	if rows["r4"].Source != "pool:team" {
		t.Fatalf("r4 source = %q, want the escape sequence dropped", rows["r4"].Source)
	}

	// --json carries the same two: `auth` as the dash, and `source` always
	// present — a row the table does not serve carries "-", as stage, verify and
	// mode do, so a reader never has to decide the absence of the member. The
	// entry's `fallback` flag is the expanded detail's, not this document's: the
	// contract fixes the source here and no other member (contract-table.md).
	doc := BuildJSON("aurora", v)
	got := map[string]JSONRun{}
	for _, r := range doc.Runs {
		got[r.Run] = r
	}
	if got["r1"].Auth != "-" || got["r1"].Source != "pool:team" {
		t.Fatalf("r1 in --json = %+v, want no auth and pool:team", got["r1"])
	}
	if got["r2"].Source != "anthropic" || got["r2"].Auth != "-" {
		t.Fatalf("r2 in --json = %+v, want no auth and anthropic", got["r2"])
	}
	if got["r3"].Source != "-" || got["r3"].Auth != "api-key" {
		t.Fatalf("r3 in --json = %+v, want api-key and the dash", got["r3"])
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range doc.Runs {
		if r.Source == "" {
			t.Fatalf("run %s carries an empty source: every row writes a value for the key\n%s", r.Run, data)
		}
	}
}

// TestDetailCarriesTheSource: the interactive view says what the selected run's
// stage goes out on, on the same line as the flag that qualifies it, because
// the table's own columns have no room for either. The dash is the same
// "nothing recorded" the credential line shows.
func TestDetailCarriesTheSource(t *testing.T) {
	v := fixture()
	v.Repos[0].Runs[0].Source, v.Repos[0].Runs[0].Fallback = "pool:team", true
	v.Repos[0].Runs[1].Source = "anthropic"
	sel := Cursor{Slug: v.Repos[0].Slug, Run: v.Repos[0].Runs[0].Run}
	got := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	for _, want := range []string{"source", "pool:team FALLBACK"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the detail must carry %q:\n%s", want, got)
		}
	}
}
