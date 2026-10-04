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

// authState says where repo stands. A --onboard-repo that is not this
// checkout's repository is refused whatever else is true.
func (e *initEngine) authState(repo string) (repoAuth, error) {
	o := e.r.o
	if o.onboardRepo != "" && !sameRepo(o.onboardRepo, repo) {
		return authNeeded, userErr("--onboard-repo %s is not this checkout's repository (its origin is %s)", pluginwire.Printable(o.onboardRepo), pluginwire.Printable(repo))
	}
	switch {
	case repoKnown(e.lc, repo), o.onboardRepo != "", e.authorized[strings.ToLower(repo)]:
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

// onboardLeft is the one line that opts a repository in.
func onboardLeft(repo string) initflow.Left {
	return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: selfCommand() + " init --onboard-repo " + quoteWord(repo)}
}

// unknownDetail says why the stage waits for the user.
func unknownDetail(project, repo string) string {
	return "the repository " + pluginwire.Printable(repo) + " is not one of project " + pluginwire.Printable(project) + "'s yet; onboarding it creates cloud resources, so it needs your own opt-in (not covered by --yes); the repository stage was not reached"
}

// confirmRepo asks, in the terminal, for the repository's owner/name typed:
// the one answer that opts it in. Anything else leaves it out.
func (e *initEngine) confirmRepo(root, repo string) (bool, error) {
	r := e.r
	fmt.Fprintf(r.w, "⚠ CONFIRM (project %s, GCP project %s): this checkout's repository is not one of the project's yet.\n", pluginwire.Printable(r.projectName), pluginwire.Printable(r.gcpProject))
	fmt.Fprintf(r.w, "  repository: %s (the origin)\n  checkout:   %q\n", pluginwire.Printable(repo), root)
	fmt.Fprintln(r.w, "  Wiring and onboarding it writes to the checkout and creates cloud resources in this project, and the repository's fugaro.yaml can start a Cloud Build. Do it only for a repository you trust and mean to onboard.")
	fmt.Fprintf(r.w, "Type %s to onboard it: ", pluginwire.Printable(repo))
	line, _ := r.in.ReadString('\n') // an ended input is a no
	if strings.TrimSpace(line) != repo {
		return false, nil
	}
	if e.authorized == nil {
		e.authorized = map[string]bool{}
	}
	e.authorized[strings.ToLower(repo)] = true
	return true, nil
}
