package intake

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// The fixtures: two members (one of them a project admin), the addresses the
// launcher class admits, and the credential a test sets. Nothing here is a
// credential: the value is a word, and the addresses are example.com.
const (
	testAudience   = "https://intake-aurora-dev-uc.a.run.app"
	testOwnerEmail = "member@example.com"
	// testOwnerSub is a Google account id: 21 digits, the width
	// maxLauncherSubDigits bounds (credsvc).
	testOwnerSub   = "102938475610293847561"
	testAdminEmail = "admin@example.com"
	testAdminSub   = "112233445566778899001"
	testOtherEmail = "other@example.com"
	testOtherSub   = "123456789012345678901"
	testValue      = "credential-value-one"
	testKid        = "intake-kid"
)

// testKey is the key every token here is signed with, generated once: a
// 2048-bit keygen is the slowest thing in this package's tests.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("intake test: generating a key: " + err.Error())
	}
	return k
})

// staticKeys is the KeySource the launcher class is built over: a fixed set,
// with the same "not here" answer the real one gives.
type staticKeys struct{ key *rsa.PublicKey }

func (s staticKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if kid != testKid {
		return nil, credsvc.ErrUnknownKeyID
	}
	return s.key, nil
}

func testKeys() credsvc.KeySource { return staticKeys{key: &testKey().PublicKey} }

// idToken returns a Google ID token for the address and account id, signed by
// the test's key: the token the launcher class verifies.
func idToken(t *testing.T, address, sub string) string {
	t.Helper()
	now := time.Now()
	return signToken(t, map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            testAudience,
		"email":          address,
		"email_verified": true,
		"sub":            sub,
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
	})
}

func signToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	head, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testKid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshalling the header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, testKey(), crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// fakeStore is the Secret Manager the tests' intake writes to: containers in a
// map, the values it was handed kept, and the calls it answered in order. It
// is the whole of what the intake can reach, which is what makes
// TestNoReadMethodExists meaningful: there is no value on it to read back.
type fakeStore struct {
	containers map[string]Secret
	values     map[string][]string
	labels     map[string]map[string]string
	created    []string
	deleted    []string
	calls      []string
	// foreign are names that are there for Create — it answers ErrExists — and
	// that Describe and List never answer with. That is what another writer's
	// container looks like to this intake, and it is not a shape a test invents:
	// the real Describe is the list call filtered by the labels the intake
	// writes, so a container the managed deployer pre-created (F35) carries none
	// and is invisible here. It is what ensure's second read exists for.
	foreign map[string]bool
	// late are names that are there for Create and that the Describe BEFORE it
	// misses while the Describe after it finds: the list call Describe is has
	// caught up in between, which is that call's own eventual consistency —
	// the state a container the intake wrote moments ago is in. ensure's two
	// reads are not atomic, and this is the window they have.
	late map[string]Secret
}

func newFakeStore() *fakeStore {
	return &fakeStore{containers: map[string]Secret{}, values: map[string][]string{}, labels: map[string]map[string]string{}, foreign: map[string]bool{}, late: map[string]Secret{}}
}

func (f *fakeStore) Describe(_ context.Context, id string) (Secret, bool, error) {
	f.calls = append(f.calls, "describe "+id)
	s, ok := f.containers[id]
	return s, ok, nil
}

func (f *fakeStore) Create(_ context.Context, id string, annotations, labels map[string]string) error {
	f.calls = append(f.calls, "create "+id)
	if _, exists := f.containers[id]; exists || f.foreign[id] {
		return ErrExists
	}
	if s, late := f.late[id]; late {
		delete(f.late, id)
		f.containers[id] = s
		return ErrExists
	}
	f.containers[id] = Secret{ID: id, Annotations: annotations}
	f.labels[id] = labels
	f.created = append(f.created, id)
	return nil
}

func (f *fakeStore) AddVersion(_ context.Context, id string, value []byte) (string, error) {
	f.calls = append(f.calls, "version "+id)
	f.values[id] = append(f.values[id], string(value))
	return "1", nil
}

// List answers the containers of one scope, and it answers them by the name
// grammar rather than by the label the production store filters on: this fake
// is the contract ("the containers the intake wrote under this scope") and not
// the mechanism, and a test seeds containers by name. What the real store's
// filter has to be is pinned against internal/gcpfake, which speaks Secret
// Manager's own filter language.
func (f *fakeStore) List(_ context.Context, scope Scope) ([]Secret, error) {
	f.calls = append(f.calls, "list "+string(scope))
	var out []Secret
	for id, s := range f.containers {
		n, err := secretname.Parse(id)
		if err != nil || n.Shared() != (scope == ScopeShared) {
			continue
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Secret) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *fakeStore) Delete(_ context.Context, id string) error {
	f.calls = append(f.calls, "delete "+id)
	delete(f.containers, id)
	f.deleted = append(f.deleted, id)
	return nil
}

// fakeAdmins is DS2.0's derived list, as the intake reads it.
type fakeAdmins struct{ list []access.Admin }

func (f fakeAdmins) Admins(context.Context) ([]access.Admin, error) { return f.list, nil }

// fixture is one intake under test: a real launcher class over the test's key
// set, a store and a list the test can look at, and the audit lines as text.
type fixture struct {
	srv    *Server
	store  *fakeStore
	admins []access.Admin
	lines  *bytes.Buffer
}

// newFixture builds the intake, admitting the addresses as launchers and
// listing the admins on the derived list, with the auditor a test reads back as
// JSON text.
func newFixture(t *testing.T, launchers []string, admins []access.Admin) *fixture {
	t.Helper()
	lines := &bytes.Buffer{}
	return newFixtureLogging(t, launchers, admins, slog.NewJSONHandler(lines, &slog.HandlerOptions{Level: slog.LevelInfo}), lines)
}

// newFixtureLogging is newFixture over a named log handler, for the one thing
// a test cannot see through a bytes.Buffer: what the intake does when the log
// itself cannot be written (TestACallThatCannotBeAuditedIsNotReportedAsDone).
func newFixtureLogging(t *testing.T, launchers []string, admins []access.Admin, h slog.Handler, lines *bytes.Buffer) *fixture {
	t.Helper()
	callers, err := credsvc.NewLaunchers(credsvc.LauncherConfig{
		Audience: testAudience,
		Accounts: launchers,
		Keys:     testKeys(),
	})
	if err != nil {
		t.Fatalf("building the launcher class: %v", err)
	}
	audit, err := NewAuditor(h)
	if err != nil {
		t.Fatalf("building the auditor: %v", err)
	}
	store := newFakeStore()
	srv, err := New(Config{Callers: callers, Admins: fakeAdmins{list: admins}, Secrets: store, Audit: audit})
	if err != nil {
		t.Fatalf("building the intake: %v", err)
	}
	return &fixture{srv: srv, store: store, admins: admins, lines: lines}
}

// brokenHandler is a log that records every level and fails every write: the
// state the intake's fail-closed rule is about, and one no bytes.Buffer can be.
type brokenHandler struct{}

func (brokenHandler) Enabled(context.Context, slog.Level) bool { return true }

func (brokenHandler) Handle(context.Context, slog.Record) error {
	return errors.New("the log is not writable")
}

func (brokenHandler) WithAttrs([]slog.Attr) slog.Handler { return brokenHandler{} }

func (brokenHandler) WithGroup(string) slog.Handler { return brokenHandler{} }

// member is the fixture every test starts from: one ordinary member and one
// project admin, with the admin on the derived list.
func member(t *testing.T) *fixture {
	t.Helper()
	return newFixture(t, []string{testOwnerEmail, testAdminEmail}, []access.Admin{{Email: testAdminEmail, Sub: testAdminSub}})
}

func (f *fixture) token(t *testing.T, address, sub string) string { return idToken(t, address, sub) }

func (f *fixture) post(t *testing.T, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	return w
}

// b64 is a value as the wire carries it.
func b64(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }

// create is the body of a create or an add-version call.
func createBody(scope Scope, name, value string) string {
	return `{"scope":"` + string(scope) + `","name":"` + name + `","value":"` + b64(value) + `"}`
}

// errorType reads a refusal's type, and fails the test on an answer that is
// not one.
func errorType(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e credsvc.WireError
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("the answer is not a refusal: %v (%s)", err, w.Body.String())
	}
	return e.Error
}

// errorMessage reads a refusal's own sentence, and fails the test on an answer
// that is not one.
func errorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e credsvc.WireError
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("the answer is not a refusal: %v (%s)", err, w.Body.String())
	}
	return e.Message
}

// audit is the call's one line, as the operator reads it, and fails the test
// unless the call wrote exactly one.
func audit(t *testing.T, f *fixture) map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(f.lines.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("the audit line is not JSON: %v (%s)", err, line)
		}
		out = append(out, rec)
	}
	if len(out) != 1 {
		t.Fatalf("the call wrote %d audit lines, want exactly one: %s", len(out), f.lines.String())
	}
	return out[0]
}

// TestPrefixFromSubNotFromBody is the red step: the prefix of a per-user
// secret is the verified `sub` claim and never anything the request says about
// itself (R3).
func TestPrefixFromSubNotFromBody(t *testing.T) {
	f := member(t)
	tok := f.token(t, testOwnerEmail, testOwnerSub)

	// A body that names an owner is refused rather than read: the request
	// shape has no field for one, and strict decoding is what makes that a
	// refusal instead of a field a release silently ignores.
	for _, body := range []string{
		`{"scope":"user","name":"api-key","value":"` + b64(testValue) + `","sub":"999"}`,
		`{"scope":"user","name":"api-key","value":"` + b64(testValue) + `","id":"fugaro_u_999_api-key"}`,
		`{"scope":"user","name":"api-key","value":"` + b64(testValue) + `","owner":"` + testOtherEmail + `"}`,
	} {
		w := f.post(t, CreatePath, tok, body)
		if w.Code != http.StatusBadRequest || errorType(t, w) != ErrorRequestRefused {
			t.Fatalf("a body naming the owner: got %d %s, want 400 %s", w.Code, errorType(t, w), ErrorRequestRefused)
		}
	}
	if len(f.store.created) != 0 {
		t.Fatalf("a refused create made the containers %v", f.store.created)
	}

	// And the container it does write is under the token's own account id,
	// whatever the body would have preferred.
	w := f.post(t, CreatePath, tok, createBody(ScopeUser, "api-key", testValue))
	if w.Code != http.StatusOK {
		t.Fatalf("create: got %d %s", w.Code, w.Body.String())
	}
	want := secretname.UserPrefix + testOwnerSub + "_api-key"
	if !slices.Equal(f.store.created, []string{want}) {
		t.Fatalf("the created containers are %v, want %v", f.store.created, []string{want})
	}
	// The annotations name the owner the name names, and the verified address.
	got := f.store.containers[want].Annotations
	for k, v := range map[string]string{
		AnnotationOwnerSub:   testOwnerSub,
		AnnotationOwnerEmail: testOwnerEmail,
		AnnotationCreatedBy:  testOwnerEmail,
	} {
		if got[k] != v {
			t.Fatalf("the container's %s is %q, want %q", k, got[k], v)
		}
	}
}

// TestRefusesNameOutsideGrammar: a name R1's grammar does not admit is refused
// before any write, reserved platform names included (R7, G11).
func TestRefusesNameOutsideGrammar(t *testing.T) {
	f := member(t)
	tok := f.token(t, testOwnerEmail, testOwnerSub)
	for _, name := range []string{
		"",
		"API-KEY",
		"api_key",
		"api key",
		"-api-key",
		"github-app-key",  // the App key's own bundle name (R7)
		"bitbucket-token", // the git token's
		strings.Repeat("a", secretname.MaxIDLen),
	} {
		w := f.post(t, CreatePath, tok, createBody(ScopeUser, name, testValue))
		if w.Code != http.StatusBadRequest || errorType(t, w) != ErrorRequestRefused {
			t.Fatalf("the name %q: got %d %s, want 400 %s", name, w.Code, errorType(t, w), ErrorRequestRefused)
		}
	}
	if len(f.store.created) != 0 {
		t.Fatalf("a refused name made the containers %v", f.store.created)
	}
}

// TestNonAdminRefusedSharedPrefix: the project-shared prefix is the derived
// admin list's (D1935, D1940) — a member who is not on it writes nothing there.
func TestNonAdminRefusedSharedPrefix(t *testing.T) {
	f := member(t)
	memberTok := f.token(t, testOwnerEmail, testOwnerSub)

	for _, path := range []string{CreatePath, VersionPath} {
		w := f.post(t, path, memberTok, createBody(ScopeShared, "openrouter-key", testValue))
		if w.Code != http.StatusForbidden || errorType(t, w) != ErrorNotAdmin {
			t.Fatalf("%s on the shared prefix: got %d %s, want 403 %s", path, w.Code, errorType(t, w), ErrorNotAdmin)
		}
	}
	if len(f.store.created) != 0 || len(f.store.values) != 0 {
		t.Fatalf("a refused shared write reached the store: created %v, values %v", f.store.created, f.store.values)
	}

	// The admin on the list holds it.
	adminTok := f.token(t, testAdminEmail, testAdminSub)
	w := f.post(t, CreatePath, adminTok, createBody(ScopeShared, "openrouter-key", testValue))
	if w.Code != http.StatusOK {
		t.Fatalf("the admin's shared create: got %d %s", w.Code, w.Body.String())
	}
	if !slices.Equal(f.store.created, []string{secretname.ProjectPrefix + "openrouter-key"}) {
		t.Fatalf("the created containers are %v", f.store.created)
	}
}

// TestListNeverReturnsValue: a list answers names and dates, and there is no
// version's value on the answer at all (G2).
func TestListNeverReturnsValue(t *testing.T) {
	f := member(t)
	tok := f.token(t, testOwnerEmail, testOwnerSub)
	if w := f.post(t, CreatePath, tok, createBody(ScopeUser, "api-key", testValue)); w.Code != http.StatusOK {
		t.Fatalf("create: got %d %s", w.Code, w.Body.String())
	}
	f.lines.Reset()

	w := f.post(t, ListPath, tok, `{"scope":"user"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("list: got %d %s", w.Code, w.Body.String())
	}
	raw := w.Body.Bytes()
	for _, secret := range []string{testValue, b64(testValue)} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("the list answer carries the value: %s", raw)
		}
	}
	var answer ListAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("the list answer: %v (%s)", err, raw)
	}
	if len(answer.Secrets) != 1 || answer.Secrets[0].ID != secretname.UserPrefix+testOwnerSub+"_api-key" {
		t.Fatalf("the list answer is %+v", answer.Secrets)
	}
	if answer.Secrets[0].Name != "api-key" || answer.Secrets[0].Scope != ScopeUser {
		t.Fatalf("the listed entry is %+v", answer.Secrets[0])
	}
	// The fields are the contract's, field for field: a value added to this
	// shape later is a change to this test.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("the list answer: %v", err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(fields["secrets"], &entries); err != nil {
		t.Fatalf("the list answer's entries: %v", err)
	}
	keys := make([]string, 0, len(entries[0]))
	for k := range entries[0] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"created", "id", "name", "scope"}; !slices.Equal(keys, want) {
		t.Fatalf("the listed entry's fields are %v, want %v", keys, want)
	}
}

// TestListAnswersOnlyTheCallersOwnContainers: the per-user list is the
// caller's own containers and no other member's. The scope's list call answers
// every member's — the stored filter is the scope, and the grant cannot name a
// prefix (F30) — so the name is what says whose a container is: a row answered
// from another account's name would hand one launcher another member's
// credential names and account id (R3, R12).
func TestListAnswersOnlyTheCallersOwnContainers(t *testing.T) {
	f := member(t)
	mine := secretname.UserPrefix + testOwnerSub + "_api-key"
	theirs := secretname.UserPrefix + testOtherSub + "_other-key"
	f.store.containers[mine] = Secret{ID: mine, Annotations: map[string]string{
		AnnotationOwnerSub: testOwnerSub, AnnotationOwnerEmail: testOwnerEmail, AnnotationCreatedBy: testOwnerEmail,
	}}
	f.store.containers[theirs] = Secret{ID: theirs, Annotations: map[string]string{
		AnnotationOwnerSub: testOtherSub, AnnotationOwnerEmail: testOtherEmail, AnnotationCreatedBy: testOtherEmail,
	}}

	w := f.post(t, ListPath, f.token(t, testOwnerEmail, testOwnerSub), `{"scope":"user"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("list: got %d %s", w.Code, w.Body.String())
	}
	for _, leak := range []string{theirs, testOtherSub, "other-key"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("the answer carries another member's container (%s): %s", leak, w.Body.String())
		}
	}
	var answer ListAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the list answer: %v (%s)", err, w.Body.String())
	}
	if len(answer.Secrets) != 1 || answer.Secrets[0].ID != mine {
		t.Fatalf("the list answer is %+v, want only %s", answer.Secrets, mine)
	}
}

// TestNonAdminCannotListAnOwnersPrefix: a member named by address is the
// admin's lookup — the one the offboarding command reads a member's containers
// through (R10) — and a caller who is not on the derived list reads nothing by
// it, not another member's and not their own.
func TestNonAdminCannotListAnOwnersPrefix(t *testing.T) {
	f := member(t)
	theirs := secretname.UserPrefix + testOtherSub + "_api-key"
	f.store.containers[theirs] = Secret{ID: theirs, Annotations: map[string]string{
		AnnotationOwnerSub: testOtherSub, AnnotationOwnerEmail: testOtherEmail, AnnotationCreatedBy: testOtherEmail,
	}}

	w := f.post(t, ListPath, f.token(t, testOwnerEmail, testOwnerSub), `{"scope":"user","owner":"`+testOtherEmail+`"}`)
	if w.Code != http.StatusForbidden || errorType(t, w) != ErrorNotAdmin {
		t.Fatalf("a member's list of another owner: got %d %s, want 403 %s", w.Code, errorType(t, w), ErrorNotAdmin)
	}
	for _, leak := range []string{theirs, testOtherSub, "api-key"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("a refused list answered with a container (%s): %s", leak, w.Body.String())
		}
	}

	// The admin on the derived list reads it, which is what the address is
	// for: the guard refuses the caller, not the lookup.
	w = f.post(t, ListPath, f.token(t, testAdminEmail, testAdminSub), `{"scope":"user","owner":"`+testOtherEmail+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the admin's list of another owner: got %d %s", w.Code, w.Body.String())
	}
	var answer ListAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the list answer: %v (%s)", err, w.Body.String())
	}
	if len(answer.Secrets) != 1 || answer.Secrets[0].ID != theirs {
		t.Fatalf("the admin's list answer is %+v, want %s", answer.Secrets, theirs)
	}
}

// TestNonAdminCannotListTheSharedScope: the project-shared prefix is the
// derived admin list's (D1935, D1940), so the names under it are read by that
// list alone.
func TestNonAdminCannotListTheSharedScope(t *testing.T) {
	f := member(t)
	shared := secretname.ProjectPrefix + "openrouter-key"
	f.store.containers[shared] = Secret{ID: shared, Annotations: map[string]string{AnnotationCreatedBy: testAdminEmail}}

	w := f.post(t, ListPath, f.token(t, testOwnerEmail, testOwnerSub), `{"scope":"shared"}`)
	if w.Code != http.StatusForbidden || errorType(t, w) != ErrorNotAdmin {
		t.Fatalf("a member's list of the shared scope: got %d %s, want 403 %s", w.Code, errorType(t, w), ErrorNotAdmin)
	}
	if strings.Contains(w.Body.String(), "openrouter-key") {
		t.Fatalf("a refused list answered with a shared name: %s", w.Body.String())
	}

	w = f.post(t, ListPath, f.token(t, testAdminEmail, testAdminSub), `{"scope":"shared"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the admin's list of the shared scope: got %d %s", w.Code, w.Body.String())
	}
	var answer ListAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the list answer: %v (%s)", err, w.Body.String())
	}
	if len(answer.Secrets) != 1 || answer.Secrets[0].ID != shared || answer.Secrets[0].Scope != ScopeShared {
		t.Fatalf("the admin's list answer is %+v, want %s in the shared scope", answer.Secrets, shared)
	}
}

// TestNoReadMethodExists: the intake's whole reach in Secret Manager is the
// five calls below, and none of them answers a value; the routes it serves are
// the four, and none of them reads one (G2).
func TestNoReadMethodExists(t *testing.T) {
	typ := reflect.TypeOf((*SecretStore)(nil)).Elem()
	var methods []string
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		methods = append(methods, m.Name)
		for j := range m.Type.NumOut() {
			if m.Type.Out(j) == reflect.TypeOf([]byte(nil)) {
				t.Errorf("SecretStore.%s answers []byte: no method of this interface may hand a value back", m.Name)
			}
		}
	}
	slices.Sort(methods)
	if want := []string{"AddVersion", "Create", "Delete", "Describe", "List"}; !slices.Equal(methods, want) {
		t.Fatalf("SecretStore's methods are %v, want %v", methods, want)
	}

	want := []string{CreatePath, VersionPath, ListPath, DeletePath}
	got := Routes()
	if !slices.Equal(got, want) {
		t.Fatalf("the intake serves %v, want exactly %v", got, want)
	}
	for _, p := range got {
		for _, word := range []string{"read", "access", "value", "reveal"} {
			if strings.Contains(p, word) {
				t.Fatalf("the route %s names %q: the intake reads no value", p, word)
			}
		}
	}
}

// TestServiceAccountRefused: a service account is not a launcher (G13) — the
// deployment cannot name one, and its token is refused at the route.
func TestServiceAccountRefused(t *testing.T) {
	job := "fugaro-job@aurora-dev.iam.gserviceaccount.com"
	if _, err := credsvc.NewLaunchers(credsvc.LauncherConfig{Audience: testAudience, Accounts: []string{job}, Keys: testKeys()}); err == nil {
		t.Fatal("the launcher class admitted a service account address")
	}

	f := member(t)
	w := f.post(t, CreatePath, idToken(t, job, testOwnerSub), createBody(ScopeUser, "api-key", testValue))
	if w.Code != http.StatusForbidden || errorType(t, w) != ErrorUnverifiedLauncher {
		t.Fatalf("a service account's call: got %d %s, want 403 %s", w.Code, errorType(t, w), ErrorUnverifiedLauncher)
	}
	if len(f.store.created) != 0 {
		t.Fatalf("a refused caller made the containers %v", f.store.created)
	}
}

// TestNonAdminCannotDeleteOtherPrefix: a member deletes under their own prefix
// and nowhere else — not by naming another account's container, and not by
// naming its owner (D1935, R10).
func TestNonAdminCannotDeleteOtherPrefix(t *testing.T) {
	f := member(t)
	other := secretname.UserPrefix + testOtherSub + "_api-key"
	f.store.containers[other] = Secret{ID: other, Annotations: map[string]string{
		AnnotationOwnerSub:   testOtherSub,
		AnnotationOwnerEmail: testOtherEmail,
		AnnotationCreatedBy:  testOtherEmail,
	}}
	memberTok := f.token(t, testOwnerEmail, testOwnerSub)

	w := f.post(t, DeletePath, memberTok, `{"scope":"user","owner":"`+testOtherEmail+`"}`)
	if w.Code != http.StatusForbidden || errorType(t, w) != ErrorNotAdmin {
		t.Fatalf("a member's offboarding sweep: got %d %s, want 403 %s", w.Code, errorType(t, w), ErrorNotAdmin)
	}
	// A name is a credential name and never a secret ID, so another account's
	// container cannot be spelled as one.
	w = f.post(t, DeletePath, memberTok, `{"scope":"user","name":"`+other+`"}`)
	if w.Code != http.StatusBadRequest || errorType(t, w) != ErrorRequestRefused {
		t.Fatalf("another account's ID as a name: got %d %s, want 400 %s", w.Code, errorType(t, w), ErrorRequestRefused)
	}
	if len(f.store.deleted) != 0 {
		t.Fatalf("a refused delete removed %v", f.store.deleted)
	}
	if _, ok := f.store.containers[other]; !ok {
		t.Fatal("another member's container was deleted")
	}
}

// TestAdminDeletesAnotherOwnersPrefix is the offboarding sweep the admin list
// admits: the member is named by address and their containers are found by the
// owner annotation (R10).
func TestAdminDeletesAnotherOwnersPrefix(t *testing.T) {
	f := member(t)
	first := secretname.UserPrefix + testOwnerSub + "_api-key"
	second := secretname.UserPrefix + testOtherSub + "_api-key"
	for id, email := range map[string]string{first: testOwnerEmail, second: testOtherEmail} {
		f.store.containers[id] = Secret{ID: id, Annotations: map[string]string{
			AnnotationOwnerSub:   strings.TrimPrefix(strings.TrimSuffix(id, "_api-key"), secretname.UserPrefix),
			AnnotationOwnerEmail: email,
			AnnotationCreatedBy:  email,
		}}
	}

	adminTok := f.token(t, testAdminEmail, testAdminSub)
	w := f.post(t, DeletePath, adminTok, `{"scope":"user","owner":"`+testOtherEmail+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the admin's sweep: got %d %s", w.Code, w.Body.String())
	}
	if !slices.Equal(f.store.deleted, []string{second}) {
		t.Fatalf("the sweep deleted %v, want %v", f.store.deleted, []string{second})
	}
	if _, ok := f.store.containers[first]; !ok {
		t.Fatal("the sweep deleted a container of another owner")
	}
}

// TestRefusesContainerItDidNotCreate: a container whose shape is not the
// intake's own — a TTL, an expiry, a topic, a CMEK, a rotation schedule, or
// annotations another writer set — is refused before any write, and before it
// is removed (R12, F35). A delete is a write to an existing container, so the
// same check is the whole of what stands between another writer's container
// and its destruction.
func TestRefusesContainerItDidNotCreate(t *testing.T) {
	ours := func() map[string]string {
		return map[string]string{
			AnnotationOwnerSub:   testOwnerSub,
			AnnotationOwnerEmail: testOwnerEmail,
			AnnotationCreatedBy:  testOwnerEmail,
		}
	}
	id := secretname.UserPrefix + testOwnerSub + "_api-key"
	for _, tc := range []struct {
		what string
		s    Secret
	}{
		{"a TTL", Secret{ID: id, Annotations: ours(), Shape: Shape{TTL: "86400s"}}},
		{"an expiry", Secret{ID: id, Annotations: ours(), Shape: Shape{ExpireTime: "2027-01-01T00:00:00Z"}}},
		{"a topic", Secret{ID: id, Annotations: ours(), Shape: Shape{Topics: []string{"projects/aurora-dev/topics/secrets"}}}},
		{"a CMEK key", Secret{ID: id, Annotations: ours(), Shape: Shape{CMEK: "projects/aurora-dev/locations/global/keyRings/k/cryptoKeys/key"}}},
		{"a rotation schedule", Secret{ID: id, Annotations: ours(), Shape: Shape{Rotation: "2592000s"}}},
		{"another owner's annotation", Secret{ID: id, Annotations: map[string]string{
			AnnotationOwnerSub: testOwnerSub, AnnotationOwnerEmail: testOtherEmail, AnnotationCreatedBy: testOwnerEmail,
		}}},
		// The name says whose container it is (R12), so an owner account the
		// name does not spell is another writer's.
		{"an owner account that is not the name's", Secret{ID: id, Annotations: map[string]string{
			AnnotationOwnerSub: testOtherSub, AnnotationOwnerEmail: testOwnerEmail, AnnotationCreatedBy: testOwnerEmail,
		}}},
		{"an annotation the intake does not write", Secret{ID: id, Annotations: map[string]string{
			AnnotationOwnerSub: testOwnerSub, AnnotationOwnerEmail: testOwnerEmail, AnnotationCreatedBy: testOwnerEmail, "fugaro_extra": "x",
		}}},
	} {
		f := member(t)
		f.store.containers[id] = tc.s
		tok := f.token(t, testOwnerEmail, testOwnerSub)
		for _, route := range []struct{ path, body string }{
			{CreatePath, createBody(ScopeUser, "api-key", testValue)},
			{VersionPath, createBody(ScopeUser, "api-key", testValue)},
			{DeletePath, `{"scope":"user","name":"api-key"}`},
		} {
			w := f.post(t, route.path, tok, route.body)
			if w.Code != http.StatusConflict || errorType(t, w) != ErrorForeignContainer {
				t.Fatalf("%s on a container with %s: got %d %s, want 409 %s", route.path, tc.what, w.Code, errorType(t, w), ErrorForeignContainer)
			}
		}
		if len(f.store.values) != 0 {
			t.Fatalf("a container with %s was written into", tc.what)
		}
		if len(f.store.deleted) != 0 {
			t.Fatalf("a container with %s was deleted", tc.what)
		}
		if _, ok := f.store.containers[id]; !ok {
			t.Fatalf("a container with %s was removed", tc.what)
		}
	}
}

// TestRefusesASharedContainerItDidNotCreate is the same check's shared branch,
// where the annotation it rests on is the one that says who made the container
// (R10). A project-shared container has no owner, so who made it is the whole
// of what it carries: a shared container with no annotations at all, or with
// an owner's on it, is another writer's, and there is nothing else on it that
// could say otherwise — which is why this is the branch that holds the rule.
func TestRefusesASharedContainerItDidNotCreate(t *testing.T) {
	id := secretname.ProjectPrefix + "openrouter-key"
	made := map[string]string{AnnotationCreatedBy: testAdminEmail}
	for _, tc := range []struct {
		what string
		s    Secret
	}{
		{"no annotations at all", Secret{ID: id, Annotations: map[string]string{}}},
		{"a TTL", Secret{ID: id, Annotations: made, Shape: Shape{TTL: "86400s"}}},
		{"an owner's annotations", Secret{ID: id, Annotations: map[string]string{
			AnnotationCreatedBy: testAdminEmail, AnnotationOwnerSub: testAdminSub, AnnotationOwnerEmail: testAdminEmail,
		}}},
	} {
		f := member(t)
		f.store.containers[id] = tc.s
		tok := f.token(t, testAdminEmail, testAdminSub)
		for _, route := range []struct{ path, body string }{
			{CreatePath, createBody(ScopeShared, "openrouter-key", testValue)},
			{VersionPath, createBody(ScopeShared, "openrouter-key", testValue)},
			{DeletePath, "{\"scope\":\"shared\",\"name\":\"openrouter-key\"}"},
		} {
			w := f.post(t, route.path, tok, route.body)
			if w.Code != http.StatusConflict || errorType(t, w) != ErrorForeignContainer {
				t.Fatalf("%s on a shared container with %s: got %d %s, want 409 %s", route.path, tc.what, w.Code, errorType(t, w), ErrorForeignContainer)
			}
		}
		if len(f.store.values) != 0 || len(f.store.deleted) != 0 {
			t.Fatalf("a shared container with %s was written into (%v) or deleted (%v)", tc.what, f.store.values, f.store.deleted)
		}
		if _, ok := f.store.containers[id]; !ok {
			t.Fatalf("a shared container with %s was removed", tc.what)
		}
	}
}

// TestARefusedSweepRemovesNothing: the offboarding sweep puts every container
// of the member's prefix to the shape check before it removes any of them, so
// a prefix holding one container the intake did not make is refused with the
// whole sweep undone — the member's own credentials are the ones a half-done
// sweep would have taken, and nothing would say so (R12, DS2.4).
//
// The fake answers a prefix in name order, so the member's own container is
// the one a check made inside the delete loop would remove before it met the
// foreign one.
func TestARefusedSweepRemovesNothing(t *testing.T) {
	f := member(t)
	good := secretname.UserPrefix + testOtherSub + "_api-key"
	foreign := secretname.UserPrefix + testOtherSub + "_bad-key"
	memberAnnotations := map[string]string{
		AnnotationOwnerSub: testOtherSub, AnnotationOwnerEmail: testOtherEmail, AnnotationCreatedBy: testOtherEmail,
	}
	f.store.containers[good] = Secret{ID: good, Annotations: memberAnnotations}
	f.store.containers[foreign] = Secret{ID: foreign, Annotations: memberAnnotations, Shape: Shape{TTL: "86400s"}}

	w := f.post(t, DeletePath, f.token(t, testAdminEmail, testAdminSub), `{"scope":"user","owner":"`+testOtherEmail+`"}`)
	if w.Code != http.StatusConflict || errorType(t, w) != ErrorForeignContainer {
		t.Fatalf("a sweep of a prefix holding a foreign container: got %d %s, want 409 %s", w.Code, errorType(t, w), ErrorForeignContainer)
	}
	if len(f.store.deleted) != 0 {
		t.Fatalf("a refused sweep deleted %v", f.store.deleted)
	}
	for _, id := range []string{good, foreign} {
		if _, ok := f.store.containers[id]; !ok {
			t.Fatalf("a refused sweep removed %s", id)
		}
	}
}

// TestRefusesAContainerInvisibleToItAndAlreadyThere is R12's window in the
// shape the deployment actually has it (F35). The intake's Describe IS the list
// call, filtered by the labels it writes, so a container the managed deployer
// pre-created — the deployer holds secrets.create unconditionally — is one
// Create answers ErrExists for and Describe cannot see, either time. Nothing
// may be written into it, and the second read in ensure is the whole of that
// refusal: without it the branch would answer 200 and rotate a credential into
// a container another writer set up to take it away.
func TestRefusesAContainerInvisibleToItAndAlreadyThere(t *testing.T) {
	f := member(t)
	id := secretname.UserPrefix + testOwnerSub + "_api-key"
	f.store.foreign[id] = true
	tok := f.token(t, testOwnerEmail, testOwnerSub)

	w := f.post(t, CreatePath, tok, createBody(ScopeUser, "api-key", testValue))
	if got := errorType(t, w); w.Code != http.StatusConflict || got != ErrorForeignContainer {
		t.Fatalf("got %d %s, want 409 %s", w.Code, got, ErrorForeignContainer)
	}
	// The act the caller asked for did not happen, under any name.
	for _, call := range f.store.calls {
		if strings.HasPrefix(call, "version ") {
			t.Fatalf("a credential was written into a container the intake cannot see: %v", f.store.calls)
		}
	}
	if len(f.store.values) != 0 {
		t.Fatalf("the intake wrote %v", f.store.values)
	}
	if _, ok := f.store.containers[id]; ok {
		t.Fatal("the intake answered for a container it did not make")
	}
	// The line names the container, so an operator has the one handle there is
	// on a state a person has to look at.
	if line := audit(t, f); line["id"] != id || line["status"] != ErrorForeignContainer {
		t.Fatalf("the line is %v, want the container named and %s", line, ErrorForeignContainer)
	}

	// A version call on the same container is the other half of the same
	// window: it must find a container it can see, so it is a 404 and not a
	// write (the accepted consequence of Describe being the filtered list).
	f.lines.Reset()
	w = f.post(t, VersionPath, tok, createBody(ScopeUser, "api-key", testValue))
	if got := errorType(t, w); w.Code != http.StatusNotFound || got != ErrorNoSuchSecret {
		t.Fatalf("a version into a container the intake cannot see: got %d %s, want 404 %s", w.Code, got, ErrorNoSuchSecret)
	}
	if len(f.store.values) != 0 {
		t.Fatalf("the intake wrote %v", f.store.values)
	}
	// The refusal is recorded like every other call, and the line names the
	// container (G6).
	if line := audit(t, f); line["id"] != id || line["status"] != ErrorNoSuchSecret {
		t.Fatalf("the line is %v, want the container named and %s", line, ErrorNoSuchSecret)
	}
}

// TestAContainerItFoundLateIsNotReportedAsMadeByThisCall: the other side of
// the same window, where the second read FINDS the container. Secret Manager's
// list call — which is what Describe is here — is eventually consistent, so a
// container this intake wrote a moment ago (or one another writer made with
// this intake's own shape) can be invisible to the read before Create and
// visible to the read after it. Create then answered ErrExists, which means
// this call did NOT make the container: SecretAnswer.Created is "made by this
// call" (its own doc, and what the found-path of ensure answers for the same
// outcome), so the answer is a rotation, not a creation.
func TestAContainerItFoundLateIsNotReportedAsMadeByThisCall(t *testing.T) {
	f := member(t)
	id := secretname.UserPrefix + testOwnerSub + "_api-key"
	f.store.late[id] = Secret{ID: id, Annotations: map[string]string{
		AnnotationOwnerSub:   testOwnerSub,
		AnnotationOwnerEmail: testOwnerEmail,
		AnnotationCreatedBy:  testOwnerEmail,
	}}

	w := f.post(t, CreatePath, f.token(t, testOwnerEmail, testOwnerSub), createBody(ScopeUser, "api-key", testValue))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200: the container carries this intake's own shape, so the call is a rotation", w.Code, w.Body.String())
	}
	var answer SecretAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the answer is not one: %v (%s)", err, w.Body.String())
	}
	if answer.Created {
		t.Fatalf("the answer reports the container as made by this call: %s", w.Body.String())
	}
	// The rotation itself happened: the version is this call's and the store
	// holds the value it carried.
	if answer.Version == "" || answer.ID != id {
		t.Fatalf("the answer is %+v, want the container and a version", answer)
	}
	if got := f.store.values[id]; len(got) != 1 || got[0] != testValue {
		t.Fatalf("the store holds %v, want the one value the call carried", got)
	}
}

// TestOneAuditLinePerCall: every call the intake answers writes exactly one
// line, and the line names the act, the caller and the status — never a value
// the call carried (G6, G13).
func TestOneAuditLinePerCall(t *testing.T) {
	f := member(t)
	memberTok := f.token(t, testOwnerEmail, testOwnerSub)
	adminTok := f.token(t, testAdminEmail, testAdminSub)

	calls := []struct {
		what string
		path string
		tok  string
		body string
		code int
		act  string
	}{
		{"create", CreatePath, memberTok, createBody(ScopeUser, "api-key", testValue), http.StatusOK, OpCreate},
		{"version", VersionPath, memberTok, createBody(ScopeUser, "api-key", testValue), http.StatusOK, OpVersion},
		{"list", ListPath, memberTok, `{"scope":"user"}`, http.StatusOK, OpList},
		{"delete", DeletePath, memberTok, `{"scope":"user","name":"api-key"}`, http.StatusOK, OpDelete},
		{"shared by a member", CreatePath, memberTok, createBody(ScopeShared, "x", testValue), http.StatusForbidden, OpCreate},
		{"a name outside the grammar", CreatePath, memberTok, createBody(ScopeUser, "API-KEY", testValue), http.StatusBadRequest, OpCreate},
		{"an unverified caller", CreatePath, "", createBody(ScopeUser, "api-key", testValue), http.StatusForbidden, OpCreate},
		{"an admin's sweep", DeletePath, adminTok, `{"scope":"user","owner":"` + testOtherEmail + `"}`, http.StatusOK, OpDelete},
	}
	for _, tc := range calls {
		f.lines.Reset()
		w := f.post(t, tc.path, tc.tok, tc.body)
		if w.Code != tc.code {
			t.Fatalf("%s: got %d %s, want %d", tc.what, w.Code, w.Body.String(), tc.code)
		}
		line := audit(t, f)
		if line["msg"] != auditMessage {
			t.Fatalf("%s: the line's message is %v", tc.what, line["msg"])
		}
		if line["op"] != tc.act {
			t.Fatalf("%s: the line's op is %v, want %q", tc.what, line["op"], tc.act)
		}
		if tc.tok == "" {
			if line["caller"] != "" || line["sub"] != "" {
				t.Fatalf("%s: an unverified caller's line names %v %v", tc.what, line["caller"], line["sub"])
			}
		} else if line["caller"] == "" {
			t.Fatalf("%s: the line names no caller", tc.what)
		}
		if line["status"] == "" {
			t.Fatalf("%s: the line names no status", tc.what)
		}
		// The value is nowhere in the line, in either spelling.
		for _, secret := range []string{testValue, b64(testValue)} {
			if strings.Contains(f.lines.String(), secret) {
				t.Fatalf("%s: the audit line carries the value: %s", tc.what, f.lines.String())
			}
		}
	}
}

// TestAValueThatSpellsTheLinesOwnTextStillRecordsTheCall: the line carries one
// of this package's own words, an account id and a container name, and a
// credential is bytes the caller chose — one byte is enough to spell part of
// any of them. An audit line the auditor refused for that would answer
// state_unavailable for a call whose act had already landed: an act in Secret
// Manager that no operator could read afterwards, which is worse than the
// over-redaction of a text field and is what this pins. The value is a word or
// a digit here, never a credential.
func TestAValueThatSpellsTheLinesOwnTextStillRecordsTheCall(t *testing.T) {
	// "1" is a byte of the account id and therefore of the container's name,
	// "e" and "key" are bytes of that name, and "create", "user" and "ok" are
	// the line's own closed-set values.
	for _, value := range []string{"1", "e", "ok", "user", "create", "key"} {
		t.Run(value, func(t *testing.T) {
			f := member(t)
			id := "fugaro_u_" + testOwnerSub + "_api-key"
			w := f.post(t, CreatePath, f.token(t, testOwnerEmail, testOwnerSub), createBody(ScopeUser, "api-key", value))
			if w.Code != http.StatusOK {
				t.Fatalf("got %d %s, want 200: the credential is in the store, so the call must be answered and recorded as the act it was", w.Code, w.Body.String())
			}
			if got := f.store.values[id]; len(got) != 1 || got[0] != value {
				t.Fatalf("the store holds %d values, want the one the call carried", len(got))
			}
			line := audit(t, f)
			if line["op"] != OpCreate {
				t.Errorf("the line's op is %v, want %q: the act is the line's own word and never the value's", line["op"], OpCreate)
			}
			if line["scope"] != string(ScopeUser) {
				t.Errorf("the line's scope is %v, want %q", line["scope"], ScopeUser)
			}
			if line["status"] != credsvc.StatusOK {
				t.Errorf("the line's status is %v, want %q", line["status"], credsvc.StatusOK)
			}
			// The name is a text field and may be over-redacted by the value:
			// a mangled name is readable enough to act on, and the act above is
			// what a reader greps for.
			if line["id"] == "" {
				t.Error("the line names no container")
			}
		})
	}
}

// TestTheLineNeverCarriesTheValueItRecorded: the scrub is the whole of the
// rule that the credential a call carried never reaches the log, and the only
// place it is observable is where the value would otherwise be written — a
// value that spells part of one of the text fields the line carries (G6, R10).
// Three fields are the ones a call that carries a value puts text in: the
// caller's address, the account id, and the container's name. (The owner field
// is the list's and the sweep's, and those carry no value to scrub: their only
// caller-controlled text is an address that canonicalAddress accepts, so a
// credential is refused as the malformed owner it is.) The closed-set fields
// are the other half of the line's rule and are the test above's.
func TestTheLineNeverCarriesTheValueItRecorded(t *testing.T) {
	for _, tc := range []struct {
		what   string
		value  string
		caller string
		sub    string
		body   string
		field  string
	}{
		{"the caller's address", testOwnerEmail, testOwnerEmail, testOwnerSub,
			createBody(ScopeUser, "api-key", testOwnerEmail), "caller"},
		{"the account id", testOwnerSub, testOwnerEmail, testOwnerSub,
			createBody(ScopeUser, "api-key", testOwnerSub), "sub"},
		{"the container's own name", "api-key", testOwnerEmail, testOwnerSub,
			createBody(ScopeUser, "api-key", "api-key"), "id"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := member(t)
			w := f.post(t, CreatePath, f.token(t, tc.caller, tc.sub), tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("got %d %s, want 200", w.Code, w.Body.String())
			}
			line := audit(t, f)
			field, _ := line[tc.field].(string)
			// The field was filled from the call — it is neither empty nor the
			// value — so the value really was there to be written.
			if field == "" || field == tc.value {
				t.Fatalf("the line's %s is %q, so this case proves nothing about the scrub", tc.field, field)
			}
			if strings.Contains(f.lines.String(), tc.value) {
				t.Fatalf("the line carries the value in its %s: %s", tc.field, f.lines.String())
			}
		})
	}
}

// TestAnAuditorThatWasNotToldTheCallsValueWritesNoLine: Write refuses an
// auditor that was never armed with the value the call carried, so a handler
// that forgot is a refusal and not a line a value reached (D143). The event
// below is one every rule of checked() accepts, so the arming is the only
// thing that can refuse it — and the armed auditor writing the same event is
// what says so.
func TestAnAuditorThatWasNotToldTheCallsValueWritesNoLine(t *testing.T) {
	ev := AuditEvent{
		Caller: testOwnerEmail,
		Sub:    testOwnerSub,
		Op:     OpCreate,
		Scope:  string(ScopeUser),
		ID:     secretname.UserPrefix + testOwnerSub + "_api-key",
		Status: credsvc.StatusOK,
	}
	newLog := func(t *testing.T) (*Auditor, *bytes.Buffer) {
		t.Helper()
		lines := &bytes.Buffer{}
		a, err := NewAuditor(slog.NewJSONHandler(lines, &slog.HandlerOptions{Level: slog.LevelInfo}))
		if err != nil {
			t.Fatalf("building the auditor: %v", err)
		}
		return a, lines
	}

	t.Run("unarmed", func(t *testing.T) {
		a, lines := newLog(t)
		if err := a.Write(context.Background(), ev); err == nil {
			t.Fatal("an auditor that was never told what value the call carried wrote the line: a credential could have reached it")
		}
		if lines.Len() != 0 {
			t.Fatalf("the refusal wrote %s", lines.String())
		}
	})

	t.Run("armed with the call's value", func(t *testing.T) {
		a, lines := newLog(t)
		if err := a.withValue([]byte(testValue)).Write(context.Background(), ev); err != nil {
			t.Fatalf("the armed auditor refused the event: %v", err)
		}
		if lines.Len() == 0 {
			t.Fatal("the armed auditor wrote no line")
		}
	})

	// The option is the other way a line is armed, and it is for a caller that
	// holds a value this package never sees: an auditor armed that way writes
	// too, which is what makes the refusal above the arming and not the event.
	t.Run("armed by the option", func(t *testing.T) {
		lines := &bytes.Buffer{}
		a, err := NewAuditor(slog.NewJSONHandler(lines, &slog.HandlerOptions{Level: slog.LevelInfo}),
			WithAuditScrub(func(s string) string { return s }))
		if err != nil {
			t.Fatalf("building the auditor: %v", err)
		}
		if err := a.Write(context.Background(), ev); err != nil {
			t.Fatalf("an auditor armed with its own redaction refused the event: %v", err)
		}
		if lines.Len() == 0 {
			t.Fatal("the armed auditor wrote no line")
		}
	})
}

// TestTheLineRefusesFieldsThisIntakeDoesNotWrite: checked() is the rule that
// keeps a field something else wrote into out of the log — every field is a
// closed set or a shape this intake itself built, and a line is refused rather
// than written when one is not. It is a SHAPE rule and not the value rule (a
// credential is printable ASCII with no space and would pass most of these);
// what the value rule rests on is the scrub, and the two orders are the test
// above and round one's. The control at the end is the same auditor writing a
// field put right, so a case cannot pass for a reason of its own.
func TestTheLineRefusesFieldsThisIntakeDoesNotWrite(t *testing.T) {
	valid := AuditEvent{
		Caller: testOwnerEmail,
		Sub:    testOwnerSub,
		Op:     OpCreate,
		Scope:  string(ScopeUser),
		ID:     secretname.UserPrefix + testOwnerSub + "_api-key",
		Status: credsvc.StatusOK,
	}
	for _, tc := range []struct {
		what string
		edit func(*AuditEvent)
	}{
		{"an act no route serves", func(e *AuditEvent) { e.Op = "read" }},
		{"a scope this release does not have", func(e *AuditEvent) { e.Scope = "root" }},
		{"a status no path writes", func(e *AuditEvent) { e.Status = "gone" }},
		{"a caller that is not one address", func(e *AuditEvent) { e.Caller = "not an address" }},
		{"an account id no container is built from", func(e *AuditEvent) { e.Sub = "not-a-sub" }},
		{"a container that is not one this intake writes", func(e *AuditEvent) { e.ID = "other-container" }},
		{"an owner that is not one address", func(e *AuditEvent) { e.Owner = "not an address" }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			lines := &bytes.Buffer{}
			a, err := NewAuditor(slog.NewJSONHandler(lines, &slog.HandlerOptions{Level: slog.LevelInfo}))
			if err != nil {
				t.Fatalf("building the auditor: %v", err)
			}
			ev := valid
			tc.edit(&ev)
			if err := a.withValue([]byte(testValue)).Write(context.Background(), ev); err == nil {
				t.Fatalf("the line was written with %s: %+v", tc.what, ev)
			}
			if lines.Len() != 0 {
				t.Fatalf("the refusal wrote %s", lines.String())
			}
			// The same auditor writes the event the field is right on.
			if err := a.withValue([]byte(testValue)).Write(context.Background(), valid); err != nil {
				t.Fatalf("the auditor refused an event every rule accepts: %v", err)
			}
		})
	}
}

// TestARefusedRequestStillWritesItsLine: the scope and the owner a call names
// go on the line, so a scope or an owner this release does not have is refused
// as the request it is — 400 request_refused and one line — and not answered
// state_unavailable with no line at all. A record a caller can suppress by
// sending a scope the intake does not serve is not a record.
func TestARefusedRequestStillWritesItsLine(t *testing.T) {
	f := member(t)
	memberTok := f.token(t, testOwnerEmail, testOwnerSub)
	adminTok := f.token(t, testAdminEmail, testAdminSub)
	value := b64(testValue)
	for _, tc := range []struct {
		what string
		path string
		tok  string
		body string
	}{
		{"an unknown scope on a create", CreatePath, memberTok, `{"scope":"root","name":"api-key","value":"` + value + `"}`},
		{"an unknown scope on a version", VersionPath, memberTok, `{"scope":"root","name":"api-key","value":"` + value + `"}`},
		{"an unknown scope on a list", ListPath, memberTok, `{"scope":"root"}`},
		{"an unknown scope on a delete", DeletePath, memberTok, `{"scope":"root","name":"api-key"}`},
		{"an unknown scope on a sweep", DeletePath, adminTok, `{"scope":"root","owner":"` + testOtherEmail + `"}`},
		{"a malformed owner on an admin's list", ListPath, adminTok, `{"scope":"user","owner":"not an address"}`},
		{"a malformed owner on a sweep", DeletePath, adminTok, `{"scope":"user","owner":"not an address"}`},
		{"an owner on the shared scope", ListPath, adminTok, `{"scope":"shared","owner":"` + testOtherEmail + `"}`},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f.lines.Reset()
			w := f.post(t, tc.path, tc.tok, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s, want 400: the request is what is wrong here", w.Code, w.Body.String())
			}
			if got := errorType(t, w); got != ErrorRequestRefused {
				t.Fatalf("the refusal is %s, want %s", got, ErrorRequestRefused)
			}
			line := audit(t, f)
			if line["status"] != ErrorRequestRefused {
				t.Fatalf("the line's status is %v, want %s", line["status"], ErrorRequestRefused)
			}
			if line["id"] == nil || line["op"] == "" {
				t.Fatalf("the line does not name the act: %v", line)
			}
		})
	}
}

// TestACallThatCannotBeAuditedIsNotReportedAsDone is the fail-closed rule
// (D143): a call whose one line cannot be written is answered
// state_unavailable, and that is the answer whether the act landed or was
// refused. The intake has no rollback — a create has reached Secret Manager by
// the time the line is due — so the only thing left to refuse is the claim
// that the call is done, which is what an operator finding a gap in the log
// has to see instead of a 200.
func TestACallThatCannotBeAuditedIsNotReportedAsDone(t *testing.T) {
	id := "fugaro_u_" + testOwnerSub + "_api-key"
	for _, tc := range []struct {
		what string
		body string
		// landed is what the store holds afterwards: what makes the two cases
		// different, and the reason the answer cannot depend on it.
		landed bool
	}{
		{"a create that landed", createBody(ScopeUser, "api-key", testValue), true},
		{"a create that was refused", createBody(ScopeUser, "API-KEY", testValue), false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newFixtureLogging(t, []string{testOwnerEmail}, nil, brokenHandler{}, &bytes.Buffer{})
			w := f.post(t, CreatePath, f.token(t, testOwnerEmail, testOwnerSub), tc.body)
			if got := errorType(t, w); w.Code != http.StatusServiceUnavailable || got != ErrorStateUnavailable {
				t.Fatalf("got %d %s, want 503 %s", w.Code, got, ErrorStateUnavailable)
			}
			// Nothing in the answer reports the call: the refusal carries the
			// service's own two fields and none of a success's.
			for _, field := range []string{`"version"`, `"created"`, `"id"`} {
				if strings.Contains(w.Body.String(), field) {
					t.Fatalf("the answer carries %s: %s", field, w.Body.String())
				}
			}
			if landed := len(f.store.values) > 0; landed != tc.landed {
				t.Fatalf("the store holds %v, want the act to have landed = %v", f.store.values, tc.landed)
			}
			if tc.landed {
				if got := f.store.values[id]; len(got) != 1 || got[0] != testValue {
					t.Fatalf("the store holds %v, want the one value the call carried", got)
				}
			}
		})
	}
}

// TestTheBodyBoundLeavesTheValueBoundReachable: the two bounds are one rule
// seen twice — a credential travels base64 in a JSON body — so the body bound
// has to be wide enough for Secret Manager's own value bound. A body bound
// narrower than the encoding of the value bound makes the value rule dead code
// and the real maximum whatever happens to fit, which is a limit neither the
// caller nor the operator can read anywhere.
func TestTheBodyBoundLeavesTheValueBoundReachable(t *testing.T) {
	f := member(t)
	tok := f.token(t, testOwnerEmail, testOwnerSub)
	id := "fugaro_u_" + testOwnerSub + "_api-key"

	// Exactly Secret Manager's own limit: a body the bound admits and a value
	// the value rule admits.
	w := f.post(t, CreatePath, tok, createBody(ScopeUser, "api-key", strings.Repeat("k", maxValueBytes)))
	if w.Code != http.StatusOK {
		t.Fatalf("a credential of exactly %d bytes: got %d %s, want 200", maxValueBytes, w.Code, w.Body.String())
	}
	if got := f.store.values[id]; len(got) != 1 || len(got[0]) != maxValueBytes {
		t.Fatalf("the store holds %d values, want one of %d bytes", len(got), maxValueBytes)
	}

	// One byte over: refused by the value's own rule and not by the body's,
	// which is what makes that rule a rule.
	f.lines.Reset()
	w = f.post(t, CreatePath, tok, createBody(ScopeUser, "api-key", strings.Repeat("k", maxValueBytes+1)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a credential of %d bytes: got %d %s, want 400", maxValueBytes+1, w.Code, w.Body.String())
	}
	if msg := errorMessage(t, w); !strings.Contains(msg, "Secret Manager allows at most") {
		t.Fatalf("the refusal is %q, want the value's own bound", msg)
	}

	// And a body past the body bound is still refused before anything reads it.
	f.lines.Reset()
	w = f.post(t, CreatePath, tok, strings.Repeat("x", maxBodyBytes+1))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a body of %d bytes: got %d, want 400", maxBodyBytes+1, w.Code)
	}
	if msg := errorMessage(t, w); !strings.Contains(msg, "body is over the intake's bound") {
		t.Fatalf("the refusal is %q, want the body's own bound", msg)
	}
}

// TestUnknownRouteIsNotAnAct: a route this intake does not serve names no act
// of its own, so it is refused and writes no line at all — the four routes are
// the whole surface, and every line this process writes is one of them.
func TestUnknownRouteIsNotAnAct(t *testing.T) {
	f := member(t)
	w := f.post(t, "/v1/secrets/read", f.token(t, testOwnerEmail, testOwnerSub), createBody(ScopeUser, "api-key", testValue))
	if w.Code != http.StatusNotFound {
		t.Fatalf("an unknown route: got %d, want 404", w.Code)
	}
	if f.lines.Len() != 0 {
		t.Fatalf("an unknown route wrote %s", f.lines.String())
	}
}
