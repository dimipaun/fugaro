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
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
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

// rotationSource names the record one rotation writes, and it is the store's
// memory of the rotation itself: the store keeps no key material (§7.3), so the
// fact that a key has been succeeded is a record and not a stored key, and it is
// what makes the old secret spent instead of merely superseded.
func rotationSource(keyID string) string {
	return "rotation:" + keyID
}

// renewalHandover is how long a rotated key's old secret still fetches its
// successor (D2135). It is the time a lost answer needs to be asked for again:
// the job writes the new secret and then the key, and a crash between the two —
// or a response the network dropped — leaves a holder whose only credential is
// the old secret (K4.1).
const renewalHandover = 24 * time.Hour

// handover is one rotation's answer, held against the key it succeeded.
//
// The digest is the *old* key's, so that a caller presenting anything else is
// refused: the window reopens the answer for the secret that earned it and for
// no other. The key and the secret are here because a rotation cannot be asked
// for a second time — the minter would sign a different key with a different
// secret, and the first one, which the holder may never have received, would be
// lost.
type handover struct {
	digest string
	key    string
	secret string
	until  time.Time
}

// handovers is the 24 hours after a rotation, held in this process.
//
// A durable one is the shared store's (S3.5/S3.7), like every other read the
// service makes: what a second instance costs is that a replay of a rotation it
// did not make is refused rather than answered, which is the safe half of the
// pair — no key is minted and no secret is spent by it.
type handovers struct {
	mu   sync.Mutex
	held map[string]handover
}

func newHandovers() *handovers {
	return &handovers{held: map[string]handover{}}
}

func (h *handovers) put(keyID string, held handover) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held[keyID] = held
}

// get answers the handover a key is still inside the window for. One the window
// has closed goes rather than being kept as a dead entry, and the comparison of
// the digest is the caller's: what is returned is what was held.
func (h *handovers) get(keyID string, now time.Time) (handover, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	held, known := h.held[keyID]
	if !known {
		return handover{}, false
	}
	if !now.Before(held.until) {
		delete(h.held, keyID)
		return handover{}, false
	}
	return held, true
}

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
	// window its lost answer is still reachable in.
	rotated bool
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
	for _, candidate := range records {
		if candidate.KeyID == call.KeyID && !issued {
			record, issued = candidate, true
		}
		if candidate.Source == rotationSource(call.KeyID) {
			rotated = true
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
	return holder{state: state, record: record, policy: policy, revoked: revoked, rotated: rotated}, true, nil
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
	if !s.limiter.allow("ip:" + clientIP(r)) {
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
		if held, live := s.handed.get(call.KeyID, now); live &&
			subtle.ConstantTimeCompare([]byte(held.digest), []byte(key.state.RenewalSecretHash)) == 1 {
			writeAnswer(w, Answer{Status: StatusOK, NewerKey: true, Key: held.key, RenewalSecret: held.secret})
			return
		}
		refuseRenewal(w)
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
		Seats:        key.record.Seats,
		TermDays:     renewalDays(key.record, key.policy, now),
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
	if err := s.store.SaveIssuance(r.Context(), record); err != nil && !errors.Is(err, ErrAlreadyRecorded) {
		fail(w, err)
		return
	}
	s.handed.put(call.KeyID, handover{
		digest: key.state.RenewalSecretHash,
		key:    minted.Key,
		secret: minted.RenewalSecret,
		until:  now.Add(renewalHandover),
	})
	writeAnswer(w, Answer{Status: StatusOK, NewerKey: true, Key: minted.Key, RenewalSecret: minted.RenewalSecret})
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

// renewalDays is the term the next key runs for: from now to the end of the term
// the customer is in, plus the row's own period, so a renewal taken inside the
// window takes no days from the customer and one taken after the key's end still
// covers a whole period from now (T26; the meaning `RequestV1.TermDays` states).
//
// It is measured from this key's own `paid_through` and never accumulated here:
// the successor ends one period past this key's end, whatever the holder asked
// for before, and a term longer than the tier allows is the minter's refusal
// rather than a clamp in this file.
//
// The days are rounded up, so a renewal taken at an hour the key was not issued
// at does not cost the customer the day it lands in.
func renewalDays(record Record, policy Policy, now time.Time) int {
	end := record.Expires
	if end.Before(now) {
		end = now
	}
	span := end.Add(policy.Term).Sub(now)
	return int((span + 24*time.Hour - 1) / (24 * time.Hour))
}
