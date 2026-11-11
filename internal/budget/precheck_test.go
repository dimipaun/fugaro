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

// recordingReader is a mapReader that remembers every path it was asked for:
// what a read-only figure reads is part of what it is (D573 reads the caps and
// the day's counters, and nothing else).
type recordingReader struct {
	mapReader
	paths []string
}

func (r *recordingReader) Get(ctx context.Context, path string, out any) (bool, error) {
	r.paths = append(r.paths, path)
	return r.mapReader.Get(ctx, path, out)
}

func headroom(t *testing.T, nodes map[string]any, capped bool) (DailyHeadroom, *recordingReader) {
	t.Helper()
	r := &recordingReader{mapReader: mapReader{nodes: nodes}}
	h, err := ReadDailyHeadroom(context.Background(), r, PrecheckInput{Project: "aurora", Slug: "gh-acme-app", Capped: capped, Now: preNow})
	if err != nil {
		t.Fatal(err)
	}
	return h, r
}

// TestDailyHeadroomReadsWithoutLeasing (D573): the figure a sampling margin is
// measured against is the day's remaining headroom, and reading it leases
// nothing — the read takes a Reader, so it cannot write, and it asks for the
// caps and the day's two counters alone: no kill switch, no run ledger, and no
// mode, which is why observe and enforce answer the same figure.
func TestDailyHeadroomReadsWithoutLeasing(t *testing.T) {
	n := preNodes()
	n[PathCapsRepo("gh-acme-app")] = map[string]any{"dailyMicros": 50_000_000, "perRunMicros": 5_000_000}
	n[PathSpendRepo(Day(preNow), "gh-acme-app")] = map[string]any{"counted": 20_000_000, "spent": 19_000_000}
	n[PathSpendGlobal(Day(preNow))] = map[string]any{"counted": 30_000_000, "spent": 25_000_000}
	h, r := headroom(t, n, true)
	// The repository's day is the tighter of the two: 50M - 19M against the
	// project's 100M - 25M. The per-run cap is the smallest in force, the
	// repository's 5M. Five of those is 25M, so this day still samples.
	if !h.Capped || h.Remaining != 31_000_000 || h.PerRun != 5_000_000 {
		t.Fatalf("headroom = %+v, want 31M left at a 5M per-run cap", h)
	}
	want := []string{PathProject, PathCapsGlobal, PathCapsDefaults, PathCapsRepo("gh-acme-app"), PathSpendGlobal(Day(preNow)), PathSpendRepo(Day(preNow), "gh-acme-app")}
	if strings.Join(r.paths, ",") != strings.Join(want, ",") {
		t.Errorf("read %v, want exactly %v", r.paths, want)
	}
	// The mode is not one of them: dropping it changes nothing.
	noMode := map[string]any{}
	for k, v := range n {
		noMode[k] = v
	}
	delete(noMode, PathMode)
	if again, _ := headroom(t, noMode, true); again != h {
		t.Errorf("headroom without a mode = %+v, want %+v", again, h)
	}
	// A day whose counters are unread is a day with its whole cap left: the
	// repository's own daily cap is the smaller of the two here (the defaults
	// give it 50M against the project's 100M), so that is the day's bound.
	if fresh, _ := headroom(t, preNodes(), true); !fresh.Capped || fresh.Remaining != 50_000_000 || fresh.PerRun != 5_000_000 {
		t.Errorf("headroom on an untouched day = %+v, want the whole 50M", fresh)
	}
}

// TestDailyHeadroomHasNoFigureToGive: the shapes with nothing to measure a
// margin against answer Capped false rather than a zero that reads as "no
// room": an uncapped run, and a project with no daily cap at all.
func TestDailyHeadroomHasNoFigureToGive(t *testing.T) {
	if h, _ := headroom(t, preNodes(), false); h.Capped {
		t.Errorf("an oauth run has no dollars to be nearly out of: %+v", h)
	}
	n := preNodes()
	delete(n, PathCapsGlobal)
	delete(n, PathCapsDefaults)
	if h, _ := headroom(t, n, true); h.Capped {
		t.Errorf("no daily cap is no bound: %+v", h)
	}
}

// TestDailyHeadroomFailsClosed: a foreign project and an unreadable node are
// errors, never a figure (fail closed).
func TestDailyHeadroomFailsClosed(t *testing.T) {
	boom := errors.New("down")
	r := &recordingReader{mapReader: mapReader{err: boom}}
	if _, err := ReadDailyHeadroom(context.Background(), r, PrecheckInput{Project: "aurora", Slug: "gh-acme-app", Capped: true, Now: preNow}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	n := preNodes()
	n[PathProject] = "other"
	if _, err := ReadDailyHeadroom(context.Background(), mapReader{nodes: n}, PrecheckInput{Project: "aurora", Slug: "gh-acme-app", Capped: true, Now: preNow}); err == nil {
		t.Fatal("a database belonging to another project must be an error")
	}
	n = preNodes()
	n[PathSpendRepo(Day(preNow), "gh-acme-app")] = "not an object"
	if _, err := ReadDailyHeadroom(context.Background(), mapReader{nodes: n}, PrecheckInput{Project: "aurora", Slug: "gh-acme-app", Capped: true, Now: preNow}); err == nil {
		t.Fatal("a malformed counter must be an error, never a pass")
	}
}
