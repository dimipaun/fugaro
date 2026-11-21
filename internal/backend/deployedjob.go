package backend

// DeployedJobEnv is the job environment variable a Batch job carries the name
// of the deployed job its run stands in for under (batch-run-identity R6, F9).
//
// On Cloud Run the platform names the job a run is in (CLOUD_RUN_JOB), and the
// runner places the run -- and so decides which identity it adopts and which
// git credential it asks for -- by that name (internal/runner's matchJobName).
// A Batch job is created per run and named after the run, so nothing on its VM
// names the job that run would have been a run of, and the run ends
// unplaceable. BR8 writes this variable into the job's environment; BR9
// teaches the runner to read it and place the run by it, and to send it to the
// credentials service as the run-job header the service reads for its own
// record -- which decides nothing there: the service verifies the account.
//
// It is launcher-forgeable, exactly like FUGARO_RUN and BatchJobEnv: whoever
// can create the job writes it, so what it is read back into is a claim to
// record and to compare, never evidence to act on.
//
// The value is the deployed job's own name -- gcp.JobName(slug, workflow) for
// a per-repository job, gcp.ProjectJobName(profile, provider, account) for a
// shared project job -- and never a resource path or a run's own name.
const DeployedJobEnv = "FUGARO_DEPLOYED_JOB"
