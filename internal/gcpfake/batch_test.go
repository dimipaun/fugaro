package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// batchCall sends one request to a Batch fake and returns the status and the
// decoded body. The fake's own tests speak HTTP rather than the generated
// client: what they check is the fake's answer, not a client's reading of it.
func batchCall(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: body %s: %v", method, url, raw, err)
		}
	}
	return resp.StatusCode, out
}

// batchJob is the smallest job body the fake accepts: the fields it reads are
// the labels (for the list filter) and nothing else.
const batchJobBody = `{"labels":{"fugaro":"managed"},"taskGroups":[{"taskSpec":{"environment":{"variables":{"FUGARO_RUN":"acme-app/run"}}}}]}`

func TestBatchFakeLifecycle(t *testing.T) {
	f := NewBatch(t)
	base := f.URL + "/v1/projects/p/locations/r1"
	code, created := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-1", batchJobBody)
	if code != http.StatusOK {
		t.Fatalf("create: status %d: %v", code, created)
	}
	if created["name"] != "projects/p/locations/r1/jobs/fugaro-x-web-1" || created["uid"] == nil {
		t.Fatalf("create answered %v", created)
	}
	if st, _ := created["status"].(map[string]any); st["state"] != "QUEUED" {
		t.Fatalf("a new job is not QUEUED: %v", created["status"])
	}
	if got := f.IDs(); len(got) != 1 || got[0] != "fugaro-x-web-1" {
		t.Fatalf("IDs = %v", got)
	}
	if got := f.Creates(); len(got) != 1 || got[0].JobID != "fugaro-x-web-1" || got[0].Job["labels"] == nil {
		t.Fatalf("Creates = %+v", got)
	}

	// get
	if code, got := batchCall(t, "GET", base+"/jobs/fugaro-x-web-1", ""); code != http.StatusOK || got["name"] != created["name"] {
		t.Fatalf("get: status %d: %v", code, got)
	}
	// a job that does not exist
	if code, got := batchCall(t, "GET", base+"/jobs/nope", ""); code != http.StatusNotFound || got["error"] == nil {
		t.Fatalf("get of an unknown job: status %d: %v", code, got)
	}

	// cancel, then delete
	if code, got := batchCall(t, "POST", base+"/jobs/fugaro-x-web-1:cancel", "{}"); code != http.StatusOK || got["done"] != true {
		t.Fatalf("cancel: status %d: %v", code, got)
	}
	if got := f.State("fugaro-x-web-1"); got != "CANCELLED" {
		t.Fatalf("state after cancel = %q", got)
	}
	if got := f.Cancels(); len(got) != 1 || got[0] != "fugaro-x-web-1" {
		t.Fatalf("Cancels = %v", got)
	}
	if code, _ := batchCall(t, "DELETE", base+"/jobs/fugaro-x-web-1", ""); code != http.StatusOK {
		t.Fatalf("delete: status %d", code)
	}
	if got := f.Deletes(); len(got) != 1 || got[0] != "fugaro-x-web-1" {
		t.Fatalf("Deletes = %v", got)
	}
	if code, _ := batchCall(t, "GET", base+"/jobs/fugaro-x-web-1", ""); code != http.StatusNotFound {
		t.Fatalf("a deleted job is still readable: status %d", code)
	}
}

// TestBatchFakeRefusesWhatItDoesNotImplement is the fake's fail-closed half:
// Batch has no jobs.patch, so a client that sends one is noticed here rather
// than in production.
func TestBatchFakeRefusesWhatItDoesNotImplement(t *testing.T) {
	f := NewBatch(t)
	var failed []string
	f.failf = func(format string, args ...any) { failed = append(failed, format) }
	base := f.URL + "/v1/projects/p/locations/r1"
	for _, c := range []struct{ method, url string }{
		{"PATCH", base + "/jobs/fugaro-x-web-1"},
		{"PUT", base + "/jobs/fugaro-x-web-1"},
		{"POST", f.URL + "/v1/projects/p/locations/r1/jobs/fugaro-x-web-1:run"},
		{"GET", f.URL + "/v2/something-else"},
		{"DELETE", f.URL + "/v1/projects/p/locations/r1/jobs"}, // no job id
		{"POST", f.URL + "/v1/locations/r1/jobs?jobId=x"},      // no project
	} {
		if code, _ := batchCall(t, c.method, c.url, "{}"); code != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, want 400", c.method, c.url, code)
		}
	}
	if len(failed) != 6 {
		t.Fatalf("the fake failed the test %d times, want 6: %v", len(failed), failed)
	}
}

// TestBatchFakeScopesJobsToProjectAndRegion: the API scopes a job to the
// project and the region in its name, so a request naming another one is
// NOT_FOUND (or, for a listing, empty) -- never the job. A fake that keyed
// jobs by id alone would be more permissive than the API, and a backend that
// dropped the project or the region from a request would pass every test.
func TestBatchFakeScopesJobsToProjectAndRegion(t *testing.T) {
	f := NewBatch(t)
	base := f.URL + "/v1/projects/p/locations/r1"
	if code, _ := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-1", batchJobBody); code != http.StatusOK {
		t.Fatal("create failed")
	}
	// A get, a cancel or a delete naming another project or region: NOT_FOUND,
	// and nothing recorded -- neither call may reach the job it names.
	for _, c := range []struct{ method, url string }{
		{"GET", f.URL + "/v1/projects/q/locations/r1/jobs/fugaro-x-web-1"},
		{"GET", f.URL + "/v1/projects/p/locations/r2/jobs/fugaro-x-web-1"},
		{"POST", f.URL + "/v1/projects/q/locations/r1/jobs/fugaro-x-web-1:cancel"},
		{"POST", f.URL + "/v1/projects/p/locations/r2/jobs/fugaro-x-web-1:cancel"},
		{"DELETE", f.URL + "/v1/projects/p/locations/r2/jobs/fugaro-x-web-1"},
		{"DELETE", f.URL + "/v1/projects/q/locations/r1/jobs/fugaro-x-web-1"},
	} {
		if code, body := batchCall(t, c.method, c.url, "{}"); code != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404: %v", c.method, c.url, code, body)
		}
	}
	if got := f.Cancels(); len(got) != 0 {
		t.Errorf("a cancel of another project's job was recorded: %v", got)
	}
	if got := f.Deletes(); len(got) != 0 {
		t.Errorf("a delete of another project's job was recorded: %v", got)
	}
	// The job those requests named is untouched, and still the only one.
	if code, body := batchCall(t, "GET", base+"/jobs/fugaro-x-web-1", ""); code != http.StatusOK {
		t.Fatalf("the job the requests named: status %d: %v", code, body)
	} else if st, _ := body["status"].(map[string]any); st["state"] != "QUEUED" {
		t.Errorf("state = %v, want QUEUED", st["state"])
	}
	// A listing is scoped to its parent too: another region's listing, and
	// another project's, are empty rather than this job's.
	for _, parent := range []string{f.URL + "/v1/projects/p/locations/r2", f.URL + "/v1/projects/q/locations/r1"} {
		code, body := batchCall(t, "GET", parent+"/jobs?filter="+`labels.fugaro%20%3D%20%22managed%22`, "")
		if code != http.StatusOK {
			t.Fatalf("list %s: status %d: %v", parent, code, body)
		}
		if got := batchNames(body); len(got) != 0 {
			t.Errorf("list %s answered %v, want nothing", parent, got)
		}
	}
	// The same id in another region, and in another project, is a different
	// job: both creates succeed, and all three are readable at their own
	// names.
	for _, u := range []string{
		f.URL + "/v1/projects/p/locations/r2/jobs?jobId=fugaro-x-web-1",
		f.URL + "/v1/projects/q/locations/r1/jobs?jobId=fugaro-x-web-1",
	} {
		if code, body := batchCall(t, "POST", u, batchJobBody); code != http.StatusOK {
			t.Fatalf("create at %s: status %d: %v", u, code, body)
		}
	}
	if got := f.IDs(); len(got) != 3 {
		t.Fatalf("IDs = %v, want the three jobs", got)
	}
	for _, u := range []string{
		base + "/jobs/fugaro-x-web-1",
		f.URL + "/v1/projects/p/locations/r2/jobs/fugaro-x-web-1",
		f.URL + "/v1/projects/q/locations/r1/jobs/fugaro-x-web-1",
	} {
		if code, body := batchCall(t, "GET", u, ""); code != http.StatusOK {
			t.Errorf("get %s: status %d: %v", u, code, body)
		}
	}
	code, body := batchCall(t, "GET", base+"/jobs?filter="+`labels.fugaro%20%3D%20%22managed%22`, "")
	if code != http.StatusOK {
		t.Fatalf("list: status %d: %v", code, body)
	}
	if got := batchNames(body); len(got) != 1 || got[0] != "projects/p/locations/r1/jobs/fugaro-x-web-1" {
		t.Fatalf("list answered %v, want only r1's job", got)
	}
}

// TestBatchFakeRefusesABadCreate: a job id is what the caller chooses and the
// API validates, so the fake validates it too -- a fake that accepted one the
// real API refuses would let a naming bug through.
func TestBatchFakeRefusesABadCreate(t *testing.T) {
	f := NewBatch(t)
	// A malformed body is the client's fault, and the fake reports it as a
	// test failure; this test sends one on purpose.
	f.failf = func(string, ...any) {}
	base := f.URL + "/v1/projects/p/locations/r1"
	// A job id starts with a lowercase letter ("must start with lowercase
	// characters", design §2.4), so a leading digit is refused; "-" cannot be
	// first or last; 63 characters is the limit; and one character is a
	// valid id, not too short.
	for _, id := range []string{"", "-leading", "trailing-", "Upper", "under_score", "1leading", "9", strings.Repeat("a", 64)} {
		code, body := batchCall(t, "POST", base+"/jobs?jobId="+id, batchJobBody)
		if code != http.StatusBadRequest {
			t.Errorf("create with jobId %q: status %d, want 400: %v", id, code, body)
		}
	}
	if code, _ := batchCall(t, "POST", base+"/jobs?jobId=a", batchJobBody); code != http.StatusOK {
		t.Errorf("create with the one-character id %q: status %d, want 200", "a", code)
	}
	if got := f.IDs(); len(got) != 1 {
		t.Fatalf("a refused create created something: %v", got)
	}
	// requestId is documented as a UUID, so another form is refused, and a
	// UUID is accepted.
	if code, _ := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-2&requestId=1", batchJobBody); code != http.StatusBadRequest {
		t.Errorf("create with requestId=1: status %d, want 400", code)
	}
	if code, _ := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-3&requestId=9f1b1c2e-4a5d-4e6f-8a9b-0c1d2e3f4a5b", batchJobBody); code != http.StatusOK {
		t.Errorf("create with a UUID requestId: status %d, want 200", code)
	}
	// The body's own name must be the job in the URL, as the API requires.
	code, _ := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-1", `{"name":"projects/p/locations/r1/jobs/other"}`)
	if code != http.StatusBadRequest {
		t.Errorf("a body naming another job: status %d, want 400", code)
	}
	// The body is not valid JSON.
	if code, _ := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-1", `{`); code != http.StatusBadRequest {
		t.Errorf("an unparseable body: status %d, want 400", code)
	}
}

// TestBatchFakeRefusesADuplicate: a Batch job is single-use and its id is the
// run's, so a second create of one is a run launched twice.
func TestBatchFakeRefusesADuplicate(t *testing.T) {
	f := NewBatch(t)
	url := f.URL + "/v1/projects/p/locations/r1/jobs?jobId=fugaro-x-web-1"
	if code, _ := batchCall(t, "POST", url, batchJobBody); code != http.StatusOK {
		t.Fatalf("first create: status %d", code)
	}
	code, body := batchCall(t, "POST", url, batchJobBody)
	if code != http.StatusConflict {
		t.Fatalf("second create: status %d, want 409: %v", code, body)
	}
	if got := f.IDs(); len(got) != 1 {
		t.Fatalf("a duplicate create created a second job: %v", got)
	}
}

// TestBatchFakeCancelOfATerminalJob: the real API's answer is UNVERIFIED
// (design §9, Check 57a step 7), so the fake refuses by default -- which is
// what makes a backend's tolerance of the race a tested requirement -- and
// answers OK when AllowCancelOfTerminal says the API is the other way.
func TestBatchFakeCancelOfATerminalJob(t *testing.T) {
	f := NewBatch(t)
	base := f.URL + "/v1/projects/p/locations/r1"
	if code, _ := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-1", batchJobBody); code != http.StatusOK {
		t.Fatal("create failed")
	}
	f.SetState("fugaro-x-web-1", "SUCCEEDED")
	code, body := batchCall(t, "POST", base+"/jobs/fugaro-x-web-1:cancel", "{}")
	if code != http.StatusBadRequest || body["error"] == nil {
		t.Fatalf("cancel of a finished job: status %d: %v", code, body)
	}
	if got := f.Cancels(); len(got) != 0 {
		t.Fatalf("a refused cancel was recorded: %v", got)
	}
	if got := f.State("fugaro-x-web-1"); got != "SUCCEEDED" {
		t.Fatalf("a refused cancel changed the state to %q", got)
	}
	f.AllowCancelOfTerminal = true
	if code, _ := batchCall(t, "POST", base+"/jobs/fugaro-x-web-1:cancel", "{}"); code != http.StatusOK {
		t.Fatalf("cancel with AllowCancelOfTerminal: status %d", code)
	}
	if got := f.State("fugaro-x-web-1"); got != "SUCCEEDED" {
		t.Fatalf("cancelling a finished job changed its state to %q", got)
	}
}

// TestBatchFakeListFiltersAndPages: the listing is what a sweeper reads, so
// its filter and its paging are the fake's, not the client's, business.
func TestBatchFakeListFiltersAndPages(t *testing.T) {
	f := NewBatch(t)
	base := f.URL + "/v1/projects/p/locations/r1"
	for _, id := range []string{"fugaro-x-web-1", "fugaro-x-web-2", "other-web-3"} {
		body := batchJobBody
		if id == "other-web-3" {
			body = `{"labels":{"someone":"else"}}`
		}
		if code, _ := batchCall(t, "POST", base+"/jobs?jobId="+id, body); code != http.StatusOK {
			t.Fatalf("create %s failed", id)
		}
	}
	// The filter is the marker, so someone else's job is not in the answer.
	code, body := batchCall(t, "GET", base+"/jobs?filter="+`labels.fugaro%20%3D%20%22managed%22`, "")
	if code != http.StatusOK {
		t.Fatalf("list: status %d: %v", code, body)
	}
	if got := batchNames(body); len(got) != 2 {
		t.Fatalf("list answered %v, want the two marked jobs", got)
	}
	// A filter the fake cannot answer fails the test rather than being
	// silently ignored: a wrong server-side filter must not read as "no
	// runs".
	var failed string
	f.failf = func(format string, args ...any) { failed = format }
	if code, _ := batchCall(t, "GET", base+"/jobs?filter=labels.fugaro%3D%22managed%22%20AND%20x%3D1", ""); code != http.StatusBadRequest {
		t.Fatalf("an unsupported filter: status %d, want 400", code)
	}
	if failed == "" {
		t.Fatal("an unsupported filter did not fail the test")
	}

	// Paging: one per page, in the fake's own order (creation order, oldest
	// first -- the reference documents no default order, so no caller may
	// assume one).
	code, page := batchCall(t, "GET", base+"/jobs?filter="+`labels.fugaro%20%3D%20%22managed%22`+"&pageSize=1", "")
	if code != http.StatusOK || len(batchNames(page)) != 1 {
		t.Fatalf("page 1: status %d: %v", code, page)
	}
	token, _ := page["nextPageToken"].(string)
	if token == "" {
		t.Fatalf("page 1 carried no next page: %v", page)
	}
	code, page2 := batchCall(t, "GET", base+"/jobs?filter="+`labels.fugaro%20%3D%20%22managed%22`+"&pageSize=1&pageToken="+token, "")
	if code != http.StatusOK || len(batchNames(page2)) != 1 {
		t.Fatalf("page 2: status %d: %v", code, page2)
	}
	if batchNames(page)[0] == batchNames(page2)[0] {
		t.Fatal("the second page repeated the first")
	}
	if page2["nextPageToken"] != nil {
		t.Fatalf("the last page carried a next page: %v", page2)
	}
}

func batchNames(body map[string]any) []string {
	jobs, _ := body["jobs"].([]any)
	var out []string
	for _, j := range jobs {
		m, _ := j.(map[string]any)
		if n, ok := m["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out
}

// TestBatchFakeAnswersWithTheProjectNumber: the API may write a project
// number where the caller wrote a project ID, and callers that compare names
// must not be broken by it.
func TestBatchFakeAnswersWithTheProjectNumber(t *testing.T) {
	f := NewBatch(t)
	f.ProjectNumber = "999"
	base := f.URL + "/v1/projects/p/locations/r1"
	if code, _ := batchCall(t, "POST", base+"/jobs?jobId=fugaro-x-web-1", batchJobBody); code != http.StatusOK {
		t.Fatal("create failed")
	}
	if got := f.JobJSON("p", "r1", "fugaro-x-web-1")["name"]; got != "projects/999/locations/r1/jobs/fugaro-x-web-1" {
		t.Fatalf("JobJSON name = %v", got)
	}
	if got := f.Creates()[0].Name; got != "projects/999/locations/r1/jobs/fugaro-x-web-1" {
		t.Fatalf("Creates[0].Name = %v", got)
	}
}

// TestBatchFakeFailCreateWith: an injected failure creates nothing, which is
// how the backend's classification of a create's errors is exercised.
func TestBatchFakeFailCreateWith(t *testing.T) {
	f := NewBatch(t)
	f.FailCreateWith = http.StatusConflict
	code, _ := batchCall(t, "POST", f.URL+"/v1/projects/p/locations/r1/jobs?jobId=fugaro-x-web-1", batchJobBody)
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if got := f.IDs(); len(got) != 0 {
		t.Fatalf("an injected failure created a job: %v", got)
	}
	if got := f.Creates(); len(got) != 1 {
		t.Fatalf("a refused create was not recorded: %v", got)
	}
}

// TestBatchFakeSetCreatedAndUID are the two knobs a test needs to reach a
// state the fake's own create never produces.
func TestBatchFakeSetCreatedAndUID(t *testing.T) {
	f := NewBatch(t)
	if code, _ := batchCall(t, "POST", f.URL+"/v1/projects/p/locations/r1/jobs?jobId=fugaro-x-web-1", batchJobBody); code != http.StatusOK {
		t.Fatal("create failed")
	}
	when := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	f.SetCreated("fugaro-x-web-1", when)
	if got, _ := f.JobJSON("p", "r1", "fugaro-x-web-1")["createTime"].(string); !strings.HasPrefix(got, when.Format(time.RFC3339)) {
		t.Fatalf("createTime = %q, want %s", got, when.Format(time.RFC3339))
	}
	f.SetUID("fugaro-x-web-1", "")
	if got := f.JobJSON("p", "r1", "fugaro-x-web-1")["uid"]; got != "" {
		t.Fatalf("uid = %v, want empty", got)
	}
}

// TestBatchFakeStatusKnobs are the three knobs a caller needs to reach a job
// whose time in its state is a fact (SetStateAt), one whose events date
// something else (SetStatusEvents), and one that reports only a runDuration
// (SetRunDuration) -- the shapes a per-state deadline is read from.
func TestBatchFakeStatusKnobs(t *testing.T) {
	f := NewBatch(t)
	if code, _ := batchCall(t, "POST", f.URL+"/v1/projects/p/locations/r1/jobs?jobId=fugaro-x-web-1", batchJobBody); code != http.StatusOK {
		t.Fatal("create failed")
	}
	when := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	f.SetStateAt("fugaro-x-web-1", "RUNNING", when)
	st := batchStatus(f.JobJSON("p", "r1", "fugaro-x-web-1"))
	if st["state"] != "RUNNING" {
		t.Fatalf("state = %v, want RUNNING", st["state"])
	}
	events, _ := st["statusEvents"].([]any)
	if len(events) != 1 {
		t.Fatalf("SetStateAt wrote %d status events, want one: %v", len(events), st)
	}
	e, _ := events[0].(map[string]any)
	if e["type"] != "STATUS_CHANGED" || e["eventTime"] != when.Format(time.RFC3339) {
		t.Fatalf("status event = %v, want a STATUS_CHANGED at %s", e, when.Format(time.RFC3339))
	}
	// SetStatusEvents replaces them, which is how a task-level event (one the
	// state deadline must not read) is reached.
	f.SetStatusEvents("fugaro-x-web-1", map[string]any{
		"type": "TASK_STATE_CHANGED", "taskState": "RUNNING",
		"eventTime": when.Add(time.Hour).Format(time.RFC3339),
	})
	events, _ = batchStatus(f.JobJSON("p", "r1", "fugaro-x-web-1"))["statusEvents"].([]any)
	if len(events) != 1 {
		t.Fatalf("SetStatusEvents wrote %d events, want one", len(events))
	}
	f.SetRunDuration("fugaro-x-web-1", "600s")
	if got := batchStatus(f.JobJSON("p", "r1", "fugaro-x-web-1"))["runDuration"]; got != "600s" {
		t.Fatalf("runDuration = %v, want 600s", got)
	}
	// And the state survives a render: it is the job's, not the event's.
	if got := f.State("fugaro-x-web-1"); got != "RUNNING" {
		t.Fatalf("State = %q, want RUNNING", got)
	}
}

// TestBatchFakeUnreachable: jobs.list reports the locations it could not reach
// alongside the jobs it did answer, and reports nothing when it reached them
// all -- a caller reading absence as fact has to be able to tell the two
// answers apart.
func TestBatchFakeUnreachable(t *testing.T) {
	f := NewBatch(t)
	if code, _ := batchCall(t, "POST", f.URL+"/v1/projects/p/locations/r1/jobs?jobId=fugaro-x-web-1", batchJobBody); code != http.StatusOK {
		t.Fatal("create failed")
	}
	list := func() map[string]any {
		code, body := batchCall(t, "GET", f.URL+"/v1/projects/p/locations/r1/jobs", "")
		if code != http.StatusOK {
			t.Fatalf("list: status %d: %v", code, body)
		}
		return body
	}
	if got := list()["unreachable"]; got != nil {
		t.Fatalf("unreachable = %v, want the field absent while every location is reachable", got)
	}
	f.Unreachable = []string{"r2", "r3"}
	got, _ := list()["unreachable"].([]any)
	if len(got) != 2 || got[0] != "r2" || got[1] != "r3" {
		t.Fatalf("unreachable = %v, want [r2 r3]", got)
	}
}

func batchStatus(job map[string]any) map[string]any {
	st, _ := job["status"].(map[string]any)
	return st
}

// TestLoggingFakeReadsBatchTaskLogsByUID is the Logging fake's Batch half:
// entries stored by UID come back for the filter design §2.3 ships, and only
// that job's.
func TestLoggingFakeReadsBatchTaskLogsByUID(t *testing.T) {
	l := NewLogging(t)
	l.AddUID("uid-1", LogEntry{Time: time.Now(), Text: "mine"})
	l.AddUID("uid-2", LogEntry{Time: time.Now(), Text: "someone else's"})
	filter := `labels.job_uid="uid-1" AND logName="projects/p/logs/batch_task_logs"`
	body, err := json.Marshal(map[string]any{"resourceNames": []string{"projects/p"}, "filter": filter, "orderBy": "timestamp asc"})
	if err != nil {
		t.Fatal(err)
	}
	code, got := batchCall(t, "POST", l.URL+"/v2/entries:list", string(body))
	if code != http.StatusOK {
		t.Fatalf("entries.list: status %d: %v", code, got)
	}
	entries, _ := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want one", entries)
	}
	e, _ := entries[0].(map[string]any)
	if e["textPayload"] != "mine" || e["logName"] != "projects/p/logs/batch_task_logs" {
		t.Fatalf("entry = %v", e)
	}
	if res, _ := e["resource"].(map[string]any); res["type"] != "batch.googleapis.com/Job" {
		t.Fatalf("resource = %v", e["resource"])
	}
	// A timestamp filter the fake cannot parse, and a log whose project is
	// not one the request may read, both fail the test rather than answering
	// something.
	var failed []string
	l.failf = func(format string, args ...any) { failed = append(failed, format) }
	for _, f := range []string{
		filter + ` AND timestamp>="not a time"`,
		`labels.job_uid="uid-1" AND logName="projects/other/logs/batch_task_logs"`,
	} {
		body, _ := json.Marshal(map[string]any{"resourceNames": []string{"projects/p"}, "filter": f})
		if code, _ := batchCall(t, "POST", l.URL+"/v2/entries:list", string(body)); code != http.StatusBadRequest {
			t.Errorf("filter %q: status %d, want 400", f, code)
		}
	}
	// A Batch filter with no resource name, or with more than one: the
	// project check reads ResourceNames[0], so it must come after the count
	// is checked -- otherwise the fake panics instead of failing the test,
	// which is not what a fake that fails closed owes its caller.
	for _, names := range [][]string{{}, {"projects/p", "projects/q"}} {
		body, _ := json.Marshal(map[string]any{"resourceNames": names, "filter": filter})
		if code, _ := batchCall(t, "POST", l.URL+"/v2/entries:list", string(body)); code != http.StatusBadRequest {
			t.Errorf("resourceNames %q: status %d, want 400", names, code)
		}
	}
	if len(failed) != 4 {
		t.Fatalf("the fake failed the test %d times, want 4: %v", len(failed), failed)
	}
}
