package pricing

// The canonical-to-route map (design docs/design/project-centric.md §6.2, plan
// Task 1b.2). A Claude model has a different ID per route: the canonical ID a
// pin carries and the price table keys (claude-sonnet-5-5), and the route's own
// spelling of the same model (anthropic/claude-sonnet-5.5). The gateway never
// rewrites a request body, so nothing translates between them unless something
// here does, and the translation has to be *data*: a guess is a wrong route and
// a wrong price.
//
// So the map is checked in, one row per canonical Claude ID, and it is
// exhaustive over the Claude IDs this build can route: a row either carries the
// route's own ID or says why it does not. Nothing derives a spelling — not a
// case fold, not a separator swap — because two spellings that "look like" one
// model are two answers to where a call goes and what it costs.

// ClaudeRoute is one row of the map: the canonical Claude ID (the ID a pin
// carries, claude-sonnet-5-5) and the route's own ID (what the route entry
// claims and what is sent and charged on it, anthropic/claude-sonnet-5.5).
//
// Exactly one of RouteID and Unmapped is set. A row with Unmapped is not an
// omission: it is the statement that this build has no measured route ID for
// that model, so routing.claude.models may not name it (the switch refuses to
// guess one), and it is what the refusal says.
type ClaudeRoute struct {
	Canonical string
	RouteID   string
	Unmapped  string
}

// noMeasuredRoute is the reason every unmapped row carries. The three route
// IDs this build knows are the measured Claude rows the price catalog's D8
// names (docs/design/provider-price-catalog.md D8: anthropic/claude-sonnet-5.5,
// anthropic/claude-opus-5.5, anthropic/claude-fable-5.1); no other Claude model
// has one, and a spelling invented here would be a route nobody measured.
const noMeasuredRoute = "no route's own ID has been measured for it: this build knows the three the price catalog's D8 names (anthropic/claude-sonnet-5.5, anthropic/claude-opus-5.5, anthropic/claude-fable-5.1). The switch never guesses a route ID, so routing.claude.models may not name this model until a row for it is added here with the route's ID measured"

// ClaudeRoutes is the map, in canonical-ID order. Every canonical Claude ID
// this build prices has a row (TestCanonicalMapIsExhaustive), and every
// canonical ID a shipped catalog row claims has one too.
var ClaudeRoutes = []ClaudeRoute{
	{Canonical: "claude-fable-5-1", RouteID: "anthropic/claude-fable-5.1"},
	{Canonical: "claude-haiku-4-5", Unmapped: noMeasuredRoute},
	{Canonical: "claude-opus-5", Unmapped: noMeasuredRoute},
	{Canonical: "claude-opus-5-5", RouteID: "anthropic/claude-opus-5.5"},
	{Canonical: "claude-sonnet-5", Unmapped: noMeasuredRoute},
	{Canonical: "claude-sonnet-5-5", RouteID: "anthropic/claude-sonnet-5.5"},
}

// RouteIDFor answers the route's own ID for a canonical Claude ID. It is the
// one translation from a pin's model to what a route is sent and charged, and
// the answer is only ever a checked-in pair:
//
//	route ""            asks for the map's own ID for canonical: the pins
//	                    renderer's question ("what do I send on this route?").
//	route non-empty     asks whether canonical and route are that pair: the
//	                    question a caller holding a route entry's claim asks
//	                    before it sends or charges anything.
//
// (id, true) is the route's ID in both cases. ("", false) is every other case:
// an ID the map does not carry, a row with an explicit Unmapped reason, and a
// spelling that is not the pair the map holds. A caller must not invent an ID
// from the answer it did not get, and must not fall back to the canonical ID
// on a route: the fail-closed direction is the whole point of a map.
//
// RouteIDFor is called from internal/config's resolver (RoutingClaudeFor) and
// nowhere else: one place answers where a Claude call goes
// (TestRoutingResolutionHasOneSite).
func RouteIDFor(canonical, route string) (string, bool) {
	for _, r := range ClaudeRoutes {
		if r.Canonical != canonical || r.RouteID == "" {
			continue
		}
		if route == "" || route == r.RouteID {
			return r.RouteID, true
		}
		return "", false
	}
	return "", false
}

// CanonicalFor is RouteIDFor's inverse: the canonical Claude ID a route's own
// ID belongs to, for the surfaces that hold the route's ID alone (a price row
// keyed anthropic/claude-sonnet-5.5, a log line's serving_model) and must name
// the model a person pinned.
func CanonicalFor(route string) (string, bool) {
	if route == "" {
		return "", false
	}
	for _, r := range ClaudeRoutes {
		if r.RouteID == route {
			return r.Canonical, true
		}
	}
	return "", false
}

// UnmappedReason is why canonical has no route ID, and whether the map carries
// a row for it at all: (reason, true) for a row with Unmapped set, and ("",
// false) for a row that has a route ID or is not in the map. It exists so a
// refusal can say what the map says instead of "no route", which reads as an
// oversight rather than a decision.
func UnmappedReason(canonical string) (string, bool) {
	for _, r := range ClaudeRoutes {
		if r.Canonical == canonical {
			return r.Unmapped, r.Unmapped != ""
		}
	}
	return "", false
}
