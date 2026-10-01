package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
)

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
func budgetProblems(cfg *config.Config, lc *localcfg.Config) (problems, warnings []config.Problem) {
	if lc == nil || lc.Name != cfg.Project {
		// No ceiling to compare with, but a committed enforce is the
		// effective mode whatever the ceiling says (the stricter layer
		// wins), so Vertex under it is refused on every run.
		if cfg.Agent.Auth == "vertex" && cfg.Budget != nil && cfg.Budget.Mode == policy.ModeEnforce {
			problems = append(problems, config.Problem{Path: "agent.auth", Message: vertexBudgetRefusal})
		}
		return problems, nil
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
	allowed := config.CheckAllowed(cfg.Agent, ef, ceilingLayer(lc).AllowedModels)
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
	prices := pricing.Embedded()
	o, err := lc.Overrides()
	if err == nil {
		prices, err = prices.With(o)
	}
	if err != nil {
		// The prices are unreadable, so a model "without a price" would be
		// the table's fault, not the file's: say only what is wrong.
		problems = append(problems, config.Problem{Path: "model_prices", Message: err.Error() + " (in the project config)"})
	} else if cfg.Agent.Auth != "oauth" {
		// An oauth run has no gateway: nothing is priced or pinned.
		for _, p := range config.CheckPins(cfg.Agent, prices) {
			if !named[p.Path] {
				problems = append(problems, p)
			}
		}
	}
	if cfg.Agent.Auth == "api-key" && e.Mode == policy.ModeEnforce && e.PerRunUSD <= 0 {
		warnings = append(warnings, config.Problem{Path: "budget.per_run_usd",
			Message: "the mode is enforce but no per-run cap is set in the project config or here; the run would halt (no_cap)"})
	}
	return problems, warnings
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

// validateOutput is the JSON shape of `fugaro validate --json`.
type validateOutput struct {
	Valid    bool             `json:"valid"`
	Problems []config.Problem `json:"problems"`
	// Warnings are not problems: they never make the file invalid.
	Warnings []config.Problem `json:"warnings"`
}

func newValidateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Check a fugaro.yaml against the schema and the repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "fugaro.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			cfg, problems := config.Parse(data)
			var warnings []config.Problem
			if cfg != nil {
				problems = append(config.Check(cfg, filepath.Dir(path)), computeProblems(cfg)...)
				lc := selectedProjectConfig(cmd.Context())
				bp, bw := budgetProblems(cfg, lc)
				problems, warnings = append(problems, bp...), bw
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
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(validateOutput{Valid: len(problems) == 0, Problems: append([]config.Problem{}, problems...), Warnings: append([]config.Problem{}, warnings...)}); err != nil {
					return err
				}
			} else {
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
	return cmd
}
