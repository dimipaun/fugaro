package mobileauth

import (
	"errors"
	"fmt"
	"strings"
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
//  2. The exact entries that name that canonical address are judged. An entry
//     names it when its own normalised form equals it; an entry is only READ
//     when its stored form is already canonical (NormalizeEmail(Address) ==
//     Address) and its role is in the closed set. If the usable entries agree on
//     one role, that role is the answer and the domain entries are never
//     consulted. If they disagree, the answer is ErrAmbiguous and the domain
//     entries are again never consulted. And if an entry names the person but
//     cannot be used — its stored form is not canonical, or its role is not one
//     this package knows — the answer is ErrNotAllowed, also without consulting
//     the domain entries, because falling through would grant the person their
//     domain's role, which is a decision the exact entry's writer did not make.
//     In every one of those three cases the rule is the same: an exact entry
//     that names a person and cannot be used refuses that person, and never
//     hands the decision to a broader kind.
//  3. Otherwise the domain entries are judged the same way, on the domain of the
//     canonical address (the part after its "@"), which must equal the entry's
//     domain exactly. One agreed role is the answer; disagreement is
//     ErrAmbiguous; none, or an entry that names the domain and cannot be used,
//     is ErrNotAllowed.
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
	exactSay := judge(exact, want, NormalizeEmail)
	if exactSay.err != nil || exactSay.role != RoleNone {
		return exactSay.role, exactSay.err
	}
	if exactSay.unusable != "" {
		return RoleNone, fmt.Errorf("%w: an exact entry names %q and cannot be used because %s, so its domain's entries are not consulted", ErrNotAllowed, want, exactSay.unusable)
	}
	// want is canonical — NormalizeEmail refused every address that is not — so
	// it carries exactly one "@" and the text after it is a validated domain:
	// this is DomainOf's answer for an address it accepts, read directly so the
	// domain branch has no unreachable refusal of its own. An address NormalizeEmail
	// refused never reaches this line.
	domain := want[strings.IndexByte(want, '@')+1:]
	domainSay := judge(domains, domain, NormalizeDomain)
	if domainSay.err != nil || domainSay.role != RoleNone {
		return domainSay.role, domainSay.err
	}
	if domainSay.unusable != "" {
		return RoleNone, fmt.Errorf("%w: a domain entry for %q names it and cannot be used because %s", ErrNotAllowed, domain, domainSay.unusable)
	}
	return RoleNone, fmt.Errorf("%w: %q is not named by an exact entry and its domain %q is not a domain entry", ErrNotAllowed, want, domain)
}

// judgment is what one kind of entry says about one key.
type judgment struct {
	// role is the role the usable entries agree on, RoleNone when none of them
	// grants one this package knows.
	role Role
	// unusable is why a naming entry could not be used, and "" when none failed
	// to be: it is the clause Resolve builds its refusal from. A judgment can
	// carry both a role and a reason (one entry naming a key is broken and
	// another is well formed), and Resolve reads the role first.
	unusable string
	// err is ErrAmbiguous when two judged entries disagree.
	err error
}

// judge reads the entries of one kind that name want — one canonical address
// for the exact list, one canonical domain for the domain list — and reports
// what they say. normalize is the kind's own normaliser, so an entry is judged
// by the kind of the list it was handed in and never by a guess at what its
// value looks like: "example.com" in the exact list names no address, and
// "dimi@example.com" in the domain list names no domain.
//
// An entry NAMES the key when its normalised form equals want. Two things stop
// a naming entry from granting anything, and each is recorded rather than
// repaired:
//
//   - Its Address is not already the canonical form (e.g. "Dimi@Example.com"
//     for the address "Dimi@example.com"). It is not an entry this package
//     wrote, so it is not read as one.
//   - Its Role is not in the closed set.
//
// What matters is that such an entry still NAMES the key. Naming is what stops
// Resolve from consulting a broader kind: an exact entry that names a person and
// cannot be used refuses that person rather than handing them their domain's
// role, which is exactly the fail-open this shape exists to close (a hand-edited
// or corrupted exact entry must not leave somebody with a domain's grant).
//
// The boundary of naming is where the normaliser fails: an entry whose value
// cannot be normalised at all (" dimi@example.com", "Dimi Paun <…>") names
// nobody, because this package cannot say whose entry it is — it is not a
// spelling of the key that was looked up, it is a value with no reading. Such an
// entry is skipped rather than allowed to refuse, so a value nobody can parse
// cannot be used to lock a whole list out.
func judge(entries []Entry, want string, normalize func(string) (string, error)) judgment {
	var say judgment
	for _, e := range entries {
		normal, err := normalize(e.Address)
		if err != nil || want == "" || normal != want {
			continue
		}
		if e.Address != want {
			say.unusable = "its stored form is not the canonical one this package writes"
			continue
		}
		role, ok := ParseRole(string(e.Role))
		if !ok {
			say.unusable = "it carries no role this package knows"
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
