package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"

	"github.com/spf13/cobra"
)

// The launcher's part of the budget backend (M9b R5):
//
//   - It is the only holder of the signer permission. It mints the run's
//     Firebase custom token after winning the launch claim and leaves it in
//     the runs bucket, sealed under a key it draws for this launch alone
//     (docs/budget-token-channel/spec.md §7.1); the runner takes it. The
//     token is never in the jobs.run request (the request reaches audit
//     logs), an env var, an error or a message.
//   - The seal key is not the token and grants nothing by itself: it opens
//     one object, for the one execution this launch starts, and it travels
//     as the value of that execution's marker (FUGARO_BUDGET_TOKEN, D15).
//     Its audience is therefore the override's — the project's readers:
//     launchers, anyone with log read, and (unverified, U2) anyone with
//     execution read — and not a peer run of the job, which holds the
//     object and no Cloud Run permission. It is never printed: the launch's
//     own errors are redacted of it (sealRedact), and it is never logged.
//   - The mint names exactly this run: fs, fr (the keys of the slug and run
//     id; the runner refuses a run whose names are not their own keys), fx
//     (the end of the identity), fp (the Fugaro project, which must equal
//     /fugaro/project) and rb (the launcher's own identity, which the
//     registry and outcome writes must carry).
//   - Every launch of an installation that has a database mints, whatever its
//     mode and whether or not a policy was published (AB4, F7): the mode is
//     the run's own business, read from the policy through the identity this
//     mint gives it. A launch on an installation with no database is refused
//     here, with the fix, rather than reaching a runner that would refuse it
//     after the claim, the task and the bucket writes.

// budgetBackendOn reports whether runs of the project use the budget backend:
// the installation has a database. That is the whole of it — the mode decides
// nothing here any more. A launch of a project whose mode is unset or observe
// mints exactly as one of an enforce project does, because the mode is not what
// a launch needs: it needs the database the run's identity comes from, and the
// mode a run is accounted for under is the published policy's, which the run
// itself reads (AB4, F7, R3). A backend with no signer cannot launch anything:
// say so.
//
// An installation with no database is refused, with the fix, and refused here
// rather than at some later door: every run has an identity and a gateway
// (spec §3), so a launch this CLI passed on would reach a runner that refuses
// it less clearly (internal/runner's strayTokenNoBackend, and AB1a's own
// bootstrap refusal) — after the claim, the task and the bucket writes. The
// database is `fugaro update`'s to create (AB7, R2), which is why the refusal
// names that command and not a hand edit of the config, and why it says who
// runs it in one wording (F5, and the same words internal/cli's own Firebase
// door uses).
func budgetBackendOn(lc *localcfg.Config) (bool, error) {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return false, noBudgetDatabaseRefusal(lc)
	}
	return true, budgetSignerOn(lc)
}

// noBudgetDatabaseRefusal is the one refusal of a launch (and of everything
// else that asks budgetBackendOn) on an installation whose config names no
// budget database: what is missing, what it costs — no identity, so nothing is
// launched — and the one command that fixes it, with who runs it.
//
// It names the project, so it reads the config rather than taking a nil one:
// budgetBackendOn, its only caller, has already read the block by the time it
// gets here.
func noBudgetDatabaseRefusal(lc *localcfg.Config) error {
	return userErr("project %s has no budget database (budget.rtdb_url is not set in its config), and every run has a budget identity, so nothing is launched: a project owner runs fugaro update --firebase-project %s --name %s once, which creates the database",
		lc.Name, firebaseProjectIDWord(lc.GCPProject), lc.Name)
}

// budgetSignerOn refuses a launch that needs a budget identity but has no
// signer to mint it with: the CLI holds the only copy of that permission, so
// nothing else can give a run its identity.
func budgetSignerOn(lc *localcfg.Config) error {
	if lc.Budget == nil || lc.Budget.TokenSigner == "" {
		return userErr("budget.token_signer is not set in project %s's config, so runs cannot be given a budget identity: a project owner runs fugaro update --firebase-project %s --name %s once", lc.Name, firebaseProjectIDWord(lc.GCPProject), lc.Name)
	}
	return nil
}

// budgetMintOn reports whether this launch must mint the run a budget
// identity: it always does, on an installation that has a database, and an
// installation that has none is refused instead (budgetBackendOn).
//
// Both readings that made this a decision of its own are gone (AB4, F7); the
// name stays because it is the question a launch asks — sealGate judges a
// launch that will mint by it, mintBudgetToken is the mint, and `doctor` reads
// it to say whether this installation's launches carry an identity — and what
// it answers is budgetBackendOn's own answer, which is now the whole condition:
// the database decides, and the local mode and the published policy, which used
// to be able to change it, do not. The context the policy read needed is gone
// with it; the signature keeps it, and the readers that already pass one.
func (e *cloudEnv) budgetMintOn(context.Context) (bool, error) {
	return budgetBackendOn(e.lc)
}

// projectPolicyRead is the CLI's read of the project's policy with the
// operator's own credentials: the read the CLI decides by (Readiness's D11
// gate, the launch gate, and in mintBudgetToken the decision whether a run
// needs a budget identity at all). A project whose config carries no database
// URL has no policy this CLI can see, and says so without a call.
// policyReadOnce is what callers use, so one launch reads it once.
//
// It is the read with projectpolicy's typed errors intact, not the wording
// (policyReadErr), for the one caller that words the version skew itself —
// the launch gate (internal/cli/run_allowlist.go, plan
// task 1.5 step 4), which names the release the policy model ships in beside
// the policy's own version, because what it refuses is a *launch* and the fix
// is on the operator's side. Every other caller wants policyReadErr.
//
// The keys this Fugaro does not know are refused here rather than in
// policyReadErr, because that refusal reads the policy the read returned
// rather than the error: a key nobody reads is a policy that never takes
// effect, and the launch gate must not pass over one either.
func projectPolicyRead(ctx context.Context, lc *localcfg.Config) (*projectpolicy.Policy, bool, error) {
	return policyReadWith(ctx, lc, projectpolicy.Read)
}

// repoPolicyRead is the repository stage's read of the same node, carried
// forward from projectpolicy.ShapeFreeFloor up
// (projectpolicy.ReadCarriedForwardShapeFree) instead of refused.
//
// The repository stage runs before the policy stage, and the node the first
// `fugaro update` after a version bump reads is written in the version behind:
// a strict read refuses it, the stage then passes no policy at all
// (repoPolicyInput), and internal/infra reads a nil policy as "nothing is
// proxied" — the repository root loses the bitbucket_proxied fact, the job's
// service account regains the accessor and the mount on bitbucket-token that
// the credentials-service proxy withholds, and the node's published image
// catalog and image map are not enforced. That is the HARD RULE's second
// command, so the stage reads the node the policy stage is about to write: a
// carry in memory states exactly what the operator's own migration writes from
// ShapeFreeFloor up (projectpolicy's doc on that floor is the argument), and
// nothing is written back here.
//
// A launch's memo does not carry (policyReadOnce.carry): that one is the read
// the gates and the launch decide by, where a node no version of this reader
// understands is a refusal rather than a document to enforce. The converging
// door's memo does, and it is this read, so every stage of one `fugaro update`
// answers from the node the same run's policy stage is about to write — the
// stages that store a secret, the Readiness gate and the repository stage
// included.
func repoPolicyRead(ctx context.Context, lc *localcfg.Config) (*projectpolicy.Policy, bool, error) {
	return policyReadWith(ctx, lc, projectpolicy.ReadCarriedForwardShapeFree)
}

// policyReadWith is the two reads above with the decode as its parameter: the
// config's own answer without a call, the client, and the unknown-keys refusal
// both of them want.
func policyReadWith(ctx context.Context, lc *localcfg.Config, read func(context.Context, *rtdb.Client) (*projectpolicy.Policy, bool, error)) (*projectpolicy.Policy, bool, error) {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, false, nil
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, false, err
	}
	p, found, err := read(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if found && len(p.UnknownKeys) > 0 {
		// The names are the node's own (a hand-writable object), so they are
		// printed the way every other name of that provenance in this package
		// is printed: through pluginwire.Printable, which is what
		// unknownKeysText does. The launch gate reads this policy and
		// shows the operator this sentence, so an unescaped name here would
		// rewrite the terminal line of a `fugaro run` refusal.
		return nil, false, userErr("%s lists %s, which this Fugaro does not know: a key nobody reads is a policy that never takes effect; fix the policy, or upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run 'fugaro update'",
			projectpolicy.PathShared(), unknownKeysText(p.UnknownKeys))
	}
	return p, found, nil
}

// policyReadErr words the errors projectpolicy.Read returns. What is left to
// the default case is a node the database handed over and this Fugaro could
// not read: not JSON, over MaxPolicyBytes, a version that is not an integer, a
// budget block the decoder refuses. That is the project's own published data,
// and the same class is the user's own error (exit 1) everywhere else in the
// CLI — dbErr and checkDBProject both call a bad node a user error. Only an
// outage is remote, and a run cannot fix either one. The node is named: a
// decoder message says what is wrong with the document, not which document it
// is.
func policyReadErr(err error) error {
	if err == nil {
		return nil
	}
	var ve *projectpolicy.VersionError
	var me *projectpolicy.MarkerError
	switch {
	case errors.As(err, &ve):
		// The remedy depends on the direction of the skew — an older binary
		// wants upgrading, and an older node is moved by whichever of the two
		// moves its version allows: `fugaro update`'s in-place rewrite for one
		// the migration chain carries, `fugaro project policy publish
		// --delete` and a fresh publish for one below the chain's first step
		// (never a re-run of init, which writes no policy node) — and
		// projectpolicy.SkewSentence is the one place that picks between the
		// sentences for this reader (the launch gate beside this file
		// words the same error through it, ReaderCLI, and so does the
		// runner's own refusal, ReaderRunner).
		return userErr("%s", projectpolicy.SkewSentence(ve, projectpolicy.ReaderCLI))
	case errors.As(err, &me):
		return userErr("%v", err)
	case errors.Is(err, rtdb.ErrPermission):
		return userErr("%v: the deployed rules no longer let this project read its own policy; if they are older than %s, run fugaro update again in your own terminal with this Fugaro, whose rules stage deploys the generation that marker is about", err, projectpolicy.PathRulesMarker())
	case errors.Is(err, rtdb.ErrUnavailable):
		return remote(err)
	default:
		// An error that already carries an exit code is one of this file's own
		// refusals (projectPolicyRead's unknown keys, the only one that can
		// reach here), and it already names the node: prefixing it again would
		// print the same path twice.
		var ee *ExitError
		if errors.As(err, &ee) {
			return err
		}
		return userErr("%s: %w", projectpolicy.PathShared(), err)
	}
}

// policyReadOnce memoizes one CLI run's read of the policy: a launch asks twice
// on one path — the gate before anything of it exists, and the mint decision —
// where two reads could disagree about the policy a single launch is admitted
// against, and the Readiness gate planRepo reads shares that one answer with
// every other caller of the run. A launch is not marked (carry below) and reads
// the node strictly, which is what the memo must do for a decision. A run that
// converges a checkout is marked, and every caller of that run then reads the
// node the same run's policy stage is about to write, so the read is one node's
// answer from the first stage to the last. The answer cannot change under any of
// them (and where it could, the run reads the node itself and is the one that
// counts).
//
// What is memoized is the raw read, with projectpolicy's typed errors intact,
// because two callers word the same error differently: the launch gate names the
// release a policy's version is from (policySkewRefusal), and every other caller
// wants policyReadErr.
type policyReadOnce struct {
	// mu makes the memo safe for the goroutines a batch of launches shares one
	// cloudEnv with (a best-of-N launch, or the concurrent same-run-ID test):
	// the first caller reads, the others wait for that one answer.
	mu    sync.Mutex
	done  bool
	p     *projectpolicy.Policy
	found bool
	err   error
	// carry makes the read the converging door's own: the node is read as the
	// same run's policy stage is about to write it
	// (projectpolicy.ReadCarriedForwardShapeFree, repoPolicyRead) instead of
	// refused where it is a predecessor or says the word the budget mode's
	// vocabulary dropped. It is what the upgrade's HARD RULE needs of every
	// stage of that run: the node the release behind published is exactly the
	// one this release's strict reader refuses, and the same run is the one that
	// rewrites it — a stage that stopped, or answered "no policy", would leave
	// the operator a second command, or would store a secret in the wrong place
	// for a repository the node admits (initsecrets' projectScope).
	//
	// It is set by initRun.policyMemo, the one way a run that converges reaches
	// this memo, and never by a launch: a launch decides by the node and must
	// refuse one no version of this reader understands (docs on repoPolicyRead).
	carry bool
}

// read is the memoized read: the policy, whether one was published, and the
// read's own error.
func (p *policyReadOnce) read(ctx context.Context, lc *localcfg.Config) (*projectpolicy.Policy, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		p.done = true
		if p.carry {
			p.p, p.found, p.err = repoPolicyRead(ctx, lc)
		} else {
			p.p, p.found, p.err = projectPolicyRead(ctx, lc)
		}
	}
	return p.p, p.found, p.err
}

// policyMode is the budget mode the published node carries, "" where no node
// was published, none could be read, or there is no local config to read one
// over. It is the switch's fourth condition, read by the launch and handed to
// credentialsServiceLaunchEnv (D2071): the credentials service is reached
// through the gateway, and the gateway exists only in `observe` and `enforce`,
// so a repository on a project whose mode is `off` is handed no address and
// keeps the mounts it has.
//
// It is the NODE's mode and not the local config's (lc.BudgetMode), and the
// two differ exactly where it matters: a run's mode is the node's (the
// runner's policyCeiling writes the node's mode over the deploy-time one), so
// a project that lowered it after the last apply would be handed an address
// its own runner refuses to use. A read that failed or found nothing answers
// "" deliberately rather than falling back to the local mode: every published
// node carries a mode (buildProjectPolicy refuses one without it), so a
// missing node is a project that has not published, and its runs are the ones
// the launch already refuses or runs on the environment ceiling. What the
// fail-closed direction costs a switched repository is its address and nothing
// else, which is D2071's own reading of it: the two ways this can answer ""
// without refusing the launch — no database to read a node from, and none
// published — are also the two ways the repository's own root read no mode when
// it was applied, so that root kept every mount (infra.ModelSecretsServed's
// `none`) and the launch it withholds the address from is one whose job still
// holds the credential it reads. `fugaro doctor` is where an operator reads why.
func (e *cloudEnv) policyMode(ctx context.Context) string {
	if e.lc == nil {
		return ""
	}
	p, found, err := e.policy.read(ctx, e.lc)
	if err != nil || !found || p == nil {
		return ""
	}
	return policyBudgetMode(p)
}

// policyBudgetMode is the mode a node carries, "" for the nil policy that
// stands for a node nobody read — the same reading internal/infra makes of its
// Inputs.Policy (D2071). It is here rather than beside each caller because the
// switch's fourth condition is read in two places of the same run: the launch
// (policyMode) and the repository door's record (recordCredentialsServiceReady,
// init.go), which must answer as the spec below them does.
func policyBudgetMode(p *projectpolicy.Policy) string {
	if p == nil {
		return ""
	}
	return p.Budget.Mode
}

// exists is read in the CLI's wording, for the callers that only ask whether
// the project has published a policy (Readiness's D11 gate). A node written in
// an older version is one of those: it is there, and the policy stage of the
// same `fugaro update` migrates it in place before any run is admitted by it.
// Refusing it here stopped the run at the stage *before* that one — the
// repository stage's readiness read, which no door orders after the policy
// stage — and told the operator to delete the node and publish again, for a
// document the run it stopped was about to rewrite. What this read may not
// swallow is a node it cannot go on with at all: one written by a *newer*
// Fugaro (the reader is the older side there, and no stage of this run can
// read or migrate it) and one no version words (malformed, oversized,
// unreadable) are the same refusal planRepo made before, at the same point.
func (p *policyReadOnce) exists(ctx context.Context, lc *localcfg.Config) (bool, error) {
	_, found, err := p.read(ctx, lc)
	if olderPolicyNode(err) {
		return true, nil
	}
	return found, policyReadErr(err)
}

// olderPolicyNode is whether err is the refusal of a published node older than
// this Fugaro, the one direction a later stage of the same run repairs.
func olderPolicyNode(err error) bool {
	var ve *projectpolicy.VersionError
	return errors.As(err, &ve) && ve.Found < ve.Understood
}

// chainLimits is the four --max-* flags of one launch, parsed once
// (chainLimitsFlag): the chain's dollar, token, wall-time and review-round
// limits (docs/run-limit-carry-over §2, D1041, D1042). Every dimension is
// optional and a launch that names none carries no chain at all: the task is
// then byte-identical to what the previous release wrote and the run gets
// today's per-run caps, which is what makes the feature opt-in.
//
// The four are the launcher's own numbers and never the caller's: they are
// written into the task's overrides (finishLaunch) and the runner merges them
// again at bootstrap as the last tighten-only layer (D1044), so a task written
// by hand or by an older CLI reaches it unchecked too.
type chainLimits struct {
	usd    *float64
	tokens *int64
	time   time.Duration
	rounds *int
}

// set reports whether this launch names any of the four.
func (c chainLimits) set() bool {
	return c.usd != nil || c.tokens != nil || c.time > 0 || c.rounds != nil
}

// layer is the four limits as the policy layer they merge as: the chain layer,
// merged last and under every layer above (policy.SourceChain).
func (c chainLimits) layer() policy.Layer {
	l := policy.Layer{MaxRunTime: c.time}
	if c.usd != nil {
		l.PerRunUSD = *c.usd
	}
	if c.tokens != nil {
		l.MaxRunTokens = *c.tokens
	}
	if c.rounds != nil {
		l.MaxReviewRounds = int64(*c.rounds)
	}
	return l
}

// chainLimitsFlag parses the four --max-* flags of a launch, once, for every
// launch shape below. The syntax is refused here — before openCloud, so a
// mistyped number costs no cloud call — and a value that would loosen is
// refused later, against the policy this launch resolves (chainLimitRefusal):
// the two are different refusals because only the second needs a read.
func chainLimitsFlag(cmd *cobra.Command, o *runOptions) error {
	f := cmd.Flags()
	if f.Changed("max-usd") {
		v, err := strconv.ParseFloat(o.maxUSD, 64)
		if err != nil {
			return userErr("--max-usd %q must be a number of dollars, such as 5 or 12.5", o.maxUSD)
		}
		if !(v > 0) || math.IsInf(v, 0) {
			return userErr("--max-usd %s must be greater than 0", o.maxUSD)
		}
		o.limits.usd = &v
	}
	if f.Changed("max-tokens") {
		v, err := strconv.ParseInt(o.maxTokens, 10, 64)
		if err != nil {
			return userErr("--max-tokens %q must be a whole number of tokens, such as 900000", o.maxTokens)
		}
		if v <= 0 {
			return userErr("--max-tokens %d must be greater than 0", v)
		}
		o.limits.tokens = &v
	}
	if f.Changed("max-time") {
		d, err := time.ParseDuration(o.maxTime)
		if err != nil {
			return userErr("--max-time %q must be a duration such as 45m or 2h", o.maxTime)
		}
		if d <= 0 {
			return userErr("--max-time %s must be greater than 0", o.maxTime)
		}
		o.limits.time = d
	}
	if f.Changed("max-review-rounds") {
		n, err := strconv.Atoi(o.maxReviewRounds)
		if err != nil {
			return userErr("--max-review-rounds %q must be a whole number of rounds, such as 3", o.maxReviewRounds)
		}
		o.limits.rounds = &n
	}
	return nil
}

// chainLimitPolicy is the effective policy a launch's chain limits are merged
// under, and the rigor mode the run resolves: the ceiling, the default branch's
// fugaro.yaml and the run's own file, in the order the runner merges them at
// bootstrap (internal/runner's resolvePolicy, D1044).
//
// The launcher resolves it rather than trusting the launch's own numbers
// because the refusal of a loosening limit is the launcher's (D1043): the
// repository layer's looser values are dropped and recorded, which is right
// for a file a team wrote once, and wrong for a number a caller typed at a
// terminal a second ago — a silent drop tells them their limit took effect
// when it did not.
type chainLimitPolicy struct {
	ceiling policy.Layer
	// under is the default branch's layer and the run's own, in that order,
	// and always two entries: a launch the launcher could not read a file for
	// merges the zero layer, so the chain layer is the fourth whatever the
	// launch resolved.
	under []policy.Layer
	// fromNode names the keys the project's published policy node set, which
	// are the ceiling's own only by name: the merge calls its first layer
	// "ceiling", and a figure the node wrote must be reported as the node's
	// (H1, internal/runner's policySources says the same at bootstrap).
	fromNode map[string]bool
	mode     rigor.Rigor
}

// chainLimitPolicyOf resolves the layers above for a launch. cfg is the file
// the run will read (launchLevelsConfig), which is never nil: a launcher with
// no checkout of the repository merges the project layer the task carries.
func chainLimitPolicyOf(ctx context.Context, env *cloudEnv, spec *task.Spec, cfg *config.Config) chainLimitPolicy {
	mode := rigor.Resolve(cfg.RigorConfig(), spec.RigorRequest(spec.Paths)).Level
	c := chainLimitPolicy{
		ceiling: modeScaledCeiling(ceilingLayer(env.lc), mode),
		under:   []policy.Layer{defaultBranchPolicyLayer(ctx, spec.Repo), runner.FileLayer(cfg)},
		mode:    mode,
	}
	// The node is what the owner says now, where the ceiling is the copy the
	// job was deployed with, and the runner reads it that way at bootstrap
	// (H1, internal/runner's policyCeiling): a launch judged against the older
	// copy would refuse a limit the run would carry, or accept one the node
	// lowers. A read that fails is the gate's own refusal a few lines further
	// on (launchPolicyGates): this fails towards the environment's figures
	// rather than refusing a launch twice.
	p, found, err := env.policy.read(ctx, env.lc)
	if err != nil || !found || p == nil {
		return c
	}
	c.fromNode = map[string]bool{}
	if v := p.Budget.PerRunUSD; v > 0 {
		c.ceiling.PerRunUSD, c.fromNode[policy.KeyPerRunUSD] = v, true
	}
	if v := p.Budget.MaxRunTokens; v > 0 {
		c.ceiling.MaxRunTokens, c.fromNode[policy.KeyMaxRunTokens] = v, true
	}
	return c
}

// modeScaledCeiling is the ceiling the runner's bootstrap merge will build for
// this run: the deployed dollar cap scaled by the run's own rigor mode
// (config.RigorBudgetCap, D16, plan T6.1), and nothing else touched.
//
// The launcher's own ceiling is the deployed figure, and the runner's is not:
// envCeiling scales the cap by the mode, so a run of mode Pro starts from 85%
// of it. Judging the merge's chain layer against the unscaled cap accepts a
// --max-usd between the two and writes it into the task, where the runner's own
// merge then drops it as looser than the ceiling — the silent drop D1043 exists
// to prevent, with the launcher reporting a figure that never took effect. Only
// the dollars are scaled, and only the ceiling's own cap: the token ceiling is
// the deployed one (envCeiling), and a cap the published node sets is the
// owner's word taken exactly as it stands (policyCeiling, H1) — which is why
// this runs before the node's keys are folded in above it.
func modeScaledCeiling(l policy.Layer, mode rigor.Rigor) policy.Layer {
	if l.PerRunUSD <= 0 {
		return l
	}
	deployed, err := pricing.FromUSD(l.PerRunUSD)
	if err != nil {
		return l
	}
	l.PerRunUSD = config.RigorBudgetCap(mode, deployed).USD()
	return l
}

// merge merges the launch's chain layer under the policy above and returns the
// effective policy. The ignored entries it appends past base's are the chain
// layer's own — Merge appends them in layer order — and they are the ones the
// launcher refuses (chainLimitRefusal); the ones before them are a file's, and
// a file's looser value is dropped and recorded as it always has been.
func (c chainLimitPolicy) merge(chain policy.Layer) (policy.Effective, int) {
	base := policy.Merge(c.ceiling, c.under...)
	e := policy.Merge(c.ceiling, append(slices.Clone(c.under), chain)...)
	for k := range c.fromNode {
		if e.Sources[k] == policy.SourceCeiling {
			e.Sources[k] = policy.SourcePolicy
		}
	}
	for i := range e.Ignored {
		if e.Ignored[i].Source == policy.SourceCeiling && c.fromNode[e.Ignored[i].Key] {
			e.Ignored[i].Source = policy.SourcePolicy
		}
	}
	return e, len(base.Ignored)
}

// chainLimitRefusal is the refusal of one value the merge dropped as looser
// (D1043): it names the flag, both numbers and the layer that set the lower
// one, because a caller who typed a number has to be told it did not take
// effect and where the number that beat it lives. nil for a key that is not
// one of the four — the merge records every key's losers, and only these four
// are a launch argument.
func chainLimitRefusal(ig policy.Ignored) error {
	flag, dim := "", ""
	switch ig.Key {
	case policy.KeyPerRunUSD:
		flag, dim = "max-usd", "chain dollar limit"
	case policy.KeyMaxRunTokens:
		flag, dim = "max-tokens", "chain token limit"
	case policy.KeyMaxRunTime:
		flag, dim = "max-time", "chain wall-time limit"
	case policy.KeyMaxReviewRounds:
		flag, dim = "max-review-rounds", "chain review-round limit"
	default:
		return nil
	}
	return userErr("--%s %s is above the %s this launch resolves: %s sets %s, and a launch can only tighten a limit, never loosen one. Pass %s or less, or raise the limit where it is set",
		flag, ig.Value, dim, layerWord(ig.Source), ig.Effective, ig.Effective)
}

// layerWord names a policy layer the way a refusal reads it.
func layerWord(src string) string {
	switch src {
	case policy.SourceCeiling:
		return "the owner's ceiling"
	case policy.SourcePolicy:
		return "this project's published policy"
	case policy.SourceDefaultBranch:
		return "the repository's default branch"
	case policy.SourceBranch:
		return "the branch this run checks out"
	case policy.SourceChain:
		return "this launch's own chain layer"
	}
	return "the " + src + " layer"
}

// chainRoundCeiling is the rounds one run of mode m spends on its review loop
// with nothing else set: the mode's own senior row, held inside the recipe's
// bound (internal/runner's setRounds, recipe.MaxReviewRounds).
//
// It is the figure a refusal about the review-round dimension quotes, and it
// is quoted from the functions rather than from the mode table: the plan that
// asked for this flag named rigor.RoundCeiling, which this tree no longer has
// (DefaultRounds and AdversarialDefault replaced it, #1483) and whose output
// the mode's row was never equal to anyway — POC's and Hotfix's rows sit below
// the floor that function used to apply and Pro's and Critical's above it
// (docs/run-limit-carry-over F15). Quoting the mode's own row through this
// function is the narrower, true claim: the number a run of this mode starts
// from is this one, and it is a default rather than a cap — the recipe's bound
// is the only ceiling either number is held under.
func chainRoundCeiling(m rigor.Rigor) int {
	return min(rigor.DefaultRounds(m).Senior, recipe.MaxReviewRounds)
}

// budgetPrecheck refuses a launch the budget would refuse anyway (see
// budget.Precheck): exit 1 with the reason for a kill switch, a missing cap
// or too little headroom, exit 2 when the database can not be read (it fails
// closed). --no-budget-check skips this and only this: the token is still
// minted, and the run itself is held to every limit. Anyone who may launch
// may use it (it loosens nothing the rules and the runner do not enforce
// again); it says so on stderr.
func (e *cloudEnv) budgetPrecheck(ctx context.Context, slug string, spec *task.Spec, stderr io.Writer) error {
	on, err := budgetBackendOn(e.lc)
	if err != nil || !on {
		return err
	}
	if e.noBudgetCheck {
		fmt.Fprintln(stderr, "budget pre-check skipped (--no-budget-check); the run is still held to the budget's limits when it starts")
		return nil
	}
	db, err := newBudgetClient(ctx, e.lc.Budget.RTDBURL, e.lc.Endpoints.NoAuth)
	if err != nil {
		return err
	}
	// An oauth run has no dollar cap to check; its kill switches still count.
	capped := true
	if c := checkoutConfig(ctx, spec.Repo); c != nil && c.Agent.Auth == "oauth" {
		capped = false
	}
	res, err := budget.Precheck(ctx, db, budget.PrecheckInput{Project: e.lc.Name, Slug: slug, Capped: capped, Now: time.Now()})
	if err != nil {
		return remote(fmt.Errorf("the budget database could not be read, so the launch of %s/%s is refused (fail closed): %w; fugaro run --no-budget-check launches anyway, and the run then halts if the backend stays unreachable", slug, spec.RunID, err))
	}
	for _, n := range res.Notes {
		fmt.Fprintf(stderr, "budget: %s\n", oneLine(n))
	}
	if res.Refused {
		return userErr("the budget refuses the launch of %s/%s: %s (%s, %s); fugaro run --no-budget-check skips this check, but the run is held to the same limits when it starts",
			slug, spec.RunID, oneLine(res.Detail), res.Reason, res.Scope)
	}
	return nil
}

// batchBudgetPrecheck is budgetPrecheck generalized to a best-of-N batch
// (fugaro run --attempts N, design generic-tool review 2026-10-13): it
// leases n * budget.MinLaunchHeadroom once, before any of the batch's n
// attempts, instead of n separate single-headroom leases that would let a
// batch with headroom for only the first attempt start, then halt attempts
// 2..n at their own pre-check. Its notes and the --no-budget-check warning
// print once here, for the whole batch; each attempt's own finishLaunch
// call then skips budgetPrecheck entirely (skipBudget).
func (e *cloudEnv) batchBudgetPrecheck(ctx context.Context, slug, repo string, n int, stderr io.Writer) error {
	on, err := budgetBackendOn(e.lc)
	if err != nil || !on {
		return err
	}
	if e.noBudgetCheck {
		fmt.Fprintln(stderr, "budget pre-check skipped (--no-budget-check); the batch's runs are still held to the budget's limits when they start")
		return nil
	}
	db, err := newBudgetClient(ctx, e.lc.Budget.RTDBURL, e.lc.Endpoints.NoAuth)
	if err != nil {
		return err
	}
	capped := true
	if c := checkoutConfig(ctx, repo); c != nil && c.Agent.Auth == "oauth" {
		capped = false
	}
	res, err := budget.Precheck(ctx, db, budget.PrecheckInput{
		Project: e.lc.Name, Slug: slug, Capped: capped, Now: time.Now(), Amount: budget.MinLaunchHeadroom * budget.Micros(n),
	})
	if err != nil {
		return remote(fmt.Errorf("the budget database could not be read, so the batch of %d on %s is refused (fail closed): %w; fugaro run --no-budget-check launches anyway, and each run then halts if the backend stays unreachable", n, slug, err))
	}
	for _, note := range res.Notes {
		fmt.Fprintf(stderr, "budget: %s\n", oneLine(note))
	}
	if res.Refused {
		return userErr("the budget refuses this batch of %d runs on %s: %s (%s, %s); --attempts needs headroom for all %d at once, not one at a time; fugaro run --no-budget-check skips this check, but each run is held to the same limits when it starts",
			n, slug, oneLine(res.Detail), res.Reason, res.Scope, n)
	}
	return nil
}

// sealGate refuses a launch that will mint a budget token when the job's image
// runs a fugaro older than sealSince: such a runner formats a marker value it
// cannot parse into its bootstrap error — the launch's seal key, into the
// run's record — and reads the token from runs/, where the launcher no longer
// writes it (D19, §7.4). Every launch of an installation with a database mints
// (AB4, F7), so this gate judges every launch of one — a launch that mints
// nothing is a launch budgetMintOn has already refused, and this gate hands
// that refusal back rather than judging an image for a launch that will not
// happen. The judge is the one every *Since gate uses (checkImageSince, or
// checkCloneImageSince for a checkout: clone workflow): a release older than
// sealSince and a missing build record refuse. An image built from a base that
// is not a release (a development build) passes when its build record says the
// base's own fugaro reads the sealed token, is refused when the record shows it
// was built before the seal's runner half existed, and is otherwise only warned
// about, because whether it supports the seal cannot be shown
// (sealDevRefusal; the owner's answer of 2026-10-17). A checkout: clone job
// has no build record, so a development base there is only warned about.
func (e *cloudEnv) sealGate(ctx context.Context, slug string, spec *task.Spec, kind string, clone bool, warn io.Writer) error {
	mint, err := e.budgetMintOn(ctx)
	if err != nil || !mint {
		return err
	}
	const subject = "a launch that mints a budget token (the token is sealed and its key is in FUGARO_BUDGET_TOKEN)"
	const knows = "the sealed budget token"
	if clone {
		// A development base is judged by the seal's own warning here, not by
		// checkCloneImageSince: that judge would warn that the ref is not a
		// release and that whether its runner knows the seal is not checked —
		// the same fact, without the stake or the cure — and the launch would
		// then carry two warnings saying one thing. Everything
		// checkCloneImageSince does beyond that warning (a missing base image,
		// a release older than the seal) is about refs that are releases, or
		// about none at all, so it still runs for them.
		ref := e.lc.BaseImage(config.BaseKind)
		if _, release := releaseOf(ref); ref != "" && !release {
			fmt.Fprintf(warn, "warning: the checkout: clone job image %s is not a release base image: %s\n", pluginwire.Printable(ref), sealDevNote(spec, true))
			return nil
		}
		return checkCloneImageSince(e.lc, spec, sealSince, subject, knows, "", warn)
	}
	return checkImageSinceDev(ctx, e, slug, spec, kind, sealSince, subject, knows, "", sealDevRefusal(spec), warn)
}

// sealDevRefusal judges a job image built from a base that is not a release
// (a development or hand-pushed image) for the seal gate, from its build
// record, whose render half the BASE's own fugaro wrote:
//
//   - the record says that fugaro reads the sealed token
//     (imagecheck.Record.SealedBudgetToken): supported, no warning;
//   - otherwise, a record built before the seal's runner half existed
//     (sealRunnerIntroduced) cannot come from a base that has it: refused,
//     with the image named;
//   - otherwise (built after, or no built_at) it cannot be shown either way —
//     a job image rebuilt today from an old development base looks the same
//     as one from a base with only the runner half — and the gate only warns.
func sealDevRefusal(spec *task.Spec) func(rec *imagecheck.Record, ref string) (bool, string, error) {
	return func(rec *imagecheck.Record, ref string) (bool, string, error) {
		if rec.SealedBudgetToken {
			return true, "", nil
		}
		if rec.BuiltAt.IsZero() || !rec.BuiltAt.Before(sealRunnerIntroduced) {
			return false, sealDevNote(spec, false), nil
		}
		return false, "", userErr("a launch that mints a budget token (the token is sealed and its key is in FUGARO_BUDGET_TOKEN) needs a job image whose runner knows the sealed budget token, "+
			"but the job image of %s workflow %s was built from %s, which is not a release base image, on %s, and so predates the seal (its runner half was introduced on %s): "+
			"its runner would echo the seal key into the run's record. Rebuild the base image from a checkout that has the seal and then the job image from it with fugaro image build --repo %s --workflow %s, "+
			"or move the workflow to a release base image (fugaro %s or later) with fugaro image refresh in its checkout, in your own terminal window",
			spec.Repo, spec.Workflow, ref, rec.BuiltAt.UTC().Format(time.RFC3339), sealRunnerIntroduced.UTC().Format(time.RFC3339), spec.Repo, spec.Workflow, sealSince)
	}
}

// sealDevNote is what the seal gate adds to its warning about a development
// base it cannot judge: the stake and the cure.
func sealDevNote(spec *task.Spec, clone bool) string {
	if clone {
		return fmt.Sprintf("if it predates the seal, its runner puts this launch's seal key into the run's record, where peer runs of the repository can read it; "+
			"move base_images.base to a release base image (fugaro %s or later) with fugaro update --base base, in your own terminal window", sealSince)
	}
	return fmt.Sprintf("if that base predates the seal, its runner puts this launch's seal key into the run's record, where peer runs of the repository can read it; "+
		"rebuild the base image from a checkout that has the seal and the job image from it with fugaro image build --repo %s --workflow %s (its record then says sealed_budget_token and this warning goes)",
		spec.Repo, spec.Workflow)
}

// proxyGate refuses a launch of a proxied repository that the job it would
// start could not carry (G14): the job image's runner has to know the proxy,
// and the backend has to be one the credentials service can serve at all.
//
// Whether a repository is proxied has ONE source, the published node's
// bitbucket_token block (G15) — the document the credentials service itself
// reads when a job asks it for the token — and this gate reads it through the
// launch's own memoized read, beside the allow-list's. What it asks of the node
// is what internal/infra's resolveBitbucketProxied asks of it for a repository
// root: this installation carries the service (its credentials_service block —
// the address `fugaro update` writes wherever it deploys one, and the one fact
// that puts it on a repository's job) and the node admits this repository with
// proxy: true. The operator's local project_secrets entry is
// deliberately not a second input: an entry that disagrees with the node is
// refused by `fugaro update --repo`, and answering the same question twice on
// the launch path is what G15 forbids.
//
// The node's answer is not the whole of proxiedness, and this gate asks the
// other half the way the launch itself does: a run is proxied only if its
// execution is handed the service's address, which is
// credentialsServiceLaunchEnv's answer — the installation has the service, the
// repository's own switch is on (its record, or the root this machine's tfvars
// deploy), and its auth is one the service serves
// (infra.CredentialsServiceServes). A gate reading the node alone refuses a
// repository whose node says proxy: true while its own switch is off, and the
// refusal is then a claim about a credential the job in fact holds: no address
// is written for that repository, its provider is opened with the ordinary
// Bitbucket token, and the run works. Asked this way the gate is the runner's
// own rule (proxiedBitbucket, which reads the same variable off the job's
// environment) and not a superset of it.
//
// The job half of that answer is asked with no job name, so what this gate
// reads through the function is the repository's own switch and the auth its
// runs use — the repository's facts, and not the job's environment — and they
// stay the repository's facts whichever job a run lands on, a Bitbucket
// workflow that names a profile now included (S4, D2304). The address is not
// written on a repository job alone: the deployer hands a shared Bitbucket job
// the service's URL too (internal/infra's ProjectJobs), and the runner's own
// rule (proxiedBitbucket) reads a run there off that URL and the node's block,
// whatever this gate's repository-side answer was.
//
// A launch that is not proxied is judged by every gate it always was and
// nothing about it changes (G9): an installation that carries no service, no
// node, no block, a repository the block does not admit, and a repository
// whose own switch is off all answer "not proxied" here. The run's
// own bootstrap decides the same fact from the same documents and refuses an
// identity it cannot work with (startProxiedGit), so a launch whose image this
// gate could not judge — one built from a base with no release tag — fails
// there rather than silently doing nothing.
func (e *cloudEnv) proxyGate(ctx context.Context, slug string, spec *task.Spec, kind string, clone bool, warn io.Writer) error {
	if !e.proxiedRepo(ctx, slug, spec) {
		return nil
	}
	if launchBackend(e.be) == backend.CloudBatch {
		return userErr("%s", proxiedOnBatchMessage(spec.Repo))
	}
	const subject = "a run of a repository whose Bitbucket access is brokered (the published policy admits it with proxy: true, so its job holds no Bitbucket token)"
	const knows = "the Bitbucket proxy (the run's loopback forwarder and the git environment that points at it, both of which reach Bitbucket through the installation's credentials service)"
	if clone {
		return checkCloneImageSince(e.lc, spec, proxySince, subject, knows, "", warn)
	}
	return checkImageSince(ctx, e, slug, spec, kind, proxySince, subject, knows, "", warn)
}

// serviceAuthGate refuses a launch of a repository whose model credential the
// credentials service serves (B2.7's flip) on a job image whose runner predates
// the service as that credential's source (serviceAuthSince): the job such a
// root deploys mounts neither the Anthropic key nor the Claude OAuth token, so
// a runner that does not know to call the service has no model credential at
// all.
//
// The condition is the launch's own, asked of the function that answers it for
// the launch and not of a copy of its clauses, exactly as proxiedRepo asks it:
// the run's agent.auth, read the way the run itself will read it (the ref's
// fugaro.yaml, else this machine's checkout), is one the service serves to a
// switched repository (config.ServiceServedAuths, and infra's
// CredentialsServiceServes behind it), and the execution is really handed the
// service's address (credentialsServiceLaunchEnv). The two together are the
// whole of "this job holds no model mount of its own": a repository whose
// document could not be read at all leaves the auth unknown, and this gate
// refuses nothing there — the shape is a launch from outside a checkout (the
// desktop console's own), where the repository's document is the only thing
// that could say `api-key` or `oauth`, and treating "unknown" as "switched"
// would refuse every documentless `vertex` repository, whose job keeps every
// mount and works.
//
// It is judged beside the proxy's and the seal's gates, from the same build
// record and the same local config, and before the claim and the task like
// every gate there. A launch whose repository the service does not serve — no
// service block, a repository whose own switch is off, a mode that is off, a
// `vertex` or `cascade` auth, a document this launcher cannot read — is not
// judged at all.
func (e *cloudEnv) serviceAuthGate(ctx context.Context, slug string, spec *task.Spec, kind string, clone bool, warn io.Writer) error {
	auth := e.switchedServiceAuth(ctx, spec)
	if auth == "" {
		return nil
	}
	subject := fmt.Sprintf("a run of a repository whose model credential is brokered (agent.auth %s on a repository the credentials service serves, so its job mounts neither the Anthropic key nor the Claude OAuth token)", auth)
	const knows = "the credentials service as the source of its model credential (the key and the account its own job no longer holds)"
	if clone {
		return checkCloneImageSince(e.lc, spec, serviceAuthSince, subject, knows, "", warn)
	}
	return checkImageSince(ctx, e, slug, spec, kind, serviceAuthSince, subject, knows, "", warn)
}

// chainRoutingGate refuses a launch of a repository whose project publishes a
// routing table where a model the run can call has no entry (R22, D3172): such
// a run has no source to be served from at all, and it is refused here — before
// the claim and before the task, like every gate in this file — rather than by
// the run that cannot start.
//
// It reads the project's table (the local config's `model_routing`, the
// document `fugaro update` publishes from, CS11) and the models the rungs can
// choose, the same two the launch's grant is read from (launchRunModels), so
// the sources asked for and the refusal are one answer.
//
// A repository the table does not serve is not judged either: a project that
// publishes no table and one whose every source is admitted to another
// repository are one answer here (infra.ChainPlanFor), because such a run goes
// out on the release-1 bridge exactly as it did (O7, R25) and a model no entry
// matches there is the config's own refusal to make. The table governs a run's
// models only where it serves the run.
func (e *cloudEnv) chainRoutingGate(ctx context.Context, spec *task.Spec) error {
	if e.lc == nil || !infra.ChainPlanFor(e.lc, spec.Repo).Serves {
		return nil
	}
	for _, model := range launchRunModels(ctx, e, spec) {
		if _, err := routing.Resolve(e.lc.ModelRouting, model); errors.Is(err, routing.ErrNoEntry) {
			return userErr("no model_routing entry matches %s: this run has no source to be served from, so add one with fugaro routing set", oneLineCLI(model))
		}
	}
	return nil
}

// switchedServiceAuth is the auth a switched repository's run reaches the
// credentials service for its model calls under, "" where this launch's run is
// not one: the document's auth is one the flip list carries
// (config.ServiceServedAuths) AND the execution really is handed the service's
// address.
//
// The second half is credentialsServiceLaunchEnv's own answer, for proxiedRepo's
// reason: a repository whose root was applied with the switch off, or whose
// project's mode is off, is handed no address, keeps every mount it had, and
// would work on the oldest image there is. Asking the same function keeps this
// gate from disagreeing with the launch it judges — one reader, one answer.
//
// An unknown document answers "" here, so the gate refuses nothing on the
// console's launches (see serviceAuthGate).
func (e *cloudEnv) switchedServiceAuth(ctx context.Context, spec *task.Spec) string {
	if e.lc == nil || e.lc.CredentialsService == nil {
		return ""
	}
	auth, deployed := e.repoServiceAuth(ctx, spec, io.Discard)
	if !slices.Contains(config.ServiceServedAuths, auth) {
		return ""
	}
	if len(credentialsServiceLaunchEnv(e.lc, "", spec.Repo, auth, e.policyMode(ctx), deployed, false)) == 0 {
		return ""
	}
	return auth
}

// vaultGate refuses a launch of a repository whose workflow secrets the
// credentials service fetches (B3.8's flip) on a job image whose runner
// predates that fetch (vaultSince): the job such a root deploys mounts none of
// the workflow's own `secrets:`, so a runner that does not know to fetch them
// has no value for a declared one and stops at bootstrap, before any stage.
//
// The condition is the launch's own (switchedVaultSecrets), read from the same
// documents and the same functions the launch itself reads: the flip list
// carries the workflow class, the execution really is handed the service's
// address, and the repository's own document declares at least one secret for
// this workflow — the very declaration the drop takes the mount of. It is
// judged beside the model credential's gate, from the same build record and the
// same local config, and before the claim and the task like every gate there. A
// launch the flip does not touch — no service block, a repository whose own
// switch is off, a repository whose workflow declares no secret at all, a
// document this launcher cannot read — is not judged.
func (e *cloudEnv) vaultGate(ctx context.Context, slug string, spec *task.Spec, kind string, clone bool, warn io.Writer) error {
	if !e.switchedVaultSecrets(ctx, spec) {
		return nil
	}
	const subject = "a run of a repository whose workflow secrets are brokered (the job mounts none of the workflow's own secrets, and the run fetches each declared one from the installation's credentials service at bootstrap)"
	const knows = "the credentials service as the source of its workflow's own secrets (the values its job no longer mounts)"
	if clone {
		return checkCloneImageSince(e.lc, spec, vaultSince, subject, knows, "", warn)
	}
	return checkImageSince(ctx, e, slug, spec, kind, vaultSince, subject, knows, "", warn)
}

// switchedVaultSecrets is whether this launch starts a run whose job holds no
// mounted value for its workflow's own secrets, which is the run whose image
// has to know the fetch: the S3 flip is on (config.VaultFetchesClass, which the
// list has answered true for since the flip's second commit filled it — an
// empty list makes this gate refuse nothing), the
// document declares at least one secret for this workflow, and the execution
// really is handed the service's address.
//
// The address half is credentialsServiceLaunchEnv's own answer, for
// switchedServiceAuth's reason: asking the function the launch itself asks
// keeps this gate from disagreeing with the launch it judges. A repository
// whose root was applied with the switch off, or whose project's mode is off,
// is handed no address, keeps every mount it has, and would work on the oldest
// image there is (G17).
//
// The declaration half is what tells a repository the flip touches from one it
// does not: the drop is per repository (Go's dropVaultMounts reads the
// repository's own document), so a switched repository whose workflow declares
// no secret loses nothing and is not refused here. It is deliberately the
// DECLARATION and not a resolution of which of its names the flip really takes:
// a name D2313 leaves storeless for this repository drops no mount either, and
// the gate refuses its launch on an old image anyway — the conservative
// direction, on a run that has no value to fetch under that ruling either way.
//
// Two launch shapes are not judged, and neither loses a mount to the flip:
//
//   - A REVIEW launch. It runs on the review-only job, whose container mounts
//     nothing at all (O1, reviewVariant) — so the flip takes nothing away from
//     it, and it works on the oldest image there is. The plan's own words for
//     this task are "review runs unchanged".
//   - A document this launcher cannot read at all (`fugaro run --repo X` from a
//     directory that holds no checkout of X, and the desktop console's own
//     launches). The question here is whether the WORKFLOW declares a secret,
//     and only the repository's document can answer it — the shape
//     serviceAuthGate accepts for the auth it cannot read either. The launch is
//     left to the run's own bootstrap, which refuses what it cannot read.
//
// A launch that lands on a shared project job is not asked a question of its
// own here: the flip's drop is the repository's own job's (`internal/infra`'s
// workflow() builds that spec and no project job's), and a shared job's mounts
// are its profile's. The address is asked of the repository's own job, as
// proxiedRepo and switchedServiceAuth ask it.
func (e *cloudEnv) switchedVaultSecrets(ctx context.Context, spec *task.Spec) bool {
	if e.lc == nil || e.lc.CredentialsService == nil {
		return false
	}
	if !config.VaultFetchesClass(config.VaultSecretClassWorkflow) {
		return false
	}
	if spec.Mode == recipe.ModeReview {
		return false
	}
	auth, deployed := e.repoServiceAuth(ctx, spec, io.Discard)
	if len(credentialsServiceLaunchEnv(e.lc, "", spec.Repo, auth, e.policyMode(ctx), deployed, false)) == 0 {
		return false
	}
	cfg := launchFileConfig(ctx, e, spec)
	if cfg == nil {
		return false
	}
	_, wf, err := cfg.SelectWorkflow(spec.Workflow)
	return err == nil && len(wf.Secrets) > 0
}

// nodeBrokersBitbucket is the published node's own answer to whether this
// repository's Bitbucket access is brokered: the installation carries the
// credentials service, the repository is a Bitbucket one, and the node admits
// it with proxy: true (G15's one source — the document the credentials service
// itself reads when a job asks it for the token).
//
// It is the half of proxiedRepo that reads documents, named for the second
// caller that needs exactly it and no more: a review launched onto the
// installation's implicit review-only job, which mounts no Bitbucket token and
// cannot mount one, is servable only where the service will forward the token
// for this repository (internal/cli's implicitReviewJobFor). Asking the
// launch's own question there instead would refuse a repository whose
// per-repository job is not handed the service's address while the implicit
// review job always is, which is a launch that would have worked.
func (e *cloudEnv) nodeBrokersBitbucket(ctx context.Context, slug string, spec *task.Spec) bool {
	if e.lc == nil || e.lc.CredentialsService == nil || e.lc.CredentialsService.URL == "" {
		return false
	}
	kind, ok := runner.ProviderForSlug(slug, spec.Repo)
	if !ok || kind != gitprov.KindBitbucket {
		return false
	}
	p, found, err := e.policy.read(ctx, e.lc)
	if err != nil || !found || p == nil || p.BitbucketToken == nil || !p.BitbucketToken.Proxy {
		return false
	}
	canonical, err := projectpolicy.Canonical(kind, spec.Repo)
	if err != nil {
		return false
	}
	return projectpolicy.AdmitRepo(p.BitbucketToken.AllowRepos, canonical)
}

// proxiedRepo is whether the run this launch starts is a proxied one: the
// published node brokers this repository (nodeBrokersBitbucket), and the launch
// hands the service's address to the job it starts. The kind is derived from
// the pair the run itself trusts (the slug it is stored under and the
// repository the task names), never from the local config's own provider, as
// launchPolicyGates does for the allow-list.
//
// Every unreadable or absent answer is a "no": a read that failed, or a node
// nothing published, leaves the launch to the gates that own those refusals
// (launchPolicyGates words them, and cascadeLaunchRefusal the read's own).
func (e *cloudEnv) proxiedRepo(ctx context.Context, slug string, spec *task.Spec) bool {
	if !e.nodeBrokersBitbucket(ctx, slug, spec) {
		return false
	}
	// The last question is asked of the function that answers it for the
	// launch, and not of a copy of its conditions: whether this execution is
	// handed the service's address is what makes the run it starts a proxied
	// one, and credentialsServiceLaunchEnv is the only place that is decided.
	// io.Discard because the launch reads the same two documents and words the
	// one warning this read can report (repoServiceAuth).
	auth, deployed := e.repoServiceAuth(ctx, spec, io.Discard)
	return len(credentialsServiceLaunchEnv(e.lc, "", spec.Repo, auth, e.policyMode(ctx), deployed, false)) > 0
}

// proxiedOnBatchMessage is the Batch arm of G14's launch check, built with the
// repository in it: the operator has to hear which of its repositories the
// refusal is about, and the refusal is one repository's, since only that
// repository's runs are brokered (G9). What the message says is why the
// backend and the proxy cannot meet: a Batch task's account is not one the
// credentials service admits, and its secrets are the installation's rather
// than the job's.
func proxiedOnBatchMessage(repo string) string {
	return fmt.Sprintf("a proxied Bitbucket run of %s is never launched on cloud-batch: a Batch task runs under the installation's VM account, which is not an account the credentials service admits as a repository's job, and Batch takes its secrets from the installation's own config rather than from the job's environment, so such a run could neither reach the service nor hold the token its job no longer mounts (docs/secrets-brokering/implementation-plan.md, Global Constraint 14). Until the service admits that account class, a repository whose Bitbucket access is brokered stays on backend: cloud-run, and nothing in this build removes the refusal", repo)
}

// newSealKey draws the seal key of one launch (token.NewKey). Tests replace
// it to make the draw fail.
var newSealKey = token.NewKey

// budgetBranch is the branch the run's identity may push (fb, G6): the branch
// a first run creates, or the one a follow-up continues. A review run writes
// only comments on its pull requests, an artifact run produces files and an
// evaluate run works on objects the runs bucket already holds, so none of the
// three pushes and none carries a branch — the proxy admits all three with no
// fb and refuses every push of theirs (B1.4).
//
// The branch a follow-up continues is checked here rather than minted
// verbatim. A branch is named by the run id that created it, and the service
// admits that name only when it is not later than the identity's own fr (G6):
// a follow-up launched with a --run-id that sorts before the run it continues
// — the flag is the operator's own text, and prSpec holds it to its shape
// alone — would mint an identity the service refuses at its first call, and a
// refusal there halts the run. So the pairing is refused at the launch, where
// nothing has been signed yet; see the arm below.
func budgetBranch(spec *task.Spec) (string, error) {
	if spec.ReviewPRs != nil || spec.Mode == recipe.ModeArtifact || spec.Mode == recipe.ModeEvaluate {
		return "", nil
	}
	if spec.Branch == "" {
		return "fugaro/" + spec.RunID, nil
	}
	root, ok := task.BranchRunID(spec.Branch)
	switch {
	case !ok:
		return "", userErr("the run follows up on %q, which is not a branch of this installation (fugaro/<run id>), so its identity could name no branch it may push", spec.Branch)
	case root > spec.RunID:
		return "", userErr("the run id %s sorts before %s, the run of the branch %s it continues, so the run would carry an identity naming a branch created after the run itself and the credentials service would refuse every call of it; launch the follow-up under a run id after %s, or without --run-id", spec.RunID, root, spec.Branch, root)
	}
	return spec.Branch, nil
}

// budgetPRs are the pull requests the run's identity may write (fpr, G6): the
// one a follow-up continues, or the one to four a review run reviews. A first
// run names none — it opens its own pull request, which is creation rather
// than a write to a pull request that exists — and an artifact run names none
// at all.
func budgetPRs(spec *task.Spec) []int {
	if len(spec.ReviewPRs) > 0 {
		return spec.ReviewPRs
	}
	if spec.PR > 0 {
		return []int{spec.PR}
	}
	return nil
}

// mintBudgetToken mints the run's custom token, seals it under a key drawn
// for this launch and leaves it in the runs bucket. key is nil when the
// project has no budget backend, and is otherwise the key the execution
// must be handed (the marker's value, D15) — the only copy: it is in no
// object, no log and no error.
//
// Every mint draws a new key (D17): a --retry, or a takeover of a stale
// claim, re-mints and re-seals, and the earlier attempt's object is deleted
// before the new one is written, so no key is ever in front of two
// executions of the run and the earlier key opens nothing that is left. On
// error nothing of this attempt remains in the bucket, and there is no path
// that writes the token unsealed (§7.4: a plaintext object is the exposure
// the seal exists to close).
//
// job is the shared project job the launch resolved, when it resolved one
// (project_job.go): its timeout is the identity window's floor. It is nil on
// every other path, which reads the timeout back from the API instead.
//
// A launch that would mint onto a job that carries no database is refused
// before anything of the mint happens (budgetJobBackend): the identity would be
// one the run cannot use, and the runner's own refusal of that state arrives
// only after the claim, the task and the object.
func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {
	on, err := e.budgetMintOn(ctx)
	if err != nil || !on {
		return nil, err
	}
	if err := e.budgetJobBackend(ctx, slug, spec, job); err != nil {
		return nil, err
	}
	run := spec.RunID
	if budget.Key(slug) != slug || budget.Key(run) != run {
		return nil, userErr("the repository slug %q or run id %q cannot be used as keys of the budget database", slug, run)
	}
	me, err := e.lc.Me(ctx)
	if err != nil {
		return nil, userErr("%v", err)
	}
	signer, err := e.tokenSigner(ctx)
	if err != nil {
		return nil, err
	}
	jobTimeout, err := e.launchIdentityWindow(ctx, slug, spec, job)
	if err != nil {
		return nil, err
	}
	// The key is drawn before the mint, so a draw that fails costs no
	// signature and leaves nothing to clean up. Its error names no value
	// (there is none yet).
	k, err := newSealKey()
	if err != nil {
		return nil, remote(fmt.Errorf("the run's budget token cannot be sealed, because its seal key could not be drawn, so nothing is launched: %w", err))
	}
	branch, err := budgetBranch(spec)
	if err != nil {
		return nil, err
	}
	// RB is the launcher's own, self-asserted name (config user or git
	// email): attribution only, nothing in the rules trusts it.
	claims := token.Claims{Slug: budget.Key(slug), Run: budget.Key(run), FX: token.ExpiryFX(time.Now(), jobTimeout), FP: e.lc.Name, RB: me,
		Branch: branch, PRs: budgetPRs(spec), ContextRepos: spec.ContextRepos}
	// The identity's claims are bounded by the identity provider, and the
	// refusal it makes is at the exchange — inside the run, long after this
	// launcher was told the launch succeeded. So the set is measured here, in
	// the person's own words, before a key is drawn and before a signature is
	// asked for: a launch whose claims would be refused there is refused here,
	// with nothing written (token.MintAt measures the same figure again, as the
	// backstop for every other caller).
	if n := token.ClaimsSize(claims); n > token.MaxClaimsBytes {
		return nil, userErr("this run's identity would carry %d bytes of claims, over the %d a custom token may carry: the identity provider refuses such a token at the exchange, inside the run, after this launcher was told the launch had succeeded. Drop the read-only context repositories this launch names (--context, at most %d of them), or shorten the run id and the repository slug the identity carries",
			n, token.MaxClaimsBytes, token.MaxContextRepos)
	}
	tok, err := token.Mint(ctx, signer, claims)
	switch {
	case errors.Is(err, token.ErrNotMinter):
		// %w and not %v, so the refusal the launcher's own caller reads is
		// still the minter's: a bridged launch tells this one case apart from
		// every other mint failure and rewraps it (launchMinterRefusal), and
		// the sentence a person reads is the same either way.
		return nil, userErr("%w", err)
	case errors.Is(err, token.ErrUnavailable):
		return nil, remote(err)
	case err != nil:
		return nil, remote(fmt.Errorf("minting the run's budget token: %w", err))
	}
	// Minted: only now replace what an earlier attempt left (a --retry, or a
	// takeover of a stale claim), so a failed mint leaves it alone. The
	// earlier object was sealed under the earlier attempt's key; deleting it
	// before the put is what keeps the new key the only one with anything
	// to open (D17).
	if err := token.DeleteObject(ctx, e.bucket, slug, run); err != nil {
		return nil, remote(err)
	}
	if err := token.PutObject(ctx, e.bucket, slug, run, tok, k); err != nil {
		// A put that failed after writing leaves an untaken object: remove
		// it, best effort. PutObject's errors carry the object's name and the
		// bucket's status, never the token or the key.
		if !errors.Is(err, token.ErrObjectExists) {
			e.dropBudgetToken(ctx, slug, run)
		}
		return nil, remote(err)
	}
	return k, nil
}

// launchIdentityWindow is how long this run's identity may live, measured from
// the moment it is taken: the target job's own timeout, floor'd by the
// launch's own timeout.
//
// It is the ticket's MaxRuntimeSeconds and the sealed token's FX, in one
// function rather than two spellings, because the two mechanisms are one
// identity (R7): a launch that asked the service for one window and sealed the
// other would hand a bridged run an identity of a different length than the
// release beside it gives the same launch.
//
// The window is the target job's own timeout (a long job elsewhere must not
// lengthen it). On a shared project job the launch resolved it already
// (project_job.go, from the profile's timeout_class — the same number the job
// was deployed with, so there is nothing to read back from the API). Elsewhere
// it is the job a named run actually starts,
// gcp.WorkflowVariant(spec.Workflow, spec.OAuthToken), not the bare workflow —
// the two can have different timeouts (review finding: this used to read the
// default account's job's timeout for every named run).
func (e *cloudEnv) launchIdentityWindow(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (time.Duration, error) {
	var jobTimeout time.Duration
	var err error
	switch {
	case job != nil:
		jobTimeout = job.Timeout
	default:
		if variant, ok := jobWorkflow(spec.Workflow, spec.OAuthToken); ok {
			jobTimeout, err = e.be.TaskTimeout(ctx, slug, variant)
		} else {
			err = backend.ErrNotFound // not a workflow name: no job can match it either
		}
		if errors.Is(err, backend.ErrNotFound) {
			// No such job: the launch itself reports that, after the mint.
			jobTimeout, err = backend.DefaultTaskTimeout, nil
		}
		if err != nil {
			return 0, remote(err)
		}
	}
	if o := launchTimeoutOf(spec); o > 0 {
		jobTimeout = max(jobTimeout, o+backend.TaskTimeoutSlack)
	}
	return jobTimeout, nil
}

// jobBackendEnvReader is the backend's read of a deployed job's whole
// container environment, when the backend has one at all. It is asked as an
// optional interface, the way internal/cli's upgrade note and lswarn read a
// deployed job: the seam (internal/backend) holds no such read because only one
// of its two backends has a job to read — a Cloud Batch task is started per run
// and keeps none — and a backend that cannot answer is not what refuses a
// launch (Cloud Batch refuses a launch that mints outright, F9).
type jobBackendEnvReader interface {
	JobFullEnv(ctx context.Context, job string) (map[string]string, bool, error)
}

// budgetJobBackend refuses a launch that would mint onto a job that carries no
// budget database (R6, F20), before anything of the mint happens.
//
// A repository's job is re-applied only by `fugaro update` run for that
// repository, so a job applied before the installation had a database carries
// no address in its environment, and it never will until that root is applied
// again. The identity this launch is about to mint would be one the run cannot
// use — it reaches the database through its job's environment, not through the
// token — so the launch would end at the runner's own refusal of exactly this
// state, after the claim and the sealed object were written and the execution
// created (internal/runner's strayTokenNoBackend, which words the same fix).
// Refusing here costs none of that and names the job: the claim goes back and
// no object, no execution and no signed identity are made.
//
// The job read is the one the execution will land on: the shared project job the
// launch resolved by name, else the repository's own job derived from the
// workflow variant exactly as the backend derives it (gcp's jobFor, from the
// same pair). A job that is not deployed at all, and a workflow that is not a
// job name, are left to the launch's own steps, which refuse both with their
// own words — and a read that fails is remote, because whether this launch can
// be accounted for is exactly what cannot be answered then.
func (e *cloudEnv) budgetJobBackend(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) error {
	be, ok := e.be.(jobBackendEnvReader)
	if !ok {
		return nil
	}
	name := launchJobName(slug, spec, job)
	if name == "" {
		return nil
	}
	env, deployed, err := be.JobFullEnv(ctx, name)
	if err != nil {
		return remote(fmt.Errorf("whether the job %s this launch would start carries a budget database could not be read, so the launch is refused: %w", name, err))
	}
	if !deployed || env[runner.RTDBURLEnv] != "" {
		return nil
	}
	return userErr("the job %s this launch would start carries no budget database (%s is not set in its environment), and it was deployed by a fugaro update run for %s from before this installation had a database: a repository's job is re-applied only by an update run for that repository, so the budget identity this launch would mint would be one the run cannot use. Nothing is launched: run fugaro update --repo in %s's checkout, which re-deploys the job with the database, then launch the run again",
		name, runner.RTDBURLEnv, spec.Repo, spec.Repo)
}

// launchJobName is the deployed job this launch's execution lands on: the
// shared project job the launch resolved by name, else the repository's own job
// derived from the workflow variant exactly as the backend derives it (gcp's
// jobFor, from the same pair). "" where this launch names a workflow that is no
// job name, which the launch's own steps refuse.
//
// Two readers hold it — the mint's budget-database check and the identity ask's
// own (executionServesTheService) — because both ask a deployed job's
// environment about the run they are about to start, and a second derivation
// would be a second answer to which job that is.
func launchJobName(slug string, spec *task.Spec, job *launchProjectJob) string {
	if job != nil {
		return job.Name
	}
	if variant, ok := jobWorkflow(spec.Workflow, spec.OAuthToken); ok {
		return gcp.JobName(slug, variant)
	}
	return ""
}

// sealMarker is the marker entry of a launch that minted: the variable's name
// and the launch's seal key in its one spelling (token.EncodeKey), which is
// the spelling the runner parses and registers with its redactor.
func sealMarker(key []byte) string {
	return runner.BudgetTokenEnv + "=" + token.EncodeKey(key)
}

// sealRedact returns err with every spelling of key removed from its text:
// the encoded one, which is what a backend error quoting the launch request
// would carry, and the raw bytes. Unwrap still reaches err, so errors.Is
// finds backend.ErrRejected and the launcher drops its claim and object on
// a refusal exactly as before. A nil key, or a nil err, is returned as is.
func sealRedact(err error, key []byte) error {
	if err == nil || len(key) == 0 {
		return err
	}
	return redactedError{err: err, text: agent.Redact(err.Error(), []string{token.EncodeKey(key), string(key)})}
}

// redactedError is an error whose text has had a secret removed; Unwrap is
// the original, so its kinds are still found.
type redactedError struct {
	err  error
	text string
}

func (e redactedError) Error() string { return e.text }
func (e redactedError) Unwrap() error { return e.err }

// dropBudgetToken removes the run's token object after a launch that
// definitely did not start. Best effort: an untaken token is dead within an
// hour (and the bucket's lifecycle rule removes the object).
func (e *cloudEnv) dropBudgetToken(ctx context.Context, slug, run string) {
	_ = token.DeleteObject(ctx, e.bucket, slug, run)
}

// tokenSigner is the signer account called with the launcher's own
// credentials (ADC). The signer holds no roles; the launcher needs only
// fugaroTokenMinter on it.
func (e *cloudEnv) tokenSigner(ctx context.Context) (token.Signer, error) {
	var ts oauth2.TokenSource
	if e.lc.Endpoints.NoAuth { // fakes only
		ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "no-auth"})
	} else {
		var err error
		if ts, err = google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform"); err != nil {
			return nil, userErr("no Google credentials to sign the run's budget token: run gcloud auth application-default login (%v)", err)
		}
	}
	var opts []token.IAMOption
	if u := e.lc.Endpoints.IAMCredentials; u != "" {
		opts = append(opts, token.WithIAMEndpoint(u))
	}
	s, err := token.NewIAMSigner(e.lc.Budget.TokenSigner, ts, opts...)
	if err != nil {
		return nil, userErr("budget.token_signer: %v", err)
	}
	return s, nil
}

// pairHeadroomCaps is D573's margin: a pair is sampled only when the day has
// this many per-run caps left, in observe and enforce mode alike. Five
// because the day's own cap is the project's guardrail and a sampled pair
// spends two of it beside the runs the owner asked for.
const pairHeadroomCaps = 5

// pairBudgetPrecheck judges whether a sampled pair fits the budget, and
// answers "" when it does or the reason it does not. It is D566's pair
// pre-check — batchBudgetPrecheck with n = 2, one read-only lease of both
// arms' headroom before either starts — followed by D573's margin: the day
// must have pairHeadroomCaps per-run caps left, read through
// budget.ReadDailyHeadroom, which leases nothing.
//
// Every answer but "" is a pair not sampled, never a launch refused: the
// champion goes on alone and on the single pre-check every launch makes
// (D554). An unreadable database is a reason too, for the same reason — and
// --no-budget-check is one as well, because D573 never samples beside a
// skipped check.
//
// Its own notes print to warn, once for the pair, exactly as a batch's do:
// the champion's single pre-check is skipped below (skipBudget), so this is
// the launch's one budget reading.
func (e *cloudEnv) pairBudgetPrecheck(ctx context.Context, slug string, spec *task.Spec, warn io.Writer) string {
	// An installation that cannot mint never reaches a pair (AB4, F7): the
	// launch is refused by the mint, before anything is started. This runs
	// ahead of that refusal (sampledPair judges the pair before the launch
	// block does), so the answer here is the refusal's own reason and not a
	// headroom figure — a figure this read could only take from a database the
	// installation does not have, or from a block that cannot sign what it
	// would mint.
	if _, err := budgetBackendOn(e.lc); err != nil {
		return oneLine(err.Error())
	}
	if e.noBudgetCheck {
		return "the launch skips the budget pre-check (--no-budget-check), and a pair is never sampled beside a skipped pre-check"
	}
	if err := e.batchBudgetPrecheck(ctx, slug, spec.Repo, 2, warn); err != nil {
		return oneLine(err.Error())
	}
	db, err := newBudgetClient(ctx, e.lc.Budget.RTDBURL, e.lc.Endpoints.NoAuth)
	if err != nil {
		return oneLine(err.Error())
	}
	capped := true
	if c := checkoutConfig(ctx, spec.Repo); c != nil && c.Agent.Auth == "oauth" {
		capped = false
	}
	h, err := budget.ReadDailyHeadroom(ctx, db, budget.PrecheckInput{Project: e.lc.Name, Slug: slug, Capped: capped, Now: time.Now()})
	if err != nil {
		return "the day's headroom could not be read: " + oneLine(err.Error())
	}
	if !h.Capped {
		return "the project has no per-run cap and daily cap to measure a margin of"
	}
	if margin := budget.Micros(pairHeadroomCaps) * h.PerRun; h.Remaining < margin {
		return fmt.Sprintf("the day has %s left and a pair is sampled only with %s (five per-run caps of %s) left",
			money(h.Remaining), money(margin), money(h.PerRun))
	}
	return ""
}
