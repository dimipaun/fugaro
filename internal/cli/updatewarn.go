package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// noUpdateCheckEnv disables the daily update check and its nag outright
// (fugaro doctor's own cli-version check ignores it: design "update
// warnings", point 3).
const noUpdateCheckEnv = "FUGARO_NO_UPDATE_CHECK"

// updateCheckExcluded are the commands the nag and the background check
// never run for: the in-container runner (also caught by
// backend.OnPlatform, but named here too so a local `fugaro exec` never
// fires either), `help`, and `version` itself, which already answers
// "what version is this". Completion is excluded separately
// (excludedCommand/inCompletionTree below): cobra dispatches `fugaro
// completion zsh` to a real "zsh" subcommand, and a shell's completion
// script invokes the hidden `__complete`/`__completeNoDesc` commands
// directly, so cmd.Name() alone never reads "completion" for any of them.
var updateCheckExcluded = map[string]bool{"exec": true, "help": true, "version": true}

// inCompletionTree reports whether cmd is cobra's own completion
// machinery: a generated shell subcommand of `completion`
// (`completion zsh`, `completion bash`, ... — cmd.Name() is "zsh"/"bash",
// never "completion", so this walks the parent chain instead) or one of
// the two hidden completion-request commands a shell's completion script
// runs on every Tab press (`__complete`, `__completeNoDesc`), which
// cmd.Name() does name directly. Reproduced (PR #296 review): without
// this, `source <(fugaro completion zsh)` in a shell rc file, and every
// Tab press afterward, nagged and could spend the day's one GitHub
// request on a keystroke.
func inCompletionTree(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "__complete", "__completeNoDesc":
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "completion" {
			return true
		}
	}
	return false
}

// excludedCommand reports whether cmd is one the nag and the background
// check never run for at all.
func excludedCommand(cmd *cobra.Command) bool {
	return updateCheckExcluded[cmd.Name()] || inCompletionTree(cmd)
}

// updateCheckHTTPClient and updateCheckEndpoint are test seams: production
// uses the real client and GitHub's endpoint, tests point at a fake
// server. The client never follows a redirect: GitHub's own API never
// issues one for this endpoint, so any redirect is either a
// misconfiguration or a hostile responder, and following one would GET
// whatever host it names next, Referer header and all (confirmed by
// reproduction: a plain &http.Client{} followed a 302 to an arbitrary
// second host and sent it a Referer naming the first).
var (
	updateCheckHTTPClient updatecheck.Client = &http.Client{CheckRedirect: refuseRedirect}
	updateCheckEndpoint                      = updatecheck.Endpoint
)

// refuseRedirect makes an *http.Client return the redirect response itself
// (3xx, unread) instead of following it.
func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// stderrIsTerminal is a test seam (as stdinIsTerminal and watchStdoutTTY
// already are for their own streams). It deliberately does NOT reuse
// isTTY: isTTY only checks os.ModeCharDevice, and /dev/null is a character
// device too, so isTTY alone would treat `fugaro ... 2>/dev/null` as a
// real terminal — exactly what cobra's generated shell completion scripts
// redirect stderr to (reproduced, PR #296 review: `fugaro config example
// 2>/dev/null` with a cached newer release wrote "warned" to the cache
// without a real terminal ever seeing the line that day). realTerminal
// uses golang.org/x/term's IsTerminal, an actual ioctl, already a
// dependency here for secrets' hidden prompts (stdinIsTerminal in
// init.go); not changed: isTTY itself and its other caller,
// watchStdoutTTY, whose own stdout-is-a-screen question isTTY already
// answers correctly (a redirected `fugaro watch > file` should draw no
// screen, which ModeCharDevice alone gets right for that case).
var stderrIsTerminal = realTerminal

// realTerminal reports whether w is an actual terminal (not a pipe, not
// /dev/null, not any other character device masquerading as one to
// isTTY's cruder check).
func realTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// updateCheckCtxKey is the PersistentPreRun-computed disable state, carried
// on the command's context to wherever the command ends up (WarnUpdateAvailable,
// called from cmd/fugaro/main.go after the command returns, success or
// not) so it is resolved (env plus a local config lookup) only once per
// command.
type updateCheckCtxKey struct{}

// withUpdateCheckDisabled and updateCheckDisabledFromContext carry
// updateCheckDisabled's result from PersistentPreRun onward, so a command
// that resolves a project (selectedProjectConfig: a git rev-parse and a
// config file read) only pays for it once.
func withUpdateCheckDisabled(ctx context.Context, disabled bool) context.Context {
	return context.WithValue(ctx, updateCheckCtxKey{}, disabled)
}

// updateCheckDisabledFromContext reports whether PersistentPreRun ever set
// a value, and what it was: ok is false when it never ran at all — a nil
// ctx (cobra leaves a matched command's own ctx unset when Find/Traverse
// itself fails before cmd.execute is ever called), a flag parse error, a
// failing Args validator, or --help (all of these return out of cobra's
// Command.execute before the PersistentPreRun loop; see NewRootCmd's
// comment) — in which case the local-config half of the decision
// (update_check: false) was never resolved. WarnUpdateAvailable fails
// closed on ok == false rather than assume "not disabled": a command this
// broken is not a real invocation to nag about, and guessing wrong here
// would print the nag despite FUGARO_NO_UPDATE_CHECK=1 or update_check:
// false, which it did until this was found by review (PR #296).
func updateCheckDisabledFromContext(ctx context.Context) (disabled, ok bool) {
	if ctx == nil {
		return false, false
	}
	disabled, ok = ctx.Value(updateCheckCtxKey{}).(bool)
	return disabled, ok
}

// updateCheckDisabled reports whether the whole feature is off for this
// command: FUGARO_NO_UPDATE_CHECK=1, or the selected project's local
// config says update_check: false. The local config lookup
// (selectedProjectConfigReadOnly: a git rev-parse and a config file read,
// no network or cloud call, and no recording of the checkout it looked at)
// is the most expensive check here, so it is only
// reached once every cheaper reason to skip it entirely — the env switch,
// an excluded command, running inside a Cloud Run job — has already not
// applied; shouldStartUpdateCheck and shouldWarnUpdateAvailable check the
// same two conditions again regardless of what this returns, so treating
// them as "disabled" here changes nothing observable.
func updateCheckDisabled(cmd *cobra.Command) bool {
	if os.Getenv(noUpdateCheckEnv) == "1" {
		return true
	}
	if excludedCommand(cmd) || backend.OnPlatform(os.Getenv) {
		return true
	}
	// selectedProjectConfigReadOnly and not selectedProjectConfig: this runs
	// on every command's PersistentPreRun, before the command has done
	// anything, and recording the checkout it looks at would make every
	// command in a checkout write the remembered checkouts — `fugaro config
	// list` and `fugaro project ls` included, which are reads the console
	// spawns under the rule that a read changes nothing. The question here
	// ("does this project's config say update_check: false") has the same
	// answer either way.
	if lc := selectedProjectConfigReadOnly(cmd.Context()); lc != nil {
		return !lc.UpdateCheckEnabled()
	}
	return false
}

// ciEnv is set (to any non-empty value) by essentially every CI system
// (GitHub Actions, Travis, CircleCI and others all set at least CI=true);
// shouldStartUpdateCheck treats any of them the same as a non-terminal
// stderr: a CI run would never see the nag printed anyway (CI output is
// not a terminal, and most CI redirects or captures both streams), so
// there is nothing to gain from the network call, and a cold CI runner
// hitting GitHub's unauthenticated rate limit (and WaitForUpdateCheck
// joining it) on every job is a cost for nothing.
const ciEnv = "CI"

// shouldStartUpdateCheck is startUpdateCheck's decision, pulled out so
// tests can check each condition without racing a real goroutine: no call
// at all when disabled, inside a Cloud Run job, in CI, off a terminal (the
// nag could never print anyway), for `doctor` (which does its own
// synchronous Refresh as part of its cli-version check: running the
// background one too would be a second, redundant GitHub request every
// time doctor runs), for an excluded command, or for a dev/unparseable
// binary version (there would be nothing to ever compare the result
// against).
func shouldStartUpdateCheck(cmd *cobra.Command, disabled bool) bool {
	return !disabled && !excludedCommand(cmd) && cmd.Name() != "doctor" &&
		os.Getenv(ciEnv) == "" && stderrIsTerminal(cmd.ErrOrStderr()) &&
		!backend.OnPlatform(os.Getenv) && updatecheck.Valid(Version)
}

// updateCheckDone, when non-nil, is closed when the background Refresh
// goroutine the most recent startUpdateCheck call launched finishes. Not a
// sync.WaitGroup: a WaitGroup's contract forbids a new Add from
// overlapping an older Wait that hasn't fully returned on every goroutine
// waiting on it, which an abandoned WaitForUpdateCheck call (one that gave
// up at updateCheckJoinBound while its own helper goroutine is still
// blocked in Wait) can leave dangling into the next command's Add — a
// hazard `go test -race` catches across this package's own sequential
// tests, which call these functions many times in one process the way
// main.go, calling each exactly once, never does. A fresh channel per
// check sidesteps the whole class of reuse hazard: closing one with no
// reader left (because WaitForUpdateCheck already gave up) is always
// safe, and goroutines reading/writing updateCheckDone itself are never
// concurrent (PersistentPreRun and the main.go call after Execute returns
// run strictly one after the other).
var updateCheckDone chan struct{}

// updateCheckJoinBound is how long WaitForUpdateCheck waits for the
// background check before giving up on it; a var so a test can shrink it.
var updateCheckJoinBound = 200 * time.Millisecond

// startUpdateCheck kicks the daily GitHub lookup in the background
// (design "update warnings", point 1): it never blocks the command. It
// uses its own context, independent of cmd's: main unregisters cmd's
// signal context right after the command returns, which would cancel the
// fetch before its own short timeout if it shared that context.
func startUpdateCheck(cmd *cobra.Command, disabled bool) {
	if !shouldStartUpdateCheck(cmd, disabled) {
		return
	}
	done := make(chan struct{})
	updateCheckDone = done
	go func() {
		defer close(done)
		updatecheck.Refresh(context.Background(), os.Getenv, updateCheckHTTPClient, updateCheckEndpoint, time.Now())
	}()
}

// WaitForUpdateCheck waits, up to updateCheckJoinBound, for the background
// daily-check goroutine startUpdateCheck launched for the command that
// just ran. cmd/fugaro/main.go calls it right after ExecuteContextC
// returns and before the process can exit. A command that never started
// one (excluded, disabled, inside a Cloud Run job, a dev build) returns at
// once, since updateCheckDone is nil; one that is still running past the
// bound (a slow or blackholed network) is abandoned — the process exits
// without it, rather than visibly holding up an otherwise-fast command for
// up to Refresh's own ~1s FetchTimeout.
func WaitForUpdateCheck() {
	if updateCheckDone == nil {
		return
	}
	select {
	case <-updateCheckDone:
	case <-time.After(updateCheckJoinBound):
	}
}

// shouldWarnUpdateAvailable is warnUpdateAvailable's decision, pulled out
// for the same reason shouldStartUpdateCheck is: silence for an excluded
// command, --help (cobra normally returns out of Command.execute before
// PersistentPreRun even runs for --help, so updateCheckDisabledFromContext's
// ok=false already catches it; this is a second, direct check in case that
// ever stops being true), under --json/--plain, off a terminal, inside a
// Cloud Run job, or when disabled. The env switch is re-checked directly
// here, not only through the disabled parameter: WarnUpdateAvailable
// already fails closed (never calls this at all) when PersistentPreRun
// never resolved disabled, but FUGARO_NO_UPDATE_CHECK=1 costs nothing to
// ask again and must never depend on that resolution having happened.
func shouldWarnUpdateAvailable(cmd *cobra.Command, disabled bool) bool {
	return !disabled && os.Getenv(noUpdateCheckEnv) != "1" &&
		!excludedCommand(cmd) && !helpRequested(cmd) &&
		!machineReadable(cmd) && stderrIsTerminal(cmd.ErrOrStderr()) && !backend.OnPlatform(os.Getenv)
}

// warnUpdateAvailable prints the cached nag line (design "update
// warnings", point 2), "" most of the time: nothing but a local cache
// read, no network call. A dev/unparseable binary version never prints
// either, through updatecheck.Warn's own use of updatecheck.Newer.
func warnUpdateAvailable(cmd *cobra.Command, disabled bool) {
	if !shouldWarnUpdateAvailable(cmd, disabled) {
		return
	}
	if line := updatecheck.Warn(os.Getenv, time.Now(), Version); line != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), line)
	}
}

// WarnUpdateAvailable is warnUpdateAvailable for cmd/fugaro/main.go: the
// command this package's root.go attached PersistentPreRun to, whatever
// ExecuteContextC returned, success or failure (cmd is never nil there,
// but nil is tolerated so this is safe to call unconditionally). The nag
// is not printed from a PersistentPostRun (see root.go's NewRootCmd
// comment): cobra's Command.execute returns immediately when the leaf
// command's RunE errors, which most command failures do, skipping
// PostRun and PersistentPostRun entirely — so main must call this itself,
// after the command's own output (including any printed error), whether
// or not it errored.
//
// Fails closed when PersistentPreRun never ran at all (a flag parse
// error, a failing Args validator, an unknown command, --help: see
// updateCheckDisabledFromContext), rather than assume "not disabled" —
// reproduced (PR #296 review) as the nag printing for `ls --bogus`,
// `doctor x y z`, `nosuchcmd`, `ls --help` and bare `fugaro` even with
// FUGARO_NO_UPDATE_CHECK=1 set, since none of those ever reach the code
// that resolves the local config's update_check.
func WarnUpdateAvailable(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	disabled, ok := updateCheckDisabledFromContext(cmd.Context())
	if !ok {
		return
	}
	warnUpdateAvailable(cmd, disabled)
}

// helpRequested reports whether cmd's auto-registered --help flag is set;
// false, never a panic, for a bare test command that never had one
// registered (machineReadable's "json"/"plain" lookups use the same
// nil-safe pattern).
func helpRequested(cmd *cobra.Command) bool {
	f := cmd.Flags().Lookup("help")
	return f != nil && f.Value.String() == "true"
}

// machineReadable reports whether cmd was asked for --json or --plain
// output: both are meant to be parsed, not read, so the nag never joins
// either.
func machineReadable(cmd *cobra.Command) bool {
	for _, name := range []string{"json", "plain"} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Value.String() == "true" {
			return true
		}
	}
	return false
}
