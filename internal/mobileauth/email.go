package mobileauth

import (
	"fmt"
	"strings"
)

// maxAddress is the longest address this package accepts (RFC 5321's 254). It
// is a bound rather than a reading of the standard: an allowlist entry longer
// than this is not an address two writers would agree on.
const maxAddress = 254

// maxDomain is the longest domain this package accepts (RFC 1035's 253).
const maxDomain = 253

// NormalizeEmail returns the canonical form of an address: the domain
// lower-cased, the local part kept byte for byte, nothing else changed.
//
// This is the single rule two writers must agree on (D222's write and D231's
// declaration both name a person), and it is deliberately the smallest rule
// that makes one address one string:
//
//   - The domain is case-insensitive and is folded, so "Dimi@Example.COM" and
//     "Dimi@example.com" are one address.
//   - The local part is NOT folded, so "Dimi@example.com" and
//     "dimi@example.com" are two addresses and one is never silently the
//     other: the local part is case-sensitive in the standard, and a mail
//     system that treats it otherwise is a fact this package cannot read.
//     A writer that wants one entry per person writes the address as the
//     person's own provider spells it.
//   - Nothing else is touched. There is no trimming, no whitespace collapsing
//     and no Unicode folding, so the function is idempotent
//     (NormalizeEmail(NormalizeEmail(a)) == NormalizeEmail(a)) and two
//     spellings that differ anywhere but the domain's case stay two addresses.
//
// What it refuses, and why refusing is the safe half: a display-name form
// ("Dimi Paun <dimi@example.com>") is refused rather than parsed into an
// address, because parsing one is how a name becomes an admission; so are
// whitespace and every control byte, a second "@", a domain literal
// ("dimi@[192.0.2.1]"), a quoted or dotted-atom-violating local part
// ("dimi..paun@example.com", ".dimi@example.com"), a domain with a bad label,
// a single-label domain ("localhost"), a trailing dot, a wildcard
// ("*.example.com") and every non-ASCII byte. Each is a form whose canonical
// address two writers could spell differently, and an entry that admits
// somebody is the last place to guess. An address that cannot be canonicalised
// admits nobody: Resolve returns ErrNotAllowed for it.
func NormalizeEmail(address string) (string, error) {
	if address == "" {
		return "", fmt.Errorf("the address is empty")
	}
	if len(address) > maxAddress {
		return "", fmt.Errorf("the address is %d bytes long, over the %d this package accepts", len(address), maxAddress)
	}
	if i, ok := firstNonPrintableASCII(address); !ok {
		return "", fmt.Errorf("the address %q carries %#x at offset %d: whitespace, a control, DEL and every non-ASCII byte are refused, because two writers would have to agree on a normalisation form this package does not implement", address, address[i], i)
	}
	if i, ok := firstStructuralByte(address, addressPunctuation); !ok {
		return "", fmt.Errorf("the address %q carries %q at offset %d: a display-name form, a quoted local part, a domain literal and a comment are refused rather than parsed, because parsing one is how a name becomes an admission", address, address[i], i)
	}
	if strings.Count(address, "@") != 1 {
		return "", fmt.Errorf("the address %q does not carry exactly one %q", address, "@")
	}
	at := strings.IndexByte(address, '@')
	local, domain := address[:at], address[at+1:]
	if local == "" {
		return "", fmt.Errorf("the address %q has no local part", address)
	}
	if len(local) > 64 {
		return "", fmt.Errorf("the local part of %q is %d bytes long, over the 64 this package accepts", address, len(local))
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return "", fmt.Errorf("the local part of %q is not a dot-atom: a leading dot, a trailing dot and two dots in a row are each a second spelling of an address, and this package refuses to decide which one a writer meant", address)
	}
	folded, err := NormalizeDomain(domain)
	if err != nil {
		return "", fmt.Errorf("the address %q: %w", address, err)
	}
	return local + "@" + folded, nil
}

// addressPunctuation is the set of bytes an address may not carry at all: the
// display-name and comment forms ("Dimi Paun <…>", "(work)"), a quoted local
// part, a domain literal, a group and a source route.
const addressPunctuation = `<>",;:()[]\`

// NormalizeDomain returns the canonical form of a domain: folded to lower case,
// with nothing else changed.
//
// A domain entry is a whole domain and matches the address whose domain IS that
// domain — never a subdomain of it and never a suffix of it. That is the
// reading D223 needs ("A whole domain is allowed"): the plan applies the domain
// rule at administration, so nothing at read time has to be a pattern match,
// and a suffix rule would be a second precedence rule nobody wrote and nobody
// would review. So there is no wildcard form: "*.example.com" is refused
// rather than read as "example.com", and an entry for "example.com" does not
// admit "dimi@sub.example.com".
//
// It refuses a single-label domain ("localhost"), a label over 63 bytes, a
// label that starts or ends with "-", a label outside [a-z0-9-] — so an
// underscore, a wildcard, a space and every non-ASCII byte (an internationalised
// domain, whose Unicode and punycode spellings two writers could disagree
// about) are all refused — a trailing dot, an empty label ("a..b"), a domain
// over 253 bytes, and a last label that is all digits (a numeric TLD belongs to
// an address literal, not to a domain entry).
func NormalizeDomain(domain string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("the domain is empty")
	}
	if len(domain) > maxDomain {
		return "", fmt.Errorf("the domain is %d bytes long, over the %d this package accepts", len(domain), maxDomain)
	}
	if i, ok := firstNonPrintableASCII(domain); !ok {
		return "", fmt.Errorf("the domain %q carries %#x at offset %d: a non-ASCII domain is refused rather than folded, because its Unicode and punycode spellings are two domains this package cannot reconcile", domain, domain[i], i)
	}
	folded := strings.ToLower(domain)
	labels := strings.Split(folded, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("the domain %q is a single label: a domain entry names a whole domain, so an entry with no dot is refused", domain)
	}
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("the domain %q has an empty label: a leading dot, a trailing dot and two dots in a row are each a second spelling of a domain", domain)
		}
		if len(label) > 63 {
			return "", fmt.Errorf("the domain %q has a %d-byte label, over the 63 this package accepts", domain, len(label))
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("the domain %q has a label that starts or ends with %q", domain, "-")
		}
		for i := 0; i < len(label); i++ {
			b := label[i]
			if (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' {
				continue
			}
			return "", fmt.Errorf("the domain %q carries %q in a label: only letters, digits and %q are a label, so a wildcard, an underscore and every other byte are refused", domain, b, "-")
		}
	}
	if allDigits(labels[len(labels)-1]) {
		return "", fmt.Errorf("the domain %q ends in an all-digit label, which is an address literal and not a domain entry", domain)
	}
	return folded, nil
}

// DomainOf returns the domain of an address: the part after its "@", and
// whether the address is one this package would claim a domain for.
//
// It accepts a canonical address only — NormalizeEmail(address) == address —
// because its answer is the key a domain entry is looked up by, and an address
// that is not canonical has no domain this package will claim: DomainOf says ok
// == false for a padded, unfolded or malformed value rather than handing back a
// domain that matches no canonical domain entry. "@b.com",
// "dimi@Example.COM" and "dimi@example.com " are all refused, so a caller that
// trusts ok never has to canonicalise again first. Resolve is the caller that
// matters: it passes its own canonical address here, which is why its domain
// entries are compared against a folded domain.
func DomainOf(address string) (string, bool) {
	canonical, err := NormalizeEmail(address)
	if err != nil || canonical != address {
		return "", false
	}
	// NormalizeEmail accepted the address, so it carries exactly one "@" with a
	// non-empty local part and a domain that passed NormalizeDomain: the index
	// below cannot miss and the part after it is never empty.
	return address[strings.IndexByte(address, '@')+1:], true
}

// firstNonPrintableASCII returns the offset of the first byte of s outside
// printable ASCII, and whether s is printable ASCII throughout.
func firstNonPrintableASCII(s string) (int, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return i, false
		}
	}
	return 0, true
}

// firstStructuralByte returns the offset of the first byte of s in set, and
// whether s carries none of them.
func firstStructuralByte(s, set string) (int, bool) {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(set, s[i]) >= 0 {
			return i, false
		}
	}
	return 0, true
}

// allDigits reports whether s is non-empty and made of digits only.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
