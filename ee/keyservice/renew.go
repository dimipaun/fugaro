// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The renewal and status endpoints (§7, §8, D2135, D2140): the two routes a
// project's key reaches the service by, and the pair that hands out and reads a
// credential. They are the intake's and not the minter's — this half holds no
// Signer, and the next key is asked for through the minter's own service like
// every other key (Check 174, `MintsV1`).
//
// What authenticates a caller is the per-key renewal secret D2135 rules, and
// the digest of it in constant time and nothing else: the key carries the
// secret's hash, and the key is read by every reader of the runs bucket, so the
// hash must not be a signing key — an HMAC keyed by it would have been forgeable
// by a bucket reader (D2135 as amended). Nothing else about the caller is read:
// there is no session and no account. The one thing a renewal takes from the
// request besides the secret is the workspace the caller is in, and it takes it
// as a claim to be checked rather than as a fact: the successor is minted into
// the workspace the key is bound to (TN2, the store's `KeyState`), so a key
// asked for from somewhere else is refused instead of moved.
//
// Every answer here is uniform (§7.1's rule on these routes): a caller that has
// not proved the secret gets one sentence and one status, whatever was wrong —
// an unknown key id, a secret that hashes to something else, a body this service
// does not read, or a request the throttle refused. Only a key whose secret was
// proved is told anything else, and what it is told is its own key's state.

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// The two statuses an answer carries. They are the wire's words rather than
// this package's, because the job that reads them is not this package's: K4.1
// asks these routes and reads these names, and they are fixed by F11.
const (
	StatusOK      = "ok"
	StatusRevoked = "revoked"
)

// Answer is what both routes answer, and it is one shape on purpose (F11): a
// status answer is this object with nothing else in it, and a renewal's is the
// same object with the next key and its new secret beside it.
//
// Key and RenewalSecret are absent from every answer but a renewal's, and a
// status answer carries neither a key nor a secret at all: what those two routes
// are read for is whether a key is still good, and a reader that learned a key
// from one of them would have been handed a licence it did not hold.
type Answer struct {
	Status string `json:"status"`
	// NewerKey answers whether the store holds a key issued after this one —
	// the successor a rotation recorded. A job that reads it true has a key to
	// ask for and does not need to renew again.
	NewerKey bool `json:"newer_key"`
	// Key and RenewalSecret are the two lines of the bundle an issue path shows
	// once (D2135): the next key and the secret that replaces this one's.
	Key           string `json:"key,omitempty"`
	RenewalSecret string `json:"renewal_secret,omitempty"`
}

// RenewalCall is what both routes read: the key a caller holds, the secret it
// proves it with, and the workspace its installation is in. A renewal is minted
// into the key's own workspace and this is the check that the caller is there:
// it names the workspace the request comes from, and one that is not the key's
// is refused. The status route reads the first two and ignores the third,
// because a status answer mints nothing and there is no workspace for it to
// name — a check must answer for a key whatever the project's own config says.
type RenewalCall struct {
	KeyID         string `json:"key_id"`
	WorkspaceID   string `json:"workspace_id"`
	RenewalSecret string `json:"renewal_secret"`
}

// renewalRefusal is the one answer every caller that has not proved the secret
// gets. It names nothing about what was wrong — not the field, not the key and
// not whether the key exists — because telling an unknown key id from a wrong
// secret would turn these two routes into a lookup for another holder's licence
// (F7, §7.1).
const renewalRefusal = "This request cannot be answered."

func refuseRenewal(w http.ResponseWriter) {
	http.Error(w, renewalRefusal, http.StatusForbidden)
}

// workspaceRefusal is the answer to a caller that proved a key's secret and
// asked for its successor in a workspace that is not the key's own. It is not
// the uniform refusal because the caller here has proved it holds the key, and
// what it is being told is its own key's binding: a key is tied to one
// workspace and moving it is a reissue (D2134 as amended by TN2), so naming
// another workspace is a request this service cannot serve rather than a
// renewal to retry.
const workspaceRefusal = "This key is bound to another workspace, and a renewal keeps it there. Moving an installation to another workspace is a reissue."

func refuseWorkspace(w http.ResponseWriter) {
	http.Error(w, workspaceRefusal, http.StatusForbidden)
}

// rotationPrefix opens the source a rotation writes. It is its own name because
// two things read it: the rotation that writes one, and §8's walk from a
// refunded key to the other keys of its licence, which takes the prefix and the
// key it is walking from as a step from a key to the key that replaced it — the
// one route whose record names its predecessor rather than only the
// subscription the walk is already matching on.
const rotationPrefix = "rotation:"

// rotationSource names the record one rotation writes, and it is the store's
// memory of the rotation itself: the store keeps no key material (§7.3), so the
// fact that a key has been succeeded is a record and not a stored key, and it is
// what makes the old secret spent instead of merely superseded.
func rotationSource(keyID string) string {
	return rotationPrefix + keyID
}

// renewalHandover is how long a rotated key's old secret still fetches its
// successor (D2135). It is the time a lost answer needs to be asked for again:
// the job writes the new secret and then the key, and a crash between the two —
// or a response the network dropped — leaves a holder whose only credential is
// the old secret (K4.1). It is also the life of the store's `Handover`, which is
// what makes the promise hold on whichever instance the retry reaches.
const renewalHandover = 24 * time.Hour

// holder is a caller that has proved a key: what the store remembers about it,
// the record of what was issued, the row it was minted from, and whether the
// licensor has withdrawn it.
type holder struct {
	state   KeyState
	record  Record
	policy  Policy
	revoked bool
	// rotated says the store holds a rotation of this key: a successor was
	// minted under a new key id, so this key's secret is spent but for the
	// window its lost answer is still reachable in. rotatedAt is the instant the
	// rotation was recorded, which is where that window starts; a record that
	// carries no instant still says a successor exists.
	rotated   bool
	rotatedAt time.Time
}

// proved answers whether a caller holds the key it names, and it is the whole of
// what either route authenticates.
//
// The digest is compared in constant time: a comparison that returned early
// would say how much of a guess was right, and the secret is the only thing
// standing between a stranger and a key. `false` is every way of not being the
// holder — an unknown key id, no state for it, a secret that hashes to something
// else, and a record the service does not hold, which is a key it could not mint
// a successor for even with the right secret. An error is the service failing to
// read, which is not an answer about the caller.
func (s *Service) proved(ctx context.Context, call RenewalCall) (holder, bool, error) {
	state, known, err := s.store.KeyState(ctx, call.KeyID)
	if err != nil {
		return holder{}, false, err
	}
	if !known || subtle.ConstantTimeCompare([]byte(state.RenewalSecretHash), []byte(digest(call.RenewalSecret))) != 1 {
		return holder{}, false, nil
	}
	records, err := s.store.Issuances(ctx)
	if err != nil {
		return holder{}, false, err
	}
	var record Record
	issued := false
	rotated := false
	var rotatedAt time.Time
	for _, candidate := range records {
		if candidate.KeyID == call.KeyID && !issued {
			record, issued = candidate, true
		}
		if candidate.Source == rotationSource(call.KeyID) {
			rotated, rotatedAt = true, candidate.Issued
		}
	}
	if !issued {
		return holder{}, false, nil
	}
	policy, known := DefaultPolicy(record.Plan)
	if !known {
		return holder{}, false, nil
	}
	_, revoked, err := s.store.Revocation(ctx, call.KeyID)
	if err != nil {
		return holder{}, false, err
	}
	return holder{state: state, record: record, policy: policy, revoked: revoked, rotated: rotated, rotatedAt: rotatedAt}, true, nil
}

// renewalCall reads one of the two routes' requests: a body under this service's
// own bound, decoded strictly like every other wire format here, and inside the
// intake's throttle for the address it came from.
//
// Everything it will not read it refuses with the one uniform answer, and the
// throttle is asked **before** the shape is: a burst of malformed bodies is one
// address's rate like any other, and a request the throttle refused is counted
// nowhere (§7.2, `throttle.allow`).
//
// The key's own bucket is spent by `charged`, once the secret has been proved,
// and not here with the address: the key id is what a support ticket quotes and
// what a log carries, so a bucket keyed on it and spent by any caller would let
// whoever has read one spend the holder's allowance with wrong-secret calls and
// have the holder's own renewal refused. Guessing is bounded by the address's
// bucket, which is the one every request pays.
func (s *Service) renewalCall(w http.ResponseWriter, r *http.Request) (RenewalCall, bool) {
	// Spent before anything is read of the request, which is the whole of what
	// makes the sentence above true: a body this service never parses is as
	// cheap to serve as a good one, so a bucket that only counted the bodies
	// that got as far as a key id would let a flood of them be served for free
	// (TestARequestSpendsItsAddressesAllowanceWhateverItsShape).
	if !s.limiter.allow("ip:" + clientIP(r)) {
		refuseRenewal(w)
		return RenewalCall{}, false
	}
	var call RenewalCall
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		refuseRenewal(w)
		return RenewalCall{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&call); err != nil {
		refuseRenewal(w)
		return RenewalCall{}, false
	}
	// The id is read as it arrived and never trimmed: what the store is looked
	// up by is the key's own identity, and a service that repaired a caller's
	// spelling would answer for an id the caller did not have.
	if call.KeyID == "" || call.RenewalSecret == "" {
		refuseRenewal(w)
		return RenewalCall{}, false
	}
	return call, true
}

// charged spends the key's own allowance, which is spent here rather than with
// the address (renewalCall's comment): only a request that proved the secret is
// counted against the key, so a holder's allowance cannot be spent by a caller
// that does not hold the secret, and a holder that asks too often is answered
// like a stranger.
func (s *Service) charged(w http.ResponseWriter, call RenewalCall) bool {
	if !s.limiter.allow("key:" + call.KeyID) {
		refuseRenewal(w)
		return false
	}
	return true
}

// status is §8's endpoint: whether a key is still good, and whether a successor
// of it exists. It carries no key and no secret, in either answer, and it is
// read by the job's check (K4.1) and by `fugaro licence refresh` (K3.2).
//
// A revoked key answers `revoked` to the caller that proved its secret, and
// nothing else about it: the licensor's reason (a refund, a chargeback, a leak)
// is the licensor's own word for it and never reaches the holder (§8).
func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	call, ok := s.renewalCall(w, r)
	if !ok {
		return
	}
	key, ok, err := s.proved(r.Context(), call)
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		refuseRenewal(w)
		return
	}
	if !s.charged(w, call) {
		return
	}
	s.sight(r.Context(), call.KeyID)
	writeAnswer(w, Answer{Status: key.status(), NewerKey: key.rotated})
}

// renew is §7's endpoint: a holder proves the key's renewal secret and is
// answered with the next key and the secret that replaces it.
//
// The successor is a **new key id** and a **new secret** (D2135): a rotation is
// what makes a leaked secret cost the holder one renewal rather than the
// licence, and the hash the successor carries is the new secret's, so the old
// secret authenticates nothing after the window below. Everything else about the
// key is carried over — the row it was minted from, its seats, its organisation
// and its own workspace — because a renewal is the same licence for a later
// term, and a term bound to another workspace would be a different licence.
//
// A row nothing renews is refused: an air-gapped key and a trial carry
// `renewal_mode: none`, and the whole of that row is that nothing contacts the
// server for it (§7, D2138). A revoked key is refused for the same kind of
// reason: §8's list is what a renewal checks first, and a key the licensor has
// withdrawn is never handed a successor.
//
// A key nobody is paying for is refused too, and it is the same kind of rule:
// the licence this route renews is one the checkout sold, so its record names
// the subscription it was minted for and the route reads that subscription —
// and how it has moved since — before a key is minted. A cancelled subscription
// and one whose end has passed are answered the key's own state and handed
// nothing, while a key whose record names no subscription at all is refused with
// the uniform answer: nothing here can prove such a key was ever paid for, and
// failing closed is the owner's ruling of 2026-11-13 (D2148 — §12.1's Enterprise
// column and `PolicyEnterprise` still claim otherwise, and TN6 has the two).
//
// A key outside its renewal window is answered its own state and handed nothing:
// the successor is the key for the term *after* the one the customer is in, and
// one minted before that term is nearly over is a term nobody paid for
// (renewable).
func (s *Service) renew(w http.ResponseWriter, r *http.Request) {
	call, ok := s.renewalCall(w, r)
	if !ok {
		return
	}
	// The workspace is required of a renewal and ignored by a status call: the
	// successor has to bind to one (the verifier refuses a key without it, TN2),
	// and a request that named none is a request this service cannot mint from.
	// What it is checked against is the key's own workspace, further down.
	if strings.TrimSpace(call.WorkspaceID) == "" {
		refuseRenewal(w)
		return
	}
	// Renewals are one at a time in this process, and the read of the key's state
	// is inside the lock with the writes that follow it: two calls that both read
	// "no successor yet" would both mint, and only one of the two could be the
	// rotation the store records — the other's key is one the store holds no
	// record of, so it is a paid key that can never renew, and answering with it
	// hands the customer exactly that (TestConcurrentRenewalsMintOneKey). What it
	// costs is that a renewal waits for the one before it, which is a mint's
	// round trip; what a lock here cannot reach is a second instance, and the
	// store's record of the rotation is what answers that one (answerRotation).
	s.renewMu.Lock()
	defer s.renewMu.Unlock()
	key, ok, err := s.proved(r.Context(), call)
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		refuseRenewal(w)
		return
	}
	if !s.charged(w, call) {
		return
	}
	s.sight(r.Context(), call.KeyID)
	if key.revoked {
		writeAnswer(w, Answer{Status: StatusRevoked, NewerKey: key.rotated})
		return
	}
	if key.policy.RenewalMode == licence.RenewalNone {
		refuseRenewal(w)
		return
	}
	// The key's own workspace is the one the successor goes into, and a state
	// that names none is an issue path's bug rather than the caller's: nothing
	// here can mint into a workspace it does not know, and a successor bound to
	// no workspace is one every verifier refuses (TN2).
	if key.state.WorkspaceID == "" {
		fail(w, errors.New("the key "+call.KeyID+" is bound to no workspace, so there is none to mint its successor into (K2.3)"))
		return
	}
	if call.WorkspaceID != key.state.WorkspaceID {
		refuseWorkspace(w)
		return
	}
	now := s.now()
	if key.rotated {
		// The successor exists and this key's secret is spent. The window is
		// the one case left, and it is the digest of the secret that earned the
		// rotation and not of any secret at all.
		if s.answerRotation(w, r.Context(), call, now) {
			return
		}
		// The window is open and the store does not hold the answer: a rotation
		// recorded a moment ago whose answer is still being written, or one whose
		// answer was lost with the write that should have kept it. Both are this
		// service unable to answer yet rather than a secret that is spent, and a
		// retry is what asks again — a refusal here would tell a holder whose
		// successor is minutes old that its key is finished.
		if key.rotatedAt.IsZero() || now.Before(key.rotatedAt.Add(renewalHandover)) {
			fail(w, errors.New("the rotation of the key "+call.KeyID+" is inside its window and the store does not hold its answer"))
			return
		}
		refuseRenewal(w)
		return
	}
	// §7's other half, and the one the window below cannot see: the window says
	// the *key* is near its end, and whether the term after it was paid for is the
	// subscription's own record (§5.2). It is read here — after the workspace has
	// been matched and after the rotated branch, both of which are about a
	// rotation already earned — and before anything is minted, so a licence whose
	// money has stopped is handed nothing.
	//
	// A key whose record names no subscription is refused with the uniform answer
	// an unproven caller gets, and not with its state: no subscription is the
	// licensor's own doing (a key minted by hand, by the admin page or by the mint
	// tool), and the owner's ruling of 2026-11-13 is that such a key does not
	// renew at all — fails closed, because a key nothing can prove was paid for is
	// the hole this gate exists to close, and answering its state would tell its
	// holder a renewal is one secret away.
	//
	// Every other unpaid state is answered with the key's own state and no key,
	// which is what the window's own refusal does and for the same reason: the
	// caller proved its secret, so what it is told is about its own key — this one
	// is still good to its end — and a uniform refusal here would read as the
	// licence being finished and send a paying holder to a reissue.
	if key.record.Subscription == "" {
		refuseRenewal(w)
		return
	}
	sub, held, err := s.store.Subscription(r.Context(), key.record.Subscription)
	if err != nil {
		fail(w, err)
		return
	}
	// A record naming a subscription the store does not hold is a store this
	// service cannot ask about the money at all — the same inconsistency
	// merchant.Renew refuses as ErrUnknownSubscription — and it is a failure
	// rather than an answer: nothing here can tell a paid term from an unpaid one,
	// and the caller's retry is what asks a store that has the subscription back.
	if !held {
		fail(w, fmt.Errorf("%w: the record of the key %s names the subscription %s", ErrUnknownSubscription, call.KeyID, key.record.Subscription))
		return
	}
	if sub.Cancelled || !now.Before(sub.Expires) {
		writeAnswer(w, Answer{Status: key.status()})
		return
	}
	// The subscription has to have paid for what the successor would cover before
	// anything is minted, and what says it has is its end being past the end of the
	// key in hand: its end is the merchant's own record of what the customer has
	// bought — the trigger's renewal is what moves it (merchant.Renew) — and this
	// route reads it rather than moving it. A route that moved it would credit every
	// caller the term after the one in hand: one month bought, a successor a call,
	// each with a fresh key id and so a throttle bucket of its own, and the
	// merchant's own renewal left finding its window closed behind them
	// (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers).
	//
	// It is that comparison and never a term measured out from the key's own end,
	// because the two ends are not on one grid (D2202): the row's key runs thirty
	// days where the subscription runs the term the customer bought, so an annual
	// subscription is a year of keys and not one term this key's arithmetic has to
	// reach; and the keys the other paid paths mint — the merchant's own renewal,
	// the seat change — end where those paths asked for them and on no grid at all.
	// A gate that asked for a whole term of room past the key refused every one of
	// them, which is a paid licence dark from the end of the key in hand
	// (TestAMerchantRenewalsKeyRenewsOffTheTermGrid,
	// TestASeatChangesKeyRenewsWhileTheTermRuns,
	// TestAMonthBilledInTwentyEightDaysRenewsToTheSubscriptionsEnd), and it is the
	// mint below rather than this check that keeps the successor to what was
	// bought, up to the whole-day round the term's own unit costs (`renewalDays`).
	//
	// The answer is the key's own state and not the uniform refusal, for the reason
	// the two checks around it give theirs: the caller proved its secret, and its key
	// is still good to its end — what it has not bought is the term after that.
	if !sub.Expires.After(key.record.Expires) {
		writeAnswer(w, Answer{Status: key.status()})
		return
	}
	if !renewable(key.record, key.policy, now) {
		// The state and not the uniform refusal: this caller proved the secret,
		// so what it is told is about its own key, and `ok` with no successor is
		// the same answer `/status` gives it. A refusal here would read as the
		// licence being finished and send a holder whose key is good for another
		// month to a reissue.
		writeAnswer(w, Answer{Status: key.status()})
		return
	}
	if s.mintsV1 == nil {
		fail(w, errors.New("the intake has no v1 minter, so there is no key to renew with (Check 174)"))
		return
	}
	minted, err := s.mintsV1.MintV1(r.Context(), RequestV1{
		Environment:  s.cfg.Environment,
		Policy:       key.policy.Name,
		WorkspaceID:  key.state.WorkspaceID,
		Organisation: key.record.Organisation,
		// The subscription's own seats and not the key's, because the two are
		// separately true: §5.3 moves the seats inside the term the customer has
		// already paid for — `seatChanged` mints the key with the count the
		// merchant sent and keeps the subscription's current — while the key it
		// supersedes runs to its own end still stating the count it was minted
		// with. A chain continued from that key would hand back the seats the
		// customer has given up, at the price of the ones it pays for, for as long
		// as the subscription runs
		// (TestASupersededKeysChainCarriesTheSubscriptionsSeats), while the
		// merchant's own renewal mints from this count already (merchant.Renew),
		// so the two routes into the term after a key agree on what it states.
		Seats:    sub.Seats,
		TermDays: renewalDays(key.record, key.policy, now, sub.Expires),
	})
	if err != nil {
		fail(w, err)
		return
	}
	// The new key's state is written before the rotation is recorded, and that
	// order is the recoverable one: a failure after the state leaves a key the
	// service can revoke and cannot renew (the store's own comment on the pair),
	// while a failure after the record alone would leave a rotation nothing can
	// be asked for again.
	if err := s.store.SaveKeyState(r.Context(), KeyState{
		KeyID:             minted.Record.KeyID,
		WorkspaceID:       key.state.WorkspaceID,
		RenewalSecretHash: digest(minted.RenewalSecret),
		LastSeen:          now,
	}); err != nil {
		fail(w, err)
		return
	}
	record := minted.Record
	record.Source = rotationSource(call.KeyID)
	// The buyer's address is carried over: the record is where a later renewal
	// still finds the only address this licence was ever delivered to (§5.1).
	record.Email = key.record.Email
	// The subscription is carried over for the reason the address is: the
	// successor is the same licence for a later term, so the gate above has to
	// find the same subscription on it, and a successor whose record named none
	// could never renew again — it would be handed no key at all, since a key
	// with no subscription is refused (§7.1).
	record.Subscription = key.record.Subscription
	if err := s.store.SaveIssuance(r.Context(), record); err != nil {
		if !errors.Is(err, ErrAlreadyRecorded) {
			fail(w, err)
			return
		}
		// Another instance rotated this key while this one was minting. What was
		// minted here is not that rotation — the store holds no record of it, so
		// nothing would let it renew, and answering with it would hand the
		// customer a paid key that is dead on arrival. The answer is the rotation
		// that won, which is where the store's handover points.
		if !s.answerRotation(w, r.Context(), call, now) {
			fail(w, errors.New("the rotation of the key "+call.KeyID+" was recorded by another instance and the store does not hold its answer yet"))
		}
		return
	}
	// The handover is written last, after the record, and that is the order that
	// can be recovered from: a rotation whose answer could not be kept is one
	// whose record and state are written, so the successor renews and the store
	// can revoke it. Writing it first would be the worse order — a caller could
	// be handed a key whose record had not been written yet, which is the dead
	// key above.
	//
	// A store that refuses the write is logged and the holder is still answered
	// the key it minted, the way `sight` answers a caller whose bookkeeping
	// failed: the successor is signed, its record and its state are in the store,
	// and throwing it away would take a paid term from a holder whose renewal
	// succeeded and leave the key unable to rotate again — a retry asks inside
	// the same window the handover was for, and past it the old secret is spent.
	// What is lost is the retry, so an answer lost in flight is a successor only
	// a reissue can replace.
	if err := s.store.SaveHandover(r.Context(), Handover{
		KeyID:         call.KeyID,
		SecretHash:    key.state.RenewalSecretHash,
		Key:           minted.Key,
		RenewalSecret: minted.RenewalSecret,
		Until:         now.Add(renewalHandover),
	}, now); err != nil {
		log.Printf("keyservice: keeping the answer to the rotation of the key %s: %v", call.KeyID, err)
	}
	// The subscription's own end is not written here, and that is the whole of what
	// keeps a renewal a thing the customer has paid for: the merchant's trigger is
	// what moves it (merchant.Renew), and a route that moved it for the term it
	// minted would credit the caller a term per call — the gate above would then be
	// asking a question this route had just answered for itself
	// (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers). What the successor
	// inherits is the subscription's *name*, and the term it covers is the one the
	// subscription's end already reaches.
	writeAnswer(w, Answer{Status: StatusOK, NewerKey: true, Key: minted.Key, RenewalSecret: minted.RenewalSecret})
}

// answerRotation answers a caller with the rotation its own secret already
// earned, if the store still holds it inside its window, and it answers whether
// the caller was answered — the one thing it never does is mint: a second
// rotation of one key would be a different key with a different secret, and the
// successor the holder's lost answer carried would be the one nobody could ask
// for again.
//
// The digest held with the handover is the old key's, so the window reopens for
// the secret that earned it and for no other.
func (s *Service) answerRotation(w http.ResponseWriter, ctx context.Context, call RenewalCall, now time.Time) bool {
	held, live, err := s.store.Handover(ctx, call.KeyID, now)
	if err != nil {
		fail(w, err)
		return true
	}
	if !live || subtle.ConstantTimeCompare([]byte(held.SecretHash), []byte(digest(call.RenewalSecret))) != 1 {
		return false
	}
	writeAnswer(w, Answer{Status: StatusOK, NewerKey: true, Key: held.Key, RenewalSecret: held.RenewalSecret})
	return true
}

// status is the key's own state, as the two routes answer it.
func (h holder) status() string {
	if h.revoked {
		return StatusRevoked
	}
	return StatusOK
}

// sight records a key's check (§8): the instant it last asked the service
// anything, which is the admin page's answer to whether a holder is still
// checking.
//
// A store that refuses the write is logged and not answered with a failure, the
// same way a verification's dropped personal data is: the caller's answer is
// about its key, and the key is answered whatever the bookkeeping does.
func (s *Service) sight(ctx context.Context, keyID string) {
	if err := s.store.TouchKey(ctx, keyID, s.now()); err != nil {
		log.Printf("keyservice: recording the check of the key %s: %v", keyID, err)
	}
}

// renewable answers whether a successor is to be minted now: the window is open
// once the key is inside the lead its own row names before `paid_through` — the
// lead the key carries as `renew_before_days` and its job asks from (§7, T26) —
// and a key already past its end is inside it trivially, which is what a holder
// whose job lapsed gets.
//
// Without the window the endpoint takes a term per call: every successor is a
// new key id with a new secret and therefore a throttle bucket of its own, so a
// holder — or whoever has read its secret — climbs the ladder in one instant and
// pays for none of it (TestChainedRenewalsStopAtOneTerm). With it, a successor
// ends a period past the key it replaced and is itself out of its own window
// until that period is nearly over, so one call buys one term and the next waits
// its turn.
//
// What it bounds is the rate and not the payment, and the payment is `renew`'s
// own gate rather than this function's: the record names the subscription the
// key was sold under — written by `paid`, the one mint site a merchant sale goes
// through, and inherited by the successor the endpoint writes — and `renew`
// reads it, and how it has moved since, before a key is minted at all. A record
// naming none is refused there and not passed on to this window: it is a key no
// subscription paid for — the admin page's, the free seat's, the trial's, all of
// them minted through `mint` (see `Record.Subscription`) — and a mint here would
// hand it a term nobody bought. So this window answers whether the term *after*
// the one the customer is in is the next one to sign, and the gate answers
// whether anybody paid for it: a subscription reaching one term ahead and no
// further stops the ladder on its first call, before this window is asked
// (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers).
func renewable(record Record, policy Policy, now time.Time) bool {
	lead := time.Duration(policy.RenewBeforeDays) * 24 * time.Hour
	return !now.Before(record.Expires.Add(-lead))
}

// renewalDays is the term the next key runs for: from now to the end of the term
// the customer is in, plus the row's own period, so a renewal taken inside the
// window takes no days from the customer and one taken after the key's end still
// covers a whole period from now (T26; the meaning `RequestV1.TermDays` states).
//
// It is measured from this key's own `paid_through` and never accumulated here:
// the successor covers the term that begins where this key ends, whatever the
// holder asked for before, and a term longer than the tier allows is the
// minter's refusal rather than a clamp in this file (Check 175).
//
// What it never asks for is a period the subscription has not reached, which is
// the other half of the gate's own question: the gate reads whether the money ends
// past the key in hand, and this is what keeps the successor to it. It has to be
// said here because past that point the two ends are not on one grid (D2202): a
// request measured by the row's period alone runs past what the customer bought
// wherever the subscription ends inside it — an annual subscription reaches a
// year beyond a thirty-day key, where the successor above it is a month, and a
// key whose end is days short of the subscription's (the chain arriving at the
// merchant's renewal) has days of room and not a period. The difference is not
// only the key's: a record standing for a term nobody paid for is read by
// `entitlementEnd` (merchant.go), which is what a lost renewal's subscription is
// repaired from. So the mint is capped, and the cap is a whole number of days
// like every other term here.
//
// The capped day is the round `daysBetween` makes and not the instant the money
// ends, so a call at another hour of the day than the subscription's own end asks
// for the day it lands in and the successor runs out up to 23h59m past that end
// (TestARenewalOffTheHourEndsWithinADayOfTheSubscriptionsEnd). Day-aligned calls
// are what make the two look equal, and the fixture that reads them is aligned
// (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers). Up rather than down, for
// the reason the paragraph below gives: a successor short of the money leaves the
// subscription's last hours outside every key, and the renewal after it — the
// gate still finds the money past this key — would be minted for the day those
// hours fall in. Up is what ends the chain instead: the successor's end is on or
// past the subscription's, the gate refuses the renewal after it, and the licence
// stops once, at the money.
//
// The term is in whole days, because that is the unit `RequestV1.TermDays`
// states and the minter dates the successor from its own clock and never from a
// date this side sends (§2), which is the rule the tier's ceiling rests on. So a
// renewal asked at an hour the key was not issued at ends a whole number of days
// from the asking: up to the asker's lateness inside the window past the end of
// the term it covers, and past it rather than short of it, because a successor
// short of that end is days taken from the customer and the renewal after it
// measures from the end the key in hand carries. The round is what that costs,
// and a job running at a fixed minute pays it once: the successor's end is on
// that minute, so its window opens on it too and the term after is exact.
func renewalDays(record Record, policy Policy, now time.Time, subscriptionEnd time.Time) int {
	days := daysBetween(now, covered(record, policy, now))
	if rest := daysBetween(now, subscriptionEnd); rest < days {
		days = rest
	}
	return days
}

// covered is the end the successor's term reaches when it is measured by the row
// alone: the whole period after this key's own `paid_through`, or after now for a
// key already past it. What the successor is asked for is the nearer of it and
// the subscription's own end (`renewalDays`), so this is the outer bound and the
// money is the inner one.
func covered(record Record, policy Policy, now time.Time) time.Time {
	end := record.Expires
	if end.Before(now) {
		end = now
	}
	return end.Add(policy.Term)
}
