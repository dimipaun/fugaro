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

// TestRunRefusesAContinuesOfARunInAPRChain: --continues is the link of a chain
// with no pull request — a halted run that opened nothing (D1045, spec §3) —
// and a chain that has a pull request is continued by naming the pull request,
// because that is the link that puts the launch *in* the chain. A launch that
// named the run instead inherits what is left of the chain without joining it:
// the PR's own runs are found through the PR and link back through follow_up,
// so a launcher reading the chain from the PR would not pass through this run
// and would not count what it spends — the next run of the pull request would
// be handed the same remaining, which is the split T4 closes.
func TestRunRefusesAContinuesOfARunInAPRChain(t *testing.T) {
	t.Run("a run that opened the pull request", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		wantRefused(t, f, "that run is part of PR #7's chain",
			"run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task")
	})
	t.Run("a run that continues one", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		// A run in the chain that opened nothing itself: the link is the record's
		// follow_up, which the runner writes at bootstrap, where pr is written at
		// finalize once the pull request exists — so a chain read for the pull
		// request a --continues named sees this run through the follow-up alone.
		seedSpec(t, f, followUpSpec(chainFu, chainRun, chainRun, 7), false)
		rec := prRecord(chainFu, "", 7, chainSpent)
		rec.Branch, rec.StartedAt, rec.PR = "fugaro/"+chainRun, time.Now().Add(-24*time.Hour), nil
		rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: chainRun}
		writeRecord(t, f, chainFu, rec)
		wantRefused(t, f, "that run is part of PR #7's chain",
			"run", "--repo", limitsRepo, "--continues", chainFu, "--run-id", wallRunIDAt(0, "140000", "d4d4"), "A task")
	})
	t.Run("a run that only launched", func(t *testing.T) {
		f := chainRig(t)
		// The record is written as the run starts, so a --pr follow-up that was
		// launched and never started has its stored task alone — and the task is
		// what names the pull request it was launched for. Read from the record
		// alone this run is a chain with no pull request, and the launch would be
		// handed the chain's limit in full while not being in the chain.
		seedSpec(t, f, followUpSpec(chainFu, chainRun, chainRun, 7), false)
		wantRefused(t, f, "that run is part of PR #7's chain",
			"run", "--repo", limitsRepo, "--continues", chainFu, "--run-id", wallRunIDAt(0, "140000", "d4d4"), "A task")
	})
	t.Run("a review job's run", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		// A review job joins its pull request's chain by ReviewPRs (D1058), and
		// its record carries neither a pr nor a follow_up: the stored task is the
		// only thing that says which pull request it is in.
		if _, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--recipe", "review-only", "--run-id", chainFu); err != nil {
			t.Fatal(err)
		}
		launched := len(f.run.Executions())
		_, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainFu, "--run-id", wallRunIDAt(0, "140000", "d4d4"), "A task")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "that run is part of PR #7's chain") {
			t.Fatalf("err = %v, want the continues of a review run refused as a pull request's chain", err)
		}
		if n := len(f.run.Executions()); n != launched {
			t.Fatalf("%d executions, want the review run's %d alone", n, launched)
		}
	})
}

// TestRunBeginningAChainReportsNoUnmeasuredDimension: the note that a chain
// does not measure one of its dimensions is about a chain this launch read, and
// a launch that begins one read none — its own --max-* IS the chain's limit
// (D1041), so no dimension of it was measured by anything. Printed there it
// states a chain that is not behind the launch and a cause that is not there:
// "the budget is off" is read out of an installation running the budget in
// enforce mode. The figures are right either way; only the report was wrong.
func TestRunBeginningAChainReportsNoUnmeasuredDimension(t *testing.T) {
	f := chainRig(t)
	// --used-usd is what reaches the write for a launch that begins a chain
	// (chainRemaining returns early without one), and --max-tokens is a
	// dimension whose limit only this launch's own flag sets.
	_, errOut, err := execute(t, "run", "--repo", limitsRepo, "--run-id", chainFu,
		"--max-usd", "4", "--max-tokens", "500000", "--used-usd", "1", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "does not measure") {
		t.Fatalf("stderr = %q, want no unmeasured-dimension note for a launch that begins a chain", errOut)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != 3 {
		t.Fatalf("overrides = %+v, want the 4 dollars this launch asked for less the 1 it stated as used", o)
	}
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

// TestRunContinuesWithAnUnmeasuredLedgerAndNoCapReportsIt: with the budget on, a
// chain-limited dimension the read could not measure is never read as a zero —
// a missing ledger read as free restores the chain's whole budget, which is
// cheaper than forging one (F14) — and it is not a refusal of the read either,
// which D1049 rules out: the rollover prunes an old chain's ledger node eight
// days after its last share while the record stays, so a ledger that cannot be
// read is the ordinary case for an old chain rather than a failure. D1049
// charges an unreadable ledger the per-run cap, so the dimension is left
// unmeasured only where this installation sets no cap to charge it with — the
// ordinary case for tokens, whose cap is optional policy with no database
// counterpart — and there the dimension is *reported*: the note on stderr names
// the run whose ledger could not be read and the per-run cap that would charge
// it, and the launch proceeds with the chain's own limit standing for this run,
// since nothing read says what the chain had left.
//
// What the launch may not do is count that silence as a spend: the chain is
// charged the runs the read could establish and nothing else, and a --used-*
// under that figure is refused (D1048). One above it is the charge, which is the
// only way a caller can tighten a dimension the engine could not measure.
func TestRunContinuesWithAnUnmeasuredLedgerAndNoCapReportsIt(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	limitsImage(t, f.cloudFixture)
	limitsCheckout(t)
	// A chain that limits the token dimension alone, whose run has no ledger
	// node at all: the dollars of the chain are not limited, so nothing else
	// answers this launch.
	tokens := int64(900_000)
	seedChainRun(t, f.cloudFixture, chainRun, chainSpent, task.Overrides{MaxTokens: &tokens})
	_, errOut, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task")
	if err != nil {
		t.Fatal(err)
	}
	// The chain's own limit stands for this run: the read measured nothing and
	// charged nothing, and a run bounded by no figure at all is the hole D1052
	// closes.
	if o := chainTask(t, f.cloudFixture, chainFu).Overrides; o.MaxTokens == nil || *o.MaxTokens != 900_000 {
		t.Fatalf("overrides = %+v, want the chain's own 900000 tokens standing for a run nothing measured", o)
	}
	// The note says why the figure is unknown, names the run whose ledger could
	// not be read and names the per-run cap that would charge it, so a reader is
	// not left to take the chain's limit for a spend somebody measured.
	for _, want := range []string{
		"does not measure its token",
		task.ShortRun(chainRun),
		"no ledger node",
		"budget.max_run_tokens",
		"spend past what the chain had left",
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr = %q, want it to name %q", errOut, want)
		}
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the continuation launched: one unreadable ledger is not a stopped chain", n)
	}
	// A stated figure is the charge, so it tightens what the run may spend: the
	// chain holds 800000 of its 900000 tokens used by the caller's own account,
	// and what is left is what this launch writes. A second run id, since the
	// launch above already started chainFu.
	second := wallRunIDAt(0, "140000", "d4d4")
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", second,
		"--used-tokens", "800000", "A task"); err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f.cloudFixture, second).Overrides; o.MaxTokens == nil || *o.MaxTokens != 100_000 {
		t.Fatalf("overrides = %+v, want the 100000 tokens the caller's own figure leaves", o)
	}
}

// TestRunContinuesWithAPerRunTokenCapChargedTheCap: the same chain with the
// installation's own per-run cap set, which is the figure D1049 charges an
// unreadable ledger instead of leaving the dimension unmeasured. The chain is
// charged the cap for the run nobody could read and what its readable runs hold
// for the rest — the most that run may have been holding — so it reads 100000 of
// its 900000 tokens used and can still pay for a continuation.
func TestRunContinuesWithAPerRunTokenCapChargedTheCap(t *testing.T) {
	f := newLaunchBudgetWith(t, "enforce", "max_run_tokens: 100000")
	limitsImage(t, f.cloudFixture)
	limitsCheckout(t)
	tokens := int64(900_000)
	seedChainRun(t, f.cloudFixture, chainRun, chainSpent, task.Overrides{MaxTokens: &tokens})
	// The run's ledger node is absent, so it is charged the installation's cap
	// and the chain reads 100000 of its 900000 tokens used.
	_, errOut, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task")
	if err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f.cloudFixture, chainFu).Overrides; o.MaxTokens == nil || *o.MaxTokens != 800_000 {
		t.Fatalf("overrides = %+v, want the 800000 tokens the chain has left once the unreadable run is charged its cap", o)
	}
	if strings.Contains(errOut, "does not measure") {
		t.Fatalf("stderr = %q, want no unmeasured-dimension note: the cap charges the run instead", errOut)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the continuation launched", n)
	}
}

// TestRunRefusesAUsedFigureBelowWhatTheChainCouldRead: a dimension the engine
// could not measure still holds a floor — the sum over the runs it could read —
// and a --used-* under it is refused like any other understatement (D1048). The
// chain here limits 450000 tokens, the run that began it holds 500000 in the
// database and the run in front of it has no ledger node at all: the read
// measures nothing, but it did read 500000, and a caller stating 100000 would
// take the charge below a figure the database itself holds — the whole remaining
// handed over by the back door, which is what T1 and F14 forbid.
func TestRunRefusesAUsedFigureBelowWhatTheChainCouldRead(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	limitsImage(t, f.cloudFixture)
	limitsCheckout(t)
	root, tip := chainRun, wallRunIDAt(1, "090000", "e5e5")
	limit := int64(450_000)
	seedChainRun(t, f.cloudFixture, root, chainSpent, task.Overrides{MaxTokens: &limit})
	// The run in front of it: a chain with no pull request, so it carries the
	// link and no PR, and its own ledger node is the one the database lacks.
	spec := firstRunSpec(tip)
	seedSpec(t, f.cloudFixture, spec, false)
	rec := prRecord(tip, "", 0, chainSpent)
	rec.Branch, rec.PR = "fugaro/"+root, nil
	rec.FollowUp = &runstore.FollowUp{PreviousRun: root}
	writeRecord(t, f.cloudFixture, tip, rec)
	f.db.Set(budget.PathRun(appSlug, root), budget.RunLedger{Tokens: 500_000})
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", tip, "--run-id", chainFu,
		"--used-tokens", "100000", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a --used-tokens under the 500000 the read holds refused", err)
	}
	for _, want := range []string{"--used-tokens 100000", "below what the engine could read", "500000 tokens"} {
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
	// A --used-* figure is the one flag that guard cannot read: the stored task
	// holds the remaining the charge went into (chainRemaining), so a repeat that
	// states a figure has nothing to be compared against — and the four --max-*
	// are compared exactly because a caller who typed a number has to be told it
	// did not take effect rather than find a looser task (D1043). Dropped in
	// silence, the same repeat reports the run as already launched while the
	// figure never applied, which is the failure the launch that wrote the task
	// refuses for a dimension the chain does not limit (D1048).
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "--used-usd", "4", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a repeat that states what the chain has used refused", err)
	}
	for _, want := range []string{"already holds a task, and a repeat cannot state what the chain has used", "run ID that has not launched"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != chainUSD-chainSpent {
		t.Fatalf("overrides = %+v after the refusal, want the stored remaining untouched", o)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the one the first launch started", n)
	}
}

// TestRunRefusesAStoredTaskAContinuesDoesNotResolve: a --continues task records
// no link of its own — task.Validate holds branch, pr and previous_run
// together, and a chain with no pull request carries none of them — so which
// chain a stored task was written for is not in it, and "run ID X already holds
// this launch" is decided from the text, the batch and the figures alone
// (storedRepeat). For a launch that already ran, that is the whole answer: the
// stored task is the run's own and the second call reports the existing run.
// For a run ID that never launched it is not, because the stored task is what
// the run would read: a task written by another launch shape (a first run, or a
// --continues of another chain) carries figures this chain never resolved, and
// starting it drops this chain's remaining in silence — the run is bounded by
// the per-run caps alone, which is the hole D1045/D1052 close for every other
// shape. Held to the chain the caller names, such a task is refused.
func TestRunRefusesAStoredTaskAContinuesDoesNotResolve(t *testing.T) {
	// The launch under test in every case: continue chainRun's chain, under the
	// run ID chainFu, with the text the stored task carries.
	args := []string{"run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task"}
	// stored is a task chainFu already holds, written by a launch of some other
	// shape: a first run's task, whose own launch carried no chain at all.
	stored := func(o task.Overrides) *task.Spec {
		spec := firstRunSpec(chainFu)
		spec.Task = "A task"
		spec.Overrides = o
		return spec
	}
	t.Run("a stored first run's task", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		seedSpec(t, f, stored(task.Overrides{}), false)
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already holds a task") {
			t.Fatalf("err = %v, want a stored task this launch does not resolve to refused", err)
		}
		for _, want := range []string{"--continues " + task.ShortRun(chainRun), "--max-usd", "3 dollars"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to name %q", err, want)
			}
		}
		if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD != nil {
			t.Fatalf("overrides = %+v after the refusal, want the stored task untouched", o)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want the stored task not started", n)
		}
	})
	t.Run("a stored task carrying the chain's limit and not its remaining", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		seedSpec(t, f, stored(task.Overrides{MaxUSD: &usd}), false)
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "5 where the chain leaves 3 dollars") {
			t.Fatalf("err = %v, want the whole limit named against the remaining", err)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want the stored task not started", n)
		}
	})
	t.Run("a stored task the chain has since spent past", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		rem := chainUSD - chainSpent
		seedSpec(t, f, stored(task.Overrides{MaxUSD: &rem}), false)
		// The chain spends 1.5 more: the task an earlier attempt of this same
		// launch stored holds 3, and the chain leaves 1.5. Started as it stands
		// the run would spend past the chain, so the repeat is refused and names
		// the way to start that task deliberately (--retry, which takes no flag
		// for the chain it is in and resolves the chain at its own launch).
		seedChainSpend(t, f, chainRun, 3.5)
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "3 where the chain leaves 1.5 dollars") {
			t.Fatalf("err = %v, want the stored remaining named against what the chain leaves", err)
		}
		if !strings.Contains(err.Error(), "--retry "+task.ShortRun(chainFu)) {
			t.Fatalf("err = %v, want it to name --retry of the stored task", err)
		}
		if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != rem {
			t.Fatalf("overrides = %+v after the refusal, want the stored task untouched", o)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want the stored task not started", n)
		}
	})
	t.Run("a stored task tighter than the chain leaves", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		// The same shape one notch tighter — the launch that wrote it named
		// --max-usd 2, which a launch continuing a chain may do (tighten-only,
		// D1043) — and it is a bound this chain still pays for, so the repeat
		// starts the task as it stands. The check is the bound, not equality: a
		// figure below the remaining was asked for and must not be refused as one
		// above it would be.
		tight := 2.0
		seedSpec(t, f, stored(task.Overrides{MaxUSD: &tight}), false)
		if _, _, err := execute(t, args...); err != nil {
			t.Fatal(err)
		}
		if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != tight {
			t.Fatalf("overrides = %+v, want the stored 2 dollars kept", o)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the stored task started once", n)
		}
	})
	t.Run("the repeat of an attempt that never started", func(t *testing.T) {
		f := chainRig(t)
		usd := chainUSD
		seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
		// A launch of this same command whose run never started — the task is
		// stored, the record and launch.json are not — is the repeat the reuse
		// exists for, and the figure it holds is one this chain still pays for:
		// the run starts with exactly the task that attempt stored.
		rem := chainUSD - chainSpent
		seedSpec(t, f, stored(task.Overrides{MaxUSD: &rem}), false)
		if _, _, err := execute(t, args...); err != nil {
			t.Fatal(err)
		}
		if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != rem {
			t.Fatalf("overrides = %+v, want the stored remaining %v kept", o, rem)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Fatalf("%d executions, want the stored task started once", n)
		}
	})
}

// TestRunRefusesARepeatOfAUsedFigureOnAFollowUp: the same rule on the --pr
// path, which is the other launch shape a repeat answers from the stored task
// (storedFollowUp). The figure is not the task's to hold there either, so a
// repeat that states one is refused rather than answered as the launch it
// repeats — the run the pull request is waiting on would be reported launched
// with a charge nobody applied.
func TestRunRefusesARepeatOfAUsedFigureOnAFollowUp(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", chainFu, "A task"); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", chainFu, "--used-usd", "4", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "a repeat cannot state what the chain has used") {
		t.Fatalf("err = %v, want a follow-up's repeat that states a --used-* figure refused", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
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

// TestReviewJobRepeatIsIdempotent: the figures a review job writes are the
// chain's at the moment it ran, so a repeat of the same --run-id is answered
// from the stored task rather than from a chain read made now — the rule a
// --continues launch's repeat follows (TestRunContinuesARepeatIsIdempotent),
// and the one a review job needs most, since a review-fix loop relaunches it.
// The chain spends more between the two calls here, which is exactly what a
// second read would answer differently, and the repeat is still that task and
// not the same command refused as a different one.
func TestReviewJobRepeatIsIdempotent(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	args := []string{"run", "--repo", limitsRepo, "--pr", "7", "--recipe", "review-only", "--run-id", chainFu}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != chainUSD-chainSpent {
		t.Fatalf("overrides = %+v, want the PR chain's remaining %v dollars", o, chainUSD-chainSpent)
	}
	// The chain spends 1.5 dollars more: a fresh read would leave 1.5 where the
	// stored task says 3.
	seedChainSpend(t, f, chainRun, 3.5)
	if out, _, err := execute(t, args...); err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("the repeat of a review launch: %v, output:\n%s", err, out)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != chainUSD-chainSpent {
		t.Fatalf("overrides = %+v after the repeat, want the stored remaining untouched", o)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// TestRunFollowUpCarriesThePreviousRunWhenTheChainHasAGap: a run's record and
// its stored task age out together, so a chain older than the window the
// bucket keeps has a hop with nothing under it — and a follow-up on such a pull
// request is a launch this CLI has always accepted (TestRunPRRootExpired,
// resolvePR). What it may not do is carry nothing (D1052): the chain's own
// limit is the run that began it, which is on the far side of the gap, but the
// previous run's stored task holds what the chain left *it*, and the chain has
// only spent more since — so that figure bounds this run instead of no figure
// at all.
func TestRunFollowUpCarriesThePreviousRunWhenTheChainHasAGap(t *testing.T) {
	f := chainRig(t)
	gone, fu1 := wallRunIDAt(95, "090000", "aaaa"), wallRunIDAt(1, "090000", "bbbb")
	usd, left := chainUSD, 3.0
	// The run that began the chain: its stored task is all the bucket still
	// holds of it, and its record is the hop the read stops at.
	spec := firstRunSpec(gone)
	spec.Overrides = task.Overrides{MaxUSD: &usd}
	seedSpec(t, f, spec, false)
	// The run this follow-up continues, which pushed and holds the chain's
	// remaining as the chain left it.
	spec = followUpSpec(fu1, gone, gone, 7)
	spec.Overrides = task.Overrides{MaxUSD: &left}
	seedSpec(t, f, spec, false)
	rec := prRecord(fu1, "", 7, chainSpent)
	rec.Branch, rec.StartedAt = "fugaro/"+gone, time.Now().Add(-24*time.Hour)
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: gone}
	writeRecord(t, f, fu1, rec)
	_, errOut, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", chainFu, "A task")
	if err != nil {
		t.Fatal(err)
	}
	if o := chainTask(t, f, chainFu).Overrides; o.MaxUSD == nil || *o.MaxUSD != left {
		t.Fatalf("overrides = %+v, want the %v dollars the previous run of the chain holds", o, left)
	}
	if got := chainTask(t, f, chainFu).PreviousRun; got != fu1 {
		t.Fatalf("previous_run = %q, want %s", got, fu1)
	}
	if !strings.Contains(errOut, "gap") {
		t.Fatalf("stderr %q, want the gap the chain's read stopped at named", errOut)
	}
}

// TestRunRefusesAChainGapWithNothingToCarry: a gap is answered by what the
// previous run's stored task holds — a bound on what the chain has left, since
// the chain has only spent more since — and where that object is gone as well
// there is no figure anywhere: not the chain's own limit, which is the run
// behind the hop, and not the previous run's remaining, which went with its
// task. A launch bounded by no figure at all is the hole D1052 closes, so this
// one stops and names the missing run rather than starting a run the chain does
// not bound (F21, T5). A --pr follow-up cannot reach this: its previous run's
// task is what names the base branch, so it is gone by the refusal at
// followup.go's own check. A review job's chain is read from the PR's runs
// without one, which is the shape below.
func TestRunRefusesAChainGapWithNothingToCarry(t *testing.T) {
	f := chainRig(t)
	gone, fu1 := wallRunIDAt(95, "090000", "aaaa"), wallRunIDAt(1, "090000", "bbbb")
	// The newest run to push to PR 7: its link names a run the bucket has
	// nothing under, and its own stored task is gone too.
	rec := prRecord(fu1, "", 7, chainSpent)
	rec.Branch, rec.StartedAt = "fugaro/"+gone, time.Now().Add(-24*time.Hour)
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: gone}
	writeRecord(t, f, fu1, rec)
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--recipe", "review-only", "--run-id", chainFu)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a gap with nothing left to carry refused", err)
	}
	for _, want := range []string{"PR #7", "cannot be read", gone, "no stored task"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesAChainItCannotRead: a failed chain read that is not a gap — a
// link that is not a run id, a record that cannot be read, the budget database
// unreachable — leaves the chain's figure unknown for a reason no other record
// answers, and a launch that carried on would run past a limit nothing
// established. Only the missing record degrades (ErrChainGap); everything else
// stops the launch.
func TestRunRefusesAChainItCannotRead(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	// The record of the run this follow-up continues, whose link is not a run
	// id at all: the walk cannot get past it.
	rec := prRecord(chainRun, "", 7, chainSpent)
	rec.Branch, rec.StartedAt = "fugaro/"+chainRun, time.Now().Add(-24*time.Hour)
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: "not-a-run-id"}
	writeRecord(t, f, chainRun, rec)
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", chainFu, "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a chain the records cannot confirm refused", err)
	}
	for _, want := range []string{"the chain behind this launch cannot be read", "is not a run id"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestReviewJobRepeatNamesItsOwnPullRequest: the review PRs are the one
// identity of a review launch that the flag comparison cannot see — a review
// task keeps them in review_prs and carries no pr of its own, while the field
// a repeat is held to (pr) is 0 for every one of them (storedRepeat). A repeat
// that tried to reuse PR 7's stored review against `--pr 8` would be answered
// with that task: the review of a pull request this call never named, reported
// as the launch it asked for, and — where that task was written but never
// launched — started as PR 7's review. The list is this call's own or the
// repeat is a different task.
func TestReviewJobRepeatNamesItsOwnPullRequest(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainPRRoot(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	if _, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--recipe", "review-only", "--run-id", chainFu); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "8", "--recipe", "review-only", "--run-id", chainFu)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v, want a repeat naming another pull request refused as a different task", err)
	}
	if spec := chainTask(t, f, chainFu); len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7 {
		t.Fatalf("review_prs = %v, want the stored review of PR 7 untouched", spec.ReviewPRs)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	// The repeat of the call that wrote the task is still that task: the check
	// above is the pull request's, not a refusal of the review path's reuse.
	if out, _, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--recipe", "review-only", "--run-id", chainFu); err != nil || !strings.Contains(out, "already launched") {
		t.Fatalf("the repeat of the review launch: %v, output:\n%s", err, out)
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
