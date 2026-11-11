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

// Figure is one dimension of a chain read: the amount, the floor under it that
// the runs the read could read put there, and whether the chain measured it at
// all.
type Figure struct {
	Amount Amount
	// Floor is what the runs the read could actually read establish, and it is
	// the figure a caller's --used-* may not go under (D1048) whichever way the
	// dimension came back. Where the read measured the dimension it is at or
	// under Amount — the same sum, with whatever the runs it could not read
	// were charged on top of it — and where it could not, Amount is nothing to
	// read while this is still the sum of the runs that were read. A caller who
	// could state a figure under it could state a zero and take the runs it
	// could not read off the chain's charge again, which is the whole cap by
	// the back door (T1, F14).
	Floor Amount
	// Measured is false when the chain has no figure for the dimension, and
	// Amount is then nothing to read rather than a number to trust. It is
	// false in three cases. With the budget off there is no ledger, and no run
	// record carries a whole-run token total — result.json's only token
	// figures are the gateway's cache sums, which exclude the input and
	// output tokens a cap counts — so a budget-off chain does not measure
	// tokens rather than approximating them (D1051, F8). A run whose ledger
	// cannot be read leaves the dimension it could not be charged in
	// unmeasured, where the caller's PerRunCaps had no bound for it: the only
	// other figures available are a zero and the sum of the runs that could
	// be read, both of which say the chain spent less than it may have
	// (D1049). And a read that stopped at a gap leaves the record-backed
	// dimensions unmeasured too: the records behind the hop are gone with the
	// one the link named, so the time and the rounds of the runs that could
	// still be read are a lower bound of the chain's, and reporting a lower
	// bound as the figure is the shortening F21 and T5 forbid. A reader
	// prints that; a false Measured is never a zero that was read.
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
// That fallback is one no caller can always pass: the chain's limit is that
// stored number and the read is what names the root run holding it, so a caller
// with only the caps passes no bound and charges the chain's own limit for the
// dimension the read leaves unmeasured instead — the same figure by the other
// route, and the one number the charge has to be.
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

// LedgerMiss is one run of a chain whose money could not be read, and which of
// the three ways it failed: Err nil is a node the database does not have, Err
// set a read that failed, and ErrChainGap a run the link reaches whose record
// the bucket no longer holds — charged the per-run cap and left with the runs
// behind it unfollowable (ResolveChain). The first two are charged the per-run
// cap (D1049) — and in a dimension the caller's PerRunCaps has no bound for,
// charged nothing and left unmeasured instead — and all three are kept apart
// here because a reader names which one it hit (L1).
type LedgerMiss struct {
	Run string
	Err error
}

// Chain is one unit of work's runs and the budget they share (D1045): the
// chain's id, the runs the link names, and what they have spent.
type Chain struct {
	// Root is the chain's id: the root run id (D1045) — the run a pull
	// request's branch is named for, the run a retry's stored task carries,
	// or the run --continues names. It is empty for the one read that stopped
	// at a gap: the run that began the chain is behind the hop the bucket no
	// longer holds anything under, so its id was never read, and rooting the
	// chain at the oldest run the link still reached would be the shortening
	// a gap must not become.
	Root string
	// Runs are the chain's run ids, the root first and the run the read
	// started from last. They come from the link, one record named at a
	// time, and never from a listing (F21). A read that stopped at a gap
	// holds the runs it could still reach, which are the ones after the hop
	// and not a chain of their own.
	Runs []string
	// Charged are the runs whose money could not be read, in Runs' order,
	// with the hop a gap stopped at last: its record is what is missing, so
	// it is in no Run and is named here (ErrChainGap).
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

// figure is one dimension's reading: the charge the chain holds, and none at
// all when the dimension is not measured — a sum over the runs that could be
// read is a lower bound, and a lower bound reported as the figure is the one
// thing an unmeasured dimension must not hand a reader. The sum is not thrown
// away with it: it is the figure's Floor, which is what the read did establish
// however the dimension came back.
func figure(amount, floor Amount, measured bool) Figure {
	if !measured {
		return Figure{Floor: floor}
	}
	return Figure{Amount: amount, Floor: floor, Measured: true}
}

// ErrChainGap is a chain read that stopped at a link whose record the bucket no
// longer holds. It is told apart from every other failed read because it is the
// one a caller may answer for rather than refuse: a run's objects age out
// together — its record and its stored task — so a chain older than the window
// the bucket keeps has a hop with nothing under it, and a caller holding the
// previous run's stored task holds what the chain left that run, a bound on
// what is left now. Every other failed read is a figure unknown for a reason no
// other object answers, and a caller that carried on without it would run past
// a limit nothing established.
//
// The read that hits one still answers what it read, with the hop charged
// (ResolveChain), and only for a chain with a ledger to charge it in: with the
// budget off the figures are the records, and a chain the records stop partway
// through has no figure at all — the runs the link still reaches are a lower
// bound of it, and the sum of a lower bound is the shortening this error exists
// to prevent.
var ErrChainGap = errors.New("the chain's records stop at a link the bucket no longer holds")

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
// other case and ends the walk there: its record is the only thing that says
// which run came before it, so with it gone the runs behind that hop cannot be
// found (F21, T5: a launcher can delete a record). What that hop does *not* do
// is drop what was read: the runs the link still reaches are answered, with the
// hop charged the per-run cap the way a ledger that cannot be read is charged
// (D1049 — the most that run could have been holding, and the dimension left
// unmeasured where the caller's PerRunCaps has no bound for it), named in
// Charged with ErrChainGap, and the record-backed dimensions reported
// unmeasured rather than as a sum over the runs that could still be read: below
// the hop sits a chain of unknown length whose records are gone with it, so
// every figure the readable runs give is a lower bound, and a lower bound
// reported as the chain's figure is exactly the shortening F21 and T5 forbid.
// The error is returned with the chain where there is a ledger to charge the
// hop in, and alone where there is not: with the budget off the figures *are*
// the records, so a read the records stop partway through has none.
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
	// gap is the linked run whose record the bucket does not have: the walk
	// stops there, and the runs behind it are not named by anything the read
	// can reach.
	var gap string
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
			gap = id
			id = ""
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
	c.Runs = ids
	// The chain's id is the run that began it, which a gap puts on the far
	// side of a hop the read cannot follow: naming the oldest run it did read
	// would root the chain at a run the link never said was the root.
	if gap == "" {
		c.Root = ids[0]
	}

	var usd, tokens, seconds, rounds Amount
	// usdRead and tokensRead are the same sums without what the runs that could
	// not be read were charged for: the floor a caller's --used-* may not go
	// under, and so the one figure an unmeasured dimension still hands over.
	var usdRead, tokensRead Amount
	unbound := map[Dimension]bool{}
	// charge is one run whose money could not be read, and what the read
	// counted for it: the per-run cap where the caller bounds the dimension
	// (D1049), and the dimension left unmeasured where it does not. A run
	// whose node is absent, a run whose read failed and the hop of a gap are
	// all three charged here, because all three leave the same figure unknown
	// — and the hop is charged the cap rather than read: the cap is the most
	// it could have been holding, so it is the larger of the two figures and
	// the direction D1049 takes.
	charge := func(id string, err error) {
		c.Charged = append(c.Charged, LedgerMiss{Run: id, Err: err})
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
	}
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
			held := recordUSD(rec)
			usd, usdRead = usd+held, usdRead+held
			continue
		}
		l, found, err := ledgers.ReadLedger(ctx, s.Slug(), id)
		if err != nil || !found {
			charge(id, err)
			continue
		}
		held, tk := Amount(l.Held()), Amount(l.Tokens)
		usd, usdRead = usd+held, usdRead+held
		tokens, tokensRead = tokens+tk, tokensRead+tk
	}
	if gap != "" {
		charge(gap, ErrChainGap)
	}
	c.used[USD] = figure(usd, usdRead, !unbound[USD])
	c.used[Tokens] = figure(tokens, tokensRead, ledgers != nil && !unbound[Tokens])
	c.used[Time] = figure(seconds, seconds, gap == "")
	c.used[ReviewRounds] = figure(rounds, rounds, gap == "")
	if gap != "" {
		err := fmt.Errorf("the chain of run %s/%s links to run %s, whose record runs/%s/%s/result.json does not have, so the runs behind it cannot be found: %w", s.Slug(), from, gap, s.Slug(), gap, ErrChainGap)
		if ledgers == nil {
			return nil, err
		}
		return c, err
	}
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
//
// Every number here is a record's, and a record is a launcher's own text
// (F21, T2), so none of them may take time off the chain: a finished_at
// before the started_at, a stage duration written as negative, and a run in
// flight whose start sits after now (a clock skew) would each subtract from
// the chain's figure and hand a --max-time chain time it has spent, which is
// the direction D1060 exists to close. A negative stage duration counts
// nothing, the elapsed time floors at zero, and the run's own figure floors
// with them — a record that runs backwards holds no time, the same way one
// whose cost_usd it cannot hold counts nothing (recordUSD).
func chainSeconds(rec *Record, now time.Time) Amount {
	var stages float64
	for _, st := range rec.Stages {
		if st.DurationS > 0 {
			stages += st.DurationS
		}
	}
	var elapsed float64
	switch {
	case rec.StartedAt.IsZero():
		// No start was recorded: there is no elapsed time to take.
	case rec.FinishedAt != nil:
		elapsed = math.Max(0, rec.FinishedAt.Sub(rec.StartedAt).Seconds())
	default:
		elapsed = math.Max(0, now.Sub(rec.StartedAt).Seconds())
	}
	return Amount(math.Max(0, math.Max(elapsed, stages)))
}
