package credsvc

// The Bitbucket proxy route's tests (B1.5): the one route of this service that
// carries a run's git traffic and its Bitbucket REST traffic to bitbucket.org
// with the bundle's token.
//
// Three properties are what every test here is a case of, and they are the
// three the route exists for:
//
//   - a request the rules refuse reaches bitbucket.org as ZERO bytes. The
//     push whose ref is not the run's is the sharp one (the plan's red step):
//     the ref is read in front of the pack (G2) and the refusal is taken
//     before any byte of the body is forwarded, which the fake upstream
//     proves by having seen nothing at all.
//   - the upstream call is addressed from what internal/gitproxy decided and
//     from nothing the caller sent (G13): the host is one of the adapter's
//     own two constants (pinned by a transport that refuses any other), and
//     the path is rebuilt — a caller's spelling cannot move a ref nobody
//     checked.
//   - the caller's credential never travels and the bundle's always does: the
//     Authorization header the run presented is its job's Google ID token,
//     and what bitbucket.org is shown is the bundle's token in the scheme the
//     call's own protocol uses (`x-token-auth` Basic for git, Bearer for the
//     REST API).
//
// Every credential in this file is built at run time (fakeCred, through the
// fixture's bundle) and no literal here is, or looks like, one (Convention 2).
// The object ids are git's own shape and carry nothing.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitproxy"
)

// The names these tests stand on. The workspace and the repository are the
// read mint's own (bitbucket_test.go), so the two Bitbucket routes of this
// service are tested against one installation's names rather than two.
const (
	// proxyOtherRef is a ref the run may not move, and the marker a refusal
	// must not echo.
	proxyOtherRef = "refs/heads/not-this-runs-branch"
	// proxyRefMarker is the part of proxyOtherRef a test searches the audit
	// line and the service's own log for: a refusal names the CLASS of what it
	// refused and never the caller's text (G7).
	proxyRefMarker = "not-this-runs-branch"
)

// proxyOIDs are two object ids of git's own shape, for the update requests
// below. They are object ids and not credentials: a push's ids are public the
// moment the pack lands.
const (
	proxyOldOID = "1111111111111111111111111111111111111111"
	proxyNewOID = "2222222222222222222222222222222222222222"
)

// proxyBranch is the branch the fixture's run pushed (the fb claim): the one
// ref its push may move, and the source a pull-request write must name. It is
// derived from the run id (task.BranchRE's form is fugaro/<run id>) rather
// than spelled, because the branch and the run the identity carries are one
// fact and the verifier holds them to each other.
func (s *svc) proxyBranch() string { return "fugaro/" + s.run }

// proxyPath is one call of the route, spelled as the runner's forwarder
// spells it (B1.6): the route's prefix and then the caller's own path.
func proxyPath(rest string) string { return BitbucketProxyPrefix + rest }

// proxyGitPath is the git path of the fixture's repository, as the runner's
// own remote names it.
func proxyGitPath(action string) string {
	return testBitbucketRepo + ".git/" + action
}

// proxyRESTPath is one REST path of the fixture's repository, under the API
// root the adapter's BaseURL carries.
func proxyRESTPath(rest string) string {
	return "2.0/repositories/" + testBitbucketRepo + rest
}

// pktLine is one git pkt-line. The tests build the update request's bytes
// themselves because that is what the wire carries: the runner forwards git's
// own bytes and never re-encodes them.
func pktLine(payload string) string { return fmt.Sprintf("%04x", len(payload)+4) + payload }

// proxyUpdateRequest is a receive-pack update request moving one ref, as git
// writes it: the update line and then the flush packet that ends the section.
func proxyUpdateRequest(ref string) string {
	return pktLine(proxyOldOID+" "+proxyNewOID+" "+ref) + "0000"
}

// ---------------------------------------------------------------------------
// The fake upstream
// ---------------------------------------------------------------------------

// proxyUpstream stands in for bitbucket.org and api.bitbucket.org: the two
// hosts a proxied call may reach (G13, O4). Every request is recorded with the
// host, the scheme and the path the SERVICE built — before the hop is
// rewritten to a loopback listener — and every request to any other host is
// refused rather than answered, which is what pins the two hosts: an
// owner-listed API root is not served (the plan's default for O4), so a call
// the route addressed anywhere else fails here.
type proxyUpstream struct {
	srv *httptest.Server

	mu     sync.Mutex
	seen   []proxySeen
	answer func(*http.Request) proxyAnswer
}

// proxySeen is one call the upstream was asked to serve, as it arrived.
type proxySeen struct {
	method string
	scheme string
	host   string
	path   string
	query  string
	header http.Header
	auth   string
	body   []byte
}

// proxyAnswer is what the fake answers one call with. The zero value is an
// empty 200, which is what every test that only cares about what was SENT
// wants.
type proxyAnswer struct {
	status int
	header http.Header
	body   []byte
}

func newProxyUpstream(t *testing.T) *proxyUpstream {
	t.Helper()
	p := &proxyUpstream{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		answer := p.answer
		p.mu.Unlock()
		a := proxyAnswer{}
		if answer != nil {
			a = answer(r)
		}
		if a.status == 0 {
			a.status = http.StatusOK
		}
		for k, vs := range a.header {
			w.Header()[k] = vs
		}
		w.WriteHeader(a.status)
		_, _ = w.Write(a.body)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// client is the HTTP client the service under test makes its upstream calls
// with: every call is sent to the fake whatever host the service built the URL
// for, and a call to a host that is not one of Bitbucket's two is refused.
func (p *proxyUpstream) client() *http.Client {
	base, err := url.Parse(p.srv.URL)
	if err != nil {
		panic("the fake's URL is not a URL: " + err.Error())
	}
	return &http.Client{Transport: &proxyTransport{base: base, up: p}}
}

type proxyTransport struct {
	base *url.URL
	up   *proxyUpstream
}

func (h *proxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.Host {
	case "bitbucket.org", "api.bitbucket.org":
	default:
		return nil, fmt.Errorf("the proxy addressed %s://%s, and a proxied call reaches bitbucket.org or api.bitbucket.org alone", r.URL.Scheme, r.URL.Host)
	}
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		_ = r.Body.Close()
	}
	h.up.record(proxySeen{
		method: r.Method,
		scheme: r.URL.Scheme,
		host:   r.URL.Host,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		header: r.Header.Clone(),
		auth:   r.Header.Get("Authorization"),
		body:   body,
	})
	c := r.Clone(r.Context())
	c.URL.Scheme = h.base.Scheme
	c.URL.Host = h.base.Host
	c.Host = h.base.Host
	if r.Body != nil {
		c.Body = io.NopCloser(bytes.NewReader(body))
		c.ContentLength = int64(len(body))
	}
	return http.DefaultTransport.RoundTrip(c)
}

func (p *proxyUpstream) record(s proxySeen) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, s)
}

// calls is what the upstream was asked, in order.
func (p *proxyUpstream) calls() []proxySeen {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]proxySeen(nil), p.seen...)
}

// count is how many calls reached the upstream at all. Zero is the number the
// refusals that must not forward a byte are held to.
func (p *proxyUpstream) count() int { return len(p.calls()) }

// only is the one call the upstream saw, failing the test when it saw none or
// more than one.
func (p *proxyUpstream) only(t *testing.T) proxySeen {
	t.Helper()
	got := p.calls()
	if len(got) != 1 {
		t.Fatalf("the upstream saw %d calls, want exactly 1", len(got))
	}
	return got[0]
}

// answerWith makes the fake answer every call the given way.
func (p *proxyUpstream) answerWith(status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = func(*http.Request) proxyAnswer { return proxyAnswer{status: status, body: []byte(body)} }
}

// ---------------------------------------------------------------------------
// The installation under test
// ---------------------------------------------------------------------------

// proxyPolicyDoc is the published policy of an installation that PROXIES this
// repository: the block admits it and the installation's switch is on, which
// is the two-legged admission Decide makes (G3, G9).
func proxyPolicyDoc(t *testing.T) string {
	t.Helper()
	return `{
		"version": 4,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testCanonical(t, testBitbucketRepo) + `"],
		"accounts": {"default": {"allow_repos": ["` + testCanonical(t, testBitbucketRepo) + `"]}},
		"credentials": {"fallback": "auto"},
		"bitbucket_token": {"allow_repos": ["` + testCanonical(t, testBitbucketRepo) + `"], "proxy": true}
	}`
}

// proxyBundleDoc is the installation's bundle with the upstream Bitbucket
// token in it (D506): the credential the proxy forwards with. The token is
// returned as well, so a test can hold what the upstream was shown to the
// bundle's own value without either being written down.
func proxyBundleDoc(t *testing.T) (doc []byte, token string) {
	t.Helper()
	token = fakeCred(t, "bitbucket")
	return append(testBundleDoc(t), []byte(config.BitbucketSecret+": "+token+"\n")...), token
}

// proxySvc is the installation under test, wired to proxy the fixture's
// Bitbucket repository: its job account is bound to that repository, its
// policy admits it with the proxy switch on, its bundle holds the token, and
// its client sends every upstream call to the fake above.
//
// The changes are made on the CONFIG and not on the fixture afterwards,
// because NewServer takes the Config by value (bitbucketSvcWith's rule).
func proxySvc(t *testing.T, mutate ...func(*svc, *Config)) (*svc, *proxyUpstream, string) {
	t.Helper()
	up := newProxyUpstream(t)
	doc, token := proxyBundleDoc(t)
	slug := bitbucketSlug(t, testBitbucketRepo)
	canonical := testCanonical(t, testBitbucketRepo)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		s.slug = slug
		cfg.JobAccounts = []JobAccount{{Email: testJobSA, Slug: slug, Repo: canonical}}
		cfg.Repos = map[string]string{slug: canonical}
		cfg.Client = up.client()
		pol := &fakePolicy{shared: decodeTestPolicy(t, proxyPolicyDoc(t)), sharedFound: true}
		cfg.Policy, s.policy = pol, pol
		s.src.docs = [][]byte{doc}
		for _, m := range mutate {
			m(s, cfg)
		}
	})
	return s, up, token
}

// proxyClaims is the run identity's claims: the branch the run pushes (fb) and
// the pull requests it may write (fpr). A caller that presents a run token is
// presenting these and nothing else of its own.
func proxyClaims(branch string, prs ...int) []func(map[string]any) {
	return []func(map[string]any){
		func(c map[string]any) { c["fb"] = branch },
		// fpr is an array and never null: a claim that is present and null is
		// not the array the rule reads, and a first run's empty list is what
		// the launcher writes for a run that writes no pull request.
		func(c map[string]any) { c["fpr"] = append([]int{}, prs...) },
	}
}

// proxyCall sends one call of the proxy route as the fixture's run: the job's
// Google ID token in Authorization, the run identity in its own header and the
// job name the run reports.
func (s *svc) proxyCall(t *testing.T, method, path string, body []byte, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.proxyCallAs(t, method, path, body, s.runToken(proxyClaims(s.proxyBranch())...), s.jobToken(), s.job, mutate...)
}

// proxyCallAs is proxyCall with the run token given, for the calls that
// present an identity the service refuses.
func (s *svc) proxyCallAs(t *testing.T, method, path string, body []byte, runTok, jobTok, job string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, s.http.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+jobTok)
	req.Header.Set(HeaderRunIdentity, runTok)
	req.Header.Set(HeaderRunJob, job)
	for _, m := range mutate {
		m(req)
	}
	resp, err := s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	defer resp.Body.Close()
	ans, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, ans
}

// proxyRefused is the refusal a test expects of one call: its status, its wire
// error type, and the class its sentence names.
func proxyRefused(t *testing.T, resp *http.Response, body []byte, wantStatus int, wantType, wantClass string) WireError {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("the call answered %d, want %d (%s)", resp.StatusCode, wantStatus, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != wantType {
		t.Errorf("the refusal's type = %q, want %q", e.Error, wantType)
	}
	if !strings.Contains(e.Message, wantClass) {
		t.Errorf("the refusal's sentence = %q, and it does not name the class %q", e.Message, wantClass)
	}
	return e
}

// auditLines is every audit line the service wrote, as the JSON objects they
// are (the fixture's own handler for C1.13's lines).
func (s *svc) auditLines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.audit.String()), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("the audit line is not JSON: %v (%s)", err, line)
		}
		out = append(out, ev)
	}
	return out
}

// auditField reads one field of an audit line as a string. A field the line
// does not carry reads as empty, which is how a line that names neither a
// purpose nor a credential is asserted (the two are written as empty strings
// rather than omitted: the event's fields are the contract's, and a reader
// that had to tell "absent" from "empty" would be reading a second contract).
func auditField(line map[string]any, key string) string {
	s, _ := line[key].(string)
	return s
}

// onlyAuditLine is the one line a single request must leave (G7), failing the
// test when a request wrote none or more than one.
func (s *svc) onlyAuditLine(t *testing.T) map[string]any {
	t.Helper()
	lines := s.auditLines(t)
	if len(lines) != 1 {
		t.Fatalf("the request wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	return lines[0]
}

// ---------------------------------------------------------------------------
// The tests
// ---------------------------------------------------------------------------

// TestBitbucketRoutePushRefusedBeforeAnyPackByte is this task's red step.
// Before this route existed the call was a 404; the property it pins is G2's:
// a push is decided on the refs its update request names, and a ref the run
// does not own is refused BEFORE any byte of the body reaches bitbucket.org.
// The fake upstream having seen NOTHING is the whole of the claim — not a
// pack that was refused after the first buffer, but a call that was never
// made.
func TestBitbucketRoutePushRefusedBeforeAnyPackByte(t *testing.T) {
	s, up, _ := proxySvc(t)
	body := proxyUpdateRequest(proxyOtherRef) + "PACK"

	resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte(body))
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonOtherRef)

	if n := up.count(); n != 0 {
		t.Fatalf("the upstream saw %d calls, and a refused push must forward no byte: %+v", n, up.calls())
	}
}

// TestBitbucketRouteForwardsFetch: an admitted fetch is forwarded, and what
// bitbucket.org is shown is the rebuilt URL and the bundle's token — never the
// caller's Google ID token.
func TestBitbucketRouteForwardsFetch(t *testing.T) {
	s, up, token := proxySvc(t)
	up.answerWith(http.StatusOK, "001e# service=git-upload-pack\n0000")

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	if string(ans) != "001e# service=git-upload-pack\n0000" {
		t.Errorf("the answer = %q, and the upstream's bytes must be relayed unchanged", ans)
	}
	got := up.only(t)
	if got.host != "bitbucket.org" || got.scheme != "https" {
		t.Errorf("the fetch was addressed to %s://%s, want https://bitbucket.org", got.scheme, got.host)
	}
	if want := "/" + proxyGitPath("info/refs"); got.path != want {
		t.Errorf("the fetch's upstream path = %q, want %q", got.path, want)
	}
	if got.query != "service=git-upload-pack" {
		t.Errorf("the fetch's upstream query = %q, want service=git-upload-pack", got.query)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(bitbucket.GitUsername+":"+token))
	if got.auth != wantAuth {
		t.Errorf("the upstream was shown %q as its credential, want the bundle's token as git's own x-token-auth Basic", got.auth)
	}
	// Nothing of the caller's own identity travels with the call: the run
	// identity and the job name are this contract's headers, and the job's
	// Google ID token is the credential the caller presented — which the
	// Authorization assertion above reads as overwritten rather than merely
	// dropped, so the two together are the whole of "the caller's credentials
	// are dropped" (dropRequestHeaders is the belt to this brace).
	for _, h := range []string{HeaderRunIdentity, HeaderRunJob} {
		if v := got.header.Get(h); v != "" {
			t.Errorf("the upstream was shown %s: %q, and this contract's own headers never leave the process", h, v)
		}
	}

	line := s.onlyAuditLine(t)
	if line["purpose"] != PurposeGit || line["credential"] != config.BitbucketSecret || line["status"] != StatusOK {
		t.Errorf("the audit line = %v, want the git purpose, the bundle's credential and ok", line)
	}
	if line["run"] != s.run || line["repository"] != s.slug {
		t.Errorf("the audit line = %v, want the run %q of the repository %q", line, s.run, s.slug)
	}
}

// TestBitbucketRouteForwardsOnlyTheCallsOwnQuery: the two list calls carry a
// query, and it is the CLASSIFIED query that reaches bitbucket.org — Classify
// keeps the parameters the call's own definition allows and refuses a query
// that carries any other, so a caller cannot append a parameter and have the
// upstream act on it. The refusal is asserted to forward no byte: a refused
// query is a refused call and not a call with its query trimmed.
func TestBitbucketRouteForwardsOnlyTheCallsOwnQuery(t *testing.T) {
	s, up, _ := proxySvc(t)
	up.answerWith(http.StatusOK, `{"values":[]}`)

	// A parameter this call does not define: the pull-request list carries q
	// alone, so pagelen is not the caller's to send.
	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyRESTPath("/pullrequests"))+"?q=state%3D%22OPEN%22&pagelen=10", nil)
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonCallNotAllowed)
	if n := up.count(); n != 0 {
		t.Fatalf("the upstream saw %d calls, and a refused query must forward none: %+v", n, up.calls())
	}

	// The call's own parameter: forwarded, and unchanged.
	resp, ans = s.proxyCall(t, http.MethodGet, proxyPath(proxyRESTPath("/pullrequests"))+"?q=state%3D%22OPEN%22", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the list answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	// The comment list's own two parameters, on the path its call rebuilds.
	resp, ans = s.proxyCall(t, http.MethodGet, proxyPath(proxyRESTPath("/pullrequests/7/comments"))+"?pagelen=20&page=2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the comment list answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	calls := up.calls()
	if len(calls) != 2 {
		t.Fatalf("the upstream saw %d calls, want the two admitted list calls: %+v", len(calls), calls)
	}
	if want := "/2.0/repositories/" + testBitbucketRepo + "/pullrequests"; calls[0].path != want {
		t.Errorf("the list was addressed to %q, want %q", calls[0].path, want)
	}
	if calls[0].query != "q=state%3D%22OPEN%22" {
		t.Errorf("the list's upstream query = %q, want the classified q and nothing else", calls[0].query)
	}
	if want := "/2.0/repositories/" + testBitbucketRepo + "/pullrequests/7/comments"; calls[1].path != want {
		t.Errorf("the comment list was addressed to %q, want %q", calls[1].path, want)
	}
	if calls[1].query != "page=2&pagelen=20" {
		t.Errorf("the comment list's upstream query = %q, want the call's own paging parameters", calls[1].query)
	}
}

// TestBitbucketRouteForwardsFetchPack: a fetch's second call — the POST that
// carries what the run wants and brings the pack back — is forwarded the same
// way. Its body is git's own negotiation and NOT an update request: this route
// reads refs in front of a PUSH alone (G2), so a fetch body the update parser
// would refuse on is neither parsed nor refused, and every byte of it reaches
// bitbucket.org. A route that read every POST's refs would break every fetch.
func TestBitbucketRouteForwardsFetchPack(t *testing.T) {
	s, up, token := proxySvc(t)
	up.answerWith(http.StatusOK, "PACKDATA")

	body := pktLine("want "+proxyNewOID+" multi_ack_detailed side-band-64k ofs-delta") + "0000" + pktLine("done") + "0000"
	resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-upload-pack")), []byte(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch's pack call answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	if string(ans) != "PACKDATA" {
		t.Errorf("the answer = %q, and the pack the upstream sent must be relayed unchanged", ans)
	}
	got := up.only(t)
	if got.method != http.MethodPost {
		t.Errorf("the upstream saw %s, want POST", got.method)
	}
	if want := "/" + proxyGitPath("git-upload-pack"); got.path != want {
		t.Errorf("the fetch's pack call was addressed to %q, want %q", got.path, want)
	}
	if got.query != "" {
		t.Errorf("the fetch's pack call carried the query %q, want none", got.query)
	}
	if !bytes.Equal(got.body, []byte(body)) {
		t.Errorf("the upstream was sent %q, want the caller's own negotiation %q", got.body, body)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(bitbucket.GitUsername+":"+token))
	if got.auth != wantAuth {
		t.Errorf("the upstream was shown %q as its credential, want the bundle's token as git's own x-token-auth Basic", got.auth)
	}
}

// TestBitbucketRouteUpstreamTimeout: the route's own upstream bound, which is
// the one number the plan names for it (60 minutes, against the model path's
// 30) and the reason behind the route copying the host's client: Check 190's
// 33 MiB push is a call that legitimately takes far longer than a model call
// does, and a push cut at the model path's bound is a push the run cannot
// make. The bound is asserted to be this route's own — the shared client is
// not mutated, which is also what keeps a long push from racing every other
// route's calls.
func TestBitbucketRouteUpstreamTimeout(t *testing.T) {
	s, _, _ := proxySvc(t)
	c := s.server.bitbucketClient()
	if c.Timeout != gitUpstreamTimeout {
		t.Errorf("the route's upstream timeout = %v, want the route's own %v", c.Timeout, gitUpstreamTimeout)
	}
	if gitUpstreamTimeout <= 30*time.Minute {
		t.Errorf("the route's upstream timeout = %v, and it must be longer than the model path's 30 minutes: a push is not a model call", gitUpstreamTimeout)
	}
	if c == s.server.cfg.Client {
		t.Errorf("the route used the host's own client, want a copy carrying this route's bound")
	}
}

// TestBitbucketRouteStreamsLargePack: a push that IS the run's is forwarded
// whole — the update request that was read in front of it and then every byte
// of the pack that follows — because this route holds no pack in memory
// (Check 190's 33 MiB push is what the h2c listener and this streaming exist
// for).
func TestBitbucketRouteStreamsLargePack(t *testing.T) {
	s, up, _ := proxySvc(t)
	up.answerWith(http.StatusOK, "0000")

	head := proxyUpdateRequest("refs/heads/" + s.proxyBranch())
	pack := make([]byte, 1<<20)
	for i := range pack {
		pack[i] = byte(i)
	}
	push := append([]byte(head), pack...)

	resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), push)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the push answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	got := up.only(t)
	if len(got.body) != len(push) {
		t.Fatalf("the upstream received %d bytes of the push, want %d: the section read in front of the pack must be replayed in front of it", len(got.body), len(push))
	}
	if sha256.Sum256(got.body) != sha256.Sum256(push) {
		t.Error("the bytes the upstream received are not the bytes the run sent")
	}
	if line := s.onlyAuditLine(t); line["purpose"] != PurposeGit {
		t.Errorf("the audit line = %v, want the git purpose", line)
	}
}

// TestBitbucketRouteFlushOnlyProbeIsForwarded: git's probe before a large
// chunked push (an update request of only a flush packet) moves no ref and is
// forwarded as the no-op it is (G13, B1.3). It is the one push that is allowed
// with an empty ref list, and the distinction that makes it safe is
// UpdatesRead: a push whose section nobody read has the same empty list and is
// refused.
func TestBitbucketRouteFlushOnlyProbeIsForwarded(t *testing.T) {
	s, up, _ := proxySvc(t)
	up.answerWith(http.StatusOK, "0000")

	resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte("0000"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the probe answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	got := up.only(t)
	if string(got.body) != "0000" {
		t.Errorf("the upstream received %q, want the flush packet the run sent", got.body)
	}
}

// TestBitbucketRouteGzipPushRefused: a push whose body is not identity-encoded
// is refused before it is parsed. Nothing in this service decompresses (B1.3's
// contract says the caller decides this), so a gzip body is not a pkt-line
// stream and a rule that read one would be deciding on bytes it cannot see.
//
// The header is a LIST, and the guard reads all of it: one value, several
// values and a comma-separated value are each a push that was compressed, and
// the case that matters most is "identity" followed by "gzip" — a guard reading
// the header's first value alone would read that body as pkt-lines and forward
// it to bitbucket.org compressed. The other side is on the same body below:
// identity, in either spelling, is admitted.
func TestBitbucketRouteGzipPushRefused(t *testing.T) {
	for _, values := range [][]string{
		{"gzip"},
		{"identity", "gzip"},
		{"gzip, identity"},
		{"identity, gzip"},
	} {
		t.Run(strings.Join(values, " then "), func(t *testing.T) {
			s, up, _ := proxySvc(t)

			resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte("\x1f\x8b\x08\x00gzip-ish"),
				func(r *http.Request) {
					for _, v := range values {
						r.Header.Add("Content-Encoding", v)
					}
				})
			proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, refusalEncodedBody)

			if n := up.count(); n != 0 {
				t.Fatalf("the upstream saw %d calls, and an encoded push must forward no byte", n)
			}
		})
	}

	t.Run("identity is admitted", func(t *testing.T) {
		s, up, _ := proxySvc(t)
		up.answerWith(http.StatusOK, "0000")
		body := proxyUpdateRequest("refs/heads/"+s.proxyBranch()) + "PACK"

		resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte(body),
			func(r *http.Request) {
				r.Header.Add("Content-Encoding", "identity")
				r.Header.Add("Content-Encoding", "IDENTITY")
			})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("an identity-encoded push answered %d, want 200 (%s)", resp.StatusCode, ans)
		}
		if n := up.count(); n != 1 {
			t.Fatalf("the upstream saw %d calls, want the one admitted push", n)
		}
	})
}

// TestBitbucketRouteUpstreamURLRebuilt is G13's property: the upstream URL is
// built from what Classify decided, never from the path the caller sent. The
// two spellings below are the two halves of it — a call whose spelling differs
// from the canonical repository (upper case) still reaches the canonical one,
// and a REST call under the API root reaches the API root this service owns.
func TestBitbucketRouteUpstreamURLRebuilt(t *testing.T) {
	s, up, _ := proxySvc(t)
	up.answerWith(http.StatusOK, "{}")

	// The caller spells the workspace and the repository in another case. The
	// repository the rules decided on is the canonical one, so that is what
	// bitbucket.org is addressed with.
	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath("Workspace-A/Repo-A.git/info/refs?service=git-upload-pack"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the fetch answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	git := up.only(t)
	if want := "/" + testBitbucketRepo + ".git/info/refs"; git.path != want {
		t.Errorf("the upstream path = %q, want %q: the path is rebuilt and never copied", git.path, want)
	}
	if git.query != "service=git-upload-pack" {
		t.Errorf("the upstream query = %q, want the one parameter git's advertisement carries", git.query)
	}

	// A commit read: the revision is part of the call the classification
	// decided on, and it is carried into the rebuilt path (Request.Rev).
	resp, ans = s.proxyCall(t, http.MethodGet, proxyPath(proxyRESTPath("/commit/abcdef1")), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the commit read answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	got := up.calls()[1]
	if got.host != "api.bitbucket.org" {
		t.Errorf("the REST call was addressed to %q, want api.bitbucket.org", got.host)
	}
	if want := "/2.0/repositories/" + testBitbucketRepo + "/commit/abcdef1"; got.path != want {
		t.Errorf("the REST call's upstream path = %q, want %q", got.path, want)
	}

	// A path the classification cannot name reaches bitbucket.org as nothing
	// at all: an encoded separator is the spelling whose meaning depends on
	// the decoder, and it is refused rather than decoded.
	resp, ans = s.proxyCall(t, http.MethodGet, proxyPath("workspace-a%2Frepo-a.git/info/refs?service=git-upload-pack"), nil)
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonPathMalformed)
	if n := up.count(); n != 2 {
		t.Fatalf("the upstream saw %d calls, and a malformed path must reach it as none: %+v", n, up.calls())
	}
	// The refusal of a path that never classified names neither a purpose nor
	// a credential (routeAct's rule for this route): the two facts are the
	// classification's answer, and the service did not have one.
	lines := s.auditLines(t)
	if len(lines) != 3 {
		t.Fatalf("the three calls wrote %d audit lines, want one each: %v", len(lines), lines)
	}
	if auditField(lines[2], "purpose") != "" || auditField(lines[2], "credential") != "" {
		t.Errorf("the line = %v, and a call that never classified names neither purpose nor credential", lines[2])
	}
}

// TestBitbucketRouteNoRedirect: this service follows no redirect — the
// credential it carries is not presented to whatever host an answer names —
// and the destination is dropped from the answer it relays, so the caller
// cannot follow one either. The upstream seeing exactly ONE call is the whole
// of the first half.
func TestBitbucketRouteNoRedirect(t *testing.T) {
	s, up, _ := proxySvc(t)
	up.mu.Lock()
	up.answer = func(*http.Request) proxyAnswer {
		h := http.Header{}
		h.Set("Location", "https://example.invalid/elsewhere")
		return proxyAnswer{status: http.StatusFound, header: h, body: []byte("moved")}
	}
	up.mu.Unlock()

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("the answer's status = %d, want the upstream's own 302 (%s)", resp.StatusCode, ans)
	}
	if got := resp.Header.Get("Location"); got != "" {
		t.Errorf("the answer carries Location %q, and a redirect this service will not make is not one it may hand the caller", got)
	}
	if n := up.count(); n != 1 {
		t.Fatalf("the upstream saw %d calls, and this service follows no redirect: %+v", n, up.calls())
	}
}

// TestBitbucketRoutePRWriteChecksSource is G12's: a pull-request write is
// decided on the pull request THIS SERVICE read with the bundle's token, and
// never on what the caller said the pull request was. The write is refused
// when the source the API reports is not the run's own branch, and it is
// allowed when it is.
func TestBitbucketRoutePRWriteChecksSource(t *testing.T) {
	s, up, token := proxySvc(t)
	write := []byte(`{"title":"the run's work"}`)

	// The facts the API answers for pull request 7: a source branch that is
	// not this run's.
	facts := func(sourceBranch string) {
		up.mu.Lock()
		defer up.mu.Unlock()
		up.answer = func(r *http.Request) proxyAnswer {
			if r.Method != http.MethodGet {
				return proxyAnswer{status: http.StatusOK, body: []byte("{}")}
			}
			body, err := json.Marshal(map[string]any{
				"source": map[string]any{
					"branch":     map[string]any{"name": sourceBranch},
					"repository": map[string]any{"full_name": testBitbucketRepo},
				},
				"destination": map[string]any{"branch": map[string]any{"name": "main"}},
			})
			if err != nil {
				t.Fatalf("building the fake's answer: %v", err)
			}
			return proxyAnswer{status: http.StatusOK, body: body}
		}
	}

	facts("someone-elses-branch")
	resp, ans := s.proxyCall(t, http.MethodPut, proxyPath(proxyRESTPath("/pullrequests/7")), write)
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonOtherPullRequest)

	seen := up.calls()
	if len(seen) != 1 || seen[0].method != http.MethodGet {
		t.Fatalf("the upstream saw %+v, want the facts read alone: a refused write is never forwarded", seen)
	}
	if seen[0].path != "/2.0/repositories/"+testBitbucketRepo+"/pullrequests/7" {
		t.Errorf("the facts were read at %q, want the pull request the write names", seen[0].path)
	}
	if seen[0].auth != "Bearer "+token {
		t.Errorf("the facts were read with %q, want the bundle's token as a REST bearer", seen[0].auth)
	}

	// The same write, with the API reporting the run's own branch.
	up.mu.Lock()
	up.seen = nil
	up.mu.Unlock()
	facts(s.proxyBranch())
	resp, ans = s.proxyCall(t, http.MethodPut, proxyPath(proxyRESTPath("/pullrequests/7")), write)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the write answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	seen = up.calls()
	if len(seen) != 2 || seen[0].method != http.MethodGet || seen[1].method != http.MethodPut {
		t.Fatalf("the upstream saw %+v, want the facts read and then the write", seen)
	}
	lines := s.auditLines(t)
	if len(lines) != 2 {
		t.Fatalf("the two calls wrote %d audit lines, want one each: %v", len(lines), lines)
	}
	if lines[0]["status"] != ErrorRequestRefused || lines[1]["status"] != StatusOK {
		t.Errorf("the lines = %v, want the refused write's and then the allowed one's", lines)
	}
	if lines[1]["purpose"] != PurposeBitbucketAPI || lines[1]["credential"] != config.BitbucketSecret {
		t.Errorf("the audit line = %v, want the Bitbucket API purpose and the bundle's credential", lines[1])
	}

	// A comment on a pull request the RUN'S OWN IDENTITY names (fpr) is
	// allowed even when the pull request is not this run's own work (G12): the
	// run that reviews pull request 9 may comment on it, and the claim — and
	// not the caller's word for it — is what the rules read. It pins that the
	// identity's pull requests reach CheckPRWrite at all.
	up.mu.Lock()
	up.seen = nil
	up.mu.Unlock()
	facts("someone-elses-branch")
	resp, ans = s.proxyCallAs(t, http.MethodPost, proxyPath(proxyRESTPath("/pullrequests/9/comments")),
		[]byte(`{"content":{"raw":"a review note"}}`),
		s.runToken(proxyClaims(s.proxyBranch(), 9)...), s.jobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the comment answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	if seen := up.calls(); len(seen) != 2 || seen[1].path != "/2.0/repositories/"+testBitbucketRepo+"/pullrequests/9/comments" {
		t.Errorf("the upstream saw %+v, want the facts read and then the comment", seen)
	}
}

// TestBitbucketRouteUserCallNamesNoRepository: GET /user is the one admitted
// call that names no repository at all (G4) — the adapter asks it which user
// the token is, so Fugaro's own comments can be told from other people's. The
// route must address it without a repository to rebuild from, and the policy's
// own admission is what decides it.
func TestBitbucketRouteUserCallNamesNoRepository(t *testing.T) {
	s, up, token := proxySvc(t)
	up.answerWith(http.StatusOK, `{"uuid":"{user}"}`)

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath("2.0/user"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d, want 200 (%s)", resp.StatusCode, ans)
	}
	got := up.only(t)
	if got.host != "api.bitbucket.org" || got.path != "/2.0/user" {
		t.Errorf("the call was addressed to %s%s, want api.bitbucket.org/2.0/user", got.host, got.path)
	}
	if got.auth != "Bearer "+token {
		t.Errorf("the call was shown %q, want the bundle's token as a REST bearer", got.auth)
	}
	if line := s.onlyAuditLine(t); line["purpose"] != PurposeBitbucketAPI {
		t.Errorf("the audit line = %v, want the Bitbucket API purpose", line)
	}
}

// TestBitbucketRoutePRFactsUnreadableIsRefused: a write whose pull request
// this service cannot read is REFUSED rather than checked against nothing —
// G12's fail-closed side, and the reason the facts read is required at all.
//
// The two answers the API can give are both cases here, and the first is the
// sharp one: an error status is not facts even beside a readable document, so
// a route that decoded the body without checking the status would decide a
// write on a 500's body. The write itself is never forwarded in either case —
// for a comment on the run's OWN pull request (fpr) an empty source is a
// source no rule refuses, so falling through here would be a write on nothing.
func TestBitbucketRoutePRFactsUnreadableIsRefused(t *testing.T) {
	document := `{"source":{"branch":{"name":"someone-elses-branch"},"repository":{"full_name":"` +
		testBitbucketRepo + `"}},"destination":{"branch":{"name":"main"}}}`
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"an error status beside a readable document", http.StatusInternalServerError, document},
		{"a 200 whose body is not the document", http.StatusOK, "not the API's document at all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, up, _ := proxySvc(t)
			up.answerWith(tc.status, tc.body)

			resp, ans := s.proxyCall(t, http.MethodPut, proxyPath(proxyRESTPath("/pullrequests/7")),
				[]byte(`{"title":"the run's work"}`))
			proxyRefused(t, resp, ans, http.StatusServiceUnavailable, ErrorStateUnavailable, "could not be read")

			seen := up.calls()
			if len(seen) != 1 || seen[0].method != http.MethodGet {
				t.Fatalf("the upstream saw %+v, want the facts read alone: an unknown pull request is never written to", seen)
			}
			line := s.onlyAuditLine(t)
			if line["status"] != ErrorStateUnavailable || line["purpose"] != PurposeBitbucketAPI {
				t.Errorf("the audit line = %v, want the refusal under the Bitbucket API purpose", line)
			}
		})
	}
}

// TestBitbucketRoutePRCreateIsCheckedOnItsBody: a create names a pull request
// that does not exist yet, so there is no fact for this service to read and the
// source it names is checked in the body it is about to send (G12). Two things
// are the whole of it: the upstream sees the create and NO read of a pull
// request, and a body naming another branch is refused.
func TestBitbucketRoutePRCreateIsCheckedOnItsBody(t *testing.T) {
	s, up, _ := proxySvc(t)
	up.answerWith(http.StatusCreated, "{}")

	create := func(branch string) []byte {
		body, err := json.Marshal(map[string]any{
			"title": "the run's work",
			"source": map[string]any{
				"branch":     map[string]any{"name": branch},
				"repository": map[string]any{"full_name": testBitbucketRepo},
			},
			"destination": map[string]any{"branch": map[string]any{"name": "main"}},
		})
		if err != nil {
			t.Fatalf("building the create's body: %v", err)
		}
		return body
	}

	resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyRESTPath("/pullrequests")), create("someone-elses-branch"))
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonSourceMismatch)
	if n := up.count(); n != 0 {
		t.Fatalf("the upstream saw %d calls, and a create has no pull request to read: %+v", n, up.calls())
	}

	resp, ans = s.proxyCall(t, http.MethodPost, proxyPath(proxyRESTPath("/pullrequests")), create(s.proxyBranch()))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the create answered %d, want 201 (%s)", resp.StatusCode, ans)
	}
	got := up.only(t)
	if want := "/2.0/repositories/" + testBitbucketRepo + "/pullrequests"; got.path != want {
		t.Errorf("the create was addressed to %q, want %q", got.path, want)
	}
}

// TestBitbucketRouteBindsJobAccountRepo: the repository a proxied call is
// judged against is the CALLING JOB ACCOUNT's and never the run identity's
// alone (review finding N1). A run token of another repository presented by
// this job is refused, and nothing reaches bitbucket.org.
func TestBitbucketRouteBindsJobAccountRepo(t *testing.T) {
	s, up, _ := proxySvc(t)

	// A run identity of another repository: the token is a valid one, verified
	// by the same key, and the job account that presents it is bound to the
	// Bitbucket repository. The binding is what refuses it.
	other := testSlug(t, testRepoPath)
	claims := append(proxyClaims(s.proxyBranch()),
		func(c map[string]any) { c["fs"] = other },
		func(c map[string]any) { c["sub"] = "r~" + other + "~" + s.run },
		func(c map[string]any) { c["user_id"] = "r~" + other + "~" + s.run },
	)
	resp, ans := s.proxyCallAs(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil,
		s.runToken(claims...), s.jobToken(), s.job)
	proxyRefused(t, resp, ans, http.StatusForbidden, ErrorUnverifiedRunIdentity, "")

	if n := up.count(); n != 0 {
		t.Fatalf("the upstream saw %d calls, and an unbound run forwards nothing", n)
	}
}

// TestBitbucketRouteAuditNeverEchoesRef: a refusal names the CLASS of what it
// refused and never the caller's text (G7). The ref a refused push named is
// searched for in the audit line and in the service's own log, which are the
// only two places a line about this route is written.
func TestBitbucketRouteAuditNeverEchoesRef(t *testing.T) {
	s, _, _ := proxySvc(t)
	body := proxyUpdateRequest(proxyOtherRef) + "PACK"

	resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte(body))
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonOtherRef)

	if line := s.onlyAuditLine(t); strings.Contains(fmt.Sprint(line), proxyRefMarker) {
		t.Errorf("the audit line carries the ref the caller named: %v", line)
	}
	if logged := s.logs.String(); strings.Contains(logged, proxyRefMarker) {
		t.Errorf("the service's own log carries the ref the caller named: %s", logged)
	}
	if strings.Contains(string(ans), proxyRefMarker) {
		t.Errorf("the refusal carries the ref the caller named: %s", ans)
	}
}

// TestBitbucketRouteOneAuditLinePerRequest: exactly one line per request that
// reaches this route (G7, C1.13) — a forwarded call, a refusal taken inside
// the route, and a refusal taken ABOVE it (the operator's stop switch), which
// is the case whose line names neither a purpose nor a credential, because the
// service had not yet decided what the call was.
func TestBitbucketRouteOneAuditLinePerRequest(t *testing.T) {
	t.Run("a forwarded fetch", func(t *testing.T) {
		s, up, _ := proxySvc(t)
		up.answerWith(http.StatusOK, "{}")
		if resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("the fetch answered %d (%s)", resp.StatusCode, ans)
		}
		line := s.onlyAuditLine(t)
		if line["status"] != StatusOK || line["purpose"] != PurposeGit || line["credential"] != config.BitbucketSecret {
			t.Errorf("the line = %v, want one git line naming the bundle's credential and ok", line)
		}
		if line["run"] != s.run || line["job"] != s.job {
			t.Errorf("the line = %v, want the run %q and the job %q", line, s.run, s.job)
		}
	})

	t.Run("a refused push", func(t *testing.T) {
		s, _, _ := proxySvc(t)
		body := proxyUpdateRequest(proxyOtherRef) + "PACK"
		resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte(body))
		proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonOtherRef)
		line := s.onlyAuditLine(t)
		if line["status"] != ErrorRequestRefused || line["purpose"] != PurposeGit {
			t.Errorf("the line = %v, want one git line whose status is the refusal's own type", line)
		}
	})

	t.Run("a REST read", func(t *testing.T) {
		s, up, _ := proxySvc(t)
		up.answerWith(http.StatusOK, "{}")
		if resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyRESTPath("")), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("the repository read answered %d (%s)", resp.StatusCode, ans)
		}
		line := s.onlyAuditLine(t)
		if line["purpose"] != PurposeBitbucketAPI {
			t.Errorf("the line = %v, want the Bitbucket API purpose", line)
		}
	})

	t.Run("the kill switch", func(t *testing.T) {
		s, up, _ := proxySvc(t, func(s *svc, _ *Config) { s.kills.repos[s.slug] = true })
		resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil)
		proxyRefused(t, resp, ans, http.StatusServiceUnavailable, ErrorStateUnavailable, "")
		if n := up.count(); n != 0 {
			t.Fatalf("a stopped service made %d upstream calls", n)
		}
		line := s.onlyAuditLine(t)
		if auditField(line, "purpose") != "" || auditField(line, "credential") != "" {
			t.Errorf("the line = %v, and a refusal taken above the classification names neither a purpose nor a credential", line)
		}
	})
}

// TestBitbucketRouteKillSwitch: the operator's stop flag stops this route
// exactly as it stops a model call (G8) — no credential is read, nothing is
// forwarded, and the run is answered a refusal it halts on.
func TestBitbucketRouteKillSwitch(t *testing.T) {
	s, up, _ := proxySvc(t, func(s *svc, _ *Config) { s.kills.global = true })

	resp, ans := s.proxyCall(t, http.MethodPost, proxyPath(proxyGitPath("git-receive-pack")), []byte(proxyUpdateRequest("refs/heads/"+s.proxyBranch())+"PACK"))
	proxyRefused(t, resp, ans, http.StatusServiceUnavailable, ErrorStateUnavailable, "kill switch")

	if n := up.count(); n != 0 {
		t.Fatalf("a stopped service made %d upstream calls: %+v", n, up.calls())
	}
	if reads := s.kills.readCount(); reads != 1 {
		t.Errorf("the stop switches were read %d times, want once", reads)
	}
}

// TestBitbucketRouteEndedRun: a run identity whose fx has passed is a run that
// ended (W2), and Verify refuses it — before the caller's identity is
// verified, so this route writes no audit line at all and forwards nothing.
func TestBitbucketRouteEndedRun(t *testing.T) {
	s, up, _ := proxySvc(t)
	ended := s.runToken(append(proxyClaims(s.proxyBranch()),
		func(c map[string]any) { c["fx"] = s.now.Add(-time.Minute).UnixMilli() })...)

	resp, ans := s.proxyCallAs(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil,
		ended, s.jobToken(), s.job)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the call answered %d, want 403 (%s)", resp.StatusCode, ans)
	}
	if e := refusal(t, resp.StatusCode, ans); e.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal's type = %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
	}
	if n := up.count(); n != 0 {
		t.Fatalf("an ended run made %d upstream calls", n)
	}
	if lines := s.auditLines(t); len(lines) != 0 {
		t.Errorf("an unverified caller wrote %d audit lines, want none (the line is written once the caller's identity is verified)", len(lines))
	}
}

// TestBitbucketRouteNoTokenInErrors: the credential this route forwards with
// appears in no answer, in no log line and in no audit line — not when the
// upstream is unreachable, which is the failure whose message a test can
// search. The error is a typed refusal the run halts on, and the cause is the
// service's own (D143).
func TestBitbucketRouteNoTokenInErrors(t *testing.T) {
	s, _, token := proxySvc(t, func(_ *svc, cfg *Config) {
		cfg.Client = &http.Client{Transport: refusingTransport{}}
	})

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil)
	proxyRefused(t, resp, ans, http.StatusServiceUnavailable, ErrorStateUnavailable, "")

	for what, text := range map[string]string{
		"the refusal":    string(ans),
		"the log":        s.logs.String(),
		"the audit line": s.audit.String(),
	} {
		if strings.Contains(text, token) {
			t.Errorf("%s carries the bundle's Bitbucket token", what)
		}
		if strings.Contains(text, "x-token-auth") {
			t.Errorf("%s carries the git credential's own spelling", what)
		}
	}
}

// refusingTransport is an upstream that cannot be reached at all: the failure
// a call meets when bitbucket.org is down, or when the network between the two
// is. It never answers, so nothing about the credential can reach an answer.
type refusingTransport struct{}

func (refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("the upstream is unreachable")
}

// TestBitbucketRouteIsTheOnlyPlaceThePolicyIsRead: a proxied call reads the
// published block — that is what admits the repository and what says whether
// this installation proxies it at all (G3, G9) — and an installation that does
// not proxy it refuses the call without ever reading the bundle. It is the
// fail-closed half of the route: the token is not offered to a repository the
// block does not name.
// TestBitbucketRouteUnauditedIssuesNothing: a proxied call whose own line
// cannot be written is answered with a refusal and the upstream's answer is
// DROPPED rather than relayed (D143, the rule refuseCall keeps for every
// refusal on this route). The call itself has already reached bitbucket.org —
// the service cannot unmake it — so what the run must not get is the answer of
// an act no operator can see.
//
// The handler fails the FIRST line and records the next: the call's own line is
// the one that fails, and the refusal that follows is audited through the same
// auditor, so the line the operator does need — the call that got no answer —
// is the one line this test reads. A route that relayed the upstream's body, or
// that returned before refuseCall, would leave that line unwritten.
func TestBitbucketRouteUnauditedIssuesNothing(t *testing.T) {
	failed := false
	auditLog := &logBuf{}
	s, up, token := proxySvc(t, func(_ *svc, cfg *Config) {
		cfg.AuditLog = &failFirstHandler{inner: slog.NewJSONHandler(auditLog, nil), failed: &failed}
	})
	up.answerWith(http.StatusOK, "the trunk's own advertisement")

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil)
	proxyRefused(t, resp, ans, StatusUnavailable, ErrorStateUnavailable, "could not record it")

	if !failed {
		t.Fatalf("the call's own line was written, and this test is about a line that could not be")
	}
	if n := up.count(); n != 1 {
		t.Fatalf("the upstream was reached %d times, want the one call whose answer was withheld", n)
	}
	if strings.Contains(string(ans), "the trunk's own advertisement") {
		t.Errorf("the refusal relays the upstream's answer: %s", ans)
	}

	lines := auditLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly the one it owes the operator: %v", len(lines), lines)
	}
	if lines[0]["status"] != ErrorStateUnavailable || lines[0]["purpose"] != PurposeGit {
		t.Errorf("the line = %v, want the refusal under the git purpose", lines[0])
	}
	for _, where := range []struct{ what, text string }{
		{"the refusal", string(ans)},
		{"the service's log", s.logs.String()},
	} {
		if strings.Contains(where.text, token) {
			t.Errorf("%s carries the bundle's token", where.what)
		}
	}
	if !strings.Contains(s.logs.String(), "could not be audited") {
		t.Errorf("the service's log does not report the line it could not write: %s", s.logs.String())
	}
}

func TestBitbucketRouteProxyOffReadsNoBundle(t *testing.T) {
	s, up, _ := proxySvc(t, func(_ *svc, cfg *Config) {
		cfg.Policy = &fakePolicy{
			shared:      decodeTestPolicy(t, strings.Replace(proxyPolicyDoc(t), `, "proxy": true`, "", 1)),
			sharedFound: true,
		}
	})

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil)
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonProxyOff)

	if n := up.count(); n != 0 {
		t.Fatalf("an installation that proxies nothing made %d upstream calls", n)
	}
	if reads := s.src.readCount(); reads != 0 {
		t.Errorf("the bundle was read %d times, and a repository this installation does not proxy has no token to offer it", reads)
	}
}

// TestBitbucketRouteEmptyAllowListIsNotAnAdmission: a block that admits no
// repository refuses every call (G3), which is the state a hand-written node
// reaches by taking the repository off the allow-list.
func TestBitbucketRouteNotAdmitted(t *testing.T) {
	s, up, _ := proxySvc(t, func(_ *svc, cfg *Config) {
		pol := cfg.Policy.(*fakePolicy)
		pol.shared.BitbucketToken.AllowRepos = []string{"bitbucket/" + testOtherWorkspace + "/*"}
	})

	resp, ans := s.proxyCall(t, http.MethodGet, proxyPath(proxyGitPath("info/refs")+"?service=git-upload-pack"), nil)
	proxyRefused(t, resp, ans, http.StatusBadRequest, ErrorRequestRefused, gitproxy.ReasonNotAdmitted)

	if n := up.count(); n != 0 {
		t.Fatalf("a repository the block does not admit made %d upstream calls", n)
	}
}
