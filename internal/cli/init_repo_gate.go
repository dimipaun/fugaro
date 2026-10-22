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
// local config does not list yet is never touched by a run that gives no
// confirmation at all: --non-interactive without --yes, --json, a pipe, a
// coding agent's session, or a door that does not own the step. It takes one
// of:
//
//   - the run's own confirmation (D12, 2026-10-20): --yes, which covers
//     onboarding an unlisted repository in either front door's converge, for an
//     origin whose identity is the one it claims; or
//   - its own typed confirmation, naming owner/name (from the origin) and the
//     checkout's path, in the user's own terminal, before the stage's first
//     cloud call or write — which is what the standalone --repo engine always
//     asks, and what a converge asks where the run's own confirmation is not
//     there to give; or
//   - --onboard-repo owner/name, the one explicit non-interactive opt-in,
//     which must equal the origin repository exactly.
//
// Without any of them the stage is needs-you, with the one-line opt-in
// command.

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
	case !opts.repo && e.onboardCovered(o):
		// The converge's onboarding of an unlisted repository is covered by
		// the run's own confirmation (D12's answer of 2026-10-20): --yes, in
		// either front door, for an origin whose identity is the one it
		// claims. It is still never given by a pipe, --json, --non-interactive
		// or a coding agent's session (canConfirmTyped), and the review's own
		// answer is not it either (onboardCovered).
		//
		// The standalone engine (--repo) is not this case: there the gate's
		// question is the identity of the repository in a checkout the run
		// was pointed at, which is the one thing --yes cannot answer, and
		// that path is unchanged (gateRepo).
		return authOK, nil
	case !opts.enrolling():
		// This door does not enrol: the repository is left for the user, with
		// the line naming the door that does. What a door does not own it
		// cannot cover (D5), so `update`'s converge reaches this case when its
		// --yes did not cover the step — an origin whose identity is not the
		// one it claims, or a name the config knows at another host.
		return authNeeded, nil
	case e.canConfirmRepo():
		return authAsk, nil
	}
	return authNeeded, nil
}

// onboardCovered reports whether the converge's own confirmation covers
// onboarding this repository now: YesOrTyped's rule, the class D12 gives the
// step in both front doors, and the run's own confirmation actually given. --yes
// is the way to give it (r.conditions().Yes, which --plan-only turns off); a run
// that cannot give it leaves the repository for the user, and at a terminal the
// typed owner/name is the step's own confirmation, asked where it always was
// (confirmRepo). A coding agent's session, --json, a pipe and --non-interactive
// give nothing (canConfirmTyped).
//
// It reads the run's conditions and never r.review: the review's own answer
// (init_review.go) covers the installation's ordinary steps, and the repository
// the run would onboard is not one the config lists, so its identity was never
// printed at the review — a person who typed the project's name there has
// authorised no repository. D12's other three call sites read the same
// conditions; this gate is not the exception.
func (e *initEngine) onboardCovered(o originInfo) bool {
	r := e.r
	if o.Rewritten || !o.standardHost() {
		// The origin's identity is not the one it claims: git config in the
		// checkout rewrites it, or its host is not a provider's own. The
		// typed confirmation — which prints the host and asks for it — is the
		// only route there, --yes and the review included.
		return false
	}
	if repoNameListed(e.lc, o) {
		// The local config knows this name at another host: the origin
		// contradicts a repository this project does have, which is the
		// gate's own question rather than a step of the converge's.
		return false
	}
	yes := r.conditions()
	yes.Terminal, yes.NonInteractive, yes.JSON = false, false, false
	return canConfirmTyped(initflow.YesOrTyped, yes)
}

// checkoutNote is printed once before the gate when installation-only flags
// are given in a checkout that the gate would ask about.
const checkoutNote = "this checkout's repository is not onboarded; the converge in a checkout also onboards its repository. To set up only the installation, run fugaro setup from outside the checkout."

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
func (e *initEngine) gateEarly(ctx context.Context) {
	if e.repo == nil {
		return
	}
	e.repo.resolve(ctx)
	tg := e.repo.tg
	if tg == nil {
		return
	}
	if a, err := e.authState(tg.origin); err != nil || a != authAsk {
		return
	}
	if len(e.r.o.installationFlags()) > 0 {
		fmt.Fprintln(e.r.w, "note: "+checkoutNote)
	}
	if ok, err := e.confirmRepo(tg.root, tg.origin); err == nil && !ok {
		if e.declined == nil {
			e.declined = map[string]bool{}
		}
		e.declined[strings.ToLower(tg.origin.typed())] = true
	}
}

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

// unknownDetail says why the stage waits for the user. optIn is the clause
// naming how this run's reader can opt the repository in, and it is the
// caller's because it is not the same sentence in every run: the converge's
// --yes covers the step for an origin whose identity is unambiguous
// (convergeOptIn), the standalone --repo gate's question IS that identity
// (repoGateOptIn), and a coding agent's session covers nothing. Taking it as
// an argument is what keeps the sentence from naming a route this run does not
// have (W5.1).
func unknownDetail(project string, o originInfo, optIn string) string {
	d := "the repository " + pluginwire.Printable(o.typed()) + " is not one of project " + pluginwire.Printable(project) + "'s yet"
	switch {
	case o.Rewritten:
		d = "the origin URL is rewritten by git config in this checkout (it says " + pluginwire.Printable(o.Raw) + ", git resolves it to " + pluginwire.Printable(o.Effective) + "), so " + d
	case !o.standardHost():
		d += " (host " + pluginwire.Printable(o.Host) + " is not a provider's own)"
	}
	return d + "; onboarding it creates cloud resources, so it needs your own opt-in" + optIn + "; the repository stage was not reached"
}

// convergeOptIn is unknownDetail's clause for a stage of a converge, where the
// run's own confirmation can cover the step (D12, 2026-10-20): --yes covers
// onboarding an unlisted repository in either front door, so the sentence names
// it as the way in. It does not where --yes cannot: an origin whose identity is
// not the one it claims (a rewritten URL, a host that is not a provider's own)
// takes the typed confirmation whatever the run was given, and a coding agent's
// session confirms nothing at all (initflow.CanConfirmManaged) — where the flag
// must not be named as a way in, since it would be a line for the agent to
// paste (AgentAdvice). A door that does not enrol has its own sentence
// (unknownDetailNotEnrolled), so it never reaches this clause.
func convergeOptIn(o originInfo) string {
	switch {
	case o.Rewritten || !o.standardHost():
		return ", and --yes does not give it for an origin whose identity is not the one it claims: type its owner/name at your own terminal"
	case agentEnv(os.Getenv):
		return " (a coding agent's session confirms nothing, --yes included): type its owner/name in your own terminal window"
	}
	return " — --yes covers it, or type its owner/name at your own terminal"
}

// repoGateOptIn is unknownDetail's clause for the standalone --repo gate: there
// the question is the identity of the repository in a checkout the run was
// pointed at, which is the one thing --yes cannot answer, so the sentence says
// so and the flag is not offered as a way in (gateRepo's own line names the
// typed route).
const repoGateOptIn = ", which --yes does not give here: this gate's question is the identity of the repository in the checkout it was pointed at"

// enrolLeft is what a door that does not enrol a repository by asking leaves
// instead of the typed question: one line naming the door that does. `update`
// re-converges what is enrolled (D5) and never asks for a repository the local
// config does not list — no typed owner/name, no --onboard-repo — but that is
// its asking, not its covering: `--yes` covers the step in its converge as it
// does in setup's (D12's answer of 2026-10-20, onboardCovered), so this line is
// what a run that did not give that confirmation leaves. It is a command, not a
// prompt, because there is nothing for this run to type.
func enrolLeft(door string) initflow.Left {
	return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand,
		Text: selfCommand() + " " + setupDoor.name, Commands: []string{selfCommand() + " " + setupDoor.name}}
}

// unknownDetailNotEnrolled says why a door that does not enrol a repository by
// asking leaves it alone: the same fact unknownDetail states, plus the door
// that asks. It names `fugaro setup` because that is the one line the reader
// acts on, and it says "by asking" rather than "never" because the door it
// speaks for does cover the step under --yes (D12, onboardCovered) — what it
// does not do is put the question, which is the one thing this run cannot
// answer (W5.1).
func unknownDetailNotEnrolled(project string, o originInfo, door string) string {
	return "the repository " + pluginwire.Printable(o.typed()) + " is not one of project " + pluginwire.Printable(project) +
		"'s yet, and " + selfCommand() + " " + door + " does not enrol one by asking: " + selfCommand() + " " + setupDoor.name + " does, in the checkout (it asks whether to onboard it)"
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
	// unknownDetail states the reason and the clause this gate's --yes cannot
	// answer (repoGateOptIn); `how` is this gate's own line, so the sentence
	// names the way in once and the two cannot contradict each other.
	return userErr("%s: %s", unknownDetail(lc.Name, oi, repoGateOptIn), how)
}
