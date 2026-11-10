// Package access holds the tree's one answer to two questions: which role a
// person holds in a repository, and whether they may do a thing there. It is
// built to docs/roles-scopes (spec.md as amended by reconciliation.md): the
// role vocabulary is the closed set viewer, operator and admin; every role is
// granted with a scope — the whole project, every repository the policy
// admits, or a list of canonical repository patterns — and the highest role
// whose scope covers a repository is the role a person holds in it.
//
// Three properties hold everywhere in this package, and each is a refusal
// rather than an accident:
//
//   - Fail closed. An address it cannot read, a role outside the closed set, a
//     scope this package did not parse and an action it does not know all
//     resolve to no role at all and to "not allowed". Nothing is guessed and
//     nothing is defaulted: a grant that does not parse grants nothing,
//     whatever wrote it (F27, the global constraints of the plan).
//   - The caller is the credential, never the config. Caller reads the
//     verified address of the Google credential the command will use. It never
//     reads the local configuration's `user:` key and never `git config
//     user.email`, because both are text a person or a coding agent can set
//     and a check built on them is a courtesy (F21). What this package cannot
//     do — and no reader should assume otherwise — is stop a person who holds
//     a project-wide IAM grant from acting on any repository with gcloud: a
//     scoped role is a CLI and console policy check, not a cloud barrier
//     (D1580, D1591).
//   - A pattern never crosses a segment. `*` matches inside one path segment
//     and never a "/"; the canonical spelling of a repository is
//     "<provider>/<owner>/<name>" (projectpolicy); and a stored grant is
//     always that spelling, so `github/acme/*` covers github/acme/app and not
//     github/acme/a/b.
//
// What this package deliberately does not do, said here so a reader does not
// look for it: it holds no licence code — a lapse is an input (Input.Lapse) and
// the limit a Subscription surface keeps while one has lapsed is ee/'s,
// installed through the seam lapse.go declares — it runs no command by itself
// (every command asks it), and it cannot check that a member exists: Fugaro has
// no directory to ask (F27).
//
// The one part of the package that touches a database is store.go (RS2): it is
// where the grants node at config/admin/access is read and conditionally
// written, through internal/rtdb — it is the only file here that imports it —
// and a reader asking which code can reach that node is reading that file.
// Everything else here is the decision over what was read: what a grant means,
// and which role a caller holds.
package access

import (
	"fmt"
	"strings"
)

// Role is the tree's closed set of roles, in the order the spec names it:
// viewer, operator, admin. The order is also the rank order — an operator
// holds everything a viewer holds — and it is what AtLeast answers.
//
// The type is this package's; internal/mobileauth re-exports it as an alias,
// so a device node's role and a grant's role are one type with one rank order
// and one parser (F1: the resolver adds no second role set).
//
// The zero Role is RoleNone, which is deliberately not a role in the set: it is
// what a caller no grant admits holds, and what a grant written with an absent
// or an unknown role carries. A caller that ignores an error therefore still
// holds nothing that admits anybody, and a writer that stores the zero value
// stores a value no reader can mistake for authority.
type Role string

const (
	// RoleNone is the empty role: no admission. It is not in Roles().
	RoleNone Role = ""
	// RoleViewer hears summaries and sees job status and spending.
	RoleViewer Role = "viewer"
	// RoleOperator is a viewer who may also talk to agents and steer jobs.
	RoleOperator Role = "operator"
	// RoleAdmin is an operator who may also manage the settings and roles
	// within its scope.
	RoleAdmin Role = "admin"
)

// Roles is the closed set, in the order the spec names it. It is the list a
// caller reads to offer a choice or to print one, and the only list ParseRole
// accepts.
func Roles() []Role { return []Role{RoleViewer, RoleOperator, RoleAdmin} }

// ParseRole returns the role s names, and whether s names one at all. It is
// exact: only the three spellings of Roles() are accepted, with no trimming
// and no case folding, because a role is written by this tree and a spelling
// it does not write is a value this package must not guess at ("Admin" is not
// admin, and neither is "administrator" — both are refused).
func ParseRole(s string) (Role, bool) {
	for _, r := range Roles() {
		if s == string(r) {
			return r, true
		}
	}
	return RoleNone, false
}

// Valid reports whether r is one of Roles().
func (r Role) Valid() bool {
	_, ok := ParseRole(string(r))
	return ok
}

// Rank is r's position in Roles(): 0 for viewer, 1 for operator, 2 for admin,
// and -1 for anything else, including RoleNone.
func (r Role) Rank() int {
	for i, x := range Roles() {
		if r == x {
			return i
		}
	}
	return -1
}

// AtLeast reports whether r holds everything min holds (operator is at least
// viewer, admin is at least operator). It is false when either role is not in
// the closed set, so an unknown or absent role is never "at least" anything:
// a caller holding nothing is not a viewer by accident.
func (r Role) AtLeast(min Role) bool {
	ri, mi := r.Rank(), min.Rank()
	return ri >= 0 && mi >= 0 && ri >= mi
}

// Grant is one stored pair: the member it names, the role, and the scope the
// role is held at (the spec's "role and scope pairs"). It is what a reader of
// the grants node holds and what this package decides against; the node's own
// shape, its reader and its writer are store.go's.
//
// No field carries a wire tag: the names the node is read and written by are
// nodeGrant's there — `user`, `role` and `scope` — and a name invented here
// would be a second spelling of one field (CLAUDE.md, "a field never changes
// meaning").
type Grant struct {
	// User is the member the grant names, in the "user:<address>" form Member
	// builds (F25: a group, a domain and a service account cannot be matched
	// against a caller by the CLI — it cannot read a caller's groups — so a
	// grant is never one of those).
	User string
	// Role is the role granted. A value outside the closed set grants nothing.
	Role Role
	// Scope is what the role covers. A scope this package did not parse
	// (Scope.Valid is false) grants nothing.
	Scope Scope
}

// Member returns the grant member of an address: "user:" and the address with
// its domain lower-cased.
//
// The domain fold is mobileauth.NormalizeEmail's rule, and it is restated here
// rather than imported because internal/mobileauth takes the Role alias from
// this package: the import can only go one way. What matters is that this is
// the function BOTH sides of the comparison go through — the member Caller
// reads from the credential and the member a grant is written with — so the
// two cannot spell one person two ways. The local part is kept byte for byte,
// as NormalizeEmail keeps it: a mail system that treats it otherwise is a fact
// this package cannot read, and folding it could merge two people.
//
// It accepts only a shape a credential can hand back: printable ASCII, exactly
// one "@", an empty-free local part and domain. Anything else is refused —
// a display-name form, whitespace, a control byte, a second "@" — and a
// member it cannot build admits nobody, which is the fail-closed half.
func Member(address string) (string, error) {
	if address == "" {
		return "", fmt.Errorf("the address is empty")
	}
	for i := 0; i < len(address); i++ {
		if address[i] < 0x21 || address[i] > 0x7e {
			return "", fmt.Errorf("the address carries %#x at offset %d: whitespace, a control byte, DEL and every non-ASCII byte are refused, because a member is a value two sides have to spell the same way", address[i], i)
		}
	}
	if strings.Count(address, "@") != 1 {
		return "", fmt.Errorf("the address %q does not carry exactly one %q", address, "@")
	}
	at := strings.IndexByte(address, '@')
	local, domain := address[:at], address[at+1:]
	if local == "" || domain == "" {
		return "", fmt.Errorf("the address %q has an empty local part or domain", address)
	}
	return "user:" + local + "@" + strings.ToLower(domain), nil
}
