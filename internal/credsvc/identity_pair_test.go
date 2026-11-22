package credsvc

// LM4's tests: the two identity routes a person asks (R14, D3126).
//
// They are not reachable through ServeHTTP yet — C1 mounts the stub for all
// five paths and I1 is the task that binds the real handlers — so these tests
// call the handlers the way I1 will, directly and with a signer. That is also
// what lets a test read the token's own payload back and say what the service
// derived, which is the whole of what these two routes decide.
//
// Nothing here is a credential: the signer is the fixture's own RSA key, the
// tokens are minted at run time, and the account names are names.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// pairSignerEmail is the account these routes mint as: the installation's
// signer account, which is the one the Firebase root holds the minter row for
// (D3121). It is a name and not a credential.
const pairSignerEmail = "fugaro-token-signer@" + testFirebase + ".iam.gserviceaccount.com"

// pairSigner is the signer the routes are handed: the token package's own
// Signer interface over the fixture's test key, so the token a route answers is
// a real RS256 JWT whose payload a test reads back. It is deliberately not an
// IAM signer — no test in this repository reaches Google's IAM endpoint — and
// the routes' own decisions, the uid and the claims, are exactly what the
// payload shows.
type pairSigner struct{ email string }

func (p pairSigner) Email() string { return p.email }

func (p pairSigner) SignJWT(_ context.Context, payload []byte) (string, error) {
	head, err := json.Marshal(map[string]any{"alg": "RS256", "kid": testJobKid, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, testKey(), crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// pairRoutes is one installation's service with the two identity routes wired
// the way I1 wires them: the caller class is the launcher roster (the people
// who may pair today, R14), the issuer's own configuration holds the signer
// account, and the signer the routes mint with is this fixture's.
type pairRoutes struct {
	*svc
	signer pairSigner
}

// newPairRoutes is the fixture. An empty roster is a class that admits nobody,
// which is the fail-closed reading every class list in this service has.
func newPairRoutes(t *testing.T, launchers ...string) *pairRoutes {
	t.Helper()
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:          testProject,
			FirebaseProject:  testFirebase,
			Audience:         testAudience,
			JobAccounts:      []string{testJobSA},
			Keys:             s.keys,
			JobKeys:          s.jobKeys,
			Outcomes:         RTDBOutcomes{DB: s.db},
			LauncherAccounts: launchers,
			Now:              func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's launcher class cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.Identity = IdentityConfig{Signer: pairSignerEmail}
	})
	return &pairRoutes{svc: s, signer: pairSigner{email: pairSignerEmail}}
}

// pairCall drives one of the two routes the way I1 will: the handler itself,
// with the signer the installation was configured with, and a recorder for the
// answer. The call carries the body the test writes and whatever headers it
// adds.
func (p *pairRoutes) pairCall(t *testing.T, path, body string, mutate ...func(*http.Request)) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentTypeJSON)
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	switch path {
	case identitywire.PairingPath:
		p.server.identityPairing(context.Background(), rec, req, p.signer)
	case identitywire.DevicePath:
		p.server.identityDevice(context.Background(), rec, req, p.signer)
	default:
		t.Fatalf("no identity route is served at %q", path)
	}
	return rec, rec.Body.Bytes()
}

// personBearer is the person's own Google ID token in the bearer: the one
// credential these two routes read, and the class both of them admit.
func (p *pairRoutes) personBearer() func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+p.launcherToken()) }
}

// serviceAccountBearer is a JOB's own Google ID token. It is a service
// account, which is a class of the ticket route's and never a person's (LM3):
// nothing a service account does is a member asking for a phone's token.
func (p *pairRoutes) serviceAccountBearer() func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+p.jobToken()) }
}

// tokenPayload is the token's own payload, read back from the answer. The
// signature is not checked here: the signer is the fixture's and the token
// package has already held the token to the uid it minted (MintPairing's
// read-back). The token is never printed, as no answer of these routes is.
func tokenPayload(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatal("the answer's token is not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("the token's payload is not base64url: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the token's payload is not JSON: %v", err)
	}
	return out
}

// claimNumber reads one numeric claim, which is how every timestamp in a
// token's payload arrives.
func claimNumber(t *testing.T, claims map[string]any, name string) int64 {
	t.Helper()
	v, ok := claims[name].(float64)
	if !ok {
		t.Fatalf("the token carries no %s claim: %v", name, claims)
	}
	return int64(v)
}

// customClaims is the token's one custom claim set (the `claims` object the
// deployed rules read), or an empty one.
func customClaims(claims map[string]any) map[string]any {
	out, _ := claims["claims"].(map[string]any)
	return out
}

// answerSummary is all a test may print of an answer: the status, and the
// contract's error code when the answer is a refusal. The body itself is never
// printed — an answer of these two routes holds a token, and a test's own
// failure output is no more a place for one than the service's log is.
func answerSummary(rec *httptest.ResponseRecorder) string {
	if e, err := ReadWireError(rec.Code, rec.Body.Bytes()); err == nil {
		return fmt.Sprintf("%d %s", rec.Code, e.Error)
	}
	return fmt.Sprintf("%d", rec.Code)
}

// TestPairingTokenForVerifiedPerson is LM4's acceptance for the pairing route:
// a verified person on the roster is answered a pairing token whose uid, claims
// and lifetime the service derived, and the answer's own id is that uid — the
// handle the CLI writes the pairing node at for a thing it did not mint.
//
// Four facts are read back out of the token itself, because they are what makes
// the token the service's and not the caller's: the uid it names begins p~
// (token.PairingPrefix — never a run's r~, never a device's m~), its one custom
// claim is pu = that uid (all the deployed rules read of a pairing token), it
// is issued by the installation's signer account, and its exp is the ceiling
// from the fixture's clock — the same instant the answer reports, because
// PairingLifetime is the minter's answer and not a second reading of it.
func TestPairingTokenForVerifiedPerson(t *testing.T) {
	p := newPairRoutes(t, testLauncherAccount)
	rec, body := p.pairCall(t, identitywire.PairingPath, "{}", p.personBearer())
	if rec.Code != http.StatusOK {
		t.Fatalf("the pairing is answered %s, want 200", answerSummary(rec))
	}
	var ans identitywire.PairingResponse
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("the pairing's answer is not one this contract reads: %v", err)
	}
	if !strings.HasPrefix(ans.Pairing, token.PairingPrefix) {
		t.Errorf("the pairing id %q does not begin %q: the uid is the minter's", ans.Pairing, token.PairingPrefix)
	}
	claims := tokenPayload(t, ans.Token)
	if got := claims["uid"]; got != ans.Pairing {
		t.Errorf("the token names the uid %v, want the answered pairing %q: the uid is the service's and the answer reports it", got, ans.Pairing)
	}
	if extra := customClaims(claims); len(extra) != 1 || extra["pu"] != ans.Pairing {
		t.Errorf("the token's claims are %v, want exactly pu = %q: it is the one claim the deployed rules read of a pairing token", extra, ans.Pairing)
	}
	if got := claims["iss"]; got != pairSignerEmail {
		t.Errorf("the token is issued by %v, want the installation's signer %q: the caller's own account is not the issuer", got, pairSignerEmail)
	}
	want := testNow().Add(token.PairingLifetime(token.PairingMaxLifetime))
	if !ans.ExpiresAt.Equal(want) {
		t.Errorf("the answer expires at %s, want %s: the lifetime is the minter's, reported and not recomputed", ans.ExpiresAt, want)
	}
	if got := claimNumber(t, claims, "exp"); got != want.Unix() {
		t.Errorf("the token's exp is %d, want %d: the answer and the token cannot drift apart", got, want.Unix())
	}

	// The same route with a body that names a uid: the request is the shape
	// this contract refuses, and no token of any kind is issued for it. A
	// route that read the body into a struct of its own would serve this call
	// under a uid the caller chose, which is the one thing R14 forbids.
	rec, answer := p.pairCall(t, identitywire.PairingPath, `{"uid":"chosen-by-the-caller"}`, p.personBearer())
	if rec.Code != StatusRequestRefused {
		t.Fatalf("a pairing body that names a uid is answered %s, want %d", answerSummary(rec), StatusRequestRefused)
	}
	if e := refusal(t, rec.Code, answer); e.Error != ErrorRequestRefused {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorRequestRefused)
	}
}

// TestDeviceTokenTakesNoCallerClaim is LM4's red step and the device half of
// R14: the request has no field a claim could come from, and the token the
// route answers takes none of one.
//
// The red step is the first half. A body that names a uid, or a claim set, is
// refused by the strict decode: the refusal names the FIELD and never what the
// call sent, and no token is minted at all — which is the property a handler
// that dropped the unknown field, or that decoded into a struct with a uid on
// it, would fail on a body carrying one.
//
// The second half is the token the route DOES mint: the uid it names is a
// freshly drawn m~ one, the answer's Device is that uid, and the token's claims
// are mu = uid and nothing else — no run's claims (fb, fpr, fx, fp), which is
// the whole difference between a device and a run identity.
func TestDeviceTokenTakesNoCallerClaim(t *testing.T) {
	p := newPairRoutes(t, testLauncherAccount)
	for _, body := range []string{
		`{"uid":"chosen-by-the-caller"}`,
		`{"claims":{"mu":"chosen-by-the-caller"}}`,
		`{"uid":"chosen-by-the-caller","claims":{"mu":"chosen-by-the-caller"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			rec, answer := p.pairCall(t, identitywire.DevicePath, body, p.personBearer())
			if rec.Code != StatusRequestRefused {
				t.Fatalf("a body that names a claim is answered %s, want %d", answerSummary(rec), StatusRequestRefused)
			}
			e := refusal(t, rec.Code, answer)
			if e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q", e.Error, ErrorRequestRefused)
			}
			if !strings.Contains(e.Message, "uid") && !strings.Contains(e.Message, "claims") {
				t.Errorf("the refusal names neither field it refused: %s", e.Message)
			}
			if strings.Contains(string(answer), "chosen-by-the-caller") {
				t.Fatalf("the refusal echoes what the call sent: %s", answer)
			}
		})
	}
	// Nothing above minted: the service's own log is where a minted token is
	// recorded, and it holds no issuance of either kind.
	if got := p.logs.String(); strings.Contains(got, "token was issued") {
		t.Fatalf("a refused call minted a token: %s", got)
	}

	rec, answer := p.pairCall(t, identitywire.DevicePath, "{}", p.personBearer())
	if rec.Code != http.StatusOK {
		t.Fatalf("the device ask is answered %s, want 200", answerSummary(rec))
	}
	var ans identitywire.DeviceResponse
	if err := json.Unmarshal(answer, &ans); err != nil {
		t.Fatalf("the device's answer is not one this contract reads: %v", err)
	}
	if !strings.HasPrefix(ans.Device, "m~") {
		t.Errorf("the device id %q does not begin m~: the uid is the minter's (token.MintDevice's deviceUIDPrefix)", ans.Device)
	}
	claims := tokenPayload(t, ans.Token)
	if got := claims["uid"]; got != ans.Device {
		t.Errorf("the token names the uid %v, want the answered device %q: the uid is the service's and the answer reports it", got, ans.Device)
	}
	extra := customClaims(claims)
	if len(extra) != 1 || extra["mu"] != ans.Device {
		t.Errorf("the token's claims are %v, want exactly mu = %q: a device token carries the device's own claim and no other", extra, ans.Device)
	}
	for _, runClaim := range []string{"fb", "fpr", "fx", "fp"} {
		if _, ok := extra[runClaim]; ok {
			t.Errorf("the device token carries the run claim %q: a token that carried a run's claims would be a run identity and a device at once (MintDevice)", runClaim)
		}
	}
	if got := claims["iss"]; got != pairSignerEmail {
		t.Errorf("the token is issued by %v, want the installation's signer %q", got, pairSignerEmail)
	}
	if want := testNow().Add(token.Lifetime); !ans.ExpiresAt.Equal(want) {
		t.Errorf("the answer expires at %s, want %s: a device token's exp is the mint's own Lifetime", ans.ExpiresAt, want)
	}
}

// TestPairRoutesRefuseServiceAccounts: the class of BOTH routes is a person's,
// and a service account is not one — a job's own Google ID token is a class of
// the ticket route's (LM3) and buys nothing here.
//
// The refusal is the contract's caller_not_admitted and not "not served": an
// issuer IS configured on this installation, and a service account told "not
// served" would be told to fall back to signing locally with the people's
// grant, which is the one reading R7's bridge must not be reachable through.
// Both routes are asked, because the class is the two routes' and a half-wired
// service would leave the second one open.
func TestPairRoutesRefuseServiceAccounts(t *testing.T) {
	p := newPairRoutes(t, testLauncherAccount)
	for _, path := range []string{identitywire.PairingPath, identitywire.DevicePath} {
		t.Run(path, func(t *testing.T) {
			rec, body := p.pairCall(t, path, "{}", p.serviceAccountBearer())
			if rec.Code != StatusUnverified {
				t.Fatalf("a service account is answered %s, want %d", answerSummary(rec), StatusUnverified)
			}
			if e := refusal(t, rec.Code, body); e.Error != identitywire.RefusalCallerNotAdmitted {
				t.Errorf("the refusal is %q, want %q: the caller is refused by the class and not by an unconfigured issuer", e.Error, identitywire.RefusalCallerNotAdmitted)
			}
			var wire map[string]any
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatalf("the refusal is not one this contract reads: %v", err)
			}
			if _, ok := wire["token"]; ok {
				t.Fatalf("the refusal carries a token field: %s", body)
			}
		})
	}
	if got := p.logs.String(); strings.Contains(got, "token was issued") {
		t.Fatalf("a refused caller was answered a token: %s", got)
	}
}

// TestPairRoutesTakeNoRunIdentity: the two routes' door, and the sibling of
// TestGrantTakesNoRunIdentity (slip_test.go) one class along. A pairing and a
// device token are asked for by a PERSON, before any run of theirs exists, so a
// caller presenting a run identity — or naming a job — is a call of a shape
// these routes do not have. It is refused before the body is read and before a
// caller is verified, so nothing is minted by it and no audit line is written.
// Both headers on BOTH routes, because the shape is each route's own and a
// half-guarded pair would leave one of them a way in for a run.
func TestPairRoutesTakeNoRunIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra func(*http.Request)
	}{
		{"a run identity", func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity-token") }},
		{"a job name", func(r *http.Request) { r.Header.Set(HeaderRunJob, "fugaro-a-job") }},
	} {
		for _, path := range []string{identitywire.PairingPath, identitywire.DevicePath} {
			t.Run(tc.name+" at "+path, func(t *testing.T) {
				p := newPairRoutes(t, testLauncherAccount)
				rec, answer := p.pairCall(t, path, "{}", p.personBearer(), tc.extra)
				if rec.Code != StatusUnverified {
					t.Fatalf("a call presenting %s is answered %s, want %d", tc.name, answerSummary(rec), StatusUnverified)
				}
				if e := refusal(t, rec.Code, answer); e.Error != ErrorUnverifiedRunIdentity {
					t.Errorf("the refusal is %q, want %q: it is the shape of the call and not the caller's class", e.Error, ErrorUnverifiedRunIdentity)
				}
				if got := p.logs.String(); strings.Contains(got, "token was issued") {
					t.Fatalf("a refused call minted a token: %s", got)
				}
			})
		}
	}
}

// TestPairRoutesAnswerNotServedWithNoIssuer is the routes' own half of R7: an
// installation whose issuer is not configured is answered the contract's "not
// served" — the code the CLI bridges on and signs a run's identity locally
// against — and not a caller refusal, because there is no signer to verify a
// caller FOR. Both routes are asked, each through its own handler, so the
// answer is each route's and not one handler's read twice. It is what the stub
// answers today and what I1's handlers must go on answering, and it is asked
// with a caller who WOULD be admitted, so that the answer cannot be the
// class's.
func TestPairRoutesAnswerNotServedWithNoIssuer(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	for _, route := range []struct {
		path    string
		handler func(context.Context, http.ResponseWriter, *http.Request, token.Signer)
	}{
		{identitywire.PairingPath, s.server.identityPairing},
		{identitywire.DevicePath, s.server.identityDevice},
	} {
		t.Run(route.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", contentTypeJSON)
			req.Header.Set("Authorization", "Bearer "+s.launcherToken())
			rec := httptest.NewRecorder()
			// No signer and no identity configuration: the state I1 answers
			// this route in before any issuer row exists.
			route.handler(context.Background(), rec, req, nil)
			if rec.Code != StatusUnavailable {
				t.Fatalf("the call is answered %s, want %d", answerSummary(rec), StatusUnavailable)
			}
			e := refusal(t, rec.Code, rec.Body.Bytes())
			if e.Error != identitywire.RefusalNotServed {
				t.Errorf("the refusal is %q, want %q: it is the code the CLI bridges on", e.Error, identitywire.RefusalNotServed)
			}
			if !strings.Contains(e.Message, "fugaro update") {
				t.Errorf("the refusal does not name the fix: %s", e.Message)
			}
		})
	}
}
