package cli

import (
	"context"
	"errors"
	"fmt"
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
		// plan.Before is the machine's own install (claudeplugin.UserVersion),
		// which is the one this step maintains and the one pluginCheck reads off
		// Claude Code's record (D949, U2): a checkout-scoped install standing at
		// another version is not reported here as if it were the machine's.
		return stepResult{state: stepCurrent, reason: "installed " + pluginwire.Printable(plan.Before)}
	}
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
	// The read of the installs can fail on its own (the record is unreadable,
	// say), and that stop is after the clear like any other.
	if installed, err = run.Installed(ctx); err != nil {
		putBack()
		return fail(err)
	}
	// What the step converged is the install it maintains: the machine's own,
	// at user scope. A checkout-scoped install is not this step's to move any
	// more (D949, U2), so one standing behind this release — a person's, or one
	// a trusted folder made from the pin it carried — is not a failure here:
	// what applies to the checkout is a question no reader of this line asks,
	// and nothing in the tree asks it (claudeplugin.Effective has no caller).
	got := claudeplugin.UserVersion(installed)
	c, ok := claudeplugin.Compare(got, ver)
	switch {
	case !ok:
		putBack()
		return fail(fmt.Errorf("after the update claude lists no install of %s at user scope", pluginwire.PluginID))
	case c < 0:
		putBack()
		return fail(fmt.Errorf("after the update the installed plugin is still %s, older than this fugaro %s", pluginwire.Printable(got), ver))
	case c > 0:
		return stepResult{state: stepDone, reason: fmt.Sprintf("installed %s, newer than this fugaro %s (Claude Code's marketplace is pinned to a newer release, and the pin is never moved down): brew upgrade dimipaun/tap/fugaro, then fugaro upgrade; restart running Claude Code sessions to apply it",
			pluginwire.Printable(got), ver)}
	}
	return stepResult{state: stepDone, reason: "installed " + ver + "; restart running Claude Code sessions to apply it"}
}

// pluginCheck is the plugin step under --check: Claude Code's record of
// installed plugins compared with this fugaro, no claude run. It answers about
// the install the write path maintains, the machine's own at user scope
// (claudeplugin.UserVersion), which Status reads off the record whether or not
// the checkout carries a pin (pluginwire.Report.UserInstall): what applies to
// the checkout is another question — doctor's, and the staleness warning's —
// and a checkout-scoped install with nothing at user scope would answer it with
// a version while the machine's own Claude Code has no plugin, which is the
// install this step's write still makes (D949, U2).
func pluginCheck(u *upgradeCtx, ver string) stepResult {
	r := pluginwire.Status(u.loc.Settings, Version, installedPlugins())
	switch r.UserInstall {
	case pluginwire.NotInstalled:
		return stepResult{state: stepSkipped, reason: "not installed on this machine (fugaro upgrade installs it through the claude CLI)"}
	case pluginwire.OK, pluginwire.InstalledDiffers:
		// Only these two carry a version read out of the record; anything else
		// about it — a file that cannot be read, a build with no release tag —
		// is what the line below says it cannot tell.
	default:
		return stepResult{state: stepSkipped, reason: "cannot tell from Claude Code's record of installed plugins (fugaro upgrade asks claude itself)"}
	}
	c, ok := claudeplugin.Compare(r.UserVersion, ver)
	switch {
	case !ok:
		return stepResult{state: stepSkipped, reason: "cannot tell from Claude Code's record of installed plugins (fugaro upgrade asks claude itself)"}
	case c < 0:
		return stepResult{state: stepStale, reason: fmt.Sprintf("installed %s, this fugaro is %s", r.UserVersion, ver)}
	case c > 0:
		return stepResult{state: stepCurrent, reason: fmt.Sprintf("installed %s, newer than this fugaro %s (brew upgrade dimipaun/tap/fugaro)", r.UserVersion, ver)}
	}
	return stepResult{state: stepCurrent, reason: "installed " + r.UserVersion}
}
