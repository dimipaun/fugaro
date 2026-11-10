package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/dimipaun/fugaro/internal/config"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The plugin-wiring stage (stage 8, design §4.3): merges the Fugaro
// marketplace and enabledPlugins entries into the checkout's
// .claude/settings.json, pinned to the binary's release tag, after showing the
// diff and a confirmation of its own (--yes confirms it). Only those two
// entries change; a file that is not valid JSON, a marketplace named fugaro
// that is not Fugaro's, and a fork are left alone and handed to the user. A
// dev build has no tag to pin to and the stage is skipped saying so; outside a
// checkout it prints the settings to add. It never commits.
//
// The stage's second half is fugaro upgrade's plugin step, and it is this
// stage's own since D941 (U1 of docs/plugin-user-scope/implementation-plan.md):
// the plugin installed or updated in the user's own Claude Code through the
// claude CLI. It is here and not in the checkout stage because this stage runs
// before the repository stage (initflow's order, F3): a session whose
// repository stage returns needs-you — a repository not enrolled, a GitHub App
// ID that is missing, an invalid fugaro.yaml — never reaches the checkout, and
// the install is the step that clears the staleness line every command prints
// ("run fugaro update"), so a run that leaves it behind leaves that reader in a
// loop with no way out (reconciliation.md F13, #1836). Both halves are writes
// on this machine, which is why the stage is LocalUnderAgent.

// pluginFirstRun ends the wiring. Observed live 2026-10-07 (cause not
// verified): after init wired the settings, opening Claude Code in the folder
// did not make the skills available and the plugin had to be installed by
// hand. fugaro update's plugin step (the same one fugaro upgrade runs, Task 6,
// docs/design/upgrade.md) does that same work, including its own claude-less
// fallback (noClaudeHint), so this text points there instead of repeating the
// manual commands (code review of upgrade task 9, 2026-10-09).
const pluginFirstRun = "Run fugaro update to install the plugin for you.\n" +
	"Once this is committed, Claude Code installs the plugin when you open this folder in a new session and trust it.\n" +
	"If the /fugaro: skills are not listed (for example the folder was already trusted), run fugaro update again; without claude on your PATH it prints the slash commands to type in Claude Code instead. fugaro doctor then shows the plugin as installed.\n" +
	"A repository's settings can install a plugin from the marketplace they name, so only trust folders you trust.\n"

// pluginRefresh follows a changed pin: Claude Code reports the new version but
// shows the plugin as "not cached" and its skills disappear until the
// marketplace is updated (verified live, V2); no restart is needed. See
// pluginFirstRun: fugaro update's plugin step runs the manual command too.
const pluginRefresh = "Run fugaro update to refresh it for you, or in Claude Code run /plugin marketplace update fugaro (until then the plugin shows \"not cached\" and its skills are gone).\n"

type pluginStage struct {
	e       *initEngine
	printed bool // the outside-a-checkout snippet, said once
	// ask: the checkout's repository is not the project's yet and has to be
	// typed in before anything is written (set by plan); repo and root name it.
	ask    bool
	origin originInfo
	root   string
	// loc and found are the checkout the plan read, and whether there is one
	// at all: the settings file the wiring half writes, and the directory the
	// install half runs claude in. Outside a checkout the install has nowhere
	// to run (the stage prints the settings to add and skips), which is the
	// only case found is false in.
	loc   pluginwire.Location
	found bool
}

func newPluginStage(e *initEngine) *pluginStage { return &pluginStage{e: e} }

func (*pluginStage) Name() string { return initflow.Plugin }

// LocalUnderAgent: in a coding agent's session the loop applies this stage
// instead of leaving it for the user (initflow.LocalUnderAgent), because every
// write it makes is on this machine — the checkout's .claude/settings.json and
// the plugin in the user's own Claude Code through the claude CLI — and those
// are the writes the user's own session makes with fugaro upgrade, on files it
// reads anyway. Nothing here reaches the cloud, which is what the marker's
// refusal is about: the pin and the plugin are the step it may not take with
// it, and it is the step that clears the line every command prints.
func (*pluginStage) LocalUnderAgent() {}

func (s *pluginStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftCommand, Text: selfCommand() + " update-skills"}
}

// forkLeft is the one line that moves a fork's marketplace pin.
func forkLeft() initflow.Left {
	return initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftCommand, Text: selfCommand() + " update-skills --allow-fork"}
}

// plan is the read-only merge for the working directory's checkout: the
// change, or the status that says why there is none.
func (s *pluginStage) plan(ctx context.Context) (*pluginwire.Change, *initflow.Status, error) {
	ch, st, err := s.plan1(ctx)
	if st != nil && st.State == initflow.NeedsYou && !strings.Contains(st.Detail, "repository stage was not reached") {
		st.Detail += "; the repository stage was not reached"
	}
	return ch, st, err
}

func (s *pluginStage) plan1(ctx context.Context) (*pluginwire.Change, *initflow.Status, error) {
	s.ask = false
	status := func(st initflow.State, detail string, left *initflow.Left) (*pluginwire.Change, *initflow.Status, error) {
		return nil, &initflow.Status{State: st, Detail: detail, Left: left}, nil
	}
	loc, ok := pluginwire.Locate(".")
	s.loc, s.found = loc, ok
	if !ok {
		if !s.printed {
			s.printed = true
			fmt.Fprintf(s.e.r.w, "not in a checkout, so nothing is wired. To use the Fugaro plugin, merge this into the repository's .claude/settings.json:\n\n%s\n", pluginwire.Snippet(Version))
		}
		return status(initflow.Skipped, "not in a checkout: the settings to add are printed above", nil)
	}
	if _, err := pluginwire.Tag(Version); err != nil {
		return status(initflow.Skipped, "this fugaro build has no release tag to pin the plugin to, so nothing is wired (a release build adds it)", nil)
	}
	// Only the checkout the project's own repository stage would target.
	e := s.e
	repo, ok := readOrigin(ctx, loc.Root)
	if !ok {
		return status(initflow.Skipped, "this checkout has no origin repository, so it is not one to wire", nil)
	}
	switch data, rerr := readFugaroYAML(filepath.Join(loc.Root, "fugaro.yaml")); {
	case rerr == nil:
		if p, perr := config.ProjectOf(data); perr != nil || p != e.lc.Name {
			return status(initflow.Skipped, "this checkout's fugaro.yaml names another project (or none), not "+pluginwire.Printable(e.lc.Name)+": the plugin is not wired here", nil)
		}
	case !errors.Is(rerr, os.ErrNotExist):
		// Unknown is not a pass: a file that cannot be read (a symlink, a
		// device, an oversize file) could name any project.
		lf := initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftConsole, Text: "make fugaro.yaml a regular file of at most 1 MiB (or remove it), then rerun " + e.r.o.doorProgram()}
		return status(initflow.NeedsYou, "this checkout's fugaro.yaml cannot be read, so which project it names is unknown and the plugin is not wired: "+pluginwire.Printable(oneLineCLI(rerr.Error())), &lf)
	}
	switch a, err := e.authState(repo); {
	case err != nil:
		return nil, nil, err
	case a == authNeeded && !e.r.o.enrolling():
		// A door that does not enrol a repository: one line naming the door
		// that does, and never the typed question or --onboard-repo, which
		// would be this run's to give (D5, W5.1).
		lf := enrolLeft(e.r.o.doorName())
		return status(initflow.NeedsYou, unknownDetailNotEnrolled(e.lc.Name, repo, e.r.o.doorName()), &lf)
	case a == authNeeded:
		lf := onboardLeft(repo, e.r.o.doorName())
		return status(initflow.NeedsYou, unknownDetail(e.lc.Name, repo, convergeOptIn(e.lc, repo)), &lf)
	case a == authAsk:
		s.origin, s.root, s.ask = repo, loc.Root, true
	}
	ch, err := pluginwire.Plan(loc.Settings, Version, s.e.r.o.allowFork)
	var fe *pluginwire.ForeignError
	var fk *pluginwire.ForkError
	switch {
	case errors.As(err, &fk):
		lf := forkLeft()
		return status(initflow.NeedsYou, fmt.Sprintf("the %q marketplace in %s names %s, not %s: not moved without --allow-fork", pluginwire.Marketplace, loc.Settings, pluginwire.Printable(fk.Repo), pluginwire.Repo), &lf)
	case errors.As(err, &fe), pluginwire.IsInvalid(err):
		lf := initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftConsole, Text: "fix " + loc.Settings + " (or merge the settings by hand: " + selfCommand() + " update-skills prints them), then rerun " + e.r.o.doorProgram()}
		return status(initflow.NeedsYou, pluginwire.Printable(oneLineCLI(err.Error())), &lf)
	case err != nil:
		return nil, &initflow.Status{State: initflow.NeedsYou, Detail: pluginwire.Printable(oneLineCLI(err.Error())), Left: nil}, nil
	}
	return ch, nil, nil
}

// Check is status plus the one fact a later stage reads off this stage: a
// plugin stage with something to do is this run taking the checkout — and this
// machine's plugin — to this release, and the checkout's own cloud half, the
// job images built FROM the base the images stage pins in this same run, is
// what moves with it (init_checkout_upgrade.go's Check reads
// initEngine.checkoutBehind). Set and never cleared, which is why the flag is
// not simply assigned: the run's own writes move what it was read from — this
// stage's pin write and the install that clears the version behind it — and
// the execution pass checks this stage again after the apply that changed both.
func (s *pluginStage) Check(ctx context.Context) (initflow.Status, error) {
	_, st, err := s.status(ctx)
	if err == nil && st.State == initflow.Todo {
		s.e.checkoutBehind = true
	}
	return st, err
}

// status is the stage's whole answer, and it is one function because Check and
// Plan have to read the same thing: the wiring half's change and its skip line,
// and the install half's state, which has been a step of this stage since U1
// (the plugin in the user's own Claude Code). The change comes back with it for
// the plan, which is what prints the diff of a wiring half about to change.
func (s *pluginStage) status(ctx context.Context) (*pluginwire.Change, initflow.Status, error) {
	ch, st, err := s.plan(ctx)
	switch {
	case err != nil:
		return nil, initflow.Status{}, err
	case st != nil && st.State == initflow.NeedsYou:
		// The wiring half cannot be read at all: the stage stops there, as it
		// did before the install half was its own, and nothing is installed.
		return ch, *st, nil
	}
	inst := s.install(ctx, initflow.Env{}, true)
	switch {
	case inst.state == stepFailed:
		return nil, initflow.Status{}, errors.New(inst.reason)
	case inst.state == stepStale:
		// The plugin in Claude Code is a release behind what this fugaro
		// installs: something to do, whatever the pin says — a pin already at
		// this release and an install that never happened is exactly the state
		// both halves have to read the same way (#1836c).
		return ch, initflow.Status{State: initflow.Todo, Detail: behindLine(ch, inst)}, nil
	case ch != nil && ch.Changed:
		// The wiring half has work of its own here, and its line is the more
		// telling of the two: the install half's own line is printed by the
		// apply either way.
		d := "adds the Fugaro plugin to " + ch.Path + ", pinned to " + ch.Tag
		if s.ask {
			d += "; asks you to type the repository's name first (it is not the project's yet)"
		}
		return ch, initflow.Status{State: initflow.Todo, Detail: d}, nil
	case s.found && s.installs() && inst.state != stepCurrent:
		// The install half is a step of this stage and it is not up to date:
		// nothing installed on this machine, or a record this fugaro cannot
		// compare — neither reads as current, which is what --check's own
		// answer words as a skip because it is about the plugin the machine
		// already has, where this stage has the install itself to make. The
		// plugin in Claude Code is the same one for every checkout, so a
		// checkout this run may not wire is not a machine that needs nothing
		// (U1, F13).
		return ch, initflow.Status{State: initflow.Todo, Detail: "installs the plugin in Claude Code: " + inst.reason}, nil
	case st != nil:
		// This checkout is not one to wire (or this build has no release tag to
		// pin to), and the install half is current or is not a step here: the
		// wiring half's own skip line is the stage's answer.
		return ch, *st, nil
	}
	d := "No changes: " + ch.Path + " pins the plugin to " + ch.Tag
	if ch.Disabled {
		d += " (the repository disables it)"
	}
	return ch, initflow.Status{State: initflow.Done, Detail: d + "; " + inst.reason}, nil
}

// behindLine is the stage's line when the two halves disagree: the pin is not
// the whole truth, because Claude Code's own record of the installed plugin is
// another version, and "No changes: ... pins the plugin to vX" alone would read
// as current while the staleness line every command prints — and doctor's
// plugin-install check — say the opposite about the same run (#1836). The
// install half's own words carry the version that is behind, and the version
// this release installs is the pin's tag.
func behindLine(ch *pluginwire.Change, inst stepResult) string {
	pin := ""
	if ch != nil && !ch.Changed {
		pin = "No changes: " + ch.Path + " pins the plugin to " + ch.Tag + "; "
	}
	return pin + "Claude Code's own plugin is behind: " + inst.reason + ", and this stage installs " + pluginTag()
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

// install is the stage's second half: fugaro upgrade's own plugin step
// (upgrade_plugin.go), in check mode when the caller is only reading. It is
// the same function fugaro upgrade runs — the stage added a caller, not a copy
// — and it is skipped where there is no checkout to run claude in, the one
// place the wiring half's own skip is not enough (it is a skip of the wiring,
// and outside a checkout there is no settings file to write either way).
func (s *pluginStage) install(ctx context.Context, env initflow.Env, check bool) stepResult {
	if !s.found {
		return stepResult{name: "plugin", state: stepSkipped,
			reason: "not in a checkout: the plugin in Claude Code is installed where the checkout is"}
	}
	u := s.e.upgradeCtx(env, s.loc)
	u.o.check = check
	r := pluginStep(ctx, u)
	r.name = "plugin"
	return r
}

// Plan words the stage's own answer (status), the same one Check decides on and
// in the same words: the plan-only view is what a run would do, and a plan that
// read the wiring half alone said "No changes" in the states where this stage
// installs the plugin into Claude Code — the pin already this release's and the
// install behind it, or a wiring half skipped where the install half is not
// (init_plugin_stage.go's status, and TestPluginStagePlanOnlySaysTheInstallRuns).
func (s *pluginStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	ch, st, err := s.status(ctx)
	if err != nil {
		return initflow.Plan{}, err
	}
	if ch != nil && ch.Changed {
		// The diff is the plan's own half of this stage's output: the apply
		// prints it too, and a plan-only run is where it is read before
		// anything is written.
		fmt.Fprintf(s.e.r.w, "%s\n%s%s", ch.Path, ch.Diff(), ch.NoticeText())
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State != initflow.Todo && st.State != initflow.NeedsYou}, nil
}

func (s *pluginStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	s.ask = false
	ch, st, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if st != nil && st.State == initflow.NeedsYou {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.leftFor(st)}
	}
	changed, detail := false, ""
	if ch != nil && ch.Changed {
		r := s.e.r
		if s.ask { // not the project's repository yet: its own typed confirmation, which --yes never gives
			if !env.Interactive {
				return initflow.Outcome{}, &initflow.NeedsYouError{Left: onboardLeft(s.origin, s.e.r.o.doorName())}
			}
			ok, err := s.e.confirmRepo(s.root, s.origin)
			if err != nil {
				return initflow.Outcome{}, err
			}
			if !ok {
				return initflow.Outcome{}, &initflow.NeedsYouError{Left: onboardLeft(s.origin, s.e.r.o.doorName())}
			}
		}
		fmt.Fprintf(r.w, "%s\n%s%s", ch.Path, ch.Diff(), ch.NoticeText())
		if ch.Note != "" {
			fmt.Fprintf(r.w, "note: %s\n", ch.Note)
		}
		switch {
		case r.gateStandsIn() && agentMarker(os.Getenv, r.stdinTerminal()) == "":
			// A managed project asks nothing here (the ruling of 2026-11-01): the
			// diff above is the whole of what the owner reads, and the write is
			// made. The marker is asked for beside the gate because the two
			// answers are about different things — the gate is a fact about the
			// project, and this branch is a fact about the session. A coding
			// agent's session confirms nothing, which is the arm below; the loop
			// reaches this Apply in one because the stage is LocalUnderAgent.
			fmt.Fprintf(r.w, "  %s\n", initflow.GateOrdinary())
		case env.Yes:
			fmt.Fprintln(r.w, "  confirmed by --yes")
		case agentMarker(os.Getenv, r.stdinTerminal()) != "":
			// A coding agent's session is not the no-terminal case the default
			// arm below is about: the write is on this machine's own file, which
			// is what LocalUnderAgent says the stage does there, and nobody is
			// here to be asked — the arm the checkout stage's own gate takes,
			// and the pin it wrote before this stage ran the step (U1). The
			// line says which session wrote it, so the diff above is not read as
			// one a person confirmed.
			fmt.Fprintln(r.w, "  written by this coding agent's session, where there is no prompt to answer")
		default:
			// A person at the terminal is asked nothing (T7): the y/N they used
			// to type is gone, and --ask is the flag that puts the question back.
			// A run nobody is at keeps leaving the step for the user, which is the
			// arm this default always was.
			if !env.Interactive {
				return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
			}
			ok, err := r.confirmNow("write this to " + ch.Path + "?")
			if err != nil {
				return initflow.Outcome{}, err
			}
			if !ok {
				return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
			}
		}
		if err := ch.Apply(); err != nil {
			return initflow.Outcome{}, err
		}
		fmt.Fprintf(r.w, "Updated %s to pin the Fugaro plugin at %s. Review it with git diff and commit it like any change.\n%s%s", ch.Path, ch.Tag, pluginRefresh, pluginFirstRun)
		changed, detail = true, "wired "+ch.Path+" to "+ch.Tag
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
		changed = true
	}
	if inst.reason != "" && inst.state != stepSkipped {
		if detail == "" {
			detail = inst.reason
		} else {
			detail += "; " + inst.reason
		}
	}
	if !changed {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	return initflow.Outcome{Changed: true, Detail: detail}, nil
}

func (s *pluginStage) leftFor(st *initflow.Status) initflow.Left {
	if st.Left != nil {
		return *st.Left
	}
	return s.Left()
}

// Verify re-reads the wiring: the merge is in, and a second merge changes
// nothing. The install half needs no read-back of its own — the step re-reads
// Claude Code's own record after its calls and fails there — and a wiring half
// that is not this checkout's (st != nil, a skip) has nothing to verify.
func (s *pluginStage) Verify(ctx context.Context) error {
	ch, st, err := s.plan(ctx)
	switch {
	case err != nil:
		return err
	case st != nil && st.State == initflow.NeedsYou:
		return fmt.Errorf("the plugin wiring cannot be read back: %s", st.Detail)
	case st != nil:
		return nil
	case ch.Changed:
		return fmt.Errorf("%s still lacks the Fugaro plugin after the write", ch.Path)
	}
	return nil
}
