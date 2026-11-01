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
//
// It calls versionTemplateFuncs' function rather than printing {{.Version}}:
// cobra renders a version template with the command as its data, and the
// command's Version field holds the value the variable had when NewRootCmd
// built the tree. The function reads the variable when the line is printed,
// which is the variable's value at the only moment that matters -- the build
// sets it with -ldflags before NewRootCmd runs, and the package's own tests
// move it (useVersion, withVersion, releaseBuild, newImagesRigFor) to check
// what a given version prints.
const versionTemplate = "{{fugaroVersion}}\n"

// versionTemplateFuncs are the functions versionTemplate uses. Cobra keeps its
// template functions in a package-level map with no per-command way to add to
// it, so the same map is registered with cobra (for the flag cobra answers)
// and handed to writeVersion's own template (for the subcommand).
var versionTemplateFuncs = template.FuncMap{
	"fugaroVersion": func() string { return Version },
}

func init() { cobra.AddTemplateFuncs(versionTemplateFuncs) }

// writeVersion prints the version line to w, by rendering root's own version
// template with the root command as its data -- the same two steps cobra takes
// for `fugaro --version` (cobra.go's tmpl: template.Must(Parse(text)) then
// Execute(w, cmd)) -- so `fugaro version` and `fugaro --version` are one
// implementation rather than two copies of one line.
func writeVersion(root *cobra.Command, w io.Writer) error {
	t := template.New("version")
	t.Funcs(versionTemplateFuncs)
	template.Must(t.Parse(root.VersionTemplate()))
	return t.Execute(w, root)
}

// NormalizeVersionArgs is argv with a LEADING `-v` or `--version` reduced to
// `--version` alone, so that the flag answers and nothing else is reached.
//
// Cobra resolves the command before it parses any flag -- ExecuteC calls Find,
// which reads the argv through stripFlags -- and at that moment the root's
// version flag does not exist yet, because InitDefaultVersionFlag adds it
// inside execute. stripFlags therefore reads a `--version` it does not know as
// a flag that takes a value and swallows the token after it: `fugaro --version
// run run` resolves `run` and fails there on `unknown flag: --version`,
// `fugaro --version junk logs` resolves `logs`, and `fugaro -v logs logs`
// resolves `logs` and fails on `unknown shorthand flag: 'v'`. The argv
// boundary is the one place that can tell the flag from what follows it,
// which is why cmd/fugaro/main.go and the tests' execute helpers call this
// before cobra parses anything -- the same reason they call
// NormalizeRepoArgs there.
//
// Only a leading flag is rewritten: the version flag is the root's own, like
// the built-in `-h`, so `fugaro run --version` stays the unknown flag it is,
// and `-h` still wins when it comes first.
func NormalizeVersionArgs(args []string) []string {
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		return []string{"--version"}
	}
	return args
}

// NormalizeArgs is argv with the rewrites the argv boundary owes cobra: the
// bare --repo (NormalizeRepoArgs) and a leading version flag
// (NormalizeVersionArgs).
func NormalizeArgs(args []string) []string {
	return NormalizeVersionArgs(NormalizeRepoArgs(args))
}

// NewRootCmd builds the fugaro command tree: the released commands, then what
// this build registered for them and the stubs for the paid names it carries
// no code for (extend.go). With nothing registered the tree is exactly the
// released one.
func NewRootCmd() *cobra.Command {
	root := newCoreRoot()
	applyExtensions(root)
	return root
}

// newCoreRoot builds the released tree. It is separate from NewRootCmd so that
// the extensions above are the only difference between the tree the core
// builds and the tree a build with paid commands ships.
func newCoreRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "fugaro",
		Short:         "Fleeting cloud workers for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version is what makes cobra add the root's `-v, --version` flag and
		// answer it, with the template below. Cobra answers it inside execute,
		// after the flag parse and before PersistentPreRun, so `fugaro
		// --version` prints the version and exits 0 without running anything:
		// no checkout read, no local config, no cloud call, no update check.
		// It is a flag of the root alone, like the built-in `-h`.
		//
		// That is not on its own enough to keep the flag from reaching a
		// subcommand, though: cobra resolves the command before it executes
		// one, and the flag does not exist yet when it does, so `fugaro
		// --version run` would resolve `run` and run it. NormalizeVersionArgs
		// is what closes that gap, at the argv boundary.
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
			ctx := cmd.Context()
			// A command the desktop console spawns as a read changes nothing
			// on this machine, so it does not write the remembered checkouts
			// either (see consoleReadPaths in project.go). Marked here, once
			// per command, from the command's own path.
			if consoleReadPaths[cmd.CommandPath()] {
				ctx = withConsoleRead(ctx)
			}
			cmd.SetContext(withCheckoutParseCache(ctx))
			warnStaleSkills(cmd)
			disabled := updateCheckDisabled(cmd)
			cmd.SetContext(withUpdateCheckDisabled(cmd.Context(), disabled))
			startUpdateCheck(cmd, disabled)
		},
	}
	root.SetVersionTemplate(versionTemplate)
	root.AddCommand(
		newVersionCmd(), newValidateCmd(), newConfigCmd(), newVerifyCmd(), newExecCmd(), newImageCmd(),
		newRunCmd(), newRecipesCmd(), newRunsCmd(), newStatsCmd(), newArtifactsCmd(), newLogsCmd(), newDiagnoseCmd(), newCancelCmd(), newSecretsCmd(), newCredsCmd(), newAccountsCmd(), newBackendsCmd(), newSetupCmd(), newUpdateCmd(), newRetiredInitCmd(), newBudgetCmd(), newWatchCmd(), newUpdateSkillsCmd(), newUpgradeCmd(), newDoctorCmd(), newProjectCmd(), newSettingsCmd(), newAllowDeleteCmd(), newManagedCmd(), newRestoreCmd(), newPlanCmd(), newReaperCmd(), newBacklogCmd(), newPairCmd(), newUserCmd(), newModelsCmd(),
		// The credentials service's own entry point: hidden, and the only
		// thing the service's image runs (credentials_serve.go, plan task
		// C1.14). It is registered like every other command so `fugaro
		// credentials serve` is what the image's CMD names, and hidden so it
		// is not something an operator is offered.
		newCredentialsCmd(),
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
