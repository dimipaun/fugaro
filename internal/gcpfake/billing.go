package gcpfake

import (
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// Billing is a fake of the Cloud Billing call Fugaro makes: a project's
// billing info (projects/<id>/billingInfo). A project it doesn't know is a
// 403, as the real API answers a project the caller can't read.
type Billing struct {
	*Server

	mu      sync.Mutex
	enabled map[string]bool
}

var billingInfoRE = regexp.MustCompile(`^/v1/projects/([^/:]+)/billingInfo$`)

// NewBilling starts a Cloud Billing fake that lives until the test ends.
func NewBilling(t *testing.T) *Billing {
	t.Helper()
	f := &Billing{enabled: map[string]bool{}}
	f.Server = newServer(t, f.handle)
	return f
}

// SetBilling makes project known, with billing on or off.
func (f *Billing) SetBilling(project string, enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled[project] = enabled
}

func (f *Billing) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	m := billingInfoRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	on, ok := f.enabled[m[1]]
	if !ok {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	body := map[string]any{"name": "projects/" + m[1] + "/billingInfo", "projectId": m[1], "billingEnabled": on}
	if on {
		body["billingAccountName"] = "billingAccounts/000000-000000-000000"
	}
	writeJSON(w, http.StatusOK, body)
}
