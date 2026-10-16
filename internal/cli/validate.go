package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner"
)

// anthropicKeyNeeded is the offline note an enforcing run with a pin that goes
// direct to Anthropic gets: it needs a stored key, and only the cloud knows
// whether the secret has a version yet, so this is a note for the owner and
// never a refusal (design docs/design/project-centric.md §6.1; the gateway
// refuses such a call, by name, when no key is stored).
const anthropicKeyNeeded = "needs a stored Anthropic API key: a pinned model goes direct to Anthropic, and the job mounts the key whenever the anthropic-api-key secret has a version (store one with `fugaro secrets set anthropic-api-key` and rerun `fugaro init --repo` if none does), or route the model through a provider"

// anthropicKeyNoEnforce is the same note when the budget isn't enforcing.
// Only enforce routes a call to a provider (config.CheckProviderGateway
// refuses a provider model in any other mode), so every call goes to Anthropic
// whatever the pins are (config.AnthropicKeyNeeded): under observe the gateway
// exists and accounts for the spend but never routes to a provider, and under
// off there is no gateway at all. The sharper fix, too: with no route to a
// provider there is nothing to route around.
const anthropicKeyNoEnforce = "needs a stored Anthropic API key: the budget isn't enforcing, so nothing routes a call to a provider (observing only accounts for the spend, and off has no gateway at all), and every call goes to Anthropic whatever the pins are (store one with `fugaro secrets set anthropic-api-key` and rerun `fugaro init --repo`)"

// needsAnthropicKey is config.AnthropicKeyNeeded over the effective mode --
// the project's ceiling merged with the file, exactly as the runner reads it.
// The one place the CLI decides it, so init's hidden prompt, init --repo's
// "still missing" list and validate's note cannot disagree.
//
// The routes are the run's own (layerRoutes, over the layer this file resolved
// over): the project layer may claim the very models the pins name, and in the
// cloud those calls go to the provider, so init must not ask for an Anthropic
// key such a run never uses. A layer whose claims the rules refuse changes
// nothing here (layerRoutes answers with lc.Providers): the run would not start
// either, and the key is then asked for rather than promised unnecessary.
func needsAnthropicKey(lc *localcfg.Config, cfg *config.Config, repo string) bool {
	mode := policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode
	var layer *config.ProjectLayer
	if cfg != nil {
		layer = cfg.Layer
	}
	providers, _ := layerRoutes(lc, layer)
	return config.AnthropicKeyNeeded(cfg.Agent, providers, repo, mode)
}

// ceilingLayer is the owner's ceiling as the runner builds it from the job
// environment init --repo sets from the project config: an "off" mode is no
// mode at all.
func ceilingLayer(lc *localcfg.Config) policy.Layer {
	var l policy.Layer
	if b := lc.Budget; b != nil {
		l.PerRunUSD, l.MaxRunTokens, l.AllowedModels = b.PerRunUSD, b.MaxRunTokens, b.AllowedModels
	}
	if m := lc.BudgetMode(); m != localcfg.BudgetOff {
		l.Mode = m
	}
	return l
}

// clampWarning says in words what the runner will do with one value of the
// file that is looser than what sits above it.
func clampWarning(ig policy.Ignored) config.Problem {
	var msg string
	switch ig.Key {
	case policy.KeyPerRunUSD, policy.KeyMaxRunTokens:
		msg = fmt.Sprintf("%s is above the project's ceiling of %s; the runner will use %s", ig.Value, ig.Effective, ig.Effective)
	case policy.KeyMode:
		msg = fmt.Sprintf("%s is looser than the project's %s; the runner will use %s", ig.Value, ig.Effective, ig.Effective)
	case policy.KeyAllowedModels:
		msg = fmt.Sprintf("[%s] names models outside the project's allow-list; the runner will use [%s]",
			strings.ReplaceAll(ig.Value, ",", ", "), strings.ReplaceAll(ig.Effective, ",", ", "))
	default:
		msg = fmt.Sprintf("%s is looser than %s; the runner will use %s", ig.Value, ig.Effective, ig.Effective)
	}
	return config.Problem{Path: runner.PolicyKeyPath(ig.Key), Message: msg}
}

// ceilingText is the ceiling in one line, for init --repo.
func ceilingText(l policy.Layer) string {
	var parts []string
	if l.PerRunUSD > 0 {
		parts = append(parts, "per_run_usd "+strconv.FormatFloat(l.PerRunUSD, 'f', -1, 64))
	}
	if l.Mode != "" {
		parts = append(parts, "mode "+l.Mode)
	}
	if l.MaxRunTokens > 0 {
		parts = append(parts, "max_run_tokens "+strconv.FormatInt(l.MaxRunTokens, 10))
	}
	if l.AllowedModels != nil {
		parts = append(parts, "allowed_models ["+strings.Join(l.AllowedModels, ", ")+"]")
	}
	if len(parts) == 0 {
		return "none (no cap, mode off, no token cap, any model)"
	}
	return strings.Join(parts, ", ")
}

// budgetProblems are the rules a repository under a budget must meet, when
// the project config selected for this command is cfg's own project: the
// project config is the ceiling, the file in hand the tightening layer
// (policy.Merge, the runner's own merge, so the two can't disagree). Every
// model must be on the allow-list; with the effective mode on, every model is
// an explicit priced ID and Vertex isn't under enforce. A key the file would
// loosen is a warning, as is an enforce with no cap anywhere (the run would
// halt no_cap). With no project config, or one for another project, only the
// file's shape (checked by config.Check) applies.
// claimed is the route table the run will have (layerRoutes): lc.Providers
// with the checkout layer's claims merged in. The *prices* stay lc.Providers'
// — the claim-blind table the run's environment builds — so a claimed ID is
// priced by the owner's model_prices and never by the catalog here either.
func budgetProblems(cfg *config.Config, lc *localcfg.Config, claimed map[string]config.ModelProvider) (problems, warnings []config.Problem) {
	// The day cap is the runner's to enforce; the database never sees it.
	if cfg.Budget != nil && cfg.Budget.PerDayUSD > 0 {
		warnings = append(warnings, config.Problem{Path: "budget.per_day_usd",
			Message: "per_day_usd is enforced by the runner against this repository's day counter only while the project has the shared budget (init --firebase, mode observe or enforce); on a project without it nothing enforces it. A lower RTDB cap wins"})
	}
	if lc == nil || lc.Name != cfg.Project {
		// No ceiling to compare with, but a committed enforce is the
		// effective mode whatever the ceiling says (the stricter layer
		// wins), so Vertex under it is refused on every run.
		if cfg.Agent.Auth == "vertex" && cfg.Budget != nil && cfg.Budget.Mode == policy.ModeEnforce {
			problems = append(problems, config.Problem{Path: "agent.auth", Message: vertexBudgetRefusal})
		}
		return problems, warnings
	}
	// The runner's default background model (a provider coder's own) is the
	// run's, so the pin rules judge it, not an unset one.
	agentPins := cfg.Agent
	agentPins.Models.Background = config.EffectiveBackground(agentPins, claimed)
	e := policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg))
	for _, ig := range e.Ignored {
		warnings = append(warnings, clampWarning(ig))
	}
	// The merge calls the file in hand the default branch's; here it is the
	// file itself.
	ef := e
	ef.Sources = map[string]string{}
	for k, v := range e.Sources {
		if v == policy.SourceDefaultBranch {
			v = "this file"
		}
		ef.Sources[k] = v
	}
	// The switch and the routes the run will have: validate resolves a route ID
	// the same way the run does, so a pin that names one is admitted here
	// exactly when the run would route it (config.CheckAllowed's `allowed`).
	// The switch is the operator's own — the policy node is published from it,
	// and this command reads no node (design §11: offline) — which is the same
	// reading the rest of this function gives the providers.
	allowed := config.CheckAllowed(agentPins, ef, ceilingLayer(lc).AllowedModels, lc.Routing, claimed)
	problems = append(problems, allowed...)
	// A role with no model already has its problem: not a second one, from
	// the pins, saying much the same.
	named := map[string]bool{}
	for _, p := range allowed {
		named[p.Path] = true
	}
	if cfg.Agent.Auth == "vertex" && e.Mode == policy.ModeEnforce {
		problems = append(problems, config.Problem{Path: "agent.auth", Message: vertexBudgetRefusal})
	}
	if e.Mode == "" || e.Mode == policy.ModeOff {
		return problems, warnings
	}
	o, err := lc.Overrides()
	if err != nil {
		// The prices are unreadable, so a model "without a price" would be
		// the table's fault, not the file's: say only what is wrong.
		problems = append(problems, config.Problem{Path: "model_prices", Message: err.Error() + " (in the project config)"})
	} else if prices, err := priceTableFor(lc.Providers, o, budgetNow()); err != nil {
		// The routes cannot be priced — a malformed catalog, or a
		// require_zdr route on a model with no ZDR-eligible endpoint. The
		// message is the fix path, and no rule below may judge a table
		// that was refused (the run would not start).
		problems = append(problems, config.Problem{Path: "providers", Message: err.Error() + " (in the project config)"})
	} else if cfg.Agent.Auth != "oauth" {
		// An oauth run has no gateway: nothing is priced or pinned.
		for _, p := range config.CheckPins(agentPins, prices) {
			if !named[p.Path] {
				problems = append(problems, p)
			}
		}
	}
	if cfg.Agent.Auth == "api-key" && e.Mode == policy.ModeEnforce && e.PerRunUSD <= 0 {
		// The database's caps are what the rules compare against (D6): a
		// per-run cap set anywhere in it bounds the run, and only a
		// database with none on any of the three sides halts it.
		warnings = append(warnings, config.Problem{Path: "budget.per_run_usd",
			Message: "the mode is enforce but no per-run cap is set in the project config or here; the database's effective per-run cap still bounds the run when one is set anywhere (the repository's own, the default for repositories without one, or the project's global one), and the run halts (no_cap) only when all three are unset"})
	}
	return problems, warnings
}

// providerProblems are the rules for models another provider serves, with the
// owner's providers from the project config when it is cfg's own project: the
// runner's refusals (a model no provider claims, a repository not in
// allow_data_to, a variant or alias, no price, auth other than api-key), and
// the warnings of the pins' prices (unverified placeholders, defaulted cache
// rates). The repository is the checkout's origin; without one the
// allow_data_to rule can't be checked, and a warning says so.
// claimed is the route table the run will have (layerRoutes); the price table
// is lc.Providers' own, exactly as budgetProblems builds it and as the run's
// environment does.
func providerProblems(ctx context.Context, cfg *config.Config, lc *localcfg.Config, claimed map[string]config.ModelProvider) (problems, warnings, notes []config.Problem) {
	if lc == nil || lc.Name != cfg.Project {
		return nil, nil, nil
	}
	o, err := lc.Overrides()
	if err != nil {
		return nil, nil, nil // budgetProblems says so
	}
	prices, err := priceTableFor(lc.Providers, o, budgetNow())
	if err != nil {
		// A refused table (a malformed catalog, or a route the switches
		// cannot price) is reported by budgetProblems when the budget is
		// on; with the mode off it never builds one, so report it here.
		if m := policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode; m == "" || m == policy.ModeOff {
			problems = append(problems, config.Problem{Path: "providers", Message: err.Error() + " (in the project config)"})
		}
		// Without a table the price rule is skipped rather than judged on
		// another table: a model with "no price" would be this refusal's
		// fault, not the file's.
		prices = nil
	}
	var allowed []string
	if cfg.Budget != nil {
		allowed = cfg.Budget.AllowedModels
	}
	repo, rerr := originRepo(ctx)
	if rerr == nil {
		problems = append(problems, config.CheckProviderPolicy(cfg.Agent, allowed, repo, claimed, prices)...)
	} else {
		repo = "" // an unknown repository: no provider may serve it
		for _, p := range config.CheckProviderPolicy(cfg.Agent, allowed, "", claimed, prices) {
			// Without the repository only the rules that don't name it hold.
			if !strings.Contains(p.Message, "allow_data_to") {
				problems = append(problems, p)
			}
		}
		if len(claimed) > 0 {
			warnings = append(warnings, config.Problem{Path: "providers", Message: "the repository is unknown here (no origin remote), so allow_data_to was not checked"})
		}
	}
	// Every provider that is open to every repository is named, whatever
	// the repository: before this design an absent allow_data_to meant
	// nobody, and a config carried over from then now mounts the key into
	// every repository's job and opens the data policy to all of them. A
	// warning, never a problem: the owner's ruling makes it valid, and
	// ["*"] is the way to keep it on purpose.
	for _, name := range config.OpenProviders(claimed) {
		warnings = append(warnings, config.Problem{Path: "providers." + name, Message: claimed[name].OpenToEveryRepoReason()})
	}
	problems = append(problems, config.CheckProviderAuth(cfg.Agent, claimed)...)
	agent := cfg.Agent
	agent.Models.Background = config.EffectiveBackground(agent, claimed)
	mode := policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode
	problems = append(problems, config.CheckProviderGateway(agent, agent.Models.Background, claimed, mode)...)
	// The run needs the Anthropic API key when a call goes to Anthropic:
	// config.AnthropicKeyNeeded, the rule init asks too, over the same
	// effective mode -- so a mode that isn't enforcing says so here as well,
	// since only enforce routes a call to a provider. That the secret
	// has a version is a fact only the cloud knows, and whether one is
	// stored is another, so this is a note, never a warning or a refusal:
	// the gateway refuses such a call by name when no key is stored (design
	// docs/design/project-centric.md §6.1).
	if config.AnthropicKeyNeeded(cfg.Agent, claimed, repo, mode) {
		msg := anthropicKeyNeeded
		if mode != policy.ModeEnforce {
			// The sharper reason, and the sharper fix: with no route to a
			// provider there is nothing to route around.
			msg = anthropicKeyNoEnforce
		}
		notes = append(notes, config.Problem{Path: "agent.auth", Message: msg})
	}
	if cfg.Agent.Auth != "oauth" {
		warnings = append(warnings, config.PinWarnings(agent, prices)...)
	}
	return problems, warnings, notes
}

// claudePin is one of the run's three pins whose model is a canonical Claude
// ID: the pins the routing switch applies to, because D1a's list is a list of
// exactly those IDs.
type claudePin struct{ path, model string }

// claudePins are the run's Claude pins, in the pin order config's own rules
// use. A pin that is not a canonical Claude ID — a provider's own model, a
// route's ID, an alias, nothing at all — is not one: those have their own
// rules (providerProblems, CheckAllowed) and the switch never names them.
func claudePins(a config.Agent, claimed map[string]config.ModelProvider) []claudePin {
	// The runner's default background model (a provider coder's own) is the
	// run's, exactly as budgetProblems judges it.
	a.Models.Background = config.EffectiveBackground(a, claimed)
	var out []claudePin
	for i, path := range [...]string{"agent.models.coder", "agent.models.reviewer", "agent.models.background"} {
		// The same three paths config's own pin rules name (internal/config's
		// pinPaths, which the pin order of PinnedModels is defined against), so
		// a problem here points at the key the owner edits.
		if m := a.PinnedModels()[i]; config.CheckRoutingModel(m) == "" {
			out = append(out, claudePin{path: path, model: m})
		}
	}
	return out
}

// routingProblems are plan task 1b.3's two routing rules: the resolution the
// switch licenses, printed when this command can read it, and the refusal of a
// Claude pin that a project routing Claude through a provider leaves on the
// direct route.
//
// The resolution is printed only when the switch is readable, and the switch is
// the operator's own project config: validate reads no policy node (design §11:
// offline), which is the same reading budgetProblems and providerProblems give
// the providers. With no config for this file's project there is nothing to
// resolve from, and the line says "unresolved offline" — a resolution this
// command did not resolve is never printed as if it had been (the resolver's
// own contract, config.RoutingClaudeFor). A model the switch names and cannot
// resolve is a problem where every run of this configuration reaches the switch
// — the run halts with "the project's routing switch is not usable", never
// falling back to direct (D1b's safeguard and the switch's own route) — and a
// note everywhere else: routing is the gateway's, via: auto never reaches a
// Vertex run, and a stored Anthropic API key turns it back to the direct path
// (routingReachOf, the runner's own decision; D11, design §6.2).
//
// A resolution is printed with what the run does with it, and the rules that
// judge the pins the switch produced are the run's own as well: a routed pin
// under an observing budget is refused by the gateway rule (the resolution is
// the first half of a halt, not the whole of a valid configuration).
//
// The refusal is design §6.2's safeguard read from the offline side. A project
// that routes Claude through a provider is a project with no Anthropic key to
// fall back on (docs/design/mixed-credentials.md §1: an OpenRouter key and
// subscription OAuth tokens, no Anthropic one), and a Claude pin the switch
// leaves on the direct route is a call nothing can make — the gateway refuses
// it by name when no key is stored (config.MissingAnthropicKey). That a key
// version exists is a fact only the cloud knows, so this refuses the state
// validate *can* read offline: an api-key run under an enforcing budget, whose
// Claude call therefore goes to Anthropic (config.AnthropicKeyNeeded), and a
// route of the owner's that claims this model with allow_data_to written — the
// resolver answers whether it would take the call. A Claude pin nothing claims
// keeps the note it always was: there is no route to send it to, and "write the
// switch" would be advice this project's own providers cannot take.
//
// That route is looked for in lc.Providers, the operator's own table, and never
// in the checkout layer's claude_models claims (which the resolution above does
// read: those are the routes the run will have). A claim in the layer is a
// repository asking for a model to be served by a route the owner defines — the
// interim path the switch does not replace (D1a) — and it is not the owner
// saying this project has no Anthropic key; the owner's own route table is.
func routingProblems(ctx context.Context, cfg *config.Config, lc *localcfg.Config, claimed map[string]config.ModelProvider) (problems, notes []config.Problem) {
	pins := claudePins(cfg.Agent, claimed)
	if len(pins) == 0 {
		return nil, nil
	}
	readable := lc != nil && lc.Name == cfg.Project
	// The mode is the run's own (policy.Merge), as budgetProblems computes it:
	// only an enforcing budget puts a gateway in front of the call, and routing
	// is the gateway's.
	mode := ""
	if readable {
		mode = policy.Merge(ceilingLayer(lc), runner.FileLayer(cfg)).Mode
	}
	repo, rerr := originRepo(ctx)
	if rerr != nil {
		repo = "" // an unknown repository: no provider's allow_data_to can be said to cover it
	}
	// The routes a run of this checkout carries, which is the table the switch
	// resolves over: the run's own (claimed — the operator's providers with the
	// layer's claude_models claims), filtered by allow_data_to for this
	// repository, exactly as runner.ProvidersEnv writes FUGARO_MODEL_PROVIDERS
	// (runner.ProvidersFor, the same filter doctor's check reads). Resolving
	// over the unfiltered table printed a route this run does not have — the
	// run halts with "the project's routing switch is not usable" — which is
	// the false confidence doctor's check stopped giving (runner.ProvidersFor's
	// own doc: a resolution read over the unfiltered block is a route a run may
	// not carry). With no origin there is no repository to filter by, so repo is
	// "" and only the routes written for every repository survive — the reading
	// the rest of this file already gives that state (providerProblems passes ""
	// to CheckProviderPolicy for the same reason).
	var routes, ownProviders map[string]config.ModelProvider
	if readable {
		// Only a readable project config has an operator's table to filter;
		// without one nothing is resolved at all (the note below), so there is
		// no table to build.
		routes = runner.ProvidersFor(claimed, repo)
		// ownProviders is the same table before the filter: the operator's own
		// providers exactly as the operator wrote them, which is the only table
		// that can answer whether an entry's allow_data_to was *written*
		// (claudeDirectRefusal). runner.ProvidersFor rewrites every entry it
		// keeps to `allow_data_to: [repo]` and so answers "written" for one that
		// never wrote the key at all.
		ownProviders = lc.Providers
	}
	// What a run of this configuration does with the switch, asked once of the
	// runner's own decision (routingReachOf) and used by every line below: the
	// switch's reach is the same for each pin, and the pins differ only in
	// whether a route carries them.
	//
	// The switch is the operator's project config: with none there is nothing
	// to read (the unresolved-offline note below) and nothing to ask the
	// runner's decision about.
	var sw *config.RoutingClaude
	if readable {
		sw = claudeSwitchOf(lc.Routing)
	}
	reach, why := routingReachOf(sw, cfg.Agent.Auth, mode)
	// The pins the switch rewrote, which are the ones the run's own rules judge
	// once this loop is done. It stays empty wherever no run of this
	// configuration takes the route.
	routedPaths := map[string]bool{}
	for _, p := range pins {
		if !readable {
			notes = append(notes, config.Problem{Path: "routing.claude", Message: fmt.Sprintf(
				"%s unresolved offline: the switch is the operator's project config for %s and this command has none selected (design §11), so a resolution it cannot read is not printed as one; `fugaro doctor` reads the published policy and prints it", p.model, cfg.Project)})
			continue
		}
		route, sent, ok := config.RoutingClaudeFor(sw, p.model, routes)
		switch {
		case ok && route == config.RouteDirect:
			// The direct route is the switch's own answer and no repository
			// changes it (the resolver reads no table for a `via: direct`
			// switch, and a model the switch does not name is direct anyway —
			// D1b), so this case comes before the repository questions below
			// and prints even from a checkout whose origin cannot be read.
			//
			// Two states resolve to direct and they are not the same sentence:
			// the switch may name this model with via: direct — a legal
			// spelling, and the one way to say "this ID stays direct" while
			// routing others — or it may not name the model at all, which is
			// D1b's default. This printed "no switch names it" for both, which
			// is wrong about the first: doctor prints the named case as
			// "-> direct (via: %s)", and the two commands read one switch.
			if sw.Names(p.model) {
				notes = append(notes, config.Problem{Path: "routing.claude", Message: fmt.Sprintf("%s -> direct (via: %s)", p.model, sw.ViaValue())})
			} else {
				notes = append(notes, config.Problem{Path: "routing.claude", Message: fmt.Sprintf("%s -> direct (no switch names it: D1b's default)", p.model)})
			}
			if why := claudeDirectRefusal(cfg.Agent, p, routes, ownProviders, repo, mode); why != "" {
				problems = append(problems, config.Problem{Path: p.path, Message: why})
			}
		case rerr != nil:
			// Everything below is a question about the routes a run carries,
			// and which routes a run carries is a question about the
			// repository: a route written for a list of repositories may or may
			// not be this one's. This checkout's origin does not answer it, so
			// the line says unresolved rather than resolving for a repository
			// nobody named (providerProblems passes "" to CheckProviderPolicy
			// for the same reason).
			notes = append(notes, config.Problem{Path: "routing.claude", Message: fmt.Sprintf(
				"%s unresolved: the routes a run carries are the ones its repository may send code to (allow_data_to), and this checkout's origin could not be read (%v), so no repository can be said to carry the switch's route; run validate in the checkout, or from one whose origin names owner/name", p.model, rerr)})
			if why := claudeDirectRefusal(cfg.Agent, p, routes, ownProviders, repo, mode); why != "" {
				problems = append(problems, config.Problem{Path: p.path, Message: why})
			}
		case !ok:
			// The switch names this model and no route a run of this repository
			// carries claims it. Whether that is a defect in the configuration
			// depends on whether a run reaches the switch at all, and that is
			// the runner's own decision (routingReachOf): routing is the
			// gateway's (design §6.2, D11), via: auto never reaches a Vertex
			// run, and a stored Anthropic API key turns auto back to the direct
			// path. A problem here is a claim about a run, so it is reported
			// only where every run of this configuration reaches the switch —
			// the other states are notes, because that halt is not this
			// command's to promise. The refusal beside this case is gated on
			// the same difference (claudeDirectRefusal): the two halves of one
			// function may not disagree about whether routing reaches here.
			msg := fmt.Sprintf("%s is named by routing.claude.models and does not resolve over the routes a run of %s carries: no single provider entry claims it with allow_data_to written, or the entry that claims it does not admit this repository, and a call the switch names has no direct fall-back (D1b, design §6.2)", p.model, repo)
			fix := "fix the list, the claim, or the allow_data_to of the entry that claims it"
			switch reach {
			case routingAlways:
				problems = append(problems, config.Problem{Path: "routing.claude", Message: msg + ". A run of this repository halts with \"the project's routing switch is not usable\"; " + fix})
			case routingKeyless:
				// The one fact that decides it is not this command's to read,
				// so the sentence names both runs rather than picking one.
				notes = append(notes, config.Problem{Path: "routing.claude", Message: msg + ". via: auto is key-presence driven, so which run this halts is a fact this command does not read: a run of this repository with no Anthropic API key stored halts with \"the project's routing switch is not usable\", and one with a key stored keeps the pins direct; " + fix})
			default:
				notes = append(notes, config.Problem{Path: "routing.claude", Message: msg + ". The switch reaches no run of this configuration (" + routingInertWhy(why, cfg.Agent.Auth, mode) + "), so nothing halts here and the pins keep their Claude IDs; " + fix})
			}
		default:
			// The route the switch licenses, said with what a run of this
			// configuration does with it: the reach is the runner's own
			// decision, and where it depends on the job's stored Anthropic API
			// key — a fact this command does not read — the line names both
			// states instead of picking one. A resolution printed as the run's
			// where no run takes it is the false confidence doctor's check
			// stopped giving.
			line := fmt.Sprintf("%s -> %s, sent as %s", p.model, route, sent)
			switch {
			case reach == routingKeyless:
				notes = append(notes, config.Problem{Path: "routing.claude", Message: line + " with no Anthropic API key stored in the job, and -> direct with one (via: auto is key-presence driven: D1b)"})
			case why == runner.RoutingInertAutoVertex:
				// via: auto's answer for a Vertex run is the direct route, and
				// that is the switch's own reading of the credential rather
				// than a fact about this run's budget: the line leads with what
				// the run does and names the licence beside it, rather than
				// printing as the run's a route no Vertex run takes (the false
				// resolution doctor's own check stopped giving).
				notes = append(notes, config.Problem{Path: "routing.claude", Message: fmt.Sprintf("%s -> direct (%s; the switch licenses %s, sent as %s, for a run that does take it)", p.model, routingInertWhy(why, cfg.Agent.Auth, mode), route, sent)})
				continue
			case reach == routingInert:
				notes = append(notes, config.Problem{Path: "routing.claude", Message: line + " (nothing takes it here: " + routingInertWhy(why, cfg.Agent.Auth, mode) + ", so the pins keep Claude's own IDs)"})
				continue
			default:
				notes = append(notes, config.Problem{Path: "routing.claude", Message: line})
			}
			routedPaths[p.path] = true
		}
	}
	// The pins the switch produces are the ones the run's own rules judge:
	// applyRouting writes the route's ID into the pin, and internal/runner's
	// checkProviderModels then asks config.CheckProviderAuth and
	// config.CheckProviderGateway about exactly those pins — under an observing
	// budget a routed pin is refused there as served by a provider that needs
	// budget.mode enforce. validate printed the resolution and exited 0 for
	// every one of those runs. The rules asked here are the runner's own, over
	// the agent the switch produced.
	if len(routedPaths) > 0 {
		a, paths := routedAgent(cfg.Agent, sw, routes)
		if len(paths) > 0 {
			bg := config.EffectiveBackground(a, routes)
			var ps []config.Problem
			ps = append(ps, config.CheckProviderAuth(a, routes)...)
			ps = append(ps, config.CheckProviderGateway(a, bg, routes, mode)...)
			for _, q := range ps {
				if !routedPaths[q.Path] {
					// Only the pins the switch rewrote are this block's
					// business: a provider model the file pins itself is
					// providerProblems'.
					continue
				}
				if reach == routingAlways {
					problems = append(problems, q)
					continue
				}
				notes = append(notes, config.Problem{Path: q.Path, Message: fmt.Sprintf("%s (via: auto: a run of this repository with no Anthropic API key stored takes the switch's route and halts there, and one with a key stored keeps the pins direct and does not)", q.Message)})
			}
		}
	}
	return problems, notes
}

// routingReach is what a run of this configuration does with the switch: the
// three answers a reader that is not the run can give.
type routingReach int

const (
	// routingInert: no run of this configuration reaches the switch. Its pins
	// keep their Claude IDs and a switch it cannot resolve does not halt it.
	routingInert routingReach = iota
	// routingKeyless: via: auto, whose reach is key-presence driven — a run
	// with no Anthropic API key stored takes the route, and one with a key
	// stored keeps the pins direct (D1b). validate reads this file's credential
	// and this machine's budget, never the job's environment, so it can say
	// only that both runs exist.
	routingKeyless
	// routingAlways: every run of this configuration that has a gateway reaches
	// the switch.
	routingAlways
)

// routingReachOf asks the runner's own decision — runner.RoutingInertReason,
// the function applyRouting branches on — instead of re-deriving the ladder
// here. A second reading of "when does routing apply" is how validate came to
// report a halt no run makes (first the gateway, then via: auto under a Vertex
// credential) and a resolution no run takes.
//
// The one fact this command cannot read is whether the job has an Anthropic API
// key stored: that is a secret version in the cloud, not a line in this file.
// The question is therefore asked twice, with the key and without it, and the
// state where the two answers differ — via: auto under api-key auth — is its
// own answer rather than one of the two guessed at. A credential that is
// neither api-key nor vertex (oauth, or nothing at all) is no gateway whichever
// key the job holds, so it is inert either way.
func routingReachOf(sw *config.RoutingClaude, auth, mode string) (routingReach, runner.RoutingInert) {
	gateway := runner.GatewayOn(auth, mode)
	why := runner.RoutingInertReason(sw, auth, gateway, false)
	switch {
	case runner.RoutingInertReason(sw, auth, gateway, true) == runner.RoutingActs:
		// A run with a key stored reaches the switch too, so the key is not
		// what this configuration's answer hangs on.
		return routingAlways, why
	case why == runner.RoutingActs:
		return routingKeyless, why
	default:
		return routingInert, why
	}
}

// routingInertWhy words the runner's own reason for a switch that reaches no
// run of this configuration (runner.RoutingInert), for a caller that holds the
// credential and the effective mode: the facts two of the reasons are about.
// why must be a reason RoutingInertReason gave for a run that does not act —
// the states routingKeyless covers have no single sentence and are worded where
// they are used.
func routingInertWhy(why runner.RoutingInert, auth, mode string) string {
	m := mode
	if m == "" {
		m = policy.ModeOff
	}
	switch why {
	case runner.RoutingInertNoGateway:
		return "no run of this configuration has a gateway (agent.auth is " + auth + " and budget.mode is " + m + ": the gateway needs a model API's credential and an observing or enforcing budget)"
	case runner.RoutingInertAutoVertex:
		return "via: auto does not route a Vertex run (Vertex serves Claude under the project's own credential, and its jobs carry no Anthropic API key)"
	case runner.RoutingInertNoSwitch:
		return "no switch is published, which is D1b's default"
	case runner.RoutingInertAutoKey:
		return "via: auto keeps the pins direct when the job has an Anthropic API key stored (D1b)"
	default:
		return "the switch routes nothing itself (via: direct)"
	}
}

// routedAgent is the agent internal/runner's applyRouting produces out of this
// one: each pin the switch names and can resolve is rewritten through the field
// its effective model came from (agent.model when no agent.models.<role> names
// one), so an agent that names only agent.model keeps its coder pin there
// rather than gaining an explicit one. The paths returned are the pins the
// switch wrote, which are the ones the run's own rules then judge; the file's
// other pins are providerProblems' business.
func routedAgent(a config.Agent, sw *config.RoutingClaude, routes map[string]config.ModelProvider) (config.Agent, []string) {
	if sw == nil {
		return a, nil
	}
	var paths []string
	slot := func(path string, get func() string, set func(string)) {
		m := get()
		if m == "" || !sw.Names(m) {
			return // the list is the opt-in: an ID it does not name is untouched
		}
		route, sent, ok := config.RoutingClaudeFor(sw, m, routes)
		if !ok || route == config.RouteDirect || sent == m {
			return
		}
		set(sent)
		paths = append(paths, path)
	}
	slot("agent.models.coder", func() string { return a.ModelFor(config.RoleCoder) }, func(m string) {
		if a.Models.Coder == "" {
			a.Model = m
			return
		}
		a.Models.Coder = m
	})
	slot("agent.models.reviewer", func() string { return a.ModelFor(config.RoleReviewer) }, func(m string) {
		if a.Models.Reviewer == "" {
			a.Model = m
			return
		}
		a.Models.Reviewer = m
	})
	slot("agent.models.background", func() string { return a.Models.Background }, func(m string) { a.Models.Background = m })
	return a, paths
}

// claudeSwitchOf is the switch a routing block holds: nil for no block, nil
// for a block with no claude member (a `routing: {}` the loader reads as an
// absent switch). The resolver takes a nil switch and answers direct (D1b), so
// callers pass this rather than reading through the block.
func claudeSwitchOf(r *config.Routing) *config.RoutingClaude {
	if r == nil {
		return nil
	}
	return r.Claude
}

// claudeDirectRefusal is the message a Claude pin that goes direct carries when
// the run cannot make the call as it stands — "" for every other state. It is
// routingProblems' second rule; the doc comment above has the reading of "no
// Anthropic key" it rests on.
//
// The two provider tables it asks are two different questions. routes is the
// table the run will have, which is what "does this run need the key" is about
// (config.AnthropicKeyNeeded, the rule providerProblems' own note asks). own is
// the operator's own table *as the operator wrote it* — the entry's own list,
// which is the only thing that can answer D1b's question, whether the claiming
// entry's allow_data_to was written.
//
// That question must be asked of own and never of runner.ProvidersFor's table:
// the filter rewrites every entry it keeps to `allow_data_to: [repo]` (its own
// doc — the run only needs to know it is allowed), so over it an entry that
// never wrote allow_data_to reads as written, and this refusal stated a premise
// the warning providerProblems prints for the same file contradicts (an absent
// allow_data_to is exactly what OpenToEveryRepoReason describes). Which entry
// claims the model for this repository is still the run's own filter's answer —
// a route written for other repositories only is no route this run carries, so
// "write the switch" is not advice that run can take — and that filter is asked
// first, for the entry's name; the written question is then asked of the
// operator's entry by that name.
func claudeDirectRefusal(a config.Agent, p claudePin, routes, own map[string]config.ModelProvider, repo, mode string) string {
	if mode != policy.ModeEnforce {
		// Without a gateway there is no routing at all: nothing is sent to a
		// provider, and "write the switch" would be advice this run cannot take
		// (the same reading providerProblems' key note gives that state).
		return ""
	}
	if !config.AnthropicKeyNeeded(a, routes, repo, mode) {
		// The run does not need the key on this configuration (an oauth run
		// brings its own token): nothing is missing.
		return ""
	}
	// Would the switch resolve this model if it named it? The one resolver
	// answers it over the routes this repository's run would have, so this rule
	// and the run's own resolution cannot disagree about whether a route is
	// there.
	probe := &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{p.model}}
	name, _, ok := config.RoutingClaudeFor(probe, p.model, runner.ProvidersFor(own, repo))
	if !ok {
		// No route of this project claims the model for this repository: there
		// is nothing to send the call to, and "write the switch" would be advice
		// this project cannot take.
		return ""
	}
	if !own[name].AllowDataToWritten() {
		// The entry admits this repository (it survived the filter above) and
		// names no repository in allow_data_to — the state the run's own table
		// cannot show, and the one D1b's safeguard is about: an absent
		// allow_data_to already means every repository of the project, which is
		// why this one path requires the list to be written. The switch cannot
		// route through the entry as it stands, so the message says which entry
		// names no repository, and what to write, before it says to write the
		// switch.
		what := `allow_data_to naming the repositories whose code may be sent there, e.g. ["owner/name"]`
		if repo != "" {
			what = fmt.Sprintf(`allow_data_to: [%q]`, repo)
		}
		return fmt.Sprintf(`%s is pinned and goes direct, and this call needs the Anthropic API key (%s), while provider %s of this project claims the model and its allow_data_to names no repository: an absent allow_data_to already means every repository of the project may mount its key and send it code, and an empty list is refused as naming nobody, which is why D1b's safeguard requires the list to be written before the switch may route a Claude call through that entry. Write %s on %s — or ["*"] to mean every repository on purpose — and then write routing.claude: openrouter in the project config: routing: {claude: {via: openrouter, models: [%s]}} (the switch is the operator's and never a repository's; D1a, D1b, design §6.2)`, p.model, config.MissingAnthropicKey, name, what, name, p.model)
	}
	return fmt.Sprintf("%s is pinned and goes direct, and this call needs the Anthropic API key (%s), while a route of this project claims the model and its allow_data_to is written: write `routing.claude: openrouter` in the project config — routing: {claude: {via: openrouter, models: [%s]}} — to send the call there instead (the switch is the operator's and never a repository's; D1a, D1b, design §6.2)", p.model, config.MissingAnthropicKey, p.model)
}

// setsPolicy is whether the file has a budget: block or a token limit: the
// keys that the default branch's file bounds.
func setsPolicy(cfg *config.Config) bool {
	return cfg.Budget != nil || cfg.Agent.MaxRunTokens > 0 || cfg.Agent.MaxOutputTokens != (config.RoleTokens{})
}

// branchNote says, when the file is a checkout's and its branch isn't the
// default branch, that the runner reads policy from the default branch's file
// and this file can only tighten it. "" when the file isn't in a checkout, is on a detached HEAD or
// the default branch isn't known locally.
func branchNote(ctx context.Context, path string) string {
	dir := filepath.Dir(path)
	git := func(args ...string) string {
		c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		c.WaitDelay = 5 * time.Second
		out, err := c.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	def := strings.TrimPrefix(git("symbolic-ref", "--short", "refs/remotes/origin/HEAD"), "origin/")
	if def == "" {
		return ""
	}
	cur := git("branch", "--show-current")
	if cur == def {
		return ""
	}
	if cur == "" {
		// A detached HEAD (a CI checkout) is no branch to speak of.
		return ""
	}
	cur = "branch " + cur
	return fmt.Sprintf("this is %s, not the default branch (%s): runs read the budget policy from %s's fugaro.yaml, "+
		"which this validation can't see; this file can only tighten it", cur, def, def)
}

// validateLayer is validate's project layer summary: which layer the file
// resolved against.
type validateLayer struct {
	Where      string `json:"where"`
	Generation int64  `json:"generation,omitempty"`
	SHA256     string `json:"sha256"`
}

// validateOutput is the JSON shape of `fugaro validate --json`.
type validateOutput struct {
	Valid    bool             `json:"valid"`
	Problems []config.Problem `json:"problems"`
	// Warnings are not problems: they never make the file invalid.
	Warnings []config.Problem `json:"warnings"`
	// Notes are neither: things the run will need in a case validate can
	// only see offline, reported so they are not a surprise (a stored
	// Anthropic key, for a pin that goes direct).
	Notes        []config.Problem `json:"notes,omitempty"`
	ProjectLayer *validateLayer   `json:"project_layer,omitempty"`
}

func newValidateCmd() *cobra.Command {
	var asJSON bool
	var layerFile string
	var offline bool
	cmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Check a fugaro.yaml against the schema and the repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "fugaro.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			data, err := readFugaroYAML(path)
			if err != nil {
				return err
			}
			rf, err := resolveFugaroYAML(cmd.Context(), data, selectedProjectConfig(cmd.Context()), layerOptions{File: layerFile, Offline: offline, Lenient: true})
			if err != nil {
				return err
			}
			cfg, problems := rf.Cfg, rf.Problems
			layer, layerWarnings := validateLayerLines(rf)
			var warnings, notes []config.Problem
			if cfg != nil {
				problems = append(config.Check(cfg, filepath.Dir(path)), computeProblems(cfg)...)
				// The project config, and the routes the run will have: the
				// owner's own with the checkout layer's claude_models merged
				// in (layerRoutes), so a layer-claimed pin is judged by the
				// routes the run will have instead of refused here. A merge
				// the rules refuse is a problem of the layer's own, and the
				// owner's own routes are what the rules below see then.
				//
				// Only when the selected config is this file's own project.
				// A layer belongs to the installation of the project the file
				// being validated names, and it is judged against that
				// installation's routes; another project's config holds other
				// routes, so merging the claim into it would report a claim
				// this file's own installation serves as "names provider X,
				// which the project config does not define" — a false refusal
				// of a valid file, from a config the rules below do not judge
				// anything by either (budgetProblems and providerProblems
				// return early for another project).
				lc := selectedProjectConfig(cmd.Context())
				var claimed map[string]config.ModelProvider
				if lc != nil {
					claimed = lc.Providers
				}
				if lc != nil && lc.Name == cfg.Project {
					merged, lp := layerRoutes(lc, rf.Layer.Layer)
					claimed = merged
					problems = append(problems, lp...)
				}
				rp, rw := recipeDirProblems(filepath.Dir(path), cfg)
				problems = append(problems, rp...)
				problems = append(problems, gateCheckProblems(filepath.Dir(path), cfg)...)
				// The model checks judge the roles as the effective recipe
				// leaves them.
				ecfg := withRecipeRoles(filepath.Dir(path), cfg)
				bp, bw := budgetProblems(ecfg, lc, claimed)
				problems, warnings = append(problems, bp...), append(bw, rw...)
				warnings = append(warnings, config.CheckWarnings(cfg, filepath.Dir(path))...)
				warnings = append(warnings, cloneWorkflowWarnings(cfg)...)
				pp, pw, pn := providerProblems(cmd.Context(), ecfg, lc, claimed)
				problems, warnings, notes = append(problems, pp...), append(warnings, pw...), append(notes, pn...)
				rtp, rtn := routingProblems(cmd.Context(), ecfg, lc, claimed)
				problems, notes = append(problems, rtp...), append(notes, rtn...)
				annotateProjectRecipe(filepath.Dir(path), cfg, problems)
				if lc != nil && lc.Name == cfg.Project && setsPolicy(cfg) {
					if n := branchNote(cmd.Context(), path); n != "" {
						warnings = append(warnings, config.Problem{Path: "branch", Message: n})
					}
				}
			} else if name := selectedProjectName(cmd.Context()); name != "" {
				// Name the project a config can be selected for, so the
				// fix is one line to add.
				for i, p := range problems {
					if p.Code == config.CodeProjectRequired {
						problems[i].Message = fmt.Sprintf("is required: add `project: %s` (the Fugaro project this repository belongs to)", name)
					}
				}
			}
			warnings = append(layerWarnings, warnings...)
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(validateOutput{Valid: len(problems) == 0, Problems: append([]config.Problem{}, problems...), Warnings: append([]config.Problem{}, warnings...), Notes: append([]config.Problem{}, notes...), ProjectLayer: layer}); err != nil {
					return err
				}
			} else {
				for _, n := range notes {
					fmt.Fprintln(cmd.ErrOrStderr(), "note: "+n.String())
				}
				for _, w := range warnings {
					fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w.String())
				}
				for _, p := range problems {
					fmt.Fprintln(out, p)
				}
				if len(problems) == 0 {
					fmt.Fprintf(out, "%s is valid\n", path)
				}
			}
			if len(problems) > 0 {
				return &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s has %d problem(s)", path, len(problems))}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	cmd.Flags().StringVar(&layerFile, "project-layer", "", "resolve against this project layer file instead of the published one (also checks the file; for CI without bucket access, and before fugaro config publish)")
	cmd.Flags().BoolVar(&offline, "offline", false, "never read the runs bucket: resolve against the cached project layer, if any")
	return cmd
}

// validateLayerLines are validate's project layer summary and warning
// lines: which layer the file resolved against, why none or the cache was
// used, and that profiles need layeredSince everywhere.
func validateLayerLines(rf resolvedFile) (*validateLayer, []config.Problem) {
	var layer *validateLayer
	var warnings []config.Problem
	if l := rf.Layer.Layer; l != nil {
		layer = &validateLayer{Where: rf.Layer.Where, Generation: rf.Layer.Generation, SHA256: l.SHA256}
		warnings = append(warnings, config.Problem{Path: "project layer", Message: fmt.Sprintf("uses the project layer %s (generation %d, sha256 %s); fugaro config show prints where each value comes from", l.Project, rf.Layer.Generation, shortSHA(l.SHA256))})
	}
	if rf.Layer.Note != "" {
		warnings = append(warnings, config.Problem{Path: "project layer", Message: rf.Layer.Note})
	}
	if rf.Cfg != nil && usesProfiles(rf.Cfg) {
		warnings = append(warnings, config.Problem{Path: "profile", Message: "profiles need fugaro " + layeredSince + ": every teammate's CLI, every CI pin and every job image must be on it before this file is merged (older ones refuse it)"})
	}
	return layer, warnings
}

// usesProfiles reports whether a resolved config took any workflow from a
// profile: what binaries before layeredSince refuse.
func usesProfiles(c *config.Config) bool {
	for _, w := range c.Workflows {
		if w.Profile != "" {
			return true
		}
	}
	return false
}

// cloneWorkflowWarnings warns, one per workflow, that checkout: clone is
// valid config but runs nothing yet: cloneSince's launch gate
// (recipes_skew.go) refuses every checkout: clone launch until fugaro
// cloneSince ships, since plan Task 17's runner side (the runner's own
// FUGARO_CHECKOUT support) is in no tagged release yet. The config stays
// valid — this is for later, once it ships — so it is a warning, not a
// Problem from Check.
func cloneWorkflowWarnings(c *config.Config) []config.Problem {
	var ps []config.Problem
	for _, name := range slices.Sorted(maps.Keys(c.Workflows)) {
		if c.Workflows[name].Checkout != config.CheckoutClone {
			continue
		}
		ps = append(ps, config.Problem{Path: "workflows." + name + ".checkout", Message: fmt.Sprintf(
			"clone runs are refused at launch until fugaro %s: checkout: clone's own runner side (generic-tool plan Task 17) has not shipped yet; checkout: baked runs today", cloneSince)})
	}
	return ps
}
