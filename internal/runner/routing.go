package runner

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// applyRouting rewrites the run's Claude pins through the project's routing
// switch (design docs/design/project-centric.md §6.2, D1a/D1b; plan Task 1b.2).
//
// It is the one place a run applies the switch, and it applies it *once*, to
// the configuration every consumer already reads: after it, a pin the switch
// opted in names the route's own model ID (anthropic/claude-sonnet-5.5) rather
// than the canonical one (claude-sonnet-5-5), so the pins the agent is given,
// the route the gateway registers, the row the price lookup charges and the
// serving_model/priced_as a call is recorded with all name the same model —
// Review Focus 11's mutation is a second translation that disagrees with this
// one.
//
// Resolving into the run's own config, rather than at each consumer, is what
// makes the whole run agree. Every rule that judges a pin — the allow-list, the
// provider's allow_data_to, the pin's price — reads the same Agent, and a
// consumer that resolved on its own would leave those rules judging the
// canonical ID while the gateway charged the route's: a routed call under an
// allow-list that names no route would be allowed by a decision about a model
// nobody sent.
//
// The resolution itself is config.RoutingClaudeFor, the one resolver, and the
// ID it returns comes from the checked-in canonical map (internal/pricing's
// claudeRoutes, unexported and reachable only through RouteIDFor): the run
// never derives a spelling, so a route entry that spells the model its own way
// is refused rather than trusted.
//
// A switch that names a pin which cannot be resolved — an ID with no map row,
// no route entry claiming it, a route whose allow_data_to is not written, or two
// claiming it — halts the run, naming the model and the reason. It never falls
// back to the direct route: D1b's default is what an *absent* switch means,
// never what a written one means when its route is gone, and the fall-through
// would send the call to Anthropic and charge the direct Claude rate for a call
// the owner routed elsewhere.
//
// Whether the switch reaches this run at all is RoutingInertReason, one
// decision shared with the readers that have to say what a run does without
// running one (`fugaro validate`, `fugaro doctor`): a second reading of the
// ladder is how validate came to report a halt no run makes — the gateway-off
// one, and then via: auto under a Vertex credential — and a resolution no run
// takes.
//
// A `cascade` run — and, through the same arm, a review class run, whose model
// path is the service's and whose class is spelled cascade (modelAuth) — is
// judged here for more things, all of them facts about the credentials service
// rather than about routing, and all refused in bootstrap before anything is
// opened: the class's own two refusals (a repository whose own auth is a
// credential the variant does not mount, and a budget mode that leaves the
// class no gateway), the switch the operator publishes (O2, which the class
// does not need) and the model allow-list that service holds every call it
// carries to (the owner's ruling of 2026-10-22, C1.11, which the class is held
// to exactly as a cascade run is). A local run is answered first and separately
// (D149).
func (r *run) applyRouting(pp *projectpolicy.Policy, found bool) error {
	auth, class := "", false
	if r.cfg != nil {
		class = r.reviewClassRun()
		auth = r.modelAuth()
	}
	if class {
		if err := r.checkReviewClassModelPath(); err != nil {
			return err
		}
	}
	if auth == config.AuthCascade {
		if r.d.Local {
			// D149 first, before the switch is judged: `fugaro exec` is
			// unchanged by the cascade, so the O2 refusal below — whose remedy
			// is the operator publishing a policy — is no remedy here. This is
			// the only place a local run's cascade file is answered *as* a
			// local run: the other sentence for it (serviceUpstream, D149
			// too) is behind the gateway, and a local run with the budget off
			// or unset never reaches it (agent.BuildEnv answers instead).
			return cascadeLocalRefusal()
		}
		if !class {
			// O2 (the owner's ruling of 2026-10-17): a repository may ask for
			// the cascade, and only the operator-written project policy enables
			// it. A cascade workflow whose policy does not carry the switch is
			// refused here, in bootstrap, before any model call and before a pin
			// moves, naming exactly what to set —
			// `fugaro validate` refuses the same file on the author's own machine
			// (C1.1's cascadeProblems), and this is the run's own side of it.
			//
			// A review class run skips this arm and only this one
			// (spec.md §2): the switch is the operator's consent for a *coding*
			// run's cascade, while a review job's credential is decided by its
			// own class, so a review needs no `routing.claude: {via: cascade}`
			// to be admitted. The allow-list arm below is not skipped with it:
			// what the switch enables is a credential, and the allow-list is
			// what the service holds every call it carries to.
			var via string
			if found && pp != nil && pp.Routing != nil && pp.Routing.Claude != nil {
				via = pp.Routing.Claude.ViaValue()
			}
			if via != config.RouteCascade {
				return fmt.Errorf("agent.auth is %s and this project's policy does not turn the cascade on: set routing.claude: {via: %s} in the project policy (%s) and publish it (fugaro project policy publish) — a repository asks for the cascade, and only the operator can set it there (O2)", config.AuthCascade, config.RouteCascade, projectpolicy.PathShared())
			}
		}
		// The allow-list the credentials service holds this run's calls to (the
		// owner's ruling of 2026-10-22): the service carries every call of a
		// cascade run and refuses a project that publishes no
		// budget.allowed_models (C1.11), so a run of such a project could make
		// no call at all. It is refused here, in bootstrap, rather than left to
		// halt on its first forwarded call — and judged on the *node's* list,
		// which is the only one the service can read: the job environment's
		// ceiling and the repository's file never reach it (H1). `fugaro
		// validate` refuses the same file (cascadeProblems), the launch refuses
		// it before a job starts at all (cascadeLaunchRefusal), and this is the
		// run's own side of it, for a job that started some other way.
		//
		// Reaching here means the switch is on (a coding run) or the class does
		// not need it (a review), so for a coding run the node was found: the
		// arm above refuses every state without it, including the node that is
		// not there.
		if pp == nil || len(pp.Budget.AllowedModels) == 0 {
			if class {
				return fmt.Errorf("this run is a review and the credentials service carries every call it makes: the project's policy (%s) publishes no budget.allowed_models, and the service refuses a project that publishes none, so this review could make no call at all — add the allow-list to the project policy and publish it (fugaro project policy publish)", projectpolicy.PathShared())
			}
			return fmt.Errorf("agent.auth is %s and this project's policy (%s) publishes no budget.allowed_models: the credentials service, which carries every call of a cascade run, refuses a project that publishes none, so this run could make no call — add the allow-list to the project policy and publish it (fugaro project policy publish)", config.AuthCascade, projectpolicy.PathShared())
		}
	}
	if !found || pp == nil || pp.Routing == nil || pp.Routing.Claude == nil {
		return nil // absent means direct (D1b)
	}
	block, sw := pp.Routing, pp.Routing.Claude
	why := RoutingInertReason(sw, auth, r.gatewayOn(), envLookup(r.d.Env, "ANTHROPIC_API_KEY") != "")
	if why != RoutingActs {
		if why == RoutingInertAutoVertex {
			r.d.Log.Info("routing.claude: via auto does not route a Vertex run: Vertex serves Claude under this project's own credential, so the pins keep Claude's own IDs (a provider route needs agent.auth: api-key)",
				"code", "routing_auto_vertex")
		}
		// Every skip lands here, before CheckRouting, for one reason: a switch
		// this run has nothing to act on does not halt it either.
		return nil
	}
	// The block is judged against the routes this run has, exactly as the
	// config loader and the policy reader judge it: the run must not act on a
	// switch it cannot carry out.
	if ps := config.CheckRouting(block, r.modelProviders(), "routing"); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return fmt.Errorf("the project's routing switch is not usable: %s", strings.Join(msgs, "; "))
	}
	for _, p := range r.pinSlots() {
		model := p.get()
		if model == "" || !sw.Names(model) {
			continue // the list is the opt-in: an ID it does not name is untouched
		}
		route, sent, ok := config.RoutingClaudeFor(sw, model, r.modelProviders())
		if !ok || route == config.RouteDirect || sent == model {
			// CheckRouting passed, so this is the belt to its braces: the
			// switch asked for a route and there is none to take.
			return fmt.Errorf("routing.claude names %s, and this run cannot resolve its route: the switch routes nothing rather than sending the call direct (D1b)", model)
		}
		p.set(sent)
	}
	return nil
}

// checkReviewClassModelPath is the review class's own admission, judged in
// bootstrap beside the credentials service's other facts and before any
// credential of the job's is built or any pin moves. A review class run's model
// calls are the service's review class's (O1, D143), and the class is spelled
// cascade (modelAuth) so that every arm the path needs already exists for it:
// what the class cannot be served under is what this refuses.
//
// The two refusals, and both are fail-closed rather than a fallback to a
// credential of the job's:
//
//   - a repository whose own agent.auth is a credential of its own. The class's
//     one credential is the pairing account's tier-A one (reviewCandidates),
//     which is a Claude OAuth account by construction (ReviewJobAccount), and
//     0.20.0 ships the OAuth review alone: the api-key review — a provider key,
//     OpenRouter or Anthropic — is a later release (§6.7). Both halves are
//     refused: an api-key or vertex repository would need a key CSR7's variant
//     deliberately does not mount, and a pairing naming anything but a Claude
//     account is one pairReview never builds. So a repository's own api-key
//     configuration can never produce an api-key review, and nothing reads as
//     though it can.
//
//   - a budget mode that leaves the class no gateway. The class's calls are the
//     service's, and the service is reached through the gateway — the only
//     thing that holds a credential and attaches it per call — so a run whose
//     mode is off reaches nothing at all. It refuses here rather than falling
//     through to agent.BuildEnv's cascade arm, whose sentence ("agent.auth
//     cascade needs ...", internal/agent/env.go) is written for a coding run
//     and names a fix a review job cannot apply: the repository's file is the
//     review job's own, and the setting that is wrong is the installation's.
//     The class does not force the gateway on: a mode the installation set to
//     off keeps its one meaning (Open item 6's answer of 2026-11-05/06).
func (r *run) checkReviewClassModelPath() error {
	switch r.cfg.Agent.Auth {
	case "oauth", config.AuthCascade:
	default:
		return fmt.Errorf("this run is a review, and its repository's fugaro.yaml writes agent.auth: %s: the independent review's model calls are served by the credentials service's review class from the installation's Claude OAuth account, and the api-key review — a provider key, OpenRouter or Anthropic — is a later release (§6.7 of docs/cloud-senior-review/implementation-plan.md): 0.20.0 ships the OAuth review alone. A review carries no credential of its own, so no key of this repository's can serve it: agent.auth oauth (or cascade) is the OAuth path the class is served from", r.cfg.Agent.Auth)
	}
	if !r.gatewayOn() {
		mode := r.spend.Mode
		if mode == "" {
			mode = policy.ModeOff
		}
		return fmt.Errorf("this run is a review, and this installation's budget mode is %s: the review class's model calls are carried by the gateway, which only budget.mode %s or %s starts, and a review does not run with the installation's budget off — set budget.mode to %s or %s in the project policy and publish it (fugaro project policy publish), and the review reaches the credentials service's review class. The class does not force the gateway on: a mode the installation set to %s keeps its one meaning", mode, policy.ModeObserve, policy.ModeEnforce, policy.ModeObserve, policy.ModeEnforce, policy.ModeOff)
	}
	return nil
}

// GatewayOn reports whether a configuration routes its calls through the
// gateway: the budget is on (observe or enforce; "" and off are off) and the
// credential is one the gateway can carry — api-key or vertex, and the cascade
// too, whose calls the gateway forwards to the credentials service (C1.19).
// An oauth run's own token never goes through it. It is run.gatewayOn, the
// run's own predicate,
// spelled for the readers that hold a mode and an auth rather than a run
// (validate, doctor): the gateway is what decides whether the switch has
// anything to act on at all (design §6.2, D11).
func GatewayOn(auth, mode string) bool {
	switch auth {
	case "api-key", "vertex", config.AuthCascade:
	default:
		return false
	}
	return mode == policy.ModeObserve || mode == policy.ModeEnforce
}

// RoutingInert is why the project's routing switch reaches no run of a
// configuration; the zero value, RoutingActs, means it does. applyRouting
// branches on this answer — it is the one place a run applies the switch — and
// a reader that has to say what a run does without running one (validate,
// doctor) asks it rather than re-deriving the ladder.
type RoutingInert string

const (
	// RoutingActs is no reason: the run applies the switch, so it reaches
	// config.CheckRouting and the per-pin resolution below, and a switch it
	// cannot carry out halts it.
	RoutingActs RoutingInert = ""
	// RoutingInertNoSwitch: nothing is published. An absent switch is D1b's
	// default, which is direct.
	RoutingInertNoSwitch RoutingInert = "no-switch"
	// RoutingInertNoGateway: routing exists only with the gateway (design §6.2,
	// D11; plan Task 1b.2 step 3). The gateway is what routes a call to a
	// provider and prices it, and with it off nothing changes — stagePins
	// returns nil and the run's pins reach the agent as --model, where a
	// route's own ID is a model Anthropic does not serve. A budget-off run
	// therefore keeps the direct spelling, and a switch it cannot resolve does
	// not halt it either: the switch has nothing to act on.
	RoutingInertNoGateway RoutingInert = "no-gateway"
	// RoutingInertViaDirect: the switch is written with via: direct, which
	// routes nothing — a legal spelling (D1b's default said out loud).
	RoutingInertViaDirect RoutingInert = "via-direct"
	// RoutingInertAutoVertex: via: auto routes only when the Claude call has
	// nowhere else to go, and it asks that by the credential the run already
	// has. A Vertex run has one — Vertex serves Claude under the owner's own
	// GCP project, and its jobs carry no Anthropic API key — so auto leaves its
	// pins alone rather than reading the absent key as "no route but the
	// provider" and sending the prompt to a third party the switch never chose
	// for this credential. That is a skip, not a refusal: the same policy
	// serves a project's api-key jobs too, and the owner wrote auto to follow
	// the credential. via: openrouter still routes a Vertex run — and is
	// refused downstream for the reason it always is, that a run does not mix
	// credentials (config.CheckProviderAuth).
	RoutingInertAutoVertex RoutingInert = "auto-vertex"
	// RoutingInertAutoKey: via: auto is key-presence driven, as the design
	// spells it. With an Anthropic key stored in the job the direct route is
	// taken and the pins are left alone (D1b); with none the provider route is
	// taken.
	RoutingInertAutoKey RoutingInert = "auto-key"
	// RoutingInertCascadeOtherAuth: the switch is via: cascade and this run's
	// agent.auth is not cascade. The cascade decides which credential carries
	// a call inside the credentials service (D133, D150) and names no model,
	// so it routes nothing for a run that did not ask for it: an installation
	// switches it on for its cascade workflows alone, and every other auth of
	// the same project keeps the direct route it had.
	RoutingInertCascadeOtherAuth RoutingInert = "cascade-other-auth"
)

// RoutingInertReason is the run's own decision about the switch: whether it
// acts, and which of the states above it is in. applyRouting returns on
// RoutingInertReason != RoutingActs before it reaches config.CheckRouting or
// rewrites a pin, and the readers that cannot run a run ask the same function,
// so all of them answer one question one way.
//
// The facts it takes are the run's own: the credential, whether the gateway is
// on (GatewayOn, and run.gatewayOn for a run), and whether the job has an
// Anthropic API key stored. A reader that is not the run does not know the last
// one — the key is a secret version in the cloud, and neither validate nor
// doctor reads the job's environment — so it asks this twice, with the key
// stored and without, and says which of the two states its answer is about
// rather than picking one.
func RoutingInertReason(sw *config.RoutingClaude, auth string, gatewayOn, anthropicKeyStored bool) RoutingInert {
	if sw == nil {
		return RoutingInertNoSwitch
	}
	if !gatewayOn {
		return RoutingInertNoGateway
	}
	if sw.ViaValue() == config.RouteDirect {
		return RoutingInertViaDirect
	}
	if sw.ViaValue() == config.RouteCascade && auth != config.AuthCascade {
		// The cascade is a credential switch (D133, D150): it names no model
		// and routes no call, so it acts only on the workflow that asked for
		// the cascade. For every other auth it is inert, exactly as via:
		// direct is — nothing changes for a run that did not opt in, which is
		// the whole reason O3 chose a spelling of its own over via: auto.
		return RoutingInertCascadeOtherAuth
	}
	if sw.ViaValue() == config.RouteAuto {
		if auth == "vertex" {
			return RoutingInertAutoVertex
		}
		if anthropicKeyStored {
			return RoutingInertAutoKey
		}
	}
	return RoutingActs
}

// routingSwitch is the routing switch this run carries, or nil: the policy
// node's block when the run read one (projectPolicy, called by resolvePolicy),
// and nothing when there is no node. It reads nothing itself — the node's read
// is memoized and resolvePolicy is the one caller — so a later caller asks
// about the switch that was applied, not about a second read that could have
// answered differently.
func (r *run) routingSwitch() *config.Routing {
	if !r.projPolicy.found || r.projPolicy.policy == nil {
		return nil
	}
	return r.projPolicy.policy.Routing
}

// pinSlot is one of the run's three pins: the model the configuration answers
// for it, and where that answer is written. A pin is set through the field its
// effective model came from, so an agent that names only agent.model keeps its
// coder pin there rather than gaining an explicit one (config.Agent.ModelFor).
type pinSlot struct {
	get func() string
	set func(string)
}

// pinSlots are the run's pins: what the agent is given as the coder, the
// reviewer and the background model — the three of PinnedModels' order, which
// every rule that judges a pin reads — followed by each review level's own
// model, which is a pin of the level's stage and of nothing else (L4.5).
func (r *run) pinSlots() []pinSlot {
	a := &r.cfg.Agent
	// A role reads models.<role>, else agent.model: the rewrite goes back to
	// whichever of the two answered, so the effective model moves and nothing
	// else about the configuration does.
	slot := func(role *string, roleModel config.Role) pinSlot {
		return pinSlot{
			get: func() string { return a.ModelFor(roleModel) },
			set: func(m string) {
				if *role == "" {
					a.Model = m
					return
				}
				*role = m
			},
		}
	}
	bg := pinSlot{get: func() string { return a.Models.Background }, set: func(m string) { a.Models.Background = m }}
	slots := []pinSlot{slot(&a.Models.Coder, config.RoleCoder), slot(&a.Models.Reviewer, config.RoleReviewer), bg}
	for _, s := range reviewStages {
		// A review level's own model is a pin of its own (L4.5): it is the
		// model the level's stage runs, and a switch naming it has to move it
		// exactly as it moves the slot the level sits in.
		block := r.levelBlockFor(s.level)
		slots = append(slots, pinSlot{
			get: func() string {
				if *block == nil {
					return ""
				}
				return (*block).Model
			},
			set: func(m string) { r.setLevelModel(s.level, m) },
		})
	}
	if r.shadow {
		// The champion's coder is pinned nowhere in a challenger arm, but the
		// plan is derived from it (bootstrap): the switch rewrites it as it
		// would have rewritten the champion's own pin, so both arms' plans
		// answer FirstLineOn from the same routed ID.
		slots = append(slots, pinSlot{get: func() string { return r.championCoder }, set: func(m string) { r.championCoder = m }})
	}
	return slots
}

// The review levels' own models (L4.5). A stage pins one of the run's role
// slots — the coder's or the reviewer's — and a review level may name a model
// of its own on top of the slot it sits in (config.ResolveLevels, L3.1):
// level 1 runs in the coder's stage, levels 2 and 3 in the reviewer's. Every
// consumer that has to agree on which model a stage runs reads stageModel,
// because a stage whose pins named one model and whose gateway set named
// another would have every call of it refused as not pinned for the stage.

// stageAdversarial is level 3's stage: the recipe step's own name, which L4.2
// puts in the plan after the review step.
const stageAdversarial = string(recipe.StepAdversarial)

// reviewStage is one review level's stage: the name the runner opens it
// under, the level it is, and the role slot it pins when no layer names it a
// model of its own.
type reviewStage struct {
	stage string
	level policy.Level
	role  config.Role
}

// reviewStages are the three review stages, in the order they run.
var reviewStages = []reviewStage{
	{"review_first", policy.LevelL1, config.RoleCoder},
	{"review", policy.LevelL2, config.RoleReviewer},
	{stageAdversarial, policy.LevelL3, config.RoleReviewer},
}

// reviewStageOf is the review stage named stage.
func reviewStageOf(stage string) (reviewStage, bool) {
	for _, s := range reviewStages {
		if s.stage == stage {
			return s, true
		}
	}
	return reviewStage{}, false
}

// stageRole is the role slot a stage pins: config.StageRole, which now knows
// the adversarial stage too, so level 3 takes the reviewer's slot — its output
// limit and its role's model — through the one ladder rather than a second
// answer beside it (TestStageRoleAgreesWithConfig).
func stageRole(stage string) config.Role {
	return config.StageRole(stage)
}

// stageModel is the model a stage runs: the review level's own model when the
// stage is a review stage a layer named one for, and the role slot's
// otherwise.
func (r *run) stageModel(stage string) string {
	if s, ok := reviewStageOf(stage); ok {
		if m := r.levelModel(s.level); m != "" {
			return m
		}
	}
	return r.cfg.Agent.ModelFor(stageRole(stage))
}

// levelModel is the model the level's own block names, "" for a level no
// layer wrote or one whose block names no model: the level then runs the
// model its role slot answers.
func (r *run) levelModel(level policy.Level) string {
	if b := r.levelBlock(level); b != nil {
		return b.Model
	}
	return ""
}

// levelBlock is the level's block in the run's configuration, nil for a level
// no layer wrote.
func (r *run) levelBlock(level policy.Level) *config.LevelSetting {
	return *r.levelBlockFor(level)
}

// levelBlockFor is where the level's block lives: the merged configuration's
// own agent.levels, allocated on demand. Writing the resolved models there is
// what makes the whole run agree — applyRouting's rule, applied to the levels
// — so every consumer reads one answer rather than resolving again.
func (r *run) levelBlockFor(level policy.Level) **config.LevelSetting {
	lv := &r.cfg.Agent.Levels
	switch level {
	case policy.LevelL1:
		return &lv.L1
	case policy.LevelL2:
		return &lv.L2
	default:
		return &lv.L3
	}
}

// setLevelModel writes the level's model into its own block, allocating the
// block for a level no layer wrote: the resolved model of a level that runs
// is a fact about the run, not a default left to a second resolution.
func (r *run) setLevelModel(level policy.Level, model string) {
	block := r.levelBlockFor(level)
	if *block == nil {
		*block = &config.LevelSetting{}
	}
	(*block).Model = model
}

// resolvedLevel is the level's own answer in a resolved set.
func resolvedLevel(levels config.Levels, level policy.Level) config.ResolvedLevel {
	switch level {
	case policy.LevelL1:
		return levels.L1
	case policy.LevelL2:
		return levels.L2
	default:
		return levels.L3
	}
}

// applyLevels resolves the run's three review levels (config.ResolveLevels,
// L3.1) and writes every level that runs into the run's own configuration, so
// the model a level names is the model its stage pins, the route it is served
// by and the gateway admits for it. It returns the resolution, its warnings
// and the refusal that stops the run; a refused resolution writes nothing,
// since the models of a set of levels the run may not use must not reach a
// stage.
//
// It is the levels' half of the runner: reading the task's review_levels, the
// owner's floor and the recipe is L4.2's, and it calls this with the inputs it
// has. One of them the run answers for itself, and it fills it in when the
// caller leaves it out, because a caller that forgot it would not fail: a nil
// Providers skips the provider-effort refusal (D725), so a forgotten input
// would be a rule silently not applied. The mode is not one of them: the
// resolver's first layer is the run's rigor mode, which is L4.2's to pass.
//
// The allow-list is deliberately *not* one of them. The resolver's own list is
// a plain membership test, while the list that bounds this run is
// config.CheckAllowed's, which admits a route ID where the switch routes the
// model to it and names the key the owner wrote — the run's own pins by their
// paths and a level's own model as agent.levels.lN.model (checkLevels,
// levelModelPath). A list filled in here would refuse a level model before
// checkBudget judged it, and refuse it by a rule that does not know the run's
// routes: the routed level model this run may use would be reported as one it
// may not. It runs in bootstrap, before any stage, so the judgement is made
// either way (checkBudget).
//
// It runs after applyRouting (resolvePolicy), which rewrites the pins of the
// models the switch names: level 1's own answer is read off the coder's model
// (Agent.FirstLineOn), so a resolution taken before the rewrite would turn the
// cheap first-line loop off for exactly the runs a switch routed.
func (r *run) applyLevels(in config.LevelInputs) (config.Levels, []config.Warning, error) {
	in.Config = r.cfg
	if in.Providers == nil {
		in.Providers = r.modelProviders()
	}
	levels, warns, err := config.ResolveLevels(in)
	if err != nil {
		return levels, warns, err
	}
	for _, s := range reviewStages {
		lv := resolvedLevel(levels, s.level)
		if !lv.On || lv.Model == "" {
			// A level that does not run carries no model, and "" is Claude
			// Code's own default rather than an ID to pin.
			continue
		}
		r.setLevelModel(s.level, lv.Model)
	}
	return levels, warns, nil
}
