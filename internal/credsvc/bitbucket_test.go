package credsvc

// The Bitbucket read mint's tests (BB4, B1/B2): the person caller class the
// service admits on one route, the one repository a call names, the read
// consumer the token is minted from, and the line every mint leaves.
//
// Every fixture here is built at run time: the consumer's key and secret come
// from fakeCred, and the token the fake endpoint answers with is this file's
// own text (Convention 2). No literal in this file is, or looks like, a
// credential.

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// readTokenPath is the read mint's path, spelled as the contract spells it.
// It is a literal on purpose: the tests below hold the constant to it, so a
// rename of either cannot quietly move the route (the rule
// internal/credsvc/bundle_test.go's readConsumerEntry is held to).
const readTokenPath = "/v1/bitbucket/read-token"

// The names these tests stand on. The workspace and the repository are names
// and not credentials: the read mint refuses a repository of another
// workspace by name (G11), and no line this service writes carries a value.
const (
	testWorkspace      = "workspace-a"
	testOtherWorkspace = "workspace-b"
	testBitbucketRepo  = testWorkspace + "/repo-a"
	testElsewhereRepo  = testOtherWorkspace + "/repo-elsewhere"
	// The people an installation admits: bare addresses and never IAM member
	// spellings, exactly as AuthConfig.PersonAccounts holds them.
	testPersonAccount = "person-1@example.invalid"
	testOtherPerson   = "person-2@example.invalid"
)

// bitbucketReadConsumerDoc is a bundle document holding the read consumer
// beside the model credentials: what an installation that has run `fugaro
// creds set bitbucket-read-consumer` holds (BB2, F21).
func bitbucketReadConsumerDoc(t *testing.T, workspace string) []byte {
	t.Helper()
	return append(testBundleDoc(t), []byte(BitbucketReadConsumerSecret+":\n"+
		"  key: "+fakeCred(t, "consumer-key")+"\n"+
		"  secret: "+fakeCred(t, "consumer-secret")+"\n"+
		"  workspace: "+workspace+"\n")...)
}

// bitbucketFake stands in for bitbucket.org's token endpoint: the one host the
// read mint's exchange reaches (T2.5, G10). It answers the client_credentials
// grant the way the endpoint does, and it records what it was asked so a test
// can hold the exchange to the path and the grant it must send.
type bitbucketFake struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []fakeExchange
	// token and scopes are the answer's, and refusal, when it is not zero, is
	// the status the endpoint refuses the grant with instead.
	token    string
	scopes   string
	expires  int
	refusals int
}

// fakeExchange is one exchange the fake endpoint answered: where it was sent
// and what it carried. The Authorization header is recorded because the
// consumer's own key and secret travel in it and a test asserts what the
// service sent without ever writing either down.
type fakeExchange struct {
	path   string
	grant  string
	key    string
	secret string
}

func newBitbucketFake(t *testing.T) *bitbucketFake {
	t.Helper()
	f := &bitbucketFake{
		token:   fakeCred(t, "read-token"),
		scopes:  "repository:read",
		expires: 7200,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			t.Errorf("reading the exchange: %v", err)
		}
		key, secret, _ := r.BasicAuth()
		f.mu.Lock()
		f.requests = append(f.requests, fakeExchange{
			path:   r.URL.Path,
			grant:  string(body),
			key:    key,
			secret: secret,
		})
		status, token, scopes, expires := f.refusals, f.token, f.scopes, f.expires
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		answer := map[string]any{"access_token": token, "expires_in": expires}
		if scopes != "" {
			answer["scopes"] = scopes
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// exchange is the http.Client the service makes its exchange with: every
// request is sent to the fake whatever host the service built the URL for, and
// a request to a host that is not Bitbucket's own OAuth root is refused rather
// than answered. That refusal is what pins the root: the route mints from the
// constant in internal/gitprov/bitbucket and from no environment value and no
// request field (T2.5, G10).
func (f *bitbucketFake) exchange() *http.Client {
	base, err := url.Parse(f.srv.URL)
	if err != nil {
		panic("the fake's URL is not a URL: " + err.Error())
	}
	return &http.Client{Transport: bitbucketTransport{base: base}}
}

type bitbucketTransport struct{ base *url.URL }

func (h bitbucketTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "bitbucket.org" {
		return nil, fmt.Errorf("the read mint exchanged a consumer with %s://%s, and a consumer is presented to bitbucket.org alone", r.URL.Scheme, r.URL.Host)
	}
	c := r.Clone(r.Context())
	c.URL.Scheme = h.base.Scheme
	c.URL.Host = h.base.Host
	c.Host = h.base.Host
	// The fake answers over a plain test listener; the client the service
	// built for the real endpoint keeps its own TLS config, which is not this
	// transport's to change, so the hop is dialled with this one.
	return (&http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}).RoundTrip(c)
}

// exchanges is what the endpoint was asked, in order.
func (f *bitbucketFake) seen() []fakeExchange {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeExchange(nil), f.requests...)
}

// bitbucketSvc is the installation under test with the read mint's three
// fixtures in place: a Bitbucket repository in its inventory, the read
// consumer in its bundle, and the people this call names admitted as the
// person class.
func bitbucketSvc(t *testing.T, people ...string) (*svc, *bitbucketFake) {
	t.Helper()
	f := newBitbucketFake(t)
	slug, err := task.Slug(gitprov.KindBitbucket, testBitbucketRepo)
	if err != nil {
		t.Fatalf("slug of %s: %v", testBitbucketRepo, err)
	}
	canonical, err := projectpolicy.Canonical(gitprov.KindBitbucket, testBitbucketRepo)
	if err != nil {
		t.Fatalf("canonical name of %s: %v", testBitbucketRepo, err)
	}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		ac := s.auth.cfg
		ac.PersonAccounts = people
		a, err := NewAuthenticator(ac)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		cfg.Auth = a
		cfg.Repos = map[string]string{slug: canonical}
		cfg.Client = f.exchange()
		s.src.docs = [][]byte{bitbucketReadConsumerDoc(t, testWorkspace)}
	})
	return s, f
}

// personToken is the Google ID token a person presents: a token of the same
// kind Cloud Run IAM admits a caller with, naming an email and no run
// (C3.9f, BB4).
func (s *svc) personToken(email string, mutate ...func(map[string]any)) string {
	claims := []func(map[string]any){func(c map[string]any) { c["email"] = email }}
	return s.jobToken(append(claims, mutate...)...)
}

// callPerson sends one call as a person: a Google ID token in Authorization
// and nothing else. It is the shape of every call the read mint answers, and
// of no call any other route has.
func (s *svc) callPerson(t *testing.T, path, body, tok string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	for _, m := range mutate {
		m(req)
	}
	resp, err := s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// readAnswer is the read mint's answer as a test reads it. It is deliberately
// this file's own struct and not the service's: what the contract carries is
// the test's business, and the fields are named here so a rename in the
// service is a failure and not a silent pass.
type readAnswer struct {
	Token      string `json:"token"`
	ExpiresAt  string `json:"expires_at"`
	Repository string `json:"repository"`
}

// TestPersonCallerOnlyOnReadRoute: the person class is admitted on the read
// mint and on nothing else. It is the service's first admission that is not a
// job of the installation (BB4), so the property that matters is the route's:
// a person's token opens the read mint, and the same token presented beside a
// whole run identity is refused on the model path, which is every other
// caller's route.
func TestPersonCallerOnlyOnReadRoute(t *testing.T) {
	s, _ := bitbucketSvc(t, testPersonAccount)

	resp, body := s.callPerson(t, readTokenPath, `{"repository":"`+testBitbucketRepo+`"}`, s.personToken(testPersonAccount))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	var answer readAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("the answer is not one this contract can read: %v (%d bytes)", err, len(body))
	}
	if answer.Token == "" || answer.ExpiresAt == "" || answer.Repository != testBitbucketRepo {
		t.Errorf("the answer does not name the token, its life and the repository: %+v", answer)
	}

	// The same person, on the route every other caller uses: the run identity
	// beside the token is whole, and the caller is not a job of this
	// installation, so the class admits nobody here.
	resp = s.postTokens(t, MessagesPath, callBody("person-on-the-model-path"), s.runToken(), s.personToken(testPersonAccount), s.job)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the model path admitted a person: HTTP %d %s", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedJobIdentity)
	}
}
