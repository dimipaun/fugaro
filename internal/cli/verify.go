package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/verify"
)

func newVerifyCmd() *cobra.Command {
	var rerun bool
	cmd := &cobra.Command{
		Use:       "verify build|test",
		Short:     "Run and record this repository's build or tests (inside a Fugaro run)",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{string(verify.KindBuild), string(verify.KindTest)},
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir := os.Getenv("FUGARO_STATE_DIR")
			if stateDir == "" {
				return errors.New("FUGARO_STATE_DIR is not set: `fugaro verify` only works inside a Fugaro run")
			}
			rec, err := verify.Run(cmd.Context(), verify.Options{
				StateDir: stateDir, Kind: verify.Kind(args[0]), Rerun: rerun,
				Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), rec.Summary())
			if !rec.Passed {
				return &ExitError{Code: ExitUserError, Err: errors.New("verification failed")}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&rerun, "rerun-failed", false, "rerun only the tests that failed in the previous test run, to detect flaky tests")
	return cmd
}
