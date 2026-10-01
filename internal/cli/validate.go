package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// budgetProblems are the rules a repository under a budget must meet, when
// the project config selected for this command is cfg's own project and its
// budget isn't off: every model an explicit priced ID, and no Vertex under
// enforce. With no project config, or one for another project, there is no
// budget to apply.
func budgetProblems(cfg *config.Config, lc *localcfg.Config) []config.Problem {
	if lc == nil || lc.Name != cfg.Project || lc.BudgetMode() == localcfg.BudgetOff {
		return nil
	}
	var ps []config.Problem
	prices := pricing.Embedded()
	o, err := lc.Overrides()
	if err == nil {
		prices, err = prices.With(o)
	}
	if err != nil {
		// The prices are unreadable, so a model "without a price" would be
		// the table's fault, not the file's: say only what is wrong.
		ps = append(ps, config.Problem{Path: "model_prices", Message: err.Error() + " (in the project config)"})
	} else if cfg.Agent.Auth != "oauth" {
		// An oauth run has no gateway: nothing is priced or pinned.
		ps = append(ps, config.CheckPins(cfg.Agent, prices)...)
	}
	if err := checkVertexBudget(lc, cfg); err != nil {
		ps = append(ps, config.Problem{Path: "agent.auth", Message: vertexBudgetRefusal})
	}
	return ps
}

// validateOutput is the JSON shape of `fugaro validate --json`.
type validateOutput struct {
	Valid    bool             `json:"valid"`
	Problems []config.Problem `json:"problems"`
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
			if cfg != nil {
				problems = append(config.Check(cfg, filepath.Dir(path)), computeProblems(cfg)...)
				problems = append(problems, budgetProblems(cfg, selectedProjectConfig(cmd.Context()))...)
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
				if err := enc.Encode(validateOutput{Valid: len(problems) == 0, Problems: append([]config.Problem{}, problems...)}); err != nil {
					return err
				}
			} else {
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
