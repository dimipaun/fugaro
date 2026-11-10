package access

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// adminlist.go is D1940's derived list: the roster's project-scoped admins,
// as a bucket object the intake reads.
//
// Why a derived object and not the roster itself: the roster is a database
// node behind IAM (store.go), and the intake may hold no database role at all
// — that is the decision's point, and reading the roster directly would mean
// a database-wide read for a service whose whole job is one narrow write. So
// `fugaro update` reads the roster, derives the list, and publishes it where
// the intake can read it with an object read: D1940, F27 and F32 together.
//
// Two properties are the list's, and both are refusals rather than accidents:
//
//   - It holds project-scoped admins alone. A role is held at a scope, and the
//     scope is what the intake's caller is admitted for: a project admin holds
//     the whole installation, while an all-repos or repository-scoped admin
//     holds their repositories and not the shared prefix, and viewer and
//     operator hold no shared write at all (D1935). The filter is in Admins,
//     it is fail closed, and a grant whose role or scope this package did not
//     parse is on no list.
//   - It lives outside every path a launcher can write. The list is what
//     admits a caller to the shared prefix, so a caller who could write it
//     would admit themselves; PathAdminList's placement is checked against the
//     launchers' own IAM condition by TestAdminListPathNotLauncherWritable.
//
// The list lags a role change until the next `fugaro update` runs, which is
// the decision's accepted cost (D1940, "the list lags a role change until
// update runs"): it is a derivation taken at one moment, and the run that
// takes it says so.
//
// Nothing here carries a secret and nothing here is one: an address and an
// account ID are identifiers the project's own members already hold.

// PathAdminList is the bucket object the derived list is written to and the
// intake reads, relative to the runs bucket.
//
// The name is the security boundary and not a filing choice: the launchers'
// objectUser grant is conditioned on runs/, tokens/ and issues/ (gcp's
// LauncherBucketCondition), the job accounts write runs/, cache/ and locks/,
// and the build accounts write builds/, so an object under admin/ is one no
// launcher, job or build credential can write. The intake's own account is
// granted a read on this key alone (DS2.2), which is what keeps the intake's
// IAM surface an object read rather than the roster's database read.
const PathAdminList = "admin/intake-admins.json"

// The object's two bounds, in store.go's shape and for its reason: the object
// is read by another service, and a truncated or hand-written one must be
// refused rather than decoded. The list is derived from a roster of at most
// MaxGrants grants, so MaxAdmins is that bound and the byte cap is the roster
// node's own.
const (
	// MaxAdminListBytes is the largest document ParseAdminList accepts.
	MaxAdminListBytes = 64 << 10
	// MaxAdmins is the largest number of entries the list may hold.
	MaxAdmins = MaxGrants
)

// Admin is one entry of the list: the address the intake compares its caller
// against, and — where one is known — the caller's Google account ID.
//
// The address is the roster's own spelling, which is Member's: the local part
// byte for byte and the domain folded, so the intake's comparison and the
// roster's key cannot spell one person two ways. It is the address alone and
// not the "user:" member form, because the field is an address and the intake
// holds an address (a verified email claim); the member form is the roster's
// spelling of a grant, and a wire shape that carried it would invite a reader
// to compare it against a bare address.
//
// Sub is the Google account ID and is optional, which is the plan's "email and
// sub where known": the roster (F27) is keyed by email and holds no account ID,
// so a list derived from it carries none, and the field is what a derivation
// that knows one writes and a reader that holds a caller's `sub` compares
// against (R10 resolves a policy's owner and allowed_users to one at grant
// time). An absent sub matches nobody: a reader looking a caller up by account
// ID finds no entry, rather than an entry that stands for every caller who has
// none.
type Admin struct {
	// Email is the member's canonical address, as Member folds it.
	Email string `json:"email"`
	// Sub is the member's Google account ID: the numeric identifier the `sub`
	// claim carries (U2), and empty when the derivation did not know one.
	Sub string `json:"sub,omitempty"`
}

// adminListDoc is the object's wire shape: one object with one `admins` array.
//
// The field is a pointer for store.go's reason: an absent list and an empty
// one are different documents, and a reader must be able to tell them apart. A
// list that holds no project admin is written as [] — the document says "no
// one", which is a fact — while a document with no `admins` field at all is
// one this reader does not understand.
type adminListDoc struct {
	Admins *[]Admin `json:"admins"`
}

// Admins derives the list from the roster: every grant that is an admin of the
// whole project, as one entry each, sorted by address.
//
// The failures are silent skips rather than errors, and that is the roster's
// own rule (fail closed): a grant that does not parse grants nothing, so a
// person it names is not an admin and is not on the list. A caller that wants
// to know what the roster held reads it with Read, which refuses an
// unreadable node.
//
// Every entry it derives is one EncodeAdminList accepts, which is a property of
// this function and not of the roster: the two disagree about exactly one
// address — Member admits a colon in the local part, and an entry refuses one
// because it is the IAM member spelling — so a grant holding one would
// otherwise make the derivation produce a list its own writer refuses, and the
// object would keep the list of an earlier update for good. The entry is
// validated here, where it is derived, and a person whose address the list
// cannot carry is one the list does not hold.
func Admins(grants []Grant) []Admin {
	out := make([]Admin, 0, len(grants))
	for _, g := range grants {
		// The role first, then the scope, and both are exact: an unknown role
		// is not RoleAdmin and a scope this package did not parse is not
		// ScopeProject, so neither reaches the list by accident.
		if g.Role != RoleAdmin || g.Scope.Kind != ScopeProject || !g.Scope.Valid() {
			continue
		}
		member, err := canonicalMember(g.User)
		if err != nil {
			continue
		}
		entry := Admin{Email: strings.TrimPrefix(member, "user:")}
		if err := entry.validate(); err != nil {
			continue
		}
		if slices.ContainsFunc(out, func(a Admin) bool { return a.Email == entry.Email }) {
			// One person, one entry: two grants make the same admin, and the
			// list is a list of people.
			continue
		}
		out = append(out, entry)
	}
	slices.SortFunc(out, compareAdmins)
	return out
}

// compareAdmins orders the list by address, so the object's bytes are a
// function of the roster and not of the order its grants happened to be in: a
// list written twice from one roster is one object, and a diff of two
// installations' lists is a diff of their admins.
func compareAdmins(a, b Admin) int { return strings.Compare(a.Email, b.Email) }

// EncodeAdminList renders the list as the object's document, validating every
// entry and both bounds first, so a writer cannot publish a document its own
// reader would refuse.
//
// It sorts the entries and refuses a duplicate address: the object is a set of
// people, and two entries for one address is a document this file never writes
// — a reader that accepted one would have to decide which of the two it meant.
func EncodeAdminList(list []Admin) ([]byte, error) {
	if len(list) > MaxAdmins {
		return nil, fmt.Errorf("the admin list would hold %d admins; at most %d are allowed", len(list), MaxAdmins)
	}
	entries := make([]Admin, 0, len(list))
	for i, a := range list {
		if err := a.validate(); err != nil {
			return nil, fmt.Errorf("admin %d: %w", i+1, err)
		}
		if slices.ContainsFunc(entries, func(e Admin) bool { return e.Email == a.Email }) {
			return nil, fmt.Errorf("admin %d: the address %s is in the list twice; the list holds each person once", i+1, a.Email)
		}
		entries = append(entries, a)
	}
	slices.SortFunc(entries, compareAdmins)
	raw, err := json.Marshal(adminListDoc{Admins: &entries})
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxAdminListBytes {
		return nil, fmt.Errorf("the admin list would be %d bytes; at most %d are allowed", len(raw), MaxAdminListBytes)
	}
	return raw, nil
}

// ParseAdminList reads the object's document. It is strict in every direction
// the object can be wrong, for the reason store.go's Decode is: the intake
// holds no roster and no database role (D1940), so this document is the whole
// of what it can check, and a reader that dropped the entry it could not read
// would be acting on a list it only partly understood.
//
// It therefore refuses an unknown field (DisallowUnknownFields, so a list
// written by a newer release is refused rather than partly read), a field
// spelled any other way or written twice (refuseAdminListSpellings: two
// spellings of a key are two children of a Realtime Database node and one
// matched field to encoding/json), a second JSON value after the first, a
// document past MaxAdminListBytes or a list past MaxAdmins, a missing list,
// and every entry whose address is not the canonical one Member builds or
// whose account ID is not an account ID.
func ParseAdminList(raw []byte) ([]Admin, error) {
	if len(raw) > MaxAdminListBytes {
		return nil, fmt.Errorf("the admin list is %d bytes; at most %d are allowed", len(raw), MaxAdminListBytes)
	}
	if err := refuseAdminListSpellings(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc adminListDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("the admin list: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("the admin list carries more than one JSON value")
	}
	if doc.Admins == nil {
		return nil, fmt.Errorf("the admin list has no %q list: it is written on every update, and a document without one vouches for nothing", "admins")
	}
	if len(*doc.Admins) > MaxAdmins {
		return nil, fmt.Errorf("the admin list holds %d admins; at most %d are allowed", len(*doc.Admins), MaxAdmins)
	}
	list := make([]Admin, 0, len(*doc.Admins))
	for i, a := range *doc.Admins {
		if err := a.validate(); err != nil {
			return nil, fmt.Errorf("the admin list, admin %d: %w", i+1, err)
		}
		list = append(list, a)
	}
	return list, nil
}

// validate refuses an entry that is not one this file writes: an address that
// is not the canonical spelling, and an account ID that is present but is not
// one. It is EncodeAdminList's and ParseAdminList's one check, so a document
// this package publishes is one it reads back.
func (a Admin) validate() error {
	// The bare address and never the IAM member spelling: the intake matches
	// this list against the claim its caller's ID token verified, so an entry
	// written as "user:<address>" — the roster's own spelling of a grant —
	// admits nobody. Refusing it here names the entry where the writer sees it,
	// rather than publishing a list that reads as right and admits no one;
	// internal/policy's ValidVerifierAccounts refuses one for the same reason,
	// over a list matched against the same kind of claim.
	if strings.Contains(a.Email, ":") {
		return fmt.Errorf("the address %q is an IAM member spelling: an entry is the bare address a Google ID token's email claim carries, never the \"user:\" form a grant is stored in", a.Email)
	}
	member, err := Member(a.Email)
	if err != nil {
		return fmt.Errorf("the address %q: %w", a.Email, err)
	}
	if canonical := strings.TrimPrefix(member, "user:"); canonical != a.Email {
		return fmt.Errorf("the address %q is not the canonical spelling %q: an address is folded once, when it is derived, and a list of two spellings of one person admits neither", a.Email, canonical)
	}
	for i := 0; i < len(a.Sub); i++ {
		if a.Sub[i] < '0' || a.Sub[i] > '9' {
			return fmt.Errorf("the account ID %q carries %q: a Google sub is the numeric account identifier, so an entry with anything else in it names no account this intake could compare a caller against", a.Sub, a.Sub[i])
		}
	}
	return nil
}

// refuseAdminListSpellings walks the document and refuses a field outside this
// file's schema, spelled any other way or written twice. It is store.go's
// refuseOtherSpellings over the object's own shape, for its reason exactly
// (encoding/json matches a key case-insensitively and lets the later of two
// spellings win), and it walks the entries because an entry's fields are where
// a role or a scope would arrive if a writer published the roster itself
// instead of the list derived from it.
func refuseAdminListSpellings(raw []byte) error {
	fields, err := objectFields(json.NewDecoder(bytes.NewReader(raw)))
	if err != nil {
		return fmt.Errorf("the admin list: %w", err)
	}
	for i, f := range fields {
		if f.name != "admins" {
			return fmt.Errorf("the admin list carries the field %q: the object has one field, %q, spelled exactly", f.name, "admins")
		}
		if i > 0 {
			return fmt.Errorf("the admin list carries the field %q twice: a field is written once", f.name)
		}
		var entries []json.RawMessage
		if err := json.NewDecoder(bytes.NewReader(f.value)).Decode(&entries); err != nil {
			return fmt.Errorf("the admin list: %q is not a list of admins: %w", "admins", err)
		}
		for _, entry := range entries {
			entryFields, err := objectFields(json.NewDecoder(bytes.NewReader(entry)))
			if err != nil {
				return fmt.Errorf("the admin list: an admin is not one object: %w", err)
			}
			seen := map[string]bool{}
			for _, ef := range entryFields {
				switch ef.name {
				case "email", "sub":
				default:
					return fmt.Errorf("the admin list: an admin carries the field %q: an admin has the address and, where it is known, the account ID, spelled exactly", ef.name)
				}
				if seen[ef.name] {
					return fmt.Errorf("the admin list: an admin carries the field %q twice: an admin writes each of its fields once", ef.name)
				}
				seen[ef.name] = true
			}
		}
	}
	return nil
}
