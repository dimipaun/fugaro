package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is the chain half of a launch: what a launch that does not begin a
// chain carries (docs/run-limit-carry-over L4, D1045, D1048, D1049, D1052,
// D1058) — the --continues and --used-* flags, the launcher's read of what the
// chain has already spent, and the remaining each dimension is written as.
//
// The four --max-* flags and the tighten-only merge of L3 stay in run.go and
// launch_budget.go. What lives here is what only a launch that continues a
// chain needs: the link it names, the chain's own limit (in the stored task of
// the run that began it, D1041/D1046) and the figures the budget database's
// per-run ledgers hold for the runs behind it.

// chainDimensions is the four dimensions, in the order every message about
// them reads: dollars, tokens, wall time, review rounds.
func chainDimensions() []runstore.Dimension {
	return []runstore.Dimension{runstore.USD, runstore.Tokens, runstore.Time, runstore.ReviewRounds}
}

// chainUsedFigures is the four --used-* flags, parsed: what the caller states
// the chain has already used. A caller may only raise the engine's own figure
// with one (D1048) — a caller who could state a smaller one could state zero
// and start the chain fresh (T1) — so each figure is a further tightening of
// the launch that follows, never a value taken as given.
type chainUsedFigures struct {
	usd    *float64
	tokens *int64
	time   *time.Duration
	rounds *int
}

// set reports whether the caller stated any of the four.
func (c chainUsedFigures) set() bool {
	return c.usd != nil || c.tokens != nil || c.time != nil || c.rounds != nil
}

// addChainContinueFlags registers the flags of a launch that continues a chain:
// --continues (D1045) and the four --used-* figures (D1048). They are
// registered from newRunCmd, whose own flag list stays the four --max-* ones
// and the text of the launch.
func addChainContinueFlags(cmd *cobra.Command, o *runOptions) {
	f := cmd.Flags()
	f.StringVar(&o.continues, "continues", "", "continue the chain of RUN (a run ID, or <repo-slug>/<run-id>): this launch inherits the chain's remaining limits, for a chain with no pull request — the run you name opened nothing to follow up. The chain's limit is read from the run that began it, and what it has spent from the chain's runs' budget ledgers; this launch's own --max-* may only tighten the remaining. Refused with --pr, --retry and --attempts, which name a chain's previous run themselves")
	f.StringVar(&o.usedUSD, "used-usd", "", "state that the chain has already used X dollars, such as 3 or 2.5: it may only raise what the engine read from the chain's runs (fugaro refuses a figure below it, naming both), which is how a caller accounts for spend the chain's own runs do not hold")
	f.StringVar(&o.usedTokens, "used-tokens", "", "state that the chain has already used N tokens, such as 400000: tighten-only like --used-usd, and the one figure a chain whose budget is off reads for the token dimension, which no run record carries")
	f.StringVar(&o.usedTime, "used-time", "", "state that the chain has already used this much wall time, as a duration such as 45m: tighten-only like --used-usd")
	f.StringVar(&o.usedReviewRounds, "used-review-rounds", "", "state that the chain has already spent N review rounds, such as 2: tighten-only like --used-usd")
}

// chainContinueFlag parses --continues and the four --used-* flags, once, for
// every launch shape that may carry them. The syntax is refused here — before
// openCloud, as the four --max-* ones are — so a mistyped number costs no read
// and leaves nothing behind. Whether a figure is above or below what the chain
// already used is the other refusal, and it needs the chain read
// (chainInherit).
func chainContinueFlag(cmd *cobra.Command, o *runOptions) error {
	f := cmd.Flags()
	if f.Changed("continues") && o.continues == "" {
		return userErr("--continues: use the run to continue, as a run ID or <repo-slug>/<run-id>")
	}
	if f.Changed("used-usd") {
		v, err := strconv.ParseFloat(o.usedUSD, 64)
		if err != nil {
			return userErr("--used-usd %q must be a number of dollars, such as 3 or 2.5", o.usedUSD)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return userErr("--used-usd %s must be zero or more dollars", o.usedUSD)
		}
		o.used.usd = &v
	}
	if f.Changed("used-tokens") {
		v, err := strconv.ParseInt(o.usedTokens, 10, 64)
		if err != nil || v < 0 {
			return userErr("--used-tokens %q must be zero or more whole tokens, such as 400000", o.usedTokens)
		}
		o.used.tokens = &v
	}
	if f.Changed("used-time") {
		d, err := time.ParseDuration(o.usedTime)
		if err != nil || d < 0 {
			return userErr("--used-time %q must be zero or more, as a duration such as 45m", o.usedTime)
		}
		o.used.time = &d
	}
	if f.Changed("used-review-rounds") {
		n, err := strconv.Atoi(o.usedReviewRounds)
		if err != nil || n < 0 {
			return userErr("--used-review-rounds %q must be zero or more whole rounds, such as 2", o.usedReviewRounds)
		}
		o.used.rounds = &n
	}
	return nil
}

// chainInherit is what a launch that continues a chain does that a launch
// beginning one does not (D1045, D1047, D1052, D1058): it resolves the link the
// launch names, reads the chain's own limit off the run that began it and what
// the chain has spent off its runs' ledgers (runstore.ResolveChain), and writes
// each dimension's remaining into this launch's task — so the run the launcher
// starts is bounded by what is left of the chain and not by its whole limit
// again (the hole D1052 closes).
//
// It runs after chainLimitGate, which wrote this launch's own --max-* figures
// into the task: those are the *request*, merged under the policy (D1043), and
// what this call writes is that request held under the chain's remaining.
//
// A launch that begins a chain — a first run, each attempt of a batch — has no
// link and no chain to read: its own figures are the chain's limit and the
// engine has read nothing, so the only thing this call acts on is a caller's
// --used-*, which is then what the chain is charged for work that is not its
// own. A launch the launcher reuses as it stands (a repeated --run-id of a
// stored follow-up) writes nothing here either: its limits were resolved when
// its task was written. --retry never reaches this file: the stored task is
// written once, so it takes no flag for the chain it is in (taskFlags) and the
// chain's remaining is computed at the retry's own launch, in the job (L5,
// D1053).
func chainInherit(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec, reused bool, warn io.Writer) error {
	if reused || o.retry != "" {
		return nil
	}
	if warn == nil {
		warn = io.Discard
	}
	from, err := chainFrom(ctx, env, slug, o, spec, warn)
	if err != nil {
		return err
	}
	var behind chainBehind
	switch {
	case from == "":
		behind.limit = spec.Overrides
	default:
		// A chain read that fails is this launch's to answer, and the errors are
		// three. A link the bucket no longer holds anything under (ErrChainGap) is
		// the bucket's own lifecycle: a run's record and its stored task age out
		// together, so a chain older than the window has a hop with nothing under
		// it, and a follow-up on such a pull request is a launch this CLI has
		// always accepted (TestRunPRRootExpired, resolvePR). Every other failed
		// read — a record that cannot be read, the budget database unreachable, a
		// link that is not a run id — leaves the chain's figure unknown for a
		// reason no other object answers, and a launch that carried on would run
		// past a limit nothing established: it is refused, for every link. D1049
		// is the same rule stated for the ledger, where the figure a failed read
		// is charged is the cap: one read that failed is not a chain of nothing.
		//
		// A run the caller *named* with --continues is refused on all three, and
		// T4 is explicit about it: a link the records cannot confirm is refused
		// rather than answered as a chain of the named run alone, which would be
		// the figure a gap leaves behind. resolvePR's link is the launcher's own
		// finding, not a caller's claim, so a gap in it degrades instead —
		// loudly, and by what the previous run's stored task still holds.
		//
		// A gap's read answers a chain as well as its error: the runs the link
		// still reaches, with the hop charged the per-run cap and the dimensions a
		// gap truncated left unmeasured (ResolveChain). What this launch carries
		// below is the previous run's remaining instead, and that figure already
		// counts every run of the chain up to that run — the runs after the hop
		// among them — so charging the hop here as well would take its spend off
		// twice and refuse launches the chain can still pay for. The read's
		// figures are for a caller with nothing else to bound the chain by; this
		// one has the stored task, or it has nothing and refuses.
		c, err := chainRead(ctx, env, slug, from)
		if err != nil {
			if o.continues != "" {
				return userErr("--continues %s: the chain cannot be read (%v), so what it has already spent cannot be established, and a link the records cannot confirm is refused rather than continued from the run named alone. Name a run whose chain is whole, or launch a run that is not part of it", task.ShortRun(o.continues), err)
			}
			if !errors.Is(err, runstore.ErrChainGap) {
				return userErr("%s: the chain behind this launch cannot be read (%v), so how much of its limits is left cannot be established, and a run launched without that figure would not be bounded by the chain at all. Nothing was launched: retry, and if the records stay unreadable, launch a run that is not part of this chain", chainFromWord(o, spec), err)
			}
			// The previous run's stored task holds what the chain left it, which
			// bounds what is left now: the chain has only spent more since. A
			// follow-up that carried nothing here would be bounded by the per-run
			// caps alone, which is the hole D1052 closes for every other launch
			// shape (chainRemaining's gap arm writes the figures).
			prev, perr := readTask(ctx, env, slug, from)
			switch {
			case errors.Is(perr, runstore.ErrNotFound):
				// Neither object of the run this chain is read from is left, so
				// there is no figure anywhere — not the chain's own limit, which
				// is behind the hop, and not the previous run's remaining, which
				// went with its task — and a run launched with the limits this
				// launch wrote would spend past a chain nothing bounded. Nothing
				// was launched: this is the one gap a launcher has no answer for.
				return userErr("%s: the chain behind this launch cannot be read (%v), and the run it is read from has no stored task left either, so no figure anywhere bounds what the chain has left. Nothing was launched: name a run whose chain is whole, or launch a run that is not part of this chain", chainFromWord(o, spec), err)
			case perr != nil:
				return perr
			default:
				behind.limit, behind.gap = prev.Overrides, true
				fmt.Fprintf(warn, "warning: the chain behind this launch has a gap (%v), so what it has already spent cannot be read; this run carries what the previous run of the chain was left with, and not the chain's whole limit or nothing\n", err)
			}
			break
		}
		behind.chain = c
		root, err := readTask(ctx, env, slug, behind.chain.Root)
		switch {
		case errors.Is(err, runstore.ErrNotFound):
			// The run that began the chain has no stored task, so the chain
			// carries no limit at all: nothing is inherited, and what bounds this
			// launch is what it asked for itself.
		case err != nil:
			return err
		default:
			behind.limit = root.Overrides
		}
	}

	next, err := chainRemaining(behind, spec, o, warn)
	if err != nil {
		return err
	}
	// Written last: a refused launch leaves the stored task untouched, and a
	// repeated --run-id of one that never launched compares the figures this call
	// resolved rather than a half-written set.
	spec.Overrides = next
	return nil
}

// recheckContinues holds a stored task a --continues repeat would start to the
// chain the caller names, the way recheckFollowUp holds a reused follow-up to
// its pull request — the other half of the same check, for the one launch shape
// whose stored task carries no link to re-read.
//
// A --continues task records no link of its own: task.Validate holds branch, pr
// and previous_run together, and a chain with no pull request has none of them,
// so what a stored task was written for is in no field of it, and "this run ID
// already holds this launch" is decided from the text, the batch and the figures
// alone (storedRepeat). For a run ID that has already launched that is the whole
// answer: the stored task is the run's own, and the second call reports the
// existing run (design §4). A run ID that never launched is where it is not: the
// stored task is what the run would read, so a task written by another launch
// shape — a first run's, or a --continues of another chain, neither of which the
// task names — carries figures this chain never resolved, and starting it would
// bound the run by them rather than by what the chain leaves, which is what
// D1045 and D1052 exist to write.
//
// What is asked of the stored task is the bound it puts on the run: every
// dimension the chain limits must carry a figure at or below the remaining, and
// one above it — or none at all, which bounds nothing — is a run the chain does
// not bound. The remaining is read by resolving a copy of the stored task whose
// own figures are cleared first: the stored figures are the request the check
// judges, and asking with them would have chainRemaining refuse a figure the
// chain has since spent past with its advice to pass a smaller --max-*, which is
// no advice for a call that typed no figure. A stored figure the chain can still
// pay for is left alone — it was this launch's own --max-* when it was written,
// tighten-only (D1043) — and the task in the bucket is never rewritten: a stored
// task is written once (createTask).
func recheckContinues(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec) error {
	next := *spec
	next.Overrides.MaxUSD, next.Overrides.MaxTokens = nil, nil
	next.Overrides.MaxTime, next.Overrides.MaxReviewRounds = "", nil
	if err := chainInherit(ctx, env, o, slug, &next, false, io.Discard); err != nil {
		return err
	}
	var loose []string
	for _, d := range chainDimensions() {
		rem, limited := chainLimitAmount(next.Overrides, d)
		if !limited {
			continue
		}
		have, carried := chainLimitAmount(spec.Overrides, d)
		switch {
		case !carried:
			loose = append(loose, fmt.Sprintf("it carries no --max-%s where the chain leaves %s",
				chainDimensionFlag(d), chainAmountText(d, rem)))
		case have > rem:
			loose = append(loose, fmt.Sprintf("it carries --max-%s %s where the chain leaves %s",
				chainDimensionFlag(d), chainFlagAmount(d, have), chainAmountText(d, rem)))
		}
	}
	if len(loose) == 0 {
		return nil
	}
	return userErr("run ID %s already holds a task that %s does not resolve to: %s. A stored task is what its run reads, so starting it would bound the run by figures the chain does not leave it. State this launch on a run ID that has not launched, or start the stored task as it stands with --retry %s",
		spec.RunID, chainFromWord(o, spec), strings.Join(loose, "; "), task.ShortRun(spec.RunID))
}

// chainFrom is the run this launch's chain is read from (D1045): the run
// --continues names, the previous run a --pr follow-up stores in its task, or
// the newest pushed run of the single pull request a review job reviews
// (D1058). "" is a launch that begins a chain: a first run, or an attempt of a
// batch, neither of which names a previous run.
func chainFrom(ctx context.Context, env *cloudEnv, slug string, o *runOptions, spec *task.Spec, warn io.Writer) (string, error) {
	switch {
	case o.continues != "":
		// The named run is what a chain with no pull request has (D1045): a
		// halted run that opened nothing is continued by naming it. A name the
		// records cannot confirm is refused rather than resolved to a chain of
		// its own (T4) — locateRun answers ErrNotFound for a run the bucket has
		// no task for, and ErrAmbiguous for an ending several repositories
		// carry — and a run of another repository is refused here, because a
		// chain is one repository's runs and its figures would be another's.
		other, id, err := locateRun(ctx, env, o.continues)
		if err != nil {
			return "", err
		}
		if other != slug {
			return "", userErr("--continues %s: that run is in repository %s, and this launch is for %s; a chain is one repository's runs, so name a run of this one", o.continues, other, slug)
		}
		// A run that is part of a pull request's chain is continued by naming the
		// pull request: --pr is the link that puts this launch in that chain, and
		// one that named the run instead would inherit the chain's remaining
		// *without being in it* — the PR's own runs link through follow_up, and a
		// launcher that reads the chain from the PR would not pass through this
		// run, so what it spends would not count against the limit the next one
		// reads. That is the split T4 closes, and it is the one the flag's own
		// text names: the run --continues is for a chain with no pull request, a
		// halted run that opened nothing.
		pr, err := continuesPRChain(ctx, env, slug, id)
		if err != nil {
			return "", err
		}
		if pr != 0 {
			return "", userErr("--continues %s: that run is part of PR #%d's chain, and a chain with a pull request is continued by naming the pull request — this launch would inherit what is left of that chain without being in it, so the next run of PR #%d would not count what this one spends. Continue the pull request instead: --pr %d",
				task.ShortRun(o.continues), pr, pr, pr)
		}
		return id, nil
	case spec.PreviousRun != "":
		return spec.PreviousRun, nil
	case len(spec.ReviewPRs) == 1:
		return reviewChainFrom(ctx, env, slug, spec.ReviewPRs[0], warn)
	}
	return "", nil
}

// continuesPRChain is the pull request the run --continues names is part of: the
// one it opened (Record.PR, the runner's own write at finalize), the one it
// continues (Record.FollowUp.PR, a --pr follow-up's link at bootstrap) or the
// one it reviews (Spec.ReviewPRs, the review mode's own link into that chain,
// D1058), and 0 for a run that is in no pull request's chain — the chain
// --continues is for, a halted run that opened nothing (D1045).
//
// Both objects a run is known by are read, because either can be the only one
// that holds the link. A run the bucket holds no record for never started — the
// record is written as a run starts — while its stored task was written by the
// launch and names the pull request it was launched for, so a --pr follow-up
// that was launched and never started would read as a chain with no pull request
// and be handed that chain's limit in full while not being in it: the launch T4
// refuses, arriving through the one gap in the record. The stored task is the
// other way round the only one for a review run, whose record carries no pr and
// no follow_up at all. A run with neither object never launched, so it opened
// nothing, follows nothing and reviews nothing: it reads as 0, which is the run
// a chain with no pull request is continued from.
func continuesPRChain(ctx context.Context, env *cloudEnv, slug, id string) (int, error) {
	rec, err := runstore.Open(env.bucket.Bucket, slug, id).ReadRecord(ctx)
	switch {
	case err == nil:
		if rec.PR != nil && rec.PR.Number != 0 {
			return rec.PR.Number, nil
		}
		if rec.FollowUp != nil && rec.FollowUp.PR != 0 {
			return rec.FollowUp.PR, nil
		}
	case !errors.Is(err, runstore.ErrNotFound):
		return 0, remote(err)
	}
	spec, err := readTask(ctx, env, slug, id)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return 0, nil
	case err != nil:
		return 0, err
	}
	if spec.PR != 0 {
		return spec.PR, nil
	}
	// One pull request, the length chainFrom reads a review launch's chain from:
	// a review of several is in no single pull request's chain.
	if len(spec.ReviewPRs) == 1 {
		return spec.ReviewPRs[0], nil
	}
	return 0, nil
}

// reviewChainFrom is the run a review job's chain is read from (D1058): the
// newest run that pushed to the pull request it reviews, which is the run whose
// chain the review is part of. It is found the way resolvePR finds a
// follow-up's previous run — the PR's own rows, the runs that pushed among
// them, newest first — and "" when the pull request has no such run, where the
// review job has no chain to join and keeps the figures it was launched with.
//
// A review job reviews several pull requests under the pick recipe: several
// pull requests are several units of work with no one chain between them, so a
// review of more than one joins none (chainFrom asks for one).
func reviewChainFrom(ctx context.Context, env *cloudEnv, slug string, pr int, warn io.Writer) (string, error) {
	now := time.Now()
	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{slug}, since: now.Add(-followUpLookback), pr: pr, warn: warn, noCache: true}, now)
	if err != nil {
		return "", err
	}
	// A review job never pushes, so it is not in these rows itself; what is left
	// is the PR's own runs, and only one that pushed is the run the chain was
	// opened by (resolvePR reads the same field for the same reason).
	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		return !r.Pushed || (r.TaskPR != pr && !task.BranchRE.MatchString(r.Branch))
	})
	if len(rows) == 0 {
		return "", nil
	}
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := startOf(b).Compare(startOf(a)); c != 0 {
			return c
		}
		return strings.Compare(b.RunID, a.RunID)
	})
	return rows[0].RunID, nil
}

// chainRead is the engine's read of the chain behind from (D1046, D1047): the
// runs the link names, one record at a time, and what each of them has spent —
// the money and tokens off the budget database's per-run ledger, the wall time
// and review rounds off the records.
func chainRead(ctx context.Context, env *cloudEnv, slug, from string) (*runstore.Chain, error) {
	ledgers, err := launcherLedgers(ctx, env)
	if err != nil {
		return nil, err
	}
	return runstore.ResolveChain(ctx, runstore.Open(env.bucket.Bucket, slug, from), ledgers, chainPerRunCaps(ctx, env), time.Now())
}

// launcherLedgers is the ledger reader a chain read speaks with: the launcher's
// own viewer role, which reads every path of the budget database (L1, U4). nil
// is an installation whose budget is off, where there is no ledger at all and
// the chain is UNENFORCED with its records as its figures (D1046).
//
// budgetBackendOn is deliberately not the test here: it also refuses a launch
// whose installation names no token_signer, and that signer is the identity the
// *run* is minted with, not the one this read uses — a chain read is nothing
// the run's missing identity should stop, and the launch's own gates refuse
// that launch a few lines later anyway.
func launcherLedgers(ctx context.Context, env *cloudEnv) (runstore.LedgerReader, error) {
	lc := env.lc
	if lc.BudgetMode() == localcfg.BudgetOff || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, nil
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, err
	}
	return chainLedgerDB{db: db}, nil
}

// chainLedgerDB is the budget database's named read (L1) as the chain read's
// interface: one get per run, by name, never a listing, and the two sides'
// ledger shapes with nothing converted between them but the three figures a
// chain counts (the money is pricing.Micros on both).
type chainLedgerDB struct{ db *rtdb.Client }

func (l chainLedgerDB) ReadLedger(ctx context.Context, slug, run string) (runstore.Ledger, bool, error) {
	got, found, err := budget.LedgerDB{DB: l.db}.ReadLedger(ctx, slug, run)
	return runstore.Ledger{Reserved: got.Reserved, Released: got.Released, Tokens: got.Tokens}, found, err
}

// chainPerRunCaps is what one run of this chain whose ledger cannot be read is
// charged (D1049): the most it could have been holding, which is the
// installation's own per-run cap — its dollars and its tokens, where it sets
// one.
//
// The figure is the cap itself and not the share this launch's mode takes of it
// (modeScaledCeiling): the run that did not settle may have run any mode, and
// config.RigorBudgetCap gives each mode its own share of the dollar cap, so the
// cap is the one figure that bounds every one of them. The database's own
// per-run cap is not read: it is the tighter of the two where an installation
// sets it, and charging the policy's larger figure is the direction D1049 takes
// — a read that failed is charged what the run may have held, never less. A
// dimension the installation caps nowhere is left unmeasured by the read rather
// than charged a zero (the per-run token cap is optional policy with no
// database counterpart, so that is the ordinary case for it).
func chainPerRunCaps(ctx context.Context, env *cloudEnv) runstore.PerRunCaps {
	layer := ceilingLayer(env.lc)
	// The published node is what the owner says now, and the ceiling is the copy
	// the job was deployed with: the runner's own merge reads the node's figure
	// (H1), so a charge taken from the stale copy could be the higher of the
	// two. A read that fails leaves the deployed figure, which is what the
	// launch's own resolution uses as well (chainLimitPolicyOf).
	if p, found, err := env.policy.read(ctx, env.lc); err == nil && found && p != nil {
		if v := p.Budget.PerRunUSD; v > 0 {
			layer.PerRunUSD = v
		}
		if v := p.Budget.MaxRunTokens; v > 0 {
			layer.MaxRunTokens = v
		}
	}
	e := policy.Merge(layer)
	var caps runstore.PerRunCaps
	if e.PerRunUSD > 0 {
		if micros, err := pricing.FromUSD(e.PerRunUSD); err == nil {
			caps.USD = micros
		}
	}
	if e.MaxRunTokens > 0 {
		caps.Tokens = e.MaxRunTokens
	}
	return caps
}

// chainBehind is the chain a launch continues as chainInherit resolved it: the
// read, the chain's own limit as the run that began it stored it, and whether
// the read stopped at a gap.
type chainBehind struct {
	// chain is the read of the chain, nil for a launch that begins one.
	chain *runstore.Chain
	// limit is the chain's own limit: the root launch's stored figures, or —
	// for a first run, and for a chain whose ledger could not be read — what
	// this launch wrote itself (D1041, D1046).
	limit task.Overrides
	// gap is a chain the bucket has aged out from under: the read stopped at a
	// link it no longer holds a record for, so the chain's own limit is
	// unreadable too and limit is what the previous run's stored task was left
	// with — a bound on what is left now, since the chain has only spent more
	// since — while what it has spent goes unread (D1052, D1049).
	gap bool
}

// chainRemaining is a task's overrides as this launch's chain resolves them:
// each dimension the chain limits becomes limit − used, this launch's own
// --max-* held under that, and a caller's --used-* raising the charge or
// refused (D1047, D1048).
//
// A launch that begins a chain is the one shape with no request apart from the
// limit: its own --max-* is the chain's limit (D1041) and the remaining is
// drawn from it, so what it writes is that limit less what the caller stated
// the chain has used elsewhere (D1048). A chain read that stopped at a gap has
// the figures a different shape again: it is a chain, so this launch's --max-*
// is held under what is left, but the spend is unknown and the limit in hand is
// already a remaining — so it is carried as it stands rather than drawn afresh
// from a limit nothing read (chainBehind.gap).
func chainRemaining(b chainBehind, spec *task.Spec, o *runOptions, warn io.Writer) (task.Overrides, error) {
	next := spec.Overrides
	begins := b.chain == nil && !b.gap
	// A launch that begins a chain and states nothing to charge it with has
	// nothing to resolve: its own figures are the chain's limit and the chain
	// has used nothing, so rewriting them would only re-render what
	// chainLimitGate already wrote (a 45m as 45m0s, say) into the task the
	// run and the record then carry.
	if begins && !o.used.set() {
		return next, nil
	}
	// enforced is whether the figures are the budget database's (D1046): with
	// the budget off the chain is UNENFORCED — a report and not a stop (T10) —
	// so a spent chain warns instead of refusing. A chain this launch begins is
	// the caller's own figures, and those are always this launcher's to act on.
	enforced := b.chain != nil && b.chain.Enforced
	used := map[runstore.Dimension]runstore.Figure{}
	if b.chain != nil {
		for _, d := range chainDimensions() {
			used[d] = b.chain.Used(d)
		}
	}
	var err error
	if used, err = chainUsedRaised(used, o.used, b.limit, enforced); err != nil {
		return task.Overrides{}, err
	}
	for _, d := range chainDimensions() {
		lim, ok := chainLimitAmount(b.limit, d)
		if !ok {
			// The chain does not limit this dimension: there is nothing to
			// inherit it from, and what this launch's own flag asked for (if any)
			// stands as chainLimitGate wrote it.
			continue
		}
		u, measured := used[d], used[d].Measured
		spent := runstore.Amount(0)
		switch {
		case measured:
			spent = u.Amount
		case b.chain != nil && b.chain.Enforced:
			// A chain-limited dimension whose figure the enforced read does not
			// have, which is a run whose ledger could not be read where this
			// installation sets no per-run cap for the dimension to charge it
			// (ResolveChain leaves such a dimension unmeasured rather than
			// charging a zero, D1049) — the ordinary case for a chain the
			// rollover has pruned, and the per-run token cap an installation may
			// simply not set (PerRunCaps).
			//
			// The one figure it must not be is a zero: the chain's whole limit
			// would go to this run again, which is the hole F14 names, and the
			// contract PerRunCaps states for want of a cap is the chain's own
			// limit for the dimension — no one run of the chain can have held
			// more than that. So the charge is that limit, and never less than
			// the runs the read could read: a number, favouring the bound, and
			// never a refusal of its own, which is what D1049 rules out (one
			// failed read is not a stopped chain, and the read is what fails
			// wherever the database has pruned an old chain's ledgers). The
			// chain reads as spent with it, and the launch stops below at the arm
			// every exhausted chain stops at, naming the figures a chain that
			// really spent everything would name.
			//
			// Neither way past it is a smaller figure, which is why chainUsedRaised
			// refuses one (D1048): a --used-* can only raise a charge that already
			// holds the limit, so it can add nothing here, and the figure it states
			// is one no run of the chain backs. What does answer is the per-run cap
			// this installation does not set — with one, the unreadable run is
			// charged the cap instead of leaving the dimension unmeasured — or a
			// raise of the chain's own limit in the run that began it (D1061).
			spent = max(lim, u.Floor)
			note := fmt.Sprintf("note: the chain behind this launch does not measure its %s (%s), and this installation sets no per-run %s cap to charge the run that could not be read; the chain's own %s limit is charged in full instead, so the chain reads as spent",
				chainDimensionWord(d), chainMissText(b.chain), chainDimensionWord(d), chainDimensionWord(d))
			if key := chainPerRunCapKey(d); key != "" {
				note += fmt.Sprintf(". A per-run cap (%s) charges that run in its place, and raising the chain's limit in the run that began it is the other way past this", key)
			}
			fmt.Fprintln(warn, note)
		case b.chain != nil:
			// The chain does not measure this dimension: with the budget off
			// there is no ledger, and no run record carries a whole-run token
			// total (D1051, F8). Saying so is the report, and the chain's own
			// limit then stands for this run — never a figure the read invented,
			// and never the limit read as unspent. The other two shapes have no
			// chain to report on, and naming one would invent it: a launch that
			// begins a chain read nothing — its own --max-* IS the chain's limit
			// (D1041) and nothing measured it, while this note would name a chain
			// behind the launch and a cause (the budget is off) that an enforced
			// installation does not have — and a gap leaves the dimension
			// unmeasured because the spend could not be read, not because the
			// chain has no figure, with the previous run's remaining in hand
			// rather than the chain's own limit: the warning chainInherit printed
			// is the report there, and it is true.
			fmt.Fprintf(warn, "note: the chain behind this launch does not measure its %s (the budget is off, and a run's record carries no such figure): the chain's own %s limit stands for this run, and nothing enforces it\n",
				chainDimensionWord(d), chainDimensionWord(d))
		}
		rem := lim - spent
		// This launch's own figure is a request held under the remaining (D1052):
		// one above it is refused rather than written down to the remaining, which
		// is the rule a --max-* above the policy already answers to (D1043) — a
		// caller who typed a number has to be told it did not take effect rather
		// than find a looser one in the task. One at or below it is what the launch
		// writes, so the figure a caller typed is the limit this run carries
		// wherever the chain can still pay for it.
		//
		// A launch that begins a chain has no second figure to hold under a
		// remaining, because the one it typed IS the chain's limit and the
		// remaining is drawn from it: holding it under a remaining drawn from
		// itself would refuse every --used-* above zero, and name the caller's
		// own limit as though it were the work already done.
		spend := rem
		if req, ok := chainLimitAmount(next, d); ok && !begins {
			if req > rem {
				if b.gap {
					// The figures a gap holds are the previous run's remaining and
					// no spend at all, so the sentence below — which reports an
					// amount used against the chain's limit — would name a
					// figure nothing read.
					return task.Overrides{}, userErr("--max-%s %s is above what the previous run of this chain was left with (%s): the chain's records stop at a link the bucket no longer holds, so what it has already spent cannot be read, and this launch can carry only what that run held. Pass %s or less, or launch a run that is not part of this chain",
						chainDimensionFlag(d), chainFlagAmount(d, req), chainAmountText(d, rem), chainAmountText(d, rem))
				}
				return task.Overrides{}, userErr("--max-%s %s is above what is left of this chain's %s limit (%s of %s used): a launch can only tighten what a chain has left, never re-open what it spent. Pass %s or less, or raise the chain's limit where it is set",
					chainDimensionFlag(d), chainFlagAmount(d, req), chainDimensionWord(d), chainAmountText(d, spent), chainAmountText(d, lim), chainAmountText(d, rem))
			}
			spend = req
		}
		if rem <= 0 {
			if begins {
				return task.Overrides{}, userErr("--used-%s %s leaves nothing of this launch's --max-%s %s: the chain would begin with nothing to spend. Lower the figure, or raise the limit",
					chainDimensionFlag(d), chainAmountText(d, spent), chainDimensionFlag(d), chainAmountText(d, lim))
			}
			if b.gap {
				// Neither of the two below is true here: the budget need not be
				// off, and the chain's own limit is exactly what the gap left
				// unread, so naming it (or the root's stored figure) would quote
				// a number this launch never saw.
				return task.Overrides{}, userErr("%s: the previous run of this chain was left with nothing of its %s limit, and the chain's records stop at a link the bucket no longer holds, so continuing it would start a run with nothing to spend. Raise the limit in the run that began the chain, or launch a run that is not part of it",
					chainFromWord(o, spec), chainDimensionWord(d))
			}
			if !enforced {
				fmt.Fprintf(warn, "warning: this chain has nothing left of its %s limit (%s of %s used); the budget is off, so nothing stops this run from spending past it\n",
					chainDimensionWord(d), chainAmountText(d, spent), chainAmountText(d, lim))
				continue
			}
			return task.Overrides{}, userErr("%s: this chain has nothing left of its %s limit (%s of %s used), so continuing it would start a run with nothing to spend. Raise the limit in the run that began the chain (%s), or launch a run that is not part of it",
				chainFromWord(o, spec), chainDimensionWord(d), chainAmountText(d, spent), chainAmountText(d, lim), b.chain.Root)
		}
		if err := setChainLimit(&next, d, spend); err != nil {
			return task.Overrides{}, err
		}
	}
	return next, nil
}

// chainMissText is the runs of a chain whose money could not be read, as the
// note that charges the chain's own limit for them names them — the run and
// which of the three ways the read failed, so a caller knows what to look at
// rather than only that a figure is missing (L1, D1049). Charged is never empty
// where this is asked for: a dimension is unmeasured under an enforced chain
// only because a charge above left it so (ResolveChain), and a budget-off chain
// reports the dimension in a note of its own instead.
func chainMissText(c *runstore.Chain) string {
	var parts []string
	for _, m := range c.Charged {
		run := task.ShortRun(m.Run)
		switch {
		case errors.Is(m.Err, runstore.ErrChainGap):
			parts = append(parts, fmt.Sprintf("the record of run %s is gone, so the runs behind it cannot be read", run))
		case m.Err != nil:
			parts = append(parts, fmt.Sprintf("the ledger of run %s could not be read (%v)", run, m.Err))
		default:
			parts = append(parts, fmt.Sprintf("the budget database has no ledger node for run %s", run))
		}
	}
	return strings.Join(parts, ", and ")
}

// chainUsedRaised is the engine's figures with the caller's --used-* applied
// (D1048): each one raises its dimension's charge, and one below what the engine
// holds is refused, naming both figures — a caller who could lower a figure could
// state zero and start the chain fresh (T1).
//
// What the engine holds is the figure's Floor: what it measured where it
// measured the dimension, and the sum over the runs it could still read where it
// did not, which is a figure the caller's own number may not understate either.
// A dimension the enforced chain does not measure is charged the chain's own
// limit for it (chainRemaining), so under an enforced chain that limit is the
// floor as well: a --used-* below it would take back a charge the launch is
// already holding, which is the hole F14 names, and one at or above it changes
// nothing the chain can pay for. Such a figure is refused rather than applied.
//
// A dimension the engine does not measure and no enforced chain charges — the
// token dimension of a chain whose budget is off (D1051) — has no figure to
// raise, and there the caller's own number is the only one there is: it is
// accepted, which is the honest use of the knob in a chain that is a report and
// not a stop (T10).
//
// A --used-* for a dimension the chain does not limit is refused, and the
// chain's limit is the only one that counts: what the figure raises is the
// charge a chain counts against its own limit (D1048), and this launch's
// --max-* bounds the run instead — the per-run cap, not a figure the chain
// holds. Accepting one there would either report a charge nothing carries, or
// be dropped by the write that fills in each limited dimension.
func chainUsedRaised(used map[runstore.Dimension]runstore.Figure, u chainUsedFigures, limit task.Overrides, enforced bool) (map[runstore.Dimension]runstore.Figure, error) {
	stated := map[runstore.Dimension]runstore.Amount{}
	if v := u.usd; v != nil {
		micros, err := pricing.FromUSD(*v)
		if err != nil {
			return nil, userErr("--used-usd %v: %v", *v, err)
		}
		stated[runstore.USD] = runstore.Amount(micros)
	}
	if v := u.tokens; v != nil {
		stated[runstore.Tokens] = runstore.Amount(*v)
	}
	if v := u.time; v != nil {
		stated[runstore.Time] = runstore.Amount(v.Seconds())
	}
	if v := u.rounds; v != nil {
		stated[runstore.ReviewRounds] = runstore.Amount(*v)
	}
	for _, d := range chainDimensions() {
		amount, ok := stated[d]
		if !ok {
			continue
		}
		lim, limited := chainLimitAmount(limit, d)
		if !limited {
			return nil, userErr("--used-%s needs a chain limit: the chain this launch is in does not bound its %s spend, so there is no limit for the figure to be charged against. A launch's own --max-%s caps the run and not the chain; leave the flag out, or begin a chain that sets --max-%s",
				chainDimensionFlag(d), chainDimensionWord(d), chainDimensionFlag(d), chainDimensionFlag(d))
		}
		have := used[d]
		// max(Amount, Floor) is the engine's own figure however the dimension
		// came back: a measured figure is at or above its floor, and an
		// unmeasured one carries the floor alone with no amount at all.
		floor := max(have.Amount, have.Floor)
		if !have.Measured && enforced {
			floor = max(floor, lim)
		}
		if amount < floor {
			if have.Measured {
				return nil, userErr("--used-%s %s is below what the engine read for this chain (%s): a caller may only raise what the chain has already used, never lower it. Pass %s or more, or leave the flag out",
					chainDimensionFlag(d), chainAmountText(d, amount), chainAmountText(d, have.Amount), chainAmountText(d, have.Amount))
			}
			return nil, userErr("--used-%s %s is below the charge this chain already holds (%s): the chain's %s spend could not be measured, so the charge is the runs the read could reach and, where the chain limits the dimension, the chain's own %s limit — a charged bound and not a spend somebody read, and a caller may only raise a charge, never lower one. Pass %s or more, or leave the flag out",
				chainDimensionFlag(d), chainAmountText(d, amount), chainAmountText(d, floor), chainDimensionWord(d), chainDimensionWord(d), chainAmountText(d, floor))
		}
		used[d] = runstore.Figure{Amount: max(amount, floor), Floor: floor, Measured: true}
	}
	return used, nil
}

// chainPerRunCapKey is the setting one dimension's per-run cap comes from, as
// the note that charges a chain's own limit for want of one names it (D1049):
// the installation's own two figures, which are what chainPerRunCaps reads. The
// record-backed dimensions have no per-run cap at all — nothing charges them but
// the records — which is why only a gap leaves one of them unmeasured, and
// nothing here is named for them.
func chainPerRunCapKey(d runstore.Dimension) string {
	switch d {
	case runstore.USD:
		return "budget.per_run_usd"
	case runstore.Tokens:
		return "budget.max_run_tokens"
	}
	return ""
}

// chainFromWord is how a refusal names the link that put this launch in the
// chain: the flag the caller typed, or the pull request whose run it continues.
func chainFromWord(o *runOptions, spec *task.Spec) string {
	switch {
	case o.continues != "":
		return "--continues " + task.ShortRun(o.continues)
	case spec.PreviousRun != "":
		return "PR #" + strconv.Itoa(spec.PR)
	case len(spec.ReviewPRs) == 1:
		return "PR #" + strconv.Itoa(spec.ReviewPRs[0])
	}
	return "this chain"
}

// chainDimensionWord names one dimension the way a message about it reads.
func chainDimensionWord(d runstore.Dimension) string {
	switch d {
	case runstore.USD:
		return "dollar"
	case runstore.Tokens:
		return "token"
	case runstore.Time:
		return "wall-time"
	case runstore.ReviewRounds:
		return "review-round"
	}
	return string(d)
}

// chainDimensionFlag is the flag suffix one dimension is named by: "usd" for
// --max-usd, --used-usd and the rest.
func chainDimensionFlag(d runstore.Dimension) string {
	switch d {
	case runstore.USD:
		return "usd"
	case runstore.Tokens:
		return "tokens"
	case runstore.Time:
		return "time"
	case runstore.ReviewRounds:
		return "review-rounds"
	}
	return string(d)
}

// chainAmountText is one dimension's amount in its own unit, as a message reads
// it: dollars to the cent, whole tokens, a duration for the wall time and a
// count for the rounds.
func chainAmountText(d runstore.Dimension, a runstore.Amount) string {
	switch d {
	case runstore.USD:
		usd := strconv.FormatFloat(pricing.Micros(a).USD(), 'f', -1, 64)
		if a == 1_000_000 {
			return usd + " dollar"
		}
		return usd + " dollars"
	case runstore.Tokens:
		if a == 1 {
			return "1 token"
		}
		return strconv.FormatInt(int64(a), 10) + " tokens"
	case runstore.Time:
		return time.Duration(math.Round(float64(a) * float64(time.Second))).String()
	case runstore.ReviewRounds:
		if a == 1 {
			return "1 round"
		}
		return strconv.Itoa(int(a)) + " rounds"
	}
	return strconv.FormatFloat(float64(a), 'f', -1, 64)
}

// chainFlagAmount is one dimension's amount as the flag that would carry it
// takes it: the figure alone, with none of chainAmountText's unit words. A
// refusal that has just named the flag is where the two read differently — the
// caller typed `--max-usd 4`, and "4 dollars" is the unit said twice — while a
// figure standing in a sentence of its own reads better with it.
func chainFlagAmount(d runstore.Dimension, a runstore.Amount) string {
	switch d {
	case runstore.USD:
		return strconv.FormatFloat(pricing.Micros(a).USD(), 'f', -1, 64)
	case runstore.Tokens, runstore.ReviewRounds:
		return strconv.FormatInt(int64(a), 10)
	case runstore.Time:
		return time.Duration(math.Round(float64(a) * float64(time.Second))).String()
	}
	return strconv.FormatFloat(float64(a), 'f', -1, 64)
}

// chainLimitAmount is one dimension's limit as a task carries it, in the
// dimension's own unit, and whether the task limits it at all (D1041: a launch
// that names none carries no chain limit). A figure a task cannot hold — a
// duration that does not parse, a dollar amount past what a Micros can be —
// reads as no limit rather than as a failure: the task was validated when it was
// written, and what this call does with it is a comparison.
func chainLimitAmount(o task.Overrides, d runstore.Dimension) (runstore.Amount, bool) {
	switch d {
	case runstore.USD:
		if o.MaxUSD == nil {
			return 0, false
		}
		micros, err := pricing.FromUSD(*o.MaxUSD)
		if err != nil {
			return 0, false
		}
		return runstore.Amount(micros), true
	case runstore.Tokens:
		if o.MaxTokens == nil {
			return 0, false
		}
		return runstore.Amount(*o.MaxTokens), true
	case runstore.Time:
		if o.MaxTime == "" {
			return 0, false
		}
		dur, err := time.ParseDuration(o.MaxTime)
		if err != nil {
			return 0, false
		}
		return runstore.Amount(dur.Seconds()), true
	case runstore.ReviewRounds:
		if o.MaxReviewRounds == nil {
			return 0, false
		}
		return runstore.Amount(*o.MaxReviewRounds), true
	}
	return 0, false
}

// setChainLimit writes one dimension's figure into a task's overrides, in that
// dimension's own unit: the four members L2 added and the runner reads (D1056).
func setChainLimit(o *task.Overrides, d runstore.Dimension, a runstore.Amount) error {
	switch d {
	case runstore.USD:
		usd := pricing.Micros(a).USD()
		o.MaxUSD = &usd
	case runstore.Tokens:
		n := int64(a)
		o.MaxTokens = &n
	case runstore.Time:
		o.MaxTime = time.Duration(math.Round(float64(a) * float64(time.Second))).String()
	case runstore.ReviewRounds:
		n := int(a)
		o.MaxReviewRounds = &n
	default:
		return fmt.Errorf("chain: %q is not a dimension", d)
	}
	return nil
}
