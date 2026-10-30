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

// TestMintCarriesBranchAndPRClaims: a launcher that binds the run to a branch
// and to pull requests mints those claims (fb, fpr, G6), and they travel to
// the signer as the JSON types the wire names them by.
func TestMintCarriesBranchAndPRClaims(t *testing.T) {
	e := newEnv(t)
	c := claims()
	c.Branch = "fugaro/20261002-120000-ab12"
	c.PRs = []int{7, 8}
	cl := payload(t, e.mint(t, c, t0))["claims"].(map[string]any)
	if cl["fb"] != c.Branch {
		t.Errorf("fb = %v, want %q", cl["fb"], c.Branch)
	}
	prs, ok := cl["fpr"].([]any)
	if !ok || len(prs) != 2 || prs[0].(float64) != 7 || prs[1].(float64) != 8 {
		t.Errorf("fpr = %v, want [7 8]", cl["fpr"])
	}
}

// TestSessionClaimsReadsBranchAndPRs: the claims the launcher minted come back
// out of the session (B1.7 consumes them), and a mint that named none reads
// back empty rather than as a zero PR.
func TestSessionClaimsReadsBranchAndPRs(t *testing.T) {
	e := newEnv(t)
	c := claims()
	c.Branch = "fugaro/20261002-115959-ab12"
	c.PRs = []int{3}
	s, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	got := s.Claims()
	if got.Branch != c.Branch {
		t.Errorf("branch = %q, want %q", got.Branch, c.Branch)
	}
	if len(got.PRs) != 1 || got.PRs[0] != 3 {
		t.Errorf("prs = %v, want [3]", got.PRs)
	}

	plain, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, claims(), t0))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Claims(); got.Branch != "" || len(got.PRs) != 0 {
		t.Errorf("an identity minted without the claims reads back %q/%v", got.Branch, got.PRs)
	}
}

// TestClaimsPRsAtMostFour: the fpr claim names at most MaxPRs pull requests,
// each positive. A mint that would exceed either bound is refused before the
// signer is called, so nothing is signed for a claim the service would refuse.
func TestClaimsPRsAtMostFour(t *testing.T) {
	e := newEnv(t)
	mut := func(f func(*token.Claims)) token.Claims { c := claims(); f(&c); return c }
	for name, c := range map[string]token.Claims{
		"five pull requests": mut(func(c *token.Claims) { c.PRs = []int{1, 2, 3, 4, 5} }),
		"a zero pull request": mut(func(c *token.Claims) {
			c.PRs = []int{1, 0}
		}),
		"a negative pull request": mut(func(c *token.Claims) {
			c.PRs = []int{-1}
		}),
	} {
		if _, err := token.MintAt(context.Background(), e.signer, c, t0); err == nil {
			t.Errorf("%s: minted", name)
		}
	}
	if n := len(e.iam.SignCalls()); n != 0 {
		t.Fatalf("a refused mint still called signJwt %d times", n)
	}
	ok := claims()
	ok.PRs = []int{1, 2, 3, 4}
	if _, err := token.MintAt(context.Background(), e.signer, ok, t0); err != nil {
		t.Fatalf("four pull requests refused: %v", err)
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
	_, err := token.MintAt(context.Background(), e.signer, claims(), t0) // not Mint: claims() fixes fx at t0+3h, which the wall clock passes
	if !errors.Is(err, token.ErrNotMinter) {
		t.Fatalf("err = %v", err)
	}
	e.iam.Refuse(503, "UNAVAILABLE", "", "later")
	if _, err := token.MintAt(context.Background(), e.signer, claims(), t0); !errors.Is(err, token.ErrUnavailable) {
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
	// tokens/, out of runs/ (§7.2, D16): one constant, the prefix the grants
	// name. A key back under runs/ is a key the job may delete.
	if got := token.ObjectKey("aurora-app", "r1"); got != "tokens/aurora-app/r1/budget-token" || token.ObjectPrefix != "tokens" {
		t.Fatal(got)
	}
}

func TestTokenObjectCreateIfAbsent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	first := e.mint(t, c, t0)
	if err := token.PutObject(ctx, b, c.Slug, c.Run, first, sealKey(1)); err != nil {
		t.Fatal(err)
	}
	second := e.mint(t, c, t0.Add(time.Minute))
	if err := token.PutObject(ctx, b, c.Slug, c.Run, second, sealKey(2)); !errors.Is(err, token.ErrObjectExists) {
		t.Fatalf("second put: %v", err)
	}
	if got, err := token.TakeObject(ctx, b, c.Slug, c.Run, sealKey(1)); err != nil || got != first {
		t.Fatalf("the object was replaced (err %v)", err)
	}
	// A token that names another run never goes into this run's object.
	other := c
	other.Run = "someone-elses"
	foreign := e.mint(t, other, t0)
	if err := token.PutObject(ctx, b, "aurora-app", "fresh-run", foreign, sealKey(3)); !errors.Is(err, token.ErrTokenMismatch) {
		t.Fatalf("foreign token: %v", err)
	}
}

// TestPutObjectNeverWritesPlaintext: the launcher seals every token object;
// a missing or short key writes nothing at all, rather than the token in the
// clear (§7.4: a plaintext object is the exposure the seal closes).
//
// Mutation (run, restored): let PutObject write the token plaintext when the
// key is empty — this test fails on "wrote".
func TestPutObjectNeverWritesPlaintext(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	tok := e.mint(t, c, t0)
	for _, key := range [][]byte{nil, {}, sealKey(4)[:16]} {
		if err := token.PutObject(ctx, b, c.Slug, c.Run, tok, key); !errors.Is(err, token.ErrSealKey) {
			t.Fatalf("PutObject with a %d-byte key = %v, want ErrSealKey", len(key), err)
		}
		if _, _, err := b.Read(ctx, token.ObjectKey(c.Slug, c.Run)); !errors.Is(err, blobx.ErrNotExist) {
			t.Fatalf("PutObject with a %d-byte key wrote an object: %v", len(key), err)
		}
	}
}

// TestTakeObjectNeverDeletes is §7.2's read-only take (D16): the job's grant
// on tokens/ has no delete, so the take issues none — a delete that fails
// cannot fail the take, the object stays for the lifecycle rule, and a second
// take with the same key reads the same token (consume-once rests on the key).
//
// Mutation (run, restored): put the delete back into TakeObject — the take
// fails under the refused deletes and this test fails.
func TestTakeObjectNeverDeletes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g, b := bucket(t)
	c := claims()
	tok := e.mint(t, c, t0)
	key := sealKey(5)
	if err := token.PutObject(ctx, b, c.Slug, c.Run, tok, key); err != nil {
		t.Fatal(err)
	}
	g.FailObjectDeletes(1000)
	for i := range 2 {
		got, err := token.TakeObject(ctx, b, c.Slug, c.Run, key)
		if err != nil || got != tok {
			t.Fatalf("take %d: %v", i, err)
		}
	}
	if _, _, err := b.Read(ctx, token.ObjectKey(c.Slug, c.Run)); err != nil {
		t.Fatalf("the object is gone after a read-only take: %v", err)
	}
}

// TestTakeObjectWithNoKeyReadsNothing: a run with no seal key reads no token
// at all, whatever the object holds — a plaintext token (which Fugaro never
// writes) included — and a missing object is ErrNoToken, the one answer the
// caller words for itself.
//
// Mutation (run, restored): let openToken return a plaintext object's bytes
// when no key is presented — this test fails on the plaintext row.
func TestTakeObjectWithNoKeyReadsNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	if _, err := token.TakeObject(ctx, b, c.Slug, c.Run, nil); !errors.Is(err, token.ErrNoToken) {
		t.Fatalf("no object: %v", err)
	}
	tok := e.mint(t, c, t0)
	if _, err := b.Create(ctx, token.ObjectKey(c.Slug, c.Run), []byte(tok), "text/plain"); err != nil {
		t.Fatal(err)
	}
	got, err := token.TakeObject(ctx, b, c.Slug, c.Run, nil)
	if !errors.Is(err, token.ErrNotSealed) || got != "" {
		t.Fatalf("a plaintext object with no key = (%v, %v), want ErrNotSealed and no token", got != "", err)
	}
	if strings.Contains(err.Error(), tok) {
		t.Fatal("the refusal carries the token")
	}
}

func TestTakeObjectRefusesAMismatchedToken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	other := claims()
	other.Run = "other-run"
	key := sealKey(6)
	// Sealed for the victim's name (a bypass of PutObject's own check).
	env, err := token.Seal(key, token.ObjectKey("aurora-app", "victim"), []byte(e.mint(t, other, t0)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create(ctx, token.ObjectKey("aurora-app", "victim"), env, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if _, err := token.TakeObject(ctx, b, "aurora-app", "victim", key); !errors.Is(err, token.ErrTokenMismatch) {
		t.Fatalf("err = %v", err)
	}
	junk, err := token.Seal(key, token.ObjectKey("aurora-app", "junk"), []byte("not a jwt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create(ctx, token.ObjectKey("aurora-app", "junk"), junk, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if _, err := token.TakeObject(ctx, b, "aurora-app", "junk", key); !errors.Is(err, token.ErrTokenInvalid) {
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
	if err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0), sealKey(7)); err != nil {
		t.Fatal(err)
	}
	if err := token.DeleteObject(ctx, b, c.Slug, c.Run); err != nil {
		t.Fatal(err)
	}
	if _, err := token.TakeObject(ctx, b, c.Slug, c.Run, sealKey(7)); !errors.Is(err, token.ErrNoToken) {
		t.Fatal(err)
	}
}

// A refused delete (gocloud maps the GCS 403 to NotFound) must not read as
// "the object was already gone": a replayable token left behind by a
// refused cleanup is a security-relevant miss, not a benign no-op
// (docs/design/bucket-iam.md §2.3).
func TestDeleteObjectForbiddenIsNotSilentSuccess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g, b := bucket(t)
	c := claims()
	if err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0), sealKey(8)); err != nil {
		t.Fatal(err)
	}
	g.DenyWrites("runs", token.ObjectKey(c.Slug, c.Run))
	if err := token.DeleteObject(ctx, b, c.Slug, c.Run); !errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("a refused delete = %v, want ErrForbidden", err)
	}
	// Still there: a refused delete is not silent success.
	if _, _, err := b.Read(ctx, token.ObjectKey(c.Slug, c.Run)); err != nil {
		t.Fatalf("the token object should still be there: %v", err)
	}
}

// A delete that fails for any other reason (the 503 of an overloaded backend,
// not the 403 of a grant) is reported too, and it is neither ErrForbidden nor
// silent success: the launcher's cleanup must not report a token removed that
// is still in the bucket.
func TestDeleteObjectReportsOtherFailures(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g, b := bucket(t)
	c := claims()
	if err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0), sealKey(9)); err != nil {
		t.Fatal(err)
	}
	g.FailObjectDeletes(1)
	err := token.DeleteObject(ctx, b, c.Slug, c.Run)
	if err == nil || errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("a delete the backend refused with 503 = %v, want a plain failure", err)
	}
	if !strings.Contains(err.Error(), "deleting the token object failed") {
		t.Errorf("err = %v, want it to say the delete failed", err)
	}
	if _, _, rerr := b.Read(ctx, token.ObjectKey(c.Slug, c.Run)); rerr != nil {
		t.Fatalf("the object should still be there: %v", rerr)
	}
}

// A put that fails other than by finding the object already there is reported
// with the bucket's own status, and nothing of it is left: the launcher
// replaces an earlier attempt's object only under its own key (D17), so a
// create the grant refuses must not read as a written token.
func TestPutObjectReportsAWriteFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g, b := bucket(t)
	c := claims()
	g.DenyWrites("runs", token.ObjectPrefix)
	err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0), sealKey(11))
	if err == nil || errors.Is(err, token.ErrObjectExists) {
		t.Fatalf("a create the grant refuses = %v, want a plain failure", err)
	}
	if !strings.Contains(err.Error(), "writing the token object failed") {
		t.Errorf("err = %v, want it to say the write failed", err)
	}
	if _, _, rerr := b.Read(ctx, token.ObjectKey(c.Slug, c.Run)); !errors.Is(rerr, blobx.ErrNotExist) {
		t.Fatalf("a refused create left an object: %v", rerr)
	}
}

// ObjectPresent's three answers, and the one it must never give: a refused
// read is not "no token object". A job account whose tokens/ grant is missing
// — the repository not re-applied since the budget token moved there — is
// refused exactly the way a missing object is reported (gocloud folds the GCS
// 403 into gcerrors.NotFound), so the caller that asks this question to decide
// whether a budget-on run may go on (the runner's refuseStrayToken) would be
// answered "absent" and let the run continue with no session and no kill
// switch. The read is strict, and a 403 is the error, as in DeleteObject.
func TestObjectPresent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := claims()
	key := token.ObjectKey(c.Slug, c.Run)
	t.Run("an absent object is false, no error", func(t *testing.T) {
		_, b := bucket(t)
		present, err := token.ObjectPresent(ctx, b, c.Slug, c.Run)
		if present || err != nil {
			t.Fatalf("ObjectPresent of nothing = %v, %v", present, err)
		}
	})
	t.Run("a present object is true", func(t *testing.T) {
		_, b := bucket(t)
		if err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0), sealKey(9)); err != nil {
			t.Fatal(err)
		}
		present, err := token.ObjectPresent(ctx, b, c.Slug, c.Run)
		if !present || err != nil {
			t.Fatalf("ObjectPresent of the token object = %v, %v", present, err)
		}
	})
	// A present object whose read is refused: the state of a job whose
	// tokens/ grant is missing. Not (false, nil), which reads as "this run
	// was never given a token".
	t.Run("a refused read of a present object is an error, not false", func(t *testing.T) {
		g, b := bucket(t)
		if err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0), sealKey(9)); err != nil {
			t.Fatal(err)
		}
		g.FailObjectGetsAfter("runs", key, 0)
		present, err := token.ObjectPresent(ctx, b, c.Slug, c.Run)
		if err == nil {
			t.Fatalf("a refused read of the token object = %v, nil: it reads as \"no token object\"", present)
		}
		if present {
			t.Fatalf("ObjectPresent of a refused read = true, %v", err)
		}
		if !errors.Is(err, blobx.ErrForbidden) {
			t.Fatalf("a refused read = %v, want ErrForbidden", err)
		}
	})
	// The job account's view of a missing object: no list permission, so GCS
	// answers 403, not 404. The primitive reports that 403 as it is
	// (ErrForbidden), never as absence and never as presence; what a 403
	// means is the caller's question (the runner's refuseStrayToken).
	t.Run("a missing object read without list permission is ErrForbidden", func(t *testing.T) {
		g, b := bucket(t)
		g.AnswerMissingAs403("runs", token.ObjectPrefix+"/")
		present, err := token.ObjectPresent(ctx, b, c.Slug, c.Run)
		if present || !errors.Is(err, blobx.ErrForbidden) {
			t.Fatalf("ObjectPresent of a missing object, no list = %v, %v; want false, ErrForbidden", present, err)
		}
	})
	// The read is a STRICT one: the same 403 answers the same way whether it
	// is the presence check that meets it or the object's bytes are wanted.
	// (The fake's own deny knob, DenyWrites, is a write-only refusal and must
	// not make this read fail: the grant a job holds on tokens/ is read-only,
	// so a refused *write* says nothing about the read.)
	t.Run("a write-only refusal does not make the read fail", func(t *testing.T) {
		g, b := bucket(t)
		if err := token.PutObject(ctx, b, c.Slug, c.Run, e.mint(t, c, t0), sealKey(9)); err != nil {
			t.Fatal(err)
		}
		g.DenyWrites("runs", token.ObjectPrefix+"/")
		present, err := token.ObjectPresent(ctx, b, c.Slug, c.Run)
		if !present || err != nil {
			t.Fatalf("ObjectPresent with writes denied = %v, %v", present, err)
		}
	})
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
	cfg.MinRefreshInterval = 10 * time.Millisecond
	cfg.OnResult = func(op string, err error) { mu.Lock(); results = append(results, err); mu.Unlock() }
	s, err := token.Exchange(context.Background(), cfg, e.mint(t, c, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	first := s.Token()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Keep(ctx) }()
	// The fake counts a refresh before the client swaps the token in, so wait
	// for the token itself.
	waitFor(t, func() bool { return e.itk.Refreshes() >= 1 && s.Token() != first }, "a background refresh that changed the token")
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
