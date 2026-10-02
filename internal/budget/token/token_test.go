package token_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const (
	signerEmail = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	apiKey      = "AIzaSyFakeWebApiKeyForTests000000000"
	launcherTok = "ya29.launcher-access-token-for-tests"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

type env struct {
	iam    *gcpfake.IAMCredentials
	itk    *gcpfake.IdentityToolkit
	signer *token.IAMSigner
	now    func() time.Time
	setNow func(time.Time)

	mu      sync.Mutex
	secrets []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{}
	cur := t0
	var cmu sync.Mutex
	e.now = func() time.Time { cmu.Lock(); defer cmu.Unlock(); return cur }
	e.setNow = func(v time.Time) { cmu.Lock(); defer cmu.Unlock(); cur = v }
	e.iam = gcpfake.NewIAMCredentials(t)
	e.iam.AddSigner(signerEmail)
	e.itk = gcpfake.NewIdentityToolkit(t, e.iam, apiKey, "aurora-fp")
	e.itk.SetClock(e.now)
	s, err := token.NewIAMSigner(signerEmail, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: launcherTok}), token.WithIAMEndpoint(e.iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	e.signer = s
	return e
}

func (e *env) register(s string) { e.mu.Lock(); e.secrets = append(e.secrets, s); e.mu.Unlock() }

func (e *env) registered() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.secrets...)
}

func (e *env) cfg() token.Config {
	return token.Config{APIKey: apiKey, IdentityURL: e.itk.URL, SecureTokenURL: e.itk.URL, Now: e.now, Register: e.register}
}

func claims() token.Claims {
	return token.Claims{Slug: "aurora-app", Run: "20261002-120000-ab12", FX: token.ExpiryFX(t0, 2*time.Hour+2*time.Minute),
		FP: "aurora", RB: "dev@example.invalid"}
}

func (e *env) mint(t *testing.T, c token.Claims, now time.Time) string {
	t.Helper()
	tok, err := token.MintAt(context.Background(), e.signer, c, now)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func payload(t *testing.T, jwt string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(jwt, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMintClaims(t *testing.T) {
	e := newEnv(t)
	c := claims()
	tok := e.mint(t, c, t0)
	calls := e.iam.SignCalls()
	if len(calls) != 1 {
		t.Fatalf("signJwt calls = %d", len(calls))
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(calls[0].Payload), &p); err != nil {
		t.Fatal(err)
	}
	if p["iss"] != signerEmail || p["sub"] != signerEmail {
		t.Fatalf("iss/sub = %v/%v", p["iss"], p["sub"])
	}
	if p["aud"] != "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit" {
		t.Fatalf("aud = %v", p["aud"])
	}
	if p["uid"] != "r~aurora-app~20261002-120000-ab12" {
		t.Fatalf("uid = %v", p["uid"])
	}
	if int64(p["iat"].(float64)) != t0.Unix() || int64(p["exp"].(float64)) != t0.Add(time.Hour).Unix() {
		t.Fatalf("iat/exp = %v/%v, want %d/+1h", p["iat"], p["exp"], t0.Unix())
	}
	cl := p["claims"].(map[string]any)
	want := map[string]any{"fs": "aurora-app", "fr": "20261002-120000-ab12", "fp": "aurora", "rb": "dev@example.invalid",
		"fx": float64(t0.Add(2*time.Hour + 2*time.Minute + time.Hour + 5*time.Minute).UnixMilli())}
	if len(cl) != len(want) {
		t.Fatalf("claims = %v, want exactly %v", cl, want)
	}
	for k, v := range want {
		if cl[k] != v {
			t.Errorf("claim %s = %v, want %v", k, cl[k], v)
		}
	}
	if got := payload(t, tok)["uid"]; got != p["uid"] {
		t.Fatalf("token uid = %v", got)
	}
}

func TestExpiryFXFormula(t *testing.T) {
	got := token.ExpiryFX(t0, 90*time.Minute)
	if want := t0.Add(90*time.Minute + time.Hour + 5*time.Minute); !got.Equal(want) {
		t.Fatalf("fx = %v, want %v", got, want)
	}
}

func TestMintRefusesBadClaims(t *testing.T) {
	e := newEnv(t)
	good := claims()
	mut := func(f func(*token.Claims)) token.Claims { c := good; f(&c); return c }
	for name, c := range map[string]token.Claims{
		"empty slug":         mut(func(c *token.Claims) { c.Slug = "" }),
		"tilde in slug":      mut(func(c *token.Claims) { c.Slug = "a~b" }),
		"tilde in run":       mut(func(c *token.Claims) { c.Run = "b~c" }),
		"slash in run":       mut(func(c *token.Claims) { c.Run = "a/b" }),
		"space in slug":      mut(func(c *token.Claims) { c.Slug = "a b" }),
		"dotdot":             mut(func(c *token.Claims) { c.Run = ".." }),
		"newline in rb":      mut(func(c *token.Claims) { c.RB = "a\nb" }),
		"empty rb":           mut(func(c *token.Claims) { c.RB = "" }),
		"empty fp":           mut(func(c *token.Claims) { c.FP = "" }),
		"fx in the past":     mut(func(c *token.Claims) { c.FX = t0.Add(-time.Second) }),
		"fx unbounded":       mut(func(c *token.Claims) { c.FX = t0.Add(365 * 24 * time.Hour) }),
		"uid over 128 bytes": mut(func(c *token.Claims) { c.Slug = strings.Repeat("s", 100); c.Run = strings.Repeat("r", 40) }),
	} {
		if _, err := token.MintAt(context.Background(), e.signer, c, t0); err == nil {
			t.Errorf("%s: minted", name)
		}
	}
	if n := len(e.iam.SignCalls()); n != 0 {
		t.Fatalf("a refused mint still called signJwt %d times", n)
	}
}

func TestMintSignsAsSigner(t *testing.T) {
	e := newEnv(t)
	e.mint(t, claims(), t0)
	calls := e.iam.SignCalls()
	if len(calls) != 1 || calls[0].Signer != signerEmail {
		t.Fatalf("calls = %+v", calls)
	}
	// The launcher's own credential calls signJwt; nothing else was asked of
	// IAM (the fake fails the test on any other call, generateAccessToken
	// included), so no access token was requested for the signer.
	if calls[0].Authorization != "Bearer "+launcherTok {
		t.Fatalf("Authorization = %q", calls[0].Authorization)
	}
	for _, r := range e.iam.Requests() {
		if !strings.HasSuffix(r.Path, ":signJwt") {
			t.Fatalf("unexpected call %s", r.Path)
		}
	}
}

func TestMintNotMinterIsTyped(t *testing.T) {
	e := newEnv(t)
	e.iam.Refuse(403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission 'iam.serviceAccounts.signJwt' denied")
	_, err := token.Mint(context.Background(), e.signer, claims())
	if !errors.Is(err, token.ErrNotMinter) {
		t.Fatalf("err = %v", err)
	}
	e.iam.Refuse(503, "UNAVAILABLE", "", "later")
	if _, err := token.Mint(context.Background(), e.signer, claims()); !errors.Is(err, token.ErrUnavailable) {
		t.Fatalf("503: err = %v", err)
	}
}

func TestNewIAMSignerRefusesOddEmails(t *testing.T) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"})
	for _, bad := range []string{"", "a@example.com", "a/../b@p.iam.gserviceaccount.com", "a@p.iam.gserviceaccount.com/x", "A@p.iam.gserviceaccount.com"} {
		if _, err := token.NewIAMSigner(bad, ts); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRetryMintsFresh(t *testing.T) {
	e := newEnv(t)
	first := e.mint(t, claims(), t0)
	later := t0.Add(7 * time.Minute)
	second := e.mint(t, claims(), later)
	if first == second {
		t.Fatal("a retry reused the first token")
	}
	p1, p2 := payload(t, first), payload(t, second)
	if p2["iat"].(float64) <= p1["iat"].(float64) || p2["exp"].(float64) <= p1["exp"].(float64) {
		t.Fatalf("second token not fresher: %v vs %v", p1, p2)
	}
	e.setNow(later)
	if _, err := token.Exchange(context.Background(), e.cfg(), second); err != nil {
		t.Fatalf("the fresh token does not exchange: %v", err)
	}
}

func bucket(t *testing.T) (*gcpfake.GCS, *blobx.Bucket) {
	t.Helper()
	g := gcpfake.NewGCS(t)
	g.AddBucket("runs", 1, nil)
	return g, g.Bucket(t, "runs")
}

func TestTokenObjectKey(t *testing.T) {
	if got := token.ObjectKey("aurora-app", "r1"); got != "runs/aurora-app/r1/budget-token" {
		t.Fatal(got)
	}
}

func TestTokenObjectCreateIfAbsent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	first := e.mint(t, c, t0)
	if err := token.PutObject(ctx, b, c.Slug, c.Run, first); err != nil {
		t.Fatal(err)
	}
	second := e.mint(t, c, t0.Add(time.Minute))
	if err := token.PutObject(ctx, b, c.Slug, c.Run, second); !errors.Is(err, token.ErrObjectExists) {
		t.Fatalf("second put: %v", err)
	}
	got, _, err := b.Read(ctx, token.ObjectKey(c.Slug, c.Run))
	if err != nil || string(got) != first {
		t.Fatalf("the object was replaced (err %v)", err)
	}
	// A token that names another run never goes into this run's object.
	other := c
	other.Run = "someone-elses"
	foreign := e.mint(t, other, t0)
	if err := token.PutObject(ctx, b, "aurora-app", "fresh-run", foreign); !errors.Is(err, token.ErrTokenMismatch) {
		t.Fatalf("foreign token: %v", err)
	}
}

func TestTokenObjectDeletedAfterRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g, b := bucket(t)
	c := claims()
	tok := e.mint(t, c, t0)
	if err := token.PutObject(ctx, b, c.Slug, c.Run, tok); err != nil {
		t.Fatal(err)
	}
	got, err := token.TakeObject(ctx, b, c.Slug, c.Run)
	if err != nil || got != tok {
		t.Fatalf("take: %v", err)
	}
	if _, err := token.TakeObject(ctx, b, c.Slug, c.Run); !errors.Is(err, token.ErrNoToken) {
		t.Fatalf("second take: %v", err)
	}
	if _, _, err := b.Read(ctx, token.ObjectKey(c.Slug, c.Run)); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("object still there: %v", err)
	}

	// A failed delete fails the take and the token is not handed out.
	if err := token.PutObject(ctx, b, c.Slug, c.Run, tok); err != nil {
		t.Fatal(err)
	}
	g.FailObjectDeletes(1000)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	got, err = token.TakeObject(cctx, b, c.Slug, c.Run)
	if err == nil || got != "" {
		t.Fatalf("a failed delete returned a token (%v, %v)", got != "", err)
	}
	if strings.Contains(err.Error(), tok) {
		t.Fatal("the error carries the token")
	}
}

func TestTakeObjectDeletesAMismatchedToken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	other := claims()
	other.Run = "other-run"
	foreign := e.mint(t, other, t0)
	// Written straight to the victim's path (a bypass of PutObject).
	if _, err := b.Create(ctx, token.ObjectKey("aurora-app", "victim"), []byte(foreign), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := token.TakeObject(ctx, b, "aurora-app", "victim"); !errors.Is(err, token.ErrTokenMismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := b.Read(ctx, token.ObjectKey("aurora-app", "victim")); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatal("the mismatched token stayed readable")
	}
	if _, err := b.Create(ctx, token.ObjectKey("aurora-app", "junk"), []byte("not a jwt"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := token.TakeObject(ctx, b, "aurora-app", "junk"); !errors.Is(err, token.ErrTokenInvalid) {
		t.Fatalf("junk: %v", err)
	}
}

func TestDeleteObject(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	if err := token.DeleteObject(ctx, b, c.Slug, c.Run); err != nil {
		t.Fatalf("a missing object: %v", err)
	}
	if err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0)); err != nil {
		t.Fatal(err)
	}
	if err := token.DeleteObject(ctx, b, c.Slug, c.Run); err != nil {
		t.Fatal(err)
	}
	if _, err := token.TakeObject(ctx, b, c.Slug, c.Run); !errors.Is(err, token.ErrNoToken) {
		t.Fatal(err)
	}
}

func TestExchangeGivesTheRunsIdentity(t *testing.T) {
	e := newEnv(t)
	c := claims()
	s, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	if s.UID() != c.UID() {
		t.Fatalf("uid %q", s.UID())
	}
	cl, err := e.itk.IDTokenClaims(s.Token())
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{"fs": c.Slug, "fr": c.Run, "fp": c.FP, "rb": c.RB, "fx": float64(c.FX.UnixMilli()), "user_id": c.UID()} {
		if cl[k] != v {
			t.Errorf("%s = %v, want %v", k, cl[k], v)
		}
	}
	if !s.Expiry().Equal(t0.Add(time.Hour)) {
		t.Fatalf("expiry %v", s.Expiry())
	}
}

func TestExchangeOlderThanOneHourExpired(t *testing.T) {
	e := newEnv(t)
	tok := e.mint(t, claims(), t0)
	e.setNow(t0.Add(61 * time.Minute))
	var results []error
	cfg := e.cfg()
	cfg.OnResult = func(_ string, err error) { results = append(results, err) }
	_, err := token.Exchange(context.Background(), cfg, tok)
	if !errors.Is(err, token.ErrTokenExpired) {
		t.Fatalf("err = %v", err)
	}
	if n := len(e.itk.Requests()); n != 0 {
		t.Fatalf("an expired token reached Google (%d requests)", n)
	}
	if len(results) != 1 || !errors.Is(results[0], token.ErrTokenExpired) {
		t.Fatalf("OnResult = %v", results)
	}
	// Just inside the hour still works.
	e.setNow(t0.Add(59 * time.Minute))
	if _, err := token.Exchange(context.Background(), e.cfg(), tok); err != nil {
		t.Fatalf("59 minutes: %v", err)
	}
}

func TestExchangeRefusals(t *testing.T) {
	e := newEnv(t)
	tok := e.mint(t, claims(), t0)
	cfg := e.cfg()
	cfg.APIKey = "AIzaWrongKeyWrongKeyWrongKey"
	if _, err := token.Exchange(context.Background(), cfg, tok); !errors.Is(err, token.ErrTokenInvalid) {
		t.Fatalf("wrong key: %v", err)
	}
	// A token whose claims were changed after signing.
	parts := strings.Split(tok, ".")
	p := payload(t, tok)
	p["claims"].(map[string]any)["fs"] = "victim-app"
	raw, _ := json.Marshal(p)
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(raw) + "." + parts[2]
	if _, err := token.Exchange(context.Background(), e.cfg(), tampered); !errors.Is(err, token.ErrTokenInvalid) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := token.Exchange(context.Background(), e.cfg(), "garbage"); !errors.Is(err, token.ErrTokenInvalid) {
		t.Fatalf("garbage: %v", err)
	}
	cfg = e.cfg()
	cfg.APIKey = ""
	if _, err := token.Exchange(context.Background(), cfg, tok); err == nil {
		t.Fatal("no key accepted")
	}
	cfg = e.cfg()
	cfg.Register = nil
	if _, err := token.Exchange(context.Background(), cfg, tok); err == nil {
		t.Fatal("no redactor accepted")
	}
}

func TestExchangeUnreachableIsTyped(t *testing.T) {
	e := newEnv(t)
	tok := e.mint(t, claims(), t0)
	cfg := e.cfg()
	dead := httptest.NewServer(http.NotFoundHandler())
	cfg.IdentityURL = dead.URL
	dead.Close()
	_, err := token.Exchange(context.Background(), cfg, tok)
	if !errors.Is(err, token.ErrUnavailable) {
		t.Fatalf("closed server: %v", err)
	}
	assertClean(t, err, tok, apiKey, dead.URL)

	e.itk.Refuse(503, "UNAVAILABLE", "", "try later")
	if _, err := token.Exchange(context.Background(), e.cfg(), tok); !errors.Is(err, token.ErrUnavailable) {
		t.Fatalf("503: %v", err)
	}
	e.itk.Refuse(429, "RESOURCE_EXHAUSTED", "", "slow down")
	if _, err := token.Exchange(context.Background(), e.cfg(), tok); !errors.Is(err, token.ErrUnavailable) {
		t.Fatalf("429: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.itk.Refuse(0, "", "", "")
	if _, err := token.Exchange(ctx, e.cfg(), tok); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func assertClean(t *testing.T, err error, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(err.Error(), s) {
			t.Fatalf("the error text carries a secret or URL (%d bytes): %v", len(s), err)
		}
	}
}

func TestRefreshKeepsClaims(t *testing.T) {
	e := newEnv(t)
	e.itk.RotateRefresh(true)
	c := claims()
	s, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := e.itk.IDTokenClaims(s.Token())
	oldID := s.Token()
	e.setNow(t0.Add(50 * time.Minute))
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Token() == oldID {
		t.Fatal("the ID token did not change")
	}
	after, err := e.itk.IDTokenClaims(s.Token())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"fs", "fr", "fx", "fp", "rb", "user_id"} {
		if before[k] != after[k] {
			t.Errorf("%s changed: %v -> %v", k, before[k], after[k])
		}
	}
	if !s.Expiry().Equal(t0.Add(110 * time.Minute)) {
		t.Fatalf("expiry %v", s.Expiry())
	}
	// The rotated refresh token works for a second refresh.
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.itk.Refreshes() != 2 || e.itk.Exchanges() != 1 {
		t.Fatalf("refreshes %d exchanges %d", e.itk.Refreshes(), e.itk.Exchanges())
	}
}

func TestRefreshOutcomes(t *testing.T) {
	e := newEnv(t)
	c := claims()
	s, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	keep := s.Token()
	e.itk.Refuse(503, "UNAVAILABLE", "", "later")
	if err := s.Refresh(context.Background()); !errors.Is(err, token.ErrUnavailable) {
		t.Fatalf("503: %v", err)
	}
	e.itk.Refuse(0, "", "", "")
	e.itk.DisableUser(c.UID())
	if err := s.Refresh(context.Background()); !errors.Is(err, token.ErrRevoked) {
		t.Fatalf("disabled: %v", err)
	}
	e.itk.DeleteUser(c.UID())
	if err := s.Refresh(context.Background()); !errors.Is(err, token.ErrRevoked) {
		t.Fatalf("deleted: %v", err)
	}
	if s.Token() != keep {
		t.Fatal("a failed refresh replaced the token")
	}
}

// A server that answers with another run's identity must not be adopted.
func TestAnswerForAnotherIdentityRefused(t *testing.T) {
	e := newEnv(t)
	c := claims()
	good, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	// A different user signed in on the same fake provides a foreign ID token.
	other := c
	other.Run = "other-run"
	foreign, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, other, t0))
	if err != nil {
		t.Fatal(err)
	}
	foreignID := foreign.Token()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"idToken": foreignID, "refreshToken": "rt-x-123456", "expiresIn": "3600"})
	}))
	defer srv.Close()
	cfg := e.cfg()
	cfg.IdentityURL = srv.URL
	if _, err := token.Exchange(context.Background(), cfg, e.mint(t, c, t0)); !errors.Is(err, token.ErrTokenMismatch) {
		t.Fatalf("exchange: %v", err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": foreignID, "refresh_token": "rt-x-123456", "expires_in": "3600"})
	}))
	defer srv2.Close()
	cfg2 := e.cfg()
	cfg2.SecureTokenURL = srv2.URL
	s, err := token.Exchange(context.Background(), cfg2, e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	keep := s.Token()
	if err := s.Refresh(context.Background()); !errors.Is(err, token.ErrTokenMismatch) {
		t.Fatalf("refresh: %v", err)
	}
	if s.Token() != keep {
		t.Fatal("a foreign ID token was adopted")
	}
	_ = good
}

func TestKeepRefreshesAndRetries(t *testing.T) {
	e := newEnv(t)
	e.itk.SetClock(time.Now)
	e.itk.SetLifetime(2 * time.Second)
	e.setNow(time.Now())
	c := claims()
	c.FX = time.Now().Add(3 * time.Hour)
	var mu sync.Mutex
	var results []error
	cfg := e.cfg()
	cfg.Now = time.Now
	cfg.RefreshBefore = time.Minute // capped to half the 2 s lifetime
	cfg.RetryEvery = 20 * time.Millisecond
	cfg.OnResult = func(op string, err error) { mu.Lock(); results = append(results, err); mu.Unlock() }
	s, err := token.Exchange(context.Background(), cfg, e.mint(t, c, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	first := s.Token()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Keep(ctx) }()
	waitFor(t, func() bool { return e.itk.Refreshes() >= 1 }, "a background refresh")
	if s.Token() == first {
		t.Fatal("Keep refreshed but the token did not change")
	}
	// An outage: refreshes fail and are retried, then succeed.
	e.itk.Refuse(503, "UNAVAILABLE", "", "later")
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, r := range results {
			if errors.Is(r, token.ErrUnavailable) {
				n++
			}
		}
		return n >= 2
	}, "failed refreshes reported")
	have := e.itk.Refreshes()
	e.itk.Refuse(0, "", "", "")
	waitFor(t, func() bool { return e.itk.Refreshes() > have }, "a refresh after the outage")
	// Revocation ends Keep with the error.
	e.itk.DisableUser(c.UID())
	select {
	case err := <-done:
		if !errors.Is(err, token.ErrRevoked) {
			t.Fatalf("Keep returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Keep did not stop on revocation")
	}
	cancel()
}

func TestKeepStopsOnContext(t *testing.T) {
	e := newEnv(t)
	s, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, claims(), t0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Keep(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Keep ignored its context")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestFirebaseTokensRedacted(t *testing.T) {
	e := newEnv(t)
	c := claims()
	custom := e.mint(t, c, t0)
	e.itk.RotateRefresh(true)

	// 1. Registered before the first request that carries the token.
	var seen []string
	var regAtRequest []string
	var mu sync.Mutex
	wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		regAtRequest = e.registered()
		mu.Unlock()
		http.Error(w, `{"error":{"code":400,"message":"INVALID_CUSTOM_TOKEN : `+custom+` `+apiKey+`"}}`, 400)
	}))
	defer wrapped.Close()
	cfg := e.cfg()
	cfg.IdentityURL = wrapped.URL
	_, err := token.Exchange(context.Background(), cfg, custom)
	if !errors.Is(err, token.ErrTokenInvalid) {
		t.Fatalf("err = %v", err)
	}
	assertClean(t, err, custom, apiKey, wrapped.URL)
	mu.Lock()
	if !contains(regAtRequest, custom) || !contains(regAtRequest, apiKey) {
		t.Fatal("the custom token and key were not registered before the request")
	}
	mu.Unlock()

	// 2. Every token the real flow produces is registered, and redaction
	// removes them from text.
	s, err := token.Exchange(context.Background(), e.cfg(), custom)
	if err != nil {
		t.Fatal(err)
	}
	e.setNow(t0.Add(40 * time.Minute))
	id1 := s.Token()
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	id2 := s.Token()
	seen = append(seen, custom, apiKey, id1, id2, "rt-1-"+c.UID(), "rt-2-"+c.UID())
	reg := e.registered()
	for _, v := range []string{custom, apiKey, id1, id2} {
		if !contains(reg, v) {
			t.Errorf("a %d-byte secret was never registered", len(v))
		}
	}
	line := "log: " + strings.Join(seen, " | ")
	out := agent.Redact(line, reg)
	for _, v := range seen {
		if strings.Contains(out, v) {
			t.Errorf("a %d-byte secret survives redaction", len(v))
		}
	}

	// 3. The session never prints a token.
	for _, f := range []string{s.String(), (&gcpfakeFmt{s}).all()} {
		for _, v := range seen {
			if strings.Contains(f, v) {
				t.Fatal("formatting the session shows a token")
			}
		}
	}
}

type gcpfakeFmt struct{ s *token.Session }

func (g *gcpfakeFmt) all() string {
	return strings.Join([]string{sprint("%v", g.s), sprint("%+v", g.s), sprint("%#v", g.s)}, " ")
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
