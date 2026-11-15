// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The tests of `fugaro licence login` (K3.3), and they are about the half of
// the command that is not ee/licence/login's: which installation the requests
// carry, that what the site answers is verified against this installation's own
// Firebase project id and the ring (TN2) before anything is written, that the
// two writes are the ones `set` makes in the one order a failure can be undone
// from, and that no line a person reads carries the key or the renewal secret
// (F5, F41).
//
// The site is this test's own httptest server on 127.0.0.1 and the browser is a
// function that calls the command's own loopback listener back the way K2.4's
// redirect would, so the whole path is hermetic: the command, the listener, the
// exchange, the store and the secret container, with no network resolved and no
// key material in a file — every key here is minted in this process, by the
// fixture's own issuer.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// What the fake site hands back: the one-time code a browser brings to the
// listener, and the device code and user code a start answer names. None of
// them is key material — the key and the renewal secret a test installs are
// minted by the fixture.
const (
	loginTestCode       = "one-time-code-value"
	loginTestDeviceCode = "device-code-1"
	loginTestUserCode   = "ABCD-EFGH"
)

// loginSite is the licence site's own routes as this command speaks to them. It
// records each body **as it arrived**, because what this file is about is the
// wire: K2.4 reads these bodies strictly (its readCall), so a field this command
// added to one would be a body the site refuses.
type loginSite struct {
	ts *httptest.Server
	// key and secret are what the exchange and the poll answer with.
	key, secret string
	// tokenReply replaces the exchange's own answer when set, with the status
	// it is answered under (0 keeps 200).
	tokenReply func(body string) (int, string)

	mu     sync.Mutex
	starts []string
	tokens []string
	polls  []string
}

// serveLoginSite points the command's licence server at a fake site answering
// the bundle the test minted, and answers every call on the happy path unless
// the test says otherwise.
func (f *licenceFixture) serveLoginSite(t *testing.T, key, secret string) *loginSite {
	t.Helper()
	s := &loginSite{key: key, secret: secret}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case keyservice.RouteToken:
			s.tokens = append(s.tokens, string(body))
			if s.tokenReply != nil {
				status, answer := s.tokenReply(string(body))
				if status != 0 {
					w.WriteHeader(status)
				}
				_, _ = io.WriteString(w, answer)
				return
			}
			_, _ = io.WriteString(w, fmt.Sprintf(`{"key":%q,"renewal_secret":%q}`, s.key, s.secret))
		case keyservice.RouteDeviceStart:
			s.starts = append(s.starts, string(body))
			_, _ = io.WriteString(w, fmt.Sprintf(`{"device_code":%q,"user_code":%q,"verification_uri":%q,"expires_in":600,"interval":1}`,
				loginTestDeviceCode, loginTestUserCode, s.ts.URL+keyservice.RouteDeviceApprove))
		case keyservice.RouteDevicePoll:
			s.polls = append(s.polls, string(body))
			_, _ = io.WriteString(w, fmt.Sprintf(`{"status":%q,"key":%q,"renewal_secret":%q}`, keyservice.DeviceOK, s.key, s.secret))
		default:
			// A sign-in that reached any other route is one this file knows
			// nothing about, and it says so rather than passing.
			t.Errorf("the licence site was called %s %s", r.Method, r.URL.Path)
			http.Error(w, "no such route", http.StatusNotFound)
		}
	}))
	t.Cleanup(s.ts.Close)
	licenceServerURL = s.ts.URL
	return s
}

// calls answers the bodies a route was called with, as they arrived.
func (s *loginSite) calls(route string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch route {
	case keyservice.RouteDeviceStart:
		return append([]string(nil), s.starts...)
	case keyservice.RouteToken:
		return append([]string(nil), s.tokens...)
	default:
		return append([]string(nil), s.polls...)
	}
}

// playBrowser is what a test's opener does: it reads the loopback port and the
// state out of the address the command printed, and calls the command's own
// listener back the way the site's redirect would — with the state, and with
// the code the person's sign-in earned.
func playBrowser(t *testing.T, address, code string) {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal("the address the command opened does not parse")
	}
	callback := "http://127.0.0.1:" + parsed.Query().Get("port") + "/callback?" + url.Values{
		"state": {parsed.Query().Get("state")},
		"code":  {code},
	}.Encode()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(callback)
	if err != nil {
		t.Fatalf("the command's own listener did not answer the callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the callback was answered HTTP %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// playsBrowser makes this test the browser, and answers nothing else: the
// address the command opens is one a person's browser would open, and the
// command then waits on its own listener until it is called back.
func playsBrowser(t *testing.T, opened *string, code string) {
	t.Helper()
	previous := licenceLoginBrowser
	licenceLoginBrowser = func(address string) error {
		if opened != nil {
			*opened = address
		}
		playBrowser(t, address, code)
		return nil
	}
	t.Cleanup(func() { licenceLoginBrowser = previous })
}

// TestLicenceLoginInstallsTheKeyTheSignInIssued is the browser flow end to end:
// the address is opened, the callback carries this flow's own state, the
// exchange is made with the code the callback brought, and the key and the
// secret the site answered are the two writes `set` makes. The address names
// this installation's Firebase project id — the value the portal's screen binds
// the licence a person picks to, shown there as untrusted text (K3.3's
// amendment) — and it carries the digest of the verifier the exchange presents.
func TestLicenceLoginInstallsTheKeyTheSignInIssued(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	f.seedSecretContainer(nil)
	s := f.serveLoginSite(t, key, secret)
	var opened string
	playsBrowser(t, &opened, loginTestCode)

	out, errOut, err := f.exec(t, "", "licence", "login")
	if err != nil {
		t.Fatalf("licence login = %v (stderr %q)", err, errOut)
	}
	assertNoKeyMaterial(t, "`licence login`'s output", out+errOut, key, secret)

	if !strings.HasPrefix(opened, s.ts.URL+keyservice.RouteAuthorize+"?") {
		t.Errorf("the command opened %q, want the site's own authorize page", safetext.Field(opened))
	}
	address, parseErr := url.Parse(opened)
	if parseErr != nil {
		t.Fatalf("the address the command opened does not parse: %v", parseErr)
	}
	if got := address.Query().Get("installation"); got != licenceTestFirebase {
		t.Errorf("the authorize address carries installation=%q, want this installation's Firebase project id %q", got, licenceTestFirebase)
	}
	if got := address.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("the authorize address asks for challenge method %q, want S256", got)
	}

	calls := s.calls(keyservice.RouteToken)
	if len(calls) != 1 {
		t.Fatalf("the site's exchange route was called %d times, want once", len(calls))
	}
	var call struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := json.Unmarshal([]byte(calls[0]), &call); err != nil {
		t.Fatalf("the exchange is not the JSON this command should send: %v", err)
	}
	if call.Code != loginTestCode {
		t.Error("the exchange did not carry the code the callback brought back")
	}
	sum := sha256.Sum256([]byte(call.CodeVerifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); address.Query().Get("code_challenge") != want {
		t.Error("the verifier the exchange presented is not the one the challenge in the authorize address is the digest of")
	}

	if obj := f.object(t); !obj.Present || len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Errorf("the project's object holds %v, want the one key the sign-in installed", obj.Keys)
	}
	if got := string(f.secrets.Latest(f.secretContainer())); got != secret {
		t.Error("the renewal secret container does not hold the secret the site answered with")
	}
	for _, want := range []string{"signed in", project.Object, string(licence.TierTeam), "paid through", string(licence.Active), "renewal secret written"} {
		if !strings.Contains(out, want) {
			t.Errorf("`licence login` printed no %q:\n%s", want, out)
		}
	}
}

// TestLicenceLoginSendsTheInstallationInTheDeviceFlow: the device code's start
// carries this installation's Firebase project id, and it carries it in K2.4's
// own field and no other — `installation` alone, because the site reads that
// body strictly and a second field is a start it refuses. What a person is shown
// is the user code and the address to type it at, and never the device code,
// which is the half only this process presents.
func TestLicenceLoginSendsTheInstallationInTheDeviceFlow(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	f.seedSecretContainer(nil)
	s := f.serveLoginSite(t, key, secret)

	out, errOut, err := f.exec(t, "", "licence", "login", "--device")
	if err != nil {
		t.Fatalf("licence login --device = %v (stderr %q)", err, errOut)
	}
	assertNoKeyMaterial(t, "`licence login --device`'s output", out+errOut, key, secret)

	starts := s.calls(keyservice.RouteDeviceStart)
	if len(starts) != 1 {
		t.Fatalf("the site's start route was called %d times, want once", len(starts))
	}
	var start map[string]any
	if err := json.Unmarshal([]byte(starts[0]), &start); err != nil {
		t.Fatalf("the start is not the JSON this command should send: %v", err)
	}
	if len(start) != 1 {
		t.Errorf("the start carries the fields %v, want the one field the site reads: its body decoder refuses a field it does not know", start)
	}
	if got := start["installation"]; got != licenceTestFirebase {
		t.Errorf("the start carries installation=%v, want this installation's Firebase project id %q", got, licenceTestFirebase)
	}
	if !strings.Contains(out, loginTestUserCode) {
		t.Errorf("`licence login --device` did not print the code a person types:\n%s", out)
	}
	if strings.Contains(out, loginTestDeviceCode) {
		t.Error("`licence login --device` printed the device code, which is the half only this process presents")
	}
	if obj := f.object(t); len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Error("the device sign-in did not install the key the site issued")
	}
	polls := s.calls(keyservice.RouteDevicePoll)
	if len(polls) != 1 || !strings.Contains(polls[0], loginTestDeviceCode) {
		t.Errorf("the poll sent %v, want the device code the site issued", polls)
	}
}

// TestLicenceLoginFallsBackToTheDeviceCodeWithoutABrowser holds the plan's
// second trigger for the device flow: a machine whose browser could not be
// opened is not a failed sign-in, and the command says so in one line, because
// the person is about to be asked for something they did not ask for.
func TestLicenceLoginFallsBackToTheDeviceCodeWithoutABrowser(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	f.seedSecretContainer(nil)
	s := f.serveLoginSite(t, key, secret)
	previous := licenceLoginBrowser
	licenceLoginBrowser = func(string) error { return errors.New("xdg-open: not found") }
	t.Cleanup(func() { licenceLoginBrowser = previous })

	out, errOut, err := f.exec(t, "", "licence", "login")
	if err != nil {
		t.Fatalf("licence login on a machine with no browser = %v", err)
	}
	if !strings.Contains(errOut, "no browser could be opened on this machine") {
		t.Errorf("the fallback was not explained:\n%s", errOut)
	}
	if len(s.calls(keyservice.RouteDeviceStart)) != 1 {
		t.Error("the command did not ask the site for a device code")
	}
	if len(s.calls(keyservice.RouteToken)) != 0 {
		t.Error("the command exchanged a code, and its browser never opened")
	}
	if obj := f.object(t); len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Error("the device sign-in did not install the key the site issued")
	}
	if !strings.Contains(out, loginTestUserCode) {
		t.Errorf("the fallback printed no code for a person to type:\n%s", out)
	}
}

// TestLicenceLoginRefusesWhatItMayNotInstall holds the four answers that are not
// an installation, and each is refused **before either write**: a key bound to
// another workspace, an account the site will not serve, a key this installation
// would not apply (T26), and a pair the site issued that does not agree with
// itself. The first three are a person's own state and exit 1; the last is the
// site contradicting itself, which is remote and exits 2.
func TestLicenceLoginRefusesWhatItMayNotInstall(t *testing.T) {
	for _, tc := range []struct {
		name string
		// want is a sentence the refusal has to carry, and code is its exit
		// code.
		want string
		code int
		run  func(t *testing.T, f *licenceFixture, s *loginSite)
	}{
		{
			name: "a key bound to another workspace",
			want: "the key was not accepted",
			code: fcli.ExitUserError,
			run: func(t *testing.T, f *licenceFixture, s *loginSite) {
				s.key, s.secret, _ = f.bundle(t, func(c *licence.Claims) { c.WorkspaceID = "someone-elses-fp" })
			},
		},
		{
			name: "an account the site will not serve",
			want: "no licence is bound to this account",
			code: fcli.ExitUserError,
			run: func(_ *testing.T, _ *licenceFixture, s *loginSite) {
				// K2.4's roads answer a caller they will not serve with one
				// uniform refusal: the sentence is shown, and the rest of the
				// body is only ever read through licenceRedact.
				s.tokenReply = func(string) (int, string) {
					return http.StatusForbidden, `{"error":"no licence is bound to this account"}`
				}
			},
		},
		{
			name: "a key this installation would not apply",
			want: "sign in again and pick the licence this installation already runs on",
			code: fcli.ExitUserError,
			run: func(t *testing.T, f *licenceFixture, s *loginSite) {
				longer, _, _ := f.bundle(t, func(c *licence.Claims) { c.KeyID = "lk_test_k33_longer" })
				install(t, f, longer)
				s.key, s.secret, _ = f.bundle(t, func(c *licence.Claims) {
					c.KeyID = "lk_test_k33_shorter"
					c.PaidThrough = f.now.Add(10 * 24 * time.Hour)
				})
			},
		},
		{
			name: "a pair the site issued that does not agree with itself",
			want: "a renewal secret that is not the one it renews with",
			code: fcli.ExitRemoteError,
			run: func(t *testing.T, f *licenceFixture, s *loginSite) {
				s.key, _, _ = f.bundle(t)
				s.secret = licenceTestSecret(t)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLicenceFixture(t)
			f.seedSecretContainer(nil)
			s := f.serveLoginSite(t, "", "")
			tc.run(t, f, s)
			before := f.object(t)
			playsBrowser(t, nil, loginTestCode)

			out, errOut, err := f.exec(t, "", "licence", "login")
			if err == nil {
				t.Fatal("the sign-in installed a key it may not install")
			}
			if got := fcli.ExitCode(err); got != tc.code {
				t.Errorf("the refusal exited %d, want %d: %v", got, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %q, which does not say %q", err.Error(), tc.want)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "nothing was written") {
				t.Errorf("the refusal is %q, which does not say that nothing was written", err.Error())
			}
			assertNoKeyMaterial(t, "the refusal", err.Error()+out+errOut, s.key, s.secret)
			if after := f.object(t); string(after.Raw) != string(before.Raw) {
				t.Error("a refused sign-in wrote to the project's object")
			}
			if f.secrets.Latest(f.secretContainer()) != nil {
				t.Error("a refused sign-in put a version in the renewal secret container")
			}
		})
	}
}

// TestLicenceLoginRefusesAKeyInArgv holds the rule every licence verb keeps: a
// key pasted where an argument belongs is refused without being repeated, and
// the sign-in never starts.
func TestLicenceLoginRefusesAKeyInArgv(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	s := f.serveLoginSite(t, key, secret)
	playsBrowser(t, nil, loginTestCode)

	out, errOut, err := f.exec(t, "", "licence", "login", key)
	if err == nil {
		t.Fatal("`licence login` took an argument")
	}
	if got := fcli.ExitCode(err); got != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", got, fcli.ExitUserError, err)
	}
	if strings.Contains(err.Error(), key) || strings.Contains(out, key) || strings.Contains(errOut, key) {
		t.Fatal("the refusal repeats its argument, which is the place a pasted key lands")
	}
	if !strings.Contains(err.Error(), "read from standard input or --file") {
		t.Errorf("the refusal is %q, which does not say where a key does go", err.Error())
	}
	if len(s.calls(keyservice.RouteDeviceStart)) != 0 || len(s.calls(keyservice.RouteToken)) != 0 {
		t.Error("a refused `licence login` called the licence site")
	}
	if f.object(t).Present {
		t.Error("a refused `licence login` installed a key")
	}
}

// TestLicenceLoginRefusesBeforeTheInstallationHasAWorkspace holds the one config
// a sign-in cannot start from: a key is bound to the installation's Firebase
// project id (TN2), so an installation with none has nothing to ask for, nothing
// the site could bind a licence to and nothing to check the answer against. The
// refusal names the command that gives it one, and it is made before a request
// and before a browser.
func TestLicenceLoginRefusesBeforeTheInstallationHasAWorkspace(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.bundle(t)
	s := f.serveLoginSite(t, key, secret)
	data, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	without := strings.Replace(string(data), "budget: { firebase_project: "+licenceTestFirebase+" }", "budget: {}", 1)
	if without == string(data) {
		t.Fatal("the fixture's config does not carry the Firebase project this test removes")
	}
	if err := os.WriteFile(f.config, []byte(without), 0o600); err != nil {
		t.Fatal(err)
	}
	var opened string
	playsBrowser(t, &opened, loginTestCode)

	out, _, err := f.exec(t, "", "licence", "login")
	if err == nil {
		t.Fatal("`licence login` started a sign-in for an installation with no Firebase project")
	}
	if got := fcli.ExitCode(err); got != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", got, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), "has no Firebase project") {
		t.Errorf("the refusal is %q, which does not say what is missing", err.Error())
	}
	if opened != "" || out != "" {
		t.Error("a sign-in that cannot be bound to anything opened a browser or printed an address")
	}
	if len(s.calls(keyservice.RouteDeviceStart)) != 0 || len(s.calls(keyservice.RouteToken)) != 0 {
		t.Error("a sign-in that cannot be bound to anything called the licence site")
	}
}
