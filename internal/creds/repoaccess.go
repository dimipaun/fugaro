package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// RepoAccess is the record at creds/repoaccess/<slug>: what one repository's
// runs MAY be served by, per craft and per grade, and the subscription
// credentials a run of it may name with --token (C1 (d), R4).
//
// # A new record under a new name, and why
//
// The repository auth record changes subject (F5): a repository has no auth
// mode any more (D3060), and what the service narrows a call by is the access
// its craft resolves to. By the rule that a field never changes meaning, a
// record whose subject changed is a NEW record with a new name and its own
// version (R4): creds/repoauth/<slug> goes on saying what it says — an older
// image's calls are still narrowed by it — and this one is what a call that
// declares its craft is served by (CM6). Through release 1 `fugaro update`
// writes both; release 2 stops writing the old one and deletes it.
//
// # What it says, and what it does not
//
// It names what a repository's runs may be served by, as the old record does,
// and never what one run resolved to (R4): for each craft and each grade the
// access the project wrote, or — where the project wrote none — ONE CREDENTIAL
// PER MODEL FAMILY (F43, D3061 as amended): the first the project declares and
// admits for that repository that reaches a Claude model, and the first for each
// provider, so that for any one model exactly one credential is named and
// nothing moves from a subscription to a paid key without a pool someone wrote.
// A craft the project wrote nothing for and the record therefore leaves out is a
// craft whose calls the service refuses rather than serves by a guess.
//
// The cascade's pool is named like any other pool, with the marker it carries
// (D3064 as amended): a subscription member of it is served as the cascade
// serves it today although the spread switch is off, and nothing else in this
// record has that effect — a plain pool still obeys the switch (CM36).
//
// # The reach is the union over the crafts (R4)
//
// A call declares its craft, and the service serves it what this record names
// for that craft — so a run's whole reach is the union over its crafts, which is
// what Reach answers. That is a statement about the record and not about a run:
// no run of a repository is served more than the union, and a single call is
// served no more than its own craft's entry. The union is here because the
// callers that have to answer "may this run reach that credential at all" — a
// doctor line, the bundle's readiness check (CM15), a refusal that names what
// the repository holds — ask it of the repository and not of one call.
//
// # No value, ever
//
// No member of this record is a credential's value and none may ever be (G18):
// every name here is a credential's or a pool's name, as the auth record's are
// an auth's and an account's. The closed shape below is what keeps it that way,
// and the read refuses a member this release does not model rather than half
// understanding it.
//
// # Who may read it
//
// It lives under creds/, the subtree the database rules deny every run identity
// (O1, D136), and it is reached through a DB by two callers: the operator's CLI,
// which writes it (CM5), and the service, which decides from it (CM6). A token
// that could WRITE it could widen a repository's reach to the whole bundle,
// which is the boundary internal/creds/boundary_test.go pins over the code half.
type RepoAccess struct {
	// Version is the format's version (RepoAccessVersion). It is a member
	// rather than a constant because the writer and the reader are two binaries,
	// and a reader that meets a version it does not know refuses the record
	// rather than reading the members it recognises as the whole of it.
	Version int `json:"version"`
	// Crafts maps each craft (config.CraftNames) to what its calls may be served
	// by: a set of names, in name order and once each, because the write puts
	// every list through the same cleaning (cleanRefs) and keeps no order a
	// caller passed. The order that matters is a pool's own members', and that
	// is the pool's (config.PoolMembers) and not this list's. A craft this
	// record does not carry is one the project wrote nothing for and the
	// migration found nothing that reaches: its calls are refused.
	Crafts map[string][]AccessRef `json:"crafts"`
	// Grades maps each grade (config.GradeNames) to what a task that names it
	// may be served by (F38). A grade's access is the project's alone (D3062,
	// F38): a repository's file may not write one, and a grade the project
	// writes nothing for is a grade whose rung is not raised.
	Grades map[string][]AccessRef `json:"grades"`
	// Tokens are the subscription credentials a run of this repository may name
	// with --token (D3069), each spelled as a credential's name is
	// (config.DefaultClaudeAccount for the unsuffixed Claude subscription, the
	// spelling the bundle's own tier-A entry carries). Sorted and without
	// repeats. It is a list of what MAY be named and not of what was: the run
	// declares the one it chose (CM6, F44).
	Tokens []string `json:"tokens"`
}

// AccessRef is one credential or pool a repository's runs may be served by: the
// name the project's config speaks, whether that name is a pool's, and — for a
// pool — whether it carries the cascade's marker (D3064 as amended).
//
// It is a name and two flags and never a value, and the flags are the two facts
// a reader cannot work out from the name alone: a credential and a pool live in
// one namespace (config.ValidatePools), and the marker is what lets a
// subscription be served with the spread switch off (CM36).
type AccessRef struct {
	Name    string `json:"name"`
	Pool    bool   `json:"pool,omitempty"`
	Cascade bool   `json:"cascade,omitempty"`
}

const (
	// pathRepoAccess is the family under creds/ these records live in, one node
	// per repository slug. It is a family of its own and not a member of the
	// auth record's: the two are written and read by different releases of the
	// same binaries through release 1 (R4).
	pathRepoAccess = "creds/repoaccess"

	// RepoAccessVersion is the version this release writes and reads.
	RepoAccessVersion = 1
)

// PathRepoAccess is one repository's record. It panics on an empty slug, as
// every path builder in this package does: a slug reaches it from a task or a
// spec, never from a request.
func PathRepoAccess(slug string) string {
	return pathRepoAccess + "/" + seg("slug", slug)
}

// The record's refusals, as sentinels so a caller can answer without reading a
// message. Neither carries a value or a caller's own text.
var (
	// ErrNoRepoAccess is a slug that names no repository: no path is built from
	// it (an empty segment would collapse onto another node).
	ErrNoRepoAccess = errors.New("creds: the repository access record names no repository")
	// ErrRepoAccessMalformed is a record that could not be written as it
	// stands: a craft or a grade this vocabulary does not hold, an entry that
	// names nothing or names one name twice, a credential carrying the cascade's
	// marker, or a token entry that is no credential's name — an answer no
	// reader could decide from.
	ErrRepoAccessMalformed = errors.New("creds: a repository access record must name, for each craft and grade, the credentials or pools a run may be served by, once each")
	// ErrRepoAccessVersion is a record written by a release this one does not
	// know. It is refused rather than half read: the members this release
	// recognises are not the whole of a document it did not write.
	ErrRepoAccessVersion = errors.New("creds: the repository access record was written by another version of fugaro")
)

// ReadRepoAccess reads one repository's record. found=false is a repository no
// update has recorded, and it is not an error: the caller decides what an
// absent record means, and for the service that is a refusal of every call that
// declares its craft (CM6) — a call the record does not name is never served.
//
// A node that is present but unreadable IS an error, and so is a version this
// release does not know: a record half understood is one that could serve a
// repository a credential its own configuration does not name.
func ReadRepoAccess(ctx context.Context, db DB, slug string) (RepoAccess, bool, error) {
	if slug == "" {
		return RepoAccess{}, false, ErrNoRepoAccess
	}
	path := PathRepoAccess(slug)
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return RepoAccess{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if !found || len(raw) == 0 {
		return RepoAccess{}, false, nil
	}
	rec, err := decodeStrict[RepoAccess](raw)
	if err != nil {
		return RepoAccess{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if rec.Version != RepoAccessVersion {
		return RepoAccess{}, false, fmt.Errorf("creds: reading %s: %w (version %d)", path, ErrRepoAccessVersion, rec.Version)
	}
	rec.normalize()
	return rec, true, nil
}

// PutRepoAccess writes one repository's record whole, replacing whatever the
// node held. It is a whole replacement and not a merge, for the reason the auth
// record's write is: the record says what the repository's configuration says
// NOW, so an access the project dropped has to go — a merge would leave the
// service serving a credential nothing names any more, which is the reach this
// record exists to bound.
//
// The write is conditional on the value read (cas), so a concurrent update of
// the same repository leaves one of the two answers rather than a torn one, and
// a node whose version member is not this release's — one a newer fugaro wrote —
// is refused instead of clobbered. The names are put in order here rather than
// left to the caller, so the node is byte-identical for the same configuration
// however the project listed it, and the version is the writer's for the same
// reason: the format is this release's, and a caller does not get to name
// another one.
func PutRepoAccess(ctx context.Context, db DB, slug string, rec RepoAccess) error {
	if slug == "" {
		return ErrNoRepoAccess
	}
	next := RepoAccess{
		Version: RepoAccessVersion,
		Crafts:  map[string][]AccessRef{},
		Grades:  map[string][]AccessRef{},
	}
	for craft, refs := range rec.Crafts {
		if !slices.Contains(config.CraftNames, craft) {
			return ErrRepoAccessMalformed
		}
		clean, ok := cleanRefs(refs)
		if !ok {
			return ErrRepoAccessMalformed
		}
		next.Crafts[craft] = clean
	}
	for grade, refs := range rec.Grades {
		if !slices.Contains(config.GradeNames, grade) {
			return ErrRepoAccessMalformed
		}
		clean, ok := cleanRefs(refs)
		if !ok {
			return ErrRepoAccessMalformed
		}
		next.Grades[grade] = clean
	}
	if len(rec.Tokens) > 0 {
		rows := slices.Clone(rec.Tokens)
		for _, t := range rows {
			if !accessRefName(t) {
				return ErrRepoAccessMalformed
			}
		}
		slices.Sort(rows)
		for i := 1; i < len(rows); i++ {
			if rows[i-1] == rows[i] {
				return ErrRepoAccessMalformed
			}
		}
		next.Tokens = rows
	}
	path := PathRepoAccess(slug)
	_, err := cas(ctx, db, path, DefaultAttempts, func(cur RepoAccess, found bool) (RepoAccess, error) {
		if found && cur.Version != RepoAccessVersion {
			return RepoAccess{}, fmt.Errorf("%w (version %d)", ErrRepoAccessVersion, cur.Version)
		}
		return next, nil
	})
	if err != nil {
		return fmt.Errorf("creds: writing %s: %w", path, err)
	}
	return nil
}

// RepoAccess reads one repository's record as the service's own account, which
// is the Store method a call's admission is narrowed through.
func (s *RTDBStore) RepoAccess(ctx context.Context, slug string) (RepoAccess, bool, error) {
	return ReadRepoAccess(ctx, s.db(), slug)
}

// AccessForCraft is what that craft's calls may be served by, and whether the
// record names anything for it. A craft the record does not carry is one whose
// calls are refused (CM6) rather than served by another craft's entry.
func (r RepoAccess) AccessForCraft(craft string) ([]AccessRef, bool) {
	refs, ok := r.Crafts[craft]
	return slices.Clone(refs), ok
}

// AccessForGrade is what a task that names that grade may be served by, and
// whether the record names anything for it (F38).
func (r RepoAccess) AccessForGrade(grade string) ([]AccessRef, bool) {
	refs, ok := r.Grades[grade]
	return slices.Clone(refs), ok
}

// NamesToken is whether a run of this repository may name that credential with
// --token (D3069). It is a method rather than a set the caller builds so that
// the comparison is spelled once, in the package that owns the record: the name
// reaches it from the caller's own header, so an equality a caller could get
// wrong is one the boundary would be wrong with.
func (r RepoAccess) NamesToken(name string) bool {
	return slices.Contains(r.Tokens, name)
}

// Reach is every credential and pool the record's crafts name, sorted and
// without repeats: a run's whole reach, which is the union over its crafts
// (R4: a call declares its craft, and the service serves it what the record
// names for that craft). The grades and the tokens are not in it: R4 states the
// reach as the union over the crafts, and AccessForGrade and NamesToken are the
// two reads that answer a grade's entry and a name a run chose.
func (r RepoAccess) Reach() []string {
	var out []string
	for _, refs := range r.Crafts {
		for _, ref := range refs {
			if !slices.Contains(out, ref.Name) {
				out = append(out, ref.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// normalize puts a record read off the wire into the shape a writer leaves:
// an absent map is an empty one, so a caller reads "no craft" and "no crafts at
// all" the same way (a node written by an older release of this format, or by
// hand, may hold null).
func (r *RepoAccess) normalize() {
	if r.Crafts == nil {
		r.Crafts = map[string][]AccessRef{}
	}
	if r.Grades == nil {
		r.Grades = map[string][]AccessRef{}
	}
}

// cleanRefs is one craft's or grade's list as the node holds it: name order,
// once each, and refused where it names nothing at all — a craft present with
// an empty list is a shape no reader could decide from, and the way to say "the
// project wrote nothing for this craft" is to leave the craft out.
func cleanRefs(refs []AccessRef) ([]AccessRef, bool) {
	if len(refs) == 0 {
		return nil, false
	}
	out := slices.Clone(refs)
	for _, ref := range out {
		if !accessRefName(ref.Name) {
			return nil, false
		}
		if ref.Cascade && !ref.Pool {
			return nil, false
		}
	}
	slices.SortFunc(out, func(a, b AccessRef) int {
		return strings.Compare(a.Name, b.Name)
	})
	for i := 1; i < len(out); i++ {
		if out[i-1].Name == out[i].Name {
			return nil, false
		}
	}
	return out, true
}

// accessRefName reports whether a name in this record is one a credential or a
// pool could have: config's own rule for a reference, and config.DefaultClaudeAccount
// beside it.
//
// The default account is admitted here although it is not a credential's name
// (config.ValidCredentialRef refuses it, and credentialNameProblem refuses it as
// a definition): it is the account axis's spelling of the UNSUFFIXED Claude
// subscription token, which is what the bundle's tier-A entry carries and what
// the auth record already writes for it. A craft whose run goes out on that
// token has to be nameable, and this is the one spelling the service, the record
// and the bundle share.
func accessRefName(name string) bool {
	if name == config.DefaultClaudeAccount {
		return true
	}
	return config.ValidCredentialRef(name) == ""
}
