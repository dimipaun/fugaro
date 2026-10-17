package gcpfake

import (
	"maps"
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// Scheduler is a fake of the Cloud Scheduler v1 calls fugaro makes: a job's
// get (discovery) and a job's run (the preload rebuild).
type Scheduler struct {
	*Server

	// RefuseRuns is how many run calls are answered 403 PERMISSION_DENIED,
	// which is what a run whose identity's binding has not propagated gets.
	// It counts down with each run call.
	RefuseRuns int

	// RefuseRunMessage replaces the refusal's message. A permanent denial --
	// the caller does not hold cloudscheduler.jobs.run -- and a propagated one
	// arrive as the same status, and the retry is keyed on the status alone
	// (infra.propagating), so a test can set this to the permanent wording and
	// see that it is retried the same way.
	RefuseRunMessage string

	mu   sync.Mutex
	jobs map[string]map[string]any // projects/<p>/locations/<r>/jobs/<name> → fields
	runs []string
}

var (
	schedulerJobRE = regexp.MustCompile(`^/v1/(projects/[^/]+/locations/[^/]+/jobs/[^/:]+)$`)
	schedulerRunRE = regexp.MustCompile(`^/v1/(projects/[^/]+/locations/[^/]+/jobs/[^/:]+):run$`)
)

// Runs are the jobs a run was asked for, in order.
func (f *Scheduler) Runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.runs...)
}

// NewScheduler starts a Cloud Scheduler fake that lives until the test
// ends.
func NewScheduler(t *testing.T) *Scheduler {
	t.Helper()
	f := &Scheduler{jobs: map[string]map[string]any{}}
	f.Server = newServer(t, f.handle)
	return f
}

// SetJob makes the job exist in project and region, posting to uri as the
// service account email.
func (f *Scheduler) SetJob(project, region, name, uri, email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	full := "projects/" + project + "/locations/" + region + "/jobs/" + name
	f.jobs[full] = map[string]any{
		"name": full, "schedule": "17 5 * * *", "timeZone": "Etc/UTC", "state": "PAUSED",
		"httpTarget": map[string]any{"uri": uri, "httpMethod": "POST", "oauthToken": map[string]any{"serviceAccountEmail": email}},
	}
}

func (f *Scheduler) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := schedulerRunRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodPost {
		if f.RefuseRuns > 0 {
			f.RefuseRuns--
			msg := "The caller does not have permission to act as the job's service account."
			if f.RefuseRunMessage != "" {
				msg = f.RefuseRunMessage
			}
			writeErrorInfo(w, http.StatusForbidden, "PERMISSION_DENIED", msg, "IAM_PERMISSION_DENIED", nil)
			return
		}
		if f.jobs[m[1]] == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found.")
			return
		}
		f.runs = append(f.runs, m[1])
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	m := schedulerJobRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	j := f.jobs[m[1]]
	if j == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Job not found.")
		return
	}
	writeJSON(w, http.StatusOK, maps.Clone(j))
}
