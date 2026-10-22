package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The gate in front of every stage that works on the checkout (the plugin
// wiring and the repository onboarding). A checkout is somebody's: a freshly
// cloned third-party repository can carry a fugaro.yaml that names this
// project, and onboarding it would create Terraform resources and offer a
// Cloud Build of its configuration in this GCP project. So a repository the
// local config does not list yet is never touched on --yes or
// --non-interactive. It needs one of:
//
//   - its own typed confirmation, naming owner/name (from the origin) and the
//     checkout's path, in the user's own terminal, before the stage's first
//     cloud call or write; or
//   - --onboard-repo owner/name, the one explicit non-interactive opt-in,
//     which must equal the origin repository exactly.
//
// Without either the stage is needs-you, with the one-line opt-in command.

type repoAuth int

const (
	authOK     repoAuth = iota // listed in the local config, or opted in
	authAsk                    // not yet: it can be asked for in this terminal
	authNeeded                 // not yet, and this run cannot ask
)

// authState says where the origin's repository stands. A --onboard-repo that
// is not this checkout's repository is refused whatever else is true. The
// flag names owner/name only, so it opts in only a repository on a provider's
// own host with an origin git config does not rewrite; any other origin takes
// the typed confirmation, which shows the host.
func (e *initEngine) authState(o originInfo) (repoAuth, error) {
	opts := e.r.o
	if opts.onboardRepo != "" && !sameRepo(opts.onboardRepo, o.Repo) {
		return authNeeded, userErr("--onboard-repo %s does not name this checkout's origin repository (%s)", pluginwire.Printable(opts.onboardRepo), pluginwire.Printable(o.Repo))
	}
	// A coding agent never opts a repository in, by the flag either.
	flagOK := opts.onboardRepo != "" && o.standardHost() && !o.Rewritten && !agentEnv(os.Getenv)
	switch {
	case repoKnown(e.lc, o), flagOK, e.authorized[strings.ToLower(o.typed())]:
		return authOK, nil
	case e.declined[strings.ToLower(o.typed())]:
		// Asked once, at the start of the run, and not typed: the stages say
		// what to do and are not asked again.
		return authNeeded, nil
	case e.canConfirmRepo():
		return authAsk, nil
	}
	return authNeeded, nil
}

// checkoutNote is printed once, immediately before the wizard's question, when
// installation-only flags are given in a checkout the question is about (so it
// is printed exactly where that question is asked: not by an `update` run,
// which asks none, and not by a run that cannot ask). W3.4 rewords it: the
// question below it is now how a reader asks for the installation alone, so
// the note points at the answer rather than at another command.
const checkoutNote = "this checkout's repository is not onboarded; the converge in a checkout also onboards its repository, unless the question about it is answered no. To set up only the installation, run fugaro setup from outside the checkout."

// enrolNote is the one line a run since W3.4 prints when there is no checkout
// to ask about: a repository is enrolled by the run that is in a checkout of
// it, and the line names `fugaro setup --repo`, the run D5 gives enrolment to.
// It says nothing about the other door: what `update` does with an unlisted
// repository of its own is W5.1's to settle.
func enrolNote() string {
	return "to enrol a repository later, run " + selfCommand() + " setup --repo in a checkout of it"
}

// notInACheckout is the opening of the repository target's refusal where there
// is no checkout at all, which is the one case with no repository to ask
// about (the others are a checkout the gate refuses). The target builds its
// refusal from it, so the two cannot drift apart.
const notInACheckout = "not in a checkout"

// The wizard's first-repository question (W3.4, D5, answered 2026-10-20 as
// recommended). The converge in a checkout of a repository the project's local
// config does not list asks whether to onboard it, at the start, before any
// stage plans. It is the wizard's question and it authorizes nothing: it is
// asked in front of the gate, not instead of it. Enter takes yes, and yes goes
// on to the same typed owner/name confirmation an unlisted repository has
// always needed; no onboards nothing, skips the repository stage (there is
// nothing left for the user about it: they answered), and says how to enrol
// the repository later.
const (
	onboardAnswerYes = "yes"
	onboardAnswerNo  = "no"
)

// onboardLabel is the question's label: the repository of the checkout, by
// owner/name, as the gate names it everywhere else.
func onboardLabel(o originInfo) string {
	return "Onboard " + pluginwire.Printable(o.typed()) + "?"
}

// asksOnboard is whether this run asks the wizard's question: the setup door,
// at the user's own terminal, in a checkout whose repository the gate would
// ask about anyway (authState == authAsk). Asking exactly where the gate asks
// keeps everything the gate covers unchanged: --yes, --json,
// --non-interactive, a pipe and a coding agent ask nothing, a repository the
// config lists or that --onboard-repo opted in is not asked about, and one
// already answered for this run is not asked again.
func (e *initEngine) asksOnboard(o originInfo) bool {
	if !e.theSetupDoor() || !e.canConfirmRepo() {
		return false
	}
	a, err := e.authState(o)
	return err == nil && a == authAsk
}

// askOnboard asks the question and returns the answer Enter takes as true.
// Anything but an answer — the input ended, or three unusable answers — is an
// error, which is not the user saying no: the gate behind the question decides
// what to do with it.
func (e *initEngine) askOnboard(o originInfo) (bool, error) {
	a, err := e.r.prompt(onboardLabel(o), onboardAnswerYes, func(v string) error {
		switch strings.ToLower(v) {
		case onboardAnswerYes, onboardAnswerNo:
			return nil
		}
		return fmt.Errorf("answer %s or %s", onboardAnswerYes, onboardAnswerNo)
	})
	if err != nil {
		return false, err
	}
	return strings.EqualFold(a, onboardAnswerYes), nil
}

// notOnboardingDetail is what the repository stage says when the answer was
// no: it names the repository the answer was about and how to onboard it
// later. Nothing is left for the user to do — they answered — so the stage is
// skipped, not needs-you.
func notOnboardingDetail(o originInfo) string {
	return "not onboarding " + pluginwire.Printable(o.typed()) + " (the answer was " + onboardAnswerNo + "): to onboard it later, run " + selfCommand() + " setup --repo in this checkout"
}

// gateEarly asks the unknown-repository question at the start of a converge,
// before any stage plans: the installation's and Firebase's Terraform plans
// take minutes, and the question used to come only after them (live Check
// 27). The decision is the same one the stages make and is not weakened: the
// same authState (known, --onboard-repo, or the typed owner/name in the
// user's own terminal, never under --yes, --non-interactive, --json or an
// agent), the same confirmation, before any plan for a checkout. It asks only
// for the repository the repository stage would target (the checkout's
// fugaro.yaml on its default branch names this project); every other case is
// left to the stages, as before. A repository not typed is remembered, so the
// stages report needs-you with the opt-in and do not ask again.
//
// Since W3.4 the setup door asks the wizard's first-repository question first
// (asksOnboard): the answer yes changes nothing about the gate below it, and
// the answer no is the run's — the repository is declined for every stage
// (declineRepo), and the repository stage reports that it is not onboarding it
// and says how to enrol it later. Outside a checkout there is nothing to ask
// about, so that run prints the one line saying how a repository is enrolled
// (enrolNote).
func (e *initEngine) gateEarly(ctx context.Context) {
	if e.repo == nil {
		return
	}
	e.repo.resolve(ctx)
	tg := e.repo.tg
	if tg == nil {
		if e.asksEnrolNote() {
			fmt.Fprintln(e.r.w, "note: "+enrolNote())
		}
		return
	}
	if a, err := e.authState(tg.origin); err != nil || a != authAsk {
		return
	}
	ask := e.asksOnboard(tg.origin)
	if len(e.r.o.installationFlags()) > 0 && ask {
		fmt.Fprintln(e.r.w, "note: "+checkoutNote)
	}
	if ask {
		yes, err := e.askOnboard(tg.origin)
		if err == nil && !yes {
			// The answer is the run's, not this stage's: the repository is
			// left out for every stage behind the gate — the secrets and the
			// plugin wiring write into the checkout and the project, and they
			// read the same answer instead of asking for the owner/name again
			// — and the repository stage is the one that says so itself.
			e.repo.notOnboarding = true
			e.declineRepo(tg.origin)
			return
		}
	}
	if ok, err := e.confirmRepo(tg.root, tg.origin); err == nil && !ok {
		e.declineRepo(tg.origin)
	}
}

// declineRepo remembers, for the rest of the run, that this repository is not
// onboarded: every stage behind the gate reads it (authState's declined case)
// and none of them asks for it again. It is how the owner/name that was not
// typed at the gate is remembered, and how the wizard's question answered no
// is carried to the stages before the repository one (the secrets and the
// plugin wiring), which otherwise ask for the same owner/name themselves.
func (e *initEngine) declineRepo(o originInfo) {
	if e.declined == nil {
		e.declined = map[string]bool{}
	}
	e.declined[strings.ToLower(o.typed())] = true
}

// asksEnrolNote is whether a run with no checkout to ask about says how a
// repository is enrolled: the setup door, at the user's own terminal, where a
// question could have been asked. A run that cannot ask (--yes, --json,
// --non-interactive, a pipe, a coding agent) prints nothing: it was not asked
// for a repository and a line about one would be noise in its output.
func (e *initEngine) asksEnrolNote() bool {
	return e.theSetupDoor() && e.canConfirmRepo() && strings.HasPrefix(e.repo.st.Detail, notInACheckout)
}

// theSetupDoor is whether this run is the wizard's own door: the question and
// the line about enrolling later are the wizard's, and `fugaro update` asks
// neither. The field itself is compared, not doorName(): a caller that built
// the converge's parts without naming a door is not a run of either door.
func (e *initEngine) theSetupDoor() bool { return e.r.o.door == setupDoor.name }

// canConfirmRepo: a typed confirmation needs the user's own terminal, as the
// secrets stage does: never with --yes, --non-interactive or --json, nor
// behind a coding agent.
func (e *initEngine) canConfirmRepo() bool {
	return e.r.canAsk() && !agentEnv(os.Getenv)
}

// onboardLeft is what the user does to opt a repository in: the one-line
// flag for an ordinary origin, else a note to use a clean clone. For a coding
// agent's session it is never the flag, which would be a line for the agent to
// paste: only the typed route, in the user's own terminal.
func onboardLeft(o originInfo, door string) initflow.Left {
	if o.standardHost() && !o.Rewritten && !agentEnv(os.Getenv) {
		return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: selfCommand() + " " + door + " --onboard-repo " + quoteWord(o.Repo)}
	}
	return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftConsole, Text: "in your own terminal window, or in a fresh clone of the real remote, type " + pluginwire.Printable(o.typed()) + " at the prompt of", Commands: []string{selfCommand() + " " + door}}
}

// unknownDetail says why the stage waits for the user.
func unknownDetail(project string, o originInfo) string {
	d := "the repository " + pluginwire.Printable(o.typed()) + " is not one of project " + pluginwire.Printable(project) + "'s yet"
	switch {
	case o.Rewritten:
		d = "the origin URL is rewritten by git config in this checkout (it says " + pluginwire.Printable(o.Raw) + ", git resolves it to " + pluginwire.Printable(o.Effective) + "), so " + d
	case !o.standardHost():
		d += " (host " + pluginwire.Printable(o.Host) + " is not a provider's own)"
	}
	return d + "; onboarding it creates cloud resources, so it needs your own opt-in (not covered by --yes), and the repository stage waits for it"
}

// confirmRepo asks, in the terminal, for the repository typed: the one
// answer that opts it in. Anything else leaves it out.
func (e *initEngine) confirmRepo(root string, o originInfo) (bool, error) {
	r := e.r
	fmt.Fprintf(r.w, "⚠ CONFIRM (project %s, GCP project %s): this checkout's repository is not one of the project's yet.\n", pluginwire.Printable(r.projectName), pluginwire.Printable(r.gcpProject))
	fmt.Fprintf(r.w, "  repository: %s on host %s (the origin)\n  checkout:   %q\n", pluginwire.Printable(o.Repo), pluginwire.Printable(o.Host), root)
	if o.Rewritten {
		fmt.Fprintf(r.w, "  the origin URL is rewritten by git config: the checkout says %s, git resolves it to %s. A checkout can ship its own .git/config: prefer a fresh clone of the real remote.\n", pluginwire.Printable(o.Raw), pluginwire.Printable(o.Effective))
	}
	fmt.Fprintln(r.w, "  Wiring and onboarding it writes to the checkout and creates cloud resources in this project, and the repository's fugaro.yaml can start a Cloud Build. Do it only for a repository you trust and mean to onboard.")
	fmt.Fprintf(r.w, "Type %s to onboard it: ", pluginwire.Printable(o.typed()))
	line, _ := r.in.ReadString('\n') // an ended input is a no
	if strings.TrimSpace(line) != o.typed() {
		return false, nil
	}
	if e.authorized == nil {
		e.authorized = map[string]bool{}
	}
	e.authorized[strings.ToLower(o.typed())] = true
	return true, nil
}

// gateRepo is the same gate for update --repo run on its own, before its first
// cloud call: a repository the local config does not list is onboarded only
// after its owner/name is typed at the user's own terminal, or by
// --onboard-repo (never from a coding agent's session). A known repository
// goes on as it always did. The converge's repository stage has done this
// itself, and --plan-only and --forget change no repository, so they skip it.
func (r *initRun) gateRepo(ctx context.Context, root string, lc *localcfg.Config) error {
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return userErr("this checkout has no origin repository to onboard")
	}
	e := &initEngine{r: r, lc: lc}
	switch a, err := e.authState(oi); {
	case err != nil:
		return err
	case a == authOK:
		return nil
	case a == authAsk:
		if ok, err := e.confirmRepo(root, oi); err != nil {
			return err
		} else if ok {
			return nil
		}
		return userErr("not confirmed (%s was not typed); nothing was changed", pluginwire.Printable(oi.typed()))
	}
	how := "in your own terminal window, not through a coding agent or a pipe, run " + selfCommand() + " " + e.r.o.doorName() + " --repo and type " + pluginwire.Printable(oi.typed()) + " at the prompt"
	if lf := onboardLeft(oi, e.r.o.doorName()); lf.Kind == initflow.LeftCommand {
		how = "pass --onboard-repo " + quoteWord(oi.Repo) + ", or " + how
	}
	return userErr("%s; onboarding it creates cloud resources, so it needs your own opt-in (not covered by --yes): %s", unknownDetail(lc.Name, oi), how)
}
