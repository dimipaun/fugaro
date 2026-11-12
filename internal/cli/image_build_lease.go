package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra/tf"
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
//
// creds is which account the read is billed to, and the two callers differ: a
// person's build names the installation's project as the quota project (the
// catalog is a global API, and an ADC without one is refused by it), and the
// catalog check job's own service account is billed for its own (budget's
// CatalogCreds).
var newBuildPricer = func(project string, creds budget.CatalogCreds) buildPricer {
	return budget.NewPricer(project, creds)
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
		c.pricer = newBuildPricer(lc.GCPProject, budget.CatalogCreds{})
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
	return openLease(ctx, w, leaseArgs{
		lc: lc, repo: repo, slug: slug,
		machine: lc.Build.MachineType, region: lc.BuildRegion(),
		pricer: func() buildPricer { return pricers.one(lc) },
		// The identity a person's build leases under, which is the account
		// `fugaro run` is asked to run as.
		who: func(ctx context.Context) (string, error) {
			me, err := lc.Me(ctx)
			if err != nil {
				return "", userErr("%v", err)
			}
			return me, nil
		},
	})
}

// leaseArgs is everything one build's lease is taken from, gathered so that the
// two callers that open one share every step after the doors below: the CLI at a
// checkout, whose values are its project config's, and the catalog check job,
// whose values are its own assignment's and whose credentials are its service
// account's (openCatalogJobLease).
//
// lc is the config the three database guards below are asked with
// (budgetDBFor, checkDBProject, requireOwnBudgetDB). The job has no local
// config and builds a minimal one holding exactly the two fields those three
// read — the project's name and its GCP project — rather than restating the
// questions: the ownership question is the one thing in this file that must not
// have a second wording (requireOwnBudgetDB's own doc says why).
type leaseArgs struct {
	w       io.Writer
	lc      *localcfg.Config
	repo    string
	slug    string
	machine string
	region  string
	// pricer is built once a lease is really taken, never for a build that will
	// not be leased at all (buildPricerCache's own rule: an installation with no
	// budget backend holds no permission on the Cloud Billing catalog).
	pricer    func() buildPricer
	who       func(context.Context) (string, error)
	noBackend string
}

// openLease is the one build-lease opener: the guards every writer into the
// budget database asks, the reclaim of what a dead process left, and the take.
//
// noBackend is the line an installation with no budget backend is told instead
// of being refused, and it is the caller's because the two callers say it about
// different things: a checkout is told to add the backend, and the catalog check
// job is told what the missing backend means for a nightly job nobody is
// watching. See this file's header for why that is a warning and a build that
// runs uncounted rather than a refusal.
func openLease(ctx context.Context, w io.Writer, a leaseArgs) (*buildLease, error) {
	if a.lc.Budget == nil || a.lc.Budget.RTDBURL == "" {
		fmt.Fprint(w, a.noBackend)
		return nil, nil
	}
	_, db, err := budgetDBFor(ctx, a.lc)
	if err != nil {
		return nil, err
	}
	if err := checkDBProject(ctx, db, a.lc); err != nil {
		return nil, err
	}
	if err := requireOwnBudgetDB(ctx, a.lc, db, "nothing was built"); err != nil {
		return nil, err
	}
	who, err := a.who(ctx)
	if err != nil {
		return nil, err
	}
	id, err := buildLeaseIDGenerator()
	if err != nil {
		return nil, remote(err)
	}
	cost := newImageCost(db, a.machine, a.region, a.repo, a.slug, who, a.pricer())
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
	var unpriced *buildUnpricedError
	if errors.As(err, &unpriced) {
		fmt.Fprintf(w, "fugaro: warning: this build's cost could not be estimated, so it takes no lease and nothing counts its cost: %s\n", oneLine(unpriced.Error()))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if advisory != nil {
		fmt.Fprintf(w, "fugaro: warning: the project's budget mode is observe, so this cap does not refuse the build and it is submitted anyway: %s; what the build reserves is counted for the day, and `fugaro budget set --global --mode enforce` is what makes a cap bite (D1548)\n",
			oneLine(advisory.Detail))
	}
	return &buildLease{cost: cost, lease: l, started: time.Now()}, nil
}

// openCatalogJobLease is the catalog check job's door of openLease (D1554): the
// nightly job submits a build for every entry of the published catalog whose
// inputs moved, and it is a build a person is not watching, so it leases each
// one exactly as a checkout's build does — same estimator, same caps, same
// verdict (D1543, D1548).
//
// Where its values come from, since it has no checkout and no local config: the
// project's name and GCP project are the job's own environment, the database URL
// is the same budget.rtdb_url every other job of the installation carries
// (CatalogCheck renders it into this job's env), the machine type and the region
// are its assignment's, and the identity is its own service account — the
// account `roles/firebasedatabase.admin` is granted to (the installation
// module's catalogcheck.tf), because a lease is an IAM write and not a rules
// one: the account's access token bypasses the database rules, and the
// /buildleases path is denied whole to every client token (internal/budget's
// rules).
//
// The three guards are called rather than restated. They are asked with a config
// holding exactly what they read — Name, GCPProject and the database URL — so
// the database a lease is written into is proved to be this installation's own
// by the same function that proves it for every other writer, which is the one
// thing here that may not have a second wording (requireOwnBudgetDB's doc).
//
// The identity is derived from the job's own account name and project rather
// than read from a metadata call: the figure is informational (BuildLease.By),
// and the job is deployed by fugaro update, which knows both.
func openCatalogJobLease(ctx context.Context, w io.Writer, e catalogJobEnv, s catalogCheckSpec, repo, slug string) (*buildLease, error) {
	return openLease(ctx, w, leaseArgs{
		lc: &localcfg.Config{
			Name: e.name, GCPProject: e.gcpProject,
			// NoAuth here is the test world's, exactly as it is for every other
			// client this job opens (checkEndpoints, the check job's own
			// endpoints): the job's service account is the real one.
			Endpoints: localcfg.Endpoints{NoAuth: checkEndpoints.NoAuth},
			Budget:    &localcfg.Budget{RTDBURL: e.budgetURL},
		},
		repo: repo, slug: slug,
		machine: s.MachineType, region: s.BuildRegion,
		pricer: func() buildPricer {
			// A job is not a person: the price lookup names no quota project and
			// is billed to the job's own (budget.CatalogCreds' two kinds).
			return newBuildPricer(e.gcpProject, budget.CatalogCreds{ServiceAccount: true})
		},
		who: func(context.Context) (string, error) {
			return tf.CatalogCheckAccountID + "@" + e.gcpProject + ".iam.gserviceaccount.com", nil
		},
		noBackend: fmt.Sprintf("fugaro: warning: project %s has no Firebase budget backend (FUGARO_RTDB_URL is not set in this job's environment), so the builds it submits take no lease and nothing counts their cost: `fugaro update --firebase-project %s` in the checkout adds one, and the next fugaro update re-deploys this job with it (D1543)\n",
			e.name, firebaseProjectIDWord(e.gcpProject)),
	})
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
