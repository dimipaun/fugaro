// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// §14's Team trial, in the portal (K2.7, portal plan P3.3, D2136, D2185,
// D2201): the door a signed-in account asks at, and the rule that one
// organisation's installation and one account get one.
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
// organisation** rather than alone (D2201): a project id is a caller's own
// string that proves nothing (F37, TN3), so a rule keyed on it alone would let
// any signed-in account at any domain name another organisation's project id,
// take the trial that organisation was about to ask for and bar it for good.
// Keyed on the pair, that ask spends the asking organisation's trial and nothing
// else. The organisation is the portal's own (P2.1) and it is the one the caller
// is a member of — an id nothing a caller types can reach, which is what makes
// the pair's first half as unguessable as its second (D2185). The count is the
// store's atomic check-and-write rather than a read this route makes before
// minting: two asks arriving together are one trial, because only one of them
// can claim it (K2.2, store.go's `ClaimTrial`).
//
// A trial is a licence, and the portal holds it as one (P3.1): the key that
// leaves here is the key of a workspace the trial writes for the project the ask
// named, and the organisation's own list of entitlements names it. That is why
// this route writes two records after the store has claimed the trial and before
// it mails anything: a key in an account's inbox is a key the portal has to be
// able to answer about (F27's renewal, P3.4's cards), and a trial that appeared
// nowhere would be a licence the person who took it cannot see.
//
// The key leaves by mail, the way every issue path's does: the bundle is the key
// line and the renewal secret line, shown once and never stored (D2135, §7.3),
// so the API's own answer carries no key and no secret — it is the SPA that put
// the ask, and the account that holds the trial gets its key at its own address.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RouteTrial is the trial's door on the portal's API (P1.0): the SPA's own
// origin reaches it through Hosting's rewrite (F21), which is why it is
// registered under the subtree rather than mounted by Handler.
const RouteTrial = "/api/trial"

// maxTrialBytes is what a trial ask may body. It carries a project id and
// nothing else, and the cap is the mount's own rule read on this route rather
// than a size that one field needs (api.go): a door a stranger can post to is
// one whose body the service has to hold.
const maxTrialBytes = 4 << 10

func init() {
	// The route reaches the sign-in verifier and the document store, and it says
	// so, so that a deployment serving the trial without one is refused when it
	// starts rather than answering every ask with a failure of its own (api.go's
	// `reaching`). The store is what holds the caller's organisation (P2.1) and
	// what the trial's own two records are written to (below), so a deployment
	// without one can serve no trial at all.
	registerAPI(http.MethodPost, RouteTrial, (*Service).trial,
		withBody(maxTrialBytes), reaching(needDocs, needSignIn))
}

// TrialCall is what a trial ask carries: the installation's Firebase project id,
// and nothing else.
//
// It carries no term, no tier and no policy, because those are the row's
// (D2133): a caller that could name them could mint a trial that never checks at
// all. It carries no seat count either: this door is the self-serve trial, and
// that one is **single-seat** by the owner's ruling — §10.3 of the Subscription
// Terms makes the free seat's renewable key the self-serve, single-seat trial,
// and B6 keys the same ruling to it — while a trial asked for more seats is
// §17's: the manual request form, which a person reads and approves one at a
// time. The minter mints the seat count this route names and refuses a request
// that names another (minter.go), so a body carrying a seat count at all is a
// body this route does not read.
//
// The body is read through decodeWire like the portal's other requests, strictly
// (P1.1). The shape has no entry in the wire registry (wire.go): a registered
// shape is a JSON Schema, a fixture and the SPA's own types beside it, and those
// are the portal's wire area's files rather than this task's. What that leaves
// untested is the SPA's agreement with this shape, not this side's reading of it.
type TrialCall struct {
	WorkspaceID string `json:"workspace_id"`
}

// trialPrefix opens the name one trial is held under, the way `rotation:` opens
// a rotation's (renew.go): it is both the id the store files the trial under and
// the source its issuance is written under, because what §14 counts and what the
// store records of one trial are one thing.
const trialPrefix = "trial:"

// trialInstallation is the installation half of §14's rule as the store holds
// it: the organisation the caller is a member of **with** the project id the ask
// named, escaped whole (D2201).
//
// The store's Trial carries the one installation field §14's rule was written
// against (K2.2, store.go), and D2201's amendment adds a second half to what
// that field counts; the pair therefore goes into it joined rather than into a
// second document, which is the store's to add and not this route's. What the
// join has to survive is the store's own naming rule: the id a document is filed
// under is one the deploy's backend can hold, so a slash in either half — a
// project id is a caller's own string and nothing bounds it here, and a local
// part may carry one (§4.1's `address`) — would file the trial as a path
// into another collection rather than as a document of this one (docstore's
// `ErrUnstorableName`). Escaping each half is what keeps one half from reaching
// into the other either: the separator is escaped inside either, so the join is
// the only place it appears.
//
// The organisation half is the portal's own id (P3.3): it is minted by the store
// (`newID`) and not derived from anything a caller sends, so the pair's first
// half is one no request can spell for another organisation.
func trialInstallation(organisation, installation string) string {
	return trialPrefix + url.PathEscape(organisation) + "|" + url.PathEscape(installation)
}

// The refusals this route answers with. Each is a code a client branches on and
// a sentence a person reads, and no sentence says anything about the service's
// inside (§7.2's rule, api.go's refusal shape).
const (
	trialRateRefusal         = "Too many requests from this address just now. Try again shortly."
	trialSignInRefusal       = "This request's sign-in could not be verified, so no trial was issued for it. Sign in to the licence project and ask again."
	trialBodyRefusal         = "This request is not the ask this route reads: it carries the workspace's Firebase project id and nothing else. A trial of more than one seat is asked for through the request form."
	trialClaimedRefusal      = "This account or this workspace has already had its trial, and §14's trial is one per installation and account."
	trialWorkspaceEmpty      = "A trial names the workspace it is for, and this request names none."
	trialOrganisationRefusal = "A trial is granted to an organisation, and this account is not a member of one in the portal yet. Create an organisation and ask again."
	trialBoundRefusal        = "This organisation is already licensed for that Firebase project. A trial is for a project it holds nothing for."
	trialFreeMailRefusal     = "A trial is granted to an organisation, and this account's address is at a free mail provider, where anyone can hold an address and it is not one an organisation's member asks with. Ask again with an address at your organisation's own domain."
	trialAccountRefusal      = "A trial is counted against the account the sign-in proved, and this address is not one a trial can be filed under. Ask again from another account."
	trialWorkspaceRefusal    = "A trial is filed under the workspace id this request names, and this one is not one a trial can be filed under: name the workspace's own Firebase project id, and nothing longer."
)

// trialNameBytes is the length the deploy's document store admits for the id of
// one of its documents (docstore's `fileable`).
const trialNameBytes = 1500

// trialFileable answers whether the store can file a document under name, which
// this route has to ask **before** it mints: the two names an ask is filed under
// are made from strings the ask brought (the account the sign-in proved, and the
// installation name the project id goes into), and the store answers a name its
// backend cannot hold only when it is writing one — which is after the mint, so
// an unfilable name would be answered by withdrawing the key the mint had
// already signed: a §8 entry per ask, into a list that only grows, and a key
// signed for nobody.
//
// The rule is the store's own, and it is written out here rather than imported
// because the predicate behind it is unexported and the store's file is another
// task's. What makes that safe is that the two have to answer alike: a name this
// admits and the store refuses is exactly the failure this exists to close, so
// the two move together — a slash is a path separator there, "." and ".." are
// path traversal, `__…__` is the database's reserved form, and 1500 bytes is
// the length it admits.
func trialFileable(name string) bool {
	if len(name) > trialNameBytes || name == "." || name == ".." ||
		(len(name) >= 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '/' })
}

// trial is §14's trial route: a signed-in account asks for a trial for one
// installation and is mailed its key.
//
// The order is the address's allowance, then the body, then the sign-in, then
// the caller's organisation, then the free mail refusal, then the two names the
// ask is filed under and the shape of the project id, then the two halves of
// §14's rule, then the account's domain's allowance, then the mint, then the
// claim, and then the licence, the mail and the two records. The address's
// allowance is spent first for the reason renew.go's is (a body this service
// never parses is as cheap to serve as a good one); the sign-in is read before
// the rule and before the mint because the account is half of what both count;
// the organisation is read from the memberships the sign-in names (P2.1) and is
// the other half; the free mail refusal follows both because it is the account's
// own address that it reads (D453); the names are checked where both their
// halves are known and before anything they would be written into, because the
// store refuses a name it cannot file only when it writes one and every such
// write comes after the mint (`trialFileable`); the project id's shape is
// checked with them, because the trial now writes a workspace for that project
// (below) and a workspace's project id is Firebase's own grammar — the same rule
// binding applies to it (D2205, workspaces.go) — while a well-shaped id is a
// string nothing proves (D2201); the rule is read before the mint because
// minting for an ask that is going to be refused spends the key ring's authority
// on a key nobody is given; and the domain's allowance comes after everything
// that can refuse the ask without a key, because a domain is shared — an ask that
// is refused mints nothing and must not spend an allowance other accounts on that
// domain still need (the throttle is documented per domain:
// docs/licensing/licence-key.md, "Limits, accepted").
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
// account is answered a failure rather than a key it never received. The
// licence below is written in the same window and for the same reason: it comes
// after the claim, so nothing is recorded for a trial the store refused, and
// before the mail, so a key an account can hold is a licence the portal can
// answer about. A failure there leaves the same state a mail failure does — a
// claimed trial, a minted key and two records — which is why it is answered the
// same way (trialFailed).
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
	// The organisation is the portal's own and not the address's domain (P3.3,
	// D2185): the free seat's organisation is the one a person signed an
	// organisation up as (P2.1), and a trial is the same kind of grant, so the
	// half of D2201's pair that this route counts is the id the caller's
	// membership names. The domain a key's `organisation` claim used to carry is
	// what the free mail refusal below still reads, and it is the one thing left
	// of the old reading: a domain is a name anybody can hold an address at, and
	// an organisation is not.
	organisation, member, err := s.currentOrganisation(r.Context(), caller)
	if err != nil {
		refuseOrgAccess(w, err)
		return
	}
	if !member {
		refuseAPI(w, http.StatusForbidden, "no_organisation", trialOrganisationRefusal)
		return
	}
	// The address the sign-in proved is the account half of §14's rule and the
	// one the key is mailed to; its **domain** is what D453's list is read
	// against, which is why the refusal below is about the address and no longer
	// claims to be about the organisation the trial is counted with.
	domain := domainOf(caller.Email)
	if s.freeMail(domain) {
		refuseAPI(w, http.StatusForbidden, "free_mail_address", trialFreeMailRefusal)
		return
	}
	// Two names this ask is filed under are made from strings the ask brought,
	// and both are refused here when the store cannot hold them — before the
	// allowance below, because a refusal spends nothing, and before the mint,
	// because the store's own refusal would arrive after it (`trialFileable`).
	// The account is the address the sign-in proved, read through §4.1's own
	// rule, which admits a slash in a local part and so admits an address the
	// account collection cannot file; the installation is the pair D2201 counts,
	// whose organisation half is the portal's own id and whose project half the
	// ask names and nothing bounds.
	if !trialFileable(caller.Email) {
		refuseAPI(w, http.StatusBadRequest, "unfileable_account", trialAccountRefusal)
		return
	}
	installation := trialInstallation(organisation.ID, call.WorkspaceID)
	if !trialFileable(installation) {
		refuseAPI(w, http.StatusBadRequest, "unfileable_workspace", trialWorkspaceRefusal)
		return
	}
	// The project id is a caller's own string and nothing proves the caller
	// controls it (D2201), so this is not a check about who owns the project: it
	// is the one check that a string is a Firebase project id at all, and it is
	// here because the trial writes a workspace for it (trialLicence) — a
	// workspace's project id is what a screen draws and what a later binding of
	// the same project is compared against, and a string Firebase could not name
	// a project is one no installation will ever match. The rule is binding's own
	// and not a second one (workspaces.go's `validProjectID`).
	if !validProjectID(call.WorkspaceID) {
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_project_id",
			"A Firebase project id is 6 to 30 characters of lower-case letters, digits and dashes, and begins with a letter.")
		return
	}
	if !s.trialFree(w, r, installation, caller.Email) {
		return
	}
	// D2204 read where the trial can honour it. The licence below is written for
	// the project the ask named — the trial is *about* that project, even though
	// what it writes carries no project id (trialLicence) — so an organisation
	// that already holds a licence of its own for that project would end up
	// holding two for one installation, which is a licence the screen draws twice
	// and a key that fights the one already there.
	//
	// What it is **not** asked is the rule binding asks. That rule reads every
	// organisation's workspaces, and a trial would then let any signed-in account
	// bar a stranger's binding by naming a project id — a caller's own string
	// that proves nothing (D2201, F37). So this reads the asking organisation's
	// own records only, and the trial writes no project id at all: the claim that
	// one project id is one workspace is made by the binding, after the
	// installation there is probed (D2205) and where operations can transfer a
	// project a squatter took (TN-P1).
	if !s.trialProjectFree(w, r, organisation.ID, call.WorkspaceID) {
		return
	}
	// The account's own **domain's** allowance is spent here, and it is the
	// throttle §14's trial is documented with: "the trial path is throttled per
	// domain" (docs/licensing/licence-key.md, "Limits, accepted"), because the
	// account a caller asks from is one they may open another of and a domain is
	// not. It is spent after the sign-in and not with the address, for the reason
	// a renewal's key allowance is (renew.go's `charged`): only a request that
	// proved who asks is counted against that domain, so no caller can spend
	// another domain's allowance. And it is spent after §14's rule rather than
	// before it, because an ask the rule is going to refuse mints nothing and
	// must not spend the domain's allowance either: a domain is shared — a free
	// mail domain is the case D453's list exists for — so an account that already
	// had its trial and keeps asking would otherwise hold that domain's allowance
	// at nothing and lock every other account on it out of a trial it never took.
	if !s.limiter.allow("domain:" + domain) {
		refuseAPI(w, http.StatusTooManyRequests, "rate_limited", trialRateRefusal)
		return
	}
	// The v1 route and not the older format's, for the reason the paid path
	// states (merchant.go's `paid`): what an installation is issued is a v1 key,
	// and the intake holds one minter whose client answers both routes, so a
	// service that could not answer a v1 request is refused rather than handed a
	// key no released verifier reads (Check 174).
	mints, answersV1 := s.mints.(MintsV1)
	if !answersV1 {
		s.trialFailed(w, errors.New("the intake's minter does not answer a v1 mint request, and a trial is a v1 key"))
		return
	}
	answer, err := mints.MintV1(r.Context(), RequestV1{
		Environment: s.cfg.Environment,
		Policy:      PolicyTrial,
		WorkspaceID: call.WorkspaceID,
		// The portal organisation the caller is a member of is what the key names,
		// and it is the same value §14's rule counted above and the entitlement
		// below is written for: what the key says the licence belongs to and what
		// the rule counted are one thing (D2201, D2185).
		Organisation: organisation.ID,
		// One seat, which is what §10.3's self-serve trial is and what the ask
		// cannot name: the minter refuses a key for nobody, and a trial of more
		// than one seat is §17's request form.
		Seats: 1,
		// No term and no end: the row runs for its own fourteen days and the
		// minter refuses a term on a row nothing renews (D2136, minter.go).
	})
	if err != nil {
		s.trialFailed(w, err)
		return
	}
	if err := s.store.ClaimTrial(r.Context(), Trial{
		Installation: installation,
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
	licence, err := s.trialLicence(r.Context(), organisation, call.WorkspaceID, s.now().UTC())
	if err != nil {
		// The trial is claimed and its key is minted, and now nothing can be written
		// for either: the account is answered a failure and asks again, which the
		// claim refuses, and an operator reads the trial's key off the store's list
		// and its licence off this transaction's failure (K2.2, §8). Leaving the
		// licence out rather than failing would be worse: a key in an account's inbox
		// is a key the portal has to answer about, and what F27's renewal and P3.4's
		// cards read is this record.
		s.trialFailed(w, err)
		return
	}
	// The issuance is named the way a binding's is and not by the trial's own
	// installation name (workspaces.go's `bindingSource`): what this key is, to
	// everything that reads the store afterwards, is the key of the licence
	// above, and the name is what a later binding or rebind of that licence reads
	// the key it replaces back with (`licenceKeys`, D2198) — a trial whose key
	// could not be found that way would be a key left live at an installation the
	// licence had moved away from. The name carries the licence's id and a nonce
	// for the reason every binding's does: the store's source is an idempotency
	// key, and two issues of one licence under one source would be the second read
	// as a redelivery of the first.
	source, err := bindingSource(licence.ID)
	if err != nil {
		s.trialFailed(w, err)
		return
	}
	answer.Record.Source = source
	answer.Record.Email = caller.Email
	if err := s.mail.Send(r.Context(), paidKeyMail(caller.Email, answer.Key, answer.RenewalSecret)); err != nil {
		s.trialFailed(w, err)
		return
	}
	if err := s.store.SaveIssuance(r.Context(), answer.Record); err != nil {
		// The store's answer is not the state, and this is where the two are told
		// apart — as they are for the claim above and on the paid path
		// (merchant.go's `paid`): the deployed store writes an issuance in a
		// transaction that is re-run when its result is lost, and its re-run
		// answers `ErrAlreadyRecorded` about its own commit
		// (firestore_store.go's `SaveIssuance`). The key has been minted, claimed
		// and mailed by now, so answering that as a failure would hand the account
		// a key this store holds no state for — and a key with no state fails the
		// proof the daily status check makes (renew.go's `proved`), so it is
		// refused like any key nobody issued while asking again is answered 409:
		// the trial is spent, and no method of the Store interface repairs one. So
		// the record is re-read under the source this ask wrote it to, and only a
		// source holding no record, or one holding another key's, is a failure.
		held, recorded, readErr := s.recorded(r.Context(), answer.Record.Source)
		if readErr != nil {
			s.trialFailed(w, errors.Join(err, readErr))
			return
		}
		if !recorded || held.KeyID != answer.Record.KeyID {
			// Unlike the paid path this one withdraws nothing: the key is one the
			// store has claimed a trial for and mailed, so a withdrawal would take
			// the account's own key away and leave the trial spent with nothing in
			// hand — the trial's claim names the key, which is what an operator
			// reads it off (§8's list, K2.2). Nothing is written for it either, so
			// what the account holds is a key its check-in is refused for, and the
			// failure is reported rather than papered over.
			s.trialFailed(w, err)
			return
		}
		// The record is this key's, so the write landed and only its answer was
		// lost: the rest of the pair is finished below rather than left unwritten,
		// because the state written there is what the account's own key needs to
		// be checked in (renew.go's `proved`).
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
	// credential may be shown. It is 204 with no body at all rather than 200 with
	// an empty one, because there is nothing in it to read: the SPA's client reads
	// a body only where there is one (`ee/portal/src/api/client.ts`), and a
	// success with a body it has to parse is a shape that has to be promised on
	// both sides.
	w.WriteHeader(http.StatusNoContent)
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

// trialCall reads the ask's body: the route's own JSON, strictly, with the one
// field it names present and non-empty. A body that carries anything else — a
// seat count above all — is refused here rather than ignored, because
// `decodeWire` is strict and a trial of more than one seat is not this door's to
// give (§10.3, B6, §17). What the id is judged for is its shape and the name it
// makes, and both of those are `trial`'s own: the shape check needs nothing this
// reader has (workspaces.go's `validProjectID`, which the trial writes a
// workspace under), and the name check needs the organisation the id is counted
// with (D2201) and has to come before the mint (`trialFileable`). What is not
// judged is whose project the id names: nothing proves that and no door can
// (D2201, wire.go's split, `decodeWire`).
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
	return call, true
}

// trialCaller verifies the ask's sign-in and answers the account it names, which
// is half of what §14's rule counts.
//
// The token rides in the request's `Authorization` header as a bearer token,
// which is where the SPA's own calls carry the ID token it holds (P1.3): what is
// verified is P1.3's business and not this route's — the verification itself is
// signin.go's own reader of the seam, so the SPA's door and the CLI's flow are
// answered by one verifier and one set of rules — and an ask whose token the
// verifier refuses is answered the same way as one that carried none: the
// account is what a trial is granted to, and there is no trial without one.
func (s *Service) trialCaller(w http.ResponseWriter, r *http.Request) (Caller, bool) {
	token, carried := bearerToken(r)
	if !carried {
		refuseAPI(w, http.StatusUnauthorized, "unverified_sign_in", trialSignInRefusal)
		return Caller{}, false
	}
	caller, verified := s.verifySignIn(r.Context(), token)
	if !verified {
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

// trialFree answers whether the store holds no trial for this installation or
// for this account, which is the cheap half of §14's rule: it reads the list and
// refuses an ask that is certainly spent before a key is minted for it. It is
// not the rule itself — two asks arriving together both pass it — and the
// store's `ClaimTrial` is what decides between them.
//
// The installation it is handed is the pair and not the project id, which is the
// whole of D2201: the id is a caller's own string and proves nothing (F37, TN3),
// so a rule that counted it alone would hand any signed-in account the power to
// take another organisation's trial by naming a project id it read somewhere, and
// to bar that organisation for good. Counted as a pair, the same ask spends only
// the asking organisation's trial.
func (s *Service) trialFree(w http.ResponseWriter, r *http.Request, installation, account string) bool {
	trials, err := s.store.Trials(r.Context())
	if err != nil {
		s.trialFailed(w, err)
		return false
	}
	for _, held := range trials {
		if held.Installation == installation || held.Account == account {
			refuseAPI(w, http.StatusConflict, "trial_already_claimed", trialClaimedRefusal)
			return false
		}
	}
	return true
}

// trialProjectFree answers whether the caller's organisation holds no workspace
// for this Firebase project, which is D2204 read where a trial can honour it:
// the licence below is granted for the project the ask named, and an
// organisation that already has a licence of its own on that project would hold
// two on one installation.
//
// What is asked is the organisation's own workspaces and never every
// organisation's, and that is D2201's half of the rule (F37, TN-P1): a project
// id is a caller's own string that proves nothing, so a trial that refused a
// project id another organisation had bound would let any signed-in account bar
// a stranger's binding by naming its project — while the harm D2204 refuses
// among strangers is refused where it can be proved, at the binding, where the
// installation is probed (D2205) and operations can transfer a project a
// squatter took (TN-P1). Since what the trial writes carries no project id
// (trialLicence), the only rows this can find are bindings that were probed,
// and a stranger's trial is not one of them.
func (s *Service) trialProjectFree(w http.ResponseWriter, r *http.Request, organisationID, projectID string) bool {
	held, err := recordsWhere[WireWorkspace](r.Context(), s.docs, workspacesCollection, Where{Field: "project_id", Value: projectID})
	if err != nil {
		s.trialFailed(w, err)
		return false
	}
	for _, other := range held {
		if other.OrganisationID == organisationID {
			refuseAPI(w, http.StatusConflict, "project_id_bound", trialBoundRefusal)
			return false
		}
	}
	return true
}

// trialLicence writes the trial's two records — the workspace the trial licenses
// and the entitlement itself — and answers the entitlement.
//
// A trial is a workspace's licence and not the organisation's alone (D2204): the
// screen draws a licence on a workspace's card (P3.4), and the trial a person
// took has to be the licence that card names, which is why this route writes a
// workspace of its own, named for the project, and points it at the trial's
// grant. It is the same pair P3.1's binding writes for a workspace nobody made
// yet (workspaces.go's `bindingWorkspace`, `pointWorkspaceAt`), and it is its own
// transaction because `grantEntitlement` reads the workspace a grant names and
// this one is written here: one transaction, reads before writes, so the project
// D2204 asks about is asked inside the write that would break it (api.go's Tx).
//
// The key is already minted and the trial already claimed when this runs, so a
// failure is the route's own and not a refusal (trialFailed): what it leaves is a
// claimed trial, a key nobody has been mailed and no licence, which is the state
// this route's order documented above accepts.
func (s *Service) trialLicence(ctx context.Context, organisation WireOrganisation, projectID string, at time.Time) (WireEntitlement, error) {
	var granted WireEntitlement
	err := s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		// Every read comes before every write: the store is read-before-write and
		// refuses a document read after one the same transaction has written
		// (docstore's ErrReadAfterWrite). The rule is asked here and not only above
		// for the reason every other one is (grantEntitlement): a binding another
		// request made between the route's own read and this one is part of what
		// this transaction read, so the store runs it again on the state that
		// holds it rather than granting a trial for a project this organisation
		// has just bound a licence of its own to.
		held, err := recordsWhere[WireWorkspace](ctx, tx, workspacesCollection, Where{Field: "project_id", Value: projectID})
		if err != nil {
			return err
		}
		for _, other := range held {
			if other.OrganisationID == organisation.ID {
				return errProjectIDBound
			}
		}
		id, err := newID()
		if err != nil {
			return fmt.Errorf("keyservice: the trial's workspace id: %w", err)
		}
		workspace := id
		entitlement, err := entitlementRecord(entitlementGrant{
			At:             at,
			OrganisationID: organisation.ID,
			WorkspaceID:    &workspace,
			Source:         entitlementSourceTrial,
			Row:            PolicyTrial,
			Seats:          1,
			StartsAt:       at,
		})
		if err != nil {
			return err
		}
		// The trial is bound as it is written, because its key is minted in the
		// same request: BoundAt is what tells a workspace with a key behind it from
		// one nobody has bound (P7.1's reminder, `bindEntitlement`'s rebind rule),
		// and a trial whose key exists but whose licence reads unbound would be a
		// card offering to bind a workspace that already has one.
		bound := at
		entitlement.BoundAt = &bound
		// The workspace carries the project's **name** and not its id, and that is
		// the whole of this route's answer to D2204. A project id on a workspace is
		// the claim that one project id is bound by one workspace across every
		// organisation (workspaces.go's `refuseBoundProject`), and it is a claim
		// only the binding may make: the binding is where the installation is
		// probed (D2205) and where operations can transfer a project a squatter
		// took (TN-P1). An ask that wrote it without proving anything would let any
		// signed-in account bar a stranger's binding for good by naming the
		// stranger's project id — a string the asker types and nothing checks
		// (D2201, F37). So the licence is granted for the project, the card is
		// drawn with its name, and the project id is attached by the same
		// `POST /workspaces/bind` every other licence's is, after its probe.
		if err := putRecord(ctx, tx, workspacesCollection, id, WireWorkspace{
			ID:             id,
			OrganisationID: organisation.ID,
			Name:           projectID,
			EntitlementID:  &entitlement.ID,
			CreatedAt:      at,
			BoundAt:        &bound,
		}); err != nil {
			return err
		}
		if err := putRecord(ctx, tx, entitlementsCollection, entitlement.ID, entitlement); err != nil {
			return err
		}
		granted = entitlement
		return nil
	})
	if err != nil {
		return WireEntitlement{}, err
	}
	return granted, nil
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
