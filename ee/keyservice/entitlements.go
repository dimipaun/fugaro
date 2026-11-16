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
)

// The two statuses this area writes. The wire shape's other two are other
// areas': `revoked` is the licensor withdrawing a grant (P5.1) and `expired` is
// a term that has run out, which is read from EndsAt rather than written here.
const (
	entitlementActive     = "active"
	entitlementSuperseded = "superseded"
)

// entitlementGrant is one grant as the area that makes it hands it over: a
// purchase, a trial, an operations grant and the free seat differ in their row,
// their source and who they are for, and in nothing else.
type entitlementGrant struct {
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
// What it replaces is read before the transaction and rewritten inside it, which
// is what api.go's Tx can do: a transaction reads documents by id and cannot
// query, so a candidate read inside one would be a candidate it could not look
// for. The re-read inside is the guard against the other writer: a grant that
// finds its candidate already superseded writes nothing over the newer state.
func (s *Service) grantEntitlement(ctx context.Context, grant entitlementGrant) (WireEntitlement, error) {
	row, known := DefaultPolicy(grant.Row)
	if !known {
		return WireEntitlement{}, fmt.Errorf("keyservice: the row %q is not one an entitlement is granted on", grant.Row)
	}
	replaced, err := s.supersededBy(ctx, grant, row)
	if err != nil {
		return WireEntitlement{}, err
	}
	var granted WireEntitlement
	err = s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		for _, held := range replaced {
			current, present, err := getRecord[WireEntitlement](ctx, tx, entitlementsCollection, held.ID)
			if err != nil {
				return err
			}
			if !present || current.Status != entitlementActive {
				continue
			}
			current.Status = entitlementSuperseded
			if err := putRecord(ctx, tx, entitlementsCollection, current.ID, current); err != nil {
				return err
			}
		}
		written, err := createEntitlement(ctx, tx, grant)
		if err != nil {
			return err
		}
		granted = written
		return nil
	})
	if err != nil {
		return WireEntitlement{}, err
	}
	return granted, nil
}

// supersededBy answers the grants a new one replaces, read as the state stands
// before the write.
//
// The workspace rule is D2204's: one workspace has one active entitlement, so
// the grant that lands in a workspace replaces whatever is active there. The
// organisation rule is D2203's: the first **paid** grant of an organisation ends
// its free seat, wherever in the organisation that seat is — a paid grant is a
// grant the organisation is paying for, and the seat it was given at sign-up is
// not something it keeps beside one. A trial ends neither: it is a grant the
// workspace holds, and D2203 names the paid grant alone.
func (s *Service) supersededBy(ctx context.Context, grant entitlementGrant, row Policy) ([]WireEntitlement, error) {
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
		held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection,
			Where{Field: "workspace_id", Value: *grant.WorkspaceID})
		if err != nil {
			return nil, err
		}
		collect(held)
	}
	if row.Kind == licence.KindPaid {
		held, err := recordsWhere[WireEntitlement](ctx, s.docs, entitlementsCollection,
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

// createEntitlement writes one grant inside the transaction it is handed and
// answers the record it wrote. It is the whole of what a grant is: the row is
// looked up for everything the key will carry, the term is the caller's or the
// row's, and the workspace — when the grant names one — is pointed at it.
//
// The row's own seat count is a bound and not a suggestion (D2184): a row that
// carries one seat is the free seat, and a grant asking it for another count is
// refused rather than written, because the key it would mint would hand out
// seats nobody granted.
func createEntitlement(ctx context.Context, tx Tx, grant entitlementGrant) (WireEntitlement, error) {
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
	id, err := newID()
	if err != nil {
		return WireEntitlement{}, fmt.Errorf("keyservice: the entitlement's id: %w", err)
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
	if err := putRecord(ctx, tx, entitlementsCollection, entitlement.ID, entitlement); err != nil {
		return WireEntitlement{}, err
	}
	if grant.WorkspaceID != nil {
		if err := pointWorkspaceAt(ctx, tx, entitlement); err != nil {
			return WireEntitlement{}, err
		}
	}
	return entitlement, nil
}

// pointWorkspaceAt moves one workspace's own pointer onto the grant that is now
// the active one there (D2204). The pointer is what the SPA finds a workspace's
// licence through (`workspace.entitlement_id`) and what the key routes read it
// through, so a workspace is never left pointing at a grant another one
// replaced.
//
// A grant naming a workspace the store does not hold, or one of another
// organisation, is refused: the pointer is the only way a workspace finds its
// licence, and one naming a workspace that is not there is a record nothing can
// answer for.
func pointWorkspaceAt(ctx context.Context, tx Tx, entitlement WireEntitlement) error {
	workspace, present, err := getRecord[WireWorkspace](ctx, tx, workspacesCollection, *entitlement.WorkspaceID)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("keyservice: the entitlement %s names the workspace %s, which the store does not hold", entitlement.ID, *entitlement.WorkspaceID)
	}
	if workspace.OrganisationID != entitlement.OrganisationID {
		return fmt.Errorf("keyservice: the workspace %s belongs to %s and the entitlement %s to %s",
			workspace.ID, workspace.OrganisationID, entitlement.ID, entitlement.OrganisationID)
	}
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
