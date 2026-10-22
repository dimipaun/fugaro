// Package mobileauth holds the mobile allowlist's entry and the rule that
// resolves it — the record that decides who may use a project from a phone
// (docs/mobile-auth: D222, the allowlist is the device node with the
// administered email and the role as fields on it; D223, the domain rule is
// applied at administration; D224, the exact-over-domain resolution runs once,
// in the CLI, at the write).
//
// It is data and one pure function. Nothing here reads or writes a database, a
// file, a credential or a clock: Resolve takes the entries it is given and the
// address it is given, and the time an entry carries is a field its writer
// filled in. That is the point of the package — the resolution rule IS the
// authorisation decision, and the rules cannot perform it (a rule cannot read
// a set of entries and order them; reconciliation §F13), so it has to be right
// in one place and be tested there. The plan (implementation-plan.md, P1)
// states the same: "a resolver that a rule cannot perform is a resolver that
// has to be right in one place and tested there."
//
// Three properties hold everywhere in this package, and each is a deliberate
// refusal rather than an accident:
//
//   - Fail closed. An address it cannot canonicalise, an entry that is not in
//     the form a writer of this package writes, a role outside the closed set,
//     and two entries that claim one key with different roles all resolve to
//     no role at all (RoleNone) and an error. Nothing is guessed, nothing is
//     defaulted, and nothing is combined: Resolve returns the role of ONE
//     entry, never the higher of two. And an entry that names the looked-up
//     address in a form this package would not write — an unfolded domain, a
//     role it does not know — refuses that address outright rather than being
//     treated as absent, so a broken exact entry can never leave somebody with
//     their domain's (possibly higher) role: a wide grant is not the fallback
//     for an unreadable narrow one.
//   - One address, one decision. NormalizeEmail is the canonical form two
//     writers agree on (the domain lower-cased, the local part kept byte for
//     byte), and it is idempotent, so two spellings that differ only in the
//     domain's case are one address and two that differ in the local part are
//     two. An address whose form this package cannot canonicalise — a display
//     name, whitespace, a second "@", a non-ASCII byte — is refused rather
//     than folded into a guess at what the writer meant.
//   - Precedence is by kind, never by rank. An exact entry overrides its
//     domain's entry (D224 and the spec's own "an exact email overrides its
//     domain's role"), including when the domain entry grants the HIGHER role:
//     writing a person down by name is what the exact entry is for. The role
//     set's order (viewer, operator, admin) is the order the spec names and it
//     is what Role.AtLeast answers; the resolver never uses it to escalate.
//
// What this package deliberately does not do, said here so a reader does not
// look for it: it does not read or write the device node at
// config/admin/devices (P2 and P3 own that), it does not apply the domain rule
// at read time (D223 puts it at administration, and no rule reads a domain),
// it does not evaluate a provider claim (D221 keeps sign-in custom-tokens-only
// and D226 has the CLI check a verified address at pairing), and it hands a
// caller no role for an entry it has not validated.
package mobileauth

import "time"

// Role is a role in the allowlist's closed set, in the order the spec names
// it: viewer, operator, admin ("Allowlist and roles", spec.md). The order is
// also the rank order — an operator holds everything a viewer holds — and it is
// what Role.AtLeast answers for the v1 question the spec states as "v1 only
// checks admin and everyone else" (D225).
//
// The zero Role is RoleNone, which is deliberately not a role in the set: it is
// what an address no entry admits resolves to, and what an entry written with
// an absent or an unknown role carries. A caller that ignores Resolve's error
// therefore still holds nothing that admits anybody, and a writer that stores
// the zero value stores a value no reader can mistake for authority.
type Role string

const (
	// RoleNone is the empty role: no admission. It is not in Roles().
	RoleNone Role = ""
	// RoleViewer hears summaries and sees job status and spending.
	RoleViewer Role = "viewer"
	// RoleOperator is a viewer who may also talk to agents and steer jobs.
	RoleOperator Role = "operator"
	// RoleAdmin is an operator who may also manage the allowlist and roles.
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
// the closed set, so an unknown or absent role is never "at least" anything.
func (r Role) AtLeast(min Role) bool {
	ri, mi := r.Rank(), min.Rank()
	return ri >= 0 && mi >= 0 && ri >= mi
}

// The children the entry takes on the device node at config/admin/devices/<uid>
// (D222, D225): the administered email and the resolved role. This package
// writes no node — `fugaro pair` (P3) is the writer and the CLI and the console
// are the readers — so the names live here, beside the entry whose fields they
// carry, and the node's shape is stated once rather than respelled by each
// reader.
const (
	// NodeFieldEmail is the entry's Address child: the address an
	// administrator wrote after checking it (the administered email).
	NodeFieldEmail = "email"
	// NodeFieldRole is the entry's Role child: the role resolved at the write
	// (D224), so nothing resolves a role at read time.
	NodeFieldRole = "role"
)

// Entry is one allowlist entry as it is written and as it is read back: an
// address, the role it carries, the time it was written and who wrote it.
//
// Address is a canonical address (NormalizeEmail) for an exact entry and a
// canonical domain (NormalizeDomain) for a domain entry. The two are different
// kinds and the resolver keeps them apart by the list they are passed in, not
// by inspecting the value: an entry in the wrong list is refused rather than
// reinterpreted, because "example.com" is a domain and never an address, and
// "dimi@example.com" is an address and never a domain.
//
// At and By are the record D222 asks for — the time the entry was written and
// who wrote it (F15: neither exists on the node today). They are carried, not
// checked: this package does not read a clock and does not know the operator
// identities, so an unwritten At is the writer's omission and not something the
// resolver can decide. No part of the resolution reads either field.
type Entry struct {
	// Address is the canonical address or domain the entry names. A value
	// that is not in canonical form is not an entry this package made, and
	// Resolve refuses to match it.
	Address string `json:"address"`
	// Role is the entry's role. A value outside the closed set admits nobody.
	Role Role `json:"role"`
	// At is when the entry was written.
	At time.Time `json:"at"`
	// By is who wrote it, as the writer recorded it.
	By string `json:"by"`
}
