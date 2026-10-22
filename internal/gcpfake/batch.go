package gcpfake

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
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
// A job is stored under the project, region and id its create named, because
// that is what the API scopes one to: a get, cancel, delete or list naming
// another project or region is NOT_FOUND (or empty), never the job. A fake
// that keyed jobs by id alone would be more permissive than the API, and a
// backend that lost the project or region on the way to a request would pass
// every test.
//
// A created job starts QUEUED, which is what the real API reports for a job
// that has not been scheduled yet, and moves only through SetState and
// :cancel. Names it returns use ProjectNumber when set, else the project in
// the request path; ProjectNumber is a rendering knob only -- the fake still
// stores the job under the project the request named, so a name it rendered
// with a number is not a name it can be read back by.
//
// Known differences from real Batch: a cancelled job jumps straight to
// CANCELLED, where the real API passes through CANCELLATION_IN_PROGRESS
// first (which of those states a cancelled job really passes through, and
// how long it sits in each, is UNVERIFIED — design §9, Check 57a step 7);
// cancelling a job that is already terminal answers 400
// FAILED_PRECONDITION, which is the fake's own choice and not documented
// (Check 57a step 7 settles it); a job is never garbage-collected 60
// days after it finishes; cancel and delete answer an Operation that is
// already done, where the real API's may take time (Check 57a steps 7 and
// 8); and jobs.list answers in creation order, oldest first, where the
// reference documents no default order at all -- deliberately not the
// newest-first order a caller might assume, so that a caller which needs an
// order sorts for itself (internal/backend/batch.List does).
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
	// FailDeleteWith, when non-zero, makes jobs.delete answer that HTTP
	// status, deleting nothing (a permission refusal, say). It is separate
	// from RefusePath because a delete shares its path with the read that
	// precedes it, so a path refusal would refuse the read as well and the
	// delete's own failure could not be reached.
	FailDeleteWith int
	// GetRace, keyed by job id, moves the job to that raw state as a
	// single-job :get arrives, before the get is answered: the read-back a
	// caller makes between a listing it decided from and the call that
	// decision leads to, for a job whose state moved in between. A job's
	// listing and its read-back read the same stored state otherwise, so this
	// is the only way a test reaches that race (CancelRace is the same knob
	// for :cancel, which has the same problem).
	GetRace map[string]string
	// AllowCancelOfTerminal, when set, makes :cancel of an already-terminal
	// job answer OK and change nothing. Unset (the default), it answers 400
	// FAILED_PRECONDITION, so the backend's tolerance of the race between a
	// read and a cancel is exercised rather than assumed.
	AllowCancelOfTerminal bool
	// Unreachable, when non-empty, is the locations jobs.list reports it could
	// not reach (ListJobsResponse.Unreachable): the shape of a partial listing.
	// A caller that treats "the job is not in the listing" as "the job is not
	// there" must notice it.
	Unreachable []string
	// IgnoreFilter makes jobs.list answer every job it holds whatever its
	// filter was, as a server that does not honour a label filter would. It is
	// how a caller's own client-side marker check is exercised: whether the
	// API honours the filter or ignores it, the answer must be the same (the
	// compute fake has the same knob, for the same reason). Unset, the fake
	// applies the filter, as the API documents it.
	IgnoreFilter bool
	// CancelGone, keyed by job id, makes :cancel answer 404 as if the job had
	// been removed between the listing that decided to cancel it and the call
	// itself -- someone else's delete, or Batch's own 60-day one (§9). The job
	// stays in the listing (which is read before it vanishes), so the same
	// knob is the only way a test reaches a cancel whose job is gone: a
	// listing and a cancel read the same stored map otherwise.
	CancelGone map[string]bool
	// CancelRace, keyed by job id, moves the job to that raw state as its
	// :cancel arrives, before the cancel is answered: a job that started or
	// finished between the listing that decided to cancel it and the call
	// itself, which the real API refuses. It is the only way a test reaches
	// that race -- a listing and a cancel read the same stored state -- and it
	// is what makes the reaper's read-back tolerance a tested requirement
	// rather than an assumption (design §2.3).
	CancelRace map[string]string

	mu      sync.Mutex
	jobs    map[jobKey]*batchJob // by project, region and id
	order   []jobKey             // oldest first
	creates []BatchCall
	cancels []string
	deletes []string
	seq     int
	ops     int
}

// jobKey is what a job is stored by: the API scopes a job to its project and
// region, so a name that differs in either is another job, or none.
type jobKey struct{ project, region, id string }

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
	f := &Batch{jobs: map[jobKey]*batchJob{}}
	f.Server = newServer(t, f.handle)
	return f
}

// jobNameRE is Batch's own job id rule: "at most 63 characters, must start
// with lowercase characters, only lowercase characters, numbers and - are
// accepted, and - cannot be the first or the last character" (design §2.4).
// The first character is a letter, not a digit ("must start with lowercase
// characters"), and the last is a letter or a digit; one character is a valid
// id, so the middle part is optional. A fake that accepted a leading digit,
// or refused a one-character id, would disagree with the API in both
// directions.
var jobNameRE = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// requestIDRE is the form jobs.create's requestId must have: "A unique
// identifier for this request. Must be a valid UUID." (Batch REST reference).
// Nothing Fugaro sends sets one -- the backend sends no requestId -- so this
// only refuses a caller that invents a different form.
var requestIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// find is the job with this id, in whichever project and region holds it;
// f.mu is held. The accessors below take an id alone because a test knows the
// job it created, and they fail the test rather than pick one when two
// projects hold the same id.
func (f *Batch) find(id string) *batchJob {
	var found *batchJob
	for k, j := range f.jobs {
		if k.id != id {
			continue
		}
		if found != nil {
			f.t.Fatalf("gcpfake: job id %q is not unique: it exists in more than one project or region", id)
		}
		found = j
	}
	return found
}

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
	out := make([]string, 0, len(f.order))
	for _, k := range f.order {
		out = append(out, k.id)
	}
	return out
}

// State reports a job's raw Batch state ("QUEUED", "RUNNING", ...).
func (f *Batch) State(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.find(id)
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
	j := f.find(id)
	if j == nil {
		f.t.Fatalf("gcpfake: SetState(%q): no such job", id)
	}
	j.state = s
	if st, ok := j.body["status"].(map[string]any); ok {
		st["state"] = s
	}
}

// SetStateAt is SetState plus the job's status events: the job reports one
// job-level STATUS_CHANGED event at at, which is when it entered the state and
// therefore the only thing a per-state deadline can be measured from (the
// reaper's stateSince). SetState alone leaves the events as they were, so a
// test that wants a job whose time in state is unknown -- no event, no
// runDuration -- writes that too.
func (f *Batch) SetStateAt(id, s string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.find(id)
	if j == nil {
		f.t.Fatalf("gcpfake: SetStateAt(%q): no such job", id)
	}
	j.state = s
	st, _ := j.body["status"].(map[string]any)
	if st == nil {
		st = map[string]any{}
		j.body["status"] = st
	}
	st["state"] = s
	st["statusEvents"] = []any{map[string]any{
		"type":        "STATUS_CHANGED",
		"description": "Job state is " + s,
		"eventTime":   stamp(at),
	}}
}

// SetStatusEvents replaces a job's status events, as jobs.get would report
// them: the shape a test needs to reach a job whose events date something other
// than its current state (a task-level event, or none at all).
func (f *Batch) SetStatusEvents(id string, events ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.find(id)
	if j == nil {
		f.t.Fatalf("gcpfake: SetStatusEvents(%q): no such job", id)
	}
	st, _ := j.body["status"].(map[string]any)
	if st == nil {
		st = map[string]any{}
		j.body["status"] = st
	}
	list := make([]any, 0, len(events))
	for _, e := range events {
		list = append(list, e)
	}
	st["statusEvents"] = list
}

// SetRunDuration sets the runDuration a RUNNING job reports, the fallback a
// client that finds no event time reads (batch/v1.JobStatus).
func (f *Batch) SetRunDuration(id, d string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.find(id)
	if j == nil {
		f.t.Fatalf("gcpfake: SetRunDuration(%q): no such job", id)
	}
	st, _ := j.body["status"].(map[string]any)
	if st == nil {
		st = map[string]any{}
		j.body["status"] = st
	}
	st["runDuration"] = d
}

// SetCreated back-dates (or post-dates) a job's create time, which the
// listing and jobs.list's filter read.
func (f *Batch) SetCreated(id string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.find(id)
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
	j := f.find(id)
	if j == nil {
		f.t.Fatalf("gcpfake: SetUID(%q): no such job", id)
	}
	j.uid = uid
}

// JobJSON is job id as jobs get would return it in project and region.
func (f *Batch) JobJSON(project, region, id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.find(id)
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
	// requestId is documented as a UUID; a caller that sends another form is
	// refused, as the API refuses it.
	if rid := r.URL.Query().Get("requestId"); rid != "" && !requestIDRE.MatchString(rid) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("requestId %q is not a UUID", rid))
		return
	}
	call := BatchCall{JobID: want, Job: job}
	if f.FailCreateWith != 0 {
		f.creates = append(f.creates, call)
		writeError(w, f.FailCreateWith, http.StatusText(f.FailCreateWith), "injected failure")
		return
	}
	k := jobKey{project, region, want}
	if _, dup := f.jobs[k]; dup {
		f.creates = append(f.creates, call)
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "job "+want+" already exists")
		return
	}
	// Job.name is output-only, so the real API ignores one in the body
	// rather than refusing it: this refusal is the fake's own choice, and it
	// errs strict (a client that passes here passes live). No Check 57a step
	// covers it -- it is a create-time field, not a job's behaviour.
	if name, _ := job["name"].(string); name != "" && name != "projects/"+project+"/locations/"+region+"/jobs/"+want {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the body names "+name+", not the job in the URL")
		return
	}
	f.seq++
	j := &batchJob{id: want, uid: fmt.Sprintf("uid-%d", f.seq), state: "QUEUED", created: time.Now(), body: job}
	f.jobs[k] = j
	f.order = append(f.order, k)
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
	j := f.jobs[jobKey{project, region, id}]
	if j == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+id+" not found")
		return
	}
	if raced := f.GetRace[id]; raced != "" {
		j.state = raced
		if st, ok := j.body["status"].(map[string]any); ok {
			st["state"] = raced
		}
	}
	writeJSON(w, http.StatusOK, f.render(project, region, j))
}

func (f *Batch) delete(w http.ResponseWriter, r *http.Request, p string) {
	project, region, id, ok := batchJobPath(p)
	if !ok || id == "" {
		f.unhandled(w, r)
		return
	}
	k := jobKey{project, region, id}
	if f.jobs[k] == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+id+" not found")
		return
	}
	if f.FailDeleteWith != 0 {
		writeError(w, f.FailDeleteWith, http.StatusText(f.FailDeleteWith), "injected failure")
		return
	}
	f.deletes = append(f.deletes, id)
	delete(f.jobs, k)
	f.order = slices.DeleteFunc(f.order, func(x jobKey) bool { return x == k })
	f.ops++
	writeJSON(w, http.StatusOK, f.op(project, region, "projects/"+f.project(project)+"/locations/"+region+"/jobs/"+id))
}

// cancel answers jobs.cancel. The real API's answer for a job that has
// already finished is UNVERIFIED (design §9, Check 57a step 7): the fake
// refuses it unless AllowCancelOfTerminal says otherwise, which is what
// makes the backend's tolerance of that race a tested requirement.
//
// The Operation it answers is already done, where the real one may take time
// to reach DELETION_IN_PROGRESS and tear the job's VMs down (Check 57a steps
// 7 and 8 settle what it really answers and how long it takes). The backend
// reads the job back rather than the operation, so the difference is not
// visible to it. CancelRace moves the job before the answer, which is the one
// thing a test cannot otherwise arrange: the listing that decided to cancel
// the job read the state this request is about to change.
func (f *Batch) cancel(w http.ResponseWriter, r *http.Request, p string) {
	project, region, id, ok := batchJobPath(p)
	if !ok || id == "" {
		f.unhandled(w, r)
		return
	}
	j := f.jobs[jobKey{project, region, id}]
	if j == nil || f.CancelGone[id] {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job "+id+" not found")
		return
	}
	if raced := f.CancelRace[id]; raced != "" {
		j.state = raced
		if st, ok := j.body["status"].(map[string]any); ok {
			st["state"] = raced
		}
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
	if f.IgnoreFilter {
		// A server that did not honour the filter answers every job it holds:
		// the shape a caller's own client-side check has to survive.
		wantKey, wantVal = "", ""
	}
	var all []*batchJob
	for _, k := range f.order {
		// The listing is per region and per project: a job created under
		// another one is not in this answer, as the API's own parent scopes
		// it.
		if k.project != project || k.region != region {
			continue
		}
		j := f.jobs[k]
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
	// Creation order, oldest first. The reference documents no default order
	// for jobs.list (unlike the resource's own create time, which it does
	// document), so this is the fake's choice and not a fact: a caller that
	// needs an order must sort for itself, which is what
	// internal/backend/batch.List does. Returning the oldest first, rather
	// than the newest first a caller might assume, is what makes an
	// unsorted caller fail here instead of in production. No Check 57a step
	// measures it.
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
	if len(f.Unreachable) > 0 {
		// jobs.list answers the locations it could not reach alongside the
		// jobs it did: a caller that reads an unreachable listing as a complete
		// one is reading absence as fact.
		resp["unreachable"] = f.Unreachable
	}
	writeJSON(w, http.StatusOK, resp)
}
