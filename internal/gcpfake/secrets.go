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
// enable, delete and destroy, and the value read at `latest` or at a numbered
// version.
// It has no access call unless a test allows it (AllowAccess): the CLI reads a
// value back only where the work cannot be done without it — the GitHub App
// pre-check and a catalog build's copy of a repository-sourced entry, which
// read the App's key or the project's bitbucket-token, and the credentials
// service's bundle document, which `fugaro creds` and `fugaro doctor
// --check-bitbucket-token` read — so any other client that tries fails the
// test.
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
// A create carries a replication policy, which the fake checks as the API
// checks it — automatic, or user-managed with a location for every replica —
// stores with the container, and answers back on every read of it, so a test
// can assert where the container a call created lives (Replication).
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
	// FailDestroyWith, when non-zero, makes versions/N:destroy answer that HTTP
	// status and destroy nothing: the refusal the revocation step 2b has to
	// report rather than pass over (mobile plan task P5.1e), since a version
	// the caller believes is gone and is not keeps verifying the messages of a
	// pairing nobody holds any more.
	FailDestroyWith int
	// FailVersionsAfter makes every version list after the first n answer
	// 403 PERMISSION_DENIED: the read a step past the label calls makes,
	// which a test cannot name by ID because the same secrets are read
	// earlier in the run. Pair it with VersionsListCalls, so a test can fail
	// the last read of a successful run without hard-coding how many there
	// are.
	FailVersionsAfter int
	// FailDeleteWith, when non-zero, makes secrets.delete answer that HTTP
	// status and delete nothing: the container stays, with every version in
	// it. It is the refusal a member who may add a version but not remove the
	// container gets, and the one a caller that wrote something before the
	// deletion has to undo.
	FailDeleteWith int
	// OnDelete, when set, is called with the secret ID before a delete is
	// handled (without the fake's lock). A delete is the one call a caller
	// makes after it has already written elsewhere, so this is how a test
	// moves that other document between the caller's write and the undo it
	// owes when the delete fails.
	OnDelete func(id string)

	mu          sync.Mutex
	secrets     map[string]*fakeSecret // by ID
	allowAccess bool
	accessed    []string // the secret IDs whose latest version was read
	deletes     []string // the secret IDs a delete removed, in order
	destroys    []string // the version names a destroy or a version delete took
	disables    []string // the version names a versions/N:disable took, in order
	enables     []string // the version names a versions/N:enable took, in order
	versionList int      // the version lists served, successful or failed
	// versionLists is the same count per secret ID: VersionsListCalls answers
	// for the whole run, which cannot say whether ONE container was listed —
	// a run lists the versions of many — and a test asking whether a secret
	// was touched at all asks the other question.
	versionLists map[string]int
	// held is what a secrets.testIamPermissions call answers, by secret ID: the
	// subset of the asked permissions the caller holds. It is nil for a test that
	// never set it, and nil holds EVERY permission asked — the fake cannot model
	// a project's IAM, and the default a test should have to opt OUT of is the
	// one that changes no other test's behaviour. SetHeldPermissions is how a test
	// says a caller is not an owner (SetHeldPermissions(id) with no permission).
	held map[string][]string
}

type fakeSecret struct {
	labels   map[string]string
	created  time.Time
	versions [][]byte
	policy   []Binding
	// regions is the container's user-managed replication as the create that
	// made it named it; empty is automatic, the zero value, which is what a
	// container the fake did not create itself (Seed) carries: the policy
	// every Fugaro container had before LP5a wrote one in the region.
	regions []string
	// disabled says every version of this secret is DISABLED, the state an
	// operator's disable leaves: the container exists, no enabled version
	// does. Set by DisableVersions.
	disabled bool
	// off names the versions a versions/N:disable call turned DISABLED, one
	// at a time: C1.25's rotation retires the version it replaced with one.
	off map[int]bool
	// gone names the versions a versions/N:destroy call took. The version stays
	// in the list — real Secret Manager keeps it and reports it DESTROYED — and
	// its value is no longer readable, which is the whole point of a destroy
	// over a disable (mobile plan task P5.1e).
	gone map[int]bool
}

// state is version n's state: "ENABLED", "DISABLED" or "DESTROYED". A
// secret-level disable (DisableVersions) covers every version of it.
func (s *fakeSecret) state(n int) string {
	if s.gone[n] {
		return "DESTROYED"
	}
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
	// secretPermissionsRE is the pre-flight probe every writer of a provider key
	// makes before it reads a value (the project-owner probe, KS2 of
	// docs/keys-through-service): it answers which of the asked permissions the
	// caller holds on ONE container, and never a policy.
	secretPermissionsRE = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+):testIamPermissions$`)
	versionRE           = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/([0-9]+)$`)
	destroyRE           = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/([0-9]+):destroy$`)
	disableRE           = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/([0-9]+):disable$`)
	enableRE            = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/:]+)/versions/([0-9]+):enable$`)
	labelTermRE         = regexp.MustCompile(`^labels\.([a-z0-9_-]+)=([a-z0-9_-]*)$`)
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

// EnabledVersions returns every ENABLED version's value of secret id, oldest
// first: what a client that reads the whole container sees, which for the
// pairing keys is the set a cloud consumer verifies with (keychain.Cloud's
// Keys). A DESTROYED version is not in the answer and neither is one a
// DisableVersions call took, so a test asserts what the consumer would verify
// with rather than which calls were made.
func (f *Secrets) EnabledVersions(id string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.secrets[id]
	if s == nil {
		return nil
	}
	var out [][]byte
	for i := range s.versions {
		if s.state(i+1) != "ENABLED" {
			continue
		}
		out = append(out, append([]byte(nil), s.versions[i]...))
	}
	return out
}

// VersionsListCalls is how many version lists the fake has served, successful
// or failed: a test reads it from one run to arm FailVersionsAfter for the
// last read of the next.
func (f *Secrets) VersionsListCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.versionList
}

// VersionsListedFor is VersionsListCalls for ONE secret, successful or failed.
// It is the per-container answer, and the two are not interchangeable: a run
// lists the versions of several secrets, so the run-wide count cannot say that
// one of them was left alone. The list is the first call a store over a
// container makes once it has the container itself, which is what a test
// asserting that a secret was never opened reads.
func (f *Secrets) VersionsListedFor(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.versionLists[id]
}

// DisableVersions marks every version of secret id DISABLED, as
// secretmanager.versions.disable does. It is the state a readiness gate
// must still read as "no version": the container is there and resolves to
// nothing. The mark is the secret's, because this helper stands for an
// operator's disable of the whole container; an enable names one version, so
// the first one spreads the mark onto the versions that exist and lifts it from
// the version the call names, exactly as disabling them one at a time would.
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
// (their full names). A destroy leaves the version in the list and DESTROYED,
// as real Secret Manager does, and its value is no longer readable — which is
// what a pairing's revoke is for (mobile plan task P5.1e).
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

// Enabled lists the version names a versions/N:enable call turned ENABLED, in
// order (their full names): what a revoke makes of a version it has to read to
// attribute it, and what it puts back with a disable when the version turns out
// not to be the revoked pairing's.
func (f *Secrets) Enabled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.enables...)
}

// Replication answers the replica locations of secret id's user-managed
// replication, or nil when the container is automatic or does not exist. It
// reads what the create stored rather than the create request: a run creates
// several containers, and the policy of ONE of them is the question.
func (f *Secrets) Replication(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.secrets[id]
	if s == nil {
		return nil
	}
	return append([]string(nil), s.regions...)
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

// SetHeldPermissions is what a secrets.testIamPermissions call answers for secret
// id: the permissions the caller holds there. A call with none held is the
// caller that is no project owner, which is the state the provider-key writers
// refuse. A test that never calls this gets an unset held, which holds
// everything asked (Secrets.held).
func (f *Secrets) SetHeldPermissions(id string, permissions ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secrets[id] == nil {
		f.t.Fatalf("gcpfake: SetHeldPermissions(%q): no such secret", id)
	}
	if f.held == nil {
		f.held = map[string][]string{}
	}
	f.held[id] = append([]string(nil), permissions...)
}

func (f *Secrets) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method == http.MethodPost && secretsPathRE.MatchString(r.URL.Path) && f.OnCreate != nil {
		f.OnCreate(r.URL.Query().Get("secretId"))
	}
	if r.Method == http.MethodPost && addVersionRE.MatchString(r.URL.Path) && f.OnAddVersion != nil {
		f.OnAddVersion(addVersionRE.FindStringSubmatch(r.URL.Path)[2])
	}
	if r.Method == http.MethodDelete && secretPathRE.MatchString(r.URL.Path) && f.OnDelete != nil {
		f.OnDelete(secretPathRE.FindStringSubmatch(r.URL.Path)[2])
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
		if state := s.state(n); state != "ENABLED" {
			// As real Secret Manager: a version that is DISABLED or DESTROYED
			// is there and its value is not readable. It is not a 404 — the
			// version exists — so a caller that reads a state as an absence is
			// caught here.
			writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "Secret Version ["+p+"] is "+strings.ToLower(state)+".")
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
	case (r.Method == http.MethodGet || r.Method == http.MethodPost) && secretPermissionsRE.MatchString(p):
		m := secretPermissionsRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Secret ["+m[2]+"] not found.")
			return
		}
		// The generated client sends the permissions in the POST body
		// (projects.secrets.testIamPermissions takes its request there), while a
		// hand-made GET carries them in the query; both are served, so a test
		// can drive the probe either way.
		asked := r.URL.Query()["permissions"]
		if r.Method == http.MethodPost && len(body) != 0 {
			var req struct {
				Permissions []string `json:"permissions"`
			}
			if err := json.Unmarshal(body, &req); err == nil && len(req.Permissions) != 0 {
				asked = req.Permissions
			}
		}

		// An unset held (nil) holds everything asked: the fake models no IAM, and
		// the answer every existing test's write already relies on is the one that
		// keeps this probe from refusing writes those tests are not about.
		if f.held == nil {
			writeJSON(w, http.StatusOK, map[string]any{"permissions": asked})
			return
		}
		held := make([]string, 0, len(asked))
		for _, want := range asked {
			if slices.Contains(f.held[m[2]], want) {
				held = append(held, want)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"permissions": held})
	case r.Method == http.MethodDelete && secretPathRE.MatchString(p):
		m := secretPathRE.FindStringSubmatch(p)
		if f.secrets[m[2]] == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		if f.FailDeleteWith != 0 {
			writeError(w, f.FailDeleteWith, http.StatusText(f.FailDeleteWith), "injected failure")
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
		if f.FailDestroyWith != 0 {
			writeError(w, f.FailDestroyWith, http.StatusText(f.FailDestroyWith), "injected failure")
			return
		}
		f.destroys = append(f.destroys, "projects/"+m[1]+"/secrets/"+m[2]+"/versions/"+m[3])
		if s.gone == nil {
			s.gone = map[int]bool{}
		}
		s.gone[atoi(m[3])] = true
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
	case r.Method == http.MethodPost && enableRE.MatchString(p):
		m := enableRE.FindStringSubmatch(p)
		s := f.secrets[m[2]]
		if s == nil || version(s, m[3]) == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		n := atoi(m[3])
		if s.state(n) == "DESTROYED" {
			// A destroyed version cannot be brought back, as real Secret
			// Manager refuses it: the destroy is the one call here with no way
			// back, which is the whole of why step 2b makes it and not a
			// disable.
			writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "Secret Version ["+p+"] is destroyed.")
			return
		}
		if s.disabled {
			if s.off == nil {
				s.off = map[int]bool{}
			}
			for i := 1; i <= len(s.versions); i++ {
				s.off[i] = true
			}
			s.disabled = false
		}
		delete(s.off, n)
		f.enables = append(f.enables, "projects/"+m[1]+"/secrets/"+m[2]+"/versions/"+m[3])
		writeJSON(w, http.StatusOK, versionJSON(m[1], m[2], n, s.created, s.state(n)))
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
			Replication createReplication `json:"replication"`
		}
		if err := json.Unmarshal(body, &req); err != nil || id == "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "want a secretId and a replication policy")
			return
		}
		regions, ok := req.Replication.regions()
		if !ok {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "want automatic, or user-managed with a location for every replica")
			return
		}
		if f.secrets[id] != nil {
			writeError(w, http.StatusConflict, "ALREADY_EXISTS", "Secret ["+id+"] already exists.")
			return
		}
		s := &fakeSecret{labels: req.Labels, created: time.Now(), regions: regions}
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
		// handle holds f.mu for the whole switch, so the counters are touched
		// directly here.
		f.versionList++
		if f.versionLists == nil {
			f.versionLists = map[string]int{}
		}
		f.versionLists[m[2]]++
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
		"replication": replicationJSON(s.regions),
	}
}

// replicationJSON is a replication policy as the API answers it: automatic,
// or user-managed with the replica locations it holds (empty locations are
// automatic, which is the zero value every seeded container has).
func replicationJSON(regions []string) map[string]any {
	if len(regions) == 0 {
		return map[string]any{"automatic": map[string]any{}}
	}
	replicas := make([]any, 0, len(regions))
	for _, r := range regions {
		replicas = append(replicas, map[string]any{"location": r})
	}
	return map[string]any{"userManaged": map[string]any{"replicas": replicas}}
}

// createReplication is the replication policy a create request carries. The
// API takes exactly one of automatic and userManaged, and a user-managed
// policy names a location for every replica: a request that names the policy
// any other way is refused, as real Secret Manager refuses it.
type createReplication struct {
	Automatic   *struct{} `json:"automatic"`
	UserManaged *struct {
		Replicas []struct {
			Location string `json:"location"`
		} `json:"replicas"`
	} `json:"userManaged"`
}

// regions is the policy's replica locations, nil for automatic; ok is false
// for a policy the API refuses: both, neither, a user-managed one with no
// replica, or a replica with no location.
func (r createReplication) regions() (regions []string, ok bool) {
	switch {
	case r.Automatic != nil && r.UserManaged != nil, r.Automatic == nil && r.UserManaged == nil:
		return nil, false
	case r.Automatic != nil:
		return nil, true
	}
	if len(r.UserManaged.Replicas) == 0 {
		return nil, false
	}
	for _, rep := range r.UserManaged.Replicas {
		if rep.Location == "" {
			return nil, false
		}
		regions = append(regions, rep.Location)
	}
	return regions, true
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
