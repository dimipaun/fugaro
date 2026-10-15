package gcpfake

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Batch is a stateful fake of the Cloud Batch v1 calls Fugaro makes: jobs
// create, get, list, cancel and delete. It implements no patch and no
// update, because Batch has none (design compute-backend.md §9): a job's
// labels and environment are fixed at creation, which is why the Batch
// backend creates one job per run.
//
// A created job starts QUEUED, which is what the real API reports for a job
// that has not been scheduled yet, and moves only through SetState and
// :cancel. Names it returns use ProjectNumber when set, else the project in
// the request path.
//
// Known differences from real Batch: a cancelled job jumps straight to
// CANCELLED, where the real API passes through CANCELLATION_IN_PROGRESS
// first (which of those states a cancelled job really passes through, and
// how long it sits in each, is UNVERIFIED — design §9, Check 57a step 7);
// cancelling a job that is already terminal answers 400
// FAILED_PRECONDITION, which is the fake's own choice and not documented
// (Check 57a step 7 settles it); and a job is never garbage-collected 60
// days after it finishes.
type Batch struct {
	*Server

	// Project and Region locate the names the fake returns, which no request
	// path supplies for a create; they default to "fake-project" and
	// "fake-region".
	Project, Region string
	// ProjectNumber, when set, replaces the project ID in every name the fake
	// returns, as the real API may.
	ProjectNumber string
	// FailCreateWith, when non-zero, makes jobs.create answer that HTTP
	// status, creating nothing.
	FailCreateWith int
	// AllowCancelOfTerminal, when set, makes :cancel of an already-terminal
	// job answer OK and change nothing. Unset (the default), it answers 400
	// FAILED_PRECONDITION, so the backend's tolerance of the race between a
	// read and a cancel is exercised rather than assumed.
	AllowCancelOfTerminal bool

	mu      sync.Mutex
	jobs    map[string]*batchJob // by job id
	order   []string             // job ids, oldest first
	creates []BatchCall
	cancels []string
	deletes []string
	seq     int
	ops     int
}

// BatchCall is one jobs.create request.
type BatchCall struct {
	JobID string         // the jobId query parameter
	Name  string         // the full resource name the fake answered with
	Job   map[string]any // the job definition as sent, decoded without loss
}

type batchJob struct {
	id      string
	uid     string
	state   string
	created time.Time
	body    map[string]any // the job as created, for get and list
}

// NewBatch starts a Cloud Batch fake that lives until the test ends.
func NewBatch(t *testing.T) *Batch {
	t.Helper()
	f := &Batch{jobs: map[string]*batchJob{}}
	f.Server = newServer(t, f.handle)
	return f
}

// jobNameRE is Batch's own job id rule: "at most 63 characters, must start
// with lowercase characters, only lowercase characters, numbers and - are
// accepted, and - cannot be the first or the last character" (design §2.4).
var jobNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)

func (f *Batch) project(p string) string {
	if f.ProjectNumber != "" {
		return f.ProjectNumber
	}
	return p
}

func (f *Batch) defProject() string {
	if f.Project == "" {
		return "fake-project"
	}
	return f.Project
}

func (f *Batch) defRegion() string {
	if f.Region == "" {
		return "fake-region"
	}
	return f.Region
}

// jobPath splits /v1/projects/<p>/locations/<r>/jobs[/<j>] into its parts.
// id is "" for the collection.
func batchJobPath(p string) (project, region, id string, ok bool) {
	f := strings.Split(p, "/")
	if len(f) < 5 || f[0] != "projects" || f[2] != "locations" || f[4] != "jobs" || f[1] == "" || f[3] == "" {
		return "", "", "", false
	}
	switch len(f) {
	case 5:
		return f[1], f[3], "", true
	case 6:
		if f[5] == "" {
			return "", "", "", false
		}
		return f[1], f[3], f[5], true
	}
	return "", "", "", false
}

// Creates returns every jobs.create request the fake answered, refused ones
// included, oldest first. A refused request has no Name.
func (f *Batch) Creates() []BatchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]BatchCall(nil), f.creates...)
}

// Cancels returns the ids :cancel was called for, in order. A refused call
// is not recorded: the API refused it, so nothing was cancelled.
func (f *Batch) Cancels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancels...)
}

// Deletes returns the ids jobs.delete was called for, in order.
func (f *Batch) Deletes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

// IDs returns every job's id, oldest first.
func (f *Batch) IDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

// State reports a job's raw Batch state ("QUEUED", "RUNNING", ...).
func (f *Batch) State(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil {
		f.t.Fatalf("gcpfake: State(%q): no such job", id)
	}
	return j.state
}

// SetState moves a job to the raw Batch state s, as the real API's own
// state machine would. It is how a test reaches a state the fake's create
// and cancel never produce, such as CANCELLATION_IN_PROGRESS or
// DELETION_IN_PROGRESS.
func (f *Batch) SetState(id, s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil {
		f.t.Fatalf("gcpfake: SetState(%q): no such job", id)
	}
	j.state = s
	if st, ok := j.body["status"].(map[string]any); ok {
		st["state"] = s
	}
}

// SetCreated back-dates (or post-dates) a job's create time, which the
// listing and jobs.list's filter read.
func (f *Batch) SetCreated(id string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil {
		f.t.Fatalf("gcpfake: SetCreated(%q): no such job", id)
	}
	j.created = t
	j.body["createTime"] = stamp(t)
}

// SetUID replaces a job's UID, which labels.job_uid selects its task logs by.
// The empty string is how a job without one is reached: the real API always
// reports a UID, but a client that reads one must not depend on it.
func (f *Batch) SetUID(id, uid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil {
		f.t.Fatalf("gcpfake: SetUID(%q): no such job", id)
	}
	j.uid = uid
}

// JobJSON is job id as jobs get would return it in project and region.
func (f *Batch) JobJSON(project, region, id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil {
		f.t.Fatalf("gcpfake: JobJSON(%q): no such job", id)
	}
	return f.render(project, region, j)
}

// render is a job's REST form; f.mu is held.
func (f *Batch) render(project, region string, j *batchJob) map[string]any {
	out := cloneJSON(j.body).(map[string]any)
	out["name"] = "projects/" + f.project(project) + "/locations/" + region + "/jobs/" + j.id
	out["uid"] = j.uid
	out["createTime"] = stamp(j.created)
	out["updateTime"] = stamp(j.created)
	st, _ := out["status"].(map[string]any)
	if st == nil {
		st = map[string]any{}
		out["status"] = st
	}
	st["state"] = j.state
	return out
}

func (f *Batch) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	p, ok := strings.CutPrefix(r.URL.Path, "/v1/")
	if !ok {
		f.unhandled(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(p, ":cancel"):
		f.cancel(w, r, strings.TrimSuffix(p, ":cancel"))
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/jobs"):
		f.create(w, r, p, body)
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/jobs"):
		f.list(w, r, p)
	case r.Method == http.MethodGet:
		f.get(w, r, p)
	case r.Method == http.MethodDelete:
		f.delete(w, r, p)
	default:
		// No patch, no update, no other call: Batch has none, so a client
		// that sends one is noticed here rather than in production.
		f.unhandled(w, r)
	}
}

func (f *Batch) create(w http.ResponseWriter, r *http.Request, parent string, body []byte) {
	project, region, id, ok := batchJobPath(parent)
	if !ok || id != "" {
		f.unhandled(w, r)
		return
	}
	job, err := decodeJSON(body)
	if err != nil {
		f.failf("gcpfake: jobs.create body: %v", err)
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	want := r.URL.Query().Get("jobId")
	if want == "" || !jobNameRE.MatchString(want) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("jobId %q is not a Batch job id", want))
		return
	}
	if n, _ := strconv.Atoi(r.URL.Query().Get("requestId")); n < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "bad requestId")
		return
	}
	call := BatchCall{JobID: want, Job: job}
	if f.FailCreateWith != 0 {
		f.creates = append(f.creates, call)
		writeError(w, f.FailCreateWith, http.StatusText(f.FailCreateWith), "injected failure")
		return
	}
	if _, dup := f.jobs[want]; dup {
		f.creates = append(f.creates, call)
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "job "+want+" already exists")
		return
	}
	if name, _ := job["name"].(string); name != "" && name != "projects/"+project+"/locations/"+region+"/jobs/"+want {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the body names "+name+", not the job in the URL")
		return
	}
	f.seq++
	j := &batchJob{id: want, uid: fmt.Sprintf("uid-%d", f.seq), state: "QUEUED", created: time.Now(), body: job}
	f.jobs[want] = j
	f.order = append(f.order, want)
	call.Name = "projects/" + f.project(project) + "/locations/" + region + "/jobs/" + want
	f.creates = append(f.creates, call)
	writeJSON(w, http.StatusOK, f.render(project, region, j))
}

func (f *Batch) get(w http.ResponseWriter, r *http.Request, p string) {
	project, region, id, ok := batchJobPath(p)
	if !ok || id == "" {
		f.unhandled(w, r)
		return
	}
	j := f.jobs[id]
	if j == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+id+" not found")
		return
	}
	writeJSON(w, http.StatusOK, f.render(project, region, j))
}

func (f *Batch) delete(w http.ResponseWriter, r *http.Request, p string) {
	project, region, id, ok := batchJobPath(p)
	if !ok || id == "" {
		f.unhandled(w, r)
		return
	}
	if f.jobs[id] == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+id+" not found")
		return
	}
	f.deletes = append(f.deletes, id)
	delete(f.jobs, id)
	f.order = slices.DeleteFunc(f.order, func(x string) bool { return x == id })
	f.ops++
	writeJSON(w, http.StatusOK, f.op(project, region, "projects/"+f.project(project)+"/locations/"+region+"/jobs/"+id))
}

// cancel answers jobs.cancel. The real API's answer for a job that has
// already finished is UNVERIFIED (design §9, Check 57a step 7): the fake
// refuses it unless AllowCancelOfTerminal says otherwise, which is what
// makes the backend's tolerance of that race a tested requirement.
func (f *Batch) cancel(w http.ResponseWriter, r *http.Request, p string) {
	project, region, id, ok := batchJobPath(p)
	if !ok || id == "" {
		f.unhandled(w, r)
		return
	}
	j := f.jobs[id]
	if j == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+id+" not found")
		return
	}
	if batchTerminal(j.state) {
		if !f.AllowCancelOfTerminal {
			writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "job "+id+" has already finished")
			return
		}
	} else {
		j.state = "CANCELLED"
	}
	if st, ok := j.body["status"].(map[string]any); ok {
		st["state"] = j.state
	}
	f.cancels = append(f.cancels, id)
	f.ops++
	writeJSON(w, http.StatusOK, f.op(project, region, "projects/"+f.project(project)+"/locations/"+region+"/jobs/"+id))
}

// batchTerminal reports whether a raw Batch state has finished, by the same
// mapping the backend uses (design §2.3). DELETION_IN_PROGRESS counts: it is
// only ever a job being deleted.
func batchTerminal(state string) bool {
	switch state {
	case "SUCCEEDED", "FAILED", "CANCELLED", "CANCELLATION_IN_PROGRESS", "DELETION_IN_PROGRESS":
		return true
	}
	return false
}

func (f *Batch) op(project, region, target string) map[string]any {
	return map[string]any{
		"name": fmt.Sprintf("projects/%s/locations/%s/operations/%d", f.project(project), region, f.ops),
		"done": true,
		"metadata": map[string]any{
			"@type":  "type.googleapis.com/google.cloud.batch.v1.OperationMetadata",
			"target": target,
		},
	}
}

// batchFilterRE is the one jobs.list filter shape the fake answers: a single
// label equality, as AIP-160 writes it. The backend sends the installation's
// marker, and its syntax against the real API is not among the facts design
// §9 verifies: the backend checks the labels client-side too, so the answer
// is right whether or not the server honours the filter. Any other filter
// fails the test rather than being silently ignored.
var batchFilterRE = regexp.MustCompile(`^labels\.([a-z0-9_-]+)\s*=\s*"([^"]*)"$`)

func (f *Batch) list(w http.ResponseWriter, r *http.Request, parent string) {
	project, region, id, ok := batchJobPath(parent)
	if !ok || id != "" {
		f.unhandled(w, r)
		return
	}
	q := r.URL.Query()
	var wantKey, wantVal string
	if filter := q.Get("filter"); filter != "" {
		m := batchFilterRE.FindStringSubmatch(filter)
		if m == nil {
			f.failf("gcpfake: jobs.list filter the fake cannot answer: %q", filter)
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported filter")
			return
		}
		wantKey, wantVal = m[1], m[2]
	}
	var all []*batchJob
	for _, x := range f.order {
		j := f.jobs[x]
		if j == nil {
			continue
		}
		if wantKey != "" {
			labels, _ := j.body["labels"].(map[string]any)
			if v, _ := labels[wantKey].(string); v != wantVal {
				continue
			}
		}
		all = append(all, j)
	}
	// Newest first, as the API orders jobs.list.
	sort.SliceStable(all, func(i, j int) bool { return all[i].created.After(all[j].created) })
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size <= 0 {
		size = 100
	}
	start, _ := strconv.Atoi(q.Get("pageToken"))
	start = min(max(start, 0), len(all))
	end := min(start+size, len(all))
	out := []any{}
	for _, j := range all[start:end] {
		out = append(out, f.render(project, region, j))
	}
	resp := map[string]any{"jobs": out}
	if end < len(all) {
		resp["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, resp)
}
