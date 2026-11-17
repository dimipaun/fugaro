package credsvc

// The label route's Bitbucket half (labelroute_bitbucket.go, D2736, G5). The
// properties these tests hold are the ones that half's design turns on:
//
//   - the host: the repository the call names is resolved against the two
//     inventories, and a name BOTH hold is refused rather than resolved by
//     precedence (TestAmbiguousRepositoryRefused, labelHostOf);
//   - the App ID: a Bitbucket call names none and one that carries one is
//     refused, which is what lets an installation with Bitbucket alone ask for
//     the read at all (TestBitbucketGetRefusesAnAppID);
//   - the act: `get` answers the state alone — open, merged or closed, as the
//     host's own adapter spells it — and the four acts around it are refused as
//     unsupported rather than answered with an empty success
//     (TestLabelGetServesBitbucketState,
//     TestLabelListAddRemoveRefuseBitbucketAsUnsupported);
//   - the mint: the token comes from the installation's read consumer with the
//     read mint's own guards — none is a refusal and never a fallback, the
//     consumer's workspace must be the repository's, and only the read scope
//     allowlist is handed on (TestBitbucketGetRefusesWithoutReadConsumer,
//     TestBitbucketGetRefusesForeignWorkspace,
//     TestBitbucketGetRefusesScopeOutsideAllowlist, G11, U11);
//   - the token: cached per workspace until shortly before it expires, and
//     nowhere a caller or an operator can read it
//     (TestBitbucketGetCachesTheMintedToken,
//     TestBitbucketLabelGetNeverReturnsAToken, D2439);
//   - the line: every verified call writes exactly one, and a Bitbucket `get`'s
//     names the READ CONSUMER and not the App key
//     (TestBitbucketGetAuditNamesReadConsumer,
//     TestBitbucketLabelGetAuditsEveryCall);
//   - the class: the orchestrator daemon and nobody else
//     (TestBitbucketLabelGetRefusesOtherClasses);
//   - the route's own answer: routeAct(LabelPath) is unchanged, so a GitHub
//     line reads exactly as it did before the second host existed
//     (TestRouteActLabelPathUnchanged).
//
// The fake below stands in for the two hosts this act reaches — bitbucket.org
// for the consumer exchange and api.bitbucket.org for the pull request read —
// through a transport that refuses every other destination, and it records the
// host the service BUILT, so "the read never happened" and "the mint was asked
// once" are claims a test reads rather than assumes. Everything here is
// hermetic (Convention 9), and no literal is, or looks like, a credential
// (Convention 2): the token and the consumer come from fakeCred.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// labelBitbucketTokenPath is the consumer exchange's path, spelled as the
// endpoint spells it. It is a literal on purpose: the adapter's DefaultOAuthRoot
// plus this path is what the service must reach, so a root or a path moved in
// the adapter is a test failure and not a test that follows it.
const labelBitbucketTokenPath = "/site/oauth2/access_token"

// labelBitbucketPRPrefix is the API path every pull request read is under. The
// two constants together are what the transport's host rule and the fake's own
// routing are keyed by, so a read that went to the exchange's host, or a mint
// that went to the API's, cannot be read as the other.
const labelBitbucketPRPrefix = "/2.0/repositories/"

// ---------------------------------------------------------------------------
// The fake Bitbucket
// ---------------------------------------------------------------------------

// bitbucketLabelSeen is one call the service made, as it built it: the host and
// the path BEFORE the hop to loopback, the bearer it presented, and the body.
// The host is recorded because which host an act reaches is this half's whole
// point, and the bearer because a read made with a token this route did not
// intend to use is a read that must not happen.
type bitbucketLabelSeen struct {
	host string
	path string
	auth string
	body string
}

// bitbucketLabelFake stands in for the two Bitbucket hosts the label route's
// read reaches.
type bitbucketLabelFake struct {
	t   *testing.T
	srv *httptest.Server

	mu   sync.Mutex
	seen []bitbucketLabelSeen
	// token, scopes and expires are the exchange's answer; mintStatus, when it
	// is not zero, is what the exchange refuses with instead.
	token      string
	scopes     string
	expires    int
	mintStatus int
	// state is the pull request's state word as bitbucket.org spells it, and
	// prStatus, when it is not zero, is what the read refuses with instead.
	state    string
	prStatus int
	// key and secret are the read consumer the fixture's bundle holds, so a
	// test can hold what the exchange presented to it without either being
	// written down.
	key, secret string
}

func newBitbucketLabelFake(t *testing.T) *bitbucketLabelFake {
	t.Helper()
	f := &bitbucketLabelFake{
		t:       t,
		token:   fakeCred(t, "bitbucket-read-token"),
		scopes:  "repository:read pullrequest:read",
		expires: 7200,
		state:   "OPEN",
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == labelBitbucketTokenPath:
			f.answerToken(w)
		case strings.HasPrefix(r.URL.Path, labelBitbucketPRPrefix):
			f.answerPull(w, r.URL.Path)
		default:
			t.Errorf("the label route's Bitbucket read reached %s, which is neither the exchange nor the API", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// answerToken is the consumer exchange's answer: the access token, its life,
// and the scopes the endpoint says it carries.
func (f *bitbucketLabelFake) answerToken(w http.ResponseWriter) {
	f.mu.Lock()
	status, token, scopes, expires := f.mintStatus, f.token, f.scopes, f.expires
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
		return
	}
	answer := map[string]any{"access_token": token, "expires_in": expires}
	if scopes != "" {
		answer["scopes"] = scopes
	}
	_ = json.NewEncoder(w).Encode(answer)
}

// answerPull is the API's answer for one pull request. The number is the last
// path segment, which is the number the call asked about: an answer for another
// one is a failure here rather than something a test has to think about.
func (f *bitbucketLabelFake) answerPull(w http.ResponseWriter, path string) {
	f.mu.Lock()
	status, state := f.prStatus, f.state
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"type":"error"}`)
		return
	}
	number, err := strconv.Atoi(path[strings.LastIndex(path, "/")+1:])
	if err != nil {
		f.t.Errorf("the label route's read named no pull request number: %s", path)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": number, "state": state, "title": "a pull request",
		"links": map[string]any{"html": map[string]any{"href": "https://example.invalid/pr/" + strconv.Itoa(number)}},
	})
}

// client is the http.Client the service under test reaches Bitbucket with:
// every call is sent to the fake whatever host the service built the URL for,
// and a call to a host that is not one of Bitbucket's two is refused rather
// than answered. That refusal is what pins the two: the token comes from
// bitbucket.org and the read goes to api.bitbucket.org, and neither is a value
// an environment or a call can move (T2.5, G10).
func (f *bitbucketLabelFake) client() *http.Client {
	base, err := url.Parse(f.srv.URL)
	if err != nil {
		panic("the fake's URL is not a URL: " + err.Error())
	}
	return &http.Client{Transport: &bitbucketLabelTransport{base: base, f: f}}
}

type bitbucketLabelTransport struct {
	base *url.URL
	f    *bitbucketLabelFake
}

func (h *bitbucketLabelTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.Host {
	case "bitbucket.org", "api.bitbucket.org":
	default:
		return nil, fmt.Errorf("the label route's Bitbucket read addressed %s://%s, and it reaches bitbucket.org or api.bitbucket.org alone", r.URL.Scheme, r.URL.Host)
	}
	// The call is recorded HERE and not in the handler, because it is the host
	// the service built that matters: after the hop the request carries the
	// loopback address, and a recording made there could not tell a read
	// addressed to the API from one addressed to the exchange's host.
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		_ = r.Body.Close()
	}
	h.f.mu.Lock()
	h.f.seen = append(h.f.seen, bitbucketLabelSeen{
		host: r.URL.Host, path: r.URL.Path, auth: r.Header.Get("Authorization"), body: string(body),
	})
	h.f.mu.Unlock()
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

// calls is every call the fake answered, in order; mints and reads are the two
// halves of them, which is what the caching and "the read never happened"
// assertions count.
func (f *bitbucketLabelFake) calls() []bitbucketLabelSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bitbucketLabelSeen(nil), f.seen...)
}

func (f *bitbucketLabelFake) mints() []bitbucketLabelSeen {
	var out []bitbucketLabelSeen
	for _, c := range f.calls() {
		if c.path == labelBitbucketTokenPath {
			out = append(out, c)
		}
	}
	return out
}

func (f *bitbucketLabelFake) reads() []bitbucketLabelSeen {
	var out []bitbucketLabelSeen
	for _, c := range f.calls() {
		if strings.HasPrefix(c.path, labelBitbucketPRPrefix) {
			out = append(out, c)
		}
	}
	return out
}

// count is every call the fake answered, which is what a test compares before
// and after a call that must reach Bitbucket not at all.
func (f *bitbucketLabelFake) count() int { return len(f.calls()) }

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// bitbucketLabelSvc is the label route's fixture wired for a Bitbucket
// repository: the route's own classes (every call below is made with the
// fixture's orchestrator daemon token), the read consumer in the bundle, and
// the two Bitbucket hosts faked behind the service's client.
func bitbucketLabelSvc(t *testing.T) (*svc, *bitbucketLabelFake) {
	t.Helper()
	return bitbucketLabelSvcWith(t, nil)
}

// bitbucketLabelSvcWith is bitbucketLabelSvc with the service's own
// configuration under the test's control, for the cases that need one wired
// differently: a bundle holding no read consumer, a consumer of another
// workspace, an inventory holding one name on both hosts, an audit log that
// cannot write.
func bitbucketLabelSvcWith(t *testing.T, mutate func(*svc, *Config)) (*svc, *bitbucketLabelFake) {
	t.Helper()
	return bitbucketLabelSvcWithFake(t, func(s *svc, cfg *Config, _ *bitbucketLabelFake) {
		if mutate != nil {
			mutate(s, cfg)
		}
	})
}

// bitbucketLabelSvcWithFake is bitbucketLabelSvcWith with the fake handed to
// the mutate as well, for the cases whose configuration is about the host: a
// mint it refuses, a read it refuses, a scope the endpoint lists.
func bitbucketLabelSvcWithFake(t *testing.T, mutate func(*svc, *Config, *bitbucketLabelFake)) (*svc, *bitbucketLabelFake) {
	t.Helper()
	f := newBitbucketLabelFake(t)
	s, _ := labelRouteSvcWith(t, func(s *svc, cfg *Config) {
		doc, key, secret := labelBundleDoc(t, testWorkspace)
		f.key, f.secret = key, secret
		// Both inventories are set, because the route resolves against both:
		// the fixture's GitHub repository stays where it is, so a test that
		// asks about it is still asking about a GitHub repository.
		cfg.Repos = map[string]string{
			githubSlug(t, testRepoPath):         testRepo,
			bitbucketSlug(t, testBitbucketRepo): testCanonical(t, testBitbucketRepo),
		}
		cfg.Client = f.client()
		s.src.docs = [][]byte{doc}
		if mutate != nil {
			mutate(s, cfg, f)
		}
	})
	return s, f
}

// labelBundleDoc is the fixture's bundle: the installation's read consumer of
// one workspace beside the GitHub App key, which is what an installation
// serving both hosts holds. The consumer's key and secret come from fakeCred and
// are answered back, so a test can hold the exchange to what the bundle carries
// without either being written down (Convention 2).
//
// Both entries are here rather than the consumer alone because the same route
// serves both hosts (D2736): a fixture holding one of the two would make every
// GitHub act of this file a refusal about the bundle, which is a different test's
// subject (TestLabelRouteFailsClosedWithoutTheAppKey).
func labelBundleDoc(t *testing.T, workspace string) (doc []byte, key, secret string) {
	t.Helper()
	key, secret = fakeCred(t, "consumer-key"), fakeCred(t, "consumer-secret")
	return append(mintBundleDoc(t), []byte(BitbucketReadConsumerSecret+":\n"+
		"  key: "+key+"\n"+
		"  secret: "+secret+"\n"+
		"  workspace: "+workspace+"\n")...), key, secret
}

// bitbucketLabelBody is one label call's body as a BITBUCKET client sends it:
// the fields the daemon writes, the App ID field present and EMPTY, and nothing
// else. It is spelled here rather than marshalled from the service's own struct
// so a rename on either side is a test failure — and the empty app_id is what
// the daemon's own client really sends (creds.LabelRequest.AppID carries no
// omitempty), which is why the route has to read an empty one as "no App ID
// named" rather than as a malformed ID.
func bitbucketLabelBody(repo, action string, number int, name, runID string) string {
	body := `{"repository":` + strconv.Quote(repo) + `,"action":` + strconv.Quote(action) + `,"app_id":""`
	if number != 0 {
		body += `,"number":` + strconv.Itoa(number)
	}
	if name != "" {
		body += `,"name":` + strconv.Quote(name)
	}
	if runID != "" {
		body += `,"run_id":` + strconv.Quote(runID)
	}
	return body + "}"
}

// bitbucketCall sends one Bitbucket body as the fixture's orchestrator daemon.
func (s *svc) bitbucketCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.labelCall(t, body, mutate...)
}

// bitbucketGet is one `get` of the fixture's Bitbucket repository: the
// fixture's own orchestrator token, no App ID, and the number the test names.
func (s *svc) bitbucketGet(t *testing.T, number int, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.bitbucketCall(t, bitbucketLabelBody(testBitbucketRepo, creds.LabelActionGet, number, "", ""), mutate...)
}

// ---------------------------------------------------------------------------
// The host
// ---------------------------------------------------------------------------

// TestAmbiguousRepositoryRefused: a name the installation holds on BOTH hosts is
// refused rather than resolved by precedence.
//
// The inventory is one table keyed by slug and the two slug derivations are
// different hashes of the same "owner/name" text, so nothing about the name says
// which host a caller meant. A route that preferred one would answer a read of
// the OTHER host's pull request — another repository's state, silently — and the
// refusal is the only answer that cannot be wrong.
//
// It is taken before the rate, the bundle and the mint: an ambiguous name is a
// call this service cannot address, so no credential is read for it and neither
// host is reached.
func TestAmbiguousRepositoryRefused(t *testing.T) {
	const shared = "acme/shared"
	s, f := bitbucketLabelSvcWith(t, func(s *svc, cfg *Config) {
		cfg.Repos = map[string]string{
			githubSlug(t, shared):               "github/" + shared,
			bitbucketSlug(t, shared):            testCanonical(t, shared),
			bitbucketSlug(t, testBitbucketRepo): testCanonical(t, testBitbucketRepo),
		}
	})
	resp, body := s.bitbucketCall(t, bitbucketLabelBody(shared, creds.LabelActionGet, 7, "", ""))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("an ambiguous repository was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorRequestRefused {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
	}
	if !strings.Contains(e.Message, ErrLabelAmbiguousRepository.Error()) {
		t.Errorf("the refusal is %q, want it to name the ambiguity (%q)", e.Message, ErrLabelAmbiguousRepository.Error())
	}
	if n := f.count(); n != 0 {
		t.Errorf("an ambiguous repository reached Bitbucket %d times, want none: %+v", n, f.calls())
	}
	if n := len(s.auditLines(t)); n != 1 {
		t.Errorf("the refused call wrote %d audit lines, want exactly one", n)
	}

	// A name ONE inventory holds is not ambiguous, and the same call reaches
	// the host that holds it: the case above would also pass on a route that
	// refused every repository.
	resp, body = s.bitbucketGet(t, 7)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the fixture's own Bitbucket repository was refused: HTTP %d %s", resp.StatusCode, body)
	}
}

// ---------------------------------------------------------------------------
// The App ID
// ---------------------------------------------------------------------------

// TestBitbucketGetRefusesAnAppID: a Bitbucket call names no App ID, and one
// that carries an App ID is refused rather than performed.
//
// There is no GitHub App on that host, so an App ID beside a Bitbucket
// repository is either a caller that resolved its repository wrongly or a field
// this contract must not let a Bitbucket call carry — and both are refusals: a
// field quietly dropped would have the caller believing it named an App whose
// key signed the read, and no key of any App is what performs this act.
//
// The refusal is a request refusal and not an outage: the call is one this
// route does not take, so nothing is minted and no pull request is read.
func TestBitbucketGetRefusesAnAppID(t *testing.T) {
	s, f := bitbucketLabelSvc(t)
	body := `{"repository":` + strconv.Quote(testBitbucketRepo) + `,"action":"` + creds.LabelActionGet +
		`","app_id":"` + testAppID + `","number":7}`
	resp, ans := s.bitbucketCall(t, body)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("a Bitbucket call carrying an App ID was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, ans)
	}
	e := refusal(t, resp.StatusCode, ans)
	if e.Error != ErrorRequestRefused {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
	}
	if !strings.Contains(e.Message, "App ID") {
		t.Errorf("the refusal is %q, and it must say which field the call must not carry", e.Message)
	}
	if n := f.count(); n != 0 {
		t.Errorf("a call carrying an App ID reached Bitbucket %d times, want none: %+v", n, f.calls())
	}
	lines := s.auditLines(t)
	if len(lines) != 1 {
		t.Fatalf("the refused call wrote %d audit lines, want exactly one", len(lines))
	}
	// The line names the host's own credential and not the App key the field
	// beside it belongs to: the host IS resolved by the time this rule is asked,
	// so the act this line records is a Bitbucket one, performed with the read
	// consumer — which is also the only credential of the two an installation
	// with Bitbucket alone holds.
	if got := auditField(lines[0], "credential"); got != BitbucketReadConsumerSecret {
		t.Errorf("the line for an App ID refused on a Bitbucket call names %q as its credential, want %q", got, BitbucketReadConsumerSecret)
	}

	// The GitHub repository of the same fixture takes an App ID and is refused
	// without one (the route's own rule, unchanged, and pinned in
	// TestLabelRouteRefusesACallItCannotRead): a Bitbucket client is the one
	// that names none.
	resp, ans = s.labelCall(t, bitbucketLabelBody(testRepoPath, creds.LabelActionGet, 7, "", ""))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("a GitHub call with no App ID was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, ans)
	}
}

// ---------------------------------------------------------------------------
// The act
// ---------------------------------------------------------------------------

// TestLabelGetServesBitbucketState: `get` is the one act a Bitbucket repository
// answers, and it answers the state alone — open, merged or closed, in
// gitprov.PRState's own spelling, which is what the daemon's syncer compares
// against (R5, R8).
//
// The four state words are the whole of the host's mapping and the two ends of
// the danger in it: DECLINED and SUPERSEDED are closed, which is what clears a
// pull request's labels, and an adapter that read either as merged would have
// the daemon folding a declined pull request as landed. The read is asserted to
// be ONE call to api.bitbucket.org — the API's host and not the exchange's.
func TestLabelGetServesBitbucketState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		want  gitprov.PRState
	}{
		{name: "open", state: "OPEN", want: gitprov.PROpen},
		{name: "merged", state: "MERGED", want: gitprov.PRMerged},
		{name: "declined", state: "DECLINED", want: gitprov.PRClosed},
		{name: "superseded", state: "SUPERSEDED", want: gitprov.PRClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := bitbucketLabelSvc(t)
			f.state = tc.state
			resp, body := s.bitbucketGet(t, 7)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the Bitbucket read was refused: HTTP %d %s", resp.StatusCode, body)
			}
			ans := labelAnswer(t, body)
			if len(ans.PullRequests) != 1 {
				t.Fatalf("the read answered %d pull requests, want the one it was asked about: %+v", len(ans.PullRequests), ans.PullRequests)
			}
			pr := ans.PullRequests[0]
			if pr.Number != 7 {
				t.Errorf("the read answered pull request %d, want 7", pr.Number)
			}
			if pr.State != string(tc.want) {
				t.Errorf("the %s pull request was answered %q, want %q", tc.name, pr.State, tc.want)
			}
			if len(pr.Labels) != 0 {
				t.Errorf("the Bitbucket answer carries the labels %v, and that host has none", pr.Labels)
			}
			if strings.Contains(string(body), "labels\":null") {
				t.Errorf("the answer carries a null label field, want the empty list this host really answered: %s", body)
			}
			reads := f.reads()
			if len(reads) != 1 {
				t.Fatalf("the read reached the API %d times, want one: %+v", len(reads), f.calls())
			}
			if reads[0].host != "api.bitbucket.org" {
				t.Errorf("the read was addressed to %q, want api.bitbucket.org", reads[0].host)
			}
			if want := labelBitbucketPRPrefix + testWorkspace + "/repo-a/pullrequests/7"; reads[0].path != want {
				t.Errorf("the read's path = %q, want %q", reads[0].path, want)
			}
			if mints := f.mints(); len(mints) != 1 || mints[0].host != "bitbucket.org" {
				t.Errorf("the exchange = %+v, want exactly one, addressed to bitbucket.org", mints)
			}
		})
	}
}

// TestLabelListAddRemoveRefuseBitbucketAsUnsupported: the four acts around the
// read are refused on a Bitbucket repository rather than answered with an empty
// success (R7, ErrLabelUnsupported).
//
// An empty answer would read as an act that was performed — the daemon's own
// provider makes a no-op of them, which is right for ITS caller and wrong for a
// service that was asked to perform one — and a listing that answered no pull
// requests would have the syncer compute a difference against nothing. The
// refusal comes BEFORE the rate and before the bundle: a call this host has no
// answer for is not an act, so no credential is read and no rate is spent.
func TestLabelListAddRemoveRefuseBitbucketAsUnsupported(t *testing.T) {
	s, f := bitbucketLabelSvc(t)
	run := s.run
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "list", body: bitbucketLabelBody(testBitbucketRepo, creds.LabelActionList, 0, "", "")},
		{name: "ensure", body: bitbucketLabelBody(testBitbucketRepo, creds.LabelActionEnsure, 0, wantRunningName, "")},
		{name: "add", body: bitbucketLabelBody(testBitbucketRepo, creds.LabelActionAdd, 7, wantRunningName, run)},
		{name: "remove", body: bitbucketLabelBody(testBitbucketRepo, creds.LabelActionRemove, 7, wantFailedName, run)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, lines := f.count(), len(s.auditLines(t))
			resp, body := s.bitbucketCall(t, tc.body)
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the %s act on a Bitbucket repository was answered %d, want %d: %s", tc.name, resp.StatusCode, StatusUnavailable, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorStateUnavailable {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
			}
			if !strings.Contains(e.Message, ErrLabelUnsupported.Error()) {
				t.Errorf("the refusal is %q, want it to name the unsupported host (%q)", e.Message, ErrLabelUnsupported.Error())
			}
			if after := f.count(); after != before {
				t.Errorf("the refused %s act reached Bitbucket %d times, want none", tc.name, after-before)
			}
			if got := len(s.auditLines(t)); got != lines+1 {
				t.Errorf("the refused %s act wrote %d audit lines, want exactly one", tc.name, got-lines)
			}
		})
	}

	// The same four acts on a GitHub repository are SERVED, which is what makes
	// these refusals a fact about the host rather than about the acts. That half
	// is the route's own tests' (TestLabelRouteReadsTheAdaptersAnswer,
	// TestLabelRouteEnsureUsesFixedText): this fixture's client reaches the two
	// Bitbucket hosts alone, so the two ends of one route are held apart by the
	// fake each reaches.
}

// ---------------------------------------------------------------------------
// The mint
// ---------------------------------------------------------------------------

// TestBitbucketGetRefusesWithoutReadConsumer: a bundle that holds no Bitbucket
// read consumer is a refusal and never a fallback to another entry (D143).
//
// It is the fail-closed case BB2's release note describes: an installation that
// has not run `fugaro creds set bitbucket-read-consumer` has no read mint here,
// and a fallback — to the run consumer, to a token another route holds — would
// be a read performed with a credential the operator never chose for it. Nothing
// is minted and nothing is read.
func TestBitbucketGetRefusesWithoutReadConsumer(t *testing.T) {
	s, f := bitbucketLabelSvcWith(t, func(s *svc, cfg *Config) {
		s.src.docs = [][]byte{mintBundleDoc(t)}
	})
	resp, body := s.bitbucketGet(t, 7)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a read with no read consumer was answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
	}
	if !strings.Contains(e.Message, ErrReadNoConsumer.Error()) {
		t.Errorf("the refusal is %q, want it to name the missing consumer (%q)", e.Message, ErrReadNoConsumer.Error())
	}
	if n := f.count(); n != 0 {
		t.Errorf("a read with no read consumer reached Bitbucket %d times, want none: %+v", n, f.calls())
	}
	// The line names the credential the act would have been performed with:
	// this is a Bitbucket act, and its credential is the read consumer whether
	// or not the bundle turned out to hold one.
	if line := s.onlyAuditLine(t); auditField(line, "credential") != BitbucketReadConsumerSecret {
		t.Errorf("the audit line = %v, want the read consumer as the credential", line)
	}
}

// TestBitbucketGetRefusesForeignWorkspace: the read consumer's workspace must be
// the repository's (G11).
//
// An access token minted from the read consumer reaches the WHOLE workspace
// (U3), so a consumer of another workspace is a credential that cannot read this
// repository at all — and the refusal is by name, exactly as the read mint
// answers it: the repository is named and the consumer's workspace is not, which
// is one fact less about the installation than the caller already had.
func TestBitbucketGetRefusesForeignWorkspace(t *testing.T) {
	s, f := bitbucketLabelSvcWith(t, func(s *svc, cfg *Config) {
		doc, _, _ := labelBundleDoc(t, testOtherWorkspace)
		s.src.docs = [][]byte{doc}
	})
	resp, body := s.bitbucketGet(t, 7)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("a repository outside the consumer's workspace was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorRequestRefused {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
	}
	if !strings.Contains(e.Message, ErrReadOtherWorkspace.Error()) {
		t.Errorf("the refusal is %q, want it to name the workspace rule (%q)", e.Message, ErrReadOtherWorkspace.Error())
	}
	if strings.Contains(e.Message, testOtherWorkspace) {
		t.Errorf("the refusal names the consumer's workspace: %q", e.Message)
	}
	if n := f.count(); n != 0 {
		t.Errorf("a repository outside the consumer's workspace reached Bitbucket %d times, want none: %+v", n, f.calls())
	}
}

// TestBitbucketGetRefusesScopeOutsideAllowlist: only the read scope allowlist is
// handed on (U11, readScopeAllowed).
//
// The guard is an allowlist and not a list of forbidden writes: Bitbucket
// documents scope words no list here could enumerate, and the token this route
// uses exists to read the installation's repositories and pull requests. A token
// the endpoint says carries anything else is refused rather than used — the
// scope word is named when it has the shape a scope word has, the way out is the
// same sentence the read mint gives, and no pull request is read.
func TestBitbucketGetRefusesScopeOutsideAllowlist(t *testing.T) {
	s, f := bitbucketLabelSvc(t)
	f.scopes = "repository:read pullrequest:read account"
	resp, body := s.bitbucketGet(t, 7)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("a read on a token with an unexpected scope was answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorStateUnavailable, e.Message)
	}
	if !strings.Contains(e.Message, ErrReadUnexpectedScopes.Error()) {
		t.Errorf("the refusal is %q, want it to name the scope rule (%q)", e.Message, ErrReadUnexpectedScopes.Error())
	}
	if !strings.Contains(e.Message, "account") || !strings.Contains(e.Message, readScopeFix) {
		t.Errorf("the refusal is %q, want the scope word and the way out (%q)", e.Message, readScopeFix)
	}
	if reads := f.reads(); len(reads) != 0 {
		t.Errorf("a token with an unexpected scope read the pull request %d times, want none: %+v", len(reads), f.calls())
	}
	if strings.Contains(s.logs.String(), f.token) {
		t.Error("the service's log carries the token it refused to use")
	}

	// The allowlist itself: the endpoint's own bare spelling of the two read
	// nouns is accepted, which is what an installation created before the
	// ":read" suffix existed answers with.
	f.scopes = "repository pullrequest"
	resp, body = s.bitbucketGet(t, 7)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the endpoint's bare read scopes were refused: HTTP %d %s", resp.StatusCode, body)
	}
}

// TestBitbucketGetCachesTheMintedToken: the token this route mints is cached
// until shortly before it expires, and every read of one workspace uses it.
//
// A mint is an act at bitbucket.org with the consumer's own credentials
// presented, so a read per call would be one exchange per tick; the cache is
// keyed by WORKSPACE, because that is what such a token reaches (U3). The last
// step moves the service's clock to inside the route's validity margin and makes
// a third call: the token is held until NEAR expiry, so that call mints again —
// a mutant that cached forever would fail here and a mutant that cached nothing
// would fail on the second call.
func TestBitbucketGetCachesTheMintedToken(t *testing.T) {
	s, f := bitbucketLabelSvc(t)
	for i := 0; i < 2; i++ {
		resp, body := s.bitbucketGet(t, 7)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Bitbucket read %d was refused: HTTP %d %s", i+1, resp.StatusCode, body)
		}
	}
	if got := len(f.mints()); got != 1 {
		t.Fatalf("two reads exchanged the consumer %d times, want one: the token is cached until near expiry", got)
	}
	for i, read := range f.reads() {
		if read.auth != "Bearer "+f.token {
			t.Errorf("read %d was made with %q, want the minted token as its bearer", i+1, read.auth)
		}
	}

	// The mint's expiry is the ENDPOINT's answer measured from the process
	// clock (bitbucket.MintAccessToken takes no clock), so the fixture's own
	// clock is placed one minute before that instant: the cached token is then
	// inside this route's two-minute margin and the next call mints again.
	// The daemon's token is minted AFTER the clock moves, because the identity
	// is verified against the same clock.
	s.fixture.now = time.Now().Add(time.Duration(f.expires)*time.Second - time.Minute)
	tok := s.orchestratorToken()
	resp, body := s.callPerson(t, LabelPath, bitbucketLabelBody(testBitbucketRepo, creds.LabelActionGet, 7, "", ""), tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read after the clock moved was refused: HTTP %d %s", resp.StatusCode, body)
	}
	if got := len(f.mints()); got != 2 {
		t.Errorf("the route exchanged the consumer %d times, want a second one once the cached token is near its expiry", got)
	}
	if got := len(f.reads()); got != 3 {
		t.Errorf("the route read the pull request %d times, want three", got)
	}
}

// ---------------------------------------------------------------------------
// The line
// ---------------------------------------------------------------------------

// TestBitbucketGetAuditNamesReadConsumer: a Bitbucket `get`'s line names the
// READ CONSUMER as its credential and not the App key.
//
// The line is what an operator greps to see which credential an act was
// performed with, and this act's is the installation's read consumer (D2736):
// an installation with Bitbucket alone holds no App key at all, so a line
// naming one would claim a credential this route never read. The purpose is the
// route's own and is unchanged — the act is this route's, and the host is not
// part of it.
func TestBitbucketGetAuditNamesReadConsumer(t *testing.T) {
	s, _ := bitbucketLabelSvc(t)
	resp, body := s.bitbucketGet(t, 7)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the Bitbucket read was refused: HTTP %d %s", resp.StatusCode, body)
	}
	line := s.onlyAuditLine(t)
	if got := auditField(line, "credential"); got != BitbucketReadConsumerSecret {
		t.Errorf("the line's credential = %q, want %q (the App key is the GitHub acts' and was never read)", got, BitbucketReadConsumerSecret)
	}
	if got := auditField(line, "purpose"); got != PurposeLabel {
		t.Errorf("the line's purpose = %q, want %q", got, PurposeLabel)
	}
	if got, want := auditField(line, "repository"), bitbucketSlug(t, testBitbucketRepo); got != want {
		t.Errorf("the line's repository = %q, want the installation's slug %q", got, want)
	}
	// Neither a run nor a job: the caller is the installation's orchestrator
	// daemon, which holds no run identity and is not a job of this installation.
	for _, field := range []string{"run", "job"} {
		if got := auditField(line, field); got != "" {
			t.Errorf("the line carries a %s (%q), and this route's caller has none", field, got)
		}
	}
}

// TestBitbucketLabelGetAuditsEveryCall: once a caller is verified, every call
// writes exactly one line, served or refused (C1.13) — no path writes zero and
// no path writes two.
//
// The five calls below are the five shapes a Bitbucket call can end in: a read
// that was performed, one this route refuses for its shape, one it has no answer
// for, one the bundle cannot serve and one the host refused. Each is a line, and
// the line's status is the answer's own.
func TestBitbucketLabelGetAuditsEveryCall(t *testing.T) {
	get := bitbucketLabelBody(testBitbucketRepo, creds.LabelActionGet, 7, "", "")
	for _, tc := range []struct {
		name string
		body string
		want int
		// credential is what the line names: the read consumer for every call
		// whose HOST was resolved, and the route's own GitHub answer for the one
		// refused before the repository was resolved at all.
		credential string
		mutate     func(*svc, *Config, *bitbucketLabelFake)
	}{
		{name: "performed", body: get, want: http.StatusOK, credential: BitbucketReadConsumerSecret},
		{
			name: "a shape this route does not read", want: StatusRequestRefused,
			body:       bitbucketLabelBody(testBitbucketRepo, creds.LabelActionGet, 0, "", ""),
			credential: config.GitHubAppKeySecret,
		},
		{
			name: "an act this host has no answer for", want: StatusUnavailable,
			body:       bitbucketLabelBody(testBitbucketRepo, creds.LabelActionList, 0, "", ""),
			credential: BitbucketReadConsumerSecret,
		},
		{
			name: "a bundle holding no read consumer", body: get, want: StatusUnavailable,
			credential: BitbucketReadConsumerSecret,
			mutate:     func(s *svc, cfg *Config, f *bitbucketLabelFake) { s.src.docs = [][]byte{mintBundleDoc(t)} },
		},
		{
			name: "a mint bitbucket.org refused", body: get, want: StatusUnavailable,
			credential: BitbucketReadConsumerSecret,
			mutate:     func(s *svc, cfg *Config, f *bitbucketLabelFake) { f.mintStatus = http.StatusUnauthorized },
		},
		{
			name: "a read the host refused", body: get, want: StatusUnavailable,
			credential: BitbucketReadConsumerSecret,
			mutate:     func(s *svc, cfg *Config, f *bitbucketLabelFake) { f.prStatus = http.StatusInternalServerError },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := bitbucketLabelSvcWithFake(t, tc.mutate)
			resp, body := s.bitbucketCall(t, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("the %s call was answered %d, want %d: %s", tc.name, resp.StatusCode, tc.want, body)
			}
			line := s.onlyAuditLine(t)
			if got := auditField(line, "credential"); got != tc.credential {
				t.Errorf("the %s call's line names %q as its credential, want %q", tc.name, got, tc.credential)
			}
		})
	}
}

// TestBitbucketLabelGetNeverReturnsAToken: the token this route mints never
// leaves this process — not in an answer, not in an audit line and not in the
// service's own log — and neither key nor secret of the read consumer does.
//
// That is the whole reason this route performs the act itself rather than
// handing a credential to the daemon (D2439, F5): the daemon asks for a state
// and receives one. The positive half is asserted too, because "no token
// anywhere" would also pass on a route that read nothing: the pull request read
// really was made, and with this token as its bearer.
func TestBitbucketLabelGetNeverReturnsAToken(t *testing.T) {
	s, f := bitbucketLabelSvc(t)
	resp, body := s.bitbucketGet(t, 7)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the Bitbucket read was refused: HTTP %d %s", resp.StatusCode, body)
	}
	reads := f.reads()
	if len(reads) != 1 {
		t.Fatalf("the read reached the API %d times, want one: %+v", len(reads), f.calls())
	}
	if reads[0].auth != "Bearer "+f.token {
		t.Errorf("the read presented %q, want the minted token as its bearer", reads[0].auth)
	}
	for _, value := range []string{f.token, f.key, f.secret} {
		if strings.Contains(string(body), value) {
			t.Error("the answer carries a credential's value")
		}
		if strings.Contains(s.audit.String(), value) {
			t.Error("an audit line carries a credential's value")
		}
		if strings.Contains(s.logs.String(), value) {
			t.Error("the service's log carries a credential's value")
		}
	}
	// The consumer's own values did reach the endpoint that mints: the exchange
	// presented them in its basic authorization, which is the one place of this
	// route they are allowed to be.
	mints := f.mints()
	if len(mints) != 1 || !strings.Contains(mints[0].auth, "Basic ") {
		t.Errorf("the exchange = %+v, want one call presenting the consumer's basic authorization", mints)
	}
	if strings.Contains(mints[0].body, f.secret) {
		t.Error("the exchange carried the consumer's secret in its body, where the endpoint does not read it from")
	}
}

// ---------------------------------------------------------------------------
// The class
// ---------------------------------------------------------------------------

// TestBitbucketLabelGetRefusesOtherClasses: the orchestrator daemon is the only
// caller of this act, whatever audience the token carries (E16, D2049, F19).
//
// The three tokens below are the credentials another class presents — a run's
// own job account, a person, the installation's coordinator — and every one is
// refused before anything is read: no line, because a line names a VERIFIED
// caller and there is none, and no call to Bitbucket. The daemon's own token on
// the same fixture is served, because a class test that only refused callers
// would pass on a route that refused everyone.
func TestBitbucketLabelGetRefusesOtherClasses(t *testing.T) {
	s, f := bitbucketLabelSvc(t)
	callers := []struct {
		name  string
		token func() string
	}{
		{name: "job", token: func() string { return s.jobToken() }},
		{name: "person", token: func() string { return s.personToken(testPersonAccount) }},
		{name: "coordinator", token: func() string { return s.personToken(testCoordinatorAccount) }},
	}
	for _, caller := range callers {
		t.Run(caller.name, func(t *testing.T) {
			before, lines := f.count(), len(s.auditLines(t))
			resp, body := s.callPerson(t, LabelPath,
				bitbucketLabelBody(testBitbucketRepo, creds.LabelActionGet, 7, "", ""), caller.token())
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("a %s caller was answered %d on the label route, want %d: %s", caller.name, resp.StatusCode, StatusUnverified, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("the %s caller's refusal is %q, want %q: %s", caller.name, e.Error, ErrorUnverifiedJobIdentity, e.Message)
			}
			if got := len(s.auditLines(t)); got != lines {
				t.Errorf("a refused %s caller wrote %d audit lines, want none: nothing is verified yet", caller.name, got-lines)
			}
			if after := f.count(); after != before {
				t.Errorf("a refused %s caller reached Bitbucket %d times, want none", caller.name, after-before)
			}
		})
	}
	if resp, body := s.bitbucketGet(t, 7); resp.StatusCode != http.StatusOK {
		t.Fatalf("the orchestrator daemon was refused the Bitbucket read: HTTP %d %s", resp.StatusCode, body)
	}
}

// ---------------------------------------------------------------------------
// The route's own answer
// ---------------------------------------------------------------------------

// TestRouteActLabelPathUnchanged: the second host changed WHICH credential a
// Bitbucket act's line names and nothing about the route's own answer.
//
// routeAct(LabelPath) is what every GitHub act of this route has always been
// keyed by, and the console, the history and an operator's greps read those
// lines by it; a Bitbucket `get` names the read consumer through labelCredential
// instead, which is a decision about one host's acts and not a change to the
// rule. The read mint's own line is asserted beside it so that the two
// credentials cannot be swapped by a later edit.
func TestRouteActLabelPathUnchanged(t *testing.T) {
	purpose, credential := routeAct(LabelPath)
	if purpose != PurposeLabel || credential != config.GitHubAppKeySecret {
		t.Errorf("routeAct(%s) = (%q, %q), want (%q, %q)", LabelPath, purpose, credential, PurposeLabel, config.GitHubAppKeySecret)
	}
	if got := labelCredential(false); got != credential {
		t.Errorf("labelCredential(GitHub) = %q, want the route's own answer %q", got, credential)
	}
	if got := labelCredential(true); got != BitbucketReadConsumerSecret {
		t.Errorf("labelCredential(Bitbucket) = %q, want %q", got, BitbucketReadConsumerSecret)
	}
	if rp, rc := routeAct(BitbucketReadPath); rp != PurposeBitbucketRead || rc != BitbucketReadConsumerSecret {
		t.Errorf("routeAct(%s) = (%q, %q), want (%q, %q)", BitbucketReadPath, rp, rc, PurposeBitbucketRead, BitbucketReadConsumerSecret)
	}
	if purpose == PurposeBitbucketRead {
		t.Error("the label route and the read mint share a purpose, so a line cannot tell the two acts apart")
	}
}
