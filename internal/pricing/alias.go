package pricing

import (
	"regexp"
	"strings"
)

// modelIDRE is a model ID as the Claude API or Vertex spells it:
// "claude-" and lowercase dash-separated words, with an optional Vertex
// "@YYYYMMDD" snapshot suffix.
var modelIDRE = regexp.MustCompile(`^claude-[a-z0-9]+(?:-[a-z0-9]+)*(?:@[0-9]{8})?$`)

// IsAlias reports whether a model name is something other than a pinned
// model ID: Claude Code's aliases (sonnet, opus, haiku, opusplan, default,
// best, ...), any "[1m]" suffix, a "-latest" pointer, a provider-prefixed
// name, or anything else that isn't spelled like an ID. With the budget
// on, roles must name an ID, so the price paid is the price in the table.
func IsAlias(model string) bool {
	if strings.HasSuffix(strings.ToLower(model), "[1m]") || strings.HasSuffix(model, "-latest") {
		return true
	}
	return !modelIDRE.MatchString(model)
}
