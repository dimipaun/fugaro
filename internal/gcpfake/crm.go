package gcpfake

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// CRM is a fake of the Cloud Resource Manager v1 calls Fugaro makes:
// projects get, for the project's number, its display name and its labels,
// and projects update, for the label the run that created a project writes on
// it. An update replaces the project's editable state with what the request
// carries, as the real API does, so a caller must send back the project it
// read (see handle, and TestProjectUpdateReplacesTheProjectsEditableFields).
//
// Like the real API, it answers a project it doesn't know with 403, not
// 404, so a caller can't tell a missing project from one it may not read.
type CRM struct {
	*Server

	mu       sync.Mutex
	projects map[string]int64
	labels   map[string]map[string]string
	names    map[string]string // the display name, as the v1 API answers it under "name"
	named    map[string]bool   // whether the fake knows a name; a project only added answers its ID
	policies map[string][]Binding
	states   map[string]string
	noGet    map[string]bool // projects whose plain read answers 403 (the policy stays readable)
	noUpdate map[string]bool // projects whose update answers 403 (the plain read stays readable)

	// PropagationReads is how many reads of a project answer 403 right
	// after its creation, as happens while a new project propagates.
	PropagationReads int
	// PendingPolls is how many gets of a create's operation answer it not
	// done yet.
	PendingPolls int

	noPolicy map[string]bool      // projects whose getIamPolicy answers 403 (the plain read stays readable)
	taken    map[string]bool      // IDs held by projects the caller cannot read
	creates  []CreatedProject     // creates accepted, in order
	failNew  *apiError            // answers every create (an organization policy, say)
	failGet  map[string]*apiError // answers the plain read of a project with an error of the test's own words
	hide     map[string]int       // reads still refused per new project
	ops      map[string]*crmOp    // operations by name
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

// FirstCreatedProjectNumber is the number the fake gives the first project a
// test creates through projects.create: nextProj starts just below it and is
// incremented before the record is made. A test that has to know a created
// project's number before the run — to seed the bucket the creation fact goes
// into, which is checked against that number — reads it here rather than
// repeating the constant.
const FirstCreatedProjectNumber = 900000000001

var (
	crmProjectRE = regexp.MustCompile(`^/v1/projects/([^/:]+)$`)
	crmPolicyRE  = regexp.MustCompile(`^/v1/projects/([^/:]+):getIamPolicy$`)
	crmV3GetRE   = regexp.MustCompile(`^/v3/projects/([^/:]+)$`)
	crmV3OpRE    = regexp.MustCompile(`^/v3/(operations/[^/]+)$`)
)

// NewCRM starts a Resource Manager fake that lives until the test ends.
func NewCRM(t *testing.T) *CRM {
	t.Helper()
	f := &CRM{projects: map[string]int64{}, labels: map[string]map[string]string{}, names: map[string]string{}, named: map[string]bool{},
		policies: map[string][]Binding{}, noGet: map[string]bool{}, states: map[string]string{},
		noPolicy: map[string]bool{}, noUpdate: map[string]bool{}, taken: map[string]bool{}, hide: map[string]int{}, ops: map[string]*crmOp{},
		nextProj: FirstCreatedProjectNumber - 1}
	f.Server = newServer(t, f.handle)
	return f
}

// AddProject makes project id exist with number.
func (f *CRM) AddProject(id string, number int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[id] = number
}

// SetLabels makes project carry labels, as its own read answers them. It adds
// to what is there rather than replacing it, so a test can set the label a
// project was created with and the ones an update adds without one wiping the
// other.
func (f *CRM) SetLabels(project string, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.labels[project] == nil {
		f.labels[project] = map[string]string{}
	}
	for k, v := range labels {
		f.labels[project][k] = v
	}
}

// Labels is what project carries now, as a copy: a test reads it to see what
// an update left behind.
func (f *CRM) Labels(project string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.labels[project]))
	for k, v := range f.labels[project] {
		out[k] = v
	}
	return out
}

// SetDisplayName gives project a display name of its own, the field the v1
// API answers as "name" and an update replaces whole (see handle).
func (f *CRM) SetDisplayName(project, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names[project] = name
	f.named[project] = true
}

// DisplayName is the project's display name as the fake answers it: the name
// it was given, and its own ID for a project that only exists (AddProject
// makes a project exist and nothing more, which is what this fake answered
// before it knew about display names at all). It is "" for a project whose
// name an update took off it, which is the state a caller that sends a
// body without one leaves behind.
func (f *CRM) DisplayName(project string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.displayName(project)
}

// displayName is DisplayName's answer. The caller holds mu.
func (f *CRM) displayName(id string) string {
	if !f.named[id] {
		return id
	}
	return f.names[id]
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

// DenyUpdate makes project's update answer 403 while its plain read stays
// readable: a caller that holds resourcemanager.projects.get and not
// resourcemanager.projects.update, which is every credential a run holds —
// the write permission the project label needs (Task 3.0, design finding 14)
// and the one no run account may have.
func (f *CRM) DenyUpdate(project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noUpdate[project] = true
}

// FailGet makes the plain read of project answer code, status and msg: an
// API error whose words are the test's, so a caller's handling of the far
// end's own text (a hostile message, say) can be exercised. A code of 0 lifts
// it.
func (f *CRM) FailGet(project string, code int, status, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGet == nil {
		f.failGet = map[string]*apiError{}
	}
	delete(f.failGet, project)
	if code != 0 {
		f.failGet[project] = &apiError{code: code, status: status, msg: msg}
	}
}

// DenyPolicy makes project's getIamPolicy answer 403 while its plain read
// stays readable (the opposite of DenyGet): a caller that holds some
// project-level roles but not resourcemanager.projects.getIamPolicy.
func (f *CRM) DenyPolicy(project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noPolicy[project] = true
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
		if _, ok := f.projects[m[1]]; !ok || f.noPolicy[m[1]] {
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
	if m == nil || (r.Method != http.MethodGet && r.Method != http.MethodPut) {
		f.unhandled(w, r)
		return
	}
	if e := f.failGet[m[1]]; e != nil {
		writeError(w, e.code, e.status, e.msg)
		return
	}
	n, ok := f.projects[m[1]]
	if !ok || f.noGet[m[1]] {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	if r.Method == http.MethodPut {
		if f.noUpdate[m[1]] {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return
		}
		var in struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a project is required")
			return
		}
		// The real API replaces the project's editable state with what the
		// request carries — the display name and the whole label map — not
		// just the fields the caller meant to change. That is what makes a
		// writer that sends only its own key drop every other label, and one
		// that sends a project object it built rather than the one it read
		// take the owner's display name off the project. gcloud and Terraform
		// send the whole object back, which is why they change one field.
		f.names[m[1]] = in.Name
		f.named[m[1]] = true
		f.labels[m[1]] = in.Labels
	}
	writeJSON(w, http.StatusOK, f.projectJSON(m[1], n))
}

// projectJSON is a project as the v1 API answers it: the number is a string,
// and the display name (the resource's "name") and labels are there only when
// the project carries any — the client's omitempty, on the way back.
func (f *CRM) projectJSON(id string, n int64) map[string]any {
	out := map[string]any{
		"projectId": id, "projectNumber": strconv.FormatInt(n, 10), "lifecycleState": cmpOr(f.states[id], "ACTIVE"),
	}
	if name := f.displayName(id); name != "" {
		out["name"] = name
	}
	if ls := f.labels[id]; len(ls) > 0 {
		out["labels"] = ls
	}
	return out
}

func cmpOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
