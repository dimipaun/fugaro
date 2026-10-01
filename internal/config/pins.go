package config

import (
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

// CheckAllowed is the rule for a run under an allow-list of models: the
// coder, the reviewer and the background model must each be on it. Without a
// list (policy.Effective.HasAllowList) every model passes. A list that is set
// but empty denies everything, and a role that names no model is refused too,
// because the model the agent would pick is not known to be on the list. Each
// problem names the role, the model and the layer that set the list.
func CheckAllowed(a Agent, e policy.Effective) []Problem {
	if !e.HasAllowList() {
		return nil
	}
	list := "none"
	if len(e.AllowedModels) > 0 {
		list = strings.Join(e.AllowedModels, ", ")
	}
	where := "allowed_models from " + e.Sources[policy.KeyAllowedModels] + ": " + list
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
			ps = append(ps, Problem{Path: r.path, Message: r.model + " is not an allowed model (" + where + ")"})
		}
	}
	return ps
}
