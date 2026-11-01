package cli

import (
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// newModelsCmd is `fugaro models`: what a run of this project can name, where
// each model is served, what it costs and which repositories may use it
// (D794). Every subcommand here only reads, so the group runs anywhere, a
// coding agent's session included (spec §8); the writes that change a route or
// a price are their own commands.
func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Show the models this project can run, and where each is served",
		Long: `Show the models this project can name and the routes behind them.

The routes are the owner's providers in the project config, each with the
models it claims, its fee and the repositories whose code may be sent to it,
beside the direct Anthropic route. The models are the ones a run can name
today: an ID a route claims, a model_prices key, a budget.allowed_models entry
and a model a repository pins.

Both reads are offline: they touch no bucket, no database and no host. What
they print about the cloud is what the local files already say.`,
	}
	cmd.AddCommand(newModelsLsCmd(), newModelsSearchCmd())
	return cmd
}

// modelsRouteOf is the route a run would send this model down: the provider
// whose models claim it, the route routing.claude sends a Claude ID to, or
// config.RouteDirect for a Claude ID nothing routes. "" is a model no route
// serves, which a launch refuses (spec §2.2).
//
// It asks config.RoutingClaudeFor rather than reading the switch, so the
// answer here and the answer a launch acts on are the same resolver's (D1a,
// and the one-resolver rule that test guards).
func modelsRouteOf(lc *localcfg.Config, id string) string {
	if lc != nil {
		if name, _, ok := config.ProviderFor(lc.Providers, id); ok {
			return name
		}
		var claude *config.RoutingClaude
		if lc.Routing != nil {
			claude = lc.Routing.Claude
		}
		// via: auto and via: cascade resolve to the direct route (the
		// cascade picks a credential, not a route), so an ID under either is
		// shown direct, which is where the call goes.
		if route, _, ok := config.RoutingClaudeFor(claude, id, lc.Providers); ok && route != config.RouteDirect {
			return route
		}
	}
	if isClaudeModel(id) {
		return config.RouteDirect
	}
	return ""
}

// isClaudeModel reports whether id is a Claude model's ID, in either spelling a
// pin may carry: the canonical one (claude-sonnet-5-5) and a route's own
// (anthropic/claude-sonnet-5.5). It is the overlap test config.ClaudePatterns
// states, asked of the ID rather than of a provider's pattern, so a model no
// route claims is told apart from a model nothing serves at all — the second is
// refused at launch and the first goes to Anthropic.
func isClaudeModel(id string) bool {
	return slices.ContainsFunc(config.ClaudePatterns, func(p string) bool {
		return config.PatternsOverlap(id, p)
	})
}

// sortedProviderNames are the owner's route names, sorted: the order every
// surface lists them in, so two reads of one config cannot disagree.
func sortedProviderNames(providers map[string]config.ModelProvider) []string {
	return slices.Sorted(maps.Keys(providers))
}
