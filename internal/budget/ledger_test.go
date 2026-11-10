package budget_test

// The one read a chain makes of the budget database: a run's ledger, by name
// (ledger.go). A chain's money and token figures come from these nodes (D1046),
// and the read is the launcher's, because a run's token reads its own ledger
// and no sibling's (U4).
//
// The figures a caller draws from the read are pinned here too, beside the read
// that carries them: Outstanding() is the held amount whether or not the run
// settled (D1046), and a ledger that does not read is charged the run's full
// per-run cap, never zero (D1049).
//
// Each test reads through kill_test.go's readKillClient: it is the same
// *rtdb.Client over a fresh fake database, and a second copy of it here would
// be a second thing to keep in step.

import (
	"context"
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

func TestReadLedgerReadsANamedRun(t *testing.T) {
	ctx := context.Background()
	client, db := readKillClient(t)
	db.Set(budget.PathRun(slug, runID), budget.RunLedger{
		Reserved: 5_000_000, Released: 1_000_000, Spent: 2_000_000, Tokens: 12_345,
	})
	// A sibling's ledger is not what a named read answers with: a chain read is
	// a list of names, never a listing (F21).
	db.Set(budget.PathRun(slug, otherRun), budget.RunLedger{Reserved: 9_000_000, Tokens: 999})

	l, found, err := budget.LedgerDB{DB: client}.ReadLedger(ctx, slug, runID)
	if err != nil || !found {
		t.Fatalf("ReadLedger(%s, %s) = %+v, found=%v, err=%v", slug, runID, l, found, err)
	}
	if l.Reserved != 5_000_000 || l.Released != 1_000_000 || l.Spent != 2_000_000 || l.Tokens != 12_345 {
		t.Fatalf("ledger = %+v, want the run's own node", l)
	}
	if l.Outstanding() != 4_000_000 {
		t.Fatalf("Outstanding = %d, want 4000000", l.Outstanding())
	}
}

func TestReadLedgerKeepsUnreadableApartFromAbsent(t *testing.T) {
	ctx := context.Background()
	client, db := readKillClient(t)
	r := budget.LedgerDB{DB: client}

	// Absent: the node is not there, and the read succeeded in saying so.
	l, found, err := r.ReadLedger(ctx, slug, runID)
	if err != nil || found {
		t.Fatalf("an absent ledger = %+v, found=%v, err=%v; want found=false and no error", l, found, err)
	}

	// Unreadable: the call itself failed. D1049 charges the two the same, and a
	// chain still prints which of them it hit, so they must not collapse into
	// one answer.
	db.DenyRead(budget.PathRun(slug, runID))
	l, found, err = r.ReadLedger(ctx, slug, runID)
	if err == nil {
		t.Fatalf("a denied read = %+v, found=%v, err=nil; want the read's failure", l, found)
	}
	if !errors.Is(err, rtdb.ErrPermission) {
		t.Fatalf("err = %v, want rtdb.ErrPermission", err)
	}
	if found {
		t.Fatalf("found = true on a read that failed")
	}
}

func TestReadLedgerRefusesAnEmptyRunID(t *testing.T) {
	ctx := context.Background()
	client, db := readKillClient(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("an empty run id was accepted")
			}
		}()
		_, _, _ = budget.LedgerDB{DB: client}.ReadLedger(ctx, slug, "")
	}()
	if creds := db.Credentials(); len(creds) != 0 {
		t.Fatalf("an empty run id sent %d requests to the database: %v", len(creds), creds)
	}
}

func TestOutstandingIsTheHeldAmountWhetherOrNotTheRunSettled(t *testing.T) {
	ctx := context.Background()
	client, db := readKillClient(t)
	r := budget.LedgerDB{DB: client}

	// A run that settled released everything it did not spend, so the amount it
	// still holds is exactly what it spent.
	db.Set(budget.PathRun(slug, runID), budget.RunLedger{Reserved: 5_000_000, Spent: 2_000_000, Released: 3_000_000})
	settled, found, err := r.ReadLedger(ctx, slug, runID)
	if err != nil || !found {
		t.Fatalf("ReadLedger = %+v, found=%v, err=%v", settled, found, err)
	}
	if settled.Outstanding() != settled.Spent || settled.Outstanding() != 2_000_000 {
		t.Fatalf("a settled run holds %d and spent %d, want both 2000000", settled.Outstanding(), settled.Spent)
	}

	// A run that did not — the container died between the halt and the release,
	// the sweeper has not reached it — still holds what it reserved, and there
	// is no closed state to branch on: one formula answers both.
	db.Set(budget.PathRun(slug, otherRun), budget.RunLedger{Reserved: 5_000_000, Spent: 2_000_000, Crashed: true})
	held, found, err := r.ReadLedger(ctx, slug, otherRun)
	if err != nil || !found {
		t.Fatalf("ReadLedger = %+v, found=%v, err=%v", held, found, err)
	}
	if held.Outstanding() != 5_000_000 || held.Outstanding() <= held.Spent {
		t.Fatalf("an unreleased run holds %d and spent %d, want the held amount above the spend", held.Outstanding(), held.Spent)
	}
}

func TestUnreadableLedgerChargesTheFullPerRunCap(t *testing.T) {
	ctx := context.Background()
	client, db := readKillClient(t)
	r := budget.LedgerDB{DB: client}

	caps := budget.Caps{Global: &budget.GlobalCaps{PerRunMicros: ptr(5_000_000)}}
	perRun, ok := caps.PerRun()
	if !ok {
		t.Fatal("no per-run cap to charge")
	}

	// D1049's rule, as the chain read applies it: what the run holds when its
	// ledger reads, and its full per-run cap — the most it could have been
	// holding — when it does not. Never zero.
	charge := func(run string) budget.Micros {
		l, found, err := r.ReadLedger(ctx, slug, run)
		if err != nil || !found {
			return perRun
		}
		return l.Outstanding()
	}

	db.Set(budget.PathRun(slug, runID), budget.RunLedger{Reserved: 5_000_000, Released: 4_000_000, Spent: 1_000_000})
	if got := charge(runID); got != 1_000_000 {
		t.Fatalf("a ledger that reads is charged %d, want the 1000000 it holds", got)
	}

	if got := charge(otherRun); got != perRun {
		t.Fatalf("an absent ledger is charged %d, want the full per-run cap %d", got, perRun)
	}

	db.DenyRead(budget.PathRun(slug, otherRun))
	if got := charge(otherRun); got != perRun {
		t.Fatalf("an unreadable ledger is charged %d, want the full per-run cap %d", got, perRun)
	}
}
