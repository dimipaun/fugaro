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
}

// ErrAlreadyRecorded is a source the store has already minted for. It is a
// state and not a failure: a redelivery and a re-followed link both read it,
// and what the caller does is nothing.
var ErrAlreadyRecorded = errors.New("keyservice: an issuance for that source is already recorded")

// Memory is the store a test drives and the shape a durable one has to keep:
// the same nine methods and the two rules above. It is safe for concurrent use
// because the service's handlers are.
type Memory struct {
	mu            sync.Mutex
	issuances     []Record
	applications  []Application
	verifications []Verification
	subscriptions map[string]Subscription
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
