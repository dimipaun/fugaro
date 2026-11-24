package pricing

// The canonical-to-route map (design docs/project-centric/spec.md §6.2, plan
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
//
// The table itself is unexported: the pairs are reachable only through the
// functions below, so a consumer cannot read the data and re-answer the
// question its own way (the review finding on this change — a second derivation
// is the failure this map exists to remove, and a package boundary is the one
// check a second derivation cannot pass). TestRoutingResolutionHasOneSite and
// TestRoutingHasOneResolver pin the same rule from the source side.
//
// A model has a third spelling, and CS3 (docs/model-credentials-routing/
// implementation-plan.md) adds it here: Vertex's own, beside the route's ID.
// It is the same kind of fact — data, per model, checked in — and it lives with
// this map for the same reason, because the chain walk (CS1) has to know how to
// write a model on the source that will serve it, and one map answering "how is
// this model written there" is what keeps the walk, the checks (RT3) and the
// Vertex translation (CS4) from disagreeing about the id a live call carries.
//
// The column has since grown a second table rather than a second answer: a
// non-Claude model on Vertex — a Gemini one (GV3) — has no route ID and no
// Unmapped reason, so it gets no row of claudeRoutes, and vertex_gemini.go
// holds its spelling beside its price instead. vertexIDFor is still the one
// reader of the column, and it reads both tables.

// claudeRoute is one row of the map: the canonical Claude ID (the ID a pin
// carries, claude-sonnet-5-5), the route's own ID (what the route entry
// claims and what is sent and charged on it, anthropic/claude-sonnet-5.5), and
// Vertex's own spelling of the same model (claude-sonnet-5-5 for a model Vertex
// publishes dateless, claude-haiku-4-5@20251001 for one it dates).
//
// Exactly one of RouteID and Unmapped is set. A row with Unmapped is not an
// omission: it is the statement that this build has no measured route ID for
// that model, so routing.claude.models may not name it (the switch refuses to
// guess one), and it is what the refusal says.
//
// Vertex is a different statement from RouteID and is never empty. RouteID is a
// claim about a route — a third party's own ID for the model, which has to be
// MEASURED before this build may name it (D8's three) — while Vertex's spelling
// is a fact about the model's own ID where Vertex serves it: the price table
// already records both halves of it (table.go: the note that Vertex serves the
// dateless models under the same ID, and claude-haiku-4-5's "@" alias). A row
// with no Vertex spelling would be a model this build may not send to Vertex,
// which is what a spelling-less source is refused for (SpellingFor below,
// D3175).
type claudeRoute struct {
	Canonical string
	RouteID   string
	Unmapped  string
	Vertex    string
}

// noMeasuredRoute is the reason every unmapped row carries. The three route
// IDs this build knows are the measured Claude rows the price catalog's D8
// names (docs/provider-price-catalog/spec.md D8: anthropic/claude-sonnet-5.5,
// anthropic/claude-opus-5.5, anthropic/claude-fable-5.1); no other Claude model
// has one, and a spelling invented here would be a route nobody measured.
const noMeasuredRoute = "no route's own ID has been measured for it: this build knows the three the price catalog's D8 names (anthropic/claude-sonnet-5.5, anthropic/claude-opus-5.5, anthropic/claude-fable-5.1). The switch never guesses a route ID, so routing.claude.models may not name this model until a row for it is added here with the route's ID measured"

// claudeRoutes is the map, in canonical-ID order, and it is not part of this
// package's API: the pairs are readable through RouteIDFor and CanonicalFor
// alone. Every canonical Claude ID this build prices has a row
// (TestCanonicalMapIsExhaustive), and every canonical ID a shipped catalog row
// claims has one too.
//
// The Vertex member is the one column here that is not measured by this build's
// own runs: it is what Vertex's models overview publishes (the two facts the
// price table records), and Check 882 (scripts/live-checks/check-882-vertex-
// capped.sh) is what verifies it live, by serving a model this table routes to
// Vertex and reading whether Vertex answered. U5 in
// docs/model-credentials-routing/reconciliation.md is that open question, and a
// spelling measured differently is a one-line change here — which is the point
// of it being one column of one table rather than a rule somewhere.
var claudeRoutes = []claudeRoute{
	{Canonical: "claude-fable-5-1", RouteID: "anthropic/claude-fable-5.1", Vertex: "claude-fable-5-1"},
	{Canonical: "claude-haiku-4-5", Unmapped: noMeasuredRoute, Vertex: "claude-haiku-4-5@20251001"},
	{Canonical: "claude-opus-5", Unmapped: noMeasuredRoute, Vertex: "claude-opus-5"},
	{Canonical: "claude-opus-5-5", RouteID: "anthropic/claude-opus-5.5", Vertex: "claude-opus-5-5"},
	{Canonical: "claude-sonnet-5", Unmapped: noMeasuredRoute, Vertex: "claude-sonnet-5"},
	{Canonical: "claude-sonnet-5-5", RouteID: "anthropic/claude-sonnet-5.5", Vertex: "claude-sonnet-5-5"},
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
// (TestRoutingResolutionHasOneSite, which resolves the import rather than the
// text, so an aliased import is the same call).
func RouteIDFor(canonical, route string) (string, bool) {
	for _, r := range claudeRoutes {
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
// ID belongs to, for a surface that holds the route's ID alone and must name the
// model a person pinned. It answers a name, never a route — no caller can send
// or charge what it returns — so it is the one reading of this map that a
// consumer other than the resolver may make, and it makes exactly one: an
// allow-list admits a pin the switch routed by the canonical ID of the same
// model (config.CheckAllowed).
func CanonicalFor(route string) (string, bool) {
	if route == "" {
		return "", false
	}
	for _, r := range claudeRoutes {
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
	for _, r := range claudeRoutes {
		if r.Canonical == canonical {
			return r.Unmapped, r.Unmapped != ""
		}
	}
	return "", false
}

// vertexIDFor answers Vertex's own spelling of a canonical model ID, and
// whether Vertex may be sent the model at all. It is the one reader of both
// halves of the spelling: a Claude row's Vertex member, and — for a model the
// Claude map does not carry, which is every Gemini model — the Vertex Gemini
// catalog (vertex_gemini.go, GV3), so a caller cannot get a Vertex spelling out
// of one half that the other contradicts.
//
// The split is a question of which table answers, never of which is consulted
// first as a preference: a model the Claude map carries is a Claude model and
// its row is the answer, and one the Claude map does not carry falls to the
// Gemini catalog, which has no row for a Claude model. The second half of the
// answer is the fail-closed direction in both — a model neither table carries
// has no Vertex spelling, and a caller that took the canonical ID anyway would
// send a call to a model nobody checked Vertex for (D3175).
func vertexIDFor(canonical string) (string, bool) {
	for _, r := range claudeRoutes {
		if r.Canonical == canonical {
			return r.Vertex, r.Vertex != ""
		}
	}
	return vertexGeminiID(canonical)
}

// spellingFor is the real translation behind SpellingFor (spelling_stub.go
// declares the variable and holds the "no spelling" stub until I1 moves both
// declarations here): the spelling a source of kind needs for model, and
// whether that source may serve it at all (D3175, docs/model-credentials-
// routing/reconciliation.md).
//
// The five kinds split in two, and the split is the whole of this function:
//
//   - anthropic, pool, launcher-pool: the source carries the call to the Claude
//     API, and the Claude API's own spelling of a Claude model is the ID a pin
//     carries — the ID the agent asked with. There is nothing to translate and
//     nothing to look up, so the answer is the model itself, for any model at
//     all. Whether the source reaches that model is a different question, and
//     not this map's: a project's Anthropic key and a pool's accounts serve
//     whatever the account serves, which validation asks (RT3's reach rule).
//   - provider, vertex: the source writes the model its own way and only the
//     map knows how — a provider's route ID (RouteIDFor), Vertex's own spelling
//     (vertexIDFor). A model the map does not spell for that kind is answered
//     ("", false), and a caller must not fall back to the ID the agent asked
//     with: that is a call sent to a model nobody measured on that source,
//     which is exactly the guess this map exists to remove.
//
// The kinds are internal/routing's Kind values, as strings because
// internal/routing reads SpellingFor and a routing.Kind parameter would make
// the two packages import each other (spelling_stub.go's own note).
// TestEveryKindIsSpelled (internal/credsvc, which may import both) holds these
// five to routing.Kinds, so a sixth kind added there without a branch here is a
// failing test rather than a silent "no spelling".
func spellingFor(kind, model string) (string, bool) {
	if model == "" {
		return "", false
	}
	switch kind {
	case "anthropic", "pool", "launcher-pool":
		return model, true
	case "provider":
		return RouteIDFor(model, "")
	case "vertex":
		return vertexIDFor(model)
	}
	return "", false
}

// The real assignment, and the file it sits in is what makes it the one that
// stands: SpellingFor is declared in spelling_stub.go, whose own init keeps the
// stub only while nothing has assigned the variable, and the toolchain presents
// a package's files in name order — so canonical.go sorts before spelling_stub.go.
// I1 deletes the stub and moves the declaration here, at which point this init
// becomes a plain assignment.
func init() {
	SpellingFor = spellingFor
}
