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
	"sync/atomic"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget/token"
)

func TestTwoConcurrentTakersExactlyOneWins(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g, gcs := bucket(t)
	_ = g
	for name, b := range map[string]*blobx.Bucket{"gcs": gcs, "mem": blobx.Wrap(memblob.OpenBucket(nil))} {
		for round := 0; round < 20; round++ {
			c := claims()
			c.Run = "run-" + name + "-" + string(rune('a'+round))
			tok := e.mint(t, c, t0)
			if err := token.PutObject(ctx, b, c.Slug, c.Run, tok); err != nil {
				t.Fatal(err)
			}
			const n = 8
			var wins, lost atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					got, err := token.TakeObject(ctx, b, c.Slug, c.Run)
					switch {
					case err == nil && got == tok:
						wins.Add(1)
					case errors.Is(err, token.ErrNoToken) && got == "":
						lost.Add(1)
					default:
						t.Errorf("%s: unexpected (%v, %v)", name, got != "", err)
					}
				}()
			}
			close(start)
			wg.Wait()
			if wins.Load() != 1 || lost.Load() != n-1 {
				t.Fatalf("%s round %d: %d takers got the token, %d lost", name, round, wins.Load(), lost.Load())
			}
		}
	}
}

func TestCredentialURLsMustBeHTTPSOrLoopback(t *testing.T) {
	e := newEnv(t)
	tok := e.mint(t, claims(), t0)
	for _, bad := range []string{
		"http://identitytoolkit.googleapis.com", "http://example.invalid", "ftp://127.0.0.1", "http://10.0.0.1:80",
		"https://user:pw@example.invalid", "https://example.invalid?x=1", "https://example.invalid#frag", "https://example.invalid?", "//example.invalid", "https://", "http://localhost.evil.example",
	} {
		cfg := e.cfg()
		cfg.IdentityURL = bad
		if _, err := token.Exchange(context.Background(), cfg, tok); err == nil || !strings.Contains(err.Error(), "Identity Toolkit URL") {
			t.Errorf("IdentityURL %q accepted (%v)", bad, err)
		}
		cfg = e.cfg()
		cfg.SecureTokenURL = bad
		if _, err := token.Exchange(context.Background(), cfg, tok); err == nil || !strings.Contains(err.Error(), "Secure Token URL") {
			t.Errorf("SecureTokenURL %q accepted (%v)", bad, err)
		}
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"})
		if _, err := token.NewIAMSigner(signerEmail, ts, token.WithIAMEndpoint(bad)); err == nil {
			t.Errorf("IAM endpoint %q accepted", bad)
		}
	}
	if n := len(e.itk.Requests()); n != 0 {
		t.Fatalf("a refused URL still got %d requests", n)
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"})
	for _, ok := range []string{"https://iamcredentials.googleapis.com", "http://127.0.0.1:8080", "http://localhost:9", "http://[::1]:80"} {
		if _, err := token.NewIAMSigner(signerEmail, ts, token.WithIAMEndpoint(ok)); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

func rawServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestServerSideExpiryAndTransientStatuses(t *testing.T) {
	e := newEnv(t)
	tok := e.mint(t, claims(), t0)
	cfg := e.cfg()
	cfg.IdentityURL = rawServer(t, 400, `{"error":{"code":400,"message":"INVALID_CUSTOM_TOKEN : Firebase custom token has expired"}}`)
	if _, err := token.Exchange(context.Background(), cfg, tok); !errors.Is(err, token.ErrTokenExpired) {
		t.Fatalf("expired: %v", err)
	}
	for _, st := range []int{408, 425, 429, 500, 503} {
		cfg.IdentityURL = rawServer(t, st, `{"error":{"message":"X"}}`)
		if _, err := token.Exchange(context.Background(), cfg, tok); !errors.Is(err, token.ErrUnavailable) {
			t.Errorf("exchange %d: %v", st, err)
		}
	}
	s, err := token.Exchange(context.Background(), e.cfg(), tok)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []int{408, 425} {
		cfg2 := e.cfg()
		cfg2.SecureTokenURL = rawServer(t, st, `{}`)
		s3, err := token.Exchange(context.Background(), cfg2, tok)
		if err != nil {
			t.Fatal(err)
		}
		if err := s3.Refresh(context.Background()); !errors.Is(err, token.ErrUnavailable) || errors.Is(err, token.ErrRevoked) {
			t.Errorf("refresh %d: %v", st, err)
		}
	}
	_ = s
}

func TestMintRefusesInvalidUTF8(t *testing.T) {
	e := newEnv(t)
	for name, f := range map[string]func(*token.Claims){
		"slug": func(c *token.Claims) { c.Slug = "a\xffb" },
		"run":  func(c *token.Claims) { c.Run = "a\xc3" },
		"rb":   func(c *token.Claims) { c.RB = "x\xff" },
	} {
		c := claims()
		f(&c)
		_, err := token.MintAt(context.Background(), e.signer, c, t0)
		if err == nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestKeepHasARefreshFloor(t *testing.T) {
	e := newEnv(t)
	e.itk.SetClock(time.Now)
	e.itk.SetLifetime(time.Second)
	c := claims()
	c.FX = time.Now().Add(3 * time.Hour)
	cfg := e.cfg()
	cfg.Now = time.Now
	s, err := token.Exchange(context.Background(), cfg, e.mint(t, c, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := s.Keep(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if n := e.itk.Refreshes(); n != 0 {
		t.Fatalf("Keep refreshed %d times inside the 30 s floor", n)
	}
}

func TestCancelledContextIsNotAFailure(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var got []error
	cfg := e.cfg()
	cfg.OnResult = func(_ string, err error) { mu.Lock(); got = append(got, err); mu.Unlock() }
	s, err := token.Exchange(context.Background(), cfg, e.mint(t, claims(), t0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := token.Exchange(ctx, cfg, e.mint(t, claims(), t0)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("OnResult saw %v, want only the first success", got)
	}
}

func TestBothUserIDAndSubMustMatch(t *testing.T) {
	e := newEnv(t)
	c := claims()
	good, err := token.Exchange(context.Background(), e.cfg(), e.mint(t, c, t0))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(good.Token(), ".")
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	for name, mut := range map[string]func(map[string]any){
		"sub differs":     func(p map[string]any) { p["sub"] = "r~aurora-app~someone-else" },
		"user_id differs": func(p map[string]any) { p["user_id"] = "r~aurora-app~someone-else" },
		"neither":         func(p map[string]any) { delete(p, "sub"); delete(p, "user_id") },
	} {
		q := map[string]any{}
		for k, v := range p {
			q[k] = v
		}
		mut(q)
		b, _ := json.Marshal(q)
		id := parts[0] + "." + base64.RawURLEncoding.EncodeToString(b) + "." + parts[2]
		resp, _ := json.Marshal(map[string]any{"idToken": id, "refreshToken": "rt-x-123456", "expiresIn": "3600"})
		cfg := e.cfg()
		cfg.IdentityURL = rawServer(t, 200, string(resp))
		if _, err := token.Exchange(context.Background(), cfg, e.mint(t, c, t0)); !errors.Is(err, token.ErrTokenMismatch) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
