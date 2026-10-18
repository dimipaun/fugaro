package backend

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Codec is one backend's name codec (design compute-backend.md §2.5, C12):
// how that platform spells a run's identity, and the platform's own signal
// for "this process is one of my runs". The seam carries one per backend
// (Backend.Codec), and every caller asks the codec instead of assuming
// Cloud Run's shape.
//
// Both halves are per backend because the platforms disagree. A Cloud Run
// execution is projects/<p>/locations/<r>/jobs/<j>/executions/<e>; a Batch
// job -- one job, one task, one run -- is projects/<p>/locations/<r>/jobs/<j>,
// six fields with no execution in it, so the two cannot share a parser.
// Cloud Run injects CLOUD_RUN_EXECUTION and a Batch task carries
// BATCH_TASK_INDEX and BATCH_TASK_COUNT, so they cannot share a signal
// either. Neither signal is a FUGARO_* variable: the launcher writes those
// into the job definition, so they record what the launcher chose, never
// where the process actually is.
type Codec interface {
	// Backend is the codec's backend name: CloudRun for a platform's codec,
	// Local for the local invocation's. launch.json's backend field and
	// FUGARO_BACKEND carry a platform's name -- the launcher's record of
	// which backend it chose -- so neither carries Local, which is not a
	// platform.
	Backend() string
	// Parse parses one of this backend's run names into the seam's
	// identity. A caller holding a name but no backend uses
	// ParseExecution, which tries every codec this build knows.
	Parse(name string) (ExecID, bool)
	// Format renders id as this backend's canonical full resource name.
	Format(id ExecID) string
	// FromEnv is the canonical name of the run this process is, or "" when
	// the platform's own signal is absent. An error means the platform
	// says this process is one of its runs but the job did not carry the
	// names the backend needs to spell it.
	FromEnv(getenv func(string) string) (string, error)
	// On reports whether this process is one of this backend's runs, from
	// a signal the platform injects -- never a FUGARO_* variable.
	On(getenv func(string) string) bool
	// ParseInRegion parses name and refuses it unless it is a name of this
	// backend's shape, in region, spelled the way this backend's platform
	// accepts. It binds no job, and under the project-centric model there is
	// no job binding left to add (design §4.1): a caller that knows the run
	// binds the name to it by the run's own FUGARO_RUN
	// (gcp.CheckRunExecution), which is what the CLI's checkExecution
	// applies.
	ParseInRegion(name, region string) (ExecID, error)
	// ValidateRun is ParseInRegion plus the job binding: name must be a run
	// of job. An empty job is refused, not skipped -- an unbound name is
	// what a caller that forgot the run's job would pass, and following it
	// would point the operator's cancel or logs at another job.
	//
	// It has **no production caller** since jobs became shared: an
	// execution's job no longer says which run it is (design §4.1, plan task
	// 3.4 step 2), so checkExecution binds a name by the run's own FUGARO_RUN
	// instead (gcp.CheckRunExecution, internal/backend/backend.go's
	// Execution.Run). A caller that needs to bind a name to a run — Task 8's
	// replacement of checkExecution's Cloud Run call among them — takes this
	// codec's ParseInRegion plus that correlation, and not this method.
	ValidateRun(name, region, job string) (ExecID, error)
}

// Local is the local invocation's backend name: `fugaro exec --task-file`
// has no platform behind it (design compute-backend.md §2.5).
const Local = "local"

// codecs are the name codecs this build knows, in the order their platform
// signals are tried. A Batch codec answers On from BATCH_TASK_INDEX and
// BATCH_TASK_COUNT, which Batch injects into every task, and parses the
// six-field job name (design §2.5). Their name shapes cannot overlap -- six
// fields against eight, with "executions" in the Cloud Run one -- so the
// order only decides which signal is asked first, not which name wins.
func codecs() []Codec { return []Codec{CloudRunCodec(), BatchCodec()} }

// CloudRunCodec is Cloud Run's name codec: the shape this package has
// always spoken, now named, so that the seam can carry a second one.
func CloudRunCodec() Codec { return cloudRunCodec{} }

type cloudRunCodec struct{}

func (cloudRunCodec) Backend() string { return CloudRun }

// Parse parses projects/<p>/locations/<r>/jobs/<j>/executions/<e>.
// <p> may be a project ID or number.
func (cloudRunCodec) Parse(name string) (ExecID, bool) {
	f := strings.Split(name, "/")
	if len(f) != 8 || f[0] != "projects" || f[2] != "locations" || f[4] != "jobs" || f[6] != "executions" {
		return ExecID{}, false
	}
	for _, v := range []string{f[1], f[3], f[5], f[7]} {
		if v == "" {
			return ExecID{}, false
		}
	}
	return ExecID{GCPProject: f[1], Region: f[3], Job: f[5], Name: f[7]}, true
}

// Format is the canonical full resource name. A backend whose identity is
// not the Cloud Run shape spells its own here.
func (cloudRunCodec) Format(id ExecID) string { return id.String() }

// FromEnv is the canonical name of the Cloud Run execution this process
// is, or "" outside Cloud Run. Cloud Run sets CLOUD_RUN_EXECUTION (the
// short name) and CLOUD_RUN_JOB; the job sets FUGARO_GCP_PROJECT (the GCP
// project ID; FUGARO_PROJECT is the Fugaro project's name) and
// FUGARO_REGION.
func (c cloudRunCodec) FromEnv(getenv func(string) string) (string, error) {
	if !c.On(getenv) {
		return "", nil
	}
	id := ExecID{GCPProject: getenv("FUGARO_GCP_PROJECT"), Region: getenv("FUGARO_REGION"), Job: getenv("CLOUD_RUN_JOB"), Name: getenv("CLOUD_RUN_EXECUTION")}
	if id.GCPProject == "" || id.Region == "" || id.Job == "" {
		return "", errors.New("on Cloud Run the job must set FUGARO_GCP_PROJECT and FUGARO_REGION (and Cloud Run sets CLOUD_RUN_JOB)")
	}
	return c.Format(id), nil
}

// On reports whether this process is a Cloud Run job execution: Cloud Run
// sets CLOUD_RUN_EXECUTION there and nowhere else. It is this backend's one
// signal for "am I on you?"; callers ask where they are through Platform,
// not through the job name or a Fugaro variable.
func (cloudRunCodec) On(getenv func(string) string) bool {
	return getenv("CLOUD_RUN_EXECUTION") != ""
}

// runPartRE is what every part of an execution name the backend follows
// must look like: project IDs and numbers, regions, job and execution
// names.
var runPartRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// ParseInRegion refuses name unless it is a Cloud Run execution name in
// region, spelled [a-z0-9-] throughout. The names it sees come from bucket
// objects a run's own service account can write (result.json, launch.json),
// so without this check a run could point the operator's cancel or logs at
// any job in the project. It binds no job: a caller that knows the run
// binds it by the run's own FUGARO_RUN (ValidateRun's job binding is unused,
// see the Codec interface).
func (cloudRunCodec) ParseInRegion(name, region string) (ExecID, error) {
	id, ok := CloudRunCodec().Parse(name)
	if !ok {
		return ExecID{}, fmt.Errorf("%q is not a Cloud Run execution name (projects/<p>/locations/<r>/jobs/<j>/executions/<e>)", name)
	}
	for _, part := range []string{id.GCPProject, id.Region, id.Job, id.Name} {
		if !runPartRE.MatchString(part) {
			return ExecID{}, fmt.Errorf("execution name %q has a part that is not [a-z0-9-]", name)
		}
	}
	if id.Region != region {
		return ExecID{}, &WrongRegionError{Name: name, Region: id.Region, Want: region}
	}
	return id, nil
}

// ValidateRun is ParseInRegion plus the binding to the run's own job. An
// empty job is refused: gcp.JobName never returns one, so it means the
// caller lost the run's job, and a name that is not bound to a job is
// exactly the name this check exists to refuse.
//
// No production caller: the CLI binds a name to a run by FUGARO_RUN now
// (Codec's own doc, gcp.CheckRunExecution), and an execution's job no longer
// names the run it belongs to.
func (c cloudRunCodec) ValidateRun(name, region, job string) (ExecID, error) {
	if job == "" {
		return ExecID{}, fmt.Errorf("execution %s is not bound to a job: the run's job name is required", name)
	}
	id, err := c.ParseInRegion(name, region)
	if err != nil {
		return ExecID{}, err
	}
	if id.Job != job {
		return ExecID{}, fmt.Errorf("execution %s belongs to job %s, not the run's job %s", name, id.Job, job)
	}
	return id, nil
}

// WrongRegionError is an execution name outside the backend's region.
// Region has passed the [a-z0-9-] check, so it is safe to quote.
type WrongRegionError struct {
	Name   string // the execution
	Region string // the execution's region
	Want   string // the backend's region
}

func (e *WrongRegionError) Error() string {
	return fmt.Sprintf("execution %s is in region %s, not %s", e.Name, e.Region, e.Want)
}

// LocalCodec is the local invocation's codec: it has no platform, so it
// has no name to parse, no signal to answer with and no execution to
// report. It exists so that "which backend is this process?" has one
// answer everywhere, and so that a local run cannot be mistaken for a
// cloud platform no codec recognises.
func LocalCodec() Codec { return localCodec{} }

type localCodec struct{}

func (localCodec) Backend() string { return Local }

func (localCodec) Parse(string) (ExecID, bool) { return ExecID{}, false }

func (localCodec) Format(ExecID) string { return "" }

func (localCodec) FromEnv(func(string) string) (string, error) { return "", nil }

// On is false whatever the environment says: nothing local claims a
// process, and no environment variable can make it.
func (localCodec) On(func(string) string) bool { return false }

func (localCodec) ParseInRegion(name, region string) (ExecID, error) {
	return ExecID{}, fmt.Errorf("%q is not a run name: a local run has no execution", name)
}

func (localCodec) ValidateRun(name, region, job string) (ExecID, error) {
	return ExecID{}, fmt.Errorf("%q is not a run name: a local run has no execution", name)
}

// ParseExecution parses name with whichever codec knows it, so a caller
// holding a name but no backend (the lock, the runner, a run listing) does
// not have to pick one. Callers that hold a backend ask it instead:
// Backend.Codec.
func ParseExecution(name string) (ExecID, bool) {
	for _, c := range codecs() {
		if id, ok := c.Parse(name); ok {
			return id, true
		}
	}
	return ExecID{}, false
}

// SameExecution reports whether a and b name the same run, whichever
// backend spelled them: the first codec that parses a decides, and b must
// be the same kind of name with the same key. Compare runs by this, never
// by ==.
func SameExecution(a, b string) bool {
	for _, c := range codecs() {
		x, ok := c.Parse(a)
		if !ok {
			continue
		}
		y, ok2 := c.Parse(b)
		return ok2 && x.Key() == y.Key()
	}
	return false
}
