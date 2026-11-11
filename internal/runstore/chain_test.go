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

// A record the link reaches that the bucket does not have stops the walk
// there: it is the only thing that names the run before it, so the runs behind
// that hop cannot be found (F21). The root's record and the middle one's are
// the two ways that hop sits, and neither may read as a shorter chain that
// spent less — the read answers the runs it could still reach, with the hop
// charged and the error, and never as a chain rooted at the hop with the runs
// before it dropped from membership and spend alike.
func TestChainRefusesALinkedRunWithNoRecord(t *testing.T) {
	for _, gone := range []string{chainRoot, chainMid} {
		t.Run(gone, func(t *testing.T) {
			b := chainBucket(t)
			if err := b.Delete(ctx, "runs/acme-app/"+gone+"/result.json"); err != nil {
				t.Fatal(err)
			}
			f := &fakeLedgers{found: map[string]Ledger{
				chainRoot: {Reserved: 1_000_000},
				chainMid:  {Reserved: 2_000_000},
				chainTip:  {Reserved: 3_000_000},
			}}
			c, err := ResolveChain(ctx, Open(b, "acme-app", chainTip), f, chainCaps, chainNow)
			if err == nil {
				t.Fatalf("a chain whose link reaches %s with no record read as a chain (ledger reads: %v)", gone, f.asked)
			}
			if !errors.Is(err, ErrChainGap) {
				t.Fatalf("err = %v, want the missing record's own error", err)
			}
			if !strings.Contains(err.Error(), gone) {
				t.Fatalf("err = %v, want the run with no record (%s) named", err, gone)
			}
			if c == nil || c.Root != "" {
				t.Fatalf("read = %+v, want the runs it could still reach and no root: the run that began the chain is behind the hop", c)
			}
			if slices.Contains(c.Runs, gone) {
				t.Fatalf("Runs = %v, want the runs the link still reaches, without %s", c.Runs, gone)
			}
		})
	}
}

// A chain whose link reaches a run the bucket no longer holds, with the budget
// on: the walk stops there, and what it must not do is answer the runs it read
// alone — the runs behind the hop cannot be found, so the sum over the runs
// that could still be read is a lower bound of the chain's spend, and a lower
// bound reported as the figure is what D1049 and F21 forbid (a chain never
// reads cheaper than it may have). The hop is charged the per-run cap the way
// a ledger that cannot be read is charged, the record-backed dimensions the
// hop truncated are reported unmeasured, and the error rides with it.
func TestChainGapChargesTheMissingRunAndKeepsTheRunsItRead(t *testing.T) {
	b := chainBucket(t)
	if err := b.Delete(ctx, "runs/acme-app/"+chainMid+"/result.json"); err != nil {
		t.Fatal(err)
	}
	// The tip's ledger alone is readable: the runs behind the hop may hold
	// anything, and 9_000_000 in the database under the root's name is never
	// read — nothing the link still reaches names that run.
	f := &fakeLedgers{found: map[string]Ledger{
		chainRoot: {Reserved: 9_000_000},
		chainTip:  {Reserved: 2_000_000, Released: 500_000, Tokens: 7_000},
	}}
	c, err := ResolveChain(ctx, Open(b, "acme-app", chainTip), f, chainCaps, chainNow)
	if !errors.Is(err, ErrChainGap) {
		t.Fatalf("err = %v, want the gap reported", err)
	}
	if c == nil {
		t.Fatal("the read answered no chain at all, so nothing of what it did read is reported")
	}
	if want := []string{chainTip}; !slices.Equal(c.Runs, want) {
		t.Fatalf("Runs = %v, want %v: the runs the link still reaches", c.Runs, want)
	}
	if c.Root != "" {
		t.Fatalf("Root = %q, want none: the run that began the chain is behind the hop", c.Root)
	}
	if len(c.Charged) != 1 || c.Charged[0].Run != chainMid || !errors.Is(c.Charged[0].Err, ErrChainGap) {
		t.Fatalf("Charged = %+v, want the hop the link reaches and its own error", c.Charged)
	}
	// The hop is charged the per-run cap -- the most it could have been
	// holding (D1049) -- and the run that could be read what its ledger
	// holds: 1_500_000 + the 5_000_000 cap, never the 1_500_000 alone.
	if got := c.Used(USD); !got.Measured || got.Amount != 6_500_000 {
		t.Fatalf("Used(usd) = %+v, want the readable run's held dollar and the hop's whole cap", got)
	}
	if got := c.Used(Tokens); !got.Measured || got.Amount != 107_000 {
		t.Fatalf("Used(tokens) = %+v, want the readable run's tokens and the hop's whole cap", got)
	}
	// The time and the rounds come from the records, and the records behind
	// the hop are gone with it: unmeasured, never the tip's own half hour and
	// no rounds.
	if got := c.Used(Time); got.Measured {
		t.Fatalf("Used(time) = %+v, want it unmeasured: the records behind the hop are gone", got)
	}
	if got := c.Used(ReviewRounds); got.Measured {
		t.Fatalf("Used(review_rounds) = %+v, want it unmeasured: the records behind the hop are gone", got)
	}
	if got := c.Remaining(9_000_000, USD); !got.Measured || got.Amount != 2_500_000 {
		t.Fatalf("Remaining(9 USD) = %+v, want the held figure and the cap taken off it", got)
	}
	// The database is asked for the runs the link names and no others: the
	// run behind the hop is not named by anything the read can reach.
	if want := []string{"acme-app/" + chainTip}; !slices.Equal(f.asked, want) {
		t.Fatalf("ledger reads = %v, want %v", f.asked, want)
	}
}

// The same gap with a caller that bounds neither dimension: there is no cap to
// charge the hop, and charging it a zero would read the runs behind it as free
// (D1049), so both money dimensions are reported unmeasured and the hop is
// named. With no ledger at all -- the budget off -- the read answers no chain:
// its figures are the records, and a record sum over the runs that could still
// be read is a lower bound rather than the chain's spend.
func TestChainGapWithNoBoundIsUnmeasured(t *testing.T) {
	b := chainBucket(t)
	if err := b.Delete(ctx, "runs/acme-app/"+chainMid+"/result.json"); err != nil {
		t.Fatal(err)
	}
	f := &fakeLedgers{found: map[string]Ledger{chainTip: {Reserved: 2_000_000}}}
	c, err := ResolveChain(ctx, Open(b, "acme-app", chainTip), f, PerRunCaps{}, chainNow)
	if !errors.Is(err, ErrChainGap) {
		t.Fatalf("err = %v, want the gap reported", err)
	}
	if c == nil {
		t.Fatal("the read answered no chain at all")
	}
	if got := c.Used(USD); got.Measured || got.Amount != 0 {
		t.Fatalf("Used(usd) = %+v, want no figure: the runs behind the hop cannot be bounded in dollars", got)
	}
	if got := c.Used(Tokens); got.Measured {
		t.Fatalf("Used(tokens) = %+v, want it unmeasured too", got)
	}
	if len(c.Charged) != 1 || c.Charged[0].Run != chainMid {
		t.Fatalf("Charged = %+v, want the hop named for a reader that has to say why", c.Charged)
	}

	// The budget off: the records are the figures, so a read the records stop
	// partway through has none to answer.
	c, err = ResolveChain(ctx, Open(b, "acme-app", chainTip), nil, chainCaps, chainNow)
	if !errors.Is(err, ErrChainGap) || c != nil {
		t.Fatalf("a budget-off read of the same chain = %+v, %v, want no chain and the gap", c, err)
	}
}

// chainOfCaps is one chain read with the per-run caps the caller resolved.
func chainOfCaps(t *testing.T, b *blob.Bucket, from string, ledgers LedgerReader, caps PerRunCaps) *Chain {
	t.Helper()
	c, err := ResolveChain(ctx, Open(b, "acme-app", from), ledgers, caps, chainNow)
	if err != nil {
		t.Fatalf("ResolveChain(%s) = %v", from, err)
	}
	return c
}

// A --max-tokens chain in the ordinary installation, where agent.max_run_tokens
// is unset (0 is none) and the database has no per-run token cap, so the only
// token bound in play is the chain's own: the caller passes that as the
// bound, and the run whose ledger cannot be read is charged it. The chain then
// reads spent rather than keeping its whole token budget -- remaining(tokens)
// overstated by that run's whole spend is exactly what D1049 forbids.
func TestChainUnreadableLedgerChargesTheChainBound(t *testing.T) {
	const limit = 2_000_000
	f := &fakeLedgers{
		found:  map[string]Ledger{chainRoot: {Tokens: 1_000}, chainMid: {Tokens: 1_000}},
		absent: map[string]bool{chainTip: true},
	}
	c := chainOfCaps(t, chainBucket(t), chainTip, f, PerRunCaps{USD: 5_000_000, Tokens: limit})
	got := c.Remaining(limit, Tokens)
	if !got.Measured || got.Amount > 0 {
		t.Fatalf("Remaining(%d tokens) = %+v, want the chain read as spent, not its budget handed back", limit, got)
	}
}

// A ledger that cannot be read and a dimension the caller has no bound for:
// there is no cap to charge and charging nothing would read the run as free
// (D1049), so the dimension is reported unmeasured -- never as a zero, and
// never as the sum of the runs that could be read, which is a lower bound and
// not the figure. A chain limited on the other dimension still reads whole.
func TestChainUnreadableLedgerWithNoBoundIsUnmeasured(t *testing.T) {
	f := &fakeLedgers{
		found:  map[string]Ledger{chainRoot: {Reserved: 1_000_000}, chainMid: {Reserved: 1_000_000}},
		absent: map[string]bool{chainTip: true},
	}
	b := chainBucket(t)
	c := chainOfCaps(t, b, chainTip, f, PerRunCaps{Tokens: 25_000})
	if got := c.Used(USD); got.Measured || got.Amount != 0 {
		t.Fatalf("Used(usd) = %+v, want no figure: the run in flight cannot be bounded in dollars", got)
	}
	if got := c.Remaining(9_000_000, USD); got.Measured {
		t.Fatalf("Remaining(usd) = %+v, want it unmeasured, not the whole limit", got)
	}
	if got := c.Used(Tokens); !got.Measured || got.Amount != 25_000 {
		t.Fatalf("Used(tokens) = %+v, want the 25_000 the caller bounded", got)
	}
	if got := c.Used(Time); !got.Measured {
		t.Fatalf("Used(time) = %+v, want the records' own figure untouched", got)
	}
	// No bound on either half: both money dimensions are unmeasured, and the
	// run is still named for a reader that has to say why.
	c = chainOfCaps(t, b, chainTip, f, PerRunCaps{})
	if got := c.Used(Tokens); got.Measured {
		t.Fatalf("Used(tokens) with no bound = %+v, want it unmeasured", got)
	}
	if len(c.Charged) != 1 || c.Charged[0].Run != chainTip {
		t.Fatalf("Charged = %+v, want the run whose ledger could not be read", c.Charged)
	}
}

// Time is one figure a launcher cannot talk the chain out of. A record is a
// launcher's own text (F21), so its finished_at can sit before its
// started_at and a stage's duration_s can be negative, and a run still in
// flight can carry a start after the moment the read is taken at; none of
// those may hand the chain its time back -- a --max-time chain reads spent,
// never refunded (D1060) -- so a run that runs backwards contributes nothing,
// while a positive stage duration the same record carries still counts.
func TestChainTimeCannotBeLoweredByAForgeableRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  string
		edit func(*Record)
		want Amount
	}{
		{
			// The middle run's twenty minutes run backwards and both of its
			// stages say they took negative time: the run counts nothing, and
			// the chain never reads less than its other two runs spent.
			name: "a finished_at before the started_at",
			run:  chainMid,
			edit: func(r *Record) {
				at := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
				r.FinishedAt = &at
				for i := range r.Stages {
					r.Stages[i].DurationS = -4_000
				}
			},
			want: 2_700,
		},
		{
			// A stage duration written as negative counts nothing: the middle
			// run contributes the 200s of its other stage rather than
			// 999_800s less than that.
			name: "a negative stage duration",
			run:  chainMid,
			edit: func(r *Record) {
				at := r.StartedAt
				r.FinishedAt = &at
				r.Stages[0].DurationS = -1_000_000
			},
			want: 2_900,
		},
		{
			// The run in flight started after chainNow -- a clock skew -- and
			// contributes nothing rather than a negative age.
			name: "a start after the moment of the read",
			run:  chainTip,
			edit: func(r *Record) { r.StartedAt = chainNow.Add(time.Hour) },
			want: 2_100,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := chainBucket(t)
			forge(t, b, tc.run, tc.edit)
			if got := chainOf(t, b, chainTip, &fakeLedgers{}).Used(Time); !got.Measured || got.Amount != tc.want {
				t.Fatalf("Used(time) = %+v, want %v: a record cannot take time off the chain", got, tc.want)
			}
		})
	}
}
