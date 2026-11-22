package cli

// `fugaro pair` asks the installation's credentials service for its two tokens
// (LM10): these tests hold the command to that ask, to R7's bridge and to what
// its refusals say.
//
// The service is a TLS server and the URL the installation's config carries is
// its own, because the loader holds credentials_service.url to https with a
// host and nothing else: a test cannot point an installation at a plain server,
// and the config the command reads is a real one. The transport that trusts this
// server's certificate is installed as the process's for the length of the test
// — the call's own client is built from http.DefaultTransport (slipClient) —
// and the identity mint is stubbed, because no test may run gcloud.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// pairIssuerToken and pairDeviceToken are what the fixture's service answers
// where a route mints. Each is three dotted parts, which is the shape this
// command carries, and neither is a credential: nothing signs them and no
// running installation would take one.
const (
	pairIssuerToken = "fake-pairing-token.not.signed"
	pairDeviceToken = "fake-device-token.not.signed"
)

// pairIssuerIdentity is what the stubbed mint answers: the operator's own
// Google ID token as this test has it. It is not a credential either.
const pairIssuerIdentity = "fake-identity-token-not-a-real-credential"

// pairServicePairingUID and pairServiceDeviceUID are the ids the fixture's
// service answers with, in the two shapes internal/budget/token draws: 32 bytes
// behind p~, 16 behind m~. Neither is the id this machine's own draw would
// produce (pairFixedRand), so a test that seeds the phone's request at one of
// them and finishes holds the command to the service's ids rather than to its
// own — a bridge would write the whole pairing somewhere else.
var (
	pairServicePairingUID = token.PairingPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, pairUIDBytes))
	pairServiceDeviceUID  = pairDeviceUIDPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x6b}, pairDeviceUIDBytes))
)

// pairServiceExpiry is the expiry both the fixture's answers carry. It is not
// the figure the command computes for a wait of its own (now + the pairing
// token's lifetime), so a test can tell the service's answer from the local
// one.
var pairServiceExpiry = pairT0.Add(3 * time.Minute)

// pairAsked is one call the service received: the path, the credential it was
// presented and the body it carried.
type pairAsked struct {
	Path, Authorization, Body string
}

// pairAnswer is one route's answer: a status and a body. A status of zero is a
// 200.
type pairAnswer struct {
	Status int
	Body   string
}

// pairService is an installation's credentials service as a pairing test has
// it.
type pairService struct {
	*httptest.Server
	answers map[string]pairAnswer

	mu    sync.Mutex
	calls []pairAsked
}

// newPairService starts one and returns it. A route the answers do not name is
// answered 404, which is what a service of the release behind answers an
// unknown path — C1 pins that answer as the bridge's second trigger, so the
// zero answers map is a service with no identity half at all.
func newPairService(t *testing.T, answers map[string]pairAnswer) *pairService {
	t.Helper()
	s := &pairService{answers: answers}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)

	prevTransport, prevIdentity, prevSelf := http.DefaultTransport, pairIdentity, selfCommand
	http.DefaultTransport = s.Client().Transport
	pairIdentity = func(context.Context, string) (string, error) { return pairIssuerIdentity, nil }
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() {
		http.DefaultTransport, pairIdentity, selfCommand = prevTransport, prevIdentity, prevSelf
	})
	return s
}

// serviceURL is the address this installation's config carries for the service:
// the same server, over the https the loader holds the block to.
func (s *pairService) serviceURL() string {
	return strings.Replace(s.URL, "http://", "https://", 1)
}

// edit points the fixture's config at this service, which is what makes the
// command ask one at all: R7's first bridge answer is the block's absence.
func (s *pairService) edit(cfg *string, _ *pairFixture) {
	*cfg += "credentials_service: { url: " + s.serviceURL() + " }\n"
}

func (s *pairService) asked() []pairAsked {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pairAsked(nil), s.calls...)
}

func (s *pairService) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls = append(s.calls, pairAsked{Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: string(body)})
	ans, known := s.answers[r.URL.Path]
	s.mu.Unlock()
	if !known {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if ans.Status != 0 {
		w.WriteHeader(ans.Status)
	}
	_, _ = io.WriteString(w, ans.Body)
}

// pairServiceAnswers is what the service answers where both routes mint.
func pairServiceAnswers() map[string]pairAnswer {
	expires := pairServiceExpiry.Format(time.RFC3339)
	return map[string]pairAnswer{
		identitywire.PairingPath: {Body: `{"token":"` + pairIssuerToken + `","pairing":"` + pairServicePairingUID + `","expires_at":"` + expires + `"}`},
		identitywire.DevicePath:  {Body: `{"token":"` + pairDeviceToken + `","device":"` + pairServiceDeviceUID + `","expires_at":"` + expires + `"}`},
	}
}

// TestPairAsksTheIssuer: a pairing on an installation whose credentials service
// mints its two tokens asks it, presents the operator's own identity, sends the
// contract's ask with no field in it, and writes what the service answered —
// with this machine's own signer never asked for a token at all.
func TestPairAsksTheIssuer(t *testing.T) {
	svc := newPairService(t, pairServiceAnswers())
	f := newPairFixture(t, pairMe, svc.edit)
	f.pairSeedRequest(pairServicePairingUID, pairKey)

	out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
	// The red step first, and before the error is judged: a command that minted
	// locally has nothing to say about the rest of this test, and the one fact
	// that tells the two apart is this machine's own signer never being asked.
	if calls := f.iam.SignCalls(); len(calls) != 0 {
		t.Fatalf("the pairing minted %d tokens with this machine's own signer (%v), want none: both tokens are the service's (LM10's red step)", len(calls), calls)
	}
	if err != nil {
		t.Fatalf("the pairing the service mints for: %v", err)
	}

	asked := svc.asked()
	if len(asked) != 2 || asked[0].Path != identitywire.PairingPath || asked[1].Path != identitywire.DevicePath {
		t.Fatalf("the service was asked %v, want the pairing route and then the device route", asked)
	}
	for _, a := range asked {
		if a.Authorization != "Bearer "+pairIssuerIdentity {
			t.Fatalf("the call to %s was presented %q, want this operator's own Google ID token", a.Path, a.Authorization)
		}
		if a.Body != "{}" {
			t.Fatalf("the call to %s carried %q, want the contract's ask with no field: the pairing id and the device's claims are the service's (R14)", a.Path, a.Body)
		}
	}

	events := pairLines(t, out)
	if len(events) == 0 || events[0].Event != pairEventQR {
		t.Fatalf("the stream starts with %v, want the QR event", events)
	}
	var qr pairQR
	if err := json.Unmarshal([]byte(events[0].Payload), &qr); err != nil {
		t.Fatalf("the QR payload does not decode: %v", err)
	}
	if qr.Token != pairIssuerToken || qr.UID != pairServicePairingUID {
		t.Fatalf("the QR payload carries token %q and id %q, want the service's %q and %q", qr.Token, qr.UID, pairIssuerToken, pairServicePairingUID)
	}
	if qr.ExpiresAt != pairServiceExpiry.Format(time.RFC3339) {
		t.Fatalf("the QR payload reports expires_at %q, want the service's own figure %q", qr.ExpiresAt, pairServiceExpiry.Format(time.RFC3339))
	}

	allowlist, ok := f.db.Value("config/admin/devices").(map[string]any)
	if !ok || len(allowlist) != 1 {
		t.Fatalf("the allowlist is %v, want exactly the one device the pairing wrote", f.db.Value("config/admin/devices"))
	}
	if _, ok := allowlist[pairServiceDeviceUID]; !ok {
		t.Fatalf("the device node is written at %v, want the service's own id %s", allowlist, pairServiceDeviceUID)
	}
	item, _ := f.pairStoredKey(t)
	if want := pairItemName("aurora", pairServiceDeviceUID); item != want {
		t.Fatalf("the keychain item is %q, want %q: the item is named after the device the service minted", item, want)
	}
	plain := pairOpenDelivery(t, pairServicePairingUID, f.pairSealedDelivery(t, pairServicePairingUID))
	var delivery struct {
		Device struct {
			UID   string `json:"uid"`
			Token string `json:"token"`
		} `json:"device"`
	}
	if err := json.Unmarshal(plain, &delivery); err != nil {
		t.Fatalf("the delivery the phone opens does not decode: %v", err)
	}
	if delivery.Device.UID != pairServiceDeviceUID || delivery.Device.Token != pairDeviceToken {
		t.Fatalf("the delivery carries device %+v, want the service's own id %q and token %q", delivery.Device, pairServiceDeviceUID, pairDeviceToken)
	}
}

// TestPairBridgesWhenRouteNotServed: the three answers R7 bridges on, and
// nothing else — the service's own "this revision holds no issuer", a service
// of the release behind answering an unknown path, and an installation with no
// service block at all. Each ends in the local mint this command made before
// the issuer existed, with both tokens local, and the command asks the service
// wherever it has one.
func TestPairBridgesWhenRouteNotServed(t *testing.T) {
	notServed := pairAnswer{Status: http.StatusServiceUnavailable, Body: `{"error":"` + identitywire.RefusalNotServed + `"}`}
	cases := []struct {
		name    string
		service bool
		answers map[string]pairAnswer
	}{
		{
			name:    "the service holds no issuer",
			service: true,
			answers: map[string]pairAnswer{identitywire.PairingPath: notServed, identitywire.DevicePath: notServed},
		},
		{name: "the service is a release behind", service: true, answers: map[string]pairAnswer{}},
		{name: "no service is configured", service: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uid := pairFixedRand(t)
			var edits []func(*string, *pairFixture)
			var svc *pairService
			if tc.service {
				svc = newPairService(t, tc.answers)
				edits = append(edits, svc.edit)
			}
			f := newPairFixture(t, pairMe, edits...)
			f.pairSeedRequest(uid, pairKey)

			out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
			if err != nil {
				t.Fatalf("the pairing this answer must bridge for: %v", err)
			}
			if calls := f.iam.SignCalls(); len(calls) != 2 {
				t.Fatalf("the bridge minted %d tokens with this machine's own signer, want both of them (the pairing's and the device's)", len(calls))
			}
			events := pairLines(t, out)
			last := events[len(events)-1]
			if last.Event != pairEventDone || last.Document == nil {
				t.Fatalf("the stream ends with %v, want the pairing's own document", last)
			}
			deviceUID := last.Document.Device.UID
			if !strings.HasPrefix(deviceUID, pairDeviceUIDPrefix) {
				t.Fatalf("the device id is %q, want one this machine's own draw minted (%s…)", deviceUID, pairDeviceUIDPrefix)
			}
			allowlist, ok := f.db.Value("config/admin/devices").(map[string]any)
			if !ok {
				t.Fatalf("the allowlist is %v, want the device node the bridged pairing wrote", f.db.Value("config/admin/devices"))
			}
			if _, ok := allowlist[deviceUID]; !ok {
				t.Fatalf("the device node is written at %v, want the id the bridge minted %s", allowlist, deviceUID)
			}
			if svc == nil {
				return
			}
			asked := svc.asked()
			if len(asked) != 2 || asked[0].Path != identitywire.PairingPath || asked[1].Path != identitywire.DevicePath {
				t.Fatalf("the service was asked %v, want both routes asked before the bridge took over", asked)
			}
		})
	}
}

// TestPairRefusalNamesTheFix: every way the ask can end, told as the sentence
// that names what to run — and none of them a bridge, because a local mint
// under a refusal is exactly the downgrade R7 forbids.
func TestPairRefusalNamesTheFix(t *testing.T) {
	cases := []struct {
		name    string
		answers map[string]pairAnswer
		// serviceURL, where the test does not stand the fixture's own service up.
		serviceURL string
		// minter refuses every signJwt with IAM's own 403, which is the bridge's
		// own failure.
		minter bool
		// seed is the uid the phone's request node is written at, for a row whose
		// pairing ask succeeds: the command reads the phone's half at the uid the
		// service answered with, so a row that means to reach its wait names that
		// uid here. Empty means the fixture's own draw, which is the uid the
		// bridged local mint produces and which the rows that refuse before the
		// wait never read.
		seed  string
		code  string
		exit  int
		wants []string
	}{
		{
			name:    "the service does not admit this caller",
			answers: map[string]pairAnswer{identitywire.PairingPath: {Status: http.StatusForbidden, Body: `{"error":"` + identitywire.RefusalCallerNotAdmitted + `"}`}},
			code:    pairRefusedIssuer,
			exit:    ExitUserError,
			wants:   []string{"fugaro update", identitywire.RefusalCallerNotAdmitted},
		},
		{
			name:       "the service cannot be reached",
			serviceURL: "https://127.0.0.1:1",
			code:       pairRefusedIssuerAnswer,
			exit:       ExitRemoteError,
			wants:      []string{"fugaro update"},
		},
		{
			name: "the service answers an id this command will not write under",
			answers: map[string]pairAnswer{identitywire.PairingPath: {
				Body: `{"token":"` + pairIssuerToken + `","pairing":"p~/../../etc","expires_at":"` + pairServiceExpiry.Format(time.RFC3339) + `"}`,
			}},
			code:  pairRefusedIssuerAnswer,
			exit:  ExitRemoteError,
			wants: []string{"fugaro update", "pairing id"},
		},
		{
			// The pairing ask is answered, so the run reaches the phone's key and
			// the device ask: what must not survive it is the id, which becomes the
			// device node's key (db.Patch(rules.PathDevices), where a '/' inside a
			// key is another path of the write) and the keychain item's name.
			name: "the service answers a device id this command will not write under",
			answers: map[string]pairAnswer{
				identitywire.PairingPath: {Body: `{"token":"` + pairIssuerToken + `","pairing":"` + pairServicePairingUID + `","expires_at":"` + pairServiceExpiry.Format(time.RFC3339) + `"}`},
				identitywire.DevicePath:  {Body: `{"token":"` + pairDeviceToken + `","device":"m~abc/role","expires_at":"` + pairServiceExpiry.Format(time.RFC3339) + `"}`},
			},
			seed:  pairServicePairingUID,
			code:  pairRefusedIssuerAnswer,
			exit:  ExitRemoteError,
			wants: []string{"fugaro update", "device id"},
		},
		{
			// The token half of the same check: a QR code carries this straight to
			// a phone, which can only answer "invalid token", so the ask's other
			// answer field is refused here rather than there.
			name: "the service answers a token that is not a pairing token",
			answers: map[string]pairAnswer{identitywire.PairingPath: {
				Body: `{"token":"not-a-firebase-custom-token","pairing":"` + pairServicePairingUID + `","expires_at":"` + pairServiceExpiry.Format(time.RFC3339) + `"}`,
			}},
			code:  pairRefusedIssuerAnswer,
			exit:  ExitRemoteError,
			wants: []string{"fugaro update", "not a Firebase custom token"},
		},
		{
			name:   "the local mint has no minter role",
			minter: true,
			code:   pairRefusedMinter,
			exit:   ExitUserError,
			wants:  []string{"fugaro update", "fugaroTokenMinter", pairSigner},
		},
	}
	// Every sentence below names what to run, and the binary is the one this
	// test's own command would print.
	prevSelf := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = prevSelf })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var edits []func(*string, *pairFixture)
			prevIdentity := pairIdentity
			switch {
			case tc.serviceURL != "":
				// The address is the installation's own and nothing answers it:
				// the mint is stubbed so that what fails is the call and not
				// this machine's gcloud.
				url := tc.serviceURL
				edits = append(edits, func(cfg *string, _ *pairFixture) {
					*cfg += "credentials_service: { url: " + url + " }\n"
				})
				pairIdentity = func(context.Context, string) (string, error) { return pairIssuerIdentity, nil }
			case tc.answers != nil:
				edits = append(edits, newPairService(t, tc.answers).edit)
			}
			t.Cleanup(func() { pairIdentity = prevIdentity })
			uid := pairFixedRand(t)
			if tc.seed != "" {
				uid = tc.seed
			}
			f := newPairFixture(t, pairMe, edits...)
			if tc.minter {
				f.iam.Refuse(403, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission 'iam.serviceAccounts.signJwt' denied")
			}
			f.pairSeedRequest(uid, pairKey)

			out, _, err := pairRunIn(t, token.Fingerprint(pairKeyBytes), "--json", "--name", "phone-a")
			if err == nil {
				t.Fatalf("the pairing went through, want the refusal that names the fix")
			}
			if got := ExitCode(err); got != tc.exit {
				t.Fatalf("exit %d for %v, want %d", got, err, tc.exit)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal %q does not name %q", err, want)
				}
			}
			events := pairLines(t, out)
			last := events[len(events)-1]
			if last.Event != pairEventRefused || last.Code != tc.code {
				t.Fatalf("the stream ends with %v, want a refusal with the code %q", last, tc.code)
			}
			if v := f.db.Value("config/admin/devices"); v != nil {
				t.Fatalf("a refused pairing wrote the device allowlist: %v", v)
			}
			if v := f.db.Value(pairPathDelivery(uid)); v != nil {
				t.Fatalf("a refused pairing wrote a delivery: %v", v)
			}
			if len(f.keys.Keys) != 0 {
				t.Fatalf("a refused pairing wrote the keychain: %v", f.keys.Keys)
			}
			if v := f.db.Value(pairPathRequest(uid)); v == nil {
				t.Fatalf("a refused pairing deleted the phone's own request node")
			}
			if tc.minter {
				return
			}
			if calls := f.iam.SignCalls(); len(calls) != 0 {
				t.Fatalf("a refused ask signed %d tokens locally (%v), want none: a refusal is never a bridge", len(calls), calls)
			}
		})
	}
}

// TestPairIdentityCallReadsTheContractsAnswer: the call itself, held to what it
// will and will not read back — the two routes answer what C1's bodies say, a
// body with a field this release does not know is refused rather than half
// read, and a 404 or the service's own "not served" is the bridge's trigger
// while every other refusal is a refusal.
func TestPairIdentityCallReadsTheContractsAnswer(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{
			name: "the contract's answer",
			body: `{"token":"` + pairIssuerToken + `","pairing":"` + pairServicePairingUID + `","expires_at":"` + pairServiceExpiry.Format(time.RFC3339) + `"}`,
		},
		{
			name:   "not served",
			status: http.StatusServiceUnavailable,
			body:   `{"error":"` + identitywire.RefusalNotServed + `"}`,
			want:   errPairIssuerAbsent,
		},
		{
			name:   "a path this release does not serve",
			status: http.StatusNotFound,
			want:   errPairIssuerAbsent,
		},
		{
			name:   "a route asked with another method",
			status: http.StatusMethodNotAllowed,
			want:   errPairIssuerAbsent,
		},
		{
			name:   "an admitted caller's ask refused",
			status: http.StatusForbidden,
			body:   `{"error":"` + identitywire.RefusalIdentityRefused + `"}`,
			want:   errPairIssuerRefused,
		},
		{
			name:   "a refusal this release cannot read",
			status: http.StatusForbidden,
			body:   `not json`,
			want:   errPairIssuerRefused,
		},
		{
			name: "a field this release does not know",
			body: `{"token":"` + pairIssuerToken + `","pairing":"` + pairServicePairingUID + `","expires_at":"` + pairServiceExpiry.Format(time.RFC3339) + `","uid":"somebody elses"}`,
			want: errPairIssuerAnswer,
		},
		{
			name: "a body that is not this contract's",
			body: `{"token":"` + pairIssuerToken + `"`,
			want: errPairIssuerAnswer,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answer := pairAnswer{Status: tc.status, Body: tc.body}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if answer.Status != 0 {
					w.WriteHeader(answer.Status)
				}
				_, _ = io.WriteString(w, answer.Body)
			}))
			defer srv.Close()

			prevIdentity := pairIdentity
			pairIdentity = func(context.Context, string) (string, error) { return pairIssuerIdentity, nil }
			t.Cleanup(func() { pairIdentity = prevIdentity })

			var ans identitywire.PairingResponse
			err := pairAskOverService(context.Background(), srv.URL, identitywire.PairingPath, identitywire.PairingRequest{}, &ans)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("the contract's own answer was refused: %v", err)
				}
				if ans.Token != pairIssuerToken || ans.Pairing != pairServicePairingUID {
					t.Fatalf("the answer read back as %+v, want the token and the id the body carried", ans)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
