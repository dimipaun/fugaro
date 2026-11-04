package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// The launcher's part of the budget backend (M9b R5):
//
//   - It is the only holder of the signer permission. It mints the run's
//     Firebase custom token after winning the launch claim and leaves it in
//     the runs bucket, sealed under a key it draws for this launch alone
//     (docs/design/budget-token-channel.md §7.1); the runner takes it. The
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
//   - A project whose budget is off, or that has no backend configured,
//     skips all of it, so such an installation launches exactly as before.

// budgetBackendOn reports whether runs of the project use the budget
// backend: its mode is not off and a database is configured. A backend with
// no signer cannot launch anything: say so.
//
// The database is half of that on purpose, and this function is where the
// decision belongs: a config with a mode (observe or enforce) and no
// budget.rtdb_url launches, and the launch reads no policy and refuses nothing
// on the mode alone. A mode-only refusal was written here once and removed
// deliberately — the same owner decision internal/infra's readiness note
// records in full, and the reason its drift note exists — because a mode is
// not a policy: this CLI cannot see a policy without a database address, and
// refusing on the mode alone would stop every launch of a project that has
// published nothing, which is the shape a never-configured installation has
// always had. What keeps that from being a hole is the run side, which is
// fail-closed rather than optimistic: a run whose job carries a Firebase
// backend, or a token marker, and cannot read the policy it is entitled to
// refuses instead of falling back to the environment ceiling (internal/runner's
// projectPolicy, R2-3). So a launch is held to a policy this side can see and
// no stricter rule, and the deploy is what refuses the job that can see one and
// cannot read it.
func budgetBackendOn(lc *localcfg.Config) (bool, error) {
	if lc.BudgetMode() == localcfg.BudgetOff || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return false, nil
	}
	return true, budgetSignerOn(lc)
}

// budgetSignerOn refuses a launch that needs a budget identity but has no
// signer to mint it with: the CLI holds the only copy of that permission, so
// nothing else can give a run its identity.
func budgetSignerOn(lc *localcfg.Config) error {
	if lc.Budget == nil || lc.Budget.TokenSigner == "" {
		return userErr("budget.token_signer is not set in project %s's config, so runs cannot be given a budget identity: an operator runs fugaro update --firebase-project %s --name %s", lc.Name, lc.GCPProject, lc.Name)
	}
	return nil
}

// budgetMintOn reports whether this launch must mint the run a budget
// identity: Firebase is configured and either the project's budget mode is not
// off or the project has published a policy. The mode decision follows the
// policy, not the operator's local mode (plan task 1.3 step 2): a job of a
// project-centric installation reads the policy — and the mode in it — through
// its Firebase identity, and a job launched without one would run on the
// ceiling the environment was deployed with, whatever the policy says. What
// the local mode still decides is the job's FUGARO_BUDGET_MODE, which is the
// fallback for the case the policy node is absent (P3).
//
// A config that names no database has no policy this CLI can see, and says so
// without a call, whatever the mode is: a mode that is on is not a policy, and
// nothing is blocked until one exists (internal/infra's readiness gate blocks
// the deploy — and this launcher's own gate refuses a launch — only for a
// policy this side can actually see). A job of such an installation carries no
// backend either, so the run reads no policy and the environment ceiling stands,
// which is what the mode-off and never-configured shapes have always done.
func (e *cloudEnv) budgetMintOn(ctx context.Context) (bool, error) {
	if on, err := budgetBackendOn(e.lc); err != nil || on {
		return on, err
	}
	if e.lc.Budget == nil || e.lc.Budget.RTDBURL == "" {
		return false, nil // no database: no policy this CLI could see either
	}
	p, found, err := e.policy.read(ctx, e.lc)
	if err != nil {
		return false, policyReadErr(err)
	}
	if !found || p == nil {
		return false, nil
	}
	// A policy exists: this run must be able to read it. That needs a signer,
	// whatever the mode is.
	return true, budgetSignerOn(e.lc)
}

// projectPolicyRead is the CLI's read of the project's policy with the
// operator's own credentials: the one read the CLI itself makes of the node
// (Readiness's D11 gate, the launch gate, and in mintBudgetToken the decision
// whether a run needs a budget identity at all). A project whose config
// carries no database URL has no policy this CLI can see, and says so without
// a call. policyReadOnce is what callers use, so one launch reads it once.
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
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, false, nil
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, false, err
	}
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if found && len(p.UnknownKeys) > 0 {
		// The names are the node's own (a hand-writable object), so they are
		// printed the way every other name of that provenance in this package
		// is printed: through pluginwire.Printable, which is what
		// unknownPolicyKeysText does. The launch gate reads this policy and
		// shows the operator this sentence, so an unescaped name here would
		// rewrite the terminal line of a `fugaro run` refusal.
		return nil, false, userErr("%s lists %s, which this Fugaro does not know: a key nobody reads is a policy that never takes effect; fix the policy, or upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run 'fugaro update'",
			projectpolicy.PathShared(), unknownPolicyKeysText(p.UnknownKeys))
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
		// The remedy depends on the direction of the skew — an older node is
		// removed with `fugaro project policy publish --delete` before this
		// release publishes again (never a re-run of init, which writes no
		// policy node), an older binary wants upgrading — and
		// projectpolicy.SkewSentence is the one place that picks between the
		// two sentences for this reader (the launch gate beside this file
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

// policyReadOnce memoizes one CLI run's read of the policy: update --repo asks
// twice (once before the first plan, once after the builds) and must not pay
// for it twice, and a launch asks twice on one path too — the gate before
// anything of it exists, and the mint decision — where two reads could
// disagree about the policy a single launch is admitted against. The answer
// cannot change under either of them (and where it could, the run reads the
// node itself and is the one that counts).
//
// What is memoized is the raw read, projectPolicyRead's result with
// projectpolicy's typed errors intact, because two callers word the same error
// differently: the launch gate names the release a policy's version is from
// (policySkewRefusal), and every other caller wants policyReadErr.
type policyReadOnce struct {
	// mu makes the memo safe for the goroutines a batch of launches shares one
	// cloudEnv with (a best-of-N launch, or the concurrent same-run-ID test):
	// the first caller reads, the others wait for that one answer.
	mu    sync.Mutex
	done  bool
	p     *projectpolicy.Policy
	found bool
	err   error
}

// read is the memoized projectPolicyRead: the policy, whether one was
// published, and the read's own error.
func (p *policyReadOnce) read(ctx context.Context, lc *localcfg.Config) (*projectpolicy.Policy, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		p.done = true
		p.p, p.found, p.err = projectPolicyRead(ctx, lc)
	}
	return p.p, p.found, p.err
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
// writes it (D19, §7.4). A launch that mints nothing is not judged. The judge
// is the one every *Since gate uses (checkImageSince, or checkCloneImageSince
// for a checkout: clone workflow): a release older than sealSince and a
// missing build record refuse. An image built from a base that is not a
// release (a development build) passes when its build record says the base's
// own fugaro reads the sealed token, is refused when the record shows it was
// built before the seal's runner half existed, and is otherwise only warned
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

// newSealKey draws the seal key of one launch (token.NewKey). Tests replace
// it to make the draw fail.
var newSealKey = token.NewKey

// budgetBranch is the branch the run's identity may push (fb, G6): the branch
// a first run creates, or the one a follow-up continues. A review run writes
// only comments on its pull requests and an artifact run produces files, so
// neither pushes and neither carries a branch — the proxy admits both with no
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
	if spec.ReviewPRs != nil || spec.Mode == recipe.ModeArtifact {
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
func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {
	on, err := e.budgetMintOn(ctx)
	if err != nil || !on {
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
	// The identity window is the target job's own timeout (a long job
	// elsewhere must not lengthen it). On a shared project job the launch
	// resolved it already (project_job.go, from the profile's timeout_class —
	// the same number the job was deployed with, so there is nothing to read
	// back from the API). Elsewhere it is the job a named run actually starts,
	// gcp.WorkflowVariant(spec.Workflow, spec.OAuthToken), not the bare
	// workflow — the two can have different timeouts (review finding: this
	// used to read the default account's job's timeout for every named run).
	var jobTimeout time.Duration
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
			return nil, remote(err)
		}
	}
	if o := launchTimeoutOf(spec); o > 0 {
		jobTimeout = max(jobTimeout, o+backend.TaskTimeoutSlack)
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
		Branch: branch, PRs: budgetPRs(spec)}
	tok, err := token.Mint(ctx, signer, claims)
	switch {
	case errors.Is(err, token.ErrNotMinter):
		return nil, userErr("%v", err)
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
