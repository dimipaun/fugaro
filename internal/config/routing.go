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
	// Via is direct, openrouter or auto; empty means direct (D1b).
	Via string `yaml:"via,omitempty" json:"via,omitempty"`
	// Models are the canonical Claude IDs this switch opts in. An ID the list
	// does not name goes direct, and either open via with an empty list is
	// refused: the opt-in is the list.
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
// the route's own ID for it *and* has allow_data_to written.
func CheckRouting(r *Routing, providers map[string]ModelProvider, path string) []Problem {
	ps := CheckRoutingShape(r, path)
	if len(ps) > 0 || r == nil || r.Claude == nil || r.Claude.ViaValue() == RouteDirect {
		return ps
	}
	// D1b's safeguard, per ID the switch names: the route must exist (exactly
	// one provider entry claims it) and that provider's allow_data_to must be
	// *written*.
	for i, m := range r.Claude.Models {
		at := fmt.Sprintf("%s.claude.models[%d]", path, i)
		claims := claudeClaimsFor(providers, m)
		switch {
		case len(claims) == 0:
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s is named by the switch but no provider entry claims it: a route entry lists the models it serves, and only the owner can add one, in the project config (or through the layer's claude_models claim, which only adds to a route the config defines) — never in a repository's fugaro.yaml", m)})
		case len(claims) > 1:
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s is claimed by two provider entries, %s: two spellings of one model are one model, and a switch that resolved to whichever name sorts first would route by provider name. Leave the claim on one entry and remove it from the other", m, claimNames(claims))})
		case !claims[0].p.AllowDataToWritten():
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s resolves to provider %s, whose allow_data_to is not written: name the repositories whose code may be sent there, or write [\"*\"] to mean every one of them on purpose (D1b; an absent allow_data_to already means every repository, which is why this one path requires it to be written)", m, claims[0].name)})
		case !routeIDPair(m, claims[0].claim):
			// The route entry claims a spelling of this model that the
			// checked-in map does not pair with it: two spellings that look
			// alike are two answers to where the call goes and what it costs,
			// and the switch does not choose between them (Task 1b.2).
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s is claimed by provider %s as %s, which is not the route's own ID for it in the canonical-to-route map (internal/pricing, ClaudeRoutes): %s", m, claims[0].name, claims[0].claim, unmappedOrPair(m))})
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
// The two open vias are the other way round: they must carry a list. An empty
// one under either is refused, because "route every Claude model through this
// provider" is the pattern D1a forbids, and an empty list is that pattern
// written by default rather than by name.
func CheckRoutingShape(r *Routing, path string) []Problem {
	if r == nil || r.Claude == nil {
		return nil
	}
	c := r.Claude
	via := c.ViaValue()
	switch via {
	case RouteDirect, RouteOpenRouter, RouteAuto:
	default:
		return []Problem{{Path: path + ".claude.via", Message: fmt.Sprintf("%q must be %s, %s or %s", c.Via, RouteDirect, RouteOpenRouter, RouteAuto)}}
	}
	var ps []Problem
	// A block written with an empty via is refused rather than read as the
	// absent switch: D1b's default is what an *unwritten* switch means, and
	// "openrouter and auto must be written" is the same sentence from the
	// other side. The resolver (RoutingClaudeFor) still reads an unwritten via
	// as direct — it is reached with a switch that passed this check, or with
	// none at all.
	if c.Via == "" {
		ps = append(ps, Problem{Path: path + ".claude.via", Message: fmt.Sprintf("is written with no value: name %s, %s or %s, or remove the block — an absent switch is %s (D1b)", RouteDirect, RouteOpenRouter, RouteAuto, RouteDirect)})
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
// and the model ID that is sent on it. It is the one resolver of the switch,
// and phase 1b's later tasks are its consumers: the pins renderer and the price
// lookup (Task 1b.2: stagePins renders the route's ID, and serving_model /
// priced_as are recorded with the same resolved ID) and validate (Task 1b.3,
// which prints the resolution or says it cannot resolve it offline). None of
// them ships in this slice, which is why nothing in this release calls it in
// production — and why TestRoutingHasOneResolver fails if any other file starts
// comparing the canonical ID with a route's spelling: two derivations of the
// route are two answers to where a call goes, and the channel's whole point is
// that there is one.
//
//	route is RouteDirect, or the name of the provider entry the call is sent
//	      to.
//	sent  is the model ID to send: the canonical ID on the direct route, and
//	      on a provider route the ID that provider claims — the route's own
//	      spelling of the same model.
//	ok    is false when the switch asks for a route and there is none to
//	      take: the ID is named but no provider entry claims it, the one that
//	      does has no allow_data_to written, or more than one claims it (in
//	      which case CheckRouting refuses the switch at load anyway, and the
//	      resolver still fails closed rather than routing by provider name). A
//	      caller must not print or use a resolution it did not resolve, and
//	      must not quietly fall back to the direct route — the fall-through D1b
//	      exists to prevent.
//
// A model the switch does not name goes direct whatever via says: the list is
// the opt-in (D1a), so a Claude ID the operator did not write is untouched.
func RoutingClaudeFor(c *RoutingClaude, model string, providers map[string]ModelProvider) (route, sent string, ok bool) {
	switch {
	case c.ViaValue() == RouteDirect:
		return RouteDirect, model, true
	case !c.Names(model):
		return RouteDirect, model, true
	}
	claims := claudeClaimsFor(providers, model)
	if len(claims) != 1 || !claims[0].p.AllowDataToWritten() {
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
// between them: case, and "." where the canonical ID has "-". The catalogue's
// own map (phase 1b.2's canonical_id column, pricing.RouteIDFor) is the
// authority on the pair; until it lands this comparison is what locates the
// claim, and it can only ever *find* a claim the operator wrote: nothing here
// invents an ID, and a claim it does not recognise resolves to nothing, which
// is the fail-closed direction.
//
// The whole list, not the first: two entries claiming the same model under
// different spellings are an ambiguity the caller has to refuse (CheckRouting
// does, naming both), and picking one by sort order would answer a routing
// question with an alphabetical accident (the review of PR #414).
func claudeClaimsFor(providers map[string]ModelProvider, model string) []routeClaim {
	var out []routeClaim
	for _, n := range sortedKeys(providers) {
		pv := providers[n]
		for _, pat := range pv.Models {
			if tail := claudeRouteID(pat); tail != "" && sameClaudeID(tail, model) {
				out = append(out, routeClaim{name: n, claim: pat, p: pv})
				break
			}
		}
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
