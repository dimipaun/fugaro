package batch

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// credentialsServiceEnv is where a run learns the address of its
// installation's credentials service, a deployed job's name for it and a
// launch entry's: the launcher writes it on a switched repository's execution
// (internal/cli's credentialsServiceLaunchEnv) under the same spelling
// providers.EnvCredentialsServiceURL gives it, which internal/infra's
// JobEnvNames carries and internal/runner reads (its own
// credentialsServiceURLEnv). It is spelled here because this package decides
// what a launch entry may name, and one of those writers is a launch.
const credentialsServiceEnv = "FUGARO_CREDENTIALS_SERVICE_URL"

// jobVariables is the task's environment variables: everything the deployed
// job this run stands in for carries (F8), the run's own identity, the VM's
// Docker socket when the run asked for a daemon, and the launch's own entries.
//
// A Batch task's variables are the run's whole environment -- something Cloud
// Run's container override is merged into rather than the thing itself -- so
// the map is what makes its own entries reserved: a later write to a map key
// is what a run would believe about itself, so an entry naming one of them is
// refused rather than applied (backend.LaunchEnv, asked with every key this
// job already sets, the one exception below apart). The platform's own five
// join those keys (backend.ReservedEnvNames), the same list Cloud Run hands
// the same function, so the two backends refuse one set of launch entries
// rather than each its own. Reserving a name the map does not hold costs a
// launcher nothing it was allowed to set: the bucket is the installation's
// configuration either way (backend.PlatformEnvNames). The catalog stamp joins
// them: this backend
// applies every non-secret entry into the task's variables, which are the
// run's environment, so an entry naming it would tell a run its image came
// from the operator's catalog when it did not -- and the runner reads exactly
// that name to decide whether the image's own variables reach the agent
// (docs/image-env, D1371). Reserving it is what keeps this backend's set the
// one Cloud Run asks for (backend.ImageEntryEnv).
//
// The deployed job's own values arrive as Options.JobEnv (F8, BR8): on Cloud
// Run the run is handed them by the platform, which merges the execution's
// override into the deployed job's environment, while a Batch job is created
// per run from nothing, so the launcher reads the installation's values -- the
// runs bucket, the budget backend, the credentials service's URL, the price
// tables, the caps and the rest -- and passes them here keyed by the name the
// deployed job carries them under (internal/infra's JobEnvNames). They are
// written first, so that the four entries below, which are about this launch's
// own backend and job rather than about the installation, are the ones a run
// reads: the deployed job says cloud-run under FUGARO_BACKEND, and this job
// must say cloud-batch.
//
// socket is the VM's Docker socket as the container sees it, "" when the run
// asked for no daemon: where the socket is mounted and which group the
// container joins are the container's own recipe (jobDefinition), while the
// path a run's docker commands read is this environment's.
func (b *Backend) jobVariables(spec backend.LaunchSpec, full, socket string) (map[string]string, error) {
	// The installation's three, which Options carries as well as JobEnv does:
	// they are what a launch with no deployed-job values at all still needs
	// (Options.JobEnv is empty on an installation whose launcher wiring has not
	// passed them in), and JobEnv's own value wins where the two are both
	// present, because that one is the deployed job's own.
	vars := map[string]string{
		"FUGARO_PROJECT":     b.o.FugaroProject,
		"FUGARO_GCP_PROJECT": b.o.GCPProject,
		"FUGARO_REGION":      b.o.Region,
	}
	for name, value := range b.o.JobEnv {
		vars[name] = value
	}
	// The four the deploy cannot carry, because they are about this launch:
	// the run's own record, which the backend reports back as Execution.Run (a
	// hint: anyone who can create the job can set it); the launcher's record of
	// which backend it chose, which launch.json and the prices path read
	// (design §2.5); the job's own name, which Batch injects nothing for
	// (BATCH_TASK_INDEX/BATCH_TASK_COUNT name no job), so the launcher writes
	// it in and the codec's FromEnv reads it back (design §2.5,
	// backend.BatchJobEnv); and the deployed job this run stands in for, which
	// the runner places the run by and sends to the credentials service (R6,
	// backend.DeployedJobEnv).
	//
	// All four are launcher-forgeable, like FUGARO_RUN.
	vars["FUGARO_RUN"] = spec.Repo.Slug + "/" + spec.RunID
	vars["FUGARO_BACKEND"] = backend.CloudBatch
	vars[backend.BatchJobEnv] = full
	vars[backend.DeployedJobEnv] = deployedJobName(spec)
	if socket != "" {
		// The agent's own docker commands read this rather than a compiled-in
		// default path (design §2.2, §4.3); it is allowlisted on the agent's
		// side only for a run that has a socket.
		vars["DOCKER_HOST"] = "unix://" + socket
	}
	// Every key of the map, with one exception: the credentials service's
	// address, the only name in the deployed job's set that a launch entry is
	// itself the writer of. A switched repository's launch hands its execution
	// that address -- on the shared project job it runs on as on its own
	// (internal/cli's credentialsServiceLaunchEnv) -- and Cloud Run carries the
	// entry, where it lands in the execution's override; refusing it here would
	// refuse a launch on Batch that starts on Cloud Run, which is the one thing
	// this list exists to prevent. Its value is the launch's and wins over the
	// deployed job's (they are applied below). Nothing else a launch writes is
	// a name in this set: FUGARO_SLIP, FUGARO_SERVICES and FUGARO_REVIEW_JOB
	// name no part of a deployed job's environment.
	reserved := make([]string, 0, len(vars)+len(backend.PlatformEnvNames)+1)
	for name := range vars {
		if name == credentialsServiceEnv {
			continue
		}
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

// deployedJobName is the deployed job a Batch run stands in for: the shared
// project job the launch names (LaunchSpec.Job) when the workflow belongs to
// one, and the per-repository workflow job otherwise -- gcp.JobName(slug,
// workflow), the name the launcher computes for a job it deploys
// (internal/infra) and the runner matches against (internal/runner's
// matchJobName). It is the value of backend.DeployedJobEnv (R6).
//
// The launcher computes it and not the backend, exactly as it computes
// LaunchSpec.Job: which job a workflow belongs to is a fact about the
// project's profiles, which this seam does not read.
func deployedJobName(spec backend.LaunchSpec) string {
	if spec.Job != "" {
		return spec.Job
	}
	return gcp.JobName(spec.Repo.Slug, spec.Workflow)
}
