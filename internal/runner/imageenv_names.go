package runner

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
)

// SecretEnvNames returns the names a job's FUGARO_SECRET_ENVS declares, in the
// order it lists them, without the blank entries a trailing or doubled comma
// leaves. It is the job's own list — every secret the job's workflow mounts,
// which is the set addMountedSecrets registers for redaction before anything
// runs — and not the subset one run's task declares, which only the run knows.
func SecretEnvNames(jobEnv map[string]string) []string {
	var names []string
	for _, name := range strings.Split(jobEnv[SecretEnvsVar], ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ImageEnvDroppedNames returns, sorted, the names a run drops: the names in a
// job's own environment that reach no agent. The names that do reach it are
// jobEnv's own names minus this set, which is why the subtraction is spelled
// once, here, where the passthrough and the report that describes it both call
// it (D1378): a name read as dropped by one is a name dropped by the other.
//
// Five sources, and nothing else:
//
//   - config.ReservedEnv, case-insensitively, which is also every name Fugaro
//     writes into a job: the FUGARO_ family (the stamp, the platform's five,
//     the budget variables, FUGARO_CHECKOUT and FUGARO_REMOTE), the launcher's
//     FUGARO_RUN, FUGARO_BUDGET_TOKEN, FUGARO_CREDENTIALS_SERVICE_URL and
//     FUGARO_REVIEW_JOB, Cloud Run's CLOUD_RUN_* names, and the names that
//     change how the runner, a shell, git or a runtime behaves (PATH, HOME,
//     LD_*, JAVA_TOOL_OPTIONS, GOOGLE_APPLICATION_CREDENTIALS, the proxies,
//     the CA bundles). Reusing the list a workflow secret's env is held to is
//     the whole point (D1372): an image sets, shadows or redirects none of
//     them, and the value the run already had stands.
//   - config.EnvNameRE's shape, the filter's other half (D1372): a name no
//     shell can express — LD-PRELOAD, one with a space, a lower-case one — is
//     dropped rather than passed on as an environment entry, and it is the
//     same spelling the workflow-secret rule uses, so the two cannot drift.
//   - backend.PlatformEnvNames, the five internal/infra's platformEnv writes
//     into every job and a test pins it to. Each is FUGARO_-prefixed and so
//     refused by the list above already; naming them here states the intent
//     rather than adding a rule.
//   - ImageEntryEnv, the stamp itself, so the agent never sees which entry its
//     image came from any more than it sees anything else FUGARO_ names.
//   - the names the job's FUGARO_SECRET_ENVS declares that declared does not
//     hold (F8). A job mounts every secret of its workflow, so a name the run
//     did not declare must not arrive through an image's variable: it reaches
//     the agent through the declared-secret path alone, or not at all. A job of
//     a switched repository mounts none of them, which is why a reader with a
//     run in hand uses imageEnvDroppedNamesForRun below: it applies this same
//     rule to the names the run fetched in their place.
//
// declared is therefore the names whose secret this reader's run may read:
// the run's own secretEnvs (internal/runner's bootstrap, built from the task's
// workflow before the agent's environment is) — an empty one for a review run,
// whose agent gets none of the workflow's secrets, so every mounted name is
// dropped. It is not r.secretList(), which is the values the redactor holds,
// not the set of names a run declares. A reader with no run in hand — the
// report a job's line is printed from — passes SecretEnvNames(jobEnv), the
// job's own list, because a name the job itself declares does reach runs.
//
// Whether a job carries the stamp at all is the caller's check: a job with no
// ImageEntryEnv passes nothing, whatever this returns (D1371's fail-closed),
// and the stamp's absence is a different fact from an empty result.
func ImageEnvDroppedNames(jobEnv map[string]string, declared []string) []string {
	jobSecrets := SecretEnvNames(jobEnv)
	var dropped []string
	for name := range jobEnv {
		if imageEnvDropped(name, jobSecrets, declared) {
			dropped = append(dropped, name)
		}
	}
	slices.Sort(dropped)
	return dropped
}

// imageEnvDroppedNamesForRun is ImageEnvDroppedNames plus the names held: the
// names this run's own workflow declares a secret for, whether its job mounts
// the value or the run fetched it (workflowsecrets.go, B3.4).
//
// A job's FUGARO_SECRET_ENVS is not that set once a repository is switched onto
// the credentials service: the flip leaves the workflow's secrets unmounted, so
// the job stops naming them and the fetch puts their values in the run's
// environment instead. A name held that way that declared does not carry is
// dropped on exactly the terms source 5 of ImageEnvDroppedNames states for a
// mounted one -- otherwise a value the flip moved would reach a review run's
// agent through the image arm, which is the one thing that arm may not do (G12,
// F8).
//
// Only a name the job's own environment holds is added: the arm this feeds
// iterates that environment, so a name outside it cannot travel this way.
//
// The report a job's line is printed from keeps calling ImageEnvDroppedNames:
// it measures a job's own environment, where no run and so no fetch is in hand.
func imageEnvDroppedNamesForRun(jobEnv map[string]string, declared, held []string) []string {
	dropped := ImageEnvDroppedNames(jobEnv, declared)
	for _, name := range held {
		_, inJob := jobEnv[name]
		if inJob && !slices.Contains(declared, name) && !slices.Contains(dropped, name) {
			dropped = append(dropped, name)
		}
	}
	slices.Sort(dropped)
	return dropped
}

// imageEnvDropped is the one rule, over one name: the names ImageEntryEnv and
// config.ReservedEnv refuse, a name outside config.EnvNameRE's shape, the
// platform's own, and a mounted secret the reader's run did not declare.
func imageEnvDropped(name string, jobSecrets, declared []string) bool {
	if name == ImageEntryEnv || config.ReservedEnv(name) || !config.EnvNameRE.MatchString(name) ||
		slices.Contains(backend.PlatformEnvNames, name) {
		return true
	}
	return slices.Contains(jobSecrets, name) && !slices.Contains(declared, name)
}
