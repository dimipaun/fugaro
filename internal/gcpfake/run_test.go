package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
)

func TestRunFakeAcceptsEveryNameForm(t *testing.T) {
	f := NewRun(t)
	f.Project, f.Region = "proj-1", "r1"
	f.AddJob("fugaro-x-web", "1", "512Mi")
	full := f.Start("fugaro-x-web")
	id, _ := backend.ParseExecution(full)
	f.SetState(id.Name, backend.StateRunning) // short
	if f.State(full) != backend.StateRunning {
		t.Fatal("short-name SetState not seen by full name")
	}
	id.GCPProject = "999"
	f.SetState(id.String(), backend.StateSucceeded) // number form
	if f.State(full) != backend.StateSucceeded {
		t.Fatal("number-form SetState not seen")
	}
	f.OnRun = func(c RunCall) { f.SetState(c.Execution, backend.StateRunning) } // must not deadlock
	resp0, err := http.Post(f.URL+"/v2/projects/proj-1/locations/r1/jobs/fugaro-x-web:run", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp0.Body.Close()
	if f.State("fugaro-x-web-2") != backend.StateRunning {
		t.Fatal("OnRun's SetState was lost")
	}
	f.ProjectNumber = "999"
	resp, err := http.Get(f.URL + "/v2/projects/proj-1/locations/r1/jobs/-/executions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"projects/999/locations/r1/jobs/fugaro-x-web/executions/fugaro-x-web-1"`) {
		t.Fatalf("list did not use the project number: %s", body)
	}
	if got := f.Executions(); len(got) != 2 || got[0] != "fugaro-x-web-1" || got[1] != "fugaro-x-web-2" {
		t.Fatalf("Executions = %v", got)
	}
}

// TestRunFakeRefusesAnotherBackendsNames: this build knows two name codecs, a
// Cloud Run execution (eight fields) and a Batch job (six), and this fake
// speaks Cloud Run only. A six-field name reaching it -- a Batch job, or a
// Cloud Run execution path with its /executions/<e> missing -- must fail the
// test, not be parsed as an execution with an empty name and answered with a
// 404 that reads like "the execution is gone".
func TestRunFakeRefusesAnotherBackendsNames(t *testing.T) {
	f := NewRun(t)
	var failed int
	f.failf = func(string, ...any) { failed++ }
	// A legitimate execution still cancels by its own eight-field name: the
	// strictness above must not have cost the Cloud Run arm anything.
	f.AddJob("fugaro-x-web", "1", "512Mi")
	full := f.Start("fugaro-x-web")
	if code := runCall(t, f, "POST", "/v2/"+full+":cancel"); code != http.StatusOK {
		t.Errorf("cancel of a Cloud Run execution: status %d, want 200", code)
	}
	for _, c := range []struct{ method, path string }{
		// A Batch job's name, and a Cloud Run path missing its execution.
		{"POST", "/v2/projects/p/locations/r/jobs/fugaro-x-web-0123456789ab:cancel"},
		{"GET", "/v2/projects/p/locations/r/jobs/-"},
	} {
		if code := runCall(t, f, c.method, c.path); code != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, want 400 (the fake fails the test)", c.method, c.path, code)
		}
	}
	if failed != 2 {
		t.Fatalf("the fake failed the test %d times, want 2", failed)
	}
}

// runCall sends one request to a Run fake and returns the status.
func runCall(t *testing.T, f *Run, method, path string) int {
	t.Helper()
	req, err := http.NewRequest(method, f.URL+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestLoggingFakeRefusesUnknownFilters(t *testing.T) {
	l := NewLogging(t)
	var failed string
	l.failf = func(format string, args ...any) { failed = format }
	resp, err := http.Post(l.URL+"/v2/entries:list", "application/json",
		strings.NewReader(`{"resourceNames":["projects/p"],"filter":"severity>=ERROR"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || failed == "" {
		t.Fatalf("status %d, failed %q", resp.StatusCode, failed)
	}
}

// TestRunFakeSetOnRunIsSynchronized sets OnRun while :run requests are
// served. Under -race it fails for a plain assignment to OnRun, which is
// what a test whose requests come from a child process needs: the race
// detector can't see that the assignment happened before them.
func TestRunFakeSetOnRunIsSynchronized(t *testing.T) {
	f := NewRun(t)
	f.AddJob("fugaro-x-web", "1", "512Mi")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 {
			resp, err := http.Post(f.URL+"/v2/projects/p/locations/r1/jobs/fugaro-x-web:run", "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}
	}()
	var calls atomic.Int32
	for range 10 {
		f.SetOnRun(func(RunCall) { calls.Add(1) })
	}
	<-done
	f.SetOnRun(func(RunCall) { calls.Add(1) })
	resp, err := http.Post(f.URL+"/v2/projects/p/locations/r1/jobs/fugaro-x-web:run", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls.Load() == 0 {
		t.Fatal("OnRun set by SetOnRun was never called")
	}
}

func TestRunFakePatchesAJobWithItsEtag(t *testing.T) {
	f := NewRun(t)
	f.SetJob("fugarochk-x", map[string]string{"fugaro": "managed"}, "img:1")
	f.SetJobEnv("fugarochk-x", map[string]string{"A": "1", "FUGARO_CHECK_SPEC": "old"})
	const path = "/v2/projects/proj-1/locations/r1/jobs/fugarochk-x"
	get := func() map[string]any {
		resp, err := http.Get(f.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var j map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	patch := func(j map[string]any) int {
		data, _ := json.Marshal(j)
		req, _ := http.NewRequest(http.MethodPatch, f.URL+path, strings.NewReader(string(data)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	j := get()
	etag, _ := j["etag"].(string)
	if etag == "" {
		t.Fatalf("no etag: %v", j)
	}
	c := j["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	c["image"] = "img:2"
	c["env"] = []any{map[string]any{"name": "A", "value": "1"}, map[string]any{"name": "FUGARO_CHECK_SPEC", "value": "new"}}
	if code := patch(j); code != http.StatusOK {
		t.Fatalf("patch: %d", code)
	}
	after := get()
	ac := after["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if ac["image"] != "img:2" || after["etag"] == etag || len(f.Patches()) != 1 {
		t.Fatalf("after: %v, patches %d", after, len(f.Patches()))
	}
	if env := ac["env"].([]any); env[1].(map[string]any)["value"] != "new" {
		t.Fatalf("env %v", env)
	}
	// The etag read before the first patch is stale now.
	if code := patch(j); code != http.StatusConflict {
		t.Fatalf("stale etag: %d", code)
	}
}

// jobCall sends method on the job at path with body (nil for none) and
// decodes the answer with numbers kept as their text.
func jobCall(t *testing.T, f *Run, method, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	rd := strings.NewReader("")
	if body != nil {
		data, _ := json.Marshal(body)
		rd = strings.NewReader(string(data))
	}
	req, _ := http.NewRequest(method, f.URL+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	d := json.NewDecoder(resp.Body)
	d.UseNumber()
	var out map[string]any
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// A job set as JSON comes back whole, fields the Go client does not model
// and zero values included, and a patch replaces the whole body.
func TestRunFakeKeepsAndReplacesTheWholeJobBody(t *testing.T) {
	f := NewRun(t)
	const path = "/v2/projects/proj-1/locations/r1/jobs/fugarochk-x"
	f.SetJobJSON("fugarochk-x", `{"name":"projects/proj-1/locations/r1/jobs/fugarochk-x","labels":{"fugaro":"managed"},
		"futureField":{"n":12345678901234567890,"off":false},
		"template":{"taskCount":1,"template":{"maxRetries":0,"timeout":"900s","containers":[{"image":"img:1","env":[{"name":"A","value":"1"}]}]}}}`)
	code, j := jobCall(t, f, http.MethodGet, path, nil)
	if code != http.StatusOK || j["etag"] != "e0" {
		t.Fatalf("get: %d %v", code, j)
	}
	task := j["template"].(map[string]any)["template"].(map[string]any)
	if task["maxRetries"] != json.Number("0") || j["futureField"].(map[string]any)["n"] != json.Number("12345678901234567890") {
		t.Fatalf("a field was lost: %v", j)
	}
	// The patch leaves futureField and maxRetries out: they are gone.
	delete(j, "futureField")
	delete(task, "maxRetries")
	task["containers"].([]any)[0].(map[string]any)["image"] = "img:2"
	if code, _ := jobCall(t, f, http.MethodPatch, path, j); code != http.StatusOK {
		t.Fatalf("patch: %d", code)
	}
	_, after := jobCall(t, f, http.MethodGet, path, nil)
	at := after["template"].(map[string]any)["template"].(map[string]any)
	if _, ok := after["futureField"]; ok || at["maxRetries"] != nil || after["etag"] != "e1" ||
		at["containers"].([]any)[0].(map[string]any)["image"] != "img:2" {
		t.Fatalf("after: %v", after)
	}
	if got := f.JobJSON("proj-1", "r1", "fugarochk-x"); got["etag"] != "e1" || got["futureField"] != nil {
		t.Fatalf("JobJSON: %v", got)
	}
}

// Injected faults: a get or patch status, an operation that needs polls
// (the job changes only when it is done) and one that fails (it never does).
func TestRunFakeJobFaults(t *testing.T) {
	f := NewRun(t)
	const path = "/v2/projects/proj-1/locations/r1/jobs/fugarochk-x"
	f.SetJobJSON("fugarochk-x", `{"template":{"template":{"containers":[{"image":"img:1"}]}}}`)
	f.SetJobFaults(JobFaults{GetStatus: http.StatusForbidden})
	if code, _ := jobCall(t, f, http.MethodGet, path, nil); code != http.StatusForbidden {
		t.Fatalf("get: %d", code)
	}
	f.SetJobFaults(JobFaults{})
	_, j := jobCall(t, f, http.MethodGet, path, nil)
	f.SetJobFaults(JobFaults{PatchStatus: http.StatusBadRequest})
	if code, _ := jobCall(t, f, http.MethodPatch, path, j); code != http.StatusBadRequest || len(f.Patches()) != 0 {
		t.Fatalf("patch: %d", code)
	}

	f.SetJobFaults(JobFaults{Polls: 2})
	j["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"] = "img:2"
	_, op := jobCall(t, f, http.MethodPatch, path, j)
	if op["done"] != nil {
		t.Fatalf("patch op: %v", op)
	}
	image := func() any {
		return f.JobJSON("proj-1", "r1", "fugarochk-x")["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"]
	}
	opPath := "/v2/" + op["name"].(string)
	for i := range 2 {
		if _, o := jobCall(t, f, http.MethodGet, opPath, nil); o["done"] != nil || image() != "img:1" {
			t.Fatalf("poll %d: %v %v", i, o, image())
		}
	}
	if _, o := jobCall(t, f, http.MethodGet, opPath, nil); o["done"] != true || image() != "img:2" {
		t.Fatalf("last poll: %v %v", o, image())
	}

	f.SetJobFaults(JobFaults{OpError: "boom"})
	_, j = jobCall(t, f, http.MethodGet, path, nil)
	j["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"] = "img:3"
	if _, o := jobCall(t, f, http.MethodPatch, path, j); o["done"] != true || o["error"].(map[string]any)["message"] != "boom" || image() != "img:2" {
		t.Fatalf("failed op: %v %v", o, image())
	}
}

// A patch whose body names another job, or that carries an updateMask, is
// refused and changes nothing: the client sends the whole job under its own
// name and never a mask.
func TestRunFakeRefusesAForeignNameAndAnUpdateMask(t *testing.T) {
	f := NewRun(t)
	const path = "/v2/projects/proj-1/locations/r1/jobs/fugarochk-x"
	f.SetJobJSON("fugarochk-x", `{"name":"projects/proj-1/locations/r1/jobs/fugarochk-x","template":{"template":{"containers":[{"image":"img:1"}]}}}`)
	_, j := jobCall(t, f, http.MethodGet, path, nil)
	j["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"] = "img:2"

	other := map[string]any{}
	for k, v := range j {
		other[k] = v
	}
	other["name"] = "projects/proj-1/locations/r1/jobs/fugarochk-other"
	if code, _ := jobCall(t, f, http.MethodPatch, path, other); code != http.StatusBadRequest {
		t.Fatalf("another job's name: %d", code)
	}
	if code, _ := jobCall(t, f, http.MethodPatch, path+"?updateMask=template", j); code != http.StatusBadRequest {
		t.Fatalf("updateMask: %d", code)
	}
	if len(f.Patches()) != 0 {
		t.Fatal("a refused patch landed")
	}
	if code, _ := jobCall(t, f, http.MethodPatch, path, j); code != http.StatusOK || len(f.Patches()) != 1 {
		t.Fatalf("the job's own name: %d", code)
	}
}

// jobs.list carries each job's labels: they are what makes a job
// discoverable as this repository's (infra.Orphans), and the real API lists
// them. jobs.delete takes the job away when its operation is done, and a
// job that is not there is 404 -- both recorded, so a test can tell a
// delete that happened from one that was never attempted.
func TestRunFakeListsJobLabelsAndDeletesAJob(t *testing.T) {
	f := NewRun(t)
	f.Project, f.Region = "proj-1", "r1"
	f.SetJob("fugaro-x-web", map[string]string{"fugaro": "managed", "fugaro_repo": "x", "fugaro_workflow": "web"}, "img:1")
	f.SetJob("someone-elses", map[string]string{"team": "other"}, "img:2")

	code, list := jobCall(t, f, http.MethodGet, "/v2/projects/proj-1/locations/r1/jobs", nil)
	jobs, _ := list["jobs"].([]any)
	if code != http.StatusOK || len(jobs) != 2 {
		t.Fatalf("jobs.list: %d %v", code, list)
	}
	first, _ := jobs[0].(map[string]any)
	if first["name"] != "projects/proj-1/locations/r1/jobs/fugaro-x-web" {
		t.Fatalf("jobs.list = %v", jobs)
	}
	labels, _ := first["labels"].(map[string]any)
	if labels["fugaro"] != "managed" || labels["fugaro_workflow"] != "web" {
		t.Fatalf("the job's labels are not listed: %v", first)
	}
	if second, _ := jobs[1].(map[string]any); second["labels"] == nil {
		t.Fatalf("a foreign job's labels are not listed either: %v", second)
	}

	code, op := jobCall(t, f, http.MethodDelete, "/v2/projects/proj-1/locations/r1/jobs/fugaro-x-web", nil)
	if code != http.StatusOK || op["done"] != true {
		t.Fatalf("jobs.delete: %d %v", code, op)
	}
	if f.HasJob("fugaro-x-web") {
		t.Fatal("the deleted job is still there")
	}
	if got := f.JobDeletes(); !slices.Equal(got, []string{"fugaro-x-web"}) {
		t.Fatalf("JobDeletes = %q", got)
	}

	code, _ = jobCall(t, f, http.MethodDelete, "/v2/projects/proj-1/locations/r1/jobs/fugaro-x-web", nil)
	if code != http.StatusNotFound {
		t.Fatalf("deleting a job that is not there: %d, want 404", code)
	}
	if got := f.JobDeletes(); !slices.Equal(got, []string{"fugaro-x-web", "fugaro-x-web"}) {
		t.Fatalf("JobDeletes = %q; a refused attempt is recorded too", got)
	}
}

// A delete's operation is polled to a terminal state before the job is gone,
// and it can fail or time out on its own: the three outcomes Task 5's flow
// distinguishes.
func TestRunFakeDeleteWaitsForItsOperationAndCanFail(t *testing.T) {
	f := NewRun(t)
	f.Project, f.Region = "proj-1", "r1"
	const path = "/v2/projects/proj-1/locations/r1/jobs/fugaro-x-web"
	f.SetJob("fugaro-x-web", map[string]string{"fugaro": "managed"}, "img:1")

	f.SetJobFaults(JobFaults{DeletePolls: 2})
	code, op := jobCall(t, f, http.MethodDelete, path, nil)
	opPath, _ := op["name"].(string)
	if code != http.StatusOK || opPath == "" || op["done"] == true {
		t.Fatalf("a delete whose operation is not done yet: %d %v", code, op)
	}
	if !f.HasJob("fugaro-x-web") {
		t.Fatal("the job went away before its operation was done")
	}
	for i, want := range []bool{false, false, true} {
		_, o := jobCall(t, f, http.MethodGet, "/v2/"+opPath, nil)
		if done, _ := o["done"].(bool); done != want {
			t.Fatalf("poll %d: done = %v, want %v (%v)", i+1, done, want, o)
		}
	}
	if f.HasJob("fugaro-x-web") {
		t.Fatal("the job is still there after its operation was done")
	}

	f.SetJob("fugaro-x-web", map[string]string{"fugaro": "managed"}, "img:1")
	f.SetJobFaults(JobFaults{DeleteOpError: "boom"})
	_, op = jobCall(t, f, http.MethodDelete, path, nil)
	if op["done"] != true || op["error"].(map[string]any)["message"] != "boom" {
		t.Fatalf("a failed delete's operation: %v", op)
	}
	if !f.HasJob("fugaro-x-web") {
		t.Fatal("a failed delete took the job anyway")
	}

	f.SetJobFaults(JobFaults{})
	f.FailDeleteWith = http.StatusForbidden
	if code, _ := jobCall(t, f, http.MethodDelete, path, nil); code != http.StatusForbidden {
		t.Fatalf("a refused delete: %d, want 403", code)
	}
	f.FailDeleteWith = 0
	f.FailListWith = http.StatusForbidden
	if code, _ := jobCall(t, f, http.MethodGet, "/v2/projects/proj-1/locations/r1/jobs", nil); code != http.StatusForbidden {
		t.Fatalf("a refused jobs.list: %d, want 403", code)
	}
}
