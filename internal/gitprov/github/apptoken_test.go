package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// key returns an RSA key shared by the package's tests (generating one is slow).
func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		if testKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return testKey
}

func pkcs1PEM(t *testing.T) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key(t))})
}

// verifyJWT checks jwt's RS256 signature against pub and returns its claims.
func verifyJWT(t *testing.T, jwt string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q does not have 3 parts", jwt)
	}
	enc := base64.RawURLEncoding
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("jwt signature: %v", err)
	}
	var header, claims map[string]any
	h, _ := enc.DecodeString(parts[0])
	c, _ := enc.DecodeString(parts[1])
	if json.Unmarshal(h, &header) != nil || json.Unmarshal(c, &claims) != nil || header["alg"] != "RS256" {
		t.Fatalf("jwt header %s, claims %s", h, c)
	}
	return claims
}

func TestParsePrivateKey(t *testing.T) {
	if k, err := ParsePrivateKey(pkcs1PEM(t)); err != nil || !k.Equal(key(t)) {
		t.Fatalf("PKCS#1: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key(t))
	if err != nil {
		t.Fatal(err)
	}
	if k, err := ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil || !k.Equal(key(t)) {
		t.Fatalf("PKCS#8: %v", err)
	}
	if _, err := ParsePrivateKey([]byte("not a key")); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestAppJWT(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	jwt, err := appJWT("1234", key(t), now)
	if err != nil {
		t.Fatal(err)
	}
	claims := verifyJWT(t, jwt, &key(t).PublicKey)
	if claims["iss"] != "1234" || claims["iat"] != float64(now.Unix()-60) || claims["exp"] != float64(now.Unix()+540) {
		t.Fatalf("claims = %v", claims)
	}
}

// jwtCheck is a RoundTripper that fails the test unless every request is
// authenticated with an App JWT signed by pub.
type jwtCheck struct {
	t   *testing.T
	pub *rsa.PublicKey
}

func (c jwtCheck) RoundTrip(r *http.Request) (*http.Response, error) {
	jwt, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		c.t.Errorf("%s %s: no bearer JWT", r.Method, r.URL.Path)
	} else {
		verifyJWT(c.t, jwt, c.pub)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func newSource(t *testing.T, fixture string, now *time.Time) *tokenSource {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	clock := func() time.Time { return *now }
	app := &httpjson.Client{BaseURL: srv.URL, HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}},
		Auth: appAuth("1234", key(t), clock)}
	return &tokenSource{app: app, owner: "acme", repo: "web", now: clock}
}

func TestTokenIsCachedThenRefreshedNearExpiry(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := newSource(t, "token_refresh.json", &now)
	ctx := context.Background()
	tok, exp, err := s.Token(ctx, 45*time.Minute)
	if err != nil || tok != "ghs_fixture_token_1" || !exp.Equal(time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("first token = %q %s %v", tok, exp, err)
	}
	now = now.Add(10 * time.Minute) // 50m left: still good for 45m
	if tok, _, err := s.Token(ctx, 45*time.Minute); err != nil || tok != "ghs_fixture_token_1" {
		t.Fatalf("cached token = %q %v", tok, err)
	}
	now = now.Add(10 * time.Minute) // 40m left: too short, and no second installation lookup
	if tok, _, err := s.Token(ctx, 45*time.Minute); err != nil || tok != "ghs_fixture_token_2" {
		t.Fatalf("refreshed token = %q %v", tok, err)
	}
}

func TestTokenAppNotInstalled(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := newSource(t, "not_installed.json", &now)
	if _, _, err := s.Token(context.Background(), time.Minute); err == nil || !strings.Contains(err.Error(), "installation for acme/web") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

// TestTokenExpiresTooSoonIsAnError covers the round-1 correction: the
// GitAuth contract (gitprov.go) promises a token valid for at least
// minValid, so a token GitHub mints with less life left than that must be
// reported as an error, not returned as if it satisfied the caller.
func TestTokenExpiresTooSoonIsAnError(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := newSource(t, "token_expires_too_soon.json", &now)
	// The fixture's token expires at 10:05, five minutes after now; asking
	// for 45 minutes of validity cannot be satisfied.
	if _, _, err := s.Token(context.Background(), 45*time.Minute); err == nil || !strings.Contains(err.Error(), "acme/web") {
		t.Fatalf("err = %v, want an error naming acme/web", err)
	}
}

// TestTokenConcurrentCallsMintOnce covers the round-1 correction: many
// goroutines calling Token concurrently against a fixture with exactly one
// installation lookup and one mint must produce exactly one of each — the
// mutex around Token must serialize the whole lookup-then-mint sequence, not
// just the cache check, so races cannot cause a second lookup or mint.
// httpfixture.Server itself proves this: it fails the test if a request
// does not match the next exchange in order, or if any exchange goes
// unused, and this fixture holds only one GET and one POST.
func TestTokenConcurrentCallsMintOnce(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s := newSource(t, "concurrent_token.json", &now)
	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	toks := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, _, err := s.Token(context.Background(), time.Minute)
			toks[i], errs[i] = tok, err
		}(i)
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || toks[i] != "ghs_fixture_token_1" {
			t.Fatalf("goroutine %d: tok = %q, err = %v", i, toks[i], errs[i])
		}
	}
}

// TestMintInstallationTokenBuildPermissions: an image build's token can
// only read the one repository it builds; the fixture fails the test on
// any other request body.
func TestMintInstallationTokenBuildPermissions(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "build_token.json"))
	tok, exp, err := MintInstallationToken(context.Background(), Options{
		Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
	}, BuildTokenPermissions())
	if err != nil || tok != "ghs_fixture_build_token" || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("MintInstallationToken = %q %s %v", tok, exp, err)
	}
	perms := BuildTokenPermissions()
	if len(perms) != 2 || perms["contents"] != "read" || perms["metadata"] != "read" {
		t.Fatalf("build permissions = %v", perms)
	}
	if _, _, err := MintInstallationToken(context.Background(), Options{Owner: "acme", Repo: "web", AppID: "1234"}, perms); err == nil {
		t.Error("minted without a private key")
	}
	if _, _, err := MintInstallationToken(context.Background(), Options{Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t)}, nil); err == nil {
		t.Error("minted without permissions")
	}
}

// TestMintRepositoriesToken: one token over two repositories (D2301). The
// fixture pins the whole of the mint — the installation found through one of
// the repositories, and ONE access_tokens request whose repositories are
// exactly the two asked for — so a body that covered one repository, or the
// owner's every repository, fails here rather than at a build that cloned
// something it was not given.
func TestMintRepositoriesToken(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "build_repositories_token.json"))
	tok, exp, err := MintRepositoriesToken(context.Background(), Options{
		Owner: "acme", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
	}, []string{"web", "api"}, BuildTokenPermissions())
	if err != nil || tok != "ghs_fixture_build_token" || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("MintRepositoriesToken = %q %s %v", tok, exp, err)
	}
	// The three shapes this function refuses rather than sending to GitHub: a
	// name that is an owner/name pair (the endpoint takes names within one
	// owner), the same repository twice, and no repository at all. Each is a
	// call whose answer would not be the reach the caller asked for.
	for _, tc := range []struct {
		name  string
		repos []string
	}{
		{name: "owner/name is not a name", repos: []string{"acme/web"}},
		{name: "the same repository twice", repos: []string{"web", "web"}},
		{name: "no repository", repos: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := MintRepositoriesToken(context.Background(), Options{
				Owner: "acme", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
				HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
			}, tc.repos, BuildTokenPermissions()); err == nil {
				t.Error("a request that names no mintable repository was minted")
			}
		})
	}
	if _, _, err := MintRepositoriesToken(context.Background(), Options{
		Owner: "acme", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL, Now: func() time.Time { return now },
	}, []string{"web"}, nil); err == nil {
		t.Error("minted without permissions")
	}
	if _, _, err := MintRepositoriesToken(context.Background(), Options{
		AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL, Now: func() time.Time { return now },
	}, []string{"web"}, BuildTokenPermissions()); err == nil {
		t.Error("minted with no owner")
	}
}

// TestSettleTokenIsPullRequestsWriteOnly is D641's rule: the settle step that
// flips a change set's pull requests ready (D614) is the one coordinator-side
// actor that writes, and the token it writes with carries the one write the
// flip needs and no reach beyond it.
//
// It is asserted as an equality rather than as "one write" alone, the rule the
// coordinator's set keeps: a permission asked for and never used is reach a
// stolen token would have for nothing. And the mint is driven against a fixture
// whose recorded body IS this set, so the set is not merely declared here but
// is what reaches GitHub: a change to the map that left the fixture alone fails
// on the fixture, and a change that widened the map fails on the map.
//
// What it is NOT is any other class's set. The coordinator's read, the run's,
// the review's and the context clone's are four declarations of their own, and
// the settle's being the coordinator's widened would make a change to either
// silently widen the other — which is the drift the coordinator's own test
// forbids from its side.
func TestSettleTokenIsPullRequestsWriteOnly(t *testing.T) {
	want := map[string]string{"pull_requests": "write", "metadata": "read"}
	if !maps.Equal(settleTokenPermissions, want) {
		t.Fatalf("settleTokenPermissions = %v, want %v", settleTokenPermissions, want)
	}
	writes := 0
	for name, level := range settleTokenPermissions {
		if strings.Contains(level, "write") {
			writes++
			if name != "pull_requests" {
				t.Errorf("the settle mint asks GitHub to write %s: %s, and a settle step writes no file", name, level)
			}
		}
	}
	if writes != 1 {
		t.Errorf("the settle mint asks for %d writes, want exactly the pull requests one", writes)
	}
	for _, other := range []struct {
		name string
		set  map[string]string
	}{
		{name: "the coordinator's", set: CoordinatorTokenPermissions()},
		{name: "a run's", set: RunTokenPermissions()},
		{name: "a review's", set: ReviewTokenPermissions()},
		{name: "a context clone's", set: ContextTokenPermissions()},
	} {
		if maps.Equal(other.set, want) {
			t.Errorf("the settle mint's set is %s set: the two are two declarations, so a change to either cannot widen the other", other.name)
		}
	}
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "settle_token.json"))
	tok, exp, err := MintSettleToken(context.Background(), Options{
		Owner: "acme", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
	}, []string{"web", "api"})
	if err != nil || tok != "ghs_fixture_settle_token" || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("MintSettleToken = %q %s %v", tok, exp, err)
	}
}

// TestSettleTokenNamesOnlyMemberRepos: a settle token covers the members of
// the set it settles and nothing else. The fixture's access_tokens body names
// exactly the two repositories the caller named, so reaching the token is
// GitHub having been asked for a token over those two — a body that covered
// one member, or the installation's every repository, fails here rather than
// at a settle that could flip a pull request outside the set.
//
// The names are within one owner, because that is the endpoint's shape: a set's
// members may span owners (D642), so the caller mints once per owner and a
// member of another owner is not a name this mint can be given. The three
// shapes it refuses rather than sending to GitHub are the ones
// MintRepositoriesToken refuses, and they matter here for the same reason: a
// token covering less than the caller asked for would leave a member unflipped
// with the failure surfacing far from its cause.
func TestSettleTokenNamesOnlyMemberRepos(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "settle_token.json"))
	opts := Options{
		Owner: "acme", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
	}
	if _, _, err := MintSettleToken(context.Background(), opts, []string{"web", "api"}); err != nil {
		t.Fatalf("MintSettleToken: %v", err)
	}
	for _, tc := range []struct {
		name  string
		repos []string
	}{
		{name: "another owner's member is not a name", repos: []string{"acme/web", "acme/api"}},
		{name: "one member twice is not two members", repos: []string{"web", "web"}},
		{name: "a set with no member to name", repos: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := MintSettleToken(context.Background(), opts, tc.repos); err == nil {
				t.Error("a settle token was minted for repositories that are not the set's members")
			}
		})
	}
}

// TestRevokeInstallationToken: the token's own end. GitHub revokes an
// installation token at DELETE /installation/token, authenticated with the
// token itself -- the App's JWT is not needed, and the endpoint revokes the
// token that carries it and no other -- and the fixture pins the whole of the
// request: the method, the path and the Authorization header.
//
// The second half is the contract the caller depends on: a refused revoke is
// returned as a failure, and the failure carries the status and never the
// token. The command that calls this revokes best-effort and prints nothing, so
// what it does with the failure is its own decision -- but the value reaching
// an error or a log line is not a decision it gets to make.
func TestRevokeInstallationToken(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "revoke_token.json"))
	if err := RevokeInstallationToken(context.Background(), srv.URL, "ghs_fixture_revoked", nil); err != nil {
		t.Fatalf("RevokeInstallationToken: %v", err)
	}
	// No token is nothing to revoke: a refusal rather than a DELETE under an
	// empty Authorization header, which would revoke whatever the API read it
	// as. No request is made, which the fixture's strict order is what proves.
	if err := RevokeInstallationToken(context.Background(), srv.URL, "", nil); err == nil {
		t.Error("RevokeInstallationToken with no token: want a refusal")
	}
	err := RevokeInstallationToken(context.Background(), srv.URL, "ghs_fixture_refused", nil)
	if err == nil {
		t.Fatal("a refused revoke: want the failure reported")
	}
	if strings.Contains(err.Error(), "ghs_fixture_refused") {
		t.Errorf("the refused revoke's failure carries the token: %v", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("the refused revoke's failure is %v, want the status reported", err)
	}
}

// TestRunTokenPermissionsAreTheRuns: RunTokenPermissions is the run's own set
// — the four permissions design §6.1 gives a run, and no more — and it is a
// copy, so a caller that mutates what it was handed (the credentials service,
// C2.1, builds its mint's request from it) cannot change what the next caller
// asks GitHub for.
//
// The set itself is pinned where it is used: token_refresh.json's two mint
// bodies are exactly these four permissions, and the fixture server refuses a
// request body that is not the one it holds.
func TestRunTokenPermissionsAreTheRuns(t *testing.T) {
	want := map[string]string{"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"}
	perms := RunTokenPermissions()
	if !maps.Equal(perms, want) {
		t.Fatalf("RunTokenPermissions() = %v, want %v", perms, want)
	}
	perms["administration"] = "write"
	if again := RunTokenPermissions(); !maps.Equal(again, want) {
		t.Errorf("RunTokenPermissions() after a caller mutated its copy = %v, want %v", again, want)
	}
}

// TestAppTokenCoordinatorPermissionsHoldNoWrite is Q3's hard rule made
// mechanical (P2 of docs/coordinator/implementation-plan.md, D193(A)): the
// installation token a coordinator mints carries the reads the project view
// makes and no write anywhere.
//
// The set is exactly the two reads the reader makes -- a pull request is read
// one at a time (gitprov.Provider.PullRequest), and metadata: read is what
// GitHub requires of any request -- and it is asserted as an equality rather
// than as "no write" alone: a permission asked for and never used is reach a
// stolen token would have for nothing, which is the half of Q3's rule that a
// write-only check cannot see.
//
// The set is read twice, because the two are not the same claim: the accessor
// is what the caller asks with, and testdata/coordinator_token.json holds the
// body the mint actually sends — a fixture server refuses a request body that
// is not the one it holds, so a permission added to the set after that file
// was written fails here rather than reaching GitHub.
func TestAppTokenCoordinatorPermissionsHoldNoWrite(t *testing.T) {
	want := map[string]string{"pull_requests": "read", "metadata": "read"}
	perms := CoordinatorTokenPermissions()
	if !maps.Equal(perms, want) {
		t.Fatalf("CoordinatorTokenPermissions() = %v, want %v", perms, want)
	}
	for name, level := range perms {
		if strings.Contains(level, "write") {
			t.Errorf("the coordinator's token asks GitHub for %s: %s", name, level)
		}
	}
	perms["pull_requests"] = "write"
	if again := CoordinatorTokenPermissions(); !maps.Equal(again, want) {
		t.Errorf("CoordinatorTokenPermissions() after a caller mutated its copy = %v, want %v", again, want)
	}

	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_token.json"))
	tok, exp, err := MintInstallationToken(context.Background(), Options{
		Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
	}, CoordinatorTokenPermissions())
	if err != nil || tok != "ghs_fixture_coordinator_token" || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("MintInstallationToken = %q %s %v", tok, exp, err)
	}
}

// TestReviewTokenPermissionsAreReadOnly is CSR1's rule (D883, F2): the review
// job's installation token carries the reads the reviewer makes -- the head it
// reads and the pull request it reviews -- and no value that could write
// anywhere. A write here is a write credential in the container that runs the
// head's own tests, which is the configuration D883 exists to leave.
//
// It is asserted as an equality rather than as "no write" alone, the rule
// TestAppTokenCoordinatorPermissionsHoldNoWrite keeps for the coordinator's
// set: a permission asked for and never used is reach a stolen token would have
// for nothing. The clone is asserted because the caller that builds a mint's
// request out of what it was handed (the credentials service, C3.9) must not be
// able to change what the next caller asks GitHub for.
func TestReviewTokenPermissionsAreReadOnly(t *testing.T) {
	want := map[string]string{"contents": "read", "pull_requests": "read", "metadata": "read"}
	perms := ReviewTokenPermissions()
	if !maps.Equal(perms, want) {
		t.Fatalf("ReviewTokenPermissions() = %v, want %v", perms, want)
	}
	for name, level := range perms {
		if level != "read" {
			t.Errorf("the review job's token asks GitHub for %s: %s", name, level)
		}
	}
	perms["contents"] = "write"
	perms["issues"] = "read"
	if again := ReviewTokenPermissions(); !maps.Equal(again, want) {
		t.Errorf("ReviewTokenPermissions() after a caller mutated its copy = %v, want %v", again, want)
	}
}

// TestRunTokenPermissionsUnchanged is the SEC half of CSR1: the task adds a
// fifth set beside the run's, and the run's is not the one it narrows. A run
// pushes its branch and opens its pull request, so its two writes are
// load-bearing -- a read-only set that reached the run would fail every push
// deep in a stage -- and the arrival of the review job's set is exactly when
// that could happen by accident.
//
// The second half is that the two are two declarations and not one map: a
// fifth set that was the run's with values changed would leave the review job
// holding the write credential D883 refuses it.
func TestRunTokenPermissionsUnchanged(t *testing.T) {
	run := RunTokenPermissions()
	if run["contents"] != "write" || run["pull_requests"] != "write" {
		t.Errorf("RunTokenPermissions() = %v: this task adds a read-only set and must not narrow the run's, whose two writes are the push and the pull request it opens", run)
	}
	if maps.Equal(run, ReviewTokenPermissions()) {
		t.Error("the review job's set is the run's set: a fifth declaration that is not one leaves the review job holding the write credential D883 refuses it")
	}
}

// TestPersonReadPermissionsCoverAFileRead is D915's rule made mechanical (A0 of
// docs/run-without-checkout/implementation-plan.md): the set the credentials
// service's person read mint asks with reaches the one file that route was
// extended to serve -- a repository's own fugaro.yaml at a ref, read through
// gitprov.ContentsReader's GET /repos/{owner}/{repo}/contents/{path}?ref= --
// and asks for no write anywhere.
//
// It carries the coordinator's two as well, because the route is one route: the
// same mint is what a person's `coordinator show` reads a pull request with, so
// the set has to reach that read too. It is asserted as an equality rather than
// as "no write" alone, the rule TestAppTokenCoordinatorPermissionsHoldNoWrite
// keeps for the coordinator's set: a permission asked for and never used is
// reach a stolen token would have for nothing, which a write-only check cannot
// see.
//
// The coordinator's set is the one this set must NOT be: widening it would
// widen the coordinator's own credential, which reads no file. That is what the
// two pins beside this test hold -- TestAppTokenCoordinatorPermissionsHoldNoWrite
// and its fixture body on one side, TestGitHubReadAsksForNoWrite in
// internal/credsvc on the other -- and the last check here says it in this
// package's own vocabulary.
func TestPersonReadPermissionsCoverAFileRead(t *testing.T) {
	want := map[string]string{"contents": "read", "pull_requests": "read", "metadata": "read"}
	perms := PersonReadTokenPermissions()
	if !maps.Equal(perms, want) {
		t.Fatalf("PersonReadTokenPermissions() = %v, want %v", perms, want)
	}
	for name, level := range perms {
		if level != "read" {
			t.Errorf("the person read mint asks GitHub for %s: %s", name, level)
		}
	}
	perms["contents"] = "write"
	perms["issues"] = "read"
	if again := PersonReadTokenPermissions(); !maps.Equal(again, want) {
		t.Errorf("PersonReadTokenPermissions() after a caller mutated its copy = %v, want %v", again, want)
	}
	if maps.Equal(CoordinatorTokenPermissions(), want) {
		t.Error("the coordinator's set is the person read set: the two are two declarations, so a change to either cannot widen the other")
	}
}

// TestContextTokenIsReadOnly is C5b's red step (D613): a run's context
// repositories are READ, and the token that reads them carries the two
// permissions a read needs and no third.
//
// Two halves, and the second is what makes the first a boundary rather than a
// coincidence. The set is pinned as a map — contents: read and metadata: read,
// nothing else, and a copy, so a caller mutating what it was handed cannot
// widen what the next caller asks GitHub for. And the mint itself is driven
// against a fixture whose recorded bodies are exactly that set (the fixture
// server refuses a request body that is not the one it holds), so the read-only
// set is not merely declared here but is what reaches the API: a change to
// ContextTokenPermissions that left this test's map alone would still fail on
// the fixture, and a change that widened the map fails on the map.
//
// What it is NOT is any other class's set. A context token is the run's own
// reach beyond its repository and not the build's, the shadow's, the
// coordinator's or a person's read — each is a declaration of its own, and
// reusing one would make a change to that one widen this.
func TestContextTokenIsReadOnly(t *testing.T) {
	want := map[string]string{"contents": "read", "metadata": "read"}
	perms := ContextTokenPermissions()
	if !maps.Equal(perms, want) {
		t.Fatalf("ContextTokenPermissions() = %v, want %v", perms, want)
	}
	for name, level := range perms {
		if level != "read" {
			t.Errorf("the context mint asks GitHub for %s: %s, and a run reads its context repositories and never writes them", name, level)
		}
	}
	perms["contents"] = "write"
	perms["issues"] = "read"
	if again := ContextTokenPermissions(); !maps.Equal(again, want) {
		t.Errorf("ContextTokenPermissions() after a caller mutated its copy = %v, want %v", again, want)
	}
	for _, other := range []struct {
		name string
		set  map[string]string
	}{
		{name: "a run's", set: RunTokenPermissions()},
		{name: "the coordinator's", set: CoordinatorTokenPermissions()},
		{name: "a review's", set: ReviewTokenPermissions()},
		{name: "a person's read", set: PersonReadTokenPermissions()},
	} {
		if maps.Equal(other.set, want) {
			t.Errorf("the context mint's set is %s set: the two are two declarations, so a change to either cannot widen the other", other.name)
		}
	}
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "context_token.json"))
	tokens, err := MintContextTokens(context.Background(), Options{
		AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
	}, []string{"acme/web", "acme/api"}, ContextTokenPermissions())
	if err != nil {
		t.Fatalf("MintContextTokens: %v", err)
	}
	// The fixture's own request body is the read-only set, so reaching here is
	// GitHub having been asked for exactly it — and the answer is that token,
	// named for the repositories it covers, in the caller's order.
	if len(tokens) != 1 || tokens[0].Token != "ghs_fixture_context_token_1" ||
		!tokens[0].ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("MintContextTokens = %+v, want the fixture's one token expiring %s", tokens, now.Add(time.Hour))
	}
	if want := []string{"acme/web", "acme/api"}; !slices.Equal(tokens[0].Repositories, want) {
		t.Errorf("the token covers %v, want %v", tokens[0].Repositories, want)
	}
	// A set the caller must supply, and an App that must be named: a context
	// mint that asked for no permission would be a token with GitHub's own
	// default reach, which is wider than any set here.
	if _, err := MintContextTokens(context.Background(), Options{
		AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL, Now: func() time.Time { return now },
	}, []string{"acme/web"}, nil); err == nil {
		t.Error("minted without permissions")
	}
	if _, err := MintContextTokens(context.Background(), Options{
		PrivateKey: key(t), BaseURL: srv.URL, Now: func() time.Time { return now },
	}, []string{"acme/web"}, ContextTokenPermissions()); err == nil {
		t.Error("minted with no App ID")
	}
}

// TestContextTokenOnePerInstallation: one token per App INSTALLATION, because
// that is the granularity GitHub's API has — a token is minted at
// /app/installations/{id}/access_tokens and covers repositories within one
// owner — and a run's context list may name repositories under two owners
// (C5a admits five, and nothing in a task says they share an owner).
//
// So a list of three repositories under two owners is two tokens, each scoped
// to its own owner's repositories, and each mint is its own installation lookup
// and its own access_tokens POST — which is what the fixture's strict order
// proves: four exchanges in the order this mint makes them, and a fifth request
// of any kind fails the test as an unused exchange or an unexpected one.
//
// The order of the answer is the caller's list's, and the repositories of each
// token are the caller's spelling of them: a caller matching the answer to its
// own list must not have to know how the grouping worked.
func TestContextTokenOnePerInstallation(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "context_token_two_installations.json"))
	tokens, err := MintContextTokens(context.Background(), Options{
		AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
	}, []string{"acme/web", "north/iceberg", "acme/api"}, ContextTokenPermissions())
	if err != nil {
		t.Fatalf("MintContextTokens: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("MintContextTokens returned %d tokens, want one per installation (2): %+v", len(tokens), tokens)
	}
	if tokens[0].Token != "ghs_fixture_context_token_1" || tokens[1].Token != "ghs_fixture_context_token_2" {
		t.Errorf("the tokens are %q and %q, want the fake's two", tokens[0].Token, tokens[1].Token)
	}
	// The first group is the owner the list named first, and its repositories
	// are that owner's two in the order they appeared — the third name of the
	// list belongs to the second group and not to the first.
	if want := []string{"acme/web", "acme/api"}; !slices.Equal(tokens[0].Repositories, want) {
		t.Errorf("the first token covers %v, want %v", tokens[0].Repositories, want)
	}
	if want := []string{"north/iceberg"}; !slices.Equal(tokens[1].Repositories, want) {
		t.Errorf("the second token covers %v, want %v", tokens[1].Repositories, want)
	}
	// What a context list may not be: an owner with no repository, a name that
	// is not owner/name, and the same repository twice. Each is a list whose
	// grouping could not be undone by the caller reading the answer, and each
	// is refused before GitHub is reached — the fixture would see a request it
	// does not hold if any of them got through.
	for _, tc := range []struct {
		name  string
		repos []string
	}{
		{name: "no repository", repos: nil},
		{name: "an owner with no repository", repos: []string{"acme/"}},
		{name: "a bare name", repos: []string{"web"}},
		{name: "a nested group", repos: []string{"acme/web/sub"}},
		{name: "the same repository twice", repos: []string{"acme/web", "acme/web"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := MintContextTokens(context.Background(), Options{
				AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
				HTTP: &http.Client{Transport: jwtCheck{t, &key(t).PublicKey}}, Now: func() time.Time { return now },
			}, tc.repos, ContextTokenPermissions()); err == nil {
				t.Error("a list that names no mintable context repository was minted")
			}
		})
	}
}
