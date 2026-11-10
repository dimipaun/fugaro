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
}

func newFakeStore() *fakeStore {
	return &fakeStore{containers: map[string]Secret{}, values: map[string][]string{}, labels: map[string]map[string]string{}}
}

func (f *fakeStore) Describe(_ context.Context, id string) (Secret, bool, error) {
	f.calls = append(f.calls, "describe "+id)
	s, ok := f.containers[id]
	return s, ok, nil
}

func (f *fakeStore) Create(_ context.Context, id string, annotations, labels map[string]string) error {
	f.calls = append(f.calls, "create "+id)
	if _, exists := f.containers[id]; exists {
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
// listing the admins on the derived list.
func newFixture(t *testing.T, launchers []string, admins []access.Admin) *fixture {
	t.Helper()
	callers, err := credsvc.NewLaunchers(credsvc.LauncherConfig{
		Audience: testAudience,
		Accounts: launchers,
		Keys:     testKeys(),
	})
	if err != nil {
		t.Fatalf("building the launcher class: %v", err)
	}
	lines := &bytes.Buffer{}
	audit, err := NewAuditor(slog.NewJSONHandler(lines, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
// annotations another writer set — is refused before any write (R12, F35).
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
		{"another owner's annotation", Secret{ID: id, Annotations: ours(), Shape: Shape{}}},
		{"an annotation the intake does not write", Secret{ID: id, Annotations: map[string]string{
			AnnotationOwnerSub: testOwnerSub, AnnotationOwnerEmail: testOwnerEmail, AnnotationCreatedBy: testOwnerEmail, "fugaro_extra": "x",
		}}},
	} {
		if tc.what == "another owner's annotation" {
			tc.s.Annotations[AnnotationOwnerEmail] = testOtherEmail
		}
		f := member(t)
		f.store.containers[id] = tc.s
		tok := f.token(t, testOwnerEmail, testOwnerSub)
		for _, path := range []string{CreatePath, VersionPath} {
			w := f.post(t, path, tok, createBody(ScopeUser, "api-key", testValue))
			if w.Code != http.StatusConflict || errorType(t, w) != ErrorForeignContainer {
				t.Fatalf("%s on a container with %s: got %d %s, want 409 %s", path, tc.what, w.Code, errorType(t, w), ErrorForeignContainer)
			}
		}
		if len(f.store.values) != 0 {
			t.Fatalf("a container with %s was written into", tc.what)
		}
	}
}

// TestOneAuditLinePerCall: every call the intake answers writes exactly one
// line, and the line names the act, the caller and the status — never a value
// (G6, G13).
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

// TestNothingIsWrittenWithoutAPrefixToWriteItUnder: a caller the class refuses
// before its account id is read writes nothing, and an unknown route is not
// one of the intake's acts.
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
