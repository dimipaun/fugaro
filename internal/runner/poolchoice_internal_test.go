package runner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The two placeholders the pool tests mount as provider keys. Neither is a
// credential and neither ever was one: nothing here talks to a provider, and
// ProviderCredential only asks that a value of the right shape is present.
const (
	poolKeyPrimary = "sk-or-v1-0123456789abcdef-not-a-real-key"
	poolKeyForeign = "sk-or-v1-fedcba9876543210-not-a-real-key"
)

// poolCoderModel is the model the tests' coder runs: a route's own ID for a
// Claude model, which is the one spelling a provider may claim out of Claude's
// family, so a pool member that serves it at all serves it as written (F23).
const poolCoderModel = "anthropic/claude-sonnet-5.5"

// poolSpec is the run a pool test needs: the craft's access as the owner's node
// publishes it, the credentials and pools beside it, the providers this job
// holds, and the subscription switch.
type poolSpec struct {
	access    string
	pools     []config.Pool
	creds     []config.Credential
	providers map[string]config.ModelProvider
	env       []string
	// spread is the subscription switch as this run read it. The read itself is
	// subscriptionSpread's, off a node document no hermetic test has a database
	// for; the tests set what it returned, which is the fact every rule below is
	// a consequence of.
	spread bool
}

// poolRun builds the run and the buffer it logs into: api-key auth with the
// named providers mounted, the coder pinned to the route's own model, and the
// owner's node carrying the crafts, the credentials and the pools.
func poolRun(t *testing.T, s poolSpec) (*run, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	r := &run{
		cfg: &config.Config{Agent: config.Agent{
			Auth:   "api-key",
			Models: config.ModelRoles{Coder: poolCoderModel},
		}},
		d: Deps{
			Log:       slog.New(slog.NewTextHandler(&log, nil)),
			Env:       s.env,
			Providers: s.providers,
		},
	}
	crafts := config.Crafts{Coder: config.Craft{Access: s.access}}
	r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{
		Crafts: &crafts, CraftCredentials: s.creds, CraftPools: s.pools,
	}}
	r.pools.read, r.pools.spread = true, s.spread
	return r, &log
}

// poolProviders is a job's provider set: or-primary claims the coder's routed
// model, and every other provider claims a model this run does not run — it is
// mounted in the runs whose crafts name it, and it serves nothing here.
func poolProviders(names ...string) map[string]config.ModelProvider {
	out := map[string]config.ModelProvider{}
	for _, name := range names {
		p := config.ModelProvider{
			Kind: config.ModelProviderKind, BaseURL: "https://" + name + ".example/api",
			Auth: "bearer", Secret: name + "-key", AllowDataTo: []string{"acme/site"}, RouteFeePct: 5,
		}
		if name == "or-primary" {
			p.Models = []string{poolCoderModel}
		} else {
			p.Models = []string{"meta/llama-4"}
		}
		out[name] = p
	}
	return out
}

// poolEnv mounts the named providers' keys in the job's environment, which is
// the launcher's own act: runner.ProvidersForRepo narrows the owner's
// providers to the ones whose allow_data_to names the repository, and the job
// carries those keys and no other.
func poolEnv(names ...string) []string {
	var out []string
	for i, n := range names {
		key := poolKeyPrimary
		if i > 0 {
			key = poolKeyForeign
		}
		out = append(out, config.ProviderKeyEnv(n+"-key")+"="+key)
	}
	return out
}

// poolMembers is the credentials block the pools below list members of.
func poolMembers(names ...string) []config.Credential {
	var out []config.Credential
	for _, n := range names {
		typ := config.CredentialTypeOpenRouter
		if strings.HasPrefix(n, "acct") {
			typ = config.CredentialTypeClaudeOAuth
		}
		out = append(out, config.Credential{Name: n, Type: typ})
	}
	return out
}

// TestPoolFailsOverOnExhaustion is the plan's red step and F5's own consumer: a
// stage whose craft goes out on a pool is served by the pool's first member,
// that member's own exhaustion signal ends the attempt, and the stage's next
// attempt is the next member — while a pool of one exhausted credential is a
// stage the walk refuses, because there is no next member to be.
//
// The text that ends the attempt is not a string this test makes up: it is what
// a real gateway relays from a provider answering 429 or 402 (relayedText,
// F5's own hermetic fake). A table that does not read what the road actually
// carries fails here, and so does a walk that moves on a text with no signal in
// it, or on a signal that belongs to another credential type.
func TestPoolFailsOverOnExhaustion(t *testing.T) {
	ctx := context.Background()
	creds := poolMembers("or-primary", "or-second")

	// One member: fresh it serves the stage, and once it is spent the walk
	// stands past the list.
	solo := config.Pool{Name: "solo", Members: []string{"or-primary"}}
	r, _ := poolRun(t, poolSpec{access: "solo", pools: []config.Pool{solo}, creds: creds,
		providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
	if got := r.checkStagePool(ctx, "implement"); got != "" {
		t.Fatalf("the stage was refused while the pool's only member was fresh: %s", got)
	}
	rateLimited := relayedText(t, 429, "rate_limit_error")
	if !r.poolEndedTheAttempt(ctx, "implement", agent.Result{IsError: true, Text: rateLimited}) {
		t.Fatalf("the member's own rate limit (%q) did not end the attempt", rateLimited)
	}
	if r.poolEndedTheAttempt(ctx, "implement", agent.Result{Text: rateLimited}) {
		t.Fatal("a successful stage moved the walk: its text carries the same words")
	}
	if r.poolEndedTheAttempt(ctx, "implement", agent.Result{IsError: true, Text: "the repository's own rate limit refused a push"}) {
		t.Fatal("a text with none of the member's signals moved the walk")
	}
	r.notePoolExhausted("implement")
	got := r.checkStagePool(ctx, "implement")
	if !strings.Contains(got, "has already refused this stage") || !strings.Contains(got, "solo") {
		t.Fatalf("a pool of one exhausted credential did not refuse the stage, got %q", got)
	}

	// Two members: the failover stands on the second, and the stage is served.
	two := config.Pool{Name: "pair", Members: []string{"or-primary", "or-second"}}
	r, log := poolRun(t, poolSpec{access: "pair", pools: []config.Pool{two}, creds: creds,
		providers: poolProviders("or-primary", "or-second"), env: poolEnv("or-primary", "or-second")})
	if got := r.checkStagePool(ctx, "implement"); got != "" {
		t.Fatalf("the first member did not serve the stage: %s", got)
	}
	r.notePoolExhausted("implement")
	if got := r.checkStagePool(ctx, "implement"); got != "" {
		t.Fatalf("the walk did not stand on the pool's second member: %s", got)
	}
	if !strings.Contains(log.String(), "or-second") {
		t.Fatalf("the failover line does not name the member it moved to: %q", log.String())
	}
	// The signal read is the member's own type's: the OAuth subscription's
	// sentence is no OpenRouter member's signal, and an attempt a spent
	// subscription would end leaves this stage alone.
	if r.poolEndedTheAttempt(ctx, "implement", agent.Result{IsError: true, Text: "claude ai usage limit"}) {
		t.Fatal("the walk read another credential type's signal as this member's")
	}
	if !r.poolEndedTheAttempt(ctx, "implement", agent.Result{IsError: true, Text: relayedText(t, 402, "invalid_request_error")}) {
		t.Fatal("the second member's own out-of-credit signal did not end the attempt")
	}
	r.notePoolExhausted("implement")
	if got := r.checkStagePool(ctx, "implement"); !strings.Contains(got, "has already refused this stage") {
		t.Fatalf("the walk did not refuse the stage once both members were spent, got %q", got)
	}
}

// TestMemberOutsideAllowDataToSkipped is F10 and D1627: a member the repository
// may not be served by is skipped and recorded, and the stage goes out on the
// next one — while a pool with no admitting member refuses the stage, naming
// what it lists.
func TestMemberOutsideAllowDataToSkipped(t *testing.T) {
	ctx := context.Background()
	creds := poolMembers("or-primary", "or-foreign")

	// or-foreign's provider is one this job does not hold: the repository is
	// not admitted to it, so the launcher mounted no key for it
	// (ProvidersForRepo), and the walk steps over it rather than sending the
	// stage's calls to a credential the repository may not be given.
	pool := config.Pool{Name: "pair", Members: []string{"or-foreign", "or-primary"}}
	r, log := poolRun(t, poolSpec{access: "pair", pools: []config.Pool{pool}, creds: creds,
		providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
	if got := r.checkStagePool(ctx, "implement"); got != "" {
		t.Fatalf("the stage was refused although a member admits the repository: %s", got)
	}
	if !strings.Contains(log.String(), "or-foreign") || !strings.Contains(log.String(), "skipped") {
		t.Fatalf("the skipped member was not recorded: %q", log.String())
	}
	// The walk stands on the admitting member, which is the second of the
	// pool: the skipped one is not a rung of the walk, and the stage that
	// fails over lands on or-primary and no other.
	candidates, skipped, err := r.poolCandidates(ctx, "implement")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Name != "or-primary" {
		t.Fatalf("the walk stands on %v, want or-primary alone", candidates)
	}
	if len(skipped) != 1 || skipped[0].Member.Name != "or-foreign" {
		t.Fatalf("the skipped members are %v, want or-foreign alone", skipped)
	}

	// A pool whose every member leaves this repository out refuses the stage:
	// there is no member to be served by, and the message names the pool and
	// what it lists.
	only := config.Pool{Name: "foreign", Members: []string{"or-foreign", "or-alsoforeign"}}
	r, _ = poolRun(t, poolSpec{access: "foreign", pools: []config.Pool{only},
		creds:     poolMembers("or-foreign", "or-alsoforeign"),
		providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
	got := r.checkStagePool(ctx, "implement")
	if !strings.Contains(got, "admitted to none of them") || !strings.Contains(got, "or-foreign") || !strings.Contains(got, "or-alsoforeign") {
		t.Fatalf("a pool with no admitting member did not refuse the stage naming its members, got %q", got)
	}
}

// TestSubscriptionPoolInertWithoutSwitch is D1622: a pool of two or more Claude
// OAuth subscription credentials serves nothing while the project's switch is
// off, and nothing but the switch turns it on.
func TestSubscriptionPoolInertWithoutSwitch(t *testing.T) {
	ctx := context.Background()
	creds := poolMembers("acct-one", "acct-two")
	pool := config.Pool{Name: "subs", Members: []string{"acct-one", "acct-two"}}

	off, _ := poolRun(t, poolSpec{access: "subs", pools: []config.Pool{pool}, creds: creds, spread: false})
	got := off.checkStagePool(ctx, "implement")
	if !strings.Contains(got, "serves nothing yet") || !strings.Contains(got, "subscription_spread") {
		t.Fatalf("a pool of two subscriptions served a stage with the switch off, got %q", got)
	}

	// With the switch on the pool is no longer inert: the craft's access
	// serves, which is the fact D1622 is about. What the walk then makes of
	// its members is the next question and not this one — the credentials
	// service is the path that serves a subscription (D1634, MC6), and this
	// job holds none.
	on, _ := poolRun(t, poolSpec{access: "subs", pools: []config.Pool{pool}, creds: creds, spread: true})
	if e := on.stageCrafts(ctx)[config.CraftCoder]; e.Inert {
		t.Fatalf("the pool is still inert with the switch on: %+v", e)
	}
	offE := off.stageCrafts(ctx)[config.CraftCoder]
	if !offE.Inert {
		t.Fatalf("the pool of two subscriptions is not inert with the switch off: %+v", offE)
	}
}

// TestPoolRefusedWhenAMemberDoesNotServeTheModel is F23 (D1637): a pool is
// admitted only if every member serves the model ID as written, and the member
// that does not is named.
func TestPoolRefusedWhenAMemberDoesNotServeTheModel(t *testing.T) {
	ctx := context.Background()
	// The coder runs a route's own Claude ID, so the provider serves it as
	// written and a subscription member does not: "anthropic/<id>" is the
	// spelling a run that has left the direct path uses, and the credentials
	// that reach Anthropic itself are never sent it.
	mixed := config.Pool{Name: "mixed", Members: []string{"or-primary", "acct-one"}}
	r, _ := poolRun(t, poolSpec{access: "mixed", pools: []config.Pool{mixed},
		creds: poolMembers("or-primary", "acct-one"), providers: poolProviders("or-primary")})
	err := r.checkPoolModels(ctx)
	if err == nil || !strings.Contains(err.Error(), "acct-one") || !strings.Contains(err.Error(), poolCoderModel) {
		t.Fatalf("a member that does not serve the model did not refuse the pool, got %v", err)
	}

	// A pool whose members all serve it is admitted: the same subscription
	// beside the built-in vertex, on a model of Claude's own spelling.
	direct := config.Pool{Name: "direct", Members: []string{"acct-one", "vertex"}}
	r, _ = poolRun(t, poolSpec{access: "direct", pools: []config.Pool{direct},
		creds: poolMembers("acct-one"), providers: poolProviders("or-primary")})
	r.cfg.Agent.Models.Coder = "claude-sonnet-5-5"
	if err := r.checkPoolModels(ctx); err != nil {
		t.Fatalf("a pool every member serves was refused: %v", err)
	}
}

// TestVertexPoolRefusedUnderEnforce is F8 and D1626's other half: a pool that
// lists the built-in Vertex credential cannot be capped by the gateway, so
// under budget.mode: enforce it is refused before any stage, in
// VertexBudgetRefusal's own words.
func TestVertexPoolRefusedUnderEnforce(t *testing.T) {
	ctx := context.Background()
	pool := config.Pool{Name: "with-vertex", Members: []string{"acct-one", "vertex"}}
	r, _ := poolRun(t, poolSpec{access: "with-vertex", pools: []config.Pool{pool}, creds: poolMembers("acct-one")})
	got := r.vertexPoolRefusal(ctx)
	if got == "" {
		t.Fatal("a pool listing the built-in vertex was admitted under enforce")
	}
	if !strings.Contains(got, VertexBudgetRefusal) || !strings.Contains(got, "with-vertex") || !strings.Contains(got, "vertex") {
		t.Fatalf("the refusal does not name the pool, the member and the reason: %q", got)
	}

	// A pool that lists no Vertex credential is not refused here: the walk's
	// own checks judge it.
	plain := config.Pool{Name: "plain", Members: []string{"acct-one", "acct-two"}}
	r, _ = poolRun(t, poolSpec{access: "plain", pools: []config.Pool{plain}, creds: poolMembers("acct-one", "acct-two")})
	if got := r.vertexPoolRefusal(ctx); got != "" {
		t.Fatalf("a pool with no Vertex member was refused under enforce: %q", got)
	}
}

// TestJobEnvHoldsOnlyItsCraftsSecrets is F20 and D1635: a job is deployed with
// the secrets of the crafts it runs and no other, so a pool's members never
// arrive together in one environment. A run whose coder goes out on a pool
// listing two providers builds a route for the model its crafts run and reads
// no other member's key at all — which is the same rule from the other side:
// the pool changes nothing about what the job holds.
func TestJobEnvHoldsOnlyItsCraftsSecrets(t *testing.T) {
	ctx := context.Background()
	pool := config.Pool{Name: "pair", Members: []string{"or-primary", "or-foreign"}}
	// or-foreign's key is deliberately not mounted: a run that read it while
	// building its routes would fail here as an infra error.
	r, _ := poolRun(t, poolSpec{access: "pair", pools: []config.Pool{pool},
		creds: poolMembers("or-primary", "or-foreign"),
		// The node's pools are read by the run; the providers and the
		// environment are the launcher's, which is what this test is about.
		providers: poolProviders("or-primary", "or-foreign"),
		env:       poolEnv("or-primary")})
	if _, _, err := ProviderCredential(r.d.Env, r.d.Providers["or-foreign"]); err == nil {
		t.Fatal("the fixture mounted the foreign provider's key, so it cannot pin what the run reads")
	}
	// A route is built for the provider the run's models name, and for no
	// other: a run that built one per pool member would read or-foreign's key
	// here and fail, which is what "the whole pool in one job's environment"
	// would cost (F20).
	routes, err := r.gatewayRoutes()
	if err != nil {
		t.Fatalf("building the run's routes read a key the job does not hold: %v", err)
	}
	if len(routes) != 1 || routes[0].Name != "or-primary" {
		t.Fatalf("the run built %d route(s) (%v), want the one provider its model names", len(routes), routes)
	}
	if got := r.checkStagePool(ctx, "implement"); got != "" {
		t.Fatalf("the stage was refused for a member the walk steps over: %s", got)
	}
}
