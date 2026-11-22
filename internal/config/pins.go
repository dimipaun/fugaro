package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// pin is one of an Agent's pins with the config path a Problem about it points
// the owner at, and — for a review level's own model — the level's name, which
// a warning carries in its own sentence as well as in the path.
type pin struct {
	path, model string
	level       string
}

// pinPaths are the config paths of an Agent's four pins, in the order
// Agent.PinnedModels returns them. One place, so `validate`, `diagnose` and
// the run's own checks name the same keys.
var pinPaths = [...]string{"agent.models.coder", "agent.models.reviewer", "agent.models.background", visionPinPath}

// visionPinPath is the config path of the vision slot, the one pin a run may
// leave empty.
const visionPinPath = "agent.models.vision"

// pins pairs each of an Agent's pins with its path, for the loops that judge or
// warn about all of them. The vision slot is left out while it names no model:
// it is the one pin a run may do without (V1, D2007) — no stage runs it until a
// stage's own model cannot read an image, while every other slot has a fallback
// (agent.model) or, for the background model, a rule of its own that requires
// it — and judging the empty entry would refuse every run that never writes the
// key.
func pins(a Agent) []pin {
	var out []pin
	for i, m := range a.PinnedModels() {
		if m == "" && pinPaths[i] == visionPinPath {
			continue
		}
		out = append(out, pin{path: pinPaths[i], model: m})
	}
	return out
}

// levelPins are the review levels' own models (L3.1), one per level that names
// a model of its own, under the key the owner wrote it — which is also the path
// the run's own model checks rename a problem about a level to
// (internal/runner/gateway.go's levelModelPath). A level's own model is a model
// the run charges: its stage runs it and the cap counts it, exactly as it
// counts a role pin.
//
// A level left on the model its role resolves to, and a level whose own model
// is one of the three pins, are that pin: the run resolves every level that
// runs onto a model and writes it into the level's block
// (internal/runner/routing.go's setLevelModel), so a pin of its own for those
// would print the coder's or the reviewer's warning once more for every level
// of every run. This is the rule the run's own model checks already follow
// (checkModels, over modelAgents).
func levelPins(a Agent) []pin {
	pinned := a.PinnedModels()
	var out []pin
	for _, l := range []struct {
		name string
		set  *LevelSetting
	}{{"l1", a.Levels.L1}, {"l2", a.Levels.L2}, {"l3", a.Levels.L3}} {
		if l.set == nil || l.set.Model == "" || slices.Contains(pinned, l.set.Model) {
			continue
		}
		out = append(out, pin{path: "agent.levels." + l.name + ".model", model: l.set.Model, level: l.name})
	}
	return out
}

// prefix names the level a warning is about, "" for a role pin. The warning's
// own sentence is the only place a level is said on the surfaces that show one
// without its path (diagnose, the runner's log line), and a level model is one
// of several pins an owner has to tell apart to fix the right one.
func (r pin) prefix() string {
	if r.level == "" {
		return ""
	}
	return "level " + r.level + ": "
}

// CraftModel is one craft's model as the layers resolved it (craft.ModelFor),
// with the two things the checks below need that the Agent block does not
// carry: the key that wrote the model in the document the reader has, and the
// access the same craft's calls go out on.
//
// It is a parameter of the checks rather than a field of Agent because the
// model a craft runs is not in the file: it is the resolver's answer over the
// repository's file, the installation's config and the project's published
// node (internal/craft, MC2), and a check that read agent.crafts.<craft>.model
// alone would judge one layer's half of it. A run needs none of this — the
// bootstrap write puts each craft's model into the slot the run reads, so the
// pins loops below judge it there (MC12) — and the callers that do are the
// launcher and `fugaro validate`, which judge a configuration before a run
// exists (MC14, D1665).
type CraftModel struct {
	// Craft is the craft's name, one of CraftNames.
	Craft string
	// Model is the model the craft resolved to, "" when no layer set one
	// anywhere. A craft with no model adds no problem and changes no verdict
	// (F17): it is served by the role slot below it, which the pins loops
	// already judge.
	Model string
	// Path is where the model's key lives in the document that wrote it
	// (craft.Layer.CraftKey): a repository's agent.crafts.<craft>.model, the
	// installation config's crafts.<craft>.model, or the project layer's
	// defaults.agent.crafts.<craft>.model. "" is this package's own key,
	// which is the repository's.
	Path string
	// Access is the credential or pool the craft's calls go out on as the
	// resolver answered it (craft.Effective.Access), "" for the project's
	// default credential. Only CheckCraftAccess reads it.
	Access string
}

// craftPath is where a problem about this craft's model is reported: the key
// in the document that wrote it where the caller knows one, this package's own
// key otherwise. A reader sent to a key that is not in the file they have is
// being sent to the wrong file (craft.Layer.CraftKey).
func (c CraftModel) craftPath() string {
	if c.Path != "" {
		return c.Path
	}
	return "agent.crafts." + c.Craft + ".model"
}

// craftModels drops the crafts that name no model, which is the F17 rule in
// one place: an unset craft leaves every verdict exactly as it was, so a check
// never sees it.
func craftModels(crafts []CraftModel) []CraftModel {
	out := make([]CraftModel, 0, len(crafts))
	for _, c := range crafts {
		if c.Model != "" {
			out = append(out, c)
		}
	}
	return out
}

// CheckPins is the rule for a run under a budget: the coder, the reviewer, the
// background model and — when it is set at all — the vision model each name an
// explicit model ID that prices knows, never an alias, so the price charged is
// the price in the table. prices is the embedded table with the owner's
// overrides.
//
// The crafts' models are judged by the same rule, at the key of the document
// that wrote them (D1665's price half): a craft is a model the run charges, so
// a craft model with no row is the same billable failure a role pin with no row
// is. crafts is additive — a caller that holds no resolved craft, and a run,
// which reads them from the slots the bootstrap write filled, passes none — and
// a craft that names no model is not judged (F17).
func CheckPins(a Agent, prices *pricing.Table, crafts ...CraftModel) []Problem {
	return pinProblems(a, prices, crafts, false)
}

// UnpricedPins is CheckPins' price half alone: one Problem per pin that NAMES a
// model the table cannot charge, and nothing for a pin that names no model at
// all.
//
// It exists for the readers that answer "what does this document stop" over a
// document that is not the whole configuration: a repository's own fugaro.yaml
// names no model on a layered installation — the project layer or the published
// policy supplies every slot — so CheckPins over it reports a role "is required
// with a budget" for a run that has one, which is exactly the false refusal
// R8's listing (`fugaro update` and `doctor`, internal/cli/budget_stops.go) may
// not print. An alias is not this function's either: it resolves to a priced
// model, and the pin rule's own sentence about it is CheckPins'.
func UnpricedPins(a Agent, prices *pricing.Table, crafts ...CraftModel) []Problem {
	return pinProblems(a, prices, crafts, true)
}

func pinProblems(a Agent, prices *pricing.Table, crafts []CraftModel, priceOnly bool) []Problem {
	var ps []Problem
	add := func(path, model string) {
		// priceOnly keeps the pins that NAME a model the table cannot charge:
		// a pin that names no model is not a model without a price, and an
		// alias resolves to a row the table holds (pricing.Lookup matches
		// aliases), so neither is a stop R8's listing may name.
		if priceOnly && (model == "" || pricing.IsAlias(model)) {
			return
		}
		switch {
		case model == "" && path == "agent.models.background":
			ps = append(ps, Problem{Path: path, Message: "is required with a budget: name a model ID such as claude-haiku-4-5"})
		case model == "":
			ps = append(ps, Problem{Path: path, Message: "is required with a budget: set it, or agent.model, to a model ID such as claude-sonnet-5-5"})
		case pricing.IsAlias(model):
			ps = append(ps, Problem{Path: path, Message: model + " is an alias: with a budget, name a model ID such as claude-sonnet-5-5"})
		default:
			if _, ok := prices.Lookup(model); !ok {
				ps = append(ps, unpricedProblem(path, model, prices))
			}
		}
	}
	for _, r := range pins(a) {
		add(r.path, r.model)
	}
	for _, c := range craftModels(crafts) {
		add(c.craftPath(), c.Model)
	}
	return ps
}

// unpricedProblem is the one sentence about a model the table cannot charge:
// the catalog's own refusal where it has one — the model, the switch and both
// ways out (P8) — and the model_prices fix otherwise.
func unpricedProblem(path, model string, prices *pricing.Table) Problem {
	if why := prices.Refusal(model); why != "" {
		return Problem{Path: path, Message: why}
	}
	return Problem{Path: path, Message: model + " has no price: add it under model_prices in the project config"}
}

// PinWarnings are the pins whose price is an embedded placeholder nobody has
// verified (a provider model's starter row), or whose owner override left
// cache rates at 0 (counted at the input price instead): the run may go on,
// but its cap counts a guess until the owner sets the real price under
// model_prices, which always wins. Callers show these at pin time and in
// diagnose.
//
// A review level's own model is a pin as well, and is warned about as one, in
// the same words and with its level named (levelPins): the cap counts it at a
// guess exactly like a role's, and this check reading only the three role keys
// left a run whose level names a model of its own — a catalog-priced row as
// much as an unverified one — warned about a price nobody set (the L4.5
// follow-up).
func PinWarnings(a Agent, prices *pricing.Table) []Problem {
	var ps []Problem
	seen := map[string]bool{}
	for _, r := range append(pins(a), levelPins(a)...) {
		m, ok := prices.Lookup(r.model)
		if !ok || seen[m.ID+r.path] {
			continue
		}
		seen[m.ID+r.path] = true
		if m.Unverified {
			ps = append(ps, Problem{Path: r.path, Code: CodeUnverifiedPrice, Message: r.prefix() + fmt.Sprintf(
				"%s has an unverified placeholder price (%s, %s): set the real one under model_prices; until then the cap counts a guess",
				r.model, m.PriceSource, m.PriceCheckedAt)})
		}
		if w := m.CacheWarning(); w != "" {
			ps = append(ps, Problem{Path: r.path, Code: pricing.CodeCacheRateDefaulted, Message: r.prefix() + w})
		}
		if w := catalogWarning(r.model, m); w != "" {
			ps = append(ps, Problem{Path: r.path, Code: CodeCatalogPrice, Message: r.prefix() + w})
		}
	}
	return ps
}

// CodeUnverifiedPrice marks a PinWarnings problem.
const CodeUnverifiedPrice = "unverified_price"

// CodeCatalogPrice marks the PinWarnings problem for a row the built-in
// provider price catalog priced.
const CodeCatalogPrice = "catalog_price"

// catalogWarning is the pin warning for a catalog-priced row: which tier and
// how many endpoints it covers, which end of that tier's range is charged,
// the numbers behind that range, and the one switch that would charge the
// other end. One place, so `validate`, `diagnose` and the runner's
// pin-warning log line say the same thing. m.Catalog is nil on every other
// row, and "" then.
func catalogWarning(model string, m pricing.Model) string {
	c := m.Catalog
	if c == nil {
		return ""
	}
	n := c.Endpoints
	if c.Tier == "zdr" {
		n = c.ZdrEndpoints
	}
	// The bound the compiler charged, said in the owner's own words, and the
	// switch that would move it to the other end (P11): highest is what an
	// unset price_policy means (D2644), so a row already at the top of its
	// range names cheapest instead — naming the default would be advice to
	// change nothing. The numbers of the dearest end are here too, when the
	// charge leaves room above it: the undercharge is dollars, not only
	// accuracy (design D6).
	end, move := "the dearest of those endpoints", "price_policy: cheapest charges the bottom of the range"
	if c.Bound == "min" {
		end, move = "the cheapest of those endpoints", "price_policy: highest charges the top of the range"
	}
	where := fmt.Sprintf("the %s tier of %d endpoint(s), checked %s, charged at %s", c.Tier, n, c.CheckedAt, end)
	if c.Stale {
		// A stale catalog charges the dearest end whatever the policy says,
		// so the sentence keeps naming highest: it is the policy under which
		// the charge and the switch agree.
		where = fmt.Sprintf("the %s tier of %d endpoint(s), and the catalog is stale (checked %s), so the dearest end is charged", c.Tier, n, c.CheckedAt)
		move = "price_policy: highest charges the top of the range"
	}
	if r := m.CatalogRange(); r != "" {
		where += "; " + r
	}
	return fmt.Sprintf("%s is priced from the built-in provider price catalog: %s. require_zdr selects the tier and price_policy selects the end of its range: %s (an owner's model_prices replaces this row whole)",
		model, where, move)
}

// CheckAllowed is the rule for a run under an allow-list of models: the
// coder, the reviewer, the background model and — when it is set — the vision
// model must each be on it. Without a list (policy.Effective.HasAllowList)
// every model passes. A list that is set
// but empty denies everything, and a role that names no model is refused too,
// because the model the agent would pick is not known to be on the list. Each
// problem names the role, the model and the layer that set the list; when
// that layer is neither the ceiling nor the policy that replaced it
// (policy.SourcePolicy: the ceiling *is* that list, so naming it twice would
// print the same node under two labels) and the ceiling has a list of its own
// (ceiling, nil when it has none), the message gives that list too, so an
// empty intersection can be understood.
//
// rt and providers are the routing switch and the routes the caller has, in
// that caller's own sense of "has": the run passes the policy node's switch and
// the routes its job carries, `fugaro validate` the operator's config and the
// routes the run will have. They are what a *route's* own ID is judged against
// (see allowed): a pin that names a route ID is admitted by an allow-list only
// when that switch routes the model to that route (review finding on this
// change — before it, any well-formed anthropic/<id> was admitted by its
// canonical ID alone, so a repository's own pin reached a route the owner's
// switch never opted in).
//
// The crafts' models are judged by the same two rules, at the key of the
// document that wrote them (D1665's allow-list and route halves), and crafts is
// the additive parameter that carries them: `budget.allowed_models` bounds a
// craft's model exactly as it bounds a pin (the same `allowed` predicate, so the
// routed-ID question is answered once), and a craft's model must be one the
// routes carry — a provider model a provider entry claims, a Claude ID the
// switch names that its own resolution can find (craftRouteProblems). The route
// half is judged whether or not the run has a list: a craft's model with no
// route is refused by the run's own bootstrap (internal/runner/routing.go's
// applyRouting halts on it), and the panel is where that is worth saying before
// the task is written. A run passes no crafts — its own pins already carry the
// resolved craft models, which the bootstrap write put in the slots (MC12) —
// and a craft that names no model is judged by neither rule (F17).
func CheckAllowed(a Agent, e policy.Effective, ceiling []string, rt *Routing, providers map[string]ModelProvider, crafts ...CraftModel) []Problem {
	var ps []Problem
	if e.HasAllowList() {
		list := "none"
		if len(e.AllowedModels) > 0 {
			list = strings.Join(e.AllowedModels, ", ")
		}
		src := e.Sources[policy.KeyAllowedModels]
		where := "allowed_models from " + src + ": " + list
		if src != policy.SourceCeiling && src != policy.SourcePolicy && ceiling != nil {
			where += "; the project's allow-list: " + strings.Join(ceiling, ", ")
		}
		for _, r := range pins(a) {
			switch {
			case r.model == "":
				ps = append(ps, Problem{Path: r.path, Message: "names no model, but the run has an allow-list (" + where + "): set it, or agent.model, to a model on the list"})
			case !allowed(e, r.model, rt, providers):
				ps = append(ps, Problem{Path: r.path, Message: CodeSpan(r.model) + " is not an allowed model (" + where + ")"})
			}
		}
		// A craft that names no model is not refused for it, which is the one
		// place this loop differs from the one above: a role's absence leaves
		// the model the agent would pick unknown, while a craft's absence
		// leaves the role slot below it in charge — and that slot the loop
		// above already judged (F17).
		for _, c := range craftModels(crafts) {
			if !allowed(e, c.Model, rt, providers) {
				ps = append(ps, Problem{Path: c.craftPath(), Message: CodeSpan(c.Model) + " is not an allowed model (" + where + ")"})
			}
		}
	}
	return append(ps, craftRouteProblems(crafts, rt, providers)...)
}

// craftRouteProblems is D1665's route half: a craft's model must be one the
// routes carry. A provider model (a vendor/model ID, or any spelling a route
// entry claims) must be carried by a provider entry the caller has — the same
// question CheckProviderPolicy asks of a pin, and the same `unclaimedModelRefusal`
// answers — and a Claude ID the switch opts in must be one the switch's own
// resolution finds (RoutingClaudeFor), because the run halts on a pin the switch
// names and cannot resolve rather than sending it direct (D1b, applyRouting).
//
// A Claude ID the switch does not name goes direct and needs no route: the list
// is the opt-in (D1a). So does an ID under via: direct — an inert list under it
// is valid and routes nothing — and under via: cascade, which carries no list at
// all and whose calls all still go to Anthropic under the ID the pin names
// (O3). The reach question (whether the switch acts at all in this run: the
// gateway off, via auto under a Vertex run) is deliberately not asked here, for
// the reason `allowed` gives: this same function is `fugaro validate`'s offline
// verdict, and a rule that turned on the run's own state would report one answer
// for a configuration the run then judged by another.
func craftRouteProblems(crafts []CraftModel, rt *Routing, providers map[string]ModelProvider) []Problem {
	var sw *RoutingClaude
	if rt != nil {
		sw = rt.Claude
	}
	var ps []Problem
	for _, c := range craftModels(crafts) {
		if !strings.Contains(c.Model, "/") && !claimed(providers, c.Model) {
			// A Claude ID: routed only when the switch names it, and then only
			// by a switch whose via routes at all (D1a's explicit list, D1b's
			// default).
			if sw == nil || !sw.Names(c.Model) {
				continue
			}
			if via := sw.ViaValue(); via != RouteOpenRouter && via != RouteAuto {
				continue
			}
			if route, sent, ok := RoutingClaudeFor(sw, c.Model, providers); !ok || route == RouteDirect || sent == c.Model {
				ps = append(ps, Problem{Path: c.craftPath(), Message: fmt.Sprintf("%s is named by the routing switch and this configuration cannot resolve its route: the switch routes nothing rather than sending the call direct, so a run on it halts in bootstrap — name a route entry that claims the model with allow_data_to written, or remove the ID from the switch's list (D1b)", CodeSpan(c.Model))})
			}
			continue
		}
		// A provider model, or any spelling a provider claims: it must be one
		// the caller's provider entries serve, which is the pin rule's own
		// question and its own refusal (CheckProviderPolicy).
		if !claimed(providers, c.Model) {
			ps = append(ps, Problem{Path: c.craftPath(), Message: unclaimedModelRefusal(c.Model)})
		}
	}
	return ps
}

// allowed reports whether an allow-list admits model: by its own name, or — for
// a pin the routing switch routed — by the canonical ID of the same model. A
// routed pin names the route's own ID (anthropic/claude-sonnet-5.5) and the
// list the owner wrote names the model (claude-sonnet-5-5): one model, one
// entry, and the list is about which models may run rather than which route
// serves them (plan Task 1b.2).
//
// The second answer is the checked-in map's inverse (pricing.CanonicalFor) plus
// the switch, and it takes all of: the map must carry the pair *inverse* (so
// the spelling is a route ID at all), the list must name the canonical ID the
// pair gives (the model, which is what allowed_models is about), and rt must
// route that canonical ID to exactly this ID — the same resolution the run
// itself performs (RoutingClaudeFor, over the same routes). Without that half,
// any well-formed anthropic/<id> a repository wrote for itself was admitted by
// its canonical ID alone, which is the owner's allowed_models *and* the
// switch's explicit-list opt-in (D1a) both bypassed by one spelling (review
// finding on this change). An absent switch, a switch that does not name the
// model, and a route the map does not pair all admit nothing.
//
// What that admits, said exactly, because "the run was going to route it
// anyway" is not the whole story (the later review finding on this change): the
// question here is the switch's *resolution*, and the run's application of the
// switch is a separate step that is skipped in states where the run has nothing
// to act on — the gateway off, via auto under a Vertex run, via auto with an
// ANTHROPIC_API_KEY in the job (internal/runner/routing.go's applyRouting). In
// those states this rule still admits a route ID the switch resolves, and
// applyRouting leaves every pin spelled as the configuration wrote it: what
// admits the pin there is the operator's own claim on the route (the entry's
// allow_data_to names this repository, D1b's safeguard) plus the canonical
// model on the list, not the switch. So the rule answers "is this model
// allowed?" and never promises the run takes the route; that promise is D1a's
// explicit list, and it is applyRouting that keeps it.
//
// It is not made skip-aware on purpose: this same function is `fugaro
// validate`'s offline verdict (internal/cli/validate.go), where there is no
// credential and no budget mode to read, so a rule that turned on the run's own
// state would report one answer for a policy the run then judged by another —
// the divergence the one-resolver rule exists to prevent. The two halves are
// tested together instead (internal/runner's
// TestAllowedAdmitsARouteIDTheSwitchDoesNotApply).
func allowed(e policy.Effective, model string, rt *Routing, providers map[string]ModelProvider) bool {
	if e.Allows(model) {
		return true
	}
	canonical, ok := pricing.CanonicalFor(model)
	if !ok || !e.Allows(canonical) || rt == nil || rt.Claude == nil {
		return false
	}
	// The model is on the list; the question left is whether this spelling is
	// the route's own ID for it. It is admitted when the switch resolves the
	// canonical ID to exactly this one, and not otherwise — a spelling the
	// switch does not route is a route's ID the run is not carrying.
	route, sent, ok := RoutingClaudeFor(rt.Claude, canonical, providers)
	return ok && route != RouteDirect && sent == model
}

// CraftAccess are the documents a craft's access is judged against: the
// repository, and the credentials, pools, Claude accounts and provider entries
// the layers hold. It is the caller's own, because the layers are — a run's
// published node, `fugaro validate`'s installation config — exactly as the
// layers of the resolver are (craft.Resolve).
type CraftAccess struct {
	// Repo is the repository (owner/name) the access must admit, "" when it is
	// unknown (a checkout with no origin). A member that names no list of its
	// own admits every repository, "" included, and one that names a list
	// admits only what the list holds: an unknown repository is then admitted
	// by nothing, which is the fail-closed answer.
	Repo string
	// Credentials and Pools are the documents' own, which is what PoolMembers
	// reads a member's type from. A member neither defines is the built-in
	// vertex (D1624).
	Credentials []Credential
	Pools       []Pool
	// Accounts are the project's Claude accounts by name: a credential of type
	// claude-oauth is the account of the same name (D1621 — the migration
	// derives one from the other), and the account's own allow_data_to is what
	// admits a repository to it. The run reads the same rule off the published
	// node, where the publish carries that list as the account's allow_repos,
	// which is why the refusal below names both spellings, as the run's own
	// skip sentence does.
	Accounts map[string]ClaudeAccount
	// Providers are the provider entries by name, whose allow_data_to admits a
	// repository to an openrouter member.
	Providers map[string]ModelProvider
}

// CheckCraftAccess is D1665's fourth check, the access half: a craft's model
// cannot be served by an access that admits this repository to nothing. A pool
// is admitted when at least one of its members is (D1627: a member that does
// not admit the repository is skipped and recorded, and only a pool with *no*
// admitting member leaves the stage with nothing to run on), and a credential
// is admitted by its own rule.
//
// The refusal is reported at the craft's model key, which is where the verdict
// belongs: the model is what cannot be served, and a craft that names no model
// is not refused here at all (F17) — its stages are served by the role slot
// below it, on a credential the access does not choose.
//
// The per-member question is F10's, and the answer is read from the same
// predicates the run's own walk reads (internal/runner/poolchoice.go's
// poolMemberAdmits): a Claude account's and a provider entry's own AllowsData
// over the same two lists — allow_repos for an account, allow_data_to for a
// provider. A member of a type no document here answers is not refused: an
// Anthropic key is the credentials service's, whose own admission rule is that
// service's, and this check holds none of its facts (the same division
// poolMemberAdmits' own comment states).
func CheckCraftAccess(crafts []CraftModel, a CraftAccess) []Problem {
	var ps []Problem
	for _, c := range craftModels(crafts) {
		if c.Access == "" {
			continue
		}
		if why := craftAccessRefusal(c.Access, a); why != "" {
			ps = append(ps, Problem{Path: c.craftPath(), Message: why})
		}
	}
	return ps
}

// craftAccessRefusal is why the credential or pool this craft's calls go out on
// may not be given this repository's code, "" when it may. The two sentences
// differ because the fixes differ: a pool is mended by listing a member that
// admits the repository, and a credential by the repository being listed in its
// own allow list.
//
// A name no document holds is admitted here: that a name resolves to nothing at
// all is the resolver's own problem (craft.Resolve reports it at the access
// key), and refusing it here would report one mistake under two keys.
func craftAccessRefusal(name string, a CraftAccess) string {
	for _, p := range a.Pools {
		if p.Name != name {
			continue
		}
		for _, m := range PoolMembers(p, a.Credentials) {
			if craftMemberAdmits(m, a) {
				return ""
			}
		}
		return fmt.Sprintf("cannot be served by this craft's access: the pool %s lists no member that may be given %s's code (allow_data_to for a provider, allow_repos for a Claude account), so every member of it is skipped and the craft is left with nothing to run on (D1627) — list a member that admits this repository, or point the craft's access at a credential or pool that does",
			CodeSpan(name), CodeSpan(a.Repo))
	}
	// A single credential is a pool of one, read through the same predicate
	// PoolMembers uses: a name a document does not define is the built-in
	// vertex, which is the one name a pool may list without a layer defining it
	// and which admits every repository (D1624).
	m := PoolMembers(Pool{Name: name, Members: []string{name}}, a.Credentials)[0]
	if craftMemberAdmits(m, a) {
		return ""
	}
	return fmt.Sprintf("cannot be served by this craft's access: the credential %s may not be given %s's code (allow_data_to for a provider, allow_repos for a Claude account), so no call of this craft can be served — list this repository in that credential's allow list, or point the craft's access at a credential or pool that admits it",
		CodeSpan(name), CodeSpan(a.Repo))
}

// craftMemberAdmits is F10's rule for one member, over the documents the
// caller holds: an account admits what its own allow_data_to lists, a provider
// admits what its allow_data_to lists, and the built-in vertex is the project's
// own GCP service, which has no list to be admitted to (D1624). A provider
// entry this document does not carry is not admitted, which is the run's own
// answer (a member whose provider is absent is a member the repository may not
// be given) and the fail-closed one. A member of a type none of those answers —
// the credentials service's — is admitted: that path's own admission rule is
// that service's, and this check holds none of its facts.
func craftMemberAdmits(m PoolMember, a CraftAccess) bool {
	switch m.Type {
	case CredentialTypeClaudeOAuth:
		return a.Accounts[m.Name].AllowsData(a.Repo)
	case CredentialTypeOpenRouter:
		p, ok := a.Providers[m.Name]
		return ok && p.AllowsData(a.Repo)
	case CredentialTypeVertex:
		return true
	}
	return true
}

// CodeSpan quotes s as a markdown code span with the characters that could
// break out of it (backticks, control characters) replaced by "?", for text
// that came from a branch and reaches a report.
func CodeSpan(s string) string {
	return "`" + strings.Map(func(r rune) rune {
		if r == '`' || r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s) + "`"
}
