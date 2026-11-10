package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The plugin stage (stage 8): the plugin installed, or updated, in the user's
// own Claude Code through the claude CLI, at user scope. It writes nothing in
// the checkout — the merge of the pin into .claude/settings.json was deleted
// outright (D949, answered 2026-11-04; U2 of
// docs/plugin-user-scope/implementation-plan.md), so this stage's only write is
// on this machine, in the user's own plugin installation.
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
// prints.
//
// The gates below (the checkout's origin, its fugaro.yaml, and the machine
// config's answer about the repository) are the checkout write's: they stay for
// the removal of a leftover pin's two entries, which U5 adds, and the install
// half drops them with it (F24, D955, U8). Until then a checkout that is not
// the project's is still left for the user, and nothing is installed from it.

type pluginStage struct {
	e *initEngine
	// loc and found are the checkout the plan read, and whether there is one
	// at all: the directory the install half runs claude in. Without one there
	// is no repository for the gates below to read, and the stage skips.
	loc   pluginwire.Location
	found bool
}

func newPluginStage(e *initEngine) *pluginStage { return &pluginStage{e: e} }

func (*pluginStage) Name() string { return initflow.Plugin }

// LocalUnderAgent: in a coding agent's session the loop applies this stage
// instead of leaving it for the user (initflow.LocalUnderAgent), because every
// write it makes is on this machine — the plugin in the user's own Claude Code
// through the claude CLI — and that is the write the user's own session makes
// with fugaro upgrade, on files it reads anyway.
func (*pluginStage) LocalUnderAgent() {}

func (s *pluginStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftCommand, Text: selfCommand() + " update-skills"}
}

// plan is the read-only half: the gates that say whether this checkout is one
// to install the machine's plugin from, or the status that says why not.
func (s *pluginStage) plan(ctx context.Context) (*initflow.Status, error) {
	s.loc, s.found = pluginwire.Locate(".")
	status := func(st initflow.State, detail string, left *initflow.Left) (*initflow.Status, error) {
		return &initflow.Status{State: st, Detail: detail, Left: left}, nil
	}
	loc := s.loc
	if !s.found {
		return status(initflow.Skipped, "not in a checkout: this stage installs the plugin from the checkout's repository", nil)
	}
	// Only the checkout the project's own repository stage would target.
	e := s.e
	repo, ok := readOrigin(ctx, loc.Root)
	if !ok {
		return status(initflow.Skipped, "this checkout has no origin repository, so it is not one to install the plugin from", nil)
	}
	switch data, rerr := readFugaroYAML(filepath.Join(loc.Root, "fugaro.yaml")); {
	case rerr == nil:
		if p, perr := config.ProjectOf(data); perr != nil || p != e.lc.Name {
			return status(initflow.Skipped, "this checkout's fugaro.yaml names another project (or none), not "+pluginwire.Printable(e.lc.Name)+": the plugin is not installed from here", nil)
		}
	case !errors.Is(rerr, os.ErrNotExist):
		// Unknown is not a pass: a file that cannot be read (a symlink, a
		// device, an oversize file) could name any project.
		lf := initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftConsole, Text: "make fugaro.yaml a regular file of at most 1 MiB (or remove it), then rerun " + e.r.o.doorProgram()}
		return status(initflow.NeedsYou, "this checkout's fugaro.yaml cannot be read, so which project it names is unknown and the plugin is not installed: "+pluginwire.Printable(oneLineCLI(rerr.Error())), &lf)
	}
	switch a, err := e.authState(repo); {
	case err != nil:
		return nil, err
	case a == authNeeded && !e.r.o.enrolling():
		// A door that does not enrol a repository: one line naming the door
		// that does, and never the typed question or --onboard-repo, which
		// would be this run's to give (D5, W5.1).
		lf := enrolLeft(e.r.o.doorName())
		return status(initflow.NeedsYou, unknownDetailNotEnrolled(e.lc.Name, repo, e.r.o.doorName()), &lf)
	case a == authNeeded:
		lf := onboardLeft(repo, e.r.o.doorName())
		return status(initflow.NeedsYou, unknownDetail(e.lc.Name, repo, convergeOptIn(e.lc, repo)), &lf)
	}
	return nil, nil
}

// Check is status plus the one fact a later stage reads off this stage: a
// plugin stage with something to do is this run taking this machine's plugin —
// and the checkout — to this release, and the checkout's own cloud half, the
// job images built FROM the base the images stage pins in this same run, is
// what moves with it (init_checkout_upgrade.go's Check reads
// initEngine.checkoutBehind). Set and never cleared, which is why the flag is
// not simply assigned: the run's own write moves what it was read from — the
// install that clears the version behind it — and the execution pass checks
// this stage again after the apply that changed it.
func (s *pluginStage) Check(ctx context.Context) (initflow.Status, error) {
	st, err := s.status(ctx)
	if err == nil && st.State == initflow.Todo {
		s.e.checkoutBehind = true
	}
	return st, err
}

// status is the stage's whole answer, and it is one function because Check and
// Plan have to read the same thing: the gates, then the install's own state
// (the plugin in the user's own Claude Code, which has been a step of this
// stage since U1).
func (s *pluginStage) status(ctx context.Context) (initflow.Status, error) {
	st, err := s.plan(ctx)
	if err != nil {
		return initflow.Status{}, err
	}
	if st != nil {
		return *st, nil
	}
	inst := s.install(ctx, initflow.Env{}, true)
	switch {
	case inst.state == stepFailed:
		return initflow.Status{}, errors.New(inst.reason)
	case inst.state == stepStale:
		// The plugin in Claude Code is a release behind what this fugaro
		// installs: something to do (#1836c).
		return initflow.Status{State: initflow.Todo, Detail: behindLine(inst)}, nil
	case !s.installs():
		// A development build has no release plugin to install: the install
		// half's own skip, and not something to do.
		return initflow.Status{State: initflow.Skipped, Detail: inst.reason}, nil
	case inst.state != stepCurrent:
		// Nothing installed on this machine, or a record this fugaro cannot
		// compare — neither reads as current, which is what --check's own
		// answer words as a skip because it is about the plugin the machine
		// already has, where this stage has the install itself to make. The
		// plugin in Claude Code is the same one for every checkout, so a
		// checkout this run may not wire is not a machine that needs nothing
		// (U1, F13).
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

// install is the stage's one write, and it is fugaro upgrade's own plugin step
// (upgrade_plugin.go), in check mode when the caller is only reading: the
// plugin installed or updated in the user's own Claude Code at user scope. It
// is the same function fugaro upgrade runs — the stage added a caller, not a
// copy — and it is skipped where there is no checkout, which is the only place
// the gates below it cannot read the repository.
func (s *pluginStage) install(ctx context.Context, env initflow.Env, check bool) stepResult {
	if !s.found {
		return stepResult{name: "plugin", state: stepSkipped,
			reason: "not in a checkout: this stage installs the plugin from the checkout's repository"}
	}
	u := s.e.upgradeCtx(env, s.loc)
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
	st, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if st != nil && st.State == initflow.NeedsYou {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.leftFor(st)}
	}
	// The install half, in upgrade's own words: the step prints its line under
	// this stage (`plugin: done: installed vX, ...`), which is the line the
	// staleness warning waits for, and a failed step fails the stage here.
	inst := s.install(ctx, env, false)
	if s.found {
		printStep(s.e.r.w, inst)
	}
	switch inst.state {
	case stepFailed:
		return initflow.Outcome{}, errors.New(inst.reason)
	case stepDone:
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
