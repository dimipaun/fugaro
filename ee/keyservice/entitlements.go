// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Entitlements: one record per grant, and the two rules about what a new grant
// replaces (portal plan P3.1; D2199, D2203, D2204).
//
// Every grant of every area is written here, because the rules about them are
// about the organisation rather than about the area that granted one: a
// workspace has one active entitlement (D2204), a purchase supersedes the trial
// or free seat in the workspace it lands in (D2204), and the organisation's
// first paid grant ends its free seat (D2203). An area that wrote its own record
// would have to hold all three, and the second area to hold them would hold them
// differently.
//
// Nothing here is a key. An entitlement is created before it is bound (F25) —
// the sign-up's free seat is created before the organisation it belongs to even
// exists — and the key is minted at binding, which is workspaces.go's.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// entitlementsCollection holds one document per grant, keyed by the
// entitlement's own id.
const entitlementsCollection = "entitlements"

// Where a grant came from, spelled as the wire shape's own vocabulary
// (wire_entitlements.go). The two this task grants are the free seat and the
// trial; a purchase arrives with P4.1's checkout, an operations grant with
// P5.1's console and a reseller's sale with P6.2's, and each of those calls the
// one door below with its own name.
const (
	entitlementSourcePurchase   = "purchase"
	entitlementSourceFreeSeat   = "free_seat"
	entitlementSourceTrial      = "trial"
	entitlementSourceOperations = "operations"
	entitlementSourceReseller   = "reseller"
)

// The statuses this area writes. The wire shape's fourth, `expired`, is a term
// that has run out, which is read from EndsAt rather than written here.
//
// `revoked` is a grant withdrawn — by operations (P5.1) and by the money going
// back, which is F29's revocation by entitlement (merchant.go's `withdrawn`).
// The two are one status because they are one thing: the licence stops serving
// keys, and every key minted for it is withdrawn.
const (
	entitlementActive     = "active"
	entitlementSuperseded = "superseded"
	entitlementRevoked    = "revoked"
)

// entitlementGrant is one grant as the area that makes it hands it over: a
// purchase, a trial, an operations grant and the free seat differ in their row,
// their source and who they are for, and in nothing else.
type entitlementGrant struct {
	// ID is the record's own id, for the one caller whose grant has to be the same
	// record when it is made twice: a purchase is granted from a merchant event the
	// merchant may deliver again, and the id it names is derived from the
	// transaction that bought the licence, so a delivery applied twice writes the
	// licence the first one did rather than a second one that supersedes it (P4.2,
	// D2204). Empty means a fresh id, which is every other grant.
	ID string
	// At is when the grant is written. The zero time means the term's own start,
	// which is what a grant made at the instant it was paid for is.
	At time.Time
	// OrganisationID is the organisation the grant is for, and it is the only
	// tenant of this record: nothing reads an entitlement without reading it
	// through an organisation.
	OrganisationID string
	// WorkspaceID is the workspace the grant is for, or nil for one granted
	// outside any workspace. A sign-up's free seat is that state (F6): the
	// organisation has no project yet, and the workspace is what binding makes
	// of the seat (D2196).
	WorkspaceID *string
	Source      string
	// Row is the policy row's name in policy.go, looked up and never trusted:
	// the tier, the kind, the term and the seat count are the row's, and a grant
	// that names a row which is not there is refused rather than written.
	Row   string
	Seats int
	// StartsAt is the payment's instant, and the term runs from it rather than
	// from the binding (D2197): a customer who pays and sets their project up a
	// month later holds the month they paid for.
	StartsAt time.Time
	// EndsAt is the end the caller names, or the zero time for the row's own
	// term. A purchase's end arrives from the merchant's event in days and is
	// computed by the caller; a grant whose caller names none runs the row's
	// whole term from StartsAt.
	EndsAt time.Time
}

// grantEntitlement is the portal's one way to grant, and the whole of what a
// caller has to know about the rules above: it works out what the new grant
// replaces, writes the replacement and the supersede in one transaction, and
// answers the record it wrote.
//
// What it replaces is read inside that transaction, which is what api.go's Tx
// can do: a query is a read of a collection and a transaction makes it like any
// other (api.go's Tx, and recordReader's own note on the sign-up's count). It
// has to be read there and not before — the rules above are about collections,
// and a candidate read outside the write is state another grant can move in the
// window between them: two grants of one workspace would each supersede what it
// read and leave the workspace holding two active licences, which is the pair
// D2204's rule exists to refuse. Read inside, a grant that landed in the window
// is part of what this transaction read, and the store runs it again on state
// that includes it.
//
// Every read this transaction makes is taken before its first write: the store
// is read-before-write and refuses a document read after one the same transaction
// has written (docstore's ErrReadAfterWrite), so the candidates and the workspace
// are read together and the writes follow them.
//
// A grant that names an id the store already holds is **answered** and not
// rewritten. The id is derived from the merchant's transaction (P4.2's
// `purchaseLicenceID`), so a delivery the merchant makes again after a failure
// downstream of the grant — the subscription's write, the origin, the record
// that names the licence, the notice — reaches this with the record the first
// delivery already wrote. Rewriting it would reset a licence the money has since
// taken back (a refund in that window revokes it, and the event is processed, so
// nothing revokes it a second time) or a licence the customer has since bound
// (BoundAt and RebindableAt are dropped, and the portal serves it as unbound
// again). Neither is a grant: the record is there, so what this answers is it.
func (s *Service) grantEntitlement(ctx context.Context, grant entitlementGrant) (WireEntitlement, error) {
	row, known := DefaultPolicy(grant.Row)
	if !known {
		return WireEntitlement{}, fmt.Errorf("keyservice: the row %q is not one an entitlement is granted on", grant.Row)
	}
	var granted WireEntitlement
	err := s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		if grant.ID != "" {
			held, present, err := getRecord[WireEntitlement](ctx, tx, entitlementsCollection, grant.ID)
			if err != nil {
				return err
			}
			if present {
				granted = held
				return nil
			}
		}
		superseding, err := s.supersededBy(ctx, tx, grant, row)
		if err != nil {
			return err
		}
		written, err := prepareEntitlement(ctx, tx, grant)
		if err != nil {
			return err
		}
		for _, held := range superseding {
			held.Status = entitlementSuperseded
			if err := putRecord(ctx, tx, entitlementsCollection, held.ID, held); err != nil {
				return err
			}
		}
		if err := writeEntitlement(ctx, tx, written); err != nil {
			return err
		}
		granted = written.entitlement
		return nil
	})
	if err != nil {
		return WireEntitlement{}, err
	}
	return granted, nil
}

// supersededBy answers the grants a new one replaces, read from the transaction
// that writes the new one.
//
// The workspace rule is D2204's: one workspace has one active entitlement, so
// the grant that lands in a workspace replaces whatever is active there. The
// organisation rule is D2203's: the first **paid** grant of an organisation ends
// its free seat, wherever in the organisation that seat is — a paid grant is a
// grant the organisation is paying for, and the seat it was given at sign-up is
// not something it keeps beside one. A trial ends neither: it is a grant the
// workspace holds, and D2203 names the paid grant alone.
//
// Both questions are asked of the state the transaction reads, which is the
// state it writes beside: the grants it answers are the ones it supersedes and
// not the ones a read before it happened to see.
func (s *Service) supersededBy(ctx context.Context, from recordReader, grant entitlementGrant, row Policy) ([]WireEntitlement, error) {
	replaced := make([]WireEntitlement, 0, 2)
	seen := map[string]bool{}
	collect := func(held []WireEntitlement) {
		for _, one := range held {
			if one.Status != entitlementActive || seen[one.ID] {
				continue
			}
			seen[one.ID] = true
			replaced = append(replaced, one)
		}
	}
	if grant.WorkspaceID != nil {
		held, err := recordsWhere[WireEntitlement](ctx, from, entitlementsCollection,
			Where{Field: "workspace_id", Value: *grant.WorkspaceID})
		if err != nil {
			return nil, err
		}
		collect(held)
	}
	if row.Kind == licence.KindPaid {
		held, err := recordsWhere[WireEntitlement](ctx, from, entitlementsCollection,
			Where{Field: "organisation_id", Value: grant.OrganisationID})
		if err != nil {
			return nil, err
		}
		seats := make([]WireEntitlement, 0, len(held))
		for _, one := range held {
			if one.Source == entitlementSourceFreeSeat {
				seats = append(seats, one)
			}
		}
		collect(seats)
	}
	return replaced, nil
}

// revokeEntitlement withdraws one licence: its record stops being active, and a
// licence that is not active is one no key is minted from again — the binding
// route refuses it, on the state its own transaction re-reads
// (`bindEntitlement`), so the keys a withdrawal reached are not replaced the
// moment after it ran (F29).
//
// It is the write half of a refund's revocation and not a grant's: nothing is
// superseded, which is a grant another grant replaced in its workspace, and
// nothing expires, which is read from the term. What moves is the status alone,
// so the licence keeps the term, the seats and the buyer the money was for. The
// one record written beside the status is the stop a licence a reseller sold
// accrues to (accrual.go): a revocation is what ends the days its seller owes
// for, and that instant is recorded nowhere else.
//
// The status is moved on the record the transaction reads, and the whole record
// written is that one: the caller's own read is a read from before whatever else
// reached the licence — a binding that landed, a grant that superseded it, a
// second operator's change — and a record written back from it would unmake
// that. It answers the record it wrote, which is what a door answers the caller
// with.
func (s *Service) revokeEntitlement(ctx context.Context, entitlement WireEntitlement) (WireEntitlement, error) {
	var revoked WireEntitlement
	err := s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		held, present, err := getRecord[WireEntitlement](ctx, tx, entitlementsCollection, entitlement.ID)
		if err != nil {
			return err
		}
		if !present {
			return errNoSuchEntitlement
		}
		// The instant a licence a reseller sold stops accruing, read with the
		// licence and before either write: it is the revocation's own instant,
		// it is written in the revocation's own transaction so that no accrual
		// can be read past a stop that has not landed, and the store refuses a
		// read after a write in one transaction (docstore's ErrReadAfterWrite).
		// A licence no reseller sold has no stop to write, and one that already
		// carries a stop keeps it (accrual.go's accrualStopToWrite).
		stop, err := accrualStopToWrite(ctx, tx, held, s.now())
		if err != nil {
			return err
		}
		held.Status = entitlementRevoked
		if err := putRecord(ctx, tx, entitlementsCollection, held.ID, held); err != nil {
			return err
		}
		if stop != nil {
			if err := putRecord(ctx, tx, resellerAccrualStopsCollection, stop.EntitlementID, *stop); err != nil {
				return err
			}
		}
		revoked = held
		return nil
	})
	if err != nil {
		return WireEntitlement{}, fmt.Errorf("keyservice: the licence %s was withdrawn and its record could not be written: %w", entitlement.ID, err)
	}
	return revoked, nil
}

// entitlementWrite is one grant's write, worked out before the transaction
// writes anything: the record, and the workspace whose pointer moves onto it
// when the grant names one.
//
// It is two steps and not one for the store's own reason: a transaction reads
// before it writes (docstore's ErrReadAfterWrite), and the workspace a grant
// names is read by the transaction writing the grant — a caller with a read of
// its own to make first (grantEntitlement's supersedes) takes both reads before
// it writes either, which one step reading the workspace after the first put
// could not do.
type entitlementWrite struct {
	entitlement WireEntitlement
	workspace   *WireWorkspace
}

// prepareEntitlement is a grant's read half: the record it would write and the
// workspace that record names. It reads and it writes nothing.
func prepareEntitlement(ctx context.Context, tx Tx, grant entitlementGrant) (entitlementWrite, error) {
	entitlement, err := entitlementRecord(grant)
	if err != nil {
		return entitlementWrite{}, err
	}
	written := entitlementWrite{entitlement: entitlement}
	if entitlement.WorkspaceID != nil {
		workspace, err := licensedWorkspace(ctx, tx, entitlement)
		if err != nil {
			return entitlementWrite{}, err
		}
		written.workspace = &workspace
	}
	return written, nil
}

// writeEntitlement is the write half: the record, and the pointer of the
// workspace it names.
func writeEntitlement(ctx context.Context, tx Tx, written entitlementWrite) error {
	if err := putRecord(ctx, tx, entitlementsCollection, written.entitlement.ID, written.entitlement); err != nil {
		return err
	}
	if written.workspace == nil {
		return nil
	}
	return pointWorkspaceAt(ctx, tx, *written.workspace, written.entitlement)
}

// createEntitlement is both halves in order, for the one caller whose transaction
// writes nothing before the grant: the sign-up's own (freeSeatOnOrgCreated). A
// caller that has already read in the transaction it hands over calls the two
// halves itself, in this order, and reads before it writes.
func createEntitlement(ctx context.Context, tx Tx, grant entitlementGrant) (WireEntitlement, error) {
	written, err := prepareEntitlement(ctx, tx, grant)
	if err != nil {
		return WireEntitlement{}, err
	}
	if err := writeEntitlement(ctx, tx, written); err != nil {
		return WireEntitlement{}, err
	}
	return written.entitlement, nil
}

// entitlementRecord is one grant as the record it is written as. It is the whole
// of what a grant is: the row is looked up for everything the key will carry, the
// term is the caller's or the row's, and the workspace is the one the grant named.
//
// The row's own seat count is a bound and not a suggestion (D2184): a row that
// carries one seat is the free seat, and a grant asking it for another count is
// refused rather than written, because the key it would mint would hand out
// seats nobody granted.
func entitlementRecord(grant entitlementGrant) (WireEntitlement, error) {
	row, known := DefaultPolicy(grant.Row)
	if !known {
		return WireEntitlement{}, fmt.Errorf("keyservice: the row %q is not one an entitlement is granted on", grant.Row)
	}
	if grant.OrganisationID == "" {
		return WireEntitlement{}, errors.New("keyservice: an entitlement with no organisation is one no member of it can reach")
	}
	if grant.Seats < 1 {
		return WireEntitlement{}, fmt.Errorf("keyservice: an entitlement of the %s row for %d seat(s) is one nobody can use", row.Name, grant.Seats)
	}
	if row.Seats != 0 && grant.Seats != row.Seats {
		return WireEntitlement{}, fmt.Errorf("keyservice: the %s row carries its own %d seat(s) and this grant names %d (D2184)", row.Name, row.Seats, grant.Seats)
	}
	if grant.StartsAt.IsZero() {
		return WireEntitlement{}, errors.New("keyservice: a term runs from the payment (D2197) and this grant names no instant it was paid at")
	}
	ends := grant.EndsAt
	if ends.IsZero() {
		ends = grant.StartsAt.Add(row.Term)
	}
	if !ends.After(grant.StartsAt) {
		return WireEntitlement{}, fmt.Errorf("keyservice: the term of the %s grant ends at %s, before it starts at %s", row.Name, ends, grant.StartsAt)
	}
	at := grant.At
	if at.IsZero() {
		at = grant.StartsAt
	}
	id := grant.ID
	if id == "" {
		minted, err := newID()
		if err != nil {
			return WireEntitlement{}, fmt.Errorf("keyservice: the entitlement's id: %w", err)
		}
		id = minted
	}
	entitlement := WireEntitlement{
		ID:             id,
		OrganisationID: grant.OrganisationID,
		WorkspaceID:    grant.WorkspaceID,
		Source:         grant.Source,
		Row:            row.Name,
		Tier:           row.Tier,
		Kind:           row.Kind,
		Seats:          grant.Seats,
		StartsAt:       grant.StartsAt.UTC(),
		EndsAt:         ends.UTC(),
		Status:         entitlementActive,
		// A key that never checks the server is the air-gapped row and nothing
		// else (D2206), and the row is where that is stated: a caller that could
		// name it could mint a key revocation never reaches (F28).
		AirGapped: row.CheckInterval == nil,
		CreatedAt: at.UTC(),
	}
	return entitlement, nil
}

// licensedWorkspace reads the workspace one grant names, inside the transaction
// that writes the grant and before anything is written to it.
//
// A grant naming a workspace the store does not hold, or one of another
// organisation, is refused: the pointer is the only way a workspace finds its
// licence, and one naming a workspace that is not there is a record nothing can
// answer for.
func licensedWorkspace(ctx context.Context, tx Tx, entitlement WireEntitlement) (WireWorkspace, error) {
	workspace, present, err := getRecord[WireWorkspace](ctx, tx, workspacesCollection, *entitlement.WorkspaceID)
	if err != nil {
		return WireWorkspace{}, err
	}
	if !present {
		return WireWorkspace{}, fmt.Errorf("keyservice: the entitlement %s names the workspace %s, which the store does not hold", entitlement.ID, *entitlement.WorkspaceID)
	}
	if workspace.OrganisationID != entitlement.OrganisationID {
		return WireWorkspace{}, fmt.Errorf("keyservice: the workspace %s belongs to %s and the entitlement %s to %s",
			workspace.ID, workspace.OrganisationID, entitlement.ID, entitlement.OrganisationID)
	}
	return workspace, nil
}

// pointWorkspaceAt moves one workspace's own pointer onto the grant that is now
// the active one there (D2204). The pointer is what the SPA finds a workspace's
// licence through (`workspace.entitlement_id`) and what the key routes read it
// through, so a workspace is never left pointing at a grant another one
// replaced.
func pointWorkspaceAt(ctx context.Context, tx Tx, workspace WireWorkspace, entitlement WireEntitlement) error {
	workspace.EntitlementID = &entitlement.ID
	return putRecord(ctx, tx, workspacesCollection, workspace.ID, workspace)
}

// freeSeatOnOrgCreated is D2199's free seat: the grant every organisation a
// sign-up creates is given, written inside the sign-up's own transaction, before
// the organisation exists, so that an organisation cannot exist without it
// (orgs.go's OrgCreatedHook).
//
// It is granted outside any workspace (F6): a sign-up names an organisation and
// no project, so there is no workspace for the seat to belong to yet and binding
// is what makes one (D2196). The instant it starts at is the acceptance of the
// terms, which is the sign-up's own clock — the hook is handed a transaction and
// not the service, and the two would be the same instant anyway.
func freeSeatOnOrgCreated(ctx context.Context, tx Tx, org WireOrganisation) error {
	if org.Terms == nil {
		return errors.New("keyservice: an organisation created without an accepted version of the terms is not one the free seat may be granted to (D2199)")
	}
	_, err := createEntitlement(ctx, tx, entitlementGrant{
		At:             org.Terms.AcceptedAt,
		OrganisationID: org.ID,
		Source:         entitlementSourceFreeSeat,
		Row:            PolicyFreeSeat,
		Seats:          1,
		StartsAt:       org.Terms.AcceptedAt,
	})
	return err
}

func init() {
	registerOrgCreated("the free seat's entitlement", freeSeatOnOrgCreated)
}
