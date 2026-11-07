// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"
)

// assertClean fails if s still carries a character a terminal would obey or a
// reader could be fooled by: a C0 or C1 control, DEL, or a format character
// such as a bidi override.
func assertClean(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r) {
			t.Errorf("%s = %q still carries %U", what, s, r)
		}
	}
}

// TestDocumentTextIsSanitised: every string a node, a source, an edge or a
// warning carries is untrusted text — a title, a repo, a release, a state, a
// kind, a note, a scope name out of a document someone else wrote — and
// Sanitise makes each one fit to print with safetext.Field, rebuilding each
// node's key from its source and id so the key and the fields it names cannot
// disagree (H2). Every field below is fed hostile text and asserted clean, so
// dropping the safetext.Field call on any one of them fails here.
//
// Hand mutation this fails on: dropping the safetext.Field call on any one
// field (its escape sequence, bidi override or bell survives into the
// assertion), or not rebuilding Key from Source and ID.
func TestDocumentTextIsSanitised(t *testing.T) {
	const (
		esc  = "\x1b[31m" // an SGR colour an attacker would rather the terminal ran
		bidi = "‮"        // a right-to-left override: text reads backwards
		bell = "\x07"
	)
	d := Document{
		Version:  DocumentVersion,
		StreamID: "s" + bell + "1",
		Sources: []SourceState{{
			Source: "coordinator" + esc,
			State:  SourceUp + bidi,
			Reason: "ok" + esc + "ay",
		}},
		Nodes: []Node{{
			Key:     "stale:key", // Sanitise must rebuild this, not trust it
			Source:  "coordinator",
			Kind:    NodePullRequest + bell,
			ID:      "1",
			Title:   esc + "Fix the thing",
			Repo:    "owner/repo" + bidi,
			State:   "open" + bell,
			Release: "0.113" + esc,
		}},
		Edges: []Edge{{
			From: "coordinator:1" + esc,
			To:   "coordinator:2" + bidi,
			Kind: "depends_on" + bell,
		}},
		Warnings: []string{"a" + esc + "b"},
	}
	if err := d.Sanitise(); err != nil {
		t.Fatalf("Sanitise: %v", err)
	}

	cases := []struct{ what, got, want string }{
		{"StreamID", d.StreamID, "s1"},
		{"Sources[0].Source", d.Sources[0].Source, "coordinator"},
		{"Sources[0].State", d.Sources[0].State, SourceUp},
		{"Sources[0].Reason", d.Sources[0].Reason, "okay"},
		{"Nodes[0].Key", d.Nodes[0].Key, "coordinator:1"},
		{"Nodes[0].Kind", d.Nodes[0].Kind, NodePullRequest},
		{"Nodes[0].ID", d.Nodes[0].ID, "1"},
		{"Nodes[0].Title", d.Nodes[0].Title, "Fix the thing"},
		{"Nodes[0].Repo", d.Nodes[0].Repo, "owner/repo"},
		{"Nodes[0].State", d.Nodes[0].State, "open"},
		{"Nodes[0].Release", d.Nodes[0].Release, "0.113"},
		{"Edges[0].From", d.Edges[0].From, "coordinator:1"},
		{"Edges[0].To", d.Edges[0].To, "coordinator:2"},
		{"Edges[0].Kind", d.Edges[0].Kind, "depends_on"},
		{"Warnings[0]", d.Warnings[0], "ab"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.what, c.got, c.want)
		}
		assertClean(t, c.what, c.got)
	}
}

// TestNodeIdentifierIsRefusedWhenSanitisingWouldMergeIt: a node's Source and ID
// are its identity, not prose, so Sanitise refuses one that safetext.Field would
// change rather than rewriting it. Field is lossy — it drops controls and format
// characters, folds whitespace and cuts at FieldMax — so two distinct raw ids
// ("42" and "42\x07", or two long ids sharing their first 511 characters) would
// otherwise share one key, and keys are unique within one source (D1356).
//
// Hand mutation this fails on: sanitising the id into the key instead of
// refusing it, which makes the pairs below pass with one key each.
func TestNodeIdentifierIsRefusedWhenSanitisingWouldMergeIt(t *testing.T) {
	for _, id := range []string{"42\x07", "42\n", "42‮", " 42", "42 ", "a\tb", strings.Repeat("a", 513)} {
		d := Document{Version: DocumentVersion, Nodes: []Node{{Source: "coordinator", ID: id, Kind: NodeRun}}}
		if err := d.Sanitise(); err == nil {
			t.Errorf("Sanitise accepted id %q, which safetext.Field would change", id)
		}
	}
	for _, source := range []string{"coordinator\x07", " coordinator", "coordinator "} {
		d := Document{Version: DocumentVersion, Nodes: []Node{{Source: source, ID: "1", Kind: NodeRun}}}
		if err := d.Sanitise(); err == nil {
			t.Errorf("Sanitise accepted source %q, which safetext.Field would change", source)
		}
	}

	// Two raw ids that differ only past Field's cut, and two that differ only in
	// a character Field drops, name two nodes; the document refuses to give them
	// one key.
	long := strings.Repeat("a", 600)
	for _, pair := range [][2]string{
		{"42", "42\x07"},
		{long + "x", long + "y"},
	} {
		d := Document{Version: DocumentVersion, Nodes: []Node{
			{Source: "coordinator", ID: pair[0], Kind: NodeRun},
			{Source: "coordinator", ID: pair[1], Kind: NodeRun},
		}}
		if err := d.Sanitise(); err == nil {
			t.Errorf("Sanitise gave %q and %q one key", pair[0], pair[1])
		}
	}

	// A clean pair keeps its two distinct keys.
	d := Document{Version: DocumentVersion, Nodes: []Node{
		{Source: "coordinator", ID: "42", Kind: NodePullRequest},
		{Source: "local:o1", ID: "42", Kind: NodeTask},
	}}
	if err := d.Sanitise(); err != nil {
		t.Fatalf("Sanitise: %v", err)
	}
	if d.Nodes[0].Key == d.Nodes[1].Key {
		t.Errorf("distinct nodes share one key: %q", d.Nodes[0].Key)
	}
}

// TestDocumentRoundTripsAndIsStrict: the document is a wire shape, so it
// marshals to one versioned JSON object and reads back equal, and a decoder
// refuses a field this build does not know rather than dropping it (CLAUDE.md,
// "Config and wire formats decode strictly").
//
// Hand mutation this fails on: dropping DisallowUnknownFields from
// ParseDocument, which makes the unknown-field case below pass silently.
func TestDocumentRoundTripsAndIsStrict(t *testing.T) {
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	updated := at.Add(-time.Minute)
	d := Document{
		Version:     DocumentVersion,
		StreamID:    "stream-1",
		Seq:         7,
		GeneratedAt: at,
		Sources:     []SourceState{{Source: "coordinator", State: SourceUp, AsOf: &updated}},
		Nodes: []Node{{
			Key: NodeKey("coordinator", "1"), Source: "coordinator", Kind: NodePullRequest,
			ID: "1", Title: "Fix the thing", Repo: "owner/repo", State: "open",
			Release: "0.113", Updated: &updated,
		}},
		Edges:    []Edge{{From: NodeKey("coordinator", "1"), To: NodeKey("coordinator", "2"), Kind: "depends_on"}},
		Warnings: []string{"a source was stale"},
	}
	if err := d.Sanitise(); err != nil {
		t.Fatalf("Sanitise: %v", err)
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseDocument(data)
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if !reflect.DeepEqual(got, d) {
		t.Errorf("round trip changed the document:\n got %+v\nwant %+v", got, d)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if v, ok := raw["version"].(float64); !ok || int(v) != DocumentVersion {
		t.Errorf("the document carries version %v, want %d", raw["version"], DocumentVersion)
	}

	raw["surprise"] = true
	withUnknown, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDocument(withUnknown); err == nil {
		t.Error("ParseDocument accepted an unknown field")
	} else if !strings.Contains(err.Error(), "surprise") {
		t.Errorf("ParseDocument refused the unknown field without naming it: %v", err)
	}

	// A document with nothing in a list still writes an array, never a null,
	// and a null read back is the empty list it means.
	empty, err := json.Marshal(Document{Version: DocumentVersion})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"sources":[]`, `"nodes":[]`, `"edges":[]`, `"warnings":[]`} {
		if !strings.Contains(string(empty), key) {
			t.Errorf("an empty document does not write %s: %s", key, empty)
		}
	}
	withNulls := []byte(`{"version":1,"stream_id":"","seq":0,"generated_at":"2026-11-07T12:00:00Z",` +
		`"sources":null,"nodes":null,"edges":null,"warnings":null}`)
	fromNulls, err := ParseDocument(withNulls)
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if fromNulls.Nodes == nil || fromNulls.Sources == nil || fromNulls.Edges == nil || fromNulls.Warnings == nil {
		t.Errorf("ParseDocument carried a null list as nil: %+v", fromNulls)
	}
}

// TestNoCrossSourceDedupe: two sources may hold the same id — a coordinator
// pull request 42 and an orchestrator task 42 are different things — so the
// document keeps both and their keys differ (D1356: dedupe only inside one
// namespace).
//
// Hand mutation this fails on: keying a node by its id alone, which collapses
// the two into one.
func TestNoCrossSourceDedupe(t *testing.T) {
	d := Document{
		Version: DocumentVersion,
		Nodes: []Node{
			{Key: NodeKey("coordinator", "42"), Source: "coordinator", Kind: NodePullRequest, ID: "42"},
			{Key: NodeKey("local:o1", "42"), Source: "local:o1", Kind: NodeTask, ID: "42"},
		},
	}
	if err := d.Sanitise(); err != nil {
		t.Fatalf("Sanitise: %v", err)
	}
	if len(d.Nodes) != 2 {
		t.Fatalf("the document dropped a node with a repeated id: %d nodes", len(d.Nodes))
	}
	if d.Nodes[0].Key == d.Nodes[1].Key {
		t.Errorf("two sources collapsed onto one key: %q", d.Nodes[0].Key)
	}
}
