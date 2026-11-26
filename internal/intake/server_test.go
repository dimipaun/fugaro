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
	f.containers[id] = Secret{ID: id, Annotations: annotations, Labels: labels}
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

// fakeAdmins is DS2.0's derived list, as the intake reads it. err is the read
// failing, which is the state adminOnly's fail-closed rule is about and the
// one a fixture slice cannot be in.
type fakeAdmins struct {
	list []access.Admin
	err  error
}

func (f fakeAdmins) Admins(context.Context) ([]access.Admin, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

// fixture is one intake under test: a real launcher class over the test's key
// set, a store and a list the test can look at, and the audit lines as text.
type fixture struct {
	srv    *Server
	store  *fakeStore
	admins AdminList
	lines  *bytes.Buffer
}

// newFixture builds the intake, admitting the addresses as launchers and
// listing the admins on the derived list, with the auditor a test reads back as
// JSON text.
func newFixture(t *testing.T, launchers []string, admins []access.Admin) *fixture {
	t.Helper()
	return newFixtureAdmins(t, launchers, fakeAdmins{list: admins})
}

// newFixtureAdmins is newFixture over the list's own implementation, for the
// one state a slice cannot be in: a list whose read fails.
func newFixtureAdmins(t *testing.T, launchers []string, admins AdminList) *fixture {
	t.Helper()
	lines := &bytes.Buffer{}
	return newFixtureLogging(t, launchers, admins, slog.NewJSONHandler(lines, &slog.HandlerOptions{Level: slog.LevelInfo}), lines)
}

// newFixtureLogging is newFixture over a named log handler, for the one thing
// a test cannot see through a bytes.Buffer: what the intake does when the log
// itself cannot be written (TestACallThatCannotBeAuditedIsNotReportedAsDone).
func newFixtureLogging(t *testing.T, launchers []string, admins AdminList, h slog.Handler, lines *bytes.Buffer) *fixture {
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
	srv, err := New(Config{Callers: callers, Admins: admins, Secrets: store, Audit: audit})
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

// TestAnUnreadableAdminListRefusesEveryCallItGates: the derived list is read
// on every route that reads or writes the project-shared prefix, and on the
// two that name a member's prefix — R10's offboarding read and its sweep — and
// a read that fails is a refusal, not a leniency (D1935, D1940). The caller
// here is the admin the list names: a failure says nothing about who is on it,
// so it is refused like anyone else's. A leniency would hand the project's
// shared prefix and the offboarding sweep to whoever asks at the moment the
// list is unreadable, which is exactly the moment the intake cannot tell who
// may write the prefix.
func TestAnUnreadableAdminListRefusesEveryCallItGates(t *testing.T) {
	f := newFixtureAdmins(t, []string{testOwnerEmail, testAdminEmail}, fakeAdmins{
		list: []access.Admin{{Email: testAdminEmail, Sub: testAdminSub}},
		err:  errors.New("the derived list could not be read"),
	})
	admin := f.token(t, testAdminEmail, testAdminSub)
	shared := secretname.ProjectPrefix + "openrouter-key"
	f.store.containers[shared] = Secret{ID: shared, Annotations: map[string]string{AnnotationCreatedBy: testAdminEmail}}
	theirs := secretname.UserPrefix + testOtherSub + "_api-key"
	f.store.containers[theirs] = Secret{ID: theirs, Annotations: map[string]string{
		AnnotationOwnerSub: testOtherSub, AnnotationOwnerEmail: testOtherEmail, AnnotationCreatedBy: testOtherEmail,
	}}

	for _, c := range []struct{ what, path, body string }{
		{"the shared scope's list", ListPath, `{"scope":"shared"}`},
		{"a member's prefix read by address", ListPath, `{"scope":"user","owner":"` + testOtherEmail + `"}`},
		{"a member's prefix swept", DeletePath, `{"scope":"user","owner":"` + testOtherEmail + `"}`},
		{"a shared container created", CreatePath, createBody(ScopeShared, "another-key", testValue)},
		{"a shared container's version added", VersionPath, createBody(ScopeShared, "openrouter-key", testValue)},
		{"a shared container deleted", DeletePath, `{"scope":"shared","name":"openrouter-key"}`},
	} {
		w := f.post(t, c.path, admin, c.body)
		if w.Code != http.StatusServiceUnavailable || errorType(t, w) != ErrorStateUnavailable {
			t.Fatalf("an admin with the derived list unreadable, on %s: got %d %s, want 503 %s",
				c.what, w.Code, errorType(t, w), ErrorStateUnavailable)
		}
	}
	if len(f.store.created) != 0 || len(f.store.deleted) != 0 || len(f.store.values[shared]) != 0 {
		t.Fatalf("a refused call wrote: created %v, deleted %v, versions %v", f.store.created, f.store.deleted, f.store.values[shared])
	}
	for _, id := range []string{shared, theirs} {
		if _, ok := f.store.containers[id]; !ok {
			t.Fatalf("a refused call removed %s", id)
		}
	}

	// The list gates the project's prefix and an admin's read of a member's,
	// and nothing else: the caller's own prefix is answered without it, which
	// is what makes the refusals above the list's rather than a refusal of
	// every call the intake serves.
	w := f.post(t, ListPath, admin, `{"scope":"user"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the admin's own list with the derived list unreadable: got %d %s", w.Code, w.Body.String())
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

// TestTheCallerTokenComesFromThePersonHeader: this intake is a Cloud Run
// service whose invoker check consumes a valid ID token in Authorization and
// hands this process that header changed, so the token a person's own account
// minted — the one a caller of this service presents — is read where it lands:
// credsvc.HeaderPersonIdentity. A call with no readable token in either header
// is still the class's refusal.
func TestTheCallerTokenComesFromThePersonHeader(t *testing.T) {
	f := member(t)
	tok := f.token(t, testOwnerEmail, testOwnerSub)

	r := httptest.NewRequest(http.MethodPost, CreatePath, strings.NewReader(createBody(ScopeUser, "api-key", testValue)))
	r.Header.Set("Authorization", "Bearer front-end-rewritten")
	r.Header.Set(credsvc.HeaderPersonIdentity, tok)
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("a person's token in %s beside a rewritten bearer: got %d %s", credsvc.HeaderPersonIdentity, w.Code, w.Body.String())
	}
	if want := []string{secretname.UserPrefix + testOwnerSub + "_api-key"}; !slices.Equal(f.store.created, want) {
		t.Fatalf("the created containers are %v, want %v", f.store.created, want)
	}

	w = f.post(t, CreatePath, "", createBody(ScopeUser, "api-key", testValue))
	if w.Code != http.StatusForbidden || errorType(t, w) != ErrorUnverifiedLauncher {
		t.Fatalf("a call with no readable token: got %d %s, want 403 %s", w.Code, errorType(t, w), ErrorUnverifiedLauncher)
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

// workflowBody is a create or a version that asks for the workflow-secret
// marker: the same document with the request's own member set (F18, G2).
func workflowBody(scope Scope, name, value string) string {
	return `{"scope":"` + string(scope) + `","name":"` + name + `","value":"` + b64(value) + `","workflow_secret":true}`
}

// TestIntakeAcceptsWorkflowSecretName (U2): the intake admits the names a
// workflow declares, and it admits them because it never filters names by the
// class they are stored for — the grammar is the whole of what a name is put
// to, and the marker is what says a container holds a workflow's own secret.
// The names a workflow declares are ordinary credential names, so nothing here
// had to be widened for them; what the test holds is that the class the marker
// names is not a namespace, which is the reading a "workflow names only" filter
// would give the credentials service's per-secret read and the workflow's own
// `secrets:` list.
func TestIntakeAcceptsWorkflowSecretName(t *testing.T) {
	for _, name := range []string{"npm-token", "deploy-key", "db-password-2", "openrouter-api-key"} {
		f := member(t)
		w := f.post(t, CreatePath, f.token(t, testAdminEmail, testAdminSub), workflowBody(ScopeShared, name, testValue))
		if w.Code != http.StatusOK {
			t.Fatalf("%s with the marker: got %d %s, want 200", name, w.Code, w.Body.String())
		}
		id := secretname.ProjectPrefix + name
		if !slices.Equal(f.store.created, []string{id}) {
			t.Fatalf("%s made the containers %v, want %v", name, f.store.created, []string{id})
		}
		if line := audit(t, f); line["op"] != OpCreate || line["id"] != id || line["scope"] != string(ScopeShared) {
			t.Fatalf("%s: the line is %v, want the create of %s under the shared scope", name, line, id)
		}
	}
	// The marker belongs to the project's prefix and to nothing else: a
	// per-user call asking for it is refused by the request, before the value
	// is decoded, because a person's own container has no workflow to be
	// shared with (F18, R3).
	f := member(t)
	w := f.post(t, CreatePath, f.token(t, testOwnerEmail, testOwnerSub), workflowBody(ScopeUser, "npm-token", testValue))
	if w.Code != http.StatusBadRequest || errorType(t, w) != ErrorRequestRefused {
		t.Fatalf("a per-user call with the marker: got %d %s, want 400 %s", w.Code, errorType(t, w), ErrorRequestRefused)
	}
	if len(f.store.created) != 0 || len(f.store.values) != 0 {
		t.Fatalf("a refused call made %v and wrote %v", f.store.created, f.store.values)
	}
}

// TestIntakeMarksWorkflowSecret (F18, G2): the container a workflow's own
// secret is stored in carries the marker, and a project-shared credential's
// does not. The marker is written at create and read from the container after
// that — no later call can set or clear it, since neither this service nor the
// credentials service holds secrets.update — so a rotated workflow secret
// keeps its class, and the two classes cannot converge on one container.
func TestIntakeMarksWorkflowSecret(t *testing.T) {
	f := member(t)
	tok := f.token(t, testAdminEmail, testAdminSub)
	id := secretname.ProjectPrefix + "npm-token"
	if w := f.post(t, CreatePath, tok, workflowBody(ScopeShared, "npm-token", testValue)); w.Code != http.StatusOK {
		t.Fatalf("the workflow secret's create: got %d %s", w.Code, w.Body.String())
	}
	if got := f.store.labels[id]; got[LabelWorkflowSecret] != WorkflowSecretMarker || got[LabelScope] != string(ScopeShared) {
		t.Fatalf("the container's labels are %v, want the shared scope and the workflow-secret marker", got)
	}
	// A second store of the same name is a rotation into the marked container:
	// the class check passes it, and the marker is still what the container
	// carries afterwards.
	if w := f.post(t, VersionPath, tok, workflowBody(ScopeShared, "npm-token", testValue)); w.Code != http.StatusOK {
		t.Fatalf("the rotation into the marked container: got %d %s", w.Code, w.Body.String())
	}
	if got := f.store.labels[id][LabelWorkflowSecret]; got != WorkflowSecretMarker {
		t.Fatalf("the marker after a rotation is %q, want %q", got, WorkflowSecretMarker)
	}
	if got := f.store.values[id]; len(got) != 2 {
		t.Fatalf("the container holds %v, want the two versions the two calls wrote", got)
	}
	// The other class's container is unmarked, and it is the state the two
	// refusals below are about.
	plain := secretname.ProjectPrefix + "openrouter-key"
	if w := f.post(t, CreatePath, tok, createBody(ScopeShared, "openrouter-key", testValue)); w.Code != http.StatusOK {
		t.Fatalf("the project-shared credential's create: got %d %s", w.Code, w.Body.String())
	}
	if got := f.store.labels[plain]; len(got) == 0 || got[LabelWorkflowSecret] != "" {
		t.Fatalf("a project-shared container's labels are %v, want no workflow-secret marker", got)
	}
	// A marked container is removable by the name its own class spells: a
	// delete is not a write into it, and removing it is the one way back from
	// a value stored under a name that was meant for something else.
	if w := f.post(t, DeletePath, tok, `{"scope":"shared","name":"npm-token"}`); w.Code != http.StatusOK {
		t.Fatalf("removing a marked container: got %d %s", w.Code, w.Body.String())
	}
	if _, ok := f.store.containers[id]; ok {
		t.Fatal("the marked container was not removed")
	}
}

// TestIntakeRefusesUnmarkedExistingContainer (F18, G2): a workflow's value is
// refused over a container a project-shared credential already holds, and the
// refusal names the container's class and what to do about it. The container
// is one this intake made — the shape check passes it — and what refuses it is
// the marker's absence, which is the only witness of the class a container was
// made for: without the refusal the workflow's jobs would mount the credential
// that is in it, and the value stored here would be read by every repository
// the shared name reaches.
func TestIntakeRefusesUnmarkedExistingContainer(t *testing.T) {
	f := member(t)
	tok := f.token(t, testAdminEmail, testAdminSub)
	id := secretname.ProjectPrefix + "npm-token"
	f.store.containers[id] = Secret{
		ID:          id,
		Annotations: map[string]string{AnnotationCreatedBy: testAdminEmail},
		Labels:      map[string]string{LabelScope: string(ScopeShared)},
	}
	for _, route := range []struct{ path, body string }{
		{CreatePath, workflowBody(ScopeShared, "npm-token", testValue)},
		{VersionPath, workflowBody(ScopeShared, "npm-token", testValue)},
	} {
		w := f.post(t, route.path, tok, route.body)
		if w.Code != http.StatusConflict || errorType(t, w) != ErrorForeignContainer {
			t.Fatalf("%s of a workflow secret over an unmarked container: got %d %s, want 409 %s", route.path, w.Code, errorType(t, w), ErrorForeignContainer)
		}
		msg := errorMessage(t, w)
		for _, want := range []string{LabelWorkflowSecret, "fugaro secrets shared rm npm-token"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal over an unmarked container does not name %q: %q", want, msg)
			}
		}
	}
	if len(f.store.values) != 0 {
		t.Fatalf("a refused call wrote %v", f.store.values)
	}
	if len(f.store.deleted) != 0 {
		t.Fatalf("a refused call removed %v", f.store.deleted)
	}

	// The same class check leaves the shape check where it was: a container
	// carrying the marker AND something the intake never writes is refused for
	// the shape, and refused for it first — the marker is a fact about the
	// class, and the shape is the fact that makes a container another writer's
	// (R12, F35).
	f = member(t)
	marked := secretname.ProjectPrefix + "npm-token"
	f.store.containers[marked] = Secret{
		ID:          marked,
		Annotations: map[string]string{AnnotationCreatedBy: testAdminEmail},
		Labels:      map[string]string{LabelScope: string(ScopeShared), LabelWorkflowSecret: WorkflowSecretMarker},
		Shape:       Shape{TTL: "86400s"},
	}
	w := f.post(t, CreatePath, tok, workflowBody(ScopeShared, "npm-token", testValue))
	if w.Code != http.StatusConflict || errorType(t, w) != ErrorForeignContainer {
		t.Fatalf("a marked container with a TTL: got %d %s, want 409 %s", w.Code, errorType(t, w), ErrorForeignContainer)
	}
	if msg := errorMessage(t, w); !strings.Contains(msg, "which the intake never writes") || !strings.Contains(msg, "fugaro secrets shared rm <name>") {
		t.Errorf("the refusal of a marked container with a TTL does not name the shape and the fix: %q", msg)
	}
	if len(f.store.values) != 0 {
		t.Fatalf("a refused call wrote %v", f.store.values)
	}
}

// TestSharedSetRefusesAMarkedContainer (G2): the project's shared prefix
// refuses a container a workflow's own secret holds, so the two classes never
// share one in either direction. The request here is the exact one `fugaro
// secrets shared set NAME` sends — the shared scope, no marker — and the
// refusal names the class the container is and the way back for a credential
// stored under a name a workflow declares.
func TestSharedSetRefusesAMarkedContainer(t *testing.T) {
	f := member(t)
	tok := f.token(t, testAdminEmail, testAdminSub)
	id := secretname.ProjectPrefix + "npm-token"
	// The container a workflow's own store writes.
	if w := f.post(t, CreatePath, tok, workflowBody(ScopeShared, "npm-token", testValue)); w.Code != http.StatusOK {
		t.Fatalf("the workflow secret's create: got %d %s", w.Code, w.Body.String())
	}
	for _, route := range []struct{ path, body string }{
		{CreatePath, createBody(ScopeShared, "npm-token", testValue)},
		{VersionPath, createBody(ScopeShared, "npm-token", testValue)},
	} {
		w := f.post(t, route.path, tok, route.body)
		if w.Code != http.StatusConflict || errorType(t, w) != ErrorForeignContainer {
			t.Fatalf("%s of a project-shared credential over a marked container: got %d %s, want 409 %s", route.path, w.Code, errorType(t, w), ErrorForeignContainer)
		}
		msg := errorMessage(t, w)
		for _, want := range []string{LabelWorkflowSecret, WorkflowSecretMarker, "fugaro secrets shared rm npm-token"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal over a marked container does not name %q: %q", want, msg)
			}
		}
	}
	if got := f.store.values[id]; len(got) != 1 {
		t.Fatalf("the workflow's container holds %v, want only the one version its own store wrote", got)
	}
	if _, ok := f.store.containers[id]; !ok {
		t.Fatal("a refused call removed the marked container")
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
			f := newFixtureLogging(t, []string{testOwnerEmail}, fakeAdmins{}, brokenHandler{}, &bytes.Buffer{})
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

// rosterPerson is the class a workspace's own project gives its intake (WC3,
// R5): the installation's launchers AND the people of the workspace's roster as
// ONE list, which is what the module composes into the class this process reads
// (intake.tf's local.intake_accounts, one expression with the invoker grant).
// The person on the roster is deliberately NOT a launcher of the project (F10:
// the launchers' own list is the workspace project's credentials service
// invoker set too, so `fugaro update` writes their address into the intake's
// second list and not into that one).
//
// The admin is on the class as well, because admin-ness decides nothing to a
// caller the class already refused: the derived list is asked only after the
// caller is admitted (internal/intake's route order), so an admin of the
// workspace project reaches the shared prefix by being on the installation's
// class like everybody else, and their being on the list is the second
// question.
func rosterPerson(t *testing.T) *fixture {
	t.Helper()
	return newFixture(t, []string{testOwnerEmail, testAdminEmail, testOtherEmail}, []access.Admin{{Email: testAdminEmail, Sub: testAdminSub}})
}

// TestAnInstallationsIntakeAdmitsItsLaunchersOnly: the class the intake is
// built with is the installation's own, and it admits exactly the people on it
// — a launcher, and a person the workspace's roster put there (WC3, R5) — and
// nobody else. The second list widens WHO may write and nothing about what they
// may write: both classes reach the same routes on the same terms, which is why
// the one class list is the whole of the admission and there is no second
// question per caller.
func TestAnInstallationsIntakeAdmitsItsLaunchersOnly(t *testing.T) {
	f := rosterPerson(t)
	for _, who := range []struct{ what, email, sub string }{
		{"a launcher of the installation", testOwnerEmail, testOwnerSub},
		{"a person of the workspace's roster", testOtherEmail, testOtherSub},
	} {
		w := f.post(t, CreatePath, f.token(t, who.email, who.sub), createBody(ScopeUser, "claude-oauth-token", testValue))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: create got %d %s", who.what, w.Code, w.Body.String())
		}
	}
	want := []string{
		secretname.UserPrefix + testOwnerSub + "_claude-oauth-token",
		secretname.UserPrefix + testOtherSub + "_claude-oauth-token",
	}
	if !slices.Equal(f.store.created, want) {
		t.Fatalf("the created containers are %v, want %v: each caller writes under their own account id", f.store.created, want)
	}

	// A caller in neither list is refused before any write, however plausible
	// their token: the class is the whole of who this installation admits.
	stranger := "stranger@example.com"
	w := f.post(t, CreatePath, idToken(t, stranger, testAdminSub), createBody(ScopeUser, "claude-oauth-token", testValue))
	if w.Code != http.StatusForbidden || errorType(t, w) != ErrorUnverifiedLauncher {
		t.Fatalf("a caller on neither list: got %d %s, want 403 %s", w.Code, errorType(t, w), ErrorUnverifiedLauncher)
	}
	if len(f.store.created) != len(want) {
		t.Fatalf("a refused caller made the containers %v, want %v", f.store.created, want)
	}
}

// TestARosterPersonWritesOnlyUnderTheirOwnId: a person the workspace's roster
// admitted writes under the account id THEIR OWN token carries and nowhere else
// — one container per person and name in the workspace project's intake
// (D3143), and their own `sub` is what names it (R3, R10).
func TestARosterPersonWritesOnlyUnderTheirOwnId(t *testing.T) {
	f := rosterPerson(t)
	tok := f.token(t, testOtherEmail, testOtherSub)

	// The body is the one every caller sends, and a body that tries to name
	// the owner at all is refused by the decoder rather than read.
	for _, body := range []string{
		`{"scope":"user","name":"claude-oauth-token","value":"` + b64(testValue) + `","sub":"` + testOwnerSub + `"}`,
		`{"scope":"user","name":"claude-oauth-token","value":"` + b64(testValue) + `","owner":"` + testOwnerEmail + `"}`,
	} {
		if w := f.post(t, CreatePath, tok, body); w.Code != http.StatusBadRequest || errorType(t, w) != ErrorRequestRefused {
			t.Fatalf("a body naming the owner: got %d %s, want 400 %s", w.Code, errorType(t, w), ErrorRequestRefused)
		}
	}

	if w := f.post(t, CreatePath, tok, createBody(ScopeUser, "claude-oauth-token", testValue)); w.Code != http.StatusOK {
		t.Fatalf("the roster person's create: got %d %s", w.Code, w.Body.String())
	}
	want := secretname.UserPrefix + testOtherSub + "_claude-oauth-token"
	if !slices.Equal(f.store.created, []string{want}) {
		t.Fatalf("the created containers are %v, want %v: the prefix is the caller's own account id and never a body's", f.store.created, []string{want})
	}
	got := f.store.containers[want].Annotations
	for k, v := range map[string]string{
		AnnotationOwnerSub:   testOtherSub,
		AnnotationOwnerEmail: testOtherEmail,
		AnnotationCreatedBy:  testOtherEmail,
	} {
		if got[k] != v {
			t.Fatalf("the container's %s is %q, want %q", k, got[k], v)
		}
	}

	// And a list answers them their own containers and no other caller's: the
	// launcher's is there for the store and not for them.
	their := f.store.containers[want]
	if w := f.post(t, CreatePath, f.token(t, testOwnerEmail, testOwnerSub), createBody(ScopeUser, "claude-oauth-token", testValue)); w.Code != http.StatusOK {
		t.Fatalf("the launcher's create: got %d %s", w.Code, w.Body.String())
	}
	if their.ID != want {
		t.Fatalf("the store's own container moved: %q", their.ID)
	}
	w := f.post(t, ListPath, tok, `{"scope":"user"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the roster person's list: got %d %s", w.Code, w.Body.String())
	}
	var answer ListAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the list answer: %v (%s)", err, w.Body.String())
	}
	if len(answer.Secrets) != 1 || answer.Secrets[0].ID != want {
		t.Fatalf("the list answer is %+v, want only %s", answer.Secrets, want)
	}
	if strings.Contains(w.Body.String(), testOwnerSub) {
		t.Fatalf("the answer carries another caller's account id: %s", w.Body.String())
	}
}

// TestARosterPersonIsRefusedTheProjectPrefix: the shared prefix stays the
// derived admin list's (D1935, D1940). A person the workspace's roster admitted
// is a person of the `fugaro_u_` half under their own id and of NOTHING ELSE of
// that installation (WC3, R5): they are not on the admin list, so every route
// that reaches `fugaro_p_` refuses them and nothing of theirs lands there.
func TestARosterPersonIsRefusedTheProjectPrefix(t *testing.T) {
	f := rosterPerson(t)
	tok := f.token(t, testOtherEmail, testOtherSub)

	for _, c := range []struct{ path, body string }{
		{CreatePath, createBody(ScopeShared, "claude-oauth-token", testValue)},
		{VersionPath, createBody(ScopeShared, "claude-oauth-token", testValue)},
		{ListPath, `{"scope":"shared"}`},
		{DeletePath, `{"scope":"shared","name":"claude-oauth-token"}`},
	} {
		w := f.post(t, c.path, tok, c.body)
		if w.Code != http.StatusForbidden || errorType(t, w) != ErrorNotAdmin {
			t.Fatalf("%s on the shared prefix: got %d %s, want 403 %s", c.path, w.Code, errorType(t, w), ErrorNotAdmin)
		}
	}
	if len(f.store.created) != 0 || len(f.store.deleted) != 0 {
		t.Fatalf("a refused shared call reached the store: created %v, deleted %v", f.store.created, f.store.deleted)
	}

	// The admin on the derived list holds it, which is what makes the refusal
	// the list's and not a route's: the second class admits a person to the
	// same routes a launcher reaches, and the shared prefix is neither's unless
	// the list says so.
	w := f.post(t, CreatePath, f.token(t, testAdminEmail, testAdminSub), createBody(ScopeShared, "claude-oauth-token", testValue))
	if w.Code != http.StatusOK {
		t.Fatalf("the admin's shared create: got %d %s", w.Code, w.Body.String())
	}
	if want := []string{secretname.ProjectPrefix + "claude-oauth-token"}; !slices.Equal(f.store.created, want) {
		t.Fatalf("the created containers are %v, want %v", f.store.created, want)
	}
}
