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

	// The admin page's own fixtures (S3.4): the account the project's sign-in
	// answers as the owner's, the plan an Order Form mints under, and the page's
	// own secret, which is obviously not a real one — the deploy holds the
	// secret and the account, and a test names its own of both.
	ownerAccount       = "owner@fugaro.example"
	enterprisePlanName = "enterprise"
	adminSecret        = "fake-admin-secret-0123456789"
	// otherAdminSecret is what a forgery is signed with: another secret of the
	// same shape, so a token carrying it is refused by the signature alone.
	otherAdminSecret = "not-this-deploys-secret-0123456789"
	stepUpCode       = "step-up-code"
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
	r.recorded(minted.Key)
	return minted, nil
}

func (r *recordingMints) MintV1(ctx context.Context, req RequestV1) (MintedV1, error) {
	minted, err := r.innerV1.MintV1(ctx, req)
	if err != nil {
		return MintedV1{}, err
	}
	r.recorded(minted.Key)
	return minted, nil
}

func (r *recordingMints) recorded(key string) {
	if r.log != nil {
		r.log.add("mint")
	}
	r.keys = append(r.keys, key)
}

// ownerSpy is the project's identity in a test: the account a request arrives
// signed in as. An empty one is nobody signed in, which is the state §6's first
// row is about.
type ownerSpy struct{ account string }

func (o *ownerSpy) SignedIn(context.Context) (string, bool) {
	return o.account, o.account != ""
}

// stepUpSpy is the second factor in a test: the code it accepts, and what it
// was asked. **D455 is open** — a hardware key or TOTP — so the factor is a seam
// and this is its only implementation in the tree.
type stepUpSpy struct {
	accepts string
	accept  bool
	err     error

	// mu is the spy's own, and not the fixture's: the factor is asked before
	// the page takes its turn, so a burst of the page's actions reaches this at
	// once. A deployment's factor is called that way and has to answer it.
	mu    sync.Mutex
	asked []string
}

func (p *stepUpSpy) Verified(_ context.Context, account, code string) (bool, error) {
	p.mu.Lock()
	p.asked = append(p.asked, account+" "+code)
	p.mu.Unlock()
	if p.err != nil {
		return false, p.err
	}
	return p.accept && code == p.accepts, nil
}

// askedCodes is what the factor was asked, in order.
func (p *stepUpSpy) askedCodes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
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

// auditorSpy records the page's audit lines (§6, D456). log, when a test sets
// it, is the order hook beside recordingMints'.
type auditorSpy struct {
	lines []MintLine
	err   error
	log   *steps
}

func (a *auditorSpy) Mint(_ context.Context, line MintLine) error {
	if a.err != nil {
		return a.err
	}
	a.lines = append(a.lines, line)
	if a.log != nil {
		a.log.add("audit")
	}
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
	owner     *ownerSpy
	stepUp    *stepUpSpy
	notifier  *notifierSpy
	auditor   *auditorSpy
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
	f.owner = &ownerSpy{account: ownerAccount}
	f.stepUp = &stepUpSpy{accept: true, accepts: stepUpCode}
	f.notifier = &notifierSpy{}
	f.auditor = &auditorSpy{}
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
		// §6's own deployment: the one account the page answers to, the secret
		// its tokens are signed with (obviously not a real one), the period a
		// rendered form stays submittable, D454's caps and the plan the page
		// reaches that the site's forms do not (§5.5). The caps are loose enough
		// that a test which is not about the limit never meets it.
		AdminAccount:  ownerAccount,
		AdminSecret:   []byte(adminSecret),
		AdminTokenTTL: 15 * time.Minute,
		MintCaps: map[string]Limits{
			licence.TierCommunity:  {Requests: 20, Window: time.Hour},
			licence.TierTeam:       {Requests: 20, Window: time.Hour},
			licence.TierEnterprise: {Requests: 20, Window: time.Hour},
		},
		PagePlans: []Plan{{
			Name: enterprisePlanName, Tier: licence.TierEnterprise,
			Features: []string{"seats"}, Seats: 25, TermDays: 365,
		}},
		// The deploy's own code host, which §4.4's check reads and no other: the
		// fixture's repositories are all on it, so a test that names a repository
		// elsewhere reads that the line was refused before any call.
		CodeHosts:       []string{"code.example"},
		MaxRepositories: 8,
		Throttle:        Limits{Requests: 50, Window: time.Hour},
	}
	f.deps = &Deps{
		Store:    f.store,
		Mints:    f.recording,
		Mailer:   f.mail,
		CodeHost: f.host,
		Proofs:   f.proofs,
		Owner:    f.owner,
		StepUp:   f.stepUp,
		Notifier: f.notifier,
		Auditor:  f.auditor,
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
	tokens := f.mailedTokens()
	if len(tokens) == 0 {
		f.t.Fatal("the intake mailed nothing, so there is no link to follow")
	}
	return tokens[len(tokens)-1]
}

// mailedTokens is the token of every verification link the intake has sent, in
// the order it sent them. A test that is about a link nobody followed needs the
// link that is not the last one.
func (f *fixture) mailedTokens() []string {
	f.t.Helper()
	out := make([]string, 0, len(f.mail.sent))
	for _, msg := range f.mail.sent {
		at := strings.Index(msg.Body, "token=")
		if at < 0 {
			f.t.Fatalf("the mail carries no verification link: %q", msg.Body)
		}
		token := msg.Body[at+len("token="):]
		if end := strings.IndexAny(token, " \n\r\t<"); end >= 0 {
			token = token[:end]
		}
		out = append(out, token)
	}
	return out
}

// verifications is every pending link the store holds.
func (f *fixture) verifications() []Verification {
	f.t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	return append([]Verification(nil), f.store.verifications...)
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
		WorkspaceID:  workspaceID,
		Email:        buyerAddress(domain),
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

// adminGet opens the page. It is a GET because the page is one: nothing it does
// changes anything, and the two actions are POSTs.
func (f *fixture) adminGet() *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, intakeOrigin+RouteAdmin, nil)
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// adminPost submits one of the page's forms.
func (f *fixture) adminPost(route string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+route, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// pageToken is the token the page renders for one action and one account, built
// the way the page builds it. A test that is about the token being refused
// builds its own instead.
func (f *fixture) pageToken(account, action string) string {
	f.t.Helper()
	token, err := f.svc.pageToken(account, action)
	if err != nil {
		f.t.Fatalf("minting a page token: %v", err)
	}
	return token
}

// mintForm is the page's mint form as the page renders it: the action's own
// token, the second factor, and whatever the test fills the rest with.
func (f *fixture) mintForm(values url.Values) url.Values {
	f.t.Helper()
	if values == nil {
		values = url.Values{}
	}
	if values.Get("token") == "" {
		values.Set("token", f.pageToken(ownerAccount, actionMint))
	}
	if values.Get("step_up") == "" {
		values.Set("step_up", stepUpCode)
	}
	return values
}

// decideForm is the page's decline form, built the same way.
func (f *fixture) decideForm(id string) url.Values {
	f.t.Helper()
	return url.Values{
		"application": {id},
		"token":       {f.pageToken(ownerAccount, actionDecide)},
	}
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
