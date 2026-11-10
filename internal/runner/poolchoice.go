package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The run's ordered failover over a pool (docs/model-crafts/spec.md §2,
// reconciliation F5, F10, F20, F23, D1622, D1626, D1627, D1635, D1637; plan
// task MC5). It is accountchoice.go's shape asked of a pool instead of the
// owner's accounts: a stage whose craft's access names a pool walks that
// pool's members in order, skipping the ones this repository may not be served
// by, and a stage-start check refuses the stage the walk cannot serve.
//
// Four properties are the whole of the design, and each is a way this code
// could go wrong:
//
//   - **A member that does not admit the repository is skipped, not fatal.**
//     Every credential serves a provider that receives the repository's code,
//     so the member's own allow_data_to bounds it (F10), and D1627 says a
//     non-admitting member is skipped and recorded while a pool with no
//     admitting member refuses the stage. A member skipped for it is a member
//     the walk steps over, which is what keeps one narrow member from blocking
//     a shared pool — and the reverse reading (refuse the pool) is the one
//     that loses a run a credential the owner did admit it.
//   - **The subscription switch is the owner's and off unless he turns it
//     on.** A pool of two or more Claude OAuth subscriptions serves nothing
//     while the switch is off (D1622), and inside a pool that also holds
//     another kind each subscription member is skipped while it is off, so a
//     single subscription with an API-key fallback is the shape that works
//     (D1626). Both are read here and neither is turned on here.
//   - **Only the members this job holds are candidates.** Cross-secret
//     failover happens at a stage boundary with the next member's secret
//     re-fetched, never by mounting a pool in one job (F20, D1635): the coding
//     agent reads its own environment (SECURITY.md), so a job that carried
//     every member's secret would hand it every credential the pool lists. A
//     member this container does not hold is therefore a refusal and never a
//     silent fallback, exactly as a chosen account is in accountchoice.go —
//     and for the same reason: the record would name a credential that did not
//     answer the stage.
//   - **Nothing inside an attempt moves the walk.** A stage's transcript, its
//     cost and its verdict belong to one member, so only the answers that end
//     an attempt advance it (poolEndedTheAttempt, and the credential service's
//     typed answer for the path this one does not serve), and the attempt that
//     follows is the next member's.
//
// Two halves of the spec are deliberately not here. Which member serves which
// credential type is D1634's: the credentials service serves Anthropic-key and
// OAuth members (MC6) and this walk serves the runner path's, so a member of a
// type this walk cannot serve is not held by this job and the stage is refused
// naming it. And what the walk served is recorded in the log as it is chosen;
// the record's own craft_credentials field is MC8's, and the account the stage
// ran as keeps the field it has (recordStageAccount).

// poolChoice is one run's walk: the switch as this run read it, the crafts it
// resolved, and what each stage has already been through. It is written on the
// stage's own goroutine (stageOnce's hook and notePoolExhausted), the way
// accountChoice is.
type poolChoice struct {
	// read is whether the subscription switch has been read, and spread what
	// it said. It is read at most once per run: the node cannot change under
	// it, and the answer decides whether a pool serves anything at all.
	read   bool
	spread bool
	// crafts is the resolver's answer for this run's four crafts, computed
	// once. An access is resolved from the run's own file and the owner's
	// published node, and neither changes under a run.
	crafts map[string]craft.Effective
	// failovers counts, per stage, how many of that stage's attempts have
	// ended on the exhaustion of the member serving them: it is the index the
	// walk stands on, so the attempt that follows starts on the next member.
	failovers map[string]int
}

// poolVertexAuth is agent.auth's Vertex spelling, the way checkBudget and
// config's own auth vocabulary write it. It is not config.DefaultAgentAuth:
// that key is the *default* a file that names no auth gets, and D1624 keeps
// "the default credential" a separate thing from the built-in vertex, so a
// reader that means "this run's calls are the project's own GCP service" says
// vertex and not "whatever the default is today".
const poolVertexAuth = "vertex"

// The two reasons a member is skipped rather than walked over silently. They
// are sentences because they are logged and, for the second, refused.
const (
	// poolSkipNotAdmitted is F10's and D1627's skip: the member serves a
	// provider the repository is not admitted to, so its key is not in this
	// job and the repository's code is never given to it.
	poolSkipNotAdmitted = "the repository is not admitted to this member, so its secret is not in this job (allow_data_to)"
	// poolSkipSubscription is D1626's inert member: the switch that spreads a
	// run across subscriptions is off, so a subscription inside a pool that
	// also holds another kind is stepped over.
	poolSkipSubscription = "the project's subscription_spread switch is off, so this subscription is not spread"
)

// poolSkip is one member the walk stepped over and why, so the line the run
// writes when it chooses a member says what it passed.
type poolSkip struct {
	Member config.PoolMember
	Why    string
}

// subscriptionSpread is the project's subscription_spread switch (D1622,
// D1636), the successor of the ordered failover's own switch: whether a pool
// may spread a run across two or more Claude OAuth subscriptions. It is off
// until the owner turns it on, and only consequences follow from it: with it
// off a pool of two or more subscriptions serves nothing, and inside a mixed
// pool each subscription member is stepped over (D1626).
//
// It is read the way accountAuto reads the switch it succeeds — off the
// owner's published node, once per run, failing closed — and for the same
// reason: the value decides which credential serves a stage, so an environment
// value of a shared job is not where it may come from, and "may this run spend
// a subscription" is answered "no" by a member that is absent, a member that
// is not a JSON true, a node this run cannot read and a value that is neither
// true nor false. projectpolicy's decoder does not carry the member yet, so it
// is read off the node's document, which tolerates it (P8) exactly as
// claude_account_auto is; the write that publishes it is not this task's, and
// a node that never carries it is every installation today.
func (r *run) subscriptionSpread(ctx context.Context) bool {
	c := &r.pools
	if c.read {
		return c.spread
	}
	c.read = true
	if _, found, err := r.projectPolicy(ctx); err != nil || !found {
		return false
	}
	db, err := r.budgetClient(ctx)
	if err != nil || db == nil {
		return false
	}
	var node map[string]json.RawMessage
	if _, err := db.Get(ctx, projectpolicy.PathShared(), &node); err != nil {
		r.d.Log.Warn("policy: the subscription switch could not be read; a pool of subscriptions serves nothing for this run", "node", projectpolicy.PathShared())
		return false
	}
	raw, ok := node["subscription_spread"]
	if !ok {
		return false
	}
	var on bool
	if err := json.Unmarshal(raw, &on); err != nil {
		// A value that is neither true nor false is a person's mistake, and
		// it is never read as "on". The value itself is not echoed, for the
		// reason every message about a node's content here gives.
		r.d.Log.Warn("policy: the subscription switch is not true or false; a pool of subscriptions serves nothing for this run", "node", projectpolicy.PathShared())
		return false
	}
	c.spread = on
	return c.spread
}

// craftLayers are the layers the resolver reads for this run, narrowest first:
// the repository's own file — the agent block the run resolved, which is where
// a checkout's crafts and the project layer file's defaults both arrive — and
// the owner's published node, which is where the credentials and the pools
// are.
//
// The node is the project layer here and the installation's own config is not
// among the layers, because a run has no installation config: the job carries
// what the owner published, which is the same answer `fugaro craft ls` reads
// on the machine that has both (internal/cli's craftLsLayers). Credentials and
// pools are read from it alone, and a repository's file can hold neither
// (config.Config has no such key), which is D1628's direction of tightness
// holding by construction in a run rather than by a check here.
//
// A craft's MODEL is not resolved here: this walk asks which credential or
// pool a stage's calls go out on, and the model a stage runs is still the
// ladder's (r.stageModel). The node's crafts carry a model half too, and
// moving the run onto it is the migration MC4 ships, not this.
func craftLayers(cfg *config.Config, p *projectpolicy.Policy, found bool) []craft.Layer {
	layers := []craft.Layer{{Name: craft.SourceRepo, Crafts: cfg.Agent.Crafts}}
	if !found || p == nil {
		return layers
	}
	l := craft.Layer{Name: craft.SourceProject, KeyPrefix: craftNodeKeyPrefix,
		Credentials: p.CraftCredentials, Pools: p.CraftPools}
	if p.Crafts != nil {
		l.Crafts = *p.Crafts
	}
	return append(layers, l)
}

// craftNodeKeyPrefix is where the policy node writes the craft keys, which is
// the path a problem about an access is reported at (craft.Layer.KeyPrefix).
const craftNodeKeyPrefix = "crafts"

// stageCrafts is the resolver's answer for this run's crafts, computed once. A
// node this run cannot read is no crafts at all, and every craft then answers
// the project's default credential — which is what a run does today, and the
// reason the walk is dormant in an installation that publishes none.
func (r *run) stageCrafts(ctx context.Context) map[string]craft.Effective {
	c := &r.pools
	if c.crafts != nil {
		return c.crafts
	}
	p, found, _ := r.projectPolicy(ctx)
	eff, _ := craft.Resolve(craftLayers(r.cfg, p, found), r.subscriptionSpread(ctx))
	c.crafts = eff
	return eff
}

// stagePool is the pool the stage's craft's calls go out on: the pool and the
// credentials block of the document that defines it (config.PoolMembers needs
// it to tell a member's type), and whether the stage's craft names a pool at
// all. A craft whose access names a credential, or names nothing any layer
// holds, is not this walk's business and answers false.
func (r *run) stagePool(ctx context.Context, stage string) (config.Pool, []config.Credential, bool) {
	name := stageCraft(stage)
	if name == "" {
		return config.Pool{}, nil, false
	}
	e, ok := r.stageCrafts(ctx)[name]
	if !ok {
		return config.Pool{}, nil, false
	}
	p, found, _ := r.projectPolicy(ctx)
	if !found || p == nil {
		return config.Pool{}, nil, false
	}
	for _, pool := range p.CraftPools {
		if pool.Name == e.Access {
			return pool, p.CraftCredentials, true
		}
	}
	return config.Pool{}, nil, false
}

// poolMemberAdmits is F10's rule for one member: whether the repository may be
// served by it at all. admittedAccounts is this stage's own walk over the
// owner's accounts (stageAccountCandidates), which is the same list — the
// accounts whose own allow_repos admits this repository — asked of a pool
// member rather than of the walk itself, so the two cannot disagree about
// which accounts this repository may use.
func (r *run) poolMemberAdmits(m config.PoolMember, admittedAccounts []string) (bool, string) {
	switch m.Type {
	case config.CredentialTypeClaudeOAuth:
		if !slices.Contains(admittedAccounts, selectedName(m.Name)) {
			return false, poolSkipNotAdmitted
		}
		return true, ""
	case config.CredentialTypeOpenRouter:
		// The job's providers are the ones the repository is admitted to: the
		// environment carries the owner's providers narrowed to the
		// repositories each named in its own allow_data_to
		// (runner.ProvidersForRepo), so a member whose provider is absent is a
		// member this repository may not be given, and one whose provider is
		// here has already been admitted.
		if _, ok := r.modelProviders()[m.Name]; !ok {
			return false, poolSkipNotAdmitted
		}
		return true, ""
	case config.CredentialTypeVertex:
		// The built-in credential is the project's own GCP service (D1624):
		// there is no provider to be admitted to and no secret to hand over.
		return true, ""
	}
	// A member of a type this walk cannot place is not admitted, and the
	// path that would judge it is the one that serves it: an Anthropic key is
	// the credentials service's (D1634, MC6), whose own admission rule is
	// that service's.
	return false, "no path this run serves admits this member's type"
}

// poolMemberHeld reports whether this job can serve the stage's calls with
// this member: whether the credential the member names is one this container
// holds.
//
// A job holds the one credential it was deployed with and no other (F20,
// D1635): the runner path's member is a provider whose key is mounted in the
// job's environment, and the built-in vertex is the project's own service
// account, which a run authenticating as vertex holds and no other run does.
// The credentials service's members — the OAuth subscription and the Anthropic
// key — are deliberately not held here: that path selects among them itself
// (D1634, MC6), and a runner that accepted one would be a second design for
// the same choice.
func (r *run) poolMemberHeld(m config.PoolMember) bool {
	switch m.Type {
	case config.CredentialTypeOpenRouter:
		p, ok := r.modelProviders()[m.Name]
		if !ok {
			return false
		}
		// The key's presence is the question, and ProviderCredential is what
		// answers it for a route (providerkey.go): a member whose key is not
		// mounted is not a member this job can serve.
		_, _, err := ProviderCredential(r.d.Env, p)
		return err == nil
	case config.CredentialTypeVertex:
		return r.cfg != nil && r.cfg.Agent.Auth == poolVertexAuth
	}
	return false
}

// poolCandidates is the ordered list this stage's walk stands on: the pool's
// members as it lists them (D1632's order is the order), with the ones this
// stage may not be served by stepped over and returned with the reason they
// were. An error is a rule this run could not read, which the caller refuses
// the stage on rather than walking a list it could not compute.
func (r *run) poolCandidates(ctx context.Context, stage string) ([]config.PoolMember, []poolSkip, error) {
	pool, creds, ok := r.stagePool(ctx, stage)
	if !ok {
		return nil, nil, nil
	}
	admitted, err := r.stageAccountCandidates(ctx)
	if err != nil {
		return nil, nil, err
	}
	spread := r.subscriptionSpread(ctx)
	var out []config.PoolMember
	var skipped []poolSkip
	for _, m := range config.PoolMembers(pool, creds) {
		// The subscription member of a pool that is not all subscriptions
		// (D1622's wholly-inert pool is refused before this is asked): while
		// the switch is off the owner has not agreed to spread a run across
		// subscriptions, and the member is stepped over rather than used.
		if m.Type == config.CredentialTypeClaudeOAuth && !spread {
			skipped = append(skipped, poolSkip{m, poolSkipSubscription})
			continue
		}
		if admits, why := r.poolMemberAdmits(m, admitted); !admits {
			skipped = append(skipped, poolSkip{m, why})
			continue
		}
		out = append(out, m)
	}
	return out, skipped, nil
}

// vertexPoolRefusal is D1626's other half, asked once per run before any
// stage: a pool one of the run's crafts goes out on that lists the built-in
// Vertex credential cannot be capped by the gateway — those calls are the
// project's own GCP service and carry no key to charge against — so under
// budget.mode: enforce the pool is refused outright, in VertexBudgetRefusal's
// own words plus the reason (VertexPoolRefusal). "" is a run whose crafts name
// no such pool, which is every run that names no pool at all.
func (r *run) vertexPoolRefusal(ctx context.Context) string {
	for _, stage := range config.RunStages {
		pool, creds, ok := r.stagePool(ctx, stage)
		if !ok {
			continue
		}
		if member := config.PoolHasVertexMember(pool, creds); member != "" {
			return VertexPoolRefusal(pool.Name, member)
		}
	}
	return ""
}

// checkPoolModels is F23's rule at config time (D1637): a pool the run's
// crafts go out on is admitted only if every member serves the model ID as
// written, and each member that does not is reported by name. It is judged for
// every stage the run may open, because the model is the stage's (stageModel)
// and two stages of one craft can run different ones — level 1's review runs
// the coder's role slot while it spends the review craft's access — so a pool
// admitted for implement and refused for review_first is a run that would
// spend its first stages and then stop.
//
// A pool no member serves and a member no path places are not reported here:
// those are config.ValidatePools' and the serving-path readers' own subject,
// and this check exists to answer F23's question about the members that are
// served.
func (r *run) checkPoolModels(ctx context.Context) error {
	judged := map[string]bool{}
	var ps []config.Problem
	for _, stage := range config.RunStages {
		name := stageCraft(stage)
		if name == "" {
			continue
		}
		pool, creds, ok := r.stagePool(ctx, stage)
		if !ok {
			continue
		}
		model := r.stageModel(stage)
		if key := pool.Name + "\x00" + model; judged[key] {
			continue
		} else {
			judged[key] = true
		}
		ps = append(ps, config.PoolModelProblems(pool, creds, model, r.modelProviders(), policy.CraftAccessKey(name))...)
	}
	if len(ps) == 0 {
		return nil
	}
	return refusal("a pool the run's crafts name is not admitted", ps)
}

// checkStagePool is the walk's stage-start decision, and it returns the
// refusal for a stage this run may not run. "" is every stage this run may:
// the craft's access names no pool, the pool serves nothing yet and the run
// holds the credential the walk stands on.
func (r *run) checkStagePool(ctx context.Context, stage string) string {
	pool, creds, ok := r.stagePool(ctx, stage)
	if !ok {
		return ""
	}
	if e := r.stageCrafts(ctx)[stageCraft(stage)]; e.Inert {
		return fmt.Sprintf("the stage's %s craft goes out on the pool %q, and that pool serves nothing yet: it lists %d Claude OAuth subscription credentials, and Fugaro spreads a run across subscriptions only when the project's subscription_spread switch is on (it is off). Turn the switch on, or list one subscription with another kind of credential beside it, which needs no switch",
			stageCraft(stage), pool.Name, oauthMembers(pool, creds))
	}
	candidates, skipped, err := r.poolCandidates(ctx, stage)
	if err != nil {
		return err.Error()
	}
	log := r.d.Log.With("stage", stage, "pool", pool.Name)
	for _, s := range skipped {
		log.Info("pool: a member this stage may not be served by was skipped",
			"member", s.Member.Name, "type", s.Member.Type, "why", s.Why)
	}
	if len(candidates) == 0 {
		return fmt.Sprintf("the pool %q lists %s and this repository is admitted to none of them: every member's own allow_data_to — or, for a Claude account, its allow_repos — leaves this repository out, and a member is skipped rather than used. List a member this repository may be served by, or name a credential the stage can go out on directly",
			pool.Name, quotes(memberNames(pool, creds)))
	}
	at := r.pools.failovers[stage]
	if at >= len(candidates) {
		return fmt.Sprintf("every member of the pool %q has already refused this stage: the run stood on %d of them (%s), and the pool holds no further one",
			pool.Name, len(candidates), strings.Join(candidateNames(candidates), ", "))
	}
	m := candidates[at]
	if at > 0 {
		log.Info("pool failover: the stage's next attempt is the pool's next member",
			"from", candidates[at-1].Name, "to", m.Name)
	}
	// A member this container does not hold ends the run named, and the two
	// halves of why are the design's: a job mounts one credential before it
	// starts (F20), and the credential a pool's next member names is fetched
	// at a stage boundary by the launcher or the credentials service and
	// never mid-stage (D1635). No file this task owns fetches one, so a
	// failover past the member the job holds stops here rather than running a
	// stage as a credential its own job does not hold.
	if !r.poolMemberHeld(m) {
		return fmt.Sprintf("the pool %q names %s, a %s credential, as the next member this stage would be served by, and this job does not hold it: a run is deployed with the one credential it authenticates with — a provider's key in its environment, or, for vertex, the project's own service account — and a pool's next member's secret is fetched at a stage boundary by the launcher or the credentials service, which this release does not do for its own path. The run stops rather than run the stage as a credential its own job does not hold: the record would name a credential that did not answer it",
			pool.Name, m.Name, m.Type)
	}
	return ""
}

// oauthMembers is how many of the pool's members are Claude OAuth
// subscriptions, which is what makes a pool inert (D1622).
func oauthMembers(pool config.Pool, creds []config.Credential) int {
	n := 0
	for _, m := range config.PoolMembers(pool, creds) {
		if m.Type == config.CredentialTypeClaudeOAuth {
			n++
		}
	}
	return n
}

// memberNames are the pool's members' names, in its order.
func memberNames(pool config.Pool, creds []config.Credential) []string {
	ms := config.PoolMembers(pool, creds)
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

// candidateNames are the names of the members the walk may stand on, in order.
func candidateNames(ms []config.PoolMember) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

// quotes renders names as a comma-separated list, each in quotes. It is
// strings.Join over the printed names rather than a second vocabulary: what a
// message about a pool has to say is the names the owner wrote.
func quotes(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(out, ", ")
}

// notePoolExhausted records that the attempt of stage that has just ended was
// ended by the exhaustion of the pool member serving it, so the stage's next
// attempt is the walk's next member. It is the only thing that advances the
// walk, and it runs where the answer is turned into the stage's end
// (stageOnce): nothing inside an attempt can move a stage to another member.
func (r *run) notePoolExhausted(stage string) {
	c := &r.pools
	if c.failovers == nil {
		c.failovers = map[string]int{}
	}
	c.failovers[stage]++
}

// poolLimitedError is the ending of an attempt the pool member serving it
// refused as spent (F5). It is not a failure: it ends the attempt the way the
// account's own allowance ends one (accountLimitedError), so the stage's next
// attempt is the walk's next member. Its text is never recorded or shown --
// what a run may serve leaves nothing behind but the member that served the
// attempt and the member that takes the next one, each written where it is
// chosen.
type poolLimitedError struct{}

func (e *poolLimitedError) Error() string {
	return "the pool member's own exhaustion ended the attempt"
}

// poolEndedTheAttempt reports whether the attempt that just ended did so
// because the pool member serving it had nothing left rather than because of
// anything about the work. It is the walk's whole classifier, and it is
// deliberately closed: a result event with the error flag, and the text one of
// that member's *own type's* signals (exhaustionSignals, gateway.go) carries.
//
// The type is the member's and not the run's credential kind, which is what F5
// is about: a spent subscription's wording is not an API key's signal, and
// reading it for one would move a run off a member that was answering. A
// successful stage is nothing for the walk to move, whatever its text says.
func (r *run) poolEndedTheAttempt(ctx context.Context, stage string, res agent.Result) bool {
	if !res.IsError {
		return false
	}
	candidates, _, err := r.poolCandidates(ctx, stage)
	if err != nil {
		return false
	}
	at := r.pools.failovers[stage]
	if at >= len(candidates) {
		return false
	}
	return credentialExhaustedText(candidates[at].Type, res.Text)
}
