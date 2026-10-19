package backend

import (
	"fmt"
	"strings"
)

// BatchJobEnv is the job env variable Launch writes the Batch job's own
// resource name into (design compute-backend.md §2.5). Batch injects
// BATCH_TASK_INDEX and BATCH_TASK_COUNT into every task but nothing that
// names the job, so the launcher supplies the name it chose. It is
// launcher-forgeable, exactly like FUGARO_RUN, so what the codec reads back
// is a hint to record and to compare -- never evidence to act on.
const BatchJobEnv = "FUGARO_BATCH_JOB"

// BatchCodec is Cloud Batch's name codec: on Batch one job is one run, so a
// run's identity is the job itself -- projects/<p>/locations/<r>/jobs/<j>,
// six fields with no execution in it (design compute-backend.md §2.5) -- and
// the platform's own signal is BATCH_TASK_INDEX/BATCH_TASK_COUNT, which
// Batch injects into every task and nowhere else.
func BatchCodec() Codec { return batchCodec{} }

type batchCodec struct{}

func (batchCodec) Backend() string { return CloudBatch }

// Parse parses projects/<p>/locations/<r>/jobs/<j>. The seam's Name slot
// stays empty: the job is the run, and its id is the whole identity.
func (batchCodec) Parse(name string) (ExecID, bool) {
	f := strings.Split(name, "/")
	if len(f) != 6 || f[0] != "projects" || f[2] != "locations" || f[4] != "jobs" {
		return ExecID{}, false
	}
	for _, v := range []string{f[1], f[3], f[5]} {
		if v == "" {
			return ExecID{}, false
		}
	}
	return ExecID{GCPProject: f[1], Region: f[3], Job: f[5]}, true
}

// Format is the canonical full resource name: the job's, since the job is
// the run. It is not ExecID.String, which spells Cloud Run's eight fields.
func (batchCodec) Format(id ExecID) string {
	return "projects/" + id.GCPProject + "/locations/" + id.Region + "/jobs/" + id.Job
}

// FromEnv is the canonical name of the Batch job this process is, or ""
// outside a Batch task. The name comes from the job definition
// (BatchJobEnv), and it is checked against the two other names the same
// definition carries: FUGARO_REGION, which the job definition also writes,
// and FUGARO_GCP_PROJECT, which must be the project the name is in. Neither
// is evidence -- the launcher writes all three, and whoever can create a job
// can write any of them -- but a job whose environment was edited must not be
// able to name another region's or another project's job.
func (c batchCodec) FromEnv(getenv func(string) string) (string, error) {
	if !c.On(getenv) {
		return "", nil
	}
	name := getenv(BatchJobEnv)
	if name == "" {
		return "", fmt.Errorf("on Batch the job must set %s (its own resource name), FUGARO_REGION and FUGARO_GCP_PROJECT", BatchJobEnv)
	}
	id, err := c.ParseInRegion(name, getenv("FUGARO_REGION"))
	if err != nil {
		return "", err
	}
	// The name carries the project, so a mismatch is a job environment that
	// names a job it is not. An empty FUGARO_GCP_PROJECT mismatches too, and
	// the CLI's own job-env check refuses that case anyway
	// (internal/cli/exec.go, refuseOldJobEnv).
	if got, want := id.GCPProject, getenv("FUGARO_GCP_PROJECT"); got != want {
		return "", fmt.Errorf("job %s is in project %s, not in the job environment's FUGARO_GCP_PROJECT (%q)", name, got, want)
	}
	return c.Format(id), nil
}

// On reports whether this process is one of Batch's tasks. Batch injects
// both BATCH_TASK_INDEX and BATCH_TASK_COUNT into every task (design §2.5),
// so requiring both is what keeps a stray variable from claiming a process.
//
// Neither name is a Fugaro variable, which is not the same as being
// unforgeable: whether a job's own environment can override the signal Batch
// injects, and so whether whoever can submit a job can forge this answer, is
// exactly the open question Check 57a step 12 settles (design §2.5, §9). This
// build has not measured it, so it claims nothing beyond "these two variables
// are set".
func (batchCodec) On(getenv func(string) string) bool {
	return getenv("BATCH_TASK_INDEX") != "" && getenv("BATCH_TASK_COUNT") != ""
}

// ParseInRegion refuses name unless it is a Batch job name in region,
// spelled [a-z0-9-] throughout. Like the Cloud Run codec's, the names it
// sees come from bucket objects a run's own service account can write, so
// without this check a run could point the operator's cancel or logs at any
// job in the project. It binds no job: a caller that knows the run binds it
// by the run's own FUGARO_RUN, not by the job (Codec's ValidateRun).
func (batchCodec) ParseInRegion(name, region string) (ExecID, error) {
	id, ok := BatchCodec().Parse(name)
	if !ok {
		return ExecID{}, fmt.Errorf("%q is not a Batch job name (projects/<p>/locations/<r>/jobs/<j>)", name)
	}
	for _, part := range []string{id.GCPProject, id.Region, id.Job} {
		if !runPartRE.MatchString(part) {
			return ExecID{}, fmt.Errorf("job name %q has a part that is not [a-z0-9-]", name)
		}
	}
	if id.Region != region {
		return ExecID{}, &WrongRegionError{Name: name, Region: id.Region, Want: region}
	}
	return id, nil
}

// ValidateRun is ParseInRegion plus the binding to the run's own job. A
// Batch job id is derived per run -- fugaro-<slug>-<workflow>-<12 hex>
// (design §2.4) -- so the caller passes the id it launched. An empty job is
// refused, not skipped, for the same reason the Cloud Run codec refuses one.
//
// No production caller, on this codec either: the CLI binds a name to a run
// by FUGARO_RUN (Codec's own doc).
func (c batchCodec) ValidateRun(name, region, job string) (ExecID, error) {
	if job == "" {
		return ExecID{}, fmt.Errorf("job %s is not bound to a run: the run's job id is required", name)
	}
	id, err := c.ParseInRegion(name, region)
	if err != nil {
		return ExecID{}, err
	}
	if id.Job != job {
		return ExecID{}, fmt.Errorf("%s is job %s, not the run's job %s", name, id.Job, job)
	}
	return id, nil
}
