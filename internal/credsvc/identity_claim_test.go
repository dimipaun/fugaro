package credsvc

// LM2's tests: the claim route and the runs' signer (R2, R3, R4, R5, R8, F33).
//
// The claim is the run's first call and the probe is `update`'s, and the two
// share one property that shapes every test here: neither caller holds a run
// identity. So the claim is answered with no run identity header at all, the
// ticket and the nonce are the whole of what the call carries, and nothing of
// the call reaches the token — the run, the repository, the uid and the claims
// are the RECORD's, and the account is the verified one checked against it.
//
// What each test is for, in one line: the token is signed as the runs' signer
// with the record's own claims (R3); a ticket is claimed once and the second
// claim is refused with one line (R5); a retry with the same nonce is not a
// second claim (R2); the record is re-checked before the signing (R3's stated
// residual: the account is in the job tables, the uid comes from the record's
// path, the claims pass R4); the nonce is in no log and no audit line (R2); the
// probe signs the contract's fixed payload, discards it and answers none of it
// (R8, F33); another job account, an expired ticket and an unknown ticket are
// each refused with one line (R4, R5).
//
// Two of those refusals are the ticket's whole reason for existing and are
// asserted on their own: a ticket whose PATH and account are right and whose
// SECRET is not the record's is refused — the fence is the record's digest of
// the ticket and not the path it names, which any job account of the
// installation can spell — and a nonce that is no nonce at all is refused
// before the ticket is even read.
//
// Everything here is hermetic (Convention 9): the signing goes through
// internal/gcpfake's IAM Credentials fake, so token.MintAt's own checks, the
// payload and the re-read of the signed token are exercised rather than faked.
// The issuance store IS a fake, and it implements the once-only fence as the
// real one does — one atomic read-and-write under one lock — because a store
// that claimed unconditionally would make TestClaimIsOnce pass on a service
// that hands two runners one identity, which is the failure R5 is about
// (slip_test.go states the same rule for the slip's store, which is the real
// one over the fake database).
//
// The handlers are called DIRECTLY, and that is a fact of this release and not
// a shortcut: until I1 binds the door (identity_stub.go), every identity path
// answers "not served" through ServeHTTP whatever the configuration holds, so a
// call over the socket could not reach either route. The door's own property —
// that these paths are answered ahead of the shared parse — is asserted where
// it is written (scope_installation_test.go walks the five paths) and again
// here for the two this file is about.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

const (
	// testClaimSigner is the account the runs' identities are signed as: the
	// account the installation's row names (IdentityConfig.Signer), which is a
	// service account address because token.NewIAMSigner refuses anything else.
	testClaimSigner = "fugaro-runs-signer@aurora-dev.iam.gserviceaccount.com"
	// testClaimCredential is the credential the service's own signer is called
	// with. It is not a credential and not shaped like one: the fake IAM
	// endpoint admits any bearer, and what these tests are about is the payload
	// that reaches it and not what carried it there.
	testClaimCredential = "credsvc-claim-test-credential"
	// testNonce and testOtherNonce are the two values the runner drew: the
	// claim's own rule is that a nonce is in no log line and no audit line, so
	// what a test needs of one is that it is findable in a buffer and that it is
	// not a credential.
	testNonce      = "the-nonce-the-runner-drew-first"
	testOtherNonce = "the-nonce-the-runner-drew-second"
)

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// claimIssuance is the issuance store the identity routes are wired with here:
// one record per slug and run, in memory, with the once-only fence implemented
// as the real store's is — one atomic read-and-write under one lock — rather
// than as an unconditional write.
type claimIssuance struct {
	mu      sync.Mutex
	records map[string]identitywire.Record
	// moved is what a writer that replaced the record between the claim's read
	// and its fence leaves behind: the interface promises the record AS IT STANDS
	// after the call, and the fence is one atomic step on the node rather than a
	// lock over the handler's two calls, so the record the fence answers is not
	// necessarily the one Get answered. LM1's Put is that writer (R6). nil is a
	// store nothing moved under, which is every other test here.
	moved func(*identitywire.Record)
	// getErr is the store's own failure on a read, which the service answers as
	// its own and never as a decision about the ticket (R16's ScopeFault rule).
	// nil is a store that reads, which is every test but the one that states
	// that answer.
	getErr error
}

func newClaimIssuance() *claimIssuance {
	return &claimIssuance{records: map[string]identitywire.Record{}}
}

func issuanceNode(slug, run string) string { return slug + "/" + run }

// put writes a record at a path, which is what the ticket route does (LM1).
// The path is given apart from the record's own claims because a record whose
// claims name another run is exactly the state TestClaimRevalidatesTheRecord
// states: the path is where the record was written and the claims are what it
// holds.
func (f *claimIssuance) put(slug, run string, rec identitywire.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[issuanceNode(slug, run)] = rec
}

// record reads one back, which is what a test reads when it needs to know what
// a call left on it.
func (f *claimIssuance) record(t *testing.T, slug, run string) identitywire.Record {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[issuanceNode(slug, run)]
	if !ok {
		t.Fatalf("no issuance record was written for %s", issuanceNode(slug, run))
	}
	return rec
}

// Put is the ticket route's writer (LM1). The claim never calls it, so what it
// holds here is the interface's shape and the one rule the contract states for
// it: another caller's record is refused.
func (f *claimIssuance) Put(_ context.Context, slug, run string, rec identitywire.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if held, ok := f.records[issuanceNode(slug, run)]; ok && held.Caller != rec.Caller {
		return identitywire.ErrAnotherCaller
	}
	f.records[issuanceNode(slug, run)] = rec
	return nil
}

func (f *claimIssuance) Get(_ context.Context, slug, run string) (identitywire.Record, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return identitywire.Record{}, false, f.getErr
	}
	rec, ok := f.records[issuanceNode(slug, run)]
	return rec, ok, nil
}

// Claim is the fence (R5) and the retry rule (R2), in one atomic step as the
// real store's is: a record claimed by this account with THIS nonce digest is
// the same claim answered again, and every other claim on a claimed record is
// refused. Both comparisons fold as the real store's do (identity_record.go,
// sameAccount), so the two spellings of one address are one account here too.
//
// The real store's own ExpectedAccount floor (ErrAnotherCaller,
// identity_record.go) is deliberately NOT repeated here: the route decides that
// on the record this returns, and TestClaimChecksTheRecordTheStoreReturns drives
// that decision through moved below, which a double that refused first would
// make unreachable.
func (f *claimIssuance) Claim(_ context.Context, slug, run string, claim identitywire.Claim) (identitywire.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[issuanceNode(slug, run)]
	if !ok {
		return identitywire.Record{}, identitywire.ErrNoRecord
	}
	if f.moved != nil {
		f.moved(&rec)
		f.records[issuanceNode(slug, run)] = rec
	}
	switch {
	case rec.Claimed == (identitywire.Claim{}):
		rec.Claimed = claim
		f.records[issuanceNode(slug, run)] = rec
		return rec, nil
	case sameAccount(rec.Claimed.Account, claim.Account) && rec.Claimed.NonceHash == claim.NonceHash:
		return rec, nil
	default:
		return identitywire.Record{}, identitywire.ErrClaimed
	}
}

// claimFixture is the identity routes' fixture: the service under test, wired
// with an issuer — a signer that signs as the runs' signer through
// internal/gcpfake's IAM Credentials fake, the issuance store above, and the
// operators the probe admits.
type claimFixture struct {
	*svc
	issuance *claimIssuance
	iam      *gcpfake.IAMCredentials
}

// newClaimFixture is the fixture. operators is the roster's operator list the
// probe is configured with (R2, A2): empty is a service whose probe admits
// nobody, which is the state one of its refusals is about.
//
// The authenticator is rebuilt here rather than taken from the fixture because
// the launcher class is part of the probe's configuration, as it is for the
// slip's grant (newSlips): this is the one place a test says who the roster
// holds.
func newClaimFixture(t *testing.T, operators ...string) *claimFixture {
	t.Helper()
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(testClaimSigner)
	issuance := newClaimIssuance()
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:          testProject,
			FirebaseProject:  testFirebase,
			Audience:         testAudience,
			JobAccounts:      []string{testJobSA, testOtherSA},
			Keys:             s.keys,
			JobKeys:          s.jobKeys,
			Outcomes:         RTDBOutcomes{DB: s.db},
			LauncherAccounts: []string{testLauncherAccount},
			Now:              func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's authenticator cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		// The identity half wired the way a deployment wires it, the ticket
		// route's own parts included: the scope check, the installation's
		// runtime maximum, and the database the installation's scope reads a
		// run's outcome through (installationScope.Outcomes). A test can
		// therefore mint a ticket through the REAL ticket route and claim it
		// (TestClaimOfATicketTheTicketRouteMinted), which is the only way the
		// two spellings of a ticket are held to each other. It is the real
		// client over internal/gcpfake's database rather than one of the
		// fixture's own fakes, because the configured database is the Patcher
		// the deployment hands the service and the same value has to answer the
		// scope's Getter.
		client, _, _ := ticketStore(t)
		cfg.DB = client
		cfg.Identity = IdentityConfig{
			Signer: testClaimSigner, Operators: operators, Store: issuance,
			Scope: &fakeTicketScope{}, MaxRuntime: testMaxRuntime,
		}
	})
	// The signer the service builds is the real one (token.NewIAMSigner over
	// token.IAMSigner) pointed at the fake endpoint: what a test replaces is
	// where the service's own credential comes from, and everything above it —
	// the claims' checks, the payload, token.MintAt's re-read — is the real code.
	old := identitySignerFor
	identitySignerFor = func(_ context.Context, email string) (token.Signer, error) {
		return token.NewIAMSigner(email,
			oauth2.StaticTokenSource(&oauth2.Token{AccessToken: testClaimCredential}),
			token.WithIAMEndpoint(iam.URL))
	}
	t.Cleanup(func() { identitySignerFor = old })
	return &claimFixture{svc: s, issuance: issuance, iam: iam}
}

// ticket is a ticket for a slug and a run, drawn by the issuer's own mint
// (newTicket, identity_ticket.go): what a test puts on a record is what LM1
// writes, so a change to the ticket's spelling fails here rather than passing
// against a file that spells it twice. The ticket the ROUTE answers is what
// TestClaimOfATicketTheTicketRouteMinted claims, and it is the same spelling
// because there is one.
func (f *claimFixture) ticket(t *testing.T, slug, run string) string {
	t.Helper()
	ticket, err := newTicket(slug, run)
	if err != nil {
		t.Fatalf("a ticket could not be drawn: %v", err)
	}
	return ticket
}

// issued writes the record a launcher would have left for the fixture's own run
// and answers the ticket it was written with and the record as it was written.
// The mutations change the record as the test needs it; the record is written
// at the PATH the ticket names whatever they do to its claims, which is what
// lets a test state a record whose claims name another run.
func (f *claimFixture) issued(t *testing.T, mutate ...func(*identitywire.Record)) (string, identitywire.Record) {
	t.Helper()
	ticket := f.ticket(t, f.slug, f.run)
	rec := identitywire.Record{
		Caller:          testLauncherAccount,
		Class:           identitywire.ClassPerson,
		At:              f.now,
		Claims:          token.Claims{Slug: f.slug, Run: f.run, FP: testProject, RB: testLauncherAccount, FX: token.ExpiryFX(f.now, 2*time.Hour)},
		ExpectedAccount: testJobSA,
		TicketHash:      identitywire.Digest(ticket),
	}
	for _, m := range mutate {
		m(&rec)
	}
	f.issuance.put(f.slug, f.run, rec)
	return ticket, rec
}

// ---------------------------------------------------------------------------
// The calls
// ---------------------------------------------------------------------------

// claimCall is one call to the claim handler: the body the test wrote, the
// job's own Google ID token in the bearer unless the test hands another, and
// no run identity — a claim is the run's first call and carries none.
func (f *claimFixture) claimCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return f.call(t, f.server.identityClaim, identitywire.ClaimPath, body, f.jobToken(), mutate...)
}

// claimOK claims a ticket and answers what the service answered, failing the
// test on any refusal.
func (f *claimFixture) claimOK(t *testing.T, body string) identitywire.ClaimResponse {
	t.Helper()
	resp, b := f.claimCall(t, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the claim is answered %d, want 200: %s", resp.StatusCode, b)
	}
	var ans identitywire.ClaimResponse
	if err := json.Unmarshal(b, &ans); err != nil {
		t.Fatalf("the claim's answer is not one this contract reads: %v", err)
	}
	return ans
}

// probeCall is one call to the probe handler, with the launcher's own Google ID
// token in the bearer.
func (f *claimFixture) probeCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return f.call(t, f.server.identityProbe, identitywire.ProbePath, body, f.launcherToken(), mutate...)
}

// call drives one identity handler over an httptest recorder: the request the
// transport could not make yet (this release's stub answers the path), and
// otherwise the fixture's own — the same body, the same bearer.
func (f *claimFixture) call(t *testing.T, h func(context.Context, http.ResponseWriter, *http.Request), path, body, tok string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentTypeJSON)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	h(context.Background(), rec, req)
	return rec.Result(), rec.Body.Bytes()
}

// jobBearer is a call presenting ANOTHER account's Google ID token: the other
// repository's job, or an address no table of this installation holds.
func (f *claimFixture) jobBearer(email string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.jobToken(func(c map[string]any) { c["email"] = email }))
	}
}

// identityClaimBody is a claim's body, built from the contract's own type: the ticket
// the launcher asked for and the nonce the runner drew, and nothing else — the
// uid, the claims and the account are the record's (R2, R3).
func identityClaimBody(t *testing.T, ticket, nonce string) string {
	t.Helper()
	return mustBody(t, identitywire.ClaimRequest{Ticket: ticket, Nonce: nonce})
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

// identityLines is the audit lines an identity route wrote, parsed: one per
// refusal past the caller's verification, and none for a served call.
func (f *claimFixture) identityLines(t *testing.T) []map[string]any {
	t.Helper()
	if f.audit.Len() == 0 {
		t.Fatal("no audit line was written, and this call is a refusal past the caller's verification")
	}
	return auditLines(t, f.audit)
}

// wantIdentityLine holds one identity refusal's line to the contract's shape
// and to the fields this route may carry: the run and the repository it was
// refused for and the status, and NOTHING else. The other seven fields are
// asserted empty and not merely unchecked, which is the property this whole
// file turns on: a line that grew a caller, a job, a purpose or a credential is
// how a value of the call would reach the operator's audit, and the nonce and
// the ticket must have nowhere to land (R2).
//
// The status is the audit's own closed vocabulary and not the contract's code
// the caller is answered (identityAuditStatus): audit.go's validErrorType is
// the older ten and no task of this plan edits that file.
func wantIdentityLine(t *testing.T, line map[string]any, run, repository, status string) {
	t.Helper()
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"run": run, "repository": repository, "status": status,
		"job": "", "caller": "", "credential": "", "purpose": "", "tier": "", "slip": "", "owner": "",
	} {
		if got := line[field]; got != want {
			t.Errorf("the line's %s is %v, want %q", field, got, want)
		}
	}
}

// wantNothingSigned holds that no signature was asked for, which is what tells
// a refusal of the RECORD (R3, R4) from a refusal of the signing.
func (f *claimFixture) wantNothingSigned(t *testing.T) {
	t.Helper()
	if calls := f.iam.SignCalls(); len(calls) != 0 {
		t.Errorf("the signer was asked %d times, want none: the call was refused before any signing", len(calls))
	}
}

// wantUnclaimed holds that the record's claim is still the zero one: a refused
// claim must not have marked the ticket (R5), or the run the ticket was issued
// for could never claim it.
func (f *claimFixture) wantUnclaimed(t *testing.T) {
	t.Helper()
	if held := f.issuance.record(t, f.slug, f.run); held.Claimed != (identitywire.Claim{}) {
		t.Errorf("the record is claimed by %+v, want an unclaimed ticket", held.Claimed)
	}
}

// ---------------------------------------------------------------------------
// The claim
// ---------------------------------------------------------------------------

// TestClaimSignsTheRecordedClaims: the whole of R3 at the claim. The job's own
// Google ID token and an unclaimed ticket yield one custom token signed as the
// runs' signer, whose uid comes from the RECORD'S PATH and whose claims are the
// record's own — nothing of the call reaches either, and the answer is the
// token and when its claims expire and nothing else.
func TestClaimSignsTheRecordedClaims(t *testing.T) {
	f := newClaimFixture(t)
	ticket, rec := f.issued(t)

	resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the claim is answered %d, want 200: %s", resp.StatusCode, body)
	}
	var ans identitywire.ClaimResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the claim's answer is not one this contract reads: %v", err)
	}
	if ans.Token == "" {
		t.Fatal("the claim answered no token")
	}
	if !ans.ExpiresAt.Equal(rec.Claims.FX) {
		t.Errorf("the answer's expiry is %s, want the record's own fx %s", ans.ExpiresAt, rec.Claims.FX)
	}

	// The token is the runs' signer's, and its identity is the record's path's.
	claims, signer, err := f.iam.Verify(ans.Token)
	if err != nil {
		t.Fatalf("the token is not one the runs' signer signed: %v", err)
	}
	if signer != testClaimSigner {
		t.Errorf("the token is signed as %q, want the runs' signer %q", signer, testClaimSigner)
	}
	for field, want := range map[string]any{
		"iss": testClaimSigner,
		"sub": testClaimSigner,
		"aud": token.Audience,
		"uid": token.UID(f.slug, f.run),
	} {
		if got := claims[field]; got != want {
			t.Errorf("the token's %s is %v, want %q", field, got, want)
		}
	}
	iat, ok := claims["iat"].(json.Number)
	if !ok {
		t.Fatalf("the token carries no iat: %v", claims)
	}
	if at, err := iat.Int64(); err != nil || at != f.now.Unix() {
		t.Errorf("the token's iat is %v, want the signing's own instant %d", claims["iat"], f.now.Unix())
	}
	exp, ok := claims["exp"].(json.Number)
	if !ok {
		t.Fatalf("the token carries no exp: %v", claims)
	}
	if at, err := exp.Int64(); err != nil || at != f.now.Add(token.Lifetime).Unix() {
		t.Errorf("the token's exp is %v, want the package's own lifetime %d", claims["exp"], f.now.Add(token.Lifetime).Unix())
	}

	// The claim set is the record's, and the expiry in it is the record's fx —
	// the value the answer reported, read off the signed token.
	set, ok := claims["claims"].(map[string]any)
	if !ok {
		t.Fatalf("the token carries no claim set: %v", claims)
	}
	for field, want := range map[string]string{"fs": f.slug, "fr": f.run, "fp": testProject, "rb": rec.Claims.RB} {
		if got, _ := set[field].(string); got != want {
			t.Errorf("the token's claim %s is %v, want %q", field, set[field], want)
		}
	}
	if fx, ok := set["fx"].(json.Number); !ok {
		t.Errorf("the token's claim set carries no fx: %v", set)
	} else if ms, err := fx.Int64(); err != nil || ms != rec.Claims.FX.UnixMilli() {
		t.Errorf("the token's fx is %v, want the record's own %d", set["fx"], rec.Claims.FX.UnixMilli())
	}

	// The payload the signer was handed is the one that was signed, and it names
	// the run's uid: the uid is derived from the claims, which were held to the
	// record's path before the signing.
	calls := f.iam.SignCalls()
	if len(calls) != 1 {
		t.Fatalf("the claim signed %d times, want once", len(calls))
	}
	if calls[0].Signer != testClaimSigner {
		t.Errorf("the signer was asked to sign as %q, want %q", calls[0].Signer, testClaimSigner)
	}
	if !strings.Contains(calls[0].Payload, token.UID(f.slug, f.run)) {
		t.Error("the payload handed to the signer does not name the run's uid, so the token it signed is not this run's")
	}

	// The record says who claimed, when, and the DIGEST of the nonce — never the
	// nonce (R2).
	held := f.issuance.record(t, f.slug, f.run)
	if held.Claimed.Account != testJobSA {
		t.Errorf("the record is claimed by %q, want the job's own account %q", held.Claimed.Account, testJobSA)
	}
	if want := identitywire.Digest(testNonce); held.Claimed.NonceHash != want {
		t.Errorf("the record's nonce digest is %q, want the digest of the nonce the runner drew", held.Claimed.NonceHash)
	}
	if !held.Claimed.At.Equal(f.now) {
		t.Errorf("the record is claimed at %s, want %s", held.Claimed.At, f.now)
	}

	// A served claim is not a refusal: no audit line. What an operator reads is
	// the service's own line, which names the run and the repository (a StatusOK
	// audit line needs a purpose, a run and a job, and this act has none of the
	// three).
	if f.audit.Len() != 0 {
		t.Errorf("a served claim wrote an audit line: %s", f.audit.String())
	}
	if line := f.logs.String(); !strings.Contains(line, f.run) || !strings.Contains(line, f.slug) {
		t.Errorf("the service's own line names neither the run nor the repository: %q", line)
	}
}

// TestClaimOfATicketTheTicketRouteMinted holds the contract's two halves to
// each other: the ticket LM1's route answers a launcher is the ticket this route
// claims.
//
// It is the test the ticket's spelling needs, and it is here rather than in
// LM1's file because the claim is the side that cannot work without the names:
// the claim's caller holds no run identity — the claim is a run's first call,
// made before it has one (R2) — so C1's ClaimRequest carries the ticket and the
// nonce and nothing else, and the ticket is the ONLY thing that tells this route
// which record to read. A claim that read a spelling the mint does not write
// would refuse every real ticket, whatever a fixture of its own drew.
func TestClaimOfATicketTheTicketRouteMinted(t *testing.T) {
	f := newClaimFixture(t)
	ask := identitywire.TicketRequest{
		Slug:              f.slug,
		Run:               f.run,
		Account:           testJobSA,
		Launcher:          testLauncherAccount,
		Branch:            "fugaro/" + f.run,
		MaxRuntimeSeconds: int(testAskRuntime / time.Second),
	}
	body, err := json.Marshal(ask)
	if err != nil {
		t.Fatalf("building the ask: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, identitywire.TicketPath, strings.NewReader(string(body)))
	f.server.identityTicket(context.Background(), rec, IdentityCaller{Address: testLauncherAccount, Class: identitywire.ClassPerson}, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("the ticket ask is answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var issued identitywire.TicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatalf("the ticket's answer is not one this contract reads: %v", err)
	}

	// The record the ticket route wrote is the one this claim signs from, and
	// the ticket it answered names its path.
	resp, answer := f.claimCall(t, identityClaimBody(t, issued.Ticket, testNonce))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the claim of the ticket the route minted is answered %d, want 200: %s", resp.StatusCode, answer)
	}
	var claimed identitywire.ClaimResponse
	if err := json.Unmarshal(answer, &claimed); err != nil {
		t.Fatalf("the claim's answer is not one this contract reads: %v", err)
	}
	claims, signer, err := f.iam.Verify(claimed.Token)
	if err != nil {
		t.Fatalf("the token is not one the runs' signer signed: %v", err)
	}
	if signer != testClaimSigner {
		t.Errorf("the token is signed as %q, want the runs' signer %q", signer, testClaimSigner)
	}
	// The uid is derived from the record's PATH, and the path is the one the
	// ticket named: a ticket that named another run's record would have this
	// claim sign that run's identity.
	if got := claims["uid"]; got != token.UID(f.slug, f.run) {
		t.Errorf("the token's uid is %v, want the uid of the run the ticket named (%q)", got, token.UID(f.slug, f.run))
	}
	set, ok := claims["claims"].(map[string]any)
	if !ok {
		t.Fatalf("the token carries no claim set: %v", claims)
	}
	for field, want := range map[string]string{"fs": f.slug, "fr": f.run, "fp": testProject, "rb": testLauncherAccount} {
		if got, _ := set[field].(string); got != want {
			t.Errorf("the token's claim %s is %v, want %q", field, set[field], want)
		}
	}

	// The ticket the route minted is spent on the job's own account, and the
	// record's digest is the digest of the ticket it answered.
	held := f.issuance.record(t, f.slug, f.run)
	if held.Claimed.Account != testJobSA {
		t.Errorf("the record is claimed by %q, want the job's own account %q", held.Claimed.Account, testJobSA)
	}
	if want := identitywire.Digest(issued.Ticket); held.TicketHash != want {
		t.Errorf("the record's ticket digest is not the digest of the ticket the route answered")
	}
}

// TestClaimOfATicketThatNamedTheAccountInAnotherCase is the review of LM2's
// finding held end to end: the ticket route admits the account an ask names
// folded and writes the ask's OWN spelling on the record
// (ticketAccountAdmitted, identity_ticket.go), while the claim's account is the
// CONFIGURED spelling the verifier answers (auth.go's verifyJob) — so the two
// spellings of one account reach the claim's match and it has to see them as
// one. Each half of that fold is a different comparison with its own test: the
// route's admission is the one this test drives (a lookup of the raw account in
// the jobs map, unfolded, would refuse the ask below and answer no ticket), and
// the store's own floor is TestTicketRecordClaimFoldsTheAccount
// (identity_record_test.go). The claim's match here is reached with the record
// still unclaimed, so it is the route's admission that carries the case
// difference; a byte-for-byte comparison in either half would refuse a ticket
// the route itself admitted, which is a run that could never claim its identity.
func TestClaimOfATicketThatNamedTheAccountInAnotherCase(t *testing.T) {
	f := newClaimFixture(t)
	ask := identitywire.TicketRequest{
		Slug:              f.slug,
		Run:               f.run,
		Account:           strings.ToUpper(testJobSA),
		Launcher:          testLauncherAccount,
		Branch:            "fugaro/" + f.run,
		MaxRuntimeSeconds: int(testAskRuntime / time.Second),
	}
	body, err := json.Marshal(ask)
	if err != nil {
		t.Fatalf("building the ask: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, identitywire.TicketPath, strings.NewReader(string(body)))
	f.server.identityTicket(context.Background(), rec, IdentityCaller{Address: testLauncherAccount, Class: identitywire.ClassPerson}, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("the ticket ask is answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var issued identitywire.TicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatalf("the ticket's answer is not one this contract reads: %v", err)
	}

	// The record holds the ASK's spelling and not the configured one, which is
	// the value the claim's match is made against.
	if held := f.issuance.record(t, f.slug, f.run); held.ExpectedAccount != strings.ToUpper(testJobSA) {
		t.Fatalf("the record's expected account is %q, want the ask's own spelling %q", held.ExpectedAccount, strings.ToUpper(testJobSA))
	}

	// The claim's account is the configured spelling the verifier answers, so a
	// byte-for-byte match would refuse this ticket.
	ans := f.claimOK(t, identityClaimBody(t, issued.Ticket, testNonce))
	if ans.Token == "" {
		t.Fatal("the claim answered no token")
	}
	held := f.issuance.record(t, f.slug, f.run)
	if held.Claimed.Account != testJobSA {
		t.Errorf("the record is claimed by %q, want the job's own account %q", held.Claimed.Account, testJobSA)
	}
	if want := identitywire.Digest(testNonce); held.Claimed.NonceHash != want {
		t.Errorf("the record's claim carries the nonce digest %q, want the digest of the nonce the claim carried, %q", held.Claimed.NonceHash, want)
	}
}

// TestClaimIsOnce: R5's fence. A ticket is claimed once, and another claim on
// it — here another nonce after a success — is refused with one audit line that
// names the run, signs nothing, and leaves the first claim on the record. The
// line is the whole of how the loser of a race is detected, so it is asserted
// to carry the run and nothing else.
func TestClaimIsOnce(t *testing.T) {
	f := newClaimFixture(t)
	ticket, _ := f.issued(t)
	f.claimOK(t, identityClaimBody(t, ticket, testNonce))
	first := f.issuance.record(t, f.slug, f.run)

	resp, body := f.claimCall(t, identityClaimBody(t, ticket, testOtherNonce))
	wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

	lines := f.identityLines(t)
	if len(lines) != 1 {
		t.Fatalf("the second claim wrote %d audit lines, want exactly one", len(lines))
	}
	wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)

	if held := f.issuance.record(t, f.slug, f.run); held.Claimed != first.Claimed {
		t.Errorf("the refused claim left %+v on the record, want the first claim %+v", held.Claimed, first.Claimed)
	}
	if calls := f.iam.SignCalls(); len(calls) != 1 {
		t.Errorf("the signer was asked %d times, want the first claim's one: a refused claim signs nothing", len(calls))
	}
}

// TestClaimRetryWithTheSameNonceIsNotASecondClaim: R2's retry. The runner
// retries because the answer was lost, and the retry carries the SAME nonce, so
// it is the same claim answered again rather than a second one: the record's
// claim is not re-marked, the run is answered the same identity, and no audit
// line is written at all.
//
// The second half is the part the store's fence alone cannot show: the clock
// moves, so a store that marked the record again would leave a later instant on
// it, and a service that had refused the retry would answer 403.
func TestClaimRetryWithTheSameNonceIsNotASecondClaim(t *testing.T) {
	f := newClaimFixture(t)
	ticket, _ := f.issued(t)
	first := f.claimOK(t, identityClaimBody(t, ticket, testNonce))
	claimedAt := f.issuance.record(t, f.slug, f.run).Claimed.At

	// The same nonce again, at the same instant: the same answer, byte for byte
	// — the claims are the record's and the signing's clock has not moved.
	again := f.claimOK(t, identityClaimBody(t, ticket, testNonce))
	if again.Token != first.Token {
		t.Error("the same nonce at the same instant answered a different token, and what is signed is the record's own claims")
	}

	// And after a while — inside the ticket's own life (R2) — it is still the
	// same claim.
	f.now = f.now.Add(time.Minute)
	later := f.claimOK(t, identityClaimBody(t, ticket, testNonce))
	if held := f.issuance.record(t, f.slug, f.run); !held.Claimed.At.Equal(claimedAt) {
		t.Errorf("the retry re-marked the record at %s, want the first claim's own instant %s", held.Claimed.At, claimedAt)
	}
	claims, _, err := f.iam.Verify(later.Token)
	if err != nil {
		t.Fatalf("the retry's token is not one the runs' signer signed: %v", err)
	}
	if got := claims["uid"]; got != token.UID(f.slug, f.run) {
		t.Errorf("the retry's token names %v, want this run's uid %q", got, token.UID(f.slug, f.run))
	}

	if f.audit.Len() != 0 {
		t.Errorf("a retried claim wrote an audit line, and a retry is not a second claim: %s", f.audit.String())
	}
}

// TestClaimRevalidatesTheRecord: R3's stated residual, at the claim. The record
// lives where this service's own database admins, the history account and the
// coordinator's writer can write one, so the claim checks it again before it
// signs: the caller's account is one of the installation's job accounts, the
// claims on the record are the ones its PATH names (the uid is derived from the
// path), and the claims pass R4's own checks (token.MintAt validates them).
// Each refusal writes one line and asks for no signature.
func TestClaimRevalidatesTheRecord(t *testing.T) {
	t.Run("the account is in the job tables", func(t *testing.T) {
		f := newClaimFixture(t)
		ticket, _ := f.issued(t)
		// An address no table of this installation holds: it is not a job
		// account at all, so the caller is refused before its claim is read —
		// and before anything was verified there is no caller for a line.
		resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce), f.jobBearer("stranger@example.invalid"))
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalCallerNotAdmitted)
		if f.audit.Len() != 0 {
			t.Errorf("a refusal of a caller this service never verified wrote an audit line: %s", f.audit.String())
		}
		f.wantNothingSigned(t)
		f.wantUnclaimed(t)
	})

	t.Run("the uid comes from the record's path", func(t *testing.T) {
		f := newClaimFixture(t)
		// A record written at this run's path whose claims name ANOTHER run: it
		// is the shape a database admin's own record has, and signing it would
		// sign a token whose uid is one run's and whose claims are another's.
		ticket, _ := f.issued(t, func(rec *identitywire.Record) { rec.Claims.Run = otherRun() })
		resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce))
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

		lines := f.identityLines(t)
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)
		f.wantNothingSigned(t)
		f.wantUnclaimed(t)
	})

	t.Run("the claims pass R4", func(t *testing.T) {
		f := newClaimFixture(t)
		// The claims themselves are what R4 refuses: an fx that has passed. The
		// record's own clock is now, so the ticket's window is not what refuses
		// this call — the signing's own validation is.
		ticket, _ := f.issued(t, func(rec *identitywire.Record) { rec.Claims.FX = f.now.Add(-time.Minute) })
		resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce))
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

		lines := f.identityLines(t)
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)
		// The claims are the record's own failure and not the signer's: nothing
		// was asked of the signer at all (token.MintAt validates before it
		// signs, and the witness is what tells the two failures apart).
		f.wantNothingSigned(t)
		// The fence WAS taken, and that is deliberate: it is taken before the
		// signing so that a claim whose signing failed is retried by the same
		// nonce rather than refused as a second claim — the retry is answered
		// this same refusal while the record stands, and the run halts on it.
		if held := f.issuance.record(t, f.slug, f.run); held.Claimed.NonceHash != identitywire.Digest(testNonce) {
			t.Errorf("the record's claim is %+v, want the fence taken with the nonce's digest before the signing", held.Claimed)
		}
	})
}

// TestClaimChecksTheRecordTheStoreReturns: the record the FENCE answers is the
// record this service signs, and it is held to every check the handler's own
// read was. The fence is one atomic step on the node and not a lock over the
// handler's two calls, and the store answers the record as it stands after the
// call — so a record that moved between the read and the fence (LM1's Put
// replaces one at the same path, R6) is the one a signing off the returned
// record would use, while the fence itself is bound to the path and the claim
// and to nothing else (C1's IssuanceStore.Claim is handed neither the ticket
// nor the account this handler checked).
//
// Each case below is a field of the record the read answered replaced under the
// claim, and each one alone is enough for the signature to be wrong: claims
// naming another run sign an identity this run is not; a ticket the launcher
// issued afterwards signs for a ticket that was killed (and burns the new one);
// another expected account signs a job's identity for the account that was
// meant to be refused; a record past its life signs a ticket that had expired
// by the time the fence answered. Every one is refused with one line and
// nothing signed.
func TestClaimChecksTheRecordTheStoreReturns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		moved func(*testing.T, *claimFixture, *identitywire.Record)
	}{
		{
			name: "the claims name another run",
			moved: func(_ *testing.T, _ *claimFixture, rec *identitywire.Record) {
				rec.Claims.Run = otherRun()
			},
		},
		{
			name: "the launcher issued a later ticket",
			moved: func(t *testing.T, f *claimFixture, rec *identitywire.Record) {
				rec.TicketHash = identitywire.Digest(f.ticket(t, f.slug, f.run))
			},
		},
		{
			name: "the launch named another job account",
			moved: func(_ *testing.T, _ *claimFixture, rec *identitywire.Record) {
				rec.ExpectedAccount = testOtherSA
			},
		},
		{
			name: "the record is past the ticket's life",
			moved: func(_ *testing.T, _ *claimFixture, rec *identitywire.Record) {
				rec.At = rec.At.Add(-2 * ticketLifetime)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClaimFixture(t)
			ticket, _ := f.issued(t)
			f.issuance.moved = func(rec *identitywire.Record) { tc.moved(t, f, rec) }

			resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce))
			wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

			lines := f.identityLines(t)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
			}
			wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)
			f.wantNothingSigned(t)
			// The fence WAS taken before the record was signed, as it is for the
			// claims that fail R4: the run is refused and the ticket is spent,
			// which is the state the run halts on. Refusing this before the
			// fence would need Claim to be handed the ticket and the account it
			// was checked against, which is C1's interface and not this route's.
			if held := f.issuance.record(t, f.slug, f.run); held.Claimed.NonceHash != identitywire.Digest(testNonce) {
				t.Errorf("the record's claim is %+v, want the fence taken before the record was signed", held.Claimed)
			}
		})
	}
}

// TestClaimNeverLogsTheNonce: R2's confinement. The nonce is kept as its digest
// and nowhere else — it is in no log line and no audit line — and neither is
// the ticket, which the record keeps as a digest too. The test drives a served
// claim and the refusals on both sides of the fence, because a line written on
// any of those paths is where a value would land.
func TestClaimNeverLogsTheNonce(t *testing.T) {
	f := newClaimFixture(t)
	ticket, _ := f.issued(t)

	f.claimOK(t, identityClaimBody(t, ticket, testNonce))
	// Another nonce after a success (R5), an unknown ticket (R4), and a ticket
	// presented by an account that is not the record's (R4).
	f.claimCall(t, identityClaimBody(t, ticket, testOtherNonce))
	f.claimCall(t, identityClaimBody(t, f.ticket(t, f.slug, otherRun()), testNonce))
	f.claimCall(t, identityClaimBody(t, ticket, testNonce), f.jobBearer(testOtherSA))

	for _, buf := range []struct {
		name string
		buf  interface{ String() string }
	}{{"the service's log", f.logs}, {"the audit", f.audit}} {
		raw := buf.buf.String()
		for _, secret := range []struct{ name, value string }{
			{"the nonce", testNonce}, {"the second nonce", testOtherNonce}, {"the ticket", ticket},
		} {
			if strings.Contains(raw, secret.value) {
				t.Errorf("%s carries %s, and R2 confines it to the run's own process", buf.name, secret.name)
			}
		}
	}

	// Not vacuous: the digest IS on the record — that is where the nonce goes —
	// and the service's own line does name the run it claimed.
	if held := f.issuance.record(t, f.slug, f.run); held.Claimed.NonceHash != identitywire.Digest(testNonce) {
		t.Error("the record does not hold the digest of the nonce that claimed it, so this test is looking for a value nothing wrote")
	}
	if !strings.Contains(f.logs.String(), f.run) {
		t.Error("the service's own line does not name the run it claimed, so this test would pass on a service that logged nothing")
	}
}

// TestClaimRefusesAnotherJobAccount: R4's account rule. The caller IS one of the
// installation's job accounts — the other repository's — so it is verified and
// its refusal is audited, and it is not the account the launch named, so it is
// refused: a run may not claim another job's identity (F12).
func TestClaimRefusesAnotherJobAccount(t *testing.T) {
	f := newClaimFixture(t)
	ticket, _ := f.issued(t)

	resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce), f.jobBearer(testOtherSA))
	wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

	lines := f.identityLines(t)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
	}
	wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)
	f.wantNothingSigned(t)
	f.wantUnclaimed(t)
}

// TestClaimRefusesAForgedTicket: R5's fence at its narrowest, and the one thing
// the ticket's thirty-two random bytes are for. Everything a caller can know
// about a run is on the record's path — the slug and the run id are not secret,
// and the ticket's own spelling is reproducible from joinIdentityTicket — so the
// only thing that ties a presented ticket to its record is the DIGEST the
// launcher left on it. Here the path, the account and the claims are all right
// and the secret is the caller's own: without that digest check any job account
// of the installation that knows a slug and a run id could claim that run's
// identity, which is the hole R5 states.
//
// The refusal names the run (the record was read, so the two names in the ticket
// are no longer a caller's text), asks for no signature, and leaves the ticket
// unclaimed — a refused claim that marked it would burn the ticket the real run
// is waiting to exchange.
func TestClaimRefusesAForgedTicket(t *testing.T) {
	f := newClaimFixture(t)
	ticket, _ := f.issued(t)

	// The forgery is the issuer's own ticket with the secret swapped: the caller
	// knows how a ticket is spelled and which run it wants, and draws a secret of
	// its own rather than the one the launcher put on the execution.
	parts := strings.Split(ticket, identityTicketSeparator)
	if len(parts) != 3 {
		t.Fatalf("the issuer's ticket is not the three parts this test forges: %q", ticket)
	}
	forged := strings.Join([]string{parts[0], parts[1], "a-ticket-secret-this-caller-made-up"}, identityTicketSeparator)
	if forged == ticket {
		t.Fatal("the forged ticket is the issuer's own, so this test proves nothing")
	}

	resp, body := f.claimCall(t, identityClaimBody(t, forged, testNonce))
	wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

	lines := f.identityLines(t)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
	}
	wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)
	f.wantNothingSigned(t)
	f.wantUnclaimed(t)
}

// TestClaimRefusesANonceOutOfShape: R2's retry rule needs a nonce to recognise,
// so a claim that carries none at all — and one too long to be a value a runner
// drew (maxIdentityNonceBytes) — is refused as a request this service does not
// read, with one line. The refusal is answered BEFORE the ticket is read, so the
// line names no run: the two names in the ticket are still the caller's text.
//
// Both halves are the guard's own: without it an empty nonce is hashed like any
// other, and the claim is served under a nonce no runner ever drew.
func TestClaimRefusesANonceOutOfShape(t *testing.T) {
	for _, call := range []struct {
		name  string
		nonce string
	}{
		{"no nonce at all", ""},
		{"a nonce longer than a runner draws", strings.Repeat("n", maxIdentityNonceBytes+1)},
	} {
		t.Run(call.name, func(t *testing.T) {
			f := newClaimFixture(t)
			ticket, _ := f.issued(t)

			resp, body := f.claimCall(t, identityClaimBody(t, ticket, call.nonce))
			wantRefusal(t, resp, body, StatusRequestRefused, identitywire.RefusalIdentityRefused)

			lines := f.identityLines(t)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
			}
			wantIdentityLine(t, lines[0], "", "", ErrorRequestRefused)
			f.wantNothingSigned(t)
			f.wantUnclaimed(t)
		})
	}
}

// TestClaimRefusesExpiredTicket: R2's window, which is the ticket's own and
// shorter than the run's identity. A ticket past it is refused however right it
// is — and so is a retry of a claim, because the same-nonce answer holds only
// for the ticket's life.
func TestClaimRefusesExpiredTicket(t *testing.T) {
	t.Run("a ticket past its life", func(t *testing.T) {
		f := newClaimFixture(t)
		// Written long ago and otherwise perfect: the claims' own fx is still in
		// the future, so the ticket's window is the only thing that refuses this.
		ticket, _ := f.issued(t, func(rec *identitywire.Record) { rec.At = f.now.Add(-2 * ticketLifetime) })
		resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce))
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

		lines := f.identityLines(t)
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)
		f.wantNothingSigned(t)
		f.wantUnclaimed(t)
	})

	t.Run("a retry after the life", func(t *testing.T) {
		f := newClaimFixture(t)
		ticket, _ := f.issued(t)
		f.claimOK(t, identityClaimBody(t, ticket, testNonce))
		// The same nonce, past the ticket's life: the retry R2 answers again is
		// the retry inside that window, and this one is a claim the launch has
		// moved past.
		f.now = f.now.Add(ticketLifetime + time.Minute)
		resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce))
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)

		lines := f.identityLines(t)
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		wantIdentityLine(t, lines[0], f.run, f.slug, ErrorUnverifiedRunIdentity)
	})
}

// TestClaimAnsweredAheadOfRunIdentity: the claim is the run's FIRST call, so it
// is answered with no run identity at all — a served call above proves that —
// and the front door answers these two paths before it reads the two identities
// (R2). The rest of the test is the other half of the same fact: a call that
// DOES present one is refused, because the run a claim is for is the ticket's
// and never a caller's to name.
func TestClaimAnsweredAheadOfRunIdentity(t *testing.T) {
	f := newClaimFixture(t)
	ticket, _ := f.issued(t)

	// No run identity header and no job name: this is the call a run makes
	// before it has an identity, and it is served.
	f.claimOK(t, identityClaimBody(t, ticket, testNonce))

	for _, path := range []string{identitywire.ClaimPath, identitywire.ProbePath} {
		if !aheadOfTheSharedParse(httptest.NewRequest(http.MethodPost, path, nil)) {
			t.Errorf("aheadOfTheSharedParse does not name %q, and RequestFromHTTP would refuse its caller for the identity headers it never had", path)
		}
	}

	// A call that names a run, or a job, is a call of a shape this route does
	// not have: it would have to be ignored, which is how a caller's text
	// becomes a run's identity.
	g := newClaimFixture(t)
	other, _ := g.issued(t)
	for i, header := range []string{HeaderRunIdentity, HeaderRunJob} {
		resp, body := g.claimCall(t, identityClaimBody(t, other, testNonce), func(r *http.Request) {
			r.Header.Set(header, g.runToken())
		})
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalIdentityRefused)
		lines := g.identityLines(t)
		if len(lines) != i+1 {
			t.Fatalf("the refusal of a call naming %s left %d audit lines, want %d — one per refusal and no more", header, len(lines), i+1)
		}
		wantIdentityLine(t, lines[i], "", "", ErrorUnverifiedRunIdentity)
	}
}

// TestClaimOnAServiceWithNoIssuerAnswersNotServed: R7's bridge, on both routes.
// An installation whose identity half is not configured answers the contract's
// own code with 503 — deliberately not the 404 a path this service does not
// know is answered with, because the CLI keys its local-signing bridge on those
// two being told apart — and writes no audit line: no caller's identity was
// verified, since there is nothing to verify one against.
func TestClaimOnAServiceWithNoIssuerAnswersNotServed(t *testing.T) {
	s := newSvc(t)
	for _, call := range []struct {
		name string
		path string
		h    func(context.Context, http.ResponseWriter, *http.Request)
	}{
		{"the claim", identitywire.ClaimPath, s.server.identityClaim},
		{"the probe", identitywire.ProbePath, s.server.identityProbe},
	} {
		req := httptest.NewRequest(http.MethodPost, call.path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", contentTypeJSON)
		req.Header.Set("Authorization", "Bearer "+s.jobToken())
		rec := httptest.NewRecorder()
		call.h(context.Background(), rec, req)

		if rec.Code != StatusUnavailable {
			t.Fatalf("%s on a service with no issuer is answered %d, want %d: %s", call.name, rec.Code, StatusUnavailable, rec.Body.String())
		}
		e := refusal(t, rec.Code, rec.Body.Bytes())
		if e.Error != identitywire.RefusalNotServed {
			t.Errorf("%s is refused %q, want the contract's own %q", call.name, e.Error, identitywire.RefusalNotServed)
		}
		if !strings.Contains(e.Message, "fugaro update") {
			t.Errorf("%s's refusal does not name the fix: %q", call.name, e.Message)
		}
	}
	if s.audit.Len() != 0 {
		t.Errorf("a service with no issuer wrote an audit line, and it verified no caller: %s", s.audit.String())
	}
}

// TestClaimWithNoStoreRefusesOnlyAVerifiedCaller is the audit rule's floor on
// the wiring-fault path. A service with a signer and no issuance store refuses
// every claim as its own failure — the store is not one that answers "no
// record" — but the refusal's audit line belongs to a VERIFIED caller, so the
// store check sits after the bearer and the job's own token were read
// (identityClaim). Run before them, as the review of LM2 found, an
// unauthenticated caller was answered through refuseIdentityCall and wrote a
// line naming an act by a caller this service never admitted.
func TestClaimWithNoStoreRefusesOnlyAVerifiedCaller(t *testing.T) {
	f := newClaimFixture(t)
	f.server.cfg.Identity.Store = nil
	ticket := f.ticket(t, f.slug, f.run)

	// No bearer at all: refused as the caller's own, with no line, and the store
	// is never reached.
	resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce), func(r *http.Request) {
		r.Header.Del("Authorization")
	})
	wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalCallerNotAdmitted)
	if f.audit.Len() != 0 {
		t.Errorf("an unauthenticated claim on a store-less service wrote an audit line, and it verified no caller: %s", f.audit.String())
	}

	// The job's own verified account: the missing store is still the service's
	// own failure, and the verified caller's refusal owes the one line the rule
	// gives it — with no run and no repository, because no record was read.
	resp, body = f.claimCall(t, identityClaimBody(t, ticket, testNonce))
	wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
	lines := f.identityLines(t)
	if len(lines) != 1 {
		t.Fatalf("the verified caller's refusal wrote %d audit lines, want exactly one", len(lines))
	}
	wantIdentityLine(t, lines[0], "", "", ErrorStateUnavailable)
	f.wantNothingSigned(t)
}

// TestClaimStoreFailureCarriesNothingTheCallerTyped: the store's own failure
// (R16's ScopeFault rule) is on the operator's line as the cause's Go type and
// never as its text. The error a read fails with is an *identitywire.StoreError,
// whose message spells the path it read — creds/issuance/<slug>/<run> — and both
// halves of that path are what the caller typed into the ticket, so a line that
// passed err itself would put two values the caller chose back on the operator's
// log before any record at that path had been read. That is the rule this
// package keeps for every store failure (refusallog.go).
func TestClaimStoreFailureCarriesNothingTheCallerTyped(t *testing.T) {
	f := newClaimFixture(t)
	ticket := f.ticket(t, f.slug, f.run)
	f.issuance.getErr = &identitywire.StoreError{
		Path: "creds/issuance/" + f.slug + "/" + f.run,
		Err:  errors.New("the node could not be read"),
	}

	resp, body := f.claimCall(t, identityClaimBody(t, ticket, testNonce))
	wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
	lines := f.identityLines(t)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
	}
	wantIdentityLine(t, lines[0], "", "", ErrorStateUnavailable)
	f.wantNothingSigned(t)

	logged := f.logs.String()
	if !strings.Contains(logged, "error_class") {
		t.Errorf("the service's line does not carry the cause by its Go type, so an operator has nothing to read it by: %s", logged)
	}
	if strings.Contains(logged, f.slug) || strings.Contains(logged, f.run) {
		t.Errorf("the service's line hands the operator the names the caller typed into the ticket: %s", logged)
	}
}

// ---------------------------------------------------------------------------
// The probe
// ---------------------------------------------------------------------------

// TestProbeSignsAndDiscards: R8 and F33. `update` calls the probe before it
// moves the people's minter row to the service, so an answer has to mean the
// row and the account are right — and it must be nothing an attacker can use:
// the service signs the contract's own fixed payload as the runs' signer and
// discards the signature, and the answer carries the signer's name and when it
// signed and no signature bytes and no digest of them.
func TestProbeSignsAndDiscards(t *testing.T) {
	t.Run("an operator is answered the signer's name and no signature", func(t *testing.T) {
		f := newClaimFixture(t, testLauncherAccount)
		resp, body := f.probeCall(t, mustBody(t, identitywire.ProbeRequest{}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the probe is answered %d, want 200: %s", resp.StatusCode, body)
		}
		// The answer is the contract's own response and nothing else, which is
		// what "no signature bytes and no digest of them" comes to on the wire.
		if want := mustBody(t, identitywire.ProbeResponse{Signer: testClaimSigner, SignedAt: f.now}); string(body) != want {
			t.Errorf("the probe's answer is %s, want the contract's own %s", body, want)
		}

		calls := f.iam.SignCalls()
		if len(calls) != 1 {
			t.Fatalf("the probe signed %d times, want once", len(calls))
		}
		if calls[0].Signer != testClaimSigner {
			t.Errorf("the probe signed as %q, want the runs' signer %q", calls[0].Signer, testClaimSigner)
		}
		// The payload is the contract's fixed marker, and it is a JSON object
		// because signJwt takes nothing else — a bare string would make every
		// probe answer 503 (identityProbePayload). No part of the call reaches
		// it: there is no field of the request a caller could put one in.
		var signed map[string]any
		if err := json.Unmarshal([]byte(calls[0].Payload), &signed); err != nil {
			t.Fatalf("the payload the signer was handed is not a JSON object, which is what signJwt takes: %v", err)
		}
		if signed["iss"] != identitywire.ProbePayload || signed["sub"] != identitywire.ProbePayload {
			t.Errorf("the signed payload is %v, want the contract's own fixed marker %q as its iss and sub", signed, identitywire.ProbePayload)
		}

		// A served probe is not a refusal, and what an operator reads is the
		// service's own line.
		if f.audit.Len() != 0 {
			t.Errorf("a served probe wrote an audit line: %s", f.audit.String())
		}
		if line := f.logs.String(); !strings.Contains(line, testClaimSigner) {
			t.Errorf("the service's own line does not name the signer the probe proved: %q", line)
		}
	})

	t.Run("a launcher of the roster that is not an operator is refused with one line", func(t *testing.T) {
		// The roster is empty here, so the launcher is a person this service
		// verified and not one of its operators: the refusal is audited, and it
		// names no run — there is none, and the signer was never reached.
		f := newClaimFixture(t)
		resp, body := f.probeCall(t, mustBody(t, identitywire.ProbeRequest{}))
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalCallerNotAdmitted)

		lines := f.identityLines(t)
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		wantIdentityLine(t, lines[0], "", "", ErrorUnverifiedRunIdentity)
		if calls := f.iam.SignCalls(); len(calls) != 0 {
			t.Errorf("the signer was asked %d times, want none: the probe refuses before it signs", len(calls))
		}
	})

	t.Run("a caller of no roster is refused before anything was verified", func(t *testing.T) {
		f := newClaimFixture(t, testLauncherAccount)
		// Another account's Google ID token: the launcher class refuses it, so
		// nothing was verified and there is no caller for a line to name.
		resp, body := f.probeCall(t, mustBody(t, identitywire.ProbeRequest{}), func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+f.personToken("stranger@example.invalid"))
		})
		wantRefusal(t, resp, body, StatusUnverified, identitywire.RefusalCallerNotAdmitted)
		if f.audit.Len() != 0 {
			t.Errorf("a refusal of a caller this service never verified wrote an audit line: %s", f.audit.String())
		}
		if calls := f.iam.SignCalls(); len(calls) != 0 {
			t.Errorf("the signer was asked %d times, want none", len(calls))
		}
	})

	t.Run("a body that names a field is refused", func(t *testing.T) {
		// The probe's request has no field at all (R8): a caller that names one
		// — a payload among them — is refused rather than served, which is what
		// keeps the route from being a signing oracle for a caller's bytes.
		f := newClaimFixture(t, testLauncherAccount)
		resp, body := f.probeCall(t, `{"payload":"a-callers-own-bytes"}`)
		wantRefusal(t, resp, body, StatusRequestRefused, identitywire.RefusalIdentityRefused)

		lines := f.identityLines(t)
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		wantIdentityLine(t, lines[0], "", "", ErrorRequestRefused)
		if calls := f.iam.SignCalls(); len(calls) != 0 {
			t.Errorf("the signer was asked %d times, want none: the body was refused before the signer was built", len(calls))
		}
	})
}
