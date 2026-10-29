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
	"bytes"
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

	"gocloud.dev/blob/memblob"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// gitServiceTokenPrefix is the shape of the installation token the
	// stand-in service mints. The token is built at run time from this prefix
	// and the test's name (Convention 2), never written as a token-shaped
	// literal.
	gitServiceTokenPrefix = "ghs_fixture_run_token_"
	// plantedAppKeyEnv is where a job's App key would be if the installation
	// had no credentials service. The fixtures here never set it, and the
	// tests assert that nothing could have read one: with the service on, the
	// key is the service's (C2.2, D145).
	plantedAppKeyEnv = "FUGARO_GITHUB_APP_PRIVATE_KEY"
	// gitServiceIDToken is what the fake metadata server answers: the job's
	// Google ID token, one of the two identities a mint presents. It is not a
	// credential of any kind, and the service's own fixture tokens are the
	// same shape (gateway_service_internal_test.go's "job-id-token-1").
	gitServiceIDToken = "job-id-token-1"
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

// gitServiceBotID and gitServiceBotLogin are the App's bot account the fake
// GitHub API answers GET /user with: the identity a run's commits are
// attributed to (resolveCommitIdentity), which is what the provider asks the
// API for once, before the first commit.
const (
	gitServiceBotID    = "900001"
	gitServiceBotLogin = "fugaro-app[bot]"
)

// gitHubAPIFake is the GitHub API as a run's own reads meet it: GET /user,
// answered with the App's bot account. The provider's API root is this server
// and never api.github.com, so the identity read is hermetic like the rest of
// the run, and a run that asked for anything else is a failure rather than a
// call this fake would guess an answer for.
func gitHubAPIFake(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("the run asked the GitHub API for %s, which this fake does not answer", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"login": gitServiceBotLogin, "id": 900001, "type": "Bot",
		}); err != nil {
			t.Errorf("the fake GitHub API could not answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gitServiceRun is a run whose job has the credentials service on: the
// service's URL, the App's ID and no App key of its own anywhere in the
// environment.
//
// meta, when set, is the job's metadata server. A nil one is a job whose
// metadata server answers nothing: the environment points at the platform's
// own link-local address with a port nothing listens on, so a run that asks
// it for the job's identity is refused rather than waiting on the real
// metadata host.
func gitServiceRun(t *testing.T, svc *gitServiceFake, meta *httptest.Server, id *token.Session, edit func(*run)) *run {
	t.Helper()
	env := []string{
		providers.EnvGitHubAppID + "=1234",
		credentialsServiceURLEnv + "=" + svc.srv.URL,
		providers.EnvGitHubAPIURL + "=" + gitHubAPIFake(t).URL,
		metadataHostEnv + "=127.0.0.1:1",
	}
	if meta != nil {
		env[3] = metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://")
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
	meta := gitMetadataFake(t, gitServiceIDToken)
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
	if want := "Bearer " + gitServiceIDToken; call.auth != want {
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

// TestGitAuthFromService: with the service on, the run's git credentials are
// the tokens the service minted, they are redacted like every other
// credential the run holds, and they are fetched once rather than per call --
// a mint is an act the service audits (C1.13), so a run that minted one per
// API call would be a ledger of noise.
func TestGitAuthFromService(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	svc := newGitServiceFake(t, now)
	meta := gitMetadataFake(t, gitServiceIDToken)
	r := gitServiceRun(t, svc, meta, gitRunIdentity(t), nil)
	// The clock the runner keeps is the fake service's own, so the cached
	// token's life is read the way the service wrote it.
	r.d.Now = func() time.Time { return now }

	if err := r.openProvider(context.Background(), gitprov.KindGitHub, "the --provider flag"); err != nil {
		t.Fatalf("opening the github provider of a service-on run: %v", err)
	}
	auth := r.auth
	if auth.Token != svc.token || auth.Env["GH_TOKEN"] != svc.token {
		t.Fatalf("the run's git credentials are %q (GH_TOKEN %q), want the service's token", auth.Token, auth.Env["GH_TOKEN"])
	}
	if want := svc.expires; !auth.Expires.Equal(want) {
		t.Errorf("the credentials expire at %s, want the service's %s", auth.Expires, want)
	}
	// A minted token is a live credential: the run registers it for
	// redaction before anything else can print it (design §6.1, global
	// constraints), exactly as an in-job minted one is.
	if got := r.redact(svc.token); strings.Contains(got, svc.token) {
		t.Errorf("the service's token is not redacted: %q", got)
	}
	// A refresh within the token's life mints nothing: the second credential
	// is the first one.
	if err := r.refreshGitAuth(context.Background(), bootstrapAuthMinValid); err != nil {
		t.Fatalf("refreshing git credentials within the token's life: %v", err)
	}
	if calls := svc.mintCalls(); len(calls) != 1 {
		t.Errorf("the service answered %d mint calls for two credentials, want exactly 1", len(calls))
	}
	if r.auth.Token != svc.token {
		t.Errorf("the refreshed credentials are %q, want the same token", r.auth.Token)
	}
}

// TestServiceTokenRefusalsCarryNoValue: every way a service mint can fail is
// a refusal written in this repository, and none of them carries a value --
// not the token the service minted, not either identity the mint presented,
// and not the service's own text, whatever the answer's body holds.
//
// The bodies planted here are what a hostile or buggy writer would send:
// they carry the token and an identity, and the sentence the run reports
// must not.
func TestServiceTokenRefusalsCarryNoValue(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	const plantedRunToken = "eyJhbGciOiJSUzI1NiJ9.PLANTED-RUN-IDENTITY.PLANTED"
	cases := []struct {
		name   string
		plant  func(f *gitServiceFake)
		wantIn string
	}{
		{
			name: "the service refuses",
			plant: func(f *gitServiceFake) {
				f.status, f.body = http.StatusForbidden, `{"error":"not_this_run","message":"the run jti `+plantedRunToken+` is not admitted"}`
			},
			wantIn: "403",
		},
		{
			name: "the service is unreachable",
			plant: func(f *gitServiceFake) {
				f.status, f.body = http.StatusBadGateway, "planted upstream text "+plantedRunToken
			},
			wantIn: "502",
		},
		{
			name: "the answer carries no token",
			plant: func(f *gitServiceFake) {
				f.body = `{"token":"","expires_at":"2026-10-22T11:00:00Z","repository":"github/acme/app"}`
			},
			wantIn: "no token",
		},
		{
			name: "the answer is not a mint's",
			plant: func(f *gitServiceFake) {
				f.body = "not json at all: " + plantedRunToken
			},
			wantIn: "not a mint's",
		},
		{
			// The one case where a real token exists: the service minted one
			// and answered with it, and it must not reach the run -- the
			// token would expire inside the validity bootstrap asked for
			// (bootstrapAuthMinValid), so the run is refused rather than
			// handed a credential it cannot honor. It is never printed
			// either, which is what the planted-value loop below holds.
			name:   "a token with too little life",
			plant:  func(f *gitServiceFake) { f.expires = now.Add(time.Minute) },
			wantIn: "expires too soon",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newGitServiceFake(t, now)
			tc.plant(svc)
			meta := gitMetadataFake(t, gitServiceIDToken)
			r := gitServiceRun(t, svc, meta, gitRunIdentity(t), nil)
			r.d.Now = func() time.Time { return now }

			err := r.openProvider(context.Background(), gitprov.KindGitHub, "the --provider flag")
			if err == nil {
				t.Fatal("a run whose service refused its mint opened a github provider")
			}
			if !errors.Is(err, github.ErrServiceTokens) {
				t.Errorf("the refusal is %v, want one wrapping github.ErrServiceTokens", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantIn) {
				t.Errorf("the refusal %q does not say what happened (%q)", msg, tc.wantIn)
			}
			for _, planted := range []string{svc.token, plantedRunToken, gitServiceIDToken, "planted upstream text", "not json at all"} {
				if strings.Contains(msg, planted) {
					t.Errorf("a value reached the refusal: %q", msg)
				}
			}
			// The run reports the refusal with its own redactor, which must
			// not change what a value-free sentence says.
			if got := r.redact(msg); strings.Contains(got, svc.token) || strings.Contains(got, gitServiceIDToken) {
				t.Errorf("the redacted refusal carries a value: %q", got)
			}
		})
	}
}

// TestNoAppKeyReadWhenTheServiceRefusesIdentity: a run that cannot present
// the two identities a mint is verified by -- no run identity at all, or no
// metadata server to mint the job's -- is refused, and the refusal is about
// the run and not about the service. The service is never called: there is no
// call this run could make that the service would verify (C1.10).
func TestNoAppKeyReadWhenTheServiceRefusesIdentity(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		edit   func(*run)
		meta   bool
		wantIn string
	}{
		{
			name:   "no run identity",
			edit:   func(r *run) { r.ident.tok = nil },
			meta:   true,
			wantIn: "the run's own identity token is not available",
		},
		{
			name:   "no metadata server",
			meta:   false,
			wantIn: "the job's identity token could not be minted",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newGitServiceFake(t, now)
			var meta *httptest.Server
			if tc.meta {
				meta = gitMetadataFake(t, gitServiceIDToken)
			}
			r := gitServiceRun(t, svc, meta, gitRunIdentity(t), tc.edit)
			r.d.Now = func() time.Time { return now }

			err := r.openProvider(context.Background(), gitprov.KindGitHub, "the --provider flag")
			if err == nil {
				t.Fatal("a run with no identity to present opened a github provider")
			}
			if !errors.Is(err, github.ErrServiceTokensIdentity) {
				t.Errorf("the refusal is %v, want one wrapping github.ErrServiceTokensIdentity", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("the refusal %q does not say what is missing (%q)", err, tc.wantIn)
			}
			if calls := svc.mintCalls(); len(calls) != 0 {
				t.Errorf("a run with no identity to present called the service %d times", len(calls))
			}
		})
	}
}

// TestBitbucketUnchanged: the credentials service holds no Bitbucket
// credential (D145), so a job with the service on gets its Bitbucket
// credentials exactly as it always did -- from its own environment -- and
// never calls the service for them. The keyless github provider C2.2 opens
// for that job is about GitHub alone.
func TestBitbucketUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	svc := newGitServiceFake(t, now)
	const repoToken = "bitbucket-repo-access-token-1"
	env := []string{
		credentialsServiceURLEnv + "=" + svc.srv.URL,
		providers.EnvBitbucketToken + "=" + repoToken,
	}
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Env = env
		r.d.OpenProvider = providers.FromEnv(env, svc.srv.Client(), nil)
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	})
	r.d.Now = func() time.Time { return now }

	if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag"); err != nil {
		t.Fatalf("opening the bitbucket provider of a service-on run: %v", err)
	}
	if want := repoToken; r.auth.Token != want {
		t.Errorf("the run's git token is %q, want the job's own bitbucket token", r.auth.Token)
	}
	if want := "x-token-auth"; r.auth.Username != want {
		t.Errorf("the git username is %q, want %q", r.auth.Username, want)
	}
	if calls := svc.mintCalls(); len(calls) != 0 {
		t.Errorf("a bitbucket run called the credentials service %d times, want none (the service holds no bitbucket credential, D145)", len(calls))
	}
}

// TestTheServiceURLIsOneSpelling: the "the service is on" fact is spelled in
// two packages -- providers.EnvCredentialsServiceURL, which FromEnv reads to
// decide whether to open a keyless provider, and the runner's own
// credentialsServiceURLEnv, which serviceTokens reads to decide whether to
// install the service. They must be the same string: a drift would be silent
// and total, with the opener going keyless on one value while the runner
// installed on the other, and every service-on GitHub run stopping on
// github.ErrServiceTokensIdentity. The two spellings are held together here
// because this is the only package that can see both (providers cannot import
// the runner, and the runner's gateway constant is unexported).
func TestTheServiceURLIsOneSpelling(t *testing.T) {
	if providers.EnvCredentialsServiceURL != credentialsServiceURLEnv {
		t.Errorf("the service's URL is spelled %q in internal/gitprov/providers and %q in the runner: a job's environment is read for one and installed on the other, so the two must be one string",
			providers.EnvCredentialsServiceURL, credentialsServiceURLEnv)
	}
}

// TestLocalRunRefusesTheService: a local `fugaro exec` whose environment names
// a credentials service is refused up front, in D149's own words, rather than
// opening a keyless provider and dying later on an identity it cannot present
// -- the same refusal serviceUpstream makes of the same situation for the
// model path. Nothing is called at the service, and the refusal carries no
// value.
func TestLocalRunRefusesTheService(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	svc := newGitServiceFake(t, now)
	env := []string{
		providers.EnvGitHubAppID + "=1234",
		credentialsServiceURLEnv + "=" + svc.srv.URL,
	}
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Local = true
		r.d.Env = env
		r.d.OpenProvider = providers.FromEnv(env, svc.srv.Client(), nil)
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	})
	r.d.Now = func() time.Time { return now }

	err := r.openProvider(context.Background(), gitprov.KindGitHub, "the --provider flag")
	if err == nil {
		t.Fatal("a local run whose environment names a credentials service opened a github provider")
	}
	for _, want := range []string{execName, "D149", providers.EnvCredentialsServiceURL} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
	if errors.Is(err, github.ErrServiceTokensIdentity) {
		t.Errorf("the refusal is the service's own identity refusal (%v), not the local-run rule of D149", err)
	}
	if calls := svc.mintCalls(); len(calls) != 0 {
		t.Errorf("a local run called the credentials service %d times", len(calls))
	}
}

// TestTheMintsRunIdentityIsThePolicyReadsSession: the identity a mint
// presents is the session the run's OWN policy read exchanged, and this test
// drives that read for real instead of planting the field.
//
// The ordering is why it matters. The run's first git credential is the
// clone's, and it comes before the budget session exists (startBudgetBackend
// runs after the clone), so r.ident.tok -- the session exchangeIdentity keeps
// -- is the only identity the run has at its first mint. Here the launcher's
// sealed token is taken out of the runs bucket and exchanged at the Firebase
// fake, exactly as budgetClient does, and the git credential is then asked
// for: what the service receives must be a token THAT exchange issued, for
// this run's uid.
//
// Mutation (delete `r.ident.tok = tok` from exchangeIdentity, projectpolicy.go):
// the run has no identity at its first credential, ServiceTokens refuses with
// ErrServiceTokensIdentity, and openProvider fails -- which is what a
// service-on run would do at its clone, before any checkout.
func TestTheMintsRunIdentityIsThePolicyReadsSession(t *testing.T) {
	ctx := context.Background()
	// Real time, not a fixture date: the Firebase fake judges the exchange's
	// own claims by its clock, so a mint in the past would be refused by the
	// fake rather than by this run.
	now := time.Now()
	svc := newGitServiceFake(t, now)
	meta := gitMetadataFake(t, gitServiceIDToken)

	bucket := memblob.OpenBucket(nil)
	defer bucket.Close()
	blobs := blobx.Wrap(bucket)
	key := bytes.Repeat([]byte{0x5a}, token.KeySize)
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(svcSignerEmail)
	itk := gcpfake.NewIdentityToolkit(t, iam, svcAPIKey, svcProject)
	db := gcpfake.NewRTDB(t)
	signer, err := token.NewIAMSigner(svcSignerEmail,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	custom, err := token.MintAt(ctx, signer, token.Claims{
		Slug: svcSlug, Run: svcRunID, FX: token.ExpiryFX(now, 3*time.Hour), FP: svcProject, RB: "owner@example.invalid",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	// The launcher's token where the launcher leaves it: sealed, under
	// tokens/ in the runs bucket.
	if err := token.PutObject(ctx, blobs, svcSlug, svcRunID, custom, key); err != nil {
		t.Fatal(err)
	}

	env := []string{
		providers.EnvGitHubAppID + "=1234",
		credentialsServiceURLEnv + "=" + svc.srv.URL,
		metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
	}
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Env = env
		r.d.OpenProvider = providers.FromEnv(env, svc.srv.Client(), nil)
		r.d.Bucket = blobs
		r.d.Store = runstore.Open(bucket, svcSlug, svcRunID)
		r.d.Backend = Backend{
			RTDBURL: db.URL, APIKey: svcAPIKey, TokenMarked: true, TokenKey: key,
			Tune: func(c *budget.Config) { c.IdentityURL, c.SecureTokenURL = itk.URL, itk.URL },
		}
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	})

	// The run's own policy read, as bootstrap makes it: the launcher's token
	// is taken and exchanged, and no budget session exists yet.
	if _, found, err := r.projectPolicy(ctx); err != nil || found {
		t.Fatalf("the run's policy read: found %t, %v", found, err)
	}
	if r.sess != nil {
		t.Fatal("the fixture opened a budget session: the mint's identity must be the read's")
	}

	// The run's first git credential, which is the clone's.
	if err := r.openProvider(ctx, gitprov.KindGitHub, "the --provider flag"); err != nil {
		t.Fatalf("the run's first git credential: %v", err)
	}
	calls := svc.mintCalls()
	if len(calls) != 1 {
		t.Fatalf("the service answered %d mint calls, want 1", len(calls))
	}
	// The header is a token this fake signed -- and only this fake's own
	// exchange could have produced one -- and it names this run.
	claims, err := itk.IDTokenClaims(calls[0].run)
	if err != nil {
		t.Fatalf("the mint's run identity is not a token the run's own exchange issued: %v", err)
	}
	if want := token.UID(svcSlug, svcRunID); claims["sub"] != want {
		t.Fatalf("the mint's run identity is a token for %v, want this run's %s", claims["sub"], want)
	}
	if claims["fs"] != svcSlug || claims["fr"] != svcRunID {
		t.Fatalf("the mint's run identity claims %v/%v, want %s/%s", claims["fs"], claims["fr"], svcSlug, svcRunID)
	}
	if itk.Exchanges() != 1 {
		t.Errorf("the run's identity was exchanged %d times, want 1 (the policy read's own)", itk.Exchanges())
	}
	if want := svc.token; r.auth.Token != want {
		t.Errorf("the run's git credentials are %q, want the service's token", r.auth.Token)
	}
	// And the read kept that session on the run, which is what the mint above
	// just read: the same identity either way.
	if r.ident.tok == nil || r.ident.tok.Token() != calls[0].run {
		t.Error("the run does not hold the session its mint presented")
	}
}
