package gcpfake

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Run is a stateful fake of the Cloud Run Admin v2 calls Fugaro makes:
// jobs.run, jobs get, list and patch, operations get (of a patch), and
// executions get, list (including across jobs/-) and cancel.
//
// It stores executions by (job, short name), so it accepts a name in any
// form: full with the project ID, full with ProjectNumber, or short when the
// job is known from the path. Names it returns use ProjectNumber when set,
// else the project in the request path.
//
// Known differences from real Cloud Run: execution names end in a counter
// (<job>-1, <job>-2, …), not a random suffix; states change only through
// SetState and cancel; cancelling a finished execution answers 400
// FAILED_PRECONDITION, which the docs don't specify (the live test confirms).
type Run struct {
	*Server

	// OnRun, when set, is called synchronously for every successful :run.
	// Once the fake may be serving, set it with SetOnRun.
	OnRun func(c RunCall)
	// FailRunWith, when non-zero, makes :run answer that HTTP status
	// without creating anything.
	FailRunWith int
	// ProjectNumber, when set, replaces the project ID in every name the
	// fake returns, as the real API may.
	ProjectNumber string
	// BadRunMetadata makes a successful :run (the execution is created)
	// answer with operation metadata the client can't read.
	BadRunMetadata bool
	// Project and Region locate the names Start returns, which no request
	// path supplies; they default to "fake-project" and "fake-region".
	Project, Region string

	mu       sync.Mutex
	requests []RunCall
	jobs     map[string]*runJob
	execs    map[execKey]*runExec
	seq      int
	ops      int
	patches  []map[string]any
	faults   JobFaults
	pending  map[string]*runOp // a patch's operations by name
}

// JobFaults inject failures into jobs get and patch and into a patch's
// operation. The zero value injects none.
type JobFaults struct {
	// GetStatus and PatchStatus, when non-zero, make jobs get and jobs
	// patch answer that HTTP status, changing nothing.
	GetStatus, PatchStatus int
	// Polls is how many operations get answer not done before the patch's
	// operation is done; when it is non-zero the patch itself answers not
	// done, and the job changes only when the operation is done.
	Polls int
	// OpError, when set, ends the patch's operation with this error, and
	// the job is left unchanged.
	OpError string
}

type runOp struct {
	polls int    // operations get still to answer not done
	err   string // the operation's error when done
	apply func() // the patch's change, made when the operation is done without error
}

// done finishes op: its change is made unless it failed; f.mu is held.
func (op *runOp) done() {
	if op.err == "" && op.apply != nil {
		op.apply()
		op.apply = nil
	}
}

// RunCall is one jobs.run request.
type RunCall struct {
	Execution string // the short name, as Cloud Run hands CLOUD_RUN_EXECUTION to the container
	Job       string
	Env       map[string]string // the override's env
	Timeout   string            // the override's task timeout ("2820s"), empty when none
}

type runJob struct {
	cpu, memory string
	timeout     time.Duration // the task template's timeout; zero is unset
	labels      map[string]string
	image       string
	env         map[string]string // the container's env
	n           int
	etag        int
	// raw, when set, is the whole job as jobs get returns it (etag aside):
	// what SetJobJSON stored or a patch sent. It wins over the fields above
	// for jobs get.
	raw map[string]any
}

type execKey struct{ job, short string }

type runExec struct {
	key                         execKey
	seq                         int
	state                       backend.State
	timeout                     string            // the task timeout override the execution was created with
	env                         map[string]string // the override env the execution was created with
	created, started, completed time.Time
}

// NewRun starts a Cloud Run fake that lives until the test ends.
func NewRun(t *testing.T) *Run {
	t.Helper()
	f := &Run{jobs: map[string]*runJob{}, execs: map[execKey]*runExec{}, pending: map[string]*runOp{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddJob creates a job with the given CPU and memory limits.
func (f *Run) AddJob(name string, cpu, memory string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[name] = &runJob{cpu: cpu, memory: memory}
}

// SetJob creates job name, or updates it, with labels and its container's
// image, as jobs get reports them.
func (f *Run) SetJob(name string, labels map[string]string, image string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[name]
	if j == nil {
		j = &runJob{cpu: "1", memory: "512Mi"}
		f.jobs[name] = j
	}
	j.labels, j.image, j.raw = maps.Clone(labels), image, nil
}

// SetJobJSON creates job name, or replaces it, with body as the whole job
// jobs get returns (with the fake's etag in place of body's), every field
// and zero value kept, those the Go client doesn't model too. A patch then
// replaces the whole body, as Cloud Run's jobs.patch replaces the job.
// SetJob, SetJobEnv and SetJobTimeout drop the body.
func (f *Run) SetJobJSON(name, body string) {
	raw, err := decodeJSON([]byte(body))
	if err != nil {
		f.t.Fatalf("gcpfake: SetJobJSON(%q): %v", name, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[name]
	if j == nil {
		j = &runJob{cpu: "1", memory: "512Mi"}
		f.jobs[name] = j
	}
	j.raw = raw
}

// JobJSON is job name as jobs get would return it in project and region.
func (f *Run) JobJSON(project, region, name string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobs[name] == nil {
		f.t.Fatalf("gcpfake: JobJSON(%q): no such job", name)
	}
	return f.jobBody(project, region, name)
}

// SetJobFaults sets the failures jobs get and patch inject.
func (f *Run) SetJobFaults(j JobFaults) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults = j
}

// decodeJSON decodes a JSON object without loss: numbers stay json.Number.
func decodeJSON(b []byte) (map[string]any, error) {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	if d.More() {
		return nil, fmt.Errorf("trailing data after the object")
	}
	return m, nil
}

// cloneJSON deep-copies a decoded JSON value.
func cloneJSON(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = cloneJSON(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = cloneJSON(x)
		}
		return out
	default:
		return v
	}
}

// SetJobEnv sets the env of job's container, as jobs get reports it.
func (f *Run) SetJobEnv(job string, env map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[job]
	if j == nil {
		f.t.Fatalf("gcpfake: SetJobEnv(%q): no such job", job)
	}
	j.env, j.raw = maps.Clone(env), nil
}

// SetJobTimeout sets the task timeout the fake reports for job in jobs.list.
func (f *Run) SetJobTimeout(job string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[job]
	if j == nil {
		f.t.Fatalf("gcpfake: SetJobTimeout(%q): no such job", job)
	}
	j.timeout, j.raw = d, nil
}

// RunRequests returns every :run request the fake has answered, refused ones
// included, oldest first. A refused request has no Execution.
func (f *Run) RunRequests() []RunCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RunCall(nil), f.requests...)
}

// SetOnRun sets OnRun under the fake's lock. Use it instead of assigning
// OnRun when the fake is already serving, as when the requests come from a
// child process: the race detector can't see that such an assignment
// happened before them.
func (f *Run) SetOnRun(fn func(RunCall)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.OnRun = fn
}

// Start creates a pending execution of job directly, as another tool would,
// and returns its full name, in Project (or ProjectNumber) and Region.
func (f *Run) Start(job string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.create(job)
	if x == nil {
		f.t.Fatalf("gcpfake: Start(%q): no such job", job)
	}
	project, region := f.Project, f.Region
	if project == "" {
		project = "fake-project"
	}
	if region == "" {
		region = "fake-region"
	}
	return f.name(project, region, x.key)
}

// StartWithEnv is Start with the override env a launch would have set
// (FUGARO_RUN, say), which the execution then reports in its template.
func (f *Run) StartWithEnv(job string, env map[string]string) string {
	name := f.Start(job)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookup(name, "").env = env
	return name
}

// create adds an execution of job; f.mu is held. It is nil for an unknown job.
func (f *Run) create(job string) *runExec {
	j := f.jobs[job]
	if j == nil {
		return nil
	}
	j.n++
	f.seq++
	x := &runExec{key: execKey{job, fmt.Sprintf("%s-%d", job, j.n)}, seq: f.seq, state: backend.StatePending, created: time.Now()}
	f.execs[x.key] = x
	return x
}

// SetState moves an execution (named in any form) to s, setting its start
// and completion times as Cloud Run would.
func (f *Run) SetState(exec string, s backend.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.lookup(exec, "")
	if x == nil {
		f.t.Fatalf("gcpfake: SetState(%q): no such execution", exec)
	}
	x.set(s)
}

// SetCreated back-dates (or post-dates) an execution's create time. The
// listing still orders by creation sequence, so a test can build a
// wildcard listing that isn't sorted by create time across jobs.
func (f *Run) SetCreated(exec string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.lookup(exec, "")
	if x == nil {
		f.t.Fatalf("gcpfake: SetCreated(%q): no such execution", exec)
	}
	x.created = t
}

// State reports an execution's state.
func (f *Run) State(exec string) backend.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.lookup(exec, "")
	if x == nil {
		f.t.Fatalf("gcpfake: State(%q): no such execution", exec)
	}
	return x.state
}

// Executions returns the short names of every execution, oldest first.
func (f *Run) Executions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.sorted("")
	out := make([]string, len(all))
	for i, x := range all {
		out[len(all)-1-i] = x.key.short
	}
	return out
}

func (x *runExec) set(s backend.State) {
	now := time.Now()
	x.state = s
	if s != backend.StatePending && x.started.IsZero() {
		x.started = now
	}
	if s.Terminal() && x.completed.IsZero() {
		x.completed = now
	}
	if !s.Terminal() {
		x.completed = time.Time{}
	}
	if s == backend.StatePending {
		x.started = time.Time{}
	}
}

// lookup finds an execution by full name, or by short name within job (any
// job when job is empty); f.mu is held.
func (f *Run) lookup(name, job string) *runExec {
	if id, ok := backend.ParseExecution(name); ok {
		return f.execs[execKey{id.Job, id.Name}]
	}
	if strings.Contains(name, "/") {
		return nil
	}
	if job != "" {
		return f.execs[execKey{job, name}]
	}
	var found *runExec
	for k, x := range f.execs {
		if k.short == name {
			if found != nil {
				f.t.Fatalf("gcpfake: short execution name %q is ambiguous", name)
			}
			found = x
		}
	}
	return found
}

// sorted returns job's executions (every job's when job is "-" or ""),
// newest first; f.mu is held.
func (f *Run) sorted(job string) []*runExec {
	var out []*runExec
	for _, x := range f.execs {
		if job == "" || job == "-" || x.key.job == job {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	return out
}

func (f *Run) project(p string) string {
	if f.ProjectNumber != "" {
		return f.ProjectNumber
	}
	return p
}

func (f *Run) name(project, region string, k execKey) string {
	return backend.ExecID{GCPProject: f.project(project), Region: region, Job: k.job, Name: k.short}.String()
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// render is the execution's REST form; f.mu is held.
func (f *Run) render(project, region string, x *runExec) map[string]any {
	j := f.jobs[x.key.job]
	container := map[string]any{
		"image":     "fake/" + x.key.job,
		"resources": map[string]any{"limits": map[string]string{"cpu": j.cpu, "memory": j.memory}},
	}
	if len(x.env) > 0 {
		var env []map[string]string
		for k, v := range x.env {
			env = append(env, map[string]string{"name": k, "value": v})
		}
		container["env"] = env
	}
	e := map[string]any{
		"name":       f.name(project, region, x.key),
		"job":        x.key.job,
		"createTime": stamp(x.created),
		"taskCount":  1,
		"logUri": "https://console.cloud.google.com/run/jobs/executions/details/" + region + "/" + x.key.short +
			"/tasks?project=" + url.QueryEscape(f.project(project)),
		"template": map[string]any{"containers": []any{container}},
	}
	if s := stamp(x.started); s != "" {
		e["startTime"] = s
	}
	if s := stamp(x.completed); s != "" {
		e["completionTime"] = s
	}
	switch x.state {
	case backend.StateRunning:
		e["runningCount"] = 1
	case backend.StateSucceeded:
		e["succeededCount"] = 1
	case backend.StateFailed:
		e["failedCount"] = 1
	case backend.StateCancelled:
		e["cancelledCount"] = 1
	}
	return e
}

// jobPath parses projects/<p>/locations/<r>/jobs/<j>.
func jobPath(p string) (project, region, job string, ok bool) {
	f := strings.Split(p, "/")
	if len(f) != 6 || f[0] != "projects" || f[2] != "locations" || f[4] != "jobs" || f[1] == "" || f[3] == "" || f[5] == "" {
		return "", "", "", false
	}
	return f[1], f[3], f[5], true
}

func (f *Run) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	p, ok := strings.CutPrefix(r.URL.Path, "/v2/")
	if !ok {
		f.unhandled(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(p, ":run"):
		f.run(w, r, strings.TrimSuffix(p, ":run"), body)
	case r.Method == http.MethodPost && strings.HasSuffix(p, ":cancel"):
		id, ok := backend.ParseExecution(strings.TrimSuffix(p, ":cancel"))
		if !ok {
			f.unhandled(w, r)
			return
		}
		x := f.execs[execKey{id.Job, id.Name}]
		if x == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "execution not found")
			return
		}
		if x.state.Terminal() {
			// The docs don't say; FAILED_PRECONDITION is the API's usual
			// answer for a finished resource. The live test confirms it.
			writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "execution "+id.Name+" has already completed")
			return
		}
		x.set(backend.StateCancelled)
		f.ops++
		writeJSON(w, http.StatusOK, map[string]any{
			"name":     fmt.Sprintf("projects/%s/locations/%s/operations/%d", f.project(id.GCPProject), id.Region, f.ops),
			"metadata": f.withType(f.render(id.GCPProject, id.Region, x)),
		})
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/jobs"):
		f.listJobs(w, r, strings.TrimSuffix(p, "/jobs"))
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/executions"):
		f.list(w, r, strings.TrimSuffix(p, "/executions"))
	case r.Method == http.MethodPatch && isJobPath(p):
		f.patchJob(w, r, p, body)
	case r.Method == http.MethodGet && isJobPath(p):
		f.getJob(w, p)
	case r.Method == http.MethodGet && strings.Contains(p, "/operations/"):
		f.getOp(w, p)
	case r.Method == http.MethodGet:
		id, ok := backend.ParseExecution(p)
		if !ok {
			f.unhandled(w, r)
			return
		}
		x := f.execs[execKey{id.Job, id.Name}]
		if x == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "execution not found")
			return
		}
		writeJSON(w, http.StatusOK, f.render(id.GCPProject, id.Region, x))
	default:
		f.unhandled(w, r)
	}
}

func isJobPath(p string) bool {
	_, _, job, ok := jobPath(p)
	return ok && job != "-"
}

// getJob answers jobs get: the stored body when there is one, else the
// job's labels and its template (the container's image and limits, and the
// task timeout when set).
func (f *Run) getJob(w http.ResponseWriter, p string) {
	project, region, name, _ := jobPath(p)
	if f.faults.GetStatus != 0 {
		writeError(w, f.faults.GetStatus, http.StatusText(f.faults.GetStatus), "injected failure")
		return
	}
	if f.jobs[name] == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Resource '"+name+"' of kind 'JOB' in region '"+region+"' in project '"+project+"' does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, f.jobBody(project, region, name))
}

// jobBody is job name's jobs get answer; f.mu is held.
func (f *Run) jobBody(project, region, name string) map[string]any {
	j := f.jobs[name]
	if j.raw != nil {
		out := cloneJSON(j.raw).(map[string]any)
		out["etag"] = fmt.Sprintf("e%d", j.etag)
		return out
	}
	container := map[string]any{
		"image":     j.image,
		"resources": map[string]any{"limits": map[string]any{"cpu": j.cpu, "memory": j.memory}},
	}
	if len(j.env) > 0 {
		var env []any
		for _, k := range slices.Sorted(maps.Keys(j.env)) {
			env = append(env, map[string]any{"name": k, "value": j.env[k]})
		}
		container["env"] = env
	}
	task := map[string]any{"containers": []any{container}, "maxRetries": 0}
	if j.timeout > 0 {
		task["timeout"] = fmt.Sprintf("%ds", int(j.timeout/time.Second))
	}
	out := map[string]any{"name": "projects/" + f.project(project) + "/locations/" + region + "/jobs/" + name,
		"template": map[string]any{"taskCount": 1, "template": task}}
	if len(j.labels) > 0 {
		out["labels"] = maps.Clone(j.labels)
	}
	out["etag"] = fmt.Sprintf("e%d", j.etag)
	return out
}

// Patches are the bodies of the jobs patch requests the fake accepted.
func (f *Run) Patches() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.patches)
}

// patchJob is jobs.patch: the whole job comes back, carrying the etag jobs
// get gave; a stale one is refused (409 ABORTED), as the real API refuses a
// conflicting update. The body replaces the whole stored job, as the real
// API replaces the job (it has no update mask), so a field the body leaves
// out is gone from the next jobs get. A body whose name is another job's
// and any updateMask are refused (400): the client sends the whole job under
// its own name and never a mask.
func (f *Run) patchJob(w http.ResponseWriter, r *http.Request, p string, body []byte) {
	project, region, name, _ := jobPath(p)
	if f.faults.PatchStatus != 0 {
		writeError(w, f.faults.PatchStatus, http.StatusText(f.faults.PatchStatus), "injected failure")
		return
	}
	if r.URL.Query().Has("updateMask") {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "updateMask is not accepted: jobs.patch takes the whole job")
		return
	}
	j := f.jobs[name]
	if j == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+name+" not found")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Etag     string `json:"etag"`
		Template struct {
			Template struct {
				Containers []struct {
					Image string `json:"image"`
					Env   []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"containers"`
			} `json:"template"`
		} `json:"template"`
	}
	raw, err := decodeJSON(body)
	if err != nil || json.Unmarshal(body, &in) != nil || len(in.Template.Template.Containers) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "not a job")
		return
	}
	// A name in the body is the URL's job (by project id or number).
	if in.Name != "" && in.Name != "projects/"+project+"/locations/"+region+"/jobs/"+name &&
		in.Name != "projects/"+f.project(project)+"/locations/"+region+"/jobs/"+name {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the body names "+in.Name+", not the job in the URL")
		return
	}
	if in.Etag != fmt.Sprintf("e%d", j.etag) {
		writeError(w, http.StatusConflict, "ABORTED", "the job was modified since it was read (etag mismatch)")
		return
	}
	f.patches = append(f.patches, cloneJSON(raw).(map[string]any))
	f.ops++
	opName := fmt.Sprintf("projects/%s/locations/%s/operations/%d", f.project(project), region, f.ops)
	op := &runOp{polls: f.faults.Polls, err: f.faults.OpError, apply: func() {
		c := in.Template.Template.Containers[0]
		j.image = c.Image
		j.env = map[string]string{}
		for _, e := range c.Env {
			j.env[e.Name] = e.Value
		}
		delete(raw, "etag")
		j.raw = raw
		j.etag++
	}}
	f.pending[opName] = op
	if op.polls > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"name": opName})
		return
	}
	op.done()
	writeJSON(w, http.StatusOK, opBody(opName, op))
}

// getOp is operations get of a patch's operation: not done for its polls,
// then done, with its error if any.
func (f *Run) getOp(w http.ResponseWriter, p string) {
	op := f.pending[p]
	if op == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "operation not found")
		return
	}
	if op.polls > 0 {
		op.polls--
		writeJSON(w, http.StatusOK, map[string]any{"name": p})
		return
	}
	op.done()
	writeJSON(w, http.StatusOK, opBody(p, op))
}

func opBody(name string, op *runOp) map[string]any {
	out := map[string]any{"name": name, "done": true}
	if op.err != "" {
		out["error"] = map[string]any{"code": 3, "message": op.err}
	}
	return out
}

func (f *Run) withType(e map[string]any) map[string]any {
	e["@type"] = "type.googleapis.com/google.cloud.run.v2.Execution"
	return e
}

func (f *Run) run(w http.ResponseWriter, r *http.Request, jp string, body []byte) {
	project, region, job, ok := jobPath(jp)
	if !ok {
		f.unhandled(w, r)
		return
	}
	var req struct {
		Overrides struct {
			ContainerOverrides []struct {
				Env []struct{ Name, Value string } `json:"env"`
			} `json:"containerOverrides"`
			Timeout string `json:"timeout"`
		} `json:"overrides"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "bad body: "+err.Error())
		return
	}
	env := map[string]string{}
	for _, c := range req.Overrides.ContainerOverrides {
		for _, e := range c.Env {
			env[e.Name] = e.Value
		}
	}
	call := RunCall{Job: job, Env: env, Timeout: req.Overrides.Timeout}
	if f.FailRunWith != 0 {
		f.requests = append(f.requests, call)
		writeError(w, f.FailRunWith, http.StatusText(f.FailRunWith), "injected failure")
		return
	}
	x := f.create(job)
	if x == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Resource '"+job+"' of kind 'JOB' in region '"+region+"' in project '"+project+"' does not exist.")
		return
	}
	x.timeout = req.Overrides.Timeout
	x.env = env
	call.Execution = x.key.short
	f.requests = append(f.requests, call)
	if on := f.OnRun; on != nil {
		// Unlocked, so OnRun may call SetState and the other accessors.
		f.mu.Unlock()
		on(call)
		f.mu.Lock()
	}
	f.ops++
	var meta any = f.withType(f.render(project, region, x))
	if f.BadRunMetadata {
		meta = "unreadable"
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": fmt.Sprintf("%s/operations/%d", jp, f.ops), "metadata": meta})
}

// listJobs answers jobs.list of a location, sorted by name, paged like
// executions.list. Each job carries its task template's timeout when set.
func (f *Run) listJobs(w http.ResponseWriter, r *http.Request, parent string) {
	f0 := strings.Split(parent, "/")
	if len(f0) != 4 || f0[0] != "projects" || f0[2] != "locations" {
		f.unhandled(w, r)
		return
	}
	names := make([]string, 0, len(f.jobs))
	for n := range f.jobs {
		names = append(names, n)
	}
	sort.Strings(names)
	q := r.URL.Query()
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size <= 0 {
		size = 100
	}
	start, _ := strconv.Atoi(q.Get("pageToken"))
	start = min(max(start, 0), len(names))
	end := min(start+size, len(names))
	var out []any
	for _, n := range names[start:end] {
		job := map[string]any{"name": parent + "/jobs/" + n}
		if d := f.jobs[n].timeout; d > 0 {
			job["template"] = map[string]any{"template": map[string]any{"timeout": fmt.Sprintf("%ds", int(d/time.Second))}}
		}
		out = append(out, job)
	}
	resp := map[string]any{"jobs": out}
	if end < len(names) {
		resp["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (f *Run) list(w http.ResponseWriter, r *http.Request, parent string) {
	project, region, job, ok := jobPath(parent)
	if !ok {
		f.unhandled(w, r)
		return
	}
	if job != "-" && f.jobs[job] == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job not found")
		return
	}
	all := f.sorted(job)
	q := r.URL.Query()
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size <= 0 {
		size = 100
	}
	start, _ := strconv.Atoi(q.Get("pageToken"))
	start = min(max(start, 0), len(all))
	end := min(start+size, len(all))
	var out []any
	for _, x := range all[start:end] {
		out = append(out, f.render(project, region, x))
	}
	resp := map[string]any{"executions": out}
	if end < len(all) {
		resp["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, resp)
}
