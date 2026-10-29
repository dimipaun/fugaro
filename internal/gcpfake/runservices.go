package gcpfake

import (
	"net/http"
	"regexp"
	"sync"
	"testing"
)

// RunServices is a stateful fake of the Cloud Run Admin v2 *services* calls
// Fugaro makes: getIamPolicy, and nothing else. It is a type of its own
// rather than a second arm of Run because a service and a job are separate
// resources with separate policies: who may start a job (Run, the launcher
// check) and who may deploy a service (this one, the credentials-service
// deployer read, plan B0.3/D501) are different questions asked of different
// resources.
//
// A call it does not implement — a service get, a job path, a revision —
// fails the test, as every other fake here does.
type RunServices struct {
	*Server

	mu       sync.Mutex
	services map[string]*fakeRunService // by short name
}

// fakeRunService is one service's policy; the service itself is only a name
// here, since the fake serves no get.
type fakeRunService struct {
	policy []Binding
}

// runServicePolicyRE splits a service's parent (its project and location)
// from its short name: the fake keys services by short name, as Run keys
// jobs, so a caller may address one with any project or location.
var runServicePolicyRE = regexp.MustCompile(`^/v2/(projects/[^/]+/locations/[^/]+)/services/([^/:]+):getIamPolicy$`)

// NewRunServices starts a Cloud Run v2 services fake that lives until the
// test ends.
func NewRunServices(t *testing.T) *RunServices {
	t.Helper()
	f := &RunServices{services: map[string]*fakeRunService{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddService makes the service name exist, with no policy: a service nobody
// granted anything on serves the empty policy getIamPolicy answers with.
func (f *RunServices) AddService(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.services[name] = &fakeRunService{}
}

// SetServicePolicy sets the IAM policy of service name, which must exist.
func (f *RunServices) SetServicePolicy(name string, bindings ...Binding) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.services[name]
	if s == nil {
		f.t.Fatalf("gcpfake: SetServicePolicy(%q): no such service", name)
	}
	s.policy = cloneBindings(bindings)
}

func (f *RunServices) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := runServicePolicyRE.FindStringSubmatch(r.URL.Path); r.Method == http.MethodGet && m != nil {
		s := f.services[m[2]]
		if s == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Service ["+m[1]+"/services/"+m[2]+"] not found.")
			return
		}
		servePolicy(w, r.URL.Query(), s.policy)
		return
	}
	f.unhandled(w, r)
}
