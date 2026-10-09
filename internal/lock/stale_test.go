package lock

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// Canonical-looking execution names for the Takeover tests below:
// backend.SameExecution parses both sides and refuses to match anything
// that doesn't, so a bare "e1" (used freely elsewhere, where no match is
// ever expected) will never equal itself here.
const (
	execA = "projects/proj-1/locations/r1/jobs/j/executions/e-aaaaa"
	execB = "projects/proj-1/locations/r1/jobs/j/executions/e-bbbbb"
)

// TestStale covers lock.Stale's table: a live lock is only ever cleared on
// positive backend proof of its holder's own execution, never anything
// else.
func TestStale(t *testing.T) {
	cases := []struct {
		name         string
		holder       Holder
		execTerminal bool
		want         bool
	}{
		{"no execution named, backend says terminal: busy", Holder{RunID: "r1"}, true, false},
		{"execution named, backend not confirmed: busy", Holder{RunID: "r1", Execution: "e1"}, false, false},
		{"execution named, backend confirms terminal: stale", Holder{RunID: "r1", Execution: "e1"}, true, true},
		{"zero holder, backend confirms terminal: busy (no execution)", Holder{}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Stale(c.holder, c.execTerminal); got != c.want {
				t.Errorf("Stale(%+v, %v) = %v, want %v", c.holder, c.execTerminal, got, c.want)
			}
		})
	}
}

// mustJSON marshals v, failing the test on error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTakeoverDeletesAMatchingLock(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			holder := Holder{RunID: "20260927-100000-aaaa", Execution: execA, ExpiresAt: t0.Add(time.Hour)}
			if _, err := b.Create(ctx, key, mustJSON(t, holder), "application/json"); err != nil {
				t.Fatal(err)
			}
			if err := Takeover(ctx, b, key, holder); err != nil {
				t.Fatalf("Takeover = %v", err)
			}
			if ok, _ := b.Exists(ctx, key); ok {
				t.Fatal("the lock survives a matching Takeover")
			}
		})
	}
}

func TestTakeoverIsFineWhenLockAlreadyGone(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/gone")
			if err := Takeover(ctx, b, key, Holder{RunID: "x", Execution: "e1"}); err != nil {
				t.Fatalf("Takeover of an absent lock = %v", err)
			}
		})
	}
}

// TestTakeoverRefusesAChangedHolder covers both ways a lock can no longer
// be the one Stale confirmed: another run entirely (RunID differs), and
// the same run restarted under a new execution (backend.SameExecution
// differs). Either must leave the new lock untouched.
func TestTakeoverRefusesAChangedHolder(t *testing.T) {
	cases := []struct {
		name    string
		checked Holder
		actual  Holder
	}{
		{"another run", Holder{RunID: "aaaa", Execution: execA}, Holder{RunID: "bbbb", Execution: execA, ExpiresAt: t0.Add(time.Hour)}},
		{"same run restarted under a new execution", Holder{RunID: "aaaa", Execution: execA}, Holder{RunID: "aaaa", Execution: execB, ExpiresAt: t0.Add(time.Hour)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for name, b := range buckets(t) {
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					key := Key("acme-app", "fugaro/changed-"+c.name)
					if _, err := b.Create(ctx, key, mustJSON(t, c.actual), "application/json"); err != nil {
						t.Fatal(err)
					}
					if err := Takeover(ctx, b, key, c.checked); !errors.Is(err, ErrHolderChanged) {
						t.Fatalf("Takeover of a changed holder = %v, want ErrHolderChanged", err)
					}
					data, _, err := b.Read(ctx, key)
					if err != nil || string(data) != string(mustJSON(t, c.actual)) {
						t.Fatalf("the new holder's lock was touched: %v, %s", err, data)
					}
				})
			}
		})
	}
}

// TestTakeoverLosesRaceWhenLockChangesUnderneath: the lock changes to a
// new holder between Takeover's read and its delete (another takeover, or
// a refresh); the generation-matched delete must lose, leaving the new
// holder's lock exactly as it wrote it. This is Takeover's own version of
// the concurrent-takeover race Acquire already guarantees a single winner
// for (TestAcquireLosesConcurrentTakeover): whichever actor's write lands
// first, the other must never delete out from under it.
func TestTakeoverLosesRaceWhenLockChangesUnderneath(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/race")
			checked := Holder{RunID: "aaaa", Execution: execA}
			checkedBody := mustJSON(t, checked)
			if _, err := b.Create(ctx, key, checkedBody, "application/json"); err != nil {
				t.Fatal(err)
			}
			rival := Holder{RunID: "bbbb", Execution: execB, ExpiresAt: t0.Add(time.Hour)}
			beforeTakeoverDelete = func() {
				beforeTakeoverDelete = nil
				// A new run acquired the branch between our read and our
				// delete: Acquire's own create-if-absent would never let
				// this double-write happen for real, but any write that
				// changes the generation exercises the same guard.
				_, gen, err := b.Read(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := b.ReplaceIf(ctx, key, mustJSON(t, rival), gen, checkedBody); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { beforeTakeoverDelete = nil })
			if err := Takeover(ctx, b, key, checked); !errors.Is(err, ErrHolderChanged) {
				t.Fatalf("Takeover across the race = %v, want ErrHolderChanged", err)
			}
			data, _, err := b.Read(ctx, key)
			if err != nil || string(data) != string(mustJSON(t, rival)) {
				t.Fatalf("the rival's lock did not survive: %v, %s", err, data)
			}
		})
	}
}

// TestTakeoverSurfacesAForbiddenDelete: under the 0.7.0 bucket hardening a
// launcher's delete of locks/ answers 403. Takeover must return that
// error as itself (isAccessDenied, internal/cli, recognizes it), never
// mask it as ErrHolderChanged or silently succeed, and never delete.
func TestTakeoverSurfacesAForbiddenDelete(t *testing.T) {
	g := gcpfake.NewGCS(t)
	b := g.Bucket(t, "runs")
	ctx := context.Background()
	key := Key("acme-app", "fugaro/forbidden")
	holder := Holder{RunID: "aaaa", Execution: execA}
	if _, err := b.Create(ctx, key, mustJSON(t, holder), "application/json"); err != nil {
		t.Fatal(err)
	}
	g.ForbidObjectDeletes(1)
	err := Takeover(ctx, b, key, holder)
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
		t.Fatalf("Takeover under a forbidden delete = %v, want a 403", err)
	}
	if errors.Is(err, ErrHolderChanged) {
		t.Fatal("a 403 must not be reported as a changed holder")
	}
	if ok, _ := b.Exists(ctx, key); !ok {
		t.Fatal("a forbidden delete must not remove the lock")
	}
}

// TestAcquireUnderstandsAnOldFormatLock: MarkReleasing adds no new field
// to Holder (ExpiresAt is the one every version has always read), so a
// lock an older — or newer, forward-compatible — version wrote, with
// fields this version doesn't expect, is still read correctly: live while
// unexpired, taken over once it isn't.
func TestAcquireUnderstandsAnOldFormatLock(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/old-format")
			raw := []byte(`{"run_id":"20260927-100000-aaaa","expires_at":"2026-09-27T11:00:00Z","future_field":"ignored"}`)
			if err := b.WriteAll(ctx, key, raw, nil); err != nil {
				t.Fatal(err)
			}
			_, err := Acquire(ctx, b, key, Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}, t0)
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Holder.RunID != "20260927-100000-aaaa" || busy.Holder.Execution != "" {
				t.Fatalf("Acquire over an old-format lock = %v, want it busy naming the run with no execution", err)
			}
			// Past its expiry, the same raw object is taken over exactly
			// as a lock this version wrote would be.
			l, err := Acquire(ctx, b, key, Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}, t0.Add(2*time.Hour))
			if err != nil {
				t.Fatalf("takeover of an old-format lock past its expiry = %v", err)
			}
			if err := l.Release(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestMarkReleasingFailureLeavesReleaseWorking: MarkReleasing conflicting
// (the lock changed underneath, here simulated directly) must not corrupt
// the Lock's own generation and body: Release right after must still
// behave exactly as it would without the failed MarkReleasing call —
// here, tolerating the very conflict that made MarkReleasing fail,
// exactly as it already tolerates a taken-over lock.
func TestMarkReleasingFailureLeavesReleaseWorking(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/mark-fail")
			h := Holder{RunID: "20260927-100000-aaaa", ExpiresAt: t0.Add(time.Hour)}
			l, err := Acquire(ctx, b, key, h, t0)
			if err != nil {
				t.Fatal(err)
			}
			// Changed from underneath the held Lock, as a conflicting
			// write mid-writeback would.
			prev, gen, err := b.Read(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			other := Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}
			if _, err := b.ReplaceIf(ctx, key, mustJSON(t, other), gen, prev); err != nil {
				t.Fatal(err)
			}
			if err := l.MarkReleasing(ctx, t0.Add(time.Minute)); err == nil {
				t.Fatal("MarkReleasing over a changed lock = nil, want an error")
			}
			if err := l.Release(ctx); err != nil {
				t.Fatalf("Release after a failed MarkReleasing = %v", err)
			}
			data, _, err := b.Read(ctx, key)
			if err != nil || string(data) != string(mustJSON(t, other)) {
				t.Fatalf("Release touched the other holder's lock: %v, %s", err, data)
			}
		})
	}
}
