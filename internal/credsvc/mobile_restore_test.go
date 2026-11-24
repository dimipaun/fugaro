package credsvc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
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

// fakeMembership is C1's Policy: the verdict it was configured with, and the
// project and claims it was asked about.
type fakeMembership struct {
	verdict mobilesession.Verdict
	err     error

	projects []string
	claims   []mobilesession.Claims
}

func (f *fakeMembership) Allow(_ context.Context, project string, claims mobilesession.Claims) (mobilesession.Verdict, error) {
	f.projects = append(f.projects, project)
	f.claims = append(f.claims, claims)
	if f.err != nil {
		return mobilesession.Verdict{}, f.err
	}
	return f.verdict, nil
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

// fakeDeviceStore is this route's device-node seam: it records every node it
// was asked to write and every uid it was asked to remove, in order, because
// what the cleanup tests read is that a failed restore left neither.
type fakeDeviceStore struct {
	createErr error
	removeErr error

	created map[string]MobileDevice
	order   []string
	removed []string
}

func (f *fakeDeviceStore) Create(_ context.Context, project, uid string, dev MobileDevice) error {
	f.order = append(f.order, "create "+uid)
	if f.createErr != nil {
		return f.createErr
	}
	if f.created == nil {
		f.created = map[string]MobileDevice{}
	}
	f.created[uid] = dev
	return nil
}

func (f *fakeDeviceStore) Remove(_ context.Context, project, uid string) error {
	f.order = append(f.order, "remove "+uid)
	f.removed = append(f.removed, uid)
	return f.removeErr
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
	claims     *fakeClaimStore
	refresh    *fakeRefreshStore
	minter     *fakeMinter
	devices    *fakeDeviceStore
	projects   *fakePolicy

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
		membership: &fakeMembership{verdict: mobilesession.Verdict{Allowed: true, Role: access.RoleOperator}},
		claims:     &fakeClaimStore{},
		refresh:    &fakeRefreshStore{},
		minter:     &fakeMinter{},
		devices:    &fakeDeviceStore{},
		projects:   &fakePolicy{shared: &projectpolicy.Policy{Version: projectpolicy.Version}, sharedFound: true},
	}
	auditor, buf := newAudit(t)
	f.audit = buf
	var logs bytes.Buffer
	f.log = &logs
	h, err := NewMobileRestore(MobileRestore{
		Project: MobileProjectConfig{
			ProjectID:         "the-project",
			Endpoint:          restoreEndpoint,
			DatabaseURL:       "https://the-project.firebaseio.invalid",
			APIKey:            "the-api-key",
			ConsumerPublicKey: "the-consumer-key",
		},
		Policy:     f.projects,
		Verifier:   f.verifier,
		Membership: f.membership,
		Claims:     f.claims,
		Refresh:    f.refresh,
		Minter:     f.minter,
		Devices:    f.devices,
		Audit:      auditor,
		Log:        slog.New(slog.NewJSONHandler(&logs, nil)),
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
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

	rec := f.restore(t, restoreBody("the-challenge"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("a restore against a strict project is answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if code := refusalCode(t, rec); code != mobilesession.CodeStrictNewDeviceNeedsQR {
		t.Errorf("the refusal is %q, want %q", code, mobilesession.CodeStrictNewDeviceNeedsQR)
	}
	// The allow path did not run: no node, no family, no token. This is what
	// "refuses before the allow path exists" means, and it is the half of the
	// test that a handler which refused AFTER admitting would fail.
	if len(f.devices.created) != 0 {
		t.Errorf("the refused restore wrote %d device nodes, want none", len(f.devices.created))
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
	if got := auditField(lines[0], "status"); got != ErrorRequestRefused {
		t.Errorf("the audit line's status is %q, want %q: the contract's refusal codes are not statuses of this service's audit", got, ErrorRequestRefused)
	}
}
