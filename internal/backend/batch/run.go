package batch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	batch "google.golang.org/api/batch/v1"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Launch creates the run's Batch job. One Batch job is one run -- a job is
// single-use and there is no reusable definition to run, so this is a create
// rather than Cloud Run's jobs.run (design §2.3) -- and it returns as soon as
// Batch has accepted it: a job sits QUEUED until the region has capacity for
// its VMs, so Launch never waits for the run to start.
//
// A create that fails with a 4xx other than 408, 429 and 499 created
// nothing, and the error says so (backend.ErrRejected). Anything else,
// including a timeout, is ambiguous: the job may exist. Everything that can
// fail before the create -- an argument no job can be built from, a label the
// installation cannot write, an unreadable workflow timeout -- created
// nothing either, and says so the same way.
func (b *Backend) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.ExecutionRef, error) {
	if spec.Repo.Slug == "" || spec.Workflow == "" || spec.RunID == "" {
		return backend.ExecutionRef{}, notCreated(fmt.Errorf("launching: a run needs a repository slug, a workflow and a run id (got %q, %q, %q)", spec.Repo.Slug, spec.Workflow, spec.RunID))
	}
	if spec.Job != "" {
		// A shared project job (phase 3, plan task 3.4 step 1) is a Cloud Run
		// job the installation deploys per profile, with the profile's own
		// account, secrets and limits. This backend builds a single-use job per
		// run from the repository's own settings and has no counterpart for a
		// profile yet, so running the per-repository job here would run the
		// workflow as an identity the project's policy did not choose. Refused,
		// not guessed at, until the compute backend learns project jobs.
		return backend.ExecutionRef{}, notCreated(fmt.Errorf("launching %s/%s: the workflow runs on the project job %s, and Cloud Batch has no project jobs yet; nothing was created", spec.Repo.Slug, spec.Workflow, spec.Job))
	}
	timeout, err := b.timeout(spec)
	if err != nil {
		return backend.ExecutionRef{}, notCreated(err)
	}
	id := JobID(spec.Repo.Slug, spec.Workflow, spec.RunID)
	full := b.location() + "/jobs/" + id
	job, err := b.jobDefinition(spec, id, full, timeout)
	if err != nil {
		return backend.ExecutionRef{}, notCreated(err)
	}
	created, err := b.batch.Projects.Locations.Jobs.Create(b.location(), job).JobId(id).Context(ctx).Do()
	if err != nil {
		return backend.ExecutionRef{}, launchError(full, err)
	}
	cid, err := b.canonical(created.Name)
	if err != nil {
		return backend.ExecutionRef{}, fmt.Errorf("Batch created %s but answered with %q: %w", full, created.Name, err)
	}
	created0, err := parseTime(created.CreateTime)
	if err != nil {
		b.o.Warn(fmt.Sprintf("job %s: createTime: %v", full, err))
	}
	return backend.ExecutionRef{Name: b.codec.Format(cid), Job: cid.Job, LogURL: b.logURL(cid, created.Uid, created0)}, nil
}

// timeout is the run's total time: the workflow's own when the caller has it
// (LaunchSpec.Timeout), else what the workflow is configured with. On Batch
// the job definition carries the timeout (there is no execution override),
// so it must be known before the create.
func (b *Backend) timeout(spec backend.LaunchSpec) (time.Duration, error) {
	if spec.Timeout > 0 {
		return spec.Timeout, nil
	}
	d, err := b.o.Timeouts.TaskTimeout(spec.Repo.Slug, spec.Workflow)
	if err != nil {
		return 0, fmt.Errorf("launching %s/%s: %w", spec.Repo.Slug, spec.Workflow, err)
	}
	if d <= 0 {
		d = backend.DefaultTaskTimeout
	}
	return d, nil
}

// jobDefinition is the Batch job of one run: a single-use job whose one task
// is the run, on a VM of the installation's machine type booted from the
// preloaded image, placed in the named network and subnetwork with no
// external IP, running as the repository's dedicated service account, with
// the VM's Docker socket mounted when the workflow asked for Docker (design
// §2.2, §2.3, §2.4; §8.1 Task 7).
func (b *Backend) jobDefinition(spec backend.LaunchSpec, id, full string, timeout time.Duration) (*batch.Job, error) {
	labels, err := Labels(spec.Repo.Slug, spec.Workflow, spec.RunID)
	if err != nil {
		return nil, fmt.Errorf("launching %s: %w", full, err)
	}
	mt, err := MachineTypeOf(b.o.MachineType)
	if err != nil {
		return nil, fmt.Errorf("launching %s: %w", full, err)
	}
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
	// The socket recipe (design §2.2, C2). Both refusals are here rather
	// than only in New because they are about a *run*: an installation may
	// legitimately have no gid recorded and no streaming, and only a run
	// that asked for Docker needs either answer.
	container := &batch.Container{ImageUri: b.o.Image}
	if spec.Docker {
		d := b.o.Docker
		if d.SocketGID == 0 {
			return nil, errors.New("this run's workflow asks for docker: true, but this installation has no Docker socket gid recorded: read it off the boot image's VM with `stat -c %g /var/run/docker.sock` (design §2.2). Launching without it would mount a socket the run cannot open, so nothing was created")
		}
		if b.o.ImageStreaming {
			return nil, errors.New("this run's workflow asks for docker: true, and image streaming is on: batch ignores a runnable's options field when streaming an image, so the container would come up without --group-add and could not open the socket (measured 2026-10-15; design §1.1, §1.2), so nothing was created")
		}
		// The host's socket, at the same path inside the container, plus the
		// group join. The gid is numeric: --group-add resolves a name inside
		// the container's own /etc/group, where the image has no docker
		// group. --user 0 is not used and --privileged changes nothing for
		// this process (both measured, §2.2); runAsNonRoot stays unset below.
		p := d.path()
		container.Volumes = []string{p + ":" + p}
		container.Options = fmt.Sprintf("--group-add %d", d.SocketGID)
		// The agent's own docker commands read this rather than a compiled-in
		// default path (design §2.2, §4.3); it is allowlisted on the agent's
		// side only for a run that has a socket.
		vars["DOCKER_HOST"] = "unix://" + p
	}
	// The launch's own variables (the launcher's budget marker) land in the
	// same map, which is the whole of the task's environment on the VM
	// (design §2.2). The map is what makes the identity entries above
	// reserved: a later write to a map key is what a run would believe about
	// itself, so an entry naming one of them is refused rather than applied
	// (backend.LaunchEnv, asked with every key this job already sets). The
	// merge is after the Docker block so DOCKER_HOST is one of them.
	//
	// The platform's own five join those keys (backend.ReservedEnvNames), the
	// same list Cloud Run hands the same function: four of them are map keys
	// here and FUGARO_BUCKET is not — no Batch job writes the runs bucket, and
	// this backend's launcher wiring is not built yet — so without the shared
	// list an entry naming FUGARO_BUCKET would be the one way a launcher could
	// choose which bucket a run writes to on this backend while Cloud Run
	// refused it (the review finding on this change). Reserving a name the map
	// does not hold costs a launcher nothing it was allowed to set: the bucket
	// is the installation's configuration either way (backend.PlatformEnvNames).
	reserved := make([]string, 0, len(vars)+len(backend.PlatformEnvNames))
	for name := range vars {
		reserved = append(reserved, name)
	}
	reserved = backend.ReservedEnvNames(reserved...)
	extra, err := backend.LaunchEnv(spec, reserved...)
	if err != nil {
		return nil, fmt.Errorf("launching %s: %w", full, err)
	}
	for _, kv := range extra {
		if kv.Secret {
			// A secret entry is the budget seal key (D15): the only place this
			// backend could put it is the job definition's plain variables,
			// which every launcher (roles/batch.jobsEditor on the Fugaro job
			// prefix, compute/iam.tf) and the reaper (batch.jobs.get) can read
			// back with `gcloud batch jobs describe`, for as long as the job is
			// kept, and which may reach the CreateJob audit record — not a peer
			// run (its account holds only agentReporter and logWriter). What
			// that carrier adds is a standing copy in the job resource, and the
			// reaper as a reader: the launchers are already in the Cloud Run
			// override's audience in kind, and
			// docs/design/budget-token-channel.md calls the seal itself "a
			// capability, not a boundary: it removes the value of what a peer
			// can reach; it does not narrow what a peer can reach".
			//
			// This refusal is the **answer** and not a waiting state (the
			// compute backend plan's P1, 2026-10-19): the seal's carrier on
			// Batch is a documented limit, because the one carrier this
			// backend could use makes the key a standing copy in the job
			// resource and widens its audience to the reaper, and the only
			// other one the design allows is its rejected C2 (a per-run Secret
			// Manager secret on a shared per-(profile, account) account). So
			// Batch carries no sealed budget run. **A launch is sealed
			// whenever its installation mints** (internal/cli/launch_budget.go's
			// budgetMintOn), and an installation has one backend, so this is
			// not a per-run fallback: an installation that mints budget tokens
			// cannot launch any run on Batch. The message below says all of
			// that to the operator, and it never quotes the value
			// (LaunchSpec.SecretEnv's own contract: the value leaves the
			// launcher once, as the execution's override member).
			return nil, fmt.Errorf("launching %s: the launch carries %s as a secret (the budget token's seal key), and a Batch job has no per-execution override: its variables are the job's definition, which every launcher the Fugaro job-name condition admits and the reaper can read back for as long as the job is kept, so the key is not put there and nothing was created; this refusal is deliberate, and it is the documented limit (docs/design/budget-token-channel.md, \"The Batch backend refuses a sealed launch\"): every launch of an installation that mints is sealed, so an installation that mints budget tokens — a budget database with budget mode on, or a published policy — cannot launch any run on Batch, and such an installation has to stay on backend: cloud-run", full, kv.Name)
		}
		vars[kv.Name] = kv.Value
	}
	// The other fast start, and the one a Docker run cannot have: with
	// streaming on Batch disregards container.options (design §1.2, §2.2).
	container.EnableImageStreaming = b.o.ImageStreaming
	// The task runs for the workflow's total time plus the slack every other
	// backend adds, so a run whose own watchdog is late is still bounded by
	// Batch. maxRunDuration bounds a task once it is running; nothing bounds
	// the queue, which is the reaper's queue deadline (design §1.4).
	env := &batch.Environment{Variables: vars}
	if len(b.o.Secrets) > 0 {
		// Names, never values: the VM reads the named secrets itself.
		//
		// A secret may not also be one of the job's own variables. Both fields
		// are one env map on the VM, so a name in both leaves which entry wins
		// to the platform -- and the entries in `vars` are how the job tells
		// the runner which run it is, which backend it is on and which project
		// it is in (§2.1, §2.5). A run whose FUGARO_RUN came from a secret
		// would be a run the launcher could not find again.
		secretNames := make([]string, 0, len(b.o.Secrets))
		for name := range b.o.Secrets {
			secretNames = append(secretNames, name)
		}
		sort.Strings(secretNames)
		for _, name := range secretNames {
			if _, ok := vars[name]; ok {
				return nil, fmt.Errorf("launching %s: the secret %s is also one of the job's own environment variables, and the job's value is the one the run reads its own identity and configuration from (design §2.1, §2.5): refusing rather than leaving which entry wins to the platform", full, name)
			}
		}
		env.SecretVariables = b.o.Secrets
	}
	return &batch.Job{
		// The labels go in both fields (C13): Job.labels reach the job and
		// its LogEntries, AllocationPolicy.labels reach the VMs.
		Labels: labels,
		AllocationPolicy: &batch.AllocationPolicy{
			Labels: labels,
			Instances: []*batch.InstancePolicyOrTemplate{{
				// Project-wide SSH keys must not open a VM that runs a
				// Docker-capable agent (design §3.5).
				BlockProjectSshKeys: true,
				// The VM: the installation's machine type, from the
				// preloaded image (design §1.2/§1.3). The image is what
				// makes a Docker run's cold start fit the budget, and the
				// disk size is what a disk created from it is grown to.
				Policy: &batch.InstancePolicy{
					MachineType: b.o.MachineType,
					BootDisk: &batch.Disk{
						Image:  b.o.BootImage,
						SizeGb: b.o.BootDiskGB,
					},
				},
			}},
			// Never the default Compute Engine account: New refuses it.
			ServiceAccount: &batch.ServiceAccount{Email: b.o.ServiceAccount},
			// The VMs carry no external IP (design §1.7, C7). Both names are
			// sent because Check 57a measured that Batch refuses
			// noExternalIpAddress unless the network and the subnetwork are
			// both named explicitly; the fields without the pair would be a
			// VM with an external IP.
			Network: &batch.NetworkPolicy{
				NetworkInterfaces: []*batch.NetworkInterface{{
					Network:             b.o.Network,
					Subnetwork:          b.o.Subnetwork,
					NoExternalIpAddress: true,
				}},
			},
		},
		// Without this the job's output never reaches Cloud Logging, and a
		// run's logs land in the project's default logs (design §1.5, C9).
		LogsPolicy: &batch.LogsPolicy{Destination: "CLOUD_LOGGING"},
		TaskGroups: []*batch.TaskGroup{{
			TaskCount:   1,
			Parallelism: 1,
			TaskSpec: &batch.TaskSpec{
				// A Fugaro run is not idempotent: retrying it would repeat
				// its side effects, the same reason Cloud Run's job sets
				// max_retries = 0 (design §2.3). ForceSendFields is what
				// keeps the 0 in the JSON: an int64 with omitempty drops it.
				MaxRetryCount:   0,
				ForceSendFields: []string{"MaxRetryCount"},
				MaxRunDuration:  fmt.Sprintf("%ds", int((timeout+backend.TaskTimeoutSlack)/time.Second)),
				Environment:     env,
				// The task asks for a share of the machine type, not all of
				// it: Batch places a task on a VM that can hold what this
				// asks for, and the VM also runs the OS, the Batch agent and
				// the Docker daemon, with the run's own containers on top of
				// them (design §1.3, §2.2). A request for the whole type
				// leaves nothing for those and sits QUEUED; the shares below
				// are what Check 57a actually scheduled with (§7.2).
				ComputeResource: taskResources(mt),
				Runnables:       []*batch.Runnable{{Container: container}},
			},
		}},
	}, nil
}

// taskVCPUShare and taskMemShare are the headroom a run's task leaves to the
// VM it runs on: the task asks for half the machine type's vCPUs and a
// quarter of its memory.
//
// Batch places a task on a VM that can hold what its computeResource asks for,
// and that VM is also running the OS, the Batch agent and the Docker daemon,
// with the run's own containers on top of them. A task that asked for the
// whole machine type -- 4000m/16384MiB on `e2-standard-4` -- would leave
// nothing for any of those and would sit QUEUED. Check 57a's submissions asked
// for exactly these shares on that type (2000m/4096MiB, design §7.2, the
// committed job JSON) and every one of them scheduled, which is the whole of
// the evidence there is: **Check 57b-9 is what measures the largest request
// that actually schedules on `e2-standard-4`**, and these two numbers move
// only when it says they can.
//
// They are a ceiling, not a measurement of what a workflow needs -- the
// workflow `resources` field is where a run's real needs belong (design §4),
// and plumbed there they can only ask for less than this.
const (
	taskVCPUShare = 2
	taskMemShare  = 4
)

// taskResources is what a run's task asks Batch for: the machine type's own
// quantities less that headroom.
func taskResources(mt MachineType) *batch.ComputeResource {
	return &batch.ComputeResource{
		CpuMilli:  int64(mt.VCPU) * 1000 / taskVCPUShare,
		MemoryMib: int64(mt.MemoryGiB) * 1024 / taskMemShare,
	}
}

// Execution reads one run by its full resource name.
func (b *Backend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	id, err := b.canonical(name)
	if err != nil {
		return backend.Execution{}, err
	}
	full := b.codec.Format(id)
	j, err := b.batch.Projects.Locations.Jobs.Get(full).Context(ctx).Do()
	if err != nil {
		return backend.Execution{}, apiError("reading job "+full, err)
	}
	return b.toExecution(j)
}

func (b *Backend) toExecution(j *batch.Job) (backend.Execution, error) {
	id, err := b.canonical(j.Name)
	if err != nil {
		return backend.Execution{}, fmt.Errorf("Batch returned a job: %w", err)
	}
	name := b.codec.Format(id)
	raw := ""
	if j.Status != nil {
		raw = j.Status.State
	}
	state, known := stateOf(raw)
	if !known {
		b.o.Warn(fmt.Sprintf("job %s: Batch state %q is not one this build knows: treating it as pending, never as finished", name, raw))
	}
	x := backend.Execution{Name: name, Job: id.Job, State: state}
	if x.Created, err = parseTime(j.CreateTime); err != nil {
		b.o.Warn(fmt.Sprintf("job %s: createTime: %v", name, err))
	}
	// Started and Completed are left zero. Design §2.3 reads them from the
	// job's and its task's status events, whose shape -- which event types
	// exist, and whether a transition always leaves one -- is UNVERIFIED:
	// Check 57a steps 7 and 8 settle it (design §9). A guessed start time
	// would become a wrong cost, and the billed window is Task 3's (§5.2),
	// so nothing is inferred here: Billed is 0, which reads as "cost
	// unknown", not as "free".
	// The region is the backend's own answer, never a second reading of the
	// name (backend.Execution.Region): a caller that prices this execution
	// uses it rather than parsing a Batch name's fields.
	x.Region = b.o.Region
	// MachineType is read off the job, not off this backend's configuration
	// (jobMachineType): a machine type is a fact about the execution, and
	// Options is a fact about today -- an operator who changes the profile's
	// machine type must not silently re-price every run already recorded at
	// the new type's quantities (design §5.2). The configuration is the
	// fallback for a job that names none, which this backend's own jobs
	// never do (checkVM requires one).
	if x.MachineType = jobMachineType(j); x.MachineType == "" {
		x.MachineType = b.o.MachineType
	}
	x.LogURL = b.logURL(id, j.Uid, x.Created)
	x.Run = runEnv(j)
	x.Preempted = preempted(j)
	return x, nil
}

// preemptExitCode is Cloud Batch's reserved exit code for a task whose VM the
// platform reclaimed: a Spot VM preempted while the run was on it. The number
// is the owner's, from the ruling this read exists for (2026-10-21) -- Batch
// documents that a task which failed for one of its own reserved reasons
// carries one of its reserved codes rather than the runnable's 50000, and
// points at cloud.google.com/batch/docs/troubleshooting#reserved-exit-codes,
// a list this build does not vendor (batch/v1's TaskExecution.ExitCode) --
// and **Check 57a step 7 is what measures it**: it is the step the design left
// unrun and Task 12 re-runs (design §7.2, plan Task 12), so this constant is a
// ruling's number and not yet a measured one. The register is where it stops
// being second-hand, and this comment is what a reader compares that
// measurement against.
//
// It is written here, in code, and deliberately not in the design document:
// that document's own text says no `50001` appears anywhere under `docs/`
// outside the plan until the check is run, and a test in internal/cli
// (`TestDocsComputeBackendSaysWhatTheTreeDisagreesWith`) asserts exactly that
// over the whole directory. The number has one home until 57a-7 runs.
const preemptExitCode = 50001

// preemptMarker is the word a status event's description carries when the
// platform reclaimed the machine this execution was on: it is the participle a
// report of the *event* uses ("the VM was preempted"), matched without regard
// to case because the prose is the platform's own.
//
// It is the participle and deliberately not the stem. "preempt" also matches
// "preemptible" -- the adjective that names a *class* of VM, which a quota
// event says the project ran out of ("Quota 'PREEMPTIBLE_CPUS' exceeded") --
// and "preemption", the notice's noun rather than the reclaim. Either would
// make a reader call a run reclaimed because of a fact about the project's
// capacity or about a notice the job asked for, and the run that later crashed
// would be reported to a person as the platform having taken its VM. A run
// whose machine was reclaimed is one the report says *was preempted*; nothing
// weaker is evidence, and the exact wording a live preemption leaves behind is
// Check 57a step 7's to record (design §7.2, §9).
const preemptMarker = "preempted"

// preempted reports whether the platform's own report of job j names a Spot
// preemption, which is what makes a terminal failure a reclamation rather than
// a fault of the run (backend.Execution.Preempted).
//
// Two conditions, and both are needed. The job must be **FAILED**: a state this
// build cannot read, or any state that is not the terminal failure, is not a
// preemption however its events read, because a *live* run must never be
// derived as dead -- an unrecognised state is treated as pending elsewhere
// (stateOf) and must not become evidence of anything here. And the job's own
// report must name it, in one of the two shapes Batch leaves such a report in:
// a task-level status event whose task execution ended with the reserved
// preemption code, or a status event whose description carries preemptMarker.
// The second is the platform's own prose -- the exact wording is Check 57a step
// 7's to record, and a reader that only knew the exit code would find no
// preemption at all if this Batch version reports the event and not the code --
// while the first is the documented machine-readable half.
//
// "Names it" is the narrow claim, and the marker above is what keeps it narrow:
// an event is evidence of a reclaim only when it says the machine *was*
// preempted, never merely for mentioning preemption. See preemptMarker.
//
// Nothing else is read: not the runnable's own exit code, not the job's
// counts, and nothing that a *run* can write. The job body is the platform's,
// so this is evidence a run cannot forge.
func preempted(j *batch.Job) bool {
	if j.Status == nil || j.Status.State != "FAILED" {
		return false
	}
	for _, ev := range j.Status.StatusEvents {
		if ev == nil {
			continue
		}
		if ev.TaskExecution != nil && ev.TaskExecution.ExitCode == preemptExitCode {
			return true
		}
		if strings.Contains(strings.ToLower(ev.Description), preemptMarker) {
			return true
		}
	}
	return false
}

// jobMachineType is the machine type a job's VMs are, from the job itself:
// allocationPolicy.instances[0].policy.machineType, the field jobDefinition
// wrote. "" for a job that names none -- one this backend did not build, or
// one built before the field was sent -- which is why the caller falls back
// to its own configuration.
func jobMachineType(j *batch.Job) string {
	if j.AllocationPolicy == nil || len(j.AllocationPolicy.Instances) == 0 {
		return ""
	}
	inst := j.AllocationPolicy.Instances[0]
	if inst == nil || inst.Policy == nil {
		return ""
	}
	return inst.Policy.MachineType
}

// runEnv is the FUGARO_RUN the job was created with, from the task's
// environment variables. Batch reports them, so unlike Cloud Run this one is
// always readable -- and it is still only a hint: whoever can create a job
// can set it (design §2.3).
func runEnv(j *batch.Job) string {
	if len(j.TaskGroups) == 0 || j.TaskGroups[0] == nil || j.TaskGroups[0].TaskSpec == nil ||
		j.TaskGroups[0].TaskSpec.Environment == nil {
		return ""
	}
	return j.TaskGroups[0].TaskSpec.Environment.Variables["FUGARO_RUN"]
}

// fugaroLabelFilter is what jobs.list is asked for: the installation's own
// jobs, by the marker every Fugaro resource carries (design §2.4). On Batch
// the `fugaro-` name prefix Cloud Run's List filters on is not enough on its
// own, because a job's label is also what reaches its log entries and its
// VMs.
//
// The syntax is AIP-160, the form gcloud's own `--filter` uses
// (`labels.fugaro_chk=57` in design §7.2's check commands), and it is not
// among the facts design §9 verifies. List therefore checks the labels
// client-side as well: whether the server honours the filter or ignores it,
// the answer is the same.
const fugaroLabelFilter = `labels.fugaro = "managed"`

// List returns runs newest first: of every Fugaro job, or of f.Jobs.
//
// Batch's jobs.list is per region, with no per-job parent, and it keeps
// returning a job for 60 days after it finished (design §2.4), so the state
// and create-time filters are applied here rather than assumed from the
// listing's contents.
func (b *Backend) List(ctx context.Context, f backend.ListFilter) ([]backend.Execution, error) {
	want := map[string]bool{}
	for _, j := range f.Jobs {
		want[j] = true
	}
	var out []backend.Execution
	token := ""
	for {
		resp, err := b.batch.Projects.Locations.Jobs.List(b.location()).Filter(fugaroLabelFilter).
			PageSize(b.listPageSize).PageToken(token).Context(ctx).Do()
		if err != nil {
			return nil, apiError("listing jobs in "+b.location(), err)
		}
		for _, j := range resp.Jobs {
			id, ok := b.codec.Parse(j.Name)
			if !ok || len(f.Jobs) > 0 && !want[id.Job] {
				continue
			}
			if !managedJob(j) {
				continue
			}
			x, err := b.toExecution(j)
			if err != nil {
				return nil, err
			}
			// A job whose createTime was empty or unreadable is kept, not
			// cut: its Created is the zero time, which is Before every Since.
			// gcp's List applies the same cut only to a time it parsed, and
			// dropping it here is the fail-dangerous direction -- the caller
			// is the sweeper's ActiveOnly+Since listing, where a job that
			// vanishes from the listing is a VM nobody reaps (Options.Warn:
			// one bad job does not fail a whole listing).
			if !f.Since.IsZero() && !x.Created.IsZero() && x.Created.Before(f.Since) {
				continue
			}
			if f.ActiveOnly && x.State.Terminal() {
				continue
			}
			out = append(out, x)
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	// The listing is newest first, but the API does not promise an order, so
	// the sort is explicit rather than assumed.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// managedJob reports whether a listed job carries the installation's own
// marker in both label fields. The job's labels are the ones that matter for
// adoption (design §2.4): a job without them is not ours to read, cancel or
// delete, whatever its name looks like.
func managedJob(j *batch.Job) bool {
	if j.Labels[labelManaged] != managedValue {
		return false
	}
	if j.AllocationPolicy == nil || j.AllocationPolicy.Labels[labelManaged] != managedValue {
		return false
	}
	return true
}

// LongestTaskTimeout is the longest configured workflow timeout (design
// §2.3): on Batch there is no deployed job to read it from, so it is a
// config/policy read.
func (b *Backend) LongestTaskTimeout(ctx context.Context) (time.Duration, error) {
	return b.o.Timeouts.LongestTaskTimeout()
}

// TaskTimeout is the workflow's configured task timeout (design §2.3), an
// error wrapping backend.ErrNotFound for a workflow that does not exist.
func (b *Backend) TaskTimeout(ctx context.Context, slug, workflow string) (time.Duration, error) {
	return b.o.Timeouts.TaskTimeout(slug, workflow)
}

// Cancel asks Batch to cancel the job: jobs.cancel, which leaves the job's
// record readable -- fugaro ls, diagnose, watch and the sweeper all read it
// back afterwards. jobs.delete erases the record, and belongs to the reaper
// and the restore alone (design §1.4, C8).
//
// A job that finished between the caller's read and this call is not an
// error: the caller asked for it to stop and it has. The API's answer for a
// job that already finished is UNVERIFIED (design §9, Check 57a step 7), so
// the race is tolerated by reading the job back rather than by trusting the
// refusal's code.
func (b *Backend) Cancel(ctx context.Context, name string) error {
	id, err := b.canonical(name)
	if err != nil {
		return err
	}
	full := b.codec.Format(id)
	if _, err := b.batch.Projects.Locations.Jobs.Cancel(full, &batch.CancelJobRequest{}).Context(ctx).Do(); err != nil {
		if x, rerr := b.Execution(ctx, full); rerr == nil && x.State.Terminal() {
			return nil
		}
		return apiError("cancelling job "+full, err)
	}
	return nil
}
