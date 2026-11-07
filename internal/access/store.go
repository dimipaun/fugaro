package access

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// PathNode is where the role and scope pairs live: one node in the Realtime
// Database, under config/admin/ (D1585, as the owner confirmed it on
// 2026-11-07).
//
// The placement is the security property, so it is worth stating where a
// reader of this file will look for it. config/policy/shared carries
// `.read: TOKEN`, so every run's agent reads it, and a grant names a person's
// address and their role: stored there, each run would read the whole roster
// (F28). config/admin carries `.read: false` and `.write: false`, which the
// Realtime Database rules cascade to every child, so no client token — a run's,
// a phone's, a signed-in user's — reaches this node. The CLI reaches it over
// IAM, which bypasses the rules, and only an IAM
// roles/firebasedatabase.admin holder may write it (F22, D1592). The fence
// binds client tokens only, and D1585 says so in the open: a person holding
// roles/firebasedatabase.viewer reads the node over IAM whatever the rules say,
// which is why the roster is not a secret from the project's own members.
const PathNode = "config/admin/access"

// The node's two bounds (F27). The node is not a member of
// projectpolicy.Policy, so it inherits neither MaxPolicyBytes nor the policy's
// validation: a hand edit, a truncation or a future writer can leave anything
// here, and this reader must refuse a document it cannot hold whole rather than
// decode it. MaxNodeBytes bounds the bytes read; MaxGrants bounds the entries,
// so a node of tiny grants cannot grow without bound under the byte cap either.
const (
	// MaxNodeBytes is the largest document Decode accepts.
	MaxNodeBytes = 64 << 10
	// MaxGrants is the largest number of grants the node may hold.
	MaxGrants = 256
)

// nodeDoc is the node's wire shape: one object with one `grants` array, each
// entry a member, a role and a scope. The scope is stored as the string a
// person types — the same value ParseScope parses — so a stored grant is
// re-validated by the grammar that admitted it, and there is no second spelling
// of a scope to keep in step.
//
// The names are this file's to choose (RS1's Grant carries no wire tag on
// purpose, so a name invented there would have been a second spelling of one
// field) and they are new: `grants` is neither `operators` nor `launchers`,
// the keys the migration retires rather than reuses, and `fugaro user ls
// --json` renames the same way (CLAUDE.md, "a field never changes meaning").
type nodeDoc struct {
	Grants *[]nodeGrant `json:"grants"`
}

type nodeGrant struct {
	User  string `json:"user"`
	Role  string `json:"role"`
	Scope string `json:"scope"`
}

// Decode reads the node's document. It is strict in every direction the node
// can be wrong: an unknown field is refused (DisallowUnknownFields, so an older
// CLI refuses a node carrying a member it does not know — F27), a second JSON
// value after the first is refused, a missing grants list is refused, a
// document past MaxNodeBytes or a list past MaxGrants is refused, and every
// entry's member, role and scope is validated — the member is the canonical
// "user:<address>" form, the role is one of the closed set and the scope is one
// ParseScope accepts.
//
// It refuses rather than dropping the entry it cannot read. A grant that does
// not parse grants nothing (Effective skips it), so dropping would be safe for
// that one entry; refusing the whole node is what a load-time check owes its
// caller, because a node this reader cannot read whole is a node whose other
// entries it cannot vouch for either, and a reader that quietly used them would
// be acting on a document it only partly understood (fail closed, F27).
func Decode(raw []byte) ([]Grant, error) {
	if len(raw) > MaxNodeBytes {
		return nil, fmt.Errorf("the grants node is %d bytes; at most %d are allowed", len(raw), MaxNodeBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc nodeDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("the grants node: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("the grants node carries more than one JSON value")
	}
	if doc.Grants == nil {
		return nil, fmt.Errorf("the grants node has no grants list: write %q", "grants")
	}
	if len(*doc.Grants) > MaxGrants {
		return nil, fmt.Errorf("the grants node holds %d grants; at most %d are allowed", len(*doc.Grants), MaxGrants)
	}
	grants := make([]Grant, 0, len(*doc.Grants))
	for i, entry := range *doc.Grants {
		g, err := entry.grant()
		if err != nil {
			return nil, fmt.Errorf("the grants node, grant %d: %w", i+1, err)
		}
		grants = append(grants, g)
	}
	return grants, nil
}

// grant validates one stored entry and returns the Grant it names.
func (entry nodeGrant) grant() (Grant, error) {
	user, err := canonicalMember(entry.User)
	if err != nil {
		return Grant{}, err
	}
	role, ok := ParseRole(entry.Role)
	if !ok {
		return Grant{}, fmt.Errorf("the role %q is not one of %s", entry.Role, roleList())
	}
	scope, err := ParseScope(entry.Scope)
	if err != nil {
		return Grant{}, err
	}
	return Grant{User: user, Role: role, Scope: scope}, nil
}

// Encode renders grants as the node's document, validating each entry and both
// bounds first, so a writer cannot store a document its own reader would refuse.
// It writes the canonical forms — Member's member, Scope.String's scope — so
// the node holds one spelling of each, which is what Decode's validation
// requires back.
func Encode(grants []Grant) ([]byte, error) {
	if len(grants) > MaxGrants {
		return nil, fmt.Errorf("the grants node would hold %d grants; at most %d are allowed", len(grants), MaxGrants)
	}
	entries := make([]nodeGrant, 0, len(grants))
	for i, g := range grants {
		user, err := canonicalMember(g.User)
		if err != nil {
			return nil, fmt.Errorf("grant %d: %w", i+1, err)
		}
		if !g.Role.Valid() {
			return nil, fmt.Errorf("grant %d: the role %q is not one of %s", i+1, string(g.Role), roleList())
		}
		if !g.Scope.Valid() {
			return nil, fmt.Errorf("grant %d: the scope is not one this package parsed", i+1)
		}
		entries = append(entries, nodeGrant{User: user, Role: string(g.Role), Scope: g.Scope.String()})
	}
	raw, err := json.Marshal(nodeDoc{Grants: &entries})
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxNodeBytes {
		return nil, fmt.Errorf("the grants node would be %d bytes; at most %d are allowed", len(raw), MaxNodeBytes)
	}
	return raw, nil
}

// canonicalMember returns the stored member if it is the canonical
// "user:<address>" spelling, and refuses anything else. A grant names a person:
// a group, a domain and a service account cannot be matched against a caller by
// the CLI, which cannot read a caller's groups (F25), so a stored member that
// is not a `user:` is refused rather than read as one. The address must be the
// one Member builds — the same fold Caller's member goes through — so the two
// sides of the comparison cannot spell one person two ways.
func canonicalMember(stored string) (string, error) {
	address, ok := strings.CutPrefix(stored, "user:")
	if !ok {
		return "", fmt.Errorf("the member %q is not a user: a grant names a person, and a group, a domain and a service account cannot be matched against a caller", stored)
	}
	member, err := Member(address)
	if err != nil {
		return "", fmt.Errorf("the member %q: %w", stored, err)
	}
	if member != stored {
		return "", fmt.Errorf("the member %q is not the canonical spelling %q: a member is folded once, when it is stored", stored, member)
	}
	return member, nil
}

// roleList renders the closed set for an error message.
func roleList() string {
	names := make([]string, 0, len(Roles()))
	for _, r := range Roles() {
		names = append(names, string(r))
	}
	return strings.Join(names, ", ")
}

// String returns the canonical spelling of a scope: the keyword for a whole
// scope and the comma-joined, sorted patterns of a ScopeRepos. It is what the
// node stores and what a command prints, so two spellings of one scope are one
// string here as they are one Scope after ParseScope.
//
// A scope Valid refuses renders as the empty string, which ParseScope also
// refuses. That is the fail-closed half and it is not decoration: a scope whose
// kind is ScopeProject but which carries repositories is invalid, and a String
// that rendered it from its kind alone would spell "project" — the widest scope
// there is — for a value no reader should act on.
func (s Scope) String() string {
	if !s.Valid() {
		return ""
	}
	switch s.Kind {
	case ScopeProject:
		return "project"
	case ScopeAllRepos:
		return "all-repos"
	case ScopeRepos:
		return strings.Join(s.Repos, ",")
	}
	return ""
}

// Node is one read of the grants node: the stored grants, the ETag a write must
// carry, and whether the node exists.
//
// Found is the difference between an installation that has not migrated yet and
// one whose node is empty. An absent node means the old launchers/operators
// lists are still the source of truth (RS3 migrates them) and never "everyone":
// the zero Node grants nobody anything beyond the implicit admins the caller
// supplies, so a caller that read nothing admits nobody.
type Node struct {
	// Grants are the stored role and scope pairs, in the node's order.
	Grants []Grant
	// ETag is the node's ETag, the precondition WriteIfMatch takes. It is
	// "null_etag" for an absent node, which is a valid precondition for
	// creating it.
	ETag string
	// Found reports whether the node exists at all.
	Found bool
}

// Read reads the grants node through db, returning the grants and the node's
// ETag. It is the read half of the conditional write F23 requires: the caller
// judges against this answer and passes ETag back to WriteIfMatch, so two
// writers cannot both act on the same stale picture and each pass the
// last-admin rule.
//
// An absent node is (Found=false, the null ETag, no grants, no error): the
// installation has not migrated its lists, which is not an error and is
// emphatically not "everyone". A node that is present but unreadable — denied,
// not JSON, over a cap, carrying an unknown role or a scope that does not parse
// — is an error the caller must halt on, never read as an empty node.
func Read(ctx context.Context, db *rtdb.Client) (Node, error) {
	var raw json.RawMessage
	etag, found, err := db.GetETag(ctx, PathNode, &raw)
	if err != nil {
		return Node{}, fmt.Errorf("reading %s: %w", PathNode, err)
	}
	if !found {
		return Node{ETag: etag}, nil
	}
	grants, err := Decode(raw)
	if err != nil {
		return Node{}, fmt.Errorf("reading %s: %w", PathNode, err)
	}
	return Node{Grants: grants, ETag: etag, Found: true}, nil
}

// WriteIfMatch writes grants at the node, conditional on the ETag of the read
// the caller judged (F23): the write lands only if the node has not moved since
// that read, and a moved node is rtdb.ErrPrecondition, which the caller answers
// by re-reading and re-judging. An empty grants list is written as an empty
// list rather than a delete, so "this project has no grants" stays distinct
// from "this project has not migrated".
//
// It refuses a document its own reader would refuse (Encode) and lets
// rtdb.PutIfMatch refuse an empty ETag, which is no precondition at all.
func WriteIfMatch(ctx context.Context, db *rtdb.Client, grants []Grant, etag string) error {
	raw, err := Encode(grants)
	if err != nil {
		return err
	}
	doc := json.RawMessage(raw)
	if err := db.PutIfMatch(ctx, PathNode, etag, doc); err != nil {
		return fmt.Errorf("writing %s: %w", PathNode, err)
	}
	return nil
}
