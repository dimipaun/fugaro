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
	allowed := config.CheckAllowed(agentPins, ef, ceilingLayer(lc).AllowedModels)
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
