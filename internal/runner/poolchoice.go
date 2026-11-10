package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The run's pools (docs/model-crafts/spec.md §2, reconciliation F5, F8, F10,
// F13, F20, F23, D1622, D1624, D1626, D1627, D1634, D1637; plan task MC5): the
// craft a stage's calls go out on, the credential or pool its access names, and
// whether this run may serve the stage with it at all.
//
// **What this path can do with a pool, and what it cannot.** It SELECTS: the
// pool's rules decide whether the stage runs and which of its members the
// stage's calls go out on, and a stage this path cannot serve is refused before
// it opens, naming the pool, its members and the reason. It cannot FAIL OVER
// from one member to the next, and the reason is the configuration rather than
// this file: a stage's calls go to the one route that claims its model
// (gateway.routeFor), a run's routes are built once when its gateway starts,
// and a model belongs to one provider — config refuses two providers claiming
// one model (providerOverlapProblems), the gateway refuses two routes claiming
// one model (validateRoutes). So at most one member of a pool this path serves
// can be sent the stage's model, and the pool's own rule (F23, D1637: every
// member serves the model ID as written) is what makes that true of the pools
// this path admits. Failover across members belongs to the credentials service,
// which holds the tier order over OAuth and API-key members and one credential
// sticky per run (D1634, MC6), and to a run whose launcher fetched the next
// member's secret and built it a route — machinery no release has yet.
//
// Three further properties are the rest of the design, and each is a way this
// code could go wrong:
//
//   - **A member that does not admit the repository is skipped, not fatal.**
//     Every credential serves a provider that receives the repository's code,
//     so the member's own admission bounds it (F10), and D1627 says a
//     non-admitting member is skipped and recorded while a pool with no
//     admitting member refuses the stage.
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
//
// Which member serves which credential type is D1634's: the credentials service
// serves Anthropic-key and OAuth members (MC6) and this path serves the ones
// whose key the job holds — a provider's, and the built-in vertex in a run that
// authenticates as it. A member of the other path, or of a type neither path
// serves, is not held here and is refused by name.
//
// What the run resolved is recorded in the log as each stage chooses it; the
// record's own craft_credentials field is MC8's, and the account a stage ran as
// keeps the field it has (recordStageAccount).

// poolChoice is one run's pool state: the switch as this run read it and the
// crafts it resolved. It is written on the stage's own goroutine (stageOnce's
// hook), the way accountChoice is.
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
}

// poolVertexAuth is agent.auth's Vertex spelling, the way checkBudget and
// config's own auth vocabulary write it. It is not config.DefaultAgentAuth:
// that key is the *default* a file that names no auth gets, and D1624 keeps
// "the default credential" a separate thing from the built-in vertex, so a
// reader that means "this run's calls are the project's own GCP service" says
// vertex and not "whatever the default is today".
const poolVertexAuth = "vertex"

// The two reasons a member is skipped rather than refused. They are sentences
// because they are logged and, the first of them, refused with.
const (
	// poolSkipNotAdmitted is F10's and D1627's skip: the member serves a
	// provider the repository is not admitted to, so its key is not in this
	// job and the repository's code is never given to it. It names both lists
	// because a member is a credential of either kind and the rule is one
	// rule: a provider's allow_data_to and a Claude account's allow_repos are
	// the same "which repositories may this credential be given".
	poolSkipNotAdmitted = "this repository is not one the member may be given this repository's code for (allow_data_to for a provider, allow_repos for a Claude account)"
	// poolSkipSubscription is D1626's inert member: the switch that spreads a
	// run across subscriptions is off, so a subscription inside a pool that
	// also holds another kind is stepped over.
	poolSkipSubscription = "the project's subscription_spread switch is off, so this subscription is not spread"
)

// poolSkip is one member this path cannot serve the stage with and why, so the
// line the run writes says what the pool held and what became of each member.
type poolSkip struct {
	Member config.PoolMember
	Why    string
}

// subscriptionSpread is the project's subscription_spread switch (D1622,
// D1636), the successor of the account walk's own switch: whether a pool may
// spread a run across two or more Claude OAuth subscriptions. It is off until
// the owner turns it on, and only consequences follow from it: with it off a
// pool of two or more subscriptions serves nothing, and inside a mixed pool
// each subscription member is stepped over (D1626).
//
// It is read the way accountAuto reads the switch it succeeds — off the owner's
// published node, once per run, failing closed — and for the same reason: the
// value decides which credential serves a stage, so an environment value of a
// shared job is not where it may come from, and "may this run spend a
// subscription" is answered "no" by a member that is absent, a member that is
// not a JSON true, a node this run cannot read and a value that is neither true
// nor false.
//
// The member is read as the typed policy's own (projectpolicy.Policy.
// SubscriptionSpread) rather than off the node's document, which is where the
// write that publishes it puts it: `fugaro settings set subscription_spread
// true` is the owner's act, and the publish copies the local config's value
// into the node. A node that carries it is therefore a node the owner's own CLI
// wrote, and one whose value the policy reader has already judged — a member
// that is not a boolean never reaches here, because the publish and the decode
// both refuse it.
func (r *run) subscriptionSpread(ctx context.Context) bool {
	c := &r.pools
	if c.read {
		return c.spread
	}
	c.read = true
	p, found, err := r.projectPolicy(ctx)
	// A project that publishes no pool publishes nothing for the switch to
	// spread: the member is not read at all, which is every installation today
	// and the reason a run that names no pool pays no cloud call for this.
	if err != nil || !found || p == nil || len(p.CraftPools) == 0 || p.SubscriptionSpread == nil {
		return false
	}
	c.spread = *p.SubscriptionSpread
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
// reason a pool changes nothing in an installation that publishes none.
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
		if !slices.Contains(admittedAccounts, m.Name) {
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
	// A member of a type this path cannot place is not admitted, and the path
	// that would judge it is the one that serves it: an Anthropic key is the
	// credentials service's (D1634, MC6), whose own admission rule is that
	// service's.
	return false, "no path this run serves admits this member's type"
}

// poolMemberHeld reports whether this job can serve the stage's calls with
// this member: whether the credential the member names is one this container
// holds.
//
// A job holds the one credential it was deployed with and no other (F20,
// D1635): this path's member is a provider whose key is mounted in the job's
// environment, and the built-in vertex is the project's own service account,
// which a run authenticating as vertex holds and no other run does. The
// credentials service's members — the OAuth subscription and the Anthropic
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

// poolMember is the pool's member this stage's calls go out on: the member
// this job holds, that this repository's admission covers, and that serves the
// stage's model as written (F23). The members that fail those rules are
// returned with the reason they did, which is what the refusal and the run's
// own log are made of.
//
// There is at most one such member, and the rules above are why: a pool this
// path admits has every member serving the stage's model as written (F23), and
// a model belongs to one provider (config.providerOverlapProblems) — so one
// member at most, in a run whose providers are the ones its own launcher
// mounted. A second one could only come from a configuration that let two
// providers serve one model, which is the configuration a failover over this
// path would need and which nothing in this release can write.
func (r *run) poolMember(ctx context.Context, stage string, pool config.Pool, creds []config.Credential) (config.PoolMember, []poolSkip, error) {
	spread := r.subscriptionSpread(ctx)
	var served []config.PoolMember
	var skipped []poolSkip
	// The accounts a pool's subscription members are judged against are read
	// only when the pool actually lists one whose admission this path asks
	// about: a pool of this path's own kinds is answered without the account
	// walk, and the list is read once however many members ask for it.
	var admittedAccounts []string
	accountsRead := false
	for _, m := range config.PoolMembers(pool, creds) {
		// The subscription member of a pool that is not all subscriptions
		// (D1622's wholly-inert pool is refused before this is asked): while
		// the switch is off the owner has not agreed to spread a run across
		// subscriptions, and the member is stepped over rather than used.
		if m.Type == config.CredentialTypeClaudeOAuth {
			if !spread {
				skipped = append(skipped, poolSkip{m, poolSkipSubscription})
				continue
			}
			if !accountsRead {
				accountsRead = true
				var err error
				if admittedAccounts, err = r.stageAccountCandidates(ctx); err != nil {
					return config.PoolMember{}, nil, err
				}
			}
		}
		if admits, why := r.poolMemberAdmits(m, admittedAccounts); !admits {
			skipped = append(skipped, poolSkip{m, why})
			continue
		}
		if !r.poolMemberHeld(m) {
			skipped = append(skipped, poolSkip{m, poolMemberNotHeld})
			continue
		}
		if !config.PoolMemberServesModel(m, r.stageModel(stage), r.modelProviders()) {
			skipped = append(skipped, poolSkip{m, poolMemberServesNothing(r.stageModel(stage))})
			continue
		}
		served = append(served, m)
	}
	if len(served) == 0 {
		return config.PoolMember{}, skipped, nil
	}
	return served[0], skipped, nil
}

// poolMemberNotHeld is why a member this job does not hold cannot serve a
// stage (F20, D1635), asked of a member one at a time rather than of the
// whole pool: a pool that holds one member this job serves and one it does
// not is refused for the second, and the line says which.
const poolMemberNotHeld = "this job does not hold the member's credential: a run is deployed with the credential it authenticates with — a provider's key in its environment, or, for vertex, the project's own service account — and a pool's next member's secret is fetched at a stage boundary by the launcher or the credentials service, which this release does not do for this path"

// poolMemberServesNothing is why a member cannot be sent the stage's model
// (F23, D1637), as one clause of the pool's refusal.
func poolMemberServesNothing(model string) string {
	return fmt.Sprintf("it does not serve %s as written, and a pool is admitted only if every member serves the model the stage runs", model)
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
		key := pool.Name + "\x00" + model
		if judged[key] {
			continue
		}
		judged[key] = true
		ps = append(ps, config.PoolModelProblems(pool, creds, model, r.modelProviders(), policy.CraftAccessKey(name))...)
	}
	if len(ps) == 0 {
		return nil
	}
	return refusal("a pool the run's crafts name is not admitted", ps)
}

// checkStagePool is the pool's stage-start decision: the member this stage's
// calls go out on, and the refusal for a stage this run may not run. A zero
// member with no refusal is every stage this run may run without a pool: the
// craft's access names no pool at all.
//
// The member it answers with is the one the run already serves the stage with
// — the provider whose route claims the stage's model, or the project's own
// GCP service in a run authenticating as vertex — and this check is what makes
// the pool's rules decide whether that member may serve at all. Nothing is
// re-pointed here, because nothing can be: see the head of this file.
func (r *run) checkStagePool(ctx context.Context, stage string) (config.PoolMember, string) {
	pool, creds, ok := r.stagePool(ctx, stage)
	if !ok {
		return config.PoolMember{}, ""
	}
	if e := r.stageCrafts(ctx)[stageCraft(stage)]; e.Inert {
		return config.PoolMember{}, fmt.Sprintf("the stage's %s craft goes out on the pool %q, and that pool serves nothing yet: it lists %d Claude OAuth subscription credentials, and Fugaro spreads a run across subscriptions only when the project's subscription_spread switch is on (it is off). Turn it on with `fugaro settings set subscription_spread true` at a terminal, or list one subscription with another kind of credential beside it, which needs no switch",
			stageCraft(stage), pool.Name, oauthMembers(pool, creds))
	}
	member, skipped, err := r.poolMember(ctx, stage, pool, creds)
	if err != nil {
		return config.PoolMember{}, err.Error()
	}
	log := r.d.Log.With("stage", stage, "pool", pool.Name)
	for _, s := range skipped {
		log.Info("pool: a member this stage cannot be served by was skipped",
			"member", s.Member.Name, "type", s.Member.Type, "why", s.Why)
	}
	if member.Name == "" {
		return config.PoolMember{}, fmt.Sprintf("the pool %q lists %s, and this run can be served by none of them: %s",
			pool.Name, strings.Join(memberNames(config.PoolMembers(pool, creds)), ", "), skippedText(skipped))
	}
	log.Info("pool: the stage's calls go out on the pool's member", "member", member.Name, "type", member.Type)
	return member, ""
}

// skippedText is what the pool's members became, one clause each, so a refusal
// says what the owner wrote and why none of it can serve this run: the
// entries are the pool's own order, which is the order the owner reads.
func skippedText(skipped []poolSkip) string {
	if len(skipped) == 0 {
		return "the pool holds no member"
	}
	parts := make([]string, 0, len(skipped))
	for _, s := range skipped {
		parts = append(parts, fmt.Sprintf("%s: %s", s.Member.Name, s.Why))
	}
	return strings.Join(parts, "; ")
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

// memberNames are the given members' names, in the order they came in: a pool's
// order for the pool's members, the run's for the ones it serves.
func memberNames(ms []config.PoolMember) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}
