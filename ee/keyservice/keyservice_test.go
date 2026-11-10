// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The fixture every test in this package drives: one intake wired to fakes, a
// clock the test moves, and a throwaway Ed25519 keypair behind the minter's
// Signer. The key is generated in the process that reads it and never a fixture
// file (.claude/rules/ee.md, R5), and it is what lets a test read a minted key
// back through ee/licence's verifier rather than through this package's own
// ideas about one.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

const (
	// The two key ids the owner creates (S3.8). Neither is a fixture of a real
	// keyring: the ids are the Terraform roots' defaults and the halves behind
	// them are the test's own.
	productionKeyID = "fugaro-licence-production"
	stagingKeyID    = "fugaro-licence-staging"

	// The fixture's plan names, which are the three routes' own names: what a key
	// of one plan carries is D449's and the fixture's plans are the test's.
	freePlanName = "free-seat"
	openPlanName = "open-source"
	paidPlanName = "team"

	intakeOrigin = "https://keys.fugaro.example"
	// webhookSecret and the applicant's IP are obviously not real ones.
	webhookSecret = "fake-secret-0123456789"
	applicantIP   = "203.0.113.7:4040"

	repositoryURL    = "https://code.example/acme/widget"
	privateURL       = "https://code.example/acme/private"
	unlicensedURL    = "https://code.example/acme/proprietary"
	openSourceOrg    = "code.example/acme/widget"
	applicantAddress = "dev@acme.example"
)

// evaluationDays is the term the fixture's free seat covers. D432/D439's
// three-month evaluation key is the ruled one; the fixture states it in days
// because the plan does.
const evaluationDays = 92

// signerSpy is the minter's Signer in a test: a throwaway key, and the count of
// what was asked of it, which is how a test says "nothing was minted" without
// reading the record. Its answer is a real Ed25519 signature, so a key a test
// reads back is one any released verifier accepts.
type signerSpy struct {
	priv  ed25519.PrivateKey
	calls int
	err   error
}

func (s *signerSpy) Sign(_ context.Context, payload []byte) ([]byte, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return ed25519.Sign(s.priv, payload), nil
}

// mailSpy records what the intake mailed: the messages are read as the headers
// and the body the intake built, never as a rendered wire message, which is the
// seam §7.2's rule is about.
type mailSpy struct {
	sent []Message
	err  error
}

func (m *mailSpy) Send(_ context.Context, msg Message) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

// hostSpy is the code host's API in a test: what each URL answers, and what was
// asked for.
type hostSpy struct {
	repos map[string]Repository
	asked []string
}

func (h *hostSpy) Repository(_ context.Context, url string) (Repository, error) {
	h.asked = append(h.asked, url)
	repo, known := h.repos[url]
	if !known {
		return Repository{}, fmt.Errorf("%w: %s", ErrRepository, url)
	}
	return repo, nil
}

// proofSpy answers the proof of control a test sets. What counts as proof is
// D452 and no production implementation exists yet, so the check is an
// interface and this is its only implementation in the tree.
type proofSpy struct {
	proved       bool
	organisation string
	asked        int
}

func (p *proofSpy) Proved(_ context.Context, _ OpenSource) (Proof, error) {
	p.asked++
	if !p.proved {
		return Proof{}, nil
	}
	return Proof{Organisation: p.organisation}, nil
}

// recordingMints is the minter as a test sees it: the fixture's own Minter,
// with what it minted kept aside. The record holds no key material (§7.3), so
// the key texts a test reads back through the verifier are the ones kept here.
type recordingMints struct {
	inner Mints
	keys  []string
}

func (r *recordingMints) Mint(ctx context.Context, req Request) (Minted, error) {
	minted, err := r.inner.Mint(ctx, req)
	if err != nil {
		return Minted{}, err
	}
	r.keys = append(r.keys, minted.Key)
	return minted, nil
}

// staticTokens is a Tokens that hands out one token: the platform's metadata
// server is the deploy's, and a test's token stands in for it.
func staticTokens(token string) Tokens {
	return tokenFunc(func(context.Context, string) (string, error) { return token, nil })
}

type tokenFunc func(ctx context.Context, audience string) (string, error)

func (f tokenFunc) IDToken(ctx context.Context, audience string) (string, error) {
	return f(ctx, audience)
}

// fixture is one intake and everything it reaches.
type fixture struct {
	t         *testing.T
	svc       *Service
	handler   http.Handler
	cfg       *Config
	deps      *Deps
	store     *Memory
	mail      *mailSpy
	host      *hostSpy
	proofs    *proofSpy
	signer    *signerSpy
	recording *recordingMints
	keys      Keys
	ring      licence.Keyring
	secret    []byte
	env       Environment
	now       time.Time
}

func newFixture(t *testing.T) *fixture { return newFixtureIn(t, Production) }

// newFixtureIn is the same intake in the environment named: the merchant's
// test-mode events are the staging intake's and nothing else's (§5.4).
func newFixtureIn(t *testing.T, env Environment) *fixture {
	t.Helper()
	return newFixtureWith(t, env, nil)
}

// newFixtureWith builds the fixture and hands it to the test before the intake
// is built from it: what a test changes there is the deployment's own
// configuration — the throttle's rate, what the intake mints through — and
// start() is what makes the change take.
func newFixtureWith(t *testing.T, env Environment, edit func(*fixture)) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the fixture's signing key: %v", err)
	}
	f := &fixture{
		t:      t,
		keys:   Keys{Production: productionKeyID, Staging: stagingKeyID},
		ring:   licence.Keyring{productionKeyID: pub, stagingKeyID: pub},
		secret: []byte(webhookSecret),
		env:    env,
		now:    time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
	}
	f.signer = &signerSpy{priv: priv}
	f.store = &Memory{}
	f.mail = &mailSpy{}
	f.host = &hostSpy{repos: map[string]Repository{
		repositoryURL: {Public: true, Licence: "MIT"},
		privateURL:    {Public: false, Licence: "MIT"},
		unlicensedURL: {Public: true, Licence: "LicenseRef-Proprietary"},
	}}
	f.proofs = &proofSpy{proved: true, organisation: openSourceOrg}
	f.recording = &recordingMints{inner: Minter{Signer: f.signer, Keys: f.keys, Now: f.clock}}
	f.cfg = &Config{
		Environment: env,
		Keys:        f.keys,
		URL:         intakeOrigin,
		// The fixture's plans are the test's own: which tier, features and seats
		// a free key carries is D449 and the owner has not ruled, so the service
		// mints from the plan it is handed and a test hands it one.
		FreeSeat:   &Plan{Name: freePlanName, Tier: licence.TierTeam, Features: []string{"seats"}, Seats: 1, TermDays: evaluationDays},
		OpenSource: &Plan{Name: openPlanName, Tier: licence.TierTeam, Features: []string{"seats"}, Seats: 1, TermDays: 365},
		Paid:       Plan{Name: paidPlanName, Tier: licence.TierTeam, Features: []string{"seats"}},
		// D453's free-mail list and D457's period are the owner's; a test names
		// its own of each. The rate is the fixture's own too, and it is loose
		// enough that a test which is not about the throttle never meets it.
		FreeMailDomains: []string{"gmail.example", "outlook.example"},
		VerificationTTL: 24 * time.Hour,
		MerchantSecret:  f.secret,
		Throttle:        Limits{Requests: 50, Window: time.Hour},
	}
	f.deps = &Deps{
		Store:    f.store,
		Mints:    f.recording,
		Mailer:   f.mail,
		CodeHost: f.host,
		Proofs:   f.proofs,
		Now:      f.clock,
	}
	if edit != nil {
		edit(f)
	}
	f.start()
	return f
}

// start builds the intake from the fixture's configuration and dependencies as
// they stand: a test that swaps what the intake reaches calls it again, and the
// swap is in Deps.
func (f *fixture) start() {
	f.t.Helper()
	svc, err := New(*f.cfg, *f.deps)
	if err != nil {
		f.t.Fatalf("building the intake: %v", err)
	}
	f.svc = svc
	f.handler = svc.Handler()
}

// mintedKeys is every key the intake asked the minter for, in order.
func (f *fixture) mintedKeys() []string {
	f.t.Helper()
	return append([]string(nil), f.recording.keys...)
}

func (f *fixture) clock() time.Time { return f.now }

// advance moves the clock both halves read.
func (f *fixture) advance(d time.Duration) { f.now = f.now.Add(d) }

// post submits one of the site's forms the way the page does: an urlencoded
// body, from the applicant's own address.
func (f *fixture) post(route string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.postFrom(applicantIP, route, form)
}

func (f *fixture) postFrom(ip, route string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+route, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// get follows a link the intake mailed.
func (f *fixture) get(route string, query url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, intakeOrigin+route+"?"+query.Encode(), nil)
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// apply is the free seat's form.
func (f *fixture) apply(email string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.post(RouteFreeSeat, url.Values{"email": {email}})
}

// applyOpenSource is the open-source programme's form: the repositories the
// applicant named, one per line, and its address.
func (f *fixture) applyOpenSource(email string, repositories ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.post(RouteOpenSource, url.Values{
		"email":        {email},
		"repositories": {strings.Join(repositories, "\n")},
	})
}

// verify follows a verification link.
func (f *fixture) verify(token string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.get(RouteVerify, url.Values{"token": {token}})
}

// mailedToken is the token of the link in the last mail the intake sent.
func (f *fixture) mailedToken() string {
	f.t.Helper()
	if len(f.mail.sent) == 0 {
		f.t.Fatal("the intake mailed nothing, so there is no link to follow")
	}
	body := f.mail.sent[len(f.mail.sent)-1].Body
	at := strings.Index(body, "token=")
	if at < 0 {
		f.t.Fatalf("the mail carries no verification link: %q", body)
	}
	token := body[at+len("token="):]
	if end := strings.IndexAny(token, " \n\r\t<"); end >= 0 {
		token = token[:end]
	}
	return token
}

// records is every issuance the store holds, oldest first.
func (f *fixture) records() []Record {
	f.t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	out := make([]Record, 0, len(f.store.issuances))
	for _, r := range f.store.issuances {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Issued.Before(out[j].Issued) })
	return out
}

// queued is every application a person has to decide.
func (f *fixture) queued() []Application {
	f.t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	return append([]Application(nil), f.store.applications...)
}

// claims verifies one minted key the way a released verifier would, and refuses
// a key that is not valid at the instant the fixture's clock reads.
func (f *fixture) claims(key string) licence.Claims {
	f.t.Helper()
	claims, state, err := licence.VerifyAgainst([]byte(key), f.now, f.ring)
	if err != nil {
		f.t.Fatalf("the minted key does not verify: %v", err)
	}
	if state != licence.Valid {
		f.t.Fatalf("the minted key reads %s at the instant it was minted", state)
	}
	return claims
}

// signEvent is the merchant's own signature over one body, in the scheme the
// intake checks: `Paddle-Signature: ts=<unix>;h1=<hmac-sha256 over "ts:body">`.
func signEvent(secret []byte, at time.Time, body []byte) string {
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(stamp + ":"))
	mac.Write(body)
	return "ts=" + stamp + ";h1=" + hex.EncodeToString(mac.Sum(nil))
}

// deliver posts one event the merchant signed, as the merchant's webhook does.
//
// The signature's timestamp is the instant the delivery is made and not the
// instant the event says it occurred: those are two different things, and an
// event that arrives late is exactly what §5.2's ordering rule is about.
func (f *fixture) deliver(e Event) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(e)
	if err != nil {
		f.t.Fatalf("encoding the event: %v", err)
	}
	return f.deliverRaw(body, signEvent(f.secret, f.now, body))
}

func (f *fixture) deliverRaw(body []byte, header string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteWebhook, bytes.NewReader(body))
	if header != "" {
		req.Header.Set(SigningHeader, header)
	}
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// purchase delivers one live purchase event for a domain and answers the record
// it minted; a purchase that minted nothing is a failure a test reads here
// rather than three lines further down.
func (f *fixture) purchase(id, domain string, seats, termDays int) Record {
	f.t.Helper()
	before := len(f.records())
	f.deliver(Event{
		ID:           id,
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-" + domain,
		Organisation: domain,
		Seats:        seats,
		TermDays:     termDays,
	})
	after := f.records()
	if len(after) != before+1 {
		f.t.Fatalf("the purchase of %d seat(s) for %s minted %d key(s), want one", seats, domain, len(after)-before)
	}
	return after[len(after)-1]
}

// event delivers one event whose fields the test sets, with the mode and the
// subscription filled in for the fixture's environment.
func (f *fixture) event(e Event) *httptest.ResponseRecorder {
	f.t.Helper()
	if e.Mode == "" {
		e.Mode = ModeFor(f.env)
	}
	if e.Subscription == "" {
		e.Subscription = "sub-" + e.Organisation
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = f.now
	}
	return f.deliver(e)
}

// answerBody is the one page every application gets (§7.1), which a test holds
// every outcome to.
func answerBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("the intake answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Fatal("the intake answered with nothing")
	}
	return rec.Body.String()
}
