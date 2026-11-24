package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// These are LM7's runner half: the run claims the identity the launcher was
// issued a ticket for, and the bridge that holds both mechanisms for one
// release (R7) -- the claim where the execution carries a ticket, the sealed
// object where it carries the old marker.
//
// The claim is a whole run's, not a fixture's idea of one: what the tests read
// is what the installation's credentials service saw (the fixture's upstream,
// which a cascade run sends every call of its own to) and what the run ended
// with, because the two things that can go wrong here -- claiming twice, and
// running with no identity at all -- are both invisible from inside the run.
//
// ticket is the one-time secret the launcher of this release put on the
// execution (identitywire.TicketEnv, LM6). It is a fixture's own value and
// never a credential: the stand-in below is the only service it reaches.
const ticketFixture = "fake-ticket-0f1e2d3c-not-a-credential"

// claimCall is one call the run made on the claim route, as the fixture's
// upstream read it.
type claimCall struct {
	method string
	header http.Header
	body   []byte
}

// claimCalls returns the calls the fixture's upstream read on the claim route.
// A cascade run sends every call it makes to the credentials service to the one
// host (C1.15), so this is where the run's claim is read -- and the tests that
// assert a run made none read the same list, so the absence is this upstream's
// observation and not the run's own account of itself.
func claimCalls(t *testing.T, f *anthropicfake.Fake) []claimCall {
	t.Helper()
	seen := f.Seen()
	var out []claimCall
	for i, r := range seen {
		if r.URL.Path == identitywire.ClaimPath {
			out = append(out, claimCall{method: r.Method, header: r.Header, body: f.Body(i)})
		}
	}
	return out
}

// answerClaim makes the fixture's upstream answer the claim route with answer,
// and every other path as the cascade stand-in did. n is the number of the
// claim this is (1 for the first), so a test can fail one and answer the next.
func answerClaim(t *testing.T, b *bk, answer func(n int, header http.Header, body []byte) anthropicfake.Reply) {
	t.Helper()
	model := b.fake.Func
	if model == nil {
		t.Fatal("the fixture's upstream answers no calls: a cascade run's own route through the service is what these tests read")
	}
	var mu sync.Mutex
	n := 0
	b.fake.Func = func(r *http.Request, body []byte) anthropicfake.Reply {
		if r.URL.Path != identitywire.ClaimPath {
			return model(r, body)
		}
		mu.Lock()
		n++
		this := n
		mu.Unlock()
		return answer(this, r.Header, body)
	}
}

// claimed is the fixture's run as a launch of this release leaves it: the
// execution carries a ticket and the launcher minted nothing (LM6, R7). The
// fixture's token object is removed and the old marker cleared, so the claim is
// the only way this run can reach an identity at all.
func claimed(t *testing.T, b *bk) {
	t.Helper()
	b.unmarked()
	if err := token.DeleteObject(context.Background(), b.bucket, repoSlug, runID); err != nil {
		t.Fatal(err)
	}
	b.deps.Env = append(filterEnv(b.deps.Env, identitywire.TicketEnv), identitywire.TicketEnv+"="+ticketFixture)
	b.deps.Backend.Ticket = ticketFixture
}

// claimAnswer is the service's answer to a claim that landed: the custom token
// the record's claims were signed into, as LM2's route answers it.
func claimAnswer(t *testing.T, custom string) anthropicfake.Reply {
	t.Helper()
	body, err := json.Marshal(identitywire.ClaimResponse{Token: custom, ExpiresAt: time.Now().Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return anthropicfake.Reply{Header: jsonHeader(), Body: string(body)}
}

// jsonHeader is the content type every answer of the service carries.
func jsonHeader() http.Header {
	return http.Header{"Content-Type": []string{"application/json"}}
}

// TestRunnerClaimsItsIdentity: a run whose execution carries a ticket claims
// its identity from the service, with the job's own Google ID token and a nonce
// the run drew itself, and exchanges the token the claim answered -- the run
// then lives on that identity, accounted for like any other (LM7, C1, R2).
//
// The fixture's launch minted nothing: there is no token object in the bucket
// and no marker in the job, so the claim is the only identity this run can
// reach, and a run that read the bucket instead of claiming stops before any
// agent call.
func TestRunnerClaimsItsIdentity(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	// The token the service signs for this run: the claims the record carries,
	// minted by the fixture's own signer, which is the identity the run's
	// exchange takes and the session opens on.
	custom := b.mintToken(repoSlug, runID, time.Now())
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return claimAnswer(t, custom)
	})
	claimed(t, b)

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec == nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a run that claims its identity must finish on it: rec = %+v, err = %v", rec, err)
	}
	calls := claimCalls(t, b.fake)
	if len(calls) != 1 {
		t.Fatalf("the credentials service was asked to claim %d times, want the run's one: a claim is taken once (R5)", len(calls))
	}
	c := calls[0]
	if c.method != http.MethodPost {
		t.Errorf("the claim is a %s, want a POST", c.method)
	}
	// The job's own Google ID token is what the service decides the claim by
	// (R2): nothing but its account is read, and it is the identity the job
	// presents and no other.
	if got := c.header.Get("Authorization"); got != "Bearer "+beatIDToken {
		t.Errorf("the claim's Authorization is %q, want the job's own ID token from the metadata server", got)
	}
	var req identitywire.ClaimRequest
	if err := json.Unmarshal(c.body, &req); err != nil {
		t.Fatalf("the claim's body is not the route's request: %v", err)
	}
	if req.Ticket != ticketFixture {
		t.Errorf("the claim presents the ticket %q, want the one the launcher put on the execution", req.Ticket)
	}
	if len(req.Nonce) < 16 {
		t.Errorf("the claim carries the nonce %q, want a value the run drew itself (R2)", req.Nonce)
	}
	if req.Nonce == ticketFixture {
		t.Error("the claim's nonce is the ticket: it is the run's own value, drawn here, and the service keeps only its digest")
	}

	// Nothing the run published carries the ticket. It is a secret of the
	// service's, handed to one execution, and it dies into a token at this
	// claim; the record and every line the run wrote are read back for it.
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(ticketFixture)) {
		t.Error("the run's record carries the ticket, which is a secret of the service's")
	}
	if strings.Contains(b.logs.String(), ticketFixture) {
		t.Errorf("a log line carries the ticket:\n%s", b.logs.String())
	}
}

// TestRunnerHaltsWhenTicketAlreadyClaimed: a ticket is claimed once (R5). The
// second claim is refused with one audit line, and the run that lost halts
// saying so -- no session, no cap and no kill switch would be worse than
// stopping, and a refusal is a decision: the run does not ask again.
func TestRunnerHaltsWhenTicketAlreadyClaimed(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	body, err := json.Marshal(credsvc.WireError{Error: identitywire.RefusalIdentityRefused, Message: "the ticket was already claimed"})
	if err != nil {
		t.Fatal(err)
	}
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusConflict, Header: jsonHeader(), Body: string(body)}
	})
	claimed(t, b)

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a refused claim halts the run, and a halt is an outcome and not an error: %v", err)
	}
	if rec == nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("the run did not halt on a refused claim: %+v", rec)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Fatalf("the halt is %+v, want the credentials service's own refusal class (%s)", rec.Halt, runstore.HaltBudgetUnavailable)
	}
	if !strings.Contains(rec.Halt.Detail, "already claimed") {
		t.Errorf("the halt's detail does not say the ticket was already claimed: %q", rec.Halt.Detail)
	}
	if !strings.Contains(rec.Halt.Detail, identitywire.RefusalIdentityRefused) {
		t.Errorf("the halt's detail does not name the contract's own code: %q", rec.Halt.Detail)
	}
	if !strings.Contains(rec.Halt.Detail, "fugaro run") {
		t.Errorf("the halt's detail names no fix: %q", rec.Halt.Detail)
	}
	if got := len(claimCalls(t, b.fake)); got != 1 {
		t.Errorf("the service was asked to claim %d times after refusing one, want one: a refusal is an answer, and asking again answers the same thing", got)
	}
	if rec.Halt.Detail == "" || !strings.Contains(rec.Halt.Detail, "already claimed") {
		t.Errorf("the detail is not the sentence a person reads: %q", rec.Halt.Detail)
	}
}

// TestRunnerRetriesThenHaltsWhenIssuerUnreachable: a service that answers no
// call is retried with backoff for a bounded time and then the run halts (R1,
// spec §Resilience). Every attempt presents the SAME nonce, because a retry
// after a lost answer must not be a second claim (R2): the first attempt may
// have landed at the service, and the nonce is what makes the second call the
// same claim rather than another one.
func TestRunnerRetriesThenHaltsWhenIssuerUnreachable(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	// No status line at all: the connection is dropped, which is what a service
	// that is not there answers a run.
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{DropBeforeHeaders: true}
	})
	claimed(t, b)

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a halt is an outcome and not an error: %v", err)
	}
	if rec == nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("the run did not halt on a service that answers nothing: %+v", rec)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Fatalf("the halt is %+v, want %s (D143: a run that cannot reach the credentials service halts like budget_unavailable)", rec.Halt, runstore.HaltBudgetUnavailable)
	}
	if !strings.Contains(rec.Halt.Detail, "could not be reached") {
		t.Errorf("the halt's detail does not say the service could not be reached: %q", rec.Halt.Detail)
	}
	if !strings.Contains(rec.Halt.Detail, "fugaro run") {
		t.Errorf("the halt's detail names no fix: %q", rec.Halt.Detail)
	}
	calls := claimCalls(t, b.fake)
	if len(calls) < 2 {
		t.Fatalf("the service saw %d claim attempts, want the retry: an attempt nothing answered is asked again", len(calls))
	}
	var nonces []string
	for _, c := range calls {
		var req identitywire.ClaimRequest
		if err := json.Unmarshal(c.body, &req); err != nil {
			t.Fatalf("a claim's body is not the route's request: %v", err)
		}
		nonces = append(nonces, req.Nonce)
	}
	for i, got := range nonces {
		if got != nonces[0] {
			t.Fatalf("attempt %d presents the nonce %q, want the first attempt's %q: a retry of a claim that may have landed is not a second claim (R2)", i+1, got, nonces[0])
		}
	}
}

// TestRunnerStillOpensASealedToken: the bridge's other arm (R7). An execution
// that carries the old marker -- the launch of a release that signed the run's
// identity itself and left it sealed -- opens the sealed object as before, and
// never claims: there is no ticket on it, and a run that asked the issuer
// anyway would be one whose identity came from somewhere it was not told to
// look.
func TestRunnerStillOpensASealedToken(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	// A claim this run must never make. It is answered a failure, so a run
	// that made one stops instead of quietly succeeding with an identity from
	// the wrong place.
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return anthropicfake.Reply{Status: http.StatusInternalServerError, Header: jsonHeader(), Body: `{"error":"the run is not supposed to claim"}`}
	})

	// The fixture as it stands is the old launch: the marker with the seal key,
	// and the run's token object sealed under it in the bucket.
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec == nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a marked launch must open its sealed object as before: rec = %+v, err = %v", rec, err)
	}
	if got := len(claimCalls(t, b.fake)); got != 0 {
		t.Errorf("the run claimed its identity %d times with no ticket on its execution, want none: R7's other arm is the sealed object", got)
	}
}

// TestTheTicketIsReadFromTheJobEnvironment: the ticket is a launch fact and
// travels in the execution's environment (identitywire.TicketEnv, LM6), so this
// is where the runner reads it -- and an environment value this run cannot use
// stops it rather than leaving it to read as "no ticket", which is a launch the
// sealed object would then be read for and never find (R7).
func TestTheTicketIsReadFromTheJobEnvironment(t *testing.T) {
	be, err := runner.BackendFromEnv(envOf(map[string]string{identitywire.TicketEnv: ticketFixture}), false)
	if err != nil || be.Ticket != ticketFixture {
		t.Fatalf("a ticket in the job's environment = %+v, %v, want it read", be, err)
	}
	// A value the run cannot present is damage to the job's environment, and it
	// fails closed: the ticket it cannot use must not become a run that reads
	// the sealed object of a launch that minted none.
	// The last is longer than any ticket the service draws (crypto/rand, like
	// the slip's claim secret): a job's environment is not a place to carry a
	// secret this run would then put in a request.
	for _, bad := range []string{"", "two words", "a\nnewline", strings.Repeat("t", 1<<10)} {
		be, err := runner.BackendFromEnv(envOf(map[string]string{identitywire.TicketEnv: bad}), false)
		if err == nil {
			t.Errorf("the ticket %q was accepted (%+v)", bad, be)
			continue
		}
		if !be.MalformedEnv {
			t.Errorf("the ticket %q: MalformedEnv = false, so the run would read no ticket and look for an object", bad)
		}
		if bad != "" && strings.Contains(err.Error(), bad) {
			t.Errorf("the ticket %q reaches the error text: %v", bad, err)
		}
	}
}

// TestRunnerWithATicketAndNoBackendRefuses is the ticket's half of M2's
// refusal (the marker's is TestRunnerWithTheMarkerAndNoBackendRefuses): a job
// whose launcher asked an issuer for this run's identity -- which it does only
// when the project has a shared budget or a published policy -- and whose
// environment carries no database to read it in. The run stops instead of going
// on with the environment ceiling, no session and no kill switch.
//
// The two mechanisms answer M2 through one predicate (Backend.Issued), and that
// is exactly what this test holds: a run that read the ticket as "nothing was
// provisioned here" would find no backend, call this project's having published
// nothing, and run on the ceiling the policy was published to replace.
//
// The run makes no claim on the way to that refusal, and that is an
// observation of the placement rather than an incidental one: an identity is
// claimed after the ceiling is known, so a run that asked the service for one
// before knowing whether this installation has a budget would have taken it to
// spend where nothing accounts for it.
//
// Mutation (verified, run and restored): make Backend.Issued answer
// b.TokenMarked alone -- the run goes on, and both this test and
// TestRunnerWithATicketAndNoBackendRefusesBeforeTheClone fail: this one on the
// stage it reached (there is no refusal and no bootstrap halt at all), that one
// on the git credential it handed out on the way to the clone.
func TestRunnerWithATicketAndNoBackendRefuses(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "off", "")
	withBucket(g.harness)
	g.deps.Backend = runner.Backend{Ticket: ticketFixture}
	rec, err := g.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a claimed run with no backend in its job must stop: rec = %+v", rec)
	}
	for _, want := range []string{identitywire.TicketEnv, runner.RTDBURLEnv, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The refusal is M2's, met where the identity was taken, and not the
	// claim's own: a run that got as far as the service would have a sentence
	// about the claim and about the service.
	if !strings.Contains(err.Error(), "carries no budget backend") {
		t.Errorf("the refusal is not the missing backend's: %v", err)
	}
	if len(g.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded {
		t.Fatalf("the run went on: calls = %d, rec = %+v", len(g.agent.calls), rec)
	}
	if got := len(claimCalls(t, g.fake)); got != 0 {
		t.Errorf("the run claimed its identity %d times with no backend to be accounted in, want none: an identity claimed there would carry no session, no cap and no kill switch", got)
	}
}

// TestRunnerWithATicketAndNoBackendRefusesBeforeTheClone is the ticket's half of
// the same regression, met at the policy read: the refusal has to come before
// admissionCheck and before the provider is opened. Stopping later is the
// fail-open direction -- the policy read answered "no policy", so the
// allow-list was never consulted, a git credential was handed out and the
// repository was cloned on a ceiling nobody had published.
//
// Mutation (verified, run and restored): make Backend.Issued answer
// b.TokenMarked alone -- this test fails on the credential count ("N git
// credential requests were made before the refusal") and on nothing else.
func TestRunnerWithATicketAndNoBackendRefusesBeforeTheClone(t *testing.T) {
	b := admissionRun(t, "github", "acme/app", "off", "", "")
	b.deps.Backend = runner.Backend{Ticket: ticketFixture}
	rec, err := b.run(t, implement("feature"))
	if err == nil {
		t.Fatalf("a claimed run with no backend in its job must stop: rec = %+v", rec)
	}
	for _, want := range []string{identitywire.TicketEnv, runner.RTDBURLEnv, "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if b.auths == nil {
		t.Fatal("the fixture is not counting git credential requests")
	}
	if *b.auths != 0 {
		t.Errorf("%d git credential requests were made before the refusal: the run read its job's ticket as nothing having been provisioned, skipped admission and cloned", *b.auths)
	}
	if len(b.agent.calls) != 0 || rec.Status == runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("a stage ran or an outcome was written: calls = %d, rec = %+v", len(b.agent.calls), rec)
	}
	if got := len(claimCalls(t, b.fake)); got != 0 {
		t.Errorf("the run claimed its identity %d times before the refusal, want none: the claim is taken after the ceiling is known", got)
	}
}

// TestRunnerHaltsWhenTheClaimURLIsNotThisRunToSend: the job's address for the
// credentials service is http on a host that is not this machine, and the claim
// carries the job's own Google ID token, the ticket and the nonce, so it would
// travel in cleartext. A slip is given up over this and the run goes on without
// one; a claim is not optional -- a run with a ticket holds no seal key -- so
// the run halts (D143) rather than presenting them.
//
// Mutation (verified, run and restored): drop the checkSlipServiceURL call from
// claimBudgetIdentity -- this test fails on the halt's detail (the run then
// spends the retry trying to reach the host and halts saying it could not be
// reached) and on nothing else.
func TestRunnerHaltsWhenTheClaimURLIsNotThisRunToSend(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	// A claim this run must never make: answered a success, so a run that made
	// one would go on and look like a run that claimed properly.
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return claimAnswer(t, b.mintToken(repoSlug, runID, time.Now()))
	})
	claimed(t, b)
	b.deps.Env = append(filterEnv(b.deps.Env, runner.CredentialsServiceURLEnv),
		runner.CredentialsServiceURLEnv+"=http://credentials.example.invalid:8443")

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a halt is an outcome and not an error: %v", err)
	}
	if rec == nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("the run did not halt on a URL it will not present its identity to: %+v", rec)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Fatalf("the halt is %+v, want %s", rec.Halt, runstore.HaltBudgetUnavailable)
	}
	if !strings.Contains(rec.Halt.Detail, "not https") {
		t.Errorf("the halt's detail does not say why the claim was not made: %q", rec.Halt.Detail)
	}
	if !strings.Contains(rec.Halt.Detail, "fugaro run") {
		t.Errorf("the halt's detail names no fix: %q", rec.Halt.Detail)
	}
	if got := len(claimCalls(t, b.fake)); got != 0 {
		t.Errorf("the claim was sent %d times to a URL this run refuses, want none: the token, the ticket and the nonce would be in cleartext", got)
	}
}

// TestRunnerHaltsWhenTheJobNamesNoCredentialsService: the execution carries a
// ticket -- so the installation has an issuer, and this job cannot reach it --
// and nothing of the launch can be spent anywhere else. The run halts rather
// than going on with no identity at all.
//
// Mutation (verified, run and restored): make the missing-URL arm return the
// run's usual path (nil, nil) instead of halting -- this test fails: the run
// goes on with an empty identity, and what reaches it first is the budget
// backend refusing "budget token: invalid: not a JWT" as an error, not a halt.
// A run that got that far has already been admitted with no identity at all.
func TestRunnerHaltsWhenTheJobNamesNoCredentialsService(t *testing.T) {
	b := newCascade(t, cascadeConfig(t), &cascadeService{})
	answerClaim(t, b, func(int, http.Header, []byte) anthropicfake.Reply {
		return claimAnswer(t, b.mintToken(repoSlug, runID, time.Now()))
	})
	claimed(t, b)
	// The job of an installation whose service address landed after this job
	// was applied (U4): the ticket is on the execution and there is nowhere to
	// present it.
	b.deps.Env = filterEnv(b.deps.Env, runner.CredentialsServiceURLEnv)

	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatalf("a halt is an outcome and not an error: %v", err)
	}
	if rec == nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("the run did not halt with no credentials service to claim from: %+v", rec)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltBudgetUnavailable {
		t.Fatalf("the halt is %+v, want %s", rec.Halt, runstore.HaltBudgetUnavailable)
	}
	for _, want := range []string{identitywire.TicketEnv, runner.CredentialsServiceURLEnv, "fugaro update"} {
		if !strings.Contains(rec.Halt.Detail, want) {
			t.Errorf("the halt's detail does not name %q: %q", want, rec.Halt.Detail)
		}
	}
	if got := len(claimCalls(t, b.fake)); got != 0 {
		t.Errorf("the claim was sent %d times with no service address in the job, want none", got)
	}
}
