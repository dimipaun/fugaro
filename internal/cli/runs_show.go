package cli

// fugaro runs show and the chain block it shares with diagnose
// (docs/run-limit-carry-over L6, spec §7, D1055, D1056): for one run, the chain
// it belongs to, each dimension's limit, used and remaining, the layer each
// limit's source names, and whether the chain is enforced. A halted run's first
// question is how much is left (D1055), so diagnose prints the same block
// beside the halt — this file holds the one reading both commands make.
//
// The block is the shape L7's launch reply carries as `chain` and `limits[]`
// (D1056), so ChainLimits and ChainLimit are the two commands' one definition of
// it: a consumer that reads the document a launch answered and the document
// `runs show --json` prints is reading the same members.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// runsShowDocVersion is the version of the document `runs show --json` prints
// (schemas/runs-show.schema.json): members are only ever added within it, so a
// renamed or re-meant member is a new member under a new name.
const runsShowDocVersion = 1

// ChainLimits is one run's chain as both commands print it: the run that began
// the chain, how many runs the link reached, whether the figures are the budget
// database's (D1046) and one row per dimension the chain bounds.
//
// The chain's id is the *root* run (D1045) and not the run being shown: the
// limit lives in the root launch's stored task and the spend is the whole
// chain's, so a reader that named the shown run would be reading a chain of one
// — the shortening D1047 forbids.
type ChainLimits struct {
	// Chain is the root run id, and empty for the one read that stopped at a
	// gap: the run that began the chain is behind a hop the bucket no longer
	// holds anything under, so its id was never read (runstore.Chain).
	Chain string `json:"chain"`
	// RunCount is how many runs of the chain the link reached, the shown run
	// among them. A chain of one is the ordinary shape of a first run.
	RunCount int `json:"run_count"`
	// Enforced is whether the money and token figures are the budget
	// database's. It is false when the installation's budget is off, where the
	// records are the figures and the chain is a report and not a stop (D1046,
	// T10).
	Enforced bool `json:"enforced"`
	// Limits are the dimensions the chain's own limit bounds, in the order
	// every message about them reads: dollars, tokens, wall time, review
	// rounds. A dimension the chain sets no figure for is absent rather than
	// present at zero: nothing bounds it.
	Limits []ChainLimit `json:"limits"`
}

// ChainLimit is one dimension of a chain's limit: the figure, what the chain
// has spent of it, what is left, and the layer the figure came from.
//
// Every amount is in the dimension's own unit (runstore.Amount) — micro-dollars
// for the money, the unit the ledger, the caps and every other document count
// in, whole tokens, seconds for the wall time and a count for the rounds — so a
// consumer reads `dimension` and knows what the number is. The three amounts are
// the engine's own figures and are never converted here: the launch reply
// carries this same block (D1056), and a conversion in one document and not the
// other is the drift the shared shape exists to prevent.
//
// Used and Remaining are absent together and only for a dimension the chain does
// not measure: with the budget off no run record carries a token total, so that
// dimension is reported unmeasured rather than approximated (D1051, F8), and a
// zero there would say the chain spent nothing.
type ChainLimit struct {
	Dimension runstore.Dimension `json:"dimension"`
	Limit     runstore.Amount    `json:"limit"`
	Used      *runstore.Amount   `json:"used"`
	Remaining *runstore.Amount   `json:"remaining"`
	// Source is the policy layer the figure came from, as the shown run's own
	// record names it — the chain layer's name is policy.SourceChain (F4,
	// D1055). It is the chain layer wherever the record names none: a chain's
	// limit is the root launch's own figure, whatever wrote the record (D1041).
	Source string `json:"source"`
}

type runsShowOptions struct {
	cloud  cloudOptions
	repo   string
	asJSON bool
}

func newRunsShowCmd() *cobra.Command {
	var o runsShowOptions
	cmd := &cobra.Command{
		Use:   "show RUN",
		Short: "Show a run's chain and what is left of its limits",
		Long: "Show the chain RUN (a run ID, <repo-slug>/<run-id>, or the last four or\n" +
			"more characters of a run ID) belongs to: the run that began it, each\n" +
			"dimension's limit, what the chain has used of it and what is left, the layer\n" +
			"each limit's source names, and whether the chain is ENFORCED or UNENFORCED.\n" +
			"When the run is halted, the halt is printed too.\n\n" +
			"A chain is the unit of work whose runs share one limit (D1045): the limit is the\n" +
			"figure the run that began the chain was launched with, and what has been used is\n" +
			"read off the chain's runs — the dollars and tokens from the budget database's\n" +
			"per-run ledgers, the wall time and review rounds from the run records. With the\n" +
			"budget off there is no ledger, the records are the figures, the chain is marked\n" +
			"UNENFORCED, and the token dimension is reported unmeasured rather than\n" +
			"approximated.\n\n" +
			"The wall-time and review-round limits are advisory: no rule enforces them.\n\n" +
			"--json prints {version, repo, run, chain, run_count, enforced, limits[], halt},\n" +
			"each limit a {dimension, limit, used, remaining, source} whose amounts are in\n" +
			"the dimension's own unit (dollars, tokens, seconds, rounds).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRunsShow(cmd, &o, args[0]) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository RUN belongs to (owner/name, or a path inside its checkout; needed only for a bare run ID)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runRunsShow(cmd *cobra.Command, o *runsShowOptions, ref string) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	// artifactsRun is the run resolver every command that takes RUN and --repo
	// uses: --repo names the repository a bare run ID belongs to, and a RUN that
	// already carries a slug has to agree with it.
	s, err := artifactsRun(ctx, env, ref, o.repo, resolveRunOpts(cmd, o.asJSON))
	if err != nil {
		return err
	}
	rec, err := showRunRecord(ctx, s, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	repo, err := showRunRepo(ctx, env, s, rec)
	if err != nil {
		return err
	}
	lim, err := chainLimitsOf(ctx, env, s.Slug(), s.RunID(), rec, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	doc := runsShowDoc{Version: runsShowDocVersion, Repo: repo, Run: s.Slug() + "/" + s.RunID(), ChainLimits: *lim}
	if rec != nil && rec.Halt != nil {
		// The halt's own sentence is the run's text, redacted the way diagnose
		// redacts every string it prints.
		h := *rec.Halt
		h.Detail = agent.RedactFunc(cliSecrets(os.Getenv))(h.Detail)
		doc.Halt = &h
	}
	if o.asJSON {
		return writeJSON(cmd.OutOrStdout(), doc)
	}
	return printRunsShow(cmd.OutOrStdout(), &doc)
}

// runsShowDoc is the document `runs show --json` prints. The chain block is
// embedded, so the members a consumer branches on are the block's own and the
// two commands cannot spell them differently (D1056).
//
// Run and Repo name the run the command was asked about the way the CLI's other
// documents do — `run` is <storage-slug>/<run-id> and `repo` the repository's
// own name (runview.Row, launchResult) — while the chain block's own `chain` is
// a bare run id, the chain being one repository's runs (D1045).
type runsShowDoc struct {
	Version int    `json:"version"`
	Repo    string `json:"repo"`
	Run     string `json:"run"`
	ChainLimits
	// Halt is the halt of a halted run, absent for a run that was not halted.
	Halt *runstore.Halt `json:"halt,omitempty"`
}

// showRunRepo is the repository the run being shown belongs to, as a person
// reads it (owner/name): the name its own record carries, which a row of
// `runs ls` prints, and the name in its stored task for a run the bucket has no
// record of yet — a run still in flight. It is never the storage slug, which is
// part of the document's own `run` beside the run id.
func showRunRepo(ctx context.Context, env *cloudEnv, s *runstore.Store, rec *runstore.Record) (string, error) {
	if rec != nil && rec.Repo != "" {
		return rec.Repo, nil
	}
	spec, err := readTask(ctx, env, s.Slug(), s.RunID())
	if err != nil {
		return "", err
	}
	return spec.Repo, nil
}

// showRunRecord is the run's own record, nil when the bucket has none or the
// record cannot be read: what the chain block reads off it — the layer each
// limit's source names and the halt — is optional, and the chain itself is read
// from the run's stored task and its ledgers either way.
func showRunRecord(ctx context.Context, s *runstore.Store, warn io.Writer) (*runstore.Record, error) {
	rec, err := absent(s.ReadRecord(ctx))
	switch {
	case corruptObject(err):
		fmt.Fprintf(warn, "warning: run %s/%s's result.json is unreadable (%v), so the halt and the layers it names are left out\n",
			s.Slug(), task.ShortRun(s.RunID()), err)
		return nil, nil
	case err != nil:
		return nil, remote(err)
	}
	return rec, nil
}

// chainLimitsOf reads the chain run id of slug belongs to and what is left of
// its limits (D1046, D1047): the link followed one record at a time, the limit
// out of the stored task of the run that began it, and what has been used out
// of the budget database's ledgers and the records. rec is the shown run's own
// record, nil when the bucket has none, and it is where the layer each limit's
// source names is read from.
//
// A read that stopped at a gap is a warning and a block with what was read:
// the bucket ages a run's objects out together, so a chain older than its
// window has a hop with nothing under it, and what the runs still reached spent
// is a fact about them whatever the hop held (ResolveChain, D1049). Every other
// failed read is the command's own failure: a chain whose spend cannot be
// established is a chain this answer would have to invent.
func chainLimitsOf(ctx context.Context, env *cloudEnv, slug, id string, rec *runstore.Record, warn io.Writer) (*ChainLimits, error) {
	if warn == nil {
		warn = io.Discard
	}
	chain, err := chainRead(ctx, env, slug, id)
	switch {
	case errors.Is(err, runstore.ErrChainGap):
		fmt.Fprintf(warn, "warning: %v; what is shown is what the run's own objects still hold\n", err)
	case err != nil:
		return nil, userErr("run %s/%s: the chain it is in cannot be read (%v), so what is left of its limits cannot be established", slug, task.ShortRun(id), err)
	}
	out := &ChainLimits{Limits: []ChainLimit{}}
	if chain == nil {
		// The budget is off and the records stop partway through, so there is
		// no figure at all: the runs the link still reaches are a lower bound
		// of the chain's spend, and a lower bound read as the figure is the
		// shortening ErrChainGap exists to prevent.
		return out, nil
	}
	out.Chain, out.RunCount, out.Enforced = chain.Root, len(chain.Runs), chain.Enforced
	if chain.Root == "" {
		// A gap with a ledger to charge the hop in: the runs read are answered
		// but the run that began the chain is behind the hop, so the limit the
		// figures would be counted against was never read.
		return out, nil
	}
	spec, err := readTask(ctx, env, slug, chain.Root)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		// The run that began the chain has no stored task left, so the chain
		// carries no limit: nothing bounds the dimensions it spent in.
		return out, nil
	case err != nil:
		return nil, err
	}
	for _, d := range chainDimensions() {
		limit, limited := chainLimitAmount(spec.Overrides, d)
		if !limited {
			continue
		}
		row := ChainLimit{Dimension: d, Limit: limit, Source: chainLimitSource(rec, d)}
		if used := chain.Used(d); used.Measured {
			amount, left := used.Amount, chain.Remaining(limit, d).Amount
			row.Used, row.Remaining = &amount, &left
		}
		out.Limits = append(out.Limits, row)
	}
	return out, nil
}

// chainLimitSource is the layer one dimension's figure came from, read where
// the run's own policy merge recorded it: Sources maps each key to the layer
// that set it, and the chain layer's name is policy.SourceChain (F4, D1055).
// Before the chain layer had a name of its own every layer past the first two
// was called `branch`, and a limit printed as coming from the branch names a
// place the number did not come from.
//
// A record that names none leaves the chain layer: a chain's limit is the root
// launch's own figure — no other layer can set one (D1041) — so a record with
// no policy block, or one written before the layer had a name, is a record
// whose silence is the chain layer's.
func chainLimitSource(rec *runstore.Record, d runstore.Dimension) string {
	if rec != nil && rec.Policy != nil {
		if src := rec.Policy.Sources[chainLimitKey(d)]; src != "" {
			return src
		}
	}
	return policy.SourceChain
}

// chainLimitKey is the policy key one dimension is merged under — the key the
// record's sources are read by, which is not the task's own field name: the
// task's max_time is the merge's max_run_time (policy.KeyMaxRunTime).
func chainLimitKey(d runstore.Dimension) string {
	switch d {
	case runstore.USD:
		return policy.KeyPerRunUSD
	case runstore.Tokens:
		return policy.KeyMaxRunTokens
	case runstore.Time:
		return policy.KeyMaxRunTime
	case runstore.ReviewRounds:
		return policy.KeyMaxReviewRounds
	}
	return string(d)
}

// printRunsShow prints the document for a person: the run, its chain and its
// limits, and the halt of a halted run.
func printRunsShow(w io.Writer, doc *runsShowDoc) error {
	fmt.Fprintf(w, "Run:      %s\n", oneLine(doc.Run))
	fmt.Fprintf(w, "Repo:     %s\n", oneLine(doc.Repo))
	if err := writeChainLimits(w, &doc.ChainLimits); err != nil {
		return err
	}
	if h := doc.Halt; h != nil {
		line := fmt.Sprintf("Halted:   %s (%s) at %s", h.Reason, h.Scope, h.At.UTC().Format(time.RFC3339))
		if h.Detail != "" {
			line += ": " + h.Detail
		}
		fmt.Fprintf(w, "%s\n", oneLine(line))
	}
	return nil
}

// writeChainLimits prints the chain and its limits, the way both commands print
// them: the chain's id and whether it is enforced, then one row per dimension
// it bounds. A chain that bounds nothing prints the line that says so rather
// than an empty table, and a chain whose root could not be read prints why.
func writeChainLimits(w io.Writer, lim *ChainLimits) error {
	word := "ENFORCED"
	if !lim.Enforced {
		word = "UNENFORCED"
	}
	switch {
	case lim.Chain != "":
		fmt.Fprintf(w, "Chain:    %s (%d runs, %s)\n", lim.Chain, lim.RunCount, word)
	case lim.RunCount > 0:
		fmt.Fprintf(w, "Chain:    the run that began it is behind a link the bucket no longer holds (%d runs read, %s)\n", lim.RunCount, word)
	default:
		fmt.Fprintf(w, "Chain:    the chain's records stop at a link the bucket no longer holds, so its limits cannot be read\n")
	}
	if len(lim.Limits) == 0 {
		if lim.Chain != "" {
			fmt.Fprintf(w, "Limits:   this chain bounds none of the four dimensions\n")
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DIMENSION\tLIMIT\tUSED\tREMAINING\tSOURCE")
	advisory := false
	for _, l := range lim.Limits {
		used, left := "unmeasured", "unmeasured"
		if l.Used != nil {
			used = chainAmountText(l.Dimension, *l.Used)
		}
		if l.Remaining != nil {
			left = chainAmountText(l.Dimension, *l.Remaining)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.Dimension, chainAmountText(l.Dimension, l.Limit), used, left, l.Source)
		advisory = advisory || l.Dimension == runstore.Time || l.Dimension == runstore.ReviewRounds
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if advisory {
		// Spec §4: the two record-backed dimensions have no rule behind them,
		// chain-scoped or per run, so they are a report beside the money.
		fmt.Fprintln(w, "Note:     the wall-time and review-round limits are advisory: they are read off the run records and no rule enforces them")
	}
	return nil
}
