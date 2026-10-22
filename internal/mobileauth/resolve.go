package mobileauth

import (
	"errors"
	"fmt"
)

// ErrNotAllowed is the answer for an address no entry admits: no exact entry
// names it, no domain entry holds its domain, or the address is not a form
// NormalizeEmail can canonicalise. It is the ordinary answer, not a failure —
// a person asking whether they may use a project is allowed to hear no.
var ErrNotAllowed = errors.New("mobileauth: no allowlist entry admits the address")

// ErrAmbiguous is the answer for an address two entries of the SAME kind claim
// with different roles — two exact entries for one address, or two domain
// entries for one domain. It is refused rather than resolved, and the refusal is
// the whole reason this error exists: the only way to pick one of two roles for
// one key is to pick a winner, and every rule for picking one (the higher role,
// the later entry) is a rule nobody wrote, that would grant authority to
// somebody on the strength of a duplicate. So the address is admitted by
// nothing until the duplicate is gone.
//
// A duplicate that agrees — two entries for one key carrying the SAME role — is
// not ambiguous: there is one answer, and it is returned. That is deliberate,
// because a re-written entry (D224's re-role is a write to the node) is exactly
// this shape.
var ErrAmbiguous = errors.New("mobileauth: the allowlist entries for the address disagree about the role")

// Resolve returns the role the allowlist grants to address, where exact holds
// the entries that name one address and domains holds the entries that name a
// whole domain. The rule is D224's and the spec's: an exact entry overrides its
// domain's role.
//
// It is a pure function of its three arguments: it reads no database, no file
// and no clock, and it writes nothing. The caller reads the entries — from the
// node, from a declaration, from a test fixture — and this decides.
//
// What it does, in order, and what each step refuses:
//
//  1. The address is canonicalised with NormalizeEmail. An address that cannot
//     be canonicalised admits nobody (ErrNotAllowed): that is the fail-closed
//     half of the normalisation, and the reason an entry and a lookup can never
//     be two spellings of one person.
//  2. The exact entries that name that canonical address are judged. An exact
//     entry is only read if its own Address is already canonical
//     (NormalizeEmail(Address) == Address); anything else is not an entry this
//     package wrote, and it names nobody. If the usable entries agree on one
//     role, that role is the answer and the domain entries are never consulted.
//     If they disagree, the answer is ErrAmbiguous and the domain entries are
//     again never consulted. And if an exact entry names the person but carries
//     no role this package knows, the answer is ErrNotAllowed — also without
//     consulting the domain entries, because falling through would grant the
//     person their domain's role, which is a decision the exact entry's writer
//     did not make. In every one of those three cases the rule is the same: an
//     exact entry that cannot be used refuses the person it names, and never
//     hands the decision to a broader kind.
//  3. Otherwise the domain entries are judged the same way, on the domain of the
//     canonical address (the part after its "@"), which must equal the entry's
//     domain exactly. One agreed role is the answer; disagreement is
//     ErrAmbiguous; none, or a match that carries no known role, is
//     ErrNotAllowed.
//
// Two things it deliberately does not do. It does not combine roles: the answer
// is always the role of one entry, never the highest of several, so a domain
// entry can never raise what an exact entry grants. And it does not match a
// domain by suffix: an entry for "example.com" does not admit
// "dimi@sub.example.com", because that precedence — the more specific domain
// wins — is a rule this plan does not write, and the safe default for a rule
// nobody wrote is that it grants nothing.
func Resolve(exact, domains []Entry, address string) (Role, error) {
	want, err := NormalizeEmail(address)
	if err != nil {
		return RoleNone, fmt.Errorf("%w: %s", ErrNotAllowed, err)
	}
	if say := judge(exact, want, canonicalAddress); say.err != nil || say.role != RoleNone {
		return say.role, say.err
	} else if say.matched {
		return RoleNone, fmt.Errorf("%w: an exact entry names %q and carries no role this package knows, so its domain's entries are not consulted", ErrNotAllowed, want)
	}
	domain, ok := DomainOf(want)
	if !ok {
		return RoleNone, fmt.Errorf("%w: %q has no domain", ErrNotAllowed, want)
	}
	say := judge(domains, domain, canonicalDomain)
	if say.err != nil || say.role != RoleNone {
		return say.role, say.err
	}
	if say.matched {
		return RoleNone, fmt.Errorf("%w: a domain entry for %q carries no role this package knows", ErrNotAllowed, domain)
	}
	return RoleNone, fmt.Errorf("%w: %q is not named by an exact entry and its domain %q is not a domain entry", ErrNotAllowed, want, domain)
}

// judgment is what one kind of entry says about one key.
type judgment struct {
	// role is the role the judged entries agree on, RoleNone when none of them
	// grants one this package knows.
	role Role
	// matched is whether any entry of this kind names the key at all —
	// whether it carried a usable role or not.
	matched bool
	// err is ErrAmbiguous when two judged entries disagree.
	err error
}

// judge reads the entries of one kind that name want — one canonical address
// for the exact list, one canonical domain for the domain list — and reports
// what they say. canonical is the kind's own normaliser, so an entry is judged
// by the kind of the list it was handed in and never by a guess at what its
// value looks like: "example.com" in the exact list names no address, and
// "dimi@example.com" in the domain list names no domain.
//
// An entry whose Address is not in canonical form is not an entry this package
// wrote, so it names nothing and is skipped; an entry whose Role is not in the
// closed set names the key and grants nothing, which is what matched beside a
// zero role says. Both are refusals rather than repairs: a value this package
// would never write is one it must not interpret, and the caller decides whether
// a match that granted nothing may fall through to a broader kind (for the exact
// list it may not; see Resolve).
func judge(entries []Entry, want string, canonical func(string) string) judgment {
	var say judgment
	for _, e := range entries {
		if want == "" || canonical(e.Address) != want {
			continue
		}
		say.matched = true
		role, ok := ParseRole(string(e.Role))
		if !ok {
			continue
		}
		if say.role == RoleNone {
			say.role = role
			continue
		}
		if role != say.role {
			return judgment{err: fmt.Errorf("%w: one entry names %s and another names %s", ErrAmbiguous, say.role, role)}
		}
	}
	return say
}

// canonicalAddress returns address when it is already the canonical form
// NormalizeEmail would produce, and "" otherwise. "" is not an address any
// lookup asks for (an empty address is refused), so an entry that is not in
// canonical form — one this package did not write — matches nothing rather than
// being folded into a match.
func canonicalAddress(address string) string {
	canonical, err := NormalizeEmail(address)
	if err != nil || canonical != address {
		return ""
	}
	return address
}

// canonicalDomain is canonicalAddress for a domain entry.
func canonicalDomain(domain string) string {
	canonical, err := NormalizeDomain(domain)
	if err != nil || canonical != domain {
		return ""
	}
	return domain
}
