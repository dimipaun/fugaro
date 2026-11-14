package mobileauth

import (
	"errors"
	"strings"
	"testing"
)

// TestDeclaredKindIsTheValueAndNotAField: which kind an entry names is read off
// the address itself, the way pairAllowlistEntries reads the node's own
// entries, so a declaration and the node it seeds cannot disagree about whether
// an entry is a person or a domain.
func TestDeclaredKindIsTheValueAndNotAField(t *testing.T) {
	for _, tc := range []struct {
		in    string
		exact bool
	}{
		{"dimi@example.com", true},
		{"example.com", false},
		{"lattica.com", false},
	} {
		if got := (Declared{Address: tc.in}).NamesAnAddress(); got != tc.exact {
			t.Errorf("NamesAnAddress(%q) = %v, want %v", tc.in, got, tc.exact)
		}
	}
}

// TestDeclaredCanonicalRefusesEveryOtherSpelling is the fail-closed half of the
// declaration: a value this package cannot read names nobody, and a value it
// reads but does not write ("Dimi@Example.com", "Example.com") is refused
// rather than folded, because the declaration is the seed of a node and one
// grant with two spellings is two records of it. The refusal names the spelling
// to write, since unlike a node's child this value comes from a file its writer
// is editing.
func TestDeclaredCanonicalRefusesEveryOtherSpelling(t *testing.T) {
	for _, ok := range []string{"dimi@example.com", "example.com", "sub.example.com"} {
		if got, err := (Declared{Address: ok}).Canonical(); err != nil || got != ok {
			t.Errorf("Canonical(%q) = %q, %v; want the value itself and no error", ok, got, err)
		}
	}
	for _, bad := range []string{
		"",
		"Dimi@Example.com",  // an address whose domain is not folded
		"Example.com",       // a domain that is not folded
		" dimi@example.com", // a value no normaliser can read
		"Dimi Paun <dimi@example.com>",
		"dimi@example",      // a single-label domain
		"@example.com",      // no local part
		"example.com.",      // a trailing dot
		"dimi@@example.com", // two at-signs
		"dimi@example.com ", // a trailing space
	} {
		got, err := (Declared{Address: bad}).Canonical()
		if err == nil {
			t.Errorf("Canonical(%q) = %q and no error: a declaration is refused rather than repaired", bad, got)
			continue
		}
		var nc *NotCanonicalError
		if errors.As(err, &nc) && (nc.Want == "" || nc.Kind == "") {
			t.Errorf("Canonical(%q): a refusal about the form names no spelling to write and no kind (%+v)", bad, nc)
		}
	}
}

// TestDeclaredCanonicalNamesTheFormToWrite: the one refusal whose fix is an
// edit says what to edit it to.
func TestDeclaredCanonicalNamesTheFormToWrite(t *testing.T) {
	_, err := (Declared{Address: "Dimi@Example.com"}).Canonical()
	if err == nil || !strings.Contains(err.Error(), "Dimi@example.com") {
		t.Fatalf("Canonical refused a non-canonical address without naming the canonical one: %v", err)
	}
	_, err = (Declared{Address: "Example.com"}).Canonical()
	if err == nil || !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("Canonical refused a non-canonical domain without naming the canonical one: %v", err)
	}
}
