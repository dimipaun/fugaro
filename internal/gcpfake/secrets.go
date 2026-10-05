package gcpfake

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Secrets is a stateful fake of the Secret Manager v1 calls Fugaro makes:
// secrets get, create, list (with a filter of labels.k=v terms joined by
// " AND "), delete, getIamPolicy, addVersion and versions list. It has no
// access call unless a test allows it (AllowAccess): Fugaro never reads a
// value back, but for the GitHub App pre-check's one read of the App's key,
// so any other client that tries fails the test.
//
// Known differences from real Secret Manager: the list filter supports only
// the one shape Fugaro sends, and pages are never split.
type Secrets struct {
	*Server

	// FailAddVersion, when set, makes addVersion answer 400 with this
	// message, so a test can make a server echo the payload.
	FailAddVersion string
	// OnCreate, when set, is called with the secret ID before a create is
	// handled (without the fake's lock), so a test can make the secret
	// appear between the client's get and its create.
	OnCreate func(id string)

	mu          sync.Mutex
	secrets     map[string]*fakeSecret // by ID
	allowAccess bool
	accessed    []string // the secret IDs whose latest version was read
}

type fakeSecret struct {
	labels   map[string]string
	created  time.Time
	versions [][]byte
	policy   []Binding
}

var (
	secretPathRE   = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)$`)
	secretsPathRE  = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets$`)
	addVersionRE   = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+):addVersion$`)
	versionsPathRE = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions$`)
	accessRE       = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/latest:access$`)
	secretPolicyRE = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+):getIamPolicy$`)
	labelTermRE    = regexp.MustCompile(`^labels\.([a-z0-9_-]+)=([a-z0-9_-]*)$`)
)

// NewSecrets starts a Secret Manager fake that lives until the test ends.
func NewSecrets(t *testing.T) *Secrets {
	t.Helper()
	f := &Secrets{secrets: map[string]*fakeSecret{}}
	f.Server = newServer(t, f.handle)
	return f
}

// Latest returns the newest version's value of secret id, or nil when the
// secret or its versions don't exist.
func (f *Secrets) Latest(id string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.secrets[id]
	if s == nil || len(s.versions) == 0 {
		return nil
	}
	return append([]byte(nil), s.versions[len(s.versions)-1]...)
}

// AllowAccess lets the fake answer the access call (the latest version's
// value); without it a client that reads a value fails the test.
func (f *Secrets) AllowAccess() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowAccess = true
}

// Accessed lists the IDs of the secrets whose value was read, in order.
func (f *Secrets) Accessed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.accessed...)
}

// Seed creates secret id with labels and one version holding value, as
// another tool would. A nil value creates the secret with no version.
func (f *Secrets) Seed(id string, labels map[string]string, value []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeSecret{labels: maps.Clone(labels), created: time.Now()}
	if value != nil {
		s.versions = [][]byte{append([]byte(nil), value...)}
	}
	f.secrets[id] = s
}

// SetPolicy replaces the IAM policy of secret id, which must exist.
func (f *Secrets) SetPolicy(id string, bindings []Binding) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.secrets[id]
	if s == nil {
		f.t.Fatalf("gcpfake: SetPolicy(%q): no such secret", id)
	}
	s.policy = cloneBindings(bindings)
}

func (f *Secrets) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method == http.MethodPost && secretsPathRE.MatchString(r.URL.Path) && f.OnCreate != nil {
		f.OnCreate(r.URL.Query().Get("secretId"))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && accessRE.MatchString(p):
		m := accessRE.FindStringSubmatch(p)
		if !f.allowAccess {
			f.t.Errorf("gcpfake: a client read the value of secret %s", m[2])
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "the fake allows no access")
			return
		}
		s := f.secrets[m[2]]
		if s == nil || len(s.versions) == 0 {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Secret ["+p+"] not found or has no versions.")
			return
		}
		f.accessed = append(f.accessed, m[2])
		writeJSON(w, http.StatusOK, map[string]any{"name": "projects/" + m[1] + "/secrets/" + m[2] + "/versions/" + strconv.Itoa(len(s.versions)),
			"payload": map[string]any{"data": base64.StdEncoding.EncodeToString(s.versions[len(s.versions)-1])}})
	case r.Method == http.MethodGet && secretPathRE.MatchString(p):
		m := secretPathRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Secret ["+p+"] not found or has no versions.")
			return
		}
		writeJSON(w, http.StatusOK, s.json(m[1], m[2]))
	case r.Method == http.MethodGet && secretPolicyRE.MatchString(p):
		m := secretPolicyRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Secret ["+m[2]+"] not found.")
			return
		}
		servePolicy(w, r.URL.Query(), s.policy)
	case r.Method == http.MethodDelete && secretPathRE.MatchString(p):
		m := secretPathRE.FindStringSubmatch(p)
		if f.secrets[m[2]] == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		delete(f.secrets, m[2])
		writeJSON(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodPost && secretsPathRE.MatchString(p):
		m := secretsPathRE.FindStringSubmatch(p)
		id := r.URL.Query().Get("secretId")
		var req struct {
			Labels      map[string]string `json:"labels"`
			Replication struct {
				Automatic *struct{} `json:"automatic"`
			} `json:"replication"`
		}
		if err := json.Unmarshal(body, &req); err != nil || id == "" || req.Replication.Automatic == nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "want a secretId and automatic replication")
			return
		}
		if f.secrets[id] != nil {
			writeError(w, http.StatusConflict, "ALREADY_EXISTS", "Secret ["+id+"] already exists.")
			return
		}
		s := &fakeSecret{labels: req.Labels, created: time.Now()}
		f.secrets[id] = s
		writeJSON(w, http.StatusOK, s.json(m[1], id))
	case r.Method == http.MethodGet && secretsPathRE.MatchString(p):
		m := secretsPathRE.FindStringSubmatch(p)
		want, ok := parseLabelFilter(r.URL.Query().Get("filter"))
		if !ok {
			f.unhandled(w, r)
			return
		}
		ids := make([]string, 0, len(f.secrets))
		for id, s := range f.secrets {
			if matchLabels(s.labels, want) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		list := make([]any, 0, len(ids))
		for _, id := range ids {
			list = append(list, f.secrets[id].json(m[1], id))
		}
		writeJSON(w, http.StatusOK, map[string]any{"secrets": list, "totalSize": len(list)})
	case r.Method == http.MethodPost && addVersionRE.MatchString(p):
		m := addVersionRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		if f.FailAddVersion != "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", f.FailAddVersion)
			return
		}
		var req struct {
			Payload struct {
				Data string `json:"data"`
			} `json:"payload"`
		}
		err := json.Unmarshal(body, &req)
		var data []byte
		if err == nil {
			data, err = base64.StdEncoding.DecodeString(req.Payload.Data)
		}
		if err != nil || len(data) == 0 || len(data) > 64*1024 {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "bad payload")
			return
		}
		s.versions = append(s.versions, data)
		n := len(s.versions)
		writeJSON(w, http.StatusOK, versionJSON(m[1], m[2], n, s.created))
	case r.Method == http.MethodGet && versionsPathRE.MatchString(p):
		m := versionsPathRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		var list []any
		for n := len(s.versions); n >= 1; n-- { // newest first, as the real API lists them
			list = append(list, versionJSON(m[1], m[2], n, s.created))
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": list, "totalSize": len(list)})
	default:
		f.unhandled(w, r)
	}
}

func (s *fakeSecret) json(project, id string) map[string]any {
	return map[string]any{
		"name":        "projects/" + project + "/secrets/" + id,
		"createTime":  s.created.UTC().Format(time.RFC3339Nano),
		"labels":      s.labels,
		"replication": map[string]any{"automatic": map[string]any{}},
	}
}

func versionJSON(project, id string, n int, created time.Time) map[string]any {
	return map[string]any{
		"name":       "projects/" + project + "/secrets/" + id + "/versions/" + strconv.Itoa(n),
		"createTime": created.UTC().Format(time.RFC3339Nano),
		"state":      "ENABLED",
	}
}

// parseLabelFilter parses "labels.a=x AND labels.b=y"; ok is false for any
// other shape.
func parseLabelFilter(filter string) (map[string]string, bool) {
	want := map[string]string{}
	if filter == "" {
		return want, true
	}
	for _, term := range strings.Split(filter, " AND ") {
		m := labelTermRE.FindStringSubmatch(term)
		if m == nil {
			return nil, false
		}
		want[m[1]] = m[2]
	}
	return want, true
}

func matchLabels(have, want map[string]string) bool {
	for k, v := range want {
		if got, ok := have[k]; !ok || got != v {
			return false
		}
	}
	return true
}
