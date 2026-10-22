package github

// ServiceTokens' own tests (C2.2): the run's installation tokens come from
// the installation's credentials service, fetched once and cached like the
// in-job mint, and every refusal is this package's own sentence carrying no
// value. The wire contract itself is pinned on both sides: here the request
// the run sends, and in internal/credsvc's
// TestTheServiceTokenContractIsTheMints the service's own spelling of it.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// serviceTokenPrefix is the shape of the installation token the fake service
// mints. It is built at run time (Convention 2), never written as a
// token-shaped literal.
const serviceTokenPrefix = "ghs_fixture_unit_token_"

// serviceFake is the credentials service as this package meets it: one
// endpoint, a recorded call, and an answer the test decides.
type serviceFake struct {
	t     *testing.T
	srv   *httptest.Server
	token string
	// expires is written by the test and read by the handler, so a test that
	// moves the clock moves what the answer says.
	expires time.Time
	status  int
	body    string

	mu    sync.Mutex
	calls []*http.Request
	body_ []string
}

func newServiceFake(t *testing.T, now time.Time) *serviceFake {
	t.Helper()
	f := &serviceFake{t: t, token: serviceTokenPrefix + strings.ReplaceAll(t.Name(), "/", "-"), expires: now.Add(time.Hour)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, r.Clone(context.Background()))
		f.body_ = append(f.body_, string(b))
		f.mu.Unlock()
		if f.body != "" {
			if f.status != 0 {
				w.WriteHeader(f.status)
			}
			_, _ = io.WriteString(w, f.body)
			return
		}
		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": f.token, "expires_at": f.expires, "repository": "github/acme/app",
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *serviceFake) minted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *serviceFake) call(i int) (*http.Request, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i], f.body_[i]
}

// TestServiceTokensContract: the mint the run makes is this service's own
// contract, and it carries the two identities and nothing else -- no App key,
// because this process holds none (C2.2).
func TestServiceTokensContract(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	f := newServiceFake(t, now)
	s := &ServiceTokens{
		URL:        f.srv.URL + "/",
		AppID:      "1234",
		Job:        "fugaro-run-job",
		RunIDToken: func() string { return "run-identity-token" },
		IDToken:    func(context.Context) (string, error) { return "job-id-token", nil },
		Now:        func() time.Time { return now },
	}
	tok, exp, err := s.Token(context.Background(), 2*time.Minute)
	if err != nil {
		t.Fatalf("minting at the service: %v", err)
	}
	if tok != f.token || !exp.Equal(f.expires) {
		t.Fatalf("Token = %q, %s, want the service's token and expiry", tok, exp)
	}
	if f.minted() != 1 {
		t.Fatalf("the service answered %d calls, want 1", f.minted())
	}
	req, body := f.call(0)
	if req.Method != http.MethodPost {
		t.Errorf("the mint is a %s, want POST", req.Method)
	}
	if want := ServiceTokenPath; req.URL.Path != want {
		t.Errorf("the mint was asked at %s, want %s", req.URL.Path, want)
	}
	if want := `{"app_id":"1234"}`; body != want {
		t.Errorf("the mint's body is %s, want %s: the App's ID is all a caller names", body, want)
	}
	if want := "Bearer job-id-token"; req.Header.Get("Authorization") != want {
		t.Errorf("the mint's job identity is %q, want %q", req.Header.Get("Authorization"), want)
	}
	if want := "run-identity-token"; req.Header.Get(ServiceHeaderRunIdentity) != want {
		t.Errorf("the mint's run identity is %q, want %q", req.Header.Get(ServiceHeaderRunIdentity), want)
	}
	if want := "fugaro-run-job"; req.Header.Get(ServiceHeaderRunJob) != want {
		t.Errorf("the mint reports job %q, want %q", req.Header.Get(ServiceHeaderRunJob), want)
	}
}

// TestServiceTokensCachesUntilExpiry: a mint is an act the service audits
// (C1.13), so the run asks once and keeps the answer until it is close to
// expiring -- the in-job tokenSource's own rule.
func TestServiceTokensCachesUntilExpiry(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	f := newServiceFake(t, now)
	s := &ServiceTokens{
		URL: f.srv.URL, AppID: "1234",
		RunIDToken: func() string { return "run-identity-token" },
		IDToken:    func(context.Context) (string, error) { return "job-id-token", nil },
		Now:        func() time.Time { return now },
	}
	for range 3 {
		if _, _, err := s.Token(context.Background(), 2*time.Minute); err != nil {
			t.Fatalf("minting: %v", err)
		}
	}
	if f.minted() != 1 {
		t.Fatalf("three calls within the token's life minted %d tokens, want 1", f.minted())
	}
	// Past the cached token's life, the next call mints another: the answer
	// is the service's, not a value this code keeps past its expiry.
	now = now.Add(2 * time.Hour)
	f.expires = now.Add(time.Hour)
	if _, _, err := s.Token(context.Background(), 2*time.Minute); err != nil {
		t.Fatalf("minting after the token expired: %v", err)
	}
	if f.minted() != 2 {
		t.Fatalf("after the token expired the service answered %d calls, want 2", f.minted())
	}
}

// TestServiceTokensRefusesAnswersThatAreNotOne: every answer that is not a
// token this caller can use is a refusal, and every refusal is a sentence
// written here -- it repeats nothing the answer carried, and the value the
// service minted (when there was one) reaches nobody.
func TestServiceTokensRefusesAnswersThatAreNotOne(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	const planted = "PLANTED-VALUE-IN-AN-ANSWER"
	cases := []struct {
		name   string
		plant  func(f *serviceFake)
		want   error
		wantIn string
	}{
		{"a refusal", func(f *serviceFake) {
			f.status, f.body = http.StatusForbidden, `{"error":"not_this_run","message":"`+planted+`"}`
		}, ErrServiceTokens, "403"},
		{"an unknown refusal code", func(f *serviceFake) { f.status, f.body = http.StatusForbidden, `{"error":"`+planted+`"}` }, ErrServiceTokens, "403"},
		{"a server error", func(f *serviceFake) { f.status, f.body = http.StatusInternalServerError, planted }, ErrServiceTokens, "500"},
		{"a body that is not a mint's", func(f *serviceFake) { f.body = planted }, ErrServiceTokens, "not a mint's"},
		{"an empty token", func(f *serviceFake) {
			f.body = `{"token":"","expires_at":"2026-10-22T11:00:00Z"}`
		}, ErrServiceTokens, "no token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t, now)
			tc.plant(f)
			s := &ServiceTokens{
				URL: f.srv.URL, AppID: "1234",
				RunIDToken: func() string { return "run-identity-token" },
				IDToken:    func(context.Context) (string, error) { return "job-id-token", nil },
				Now:        func() time.Time { return now },
			}
			tok, _, err := s.Token(context.Background(), 2*time.Minute)
			if err == nil {
				t.Fatal("a refused mint returned a token")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("the refusal is %v, want one wrapping %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("the refusal %q does not say what happened (%q)", err, tc.wantIn)
			}
			for _, v := range []string{planted, f.token, "run-identity-token", "job-id-token"} {
				if v != "" && strings.Contains(err.Error(), v) {
					t.Errorf("a value reached the refusal: %q", err)
				}
			}
			if tok != "" {
				t.Errorf("a refused mint returned %q", tok)
			}
			// Nothing was cached: the next call asks the service again rather
			// than handing out what it refused.
			before := f.minted()
			_, _, _ = s.Token(context.Background(), 2*time.Minute)
			if f.minted() != before+1 {
				t.Errorf("a refused token was cached: %d calls, want %d", f.minted(), before+1)
			}
		})
	}
}

// TestServiceTokensRefusesTooShortAnAnswer: the GitAuth contract promises a
// token valid for at least minValid. A token the service minted with less
// life than that cannot honor it, so it is refused -- and it is never
// printed, only refused.
func TestServiceTokensRefusesTooShortAnAnswer(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	f := newServiceFake(t, now)
	f.expires = now.Add(time.Minute)
	s := &ServiceTokens{
		URL: f.srv.URL, AppID: "1234",
		RunIDToken: func() string { return "run-identity-token" },
		IDToken:    func(context.Context) (string, error) { return "job-id-token", nil },
		Now:        func() time.Time { return now },
	}
	_, _, err := s.Token(context.Background(), 30*time.Minute)
	if !errors.Is(err, ErrServiceTokens) || !strings.Contains(err.Error(), "expires too soon") {
		t.Fatalf("a token with a minute of life for a request of thirty is %v", err)
	}
	if strings.Contains(err.Error(), f.token) {
		t.Errorf("the refusal carries the token: %q", err)
	}
	// The same token IS good enough for a caller that asked for less: the
	// refusal was about this call's demand, not about the token.
	if tok, _, err := s.Token(context.Background(), time.Minute); err != nil || tok != f.token {
		t.Fatalf("Token = %q, %v, want the token the service minted", tok, err)
	}
}

// TestServiceTokensRequiresBothIdentities: a mint is verified by the run's
// own identity and the job's, and this code presents both or calls nowhere
// (C1.10). The refusals are about the run's launch, not about the service.
func TestServiceTokensRequiresBothIdentities(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	ok := func(context.Context) (string, error) { return "job-id-token", nil }
	cases := []struct {
		name string
		s    *ServiceTokens
	}{
		{"no service URL", &ServiceTokens{AppID: "1234", RunIDToken: func() string { return "r" }, IDToken: ok}},
		{"no App ID", &ServiceTokens{URL: "https://creds.example", RunIDToken: func() string { return "r" }, IDToken: ok}},
		{"no run identity source", &ServiceTokens{URL: "https://creds.example", AppID: "1234", IDToken: ok}},
		{"no job identity source", &ServiceTokens{URL: "https://creds.example", AppID: "1234", RunIDToken: func() string { return "r" }}},
		{"an empty run identity", &ServiceTokens{URL: "https://creds.example", AppID: "1234", RunIDToken: func() string { return "" }, IDToken: ok}},
		{"an identity that is not a header value", &ServiceTokens{URL: "https://creds.example", AppID: "1234", RunIDToken: func() string { return "run\nidentity" }, IDToken: ok}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.Now = func() time.Time { return now }
			got, _, err := tc.s.Token(context.Background(), time.Minute)
			if !errors.Is(err, ErrServiceTokensIdentity) {
				t.Fatalf("Token = %q, %v, want github.ErrServiceTokensIdentity", got, err)
			}
			if strings.Contains(err.Error(), "run\nidentity") {
				t.Errorf("the refusal carries the identity: %q", err)
			}
		})
	}
}
