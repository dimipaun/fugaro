package credsvc

// P4's tests: the refresh store's rotation rules over a real database, and the
// two routes over fakes.
//
// The two halves are deliberately separate. The store's rules (a replayed token
// ends the family, an idle family lapses, nothing at rest is a token) are about
// what one node holds, so they are read off a fake Realtime Database through
// the real store — a fake store would only restate them. The routes' rules are
// about ORDER (nothing is rotated before the signature is checked, the audit
// line precedes the act, a session that ends takes the device node with it), so
// they are read off the handler with the seams it is built from recorded rather
// than with a database in the way.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// --- the store over a fake database -----------------------------------------

// mobileStoreFixture is the real RTDBRefreshStore over a fake database, with
// the fake returned beside it because what a rotation left on the node is a
// fact about the database and not only about the store.
func mobileStoreFixture(t *testing.T) (*gcpfake.RTDB, RTDBRefreshStore) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	return db, RTDBRefreshStore{DB: client}
}

// mobileTestUID is a device uid in the one spelling the tree mints: "m~" and
// the 22 unpadded base64url characters of 16 random bytes (M4).
func mobileTestUID(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		t.Fatalf("drawing a device uid: %v", err)
	}
	return "m~" + base64.RawURLEncoding.EncodeToString(raw)
}

func TestMobileSessionRefreshStoreRotatesWithinOneFamily(t *testing.T) {
	_, store := mobileStoreFixture(t)
	uid := mobileTestUID(t)
	ctx := context.Background()

	grant, err := store.Issue(ctx, "a-project", uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if grant.Family != uid {
		t.Fatalf("Issue named family %q, want the device's own uid %q", grant.Family, uid)
	}
	if !grant.ExpiresAt.After(time.Now()) {
		t.Fatalf("Issue answered an expiry in the past: %v", grant.ExpiresAt)
	}

	owner, ok, err := store.Owner(ctx, "a-project", grant.Token)
	if err != nil || !ok || owner != uid {
		t.Fatalf("Owner(%s) = %q, %v, %v; want %q, true, nil", grant.Token, owner, ok, err, uid)
	}

	next, err := store.Rotate(ctx, "a-project", grant.Token)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if next.Token == grant.Token {
		t.Fatal("Rotate answered the token it was given, so nothing rotated")
	}
	if next.Family != uid {
		t.Fatalf("the successor named family %q, want %q", next.Family, uid)
	}
	if owner, ok, _ := store.Owner(ctx, "a-project", next.Token); !ok || owner != uid {
		t.Fatalf("the successor does not name its family: %q, %v", owner, ok)
	}

	// The presented token is dead the moment its successor is written: it is
	// no longer the family's live one, and the family is not revoked by that
	// alone (a stale token is refused and never admitted, and it is not a way
	// to end a session).
	if _, err := store.Rotate(ctx, "a-project", grant.Token); !errors.Is(err, errMobileRefreshReplayed) {
		t.Fatalf("Rotate of the spent token = %v, want errMobileRefreshReplayed", err)
	}
}

// TestMobileSessionRefreshStoreReplayRevokesTheFamily is P4's red step: the
// second use of a presented token revokes the WHOLE family, so the successor
// the honest phone is holding is dead too — which is what a stolen token's
// replay must cost the thief and the device alike (D2783, F32).
func TestMobileSessionRefreshStoreReplayRevokesTheFamily(t *testing.T) {
	db, store := mobileStoreFixture(t)
	uid := mobileTestUID(t)
	ctx := context.Background()

	first, err := store.Issue(ctx, "a-project", uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, err := store.Rotate(ctx, "a-project", first.Token)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// The replay: the token the first Rotate already spent.
	if _, err := store.Rotate(ctx, "a-project", first.Token); !errors.Is(err, errMobileRefreshReplayed) {
		t.Fatalf("the replayed token = %v, want errMobileRefreshReplayed", err)
	}

	// The family is gone, so the successor the honest phone holds is refused
	// as a family that is no longer there.
	if _, err := store.Rotate(ctx, "a-project", second.Token); !errors.Is(err, errMobileRefreshRevoked) {
		t.Fatalf("the successor after a replay = %v, want errMobileRefreshRevoked", err)
	}
	if got := db.Value("config/admin/mobile/refresh/" + uid); got != nil {
		t.Fatalf("the family node outlived its replay: %#v", got)
	}
}

func TestMobileSessionRefreshStoreIdleLifetimeLapses(t *testing.T) {
	_, store := mobileStoreFixture(t)
	uid := mobileTestUID(t)
	ctx := context.Background()

	now := time.Now()
	store.Now = func() time.Time { return now }
	grant, err := store.Issue(ctx, "a-project", uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	store.Now = func() time.Time { return now.Add(mobilesession.IdleLifetime) }
	if _, err := store.Rotate(ctx, "a-project", grant.Token); !errors.Is(err, errMobileRefreshLapsed) {
		t.Fatalf("Rotate past the idle lifetime = %v, want errMobileRefreshLapsed", err)
	}
	store.Now = func() time.Time { return now }
	if _, err := store.Rotate(ctx, "a-project", grant.Token); !errors.Is(err, errMobileRefreshRevoked) {
		t.Fatalf("Rotate after the lapse = %v, want errMobileRefreshRevoked (the family was removed)", err)
	}
}

func TestMobileSessionRefreshStoreKeepsNoTokenAtRest(t *testing.T) {
	db, store := mobileStoreFixture(t)
	uid := mobileTestUID(t)
	ctx := context.Background()

	grant, err := store.Issue(ctx, "a-project", uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	next, err := store.Rotate(ctx, "a-project", grant.Token)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	raw, err := json.Marshal(db.Value("config/admin/mobile/refresh/" + uid))
	if err != nil {
		t.Fatalf("the family node: %v", err)
	}
	for name, token := range map[string]string{"the issued token": grant.Token, "its successor": next.Token} {
		if bytes.Contains(raw, []byte(token)) {
			t.Fatalf("%s appears in the family node, so the store keeps a presentable credential at rest: %s", name, raw)
		}
	}
	if !bytes.Contains(raw, []byte("live")) || !bytes.Contains(raw, []byte("spent")) {
		t.Fatalf("the family node holds no live and spent hashes: %s", raw)
	}
}

func TestMobileSessionRefreshStoreRefusesAStringItDidNotWrite(t *testing.T) {
	_, store := mobileStoreFixture(t)
	ctx := context.Background()
	for _, token := range []string{
		"",
		"not-base64url!!",
		base64.RawURLEncoding.EncodeToString([]byte("too-short")),
		base64.RawURLEncoding.EncodeToString(append(make([]byte, 32), []byte("not-a-uid")...)),
	} {
		if _, err := store.Rotate(ctx, "a-project", token); !errors.Is(err, errMobileRefreshUnknown) {
			t.Fatalf("Rotate(%q) = %v, want errMobileRefreshUnknown", token, err)
		}
		if _, ok, err := store.Owner(ctx, "a-project", token); err != nil || ok {
			t.Fatalf("Owner(%q) = _, %v, %v; want false, nil", token, ok, err)
		}
	}
}

func TestMobileSessionRefreshStoreRevokeIsIdempotent(t *testing.T) {
	db, store := mobileStoreFixture(t)
	uid := mobileTestUID(t)
	ctx := context.Background()

	if err := store.Revoke(ctx, "a-project", uid); err != nil {
		t.Fatalf("Revoke of a family that was never there: %v", err)
	}
	grant, err := store.Issue(ctx, "a-project", uid)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := store.Revoke(ctx, "a-project", uid); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := store.Revoke(ctx, "a-project", uid); err != nil {
		t.Fatalf("Revoke again: %v", err)
	}
	if got := db.Value("config/admin/mobile/refresh/" + uid); got != nil {
		t.Fatalf("the family node survived its revoke: %#v", got)
	}
	if _, err := store.Rotate(ctx, "a-project", grant.Token); !errors.Is(err, errMobileRefreshRevoked) {
		t.Fatalf("Rotate after Revoke = %v, want errMobileRefreshRevoked", err)
	}
}

// --- the routes over fakes ---------------------------------------------------

// fakeMobileRefresh is C1's RefreshStore plus Owner, over one map per device.
// Its rules are the store's (a second use of a presented token ends the
// family), so a route's ORDER is what a test reads off it and not the rules,
// which the store's own tests above read off a database.
type fakeMobileRefresh struct {
	live     map[string]string // uid -> the token the family considers live
	spent    map[string]bool   // tokens already used
	revoked  []string          // families revoked, in the order they were
	rotates  int
	revokes  int
	issueErr error
	rotateFn func(uid, token string) (mobilesession.RefreshGrant, error)
	revokeFn func(uid string) error
}

func newFakeMobileRefresh() *fakeMobileRefresh {
	return &fakeMobileRefresh{live: map[string]string{}, spent: map[string]bool{}}
}

func (f *fakeMobileRefresh) Issue(_ context.Context, _, uid string) (mobilesession.RefreshGrant, error) {
	if f.issueErr != nil {
		return mobilesession.RefreshGrant{}, f.issueErr
	}
	token := "refresh-" + uid + "-" + time.Now().Format("150405.000000000")
	f.live[uid] = token
	return mobilesession.RefreshGrant{Token: token, Family: uid, ExpiresAt: time.Now().Add(mobilesession.IdleLifetime)}, nil
}

func (f *fakeMobileRefresh) Owner(_ context.Context, _, token string) (string, bool, error) {
	for uid, live := range f.live {
		if live == token {
			return uid, true, nil
		}
	}
	for uid := range f.live {
		if strings.HasPrefix(token, "refresh-"+uid+"-") {
			return uid, true, nil
		}
	}
	return "", false, nil
}

func (f *fakeMobileRefresh) Rotate(_ context.Context, _, token string) (mobilesession.RefreshGrant, error) {
	f.rotates++
	uid, ok, _ := f.Owner(context.Background(), "", token)
	if !ok {
		return mobilesession.RefreshGrant{}, errMobileRefreshUnknown
	}
	if f.rotateFn != nil {
		return f.rotateFn(uid, token)
	}
	if f.spent[token] {
		delete(f.live, uid)
		return mobilesession.RefreshGrant{}, errMobileRefreshReplayed
	}
	if f.live[uid] != token {
		return mobilesession.RefreshGrant{}, errMobileRefreshStale
	}
	f.spent[token] = true
	next := token + "+1"
	f.live[uid] = next
	return mobilesession.RefreshGrant{Token: next, Family: uid, ExpiresAt: time.Now().Add(mobilesession.IdleLifetime)}, nil
}

func (f *fakeMobileRefresh) Revoke(_ context.Context, _, family string) error {
	f.revokes++
	if f.revokeFn != nil {
		return f.revokeFn(family)
	}
	f.revoked = append(f.revoked, family)
	delete(f.live, family)
	return nil
}

// fakeMobileDevices is the device node store, recording what it was asked to
// write so that "the node went with the family" is a fact a test can read.
type fakeMobileDevices struct {
	nodes   map[string]MobileDevice
	deleted []string
	roles   map[string]access.Role
	readErr error
}

func newFakeMobileDevices(nodes map[string]MobileDevice) *fakeMobileDevices {
	return &fakeMobileDevices{nodes: nodes, roles: map[string]access.Role{}}
}

func (f *fakeMobileDevices) Device(_ context.Context, uid string) (MobileDevice, bool, error) {
	if f.readErr != nil {
		return MobileDevice{}, false, f.readErr
	}
	node, ok := f.nodes[uid]
	return node, ok, nil
}

func (f *fakeMobileDevices) SetRole(_ context.Context, uid string, role access.Role) error {
	f.roles[uid] = role
	return nil
}

func (f *fakeMobileDevices) Delete(_ context.Context, uid string) error {
	f.deleted = append(f.deleted, uid)
	delete(f.nodes, uid)
	return nil
}

type fakeMobileRoster struct {
	roles map[string]access.Role
	err   error
	calls int
}

func (f *fakeMobileRoster) RoleAtProject(_ context.Context, address string) (access.Role, error) {
	f.calls++
	if f.err != nil {
		return access.RoleNone, f.err
	}
	return f.roles[address], nil
}

type fakeMobilePolicy struct {
	policy *projectpolicy.Policy
	found  bool
	err    error
}

func (f fakeMobilePolicy) Shared(context.Context) (*projectpolicy.Policy, bool, error) {
	return f.policy, f.found, f.err
}

func (f fakeMobilePolicy) Admissions(context.Context, string) (*projectpolicy.RepoPolicy, bool, error) {
	return nil, false, nil
}

type fakeMobileMinter struct {
	calls  int
	last   mobilesession.MintRequest
	err    error
	expiry time.Time
}

func (f *fakeMobileMinter) Mint(_ context.Context, req mobilesession.MintRequest) (mobilesession.CustomToken, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return mobilesession.CustomToken{}, f.err
	}
	return mobilesession.CustomToken{Token: "custom-" + req.UID, ExpiresAt: f.expiry}, nil
}

// mobileFixture is the routes' whole wiring, with every seam reachable so a
// test can read what a call asked of it.
type mobileFixture struct {
	session  *MobileSession
	refresh  *fakeMobileRefresh
	devices  *fakeMobileDevices
	roster   *fakeMobileRoster
	minter   *fakeMobileMinter
	policy   *fakeMobilePolicy
	audit    *bytes.Buffer
	uid      string
	pub      string
	priv     ed25519.PrivateKey
	live     string
	person   string
	nodeRole string
}

func newMobileFixture(t *testing.T, opts ...func(*mobileFixture)) *mobileFixture {
	t.Helper()
	uid := mobileTestUID(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a device key: %v", err)
	}
	f := &mobileFixture{
		refresh:  newFakeMobileRefresh(),
		uid:      uid,
		pub:      base64.RawURLEncoding.EncodeToString(pub),
		priv:     priv,
		person:   testPersonAccount,
		nodeRole: string(access.RoleAdmin),
	}
	for _, opt := range opts {
		opt(f)
	}
	f.devices = newFakeMobileDevices(map[string]MobileDevice{
		uid: {Email: f.person, Role: f.nodeRole, DevicePublicKey: f.pub},
	})
	f.roster = &fakeMobileRoster{roles: map[string]access.Role{f.person: access.RoleAdmin}}
	f.minter = &fakeMobileMinter{expiry: time.Now().Add(mobilesession.CustomTokenLifetime)}
	a, buf := newAudit(t)
	f.audit = buf

	policy := &projectpolicy.Policy{Mobile: &projectpolicy.Mobile{
		CostlyModes:       []rigor.Rigor{rigor.Pro},
		BiometricOnCostly: boolPtrMobile(true),
	}}
	f.policy = &fakeMobilePolicy{policy: policy, found: true}
	session, err := NewMobileSession(MobileSessionConfig{
		Project: "a-project",
		Refresh: f.refresh,
		Mint:    f.minter,
		Devices: f.devices,
		Roster:  f.roster,
		Policy:  f.policy,
		Audit:   a,
		Log:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewMobileSession: %v", err)
	}
	f.session = session
	grant, err := f.refresh.Issue(context.Background(), "a-project", uid)
	if err != nil {
		t.Fatalf("issuing the fixture's token: %v", err)
	}
	f.live = grant.Token
	return f
}

func boolPtrMobile(v bool) *bool { return &v }

// sign builds the body's signature the way the app does: over
// SignatureMessage(purpose, the token).
func (f *mobileFixture) sign(t *testing.T, purpose mobilesession.SignaturePurpose, token string) string {
	t.Helper()
	msg := mobilesession.SignatureMessage(purpose, token)
	if msg == nil {
		t.Fatalf("SignatureMessage(%q) is nil", purpose)
	}
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.priv, msg))
}

func (f *mobileFixture) post(t *testing.T, route string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	switch route {
	case mobilesession.RouteRefresh:
		f.session.ServeRefresh(rec, req)
	case mobilesession.RouteRevokeSelf:
		f.session.ServeRevokeSelf(rec, req)
	default:
		t.Fatalf("no route %q", route)
	}
	return rec
}

func (f *mobileFixture) refreshRequest(t *testing.T, token string) mobilesession.RefreshRequest {
	t.Helper()
	return mobilesession.RefreshRequest{RefreshToken: token, Signature: f.sign(t, mobilesession.PurposeRefresh, token)}
}

// decodeMobileAnswer reads one answer body into out and returns the recorder.
func decodeMobileAnswer(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("the answer %s is not JSON: %v", rec.Body.String(), err)
	}
}

// mobileAuditLines reads an audit buffer that is allowed to be empty: a mobile
// refusal before the caller has proved possession of its device key owes no
// line at all (the trail starts once a caller is verified), and auditLines
// treats an empty buffer as a failure, which is the right answer everywhere
// else in this package.
func mobileAuditLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	if buf.Len() == 0 {
		return nil
	}
	return auditLines(t, buf)
}

// mobileRefusal reads an answer as the contract's Error and checks the code.
func mobileRefusal(t *testing.T, rec *httptest.ResponseRecorder, status int, code mobilesession.Code) mobilesession.Error {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("the answer is %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	var e mobilesession.Error
	decodeMobileAnswer(t, rec, &e)
	if e.Code != code {
		t.Fatalf("the refusal's code is %q, want %q: %s", e.Code, code, rec.Body.String())
	}
	return e
}

func TestMobileSessionRefreshAnswersASuccessorAndACustomToken(t *testing.T) {
	f := newMobileFixture(t)
	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))

	if rec.Code != http.StatusOK {
		t.Fatalf("the refresh answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("the answer's Cache-Control is %q, want no-store", got)
	}
	var out mobilesession.RefreshResponse
	decodeMobileAnswer(t, rec, &out)
	if out.RefreshToken == f.live || out.RefreshToken == "" {
		t.Fatalf("the answer carried %q as the successor of %q", out.RefreshToken, f.live)
	}
	if out.CustomToken != "custom-"+f.uid {
		t.Fatalf("the custom token is %q, want the minter's answer for %q", out.CustomToken, f.uid)
	}
	if out.ExpiresIn != int(mobilesession.CustomTokenLifetime/time.Second) {
		t.Fatalf("ExpiresIn = %d, want %d", out.ExpiresIn, int(mobilesession.CustomTokenLifetime/time.Second))
	}
	if out.Role != access.RoleAdmin {
		t.Fatalf("Role = %q, want %q", out.Role, access.RoleAdmin)
	}
	if len(out.Policy.CostlyModes) != 1 || out.Policy.CostlyModes[0] != string(rigor.Pro) || !out.Policy.BiometricOnCostly {
		t.Fatalf("the session policy is %+v, want the project's costly modes and biometric switch", out.Policy)
	}
	if f.minter.calls != 1 || f.minter.last.UID != f.uid || f.minter.last.Role != access.RoleAdmin {
		t.Fatalf("the minter was asked %d times, last %+v; want once for the node's own uid", f.minter.calls, f.minter.last)
	}
	if len(f.devices.deleted) != 0 {
		t.Fatalf("a successful refresh deleted %v", f.devices.deleted)
	}
	if len(f.refresh.revoked) != 0 {
		t.Fatalf("a successful refresh revoked %v", f.refresh.revoked)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("a successful refresh wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	if lines[0]["purpose"] != PurposeMobileRefresh || lines[0]["status"] != StatusOK {
		t.Fatalf("the audit line is %v, want a mobile_refresh OK", lines[0])
	}
	if lines[0]["caller"] != f.person {
		t.Fatalf("the audit line's caller is %v, want the device node's address %q", lines[0]["caller"], f.person)
	}
}

func TestMobileSessionRefreshRewritesARoleThatMoved(t *testing.T) {
	f := newMobileFixture(t)
	f.nodeRole = string(access.RoleViewer)
	f.devices.nodes[f.uid] = MobileDevice{Email: f.person, Role: f.nodeRole, DevicePublicKey: f.pub}
	f.roster.roles[f.person] = access.RoleOperator

	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	if rec.Code != http.StatusOK {
		t.Fatalf("the refresh answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out mobilesession.RefreshResponse
	decodeMobileAnswer(t, rec, &out)
	if out.Role != access.RoleOperator {
		t.Fatalf("Role = %q, want the roster's %q", out.Role, access.RoleOperator)
	}
	if got := f.devices.roles[f.uid]; got != access.RoleOperator {
		t.Fatalf("the node's role was rewritten to %q, want %q", got, access.RoleOperator)
	}
}

func TestMobileSessionRefreshKeepsARoleThatDidNotMove(t *testing.T) {
	f := newMobileFixture(t)
	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	if rec.Code != http.StatusOK {
		t.Fatalf("the refresh answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(f.devices.roles) != 0 {
		t.Fatalf("a refresh wrote the node's role though it had not moved: %v", f.devices.roles)
	}
}

// TestMobileSessionRefreshReplayEndsTheSession is P4's red step at the route:
// the second use of a presented token must revoke the family FIRST, and the
// device node must go with it, so no Firebase refresh token the app already
// derived keeps opening the database (F32).
func TestMobileSessionRefreshReplayEndsTheSession(t *testing.T) {
	f := newMobileFixture(t)
	first := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	if first.Code != http.StatusOK {
		t.Fatalf("the first refresh answered %d, want 200: %s", first.Code, first.Body.String())
	}

	// The replay of the token the first refresh spent.
	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	mobileRefusal(t, rec, http.StatusForbidden, mobilesession.CodeDeviceRevoked)

	if len(f.refresh.revoked) != 1 || f.refresh.revoked[0] != f.uid {
		t.Fatalf("the replay revoked %v, want exactly the caller's family %q", f.refresh.revoked, f.uid)
	}
	if len(f.devices.deleted) != 1 || f.devices.deleted[0] != f.uid {
		t.Fatalf("the replay deleted %v, want exactly the caller's device node %q", f.devices.deleted, f.uid)
	}
	if _, still := f.devices.nodes[f.uid]; still {
		t.Fatal("the device node is still there after a replay, so its Firebase refresh token still opens the database")
	}
	if lines := auditLines(t, f.audit); len(lines) != 2 {
		t.Fatalf("the replay wrote %d audit lines for two attempts, want 2 (one per attempt, no more)", len(lines))
	}
}

func TestMobileSessionRefreshNodeGoneRevokesTheFamily(t *testing.T) {
	f := newMobileFixture(t)
	delete(f.devices.nodes, f.uid)

	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	mobileRefusal(t, rec, http.StatusForbidden, mobilesession.CodeDeviceRevoked)
	if len(f.refresh.revoked) != 1 || f.refresh.revoked[0] != f.uid {
		t.Fatalf("a refresh whose node is gone revoked %v, want the family %q", f.refresh.revoked, f.uid)
	}
	if lines := mobileAuditLines(t, f.audit); len(lines) != 0 {
		t.Fatalf("a refusal before the caller proved anything wrote %d audit lines, want 0: %v", len(lines), lines)
	}
	if f.refresh.rotates != 0 {
		t.Fatalf("a refresh with no device node rotated the family %d times", f.refresh.rotates)
	}
}

func TestMobileSessionRefreshUnknownTokenIsRefused(t *testing.T) {
	f := newMobileFixture(t)
	body := mobilesession.RefreshRequest{RefreshToken: "not-a-token-this-project-wrote", Signature: "AAAA"}
	rec := f.post(t, mobilesession.RouteRefresh, body)
	mobileRefusal(t, rec, http.StatusForbidden, mobilesession.CodeDeviceRevoked)
	if f.refresh.rotates != 0 || len(f.devices.deleted) != 0 {
		t.Fatal("an unknown token rotated or deleted something")
	}
	if lines := mobileAuditLines(t, f.audit); len(lines) != 0 {
		t.Fatalf("an unknown token wrote %d audit lines, want 0", len(lines))
	}
}

// TestMobileSessionRefreshBadSignatureLeavesTheFamilyStanding: a caller that
// has not proved possession of the device key must not be able to end the
// session of the device whose token it copied.
func TestMobileSessionRefreshBadSignatureLeavesTheFamilyStanding(t *testing.T) {
	f := newMobileFixture(t)
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a second key: %v", err)
	}
	forged := base64.RawURLEncoding.EncodeToString(ed25519.Sign(otherPriv, mobilesession.SignatureMessage(mobilesession.PurposeRefresh, f.live)))

	rec := f.post(t, mobilesession.RouteRefresh, mobilesession.RefreshRequest{RefreshToken: f.live, Signature: forged})
	mobileRefusal(t, rec, http.StatusForbidden, mobilesession.CodeDeviceRevoked)

	if f.refresh.rotates != 0 {
		t.Fatalf("a forged signature rotated the family %d times", f.refresh.rotates)
	}
	if len(f.refresh.revoked) != 0 || len(f.devices.deleted) != 0 {
		t.Fatalf("a forged signature ended the session: revoked %v, deleted %v", f.refresh.revoked, f.devices.deleted)
	}
	if _, still := f.devices.nodes[f.uid]; !still {
		t.Fatal("a forged signature deleted the device node")
	}
	if lines := mobileAuditLines(t, f.audit); len(lines) != 0 {
		t.Fatalf("a caller that proved nothing wrote %d audit lines, want 0: %v", len(lines), lines)
	}
	// The honest phone still works.
	if rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live)); rec.Code != http.StatusOK {
		t.Fatalf("the honest refresh after a forged one answered %d: %s", rec.Code, rec.Body.String())
	}
}

// TestMobileSessionRefreshSignatureIsOverThisToken: the signature is bound to
// the token it was made for, so one the device signed for its live token cannot
// be replayed over another string.
func TestMobileSessionRefreshSignatureIsOverThisToken(t *testing.T) {
	f := newMobileFixture(t)
	rec := f.post(t, mobilesession.RouteRefresh, mobilesession.RefreshRequest{
		RefreshToken: f.live,
		Signature:    f.sign(t, mobilesession.PurposeRevokeSelf, f.live),
	})
	mobileRefusal(t, rec, http.StatusForbidden, mobilesession.CodeDeviceRevoked)
	if f.refresh.rotates != 0 {
		t.Fatal("a signature made for another purpose rotated the family")
	}
}

// TestMobileSessionRefreshMembershipGoneEndsTheSession: an address with no
// grant left is the end of the session, and the device node goes with it (O14,
// F32).
func TestMobileSessionRefreshMembershipGoneEndsTheSession(t *testing.T) {
	f := newMobileFixture(t)
	f.roster.roles = map[string]access.Role{}

	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	mobileRefusal(t, rec, http.StatusForbidden, mobilesession.CodeDeviceRevoked)

	if f.roster.calls != 1 {
		t.Fatalf("the roster was read %d times, want once", f.roster.calls)
	}
	if len(f.refresh.revoked) != 1 || len(f.devices.deleted) != 1 {
		t.Fatalf("a failed membership re-check revoked %v and deleted %v, want both once", f.refresh.revoked, f.devices.deleted)
	}
	if f.refresh.rotates != 0 || f.minter.calls != 0 {
		t.Fatalf("a refused membership rotated %d times and minted %d tokens, want 0 and 0", f.refresh.rotates, f.minter.calls)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("a refusal after proof wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	if lines[0]["purpose"] != PurposeMobileRefresh {
		t.Fatalf("the audit line's purpose is %v, want %q", lines[0]["purpose"], PurposeMobileRefresh)
	}
}

// TestMobileSessionRefreshFailuresOfOursAnswer503: a failure of the service's
// own is an outage and never a verdict about the caller, so nothing is ended
// and the code is empty — the app refuses a code it does not know rather than
// signing the person out.
func TestMobileSessionRefreshFailuresOfOursAnswer503(t *testing.T) {
	cases := map[string]func(*mobileFixture){
		"the roster cannot be read": func(f *mobileFixture) { f.roster.err = errors.New("the roster is unreadable") },
		"the device node cannot be read": func(f *mobileFixture) {
			f.devices.readErr = errors.New("the database refused the read")
		},
		"the minter refuses": func(f *mobileFixture) { f.minter.err = errors.New("the signer refused") },
		"the rotation fails": func(f *mobileFixture) {
			f.refresh.rotateFn = func(string, string) (mobilesession.RefreshGrant, error) {
				return mobilesession.RefreshGrant{}, errors.New("the database refused the write")
			}
		},
		"the policy is absent": func(f *mobileFixture) { f.policy.found = false },
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			f := newMobileFixture(t)
			break_(f)
			rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
			mobileRefusal(t, rec, StatusUnavailable, "")
			if len(f.devices.deleted) != 0 {
				t.Fatalf("an outage deleted %v", f.devices.deleted)
			}
			if _, still := f.devices.nodes[f.uid]; !still {
				t.Fatal("an outage deleted the device node")
			}
		})
	}
}

// TestMobileSessionRefreshAuditFailureWithholdsTheAct: an issuance this service
// cannot record must not be made (D143), so the token the caller holds stays
// live and the phone retries.
func TestMobileSessionRefreshAuditFailureWithholdsTheAct(t *testing.T) {
	f := newMobileFixture(t)
	failing, err := NewAuditor(&recordHandler{err: errors.New("the log refused the line")}, armed())
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	session, err := NewMobileSession(MobileSessionConfig{
		Project: "a-project", Refresh: f.refresh, Mint: f.minter, Devices: f.devices,
		Roster: f.roster, Policy: f.policy, Audit: failing,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewMobileSession: %v", err)
	}
	f.session = session

	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	mobileRefusal(t, rec, StatusUnavailable, "")
	if f.refresh.rotates != 0 || f.minter.calls != 0 {
		t.Fatalf("an unrecordable act rotated %d times and minted %d tokens, want 0 and 0", f.refresh.rotates, f.minter.calls)
	}
	if len(f.refresh.revoked) != 0 || len(f.devices.deleted) != 0 {
		t.Fatalf("an unrecordable act ended the session: revoked %v, deleted %v", f.refresh.revoked, f.devices.deleted)
	}
}

// TestMobileSessionRefreshBodyIsReadStrictly: an unknown field, a missing half
// and a body past the bound are all refused with an EMPTY code, because the app
// must refuse what it does not know rather than show a screen for it.
func TestMobileSessionRefreshBodyIsReadStrictly(t *testing.T) {
	f := newMobileFixture(t)
	cases := map[string]string{
		"an unknown field":      `{"refresh_token":"x","signature":"y","uid":"m~nope"}`,
		"no token":              `{"signature":"y"}`,
		"no signature":          `{"refresh_token":"x"}`,
		"not an object":         `["refresh_token"]`,
		"a body past the bound": `{"refresh_token":"` + strings.Repeat("a", mobileRequestBytes) + `","signature":"y"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, mobilesession.RouteRefresh, strings.NewReader(body))
			rec := httptest.NewRecorder()
			f.session.ServeRefresh(rec, req)
			e := mobileRefusal(t, rec, http.StatusBadRequest, "")
			if e.Message == "" {
				t.Fatal("the refusal carried no sentence for a person")
			}
			if strings.Contains(e.Message, body) {
				t.Fatalf("the refusal's sentence carries the body: %q", e.Message)
			}
		})
	}
	if f.refresh.rotates != 0 {
		t.Fatalf("a malformed body rotated the family %d times", f.refresh.rotates)
	}
}

// TestMobileSessionRefreshAnswerCarriesNoFirebaseRefreshToken: the answer is
// the contract's five fields and nothing else, and the token it echoes is the
// family's successor — a Fugaro token the service minted, never a Firebase
// refresh token (D2783).
func TestMobileSessionRefreshAnswerCarriesNoFirebaseRefreshToken(t *testing.T) {
	f := newMobileFixture(t)
	rec := f.post(t, mobilesession.RouteRefresh, f.refreshRequest(t, f.live))
	var raw map[string]any
	decodeMobileAnswer(t, rec, &raw)
	want := map[string]bool{"refresh_token": true, "custom_token": true, "expires_in": true, "role": true, "policy": true}
	for key := range raw {
		if !want[key] {
			t.Fatalf("the answer carries a field the contract does not: %q", key)
		}
	}
	for key := range want {
		if _, ok := raw[key]; !ok {
			t.Fatalf("the answer carries no %q", key)
		}
	}
}

// TestMobileSessionRevokeSelfEndsTheSession: the phone's own sign-out ends the
// family and deletes the device node, and it needs the same proof the refresh
// does.
func TestMobileSessionRevokeSelfEndsTheSession(t *testing.T) {
	f := newMobileFixture(t)
	rec := f.post(t, mobilesession.RouteRevokeSelf, mobilesession.RevokeSelfRequest{
		RefreshToken: f.live,
		Signature:    f.sign(t, mobilesession.PurposeRevokeSelf, f.live),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke-self answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out mobilesession.RevokeSelfResponse
	decodeMobileAnswer(t, rec, &out)
	if len(f.refresh.revoked) != 1 || f.refresh.revoked[0] != f.uid {
		t.Fatalf("revoke-self revoked %v, want the caller's family %q", f.refresh.revoked, f.uid)
	}
	if len(f.devices.deleted) != 1 || f.devices.deleted[0] != f.uid {
		t.Fatalf("revoke-self deleted %v, want the caller's node %q", f.devices.deleted, f.uid)
	}
	if f.roster.calls != 0 {
		t.Fatalf("revoke-self read the roster %d times, want 0: a person off the roster must still be able to sign out", f.roster.calls)
	}
	if f.minter.calls != 0 {
		t.Fatalf("revoke-self minted %d tokens, want 0", f.minter.calls)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("revoke-self wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	if lines[0]["purpose"] != PurposeMobileRevoke || lines[0]["status"] != StatusOK {
		t.Fatalf("the audit line is %v, want a mobile_revoke OK", lines[0])
	}
}

func TestMobileSessionRevokeSelfNeedsTheSignature(t *testing.T) {
	f := newMobileFixture(t)
	rec := f.post(t, mobilesession.RouteRevokeSelf, mobilesession.RevokeSelfRequest{
		RefreshToken: f.live,
		Signature:    base64.RawURLEncoding.EncodeToString(make([]byte, mobilesession.DeviceSignatureSize)),
	})
	mobileRefusal(t, rec, http.StatusForbidden, mobilesession.CodeDeviceRevoked)
	if len(f.refresh.revoked) != 0 || len(f.devices.deleted) != 0 {
		t.Fatalf("an unproved sign-out ended the session: revoked %v, deleted %v", f.refresh.revoked, f.devices.deleted)
	}
	if lines := mobileAuditLines(t, f.audit); len(lines) != 0 {
		t.Fatalf("an unproved sign-out wrote %d audit lines, want 0", len(lines))
	}
}

// TestMobileSessionRevokeSelfSignsOutAMemberOffTheRoster: a person whose grant
// was revoked must still be able to end the session on their own phone.
func TestMobileSessionRevokeSelfSignsOutAMemberOffTheRoster(t *testing.T) {
	f := newMobileFixture(t)
	f.roster.roles = map[string]access.Role{}
	f.roster.err = errors.New("the roster must not be read at all")
	rec := f.post(t, mobilesession.RouteRevokeSelf, mobilesession.RevokeSelfRequest{
		RefreshToken: f.live,
		Signature:    f.sign(t, mobilesession.PurposeRevokeSelf, f.live),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke-self answered %d for a person off the roster, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(f.devices.deleted) != 1 {
		t.Fatalf("revoke-self deleted %v, want the caller's node", f.devices.deleted)
	}
}

// TestMobileSessionConstructionRefusesAMissingSeam: a session that cannot
// re-check a membership, mint a token or record its act is not one this service
// may answer.
func TestMobileSessionConstructionRefusesAMissingSeam(t *testing.T) {
	f := newMobileFixture(t)
	full := MobileSessionConfig{
		Project: "a-project", Refresh: f.refresh, Mint: f.minter, Devices: f.devices,
		Roster: f.roster, Policy: fakeMobilePolicy{found: true}, Audit: mustAuditor(t),
	}
	if _, err := NewMobileSession(full); err != nil {
		t.Fatalf("NewMobileSession on the whole config: %v", err)
	}
	cases := map[string]func(*MobileSessionConfig){
		"no project": func(c *MobileSessionConfig) { c.Project = "" },
		"no refresh": func(c *MobileSessionConfig) { c.Refresh = nil },
		"no minter":  func(c *MobileSessionConfig) { c.Mint = nil },
		"no devices": func(c *MobileSessionConfig) { c.Devices = nil },
		"no roster":  func(c *MobileSessionConfig) { c.Roster = nil },
		"no policy":  func(c *MobileSessionConfig) { c.Policy = nil },
		"no auditor": func(c *MobileSessionConfig) { c.Audit = nil },
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := full
			break_(&cfg)
			if _, err := NewMobileSession(cfg); err == nil {
				t.Fatalf("NewMobileSession accepted a config with %s", name)
			}
		})
	}
}

func mustAuditor(t *testing.T) *Auditor {
	t.Helper()
	a, _ := newAudit(t)
	return a
}

// TestMobileSessionRTDBRosterResolvesTheRoster is the roster seam over the real
// reader: a stored grant places the address, an implicit admin places it without
// one, and an address with neither holds RoleNone (O14).
func TestMobileSessionRTDBRosterResolvesTheRoster(t *testing.T) {
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	db.Set(access.PathNode, map[string]any{
		"grants": []map[string]any{
			{"user": "user:" + testPersonAccount, "role": string(access.RoleOperator), "scope": "project"},
		},
	})
	roster := RTDBRoster{Records: RTDBAccess{DB: client}, Implicit: []string{"user:owner@example.invalid"}}
	ctx := context.Background()

	if role, err := roster.RoleAtProject(ctx, testPersonAccount); err != nil || role != access.RoleOperator {
		t.Fatalf("RoleAtProject(%s) = %q, %v; want %q", testPersonAccount, role, err, access.RoleOperator)
	}
	if role, err := roster.RoleAtProject(ctx, "owner@example.invalid"); err != nil || role == access.RoleNone {
		t.Fatalf("RoleAtProject of an implicit admin = %q, %v; want a role", role, err)
	}
	if role, err := roster.RoleAtProject(ctx, "nobody@example.invalid"); err != nil || role != access.RoleNone {
		t.Fatalf("RoleAtProject of a stranger = %q, %v; want RoleNone", role, err)
	}
	if _, err := roster.RoleAtProject(ctx, "not-an-address"); err == nil {
		t.Fatal("RoleAtProject accepted a node address that is not one a grant can name")
	}
}

// TestMobileSessionRTDBDevicesReadsAndWritesTheNode: the store reads the node a
// pairing writes and rewrites or removes exactly one child of it.
func TestMobileSessionRTDBDevicesReadsAndWritesTheNode(t *testing.T) {
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	uid := mobileTestUID(t)
	db.Set("config/admin/devices/"+uid, map[string]any{
		"email":             testPersonAccount,
		"role":              string(access.RoleAdmin),
		"device_public_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"name":              "the phone",
	})
	devices := RTDBDevices{DB: client}
	ctx := context.Background()

	dev, found, err := devices.Device(ctx, uid)
	if err != nil || !found {
		t.Fatalf("Device(%s) = _, %v, %v; want the node", uid, found, err)
	}
	if dev.Email != testPersonAccount || dev.Role != string(access.RoleAdmin) || dev.DevicePublicKey == "" {
		t.Fatalf("Device read %+v", dev)
	}
	if _, found, err := devices.Device(ctx, mobileTestUID(t)); err != nil || found {
		t.Fatalf("Device of an unpaired uid = _, %v, %v; want not found", found, err)
	}

	if err := devices.SetRole(ctx, uid, access.RoleViewer); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if got := db.Value("config/admin/devices/" + uid + "/role"); got != string(access.RoleViewer) {
		t.Fatalf("the node's role is %v, want viewer", got)
	}
	if got := db.Value("config/admin/devices/" + uid + "/name"); got != "the phone" {
		t.Fatalf("SetRole rewrote a field that is not the role: name = %v", got)
	}

	if err := devices.Delete(ctx, uid); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := db.Value("config/admin/devices/" + uid); got != nil {
		t.Fatalf("the node survived Delete: %#v", got)
	}
	if err := devices.SetRole(ctx, "not-a-uid", access.RoleViewer); err == nil {
		t.Fatal("SetRole accepted a uid this tree did not mint")
	}
}

// TestMobileSessionPolicyCarriesTheProjectsSwitches: the session policy is the
// published policy's mobile members, and the compaction threshold is left 0
// because the member behind it is Z2's.
func TestMobileSessionPolicyCarriesTheProjectsSwitches(t *testing.T) {
	p := &projectpolicy.Policy{Mobile: &projectpolicy.Mobile{
		CostlyModes:       []rigor.Rigor{rigor.Critical, rigor.Hotfix},
		BiometricOnCostly: boolPtrMobile(true),
	}}
	got := mobileSessionPolicy(p)
	if len(got.CostlyModes) != 2 || got.CostlyModes[0] != string(rigor.Critical) || got.CostlyModes[1] != string(rigor.Hotfix) {
		t.Fatalf("CostlyModes = %v, want the project's list", got.CostlyModes)
	}
	if !got.BiometricOnCostly {
		t.Fatal("BiometricOnCostly is false though the project wrote it")
	}
	if got.CompactThresholdPercent != 0 {
		t.Fatalf("CompactThresholdPercent = %d, want 0 until Z2 defines the member", got.CompactThresholdPercent)
	}
}
