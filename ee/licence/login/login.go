// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package login is the client half of `fugaro licence login` (plan K3.3): the
// browser sign-in of the specification's section 11.3 and the device code of
// section 11.4, spoken to the licence site's own routes (K2.4,
// ee/keyservice/signin.go). It is the one package under ee/licence/ a network
// reaches — the plan gives that to this file and keeps the verifier's package
// free of one — and the split it holds is the one between knowing the wire and
// owning the key: this package runs the two flows and answers the key and the
// renewal secret it was handed, and ee/register/licence_login.go decides what
// the installation sends, verifies what comes back against the project's own
// Firebase project id (TN2) and writes both halves (D2137).
//
// Six things are the reason this file reads the way it does, and each is a test
// in login_test.go:
//
//   - The loopback listener binds the literal 127.0.0.1 and one request: the
//     address is a constant of this package (loopbackAddress) and not a name a
//     resolver owns, because `localhost` is a name anything on the machine can
//     point elsewhere, and a listener on a wildcard address is a callback any
//     process on the network can claim.
//   - A callback whose state is not this flow's own is refused and **does not
//     end the flow**: the state is what binds the code to the terminal that
//     asked for it, so a local process that guessed the port must not be able
//     to spend the person's sign-in by hitting it first (RFC 8252 section 7.3).
//   - The PKCE verifier never leaves this process. The challenge in the
//     authorize address is its digest, the exchange is the one place the
//     verifier is sent, and the code the browser brought is worth nothing
//     without it.
//   - No line this package writes — the instructions a person reads, the page
//     the browser lands on, the server's own error log — carries the one-time
//     code, the verifier, the key or the renewal secret. The user code of the
//     device flow is the one code shown, and it is shown because a person has
//     to type it on the site.
//   - The device poll stops at the code's own expiry, whatever the site
//     answers: a poll that kept asking would be a terminal hanging on a code
//     nothing is waiting for any more.
//   - Both requests carry the installation's Firebase project id (K3.3's
//     amendment of 2026-11-13): the site binds the entitlement a person picks
//     to it, and the id is untrusted text on the page that picks — nothing
//     proves it (the portal's F37), and what it buys is the key's
//     `workspace_id`, which `licence set` then checks against the project's own
//     Firebase project id.
package login

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The listener's own shape. The address is the plan's own literal ("the
// loopback listener binds the literal 127.0.0.1") and port 0 is the kernel's
// own random port, so two sign-ins on one machine never fight for one. The path
// is K2.4's redirect target (`callbackPath` in signin.go, unexported there and
// pinned by TestLoopbackBindsOnlyTheLoopback here): a path this package chose
// for itself would be a listener the site's own redirect never lands on.
const (
	loopbackAddress = "127.0.0.1:0"
	loopbackPath    = "/callback"
)

// The bounds on one of these flows. The five minutes is the deadline K3.3's
// listener holds open, and it is the same five minutes K2.4's authorize code
// lives; the request timeout bounds one call to the site, and the answer bound
// is the key and the secret at the most (ee/register's own bound is the same
// size).
const (
	defaultDeadline       = 5 * time.Minute
	defaultRequestTimeout = 30 * time.Second
	maxAnswerBytes        = 16 << 10
	// loopbackHeaderTimeout is how long a connection may take to send its
	// request line and headers: a socket that opens and says nothing must not
	// hold one of the listener's slots for the whole window.
	loopbackHeaderTimeout = 10 * time.Second
	// browserOpenWait is how long the browser opener is waited for. See
	// openInBrowser for what the two outcomes mean.
	browserOpenWait = 5 * time.Second
)

// The device flow's own numbers, used when the site's start answer names none.
// They mirror K2.4's (signin.go's deviceCodeTTL and devicePollInterval) so that
// a deployment which answered a shape of its own is still polled at a rate its
// own per-code interval accepts, while the expiry is the bound the poll stops
// at whatever the site says about it.
const (
	defaultDeviceTTL    = 10 * time.Minute
	defaultPollInterval = 5 * time.Second
	// maxDeviceTTL bounds a site-chosen expiry: a code whose life is longer
	// than this is one this Fugaro will not sit in front of, and the terminal
	// is left with the answer rather than hanging.
	maxDeviceTTL = time.Hour
)

// randomBytes is what one of this flow's own values is made of: the state and
// the PKCE verifier, each 32 bytes of the system's randomness in base64url —
// 43 characters, which is inside RFC 7636's 43-to-128 bound and inside the
// bound K2.4 puts on a state (200 printable ASCII, and base64url's alphabet is
// a subset of the one it accepts).
const randomBytes = 32

// maxInstallationBytes is the length K2.4 reads of the installation name
// (signin.go's maxInstallBytes). A longer value is refused here rather than at
// the site, whose answer to it is the flow's uniform refusal and would leave a
// person reading a sentence about their sign-in when the fault is a project id
// in their own config.
const maxInstallationBytes = 64

// statusMax bounds a status word out of an answer before it reaches a sentence:
// a status is a field of a body, and a body is a place text from anywhere can
// arrive.
const statusMax = 40

// ErrRefused marks every answer in which the site declined to hand a key to
// this sign-in — the uniform sentence it gives a caller it will not serve —
// and, with it, the local endings of the same kind: a 200 that carried no key.
// It is a person's own state rather than a failure of the call, so the command
// answers it as a user error.
var ErrRefused = errors.New("the licence site did not hand a key to this sign-in")

// ErrExpired is a code, or the listener's window, closing before the sign-in
// was finished. Nothing was written and nothing is left running: the command
// answers it as a user error and names the flow that starts again.
var ErrExpired = errors.New("the sign-in was not finished before its code expired")

// ErrNoBrowser is the opener failing on this machine, which is the signal
// `licence login` falls back to the device code on: a browser that could not be
// started is not a sign-in that failed, and the plan gives the flow that reads
// a code off another machine for exactly this case.
var ErrNoBrowser = errors.New("no browser could be opened on this machine")

// Refusal is one request the site answered with a status that is not the
// flow's own answer: a 403 (the uniform refusal, which is also how a verified
// account holding no licence is told so), a 429 (the poll's own rate), or a
// failure of the site's.
//
// Body is what the site said, made one printable line and bounded
// (safetext.Field) and **not redacted**: the two sentences worth a person's
// time are the site's own account of their account, and the one place a key
// this process never held can come back from is a body. So the caller that
// owns the key redacts it before any of it is printed
// (register.licenceRedact), which is why this package returns it rather than
// printing it.
type Refusal struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("the licence site answered HTTP %d", r.Status)
}

// Bundle is what a finished flow hands back: the two lines every issue path
// shows once (D2135) — the key, and the renewal secret that replaces it, absent
// on an offline key.
type Bundle struct {
	Key           string
	RenewalSecret string
}

// Client is one sign-in: where the site is, which installation is asking, and
// where a person is told what to do. The zero value is not usable — ServerURL
// and FirebaseProject are what a sign-in is about.
type Client struct {
	// ServerURL is the licence site's address (the CLI's licenceServerURL).
	ServerURL string
	// FirebaseProject is the installation's Firebase project id: the value
	// both requests carry, and the one the site binds the entitlement a person
	// picks to (K3.3's amendment).
	FirebaseProject string
	// Out is where the flow tells the person what to do — the address to
	// visit and, in the device flow, the code to type. Nothing else is ever
	// written to it.
	Out io.Writer
	// OpenURL opens the authorize address in the person's browser. nil is
	// this package's own opener (openInBrowser), and a test drives the flow
	// with one that plays the browser and the site.
	OpenURL func(address string) error
	// HTTP is the client these calls are made with. nil is this package's own:
	// a request timeout and **no redirect followed**, which is the renewal
	// path's rule for the same reason — these requests carry the one-time code
	// and the verifier, and a redirect would re-send them to whatever host a
	// Location names.
	HTTP *http.Client
	// Deadline is how long the browser flow holds its listener open (the
	// plan's five minutes). 0 is defaultDeadline.
	Deadline time.Duration
}

// Authorize is section 11.3: the listener, the browser, the state the callback
// is checked against, the PKCE exchange, and the bundle at the end of it.
func (c *Client) Authorize(ctx context.Context) (Bundle, error) {
	base, err := c.server()
	if err != nil {
		return Bundle{}, err
	}
	installation, err := c.installation()
	if err != nil {
		return Bundle{}, err
	}
	state, err := randomToken()
	if err != nil {
		return Bundle{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return Bundle{}, err
	}
	listener, err := newLoopbackListener(state)
	if err != nil {
		return Bundle{}, err
	}
	defer listener.Close()
	address := base + keyservice.RouteAuthorize + "?" + authorizeQuery(listener.port, state, challenge(verifier), installation)
	fmt.Fprintf(c.out(), "Opening the licence site in your browser. Sign in there, and pick the licence this installation should run on.\n\n  %s\n\nIf the browser did not open, visit that address yourself.\n", address)
	if err := c.open(address); err != nil {
		return Bundle{}, fmt.Errorf("%w: %s", ErrNoBrowser, safetext.Field(err.Error()))
	}
	code, err := listener.wait(ctx, c.deadline())
	if err != nil {
		return Bundle{}, err
	}
	return c.exchange(ctx, code, verifier)
}

// Device is section 11.4: a code the site names, a person who types it
// somewhere with a browser, and the poll that stops at the code's expiry.
//
// What the person is shown is the user code and the address to type it at, and
// never the device code beside it: the user code is the half the site requires
// a person to type, which is what makes a page they did not open useless to
// whoever showed it to them, and the device code is the half only this process
// presents.
func (c *Client) Device(ctx context.Context) (Bundle, error) {
	installation, err := c.installation()
	if err != nil {
		return Bundle{}, err
	}
	var start struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := c.post(ctx, keyservice.RouteDeviceStart, deviceCall{Installation: installation}, &start); err != nil {
		return Bundle{}, err
	}
	if start.DeviceCode == "" || start.UserCode == "" {
		return Bundle{}, fmt.Errorf("%w: the site answered no device code", ErrRefused)
	}
	uri, err := verificationURI(start.VerificationURI)
	if err != nil {
		return Bundle{}, err
	}
	ttl := deviceTTL(start.ExpiresIn)
	interval := pollInterval(start.Interval)
	fmt.Fprintf(c.out(), "Open this address in a browser, on any machine:\n\n  %s\n\nand type this code when it asks:\n\n  %s\n\nThe code is worth %s. Waiting for the sign-in to be approved.\n", uri, safetext.Field(start.UserCode), ttl)
	deadline := time.Now().Add(ttl)
	for {
		var answer struct {
			Status        string `json:"status"`
			Key           string `json:"key"`
			RenewalSecret string `json:"renewal_secret"`
		}
		err := c.post(ctx, keyservice.RouteDevicePoll, devicePollCall{DeviceCode: start.DeviceCode}, &answer)
		var refusal *Refusal
		switch {
		case err == nil:
			switch answer.Status {
			case keyservice.DeviceOK:
				return bundleOf(answer.Key, answer.RenewalSecret)
			case keyservice.DevicePending:
			default:
				// A status is a field of a body, and this build reads two of
				// them: one it does not read is answered rather than waited on,
				// because a site that renamed one would otherwise leave the
				// terminal polling until the code expired.
				return Bundle{}, fmt.Errorf("the licence site answered a status this Fugaro does not read (%s)", safetext.Field(clipRunes(answer.Status, statusMax)))
			}
		case errors.As(err, &refusal) && refusal.Status == http.StatusTooManyRequests:
			// The site's own rate, kept per code (K2.4's pollDevice): the wait
			// it asks for is what this poll obeys rather than the interval,
			// which the site has just said is not what it is enforcing.
			if refusal.RetryAfter > 0 {
				interval = refusal.RetryAfter
			}
		default:
			return Bundle{}, err
		}
		if !time.Now().Before(deadline) {
			return Bundle{}, fmt.Errorf("%w: the site's code lived %s and no sign-in was approved for it", ErrExpired, ttl)
		}
		if err := waitContext(ctx, min(interval, time.Until(deadline))); err != nil {
			return Bundle{}, err
		}
	}
}

// exchange is section 11.3's last step: the one-time code, the PKCE verifier
// that earned it, and the bundle the site answers. The verifier is sent here
// and nowhere else, after the code has come back to this machine's own
// listener.
func (c *Client) exchange(ctx context.Context, code, verifier string) (Bundle, error) {
	var answer struct {
		Key           string `json:"key"`
		RenewalSecret string `json:"renewal_secret"`
	}
	if err := c.post(ctx, keyservice.RouteToken, tokenCall{Code: code, CodeVerifier: verifier}, &answer); err != nil {
		return Bundle{}, err
	}
	return bundleOf(answer.Key, answer.RenewalSecret)
}

// bundleOf is what a finished flow answers, and it refuses an answer that
// carried no key: a 200 with nothing in it is not a key, and a caller that read
// it as one would report a sign-in that did not happen.
func bundleOf(key, secret string) (Bundle, error) {
	if strings.TrimSpace(key) == "" {
		return Bundle{}, fmt.Errorf("%w: the site answered no key", ErrRefused)
	}
	return Bundle{Key: key, RenewalSecret: secret}, nil
}

// deviceCall is section 11.4's start request.
//
// The field is K2.4's (`installation`, signin.go) and the value is the
// installation's Firebase project id rather than a display name, which is the
// portal's amendment of 2026-11-13 (K3.3: "the request carries the
// installation's Firebase project id ... with the id shown as untrusted
// text"). The reason it is the value of the field there is rather than a field
// beside it is that the site reads this body strictly (keyservice.readCall
// refuses a field it does not know), so a second field is a body it answers
// with the flow's uniform refusal; and the reason it is the id at all is what
// the site does with it — the portal's own screens (P3.5) list the
// entitlements a signed-in account holds and bind the chosen one to the
// requesting installation's project id, which is the value they have. A name
// nothing could be bound to would mint a key whose `workspace_id` no
// installation matches, and `licence set` refuses one of those (TN2).
type deviceCall struct {
	Installation string `json:"installation"`
}

// devicePollCall is section 11.4's poll: the device code and nothing else. The
// user code is not sent — it is what the person types on the site, and the
// site is what compares it.
type devicePollCall struct {
	DeviceCode string `json:"device_code"`
}

// tokenCall is section 11.3's exchange: the one-time code and the verifier
// whose digest the authorize step recorded.
type tokenCall struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
}

// server is the site's address with one spelling of its trailing slash, and a
// refusal rather than a request for anything that is not an http(s) address: a
// sign-in carries a code and a verifier, and the one thing this package may not
// do is send them somewhere it cannot name.
func (c *Client) server() (string, error) {
	address := strings.TrimSpace(c.ServerURL)
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("the licence site's address (%s) is not an http(s) address, so nothing was sent to it", safetext.Field(address))
	}
	return strings.TrimSuffix(address, "/"), nil
}

// installation is the value both requests carry, checked against the bounds
// K2.4 reads it within: an address the site would refuse leaves a person
// reading the flow's uniform sentence when the fault is in their own config.
func (c *Client) installation() (string, error) {
	id := c.FirebaseProject
	if id == "" {
		return "", errors.New("this installation has no Firebase project id, and a key is bound to one (TN2): nothing was sent")
	}
	if len(id) > maxInstallationBytes {
		return "", fmt.Errorf("this installation's Firebase project id is %d bytes, and the licence site reads %d", len(id), maxInstallationBytes)
	}
	for i := 0; i < len(id); i++ {
		if ch := id[i]; ch < 0x20 || ch > 0x7e {
			return "", errors.New("this installation's Firebase project id carries a character the licence site does not read")
		}
	}
	return id, nil
}

// out is where a person is told what to do. A Client with no writer is one
// whose flow runs invisibly, which is a caller's mistake and not something this
// package can repair: it writes nowhere rather than to a stream it guessed.
func (c *Client) out() io.Writer {
	if c.Out == nil {
		return io.Discard
	}
	return c.Out
}

func (c *Client) deadline() time.Duration {
	if c.Deadline <= 0 {
		return defaultDeadline
	}
	return c.Deadline
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		Timeout:       defaultRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// post sends one JSON body to one of the site's routes and decodes the answer.
//
// A failure to reach the site, an answer that cannot be read and an answer this
// package cannot decode are plain errors, which the command answers as remote
// ones — nothing a person at the terminal did — while a status that is not 200
// is a Refusal, which is where the site's own sentence for that person lives.
// No error here carries the body of a 200: the decode is strict enough to tell
// a shape it does not know, and printing what a body held is the one thing this
// package leaves to the caller that redacts.
func (c *Client) post(ctx context.Context, route string, body, into any) error {
	base, err := c.server()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+route, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("the licence site could not be reached: %s", safetext.Field(err.Error()))
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	if err != nil {
		return fmt.Errorf("the licence site's answer could not be read: %s", safetext.Field(err.Error()))
	}
	if len(answer) > maxAnswerBytes {
		return fmt.Errorf("the licence site's answer is over %d bytes", maxAnswerBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return &Refusal{Status: resp.StatusCode, Body: safetext.Field(string(answer)), RetryAfter: retryAfter(resp)}
	}
	if err := json.Unmarshal(answer, into); err != nil {
		return errors.New("the licence site answered something this Fugaro does not read")
	}
	return nil
}

// retryAfter is the wait a 429 asks for, bounded to the flow's own ceiling: the
// header is the site's, and a site that named an hour is asking for a poll this
// Fugaro does not make — the code's own expiry ends the flow either way.
func retryAfter(resp *http.Response) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return 0
	}
	wait := time.Duration(seconds) * time.Second
	if wait > maxDeviceTTL {
		return maxDeviceTTL
	}
	return wait
}

// authorizeQuery is section 11.3's address, as the site's own route reads it:
// the loopback port the code may come back to, the state, the PKCE challenge
// and its method, and the installation's Firebase project id (K3.3's
// amendment), which the portal's own authorize screen is the page that reads
// (P3.5) — K2.4's route takes the three parameters it needs from the query and
// ignores the rest, so the id travels on the address a person's browser opens
// and is the value that screen binds the chosen entitlement to. It has the
// same name as the device flow's field for the same value, so one value has one
// spelling across the two requests of one flow.
func authorizeQuery(port, state, challenge, installation string) string {
	query := url.Values{}
	query.Set("port", port)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", pkceMethod)
	query.Set("installation", installation)
	return query.Encode()
}

// pkceMethod is the only challenge method K2.4 accepts (signin.go's own
// constant): `plain` is refused there rather than supported, because a plain
// challenge is the verifier itself.
const pkceMethod = "S256"

// challenge is RFC 7636's S256 challenge: the base64url of the SHA-256 digest
// of the verifier. It is what travels (the address a browser opens, a shell's
// history and a proxy's log among the places it lands) while the verifier stays
// in this process until the exchange.
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomToken is one of this flow's own 256-bit values, generated from the
// system's randomness and refused rather than weakened when that fails: a state
// with no entropy is a callback any local process can forge, and a verifier
// with none is a code any reader can exchange.
func randomToken() (string, error) {
	buf := make([]byte, randomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("the system's randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// verificationURI is where the device flow sends a person, and it is refused
// unless it is an absolute http(s) address: this string is the one thing the
// flow tells someone to type a code into, so a site answering a relative path,
// a `file:` address or nothing at all is answered by this package rather than
// pasted into a browser.
func verificationURI(raw string) (string, error) {
	address := strings.TrimSpace(raw)
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("the licence site answered no address to approve the code at, and one this Fugaro will not send a person to: nothing was typed anywhere")
	}
	return address, nil
}

// deviceTTL is how long the device flow waits, as the site's answer says and
// within this package's ceiling.
func deviceTTL(seconds int) time.Duration {
	ttl := time.Duration(seconds) * time.Second
	if ttl <= 0 || ttl > maxDeviceTTL {
		return defaultDeviceTTL
	}
	return ttl
}

// pollInterval is how long between polls, as the site's answer says: a site
// that names none, or names one this package refuses to hammer it at, is polled
// at K2.4's own interval — a zero here would be a poll loop, which the site
// answers with a 429 and every other terminal behind the same address shares.
func pollInterval(seconds int) time.Duration {
	interval := time.Duration(seconds) * time.Second
	if interval <= 0 || interval > maxDeviceTTL {
		return defaultPollInterval
	}
	return interval
}

// loginListen is net.Listen, and it is a variable so that the test that holds
// the listener to the loopback can pin the two arguments this package hands it
// ("tcp", "127.0.0.1:0") rather than a listener's address after the kernel has
// resolved one. Nothing else may call it.
var loginListen = net.Listen

// loopbackListener is section 11.3's local HTTP listener: it serves the one
// callback carrying this flow's state, answers every other request with a
// refusal, and hands the code to the flow that is waiting on it.
//
// It is a server of this package's own over a loopback address, and its error
// log is loginLog rather than the standard one: what a protocol error's own
// sentence can carry is a request line, and a request line is where a code
// would be — the rule that no line of this flow reaches a log is kept by
// giving the server nowhere to write.
type loopbackListener struct {
	port  string
	state string
	codes chan string
	srv   *http.Server
}

// loopbackHeaderTimeout is how long the listener waits for a request's own
// headers, and it is a constant of the server rather than a caller's: nothing
// about a sign-in should let one connection hold the listener for the window.
func newLoopbackListener(state string) (*loopbackListener, error) {
	ln, err := loginListen("tcp", loopbackAddress)
	if err != nil {
		return nil, fmt.Errorf("the loopback listener (%s) could not be opened: %w", loopbackAddress, err)
	}
	address, ok := ln.Addr().(*net.TCPAddr)
	if !ok || address.Port <= 0 {
		_ = ln.Close()
		return nil, fmt.Errorf("the loopback listener (%s) answered an address that is not a port", loopbackAddress)
	}
	l := &loopbackListener{
		port:  strconv.Itoa(address.Port),
		state: state,
		codes: make(chan string, 1),
	}
	l.srv = &http.Server{
		Handler:           http.HandlerFunc(l.serve),
		ErrorLog:          loginLog,
		ReadHeaderTimeout: loopbackHeaderTimeout,
	}
	go func() { _ = l.srv.Serve(ln) }()
	return l, nil
}

// loginLog is where the listener's server writes, and it is nowhere: this
// variable is the seam a test reads to hold the flow to "no line of this
// package's carries the code" even when a request is malformed enough for the
// server to have something to say.
var loginLog = log.New(io.Discard, "", 0)

// serve is the listener's whole behaviour, and what it does with a request is
// only ever this: read the method, the path and three query values, and answer
// a page that is a constant of this package. It never reads the body, never
// echoes a value it was given and never writes a line anywhere but the
// response, so a request carrying a code — this flow's or anybody's — leaves
// nothing behind here.
func (l *loopbackListener) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", loopbackCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet || r.URL.Path != loopbackPath {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, loopbackMissPage)
		return
	}
	query := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(l.state)) != 1 || query.Get("code") == "" {
		// A request carrying anything but this flow's own state is refused and
		// changes nothing: the flow is still waiting, and the person's own
		// callback is still the only one that ends it. The comparison is
		// constant time because it is a guess a local process can make as often
		// as it likes, and the sentence says as little about the state as the
		// site's own refusal says about a sign-in.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, loopbackRefusedPage)
		return
	}
	// The page is answered before the code is handed over, so a person's
	// browser is finished with whatever the exchange behind it does. The
	// channel holds one value: a second callback carrying this flow's state
	// after the first is answered is not a second sign-in, and the flow that is
	// already finishing with the first is not disturbed by it.
	select {
	case l.codes <- query.Get("code"):
	default:
	}
	_, _ = io.WriteString(w, loopbackDonePage)
}

// wait is the flow waiting for its callback. It answers the code, the caller's
// own cancellation, or the deadline: a listener that waited for ever would be a
// process a person has to kill, and the deadline is the plan's own five
// minutes.
func (l *loopbackListener) wait(ctx context.Context, deadline time.Duration) (string, error) {
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case code := <-l.codes:
		return code, nil
	case <-ctx.Done():
		return "", fmt.Errorf("the sign-in was interrupted and nothing was written: %w", ctx.Err())
	case <-timer.C:
		return "", fmt.Errorf("%w: the browser was given %s to finish the sign-in in, and the code it would have brought is worth the same", ErrExpired, deadline)
	}
}

func (l *loopbackListener) Close() error { return l.srv.Close() }

// The three pages the listener answers with, and none of them interpolates
// anything: what the browser shows is a constant, so no value a request carried
// can reach a page, and there is no escaping to get wrong.
const (
	loopbackCSP          = "default-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	loopbackDonePage     = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Fugaro</title></head><body><h1>Signed in</h1><p>The terminal has the sign-in. You can close this window and go back to it.</p></body></html>`
	loopbackRefusedPage  = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Fugaro</title></head><body><h1>This is not the sign-in's own callback</h1><p>Nothing was changed, and the terminal that started the sign-in is still waiting for it.</p></body></html>`
	loopbackMissPage     = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Fugaro</title></head><body><h1>Nothing is here</h1><p>Nothing was changed.</p></body></html>`
)

// open is the browser step: this package's own opener unless the caller brought
// one, which is what lets a test play the browser and the site.
func (c *Client) open(address string) error {
	if c.OpenURL != nil {
		return c.OpenURL(address)
	}
	return openInBrowser(address)
}

// openInBrowser hands the address to this machine's own opener and waits for it
// for browserOpenWait. The two outcomes are the two the flow needs:
//
//   - the opener exits inside that window with a failure — `xdg-open` on a
//     machine with no display exits at once — and the flow knows there is no
//     browser here, which is what ErrNoBrowser means and what the device code
//     is the fallback for;
//   - the opener is still running when the window closes, which is an opener
//     that has handed the address to something and is not waited for: a browser
//     in the foreground would otherwise hold the terminal for as long as a
//     person reads the page.
//
// The address is one argument and no shell is involved, so nothing in it is
// word-split or expanded: it was built by this package out of the site's own
// address and values this package generated.
func openInBrowser(address string) error {
	command, args := browserCommand(runtime.GOOS)
	cmd := exec.Command(command, append(args, address)...)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(browserOpenWait):
		return nil
	}
}

// browserCommand is the opener of one platform, named here so that the three
// spellings live in one place: a system with none of them is a system whose
// opener fails at exec and whose sign-in takes the device code.
func browserCommand(goos string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		return "xdg-open", nil
	}
}

// waitContext is a wait that a cancellation ends, so a sign-in the caller
// abandons does not leave a poll sleeping on it.
func waitContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// clipRunes bounds a value out of an answer before it reaches a sentence.
func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
