package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// image_build_lease.go is the one place the image commands' Cloud Build
// submissions meet the budget: T8's lease, taken before each submission and
// closed after it (T4 of docs/no-prompt-policy — D1543, D1548).
//
// What it replaces, and why that is the whole of the trade. A billable build
// used to be guarded by a confirmation: the project's name typed at a real
// terminal, which --json, a pipe and a coding agent's session could never give.
// That is a prompt, so the policy removes it, and what stands in its place is
// the lease: the build's estimated cost is reserved in the project's budget
// database before the submission, judged by the same predicate a run's lease is
// judged by — the per-run cap, the day caps, the repository's and the project's
// — and a build no cap has room for is refused by name, with the one command
// that raises the cap. In observe mode the refusal is recorded and the build
// proceeds (D1548), which is what makes `fugaro doctor`'s line about an
// installation whose builds nothing refuses worth printing (doctor_build_budget.go).
//
// Why a build on an installation with no budget backend takes no lease at all.
// The backend is optional: `--firebase-project` is what turns it on
// (init_stages.go's own stage skips without it), and a project that never got
// one has no ledger to write a reservation into, no cap to judge the build
// against and no permission on the Billing catalog the price would be read from
// (billing-price-spike.md §1.2) — so there the lookup must not run and there is
// nothing to reserve. What that leaves is the build the confirmation used to
// stand in front of, submitted with nothing counted, and it is the shape a run
// has had on such an installation all along (launch_budget.go returns before
// the call). The alternative is worse than the hole it would close: refusing
// makes every build on a backend-less installation fail, which includes step 4
// of `fugaro update`'s checkout stage — stale images would never be rebuilt and
// the person would be sent to an operator step by the one command that is
// meant to be the whole upgrade (HARD RULE). So the build proceeds and says so:
// openBuildLease prints the one line, and the lease it returns is nil, which
// every use of it here tolerates.
//
// Where that leaves the lease itself. A build this command CAN reserve against
// has no path around it: no price, a database that would not answer, a cap with
// no room — each refuses by name, with the one command that supplies what is
// missing (D1550), because an unbounded billable build is exactly what the
// confirmation used to be there to prevent.
//
// One lease per submission, and the lease is what a dead CLI leaves behind: a
// process that died mid-build leaves its reservation open, and the next command
// gives it back before it takes its own (imageCost.reclaim).

// newBuildPricer is the price lookup a build's lease is sized from. It is a
// package variable because the real one reads the Cloud Billing catalog on the
// caller's own credentials, which a test has no account for and no way to
// inject: the interface (buildPricer, image_cost.go) is what a test hands a
// book of its own.
var newBuildPricer = func(lc *localcfg.Config) buildPricer {
	// The user's own credentials name the installation's project as the quota
	// project: the catalog is a global API, and an ADC without a quota project
	// is refused by it (budget.CatalogCreds).
	return budget.NewPricer(lc.GCPProject, budget.CatalogCreds{})
}

// buildPricerCache is one command's price lookups, and it exists because
// budget.Pricer reads the catalog at most once per command while a command that
// submits builds opens one lease per build: `fugaro image refresh` per workflow,
// a catalog build per entry. Each of those leases handed the pricer it built
// itself would read the catalog again, and every read is a fresh way for the
// loop to stop mid-way on one that failed (D1550 refuses on a read that fails),
// so the command holds one of these and every lease it opens draws from it.
//
// The pricer is built on the first lease that actually takes one, never for a
// build that will not be leased at all. On an installation with no budget
// backend the lookup must not run: that installation holds no permission on the
// catalog (billing-price-spike.md §1.2, openBuildLease's own header).
type buildPricerCache struct{ pricer buildPricer }

// one is the command's pricer for a lease on lc, built on first use and reused
// by every later lease of the same command.
func (c *buildPricerCache) one(lc *localcfg.Config) buildPricer {
	if c.pricer == nil {
		c.pricer = newBuildPricer(lc)
	}
	return c.pricer
}

// buildLeaseIDGenerator is the id one build lease is taken under. It is
// generated here rather than read from Cloud Build because Cloud Build's own
// id exists only once the build is submitted, and the submission is what the
// lease has to come before: task.NewRunID's shape is reused so a lease id
// sorts and reads like every other id this program writes, and the key space
// it lands in (/buildleases/<slug>/<id>) keeps it away from /runs
// (internal/budget/lease_build.go).
var buildLeaseIDGenerator = func() (string, error) { return task.NewRunID(time.Now(), rand.Reader) }

// buildLease is one Cloud Build's reservation: the writer that holds it, the
// lease itself, and the moment the build was submitted, which is what the
// settle that follows measures.
type buildLease struct {
	cost    *imageCost
	lease   budget.BuildLease
	started time.Time
}

// openBuildLease gives back what a CLI that died mid-build left open, and takes
// this build's reservation. On an installation with no budget backend there is
// nothing to give back and nothing to take: the build proceeds under a nil
// lease, after the one line that says so (see this file's header for why that
// is the answer rather than a refusal).
//
// w is where the lines a person has to read go: the leases a dead CLI left
// behind and this command closed, the note that this installation has no
// backend for a build to lease against, and the advisory of a cap the project's
// mode does not let refuse the build (D1548). All of them are notes about
// money, so they are printed rather than left to the record.
//
// The error is the whole of the refusal: an over-cap build is
// *BuildOverCapError (estimate, cap, spent, the one command, exit 1), a missing
// price is budget.NoPriceError's sentence, and a database that would not answer
// is the budget backend's own (fail closed, D1550).
//
// The database is asked whose it is before any of that, because this takes a
// reservation in it and moves the day's counters: both are writes into the one
// shared database, so the build's lease asks the same two questions every other
// writer asks before it writes (checkDBProject at /fugaro/project, then
// requireOwnBudgetDB for the mark — the owner's ruling of 2026-10-31). Without
// them a checkout whose config points budget.rtdb_url at another installation's
// database would be judged against a cap that bounds nothing here, and would
// write its reservation and its counters there — spend that installation never
// made, which its own runs are then refused for.
//
// pricers is the command's own (buildPricerCache): the lease draws the price
// lookup from it rather than building one, so every build the command submits
// is priced from one catalog read — budget.Pricer's own rule, which a command
// that opened a lease per build and built a pricer per lease would break.
func openBuildLease(ctx context.Context, w io.Writer, lc *localcfg.Config, repo, slug string, pricers *buildPricerCache) (*buildLease, error) {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		// The backend's own door (budgetDBFor) refuses with a sentence for an
		// operator; here the answer is not a refusal but a build that runs
		// uncounted, so the line is stated from the build's side: what this
		// build is, and what would make it a leased one.
		fmt.Fprintf(w, "fugaro: warning: project %s has no Firebase budget backend (budget.rtdb_url is not set in its project config), so this build takes no lease and nothing counts its cost: `fugaro update --firebase-project %s` in the checkout adds one (D1543)\n",
			lc.Name, firebaseProjectIDWord(lc.GCPProject))
		return nil, nil
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, err
	}
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was built"); err != nil {
		return nil, err
	}
	who, err := lc.Me(ctx)
	if err != nil {
		return nil, userErr("%v", err)
	}
	id, err := buildLeaseIDGenerator()
	if err != nil {
		return nil, remote(err)
	}
	cost := newImageCost(lc, db, repo, slug, who, pricers.one(lc))
	if n, err := cost.reclaim(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		leases, them := fmt.Sprintf("%d build leases", n), "them"
		if n == 1 {
			leases, them = "a build lease", "it"
		}
		fmt.Fprintf(w, "fugaro: gave back %s a CLI that died mid-build left open, so the day no longer counts %s\n", leases, them)
	}
	l, advisory, err := cost.take(ctx, id)
	if err != nil {
		return nil, err
	}
	if advisory != nil {
		fmt.Fprintf(w, "fugaro: warning: the project's budget mode is observe, so this cap does not refuse the build and it is submitted anyway: %s; what the build reserves is counted for the day, and `fugaro budget set --global --mode enforce` is what makes a cap bite (D1548)\n",
			oneLine(advisory.Detail))
	}
	return &buildLease{cost: cost, lease: l, started: time.Now()}, nil
}

// settle records what the submitted build spent and gives the rest of the
// reservation back. waited says whether this command watched the build to a
// terminal status: when it did not — --no-wait, or a watch it lost — the whole
// reservation is recorded, because the build will run and nothing here knows for
// how long — the other direction, releasing it, would let a loop of --no-wait
// submissions run with nothing counted at all.
//
// A nil lease is the installation with no budget backend (openBuildLease):
// nothing was reserved, so there is nothing to settle and no counters to move.
func (b *buildLease) settle(ctx context.Context, waited bool) error {
	if b == nil {
		return nil
	}
	wctx, cancel := leaseWriteCtx(ctx)
	defer cancel()
	_, err := b.cost.settle(wctx, b.lease, buildSpend(b.lease, waited, time.Since(b.started)))
	return err
}

// release gives the whole reservation back: nothing was submitted, so nothing
// is billed. A nil lease is the installation with no budget backend, where
// nothing was reserved either.
func (b *buildLease) release(ctx context.Context) error {
	if b == nil {
		return nil
	}
	wctx, cancel := leaseWriteCtx(ctx)
	defer cancel()
	_, err := b.cost.release(wctx, b.lease)
	return err
}

// leaseWriteTimeout is how long a lease's closing write may take once the
// command's own context is gone: the lease node and the day's counters, a few
// small conditional writes on the credentials the build was submitted with.
// The 30 seconds are this repository's figure for a write that has to land
// after a signal (cmd/fugaro/main.go's signalContext, run.go's own release
// writes): long enough for a slow database, short enough that a person who
// pressed Ctrl-C is not left waiting.
const leaseWriteTimeout = 30 * time.Second

// leaseWriteCtx is the context a lease's closing write runs under: the caller's
// own values, without its cancellation, and with a timeout of its own.
//
// Why it is not the caller's context. The settle that matters most runs on the
// tail of an already-cancelled one: Ctrl-C during the wait cancels the context
// (first signal cancels and leaves the process alive), Wait returns the lost
// watch's error, and the settle then has to record a build Cloud Build is still
// running. Writing that with the cancelled context fails at once, and what that
// leaves is worse than a missing line: the lease stays OPEN, and once its
// deadline passes the next command's reclaim (imageCost.reclaim) releases it —
// refunding the whole reservation and counting 0 µ$ for a build that is really
// spending money, which is exactly the undercount this settle exists to
// prevent, reached from the other side. The write is a fact about the past (the
// build was submitted and may be running), so a signal about the future is not
// the thing to judge it by; the timeout is what keeps that from hanging on a
// database that will not answer.
func leaseWriteCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), leaseWriteTimeout)
}

// buildSpend is what a submitted build really spent, as far as this command can
// tell: the wall time it watched the build for, priced at the rate the lease
// was taken at and rounded up to whole minutes, which is how Cloud Build bills.
//
// Where the rate comes from, and why it is not carried beside the lease. Cloud
// Build reports a finished build's status, image and log, and no billed time at
// all, so the only measurement there is is this command's own clock — and the
// rate is recoverable from the lease because the reservation IS the rate: a
// lease reserves the build's fixed timeout at the machine's rate
// (BuildRate.Estimate), so the rate is the reservation over the timeout's own
// minutes. Pricing the elapsed time at it is what makes the day count what the
// build cost rather than the hour it was allowed to cost.
//
// The measurement is capped at what the lease holds: a build that waited out
// its queue and then ran still holds one reservation, and Settle refuses an
// amount over it (internal/budget's BuildLease.Settle).
func buildSpend(l budget.BuildLease, waited bool, elapsed time.Duration) budget.Micros {
	unspent := l.Unsettled()
	if !waited {
		return unspent
	}
	perMinute := l.Reserved / (budget.MaxBuildSeconds / 60)
	if perMinute <= 0 || elapsed <= 0 {
		return 0
	}
	minutes := budget.Micros((int64(elapsed) + int64(time.Minute) - 1) / int64(time.Minute))
	if spent := minutes * perMinute; spent < unspent {
		return spent
	}
	return unspent
}

// withLease is the build's own outcome beside the lease's: both are true and
// either one alone would hide the other, and the build's error comes first
// because it is the one the person has to act on (a spec Cloud Build refused, a
// build that failed) while the lease's is the day's counters that could not be
// moved (the message says what to read). A nil lease error is the cause itself.
func withLease(cause, leaseErr error) error {
	switch {
	case leaseErr == nil:
		return cause
	case cause == nil:
		return leaseErr
	}
	return errors.Join(cause, leaseErr)
}
