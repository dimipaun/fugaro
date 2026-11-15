package credsvc

// The reaction this route takes to what bitbucket.org answers (T9, R4, R14).
//
// The property every test here pins is one sentence of the plan's: the answer
// and its status are PASSED THROUGH UNCHANGED, and the reaction the answer
// earns — a mark, a dropped project match — is recorded on the request's own
// single audit line. What the route must never do is act on an answer that does
// not say what it is being read as saying: a 401 is confirmed with a read of a
// repository the token covers before it marks anything, a 403 on a write is
// the token's scope and not the repository's absence, and a 404 is an ordinary
// answer.
//
// Every test here holds the service to the CONTRACT and not to the real store:
// the one value answering the selection, the resolver and the status store is
// the contract's own stub (bbtokens.Stub), which is the seam this task builds
// against and what I1 replaces with the real store. The fake upstream is
// bitbucketproxy_test.go's, so the two hosts, the rebuilt URL and the token a
// call carries are the ones this route's own tests already pin.

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// reactConfirmPath is the path of the read this route owes a 401: the
// repository read bitbucketProbeURL builds for the fixture's repository (R14).
// The calls these tests make are git's, so this path tells the confirmation
// apart from the call it confirms — and it is a repository path and never
// /2.0/user, which is the whole of what it pins.
const reactConfirmPath = "/2.0/repositories/" + testBitbucketRepo

// reactFetchPath is the call these tests make of the route: git's advertisement
// request for the fixture's repository, which is a READ (so no write scope is
// in question) and the call a proxied run opens every fetch with.
func reactFetchPath() string {
	return proxyPath(proxyGitPath("info/refs") + "?service=git-upload-pack")
}

// proxyReactSvc is the installation these tests are written against: the
// contract's stub as the WHOLE store — its one value answers the selection (T8)
// and the two side handles T9's reactions are taken through — the bundle's
// legacy entry with one named token beside it, and the fake upstream. The stub
// selects the named token, so a test knows which token a reaction is about
// without either value being written down.
func proxyReactSvc(t *testing.T, mutate ...func(*svc, *Config)) (*svc, *proxyUpstream, *bbtokens.Stub, string) {
	t.Helper()
	stub := &bbtokens.Stub{Selected: proxyNamedToken}
	s, up, _, named := proxyTokensSvc(t, stub, mutate...)
	return s, up, stub, named
}

// reactAnswers makes the fake answer the route's own call and the confirmation
// read it owes on a 401 differently, told apart by the path the service built
// (proxyUpstream records it before the hop is rewritten to the loopback
// listener). The write is made under the fake's own lock, because the handler
// reads the answer on every call.
func reactAnswers(up *proxyUpstream, call, confirm proxyAnswer) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.answer = func(r *http.Request) proxyAnswer {
		if r.URL.Path == reactConfirmPath {
			return confirm
		}
		return call
	}
}

// reactAnswerEveryWay is reactAnswers for the tests where the confirmation
// can never be reached, and for the ones that hold every call of the request to
// one answer.
func reactAnswerEveryWay(up *proxyUpstream, call proxyAnswer) { reactAnswers(up, call, call) }

// reactNoTokenValue holds the three places a token value could surface — the
// answer, the service's own log and the audit line — to the values this test
// knows, which are the two entries of the fixture's bundle: the token that
// served the request and the legacy entry that did not.
func reactNoTokenValue(t *testing.T, s *svc, ans []byte, values ...string) {
	t.Helper()
	for what, text := range map[string]string{
		"the answer":     string(ans),
		"the log":        s.logs.String(),
		"the audit line": s.audit.String(),
	} {
		for _, value := range values {
			if strings.Contains(text, value) {
				t.Errorf("%s carries the value of a Bitbucket token", what)
			}
		}
	}
}

// TestProxy401ConfirmedByARepositoryReadMarksBroken: an upstream 401 is
// confirmed with a read of a repository the token covers before the token is
// marked broken (R4, R14) — never from the 401 itself, and never with a
// GET /user — and the request's single line records the mark.
//
// The facts beside the state are carried over: the identity the add-time check
// proved and the count of the window are what `creds ls` and `doctor` show
// (R12, D2356), so a mark that wrote a fresh status would take two facts away
// with the one it wrote.
func TestProxy401ConfirmedByARepositoryReadMarksBroken(t *testing.T) {
	s, up, stub, named := proxyReactSvc(t)
	reactAnswerEveryWay(up, proxyAnswer{status: http.StatusUnauthorized})
	stub.Marks = map[bbtokens.Name]bbtokens.Status{
		proxyNamedToken: {VerifiedAt: s.now, Identity: "uuid-ci", Count: 7},
	}

	resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the call answered %d, want the upstream's own 401 (%s)", resp.StatusCode, ans)
	}

	calls := up.calls()
	if len(calls) != 2 {
		t.Fatalf("the upstream saw %d calls, want the call and the one confirmation read: %+v", len(calls), calls)
	}
	if got := calls[1]; got.method != http.MethodGet || got.host != "api.bitbucket.org" || got.path != reactConfirmPath {
		t.Errorf("the confirmation was %s %s%s, want a GET of %s: a repository the token covers, and never GET /2.0/user (R14, O7)",
			got.method, got.host, got.path, reactConfirmPath)
	}
	if got := calls[1].auth; got != "Bearer "+named {
		t.Errorf("the confirmation was shown %q, want the token the request was served with: it is the token the 401 is about", got)
	}

	mark := stub.Marks[proxyNamedToken]
	if mark.State != bbtokens.StateBroken {
		t.Errorf("the token's status is %+v, want broken: the 401 was confirmed by a read of a repository it covers (R4)", mark)
	}
	if mark.VerifiedAt != s.now || mark.Identity != "uuid-ci" || mark.Count != 7 {
		t.Errorf("the mark = %+v, want the facts the store held carried over beside the state (R12, D2356)", mark)
	}

	line := s.onlyAuditLine(t)
	if got := auditField(line, "status"); got != ReasonBroken {
		t.Errorf("the line's status is %q, want %q: the one line records the mark the reaction left (R14)", got, ReasonBroken)
	}
	if got := auditField(line, "credential"); got != string(proxyNamedToken) {
		t.Errorf("the line names the credential %q, want the document key of the token the request was served with (%q)", got, proxyNamedToken)
	}
	if line["purpose"] != PurposeGit {
		t.Errorf("the line = %v, want git's own purpose", line)
	}
	reactNoTokenValue(t, s, ans, named)
}

// TestProxyUserCall401MarksNothing is the plan's red step, on the other side.
//
// `GET /2.0/user` is the call every run's own selfUser makes, and the answer a
// repository token gets for it is a refusal of the ENDPOINT rather than of the
// token (R14, O7). So a 401 on it is not evidence about the token: this route
// marks nothing from that answer, and the read it makes to confirm a 401 is a
// repository the token covers — never /user again. The fake answers the user
// call 401 and every repository read 200, which is a token that WORKS: an
// implementation that marked the token broken on the user answer, or confirmed
// by re-reading /user, marks it here and fails.
func TestProxyUserCall401MarksNothing(t *testing.T) {
	s, up, stub, _ := proxyReactSvc(t)
	up.mu.Lock()
	up.answer = func(r *http.Request) proxyAnswer {
		if r.URL.Path == "/2.0/user" {
			return proxyAnswer{status: http.StatusUnauthorized}
		}
		return proxyAnswer{status: http.StatusOK, body: []byte("{}")}
	}
	up.mu.Unlock()

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath("2.0/user"), nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the call answered %d, want the upstream's own 401 (%s)", resp.StatusCode, ans)
	}
	for i, got := range up.calls() {
		if i == 0 {
			if got.path != "/2.0/user" {
				t.Fatalf("the first call the upstream saw is %s, want the run's own GET /2.0/user", got.path)
			}
			continue
		}
		if got.path != reactConfirmPath {
			t.Errorf("the service made a second call of %s, and the read it confirms a 401 with is a repository the token covers (R14)", got.path)
		}
	}
	if mark, ok := stub.Marks[proxyNamedToken]; ok && !mark.Available() {
		t.Errorf("the token's status is %+v, want nothing marked: a GET /user answer never marks a token (R14)", mark)
	}
	if len(stub.Marks) != 0 {
		t.Errorf("the store holds %v, want nothing: the token served a repository read (200), so it works", stub.Marks)
	}
	if got := auditField(s.onlyAuditLine(t), "status"); got != StatusOK {
		t.Errorf("the line's status is %q, want %q: nothing was done about this answer", got, StatusOK)
	}
	if n := len(stub.Counted); n != 1 {
		t.Errorf("the token was counted %d times, want once per request served with it (D2356)", n)
	}
}

// TestProxyUnconfirmed401MarksNothing: only a confirmed refusal marks a token
// broken (R4, R14). A confirmation that answers anything else — a 200 (the
// token works, so it was the caller's CALL that was refused), a 403 for a
// repository the record claimed, a 5xx — leaves the token in service.
//
// The direction is deliberate and it is the opposite of the forward path's: a
// token marked broken is out of service until an operator clears it (C1.21), so
// an inconclusive read that took a working token out of service would stop every
// run that token serves. A dead token left in costs a 401 per call, which is
// where this route was before it had a store at all.
func TestProxyUnconfirmed401MarksNothing(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
	}{
		{"the token works and the call was refused", http.StatusOK},
		{"the record claimed a repository the token does not reach", http.StatusForbidden},
		{"Bitbucket failed", http.StatusInternalServerError},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, up, stub, _ := proxyReactSvc(t)
			reactAnswers(up, proxyAnswer{status: http.StatusUnauthorized}, proxyAnswer{status: c.status})

			resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("the call answered %d, want the upstream's own 401 (%s)", resp.StatusCode, ans)
			}
			if n := up.count(); n != 2 {
				t.Fatalf("the upstream saw %d calls, want the call and the confirmation read: %+v", n, up.calls())
			}
			if mark, ok := stub.Marks[proxyNamedToken]; ok && !mark.Available() {
				t.Errorf("the token's status is %+v, want nothing marked: a %d confirms no 401", mark, c.status)
			}
			if got := auditField(s.onlyAuditLine(t), "status"); got != StatusOK {
				t.Errorf("the line's status is %q, want %q: nothing was done about this answer", got, StatusOK)
			}
		})
	}
}

// TestProxy429MarksThrottledWithRetryAfter: a 429 is the one mark this route
// writes from the upstream's own answer (D2356). The token is held until the
// reset the answer named, backed off when it named none, and a request no token
// can serve is refused with HTTP 429 and the wait rather than with the 400 every
// other refusal of this route is (R14).
func TestProxy429MarksThrottledWithRetryAfter(t *testing.T) {
	t.Run("a 429 holds the token until the reset it named", func(t *testing.T) {
		s, up, stub, _ := proxyReactSvc(t)
		reactAnswerEveryWay(up, proxyAnswer{
			status: http.StatusTooManyRequests,
			header: http.Header{"Retry-After": []string{"600"}},
		})

		resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("the call answered %d, want the upstream's own 429 (%s)", resp.StatusCode, ans)
		}
		if n := up.count(); n != 1 {
			t.Errorf("the upstream saw %d calls, want the call alone: a 429 owes no confirmation read", n)
		}
		mark := stub.Marks[proxyNamedToken]
		if mark.State != bbtokens.StateExhausted || !mark.Until.Equal(s.now.Add(600*1e9)) {
			t.Errorf("the token's status is %+v, want exhausted until %v (Retry-After: 600)", mark, s.now.Add(600*1e9))
		}
		if got := auditField(s.onlyAuditLine(t), "status"); got != ReasonExhausted {
			t.Errorf("the line's status is %q, want %q: the mark the reaction left", got, ReasonExhausted)
		}
	})

	t.Run("a 429 that named none backs the token off until this side's own wait", func(t *testing.T) {
		canonical := testCanonical(t, testBitbucketRepo)
		s, up, stub, _ := proxyReactSvc(t)
		reactAnswerEveryWay(up, proxyAnswer{status: http.StatusTooManyRequests})

		resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("the call answered %d, want the upstream's own 429 (%s)", resp.StatusCode, ans)
		}
		// The policy the fixture publishes writes no backoff block, so the wait
		// this side answers with is the default one (projectpolicy).
		wait := projectpolicy.DefaultBackoffInitial
		mark := stub.Marks[proxyNamedToken]
		if mark.State != bbtokens.StateBackedOff || !mark.Until.Equal(s.now.Add(wait)) {
			t.Fatalf("the token's status is %+v, want backed off until %v: a hold of this side's own carries an end like any other, and a hold with no end never lapses (the zero Until is a broken token's, bbtokens.Status.Until)", mark, s.now.Add(wait))
		}

		// The end is what the hold is worth: the next request, which the
		// selection refuses because the only token covering it is held, is
		// answered with 429 and the wait this reaction wrote — and not with no
		// Retry-After at all, which is what a hold with no end leaves T9's own
		// throttledHold stating.
		stub.Err = &bbtokens.Refusal{Class: bbtokens.ClassThrottled, Repo: canonical}
		resp, ans = s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
		proxyRefused(t, resp, ans, http.StatusTooManyRequests, ErrorExhaustedUntil, "throttled")
		if got := resp.Header.Get("Retry-After"); got != strconv.Itoa(int(wait.Seconds())) {
			t.Errorf("the refusal's Retry-After is %q, want the hold's own end (%d)", got, int(wait.Seconds()))
		}
	})

	t.Run("a request no token can serve is refused with 429 and the wait", func(t *testing.T) {
		canonical := testCanonical(t, testBitbucketRepo)
		s, up, stub, _ := proxyReactSvc(t)
		reactAnswerEveryWay(up, proxyAnswer{status: http.StatusOK, body: []byte("{}")})
		// A dispatch change (a field read per call), so a later sub-test cannot
		// see this one's hold.
		stub.Err = &bbtokens.Refusal{Class: bbtokens.ClassThrottled, Repo: canonical}
		stub.Marks = map[bbtokens.Name]bbtokens.Status{
			proxyNamedToken: {State: bbtokens.StateExhausted, Until: s.now.Add(300 * 1e9)},
		}

		resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
		proxyRefused(t, resp, ans, http.StatusTooManyRequests, ErrorExhaustedUntil, "throttled")
		if got := resp.Header.Get("Retry-After"); got != "300" {
			t.Errorf("the refusal's Retry-After is %q, want the hold the store states (300)", got)
		}
		if n := up.count(); n != 0 {
			t.Errorf("the upstream saw %d calls, and a request no token can serve is refused before the forward", n)
		}
		line := s.onlyAuditLine(t)
		if got := auditField(line, "status"); got != ErrorExhaustedUntil {
			t.Errorf("the line's status is %q, want %q: the refusal's own wire type", got, ErrorExhaustedUntil)
		}
		if got := auditField(line, "credential"); got != config.BitbucketSecret {
			t.Errorf("the line names the credential %q, and a request refused before a token was chosen names the installation's own key", got)
		}
		if n := len(stub.Counted); n != 0 {
			t.Errorf("the token was counted %d times, and a request that was never served is not counted (D2356)", n)
		}
	})
}

// TestProxy403EvictsAndMarksNothing: a 403 or a 404 drops the resolver's cached
// match for the pairing's repository and marks nothing (R2, R4). The two say
// one thing about the cache — wherever it placed this repository, or whatever it
// said about the token reaching it, is not true — and the next call looks it up
// again. Neither is a mark: the token is not dead and not held.
//
// A 403 answering a WRITE is Bitbucket refusing a scope the token never proved
// (D2353), and that class goes on the service's own log line, which is where
// every class of this route is readable: the answer the run gets stays
// Bitbucket's own (git reports a status and not a body), and the audit line's
// status is the wire's own closed vocabulary, which carries no class.
func TestProxy403EvictsAndMarksNothing(t *testing.T) {
	canonical := testCanonical(t, testBitbucketRepo)

	for _, c := range []struct {
		name   string
		status int
	}{
		{"a read Bitbucket refuses", http.StatusForbidden},
		{"a repository Bitbucket does not have", http.StatusNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, up, stub, _ := proxyReactSvc(t)
			reactAnswerEveryWay(up, proxyAnswer{status: c.status})

			resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
			if resp.StatusCode != c.status {
				t.Fatalf("the call answered %d, want the upstream's own %d (%s)", resp.StatusCode, c.status, ans)
			}
			if got := stub.Evicted; len(got) != 1 || got[0] != canonical {
				t.Errorf("the resolver was asked to forget %v, want the pairing's repository %q once", got, canonical)
			}
			if len(stub.Marks) != 0 {
				t.Errorf("the store holds %v, and neither a 403 nor a 404 marks a token (R4)", stub.Marks)
			}
			line := s.onlyAuditLine(t)
			if got := auditField(line, "status"); got != StatusOK {
				t.Errorf("the line's status is %q, want %q: the reaction is the eviction and nothing was marked", got, StatusOK)
			}
			if logged := s.logs.String(); strings.Contains(logged, string(bbtokens.ClassWriteScopeMissing)) {
				t.Errorf("the service's own log names the write scope for a %d answered to a read: %s", c.status, logged)
			}
		})
	}

	t.Run("a write Bitbucket refuses for its scope carries the class", func(t *testing.T) {
		s, up, stub, _ := proxyReactSvc(t)
		reactAnswerEveryWay(up, proxyAnswer{status: http.StatusForbidden})

		body := proxyUpdateRequest("refs/heads/"+s.proxyBranch()) + "PACK"
		resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte(body))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("the push answered %d, want the upstream's own 403 (%s)", resp.StatusCode, ans)
		}
		if got := stub.Evicted; len(got) != 1 || got[0] != canonical {
			t.Errorf("the resolver was asked to forget %v, want the pairing's repository %q once", got, canonical)
		}
		if len(stub.Marks) != 0 {
			t.Errorf("the store holds %v, and a 403 marks no token", stub.Marks)
		}
		if logged := s.logs.String(); !strings.Contains(logged, "class="+string(bbtokens.ClassWriteScopeMissing)) {
			t.Errorf("the service's own log does not carry the class %q, which is where a class of this route is read: %s", bbtokens.ClassWriteScopeMissing, logged)
		}
		if got := auditField(s.onlyAuditLine(t), "status"); got != StatusOK {
			t.Errorf("the line's status is %q, want %q: the answer is passed through as it is (R14)", got, StatusOK)
		}
	})
}

// TestProxyPassesTheUpstreamStatusThrough: whatever Bitbucket answers, the
// caller gets that answer and that status (R14). A 404 is an ordinary answer —
// a missing pull request, a file that is not there — and so is a 5xx: this route
// reacts to what it is told and never rewrites it for the run.
//
// A service with no store wired reacts to nothing at all, which is the legacy
// single-token installation: its requests are answered exactly as they were
// before this task, and each still writes one line.
func TestProxyPassesTheUpstreamStatusThrough(t *testing.T) {
	const answer = `{"the":"upstream's own body"}`

	for _, status := range []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusNotFound,
		http.StatusGone,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, up, stub, _ := proxyReactSvc(t)
			reactAnswerEveryWay(up, proxyAnswer{status: status, body: []byte(answer)})

			resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
			if resp.StatusCode != status {
				t.Fatalf("the call answered %d, want the upstream's own %d", resp.StatusCode, status)
			}
			if string(ans) != answer {
				t.Errorf("the answer is %q, want the upstream's own body: this route relays it and never rewrites it", ans)
			}
			if got := auditField(s.onlyAuditLine(t), "status"); got != StatusOK {
				t.Errorf("the line's status is %q, want %q: a %d earns no mark", got, StatusOK, status)
			}
			if len(stub.Marks) != 0 {
				t.Errorf("the store holds %v, and a %d marks no token", stub.Marks, status)
			}
			if got := len(stub.Counted); got != 1 {
				t.Errorf("the token was counted %d times, want once per request served with it (D2356)", got)
			}
		})
	}

	t.Run("a service with no store wired reacts to nothing", func(t *testing.T) {
		s, up, _ := proxySvc(t)
		reactAnswerEveryWay(up, proxyAnswer{status: http.StatusForbidden})

		resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("the call answered %d, want the upstream's own 403 (%s)", resp.StatusCode, ans)
		}
		if n := up.count(); n != 1 {
			t.Errorf("the upstream saw %d calls, want the call alone: a route with no store has nothing to react through", n)
		}
		if got := auditField(s.onlyAuditLine(t), "status"); got != StatusOK {
			t.Errorf("the line's status is %q, want %q: the release behind's behaviour is unchanged", got, StatusOK)
		}
	})
}

// TestProxyReactionAuditsOnce: each request writes exactly ONE audit line, and
// that line is what records the reaction (G7, R14). A reaction is never a second
// line, and never a changed status on the answer the run gets: a mark and an
// eviction are facts about a credential, and the run is told what bitbucket.org
// answered.
func TestProxyReactionAuditsOnce(t *testing.T) {
	s, up, stub, _ := proxyReactSvc(t)
	reactAnswerEveryWay(up, proxyAnswer{
		status: http.StatusTooManyRequests,
		header: http.Header{"Retry-After": []string{"120"}},
	})

	resp, ans := s.proxyCall(t, http.MethodGet, reactFetchPath(), nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the call answered %d, want the upstream's own 429 (%s)", resp.StatusCode, ans)
	}
	line := s.onlyAuditLine(t)
	if line["status"] != ReasonExhausted || line["purpose"] != PurposeGit {
		t.Errorf("the line = %v, want one git line whose status is the mark the reaction left", line)
	}
	if got := auditField(line, "credential"); got != string(proxyNamedToken) {
		t.Errorf("the line names the credential %q, want the token the request was served with (%q)", got, proxyNamedToken)
	}
	if line["run"] != s.run || line["job"] != s.job {
		t.Errorf("the line = %v, want the run %q and the job %q", line, s.run, s.job)
	}
	if got := stub.Counted; len(got) != 1 || got[0] != proxyNamedToken {
		t.Errorf("the store counted %v, want the token the request was served with, once", got)
	}
}

// reactCtxStore is the contract's stub with the two things the stub does not
// have, and the test below needs both.
//
// It HONOURS the context it is handed, so a test can hold the reaction's read
// and write to the context they were made on. A stub that answered from its map
// whatever the context said would confirm a mark taken for a caller that has
// hung up, which is the state the finding is about — and the real store is a
// database call that a context can cancel.
//
// And it is SAFE FOR CONCURRENT USE. The service's handler goroutine is still
// working after the caller hangs up — that is the whole point of the test, and
// it is the only test here where the caller goes away mid-call — so the mark it
// writes and the count it appends land on the stub WHILE the test goroutine is
// reading them. bbtokens.Stub guards neither its map nor its slice (stub.go):
// it is a stub for calls a test makes one at a time, and it says so. A test that
// read them directly, as the others here do across a call that has already
// returned, would be racing exactly the two writes it is waiting for — which CI
// catches, because CI runs the package under -race.
//
// So every call into the stub takes this mutex, and the test seeds and reads it
// through the two helpers below, which take it too.
//
// It answers the selection, the resolver and the status store, because it is
// the one value the service holds (T8's seam): the service is wired with this
// wrapper, and the test reads the mark off the stub inside it.
type reactCtxStore struct {
	*bbtokens.Stub
	mu sync.Mutex
}

func (c *reactCtxStore) Read(ctx context.Context, name bbtokens.Name) (bbtokens.Status, error) {
	if err := ctx.Err(); err != nil {
		return bbtokens.Status{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Stub.Read(ctx, name)
}

func (c *reactCtxStore) Write(ctx context.Context, name bbtokens.Name, st bbtokens.Status) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Stub.Write(ctx, name, st)
}

func (c *reactCtxStore) CountRequest(ctx context.Context, name bbtokens.Name) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Stub.CountRequest(ctx, name)
}

func (c *reactCtxStore) Evict(canonical string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Stub.Evict(canonical)
}

// seed puts the status the store holds for a token beside the mark the
// reaction is about to write, under the same lock the reaction's own read takes.
func (c *reactCtxStore) seed(name bbtokens.Name, st bbtokens.Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Marks == nil {
		c.Marks = make(map[bbtokens.Name]bbtokens.Status)
	}
	c.Marks[name] = st
}

// snapshot is what the store holds for one token: the mark and a copy of the
// tokens it has counted. Both are read in one locked step, so the value a test
// asserts is never half of one write and half of the next, and the copy is what
// lets the assertion be made outside the lock.
func (c *reactCtxStore) snapshot(name bbtokens.Name) (bbtokens.Status, []bbtokens.Name) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Marks[name], append([]bbtokens.Name(nil), c.Counted...)
}

// proxyCallDetached sends one call of the proxy route on a context the test
// owns and reads nothing of the answer: a client that hangs up mid-call is owed
// no answer, and what the test measures is what the service recorded. proxyCall
// cannot be used for it because proxyCall fails the test on a transport error,
// and a transport error is exactly what the hang-up makes (probe_test.go's
// callDetached makes the same call for the model route).
func (s *svc) proxyCallDetached(t *testing.T, ctx context.Context, method, path string, body []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, s.http.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken(proxyClaims(s.proxyBranch())...))
	req.Header.Set(HeaderRunJob, s.job)
	resp, err := s.http.Client().Do(req)
	if err != nil {
		// The client's own context ended, which is the disconnect this test
		// makes. Anything else is the fixture.
		if ctx.Err() == nil {
			t.Fatalf("the call failed for a reason that is not the disconnect: %v", err)
		}
		return
	}
	resp.Body.Close()
}

// TestProxyReactionSurvivesACallerThatHangsUp: the run hangs up while the 401
// is being confirmed, and the mark the request's own audit line records as
// taken is still written — with the facts the store held beside it.
//
// The disconnect is made from inside the upstream, at the confirmation read,
// and the answer is held a moment after it so the hang-up has certainly landed
// by the time the store is touched (probe_test.go's
// TestProbeSurvivesAClientDisconnect makes the same disconnect for the probe).
// The store is the stub under reactCtxStore, which fails a call made on a
// context that has ended, so an un-detached read or write leaves nothing here —
// and BOTH are needed: a detached write over a read that failed would write the
// mark without the identity and the count the store held (R12, D2356), which
// the live caller's control pins as the shape a mark must have.
//
// The handler goroutine outlives the caller here, so the store is read under
// the wrapper's own lock (seed, snapshot) and what the test waits for is BOTH
// halves of what the request leaves behind: the count is written after the
// answer is copied to the caller (forwardBitbucket's tail), so a mark that has
// landed does not yet mean the count has.
func TestProxyReactionSurvivesACallerThatHangsUp(t *testing.T) {
	for _, c := range []struct {
		name   string
		hangUp bool
	}{
		{"the caller hangs up while the 401 is confirmed", true},
		{"the caller stays", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &reactCtxStore{Stub: &bbtokens.Stub{Selected: proxyNamedToken}}
			s, up, _, named := proxyTokensSvc(t, store)
			store.seed(proxyNamedToken, bbtokens.Status{VerifiedAt: s.now, Identity: "uuid-ci", Count: 7})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			up.mu.Lock()
			up.answer = func(r *http.Request) proxyAnswer {
				if c.hangUp && r.URL.Path == reactConfirmPath {
					once.Do(cancel)
					time.Sleep(20 * time.Millisecond)
				}
				return proxyAnswer{status: http.StatusUnauthorized}
			}
			up.mu.Unlock()

			s.proxyCallDetached(t, ctx, http.MethodGet, reactFetchPath(), nil)

			waitFor(t, "the reaction's mark and count to be written", func() bool {
				mark, counted := store.snapshot(proxyNamedToken)
				return mark.State == bbtokens.StateBroken && len(counted) == 1
			})
			mark, counted := store.snapshot(proxyNamedToken)
			want := bbtokens.Status{State: bbtokens.StateBroken, VerifiedAt: s.now, Identity: "uuid-ci", Count: 7}
			if mark != want {
				t.Errorf("the store holds %+v, want %+v: the mark is taken whatever the caller does, and it carries the facts the store held (R12, D2356)", mark, want)
			}
			if len(counted) != 1 || counted[0] != proxyNamedToken {
				t.Errorf("the store counted %v, want the token the request was served with, once", counted)
			}
			reactNoTokenValue(t, s, nil, named)
		})
	}
}
