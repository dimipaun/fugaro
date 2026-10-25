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

// TestRunTokenPermissionsAreTheRuns: RunTokenPermissions is the run's own set
// — the four permissions design §6.1 gives a run, and no more — and it is a
// copy, so a caller that mutates what it was handed (the credentials service,
// C2.1, builds its mint's request from it) cannot change what the next caller
// asks GitHub for.
//
// The set itself is pinned where it is used: token_refresh.json's two mint
// bodies are exactly these four permissions, and the fixture server refuses a
// request body that is not the one it holds.
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
