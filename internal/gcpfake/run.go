package gcpfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Run is a stateful fake of the Cloud Run Admin v2 calls Fugaro makes:
// jobs.run, and executions get, list (including across jobs/-) and cancel.
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

	mu    sync.Mutex
	jobs  map[string]*runJob
	execs map[execKey]*runExec
	seq   int
	ops   int
}

// RunCall is one jobs.run request.
type RunCall struct {
	Execution string // the short name, as Cloud Run hands CLOUD_RUN_EXECUTION to the container
	Job       string
	Env       map[string]string // the override's env
}

type runJob struct {
	cpu, memory string
	n           int
}

type execKey struct{ job, short string }

type runExec struct {
	key                         execKey
	seq                         int
	state                       backend.State
	created, started, completed time.Time
}

// NewRun starts a Cloud Run fake that lives until the test ends.
func NewRun(t *testing.T) *Run {
	t.Helper()
	f := &Run{jobs: map[string]*runJob{}, execs: map[execKey]*runExec{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddJob creates a job with the given CPU and memory limits.
func (f *Run) AddJob(name string, cpu, memory string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[name] = &runJob{cpu: cpu, memory: memory}
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
	return backend.ExecID{Project: f.project(project), Region: region, Job: k.job, Name: k.short}.String()
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
	e := map[string]any{
		"name":       f.name(project, region, x.key),
		"job":        x.key.job,
		"createTime": stamp(x.created),
		"taskCount":  1,
		"logUri": "https://console.cloud.google.com/run/jobs/executions/details/" + region + "/" + x.key.short +
			"/tasks?project=" + url.QueryEscape(f.project(project)),
		"template": map[string]any{"containers": []any{map[string]any{
			"image":     "fake/" + x.key.job,
			"resources": map[string]any{"limits": map[string]string{"cpu": j.cpu, "memory": j.memory}},
		}}},
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
			"name":     fmt.Sprintf("projects/%s/locations/%s/operations/%d", f.project(id.Project), id.Region, f.ops),
			"metadata": f.withType(f.render(id.Project, id.Region, x)),
		})
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/executions"):
		f.list(w, r, strings.TrimSuffix(p, "/executions"))
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
		writeJSON(w, http.StatusOK, f.render(id.Project, id.Region, x))
	default:
		f.unhandled(w, r)
	}
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
	if f.FailRunWith != 0 {
		writeError(w, f.FailRunWith, http.StatusText(f.FailRunWith), "injected failure")
		return
	}
	var req struct {
		Overrides struct {
			ContainerOverrides []struct {
				Env []struct{ Name, Value string } `json:"env"`
			} `json:"containerOverrides"`
		} `json:"overrides"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "bad body: "+err.Error())
		return
	}
	x := f.create(job)
	if x == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Resource '"+job+"' of kind 'JOB' in region '"+region+"' in project '"+project+"' does not exist.")
		return
	}
	env := map[string]string{}
	for _, c := range req.Overrides.ContainerOverrides {
		for _, e := range c.Env {
			env[e.Name] = e.Value
		}
	}
	if on := f.OnRun; on != nil {
		// Unlocked, so OnRun may call SetState and the other accessors.
		f.mu.Unlock()
		on(RunCall{Execution: x.key.short, Job: job, Env: env})
		f.mu.Lock()
	}
	f.ops++
	var meta any = f.withType(f.render(project, region, x))
	if f.BadRunMetadata {
		meta = "unreadable"
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": fmt.Sprintf("%s/operations/%d", jp, f.ops), "metadata": meta})
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
