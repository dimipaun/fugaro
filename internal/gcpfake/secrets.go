package gcpfake

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Secrets is a stateful fake of the Secret Manager v1 calls Fugaro makes:
// secrets get, create, list (with a filter of labels.k=v terms joined by
// " AND "), delete, getIamPolicy, addVersion and versions list, disable,
// delete and destroy, and the value read at `latest` or at a numbered version.
// It has no access call unless a test allows it (AllowAccess): the CLI reads a
// value back in exactly two places — the GitHub App pre-check's one read of
// the App's key, and C1.25's read of the bundle document it is about to
// rewrite — so any other client that tries fails the test.
//
// Deleted and Destroyed record the two destructive calls, so a test can
// assert that a run never made one: a retirement leaves a secret and its
// values in Secret Manager, and only the labels ever take its container
// away. Disabled records the version names a disable took — the one call that
// retires a version without taking it away, which C1.25's rotation makes on
// the version it replaced — and a DISABLED version is refused by the access
// call, as real Secret Manager refuses it, so the retirement is observable
// and not only recorded.
//
// Known differences from real Secret Manager: the list filter supports only
// the one shape Fugaro sends, and pages are never split.
type Secrets struct {
	*Server

	// FailAddVersion, when set, makes addVersion answer 400 with this
	// message, so a test can make a server echo the payload.
	FailAddVersion string
	// EchoAddVersionPayload, when set, makes addVersion answer 400 with the
	// payload it received, decoded and as it was sent (base64), which is what
	// a server that repeats its request body hands back. The test does not
	// have to know the bytes: this is the payload the client actually sent,
	// which for the bundle is the whole document and every credential in it.
	EchoAddVersionPayload bool
	// OnCreate, when set, is called with the secret ID before a create is
	// handled (without the fake's lock), so a test can make the secret
	// appear between the client's get and its create.
	OnCreate func(id string)
	// OnAddVersion, when set, is called with the secret ID before an addVersion
	// is handled (without the fake's lock), so a test can make a version
	// appear between the client's read and its write — the window a
	// read-modify-write has no conditional write for.
	OnAddVersion func(id string)
	// FailListWith, when non-zero, makes secrets.list answer that HTTP
	// status: what pins the degradation of a refused label read.
	FailListWith int
	// FailListWithFilter, when set, makes the secrets.list carrying exactly
	// this filter answer 403 PERMISSION_DENIED and leaves every other listing
	// alone: what pins the read doctor makes once its other listings have
	// answered (the whole managed set), which FailListWith cannot single out
	// because the same project is listed more than once in a run.
	FailListWithFilter string
	// FailPolicyWith, when non-zero, makes a container's getIamPolicy answer
	// that HTTP status: what pins a reader sweep that must report a policy it
	// could not read rather than count it as one that grants nothing.
	FailPolicyWith int
	// FailDisableWith, when non-zero, makes versions/N:disable answer that
	// HTTP status: what pins the retirement C1.25's rotation makes as
	// best-effort — the write is already verified when it is attempted, so a
	// refused disable is reported and the command still succeeds.
	FailDisableWith int
	// FailVersionsAfter makes every version list after the first n answer
	// 403 PERMISSION_DENIED: the read a step past the label calls makes,
	// which a test cannot name by ID because the same secrets are read
	// earlier in the run. Pair it with VersionsListCalls, so a test can fail
	// the last read of a successful run without hard-coding how many there
	// are.
	FailVersionsAfter int

	mu          sync.Mutex
	secrets     map[string]*fakeSecret // by ID
	allowAccess bool
	accessed    []string // the secret IDs whose latest version was read
	deletes     []string // the secret IDs a delete removed, in order
	destroys    []string // the version names a destroy or a version delete took
	disables    []string // the version names a versions/N:disable took, in order
	versionList int      // the version lists served, successful or failed
}

type fakeSecret struct {
	labels   map[string]string
	created  time.Time
	versions [][]byte
	policy   []Binding
	// disabled says every version of this secret is DISABLED, the state an
	// operator's disable leaves: the container exists, no enabled version
	// does. Set by DisableVersions.
	disabled bool
	// off names the versions a versions/N:disable call turned DISABLED, one
	// at a time: C1.25's rotation retires the version it replaced with one.
	off map[int]bool
}

// state is version n's state, "ENABLED" or "DISABLED". A secret-level
// disable (DisableVersions) covers every version of it.
func (s *fakeSecret) state(n int) string {
	if s.disabled || s.off[n] {
		return "DISABLED"
	}
	return "ENABLED"
}

var (
	secretPathRE   = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)$`)
	secretsPathRE  = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets$`)
	addVersionRE   = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+):addVersion$`)
	versionsPathRE = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions$`)
	// accessRE is the value read, at `latest` or at a numbered version: C1.25's
	// `creds ls` reads the version its metadata came from, so that the names it
	// prints and the time beside them are one version's.
	accessRE       = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/(latest|[0-9]+):access$`)
	secretPolicyRE = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+):getIamPolicy$`)
	versionRE      = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/([0-9]+)$`)
	destroyRE      = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/([0-9]+):destroy$`)
	disableRE      = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/([0-9]+):disable$`)
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

// VersionsListCalls is how many version lists the fake has served, successful
// or failed: a test reads it from one run to arm FailVersionsAfter for the
// last read of the next.
func (f *Secrets) VersionsListCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.versionList
}

// DisableVersions marks every version of secret id DISABLED, as
// secretmanager.versions.disable does. It is the state a readiness gate
// must still read as "no version": the container is there and resolves to
// nothing.
func (f *Secrets) DisableVersions(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.secrets[id]; s != nil {
		s.disabled = true
	}
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

// Deleted lists the IDs of the secret containers a delete removed, in order:
// Fugaro never deletes one, so a retirement's test asserts this is empty.
func (f *Secrets) Deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

// Destroyed lists the versions a destroy or a version delete took, in order
// (their full names). It is empty for every Fugaro run.
func (f *Secrets) Destroyed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.destroys...)
}

// Disabled lists the version names a versions/N:disable call turned DISABLED,
// in order (their full names). It is what a test asserts a rotation's
// retirement with: C1.25's `creds set` disables the one version it replaced
// and no other.
func (f *Secrets) Disabled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.disables...)
}

// AddVersion appends a version to secret id, as another writer of the same
// secret would: it changes the fake's state without a client call, so a test
// can land a concurrent write inside a window of the code under test — and
// Requests stays exactly what that code asked for.
func (f *Secrets) AddVersion(id string, value []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.secrets[id]; s != nil {
		s.versions = append(s.versions, append([]byte(nil), value...))
	}
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
	if r.Method == http.MethodPost && addVersionRE.MatchString(r.URL.Path) && f.OnAddVersion != nil {
		f.OnAddVersion(addVersionRE.FindStringSubmatch(r.URL.Path)[2])
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
		// `latest` is the newest version, whatever its state; a number is that
		// version or nothing.
		n := len(s.versions)
		if m[3] != "latest" {
			if n = atoi(m[3]); n < 1 || n > len(s.versions) {
				writeError(w, http.StatusNotFound, "NOT_FOUND", "Secret ["+p+"] not found.")
				return
			}
		}
		if state := s.state(n); state == "DISABLED" {
			// As real Secret Manager: a DISABLED version is there and its
			// value is not readable. It is not a 404 — the version exists —
			// so a caller that reads a state as an absence is caught here.
			writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "Secret Version ["+p+"] is disabled.")
			return
		}
		f.accessed = append(f.accessed, m[2])
		writeJSON(w, http.StatusOK, map[string]any{"name": "projects/" + m[1] + "/secrets/" + m[2] + "/versions/" + strconv.Itoa(n),
			"payload": map[string]any{"data": base64.StdEncoding.EncodeToString(s.versions[n-1])}})
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
		if f.FailPolicyWith != 0 {
			writeError(w, f.FailPolicyWith, http.StatusText(f.FailPolicyWith), "injected failure")
			return
		}
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
		f.deletes = append(f.deletes, m[2])
		delete(f.secrets, m[2])
		writeJSON(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodPost && destroyRE.MatchString(p):
		m := destroyRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil || version(s, m[3]) == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		f.destroys = append(f.destroys, "projects/"+m[1]+"/secrets/"+m[2]+"/versions/"+m[3])
		writeJSON(w, http.StatusOK, versionJSON(m[1], m[2], atoi(m[3]), s.created, s.state(atoi(m[3]))))
	case r.Method == http.MethodPost && disableRE.MatchString(p):
		m := disableRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil || version(s, m[3]) == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		if f.FailDisableWith != 0 {
			writeError(w, f.FailDisableWith, http.StatusText(f.FailDisableWith), "injected failure")
			return
		}
		f.disables = append(f.disables, "projects/"+m[1]+"/secrets/"+m[2]+"/versions/"+m[3])
		if s.off == nil {
			s.off = map[int]bool{}
		}
		s.off[atoi(m[3])] = true
		writeJSON(w, http.StatusOK, versionJSON(m[1], m[2], atoi(m[3]), s.created, s.state(atoi(m[3]))))
	case r.Method == http.MethodDelete && versionRE.MatchString(p):
		m := versionRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil || version(s, m[3]) == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		f.destroys = append(f.destroys, "projects/"+m[1]+"/secrets/"+m[2]+"/versions/"+m[3])
		s.versions = slices.Delete(s.versions, atoi(m[3])-1, atoi(m[3]))
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
		if f.FailListWith != 0 {
			writeError(w, f.FailListWith, http.StatusText(f.FailListWith), "injected failure")
			return
		}
		if filter := r.URL.Query().Get("filter"); f.FailListWithFilter != "" && filter == f.FailListWithFilter {
			// 403, not 5xx: the client retries a 500 on its own, so the
			// caller would never see it.
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "injected failure")
			return
		}
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
		if f.FailAddVersion != "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", f.FailAddVersion)
			return
		}
		if f.EchoAddVersionPayload {
			// Both spellings a repeating server produces: the payload decoded,
			// and the payload as the request carried it. The body is JSON, so
			// the decoded form is the one an unhelpful message would carry and
			// the base64 is the one a raw body echo carries.
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"the payload was "+string(data)+" (as sent: "+req.Payload.Data+")")
			return
		}
		s.versions = append(s.versions, data)
		n := len(s.versions)
		writeJSON(w, http.StatusOK, versionJSON(m[1], m[2], n, s.created, s.state(n)))
	case r.Method == http.MethodGet && versionsPathRE.MatchString(p):
		m := versionsPathRE.FindStringSubmatch(p)
		// handle holds f.mu for the whole switch, so the counter is touched
		// directly here.
		f.versionList++
		if f.FailVersionsAfter > 0 && f.versionList > f.FailVersionsAfter {
			// 403, not 5xx: the client retries a 500 on its own, so the
			// caller would never see it.
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "injected failure")
			return
		}
		s := f.secrets[m[2]]
		if s == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		var list []any
		for n := len(s.versions); n >= 1; n-- { // newest first, as the real API lists them
			list = append(list, versionJSON(m[1], m[2], n, s.created, s.state(n)))
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

// version is version n of s, or nil when the secret has no such version.
func version(s *fakeSecret, n string) []byte {
	i := atoi(n)
	if i < 1 || i > len(s.versions) {
		return nil
	}
	return s.versions[i-1]
}

// atoi is a version number the regexps already restricted to digits.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// versionJSON is version n's metadata: its name, when it was created and its
// state (state is fakeSecret.state, "ENABLED" or "DISABLED").
func versionJSON(project, id string, n int, created time.Time, state string) map[string]any {
	return map[string]any{
		"name":       "projects/" + project + "/secrets/" + id + "/versions/" + strconv.Itoa(n),
		"createTime": created.UTC().Format(time.RFC3339Nano),
		"state":      state,
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
