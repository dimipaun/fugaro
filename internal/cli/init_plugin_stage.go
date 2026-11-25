package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dimipaun/fugaro/internal/claudeplugin"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The plugin stage (stage 8): the plugin installed, or updated, in the user's
// own Claude Code through the claude CLI, at user scope. The merge that wrote a
// pin into the checkout was deleted outright (D949, answered 2026-11-04; U2 of
// docs/plugin-user-scope/implementation-plan.md), and since U6a nothing writes
// one either: a pin an older fugaro left is a notice `update` prints after the
// loop (repo_notices.go, D3340), so this stage writes no checkout at all and
// every write it makes is on this machine, in the user's own plugin
// installation.
//
// The stage is here and not in the checkout stage because it runs before the
// repository stage (initflow's order, F3): a session whose repository stage
// returns needs-you — a repository not enrolled, a GitHub App ID that is
// missing, an invalid fugaro.yaml — never reaches the checkout, and the install
// is the step that clears the staleness line every command prints ("run fugaro
// update"), so a run that leaves it behind leaves that reader in a loop with no
// way out (reconciliation.md F13, #1836). It is LocalUnderAgent because what it
// writes is on this machine: the user's own Claude Code, on files its session
// reads anyway. Nothing here reaches the cloud, which is what the marker's
// refusal is about, and it is the step that clears the line every command
// prints. That is also what makes it initflow.MachineLocal (D954, U8): the
// stages in front of it are the cloud's, a reader who holds no owner's role can
// be stopped by any of them, and this stage's own writes are not what those
// stages were guarding — so it runs although one of them stopped, and its line
// says which.
//
// The stage reads no checkout gate (D955, U8): the plugin it installs is the
// same one for every checkout, it concerns no repository, and the gate that
// consults the machine's config would send every teammate to fugaro setup for a
// write that is not the repository's (F24). So the install happens in any
// directory, checkout or not, and a new machine's empty repository list is no
// answer about the machine's own plugin.

type pluginStage struct {
	e *initEngine
	// loc is the fixed directory the install half runs claude in, and nothing
	// it does is about a repository (U6a). The plugin this stage installs is
	// this machine's, at user scope, and the directory claude is spawned in is
	// a cwd it needs and nothing more.
	loc pluginwire.Location
	// installStep is whether the stage's own something-to-do is the install
	// half's (Check): it is this run taking the machine's plugin to this
	// release, and the checkout's own cloud half moves with it.
	installStep bool
}

func newPluginStage(e *initEngine) *pluginStage { return &pluginStage{e: e} }

func (*pluginStage) Name() string { return initflow.Plugin }

// LocalUnderAgent: in a coding agent's session the loop applies this stage
// instead of leaving it for the user (initflow.LocalUnderAgent), because every
// write it makes is on this machine — the plugin in the user's own Claude Code
// through the claude CLI — and that is the write the user's own session makes
// with the converge, on files it reads anyway.
func (*pluginStage) LocalUnderAgent() {}

func (s *pluginStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftCommand, Text: selfCommand() + " update-skills"}
}

// locate sets the fixed directory the install half runs claude in. It is the
// operating system's temporary directory, which always exists, is writable, and
// is never a checkout: what this stage writes is this machine's plugin, and the
// directory claude is spawned in is nothing but a cwd it needs (U6a). Nothing
// here reads the working directory — that was the pin's, and the pin is a
// notice now (repo_notices.go).
func (s *pluginStage) locate() {
	root := pluginInstallDir()
	s.loc = pluginwire.Location{Root: root, Settings: filepath.Join(root, ".claude", "settings.json")}
}

// pluginInstallDir is the fixed directory the install half runs claude in
// (U6a), and it is a function rather than a constant so a test can read it.
func pluginInstallDir() string { return os.TempDir() }

// Check is status plus the one fact a later stage reads off this stage: a
// plugin stage whose install half has something to do is this run taking this
// machine's plugin — and the checkout — to this release, and the checkout's own
// cloud half, the job images built FROM the base the images stage pins in this
// same run, is what moves with it (init_checkout_upgrade.go's Check reads
// initEngine.checkoutBehind). Set and never cleared, which is why the flag is
// not simply assigned: the run's own write moves what it was read from — the
// install that clears the version behind it — and the execution pass checks
// this stage again after the apply that changed it.
func (s *pluginStage) Check(ctx context.Context) (initflow.Status, error) {
	st, err := s.status(ctx)
	if err == nil && s.installStep {
		s.e.checkoutBehind = true
	}
	return st, err
}

// status is the stage's whole answer, and it is one function because Check and
// Plan have to read the same thing: the install's own state, the plugin in the
// user's own Claude Code, which is the whole of what this stage does (U6a).
// Nothing here reads a checkout: the plugin is the same one for every
// repository, and a pin an older fugaro left is a notice the run prints after
// the loop (repo_notices.go) rather than a write this stage makes.
func (s *pluginStage) status(ctx context.Context) (initflow.Status, error) {
	s.locate()
	inst := s.install(ctx, initflow.Env{}, true)
	if inst.state == stepFailed {
		return initflow.Status{}, errors.New(inst.reason)
	}
	switch {
	case inst.state == stepStale:
		// The plugin in Claude Code is a release behind what this fugaro
		// installs: something to do (#1836c).
		s.installStep = true
		return initflow.Status{State: initflow.Todo, Detail: behindLine(inst)}, nil
	case !s.installs():
		// A development build has no release plugin to install: the install
		// half's own skip, and not something to do.
		return initflow.Status{State: initflow.Skipped, Detail: inst.reason}, nil
	case inst.state != stepCurrent:
		// Nothing installed on this machine, or a record this fugaro cannot
		// compare — neither reads as current, which is what --check's own
		// answer words as a skip because it is about the plugin the machine
		// already has, where this stage has the install itself to make.
		s.installStep = true
		return initflow.Status{State: initflow.Todo, Detail: "installs the plugin in Claude Code: " + inst.reason}, nil
	}
	return initflow.Status{State: initflow.Done, Detail: "No changes: " + inst.reason}, nil
}

// behindLine is the stage's line when Claude Code's own record of the installed
// plugin is another version than this release's: "No changes" alone would read
// as current while the staleness line every command prints — and doctor's
// plugin-install check — say the opposite about the same run (#1836). The
// install half's own words carry the version that is behind, and the version
// this release installs is its own tag.
func behindLine(inst stepResult) string {
	return "Claude Code's own plugin is behind: " + inst.reason + ", and this stage installs " + pluginTag()
}

// installs: this build has a release to install, so the install half is a step
// of this stage at all. A development build's own skip is not something to do,
// whatever the machine's record of the plugin says.
func (s *pluginStage) installs() bool {
	_, err := pluginwire.Tag(Version)
	return err == nil
}

// pluginTag is this release's plugin tag, or "" for a build that has none
// (behindLine is only reached on a release build: the install half skips such a
// build before it can read anything).
func pluginTag() string {
	tag, err := pluginwire.Tag(Version)
	if err != nil {
		return Version
	}
	return tag
}

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
// U5). It is the plugin stage's principal write and `fugaro update-skills`'s
// machine-side half, and `fugaro update --check` runs it in check mode, which
// only reads Claude Code's record of installed plugins.
func pluginStep(ctx context.Context, u *stepCtx) stepResult {
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
	fmt.Fprintf(u.detailOut(), "  using claude at %s\n", pluginwire.Printable(bin))
	run := &claudeplugin.Runner{Bin: bin, Dir: u.loc.Root, Out: u.detailOut()}
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
				fmt.Fprintf(u.detailOut(), "  putting back what the clear removed failed: %v\n", rerr)
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
			fmt.Fprintf(u.detailOut(), "  continuing without clearing: %v\n", err)
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
			fmt.Fprintf(u.detailOut(), "  continuing without that call: %v\n", err)
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
		return stepResult{state: stepDone, reason: fmt.Sprintf("installed %s, newer than this fugaro %s (Claude Code's marketplace is pinned to a newer release, and the pin is never moved down): brew upgrade dimipaun/tap/fugaro, then fugaro update-skills; restart running Claude Code sessions to apply it",
			pluginwire.Printable(got), ver)}
	}
	return stepResult{state: stepDone, reason: "installed " + ver + "; restart running Claude Code sessions to apply it"}
}

// pluginCheck is the plugin step under --check: Claude Code's record of
// installed plugins compared with this fugaro, no claude run. It answers about
// the install the write path maintains, the machine's own at user scope
// (claudeplugin.UserVersion), which Status reads off the record
// (pluginwire.Report.UserInstall): what applies to a checkout is another
// question — doctor's, and the staleness warning's — and a checkout-scoped
// install with nothing at user scope would answer it with a version while the
// machine's own Claude Code has no plugin, which is the install this step's
// write still makes (D949, U2).
func pluginCheck(u *stepCtx, ver string) stepResult {
	r := pluginwire.Status(u.loc.Settings, Version, installedPlugins())
	switch r.UserInstall {
	case pluginwire.NotInstalled:
		return stepResult{state: stepSkipped, reason: "not installed on this machine (fugaro update-skills installs it through the claude CLI)"}
	case pluginwire.OK, pluginwire.InstalledDiffers:
		// Only these two carry a version read out of the record; anything else
		// about it — a file that cannot be read, a build with no release tag —
		// is what the line below says it cannot tell.
	default:
		return stepResult{state: stepSkipped, reason: "cannot tell from Claude Code's record of installed plugins (fugaro update-skills asks claude itself)"}
	}
	c, ok := claudeplugin.Compare(r.UserVersion, ver)
	switch {
	case !ok:
		return stepResult{state: stepSkipped, reason: "cannot tell from Claude Code's record of installed plugins (fugaro update-skills asks claude itself)"}
	case c < 0:
		return stepResult{state: stepStale, reason: fmt.Sprintf("installed %s, this fugaro is %s: fugaro update-skills installs it through the claude CLI, and running sessions apply it after a restart", r.UserVersion, ver)}
	case c > 0:
		return stepResult{state: stepCurrent, reason: fmt.Sprintf("installed %s, newer than this fugaro %s (brew upgrade dimipaun/tap/fugaro)", r.UserVersion, ver)}
	}
	return stepResult{state: stepCurrent, reason: "installed " + r.UserVersion}
}

// install is the stage's principal write, and it is the plugin step every door
// runs (pluginStep), in check mode when the caller is only reading: the plugin
// installed or updated in the user's own Claude Code at user scope. The stage
// added a caller, not a copy, and it runs from any directory, checkout or not
// (D955, U8): what it writes is the machine's, and the checkout it is given is
// only the directory claude is run in (claudeplugin.Runner's Dir, cwd when
// there is none).
func (s *pluginStage) install(ctx context.Context, env initflow.Env, check bool) stepResult {
	u := s.e.stepCtx(env, s.loc)
	u.o.check = check
	r := pluginStep(ctx, u)
	r.name = "plugin"
	return r
}

// Plan words the stage's own answer (status), the same one Check decides on and
// in the same words: the plan-only view is what a run would do, and a plan that
// read the install half alone said "No changes" in the states where this stage
// installs the plugin into Claude Code (init_plugin_stage.go's status, and
// TestPluginStagePlanOnlySaysTheInstallRuns).
func (s *pluginStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	st, err := s.status(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State != initflow.Todo && st.State != initflow.NeedsYou}, nil
}

func (s *pluginStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	s.locate()
	// The install half, in upgrade's own words: the step prints its line under
	// this stage (`plugin: done: installed vX, ...`), which is the line the
	// staleness warning waits for, and a failed step fails the stage here.
	inst := s.install(ctx, env, false)
	printStep(s.e.r.w, inst)
	if inst.state == stepFailed {
		return initflow.Outcome{}, errors.New(inst.reason)
	}
	if inst.state == stepDone {
		return initflow.Outcome{Changed: true, Detail: inst.reason}, nil
	}
	return initflow.Outcome{Detail: "No changes"}, nil
}

func (s *pluginStage) leftFor(st *initflow.Status) initflow.Left {
	if st.Left != nil {
		return *st.Left
	}
	return s.Left()
}

// Verify has nothing to read back: the stage writes nothing in the checkout,
// and the install step re-reads Claude Code's own record after its calls and
// fails there when the machine did not come out of them with this release's
// plugin.
func (s *pluginStage) Verify(context.Context) error { return nil }
