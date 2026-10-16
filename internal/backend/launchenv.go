package backend

import (
	"fmt"
	"strings"
)

// EnvVar is one NAME=value entry of a launch's own environment, parsed.
type EnvVar struct{ Name, Value string }

// JobEnvNames are the variables a job's own environment already sets, by name:
// the run's own record (FUGARO_RUN, which Cloud Run carries as the launch
// override this file judges and Batch writes as a job variable), where the
// installation is and which backend it is on (FUGARO_BUCKET, FUGARO_BACKEND,
// FUGARO_GCP_PROJECT, FUGARO_PROJECT, FUGARO_REGION — the five
// internal/infra's platformEnv writes into every job and its PlatformEnv names,
// which TestJobEnvNamesCoverPlatformEnv pins this list to), and the three a
// backend adds for itself (Batch's own FUGARO_BATCH_JOB, the DOCKER_HOST of its
// socket recipe, and the Vertex placement a Vertex Cloud Run job carries,
// CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID).
//
// Both backends ask LaunchEnv with this list, so what a launcher must not put
// in LaunchSpec.Env is one rule rather than a per-backend accident: Cloud Run
// merges the override into the job's environment and Batch writes the map its
// task starts with, so an entry naming a variable on this list would shadow the
// value the run reads its own identity and placement from on either of them —
// and the launcher's own walk of one backend could not see the other's. The
// list is a superset of what either backend sets alone on purpose: a name the
// platform owns is refused everywhere, so which backend a run happens to be on
// is not what decides whether a launch may rewrite its identity (the review of
// this change: Cloud Run reserved FUGARO_RUN alone, so an entry Batch refused
// was silently accepted, and applied as an override, there).
var JobEnvNames = []string{
	"FUGARO_RUN",
	"FUGARO_BUCKET",
	"FUGARO_BACKEND",
	"FUGARO_GCP_PROJECT",
	"FUGARO_PROJECT",
	"FUGARO_REGION",
	"FUGARO_BATCH_JOB",
	"DOCKER_HOST",
	"CLOUD_ML_REGION",
	"ANTHROPIC_VERTEX_PROJECT_ID",
}

// LaunchEnv parses LaunchSpec.Env — the variables a launcher adds for one
// execution only — into entries, in the order they were given, and refuses
// the two an entry must not be. Both backends build the run's environment
// through here, so what a launcher hands the seam is either carried by every
// backend or refused by every backend: Env's one producer today is the budget
// marker (internal/cli/run.go, internal/runner's BudgetTokenEnv), and a
// backend that dropped it would leave the run unable to tell a minted token
// from a project that published no policy at all (plan R2-8(a)). A backend
// that carries an entry into a map of its own (Batch's job variables) must
// also name everything that map already holds in reserved, so the entry is
// refused there rather than silently replacing the value the run reads its own
// identity from; JobEnvNames is the set the names the platform itself owns are
// shared through, so both backends refuse them rather than one.
//
// The two refusals:
//
//   - an entry that is not NAME=value. An entry with no "=" or with an empty
//     name is a launcher's bug, and a silently dropped variable is a run that
//     behaves as if the launcher had not set it at all — the failure the
//     budget marker exists to prevent.
//   - an entry naming a variable in reserved: the job's own environment
//     already sets it — the run's identity (FUGARO_RUN), which backend it is
//     on, which project it is in and where its bucket is (§2.1, §2.5) — and
//     that is what the run believes about itself. Both fields are one env on
//     the platform, so which of the two values wins is the platform's to
//     decide; a run whose FUGARO_RUN came from a launch override would be a
//     run the launcher could not find again.
func LaunchEnv(spec LaunchSpec, reserved ...string) ([]EnvVar, error) {
	if len(spec.Env) == 0 {
		return nil, nil
	}
	own := make(map[string]bool, len(reserved))
	for _, name := range reserved {
		own[name] = true
	}
	out := make([]EnvVar, 0, len(spec.Env))
	for _, kv := range spec.Env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("launch env %q: it must be NAME=value", kv)
		}
		if own[name] {
			return nil, fmt.Errorf("launch env %s: the job's own environment already sets it, and that value is what the run reads its own identity and configuration from (design §2.1, §2.5): refusing rather than leaving which of the two the platform hands the run to chance", name)
		}
		out = append(out, EnvVar{Name: name, Value: value})
	}
	return out, nil
}
