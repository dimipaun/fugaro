package runner

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/task"
)

// These are C1.15's runner half: which upstream a run's auth builds, what
// that upstream carries to the credentials service, and the ID token the job
// presents there. They ask gatewayUpstream -- the function startGateway
// builds its options from -- rather than running a whole run, because the
// cascade's end-to-end path is C1.19's switch: GatewayOn refuses the auth
// until that task teaches it, so a run's own startGateway cannot reach this
// code today and a test that insisted on a whole run would be testing the
// switch instead of the upstream.
//
// The one thing here that cannot be made up is the run's own identity: it is
// the budget session's token, not an environment value, so the wiring test
// opens a real session against internal/gcpfake's Firebase Auth, the way the
// run's own bootstrap does.

const (
	svcSignerEmail = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	svcAPIKey      = "AIzaSyFakeWebApiKeyForTests000000000"
	svcProject     = "aurora-fp"
	svcSlug        = "acme-app-dc4d4e59e884fc44"
	svcRunID       = "20261021-000000-abcd"
	svcJob         = "fugaro-run-job"
	// svcExecution is the platform's own report of the execution this run is
	// in: the job's name is read back out of it (jobName) and never from an
	// environment a launcher can write.
	svcExecution = "projects/aurora-fp/locations/europe-west1/jobs/" + svcJob + "/executions/" + svcJob + "-aaaaa"
	// plantedKey is a key-shaped value that is not, and never was, a key: on
	// a cascade run nothing may reach for it, and it must not surface in any
	// sentence either.
	plantedKey = "sk-ant-LOCALTESTKEY-0123456789abcdefghijklmnop"
)

// svcRun is a run whose only business is its gateway upstream: the file's
// auth, the platform's report of the execution, and the job's environment.
func svcRun(t *testing.T, auth string, edit func(*run)) *run {
	t.Helper()
	r := &run{
		cfg: &config.Config{Agent: config.Agent{Auth: auth}},
		d: Deps{
			Execution: svcExecution,
			Env:       []string{},
			Log:       slog.New(slog.DiscardHandler),
			Now:       time.Now,
		},
	}
	if edit != nil {
		edit(r)
	}
	return r
}

// svcSession opens the run's own budget session, against the fakes: the run
// is admitted, and the identity it was admitted with is the one the
// credentials service verifies it by (C1.10). Nothing is started, so no
// heartbeat, kill watch or lease runs.
func svcSession(t *testing.T) *budget.Session {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(svcSignerEmail)
	itk := gcpfake.NewIdentityToolkit(t, iam, svcAPIKey, svcProject)
	signer, err := token.NewIAMSigner(svcSignerEmail,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.launcher"}), token.WithIAMEndpoint(iam.URL))
	if err != nil {
		t.Fatal(err)
	}
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if slug != svcSlug {
		t.Fatalf("the fixture's slug is %q", slug)
	}
	now := time.Now()
	custom, err := token.MintAt(context.Background(), signer, token.Claims{
		Slug: slug, Run: svcRunID, FX: token.ExpiryFX(now, 3*time.Hour), FP: svcProject, RB: "owner@example.invalid",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := budget.Open(context.Background(), budget.Config{
		RTDBURL: db.URL, APIKey: svcAPIKey,
		IdentityURL: itk.URL, SecureTokenURL: itk.URL,
		Slug: slug, Run: svcRunID,
		Grace: time.Minute, Now: time.Now,
		Log: slog.New(slog.DiscardHandler),
		// The run's redactor, which every session registers its secrets with
		// before their first use: a real run's is r.addSecret. Nothing here
		// keeps them, and nothing in these tests prints one.
		Register: func(string) {},
	}, custom)
	if err != nil {
		t.Fatalf("opening the run's budget session: %v", err)
	}
	return sess
}

// TestExecUnchanged: D149. `fugaro exec` is untouched by the cascade. A local
// run has no installation identity and no path to a private service, so a
// cascade auth there is a mistake in the file, said so rather than reached
// for; and the auths a local run has always had are built exactly as they
// were.
func TestExecUnchanged(t *testing.T) {
	t.Run("a local cascade run is refused, not routed", func(t *testing.T) {
		// Even with a service in the environment, and a session in hand: the
		// refusal is about what a local invocation is.
		r := svcRun(t, config.AuthCascade, func(r *run) {
			r.d.Local = true
			r.d.Env = []string{credentialsServiceURLEnv + "=https://creds.example"}
		})
		r.sess = svcSession(t)
		_, err := r.gatewayUpstream(context.Background())
		if err == nil {
			t.Fatal("a local run was given a credentials service upstream (D149)")
		}
		if !strings.Contains(err.Error(), execName) || !strings.Contains(err.Error(), "D149") {
			t.Errorf("the refusal doesn't name the local invocation: %v", err)
		}
	})

	t.Run("a local api-key run is what it always was", func(t *testing.T) {
		r := svcRun(t, "api-key", func(r *run) {
			r.d.Local = true
			r.d.Env = []string{"ANTHROPIC_API_KEY=" + plantedKey}
		})
		up, err := r.gatewayUpstream(context.Background())
		if err != nil {
			t.Fatalf("a local api-key run: %v", err)
		}
		if up.Kind != "anthropic" || up.APIKey != plantedKey || up.Service != nil {
			t.Errorf("a local api-key run's upstream is %+v", up)
		}
	})

	t.Run("a local vertex run is what it always was", func(t *testing.T) {
		r := svcRun(t, "vertex", func(r *run) {
			r.d.Local = true
			r.d.Env = []string{"ANTHROPIC_VERTEX_PROJECT_ID=proj-1234", "CLOUD_ML_REGION=us-east5"}
			r.d.VertexTokens = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.test"})
		})
		up, err := r.gatewayUpstream(context.Background())
		if err != nil {
			t.Fatalf("a local vertex run: %v", err)
		}
		if up.Kind != "vertex" || up.Token == nil || up.Service != nil {
			t.Errorf("a local vertex run's upstream is %+v", up)
		}
	})

	t.Run("a local run never points at a service", func(t *testing.T) {
		// A developer's environment can hold anything, the service's URL
		// included; what matters is that no local upstream is a service one.
		r := svcRun(t, "api-key", func(r *run) {
			r.d.Local = true
			r.d.Env = []string{"ANTHROPIC_API_KEY=" + plantedKey, credentialsServiceURLEnv + "=https://creds.example"}
		})
		up, err := r.gatewayUpstream(context.Background())
		if err != nil {
			t.Fatalf("a local api-key run with a service in its env: %v", err)
		}
		if up.Kind == gateway.KindService || up.Service != nil {
			t.Errorf("a local run was pointed at the credentials service: %+v", up)
		}
	})
}

// TestCascadeWithoutTheServiceIsRefused: a cascade run in a job that has no
// credentials service is refused before any call, naming what is missing --
// the installation has not opted in (D147) -- and it never reaches for a key
// of its own instead (D143).
func TestCascadeWithoutTheServiceIsRefused(t *testing.T) {
	r := svcRun(t, config.AuthCascade, nil)
	r.sess = svcSession(t) // the run's identity is here: the service is what is missing
	_, err := r.gatewayUpstream(context.Background())
	if err == nil {
		t.Fatal("a cascade run with no credentials service was given an upstream")
	}
	for _, want := range []string{credentialsServiceURLEnv, "D147", "D143"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal doesn't mention %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), svcAPIKey) {
		t.Errorf("the refusal carries the fixture's key: %v", err)
	}
}

// TestCascadeRefusesATestUpstream: a cascade run's destination is the
// installation's own service and nothing else, so a run handed a test
// upstream as well is told rather than quietly sent to one of them.
func TestCascadeRefusesATestUpstream(t *testing.T) {
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Env = []string{credentialsServiceURLEnv + "=https://creds.example"}
		r.d.GatewayUpstream = "http://127.0.0.1:1"
	})
	r.sess = svcSession(t)
	err := func() error { _, err := r.gatewayUpstream(context.Background()); return err }()
	if err == nil {
		t.Fatal("a cascade run was sent to a test upstream")
	}
	if !strings.Contains(err.Error(), credentialsServiceURLEnv) {
		t.Errorf("the refusal doesn't say where the run's calls go: %v", err)
	}
}

// TestCascadeNeedsTheRunsOwnIdentity: the service verifies the run by the
// identity the budget backend admitted it with (C1.10), so a run without one
// is refused rather than presented under the job's identity alone.
func TestCascadeNeedsTheRunsOwnIdentity(t *testing.T) {
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Env = []string{credentialsServiceURLEnv + "=https://creds.example"}
	})
	// No session at all: r.sess is nil.
	if _, err := r.gatewayUpstream(context.Background()); err == nil {
		t.Fatal("a cascade run with no run identity was given an upstream")
	} else if !strings.Contains(err.Error(), "run identity") {
		t.Errorf("the refusal doesn't name what is missing: %v", err)
	}
}

// TestCascadeNeedsAJobName: the upstream reports which job it is in, and the
// gateway refuses a service upstream without a valid job name. A run that
// cannot name its job -- the platform reported no execution, and this is not
// a local invocation -- stops here rather than at the gateway.
func TestCascadeNeedsAJobName(t *testing.T) {
	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Env = []string{credentialsServiceURLEnv + "=https://creds.example"}
		r.d.Execution = ""
	})
	r.sess = svcSession(t)
	if _, err := r.gatewayUpstream(context.Background()); err == nil {
		t.Fatal("a cascade run with no job name was given an upstream")
	} else if !strings.Contains(err.Error(), "Cloud Run execution") {
		t.Errorf("the refusal doesn't name what is missing: %v", err)
	}
}

// TestServiceUpstreamCarriesTheRunsIdentityAndTheJobsIDToken is the wiring
// end to end inside the job: the upstream a cascade run builds is started as
// a gateway, the agent's call goes through it, and the credentials service
// receives the run's own Firebase identity, the job's Google ID token minted
// for the service's URL, the job's name -- and no credential of the job's own
// anywhere. What the service answers (a tier and a credential's name) reaches
// the run as a ServiceAnswer and the model's body, and the service's three
// headers reach the agent as nothing at all.
func TestServiceUpstreamCarriesTheRunsIdentityAndTheJobsIDToken(t *testing.T) {
	const idToken = "ya29.an-id-token-from-the-metadata-server"

	var mu sync.Mutex
	var metaSeen []*http.Request
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		metaSeen = append(metaSeen, r.Clone(r.Context()))
		mu.Unlock()
		if r.Header.Get("Metadata-Flavor") != "Google" {
			http.Error(w, "the metadata server is asked with Metadata-Flavor: Google", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(idToken + "\n"))
	}))
	defer meta.Close()

	// The credentials service, as the run will call it: a stand-in that
	// answers one messages call the way the service answers it.
	var svcSeen []*http.Request
	fake, svc := anthropicfake.New(t)
	fake.Func = func(r *http.Request, body []byte) anthropicfake.Reply {
		mu.Lock()
		svcSeen = append(svcSeen, r.Clone(r.Context()))
		mu.Unlock()
		rep := anthropicfake.MessageOK("claude-sonnet-5-5", pricing.Usage{Input: 1, Output: 3})
		rep.Header = http.Header{
			gateway.HeaderTier:       []string{gateway.TierB},
			gateway.HeaderCredential: []string{"repo-shared-b"},
		}
		return rep
	}

	r := svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Env = []string{
			credentialsServiceURLEnv + "=" + svc.URL,
			metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
		}
	})
	r.sess = svcSession(t)

	up, err := r.gatewayUpstream(context.Background())
	if err != nil {
		t.Fatalf("a cascade run's upstream: %v", err)
	}
	if up.Kind != gateway.KindService || up.BaseURL != svc.URL {
		t.Fatalf("the upstream is %+v, want the service the job was given", up)
	}
	if up.APIKey != "" || up.Token != nil {
		t.Fatalf("the service upstream carries a credential of the job's own: %+v", up)
	}
	if up.Service == nil || up.Service.Job != svcJob {
		t.Fatalf("the upstream reports the job %+v", up.Service)
	}
	// The run's identity is the session's, and it is the function the session
	// hands out rather than a value taken now: a caller that took one would
	// keep presenting a token the session has since refreshed.
	runToken := up.Service.RunIDToken()
	if runToken == "" || runToken != r.sess.IDToken()() {
		t.Fatalf("the run identity is not the session's own token")
	}

	var mu2 sync.Mutex
	var answers []gateway.ServiceAnswer
	gw, err := gateway.Start(context.Background(), gateway.Options{
		Upstream:        up,
		Prices:          pricing.Embedded(),
		Mode:            gateway.Observe,
		Log:             slog.New(slog.DiscardHandler),
		OnServiceAnswer: func(a gateway.ServiceAnswer) { mu2.Lock(); answers = append(answers, a); mu2.Unlock() },
	})
	if err != nil {
		t.Fatalf("starting the gateway on the run's own upstream: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	}()
	gw.BeginStage(gateway.Stage{Name: "implement", Model: "claude-sonnet-5-5"})

	req, err := http.NewRequest("POST", gw.URL()+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", gw.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the agent's call was answered %d: %s", resp.StatusCode, body)
	}

	// What the credentials service saw on the call the run made.
	mu.Lock()
	got, metas := svcSeen, metaSeen
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("the credentials service saw %d calls, want one", len(got))
	}
	h := got[0].Header
	if h.Get("Authorization") != "Bearer "+idToken {
		t.Errorf("the call's Authorization is %q, want the job's ID token from the metadata server", h.Get("Authorization"))
	}
	if h.Get(gateway.HeaderRunIdentity) != runToken {
		t.Errorf("the call's run identity is not the run's own token")
	}
	if h.Get(gateway.HeaderRunJob) != svcJob {
		t.Errorf("the call reports the job as %q, want %q", h.Get(gateway.HeaderRunJob), svcJob)
	}
	if h.Get("x-api-key") != "" {
		t.Error("the agent's own gateway token reached the credentials service")
	}
	for _, k := range []string{gateway.HeaderTier, gateway.HeaderCredential, gateway.HeaderSwitch} {
		if h.Get(k) != "" {
			t.Errorf("the agent set %s on the call to the service", k)
		}
	}
	if strings.Contains(h.Get("Authorization"), plantedKey) {
		t.Error("a key-shaped value was presented to the service")
	}

	// The metadata server was asked for a token for the service's own URL,
	// which is the whole of what Cloud Run IAM checks (D135).
	if len(metas) != 1 {
		t.Fatalf("the metadata server saw %d requests, want one", len(metas))
	}
	if aud := metas[0].URL.Query().Get("audience"); aud != svc.URL {
		t.Errorf("the ID token was minted for audience %q, want the service's URL %q", aud, svc.URL)
	}
	if metas[0].URL.Path != metadataIdentityPath {
		t.Errorf("the metadata request went to %q", metas[0].URL.Path)
	}

	// What the run learned from the service's answer, and what the agent got.
	mu2.Lock()
	defer mu2.Unlock()
	if len(answers) != 1 || answers[0].Tier != gateway.TierB || answers[0].Credential != "repo-shared-b" {
		t.Fatalf("the run was told %+v, want the service's tier and credential", answers)
	}
	for _, k := range []string{gateway.HeaderTier, gateway.HeaderCredential, gateway.HeaderSwitch} {
		if resp.Header.Get(k) != "" {
			t.Errorf("the agent's answer carries %s", k)
		}
	}
	if !strings.Contains(string(body), "claude-sonnet-5-5") {
		t.Errorf("the model's body did not reach the agent: %s", body)
	}
}

// TestMetadataIDToken: the job's own identity as the job mints it -- the path
// the platform serves, the audience Cloud Run IAM checks, the Metadata-Flavor
// header, and a token held to one clean header value. Nothing the metadata
// server answers is echoed into an error.
func TestMetadataIDToken(t *testing.T) {
	const audience = "https://creds-service-abc-ew.a.run.app"

	t.Run("the request it makes", func(t *testing.T) {
		const tok = "ya29.the-jobs-own-id-token"
		var mu sync.Mutex
		var seen []*http.Request
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Clone(r.Context()))
			mu.Unlock()
			_, _ = w.Write([]byte(tok + "\n"))
		}))
		defer srv.Close()
		env := []string{metadataHostEnv + "=" + strings.TrimPrefix(srv.URL, "http://")}
		got, err := metadataIDToken(context.Background(), env, audience)
		if err != nil {
			t.Fatalf("minting the job's identity: %v", err)
		}
		if got != tok {
			t.Errorf("the token is %q, want the metadata server's own (trailing whitespace trimmed)", got)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(seen) != 1 {
			t.Fatalf("the metadata server was asked %d times, want once", len(seen))
		}
		if seen[0].URL.Path != metadataIdentityPath {
			t.Errorf("the path is %q, want %q", seen[0].URL.Path, metadataIdentityPath)
		}
		if aud := seen[0].URL.Query().Get("audience"); aud != audience {
			t.Errorf("the audience is %q, want %q", aud, audience)
		}
		if seen[0].Header.Get("Metadata-Flavor") != "Google" {
			t.Error("the request carries no Metadata-Flavor: Google")
		}
	})

	for _, tc := range []struct {
		name    string
		answer  func(w http.ResponseWriter, r *http.Request)
		wantErr string
	}{
		{"a refusal from the metadata server", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "the metadata server's own sentence", http.StatusForbidden)
		}, "answered 403"},
		{"a token with a newline in it", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ya29.one\nya29.two"))
		}, "no usable identity token"},
		{"a token with a space in it", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ya29.one ya29.two"))
		}, "no usable identity token"},
		{"an empty answer", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("\n"))
		}, "no usable identity token"},
		{"an answer longer than a token may be", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("t", maxIDTokenBytes+1)))
		}, "no usable identity token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(tc.answer))
			defer srv.Close()
			env := []string{metadataHostEnv + "=" + strings.TrimPrefix(srv.URL, "http://")}
			got, err := metadataIDToken(context.Background(), env, audience)
			if err == nil {
				t.Fatalf("the mint returned %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the error is %q, want it to say %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "the metadata server's own sentence") {
				t.Errorf("the error echoes the metadata server's body: %v", err)
			}
			if strings.Contains(err.Error(), audience) {
				t.Errorf("the error carries the request's URL: %v", err)
			}
		})
	}

	t.Run("an unreachable metadata server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		env := []string{metadataHostEnv + "=" + strings.TrimPrefix(srv.URL, "http://")}
		srv.Close()
		got, err := metadataIDToken(context.Background(), env, audience)
		if err == nil {
			t.Fatalf("an unreachable metadata server returned %q", got)
		}
		if !strings.Contains(err.Error(), "could not be reached") {
			t.Errorf("the error is %q", err)
		}
	})

	t.Run("the platform's own host is the default", func(t *testing.T) {
		// With no GCE_METADATA_HOST -- which is how the platform's own hosts
		// look -- the token is asked for at the link-local name, and nothing
		// else in the environment moves it.
		if defaultMetadataHost != "metadata.google.internal" {
			t.Fatalf("the default host is %q", defaultMetadataHost)
		}
		// A run's env with no host in it, against the platform's name: the
		// request goes there and fails to connect, which is what a machine
		// that is not a job sees. The error names no host and no URL.
		_, err := metadataIDToken(context.Background(), []string{"PATH=/usr/bin"}, audience)
		if err == nil {
			t.Skip("a metadata server is reachable from this machine")
		}
		if strings.Contains(err.Error(), audience) || strings.Contains(err.Error(), "metadata.google.internal") {
			t.Errorf("the error names where the request went: %v", err)
		}
	})
}
