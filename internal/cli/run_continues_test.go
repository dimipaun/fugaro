package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is L4's half of the chain tests (docs/run-limit-carry-over L4):
// the link a chain with no pull request is continued by (--continues, D1045),
// the caller's --used-* figures (D1048), the cap an unreadable ledger is
// charged (D1049), the remaining a --pr follow-up inherits (D1052) and the
// chain a review job joins (D1058).
//
// The chain's own limit is read from the stored task of the run that began it
// (D1041, D1046), and its spend from the run records when the budget is off,
// so most of these tests need no budget database: they seed a run whose task
// carries the limit and whose record states what it spent (seedChainRun). The
// two that are about the ledger (D1049, and the figures a real ledger holds)
// use the minting fixture, whose config points at the RTDB fake.

// chainRun is the run a chain in this file begins at, and chainFu the launch
// that continues it. They are minted from the wall clock because that is the
// clock run --pr and run --retry read (wallRunIDAt): a follow-up's window is
// followUpLookback back from time.Now(), which no test can move.
var (
	chainRun = wallRunIDAt(2, "090000", "a1a1")
	chainFu  = wallRunIDAt(0, "130000", "b2b2")
)

// chainUSD is the chain limit these tests write into the run that begins a
// chain, and chainSpent the model spend its record states. It is under the
// limits checkout's per_run_usd (5) and under what the mode scaling leaves of
// it, so a launch that names it itself is accepted: this file is about the
// remaining, never about the tightening refusal (run_limits_test.go).
const (
	chainUSD   = 5.0
	chainSpent = 2.0
)

// seedChainRun stores id as a halted run that opened nothing: a task carrying
// the chain limit overrides, and a record whose model spend is costUSD. It is
// the shape a chain with no pull request is continued from — the run --continues
// names — and, with the budget off, the record is the figure the chain reads
// (D1046).
//
// overrides is given as the fields a task carries rather than as the flags,
// because this file seeds the run that began the chain: what its task holds is
// what a later launch inherits, whatever wrote it.
func seedChainRun(t *testing.T, f *cloudFixture, id string, costUSD float64, o task.Overrides) {
	t.Helper()
	spec := firstRunSpec(id)
	spec.Overrides = o
	seedSpec(t, f, spec, false)
	seedChainSpend(t, f, id, costUSD)
}

// seedChainSpend rewrites id's record with a model spend of costUSD, which is
// the figure a chain with the budget off reads for it: a chain spends more
// without the run being launched a second time, which is what a task already
// written cannot express (seedChainRun's own task is CreateTask's, once).
func seedChainSpend(t *testing.T, f *cloudFixture, id string, costUSD float64) {
	t.Helper()
	rec := prRecord(id, "", 0, costUSD)
	rec.Branch, rec.Status, rec.Stage, rec.Outcome = "fugaro/"+id, runstore.StatusHalted, "writeback", runstore.OutcomeNone
	rec.PR, rec.PushedHead = nil, ""
	rec.FollowUp = nil
	writeRecord(t, f, id, rec)
}

// seedChainPRRoot stores id as the first run of PR pr: a task carrying the
// chain limit o, and a record that pushed, which is what resolvePR finds a
// --pr follow-up's previous run through.
func seedChainPRRoot(t *testing.T, f *cloudFixture, id string, costUSD float64, o task.Overrides) {
	t.Helper()
	spec := firstRunSpec(id)
	spec.Overrides = o
	seedSpec(t, f, spec, false)
	rec := prRecord(id, "", 7, costUSD)
	rec.Branch, rec.StartedAt = "fugaro/"+id, time.Now().Add(-48*time.Hour)
	writeRecord(t, f, id, rec)
}

// chainTask is one run's stored task as a launch wrote it.
func chainTask(t *testing.T, f *cloudFixture, id string) *task.Spec {
	t.Helper()
	return readSpec(t, f, id)
}

// chainRig is a launchable checkout with an image that reads the four chain
// limits, which is what every test here launches from: limitsCheckout's file
// sets the repository layer the four --max-* are merged under (per_run_usd 5,
// max_run_tokens 900000), and a chain limit has to be a figure that layer can
// still pay for, or the refusal under test would be run_limits_test.go's.
func chainRig(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	limitsImage(t, f)
	limitsCheckout(t)
	return f
}

// TestRunContinuesAHaltedChain: a run that halted with nothing to continue it
// through — no pull request, so no --pr link — is continued by naming it
// (D1045). The chain's limit is the one the run that began it stored, its
// spend is what that run's record states (the budget-off fallback), and the
// launch that continues it carries the remaining rather than the whole limit
// again: starting from the full cap is the hole D1052 closes.
func TestRunContinuesAHaltedChain(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task"); err != nil {
		t.Fatal(err)
	}
	o := chainTask(t, f, chainFu).Overrides
	if o.MaxUSD == nil {
		t.Fatalf("overrides = %+v, want the chain's remaining dollars", o)
	}
	if got, want := *o.MaxUSD, chainUSD-chainSpent; got != want {
		t.Fatalf("max_usd = %v, want %v (%v of the chain's %v already spent)", got, want, chainSpent, chainUSD)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the continued run launched", n)
	}
}

// TestRunContinuesAChainTheLedgerHasSpendFor: the dollars and tokens of a
// chain are the budget database's per-run ledgers, not the records (D1046), so
// a chain whose runs settled normally reads exactly what they still hold —
// reserved less released — and the remaining is what is left of the chain's
// limit over that figure.
func TestRunContinuesAChainTheLedgerHasSpendFor(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	limitsImage(t, f.cloudFixture)
	limitsCheckout(t)
	usd := chainUSD
	// The record states a figure the ledger disagrees with (9 dollars against
	// the 1.5 the ledger still holds): while the budget is on, the ledger is
	// the chain's figure and the record's cost is not read at all (D1046).
	seedChainRun(t, f.cloudFixture, chainRun, 9, task.Overrides{MaxUSD: &usd})
	// The run settled normally — it released what it did not spend — so it
	// still holds 1.5 of the 2 dollars it reserved, and the chain reads 5 less
	// that.
	f.db.Set(budget.PathRun(appSlug, chainRun), budget.RunLedger{Reserved: 2_000_000, Released: 500_000, Tokens: 400_000})
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task"); err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f.cloudFixture, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != 3.5 {
		t.Fatalf("overrides = %+v, want 3.5 dollars (5 less the 1.5 the chain's ledger holds)", o)
	}
}

// TestRunRefusesAContinuesWithPR: --pr names the pull request whose chain the
// launch joins and --continues names the run directly (D1045). Two answers to
// one question are refused rather than merged: one of them would be ignored,
// and the caller would be told nothing about which chain the run is in.
func TestRunRefusesAContinuesWithPR(t *testing.T) {
	f := chainRig(t)
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{})
	wantRefused(t, f, "pass one of them", "run", "--repo", limitsRepo, "--pr", "7", "--continues", chainRun, "A task")
}

// TestRunRefusesAContinuesWithRetry: a retry relaunches the stored task as it
// is, so it takes no flag that writes one (taskFlags) — and the chain it is in
// is the stored run's own, resolved at the retry's launch (D1053). A
// --continues beside it would name a second chain.
func TestRunRefusesAContinuesWithRetry(t *testing.T) {
	f := chainRig(t)
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{})
	// No --repo: --retry refuses the first of taskFlags the call changed, and
	// any other of them would name itself in this refusal instead.
	wantRefused(t, f, "it takes no --continues", "run", "--retry", chainRun, "--continues", chainFu)
}

// TestRunRefusesAContinuesWithAttempts: a batch is N chains, not one (D1053),
// so a launch naming a previous run and a batch naming none are refused
// together, as --attempts already refuses --pr, --run-id and --retry.
func TestRunRefusesAContinuesWithAttempts(t *testing.T) {
	f := chainRig(t)
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{})
	wantRefused(t, f, "it takes no --continues", "run", "--repo", limitsRepo, "--attempts", "2", "--continues", chainRun, "A task")
}

// TestRunRefusesAContinuesItCannotConfirm: the run --continues names is the
// whole link, so a name the records cannot confirm is refused (T4) — an unknown
// run, a run of another repository (a chain is one repository's runs, and
// another repository's figures are not this one's) and a chain whose records
// run out are each a launch that stops rather than one continued from whatever
// the bucket could still see.
func TestRunRefusesAContinuesItCannotConfirm(t *testing.T) {
	t.Run("a run the bucket does not have", func(t *testing.T) {
		f := chainRig(t)
		wantRefused(t, f, "no run", "run", "--repo", limitsRepo, "--continues", wallRunIDAt(1, "090000", "dead"), "A task")
	})
	t.Run("a run of another repository", func(t *testing.T) {
		f := chainRig(t)
		addOtherRepo(t, f)
		other := mustSlug("github", "acme/other")
		seedTaskIn(t, f, other, chainRun)
		wantRefused(t, f, "a chain is one repository's runs", "run", "--repo", limitsRepo, "--continues", other+"/"+chainRun, "A task")
	})
	t.Run("a chain whose records run out", func(t *testing.T) {
		f := chainRig(t)
		seedChainRun(t, f, chainRun, chainSpent, task.Overrides{})
		// A record that names a run the bucket has none of: the runs behind
		// that hop are not findable, so what the chain spent is unknown, and
		// answering from the named run alone would be the figure the gap
		// leaves behind (F21, T5).
		rec := prRecord(chainRun, "", 0, chainSpent)
		rec.Branch, rec.FollowUp = "fugaro/"+chainRun, &runstore.FollowUp{PreviousRun: wallRunIDAt(5, "090000", "beef")}
		writeRecord(t, f, chainRun, rec)
		wantRefused(t, f, "--continues "+task.ShortRun(chainRun)+": the chain cannot be read",
			"run", "--repo", limitsRepo, "--continues", chainRun, "A task")
	})
}

// TestRunContinuesWithAnUnreadableLedgerChargedTheCap: a chain's ledger that
// cannot be read is never charged zero — a missing read as free restores the
// whole chain's budget, which is cheaper than forging a ledger (F14) — so the
// run is charged its full per-run cap, the most it could have been holding
// (D1049). Here the cap is more than the chain's own limit, so the chain reads
// as spent and the launch is refused; the refusal names the cap it was charged
// and not a zero.
func TestRunContinuesWithAnUnreadableLedgerChargedTheCap(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	limitsImage(t, f.cloudFixture)
	limitsCheckout(t)
	usd := chainUSD
	seedChainRun(t, f.cloudFixture, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	// No ledger node for the run at all, and no release either: the read finds
	// nothing, and the figure the chain holds is unknown.
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the chain charged its full per-run cap and the launch refused", err)
	}
	for _, want := range []string{
		"--continues " + task.ShortRun(chainRun),
		"5 dollars of 5 dollars used", // the installation's per-run cap, not zero
		"nothing to spend",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunContinuesWithAUsedFigureThatRaises: --used-* is the caller's one
// honest knob for spend the chain's own runs do not hold — work a previous
// container did without settling a ledger, say — and it may only raise the
// charge (D1048): the engine read 2 dollars from the record, the caller states
// 3, and 3 is what the chain is charged, so the launch's remaining is 2.
func TestRunContinuesWithAUsedFigureThatRaises(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu,
		"--used-usd", "3", "A task"); err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != 2 {
		t.Fatalf("overrides = %+v, want 2 dollars (5 less the 3 the caller stated)", o)
	}
}

// TestRunRefusesAUsedFigureBelowWhatTheEngineRead: a --used-* below the
// engine's own figure is refused, naming both (D1048). Accepting it as given
// would let a caller state zero and start the chain fresh, which is the whole
// cap again by the back door (T1).
func TestRunRefusesAUsedFigureBelowWhatTheEngineRead(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu,
		"--used-usd", "1", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a --used-usd under the engine's figure refused", err)
	}
	for _, want := range []string{"--used-usd 1 dollar is below what the engine read", "2 dollars", "leave the flag out"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
}

// TestRunStatesWhatAChainItBeginsHasUsed: a launch that begins a chain states
// its limit itself, so --used-* charges that limit for work the chain's own
// runs do not hold (D1048) and what is left is what the launch writes:
// --max-usd 5 with --used-usd 3 begins a chain with 2 dollars left. There is no
// request apart from the limit here — the figure this launch typed IS the
// chain's limit (D1041) — so the two are not held under one another, and a
// --used-* that spends the whole limit is refused rather than launched with
// nothing.
func TestRunStatesWhatAChainItBeginsHasUsed(t *testing.T) {
	f := chainRig(t)
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--run-id", chainFu,
		"--max-usd", "5", "--used-usd", "3", "A task"); err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != 2 {
		t.Fatalf("overrides = %+v, want 2 dollars (5 less the 3 the chain has already used)", o)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the chain's first run launched", n)
	}
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--run-id", wallRunIDAt(0, "131500", "d4d4"),
		"--max-usd", "2", "--used-usd", "3", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a --used-* that spends the whole limit refused", err)
	}
	for _, want := range []string{"--used-usd 3 dollars leaves nothing of this launch's --max-usd 2 dollars", "Lower the figure"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
}

// TestRunRefusesAUsedFigureWithNoChainLimit: what --used-* raises is the charge
// a chain counts against its own limit (D1048), so a dimension the chain does
// not limit has no charge to raise: the figure is refused rather than accepted
// and then dropped by the write. A launch's own --max-usd is not the chain's
// limit — it caps the run — and a --pr follow-up takes the chain's limit from
// the run that began it, which set none in the first subtest.
func TestRunRefusesAUsedFigureWithNoChainLimit(t *testing.T) {
	t.Run("a chain that sets no dollar limit", func(t *testing.T) {
		f := chainRig(t)
		seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{})
		_, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", chainFu,
			"--max-usd", "5", "--used-usd", "3", "A task")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v, want a --used-usd on a chain that limits nothing refused", err)
		}
		for _, want := range []string{"--used-usd needs a chain limit", "caps the run and not the chain"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to name %q", err, want)
			}
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
	t.Run("a first run that sets no limit of its own", func(t *testing.T) {
		f := chainRig(t)
		wantRefused(t, f, "--used-usd needs a chain limit", "run", "--repo", limitsRepo, "--run-id", chainFu, "--used-usd", "3", "A task")
	})
}

// TestRunContinuesARepeatIsIdempotent: the figures a --continues launch writes
// are the chain's at the moment it ran, so a repeat is answered from the stored
// task rather than from a chain read made now — a --run-id repeat is idempotent
// as any other (docs/design/v1.md), the same way a repeated --pr follow-up is.
// The chain's spend moves between the two calls here, which is exactly what a
// second read would answer differently, and the repeat is still that task and
// not the same command refused as a different one.
func TestRunContinuesARepeatIsIdempotent(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	args := []string{"run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != chainUSD-chainSpent {
		t.Fatalf("overrides = %+v, want the chain's remaining %v dollars", o, chainUSD-chainSpent)
	}
	// The chain spends 1.5 dollars more: a fresh read would leave 1.5 where the
	// stored task says 3.
	seedChainSpend(t, f, chainRun, 3.5)
	if out, _, err := execute(t, args...); err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("the repeat of a --continues launch: %v, output:\n%s", err, out)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != chainUSD-chainSpent {
		t.Fatalf("overrides = %+v after the repeat, want the stored remaining untouched", o)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	// The reuse is not a licence to drop the guard: a repeat naming another
	// figure is a different task, as a repeated --pr follow-up's is.
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "--max-usd", "2", "A task"); err == nil || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v, want a repeat naming another limit refused as a different task", err)
	}
}

// TestRunFollowUpInheritsRemaining: a --pr follow-up is part of the chain of
// the pull request it continues (D1052), so it inherits the remaining and not
// the limit its previous run was launched with. A follow-up that starts from
// the full cap is the hole this closes: the chain could spend its whole budget
// again at every restart.
func TestRunFollowUpInheritsRemaining(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", chainFu, "A task"); err != nil {
		t.Fatal(err)
	}
	o := chainTask(t, f, chainFu).Overrides
	if o.MaxUSD == nil || *o.MaxUSD != chainUSD-chainSpent {
		t.Fatalf("overrides = %+v, want the chain's remaining %v dollars", o, chainUSD-chainSpent)
	}
	if got := chainTask(t, f, chainFu).PreviousRun; got != chainRun {
		t.Fatalf("previous_run = %q, want the chain's previous run %s", got, chainRun)
	}
}

// TestRunFollowUpMergesItsLimitUnderTheRemaining: a follow-up's own --max-* is
// merged under the inherited remaining, tighten-only (D1052): a figure the
// remaining can still pay for is what the launch writes, and one above it is
// refused rather than written down to the remaining, which would be a number
// the caller never typed (D1043).
func TestRunFollowUpMergesItsLimitUnderTheRemaining(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	// The refusal is asked for first: a follow-up that launched is a run the
	// PR is waiting on, and a second --pr launch on it is refused for that
	// reason rather than for the one this test is about.
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", wallRunIDAt(0, "130001", "c3c3"),
		"--max-usd", "4", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a --max-usd above the chain's remaining refused", err)
	}
	for _, want := range []string{"--max-usd 4 is above what is left of this chain's dollar limit", "2 dollars of 5 dollars used", "Pass 3 dollars or less"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", chainFu, "--max-usd", "2", "A task"); err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != 2 {
		t.Fatalf("overrides = %+v, want the caller's own 2 dollars (under the 3 left)", o)
	}
}

// TestReviewJobJoinsThePRChain: a review job on PR N joins that pull request's
// chain by its own --pr link and does not open a chain of its own (D1058) — the
// chain's limits are what bound a review-fix loop, and the review's task
// carries the remaining like any other launch on the PR. A review job has no
// previous run of its own to inherit from, so the link is the PR's runs, found
// the way a follow-up's is.
func TestReviewJobJoinsThePRChain(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--recipe", "review-only", "--run-id", chainFu); err != nil {
		t.Fatal(err)
	}
	spec := chainTask(t, f, chainFu)
	if len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7 {
		t.Fatalf("review_prs = %v, want the review of PR 7 this test launched", spec.ReviewPRs)
	}
	if o := spec.Overrides; o.MaxUSD == nil || *o.MaxUSD != chainUSD-chainSpent {
		t.Fatalf("overrides = %+v, want the PR chain's remaining %v dollars", o, chainUSD-chainSpent)
	}
}

// TestRunRefusesAMalformedUsedFigure: the four --used-* flags are parsed once,
// before anything of the launch exists, so a mistyped figure costs no read and
// leaves nothing behind. An empty --continues is refused the same way.
func TestRunRefusesAMalformedUsedFigure(t *testing.T) {
	f := chainRig(t)
	for _, tc := range []struct{ args, want []string }{
		{[]string{"--used-usd", "three"}, []string{"--used-usd \"three\"", "number of dollars"}},
		{[]string{"--used-usd", "-1"}, []string{"--used-usd -1", "zero or more dollars"}},
		{[]string{"--used-tokens", "many"}, []string{"--used-tokens \"many\"", "whole tokens"}},
		{[]string{"--used-time", "soon"}, []string{"--used-time \"soon\"", "duration"}},
		{[]string{"--used-review-rounds", "two"}, []string{"--used-review-rounds \"two\"", "whole rounds"}},
	} {
		args := append([]string{"run", "--repo", limitsRepo, "--run-id", chainFu}, append(tc.args, "A task")...)
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("%v: err = %v, want a user error", tc.args, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%v: err = %v, want it to name %q", tc.args, err, want)
			}
		}
	}
	wantRefused(t, f, "--continues: use the run to continue", "run", "--repo", limitsRepo, "--continues", "", "A task")
}

// TestRunPRWithNoChainNamesContinues: when --pr finds no run, the run the
// caller means is one that opened nothing — a halted run whose chain has no
// pull request to link through — and --continues is the way to it (F9). The
// message names it, so a caller who reached for --pr is told what to do
// instead rather than only that the pull request is not Fugaro's.
func TestRunPRWithNoChainNamesContinues(t *testing.T) {
	f := chainRig(t)
	wantRefused(t, f, "continue it with fugaro run --continues", "run", "--repo", limitsRepo, "--pr", "9", "A task")
}

// seedTaskIn stores a task for a run of another repository, which is what a
// --continues naming a run of one refuses (a chain is one repository's runs).
func seedTaskIn(t *testing.T, f *cloudFixture, slug, id string) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := runstore.Open(b, slug, id).CreateTask(ctx, firstRunSpec(id)); err != nil {
		t.Fatal(err)
	}
}
