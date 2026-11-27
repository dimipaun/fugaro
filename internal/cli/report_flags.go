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
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/report"
)

// verboseFlag is the persistent flag's name, read by reporterFor and declared
// by the root (newCoreRoot).
const verboseFlag = "verbose"

// jsonFlag is the flag that makes a command print its result document instead
// of its human view. Every command that has one declares it itself, in its own
// flag set, so no persistent flag carries this name.
const jsonFlag = "json"

// jsonNeedsFlag names the commands whose --json is the document of one of their
// own modes, and the flag that mode is asked for: --dry-run for the writes,
// whose --json is the dry run's document, and --local for image build, whose
// --json is the local build's report.
//
// Alone, --json makes every one of them refuse, before anything of theirs is
// read, so turning it on for an agent would answer a command with a sentence
// about a flag the agent never passed — and would answer the writes' own
// refusal in a coding agent's session (D1552) with the dry run's sentence
// instead. So the flag is turned on there only when the mode it belongs to was
// asked for, which is the run an agent gets a document out of anyway.
//
// Every entry earns its place, refusal and all (agent_json_test.go). A command
// whose --json does stand on its own beside a --dry-run — image check, mailbox
// sweep — is not here, and an agent reads its document as it reads any other's.
var jsonNeedsFlag = map[string]string{
	"fugaro image build":    "local",
	"fugaro image add":      "dry-run",
	"fugaro models add":     "dry-run",
	"fugaro secrets rm":     "dry-run",
	"fugaro secrets set":    "dry-run",
	"fugaro settings set":   "dry-run",
	"fugaro settings unset": "dry-run",
}

// jsonRefusedWith names the flags --json is refused beside, per command: there
// the two are two answers to one question. An agent that passed one asked for
// that answer, so the flag is left alone and the run reads what was asked for
// rather than a complaint about a flag nobody gave (agent_json_test.go).
//
// A command that already declares its own list keeps it: run's is
// changeSetExcludes, which an ee build refuses --json from, and the entry here
// is that list's own fact rather than a second copy of it (agent_json_test.go
// pins the two together).
var jsonRefusedWith = map[string][]string{
	"fugaro logs":        {"url"},
	"fugaro run":         {"change-set"},
	"fugaro runs digest": {"stdout", "submit-draft"},
	"fugaro update":      {"check"},
	"fugaro watch":       {"plain"},
}

// agentJSON turns --json on for a coding agent's session (D3437, spec §4: "when
// an agent runs a command, give it structured detail to work from"). The agent
// reads the document and the person it works for reads the lines, so a command
// that carries the flag behaves exactly as if it had been passed: the result
// document on stdout and the short lines on stderr, which is where the reporter
// already puts them under --json. An explicit --json is unchanged, and so is
// every other session — a person's own terminal, with or without the IDE
// extension's marker (agentMarker).
//
// It is called at the start of the root's PersistentPreRunE, before any command
// reads its own flags or builds a reporter, because it is nothing but the flag
// being set: a command that reads it after — boolFlag, its own options struct,
// the reporter's Options.JSON — reads what it reads when a person passes --json.
func agentJSON(cmd *cobra.Command) {
	if cmd.Flags().Lookup(jsonFlag) == nil {
		return
	}
	if agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())) == "" {
		return
	}
	changed := func(names []string) bool {
		for _, name := range names {
			if cmd.Flags().Changed(name) {
				return true
			}
		}
		return false
	}
	if flag, paired := jsonNeedsFlag[cmd.CommandPath()]; paired && !cmd.Flags().Changed(flag) {
		return
	}
	if changed(jsonRefusedWith[cmd.CommandPath()]) {
		return
	}
	// Set cannot fail: Lookup found the flag, and a bool parses "true".
	_ = cmd.Flags().Set(jsonFlag, "true")
}

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
	return openReporter(cmd, name, secrets, false)
}

// doorLogName is the name a run opened by a door is filed under (D1351): the
// command path a person typed, one dash for each space.
//
// A door's own name is the sentence's — `r.o.doorName()` says "fugaro workspace
// cap set" — while a log's name is the command's, which is what
// report.OpenLog accepts and what the reporter's census writes down as each
// command's value (report_exempt.go). The two cannot be one string, so the
// door that opens the run converts: a name passed through unaltered would be
// refused by OpenLog, and the refusal is silent by design (a run that cannot
// open a log prints everything it printed before), so the command would file
// no log at all and nothing would say so.
func doorLogName(door string) string { return strings.ReplaceAll(door, " ", "-") }

// convergeReporter is reporterFor for the converge — `fugaro setup` and
// `fugaro update` — which is the one family on the brief account: a step's
// reason is one clause on the terminal and whole in the log (report.Brief —
// the owner's feedback of 2026-11-09 asked for one line per step, and a stage
// that leaves something alone has a paragraph of why). Everything else about
// the reporter is reporterFor's, so the two cannot disagree about colour, the
// spinner or the log.
func convergeReporter(cmd *cobra.Command, name string, secrets func() []string) (*report.Reporter, report.Sink) {
	return openReporter(cmd, name, secrets, true)
}

// openReporter builds one reporter for one command's run, and turns the brief
// account on only for a run that got a log: what Brief cuts, the log holds.
func openReporter(cmd *cobra.Command, name string, secrets func() []string, converge bool) (*report.Reporter, report.Sink) {
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
	// The brief account is one clause on the screen *because* the whole of the
	// reason is in the log (Options.Brief). A run that could not open one — a
	// state directory that is a link, foreign-owned or read-only, G5 — cuts
	// nothing: what it printed is all there is.
	o.Brief, o.Sink = converge, sink
	return report.New(o), sink
}

// questionStyle is the colour a run's own lines are drawn with before it has a
// reporter: report.NewStyle over the same flags and the same environment
// openReporter reads, so a wizard question and the steps under it are one
// syntax. The run holds one (initRun.qs); the writer is the command's stdout,
// because whether colour is on is a fact about where a person reads, and not
// about the writer a run has already wrapped around it.
func questionStyle(cmd *cobra.Command, out io.Writer) report.Style {
	return report.NewStyle(report.Options{
		Out:        out,
		Getenv:     os.Getenv,
		IsTerminal: writerIsTerminal,
		JSON:       boolFlag(cmd, "json"),
		NoColor:    boolFlag(cmd, "no-color"),
		ASCII:      boolFlag(cmd, "ascii"),
	})
}

// reporterOpener is a reporterFor a command hands to its flow instead of
// calling it itself: the flow opens the reporter where its own work starts, so
// a refusal made before that point — one this CLI answers without reaching the
// APIs — keeps no log, not even an empty file (D1352, D1354). A nil one is a
// caller driving the flow with no reporter at all, the flow's own tests, and
// it prints exactly what it printed before there was one.
type reporterOpener func() (*report.Reporter, report.Sink)

// loggedOut is where a command's own output goes: the writer it printed to
// before the reporter, and the run's log beside it, so the log holds what the
// person read (D1352). A run that could not open one — a read-only home,
// CL3's noLog — writes to the screen alone.
//
// The reporter's own step lines are not routed through here: they are the
// screen's account of the steps, and what the log keeps is the run's own
// output beside them.
func loggedOut(w io.Writer, sink report.Sink) io.Writer {
	if sink == nil {
		return w
	}
	return io.MultiWriter(w, sink)
}

// streamOut is where a command's own progress goes once it has a reporter: the
// reporter's Stream — the log always, the terminal under --verbose — so the
// default view stays one line per step (D1354) and the full output is kept
// (D1352). w is where those lines went before the reporter, and is what a run
// whose log could not be opened at all (a state directory that is read-only or
// a link, G5) still writes them to: the output that explains a failure is the
// last thing a run may swallow (G8).
func streamOut(rep *report.Reporter, sink report.Sink, w io.Writer) io.Writer {
	if sink == nil {
		return w
	}
	return rep.Stream()
}

// failStep ends a step in failure and closes the run, so a command's failure
// path is one line and still returns its own error: the step is the run's
// verdict, the summary is the line under it, and both have to be written
// before the command's error reaches cobra, which prints it (G8).
//
// context is what the failure has to be read with — a line this run had already
// earned and would otherwise lose with it — and it goes where
// report.Step.Failed puts its own: under the step, and in the log. Only a
// failure whose subject the run can no longer name any other way passes one.
func failStep(rep *report.Reporter, step *report.Step, err error, context ...string) error {
	step.Failed(err, context)
	rep.Summary()
	return err
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
