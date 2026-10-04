package gcpfake

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// FirebaseMgmt is a fake of the Firebase Management v1beta1 calls fugaro
// init makes to create a project: projects.get, projects.addFirebase and the
// get of its operation. A project that the Resource Manager fake (UseCRM)
// does not know is refused with 403; one without Firebase answers a get 404.
type FirebaseMgmt struct {
	*Server

	// PendingPolls is how many gets of an addFirebase's operation answer it
	// not done yet.
	PendingPolls int

	mu      sync.Mutex
	crm     *CRM
	have    map[string]bool
	ops     map[string]int
	adds    []string
	failAdd *apiError
	nextOp  int
}

var (
	fbMgmtProjectRE = regexp.MustCompile(`^/v1beta1/projects/([^/:]+)$`)
	fbMgmtAddRE     = regexp.MustCompile(`^/v1beta1/projects/([^/:]+):addFirebase$`)
	fbMgmtOpRE      = regexp.MustCompile(`^/v1beta1/(operations/[^/]+)$`)
)

// NewFirebaseMgmt starts the fake; it lives until the test ends.
func NewFirebaseMgmt(t *testing.T) *FirebaseMgmt {
	t.Helper()
	f := &FirebaseMgmt{have: map[string]bool{}, ops: map[string]int{}}
	f.Server = newServer(t, f.handle)
	return f
}

// UseCRM makes the projects c knows the ones this fake serves.
func (f *FirebaseMgmt) UseCRM(c *CRM) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.crm = c
}

// AddFirebase makes the project already have Firebase.
func (f *FirebaseMgmt) AddFirebase(project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.have[project] = true
}

// FailAdds makes every addFirebase answer code, status and msg. A code of 0
// lifts it.
func (f *FirebaseMgmt) FailAdds(code int, status, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAdd = nil
	if code != 0 {
		f.failAdd = &apiError{code: code, status: status, msg: msg}
	}
}

// Adds are the projects addFirebase was accepted for.
func (f *FirebaseMgmt) Adds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.adds...)
}

func (f *FirebaseMgmt) known(id string) bool {
	return f.crm == nil || f.crm.Has(id) || f.have[id]
}

func (f *FirebaseMgmt) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case fbMgmtAddRE.MatchString(r.URL.Path) && r.Method == http.MethodPost:
		id := fbMgmtAddRE.FindStringSubmatch(r.URL.Path)[1]
		if !f.known(id) {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return
		}
		if f.failAdd != nil {
			writeError(w, f.failAdd.code, f.failAdd.status, f.failAdd.msg)
			return
		}
		f.nextOp++
		name := "operations/af." + strconv.Itoa(f.nextOp)
		f.ops[name] = f.PendingPolls
		f.adds = append(f.adds, id)
		f.have[id] = true
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": false})
	case fbMgmtOpRE.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		name := fbMgmtOpRE.FindStringSubmatch(r.URL.Path)[1]
		n, ok := f.ops[name]
		switch {
		case !ok:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "operation not found")
		case n > 0:
			f.ops[name] = n - 1
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": false})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": true, "response": map[string]any{"@type": "type.googleapis.com/google.firebase.v1beta1.FirebaseProject"}})
		}
	case fbMgmtProjectRE.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		id := fbMgmtProjectRE.FindStringSubmatch(r.URL.Path)[1]
		switch {
		case !f.known(id):
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		case !f.have[id]:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		default:
			writeJSON(w, http.StatusOK, map[string]any{"projectId": id, "name": "projects/" + id, "state": "ACTIVE"})
		}
	default:
		f.unhandled(w, r)
	}
}
