package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// required_cli.go is the required-CLI fence: the check every command passes
// through before it acts on a project (docs/required-version-stamp, T3).
//
// **What it refuses.** A project's installation carries a stamp — the oldest
// fugaro release that may act on it, written by `fugaro update` (T4) and read
// through internal/versionstamp (T1). A binary below it refuses every command
// that acts on that project, with one sentence and exit 1, and there is no
// flag, no environment switch and no prompt that overrides it (A5): the answer
// to a binary that is too old is `fugaro update` from a binary that is not.
// The fence is not a security boundary (IAM is), so every read failure fails
// open with one stderr line; the two cases that do not are a stamp that is
// present and malformed, and a cached value above this binary (D3264).
//
// **Where it runs.** `newCoreRoot`'s PersistentPreRunE, last, so everything the
// hook already did (the console-read mark, the checkout-parse cache, the
// plugin staleness line, the update check) is unchanged and the command's own
// flags are parsed by the time the fence reads them (R3). A new command is
// fenced by default: every exemption is a decision written down in one table
// below, and a command added tomorrow is fenced until somebody writes one.
//
// **Where it cannot run.** A command that resolves its project itself, because
// it has no local config to resolve: `selectProject` falls back to the shared
// config published to the runs bucket, and the fence is asked there instead
// (R4). The hook records its verdict in the command's context for that call
// site, which has no command to classify.
//
// **What it is not.** It does not remember the checkout it looks at
// (resolveCheckout, as the update check already does: a fence is not a reason
// for a command to write this machine's state), it does not depend on
// `updateCheckDisabled` (A5), and it fetches nothing: a command whose project
// exists only as a shared config is fenced by selectProject's own branch, once
// the fetch has happened.

// fenceVerdict is what the hook decided about one command: whether the fence
// asks about it at all, and why not, where it does not.
type fenceVerdict uint8

const (
	// fenceNone is no verdict: a context that never went through the hook, such
	// as a test calling a resolver directly. R4: not checked, because nothing
	// classified the command and a fence that guessed would refuse commands its
	// own table exempts.
	fenceNone fenceVerdict = iota
	// fenceFenced is the ordinary answer: the command acts on a project and the
	// fence asks whether this binary may.
	fenceFenced
	// fenceExempted is a command the fence never asks about, from the table
	// below.
	fenceExempted
	// fenceConsoleRead is a read the desktop console spawns: the fence asks,
	// but a stamp above this binary prints one line instead of refusing
	// (D3265), because blanking the one screen that could show the person the
	// problem is the wrong answer.
	fenceConsoleRead
	// fenceOnPlatform is a process that is a job, a build step or a service:
	// it acts on the installation's own behalf, under an image the installation
	// pins, and nothing a person's binary says about it applies (R6).
	fenceOnPlatform
)

// checks reports whether the fence asks this verdict's question. The two
// exemptions pass, and so does a platform; "fenced" and "console read" are the
// two flavours of being asked, and only the console read's answer differs.
func (v fenceVerdict) checks() bool {
	return v == fenceFenced || v == fenceConsoleRead
}

// fenceVerdictCtxKey carries the verdict from the hook to selectProject's
// shared-config branch, the one call site that has no command to classify.
type fenceVerdictCtxKey struct{}

// withFenceVerdict records v in ctx; the hook is the only caller.
func withFenceVerdict(ctx context.Context, v fenceVerdict) context.Context {
	return context.WithValue(ctx, fenceVerdictCtxKey{}, v)
}

// fenceVerdictFrom is the verdict ctx carries, and false for a context that
// never carried one (a test calling a resolver directly, a caller that reached
// selectProject without the hook). A missing verdict is not "fenced": R4.
func fenceVerdictFrom(ctx context.Context) (fenceVerdict, bool) {
	v, ok := ctx.Value(fenceVerdictCtxKey{}).(fenceVerdict)
	return v, ok
}

// fenceExemption is one entry of the table: why a command is not refused, and
// the one flag predicate an entry may carry.
type fenceExemption struct {
	// why is the decision the exemption comes from, for the next reader and for
	// the tree-walk test's failure message.
	why string
	// line is whether the command passes the way a console read does, with one
	// stderr line, rather than silently.
	line bool
	// applies, where set, is asked with the parsed command: the entry exempts
	// the command only when it answers true. Cobra has parsed the flags by the
	// time the hook runs, so this reads what the person typed.
	applies func(*cobra.Command) bool
}

// exempts reports whether e answers for cmd: an entry with no predicate always
// does.
func (e fenceExemption) exempts(cmd *cobra.Command) bool {
	return e.applies == nil || e.applies(cmd)
}

// fenceExemptions is the one table of commands the fence never refuses, keyed
// by cobra's command path (cmd.CommandPath(), e.g. "fugaro budget show") for
// the reason consoleReadPaths is keyed that way: a name is not enough, since
// "kill" is `budget kill` here and `replica kill` elsewhere, and a command path
// is what a person types and what the tree-walk test walks (R6).
//
// Every entry is a decision, and each one names it. The two entries a reader
// will ask about are `budget kill`, which is exempt in every form because
// stopping spend must always work (D3270), and `apply`, whose exemption is its
// read arm alone: `--status` changes nothing, and the bare word writes a
// project, so it is fenced (R5).
var fenceExemptions = map[string]fenceExemption{
	"fugaro version":          {why: "it answers what this binary is, which is the question the refusal would answer"},
	"fugaro help":             {why: "it answers what commands there are, and acts on nothing"},
	"fugaro doctor":           {why: "doctor reports the stamp as a check of its own instead (D3265's sibling, spec \"doctor\")"},
	"fugaro update":           {why: "update is the upgrade itself: it refuses below the stamp at its own door, before its first write (D3266, R8)"},
	"fugaro setup":            {why: "setup is the upgrade's other half and takes the same door (R8)"},
	"fugaro update-skills":    {why: "the plugin pin is part of the upgrade, so it may not be the thing that stops one"},
	"fugaro budget kill":      {why: "stopping spend must always work, in every form and --workspace included (D3270)"},
	"fugaro init":             {why: "the retired tombstone only says where init went and applies nothing"},
	"fugaro orchestrator run": {why: "the daemon reads the stamp itself at every tick and holds for an update rather than refusing (R7)"},
	// The in-cloud entry points: each is a process the installation itself
	// starts, on a machine whose image the installation pins, and each is
	// hidden because no person is meant to type it. R6 keeps them keyed by
	// path, so they also pass when somebody does type one on a laptop (O8).
	"fugaro exec":                 {why: "the in-container runner's entry point (R6)"},
	"fugaro credentials serve":    {why: "the credentials service's process (R6)"},
	"fugaro orchestrator serve":   {why: "the orchestrator service's process (R6)"},
	"fugaro voice serve":          {why: "the voice service's process (R6)"},
	"fugaro intake serve":         {why: "the intake service's process (R6)"},
	"fugaro coordinator serve":    {why: "the coordinator service's process (R6; registered from ee/)"},
	"fugaro budget history":       {why: "the history job's process (R6)"},
	"fugaro image gate":           {why: "a Cloud Build step: it gates an image the installation builds (R6)"},
	"fugaro image record":         {why: "a Cloud Build step: it records a built image (R6)"},
	"fugaro image git-credential": {why: "a Cloud Build step: the git credential helper of a build (R6)"},
	"fugaro image selftest":       {why: "a build's own self-check, which runs before any project is reachable (R6)"},
	// apply is the console's pending-steps read with --status, and the chain's
	// writer without it (D1569, D1572). Only the read arm is exempt, and it
	// prints the line: R5's "minus the write arm of apply".
	"fugaro apply": {
		why:     "apply --status is the console's pending-steps read, which writes nothing; the bare word writes a project (R5)",
		line:    true,
		applies: func(cmd *cobra.Command) bool { v, err := cmd.Flags().GetBool("status"); return err == nil && v },
	},
}

// fenceVerdictFor classifies cmd for the fence. The order is the decision:
// a platform first (a job's environment is what makes the whole question
// moot), then the table, then the console's reads, then cobra's own completion
// machinery, and a command none of those names is fenced.
//
// A path the table names that no command in this build has (an entry for a
// command only ee/ registers) simply never matches, which is why the table may
// hold one.
func fenceVerdictFor(cmd *cobra.Command) fenceVerdict {
	if backend.OnPlatform(os.Getenv) {
		return fenceOnPlatform
	}
	if e, ok := fenceExemptions[cmd.CommandPath()]; ok {
		if !e.exempts(cmd) {
			// The entry's predicate is this path's whole answer. `apply` is
			// the one such entry: --status is the console's read and the bare
			// word writes a project, so the console-read table below must not
			// hand back the exemption the predicate just refused (R5).
			return fenceFenced
		}
		if e.line {
			return fenceConsoleRead
		}
		return fenceExempted
	}
	if consoleReadPaths[cmd.CommandPath()] {
		return fenceConsoleRead
	}
	// The completion tree is excluded by name and by parent rather than by
	// path: cobra dispatches `fugaro completion zsh` to a "zsh" subcommand and
	// a shell's completion script runs the hidden `__complete` directly, so
	// cmd.CommandPath() reads neither "completion" nor the two hidden names in
	// the shape a table would need (inCompletionTree's own comment).
	if inCompletionTree(cmd) {
		return fenceExempted
	}
	return fenceFenced
}

// fenceCommand is the hook's last step: classify cmd, record the verdict where
// selectProject's shared-config branch can read it (R4), and run the fence.
//
// The verdict is recorded before the fence runs, and for every verdict: the
// shared-config branch is reached by commands whose local project did not
// exist, so what it needs is the classification, not this call's answer.
func fenceCommand(cmd *cobra.Command) error {
	v := fenceVerdictFor(cmd)
	ctx := withFenceVerdict(cmd.Context(), v)
	cmd.SetContext(ctx)
	if !v.checks() {
		return nil
	}
	return requireCLI(ctx, fenceProject(ctx, cmd), cmd.ErrOrStderr())
}

// fenceProject is the project a command's own facts select, resolved for the
// fence (R3): the command's parsed --config, --project and --gcp-project, then
// the environment and the checkout the command runs in, exactly as the
// command's own resolver reads them.
//
// Nil is "no project", and the fence passes: a machine where nothing selects a
// project config, or where the selection is refused, has no installation for a
// stamp to fence, and the command itself reports the refusal in its own words.
// The checkout is resolved through resolveCheckout, never checkoutProject: a
// command that runs in a checkout must not remember it because the fence looked
// (TestTheUpdateCheckLookupRemembersNothing pins the same rule for the update
// check).
func fenceProject(ctx context.Context, cmd *cobra.Command) *localcfg.Config {
	lc, _ := selectedProjectErr(ctx, fenceOptions(cmd), resolveCheckout)
	return lc
}

// fenceOptions is the command's own cloud flags as the resolver wants them.
// sharedOK stays false, which is what keeps this resolution offline: the
// shared config is selectProject's fallback to fetch, not the fence's.
func fenceOptions(cmd *cobra.Command) cloudOptions {
	o := cloudOptions{stderr: cmd.ErrOrStderr}
	f := cmd.Flags()
	if f.Lookup("config") != nil {
		o.config, _ = f.GetString("config")
	}
	if f.Lookup("project") != nil {
		o.project, _ = f.GetString("project")
	}
	if f.Lookup("gcp-project") != nil {
		o.gcpProject, _ = f.GetString("gcp-project")
	}
	return o
}

// requireCLI is the fence itself, given the project a command is about to act
// on: it refuses with *versionstamp.BelowError — the spec's one sentence, exit
// 1, on stderr, `--json` included — when this binary is below the project's
// required CLI.
//
// It has two call sites and they are the two places a project becomes known:
// the hook above, which resolves it from the command's own flags (fenceProject),
// and selectProject's shared-config branch, which has just fetched it (R4).
//
// It passes, in order: a project with no database (nothing to read), a dev or
// pre-release binary (nothing to compare: such a build skips the fence, spec
// "Dev builds"), a node that is not there (no fence), a stamp this binary is
// not below, and a console read, which is served with one stderr line rather
// than refused (D3265, fenceReadLine). A read that failed prints the one line
// the fail-open rule owes the person and passes; a stamp that is present and
// malformed is the error T1's reader returns, which no caller may fail open on.
func requireCLI(ctx context.Context, lc *localcfg.Config, errw io.Writer) error {
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil
	}
	if !updatecheck.Valid(Version) {
		return nil
	}
	res, err := newStampSource(versionstamp.Options{Open: stampOpen}).Required(ctx, versionstamp.Query{
		URL:    lc.Budget.RTDBURL,
		NoAuth: lc.Endpoints.NoAuth,
	})
	if err != nil {
		return err
	}
	switch {
	case res.Origin == versionstamp.OriginNone:
		fmt.Fprintln(errw, fenceUnreadLine(res.Reason))
		return nil
	case !res.Found:
		return nil
	case !versionstamp.Below(res.Stamp, Version):
		return nil
	case fenceReadLine(ctx):
		fmt.Fprintln(errw, fenceLine(res.Stamp))
		return nil
	}
	return &versionstamp.BelowError{Stamp: res.Stamp, Version: Version}
}

// fenceReadLine reports whether this call is serving a console read, which
// prints one line instead of refusing (D3265).
//
// The verdict is the answer, not consoleReadPaths' context mark, and the
// difference is `apply`: both arms are the path "fugaro apply", so the mark
// says console read for the bare word too, while the verdict is the one thing
// that tells the read arm from the write arm (R5). Every call site has a
// verdict — the hook records it before it calls, and selectProject's branch
// only calls on one — so a context without one is not a read.
func fenceReadLine(ctx context.Context) bool {
	v, ok := fenceVerdictFrom(ctx)
	return ok && v == fenceConsoleRead
}

// fenceLine is the one stderr line a console read prints instead of refusing
// (D3265). It carries the refusal's own facts and fix, and says why the read
// was served anyway, so a person reading the console's log has everything the
// refusal would have given them. It never goes to stdout: the console parses
// stdout, and a line there would be the read's answer corrupted rather than a
// warning beside it.
func fenceLine(stamp versionstamp.Stamp) string {
	return fmt.Sprintf("fugaro: note: this project requires fugaro %s or newer (set by fugaro update on %s) and this is %s; this is a console read, so it was served anyway: upgrade with %s",
		stamp.Min, stamp.SetAt.UTC().Format("2006-01-02"), Version, updatecheck.BrewCommand)
}

// fenceUnreadLine is the one stderr line a read failure owes the person
// (D3264): what could not be read and why, with nothing of a credential in it
// (versionstamp.Result.Reason's own rule). The command goes on — the fence is
// not a security boundary — and a command that needs the cloud fails on its
// own account, in its own words.
func fenceUnreadLine(reason string) string {
	return fmt.Sprintf("fugaro: note: could not read this project's required CLI (%s): %s", versionstamp.Path, reason)
}
