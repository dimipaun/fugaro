package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// noUpdateCheckEnv disables the daily update check and its nag outright
// (fugaro doctor's own cli-version check ignores it: design "update
// warnings", point 3).
const noUpdateCheckEnv = "FUGARO_NO_UPDATE_CHECK"

// updateCheckExcluded are the commands the nag and the background check
// never run for: the in-container runner (also caught by
// backend.OnCloudRun, but named here too so a local `fugaro exec` never
// fires either), the two commands meant for a script or a shell's own
// completion wiring (sourced on every new shell for some users), and
// `version` itself, which already answers "what version is this".
var updateCheckExcluded = map[string]bool{"exec": true, "completion": true, "help": true, "version": true}

// updateCheckHTTPClient and updateCheckEndpoint are test seams: production
// uses the real client and GitHub's endpoint, tests point at a fake
// server.
var (
	updateCheckHTTPClient updatecheck.Client = &http.Client{}
	updateCheckEndpoint                      = updatecheck.Endpoint
)

// stderrIsTerminal is isTTY, a test seam (as watchStdoutTTY already is for
// stdout).
var stderrIsTerminal = isTTY

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

// updateCheckDisabledFromContext tolerates a nil ctx: cobra leaves a
// matched command's own ctx unset when Find/Traverse itself fails (an
// unknown command, a bad flag) before PersistentPreRun ever runs, and
// WarnUpdateAvailable is called unconditionally on whatever ExecuteContextC
// returns.
func updateCheckDisabledFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(updateCheckCtxKey{}).(bool)
	return v
}

// updateCheckDisabled reports whether the whole feature is off for this
// command: FUGARO_NO_UPDATE_CHECK=1, or the selected project's local
// config says update_check: false. The local config lookup
// (selectedProjectConfig: a git rev-parse and a config file read, no
// network or cloud call) is the most expensive check here, so it is only
// reached once every cheaper reason to skip it entirely — the env switch,
// an excluded command, running inside a Cloud Run job — has already not
// applied; shouldStartUpdateCheck and shouldWarnUpdateAvailable check the
// same two conditions again regardless of what this returns, so treating
// them as "disabled" here changes nothing observable.
func updateCheckDisabled(cmd *cobra.Command) bool {
	if os.Getenv(noUpdateCheckEnv) == "1" {
		return true
	}
	if updateCheckExcluded[cmd.Name()] || backend.OnCloudRun(os.Getenv) {
		return true
	}
	if lc := selectedProjectConfig(cmd.Context()); lc != nil {
		return !lc.UpdateCheckEnabled()
	}
	return false
}

// shouldStartUpdateCheck is startUpdateCheck's decision, pulled out so
// tests can check each condition without racing a real goroutine: no call
// at all when disabled, inside a Cloud Run job, for an excluded command,
// or for a dev/unparseable binary version (there would be nothing to ever
// compare the result against).
func shouldStartUpdateCheck(cmd *cobra.Command, disabled bool) bool {
	return !disabled && !updateCheckExcluded[cmd.Name()] && !backend.OnCloudRun(os.Getenv) && updatecheck.Valid(Version)
}

// updateCheckWG tracks the background Refresh goroutine startUpdateCheck
// may launch. main.go calls WaitForUpdateCheck before the process exits:
// without joining it, a command whose own work finishes before the HTTP
// GET and the cache Save do (common for a fast, local-only command racing
// FetchTimeout's ~1s budget) would have main return and the process exit
// right under it, silently dropping that day's check instead of
// completing it — unlike every other background goroutine in this
// package (ls.go, watch_queued.go, initsecrets.go, secrets.go), which all
// join before their command returns.
var updateCheckWG sync.WaitGroup

// startUpdateCheck kicks the daily GitHub lookup in the background
// (design "update warnings", point 1): it never blocks the command. It
// uses its own context, independent of cmd's: main unregisters cmd's
// signal context right after the command returns, which would cancel the
// fetch before its own short timeout if it shared that context.
func startUpdateCheck(cmd *cobra.Command, disabled bool) {
	if !shouldStartUpdateCheck(cmd, disabled) {
		return
	}
	updateCheckWG.Add(1)
	go func() {
		defer updateCheckWG.Done()
		updatecheck.Refresh(context.Background(), os.Getenv, updateCheckHTTPClient, updateCheckEndpoint, time.Now())
	}()
}

// WaitForUpdateCheck blocks until any background daily-check goroutine
// startUpdateCheck launched for the command that just ran has finished,
// bounded by its own FetchTimeout (~1s). cmd/fugaro/main.go calls it right
// after ExecuteContextC returns and before the process can exit; a command
// that never started one (excluded, disabled, inside a Cloud Run job, a
// dev build) returns at once, since updateCheckWG was never incremented.
func WaitForUpdateCheck() {
	updateCheckWG.Wait()
}

// shouldWarnUpdateAvailable is warnUpdateAvailable's decision, pulled out
// for the same reason shouldStartUpdateCheck is: silence for an excluded
// command, under --json/--plain, off a terminal, inside a Cloud Run job,
// or when disabled.
func shouldWarnUpdateAvailable(cmd *cobra.Command, disabled bool) bool {
	return !disabled && !updateCheckExcluded[cmd.Name()] && !machineReadable(cmd) && stderrIsTerminal(cmd.ErrOrStderr()) && !backend.OnCloudRun(os.Getenv)
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
func WarnUpdateAvailable(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	warnUpdateAvailable(cmd, updateCheckDisabledFromContext(cmd.Context()))
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
