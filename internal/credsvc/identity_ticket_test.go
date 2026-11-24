package credsvc

// LM1's tests: the ticket route (identity_ticket.go) and the checks of R4 it
// makes before it writes anything.
//
// What each test is for, in one line: a verified caller gets its run's ticket
// and the record says so; another caller's ask for the same run is refused
// (R5); the same caller's re-ask before a claim replaces the ticket (R6); a
// claimed record and a run that has ended are refused; a repository this
// installation does not serve and an account that is not its job's are
// refused; the expiry is bounded by the installation's own maximum and refused
// and not clamped; and every refusal of a VERIFIED caller writes exactly one
// audit line — no more, no fewer.
//
// The handler is called the way the mount (I1) will call it, with the caller
// the mount has already verified: verifying a Google ID token is LM3's act and
// its own test, and what is under test here is everything that happens after
// it. The route's own body is read by the handler (readCall), so the calls
// below carry a body and no bearer, exactly as the mount's will.
//
// Everything is hermetic (Convention 9): the record goes to internal/gcpfake's
// Realtime Database through the real store, and the run's outcome is read from
// the same fake. Every value that stands for a secret is drawn at run time
// (Convention 2) — the ticket under test is one this service drew, read out of
// the answer it made.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

const (
	// testIssuer is the account an installation's identities are signed as
	// (IdentityConfig.Signer). It is a name and not a key: the issuer's own
	// credentials are LM2's and the deployment's.
	testIssuer = "fugaro-issuer@aurora-dev.iam.gserviceaccount.com"
	// testOtherLauncher is a SECOND address on the fixture's launcher roster,
	// which is what makes TestTicketRefusedForAnotherCallersRun a test of the
	// record's caller rather than of the roster: both addresses may ask, and
	// only one of them owns the run.
	testOtherLauncher = "launcher-2@example.invalid"
	// The installation's other job accounts are the package's own fixtures and
	// not respelled here: testSharedSA with testSharedProfile and testSharedRepo
	// is a shared project job's (auth_test.go), testReviewSA is a review job's
	// (review_test.go), and testOrchestratorSA is the resident orchestrator's
	// (mergemint_test.go).
	// testAskRuntime is the runtime the asks in this file name, and testMaxRuntime
	// the maximum the fixture's installation issues (IdentityConfig.MaxRuntime).
	// One is inside the other with room to state both ends.
	testAskRuntime = time.Hour
	testMaxRuntime = 24 * time.Hour
)

// tickets is the fixture: the service with its identity half wired the way a
// deployment wires it (I1), over a fake database the records are written to and
// the run's outcome is read from.
type tickets struct {
	*svc
	// db is the raw tree, which is where a test reads what a call left: the
	// store's own readers would hide a record written under a node nothing
	// serves.
	db    *gcpfake.RTDB
	store IssuanceStore
	scope *fakeTicketScope
}

// newTickets is the fixture. The configuration it sets is the deployment's
// three parts and nothing else: the database client (the same *rtdb.Client the
// records are written with, which is what makes installationScope.Outcomes
// answer at all — it asks the configuration for a Getter and answers nil for
// anything else), the issuer, and the store and scope check.
func newTickets(t *testing.T, mutate ...func(*svc, *Config)) *tickets {
	t.Helper()
	client, db, store := ticketStore(t)
	scope := &fakeTicketScope{}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		cfg.DB = client
		// The installation's own repository table, spelled out: a deployment
		// with shared or review job accounts must carry one (NewServer refuses
		// it otherwise, because a shared call is paired with a run through it),
		// and the derived table is the same one.
		cfg.Repos = map[string]string{s.slug: testRepo, testSlug(t, testOtherRepoPath): testOtherRepo}
		// The sibling account is bound to the fixture's own repository and runs
		// as a job of its own, so an ask that names it is refused by the
		// ACCOUNT check and not by the repository table — which is what
		// TestTicketRefusesUnknownRepositoryAndForeignAccount turns on.
		cfg.JobAccounts = append(cfg.JobAccounts, JobAccount{Email: testSiblingSA, Slug: s.slug, Repo: testRepo})
		cfg.Identity = IdentityConfig{
			Signer:     testIssuer,
			MaxRuntime: testMaxRuntime,
			Store:      store,
			Scope:      scope,
		}
		for _, m := range mutate {
			m(s, cfg)
		}
	})
	return &tickets{svc: s, db: db, store: store, scope: scope}
}

// fakeTicketScope is the launch-scope check as a test drives it: it admits
// every ask (the real one is LM23's and LM24's), and a test either refuses with
// an error of its own or faults. Both lists are kept so a test can state that
// the check was asked ONCE, with the caller and the whole ask.
type fakeTicketScope struct {
	refuse error
	fault  error
	asks   []identitywire.TicketRequest
	seen   []IdentityCaller
}

func (f *fakeTicketScope) Check(_ context.Context, caller IdentityCaller, ask identitywire.TicketRequest) error {
	f.asks = append(f.asks, ask)
	f.seen = append(f.seen, caller)
	if f.fault != nil {
		return f.fault
	}
	return f.refuse
}

// person is a caller the mount verified as a roster person, and jobAccount one
// verified as a run's job (which the ticket route does not admit).
func (f *tickets) person(addr string) IdentityCaller {
	return IdentityCaller{Address: addr, Class: identitywire.ClassPerson}
}

// goodAsk is the ask a launcher of this installation makes: the fixture's own
// repository and run, its job's account, the launcher's own name, a branch, and
// a runtime inside the installation's maximum.
func (f *tickets) goodAsk() identitywire.TicketRequest {
	return identitywire.TicketRequest{
		Slug:              f.slug,
		Run:               f.run,
		Account:           testJobSA,
		Launcher:          testLauncherAccount,
		Branch:            "fugaro/" + f.run,
		MaxRuntimeSeconds: int(testAskRuntime / time.Second),
	}
}

// ask calls the handler with one ask, and raw with a body the test wrote itself
// (a body that is not this contract, which is the only way to send one).
func (f *tickets) ask(t *testing.T, caller IdentityCaller, ask identitywire.TicketRequest) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	body, err := json.Marshal(ask)
	if err != nil {
		t.Fatalf("building the ask: %v", err)
	}
	return f.raw(t, caller, string(body))
}

func (f *tickets) raw(t *testing.T, caller IdentityCaller, body string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, identitywire.TicketPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.server.identityTicket(context.Background(), rec, caller, r)
	return rec, rec.Body.Bytes()
}

// issued reads one answer as the ticket it is, checking the shape a credential
// has to have before the tests below build on it.
func (f *tickets) issued(t *testing.T, rec *httptest.ResponseRecorder, body []byte) identitywire.TicketResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("the ask is answered %d, want 200: %s", rec.Code, body)
	}
	var ans identitywire.TicketResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the answer is not one this contract can read: %v (%s)", err, body)
	}
	// The ticket is the record's own two names and the secret, in the one
	// spelling the mint and the claim share (joinIdentityTicket and its inverse,
	// identity_claim.go): the claim's caller holds no run identity, so the ticket
	// is the only thing that tells it which record to read, and a mint that
	// spelled it another way would be a ticket no run could claim. That the two
	// names are the ask's own is what the claim's tests state, by claiming a
	// ticket this route minted (identity_claim_test.go).
	parts := strings.Split(ans.Ticket, identityTicketSeparator)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		t.Fatalf("the ticket is %q, want the record's slug and run and a secret", ans.Ticket)
	}
	if len(parts[2]) != ticketBytes*2 {
		t.Fatalf("the ticket's secret is %d characters, want %d", len(parts[2]), ticketBytes*2)
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		t.Fatalf("the ticket's secret is not hex: %v", err)
	}
	if !ans.ExpiresAt.Equal(f.now.Add(ticketLifetime)) {
		t.Errorf("the ticket expires at %s, want the lifetime every ticket of this release has (%s)", ans.ExpiresAt, f.now.Add(ticketLifetime))
	}
	return ans
}

// refused checks one refusal whole: the status, the contract's code, the audit
// status it was recorded under, the caller on the line, and that there is
// exactly one line.
func (f *tickets) refused(t *testing.T, rec *httptest.ResponseRecorder, body []byte, caller string, wantStatus int, wantCode, wantAudit string) WireError {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("the ask is answered %d, want %d: %s", rec.Code, wantStatus, body)
	}
	e := refusal(t, rec.Code, body)
	if e.Error != wantCode {
		t.Errorf("the refusal is %q, want the contract's %q: %s", e.Error, wantCode, e.Message)
	}
	if got := e.Message; !strings.HasPrefix(got, requestRefused) && wantStatus == StatusRequestRefused {
		t.Errorf("the refusal reads %q, want the service's own prefix %q first", got, requestRefused)
	}
	line := f.oneLine(t, caller)
	if got := line["status"]; got != wantAudit {
		t.Errorf("the refusal is recorded as %v, want %q", got, wantAudit)
	}
	if got := line["purpose"]; got != PurposeSlipGrant {
		t.Errorf("the line carries purpose %v, want %q: the act is the launcher's ask for a run it has not started", got, PurposeSlipGrant)
	}
	return e
}

// oneLine reads the ONE audit line a refusal owes, with the caller on it.
func (f *tickets) oneLine(t *testing.T, caller string) map[string]any {
	t.Helper()
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	if got := lines[0]["caller"]; got != caller {
		t.Errorf("the line names caller %v, want the verified caller %q: it is the one fact this line carries", got, caller)
	}
	for _, field := range []string{"run", "repository", "job"} {
		if got := lines[0][field]; got != "" {
			t.Errorf("the line carries %s %v, want none: the run and the repository are the ask's own bytes and not values this service verified (a grant's refusal line names no run either)", field, got)
		}
	}
	return lines[0]
}

// noLines states that a call wrote no audit line at all: an issue is not
// audited (a StatusOK line requires a credential, and this route issues none)
// and neither is a refusal made before the caller was verified.
func (f *tickets) noLines(t *testing.T) {
	t.Helper()
	if got := f.audit.String(); got != "" {
		t.Errorf("the call wrote an audit line, want none: %s", got)
	}
}

// record reads one record back through the store the service wrote it with.
func (f *tickets) record(t *testing.T, slug, run string) identitywire.Record {
	t.Helper()
	rec, found, err := f.store.Get(context.Background(), slug, run)
	if err != nil {
		t.Fatalf("reading the record back: %v", err)
	}
	if !found {
		t.Fatalf("the run %q has no issuance record", run)
	}
	return rec
}

// nothingWritten states that no record was written anywhere: a refused ask
// leaves the node exactly as it was, and a call that never reached the store
// leaves no node at all.
func (f *tickets) nothingWritten(t *testing.T) {
	t.Helper()
	if got := f.db.Value("creds/issuance"); got != nil {
		t.Errorf("the refused ask wrote a record: %v", got)
	}
}

// TestTicketIssuedToRosterCaller is LM1's headline: a verified caller gets a
// ticket for the slug and run it asked for, the whole of R4's checks having
// passed, and the record is written once with what the service will sign at the
// claim.
//
// It also states the three things the answer must never be: cached, logged, and
// stored. The ticket is a credential — the run exchanges it, with its job's own
// identity token, for an identity of that run (R2) — so it appears in the
// answer and in nothing else: not on the service's own line, not on an audit
// line, and not in the database, which keeps its digest.
func TestTicketIssuedToRosterCaller(t *testing.T) {
	f := newTickets(t)
	ask := f.goodAsk()
	rec, body := f.ask(t, f.person(testLauncherAccount), ask)
	ans := f.issued(t, rec, body)

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("the answer is cacheable with %q, want no-store: its body carries the ticket", got)
	}
	got := f.record(t, f.slug, f.run)
	if got.Caller != testLauncherAccount || got.Class != identitywire.ClassPerson {
		t.Errorf("the record names caller %q class %q, want the verified caller %q as a %q", got.Caller, got.Class, testLauncherAccount, identitywire.ClassPerson)
	}
	if !got.At.Equal(f.now) {
		t.Errorf("the record is stamped %s, want this service's now (%s)", got.At, f.now)
	}
	if got.ExpectedAccount != testJobSA {
		t.Errorf("the record expects account %q, want the ask's own %q: the claim is what it fences", got.ExpectedAccount, testJobSA)
	}
	if got.TicketHash != identitywire.Digest(ans.Ticket) {
		t.Errorf("the record keeps %q, want the digest of the ticket it answered: the ticket itself is stored nowhere", got.TicketHash)
	}
	if got.Claimed.Account != "" {
		t.Errorf("the record is already claimed by %q, want an unclaimed ticket", got.Claimed.Account)
	}
	if got.Attempt != "" {
		t.Errorf("the record carries attempt %q, want none: no class but the orchestrator has one to name", got.Attempt)
	}
	claims := got.Claims
	if claims.Slug != budget.Key(ask.Slug) || claims.Run != budget.Key(ask.Run) {
		t.Errorf("the claims name %q/%q, want the ask's own slug and run", claims.Slug, claims.Run)
	}
	if claims.FP != testProject {
		t.Errorf("the claims carry project %q, want this installation's own %q: the launcher does not name it, the service derives it", claims.FP, testProject)
	}
	if claims.RB != testLauncherAccount || claims.Branch != ask.Branch {
		t.Errorf("the claims carry rb %q branch %q, want the ask's own", claims.RB, claims.Branch)
	}
	if want := token.ExpiryFX(f.now, testAskRuntime); !claims.FX.Equal(want) {
		t.Errorf("the claims expire at %s, want %s: the identity is bounded by the runtime the ask named", claims.FX, want)
	}

	// The scope check was asked once, with the verified caller and the ask it
	// was made for: the check's other side is LM23's and LM24's, and what this
	// route owes it is the whole ask.
	if len(f.scope.asks) != 1 {
		t.Fatalf("the launch scope was asked %d times, want exactly 1", len(f.scope.asks))
	}
	if len(f.scope.seen) != 1 || f.scope.seen[0].Address != testLauncherAccount {
		t.Errorf("the scope check saw %+v, want the verified caller", f.scope.seen)
	}
	if f.scope.asks[0].Run != ask.Run || f.scope.asks[0].Account != ask.Account || f.scope.asks[0].Attempt != ask.Attempt {
		t.Errorf("the scope check saw %+v, want the ask whole", f.scope.asks[0])
	}

	f.noLines(t)
	node, _ := json.Marshal(f.db.Value(identitywire.PathRecord(f.slug, f.run)))
	if strings.Contains(string(node), ans.Ticket) {
		t.Errorf("the ticket is written to the database: %s", node)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, "ticket was issued") || !strings.Contains(logs, ask.Run) {
		t.Errorf("the service's own line does not name the issue: %s", logs)
	}
	if strings.Contains(logs, ans.Ticket) {
		t.Errorf("a log line carries the ticket: %s", logs)
	}
}

// TestTicketRefusedForAnotherCallersRun is the red step's subject and R5: the
// ticket of a run is re-issued to the caller on the record and to nobody else,
// whatever a second verified caller asks. The refusal tells it what to do — a
// new attempt takes a new run id — and the record is left exactly as it was, so
// the owner's ticket is still the live one.
func TestTicketRefusedForAnotherCallersRun(t *testing.T) {
	f := newTickets(t)
	firstRec, firstBody := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
	first := f.issued(t, firstRec, firstBody)

	rec, body := f.ask(t, f.person(testOtherLauncher), f.goodAsk())
	e := f.refused(t, rec, body, testOtherLauncher, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
	if !strings.Contains(e.Message, "run id") {
		t.Errorf("the refusal does not name what to do about it: %s", e.Message)
	}
	got := f.record(t, f.slug, f.run)
	if got.Caller != testLauncherAccount || got.TicketHash != identitywire.Digest(first.Ticket) {
		t.Errorf("the refused ask changed the record: caller %q ticket %q", got.Caller, got.TicketHash)
	}
}

// TestTicketReplacedBeforeClaim is R6: before the ticket is claimed, the same
// caller asking again gets a NEW ticket and the old one dies — it dies because
// the node now holds the digest of the new one and nothing else remembers a
// ticket, so there is nothing to unwind. Both asks were served, so neither
// wrote an audit line.
func TestTicketReplacedBeforeClaim(t *testing.T) {
	f := newTickets(t)
	firstRec, firstBody := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
	first := f.issued(t, firstRec, firstBody)
	rec, secondBody := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
	second := f.issued(t, rec, secondBody)

	if first.Ticket == second.Ticket {
		t.Fatalf("the re-ask answered the same ticket: R6 is a replacement, and a ticket kept alive beside the new one is a second way in")
	}
	got := f.record(t, f.slug, f.run)
	if got.TicketHash != identitywire.Digest(second.Ticket) {
		t.Errorf("the record keeps %q, want the digest of the re-issued ticket: the first ticket must be dead", got.TicketHash)
	}
	if got.TicketHash == identitywire.Digest(first.Ticket) {
		t.Errorf("the record still holds the first ticket's digest")
	}
	f.noLines(t)
}

// TestTicketRefusedAfterClaimOrOutcome is R4's last two checks on the record:
// a claimed ticket is never re-issued (the identity was signed from that
// record), and a run that has already written its outcome is over.
func TestTicketRefusedAfterClaimOrOutcome(t *testing.T) {
	t.Run("a claimed ticket", func(t *testing.T) {
		f := newTickets(t)
		firstRec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		ans := f.issued(t, firstRec, body)
		claim := identitywire.Claim{Account: testJobSA, At: f.now, NonceHash: identitywire.Digest("the-nonce-the-run-drew")}
		if _, err := f.store.Claim(context.Background(), f.slug, f.run, claim); err != nil {
			t.Fatalf("claiming the record: %v", err)
		}

		rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		if !strings.Contains(e.Message, "claimed") {
			t.Errorf("the refusal does not say the ticket was claimed (nor that a new attempt takes a new run id): %s", e.Message)
		}
		got := f.record(t, f.slug, f.run)
		if got.TicketHash != identitywire.Digest(ans.Ticket) || got.Claimed.Account != testJobSA {
			t.Errorf("the refused ask changed the claimed record: %+v", got)
		}
	})

	t.Run("a run that has ended", func(t *testing.T) {
		f := newTickets(t)
		run := otherRun()
		// The outcome is written under the database's own day, which is the
		// day the scan reads (budget.Day of this service's clock here, because
		// the fake's is this fixture's).
		f.db.Set(budget.PathOutcome(budget.Day(f.now), f.slug, run), map[string]any{"status": "done"})
		ask := f.goodAsk()
		ask.Run = run

		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		if !strings.Contains(e.Message, "over") {
			t.Errorf("the refusal does not say the run is over: %s", e.Message)
		}
		if got := f.db.Value(identitywire.PathRecord(f.slug, run)); got != nil {
			t.Errorf("a ticket was written for a run that has ended: %v", got)
		}
	})
}

// TestTicketRefusesUnknownRepositoryAndForeignAccount is R4's second and fourth
// checks: the repository must be one this installation has a job for, and the
// account must be a job account its runs go out on.
//
// The unknown repository is answered the launch-scope refusal, which is the
// reading this task took of R4's sentence: "this installation serves no
// repository of that slug" and "you hold no launch action on it" are one thing
// for a caller — it gets no ticket — and what it does about either is the same.
func TestTicketRefusesUnknownRepositoryAndForeignAccount(t *testing.T) {
	t.Run("a repository this installation does not serve", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.Slug = "acme-nowhere"
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		f.refused(t, rec, body, testLauncherAccount, StatusUnverified, identitywire.RefusalNoLaunchScope, ReasonNotAdmitted)
		if len(f.scope.asks) != 0 {
			t.Errorf("the launch scope was asked about a repository this installation does not serve: the table is refused before the resolver")
		}
		f.nothingWritten(t)
	})

	t.Run("an account that is not this repository's job", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.Account = testOtherSA
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		if !strings.Contains(e.Message, "account") {
			t.Errorf("the refusal does not name the account: %s", e.Message)
		}
		f.nothingWritten(t)
	})
}

// TestTicketExpiryBounded is R4's expiry check: an ask for more than the
// installation's configured maximum is REFUSED, never clamped, because a
// shorter identity than the run needs is a run that halts in the middle of its
// work — worse than a launch that fails where the person is still standing. An
// installation that configured no maximum issues nothing at all, which is the
// fail-closed reading of a value nobody set.
func TestTicketExpiryBounded(t *testing.T) {
	t.Run("over the maximum", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.MaxRuntimeSeconds = int(testMaxRuntime/time.Second) + 1
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		if !strings.Contains(e.Message, strconv.Itoa(int(testMaxRuntime/time.Second))) {
			t.Errorf("the refusal does not name the maximum this installation issues: %s", e.Message)
		}
		f.nothingWritten(t)
	})

	t.Run("exactly the maximum", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.MaxRuntimeSeconds = int(testMaxRuntime / time.Second)
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		f.issued(t, rec, body)
		got := f.record(t, f.slug, f.run)
		if want := token.ExpiryFX(f.now, testMaxRuntime); !got.Claims.FX.Equal(want) {
			t.Errorf("the claims expire at %s, want %s: the bound includes its own end", got.Claims.FX, want)
		}
	})

	t.Run("no runtime at all", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.MaxRuntimeSeconds = 0
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		f.nothingWritten(t)
	})

	t.Run("an installation with no maximum", func(t *testing.T) {
		f := newTickets(t, func(_ *svc, cfg *Config) { cfg.Identity.MaxRuntime = 0 })
		rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		f.nothingWritten(t)
	})
}

// TestTicketRefusesTheClassTheShapeAndTheFields is everything the route refuses
// before it reads anything of the installation: a class the route does not
// admit, an ask that names no repository or no run, an attempt no class but the
// orchestrator may name, and — the contract's own point — a body naming a field
// this wire does not have. A uid or a claim set in an ask must be refused BY
// NAME, because a wire that took one would be a caller choosing its own
// identity (R2).
func TestTicketRefusesTheClassTheShapeAndTheFields(t *testing.T) {
	t.Run("a class the route does not admit", func(t *testing.T) {
		f := newTickets(t)
		caller := IdentityCaller{Address: testJobSA, Class: identitywire.ClassJobAccount}
		rec, body := f.ask(t, caller, f.goodAsk())
		f.refused(t, rec, body, testJobSA, StatusUnverified, identitywire.RefusalCallerNotAdmitted, ReasonNotAdmitted)
		if len(f.scope.asks) != 0 {
			t.Errorf("the launch scope was asked for a caller the route does not admit")
		}
		f.nothingWritten(t)
	})

	for _, tc := range []struct {
		name   string
		mutate func(*identitywire.TicketRequest)
		names  string
	}{
		{"a repository slug of no shape", func(a *identitywire.TicketRequest) { a.Slug = "" }, "slug"},
		{"a run id of no shape", func(a *identitywire.TicketRequest) { a.Run = "not-a-run-id" }, "run id"},
		{"an ask that names no runtime", func(a *identitywire.TicketRequest) { a.MaxRuntimeSeconds = -1 }, "runtime"},
		{"an attempt from a class that has none", func(a *identitywire.TicketRequest) { a.Attempt = "wi-1:1" }, "attempt"},
		{"an attempt no orchestrator could have written", func(a *identitywire.TicketRequest) { a.Attempt = "wi-1:0" }, "attempt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTickets(t)
			ask := f.goodAsk()
			tc.mutate(&ask)
			rec, body := f.ask(t, f.person(testLauncherAccount), ask)
			e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
			if !strings.Contains(e.Message, tc.names) {
				t.Errorf("the refusal does not name the %s: %s", tc.names, e.Message)
			}
			f.nothingWritten(t)
		})
	}

	t.Run("a body naming a field of its own", func(t *testing.T) {
		f := newTickets(t)
		// The ask is well formed and carries ONE field this wire does not have,
		// which is what a caller reaching for its own identity sends.
		raw, err := json.Marshal(map[string]any{
			"slug": f.slug, "run": f.run, "account": testJobSA,
			"launcher": testLauncherAccount, "max_runtime_seconds": 3600,
			"uid": "a-uid-the-call-chose",
		})
		if err != nil {
			t.Fatalf("building the body: %v", err)
		}
		rec, body := f.raw(t, f.person(testLauncherAccount), string(raw))
		e := f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		if !strings.Contains(e.Message, "uid") {
			t.Errorf("the refusal does not name the field it refused: %s", e.Message)
		}
		f.nothingWritten(t)
	})

	t.Run("a body that is not this contract", func(t *testing.T) {
		f := newTickets(t)
		rec, body := f.raw(t, f.person(testLauncherAccount), "{not json")
		f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
		f.nothingWritten(t)
	})

	t.Run("an attempt from the orchestrator", func(t *testing.T) {
		f := newTickets(t)
		ask := f.goodAsk()
		ask.Attempt = "wi-1:1"
		ask.ContextRepos = nil
		caller := IdentityCaller{Address: testOrchestratorSA, Class: identitywire.ClassOrchestrator}
		rec, body := f.ask(t, caller, ask)
		f.issued(t, rec, body)
		if got := f.record(t, f.slug, f.run); got.Attempt != "wi-1:1" {
			t.Errorf("the record carries attempt %q, want the orchestrator's own reference: it is what says which attempt the run belongs to", got.Attempt)
		}
	})
}

// TestTicketRefusesWhenTheScopeSaysNoOrFaults is R16 at this route: the scope
// check's refusal is the CALLER's and is answered as the launch-scope refusal,
// while a fault is the SERVICE's — a roles record nobody could read is not an
// empty one, and answering a person "no launch scope" when the service could
// not tell would halt a run that holds its scope on a 403 (a run's gateway
// reads one as a missing invoke grant).
func TestTicketRefusesWhenTheScopeSaysNoOrFaults(t *testing.T) {
	t.Run("the scope refuses the caller", func(t *testing.T) {
		f := newTickets(t)
		f.scope.refuse = errors.New("this caller holds no launch action on this repository")
		rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		e := f.refused(t, rec, body, testLauncherAccount, StatusUnverified, identitywire.RefusalNoLaunchScope, ReasonNotAdmitted)
		if !strings.Contains(e.Message, "launch action") {
			t.Errorf("the refusal does not carry the checker's own sentence: %s", e.Message)
		}
		f.nothingWritten(t)
	})

	t.Run("the scope faults", func(t *testing.T) {
		f := newTickets(t)
		f.scope.fault = &ScopeFault{What: "the roles node", Err: errors.New("the database could not be read")}
		rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		f.refused(t, rec, body, testLauncherAccount, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable)
		if logs := f.logs.String(); !strings.Contains(logs, "could not be judged") {
			t.Errorf("the service's own line does not name the fault: %s", logs)
		}
		f.nothingWritten(t)
	})
}

// TestTicketRefusesWhenTheServiceItselfCannotAnswer is the fail-closed floor:
// every way this service can fail to make the decision is answered as the
// service's own failure and never as a verdict about the caller.
//
// The no-issuer case keeps the contract's "not served" — the code a CLI
// bridges on (R7) — and it is the one case where a caller IS verified, so it
// owes the audit line the mount's own "not served" does not. The wiring faults
// (no store, no scope, no project to claim) are deliberately NOT "not served":
// a CLI reading that would sign the run's identity locally with a row this
// installation may already have moved.
func TestTicketRefusesWhenTheServiceItselfCannotAnswer(t *testing.T) {
	t.Run("no issuer is configured", func(t *testing.T) {
		f := newTickets(t, func(_ *svc, cfg *Config) { cfg.Identity.Signer = "" })
		rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		e := f.refused(t, rec, body, testLauncherAccount, StatusUnavailable, identitywire.RefusalNotServed, ErrorStateUnavailable)
		if !strings.Contains(e.Message, "fugaro update") {
			t.Errorf("the refusal does not name the fix: %s", e.Message)
		}
		f.nothingWritten(t)
	})

	for _, tc := range []struct {
		name   string
		mutate func(*svc, *Config)
	}{
		{"no issuance store", func(_ *svc, cfg *Config) { cfg.Identity.Store = nil }},
		{"no scope check", func(_ *svc, cfg *Config) { cfg.Identity.Scope = nil }},
		{"no project to claim", func(s *svc, _ *Config) { s.auth.cfg.Project = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTickets(t, tc.mutate)
			rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
			e := f.refused(t, rec, body, testLauncherAccount, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable)
			if e.Error == identitywire.RefusalNotServed {
				t.Errorf("a wiring fault is answered 'not served': a CLI would bridge on it and sign the identity locally")
			}
			f.nothingWritten(t)
		})
	}

	t.Run("the record cannot be written", func(t *testing.T) {
		f := newTickets(t)
		f.db.DenyNext(1)
		rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		f.refused(t, rec, body, testLauncherAccount, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable)
		if logs := f.logs.String(); !strings.Contains(logs, "record could not be written") {
			t.Errorf("the service's own line does not name the failure: %s", logs)
		}
		f.nothingWritten(t)
	})

	t.Run("the run's outcome cannot be read", func(t *testing.T) {
		f := newTickets(t)
		f.db.DenyRead("outcomes")
		rec, body := f.ask(t, f.person(testLauncherAccount), f.goodAsk())
		f.refused(t, rec, body, testLauncherAccount, StatusUnavailable, ErrorStateUnavailable, ErrorStateUnavailable)
		f.nothingWritten(t)
	})
}

// TestTicketAdmitsTheInstallationsOtherJobAccounts is F12's other half: the
// account an ask names may also be a SHARED project job's (the ticket narrows
// to the profile and not to the repository, which F12 states) or one of the
// installation's review jobs (whose accounts name no repository at all). The
// addresses are compared folded, as every address in this service is: a
// deployment spells the row, and Google's token carries the address the way its
// owner registered it.
func TestTicketAdmitsTheInstallationsOtherJobAccounts(t *testing.T) {
	t.Run("a shared project job's account", func(t *testing.T) {
		f := newTickets(t, func(_ *svc, cfg *Config) {
			cfg.SharedAccounts = []SharedJobAccount{{Email: testSharedSA, Profile: testSharedProfile, Repos: []string{testSharedRepo}}}
		})
		ask := f.goodAsk()
		ask.Account = testSharedSA
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		f.issued(t, rec, body)
	})

	t.Run("a shared job's account whose profile is another repository", func(t *testing.T) {
		f := newTickets(t, func(_ *svc, cfg *Config) {
			// The same account, with the profile bound to another repository:
			// what F12 narrows is the profile's own set, so a run of this
			// repository may not go out on it.
			cfg.SharedAccounts = []SharedJobAccount{{Email: testSharedSA, Profile: testSharedProfile, Repos: []string{testOtherRepo}}}
		})
		ask := f.goodAsk()
		ask.Account = testSharedSA
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		f.refused(t, rec, body, testLauncherAccount, StatusRequestRefused, identitywire.RefusalIdentityRefused, ErrorRequestRefused)
	})

	t.Run("a review job's account, in the configuration's other spelling", func(t *testing.T) {
		f := newTickets(t, func(_ *svc, cfg *Config) {
			// The row is spelled the way a deployer's console spells it, and the
			// ask carries the address Google's token would: the two are compared
			// folded (foldEmail), which is the rule for every address here.
			cfg.ReviewAccounts = []ReviewJobAccount{{Email: strings.ToUpper(testReviewSA), Account: testReviewAccount}}
		})
		ask := f.goodAsk()
		ask.Account = testReviewSA
		rec, body := f.ask(t, f.person(testLauncherAccount), ask)
		f.issued(t, rec, body)
	})
}
