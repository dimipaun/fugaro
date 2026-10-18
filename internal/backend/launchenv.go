package backend

import (
	"fmt"
	"strings"
)

// EnvVar is one NAME=value entry of a launch's own environment, parsed.
// Secret is true for an entry from LaunchSpec.SecretEnv: its value is a secret
// a backend carries only where the design accepts the audience, or refuses.
type EnvVar struct {
	Name, Value string
	Secret      bool
}

// PlatformEnvNames are the names every deployed job's own environment carries
// the installation's identity and configuration under: the runs bucket, which
// compute backend the run is on, the installation's project name, the GCP
// project ID and the region (design §2.1, §2.5). internal/infra's platformEnv
// is where those five are written into every job (and the check job), so a
// launch lands in an environment that already holds them on either backend —
// Cloud Run's container override is merged into the job's environment, and
// Batch's task variables are that environment (design §2.1). A backend
// therefore reserves them with the rest of what its environment holds, which
// is the rule LaunchEnv's own doc states: an entry naming one of them would
// replace a value the run reads its own configuration from.
//
// The list is pinned to platformEnv by internal/infra's
// TestPlatformEnvIsWhatTheBackendsReserve, so a platform variable added there
// without being added here is a failing test rather than an environment a
// launch can silently rewrite. Not to be written to.
var PlatformEnvNames = []string{"FUGARO_BUCKET", "FUGARO_BACKEND", "FUGARO_GCP_PROJECT", "FUGARO_PROJECT", "FUGARO_REGION"}

// ReservedEnvNames is the reserved list both backends hand LaunchEnv: the
// names a backend's own job definition writes (own) and the platform's five
// (PlatformEnvNames). It is one function rather than two lists because the
// two backends must refuse the same launch entries — a launch that lands on
// Cloud Run and one that lands on Batch differ in nothing the launcher's
// entry is allowed to name — and because they did not: Cloud Run reserved the
// platform's five and Batch reserved the keys of its own variables map, which
// holds four of them and no FUGARO_BUCKET (no Batch job writes the runs
// bucket; Batch's launcher wiring is not built yet), so an entry naming
// FUGARO_BUCKET was refused on one backend and applied on the other (the
// review finding on this change).
//
// A backend whose environment does not hold a name here still reserves it:
// on Batch the map is the whole of the task's environment, so the entry would
// not be applied *over* the platform's bucket but would be the only writer of
// it, which is the same thing the reservation refuses elsewhere — a launch
// telling a run which bucket to write its results to and to read the
// project's policy from.
func ReservedEnvNames(own ...string) []string {
	out := make([]string, 0, len(own)+len(PlatformEnvNames))
	out = append(out, own...)
	return append(out, PlatformEnvNames...)
}

// LaunchEnv parses LaunchSpec.Env and then LaunchSpec.SecretEnv — the
// variables a launcher adds for one execution only — into entries, in the
// order they were given (SecretEnv's marked Secret), and refuses the three an
// entry must not be. Both backends build the run's environment through here,
// so what a launcher hands the seam is either carried by every backend or
// refused by every backend — except a Secret entry, which a backend must also
// refuse when the only place it could carry it is readable by a wider
// audience than the execution's override (Batch's job definition). The
// launcher's one entry today is the budget marker (internal/cli/run.go,
// internal/runner's BudgetTokenEnv), a SecretEnv entry once the launcher seals
// (its value is the seal key, D15), and a
// backend that dropped it would leave the run unable to tell a minted token
// from a project that published no policy at all (plan R2-8(a)). A backend
// that carries an entry into a map of its own (Batch's job variables) must
// also name everything that map already holds in reserved, so the entry is
// refused there rather than silently replacing the value the run reads its own
// identity from.
//
// The three refusals, none of which quotes a secret entry's value:
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
//   - a name given twice, in either list or across both: which value the
//     platform keeps is not the launcher's to leave to chance.
func LaunchEnv(spec LaunchSpec, reserved ...string) ([]EnvVar, error) {
	if len(spec.Env) == 0 && len(spec.SecretEnv) == 0 {
		return nil, nil
	}
	own := make(map[string]bool, len(reserved))
	for _, name := range reserved {
		own[name] = true
	}
	seen := make(map[string]bool, len(spec.Env)+len(spec.SecretEnv))
	out := make([]EnvVar, 0, len(spec.Env)+len(spec.SecretEnv))
	add := func(kv string, secret bool) error {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			// Nothing of a malformed entry is quoted, secret or not: an entry
			// with no "=" may be a name run into its value (a launcher bug such
			// as the marker's name directly followed by the seal key), so the
			// refusal names only the shape it wanted.
			kind := "a launch env entry"
			if secret {
				kind = "a secret launch env entry"
			}
			return fmt.Errorf("%s is not NAME=value (the entry is not quoted: it may carry a value)", kind)
		}
		if own[name] {
			return fmt.Errorf("launch env %s: the job's own environment already sets it, and that value is what the run reads its own identity and configuration from (design §2.1, §2.5): refusing rather than leaving which of the two the platform hands the run to chance", name)
		}
		if seen[name] {
			return fmt.Errorf("launch env %s: the launch names it twice, and which value the platform hands the run would be left to chance", name)
		}
		seen[name] = true
		out = append(out, EnvVar{Name: name, Value: value, Secret: secret})
		return nil
	}
	for _, kv := range spec.Env {
		if err := add(kv, false); err != nil {
			return nil, err
		}
	}
	for _, kv := range spec.SecretEnv {
		if err := add(kv, true); err != nil {
			return nil, err
		}
	}
	return out, nil
}
