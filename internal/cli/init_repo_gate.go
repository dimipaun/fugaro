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
func onboardLeft(o originInfo) initflow.Left {
	if o.standardHost() && !o.Rewritten && !agentEnv(os.Getenv) {
		return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: selfCommand() + " init --onboard-repo " + quoteWord(o.Repo)}
	}
	return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftConsole, Text: "in your own terminal window, or in a fresh clone of the real remote, type " + pluginwire.Printable(o.typed()) + " at the prompt of", Commands: []string{selfCommand() + " init"}}
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
	return d + "; onboarding it creates cloud resources, so it needs your own opt-in (not covered by --yes); the repository stage was not reached"
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

// gateRepo is the same gate for init --repo run on its own, before its first
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
	how := "in your own terminal window, not through a coding agent or a pipe, run " + selfCommand() + " init --repo and type " + pluginwire.Printable(oi.typed()) + " at the prompt"
	if lf := onboardLeft(oi); lf.Kind == initflow.LeftCommand {
		how = "pass --onboard-repo " + quoteWord(oi.Repo) + ", or " + how
	}
	return userErr("%s; onboarding it creates cloud resources, so it needs your own opt-in (not covered by --yes): %s", unknownDetail(lc.Name, oi), how)
}
