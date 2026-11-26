package credsvc

// I1's end-to-end test: the identity contract answered over the SOCKET, through
// server.go's dispatch and the door (identity_mount.go), against the handlers
// LM1 to LM4 wrote and the orchestrator's check LM24 — with the parts a
// deployment really wires: the issuance store and the launch scope over
// internal/gcpfake's Realtime Database, the authenticator over the fixture's key
// sets, and the runs' signer over the fake IAM Credentials endpoint.
//
// Why the socket and not the handlers: LM1 to LM4 have tests of their own that
// call each handler DIRECTLY, which is what a case about one decision needs.
// What a handler test cannot see is the door — that these paths are answered
// ahead of the shared parse, that the caller's class is verified once for the
// two routes a launcher calls, and that an installation with no issuer answers
// before anything of a call is read — and that is what this file drives. The
// door's dispatch is asserted where it is written (identity_routes_test.go) and
// the paths' place ahead of the shared parse by scope_installation_test.go;
// here they are reached the way a launcher, a run and a phone reach them.
//
// What is deliberately NOT here: the exchange at Identity Toolkit, which is the
// CLI test's (internal/cli/launch_identity_e2e_test.go drives a whole launch to
// a session), and the halt a losing run takes when its ticket was already
// claimed, which is LM7's own (TestRunnerHaltsWhenTicketAlreadyClaimed): what
// this file asserts is the wire answer the losing run halts on.
//
// Everything is hermetic (Convention 9), and every value that stands for a
// secret is drawn at run time: the tickets and tokens here are ones the service
// under test drew or the fixture signed.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// testNoScopeLauncher is a THIRD person on the fixture's roster, and the one
// the records place as a viewer of the fixture's repository: it is what makes
// the launch-scope refusal below a refusal of a caller this service ADMITTED
// (LM3) and then judged (LM23) — where an address on no roster is refused one
// step earlier, by the class, with a different code and no audit line.
const testNoScopeLauncher = "launcher-3@example.invalid"

// identityE2E is the fixture: one installation whose identity half is wired the
// way internal/cli/credentials_serve.go wires it (I1), over one fake database
// that holds the issuance records, the grants node and the published policy.
type identityE2E struct {
	*svc
	db     *gcpfake.RTDB
	store  IssuanceStore
	record *recordFake
}

// launcherTokenOf is the Google ID token a person on the roster presents: the
// launcher class's own token (launcherToken), with this address. The fixture's
// names the first of the roster's people, and this file drives three.
func (e *identityE2E) launcherTokenOf(addr string) string {
	return e.launcherToken(func(c map[string]any) { c["email"] = addr })
}

// newIdentityE2E is the fixture. The mutate hooks are for the cases that need
// one thing wired differently — an installation with no issuer is the one below
// — and they run before the server is built.
func newIdentityE2E(t *testing.T, mutate ...func(*svc, *Config)) *identityE2E {
	t.Helper()
	client, db, store := ticketStore(t)
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(testClaimSigner)
	record, pauses := &recordFake{}, &pausesFake{}
	repos := map[string]string{testSlug(t, testRepoPath): testRepo, testSlug(t, testOtherRepoPath): testOtherRepo}
	scope, err := NewOrchestratedScope(
		NewLaunchScope(LaunchScopeConfig{
			Repos:   repos,
			Records: RTDBAccess{DB: client},
			Policy:  RTDBPolicy{DB: client},
		}), record, pauses)
	if err != nil {
		t.Fatalf("the fixture's launch scope: %v", err)
	}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		// The authenticator is rebuilt here because the three classes the
		// ticket route admits are its configuration (LM3): the roster's people,
		// its service accounts and the resident orchestrator.
		auth, err := NewAuthenticator(AuthConfig{
			Project:                 testProject,
			FirebaseProject:         testFirebase,
			Audience:                testAudience,
			JobAccounts:             []string{testJobSA, testOtherSA},
			Keys:                    s.keys,
			JobKeys:                 s.jobKeys,
			Outcomes:                RTDBOutcomes{DB: s.db},
			LauncherAccounts:        []string{testLauncherAccount, testOtherLauncher, testNoScopeLauncher},
			LauncherServiceAccounts: []string{testRosterSA},
			OrchestratorAccounts:    []string{testOrchestratorSA},
			Now:                     func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's authenticator cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		// The real database client, which is what the store, the scope's
		// records and the scope's policy are all read over — one client, as
		// the deployment has one.
		cfg.DB = client
		cfg.Repos = repos
		cfg.Identity = IdentityConfig{
			Signer:     testClaimSigner,
			Operators:  []string{testLauncherAccount},
			Store:      store,
			Scope:      scope,
			MaxRuntime: testMaxRuntime,
		}
		for _, m := range mutate {
			m(s, cfg)
		}
	})
	// The records: the two people that may launch the fixture's repository, and
	// a third the records place as a viewer of it. Nothing publishes a policy,
	// which is the state of an installation that never published one: the
	// allow-list admits every repository, and what judges a person is the grant
	// it holds.
	doc, err := accesslib.Encode([]accesslib.Grant{
		scopeGrant(t, "user:"+testLauncherAccount, accesslib.RoleOperator, testRepo),
		scopeGrant(t, "user:"+testOtherLauncher, accesslib.RoleOperator, testRepo),
		scopeGrant(t, "user:"+testNoScopeLauncher, accesslib.RoleViewer, testRepo),
	})
	if err != nil {
		t.Fatalf("the fixture's grants: %v", err)
	}
	db.Set(accesslib.PathNode, json.RawMessage(doc))
	// The signer is the real construction (token.NewIAMSigner over
	// token.IAMSigner) pointed at the fake endpoint: what a test replaces is
	// where the service's own credential comes from, and everything above it —
	// the claims' checks, the payload and token.MintAt's re-read — is the real
	// code.
	old := IdentitySignerFor
	IdentitySignerFor = func(_ context.Context, email string) (token.Signer, error) {
		return token.NewIAMSigner(email,
			oauth2.StaticTokenSource(&oauth2.Token{AccessToken: testClaimCredential}),
			token.WithIAMEndpoint(iam.URL))
	}
	t.Cleanup(func() { IdentitySignerFor = old })
	return &identityE2E{svc: s, db: db, store: store, record: record}
}

// ---------------------------------------------------------------------------
// The calls
// ---------------------------------------------------------------------------

// ask is the launch this file drives: the fixture's own repository and run, its
// job's account, the launcher asking for it, a branch and a runtime inside the
// installation's maximum. It names no job, which is a Cloud Run launch's ask —
// the case the VM link is asked nothing about (claim_attestation.go).
func (e *identityE2E) ask() identitywire.TicketRequest {
	return identitywire.TicketRequest{
		Slug:              e.slug,
		Run:               e.run,
		Account:           testJobSA,
		Launcher:          testLauncherAccount,
		Branch:            "fugaro/" + e.run,
		MaxRuntimeSeconds: int(testAskRuntime / time.Second),
	}
}

// post sends one identity call over the socket with the caller's own token in
// the bearer and no run identity: not one of the six routes carries a run, and
// the door's whole reason for being ahead of the shared parse is that their
// callers have none.
func (e *identityE2E) post(t *testing.T, path, body, bearer string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, m := range mutate {
		m(req)
	}
	resp, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call to %s failed: %v", path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s's answer: %v", path, err)
	}
	return resp, b
}

// claim is one call to the claim route as the run: the job's own Google ID
// token in the bearer and the ticket and nonce in the body, and nothing else
// (the claim is the run's first call and carries no run identity).
func (e *identityE2E) claim(t *testing.T, ticket, nonce string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return e.post(t, identitywire.ClaimPath, mustBody(t, identitywire.ClaimRequest{Ticket: ticket, Nonce: nonce}), e.jobToken(), mutate...)
}

// claimed claims a ticket and answers what the service answered, failing the
// test on any refusal.
func (e *identityE2E) claimed(t *testing.T, ticket, nonce string) identitywire.ClaimResponse {
	t.Helper()
	resp, body := e.claim(t, ticket, nonce)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the claim is answered %d, want 200: %s", resp.StatusCode, body)
	}
	var ans identitywire.ClaimResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the claim's answer is not one this contract reads: %v (%s)", err, body)
	}
	return ans
}

// asAccount is a mutation presenting ANOTHER account's Google ID token: the
// installation's second job — which is a job account of this installation and
// still not the run's — or a person's.
func (e *identityE2E) asAccount(email string, person bool) func(*http.Request) {
	return func(r *http.Request) {
		tok := e.jobToken(func(c map[string]any) { c["email"] = email })
		if person {
			tok = e.personToken(email)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
	}
}

// refused checks one refusal whole: the status, the contract's code, and that
// the call added exactly ONE audit line naming the verified caller (LM1's rule,
// audit.go). before is the line count the test read before the call, because
// the fixture's audit buffer is never reset (logBuf has no Reset) and one test
// drives many refusals: the difference is what makes "exactly one" a property
// of the call.
func (e *identityE2E) refused(t *testing.T, before int, resp *http.Response, body []byte, status int, code string) map[string]any {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, status, body)
	}
	if got := refusal(t, status, body).Error; got != code {
		t.Errorf("the refusal is %q, want the contract's %q: %s", got, code, body)
	}
	lines := e.lines(t)
	if len(lines) != before+1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one more than the %d already there: %v", len(lines)-before, before, lines[before:])
	}
	line := lines[before]
	checkAuditShape(t, line)
	return line
}

// refusedQuietly is refused for a call the service refuses BEFORE any caller is
// verified: no audit line at all, because a line names an identity this service
// verified and there is none (audit.go).
func (e *identityE2E) refusedQuietly(t *testing.T, before int, resp *http.Response, body []byte, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, status, body)
	}
	if got := refusal(t, status, body).Error; got != code {
		t.Errorf("the refusal is %q, want the contract's %q: %s", got, code, body)
	}
	if got := len(e.lines(t)); got != before {
		t.Errorf("the call wrote %d audit lines, want none: a refusal made before the caller was verified has no identity to name", got-before)
	}
}

// lines is the audit lines the fixture holds now, parsed the way an operator's
// tooling reads them.
func (e *identityE2E) lines(t *testing.T) []map[string]any {
	t.Helper()
	if e.audit.Len() == 0 {
		return nil
	}
	return auditLines(t, e.audit)
}

// ---------------------------------------------------------------------------
// The tests
// ---------------------------------------------------------------------------

// TestIdentityEndToEnd is the launch one person makes and the acts around it: a
// ticket issued, claimed, and the claim's own retry; a second claim refused and
// a copied ticket refused with it; another caller's ask for that run refused; a
// caller the records place but do not admit refused; the orchestrator's ask for
// a run its record does not carry refused; and a pairing completed.
//
// Every refusal past the caller's verification is checked for its ONE audit
// line, and the issues and the pairing for none: that is the property audit.go
// states (a StatusOK line requires a credential, and these routes issue none)
// and the one LM1's plan makes a property of the route rather than of a
// discipline.
func TestIdentityEndToEnd(t *testing.T) {
	e := newIdentityE2E(t)
	ctx := context.Background()
	ask := e.ask()

	// The ask. A person on the roster asks for the run it is about to start,
	// and is answered a ticket — a credential, which is why the issue is
	// recorded on the service's own line and not as an audit line.
	resp, body := e.post(t, identitywire.TicketPath, mustBody(t, ask), e.launcherToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the ask is answered %d, want 200: %s", resp.StatusCode, body)
	}
	var ticket identitywire.TicketResponse
	if err := json.Unmarshal(body, &ticket); err != nil {
		t.Fatalf("the answer is not one this contract reads: %v (%s)", err, body)
	}
	if ticket.Ticket == "" {
		t.Fatalf("the answer carries no ticket: %s", body)
	}
	if !ticket.ExpiresAt.After(e.now) {
		t.Errorf("the ticket expires at %s, want a time after the fixture's own instant (%s)", ticket.ExpiresAt, e.now)
	}
	if got := len(e.lines(t)); got != 0 {
		t.Errorf("the issue wrote %d audit lines, want none: a StatusOK line requires a credential and this route issues none", got)
	}
	rec, found, err := e.store.Get(ctx, e.slug, e.run)
	if err != nil || !found {
		t.Fatalf("the run has no issuance record (found = %v, err = %v): the ticket's whole fence is the digest written with it", found, err)
	}
	if rec.TicketHash != identitywire.Digest(ticket.Ticket) {
		t.Errorf("the record holds the digest of another ticket: a ticket the launcher was not answered can never claim this run")
	}
	if rec.Caller != testLauncherAccount || rec.ExpectedAccount != testJobSA || rec.Class != identitywire.ClassPerson {
		t.Errorf("the record is %+v, want the verified caller, the ask's account and the class the door admitted", rec)
	}
	if e.db.Value(identitywire.PathRecord(e.slug, e.run)) == nil {
		t.Errorf("no record is at %s: the path is the contract's and is what the claim reads the run back from", identitywire.PathRecord(e.slug, e.run))
	}

	// The claim: the run's first call, from the job account the launch named,
	// with the ticket and the nonce and no run identity. The answer is the
	// custom token, signed as the runs' signer with the RECORD's claims.
	claimed := e.claimed(t, ticket.Ticket, testNonce)
	if claimed.Token == "" {
		t.Fatal("the claim's answer carries no token")
	}
	if !claimed.ExpiresAt.After(e.now) {
		t.Errorf("the token's claims expire at %s, want a time after the fixture's instant", claimed.ExpiresAt)
	}

	// The same nonce again is this claim's own retry (R2): a claim whose answer
	// a run lost must not cost the run its identity, so it is answered rather
	// than refused — and it is not a second claim.
	retry := e.claimed(t, ticket.Ticket, testNonce)
	if retry.Token != claimed.Token {
		t.Errorf("the retry's token differs from the claim's: the record's claims and the service's clock are the same, so the same nonce is the same signature")
	}
	if got := len(e.lines(t)); got != 0 {
		t.Errorf("the claim and its retry wrote %d audit lines, want none: neither is a refusal", got)
	}

	// A second claim on a claimed ticket with ANOTHER nonce is R5's loser: one
	// refusal, one audit line, and no token. The run that loses halts on
	// exactly this answer, which is LM7's test.
	lines := len(e.lines(t))
	resp, body = e.claim(t, ticket.Ticket, testOtherNonce)
	line := e.refused(t, lines, resp, body, http.StatusForbidden, identitywire.RefusalIdentityRefused)
	if got := line["run"]; got != e.run {
		t.Errorf("the line names run %v, want %q: the run is the fact R5's residual turns on", got, e.run)
	}
	if got := line["caller"]; got != "" {
		t.Errorf("the line names caller %v, want none: the claim's caller is a job account with no job of its own to report", got)
	}

	// A copied ticket claimed by ANOTHER of the installation's job accounts is
	// refused too: the ticket is bound to the account the launch named, and a
	// job that copies it from the record or from a log proves the wrong job.
	lines = len(e.lines(t))
	resp, body = e.claim(t, ticket.Ticket, testOtherNonce, e.asAccount(testOtherSA, false))
	e.refused(t, lines, resp, body, http.StatusForbidden, identitywire.RefusalIdentityRefused)

	// Another person's ask for that run is refused (R5): the record is the
	// caller's, and the class that admits the ask is the roster — so the
	// refusal is the record's and not the roster's.
	lines = len(e.lines(t))
	resp, body = e.post(t, identitywire.TicketPath, mustBody(t, ask), e.launcherTokenOf(testOtherLauncher))
	line = e.refused(t, lines, resp, body, http.StatusBadRequest, identitywire.RefusalIdentityRefused)
	if got := line["caller"]; got != testOtherLauncher {
		t.Errorf("the line names caller %v, want %q: the verified caller is the one fact this line carries", got, testOtherLauncher)
	}
	for _, field := range []string{"run", "repository"} {
		if got := line[field]; got != "" {
			t.Errorf("the line carries %s %v, want none: the ask's own bytes are not values this service verified", field, got)
		}
	}

	// A caller the records place as a viewer of this repository holds no launch
	// scope on it (R16, LM23): it is REFUSED, and the class is not what
	// refused it — the roster admitted it and the records judged it.
	lines = len(e.lines(t))
	resp, body = e.post(t, identitywire.TicketPath, mustBody(t, ask), e.launcherTokenOf(testNoScopeLauncher))
	line = e.refused(t, lines, resp, body, http.StatusForbidden, identitywire.RefusalNoLaunchScope)
	if got := line["status"]; got != ReasonNotAdmitted {
		t.Errorf("the refusal is recorded as %v, want %q: the caller is one this service admitted and did not place", got, ReasonNotAdmitted)
	}

	// The orchestrator's ask for a run its record does not carry is refused
	// (LM24): the class is bound to its own record, and the record here holds
	// no attempt at all. The code is the launch scope's — the ticket route
	// answers every scope refusal that is not a fault with it — and the
	// orchestrator's check is composed behind the same hook, which is what I1
	// wires.
	lines = len(e.lines(t))
	orchestrated := ask
	orchestrated.Run = branchRun
	orchestrated.Attempt = identitywire.AttemptID("wi-1:1")
	orchestrated.Launcher = "orchestrator"
	resp, body = e.post(t, identitywire.TicketPath, mustBody(t, orchestrated), e.orchestratorToken())
	e.refused(t, lines, resp, body, http.StatusForbidden, identitywire.RefusalNoLaunchScope)
	if e.record.reads != 1 {
		t.Errorf("the orchestrator's record was read %d times, want 1: the class's judgement IS that record", e.record.reads)
	}

	// The pairing, which is the act a phone completes in two halves: the
	// pairing token the operator's machine asks for before the code is shown,
	// and the device token it asks for once the phone has written its key. Both
	// are the runs' signer's and neither is an audited act (identity_pair.go:
	// the audit's purpose set holds no identity act).
	resp, body = e.post(t, identitywire.PairingPath, "{}", e.launcherToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the pairing is answered %d, want 200: %s", resp.StatusCode, body)
	}
	var pairing identitywire.PairingResponse
	if err := json.Unmarshal(body, &pairing); err != nil {
		t.Fatalf("the pairing's answer is not one this contract reads: %v (%s)", err, body)
	}
	if pairing.Token == "" || pairing.Pairing == "" {
		t.Errorf("the pairing's answer is %+v, want a token and the pairing it names", pairing)
	}
	resp, body = e.post(t, identitywire.DevicePath, "{}", e.launcherToken())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the device ask is answered %d, want 200: %s", resp.StatusCode, body)
	}
	var device identitywire.DeviceResponse
	if err := json.Unmarshal(body, &device); err != nil {
		t.Fatalf("the device's answer is not one this contract reads: %v (%s)", err, body)
	}
	if device.Token == "" || device.Device == "" {
		t.Errorf("the device's answer is %+v, want a token and the device it names", device)
	}

	// Five refusals above owed a line and the issues owed none, so the whole
	// test leaves exactly five: a path that wrote two or none is a defect here
	// rather than a line an operator reads later.
	if got := len(e.lines(t)); got != 5 {
		t.Errorf("the test wrote %d audit lines, want 5 — one per refusal of a verified caller and none for an issue: %v", got, e.lines(t))
	}
}

// TestIdentityCrossClassRefused is the contract's class rule held from the
// outside: no token of one kind is accepted on another kind's route. Each row
// presents the token that route's own class would carry — or one of another
// class's — and every one of them is refused caller_not_admitted.
//
// The last rows are the reason the rule is checked at the DOOR and not in one
// handler: the ticket route's three classes are verified there, so a call that
// reaches a handler has already been named, and the routes whose caller is a
// person or a job account each refuse the other two before anything of the
// installation is read — with no audit line, because no caller was verified to
// name (audit.go).
func TestIdentityCrossClassRefused(t *testing.T) {
	e := newIdentityE2E(t)
	ask := e.ask()

	for _, tc := range []struct {
		name string
		path string
		body string
		tok  string
		// line is true for the one row whose caller the route VERIFIES before
		// refusing it: the probe admits the roster's people and refuses the
		// launchers that are not its operators, so that refusal owes a line —
		// the narrowest this service writes (auditIdentity): the run identity's
		// fields and the status, and no caller, because the probe's and the
		// claim's callers are read off tokens that name no job this service
		// could write.
		line bool
	}{
		{
			// The run's own Firebase identity token at the ticket route: the
			// token the run carries is the CLAIM's answer, and it is not a
			// Google ID token any class here verifies.
			name: "a run's token at the ticket route",
			path: identitywire.TicketPath, body: mustBody(t, ask), tok: e.runToken(),
		},
		{
			// A run's own job account at the ticket route: the class the claim
			// route exists to answer instead (R4).
			name: "a job account at the ticket route",
			path: identitywire.TicketPath, body: mustBody(t, ask), tok: e.jobToken(),
		},
		{
			name: "a person at the claim route",
			path: identitywire.ClaimPath, body: mustBody(t, identitywire.ClaimRequest{Ticket: ticketTestSlug + "/x/y", Nonce: testNonce}), tok: e.launcherToken(),
		},
		{
			name: "a roster service account at the claim route",
			path: identitywire.ClaimPath, body: mustBody(t, identitywire.ClaimRequest{Ticket: ticketTestSlug + "/x/y", Nonce: testNonce}), tok: e.serviceAccountToken(testRosterSA),
		},
		{
			// A service account pairs nothing: the two phone routes are the
			// roster's PEOPLE's (R14).
			name: "a roster service account at the pairing route",
			path: identitywire.PairingPath, body: "{}", tok: e.serviceAccountToken(testRosterSA),
		},
		{
			name: "the orchestrator at the device route",
			path: identitywire.DevicePath, body: "{}", tok: e.orchestratorToken(),
		},
		{
			// The probe is the roster's OPERATORS' alone: this caller is a
			// launcher of this installation, verified, and not one of them.
			name: "a launcher who is not an operator at the probe route",
			path: identitywire.ProbePath, body: "{}", tok: e.launcherTokenOf(testOtherLauncher), line: true,
		},
		{
			name: "a job account at the probe route",
			path: identitywire.ProbePath, body: "{}", tok: e.jobToken(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(e.lines(t))
			resp, body := e.post(t, tc.path, tc.body, tc.tok)
			if tc.line {
				line := e.refused(t, before, resp, body, http.StatusForbidden, identitywire.RefusalCallerNotAdmitted)
				for _, field := range []string{"caller", "run", "repository"} {
					if got := line[field]; got != "" {
						t.Errorf("the line carries %s %v, want none: this route's line is the run identity's fields and the status", field, got)
					}
				}
				if got := line["status"]; got != ErrorUnverifiedRunIdentity {
					t.Errorf("the refusal is recorded as %v, want %q: the caller is verified and holds no run identity of its own", got, ErrorUnverifiedRunIdentity)
				}
				return
			}
			e.refusedQuietly(t, before, resp, body, http.StatusForbidden, identitywire.RefusalCallerNotAdmitted)
		})
	}
}

// TestNoSignerConfiguredIsNotServed is the state every installation is in until
// `fugaro update` gives it an issuer (LM11, LM12): the six paths answer the
// contract's "not served" (R7) on every one of them, BEFORE anything of a call
// is read — so a launcher that reaches an installation whose roots carry no
// issuer row yet stays on the bridge and signs a run's identity locally, and
// never reads the answer as a refusal of its own ask.
//
// The call is made with no bearer and no body at all, which is what "before
// anything of it is read" means: a service that read the caller first would
// answer "no bearer" instead, and a launcher would read that as its own fault.
// No audit line is written for the same reason, and the signer's own seam is
// never reached: there is nothing to sign with.
func TestNoSignerConfiguredIsNotServed(t *testing.T) {
	e := newIdentityE2E(t, func(_ *svc, cfg *Config) { cfg.Identity.Signer = "" })
	reached := false
	old := IdentitySignerFor
	IdentitySignerFor = func(context.Context, string) (token.Signer, error) {
		reached = true
		return old(context.Background(), testClaimSigner)
	}
	t.Cleanup(func() { IdentitySignerFor = old })

	for _, path := range []string{
		identitywire.TicketPath,
		identitywire.ClaimPath,
		identitywire.PairingPath,
		identitywire.DevicePath,
		identitywire.ProbePath,
		identitywire.JobPath,
	} {
		t.Run(path, func(t *testing.T) {
			resp, body := e.post(t, path, "", "")
			e.refusedQuietly(t, 0, resp, body, StatusUnavailable, identitywire.RefusalNotServed)
		})
	}
	if reached {
		t.Error("the runs' signer was built for a service that holds no issuer: there is nothing to sign with, and the seam is reached once a deployment has one")
	}
}
