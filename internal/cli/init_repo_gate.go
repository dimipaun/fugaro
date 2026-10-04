package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
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
	flagOK := opts.onboardRepo != "" && o.standardHost() && !o.Rewritten
	switch {
	case repoKnown(e.lc, o), flagOK, e.authorized[strings.ToLower(o.typed())]:
		return authOK, nil
	case e.canConfirmRepo():
		return authAsk, nil
	}
	return authNeeded, nil
}

// canConfirmRepo: a typed confirmation needs the user's own terminal, as the
// secrets stage does: never with --yes, --non-interactive or --json, nor
// behind a coding agent.
func (e *initEngine) canConfirmRepo() bool {
	return e.r.canAsk() && !agentEnv(os.Getenv)
}

// onboardLeft is what the user does to opt a repository in: the one-line
// flag for an ordinary origin, else a note to use a clean clone.
func onboardLeft(o originInfo) initflow.Left {
	if o.standardHost() && !o.Rewritten {
		return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: selfCommand() + " init --onboard-repo " + quoteWord(o.Repo)}
	}
	return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftConsole, Text: "run fugaro init in your own terminal and type " + pluginwire.Printable(o.typed()) + " at the prompt, or in a fresh clone of the real remote"}
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
