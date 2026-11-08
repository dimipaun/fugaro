package orchestrator_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The audit log's tests (O3, D1229, D1230).
//
// The log is the one place a person reads afterwards, and the two things that
// make it readable are the id and the order: an id derived from the event, so
// a producer's retry is a write to the same key rather than a second line, and
// a stamp the database server gave, never a laptop's clock. Both are asserted
// here by reading the node the fake really carries.
//
// **The red step that was run and watched fail** (this file's): with Append's
// id taken from the caller's entry instead of derived from the subject and the
// transition, TestAuditAppendIsIdempotentOnADerivedId fails with two entries
// for one event delivered twice. Restored; see the pull request.

// TestAuditAppendIsIdempotentOnADerivedId is D1230's rule, one append at a
// time: the same event delivered twice is one line, at the key its own two
// names derive, stamped with the server's clock.
//
// Mutation: mint the id from the caller (or from a counter) instead of
// deriving it, drop the cleaning of the free text, or stamp the entry from the
// caller's clock, and the case that names it stops failing. Honour an id or a
// stamp the caller set (`if e.ID == ""`, `if e.At.IsZero()`) and the case that
// passes both stops failing, which is what makes that guard a tested one
// rather than a line the next edit may delete.
func TestAuditAppendIsIdempotentOnADerivedId(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	clock := leaseAt
	now := func() (time.Time, bool) { return clock, true }
	audit := mustAudit(t, rtdbClient(t, f), now)
	auditPath := mustPath(t, orchestrator.PathAudit)

	event := orchestrator.Entry{Subject: winnerHolder, Transition: "started", Text: "the daemon took the lease"}
	first, err := audit.Append(ctx, event)
	if err != nil {
		t.Fatalf("the first append: %v", err)
	}
	if want := winnerHolder + ":started"; first.ID != want {
		t.Fatalf("the entry's id is %q, not the one its names derive (%q)", first.ID, want)
	}
	if first.At != leaseAt {
		t.Errorf("the entry is stamped %s, not the server time the append read (%s)", first.At, leaseAt)
	}

	// The same event, delivered twice — a tick that retried, a command that
	// ran again. One event is one line, whatever the caller's clock says.
	clock = leaseAt.Add(90 * time.Second)
	second, err := audit.Append(ctx, event)
	if err != nil {
		t.Fatalf("the retried append: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("the retry wrote %q where the event's first append wrote %q", second.ID, first.ID)
	}
	log := nodeObject(t, f.Value(auditPath), "the audit log")
	if len(log) != 1 {
		t.Fatalf("the log carries %d entries for one event delivered twice: %v", len(log), logKeys(log))
	}
	if _, ok := log[first.ID]; !ok {
		t.Errorf("the log's one entry is not at the derived key %q: %v", first.ID, logKeys(log))
	}

	// A different transition is a different event: a second line, and the
	// first one is left exactly where it was.
	clock = leaseAt.Add(2 * time.Minute)
	if _, err := audit.Append(ctx, orchestrator.Entry{Subject: winnerHolder, Transition: "finished", Text: "the run merged"}); err != nil {
		t.Fatalf("the second event's append: %v", err)
	}
	log = nodeObject(t, f.Value(auditPath), "the audit log")
	if len(log) != 2 {
		t.Fatalf("the log carries %d entries for two events: %v", len(log), logKeys(log))
	}
	entry := nodeObject(t, log[first.ID], "the first entry")
	if got := nodeText(t, entry, "transition"); got != "started" {
		t.Errorf("the first entry's transition is %q after a second append", got)
	}
	if got := nodeText(t, entry, "id"); got != first.ID {
		t.Errorf("the first entry carries the id %q, not %q", got, first.ID)
	}

	t.Run("the free text is cleaned and capped", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		audit := mustAudit(t, rtdbClient(t, f), clockAt(leaseAt))
		long := strings.Repeat("a", orchestrator.MaxAuditTextRunes*3)
		stored, err := audit.Append(ctx, orchestrator.Entry{
			Subject: winnerHolder, Transition: "blocked",
			Text: long + "\n\x1b[31mred\x1b[0m",
		})
		if err != nil {
			t.Fatalf("the append: %v", err)
		}
		if got := utf8.RuneCountInString(stored.Text); got != orchestrator.MaxAuditTextRunes {
			t.Errorf("the stored text is %d runes, not the cap of %d", got, orchestrator.MaxAuditTextRunes)
		}
		if strings.ContainsFunc(stored.Text, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			t.Errorf("the stored text still carries control characters: %q", stored.Text)
		}
		doc := nodeObject(t, f.Value(mustPath(t, orchestrator.PathAudit)), "the audit log")
		entry := nodeObject(t, doc[stored.ID], "the entry")
		if got := nodeText(t, entry, "text"); got != stored.Text {
			t.Errorf("the node carries %q and the append returned %q: the caller is told a line the log does not have", got, stored.Text)
		}
	})

	t.Run("an id and a stamp from the caller are ignored", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		audit := mustAudit(t, rtdbClient(t, f), clockAt(leaseAt))

		// A producer that minted both itself, with a forged id that is a name a
		// key can carry (so what this case catches cannot be the empty key) and a
		// stamp two days in the past: the two things that make the log readable
		// are the derived id, which makes one event one line however often it is
		// delivered, and the server's order, which is what a laptop's clock
		// cannot be. A caller that could set either would make both untrue.
		stored, err := audit.Append(ctx, orchestrator.Entry{
			ID: winnerHolder + ":forged", At: leaseAt.Add(-48 * time.Hour),
			Subject: winnerHolder, Transition: "blocked", Text: "a line a caller wrote",
		})
		if err != nil {
			t.Fatalf("the append: %v", err)
		}
		if want := winnerHolder + ":blocked"; stored.ID != want {
			t.Errorf("the entry keeps the caller's id %q, not the one its names derive (%q)", stored.ID, want)
		}
		if stored.At != leaseAt {
			t.Errorf("the entry is stamped %s, not the server time the append read (%s)", stored.At, leaseAt)
		}

		log := nodeObject(t, f.Value(mustPath(t, orchestrator.PathAudit)), "the audit log")
		if len(log) != 1 {
			t.Fatalf("the log carries %d entries for one append: %v", len(log), logKeys(log))
		}
		if _, ok := log[winnerHolder+":forged"]; ok {
			t.Errorf("the caller's own id was written as a key: %v", logKeys(log))
		}
		entry := nodeObject(t, log[stored.ID], "the entry")
		if got := nodeText(t, entry, "id"); got != stored.ID {
			t.Errorf("the node carries the id %q, not the derived one (%q)", got, stored.ID)
		}
		if got := nodeText(t, entry, "at"); got != leaseAt.Format(time.RFC3339) {
			t.Errorf("the node carries the stamp %q, not the server's (%q)", got, leaseAt.Format(time.RFC3339))
		}
	})

	t.Run("a name a key cannot carry is refused and nothing is written", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		audit := mustAudit(t, rtdbClient(t, f), clockAt(leaseAt))
		auditPath := mustPath(t, orchestrator.PathAudit)

		for _, e := range []orchestrator.Entry{
			// A dot is refused by the transport wherever it appears in the
			// tree, so it is not a name here either: the subject is checked by
			// the state model's own rule (idRE), which is what makes the key
			// this log derives one the database carries.
			{Subject: "run.20261107", Transition: "started"},
			{Subject: winnerHolder, Transition: "Started"},
			{Subject: winnerHolder + ":other", Transition: "started"},
			{Subject: "", Transition: "started"},
		} {
			if _, err := audit.Append(ctx, e); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Errorf("an entry %q/%q was not refused as %v: %v", e.Subject, e.Transition, orchestrator.ErrInvalid, err)
			}
		}
		if f.Value(auditPath) != nil {
			t.Errorf("a refused append wrote the log: %v", f.Value(auditPath))
		}
	})

	t.Run("an entry with no server time is not written", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		audit := mustAudit(t, rtdbClient(t, f), neverAnswered)
		if _, err := audit.Append(ctx, orchestrator.Entry{Subject: winnerHolder, Transition: "started"}); !errors.Is(err, orchestrator.ErrNoServerTime) {
			t.Fatalf("an append with no server time ended as %v, not ErrNoServerTime", err)
		}
		if f.Value(mustPath(t, orchestrator.PathAudit)) != nil {
			t.Error("an entry with no server time was written: a laptop's clock is not an order")
		}
	})
}

// neverAnswered is a clock no request has ever answered: the state
// rtdb.ServerNow is in before the first response.
func neverAnswered() (time.Time, bool) { return time.Time{}, false }

func logKeys(log map[string]any) []string {
	keys := make([]string, 0, len(log))
	for k := range log {
		keys = append(keys, k)
	}
	return keys
}
