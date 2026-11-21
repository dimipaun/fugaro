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
// host (C1.15), so this is where the run's claim is read.
func claimCalls(t *testing.T, b *bk) []claimCall {
	t.Helper()
	seen := b.fake.Seen()
	var out []claimCall
	for i, r := range seen {
		if r.URL.Path == identitywire.ClaimPath {
			out = append(out, claimCall{method: r.Method, header: r.Header, body: b.fake.Body(i)})
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
	calls := claimCalls(t, b)
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
	if got := len(claimCalls(t, b)); got != 1 {
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
	calls := claimCalls(t, b)
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
	if got := len(claimCalls(t, b)); got != 0 {
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
