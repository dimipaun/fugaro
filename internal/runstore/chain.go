package runstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/task"
)

// Dimension is one of the four a chain limit bounds (spec §2): dollars and
// tokens, which are money and so come from the budget database's per-run
// ledger, and wall time and review rounds, which a clock and a round count
// and so come from the run records.
type Dimension string

const (
	USD          Dimension = "usd"
	Tokens       Dimension = "tokens"
	Time         Dimension = "time"
	ReviewRounds Dimension = "review_rounds"
)

// Amount is one dimension's figure, in that dimension's own unit and never
// converted between dimensions: micro-dollars for USD (the unit the ledger
// keeps money in, so a chain's sum is exact), whole tokens for Tokens,
// seconds for Time (a stage's duration_s is fractional) and a count for
// ReviewRounds.
type Amount float64

// Figure is one dimension of a chain read: the amount, and whether the chain
// measured it at all.
type Figure struct {
	Amount Amount
	// Measured is false when the chain has no figure for the dimension, and
	// Amount is then nothing to read rather than a number to trust. It is
	// false in two cases. With the budget off there is no ledger, and no run
	// record carries a whole-run token total — result.json's only token
	// figures are the gateway's cache sums, which exclude the input and
	// output tokens a cap counts — so a budget-off chain does not measure
	// tokens rather than approximating them (D1051, F8). And a run whose
	// ledger cannot be read leaves the dimension it could not be charged in
	// unmeasured, where the caller's PerRunCaps had no bound for it: the
	// only other figures available are a zero and the sum of the runs that
	// could be read, both of which say the chain spent less than it may have
	// (D1049). A reader prints that; a false Measured is never a zero that
	// was read.
	Measured bool
}

// Ledger is what a chain read takes from one run's ledger node,
// /runs/<slug>/<run>: its two money leaves and its token leaf, which is
// every figure the database keeps for a run (F20). It is this package's own
// small shape, not budget.RunLedger, because internal/budget imports this
// package (rollover_facts.go) and so this one cannot import it back; the
// money is budget.Micros' own type (pricing.Micros), which is what lets
// budget's named read satisfy LedgerReader below without a conversion.
type Ledger struct {
	Reserved pricing.Micros
	Released pricing.Micros
	Tokens   int64
}

// Held is what the run still counts against the shared counters: reserved
// less released (budget.RunLedger.Outstanding, D1046). A run that settled
// normally released everything it did not spend, so this is its spend; a run
// that did not — the container died between the halt and the release, the
// sweeper has not reached it — holds more, which is the direction the
// sweeper takes. There is no settled state to branch on: RunLedger carries
// no leaf that says a run is closed (F20).
func (l Ledger) Held() pricing.Micros { return l.Reserved - l.Released }

// LedgerReader reads one run's ledger out of the budget database by name. It
// is the whole seam between a chain read and the budget: the launcher fills
// it with the read its viewer role allows, the runner's is its own run's
// token, and a test's is a fake.
//
// found false is a node the database does not have; a non-nil err is a read
// that failed. D1049 charges both a run's full per-run cap and a chain keeps
// which of the two it hit, so the two are never collapsed into one error.
type LedgerReader interface {
	ReadLedger(ctx context.Context, slug, run string) (Ledger, bool, error)
}

// PerRunCaps is, per dimension, the largest amount one run of this chain
// could have been holding, and so what a ledger that cannot be read is
// charged (D1049). The caller passes the bounds it resolved: the effective
// per-run cap where the installation sets one — the lower of the policy's
// and the database's — and otherwise **the chain's own limit for that
// dimension**, which is then the tightest bound in play. Neither is the
// chain's limit where a per-run cap exists: that one is the root launch's
// number and lives in its stored task (D1046).
//
// **Zero is the absence of a bound**, in this codebase's own vocabulary for
// both dimensions: policy.PerRunUSD is documented `0 = unset` and
// agent.max_run_tokens (FUGARO_MAX_RUN_TOKENS) `0 is none`, and the per-run
// token cap is optional policy with no database counterpart, so an
// installation with no token bound is the ordinary case rather than a
// strange one. A database per-run cap of literally zero is the one thing
// this reading costs: it bounds a run to spending nothing, so no such run
// can be holding anything, and reading it as no bound at all only makes the
// token and dollar dimensions *more* cautious than they need to be.
//
// A dimension the caller has no bound for cannot be charged, and this read
// never charges a zero (D1049: a missing ledger read as free restores the
// whole chain's budget, F14). That dimension is reported **unmeasured** —
// see Figure.Measured — and the run is named in Charged, so a caller
// enforcing a limit on it knows the figure is unknown rather than smaller
// than it is. Refusing the whole read instead would stop a chain over a
// dimension it may not even limit (a --max-usd chain in an installation that
// sets no token cap), and D1049 is explicit that one failed read is not a
// stopped chain.
type PerRunCaps struct {
	USD    pricing.Micros
	Tokens int64
}

// LedgerMiss is one run of a chain whose ledger could not be read, and which
// of the two ways it failed: Err set is a read that failed, Err nil a node
// the database does not have. Both are charged the per-run cap (D1049) —
// and in a dimension the caller's PerRunCaps has no bound for, charged
// nothing and left unmeasured instead — and they are kept apart here because
// a reader names which one it hit (L1).
type LedgerMiss struct {
	Run string
	Err error
}

// Chain is one unit of work's runs and the budget they share (D1045): the
// chain's id, the runs the link names, and what they have spent.
type Chain struct {
	// Root is the chain's id: the root run id (D1045) — the run a pull
	// request's branch is named for, the run a retry's stored task carries,
	// or the run --continues names.
	Root string
	// Runs are the chain's run ids, the root first and the run the read
	// started from last. They come from the link, one record named at a
	// time, and never from a listing (F21).
	Runs []string
	// Charged are the runs whose ledger could not be read, in Runs' order.
	Charged []LedgerMiss
	// Enforced is whether the money and token figures are the budget
	// database's. It is false when the read had no ledger to consult — the
	// budget is off — where the records are the figures and the chain is a
	// report and not a stop (D1046, T10).
	Enforced bool

	used map[Dimension]Figure
}

// Used is what the chain's runs have already spent in one dimension
// (D1046, D1047): the dollars and tokens from their ledgers, the time and
// rounds from their records. A dimension the chain does not measure reports
// Measured false.
func (c *Chain) Used(dim Dimension) Figure { return c.used[dim] }

// Remaining is what is left of limit once the chain's use is taken off it
// (D1047): limit less used. It is not clamped at zero — a chain that spent
// past its limit reads as spent, not as unspent — and a dimension the chain
// does not measure stays unmeasured rather than becoming the whole limit.
func (c *Chain) Remaining(limit Amount, dim Dimension) Figure {
	used := c.Used(dim)
	if !used.Measured {
		return Figure{}
	}
	return Figure{Amount: limit - used.Amount, Measured: true}
}

// figure is one dimension's reading, and none at all when the dimension is
// not measured: a sum over the runs that could be read is a lower bound, and
// a lower bound reported as the figure is the one thing an unmeasured
// dimension must not hand a reader.
func figure(a Amount, measured bool) Figure {
	if !measured {
		return Figure{}
	}
	return Figure{Amount: a, Measured: true}
}

// ResolveChain reads the chain the run of s belongs to: the runs its records
// link back to the root, and what each of them has already spent.
//
// The link is followed one record at a time, by name — each run's
// follow_up.previous_run names the run before it — because a listing is not
// available and is not what the link is (F21: the job's account is bound to
// runs/<slug>/ objects and "the runner never lists").
//
// The run the read was named for may have no record at all: it never started,
// since a run writes result.json as it starts — the run a --retry's stored
// task names, and the one a --continues names — so nothing is read for it and
// it holds nothing (D1045). A run the link *reaches* with no record is the
// other case and ends the read with an error: its record is the only thing
// that says which run came before it, so with it gone the runs behind that
// hop cannot be found, and answering anyway would root the chain at the hop
// and drop every run before it from both the membership and the spend — the
// same reason a record that cannot be read for any other reason and a link
// that runs in a circle end the read (F21, T5: a launcher can delete a
// record, and a chain's figure is never the one a deletion leaves behind).
//
// ledgers is the budget database, nil when the budget is off — where the
// records are the figures and the chain is UNENFORCED (D1046) — perRun is
// what a run whose ledger cannot be read is charged (D1049), leaving each
// dimension it holds no bound for unmeasured, and now is the moment a run
// still in flight is measured at.
func ResolveChain(ctx context.Context, s *Store, ledgers LedgerReader, perRun PerRunCaps, now time.Time) (*Chain, error) {
	from := s.RunID()
	if from == "" {
		return nil, errors.New("runstore: a chain read needs the run it resolves from")
	}
	c := &Chain{Enforced: ledgers != nil, used: make(map[Dimension]Figure, 4)}

	// Walk the link back from the run the caller named, newest first.
	// linked is whether the id in hand came from a record's link, which is
	// what tells a hop of the chain apart from the run the read was named
	// for: the two mean different things when the bucket has no record.
	var ids []string
	records := map[string]*Record{}
	id, linked := from, false
	for id != "" {
		if _, seen := records[id]; seen {
			// A record names a run the walk has already passed: a launcher
			// can write records (F21), and a link that runs in a circle is
			// not a chain.
			return nil, fmt.Errorf("the chain of run %s/%s links in a circle at %s", s.Slug(), from, id)
		}
		if !task.ValidRunID(id) {
			// Every id here becomes an object name under runs/<slug>/, and
			// a record's previous_run is a launcher's own text: one that is
			// not a run id's form is refused rather than joined into a path
			// that could leave this repository (F21, T5).
			return nil, fmt.Errorf("the chain of run %s/%s names %q, which is not a run id", s.Slug(), from, id)
		}
		rec, err := s.Sibling(id).ReadRecord(ctx)
		switch {
		case errors.Is(err, ErrNotFound) && linked:
			return nil, fmt.Errorf("the chain of run %s/%s links to run %s, whose record runs/%s/%s/result.json does not have, so the runs behind it cannot be found", s.Slug(), from, id, s.Slug(), id)
		case errors.Is(err, ErrNotFound):
			// The run the read was named for never started, so the chain is
			// it alone and it holds nothing (D1045).
			ids = append(ids, id)
			id = ""
		case err != nil:
			return nil, fmt.Errorf("reading the record of run %s/%s: %w", s.Slug(), id, err)
		default:
			records[id] = rec
			ids = append(ids, id)
			if rec.FollowUp == nil {
				id = ""
			} else {
				id, linked = rec.FollowUp.PreviousRun, true
			}
		}
	}
	slices.Reverse(ids)
	c.Runs, c.Root = ids, ids[0]

	var usd, tokens, seconds, rounds Amount
	unbound := map[Dimension]bool{}
	for _, id := range ids {
		rec := records[id]
		if rec == nil {
			// Only the run the read was named for can be here, and only
			// when it never started: it holds nothing (D1045).
			continue
		}
		seconds += chainSeconds(rec, now)
		rounds += Amount(len(rec.Reviews))
		if ledgers == nil {
			usd += recordUSD(rec)
			continue
		}
		l, found, err := ledgers.ReadLedger(ctx, s.Slug(), id)
		if err != nil || !found {
			c.Charged = append(c.Charged, LedgerMiss{Run: id, Err: err})
			// A dimension the caller has no bound for is not charged: a
			// zero here reads the run as free, which is the figure D1049
			// forbids, so the dimension is reported unmeasured instead (and
			// not as the sum of the runs that could be read, which is a
			// lower bound rather than the figure).
			if perRun.USD == 0 {
				unbound[USD] = true
			} else {
				usd += Amount(perRun.USD)
			}
			if perRun.Tokens == 0 {
				unbound[Tokens] = true
			} else {
				tokens += Amount(perRun.Tokens)
			}
			continue
		}
		usd += Amount(l.Held())
		tokens += Amount(l.Tokens)
	}
	c.used[USD] = figure(usd, !unbound[USD])
	c.used[Tokens] = figure(tokens, ledgers != nil && !unbound[Tokens])
	c.used[Time] = Figure{Amount: seconds, Measured: true}
	c.used[ReviewRounds] = Figure{Amount: rounds, Measured: true}
	return c, nil
}

// recordUSD is a run's model spend as its own record states it: the dollar
// figure of the budget-off fallback (D1046). A launcher can write a record
// (F21), which is why that fallback is UNENFORCED whatever it says. A figure
// a record cannot hold — NaN, negative — counts nothing rather than failing
// the read, and one past what a dollar amount can be reads at the most it
// can hold, so a malformed record never reads cheaper than what it claimed.
func recordUSD(rec *Record) Amount {
	usd, err := pricing.FromUSD(math.Min(rec.CostUSD, pricing.MaxUSD))
	if err != nil {
		return 0
	}
	return Amount(usd)
}

// chainSeconds is one run's elapsed time as the chain counts it: the larger
// of the record's own elapsed time and the sum of its stages' durations, so
// a start moved forward cannot report less time than the work took (D1060).
// A run still in flight has no finished_at and is measured to now — the same
// read runview.Row.Elapsed makes, stated here as well because runview
// imports this package and this one cannot call it back.
func chainSeconds(rec *Record, now time.Time) Amount {
	var stages float64
	for _, st := range rec.Stages {
		stages += st.DurationS
	}
	var elapsed float64
	switch {
	case rec.StartedAt.IsZero():
		// No start was recorded: there is no elapsed time to take.
	case rec.FinishedAt != nil:
		elapsed = rec.FinishedAt.Sub(rec.StartedAt).Seconds()
	default:
		elapsed = now.Sub(rec.StartedAt).Seconds()
	}
	return Amount(math.Max(elapsed, stages))
}
