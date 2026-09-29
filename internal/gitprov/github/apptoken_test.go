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
