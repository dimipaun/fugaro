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
// The *profile* half of the admission is checked beside this file rather than
// here: which profile a launch is admitted to is known only where the job is
// decided, and a launch on a per-repository job is admitted to no profile at
// all (profileAdmissionRefusal, called by project_job.go's
// launchProjectJobFor, the same place the account and OAuth refusals of a
// shared-job launch are made).
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
	// A config that names no database has no policy this launcher can see, and
	// the read below answers "not found" without a call. That is not read as a
	// refusal: nothing is blocked until a policy exists (internal/infra's
	// readiness gate says the same at deploy time — a job whose mode is on and
	// whose environment carries no backend is a note there, and becomes a gate
	// the moment the project has published a policy this installation cannot
	// read), and a mode that is on is not a policy. What refuses a launch is a
	// policy the launcher *can* see and cannot honour: an allow-list that does
	// not admit the repository (below), a node that cannot be read, a version it
	// does not understand, a key the policy lists and this Fugaro does not know.
	//
	// The allow-list is still not the pre-check's to skip: the pre-check is the
	// operator's (--no-budget-check) and skips itself when there is no backend,
	// while this gate runs whatever the mode is.
	p, found, err := env.policy.read(ctx, env.lc)
	var ve *projectpolicy.VersionError
	if errors.As(err, &ve) {
		// This is the one read whose version skew is worded here rather than
		// by policyReadErr: the gate is the owner of the release a policy may
		// name (plan task 1.5 step 4). The rules-version marker is
		// deliberately not part of it: a marker absent or below
		// PolicyRulesVersion is the legacy path (found = false, no policy read,
		// no refusal), and an installation without the policy nodes has no
		// policy for this gate to compare (plan task 1.3 step 1, P3) — while a
		// marker at or above it, this binary's generation included, reads the
		// policy and is compared below.
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
	canonical, err := launchCanonical(provider, repo)
	if err != nil {
		return err
	}
	if projectpolicy.AdmitRepo(p.AllowRepos, canonical) {
		return nil
	}
	return userErr("%s. The launch stops before any execution exists", projectpolicy.NotAdmitted(p.AllowRepos, canonical))
}

// profileAdmissionRefusal is the launcher's twin of the *profile* half of the
// run's admission check: the repository must be admitted to the profile whose
// job this launch runs on, and not only to the project's allow_repos that
// allowListRefusal checks (design §4.1, plan task 3.3 step 4). A profile may
// carry a list narrower than the project's — a secret that must stay away from
// some repositories lives on a profile they are not allowed on, which is what
// a profile's own allow_repos means — and the repository's own fugaro.yaml is
// what selects the profile, so without this half a repository could widen its
// own access by selecting a profile whose list is wider.
//
// It is called where the job is decided (internal/cli's launchProjectJobFor),
// which is the only place the launcher knows the profile: a launch that runs
// on a per-repository job carries none, and is held to the project's
// allow_repos alone, exactly as the run in such a job is. The refusal and the
// fail-closed error are the runner's, worded once by
// projectpolicy.AdmissionRefusal and ProfileFor and shared with
// internal/runner's admissionCheck, so the two sides cannot refuse the same
// repository for different reasons — or admit it on one and not the other.
//
// provider and repo must be the pair this launch is for: the kind derived from
// the repository (the caller's own comparison of it with the profile's), never
// a value chosen to make the check pass.
func profileAdmissionRefusal(p *projectpolicy.Policy, profile, provider, repo string) error {
	canonical, err := launchCanonical(provider, repo)
	if err != nil {
		return err
	}
	refusal, err := p.AdmissionRefusal(profile, canonical)
	if err != nil {
		// ProfileFor's fail-closed error for a profile the policy does not
		// define, which is not a refusal: the caller has already resolved the
		// profile, so this cannot happen here, and it is reported as the halt
		// it is rather than as an unenrolled repository.
		return userErr("this launch would run on the job profile %q, which the published policy cannot be read for: %v", profile, err)
	}
	if refusal == "" {
		return nil
	}
	return userErr("%s. The launch stops before any execution exists", refusal)
}

// launchCanonical is the canonical name of the repository this launch is for:
// projectpolicy.Canonical in the project's own spelling, which is what the
// allow-lists and the profile's admission are matched against. A repository
// that cannot be named at all is refused by the run too, so the launch is
// refused before any execution exists rather than launched at a run that
// stops on it.
func launchCanonical(provider, repo string) (string, error) {
	canonical, err := projectpolicy.Canonical(provider, repo)
	if err != nil {
		return "", userErr("the repository this launch is for cannot be named in the project's allow-list: %v; the run refuses it too, so the launch is refused before any execution exists", err)
	}
	return canonical, nil
}

// policySkewRefusal is the policySince gate's message (plan task 1.5 step 4):
// the sentence the CLI prints for a policy written in a version this Fugaro
// does not read, the release the skew is about, and the remedy for *this
// direction* of the skew, which the gate takes from projectpolicy.SkewRemedy —
// the one place both this package's readers and the runner's word a version
// skew from, so neither side can tell an operator to upgrade a binary that is
// already ahead of the node (the review finding on this change).
// launchPolicyGates refuses a launch with it before the execution is created.
//
// The release it names is the one that introduced the version on the node when
// the node is ahead of this binary, from projectpolicy.VersionSince (through
// SkewSentence, the sentence every too-new reader prints) — the release the
// operator has to reach — and never policySince: that constant names the
// release the policy *model* ships in, and quoting it beside a version-2 node
// told the operator of an 0.8.1 binary to upgrade to >= 0.8.0, which they
// already run (the table's own comment has the whole argument). A version no
// row of the table names came from a release newer than any this binary has
// heard of, and it is answered with the remedy alone rather than with a
// release that is already installed. The other direction, a node *behind* this
// binary, keeps the model's release, which is what wrote a node of a version
// below projectpolicy.Version.
func policySkewRefusal(ve *projectpolicy.VersionError) error {
	if ve.Found > ve.Understood {
		return userErr("%s", projectpolicy.SkewSentence(ve, projectpolicy.ReaderCLI))
	}
	return userErr("%s (the policy model is fugaro %s's): %s",
		fmt.Sprintf(projectpolicy.Skew, ve.Found, ve.Understood), policySince,
		projectpolicy.SkewRemedy(ve, projectpolicy.ReaderCLI))
}
