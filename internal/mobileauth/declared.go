package mobileauth

import "strings"

// Declared is one entry of the DECLARED allowlist: the list an operator writes
// down on their own machine — the local project config `internal/localcfg`
// reads — and that `fugaro project policy publish` seeds the device node from
// (D231). It is the declaration the node is a seed and a record of, which is
// the other direction from every other type in this package: Resolve answers
// what a set of entries grants, and this is what an operator says should be
// granted, in the shape a human writes.
//
// Three fields and no more, because each one is a fact the node's node carries
// (mobileauth's NodeFieldEmail and NodeFieldRole, and the uid the node is keyed
// by):
//
//   - UID is the Firebase Auth uid the device node at config/admin/devices is
//     keyed by — the pairing's own uid, which `fugaro pair` mints. It is
//     OPTIONAL, and an entry that carries none declares a grant without naming
//     a device: such an entry seeds nothing and admits nobody, because the node
//     is what the rules read (D231) and a device node is keyed by a uid (a
//     declaration that names an address is a record of a person, not the
//     person's node).
//   - Address is what the entry names: one administered email for an exact
//     entry, or a whole domain for a domain entry. The kind is read off the
//     value the way pairAllowlistEntries reads the node's own entries — an "@"
//     makes it an address, its absence a domain — so a declaration and the node
//     it seeds cannot disagree about which kind an entry is.
//   - Role is the role the entry grants. It is required, and it is not
//     defaulted: an entry naming a person or a domain with no role this package
//     knows is refused by the reader rather than given one, because the role a
//     person is admitted with is an authority and the safe reading of a value
//     nobody can parse is none.
//
// It carries no At and no By: the declaration is a file an operator edits, and
// the node's record of when an entry was written is the publish's own clock
// (the writer records it, not the file). Nothing here reads a clock or a file —
// the parsing is internal/localcfg's and the seeding is internal/cli's — so this
// package stays what it is: data, and one pure rule about it.
type Declared struct {
	UID     string `yaml:"uid,omitempty"`
	Address string `yaml:"address"`
	Role    Role   `yaml:"role"`
}

// NamesAnAddress reports whether the declared entry names one address rather
// than a whole domain, which is the presence of an "@" and nothing else. It is
// the same test pairAllowlistEntries reads the node's entries with, so the
// declaration and the node agree about the kind of a value by construction.
func (d Declared) NamesAnAddress() bool { return strings.Contains(d.Address, "@") }

// Canonical returns the entry's address in the canonical form this package
// writes — NormalizeEmail for an entry that names one address, NormalizeDomain
// for one that names a domain — and refuses a declaration written any other
// way, including a canonical value written differently ("Dimi@Example.com",
// "Example.com").
//
// Both halves matter. A value that cannot be normalised is refused because it
// names nobody this package can identify; a value that normalises to something
// ELSE is refused because the declaration is the seed of a node, and a node
// holding one spelling while the file holds another is two records of one grant
// that a later reader would have to reconcile. The refusal names the form to
// write, so the fix is one edit.
func (d Declared) Canonical() (string, error) {
	if d.NamesAnAddress() {
		got, err := NormalizeEmail(d.Address)
		if err != nil {
			return "", err
		}
		if got != d.Address {
			return "", &NotCanonicalError{Want: got, Kind: "address"}
		}
		return got, nil
	}
	got, err := NormalizeDomain(d.Address)
	if err != nil {
		return "", err
	}
	if got != d.Address {
		return "", &NotCanonicalError{Want: got, Kind: "domain"}
	}
	return got, nil
}

// NotCanonicalError is Canonical's refusal of a value that is a form this
// package reads but not the form it writes: the declaration says
// "Dimi@Example.com" and the node it seeds would hold "Dimi@example.com", which
// is one grant with two spellings. Want is the spelling to write and Kind says
// which normaliser produced it, because the fix is one edit and the sentence is
// the whole of it; the value itself is not repeated here — the reader of the
// file that carries it names it (internal/localcfg's validation prints the
// entry's position and its value), and the refusal an operator reads carries
// both halves without this type holding a second copy of one.
type NotCanonicalError struct {
	Want string
	Kind string
}

func (e *NotCanonicalError) Error() string {
	return "it is not the canonical form this package writes: write it as " + e.Kind + " " + e.Want
}
