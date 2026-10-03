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

// IsAlias reports whether a model name is something other than a pinned
// model ID: Claude Code's aliases (sonnet, opus, haiku, opusplan, default,
// best, ...), any "[1m]" suffix, a "-latest" pointer, a provider-prefixed
// name other than a plain "vendor/model" ID (a variant suffix included, a
// vendor that starts with "claude" because Claude goes direct under its own
// ID), an ID over 128 characters, or anything else that isn't spelled like an
// ID. With the budget on, roles must name an ID, so the price paid is the
// price in the table.
func IsAlias(model string) bool {
	if strings.HasSuffix(strings.ToLower(model), "[1m]") || strings.HasSuffix(model, "-latest") {
		return true
	}
	if len(model) > maxIDLen {
		return true
	}
	if strings.HasPrefix(model, "anthropic/") { // Claude goes direct, under its own ID
		return true
	}
	if strings.HasPrefix(model, "claude") && strings.Contains(model, "/") {
		return true
	}
	return !modelIDRE.MatchString(model) && !providerIDRE.MatchString(model)
}
