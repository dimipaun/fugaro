package batch

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
)

// jobVariables is the task's environment variables: the run's own identity,
// the VM's Docker socket when the run asked for a daemon, and the launch's own
// entries.
//
// A Batch task's variables are the run's whole environment -- something Cloud
// Run's container override is merged into rather than the thing itself -- so
// the map is what makes its own entries reserved: a later write to a map key
// is what a run would believe about itself, so an entry naming one of them is
// refused rather than applied (backend.LaunchEnv, asked with every key this
// job already sets). The platform's own five join those keys
// (backend.ReservedEnvNames), the same list Cloud Run hands the same function:
// four of them are map keys here and FUGARO_BUCKET is not -- no Batch job
// writes the runs bucket yet -- so without the shared list an entry naming
// FUGARO_BUCKET would be the one way a launcher could choose which bucket a
// run writes to on this backend while Cloud Run refused it (the review finding
// on this change). Reserving a name the map does not hold costs a launcher
// nothing it was allowed to set: the bucket is the installation's
// configuration either way (backend.PlatformEnvNames). The catalog stamp joins
// them although no Batch job writes it either: this backend applies every
// non-secret entry into the task's variables, which are the run's environment,
// so an entry naming it would tell a run its image came from the operator's
// catalog when it did not -- and the runner reads exactly that name to decide
// whether the image's own variables reach the agent (docs/image-env, D1371).
// Reserving it is what keeps this backend's set the one Cloud Run asks for
// (backend.ImageEntryEnv).
//
// socket is the VM's Docker socket as the container sees it, "" when the run
// asked for no daemon: where the socket is mounted and which group the
// container joins are the container's own recipe (jobDefinition), while the
// path a run's docker commands read is this environment's.
//
// **This is the helper BR8 owns** (F8, R6): that task fills the map in with
// every variable the deployed job this run stands in for carries -- the runs
// bucket, the budget backend, the credentials service's URL, the price tables,
// the caps and the rest -- built from the same values internal/infra builds
// that job's template from, plus backend.DeployedJobEnv, the name of the
// deployed job this run stands in for. What it must keep is the reservation
// above: each of those names is one a launch entry may not set.
func (b *Backend) jobVariables(spec backend.LaunchSpec, full, socket string) (map[string]string, error) {
	vars := map[string]string{
		// The run's own record, which the backend reports back as
		// Execution.Run (a hint: anyone who can create the job can set it).
		"FUGARO_RUN": spec.Repo.Slug + "/" + spec.RunID,
		// The launcher's record of which backend it chose, which launch.json
		// and the prices path read (design §2.5).
		"FUGARO_BACKEND": backend.CloudBatch,
		// Batch injects BATCH_TASK_INDEX/BATCH_TASK_COUNT but nothing that
		// names the job, so the launcher writes the job's own name in: the
		// codec's FromEnv reads it back (design §2.5). Launcher-forgeable,
		// like FUGARO_RUN.
		backend.BatchJobEnv:  full,
		"FUGARO_PROJECT":     b.o.FugaroProject,
		"FUGARO_GCP_PROJECT": b.o.GCPProject,
		"FUGARO_REGION":      b.o.Region,
	}
	if socket != "" {
		// The agent's own docker commands read this rather than a compiled-in
		// default path (design §2.2, §4.3); it is allowlisted on the agent's
		// side only for a run that has a socket.
		vars["DOCKER_HOST"] = "unix://" + socket
	}
	reserved := make([]string, 0, len(vars)+len(backend.PlatformEnvNames)+1)
	for name := range vars {
		reserved = append(reserved, name)
	}
	reserved = append(reserved, backend.ImageEntryEnv)
	reserved = backend.ReservedEnvNames(reserved...)
	extra, err := backend.LaunchEnv(spec, reserved...)
	if err != nil {
		return nil, fmt.Errorf("launching %s: %w", full, err)
	}
	// A plain entry is the job's: this backend applies it into the task's
	// variables, which are the run's environment. A secret one is the seal's,
	// and the helper below is where it is refused today and wrapped later on
	// (seal.go, BR1).
	var sealed []backend.EnvVar
	for _, kv := range extra {
		if kv.Secret {
			sealed = append(sealed, kv)
			continue
		}
		vars[kv.Name] = kv.Value
	}
	wrapped, err := b.sealedVars(full, sealed)
	if err != nil {
		return nil, err
	}
	for name, value := range wrapped {
		vars[name] = value
	}
	return vars, nil
}
