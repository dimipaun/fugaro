package credsvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The mobile restore tests (K2). They are one fixture and one double per side
// the route calls, because the route's whole job is the ORDER of those calls
// and what it does when one of them answers badly: a real store would test the
// store.

// ---------------------------------------------------------------------------
// The doubles
// ---------------------------------------------------------------------------

// fakeVerifier is C1's Verifier: it returns the claims it was configured with
// and records every binding it was handed, so a test can read what the route
// required the assertion to be bound to.
type fakeVerifier struct {
	claims mobilesession.Claims
	err    error

	assertions []string
	bindings   []mobilesession.Binding
}

func (f *fakeVerifier) Verify(_ context.Context, assertion string, want mobilesession.Binding) (mobilesession.Claims, error) {
	f.assertions = append(f.assertions, assertion)
	f.bindings = append(f.bindings, want)
	if f.err != nil {
		return mobilesession.Claims{}, f.err
	}
	return f.claims, nil
}

// fakeMembership is C1's Policy: the verdict and the crash-report switch it was
// configured with, and the project and claims it was asked about.
type fakeMembership struct {
	verdict mobilesession.Verdict
	err     error

	crashReports    bool
	crashReportsErr error

	projects []string
	claims   []mobilesession.Claims
	crashes  []string
}

func (f *fakeMembership) Allow(_ context.Context, project string, claims mobilesession.Claims) (mobilesession.Verdict, error) {
	f.projects = append(f.projects, project)
	f.claims = append(f.claims, claims)
	if f.err != nil {
		return mobilesession.Verdict{}, f.err
	}
	return f.verdict, nil
}

func (f *fakeMembership) CrashReports(_ context.Context, project string) (bool, error) {
	f.crashes = append(f.crashes, project)
	if f.crashReportsErr != nil {
		return false, f.crashReportsErr
	}
	return f.crashReports, nil
}

// fakeClaimStore is C1's ClaimStore: a map, which is the one thing the real
// store is not — two instances of the public service are two maps — and enough
// for a single process's tests. It records what was claimed under which
// namespace, so the challenge's single use and its namespace are both readable.
type fakeClaimStore struct {
	spent map[string]bool
	err   error

	namespaces []string
	keys       []string
	ttls       []time.Duration
}

func (f *fakeClaimStore) Claim(_ context.Context, namespace, key string, ttl time.Duration) (bool, error) {
	f.namespaces = append(f.namespaces, namespace)
	f.keys = append(f.keys, key)
	f.ttls = append(f.ttls, ttl)
	if f.err != nil {
		return false, f.err
	}
	if f.spent == nil {
		f.spent = map[string]bool{}
	}
	if f.spent[namespace+"\x00"+key] {
		return false, nil
	}
	f.spent[namespace+"\x00"+key] = true
	return true, nil
}

// fakeRefreshStore is C1's RefreshStore: it issues one family per uid it is
// asked for and records every issue and every revoke, which is what the
// cleanup tests read.
type fakeRefreshStore struct {
	issueErr error
	grant    mobilesession.RefreshGrant

	issued  []string
	revoked []string
}

func (f *fakeRefreshStore) Issue(_ context.Context, project, uid string) (mobilesession.RefreshGrant, error) {
	f.issued = append(f.issued, uid)
	if f.issueErr != nil {
		return mobilesession.RefreshGrant{}, f.issueErr
	}
	if f.grant.Token == "" {
		return mobilesession.RefreshGrant{Token: "refresh-" + uid, Family: "family-" + uid, ExpiresAt: time.Unix(0, 0)}, nil
	}
	return f.grant, nil
}

func (f *fakeRefreshStore) Rotate(context.Context, string, string) (mobilesession.RefreshGrant, error) {
	return mobilesession.RefreshGrant{}, nil
}

func (f *fakeRefreshStore) Revoke(_ context.Context, _, family string) error {
	f.revoked = append(f.revoked, family)
	return nil
}

// fakeMinter is C1's Minter: it mints the token it was configured with and
// records every request, so a test can read the uid and the role the route
// asked for.
type fakeMinter struct {
	token mobilesession.CustomToken
	err   error

	reqs []mobilesession.MintRequest
}

func (f *fakeMinter) Mint(_ context.Context, req mobilesession.MintRequest) (mobilesession.CustomToken, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return mobilesession.CustomToken{}, f.err
	}
	if f.token.Token == "" {
		return mobilesession.CustomToken{Token: "custom-" + req.UID, ExpiresAt: time.Unix(0, 0)}, nil
	}
	return f.token, nil
}

// fakeMemberStore is C1's MemberStore: the record the pairing writes and this
// route only reads. It reports the record it was configured with and, when told
// to, that there is none — an address that has never paired — and it can fail
// the read, which is the service's own failure and not a refusal.
type fakeMemberStore struct {
	record mobilesession.Member
	found  bool
	err    error

	emails []string
}

func (f *fakeMemberStore) Member(_ context.Context, _, email string) (mobilesession.Member, bool, error) {
	f.emails = append(f.emails, email)
	if f.err != nil {
		return mobilesession.Member{}, false, f.err
	}
	return f.record, f.found, nil
}

func (f *fakeMemberStore) Record(context.Context, string, mobilesession.Member) error { return nil }

func (f *fakeMemberStore) Remove(context.Context, string, string) error { return nil }

// fakeDeviceStore is the pairing route's own device-node seam, which this route
// writes through as well (MobileDeviceNodes, P3): it answers the consumer key it
// was configured with and records every node it was asked to write and every uid
// it was asked to retire, in order, because what the cleanup tests read is that a
// failed restore left neither.
type fakeDeviceStore struct {
	consumer    string
	consumerErr error
	writeErr    error
	retireErr   error

	written map[string]MobileDeviceNode
	order   []string
	retired []string
}

func (f *fakeDeviceStore) ConsumerKey(context.Context) (string, error) {
	if f.consumerErr != nil {
		return "", f.consumerErr
	}
	return f.consumer, nil
}

func (f *fakeDeviceStore) Write(_ context.Context, uid string, node MobileDeviceNode) error {
	f.order = append(f.order, "write "+uid)
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.written == nil {
		f.written = map[string]MobileDeviceNode{}
	}
	f.written[uid] = node
	return nil
}

func (f *fakeDeviceStore) Retire(_ context.Context, uid string) error {
	f.order = append(f.order, "retire "+uid)
	f.retired = append(f.retired, uid)
	return f.retireErr
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// restoreEndpoint is the project's mobile endpoint in these tests. It is a
// host in the reserved .invalid domain (Convention 2): no test in this
// repository reaches a network, and an endpoint that resolved would be one a
// test could.
const restoreEndpoint = "https://mobile.example.invalid/p"

// restoreEmail is the address the assertion in these tests was verified for,
// and the address the roster holds.
const restoreEmail = "member@example.invalid"

// restoreFixture is one MobileRestore with every side it calls replaced. The
// fields are the doubles themselves so a test can read what the route did and
// set what a side answers before it is called.
type restoreFixture struct {
	h *MobileRestore

	verifier   *fakeVerifier
	membership *fakeMembership
	members    *fakeMemberStore
	claims     *fakeClaimStore
	refresh    *fakeRefreshStore
	minter     *fakeMinter
	devices    *fakeDeviceStore
	projects   *fakePolicy

	// challenge is the challenge this fixture's service issued: the value a
	// test posts to the restore, built once so that a test which posts it twice
	// posts the same one. It is fresh (built when the fixture was) and the MAC
	// is the deployment key's, which is what the route checks before anything
	// else.
	challenge string

	audit *bytes.Buffer
	log   *bytes.Buffer
}

// newRestoreFixture builds the handler over doubles that admit: the assertion
// verifies, the challenge is unspent, the policy allows with the operator's
// role and the published policy is a plain project's. A test that wants a
// refusal changes the one double that answers it.
func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	f := &restoreFixture{
		challenge: mobileChallengeValue(restoreChallengeKey(), make([]byte, mobileChallengeNonceBytes), time.Now()),
		verifier: &fakeVerifier{claims: mobilesession.Claims{
			Issuer:        "https://identity.example.invalid",
			Audience:      mustAudience(t, restoreEndpoint),
			Subject:       "provider-sub-1",
			Email:         restoreEmail,
			EmailVerified: true,
			Provider:      projectpolicy.ProviderGoogle,
			AuthTime:      time.Unix(1_700_000_000, 0).Unix(),
			JTI:           "assertion-1",
		}},
		membership: &fakeMembership{verdict: mobilesession.Verdict{Allowed: true, Role: access.RoleOperator}, crashReports: true},
		members:    &fakeMemberStore{record: mobilesession.Member{Email: restoreEmail, Provider: projectpolicy.ProviderGoogle, Sub: "provider-sub-1"}, found: true},
		claims:     &fakeClaimStore{},
		refresh:    &fakeRefreshStore{},
		minter:     &fakeMinter{},
		devices:    &fakeDeviceStore{consumer: "the-consumer-key"},
		projects:   &fakePolicy{shared: &projectpolicy.Policy{Version: projectpolicy.Version}, sharedFound: true},
	}
	auditor, buf := newAudit(t)
	f.audit = buf
	var logs bytes.Buffer
	f.log = &logs
	h, err := NewMobileRestore(MobileRestore{
		Project: MobileProjectConfig{
			ProjectID:   "the-project",
			Endpoint:    restoreEndpoint,
			DatabaseURL: "https://the-project.firebaseio.invalid",
			APIKey:      "the-api-key",
		},
		ChallengeKey: restoreChallengeKey(),
		Policy:       f.projects,
		Verifier:     f.verifier,
		Membership:   f.membership,
		Members:      f.members,
		Claims:       f.claims,
		Refresh:      f.refresh,
		Minter:       f.minter,
		Devices:      f.devices,
		Audit:        auditor,
		Log:          slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewMobileRestore: %v", err)
	}
	f.h = h
	return f
}

// mustAudience is AudienceOf for a test: the derivation is the contract's, and
// an endpoint it refuses would be a test that never got started.
func mustAudience(t *testing.T, endpoint string) string {
	t.Helper()
	aud, ok := mobilesession.AudienceOf(endpoint)
	if !ok {
		t.Fatalf("AudienceOf(%q) is refused: the test's own endpoint is not one the contract derives an audience from", endpoint)
	}
	return aud
}

// deviceKey is a device public key in the wire spelling: 32 zero bytes, which
// is the vectors' own placeholder and no key at all.
func deviceKey() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, mobilesession.DevicePublicKeySize))
}

// restoreChallengeKey is the key the fixture's handler stamps its challenges
// with and checks them against: the SHA-256 of a plain sentence, which is a key
// of the size the constructor requires and no credential — a literal that read
// like one would be copied into a fixture and then into a report.
func restoreChallengeKey() []byte {
	sum := sha256.Sum256([]byte("not-a-credential-test-challenge-key"))
	return sum[:]
}

// body is a restore call's body: the challenge the fixture issued and the
// device's key.
func restoreBody(challenge string) string {
	req := mobilesession.RestoreRequest{
		Assertion:       "assertion-the-identity-project-signed",
		Challenge:       challenge,
		DevicePublicKey: deviceKey(),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// restore posts a restore call and answers what the handler wrote.
func (f *restoreFixture) restore(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeRestore(rec, httptest.NewRequest(http.MethodPost, mobilesession.RouteRestore, strings.NewReader(body)))
	return rec
}

// mobileFlag is the written true or false a *bool member of the published
// mobile block carries: the zero value of the member is "not written", so a
// test cannot state one without taking its address.
func mobileFlag(v bool) *bool { return &v }

// strictSharedPolicy is the published policy of a project whose strict switch
// is on (D2784): every new device pairs again with a QR.
func strictSharedPolicy() *projectpolicy.Policy {
	return &projectpolicy.Policy{
		Version: projectpolicy.Version,
		Mobile:  &projectpolicy.Mobile{StrictNewDevice: mobileFlag(true)},
	}
}

// refusalCode is the contract's code from a refusal body, failing the test when
// the body is not one: a mobile refusal is C1's Error and never a sentence.
func refusalCode(t *testing.T, rec *httptest.ResponseRecorder) mobilesession.Code {
	t.Helper()
	var e mobilesession.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("the refusal is not one the contract reads: %v (%s)", err, rec.Body.String())
	}
	if e.Code == "" {
		t.Fatalf("the refusal names no code: %s", rec.Body.String())
	}
	return e.Code
}

// ---------------------------------------------------------------------------
// The strict switch (K2's red step)
// ---------------------------------------------------------------------------

// TestMobileRestoreStrictProjectRefusesWithQR is K2's red step: a project whose
// published policy carries `mobile.strict_new_device` refuses a restore
// outright and sends the phone back to the QR — and it refuses BEFORE the allow
// path, so the refusal is made with no device node written, no refresh family
// issued and no custom token minted.
//
// It is one line of the plan's global table ("Restore with strict switch on
// needs a fresh QR") and one of D2784's two halves: a project that wants the
// second factor the physical QR is.
func TestMobileRestoreStrictProjectRefusesWithQR(t *testing.T) {
	f := newRestoreFixture(t)
	f.projects.shared = strictSharedPolicy()

	rec := f.restore(t, restoreBody(f.challenge))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("a restore against a strict project is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if code := refusalCode(t, rec); code != mobilesession.CodeStrictNewDeviceNeedsQR {
		t.Errorf("the refusal is %q, want %q", code, mobilesession.CodeStrictNewDeviceNeedsQR)
	}
	// The allow path did not run: no node, no family, no token. This is what
	// "refuses before the allow path exists" means, and it is the half of the
	// test that a handler which refused AFTER admitting would fail.
	if len(f.devices.written) != 0 {
		t.Errorf("the refused restore wrote %d device nodes, want none", len(f.devices.written))
	}
	if len(f.refresh.issued) != 0 {
		t.Errorf("the refused restore issued the families %v, want none", f.refresh.issued)
	}
	if len(f.minter.reqs) != 0 {
		t.Errorf("the refused restore asked for %d custom tokens, want none", len(f.minter.reqs))
	}

	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the refused restore wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	if got := auditField(lines[0], "purpose"); got != PurposeMobileRestore {
		t.Errorf("the audit line's purpose is %q, want %q", got, PurposeMobileRestore)
	}
	if got := auditField(lines[0], "caller"); got != restoreEmail {
		t.Errorf("the audit line's caller is %q, want the verified address %q", got, restoreEmail)
	}
	if got := auditField(lines[0], "status"); got != ReasonNotAdmitted {
		t.Errorf("the audit line's status is %q, want %q: the contract's refusal codes are not statuses of this service's audit, and not_admitted is the word for a caller this service named and does not admit", got, ReasonNotAdmitted)
	}
}

// ---------------------------------------------------------------------------
// The doubles' reader side
// ---------------------------------------------------------------------------

// challenge posts a challenge call and answers what the handler wrote.
func (f *restoreFixture) challengeCall(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeChallenge(rec, httptest.NewRequest(http.MethodPost, mobilesession.RouteRestoreChallenge, strings.NewReader(body)))
	return rec
}

// restoreRefusal is the contract's refusal from a body, failing the test when the
// body is not one: every answer of these routes is C1's Error — a body the route
// could not read and a failure of the service's own included — so a body
// carrying the runner's wire shape is a failure here, not a shape to read.
func restoreRefusal(t *testing.T, rec *httptest.ResponseRecorder) mobilesession.Error {
	t.Helper()
	var e mobilesession.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("the refusal is not one the contract reads: %v (%s)", err, rec.Body.String())
	}
	if e.Code == "" {
		t.Fatalf("the refusal names no code, and the contract's routes answer a code and never a sentence alone: %s", rec.Body.String())
	}
	return e
}

// noAuditLine is the rule for a refusal taken before this service could name an
// identity: the request shape and the assertion write nothing (P0, and the
// file doc's order).
func noAuditLine(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	if buf.Len() != 0 {
		t.Fatalf("the call wrote an audit line it must not have: %s", buf.String())
	}
}

// deviceUID is the uid the route drew, read off the node it wrote. It is one
// node per admitted device, so a test that reads it has one and no more.
func deviceUID(t *testing.T, f *restoreFixture) string {
	t.Helper()
	if len(f.devices.written) != 1 {
		t.Fatalf("the restore wrote %d device nodes, want exactly one: %v", len(f.devices.written), f.devices.written)
	}
	for uid := range f.devices.written {
		return uid
	}
	panic("one node, and the loop above returned nothing")
}

// ---------------------------------------------------------------------------
// The challenge route
// ---------------------------------------------------------------------------

// TestMobileRestoreChallengeIsFreshAndBoundToTheContract has the challenge
// route answer one challenge: a fresh random value of the contract's size, its
// life in seconds, and no-store, so a cache in front of the public service
// cannot hand one phone another's challenge.
//
// The route writes no audit line, and that is the point of it: a challenge
// admits nobody and the caller has proved nothing, so there is no identity to
// name (P0's rule is a line per act and this act has none until the restore
// spends it).
func TestMobileRestoreChallengeIsFreshAndBoundToTheContract(t *testing.T) {
	f := newRestoreFixture(t)

	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		rec := f.challengeCall(t, "{}")
		if rec.Code != http.StatusOK {
			t.Fatalf("the challenge route answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != contentTypeJSON {
			t.Errorf("the challenge answer's content type is %q, want %q", ct, contentTypeJSON)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("the challenge answer's cache control is %q, want %q: one phone's challenge must never be another's", cc, "no-store")
		}
		var resp mobilesession.RestoreChallengeResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("the challenge answer is not one the contract reads: %v (%s)", err, rec.Body.String())
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(resp.Challenge)
		if err != nil || len(raw) != mobileChallengeLen {
			t.Fatalf("the challenge %q is not %d bytes of unpadded base64url: %v", resp.Challenge, mobileChallengeLen, err)
		}
		if _, ok := mobileChallengeIssuedAt(restoreChallengeKey(), resp.Challenge); !ok {
			t.Fatalf("the challenge %q does not carry the MAC of the key this deployment holds, so the restore route would refuse the challenge this route just issued", resp.Challenge)
		}
		if seen[resp.Challenge] {
			t.Fatalf("the challenge route answered %q twice in eight calls, and a challenge that repeats is one an attacker can guess", resp.Challenge)
		}
		seen[resp.Challenge] = true
		if want := int(mobilesession.ChallengeLifetime / time.Second); resp.ExpiresIn != want {
			t.Errorf("the challenge's life is %d seconds, want the contract's %d (ChallengeLifetime)", resp.ExpiresIn, want)
		}
		noAuditLine(t, f.audit)
	}
}

// TestMobileRestoreSpendsTheChallengeItIssued is the two routes' agreement on
// one value: the challenge the route hands out is one the restore route admits,
// on a restored device, with nothing shared between them but the deployment's
// key. It is the test the MAC is for — the challenge the phone paid a round trip
// for is the challenge the service recognises, and it is the value the restore
// then spends once.
func TestMobileRestoreSpendsTheChallengeItIssued(t *testing.T) {
	f := newRestoreFixture(t)

	rec := f.challengeCall(t, "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("the challenge route answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp mobilesession.RestoreChallengeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("the challenge answer is not one the contract reads: %v (%s)", err, rec.Body.String())
	}

	restored := f.restore(t, restoreBody(resp.Challenge))
	if restored.Code != http.StatusOK {
		t.Fatalf("the restore of the challenge this service just issued is answered %d, want %d: %s", restored.Code, http.StatusOK, restored.Body.String())
	}
	if len(f.claims.keys) != 1 || f.claims.keys[0] != resp.Challenge {
		t.Errorf("the claimed challenges are %v, want exactly the one this service issued (%q)", f.claims.keys, resp.Challenge)
	}
}

// TestMobileRestoreChallengeRefusesABodyThatNamesAField holds the route to the
// shape the contract declares: RestoreChallengeRequest carries nothing, and a
// field this release ignored would be one the next release reads as something
// else. The call is answered as this service refuses a body it cannot read, and
// it writes no line.
func TestMobileRestoreChallengeRefusesABodyThatNamesAField(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"not JSON", "{"},
		{"names a field", `{"anything":1}`},
		{"an array", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestoreFixture(t)
			rec := f.challengeCall(t, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("a challenge body that is %s is answered %d, want %d: %s", tc.name, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
				t.Errorf("the refusal is %q, want the contract's %q: a body this route could not read is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
			}
			noAuditLine(t, f.audit)
		})
	}
}

// TestMobileRestoreChallengeRefusesAChallengeItCannotDraw pins the one failure
// this route may never fall back from: a challenge that is not random is one an
// attacker binds their own assertion to and spends before the phone does, so a
// draw that fails answers the service's unavailable state instead.
func TestMobileRestoreChallengeRefusesAChallengeItCannotDraw(t *testing.T) {
	f := newRestoreFixture(t)
	restoreRandom(t, failingReader{})

	rec := f.challengeCall(t, "{}")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a challenge that could not be drawn is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
		t.Errorf("the refusal is %q, want the contract's %q: a failure of the service is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
	}
	noAuditLine(t, f.audit)
}

// failingReader is a randomness source that fails, which is the only way a test
// reaches the routes' draw failures: crypto/rand does not fail on demand.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("the test's randomness source fails")
}

// restoreRandom replaces the routes' randomness source for one test and puts
// the real one back, so a draw failure does not leak into another test.
func restoreRandom(t *testing.T, r io.Reader) {
	t.Helper()
	previous := mobileRandom
	mobileRandom = r
	t.Cleanup(func() { mobileRandom = previous })
}

// ---------------------------------------------------------------------------
// The allow path
// ---------------------------------------------------------------------------

// TestMobileRestoreAdmitsADevice is the whole of what a restore does when no
// side refuses: it spends the challenge once, asks the pairing's own policy,
// writes one device node, starts the family, mints the token, records one line
// and answers the session (K2's acceptance, C1's SessionResponse).
//
// Every value the phone holds is checked, because the answer is the only place
// three of them travel (the database URL, the API key and the consumer key are
// in no QR, F4), and the device node is checked because it is what the consumer
// and the database rules read afterwards.
func TestMobileRestoreAdmitsADevice(t *testing.T) {
	f := newRestoreFixture(t)
	// The project's own compaction threshold, so that the policy half below is
	// checked against a value the zero default cannot stand in for (Z2, D2789).
	f.projects.shared.MobileCompactionThreshold = intPtr(55)

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusOK {
		t.Fatalf("the restore is answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("the session answer's cache control is %q, want %q: a session is one device's and never a cache's", cc, "no-store")
	}
	var session mobilesession.SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("the session answer is not one the contract reads: %v (%s)", err, rec.Body.String())
	}
	if session.ProjectID != "the-project" {
		t.Errorf("the session's project id is %q, want %q", session.ProjectID, "the-project")
	}
	if session.Endpoint != restoreEndpoint {
		t.Errorf("the session's endpoint is %q, want the deployment's own %q", session.Endpoint, restoreEndpoint)
	}
	if session.DatabaseURL != "https://the-project.firebaseio.invalid" {
		t.Errorf("the session's database URL is %q, want the deployment's own", session.DatabaseURL)
	}
	if session.APIKey != "the-api-key" {
		t.Errorf("the session's API key is %q, want the deployment's own", session.APIKey)
	}
	if session.ConsumerPublicKey != "the-consumer-key" {
		t.Errorf("the session's consumer key is %q, want the published one: the phone pins it and verifies every mailbox reply with it (O12)", session.ConsumerPublicKey)
	}
	if session.Role != access.RoleOperator {
		t.Errorf("the session's role is %q, want the roster's %q (O14)", session.Role, access.RoleOperator)
	}
	if want := int(mobilesession.CustomTokenLifetime / time.Second); session.ExpiresIn != want {
		t.Errorf("the custom token's life is %d seconds, want the contract's %d", session.ExpiresIn, want)
	}

	// The policy half of the answer is the published node's (P6), and the
	// compaction threshold is the node's own whole percent (Z2, D2789): the
	// phone's automatic compaction fires on it, and the member the node carries
	// none of is the contract's zero, "this project set no automatic
	// compaction".
	node := f.projects.shared
	wantModes := node.MobileCostlyModes()
	if len(session.Policy.CostlyModes) != len(wantModes) {
		t.Errorf("the session's costly modes are %v, want the published node's %v", session.Policy.CostlyModes, wantModes)
	} else {
		for i, mode := range wantModes {
			if session.Policy.CostlyModes[i] != string(mode) {
				t.Errorf("the session's costly mode %d is %q, want %q", i, session.Policy.CostlyModes[i], string(mode))
			}
		}
	}
	if session.Policy.BiometricOnCostly != node.MobileBiometricOnCostly() {
		t.Errorf("the session's biometric switch is %v, want the published node's %v", session.Policy.BiometricOnCostly, node.MobileBiometricOnCostly())
	}
	// The fixture's node carries 55 (the test's second line), so this compares
	// against a value the contract's zero cannot stand in for: a session policy
	// that read no project field would answer 0 and fail here.
	if got, want := session.Policy.CompactThresholdPercent, node.MobileCompactionThresholdPercent(); got != want {
		t.Errorf("the session's compaction threshold is %d, want the published node's %d: the phone's automatic compaction fires on the project's own value", got, want)
	}
	// The crash-report switch is the policy's own answer and never a value this
	// route invents (D731, D738): C1 writes the member whether or not the
	// published policy carries the key, and the app sends nothing before it has
	// read it (D734).
	if !session.Policy.CrashReports {
		t.Errorf("the session's crash-report switch is off, want the policy's own answer: the app reads it before it collects anything (D731, D734, D738)")
	}
	if len(f.membership.crashes) != 1 {
		t.Errorf("the policy was asked for the crash-report switch %d times, want exactly one", len(f.membership.crashes))
	}

	// What the phone holds afterwards belongs to the device the route just
	// made: the node's uid is the family's and the token's, and a route that
	// mixed the three would hand one device another's session.
	uid := deviceUID(t, f)
	node0 := f.devices.written[uid]
	if node0.Email != restoreEmail {
		t.Errorf("the device node's address is %q, want the verified %q", node0.Email, restoreEmail)
	}
	if node0.Role != string(access.RoleOperator) {
		t.Errorf("the device node's role is %q, want the roster's %q (O14)", node0.Role, access.RoleOperator)
	}
	if node0.PublicKey != deviceKey() {
		t.Errorf("the device node's public key is not the one the call carried")
	}
	if _, err := time.Parse(time.RFC3339, node0.At); err != nil {
		t.Errorf("the device node's time is %q, which is not the RFC3339 this tree writes on every node: %v", node0.At, err)
	}
	if len(f.refresh.issued) != 1 || f.refresh.issued[0] != uid {
		t.Errorf("the families issued are %v, want the node's own uid %q", f.refresh.issued, uid)
	}
	if session.RefreshToken != "refresh-"+uid {
		t.Errorf("the session's refresh token is %q, want the family's own for %q", session.RefreshToken, uid)
	}
	if len(f.minter.reqs) != 1 {
		t.Fatalf("the restore asked for %d custom tokens, want exactly one", len(f.minter.reqs))
	}
	if f.minter.reqs[0].UID != uid {
		t.Errorf("the mint was asked for uid %q, want the node's own %q", f.minter.reqs[0].UID, uid)
	}
	if f.minter.reqs[0].Role != access.RoleOperator {
		t.Errorf("the mint was asked for role %q, want the roster's %q", f.minter.reqs[0].Role, access.RoleOperator)
	}
	if session.CustomToken != "custom-"+uid {
		t.Errorf("the session's custom token is %q, want the mint's own for %q", session.CustomToken, uid)
	}

	// The assertion was bound to this deployment's audience and to the
	// challenge the call carried, which is what F39 requires of it.
	if len(f.verifier.bindings) != 1 {
		t.Fatalf("the verifier was asked about %d assertions, want exactly one", len(f.verifier.bindings))
	}
	if want := mustAudience(t, restoreEndpoint); f.verifier.bindings[0].Audience != want {
		t.Errorf("the assertion was bound to audience %q, want the deployment's own %q", f.verifier.bindings[0].Audience, want)
	}
	if f.verifier.bindings[0].Challenge != f.challenge {
		t.Errorf("the assertion was bound to challenge %q, want the call's own", f.verifier.bindings[0].Challenge)
	}

	// The challenge was spent once, under the contract's namespace and for its
	// own life, which is what makes it single-use across instances (F39).
	if len(f.claims.keys) != 1 {
		t.Fatalf("the route claimed %d values, want exactly one", len(f.claims.keys))
	}
	if f.claims.namespaces[0] != mobilesession.NamespaceChallenge {
		t.Errorf("the challenge was claimed under namespace %q, want %q", f.claims.namespaces[0], mobilesession.NamespaceChallenge)
	}
	if f.claims.keys[0] != f.challenge {
		t.Errorf("the route claimed %q, want the call's own challenge", f.claims.keys[0])
	}
	if f.claims.ttls[0] != mobilesession.ChallengeLifetime {
		t.Errorf("the claim's ttl is %s, want the contract's %s", f.claims.ttls[0], mobilesession.ChallengeLifetime)
	}

	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the restore wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	if got := auditField(lines[0], "purpose"); got != PurposeMobileRestore {
		t.Errorf("the audit line's purpose is %q, want %q", got, PurposeMobileRestore)
	}
	if got := auditField(lines[0], "caller"); got != restoreEmail {
		t.Errorf("the audit line's caller is %q, want the verified address %q", got, restoreEmail)
	}
	if got := auditField(lines[0], "status"); got != StatusOK {
		t.Errorf("the audit line's status is %q, want %q: the restore was made", got, StatusOK)
	}
}

// TestMobileRestoreWritesTheCanonicalAddress has the node and the audit line
// carry the address in the one spelling two writers agree on (mobileauth's
// rule, D222): the domain folded and the local part kept byte for byte. A route
// that wrote the assertion's text verbatim would key the node by a second
// spelling of one person.
func TestMobileRestoreWritesTheCanonicalAddress(t *testing.T) {
	f := newRestoreFixture(t)
	f.verifier.claims.Email = "Member@Example.INVALID"

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusOK {
		t.Fatalf("the restore is answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	const canonical = "Member@example.invalid"
	uid := deviceUID(t, f)
	if got := f.devices.written[uid].Email; got != canonical {
		t.Errorf("the device node's address is %q, want the canonical %q", got, canonical)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the restore wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	if got := auditField(lines[0], "caller"); got != canonical {
		t.Errorf("the audit line's caller is %q, want the canonical %q", got, canonical)
	}
}

// ---------------------------------------------------------------------------
// The refusals a verified identity gets
// ---------------------------------------------------------------------------

// TestMobileRestoreRefusesAnAssertionItCannotVerify is the first refusal: an
// assertion the verifier will not accept is answered as auth_too_old — the
// app's screen for it is "sign out and back in", which is what a phone holding
// one must do — and the route records NOTHING, because the identity is exactly
// what it could not establish.
//
// The challenge is not spent either, and that order is deliberate: a claim
// taken before the assertion verified would let any caller burn a project's
// challenges with assertions that never verified.
func TestMobileRestoreRefusesAnAssertionItCannotVerify(t *testing.T) {
	f := newRestoreFixture(t)
	f.verifier.err = errors.New("the assertion did not verify")

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an unverifiable assertion is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if code := refusalCode(t, rec); code != mobilesession.CodeAuthTooOld {
		t.Errorf("the refusal is %q, want %q", code, mobilesession.CodeAuthTooOld)
	}
	noAuditLine(t, f.audit)
	if len(f.claims.keys) != 0 {
		t.Errorf("the route claimed %v before the assertion verified, want nothing claimed", f.claims.keys)
	}
}

// TestMobileRestoreRefusesAnAddressItCannotName is the second half of the same
// refusal: an assertion that verified but names an address this tree cannot
// write down is an identity this service cannot key a node by or name in a
// line, so it is refused before anything is written and recorded nowhere.
func TestMobileRestoreRefusesAnAddressItCannotName(t *testing.T) {
	f := newRestoreFixture(t)
	f.verifier.claims.Email = "not an address"

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an address this tree cannot write down is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if code := refusalCode(t, rec); code != mobilesession.CodeAuthTooOld {
		t.Errorf("the refusal is %q, want %q: what to do about it is sign in again", code, mobilesession.CodeAuthTooOld)
	}
	noAuditLine(t, f.audit)
	if len(f.claims.keys) != 0 {
		t.Errorf("the route claimed %v, want nothing claimed: the address was refused before the challenge was spent", f.claims.keys)
	}
}

// TestMobileRestoreRefusesASpentChallenge is the challenge's single use: the
// first restore spends it, and the second call with the same value is answered
// nonce_expired. Unlike a failed assertion this refusal is RECORDED — the
// identity verified and the address was nameable, so this service knows who
// was refused.
func TestMobileRestoreRefusesASpentChallenge(t *testing.T) {
	f := newRestoreFixture(t)

	if rec := f.restore(t, restoreBody(f.challenge)); rec.Code != http.StatusOK {
		t.Fatalf("the first restore is answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a spent challenge is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if code := refusalCode(t, rec); code != mobilesession.CodeNonceExpired {
		t.Errorf("the refusal is %q, want %q", code, mobilesession.CodeNonceExpired)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 2 {
		t.Fatalf("the two calls wrote %d audit lines, want one each: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[1])
	if got := auditField(lines[1], "caller"); got != restoreEmail {
		t.Errorf("the refusal's caller is %q, want the verified address %q", got, restoreEmail)
	}
	if got := auditField(lines[1], "status"); got != ReasonNotAdmitted {
		t.Errorf("the refusal's status is %q, want %q", got, ReasonNotAdmitted)
	}
	if len(f.devices.written) != 1 {
		t.Errorf("the two calls wrote %d device nodes, want only the first call's one", len(f.devices.written))
	}
}

// TestMobileRestoreRefusesAChallengePastItsLife pins the ten minutes the
// challenge route reports (C1's ChallengeLifetime) as a life this route
// enforces and not a figure it only advertises: a challenge of ours whose issue
// time is older than that is refused nonce_expired, BEFORE the store is asked —
// an expired challenge is not a spent one — and the refusal is recorded like the
// spent challenge's, because the identity verified and the address was
// nameable.
func TestMobileRestoreRefusesAChallengePastItsLife(t *testing.T) {
	f := newRestoreFixture(t)
	stale := mobileChallengeValue(
		restoreChallengeKey(),
		make([]byte, mobileChallengeNonceBytes),
		time.Now().Add(-mobilesession.ChallengeLifetime-time.Minute),
	)

	rec := f.restore(t, restoreBody(stale))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a challenge past its life is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if code := refusalCode(t, rec); code != mobilesession.CodeNonceExpired {
		t.Errorf("the refusal is %q, want %q", code, mobilesession.CodeNonceExpired)
	}
	if len(f.claims.keys) != 0 {
		t.Errorf("a challenge past its life reached the claim store (%v), want nothing written: it was refused before the store was asked", f.claims.keys)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	if got := auditField(lines[0], "caller"); got != restoreEmail {
		t.Errorf("the refusal's caller is %q, want the verified address %q", got, restoreEmail)
	}
	if len(f.devices.written) != 0 {
		t.Errorf("the refusal wrote %d device nodes, want none", len(f.devices.written))
	}
}

// TestMobileRestoreRefusesAPolicyVerdict walks C1's Policy's refusals: each is
// answered with the policy's own code — the app renders a screen per code — and
// each writes exactly one line naming the member. Nothing of the allow path
// runs, which is what a refusal means here.
//
// Two codes that are inside the contract's set are the service's own failure
// when a policy answers them: nonce_expired is the pair route's answer about an
// invite no policy sees, and strict_new_device_needs_qr is this route's own
// decision from the published switch. A code outside the set is a code nobody
// wrote, and the app would show it as a crash.
func TestMobileRestoreRefusesAPolicyVerdict(t *testing.T) {
	for _, tc := range []struct {
		name     string
		verdict  mobilesession.Verdict
		wantCode mobilesession.Code // empty means this service's own failure
		wantList []string
	}{
		{"not a member", mobilesession.Verdict{Code: mobilesession.CodeNotAMember}, mobilesession.CodeNotAMember, nil},
		{"provider not accepted", mobilesession.Verdict{Code: mobilesession.CodeProviderNotAccepted, Accepted: []string{projectpolicy.ProviderGoogle}}, mobilesession.CodeProviderNotAccepted, []string{projectpolicy.ProviderGoogle}},
		{"authentication too old", mobilesession.Verdict{Code: mobilesession.CodeAuthTooOld}, mobilesession.CodeAuthTooOld, nil},
		{"mobile disabled", mobilesession.Verdict{Code: mobilesession.CodeMobileDisabled}, mobilesession.CodeMobileDisabled, nil},
		{"device revoked", mobilesession.Verdict{Code: mobilesession.CodeDeviceRevoked}, mobilesession.CodeDeviceRevoked, nil},
		{"the pair route's own code", mobilesession.Verdict{Code: mobilesession.CodeNonceExpired}, "", nil},
		{"this route's own strict code", mobilesession.Verdict{Code: mobilesession.CodeStrictNewDeviceNeedsQR}, "", nil},
		{"a code nobody wrote", mobilesession.Verdict{Code: mobilesession.Code("not_a_code")}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestoreFixture(t)
			f.membership.verdict = tc.verdict

			rec := f.restore(t, restoreBody(f.challenge))
			if tc.wantCode == "" {
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("a policy answer this route does not render is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
				}
				if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
					t.Errorf("the refusal is %q, want the contract's %q: a failure of the service is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
				}
			} else {
				if rec.Code != http.StatusForbidden {
					t.Fatalf("the policy's refusal is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
				}
				if code := refusalCode(t, rec); code != tc.wantCode {
					t.Errorf("the refusal is %q, want the policy's own %q", code, tc.wantCode)
				}
				if tc.wantList != nil {
					var e mobilesession.Error
					if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
						t.Fatalf("the refusal is not one the contract reads: %v", err)
					}
					if len(e.AcceptedProviders) != len(tc.wantList) || (len(tc.wantList) == 1 && e.AcceptedProviders[0] != tc.wantList[0]) {
						t.Errorf("the refusal carries the accepted providers %v, want the policy's %v", e.AcceptedProviders, tc.wantList)
					}
				}
			}
			if len(f.devices.written) != 0 || len(f.refresh.issued) != 0 || len(f.minter.reqs) != 0 {
				t.Errorf("the refused restore reached the allow path: %v nodes, %v families, %d mints", len(f.devices.written), f.refresh.issued, len(f.minter.reqs))
			}
			lines := auditLines(t, f.audit)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one: %v", len(lines), lines)
			}
			checkAuditShape(t, lines[0])
			if got := auditField(lines[0], "caller"); got != restoreEmail {
				t.Errorf("the line's caller is %q, want the verified address %q", got, restoreEmail)
			}
			wantStatus := ReasonNotAdmitted
			if tc.wantCode == "" {
				wantStatus = ErrorStateUnavailable
			}
			if got := auditField(lines[0], "status"); got != wantStatus {
				t.Errorf("the line's status is %q, want %q", got, wantStatus)
			}
		})
	}
}

// TestMobileRestoreRefusesAMemberWithNoRecordedIdentity is the record half of
// C1's policy, which this route applies for itself: the policy admits an address
// the project holds no identity for, because for a PAIRING that absence is the
// invitation's own first use (D2785). A restore spends no invite and has no QR,
// and a QR is the one way a first device comes in (F8) — so an address this
// project has never recorded an identity for is refused, with no node, no family
// and no token, and the refusal is recorded like any other.
func TestMobileRestoreRefusesAMemberWithNoRecordedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*restoreFixture)
	}{
		{"no record at all", func(f *restoreFixture) { f.members.found = false }},
		{"a record that holds no identity", func(f *restoreFixture) { f.members.record = mobilesession.Member{Email: restoreEmail} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestoreFixture(t)
			tc.mutate(f)

			rec := f.restore(t, restoreBody(f.challenge))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("a member with no recorded identity is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if code := refusalCode(t, rec); code != mobilesession.CodeNotAMember {
				t.Errorf("the refusal is %q, want %q: the assertion's sub is not the one this project first recorded, because it recorded none", code, mobilesession.CodeNotAMember)
			}
			if len(f.devices.written) != 0 || len(f.refresh.issued) != 0 || len(f.minter.reqs) != 0 {
				t.Errorf("the refused restore reached the allow path: %d nodes, %v families, %d mints", len(f.devices.written), f.refresh.issued, len(f.minter.reqs))
			}
			lines := auditLines(t, f.audit)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one: %v", len(lines), lines)
			}
			checkAuditShape(t, lines[0])
			if got := auditField(lines[0], "caller"); got != restoreEmail {
				t.Errorf("the line's caller is %q, want the verified address %q", got, restoreEmail)
			}
			if got := auditField(lines[0], "status"); got != ReasonNotAdmitted {
				t.Errorf("the line's status is %q, want %q", got, ReasonNotAdmitted)
			}
		})
	}
}

// TestMobileRestoreRefusesAMemberRecordItCannotRead is the other half of that
// read: a record the service cannot read is not an address it may admit, and it
// is the service's own failure rather than a refusal of the caller.
func TestMobileRestoreRefusesAMemberRecordItCannotRead(t *testing.T) {
	f := newRestoreFixture(t)
	f.members.err = errors.New("the member store is down")

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable member record is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if e := restoreRefusal(t, rec); e.Message != mobileMemberUnread {
		t.Errorf("the answer is %q, want the unreadable-record sentence", e.Message)
	}
	if len(f.devices.written) != 0 {
		t.Errorf("the restore wrote %d device nodes, want none", len(f.devices.written))
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the failure wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	if got := auditField(lines[0], "status"); got != ErrorStateUnavailable {
		t.Errorf("the line's status is %q, want %q", got, ErrorStateUnavailable)
	}
}

// TestMobileRestoreRefusesAnAllowedVerdictWithNoRole closes the one hole a
// policy's own document names: an allowed verdict with no role is a policy bug,
// and the node it would write is one no consumer can resolve an action against.
// It is answered as the service's own failure and recorded, rather than written
// as a device that can do nothing.
func TestMobileRestoreRefusesAnAllowedVerdictWithNoRole(t *testing.T) {
	f := newRestoreFixture(t)
	f.membership.verdict = mobilesession.Verdict{Allowed: true, Role: access.RoleNone}

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an allowed verdict with no role is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
		t.Errorf("the refusal is %q, want the contract's %q: a failure of the service is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
	}
	if len(f.devices.written) != 0 {
		t.Errorf("the restore wrote %d device nodes, want none: a node with no role is one no consumer can resolve", len(f.devices.written))
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
}

// TestMobileRestoreRefusesAProjectPolicyItCannotRead is the read of the
// published node: a policy the service cannot read is a project whose strict
// switch it does not know, so it refuses rather than admitting a device into a
// project that asked for a QR.
func TestMobileRestoreRefusesAProjectPolicyItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*restoreFixture)
	}{
		{"the read fails", func(f *restoreFixture) { f.projects.sharedErr = errors.New("the policy store is down") }},
		{"no node is published", func(f *restoreFixture) { f.projects.sharedFound = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestoreFixture(t)
			tc.mutate(f)

			rec := f.restore(t, restoreBody(f.challenge))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("an unreadable policy is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}
			if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
				t.Errorf("the refusal is %q, want the contract's %q: a failure of the service is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
			}
			if len(f.devices.written) != 0 {
				t.Errorf("the restore wrote %d device nodes, want none", len(f.devices.written))
			}
			lines := auditLines(t, f.audit)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one: %v", len(lines), lines)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The refusals a body gets
// ---------------------------------------------------------------------------

// TestMobileRestoreRefusesABadRequest holds the route to the contract's request
// shape before it reads anything: a body it will not hold is answered as the
// service refuses one, and it writes no line at all, because there is no
// identity to name and nothing was done.
func TestMobileRestoreRefusesABadRequest(t *testing.T) {
	f := newRestoreFixture(t)
	padded := base64.URLEncoding.EncodeToString(make([]byte, mobilesession.DevicePublicKeySize))
	short := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	paddedChallenge := base64.URLEncoding.EncodeToString(make([]byte, mobileChallengeLen))
	shortChallenge := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	challenge := f.challenge
	for _, tc := range []struct {
		name string
		body string
	}{
		{"not JSON", "{"},
		{"names an unknown field", `{"assertion":"a","challenge":"` + challenge + `","device_public_key":"` + deviceKey() + `","extra":1}`},
		{"an empty object", `{}`},
		{"no assertion", `{"challenge":"` + challenge + `","device_public_key":"` + deviceKey() + `"}`},
		{"no challenge", `{"assertion":"a","device_public_key":"` + deviceKey() + `"}`},
		{"no device key", `{"assertion":"a","challenge":"` + challenge + `"}`},
		{"a padded key", `{"assertion":"a","challenge":"` + challenge + `","device_public_key":"` + padded + `"}`},
		{"a key of the wrong size", `{"assertion":"a","challenge":"` + challenge + `","device_public_key":"` + short + `"}`},
		{"a padded challenge", `{"assertion":"a","challenge":"` + paddedChallenge + `","device_public_key":"` + deviceKey() + `"}`},
		{"a challenge of the wrong size", `{"assertion":"a","challenge":"` + shortChallenge + `","device_public_key":"` + deviceKey() + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestoreFixture(t)
			rec := f.restore(t, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("a body that is %s is answered %d, want %d: %s", tc.name, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
				t.Errorf("the refusal is %q, want the contract's %q: a body this route could not read is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
			}
			noAuditLine(t, f.audit)
			if len(f.verifier.assertions) != 0 {
				t.Errorf("the route asked the verifier about %v, want nothing: the body was refused first", f.verifier.assertions)
			}
			if len(f.claims.keys) != 0 {
				t.Errorf("the route claimed %v, want nothing claimed", f.claims.keys)
			}
		})
	}
}

// TestMobileRestoreRefusesAChallengeItDidNotIssue is the finding this route was
// read for: the challenge is caller-supplied text that the route would use
// VERBATIM as a key of the shared compare-and-set store, before any policy has
// been asked. An attacker who can obtain a signed assertion for this project
// (the signer needs no membership, and it signs an assertion for any challenge
// a verified phone names) could therefore write a path-shaped string, a
// database-key-illegal character or a value worth storing into a store every
// instance of the service reads.
//
// Two things have to hold for a value to be this service's, and every row below
// fails one of them. The spelling: this service's challenge is mobileChallengeLen
// bytes of unpadded base64url and nothing else is. The MAC: a well-formed value
// that this service did not stamp — an invented nonce, a tampered MAC, another
// key's — is not one it issued, and the identity project's signature over an
// assertion bound to it proves nothing about the challenge.
//
// Both are refused as the shape refusal, and the store is never reached — which
// is what the assertion on the claim store is: not merely that the call failed,
// but that nothing was written.
func TestMobileRestoreRefusesAChallengeItDidNotIssue(t *testing.T) {
	nonce := make([]byte, mobileChallengeNonceBytes)
	if _, err := io.ReadFull(mobileRandom, nonce); err != nil {
		t.Fatalf("the test's nonce: %v", err)
	}
	otherKey := sha256.Sum256([]byte("not-a-credential-test-other-challenge-key"))
	ours := mobileChallengeValue(restoreChallengeKey(), nonce, time.Now())
	raw, err := base64.RawURLEncoding.Strict().DecodeString(ours)
	if err != nil {
		t.Fatalf("the test's own challenge %q is not base64url: %v", ours, err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	for _, tc := range []struct {
		name      string
		challenge string
	}{
		{"a path traversal", "../../config/admin/access"},
		{"a database-key character", "a.b#c$d[e]f/g"},
		{"a challenge of the right length but not base64url", strings.Repeat("!", mobileChallengeLen)},
		{"a value long enough to be worth writing", strings.Repeat("x", 4096)},
		{"a challenge the store would take for another namespace's", "jti:"},
		{"a well-formed value this service never issued", base64.RawURLEncoding.EncodeToString(make([]byte, mobileChallengeLen))},
		{"a well-formed value with the MAC of another key", mobileChallengeValue(otherKey[:], nonce, time.Now())},
		{"a challenge of ours with its MAC altered", tampered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestoreFixture(t)
			body, err := json.Marshal(mobilesession.RestoreRequest{
				Assertion:       "assertion-the-identity-project-signed",
				Challenge:       tc.challenge,
				DevicePublicKey: deviceKey(),
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			rec := f.restore(t, string(body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("a challenge this service never issued is answered %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
				t.Errorf("the refusal is %q, want the contract's %q: a body this route could not read is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
			}
			if len(f.claims.keys) != 0 {
				t.Errorf("the challenge %q reached the claim store, want nothing written: the value is a key every instance of this service reads", tc.challenge)
			}
			if len(f.claims.namespaces) != 0 || len(f.verifier.assertions) != 0 {
				t.Errorf("the refused call reached the verifier (%v) or the store (%v), want neither", f.verifier.assertions, f.claims.namespaces)
			}
			if len(f.devices.written) != 0 || len(f.refresh.issued) != 0 || len(f.minter.reqs) != 0 {
				t.Errorf("the refused call reached the allow path: %d nodes, %v families, %d mints", len(f.devices.written), f.refresh.issued, len(f.minter.reqs))
			}
			noAuditLine(t, f.audit)
		})
	}
}

// ---------------------------------------------------------------------------
// What a failure of the service takes back
// ---------------------------------------------------------------------------

// TestMobileRestoreLeavesNothingBehindWhenItCannotAnswer is F32's rule one step
// early: a restore this service cannot answer must leave no device node and no
// live family — a node whose device holds no session is a device the database
// rules admit and nobody can revoke, and a family with no device is a token
// nobody holds.
//
// Each row is one step failing after the node was written, and the assertion is
// what the route took back and what it left: the device node is gone in every
// row, the family is revoked once it exists, and the caller is answered the
// service's unavailable state with one line.
func TestMobileRestoreLeavesNothingBehindWhenItCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*restoreFixture)
		wantNode  bool // the refused call wrote a node (and so must take it back)
		wantRevok bool // the refused call issued a family (and so must revoke it)
	}{
		{"the family cannot start", func(f *restoreFixture) { f.refresh.issueErr = errors.New("the family store is down") }, true, false},
		{"the token cannot be minted", func(f *restoreFixture) { f.minter.err = errors.New("the signer grant is gone") }, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestoreFixture(t)
			tc.mutate(f)

			rec := f.restore(t, restoreBody(f.challenge))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("a restore the service cannot finish is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}
			if e := restoreRefusal(t, rec); e.Code != mobilesession.CodeNotAMember {
				t.Errorf("the refusal is %q, want the contract's %q: a failure of the service is answered in the phone's own shape", e.Code, mobilesession.CodeNotAMember)
			}
			if tc.wantNode {
				uid := deviceUID(t, f)
				if !slices.Contains(f.devices.retired, uid) {
					t.Errorf("the node %q is still there: a device with no session is one nobody can revoke (F32)", uid)
				}
			}
			if tc.wantRevok {
				if len(f.refresh.revoked) != 1 {
					t.Errorf("the revoked families are %v, want the one this call issued", f.refresh.revoked)
				}
			} else if len(f.refresh.revoked) != 0 {
				t.Errorf("the route revoked %v, want none: no family was issued", f.refresh.revoked)
			}
			lines := auditLines(t, f.audit)
			if len(lines) != 1 {
				t.Fatalf("the failure wrote %d audit lines, want exactly one: %v", len(lines), lines)
			}
			if got := auditField(lines[0], "status"); got != ErrorStateUnavailable {
				t.Errorf("the line's status is %q, want %q", got, ErrorStateUnavailable)
			}
		})
	}
}

// TestMobileRestoreLeavesNothingWhenTheDeviceCannotBeWritten is the first step
// of the allow path failing: the node is not there, so there is nothing to take
// back and nothing to revoke — and the answer is the service's, not a refusal
// of a member the policy had already allowed.
func TestMobileRestoreLeavesNothingWhenTheDeviceCannotBeWritten(t *testing.T) {
	f := newRestoreFixture(t)
	f.devices.writeErr = errors.New("the device store is down")

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a device that could not be written is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if len(f.devices.retired) != 0 {
		t.Errorf("the route removed %v, want nothing: no node was written", f.devices.retired)
	}
	if len(f.refresh.issued) != 0 {
		t.Errorf("the route issued %v, want no family for a device it could not write", f.refresh.issued)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the failure wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
}

// TestMobileRestoreTakesBackADeviceItCannotRecord is D143's fail-closed rule on
// this route: a restore the service cannot audit is a restore it does not make.
// The node and the family are taken back before the caller is answered, so no
// session exists that no line names.
func TestMobileRestoreTakesBackADeviceItCannotRecord(t *testing.T) {
	f := newRestoreFixture(t)
	auditor, err := NewAuditor(&recordHandler{err: errors.New("the audit log is full")}, armed())
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	f.h.Audit = auditor

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a restore that could not be recorded is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if e := restoreRefusal(t, rec); e.Message != mobileRestoreUnaudited {
		t.Errorf("the answer is %q, want the unaudited sentence: the act was not made", e.Message)
	}
	uid := deviceUID(t, f)
	if !slices.Contains(f.devices.retired, uid) {
		t.Errorf("the node %q survived a restore the service could not record", uid)
	}
	if len(f.refresh.revoked) != 1 {
		t.Errorf("the revoked families are %v, want the one this call issued", f.refresh.revoked)
	}
	if f.log.Len() == 0 {
		t.Error("the service logged nothing: an operator reads the cleanup's failure from the log")
	}
}

// TestMobileRestoreRefusesAProjectWithNoConsumerKey is the allow path's first
// step, which is a READ: a project that has published no mailbox consumer key
// cannot complete a restore — the phone would pin nothing and could verify no
// reply it reads (O12) — so the call is refused before a device node exists that
// a later failure would have to take back. The identity is known by then, so the
// refusal carries the one line.
//
// The device uid's own draw is not reached from here: it is the pairing route's
// helper (mobileDeviceUID, P3), which reads crypto/rand directly, and this route
// guards its error exactly as the pairing does.
func TestMobileRestoreRefusesAProjectWithNoConsumerKey(t *testing.T) {
	f := newRestoreFixture(t)
	f.devices.consumerErr = errors.New("this installation never published a consumer key")

	rec := f.restore(t, restoreBody(f.challenge))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a project with no consumer key is answered %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if e := restoreRefusal(t, rec); e.Message != mobileConsumerKeyUnread {
		t.Errorf("the answer is %q, want the unreadable-consumer-key sentence", e.Message)
	}
	if len(f.devices.written) != 0 || len(f.refresh.issued) != 0 {
		t.Errorf("the refusal reached the allow path: %d nodes, %v families", len(f.devices.written), f.refresh.issued)
	}
	lines := auditLines(t, f.audit)
	if len(lines) != 1 {
		t.Fatalf("the failure wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	if got := auditField(lines[0], "status"); got != ErrorStateUnavailable {
		t.Errorf("the line's status is %q, want %q: the service could not read its own state", got, ErrorStateUnavailable)
	}
}

// ---------------------------------------------------------------------------
// The shapes this route states and shares
// ---------------------------------------------------------------------------

// TestMobileDeviceUIDAgreesWithTheBudgetTokensDeviceUID is the test P3's own
// comment promises (mobile_pair.go's mobileDeviceUIDPrefix): it holds this
// package's spelling of a device uid to internal/budget/token's, which is the
// code that mints one. The uid is a database key and a Firebase uid, so the two
// writers have to draw the same shape, and the constants are respelled because
// the minter's are unexported (internal/voicesvc's TestDeviceShapeIsTheMintersOwn
// is the same test for the same reason).
//
// The shape checked is mobileDeviceUIDValid's, which is the ONE spelling this
// package has of it: the pairing writes and retires nodes under it, the restore
// writes through the same store, and internal/voicesvc/auth.go admits it. A
// second copy of the rule in a test would be a second spelling.
//
// The signer is an echo: what is being read is the uid the minter DREW, not the
// token it wrapped around it.
func TestMobileDeviceUIDAgreesWithTheBudgetTokensDeviceUID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		dt, err := token.MintDevice(context.Background(), restoreEchoSigner{}, time.Unix(1_700_000_000, 0))
		if err != nil {
			t.Fatalf("MintDevice: %v", err)
		}
		if err := mobileDeviceUIDValid(dt.UID); err != nil {
			t.Fatalf("a uid the minter produced is not one this service would write: %q: %v", dt.UID, err)
		}
		seen[dt.UID] = true
	}
	if len(seen) != 32 {
		t.Errorf("the minter drew %d distinct uids of 32, and a uid that repeats is a device another one collides with", len(seen))
	}
	mine := map[string]bool{}
	for i := 0; i < 32; i++ {
		uid, err := mobileDeviceUID()
		if err != nil {
			t.Fatalf("mobileDeviceUID: %v", err)
		}
		if err := mobileDeviceUIDValid(uid); err != nil {
			t.Fatalf("this service drew a uid it would not accept: %q: %v", uid, err)
		}
		mine[uid] = true
	}
	if len(mine) != 32 {
		t.Errorf("this service drew %d distinct uids of 32", len(mine))
	}
}

// restoreEchoSigner is a token.Signer that returns the payload it was handed as
// a JWT: enough for MintDevice's own read-back, and no key of any kind.
type restoreEchoSigner struct{}

func (restoreEchoSigner) Email() string { return "signer@example.invalid" }

func (restoreEchoSigner) SignJWT(_ context.Context, payload []byte) (string, error) {
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature", nil
}

// TestNewMobileRestoreRefusesAnIncompleteConfiguration pins the constructor: a
// handler missing a side is not one that refuses, it is one that would answer a
// session it cannot make or admit a device it cannot record, so the deployment
// learns at construction instead of at the first phone.
//
// The endpoint is the one configuration fault that is not a missing field: an
// endpoint no audience can be derived from is an assertion that can never be
// bound, so it is refused too.
func TestNewMobileRestoreRefusesAnIncompleteConfiguration(t *testing.T) {
	f := newRestoreFixture(t)
	valid := func() MobileRestore {
		return MobileRestore{
			Project: f.h.Project, ChallengeKey: f.h.ChallengeKey,
			Policy: f.h.Policy, Verifier: f.h.Verifier,
			Membership: f.h.Membership, Members: f.h.Members, Claims: f.h.Claims, Refresh: f.h.Refresh,
			Minter: f.h.Minter, Devices: f.h.Devices, Audit: f.h.Audit,
		}
	}
	if _, err := NewMobileRestore(valid()); err != nil {
		t.Fatalf("the fixture's own configuration is refused: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*MobileRestore)
	}{
		{"no project id", func(c *MobileRestore) { c.Project.ProjectID = "" }},
		{"no endpoint", func(c *MobileRestore) { c.Project.Endpoint = "" }},
		{"no database URL", func(c *MobileRestore) { c.Project.DatabaseURL = "" }},
		{"no API key", func(c *MobileRestore) { c.Project.APIKey = "" }},
		{"no challenge key", func(c *MobileRestore) { c.ChallengeKey = nil }},
		{"a challenge key shorter than the one this route stamps with", func(c *MobileRestore) { c.ChallengeKey = restoreChallengeKey()[:mobileChallengeKeyBytes-1] }},
		{"no policy reader", func(c *MobileRestore) { c.Policy = nil }},
		{"no verifier", func(c *MobileRestore) { c.Verifier = nil }},
		{"no membership policy", func(c *MobileRestore) { c.Membership = nil }},
		{"no member store", func(c *MobileRestore) { c.Members = nil }},
		{"no claim store", func(c *MobileRestore) { c.Claims = nil }},
		{"no refresh store", func(c *MobileRestore) { c.Refresh = nil }},
		{"no minter", func(c *MobileRestore) { c.Minter = nil }},
		{"no device store", func(c *MobileRestore) { c.Devices = nil }},
		{"no auditor", func(c *MobileRestore) { c.Audit = nil }},
		{"an endpoint no audience comes from", func(c *MobileRestore) { c.Project.Endpoint = "not-a-url" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.mutate(&cfg)
			if _, err := NewMobileRestore(cfg); err == nil {
				t.Fatalf("a configuration with %s is accepted, want a refusal naming it", tc.name)
			}
		})
	}
	if f.h.Log == nil {
		t.Error("the handler's log is nil: a handler built without one logs nowhere, and the cleanup's failures are read there")
	}
}
