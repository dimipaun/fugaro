package gcpfake

import (
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Binding is one role binding of an IAM policy, as the GCS and Secret
// Manager fakes serve it.
type Binding struct {
	Role      string        `json:"role"`
	Members   []string      `json:"members"`
	Condition *IAMCondition `json:"condition,omitempty"`
}

// IAMCondition is a binding's condition.
type IAMCondition struct {
	Title       string `json:"title"`
	Expression  string `json:"expression"`
	Description string `json:"description,omitempty"`
}

// servePolicy answers a getIamPolicy call with bindings. As the real APIs
// do, it refuses a policy that holds a condition unless the caller asked
// for version 3, since a lower version would drop the conditions.
func servePolicy(w http.ResponseWriter, q url.Values, bindings []Binding) {
	version := 1
	for _, b := range bindings {
		if b.Condition != nil {
			version = 3
		}
	}
	requested, _ := strconv.Atoi(q.Get("optionsRequestedPolicyVersion"))
	if requested == 0 {
		requested, _ = strconv.Atoi(q.Get("options.requestedPolicyVersion"))
	}
	if version == 3 && requested < 3 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the policy has conditions; request policy version 3")
		return
	}
	if bindings == nil {
		bindings = []Binding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "etag": "BwX0", "bindings": bindings})
}

func cloneBindings(bs []Binding) []Binding {
	out := make([]Binding, len(bs))
	for i, b := range bs {
		out[i] = Binding{Role: b.Role, Members: append([]string(nil), b.Members...)}
		if b.Condition != nil {
			c := *b.Condition
			out[i].Condition = &c
		}
	}
	return out
}

// IAM is a fake of the IAM v1 calls Fugaro makes: serviceAccounts get,
// list and getIamPolicy, and a custom role's get (a project's or an
// organization's).
type IAM struct {
	*Server

	mu       sync.Mutex
	accounts map[string]map[string]string // projects/<p>/serviceAccounts/<email> → fields
	roles    map[string]map[string]any    // projects/<p>/roles/<id> → fields
	policies map[string][]Binding         // projects/<p>/serviceAccounts/<email> → its IAM policy
	hidden   map[string]bool              // accounts whose policy answers PERMISSION_DENIED
	disabled map[string]bool              // accounts served with disabled: true
	userKeys map[string]int               // accounts → how many user-managed keys they have
	// PageSize, when set, is the most accounts one list page holds, whatever
	// the request asks for, so a caller's paging is exercised.
	PageSize int
}

var (
	serviceAccountRE  = regexp.MustCompile(`^/v1/(projects/[^/]+/serviceAccounts/[^/:]+)$`)
	serviceAccountsRE = regexp.MustCompile(`^/v1/(projects/[^/]+)/serviceAccounts$`)
	saKeysRE          = regexp.MustCompile(`^/v1/(projects/[^/]+/serviceAccounts/[^/:]+)/keys$`)
	saPolicyRE        = regexp.MustCompile(`^/v1/(projects/[^/]+/serviceAccounts/[^/:]+):getIamPolicy$`)
	customRoleRE      = regexp.MustCompile(`^/v1/((?:(?:projects|organizations)/[^/]+/)?roles/[^/:]+)$`)
)

// NewIAM starts an IAM fake that lives until the test ends.
func NewIAM(t *testing.T) *IAM {
	t.Helper()
	f := &IAM{accounts: map[string]map[string]string{}, roles: map[string]map[string]any{}, policies: map[string][]Binding{}, hidden: map[string]bool{}, disabled: map[string]bool{}, userKeys: map[string]int{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddRole makes the project custom role id exist in project with title;
// deleted makes it a soft-deleted role, which the API still serves.
func (f *IAM) AddRole(project, id, title string, deleted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := "projects/" + project + "/roles/" + id
	f.roles[name] = map[string]any{"name": name, "title": title, "deleted": deleted, "stage": "GA", "etag": "BwX0"}
}

// SetRolePermissions sets the permissions the custom role name
// (projects/<p>/roles/<id> or organizations/<n>/roles/<id>) includes, as its
// get reports them; AddRole makes the role exist first.
func (f *IAM) SetRolePermissions(name string, perms ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.roles[name] == nil {
		f.roles[name] = map[string]any{"name": name, "title": name, "stage": "GA", "etag": "BwX0"}
	}
	f.roles[name]["includedPermissions"] = perms
}

// SetServiceAccountPolicy sets the IAM policy of the account email of
// project, as its getIamPolicy serves it.
func (f *IAM) SetServiceAccountPolicy(project, email string, bindings ...Binding) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies["projects/"+project+"/serviceAccounts/"+email] = cloneBindings(bindings)
}

// HidePolicy makes the getIamPolicy of the account projects/<p>/serviceAccounts/<email>
// answer PERMISSION_DENIED.
func (f *IAM) HidePolicy(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hidden[name] = true
}

// AddServiceAccount makes the account email exist in project with
// displayName.
func (f *IAM) AddServiceAccount(project, email, displayName string) {
	f.AddServiceAccountFull(project, email, displayName, "", false)
}

// AddServiceAccountFull makes the account email exist in project with
// displayName and description, disabled or not.
func (f *IAM) AddServiceAccountFull(project, email, displayName, description string, disabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disabled["projects/"+project+"/serviceAccounts/"+email] = disabled
	f.accounts["projects/"+project+"/serviceAccounts/"+email] = map[string]string{
		"name":        "projects/" + project + "/serviceAccounts/" + email,
		"email":       email,
		"projectId":   project,
		"displayName": displayName,
		"description": description,
		"uniqueId":    strconv.Itoa(100000 + len(f.accounts)),
	}
}

// AddUserKey gives the account email of project one more user-managed key.
func (f *IAM) AddUserKey(project, email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userKeys["projects/"+project+"/serviceAccounts/"+email]++
}

// account is the JSON of the account name's fields: strings, plus disabled.
func (f *IAM) account(name string) map[string]any {
	out := map[string]any{}
	for k, v := range f.accounts[name] {
		if k == "description" && v == "" {
			continue
		}
		out[k] = v
	}
	if f.disabled[name] {
		out["disabled"] = true
	}
	return out
}

// listKeys serves the keys of the account name; only the user-managed ones
// exist, so a keyTypes filter that leaves them out gets none.
func (f *IAM) listKeys(w http.ResponseWriter, r *http.Request, name string) {
	if f.accounts[name] == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Unknown service account")
		return
	}
	keys := []map[string]string{}
	types := r.URL.Query()["keyTypes"]
	if len(types) == 0 || slices.Contains(types, "USER_MANAGED") {
		for i := range f.userKeys[name] {
			id := "k" + strconv.Itoa(i+1)
			keys = append(keys, map[string]string{"name": name + "/keys/" + id, "keyType": "USER_MANAGED", "keyOrigin": "GOOGLE_PROVIDED",
				"validAfterTime": "2026-01-01T00:00:00Z", "validBeforeTime": "9999-12-31T23:59:59Z"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (f *IAM) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := customRoleRE.FindStringSubmatch(r.URL.Path); r.Method == http.MethodGet && m != nil {
		role := f.roles[m[1]]
		if role == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "The role named "+m[1]+" was not found.")
			return
		}
		writeJSON(w, http.StatusOK, maps.Clone(role))
		return
	}
	if m := saPolicyRE.FindStringSubmatch(r.URL.Path); r.Method == http.MethodPost && m != nil {
		if f.accounts[m[1]] == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Unknown service account")
			return
		}
		if f.hidden[m[1]] {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "Permission 'iam.serviceAccounts.getIamPolicy' denied")
			return
		}
		servePolicy(w, r.URL.Query(), f.policies[m[1]])
		return
	}
	if m := saKeysRE.FindStringSubmatch(r.URL.Path); r.Method == http.MethodGet && m != nil {
		f.listKeys(w, r, m[1])
		return
	}
	if m := serviceAccountsRE.FindStringSubmatch(r.URL.Path); r.Method == http.MethodGet && m != nil {
		f.listAccounts(w, r, m[1])
		return
	}
	m := serviceAccountRE.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || m == nil {
		f.unhandled(w, r)
		return
	}
	a := f.accounts[m[1]]
	if a == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Unknown service account")
		return
	}
	writeJSON(w, http.StatusOK, f.account(m[1]))
}

// listAccounts serves one page of project's accounts, in name order. The
// page token is the index of the next account.
func (f *IAM) listAccounts(w http.ResponseWriter, r *http.Request, project string) {
	var names []string
	for name := range f.accounts {
		if strings.HasPrefix(name, project+"/serviceAccounts/") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	start, _ := strconv.Atoi(r.URL.Query().Get("pageToken"))
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if f.PageSize > 0 && (size <= 0 || size > f.PageSize) {
		size = f.PageSize
	}
	if size <= 0 {
		size = len(names)
	}
	start = min(start, len(names))
	end := min(start+size, len(names))
	page := []map[string]any{}
	for _, n := range names[start:end] {
		page = append(page, f.account(n))
	}
	out := map[string]any{"accounts": page}
	if end < len(names) {
		out["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, out)
}
