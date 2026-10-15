package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The run's read of the project's own policy (docs/design/project-centric.md
// D11, plan task 1.3). Three things are true at once and the code below is
// shaped by them:
//
//   - The policy node can only be read with the run's Firebase identity: the
//     same custom token, exchanged for the same ID token, that the budget
//     session uses. token.TakeObject *deletes* the object as it reads it
//     (internal/budget/token/object.go), so the take is shared: exactly once
//     per run, handed to the policy read and to budget.Open both (finding 2).
//   - The mode-off path is entered by the job's own budget mode being off,
//     never by an absence of a credential and never by a failure the run sees
//     (B1). A budget-on run with no FUGARO_FIREBASE_API_KEY, or with no token
//     object in the runs bucket, stops on the shipped refusals below; an
//     exchange that fails halts the run the way the session's own Open does.
//     A failure is never read as "no policy here", which would drop the run
//     to the environment ceiling with the project's policy unenforced (R2-3).
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
// db nil and err nil mean this run has no database to read: the job carries no
// backend, or its budget is off and the bucket holds no token (see
// budgetClient).
type budgetIdentity struct {
	done   bool
	cfg    budget.Config
	custom string
	db     *rtdb.Client
	err    error
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
	if !r.d.Backend.On() && !r.d.Backend.MalformedEnv && !r.d.Backend.TokenMarked {
		// No database in the job's environment: no client, no marker, no node.
		// This is the absence the readiness gate refuses (init --repo) once
		// the project has published a policy; a run cannot invent the
		// credential it would need to notice one. An environment that named a
		// backend it could not parse is not that absence (L-b), and neither is
		// one whose launcher minted a token (M2): both name something that only
		// exists when a policy or a shared budget does, so both go through
		// budgetClient, which refuses them whatever the run's mode. Answering
		// "no policy" here for a marked job is the fail-open M2 closed one step
		// too late: admissionCheck would never see the allow-list, and the
		// clone would be made before startBudgetBackend refused.
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
// one cause that is not tampering (an older CLI's init --firebase redeploys the
// pre-policy rules and leaves the marker in place), and the fix.
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
		// is not tampering is an older CLI's init --firebase, which redeploys
		// the rules from before the policy nodes while leaving the marker in
		// place, so a policy that was published becomes unreadable and every
		// budget-on run of the project stops here (R2-8(b)).
		return fmt.Errorf("the project's policy at %s could not be read: the deployed rules are older than the marker %s and refuse this run's token for a node that exists, which is what an older fugaro's 'fugaro init --firebase' leaves behind when it redeploys rules that predate that marker and leaves it in place. Run 'fugaro init --firebase' again with an up-to-date fugaro, which deploys the rules generation this runner reads (%d). The run does not start on a ceiling the policy did not set: %s",
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
		// The node is newer than this image, which cannot be upgraded
		// mid-run: the shared sentence plus the runner's own remedy (P8).
		return fmt.Errorf("%s: %s", fmt.Sprintf(projectpolicy.RunnerSkew, ve.Found, ve.Understood), projectpolicy.PathShared())
	}
	return fmt.Errorf("the project's policy at %s could not be read (%s, which marks the deployed rules at generation %d): the run does not start on a ceiling the policy did not set: %s",
		projectpolicy.PathShared(), marker, projectpolicy.RulesVersion, r.redact(err.Error()))
}

// budgetClient builds the client this run reads the project's database with,
// from its own Firebase identity. TakeObject is called exactly once per run and
// its result shared with budget.Open (finding 2).
//
// A nil client with a nil error means this run has no database to read: the job
// carries no backend, or its budget is off and the runs bucket holds no token
// for it, or its budget is off and the job carries no web API key. The second is
// safe to read as "no policy here" because the launcher
// mints a token whenever it can see a policy, whatever the mode
// (internal/cli's mint decision): off, with no token, is a job of a project
// with nothing published. Every other failure is returned as the error that
// ends bootstrap, already classified.
func (r *run) budgetClient(ctx context.Context) (*rtdb.Client, error) {
	c := &r.ident
	if c.done {
		return c.db, c.err
	}
	c.done = true
	if r.d.BackendErr != nil {
		// The job's environment does not describe a usable backend. A
		// budget-off run ignores it when it describes no backend at all
		// (MalformedEnv false: a stray grace value, an API key with no URL), as
		// it ignores the rest of the backend's environment; a budget-on run
		// stops on the shipped error, before anything else (its text is the one
		// startBudgetBackend has always returned for it).
		//
		// An environment that *carries* the URL or the launcher's marker and
		// cannot be parsed stops the run whatever its mode (L-b of the security
		// review of PR #379). This is the one place the marker did not fail
		// closed: BackendFromEnv returns a zero Backend with the error, so the
		// marker was lost, and a job whose launcher had minted a token — proof
		// that a policy exists — read the missing backend as "this project has
		// published nothing" and ran on the environment ceiling with no
		// session, no cap and no kill switch.
		if !r.d.Spend.On() && !r.d.Backend.MalformedEnv {
			return nil, nil
		}
		c.err = fmt.Errorf("the budget backend in the job's environment: %w", r.d.BackendErr)
		return nil, c.err
	}
	if !r.d.Backend.On() {
		// A job with no database in its environment, and no launcher's marker
		// either, has nothing to read (projectPolicy's early return does not
		// send it here). A marked one is M2's state met at the policy read
		// rather than at startBudgetBackend: the launcher minted a token, which
		// it does only when the project has a shared budget or a published
		// policy, so this run has a ceiling it cannot read and must stop before
		// admission and before any git credential exists — not after the clone.
		if r.d.Backend.TokenMarked {
			c.err = strayTokenNoBackend()
			return nil, c.err
		}
		return nil, nil
	}
	if r.d.Backend.APIKey == "" && !r.d.Spend.On() && !r.d.Backend.TokenMarked {
		// A job that names a database and carries no web API key: the token
		// exchange needs that key, so this run cannot read the marker or the
		// node at all. The key is a credential, and on a budget-off job its
		// absence is the mode-off path, exactly as an absent backend or an
		// absent token object is (B1, P3): the mode-off path is entered by the
		// job's own mode being off, never by an absence of a credential, and
		// the refusal for this job is the launcher's readiness gate — a job
		// that cannot read a published policy is not deployed (task 1.4).
		// The run cannot tell "no policy" from "no key" here, which is why the
		// gate and not the runner is the refusal.
		//
		// A budget-on job stops on the shipped error below, and so does a job
		// whose launcher minted a token: with the mode off the launcher mints
		// only when a policy exists (internal/cli's budgetMintOn), so the
		// marker says this launch had one to read, and the run must not read
		// its own missing credential as "this project has published nothing"
		// (M2). So does a budget-off job whose environment names a backend it
		// cannot parse (BackendErr above, L-b), which never reaches here.
		return nil, nil
	}
	custom, err := r.takeBudgetToken(ctx)
	switch {
	case err == nil:
	case errors.Is(err, token.ErrNoToken) && !r.d.Spend.On() && !r.d.Backend.TokenMarked:
		// No token, and the job's own mode is off, and no launcher said it
		// minted one: nothing to read.
		return nil, nil
	case errors.Is(err, token.ErrNoToken):
		// A budget-on run with no token object in the bucket is the shipped
		// refusal (B1, budget_session.go's startBudgetBackend said the same
		// before this read existed). So is a job whose environment carries
		// the launcher's marker (M2): the launcher mints whenever it can see
		// a policy, whatever the mode, so a marker means this run HAD a token
		// and it is gone — another run of the repository can delete a queued
		// run's object. Reading that as "no policy here" would run a mode-off
		// job of a policy-enforcing project with no session, no cap and no
		// kill switch. The marker is a fact about this launch, so it is the
		// one the run trusts.
		if r.d.Backend.TokenMarked {
			c.err = errors.New("the run's budget token is not in the runs bucket, though the launcher minted one for this run (FUGARO_BUDGET_TOKEN is set in the job's environment): it was already taken, or deleted; launch the run again")
			return nil, c.err
		}
		c.err = errors.New("the run's budget token is not in the runs bucket (already taken, or the run was not launched with one): launch the run again")
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

// takeBudgetToken reads and deletes the run's token object, once per run. Its
// refusals are the shipped ones (plan R10, B1): a job whose environment says
// the budget is on but carries no Firebase API key, or no runs bucket, or a
// slug or run id that cannot be a database key, is damage to the job, and the
// run stops before anything else.
func (r *run) takeBudgetToken(ctx context.Context) (string, error) {
	b := r.d.Backend
	if b.APIKey == "" {
		return "", fmt.Errorf("the budget backend is configured (%s) but %s is not set", RTDBURLEnv, FirebaseAPIKeyEnv)
	}
	if r.d.Bucket == nil {
		return "", errors.New("the budget token is delivered through the runs bucket, and this run has none")
	}
	slug, runID := r.d.Store.Slug(), r.d.Store.RunID()
	if budget.Key(slug) != slug || budget.Key(runID) != runID || slug == "" || runID == "" {
		return "", fmt.Errorf("the repository slug %q or run id %q cannot be used as keys of the budget database", slug, runID)
	}
	custom, err := token.TakeObject(ctx, r.d.Bucket, slug, runID)
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
