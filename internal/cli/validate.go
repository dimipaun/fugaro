package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
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
	providers, _, _ := layerRoutes(lc, layer, repo)
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
// claimed is the owner's whole route table with the checkout layer's claims
// written over the entries this repository's runs carry (layerRoutes). The
// routes judged here are the run's own, filtered out of it with
// runner.ProvidersFor, exactly as the run's job environment is written; a rule
// that reads an entry's own allow_data_to needs the entries the filter drops,
// which is why providerProblems gets this table and not the filtered one. The
// *prices* stay lc.Providers' — the claim-blind table the run's environment
// builds — so a claimed ID is priced by the owner's model_prices and never by
// the catalog here either.
//
// The pins judged here are the pins of a *run*, which are not always the pins
// the file wrote: applyRouting rewrites every Claude pin the switch names to the
// route's own ID, and checkProviderModels derives agent.models.background from
// those pins over the routes the run carries — both before checkBudget reads
// the allow-list and prices the pins. Judging the file's own pins judged a run
// that may not exist, in both directions the review found (splitByReach and
// runPins below carry the details). Which run it is is the switch's reach
// (routingReachOf), the same question routingProblems asks; the routed pins'
// provider and price rules are that function's, so the pins it rewrote are
// left to it here.
func budgetProblems(ctx context.Context, cfg *config.Config, lc *localcfg.Config, claimed map[string]config.ModelProvider) (problems, warnings, notes []config.Problem) {
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
		return problems, warnings, nil
	}
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
	// The switch, the routes and the reach: validate resolves a route ID the
	// same way the run does, so a pin that names one is admitted here exactly
	// when the run would route it (config.CheckAllowed's `allowed`), and which
	// pins the run has is the switch's reach. The switch read here is the
	// file's, over the layer cache: the operator's own, since the policy node is
	// published from it — this resolution reads no node (design §11: offline,
	// and validate says "unresolved offline" where it cannot see one), which is
	// the same reading the rest of this function gives the providers. The
	// command does make one node read, and it is cascadeProblems': the published
	// policy, for O2's switch alone, which is a refusal of its own where the node
	// cannot be read (no database, none published, unreadable, or --offline) and
	// which nothing here waits on. The routes are the run's own table, the
	// operator's providers filtered by allow_data_to for this repository
	// (runner.ProvidersFor), the same table routingProblems and providerProblems
	// read.
	repo, rerr := originRepo(ctx)
	if rerr != nil {
		// An unknown repository: only the routes written for every repository
		// are carried, the reading providerProblems and routingProblems give
		// the same state (they pass "" to the rules that name a repository).
		repo = ""
	}
	routes := runner.ProvidersFor(claimed, repo)
	sw := claudeSwitchOf(lc.Routing)
	reach, _ := routingReachOf(sw, cfg.Agent.Auth, e.Mode)
	// runPins is what checkProviderModels leaves the pins of the run that
	// carries a: the agent as applyRouting left it (routedAgent), with its
	// background derived from those pins over the routes that run carries
	// (r.backgroundModel is exactly this). The derivation is why an unset
	// background is not judged as unset: the run fills it with the coder's own
	// model, and a routed coder is the route's ID, which is the ID the
	// allow-list has to admit.
	routed, rewritten := routedAgent(cfg.Agent, sw, routes)
	rewrittenSet := make(map[string]bool, len(rewritten))
	for _, p := range rewritten {
		rewrittenSet[p] = true
	}
	runPins := func(a config.Agent) config.Agent {
		a.Models.Background = config.EffectiveBackground(a, routes)
		return a
	}
	allow := func(a config.Agent) []config.Problem {
		return config.CheckAllowed(a, ef, ceilingLayer(lc).AllowedModels, lc.Routing, routes)
	}
	allowed, allowNotes := splitByReach(reach, allow(runPins(routed)), allow(runPins(cfg.Agent)))
	problems = append(problems, allowed...)
	notes = append(notes, allowNotes...)
	// A role with no model already has its problem: not a second one, from
	// the pins, saying much the same. A pin the allow-list refused, or one it
	// left to a note (a state that hangs on the job's key), is that pin's line:
	// the price rule below does not add a second one to it.
	named := map[string]bool{}
	for _, p := range allowed {
		named[p.Path] = true
	}
	for _, p := range allowNotes {
		named[p.Path] = true
	}
	if cfg.Agent.Auth == "vertex" && e.Mode == policy.ModeEnforce {
		problems = append(problems, config.Problem{Path: "agent.auth", Message: vertexBudgetRefusal})
	}
	if e.Mode == "" || e.Mode == policy.ModeOff {
		return problems, warnings, notes
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
		//
		// The pins priced are the runs' own (runPins), for the same reason the
		// allow-list is asked of them: CheckPins is the run's own second half of
		// this gate, over the agent applyRouting and checkProviderModels left
		// and only with the gateway on — which for auth other than "oauth" is
		// the mode being on, the check above (checkBudget). The pins the switch
		// rewrote are left to routingProblems, which judges exactly those with
		// the run's own provider checks (its routed-pin block): one pin's price
		// said twice, in two near-identical sentences, is noise dedupeLines
		// cannot drop, its key being path and message both.
		pins := func(a config.Agent, takesRoute bool) []config.Problem {
			var ps []config.Problem
			for _, q := range config.CheckPins(a, prices) {
				if named[q.Path] || takesRoute && rewrittenSet[q.Path] {
					continue
				}
				ps = append(ps, q)
			}
			return ps
		}
		p, n := splitByReach(reach, pins(runPins(routed), true), pins(runPins(cfg.Agent), false))
		problems = append(problems, p...)
		notes = append(notes, n...)
	}
	if cfg.Agent.Auth == "api-key" && e.Mode == policy.ModeEnforce && e.PerRunUSD <= 0 {
		// The database's caps are what the rules compare against (D6): a
		// per-run cap set anywhere in it bounds the run, and only a
		// database with none on any of the three sides halts it.
		warnings = append(warnings, config.Problem{Path: "budget.per_run_usd",
			Message: "the mode is enforce but no per-run cap is set in the project config or here; the database's effective per-run cap still bounds the run when one is set anywhere (the repository's own, the default for repositories without one, or the project's global one), and the run halts (no_cap) only when all three are unset"})
	}
	return problems, warnings, notes
}

// providerProblems are the rules for models another provider serves, with the
// owner's providers from the project config when it is cfg's own project: the
// runner's refusals (a model no provider claims, a repository not in
// allow_data_to, a variant or alias, no price, auth other than api-key), and
// the warnings of the pins' prices (unverified placeholders, defaulted cache
// rates). The repository is the checkout's origin; without one the
// allow_data_to rule can't be checked, and a warning says so.
// claimed is the owner's whole route table with the layer's claims written over
// the entries this repository's runs carry (layerRoutes), which is what the
// rules here read: CheckProviderPolicy asks which entry serves a pin *and*
// whether that entry's own allow_data_to names this repository, so the entries
// the run does not carry — the ones whose allow_data_to is the reason a pin is
// refused — must be in the table. The price table is lc.Providers' own, exactly
// as budgetProblems builds it and as the run's environment does.
func providerProblems(ctx context.Context, cfg *config.Config, lc *localcfg.Config, claimed map[string]config.ModelProvider) (problems, warnings, notes []config.Problem) {
	if lc == nil || lc.Name != cfg.Project {
		return nil, nil, nil
	}
	// The reserved provider-key names, before anything that can return early
	// and independently of the budget, the prices and the pins: a workflow
	// secret named <entry>-api-key is a name the job's own spec refuses
	// (infra.SecretMounts) and `fugaro secrets set` refuses to store
	// (config.ReservedProviderSecret), and it is judged against the owner's
	// providers, which is why Validate itself cannot make this call. claimed
	// is the owner's whole table with the layer's claims written over the
	// entries the run carries, so its key set is lc.Providers' — the same
	// names, and so the same reservations.
	problems = append(problems, config.CheckWorkflowProviderSecrets(cfg, claimed)...)
	o, err := lc.Overrides()
	if err != nil {
		return problems, nil, nil // budgetProblems says so
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
	// The naming convention (owner ruling of 2026-10-18): a provider's key is
	// named after its entry, <provider>-api-key. A key stored under any other
	// name keeps working — nothing is renamed and nothing is migrated — so
	// this is a warning, never a problem: the file stays valid, and the line
	// names the entry, the name the key has and the name the convention gives
	// it. The conventional name stays reserved for that provider either way
	// (config.ReservedProviderSecret, which the write paths enforce).
	warnings = append(warnings, config.ProviderSecretWarnings(claimed)...)
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

// cascadeProblems are the rules a `agent.auth: cascade` workflow is held to
// here, beyond the file's own (config.Validate's auth list, whose
// followup.allow_public refusal is the file's own business): the gateway its
// calls are made by, and O2 — the project policy's switch.
//
// The mode the gateway rule is judged over is the run's effective one: the
// ceiling this file's project config builds, the published policy node's mode
// written over it, and the file merged on top (policy.Merge, the runner's own
// merge). All three matter. The file's own mode is only a lower bound on the
// run's — a file that writes no budget under a ceiling that enforces has the
// gateway on — and the node *is* the ceiling for the mode rather than a layer
// over it: internal/runner/policy.go's policyCeiling writes pp.Budget.Mode over
// the environment ceiling's ("the mode is the key this matters most for"), so a
// node that observes gives every run of the project the gateway even where the
// project config says off, and one that says off takes it away where the config
// observes. Judging the rule without the node's mode refused files that every
// run of them admits, for a reason — "budget.mode is not set" — that is false of
// the project.
//
// The ceiling is this file's project's, and a file that belongs to no selected
// project config has none: another project's config holds another
// installation's routes, ceiling and policy, so the gateway rule is judged only
// where that ceiling can be read at all. Judging it without one reads the
// file's own mode as the run's, which is a problem no run of the configuration
// necessarily has. Every such state is refused by O2 below anyway, on the
// reason that is true of it. Those states are not one sentence: nothing
// selected and a config that is there and unreadable are told apart (the rest
// of this file already does, and TestValidateReportsAProjectConfigItCouldNotRead
// holds it), an installation whose config names no database is told that rather
// than "published no policy" — a statement about the operator's file, which is
// what sends them to `fugaro init --firebase` — and `--offline`, which is this
// command's promise to read nothing over the network, says so instead of
// opening the connection the flag exists to prevent.
//
// O2 (the owner's ruling of 2026-10-17, reconciliation.md §4) is the reason
// this function reads the *published* policy: a repository may ask for the
// cascade, and only the operator-written project policy — routing.claude's
// via being exactly `cascade`, O3's spelling — turns it on, so a cascade file
// whose policy does not carry it is a problem naming what to set and that only
// the operator can set it. A via of any other value is not the switch: `direct`
// names the direct path and `auto` keeps today's routing meaning (O3), and
// neither gives any run a credentials cascade. The read fails closed in every
// direction — no database to read a policy from, no policy published, a policy
// this Fugaro cannot read or lists keys it does not know, a routing block
// absent or written with another via, the flag that forbids the read — each
// refuses the file rather than letting a cascade workflow through that no run
// would be admitted with. The launch refuses the same pair again on its own
// side (plan Task C1.19); this is where the repository's author is told before
// pushing.
func cascadeProblems(ctx context.Context, cfg *config.Config, lc *localcfg.Config, lcErr error, offline bool) []config.Problem {
	if cfg.Agent.Auth != config.AuthCascade {
		return nil
	}
	var problems []config.Problem
	refuse := func(why string) {
		problems = append(problems, config.Problem{Path: "agent.auth", Message: fmt.Sprintf(
			"is refused: %s. Set %s in the project policy and publish it (fugaro project policy publish) — a repository asks for the cascade, and only the operator can set it there (O2)",
			why, "routing.claude: {via: cascade}")})
	}
	if lc == nil || lc.Name != cfg.Project {
		switch {
		case lcErr != nil:
			// The config is selected and was refused at load. Its own reason is
			// the problem the caller prepends for it, printed whole and once.
			// No project is named here: which project an unreadable config is
			// for is not a question the file answers.
			refuse("the project config selected here could not be read (the reason is the problem reported for it above), so the policy this project has published cannot be read either")
		default:
			refuse(fmt.Sprintf("no project config for %s is selected here, so the policy this project has published cannot be read", pluginwire.Printable(cfg.Project)))
		}
		return problems
	}
	if offline {
		// --offline promises this command reads nothing over the network, and
		// the published policy is a database read (projectPolicyRead): reading
		// it here is the connection the flag exists to prevent, and it is the
		// one a job without Google credentials cannot make anyway. The switch
		// cannot be seen without it and a cascade file nobody has proven is
		// enabled is refused, never passed — so this is the refusal, and it
		// names the flag rather than the network.
		refuse("--offline was given, so the policy this project has published was not read and whether its switch is set cannot be seen without it")
		return problems
	}
	// The ceiling is readable from here on, so the gateway rule can be judged
	// over the run's own mode. The node's mode stands in for the ceiling's, as
	// the runner's policyCeiling does (the doc comment above); the read is the
	// one O2 needs below in any case, moved up.
	ceiling := ceilingLayer(lc)
	p, found, err := projectPolicyRead(ctx, lc)
	if err == nil && found && p != nil && p.Budget.Mode != "" {
		ceiling.Mode = p.Budget.Mode
	}
	problems = config.CheckCascadeGateway(cfg.Agent, policy.Merge(ceiling, runner.FileLayer(cfg)).Mode)
	switch {
	case err != nil:
		refuse("the project's published policy could not be read (" + oneLineCLI(err.Error()) + ")")
	case lc.Budget == nil || lc.Budget.RTDBURL == "":
		// The config names no database, so projectPolicyRead answered "not
		// found" without a call: "this project has published no policy" would
		// be a claim about a node nobody can read, and the fix is the
		// database, not the policy.
		refuse("this project config names no database, so the policy this project has published cannot be read (fugaro init --firebase writes the database's URL into it)")
	case !found || p == nil:
		refuse("this project has published no policy")
	case p.Routing == nil || p.Routing.Claude == nil || p.Routing.Claude.ViaValue() != config.RouteCascade:
		refuse("this project's published policy does not turn the cascade on")
	}
	return problems
}

// claudePin is one of the run's three pins whose model is a canonical Claude
// ID: the pins the routing switch applies to, because D1a's list is a list of
// exactly those IDs.
type claudePin struct{ path, model string }

// claudePins are the run's Claude pins, in the pin order config's own rules
// use. A pin that is not a canonical Claude ID — a provider's own model, a
// route's ID, an alias, nothing at all — is not one: those have their own
// rules (providerProblems, CheckAllowed) and the switch never names them.
func claudePins(a config.Agent, routes map[string]config.ModelProvider) []claudePin {
	// The runner's default background model (a provider coder's own) is the
	// run's, exactly as budgetProblems judges it: derived over the routes the
	// run carries, not over the owner's whole table, since a coder only a route
	// this repository may not use serves is no coder of this run's.
	a.Models.Background = config.EffectiveBackground(a, routes)
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

// unreadableProjectConfig is the reason a project config that is there was
// refused at load, with the file it refused at the end of the line.
//
// The loader spells its refusal "<file>: local config: <reason>", and validate
// printed that string whole through oneLineCLI — pluginwire.Printable, 200
// runes. The file's absolute path was most of it, so on a machine whose config
// directory is deep the reason was what got cut ("routing.claude.models[0]:
// claude-sonnet-5-… (truncated)"): measured on macOS's default per-user TMPDIR,
// where the reviewed test failed and passed with TMPDIR=/tmp. The reason is the
// half an operator cannot guess and the path is the half they can, so the
// reason leads and the place comes last, shortened to its last elements.
//
// The reason is printed whole, though — escaping kept, no cut. Padding it into
// MaxPrintable was not enough: the loader's reasons are long and end in the
// instruction (the fix), so a cap anywhere near 200 runes took exactly the half
// the line exists for — "…whose allow_data_to is not written: name the
// repositories whose code may b... (truncated)" loses the "or write ["*"] to
// mean every one of them on purpose" that tells the operator what to do, and
// the switch's empty list lost "one explicit Claude ID per entry" the same way.
// Every other config error validate prints is already whole (budgetProblems and
// providerProblems print localcfg's own error as it is), and the file this
// reason comes from is the operator's own local config rather than a
// repository's, so there is no hostile-file budget to keep here; the escaping
// stays, which is the part a terminal needs.
func unreadableProjectConfig(err error) string {
	// The loader's own shape, and nothing else's: an error that does not carry
	// it is printed as it is (nil or empty is not a state this is called in).
	reason, path := err.Error(), ""
	if p, rest, ok := strings.Cut(reason, ": "); ok {
		if r, ok := strings.CutPrefix(rest, "local config: "); ok {
			reason, path = r, p
		}
	}
	// whole is pluginwire's escaping with its rune cap lifted: the cap counts
	// runes and MaxInt is one more than any string has.
	whole := pluginwire.PrintableN(reason, math.MaxInt)
	if path == "" {
		return whole
	}
	return whole + " (in " + shortPath(path) + ")"
}

// shortPath is where a project config is, cut to its last elements: what names
// the file and the directory it lives in (…/fugaro/projects/aurora.yaml). The
// absolute prefix is what says nothing to an operator — on a test machine it is
// a temporary directory — and it is the half of the line that may be spent,
// since the reason leads.
func shortPath(path string) string {
	const elems, max = 3, 40
	short := path
	if parts := strings.Split(filepath.ToSlash(path), "/"); len(parts) > elems {
		short = "…/" + strings.Join(parts[len(parts)-elems:], "/")
	}
	// The leading ellipsis is what keeps a tail from being read as a whole
	// path, and counting it inside max is what keeps the result from being
	// capped again by the caller's own PrintableN.
	if r := []rune(short); len(r) > max {
		short = "…" + string(r[len(r)-max+1:])
	}
	return short
}

// routingProblems are plan task 1b.3's two routing rules: the resolution the
// switch licenses, printed when this command can read it, and the note a Claude
// pin carries when a project routing Claude through a provider leaves it on the
// direct route. Both are asked of the switch's own list as well as of the pins,
// because the run asks them of the list: a switch that names a model no pin
// uses is a switch the run still has to carry out, and one it cannot carry out
// halts it (the switch check below).
//
// The resolution is printed only when the switch is readable, and the switch is
// the operator's own project config: this resolution reads no policy node
// (design §11: offline), which is the same reading budgetProblems and
// providerProblems give the providers. The command's one node read is
// cascadeProblems', for O2's switch alone and never for a route, so nothing
// here waits on it. With no config for this file's project there is nothing to
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
// The note beside a pin that goes direct is design §6.2's safeguard read from
// the offline side. A project that routes Claude through a provider is a
// project with no Anthropic key to fall back on (docs/design/mixed-credentials.md
// §1: an OpenRouter key and subscription OAuth tokens, no Anthropic one), and a
// Claude pin the switch leaves on the direct route is a call that needs the key
// whenever the job holds none — the gateway refuses it by name when none is
// stored (config.MissingAnthropicKey). Whether the job holds one is the fact
// this command cannot read, which is why this is a note and never a refusal.
// What it *can* read offline is the pair that makes the key matter: an api-key
// run under an enforcing budget, whose Claude call therefore goes to Anthropic
// (config.AnthropicKeyNeeded), and a route of the owner's that claims this model
// — the resolver answers whether that route would take the call. Reporting that
// pair as a problem was the same error the resolution lines had stopped making,
// and the harsher version of it: it refused a configuration that is valid
// whenever a key *is* stored, and told an operator who has one to send Claude
// prompts to a third party. A Claude pin nothing claims keeps the note it always
// was: there is no route to send it to, and "write the switch" would be advice
// this project's own providers cannot take.
//
// A note the reader would see twice — the same path and the same sentence —
// is printed once (dedupeLines). claudePins enumerates the three pin paths, and
// the routing lines are keyed by the model rather than by the pin, so a file
// whose agent.model serves both the coder and the reviewer printed the same
// routing line twice. A pin's own line keeps its own path, which is what the
// run's own pin rules name (internal/config's pinPaths), so a note has to
// repeat in both to be dropped: what carries no information is the sentence,
// not the key the owner edits.
//
// That route is looked for in lc.Providers, the operator's own table, and never
// in the checkout layer's claude_models claims (which the resolution above does
// read: those are the routes the run will have). A claim in the layer is a
// repository asking for a model to be served by a route the owner defines — the
// interim path the switch does not replace (D1a) — and it is not the owner
// saying this project has no Anthropic key; the owner's own route table is.
func routingProblems(ctx context.Context, cfg *config.Config, lc *localcfg.Config, runRoutes map[string]config.ModelProvider, lcErr error) (problems, notes []config.Problem) {
	pins := claudePins(cfg.Agent, runRoutes)
	readable := lc != nil && lc.Name == cfg.Project
	// The switch is the operator's project config: with none there is nothing
	// to read (the unresolved-offline note below) and nothing to ask the
	// runner's decision about.
	var sw *config.RoutingClaude
	if readable {
		sw = claudeSwitchOf(lc.Routing)
	}
	if len(pins) == 0 && sw == nil {
		// Nothing to say: no Claude pin carries a line, and no switch is
		// readable whose own list could halt a run. The early return may not
		// stand on the pins alone — a switch names its models itself, and the
		// run halts on those whether or not a pin uses one (the switch check
		// below) — but with no switch in hand there is no line to print either
		// way.
		return nil, nil
	}
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
	// resolves over: runRoutes, the run's own (the caller's layerRoutes table
	// for the routing rules — the operator's providers with the layer's
	// claude_models claims), filtered by allow_data_to for this repository,
	// exactly as runner.ProvidersEnv writes FUGARO_MODEL_PROVIDERS
	// (runner.ProvidersFor, the same filter doctor's check reads). The caller
	// hands over a table that a layer with no claim left unfiltered (its
	// lc.Providers), so the filter is asked here rather than assumed.
	// Resolving over the unfiltered table printed a route this run does not have
	// — the run halts with "the project's routing switch is not usable" — which
	// is the false confidence doctor's check stopped giving (runner.ProvidersFor's
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
		routes = runner.ProvidersFor(runRoutes, repo)
		// ownProviders is the same table before the filter: the operator's own
		// providers exactly as the operator wrote them, which is the only table
		// that can answer whether an entry's allow_data_to was *written*
		// (claudeDirectKeyNote). runner.ProvidersFor rewrites every entry it
		// keeps to `allow_data_to: [repo]` and so answers "written" for one that
		// never wrote the key at all.
		ownProviders = lc.Providers
	}
	// What a run of this configuration does with the switch, asked once of the
	// runner's own decision (routingReachOf) and used by every line below: the
	// switch's reach is the same for each pin, and the pins differ only in
	// whether a route carries them.
	reach, why := routingReachOf(sw, cfg.Agent.Auth, mode)
	// The switch's own list, judged over the routes this run carries and
	// before the pins, because that is the run's own order: applyRouting calls
	// config.CheckRouting(block, r.modelProviders(), "routing") over every ID
	// in routing.claude.models and returns on the first problem, long before it
	// looks at a pin. validate read that list only through the pins — one line
	// per pinned model — so a switch naming a model no pin uses printed
	// "claude-opus-5-5 -> direct" and exited 0 while every run of the
	// configuration halted with "the project's routing switch is not usable",
	// and a file with no Claude pin at all returned before the switch was read.
	//
	// The project config's own loader does not catch it either: localcfg holds
	// the switch to CheckRouting over c.Providers, the operator's table
	// unfiltered by allow_data_to, and the entry that claims the model may be
	// one this repository's run does not carry (runner.ProvidersFor, above).
	//
	// The path is CheckRouting's own ("routing.claude.models[i]"), which is
	// what the loader writes for the same rule at load, so the owner edits the
	// key the same sentence names on both sides.
	//
	// With no repository there is no such judgement to make. Which routes a run
	// carries is a question about the repository (allow_data_to names them), and
	// the table an unknown repository leaves — runner.ProvidersFor's filter over
	// "" — keeps only the entries written for every repository, so a model served
	// by a repository-scoped entry read as unresolvable there: the same file
	// exited 0 in an acme/sandbox checkout and 1 in a directory with no origin,
	// asserting a halt no run necessarily makes, in a run whose provider lines
	// say allow_data_to was not checked at all (providerProblems takes the same
	// branch on rerr). The pin loop answers that state with an unresolved note
	// (its own `case rerr != nil`), and the switch's list gets one here — the
	// list the loader already held to CheckRouting over the operator's own
	// unfiltered table, so it is a route this repository may not carry rather
	// than a claim nobody wrote.
	if sw != nil {
		pinned := make(map[string]bool, len(pins))
		for _, p := range pins {
			pinned[p.model] = true
		}
		if rerr != nil && sw.ViaValue() != config.RouteDirect {
			for i, m := range sw.Models {
				if pinned[m] {
					// The pin loop says this one unresolved in the sentence a
					// pin needs, from the same state and the same reason.
					continue
				}
				notes = append(notes, config.Problem{Path: switchListPath(i), Message: fmt.Sprintf(
					"%s unresolved: the switch names it, and whether a route a run of this repository carries claims it with allow_data_to written is a question about the repository, which this checkout's origin does not name (%v); run validate in the checkout, or from one whose origin names owner/name", m, rerr)})
			}
		} else {
			for _, q := range config.CheckRouting(&config.Routing{Claude: sw}, routes, "routing") {
				if m, ok := switchListModel(sw, q.Path); ok && pinned[m] {
					// The pin rule below judges this model itself, in the sentence
					// a pin needs ("a call the switch names has no direct
					// fall-back") and with the same verdict attached. One model
					// said twice is the repetition dedupeLines exists to drop.
					continue
				}
				msg := q.Message
				if m, ok := switchListModel(sw, q.Path); ok {
					// The state CheckRouting cannot see from the table it is
					// handed: the entry that claims this model exists and its
					// allow_data_to leaves this repository out, so the entry is
					// not in the run's table at all and the rule above reads it
					// as a model nobody claims. validate holds both tables: the
					// operator's own, exactly as written (ownProviders, which the
					// key is read from below too), and the run's (routes).
					msg += switchListFilteredClause(sw, m, repo, ownProviders, routes)
				}
				verdict, halt := switchVerdict(reach, why, cfg.Agent.Auth, mode)
				msg += verdict
				if halt {
					problems = append(problems, config.Problem{Path: q.Path, Message: msg})
					continue
				}
				notes = append(notes, config.Problem{Path: q.Path, Message: msg})
			}
		}
	}
	// The pins the switch rewrote, which are the ones the run's own rules judge
	// once this loop is done. It stays empty wherever no run of this
	// configuration takes the route.
	routedPaths := map[string]bool{}
	for _, p := range pins {
		if !readable {
			if lcErr != nil {
				// The project config is there and was refused at load: the
				// reason is the loader's own, reported once as a problem (the
				// caller's line). The words below — "this command has none
				// selected" — are false of that state, so the resolution is not
				// said a second time here, in those words.
				continue
			}
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
			if msg := claudeDirectKeyNote(cfg.Agent, p, routes, ownProviders, repo, mode); msg != "" {
				notes = append(notes, config.Problem{Path: p.path, Message: msg})
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
			if msg := claudeDirectKeyNote(cfg.Agent, p, routes, ownProviders, repo, mode); msg != "" {
				notes = append(notes, config.Problem{Path: p.path, Message: msg})
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
			// command's to promise. The note a pin that goes direct carries is
			// gated on the same difference (claudeDirectKeyNote): the two halves
			// of one function may not disagree about whether routing reaches
			// here — and the note, unlike this problem, never turns on the key.
			msg := fmt.Sprintf("%s is named by routing.claude.models and does not resolve over the routes a run of %s carries: no single provider entry claims it with allow_data_to written, or the entry that claims it does not admit this repository, and a call the switch names has no direct fall-back (D1b, design §6.2)", p.model, repo)
			verdict, halt := switchVerdict(reach, why, cfg.Agent.Auth, mode)
			msg += verdict + "; fix the list, the claim, or the allow_data_to of the entry that claims it"
			if halt {
				problems = append(problems, config.Problem{Path: "routing.claude", Message: msg})
				continue
			}
			notes = append(notes, config.Problem{Path: "routing.claude", Message: msg})
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
	// The pins the switch produces are the ones the run's own rules judge, and
	// they are judged by the run's own checks: applyRouting writes the route's
	// ID into the pin, and internal/runner's checkProviderModels then asks
	// config.CheckProviderPolicy (the owner's data policy for provider models —
	// the repository's claim on the entry, the variant and the alias rule, and
	// whether the ID it now names has a price), config.CheckProviderAuth and
	// config.CheckProviderGateway about exactly those pins, before CheckAllowed
	// and — with the gateway on — config.CheckPins (budget's own price rule).
	// validate asked two of them, so a routed Claude pin whose route's row has
	// no price printed "-> openrouter, sent as anthropic/claude-sonnet-5.5" and
	// exited 0 while every run of the configuration halted in checkProviderModels
	// with "`anthropic/claude-sonnet-5.5` has no price": the pin now names the
	// route's own ID, and the embedded catalog's Claude row prices the canonical
	// ID only, so nothing but the owner's model_prices prices it (the runner's
	// own TestRoutedClaudeIsPricedFromTheRouteRowNotTheDirectRate). The rules
	// asked here are the runner's own, over the agent the switch produced, and
	// its table: config.CheckAllowed is not repeated because the routed pin asks
	// it the same question as the pin the file wrote — one canonical model, and
	// config.CheckAllowed's inverse-map half admits exactly the route IDs this
	// switch resolves — which budgetProblems has already judged.
	if len(routedPaths) > 0 {
		a, paths := routedAgent(cfg.Agent, sw, routes)
		if len(paths) > 0 {
			bg := config.EffectiveBackground(a, routes)
			var allowed []string
			if cfg.Budget != nil {
				allowed = cfg.Budget.AllowedModels
			}
			// The table the run prices the routes with: lc.Providers' rows and
			// the owner's model_prices, the same one providerProblems builds (a
			// refused table is nil here, and providerProblems and budgetProblems
			// have already said why it was refused).
			prices, _, _ := priceTable(lc, budgetNow())
			var ps []config.Problem
			ps = append(ps, config.CheckProviderPolicy(a, allowed, repo, routes, prices)...)
			ps = append(ps, config.CheckProviderAuth(a, routes)...)
			ps = append(ps, config.CheckProviderGateway(a, bg, routes, mode)...)
			if prices != nil && runner.GatewayOn(cfg.Agent.Auth, mode) {
				// CheckPins is the runner's other price rule, and it runs only
				// with the gateway on (checkBudget). A routed pin is the route's
				// own ID and is claimed by the entry that resolved it, so
				// CheckProviderPolicy has already asked it the same question —
				// the price, the alias — in its own words: a pin refused there
				// is not said twice, because the run prints one of the two
				// (checkProviderModels returns before CheckPins) and two
				// near-identical sentences for one defect is noise
				// dedupeLines does not drop, its key being the message as well
				// as the path. It runs anyway, because it is what the run runs,
				// and a routed ID of a shape the first rule lets through is
				// still this rule's.
				refused := make(map[string]bool, len(ps))
				for _, q := range ps {
					refused[q.Path] = true
				}
				for _, q := range config.CheckPins(a, prices) {
					if !refused[q.Path] {
						ps = append(ps, q)
					}
				}
			}
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
	return dedupeLines(problems), dedupeLines(notes)
}

// dedupeLines drops a line the reader would see twice: the same path and the same
// sentence, which is what a configuration serving two pins from one model
// produces. claudePins enumerates the three pin paths and a file may fill two of
// them from one key (agent.model, or an unset agent.models.reviewer), while the
// routing lines name the model rather than the pin — so the same line came back
// once per pin, and the second copy says nothing the first did not. A note a
// pin's own rule writes keeps that pin's path, which is what the run's pin rules
// name (internal/config's pinPaths), so a note whose path differs is not
// repetition: only both together are.
//
// The problems go through the same rule, because the routing halt is keyed by
// the switch rather than by the pin too — its sentence names routing.claude, the
// key the owner edits being the switch's list — so a file whose agent.model
// answers for both the coder and the reviewer held the same halt twice: the
// repetition this routine exists to drop, in the slice that decides the exit
// code.
func dedupeLines(ns []config.Problem) []config.Problem {
	seen := make(map[string]bool, len(ns))
	out := make([]config.Problem, 0, len(ns))
	for _, n := range ns {
		key := n.Path + "\x00" + n.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, n)
	}
	return out
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
		// The cascade is the one auth whose gateway is not a model API's
		// credential: a cascade run's calls are carried by the credentials
		// service (D133, D143), and runner.GatewayOn answers false for the value
		// because it knows api-key and vertex only — teaching it `cascade` is
		// plan Task C1.19. So the reach is the same today and this clause's
		// conclusion stands, but the reason below is false of it: the cascade's
		// gateway is precisely the one that does not need a credential of the
		// job's own, and the sentence would read as "the cascade has no
		// credential for a gateway to carry", the opposite of what the value
		// asks for. The arm below says what is true instead.
		if auth == config.AuthCascade {
			return "no run of this configuration has a gateway (agent.auth is " + config.AuthCascade + " and budget.mode is " + m + ": a cascade run's gateway is the credentials service's, and this Fugaro's runner does not put one in front of a cascade run yet)"
		}
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

// switchVerdict is the clause a line about a switch no run of this repository
// can carry out ends with: what the switch's reach makes of it. halt is true
// where the clause is a problem — every run of this configuration reaches the
// switch, so every run of it halts — and false where the line is a note,
// because some run does not: via: auto's reach hangs on the key the job holds
// (routingKeyless), and the other reasons say the switch reaches no run at all.
//
// Two rules end a sentence with it: the pin rule, for a model the switch names
// and cannot resolve, and the switch's own list. They say it through one
// function so that one configuration is never called a halt in one line and
// something else in the next; the review finding on the switch's own list was
// exactly a halt no line spoke of, and the four-state table in
// TestValidateDoesNotClaimAHaltWithoutAGateway is the other half of the rule.
func switchVerdict(reach routingReach, why runner.RoutingInert, auth, mode string) (verdict string, halt bool) {
	switch reach {
	case routingAlways:
		return ". A run of this repository halts with \"the project's routing switch is not usable\"", true
	case routingKeyless:
		// The one fact that decides it is not this command's to read, so the
		// sentence names both runs rather than picking one.
		return ". via: auto is key-presence driven, so which run this halts is a fact this command does not read: a run of this repository with no Anthropic API key stored halts with \"the project's routing switch is not usable\", and one with a key stored keeps the pins direct", false
	default:
		return ". The switch reaches no run of this configuration (" + routingInertWhy(why, auth, mode) + "), so nothing halts here and the pins keep their Claude IDs", false
	}
}

// splitByReach sorts the findings two runs produce into refusals and notes:
// routed is what a run whose pins the switch rewrote refuses, direct what a run
// that kept the file's pins refuses.
//
//   - routingAlways: every run of this configuration takes the route, so the
//     routed run's findings are the configuration's — problems.
//   - routingInert: no run reaches the switch, so the pins are the file's own
//     and the direct run's findings are.
//   - routingKeyless (via: auto with an api-key credential, where a stored
//     Anthropic API key turns the run back to the direct path): both runs exist
//     and which one a job is is not a fact this command reads, so a finding
//     *both* runs make is a problem — every run of this configuration halts on
//     it, which is the rule switchVerdict states for the routing lines — and
//     one only one of them makes is a note naming which run halts. The note is
//     the owner's ruling on the same question (the no-key Claude pin): name
//     both states rather than pick one.
//
// The two runs' findings are keyed by path, not by message: one pin refused by
// both runs is one defect even where the two name different IDs for it (the
// file's spelling and the route's are the same model). The routed run's line is
// the one reported where both make it, because that is the run the switch's own
// lines talk about.
func splitByReach(reach routingReach, routed, direct []config.Problem) (problems, notes []config.Problem) {
	switch reach {
	case routingAlways:
		return routed, nil
	case routingInert:
		return direct, nil
	}
	byPath := func(ps []config.Problem) map[string]bool {
		m := make(map[string]bool, len(ps))
		for _, p := range ps {
			m[p.Path] = true
		}
		return m
	}
	routedPaths, directPaths := byPath(routed), byPath(direct)
	for _, p := range routed {
		if directPaths[p.Path] {
			problems = append(problems, p)
			continue
		}
		notes = append(notes, config.Problem{Path: p.Path, Message: p.Message + noKeyRunHalts})
	}
	for _, p := range direct {
		if routedPaths[p.Path] {
			continue // said above, once, with the routed run's own words
		}
		notes = append(notes, config.Problem{Path: p.Path, Message: p.Message + keyRunHalts})
	}
	return problems, notes
}

// The two clauses splitByReach ends a note with under via: auto: which of the
// two runs the finding belongs to, and what the other one does instead. Both
// runs are named because the fact that decides it — the Anthropic API key
// stored in the job — is not a fact validate reads offline (the owner's ruling
// on the no-key Claude pin).
const (
	noKeyRunHalts = " (via: auto, which this depends on: a run of this repository with no Anthropic API key stored takes the switch's route, and this is refused there, so that run halts; one with a key stored keeps the pins direct and does not)"
	keyRunHalts   = " (via: auto, which this depends on: a run of this repository with an Anthropic API key stored keeps the pins direct, and this is refused there, so that run halts; one with no key stored takes the switch's route and does not)"
)

// switchListFilteredClause is the clause a switch-list line gets where the model
// does resolve over the operator's own table but not over the routes a run of
// this repository carries. config.CheckRouting wrote its line from the table it
// was handed — the run's own, which is what the run judges: runner.ProvidersFor
// keeps the entries whose allow_data_to admits the repository and drops the
// rest, so an entry this repository may not send code to is not in it at all,
// and the rule reads that state as "no provider entry claims it". A line saying
// only that sends the operator to write a route entry that already exists; this
// names the entry, its allow_data_to and the repository, so the write is one
// line rather than a search (the review finding on that message).
//
// Empty for every state it is not about: no repository was named (nothing was
// filtered out over "", so the two tables cannot differ about it), the switch
// does not name the model, the operator's own table does not resolve it either
// (nothing claims it, or the claims are ambiguous — CheckRouting has its own
// line for each of those, and RoutingClaudeFor answers !ok for both), or the
// run's table does resolve it after all.
func switchListFilteredClause(sw *config.RoutingClaude, model, repo string, own, routes map[string]config.ModelProvider) string {
	if sw == nil || repo == "" {
		return ""
	}
	entry, sent, ok := config.RoutingClaudeFor(sw, model, own)
	if !ok || entry == config.RouteDirect {
		return ""
	}
	if _, _, ok := config.RoutingClaudeFor(sw, model, routes); ok {
		return ""
	}
	p, ok := own[entry]
	if !ok || len(p.AllowDataTo) == 0 {
		// An entry that wrote no allow_data_to is carried by every repository's
		// run (config.ModelProvider.AllowsData), so it cannot be the one the
		// filter dropped: there is nothing to point the operator at.
		return ""
	}
	return fmt.Sprintf(". The entry that claims it is %s, which serves %s: this repository's run does not carry that entry, because its allow_data_to (%s) does not name %s — add %s to it in the project config and run fugaro init --repo for this repository (the list a run reads is built into its job at deploy time)", entry, sent, `"`+strings.Join(p.AllowDataTo, `", "`)+`"`, repo, repo)
}

// switchListPrefix is config.CheckRouting's own path for one entry of the
// switch's list, with the "routing" path validate passes it: the key the owner
// edits, spelled the same on both sides of the rule.
const switchListPrefix = "routing.claude.models["

// switchListPath is that path for the i-th entry of the switch's list, for the
// lines validate writes itself rather than out of CheckRouting's problems.
func switchListPath(i int) string { return switchListPrefix + strconv.Itoa(i) + "]" }

// switchListModel is the model a problem from config.CheckRouting is about,
// where it is about one entry of the switch's list: CheckRouting's path for
// those is "<path>.claude.models[i]", and a caller that drops a line it has
// already printed per pin needs the model rather than the index. A problem
// about the block itself — the via, the list — names no model, and ok is false
// for it.
func switchListModel(sw *config.RoutingClaude, path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, switchListPrefix)
	if !ok {
		return "", false
	}
	i, err := strconv.Atoi(strings.TrimSuffix(rest, "]"))
	if err != nil || i < 0 || i >= len(sw.Models) {
		return "", false
	}
	return sw.Models[i], true
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
	file := a
	slot := func(get func() string, set func(string)) {
		m := get()
		if m == "" || !sw.Names(m) {
			return // the list is the opt-in: an ID it does not name is untouched
		}
		route, sent, ok := config.RoutingClaudeFor(sw, m, routes)
		if !ok || route == config.RouteDirect || sent == m {
			return
		}
		set(sent)
	}
	slot(func() string { return file.ModelFor(config.RoleCoder) }, func(m string) {
		if a.Models.Coder == "" {
			a.Model = m
			return
		}
		a.Models.Coder = m
	})
	slot(func() string { return file.ModelFor(config.RoleReviewer) }, func(m string) {
		if a.Models.Reviewer == "" {
			a.Model = m
			return
		}
		a.Models.Reviewer = m
	})
	slot(func() string { return file.Models.Background }, func(m string) { a.Models.Background = m })
	return a, rewrittenPaths(file, a, routes)
}

// rewrittenPaths are the pin paths whose *effective* model the switch moved,
// which is not the same set as the slots it assigned: every slot a file does
// not write is answered by another key (config.Agent.ModelFor: a role with no
// models.<role> is answered by agent.model), so rewriting one key moves every
// pin that reads it — with only agent.model set, the route the coder's rewrite
// writes there is the reviewer's model too, and the background derived from
// that coder is the route's ID as well. Reported per slot instead, only the
// coder was named and budgetProblems' skip of the pins the switch rewrote did
// not cover the other two, so the run's own routing block and its budget block
// each printed the same missing-price line for the reviewer.
//
// The comparison is of the effective pins a run has: the three PinnedModels
// with the background a run derives for them (config.EffectiveBackground, what
// budgetProblems' runPins does), taken before and after. Nothing else about the
// agent is compared — the rewrite moves a pin and leaves the rest of the
// configuration alone.
func rewrittenPaths(file, routed config.Agent, routes map[string]config.ModelProvider) []string {
	pins := func(a config.Agent) []string {
		a.Models.Background = config.EffectiveBackground(a, routes)
		return a.PinnedModels()
	}
	before, after := pins(file), pins(routed)
	var paths []string
	for i, path := range [...]string{"agent.models.coder", "agent.models.reviewer", "agent.models.background"} {
		if before[i] != after[i] {
			paths = append(paths, path)
		}
	}
	return paths
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

// claudeDirectKeyNote is the note a Claude pin that goes direct carries when the
// call would need the Anthropic API key — "" for every state where there is
// nothing to say. It is routingProblems' second rule; the doc comment above has
// the reading of "no Anthropic key" it rests on.
//
// It is a note and never a problem for the reason the resolution lines beside it
// are notes: whether the job holds a key is the one fact this command cannot read
// offline (the agent.auth note above says the key is *needed*, and the version
// that would settle it is the cloud's). A refusal here would fail a
// configuration that is valid whenever one is stored, and would tell an operator
// who has one to send Claude prompts to a third party. So the sentence names
// both runs — one with a version of the anthropic-api-key secret mounted sends
// the call to Anthropic as it stands, and one with none halts with the gateway's
// own message — and leaves the judgement to the operator. What validate can read
// is the pair that makes the key matter at all (an enforcing api-key run whose
// call goes to Anthropic, and a route that would take it), and that pair is what
// this note turns on.
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
// never wrote allow_data_to reads as written, and this note stated a premise
// the warning providerProblems prints for the same file contradicts (an absent
// allow_data_to is exactly what OpenToEveryRepoReason describes). Which entry
// claims the model for this repository is still the run's own filter's answer —
// a route written for other repositories only is no route this run carries, so
// "write the switch" is not advice that run can take — and that filter is asked
// first, for the entry's name; the written question is then asked of the
// operator's entry by that name.
func claudeDirectKeyNote(a config.Agent, p claudePin, routes, own map[string]config.ModelProvider, repo, mode string) string {
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
		return keyStates(p.model) + fmt.Sprintf(`, and provider %s of this project claims the model and its allow_data_to names no repository: an absent allow_data_to already means every repository of the project may mount its key and send it code, and an empty list is refused as naming nobody, which is why D1b's safeguard requires the list to be written before the switch may route a Claude call through that entry. Write %s on %s — or ["*"] to mean every repository on purpose — and then write routing.claude: openrouter in the project config: routing: {claude: {via: openrouter, models: [%s]}} (the switch is the operator's and never a repository's; D1a, D1b, design §6.2)`, name, what, name, p.model)
	}
	return keyStates(p.model) + fmt.Sprintf(", and a route of this project claims the model and its allow_data_to is written: write `routing.claude: openrouter` in the project config — routing: {claude: {via: openrouter, models: [%s]}} — to send the call there instead of to Anthropic (the switch is the operator's and never a repository's; D1a, D1b, design §6.2)", p.model)
}

// keyStates opens claudeDirectKeyNote with the two states of the one fact the
// note cannot read. Both halves are the run's own: the halt quotes the gateway's
// message verbatim (config.MissingAnthropicKey is what a call that finds no key
// stored is refused with), and the working configuration is the same call with
// the version in place. Naming both is the point — picking one would be validate
// asserting a secret it never looked at.
func keyStates(model string) string {
	return fmt.Sprintf(`%s is pinned and goes direct, and this call needs a stored Anthropic API key whenever the job holds none: a job with a version of the anthropic-api-key secret mounts one and sends this call to Anthropic as it stands, and a job with none halts — "%s". Whether this job holds one is not a fact this command reads offline, so this is a note and never a refusal`, model, config.MissingAnthropicKey)
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
			// The project config, and the reason there is none: a config that is
			// there and was refused at load is a real problem with it
			// (selectedProjectConfigErr), never the state "nothing selected"
			// (the routing note below), and the file's own checks cannot see it.
			lc, lcErr := selectedProjectConfigErr(cmd.Context())
			rf, err := resolveFugaroYAML(cmd.Context(), data, lc, layerOptions{File: layerFile, Offline: offline, Lenient: true})
			if err != nil {
				return err
			}
			cfg, problems := rf.Cfg, rf.Problems
			layer, layerWarnings := validateLayerLines(rf)
			var warnings, notes []config.Problem
			if cfg != nil {
				problems = append(config.Check(cfg, filepath.Dir(path)), computeProblems(cfg)...)
				// The project config, and the two route tables the rules below
				// read: routes, the run's own — the owner's with the checkout
				// layer's claude_models merged in (layerRoutes), so a
				// layer-claimed pin is judged by the routes the run will have
				// instead of refused here — which the routing rules take, and
				// claimed, the owner's whole table with those claims written
				// over the entries this repository's runs carry, which is what
				// the rules that name a provider or read an allow_data_to read:
				// over the filtered table those sentences lose the entry they
				// are about and send the operator to add a provider that
				// already exists. A merge the rules refuse is a problem of the
				// layer's own, and the owner's own routes are what the rules
				// below see then.
				//
				// With no project config there is no table at all. Only when
				// the selected config is this file's own project: a layer belongs
				// to the installation of the project the file being validated
				// names, and it is judged against that installation's routes;
				// another project's config holds other routes, so merging the
				// claim into it would report a claim this file's own
				// installation serves as "names provider X, which the project
				// config does not define" — a false refusal of a valid file,
				// from a config the rules below do not judge anything by either
				// (budgetProblems and providerProblems return early for another
				// project).
				// lc is the config selected above, read once for the whole
				// command: a broken one is already reported as a problem
				// (selectedProjectConfigErr), so the rules below see the same
				// nil they saw before.
				var routes, claimed map[string]config.ModelProvider
				if lc != nil {
					routes, claimed = lc.Providers, lc.Providers
				}
				if lc != nil && lc.Name == cfg.Project {
					// The filter layerRoutes merges over is the repository's:
					// the checkout's origin, and "" (only the routes written
					// for every repository) when there is none — the reading
					// the rules below already give that state.
					repo, rerr := originRepo(cmd.Context())
					if rerr != nil {
						repo = ""
					}
					merged, withClaims, lp := layerRoutes(lc, rf.Layer.Layer, repo)
					routes, claimed = merged, withClaims
					problems = append(problems, lp...)
				}
				rp, rw := recipeDirProblems(filepath.Dir(path), cfg)
				problems = append(problems, rp...)
				problems = append(problems, gateCheckProblems(filepath.Dir(path), cfg)...)
				// The model checks judge the roles as the effective recipe
				// leaves them.
				ecfg := withRecipeRoles(filepath.Dir(path), cfg)
				bp, bw, bn := budgetProblems(cmd.Context(), ecfg, lc, claimed)
				problems, warnings, notes = append(problems, bp...), append(bw, rw...), append(notes, bn...)
				warnings = append(warnings, config.CheckWarnings(cfg, filepath.Dir(path))...)
				warnings = append(warnings, cloneWorkflowWarnings(cfg)...)
				pp, pw, pn := providerProblems(cmd.Context(), ecfg, lc, claimed)
				problems, warnings, notes = append(problems, pp...), append(warnings, pw...), append(notes, pn...)
				rtp, rtn := routingProblems(cmd.Context(), ecfg, lc, routes, lcErr)
				problems, notes = append(problems, rtp...), append(notes, rtn...)
				problems = append(problems, cascadeProblems(cmd.Context(), ecfg, lc, lcErr, offline)...)
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
			if lcErr != nil {
				// Prepended, not appended: this is the reason the lines below it
				// said less — there is no switch to resolve, and the budget and
				// provider rules each return early with no project config — and
				// an operator reading top-down should meet it first. Added here
				// rather than where the selection was made because the file's own
				// checks above rebuild the problem list (a config that resolved
				// carries no problems of its own, so rf.Problems is empty and the
				// list is built from config.Check on).
				problems = append([]config.Problem{{Path: "project config",
					Message: "could not be read: " + unreadableProjectConfig(lcErr)}}, problems...)
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
	cmd.Flags().BoolVar(&offline, "offline", false, "never read the runs bucket or the project's published policy: resolve against the cached project layer, if any; a cascade file is refused, not admitted unproven")
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
// valid config but runs nothing yet — while cloneShipped is false
// (recipes_skew.go): cloneSince's launch gate refuses every checkout: clone
// launch then, since the runner side (generic-tool plan Task 17) is in no
// tagged release yet. The config stays valid either way, so it is a
// warning, not a Problem from Check. cloneShipped is true since 0.9.0, so
// this reports nothing today; it stays for the flip-back the constant's
// comment describes, and the check below is what makes that flip-back warn
// again.
func cloneWorkflowWarnings(c *config.Config) []config.Problem {
	if cloneShipped {
		return nil
	}
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
