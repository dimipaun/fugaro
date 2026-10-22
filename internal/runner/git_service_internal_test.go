package runner

// This file is C2.2's runner half: the run's git credentials come from the
// installation's credentials service when it is on, and the job reads no App
// key of its own.
//
// It asks openProvider and refreshGitAuth -- the two functions a run's git
// credential goes through -- rather than running a whole run, for the reason
// gateway_service_internal_test.go gives for its own half: the run's own
// identity is the budget session's token and not an environment value, so the
// fixtures here build it the way the run's own policy read does
// (gitRunIdentity), and everything else is the job's environment.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// gitServiceToken is the installation token the stand-in service mints.
	// It is built at run time from a prefix and the test's name (Convention
	// 2), never written as a token-shaped literal.
	gitServiceTokenPrefix = "ghs_fixture_run_token_"
	// gitServiceIDToken and gitServiceRunToken stand in for the two
	// identities a mint carries. Neither is a credential: the first is what
	// the fake metadata server answers, the second what the fake identity
	// toolkit's exchange yields.
	plantedAppKeyEnv = "FUGARO_GITHUB_APP_PRIVATE_KEY"
)

// gitServiceFake is the credentials service as a run's git path meets it: one
// endpoint (POST /v1/github/installation-token), the two identities in the
// contract's own headers, and an answer carrying the minted token.
type gitServiceFake struct {
	t       *testing.T
	srv     *httptest.Server
	token   string
	expires time.Time
	status  int
	// body, when set, is answered verbatim instead of a mint's answer: it is
	// how a refusal (and a body carrying something it must not) is planted.
	body string

	mu    sync.Mutex
	calls []gitServiceCall
}

type gitServiceCall struct {
	path, auth, run, job, body string
}

func newGitServiceFake(t *testing.T, now time.Time) *gitServiceFake {
	t.Helper()
	f := &gitServiceFake{t: t, token: gitServiceTokenPrefix + strings.ReplaceAll(t.Name(), "/", "-"),
		expires: now.Add(time.Hour)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, gitServiceCall{
			path: r.URL.Path, auth: r.Header.Get("Authorization"),
			run: r.Header.Get("X-Fugaro-Run-Identity"), job: r.Header.Get("X-Fugaro-Run-Job"),
			body: string(body),
		})
		f.mu.Unlock()
		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			if f.body != "" {
				_, _ = io.WriteString(w, f.body)
			}
			return
		}
		if f.body != "" {
			_, _ = io.WriteString(w, f.body)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"token": f.token, "expires_at": f.expires, "repository": "github/acme/app",
		}); err != nil {
			t.Errorf("the fake service could not answer: %v", err)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *gitServiceFake) mintCalls() []gitServiceCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gitServiceCall(nil), f.calls...)
}

// gitMetadataFake is the job's metadata server: the Google ID token the mint
// presents as the job's identity (the runner's metadataIDToken asks it for
// one over GCE_METADATA_HOST, which is where the job's environment points
// this test too).
func gitMetadataFake(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/identity") || r.URL.Query().Get("audience") == "" {
			t.Errorf("the metadata server was asked for %s, which is not an identity", r.URL)
		}
		_, _ = io.WriteString(w, token)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gitRunIdentity exchanges the launcher's budget token for the run's own
// Firebase identity, the way budgetClient's own exchange does: this is the
// token a mint presents as the run's identity, and the fixture cannot make it
// up (as gateway_service_internal_test.go's svcSession says of its own).
func gitRunIdentity(t *testing.T) *token.Session {
	t.Helper()
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(svcSignerEmail)
	itk := gcpfake.NewIdentityToolkit(t, iam, svcAPIKey, svcProject)
	signer, err := token.NewIAMSigner(svcSignerEmail,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	custom, err := token.MintAt(context.Background(), signer, token.Claims{
		Slug: svcSlug, Run: svcRunID, FX: token.ExpiryFX(now, 3*time.Hour), FP: svcProject, RB: "owner@example.invalid",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := token.Exchange(context.Background(), token.Config{
		APIKey: svcAPIKey, IdentityURL: itk.URL, SecureTokenURL: itk.URL,
		Now: time.Now, Register: func(string) {},
	}, custom)
	if err != nil {
		t.Fatalf("exchanging the run's identity: %v", err)
	}
	return tok
}

// gitServiceRun is a run whose job has the credentials service on: the
// service's URL, the App's ID and no App key of its own anywhere in the
// environment.
func gitServiceRun(t *testing.T, svc *gitServiceFake, meta *httptest.Server, id *token.Session, edit func(*run)) *run {
	t.Helper()
	env := []string{
		providers.EnvGitHubAppID + "=1234",
		credentialsServiceURLEnv + "=" + svc.srv.URL,
		metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
	}
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Env = env
		r.d.OpenProvider = providers.FromEnv(env, svc.srv.Client(), nil)
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
		r.ident.tok = id
		if edit != nil {
			edit(r)
		}
	})
	return r
}

// TestNoAppKeyReadWithService is C2.2's red step: with the installation's
// credentials service on, the run's job holds no App key at all -- the
// service holds it (C2.1) and mints the run's installation token -- so the
// run's git credentials must come from the service, and nothing may read a
// key the job does not have.
//
// The run's own identity is the one the service verifies the mint with, and
// it is read before the session exists (the first credential is what the
// clone needs), so the fixture holds it where the policy read puts it
// (r.ident.tok) and leaves r.sess nil.
func TestNoAppKeyReadWithService(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	svc := newGitServiceFake(t, now)
	meta := gitMetadataFake(t, "job-id-token-1")
	id := gitRunIdentity(t)
	r := gitServiceRun(t, svc, meta, id, nil)
	r.d.Now = func() time.Time { return now }

	if err := r.openProvider(context.Background(), gitprov.KindGitHub, "the --provider flag"); err != nil {
		t.Fatalf("a run of an installation with the credentials service on could not open its github provider: %v", err)
	}
	calls := svc.mintCalls()
	if len(calls) != 1 {
		t.Fatalf("the service answered %d mint calls, want exactly 1", len(calls))
	}
	call := calls[0]
	if want := svc.token; r.auth.Token != want || r.auth.Env["GH_TOKEN"] != want {
		t.Errorf("the run's git credentials are %q (GH_TOKEN %q), want the service's token", r.auth.Token, r.auth.Env["GH_TOKEN"])
	}
	// The shape of GitAuth is what it always was: the GitHub adapter's own
	// username for an installation token, and GH_TOKEN for the agent's gh.
	if want := "x-access-token"; r.auth.Username != want {
		t.Errorf("the git username is %q, want %q", r.auth.Username, want)
	}
	if call.path != "/v1/github/installation-token" {
		t.Errorf("the mint was asked at %s", call.path)
	}
	if want := "Bearer job-id-token-1"; call.auth != want {
		t.Errorf("the mint's job identity is %q, want %q", call.auth, want)
	}
	if want := id.Token(); call.run != want || want == "" {
		t.Errorf("the mint's run identity is %q, want the run's own token", call.run)
	}
	if want := svcJob; call.job != want {
		t.Errorf("the mint reports job %q, want %q", call.job, want)
	}
	if want := `{"app_id":"1234"}`; call.body != want {
		t.Errorf("the mint's body is %s, want %s (the App ID, and the repository is never the caller's to name)", call.body, want)
	}
	// The job's environment holds no App key at all: nothing could have read
	// one, and the run works anyway.
	for _, kv := range r.d.Env {
		if strings.HasPrefix(kv, plantedAppKeyEnv+"=") {
			t.Fatalf("the fixture planted an App key: %s", kv)
		}
	}
}
