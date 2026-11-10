package runstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
)

// The chain in testdata/chain: a root run, two follow-ups of it (F10), a run
// no link names, and a task that was stored and never started. Every run id
// in the fixtures is a placeholder.
const (
	chainRoot    = "20260101-090000-aaaa"
	chainMid     = "20260101-100000-bbbb"
	chainTip     = "20260101-110000-cccc"
	chainOther   = "20260101-120000-dddd" // in the bucket, in no chain
	chainRetried = "20260101-130000-eeee" // stored, never started: no record
)

// chainNow is the moment the fixture chain is read at: the tip run started
// half an hour before it and has not finished.
var chainNow = time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)

// chainCaps is a per-run cap no fixture carries, so a run charged it is
// never mistaken for one whose ledger was read.
var chainCaps = PerRunCaps{USD: 5_000_000, Tokens: 100_000}

// chainBucket seeds the fixture chain into a fresh bucket, each record under
// the run id its file is named for.
func chainBucket(t *testing.T) *blob.Bucket {
	t.Helper()
	dir := filepath.Join("testdata", "chain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := memblob.OpenBucket(nil)
	t.Cleanup(func() { b.Close() })
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if err := b.WriteAll(ctx, "runs/acme-app/"+id+"/result.json", data, nil); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// chainOf is one chain read of the fixture chain from from.
func chainOf(t *testing.T, b *blob.Bucket, from string, ledgers LedgerReader) *Chain {
	t.Helper()
	c, err := ResolveChain(ctx, Open(b, "acme-app", from), ledgers, chainCaps, chainNow)
	if err != nil {
		t.Fatalf("ResolveChain(%s) = %v", from, err)
	}
	return c
}

// fakeLedgers is the whole seam L1 fills: one ledger per run id, the runs
// whose node the database does not have, and the runs whose read fails. It
// answers a run id it is handed and can do nothing else, which is the read
// F21 requires of a chain.
type fakeLedgers struct {
	found  map[string]Ledger
	absent map[string]bool
	broken map[string]bool
	// asked is every read, as <slug>/<run>, in the order they were made.
	asked []string
}

func (f *fakeLedgers) ReadLedger(_ context.Context, slug, run string) (Ledger, bool, error) {
	f.asked = append(f.asked, slug+"/"+run)
	switch {
	case f.broken[run]:
		return Ledger{}, false, errors.New("the budget database could not be read")
	case f.absent[run]:
		return Ledger{}, false, nil
	}
	l, ok := f.found[run]
	return l, ok, nil
}

// forge rewrites one run's record in the bucket: what a launcher holds the
// grant to do (F21), and what no chain read trusts for money (T2).
func forge(t *testing.T, b *blob.Bucket, id string, edit func(*Record)) {
	t.Helper()
	s := Open(b, "acme-app", id)
	rec, err := s.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	edit(rec)
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// A chain's money is what its runs hold: the root settled holding 500_000 µ$
// and the run after it settled holding 1_000_000, while the run in flight
// holds the whole lease it took, 3_000_000 (D1046, D1047).
func TestChainUsedSumsTheLedgers(t *testing.T) {
	f := &fakeLedgers{found: map[string]Ledger{
		chainRoot: {Reserved: 2_000_000, Released: 1_500_000},
		chainMid:  {Reserved: 1_500_000, Released: 500_000},
		chainTip:  {Reserved: 3_000_000},
	}}
	c := chainOf(t, chainBucket(t), chainTip, f)
	if got := c.Used(USD); !got.Measured || got.Amount != 4_500_000 {
		t.Fatalf("Used(usd) = %+v, want 4_500_000 µ$", got)
	}
	if got := c.Remaining(6_000_000, USD); !got.Measured || got.Amount != 1_500_000 {
		t.Fatalf("Remaining(6 USD) = %+v, want 1_500_000 µ$", got)
	}
	if want := []string{"acme-app/" + chainRoot, "acme-app/" + chainMid, "acme-app/" + chainTip}; !slices.Equal(f.asked, want) {
		t.Fatalf("ledger reads = %v, want %v", f.asked, want)
	}
}

// The figure is what the run still holds — reserved less released — and not
// what it reserved: a run that settled released what it did not spend, so
// its outstanding amount is exactly its spend (D1046).
func TestChainUsedIsTheHeldAmount(t *testing.T) {
	f := &fakeLedgers{found: map[string]Ledger{chainRoot: {Reserved: 2_000_000, Released: 1_500_000}}}
	c := chainOf(t, chainBucket(t), chainRoot, f)
	if got := c.Used(USD); !got.Measured || got.Amount != 500_000 {
		t.Fatalf("Used(usd) = %+v, want the 500_000 µ$ held, not the 2_000_000 reserved", got)
	}
}

// A release is capped at reserved − spent by the rules, so no token a
// launcher can mint writes a chain's figure below what its run spent (T3).
func TestChainUsedCannotBeLoweredByARelease(t *testing.T) {
	// spent 800_000, released at its cap: 2_000_000 − 800_000.
	f := &fakeLedgers{found: map[string]Ledger{chainRoot: {Reserved: 2_000_000, Released: 1_200_000}}}
	c := chainOf(t, chainBucket(t), chainRoot, f)
	if got := c.Used(USD); got.Amount != 800_000 {
		t.Fatalf("Used(usd) = %+v, want the 800_000 µ$ the run spent", got)
	}
}

// The money is the database's, not the record's: a launcher can write a
// record (F21), and one claiming the run cost a cent changes no figure the
// engine reads while the budget is on (T2).
func TestChainUsedIgnoresAForgeableRecord(t *testing.T) {
	b := chainBucket(t)
	forge(t, b, chainRoot, func(r *Record) { r.CostUSD = 0.01 })
	f := &fakeLedgers{found: map[string]Ledger{chainRoot: {Reserved: 2_000_000}}}
	if got := chainOf(t, b, chainRoot, f).Used(USD); !got.Measured || got.Amount != 2_000_000 {
		t.Fatalf("Used(usd) = %+v, want the ledger's 2_000_000 µ$ and not the record's cent", got)
	}
}

// The token dimension is the ledger's tokens leaf and nothing else, the one
// figure the database already keeps (D1051).
func TestChainTokenDimensionIsTheLedgersTokens(t *testing.T) {
	f := &fakeLedgers{found: map[string]Ledger{
		chainRoot: {Tokens: 1_000},
		chainMid:  {Tokens: 4_000},
		chainTip:  {Tokens: 2_500},
	}}
	c := chainOf(t, chainBucket(t), chainTip, f)
	if got := c.Used(Tokens); !got.Measured || got.Amount != 7_500 {
		t.Fatalf("Used(tokens) = %+v, want 7_500", got)
	}
}

// Time is the larger of a run's own elapsed time and the sum of its stages'
// durations, so a record whose start is moved forward cannot report less
// time than the work took (D1060): the root ran 600s with 900s of stages,
// the run after it 1200s with 500s, and the run in flight has been going
// 1800s at chainNow.
func TestChainTimeTakesTheLargerOfRecordAndStages(t *testing.T) {
	c := chainOf(t, chainBucket(t), chainTip, &fakeLedgers{})
	if got := c.Used(Time); !got.Measured || got.Amount != 3_900 {
		t.Fatalf("Used(time) = %+v, want 3_900 seconds", got)
	}
}

// The rounds are the reviews the runs recorded, one entry per round: two,
// two and one in the chain's three records.
func TestChainReviewRoundsAreTheRecordsReviews(t *testing.T) {
	c := chainOf(t, chainBucket(t), chainTip, &fakeLedgers{})
	if got := c.Used(ReviewRounds); !got.Measured || got.Amount != 5 {
		t.Fatalf("Used(review_rounds) = %+v, want 5", got)
	}
	if got := c.Remaining(3, ReviewRounds); got.Amount != -2 {
		t.Fatalf("Remaining(3 rounds) = %+v, want −2: a chain past its limit reads as spent, not as empty", got)
	}
}

// A ledger that cannot be read is charged the run's full per-run cap, never
// zero (D1049): one run's node is absent from the database and the next
// one's read fails, and both are charged, each named for which of the two it
// hit.
func TestChainUnreadableLedgerChargesTheFullPerRunCap(t *testing.T) {
	f := &fakeLedgers{
		found:  map[string]Ledger{chainRoot: {Reserved: 1_000_000}},
		absent: map[string]bool{chainMid: true},
		broken: map[string]bool{chainTip: true},
	}
	c := chainOf(t, chainBucket(t), chainTip, f)
	if got := c.Used(USD); !got.Measured || got.Amount != 11_000_000 {
		t.Fatalf("Used(usd) = %+v, want 1_000_000 held plus two per-run caps of 5_000_000", got)
	}
	if got := c.Used(Tokens); !got.Measured || got.Amount != 200_000 {
		t.Fatalf("Used(tokens) = %+v, want two per-run caps of 100_000", got)
	}
	if len(c.Charged) != 2 {
		t.Fatalf("Charged = %+v, want the two runs whose ledger could not be read", c.Charged)
	}
	absent, broken := c.Charged[0], c.Charged[1]
	if absent.Run != chainMid || absent.Err != nil || broken.Run != chainTip || broken.Err == nil {
		t.Fatalf("Charged = %+v, want %s absent and %s failing, kept apart", c.Charged, chainMid, chainTip)
	}
}

// With the budget off there is no ledger to read, so a chain is the records
// alone and says it is unenforced — a report and not a stop — while its
// token dimension is not measured at all rather than approximated from the
// cache sums (D1046, D1051, T10).
func TestChainOfABudgetOffReadIsUnenforced(t *testing.T) {
	b := chainBucket(t)
	forge(t, b, chainRoot, func(r *Record) { r.CostUSD = 1.25 })
	forge(t, b, chainMid, func(r *Record) { r.CostUSD = 0.5 })
	c := chainOf(t, b, chainTip, nil)
	if c.Enforced {
		t.Fatal("Enforced = true with no ledger to read, want the chain marked UNENFORCED")
	}
	if got := c.Used(USD); !got.Measured || got.Amount != 1_750_000 {
		t.Fatalf("Used(usd) = %+v, want the records' 1_750_000 µ$", got)
	}
	if got := c.Used(Tokens); got.Measured {
		t.Fatalf("Used(tokens) = %+v, want it unmeasured: no record carries a whole-run token total", got)
	}
	if got := c.Remaining(5, Tokens); got.Measured {
		t.Fatalf("Remaining(tokens) = %+v, want it unmeasured too", got)
	}
}

// The chain of a pull request is the runs its records link, one
// follow_up.previous_run at a time back to the run the branch is named for
// (F10, D1045).
func TestChainOfAPRFollowsPreviousRun(t *testing.T) {
	b := chainBucket(t)
	c := chainOf(t, b, chainTip, &fakeLedgers{})
	if c.Root != chainRoot {
		t.Fatalf("Root = %q, want the root run %q", c.Root, chainRoot)
	}
	if want := []string{chainRoot, chainMid, chainTip}; !slices.Equal(c.Runs, want) {
		t.Fatalf("Runs = %v, want %v", c.Runs, want)
	}
	for _, id := range c.Runs {
		rec, err := Open(b, "acme-app", id).ReadRecord(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Branch != "fugaro/"+chainRoot {
			t.Fatalf("run %s is on branch %q, want the root's fugaro/%s", id, rec.Branch, chainRoot)
		}
	}
}

// A retry relaunches a stored task that never started, so nothing of it is
// under runs/ at all: the chain is rooted at the run id that task carries,
// and nothing of it has been spent (D1045).
func TestChainOfARetryIsTheStoredRunID(t *testing.T) {
	f := &fakeLedgers{}
	c := chainOf(t, chainBucket(t), chainRetried, f)
	if c.Root != chainRetried || !slices.Equal(c.Runs, []string{chainRetried}) {
		t.Fatalf("chain = %q %v, want %q alone", c.Root, c.Runs, chainRetried)
	}
	if got := c.Used(USD); !got.Measured || got.Amount != 0 {
		t.Fatalf("Used(usd) = %+v, want nothing: a run that never started holds nothing", got)
	}
	if len(f.asked) != 0 {
		t.Fatalf("ledger reads = %v, want none for a run that never started", f.asked)
	}
}

// The chain's run ids come from the link, one record named at a time, and
// never from a listing: a record the link does not name is in no chain, and
// the database is asked for the chain's runs alone (F21).
func TestChainIdsComeFromTheLinkNotAListing(t *testing.T) {
	f := &fakeLedgers{found: map[string]Ledger{
		chainRoot:  {Reserved: 1_000_000},
		chainMid:   {Reserved: 1_000_000},
		chainTip:   {Reserved: 1_000_000},
		chainOther: {Reserved: 9_000_000},
	}}
	c := chainOf(t, chainBucket(t), chainTip, f)
	if want := []string{chainRoot, chainMid, chainTip}; !slices.Equal(c.Runs, want) {
		t.Fatalf("Runs = %v, want %v, without %s", c.Runs, want, chainOther)
	}
	if got := c.Used(USD); got.Amount != 3_000_000 {
		t.Fatalf("Used(usd) = %+v, want 3_000_000: %s holds 9_000_000 and no link names it", got, chainOther)
	}
	if want := []string{"acme-app/" + chainRoot, "acme-app/" + chainMid, "acme-app/" + chainTip}; !slices.Equal(f.asked, want) {
		t.Fatalf("ledger reads = %v, want one named read per run of the chain", f.asked)
	}
}

// refuseChainRead is a chain read that must not answer at all.
func refuseChainRead(t *testing.T, b *blob.Bucket, from string) error {
	t.Helper()
	c, err := ResolveChain(ctx, Open(b, "acme-app", from), &fakeLedgers{}, chainCaps, chainNow)
	if err == nil {
		t.Fatalf("ResolveChain(%s) = %+v, want the read refused", from, c)
	}
	return err
}

// A record's previous_run is a launcher's own text, and it becomes an object
// name under runs/<slug>/: one that is not a run id's form is refused rather
// than joined into a path, or a forged link reads another repository's
// records into this chain (F21, T5).
func TestChainRefusesAPreviousRunThatIsNotARunID(t *testing.T) {
	b := chainBucket(t)
	forge(t, b, chainMid, func(r *Record) { r.FollowUp.PreviousRun = "../../acme-web/" + chainRoot })
	if err := refuseChainRead(t, b, chainTip); !strings.Contains(err.Error(), "not a run id") {
		t.Fatalf("err = %v, want the id refused before it names an object", err)
	}
}

// A link that runs in a circle is not a chain, and reading it as one would
// either never end or report a chain the records do not.
func TestChainRefusesALinkThatRunsInACircle(t *testing.T) {
	b := chainBucket(t)
	forge(t, b, chainRoot, func(r *Record) { r.FollowUp = &FollowUp{PR: 7, PreviousRun: chainTip} })
	if err := refuseChainRead(t, b, chainTip); !strings.Contains(err.Error(), "circle") {
		t.Fatalf("err = %v, want the circle named", err)
	}
}
