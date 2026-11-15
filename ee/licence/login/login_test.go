// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package login

// The tests of the two flows, and they are what holds the six properties this
// package's own documentation lists: the listener binds the literal loopback
// address (TestLoopbackBindsOnlyTheLoopback), a callback carrying anything but
// this flow's state is refused and does not end the flow
// (TestCallbackWithTheWrongStateIsRefused), no line of a flow carries the
// one-time code or the verifier (TestTheCodeIsNeverLogged), the poll stops at
// the code's own expiry (TestDevicePollStopsAtExpiry), and both requests carry
// the installation's Firebase project id (TestTheAuthorizeAddressCarriesTheFirebaseProjectID,
// TestDeviceCarriesTheFirebaseProjectID).
//
// The site is a test's own httptest server on 127.0.0.1 and the browser is a
// function the test supplies, so the whole flow is hermetic: every address is a
// loopback one, nothing resolves a name, and no value that is key material
// stands in this file — a key a site signs is verified in ee/register, and this
// package only carries it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice"
)

// The fixture values of these tests: the installation both requests carry, the
// code a browser brings back, and the two values a site answers with them.
const (
	testInstallation = "aurora-fp"
	testCode         = "one-time-code-value"
	testSolvedCode   = "another-code-a-guess-would-use"
	testKey          = "key-the-site-issued"
	testSecret       = "secret-the-site-issued"
	testDeviceCode   = "device-code-1"
	testUserCode     = "ABCD-EFGH"
)

// site is the licence site's own routes as far as these tests speak to them: it
// records what it was called with and answers what a test tells it to, so that
// an assertion is about the wire rather than about a client's own reading of
// it.
type site struct {
	ts *httptest.Server

	mu       sync.Mutex
	tokens   []tokenCall
	polls    []devicePollCall
	starts   []deviceCall
	startErr func(deviceCall, http.ResponseWriter, *http.Request)
	tokenErr func(tokenCall, http.ResponseWriter, *http.Request)
	pollErr  func(int, http.ResponseWriter, *http.Request)
}

// newSite is a running site answering the happy path of both flows: the start
// names a code worth ten minutes and polled every second, the poll approves the
// sign-in at once, and the exchange hands back the key and the secret.
func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *site) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxAnswerBytes))
	if err != nil {
		http.Error(w, "unreadable", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case keyservice.RouteToken:
		var call tokenCall
		if err := json.Unmarshal(body, &call); err != nil {
			http.Error(w, "the exchange is not a tokenCall", http.StatusBadRequest)
			return
		}
		s.tokens = append(s.tokens, call)
		if s.tokenErr != nil {
			s.tokenErr(call, w, r)
			return
		}
		_, _ = io.WriteString(w, bundleJSON())
	case keyservice.RouteDeviceStart:
		var call deviceCall
		if err := json.Unmarshal(body, &call); err != nil {
			http.Error(w, "the start is not a deviceCall", http.StatusBadRequest)
			return
		}
		s.starts = append(s.starts, call)
		if s.startErr != nil {
			s.startErr(call, w, r)
			return
		}
		_, _ = io.WriteString(w, fmt.Sprintf(`{"device_code":%q,"user_code":%q,"verification_uri":%q,"expires_in":600,"interval":1}`,
			testDeviceCode, testUserCode, s.ts.URL+keyservice.RouteDeviceApprove))
	case keyservice.RouteDevicePoll:
		var call devicePollCall
		if err := json.Unmarshal(body, &call); err != nil {
			http.Error(w, "the poll is not a devicePollCall", http.StatusBadRequest)
			return
		}
		s.polls = append(s.polls, call)
		if s.pollErr != nil {
			s.pollErr(len(s.polls), w, r)
			return
		}
		_, _ = io.WriteString(w, deviceAnswerJSON(keyservice.DeviceOK))
	default:
		http.Error(w, "no such route", http.StatusNotFound)
	}
}

// answer replaces one route's own answer. It takes the lock the answers are
// built under, so a test setting one and the server goroutine reading it are
// ordered.
func (s *site) answer(start func(deviceCall, http.ResponseWriter, *http.Request), token func(tokenCall, http.ResponseWriter, *http.Request), poll func(int, http.ResponseWriter, *http.Request)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startErr, s.tokenErr, s.pollErr = start, token, poll
}

func bundleJSON() string {
	return fmt.Sprintf(`{"key":%q,"renewal_secret":%q}`, testKey, testSecret)
}

func deviceAnswerJSON(status string) string {
	return fmt.Sprintf(`{"status":%q,"key":%q,"renewal_secret":%q}`, status, testKey, testSecret)
}

// pendingJSON is the one answer a poll gets while a person has not approved the
// code yet.
const pendingJSON = `{"status":"` + keyservice.DevicePending + `"}`

func (s *site) tokenCalls() []tokenCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tokenCall(nil), s.tokens...)
}

func (s *site) pollCalls() []devicePollCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]devicePollCall(nil), s.polls...)
}

func (s *site) startCalls() []deviceCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]deviceCall(nil), s.starts...)
}

// testClient is the HTTP client these tests make their loopback requests with:
// a bound on how long a listener that never answers may hold a test, and no
// redirect followed, so a test never reaches an address it did not name.
var testClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// browser is the flow's browser and the site's own redirect in one: it takes
// the address the flow opened, reads the loopback port and the state out of it,
// and calls that listener back the way K2.4's redirect would — the state, and
// the code a person's sign-in earned.
type browser struct {
	t      *testing.T
	opened string
	// before, when set, is called with the address before the real callback is
	// made: it is where a test puts the requests that must not end the flow.
	before func(address string) *http.Response
	code   string
}

func (b *browser) open(address string) error {
	b.opened = address
	if b.before != nil {
		if resp := b.before(address); resp != nil {
			_ = resp.Body.Close()
		}
	}
	code := b.code
	if code == "" {
		code = testCode
	}
	resp, err := testClient.Get(b.callback(b.state(), code))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the callback answered HTTP %d", resp.StatusCode)
	}
	return nil
}

// state is this flow's own state, read out of the address the browser was
// handed — the only place a test can read it from, which is the point: the
// state reaches no other place, and the callback that carries it back is the
// only one that ends the flow.
func (b *browser) state() string {
	parsed, err := url.Parse(b.opened)
	if err != nil {
		b.t.Fatalf("the authorize address does not parse: %v", err)
	}
	return parsed.Query().Get("state")
}

// callback is the address K2.4's own redirect builds out of the port this flow
// sent: the loopback address, the path the site redirects to, the state and the
// code.
func (b *browser) callback(state, code string) string {
	return b.listener(loopbackPath, url.Values{"state": {state}, "code": {code}})
}

// listener is the flow's own listener as a browser reaches it: 127.0.0.1 on the
// port the authorize address named, and nothing else — the address a callback
// arrives on is the one this process opened.
func (b *browser) listener(path string, query url.Values) string {
	b.t.Helper()
	parsed, err := url.Parse(b.opened)
	if err != nil {
		b.t.Fatalf("the authorize address (%d bytes) does not parse: %v", len(b.opened), err)
	}
	port := parsed.Query().Get("port")
	if port == "" {
		b.t.Fatal("the authorize address carries no port, and a callback has nowhere to go")
	}
	return "http://127.0.0.1:" + port + path + "?" + query.Encode()
}

// request makes one request to the flow's listener and answers the response.
func request(t *testing.T, address, method string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatalf("the listener did not answer %s: %v", method, err)
	}
	return resp
}

// atSite is a client over one test's fake site, asking for one installation.
func atSite(c Client, s *site) *Client {
	c.ServerURL = s.ts.URL
	c.FirebaseProject = testInstallation
	return &c
}

// TestLoopbackBindsOnlyTheLoopback holds the plan's own words: the listener
// binds the literal 127.0.0.1, on a port the kernel chooses, and answers the
// one path the site's redirect lands on. It reads the two arguments net.Listen
// was handed rather than the address afterwards, because the address after the
// kernel has resolved it is the same for a listener on any interface this
// machine has.
func TestLoopbackBindsOnlyTheLoopback(t *testing.T) {
	type listenCall struct{ network, address string }
	var (
		mu     sync.Mutex
		listed []listenCall
	)
	restore := loginListen
	loginListen = func(network, address string) (net.Listener, error) {
		mu.Lock()
		listed = append(listed, listenCall{network: network, address: address})
		mu.Unlock()
		return net.Listen(network, address)
	}
	t.Cleanup(func() { loginListen = restore })

	s := newSite(t)
	b := &browser{t: t}
	var out bytes.Buffer
	c := atSite(Client{Out: &out, OpenURL: b.open}, s)
	bundle, err := c.Authorize(context.Background())
	if err != nil {
		t.Fatalf("the browser sign-in = %v", err)
	}
	if bundle.Key != testKey {
		t.Errorf("the sign-in answered a key that is not the one the site issued")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(listed) != 1 {
		t.Fatalf("the flow opened %d listeners, want exactly one: %v", len(listed), listed)
	}
	if listed[0].network != "tcp" || listed[0].address != "127.0.0.1:0" {
		t.Errorf("the listener was opened on (%q, %q), want (\"tcp\", %q): a listener on any other address is a callback this machine's network can claim, and a fixed port is one two sign-ins fight over",
			listed[0].network, listed[0].address, loopbackAddress)
	}

	// The address a person opens is the site's, and the port on it is the one
	// the listener is really on: the site builds the callback out of that port,
	// so a port that is not the listener's is a code sent somewhere else.
	opened, err := url.Parse(b.opened)
	if err != nil {
		t.Fatalf("the authorize address does not parse: %v", err)
	}
	if opened.Scheme != "http" && opened.Scheme != "https" {
		t.Errorf("the authorize address is %q, which is neither http nor https", opened.Scheme)
	}
	if host, _, err := net.SplitHostPort(opened.Host); err != nil || host != "127.0.0.1" {
		t.Errorf("the authorize address is on host %q, so the code comes back to something this machine is not listening on", opened.Host)
	}
	if port := opened.Query().Get("port"); port == "" || port == "0" {
		t.Errorf("the authorize address carries port %q, want the port the listener was opened on", port)
	}
	// K2.4's own redirect lands on this path (signin.go's callbackPath, which
	// is unexported there): a listener of this package's own would be one the
	// site's redirect never reaches.
	if loopbackPath != "/callback" {
		t.Errorf("the listener answers %q, and the site's redirect lands on %q", loopbackPath, "/callback")
	}
}

// TestTheListenerStopsWhenTheFlowIsOver: the listener is closed by the flow
// that opened it, so a signed-in terminal leaves nothing of this flow
// accepting connections on the loopback address.
func TestTheListenerStopsWhenTheFlowIsOver(t *testing.T) {
	s := newSite(t)
	b := &browser{t: t}
	c := atSite(Client{Out: io.Discard, OpenURL: b.open}, s)
	if _, err := c.Authorize(context.Background()); err != nil {
		t.Fatalf("the browser sign-in = %v", err)
	}
	callback, err := url.Parse(b.callback(b.state(), testCode))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", callback.Host, 2*time.Second)
	if err == nil {
		conn.Close()
		t.Errorf("the listener is still accepting connections on %s after the sign-in finished", callback.Host)
	}
}

// TestCallbackWithTheWrongStateIsRefused holds the state's whole job: the
// callback that ends the flow is the one carrying this flow's own state, and a
// request with any other state — or with this state and no code, or on a path
// or method the listener does not answer — is refused **without** ending the
// flow. A local process that guessed the port must not be able to spend the
// person's sign-in by hitting it first (RFC 8252 section 7.3).
func TestCallbackWithTheWrongStateIsRefused(t *testing.T) {
	s := newSite(t)
	b := &browser{t: t}
	b.before = func(string) *http.Response {
		for _, tc := range []struct {
			name    string
			address string
			method  string
			status  int
		}{
			{name: "another state, with the code a guess would bring", address: b.callback("not-this-flows-state", testSolvedCode), method: http.MethodGet, status: http.StatusBadRequest},
			{name: "this flow's state, with no code", address: b.listener(loopbackPath, url.Values{"state": {b.state()}}), method: http.MethodGet, status: http.StatusBadRequest},
			{name: "another path", address: b.listener("/nothing", url.Values{"state": {b.state()}, "code": {testCode}}), method: http.MethodGet, status: http.StatusNotFound},
			{name: "the callback written to instead of read", address: b.callback(b.state(), testCode), method: http.MethodPost, status: http.StatusNotFound},
		} {
			resp := request(t, tc.address, tc.method)
			status := resp.StatusCode
			_ = resp.Body.Close()
			if status != tc.status {
				t.Errorf("a callback with %s was answered HTTP %d, want %d", tc.name, status, tc.status)
			}
		}
		return nil
	}
	c := atSite(Client{Out: io.Discard, OpenURL: b.open}, s)
	bundle, err := c.Authorize(context.Background())
	if err != nil {
		t.Fatalf("the sign-in was ended by a request that is not its own callback (%v)", err)
	}
	if bundle.Key != testKey {
		t.Errorf("the sign-in answered a key that is not the one the site issued for this flow")
	}
	calls := s.tokenCalls()
	if len(calls) != 1 {
		t.Fatalf("the exchange was made %d times, want once: only the callback carrying this flow's state is an answer", len(calls))
	}
	if calls[0].Code != testCode {
		t.Errorf("the exchange carried a code that is not the one this flow's callback brought: a guessed code spent the sign-in")
	}
}

// TestTheCodeIsNeverLogged holds F5's rule for this flow: the one-time code a
// browser brings, the verifier it is exchanged with and the key the site hands
// back are in no line the flow writes — not the instructions a person reads,
// not the page the browser lands on, and not the server's own error log.
//
// The log half is structural as well as behavioural, and it has to be: net/http
// writes a line of its own only for failures a test cannot provoke on a
// listener that is up (a TLS handshake against a plain listener, a panic in a
// handler), so what is held here is that the one place it could write to is the
// package's discarding logger and that the listener's server is wired to it.
func TestTheCodeIsNeverLogged(t *testing.T) {
	if loginLog.Writer() != io.Discard {
		t.Errorf("the listener's own error log writes to %T, want nowhere: a protocol error's sentence can carry a request line, and a request line is where a code would be", loginLog.Writer())
	}
	listener, err := newLoopbackListener("a-state")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listener.srv.ErrorLog != loginLog {
		t.Errorf("the listener's server writes a line of its own to %T, want the package's discarding logger (loginLog)", listener.srv.ErrorLog.Writer())
	}

	var logged lockedBuffer
	restore := loginLog
	loginLog = log.New(&logged, "", 0)
	t.Cleanup(func() { loginLog = restore })

	s := newSite(t)
	b := &browser{t: t}
	b.before = func(string) *http.Response {
		// Requests that carry the code and are not the callback: a wrong state,
		// and a path nothing answers. Both are refused, and neither may leave a
		// line behind.
		wrong := request(t, b.callback("not-this-flows-state", testCode), http.MethodGet)
		page, _ := io.ReadAll(wrong.Body)
		_ = wrong.Body.Close()
		if strings.Contains(string(page), testCode) {
			t.Error("the page the browser lands on carries the code it brought")
		}
		miss := request(t, b.listener("/nothing", url.Values{"code": {testCode}}), http.MethodGet)
		_ = miss.Body.Close()
		return nil
	}
	var out bytes.Buffer
	c := atSite(Client{Out: &out, OpenURL: b.open}, s)
	bundle, err := c.Authorize(context.Background())
	if err != nil {
		t.Fatalf("the sign-in = %v", err)
	}
	calls := s.tokenCalls()
	if len(calls) != 1 {
		t.Fatalf("the exchange was made %d times, want once", len(calls))
	}

	text := out.String() + "\n" + logged.String()
	for _, value := range []string{testCode, bundle.Key, bundle.RenewalSecret, calls[0].CodeVerifier, calls[0].Code} {
		if value == "" {
			continue
		}
		if strings.Contains(text, value) {
			t.Errorf("a line the flow wrote carries a value of %d runes that it may not: the code, the verifier and the key are none of them a line of output", len(value))
		}
	}
	// What the flow does print is the address a person has to open.
	if !strings.Contains(out.String(), s.ts.URL+keyservice.RouteAuthorize) {
		t.Errorf("the flow did not print the address a person opens (%d bytes of output)", out.Len())
	}
}

// lockedBuffer is a buffer a server's goroutine and the test both reach.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestTheExchangeCarriesThePKCEVerifier holds the shape of the flow's proof:
// the address a browser opens carries the S256 challenge and not the verifier,
// and the exchange carries the verifier the challenge is the digest of — so the
// code a browser brings is worth nothing to whoever reads the address it
// travelled in (a shell's history, a proxy's log, the browser's own).
func TestTheExchangeCarriesThePKCEVerifier(t *testing.T) {
	s := newSite(t)
	b := &browser{t: t}
	c := atSite(Client{Out: io.Discard, OpenURL: b.open}, s)
	if _, err := c.Authorize(context.Background()); err != nil {
		t.Fatalf("the sign-in = %v", err)
	}
	address, err := url.Parse(b.opened)
	if err != nil {
		t.Fatal(err)
	}
	query := address.Query()
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Errorf("the authorize address asks for challenge method %q, want S256", got)
	}
	calls := s.tokenCalls()
	if len(calls) != 1 {
		t.Fatalf("the exchange was made %d times, want once", len(calls))
	}
	verifier := calls[0].CodeVerifier
	if verifier == "" {
		t.Fatal("the exchange carried no code_verifier")
	}
	if strings.Contains(b.opened, verifier) {
		t.Error("the authorize address carries the verifier itself, and anyone who reads that address could spend the code without the sign-in")
	}
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); query.Get("code_challenge") != want {
		t.Error("the authorize address carries a challenge that is not the SHA-256 of the verifier the exchange sent")
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Errorf("the verifier is %d characters, and RFC 7636 reads 43 to 128", len(verifier))
	}
	if state := b.state(); len(state) < 43 {
		t.Errorf("the state is %d characters, and a state that can be guessed is a callback any local process can make first", len(state))
	}
	if state := b.state(); strings.Contains(calls[0].Code, state) {
		t.Error("the state is carried in the code the site issued")
	}
}

// TestTheAuthorizeAddressCarriesTheFirebaseProjectID holds K3.3's amendment of
// 2026-11-13: the request names the installation's Firebase project id, which
// is the value the portal's own authorize screen binds the entitlement a person
// picks to, and shows as untrusted text — nothing proves it (the portal's F37).
func TestTheAuthorizeAddressCarriesTheFirebaseProjectID(t *testing.T) {
	s := newSite(t)
	b := &browser{t: t}
	c := atSite(Client{Out: io.Discard, OpenURL: b.open}, s)
	if _, err := c.Authorize(context.Background()); err != nil {
		t.Fatalf("the sign-in = %v", err)
	}
	address, err := url.Parse(b.opened)
	if err != nil {
		t.Fatal(err)
	}
	if got := address.Query().Get("installation"); got != testInstallation {
		t.Errorf("the authorize address carries installation=%q, want the installation's Firebase project id %q: the site binds the licence a person picks to the id the request names", got, testInstallation)
	}
}

// TestDeviceCarriesTheFirebaseProjectID holds the same amendment on the device
// flow's own request, and holds the field it travels in: `installation`, whose
// value is the Firebase project id and not a display name. The site reads this
// body strictly (K2.4's readCall refuses a field it does not know), so a second
// field would be a body it answers with the flow's uniform refusal.
func TestDeviceCarriesTheFirebaseProjectID(t *testing.T) {
	s := newSite(t)
	var out bytes.Buffer
	c := atSite(Client{Out: &out}, s)
	bundle, err := c.Device(context.Background())
	if err != nil {
		t.Fatalf("the device sign-in = %v", err)
	}
	if bundle.Key != testKey || bundle.RenewalSecret != testSecret {
		t.Error("the device sign-in answered a bundle that is not the site's own")
	}
	starts := s.startCalls()
	if len(starts) != 1 {
		t.Fatalf("the start was made %d times, want once", len(starts))
	}
	if starts[0].Installation != testInstallation {
		t.Errorf("the start carried installation=%q, want the installation's Firebase project id %q", starts[0].Installation, testInstallation)
	}
	// What a person is shown is the user code and the address to type it at.
	// The device code is the half only this process presents, and the key and
	// the secret are shown nowhere.
	text := out.String()
	if !strings.Contains(text, testUserCode) {
		t.Error("the device flow did not show the user code a person has to type")
	}
	for _, value := range []string{testDeviceCode, bundle.Key, bundle.RenewalSecret} {
		if strings.Contains(text, value) {
			t.Errorf("the device flow printed a value a person is not shown (%d runes)", len(value))
		}
	}
	polls := s.pollCalls()
	if len(polls) != 1 || polls[0].DeviceCode != testDeviceCode {
		t.Errorf("the poll carried %v, want the device code the site issued and nothing else", polls)
	}
}

// TestDevicePollStopsAtExpiry holds the one thing a poll may not do: keep
// asking after the code it is asking about has expired. The site answers
// `pending` for ever, so the only thing that can end this flow is the code's own
// expiry — and it ends with ErrExpired rather than with a terminal someone has
// to kill.
func TestDevicePollStopsAtExpiry(t *testing.T) {
	s := newSite(t)
	s.answer(func(_ deviceCall, w http.ResponseWriter, _ *http.Request) {
		// A code the site says is worth one second, polled as often as it
		// allows: the expiry is the site's own number, and the flow reads it.
		_, _ = io.WriteString(w, fmt.Sprintf(`{"device_code":%q,"user_code":%q,"verification_uri":%q,"expires_in":1,"interval":1}`,
			testDeviceCode, testUserCode, s.ts.URL+keyservice.RouteDeviceApprove))
	}, nil, func(_ int, w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, pendingJSON)
	})

	var out bytes.Buffer
	c := atSite(Client{Out: &out}, s)
	started := time.Now()
	bundle, err := c.Device(context.Background())
	elapsed := time.Since(started)
	if err == nil {
		t.Fatalf("a poll for a code that expired answered a bundle: %+v", bundle)
	}
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("the poll ended with %v, want ErrExpired", err)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("the refusal is %q, which does not say what happened", err.Error())
	}
	polls := len(s.pollCalls())
	if polls == 0 {
		t.Error("the flow never polled the site at all")
	} else if polls > 3 {
		t.Errorf("the flow polled %d times for a one-second code, want it to stop at the expiry", polls)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the poll ran for %s on a one-second code", elapsed)
	}
}

// TestDevicePollRefusedAtExpiryIsAnExpiry holds the ending the real site
// answers, and not the one the test above can stage: K2.4 sweeps a code the
// moment its window closes, and a poll for a code it is no longer holding gets
// the uniform refusal it gives every caller it will not serve — a 403 whose
// sentence is about the request, not about the code. Read as the site's own
// answer, that sentence says the account was refused when all that happened is
// that the code ran out, and it hides the one thing a person can act on: that
// no sign-in was approved and the sign-in can simply be started again. The
// flow's own deadline is what tells the two apart, so a refusal that arrives at
// or after it ends the poll as the expiry it is.
func TestDevicePollRefusedAtExpiryIsAnExpiry(t *testing.T) {
	s := newSite(t)
	s.answer(func(_ deviceCall, w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf(`{"device_code":%q,"user_code":%q,"verification_uri":%q,"expires_in":1,"interval":1}`,
			testDeviceCode, testUserCode, s.ts.URL+keyservice.RouteDeviceApprove))
	}, nil, func(poll int, w http.ResponseWriter, _ *http.Request) {
		// The sweep: from the second poll on, the code this poll names is one
		// the site is not holding any more, which is what a poll at the
		// deadline always finds. The sentence is K2.4's own for it.
		if poll > 1 {
			http.Error(w, "This request cannot be answered.", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, pendingJSON)
	})

	var out bytes.Buffer
	c := atSite(Client{Out: &out}, s)
	bundle, err := c.Device(context.Background())
	if err == nil {
		t.Fatalf("a poll the site refused answered a bundle: %+v", bundle)
	}
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("the poll ended with %v, want ErrExpired: the code's window had closed, and the site's refusal is about the code it swept rather than about the account", err)
	}
	var refusal *Refusal
	if errors.As(err, &refusal) {
		t.Errorf("the expired code ended with the site's own refusal (%v), which is the sentence about a request and not the ending", refusal)
	}
	if !strings.Contains(err.Error(), "no sign-in was approved") {
		t.Errorf("the refusal is %q, which does not name what happened", err.Error())
	}
	if polls := len(s.pollCalls()); polls > 3 {
		t.Errorf("the flow polled %d times for a one-second code, want it to stop at the expiry", polls)
	}
}

// TestDevicePollObeysTheSitesOwnRate holds the other half of the same loop: a
// 429 is the site's own rate for the code, and the wait it asks for is what the
// poll takes — not the interval the start answer named, which the site has just
// said is not what it is enforcing.
func TestDevicePollObeysTheSitesOwnRate(t *testing.T) {
	s := newSite(t)
	s.answer(func(_ deviceCall, w http.ResponseWriter, _ *http.Request) {
		// The site asks for five seconds between polls and then answers the
		// first one with a rate of one: a client that waited the interval would
		// sit here for five seconds, and one that obeyed the site answers in
		// about one.
		_, _ = io.WriteString(w, fmt.Sprintf(`{"device_code":%q,"user_code":%q,"verification_uri":%q,"expires_in":600,"interval":5}`,
			testDeviceCode, testUserCode, s.ts.URL+keyservice.RouteDeviceApprove))
	}, nil, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"status":"slow down"}`)
			return
		}
		_, _ = io.WriteString(w, deviceAnswerJSON(keyservice.DeviceOK))
	})

	c := atSite(Client{Out: io.Discard}, s)
	started := time.Now()
	bundle, err := c.Device(context.Background())
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("the device sign-in = %v", err)
	}
	if bundle.Key != testKey {
		t.Error("the sign-in answered a bundle that is not the site's own")
	}
	if polls := len(s.pollCalls()); polls != 2 {
		t.Errorf("the site was polled %d times, want the 429 and then the answer", polls)
	}
	if elapsed > 4*time.Second {
		t.Errorf("the poll waited %s, and the site asked for 1s: the interval it named is not what it is enforcing", elapsed)
	}
}

// TestNothingIsSentToAnAddressThatIsNotTheSites holds what a sign-in may not do
// with the address it is given: these requests carry a one-time code and a PKCE
// verifier, so an address that is not an http(s) one is refused before any of
// them is generated and before a browser is opened — a browser is a place these
// values would travel to and stay.
func TestNothingIsSentToAnAddressThatIsNotTheSites(t *testing.T) {
	for _, address := range []string{"", "keys.fugaro.dev", "ftp://keys.fugaro.dev", "file:///etc/passwd", "://", "https://"} {
		for _, flow := range []string{"authorize", "device"} {
			var out bytes.Buffer
			opened := false
			c := Client{ServerURL: address, FirebaseProject: testInstallation, Out: &out,
				OpenURL: func(string) error {
					opened = true
					return errors.New("no browser in this test")
				}}
			var err error
			if flow == "authorize" {
				_, err = c.Authorize(context.Background())
			} else {
				_, err = c.Device(context.Background())
			}
			if err == nil {
				t.Errorf("the %s flow accepted %q as the licence site's address", flow, address)
				continue
			}
			if !strings.Contains(err.Error(), "not an http(s) address") {
				t.Errorf("the %s flow refused %q with %q, which does not say why", flow, address, err.Error())
			}
			if opened {
				t.Errorf("the %s flow opened %q in a browser", flow, address)
			}
		}
	}
}

// TestARedirectIsNotFollowed holds the rule the flows' own client keeps: a
// route this package POSTs to is answered in place, never chased. The bodies
// those routes carry are the one-time code, the PKCE verifier and the device
// code, so a client that obeyed a Location would re-send them to whatever host
// the answer named — one the site chose, and one this process never decided to
// talk to. A 302 is therefore an answer this package reads (a Refusal), and the
// address it points at is never called.
//
// The second server is what makes that observable: it answers a 200 with a
// whole bundle, so a client that followed the redirect would not merely send
// the values away — it would come back with a key.
func TestARedirectIsNotFollowed(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, bundleJSON())
	}))
	t.Cleanup(trap.Close)

	for _, flow := range []string{"authorize", "device"} {
		t.Run(flow, func(t *testing.T) {
			s := newSite(t)
			redirect := func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, trap.URL+"/the-redirects-host", http.StatusFound)
			}
			// The one route each flow reaches with a body in hand: the device
			// flow's start, and the browser flow's exchange.
			s.answer(func(_ deviceCall, w http.ResponseWriter, r *http.Request) { redirect(w, r) },
				func(_ tokenCall, w http.ResponseWriter, r *http.Request) { redirect(w, r) }, nil)

			b := &browser{t: t}
			c := atSite(Client{Out: io.Discard, OpenURL: b.open}, s)
			var (
				bundle Bundle
				err    error
			)
			if flow == "device" {
				bundle, err = c.Device(context.Background())
			} else {
				bundle, err = c.Authorize(context.Background())
			}
			if err == nil {
				t.Fatalf("a site that answered a redirect ended the flow with a bundle: %+v", bundle)
			}
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Status != http.StatusFound {
				t.Errorf("a redirect ended the flow with %v, want the site's own HTTP %d as a refusal", err, http.StatusFound)
			}
			mu.Lock()
			sent := append([]string(nil), paths...)
			mu.Unlock()
			if len(sent) != 0 {
				t.Errorf("the redirect was followed, and the flow's own values were re-sent to the address the site named: %v", sent)
			}
		})
	}
}

// TestAnInstallationWithNoProjectIDSendsNothing holds the other value a
// sign-in is about: a key is bound to the installation's Firebase project id
// (TN2), so an installation with none has nothing to ask for and nothing to
// check the answer against — and each refusal comes before a listener is opened
// and before a request is made, because the site's own answer would be its
// uniform sentence about a sign-in.
func TestAnInstallationWithNoProjectIDSendsNothing(t *testing.T) {
	s := newSite(t)
	for _, tc := range []struct {
		project string
		says    string
	}{
		{project: "", says: "no Firebase project id"},
		{project: strings.Repeat("a", maxInstallationBytes+1), says: "the licence site reads"},
		{project: "aurora\nfp", says: "does not read"},
		{project: "aurora\x7ffp", says: "does not read"},
	} {
		// The opener fails rather than plays the browser: a flow that wrongly
		// proceeded has to end here, because the test's own assertion is the
		// only thing that would end it otherwise.
		c := Client{ServerURL: s.ts.URL, FirebaseProject: tc.project, Out: io.Discard,
			OpenURL: func(string) error {
				t.Error("a sign-in with nothing to ask for opened a browser")
				return errors.New("no browser in this test")
			}}
		if _, err := c.Authorize(context.Background()); err == nil {
			t.Errorf("the sign-in accepted %q as the installation's Firebase project id", tc.project)
		} else if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("the refusal of %q is %q, which does not say %q", tc.project, err.Error(), tc.says)
		}
		if _, err := c.Device(context.Background()); err == nil {
			t.Errorf("the device sign-in accepted %q as the installation's Firebase project id", tc.project)
		}
	}
	if starts, tokens := s.startCalls(), s.tokenCalls(); len(starts) != 0 || len(tokens) != 0 {
		t.Error("a sign-in with nothing to ask for called the licence site anyway")
	}
}

// TestABrowserThatCouldNotBeOpenedIsItsOwnRefusal holds the signal the command
// falls back to the device code on: an opener that failed is ErrNoBrowser and
// not a failed sign-in, it carries the opener's own words for whoever reads the
// note, and it exchanges nothing.
func TestABrowserThatCouldNotBeOpenedIsItsOwnRefusal(t *testing.T) {
	s := newSite(t)
	c := atSite(Client{Out: io.Discard, OpenURL: func(string) error { return errors.New("xdg-open: not found") }}, s)
	_, err := c.Authorize(context.Background())
	if !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("an opener that failed answered %v, want ErrNoBrowser", err)
	}
	if !strings.Contains(err.Error(), "xdg-open: not found") {
		t.Errorf("the refusal is %q, which does not carry what the opener said", err.Error())
	}
	if len(s.tokenCalls()) != 0 {
		t.Error("a sign-in whose browser never opened exchanged a code")
	}
}

// TestAServerThatAnswersNothingReadableIsRefused holds what the flows do with
// an answer they do not read: a 200 carrying no key, and a device start
// carrying no code, are answers the site declined with — the local end of
// ErrRefused — rather than a bundle that reports a sign-in which never
// happened.
func TestAServerThatAnswersNothingReadableIsRefused(t *testing.T) {
	s := newSite(t)
	s.answer(func(_ deviceCall, w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"user_code":"ABCD-EFGH"}`)
	}, func(_ tokenCall, w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"renewal_secret":"a-secret-with-no-key"}`)
	}, nil)

	b := &browser{t: t}
	c := atSite(Client{Out: io.Discard, OpenURL: b.open}, s)
	bundle, err := c.Authorize(context.Background())
	if err == nil {
		t.Fatalf("an exchange the site answered with no key answered a bundle: %+v", bundle)
	}
	if !errors.Is(err, ErrRefused) {
		t.Errorf("an answer with no key ended with %v, want ErrRefused", err)
	}
	if !errors.Is(err, ErrRefused) || strings.Contains(err.Error(), testSecret) {
		t.Errorf("the refusal is %q, which repeats a value out of the answer", err.Error())
	}
	if _, err := c.Device(context.Background()); !errors.Is(err, ErrRefused) {
		t.Errorf("a device start with no code ended with %v, want ErrRefused", err)
	}
}
