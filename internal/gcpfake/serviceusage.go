package gcpfake

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// ServiceUsage is a fake of the Service Usage v1 calls Fugaro makes:
// a service's enable, the get of its operation, and a service's own get
// (`fugaro doctor` reads whether the Compute and Batch APIs are enabled
// before it reads anything else about a Batch installation). It also turns the
// other fakes' APIs off: while Disable has a service disabled, every call
// to the fakes serving it is answered as Google answers a call to a
// disabled API (403 with an ErrorInfo whose reason is SERVICE_DISABLED).
// An enable turns the service back on.
//
// A service's get answers ENABLED for one the fake was never told about
// (see the handler): the fake models "disabled" as something a test asserts,
// not as the real API's default for a service nobody enabled.
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
	// Deactivating is how many enable calls are answered Google's "the
	// service is currently being deactivated" (400 FAILED_PRECONDITION),
	// which is what enabling a just-disabled service gets until the disable
	// has finished. It counts down with each enable.
	Deactivating int

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
	usageGetRE    = regexp.MustCompile(`^/v1/projects/([^/]+)/services/([^/:]+)$`)
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
	if m := usageGetRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodGet {
		// A service's get, as `fugaro doctor` reads a Batch installation's
		// two APIs with. The fake answers a service it was never told about
		// as ENABLED, which is the same thing Enabled() says and the
		// opposite of what the real API answers for a service that was never
		// enabled: a test that wants a disabled API says so with Disable,
		// and a client that forgot to read the state is what a fake
		// defaulting to ENABLED catches. The get deliberately does not count
		// down Propagation -- that counts calls to the service's own API
		// (refuses), not to Service Usage.
		s := f.services[m[2]]
		state := "ENABLED"
		if s != nil && s.disabled {
			state = "DISABLED"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name":  "projects/" + m[1] + "/services/" + m[2],
			"state": state,
			"config": map[string]any{
				"name": m[2], "title": m[2], "usage": map[string]any{"requirements": []any{}},
			},
		})
		return
	}
	if m := usageEnableRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodPost {
		service := m[2]
		if f.Deactivating > 0 {
			f.Deactivating--
			writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION",
				"The service "+service+" is currently being deactivated. Please try again later.")
			return
		}
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
