package mobileauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestRolesAreTheSpecsClosedSetInOrder pins the set's closure and its order:
// the three roles the spec's table names, in the order it names them, and
// nothing else.
func TestRolesAreTheSpecsClosedSetInOrder(t *testing.T) {
	want := []Role{RoleViewer, RoleOperator, RoleAdmin}
	got := Roles()
	if len(got) != len(want) {
		t.Fatalf("Roles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Roles()[%d] = %q, want %q", i, got[i], want[i])
		}
		if !got[i].Valid() {
			t.Errorf("Roles()[%d] = %q is not Valid", i, got[i])
		}
		if got[i].Rank() != i {
			t.Errorf("%q has Rank %d, want %d", got[i], got[i].Rank(), i)
		}
	}
}

// TestParseRoleRefusesEverythingButTheThreeSpellings is the closure's red step:
// a role outside the set is refused, and so is a spelling of one inside it that
// this package would never write. RoleNone is not a role.
func TestParseRoleRefusesEverythingButTheThreeSpellings(t *testing.T) {
	refused := []string{"", "Admin", "ADMIN", "administrator", "viewer ", " viewer", "none", "superuser", "owner"}
	for _, s := range refused {
		if r, ok := ParseRole(s); ok {
			t.Errorf("ParseRole(%q) = %q, true; want the zero Role and false", s, r)
		}
	}
	if RoleNone.Valid() {
		t.Error("RoleNone.Valid() = true; the empty role is not a role in the set")
	}
	if RoleNone.Rank() != -1 {
		t.Errorf("RoleNone.Rank() = %d, want -1", RoleNone.Rank())
	}
}

// TestRoleAtLeastIsFalseForAnythingOutsideTheSet: an unknown or absent role is
// never "at least" anything, so a caller that asks the question with a value it
// did not parse gets no.
func TestRoleAtLeastIsFalseForAnythingOutsideTheSet(t *testing.T) {
	cases := []struct {
		role, min Role
		want      bool
	}{
		{RoleViewer, RoleViewer, true},
		{RoleOperator, RoleViewer, true},
		{RoleAdmin, RoleOperator, true},
		{RoleAdmin, RoleAdmin, true},
		{RoleViewer, RoleOperator, false},
		{RoleOperator, RoleAdmin, false},
		{RoleNone, RoleViewer, false},
		{RoleAdmin, RoleNone, false},
		{RoleNone, RoleNone, false},
		{Role("Admin"), RoleViewer, false},
	}
	for _, c := range cases {
		if got := c.role.AtLeast(c.min); got != c.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", c.role, c.min, got, c.want)
		}
	}
}

// TestNodeFieldsAreTheEntrysFieldsOnTheNode pins the two child names P2 writes
// the proof against and P3 writes: the node states the entry's shape once, and
// a reader spells neither name again.
func TestNodeFieldsAreTheEntrysFieldsOnTheNode(t *testing.T) {
	if NodeFieldEmail != "email" || NodeFieldRole != "role" {
		t.Fatalf("the node's fields are %q and %q, want email and role", NodeFieldEmail, NodeFieldRole)
	}
}

// TestResolveDomainEntryAloneAdmits is the red step's first mutation: a domain
// entry admits its own member with no exact entry anywhere. Remove the domain
// branch from Resolve and this test fails.
func TestResolveDomainEntryAloneAdmits(t *testing.T) {
	domains := []Entry{{Address: "example.com", Role: RoleOperator, At: written(), By: "dimi"}}
	role, err := Resolve(nil, domains, "ana@example.com")
	if err != nil {
		t.Fatalf("Resolve(domains, ana@example.com) = %v, want operator", err)
	}
	if role != RoleOperator {
		t.Fatalf("Resolve(domains, ana@example.com) = %q, want %q", role, RoleOperator)
	}
}

// TestResolveExactOverridesDomainRole is the headline test of the resolution
// rule (D224, and the spec's "an exact email overrides its domain's role"), and
// it is the test the red step's second mutation is proved against: reorder the
// two branches — consult the domain entries first — and the case where the
// exact entry grants LESS than its domain fails.
//
// Both directions are checked, because only one of them can be a promotion: the
// rule is precedence by kind and not the higher role winning, so an exact entry
// that lowers a person's role must hold too.
func TestResolveExactOverridesDomainRole(t *testing.T) {
	cases := []struct {
		name         string
		exact, doman Role
		want         Role
	}{
		{"an exact entry raises what the domain grants", RoleAdmin, RoleViewer, RoleAdmin},
		{"an exact entry lowers what the domain grants", RoleViewer, RoleAdmin, RoleViewer},
		{"an exact entry agrees with the domain", RoleOperator, RoleOperator, RoleOperator},
	}
	for _, c := range cases {
		exact := []Entry{{Address: "dimi@example.com", Role: c.exact, At: written(), By: "dimi"}}
		domains := []Entry{{Address: "example.com", Role: c.doman, At: written(), By: "dimi"}}
		role, err := Resolve(exact, domains, "dimi@example.com")
		if err != nil {
			t.Errorf("%s: Resolve = %v, want %q", c.name, err, c.want)
			continue
		}
		if role != c.want {
			t.Errorf("%s: Resolve = %q, want %q", c.name, role, c.want)
		}
	}
}

// TestResolveUnknownRoleIsRefused is the red step's third mutation: a role
// outside the closed set admits nobody, and it does not fall back to the
// domain entry either. Widen the set — or default an unknown role to viewer —
// and this test fails.
func TestResolveUnknownRoleIsRefused(t *testing.T) {
	exact := []Entry{{Address: "dimi@example.com", Role: Role("superuser"), At: written(), By: "dimi"}}
	domains := []Entry{{Address: "example.com", Role: RoleAdmin, At: written(), By: "dimi"}}
	role, err := Resolve(exact, domains, "dimi@example.com")
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Resolve with an unknown role = %q, %v; want ErrNotAllowed", role, err)
	}
	if role != RoleNone {
		t.Fatalf("Resolve with an unknown role = %q, want the zero Role", role)
	}
	if _, err := Resolve(exact, nil, "dimi@example.com"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Resolve with only an unknown role = %v, want ErrNotAllowed", err)
	}
	// The domain kind's twin of the same branch: a domain entry that names the
	// domain and carries no role this package knows is a refusal of its own,
	// and the message is asserted because the branch is otherwise
	// indistinguishable — without it the same ErrNotAllowed comes from the
	// final "not named at all" return, so a reader of the error cannot tell a
	// domain entry that failed to be usable from a domain that is not an entry.
	for _, role := range []Role{RoleNone, Role("superuser"), Role("Admin")} {
		domains := []Entry{{Address: "example.com", Role: role, At: written(), By: "dimi"}}
		got, err := Resolve(nil, domains, "dimi@example.com")
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("Resolve with a domain entry carrying role %q = %q, %v; want ErrNotAllowed", role, got, err)
			continue
		}
		if !strings.Contains(err.Error(), "cannot be used") {
			t.Errorf("Resolve with a domain entry carrying role %q = %v; want the refusal to say the entry names the domain and cannot be used", role, err)
		}
	}
}

// TestResolveRefusesAnEntryThatNamesTheAddressButCannotBeUsed is the fail-open
// this rule closes, and it is the one the review found: an exact entry whose
// stored form is not canonical (or whose role is unknown) still NAMES the person
// it was written for, so an exact entry that cannot be used must refuse that
// person rather than handing them their domain's role. Treat a non-canonical
// entry as absent — skip it and fall through — and the first case returns admin.
func TestResolveRefusesAnEntryThatNamesTheAddressButCannotBeUsed(t *testing.T) {
	cases := []struct {
		name    string
		exact   []Entry
		address string
		domain  Role
	}{
		{
			name:    "an exact entry with an unfolded domain",
			exact:   []Entry{{Address: "Dimi@Example.com", Role: RoleViewer, At: written(), By: "dimi"}},
			address: "Dimi@example.com",
			domain:  RoleAdmin,
		},
		{
			name:    "an exact entry with an unfolded domain and a folded lookup",
			exact:   []Entry{{Address: "dimi@Example.com", Role: RoleViewer, At: written(), By: "dimi"}},
			address: "dimi@example.com",
			domain:  RoleAdmin,
		},
		{
			name:    "an exact entry with an unknown role",
			exact:   []Entry{{Address: "dimi@example.com", Role: Role("superuser"), At: written(), By: "dimi"}},
			address: "dimi@example.com",
			domain:  RoleAdmin,
		},
		{
			name:    "an exact entry with an absent role",
			exact:   []Entry{{Address: "dimi@example.com", Role: RoleNone, At: written(), By: "dimi"}},
			address: "dimi@example.com",
			domain:  RoleAdmin,
		},
	}
	for _, c := range cases {
		domains := []Entry{{Address: "example.com", Role: c.domain, At: written(), By: "dimi"}}
		role, err := Resolve(c.exact, domains, c.address)
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s: Resolve(%q) = %q, %v; want ErrNotAllowed and no fall-through to the domain's %s", c.name, c.address, role, err, c.domain)
			continue
		}
		// The refusal says which half of the rule refused, and the sentence is
		// the evidence that no fall-through happened: the same ErrNotAllowed
		// would otherwise come from a lookup that named nobody at all.
		if !strings.Contains(err.Error(), "so its domain's entries are not consulted") {
			t.Errorf("%s: Resolve(%q) = %v; want the refusal to say the unusable exact entry stops the domain's entries being consulted", c.name, c.address, err)
		}
	}
	// A well-formed entry of the same key still grants: an entry that cannot be
	// used is not a poison pill for the person's other, usable entry.
	exact := []Entry{
		{Address: "dimi@example.com", Role: RoleViewer, At: written(), By: "dimi"},
		{Address: "Dimi@Example.com", Role: RoleAdmin, At: written(), By: "dimi"},
	}
	if role, err := Resolve(exact, nil, "dimi@example.com"); err != nil || role != RoleViewer {
		t.Errorf("Resolve with one usable and one unusable entry for the key = %q, %v; want viewer", role, err)
	}
}

// TestDomainOfAcceptsACanonicalAddressOnly: DomainOf's answer is the key a
// domain entry is looked up by, so it gives one only for an address this package
// would claim — never a domain for a value a caller forgot to canonicalise.
func TestDomainOfAcceptsACanonicalAddressOnly(t *testing.T) {
	cases := []struct {
		address, want string
	}{
		{"dimi@example.com", "example.com"},
		{"dimi@sub.example.com", "sub.example.com"},
		{"Dimi@example.com", "example.com"},
	}
	for _, c := range cases {
		domain, ok := DomainOf(c.address)
		if !ok || domain != c.want {
			t.Errorf("DomainOf(%q) = %q, %v; want %q, true", c.address, domain, ok, c.want)
		}
	}
	refused := []string{
		"", "@b.com", "@", "dimi@", "dimi@Example.COM", "dimi@example.com ", " dimi@example.com",
		"dimi@@example.com", "dimi@example.com@example.com", "Dimi Paun <dimi@example.com>",
		"dimi@localhost", "dimi@example.com.",
	}
	for _, address := range refused {
		if domain, ok := DomainOf(address); ok {
			t.Errorf("DomainOf(%q) = %q, true; want the domain of a canonical address only", address, domain)
		}
	}
}

// TestResolveFoldsOnlyTheDomainsCase is the red step's fourth mutation: two
// spellings of one address that differ only in the domain's case are one
// address, and two that differ in the local part are two. Drop the
// normalisation and the case-folded lookup fails; fold the local part too and
// the second half fails.
func TestResolveFoldsOnlyTheDomainsCase(t *testing.T) {
	exact := []Entry{{Address: "dimi@example.com", Role: RoleOperator, At: written(), By: "dimi"}}
	for _, address := range []string{"dimi@example.com", "dimi@Example.Com", "dimi@EXAMPLE.COM"} {
		role, err := Resolve(exact, nil, address)
		if err != nil {
			t.Errorf("Resolve(%q) = %v, want operator", address, err)
			continue
		}
		if role != RoleOperator {
			t.Errorf("Resolve(%q) = %q, want %q", address, role, RoleOperator)
		}
	}
	if role, err := Resolve(exact, nil, "Dimi@example.com"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("Resolve(Dimi@example.com) = %q, %v; the local part is not folded, so want ErrNotAllowed", role, err)
	}
	// The two spellings are one address and not two, which is what the
	// canonical form is for.
	first, err := NormalizeEmail("dimi@Example.COM")
	if err != nil {
		t.Fatalf("NormalizeEmail = %v", err)
	}
	second, err := NormalizeEmail("dimi@example.com")
	if err != nil {
		t.Fatalf("NormalizeEmail = %v", err)
	}
	if first != second {
		t.Errorf("%q and %q canonicalise to %q and %q, want one form", "dimi@Example.COM", "dimi@example.com", first, second)
	}
}

// TestResolveAmbiguousEntriesAdmitNobody is the fail-closed half of the rule:
// two entries of one kind that claim one key with different roles are refused,
// not resolved, and the refusal does not hand the decision to the broader kind.
func TestResolveAmbiguousEntriesAdmitNobody(t *testing.T) {
	disagreeing := []Entry{
		{Address: "dimi@example.com", Role: RoleViewer, At: written(), By: "dimi"},
		{Address: "dimi@example.com", Role: RoleAdmin, At: written().Add(time.Hour), By: "dimi"},
	}
	role, err := Resolve(disagreeing, []Entry{{Address: "example.com", Role: RoleAdmin, At: written(), By: "dimi"}}, "dimi@example.com")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Resolve with two disagreeing exact entries = %q, %v; want ErrAmbiguous and no fallback to the domain entry", role, err)
	}
	if role != RoleNone {
		t.Fatalf("Resolve with two disagreeing exact entries = %q, want the zero Role", role)
	}
	// Two entries that agree are not ambiguous: a re-write of one entry is
	// exactly this shape.
	agreeing := []Entry{
		{Address: "dimi@example.com", Role: RoleAdmin, At: written(), By: "dimi"},
		{Address: "dimi@example.com", Role: RoleAdmin, At: written().Add(time.Hour), By: "ana"},
	}
	if role, err := Resolve(agreeing, nil, "dimi@example.com"); err != nil || role != RoleAdmin {
		t.Fatalf("Resolve with two agreeing exact entries = %q, %v; want admin", role, err)
	}
	// A domain entry matches its own domain exactly, so it never collides with
	// a longer domain's entry.
	if role, err := Resolve(nil, []Entry{{Address: "sub.example.com", Role: RoleViewer, At: written(), By: "dimi"}}, "dimi@sub.example.com"); err != nil || role != RoleViewer {
		t.Fatalf("Resolve(dimi@sub.example.com) = %q, %v; want viewer", role, err)
	}
}

// TestResolveRefusesAnEntryItDidNotWrite: an entry whose address is not in the
// canonical form this package writes, or that is in the wrong list for its kind,
// admits nobody. It is ignored rather than reinterpreted — "example.com" is a
// domain and never an address.
func TestResolveRefusesAnEntryItDidNotWrite(t *testing.T) {
	cases := []struct {
		name         string
		exact, doman []Entry
		address      string
	}{
		{"an exact entry with an unfolded domain", []Entry{{Address: "dimi@Example.com", Role: RoleAdmin, At: written(), By: "dimi"}}, nil, "dimi@example.com"},
		{"an exact entry with a padded address", []Entry{{Address: " dimi@example.com", Role: RoleAdmin, At: written(), By: "dimi"}}, nil, "dimi@example.com"},
		{"a domain entry with an unfolded domain", nil, []Entry{{Address: "Example.com", Role: RoleAdmin, At: written(), By: "dimi"}}, "dimi@example.com"},
		{"a wildcard domain entry", nil, []Entry{{Address: "*.example.com", Role: RoleAdmin, At: written(), By: "dimi"}}, "dimi@example.com"},
		{"a domain in the exact list", []Entry{{Address: "example.com", Role: RoleAdmin, At: written(), By: "dimi"}}, nil, "dimi@example.com"},
		{"an address in the domain list", nil, []Entry{{Address: "dimi@example.com", Role: RoleAdmin, At: written(), By: "dimi"}}, "dimi@example.com"},
	}
	for _, c := range cases {
		role, err := Resolve(c.exact, c.doman, c.address)
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s: Resolve = %q, %v; want ErrNotAllowed", c.name, role, err)
		}
	}
}

// TestResolveNeverAdmitsOnADomainEntriesSuffix: the domain rule matches one
// whole domain, so a sibling name that merely ends in the same text is refused.
func TestResolveNeverAdmitsOnADomainEntriesSuffix(t *testing.T) {
	domains := []Entry{{Address: "example.com", Role: RoleAdmin, At: written(), By: "dimi"}}
	for _, address := range []string{"dimi@sub.example.com", "dimi@notexample.com", "dimi@example.com.evil.example", "dimi@example.co"} {
		if role, err := Resolve(nil, domains, address); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("Resolve(%q) = %q, %v; want ErrNotAllowed", address, role, err)
		}
	}
}

// TestResolveReadsNoClockAndNoNode is a reading rather than a run: the entry's
// time is data, so an entry written in the future (or with no time at all)
// resolves exactly as any other. Nothing here consults a clock, which is what
// makes the function pure and the tests repeatable.
func TestResolveReadsNoClockAndNoNode(t *testing.T) {
	exact := []Entry{
		{Address: "future@example.com", Role: RoleAdmin, At: time.Now().Add(100 * 365 * 24 * time.Hour), By: "dimi"},
		{Address: "never@example.com", Role: RoleOperator},
	}
	if role, err := Resolve(exact, nil, "future@example.com"); err != nil || role != RoleAdmin {
		t.Errorf("Resolve(future@example.com) = %q, %v; want admin", role, err)
	}
	if role, err := Resolve(exact, nil, "never@example.com"); err != nil || role != RoleOperator {
		t.Errorf("Resolve(never@example.com) = %q, %v; want operator", role, err)
	}
}

// TestAllDigitsIsFalseWithoutADigit pins the helper the numeric-TLD refusal is
// built on, including the empty label: "" is not an all-digit label, so a domain
// whose last label is empty is refused by the empty-label rule and not admitted
// by this one. It is a direct test because the caller reaches allDigits only
// with a label the loop has already proved non-empty, which leaves the empty
// case covered by nothing else.
func TestAllDigitsIsFalseWithoutADigit(t *testing.T) {
	cases := map[string]bool{"": false, "0": true, "123": true, "com": false, "1a": false, "a1": false, "127": true}
	for s, want := range cases {
		if got := allDigits(s); got != want {
			t.Errorf("allDigits(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestNormalizeEmailIsIdempotent: the canonical form of a canonical form is
// itself, which is the property that lets two writers compare what they wrote.
func TestNormalizeEmailIsIdempotent(t *testing.T) {
	for _, address := range []string{"dimi@example.com", "Dimi@example.com", "dimi.paun+fugaro@Example.COM", "dimi@sub.example.com"} {
		once, err := NormalizeEmail(address)
		if err != nil {
			t.Fatalf("NormalizeEmail(%q) = %v", address, err)
		}
		twice, err := NormalizeEmail(once)
		if err != nil {
			t.Fatalf("NormalizeEmail(%q) = %v", once, err)
		}
		if once != twice {
			t.Errorf("NormalizeEmail(%q) = %q then %q, want one fixed point", address, once, twice)
		}
	}
}

// TestNodeKeyRoundTrips is the red step's fifth mutation: a key can be read back
// as the address it was made from. Drop the encoding's inverse and this test
// fails.
func TestNodeKeyRoundTrips(t *testing.T) {
	addresses := []string{
		"dimi@example.com",
		"dimi.paun+fugaro@Example.COM",
		"example.com",
		"a%40b@example.com",
		"dimi@exämple.com",
		"a/b#c$d[e]@example.com",
		"\"quoted\"@example.com",
	}
	seen := map[string]string{}
	for _, address := range addresses {
		key, err := EncodeNodeKey(address)
		if err != nil {
			t.Fatalf("EncodeNodeKey(%q) = %v", address, err)
		}
		if key == address {
			t.Errorf("EncodeNodeKey(%q) = %q unchanged: every address here carries a byte a key may not, so the encoding must have escaped one", address, key)
		}
		back, err := DecodeNodeKey(key)
		if err != nil {
			t.Fatalf("DecodeNodeKey(%q) = %v", key, err)
		}
		if back != address {
			t.Errorf("DecodeNodeKey(EncodeNodeKey(%q)) = %q, want %q", address, back, address)
		}
		if other, ok := seen[key]; ok {
			t.Errorf("%q and %q share the key %q: the encoding must be injective", other, address, key)
		}
		seen[key] = address
	}
}
