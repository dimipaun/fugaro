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
// (docs/design/project-centric.md §6.2). It is written by the operator — in
// the local config, or in the project layer — and only *delivered* to a run
// through the policy: a repository's fugaro.yaml has no routing key, exactly as
// it has no providers key.
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
	// does not name goes direct, and openrouter with an empty list is refused:
	// the opt-in is the list.
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
// canonical Claude IDs, never a pattern; a list that is not empty when via:
// openrouter; and, for every ID the list names, a provider entry that claims
// the route's own ID for it *and* has allow_data_to written.
func CheckRouting(r *Routing, providers map[string]ModelProvider, path string) []Problem {
	ps := CheckRoutingShape(r, path)
	if len(ps) > 0 || r == nil || r.Claude == nil || r.Claude.ViaValue() == RouteDirect {
		return ps
	}
	// D1b's safeguard, per ID the switch names: the route must exist (a
	// provider entry claims it) and that provider's allow_data_to must be
	// *written*.
	for i, m := range r.Claude.Models {
		at := fmt.Sprintf("%s.claude.models[%d]", path, i)
		name, _, p, claimed := claudeRouteFor(providers, m)
		switch {
		case !claimed:
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s is named by the switch but no provider entry claims it: a route entry lists the models it serves, and only the owner can add one, in the project config or the project layer — never in a repository's fugaro.yaml", m)})
		case !p.AllowDataToWritten():
			ps = append(ps, Problem{Path: at, Message: fmt.Sprintf("%s resolves to provider %s, whose allow_data_to is not written: name the repositories whose code may be sent there, or write [\"*\"] to mean every one of them on purpose (D1b; an absent allow_data_to already means every repository, which is why this one path requires it to be written)", m, name)})
		}
	}
	return ps
}

// CheckRoutingShape is the rule the routing object is held to wherever it is
// written or read: the via, and an explicit list of Claude IDs, never a
// pattern (D1a). It needs no providers, so a reader that does not hold them —
// the project layer, or a policy node being decoded — applies exactly the rule
// the operator's own config loader applies, and the three cannot drift.
//
// An absent block, and via: direct, are valid and route nothing (D1b). A list
// under via: direct is inert rather than refused: it names nothing that is
// routed, and D1b's default is that the direct route is where an unwritten
// switch leaves every call.
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
	if via == RouteOpenRouter && len(c.Models) == 0 {
		ps = append(ps, Problem{Path: path + ".claude.models", Message: "is empty, and an empty list with via: openrouter would opt every Claude model in: name the IDs this switch carries, one explicit Claude ID per entry, never a pattern (D1a, docs/design/mixed-credentials.md §4)"})
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
// used by the pins renderer, the price lookup and validate (Task 1b.1's
// interface), so the three cannot disagree about where a call goes.
//
//	route is RouteDirect, or the name of the provider entry the call is sent
//	      to.
//	sent  is the model ID to send: the canonical ID on the direct route, and
//	      on a provider route the ID that provider claims — the route's own
//	      spelling of the same model.
//	ok    is false when the switch asks for a route and there is none to
//	      take: the ID is named but no provider entry claims it, or the one
//	      that does has no allow_data_to written. A caller must not print or
//	      use a resolution it did not resolve, and must not quietly fall back
//	      to the direct route — the fall-through D1b exists to prevent.
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
	name, claim, p, claimed := claudeRouteFor(providers, model)
	if !claimed || !p.AllowDataToWritten() {
		return "", "", false
	}
	return name, claim, true
}

// claudeRouteFor is the provider entry that claims model's route ID: the entry
// whose models name an explicit anthropic/<id> for this model, and the claim
// itself (the ID to send).
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
func claudeRouteFor(providers map[string]ModelProvider, model string) (name, claim string, p ModelProvider, ok bool) {
	for _, n := range sortedKeys(providers) {
		pv := providers[n]
		for _, pat := range pv.Models {
			if tail := claudeRouteID(pat); tail != "" && sameClaudeID(tail, model) {
				return n, pat, pv, true
			}
		}
	}
	return "", "", ModelProvider{}, false
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
