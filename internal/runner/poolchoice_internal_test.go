package runner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
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
// family, so a provider member that serves it at all serves it as written
// (F23).
const poolCoderModel = "anthropic/claude-sonnet-5.5"

// poolDirectModel is the same coder on Claude's own spelling, which is the one
// the credentials that reach Anthropic itself take: it is what a pool holding a
// subscription or the built-in vertex is judged against.
const poolDirectModel = "claude-sonnet-5-5"

// poolOtherModel is what every provider but the primary claims (poolProviders):
// the senior review's own model in a mixed project, and so the model its craft's
// pool is judged against.
const poolOtherModel = "meta/llama-4"

// poolSpec is the run a pool test needs: the craft's access as the owner's node
// publishes it, the credentials and pools beside it, the providers this job
// holds, and the subscription switch.
type poolSpec struct {
	access    string
	pools     []config.Pool
	creds     []config.Credential
	providers map[string]config.ModelProvider
	env       []string
	// senior is the senior review craft's own access, "" for a craft that sets
	// none: a test that needs the judge's craft told apart from the senior
	// review's sets both.
	senior string
	// judge is the merge judge craft's own access, "" for a craft that sets none
	// and follows the coder (config.CraftParent).
	judge string
	// accounts are the Claude accounts the fixture policy admits for the
	// fixture repository, which is the list a pool's subscription members are
	// judged against (F10, stageAccountCandidates).
	accounts []string
	// spread is the subscription switch as this run read it. The read itself is
	// subscriptionSpread's, off a node document no hermetic test has a database
	// for; the tests set what it returned, which is the fact every rule below is
	// a consequence of.
	spread bool
	// auth is the run's own credential (agent.auth), "" for api-key.
	auth string
	// model is the coder's pin, "" for the routed one.
	model string
}

// poolRun builds the run and the buffer it logs into: the named auth with the
// named providers mounted, the coder pinned to the given model, and the owner's
// node carrying the crafts, the credentials and the pools.
//
// It stands on the walk's own fixture (newAutoFakeRun) for the store and the
// task spec a run reads its repository and its slug from, and it means to: a
// pool's subscription members are admitted by the same list the account walk
// stands on (stageAccountCandidates), so the two fixtures are one, and a second
// store here would be a second answer to which accounts this repository may
// use.
func poolRun(t *testing.T, s poolSpec) (*run, *bytes.Buffer) {
	t.Helper()
	auth, model := s.auth, s.model
	if auth == "" {
		auth = "api-key"
	}
	if model == "" {
		model = poolCoderModel
	}
	accounts := map[string]projectpolicy.Account{}
	for _, name := range s.accounts {
		accounts[name] = projectpolicy.Account{AllowRepos: []string{"github/acme/app"}}
	}
	crafts := config.Crafts{Coder: config.Craft{Access: s.access}}
	if s.senior != "" {
		crafts.SeniorReview = config.Craft{Access: s.senior}
	}
	if s.judge != "" {
		crafts.MergeJudge = config.Craft{Access: s.judge}
	}
	p := &projectpolicy.Policy{
		Crafts: &crafts, CraftCredentials: s.creds, CraftPools: s.pools, Accounts: accounts,
	}
	h := newAutoFakeRun(t, false, false, p, "", nil)
	r := h.r
	r.cfg = &config.Config{Agent: config.Agent{Auth: auth, Models: config.ModelRoles{Coder: model}}}
	r.d.Log = slog.New(slog.NewTextHandler(h.logs, nil))
	r.d.Providers, r.d.Env = s.providers, s.env
	r.projPolicy = policyRead{done: true, found: true, policy: p}
	r.pools.read, r.pools.spread = true, s.spread
	return r, h.logs
}

// poolProviders is a job's provider set, and it is a set a real project could
// hold: or-primary claims the coder's routed model and every other name claims
// a model this run does not run, so no two of them claim one model — the rule
// config's providerOverlapProblems writes and the gateway's validateRoutes
// writes again ("a model belongs to one route"). A test that needed two
// providers on one model would be a test of a configuration no installation can
// be in.
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
			p.Models = []string{poolOtherModel}
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

// poolCreds is the credentials block the pools below list members of: acct-*
// are Claude OAuth subscriptions and every other name is one of the providers
// above.
func poolCreds(names ...string) []config.Credential {
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

// TestStageCraftFollowsF13 pins the mapping the walk reads a stage's access
// through, and the one row where it deliberately disagrees with the role slots
// beside it: the first-line review runs the coder's model (reviewStages) on
// the review craft's access, so a mapping that answered by role would send
// those calls out on the coder's credential — the wrong half of a spending and
// egress grant. triage is D1848's and runs level 2's model, so it is the senior
// review's access; plan is the coder's own planning, which F13 does not name.
// The merge judge is the escalation ladder's own craft (E6), and its stage is
// named here rather than left to the loop below: the stage is the whole of a
// judge run and it is not in config.RunStages, so a mapping that dropped the
// row would send the judge's calls out on the run's default credential with
// nothing else in the suite saying so.
func TestStageCraftFollowsF13(t *testing.T) {
	for _, tc := range []struct {
		stage string
		want  string
	}{
		{"plan", config.CraftCoder},
		{"implement", config.CraftCoder},
		{"fix", config.CraftCoder},
		{"review_first", config.CraftReview},
		{"review", config.CraftSeniorReview},
		{"triage", config.CraftSeniorReview},
		{stageAdversarial, config.CraftSecurityReview},
		{stageMergeJudge, config.CraftMergeJudge},
		{"", ""},
		{"bootstrap", ""},
	} {
		if got := stageCraft(tc.stage); got != tc.want {
			t.Errorf("stageCraft(%q) = %q, want %q", tc.stage, got, tc.want)
		}
	}
	// Every stage a run opens has a craft: a stage the vocabulary holds and
	// this mapping does not would be a stage whose pool the run never reads.
	for _, stage := range config.RunStages {
		if stageCraft(stage) == "" {
			t.Errorf("the run's stage %q maps to no craft", stage)
		}
	}
}

// TestJudgeCraftFollowsTheCoder pins the craft the judge's two halves come from
// (E6): the judge stage runs the coder's role slot — the ladder's grade is
// written there (E4, applyGrade) — so its access is the coder's too, and never
// the senior review's.
//
// The configuration below is the one that tells the two apart, and the one a
// mixed project is in: the coder runs a provider's model and the senior review's
// craft goes out on a pool of a provider that serves another model. A judge that
// followed the senior review would take that pool and then be judged against the
// coder's model (poolMember, F23), so checkPoolModels would refuse the run at
// bootstrap and checkStagePool would refuse the stage — and a judge stage that
// does not run is a veto on every head (G1), for a reason that has nothing to do
// with the change under review.
func TestJudgeCraftFollowsTheCoder(t *testing.T) {
	ctx := context.Background()
	r, _ := poolRun(t, poolSpec{
		access: "or-main", senior: "review-pool",
		pools:     []config.Pool{{Name: "review-pool", Members: []string{"or-foreign"}}},
		creds:     poolCreds("or-main", "or-foreign"),
		providers: poolProviders("or-primary", "or-foreign"),
		env:       poolEnv("or-primary", "or-foreign"),
	})
	// The senior review runs its own model on that pool, which is what makes the
	// senior review's craft a coherent one for this run to be served by.
	r.cfg.Agent.Models.Reviewer = poolOtherModel
	// The run's recipe holds the judge step, so the walk judges its craft too
	// (poolStages): the assertions below are about which craft that is.
	r.rcp = judgeRecipe()

	eff := r.stageCrafts(ctx)
	if got := eff[config.CraftSeniorReview].Access; got != "review-pool" {
		t.Fatalf("the fixture's senior review goes out on %q, want its own pool: the test tells the two crafts apart by it", got)
	}
	if got := eff[config.CraftMergeJudge].Access; got != "or-main" {
		t.Errorf("the merge judge goes out on %q, want the coder's access: the judge stage runs the coder's model, so a craft taking another craft's access is judged against a model it does not run (F23)", got)
	}
	if _, reason := r.checkStagePool(ctx, stageMergeJudge); reason != "" {
		t.Errorf("the judge's own stage was refused: %s", reason)
	}
	if err := r.checkPoolModels(ctx); err != nil {
		t.Errorf("the run was refused at bootstrap over the judge's craft: %v", err)
	}
}

// TestPoolJudgedForTheJudgeStage: the judge's stage is judged by the same two
// checks as the run's own stages (E6), so a judge run whose craft names a pool
// that cannot serve the judge's model — or one holding the built-in vertex under
// enforce — is refused at bootstrap rather than when its one stage starts, which
// in a judge run is the verdict's absence and so a veto on the head (G1).
//
// The stage is judged where the run's recipe holds the judge step, and nowhere
// else: a repository may name an access for a craft its recipes never reach, and
// a refusal over a stage this run does not open would stop runs that never
// judge.
func TestPoolJudgedForTheJudgeStage(t *testing.T) {
	ctx := context.Background()
	// The merge judge's own access, and only its: the coder goes out on a
	// credential, so no other stage of this run names this pool.
	spec := poolSpec{access: "or-main", judge: "judge-pool",
		pools:     []config.Pool{{Name: "judge-pool", Members: []string{"or-foreign"}}},
		creds:     poolCreds("or-main", "or-foreign"),
		providers: poolProviders("or-primary", "or-foreign"),
		env:       poolEnv("or-primary", "or-foreign")}

	judge, _ := poolRun(t, spec)
	judge.rcp = judgeRecipe()
	err := judge.checkPoolModels(ctx)
	if err == nil || !strings.Contains(err.Error(), "crafts.merge-judge.access") || !strings.Contains(err.Error(), poolCoderModel) {
		t.Fatalf("a judge run whose craft names a pool that cannot serve the judge's model was admitted at bootstrap, got %v", err)
	}
	if _, reason := judge.checkStagePool(ctx, stageMergeJudge); reason == "" {
		t.Error("the judge's stage is served by a member the model check refuses")
	}

	// The negative control: a run whose recipe holds no judge step never opens
	// the stage, so the same craft is not judged and the run is admitted.
	ordinary, _ := poolRun(t, spec)
	if err := ordinary.checkPoolModels(ctx); err != nil {
		t.Errorf("a run that never opens the judge stage was refused over its craft: %v", err)
	}

	// D1626's half: a judge craft on a pool holding the built-in credential
	// cannot be capped by the gateway, so under enforce it is refused here.
	vertex, _ := poolRun(t, poolSpec{access: "or-main", judge: "judge-vertex",
		pools:     []config.Pool{{Name: "judge-vertex", Members: []string{config.CredentialVertex}}},
		creds:     poolCreds("or-main"),
		providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
	vertex.rcp = judgeRecipe()
	reason := vertex.vertexPoolRefusal(ctx)
	if !strings.Contains(reason, "judge-vertex") {
		t.Errorf("a judge craft on a pool holding the built-in vertex was not refused under enforce, got %q", reason)
	}
}

// judgeRecipe is a judge run's recipe as the pool walk reads it: the one step a
// mode: evaluate run of rung 3 holds (recipe.UsesMergeJudge).
func judgeRecipe() *recipe.Recipe {
	return &recipe.Recipe{Steps: []recipe.Step{{Kind: recipe.StepMergeJudge}}}
}

// TestPoolServesTheMemberItsModelNames pins what this path does with a pool: it
// serves the stage with the member whose own provider claims the stage's model,
// which is the provider the run's route for that model already names — and a
// pool holding a second member that this path could serve fails over to nothing,
// because there is nothing it could fail over to.
//
// The second half is the rule the first half rests on: a model belongs to one
// provider (config) and one route (the gateway), so a pool of two providers is
// a pool whose second member is sent nothing at all. The refusal is F23's, and
// it is what a run meets — not a failover onto a member whose provider never
// claimed the model.
func TestPoolServesTheMemberItsModelNames(t *testing.T) {
	ctx := context.Background()

	// One member: it is the run's provider for the stage's model, and the stage
	// goes out on it.
	solo := config.Pool{Name: "solo", Members: []string{"or-primary"}}
	r, log := poolRun(t, poolSpec{access: "solo", pools: []config.Pool{solo}, creds: poolCreds("or-primary"),
		providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
	member, reason := r.checkStagePool(ctx, "implement")
	if reason != "" {
		t.Fatalf("the stage was refused while its pool's member serves the model: %s", reason)
	}
	if member.Name != "or-primary" || member.Type != config.CredentialTypeOpenRouter {
		t.Fatalf("the stage is served by %+v, want the provider member or-primary", member)
	}
	if !strings.Contains(log.String(), "or-primary") {
		t.Fatalf("the run did not record the member the stage went out on: %q", log.String())
	}
	// The member is the one the gateway's own routes carry: the stage's model
	// goes to that member's provider, which is what makes the pool's decision
	// the one the calls follow.
	routes, err := r.gatewayRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Name != member.Name {
		t.Fatalf("the run's routes are %v, want the one the pool served the stage with (%s)", routes, member.Name)
	}
	// The same pool is admitted by F23 at bootstrap, which is where the run
	// asks it before any stage.
	if err := r.checkPoolModels(ctx); err != nil {
		t.Fatalf("the pool the stage was served by was refused at bootstrap: %v", err)
	}

	// Two members: the second cannot serve the stage's model, so the pool is
	// refused rather than served by the first — and refused at bootstrap too,
	// naming the member that does not serve it.
	pair := config.Pool{Name: "pair", Members: []string{"or-primary", "or-foreign"}}
	r, _ = poolRun(t, poolSpec{access: "pair", pools: []config.Pool{pair},
		creds: poolCreds("or-primary", "or-foreign"),
		// Both keys are mounted: this is the strongest shape of the reviewer's
		// question — a job that holds both members — and it still fails over to
		// nothing, because only one of them can be sent this model.
		providers: poolProviders("or-primary", "or-foreign"), env: poolEnv("or-primary", "or-foreign")})
	err = r.checkPoolModels(ctx)
	if err == nil || !strings.Contains(err.Error(), "or-foreign") || !strings.Contains(err.Error(), poolCoderModel) {
		t.Fatalf("a pool whose second member does not serve the model was admitted, got %v", err)
	}
	member, reason = r.checkStagePool(ctx, "implement")
	if reason != "" {
		t.Fatalf("the stage is served by the one member that serves it, and nothing failed over: %s", reason)
	}
	if member.Name != "or-primary" {
		t.Fatalf("the stage is served by %s, want the member the run's route names", member.Name)
	}
}

// TestMemberOutsideAllowDataToSkipped is F10 and D1627: a member the repository
// may not be served by is skipped and recorded, and it is never used. A pool
// whose only member leaves this repository out refuses the stage, naming what it
// lists and what became of each member — which is the shape this path can be in,
// because a second member that could serve the same model does not exist (see
// TestPoolServesTheMemberItsModelNames).
func TestMemberOutsideAllowDataToSkipped(t *testing.T) {
	ctx := context.Background()

	// or-foreign's provider is one this job does not hold: the repository is
	// not admitted to it, so the launcher mounted no key for it
	// (ProvidersForRepo), and the member is stepped over rather than sending the
	// stage's calls to a credential the repository may not be given.
	pool := config.Pool{Name: "foreign", Members: []string{"or-foreign"}}
	r, log := poolRun(t, poolSpec{access: "foreign", pools: []config.Pool{pool},
		creds: poolCreds("or-foreign"), providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
	member, reason := r.checkStagePool(ctx, "implement")
	if member.Name != "" || reason == "" {
		t.Fatalf("a pool whose only member this repository is not admitted to served the stage: %+v", member)
	}
	for _, want := range []string{"foreign", "or-foreign", "allow_data_to"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the refusal does not say %q: %s", want, reason)
		}
	}
	if !strings.Contains(log.String(), "or-foreign") || !strings.Contains(log.String(), "skipped") {
		t.Fatalf("the skipped member was not recorded: %q", log.String())
	}

	// An admitted member serves the stage: this is the pool the run may be
	// served by, and the member of it that is mounted.
	r, _ = poolRun(t, poolSpec{access: "home", pools: []config.Pool{{Name: "home", Members: []string{"or-primary"}}},
		creds: poolCreds("or-primary"), providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
	if _, reason := r.checkStagePool(ctx, "implement"); reason != "" {
		t.Fatalf("the admitted member did not serve the stage: %s", reason)
	}
}

// TestSubscriptionPoolInertWithoutSwitch is D1622 and D1626: a pool of two or
// more Claude OAuth subscription credentials serves nothing while the project's
// switch is off, nothing but the switch turns it on, and inside a pool that also
// holds another kind each subscription member is stepped over while it is off.
func TestSubscriptionPoolInertWithoutSwitch(t *testing.T) {
	ctx := context.Background()
	creds := poolCreds("acct-one", "acct-two")
	pool := config.Pool{Name: "subs", Members: []string{"acct-one", "acct-two"}}

	off, _ := poolRun(t, poolSpec{access: "subs", pools: []config.Pool{pool}, creds: creds, spread: false,
		auth: poolVertexAuth, model: poolDirectModel})
	member, reason := off.checkStagePool(ctx, "implement")
	if member.Name != "" || !strings.Contains(reason, "serves nothing yet") || !strings.Contains(reason, "subscription_spread") {
		t.Fatalf("a pool of two subscriptions served a stage with the switch off, got %q", reason)
	}
	if e := off.stageCrafts(ctx)[config.CraftCoder]; !e.Inert {
		t.Fatalf("the pool of two subscriptions is not inert with the switch off: %+v", e)
	}

	// With the switch on the pool is no longer inert: the craft's access
	// serves, which is the fact D1622 is about. What the run then makes of its
	// members is the credentials service's question (D1634, MC6) — this job
	// holds no subscription, and the refusal below says so. Both accounts are
	// ones the repository is admitted to, so what stops the stage is the job's
	// own credential and not an admission.
	on, _ := poolRun(t, poolSpec{access: "subs", pools: []config.Pool{pool}, creds: creds, spread: true,
		accounts: []string{"acct-one", "acct-two"}, auth: poolVertexAuth, model: poolDirectModel})
	if e := on.stageCrafts(ctx)[config.CraftCoder]; e.Inert {
		t.Fatalf("the pool is still inert with the switch on: %+v", e)
	}
	member, reason = on.checkStagePool(ctx, "implement")
	if member.Name != "" || !strings.Contains(reason, "acct-one") || !strings.Contains(reason, "does not hold") {
		t.Fatalf("a run that holds no subscription served the stage with one, got %q", reason)
	}

	// D1626's own shape is not inert, and its rule is per member: one
	// subscription beside the built-in vertex, on a model of Claude's own
	// spelling, is served by vertex while the subscription is stepped over —
	// and the subscription is stepped over before its own admission is asked
	// at all, so a run that never held the account is asked nothing about it.
	mixed := config.Pool{Name: "mixed", Members: []string{"acct-one", "vertex"}}
	mixedRun, log := poolRun(t, poolSpec{access: "mixed", pools: []config.Pool{mixed},
		creds: poolCreds("acct-one"), spread: false, auth: poolVertexAuth, model: poolDirectModel})
	if e := mixedRun.stageCrafts(ctx)[config.CraftCoder]; e.Inert {
		t.Fatalf("a subscription beside another kind made the whole pool inert: %+v", e)
	}
	member, reason = mixedRun.checkStagePool(ctx, "implement")
	if member.Name != config.CredentialVertex || reason != "" {
		t.Fatalf("the stage is served by %+v (%s), want the built-in vertex", member, reason)
	}
	if !strings.Contains(log.String(), "acct-one") || !strings.Contains(log.String(), "subscription_spread") {
		t.Fatalf("the subscription member was not recorded as the skipped one: %q", log.String())
	}
}

// TestPoolStopsAtAMemberTheJobDoesNotHold is F20 and D1635: a run is deployed
// with the credential it authenticates with and no other, so a pool member of a
// kind this container does not hold stops the run named rather than running the
// stage as a credential its own job does not hold — the record would name a
// credential that answered none of it.
//
// The pool is the legal one, `acct-one` beside the built-in vertex on a model of
// Claude's own spelling: the members serve the model as written (F23 admits it),
// and which of them this run holds is the run's own auth.
func TestPoolStopsAtAMemberTheJobDoesNotHold(t *testing.T) {
	ctx := context.Background()
	pool := config.Pool{Name: "to-vertex", Members: []string{"acct-one", "vertex"}}
	spec := poolSpec{access: "to-vertex", pools: []config.Pool{pool},
		creds: poolCreds("acct-one"), providers: poolProviders("or-primary"), model: poolDirectModel}

	// An api-key run holds its provider's key and not the project's own GCP
	// service, which is what the built-in vertex credential is: the subscription
	// is the credentials service's path and the vertex member is one this job
	// does not hold, so the stage is refused and both members are named.
	r, _ := poolRun(t, spec)
	member, reason := r.checkStagePool(ctx, "implement")
	if member.Name != "" || !strings.Contains(reason, "acct-one") || !strings.Contains(reason, "does not hold") || !strings.Contains(reason, "vertex") {
		t.Fatalf("the run did not stop at the members this job does not hold, got %+v / %q", member, reason)
	}

	// The same pool in a run authenticating as vertex: the built-in credential
	// is the project's own service account, which that run holds, and the
	// subscription member is still stepped over — this path never serves it.
	spec.auth = poolVertexAuth
	r, log := poolRun(t, spec)
	member, reason = r.checkStagePool(ctx, "implement")
	if reason != "" {
		t.Fatalf("a vertex run did not hold the built-in vertex credential: %s", reason)
	}
	if member.Name != config.CredentialVertex {
		t.Fatalf("the stage is served by %+v, want the built-in vertex", member)
	}
	if !strings.Contains(log.String(), "acct-one") {
		t.Fatalf("the subscription member this path cannot serve was not recorded: %q", log.String())
	}
}

// TestPoolRefusedWhenAMemberDoesNotServeTheModel is F23 (D1637): a pool is
// admitted only if every member serves the model ID as written, and the member
// that does not is named. The rule is asked of each member's own credential —
// for a provider, of that provider's own claim — which is what makes a pool of
// two providers a pool this path refuses rather than one it silently serves with
// the first.
func TestPoolRefusedWhenAMemberDoesNotServeTheModel(t *testing.T) {
	ctx := context.Background()

	// The coder runs a route's own Claude ID, so the provider serves it as
	// written and a subscription member does not: "anthropic/<id>" is the
	// spelling a run that has left the direct path uses, and the credentials
	// that reach Anthropic itself are never sent it.
	mixed := config.Pool{Name: "mixed", Members: []string{"or-primary", "acct-one"}}
	r, _ := poolRun(t, poolSpec{access: "mixed", pools: []config.Pool{mixed},
		creds: poolCreds("or-primary", "acct-one"), providers: poolProviders("or-primary")})
	err := r.checkPoolModels(ctx)
	if err == nil || !strings.Contains(err.Error(), "acct-one") || !strings.Contains(err.Error(), poolCoderModel) {
		t.Fatalf("a member that does not serve the model did not refuse the pool, got %v", err)
	}

	// Two providers, one spelling each: the second provider's models carry
	// llama, so it is no member for this stage and the pool is refused naming
	// it — the case a failover would need and the config cannot hold.
	twoProviders := config.Pool{Name: "two", Members: []string{"or-primary", "or-foreign"}}
	r, _ = poolRun(t, poolSpec{access: "two", pools: []config.Pool{twoProviders},
		creds:     poolCreds("or-primary", "or-foreign"),
		providers: poolProviders("or-primary", "or-foreign"), env: poolEnv("or-primary", "or-foreign")})
	err = r.checkPoolModels(ctx)
	if err == nil || !strings.Contains(err.Error(), "or-foreign") || !strings.Contains(err.Error(), poolCoderModel) {
		t.Fatalf("a pool whose second provider serves another model was admitted, got %v", err)
	}

	// A pool whose members all serve it is admitted: the same subscription
	// beside the built-in vertex, on a model of Claude's own spelling.
	direct := config.Pool{Name: "direct", Members: []string{"acct-one", "vertex"}}
	r, _ = poolRun(t, poolSpec{access: "direct", pools: []config.Pool{direct},
		creds: poolCreds("acct-one"), providers: poolProviders("or-primary"), model: poolDirectModel})
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
	r, _ := poolRun(t, poolSpec{access: "with-vertex", pools: []config.Pool{pool},
		creds: poolCreds("acct-one"), model: poolDirectModel})
	// The check is asked where the run asks it, and it refuses before any
	// stage: bootstrap's budget gate, under enforce.
	r.spend = Spend{Mode: "enforce"}
	err := r.checkBudget(ctx)
	if err == nil || !strings.Contains(err.Error(), VertexBudgetRefusal) {
		t.Fatalf("a pool listing the built-in vertex did not refuse the run under enforce: %v", err)
	}
	got := r.vertexPoolRefusal(ctx)
	if !strings.Contains(got, VertexBudgetRefusal) || !strings.Contains(got, "with-vertex") || !strings.Contains(got, "vertex") {
		t.Fatalf("the refusal does not name the pool, the member and the reason: %q", got)
	}

	// A pool that lists no Vertex credential is not refused here: the stage
	// check's own rules judge it.
	plain := config.Pool{Name: "plain", Members: []string{"acct-one", "acct-two"}}
	r, _ = poolRun(t, poolSpec{access: "plain", pools: []config.Pool{plain}, creds: poolCreds("acct-one", "acct-two")})
	if got := r.vertexPoolRefusal(ctx); got != "" {
		t.Fatalf("a pool with no Vertex member was refused under enforce: %q", got)
	}
}

// TestJobEnvHoldsOnlyItsCraftsSecrets is F20 and D1635: a job is deployed with
// the secrets of the crafts it runs and no other, so a pool's members never
// arrive together in one environment as one credential's worth of trust. A run
// whose coder goes out on a pool listing two providers builds a route for the
// model its crafts run and reads no other member's key at all — which is the
// same rule from the other side: the pool changes nothing about what the job
// holds, and the member the stage is served by is the one its route names.
func TestJobEnvHoldsOnlyItsCraftsSecrets(t *testing.T) {
	ctx := context.Background()
	pool := config.Pool{Name: "pair", Members: []string{"or-primary", "or-foreign"}}
	// or-foreign's key is deliberately not mounted: a run that read it while
	// building its routes would fail here as an infra error.
	r, _ := poolRun(t, poolSpec{access: "pair", pools: []config.Pool{pool},
		creds: poolCreds("or-primary", "or-foreign"),
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
	member, reason := r.checkStagePool(ctx, "implement")
	if reason != "" {
		t.Fatalf("the stage was refused although its own member serves it: %s", reason)
	}
	if member.Name != routes[0].Name {
		t.Fatalf("the stage is served by %s, and the run's only route is %s", member.Name, routes[0].Name)
	}
}

// TestSubscriptionSpreadReadsThePublishedSwitch is the read the other tests
// stand on: `subscriptionSpread` takes the switch off the policy the run
// published (projectpolicy.Policy.SubscriptionSpread), once per run, and every
// answer that is not a written true — an absent member, a member the owner
// wrote false, no policy and no pool at all — is off. It is the write side that
// puts the member there (`fugaro settings set subscription_spread true`, then
// the publish or `fugaro update`), so what is pinned here is that the runner
// reads exactly what that road writes and nothing else.
//
// The tests above set `r.pools.spread` directly, because the rule they check is
// a consequence of the switch's answer rather than of the read; this one is the
// read itself, and it is the only one that goes through it.
func TestSubscriptionSpreadReadsThePublishedSwitch(t *testing.T) {
	ctx := context.Background()
	on, off := true, false
	pool := config.Pool{Name: "home", Members: []string{"or-primary"}}

	for _, tc := range []struct {
		name  string
		set   *bool
		pools []config.Pool
		want  bool
	}{
		{name: "written true", set: &on, pools: []config.Pool{pool}, want: true},
		{name: "written false", set: &off, pools: []config.Pool{pool}},
		{name: "absent", set: nil, pools: []config.Pool{pool}},
		{name: "no pool at all", set: &on},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := poolRun(t, poolSpec{access: "home", pools: tc.pools,
				creds: poolCreds("or-primary"), providers: poolProviders("or-primary"), env: poolEnv("or-primary")})
			// poolRun stamps the switch as already read (the tests above set
			// the fact); this test is the read, so it is un-stamped here.
			r.projPolicy.policy.SubscriptionSpread = tc.set
			r.pools.read, r.pools.spread = false, false

			if got := r.subscriptionSpread(ctx); got != tc.want {
				t.Fatalf("subscriptionSpread = %v, want %v", got, tc.want)
			}
			if !r.pools.read {
				t.Error("the switch was not recorded as read, so every stage would ask the policy again")
			}
			// Read once per run: the node cannot change under a run, and the
			// answer decides whether a pool serves anything at all.
			r.projPolicy.policy.SubscriptionSpread = &off
			if got := r.subscriptionSpread(ctx); got != tc.want {
				t.Errorf("the second read = %v, want the first answer (%v)", got, tc.want)
			}
		})
	}
}
