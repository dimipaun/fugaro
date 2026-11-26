package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The checkout stage of the converge (initflow.Checkout): the job images of
// every repository this project lists, on the one command, `fugaro update` —
// the owner's rule of 2026-10-30, one fugaro update is the whole upgrade, and
// U4 of docs/plugin-user-scope/implementation-plan.md retired the second
// command that used to run these steps. The user is busy and is not expected to
// run another one, so each repository's job images move here, with the version
// the binary just became.
//
// It reads no working directory (U4 of docs/update-from-anywhere, R10): it asks
// the run for the project's repositories (e.repos, contract C1) and runs the
// cloud step once per repository, from that repository's fugaro.yaml and head
// commit — the same staleness test and the same build a checkout ran before
// (R6). A repository it cannot read, one whose file names another project and
// one this machine cannot build get one line each, named, and the run goes on:
// the stage is the project's, not the directory's.
//
// It calls the step itself and never a copy of it, so the mode and the apply
// cannot drift: a refusal (a cloud write in a coding agent's session) is the
// step's own, in its own words, and what `fugaro update --check` reads is the
// same function (update_check.go). The policy step is not among them: the
// policy stage
// (init_policy_migrate.go) is that migration, registered for the same
// checkout, and running it twice would migrate one node twice. The pin and the
// plugin are not among them either, and that is decision D941 (U1 of
// docs/plugin-user-scope/implementation-plan.md): they are the plugin stage's
// own, because that stage runs before the repository stage and the plugin
// install is the step that clears the staleness line every command prints — a
// step that a stopped repository, or a repository the run never reached, may
// not take with it (reconciliation.md F13).
//
// The stage is skipped, not failed, where it has no repository to work on: a
// project whose repositories this machine read none of has no job images to
// refresh, and so has one whose every repository is skipped for a reason of its
// own.

// checkoutSteps are the steps this stage runs for each repository, in the order
// the converge's own four have, minus the policy step the converge already runs
// as its own stage and minus the pin and the plugin, which are the plugin
// stage's (init_plugin_stage.go).
var checkoutSteps = []checkStep{{"cloud", cloudStep}}

type checkoutStage struct {
	e *initEngine
	// pastStops are the stops this run is running past this stage
	// (initflow.PastStops), the repository stage's needs-you stop when it has
	// one: the stage's cloud step builds from the repositories this run
	// onboards, so the run that is running past the stop drops it rather than
	// building out of order, and says so in its own line rather than leaving
	// the reader to guess what became of the images.
	pastStops []string
	// lines is what this run said about each repository it read: one line per
	// repository, built as the plan and the apply answer for them (C1). The
	// loop reads them off the stage through RepoLines (U13).
	lines []initflow.RepoLine
	// planned is the last plan this stage answered for, which is the one the
	// loop's own line about the stage is built from (Note). A step this run
	// does not run is a fact about the plan, and the loop asks for the line
	// after the stage has answered.
	planned []repoPlace
}

func newCheckoutStage(e *initEngine) *checkoutStage { return &checkoutStage{e: e} }

func (*checkoutStage) Name() string { return initflow.Checkout }

// PastStops is the run's own answer to whether the repository stage stopped
// (initflow.PastStops): what this stage's row tolerates is a stop the run is
// at, and which stop it is cannot be read off the world — the run's own writes
// move the world, and the repository stage's anchor edits a repository's
// fugaro.yaml in the run that onboarded it.
func (s *checkoutStage) PastStops(stops []string) { s.pastStops = stops }

// SelfConfirming: the steps keep their own confirmations — the cloud step's
// per-step and per-build ones — which is what lets the stage run where the loop
// would otherwise ask for the run's own typed name.
func (*checkoutStage) SelfConfirming() {}

// LocalUnderAgent: in a coding agent's session the loop applies this stage
// instead of leaving it for the user (initflow.LocalUnderAgent). Every step it
// has left is one the session may not take — the cloud half, whose writes are
// not this machine's — so what the marker buys here is not the step but the
// run's account of it: the stage's plan drops the cloud step under a marker,
// in that step's own sentence (cloudAgentRefusal), and the apply leaves it for
// the user as a named step with the command that runs it, where a stage the
// loop never applied would be one line naming this door and nothing of what
// became of the images. The machine's own writes — the pin and the plugin
// in the user's own Claude Code — are the plugin stage's since U1, and that
// stage is LocalUnderAgent for the same reason it is there.
func (*checkoutStage) LocalUnderAgent() {}

// Left is the command that runs this stage's half in the user's own terminal:
// this door, which is the one command the whole upgrade is (D948, U4). It is
// the project's repositories it re-reads, so it names no repository and no
// directory (againView).
func (s *checkoutStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Checkout, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// RepoLines is what this run said about each repository it read: one line per
// repository, in canonical owner/name order, which is how the loop carries
// them into the stage's result and its event (C1, U13).
func (s *checkoutStage) RepoLines() []initflow.RepoLine { return s.lines }

// Note is this run's one line about the cloud step it did not run, which the
// loop puts in the stage's own line (StageNote): the lines this stage reports
// say how each repository was left and never that the job images were not
// moved for it, and a run whose every repository's step is skipped has no step
// line of its own to say it in — the account would read "checkout: skipped: 1
// repository: 1 unread" and never a word about the images. It is one line for
// the whole run, and the reasons are clauses, so a project of four skipped
// repositories still says it in one line.
func (s *checkoutStage) Note() string { return skippedHere(s.planned) }

// repoPlace is one repository's cloud step in this run: the step, the view the
// run read the repository through, and the line saying why it is skipped here
// when it is. The stage has one place per repository of the project.
type repoPlace struct {
	step checkStep
	view repoView
	skip string
	// clause is the same reason in a clause, for the one line the run prints
	// about the step it did not run (skippedHere): skip is the whole of it —
	// what the repository's own line and the step's carry, with the fix — and a
	// line naming the step for the whole run cannot hold four of those.
	clause string
	// state is how the stage leaves this repository where it does not run the
	// step for it: unread is the reader's own word for one it could not read
	// (R13), skipped for one the stage does not apply to, needs you for one
	// only the owner can take the next step on.
	state initflow.RepoState
	// parked: this run does not run the step and never will (the cloud half in a
	// coding agent's session, placeFor), so the stage is done with what it may
	// do and still not done: the run reports the step as left for the user, and
	// says so on every run until it is run where it can be.
	parked bool
}

func (p repoPlace) repo() string { return p.view.Repo }

// plan is this run's places, one per repository of the project, and the one
// line saying why the stage has none. It is read once per answer — the reader
// memoizes the list per run (C1) — and it resets the reported lines, which each
// answer builds again for the repositories it answered for.
func (s *checkoutStage) plan(ctx context.Context) ([]repoPlace, string) {
	if releaseVersion() == "" {
		return nil, "a development build has no release to refresh to"
	}
	views, _ := s.e.repos(ctx)
	if len(views) == 0 {
		return nil, "no repository of this project was read, so there is no job image to refresh"
	}
	s.lines, s.planned = nil, nil
	places := make([]repoPlace, 0, len(views))
	for _, v := range views {
		places = append(places, s.placeFor(v))
	}
	s.planned = places
	return places, ""
}

// placeFor is the cloud step for one repository this run read, or the line
// saying why the stage does not run it here. Every arm but the last is a skip
// with the line the run reports: a repository the reader could not read is
// never planned from (R13, RepoUnread); one whose fugaro.yaml names another
// project (or none) is not this run's to touch; one this machine has no local
// config entry for cannot be built from here — the region, the registry host
// and the image names are read from that entry — and that build is the owner's
// go (V1, D3350, R20); and a run whose repository stage stopped for the user
// builds nothing for the repository it stopped on (repositoryStopped). Under a
// coding agent's marker the step is parked rather than skipped: it is the
// user's, in the terminal where it can run.
func (s *checkoutStage) placeFor(v repoView) repoPlace {
	p := repoPlace{step: checkoutSteps[0], view: v, state: initflow.RepoDone}
	u := s.placeCtx(initflow.Env{}, p)
	switch {
	case !v.Read():
		p.skip, p.clause, p.state = v.Unread, "the repository could not be read", initflow.RepoUnread
	case v.Cfg == nil:
		p.skip, p.clause, p.state = "its fugaro.yaml could not be parsed from its default branch", "its fugaro.yaml could not be parsed", initflow.RepoUnread
	case !s.thisProject(v):
		p.skip, p.clause, p.state = "its fugaro.yaml names another project (or none), not "+pluginwire.Printable(s.e.lc.Name)+": its job images are not refreshed here", "its fugaro.yaml names another project", initflow.RepoSkipped
	case s.repositoryStopped() != "":
		p.skip, p.clause, p.state = s.repositoryStopped(), "the repository stage stopped for the user in this run", initflow.RepoSkipped
	case !buildableHere(s.e.lc, v):
		p.skip, p.clause, p.state = "this machine has no local config entry saying which provider serves it, so its job images cannot be built from here: onboard it here first ("+selfCommand()+" update --repo <its checkout>)", "this machine has no local config entry for it", initflow.RepoNeedsYou
	case u.agent != "":
		p.skip, p.clause, p.parked, p.state = cloudAgentRefusal(u), "a coding agent's session never applies cloud changes", true, initflow.RepoNeedsYou
	default:
		return p
	}
	s.lines = append(s.lines, newRepoLine(v.Repo, p.state, p.skip))
	return p
}

// thisProject is whether a repository's fugaro.yaml names this run's project:
// the file of another project is not this run's to touch, its job images least
// of all. A file whose project cannot be read is that case too — unknown is not
// the same as matching, and it could name any project — and the reader has
// already refused a file it could not parse (R13).
func (s *checkoutStage) thisProject(v repoView) bool {
	p, err := config.ProjectOf(v.YAML)
	return err == nil && p == s.e.lc.Name
}

// buildableHere is whether this machine can build a repository's job images:
// the local config's entry for it is what the region, the registry host, the
// image names and the clone URL are read from, so a repository known only from
// the project's list is one this stage reports rather than builds (V1, D3350).
func buildableHere(lc *localcfg.Config, v repoView) bool {
	e, ok := viewEntry(lc, v)
	return ok && e.Provider != ""
}

// repositoryStopped is why the cloud step is dropped: this run stopped at the
// repository stage for the user, and the job images this step builds are that
// stage's — built for the repositories it onboards, off the layer and the
// fugaro.yaml it just touched — so a run that did not get them onboarded builds
// nothing. The stop is the run's own (PastStops), never a fresh look at a
// repository: the repository stage's own answers move the world under it — its
// anchor writes gcp_project: into a repository's fugaro.yaml in the very run
// that onboarded it, which leaves that repository's working tree different from
// its default branch — and a stage that re-derived the stop from that would
// drop the refresh of a repository the run had just onboarded, on that run and
// on every run until the edit is merged (#1836).
//
// It is every needs-you stop of that stage, not only the ones about a file:
// a repository the run may not onboard — an unlisted one, an agent's refusal,
// a missing GitHub App ID — is one whose job images may not be built either,
// and the build submitted over it fails at the job's own steps and leaves the
// user a second command this door may not name.
//
// The stop names no repository (initflow.PastStops is the stage's answer), so
// it drops the step for every repository of the project; a run that stopped on
// one of them is a stop to tell apart per repository, which is the reader's
// list to carry when it lands (I1).
func (s *checkoutStage) repositoryStopped() string {
	if slices.Contains(s.pastStops, initflow.Repository) {
		return "the repository stage stopped for the user in this run, and this stage's job images are built for the repositories it onboards: they are not refreshed here, and the next " + s.e.r.o.doorProgram() + " that gets past that stage refreshes them"
	}
	return ""
}

// detail is the stage's one line, in both the read-only view and the plan: the
// repositories it refreshes, and the ones it does not, with the reason.
func (s *checkoutStage) detail(places []repoPlace) string {
	runs := make([]string, 0, len(places))
	for _, p := range places {
		if p.skip == "" {
			runs = append(runs, pluginwire.Printable(p.repo()))
		}
	}
	line := "moves this project's job images to this release"
	if len(runs) > 0 {
		line += ": " + places[0].step.name + " for " + strings.Join(runs, ", ")
	}
	if tail := skippedHere(places); tail != "" {
		line += "; " + tail
	}
	return line
}

// skippedHere is the one line this run prints about the cloud step it did not
// run, or "" when it ran it for every repository: one clause per repository the
// step was skipped for, in the words the run has always said it in ("skipped
// here: acme/app: cloud (…)"). The step is named because the repository's own
// line names the repository and its state and never the step — a reader who
// saw "acme/app: unread: …" learned nothing about the job images that were not
// built — and it is one line for the run rather than one per repository, so a
// project whose cloud step was skipped for four of them still says it once.
//
// The reason is a clause here and whole in skip: a line naming the step for
// every repository of a project cannot hold four paragraphs of why, and the
// command that fixes each one is on the repository's own line.
func skippedHere(places []repoPlace) string {
	var skipped []string
	for _, p := range places {
		if p.clause != "" {
			skipped = append(skipped, pluginwire.Printable(p.repo())+": "+p.step.name+" ("+p.clause+")")
		}
	}
	if len(skipped) == 0 {
		return ""
	}
	return "skipped here: " + strings.Join(skipped, "; ")
}

// Check answers whether this project has anything to do, by running each
// repository's step in its check mode: the same mode `fugaro update --check`
// runs (u.o.check), which reads and decides and never writes, never asks and
// never calls the cloud. Every repository at this release is one already at it,
// and a second fugaro update then writes nothing, builds nothing and needs no
// terminal. Anything else is something for the apply, which reports it in the
// step's own words — and a step that fails in its check mode fails the stage
// here, at the same point the apply would.
//
// The cloud step's own verdict is the one thing that check mode cannot settle
// on its own: it reads fugaro.yaml and the local config and never a build
// record, so the run that is taking these repositories to this release — the
// run whose job images move with it — has to be asked about separately
// (initEngine.checkoutBehind).
func (s *checkoutStage) Check(ctx context.Context) (initflow.Status, error) {
	places, why := s.plan(ctx)
	if why != "" {
		return initflow.Status{State: initflow.Skipped, Detail: why}, nil
	}
	var current []string
	todo := false
	for _, p := range places {
		if p.skip != "" {
			// Not this repository's step at all: nothing to do about it here.
			continue
		}
		// Every repository is checked, not just up to the first with something
		// to do: a step that fails here fails the run before any of them
		// writes.
		switch r := s.checkPlace(ctx, p); r.state {
		case stepFailed:
			return initflow.Status{}, errors.New(r.reason)
		case stepCurrent:
			if s.e.checkoutBehind {
				// The cloud step's own check reads fugaro.yaml and the local
				// config and never a build record (cloudCheck), so it cannot
				// see the job images the refresh moves: a run that is taking
				// these repositories to this release is one whose job images
				// move with it (initEngine.checkoutBehind), and what they are
				// on is the apply's to say, not this check's. The step is the
				// only one this stage has, so dropping it here is dropping the
				// stage — which is what a run that stopped refreshing the
				// repositories' job images on an upgrade was doing.
				todo = true
				break
			}
			current = append(current, p.repo())
		default:
			todo = true
		}
	}
	switch {
	case parked(places):
		return initflow.Status{State: initflow.Todo, Detail: s.detail(places)}, nil
	case allSkipped(places), todo || len(current) == 0:
		// Every repository of the project is skipped for a reason of its own:
		// there is nothing here for this stage to do.
		state := initflow.Todo
		if allSkipped(places) {
			state = initflow.Skipped
		}
		return initflow.Status{State: state, Detail: s.detail(places)}, nil
	}
	return initflow.Status{State: initflow.Done, Detail: "up to date: " + strings.Join(current, ", ")}, nil
}

// parked reports whether this run leaves one of the repositories' steps for the
// user. A step that is parked is not done and never will be here, so the stage
// is not done either: reporting it as such is what made a coding agent's
// session read as a finished checkout while the images were never touched, and
// what made the second run of one drop even the note that they were skipped.
// The apply is what says which step it is (leftBehind), so the line keeps the
// plan's own words on every run.
func parked(places []repoPlace) bool {
	for _, p := range places {
		if p.parked {
			return true
		}
	}
	return false
}

// allSkipped reports whether every repository of the project is skipped here, so
// that the stage has nothing to do at all; a stage with no repository at all is
// the plan's own line and not this.
func allSkipped(places []repoPlace) bool {
	if len(places) == 0 {
		return false
	}
	for _, p := range places {
		if p.skip == "" {
			return false
		}
	}
	return true
}

// placeCtx is what the steps see for one repository this run read: the run's own
// command and output, the flags the steps read, and the view the step runs for
// (u.view). The location is the zero one, because no working directory is read
// for a repository the run read from the project's list (R10).
func (s *checkoutStage) placeCtx(env initflow.Env, p repoPlace) *stepCtx {
	u := s.e.stepCtx(env, pluginwire.Location{})
	u.view = &refreshView{v: p.view, lc: s.e.lc, lcPath: s.e.path, lcOld: s.e.old}
	return u
}

// checkPlace is one repository's step in its check mode: read-only, no prompt,
// and quiet. The notes and warnings a check prints — the fork warning, the
// caveat on the deployed job's config copy — belong to the apply that really
// runs the step, not to a look at whether there is anything to do.
func (s *checkoutStage) checkPlace(ctx context.Context, p repoPlace) stepResult {
	u := s.placeCtx(initflow.Env{}, p)
	u.o.check = true
	u.w = io.Discard
	c := *s.e.r.cmd // the run's command, its output silenced: the steps' side notes go there
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetContext(ctx)
	u.cmd = &c
	return p.step.run(ctx, u)
}

// record is one line about one repository, from the step's own result: how the
// stage left it, and the step's own sentence about it. A place that was skipped
// reported its line when it was planned (placeFor).
func (s *checkoutStage) record(p repoPlace, r stepResult) {
	if p.skip != "" {
		return
	}
	s.lines = append(s.lines, newRepoLine(p.repo(), repoStateOf(r), r.reason))
}

// repoStateOf is where one repository's step left it, in the list's own words: a
// step that ran to the end left it done, and one it skipped or failed on is the
// owner's to take next.
func repoStateOf(r stepResult) initflow.RepoState {
	switch r.state {
	case stepSkipped, stepFailed:
		return initflow.RepoNeedsYou
	}
	return initflow.RepoDone
}

func (s *checkoutStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	places, why := s.plan(ctx)
	if why != "" {
		return initflow.Plan{NothingToDo: true, Detail: why}, nil
	}
	return initflow.Plan{Detail: s.detail(places)}, nil
}

// Apply runs the stage's steps for the project's repositories in the order
// checkoutSteps gives them and stops at the first that fails — one stop, and
// the repositories after it report the step the run never reached. Its outcome
// is the steps' own: something changed, or everything was already current or
// skipped.
func (s *checkoutStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	places, why := s.plan(ctx)
	if why != "" {
		return initflow.Outcome{Detail: why}, nil
	}
	if allSkipped(places) && !parked(places) {
		// Nothing here is this stage's to run: every repository is skipped for a
		// reason the plan's line already says, and there is nothing to ask
		// about or build.
		return initflow.Outcome{Detail: s.detail(places)}, nil
	}
	u := s.e.stepCtx(env, pluginwire.Location{})
	if !env.Yes && !env.Interactive && u.agent == "" {
		// The cloud step asks before every change, each billable build
		// included, and there is no terminal here to ask at; the stage
		// leaves the project for the user rather than running a step
		// it cannot confirm. A coding agent's session
		// is not that case, and it is the one the gate has to look at: the
		// save from leaving the stage to the user there is no longer a step
		// of its own (the pin and the plugin became the plugin stage's, U1),
		// and what placeFor's agent arm drops is the cloud half alone — so a
		// gate that answered the marker's no-terminal run with needs-you
		// would leave the run's account of the images at the stage's own
		// line, with none of the step's own sentence in it.
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	// The refresh reads its context off the command (image_refresh.go), and the
	// loop's own context is the run's: the steps stop when it does. The command
	// belongs to the run, not to this stage, so the context it came with goes
	// back when the stage is done: a later stage reads what it would have read
	// had this one never run.
	prev := s.e.r.cmd.Context()
	s.e.r.cmd.SetContext(ctx)
	defer s.e.r.cmd.SetContext(prev)
	var done []string
	for _, p := range places {
		pu := s.placeCtx(env, p)
		r := stepResult{name: p.step.name, state: stepSkipped, reason: p.skip}
		if p.skip == "" {
			r = p.step.run(ctx, pu)
			r.name = p.step.name
			s.record(p, r)
		}
		printRepoStep(s.e.r.w, p.repo(), r)
		if r.state == stepFailed {
			return initflow.Outcome{}, s.stepFailure(r, pu, p.step)
		}
		if r.state == stepDone {
			done = append(done, p.step.name+" for "+pluginwire.Printable(p.repo()))
		}
	}
	out := initflow.Outcome{Detail: "No changes"}
	if len(done) > 0 {
		out = initflow.Outcome{Changed: true, Detail: "ran " + strings.Join(done, ", ")}
	}
	out.Left = s.leftBehind(env, places)
	return out, nil
}

// printRepoStep is one step's line for one of the project's repositories: the
// step, its state, the repository it ran for, and why. One line per repository,
// so a run that refreshed several says what it did where — and a run that read
// none of them never reaches here.
func printRepoStep(w io.Writer, repo string, r stepResult) {
	line := r.name + ": " + string(r.state) + ": " + pluginwire.Printable(oneLine(repo))
	if r.reason != "" {
		line += ": " + r.reason
	}
	fmt.Fprintln(w, line)
}

// stepFailure is a step of this stage that failed, as the run reports it: the
// step's own sentence, and, for the cloud half, the fix of it.
//
// Every other step needs no fix of its own: the loop's default (fix what the
// error says and rerun this door, which resumes at this stage) is true there,
// because the stage's check reads the same state the step does and a rerun
// reaches the step again.
//
// The cloud half is where that stops being true, and the loop's default is
// then the one instruction that does nothing: step 2 records this release's
// base image in the local config before step 4 builds from it, and the check
// the rerun starts with (cloudCheck) reads that config and fugaro.yaml and
// never a build record — the records live in the runs bucket — so a build that
// failed, or was not confirmed, reads as a finished cloud half afterwards and
// this door is done at the repository. The run that offers the build again is
// the one whose cloud step really runs the refresh, which is the run this
// step's own stop line names (u.cloudCommand): said here so the two do not
// drift.
func (s *checkoutStage) stepFailure(r stepResult, u *stepCtx, step checkStep) error {
	err := errors.New(r.reason)
	if step.name != "cloud" {
		return err
	}
	return &initflow.StageError{Err: err, Fix: fmt.Sprintf("rerun %s in your own terminal window: its cloud step re-reads the build records, so the build that was not submitted is offered again (this stage's own check reads fugaro.yaml and the local config, never a build record)",
		u.cloudCommand())}
}

// leftBehind is what this apply leaves the user, or nil: the parked cloud half,
// as a step of this run that only the user can run. The command is the one the
// parked step's own refusal names — this run's flags, for the project's
// repositories (cloudCommand, againView) — rather than this stage's one line,
// because the refusal is what knows the flags this run carries. The why is the
// plan's line, in the step's own words (cloudAgentRefusal).
func (s *checkoutStage) leftBehind(env initflow.Env, places []repoPlace) *initflow.Left {
	for _, p := range places {
		if !p.parked {
			continue
		}
		u := s.placeCtx(env, p)
		return &initflow.Left{Stage: initflow.Checkout, Kind: initflow.LeftCommand,
			Text:     "in your own terminal window, not through a coding agent or a pipe, run this project's cloud half — the base images, the daily check jobs' config and the billable job-image builds, each confirmed as it is — by running",
			Commands: []string{u.cloudCommand()}}
	}
	return nil
}

func (*checkoutStage) Verify(context.Context) error { return nil }

// stepCtx is what the steps see for one repository: the converge's own command
// and output, the flags the steps read, and the marker of the coding agent's
// session this run is in — the run's own options().Agent, and not a second read
// of the environment, so that the stages and the loop that drives them answer
// the marker the same way. Under a marker the cloud step refuses before it
// reads anything, so the plugin stage moves the plugin there and the images do
// not.
//
// It is the engine's and not one stage's, because more than one stage of the
// converge runs one of these steps: the plugin stage the plugin's (`plugin:`,
// U1), the checkout stage the cloud half.
func (e *initEngine) stepCtx(env initflow.Env, loc pluginwire.Location) *stepCtx {
	o := e.r.o
	return &stepCtx{
		cmd: e.r.cmd,
		w:   e.r.w,
		o: stepOptions{yes: env.Yes, refresh: refreshOptions{
			door:        o.doorName(),
			imageSource: o.imageSource, expectDigests: o.expectDigests, cloud: o.cloud,
			// The refresh's own run prints and asks where its command says
			// (newInitRun), and this stage runs inside the converge's step:
			// given this run's reader and writer, what it prints takes the step
			// in flight down and the questions it asks hold it down until the
			// answer is in (G1). The reader is the same value the stage's own
			// steps read through, so a typed-ahead answer is not lost between
			// two readers over one stdin.
			in: e.r.in, out: e.r.w, detail: e.r.stageDetail()}},
		loc:   loc,
		agent: e.options().Agent,
		// What the step says about itself — the claude binary it runs, the
		// subprocess lines under it — is the converge's own account, not the
		// step's line: it goes where the rest of that account does (§9.2).
		detail: e.r.stageDetail(),
	}
}
