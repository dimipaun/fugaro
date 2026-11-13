// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The licence site's sign-in, authorize and device code (spec §11.3, §11.4;
// D2144, TN3): the two paths `fugaro licence login` (K3.3) reaches, and the
// first routes here that act for a person's own account rather than for a
// merchant's event, a key's own secret or the operator reading the admin page.
//
// What proves the person is a Firebase ID token of the **owner's own licence
// project** — never a customer's workspace project, whose token would prove only
// that somebody controls it (F14, D2144, TN3) — verified by the deployment's one
// sign-in verifier (`Deps.SignIn`, the portal's own seam) rather than by a
// second implementation of the same rules here. That verifier pins the audience
// to the project's bare id, the issuer to the same project, and
// `email_verified` to true, and the last of the three is not a detail here: it
// is the whole **purchaser binding**. A key is handed to the account whose
// verified address the licence was bought with, so a token asserting an address
// its holder never proved must not reach a key, and the verifier refuses it
// before this file reads a claim.
//
// Every form here carries no script and submits only to itself, so the
// deployment's CSP keeps `form-action 'self'` and the ID token travels in a POST
// body rather than in a URL — the rule D923 states for the coordinator's own
// authorize endpoint, read the same way. The authorize form's page adds one
// origin to that rule and no other page does: its submission is answered with
// the redirect to the CLI's listener, which a browser checks `form-action`
// against (signInRedirectCSP). The pages these routes render are this
// file's: the plan's amendment of 2026-11-13 gives the SPA its own sign-in
// screens (P3.5, `ee/portal/src/screens/cli/*`), and until those land the CLI's
// two browser steps are served from here.
//
// The codes are **this process's**. An authorize code lives five minutes and a
// device code ten, and what holds them is a map on the service rather than the
// store: a code that lives minutes is not a record anybody reads later. The
// price is named rather than hidden: two instances of this service do not share
// these maps, so a deployment that answers these routes from more than one
// process needs a table for them, which is a change to `store.go` and not this
// task's to make.

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
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The routes. The first four are the CLI's and carry what a program carries; the
// last is the one page a person reads, which is where a device code is approved
// (K3.3's "the device approve page shows the requesting installation's name as
// untrusted text and requires typing the user code").
const (
	// RouteAuthorize is §11.3's authorize step: a GET renders the sign-in form
	// and a POST is the sign-in itself, which answers with the redirect to the
	// CLI's loopback listener.
	RouteAuthorize = "/cli/authorize"
	// RouteToken is §11.3's step 5: the one-time code, exchanged with the PKCE
	// verifier for the key's two lines.
	RouteToken = "/cli/token"
	// RouteDeviceStart and RouteDevicePoll are §11.4's two calls, and
	// RouteDeviceApprove is where a signed-in person answers the code the CLI is
	// showing.
	RouteDeviceStart   = "/cli/device/code"
	RouteDevicePoll    = "/cli/device/token"
	RouteDeviceApprove = "/cli/device"
)

// The path §11.3's redirect lands on, and the only one this service will send a
// code to.
const callbackPath = "/callback"

// The lives and the rates of the two flows. The five minutes is the deadline
// K3.3's listener holds open and therefore the longest an authorize code may be
// useful; the device code's ten and the poll's five seconds are §11.4's shape,
// short enough that a code photographed off a terminal screen is worth nothing
// by the time it is typed somewhere else.
const (
	authorizeCodeTTL   = 5 * time.Minute
	deviceCodeTTL      = 10 * time.Minute
	devicePollInterval = 5 * time.Second
)

// The device poll's own bucket, and the one rate in this file rather than the
// deploy's (§7.2's caps are capacity decisions; this one is the flow's own
// shape). It is ten polls a minute for every one the flow asks on, which is one
// every half second sustained: room for several terminals behind one address,
// since a sign-in is twelve a minute, and still a bound on what a stranger can
// make an unauthenticated route do. It is derived from the interval rather than
// configured because a rate below the flow's own would break the flow — the
// start answer tells the CLI the interval, so a deployment that set the two
// apart would be telling the CLI something untrue.
var devicePollLimits = Limits{
	Requests: 10 * int(time.Minute/devicePollInterval),
	Window:   time.Minute,
}

// The bounds on what a caller may name. The state is the CLI's own random value
// and the challenge is RFC 7636's 43 to 128 characters; the installation is
// untrusted text a person reads on the approve page.
const (
	maxStateBytes    = 200
	minPKCEBytes     = 43
	maxPKCEBytes     = 128
	maxInstallBytes  = 64
	maxDeviceBytes   = 4 << 10
	userCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	userCodeLength   = 8
	userCodeGroup    = 4
)

// The statuses the poll answers with. They are the wire's words and not this
// package's: K3.3's CLI reads them, and `pending` is the one RFC 8628 already
// spells that way for the same reason.
const (
	DevicePending = "pending"
	DeviceOK      = "ok"
)

// ErrNoLicence is a signed-in account this service cannot hand a key to: it
// bought none, its subscription is cancelled, its term is over, or the record it
// bought under cannot be minted from at all — no workspace to bind (TN2), no
// organisation or seats, or a term the checkout does not sell (D442). It is one
// error for all of them because the answer to the person is one sentence: what
// the account holds and what the record says are the service's to repair or the
// owner's, and none of it is something a person at a terminal can act on.
var ErrNoLicence = errors.New("keyservice: the account holds no licence that is running")

// The sentences these routes refuse with. The sign-in's is **uniform** and
// names nothing — not the parameter that was wrong, not the token's own text and
// not whether the address is one this service knows — because a refusal is
// reachable by anyone and an answer that told a stranger which half of a guess
// was right would be a lookup. The device poll's is uniform for the same reason,
// over a code space small enough that a probe must not narrow it. Only a caller
// whose sign-in has been **verified** is told anything else, and what it is told
// is about its own account.
const (
	refuseSignIn    = "This sign-in could not be verified. Return to your terminal and try again."
	refuseDevice    = "This request cannot be answered."
	refuseNoLicence = "This account holds no licence that is running. Buy one on the licence site, then sign in again."
	refuseSlowDown  = "That device code is being polled too often."
)

// refuseSlowPoll carries the interval as Retry-After, so the CLI waits the
// time this deployment enforces rather than one it picks itself.
func refuseSlowPoll(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(devicePollInterval/time.Second)))
	http.Error(w, refuseSlowDown, http.StatusTooManyRequests)
}

// refuseSignInToken is the answer to every request these routes turn away
// before a sign-in is verified, whatever the fault was: one sentence and one
// status, so a caller cannot tell a bad port from a bad state from a token that
// does not verify. What a caller whose token did verify is told is about its own
// account alone.
func refuseSignInToken(w http.ResponseWriter) {
	http.Error(w, refuseSignIn, http.StatusForbidden)
}

func refuseDevicePoll(w http.ResponseWriter) {
	http.Error(w, refuseDevice, http.StatusForbidden)
}

// authorizeCode is one authorize request between its sign-in and its exchange:
// the subscription the account's key is minted for, the PKCE challenge the
// exchange is bound to, and the instant it stops working.
//
// What it does **not** carry is a redirect target, and that is the one thing an
// authorization code's leak turns on: the code went to the target the sign-in
// validated and the exchange reads none, so a caller that has read a code off a
// log or out of a browser's history has nothing to point it at — the only party
// that can exchange it is the one that already received it (redirectWithCode).
type authorizeCode struct {
	subscription string
	account      string
	challenge    string
	expires      time.Time
	spent        bool
}

// deviceCode is one device-code request (§11.4): the short code's digest, the
// installation's own name as untrusted text, and — once a signed-in person has
// approved it — the subscription the key is minted for.
type deviceCode struct {
	userCodeHash string
	installation string
	subscription string
	account      string
	expires      time.Time
	approved     bool
}

// signInCodes is what this process is holding for the two flows.
//
// The maps are keyed by the **digest** of the secret the caller presents, the
// way the store keys a verification (`Verification.ID`), so what a reader of
// this map holds is what a code was about and not the code itself. The user code
// is the exception and is hashed rather than kept whole for the same reason —
// what is compared is the digest of what the person typed — with the honest note
// that a short code cannot be hashed into safety: what bounds it is its ten
// minutes and the poll's rate, not the digest.
//
// Nothing here caps how many codes one process holds: what bounds the **code**
// tables is each code's own window, swept on the way past every write, and the
// intake's throttle in front of the two start routes. So that throttle is not
// optional there, and the rate it allows is the deploy's to set — a deployment
// under a flood of device starts holds as many entries as its own rate lets
// through in ten minutes.
//
// The poll route is the exception and needs no such help from the intake: it
// holds one entry per code this process is already holding (polls), it records
// nothing for a code it does not hold, and what a stranger can make it do is one
// lookup and one lock per request, behind the poll's own rate (Service.pollLimiter).
type signInCodes struct {
	mu      sync.Mutex
	codes   map[string]*authorizeCode
	devices map[string]*deviceCode
	// polls is when each device code **this process holds** was last polled,
	// keyed by the digest of the code the CLI presents. An entry is written only
	// for a code found in `devices` and is dropped by `sweep` when that code
	// ends, so the table can never hold more than the codes themselves — a code
	// the service never issued is answered and recorded nowhere, which is what
	// keeps the poll from being a way to grow this table or to make every other
	// route of the flow wait behind a sweep.
	polls map[string]time.Time
}

func newSignInCodes() *signInCodes {
	return &signInCodes{
		codes:   map[string]*authorizeCode{},
		devices: map[string]*deviceCode{},
		polls:   map[string]time.Time{},
	}
}

// keep writes one authorize code and sweeps the ones whose window has closed.
// The sweep is here — on the way past a write — because an authorize code is
// short-lived and a code nobody ever exchanges is the common case: a person who
// opens the browser and closes it again is the flow's ordinary ending.
func (c *signInCodes) keep(code string, held authorizeCode, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(now)
	c.codes[digest(code)] = &held
}

// spend is the exchange's whole check, under one lock: the code is looked up, it
// has to be inside its window and unspent, and the verifier presented has to hash
// to the challenge the authorize step recorded. Only then is it spent, and what
// the caller gets back is what the code was about.
//
// The order is the safe one and it is deliberate. A verifier that does not match
// spends nothing, so a caller who has read a code off a log — or a shoulder —
// cannot burn the holder's flow: the code is spent only by whoever can also
// prove the verifier, which is the half that never left the CLI. The comparison
// is constant time because a guess that returned early would say how much of a
// verifier was right.
func (c *signInCodes) spend(code, verifier string, now time.Time) (authorizeCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(now)
	held, found := c.codes[digest(code)]
	if !found || held.spent || !now.Before(held.expires) {
		return authorizeCode{}, false
	}
	if !pkceMatches(held.challenge, verifier) {
		return authorizeCode{}, false
	}
	held.spent = true
	return *held, true
}

// unspend hands a code back for the reason `verifyLink` hands back a link: a
// mint that failed is a key nobody received, and the person holding the terminal
// would otherwise have to start the whole sign-in again because the service's
// minter was briefly unhappy.
func (c *signInCodes) unspend(code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if held, found := c.codes[digest(code)]; found {
		held.spent = false
	}
}

// keepDevice writes one device code and sweeps the expired ones, for the reason
// keep sweeps.
func (c *signInCodes) keepDevice(device string, held deviceCode, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(now)
	c.devices[digest(device)] = &held
}

// pollDevice is the poll's whole exchange with the table, under one lock, and
// the order of the three things it does is what each of them needs:
//
//   - a code this process is **not holding** is answered without being recorded,
//     so the poll table only ever holds codes the start route issued. The poll
//     is reachable by anyone and takes the lock every other route of this flow
//     waits on, so a flood of guesses must not be able to grow that table or to
//     leave the sweep below with work to do: what a guesser costs is one lookup.
//   - the interval is kept **only for a code that is held**, so an unknown code
//     is answered the same however often it is asked about (`refuseDevicePoll`).
//     What that costs is the one difference left between a held code and a code
//     nobody issued — a held one polled twice inside the interval is the
//     slow-down — and the party who can see it is the one already holding 256
//     bits of the system's randomness, which the start route gave to nobody else.
//   - an approved code is taken out in the same read that finds it, so one
//     approval answers one key however slow the mint behind it is, and a mint
//     that fails hands it back (unspendDevice).
func (c *signInCodes) pollDevice(device string, now time.Time) (deviceCode, pollAnswer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(now)
	key := digest(device)
	held, found := c.devices[key]
	if !found {
		return deviceCode{}, pollUnknown
	}
	if at, seen := c.polls[key]; seen && now.Before(at.Add(devicePollInterval)) {
		return deviceCode{}, pollTooSoon
	}
	c.polls[key] = now
	if !held.approved {
		return *held, pollPending
	}
	delete(c.devices, key)
	return *held, pollApproved
}

// pollAnswer is what the poll's read of the table found: a code it is not
// holding, a code held and asked about inside the interval, one still waiting
// for its person, and one that person has approved.
type pollAnswer int

const (
	pollUnknown pollAnswer = iota
	pollTooSoon
	pollPending
	pollApproved
)

// unspendDevice hands an approved code back when the mint behind it failed, for
// the reason `unspend` and `verifyLink` hand theirs back: the person approved and
// no key arrived, and a terminal should not have to be approved again because
// the service's minter was briefly unhappy. A code whose window closed while the
// mint was running is not put back: it is over.
func (c *signInCodes) unspendDevice(device string, held deviceCode, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !now.Before(held.expires) {
		return
	}
	c.sweep(now)
	c.devices[digest(device)] = &held
}

// approve marks the code one signed-in person answered, and it is looked up by
// the **user code** the person typed and not by the device code, which the CLI
// holds and the person never sees. The scan is over the codes this process is
// holding inside their ten minutes, which is what makes a scan the honest shape.
func (c *signInCodes) approve(userCode, subscription, account string, now time.Time) (deviceCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(now)
	hash := digest(userCode)
	for _, held := range c.devices {
		if held.userCodeHash != hash || held.approved || !now.Before(held.expires) {
			continue
		}
		held.approved, held.subscription, held.account = true, subscription, account
		return *held, true
	}
	return deviceCode{}, false
}

// deviceByUserCode is what the confirmation page is rendered from: the code a
// person typed, read before anything about it is approved. It is read here and
// not left to the approval so that the page naming an installation is never a
// page about a code this service is not holding — a person who typed a code
// nothing is waiting on reads that they typed it wrong rather than a
// confirmation of something that will then be refused.
func (c *signInCodes) deviceByUserCode(userCode string, now time.Time) (deviceCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(now)
	hash := digest(userCode)
	for _, held := range c.devices {
		if held.userCodeHash == hash && !held.approved && now.Before(held.expires) {
			return *held, true
		}
	}
	return deviceCode{}, false
}

// sweep drops every code whose window has closed, and the poll bookkeeping that
// belongs to it. The caller holds the lock.
//
// The poll table is swept **here** and not by the poll that would have read it,
// because the two ways an entry can end are both endings of the code rather than
// of the poll: the code expires, or the code is claimed and answered (the mint
// may still be running, and `unspendDevice` puts both back together). So the
// poll table holds one entry per code this process is holding and never more:
// what bounds it is the start route's rate, not how many times somebody polls.
func (c *signInCodes) sweep(now time.Time) {
	for key, held := range c.codes {
		if !now.Before(held.expires) {
			delete(c.codes, key)
		}
	}
	for key, held := range c.devices {
		if !now.Before(held.expires) {
			delete(c.devices, key)
		}
	}
	for key := range c.polls {
		if _, held := c.devices[key]; !held {
			delete(c.polls, key)
		}
	}
}

// authorize serves §11.3's route: the GET renders the sign-in form and the POST
// is the sign-in.
func (s *Service) authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.authorizeForm(w, r)
		return
	}
	s.authorizeSubmit(w, r)
}

// authorizeForm validates §11.3's parameters and renders the form the person
// signs in on.
//
// What it validates here is not a decision — the POST validates the same three
// again and trusts none of this — it is the answer to a browser that was sent to
// a redirect this service will not use: the person is told at the door rather
// than after signing in, and the CLI's own loopback port is refused the moment
// it is not one.
func (s *Service) authorizeForm(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	target, ok := loopbackRedirect(query.Get("redirect_uri"), query.Get("port"))
	if !ok {
		refuseSignInToken(w)
		return
	}
	state, ok := authorizeState(query.Get("state"))
	if !ok {
		refuseSignInToken(w)
		return
	}
	challenge, ok := pkceChallenge(query.Get("code_challenge"), query.Get("code_challenge_method"))
	if !ok {
		refuseSignInToken(w)
		return
	}
	view := signInView{Target: target, State: state, Challenge: challenge, Method: pkceMethod}
	s.renderSignIn(w, signInTemplate, view, signInRedirectCSP(target))
}

// authorizeSubmit is the sign-in itself: the ID token is verified, the account's
// own licence is found, and a one-time code bound to the validated redirect and
// to the PKCE challenge is minted and sent back to the CLI's listener.
//
// Nothing is minted here. The code is what the exchange holds, and a browser
// that never follows the redirect — the ordinary ending of a person who changes
// their mind — then leaves no key behind at all: what expires is a code.
func (s *Service) authorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("ip:" + clientIP(r)) {
		refuseSignInToken(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		refuseSignInToken(w)
		return
	}
	form := r.PostForm
	target, ok := loopbackRedirect(form.Get("redirect_uri"), "")
	if !ok {
		refuseSignInToken(w)
		return
	}
	state, ok := authorizeState(form.Get("state"))
	if !ok {
		refuseSignInToken(w)
		return
	}
	challenge, ok := pkceChallenge(form.Get("code_challenge"), form.Get("code_challenge_method"))
	if !ok {
		refuseSignInToken(w)
		return
	}
	caller, ok := s.verifySignIn(r.Context(), form.Get("id_token"))
	if !ok {
		refuseSignInToken(w)
		return
	}
	sub, held, err := s.accountLicence(r.Context(), caller.Email)
	if err != nil {
		fail(w, err)
		return
	}
	if !held {
		http.Error(w, refuseNoLicence, http.StatusForbidden)
		return
	}
	code, err := newSecret()
	if err != nil {
		fail(w, err)
		return
	}
	now := s.now()
	s.codes.keep(code, authorizeCode{
		subscription: sub.ID,
		account:      caller.Email,
		challenge:    challenge,
		expires:      now.Add(authorizeCodeTTL),
	}, now)
	s.redirectWithCode(w, r, target, code, state)
}

// token is §11.3's step 5: the code comes back with the PKCE verifier and is
// exchanged for the key's two lines — the key and the renewal secret that
// replaces it, which is the bundle every issue path shows once (D2135).
func (s *Service) token(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("ip:" + clientIP(r)) {
		refuseSignInToken(w)
		return
	}
	var call struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	if !readCall(w, r, &call) {
		refuseSignInToken(w)
		return
	}
	held, ok := s.codes.spend(call.Code, call.CodeVerifier, s.now())
	if !ok {
		refuseSignInToken(w)
		return
	}
	minted, err := s.signInMint(r.Context(), held.subscription, held.account)
	if err != nil {
		// The code is handed back, so a person whose exchange failed on the
		// service's side retries the exchange rather than signing in again —
		// and the verifier is still the one that earned it, which is what makes
		// handing it back safe.
		s.codes.unspend(call.Code)
		s.answerMintFailure(w, err)
		return
	}
	writeBundle(w, tokenAnswer{Key: minted.Key, RenewalSecret: minted.RenewalSecret})
}

// tokenAnswer is what the exchange answers: the two lines of the bundle and
// nothing else. It carries no status field, because there is one answer here
// that is not a refusal and a refusal is the uniform sentence.
type tokenAnswer struct {
	Key           string `json:"key"`
	RenewalSecret string `json:"renewal_secret"`
}

// deviceStart is §11.4's first call: a device code, the short code a person
// types, and where to type it.
//
// The verification address is built from the intake's own configured origin and
// never from the request's Host header (§7.2's rule for the verification link,
// which a page a person is sent to needs even more).
func (s *Service) deviceStart(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("ip:" + clientIP(r)) {
		refuseDevicePoll(w)
		return
	}
	var call struct {
		Installation string `json:"installation"`
	}
	if !readCall(w, r, &call) {
		refuseDevicePoll(w)
		return
	}
	installation, ok := untrustedName(call.Installation)
	if !ok {
		refuseDevicePoll(w)
		return
	}
	device, err := newSecret()
	if err != nil {
		fail(w, err)
		return
	}
	userCode, err := newUserCode()
	if err != nil {
		fail(w, err)
		return
	}
	now := s.now()
	s.codes.keepDevice(device, deviceCode{
		userCodeHash: digest(userCode),
		installation: installation,
		expires:      now.Add(deviceCodeTTL),
	}, now)
	writeBundle(w, struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}{
		DeviceCode:      device,
		UserCode:        groupCode(userCode),
		VerificationURI: strings.TrimSuffix(s.cfg.URL, "/") + RouteDeviceApprove,
		ExpiresIn:       int(deviceCodeTTL / time.Second),
		Interval:        int(devicePollInterval / time.Second),
	})
}

// devicePoll is §11.4's third step: the CLI asks until the code is approved,
// and is answered `pending` until it is, the bundle once it is, and one uniform
// sentence for every code this service cannot answer about at all.
//
// The key is minted here and not at the approval, which is the same order the
// authorize route takes and for the same reason: what an approval records is
// whose licence it is, and a person who approves a code that is never polled
// again — the ordinary ending of a terminal that was closed — leaves no key
// behind at all.
//
// It is the one route of this flow that is **not** on the intake's own rate
// (§7.2). The CLI is told to ask every five seconds for the code's ten minutes,
// so one sign-in is up to 120 requests — four times that hour's whole budget for
// one address, on a bucket the mail forms share: a person who signs in would
// lock themselves and everyone behind the same address out of the forms, and the
// poll would end in the intake's uniform refusal, which a CLI cannot tell from a
// code that has expired. What bounds it instead is its own rate per address
// (pollLimiter), the interval kept per code (pollDevice), and the code itself,
// which is 256 bits of the system's randomness and not a thing anybody guesses
// at. The rate is checked first, so a flood is turned away before it reaches the
// table every other route of the flow takes the lock on, and both refusals say
// what to do — 429 and the interval — rather than the uniform sentence a CLI
// cannot tell from a code that has expired.
func (s *Service) devicePoll(w http.ResponseWriter, r *http.Request) {
	if !s.pollLimiter.allow("ip:" + clientIP(r)) {
		refuseSlowPoll(w)
		return
	}
	var call struct {
		DeviceCode string `json:"device_code"`
	}
	if !readCall(w, r, &call) {
		refuseDevicePoll(w)
		return
	}
	held, answer := s.codes.pollDevice(call.DeviceCode, s.now())
	switch answer {
	case pollUnknown:
		refuseDevicePoll(w)
		return
	case pollTooSoon:
		refuseSlowPoll(w)
		return
	case pollPending:
		writeBundle(w, deviceAnswer{Status: DevicePending})
		return
	}
	minted, err := s.signInMint(r.Context(), held.subscription, held.account)
	if err != nil {
		// The code is handed back for the reason the exchange hands its own
		// back: a mint that failed is a key nobody received, and the person
		// already approved this installation once.
		s.codes.unspendDevice(call.DeviceCode, held, s.now())
		s.answerMintFailure(w, err)
		return
	}
	writeBundle(w, deviceAnswer{Status: DeviceOK, Key: minted.Key, RenewalSecret: minted.RenewalSecret})
}

// deviceAnswer is what the poll answers: `pending` while the person has not
// answered, and the bundle once they have (K3.3 reads both).
type deviceAnswer struct {
	Status        string `json:"status"`
	Key           string `json:"key,omitempty"`
	RenewalSecret string `json:"renewal_secret,omitempty"`
}

// deviceApprove is §11.4's second step, and it takes two requests on purpose.
//
// The first is the person typing the code the terminal is showing; the second is
// the confirmation that names the installation in words. Splitting them is what
// makes K3.3's page honest: the name is shown **before** the licence is handed
// over, and it is the whole defence against a code that was typed somewhere
// other than where it was printed — a person who reads "acme-laptop is asking
// for a licence" and does not recognise it submits nothing.
//
// Both requests carry the ID token in the body, because there is no session here
// to carry it for them: the confirm step re-verifies it rather than trusting the
// page that rendered it.
func (s *Service) deviceApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.renderSignIn(w, deviceFormTemplate, signInView{}, signInCSP)
		return
	}
	if !s.limiter.allow("ip:" + clientIP(r)) {
		refuseDevicePoll(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		refuseDevicePoll(w)
		return
	}
	form := r.PostForm
	caller, ok := s.verifySignIn(r.Context(), form.Get("id_token"))
	if !ok {
		refuseSignInToken(w)
		return
	}
	userCode, ok := userCodeKey(form.Get("user_code"))
	if !ok {
		refuseDevicePoll(w)
		return
	}
	sub, held, err := s.accountLicence(r.Context(), caller.Email)
	if err != nil {
		fail(w, err)
		return
	}
	if !held {
		http.Error(w, refuseNoLicence, http.StatusForbidden)
		return
	}
	if form.Get("confirm") == "" {
		// The confirmation is a read of what the code is about, and it is asked
		// before anything is approved: a code that is not live is refused here
		// exactly as it is refused at the approval, so the page the person reads
		// is never a page about a code this service is not holding.
		asking, found := s.codes.deviceByUserCode(userCode, s.now())
		if !found {
			refuseDevicePoll(w)
			return
		}
		s.renderSignIn(w, deviceConfirmTemplate, confirmView{
			Installation: asking.installation,
			Organisation: sub.Organisation,
			// What the page carries back is the code as this service read it
			// and not the text that was typed: the two are the same code, and
			// what travels on is the normalised one (userCodeKey).
			UserCode: groupCode(userCode),
			IDToken:  form.Get("id_token"),
		}, signInCSP)
		return
	}
	approved, found := s.codes.approve(userCode, sub.ID, caller.Email, s.now())
	if !found {
		refuseDevicePoll(w)
		return
	}
	s.renderSignIn(w, deviceDoneTemplate, confirmView{
		Installation: approved.installation,
		Organisation: sub.Organisation,
	}, signInCSP)
}

// verifySignIn verifies the ID token a form carried and answers the caller it
// attests, or false for every reason it was refused.
//
// The seam is the portal's own (`Deps.SignIn`, P1.0, P1.3), and the rules the
// purchaser binding rests on — the audience, the issuer, the signature and
// `email_verified` — are the ones its contract states it checked before it
// answers a caller (api.go): both this flow and the SPA's are answered by one
// verifier, because two would be two answers about whose account a token names.
//
// A deployment holding no verifier is a deployment that can hand nobody a key,
// and it is refused here rather than at the door: the routes' answer is the
// sign-in's own uniform sentence, which is what an unconfigured service has to
// say to a person either way.
func (s *Service) verifySignIn(ctx context.Context, raw string) (Caller, bool) {
	if s.signIn == nil {
		return Caller{}, false
	}
	caller, err := s.signIn.Verify(ctx, raw)
	if err != nil {
		// What is logged is the verifier's own sentence and never the token: it
		// names the rule that refused (the audience, the issuer, the email, the
		// window) and no value out of the token, which is the rule the verifier's
		// own refusals keep.
		log.Printf("keyservice: the site's sign-in refused an ID token: %v", err)
		return Caller{}, false
	}
	return caller, true
}

// accountLicence is the purchaser binding: the subscription a signed-in
// account's **verified** address bought.
//
// The address is the whole of the check, and it is the verified one or nothing —
// the verifier refuses a token whose address its holder has not proved, so a
// caller cannot bind itself to somebody else's purchase by asserting their
// address. Where an account has bought more than one, the newest subscription is
// the one answered; picking among several, and the seats the account holds
// rather than bought, are the account page's (K5.1).
//
// What "running" is read from is the subscription's own record, and that is a
// gap this route has rather than one it makes: §8's refund and chargeback write
// the revocation list for the key the event names and leave the subscription
// uncancelled (merchant.go's `withdrawn`, which says why — what a refund ends is
// the owner's decision, not a repair for that path to make), so an address that
// was refunded can sign in and collect a fresh key for the rest of the term, and
// a key this route minted before the refund is not named by any entry either.
// This route is what makes the gap self-serve: the refunded address can ask for
// another key whenever it likes and as often as it likes, so §8's entry for the
// key the refund named is left behind on the first one — an eviction of D2131's
// revocation that needs nobody's help. The fix is one write on the merchant's
// side — the withdrawal recorded on the subscription (K2.5, K2.2), which this
// function and signInMint already refuse on — and until it exists
// TestTokenFromARefundedSubscriptionStillSignsIn measures the behaviour. What
// `withdrawn` leaves to the pairing the deploy owes (S3.7) is the other half, and
// the test pins that too: the merchant's cancellation, the event §5.3 says stops
// the reissue, stops the same sign-in.
//
// A sign-in adds a **door** to that hole and not the hole itself: `withdrawn`'s
// own comment records that a renewal and a seat change inside the window already
// mint under a fresh id no entry names. What can close it is a record of which
// key a subscription holds, which the store does not keep — Record, Revocation
// and KeyState carry no subscription (store.go) — and merchant.go says making
// the list stop a reissue is the owner's decision (D2131, §5.3, §8, S3.7). So
// closing it belongs to the merchant half or to the owner's ruling, not to a
// repair here: reading the list for the issuances **this flow** minted is the
// half of the second option that is available, and it catches nothing here — the
// merchant's refund names the key its own record holds, which is the purchase's,
// not a sign-in's — while a pairing guessed from the workspace or the address
// would refuse a customer whose earlier key was refunded and who has bought
// again, or one whose key leaked: a leak is revoked and reissued under the next
// key_id (§8), so a workspace's revoked keys are not a statement about who is
// entitled to sign in. What closes a refund is knowing which key a subscription
// holds, and that is the record the store has never kept.
//
// The portal that replaces this reading closes it by keeping that record: a
// refund resolves to the entitlement (P4.2) and revocation is by entitlement
// (P3.6, F29, `TestRevocationCoversEveryRotation`), which is the thing this route
// would then read — so what is missing is that model and not a check this file
// could invent from the subscription alone.
func (s *Service) accountLicence(ctx context.Context, email string) (Subscription, bool, error) {
	subscriptions, err := s.store.Subscriptions(ctx)
	if err != nil {
		return Subscription{}, false, err
	}
	found, held := Subscription{}, false
	for _, candidate := range subscriptions {
		if candidate.Cancelled || !sameAddress(candidate.Email, email) {
			continue
		}
		if !held || candidate.At.After(found.At) {
			found, held = candidate, true
		}
	}
	return found, held, nil
}

// signInMint mints the key a signed-in account is handed: the subscription's own
// workspace, organisation and seats, the row its term is sold under, and the rest
// of the term the customer is in.
//
// The term is the rest of the paid one and never a fresh period. What a person
// who signs in holds is a licence they have already paid for, and a login that
// minted a new period would hand out a term nobody bought — the same term the
// merchant's own renewal mints, read from the same record. A subscription past
// its end is refused rather than extended, and the key that ends at the
// subscription's own end still carries its row's grace (D2133), so a customer in
// grace collects their key and a customer who has lapsed is told so.
//
// The state is written before the record, the recoverable order the renewal
// endpoint states: a failure between the two leaves a key the store can revoke
// and nothing can renew, which is the half to lose.
//
// What it does **not** do is rotate the key the account already holds, which is
// what K2.3's rule for a re-download asks of both of these issue paths. The link
// a rotation reads is the issuance record's own source, and it exists for a key
// **this flow** minted (`signinSource`); it does not exist for the licence's
// other keys, because a purchase, a seat change and a renewal name their record
// after the merchant's event or the rotated key (renew.go's `rotationSource`)
// and no field of a key's record or of its state names the subscription it was
// sold for. A rotation written here would therefore end an earlier sign-in's
// bundle and leave the rest of the licence's bundles live beside it, still
// renewable — a leaked key the next sign-in does not evict, which is worse than
// a gap that is named. Making a sign-in really replace a licence's bundle means
// recording which key a subscription currently holds and saying what that does
// to a key another machine is already renewing, and that is the owner's decision
// about what a re-download is rather than a repair here.
//
// The portal that replaces this reading places K2.3's rule elsewhere: under it a
// sign-in **binds** an entitlement rather than handing a licence its bundle
// (D2196, P3.5's screens, K3.3), the secret rotates with the binding (P3.1's
// `TestRebindRotatesTheSecret`) and with the account page's reveal, which is
// where the 24-hour handover K2.3 names is realized at all (P3.2, D2182 as
// amended: one a day, after a fresh sign-in, the other owners mailed). Rotating
// here as well would be a second rotation writer beside renew.go's, over a
// secret the portal's own model says this flow does not hold.
//
// TestTokenFromASecondSignInLeavesTheEarlierKeyLive pins both halves.
func (s *Service) signInMint(ctx context.Context, subscriptionID, account string) (MintedV1, error) {
	if s.mintsV1 == nil {
		return MintedV1{}, errors.New("the intake has no v1 minter, so the site's sign-in has no key to hand over (Check 174)")
	}
	sub, held, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return MintedV1{}, err
	}
	if !held {
		return MintedV1{}, fmt.Errorf("%w: no subscription %s is recorded", ErrNoLicence, subscriptionID)
	}
	// What stops a subscription here is the merchant's cancellation and nothing
	// else — not a refund, which revokes the key it names and leaves this record
	// as it was (accountLicence's own note on the gap).
	if sub.Cancelled {
		return MintedV1{}, fmt.Errorf("%w: the subscription %s is cancelled", ErrNoLicence, sub.ID)
	}
	policy, sold := checkoutPolicy(sub.TermDays)
	if !sold {
		return MintedV1{}, fmt.Errorf("%w: the subscription %s is for a term the checkout does not sell (D442)", ErrNoLicence, sub.ID)
	}
	if sub.WorkspaceID == "" {
		return MintedV1{}, fmt.Errorf("%w: the subscription %s names no workspace, and every key binds to one (TN2)", ErrNoLicence, sub.ID)
	}
	if sub.Organisation == "" || sub.Seats < 1 {
		return MintedV1{}, fmt.Errorf("%w: the subscription %s names no organisation or no seats", ErrNoLicence, sub.ID)
	}
	days := daysBetween(s.now(), sub.Expires)
	if days < 1 {
		return MintedV1{}, fmt.Errorf("%w: the subscription %s ended at %s", ErrNoLicence, sub.ID, sub.Expires.UTC().Format(time.RFC3339))
	}
	minted, err := s.mintsV1.MintV1(ctx, RequestV1{
		Environment:  s.cfg.Environment,
		Policy:       policy,
		WorkspaceID:  sub.WorkspaceID,
		Organisation: sub.Organisation,
		Seats:        sub.Seats,
		TermDays:     days,
	})
	if err != nil {
		return MintedV1{}, err
	}
	if err := s.store.SaveKeyState(ctx, KeyState{
		KeyID:             minted.Record.KeyID,
		WorkspaceID:       sub.WorkspaceID,
		RenewalSecretHash: digest(minted.RenewalSecret),
		LastSeen:          s.now(),
	}); err != nil {
		return MintedV1{}, err
	}
	record := minted.Record
	record.Source = signInSource(sub.ID, record.KeyID)
	record.Email = account
	if err := s.store.SaveIssuance(ctx, record); err != nil {
		return MintedV1{}, err
	}
	return minted, nil
}

// signInSource names the record one sign-in writes. It carries the key's own id
// rather than an instant, because the key id is fresh on every mint: two
// downloads inside the same second are two records rather than one source the
// store refuses the second of.
func signInSource(subscriptionID, keyID string) string {
	return "signin:" + subscriptionID + ":" + keyID
}

// answerMintFailure answers a mint that could not be finished: a licence the
// account does not hold is the person's own state and is told plainly, and
// everything else is the service failing, which is the one thing these routes
// answer with a status of their own.
func (s *Service) answerMintFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNoLicence) {
		http.Error(w, refuseNoLicence, http.StatusForbidden)
		return
	}
	fail(w, err)
}

// redirectWithCode sends §11.3's step 4: the code and the state back to the
// CLI's listener.
//
// The target is the one the caller asked for, and the whole of what makes it
// safe is that it came out of `loopbackRedirect` in the same request, which
// rebuilds a loopback address from a validated port — this function echoes and
// judges nothing. That matters because a code is a bearer credential for one
// exchange: one sent to an address the caller could still choose would be the
// key itself, so any new caller here has to pass a target that came from there.
func (s *Service) redirectWithCode(w http.ResponseWriter, r *http.Request, target, code, state string) {
	address, err := url.Parse(target)
	if err != nil {
		fail(w, err)
		return
	}
	query := address.Query()
	query.Set("code", code)
	query.Set("state", state)
	address.RawQuery = query.Encode()
	http.Redirect(w, r, address.String(), http.StatusFound)
}

// readCall decodes one of these routes' JSON bodies: under this service's own
// bound and strictly, the way every other wire format here is read. It answers
// false for everything it will not read, and the caller answers the uniform
// sentence — which of a caller's mistakes it was is not something these routes
// tell.
func readCall(w http.ResponseWriter, r *http.Request, into any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDeviceBytes))
	if err != nil {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(into) == nil
}

// writeBundle writes one of these routes' answers. It is writeAnswer with the
// one header these answers need and the minter's own route does not: every one
// of them carries a credential — a key and its renewal secret, a device code, a
// short code — and a shared cache is not a place any of them belongs.
func writeBundle(w http.ResponseWriter, minted any) {
	w.Header().Set("Cache-Control", "no-store")
	writeAnswer(w, minted)
}

// loopbackRedirect reads the target §11.3's code may be sent to, from either
// spelling the flow has: `port`, which is what K3.3's URL carries, or a
// `redirect_uri`, which is the same thing written out. The two together have to
// agree, so a request that names both cannot be read two ways.
//
// What it answers is rebuilt from the port and never echoed back, so nothing a
// caller wrote survives into the Location header.
func loopbackRedirect(redirectURI, port string) (string, bool) {
	if redirectURI == "" {
		return loopbackCallback(port)
	}
	fromPort, ok := loopbackCallback(port)
	if port != "" {
		if !ok || fromPort != redirectURI {
			return "", false
		}
		return fromPort, true
	}
	address, err := url.Parse(redirectURI)
	if err != nil {
		return "", false
	}
	if address.Scheme != "http" || address.Opaque != "" || address.User != nil ||
		address.RawQuery != "" || address.Fragment != "" || address.Path != callbackPath {
		return "", false
	}
	if host, _, err := splitAuthority(address.Host); err != nil || host != "127.0.0.1" {
		return "", false
	}
	return loopbackCallback(address.Port())
}

// loopbackCallback is the one redirect target §11.3's flow uses: the literal
// loopback address K3.3's listener binds, on the port it bound, on the path the
// spec writes. A port spelled any other way than its digits — padded, signed,
// or with anything around it — is refused rather than normalised, because what
// it would be normalised to is a target the CLI's own listener is not on.
func loopbackCallback(port string) (string, bool) {
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
		return "", false
	}
	return "http://127.0.0.1:" + port + callbackPath, true
}

// splitAuthority reads a URL authority as the host and port it is, refusing the
// spellings that name anything but a literal address: `localhost` reaches the
// loopback by a name the resolver owns, and a host that is a name is a redirect
// this service will not send a code to.
func splitAuthority(authority string) (host, port string, err error) {
	if authority == "" || strings.ContainsAny(authority, "@/\\") {
		return "", "", errors.New("the authority is not an address")
	}
	host, port = authority, ""
	if index := strings.LastIndexByte(authority, ':'); index >= 0 {
		host, port = authority[:index], authority[index+1:]
	}
	if host != "127.0.0.1" {
		return "", "", errors.New("the authority is not the loopback address")
	}
	return host, port, nil
}

// authorizeState reads the CLI's own `state`: a value it generates, hands over
// and compares when the callback arrives. It is required and it is printable
// ASCII within a header's bound, because it travels back in a redirect's query
// and a value carrying a control character is one a client's URL parser reads
// differently from this service's.
func authorizeState(state string) (string, bool) {
	if state == "" || len(state) > maxStateBytes {
		return "", false
	}
	for i := 0; i < len(state); i++ {
		if c := state[i]; c < 0x21 || c > 0x7e {
			return "", false
		}
	}
	return state, true
}

// pkceMethod is the only challenge method this service accepts. `plain` is
// refused rather than supported: a plain challenge is the verifier itself, so a
// caller that could ask for it could read the code out of the authorize URL and
// exchange it without ever holding the verifier (RFC 7636 §7.2).
const pkceMethod = "S256"

// pkceChallenge reads §11.3's PKCE challenge: exactly the method this service
// accepts, and the base64url of a SHA-256 digest of the verifier.
func pkceChallenge(challenge, method string) (string, bool) {
	if method != pkceMethod || len(challenge) < minPKCEBytes || len(challenge) > maxPKCEBytes {
		return "", false
	}
	for i := 0; i < len(challenge); i++ {
		switch c := challenge[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
		default:
			return "", false
		}
	}
	return challenge, true
}

// pkceMatches answers whether a presented verifier is the one a challenge was
// made from, in constant time: a comparison that returned early would say how
// much of a verifier was right, and the verifier is the only thing standing
// between a reader of the code and the key.
func pkceMatches(challenge, verifier string) bool {
	if len(verifier) < minPKCEBytes || len(verifier) > maxPKCEBytes {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(challenge), []byte(base64.RawURLEncoding.EncodeToString(sum[:]))) == 1
}

// newUserCode is the short code §11.4's person reads off the terminal and types
// into the page: eight characters of the alphabet that leaves out the pairs a
// person confuses (I and 1, O and 0), without the separator the page shows it
// with.
//
// Its entropy is the code's own bound and the reason the poll is rate-limited
// and the code expires: eight of thirty-two symbols is forty bits, which is
// nothing to guess and everything to type.
func newUserCode() (string, error) {
	raw := make([]byte, userCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("the system's randomness: %w", err)
	}
	code := make([]byte, 0, userCodeLength)
	for _, b := range raw {
		// The alphabet is thirty-two symbols and a byte has two hundred and
		// fifty-six values, so the reduction is exact and no symbol is likelier
		// than another.
		code = append(code, userCodeAlphabet[int(b)%len(userCodeAlphabet)])
	}
	return string(code), nil
}

// groupCode is the code as the terminal shows it and the page asks for it, in
// two groups of four: what a person reads back letter by letter is read more
// wrongly the longer it runs, and the separator is where they lose their place.
//
// It is a rendering and never the value: what the service holds and compares is
// userCodeKey's normalised form, so the separator is decoration a typist may
// leave out.
func groupCode(code string) string {
	if len(code) <= userCodeGroup {
		return code
	}
	return code[:userCodeGroup] + "-" + code[userCodeGroup:]
}

// userCodeKey normalises the code a person typed: case is not a distinction a
// person reading a screen is asked to keep, the group separator is dropped, and
// nothing else is repaired — a code that is not exactly one of this service's is
// refused rather than trimmed into one.
func userCodeKey(code string) (string, bool) {
	trimmed := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if len(trimmed) != userCodeLength {
		return "", false
	}
	for i := 0; i < len(trimmed); i++ {
		if !strings.ContainsRune(userCodeAlphabet, rune(trimmed[i])) {
			return "", false
		}
	}
	return trimmed, true
}

// untrustedName reads the installation's own name, which is text a stranger
// wrote and a person reads on the approve page. The template escapes it and
// this bounds it: printable ASCII within a name's length, so what the page
// carries is a name and never a document.
func untrustedName(name string) (string, bool) {
	if name == "" {
		return "", true
	}
	if len(name) > maxInstallBytes {
		return "", false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c > 0x7e {
			return "", false
		}
	}
	return name, true
}

// sameAddress answers whether two addresses are the same one, folded to lower
// case as ASCII for the reason the verifier's own fold is ASCII-only: a Unicode fold
// would match a spelling the sign-in never proved. Both sides are printable
// ASCII already — the forms' and the merchant's grammar on one side, the
// verifier's own fold on the other — so this fold is exact rather than
// approximate.
func sameAddress(a, b string) bool {
	return a != "" && strings.ToLower(a) == strings.ToLower(b)
}

// signInCSP is what these pages may load, and it is none of it: they carry no
// script, submit only to themselves and cannot be framed — the same three rules
// the admin page's carries (adminCSP), for the same reason.
const signInCSP = "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// signInRedirectCSP is the authorize form's own policy: signInCSP with the one
// origin that form's submission ends at allowed for `form-action`.
//
// It is the authorize page's alone because it is the only page here whose
// submission a redirect answers (§11.3 step 4). A browser checks `form-action`
// against a redirect a form submission leads to as well as against the form's
// own action, so `'self'` alone would have the browser drop the 302 to the CLI's
// listener: the person would read a page that looks finished and K3.3's listener
// would wait for a code nothing sent, with every handler test still green.
//
// What is added is the validated target's own origin and not a wildcard over
// loopback ports: the value comes from `loopbackRedirect` in this same request,
// which is also the address `redirectWithCode` sends the code to, so the policy
// allows exactly the one place this page's sign-in can end up.
func signInRedirectCSP(target string) string {
	address, err := url.Parse(target)
	if err != nil {
		return signInCSP
	}
	host, port, err := splitAuthority(address.Host)
	if err != nil || address.Scheme != "http" || port == "" {
		return signInCSP
	}
	return "default-src 'none'; form-action 'self' " + address.Scheme + "://" + host + ":" + port +
		"; base-uri 'none'; frame-ancestors 'none'"
}

// signInView is what the authorize form is rendered from: the three parameters
// the person's sign-in carries back, all of them validated.
type signInView struct {
	Target    string
	State     string
	Challenge string
	Method    string
}

// confirmView is what the approve route's two pages are rendered from: the name
// the requesting installation gave, as untrusted text, and the licence that is
// about to be handed to it.
type confirmView struct {
	Installation string
	Organisation string
	UserCode     string
	IDToken      string
}

// renderSignIn writes one of these pages, with the three headers the admin page
// writes for the same reasons (adminPage), under the policy its caller chose:
// every page here takes signInCSP whole except the authorize form, whose
// submission a redirect answers (signInRedirectCSP).
func (s *Service) renderSignIn(w http.ResponseWriter, page *template.Template, view any, policy string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", policy)
	// The sign-in page carries the person's own ID token back and the
	// confirmation page carries it too, and the page that mints is not a page a
	// shared cache holds either (adminPage).
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := page.Execute(w, view); err != nil {
		// The header is out by now, so the page is what it is; what a reader
		// must not be left with is a half-rendered form that looks complete.
		log.Printf("keyservice: rendering the site's sign-in page: %v", err)
	}
}

// The three pages. They are the deployment's fallback and not the site: K5.1
// renders the real ones, with the sign-in the licence project provides, and
// these exist so that a deployment which has not built them yet still serves a
// flow a person and a test can complete — which is also why they carry no
// script at all (signInCSP).
//
// The installation's name is a text node of the confirmation page and
// html/template escapes it, exactly as the admin page's applicant text is: the
// page names a stranger's string and never renders it.
var (
	signInTemplate = template.Must(template.New("authorize").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Sign in to Fugaro</title></head>
<body>
<h1>Sign in to Fugaro</h1>
<p>Signing in here hands a licence key to the terminal that opened this page.</p>
<form method="post" action="/cli/authorize">
<input type="hidden" name="redirect_uri" value="{{.Target}}">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="code_challenge" value="{{.Challenge}}">
<input type="hidden" name="code_challenge_method" value="{{.Method}}">
<p><label>Sign-in token <textarea name="id_token" rows="6" cols="60"></textarea></label></p>
<p><button type="submit">Sign in</button></p>
</form>
</body>
</html>
`))

	deviceFormTemplate = template.Must(template.New("device").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Approve a Fugaro sign-in</title></head>
<body>
<h1>Approve a sign-in</h1>
<p>Type the code the terminal is showing.</p>
<form method="post" action="/cli/device">
<p><label>Code <input type="text" name="user_code" size="16" autocomplete="off"></label></p>
<p><label>Sign-in token <textarea name="id_token" rows="6" cols="60"></textarea></label></p>
<p><button type="submit">Continue</button></p>
</form>
</body>
</html>
`))

	deviceConfirmTemplate = template.Must(template.New("confirm").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Confirm a Fugaro sign-in</title></head>
<body>
<h1>Confirm</h1>
<p>{{if .Installation}}The installation {{.Installation}} is asking for a licence{{else}}An installation that gave no name is asking for a licence{{end}} for {{.Organisation}}.</p>
<p>Handing it a key lets that terminal run on this account's licence. If you did not start this, close this page.</p>
<form method="post" action="/cli/device">
<input type="hidden" name="user_code" value="{{.UserCode}}">
<input type="hidden" name="id_token" value="{{.IDToken}}">
<input type="hidden" name="confirm" value="1">
<p><button type="submit">Hand over the key</button></p>
</form>
</body>
</html>
`))

	deviceDoneTemplate = template.Must(template.New("done").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Fugaro</title></head>
<body>
<h1>Done</h1>
<p>{{if .Installation}}{{.Installation}} has been handed a licence{{else}}The installation has been handed a licence{{end}} for {{.Organisation}}. You can close this page and return to the terminal.</p>
</body>
</html>
`))
)
