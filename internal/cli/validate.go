package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
)

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
