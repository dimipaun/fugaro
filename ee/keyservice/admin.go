// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The admin page (§6): the second authenticated surface, and the only one that
// can mint **any** key for **any** tier (T13 item 8), which is why every defence
// the design names for it is here rather than in a template's business:
//
//   - authentication — the project's owner account and no other (§6, D455);
//   - step-up — a fresh second factor per minting action, which is **D455** and
//     is a seam: with none the page renders read-only and every mint is refused;
//   - CSRF — a token the page renders and this file signs;
//   - escaping — the page is html/template's, so an applicant's text is a text
//     node in it, and the page carries no script at all (Check 173 step 4);
//   - audit — one line per mint, naming the tier, the seats, the plan and the
//     actor, written before the key is returned (**D456**'s destination);
//   - limits — D454's per-tier cap, read over the issuance record;
//   - no key on the page — a key is mailed to the address the mint names and
//     never stored or rendered (§7.3).
//
// The page mints through the two doors every other path does — the minter's own
// service for the key and the store for the record — and it holds no signer
// (Check 174).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The page's own routes. They are routes of the same handler as the intake's
// (§3.1): one program's parts, and what separates this surface from the site's
// forms is authentication rather than an address.
const (
	RouteAdmin       = "/admin"
	RouteAdminMint   = "/admin/mint"
	RouteAdminDecide = "/admin/decide"
)

// The two actions a page token is bound to. A token is good for one of them and
// for one account, so a form the page rendered to decline cannot be replayed to
// mint.
const (
	actionMint   = "mint"
	actionDecide = "decide"
)

// minAdminSecret is the shortest secret the page's tokens may be signed with.
// It is deliberately low — what a secret should be is the deploy's — and it is
// here at all because a two-byte secret is one a token can be forged against,
// and a forgery is a mint.
const minAdminSecret = 16

// The page's refusals, as the sentences the owner reads. They are the only place
// this file describes what it refused and why: the caller is the owner, so
// there is no one to be uniform for (§7.1's rule is about applicants), and the
// sentence is what tells a person which defence answered.
const (
	refuseCrossSite = "That form did not come from this page. Open the page and submit it again."
	refuseNoFactor  = "This deployment has no second factor configured, so the page can mint nothing. D455 is the owner's to answer."
	refuseNoStepUp  = "That action was not confirmed with your second factor."
	refuseFields    = "That form is missing something a mint needs: a plan, a seat count, a term and an address the key can be sent to."
	refusePlan      = "That plan is not one this page mints."
	refuseDeclined  = "That request has already been declined."
	refuseApproved  = "That request has already been approved."
	refuseUnknown   = "No request in the queue has that id."
	refuseOverCap   = "This tier's mint limit for the window is reached. The request stays in the queue."
)

// Owner answers who a request is signed in as (§6). The project's own identity
// provider is behind it, and this package compares its answer to the one account
// the deploy names: the page is the Licensor's, and there is one Licensor
// (D455).
type Owner interface {
	SignedIn(ctx context.Context) (account string, ok bool)
}

// StepUp answers whether a code is a fresh second factor for one account.
// **D455 is open** — a hardware key or TOTP, and whether a second person may
// ever hold the page — so it is a seam, and a service built with none mints
// nothing from the page and renders it read-only rather than trusting a session
// alone, which is the row the design is about.
//
// It is per action and not per session: the code the form carried is the whole
// of what this is asked, so a stolen session has nothing to submit.
type StepUp interface {
	Verified(ctx context.Context, account, code string) (bool, error)
}

// Auditor writes the page's audit line (§6, **D456**). The line itself is not
// optional — one is written before every mint returns a key — and what is open
// is where it goes, so a service with no Auditor writes it to the process's own
// log, which is D456(a)'s sink as far as this package can express one.
type Auditor interface {
	Mint(ctx context.Context, line MintLine) error
}

// MintLine is one key the page minted, as the audit line names it (§6): the
// tier, the seats, the plan and the actor, and what the mint was for. It carries
// no key material (§7.3).
type MintLine struct {
	At           time.Time
	Actor        string
	Tier         string
	Seats        int
	Plan         string
	Organisation string
	Email        string
	Source       string
	Application  string
}

// String is the line as the log carries it: one line, so a log a person reads is
// a list of keys rather than a paragraph per key.
func (l MintLine) String() string {
	return fmt.Sprintf("mint %s tier=%s seats=%d plan=%s actor=%s organisation=%s email=%s source=%s application=%s",
		l.At.UTC().Format(time.RFC3339), l.Tier, l.Seats, l.Plan, l.Actor, l.Organisation, l.Email, l.Source, l.Application)
}

// The two sources a mint from the page writes, and the whole of what "the page's
// own mints" means when a cap is counted. §5.2 makes a source the record's
// identity and its naming is already a convention (`renew:<id>:<expires>`), so
// the cap reads that and nothing else: adding a field to the record for a cap's
// benefit would put a second meaning on the issuance shape.
const (
	sourceApproval = "approval:"
	sourceOrder    = "order:"
)

// pageMint answers whether a record's source is one the page wrote.
func pageMint(source string) bool {
	return strings.HasPrefix(source, sourceApproval) || strings.HasPrefix(source, sourceOrder)
}

// adminCSP is what the page may load, and it is none of it. The page can mint
// any key for any tier, so it fetches nothing, submits nowhere but itself, and
// cannot be framed — which is also what makes "the page carries no script" a
// property of the deployment rather than of the markup.
const adminCSP = "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// adminPage renders the queue, the mint forms and the record.
func (s *Service) adminPage(w http.ResponseWriter, r *http.Request) {
	account, ok := s.owner(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	open, err := s.queue(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	records, err := s.store.Issuances(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	mintToken, err := s.pageToken(account, actionMint)
	if err != nil {
		fail(w, err)
		return
	}
	decideToken, err := s.pageToken(account, actionDecide)
	if err != nil {
		fail(w, err)
		return
	}

	view := adminView{
		Owner:       account,
		Now:         s.now(),
		MintToken:   mintToken,
		DecideToken: decideToken,
		PlanNames:   s.planNames(),
		DefaultPlan: s.defaultPlanName(),
		Records:     newestFirst(records, maxAdminRecords),
	}
	if s.stepUp == nil {
		view.Refusal = refuseNoFactor
	}
	for _, a := range open {
		view.Queue = append(view.Queue, s.rowFor(a))
	}
	for _, tier := range sortedTiers() {
		used, err := s.mintedIn(ctx, tier)
		if err != nil {
			fail(w, err)
			return
		}
		view.Caps = append(view.Caps, capRow{Tier: tier, Used: used, Limit: s.cfg.MintCaps[tier]})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", adminCSP)
	// The queue is a list of people and the page is the surface that mints:
	// a shared cache is not a place either belongs.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := adminTemplate.Execute(w, view); err != nil {
		// The header is out by now, so the page is what it is; what a reader
		// must not be left with is a half-rendered queue that looks complete.
		log.Printf("keyservice: rendering the admin page: %v", err)
	}
}

// adminMint is the page's one minting action (§6): an approval, when the form
// names the request it answers, or §5.5's Order-Form mint, when it does not.
// They are one path because they are one thing — the owner granting a licence
// from the page — and everything §6's table names has to hold for both.
func (s *Service) adminMint(w http.ResponseWriter, r *http.Request) {
	account, ok := s.owner(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	form, ok := s.adminForm(w, r, account, actionMint)
	if !ok {
		return
	}
	if s.stepUp == nil {
		adminRefuse(w, http.StatusForbidden, refuseNoFactor)
		return
	}
	code := strings.TrimSpace(form.Get("step_up"))
	if code == "" {
		adminRefuse(w, http.StatusForbidden, refuseNoStepUp)
		return
	}
	verified, err := s.stepUp.Verified(ctx, account, code)
	if err != nil {
		fail(w, err)
		return
	}
	if !verified {
		adminRefuse(w, http.StatusForbidden, refuseNoStepUp)
		return
	}

	plan, known := s.pagePlan(strings.TrimSpace(form.Get("plan")))
	if !known {
		adminRefuse(w, http.StatusBadRequest, refusePlan)
		return
	}
	seats, seatsOK := count(form.Get("seats"))
	if !seatsOK {
		seats = plan.Seats
	}
	term, termOK := count(form.Get("term_days"))
	if !termOK {
		term = plan.TermDays
	}
	if seats < 1 || term < 1 {
		adminRefuse(w, http.StatusBadRequest, refuseFields)
		return
	}

	var queued Application
	if id := strings.TrimSpace(form.Get("application")); id != "" {
		held, found, err := s.application(ctx, id)
		if err != nil {
			fail(w, err)
			return
		}
		if !found {
			adminRefuse(w, http.StatusNotFound, refuseUnknown)
			return
		}
		if held.Decision == Declined {
			adminRefuse(w, http.StatusConflict, refuseDeclined)
			return
		}
		queued = held
	}

	organisation := strings.TrimSpace(form.Get("organisation"))
	if organisation == "" {
		organisation = queued.Organisation
	}
	email := strings.TrimSpace(form.Get("email"))
	if email == "" {
		email = queued.Email
	}
	to, valid := address(email)
	if !valid || strings.TrimSpace(organisation) == "" {
		adminRefuse(w, http.StatusBadRequest, refuseFields)
		return
	}

	// §5.2's idempotency, read before anything is minted or written down. It has
	// to be read first because minting mails the key before it records it: a
	// second click that reached the minter would sign and deliver a second key
	// and then write nothing, which is a key nobody is told about and no line in
	// the log either (§4.5, TestApprovalMintsOnce).
	//
	// An approval keys on the request it answers, which is already a record the
	// deployment holds. §5.5's Order-Form mint has no such thing — the order is
	// a document nobody here has seen — so it keys on the render the form came
	// from: the form in front of the owner submits once and a second submission
	// of it is the click that already minted, while opening the page again is a
	// licence of its own. An order form and a seat count alone would make a
	// re-issue impossible and answer it as a success, which is the one thing a
	// page that cannot tell the two apart must not do
	// (TestAnOrderFormMintCanBeIssuedAgain).
	source := sourceApproval + queued.ID
	if queued.ID == "" {
		source = sourceOrder + organisation + ":" + strconv.Itoa(seats) + ":" + pageNonce(form.Get("token"))
	}
	if _, held, err := s.recorded(ctx, source); err != nil {
		fail(w, err)
		return
	} else if held {
		// The mint this click repeats is in the record; what can still be
		// missing is the answer, when the write after that mint failed. This is
		// where it is written, because the queue is where the owner finds the
		// request: a click that only redirected would leave it open for good,
		// with a key issued for it and no action left that could answer it
		// (§5.2's order, one step out; TestAFailedDecisionIsWrittenByTheNextClick).
		if queued.ID != "" {
			if err := s.decide(ctx, queued, Approved); err != nil {
				fail(w, err)
				return
			}
		}
		seeOther(w, r)
		return
	}

	within, err := s.withinCap(ctx, plan.Tier)
	if err != nil {
		fail(w, err)
		return
	}
	if !within {
		adminRefuse(w, http.StatusTooManyRequests, refuseOverCap)
		return
	}

	line := MintLine{
		At: s.now(), Actor: account, Tier: plan.Tier, Seats: seats, Plan: plan.Name,
		Organisation: organisation, Email: to, Source: source, Application: queued.ID,
	}
	if err := s.audit(ctx, line); err != nil {
		fail(w, err)
		return
	}

	if _, err := s.mint(ctx, s.mintRequest(plan, organisation, seats, term), to, source); err != nil {
		fail(w, err)
		return
	}
	if queued.ID != "" {
		if err := s.decide(ctx, queued, Approved); err != nil {
			// The key is out and the record is written (§5.2's order): what a
			// failed write here costs is that the request stays in the queue,
			// where the owner's next click finds the record, mints nothing and
			// writes the answer this one could not
			// (TestAFailedDecisionIsWrittenByTheNextClick).
			log.Printf("keyservice: recording the decision on the request %s: %v", queued.ID, err)
		}
	}
	seeOther(w, r)
}

// adminDecide is a decline (§4.5): the owner has looked at the request and will
// not grant it. Nothing is minted, so there is no step-up — §6's second factor
// is per **minting** action — and no audit line, because the log is a list of
// keys and this is not one.
func (s *Service) adminDecide(w http.ResponseWriter, r *http.Request) {
	account, ok := s.owner(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	form, ok := s.adminForm(w, r, account, actionDecide)
	if !ok {
		return
	}
	id := strings.TrimSpace(form.Get("application"))
	held, found, err := s.application(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	if !found {
		adminRefuse(w, http.StatusNotFound, refuseUnknown)
		return
	}
	if held.Decision == Approved {
		adminRefuse(w, http.StatusConflict, refuseApproved)
		return
	}
	// The flag can lag the mint: the write that answers a request comes after
	// the key is out, and a store that could not take it leaves the request open
	// with a key already issued for it. What says a request has been granted is
	// the record rather than the flag (§5.2's order), so the decline reads it
	// too — a request the owner has already licensed is the one the queue must
	// not take a "no" for (TestAFailedDecisionIsWrittenByTheNextClick).
	if _, minted, err := s.recorded(ctx, sourceApproval+held.ID); err != nil {
		fail(w, err)
		return
	} else if minted {
		adminRefuse(w, http.StatusConflict, refuseApproved)
		return
	}
	if err := s.decide(ctx, held, Declined); err != nil {
		fail(w, err)
		return
	}
	seeOther(w, r)
}

// owner is §6's first row: the page and both its actions are the owner
// account's, and every other answer — no session, another account, a session
// that has been revoked — is the answer a path that is not here at all gets.
// The page's existence, its queue and the fact that this deployment has one are
// not a stranger's business, and "not found" is what says none of them.
func (s *Service) owner(w http.ResponseWriter, r *http.Request) (string, bool) {
	account, signedIn := s.signedIn.SignedIn(r.Context())
	if !signedIn || account != s.cfg.AdminAccount {
		http.NotFound(w, r)
		return "", false
	}
	return account, true
}

// adminForm reads one of the page's form submissions and checks the token the
// page rendered with it (§6's CSRF row). The body bound is the same one the
// site's forms get: the page is the owner's, and it is still a request on the
// internet.
func (s *Service) adminForm(w http.ResponseWriter, r *http.Request, account, action string) (form url.Values, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		adminRefuse(w, http.StatusBadRequest, refuseFields)
		return nil, false
	}
	if !s.pageTokenValid(account, action, r.PostFormValue("token")) {
		adminRefuse(w, http.StatusForbidden, refuseCrossSite)
		return nil, false
	}
	return r.PostForm, true
}

// pageToken is the page's own state, signed rather than remembered: it names the
// account and the action it is good for, it expires, and it carries 256 bits of
// randomness so two renders of one page are two different tokens.
//
// Stateless on purpose. The intake runs on more than one instance (§3.4), so a
// page that held its state in memory would hand the owner a form that the next
// request — a different instance's — refuses at random; what makes that
// unnecessary is that the whole of the state is in the token and the whole of
// what authenticates it is the deploy's own secret.
func (s *Service) pageToken(account, action string) (string, error) {
	nonce, err := newSecret()
	if err != nil {
		return "", err
	}
	expires := s.now().Add(s.cfg.AdminTokenTTL).Unix()
	return nonce + "." + strconv.FormatInt(expires, 10) + "." + s.pageMAC(account, action, nonce, expires), nil
}

// pageTokenValid answers whether a token is one this deploy rendered, for this
// account and this action, inside its period.
func (s *Service) pageTokenValid(account, action, token string) bool {
	nonce, rest, found := strings.Cut(token, ".")
	if !found || nonce == "" {
		return false
	}
	stamp, carried, found := strings.Cut(rest, ".")
	if !found {
		return false
	}
	expires, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return false
	}
	if !s.now().Before(time.Unix(expires, 0)) {
		return false
	}
	want := s.pageMAC(account, action, nonce, expires)
	return hmac.Equal([]byte(want), []byte(carried))
}

// pageNonce is the random part of a token the page has already validated: which
// render a submission came from. It is what an Order-Form mint keys its own
// idempotency on, and it is the nonce alone rather than the token because a
// source is rendered on the page and written to the audit line, and the token is
// the credential that lets the form mint at all.
func pageNonce(token string) string {
	nonce, _, _ := strings.Cut(token, ".")
	return nonce
}

// pageMAC is the deployment's signature over one token's four fields. The
// fields are separated by a byte an account, an action and a base64url nonce
// cannot contain, so no two field lists can be read as one another.
func (s *Service) pageMAC(account, action, nonce string, expires int64) string {
	mac := hmac.New(sha256.New, s.cfg.AdminSecret)
	for _, field := range []string{account, action, nonce, strconv.FormatInt(expires, 10)} {
		mac.Write([]byte(field))
		mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// audit writes the page's audit line (§6). It is the mint's own step and it
// happens before the key is asked for, so a key that exists is a key that was
// written down; a failure to write it is a failed mint rather than an
// unrecorded one, which is a mint that wrote no line and signed no key
// (TestAFailedAuditLineFailsTheMint).
func (s *Service) audit(ctx context.Context, line MintLine) error {
	if s.auditor == nil {
		log.Printf("keyservice: %s", line)
		return nil
	}
	return s.auditor.Mint(ctx, line)
}

// withinCap answers whether the page may mint one more key of a tier inside
// D454's window. The count is over the issuance record and over the page's own
// sources alone: a cap that counted the merchant's events or a followed link
// would refuse a paying customer because the owner had approved a trial.
//
// A tier the deploy names no cap for is not capped. New refuses such a
// deployment, and this is what a Service built around it does.
func (s *Service) withinCap(ctx context.Context, tier string) (bool, error) {
	limit, capped := s.cfg.MintCaps[tier]
	if !capped {
		return true, nil
	}
	used, err := s.mintedIn(ctx, tier)
	if err != nil {
		return false, err
	}
	return used < limit.Requests, nil
}

// mintedIn is how many keys the page has minted of one tier inside its window.
func (s *Service) mintedIn(ctx context.Context, tier string) (int, error) {
	limit, capped := s.cfg.MintCaps[tier]
	if !capped {
		return 0, nil
	}
	held, err := s.store.Issuances(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := s.now().Add(-limit.Window)
	used := 0
	for _, r := range held {
		if r.Tier == tier && pageMint(r.Source) && r.Issued.After(cutoff) {
			used++
		}
	}
	return used, nil
}

// pagePlan answers the plan one minting action names. The three the site's forms
// mint from are reachable from the page as well — an approval is most often one
// of them — and PagePlans are the ones only the page reaches (§5.5's Enterprise
// keys), so what this reads is the deploy's own set rather than a name the form
// chose.
func (s *Service) pagePlan(name string) (Plan, bool) {
	if name == "" {
		return Plan{}, false
	}
	if plan, known := s.planFor(name); known {
		return plan, true
	}
	if s.cfg.Paid.Name == name {
		// The merchant's plan is reachable from the page as well: §5.3's seat
		// change and renewal are a person's answer to a customer's mail, and the
		// Order Form of §5.5 mints what that customer already holds.
		return s.cfg.Paid, true
	}
	for _, plan := range s.cfg.PagePlans {
		if plan.Name == name {
			return plan, true
		}
	}
	return Plan{}, false
}

// planNames is every plan the page may mint, in the order it offers them.
func (s *Service) planNames() []string {
	names := []string{s.freeSeat.Name, s.openSource.Name, s.cfg.Paid.Name}
	for _, plan := range s.cfg.PagePlans {
		names = append(names, plan.Name)
	}
	return names
}

// defaultPlanName is the plan the Order-Form form starts on: the last one the
// page has, which is the page's own and not one of the three the site's forms
// reach.
func (s *Service) defaultPlanName() string {
	if len(s.cfg.PagePlans) > 0 {
		return s.cfg.PagePlans[len(s.cfg.PagePlans)-1].Name
	}
	return s.cfg.Paid.Name
}

// queueRow is one queued request as the page renders it: the request, and the
// numbers the mint form starts on — the plan's own when the request named one
// this deploy has, so approving a free seat is one click.
func (s *Service) rowFor(a Application) queueRow {
	row := queueRow{Application: a, Seats: 1, TermDays: 1}
	if plan, known := s.pagePlan(a.Plan); known {
		row.Seats, row.TermDays = plan.Seats, plan.TermDays
	}
	if len(s.cfg.PagePlans) > 0 {
		page := s.cfg.PagePlans[len(s.cfg.PagePlans)-1]
		if row.Seats < 1 {
			row.Seats = page.Seats
		}
		if row.TermDays < 1 {
			row.TermDays = page.TermDays
		}
	}
	return row
}

// adminRefuse is the page's answer to an action it will not take. It is plain
// text and never a redirect: the owner is the only reader, and the sentence is
// what tells a person which defence answered — there is no one to be uniform
// for, because §7.1's uniformity is about applicants.
func adminRefuse(w http.ResponseWriter, status int, why string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	io.WriteString(w, why+"\n")
}

// seeOther sends the owner back to the page after an action that worked. It is
// a redirect rather than a render because the action changed what the page
// shows, and a POST's own answer is one a reload would submit again.
func seeOther(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, RouteAdmin, http.StatusSeeOther)
}

// count reads one numeric field of the page's form.
func count(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, false
	}
	return value, true
}

// sortedTiers is the tiers a key can be issued for, in one order, so the page's
// limits read the same way twice.
func sortedTiers() []string {
	tiers := make([]string, 0, len(tierMaxTerm))
	for tier := range tierMaxTerm {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	return tiers
}

// newestFirst is the record as a page reads it: the last few keys, newest first,
// so the one the owner just minted is the one at the top.
func newestFirst(records []Record, most int) []Record {
	out := append([]Record(nil), records...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Issued.After(out[j].Issued) })
	if len(out) > most {
		out = out[:most]
	}
	return out
}

// maxAdminRecords is how much of the record the page shows. It is a bound on a
// page rather than on the record: the owner's question is "did that mint land",
// and answering it does not need the licensor's whole history in one response.
const maxAdminRecords = 20

// queueRow and the rest of the page's shape. The types are the template's
// contract and nothing else reads them.
type (
	queueRow struct {
		Application
		Seats    int
		TermDays int
	}
	capRow struct {
		Tier  string
		Used  int
		Limit Limits
	}
	adminView struct {
		Owner       string
		Now         time.Time
		Refusal     string
		Queue       []queueRow
		Records     []Record
		Caps        []capRow
		PlanNames   []string
		DefaultPlan string
		MintToken   string
		DecideToken string
	}
)

// adminTemplate is the page. It holds no script and loads nothing: what it may
// do is fetch nothing at all (adminCSP), and what it says it may do is what the
// deployment makes true.
//
// The applicant's text is a text node and html/template is what escapes it
// (§6's escaping row, Check 173 step 4). That is the whole reason the page is
// rendered by a template engine rather than by a builder: the surface that can
// mint **any** key for **any** tier is the last place to hand-roll an escape.
var adminTemplate = template.Must(template.New("admin").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Fugaro licence keys</title>
</head>
<body>
<h1>Fugaro licence keys</h1>
<p>Signed in as {{.Owner}}. This page mints the Licensor's keys: every key it grants is a licence.</p>
{{if .Refusal}}<p><strong>{{.Refusal}}</strong></p>{{end}}

<h2>The queue</h2>
{{if not .Queue}}<p>Nothing is waiting.</p>{{end}}
{{range .Queue}}
<section>
<h3>Request from {{.Email}}</h3>
<p>Received {{.At}}.{{if .Plan}} Asked for {{.Plan}}.{{end}}{{if .Domain}} Domain {{.Domain}}.{{end}}</p>
{{if .Repositories}}<p>Repositories: {{range .Repositories}}{{.}} {{end}}</p>{{end}}
<pre>{{.Detail}}</pre>
{{if not $.Refusal}}
<form method="post" action="/admin/mint">
<input type="hidden" name="token" value="{{$.MintToken}}">
<input type="hidden" name="application" value="{{.ID}}">
<p>Plan <input name="plan" value="{{.Plan}}"> ({{range $.PlanNames}}{{.}} {{end}})</p>
<p>Seats <input name="seats" value="{{.Seats}}"> Term days <input name="term_days" value="{{.TermDays}}"></p>
<p>Organisation <input name="organisation" value="{{.Organisation}}"> Address <input name="email" value="{{.Email}}"></p>
<p>Second factor <input name="step_up" autocomplete="one-time-code"></p>
<button type="submit">Mint this key</button>
</form>
{{end}}
<form method="post" action="/admin/decide">
<input type="hidden" name="token" value="{{$.DecideToken}}">
<input type="hidden" name="application" value="{{.ID}}">
<button type="submit">Decline</button>
</form>
</section>
{{end}}

<h2>Mint from an Order Form</h2>
{{if .Refusal}}<p>{{.Refusal}}</p>{{else}}
<form method="post" action="/admin/mint">
<input type="hidden" name="token" value="{{.MintToken}}">
<p>Plan <input name="plan" value="{{.DefaultPlan}}"> ({{range .PlanNames}}{{.}} {{end}})</p>
<p>Seats <input name="seats" value="1"> Term days <input name="term_days" value="365"></p>
<p>Organisation <input name="organisation"> Address <input name="email"></p>
<p>Second factor <input name="step_up" autocomplete="one-time-code"></p>
<button type="submit">Mint</button>
</form>
{{end}}

<h2>Limits</h2>
{{range .Caps}}<p>{{.Tier}}: {{.Used}} of {{.Limit.Requests}} in {{.Limit.Window}}.</p>{{end}}

<h2>Issued</h2>
{{if not .Records}}<p>Nothing has been issued yet.</p>{{end}}
{{range .Records}}<p>{{.Issued}} {{.Plan}} ({{.Tier}}, {{.Seats}} seat(s)) {{.Organisation}} {{.Email}} — {{.KeyID}}, {{.Source}}</p>{{end}}
</body>
</html>
`))
