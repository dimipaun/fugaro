package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// The launcher's part of the budget backend (M9b R5):
//
//   - It is the only holder of the signer permission. It mints the run's
//     Firebase custom token after winning the launch claim and leaves it in
//     the runs bucket; the runner takes it. The token is never in the
//     jobs.run request (the request reaches audit logs), an env var, an
//     error or a message.
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
func budgetBackendOn(lc *localcfg.Config) (bool, error) {
	if lc.BudgetMode() == localcfg.BudgetOff || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return false, nil
	}
	if lc.Budget.TokenSigner == "" {
		return false, userErr("budget.token_signer is not set in project %s's config, so runs cannot be given a budget identity: an operator runs fugaro init --firebase <firebase-project-id> --name %s", lc.Name, lc.Name)
	}
	return true, nil
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

// mintBudgetToken mints the run's custom token and leaves it in the runs
// bucket. minted is false when the project has no budget backend. A token
// object left by an earlier attempt is replaced (PutObject never overwrites,
// and the new mint supersedes the old identity's delivery). On error nothing
// of this attempt remains in the bucket.
func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec) (minted bool, err error) {
	on, err := budgetBackendOn(e.lc)
	if err != nil || !on {
		return false, err
	}
	run := spec.RunID
	if budget.Key(slug) != slug || budget.Key(run) != run {
		return false, userErr("the repository slug %q or run id %q cannot be used as keys of the budget database", slug, run)
	}
	me, err := e.lc.Me(ctx)
	if err != nil {
		return false, userErr("%v", err)
	}
	signer, err := e.tokenSigner(ctx)
	if err != nil {
		return false, err
	}
	// The identity window is the target job's own timeout (a long job
	// elsewhere must not lengthen it).
	jobTimeout, err := e.be.TaskTimeout(ctx, slug, spec.Workflow)
	if errors.Is(err, backend.ErrNotFound) {
		// No such job: the launch itself reports that, after the mint.
		jobTimeout, err = backend.DefaultTaskTimeout, nil
	}
	if err != nil {
		return false, remote(err)
	}
	if o := launchTimeoutOf(spec); o > 0 {
		jobTimeout = max(jobTimeout, o+backend.TaskTimeoutSlack)
	}
	// RB is the launcher's own, self-asserted name (config user or git
	// email): attribution only, nothing in the rules trusts it.
	claims := token.Claims{Slug: budget.Key(slug), Run: budget.Key(run), FX: token.ExpiryFX(time.Now(), jobTimeout), FP: e.lc.Name, RB: me}
	tok, err := token.Mint(ctx, signer, claims)
	switch {
	case errors.Is(err, token.ErrNotMinter):
		return false, userErr("%v", err)
	case errors.Is(err, token.ErrUnavailable):
		return false, remote(err)
	case err != nil:
		return false, remote(fmt.Errorf("minting the run's budget token: %w", err))
	}
	// Minted: only now replace what an earlier attempt left (a --retry, or a
	// takeover of a stale claim), so a failed mint leaves it alone.
	if err := token.DeleteObject(ctx, e.bucket, slug, run); err != nil {
		return false, remote(err)
	}
	if err := token.PutObject(ctx, e.bucket, slug, run, tok); err != nil {
		// A put that failed after writing leaves an untaken token valid
		// for an hour: remove it, best effort.
		if !errors.Is(err, token.ErrObjectExists) {
			e.dropBudgetToken(ctx, slug, run)
		}
		return false, remote(err)
	}
	return true, nil
}

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
