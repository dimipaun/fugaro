package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/task"
)

// These are B2.2's runner half (F30): an api-key or oauth run whose job carries
// the credentials service's URL is served by the service, and declares which
// repository it is and, for oauth, which account. The switch is the URL alone
// (D2072): the mounts are still there (B2.3 keeps them, B2.7 drops them), and a
// run keyed on the flip list instead would ignore the URL that arrived first.
//
// The runs here are built the way C1.15's own tests build them (svcRun,
// svcSession): the file's auth, the platform's report of the execution, the
// job's environment, and -- where the run's own identity is asked for -- a real
// budget session against internal/gcpfake.

// plantedOAuthToken is the job's own mounted Claude token in these tests: a
// value shaped like nothing, which a switched run must neither reach for nor
// hand to the agent.
const plantedOAuthToken = "PLANTED-OAUTH-MOUNT-NOT-A-TOKEN"

// routedModel is the provider's own ID for the model a routed pin names, as
// the switch renders it (pricing's canonical map). A run that pins it is a run
// with a provider route, which is the case a switched api-key repository
// keeps (R6, D1634).
const routedModel = "anthropic/claude-sonnet-5.5"

// switchedJob is the job of a repository on the switch: the credentials
// service as a stand-in that answers a served call and keeps what it received,
// the metadata server the job's own ID token is minted from, and the two
// environment variables a switched run reads. seen returns the calls the
// service has received.
func switchedJob(t *testing.T) (env []string, seen func() []*http.Request) {
	t.Helper()
	fake, svc := anthropicfake.New(t)
	fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		rep := anthropicfake.MessageOK("claude-sonnet-5-5", pricing.Usage{Input: 1, Output: 3})
		rep.Header = http.Header{
			gateway.HeaderTier:       []string{gateway.TierB},
			gateway.HeaderCredential: []string{"repo-shared-b"},
		}
		return rep
	}
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "job-id-token-from-the-metadata-stand-in")
	}))
	t.Cleanup(meta.Close)
	return []string{
		credentialsServiceURLEnv + "=" + svc.URL,
		metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
	}, fake.Seen
}

// TestSwitchedAPIKeyRunGoesThroughTheService is B2.2's red step: an api-key
// run whose job carries the service's URL takes the service upstream, and the
// key the job still mounts (B2.3) is nowhere in it — the gateway is not the
// thing that holds this run's credential any more, the service is (D2071,
// D2072).
func TestSwitchedAPIKeyRunGoesThroughTheService(t *testing.T) {
	env, _ := switchedJob(t)
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Env = append(env, "ANTHROPIC_API_KEY="+plantedKey)
		r.spend = Spend{Mode: policy.ModeObserve, Prices: pricing.Embedded()}
	})
	r.sess = svcSession(t)

	up, err := r.gatewayUpstream(context.Background())
	if err != nil {
		t.Fatalf("a switched api-key run's upstream: %v", err)
	}
	if up.Kind != gateway.KindService || up.Service == nil || up.BaseURL == "" {
		t.Fatalf("a switched api-key run's upstream is %+v, want the credentials service", up)
	}
	if up.APIKey != "" || up.Token != nil {
		t.Errorf("the upstream carries a credential of the job's own: %+v", up)
	}
	if up.AuthMode != "api-key" {
		t.Errorf("the run declares the auth %q, want api-key", up.AuthMode)
	}
	if up.Account != "" {
		t.Errorf("an api-key run declares the account %q, want none", up.Account)
	}
	if !r.gatewayOn() {
		t.Error("the gateway is off for a switched api-key run in observe")
	}
	if s := fmt.Sprintf("%+v", up); strings.Contains(s, plantedKey) {
		t.Error("the mounted key reached the upstream")
	}
}

// TestSwitchedOAuthRunStartsTheGateway: an oauth run was the one auth the
// gateway never carried (F30), and on the switch it starts — with the
// declaration the service needs to serve the repository's own account, and
// with the agent's environment the switch's shape rather than the mounted
// token's.
func TestSwitchedOAuthRunStartsTheGateway(t *testing.T) {
	env, seen := switchedJob(t)
	r := svcRun(t, "oauth", func(r *run) {
		r.d.Env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+plantedOAuthToken)
		r.spend = Spend{Mode: policy.ModeObserve, Prices: pricing.Embedded()}
	})
	r.sess = svcSession(t)

	if !r.gatewayOn() {
		t.Fatal("the gateway is off for a switched oauth run in observe")
	}
	if got := r.servedAuth(); got != config.AuthCascade {
		t.Fatalf("the agent's environment is built as auth %q, want %s: the oauth arm refuses a gateway, and a switched run has one", got, config.AuthCascade)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.startGateway(ctx); err != nil {
		t.Fatalf("the run's own gateway: %v", err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = r.gw.Close(c)
	}()

	// The agent's own environment: the gateway, and not the token the job
	// still mounts (B2.3 keeps it until B2.7's flip).
	agentEnv, _, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{Auth: r.servedAuth(), Gateway: r.gateway()})
	if err != nil {
		t.Fatalf("the agent's environment for a switched oauth run: %v", err)
	}
	joined := strings.Join(agentEnv, "\n")
	if !strings.Contains(joined, "ANTHROPIC_BASE_URL="+r.gateway().BaseURL) {
		t.Errorf("the agent's environment does not point at the gateway: %s", joined)
	}
	if strings.Contains(joined, plantedOAuthToken) {
		t.Error("the mounted token reached the agent's environment")
	}

	if code, body := svcPost(t, r); code != http.StatusOK {
		t.Fatalf("the agent's call was answered %d: %s", code, body)
	}
	got := seen()
	if len(got) != 1 {
		t.Fatalf("the credentials service received %d calls, want one", len(got))
	}
	h := got[0].Header
	if h.Get(gateway.HeaderAuthMode) != "oauth" {
		t.Errorf("the call declares the auth %q, want oauth", h.Get(gateway.HeaderAuthMode))
	}
	if h.Get(gateway.HeaderAuthAccount) != config.DefaultClaudeAccount {
		t.Errorf("the call declares the account %q, want %q for a run that named none", h.Get(gateway.HeaderAuthAccount), config.DefaultClaudeAccount)
	}
}

// TestSwitchedRunSendsItsModeAndAccount: what the run declares is its own
// repository's auth and, for oauth, the account it was launched on — spelled
// the way the service reads an account (config.DefaultClaudeAccount for the
// account a run that named none uses), because the declaration is what the
// service holds the call to the record `fugaro update` wrote for that
// repository (D2070 (a), B2.1).
func TestSwitchedRunSendsItsModeAndAccount(t *testing.T) {
	for _, tc := range []struct {
		name        string
		auth        string
		account     string
		wantMode    string
		wantAccount string
	}{
		{name: "an api-key run declares no account", auth: "api-key", wantMode: "api-key"},
		{name: "an oauth run that named none", auth: "oauth", wantMode: "oauth", wantAccount: config.DefaultClaudeAccount},
		{name: "an oauth run launched on an account", auth: "oauth", account: "team-a", wantMode: "oauth", wantAccount: "team-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := switchedJob(t)
			r := svcRun(t, tc.auth, func(r *run) {
				r.d.Env = env
				r.spend = Spend{Mode: policy.ModeEnforce, Prices: pricing.Embedded()}
				if tc.account != "" {
					r.spec = &task.Spec{OAuthToken: tc.account}
				}
			})
			r.sess = svcSession(t)

			up, err := r.gatewayUpstream(context.Background())
			if err != nil {
				t.Fatalf("the upstream: %v", err)
			}
			if up.AuthMode != tc.wantMode {
				t.Errorf("the run declares the auth %q, want %q", up.AuthMode, tc.wantMode)
			}
			if up.Account != tc.wantAccount {
				t.Errorf("the run declares the account %q, want %q", up.Account, tc.wantAccount)
			}
		})
	}
}

// TestUnswitchedAPIKeyRunUnchanged: a repository that is not on the switch is
// exactly what it was — the gateway holds the job's own key, and no
// declaration is made anywhere.
func TestUnswitchedAPIKeyRunUnchanged(t *testing.T) {
	r := svcRun(t, "api-key", func(r *run) {
		r.d.Env = []string{"ANTHROPIC_API_KEY=" + plantedKey}
		r.spend = Spend{Mode: policy.ModeObserve, Prices: pricing.Embedded()}
	})
	up, err := r.gatewayUpstream(context.Background())
	if err != nil {
		t.Fatalf("an unswitched api-key run's upstream: %v", err)
	}
	if up.Kind != "anthropic" || up.APIKey != plantedKey || up.Service != nil {
		t.Errorf("an unswitched api-key run's upstream is %+v, want the job's own key", up)
	}
	if up.AuthMode != "" || up.Account != "" {
		t.Errorf("an unswitched run declares %q/%q, want no declaration", up.AuthMode, up.Account)
	}
	if !r.gatewayOn() {
		t.Error("the gateway is off for an api-key run in observe")
	}
}

// TestUnswitchedOAuthRunHasNoGateway: without the URL, an oauth run is the run
// it always was — its own token, no gateway, and nothing asked of the
// credentials service.
func TestUnswitchedOAuthRunHasNoGateway(t *testing.T) {
	r := svcRun(t, "oauth", func(r *run) {
		r.d.Env = []string{"CLAUDE_CODE_OAUTH_TOKEN=" + plantedOAuthToken}
		r.spend = Spend{Mode: policy.ModeObserve, Prices: pricing.Embedded()}
	})
	if r.gatewayOn() {
		t.Error("the gateway is on for an oauth run that is not on the switch")
	}
	if _, err := r.gatewayUpstream(context.Background()); err == nil {
		t.Fatal("an unswitched oauth run was given an upstream")
	} else if !strings.Contains(err.Error(), "oauth") {
		t.Errorf("the refusal doesn't name the auth: %v", err)
	}
}

// TestSwitchedRunKeepsItsProviderRoute (R6, D1634): a switched api-key
// repository whose pins name a provider-served model still starts its gateway.
// That repository's provider key is the job's own and stays on the runner
// path, so the route the run registers is the provider's own call and every
// other call of the run is the service's (B2.2, F30). Before this the run
// stopped in bootstrap: the service upstream refused the route the run's pins
// oblige it to register, and the sentence said nothing the operator could act
// on.
func TestSwitchedRunKeepsItsProviderRoute(t *testing.T) {
	env, _ := switchedJob(t)
	prices, err := pricing.Embedded().With(pricing.Overrides{
		routedModel: {Rates: pricing.Rates{InputPerM: 1, OutputPerM: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := svcRun(t, "api-key", func(r *run) {
		r.cfg.Agent.Models = config.ModelRoles{Coder: routedModel, Reviewer: "claude-opus-5"}
		r.d.Env = append(env, config.ProviderKeyEnv("openrouter-api-key")+"="+routedProviderKey)
		r.d.Providers = map[string]config.ModelProvider{
			"openrouter": {
				Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
				Secret: "openrouter-api-key", Models: []string{routedModel},
			},
		}
		r.spend = Spend{Mode: policy.ModeEnforce, Prices: prices}
	})
	r.sess = svcSession(t)

	if routes, err := r.gatewayRoutes(); err != nil || len(routes) != 1 {
		t.Fatalf("the run's own provider route = %+v (%v), want the one its pins name", routes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.startGateway(ctx); err != nil {
		t.Fatalf("a switched api-key run that pins a provider-served model: %v", err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = r.gw.Close(c)
	}()
	if !r.routed {
		t.Error("the run's gateway registered no provider route: the job's own key is what serves the model its pins name")
	}
}

// TestSwitchedRunWithBudgetOffRefusedAtBootstrap is D2071: the switch gains
// the budget mode as a condition, because the gateway is what reaches the
// service and the gateway exists only in observe and enforce. A repository on
// the switch whose installation has the mode off is refused in bootstrap — the
// run does not fall back to a mount the installation has stopped provisioning
// for it — and a local invocation, which is never on the switch (D149), is
// untouched by the refusal.
func TestSwitchedRunWithBudgetOffRefusedAtBootstrap(t *testing.T) {
	url := credentialsServiceURLEnv + "=https://creds.example"

	for _, auth := range []string{"api-key", "oauth"} {
		t.Run(auth+" with the budget off", func(t *testing.T) {
			r := svcRun(t, auth, func(r *run) {
				r.d.Env = []string{url}
				r.spend = Spend{Mode: policy.ModeOff}
			})
			err := r.applyRouting(nil, false)
			if err == nil {
				t.Fatal("a switched run with the budget off was admitted")
			}
			for _, want := range []string{credentialsServiceURLEnv, policy.ModeOff, "D2071"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal doesn't mention %s: %v", want, err)
				}
			}
			if !strings.Contains(err.Error(), policy.ModeObserve) || !strings.Contains(err.Error(), policy.ModeEnforce) {
				t.Errorf("the refusal doesn't name the modes that start a gateway: %v", err)
			}
		})
	}

	t.Run("the mode unset is off", func(t *testing.T) {
		r := svcRun(t, "oauth", func(r *run) {
			r.d.Env = []string{url}
		})
		if err := r.applyRouting(nil, false); err == nil {
			t.Fatal("a switched run with no budget mode was admitted")
		}
	})

	t.Run("observe admits it", func(t *testing.T) {
		r := svcRun(t, "oauth", func(r *run) {
			r.d.Env = []string{url}
			r.spend = Spend{Mode: policy.ModeObserve, Prices: pricing.Embedded()}
		})
		if err := r.applyRouting(nil, false); err != nil {
			t.Fatalf("a switched run in observe: %v", err)
		}
	})

	t.Run("a local run is not on the switch", func(t *testing.T) {
		r := svcRun(t, "api-key", func(r *run) {
			r.d.Local = true
			r.d.Env = []string{url, "ANTHROPIC_API_KEY=" + plantedKey}
			r.spend = Spend{Mode: policy.ModeOff}
		})
		if err := r.applyRouting(nil, false); err != nil {
			t.Fatalf("a local run was refused for the switch (D149): %v", err)
		}
	})
}
