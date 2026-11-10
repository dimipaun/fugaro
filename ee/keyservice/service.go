// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The intake: the internet-facing half of the key service (§3.1). It serves the
// site's three forms, the merchant's events and the verification links, it keeps
// the record, and it **cannot sign** — every key it hands out it asked the
// minter's own service for (Check 174).
//
// The whole package is one program's parts because the split T13 rules is an
// identity boundary and not a compilation one (§3.4): this file and minter.go
// are the two services' entry points, and what keeps them apart is which one
// holds a Signer.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The routes. The three form routes are the site's half of the intake's
// contract — its page builds its form actions from the same table
// (website/test/keys.mjs holds it, S3.3) — and they are the service's to answer
// with the field names the page posts.
const (
	RouteFreeSeat   = "/free-seat"
	RouteOpenSource = "/open-source"
	RouteRequest    = "/request"
	RouteVerify     = "/verify"
	RouteWebhook    = "/webhook"
)

// The bounds on what a stranger may hand the service. Each is a body a form or a
// merchant event is expected to fit in by orders of magnitude, and the point of
// a bound is that the service reads a body it can hold rather than one that
// arrives in pieces.
const (
	maxFormBytes    = 16 << 10
	maxEventBytes   = 256 << 10
	maxRequestBytes = 64 << 10
	maxDetailBytes  = 4 << 10
)

// Config is the deployment's own configuration: the key ids, the plans the
// routes mint, the free-mail list and the numbers a rate needs. It is built in
// Go by the service's entry point rather than decoded, because the deploy's
// shape — environment variables, Secret Manager, a config file — is S3.5's and
// S3.7's, and a decoder here would be a decision taken early.
type Config struct {
	// Environment is which of the two pairs this is, and it is what a
	// test-mode event is measured against (§5.4).
	Environment Environment
	// Keys are the two key ids the owner creates (S3.8).
	Keys Keys
	// URL is the intake's own origin — `https://keys.` plus the site's
	// canonical host (§3.1). It is what the verification link is built from,
	// and never the request's Host header (§7.2).
	URL string
	// FreeSeat and OpenSource are what those two routes mint. The numbers in
	// them are D449's, which is why they are configuration: the owner rules
	// them and a deploy states them.
	FreeSeat   *Plan
	OpenSource *Plan
	// Paid is the plan a merchant event mints under. Its seats are the event's
	// and its term is the event's (§5.1).
	Paid Plan
	// FreeMailDomains is D453's list, as domains, lowercased here.
	FreeMailDomains []string
	// VerificationTTL is how long a link stays usable. The period is D457's
	// and it has to be long enough that a person who gets the mail during a
	// short outage can still finish (§8).
	VerificationTTL time.Duration
	// MerchantSecret is the merchant of record's signing secret (§5.2).
	MerchantSecret []byte
	// Throttle is the intake's rate (§7.2).
	Throttle Limits
}

// Deps is everything the intake reaches. There is no signer here and there is
// no signer on Service: the intake's one route to a key is Mints, which is the
// minter's own service (Check 174).
type Deps struct {
	Store    Store
	Mints    Mints
	Mailer   Mailer
	CodeHost CodeHost
	// Proofs may be nil: D452 is open, and a service with no proof check
	// refuses the open-source programme rather than treating a public
	// repository as proof of who is asking (§4.4).
	Proofs Proofs
	Now    func() time.Time
}

// Service is the intake.
type Service struct {
	cfg        Config
	freeSeat   Plan
	openSource Plan
	store      Store
	mints      Mints
	mail       Mailer
	host       CodeHost
	proofs     Proofs
	now        func() time.Time
	limiter    *throttle
}

// New builds the intake. Everything it refuses here is a deployment that would
// otherwise answer a form it cannot serve, or mint a key it should not: the
// service would rather not start.
func New(cfg Config, deps Deps) (*Service, error) {
	if cfg.Environment != Production && cfg.Environment != Staging {
		return nil, fmt.Errorf("the intake's environment is %q, and the two it has are %s and %s", cfg.Environment, Production, Staging)
	}
	if _, err := cfg.Keys.For(cfg.Environment); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, errors.New("the intake has no URL, and the link it mails is built from it")
	}
	if cfg.FreeSeat == nil || cfg.OpenSource == nil {
		return nil, errors.New("the intake serves the free seat's and the open-source programme's routes, and a plan for each is what they mint")
	}
	for _, plan := range []Plan{*cfg.FreeSeat, *cfg.OpenSource, cfg.Paid} {
		if strings.TrimSpace(plan.Name) == "" {
			return nil, errors.New("a plan with no name is one a record cannot name")
		}
		if _, known := tierMaxTerm[plan.Tier]; !known {
			return nil, fmt.Errorf("the plan %q names the tier %q, which is not one a key can be issued for", plan.Name, plan.Tier)
		}
	}
	if cfg.FreeSeat.TermDays < 1 || cfg.FreeSeat.Seats < 1 || cfg.OpenSource.TermDays < 1 || cfg.OpenSource.Seats < 1 {
		return nil, errors.New("a plan a check mints from needs a term in days and at least one seat")
	}
	if cfg.VerificationTTL <= 0 {
		return nil, errors.New("the verification links have no life, and a token that never expires is a key in a mailbox forever")
	}
	if len(cfg.MerchantSecret) == 0 {
		return nil, errors.New("the intake holds no merchant secret, and without one it would read an unsigned body")
	}
	switch {
	case deps.Store == nil:
		return nil, errors.New("the intake has no store, so it can neither remember a mint nor tell a redelivery from a first one")
	case deps.Mints == nil:
		return nil, errors.New("the intake has no minter: this half cannot sign, so a key it cannot ask for is a key it does not issue")
	case deps.Mailer == nil:
		return nil, errors.New("the intake has no mail path, and an unverified application is no application")
	case deps.CodeHost == nil:
		return nil, errors.New("the intake cannot read a code host, so the open-source check can read no repository")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	limiter, err := newThrottle(cfg.Throttle, now)
	if err != nil {
		return nil, err
	}
	cfg.FreeMailDomains = lowerAll(cfg.FreeMailDomains)
	return &Service{
		cfg:        cfg,
		freeSeat:   *cfg.FreeSeat,
		openSource: *cfg.OpenSource,
		store:      deps.Store,
		mints:      deps.Mints,
		mail:       deps.Mailer,
		host:       deps.CodeHost,
		proofs:     deps.Proofs,
		now:        now,
		limiter:    limiter,
	}, nil
}

// Handler serves the intake. The methods are part of the routes: the three
// forms are POSTs because an application carries an address, and the
// verification link is a GET because it is a link in a mail.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+RouteFreeSeat, s.freeSeatForm)
	mux.HandleFunc("POST "+RouteOpenSource, s.openSourceForm)
	mux.HandleFunc("POST "+RouteRequest, s.requestForm)
	mux.HandleFunc("GET "+RouteVerify, s.verifyLink)
	mux.HandleFunc("POST "+RouteWebhook, s.webhook)
	return mux
}

// freeSeat is the free seat's route (§4.1–§4.3): a free-mail domain and a
// domain that already holds a free key go to the queue, a domain the record
// knows has bought is refused, and anything else is mailed a verification link
// and minted when it comes back.
func (s *Service) freeSeatForm(w http.ResponseWriter, r *http.Request) {
	_, email, domain, ok := s.applicant(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	paid, err := s.paidFor(ctx, domain)
	if err != nil {
		fail(w, err)
		return
	}
	if paid {
		answer(w)
		return
	}
	held, err := s.holdsFree(ctx, domain)
	if err != nil {
		fail(w, err)
		return
	}
	if s.freeMail(domain) || held {
		id, err := newID()
		if err != nil {
			fail(w, err)
			return
		}
		if err := s.store.SaveApplication(ctx, Application{
			ID:     id,
			Plan:   s.freeSeat.Name,
			Email:  email,
			Domain: domain,
			At:     s.now(),
		}); err != nil {
			fail(w, err)
			return
		}
		answer(w)
		return
	}
	if err := s.pending(ctx, Verification{
		Plan:         s.freeSeat.Name,
		Email:        email,
		Domain:       domain,
		Organisation: domain,
	}); err != nil {
		fail(w, err)
		return
	}
	answer(w)
}

// openSource is the open-source programme's route (§4.4): every repository the
// applicant named has to be public under a licence the OSI approves and D452's
// proof of control has to be given, and only then is the address verified.
func (s *Service) openSourceForm(w http.ResponseWriter, r *http.Request) {
	form, email, domain, ok := s.applicant(w, r)
	if !ok {
		return
	}
	repositories := splitLines(form.Get("repositories"))
	if len(repositories) == 0 {
		answer(w)
		return
	}
	organisation, passed := s.openSourceControl(r.Context(), OpenSource{Email: email, Repositories: repositories})
	if !passed {
		answer(w)
		return
	}
	if err := s.pending(r.Context(), Verification{
		Plan:         s.openSource.Name,
		Email:        email,
		Domain:       domain,
		Organisation: organisation,
		Repositories: repositories,
	}); err != nil {
		fail(w, err)
		return
	}
	answer(w)
}

// request is the manual queue's route (§4.5): the third form is the requests the
// checks cannot decide, and every one of them is a person's. Nothing is mailed
// and nothing is minted — the request itself is what the owner reads.
func (s *Service) requestForm(w http.ResponseWriter, r *http.Request) {
	form, email, domain, ok := s.applicant(w, r)
	if !ok {
		return
	}
	id, err := newID()
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.store.SaveApplication(r.Context(), Application{
		ID:     id,
		Email:  email,
		Domain: domain,
		Detail: detail(form.Get("request")),
		At:     s.now(),
	}); err != nil {
		fail(w, err)
		return
	}
	answer(w)
}

// verify follows a link the intake mailed (§4.1). The token is spent before the
// mint, so a link followed twice mints once, and a link that has expired mints
// nothing.
func (s *Service) verifyLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.URL.Query().Get("token")
	if token == "" {
		answer(w)
		return
	}
	held, err := s.store.Verifications(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	var pending Verification
	found := false
	for _, candidate := range held {
		if candidate.ID == digest(token) {
			pending, found = candidate, true
			break
		}
	}
	if !found || pending.Used || !s.now().Before(pending.Expires) {
		answer(w)
		return
	}
	pending.Used = true
	if err := s.store.SaveVerification(ctx, pending); err != nil {
		fail(w, err)
		return
	}
	plan, known := s.planFor(pending.Plan)
	if !known {
		answer(w)
		return
	}
	if plan.Name == s.freeSeat.Name {
		// §4.3 and §4.2 are re-read at the moment of minting: the two days
		// between an application and its link are two days in which the domain
		// could have bought, or another applicant could have arrived.
		paid, err := s.paidFor(ctx, pending.Domain)
		if err != nil {
			fail(w, err)
			return
		}
		held, err := s.holdsFree(ctx, pending.Domain)
		if err != nil {
			fail(w, err)
			return
		}
		if paid || held {
			answer(w)
			return
		}
	}
	if _, err := s.mint(ctx, s.mintRequest(plan, pending.Organisation, plan.Seats, plan.TermDays), pending.Email, "verification:"+pending.ID); err != nil {
		fail(w, err)
		return
	}
	answer(w)
}

// applicant reads one of the site's form submissions: an address the intake can
// mail to, inside the deploy's throttle. What it refuses it answers itself, with
// the one page every outcome gets (§7.1).
func (s *Service) applicant(w http.ResponseWriter, r *http.Request) (url.Values, string, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		answer(w)
		return nil, "", "", false
	}
	email, valid := address(r.PostFormValue("email"))
	if !valid {
		answer(w)
		return nil, "", "", false
	}
	if !s.limiter.allow("address:"+email, "ip:"+hostOf(r.RemoteAddr)) {
		answer(w)
		return nil, "", "", false
	}
	return r.PostForm, email, domainOf(email), true
}

// pending writes one verification and mails its link.
//
// The store keeps the token's digest and never the token (§7.3's "no key
// material" read the same way): a leaked store is then a list of links rather
// than a list of live ones, and the link itself is what the applicant holds.
func (s *Service) pending(ctx context.Context, v Verification) error {
	token, err := newSecret()
	if err != nil {
		return err
	}
	v.ID = digest(token)
	v.Expires = s.now().Add(s.cfg.VerificationTTL)
	if err := s.store.SaveVerification(ctx, v); err != nil {
		return err
	}
	mail := verificationMail(s.cfg.URL, token, s.cfg.VerificationTTL)
	mail.To = v.Email
	return s.mail.Send(ctx, mail)
}

// mint asks the minter for one key, writes the record and mails the key to the
// applicant.
//
// The record is written before the answer is returned (§5.2), and it is the
// record and not a reservation: two deliveries of one event in flight at once
// both reach the minter and the second's write is refused, so one key is minted
// twice and one record is kept. What makes that harmless is that the two keys
// are the same key in every claim — and closing it properly is a store that
// reserves a source, which is the durable backend's (S3.5/S3.7).
func (s *Service) mint(ctx context.Context, req Request, applicant, source string) (Minted, error) {
	answer, err := s.mints.Mint(ctx, req)
	if err != nil {
		return Minted{}, err
	}
	answer.Record.Source = source
	answer.Record.Email = applicant
	if err := s.store.SaveIssuance(ctx, answer.Record); err != nil {
		if errors.Is(err, ErrAlreadyRecorded) {
			return answer, nil
		}
		return Minted{}, err
	}
	if applicant != "" {
		mail := keyMail(applicant, answer.Key)
		if err := s.mail.Send(ctx, mail); err != nil {
			return answer, err
		}
	}
	return answer, nil
}

// answerPage is the one page every application gets (§7.1): whether the key was
// minted, queued or the domain already holds one, the answer is this sentence —
// which is what keeps the form from being a lookup for someone else's licence
// state (T13 item 10).
const answerPage = "Thank you. Your request has been received.\n\n" +
	"If it can be granted, a licence key will arrive at the address you gave.\n"

func answer(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, answerPage)
}

// accept is the webhook's answer to everything it will not act on: `200` and no
// body (§5.2).
func (s *Service) accept(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
}

// fail is the answer to a request the service could not finish. It says nothing
// about what failed — an applicant's answer is not the place to describe the
// service's inside — and it is a `500`, which is a merchant's cue to send the
// event again (§5.2).
func fail(w http.ResponseWriter, err error) {
	log.Printf("keyservice: %v", err)
	http.Error(w, "The key service could not answer this request just now. Please try again.", http.StatusInternalServerError)
}

// address reads the one field all three forms share: one email address, and
// nothing else.
//
// It is strict on purpose. The value ends up in a mail header's recipient, so
// any whitespace or control character in it is a header injection and is refused
// rather than trimmed (§7.2, TestApplicantFieldsCannotInjectMailHeaders), and a
// comma, an angle bracket or a quote is the same hole written differently. The
// cost is that a pasted address with a trailing space is refused; the benefit is
// that the only thing the intake ever mails to is an address it read whole.
func address(raw string) (string, bool) {
	if raw == "" || len(raw) > 254 {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= ' ' || c == 0x7f {
			return "", false
		}
	}
	if strings.ContainsAny(raw, "<>,;\"\\") {
		return "", false
	}
	local, domain, found := strings.Cut(raw, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return "", false
	}
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", false
	}
	return local + "@" + strings.ToLower(domain), true
}

// domainOf is the part of an address §4.2's proxy counts: the domain, which is
// what "one per organisation, by email domain" compares.
func domainOf(email string) string {
	_, domain, found := strings.Cut(email, "@")
	if !found {
		return ""
	}
	return domain
}

// hostOf is the address a request came from, without its port. It is the
// connection's peer and not a forwarded header: a caller that sets
// X-Forwarded-For would otherwise choose its own throttle bucket (§7.2).
func hostOf(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// newID names one queued application. It is a second secret rather than a
// counter, because the store is an upsert by id and two applications that
// arrived at once must not become one.
func newID() (string, error) {
	return newSecret()
}

// newSecret is 256 bits of the system's randomness, spelled in the alphabet a
// key is: what a verification token and an application's id are made of.
func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("the system's randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// digest is what the store keeps in place of a token.
func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func lowerAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
