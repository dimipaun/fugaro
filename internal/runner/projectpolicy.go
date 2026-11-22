package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The run's read of the project's own policy (docs/project-centric/spec.md
// D11, plan task 1.3). Three things are true at once and the code below is
// shaped by them:
//
//   - The policy node can only be read with the run's Firebase identity: the
//     same custom token, exchanged for the same ID token, that the budget
//     session uses. token.TakeObject only *reads* the object (a job's grant on
//     tokens/ is read-only, D16: consume-once rests on the per-launch key, not on
//     a delete; internal/budget/token/object.go), and the take is still shared:
//     exactly once per run, handed to the policy read and to budget.Open both
//     (finding 2).
//   - The path with nothing read at all is entered by a launch the launcher
//     minted no identity for, never by a failure the run sees (B1): its job
//     carries no backend, or names one and no token was minted for it. From
//     AB1a on every run is accounted for (D3009, R3), so no mode turns a run
//     down this path either — the launcher's marker does, and the marker is
//     evidence about this launch rather than about the mode. A job that cannot
//     be read with — no FUGARO_FIREBASE_API_KEY, an environment this image
//     cannot parse — stops on the shipped refusals below, and an absent token
//     object does too unless the launch is one the launcher could not have
//     minted for; an exchange that fails halts the run the way the session's
//     own Open does. A failure is never read as "no policy here", which would
//     drop the run to the environment ceiling with the project's policy
//     unenforced (R2-3). The one absence that still reads that way is the
//     markless launch with no token whose job names no live mode (liveMode): a
//     launcher mints for what it can see — a shared budget, or a published
//     policy — and internal/infra writes a mode into the job only when it is
//     observe or enforce, so that is an installation whose mode is off or unset
//     and which has published nothing. Such a run has nothing to read, and
//     until the launcher mints on every launch (AB4, R6) a refusal here would
//     stop it on a fix that cannot change what its launcher mints.
//   - Whether a policy exists at all may not be guessed from a read this run
//     cannot make. The rules marker decides (projectpolicy.Read): before the
//     policy rules are deployed, no policy can have been published here, so an
//     installation that has not deployed them runs exactly as it does today,
//     denied read and all.

// policyRead memoizes one run's read of the project's policy: the answer cannot
// change under the run, and the node is read and decoded once (plan task 1.3
// step 5, which stashes allow_repos for the enrollment check of task 1.5).
type policyRead struct {
	done   bool
	policy *projectpolicy.Policy
	found  bool
	err    error
}

// budgetIdentity is the run's connection to the project's budget backend,
// built once: the custom token taken out of the runs bucket, the configuration
// the session opens with, and the database client built from the exchanged
// identity. The policy read needs the client before the session exists, and
// both must be the same identity, so this is memoized on the run.
//
// db nil and err nil mean this run has nothing to read with: the job carries no
// backend at all, or names one and holds no token object for this run with no
// marker saying one was minted and no live mode of its own (see budgetClient).
type budgetIdentity struct {
	done   bool
	cfg    budget.Config
	custom string
	db     *rtdb.Client
	err    error
	// tok is the session this read exchanged the launcher's custom token for:
	// the run's own Firebase identity, which the credentials service verifies
	// a mint by (C1.10). The read needs it before the budget opens, so it is
	// kept here for the git path, which fetches its first credential before
	// the budget session exists (r.runIDToken); once the budget session
	// exists that one answers instead. It is guarded by r.mu.
	tok *token.Session
}

// projectPolicy returns the project's policy, if this run can read one. Its
// three outcomes are projectpolicy.Read's:
//
//   - (nil, false, nil): this installation has no policy to apply. Either the
//     job carries no Firebase backend at all (a job of an installation that
//     never configured one, which the readiness gate refuses once a policy
//     exists), or the deployed rules predate the policy nodes, or the node is
//     absent: today's behaviour, the policy from the job environment.
//   - (p, true, nil): a policy this Fugaro understands, which replaces the
//     ceiling (resolvePolicy).
//   - (nil, false, err): the node exists and cannot be read or decoded, or the
//     run's identity could not be established. Bootstrap stops: the run never
//     falls back to a ceiling the policy did not set.
//
// It is memoized like defaultBranchFile: one read, one decode, one token take
// per run.
func (r *run) projectPolicy(ctx context.Context) (*projectpolicy.Policy, bool, error) {
	p := &r.projPolicy
	if p.done {
		return p.policy, p.found, p.err
	}
	p.done = true
	if !r.d.Backend.On() && !r.d.Backend.MalformedEnv && !r.d.Backend.Issued() {
		// No database in the job's environment: no client, nothing a launcher
		// left, no node. This is the absence the readiness gate refuses (fugaro
		// update) once the project has published a policy; a run cannot invent
		// the credential it would need to notice one. An environment that named
		// a backend it could not parse is not that absence (L-b), and neither
		// is one whose launcher provisioned this run's identity -- a minted
		// token (M2) or a ticket (LM7): both say something that only exists
		// when a policy or a shared budget does, so both go through
		// budgetClient, which refuses them. Answering "no policy" here for such
		// a job is the fail-open M2 closed one step too late: admissionCheck
		// would never see the allow-list, and the clone would be made before
		// startBudgetBackend refused.
		return nil, false, nil
	}
	db, err := r.budgetClient(ctx)
	if err != nil {
		p.err = err
		return nil, false, err
	}
	if db == nil {
		return nil, false, nil
	}
	pp, found, err := r.readPolicy(ctx, &r.ident.cfg, db)
	if err != nil {
		p.err = r.policyReadError(err)
		return nil, false, p.err
	}
	if found && len(pp.UnknownKeys) > 0 {
		// Tolerated, not ignored: this Fugaro enforces every value it does
		// understand, and a key it does not may be a newer Fugaro's (P8) or a
		// misspelling (the CLI refuses those). Say so, once.
		r.d.Log.Warn("policy: the project's policy lists keys this Fugaro does not know; they have no effect",
			"node", projectpolicy.PathShared(), "keys", strings.Join(pp.UnknownKeys, ","))
	}
	p.policy, p.found = pp, found
	return pp, found, nil
}

// bitbucketOwnIdentities is the set of account UUIDs Fugaro's own Bitbucket
// tokens have on this project, and whether that set is known to be missing an
// account this run may write as (T16, F20, R12, R14).
//
// It is the node's own bitbucket_tokens key, read raw for accountAuto's reason:
// the key is a top-level one the policy reader of this release does not know
// yet, and P8 tolerates an unknown top-level key, so a node carrying it is
// readable here (its reader lists the key in UnknownKeys) while a decoded
// Policy carries no member for it.
//
// The read is the run's own: the database client is the one bootstrap already
// built (budgetClient, memoized, the same identity the policy read used), so a
// run pays no second exchange for it. Nothing else can reach this value -- not
// the task, not the job's environment, not the checkout the agent works in --
// because an identity planted in any of the three would let its author's
// comments pass as the run's own (F21 trusts the set at the node writer's
// level, and nowhere below it).
//
// partial is true when the run cannot name every account it may write as, which
// is either a set the node names incompletely or a set this run could not read
// at all.
//
// What makes a set the node names incompletely partial is
// projectpolicy.BitbucketOwnIdentities's rule, which this read applies rather
// than restates: the identities of the entries, an entry that names none, and
// the legacy record serving beside typed ones (R3, R14) all make it partial, and
// in every one of them GET /user naming an account of Fugaro's no longer says
// the run can tell its own comments apart -- an account outside the set may
// still be one of this run's, and claiming otherwise is what shuts the
// markers-from-anyone fallback the follow-up trust rules rest on.
//
// A set this run could not read at all is not an empty set but an unknown one,
// and it is partial too: a node whose read failed, or a key that will not
// decode. A run that could not say which accounts are Fugaro's must not answer
// that it can tell its own comments apart -- the claim is what the adapter's
// SelfKnown carries -- and calling a set complete because it was unreadable is
// the false claim this read exists to stop making, from the other side. What
// the run falls back to there is GET /user's own account, the narrower answer,
// with the markers still read from anyone.
//
// What is left -- no published node, a node without the key -- is today's
// behaviour: the empty set (or the legacy record's single token, which GET
// /user answers for), and a run that tells its own comments apart by that
// answer alone. That fallback is the narrower one: a comment this run did write
// reads as somebody else's, which the markers-from-anyone rule already answers.
func (r *run) bitbucketOwnIdentities(ctx context.Context) (projectpolicy.BitbucketTokenIdentities, bool) {
	pp, found, err := r.projectPolicy(ctx)
	if err != nil {
		return nil, true
	}
	if !found {
		return nil, false
	}
	db, err := r.budgetClient(ctx)
	if err != nil || db == nil {
		return nil, true
	}
	var node map[string]json.RawMessage
	if _, err := db.Get(ctx, projectpolicy.PathShared(), &node); err != nil {
		r.d.Log.Warn("policy: the Bitbucket token identities could not be read; this run tells its own comments apart by its own token alone",
			"node", projectpolicy.PathShared(), "key", projectpolicy.BitbucketTokensKey)
		return nil, true
	}
	raw, ok := node[projectpolicy.BitbucketTokensKey]
	if !ok || projectpolicy.IsNull(raw) {
		return nil, false
	}
	var tokens projectpolicy.BitbucketTokens
	if err := json.Unmarshal(raw, &tokens); err != nil {
		// The value itself is never echoed: what a node holds is nobody's to
		// read out of a log line, and the key's name is enough to find it.
		r.d.Log.Warn("policy: the Bitbucket tokens key could not be read; this run tells its own comments apart by its own token alone",
			"node", projectpolicy.PathShared(), "key", projectpolicy.BitbucketTokensKey)
		return nil, true
	}
	// The entries are judged by the rule's one home, so this read and the
	// adapter's own tests cannot answer the same node differently. The legacy
	// half is read off the decoded policy and not the raw node because this
	// release knows the block: its reader validates it, and the run's own
	// admission already decides on it, so the two readers cannot disagree about
	// which installation has a live legacy token.
	return projectpolicy.BitbucketOwnIdentities(tokens.Entries, pp.BitbucketToken)
}

// readPolicy reads the project's policy through db, retrying a database that
// cannot be reached until the grace runs out (D14), exactly as budget.Open
// retries the first call of a session: a blip during the read is an outage, and
// it halts the run the way the session's own outage does rather than letting
// the run go on with the policy unenforced. Nothing else is retried: a refusal
// of the credential, a node this Fugaro cannot decode and a marker that is not
// a generation all return at once, through unreadablePolicy.
func (r *run) readPolicy(ctx context.Context, cfg *budget.Config, db *rtdb.Client) (*projectpolicy.Policy, bool, error) {
	g := budget.NewGrace(cfg.Grace)
	defer g.Stop()
	for {
		pp, found, err := projectpolicy.Read(ctx, db)
		if err == nil {
			return pp, found, nil
		}
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		if !errors.Is(err, rtdb.ErrUnavailable) {
			return nil, false, r.unreadablePolicy(err)
		}
		g.Fail("config", err)
		every := cfg.RetryEvery
		if every <= 0 {
			every = defaultRetryEvery
		}
		t := time.NewTimer(every)
		select {
		case <-t.C:
		case <-g.Expired():
			t.Stop()
			src, cause := g.Cause()
			return nil, false, fmt.Errorf("%w (%s: %v)", budget.ErrGraceExpired, src, cause)
		case <-ctx.Done():
			t.Stop()
			return nil, false, ctx.Err()
		}
		t.Stop()
	}
}

// policyReadError classifies what ended the read of the project's policy. A
// failure of the run's identity or of the database's reachability goes through
// the session's own mapping (budgetIdentityError), so one failure cannot
// classify two ways depending on which of the two reads reached it first
// (R2-3). What is left is a policy this Fugaro read and may not use, which
// unreadablePolicy has already worded; it stops the run as it is.
func (r *run) policyReadError(err error) error {
	switch {
	case errors.Is(err, budget.ErrGraceExpired),
		errors.Is(err, token.ErrTokenExpired),
		errors.Is(err, budget.ErrPermissionDenied):
		return r.budgetIdentityError(err)
	default:
		return err
	}
}

// unreadablePolicy is the refusal of a policy that exists and cannot be used: a
// denied read, a node that is not this Fugaro's policy, a read that failed. The
// run does not start — never on the environment ceiling, which would be less
// constrained than what the owner published. The message names the node, the
// one cause that is not tampering (an older CLI's fugaro update
// --firebase-project redeploys the pre-policy rules and leaves the marker in
// place), and the fix.
func (r *run) unreadablePolicy(err error) error {
	marker := projectpolicy.PathRulesMarker()
	if errors.Is(err, rtdb.ErrPermission) && !strings.Contains(err.Error(), projectpolicy.PathShared()) {
		// The marker's own read was denied. Every node under config/limits is
		// readable by a run token under the rules that predate the policy
		// nodes, so this is not a rules skew: the database refuses this run's
		// credential outright — the wrong project, a revoked token, or rules
		// that never granted the read. That is the shipped bootstrap failure
		// of internal/budget's retryBoot (ErrPermissionDenied), and it is
		// classified here with the same words.
		return fmt.Errorf("%w: %s", budget.ErrPermissionDenied, r.redact(err.Error()))
	}
	if errors.Is(err, rtdb.ErrPermission) {
		// The node's read was denied, with the marker read: the one cause that
		// is not tampering is an older CLI's fugaro update --firebase-project,
		// which redeploys the rules from before the policy nodes while leaving
		// the marker in place, so a policy that was published becomes
		// unreadable and every budget-on run of the project stops here
		// (R2-8(b)).
		return fmt.Errorf("the project's policy at %s could not be read: the deployed rules are older than the marker %s and refuse this run's token for a node that exists, which is what an older fugaro's 'fugaro update --firebase-project' leaves behind when it redeploys rules that predate that marker and leaves it in place. Run 'fugaro update' in your own terminal with an up-to-date fugaro: its rules stage deploys the rules generation this runner reads (%d) over that skew. The run does not start on a ceiling the policy did not set: %s",
			projectpolicy.PathShared(), marker, projectpolicy.RulesVersion, r.redact(err.Error()))
	}
	var me *projectpolicy.MarkerError
	if errors.As(err, &me) {
		// A hand-edited marker is a person's mistake, and it is never read as
		// "no marker" — that would silently make a project-centric
		// installation a legacy one (projectpolicy.MarkerError says what the
		// node must hold).
		return fmt.Errorf("the deployed rules cannot be told apart: %s. The run does not start on a ceiling the policy did not set", me.Error())
	}
	var ve *projectpolicy.VersionError
	if errors.As(err, &ve) {
		// A node this image does not read, in either direction: newer than it
		// (a release ahead of this job's image), or older (a hand-written
		// node, or one a release before this shape wrote — a node below
		// Version is not the legacy path, that is the marker's job). Both
		// stop the run the same way, because the ceiling a policy sets is not
		// something a run may fall back from — and each is worded for its own
		// state, by projectpolicy.SkewSentence with ReaderRunner: a refresh of
		// the image for a node ahead of it, and for one behind it whichever
		// move its version allows — `fugaro update`'s in-place rewrite for a
		// node the migration chain carries, a policy published again from a
		// release this image understands for one below the chain's first step.
		// Telling the operator of a node that is merely behind to refresh the
		// image would send them to a move that changes nothing (the review
		// finding on this change), and telling them to delete a node the chain
		// would have carried forward costs what only the node holds; the
		// runner can make none of these moves mid-run, so the operator does,
		// which is why the message is the shared sentence plus the remedy for
		// this state (P8).
		return fmt.Errorf("%s: %s", projectpolicy.SkewSentence(ve, projectpolicy.ReaderRunner), projectpolicy.PathShared())
	}
	return fmt.Errorf("the project's policy at %s could not be read (%s, which marks the deployed rules at generation %d): the run does not start on a ceiling the policy did not set: %s",
		projectpolicy.PathShared(), marker, projectpolicy.RulesVersion, r.redact(err.Error()))
}

// liveMode reports whether a mode string names one of the two live modes. It
// has one reader — budgetClient's markless arm — and its subject is the job's
// own environment rather than what the run is accounted for: internal/infra
// writes FUGARO_BUDGET_MODE only when the mode is observe or enforce
// (budgetEnv), so a job carrying one is a job whose launcher mints, while a job
// carrying "" or the word that was a mode is a launch of an installation whose
// launcher mints nothing (internal/cli's budgetMintOn: the mode is off, and no
// policy node was found to mint for). What a run is *accounted for* is C1's
// answer and never this string (Spend.On, and the record's own mode); the raw
// string is otherwise kept for the gateway's question, which AB1b makes
// unconditional.
func liveMode(mode string) bool {
	return mode == policy.ModeObserve || mode == policy.ModeEnforce
}

// budgetClient builds the client this run reads the project's database with,
// from its own Firebase identity. It is the run's one call site of
// takeBudgetToken — the memo guard below is what makes it one read of the
// object per run, and TestTheBudgetTokenIsTakenAtOneCallSite is what holds the
// call site at one — and the unsealed token is shared with budget.Open
// (finding 2).
//
// A nil client with a nil error means this launch has nothing to read the
// project's policy with: the job carries no backend at all, or names one and
// holds no token object for this run with no marker saying one was minted and
// no live mode in its own environment (liveMode). From AB1a on every run is
// accounted for (D3009, R3), so what a run is accounted for is never what
// decides that — the launcher's evidence about this launch is: its marker, and
// the mode the job carries, which internal/infra writes only for an
// installation whose launcher mints.
//
// The one absence that is not a refusal is a markless launch with no token
// whose job names no live mode. The launcher mints whenever it can see a shared
// budget or a published policy (internal/cli's budgetMintOn), so this is the
// shape an installation whose mode is off or unset and which has published
// nothing gives its runs: nothing to read, no session to open, and the
// environment ceiling exactly as it stood before AB1a. Refusing it would stop
// such a run on a remedy — fugaro update — that cannot change what its launcher
// mints until the launcher mints on every launch (AB4, R6).
//
// Everything else that leaves this function clientless is a refusal, returned
// as the error that ends bootstrap, already classified: an absent token when a
// marker says this run had one, or when the job's own mode is one its launcher
// mints for; an environment this image cannot parse; a job that names a
// database with no web API key to exchange with.
func (r *run) budgetClient(ctx context.Context) (*rtdb.Client, error) {
	c := &r.ident
	if c.done {
		return c.db, c.err
	}
	c.done = true
	if r.d.BackendErr != nil {
		// The job's environment does not describe a usable backend, and from
		// AB1a on that stops every run: the run is accounted for whatever its
		// mode is (D3009, R3), so a value it cannot parse is damage to the
		// ceiling's own environment rather than something a run ignores — its
		// text is the one startBudgetBackend has always returned for it.
		//
		// An environment that *carries* the URL or the launcher's marker and
		// cannot be parsed is the state this arm exists for (L-b of the
		// security review of PR #379). This is the one place the marker did not
		// fail closed: BackendFromEnv returns a zero Backend with the error, so
		// the marker was lost, and a job whose launcher had minted a token —
		// proof that a policy exists — read the missing backend as "this
		// project has published nothing" and ran on the environment ceiling
		// with no session, no cap and no kill switch.
		c.err = fmt.Errorf("the budget backend in the job's environment: %w", r.d.BackendErr)
		return nil, c.err
	}
	if !r.d.Backend.On() {
		// A job with no database in its environment, and nothing a launcher
		// left on it, has nothing to read (projectPolicy's early return does
		// not send it here). A job the launcher provisioned an identity for --
		// a minted token or a ticket (LM7) -- is M2's state met at the policy
		// read rather than at startBudgetBackend: the launcher does either only
		// when the project has a shared budget or a published policy, so this
		// run has a ceiling it cannot read and must stop before admission and
		// before any git credential exists — not after the clone.
		if r.d.Backend.Issued() {
			c.err = noBackendForWhatTheLauncherLeft(r.d.Backend)
			return nil, c.err
		}
		return nil, nil
	}
	custom, err := r.takeBudgetToken(ctx)
	switch {
	case err == nil:
	case errors.Is(err, token.ErrNoToken) && !r.d.Backend.TokenMarked && !liveMode(r.d.Spend.Mode):
		// No token object, no launcher said it minted one, and the job's own
		// environment names no live mode: nothing to read, and no launch that
		// was entitled to a token. The launcher mints only for what it can see
		// — a shared budget, or a published policy — and internal/infra writes
		// the mode into the job only when it is observe or enforce, so this is
		// the deployment of an installation whose mode is off or unset and
		// which has published nothing, with no session and no cap, exactly as
		// before AB1a. Until the launcher mints on every launch (AB4, R6) the
		// refusal that used to stand here would stop such a run on a remedy —
		// fugaro update — that cannot change what its launcher mints.
		return nil, nil
	case errors.Is(err, token.ErrNoToken):
		// The shipped refusal (B1, budget_session.go's startBudgetBackend said
		// the same before this read existed), in the two sentences it has
		// always had. The marker picks the marked one (M2) and is a fact about
		// this launch, so it is the one the run trusts; a markless job whose
		// own environment names a live mode is a launch its launcher was
		// entitled to mint for — infra writes no mode on an installation that
		// mints nothing, and a markless job of one never reaches here (the arm
		// above) — so an absent token is a token this run's launch should have
		// carried: a direct execution of the job, or one whose override was
		// dropped. Reading either absence as "no policy here" would run a job
		// of a policy-enforcing project with no session, no cap and no kill
		// switch.
		//
		// What the absence means changed with the seal (D16): the take is
		// read-only and no run can delete a queued run's object, so "already
		// taken" is never the cause. The job account's grant on tokens/ is
		// conditioned and has no list, so GCS answers a refused read and a
		// missing object alike with 403, and gocloud reports both as
		// NotFound (token.ErrNoToken): the run cannot tell them apart. The
		// likely cause after an upgrade is the grant — a job not re-applied
		// since the token moved to tokens/ has none — so the refusal names it
		// and its cure first. It names no object, and no key.
		const grant = "this job's service account lacks its read grant on the repository's token objects under tokens/ (a job not re-applied since the upgrade that moved the budget token there: an operator runs fugaro update for this repository in its checkout)"
		if r.d.Backend.TokenMarked {
			c.err = fmt.Errorf("the run's budget token could not be read from the runs bucket, though the launcher minted one for this run (FUGARO_BUDGET_TOKEN is set in the job's environment); the run cannot tell a refused read from a missing object. Either %s — then start a new run (fugaro run with the same task): this run has launched, and retrying its run id only reports it — or the object is gone (a launch that failed and was cleaned up, or the bucket's day-long lifecycle rule): launch the run again", grant)
			return nil, c.err
		}
		c.err = fmt.Errorf("the run's budget token could not be read from the runs bucket: the run was not launched with one, or %s — either way, start a new run (fugaro run with the same task): this run has launched, and retrying its run id only reports it — and when the grant is the cause, an operator runs that update first", grant)
		return nil, c.err
	default:
		c.err = err
		return nil, err
	}
	c.custom = custom
	// The id token this client carries is the one the session will have:
	// exchangeIdentity checks the claims against these two names, so the
	// policy read and the session read the database as one identity.
	c.cfg = r.budgetConfig()
	db, err := r.exchangeIdentity(ctx, &c.cfg, custom)
	if err != nil {
		c.err = r.budgetIdentityError(err)
		return nil, c.err
	}
	c.db = db
	return db, nil
}

// errLauncherPredatesSeal is the mixed-fleet refusal (D19): the job's marker
// is "1", which only a launcher from before the seal writes, so the token it
// minted is unsealed and under runs/, and this runner reads neither.
func errLauncherPredatesSeal(runID string) error {
	return fmt.Errorf("%s is 1, so the fugaro that launched this run predates the seal of the budget token: it wrote the token unsealed, where this runner never reads one, and handed this execution no key; nothing was read. Upgrade the launching fugaro (brew upgrade dimipaun/tap/fugaro), run 'fugaro update' with the newer binary, then start a new run (fugaro run with the same task): run %s has launched, and retrying its run id would only report it", BudgetTokenEnv, runID)
}

// takeBudgetToken yields the run's identity: it claims the ticket the launcher
// asked the installation's credentials service for, or reads the sealed token
// object a launcher of the release behind left in the runs bucket (LM7, R7). It
// has one call site — budgetClient, which memoizes what this returns, so the
// identity is taken once per run — and that is a shape the source holds rather
// than one a run's state can show: see TestTheBudgetTokenIsTakenAtOneCallSite.
//
// Which of the two it does is what the launch left on the execution and nothing
// else: a ticket where the launcher asked an issuer for this run's identity,
// the marker's seal key where it minted the token itself. Both arms hand back
// one custom token for the same exchange below, so the policy read and the
// session are one identity whichever the launch was (the bridge, R7).
//
// Its refusals are the shipped ones (plan R10, B1) for the sealed object — a
// job whose environment says the budget is on but carries no Firebase API key,
// or no runs bucket, or a slug or run id that cannot be a database key, is
// damage to the job, and the run stops before anything else — and the claim's
// own for a ticket (identity_claim.go), which halts the run rather than
// returning a reason a caller could carry on from.
func (r *run) takeBudgetToken(ctx context.Context) (string, error) {
	b := r.d.Backend
	if b.APIKey == "" {
		return "", fmt.Errorf("the budget backend is configured (%s) but %s is not set", RTDBURLEnv, FirebaseAPIKeyEnv)
	}
	if b.Ticket != "" {
		// The claim needs the API key and nothing else of the backend: the
		// custom token it answers is exchanged below with that key, and a
		// claimed launch minted no object, so the runs bucket is not read and
		// its absence is not an error here.
		custom, err := r.claimBudgetIdentity(ctx, b.Ticket)
		if err != nil {
			return "", err
		}
		// Registered before anything can print it: the answer that carried it
		// is already out of claimBudgetIdentity, and every reason this run
		// publishes from here on is redacted of it (the sealed object's rule
		// below, one mechanism over).
		r.addSecret(custom)
		return custom, nil
	}
	if r.d.Bucket == nil {
		return "", errors.New("the budget token is delivered through the runs bucket, and this run has none")
	}
	slug, runID := r.d.Store.Slug(), r.d.Store.RunID()
	if budget.Key(slug) != slug || budget.Key(runID) != runID || slug == "" || runID == "" {
		return "", fmt.Errorf("the repository slug %q or run id %q cannot be used as keys of the budget database", slug, runID)
	}
	// A marker of "1" is a launcher from before the seal (D19): it wrote the
	// token unsealed under runs/, which this runner never reads, and handed
	// no key. The run refuses here, before the bucket, with the side that is
	// old named — never by reading a plaintext object instead.
	if b.TokenMarked && len(b.TokenKey) == 0 {
		return "", errLauncherPredatesSeal(runID)
	}
	// The take carries the launch's seal key (Backend.TokenKey, from the
	// marker's value) and unseals the object under tokens/ with it; a
	// plaintext object, and any object with no key, is refused rather than
	// read (token.TakeObject). The take is read-only — the job's grant on
	// tokens/ has no delete (D16) — so it removes nothing. The key itself is
	// registered with the redactor at the top of Run, before this call, so
	// nothing here may print it.
	custom, err := token.TakeObject(ctx, r.d.Bucket, slug, runID, r.d.Backend.TokenKey)
	switch {
	case errors.Is(err, token.ErrNoToken):
		return "", err // the caller says what an absent token means for this run
	case err != nil:
		return "", fmt.Errorf("taking the run's budget token: %s", r.redact(err.Error()))
	}
	// Registered before anything can print it.
	r.addSecret(custom)
	return custom, nil
}

// budgetConfig is the session's configuration from the job's backend. Tune is
// applied here, once, because the client the policy read builds and the session
// Open builds must be the same database with the same test hooks; the fields
// that depend on the merged policy (the cap, the committed day, the local
// enforce) are filled in by startBudgetBackend.
func (r *run) budgetConfig() budget.Config {
	b := r.d.Backend
	cfg := budget.Config{
		RTDBURL: b.RTDBURL, APIKey: b.APIKey, HTTP: b.HTTP, Grace: b.Grace,
		Slug: r.d.Store.Slug(), Run: r.d.Store.RunID(),
		Register: r.addSecret, Now: r.d.Now,
		Log:    slog.New(redactHandler{h: r.d.Log.Handler(), redact: r.redact}),
		OnHalt: r.onBudgetHalt,
	}
	if b.Tune != nil {
		b.Tune(&cfg)
	}
	return cfg
}

// exchangeIdentity exchanges the run's custom token for its Firebase identity
// and builds the database client: what budget.Open does first, done here as
// well because the policy read needs the client before the session exists. The
// behaviour is Open's, deliberately and in full: the same grace for an outage
// (D14), the same claim check, the same rtdb.New options. Anything else would
// let one transient failure classify two ways depending on which of the two
// read the database first.
func (r *run) exchangeIdentity(ctx context.Context, cfg *budget.Config, custom string) (*rtdb.Client, error) {
	g := budget.NewGrace(cfg.Grace)
	defer g.Stop()
	tcfg := token.Config{
		APIKey: cfg.APIKey, IdentityURL: cfg.IdentityURL, SecureTokenURL: cfg.SecureTokenURL, HTTP: cfg.HTTP,
		Now: cfg.Now, Register: cfg.Register,
		OnResult: func(op string, err error) {
			switch {
			case err == nil:
				g.OK(op)
			case errors.Is(err, token.ErrUnavailable):
				g.Fail(op, err)
			}
		},
	}
	var tok *token.Session
	for {
		var err error
		if tok, err = token.Exchange(ctx, tcfg, custom); err == nil {
			break
		}
		if !errors.Is(err, token.ErrUnavailable) {
			return nil, err
		}
		// The session's own wait: the grace, the retry interval (Open's
		// default 5 s), the context.
		every := cfg.RetryEvery
		if every <= 0 {
			every = defaultRetryEvery
		}
		t := time.NewTimer(every)
		select {
		case <-t.C:
		case <-g.Expired():
			t.Stop()
			src, cause := g.Cause()
			return nil, fmt.Errorf("%w (%s: %v)", budget.ErrGraceExpired, src, cause)
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
		t.Stop()
	}
	if c := tok.Claims(); c.Slug != cfg.Slug || c.Run != cfg.Run {
		return nil, token.ErrTokenMismatch
	}
	// The session the read exchanged is kept on the run: it is the identity
	// the credentials service verifies a mint by, and the git path asks for
	// its token before the budget session exists (r.runIDToken). The check
	// above is the same one budget.Open makes of the second exchange, so this
	// is the same identity either way.
	r.mu.Lock()
	r.ident.tok = tok
	r.mu.Unlock()
	var opts []rtdb.Option
	if cfg.HTTP != nil {
		opts = append(opts, rtdb.WithHTTPClient(cfg.HTTP))
	}
	return rtdb.New(cfg.RTDBURL, rtdb.Auth{IDToken: tok.Token}, opts...)
}

// defaultRetryEvery is budget.Open's own default for the pause between
// bootstrap attempts (its Config.RetryEvery; the session sets it when the
// caller leaves it unset).
const defaultRetryEvery = 5 * time.Second

// budgetIdentityError maps a failure of the run's Firebase identity to the
// error that ends bootstrap, exactly as budget.Open's own failures are mapped
// in startBudgetBackend: an expired token halts, an outage that outlasted the
// grace halts, anything else refuses the run's identity. One function for both
// so the same failure cannot classify two ways depending on which read reached
// it first (plan task 1.3 step 1, R2-3).
func (r *run) budgetIdentityError(err error) error {
	switch {
	case errors.Is(err, token.ErrTokenExpired):
		return r.bootstrapHalt(runstore.HaltBudgetTokenExpired, "run",
			"the run's budget token was minted more than an hour ago, usually because the run queued for that long: launch it again")
	case errors.Is(err, budget.ErrGraceExpired):
		return r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", "the budget backend could not be reached before the run began: "+r.redact(err.Error()))
	default:
		return fmt.Errorf("the budget backend refused the run's identity: %s", r.redact(err.Error()))
	}
}
