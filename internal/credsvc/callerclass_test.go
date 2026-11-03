package credsvc

// The caller-class matrix (BB4, G4): every class this service admits, on every
// route it serves, admitted or refused exactly as designed.
//
// The classes are four — a per-repository job, a review job, a verifier and a
// person — and each is matched against its OWN list (verifyJob against the job
// accounts, VerifyVerifier against the verifier list, VerifyPerson against the
// people). "Which list a caller is looked up in is what makes it a class" is
// the whole of the design, and it is a property no single-route test can hold:
// a mutant that made VerifyPerson also read the job or verifier lists left
// every test in this package green, because each of them only ever presented
// the token of the class it was about.
//
// So this file presents all four to all five routes. The table below is the
// design written down, cell by cell, and a cell that answers something else —
// including a route that answers 200 to a caller of another class — is a
// failure that names itself.
//
// The refusals a class that holds no run identity gets on the three routes the
// shared parse reads (mint, sign, messages) are that parse's own
// (`unverified_run_identity`) and not the class's: a verifier and a person
// cannot build the call those routes are read by at all, which is why they are
// answered ahead of RequestFromHTTP on their own routes. That is the designed
// answer for those cells and not an oversight of the matrix.

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// classMatrixBundleDoc is the one document every route of the matrix reads:
// the model credentials, the GitHub App key a mint signs with, the review
// signing entry of the account the review job is of, and the read consumer a
// read token is minted from.
//
// It is assembled here rather than by appending the family fixtures to each
// other, because two of them write the same `claude-oauth-token-alice` entry
// and a document with a key twice is not a document this package's loader is
// asked to read.
func classMatrixBundleDoc(t *testing.T, workspace string) (doc []byte, key, secret string) {
	t.Helper()
	doc = mintBundleDoc(t)
	doc = append(doc, []byte(config.ReviewSigningKeySecret(testReviewAccount)+": "+reviewKey(t, "current")+"\n"+
		config.ReviewSigningKeyPreviousSecret(testReviewAccount)+": "+reviewKey(t, "previous")+"\n")...)
	key, secret = fakeCred(t, "consumer-key"), fakeCred(t, "consumer-secret")
	return append(doc, []byte(BitbucketReadConsumerSecret+":\n"+
		"  key: "+key+"\n"+
		"  secret: "+secret+"\n"+
		"  workspace: "+workspace+"\n")...), key, secret
}

// classMatrixTransport is the one client every route of the matrix reaches its
// fake through, dispatched by the HOST the route itself built: a mint builds
// api.github.com, the read mint builds bitbucket.org (G10, T2.5), and the
// model path builds api.anthropic.com. Which fake answers is therefore decided
// by the route's own destination and by nothing a test passes in, and the two
// hosts the fixtures stand for are held to their own names here rather than
// assumed.
type classMatrixTransport struct {
	github    *ghFake
	bitbucket *bitbucketFake
	upstream  http.RoundTripper
}

func (h classMatrixTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.Host {
	case "api.github.com":
		return ghTransport{srv: h.github.srv}.RoundTrip(r)
	case "bitbucket.org":
		base, err := url.Parse(h.bitbucket.srv.URL)
		if err != nil {
			return nil, fmt.Errorf("the read fake's URL is not a URL: %w", err)
		}
		return bitbucketTransport{base: base}.RoundTrip(r)
	}
	return h.upstream.RoundTrip(r)
}

// classMatrixSvc is one installation configured with ALL four classes at once
// and behind all five routes: the job fixture's own repository, a review job
// whose account the published node admits, a verifier, a person, the fake
// GitHub a mint reaches, the fake bitbucket.org the read mint reaches, and the
// bundle holding every entry the five routes read.
func classMatrixSvc(t *testing.T) (*svc, *bitbucketFake) {
	t.Helper()
	bb := newBitbucketFake(t)
	gh := newGHFake(t)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:           testProject,
			FirebaseProject:   testFirebase,
			Audience:          testAudience,
			JobAccounts:       []string{testJobSA},
			Keys:              s.keys,
			JobKeys:           s.jobKeys,
			Outcomes:          RTDBOutcomes{DB: s.db},
			SharedJobAccounts: []string{testSharedSA},
			ReviewJobAccounts: []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}},
			VerifierAccounts:  []string{testVerifierSA},
			PersonAccounts:    []string{testPersonAccount},
			Now:               func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the four classes cannot be configured together: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.ReviewAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
		cfg.Repos = map[string]string{
			s.slug:                              testRepo,
			bitbucketSlug(t, testBitbucketRepo): testCanonical(t, testBitbucketRepo),
		}
		cfg.Signs = s.probes
		cfg.Client = &http.Client{Transport: classMatrixTransport{
			github:    gh,
			bitbucket: bb,
			upstream:  cfg.Client.Transport,
		}}
		doc, key, secret := classMatrixBundleDoc(t, testWorkspace)
		bb.key, bb.secret = key, secret
		s.src.docs = [][]byte{doc}
		// The matrix makes the model path's two admitted calls — the job's and
		// the review job's — and the fixture's upstream is scripted with one
		// reply, so the second is added here. Every other cell is a refusal
		// answered before the upstream is reached, which the matrix's own
		// count asserts.
		s.up.Script = append(s.up.Script, anthropicfake.MessageOK(testModel, testUsage))
	})
	return s, bb
}

// classVerdict is one cell's designed answer: the status, and the wire's own
// error name when the cell is a refusal. An admitted cell carries no error.
type classVerdict struct {
	status int
	code   string
}

// admitted is the one cell that is not a refusal: HTTP 200 and no error at
// all.
var admitted = classVerdict{status: http.StatusOK}

// classVerdicts is the matrix itself: the designed answer for each caller on
// each route, keyed by "<class>/<route>". Every cell of the table below has an
// entry here and the test fails on a key it does not hold, so a route or a
// class added later cannot quietly go unaimed at.
var classVerdicts = map[string]classVerdict{
	"job/read-token": {StatusUnverified, ErrorUnverifiedJobIdentity},
	"job/verify":     {StatusUnverified, ErrorUnverifiedJobIdentity},
	"job/mint":       admitted,
	"job/sign":       {StatusUnverified, ErrorUnverifiedRunIdentity},
	"job/messages":   admitted,

	"review/read-token": {StatusUnverified, ErrorUnverifiedJobIdentity},
	"review/verify":     {StatusUnverified, ErrorUnverifiedJobIdentity},
	"review/mint":       admitted,
	"review/sign":       admitted,
	"review/messages":   admitted,

	"verifier/read-token": {StatusUnverified, ErrorUnverifiedJobIdentity},
	"verifier/verify":     admitted,
	"verifier/mint":       {StatusUnverified, ErrorUnverifiedRunIdentity},
	"verifier/sign":       {StatusUnverified, ErrorUnverifiedRunIdentity},
	"verifier/messages":   {StatusUnverified, ErrorUnverifiedRunIdentity},

	"person/read-token": admitted,
	"person/verify":     {StatusUnverified, ErrorUnverifiedJobIdentity},
	"person/mint":       {StatusUnverified, ErrorUnverifiedRunIdentity},
	"person/sign":       {StatusUnverified, ErrorUnverifiedRunIdentity},
	"person/messages":   {StatusUnverified, ErrorUnverifiedRunIdentity},
}

// TestRouteCallerClassMatrix is the cross-class matrix: four classes against
// five routes, each cell held to the verdict classVerdicts writes down.
func TestRouteCallerClassMatrix(t *testing.T) {
	s, _ := classMatrixSvc(t)
	digest, signature := testDigest(t, "matrix"), strings.Repeat("0", 64)

	callers := []struct {
		name string
		// token is the class's own credential: the Google ID token Cloud Run
		// IAM admitted, naming the account the class's list holds.
		token func(*svc) string
		// run is the run identity, and "" for the two classes that hold none.
		run func(*svc) string
	}{
		{
			name:  "job",
			token: func(s *svc) string { return s.jobToken() },
			run:   func(s *svc) string { return s.runToken() },
		},
		{
			name:  "review",
			token: func(s *svc) string { return s.reviewJobToken() },
			run:   func(s *svc) string { return s.reviewRunToken() },
		},
		{
			name:  "verifier",
			token: func(s *svc) string { return verifierToken(t, s, testVerifierSA) },
			run:   func(*svc) string { return "" },
		},
		{
			name:  "person",
			token: func(s *svc) string { return s.personToken(testPersonAccount) },
			run:   func(*svc) string { return "" },
		},
	}

	routes := []struct {
		name string
		path string
		body func(*svc) string
		// identity is whether the route is read by the shared parse, which is
		// what asks a call for a run identity: the three routes below
		// ServeHTTP's early branches are.
		identity bool
	}{
		{
			name: "read-token",
			path: readTokenPath,
			body: func(*svc) string { return `{"repository":"` + testBitbucketRepo + `"}` },
		},
		{
			name: "verify",
			path: VerifyPath,
			body: func(s *svc) string { return verifyBody(testReviewAccount, s.run, s.slug, digest, signature) },
		},
		{
			name:     "mint",
			path:     MintPath,
			body:     func(*svc) string { return mintBody(testAppID) },
			identity: true,
		},
		{
			name:     "sign",
			path:     SignPath,
			body:     func(*svc) string { return signBody(digest) },
			identity: true,
		},
		{
			name:     "messages",
			path:     MessagesPath,
			body:     func(*svc) string { return callBody("the-class-matrix") },
			identity: true,
		},
	}

	for _, caller := range callers {
		for _, route := range routes {
			key := caller.name + "/" + route.name
			want, ok := classVerdicts[key]
			if !ok {
				t.Errorf("the matrix holds no verdict for %s, so nothing aims at that cell", key)
				continue
			}
			t.Run(key, func(t *testing.T) {
				run := ""
				if route.identity {
					run = caller.run(s)
				}
				resp, body := s.callClass(t, route.path, route.body(s), caller.token(s), run, s.job)
				if resp.StatusCode != want.status {
					t.Fatalf("a %s caller was answered %d on %s, want %d: %s", caller.name, resp.StatusCode, route.name, want.status, body)
				}
				if want.code == "" {
					return
				}
				if got := refusal(t, resp.StatusCode, body); got.Error != want.code {
					t.Errorf("the %s caller's refusal on %s is %q, want %q: %s", caller.name, route.name, got.Error, want.code, got.Message)
				}
			})
		}
	}
	// The upstream's own count is the other half of the matrix: the model path
	// has exactly two admitted callers, so a cell that admitted a third is a
	// caller that reached the one route whose reach costs money — which no
	// status this test read would have shown if the extra call had been
	// answered beside a refusal.
	if n := s.up.Count(); n != 2 {
		t.Errorf("the upstream saw %d model calls, want the two admitted callers' and no more", n)
	}
}

// callClass sends one call as one class: the caller's own token in
// Authorization, and the run identity's headers only when the class holds one
// and the route is read by the shared parse. The headers are set only then, so
// a verifier's or a person's call is the shape such a caller really sends
// rather than a run identity it cannot have.
func (s *svc) callClass(t *testing.T, path, body, tok, run, job string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	if run != "" {
		req.Header.Set(HeaderRunIdentity, run)
		req.Header.Set(HeaderRunJob, job)
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

// TestNewAuthenticatorRefusesAPersonThatIsAnotherClass: the person list is the
// one list that may not overlap another class's (G4). Each of the four shapes
// below is a deployment that disagrees with itself — one caller, two classes —
// and the classes would then be spelled by the order the routes read their
// lists in. A service that starts on such a configuration is one whose routes
// admit a caller the deployment did not name as one of that route's class.
//
// The refusals name the field and the class it collided with and never the
// address: an entry of every one of these lists is an account, and the
// package's no-echo rule holds for a configuration error as it does for a
// caller's claim.
func TestNewAuthenticatorRefusesAPersonThatIsAnotherClass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		person  string
		authFor func(*AuthConfig)
		want    string
	}{
		{
			name:   "a per-repository job's account",
			person: testJobSA,
			want:   "job service account",
		},
		{
			name:   "a shared project job's account",
			person: testSharedSA,
			want:   "shared job service account",
		},
		{
			name:   "a review job's account",
			person: testReviewSA,
			authFor: func(cfg *AuthConfig) {
				cfg.ReviewJobAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
			},
			want: "review job service account",
		},
		{
			name:   "a verifier's address",
			person: testVerifierSA,
			authFor: func(cfg *AuthConfig) {
				cfg.VerifierAccounts = []string{testVerifierSA}
			},
			want: "verifier",
		},
		{
			name:   "a service account address of another shape",
			person: "some-machine@example-project.iam.gserviceaccount.com",
			want:   "service account address",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := NewAuthenticator(personAuthConfig(f, tc.person, tc.authFor))
			if err == nil {
				t.Fatalf("NewAuthenticator admitted %s as a person", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the configuration error is %q, want it to be about the %s the address also is", err, tc.want)
			}
			if strings.Contains(err.Error(), tc.person) {
				t.Errorf("the configuration error is %q, and it carries the address it refused", err)
			}
		})
	}
}

// TestPersonListStaysDisjointInTheOtherDirection: the four checks above are
// about the people list, and the three lists a person may not be in must
// themselves still admit those addresses where their own rules do. An
// installation whose verifier is also a job's account is exactly the
// configuration C3.9f's class is built on (TestVerifierIsNotGatedByTheSharedJobRule),
// so the new rule may not have become a rule about those lists.
func TestPersonListStaysDisjointInTheOtherDirection(t *testing.T) {
	f := newFixture(t)
	cfg := personAuthConfig(f, testPersonAccount, func(cfg *AuthConfig) {
		cfg.VerifierAccounts = []string{testVerifierSA, testJobSA}
		cfg.ReviewJobAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
	})
	if _, err := NewAuthenticator(cfg); err != nil {
		t.Fatalf("NewAuthenticator refused a verifier that is also a job's account, which is C3.9f's own configuration: %v", err)
	}
}

// personAuthConfig is the fixture's own configuration with the person list
// under the test's control and, when it gives one, the extra lists a case
// needs — so a case holds one thing and not a whole configuration.
func personAuthConfig(f *fixture, person string, extra func(*AuthConfig)) AuthConfig {
	cfg := AuthConfig{
		Project:           testProject,
		FirebaseProject:   testFirebase,
		Audience:          testAudience,
		JobAccounts:       []string{testJobSA},
		Keys:              f.keys,
		JobKeys:           f.jobKeys,
		Outcomes:          RTDBOutcomes{DB: f.db},
		SharedJobAccounts: []string{testSharedSA},
		PersonAccounts:    []string{person},
		Now:               func() time.Time { return f.now },
	}
	if extra != nil {
		extra(&cfg)
	}
	return cfg
}
