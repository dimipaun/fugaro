package runstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestDiscoveredBounded: what a run reports about the graph is its own agent's
// words, written by the run the graph is meant to constrain, so the record
// carries the least that can draw an edge — at most MaxDiscoveredItems entries
// per list, each text at MaxDiscoveredText runes, and at most MaxConflictPaths
// paths — and an entry no reader can place is dropped rather than carried.
func TestDiscoveredBounded(t *testing.T) {
	var blocks []DiscoveredItem
	for i := range 100 {
		blocks = append(blocks, DiscoveredItem{PlanTask: fmt.Sprintf("X%d", i+1), Reason: "waits for the store this run rewrites"})
	}
	d := NewDiscovered(blocks, nil)
	if d == nil {
		t.Fatal("a run that blocked a hundred items discovered nothing")
	}
	if len(d.Blocks) != MaxDiscoveredItems {
		t.Fatalf("stored %d blocks, want the bound of %d", len(d.Blocks), MaxDiscoveredItems)
	}
	// The entries kept are the first the run named, in order: the cap falls on
	// the same entries however many times the list is bounded.
	if d.Blocks[0].PlanTask != "X1" || d.Blocks[MaxDiscoveredItems-1].PlanTask != "X16" {
		t.Fatalf("kept %q … %q, want the first %d in order", d.Blocks[0].PlanTask, d.Blocks[len(d.Blocks)-1].PlanTask, MaxDiscoveredItems)
	}
	// The other list is empty and not null: an empty array is a fact ("this
	// run needed nothing") and a null is not.
	if d.Needed == nil || len(d.Needed) != 0 {
		t.Fatalf("a list with nothing in it came out as %#v", d.Needed)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"needed":[]`) {
		t.Fatalf("an empty list did not marshal as an empty array: %s", b)
	}

	// Both texts are cut at the rune, not at the byte: a path or a reason of
	// multi-byte runes has to come back whole, since a cut splits one in two
	// and a reader of an agent's file is handed whatever it wrote.
	long := strings.Repeat("é", 2*MaxDiscoveredText)
	d = NewDiscovered(nil, []DiscoveredItem{{Path: long, Reason: long}})
	if len(d.Needed) != 1 {
		t.Fatalf("a needed entry with a long path and reason was dropped: %#v", d.Needed)
	}
	for _, got := range []string{d.Needed[0].Path, d.Needed[0].Reason} {
		if n := utf8.RuneCountInString(got); n != MaxDiscoveredText {
			t.Fatalf("clipped to %d runes (%d bytes), want %d", n, len(got), MaxDiscoveredText)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("the clip split a rune: %q", got)
		}
	}

	// An entry that names no item, names two, or carries no why is one no
	// reader can place or account for, so it is not in the record — and the
	// entries beside it are.
	d = NewDiscovered([]DiscoveredItem{
		{Reason: "names neither a plan task nor a path"},
		{PlanTask: "X7", Path: "internal/runstore/runstore.go", Reason: "names both"},
		{PlanTask: "X8"},
		{Reason: "", Path: "internal/runstore/discovered.go"},
		{PlanTask: "X9", Reason: "this one a reader can place"},
	}, nil)
	if d == nil || len(d.Blocks) != 1 || d.Blocks[0].PlanTask != "X9" {
		t.Fatalf("unplaceable entries were kept: %#v", d)
	}

	// Nothing a reader can place is nothing to record: the block is nil, so a
	// run that discovered nothing writes the record it always wrote.
	if got := NewDiscovered([]DiscoveredItem{{Reason: "names nothing"}}, nil); got != nil {
		t.Fatalf("a block of unplaceable entries came out as %#v, want nil", got)
	}
	if got := NewDiscovered(nil, nil); got != nil {
		t.Fatalf("a run that discovered nothing came out as %#v, want nil", got)
	}

	// The record is an object the job's service account can write, so what is
	// read back is bounded again rather than trusted.
	var lying []DiscoveredItem
	for range 100 {
		lying = append(lying, DiscoveredItem{Path: long, Reason: long})
	}
	back := (&Discovered{Blocks: append([]DiscoveredItem{{Reason: "names nothing"}}, lying...), Needed: lying}).Bounded()
	if back == nil || len(back.Blocks) != MaxDiscoveredItems || len(back.Needed) != MaxDiscoveredItems {
		t.Fatalf("an oversized block read back as %#v", back)
	}
	if n := utf8.RuneCountInString(back.Blocks[0].Reason); n != MaxDiscoveredText {
		t.Fatalf("an over-long reason read back at %d runes, want %d", n, MaxDiscoveredText)
	}
	if (*Discovered)(nil).Bounded() != nil {
		t.Fatal("a record with no block must read as no block")
	}
	if empty := (&Discovered{}).Bounded(); empty != nil {
		t.Fatalf("a block with nothing placeable read back as %#v, want nil", empty)
	}

	// And the same for the conflicting paths: at most MaxConflictPaths of
	// them, in the order the run met them, each clipped, empty paths dropped.
	var paths []string
	for i := range 200 {
		paths = append(paths, fmt.Sprintf("internal/pkg%03d/file.go", i))
	}
	kept := NewConflictPaths(append([]string{"", ""}, paths...))
	if len(kept) != MaxConflictPaths {
		t.Fatalf("stored %d conflict paths, want the bound of %d", len(kept), MaxConflictPaths)
	}
	if kept[0] != paths[0] || kept[MaxConflictPaths-1] != paths[MaxConflictPaths-1] {
		t.Fatalf("kept %q … %q, want the first %d in order", kept[0], kept[len(kept)-1], MaxConflictPaths)
	}
	if got := NewConflictPaths([]string{"", long}); len(got) != 1 || utf8.RuneCountInString(got[0]) != MaxDiscoveredText {
		t.Fatalf("an over-long path was not clipped: %#v", got)
	}
	if got := NewConflictPaths([]string{"", ""}); got != nil {
		t.Fatalf("a list of empty paths came out as %#v, want nil", got)
	}
	if got := BoundedConflictPaths(paths); len(got) != MaxConflictPaths {
		t.Fatalf("a lying record's %d paths read back as %d, want %d", len(paths), len(got), MaxConflictPaths)
	}
}

// TestRecordWithoutDiscoveredIsUnchanged: a record written before these fields
// existed decodes and re-encodes byte for byte, and a run that discovered
// nothing writes neither key — the fields are additive, so a reader from
// before them sees the record it always saw.
func TestRecordWithoutDiscoveredIsUnchanged(t *testing.T) {
	// The record a writer that never heard of `discovered` produced, in the
	// order the Go struct tags put the fields, so the round trip below is
	// byte-for-byte: a field inserted ahead of any of these would show.
	const before = `{"version":1,"run_id":"20261116-120000-abcd","repo":"acme/app","status":"succeeded","stage":"writeback","outcome":"ready","cost_usd":1,"started_at":"2026-11-16T12:00:00Z"}`
	var rec Record
	if err := json.Unmarshal([]byte(before), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Discovered != nil || rec.ConflictPaths != nil {
		t.Fatalf("an old record read as discovered %#v, conflict paths %#v", rec.Discovered, rec.ConflictPaths)
	}
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != before {
		t.Fatalf("re-encoding an old record changed it:\n got %s\nwant %s", out, before)
	}
	// The constructors' nil answer for nothing found is what keeps the keys
	// out: a run that discovered nothing and met no conflict writes the record
	// it always wrote.
	rec.Discovered = NewDiscovered(nil, nil)
	rec.ConflictPaths = NewConflictPaths(nil)
	out, err = json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != before {
		t.Fatalf("a run that found nothing wrote:\n got %s\nwant %s", out, before)
	}
	for _, key := range []string{"discovered", "conflict_paths"} {
		if strings.Contains(string(out), key) {
			t.Fatalf("a run that found nothing still writes %q: %s", key, out)
		}
	}

	// What a run that did find something out writes is what a reader gets
	// back, bounded and whole.
	rec.Discovered = NewDiscovered(
		[]DiscoveredItem{{PlanTask: "X2", Reason: "X2 adds the record's own fields"}},
		[]DiscoveredItem{{Path: "internal/runstore/runstore.go", Reason: "the fold needs the record"}},
	)
	rec.ConflictPaths = NewConflictPaths([]string{"internal/runstore/runstore.go"})
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var round Record
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	if round.Discovered == nil || len(round.Discovered.Blocks) != 1 || round.Discovered.Blocks[0].PlanTask != "X2" ||
		len(round.Discovered.Needed) != 1 || round.Discovered.Needed[0].Path != "internal/runstore/runstore.go" {
		t.Fatalf("the block round-tripped as %#v", round.Discovered)
	}
	if len(round.ConflictPaths) != 1 || round.ConflictPaths[0] != "internal/runstore/runstore.go" {
		t.Fatalf("the conflict paths round-tripped as %#v", round.ConflictPaths)
	}
	if got := round.Discovered.Bounded(); len(got.Blocks) != 1 || len(got.Needed) != 1 {
		t.Fatalf("a whole block bounded to %#v", got)
	}
}
