package config

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// CheckPins is the rule for a run under a budget: the coder, the reviewer and
// the background model each name an explicit model ID that prices knows, never
// an alias, so the price charged is the price in the table. prices is the
// embedded table with the owner's overrides.
func CheckPins(a Agent, prices *pricing.Table) []Problem {
	var ps []Problem
	for _, r := range []struct{ path, model string }{
		{"agent.models.coder", a.ModelFor(RoleCoder)},
		{"agent.models.reviewer", a.ModelFor(RoleReviewer)},
		{"agent.models.background", a.Models.Background},
	} {
		switch {
		case r.model == "" && r.path == "agent.models.background":
			ps = append(ps, Problem{Path: r.path, Message: "is required with a budget: name a model ID such as claude-haiku-4-5"})
		case r.model == "":
			ps = append(ps, Problem{Path: r.path, Message: "is required with a budget: set it, or agent.model, to a model ID such as claude-sonnet-5-5"})
		case pricing.IsAlias(r.model):
			ps = append(ps, Problem{Path: r.path, Message: r.model + " is an alias: with a budget, name a model ID such as claude-sonnet-5-5"})
		default:
			if _, ok := prices.Lookup(r.model); !ok {
				ps = append(ps, Problem{Path: r.path, Message: r.model + " has no price: add it under model_prices in the project config"})
			}
		}
	}
	return ps
}

// PinWarnings are the pins whose price is an embedded placeholder nobody has
// verified (a provider model's starter row), or whose owner override left
// cache rates at 0 (counted at the input price instead): the run may go on,
// but its cap counts a guess until the owner sets the real price under
// model_prices, which always wins. Callers show these at pin time and in
// diagnose.
func PinWarnings(a Agent, prices *pricing.Table) []Problem {
	var ps []Problem
	seen := map[string]bool{}
	for _, r := range []struct{ path, model string }{
		{"agent.models.coder", a.ModelFor(RoleCoder)},
		{"agent.models.reviewer", a.ModelFor(RoleReviewer)},
		{"agent.models.background", a.Models.Background},
	} {
		m, ok := prices.Lookup(r.model)
		if !ok || seen[m.ID+r.path] {
			continue
		}
		seen[m.ID+r.path] = true
		if m.Unverified {
			ps = append(ps, Problem{Path: r.path, Code: CodeUnverifiedPrice, Message: fmt.Sprintf(
				"%s has an unverified placeholder price (%s, %s): set the real one under model_prices; until then the cap counts a guess",
				r.model, m.PriceSource, m.PriceCheckedAt)})
		}
		if w := m.CacheWarning(); w != "" {
			ps = append(ps, Problem{Path: r.path, Code: pricing.CodeCacheRateDefaulted, Message: w})
		}
	}
	return ps
}

// CodeUnverifiedPrice marks a PinWarnings problem.
const CodeUnverifiedPrice = "unverified_price"

// CheckAllowed is the rule for a run under an allow-list of models: the
// coder, the reviewer and the background model must each be on it. Without a
// list (policy.Effective.HasAllowList) every model passes. A list that is set
// but empty denies everything, and a role that names no model is refused too,
// because the model the agent would pick is not known to be on the list. Each
// problem names the role, the model and the layer that set the list; when
// that layer is not the ceiling and the ceiling has a list of its own
// (ceiling, nil when it has none), the message gives that list too, so an
// empty intersection can be understood.
func CheckAllowed(a Agent, e policy.Effective, ceiling []string) []Problem {
	if !e.HasAllowList() {
		return nil
	}
	list := "none"
	if len(e.AllowedModels) > 0 {
		list = strings.Join(e.AllowedModels, ", ")
	}
	src := e.Sources[policy.KeyAllowedModels]
	where := "allowed_models from " + src + ": " + list
	if src != policy.SourceCeiling && ceiling != nil {
		where += "; the project's allow-list: " + strings.Join(ceiling, ", ")
	}
	var ps []Problem
	for _, r := range []struct{ path, model string }{
		{"agent.models.coder", a.ModelFor(RoleCoder)},
		{"agent.models.reviewer", a.ModelFor(RoleReviewer)},
		{"agent.models.background", a.Models.Background},
	} {
		switch {
		case r.model == "":
			ps = append(ps, Problem{Path: r.path, Message: "names no model, but the run has an allow-list (" + where + "): set it, or agent.model, to a model on the list"})
		case !e.Allows(r.model):
			ps = append(ps, Problem{Path: r.path, Message: CodeSpan(r.model) + " is not an allowed model (" + where + ")"})
		}
	}
	return ps
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
