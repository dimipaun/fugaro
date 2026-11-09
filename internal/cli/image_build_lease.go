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
// Why the lease is mandatory, with no path around it. A build this command
// cannot reserve against — no budget database, no price, a database that would
// not answer — is a build whose cost nothing bounds, and an unbounded billable
// build is exactly what the confirmation used to be there to prevent. So the
// refusal is the whole of the answer: it names the missing piece and the one
// command that supplies it (D1550's rule for a missing price, and the budget
// backend's own sentence for a database that is not there).
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
// this build's reservation.
//
// w is where the two lines a person has to read go: the leases a dead CLI left
// behind and this command closed, and the advisory of a cap the project's mode
// does not let refuse the build (D1548). Both are notes about money, so they
// are printed rather than left to the record.
//
// The error is the whole of the refusal: an over-cap build is
// *BuildOverCapError (estimate, cap, spent, the one command, exit 1), a missing
// price is budget.NoPriceError's sentence, and a database that would not answer
// is the budget backend's own (fail closed, D1550).
func openBuildLease(ctx context.Context, w io.Writer, lc *localcfg.Config, repo, slug string) (*buildLease, error) {
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
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
	cost := newImageCost(lc, db, repo, slug, who, newBuildPricer(lc))
	if n, err := cost.reclaim(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		fmt.Fprintf(w, "fugaro: gave back %d build lease(s) a CLI that died mid-build left open, so the day no longer counts %s of them\n",
			n, map[bool]string{true: "it", false: "any"}[n == 1])
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
// reservation back. waited says whether this command watched the build: when it
// did not (--no-wait), the whole reservation is recorded, because the build
// will run and nothing here knows for how long — the other direction, releasing
// it, would let a loop of --no-wait submissions run with nothing counted at all.
func (b *buildLease) settle(ctx context.Context, waited bool) error {
	_, err := b.cost.settle(ctx, b.lease, buildSpend(b.lease, waited, time.Since(b.started)))
	return err
}

// release gives the whole reservation back: nothing was submitted, so nothing
// is billed.
func (b *buildLease) release(ctx context.Context) error {
	_, err := b.cost.release(ctx, b.lease)
	return err
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
