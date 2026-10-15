package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// The launcher's pre-launch read of the project's policy (plan task 1.5
// step 4). Two things are checked here, both before anything is created:
//
//   - the repository is admitted: the slug this launch stores its run under
//     and the repository the task names must agree, and a published policy
//     must have the repository on its allow_repos;
//   - the policy is not newer than this binary (the policySince gate, plan
//     finding 16/B5).
//
// Both are *advisory* and the docs say so (design §4.2/§4.3): a launcher can
// call jobs.run directly, so the run's own admission check
// (internal/runner's admissionCheck) is the one that counts. What this adds
// is a refusal while the operator is still there to read it, before a job
// exists to fail. For a repository the policy does not admit that refusal is
// the run's own message, from the same policy node, one step earlier
// (projectpolicy.NotAdmitted, which is what both sides word it with); the
// binding and the skew are worded here, because they answer questions only
// this side can see — which slug the launch was about to store the run under,
// and which release this binary is.
//
// The read is the operator's own (policyReadOnce, which this gate shares with
// the launch's mint decision), which is what makes the
// gate the launcher's and not the runner's (R2-8(a)): the runner on a
// credential-less run has no way to read the node at all.

// launchPolicyGates reads the project's policy and refuses a launch the run
// it starts would refuse. Called only on the path that creates an execution
// (finishLaunch's prior == nil block), before the task is stored and before
// the launch claim: a refused launch leaves nothing behind at all — no task,
// no token, no claim and no execution.
func launchPolicyGates(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) error {
	// A launch that cannot read the policy at all refuses here, before the
	// read below answers "not found" for a node it never looked for
	// (missingDatabaseRefusal, launch_budget.go). It is this gate's business
	// and not the budget pre-check's: the pre-check is the operator's to skip
	// (--no-budget-check) and skips itself when there is no backend, while the
	// allow-list is not his to skip — the run is held to it whatever the mode
	// is.
	if err := missingDatabaseRefusal(env.lc); err != nil {
		return err
	}
	p, found, err := env.policy.read(ctx, env.lc)
	var ve *projectpolicy.VersionError
	if errors.As(err, &ve) {
		// This is the one read whose version skew is worded here rather than
		// by policyReadErr: the gate is the owner of the release a policy may
		// name (plan task 1.5 step 4). The rules-version marker is
		// deliberately not part of it: a marker absent or below
		// RulesVersion is the legacy path (found = false, no policy read, no
		// refusal), and an unmarked installation has no policy for this gate
		// to compare (plan task 1.3 step 1, P3).
		return policySkewRefusal(ve)
	}
	if err != nil {
		return policyReadErr(err)
	}
	// The provider kind is derived from the pair the run itself trusts — the
	// slug the run is stored under and the repository the task names — never
	// from the local config's own provider, which is what chose the slug.
	//
	// No launch this CLI builds can reach this refusal: repoSlug computes the
	// slug with task.Slug of a provider validated against config.Providers,
	// and that list is the run's own kinds (runner.ProviderKinds reports the
	// same slice; TestProviderKindsAgreeWithTheConfigsList pins it). The
	// refusal is here for the day the two stop agreeing — a kind one side
	// knows and the other does not is a repository whose slug and whose run
	// would be derived differently — and the test beside this file calls it
	// with the pair a launcher's own jobs.run call would have to build by
	// hand.
	kind, ok := runner.ProviderForSlug(slug, spec.Repo)
	if !ok {
		return userErr("the slug this launch stores its run under (%s) is not the slug of %s under any git provider kind (%s): the run this launch would start refuses it for that reason, so the launch is refused before any execution exists",
			slug, spec.Repo, strings.Join(runner.ProviderKinds(), ", "))
	}
	if !found {
		// No policy published here (no marker, or a marker and no node): there
		// is no allow-list to check, and reading this as an empty one would
		// refuse every launch of an installation that has published nothing.
		return nil
	}
	return allowListRefusal(p, kind, spec.Repo)
}

// allowListRefusal is the launcher's twin of the run's allow-list refusal: the
// same refusal, from the same policy, worded by projectpolicy.NotAdmitted.
// provider must be the kind derived from the slug (launchPolicyGates'), never
// a value this launcher chose.
func allowListRefusal(p *projectpolicy.Policy, provider, repo string) error {
	canonical, err := projectpolicy.Canonical(provider, repo)
	if err != nil {
		return userErr("the repository this launch is for cannot be named in the project's allow-list: %v; the run refuses it too, so the launch is refused before any execution exists", err)
	}
	if projectpolicy.AdmitRepo(p.AllowRepos, canonical) {
		return nil
	}
	return userErr("%s. The launch stops before any execution exists", projectpolicy.NotAdmitted(p.AllowRepos, canonical))
}

// policySkewRefusal is the policySince gate's message (plan task 1.5 step 4):
// the sentence the CLI prints for a policy written in a version this Fugaro
// does not read, the release the policy model ships in quoted beside the
// policy's own version, and the remedy for *this direction* of the skew.
// launchPolicyGates refuses a launch with it before the execution is created.
func policySkewRefusal(ve *projectpolicy.VersionError) error {
	return userErr("%s (the policy model is fugaro %s's): %s",
		fmt.Sprintf(projectpolicy.Skew, ve.Found, ve.Understood), policySince, cliSkewRemedy(ve))
}

// cliSkewSentence is the CLI's refusal of a policy node written in a version
// this Fugaro does not read, remedy included, picked by the direction of the
// skew: this Fugaro is the newer side (CLISkewNewer: the node is behind, and
// the operator re-runs init to bring it up to this release) or the older one
// (CLISkew: the node is ahead, so this binary must be upgraded). Found >
// Understood is the former.
//
// Both callers in this package share it — the launch gate's refusal above and
// launch_budget.go's policyReadErr, which words the same error for the budget
// decision — so an operator reads the same sentence whichever read refused.
func cliSkewSentence(ve *projectpolicy.VersionError) string {
	if ve.Found > ve.Understood {
		return fmt.Sprintf(projectpolicy.CLISkew, ve.Found, ve.Understood)
	}
	return fmt.Sprintf(projectpolicy.CLISkewNewer, ve.Found, ve.Understood)
}

// cliSkewRemedy is cliSkewSentence's remedy alone — everything after the
// shared "Skew; " prefix — for the gate, which prints Skew itself with the
// release the model ships in inside it. Deriving it here rather than wording a
// second remedy keeps the constants the one source of both sentences (what
// each direction may and may not say is pinned by
// TestTheCLISkewRemediesStayApart and TestSkewSentencesSplitByDirection).
func cliSkewRemedy(ve *projectpolicy.VersionError) string {
	skew := fmt.Sprintf(projectpolicy.Skew, ve.Found, ve.Understood)
	return strings.TrimPrefix(cliSkewSentence(ve), skew+"; ")
}
