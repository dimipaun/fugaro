package cli

// The persistent --verbose (-v), and the reporter a command builds from it.
//
// The flag is the root's and persistent, so every subcommand takes it: the
// documented form is `fugaro update -v`, because a LEADING `fugaro -v` is the
// version (NormalizeVersionArgs rewrites it at the argv boundary before cobra
// parses anything). That rewrite is load-bearing here: the shorthand this flag
// takes is the one cobra would otherwise give the root's version flag, so
// `fugaro -v` stays the version only because the boundary sees it first (F4,
// D1353).
//
// reporterFor is the one place a command turns its flags, the environment and
// the package's terminal seam into a report.Reporter, so no command invents its
// own colour or spinner rule (D1354). The converge is the first command family
// wired to it (CL5b); the rest follow one batch at a time.

import (
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/report"
)

// verboseFlag is the persistent flag's name, read by reporterFor and declared
// by the root (newCoreRoot).
const verboseFlag = "verbose"

// reporterFor builds the reporter for one command's run and opens the log its
// full output is kept in (D1352): the persistent --verbose (a command's own
// --verbose of the same name shadows it, which is `recipes ls`'s), the
// environment the colour rule reads, the package's terminal seam, and the
// command's own writers. name is the command name the log is filed under.
//
// secrets is the run's redaction source, read at each line of the log, so a
// value a stage fills in after the log was opened still applies (F6): the
// converge hands over its own live slots. A nil one is a run with no value to
// hide.
//
// The reporter's colour and spinner rules read the OUTPUT writer: stdin's
// terminal-ness is what the confirmation paths ask before they read a line
// (G1), not something the reporter draws by.
//
// The sink comes back with the reporter because closing it is the caller's: a
// log that is never closed loses the last partial line of a run that died
// mid-line. Every failure to open one is "no log" and never a failed command
// (G5), so the error is not returned and the sink is nil.
func reporterFor(cmd *cobra.Command, name string, secrets func() []string) (*report.Reporter, report.Sink) {
	o := report.Options{
		Out:        cmd.OutOrStdout(),
		Err:        cmd.ErrOrStderr(),
		Getenv:     os.Getenv,
		IsTerminal: writerIsTerminal,
		Verbose:    boolFlag(cmd, verboseFlag),
		JSON:       boolFlag(cmd, "json"),
		NoColor:    boolFlag(cmd, "no-color"),
		ASCII:      boolFlag(cmd, "ascii"),
	}
	sink, err := report.OpenLog(name, os.Getenv, runtime.GOOS, time.Now, secrets)
	if err != nil {
		return report.New(o), nil
	}
	o.Sink = sink
	return report.New(o), sink
}

// boolFlag reads a boolean flag when the command has it, and is false when it
// does not: most commands have --json, and only watch has --no-color and
// --ascii.
func boolFlag(cmd *cobra.Command, name string) bool {
	if cmd.Flags().Lookup(name) == nil {
		return false
	}
	v, err := cmd.Flags().GetBool(name)
	return err == nil && v
}
