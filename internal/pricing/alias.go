package pricing

import (
	"regexp"
	"strings"
)

// modelIDRE is a model ID as the Claude API or Vertex spells it:
// "claude-" and lowercase dash-separated words, with an optional Vertex
// "@YYYYMMDD" snapshot suffix.
var modelIDRE = regexp.MustCompile(`^claude-[a-z0-9]+(?:-[a-z0-9]+)*(?:@[0-9]{8})?$`)

// providerIDRE is a provider's model ID ("deepseek/deepseek-v4-flash"): a
// vendor and a model name, lowercase. A ":" variant suffix (":free",
// ":online") is not part of it: a variant is another price and routing.
var providerIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*/[a-z0-9][a-z0-9._-]*$`)

// maxIDLen bounds a model ID: nothing real is longer, and an ID travels in
// an environment variable and a log line.
const maxIDLen = 128

// IsProviderModelID reports whether m is a plain "vendor/model" ID: the only
// shape a price key or a route claim may take. It is the grammar alone
// (providerIDRE) and carries no length bound of its own -- IsAlias's
// 128-character limit and CheckModelPattern's 100 are theirs.
func IsProviderModelID(m string) bool { return providerIDRE.MatchString(m) }

// IsAlias reports whether a model name is something other than a pinned
// model ID: Claude Code's aliases (sonnet, opus, haiku, opusplan, default,
// best, ...), any "[1m]" suffix, a "-latest" pointer, a provider-prefixed
// name other than a plain "vendor/model" ID (a variant suffix included, a
// vendor that starts with "claude" because Claude goes direct under its own
// ID), an ID over 128 characters, or anything else that isn't spelled like an
// ID. With the budget on, roles must name an ID, so the price paid is the
// price in the table.
//
// Claude models go direct to Anthropic, so the "anthropic/" vendor prefix is
// an alias too -- with one exception: an explicit, well-formed provider model
// ID under that prefix ("anthropic/claude-sonnet-5.5") is a real model ID,
// because it is the one shape a provider route may claim by naming it
// (design docs/design/mixed-credentials.md §4). Whether a route actually
// claims it is not this package's question: the price table keys what a pin
// may name, and who may route it is decided where the routes are
// (internal/config.CheckProviderPolicy). A wildcard ("anthropic/*"), a
// variant, a "[1m]" spelling or a name that is not a well-formed ID stays an
// alias -- which is the last check's doing: providerIDRE is the whole of the
// exception, so what it does not match is an alias without a branch of its
// own saying so.
func IsAlias(model string) bool {
	if strings.HasSuffix(strings.ToLower(model), "[1m]") || strings.HasSuffix(model, "-latest") {
		return true
	}
	if len(model) > maxIDLen {
		return true
	}
	if strings.HasPrefix(model, "claude") && strings.Contains(model, "/") {
		return true
	}
	// The fallback is also where "anthropic/<id>" is judged: for a model under
	// that prefix, modelIDRE can never match, so this is exactly "not a
	// provider model ID" -- the same predicate a branch of its own used to
	// spell out twice (removed in the 2026-10-15 review). It is not a wall
	// that could ever be a second one: if providerIDRE were widened to admit a
	// wildcard, that branch and this check would widen together.
	return !modelIDRE.MatchString(model) && !providerIDRE.MatchString(model)
}
