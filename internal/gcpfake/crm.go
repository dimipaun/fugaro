package gcpfake

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// CRM is a fake of the Cloud Resource Manager v1 call Fugaro makes:
// projects get, for the project's number.
//
// Like the real API, it answers a project it doesn't know with 403, not
// 404, so a caller can't tell a missing project from one it may not read.
type CRM struct {
	*Server

	mu       sync.Mutex
	projects map[string]int64
}

var crmProjectRE = regexp.MustCompile(`^/v1/projects/([^/:]+)$`)

// NewCRM starts a Resource Manager fake that lives until the test ends.
func NewCRM(t *testing.T) *CRM {
	t.Helper()
	f := &CRM{projects: map[string]int64{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddProject makes project id exist with number.
func (f *CRM) AddProject(id string, number int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[id] = number
}

func (f *CRM) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := crmProjectRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	n, ok := f.projects[m[1]]
	if !ok {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projectId": m[1], "projectNumber": strconv.FormatInt(n, 10), "lifecycleState": "ACTIVE", "name": m[1],
	})
}
