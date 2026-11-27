package creds

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// What one member has spent (PL10) is counted per member and per window, in a
// node of its own, and the three properties that make it worth a node are here:
// two members' figures never mix, a window that has ended starts a new count
// rather than accumulating for ever, and the node holds figures — counts — and
// never a value (F19).
func TestUsageIsCountedPerMember(t *testing.T) {
	db := newFakeDB()
	st := &RTDBStore{DB: db}

	// Two members, three calls: each member's figures are its own.
	for _, c := range []struct {
		name   string
		calls  int64
		tokens int64
	}{
		{"shared-1", 1, 100},
		{"shared-1", 1, 100},
		{"project-key", 1, 7},
	} {
		if err := CountUsage(ctx, st, c.name, now, c.calls, c.tokens); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Usages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("the usage read answered %d members; want the two that were counted", len(got))
	}
	if u := got["shared-1"]; u.Calls != 2 || u.Tokens != 200 {
		t.Errorf("shared-1 reads %d calls and %d tokens; want the two calls it served and their 200 tokens", u.Calls, u.Tokens)
	}
	if u := got["project-key"]; u.Calls != 1 || u.Tokens != 7 {
		t.Errorf("project-key reads %d calls and %d tokens; want its own one call and 7 tokens", u.Calls, u.Tokens)
	}
	if _, ok := got["shared-2"]; ok {
		t.Error("a member nothing was counted for has a node: the read answered a member the service never served")
	}

	// The window: a call inside it adds, and a call at or past its end starts a
	// new count rather than adding to a figure that describes a window nobody is
	// in any more.
	if err := CountUsage(ctx, st, "shared-1", now.Add(DefaultUsageWindow-time.Minute), 1, 50); err != nil {
		t.Fatal(err)
	}
	if u, err := st.Usages(ctx); err != nil {
		t.Fatal(err)
	} else if u["shared-1"].Calls != 3 || u["shared-1"].Tokens != 250 {
		t.Errorf("a call inside the window reads %d calls and %d tokens; want the third call added to the same window",
			u["shared-1"].Calls, u["shared-1"].Tokens)
	}
	if err := CountUsage(ctx, st, "shared-1", now.Add(DefaultUsageWindow), 1, 10); err != nil {
		t.Fatal(err)
	}
	u, err := st.Usages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u["shared-1"].Calls != 1 || u["shared-1"].Tokens != 10 {
		t.Errorf("a call at the window's own end reads %d calls and %d tokens; want a new window holding only that call",
			u["shared-1"].Calls, u["shared-1"].Tokens)
	}
	if want := now.Add(DefaultUsageWindow); !u["shared-1"].Window.Equal(want) {
		t.Errorf("the window reads %v; want the instant the new count began, %v", u["shared-1"].Window, want)
	}

	// The node holds the counts, the window and the provider's figure — figures
	// and never a value: no token, no key, no secret and no header value has any
	// way in (F19).
	var raw json.RawMessage
	if _, _, err := db.GetETag(ctx, PathUsage("shared-1"), &raw); err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	for k := range members {
		switch k {
		case "calls", "tokens", "window", "utilisation":
		default:
			t.Errorf("a usage node carries the member %q; the node holds figures and never a value (F19)", k)
		}
	}

	// Headroom is the room the provider's own figure leaves, and it is absent
	// where no figure was reported — never guessed (D3178, U1).
	if h := u["shared-1"].Headroom(); h != nil {
		t.Errorf("a member no provider reported a figure for has %v of headroom; want none at all", *h)
	}
	if _, err := st.UpdateUsage(ctx, "shared-1", func(prev Usage, found bool) (Usage, error) {
		if !found {
			t.Fatal("the usage node was not found by the store that wrote it")
		}
		prev.Utilisation = ptr(0.25)
		return prev, nil
	}); err != nil {
		t.Fatal(err)
	}
	u, err = st.Usages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h := u["shared-1"].Headroom(); h == nil || *h != 0.75 {
		t.Errorf("a member reported at 0.25 of its limit has %v of headroom; want 0.75", h)
	}
	if h := (Usage{Utilisation: ptr(1.5)}).Headroom(); h == nil || *h != 0 {
		t.Errorf("a member reported past its limit has %v of headroom; want none left", h)
	}

	// The figure belongs to the window it was reported in: a new window starts
	// with none, which is what lets a threshold rule come back (PL8).
	if err := CountUsage(ctx, st, "shared-1", now.Add(2*DefaultUsageWindow), 1, 1); err != nil {
		t.Fatal(err)
	}
	if u, err := st.Usages(ctx); err != nil {
		t.Fatal(err)
	} else if u["shared-1"].Utilisation != nil {
		t.Errorf("the new window carries the figure %v of the one before it; want none", *u["shared-1"].Utilisation)
	}

	// The name is escaped into the path as the marks' is, and it is one segment.
	name := "acme/shared.1"
	if got, want := PathUsage(name), pathUsage+"/"+budget.Key(name); got != want {
		t.Fatalf("PathUsage(%q) = %q; want %q", name, got, want)
	}
	if strings.Contains(strings.TrimPrefix(PathUsage(name), pathUsage+"/"), "/") {
		t.Fatalf("PathUsage(%q) = %q: the name is more than one segment", name, PathUsage(name))
	}
}
