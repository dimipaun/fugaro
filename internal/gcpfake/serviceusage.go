package gcpfake

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// ServiceUsage is a fake of the Service Usage v1 calls fugaro init makes:
// a service's enable, and the get of its operation. It also turns the
// other fakes' APIs off: while Disable has a service disabled, every call
// to the fakes serving it is answered as Google answers a call to a
// disabled API (403 with an ErrorInfo whose reason is SERVICE_DISABLED).
// An enable turns the service back on.
type ServiceUsage struct {
	*Server

	// Consumer is the project a SERVICE_DISABLED answer names, as
	// projects/<number>.
	Consumer string
	// PendingPolls is how many gets of an enable's operation answer it
	// not done yet before it is done.
	PendingPolls int
	// Propagation is how many calls to a service's API are still answered
	// SERVICE_DISABLED after its enable, as happens while the enable
	// propagates.
	Propagation int

	mu       sync.Mutex
	services map[string]*usageService
	ops      map[string]int // operations/<id> → polls left before done
	nextOp   int
}

type usageService struct {
	disabled bool
	stale    int // calls still refused after the enable
}

var (
	usageEnableRE = regexp.MustCompile(`^/v1/projects/([^/]+)/services/([^/:]+):enable$`)
	usageOpRE     = regexp.MustCompile(`^/v1/(operations/[^/]+)$`)
)

// NewServiceUsage starts a Service Usage fake that lives until the test
// ends.
func NewServiceUsage(t *testing.T) *ServiceUsage {
	t.Helper()
	f := &ServiceUsage{Consumer: "projects/123456789012", services: map[string]*usageService{}, ops: map[string]int{}}
	f.Server = newServer(t, f.handle)
	return f
}

// Disable turns service off, and has each of servers (the fakes serving
// its API) answer every call SERVICE_DISABLED until an enable turns it on.
func (f *ServiceUsage) Disable(service string, servers ...*Server) {
	f.mu.Lock()
	f.services[service] = &usageService{disabled: true}
	f.mu.Unlock()
	for _, s := range servers {
		s.mu.Lock()
		s.disabled = func() (string, string, bool) { return service, f.Consumer, f.refuses(service) }
		s.mu.Unlock()
	}
}

// Enabled reports whether service is on: never disabled, or enabled since.
func (f *ServiceUsage) Enabled(service string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.services[service]
	return s == nil || !s.disabled
}

// Enables are the services an enable was asked for, in order.
func (f *ServiceUsage) Enables() []string {
	var out []string
	for _, r := range f.Requests() {
		if m := usageEnableRE.FindStringSubmatch(r.Path); m != nil && r.Method == http.MethodPost {
			out = append(out, m[2])
		}
	}
	return out
}

// refuses reports whether a call to service's API is refused, counting
// down the calls refused while an enable propagates.
func (f *ServiceUsage) refuses(service string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.services[service]
	switch {
	case s == nil:
		return false
	case s.disabled:
		return true
	case s.stale > 0:
		s.stale--
		return true
	}
	return false
}

func (f *ServiceUsage) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := usageEnableRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodPost {
		service := m[2]
		s := f.services[service]
		if s == nil || !s.disabled {
			// Enabled already: a no-op, answered done at once, as Google does.
			writeJSON(w, http.StatusOK, doneOp("operations/noop.DONE_OPERATION", m[1], service))
			return
		}
		s.disabled, s.stale = false, f.Propagation
		f.nextOp++
		name := "operations/acf.p2-" + strconv.Itoa(f.nextOp)
		if f.PendingPolls == 0 {
			writeJSON(w, http.StatusOK, doneOp(name, m[1], service))
			return
		}
		f.ops[name] = f.PendingPolls
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "metadata": map[string]any{"@type": "type.googleapis.com/google.api.serviceusage.v1.OperationMetadata"}})
		return
	}
	if m := usageOpRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodGet {
		left, ok := f.ops[m[1]]
		switch {
		case !ok:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "operation "+m[1]+" not found")
		case left > 0:
			f.ops[m[1]] = left - 1
			writeJSON(w, http.StatusOK, map[string]any{"name": m[1]})
		default:
			writeJSON(w, http.StatusOK, doneOp(m[1], "", ""))
		}
		return
	}
	f.unhandled(w, r)
}

// doneOp is a finished enable's operation.
func doneOp(name, project, service string) map[string]any {
	return map[string]any{"name": name, "done": true, "response": map[string]any{
		"@type":   "type.googleapis.com/google.api.serviceusage.v1.EnableServiceResponse",
		"service": map[string]any{"name": "projects/" + project + "/services/" + service, "state": "ENABLED"},
	}}
}
