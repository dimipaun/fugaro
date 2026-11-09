package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	// The pin the checkout's own settings carry, and the one Claude Code's
	// marketplace is declared at: releaseVersion is X.Y.Z, so Tag cannot fail
	// here, and a marketplace is never pinned to anything but a release tag.
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
	repo, why := marketRepo(u)
	if repo == "" {
		return stepResult{state: stepSkipped, reason: why}
	}
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
	for _, c := range plan.Calls {
		if err := run.Change(ctx, c...); err != nil {
			return fail(err)
		}
	}
	note := ""
	if after, _ := os.ReadFile(u.loc.Settings); !bytes.Equal(before, after) {
		note = "; claude changed " + pluginwire.Printable(u.loc.Settings) + ": review it with git diff"
	}
	if installed, err = run.Installed(ctx); err != nil {
		return fail(err)
	}
	got := claudeplugin.Effective(u.loc.Root, installed)
	c, ok := claudeplugin.Compare(got, ver)
	switch {
	case !ok:
		return fail(fmt.Errorf("after the update claude lists no install of %s that applies to this checkout", pluginwire.PluginID))
	case c < 0:
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

// marketRepo is the marketplace repository the plugin step passes to claude:
// dimipaun/fugaro, or, with --allow-fork, the fork the checkout's pin names
// when it is a valid owner/name; "" and the reason otherwise.
func marketRepo(u *upgradeCtx) (repo, why string) {
	r := pluginwire.Status(u.loc.Settings, Version, "")
	switch {
	case r.Repo == "" || strings.EqualFold(r.Repo, pluginwire.Repo):
		return pluginwire.Repo, ""
	case !u.o.allowFork:
		return "", "the checkout's marketplace is " + pluginwire.Printable(r.Repo) + ", not " + pluginwire.Repo + ": pass --allow-fork if that fork is yours"
	case !claudeplugin.ValidRepo(r.Repo):
		return "", "the checkout's marketplace " + pluginwire.Printable(r.Repo) + " is not a GitHub owner/name fugaro passes to claude"
	}
	return r.Repo, ""
}
