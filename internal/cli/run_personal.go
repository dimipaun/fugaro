package cli

// The person's own token at the workspace, asked for at the launch of a run
// that is to draw on it (WC7 of docs/workspace-credentials/; R8, D3141, D3142,
// D3147, and D2976's consequence).
//
// A run's model calls are served from somewhere, and the record this
// repository's own `fugaro update` published — creds.RoutePath, which the run
// itself reads at bootstrap (internal/runner's readRoute) — is what says where.
// When it says the workspace, and when the member's published policy turns
// R9's switch on, the WS7A route exists to serve the launcher's own
// claude-oauth-token to its own runs (WC4): this launch asks for the grant by
// C1's shapes and hands the job the id it is answered, under C1's own
// environment name, which WC8 claims at the workspace's service.
//
// Four decisions of the reconciliation are the shape of what is here.
//
//   - **D3141: it follows the route.** The project's own model credential
//     shadows the person's token, so a launch asks only where the record moves
//     the model kind to the workspace. The route is read from the member's own
//     database — the one this machine's `fugaro update` published it into, and
//     never an environment variable, which is F32's whole point for the run's
//     own read.
//
//   - **D3142: automatically, and skippable once.** A person stored the token
//     to use it, so a run of theirs draws on it without being asked, and the
//     launch says which token it draws on in one line naming the workspace
//     and the flag that turns it off. `--no-personal-token` asks for no grant
//     at all, which is a launch that never reads the route, never mints and
//     never calls.
//
//   - **D3147: a failed grant never stops a launch.** "No token held" is an
//     ANSWER and prints nothing (R8): that person's runs start on the shared
//     entries exactly as they did before this plan. A refusal and an
//     unreachable service are the two things it is not, and both start the run
//     on the shared entries with one line saying why — the classes are kept
//     apart because a launcher that folded them together could not say which
//     happened.
//
//   - **D2976's consequence: a service account asks for none.** A run launched
//     by one holds no personal token, and this launcher does not ask a machine
//     that is not a person: gcloud's own listing of the account it would mint
//     with is read first.
//
// Nothing here waits for input: the grant is one bounded call, and every answer
// it can give ends the launch's part of this at once (the closed-stdin harness
// holds the whole command tree to that).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
)

// personGrant is the grant call a launch makes, and a package variable for
// slipGrant's reason: the call carries the launcher's own identity token, so a
// test about something else replaces it rather than reaching a service or
// minting a credential. A test about the grant itself calls
// personGrantOverService against a service of its own.
var personGrant = personGrantOverService

// personGrantIdentity mints the launcher's own Google ID token for the
// workspace's service at serviceURL — the identity the workspace's launcher
// class verifies (C2's Launchers) and the only credential this call presents.
//
// It is the PLAIN mint and not the account-aware one (personIdentityArgs): the
// account it is reached with is a person's own, because a service account never
// asks for a grant at all (D2976), and gcloud refuses --audiences outright for
// a user account. Its audience is therefore gcloud's own client id, which is
// the audience the workspace's launcher class admits for a person exactly as
// the installation's does (D1939, D1944).
//
// It is a package variable so that a test replaces it: no test mints a real
// credential, and no value this returns or refuses with reaches an error, a log
// line or an output — the refusals below name the work and never the service.
var personGrantIdentity = func(ctx context.Context, serviceURL string) (string, error) {
	if _, err := reviewGcloudLookPath(reviewGcloudBin); err != nil {
		return "", fmt.Errorf("%s is not on PATH", reviewGcloudBin)
	}
	cctx, cancel := context.WithTimeout(ctx, reviewIdentityTimeout)
	defer cancel()
	out, err := reviewGcloudRun(cctx, "auth", "print-identity-token", "--quiet")
	if err != nil {
		return "", fmt.Errorf("your own Google ID token could not be minted; run `%s auth login`", reviewGcloudBin)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" || len(tok) > reviewIdentityMaxBytes || !reviewPrintableToken(tok) {
		return "", fmt.Errorf("`%s auth print-identity-token` answered with no usable identity token", reviewGcloudBin)
	}
	return tok, nil
}

// personGrantOverService is the grant call: the launcher's own identity token
// presented to the workspace's own grant route, and the answer read strictly.
//
// Nothing here can put a value a caller sent into an error: the identity token
// is never formatted into one, the request body is the launcher's own names,
// and the answer is read for its status, its size and its shape alone.
func personGrantOverService(ctx context.Context, serviceURL string, req creds.PersonGrantRequest) (creds.PersonGrant, error) {
	if err := checkSlipServiceURL(serviceURL); err != nil {
		return creds.PersonGrant{}, errors.New("the workspace's service address is not one this launcher sends its own identity token to")
	}
	identity, err := personGrantIdentity(ctx, serviceURL)
	if err != nil {
		return creds.PersonGrant{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return creds.PersonGrant{}, errors.New("the workspace grant call's request could not be built")
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(serviceURL, "/")+credsvc.SlipPath, bytes.NewReader(body))
	if err != nil {
		return creds.PersonGrant{}, errors.New("the workspace grant call's request could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	resp, err := slipClient().Do(hreq)
	if err != nil {
		return creds.PersonGrant{}, errors.New("the workspace's credentials service could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode == credsvc.StatusRequestRefused {
		// C1's one class of refusal, read back as its sentinel: the answer names
		// no account, no repository and no person, and this launcher has one
		// thing to branch on (D3147).
		return creds.PersonGrant{}, fmt.Errorf("%w: the workspace answered %d", creds.ErrPersonGrantRefused, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		// Everything else the service can say — its own state, an intermediary —
		// is not that class and is not read as one.
		return creds.PersonGrant{}, fmt.Errorf("the workspace's credentials service answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSlipAnswerBytes+1))
	if err != nil {
		return creds.PersonGrant{}, errors.New("the workspace's answer to the grant call could not be read")
	}
	if len(data) > maxSlipAnswerBytes {
		return creds.PersonGrant{}, errors.New("the workspace's answer to the grant call is larger than a grant's")
	}
	grant, err := creds.ParsePersonGrant(data)
	if err != nil {
		// The strict decode is what keeps the rest of the answer out of this
		// process: a body carrying a claim secret is refused here rather than
		// read for the one field this launcher wants (F14), and the claim secret
		// is answered by the claim route alone, to the run the slip names.
		return creds.PersonGrant{}, errors.New("the workspace's answer to the grant call is not a grant's")
	}
	return grant, nil
}

// runPersonalEnv asks the workspace's service for the grant this run draws on,
// and answers the environment entry that hands the job the workspace slip's id
// (creds.PersonSlipEnv, which WC8 claims at bootstrap) — or nothing at all,
// which is every state but a granted slip.
//
// The two conditions are read in the order that costs the least: the route
// record this repository published says whether the workspace serves its model
// at all (D3141), the member's published policy says whether the personal half
// is on (R9), and only then does the launcher look at the account gcloud would
// mint with (D2976) and make the call.
//
// A route record that cannot be read is answered as no record and the launch
// says nothing: the run reads the same record itself at bootstrap and stops on
// one it cannot read, so a line here would name a state the run is about to
// report louder. A switch that cannot be read is NOT the same case and is said
// out loud — a policy that cannot be read is never a switch that is off (C1) —
// and the run starts on the shared entries, which is D3147's outcome.
//
// account is the account the execution this environment goes to will call as,
// derived by the caller from the job that execution is on, and it is the same
// one the member's grant names: the workspace holds this grant to it too (R3).
func runPersonalEnv(ctx context.Context, env *cloudEnv, spec *task.Spec, slug, account string) []string {
	if env.lc == nil || env.lc.Budget == nil || env.lc.Budget.RTDBURL == "" {
		// No database is no published policy and no published route: this
		// project is not a member of any workspace, and there is nothing to
		// read either condition out of.
		return nil
	}
	db, err := newBudgetClient(ctx, env.lc.Budget.RTDBURL, env.lc.Endpoints.NoAuth)
	if err != nil {
		return nil
	}
	route, err := personalRoute(ctx, db, slug)
	if err != nil {
		return nil
	}
	if route.Holder(creds.KindModel) != creds.HolderWorkspace {
		return nil
	}
	where := personalWhere(env.lc)
	on, err := projectpolicy.PersonSwitchOverDB(db).PersonSwitchOn(ctx, env.lc.Name)
	if err != nil {
		personalWithout(where, "the member's published policy could not be read")
		return nil
	}
	if !on {
		// The switch is off, which is R9's absent — a member that never turned
		// the personal half on launches exactly what it launched before.
		return nil
	}
	cfg, err := personGcloudAccount(ctx)
	if err != nil || cfg.serviceAccount() {
		// D2976's consequence: a machine that is a service account — or one
		// whose gcloud names no account this launch could mint as — holds no
		// personal token and is asked for none. Nothing is said: a daemon's
		// launch is the ordinary state of this machine, not a fault of it.
		return nil
	}
	// The request is built in its own statement, as runSlipEnv's is, so that the
	// name the answer is read under is bound by no expression of internal/creds:
	// this launcher names the grant's two fields and holds no store of the slip
	// half, which is the boundary that keeps a slip out of everything but the
	// service (internal/creds' TestBoundaryAllowsSlipsOnlyViaService).
	req := creds.PersonGrantRequest{
		Slug:    slug,
		Run:     spec.RunID,
		Account: account,
		Name:    creds.PersonTokenName,
	}
	sctx, cancel := context.WithTimeout(ctx, slipGrantTimeout)
	defer cancel()
	ans, err := personGrant(sctx, route.Address, req)
	switch {
	case errors.Is(err, creds.ErrPersonGrantRefused):
		personalWithout(where, "the workspace refused its grant")
		return nil
	case err != nil:
		personalWithout(where, oneLine(err.Error()))
		return nil
	}
	if !ans.Held {
		// R8's answer and not a failure: this person holds no token of the name
		// the launch asked about, so the run is served the shared entries
		// exactly as before, and there is nothing to say about it.
		return nil
	}
	if !slipIDUsable(ans.Slip) {
		personalWithout(where, "the workspace answered with no usable slip id")
		return nil
	}
	fmt.Fprint(launchWarn, noteLine(fmt.Sprintf("this run draws on your own token at %s; --no-personal-token launches one that does not", where)))
	return []string{creds.PersonSlipEnv + "=" + ans.Slip}
}

// personalRoute reads this repository's credential-route record out of the
// member's own database, as the run itself reads it at bootstrap
// (internal/runner's readRoute): a node that is not there, and a node this
// machine's token may not read, are both the zero Route — every kind the
// project's, which is what a repository that published no record at all reads —
// and a record that IS readable and cannot be decoded is an error.
func personalRoute(ctx context.Context, db *rtdb.Client, slug string) (creds.Route, error) {
	var raw json.RawMessage
	found, err := db.Get(ctx, creds.RoutePath(slug), &raw)
	switch {
	case err == nil:
	case errors.Is(err, rtdb.ErrPermission):
		return creds.Route{}, nil
	default:
		return creds.Route{}, errors.New("this repository's credential route could not be read")
	}
	if !found {
		return creds.Route{}, nil
	}
	route, _, err := creds.DecodeRoute(raw)
	if err != nil {
		return creds.Route{}, errors.New("this repository's credential route is not one this release reads")
	}
	return route, nil
}

// personalWhere names the workspace this member's launches draw on, as the
// member's own config records its membership: the name is the one a person
// reads in the line about their own token.
func personalWhere(lc *localcfg.Config) string {
	if lc.Workspace == "" {
		return "the workspace"
	}
	return "the workspace " + lc.Workspace
}

// personalWithout is the one line a launch prints when it does NOT draw on the
// person's own token, and why: D3147's outcome is that the run starts anyway,
// on the shared entries, and the person is told which entries it spends rather
// than left to wonder whether their token was used.
func personalWithout(where, why string) {
	fmt.Fprintf(launchWarn, "warning: this run spends the shared entries, not your own token at %s: %s\n", oneLine(where), oneLine(why))
}
