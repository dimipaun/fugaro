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
// are the two halves' handlers, and what keeps them apart is which one holds a
// Signer.
//
// Neither half is a deployable program yet and this package holds no `main`: the
// two entry points, and with them the durable Store, the code host's client, the
// mail provider, D452's proof check and the key ring's signing client, are the
// deploy's (§11, S3.7) — the images S3.5's roots name are built from this
// package's source and nothing in this tree builds one. What is here is what
// those pieces plug into, driven in a test through Deps.

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
	"sync"
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
	// The merchant's events are the portal's API door and not the site's: F16
	// moves the route under the subtree Hosting rewrites here (api.go), and the
	// merchant's own webhook is configured at that address (P0.2).
	RouteWebhook = "/api/paddle/webhook"
	// The renewal and the status routes are the site's forms' siblings and not
	// the site's own (§7, §8): the customer's project is what asks them, and
	// what they carry is a key's secret rather than an applicant's address.
	RouteRenew  = "/renew"
	RouteStatus = "/status"
)

func init() {
	// The merchant's event route is the intake's own API door, registered here
	// from the table every area registers into: an area adds its own routes from
	// its own file's init, so no later task edits this one (api.go).
	registerAPI(http.MethodPost, RouteWebhook, (*Service).webhook, withBody(maxEventBytes))
}

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
	// CodeHosts are the hosts whose repositories the open-source check reads, as
	// hosts — `code.example`, never a URL. §4.4's check reads a host's own API
	// with the deploy's credentials, so a line naming any other host is refused
	// rather than looked up: nothing an applicant types is an address this
	// service dials (§7.2). It is configuration because the deploy's code host
	// is (D442's merchant is not it).
	CodeHosts []string
	// MaxRepositories is how many repositories one open-source application may
	// name. Each one is a call into the deploy's own code-host credentials, made
	// on the strength of an unauthenticated form post, so the bound belongs to
	// the deploy beside the rate: an application that names more is refused
	// before the first call rather than read (§4.4, §7.2).
	MaxRepositories int
	// Throttle is the intake's rate (§7.2).
	Throttle Limits
	// AdminAccount is the one account the admin page answers to: the project
	// owner's (§6, D455). There is exactly one Licensor, so this is one account
	// and never a list.
	AdminAccount string
	// AdminSecret is what the page's tokens are signed with (§6's CSRF row). It
	// is the deploy's, and it is what makes a token unforgeable: with it, a page
	// the owner visits cannot mint, because nothing else can render a token this
	// service accepts.
	AdminSecret []byte
	// AdminTokenTTL is how long a rendered form stays submittable. It is short,
	// because the token is bound to one account and one action and there is no
	// reason for a page opened this morning to still be good this evening.
	AdminTokenTTL time.Duration
	// MintCaps are D454's per-tier caps, one per tier a key can be issued for.
	// They are configuration and the numbers are the owner's; a tier the deploy
	// does not cap is not capped, which is why New refuses a deployment that
	// leaves one out rather than running one tier unlimited.
	MintCaps map[string]Limits
	// PagePlans are the plans only the page reaches (§5.5's Enterprise keys).
	// They are here rather than in Paid because a merchant event's plan is the
	// event's and this one is an Order Form's.
	PagePlans []Plan
	// PaddleAPIKey is the merchant of record's own key (P4.1). It is the
	// deploy's secret and it stays here: a transaction the browser could create
	// is one whose custom_data is the buyer's to forge (F15).
	PaddleAPIKey []byte
	// ResendKey is the mail provider's key (P0.2), which is what the API's own
	// mail path sends with (P2.2).
	ResendKey []byte
	// SentryDSN is where the API reports what failed (P1.6). Empty is off, and
	// off is a deployment that reports nowhere rather than one that reports to
	// whatever a body names.
	SentryDSN string
	// SchedulerAudience is the audience Cloud Scheduler's tokens carry (F33):
	// the API checks the account and this, so a token minted for another
	// service is refused even when an allowed account signed it.
	SchedulerAudience string
	// SealerKeyName is the KMS key the handover is sealed under (F3, P1.7).
	SealerKeyName string
	// TenantsCollection is the document collection the organisations' lists are
	// kept in: the invitation domains the operations organisation allows and the
	// Microsoft (Entra ID) directories a reseller pins (D2207, plan P5.1). It is
	// configuration
	// because the deploy names its own collections, and the lists themselves are
	// data an operations owner sets.
	TenantsCollection string
}

// Deps is everything the intake reaches. There is no signer here and there is
// no signer on Service: the intake's one route to a key is Mints, which is the
// minter's own service (Check 174).
type Deps struct {
	Store    Store
	Mints    Mints
	Mailer   Mailer
	CodeHost CodeHost
	// MintsV1 may be nil: it is the intake's route to a v1 key, which the
	// renewal endpoint is the only caller of (§7, K2.3), and a deployment that
	// mints only the older format has none. The renewal route then answers that
	// it cannot mint rather than reaching for a signer this half never holds
	// (Check 174).
	MintsV1 MintsV1
	// Proofs may be nil: D452 is open, and a service with no proof check
	// refuses the open-source programme rather than treating a public
	// repository as proof of who is asking (§4.4).
	Proofs Proofs
	// Owner is the project's own identity, which is what the admin page reads to
	// find the account a request is signed in as (§6).
	Owner Owner
	// StepUp may be nil: **D455 is open**, and a service with no second factor
	// mints nothing from the page and renders it read-only rather than trusting
	// a session alone (§6).
	StepUp StepUp
	// Notifier tells the owner a request is in the queue (§4.5). It is not the
	// applicant's mail path and it is not optional: a queue nobody is told about
	// is one nobody reads.
	Notifier Notifier
	// Auditor may be nil: **D456 is open**, and with none the audit line goes to
	// the process's own log, which is the project's log sink as far as this
	// package can express one (§6).
	Auditor Auditor
	// The seams the portal's API reaches (P1.0). Each may be nil: a deployment
	// that mounts no route reaching one has no business holding it, and New
	// refuses a missing seam only when a mounted route declares it (api.go).
	Docs      Docs
	SignIn    SignInVerifier
	Paddle    Paddle
	Scheduler Scheduler
	Sealer    Sealer
	Now       func() time.Time
}

// Service is the intake.
type Service struct {
	cfg        Config
	freeSeat   Plan
	openSource Plan
	store      Store
	mints      Mints
	mintsV1    MintsV1
	mail       Mailer
	host       CodeHost
	proofs     Proofs
	signedIn   Owner
	stepUp     StepUp
	alerts     Notifier
	auditor    Auditor
	now        func() time.Time
	limiter    *throttle
	// renewMu is one renewal at a time in this process. A renewal reads the key's
	// state, mints and then writes what records the rotation, and two of them in
	// flight together would both read "no successor yet" and both mint — of which
	// only one can be the rotation the store records, so the other would answer
	// with a key nothing would let renew (renew's own comment).
	//
	// It is this process's, like pageMu above and for the same reason: what a
	// second instance does is decided by the store's record of the rotation and
	// by the handover beside it (D2135), which is where the 24-hour window lives.
	renewMu sync.Mutex
	// pageMu is the page's actions one at a time. A minting action is a count,
	// an audit line and a key, and the count reads the record the key writes:
	// two of them in flight together would each count a record that does not
	// hold the other's key yet, which is D454's cap minted past and one
	// approval minted twice over a source the store was asked about too early
	// (TestConcurrentMintsRespectTheCap, TestConcurrentApprovalsMintOnce).
	//
	// It is the page's and not the intake's: §5.1's events are idempotent by
	// their own ids in the store, and the page is one person clicking. What a
	// lock in one process cannot reach is a second process, whose record is its
	// own until the store is a service — which is the store that reserves a
	// source (mint's own comment, §5.2).
	pageMu sync.Mutex
	// signIn is the portal's verifier of the licence project's own sign-ins
	// (api.go's seam, P1.3): the trial route reads it to learn the account that
	// asks, which is half of what §14 counts (trial.go).
	signIn SignInVerifier
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
	if cfg.MaxRepositories < 1 {
		return nil, errors.New("the intake's repository cap is not set, and an open-source application that names a thousand repositories is a thousand calls into the deploy's code-host credentials")
	}
	cfg.CodeHosts = lowerAll(cfg.CodeHosts)
	if len(cfg.CodeHosts) == 0 {
		return nil, errors.New("the intake reads repositories from no host, and §4.4's check would then read whatever an applicant typed")
	}
	for _, host := range cfg.CodeHosts {
		if strings.ContainsAny(host, "/:") {
			return nil, fmt.Errorf("the intake is configured to read repositories from %q, and a host is a name and not a URL", host)
		}
	}
	if strings.TrimSpace(cfg.AdminAccount) == "" {
		return nil, errors.New("the admin page has no account, and the page mints any key for any tier: the one account it answers to is the deploy's to name (D455)")
	}
	if len(cfg.AdminSecret) < minAdminSecret {
		return nil, fmt.Errorf("the admin page's token secret is %d byte(s), and a secret that short is one the page's own tokens can be forged with", len(cfg.AdminSecret))
	}
	if cfg.AdminTokenTTL <= 0 {
		return nil, errors.New("the admin page's forms have no life, and a form rendered once would stay submittable for ever")
	}
	if len(cfg.PagePlans) == 0 {
		return nil, errors.New("the admin page has no plan of its own, so it could mint nothing the site's forms cannot (§5.5)")
	}
	named := map[string]bool{}
	for _, plan := range []Plan{*cfg.FreeSeat, *cfg.OpenSource, cfg.Paid} {
		named[plan.Name] = true
	}
	for _, plan := range cfg.PagePlans {
		if strings.TrimSpace(plan.Name) == "" {
			return nil, errors.New("a plan with no name is one a record cannot name")
		}
		if named[plan.Name] {
			return nil, fmt.Errorf("the admin page's plan %q is a name one of the site's forms already mints under, and a name two plans share is one a record reads wrongly", plan.Name)
		}
		named[plan.Name] = true
		if _, known := tierMaxTerm[plan.Tier]; !known {
			return nil, fmt.Errorf("the plan %q names the tier %q, which is not one a key can be issued for", plan.Name, plan.Tier)
		}
	}
	for tier := range tierMaxTerm {
		cap, capped := cfg.MintCaps[tier]
		if !capped {
			return nil, fmt.Errorf("the admin page has no mint cap for the tier %q, and a page that mints any key for any tier with a tier left uncapped is one session away from draining the key ring's authority (D454)", tier)
		}
		if cap.Requests < 1 || cap.Window <= 0 {
			return nil, fmt.Errorf("the mint cap for %q is %d mint(s) per %s: a cap the deploy names, and a cap of none is not a limit", tier, cap.Requests, cap.Window)
		}
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
	case deps.Owner == nil:
		return nil, errors.New("the intake has no identity to read, so the admin page could not tell the owner from anyone else")
	case deps.Notifier == nil:
		return nil, errors.New("the intake has no notifier, so a queued request would wait for an owner who is never told")
	}
	if err := apiRoutes.missing(deps); err != nil {
		return nil, err
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
		mintsV1:    deps.MintsV1,
		mail:       deps.Mailer,
		host:       deps.CodeHost,
		proofs:     deps.Proofs,
		signedIn:   deps.Owner,
		stepUp:     deps.StepUp,
		alerts:     deps.Notifier,
		auditor:    deps.Auditor,
		now:        now,
		limiter:    limiter,
		signIn:     deps.SignIn,
	}, nil
}

// Handler serves the intake. The methods are part of its routes: the three
// forms are POSTs because an application carries an address, and the
// verification link is a GET because it is a link in a mail. The page below is
// the one surface whose routes carry none, and the portal's own routes are the
// API's table's (api.go) rather than lines here.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+RouteFreeSeat, s.freeSeatForm)
	mux.HandleFunc("POST "+RouteOpenSource, s.openSourceForm)
	mux.HandleFunc("POST "+RouteRequest, s.requestForm)
	mux.HandleFunc("GET "+RouteVerify, s.verifyLink)
	// §7's renewal and §8's status: POSTs, because what they carry is a key's
	// secret and a secret never belongs in a URL.
	mux.HandleFunc("POST "+RouteRenew, s.renew)
	mux.HandleFunc("POST "+RouteStatus, s.status)
	// §6's page: where the queue is read and where the owner's key is minted.
	// The patterns carry no method, and the exact one beside the subtree is
	// what keeps the mux from answering a bare /admin with a redirect to
	// /admin/ before anything is asked: the account is asked before the route
	// is chosen, so nothing here tells a stranger the page exists
	// (adminSurface).
	mux.HandleFunc(RouteAdmin, s.adminSurface)
	mux.HandleFunc(RouteAdmin+"/", s.adminSurface)
	// Every door of the portal's API, from the table the areas register into.
	apiRoutes.mount(mux, s)
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
		if err := s.enqueue(ctx, Application{
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
//
// The count is capped before the first call. The form's body is bounded at
// maxFormBytes and that alone is not a bound on the work: 16 KiB of short URLs
// is more repositories than the deploy's code-host credentials should be asked
// about on the strength of one unauthenticated post.
//
// Every line is read against the deploy's own code host before any of them is
// asked about (§4.4): one line the host does not serve refuses the whole
// application, so the record is never a list with a stranger's address in it.
func (s *Service) openSourceForm(w http.ResponseWriter, r *http.Request) {
	form, email, domain, ok := s.applicant(w, r)
	if !ok {
		return
	}
	lines := splitLines(form.Get("repositories"))
	if len(lines) == 0 || len(lines) > s.cfg.MaxRepositories {
		answer(w)
		return
	}
	repositories := make([]RepositoryRef, 0, len(lines))
	named := make([]string, 0, len(lines))
	for _, line := range lines {
		repository, served := s.repository(line)
		if !served {
			answer(w)
			return
		}
		repositories = append(repositories, repository)
		named = append(named, repository.URL)
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
		// What is stored is the canonical URL of each repository on the deploy's
		// own host, and never the line the applicant typed: the record is what a
		// person reads and what a later check would re-read (§4.4, §7.3).
		Repositories: named,
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
	if err := s.enqueue(r.Context(), Application{
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
// nothing. A request that could not be finished hands the token back, so a link
// is never spent on a key nobody received.
//
// Every link that cannot be followed again loses the address it was about
// (§7.3): the one this request spends, when the request it spent it for is
// over, and every other one whose window has closed — a token nobody ever
// follows is the common case, since an applicant who does not prove the mailbox
// is the applicant the link exists to filter out.
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
	// §7.3 drops a token's address once it is spent or expires, and this read is
	// the only sweep the package has: a durable store expires them itself (a
	// TTL, or D457's period), which is S3.5's, and until it does, a request to
	// this route is what takes the store's dead links out of it.
	for _, candidate := range held {
		if candidate.Used || !s.now().Before(candidate.Expires) {
			s.dropPersonal(ctx, candidate)
		}
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
		s.dropPersonal(ctx, pending)
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
			s.dropPersonal(ctx, pending)
			answer(w)
			return
		}
	}
	if _, err := s.mint(ctx, s.mintRequest(plan, pending.Organisation, plan.Seats, plan.TermDays), pending.Email, "verification:"+pending.ID); err != nil {
		// The link is handed back. A token spent on a request that failed is a
		// dead link with no key behind it, and the applicant has no way to ask
		// again: the mail that carried the link is the only place it existed
		// (§4.1, §7.2). What the spending closed reopens here — a second follow
		// of the same link is then one more mint, which is a key delivered twice
		// rather than a key delivered never.
		pending.Used = false
		if err := s.store.SaveVerification(ctx, pending); err != nil {
			log.Printf("keyservice: giving the verification %s back: %v", pending.ID, err)
		}
		fail(w, err)
		return
	}
	s.dropPersonal(ctx, pending)
	answer(w)
}

// dropPersonal is §7.3's rule at the one place a verification's personal data
// can go: the link cannot be followed again — it minted, it was refused for
// good, or its window closed — so the address it was about, its domain and the
// repositories an application named are dropped, and what is left is the digest,
// the plan and the instant, none of which is a person.
//
// It is called on the way past rather than checked for, and a store that
// refuses the write is logged and not answered with a failure: the applicant's
// answer is about a key, not about a bookkeeping write, the key is delivered
// whatever the store does, and the next follow's sweep is what drops it instead.
func (s *Service) dropPersonal(ctx context.Context, v Verification) {
	if v.Email == "" && v.Domain == "" && v.Organisation == "" && len(v.Repositories) == 0 {
		return
	}
	v.Email, v.Domain, v.Organisation, v.Repositories = "", "", "", nil
	if err := s.store.SaveVerification(ctx, v); err != nil {
		log.Printf("keyservice: dropping the verification %s's personal data: %v", v.ID, err)
	}
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
	if !s.limiter.allow("address:"+email, "ip:"+clientIP(r)) {
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

// mint asks the minter for one key, mails it to the applicant and writes the
// record.
//
// The mail comes **before** the record, and that order is the whole of it: a key
// is mailed and never stored (§7.3), so a record written for a mail that failed
// is a source spent on a key nobody holds — the merchant's redelivery finds the
// source recorded and mails nothing, and the customer has paid for a key that
// exists nowhere (§5.2). Mailing first makes a failed delivery a failed request:
// nothing is recorded, the source stays free, and the next delivery mints and
// mails again. The price is the other order's failure: a mail that lands and a
// write that fails delivers the key twice on the retry, which is a duplicate key
// rather than a lost one.
//
// The record is still written before the answer is returned (§5.2), and it is
// the record and not a reservation: two deliveries of one event in flight at
// once both reach the minter and both mail, and the second's write is refused,
// so one key is minted twice and one record is kept. What makes that harmless is
// that the two keys are the same key in every claim — and closing it properly is
// a store that reserves a source, which is the durable backend's (S3.5/S3.7).
func (s *Service) mint(ctx context.Context, req Request, applicant, source string) (Minted, error) {
	answer, err := s.mints.Mint(ctx, req)
	if err != nil {
		return Minted{}, err
	}
	answer.Record.Source = source
	answer.Record.Email = applicant
	if applicant != "" {
		if err := s.mail.Send(ctx, keyMail(applicant, answer.Key)); err != nil {
			return Minted{}, err
		}
	}
	if err := s.store.SaveIssuance(ctx, answer.Record); err != nil {
		if errors.Is(err, ErrAlreadyRecorded) {
			return answer, nil
		}
		return Minted{}, err
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
// It is strict on purpose, and it reads each half by its own grammar rather than
// by a list of characters to refuse. The value ends up in a mail header's
// recipient, so any whitespace or control character in it is a header injection
// and is refused rather than trimmed (§7.2,
// TestApplicantFieldsCannotInjectMailHeaders); everything else RFC 5322's
// `specials` list holds — the comma, the angle bracket, the semicolon, the
// quote, the backslash, the comment — is not atext and not a label, so it fails
// here by the same rule that admits `dev@acme.example` and needs no second list
// to keep in step.
//
// Reading the domain as a name of labels is what the three eligibility checks
// stand on: the free-mail queue (§4.1), the one-free-key-per-domain count (§4.2)
// and the paid-domain refusal (§4.3) all compare the domain, so a domain that is
// not one is a domain they compare wrongly. `maintainer@gmail.example(x)` is the
// case that names it — a comment, deliverable by net/mail to gmail.example and
// by no parse a domain equal to gmail.example — which would otherwise walk past
// all three.
//
// The cost is that a pasted address carrying a trailing space, a comment or a
// quoted local part is refused; the benefit is that the only address the intake
// mails to, and the only domain it counts, is one it read whole.
func address(raw string) (string, bool) {
	if raw == "" || len(raw) > 254 {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= ' ' || c == 0x7f {
			return "", false
		}
	}
	local, domain, found := strings.Cut(raw, "@")
	if !found || !localPart(local) {
		return "", false
	}
	domain = strings.ToLower(domain)
	if !domainName(domain) {
		return "", false
	}
	return local + "@" + domain, true
}

// localPart is RFC 5322's dot-atom: one or more atext atoms joined by single
// dots. A leading, trailing or doubled dot is a different production and not
// this one, which is why the atoms are read rather than the characters.
func localPart(local string) bool {
	if local == "" || len(local) > 64 || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") {
		return false
	}
	for _, atom := range strings.Split(local, ".") {
		if atom == "" {
			return false
		}
		for i := 0; i < len(atom); i++ {
			if !isAtext(atom[i]) {
				return false
			}
		}
	}
	return true
}

// isAtext is RFC 5322's `atext`: the characters a bare word is made of. Every
// character `specials` names is outside it, which is what makes the local part's
// grammar the header-injection rule as well as the address one.
func isAtext(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0
}

// domainName is a dotted name of at least two labels, which is the shape §4.1's
// list and §4.2's count compare: a comment, a quoted string, a bracketed address
// literal and a name that is not ASCII all fail here, so the domain the checks
// read is one a mail provider reads the same way. A name of one label — the
// `localhost` an address is not reachable at — is refused for the same reason
// the rest of the form is: the intake mails, and it has nothing to mail to.
func domainName(domain string) bool {
	if len(domain) > 253 {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !domainLabel(label) {
			return false
		}
	}
	return true
}

// domainLabel is one label of a name: one to 63 characters, a letter or a digit,
// with a hyphen inside it and at neither end.
func domainLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i := 0; i < len(label); i++ {
		switch c := label[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(label)-1:
		default:
			return false
		}
	}
	return true
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

// clientIP is the address a request came from, which is §7.2's per-IP bucket.
//
// It is the **last** entry of X-Forwarded-For and not the connection's peer,
// because the peer is the platform's own front end: on Cloud Run every applicant
// arrives from the same one, so a bucket keyed on it is one bucket for the whole
// internet, and the intake refuses everyone as soon as D454's rate is reached
// (§3.4, §7.2). The front end appends the address it saw to whatever the caller
// sent, so the last entry is that address and the entries a caller writes itself
// are the earlier ones this reads past — a caller cannot pick its own bucket.
// With no header at all — a test, or a local run — the peer is the only address
// there is and it is the fallback.
func clientIP(r *http.Request) string {
	forwarded := r.Header.Values("X-Forwarded-For")
	for i := len(forwarded) - 1; i >= 0; i-- {
		entries := strings.Split(forwarded[i], ",")
		for j := len(entries) - 1; j >= 0; j-- {
			if entry := strings.TrimSpace(entries[j]); entry != "" {
				return entry
			}
		}
	}
	return hostOf(r.RemoteAddr)
}

// hostOf is the address a request's connection ended at, without its port.
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
