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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	repositoryTwoURL = "https://code.example/acme/gadget"
	privateURL       = "https://code.example/acme/private"
	unlicensedURL    = "https://code.example/acme/proprietary"
	unapprovedURL    = "https://code.example/acme/unapproved"
	openSourceOrg    = "code.example/acme/widget"
	applicantAddress = "dev@acme.example"

	// The deployment's own fixtures: the account the project's sign-in answers as
	// the owner's, the plan an Order Form mints under, and the secret a forgery is
	// signed with — another deployment's, of the same shape, so an event carrying
	// it is refused by the signature alone.
	ownerAccount       = "owner@fugaro.example"
	enterprisePlanName = "enterprise"
	otherDeploySecret  = "not-this-deploys-secret-0123456789"

	// The portal's own deploy values (plan P1.0). None of them is a real one,
	// and the three that are secret-shaped are the fixture's own strings rather
	// than anything a deploy holds.
	paddleAPIKey      = "fake-paddle-api-key-0123456789"
	resendKey         = "fake-resend-key-0123456789"
	schedulerAudience = "https://api.fugaro.example"
	sealerKeyName     = "projects/fugaro-example/locations/europe-west1/keyRings/portal/cryptoKeys/handover"
	tenantsCollection = "orgs"
	// termsVersion is the version of the terms the fixture's deployment serves
	// and records at a sign-up (D2199). The owner has not published the wording
	// itself, so a test names a version of its own, in the shape the terms will
	// spell one.
	termsVersion = "2026-10-27"
)

// buyerAddress is the address a merchant's payload carries for the buyer of a
// subscription: the paid path mails the key to it and refuses an event without
// one, so an event the fixture sends carries one.
func buyerAddress(organisation string) string { return "buyer@" + organisation }

// evaluationDays is the term the fixture's free seat covers. D432/D439's
// three-month evaluation key is the ruled one; the fixture states it in days
// because the plan does.
const evaluationDays = 92

// signerSpy is the minter's Signer in a test: a throwaway key, and the count of
// what was asked of it, which is how a test says "nothing was minted" without
// reading the record. Its answer is a real Ed25519 signature, so a key a test
// reads back is one any released verifier accepts — and `size`, when a test sets
// it, is a key ring that answers that many bytes instead of a signature, the
// shape a managed key's API is in when it is misconfigured, which the minter has
// to refuse rather than put into a key text.
type signerSpy struct {
	priv  ed25519.PrivateKey
	calls int
	err   error
	size  int
}

func (s *signerSpy) Sign(_ context.Context, payload []byte) ([]byte, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	signature := ed25519.Sign(s.priv, payload)
	if s.size > 0 && s.size != len(signature) {
		answered := make([]byte, s.size)
		copy(answered, signature)
		return answered, nil
	}
	return signature, nil
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

// hostSpy is the code host's API in a test: what each repository answers, and
// what was asked for. It is keyed by the two parts — what the contract hands an
// implementation (§4.4) — and never by a URL, so a test that a hostile line was
// refused has the absence of a call to read.
type hostSpy struct {
	repos map[string]Repository
	asked []string
}

func (h *hostSpy) Repository(_ context.Context, owner, repo string) (Repository, error) {
	named := owner + "/" + repo
	h.asked = append(h.asked, named)
	answer, known := h.repos[named]
	if !known {
		return Repository{}, fmt.Errorf("%w: %s", ErrRepository, named)
	}
	return answer, nil
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
//
// It answers both formats, because the intake reaches one minter: the paid path
// mints v1 keys and the free routes still mint the older format's, which is the
// pair K7.3 collapses.
type recordingMints struct {
	inner   Mints
	innerV1 MintsV1
	keys    []string
	// secrets is the renewal secret each v1 mint answered, in the same order as
	// keys: a key's secret is what its holder renews with, and F3 mails no key —
	// so the mint's own answer is the only record of it a test can reach.
	secrets []string
	// log, when a test sets it, is the order hook: a mint is written down where
	// the key comes out, which is what makes "the audit line is written before
	// the key is returned" a claim a test can read.
	log *steps
}

func (r *recordingMints) Mint(ctx context.Context, req Request) (Minted, error) {
	minted, err := r.inner.Mint(ctx, req)
	if err != nil {
		return Minted{}, err
	}
	r.recorded(minted.Key, "")
	return minted, nil
}

func (r *recordingMints) MintV1(ctx context.Context, req RequestV1) (MintedV1, error) {
	minted, err := r.innerV1.MintV1(ctx, req)
	if err != nil {
		return MintedV1{}, err
	}
	r.recorded(minted.Key, minted.RenewalSecret)
	return minted, nil
}

func (r *recordingMints) recorded(key, secret string) {
	if r.log != nil {
		r.log.add("mint")
	}
	r.keys = append(r.keys, key)
	r.secrets = append(r.secrets, secret)
}

// notifierSpy records what the owner was told a request is waiting (§4.5). It
// is a seam of its own and not the applicant's mail path, which is what lets
// §7.2's "the manual form mails nothing" hold while the owner is notified.
type notifierSpy struct {
	alerts []Alert
	err    error
}

func (n *notifierSpy) Notify(_ context.Context, alert Alert) error {
	if n.err != nil {
		return n.err
	}
	n.alerts = append(n.alerts, alert)
	return nil
}

// steps is one ordered log of what the intake reached, in the order it reached
// it: the only way a test can say "before" about two seams that are handed and
// answer in one call each.
type steps struct {
	mu sync.Mutex
	at []string
}

func (s *steps) add(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at = append(s.at, step)
}

func (s *steps) index(step string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, at := range s.at {
		if at == step {
			return i
		}
	}
	return -1
}

// docsSpy is the portal's document store in a test: the documents in memory,
// keyed by collection and id. The store the deploy runs is P1.2's, and an area's
// test drives its own records through this one.
//
// It answers a query's documents in no particular order, which is what a store
// with no order does: a test that is about an order asks for one.
//
// A withdrawal a rebind makes through a transaction (Tx.Revoke) goes to the key
// store the case reads, because a deployment's two seams are one document store:
// firestore_store.go's NewDocStore is handed the database the portal's seam is
// reached over, and the key store's records live in it beside the portal's
// documents. A case that reads a revocation back from f.store is therefore
// reading the same place the service wrote it, and a case that needs the store's
// own rules — a transaction that cannot commit, a write that lands beside this
// seam's — wires one document store behind both seams instead
// (workspaces_test.go's docsOnDocstore).
type docsSpy struct {
	mu    sync.Mutex
	held  map[string]Doc
	store Store
}

func newDocsSpy(store Store) *docsSpy {
	return &docsSpy{held: map[string]Doc{}, store: store}
}

func (d *docsSpy) Get(_ context.Context, collection, id string) (Doc, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	held, ok := d.held[collection+"/"+id]
	if !ok {
		return nil, ErrNoDocument
	}
	return held, nil
}

func (d *docsSpy) Put(_ context.Context, collection, id string, doc Doc) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held[collection+"/"+id] = doc
	return nil
}

func (d *docsSpy) Delete(_ context.Context, collection, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.held, collection+"/"+id)
	return nil
}

// Revoke withdraws keys into the store this seam stands on, one store call each:
// in a deploy the two seams are one document store, and here they are one test
// store. This spy has no transactions of its own to nest, which is exactly what
// makes it unable to tell a withdrawal committed with the caller's transaction
// from one committed beside it — a case that has to tell them apart wires a real
// document store behind both seams (workspaces_test.go's docsOnDocstore).
func (d *docsSpy) Revoke(ctx context.Context, revocations []Revocation) error {
	for _, r := range revocations {
		if err := d.store.Revoke(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (d *docsSpy) Query(_ context.Context, collection string, where Where) ([]Doc, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	found := []Doc{}
	for key, doc := range d.held {
		if !strings.HasPrefix(key, collection+"/") {
			continue
		}
		if where.Field != "" && doc[where.Field] != where.Value {
			continue
		}
		found = append(found, doc)
	}
	return found, nil
}

// Update runs the change over this store as its own transaction: a conflict is
// what a durable store's own concurrency is about (P1.2), and a test that is
// about one drives that store.
func (d *docsSpy) Update(ctx context.Context, fn func(context.Context, Tx) error) error {
	return fn(ctx, d)
}

// signInSpy is the portal's sign-in verifier in a test: the caller it answers
// and every token it was handed, so a test reads what an area did with one. The
// tokens are taken under a lock because a case may put a burst of requests
// through the handler at once (orgs_test.go's concurrent sign-ups).
type signInSpy struct {
	mu     sync.Mutex
	caller Caller
	err    error
	tokens []string
	// now is the clock the freshness rule is measured against, which is the
	// fixture's (f.clock): a case that moves the clock moves a sign-in out of
	// its window with it.
	now func() time.Time
}

func (s *signInSpy) Verify(_ context.Context, idToken string) (Caller, error) {
	s.mu.Lock()
	s.tokens = append(s.tokens, idToken)
	s.mu.Unlock()
	if s.err != nil {
		return Caller{}, s.err
	}
	return s.caller, nil
}

// RequireSSO is the rule and not the spy: the fixture's seam applies P1.3's own
// implementation (ssoRefuses), so a test that reads a refusal reads the rule a
// deployment would apply. What the spy answers is the caller, and that is what a
// test sets to be somebody else.
func (s *signInSpy) RequireSSO(caller Caller, rule SSORule) error {
	return ssoRefuses(signInOf(caller), rule)
}

// FreshWithin is the rule for the same reason RequireSSO is: the fixture's seam
// applies P1.3's own implementation (freshSignIn), so a case that reads a
// refusal reads the rule a deployment would apply rather than a stub that agrees
// with the route.
func (s *signInSpy) FreshWithin(caller Caller, within time.Duration) error {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	return freshSignIn(signInOf(caller), within, now())
}

// paddleSpy is the merchant of record in a test: what it answers and what it was
// asked, so an area's test reads that a transaction was created server-side and
// that a seat change and a cancellation went the same way (F15).
type paddleSpy struct {
	transaction Transaction
	portalURL   string
	customerID  string
	err         error
	checkouts   []Checkout
	seats       []int
	cancelled   []string
	// cancelErrors is what the merchant answers each cancellation with, in
	// order, and `err` stands for every attempt after the list is spent. A case
	// uses it for an answer that only a later attempt meets: a merchant refuses
	// to cancel a subscription it has ended already, so a case whose retry asked
	// it again would be reading that refusal.
	cancelErrors []error
}

func (p *paddleSpy) CreateTransaction(_ context.Context, checkout Checkout) (Transaction, error) {
	p.checkouts = append(p.checkouts, checkout)
	if p.err != nil {
		return Transaction{}, p.err
	}
	return p.transaction, nil
}

func (p *paddleSpy) PortalSession(_ context.Context, customerID string) (string, error) {
	p.customerID = customerID
	if p.err != nil {
		return "", p.err
	}
	return p.portalURL, nil
}

func (p *paddleSpy) SetSeats(_ context.Context, _ string, seats int) error {
	p.seats = append(p.seats, seats)
	return p.err
}

func (p *paddleSpy) Cancel(_ context.Context, subscriptionID string) error {
	p.cancelled = append(p.cancelled, subscriptionID)
	if len(p.cancelErrors) > 0 {
		err := p.cancelErrors[0]
		p.cancelErrors = p.cancelErrors[1:]
		return err
	}
	return p.err
}

// schedulerSpy is Cloud Scheduler's verifier in a test: the account it answers
// and every token it was handed.
type schedulerSpy struct {
	account string
	err     error
	tokens  []string
}

func (s *schedulerSpy) Verify(_ context.Context, bearer string) (string, error) {
	s.tokens = append(s.tokens, bearer)
	if s.err != nil {
		return "", s.err
	}
	return s.account, nil
}

// sealerSpy is the handover's sealer in a test: it wraps what it is given, so a
// test reads that a record holds the sealed bytes and never the plaintext (F3),
// and it writes down what it was asked to open. What it wraps with is obviously
// not a seal: the deploy's key is KMS's (P1.7).
type sealerSpy struct {
	err    error
	sealed [][]byte
	opened [][]byte
}

const sealerMarker = "sealed:"

func (s *sealerSpy) Seal(_ context.Context, plaintext []byte) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	sealed := append([]byte(sealerMarker), plaintext...)
	s.sealed = append(s.sealed, sealed)
	return sealed, nil
}

func (s *sealerSpy) Open(_ context.Context, sealed []byte) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.opened = append(s.opened, sealed)
	return bytes.TrimPrefix(sealed, []byte(sealerMarker)), nil
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
	notifier  *notifierSpy
	docs      *docsSpy
	signIn    *signInSpy
	paddle    *paddleSpy
	scheduler *schedulerSpy
	sealer    *sealerSpy
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
		"acme/widget":      {Public: true, Licence: "MIT"},
		"acme/gadget":      {Public: true, Licence: "Apache-2.0"},
		"acme/private":     {Public: false, Licence: "MIT"},
		"acme/proprietary": {Public: true, Licence: "LicenseRef-Proprietary"},
		"acme/unapproved":  {Public: true, Licence: "OpenSSL"},
	}}
	f.proofs = &proofSpy{proved: true, organisation: openSourceOrg}
	f.notifier = &notifierSpy{}
	f.docs = newDocsSpy(f.store)
	f.signIn = &signInSpy{caller: Caller{
		UID: "uid-1", Email: ownerAccount, Provider: "google.com", AuthTime: f.now,
	}, now: f.clock}
	f.paddle = &paddleSpy{
		transaction: Transaction{ID: "txn-1", CustomerID: "ctm-1"},
		portalURL:   "https://paddle.example/portal",
	}
	f.scheduler = &schedulerSpy{account: "scheduler@fugaro.example"}
	f.sealer = &sealerSpy{}
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: env, Now: f.clock}
	f.recording = &recordingMints{inner: minter, innerV1: minter}
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
		// D454's caps: loose enough that a test which is not about the limit
		// never meets it.
		MintCaps: map[string]Limits{
			licence.TierCommunity:  {Requests: 20, Window: time.Hour},
			licence.TierTeam:       {Requests: 20, Window: time.Hour},
			licence.TierEnterprise: {Requests: 20, Window: time.Hour},
		},
		// The deploy's own code host, which §4.4's check reads and no other: the
		// fixture's repositories are all on it, so a test that names a repository
		// elsewhere reads that the line was refused before any call.
		CodeHosts:       []string{"code.example"},
		MaxRepositories: 8,
		Throttle:        Limits{Requests: 50, Window: time.Hour},
		// F35's invitation rate: loose enough that a case which is not about it
		// never meets it, and a rate the deploy names all the same.
		InviteRate: Limits{Requests: 50, Window: time.Hour},
		// The portal's own deploy values (plan P1.0). Sentry is left without a
		// DSN, which is Sentry off (P1.6) and what a test that is not about
		// reporting wants.
		PaddleAPIKey:      []byte(paddleAPIKey),
		ResendKey:         []byte(resendKey),
		SchedulerAudience: schedulerAudience,
		SealerKeyName:     sealerKeyName,
		TenantsCollection: tenantsCollection,
		TermsVersion:      termsVersion,
	}
	f.deps = &Deps{
		Store:     f.store,
		Mints:     f.recording,
		Mailer:    f.mail,
		CodeHost:  f.host,
		Proofs:    f.proofs,
		Notifier:  f.notifier,
		Docs:      f.docs,
		SignIn:    f.signIn,
		Paddle:    f.paddle,
		Scheduler: f.scheduler,
		Sealer:    f.sealer,
		Now:       f.clock,
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

// secretOf is the renewal secret the mint that produced this key answered, which
// is what a test that walks a key's chain needs: the store keeps the secret's
// digest and never the secret (§7.3), and since D2182 no notice carries a key —
// the portal is where one is issued (F3), so the mint's own answer is the only
// record of the secret its holder renews with.
func (f *fixture) secretOf(t *testing.T, key string) string {
	t.Helper()
	for i, minted := range f.recording.keys {
		if minted == key {
			return f.recording.secrets[i]
		}
	}
	t.Fatalf("no mint of this intake produced the key %q", key)
	return ""
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

// api sends one request to the portal's API, from the applicant's own address.
// The body is what the caller wrote: the API's own routes read JSON.
func (f *fixture) api(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, intakeOrigin+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = applicantIP
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

// revocations is every entry of §8's revocation list the store holds.
func (f *fixture) revocations() []Revocation {
	f.t.Helper()
	entries, err := f.store.Revocations(context.Background())
	if err != nil {
		f.t.Fatalf("reading the revocation list: %v", err)
	}
	return entries
}

// queued is every application a person has to decide.
func (f *fixture) queued() []Application {
	f.t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	return append([]Application(nil), f.store.applications...)
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
// The body is Paddle's own notification and not this service's canonical event:
// a case writes the event it means and `paddleBody` spells it the way the
// merchant does, so every case here drives the adapter (paddleevents.go) beside
// the paid path it is about (P4.2, F16).
//
// The signature's timestamp is the instant the delivery is made and not the
// instant the event says it occurred: those are two different things, and an
// event that arrives late is exactly what §5.2's ordering rule is about.
func (f *fixture) deliver(e Event) *httptest.ResponseRecorder {
	f.t.Helper()
	e = f.claimsOf(e)
	body := paddleBody(f.t, e)
	return f.deliverRaw(body, signEvent(f.secret, f.now, body))
}

// claimsOf fills in what one case's event does not state and the deployment
// would: the transaction the API created at the checkout, and the records the
// event is admitted by and granted against (F15, P4.2). What it writes is what
// P4.1's checkout and P3.1's workspace list write — the two records an event
// read outside a case has behind it — and it writes nothing for an event that
// names neither.
func (f *fixture) claimsOf(e Event) Event {
	f.t.Helper()
	if e.Type != EventPurchase {
		return e
	}
	if e.Transaction == "" {
		e.Transaction = "txn-" + e.Subscription
	}
	f.seed(checkoutTransactionsCollection, e.Transaction, checkoutTransaction{
		ID:             e.Transaction,
		OrganisationID: e.Organisation,
		WorkspaceID:    e.WorkspaceID,
		Seats:          e.Seats,
		UID:            f.signIn.caller.UID,
		Email:          e.Email,
		CreatedAt:      f.now,
	})
	if e.Organisation != "" && e.WorkspaceID != "" {
		f.seed(workspacesCollection, e.WorkspaceID, WireWorkspace{
			ID: e.WorkspaceID, OrganisationID: e.Organisation,
			Name: e.WorkspaceID, CreatedAt: f.now,
		})
	}
	return e
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

// purchase delivers one live purchase event for a domain, binds the licence it
// bought to a project, and answers the record of the key that binding minted.
//
// Two acts and not one, because that is the shape D2196 gives a licence: the
// purchase grants the **entitlement** and mints nothing, and the customer's own
// binding is what issues the key for their project (P3.1). A case that wants a
// key in hand drives both, and a case about the purchase alone drives `deliver`
// — which is what keeps "the purchase minted a key" from being a claim any test
// here can still make.
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
		WorkspaceID:  workspaceID,
		Email:        buyerAddress(domain),
		Seats:        seats,
		TermDays:     termDays,
	})
	f.bind(domain, "sub-"+domain)
	after := f.records()
	if len(after) != before+1 {
		f.t.Fatalf("the purchase of %d seat(s) for %s minted %d key(s), want one", seats, domain, len(after)-before)
	}
	return after[len(after)-1]
}

// bind binds the licence one subscription bought to the fixture's own project,
// the way the customer does from the portal: the organisation the money was for,
// the workspace it bought, and the key that binding mints.
//
// The project is read as present, because a case here is about the paid path and
// not about D2205's check: a probe that reached Firebase would be a test that
// needs a network, and the answers a real database gives are the one half of
// that rule this repository cannot check (workspaces_test.go holds the cases
// that are about it).
func (f *fixture) bind(organisation, subscription string) Record {
	f.t.Helper()
	if err := f.bindingAttempt(organisation, subscription); err != nil {
		f.t.Fatalf("binding the licence of %s: %v", organisation, err)
	}
	records := f.records()
	return records[len(records)-1]
}

// bindingAttempt binds a licence the way `bind` does and answers the binding's
// own failure rather than failing the case with it: a case whose subject is a
// binding that fails — a store that would not record the key it issued — has to
// read that failure.
func (f *fixture) bindingAttempt(organisation, subscription string) error {
	f.t.Helper()
	f.actsAsOwnerOf(organisation)
	previous := probeInstallation
	probeInstallation = projectInstallation{
		databases: []databaseHost{{location: usCentralDatabase, address: "https://%s.example"}},
		client:    &http.Client{Transport: presentProject{}},
	}
	f.t.Cleanup(func() { probeInstallation = previous })
	origin, held, err := f.svc.origin(context.Background(), subscription)
	if err != nil || !held {
		f.t.Fatalf("the purchase of %s left no record of the subscription %s (held=%v, err=%v)", organisation, subscription, held, err)
	}
	_, err = f.svc.bindEntitlement(context.Background(), f.signIn.caller, origin.EntitlementID, workspaceID)
	return err
}

// actsAsOwnerOf makes the fixture's caller the owner of the organisation named,
// which is the membership a binding is made by: the money's organisation is the
// checkout's own record and the portal's, and a caller who is not in it is told
// the licence is not there.
func (f *fixture) actsAsOwnerOf(organisation string) {
	f.t.Helper()
	caller := f.signIn.caller
	if _, held := f.organisation(organisation); !held {
		f.seed(organisationsCollection, organisation, WireOrganisation{
			ID: organisation, Name: organisation, Type: orgTypeCustomer, CreatedAt: f.now,
		})
	}
	f.seed(membershipsCollection, membershipID(organisation, caller.UID), WireMembership{
		OrganisationID: organisation, UID: caller.UID, Email: caller.Email,
		Role: roleOwner, CreatedAt: f.now,
	})
}

// presentProject is D2205's read answered without a database: a refusal of
// permission is a project whose rules are locked, which is an installation that
// is there, and it is what a probe seam has to answer for a binding to proceed.
type presentProject struct{}

func (presentProject) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"Permission denied"}`)),
	}, nil
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
	if e.Email == "" && e.Organisation != "" {
		e.Email = buyerAddress(e.Organisation)
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = f.now
	}
	// A purchase's and a seat change's key binds to a workspace, so an event
	// that names none is filled with the fixture's own: a test about the binding
	// sets one of its own (§5.1, TN2).
	if e.WorkspaceID == "" {
		e.WorkspaceID = workspaceID
	}
	return f.deliver(e)
}

// open is the queue as the page reads it: the requests nobody has answered.
func (f *fixture) open() []Application {
	f.t.Helper()
	out := []Application{}
	for _, a := range f.queued() {
		if a.Decision == Undecided {
			out = append(out, a)
		}
	}
	return out
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

// burstGate is how long one request waits for the burst it is part of. It is the
// window a burst of one account's requests arrives inside, and a request that
// arrives alone waits it out rather than waiting for requests that are not
// coming.
const burstGate = 200 * time.Millisecond

// together submits one request n times at once and answers the status code each
// submission got, in submission order. They all wait on one channel, so the n
// are in flight together rather than queued behind a loop.
func together(n int, submit func(i int) *httptest.ResponseRecorder) []int {
	start := make(chan struct{})
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i] = submit(i).Code
		}()
	}
	close(start)
	wg.Wait()
	return codes
}

// answered is how many of the codes are one status.
func answered(codes []int, want int) int {
	n := 0
	for _, code := range codes {
		if code == want {
			n++
		}
	}
	return n
}
