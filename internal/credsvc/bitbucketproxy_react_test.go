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
	"net/http"
	"strconv"
	"strings"
	"testing"

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
