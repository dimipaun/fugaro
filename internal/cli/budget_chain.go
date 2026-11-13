package cli

// fugaro budget chain raise (docs/run-limit-carry-over L9, D1061, F22, spec §4):
// the supported path a launcher takes to raise a chain that has spent its limit.
//
// A chain's limit is a launcher's own number, held in the stored task of the run
// that began the chain (D1041, D1046) — there is no database node, no cap and no
// policy key for it — so raising an exhausted chain is a rewrite of that stored
// task with the reason for the raise recorded. This command writes exactly the
// two objects a launcher already holds (F21: `roles/storage.objectUser` on the
// runs/ prefix): the root run's task.json with the raised figures, and its
// result.json with the entry that says who raised it, when, why and from what to
// what (runstore.ChainRaise).
//
// Nothing of the project's budget moves for it. The per-run cap and the daily
// and project caps are the database's own rules (spec §4) and a raise touches
// none of them; what it moves is the one figure that rides a launch's task and
// merges as the last tighten-only layer of the run it starts (D1059). The design
// states that plainly rather than pretending otherwise: a launcher can rewrite
// that stored task by hand, so the rule is a convention this installation states
// and not a bound the platform enforces (spec §9), and what this command is, is
// the honest path — the figures, the refusals, and the reason recorded where a
// reader finds it.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// chainRaiseOptions is one raise: the run it is asked about, the four --max-*
// flags as the caller typed them, and the reason.
type chainRaiseOptions struct {
	cloud  cloudOptions
	repo   string
	reason string
	// The four chain limits, the flags' own text, parsed by chainRaiseFigures
	// in the dimensions' own units — the same four flags a launch writes the
	// chain's limit with, so a caller raises a chain with the words it began
	// it with (D1042).
	maxUSD, maxTokens, maxTime, maxReviewRounds string
}

func newBudgetChainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chain",
		Short: "Raise the limit of a chain that has spent it",
		Long: "Work on one chain's own limit: the figure the launch that began the chain\n" +
			"set, which bounds that run and every run that continues it (D1041, D1045).\n\n" +
			"A chain's limit lives in the stored task of the run that began it and in no\n" +
			"database node, so what this family does is rewrite that stored task with the\n" +
			"reason recorded; nothing of the project's budget moves for it.",
	}
	cmd.AddCommand(newBudgetChainRaiseCmd())
	return cmd
}

func newBudgetChainRaiseCmd() *cobra.Command {
	var o chainRaiseOptions
	cmd := &cobra.Command{
		Use:   "raise RUN (--max-usd N | --max-tokens N | --max-time D | --max-review-rounds N) --reason TEXT",
		Short: "Raise a chain that has spent its limit, recording who, when, why and the figures",
		Long: "Raise the limit of the chain RUN (a run ID, <repo-slug>/<run-id>, or the last\n" +
			"four or more characters of a run ID) belongs to, by rewriting the stored task\n" +
			"of the run that began the chain, which is the one place a chain's limit lives\n" +
			"(D1041, D1046).\n\n" +
			"A chain has no database node and no cap of its own, so a chain that has spent\n" +
			"its figure is not stopped by anything: the launch that would continue it is\n" +
			"refused for having nothing left to spend, and this is the command that answers\n" +
			"that refusal. Each --max-* flag names a dimension the chain already limits and\n" +
			"a figure above the one in force — a raise moves a limit up, and nothing here\n" +
			"lowers one, since asking for less is what a launch's own --max-* is for. The\n" +
			"reason is required and is recorded beside the figures, in the record of the\n" +
			"run that began the chain: a chain limit is a convention this installation\n" +
			"states rather than a bound the platform enforces, so who raised it, when and\n" +
			"why is the whole of what tells a raise from a rewritten limit.\n\n" +
			"The project's own budget is untouched: the per-run cap and the daily and\n" +
			"project caps are the database's rules, and a raise moves none of them.\n\n" +
			"The run that began the chain has to have finished. A run writes its own\n" +
			"result.json while it is going, so an entry recorded in one still in flight\n" +
			"would be lost under the run's next write; a raise is refused while that run\n" +
			"has not ended, and nothing is written for it.\n\n" +
			"Only the runs bucket is written, through the access a launcher already has:\n" +
			"no budget database, no cap and no kill switch is read or written.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runChainRaise(cmd, &o, args[0]) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository RUN belongs to (owner/name, or a path inside its checkout; needed only for a bare run ID)")
	f.StringVar(&o.maxUSD, "max-usd", "", "the chain's dollar limit, above the one in force, such as 6 or 12.5")
	f.StringVar(&o.maxTokens, "max-tokens", "", "the chain's token limit, above the one in force, such as 1200000")
	f.StringVar(&o.maxTime, "max-time", "", "the chain's wall-time limit, above the one in force, as a duration such as 90m or 3h")
	f.StringVar(&o.maxReviewRounds, "max-review-rounds", "", "the chain's review-round limit, above the one in force, such as 6")
	f.StringVar(&o.reason, "reason", "", "why the chain is being raised (required; recorded with who, when and the figures)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runChainRaise(cmd *cobra.Command, o *chainRaiseOptions, ref string) error {
	ctx := cmd.Context()
	// This run's account and its log (D1352), under the name a person typed:
	// the raise is a write like `budget set` and `budget kill` beside it, so
	// it says what it did in one step and keeps that step.
	rep, sink := reporterFor(cmd, "budget-chain-raise", nil)
	if sink != nil {
		defer func() { _ = sink.Close() }()
	}
	defer rep.Summary()
	// The caller's own words first, before any read: a mistyped figure or a
	// missing reason costs no bucket access and leaves nothing behind.
	figures, err := chainRaiseFigures(cmd, o)
	if err != nil {
		return err
	}
	reason, err := chainRaiseReason(o.reason)
	if err != nil {
		return err
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	if o.repo, err = repoArg(ctx, o.cloud, o.repo); err != nil {
		return err
	}
	s, err := artifactsRun(ctx, env, ref, o.repo, resolveRunOpts(cmd, false))
	if err != nil {
		return err
	}
	slug := s.Slug()
	// The chain's limit is the root launch's own figure, so the run to rewrite
	// is the run that began the chain and never the one the caller named
	// (D1046): a mid-chain run carries what its launch was left with, and
	// rewriting that would bound the next launch by a figure the chain's own
	// launch never set.
	root, rootSpec, rootRec, err := chainRaiseTarget(ctx, env, s)
	if err != nil {
		return err
	}
	raised := make([]runstore.ChainRaiseLimit, 0, len(figures))
	next := *rootSpec
	for _, d := range chainDimensions() {
		to, named := figures[d]
		if !named {
			continue
		}
		from, limited := chainLimitAmount(rootSpec.Overrides, d)
		if !limited {
			return userErr("--max-%s: the chain run %s began bounds no %s spend, so there is no chain %s limit to raise. A chain's limit is what its own launch asked for, and this command raises the figures already set: relaunch the chain with --max-%s to bound it",
				chainDimensionFlag(d), task.ShortRun(root), chainDimensionWord(d), chainDimensionWord(d), chainDimensionFlag(d))
		}
		if to <= from {
			return userErr("--max-%s %s is not above this chain's %s limit (%s): a raise moves a limit up, and nothing here lowers one — the launch that continues the chain asks for less with its own --max-%s. Pass more than %s, or leave the flag out",
				chainDimensionFlag(d), chainFlagAmount(d, to), chainDimensionWord(d), chainAmountText(d, from),
				chainDimensionFlag(d), chainFlagAmount(d, from))
		}
		if err := setChainLimit(&next.Overrides, d, to); err != nil {
			return err
		}
		raised = append(raised, runstore.ChainRaiseLimit{Dimension: d, From: from, To: to})
	}

	// Who is last, and it is the launcher's own account (localcfg's user, else
	// git's): the raise is this person's, and a refusal here — an installation
	// that can tell neither — costs no write.
	me, err := env.lc.Me(ctx)
	if err != nil {
		return userErr("cannot record who is raising this chain: %v", err)
	}

	// The entry first, then the task. The record is what a later reader has,
	// and a task rewritten with no entry beside it would be a raise nothing
	// explains; an entry whose task write then fails is a raise the command
	// reported as failed and whose figures the task's own bytes contradict —
	// which is the direction worth failing in, since the alternative loses the
	// reason silently.
	entry := runstore.ChainRaise{By: me, At: time.Now().UTC(), Reason: reason, Limits: raised}
	rootRec.ChainRaises = append(rootRec.ChainRaises, entry)
	if err := s.Sibling(root).WriteRecord(ctx, rootRec); err != nil {
		return remote(fmt.Errorf("recording the raise in runs/%s/%s/result.json: %w", slug, root, err))
	}
	if err := s.Sibling(root).WriteTask(ctx, &next); err != nil {
		return remote(fmt.Errorf("the raise is recorded in runs/%s/%s/result.json, but rewriting runs/%s/%s/task.json failed: %w", slug, root, slug, root, err))
	}

	// The step is the write, and the lines under it are what it wrote: the
	// figures in the dimension's own words, the reason the record keeps, and
	// the command that reads both back (runs show's details view, which is
	// where a person copies a whole run ID from, D1705).
	tip := task.ShortRun(s.RunID())
	rep.Step("chain").Done(fmt.Sprintf("raised the chain of run %s, begun by %s", tip, task.ShortRun(root)))
	out := sayThrough(rep, cmd.OutOrStdout())
	for _, l := range raised {
		fmt.Fprintf(out, "  %s: %s -> %s\n", chainDimensionWord(l.Dimension), chainAmountText(l.Dimension, l.From), chainAmountText(l.Dimension, l.To))
	}
	fmt.Fprintf(out, "reason: %s\n", reason)
	fmt.Fprintf(out, "the next launch that continues this chain carries what is left of it; `fugaro runs show %s --repo %s` reads the new figures and the record the raise is in\n",
		tip, quoteWord(slug))
	return nil
}

// chainRaiseTarget is the run a raise rewrites and the two objects it is read
// and written through: the run that began the chain (D1046), its stored task —
// where the limit is — and its record, where the raise is recorded.
//
// A chain read that fails is refused rather than guessed at: the run to rewrite
// is exactly what a failed read could not say, and the figures a raise must be
// above are in that run's task alone. Every refusal here writes nothing.
func chainRaiseTarget(ctx context.Context, env *cloudEnv, s *runstore.Store) (string, *task.Spec, *runstore.Record, error) {
	root, err := chainRoot(ctx, s)
	if err != nil {
		if errors.Is(err, runstore.ErrChainGap) {
			// The records stop at a hop the bucket no longer holds, so the run
			// that began the chain is behind it and its stored task — the
			// chain's limit — cannot be found. A raise would have to name the
			// run to rewrite, and naming the oldest run the link still reaches
			// would rewrite a figure the chain never set (D1046).
			return "", nil, nil, userErr("run %s/%s: the chain it is in cannot be read (%v), so the run whose stored task holds the chain's limit is unknown, and nothing was written. Raise the figure where the chain is whole, or launch a run that begins a new chain",
				s.Slug(), task.ShortRun(s.RunID()), err)
		}
		return "", nil, nil, userErr("run %s/%s: the chain it is in cannot be read (%v), so the run whose stored task holds the chain's limit is unknown, and nothing was written",
			s.Slug(), task.ShortRun(s.RunID()), err)
	}
	spec, err := readTask(ctx, env, s.Slug(), root)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		// The bucket ages a run's objects out together, and a chain whose root
		// is older than that window has no stored task left: there is no figure
		// to raise and none to compare a new one against.
		return "", nil, nil, userErr("run %s began this chain and its stored task is gone from the bucket, so the chain's own limit cannot be read and there is nothing to raise. A chain whose root is older than the bucket's window is bounded by the per-run caps alone",
			task.ShortRun(root))
	case err != nil:
		return "", nil, nil, err
	}
	rec, err := s.Sibling(root).ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		// A run writes result.json as it starts, so the two ways here are a run
		// that never started and one the bucket has aged out. Neither has a
		// record to record the raise in, and creating one would race the runner
		// of a run that is about to start (it treats an existing record as
		// another execution's).
		return "", nil, nil, userErr("run %s, which began this chain, has no result.json yet, and that is where a raise is recorded: raise the chain once that run has a record, or launch the chain's next run from a run that does",
			task.ShortRun(root))
	case err != nil:
		return "", nil, nil, remote(err)
	}
	if rec.FinishedAt == nil {
		// A run in flight owns its result.json: it writes the whole record from
		// its own snapshot at every stage boundary, so an entry written into it
		// now is gone at the run's next write.
		return "", nil, nil, userErr("run %s began this chain and is still running: it writes its own result.json, so a raise recorded in it now would be lost under the run's next write. Nothing was written — raise the chain once that run has finished",
			task.ShortRun(root))
	}
	return root, spec, rec, nil
}

// chainRoot is the run that began the chain run s belongs to: the link followed
// one record at a time, and nothing else read (runstore.ResolveChain with no
// ledger).
//
// A raise needs the root's id and no figure of what the chain has spent — the
// figures it moves are the ones that run's launch stored — so the budget
// database is not opened for it, and the project's published policy is not read
// either. That is one fewer thing that can refuse a raise, and one fewer read of
// a database this command never writes.
func chainRoot(ctx context.Context, s *runstore.Store) (string, error) {
	c, err := runstore.ResolveChain(ctx, s, nil, runstore.PerRunCaps{}, time.Now())
	if err != nil {
		return "", err
	}
	return c.Root, nil
}

// chainRaiseFigures parses the four --max-* flags a raise carries, each into the
// dimension's own unit, and refuses a raise that names none: the flags are what
// this command moves, and a command that wrote the task as it stands would file
// an entry for a raise nothing moved.
func chainRaiseFigures(cmd *cobra.Command, o *chainRaiseOptions) (map[runstore.Dimension]runstore.Amount, error) {
	f := cmd.Flags()
	figures := make(map[runstore.Dimension]runstore.Amount, 4)
	if f.Changed("max-usd") {
		v, err := strconv.ParseFloat(o.maxUSD, 64)
		if err != nil {
			return nil, userErr("--max-usd %q must be a number of dollars, such as 6 or 12.5", o.maxUSD)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return nil, userErr("--max-usd %s must be greater than 0", o.maxUSD)
		}
		micros, err := pricing.FromUSD(v)
		if err != nil {
			return nil, userErr("--max-usd %v: %v", v, err)
		}
		figures[runstore.USD] = runstore.Amount(micros)
	}
	if f.Changed("max-tokens") {
		v, err := strconv.ParseInt(o.maxTokens, 10, 64)
		if err != nil || v <= 0 {
			return nil, userErr("--max-tokens %q must be a whole number of tokens greater than 0, such as 1200000", o.maxTokens)
		}
		figures[runstore.Tokens] = runstore.Amount(v)
	}
	if f.Changed("max-time") {
		d, err := time.ParseDuration(o.maxTime)
		if err != nil || d <= 0 {
			return nil, userErr("--max-time %q must be greater than 0, as a duration such as 90m or 3h", o.maxTime)
		}
		figures[runstore.Time] = runstore.Amount(d.Seconds())
	}
	if f.Changed("max-review-rounds") {
		n, err := strconv.Atoi(o.maxReviewRounds)
		if err != nil || n <= 0 {
			return nil, userErr("--max-review-rounds %q must be a whole number of rounds greater than 0, such as 6", o.maxReviewRounds)
		}
		figures[runstore.ReviewRounds] = runstore.Amount(n)
	}
	if len(figures) == 0 {
		return nil, userErr("name the figure to raise: --max-usd, --max-tokens, --max-time or --max-review-rounds")
	}
	return figures, nil
}

// chainRaiseReason is why the chain is being raised, which a raise never goes
// without: the reason is what the record keeps, and a limit raised with no
// reason beside it is the rewritten figure the convention exists to tell apart
// from a raise (spec §4). It is bounded like a kill switch's reason, which is
// the other launcher-written sentence the record keeps.
func chainRaiseReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", userErr("a raise is recorded with the reason it was made for, and --reason is empty. Say why the chain is being raised: --reason TEXT")
	}
	if clipped := clipRunes(reason, maxKillReason); clipped != reason {
		return "", userErr("--reason is longer than the %d characters a recorded reason holds; say it in one line, or keep the rest of the explanation where the work is tracked", maxKillReason)
	}
	return reason, nil
}
