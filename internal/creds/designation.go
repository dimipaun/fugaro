package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

// The designation store (R26, F51, F61; the record itself is C2's, in
// internal/routing/pools.go).
//
// One principal's designation for one project lives at one node, keyed by the
// principal's own id — a person's Google subject, a service account's address —
// and the node sits in the service's own subtree, beside the credential state
// and under the same denial: the rules refuse the whole of creds/ to every run
// token (O1), and what the node holds is a pool's NAME and never a value.
//
// **Why no rule can hold who writes it** (F61): the people who administer the
// project reach this database by IAM, which bypasses the rules, so a rule here
// would bind nobody who could write anyway. The writer that matters is the
// service's own route (internal/credsvc's designation_route.go, PL4), which
// verifies the caller and refuses a pool that caller does not own; the store is
// otherwise closed, and an administrator holding the database can write here as
// they can write any project state (accepted, D1943).
//
// **What a reader may not do with the record** is treat it as an authorization
// input: every launcher of the project can read these nodes (they are display
// data, R26), and the service reads one only against the principal it verified
// on a run's slip — never against what a job says about itself.
const pathDesignations = "creds/designations"

// ErrDesignation is the refusal of a designation record this code will not read
// or write: a record whose principal is not the one its node is keyed by, a
// record that names no project or no pool, and a principal no path can be built
// from. Every refusal wraps it, so a caller has one sentinel and no refusal
// carries a value.
var ErrDesignation = errors.New("creds: the designation record is not one this service may read or write")

// DesignationPath is the node one principal's designation lives at, relative to
// the database root and without a leading slash.
//
// The one segment is escaped with budget.Key (seg), which is the spelling the
// rules compare a token's `fs` claim with, so a path and a claim about one
// principal are one escape.
func DesignationPath(principal routing.Principal) string {
	return pathDesignations + "/" + seg("principal id", principal.ID)
}

// ReadDesignation reads one principal's designation for this project. found is
// false for a principal that has designated no pool — a node that is not there
// at all (nothing, or null) — and that is not an error: "no designation" is a
// state the lookup answers "none" for (D3180), and the caller decides what it
// means.
//
// A node that is present but unreadable IS an error, and never "no
// designation": a record half understood is one the service would serve a
// fallback source over, and the two answers have to stay tellable apart. It is
// refused for a key this release does not model (every key is strict), for a
// record whose principal is not the one the node is keyed by, and for a record
// that names no project or no pool.
func ReadDesignation(ctx context.Context, db DB, principal routing.Principal) (routing.Designation, bool, error) {
	if principal.ID == "" {
		return routing.Designation{}, false, ErrDesignation
	}
	path := DesignationPath(principal)
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return routing.Designation{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if !found || len(raw) == 0 || projectpolicy.IsNull(raw) {
		return routing.Designation{}, false, nil
	}
	d, err := decodeStrict[routing.Designation](raw)
	if err != nil {
		return routing.Designation{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if err := checkDesignation(d, principal); err != nil {
		return routing.Designation{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	return d, true, nil
}

// checkDesignation is the whole of what makes a record readable at a node: it
// names the principal the node is keyed by, and it names a project and a pool.
//
// A record naming another principal is refused rather than read: the path is
// built from the principal a caller verified, so a record there that names
// somebody else was written by something that did not go through the route — and
// reading it would be a designation written in another principal's name, which
// is exactly what the route exists to refuse (R26).
func checkDesignation(d routing.Designation, principal routing.Principal) error {
	switch {
	case !d.Principal.Valid():
		return fmt.Errorf("%w: it names no principal", ErrDesignation)
	case d.Principal != principal:
		return fmt.Errorf("%w: it names another principal than the node it is stored under", ErrDesignation)
	case d.Project == "":
		return fmt.Errorf("%w: it names no project", ErrDesignation)
	case d.Pool == "":
		return fmt.Errorf("%w: it names no pool", ErrDesignation)
	}
	return nil
}

// WriteDesignation records one principal's designation, replacing whatever the
// node held. It is the only writer the service has (the route of PL4), and it
// writes the whole record rather than merging: a designation says which pool
// serves NOW, so a pool a person moved off has to go.
//
// The write is conditional on the value read (cas), so two writers of the same
// principal leave one of the two answers rather than a torn one. A node that
// already holds a record naming another principal is refused instead of
// clobbered: one principal's node is never another's to write, whatever path a
// caller worked out.
func WriteDesignation(ctx context.Context, db DB, d routing.Designation) error {
	if !d.Principal.Valid() {
		return fmt.Errorf("%w: it names no principal", ErrDesignation)
	}
	if d.Project == "" {
		return fmt.Errorf("%w: it names no project", ErrDesignation)
	}
	if d.Pool == "" {
		return fmt.Errorf("%w: it names no pool", ErrDesignation)
	}
	path := DesignationPath(d.Principal)
	_, err := cas(ctx, db, path, DefaultAttempts, func(cur routing.Designation, found bool) (routing.Designation, error) {
		if found && cur.Principal != d.Principal {
			return routing.Designation{}, fmt.Errorf("%w: it would replace a record that names another principal", ErrDesignation)
		}
		return d, nil
	})
	if err != nil {
		return fmt.Errorf("creds: writing %s: %w", path, err)
	}
	return nil
}
