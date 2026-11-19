// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The intake: the internet-facing half of the key service (§3.1). It serves the
// merchant's events, the portal's API (api.go) and the routes a customer's own
// installation and CLI ask, it keeps the record, and it **cannot sign** — every
// key it hands out it asked the minter's own service for (Check 174).
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
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The routes. S3.3's three forms and S3.4's admin page are retired (portal plan
// P9.3): a licence is asked for in the portal now, and what is left to this
// table is the merchant's door, the renewal and status routes the customer's own
// installation asks (§7, §8) and §11's two sign-in flows.
const (
	// The merchant's events are the portal's API door and not the site's: F16
	// moves the route under the subtree Hosting rewrites here (api.go), and the
	// merchant's own webhook is configured at that address (P0.2).
	RouteWebhook = "/api/paddle/webhook"
	// The renewal and the status routes are the CLI's and not the site's either:
	// the customer's project is what asks them, and what they carry is a key's
	// secret rather than an applicant's address.
	RouteRenew  = "/renew"
	RouteStatus = "/status"
	// §11's two sign-in flows are the CLI's and not the site's either: the
	// person reaches them through the licence site's page (K5.1) and what they
	// hand back is a key, and the routes themselves are named in signin.go
	// beside the rules that answer them.
)

func init() {
	// The merchant's event route is the intake's own API door, registered here
	// from the table every area registers into: an area adds its own routes from
	// its own file's init, so no later task edits this one (api.go).
	registerAPI(http.MethodPost, RouteWebhook, (*Service).webhook, withBody(maxEventBytes))
}

// The bounds on what a stranger may hand the service. Each is a body a form, a
// sign-in step or a merchant event is expected to fit in by orders of magnitude,
// and the point of a bound is that the service reads a body it can hold rather
// than one that arrives in pieces.
const (
	maxFormBytes    = 16 << 10
	maxEventBytes   = 256 << 10
	maxRequestBytes = 64 << 10
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
	// canonical host (§3.1). It is what the portal's own origin is read from
	// (portalOrigin, the one origin a deployment states), and never the
	// request's Host header (§7.2).
	URL string
	// FreeSeat is D449's free-seat plan. The free seat is granted as an
	// entitlement when an organisation is created (D2199, freeSeatOnOrgCreated),
	// so the form that minted from this plan is gone (P9.3) and nothing here reads
	// it: it stays because FUGARO_PLANS still states it and config.go requires the
	// document to name one, and taking it out of that document is a change to the
	// deployment's own shape rather than to this one.
	FreeSeat *Plan
	// OpenSource is the plan the portal's open-source route mints from (P3.3).
	// The numbers are D449's, which is why they are configuration: the owner
	// rules them and a deploy states them.
	OpenSource *Plan
	// Paid is the plan a merchant event mints under. Its seats are the event's
	// and its term is the event's (§5.1).
	Paid Plan
	// FreeMailDomains is D453's list, as domains, lowercased here.
	FreeMailDomains []string
	// VerificationTTL was how long a verification link stayed usable (§4.1,
	// D457). The links retired with the forms that mailed them (P9.3) and nothing
	// here reads it: it stays for FreeSeat's reason — the deployment still states
	// FUGARO_VERIFICATION_TTL, and dropping a variable is the deploy's shape
	// changing.
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
	// on the strength of one request, so the bound belongs to the deploy beside
	// the rate: an application that names more is refused before the first call
	// rather than read (§4.4, §7.2).
	MaxRepositories int
	// Throttle is the intake's rate (§7.2).
	Throttle Limits
	// InviteRate is how many invitations one organisation may write in one
	// window (F35, portal plan P2.2). An invitation is mail to an address the
	// caller chooses, sent from the deployment's own verified sender and
	// carrying text the caller wrote (the organisation's name), so the door that
	// reaches it is a rate — every other path that mails has one, and this is
	// the one that a person reaches by signing up for a free organisation. It is
	// per organisation and not per caller: what an organisation's owners and
	// admins may reach is that organisation's allowance, and a second admin is
	// not a second allowance. Like Throttle it is the deploy's, and New refuses a
	// deployment that names none rather than mailing on demand.
	InviteRate Limits
	// AdminAccount, AdminSecret and AdminTokenTTL were the admin page's: the one
	// account it answered to (§6, D455), the token it signed its forms with
	// (§6's CSRF row) and that token's life. The page is retired (P9.3) and
	// nothing here reads any of the three: they stay for FreeSeat's reason — the
	// deployment still states FUGARO_ADMIN_ACCOUNT, FUGARO_ADMIN_TOKEN_TTL and
	// the `admin` secret role, and taking a secret out of it is its own change.
	AdminAccount  string
	AdminSecret   []byte
	AdminTokenTTL time.Duration
	// MintCaps are D454's per-tier caps, one per tier a key can be issued for.
	// They are configuration and the numbers are the owner's; a tier the deploy
	// does not cap is not capped, which is why New refuses a deployment that
	// leaves one out rather than running one tier unlimited.
	MintCaps map[string]Limits
	// PagePlans were the plans only the retired page reached (§5.5's Enterprise
	// keys), and nothing here reads them (P9.3). They stay for the three fields
	// above's reason: FUGARO_PLANS still states them.
	PagePlans []Plan
	// PaddleAPIKey is the merchant of record's own key (P4.1). It is the
	// deploy's secret and it stays here: a transaction the browser could create
	// is one whose custom_data is the buyer's to forge (F15).
	PaddleAPIKey []byte
	// PaddlePriceID is the price the portal's checkout sells (P4.1, D442,
	// P0.2's one price per row). It is the deploy's and not the code's for two
	// reasons: the price is a row of the seller's own account, so staging's
	// sandbox price and production's live one are different ids for one product;
	// and a price the SPA named would be a price a page chooses (F15).
	//
	// It is one price and not a map of the table's rows, because the checkout's
	// wire shape (P1.1) names a workspace and no row: this deployment sells the
	// Team month from the portal, and a second row sold there is a field that
	// shape would have to gain first.
	PaddlePriceID string
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
	// TermsVersion is the version of the terms of service this deployment
	// serves, and it is what a sign-up records as the wording the person
	// accepted (D2199, plan P2.1). It is configuration for the plans' own
	// reason: the deployment serves the text, two deployments can be serving
	// different wordings at once, and a record naming one figure for both would
	// state the wrong one of them.
	TermsVersion string
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
	// Notifier tells the operations team a request is in the queue (§4.5, the
	// portal's open-source ask). It is not the applicant's mail path and it is
	// not optional: a queue nobody is told about is one nobody reads.
	Notifier Notifier
	// The seams the portal's API reaches (P1.0). Each may be nil: a deployment
	// that mounts no route reaching one has no business holding it, and New
	// refuses a missing seam only when a mounted route declares it (api.go).
	Docs Docs
	// SignIn is **one** verifier of the licence project's own ID tokens: the
	// portal's API reaches it (P1.0, P1.3) and so do §11.3's and §11.4's sign-in
	// flows (signin.go, D2144, TN3). The two share the seam rather than holding
	// one each, because a deployment that verified a token one way for the SPA
	// and another for the CLI would be two answers about whose account it is.
	//
	// The two read a nil differently on purpose: the API refuses the start when
	// a mounted route declares it (api.go), and the sign-in flows refuse every
	// caller of theirs, at the request, with the sign-in's own uniform sentence
	// rather than a key nothing checked.
	SignIn    SignInVerifier
	Paddle    Paddle
	Scheduler Scheduler
	Sealer    Sealer
	Now       func() time.Time
}

// Service is the intake.
type Service struct {
	cfg        Config
	openSource Plan
	store      Store
	mints      Mints
	mintsV1    MintsV1
	mail       Mailer
	host       CodeHost
	proofs     Proofs
	alerts     Notifier
	now        func() time.Time
	limiter    *throttle
	// pollLimiter is the device poll's own rate, per address, and it is a bucket
	// of its own rather than the intake's: one sign-in is up to 120 polls, four
	// times the intake's whole hourly budget for one address (devicePoll).
	pollLimiter *throttle
	// invites is the portal's invitation rate, per organisation, and it is a
	// bucket of its own rather than the intake's: the intake's is keyed on the
	// address a form carries and the IP it came from, and an invitation is
	// neither — what it bounds is the mail one organisation can be made to send
	// (F35, Config.InviteRate).
	invites *throttle
	// signIn is §11's verifier of the licence project's own ID tokens, and codes
	// is what the two sign-in flows are holding: an authorize code between its
	// sign-in and its exchange, and a device code between the CLI's start and
	// its poll. Both are this process's and both belong to signin.go, which
	// states what that costs a deployment of more than one instance.
	signIn SignInVerifier
	codes  *signInCodes
	// The seams the portal's API reaches besides signIn (plan P1.0). They are
	// held here rather than read out of Deps, because Deps is the deployment's
	// whole shape and an area's route has no business holding the minter, the
	// mail path and the code host to reach the document store.
	//
	// signIn is the sign-in seam above and is not repeated: §11's flows and the
	// portal's API verify with one verifier rather than one each (Deps.SignIn's
	// own comment).
	//
	// P1.0 declared these on Deps and left Service without them, so every
	// area's task would have had to edit this file to reach its own — the first
	// area paid that once (the plan's shared-files table names it a writer of
	// this file for doing so) and no area after it does. What an area reaches,
	// it states on its routes (api.go's reaching), and a deployment that mounts
	// no route of an area holds none of that area's seams.
	docs      Docs
	paddle    Paddle
	scheduler Scheduler
	sealer    Sealer
	// signUpMu is one sign-up at a time in this process. F35's day's count is a
	// read of the memberships an account owns and then a write of one more, and
	// two sign-ups in flight together would each read a count that does not hold
	// the other's membership yet — the limit passed by racing it. The count
	// belongs inside the sign-up's own transaction, and this is where it is not:
	// a count is a query, and a query inside that transaction would make every
	// other sign-up of the day a conflict with it (orgs.go's recordReader). It
	// is this process's, like renewMu below and for the same reason — what a
	// second process does is the store's to hold, and when the store can count
	// inside a transaction the lock is what goes.
	//
	// The reseller's customer creation takes it too (resellers.go), because the
	// count it takes first is this same count of this same account's day: two
	// writers of one account's organisations are one window, whatever route they
	// came in by.
	signUpMu sync.Mutex
	// renewMu is one renewal at a time in this process. A renewal reads the key's
	// state, mints and then writes what records the rotation, and two of them in
	// flight together would both read "no successor yet" and both mint — of which
	// only one can be the rotation the store records, so the other would answer
	// with a key nothing would let renew (renew's own comment).
	//
	// It is this process's, like signUpMu above and for the same reason: what a
	// second instance does is decided by the store's record of the rotation and
	// by the handover beside it (D2135), which is where the 24-hour window lives.
	renewMu sync.Mutex
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
	// The two plans a route names: the open-source programme's, which the portal's
	// ask files its application under (P3.3), and the paid one, which the
	// merchant's events and the checkout are measured against. The free seat and
	// the retired page's plans are named by no route (P9.3) and so are checked
	// nowhere here: the free seat is granted as an entitlement from a policy row
	// (D2199, freeSeatOnOrgCreated) rather than minted from this plan.
	if cfg.OpenSource == nil {
		return nil, errors.New("the intake files the open-source programme's applications under a plan, and this deployment names none")
	}
	for _, plan := range []Plan{*cfg.OpenSource, cfg.Paid} {
		if strings.TrimSpace(plan.Name) == "" {
			return nil, errors.New("a plan with no name is one a record cannot name")
		}
		if _, known := tierMaxTerm[plan.Tier]; !known {
			return nil, fmt.Errorf("the plan %q names the tier %q, which is not one a key can be issued for", plan.Name, plan.Tier)
		}
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
	for tier := range tierMaxTerm {
		cap, capped := cfg.MintCaps[tier]
		if !capped {
			return nil, fmt.Errorf("operations has no mint cap for the tier %q, and an operations mint with a tier left uncapped is one session away from draining the key ring's authority (D454)", tier)
		}
		if cap.Requests < 1 || cap.Window <= 0 {
			return nil, fmt.Errorf("the mint cap for %q is %d mint(s) per %s: a cap the deploy names, and a cap of none is not a limit", tier, cap.Requests, cap.Window)
		}
	}
	if strings.TrimSpace(cfg.TermsVersion) == "" {
		return nil, errors.New("the portal records the terms of service a sign-up accepted and this deployment names no version, so every organisation it created would record an acceptance that names no wording (D2199)")
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
	// The poll's rate is this package's and not the deploy's (devicePollLimits),
	// so this cannot fail while that constant is a rate — and it is checked
	// rather than assumed because the constant is a line somebody can edit.
	pollLimiter, err := newThrottle(devicePollLimits, now)
	if err != nil {
		return nil, fmt.Errorf("the device poll's rate is %d request(s) per %s: a poll with no rate is one a stranger sets", devicePollLimits.Requests, devicePollLimits.Window)
	}
	// The invitations' rate is the deploy's (F35), beside the intake's own: an
	// invitation mails an address the caller chooses, so an organisation with no
	// rate is one whose owners can be made to mail on demand.
	inviteLimiter, err := newThrottle(cfg.InviteRate, now)
	if err != nil {
		return nil, fmt.Errorf("the portal's invitations cannot be served without a rate of their own: %w", err)
	}
	cfg.FreeMailDomains = lowerAll(cfg.FreeMailDomains)
	return &Service{
		cfg:         cfg,
		openSource:  *cfg.OpenSource,
		store:       deps.Store,
		mints:       deps.Mints,
		mintsV1:     deps.MintsV1,
		mail:        deps.Mailer,
		host:        deps.CodeHost,
		proofs:      deps.Proofs,
		alerts:      deps.Notifier,
		now:         now,
		limiter:     limiter,
		pollLimiter: pollLimiter,
		invites:     inviteLimiter,
		signIn:      deps.SignIn,
		codes:       newSignInCodes(),
		docs:        deps.Docs,
		paddle:      deps.Paddle,
		scheduler:   deps.Scheduler,
		sealer:      deps.Sealer,
	}, nil
}

// Handler serves the intake. The methods are part of its routes: §7's renewal
// and §8's status are POSTs because what they carry is a key's secret and a
// secret never belongs in a URL, and the portal's own routes are the API's
// table's (api.go) rather than lines here.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	// §7's renewal and §8's status: POSTs, because what they carry is a key's
	// secret and a secret never belongs in a URL.
	mux.HandleFunc("POST "+RouteRenew, s.renew)
	mux.HandleFunc("POST "+RouteStatus, s.status)
	// §11.3's and §11.4's two flows (signin.go). The two browser steps carry
	// their method in the pattern, so the mux answers a method nothing serves
	// with its own refusal rather than with a handler's.
	mux.HandleFunc("GET "+RouteAuthorize, s.authorize)
	mux.HandleFunc("POST "+RouteAuthorize, s.authorize)
	mux.HandleFunc("POST "+RouteToken, s.token)
	mux.HandleFunc("POST "+RouteDeviceStart, s.deviceStart)
	mux.HandleFunc("POST "+RouteDevicePoll, s.devicePoll)
	mux.HandleFunc("GET "+RouteDeviceApprove, s.deviceApprove)
	mux.HandleFunc("POST "+RouteDeviceApprove, s.deviceApprove)
	// Every door of the portal's API, from the table the areas register into.
	// The two sets do not overlap: the API's are all under the subtree Hosting
	// rewrites to this service (api.go's apiPrefix) and these are the CLI's own.
	apiRoutes.mount(mux, s)
	return mux
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

// address reads one address, and nothing else: what the portal's routes carry —
// a signed-in account's own address, an applicant's, a buyer's event's — is one
// shape, and this is the one reader of it.
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
// Reading the domain as a name of labels is what the eligibility checks stand
// on: the free-mail refusal a trial application meets (§4.1) and the paid-domain
// refusals compare the domain, so a domain that is not one is a domain they
// compare wrongly. `maintainer@gmail.example(x)` is the case that names it — a
// comment, deliverable by net/mail to gmail.example and by no parse a domain
// equal to gmail.example — which would otherwise walk past them.
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
