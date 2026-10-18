package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend"
)

var _ backend.Backend = (*Backend)(nil)

// location is projects/<p>/locations/<r>, the parent of the jobs.
func (b *Backend) location() string {
	return "projects/" + b.o.GCPProject + "/locations/" + b.o.Region
}

// JobPath is the full resource name of the Cloud Run job of (slug, workflow).
func (b *Backend) JobPath(slug, workflow string) string {
	return b.location() + "/jobs/" + JobName(slug, workflow)
}

// nameRE is what every part of a name the backend follows must look like:
// project IDs and numbers, regions, job and execution names. The execution
// name's own parts are checked by the codec
// (backend.CloudRunCodec().ParseInRegion, through parseInRegion below); this
// one covers the job names the backend builds and reads itself.
var nameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// Codec is Cloud Run's name codec (design compute-backend.md §2.5): the
// shape of an execution name, and the signal (CLOUD_RUN_EXECUTION) that
// says a process is one of this backend's executions.
func (b *Backend) Codec() backend.Codec { return backend.CloudRunCodec() }

// canonical rebuilds an execution name with the backend's project ID, so a
// project number the API sends never reaches a caller: the CLI and the
// runner store the same name for one execution.
//
// Names reach it from bucket objects a run's own service account can write
// (result.json, launch.json), so it follows only a name in the backend's
// region whose every part is [a-z0-9-]: nothing else reaches a request
// path. Callers that know the run bind the job too (the
// CLI's checkExecution).
func (b *Backend) canonical(name string) (backend.ExecID, error) {
	id, err := parseInRegion(name, b.o.Region)
	if err != nil {
		return backend.ExecID{}, err
	}
	id.GCPProject = b.o.GCPProject
	return id, nil
}

// parseInRegion parses name through the backend's codec, and refuses it
// unless it is in region and every part is [a-z0-9-]. It binds no job:
// callers that know the run bind it with CheckRunExecution.
func parseInRegion(name, region string) (backend.ExecID, error) {
	return backend.CloudRunCodec().ParseInRegion(name, region)
}

// CheckExecutionName refuses an execution name read from a run's bucket
// objects unless the backend could have returned it: in region, and every
// part [a-z0-9-]. The run's service account can rewrite those objects, so a
// name nothing else checks would otherwise reach a request path or a log
// filter.
func CheckExecutionName(name, region string) error {
	_, err := parseInRegion(name, region)
	return err
}

// CheckRunExecution refuses x, an execution read from a run's bucket
// objects, unless it is the execution of run (slug, runID): the FUGARO_RUN
// the execution itself reports must be "<slug>/<run id>".
//
// It used to bind the run to the *job* the execution's name was in
// (JobName(slug, variant)). Under the project-centric model one job is
// shared by every repository admitted to a profile, so an execution's job
// says nothing about which run it is, and two runs of one job — or of two
// repositories on one profile — would have been followable by each other
// (design §4.1, plan task 3.4 step 2). What does say it is the FUGARO_RUN
// override the launcher set, which the platform reports back in the
// execution's own template: a run's service account can write its own
// bucket objects, but not another execution's environment.
//
// The name is checked separately (CheckExecutionName), and first: it is the
// half that needs no request, so a name in another region is refused with
// the region to pass (--region) rather than as a missing execution.
func CheckRunExecution(x backend.Execution, slug, runID string) error {
	want := slug + "/" + runID
	if x.Run != want {
		// Not x.Run: the refusal reaches whoever asked about this run, and
		// the execution it names may be another repository's (review D2).
		return fmt.Errorf("execution %s reports another run's FUGARO_RUN (or none), not %q: it is not the execution of run %s/%s. A run's objects may name only its own execution, and on a shared job they are the only thing that names one", x.Name, want, slug, runID)
	}
	return nil
}

// jobFor is the job a launch runs: the one the spec names (the shared
// project job of the profile the workflow selects, plan task 3.4 step 1),
// else the per-repository job of the slug and the workflow.
func (b *Backend) jobFor(spec backend.LaunchSpec) string {
	if spec.Job != "" {
		return b.location() + "/jobs/" + spec.Job
	}
	return b.JobPath(spec.Repo.Slug, spec.Workflow)
}

// Launch starts one execution of the workflow's job with FUGARO_RUN set.
// It returns once Cloud Run has accepted the execution; the operation it
// returns completes only when the execution ends, so Launch never waits.
func (b *Backend) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.ExecutionRef, error) {
	// Cloud Run has no Docker daemon and no way to give a run one: a job
	// that asked for docker: true would start, run its stages, and fail
	// stage by stage with nothing to talk to (design compute-backend.md
	// §4.2/§4.3). The ask is the workflow's, so it is refused here, before
	// the run exists, and nothing can have started.
	if spec.Docker {
		return backend.ExecutionRef{}, fmt.Errorf("launching %s/%s: this workflow asks for docker: true, and Cloud Run cannot give a run a Docker daemon: it has no VM to mount one from. Run it on a backend that has VMs, or drop docker: true: %w", spec.Repo.Slug, spec.Workflow, backend.ErrRejected)
	}
	job := b.jobFor(spec)
	// The spec's own variables (the launcher's budget marker) go in the same
	// override as FUGARO_RUN: the API merges these into the job's environment.
	// A secret entry (the marker of a launch that sealed its token, whose
	// value is the seal key, D15) goes there too: the execution's override is
	// the carrier docs/design/budget-token-channel.md §7.1 accepts and prices
	// (the project's readers, not a peer run of the job).
	// backend.LaunchEnv is where both backends read them, so an entry this one
	// refuses (a malformed one, or one naming a variable the environment
	// already holds) is refused on Cloud Batch too.
	//
	// What the environment already holds is what is reserved: the
	// installation's own configuration (backend.PlatformEnvNames, which
	// internal/infra's platformEnv writes into the deployed job), the
	// execution's own identity (CLOUD_RUN_JOB and CLOUD_RUN_EXECUTION, which
	// Cloud Run fills in and the codec reads back to say which job and which
	// execution this is — internal/backend's codec.go), and FUGARO_RUN, which
	// this override sets below. An entry naming one of them is applied over
	// the value the run believes about itself, which is what
	// backend.ReservedEnvNames is: one list for both backends, so Batch
	// refuses the same names rather than the keys of its own map alone (the
	// review finding on this change: this backend reserved FUGARO_RUN alone,
	// so a launcher entry naming FUGARO_BUCKET or FUGARO_REGION was applied
	// here and refused there — and Batch's map holds no FUGARO_BUCKET at all,
	// so the parity this comment claimed was not the reservation the two
	// backends made).
	reserved := backend.ReservedEnvNames("FUGARO_RUN", "CLOUD_RUN_JOB", "CLOUD_RUN_EXECUTION")
	extra, err := backend.LaunchEnv(spec, reserved...)
	if err != nil {
		// Nothing has been sent, so this is a refusal and not an ambiguity:
		// the launcher's ErrRejected branch drops its claim and the token it
		// minted, and its ambiguous branch keeps both.
		return backend.ExecutionRef{}, fmt.Errorf("launching %s: %v, so nothing was submitted: %w", job, err, backend.ErrRejected)
	}
	env := make([]*run.GoogleCloudRunV2EnvVar, 0, len(extra)+1)
	env = append(env, &run.GoogleCloudRunV2EnvVar{Name: "FUGARO_RUN", Value: spec.Repo.Slug + "/" + spec.RunID})
	for _, kv := range extra {
		env = append(env, &run.GoogleCloudRunV2EnvVar{Name: kv.Name, Value: kv.Value})
	}
	req := &run.GoogleCloudRunV2RunJobRequest{Overrides: &run.GoogleCloudRunV2Overrides{
		ContainerOverrides: []*run.GoogleCloudRunV2ContainerOverride{{Env: env}},
	}}
	if spec.Timeout != 0 {
		req.Overrides.Timeout = fmt.Sprintf("%ds", int((spec.Timeout+backend.TaskTimeoutSlack)/time.Second))
	}
	op, err := b.run.Projects.Locations.Jobs.Run(job, req).Context(ctx).Do()
	if err != nil {
		return backend.ExecutionRef{}, launchError(job, spec.Job != "", err)
	}
	var meta struct {
		Name   string `json:"name"`
		LogURI string `json:"logUri"`
	}
	if err := json.Unmarshal(op.Metadata, &meta); err != nil || meta.Name == "" {
		return backend.ExecutionRef{}, fmt.Errorf("Cloud Run started %s but did not report the execution (operation %s)", job, op.Name)
	}
	id, err := b.canonical(meta.Name)
	if err != nil {
		return backend.ExecutionRef{}, fmt.Errorf("Cloud Run started %s (operation %s): %w", job, op.Name, err)
	}
	return backend.ExecutionRef{Name: id.String(), Job: id.Job, LogURL: b.logURL(id, meta.LogURI, b.now())}, nil
}

// statusClientClosed is 499, gRPC CANCELLED over HTTP: the request was
// abandoned, not refused, so it is ambiguous.
const statusClientClosed = 499

// launchError classifies a jobs.run failure. Only a definitive refusal (a
// 4xx other than 408, 429 and 499) wraps ErrRejected: nothing can have started.
// Anything else, including timeouts and transport errors, is ambiguous.
//
// shared says the job was the spec's own (a shared project job), which has a
// different fix from a repository's: no `fugaro init --repo` creates one.
func launchError(job string, shared bool, err error) error {
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		switch {
		case ge.Code == http.StatusNotFound && shared:
			return fmt.Errorf("launching: the project job %s does not exist; the installation's shared project jobs are deployed by fugaro init, which also publishes the profiles the launcher selects them by (%v): %w: %w", job, err, backend.ErrNotFound, backend.ErrRejected)
		case ge.Code == http.StatusNotFound:
			return fmt.Errorf("launching: job %s does not exist; create it with fugaro init --repo (%v): %w: %w", job, err, backend.ErrNotFound, backend.ErrRejected)
		case ge.Code >= 400 && ge.Code < 500 && ge.Code != http.StatusRequestTimeout && ge.Code != http.StatusTooManyRequests &&
			ge.Code != statusClientClosed:
			return fmt.Errorf("launching %s: %w: %w", job, backend.ErrRejected, err)
		}
	}
	return fmt.Errorf("launching %s (the execution may have started): %w", job, err)
}

// apiError wraps a Cloud Run error, mapping 404 to ErrNotFound.
func apiError(what string, err error) error {
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
		return fmt.Errorf("%s: %w: %w", what, backend.ErrNotFound, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Execution reads one execution by its full resource name.
func (b *Backend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	id, err := b.canonical(name)
	if err != nil {
		return backend.Execution{}, err
	}
	e, err := b.run.Projects.Locations.Jobs.Executions.Get(id.String()).Context(ctx).Do()
	if err != nil {
		return backend.Execution{}, apiError("reading execution "+id.String(), err)
	}
	return b.toExecution(e)
}

// List returns executions newest first: of every Fugaro job, or of f.Jobs.
func (b *Backend) List(ctx context.Context, f backend.ListFilter) ([]backend.Execution, error) {
	parents := []string{b.location() + "/jobs/-"}
	if len(f.Jobs) > 0 {
		parents = parents[:0]
		for _, j := range f.Jobs {
			parents = append(parents, b.location()+"/jobs/"+j)
		}
	}
	var out []backend.Execution
	for _, parent := range parents {
		got, err := b.list(ctx, parent, len(f.Jobs) == 0, f)
		if len(f.Jobs) > 0 && errors.Is(err, backend.ErrNotFound) {
			b.o.Warn("job " + parent + " does not exist; skipped")
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	if len(parents) > 1 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	}
	return out, nil
}

func (b *Backend) list(ctx context.Context, parent string, onlyFugaro bool, f backend.ListFilter) ([]backend.Execution, error) {
	var out []backend.Execution
	token := ""
	for {
		resp, err := b.run.Projects.Locations.Jobs.Executions.List(parent).PageSize(b.listPageSize).PageToken(token).Context(ctx).Do()
		if err != nil {
			return nil, apiError("listing executions of "+parent, err)
		}
		for _, e := range resp.Executions {
			// The API sorts by creation time, newest first, across every job
			// for the jobs/- wildcard too (confirmed by the live check):
			// everything after the first execution older than Since is
			// older too.
			if !f.Since.IsZero() {
				if c, err := parseTime(e.CreateTime); err == nil && !c.IsZero() && c.Before(f.Since) {
					return out, nil
				}
			}
			if id, ok := b.Codec().Parse(e.Name); onlyFugaro && (!ok || !strings.HasPrefix(id.Job, "fugaro-")) {
				continue
			}
			x, err := b.toExecution(e)
			if err != nil {
				return nil, err
			}
			if f.ActiveOnly && x.State.Terminal() {
				continue
			}
			out = append(out, x)
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		token = resp.NextPageToken
	}
}

// LongestTaskTimeout is the longest task timeout among the Fugaro workflow
// jobs of the region: one paged jobs.list. A job whose name doesn't start
// with "fugaro-" is skipped, which excludes the repositories' check jobs
// ("fugarochk-"). It is zero when there are no such jobs.
func (b *Backend) LongestTaskTimeout(ctx context.Context) (time.Duration, error) {
	var longest time.Duration
	token := ""
	for {
		resp, err := b.run.Projects.Locations.Jobs.List(b.location()).PageSize(b.listPageSize).PageToken(token).Context(ctx).Do()
		if err != nil {
			return 0, apiError("listing jobs", err)
		}
		for _, j := range resp.Jobs {
			if i := strings.LastIndex(j.Name, "/"); !strings.HasPrefix(j.Name[i+1:], "fugaro-") {
				continue
			}
			if j.Template == nil || j.Template.Template == nil || j.Template.Template.Timeout == "" {
				continue
			}
			d, err := time.ParseDuration(j.Template.Template.Timeout)
			if err != nil {
				return 0, fmt.Errorf("job %s: task timeout %q: %w", j.Name, j.Template.Template.Timeout, err)
			}
			longest = max(longest, d)
		}
		if resp.NextPageToken == "" {
			return longest, nil
		}
		token = resp.NextPageToken
	}
}

// TaskTimeout is the task timeout of the workflow's job, Cloud Run's default
// (backend.DefaultTaskTimeout) when the job sets none.
func (b *Backend) TaskTimeout(ctx context.Context, slug, workflow string) (time.Duration, error) {
	j, err := b.run.Projects.Locations.Jobs.Get(b.JobPath(slug, workflow)).Context(ctx).Do()
	if err != nil {
		return 0, apiError("reading job "+JobName(slug, workflow), err)
	}
	if j.Template == nil || j.Template.Template == nil || j.Template.Template.Timeout == "" {
		return backend.DefaultTaskTimeout, nil
	}
	d, err := time.ParseDuration(j.Template.Template.Timeout)
	if err != nil {
		return 0, fmt.Errorf("job %s: task timeout %q: %w", j.Name, j.Template.Template.Timeout, err)
	}
	return d, nil
}

// JobEnv reads job (a job name in the backend's region) and returns the
// value of its container's env variable name, and whether the job exists.
// A job without the variable gives "", true.
func (b *Backend) JobEnv(ctx context.Context, job, name string) (string, bool, error) {
	env, exists, err := b.JobFullEnv(ctx, job)
	if err != nil || !exists {
		return "", exists, err
	}
	return env[name], true, nil
}

// JobFullEnv reads job's whole container env in one Jobs.Get call, and
// whether the job exists. A job with no container env gives an empty, non-nil
// map, true.
//
// Only plain-value entries (ValueSource == nil) are included. deploy/terraform/gcp/modules/workflow/job.tf
// deploys every secret_env entry (CLAUDE_CODE_OAUTH_TOKEN,
// FUGARO_GITHUB_APP_PRIVATE_KEY, ANTHROPIC_API_KEY, a provider's key, ...)
// as an env var whose value comes from value_source.secret_key_ref, never a
// literal value: the API answers those with Value == "" and ValueSource set.
// A caller comparing this map against a config-derived one (never a secret)
// must not see those as present-but-empty, or every one of them reads as
// "removed" forever.
func (b *Backend) JobFullEnv(ctx context.Context, job string) (map[string]string, bool, error) {
	if !nameRE.MatchString(job) {
		return nil, false, fmt.Errorf("bad job name %q", job)
	}
	j, err := b.run.Projects.Locations.Jobs.Get(b.location() + "/jobs/" + job).Context(ctx).Do()
	if err != nil {
		err = apiError("reading job "+job, err)
		if errors.Is(err, backend.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	env := map[string]string{}
	if j.Template != nil && j.Template.Template != nil {
		for _, c := range j.Template.Template.Containers {
			for _, e := range c.Env {
				if e.ValueSource != nil {
					continue
				}
				env[e.Name] = e.Value
			}
		}
	}
	return env, true, nil
}

// JobSecretRefs reads job (a job name in the backend's region) and returns
// the secret ID each of its container's secret-backed env variables names,
// and whether the job exists. A job with no such env gives an empty, non-nil
// map, true.
//
// It is JobFullEnv's mirror: that one returns the plain-value entries, this
// one the entries whose value comes from value_source.secret_key_ref (the
// deployed job's mounted secrets, design docs/design/project-secrets.md §7),
// and neither reads a secret's value. The ID is the reference's own name
// with any projects/<p>/secrets/ prefix stripped, so a name written the same
// project's way (deploy/terraform/gcp/modules/workflow/job.tf) and one
// written fully qualified compare equal.
func (b *Backend) JobSecretRefs(ctx context.Context, job string) (map[string]string, bool, error) {
	if !nameRE.MatchString(job) {
		return nil, false, fmt.Errorf("bad job name %q", job)
	}
	j, err := b.run.Projects.Locations.Jobs.Get(b.location() + "/jobs/" + job).Context(ctx).Do()
	if err != nil {
		err = apiError("reading job "+job, err)
		if errors.Is(err, backend.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	refs := map[string]string{}
	if j.Template != nil && j.Template.Template != nil {
		for _, c := range j.Template.Template.Containers {
			for _, e := range c.Env {
				if e.ValueSource == nil || e.ValueSource.SecretKeyRef == nil {
					continue
				}
				refs[e.Name] = lastSegment(e.ValueSource.SecretKeyRef.Secret)
			}
		}
	}
	return refs, true, nil
}

// Cancel asks Cloud Run to stop the execution. It does not wait.
func (b *Backend) Cancel(ctx context.Context, name string) error {
	id, err := b.canonical(name)
	if err != nil {
		return err
	}
	if _, err := b.run.Projects.Locations.Jobs.Executions.Cancel(id.String(), &run.GoogleCloudRunV2CancelExecutionRequest{}).Context(ctx).Do(); err != nil {
		return apiError("cancelling execution "+id.String(), err)
	}
	return nil
}

// stateOf maps an execution's times and task counts to a State. An
// execution that ended without a succeeded task and with no failed or
// cancelled one is failed, never succeeded.
func stateOf(e *run.GoogleCloudRunV2Execution) backend.State {
	switch {
	case e.CompletionTime != "" && e.CancelledCount > 0:
		return backend.StateCancelled
	case e.CompletionTime != "" && e.SucceededCount > 0 && e.FailedCount == 0:
		return backend.StateSucceeded
	case e.CompletionTime != "":
		return backend.StateFailed
	case e.StartTime != "" || e.RunningCount > 0:
		return backend.StateRunning
	default:
		return backend.StatePending
	}
}

func (b *Backend) toExecution(e *run.GoogleCloudRunV2Execution) (backend.Execution, error) {
	id, err := b.canonical(e.Name)
	if err != nil {
		return backend.Execution{}, fmt.Errorf("Cloud Run returned an execution: %w", err)
	}
	x := backend.Execution{Name: id.String(), Job: id.Job, State: stateOf(e), Region: id.Region, LogURL: e.LogUri}
	// Anything but the name degrades: a bad time is zero, and bad limits
	// make the cost unknown (0) rather than failing every listing.
	for _, t := range []struct {
		dst  *time.Time
		src  string
		what string
	}{{&x.Created, e.CreateTime, "createTime"}, {&x.Started, e.StartTime, "startTime"}, {&x.Completed, e.CompletionTime, "completionTime"}} {
		var err error
		if *t.dst, err = parseTime(t.src); err != nil {
			b.o.Warn(fmt.Sprintf("execution %s: %s: %v", x.Name, t.what, err))
		}
	}
	x.LogURL = b.logURL(id, e.LogUri, x.Created)
	if e.Template != nil && len(e.Template.Containers) > 0 {
		for _, ev := range e.Template.Containers[0].Env {
			if ev.Name == "FUGARO_RUN" {
				x.Run = ev.Value
			}
		}
	}
	if e.Template != nil && len(e.Template.Containers) > 0 && e.Template.Containers[0].Resources != nil {
		limits := e.Template.Containers[0].Resources.Limits
		cpu, cerr := parseLimit(limits["cpu"], parseCPU)
		mem, merr := parseLimit(limits["memory"], backend.MemoryGiB)
		if err := errors.Join(cerr, merr); err != nil {
			b.o.Warn(fmt.Sprintf("execution %s: cost unknown: %v", x.Name, err))
		} else {
			x.CPU, x.MemoryGiB = cpu, mem
		}
	}
	return x, nil
}

// parseLimit parses a resource limit; an absent one is 0.
func parseLimit(s string, parse func(string) (float64, error)) (float64, error) {
	if s == "" {
		return 0, nil
	}
	return parse(s)
}

// parseTime parses an API timestamp; empty is the zero time.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", s, err)
	}
	return t, nil
}

// parseCPU parses a CPU limit: "4", "0.5" or millicores like "4000m".
func parseCPU(s string) (float64, error) {
	num, div := s, 1.0
	if n, ok := strings.CutSuffix(s, "m"); ok {
		num, div = n, 1000
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || !(v > 0) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("CPU limit %q must look like 4, 0.5 or 4000m", s)
	}
	return v / div, nil
}
