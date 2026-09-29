package gcpfake

import (
	"maps"
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// Scheduler is a fake of the Cloud Scheduler v1 call fugaro init's
// discovery makes: a job's get.
type Scheduler struct {
	*Server

	mu   sync.Mutex
	jobs map[string]map[string]any // projects/<p>/locations/<r>/jobs/<name> → fields
}

var schedulerJobRE = regexp.MustCompile(`^/v1/(projects/[^/]+/locations/[^/]+/jobs/[^/:]+)$`)

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
