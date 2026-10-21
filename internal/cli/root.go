// Package cli implements the fugaro command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
	"text/template"

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

// versionTemplate renders the version line: the Version variable and a
// newline, which is exactly what `fugaro version` prints. It is the one
// definition of that line. Cobra renders it for the root's `--version` and
// `-v` flags (its own Version-field machinery), and newVersionCmd renders the
// same template through writeVersion, so the spellings cannot drift apart.
//
// Cobra reads this through text/template rather than through a Go function,
// which is why the line is a template here and not a format string.
const versionTemplate = "{{.Version}}\n"

// writeVersion prints the version line to w, by rendering root's own version
// template with the root command as its data -- the same two steps cobra takes
// for `fugaro --version` (cobra.go's tmpl: template.Must(Parse(text)) then
// Execute(w, cmd)), so `fugaro version` and `fugaro --version` are one
// implementation rather than two copies of one line.
func writeVersion(root *cobra.Command, w io.Writer) error {
	t := template.New("version")
	template.Must(t.Parse(root.VersionTemplate()))
	return t.Execute(w, root)
}

// NewRootCmd builds the fugaro command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "fugaro",
		Short:         "Fleeting cloud workers for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version is what makes cobra add the root's `-v, --version` flag and
		// answer it, with the template below. Cobra answers it inside execute,
		// before it resolves a subcommand and before PersistentPreRun, so
		// `fugaro --version` prints the version and exits 0 without running
		// anything: no checkout read, no local config, no cloud call, no update
		// check (`fugaro --version run` prints the version and does not run
		// `run`). It is a flag of the root alone, like the built-in `-h`.
		//
		// The shorthand `-v` is free: cobra only takes it when the root's own
		// flag set does not already, and no command in this tree defines a `v`
		// shorthand (the only shorthand anywhere is `-f` on `logs --follow`).
		Version: Version,
		// The plugin staleness line (design 4.5) and the update-check kickoff
		// (docs/gcp-setup.md "Staying up to date"). No command defines its own
		// PersistentPreRun, which would replace this one. The update-available
		// nag is NOT printed from a PersistentPostRun: cobra's Command.execute
		// returns as soon as RunE errors, skipping PostRun and
		// PersistentPostRun entirely, so most command failures would never
		// carry it. cmd/fugaro/main.go instead calls WarnUpdateAvailable
		// itself, after ExecuteContextC returns, whether or not it errored.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SetContext(withCheckoutParseCache(cmd.Context()))
			warnStaleSkills(cmd)
			disabled := updateCheckDisabled(cmd)
			cmd.SetContext(withUpdateCheckDisabled(cmd.Context(), disabled))
			startUpdateCheck(cmd, disabled)
		},
	}
	root.SetVersionTemplate(versionTemplate)
	root.AddCommand(
		newVersionCmd(), newValidateCmd(), newConfigCmd(), newVerifyCmd(), newExecCmd(), newImageCmd(),
		newRunCmd(), newRecipesCmd(), newRunsCmd(), newStatsCmd(), newArtifactsCmd(), newLogsCmd(), newDiagnoseCmd(), newCancelCmd(), newSecretsCmd(), newAccountsCmd(), newBackendsCmd(), newInitCmd(), newBudgetCmd(), newWatchCmd(), newReportCmd(), newUpdateSkillsCmd(), newUpgradeCmd(), newDoctorCmd(), newProjectCmd(), newAllowDeleteCmd(), newManagedCmd(), newRestoreCmd(), newPlanCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the fugaro version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.Root(), cmd.OutOrStdout())
		},
	}
}
