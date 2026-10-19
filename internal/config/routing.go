package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// The values routing.claude.via accepts (docs/design/project-centric.md §6.2,
// D1a/D1b). They are the config's spelling of projectpolicy's Route* constants,
// which is what a policy node carries, so the two cannot drift.
const (
	// RouteDirect calls Anthropic directly: what an absent switch means (D1b).
	RouteDirect = "direct"
	// RouteOpenRouter sends the opted-in Claude IDs to the provider entry that
	// claims them, with allow_data_to written (D1b's safeguard).
	RouteOpenRouter = "openrouter"
	// RouteAuto is openrouter when the run has no Anthropic key and direct
	// when it has one. The resolver answers the route the switch licenses —
	// the provider's — and the caller that knows whether a key version exists
	// is the one that prefers the direct path (Task 1b.3).
	RouteAuto = "auto"
	// RouteCascade turns the credential cascade on for a workflow whose
	// agent.auth is cascade (D133, D150, O3): an exhausted shared subscription
	// token is replaced by the next usable credential *inside the credentials
	// service*, so the switch changes which credential carries a call and never
	// which route it takes. Tier C for Claude is off (C3.6), so every call
	// still goes to Anthropic under Claude's own model IDs — which is why the
	// switch resolves through no provider entry at all (O3: it "needs no
	// provider route while tier C for Claude is off") and why it takes no
	// models list (CheckRoutingShape).
	//
	// It is one switch with one key, as D150 requires: `routing.claude` stays
	// the one place a Claude call's destination is written, and this value is
	// the operator's way of saying "the cascade decides the credential here".
	// For every other agent.auth the value routes nothing — like direct — and
	// `via: auto` keeps exactly its old meaning (O3).
	RouteCascade = "cascade"
)

// Routing is the routing switch of the operator's config and of the policy
// (docs/design/project-centric.md §6.2). Its definition site in this release is
// the operator's local config alone — written there, and only *delivered* to a
// run through the policy: a repository's fugaro.yaml has no routing key, exactly
// as it has no providers key.
//
// D1a names a *second* definition site, the project layer, and it is not open
// in this release: the layer's own decode is strict, so a layer carrying
// `routing:` fails closed rather than half-applying, and the layer's part in
// routing today is the claim path (`claude_models`: one explicit
// `anthropic/<id>` per route entry, merged into the owner's routes on every
// launch). A claim can therefore be the entry a switch resolves to — but only
// for a route the operator's config defines, and the config-load validation
// judges the switch against the config's own routes, so an operator who wants
// the switch to resolve should keep the claim in the config too.
type Routing struct {
	Claude *RoutingClaude `yaml:"claude,omitempty" json:"claude,omitempty"`
}

// RoutingClaude is the Claude routing switch: which route the Claude IDs it
// names take.
//
// Models is an explicit list of canonical Claude IDs — the IDs a pin may carry
// (`claude-sonnet-5-5`, the price table's key) — and never a pattern: the
// merged ruling (docs/design/mixed-credentials.md §4, §8) requires one explicit
// ID per route entry, never a pattern, and the switch is only a second place
// the same rule is written.
type RoutingClaude struct {
	// Via is direct, openrouter, auto or cascade; empty means direct (D1b).
	Via string `yaml:"via,omitempty" json:"via,omitempty"`
	// Models are the canonical Claude IDs this switch opts in. An ID the list
	// does not name goes direct, and either provider via (openrouter, auto)
	// with an empty list is refused: the opt-in is the list. `via: cascade`
	// takes no list at all — it is the workflow-wide credential switch, not a
	// per-model route — and `via: direct` reads one as inert (D1b's default
	// said out loud).
	Models []string `yaml:"models,omitempty" json:"models,omitempty"`
}

// ViaValue is the via spelled out: empty is direct (D1b), so no caller has to
// branch on the empty string.
func (c *RoutingClaude) ViaValue() string {
	if c == nil || c.Via == "" {
		return RouteDirect
	}
	return c.Via
}

// Names reports whether the switch opts model in (D1a's explicit list).
func (c *RoutingClaude) Names(model string) bool {
	return c != nil && slices.Contains(c.Models, model)
}

// AllowDataToWritten reports whether a provider's allow_data_to was written
// with the repositories it may send code to: a list, or the explicit ["*"]. An
// absent key means every repository under the owner's ruling
// (docs/design/project-secrets.md §3a), which is exactly why the routing
// safeguard requires it to be *written* rather than merely permitted (D1b) —
// "not decided yet" and "every repository" must not be the same state on the
// one path that sends a Claude prompt to a third party. An empty list counts as
// not written too: it lists nobody, and providers validation refuses it as
// ambiguous either way (ValidateModelProviders for YAML, validProviders for a
// policy node) — this is the same answer from the reader that routes by it.
func (p ModelProvider) AllowDataToWritten() bool { return len(p.AllowDataTo) > 0 }

// CheckRouting validates a routing block against the providers it resolves
// through. path is the block's own path ("routing" in the operator's config),
// so a problem names the key the owner wrote.
//
// An absent block, and via: direct, are valid and route nothing (D1b).
// openrouter and auto are held to D1a and D1b between them: an explicit list of
// canonical Claude IDs, never a pattern; a list that is not empty under either
// open via; and, for every ID the list names, a provider entry that claims
// the route's own ID for it *and* has allow_data_to written. via: cascade
// resolves through no provider at all (O3), so the claim rules are not applied
// to it: the cascade changes which credential carries a call, not where it
// goes, and while tier C for Claude is off (C3.6) there is no third-party route
// for a call to be sent on.
func CheckRouting(r *Routing, providers map[string]ModelProvider, path string) []Problem {
	ps := CheckRoutingShape(r, path)
	if len(ps) > 0 || r == nil || r.Claude == nil || r.Claude.ViaValue() == RouteDirect {
		return ps
	}
	// via: cascade reaches no provider claim, and it does so by carrying no list
	// for the loop below to walk: CheckRoutingShape refuses a models list under
	// it, so by the time this function gets past the guard above there is
	// nothing left to hold to a claim. That is O3 — the cascade is switched on
	// without the operator configuring a route it never uses — and it is one
	// rule rather than two: the shape rule is the rule everywhere the object is
	// written or read, and a second copy of "cascade needs no claim" here would
	// be a second answer to drift from it.
	//
	// D1b's safeguard, per ID the switch names: the route must exist (exactly
	// one provider entry claims it) and that provider's allow_data_to must be
	// *written*.
	//
	// providers is the table the caller judges over, and the two callers hold
	// different ones: the config loader and the policy reader pass the
	// operator's own entries, and a run passes the routes its job carries —
	// runner.ProvidersFor's table, every entry whose allow_data_to does not
	// name that repository dropped (internal/runner/providers.go). The
	// no-claim message therefore names both causes rather than only the first,
	// because "no provider entry claims it" is false of a project whose entry
	// claims the model for other repositories: an operator sent to add a route
	// entry that already exists edits the wrong file. A caller that holds both
	// tables can sharpen it (internal/cli/validate.go's switch-list rule).
	for i, m := range r.Claude.Models {
		at := fmt.Sprintf("%s.claude.models[%d]", path, i)
		claims := claudeClaimsFor(providers, m)
		switch {
		case len(claims) == 0:
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s is named by the switch but no provider entry claims it: a route entry lists the models it serves, and only the owner can add one, in the project config (or through the layer's claude_models claim, which only adds to a route the config defines) — never in a repository's fugaro.yaml. Where this table is a run's own routes, an entry that claims the model for other repositories is not among them at all: add this repository to that entry's allow_data_to, then run fugaro init --repo for it (the list a run reads is built into its job at deploy time)", m)})
		case len(claims) > 1:
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s is claimed by %d provider entries, %s: two spellings of one model are one model, and a switch that resolved to whichever name sorts first would route by provider name. Leave the claim on one entry and remove it from the others", m, len(claims), claimNames(claims))})
		case reservedRouteName(claims[0].name):
			// The entry's name is what the resolver hands back as the route,
			// and "direct" is the resolver's own value for the direct route:
			// its consumers tell "no route to take" from "the direct route" by
			// comparing the route with RouteDirect (internal/runner's routing
			// check, internal/config's pin admission), so this switch would be
			// read as direct by some readers and as a provider by others, and
			// every run of the project would halt with a cause that is not the
			// real one (the review finding on this change).
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s resolves to the provider entry %s, and the switch's own name for the direct route is %s: the resolver returns a provider entry's name as the route (never the via word), so a provider entry named %s cannot be told from the direct route by the code that reads the resolution. Rename the provider entry (to any other name) and update the models it claims", m, claims[0].name, RouteDirect, RouteDirect)})
		case !claims[0].p.AllowDataToWritten():
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s resolves to provider %s, whose allow_data_to is not written: name the repositories whose code may be sent there, or write [\"*\"] to mean every one of them on purpose (D1b; an absent allow_data_to already means every repository, which is why this one path requires it to be written)", m, claims[0].name)})
		case !routeIDPair(m, claims[0].claim):
			// The route entry claims a spelling of this model that the
			// checked-in map does not pair with it: two spellings that look
			// alike are two answers to where the call goes and what it costs,
			// and the switch does not choose between them (Task 1b.2).
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s is claimed by provider %s as %s, which is not the route's own ID for it in the canonical-to-route map (internal/pricing, read through RouteIDFor): %s", m, claims[0].name, claims[0].claim, unmappedOrPair(m))})
		}
	}
	return ps
}

// routeIDPair reports whether canonical and route are the pair the checked-in
// map holds (pricing.RouteIDFor): the one translation from a pin's Claude ID to
// what a route is sent and charged.
func routeIDPair(canonical, route string) bool {
	_, ok := pricing.RouteIDFor(canonical, route)
	return ok
}

// reservedRouteName reports whether a provider entry's name is one of the
// switch's own route words, which no provider entry may be: RoutingClaudeFor
// answers with the entry's *name* as the route, and its consumers tell the
// direct route from a route by comparing that value with RouteDirect
// (internal/runner's routing check, internal/config's pin admission) — so a
// provider entry named direct is the same string as "the direct route" to
// every reader of the resolution, and the run that resolves through it halts
// naming a cause that is not the real one (the review finding on this change).
//
// Only the direct route is reserved, and not the other via words: the resolver
// never returns a via word as the route, so "auto" and "openrouter" name a
// provider entry like any other name — a fixture's own provider is called
// openrouter — while RouteDirect is a value a caller compares the resolution
// with.
func reservedRouteName(name string) bool { return name == RouteDirect }

// unmappedOrPair is the clause a switch problem carries for a model whose claim
// is not the map's pair: the map's own reason when it has one (a row that is
// explicitly unmapped), else what the map does hold for it, so a typo in a route
// entry reads as a typo.
func unmappedOrPair(canonical string) string {
	if why, ok := pricing.UnmappedReason(canonical); ok {
		return why
	}
	if id, ok := pricing.RouteIDFor(canonical, ""); ok {
		return fmt.Sprintf("the map pairs it with %s", id)
	}
	return "the map carries no row for it: add one, with the route's ID measured, before the switch may name this model"
}

// claimNames spells the entries claiming one model for a problem message:
// "a (anthropic/claude-sonnet-5.5) and b (anthropic/claude-sonnet-5-5)", each
// with the spelling it claims, because the two spellings being the same model
// is the whole reason the entry is refused.
func claimNames(claims []routeClaim) string {
	parts := make([]string, 0, len(claims))
	for _, c := range claims {
		parts = append(parts, fmt.Sprintf("%s (%s)", c.name, c.claim))
	}
	return strings.Join(parts, " and ")
}

// CheckRoutingShape is the rule the routing object is held to wherever it is
// written or read: the via, and an explicit list of Claude IDs, never a
// pattern (D1a). It needs no providers, so a reader that does not hold them —
// a policy node being decoded, say — applies exactly the
// rule the operator's own config loader applies, and the three cannot drift.
//
// An absent block, and via: direct, are valid and route nothing (D1b). A list
// under via: direct is inert rather than refused: it names nothing that is
// routed, and D1b's default is that the direct route is where an unwritten
// switch leaves every call.
//
// The two provider vias are the other way round: they must carry a list. An
// empty one under either is refused, because "route every Claude model through
// this provider" is the pattern D1a forbids, and an empty list is that pattern
// written by default rather than by name. via: cascade is the third case, and
// it carries no list at all: it is the workflow's credential switch (D133,
// D150), not a per-model route, so a list under it would be a setting nothing
// acts on — and an inert list here is not harmless, because the reader that
// rewrites a run's pins would stop on a model the cascade "opts in" and cannot
// resolve to a route.
func CheckRoutingShape(r *Routing, path string) []Problem {
	if r == nil || r.Claude == nil {
		return nil
	}
	c := r.Claude
	via := c.ViaValue()
	switch via {
	case RouteDirect, RouteOpenRouter, RouteAuto, RouteCascade:
	default:
		return []Problem{{Path: path + ".claude.via", Message: fmt.Sprintf("%q must be %s, %s, %s or %s", c.Via, RouteDirect, RouteOpenRouter, RouteAuto, RouteCascade)}}
	}
	var ps []Problem
	// A block written with an empty via is refused rather than read as the
	// absent switch: D1b's default is what an *unwritten* switch means, and
	// "openrouter and auto must be written" is the same sentence from the
	// other side. The resolver (RoutingClaudeFor) still reads an unwritten via
	// as direct — it is reached with a switch that passed this check, or with
	// none at all.
	if c.Via == "" {
		ps = append(ps, Problem{Path: path + ".claude.via", Message: fmt.Sprintf("is written with no value: name %s, %s, %s or %s, or remove the block — an absent switch is %s (D1b)", RouteDirect, RouteOpenRouter, RouteAuto, RouteCascade, RouteDirect)})
	}
	if via == RouteCascade && len(c.Models) > 0 {
		// The cascade changes which credential carries a call, never which
		// route it takes: tier C for Claude is off (C3.6), so every cascaded
		// call still goes to Anthropic under the ID a pin already names, and
		// there is no route for a model list to opt into (O3). Refused rather
		// than tolerated as an inert list, because the pin rewriter acts on the
		// list it finds: a model this switch "names" has no route to resolve to
		// and would halt every run of the project (internal/runner's
		// applyRouting), while the operator who wrote it believed they were
		// choosing which models cascade.
		return []Problem{{Path: path + ".claude.models", Message: fmt.Sprintf("names %d model(s), and via: %s takes no list: the cascade is turned on for the whole run by the via alone — it decides which credential carries a call, never which route one takes, and while tier C for Claude is off every cascaded call still goes to Anthropic under the ID the pin already names (D133, D150, C3.6). Remove the list, or write the provider route the switch takes (via: %s with an explicit list of the IDs it carries)", len(c.Models), RouteCascade, RouteOpenRouter)}}
	}
	if (via == RouteOpenRouter || via == RouteAuto) && len(c.Models) == 0 {
		ps = append(ps, Problem{Path: path + ".claude.models", Message: fmt.Sprintf("is empty, and an empty list with via: %s would opt every Claude model in: name the IDs this switch carries, one explicit Claude ID per entry, never a pattern (D1a, docs/design/mixed-credentials.md §4)", via)})
	}
	for i, m := range c.Models {
		if msg := CheckRoutingModel(m); msg != "" {
			ps = append(ps, Problem{Path: fmt.Sprintf("%s.claude.models[%d]", path, i), Message: msg})
		}
	}
	return ps
}

// RoutingClaudeFor resolves one Claude model's route: the route its calls take
// and the model ID that is sent on it. It is the one resolver of the switch, and
// phase 1b's later tasks are its consumers: validate (Task 1b.3, which prints
// the resolution or says it cannot resolve it offline), and the pins renderer
// and the price lookup, which are served by the run applying this resolver once
// to its own configuration (internal/runner/routing.go, Task 1b.2): every pin
// the agent is given, every route the gateway registers and every row a call is
// priced from then names the route's own ID, so serving_model and priced_as are
// the ID this returned. TestRoutingHasOneResolver fails if any other file starts
// comparing the canonical ID with a route's spelling: two derivations of the
// route are two answers to where a call goes, and the channel's whole point is
// that there is one.
//
//	route is RouteDirect, or the name of the provider entry the call is sent
//	      to. A provider entry named RouteDirect is refused rather than
//	      answered with (reservedRouteName): the route and the direct route
//	      would be one string.
//	sent  is the model ID to send: the canonical ID on the direct route, and
//	      on a provider route the ID that provider claims — the route's own
//	      spelling of the same model.
//	ok    is false when the switch asks for a route and there is none to
//	      take: the ID is named but no provider entry claims it, the one that
//	      does has no allow_data_to written, it is named RouteDirect, or more
//	      than one claims it (in
//	      which case CheckRouting refuses the switch at load anyway, and the
//	      resolver still fails closed rather than routing by provider name). A
//	      caller must not print or use a resolution it did not resolve, and
//	      must not quietly fall back to the direct route — the fall-through D1b
//	      exists to prevent.
//
// A model the switch does not name goes direct whatever via says: the list is
// the opt-in (D1a), so a Claude ID the operator did not write is untouched.
//
// via: cascade resolves every model to the direct route, and that is the
// switch's whole answer here: the cascade decides which *credential* carries a
// call inside the credentials service, not which route one takes, and while
// tier C for Claude is off (C3.6) the ID a pin names is the ID Anthropic is
// sent. There is deliberately no cascade value of route or sent to hand back —
// a caller that acted on one would be a second place the cascade's choice is
// made (D133: one explicit mode, and the service is what picks the
// credential).
func RoutingClaudeFor(c *RoutingClaude, model string, providers map[string]ModelProvider) (route, sent string, ok bool) {
	switch {
	case c.ViaValue() == RouteDirect:
		return RouteDirect, model, true
	case c.ViaValue() == RouteCascade:
		// `sent` is the model as the pin names it: the cascade forwards the
		// body it was given, so nothing about the call's model ID changes.
		return RouteDirect, model, true
	case !c.Names(model):
		return RouteDirect, model, true
	}
	claims := claudeClaimsFor(providers, model)
	if len(claims) != 1 || !claims[0].p.AllowDataToWritten() {
		return "", "", false
	}
	if reservedRouteName(claims[0].name) {
		// The route would be the switch's own word for the direct route:
		// CheckRouting refuses this configuration at load and on the node, and
		// this is the resolver failing closed rather than answering a route no
		// caller can tell from "direct" (it is what a caller that resolved
		// without asking CheckRouting first would otherwise act on).
		return "", "", false
	}
	// The route's own ID is the map's pair, never the spelling the entry
	// happens to carry: two spellings that look alike are two routes and two
	// prices (Task 1b.2).
	sent, ok = pricing.RouteIDFor(model, claims[0].claim)
	if !ok {
		return "", "", false
	}
	return claims[0].name, sent, true
}

// routeClaim is one provider entry claiming a model's route ID: the entry's
// name, the claim it wrote (the ID to send), and the entry itself.
type routeClaim struct {
	name  string
	claim string
	p     ModelProvider
}

// claudeClaimsFor returns every provider entry that claims model's route ID,
// in provider-name order: the entries whose models name an explicit
// anthropic/<id> for this model, and the claim itself (the ID to send).
//
// The two spellings are the same model on two routes — the canonical
// claude-sonnet-5-5 of the price table and a pin, and OpenRouter's
// anthropic/claude-sonnet-5.5 — and the comparison is by the only difference
// between them: case, and "." where the canonical ID has "-". It only *locates*
// a claim: whether the claim it found is the route's own ID for this model is
// the map's answer (pricing.RouteIDFor), and a claim the map does not pair with
// the model resolves to nothing — the fold must not decide a route, which is
// what phase 1b.2 removed.
//
// The whole list, not the first: two entries claiming the same model under
// different spellings are an ambiguity the caller has to refuse (CheckRouting
// does, naming both), and picking one by sort order would answer a routing
// question with an alphabetical accident (the review of PR #414).
//
// Within one entry, the claim is the map's pair when the entry writes it, and
// the first claim it writes otherwise. One entry spells this model twice only
// when it writes both the route's own ID and the canonical-looking one
// (anthropic/claude-sonnet-5.5 and anthropic/claude-sonnet-5-5 are one model),
// and which of the two the entry is judged and sent by must not depend on the
// order the two are listed in: taking the first match made
// [anthropic/claude-sonnet-5.5, anthropic/claude-sonnet-5-5] resolve and the
// same two in the other order a refusal (review finding on this change). The
// preference is not a second derivation — it selects between spellings the
// entry itself wrote, and the map still answers which one a route is sent.
func claudeClaimsFor(providers map[string]ModelProvider, model string) []routeClaim {
	var out []routeClaim
	for _, n := range sortedKeys(providers) {
		pv := providers[n]
		var matched []string
		for _, pat := range pv.Models {
			if tail := claudeRouteID(pat); tail != "" && sameClaudeID(tail, model) {
				matched = append(matched, pat)
			}
		}
		if len(matched) == 0 {
			continue
		}
		claim := matched[0]
		for _, pat := range matched {
			if routeIDPair(model, pat) {
				claim = pat
				break
			}
		}
		out = append(out, routeClaim{name: n, claim: claim, p: pv})
	}
	return out
}

// claudeRouteID reports whether id is an explicit Claude claim — the
// "anthropic/<id>" a route entry writes to serve one Claude model — and returns
// its tail. "" for anything else.
func claudeRouteID(id string) string {
	tail, found := strings.CutPrefix(id, "anthropic/")
	if !found || tail == "" || strings.ContainsAny(tail, "*: ") {
		return ""
	}
	return tail
}

// sameClaudeID reports whether two spellings name the same Claude model: the
// canonical ID of the price table (claude-sonnet-5-5) and the route's own
// (claude-sonnet-5.5) differ only in case and in which separator holds the
// version.
func sameClaudeID(a, b string) bool {
	return claudeIDKey(a) == claudeIDKey(b)
}

func claudeIDKey(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), ".", "-")
}

// CheckRoutingModel is the rule for one entry of routing.claude.models: an
// explicit canonical Claude ID, never a pattern (D1a). It returns the problem,
// or "" when the entry is fine.
func CheckRoutingModel(m string) string {
	switch {
	case m == "":
		return "is empty: name a Claude ID such as claude-sonnet-5-5"
	case strings.Contains(m, "*"):
		return fmt.Sprintf("%q is a pattern: routing.claude.models is an explicit list of Claude IDs, one ID per entry, never a pattern (D1a, docs/design/mixed-credentials.md §4, §8)", m)
	case strings.Contains(m, "/"):
		return fmt.Sprintf("%q is a route ID, not the canonical one: name the Claude ID a pin carries (claude-sonnet-5-5); the route entry is what names anthropic/claude-sonnet-5.5", m)
	case strings.Contains(m, ":"):
		return fmt.Sprintf("%q is a variant (a \":\" suffix is another price and routing): name the plain Claude ID", m)
	case !strings.HasPrefix(m, "claude-") || len(m) <= len("claude-"):
		return fmt.Sprintf("%q is not a Claude model ID: routing.claude.models is an explicit list of canonical Claude IDs such as claude-sonnet-5-5, never a pattern", m)
	case pricing.IsAlias(m):
		return fmt.Sprintf("%q is an alias: name the explicit Claude ID a pin carries (claude-sonnet-5-5)", m)
	}
	return ""
}
