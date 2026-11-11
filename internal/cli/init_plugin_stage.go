package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The plugin stage (stage 8): the plugin installed, or updated, in the user's
// own Claude Code through the claude CLI, at user scope, and the pin an older
// fugaro left in the checkout taken out (U5). The merge that wrote the pin was
// deleted outright (D949, answered 2026-11-04; U2 of
// docs/plugin-user-scope/implementation-plan.md), so nothing writes one any
// more and the removal is the only thing in the tree that touches one at all
// (F26): the checkout is written once, to take the two entries the merge left
// out, and every other write of this stage is on this machine, in the user's
// own plugin installation.
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
// The two halves have different gates (D955, U8). The removal's gates are the
// checkout's own, whole: its origin, its fugaro.yaml and the machine config's
// answer about the repository that origin names — everything that authorises
// writing .claude/settings.json in somebody's checkout — and a checkout this
// run may not write into still leaves the pin exactly as it is. The install half
// reads none of them: the plugin it installs is the same one for every checkout,
// it concerns no repository, and the gate that consults the machine's config
// would send every teammate to fugaro setup for a write that is not the
// repository's (F24). So the install happens in any directory, checkout or not,
// and the removal's gate is read only where the checkout carries a pin at all
// (s.pin): a gate authorises a write, and where there is nothing to write it
// authorises nothing and is asked nothing — which is what keeps a new machine's
// empty repository list from being an answer about the machine's own plugin.

type pluginStage struct {
	e *initEngine
	// loc and found are the checkout the plan read, and whether there is one
	// at all: the directory the install half runs claude in, and the checkout
	// the pin below could be in. Without one there is nothing for the removal's
	// gate to read, and the install half still runs.
	loc   pluginwire.Location
	found bool
	// installStep is whether the stage's own something-to-do is the install
	// half's, and not the pin's removal alone (Check). The two are Todo alike
	// and only one of them is this run taking the machine's plugin — and the
	// checkout — to this release.
	installStep bool
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

// locate is the checkout the command was typed in, and whether there is one.
func (s *pluginStage) locate() {
	s.loc, s.found = pluginwire.Locate(".")
	if !s.found {
		// Not a checkout: the removal has nothing to read (the pin is a
		// checkout's file) and the install half still runs. It runs from the
		// directory the command was typed in, which is the one claude is given
		// as its working directory — an absolute path, because the runner
		// refuses anything else, and "" is not one.
		if wd, err := os.Getwd(); err == nil {
			s.loc.Root = wd
		}
	}
}

// pin is the checkout's pin an older fugaro left and the gate that authorises
// taking it out, read together and read only where the checkout carries a pin at
// all (D955, U8): the gate is what authorises a write into somebody's checkout,
// so where there is nothing to write it is not read and its answer about the
// repository is no answer about the machine's own plugin. That is F24's gap: a
// new machine's config lists no repositories, and the machine's install concerns
// none of them.
//
// Where it is read, its answer is about the removal alone, which is all it
// authorises: the three states of the checkout's own read (no origin, another
// project's fugaro.yaml or one that cannot be read) are skips that leave the
// pin where it is with their own line, and a repository the machine's config
// does not list is needs-you with the door that enrols it (F24) — the checkout
// write is that repository's, and the run does not make it on its own.
//
// Neither answer is about the machine's own plugin: the install half runs
// whatever the gate says (D955, U8), and a gate that refuses leaves its step
// for the user beside an install that happened — what status puts on the
// stage's line and Apply turns into the run's own left.
//
// It returns the removal to make, the sentence for a pin that is not this
// package's to take out, and the gate's own status where the checkout is not
// this run's to write.
func (s *pluginStage) pin(ctx context.Context) (*pluginwire.Removal, string, *initflow.Status, error) {
	if !s.found {
		return nil, "", nil, nil
	}
	rem, note := s.removal()
	if rem == nil && note == "" {
		// Nothing to take out: no checkout write, and no gate to read for one.
		return nil, "", nil, nil
	}
	gate, err := s.pinGate(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	if gate != nil {
		return nil, "", gate, nil
	}
	return rem, note, nil, nil
}

// pinStays is the clause every sentence from the pin's gate ends with: what
// the gate's own words say about the repository, this one says about the pin
// that stays where it is because of it.
const pinStays = ": the pin an older fugaro left is not taken out of here"

// pinGate is the removal's own gate: whether this checkout is one the pin an
// older fugaro left may be taken out of, or the status that says why not. It
// reads the checkout's origin and its fugaro.yaml the way every other checkout
// write does (namesAnotherProject), and asks authState about the repository that
// origin names.
//
// Its needs-you is not the stage's: the machine's plugin is installed whatever
// this says (D955), so the answer goes on the stage's line and its own left —
// the door that enrols the repository — beside an install that happened
// (status, Apply).
func (s *pluginStage) pinGate(ctx context.Context) (*initflow.Status, error) {
	status := func(st initflow.State, detail string, left *initflow.Left) (*initflow.Status, error) {
		return &initflow.Status{State: st, Detail: detail, Left: left}, nil
	}
	skip := func(detail string) (*initflow.Status, error) { return status(initflow.Skipped, detail, nil) }
	e := s.e
	repo, ok := readOrigin(ctx, s.loc.Root)
	if !ok {
		return skip("this checkout has no origin repository, so the repository it belongs to is unknown" + pinStays)
	}
	switch data, rerr := readFugaroYAML(filepath.Join(s.loc.Root, "fugaro.yaml")); {
	case rerr == nil:
		if p, perr := config.ProjectOf(data); perr != nil || p != e.lc.Name {
			return skip("this checkout's fugaro.yaml names another project (or none), not " + pluginwire.Printable(e.lc.Name) + pinStays)
		}
	case !errors.Is(rerr, os.ErrNotExist):
		// Unknown is not a pass: a file that cannot be read (a symlink, a
		// device, an oversize file) could name any project.
		return skip("this checkout's fugaro.yaml cannot be read, so which project it names is unknown" + pinStays + ": " +
			pluginwire.Printable(oneLineCLI(rerr.Error())))
	}
	switch a, err := e.authState(repo); {
	case err != nil:
		return nil, err
	case a == authNeeded && !e.r.o.enrolling():
		// A door that does not enrol a repository: one line naming the door
		// that does, and never the typed question or --onboard-repo, which
		// would be this run's to give (D5, W5.1).
		lf := enrolLeft(e.r.o.doorName())
		return status(initflow.NeedsYou, unknownDetailNotEnrolled(e.lc.Name, repo, e.r.o.doorName())+pinStays, &lf)
	case a == authNeeded:
		lf := onboardLeft(repo, e.r.o.doorName())
		return status(initflow.NeedsYou, unknownBecause(e.lc.Name, repo, convergeOptIn(e.lc, repo))+pinStays, &lf)
	}
	return nil, nil
}

// Check is status plus the one fact a later stage reads off this stage: a
// plugin stage whose install half has something to do is this run taking this
// machine's plugin — and the checkout — to this release, and the checkout's own
// cloud half, the job images built FROM the base the images stage pins in this
// same run, is what moves with it (init_checkout_upgrade.go's Check reads
// initEngine.checkoutBehind). Set and never cleared, which is why the flag is
// not simply assigned: the run's own write moves what it was read from — the
// install that clears the version behind it — and the execution pass checks
// this stage again after the apply that changed it.
//
// The pin's removal alone is not that fact: a checkout that carries a pin and
// the plugin this release already installs has nothing of the run's to move but
// the two entries, and a billable image rebuild is not what taking them out
// asks for (U5).
func (s *pluginStage) Check(ctx context.Context) (initflow.Status, error) {
	st, err := s.status(ctx)
	if err == nil && s.installStep {
		s.e.checkoutBehind = true
	}
	return st, err
}

// status is the stage's whole answer, and it is one function because Check and
// Plan have to read the same thing: the install's own state (the plugin in the
// user's own Claude Code, which has been a step of this stage since U1), then
// the pin an older fugaro left, which U5 takes out, with the gate that
// authorises that write read beside it (s.pin). Deleting the pin's writer
// narrowed the gate to the removal (F24, D955): the machine's plugin is the same
// one for every checkout, so a checkout this run may not take the write into is
// not a machine with nothing to install (U1, F13) — and the gate is not read at
// all where the checkout carries no pin, which is the machine F24 describes.
//
// The removal's line stands beside the install's, so a reader sees both, and a
// pin the removal refused is not a line beside a stage that is done: it is
// something the run leaves for the user, and the loop applies only what a check
// calls something to do, so that case is worded as the Todo it is (F25).
//
// The gate's own refusal is worded that way too, and for D955's reason: the
// gate is the checkout write's, the install is the machine's, and a teammate
// whose machine lists no repository — the one F24 is about — still gets the
// plugin (D955, U8). So the gate's sentence stands on the line beside the
// install instead of ending the stage, the pin stays exactly where it is, and
// Apply turns the gate's own left — the door that enrols the repository — into
// the run's, which is the F25 shape: the loop does not stop, and the exit code
// says the step is the reader's.
func (s *pluginStage) status(ctx context.Context) (initflow.Status, error) {
	s.locate()
	inst := s.install(ctx, initflow.Env{}, true)
	if inst.state == stepFailed {
		return initflow.Status{}, errors.New(inst.reason)
	}
	rem, note, st, err := s.pin(ctx)
	if err != nil {
		return initflow.Status{}, err
	}
	gate := ""
	if st != nil {
		gate = st.Detail
	}
	var out initflow.Status
	switch {
	case inst.state == stepStale:
		// The plugin in Claude Code is a release behind what this fugaro
		// installs: something to do, whatever the gate says (#1836c) — and the
		// gate's sentence stands beside it, as it does below.
		s.installStep = true
		out = initflow.Status{State: initflow.Todo, Detail: beside(behindLine(inst), gate)}
	case !s.installs():
		// A development build has no release plugin to install: the install
		// half's own skip, and not something to do.
		out = initflow.Status{State: initflow.Skipped, Detail: inst.reason}
	case inst.state != stepCurrent:
		// Nothing installed on this machine, or a record this fugaro cannot
		// compare — neither reads as current, which is what --check's own
		// answer words as a skip because it is about the plugin the machine
		// already has, where this stage has the install itself to make. The
		// gate's own sentence stands beside it where the checkout is not this
		// run's to write: the reader learns both, and nothing of the checkout
		// is written.
		s.installStep = true
		out = initflow.Status{State: initflow.Todo, Detail: beside("installs the plugin in Claude Code: "+inst.reason, gate)}
	case st != nil && st.State == initflow.NeedsYou:
		// The machine's plugin is current and this checkout is not this run's
		// to write: what the run leaves is the gate's step, and the stage is
		// something to do because the loop exports a left off an apply and off
		// nothing else (F25). Nothing of the checkout is written.
		out = initflow.Status{State: initflow.Todo, Detail: beside("No changes: "+inst.reason, gate)}
	case st != nil:
		// A skip of the checkout's own read: the pin is not this run's to take
		// out, which is all the line has to say.
		out = *st
	default:
		out = initflow.Status{State: initflow.Done, Detail: "No changes: " + inst.reason}
	}
	if rem != nil {
		line := removalLine(rem)
		if out.State == initflow.Todo {
			out.Detail = beside(out.Detail, line)
		} else {
			// The install half is current (or is not a step of this build) and
			// the pin is the whole of what is left to do.
			out = initflow.Status{State: initflow.Todo, Detail: line}
		}
	}
	if note != "" {
		out.State, out.Detail = initflow.Todo, beside(out.Detail, note)
	}
	return out, nil
}

// beside appends a sentence to a stage's line, or is the line when there is
// none: a pin the removal refused goes beside whatever the stage was going to
// say, so the reader learns both and the run goes on to the next stage (spec
// §8).
func beside(line, note string) string {
	switch {
	case note == "":
		return line
	case line == "":
		return note
	}
	return line + "; " + note
}

// removal plans the pin's removal, and is the sentence for a pin that is not
// this package's to take out. A file that is not usable settings, a
// marketplace that is not Fugaro's own repository at a release tag, a plugin
// the repository disabled: each is left exactly as it is — that is not a
// failure, and the run continues (spec §8) — and the sentence is the one the
// read side already has for the same file (pluginwire's refusal errors, and
// readPin's own text).
//
// It reads only where there is a checkout this run may write into, and only on
// a release build: a development build installs no release plugin and migrates
// nothing, which is the install half's own skip.
func (s *pluginStage) removal() (*pluginwire.Removal, string) {
	if !s.found || !s.installs() {
		return nil, ""
	}
	rem, err := pluginwire.PlanRemoval(s.loc.Settings)
	if err != nil {
		return nil, "the pin is left as it is: " + oneLineCLI(err.Error())
	}
	if !rem.Changed {
		// Nothing is removed twice (spec §8): the offer fires while the entries
		// are there and stops when they are gone.
		return nil, ""
	}
	return rem, ""
}

// removalLine is the stage's line for a pin it will take out, and the whole of
// what a plan-only run is told about it: what the entries are and where.
func removalLine(rem *pluginwire.Removal) string {
	return "removes the " + pluginwire.PluginID + " pin an older fugaro left in " + pluginwire.Printable(rem.Path)
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

// install is the stage's principal write, and it is fugaro upgrade's own plugin
// step (upgrade_plugin.go), in check mode when the caller is only reading: the
// plugin installed or updated in the user's own Claude Code at user scope. It
// is the same function fugaro upgrade runs — the stage added a caller, not a
// copy — and it runs from any directory, checkout or not (D955, U8): what it
// writes is the machine's, and the checkout it is given is only the directory
// claude is run in (claudeplugin.Runner's Dir, cwd when there is none).
func (s *pluginStage) install(ctx context.Context, env initflow.Env, check bool) stepResult {
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
	s.locate()
	rem, note, st, err := s.pin(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	// The removal first: it is the checkout's write, it prints what it takes
	// out, and where the session may not make it there is a line to run, which
	// is not a reason to leave the machine's own plugin as it is.
	removed := false
	var left *initflow.Left
	if rem != nil {
		if removed, left, err = s.remove(rem, env); err != nil {
			return initflow.Outcome{}, err
		}
	}
	// The install half, in upgrade's own words: the step prints its line under
	// this stage (`plugin: done: installed vX, ...`), which is the line the
	// staleness warning waits for, and a failed step fails the stage here.
	inst := s.install(ctx, env, false)
	printStep(s.e.r.w, inst)
	if inst.state == stepFailed {
		return initflow.Outcome{}, errors.New(inst.reason)
	}
	out := initflow.Outcome{Detail: "No changes"}
	switch {
	case removed && inst.state == stepDone:
		out = initflow.Outcome{Changed: true, Detail: removedLine(rem) + "; " + inst.reason}
	case removed:
		out = initflow.Outcome{Changed: true, Detail: removedLine(rem)}
	case inst.state == stepDone:
		out = initflow.Outcome{Changed: true, Detail: inst.reason}
	}
	out.Left = left
	switch {
	case out.Left == nil && st != nil && st.State == initflow.NeedsYou:
		// The gate refused the checkout write: the pin is left exactly as it is
		// and the gate's own step is the run's, which is the F25 shape — and
		// the install above has already been made, because it is the machine's
		// and not the checkout's (D955). The gate's left names the door that
		// enrols the repository; without one, its sentence is the entry, so a
		// refusal never disappears.
		out.Left = st.Left
		if out.Left == nil {
			out.Left = pinLeft(st.Detail)
		}
		out.Detail = beside(out.Detail, st.Detail)
	case out.Left == nil && note != "":
		// A pin the removal may not take out is left for the user, and it does
		// not end the run (F25, spec §8): the sentence the stage's line carries
		// is the entry, and the install half has already been made.
		out.Left = pinLeft(note)
		out.Detail = beside(out.Detail, note)
	}
	return out, nil
}

// pinLeft is the pin the removal refused, as the run's left-for-you entry. Its
// kind is console rather than a command because there is no command of fugaro's
// that takes this pin out — the marketplace is not ours to remove, and the file
// may be edited by whoever pinned it — and the sentence is the refusal itself,
// which names the condition and the file (spec §8's "left exactly as it is,
// with the existing refusal text").
func pinLeft(note string) *initflow.Left {
	return &initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftConsole, Text: note}
}

// remove is the removal's own apply, and its arms are the other checkout
// writes' (writeAnchor): the diff, then one sentence about what the repository
// loses, then --yes writes, a person at the terminal writes having read the
// diff, and a session that may not write it leaves the file alone and prints
// the line to run instead. It reports whether the file was written, and the
// step left where it was not.
//
// The coding agent's arm comes first, and before --yes: the removal is a change
// to a file the repository commits, and an agent does not decide that, --yes
// included (spec §8). Nothing unsets the marker, and no flag, environment
// variable or non-interactive mode gets around this: the refusal is the one
// fugaro update already makes for the writes that reach outside the machine.
//
// The arm after it is the managed project's gate, where the tree puts it for
// the steps the gate judges no document of (init_fugaroyaml.go's writeAnchor,
// init_layer_stage.go's confirmWrite): the owner at their own terminal is asked
// nothing, and reads the sentence for the step instead — the diff above is what
// they read, and it is printed before the arm is reached. Every other session
// keeps the confirmation it had, so a run the gate does not stand in for — no
// gate read, a project that is not managed, --ask, a pipe, a coding agent — is
// exactly what it was before this arm existed.
func (s *pluginStage) remove(rem *pluginwire.Removal, env initflow.Env) (bool, *initflow.Left, error) {
	w := s.e.r.w
	fmt.Fprintf(w, "%s\n%s", rem.Path, removalDiff(rem))
	fmt.Fprintln(w, "  "+removalLoss())
	switch {
	case s.e.options().Agent != "":
		fmt.Fprintf(w, "  %s\n", initflow.AgentRefusal(s.e.options().Agent))
		return false, removalLeft(rem), nil
	case s.e.r.gateStandsIn() && initflow.PersonAtTheTerminal(s.e.r.conditions()):
		fmt.Fprintf(w, "  %s\n", initflow.OwnerProceeded())
	case env.Yes:
		fmt.Fprintln(w, "  confirmed by --yes")
	default:
		ok, err := s.e.r.confirmNow("remove the " + pluginwire.PluginID + " pin from " + rem.Path + "?")
		if err != nil {
			return false, nil, err
		}
		if !ok {
			fmt.Fprintln(w, "  "+removalByHand(rem))
			return false, nil, nil
		}
	}
	if err := rem.Apply(); err != nil {
		return false, nil, err
	}
	fmt.Fprintln(w, "removed the "+pluginwire.PluginID+" pin from "+pluginwire.Printable(rem.Path)+
		". Review it with git diff and commit it like any change, or discard it.")
	return true, nil, nil
}

// removalDiff is the removal as the two entries going, the way every settings
// write shows its edit (settingsEditDiff). The table lineDiff builds is capped
// off the same way a layer's diff is: a settings file is a few lines, and one
// that is not is not worth gigabytes of table to print.
func removalDiff(rem *pluginwire.Removal) string {
	x, y := splitLines(string(rem.Before)), splitLines(string(rem.After))
	if n := len(x) * len(y); n > diffMaxCells {
		return fmt.Sprintf("  (the diff is skipped: %d and %d lines would need a table of %d cells)\n", len(x), len(y), n)
	}
	return printableLines(lineDiff(string(rem.Before), string(rem.After)))
}

// removalLoss is the one sentence about what the repository loses, and it is
// the cost D943 accepted: the pin was how a checkout gave every teammate the
// skills without any of them running anything, and the machine's own install is
// what replaces it there (spec §8).
func removalLoss() string {
	return "the repository loses the pin, so a teammate who clones it takes the skills from their own machine's install instead of from the checkout: nothing here is committed, and the skills are already installed at user scope on this machine"
}

// removalByHand is what is printed where the removal is not made and nobody was
// asked — the entry to delete, and the command that takes it out.
func removalByHand(rem *pluginwire.Removal) string {
	return "nothing was written: to take the pin out yourself, delete the fugaro entry from extraKnownMarketplaces and the " +
		pluginwire.PluginID + " entry from enabledPlugins in " + pluginwire.Printable(rem.Path) + ", or run " + selfCommand() + " update at your own terminal"
}

// removalLeft is the checkout's write as a step only the user can run: the pin
// is a committed file's, so a coding agent's session prints what it would take
// out and leaves it. The command is the door the removal is in — `fugaro
// update` — and not `update-skills`, which is the install's own route and
// writes the machine (pluginStage.Left): what is left here is a write into the
// checkout, and an owner sent to the machine-side command would find no pin
// taken out.
func removalLeft(rem *pluginwire.Removal) *initflow.Left {
	return &initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftCommand,
		Text: "in your own terminal window, not through a coding agent or a pipe, remove the " + pluginwire.PluginID +
			" pin an older fugaro left in " + pluginwire.Printable(rem.Path) + " — the diff above is the two entries — by running",
		Commands: []string{selfCommand() + " update"}}
}

// removedLine is the stage's line after the removal was written: what it was,
// and that it is the working tree's.
func removedLine(rem *pluginwire.Removal) string {
	return "removed the " + pluginwire.PluginID + " pin from " + pluginwire.Printable(rem.Path) + "; review it with git diff and commit it like any change, or discard it"
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
