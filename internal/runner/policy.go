package runner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// defaultBranchData is fugaro.yaml as the repository's default branch has
// it, read once per run.
type defaultBranchData struct {
	done bool
	name string // the default branch
	data []byte
	err  error
}

// defaultBranchFile reads fugaro.yaml at origin/<default branch>: the file a
// run's project and policy are checked against, which no file in the
// checkout can choose. It asks origin for the default branch, fetches it and
// shows the file, once; later calls return the first answer (or error). The
// error messages are the project check's, unchanged.
func (r *run) defaultBranchFile(ctx context.Context) ([]byte, string, error) {
	d := &r.defFile
	if d.done {
		return d.data, d.name, d.err
	}
	d.done = true
	def, err := r.repo.DefaultBranch(ctx)
	if err != nil {
		d.err = fmt.Errorf("finding the default branch to read fugaro.yaml from: %w", err)
		return nil, "", d.err
	}
	d.name = def
	if err := r.repo.FetchBase(ctx, def); err != nil {
		d.err = fmt.Errorf("fetching default branch %s: %w", def, err)
		return nil, def, d.err
	}
	if d.data, err = r.repo.ShowFile(ctx, "refs/remotes/origin/"+def, "fugaro.yaml"); err != nil {
		d.err = fmt.Errorf("reading fugaro.yaml at origin/%s: %w", def, err)
		d.data = nil
	}
	return d.data, def, d.err
}

// resolvePolicy computes the run's effective budget once, at bootstrap, as
// the tightest of four layers: the owner's ceiling (the job environment,
// r.d.Spend), the default branch's fugaro.yaml, the run's own cfg and — for a
// launch that asked for one — the chain's remaining (D1044). A layer can only
// tighten, so a branch (or the agent, which can edit fugaro.yaml mid-run)
// never loosens what the owner or the team set. The chain is the last of the
// four, because what it carries is the run's to write (a hand-written task,
// an older CLI) and the figure a chain has left is not. The result is stored
// on the run (r.spend, r.policy) and never recomputed.
//
// A default-branch file whose policy keys are invalid is an error: the
// ceiling alone would silently drop the team's rules. When the job has no
// project (a local run) the default branch is not read, as in checkProject.
func (r *run) resolvePolicy(ctx context.Context, cfg *config.Config) error {
	c := r.d.Spend
	// The mode this run resolved at bootstrap is the per-run default under
	// the ceiling (D16, plan T6.1): a fraction of the owner's cap, and never
	// above it. It is read once, here, with the rest of the policy — the mode
	// a run has reached later in the run only ever rises, and a policy a
	// run's own stages could move would not be the ceiling it is.
	mode := r.reachedRigor()
	// The ceiling is the job environment's copy of the owner's config, and
	// sets no output limits (the project config has none).
	ceiling := envCeiling(c, mode)
	// The project layer's governance: block joins the ceiling, the way the
	// project's budget does: it is the one layer that may loosen Fugaro's own
	// defaults for those keys, and every layer under it only tightens (T7.1,
	// D331). It is placed here rather than in envCeiling because it does not
	// travel in the job environment — the project config carries no governance
	// — it arrives with the task, in the layer the run resolved. The policy
	// node has no governance either, so policyCeiling leaves it standing.
	//
	// Fugaro's own defaults are filled in here, because this is the layer every
	// later one is held to and none of them may loosen (Governance.WithDefaults,
	// T7.1). Without them the first layer to write a governance key would be the
	// one that wins with it: a repository's fugaro.yaml — on its default branch,
	// and on the branch the run checks out, which the agent can edit mid-run —
	// could raise a guardrail Fugaro ships and nothing would say so. A key the
	// project layer does write is kept exactly as it stands, looser than the
	// default included: that layer is where the guardrails are loosened.
	var gov policy.Governance
	if l := cfg.Layer; l != nil {
		gov = l.Governance.Policy()
	}
	ceiling.Governance = gov.WithDefaults()
	// The project's own policy, when it has published one, *is* the ceiling
	// for the keys it sets: the node is what the owner says now, where the
	// copy is what he said when the job was deployed (design §5, plan task
	// 1.3 step 3, 12a item 13). Every key the node is silent about stays the
	// environment's — the node replaces the ceiling key by key, never whole
	// (H1: a policy published for allow_repos alone used to drop the mode,
	// the cap and the kill switches the environment carried).
	//
	// A read that fails is returned as it is: the run does not start rather
	// than start on a ceiling the policy did not set.
	pp, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	var fromPolicy map[string]bool
	if found {
		if ceiling, c, fromPolicy, err = r.policyCeiling(pp, ceiling, c); err != nil {
			return err
		}
	}
	r.ceiling = ceiling

	var def policy.Layer
	if r.d.Project == "" {
		r.d.Log.Info("no project: not reading the budget policy from the default branch")
	} else {
		data, branch, err := r.defaultBranchFile(ctx)
		if err != nil {
			return err
		}
		p, err := config.PolicyOf(data)
		if err != nil {
			return fmt.Errorf("fugaro.yaml on the default branch (%s) has an invalid policy, so the run can't tell what the team allows: %w", branch, err)
		}
		def = policy.Layer{Mode: p.Mode, PerRunUSD: p.PerRunUSD, PerDayUSD: p.PerDayUSD, MaxRunTokens: p.MaxRunTokens, AllowedModels: p.AllowedModels,
			MaxOutputCoder: p.MaxOutputTokens.Coder, MaxOutputReviewer: p.MaxOutputTokens.Reviewer, Governance: p.Governance}
	}

	file := FileLayer(cfg)
	// The chain's remaining is resolved against the three layers above it and
	// merged after them: it is the run's own figure that arrives here (the
	// launcher wrote it into the task, and a hand-written task carries
	// whatever its writer typed), so it is the one layer that must not be able
	// to loosen what the ceiling, the default branch or the branch resolved
	// (D1044). pre is the merge under it, and chainLayer reads only its mode
	// out of it.
	pre := policy.Merge(ceiling, def, file)
	chain, err := r.chainLayer(ctx, pre)
	if err != nil {
		return err
	}
	layers := []policy.Layer{ceiling, def, file, chain}
	r.govLayers = layers
	e := policy.Merge(layers[0], layers[1:]...)
	if found {
		// The merge names the first layer "ceiling": for the keys the node
		// set, that layer is the node and not the job environment, and a
		// run's result.json has to say which of the two each value was
		// (H1: the keys the node is silent about are still the environment's,
		// and saying "policy" for those would name the wrong owner).
		policySources(&e, fromPolicy)
	}
	// Merge lists the ignored values in layer order, so the numbers the first
	// two layers drop and the first three drop say who asked for each: what
	// the branch's own file lost is between them, and everything after the
	// second is the chain's (recordPolicy reads them back).
	nDef := len(policy.Merge(ceiling, def).Ignored)
	nFile := len(pre.Ignored)
	s := Spend{Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels, Prices: c.Prices}
	if s.Mode == "" {
		s.Mode = policy.ModeOff
	}
	if e.PerRunUSD > 0 {
		if src := e.Sources[policy.KeyPerRunUSD]; src == policy.SourceCeiling {
			// The ceiling's own cap, scaled by the run's mode and not by a
			// float round trip through the layer's dollar figure (D16). A cap
			// some other layer wrote is that layer's own number and is taken
			// as it is: the mode's fraction is the default a run gets where
			// nothing says otherwise, never a multiplier over a number the
			// owner, the team or the branch set.
			s.Cap = config.RigorBudgetCap(mode, c.Cap)
		} else {
			var err error
			if s.Cap, err = pricing.FromUSD(e.PerRunUSD); err != nil || s.Cap < 1 {
				return fmt.Errorf("budget.per_run_usd %v: not a usable cap: %v", e.PerRunUSD, err)
			}
		}
	}
	if s.On() && s.Prices == nil {
		// The ceiling was off with no price overrides (they would have
		// been loaded); a committed observe or enforce charges the same
		// table, built with no overrides.
		prices, err := r.priceTable(nil)
		if err != nil {
			return fmt.Errorf("pricing the models: %w", err)
		}
		s.Prices = prices
	}
	// The stages read their per-call output limits from the run's
	// configuration: it carries the merged limits from here on.
	cfg.Agent.MaxOutputTokens = config.RoleTokens{Coder: e.MaxOutputCoder, Reviewer: e.MaxOutputReviewer}
	r.spend, r.policy = s, e
	// The routing switch is applied here, once, after r.spend and before
	// anything reads the run's pins: the policy is where D1a delivers it, and
	// the pins it rewrites are what the pins renderer renders, the gateway
	// routes and prices, and the record names (Task 1b.2).
	//
	// It comes *after* the assignment above because `gatewayOn` — which
	// decides whether the switch has anything to act on at all — is read off
	// r.spend: applied before it, every run would look like a run with the
	// gateway off, and the switch would silently never take effect (the
	// critical finding of this PR's review). The price table this function
	// built is keyed by the routes and the catalog rather than by the pins, and
	// the two decisions that *are* read off a pin — the review levels, whose
	// level 1 answer is the coder's model, and the plan, built from those
	// levels and Agent.FirstLineOn — are taken just below and by the caller
	// after this returns, so both see the pins the switch produced rather than
	// the ones it replaced.
	//
	// The run's review levels are resolved after the switch for that reason:
	// the floor is the merged policy's (G1), and the models a level names are
	// read off the pins the switch left, so a resolution taken before the
	// rewrite would answer about the IDs it replaced.
	if err := r.applyRouting(pp, found); err != nil {
		return err
	}
	if err := r.resolveLevels(); err != nil {
		return err
	}
	r.rec.Policy = r.recordPolicy(nDef, nFile)
	return nil
}

// chainDimensions is the four dimensions a chain limits (spec §2), in the
// order a message about them names them.
var chainDimensions = []runstore.Dimension{runstore.USD, runstore.Tokens, runstore.Time, runstore.ReviewRounds}

// chainLayer is the merge's last layer: what is left of the chain's limits
// once the runs behind this one are charged for what they spent (D1041,
// D1044, D1047).
//
// The launcher resolved this figure before the launch and wrote it into the
// task, and the runner resolves it again here because a task reaches it
// unchecked: ReadTask decodes whatever the bucket holds, so a hand-written
// task — or one an older CLI wrote before the four members existed — carries
// the chain's limit itself or a figure a chain that has spent cannot leave
// (D1044). Nothing is taken on the task's word. The chain behind this run is
// read from the bucket's records, one id at a time, by name (F21), and its
// own limit is read back from the stored task of the run that began it
// (D1046). The money and token figures come from the budget database, read
// with this run's own token, which may read its own ledger node and no
// sibling's (U4) — so a sibling's ledger read fails rather than answering
// zero, and the dimension is left unmeasured instead of charged nothing
// (D1049, D1051).
//
// pre is the merge under this layer, and only its mode is read: with the
// budget off there is no ledger at all, the records are the figures and the
// chain is a report rather than a stop (D1046, T10).
//
// A chain with nothing left of a dimension this run is asked to carry is a
// refusal at bootstrap and not a run that starts and halts at once (D1050
// case 1): the halt is recorded and nothing has been spent, no ledger has
// been leased and no agent has run.
//
// A read that stops at a link the bucket no longer holds is the one failure
// this layer answers rather than refuses (ErrChainGap): the chain's limit and
// its spend are both gone with the record, and the figures the task carries —
// what the launcher resolved against the previous run's stored task, which is
// what survives the gap — are the bound the run runs under, applied as the
// first bound alone and refused over when they are spent (D1052).
func (r *run) chainLayer(ctx context.Context, pre policy.Effective) (policy.Layer, error) {
	var l policy.Layer
	o := r.spec.Overrides
	if o.MaxUSD == nil && o.MaxTokens == nil && o.MaxTime == "" && o.MaxReviewRounds == nil {
		// A launch that asked for no chain limit is in no chain (D1041), and
		// nothing is read to establish that: the four figures above are the
		// whole of what a chain is, so a run that carries none of them — every
		// ordinary run — resolves exactly as it did before this layer existed.
		return l, nil
	}
	ledgers, err := r.chainLedgers(ctx, pre)
	if err != nil {
		return l, err
	}
	own, err := r.ownChainUse(ctx, ledgers)
	if err != nil {
		return l, err
	}
	from := r.spec.PreviousRun
	if from == "" {
		// A launch that begins a chain is the chain: the run the launcher
		// linked it to is its root, and a retry's link is the stored task's
		// own previous_run (D1045). A retry therefore resolves the chain
		// behind the run it retries, and what *this* run already holds — the
		// ledger node it kept from the attempt that halted — is charged
		// against the remaining below (D1050 case 3).
		from = r.spec.RunID
	}
	chain, err := runstore.ResolveChain(ctx, r.d.Store.Sibling(from), ledgers, runstore.PerRunCaps{}, r.d.Now())
	gapped := false
	switch {
	case errors.Is(err, runstore.ErrChainGap):
		// The bucket has aged a hop out from under the chain, so what it has
		// spent cannot be read at all (D1052). The figures this run's task
		// carries are still a bound — the launcher resolved them against the
		// previous run's stored task, which is what survives the gap — and
		// with no chain read there is no second figure to hold them under, so
		// they are applied as chainRemaining's first bound and nothing else.
		// Dropping the layer instead would leave the run bounded by the
		// per-run caps alone, which is the hole the launcher's own gap arm
		// closes for every other launch shape.
		r.d.Log.Warn("the chain's records stop at a link the bucket no longer holds: this run is held to the limit its task was launched with", "err", err)
		chain, gapped = nil, true
	case err != nil:
		return l, fmt.Errorf("reading the chain this run continues: %w", err)
	}
	limit := task.Overrides{}
	if !gapped {
		limit = r.spec.Overrides
	}
	if chain != nil && chain.Root != r.spec.RunID {
		// The chain's own limit is the root launch's number and nobody else's
		// (D1046): it is read back from the stored task of the run that began
		// the chain, which no later launch can raise (D1052). A task the
		// bucket no longer holds leaves the launcher's own figures as the only
		// limit in hand — never a figure invented here.
		spec, err := r.d.Store.Sibling(chain.Root).ReadTask(ctx)
		switch {
		case errors.Is(err, runstore.ErrNotFound):
			limit = task.Overrides{}
		case err != nil:
			return l, fmt.Errorf("reading the stored task of run %s, which began this chain: %w", chain.Root, err)
		default:
			limit = spec.Overrides
		}
	}
	enforced := ledgers != nil
	for _, d := range chainDimensions {
		rem, limited := chainRemaining(d, o, limit, chain, own)
		if !limited {
			continue
		}
		if !chainSpentOut(d, rem) {
			setChainLayer(&l, d, rem)
			continue
		}
		// What is spent out is named by the bound that is spent — see
		// chainFromWord — and what a person can do about it is the bound's own:
		// a chain's limit is raised in the run that began it, while a gap
		// leaves the chain unreadable and the launch outside it the way out.
		spent := fmt.Sprintf("%s has nothing left of its %s limit", chainFromWord(chain), chainDimensionWord(d))
		advice := "raise the limit in the run that began the chain, or launch a run that is not part of it"
		if gapped {
			advice = "the chain behind it cannot be read (the bucket has aged its records out), so launch a run that is not part of this chain"
		}
		if !enforced {
			// With the budget off the chain is UNENFORCED: a report and not a
			// stop (D1046, T10), so a spent chain is said out loud and the run
			// goes on to spend past it.
			r.d.Log.Warn(spent+", and the budget is off, so nothing stops this run from spending past it",
				"dimension", chainDimensionWord(d))
			continue
		}
		return l, &HaltError{Halt: runstore.Halt{
			Reason: runstore.HaltRunCap,
			Scope:  "run",
			At:     r.d.Now().UTC(),
			Detail: spent + ", so this run would start with nothing to spend: " + advice,
		}}
	}
	return l, nil
}

// chainLedgers is the ledger reader this run reads its chain with, nil when
// there is no ledger at all.
//
// The gate is the resolved policy's mode and not the job's: the ceiling can be
// off while a repository commits observe or enforce, and the chain's money
// figures are the database's exactly when the run is accounted for
// (Spend.On). The reader itself is this run's own node — see ownLedger.
func (r *run) chainLedgers(ctx context.Context, pre policy.Effective) (runstore.LedgerReader, error) {
	if !(Spend{Mode: pre.Mode}).On() {
		return nil, nil
	}
	db, err := r.budgetClient(ctx)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, nil
	}
	return ownLedger{db: db, slug: r.d.Store.Slug(), run: r.spec.RunID}, nil
}

// ownLedger is the budget database as one run of a chain can read it: its own
// node, and no other.
//
// The job's Firebase token carries the slug and the run it was minted for (the
// rules' RUN claim, auth.token.fs == $slug && auth.token.fr == $run), so any
// other node is refused by the database rather than missing (U4). That is not
// an accident to be smoothed over: the chain's money figures for the runs
// behind this one are exactly what this identity cannot see, and a read that
// answered zero for them would hand the chain the whole budget it had already
// spent (D1049, F14). It is reported as the failure it is, and ResolveChain
// then leaves the dimension unmeasured.
type ownLedger struct {
	db   *rtdb.Client
	slug string
	run  string
}

func (l ownLedger) ReadLedger(ctx context.Context, slug, run string) (runstore.Ledger, bool, error) {
	if slug != l.slug || run != l.run {
		return runstore.Ledger{}, false, fmt.Errorf("the ledger of run %s/%s: this run's token reads only its own", slug, run)
	}
	got, found, err := budget.LedgerDB{DB: l.db}.ReadLedger(ctx, slug, run)
	return runstore.Ledger{Reserved: got.Reserved, Released: got.Released, Tokens: got.Tokens}, found, err
}

// ownChainUse is what this run has already charged its own ledger, in the two
// dimensions a ledger keeps (F20): the money it still holds — reserved less
// released (D1046) — and its tokens. Time and review rounds hold nothing of
// their own: both are read off the record the run is writing, and a chain's
// figures for them come from the runs behind it.
//
// The chain read cannot answer this. It walks the chain *behind* this run (a
// follow-up, a retry), and a chain of one — a first run — is read from the
// run's own record and its own ledger node, which is at bootstrap a node
// nothing has written yet. A run that halted mid-flight and started again is
// the case that needs the figure: its reservation is still held, and what is
// left of the chain's limit is drawn from the rest (D1050 case 3).
func (r *run) ownChainUse(ctx context.Context, ledgers runstore.LedgerReader) (map[runstore.Dimension]runstore.Amount, error) {
	own := map[runstore.Dimension]runstore.Amount{}
	if ledgers == nil {
		return own, nil
	}
	l, found, err := ledgers.ReadLedger(ctx, r.d.Store.Slug(), r.spec.RunID)
	if err != nil {
		// The database refused this run's own node, or the read failed: the
		// run's own session reads the same node a moment later and fails the
		// same way, so it stops here rather than resolve a limit against a
		// figure it never saw.
		return nil, fmt.Errorf("reading this run's own ledger: %w", err)
	}
	if !found {
		// A run that has held nothing yet: the node is written with the first
		// lease, and a launch that has not spent holds nothing (F20).
		return own, nil
	}
	own[runstore.USD] = runstore.Amount(l.Held())
	own[runstore.Tokens] = runstore.Amount(l.Tokens)
	return own, nil
}

// chainRemaining is one dimension's remaining as this run must carry it, and
// whether anything bounds it at all.
//
// Two figures can bound it, and the tighter of them is the one that survives —
// a hand-written task is the run's own text and the chain's limit is not, so
// neither may be trusted alone:
//
//   - The task's own figure, less what this run already holds (own). For a
//     launch that begins a chain the figure is the chain's limit itself
//     (D1041); for a follow-up or a retry the launcher wrote the remaining it
//     resolved (D1047). The spend since then is this run's own, and nothing
//     else's: no other run of the chain can have spent between the launcher's
//     read and this one, and a run that halted and started again holds its
//     reservation (D1050 case 3).
//   - The chain's own limit, read back from the root's stored task (D1046),
//     less what the chain's records and ledgers say it has spent. This is the
//     figure the task cannot loosen, and it is only there where the read
//     measured the dimension — money and tokens are measured only for a chain
//     of this run alone, since this token reads its own ledger node and no
//     sibling's (U4, D1049).
//
// A nil chain is a read that stopped at a link the bucket no longer holds: the
// chain's limit and its spend are both unknown, so the first bound stands
// alone (ErrChainGap, D1052).
func chainRemaining(d runstore.Dimension, o task.Overrides, limit task.Overrides, chain *runstore.Chain, own map[runstore.Dimension]runstore.Amount) (runstore.Amount, bool) {
	rem, limited := runstore.Amount(0), false
	if lim, ok := chainLimitAmount(o, d); ok {
		rem, limited = lim-own[d], true
	}
	if chain == nil {
		return rem, limited
	}
	if lim, ok := chainLimitAmount(limit, d); ok {
		if used := chain.Used(d); used.Measured {
			if left := lim - used.Amount; !limited || left < rem {
				rem, limited = left, true
			}
		}
	}
	return rem, limited
}

// chainSpentOut reports whether a remaining is no figure at all in the unit
// the layer carries it: a sub-micro-dollar cap is no cap, a fractional token
// or round is no whole one, and a chain with nothing left of a dimension is a
// refusal rather than a limit the run resolves and cannot be held to (D1050
// case 1).
func chainSpentOut(d runstore.Dimension, a runstore.Amount) bool {
	if d == runstore.Time {
		return a <= 0
	}
	return a < 1
}

// chainLimitAmount is one dimension's limit as a task carries it, in the
// dimension's own unit, and whether the task limits it at all (D1041: a launch
// that names none carries no chain limit). A figure a task cannot hold — a
// duration that does not parse, a dollar amount past what a micro-dollar can
// be — reads as no limit rather than as a failure: the task was validated when
// it was written, and what this call does with it is a comparison. It is the
// same read the launcher's own chain resolution makes of a task
// (internal/cli's chainLimitAmount), restated here because the CLI imports
// this package and not the other way round.
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

// setChainLayer writes one dimension's remaining into the layer, in the unit
// the merge compares in (D1041): dollars for the cap, whole tokens, a
// duration, a count. It is the same write the launcher makes into a task
// (internal/cli's setChainLimit), in the merge's vocabulary rather than the
// task's, and it is the whole of how the chain reaches the run's budget: the
// cap the gateway enforces is this figure and nothing else (D1059).
func setChainLayer(l *policy.Layer, d runstore.Dimension, a runstore.Amount) {
	switch d {
	case runstore.USD:
		l.PerRunUSD = pricing.Micros(a).USD()
	case runstore.Tokens:
		l.MaxRunTokens = int64(a)
	case runstore.Time:
		l.MaxRunTime = time.Duration(math.Round(float64(a) * float64(time.Second)))
	case runstore.ReviewRounds:
		l.MaxReviewRounds = int64(a)
	}
}

// chainFromWord names the bound a refusal is about: the chain the figures were
// read from — the run that began it, which is where its limit lives and where a
// person raises it (D1046, F22) — or, for the nil chain a gap leaves, the
// figures this run's task carries, which are then the only bound there is
// (ErrChainGap, D1052).
func chainFromWord(c *runstore.Chain) string {
	if c == nil {
		return "the limit this run's task carries"
	}
	if len(c.Runs) > 1 {
		return fmt.Sprintf("the chain of run %s, %d runs long,", c.Root, len(c.Runs))
	}
	return fmt.Sprintf("the chain of run %s", c.Root)
}

// chainDimensionWord names one dimension the way a message about it reads. It
// is the launcher's own wording for the same four figures (internal/cli's
// chainDimensionWord): the two sides say the same thing about a chain, and the
// runner cannot call into the CLI, which imports this package.
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

// envCeiling is the ceiling the job environment deploys: the owner's mode, cap,
// token ceiling and allow-list as fugaro update wrote them. The mode "off" is
// unset — it is the absence of a mode, not a ceiling that says nothing may be
// accounted — and a mode that is not one of the three is refused by
// SpendFromEnv long before this.
//
// The cap comes through the run's rigor mode (D16, plan T6.1): the ceiling is
// the ceiling and a mode's per-run default is a fraction of it, so what the
// layer carries is the owner's cap scaled by the mode, and every later layer
// still only tightens it. A run whose mode contributes no default — Critical,
// which is today's behaviour, or one no resolution recorded — carries the
// owner's cap exactly.
func envCeiling(c Spend, mode rigor.Mode) policy.Layer {
	l := policy.Layer{MaxRunTokens: c.MaxRunTokens, AllowedModels: c.AllowedModels}
	if c.Mode != "" && c.Mode != policy.ModeOff {
		l.Mode = c.Mode
	}
	if c.Cap > 0 {
		l.PerRunUSD = config.RigorBudgetCap(mode, c.Cap).USD()
	}
	return l
}

// policyCeiling is the ceiling a published policy gives a run: the job
// environment's ceiling with the keys the node sets replaced, and nothing else
// touched. It returns the keys the node set as well, because the merge's first
// layer is named "ceiling" and result.json has to say which of the two each
// effective value actually was.
//
// The replacement is per key, not whole (H1, the security review of this PR).
// A node published for allow_repos alone used to replace the ceiling whole —
// mode, cap, token ceiling, allow-list, price table — so an installation that
// enforces a $50 cap with its kill switches on ran with no session, no cap and
// no switches at all, and nothing said so. The node is the owner's word about
// the keys it names; about the keys it does not, the deployed ceiling is the
// only thing that was ever said. The mode is the key this matters most for,
// and it is why the decoder refuses a budget block that names no mode: a node
// that meant to enforce cannot silently inherit the environment's "off".
//
// Spend.Cap carries the same ceiling in micro-dollars, kept exact rather than
// through a float round trip, so it is replaced only by the node's own cap and
// keeps the environment's when the node sets none.
//
// It is a method on the run because the price table it builds for the node's
// model_prices is the run's own: r.priceTable is config.PriceTable over the
// routes the job carries (FUGARO_MODEL_PROVIDERS) and the built-in catalog,
// with the node's overrides applied on top. Building it as
// pricing.Embedded().With(overrides) instead would drop every catalog row those
// routes contributed, so a run pinned to a catalog-priced provider model would
// be refused for "having no price" under a policy that priced some other model,
// and an override over a catalog row would come back with no token limits. The
// node replaces the owner's price OVERRIDES; the routes and the catalog
// underneath them are the job's, exactly as they are with no node at all.
func (r *run) policyCeiling(pp *projectpolicy.Policy, env policy.Layer, c Spend) (policy.Layer, Spend, map[string]bool, error) {
	ceiling, set := env, map[string]bool{}
	if m := pp.Budget.Mode; m != "" {
		ceiling.Mode, set[policy.KeyMode] = m, true
	}
	if v := pp.Budget.PerRunUSD; v > 0 {
		var err error
		if c.Cap, err = pricing.FromUSD(v); err != nil || c.Cap < 1 {
			return env, c, nil, fmt.Errorf("%s: budget.per_run_usd %v: not a usable cap: %v", projectpolicy.PathShared(), v, err)
		}
		// The node's cap is the owner's word about the ceiling, and it is
		// taken exactly as it stands: the run's rigor mode is the default a
		// run gets where no layer says otherwise (D16), and multiplying a
		// number the owner published by it would put a figure in result.json
		// the node never wrote — the very thing the source labels exist to
		// prevent (H1). The ceiling's own cap is the one the mode scales, and
		// it has no node to speak for it.
		ceiling.PerRunUSD, set[policy.KeyPerRunUSD] = v, true
	}
	if v := pp.Budget.MaxRunTokens; v > 0 {
		ceiling.MaxRunTokens, set[policy.KeyMaxRunTokens] = v, true
	}
	if pp.Budget.AllowedModels != nil {
		// A non-nil empty list is deny-all, which the decoder has already
		// refused: ValidBudget holds a policy's list to at least one model.
		ceiling.AllowedModels, set[policy.KeyAllowedModels] = pp.Budget.AllowedModels, true
	}
	// The review-level floor rides the ceiling layer, and this is the only
	// place it is read from (D722, G1): it is the owner's, it lives in the
	// owner-only node, and every other layer is written by the launcher. It is
	// not recorded in `set`, which names the keys result.json sources as the
	// node's: the floor is not a key of the merge's own vocabulary, and the
	// record has no field for it yet (L4.1). Merge unions it into the
	// effective policy (policy.Merge), so a later layer could add a level and
	// none can take one away.
	ceiling.ReviewFloor = pp.ReviewFloor
	// The triage switch rides the ceiling layer for the floor's reason
	// (D1854): it is the owner's word about whether a run's review rounds are
	// judged at all, and the node is the one object the launcher cannot write.
	// Unlike the floor it IS a key of the merge's own vocabulary
	// (policy.KeyTriage, which the repository writes as agent.triage), so a
	// node that turns it off and a repository that asks for it meet in
	// policy.Merge and the tighter one wins — off, whichever way round
	// (Effective.TriageOff). It is named in `set`, again unlike the floor: the
	// node wrote a value result.json reports the source of, and "ceiling"
	// would name the owner of the switch he did not flip.
	if pp.Triage != nil {
		ceiling.Triage, set[policy.KeyTriage] = pp.Triage, true
	}
	// The binary allow rule rides the ceiling layer for the floor's reason
	// (D986): it is the owner's word about the paths a run may add a binary at,
	// the node is the one object the launcher cannot write, and the merge
	// intersects the list and takes the smaller cap, so a layer under the
	// ceiling can remove an admitted path and never add one. A node with no
	// block leaves the ceiling with no list, which admits no binary at all —
	// exactly what every installation does today.
	//
	// No decoder fills the field yet (projectpolicy.Binaries): this release
	// lists a node's `binaries` key in UnknownKeys instead of reading it, so
	// every run today is the node-with-no-block case above. The read is here
	// because it is the seam the block arrives through, and because a policy
	// built by hand carries one — which is what AR2's tests below do.
	//
	// Unlike the floor these are keys of the merge's own vocabulary, so the
	// node's are named in `set`: result.json then sources them as "policy" and
	// not as a ceiling that carries no such value at all.
	//
	// The empty list the node wrote counts as its own: `binaries: {allow: []}`
	// is D987's spelling for clearing the list, the merge reads it as the first
	// list there is (the empty one), and it would then be sourced from the job
	// environment — the one layer that can carry no binaries at all, since
	// fugaro.yaml has no such key. So the member being written, not its length,
	// is what names the key.
	if b := pp.Binaries; b != nil {
		ceiling.BinariesAllow, ceiling.BinariesMaxBytes = b.Allow, b.MaxBytes
		if b.Allow != nil {
			set[policy.KeyBinariesAllow] = true
		}
		if b.MaxBytes > 0 {
			set[policy.KeyBinariesMaxBytes] = true
		}
	}
	if len(pp.Prices) > 0 {
		o, err := policyOverrides(pp)
		if err != nil {
			return env, c, nil, fmt.Errorf("%s: model_prices: %w", projectpolicy.PathShared(), err)
		}
		prices, err := r.priceTable(o)
		if err != nil {
			return env, c, nil, fmt.Errorf("%s: model_prices: %w", projectpolicy.PathShared(), err)
		}
		c.Prices = prices
	}
	return ceiling, c, set, nil
}

// policyOverrides is the owner's price overrides as the policy node carries
// them. The node spells model_prices exactly as the price catalogue spells them
// (projectpolicy.Price), so the catalogue's own parser reads them here too —
// the same one FUGARO_MODEL_PRICES goes through, whose overrides these replace:
// the policy is the owner's price OVERRIDES, whole.
//
// Prices.CatalogJSON is the spelling the parser reads: the node's own member
// names escape what an RTDB key may not hold (the "/" and "." of a model ID),
// and the decoded map is keyed by the real ID. The table the run prices with is
// the run's own (r.priceTable), because the routes the job carries and the
// built-in catalog are not the node's to replace — see policyCeiling.
func policyOverrides(pp *projectpolicy.Policy) (pricing.Overrides, error) {
	if len(pp.Prices) == 0 {
		return nil, nil
	}
	raw, err := pp.Prices.CatalogJSON()
	if err != nil {
		return nil, err
	}
	return pricing.ParseOverrides(string(raw))
}

// policySources renames the merge's first layer for the keys the project's
// policy node set: result.json's policy.sources then says
// "policy=<config/policy/shared>" for those keys, and the refusals and the
// report can send the reader to the node rather than to a job environment that
// no longer holds the value. The keys in fromPolicy are the node's; the rest
// of the first layer is the job environment's and keeps the name "ceiling"
// (H1). Nothing else changes: every later layer is still the same branch or
// file, and still only tightens.
func policySources(e *policy.Effective, fromPolicy map[string]bool) {
	for k := range fromPolicy {
		if e.Sources[k] == policy.SourceCeiling {
			e.Sources[k] = policy.SourcePolicy
		}
	}
	for i := range e.Ignored {
		if e.Ignored[i].Source == policy.SourceCeiling && fromPolicy[e.Ignored[i].Key] {
			e.Ignored[i].Source = policy.SourcePolicy
		}
	}
}

// FileLayer is what a fugaro.yaml says about policy: the run's own layer,
// and what `fugaro validate` checks, built in one place so the two agree.
func FileLayer(cfg *config.Config) policy.Layer {
	l := policy.Layer{MaxRunTokens: cfg.Agent.MaxRunTokens,
		MaxOutputCoder: cfg.Agent.MaxOutputTokens.Coder, MaxOutputReviewer: cfg.Agent.MaxOutputTokens.Reviewer,
		Governance: cfg.Governance.Policy()}
	if b := cfg.Budget; b != nil {
		l.Mode, l.PerRunUSD, l.PerDayUSD, l.AllowedModels = b.Mode, b.PerRunUSD, b.PerDayUSD, b.AllowedModels
	}
	return l
}

// policyRecord builds result.json's policy object from the merge: nil when
// no layer set anything that bears on money or models and nothing was
// ignored, so a repository without policy writes the same record as before.
// The per-call output limits are not by themselves a policy.
//
// A governance key still at the figure Fugaro ships is not a policy either,
// even though the ceiling carries it: the ceiling is built with the defaults
// (Governance.WithDefaults), so a key no layer wrote has the ceiling as its
// source for the same reason it has a value at all — because Fugaro put it
// there. Sourcing it as "ceiling" would name the owner of a guardrail he never
// chose (H1), and it would put all eighteen governance keys in every record,
// including the records of repositories that write no policy at all. A key a
// layer did write is kept: the value is then a decision somebody made.
func policyRecord(e policy.Effective, nDef, nFile int) *runstore.PolicyRecord {
	sources := maps.Clone(e.Sources)
	for k := range e.Governance.DefaultGovernanceKeys() {
		if sources[k] == policy.SourceCeiling {
			delete(sources, k)
		}
	}
	bears := false
	for k := range sources {
		if k != policy.KeyMaxOutputCoder && k != policy.KeyMaxOutputReviewer {
			bears = true
			break
		}
	}
	if !bears && len(e.Ignored) == 0 {
		return nil
	}
	rec := &runstore.PolicyRecord{Effective: runstore.PolicyEffective{
		PerRunUSD: e.PerRunUSD, PerDayUSD: e.PerDayUSD, Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels}}
	if e.MaxOutputCoder > 0 || e.MaxOutputReviewer > 0 {
		rec.Effective.MaxOutputTokens = &runstore.PolicyOutput{Coder: e.MaxOutputCoder, Reviewer: e.MaxOutputReviewer}
	}
	if len(sources) > 0 {
		rec.Sources = sources
	}
	// The ignored values come back in layer order, so the two counts say which
	// layer asked for each: before nDef the default branch, before nFile the
	// run's own file, and after it the chain — the one layer whose figure is
	// the run's own text and may therefore be the one that lost (D1044).
	for i, ig := range e.Ignored {
		from := policy.SourceBranch
		switch {
		case i < nDef:
			from = policy.SourceDefaultBranch
		case i >= nFile:
			from = policy.SourceChain
		}
		rec.Ignored = append(rec.Ignored, runstore.PolicyIgnored{Key: ig.Key, Value: ig.Value, Effective: ig.Effective, Source: ig.Source, From: from})
	}
	return rec
}

// recordPolicy builds the record and, once per run, logs the policy and
// warns once per key about each value that was dropped.
func (r *run) recordPolicy(nDef, nFile int) *runstore.PolicyRecord {
	rec := policyRecord(r.policy, nDef, nFile)
	if rec == nil {
		return nil
	}
	e := rec.Effective
	allowed := "any"
	if e.AllowedModels != nil {
		allowed = strings.Join(e.AllowedModels, ",")
		if allowed == "" {
			allowed = "none"
		}
	}
	args := []any{"per_run_usd", e.PerRunUSD}
	if e.PerDayUSD > 0 {
		args = append(args, "per_day_usd", e.PerDayUSD)
	}
	if e.Mode != "" {
		args = append(args, "mode", e.Mode)
	}
	args = append(args, "max_run_tokens", e.MaxRunTokens, "allowed_models", allowed, "sources", sourcesText(rec.Sources))
	r.d.Log.Info("policy", args...)
	warned := map[string]bool{}
	for _, ig := range rec.Ignored {
		if warned[ig.Key] {
			continue
		}
		warned[ig.Key] = true
		r.d.Log.Warn("policy: a looser value in fugaro.yaml was ignored", "key", PolicyKeyPath(ig.Key),
			"value", ig.Value, "effective", ig.Effective, "from", ig.From, "limit_from", ig.Source)
	}
	return rec
}

func sourcesText(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, " ")
}

// PolicyKeyPath is where a policy key lives in fugaro.yaml: the governance
// keys under a governance: block of their own — a tier's own keys under
// governance.tiers.<tier>, the block that carries them (policy.TierKey) — the
// agent's own keys, its output limits, the triage switch and the crafts, under
// agent:, and everything else — the budget's cost and model policy — under
// budget:. The triage switch is the agent's key and not the budget's
// (policy.KeyTriage is "triage" and the repository writes agent.triage,
// config.Scopes), so a report that read it as a budget key would name a key no
// file has.
//
// A craft key is the agent.crafts block (policy.CraftAccessKey), which is the
// block where a repository's file writes the one of the three it may write at
// all: a craft's access, a selection among the credentials and the pools the
// project creates (F12, D1628). A credential or a pool a later layer may not
// introduce is reported under it too — no repository file carries either, and
// the block that speaks a credential name is the one that carries crafts, never
// budget:, where neither a craft nor a credential lives.
func PolicyKeyPath(key string) string {
	switch key {
	case policy.KeyMaxRunTokens, policy.KeyMaxOutputCoder, policy.KeyMaxOutputReviewer, policy.KeyTriage:
		return "agent." + key
	case policy.KeyReviewRatio, policy.KeyReviewFloorCalls, policy.KeyMaxSubagents,
		policy.KeyStageBudgetImplement, policy.KeyStageBudgetReview, policy.KeyStageBudgetFix,
		policy.KeyStageTokensImplement, policy.KeyStageTokensReview, policy.KeyStageTokensFix,
		policy.KeyStageTimeoutImplement, policy.KeyStageTimeoutReview, policy.KeyStageTimeoutFix:
		return "governance." + key
	}
	if strings.HasPrefix(key, "tiers.") {
		return "governance." + key
	}
	if strings.HasPrefix(key, "crafts.") {
		return "agent." + key
	}
	return "budget." + key
}
