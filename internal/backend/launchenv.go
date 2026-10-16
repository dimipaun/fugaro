package backend

import (
	"fmt"
	"strings"
)

// EnvVar is one NAME=value entry of a launch's own environment, parsed.
type EnvVar struct{ Name, Value string }

// JobIdentityEnv are the variables a job's own environment carries about
// itself, the same six on both backends: which execution it is (FUGARO_RUN),
// which backend it is on, which Fugaro project and GCP project it is in, which
// region it runs in, and where its bucket is (§2.1, §2.5). Batch writes them
// into the job's variable map; Cloud Run's deploy writes them into the job's
// environment, and the launch override adds FUGARO_RUN. A launcher's entry
// naming one of them is refused here, in the seam, rather than by each backend
// separately: on either platform the entry is merged into the very
// environment the run reads its own identity and configuration from, so the
// two backends must answer the same thing about it.
//
// The names are the platform's, and the packages that write them keep their
// own constants (internal/infra's BucketEnv, BackendEnv, GCPProjectEnv,
// ProjectEnv, RegionEnv; internal/backend's own FUGARO_RUN and
// internal/runner's readers). internal/infra's
// TestTheJobIdentityNamesAreTheSeams is what keeps the two spellings from
// drifting apart.
var JobIdentityEnv = []string{
	"FUGARO_RUN", "FUGARO_BACKEND", "FUGARO_BUCKET",
	"FUGARO_GCP_PROJECT", "FUGARO_PROJECT", "FUGARO_REGION",
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
// identity from.
//
// The two refusals:
//
//   - an entry that is not NAME=value. An entry with no "=" or with an empty
//     name is a launcher's bug, and a silently dropped variable is a run that
//     behaves as if the launcher had not set it at all — the failure the
//     budget marker exists to prevent.
//   - an entry naming a variable in reserved, or one of JobIdentityEnv: the
//     job's own environment already sets it, and that is what the run believes
//     about itself. Both fields are one env on the platform, so which of the
//     two values wins is the platform's to decide; a run whose FUGARO_RUN came
//     from a launch override would be a run the launcher could not find again.
//     JobIdentityEnv is refused for every backend here; reserved is the rest
//     of one backend's own map (Batch's job variables beyond the identity
//     six, its DOCKER_HOST), which the other backend has no counterpart for.
func LaunchEnv(spec LaunchSpec, reserved ...string) ([]EnvVar, error) {
	if len(spec.Env) == 0 {
		return nil, nil
	}
	own := make(map[string]bool, len(reserved)+len(JobIdentityEnv))
	for _, name := range reserved {
		own[name] = true
	}
	for _, name := range JobIdentityEnv {
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
