// Package cli implements the fugaro command-line interface.
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time with
// -ldflags "-X github.com/dimipaun/fugaro/internal/cli.Version=<version>".
var Version = "dev"

// Exit codes are part of the CLI contract (design §9.1).
const (
	ExitOK          = 0
	ExitUserError   = 1
	ExitRemoteError = 2
)

// ExitError carries a specific process exit code out of a command.
type ExitError struct {
	Code int   // Code is the process exit code (design §9.1).
	Err  error // Err is the underlying error, or nil if Code alone explains the exit.
}

// Error implements error. When Err is nil, it falls back to a message naming
// the exit code, so a bare &ExitError{Code: ...} still prints something useful.
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap exposes Err so errors.As and errors.Is see through an ExitError.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error returned by a command to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitUserError
}

// NewRootCmd builds the fugaro command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "fugaro",
		Short:         "Fleeting cloud workers for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd(), newValidateCmd(), newConfigCmd(), newVerifyCmd(), newExecCmd(), newImageCmd())
	root.AddCommand(newGCPCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the fugaro version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), Version)
			return err
		},
	}
}
