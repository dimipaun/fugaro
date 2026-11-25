package routing

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestTableShapesAreStable pins the JSON of every shape this package declares,
// because they are what a project publishes and what the run's record, the
// policy and the console read back: the table's own document, the answer's
// SourceName, and the closed set of kinds' spellings.
//
// It pins the JSON and not the Go types on purpose. A member renamed in Go is a
// compile error at every call site, which is a loud failure; a member renamed
// in JSON is silent — a project's published table stops decoding, a record's
// member stops being read, and the console draws a blank where a figure was.
// The Go names are free to move; these strings are the contract.
func TestTableShapesAreStable(t *testing.T) {
	table := Table{
		{
			Match: "claude-sonnet-5-5",
			Chain: []Source{
				{Kind: KindLauncherPool},
				{Kind: KindPool, Pool: "team"},
				{Kind: KindProvider, Provider: "openrouter", Model: "claude-haiku-4-5", Admit: []string{"fugaro", "website"}},
				{Kind: KindAnthropic, Admit: []string{"fugaro"}},
				{Kind: KindVertex},
			},
		},
		{Match: "claude-*", Chain: []Source{{Kind: KindAnthropic, Admit: []string{"fugaro"}}}},
	}

	// What the shapes write. Every member of every shape appears once, and the
	// members that may be absent are absent: a source names one of a pool and a
	// provider, a source that serves a model other than the one the call asked
	// for names it in model (the written downgrade — never a spelling, which is
	// the checked-in map's alone), and an admit list no source carries is not
	// written at all rather than written empty.
	const written = `[{"match":"claude-sonnet-5-5","chain":[` +
		`{"kind":"launcher-pool"},` +
		`{"kind":"pool","pool":"team"},` +
		`{"kind":"provider","provider":"openrouter","model":"claude-haiku-4-5","admit":["fugaro","website"]},` +
		`{"kind":"anthropic","admit":["fugaro"]},` +
		`{"kind":"vertex"}]},` +
		`{"match":"claude-*","chain":[{"kind":"anthropic","admit":["fugaro"]}]}]`

	got, err := json.Marshal(table)
	if err != nil {
		t.Fatalf("marshalling a table: %v", err)
	}
	if string(got) != written {
		t.Errorf("a table writes\n%s\nwant\n%s", got, written)
	}

	// What the shapes read: the same document as a person would write it in a
	// project's configuration, and the same value back.
	const published = `[
  {
    "match": "claude-sonnet-5-5",
    "chain": [
      {"kind": "launcher-pool"},
      {"kind": "pool", "pool": "team"},
      {"kind": "provider", "provider": "openrouter", "model": "claude-haiku-4-5", "admit": ["fugaro", "website"]},
      {"kind": "anthropic", "admit": ["fugaro"]},
      {"kind": "vertex"}
    ]
  },
  {"match": "claude-*", "chain": [{"kind": "anthropic", "admit": ["fugaro"]}]}
]`
	var read Table
	if err := json.Unmarshal([]byte(published), &read); err != nil {
		t.Fatalf("reading a published table: %v", err)
	}
	if !reflect.DeepEqual(read, table) {
		t.Errorf("a published table reads as\n%+v\nwant\n%+v", read, table)
	}

	// The answer's shape. Name is written even when it is empty: the two kinds
	// that are a single credential of the project have nothing to distinguish,
	// and a reader must not have to decide what an absent name means.
	for _, tc := range []struct {
		name SourceName
		want string
	}{
		{SourceName{Kind: KindAnthropic}, `{"kind":"anthropic","name":""}`},
		{SourceName{Kind: KindVertex}, `{"kind":"vertex","name":""}`},
		{SourceName{Kind: KindPool, Name: "team"}, `{"kind":"pool","name":"team"}`},
		{SourceName{Kind: KindProvider, Name: "openrouter"}, `{"kind":"provider","name":"openrouter"}`},
		{SourceName{Kind: KindLauncherPool, Name: "team"}, `{"kind":"launcher-pool","name":"team"}`},
	} {
		got, err := json.Marshal(tc.name)
		if err != nil {
			t.Fatalf("marshalling %+v: %v", tc.name, err)
		}
		if string(got) != tc.want {
			t.Errorf("the source %+v writes %s, want %s", tc.name, got, tc.want)
		}
	}

	// The kinds' spellings, in the order spec.md lists them. A spelling is what
	// a project writes and what the record, the policy and the console read, so
	// a renamed kind is a table nobody can publish.
	kinds, err := json.Marshal(Kinds)
	if err != nil {
		t.Fatalf("marshalling the kinds: %v", err)
	}
	const wantKinds = `["launcher-pool","pool","anthropic","provider","vertex"]`
	if string(kinds) != wantKinds {
		t.Errorf("the kinds write %s, want %s", kinds, wantKinds)
	}
	for _, k := range Kinds {
		if !ValidKind(k) {
			t.Errorf("ValidKind(%q) is false for a kind of the closed set", k)
		}
	}
	for _, k := range []Kind{"", "launcher_pool", "key", "gemini", "Anthropic", "vertex "} {
		if ValidKind(k) {
			t.Errorf("ValidKind(%q) is true, and %q is no kind this build serves", k, k)
		}
	}
}
