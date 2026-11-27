package gcpfake

import (
	"encoding/json"
	"maps"
	"net/http"
	"regexp"
	"slices"
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
// It serves the v3 calls the folder authority makes besides them
// (internal/infra/folder.go): folders.create (an operation, polled),
// folders.get, folders.getIamPolicy and folders.setIamPolicy,
// organizations.search, projects.move (an operation) and the project read
// that answers a project's parent. A project's parent is what it was added,
// created or moved with, so a move is visible in the read that follows it
// (ProjectParent, Moves). v1 projects.setIamPolicy is served too, for the
// same one-writer reason in reverse: the end-to-end test (WU21, level 5) drives
// a member's own policy write against this fake, and WU2 is the only writer
// crm.go has, so the write is here before the test that needs it.
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
	audits   map[string][]AuditConfig // a project's audit-logging configuration
	states   map[string]string
	noGet    map[string]bool // projects whose plain read answers 403 (the policy stays readable)
	noUpdate map[string]bool // projects whose update answers 403 (the plain read stays readable)

	folders        map[string]gcpFolder // folder ID (the number) -> what the folder is
	folderPolicies map[string][]Binding
	folderAudits   map[string][]AuditConfig
	orgs           []string          // the organizations organizations.search answers
	parents        map[string]string // a project's parent, once the fake knows one
	moves          []ProjectMove
	nextFolder     int64

	// PropagationReads is how many reads of a project answer 403 right
	// after its creation, as happens while a new project propagates.
	PropagationReads int
	// PendingPolls is how many gets of a create's operation answer it not
	// done yet.
	PendingPolls int

	noPolicy map[string]bool      // projects whose getIamPolicy answers 403 (the plain read stays readable)
	held     map[string][]string  // what testIamPermissions reports the caller holds, by project
	taken    map[string]bool      // IDs held by projects the caller cannot read
	creates  []CreatedProject     // creates accepted, in order
	failNew  *apiError            // answers every create (an organization policy, say)
	failOp   *crmOpError          // the create's operation completes with this error
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
	// Labels are the labels the create request carried.
	Labels map[string]string
}

// gcpFolder is one folder: its display name and its parent (organizations/N,
// folders/N or "").
type gcpFolder struct{ DisplayName, Parent string }

// AuditConfig is one service's audit-logging configuration in a policy: the
// service it covers and the log types it sets. It is what a folder policy
// serves under "auditConfigs", and the shape a write carries back.
type AuditConfig struct {
	Service         string           `json:"service"`
	AuditLogConfigs []AuditLogConfig `json:"auditLogConfigs,omitempty"`
}

// AuditLogConfig is one log type an audit configuration sets, with the members
// exempted from it.
type AuditLogConfig struct {
	LogType         string   `json:"logType"`
	ExemptedMembers []string `json:"exemptedMembers,omitempty"`
}

// ProjectMove is a move the fake accepted, in the order the moves completed.
type ProjectMove struct{ Project, Parent string }

type crmOp struct {
	polls int
	proj  CreatedProject
	// kind is what the operation carries: "project" for a create (proj),
	// "folder" for a folder create (folder is the folder's ID, and the
	// operation answers the folder the create made) or "move" (move is the
	// destination the project goes to when the operation completes).
	kind   string
	folder string
	move   ProjectMove
}

// crmOpError is an operation's own error: a google.rpc.Code and its message,
// as the v3 API reports one.
type crmOpError struct {
	code int64
	msg  string
}

// FirstCreatedProjectNumber is the number the fake gives the first project a
// test creates through projects.create: nextProj starts just below it and is
// incremented before the record is made. A test that has to know a created
// project's number before the run — to seed the bucket the creation fact goes
// into, which is checked against that number — reads it here rather than
// repeating the constant.
const FirstCreatedProjectNumber = 900000000001

// FirstCreatedFolderNumber is the number the fake gives the first folder a
// test creates through folders.create, on the same terms: nextFolder starts
// just below it.
const FirstCreatedFolderNumber = 700000000001

var (
	crmProjectRE     = regexp.MustCompile(`^/v1/projects/([^/:]+)$`)
	crmPolicyRE      = regexp.MustCompile(`^/v1/projects/([^/:]+):getIamPolicy$`)
	crmSetPolicyRE   = regexp.MustCompile(`^/v1/projects/([^/:]+):setIamPolicy$`)
	crmTestPermsRE   = regexp.MustCompile(`^/v1/projects/([^/:]+):testIamPermissions$`)
	crmV3GetRE       = regexp.MustCompile(`^/v3/projects/([^/:]+)$`)
	crmV3MoveRE      = regexp.MustCompile(`^/v3/projects/([^/:]+):move$`)
	crmV3OpRE        = regexp.MustCompile(`^/v3/(operations/[^/]+)$`)
	crmV3FolderRE    = regexp.MustCompile(`^/v3/folders/([^/:]+)$`)
	crmV3FolderPolRE = regexp.MustCompile(`^/v3/folders/([^/:]+):(get|set)IamPolicy$`)
)

// NewCRM starts a Resource Manager fake that lives until the test ends.
func NewCRM(t *testing.T) *CRM {
	t.Helper()
	f := &CRM{projects: map[string]int64{}, labels: map[string]map[string]string{}, names: map[string]string{}, named: map[string]bool{},
		policies: map[string][]Binding{}, audits: map[string][]AuditConfig{}, noGet: map[string]bool{}, states: map[string]string{},
		noPolicy: map[string]bool{}, held: map[string][]string{}, noUpdate: map[string]bool{}, taken: map[string]bool{}, hide: map[string]int{}, ops: map[string]*crmOp{},
		folders: map[string]gcpFolder{}, folderPolicies: map[string][]Binding{}, folderAudits: map[string][]AuditConfig{}, parents: map[string]string{},
		nextProj: FirstCreatedProjectNumber - 1, nextFolder: FirstCreatedFolderNumber - 1}
	f.Server = newServer(t, f.handle)
	return f
}

// AddFolder makes folder id exist, named displayName under parent.
func (f *CRM) AddFolder(id, displayName, parent string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folders[id] = gcpFolder{DisplayName: displayName, Parent: parent}
}

// SetFolderPolicy sets the bindings of folder's IAM policy.
func (f *CRM) SetFolderPolicy(id string, bindings ...Binding) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folderPolicies[id] = bindings
}

// FolderPolicy is folder's IAM policy bindings as the fake holds them now, so a
// test can read what a write left behind.
func (f *CRM) FolderPolicy(id string) []Binding {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Binding(nil), f.folderPolicies[id]...)
}

// SetFolderAuditConfigs sets the audit-logging configuration of folder's IAM
// policy, which a policy write carries back whole alongside its bindings.
func (f *CRM) SetFolderAuditConfigs(id string, configs ...AuditConfig) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folderAudits[id] = configs
}

// FolderAuditConfigs is folder's audit-logging configuration as the fake holds
// it now: empty when a write did not carry back what the read answered, which
// is what a policy rebuilt from its bindings alone looks like.
func (f *CRM) FolderAuditConfigs(id string) []AuditConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]AuditConfig(nil), f.folderAudits[id]...)
}

// AddOrganization makes organizations.search answer id.
func (f *CRM) AddOrganization(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgs = append(f.orgs, id)
}

// SetParent makes a read of project answer parent, for a project no create or
// move of the test's put anywhere.
func (f *CRM) SetParent(project, parent string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parents[project] = parent
}

// ProjectParent is where a read of project puts it: the parent it was added,
// created or moved with, and "" for a project the fake knows none for.
func (f *CRM) ProjectParent(project string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.parents[project]
}

// Moves are the moves the fake completed, in order.
func (f *CRM) Moves() []ProjectMove {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ProjectMove(nil), f.moves...)
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

// SetAuditConfigs sets the audit-logging configuration of project's IAM policy,
// which a policy write carries back whole alongside its bindings: the real
// projects.setIamPolicy replaces the WHOLE policy, so a writer that sent a
// policy built from its bindings alone takes the project's data-access logging
// off it.
func (f *CRM) SetAuditConfigs(project string, configs ...AuditConfig) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits[project] = configs
}

// AuditConfigs is project's audit-logging configuration as the fake holds it
// now: empty when a write did not carry back what the read answered.
func (f *CRM) AuditConfigs(project string) []AuditConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]AuditConfig(nil), f.audits[project]...)
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

// SetHeldPermissions sets what testIamPermissions on project reports the
// caller holds (by default nothing). The read is about the caller's own
// permissions, so what it answers is set rather than derived from the
// project's policy, exactly as the real API answers it.
func (f *CRM) SetHeldPermissions(project string, perms ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held[project] = perms
}

// testPermissions answers projects.testIamPermissions: the permissions asked
// for that the caller holds, in the order they were asked, and nothing else —
// a permission the caller does not hold is left out, which is how the real API
// answers it (never with a refusal: a caller that holds none of them is
// answered an empty list). The caller holds mu, and has checked that the
// project is one this caller may read.
func (f *CRM) testPermissions(w http.ResponseWriter, project string, body []byte) {
	var in struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a list of permissions is required")
		return
	}
	var got []string
	for _, p := range in.Permissions {
		if slices.Contains(f.held[project], p) {
			got = append(got, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": got})
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

// FailOperation makes every create's operation complete with error code and
// message, as the API does when the refusal comes back asynchronously (an
// organization policy, say): the create itself is accepted and the caller only
// learns the truth by polling — and, as with the real API, no project exists
// when the operation ends that way (the record the create made is dropped, so
// Has and Creates answer no project, which is what a test asserts). code is a
// google.rpc.Code — 7 PERMISSION_DENIED, 8 RESOURCE_EXHAUSTED, 9
// FAILED_PRECONDITION. A code of 0 lifts it.
func (f *CRM) FailOperation(code int64, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOp = nil
	if code != 0 {
		f.failOp = &crmOpError{code: code, msg: msg}
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

// drop forgets everything the fake recorded about a project the caller
// created and then lost: a create whose operation ends in an error leaves no
// project behind. The caller holds mu.
func (f *CRM) drop(id string) {
	delete(f.projects, id)
	delete(f.hide, id)
	for i := len(f.creates) - 1; i >= 0; i-- {
		if f.creates[i].ID == id {
			f.creates = append(f.creates[:i], f.creates[i+1:]...)
			break
		}
	}
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
	Name        string            `json:"name"`
	ProjectID   string            `json:"projectId"`
	DisplayName string            `json:"displayName,omitempty"`
	State       string            `json:"state"`
	Parent      string            `json:"parent,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

func (f *CRM) v3Project(id string) v3Project {
	p := v3Project{Name: "projects/" + strconv.FormatInt(f.projects[id], 10), ProjectID: id, DisplayName: id, State: cmpOr(f.states[id], "ACTIVE")}
	p.Parent = f.parents[id]
	if ls := f.labels[id]; len(ls) > 0 {
		p.Labels = maps.Clone(ls)
	}
	return p
}

// handleV3 serves projects.create, projects.get, projects.move,
// folders.create, folders.get, the folder policy, organizations.search and
// the operations both creates and the move poll.
func (f *CRM) handleV3(w http.ResponseWriter, r *http.Request, body []byte) bool {
	switch {
	case r.URL.Path == "/v3/projects" && r.Method == http.MethodPost:
		var in struct {
			ProjectID   string            `json:"projectId"`
			DisplayName string            `json:"displayName"`
			Parent      string            `json:"parent"`
			Labels      map[string]string `json:"labels"`
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
		p := CreatedProject{ID: in.ProjectID, DisplayName: in.DisplayName, Parent: in.Parent, Number: f.nextProj, Labels: maps.Clone(in.Labels)}
		f.projects[p.ID] = p.Number
		if in.Parent != "" {
			f.parents[p.ID] = in.Parent
		}
		if len(in.Labels) > 0 {
			f.labels[p.ID] = maps.Clone(in.Labels)
		}
		f.hide[p.ID] = f.PropagationReads
		f.creates = append(f.creates, p)
		name := "operations/cp." + strconv.Itoa(f.nextOp)
		f.ops[name] = &crmOp{polls: f.PendingPolls, kind: "project", proj: p}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": false})
		return true
	case r.URL.Path == "/v3/folders" && r.Method == http.MethodPost:
		var in struct {
			DisplayName string `json:"displayName"`
			Parent      string `json:"parent"`
		}
		if err := json.Unmarshal(body, &in); err != nil || in.DisplayName == "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a folder display name is required")
			return true
		}
		f.nextFolder++
		f.nextOp++
		id := strconv.FormatInt(f.nextFolder, 10)
		f.folders[id] = gcpFolder{DisplayName: in.DisplayName, Parent: in.Parent}
		name := "operations/cf." + strconv.Itoa(f.nextOp)
		f.ops[name] = &crmOp{polls: f.PendingPolls, kind: "folder", folder: id}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": false})
		return true
	case r.URL.Path == "/v3/organizations:search" && r.Method == http.MethodGet:
		out := make([]map[string]any, 0, len(f.orgs))
		for _, id := range f.orgs {
			out = append(out, map[string]any{"name": "organizations/" + id, "displayName": "organizations/" + id,
				"state": "ACTIVE"})
		}
		writeJSON(w, http.StatusOK, map[string]any{"organizations": out})
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
		if f.failOp != nil {
			// The real API leaves nothing behind when an operation ends in an
			// error: a create's project never existed (the record the create
			// made is dropped with it, so a test can assert that nothing was
			// left behind — nothingDone reads Creates) and a move's project
			// stays where it was.
			if op.kind == "project" {
				f.drop(op.proj.ID)
			}
			if op.kind == "folder" {
				delete(f.folders, op.folder)
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": true,
				"error": map[string]any{"code": f.failOp.code, "message": f.failOp.msg}})
			return true
		}
		switch op.kind {
		case "folder":
			fo := f.folders[op.folder]
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": true,
				"response": map[string]any{"@type": "type.googleapis.com/google.cloud.resourcemanager.v3.Folder",
					"name": "folders/" + op.folder, "displayName": fo.DisplayName, "parent": fo.Parent, "state": "ACTIVE"}})
			return true
		case "move":
			// The move lands when its operation completes, as the API's does:
			// the project's read answers the new parent from here on.
			f.parents[op.move.Project] = op.move.Parent
			f.moves = append(f.moves, op.move)
			p := f.v3Project(op.move.Project)
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": true,
				"response": map[string]any{"@type": "type.googleapis.com/google.cloud.resourcemanager.v3.Project",
					"name": p.Name, "projectId": p.ProjectID, "state": p.State, "parent": p.Parent}})
			return true
		}
		resp := f.v3Project(op.proj.ID)
		resp.DisplayName = op.proj.DisplayName
		resp.Parent = op.proj.Parent
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": true,
			"response": map[string]any{"@type": "type.googleapis.com/google.cloud.resourcemanager.v3.Project",
				"name": resp.Name, "projectId": resp.ProjectID, "state": resp.State}})
		return true
	case crmV3MoveRE.MatchString(r.URL.Path) && r.Method == http.MethodPost:
		id := crmV3MoveRE.FindStringSubmatch(r.URL.Path)[1]
		if _, ok := f.projects[id]; !ok {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return true
		}
		var in struct {
			DestinationParent string `json:"destinationParent"`
		}
		if err := json.Unmarshal(body, &in); err != nil || in.DestinationParent == "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a destination parent is required")
			return true
		}
		f.nextOp++
		name := "operations/cm." + strconv.Itoa(f.nextOp)
		f.ops[name] = &crmOp{polls: f.PendingPolls, kind: "move", move: ProjectMove{Project: id, Parent: in.DestinationParent}}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "done": false})
		return true
	case crmV3FolderPolRE.MatchString(r.URL.Path) && r.Method == http.MethodPost:
		m := crmV3FolderPolRE.FindStringSubmatch(r.URL.Path)
		id, verb := m[1], m[2]
		if _, ok := f.folders[id]; !ok {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return true
		}
		if verb == "set" {
			var in struct {
				Policy struct {
					Bindings     []Binding     `json:"bindings"`
					AuditConfigs []AuditConfig `json:"auditConfigs"`
				} `json:"policy"`
			}
			if err := json.Unmarshal(body, &in); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a policy is required")
				return true
			}
			f.folderPolicies[id] = in.Policy.Bindings
			f.folderAudits[id] = in.Policy.AuditConfigs
		}
		bs := f.folderPolicies[id]
		if bs == nil {
			bs = []Binding{}
		}
		ac := f.folderAudits[id]
		if ac == nil {
			ac = []AuditConfig{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": 3, "etag": "BwFolder", "bindings": bs, "auditConfigs": ac})
		return true
	case crmV3FolderRE.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		id := crmV3FolderRE.FindStringSubmatch(r.URL.Path)[1]
		fo, ok := f.folders[id]
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "folder not found")
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": "folders/" + id, "displayName": fo.DisplayName,
			"parent": fo.Parent, "state": "ACTIVE"})
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
	if m := crmSetPolicyRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodPost {
		if _, ok := f.projects[m[1]]; !ok || f.noPolicy[m[1]] {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return
		}
		var in struct {
			Policy struct {
				Bindings []Binding `json:"bindings"`
			} `json:"policy"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "a policy is required")
			return
		}
		f.policies[m[1]] = in.Policy.Bindings
		bs := f.policies[m[1]]
		if bs == nil {
			bs = []Binding{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": 3, "etag": "BwX1", "bindings": bs})
		return
	}
	if m := crmTestPermsRE.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodPost {
		// A project this caller cannot read answers 403 rather than an empty
		// list, as the fake's other IAM reads do: an empty list is the real
		// API's answer to a caller that holds none of the permissions it
		// asked about, and the two must not be one answer.
		if _, ok := f.projects[m[1]]; !ok || f.noPolicy[m[1]] || f.noGet[m[1]] {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return
		}
		f.testPermissions(w, m[1], body)
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
