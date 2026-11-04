package bitbucket

// The read mint's client half (BB4): the call it makes, the identity it
// presents, the repository it names and checks, and the one rule every refusal
// here keeps -- no value reaches an error.
//
// The fixtures are built at run time. No literal in this file is, or looks
// like, a credential.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeReadService stands in for the credentials service's read mint. It records
// what it was asked and answers what the case set.
type fakeReadService struct {
	srv *httptest.Server

	token   string
	expires time.Duration
	// now is the clock the answer's expiry is built from, so a test that moves
	// its own clock forward moves the token's life with it.
	now       func() time.Time
	names     string // the repository the answer names; "" means the one asked for
	status    int
	body      string // a raw answer body, when a case needs one
	lastBody  string
	lastAuth  string
	lastPath  string
	callCount int
}

func newReadService(t *testing.T) *fakeReadService {
	t.Helper()
	f := &fakeReadService{token: "fake-service-token-not-a-real-credential", expires: 2 * time.Hour, now: time.Now}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.lastBody = string(body)
		f.lastAuth = r.Header.Get("Authorization")
		f.lastPath = r.URL.Path
		f.callCount++
		if f.body != "" {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(f.body))
			return
		}
		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"error":"state_unavailable","message":"refused"}`))
			return
		}
		var req struct {
			Repository string `json:"repository"`
		}
		_ = json.Unmarshal(body, &req)
		name := req.Repository
		if f.names != "" {
			name = f.names
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      f.token,
			"expires_at": f.now().Add(f.expires).UTC().Format(time.RFC3339Nano),
			"repository": name,
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// tokens is the client under test, pointed at the fake with a made-up identity.
func (f *fakeReadService) tokens(repo string) *ServiceReadTokens {
	return &ServiceReadTokens{
		URL:        f.srv.URL,
		Repository: repo,
		IDToken:    func(context.Context) (string, error) { return "fake-identity-token-not-a-real-credential", nil },
		HTTP:       f.srv.Client(),
	}
}

const testReadRepo = "workspace-a/repo-a"

func TestServiceReadTokens(t *testing.T) {
	f := newReadService(t)
	s := f.tokens(testReadRepo)
	// The clock is the test's, so "the cached token is still good" and "the
	// cached token has run down" are two instants a test can stand at without
	// waiting two hours for one of them.
	at := time.Now()
	s.Now = func() time.Time { return at }
	f.now = func() time.Time { return at }

	tok, exp, err := s.Token(context.Background(), 2*time.Minute)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != f.token || exp.IsZero() {
		t.Fatalf("Token = %q, %s, want the service's own token and life", tok, exp)
	}
	if f.lastPath != ServiceReadPath {
		t.Errorf("the call went to %s, want %s", f.lastPath, ServiceReadPath)
	}
	if !strings.HasPrefix(f.lastAuth, "Bearer ") {
		t.Errorf("the call presented no bearer identity: %q", f.lastAuth)
	}
	// The service hears the repository the caller is reading, in the spelling
	// the installation uses, and nothing else: it is the one field of this
	// contract, and the workspace is the service's to resolve and not this
	// caller's to send.
	if want := `{"repository":"` + testReadRepo + `"}`; f.lastBody != want {
		t.Errorf("the call's body = %s, want %s", f.lastBody, want)
	}

	// A second call under the same validity is served from the cache: a mint is
	// an audited act, so one read is one mint.
	if _, _, err := s.Token(context.Background(), 2*time.Minute); err != nil {
		t.Fatalf("the cached Token: %v", err)
	}
	if f.callCount != 1 {
		t.Errorf("the service was called %d times for two reads under one validity, want one", f.callCount)
	}
	// And a cached token that can no longer satisfy the asked validity mints
	// again, rather than handing the provider something that dies under it.
	at = at.Add(110 * time.Minute)
	if _, _, err := s.Token(context.Background(), 30*time.Minute); err != nil {
		t.Fatalf("the minting Token: %v", err)
	}
	if f.callCount != 2 {
		t.Errorf("the service was called %d times, want two: a token that cannot satisfy the asked validity is minted again", f.callCount)
	}

	t.Run("an answer naming another repository is refused", func(t *testing.T) {
		other := newReadService(t)
		other.names = "workspace-a/repo-elsewhere"
		if _, _, err := other.tokens(testReadRepo).Token(context.Background(), 2*time.Minute); err == nil {
			t.Fatal("a token the service minted for another repository was used")
		}
	})

	t.Run("an answer with no token is refused", func(t *testing.T) {
		bare := newReadService(t)
		bare.body = `{"expires_at":"2099-01-01T00:00:00Z","repository":"` + testReadRepo + `"}`
		bare.status = http.StatusOK
		if _, _, err := bare.tokens(testReadRepo).Token(context.Background(), 2*time.Minute); err == nil {
			t.Fatal("an answer carrying no token was accepted")
		}
	})

	t.Run("a mint with nothing to present is refused without a call", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			s    *ServiceReadTokens
		}{
			{"no service URL", &ServiceReadTokens{Repository: testReadRepo, IDToken: func(context.Context) (string, error) { return "x", nil }}},
			{"no repository", &ServiceReadTokens{URL: f.srv.URL, IDToken: func(context.Context) (string, error) { return "x", nil }}},
			{"no identity", &ServiceReadTokens{URL: f.srv.URL, Repository: testReadRepo}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := f.callCount
				if _, _, err := tc.s.Token(context.Background(), time.Minute); err == nil {
					t.Fatal("a read mint with nothing to present was made")
				}
				if f.callCount != before {
					t.Error("the service was called anyway")
				}
			})
		}
	})

	t.Run("the service URL must be one an identity token may be sent to", func(t *testing.T) {
		for _, raw := range []string{"http://creds.example.invalid", "https://user@creds.example.invalid", "https://creds.example.invalid/v1", "creds.example.invalid"} {
			s := f.tokens(testReadRepo)
			s.URL = raw
			if _, _, err := s.Token(context.Background(), time.Minute); err == nil {
				t.Errorf("a read mint was sent to %q, and it carries the caller's own identity", raw)
			}
		}
	})

	t.Run("a redirect is not followed", func(t *testing.T) {
		landed := 0
		elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			landed++
		}))
		defer elsewhere.Close()
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+ServiceReadPath, http.StatusTemporaryRedirect)
		}))
		defer redirect.Close()
		s := f.tokens(testReadRepo)
		s.URL = redirect.URL
		s.HTTP = redirect.Client()
		if _, _, err := s.Token(context.Background(), time.Minute); err == nil {
			t.Fatal("a redirected read mint was followed")
		}
		if landed != 0 {
			t.Fatalf("the caller's own identity token was replayed to the redirect's target %d times", landed)
		}
	})
}

// TestServiceReadTokensRedacted: the service's answer is another writer's bytes,
// and a refusal of it must carry none of them. The endpoint here is the case
// that makes that concrete -- it prints the token back in the message beside the
// error code, which is what an over-helpful (or hostile) answer looks like.
func TestServiceReadTokensRedacted(t *testing.T) {
	f := newReadService(t)
	f.status = http.StatusServiceUnavailable
	f.body = fmt.Sprintf(`{"error":"state_unavailable","message":"the service minted %s and would not hand it over"}`, f.token)

	_, _, err := f.tokens(testReadRepo).Token(context.Background(), time.Minute)
	if err == nil {
		t.Fatal("a refused read mint returned no error")
	}
	if strings.Contains(err.Error(), f.token) {
		t.Fatalf("the refusal carries a token: %v", err)
	}
	// The code IS relayed, because an operator needs to tell one refusal from
	// another, and it is this contract's own bounded word.
	if !strings.Contains(err.Error(), "state_unavailable") {
		t.Errorf("the refusal does not name the service's error code: %v", err)
	}
}
