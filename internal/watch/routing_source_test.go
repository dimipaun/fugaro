package watch

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

// TestAServedOAuthRowStaysNotional (model-credentials-routing CS6): a run the
// routing table serves writes no auth of its own, with one exception — an
// `oauth` run keeps it — and this test is why. A live row's notional flag is
// read off that member and nothing else, so a served subscription run whose
// member was written as nothing would be drawn as paying billed dollars for
// every call, with no error and no dash to explain it. The exception goes when
// the entry's source, which is what the run really goes out on, can say the
// same thing.
//
// Mutation (the runner writes nothing for a served oauth run): the row below
// loses its flag and its spent dollars read as billed.
func TestAServedOAuthRowStaysNotional(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{
	 "r1":`+agent("acme/app", "a served oauth run", `,"auth":"oauth","source":"anthropic","spent":2000000`)+`,
	 "r2":`+agent("acme/app", "a served key run", `,"source":"pool:team","spent":2000000`)+`}}`, t0)
	v := Build(s, t0, Config{})

	rows := map[string]RunRow{}
	for _, r := range v.Repos[0].Runs {
		rows[r.Run] = r
	}
	if !rows["r1"].Notional || rows["r1"].Auth != "oauth" || rows["r1"].Source != "anthropic" {
		t.Fatalf("r1 = %+v, want the oauth member kept beside the source it goes out on", rows["r1"])
	}
	if rows["r2"].Notional || rows["r2"].Auth != "-" {
		t.Fatalf("r2 = %+v, want no auth and no notional for a served run that is not oauth", rows["r2"])
	}
}

// TestWatchShowsANonEntryRunsSourceAsAbsent: the rows that are not registry
// entries — a queued run nothing has written yet, a finished one whose entry is
// gone — have no routing answer to report, and they show the dash the rest of
// the row shows, never the empty string a row left at the zero value carries.
//
// The member is written by the run into its entry, so every other row kind has
// nothing to put there, and the dash is the same "not recorded" `auth`, `mode`
// and `verify` already write on those rows. It matters in two places: --json,
// where a reader may draw the value, and the expanded detail, which prints the
// row's own string beside the credential's dash — a row kind added later that
// left it at the zero value would draw a blank line there and write
// `"source":""` to a reader's document.
//
// Mutation (a builder that leaves Source out, or a detail that prints
// run.Source raw): the queued run's line reads `source` with nothing under it
// and --json carries an empty string.
func TestWatchShowsANonEntryRunsSourceAsAbsent(t *testing.T) {
	s := newState(t)
	put(t, s, SrcAgents, "/", `{"acme%2Fapp":{"r1":`+agent("acme/app", "a live run", `,"source":"pool:team"`)+`}}`, t0)
	v := MergeQueued(Build(s, t0, Config{}), Config{},
		[]QueuedRun{{Run: "r-queued1", Slug: "acme__app", Workflow: "web", LaunchedAt: t0.Add(-2 * time.Minute)}}, "", t0)
	rows := map[string]RunRow{}
	slug := ""
	for _, b := range v.Repos {
		for _, r := range b.Runs {
			rows[r.Run] = r
			if r.Run == "r-queued1" {
				slug = b.Slug
			}
		}
	}
	if rows["r1"].Source != "pool:team" {
		t.Fatalf("the live row = %+v, want the source its entry recorded", rows["r1"])
	}
	queued, ok := rows["r-queued1"]
	if !ok {
		t.Fatalf("the queued run is not in the view: %+v", v.Repos)
	}
	if queued.Source != "-" {
		t.Fatalf("queued row = %+v, want the - every other absent value shows", queued)
	}
	if got := finishedRow(FinishedRun{Run: "r-done1", Slug: "acme__app", Status: "succeeded", FinishedAt: t0}, t0); got.Source != "-" {
		t.Fatalf("finished row source = %q, want the - every other absent value shows", got.Source)
	}
	// The contract is about the whole document, not this one row: every run in
	// runs[] carries a value for the key, so no row kind can hand a reader an
	// empty string.
	for _, r := range BuildJSON("aurora", v).Runs {
		if r.Source == "" {
			t.Fatalf("run %s carries an empty source, want the dash", r.Run)
		}
	}

	// The detail of a row with nothing recorded draws the dash, beside the
	// credential's own: the two lines belong to the same block, and a blank one
	// reads as a rendering fault rather than as "not recorded".
	sel := Cursor{Slug: slug, Run: "r-queued1"}
	got := frame(v, 120, 0, func(o *RenderOptions) {
		o.Selected, o.Expanded = sel, map[Cursor]bool{sel: true}
	})
	if !strings.Contains(got, "source     -") {
		t.Fatalf("the detail of a row with no source must draw the dash:\n%s", got)
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
