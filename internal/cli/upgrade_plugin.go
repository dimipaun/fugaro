package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/dimipaun/fugaro/internal/claudeplugin"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// claudeLookPath finds claude on PATH; tests replace it, so no test runs a
// developer's own claude.
var claudeLookPath = exec.LookPath

// noClaudeHint is the plugin step without claude on PATH: the commands the
// step would run, with the marketplace pinned to this release's tag, because
// the same update that needs them is the one a marketplace on the default
// branch cannot converge (a patch release's plugin version is on the tag, not
// on main).
func noClaudeHint(tag string) string {
	return "claude is not on PATH: in Claude Code run /plugin marketplace add " + pluginwire.Repo + "#" + tag +
		" (once), /plugin marketplace update " + pluginwire.Marketplace + " and /plugin install " + pluginwire.PluginID + ", then restart the session"
}

// pluginStep installs or updates the plugin in Claude Code through the claude
// CLI (claudeplugin: fixed calls, no shell, a timeout each; decisions U2 to
// U5). --check only reads Claude Code's record of installed plugins.
func pluginStep(ctx context.Context, u *upgradeCtx) stepResult {
	ver := releaseVersion()
	if ver == "" {
		return stepResult{state: stepSkipped, reason: "a development build has no release plugin to install"}
	}
	if u.o.check {
		return pluginCheck(u, ver)
	}
	fail := func(err error) stepResult {
		return stepResult{state: stepFailed, code: ExitUserError, reason: oneLine(err.Error())}
	}
	// The tag Claude Code's marketplace is declared at: releaseVersion is
	// X.Y.Z, so Tag cannot fail here. Nothing reads a pin off the checkout any
	// more (D949): the marketplace this step declares is Fugaro's own.
	tag, err := pluginwire.Tag(ver)
	if err != nil {
		return fail(err)
	}
	bin, err := claudeplugin.Find(claudeLookPath)
	if errors.Is(err, claudeplugin.ErrNoClaude) {
		return stepResult{state: stepSkipped, reason: noClaudeHint(tag)}
	}
	if err != nil {
		return fail(err)
	}
	repo := pluginwire.Repo
	fmt.Fprintf(u.w, "  using claude at %s\n", pluginwire.Printable(bin))
	run := &claudeplugin.Runner{Bin: bin, Dir: u.loc.Root, Out: u.w}
	markets, err := run.Markets(ctx)
	if err != nil {
		return fail(err)
	}
	installed, err := run.Installed(ctx)
	if err != nil {
		return fail(err)
	}
	plan, err := claudeplugin.Decide(claudeplugin.Input{Root: u.loc.Root, Want: ver, Ref: tag, Repo: repo, Markets: markets, Installed: installed})
	if err != nil {
		return fail(err)
	}
	if len(plan.Calls) == 0 {
		return stepResult{state: stepCurrent, reason: "installed " + pluginwire.Printable(plan.Before)}
	}
	before, _ := os.ReadFile(u.loc.Settings)
	// cleared is whether a Clear ran, and so whether a failed add has anything
	// to put back: claude's removal takes the marketplace declaration and every
	// install of it, so a failed step must not leave the machine without either
	// (a clear of a name claude has not declared fails, and then there is
	// nothing to put back).
	cleared := false
	// putBack runs what the clear removed, before the step reports a stop:
	// claude's removal takes the declaration and every install of it, and a
	// step that stopped after the clear must not leave the machine without
	// them. The calls are best effort and say so when they fail: once this
	// release's tag is declared, claude refuses the name at the source it had,
	// and the install from the declaration that stands is what puts the plugin
	// back; and when the failure is the network, the clone that would put the
	// declaration back fails the same way.
	putBack := func() {
		if !cleared {
			return
		}
		for _, args := range plan.Restore {
			if rerr := run.Change(ctx, args...); rerr != nil {
				fmt.Fprintf(u.w, "  putting back what the clear removed failed: %v\n", rerr)
			}
		}
	}
	for _, c := range plan.Calls {
		err := run.Change(ctx, c.Args...)
		if err == nil {
			if c.Kind == claudeplugin.Clear {
				cleared = true
			}
			continue
		}
		switch c.Kind {
		case claudeplugin.Clear:
			// The declaration Claude Code refuses an add by: clearing one it
			// does not have fails the same way, and the add below is what
			// decides whether the marketplace ends up at this release's tag.
			fmt.Fprintf(u.w, "  continuing without clearing: %v\n", err)
		case claudeplugin.Declare:
			// The one add failure fugaro cannot fix: a source is declared for
			// the name in settings fugaro does not own and must not rewrite, and
			// claude's own message says so. The version cannot be had on this
			// machine, and the checkout's other steps are not this one's to
			// stop: the step says exactly that, and hands the user the commands
			// that do it by hand. Any other failure — a clone that failed, a
			// network that is down — fails the step. Either way what the clear
			// removed went back above, so neither outcome leaves the machine
			// without the marketplace and the plugin it had.
			putBack()
			if !claudeplugin.RefusedByDeclaration(err) {
				return fail(err)
			}
			return stepResult{state: stepSkipped, reason: fmt.Sprintf("claude would not declare the %s marketplace at %s (%s): in Claude Code run /plugin marketplace remove %s, then /plugin marketplace add %s#%s and /plugin install %s, then rerun; this update's other steps do not need it",
				pluginwire.Marketplace, tag, oneLine(err.Error()), pluginwire.Marketplace, repo, tag, pluginwire.PluginID)}
		case claudeplugin.Repair:
			// A call the clear may have taken the need for: the install that
			// puts the plugin back, or the update of an install the clear took
			// (claude answers that it has no such install) or left standing.
			// Neither failure is the step's to report, because the check after
			// the calls sees what came out of them, and anything that leaves the
			// version where it was is a failed step there.
			fmt.Fprintf(u.w, "  continuing without that call: %v\n", err)
		default:
			// The update's own work, after a clear: a failure here is the
			// step's, and the machine is not left without what the clear took.
			putBack()
			return fail(err)
		}
	}
	note := ""
	if after, _ := os.ReadFile(u.loc.Settings); !bytes.Equal(before, after) {
		note = "; claude changed " + pluginwire.Printable(u.loc.Settings) + ": review it with git diff"
	}
	if installed, err = run.Installed(ctx); err != nil {
		// The read of the installs can fail on its own (the record is
		// unreadable, say), and that stop is after the clear like any other.
		putBack()
		return fail(err)
	}
	got := claudeplugin.Effective(u.loc.Root, installed)
	c, ok := claudeplugin.Compare(got, ver)
	switch {
	case !ok:
		putBack()
		return fail(fmt.Errorf("after the update claude lists no install of %s that applies to this checkout", pluginwire.PluginID))
	case c < 0:
		putBack()
		return fail(fmt.Errorf("after the update the installed plugin is still %s, older than this fugaro %s", pluginwire.Printable(got), ver))
	case c > 0:
		return stepResult{state: stepDone, reason: fmt.Sprintf("installed %s, newer than this fugaro %s (Claude Code's marketplace is pinned to a newer release, and the pin is never moved down): brew upgrade dimipaun/tap/fugaro, then fugaro upgrade; restart running Claude Code sessions to apply it%s",
			pluginwire.Printable(got), ver, note)}
	}
	return stepResult{state: stepDone, reason: "installed " + ver + "; restart running Claude Code sessions to apply it" + note}
}

// pluginCheck is the plugin step under --check: Claude Code's record of
// installed plugins compared with this fugaro, no claude run.
func pluginCheck(u *upgradeCtx, ver string) stepResult {
	r := pluginwire.Status(u.loc.Settings, Version, installedPlugins())
	if r.Install == pluginwire.NotInstalled {
		return stepResult{state: stepSkipped, reason: "not installed on this machine (fugaro upgrade installs it through the claude CLI)"}
	}
	c, ok := claudeplugin.Compare(r.Version, ver)
	switch {
	case !ok:
		return stepResult{state: stepSkipped, reason: "cannot tell from Claude Code's record of installed plugins (fugaro upgrade asks claude itself)"}
	case c < 0:
		return stepResult{state: stepStale, reason: fmt.Sprintf("installed %s, this fugaro is %s", r.Version, ver)}
	case c > 0:
		return stepResult{state: stepCurrent, reason: fmt.Sprintf("installed %s, newer than this fugaro %s (brew upgrade dimipaun/tap/fugaro)", r.Version, ver)}
	}
	return stepResult{state: stepCurrent, reason: "installed " + r.Version}
}
