package gcpfake

import (
	"encoding/json"
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
	policies map[string][]Binding
	states   map[string]string
	noGet    map[string]bool // projects whose plain read answers 403 (the policy stays readable)

	// PropagationReads is how many reads of a project answer 403 right
	// after its creation, as happens while a new project propagates.
	PropagationReads int
	// PendingPolls is how many gets of a create's operation answer it not
	// done yet.
	PendingPolls int

	taken    map[string]bool   // IDs held by projects the caller cannot read
	creates  []CreatedProject  // creates accepted, in order
	failNew  *apiError         // answers every create (an organization policy, say)
	hide     map[string]int    // reads still refused per new project
	ops      map[string]*crmOp // operations by name
	nextOp   int
	nextProj int64
}

// CreatedProject is a create the fake accepted.
type CreatedProject struct {
	ID, DisplayName, Parent string
	Number                  int64
}

type crmOp struct {
	polls int
	proj  CreatedProject
}

var (
	crmProjectRE = regexp.MustCompile(`^/v1/projects/([^/:]+)$`)
	crmPolicyRE  = regexp.MustCompile(`^/v1/projects/([^/:]+):getIamPolicy$`)
	crmV3GetRE   = regexp.MustCompile(`^/v3/projects/([^/:]+)$`)
	crmV3OpRE    = regexp.MustCompile(`^/v3/(operations/[^/]+)$`)
)

// NewCRM starts a Resource Manager fake that lives until the test ends.
func NewCRM(t *testing.T) *CRM {
	t.Helper()
	f := &CRM{projects: map[string]int64{}, policies: map[string][]Binding{}, noGet: map[string]bool{}, states: map[string]string{},
		taken: map[string]bool{}, hide: map[string]int{}, ops: map[string]*crmOp{}, nextProj: 900000000000}
	f.Server = newServer(t, f.handle)
	return f
}

// AddProject makes project id exist with number.
func (f *CRM) AddProject(id string, number int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[id] = number
}

// SetPolicy sets the bindings of project's IAM policy.
func (f *CRM) SetPolicy(project string, bindings ...Binding) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies[project] = bindings
}

// DenyGet makes the plain read of project (its number, say) answer 403 while
// its IAM policy stays readable.
func (f *CRM) DenyGet(project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noGet[project] = true
}

// SetLifecycle makes project report state (ACTIVE by default).
func (f *CRM) SetLifecycle(project, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[project] = state
}

// AddForeign makes id taken by a project the caller cannot read: a read
// answers 403 (as for a missing one) and a create answers 409.
func (f *CRM) AddForeign(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taken[id] = true
}

// FailCreates makes every create answer code, status and msg (a 403 for an
// organization policy, say). A code of 0 lifts it.
func (f *CRM) FailCreates(code int, status, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNew = nil
	if code != 0 {
		f.failNew = &apiError{code: code, status: status, msg: msg}
	}
}

// Creates are the projects created so far.
func (f *CRM) Creates() []CreatedProject {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]CreatedProject(nil), f.creates...)
}

// Has reports whether the project exists (created or added).
func (f *CRM) Has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.projects[id]
	return ok
}

// visible reports whether a read of id may answer now, counting down the
// propagation of a new project. The caller holds mu.
func (f *CRM) visible(id string) bool {
	if n := f.hide[id]; n > 0 {
		f.hide[id] = n - 1
		return false
	}
	return true
}

type v3Project struct {
	Name        string `json:"name"`
	ProjectID   string `json:"projectId"`
	DisplayName string `json:"displayName,omitempty"`
	State       string `json:"state"`
	Parent      string `json:"parent,omitempty"`
}

func (f *CRM) v3Project(id string) v3Project {
	return v3Project{Name: "projects/" + strconv.FormatInt(f.projects[id], 10), ProjectID: id, DisplayName: id, State: cmpOr(f.states[id], "ACTIVE")}
}

// handleV3 serves projects.create, its operation and projects.get.
func (f *CRM) handleV3(w http.ResponseWriter, r *http.Request, body []byte) bool {
	switch {
	case r.URL.Path == "/v3/projects" && r.Method == http.MethodPost:
		var in struct {
			ProjectID   string `json:"projectId"`
			DisplayName string `json:"displayName"`
			Parent      string `json:"parent"`
		}
		if err := json.Unmarshal(body, &in); err != nil || in.ProjectID == "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a project ID is required")
			return true
		}
		if f.failNew != nil {
			writeError(w, f.failNew.code, f.failNew.status, f.failNew.msg)
			return true
		}
		if _, ok := f.projects[in.ProjectID]; ok || f.taken[in.ProjectID] {
			writeError(w, http.StatusConflict, "ALREADY_EXISTS", "Requested entity already exists")
			return true
		}
		f.nextProj++
		f.nextOp++
		p := CreatedProject{ID: in.ProjectID, DisplayName: in.DisplayName, Parent: in.Parent, Number: f.nextProj}
		f.projects[p.ID] = p.Number
		f.hide[p.ID] = f.PropagationReads
		f.creates = append(f.creates, p)
		name := "operations/cp." + strconv.Itoa(f.nextOp)
		f.ops[name] = &crmOp{polls: f.PendingPolls, proj: p}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": false})
		return true
	case crmV3OpRE.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		name := crmV3OpRE.FindStringSubmatch(r.URL.Path)[1]
		op := f.ops[name]
		if op == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "operation not found")
			return true
		}
		if op.polls > 0 {
			op.polls--
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": false})
			return true
		}
		resp := f.v3Project(op.proj.ID)
		resp.DisplayName = op.proj.DisplayName
		resp.Parent = op.proj.Parent
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": true,
			"response": map[string]any{"@type": "type.googleapis.com/google.cloud.resourcemanager.v3.Project",
				"name": resp.Name, "projectId": resp.ProjectID, "state": resp.State}})
		return true
	case crmV3GetRE.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		id := crmV3GetRE.FindStringSubmatch(r.URL.Path)[1]
		if _, ok := f.projects[id]; !ok || !f.visible(id) {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return true
		}
		writeJSON(w, http.StatusOK, f.v3Project(id))
		return true
	}
	return false
}

func (f *CRM) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handleV3(w, r, body) {
		return
	}
	if m := crmPolicyRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodPost {
		if _, ok := f.projects[m[1]]; !ok {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return
		}
		bs := f.policies[m[1]]
		if bs == nil {
			bs = []Binding{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": 3, "etag": "BwX0", "bindings": bs})
		return
	}
	m := crmProjectRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	n, ok := f.projects[m[1]]
	if !ok || f.noGet[m[1]] {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projectId": m[1], "projectNumber": strconv.FormatInt(n, 10), "lifecycleState": cmpOr(f.states[m[1]], "ACTIVE"), "name": m[1],
	})
}

func cmpOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
