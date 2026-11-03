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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
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

// readConsumer is a bundle document holding the read consumer beside the model
// credentials — what an installation that has run `fugaro creds set
// bitbucket-read-consumer` holds (BB2, F21) — together with the key and the
// secret it carries, built in one place so a test can hold the exchange's
// Authorization header to the bundle's own value without either being written
// down.
func readMintBundleDoc(t *testing.T, workspace string) (doc []byte, key, secret string) {
	t.Helper()
	key, secret = fakeCred(t, "consumer-key"), fakeCred(t, "consumer-secret")
	return append(testBundleDoc(t), []byte(BitbucketReadConsumerSecret+":\n"+
		"  key: "+key+"\n"+
		"  secret: "+secret+"\n"+
		"  workspace: "+workspace+"\n")...), key, secret
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
	refresh  string
	expires  int
	refusals int
	// key and secret are the read consumer the fixture's bundle holds, so a
	// test can hold what the exchange was asked to it without either being
	// written down.
	key, secret string
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
		status, token, scopes, refresh, expires := f.refusals, f.token, f.scopes, f.refresh, f.expires
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
		if refresh != "" {
			answer["refresh_token"] = refresh
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
	return bitbucketSvcWith(t, nil, people...)
}

// bitbucketSvcWith is bitbucketSvc with the service's own configuration under
// the test's control, for the cases that need one wired differently: an audit
// log that cannot write, an inventory holding a second workspace's repository.
// The configuration is set here and not read off the fixture afterwards,
// because NewServer takes the Config by value: what a test changes after the
// server is built is not what the server reads.
func bitbucketSvcWith(t *testing.T, mutate func(*svc, *Config), people ...string) (*svc, *bitbucketFake) {
	t.Helper()
	f := newBitbucketFake(t)
	doc, key, secret := readMintBundleDoc(t, testWorkspace)
	f.key, f.secret = key, secret
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		ac := s.auth.cfg
		ac.PersonAccounts = people
		a, err := NewAuthenticator(ac)
		if err != nil {
			t.Fatalf("NewAuthenticator: %v", err)
		}
		cfg.Auth = a
		cfg.Repos = map[string]string{bitbucketSlug(t, testBitbucketRepo): testCanonical(t, testBitbucketRepo)}
		cfg.Client = f.exchange()
		s.src.docs = [][]byte{doc}
		if mutate != nil {
			mutate(s, cfg)
		}
	})
	return s, f
}

// testSlug is one repository's slug, the key the installation's inventory holds
// it under, and testCanonical is the canonical name that inventory maps it to.
// Both are derived rather than spelled, because they are the two derivations
// the service itself resolves a repository through (task.Slug,
// projectpolicy.Canonical) and a test that spelled either could pass while the
// service looked somewhere else.
func bitbucketSlug(t *testing.T, repo string) string {
	t.Helper()
	slug, err := task.Slug(gitprov.KindBitbucket, repo)
	if err != nil {
		t.Fatalf("slug of %s: %v", repo, err)
	}
	return slug
}

func testCanonical(t *testing.T, repo string) string {
	t.Helper()
	canonical, err := projectpolicy.Canonical(gitprov.KindBitbucket, repo)
	if err != nil {
		t.Fatalf("canonical name of %s: %v", repo, err)
	}
	return canonical
}

// readMint sends one read mint for repo as person and returns the answer. It
// is the shape every test below starts from, whether the call is admitted or
// refused.
func (s *svc) readMint(t *testing.T, repo, person string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"repository": repo})
	if err != nil {
		t.Fatalf("building the call's body: %v", err)
	}
	return s.callPerson(t, readTokenPath, string(body), s.personToken(person), mutate...)
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

// TestPersonCallerRefusedOtherAccounts: the class is a list, and an address
// that is not on it is refused whether or not it is a real person's. The
// admission is the installation's operators and no other account (BB4), so a
// token this service would not mint for a job is not minted for a stranger
// either.
func TestPersonCallerRefusedOtherAccounts(t *testing.T) {
	s, f := bitbucketSvc(t, testPersonAccount)

	resp, body := s.readMint(t, testBitbucketRepo, testOtherPerson)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the read mint admitted an account the person class does not hold: HTTP %d %s", resp.StatusCode, body)
	}
	if n := len(f.seen()); n != 0 {
		t.Errorf("the service exchanged the read consumer %d times for a caller it refused, want none", n)
	}
	// An unverified caller has no name for a line: the refusal is typed and the
	// audit log stays empty (audit.go: a line's caller is what this service
	// verified, and there is none).
	if got := s.audit.String(); got != "" {
		t.Errorf("a refusal taken before the caller was verified wrote an audit line: %s", got)
	}
}

// TestPersonCallerTakesNoRunIdentity: the class takes a person's own token and
// nothing else. A caller that presents a run identity beside it is refused by
// name, which is what keeps this route from being a second way in for a run —
// the property G4 asks for in as many words.
func TestPersonCallerTakesNoRunIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra func(*http.Request)
	}{
		{"a run identity", func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity-token") }},
		{"a job name", func(r *http.Request) { r.Header.Set(HeaderRunJob, "fugaro-a-job") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := bitbucketSvc(t, testPersonAccount)
			resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount, tc.extra)
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the read mint answered a call presenting %s: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
				t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
			}
			if n := len(f.seen()); n != 0 {
				t.Errorf("the service exchanged the read consumer %d times for a call it refused, want none", n)
			}
			// The refusal IS recorded: the caller was verified, so the line
			// names the person and the act they asked for, and it carries no
			// run and no job.
			lines := auditLines(t, s.audit)
			if len(lines) != 1 {
				t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			checkAuditShape(t, lines[0])
			for field, want := range map[string]any{
				"caller":     testPersonAccount,
				"purpose":    PurposeBitbucketRead,
				"status":     ErrorUnverifiedRunIdentity,
				"run":        "",
				"job":        "",
				"tier":       "",
				"repository": "",
			} {
				if got := lines[0][field]; got != want {
					t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
				}
			}
		})
	}
}

// TestReadMintUsesOnlyTheReadKind: the token is minted from the read consumer
// and from nothing else (G3, F21). The bundle holds the model credentials
// beside it, and the endpoint is presented the read consumer's own key and
// secret — the value the bundle carries under that document key and not
// another entry's.
func TestReadMintUsesOnlyTheReadKind(t *testing.T) {
	s, f := bitbucketSvc(t, testPersonAccount)
	resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	seen := f.seen()
	if len(seen) != 1 {
		t.Fatalf("the service exchanged the read consumer %d times, want one", len(seen))
	}
	if seen[0].key != f.key || seen[0].secret != f.secret {
		t.Error("the exchange did not present the read consumer's own key and secret: the bundle holds one entry this route may mint from, and it is the one it read")
	}
	if seen[0].grant != "grant_type=client_credentials" {
		t.Errorf("the exchange sent the grant %q, want the client_credentials grant alone", seen[0].grant)
	}
}

// TestReadMintRefusesWriteScopes: when the token answer lists scopes (U11), a
// token carrying any write scope is refused and audited. It holds even if the
// installing person created the "read" consumer with write scopes — the point
// of the check is that this route does not trust the name the operator gave the
// consumer, it trusts what the endpoint says the token can do.
func TestReadMintRefusesWriteScopes(t *testing.T) {
	for _, tc := range []struct{ name, scopes string }{
		{"a write scope", "repository:write pullrequest:read"},
		{"an admin scope", "repository:admin"},
		{"a delete scope", "pullrequest:delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := bitbucketSvc(t, testPersonAccount)
			f.scopes = tc.scopes
			resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the read mint handed out a token the endpoint says can write: HTTP %d %s", resp.StatusCode, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorStateUnavailable || !strings.Contains(e.Message, ErrReadWriteScopes.Error()) {
				t.Errorf("the refusal is %+v, want state_unavailable and the write-scope class", e)
			}
			// The value reaches nobody: not the answer, and not the operator's
			// own log, which names the offending SCOPE and never the token.
			for _, where := range []struct{ what, text string }{{"the answer", string(body)}, {"the service's log", s.logs.String()}} {
				if strings.Contains(where.text, f.token) {
					t.Fatalf("%s carries the token a refused read mint was handed", where.what)
				}
			}
			lines := auditLines(t, s.audit)
			if len(lines) != 1 {
				t.Fatalf("the refused mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			for field, want := range map[string]any{
				"caller":     testPersonAccount,
				"repository": bitbucketSlug(t, testBitbucketRepo),
				"purpose":    PurposeBitbucketRead,
				"credential": BitbucketReadConsumerSecret,
				"status":     ErrorStateUnavailable,
			} {
				if got := lines[0][field]; got != want {
					t.Errorf("the refused mint's audit line %s = %v, want %q", field, got, want)
				}
			}
		})
	}

	// The other half, and the reason the rule is written to the endpoint's
	// words rather than to the word "read": a list that names reads and the
	// bare nouns bitbucket.org writes for them is not a write scope, and an
	// answer that lists nothing is the "the endpoint did not say" case
	// (oauth.go's Minted.Scopes) rather than a refusal.
	for _, tc := range []struct{ name, scopes string }{
		{"bare read nouns", "repository pullrequest account"},
		{"reads spelled out", "repository:read pullrequest:read"},
		{"no scopes listed at all", ""},
	} {
		t.Run(tc.name+" is not a write scope", func(t *testing.T) {
			s, f := bitbucketSvc(t, testPersonAccount)
			f.scopes = tc.scopes
			resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the read mint refused a token whose scopes are reads: HTTP %d %s", resp.StatusCode, body)
			}
		})
	}
}

// TestReadMintRefusesAnotherWorkspace: a repository of this installation that
// is not in the read consumer's workspace is refused, BY NAME (G11). The
// refusal names the repository the caller asked about and not the workspace the
// consumer is scoped to, and nothing is exchanged.
func TestReadMintRefusesAnotherWorkspace(t *testing.T) {
	other := testElsewhereRepo
	s, f := bitbucketSvcWith(t, func(_ *svc, cfg *Config) {
		cfg.Repos[bitbucketSlug(t, other)] = testCanonical(t, other)
	}, testPersonAccount)

	resp, body := s.readMint(t, testElsewhereRepo, testPersonAccount)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the read mint answered for a repository of another workspace: HTTP %d %s", resp.StatusCode, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if !strings.Contains(e.Message, ErrReadOtherWorkspace.Error()) {
		t.Errorf("the refusal is %q, want the other-workspace class", e.Message)
	}
	if !strings.Contains(e.Message, testElsewhereRepo) {
		t.Errorf("the refusal does not name the repository it refused: %q", e.Message)
	}
	if strings.Contains(e.Message, testWorkspace) {
		t.Errorf("the refusal names the workspace the read consumer is scoped to: %q", e.Message)
	}
	if n := len(f.seen()); n != 0 {
		t.Errorf("the service exchanged the read consumer %d times for another workspace, want none", n)
	}
	// And a repository the inventory does NOT hold is refused the same way and
	// for the plainer reason: the workspace is resolved from the installation's
	// own table, so a name no row holds has none (ErrReadUnknownRepository).
	missing, _ := bitbucketSvc(t, testPersonAccount)
	resp, body = missing.readMint(t, testElsewhereRepo, testPersonAccount)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the read mint answered for a repository the installation does not hold: HTTP %d %s", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrReadUnknownRepository.Error()) {
		t.Errorf("the refusal is %q, want the unknown-repository class", e.Message)
	}
}

// TestReadMintRepeatsNoRepositoryTextThatIsNotAName: the repository a refusal
// names is the CALLER'S OWN TEXT, and readReadRequest holds it to
// gitprov.SplitRepo — which is permissive on purpose, because a repository path
// is the caller's to spell. So the echo is held to the stricter rule as well
// (task.CanonicalRepo): a text that is not an "owner/name" this program would
// read is refused all the same and named as the field it is.
//
// The three shapes below are why: a relative path names something the service
// never looked at, an escape sequence rewrites the terminal an operator reads
// the refusal in, and a newline forges a line in the text log the same refusal
// goes to. Each is checked in the answer AND in the service's own log, because
// a refusal that is clean on the wire and hostile in the log is the half of
// this that an operator is the one to suffer.
func TestReadMintRepeatsNoRepositoryTextThatIsNotAName(t *testing.T) {
	for _, tc := range []struct {
		repo string
		// class is the refusal the call is owed, and it is nil for the values
		// readReadRequest refuses before a repository is resolved at all (no
		// "owner/name" shape whatever): those reach no echo either, and the
		// test holds them to the same rule.
		class error
	}{
		{"../repo-a", ErrReadUnknownRepository},
		{"\x1b[31m/repo-a", ErrReadUnknownRepository},
		{testWorkspace + "/repo-a\x1b[0m", ErrReadUnknownRepository},
		{testWorkspace + "/repo-a\ncredsvc: a line nobody wrote", ErrReadUnknownRepository},
		{testWorkspace + "/repo-a\rforged", ErrReadUnknownRepository},
		{"\x1b[31mrepo-a", nil},
		{testWorkspace + "/../repo-a", nil},
		{testWorkspace + "/repo-a/../..", nil},
	} {
		t.Run(fmt.Sprintf("%q", tc.repo), func(t *testing.T) {
			s, f := bitbucketSvc(t, testPersonAccount)
			resp, body := s.readMint(t, tc.repo, testPersonAccount)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the read mint answered a call naming %q: HTTP %d %s", tc.repo, resp.StatusCode, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, ErrorRequestRefused, e.Message)
			}
			for _, where := range []struct{ what, text string }{
				{"the refusal", e.Message},
				{"the service's log", s.logs.String()},
			} {
				if strings.Contains(where.text, tc.repo) {
					t.Errorf("%s repeats the text the call sent: %q", where.what, where.text)
				}
			}
			// The refusal still says which class it is — a repository the
			// installation does not hold, or the call's own missing shape — so
			// an operator reading it has a reason and not just a no.
			want := "the call names no repository"
			if tc.class != nil {
				want = tc.class.Error()
			}
			if !strings.Contains(e.Message, want) {
				t.Errorf("the refusal is %q, want it to name the class it refused (%s)", e.Message, want)
			}
			if n := len(f.seen()); n != 0 {
				t.Errorf("the service exchanged the read consumer %d times for a call it refused, want none", n)
			}
		})
	}
}

// TestReadMintStillNamesARepositoryItRefused: the rule above is about text that
// is not a repository name, and a caller that spells one the service does not
// hold still hears which one it was — the reason an operator reads a refusal at
// all (G11).
func TestReadMintStillNamesARepositoryItRefused(t *testing.T) {
	s, _ := bitbucketSvc(t, testPersonAccount)
	repo := testOtherWorkspace + "/repo-not-held"
	resp, body := s.readMint(t, repo, testPersonAccount)
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the read mint answered for a repository the installation does not hold: HTTP %d %s", resp.StatusCode, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if !strings.Contains(e.Message, ErrReadUnknownRepository.Error()) {
		t.Errorf("the refusal is %q, want the unknown-repository class", e.Message)
	}
	if !strings.Contains(e.Message, repo) {
		t.Errorf("the refusal does not name the repository it refused: %q", e.Message)
	}
}

// TestReadMintAuditedOnce: one granted mint writes exactly one line, and the
// line is the read mint's own shape — the person as the caller, the repository
// as the installation's slug, the read consumer as the credential, and NEITHER
// a run nor a job. It is the relaxation BB4 extends, pinned from the side that
// matters: what a granted mint's line carries.
func TestReadMintAuditedOnce(t *testing.T) {
	s, _ := bitbucketSvc(t, testPersonAccount)
	resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("a granted mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	for field, want := range map[string]any{
		"caller":     testPersonAccount,
		"repository": bitbucketSlug(t, testBitbucketRepo),
		"run":        "",
		"job":        "",
		"tier":       "",
		"purpose":    PurposeBitbucketRead,
		"credential": BitbucketReadConsumerSecret,
		"status":     StatusOK,
	} {
		if got := lines[0][field]; got != want {
			t.Errorf("the granted mint's audit line %s = %v, want %q", field, got, want)
		}
	}
}

// TestReadMintFailsClosedUnaudited: a token bitbucket.org granted and this
// service could not record reaches nobody (G2, D143). The exchange HAS
// happened — a Bitbucket access token cannot be unminted — so what "not made"
// means here is that no answer, no log line and no error carries it.
func TestReadMintFailsClosedUnaudited(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler slog.Handler
	}{
		{"a handler that fails to write", &recordHandler{err: errors.New("the log is not recording")}},
		{"a log that does not record the level", &levelOffHandler{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The handler is handed to the SERVICE and not to an auditor built
			// here: the read mint writes through the auditor the service
			// builds for every line it writes (NewServer's NewProbeAuditor),
			// so a handler the service cannot write through is one the mint
			// cannot either.
			s, f := bitbucketSvcWith(t, func(_ *svc, cfg *Config) { cfg.AuditLog = tc.handler }, testPersonAccount)
			resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("a mint that could not be audited answered %d, want %d: the token is handed to nobody", resp.StatusCode, StatusUnavailable)
			}
			if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrReadUnaudited.Error()) {
				t.Errorf("the refusal is %q, want the unaudited class", e.Message)
			}
			if n := len(f.seen()); n != 1 {
				t.Errorf("the endpoint was exchanged %d times, want one: the token was minted and then withheld", n)
			}
			for _, where := range []struct{ what, text string }{{"the answer", string(body)}, {"the service's log", s.logs.String()}} {
				if strings.Contains(where.text, f.token) {
					t.Fatalf("%s carries the token of a mint that could not be audited", where.what)
				}
			}
		})
	}
}

// TestReadMintRefusedWithoutReadConsumer: the bundle holding no read consumer
// is a refusal and never a fallback (D143). An installation that has not run
// `fugaro creds set bitbucket-read-consumer` has no read mint, and the model
// credentials the same bundle holds are not something this route may mint from.
func TestReadMintRefusedWithoutReadConsumer(t *testing.T) {
	s, f := bitbucketSvc(t, testPersonAccount)
	s.src.docs = [][]byte{testBundleDoc(t)}

	resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the read mint answered with no read consumer in the bundle: HTTP %d %s", resp.StatusCode, body)
	}
	if e := refusal(t, resp.StatusCode, body); !strings.Contains(e.Message, ErrReadNoConsumer.Error()) {
		t.Errorf("the refusal is %q, want the no-read-consumer class", e.Message)
	}
	if n := len(f.seen()); n != 0 {
		t.Errorf("the service exchanged an endpoint %d times with no read consumer in the bundle, want none", n)
	}
}

// TestReadMintNeverForwardsRefreshToken: Bitbucket may answer the grant with a
// refresh token beside the access token. The service hands on the access token
// alone (G5), and the refresh token — the value that would let a holder mint
// again without the consumer — appears nowhere a caller or an operator can
// read.
func TestReadMintNeverForwardsRefreshToken(t *testing.T) {
	s, f := bitbucketSvc(t, testPersonAccount)
	f.refresh = fakeCred(t, "refresh-token")
	f.scopes = "repository:read"

	resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	for _, where := range []struct{ what, text string }{
		{"the answer", string(body)},
		{"the service's log", s.logs.String()},
		{"the audit line", s.audit.String()},
	} {
		if strings.Contains(where.text, f.refresh) {
			t.Fatalf("%s carries the refresh token", where.what)
		}
	}
	if !strings.Contains(string(body), f.token) {
		t.Error("the answer does not carry the access token, so this test is not reading the answer it thinks")
	}
}

// TestReadMintTakesNoTokenURLFromCaller: the endpoint a consumer's key and
// secret are presented to is this service's constant (T2.5, G10), and no part
// of a call can move it. A body that tries is refused by the strict decoder —
// this contract has one field — and the exchange, when it happens, goes to
// bitbucket.org's own OAuth root and nowhere else.
func TestReadMintTakesNoTokenURLFromCaller(t *testing.T) {
	s, f := bitbucketSvc(t, testPersonAccount)
	for _, tc := range []struct{ name, body string }{
		{"a token endpoint", `{"repository":"` + testBitbucketRepo + `","token_url":"https://elsewhere.invalid/token"}`},
		{"an api root", `{"repository":"` + testBitbucketRepo + `","api_url":"https://elsewhere.invalid/2.0"}`},
		{"a workspace", `{"repository":"` + testBitbucketRepo + `","workspace":"` + testOtherWorkspace + `"}`},
		{"a scope", `{"repository":"` + testBitbucketRepo + `","scopes":"repository:write"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := s.callPerson(t, readTokenPath, tc.body, s.personToken(testPersonAccount))
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the read mint read a body naming %s: HTTP %d %s", tc.name, resp.StatusCode, body)
			}
			if n := len(f.seen()); n != 0 {
				t.Errorf("the service exchanged the read consumer %d times for a body this contract refuses, want none", n)
			}
		})
	}

	// The exchange that does happen goes to the root this package pins. The
	// fake's own transport refuses any host but bitbucket.org, so this holds
	// the whole path and not just the URL this test can see.
	if bitbucket.DefaultOAuthRoot != "https://bitbucket.org" {
		t.Errorf("the OAuth root = %q, want bitbucket.org's own: a consumer's key and secret are presented to that host alone", bitbucket.DefaultOAuthRoot)
	}
	resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the read mint refused an admitted person: HTTP %d %s", resp.StatusCode, body)
	}
	seen := f.seen()
	if len(seen) != 1 || seen[0].path != "/site/oauth2/access_token" {
		t.Errorf("the exchange was sent to %v, want the OAuth token path once", seen)
	}
}

// TestReadMintRefusedWhenStopped: the global kill switch stops the person class
// too, and it does so before the bundle is read — a stopped service reads no
// credential at all. The repository's own switch does NOT stop it: this route
// has no run, and a repository's switch is about that repository's runs rather
// than about a person's read (F34).
func TestReadMintRefusedWhenStopped(t *testing.T) {
	t.Run("the global switch", func(t *testing.T) {
		s, f := bitbucketSvc(t, testPersonAccount)
		s.kills.global = true
		resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a stopped service answered the read mint: HTTP %d %s", resp.StatusCode, body)
		}
		if n := len(f.seen()); n != 0 {
			t.Errorf("a stopped service exchanged the read consumer %d times, want none", n)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("a stopped service read the bundle %d times, want none", n)
		}
		lines := auditLines(t, s.audit)
		if len(lines) != 1 {
			t.Fatalf("the stopped mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		for field, want := range map[string]any{
			"caller":     testPersonAccount,
			"purpose":    PurposeBitbucketRead,
			"credential": BitbucketReadConsumerSecret,
			"status":     ErrorStateUnavailable,
			"repository": "",
		} {
			if got := lines[0][field]; got != want {
				t.Errorf("the stopped mint's audit line %s = %v, want %q", field, got, want)
			}
		}
	})

	t.Run("a repository's own switch is not this route's", func(t *testing.T) {
		s, _ := bitbucketSvc(t, testPersonAccount)
		s.kills.repos[bitbucketSlug(t, testBitbucketRepo)] = true
		resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a repository's own stop switch stopped a person's read: HTTP %d %s", resp.StatusCode, body)
		}
	})
}

// TestPersonClassEmptyListAdmitsNobody: the service running with no operators
// configured admits nobody at all. An empty list is not a list of everybody,
// which is the reading a default could take and this one does not: the class is
// the installation's own list, and an installation that published none has
// published no admission.
func TestPersonClassEmptyListAdmitsNobody(t *testing.T) {
	s, f := bitbucketSvc(t)
	resp, body := s.readMint(t, testBitbucketRepo, testPersonAccount)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the read mint admitted somebody with no operators configured: HTTP %d %s", resp.StatusCode, body)
	}
	if n := len(f.seen()); n != 0 {
		t.Errorf("the service exchanged the read consumer %d times for a caller it refused, want none", n)
	}
}

// TestNewRoutesAuditConsumerKind: routeAct and the route list name the read
// route, so a refusal taken BEFORE the route is branched on — the binding, the
// stop switches, the 404 — audits the read consumer's kind and never the App
// key's name. The path is spelled here as the contract spells it and held to
// the constant and to the client that calls it, so a rename of either cannot
// quietly move the route.
func TestNewRoutesAuditConsumerKind(t *testing.T) {
	if BitbucketReadPath != readTokenPath {
		t.Errorf("the read mint's path = %q, want %q", BitbucketReadPath, readTokenPath)
	}
	if bitbucket.ServiceReadPath != BitbucketReadPath {
		t.Errorf("the client's read path = %q and the service's = %q, want one path", bitbucket.ServiceReadPath, BitbucketReadPath)
	}
	purpose, credential := routeAct(BitbucketReadPath)
	if purpose != PurposeBitbucketRead || credential != BitbucketReadConsumerSecret {
		t.Errorf("routeAct(%s) = (%q, %q), want (%q, %q)", BitbucketReadPath, purpose, credential, PurposeBitbucketRead, BitbucketReadConsumerSecret)
	}
	// The credential a read route's line names is its own and not the mint's:
	// two endpoints, two credentials, and a line that named the App key for a
	// Bitbucket read would send an operator looking for the wrong secret.
	if other, _ := routeAct(MintPath); other == purpose {
		t.Errorf("the read route and the mint share the purpose %q, so a line cannot tell them apart", purpose)
	}

	// A route this service does not serve names the new path among the ones it
	// does, which is the half of "the route list names it" that a caller sees.
	s, _ := bitbucketSvc(t, testPersonAccount)
	resp, body := s.callPerson(t, "/v1/bitbucket/read", "", s.personToken(testPersonAccount))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown path answered %d, want 404: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), BitbucketReadPath) {
		t.Errorf("the not-found answer does not name %s among the routes this service serves: %s", BitbucketReadPath, body)
	}

	// And the refusal taken before the branch — the global stop switch is read
	// in ServeHTTP's own path for everything but this route's two cousins —
	// carries the read consumer's name and not the App key's.
	stopped, _ := bitbucketSvc(t, testPersonAccount)
	stopped.kills.global = true
	if _, body := stopped.readMint(t, testBitbucketRepo, testPersonAccount); strings.Contains(string(body), config.GitHubAppKeySecret) {
		t.Errorf("a stopped read mint's refusal names the App key: %s", body)
	}
	lines := auditLines(t, stopped.audit)
	if len(lines) != 1 {
		t.Fatalf("the stopped mint wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	if got := lines[0]["credential"]; got != BitbucketReadConsumerSecret {
		t.Errorf("the stopped read mint's audit line credential = %v, want %q", got, BitbucketReadConsumerSecret)
	}
}
