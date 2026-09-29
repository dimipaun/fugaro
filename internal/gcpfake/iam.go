package gcpfake

import (
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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

// IAM is a fake of the IAM v1 call Fugaro makes: serviceAccounts get.
type IAM struct {
	*Server

	mu       sync.Mutex
	accounts map[string]map[string]string // projects/<p>/serviceAccounts/<email> → fields
}

var serviceAccountRE = regexp.MustCompile(`^/v1/(projects/[^/]+/serviceAccounts/[^/:]+)$`)

// NewIAM starts an IAM fake that lives until the test ends.
func NewIAM(t *testing.T) *IAM {
	t.Helper()
	f := &IAM{accounts: map[string]map[string]string{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddServiceAccount makes the account email exist in project with
// displayName.
func (f *IAM) AddServiceAccount(project, email, displayName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts["projects/"+project+"/serviceAccounts/"+email] = map[string]string{
		"name":        "projects/" + project + "/serviceAccounts/" + email,
		"email":       email,
		"projectId":   project,
		"displayName": displayName,
		"uniqueId":    strconv.Itoa(100000 + len(f.accounts)),
	}
}

func (f *IAM) handle(w http.ResponseWriter, r *http.Request, _ []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	writeJSON(w, http.StatusOK, maps.Clone(a))
}
