// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// §14's Team trial (K2.7, D2136, D2201): the door a signed-in account asks at,
// and the rule that one organisation's installation and one account get one.
//
// The trial is a normal Team key issued without payment, and nothing else about
// it is special: it is minted from the `trial` row of the policy table, so its
// fourteen days, its zero grace and its daily check are the row's and not this
// file's (D2133, D2136, D2138, policy.go), and it is a v1 key like every other
// issue path's, so a revocation can name its `key_id` and the installation reads
// its claims back as it reads any other key's.
//
// What the file owns is §14's own rule. The installation is the workspace's
// **Firebase project id** and the account is the one the sign-in proved (K2.4,
// P1.3) — never one the request names — and the id is counted **with the
// organisation** rather than alone (D2201, and store.go's Trial says why): a
// project id is a caller's own string that proves nothing (F37, TN3), so a rule
// keyed on it alone would let any signed-in account at any domain name another
// organisation's project id, take the trial that organisation was about to ask
// for and bar it for good. Keyed on the pair, that ask spends the asking
// organisation's trial and nothing else — and only a domain the account's own
// address names is counted for it: from an address at a free mail provider no
// organisation is counted at all, and the ask is refused (D453, below). The
// count is the store's atomic check-and-write rather than a read this route
// makes before minting: two asks arriving together are one trial, because only
// one of them can claim it (K2.2, store.go's `ClaimTrial`).
//
// The key leaves by mail, the way every issue path's does: the bundle is the key
// line and the renewal secret line, shown once and never stored (D2135, §7.3),
// so the API's own answer carries no key and no secret — it is the SPA that put
// the ask, and the account that holds the trial gets its key at its own address.

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

// RouteTrial is the trial's door on the portal's API (P1.0): the SPA's own
// origin reaches it through Hosting's rewrite (F21), which is why it is
// registered under the subtree rather than mounted by Handler.
const RouteTrial = "/api/trial"

// maxTrialBytes is what a trial ask may body. It carries a project id and a seat
// count, and the cap is the mount's own rule read on this route rather than a
// size these two fields need (api.go): a door a stranger can post to is one
// whose body the service has to hold.
const maxTrialBytes = 4 << 10

func init() {
	// The route reaches the sign-in verifier and says so, so that a deployment
	// serving the trial without one is refused when it starts rather than
	// answering every ask with a failure of its own (api.go's `reaching`).
	registerAPI(http.MethodPost, RouteTrial, (*Service).trial,
		withBody(maxTrialBytes), reaching(needSignIn))
}

// TrialCall is what a trial ask carries: the installation's Firebase project id,
// and the seats the trial is asked for.
//
// It carries no term, no tier and no policy, because those are the row's
// (D2133): a caller that could name them could mint a trial that never checks at
// all. The seats are the one number that is the caller's — the row names none,
// and how many people an organisation tries the tier with is its own decision
// (K2.1's policy table).
//
// The body is read through decodeWire like the portal's other requests, strictly
// (P1.1). The shape has no entry in the wire registry (wire.go) yet: a
// registered shape is a JSON Schema, a fixture and the SPA's own types beside
// it, and those are the portal's wire area's files rather than this task's. What
// that leaves untested is the SPA's agreement with this shape, not this side's
// reading of it.
type TrialCall struct {
	WorkspaceID string `json:"workspace_id"`
	Seats       int    `json:"seats"`
}

// trialPrefix opens the source a trial's issuance is written under, the way
// `rotation:` opens a rotation's (renew.go): it is the record of what the trial
// minted, and it names the installation the trial was granted to rather than the
// key, because what §14 counts is the installation and not the key
// (`trialSource`).
const trialPrefix = "trial:"

// trialSource names the record one trial writes, under the pair the rule counts
// (`trialPair`).
func trialSource(organisation, installation string) string {
	return trialPrefix + trialPair(organisation, installation)
}

// The refusals this route answers with. Each is a code a client branches on and
// a sentence a person reads, and no sentence says anything about the service's
// inside (§7.2's rule, api.go's refusal shape).
const (
	trialRateRefusal     = "Too many requests from this address just now. Try again shortly."
	trialSignInRefusal   = "This request's sign-in could not be verified, so no trial was issued for it. Sign in to the licence project and ask again."
	trialBodyRefusal     = "This request is not the ask this route reads: it carries the workspace's Firebase project id and the seats the trial is asked for, and nothing else."
	trialClaimedRefusal  = "This account or this workspace has already had its trial, and §14's trial is one per installation and account."
	trialWorkspaceEmpty  = "A trial names the workspace it is for, and this request names none."
	trialFreeMailRefusal = "A trial is granted to an organisation, and this account's address is at a free mail provider, where anyone can hold an address and the domain is not one organisation's. Ask again with an address at your organisation's own domain."
	trialSeatsRefusal    = "A trial is asked for a number of seats, and this request asks for none."
)

// trialSeatFloor is the seat count below which an ask is not an ask. It is one:
// a key for nobody is not a licence (minter.go refuses one), and a request that
// named none has left the field out rather than asked for none.
const trialSeatFloor = 1

// trial is §14's trial route: a signed-in account asks for a trial for one
// installation and is mailed its key.
//
// The order is the address's allowance, then the body, then the sign-in, then
// the account's domain's allowance, then the cheap half of §14's rule, then the
// mint, then the claim, and then the mail and the two records. The address's
// allowance is spent first for the reason renew.go's is (a body this service
// never parses is as cheap to serve as a good one), the sign-in is read before
// the domain's allowance and before the rule because the account is half of what
// the rule counts, and the rule is read before the mint because minting for an
// ask that is going to be refused spends the key ring's authority on a key
// nobody is given.
//
// The mint stands before the claim and not after it, and that order costs
// something either way: the claim is the store's check-and-write and it needs
// the `key_id` the mint answers, so the two cannot be one act here. Minting
// first means a lost race has a key to withdraw (`withdrawUnrecorded`, §8's
// list) rather than a second trial nobody holds; claiming first would mean an
// ask whose mail failed had burnt the installation's one trial, and a mail
// failure is rarer than a person's second click. What is left of the price is
// that a mail failure after the claim leaves the trial claimed and its key
// unmailed: the store holds a trial naming a key, an operator sees it, and the
// account is answered a failure rather than a key it never received.
//
// A claim that comes back with an error is read against the key before anything
// is done with it: the store's answer alone cannot say whether the claim landed,
// because the deployed store re-runs a transaction whose result was lost and
// then reads its own committed documents (`trialLanded`, firestore_store.go).
func (s *Service) trial(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("ip:" + clientIP(r)) {
		refuseAPI(w, http.StatusTooManyRequests, "rate_limited", trialRateRefusal)
		return
	}
	call, ok := s.trialCall(w, r)
	if !ok {
		return
	}
	caller, ok := s.trialCaller(w, r)
	if !ok {
		return
	}
	// The organisation is read off the sign-in once and is the one value §14's
	// rule, the throttle and the key all use: what the store counts a trial
	// against, what the allowance is spent on and what the key says the trial
	// belongs to are one thing, and a second reading of it would be a second
	// rule (D2201).
	organisation := domainOf(caller.Email)
	// A free mail domain is refused here, which is D453's list read the way the
	// free seat reads it (service.go's `freeSeatForm`: such a domain goes to the
	// owner's queue rather than being minted for). What it protects is D2201's
	// rule itself: an organisation that anybody can hold an address at is not an
	// organisation, so a trial counted against one counts nothing — every account
	// at gmail.example shares it, and the first of them to name a project id
	// would spend every other one's trial for that id and bar the organisation
	// that owns the project (409, and no method of the Store interface repairs a
	// trial). The free seat's queue is not what this route does with it: a queued
	// application is minted a free seat by the owner's key and has no path to a
	// trial, and this is a door a signed-in stranger posts to, not a place a
	// person reads a request. A trial is asked for with an address at the
	// organisation's own domain.
	if s.freeMail(organisation) {
		refuseAPI(w, http.StatusForbidden, "free_mail_organisation", trialFreeMailRefusal)
		return
	}
	if !s.trialFree(w, r, call, caller.Email, organisation) {
		return
	}
	// The account's own **domain's** allowance is spent here, and it is the
	// throttle §14's trial is documented with: "the trial path is throttled per
	// domain" (docs/licensing/licence-key.md, "Limits, accepted"), because the
	// account a caller asks from is one they may open another of and the
	// organisation behind it is not. It is spent after the sign-in and not with
	// the address, for the reason a renewal's key allowance is (renew.go's
	// `charged`): only a request that proved who asks is counted against that
	// domain, so no caller can spend another domain's allowance. And it is spent
	// after §14's rule rather than before it, because an ask the rule is going to
	// refuse mints nothing and must not spend the domain's allowance either: a
	// domain is shared — a free mail domain is the case D453's list exists for —
	// so an account that already had its trial and keeps asking would otherwise
	// hold that domain's allowance at nothing and lock every other account on it
	// out of a trial it never took.
	if !s.limiter.allow("domain:" + organisation) {
		refuseAPI(w, http.StatusTooManyRequests, "rate_limited", trialRateRefusal)
		return
	}
	// The v1 route and not the older format's, for the reason the paid path
	// states (merchant.go's `paid`): what an installation is issued is a v1 key,
	// and a service that could not answer a v1 request is refused rather than
	// handed a key no released verifier reads (Check 174).
	mints, answersV1 := s.mints.(MintsV1)
	if !answersV1 {
		s.trialFailed(w, errors.New("the intake's minter does not answer a v1 mint request, and a trial is a v1 key"))
		return
	}
	answer, err := mints.MintV1(r.Context(), RequestV1{
		Environment: s.cfg.Environment,
		Policy:      PolicyTrial,
		WorkspaceID: call.WorkspaceID,
		// The account's own domain is the organisation the key names, which is
		// the same reading of "one per organisation" the free seat's check uses
		// (service.go's `domainOf`, §4.2). A trial's organisation is otherwise
		// the portal's model of one (P3.3), which does not exist yet: until it
		// does, the address the sign-in proved is the only organisation this
		// route can name, and the key has to name one to verify at all. It is
		// the same value the claim below is keyed on, and not a second reading
		// of it: what the key says its organisation is and what the rule counted
		// are one thing (D2201).
		Organisation: organisation,
		Seats:        call.Seats,
		// No term and no end: the row runs for its own fourteen days and the
		// minter refuses a term on a row nothing renews (D2136, minter.go).
	})
	if err != nil {
		s.trialFailed(w, err)
		return
	}
	if err := s.store.ClaimTrial(r.Context(), Trial{
		Organisation: organisation,
		Installation: call.WorkspaceID,
		Account:      caller.Email,
		KeyID:        answer.Record.KeyID,
		At:           s.now(),
	}); err != nil {
		// An error is not the same as a claim that did not land. The deployed
		// store is a transaction that is re-run when its result is lost, and the
		// re-run reads the documents its own first attempt committed
		// (firestore_store.go's `ClaimTrial`), so it answers "already claimed"
		// about **this** ask — one that did land, whose trial names the key this
		// mint answered. The key is what tells the two apart, and it is the only
		// thing that can: nothing else in the store says which ask a trial holds.
		landed, readErr := s.trialLanded(r.Context(), answer.Record.KeyID)
		if readErr != nil {
			// Nothing can say whether the claim landed, and this is the one case
			// where withdrawing is the worse of the two harms: a trial that did
			// land is this ask's own, and it leaves the account and the
			// installation barred with no way back (no method of the Store
			// interface repairs one). The key is left where the mint put it —
			// nobody holds it, because the mail below never went out — and the
			// ask is answered a failure: the customer asks again, and the ask
			// that finds the trial claimed is one an operator reads the trial's
			// key off (§8's list, K2.2).
			s.trialFailed(w, errors.Join(err, readErr))
			return
		}
		if !landed {
			// The store answered, and no trial names this key: the claim did not
			// land — the lost race (`trialFree` cannot see it) or a store that
			// could not be written — so the key that was minted for it is
			// recorded nowhere, the mail below never went out and nothing will
			// write its issuance. It is withdrawn rather than left live: a key
			// nobody holds is what §8's list is where the licensor can see it.
			if withdrawErr := s.withdrawUnrecorded(r.Context(), answer.Record.KeyID); withdrawErr != nil {
				s.trialFailed(w, errors.Join(err, withdrawErr))
				return
			}
			if !errors.Is(err, ErrTrialAlreadyClaimed) {
				s.trialFailed(w, err)
				return
			}
			refuseAPI(w, http.StatusConflict, "trial_already_claimed", trialClaimedRefusal)
			return
		}
		// The ask's own claim landed and its answer was lost: the trial is this
		// ask's, the account never received its key, and serving it below is the
		// whole of what is left to do.
	}
	answer.Record.Source = trialSource(organisation, call.WorkspaceID)
	answer.Record.Email = caller.Email
	if err := s.mail.Send(r.Context(), paidKeyMail(caller.Email, answer.Key, answer.RenewalSecret)); err != nil {
		s.trialFailed(w, err)
		return
	}
	if err := s.store.SaveIssuance(r.Context(), answer.Record); err != nil {
		// Unlike the paid path this one withdraws nothing here: the key was
		// minted for a trial the store has already claimed and handed to the
		// account, and a trial renews at no row's hand (its row's renewal mode
		// is none), so a record that did not land costs the licensor the
		// bookkeeping and costs the customer nothing it can still use.
		s.trialFailed(w, err)
		return
	}
	if err := s.store.SaveKeyState(r.Context(), KeyState{
		KeyID:             answer.Record.KeyID,
		WorkspaceID:       call.WorkspaceID,
		RenewalSecretHash: digest(answer.RenewalSecret),
	}); err != nil {
		// The pair's safe half, as on every issue path: the key is one the store
		// holds a trial and an issuance for, so a revocation can name it and the
		// trial is not lost with this write.
		s.trialFailed(w, err)
		return
	}
	// The answer carries no key and no secret (D2135): the bundle went to the
	// account's own address, and an answer the SPA holds is not a place a
	// credential may be shown.
	w.WriteHeader(http.StatusOK)
}

// trialLanded answers whether the store holds the trial this ask claimed, which
// is what tells a claim whose answer was lost from one that did not land: a key
// is minted for one ask and minted here, so a trial naming this key is this
// ask's claim and cannot be another's. The read is a read of the whole list
// because the store's interface holds no lookup by key (`ClaimTrial` is the one
// trial method that is not a list), and the list is small — one document per
// installation that ever had a trial.
func (s *Service) trialLanded(ctx context.Context, keyID string) (bool, error) {
	trials, err := s.store.Trials(ctx)
	if err != nil {
		return false, err
	}
	for _, held := range trials {
		if held.KeyID == keyID {
			return true, nil
		}
	}
	return false, nil
}

// trialCall reads the ask's body: the route's own JSON, strictly, with the two
// fields it names present and the workspace and the seat count checked for being
// there at all. What the values say is not this route's to judge beyond that —
// a project id that is not one mints a key that verifies and binds to nothing
// anybody owns, which is the asker's own loss (wire.go's split, `decodeWire`).
func (s *Service) trialCall(w http.ResponseWriter, r *http.Request) (TrialCall, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", "This request's body could not be read.")
		return TrialCall{}, false
	}
	var call TrialCall
	if err := decodeWire(body, &call); err != nil {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", trialBodyRefusal)
		return TrialCall{}, false
	}
	// The id is read as it arrived and never trimmed, the way a renewal reads a
	// key's own id: what the key binds to and what the trial counts is the value
	// the caller sent, and a service that repaired a misspelling would count
	// something nobody asked for.
	if strings.TrimSpace(call.WorkspaceID) == "" {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", trialWorkspaceEmpty)
		return TrialCall{}, false
	}
	if call.Seats < trialSeatFloor {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", trialSeatsRefusal)
		return TrialCall{}, false
	}
	return call, true
}

// trialCaller verifies the ask's sign-in and answers the account it names, which
// is half of what §14's rule counts.
//
// The token rides in the request's `Authorization` header as a bearer token,
// which is where the SPA's own calls carry the ID token it holds (P1.3): what is
// verified is P1.3's business and not this route's, and an ask whose token the
// verifier refuses is answered the same way as one that carried none — the
// account is what a trial is granted to, and there is no trial without one.
//
// The seam is not checked for being there: `reaching(needSignIn)` makes New
// refuse a deployment that mounts this route and holds none (api.go), so a nil
// verifier here is a route no service ever serves.
func (s *Service) trialCaller(w http.ResponseWriter, r *http.Request) (Caller, bool) {
	token, carried := bearerToken(r)
	if !carried {
		refuseAPI(w, http.StatusUnauthorized, "unverified_sign_in", trialSignInRefusal)
		return Caller{}, false
	}
	caller, err := s.signIn.Verify(r.Context(), token)
	if err != nil {
		refuseAPI(w, http.StatusUnauthorized, "unverified_sign_in", trialSignInRefusal)
		return Caller{}, false
	}
	// The address is read the way every other part of this service reads one
	// (§4.2's `address`, which the forms validate through), and what it answers is
	// what the trial is counted against. A verifier that proved no address, or one
	// it proved that is not an address, has not done the half of its job §14 needs
	// and is answered as a sign-in that failed: the account is what a trial is
	// granted to, and there is no trial without one. Reading it rather than
	// trusting the spelling matters twice over — the rule counts one account and
	// one organisation, and an address in another case is the same address — and
	// the domain it answers is the one D453's list is compared against, which a
	// spelling of its own would walk past.
	email, readable := address(caller.Email)
	if !readable {
		refuseAPI(w, http.StatusUnauthorized, "unverified_sign_in", trialSignInRefusal)
		return Caller{}, false
	}
	caller.Email = email
	return caller, true
}

// trialFree answers whether the store holds no trial for this organisation's
// installation or for this account, which is the cheap half of §14's rule: it
// reads the list and refuses an ask that is certainly spent before a key is
// minted for it. It is not the rule itself — two asks arriving together both
// pass it — and the store's `ClaimTrial` is what decides between them.
//
// The installation half is the pair and not the project id, which is the whole
// of D2201: the id is a caller's own string and proves nothing (F37, TN3), so a
// rule that counted it alone would hand any signed-in account at any domain the
// power to take another organisation's trial by naming a project id it read
// somewhere, and to bar that organisation for good. Counted as a pair, the same
// ask spends only the naming organisation's trial.
func (s *Service) trialFree(w http.ResponseWriter, r *http.Request, call TrialCall, account, organisation string) bool {
	trials, err := s.store.Trials(r.Context())
	if err != nil {
		s.trialFailed(w, err)
		return false
	}
	for _, held := range trials {
		claimed := held.Organisation == organisation && held.Installation == call.WorkspaceID
		if claimed || held.Account == account {
			refuseAPI(w, http.StatusConflict, "trial_already_claimed", trialClaimedRefusal)
			return false
		}
	}
	return true
}

// trialFailed answers a failure of this route's own making: the API's one
// failure shape, with the cause written to the process's log, which is where it
// is read from (P1.6 reports the answer itself, and an answer's body is never a
// service's inside, §7.2).
func (s *Service) trialFailed(w http.ResponseWriter, err error) {
	log.Printf("keyservice: the trial route could not serve this request: %v", err)
	refuseAPI(w, http.StatusInternalServerError, apiFailedCode, apiFailedMessage)
}

// bearerToken reads the request's own bearer token: the scheme and the token,
// and the scheme is compared without regard to case because RFC 7235 says the
// two are one (a scheme is case-insensitive; a token is not, and it is handed on
// exactly as it arrived).
//
// Anything else in the header is no token at all rather than a token to salvage:
// a caller that put a key or a secret there has sent a credential to a route
// that reads one field, and what this route does with it is nothing.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	return token, true
}
