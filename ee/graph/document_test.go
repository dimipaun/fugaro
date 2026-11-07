// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDocumentTextIsSanitised: every string a node, a source or an edge
// carries is untrusted text — a title, a repo, a branch, a note, a scope name
// out of a document someone else wrote — and Sanitise makes each one fit to
// print with safetext.Field, rebuilding each node's key from its sanitised
// source and id so the key and the fields it names cannot disagree (H2).
//
// Hand mutation this fails on: dropping the safetext.Field call on any one
// field (the escape sequence or the bidi override survives), or not rebuilding
// Key from Source and ID.
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
			State:  SourceUp,
			Reason: "ok" + bidi + "ay",
		}},
		Nodes: []Node{{
			Key:     "stale:key", // Sanitise must rebuild this, not trust it
			Source:  "coordinator",
			Kind:    NodePullRequest,
			ID:      "1" + bell,
			Title:   esc + "Fix the thing",
			Repo:    "owner/repo" + bidi,
			State:   "open",
			Release: "0.113" + esc,
		}},
		Edges:    []Edge{{From: "coordinator:1" + esc, To: "coordinator:2", Kind: "depends_on"}},
		Warnings: []string{"a" + esc + "b"},
	}
	d.Sanitise()

	if got, want := d.StreamID, "s1"; got != want {
		t.Errorf("StreamID = %q, want %q", got, want)
	}
	if got, want := d.Sources[0].Source, "coordinator"; got != want {
		t.Errorf("Sources[0].Source = %q, want %q", got, want)
	}
	if got, want := d.Sources[0].Reason, "okay"; got != want {
		t.Errorf("Sources[0].Reason = %q, want %q", got, want)
	}
	n := d.Nodes[0]
	if got, want := n.Key, "coordinator:1"; got != want {
		t.Errorf("Nodes[0].Key = %q, want %q (rebuilt from Source and ID)", got, want)
	}
	if got, want := n.ID, "1"; got != want {
		t.Errorf("Nodes[0].ID = %q, want %q", got, want)
	}
	if got, want := n.Title, "Fix the thing"; got != want {
		t.Errorf("Nodes[0].Title = %q, want %q", got, want)
	}
	if got, want := n.Repo, "owner/repo"; got != want {
		t.Errorf("Nodes[0].Repo = %q, want %q", got, want)
	}
	if got, want := n.Release, "0.113"; got != want {
		t.Errorf("Nodes[0].Release = %q, want %q", got, want)
	}
	if got, want := d.Edges[0].From, "coordinator:1"; got != want {
		t.Errorf("Edges[0].From = %q, want %q", got, want)
	}
	if got, want := d.Warnings[0], "ab"; got != want {
		t.Errorf("Warnings[0] = %q, want %q", got, want)
	}

	// Nothing a terminal would obey may survive anywhere in the document.
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{esc, bidi, bell, "\u009b"} {
		if strings.Contains(string(data), bad) {
			t.Errorf("the sanitised document still carries %q: %s", bad, data)
		}
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
	d.Sanitise()

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
	d.Sanitise()
	if len(d.Nodes) != 2 {
		t.Fatalf("the document dropped a node with a repeated id: %d nodes", len(d.Nodes))
	}
	if d.Nodes[0].Key == d.Nodes[1].Key {
		t.Errorf("two sources collapsed onto one key: %q", d.Nodes[0].Key)
	}
}
