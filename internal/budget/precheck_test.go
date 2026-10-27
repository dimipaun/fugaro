package budget

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// mapReader serves nodes from a map; an absent path is a null node.
type mapReader struct {
	nodes map[string]any
	err   error
}

func (m mapReader) Get(_ context.Context, path string, out any) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	v, ok := m.nodes[path]
	if !ok {
		return false, nil
	}
	b, _ := json.Marshal(v)
	return true, json.Unmarshal(b, out)
}

var preNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func preNodes() map[string]any {
	return map[string]any{
		PathProject:      "aurora",
		PathMode:         "enforce",
		PathCapsGlobal:   map[string]any{"dailyMicros": 100_000_000, "perRunMicros": 10_000_000},
		PathCapsDefaults: map[string]any{"repoDailyMicros": 50_000_000, "repoPerRunMicros": 5_000_000},
	}
}

func pre(t *testing.T, nodes map[string]any, capped bool) PrecheckResult {
	t.Helper()
	res, err := Precheck(context.Background(), mapReader{nodes: nodes}, PrecheckInput{Project: "aurora", Slug: "gh-acme-app", Capped: capped, Now: preNow})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPrecheckPasses(t *testing.T) {
	if res := pre(t, preNodes(), true); res.Refused || len(res.Notes) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestPrecheckOAuthIgnoresCapsNotKills(t *testing.T) {
	n := preNodes()
	delete(n, PathCapsDefaults)
	delete(n, PathCapsGlobal)
	if res := pre(t, n, false); res.Refused {
		t.Fatalf("an oauth run has no dollar cap to miss: %+v", res)
	}
	if res := pre(t, n, true); !res.Refused || res.Reason != ReasonNoCap {
		t.Fatalf("a capped run with no caps must be refused: %+v", res)
	}
	n[PathKillRepo("gh-acme-app")] = map[string]any{"on": true}
	if res := pre(t, n, false); !res.Refused || res.Reason != ReasonKillSwitch || res.Scope != ScopeRepo {
		t.Fatalf("kills apply to oauth: %+v", res)
	}
}

func TestPrecheckModeAbsentNotes(t *testing.T) {
	n := preNodes()
	delete(n, PathMode)
	n[PathSpendRepo(Day(preNow), "gh-acme-app")] = map[string]any{"counted": 49_900_000}
	res := pre(t, n, true)
	// The counter that leaves too little headroom for the advisory note is
	// past 80% of the same cap by construction, so its warning comes too.
	if res.Refused || len(res.Notes) != 3 {
		t.Fatalf("want a pass with the mode note, the would-refuse note and the 80%% note: %+v", res)
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "past 80% warns") {
		t.Fatalf("the 80%% warning is missing: %+v", res)
	}
}

func TestPrecheckPerRunCapBelowMinimumRefuses(t *testing.T) {
	n := preNodes()
	n[PathCapsRepo("gh-acme-app")] = map[string]any{"dailyMicros": 50_000_000, "perRunMicros": 100_000}
	if res := pre(t, n, true); !res.Refused || res.Reason != ReasonRunCap {
		t.Fatalf("%+v", res)
	}
}

func TestPrecheckFailsClosed(t *testing.T) {
	boom := errors.New("down")
	if _, err := Precheck(context.Background(), mapReader{err: boom}, PrecheckInput{Project: "aurora", Slug: "s", Capped: true, Now: preNow}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	n := preNodes()
	n[PathKillGlobal] = "not an object"
	if _, err := Precheck(context.Background(), mapReader{nodes: n}, PrecheckInput{Project: "aurora", Slug: "s", Capped: true, Now: preNow}); err == nil {
		t.Fatal("a malformed kill switch must be an error, never a pass")
	}
}
