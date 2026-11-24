package creds

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The record of what a credential's last answer carried (PL7, Check 880) is a
// node of its own, beside the marks, and these are the two properties that make
// that worth doing: a release that reads the marks reads exactly the Mark it
// always read — the observation is not in it — and an answer that carried
// nothing is recorded as recorded, which is what tells it apart from a
// credential no release has recorded.
func TestTheObservationIsANodeOfItsOwn(t *testing.T) {
	db := newFakeDB()
	st := &RTDBStore{DB: db}

	names := []string{"Anthropic-Ratelimit-Unified-5h-Utilization", "Retry-After"}
	if _, err := st.UpdateObservation(ctx, "shared-1", func(Observation, bool) (Observation, error) {
		return Observed(names), nil
	}); err != nil {
		t.Fatal(err)
	}

	// The node is where the operator's Check 880 reads it, and its whole
	// content is the names: no time, no state, no value.
	var raw json.RawMessage
	if _, _, err := db.GetETag(ctx, PathObservation("shared-1"), &raw); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(raw)), `{"headers":["`+names[0]+`","`+names[1]+`"]}`; got != want {
		t.Errorf("the observation node holds %s; want %s", got, want)
	}

	// The caller's slice is copied: the node must not share the backing array
	// of the walk that built it (Observed).
	names[0] = "X-Not-A-Header"
	found := false
	back, err := st.UpdateObservation(ctx, "shared-1", func(prev Observation, ok bool) (Observation, error) {
		found = ok
		return prev, nil
	})
	if err != nil || !found {
		t.Fatalf("reading the observation back: %v, found=%v", err, found)
	}
	if back.Headers[0] == "X-Not-A-Header" {
		t.Error("the observation shares its backing array with the caller's slice: a later write to that slice changed what the node holds")
	}

	// An answer that carried no rate-limit header is still an answer that was
	// recorded, so the node is written and names nothing. Check 880 reads the
	// node's absence as "nothing has been recorded since this release" and this
	// as "recorded, and the answer named none" — two different answers, which is
	// why the field is written when it is empty.
	if _, err := st.UpdateObservation(ctx, "shared-2", func(Observation, bool) (Observation, error) {
		return Observed(nil), nil
	}); err != nil {
		t.Fatal(err)
	}
	var empty json.RawMessage
	if _, found, err := db.GetETag(ctx, PathObservation("shared-2"), &empty); err != nil || !found {
		t.Fatalf("an answer that carried no rate-limit header wrote no node: %v, found=%v", err, found)
	}
	if got := strings.TrimSpace(string(empty)); got != `{"headers":[]}` {
		t.Errorf("the observation of an answer that named nothing holds %s; want {\"headers\":[]}", got)
	}

	// The marks are read as they always were. A mark this release wrote beside
	// an observation is the four members it has always been, so a reader a
	// release behind — a CLI, a console, a doctor, all of which decode strictly
	// (Marks) — reads it exactly as it reads its own (D-finding: the observation
	// must not ride in the mark).
	db.put(PathMark("shared-1"), Mark{State: StateExhausted, Since: now, Until: now.Add(time.Hour)})
	marks, err := st.Marks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 {
		t.Fatalf("the marks read answered %d credentials; want the one mark and nothing of the observation beside it", len(marks))
	}
	m := marks["shared-1"]
	if m.State != StateExhausted || !m.Until.Equal(now.Add(time.Hour)) {
		t.Errorf("the mark reads %+v; want the exhaustion it holds", m)
	}
	rawMark, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(rawMark, &members); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"headers", "observation", "names"} {
		if _, ok := members[k]; ok {
			t.Errorf("a mark carries the member %q: the observation is a node of its own, and a member here is a member every release behind refuses the node over", k)
		}
	}

	// The name is escaped into the path as the marks' is: a credential name
	// that carries a slash or a dot is one key and not a path of its own.
	name := "acme/shared.1"
	if got, want := PathObservation(name), pathObserved+"/"+budget.Key(name); got != want {
		t.Fatalf("PathObservation(%q) = %q; want %q", name, got, want)
	}
	if strings.Contains(strings.TrimPrefix(PathObservation(name), pathObserved+"/"), "/") {
		t.Fatalf("PathObservation(%q) = %q: the name is more than one segment", name, PathObservation(name))
	}
}
