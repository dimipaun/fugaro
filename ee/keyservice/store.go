// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The service's memory: the issuance record §7.3 keeps, the applications a
// person has to decide, a pending verification, and a subscription's own state
// (§5.2).
//
// Everything here is a whole record and the checks filter lists in the service.
// That is deliberate: this interface has to be small enough that the durable
// backend the deploy names can be written from it without guessing what a query
// means, and a licensor's store holds thousands of keys rather than millions —
// the scale at which "read the list" stops being the honest shape is the scale
// that writes index queries, and it is not this one. The memory implementation
// below is what a test drives; nothing in this task deploys the other.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Record is one issuance: §7.3's fields, and **no key material**. The key is
// mailed to the applicant and never kept, so the store is a list of who holds a
// licence and never a list of licences, and a leak of it cannot license anyone.
//
// Source is what minted the key — a merchant event id, a spent verification, a
// renewal's term — and it is the record's identity: §5.2's idempotency is
// "has this source been minted for?" and nothing else.
type Record struct {
	Source       string    `json:"source"`
	KeyID        string    `json:"key_id"`
	Plan         string    `json:"plan"`
	Tier         string    `json:"tier"`
	Features     []string  `json:"features"`
	Seats        int       `json:"seats"`
	Organisation string    `json:"organisation"`
	Email        string    `json:"email"`
	Issued       time.Time `json:"issued"`
	Expires      time.Time `json:"expires"`
}

// Application is one request a person has to decide (§4.5): the ones the
// checks send to the queue and the ones a person writes. Detail is the
// applicant's own text and is data on render, never markup and never a header
// (§6, §7.2).
type Application struct {
	ID           string    `json:"id"`
	Plan         string    `json:"plan"`
	Email        string    `json:"email"`
	Domain       string    `json:"domain"`
	Detail       string    `json:"detail"`
	Repositories []string  `json:"repositories"`
	Organisation string    `json:"organisation"`
	At           time.Time `json:"at"`
	// Decision and DecidedAt are what the owner made of the request and when
	// (§4.5). What makes an approval mint once is §5.2's record and not this
	// field: the decision says the request was answered, and the record says a
	// key exists.
	Decision  Decision  `json:"decision"`
	DecidedAt time.Time `json:"decided_at"`
}

// Verification is one pending link: the address an application is waiting to
// have verified, what the token will mint, and the instant it stops working
// (§4.1, §7.2).
//
// ID is the token's digest and never the token: what makes a link usable is the
// link, and a store that held tokens would be a list of live ones (§7.3).
//
// Used is what makes a token single-use, and it is written before the mint: a
// link followed twice mints once, and a mint that then fails has spent a token
// rather than left one live.
//
// Email, Domain, Organisation and Repositories are **personal data and they go**
// once the link can no longer be followed (§7.3): the service drops them when it
// spends the token for good and when the window closes, whether or not the
// applicant ever followed it, so an address at which nobody proved the mailbox
// is not kept for ever. What survives is the digest, the plan and the instant.
type Verification struct {
	ID           string    `json:"id"`
	Plan         string    `json:"plan"`
	Email        string    `json:"email"`
	Domain       string    `json:"domain"`
	Organisation string    `json:"organisation"`
	Repositories []string  `json:"repositories"`
	Expires      time.Time `json:"expires"`
	Used         bool      `json:"used"`
}

// Subscription is what the merchant's events have made of one subscription:
// the organisation it is for, the seats it holds, the end of the term it has
// paid for, and whether it is cancelled (§5.2, §5.3).
//
// Expires is the term's end and not the key's: a renewal key is minted inside
// the window before it and covers the term after it, so the two are separately
// true and Renew's arithmetic is the whole of the difference.
//
// Email is the buyer's own address as the purchase carried it, and it is kept
// because a renewal is minted months later with nothing but this record to
// deliver the key to: the event that carried the address is long gone, and a
// key minted with no address reaches nobody (§5.1, §5.3).
type Subscription struct {
	ID           string    `json:"id"`
	Organisation string    `json:"organisation"`
	Seats        int       `json:"seats"`
	TermDays     int       `json:"term_days"`
	Email        string    `json:"email"`
	At           time.Time `json:"at"`
	Expires      time.Time `json:"expires"`
	Cancelled    bool      `json:"cancelled"`
}

// Revocation is one entry of §8's revocation list: a key the licensor has
// withdrawn, the reason and the instant. Reason is the licensor's own word for
// it — a refund, a chargeback, a key that leaked — because what reaches the
// holder is the key's state and never why the licensor ended it.
//
// The list only ever grows. A revoked key is never renewed and a leak is
// reissued as a new key_id with a new entry beside the old one (§8), so nothing
// here is ever edited back, which is why Revoke refuses to overwrite an entry
// the store already holds.
type Revocation struct {
	KeyID  string    `json:"key_id"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// KeyState is what the service remembers about one issued key that the key's
// own claims cannot supply to it: the workspace it is bound to (TN2), the
// renewal secret's digest (§7, D2135) and the instant the key last asked the
// server anything (§8).
//
// The digest and never the secret. The secret is the holder's half of a renewal
// and the store is read by whoever operates the service, so the store keeping
// the secret itself would make it a list of live renewal credentials — the rule
// §7.3 already states for a verification token, applied to this one. What is
// kept is what decides whether a caller who sends the secret is the holder, and
// nothing else is derivable from it.
//
// WorkspaceID is here because a renewal mints the successor into the key's own
// workspace and a request's word for it is not proof: the claim is in the key,
// which only its holder can read, so a service that minted into the workspace
// the caller named would let whoever holds a key and its secret bind the next
// term to a workspace of their choosing. A state with no workspace is one no
// renewal can serve — the issue path that writes this record owes it (K2.3).
//
// LastSeen is zero for a key that has never called in, which is not the same as
// a key that called in long ago: the status endpoint's answer is the key's
// state and not this, and the instant is what the admin page reads when it asks
// whether a holder is still checking.
type KeyState struct {
	KeyID             string    `json:"key_id"`
	WorkspaceID       string    `json:"workspace_id"`
	RenewalSecretHash string    `json:"renewal_secret_hash"`
	LastSeen          time.Time `json:"last_seen"`
}

// Handover is one rotation's answer, kept for the window its own lost answer can
// be asked for again in (D2135, §7): the successor an old secret fetched, that
// old secret's digest and the instant the window closes.
//
// It is the one record in this store that holds key material, and it is here
// because the promise cannot be kept anywhere else. A replay lands on whichever
// instance answers it, hours after the rotation and never on the process that
// made it; what it asks for is the successor's own two lines; and the minter
// cannot be asked again, because a second mint is a *different* key with a
// different secret, and the first one is the only successor the holder's answer
// ever carried. What keeps the exception small is Until: a handover is answered
// only inside its window and is dropped when the window closes, so what the
// store holds is the rotations of the last 24 hours rather than a growing list
// of live credentials, and a durable store expires them by that instant (a TTL)
// rather than keeping a table nothing sweeps.
//
// SecretHash is the **old** key's renewal-secret digest, so the window reopens
// for the secret that earned it and for no other, and KeyID is that same old
// key's id, because that is what a replay presents.
type Handover struct {
	KeyID         string    `json:"key_id"`
	SecretHash    string    `json:"secret_hash"`
	Key           string    `json:"key"`
	RenewalSecret string    `json:"renewal_secret"`
	Until         time.Time `json:"until"`
}

// Trial is one 14-day Team trial handed out under §14: the installation it was
// issued to, the account that asked, the key it minted and when.
//
// Installation is the installation's **Firebase project id** (K2.7) and not a
// name the request carries: a project id is something a caller has to control a
// Firebase project to have, and the whole of §14's "one trial per workspace or
// account" rests on the two fields here being hard to mint twice. Account is
// the identity the site's sign-in proved (K2.4) — the account that asked, not
// one the request names. Both are required; a trial with either missing is
// ErrTrialUnnamed.
type Trial struct {
	Installation string    `json:"installation"`
	Account      string    `json:"account"`
	KeyID        string    `json:"key_id"`
	At           time.Time `json:"at"`
}

// Store is the service's memory. Save is an upsert by the record's own id
// (Source for an issuance, ID for the rest), except for an issuance the store
// already holds under that source, which is ErrAlreadyRecorded -- so two
// deliveries of one event in flight at once cannot both mint (§5.2).
//
// Nothing here is a transaction, and that is a limit this contract states
// rather than hides: every check that reads a list and then writes — §4.2's one
// free key per domain, §4.3's paid-domain refusal, §4.4's proof of control — is
// read-then-write, so two applications arriving together can both pass it, and
// a purchase's mint and its subscription are two writes rather than one. What
// bounds the first is that both applicants must be at the same domain and that
// the owner sees both in the queue; what bounds the second is `apply`, which
// finishes the state the record describes when the merchant redelivers. A
// backend that can express either pair as one write closes it, which is why the
// interface is small enough to be given one (S3.5).
//
// After Subscriptions come the things a key's life adds to §7.3's record: the
// state its renewal is decided from (§7), the revocation list (§8), the trials
// handed out (§14) and a rotation's handover (D2135). They keep the shape above
// — whole records that the service filters as it reads — and they meet that
// limit in the same two ways: an issuance and its key state are two writes, so a
// mint that dies between them leaves a key the store can revoke and cannot renew
// (the safe half of the pair), while ClaimTrial is a check and a write that
// cannot be split, because two asks arriving together would otherwise be two
// trials.
type Store interface {
	SaveIssuance(ctx context.Context, r Record) error
	Issuances(ctx context.Context) ([]Record, error)
	SaveApplication(ctx context.Context, a Application) error
	Applications(ctx context.Context) ([]Application, error)
	SaveVerification(ctx context.Context, v Verification) error
	Verifications(ctx context.Context) ([]Verification, error)
	SaveSubscription(ctx context.Context, s Subscription) error
	Subscriptions(ctx context.Context) ([]Subscription, error)
	Subscription(ctx context.Context, id string) (Subscription, bool, error)
	Revoke(ctx context.Context, r Revocation) error
	Revocation(ctx context.Context, keyID string) (Revocation, bool, error)
	Revocations(ctx context.Context) ([]Revocation, error)
	SaveKeyState(ctx context.Context, s KeyState) error
	KeyState(ctx context.Context, keyID string) (KeyState, bool, error)
	TouchKey(ctx context.Context, keyID string, at time.Time) error
	// SaveHandover and Handover take the instant they are read at, the way
	// TouchKey does: what a store holds of a handover is a window, and which side
	// of it a read is on is the service's clock rather than the store's.
	SaveHandover(ctx context.Context, h Handover, now time.Time) error
	Handover(ctx context.Context, keyID string, now time.Time) (Handover, bool, error)
	ClaimTrial(ctx context.Context, t Trial) error
	Trials(ctx context.Context) ([]Trial, error)
}

// ErrAlreadyRecorded is a source the store has already minted for. It is a
// state and not a failure: a redelivery and a re-followed link both read it,
// and what the caller does is nothing.
var ErrAlreadyRecorded = errors.New("keyservice: an issuance for that source is already recorded")

// ErrTrialAlreadyClaimed is an installation or an account that has already had
// its trial (§14). It is one error for both halves on purpose: which of the two
// matched is a fact about somebody else's account, and the answer to the ask is
// the same either way.
var ErrTrialAlreadyClaimed = errors.New("keyservice: that installation or account has already had a trial")

// ErrTrialUnnamed is a trial asked for with no installation or no account.
// Both are required, and neither has a default: the store cannot count what the
// caller did not say.
var ErrTrialUnnamed = errors.New("keyservice: a trial names the installation and the account it is for")

// ErrRevocationUnnamed is a revocation with no key_id. The list is looked up by
// the key it withdraws, so an entry under the empty string would be the one
// entry that answers every lookup of an unnamed key — a caller that sent
// nothing would be told its key is revoked, which is a project dropped to Free
// by a record nobody meant to write.
var ErrRevocationUnnamed = errors.New("keyservice: a revocation names the key it withdraws")

// ErrHandoverUnnamed is a handover with no key_id, refused for the reason a
// revocation with none is: an entry under the empty string would answer for
// every key nobody named, and this one answers with a key.
var ErrHandoverUnnamed = errors.New("keyservice: a handover names the key it succeeded")

// Memory is the store a test drives and the shape a durable one has to keep:
// the same methods and the same rules. It is safe for concurrent use because
// the service's handlers are.
type Memory struct {
	mu            sync.Mutex
	issuances     []Record
	applications  []Application
	verifications []Verification
	subscriptions map[string]Subscription
	revocations   map[string]Revocation
	keys          map[string]KeyState
	handovers     map[string]Handover
	trials        []Trial
}

func (m *Memory) SaveIssuance(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, held := range m.issuances {
		if held.Source == r.Source {
			return ErrAlreadyRecorded
		}
	}
	m.issuances = append(m.issuances, r)
	return nil
}

func (m *Memory) Issuances(_ context.Context) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.issuances...), nil
}

func (m *Memory) SaveApplication(_ context.Context, a Application) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.applications {
		if m.applications[i].ID == a.ID {
			m.applications[i] = a
			return nil
		}
	}
	m.applications = append(m.applications, a)
	return nil
}

func (m *Memory) Applications(_ context.Context) ([]Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Application(nil), m.applications...), nil
}

func (m *Memory) SaveVerification(_ context.Context, v Verification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.verifications {
		if m.verifications[i].ID == v.ID {
			m.verifications[i] = v
			return nil
		}
	}
	m.verifications = append(m.verifications, v)
	return nil
}

func (m *Memory) Verifications(_ context.Context) ([]Verification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Verification(nil), m.verifications...), nil
}

func (m *Memory) SaveSubscription(_ context.Context, s Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subscriptions == nil {
		m.subscriptions = map[string]Subscription{}
	}
	m.subscriptions[s.ID] = s
	return nil
}

func (m *Memory) Subscriptions(_ context.Context) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Subscription, 0, len(m.subscriptions))
	for _, s := range m.subscriptions {
		out = append(out, s)
	}
	return out, nil
}

func (m *Memory) Subscription(_ context.Context, id string) (Subscription, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, held := m.subscriptions[id]
	return s, held, nil
}

// Revoke records a revocation and refuses to change one it already holds: a
// refund delivered twice is one revocation, and the key is revoked from the
// first instant either way. A key the store has never issued can be revoked
// too — the entry is what the status endpoint answers from, and a key whose
// issuance is missing from the store is exactly the one whose revocation must
// not be refused.
//
// A revocation with no key_id is the one thing here that is refused, and it is
// refused rather than dropped: the entry would answer for every key nobody
// named, and a licensor who writes it has a bug in the event that carried it.
func (m *Memory) Revoke(_ context.Context, r Revocation) error {
	if r.KeyID == "" {
		return ErrRevocationUnnamed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revocations == nil {
		m.revocations = map[string]Revocation{}
	}
	if _, held := m.revocations[r.KeyID]; held {
		return nil
	}
	m.revocations[r.KeyID] = r
	return nil
}

func (m *Memory) Revocation(_ context.Context, keyID string) (Revocation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, held := m.revocations[keyID]
	return r, held, nil
}

func (m *Memory) Revocations(_ context.Context) ([]Revocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Revocation, 0, len(m.revocations))
	for _, r := range m.revocations {
		out = append(out, r)
	}
	return out, nil
}

func (m *Memory) SaveKeyState(_ context.Context, s KeyState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keys == nil {
		m.keys = map[string]KeyState{}
	}
	m.keys[s.KeyID] = s
	return nil
}

func (m *Memory) KeyState(_ context.Context, keyID string) (KeyState, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, held := m.keys[keyID]
	return s, held, nil
}

// TouchKey moves a key's last-seen and nothing else, in one write. The caller
// that records a sighting is the handler that has just compared a secret's
// digest, and it holds the digest it read rather than the one it should keep:
// a read-modify-write here would let a status call in flight put back a digest
// a rotation had already replaced.
//
// A key the store does not hold is left alone rather than given an entry. A
// sighting of a key whose secret was never recorded says nothing the store
// wants to keep, and inventing a record for it would put a key in the store
// with no digest, which is the one key state that could never be renewed.
//
// The instant only moves forward. These are the service's own clock's readings
// and the deploy's instances are separate machines, so a check that took a
// second longer to arrive can carry an earlier instant than one already
// recorded — and what the admin page reads is the last time anybody saw the
// key, which an out-of-order arrival does not change.
func (m *Memory) TouchKey(_ context.Context, keyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, known := m.keys[keyID]
	if !known || !at.After(held.LastSeen) {
		return nil
	}
	held.LastSeen = at
	m.keys[keyID] = held
	return nil
}

// SaveHandover keeps one rotation's answer, and it sweeps the windows that have
// closed as it writes: a handover is the one record here that holds key
// material, so an expired one is dropped rather than waiting for somebody to ask
// about that key again — which is exactly the case the sweep is for, since a
// holder whose answer was lost is the one who never asks a second time.
func (m *Memory) SaveHandover(_ context.Context, h Handover, now time.Time) error {
	if h.KeyID == "" {
		return ErrHandoverUnnamed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handovers == nil {
		m.handovers = map[string]Handover{}
	}
	m.sweepHandovers(now)
	m.handovers[h.KeyID] = h
	return nil
}

// Handover answers the rotation a key is still inside its window for. One the
// window has closed goes rather than being answered: what is held there is a
// successor's key and its renewal secret, and the window is what the promise
// bounds them by.
func (m *Memory) Handover(_ context.Context, keyID string, now time.Time) (Handover, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepHandovers(now)
	held, known := m.handovers[keyID]
	return held, known, nil
}

// sweepHandovers drops every handover whose window has closed. The caller holds
// the lock.
func (m *Memory) sweepHandovers(now time.Time) {
	for keyID, held := range m.handovers {
		if !now.Before(held.Until) {
			delete(m.handovers, keyID)
		}
	}
}

// ClaimTrial is §14's one trial per installation and per account, and it is the
// store that decides it: the two fields are checked and the new trial is
// appended under the same lock, so two asks arriving together cannot both find
// the store empty.
//
// An unnamed trial is refused before any of that. A claim with an empty
// installation matches nothing the store already holds, so it would be the one
// ask that always succeeds, and §14's rule would be given away by a zero value
// in a caller's request.
func (m *Memory) ClaimTrial(_ context.Context, t Trial) error {
	if t.Installation == "" || t.Account == "" {
		return ErrTrialUnnamed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, held := range m.trials {
		if held.Installation == t.Installation || held.Account == t.Account {
			return ErrTrialAlreadyClaimed
		}
	}
	m.trials = append(m.trials, t)
	return nil
}

func (m *Memory) Trials(_ context.Context) ([]Trial, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Trial(nil), m.trials...), nil
}
