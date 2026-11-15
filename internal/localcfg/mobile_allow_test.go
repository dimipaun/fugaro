package localcfg

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/mobileauth"
)

// mobileAllowConfig is the fixture's whole file with a mobile_allow: block
// appended: what an operator writes when they declare who may use the project
// from a phone.
func mobileAllowConfig(block string) string {
	return "version: 2\nname: aurora\ngcp_project: aurora-gcp-1\nregion: us-east5\n" +
		"runs_bucket: aurora-runs\nmax_parallel: 2\n" + block
}

// TestMobileAllowLoadsACanonicalDeclaration: the block an operator writes is
// read, one entry at a time, with the kind of each entry read off the value
// (an "@" is a person, its absence a domain) and the role required.
func TestMobileAllowLoadsACanonicalDeclaration(t *testing.T) {
	c, err := Parse([]byte(mobileAllowConfig(
		"mobile_allow:\n" +
			"  - uid: m~7f3a2b9c\n" +
			"    address: dimi@example.com\n" +
			"    role: admin\n" +
			"  - address: lattica.com\n" +
			"    role: viewer\n")))
	if err != nil {
		t.Fatalf("a canonical declaration was refused: %v", err)
	}
	if len(c.MobileAllow) != 2 {
		t.Fatalf("mobile_allow carries %d entries, want 2: %+v", len(c.MobileAllow), c.MobileAllow)
	}
	if got := c.MobileAllow[0]; got.UID != "m~7f3a2b9c" || got.Address != "dimi@example.com" || got.Role != mobileauth.RoleAdmin {
		t.Errorf("entry 0 = %+v, want the uid, the address and the role as written", got)
	}
	if got := c.MobileAllow[1]; got.UID != "" || got.Address != "lattica.com" || got.Role != mobileauth.RoleViewer {
		t.Errorf("entry 1 = %+v, want a domain entry with no uid", got)
	}
}

// TestMobileAllowRefusesAMalformedEntry: a declaration is an authority, so
// every way of writing one that cannot be read is refused at the load — by the
// entry's position, with the value it carries — and no entry is ever repaired
// or given a role. Each case is a file an operator can write and a publish
// would otherwise seed a node from.
func TestMobileAllowRefusesAMalformedEntry(t *testing.T) {
	for _, tc := range []struct {
		name, block, want string
	}{
		{"no role", "mobile_allow:\n  - address: dimi@example.com\n", "role"},
		{"a role outside the set", "mobile_allow:\n  - address: dimi@example.com\n    role: administrator\n", "role"},
		{"a role whose case is wrong", "mobile_allow:\n  - address: dimi@example.com\n    role: Admin\n", "role"},
		{"a person with no role beside it", "mobile_allow:\n  - uid: m~1\n    address: dimi@example.com\n    role: \"\"\n", "role"},
		{"an address that is not canonical", "mobile_allow:\n  - address: Dimi@Example.com\n    role: admin\n", "mobile_allow[0]"},
		{"a domain that is not canonical", "mobile_allow:\n  - address: Example.com\n    role: admin\n", "mobile_allow[0]"},
		{"a value that names nobody", "mobile_allow:\n  - address: \" dimi@example.com\"\n    role: admin\n", "mobile_allow[0]"},
		{"a uid a database key cannot hold", "mobile_allow:\n  - uid: m~7f.3a\n    address: dimi@example.com\n    role: admin\n", "uid"},
		{"a uid that is only a space", "mobile_allow:\n  - uid: \" \"\n    address: dimi@example.com\n    role: admin\n", "uid"},
		{"one uid declared twice", "mobile_allow:\n  - uid: m~1\n    address: dimi@example.com\n    role: admin\n  - uid: m~1\n    address: ana@example.com\n    role: viewer\n", "twice"},
		{"one address with two roles", "mobile_allow:\n  - uid: m~1\n    address: dimi@example.com\n    role: admin\n  - uid: m~2\n    address: dimi@example.com\n    role: viewer\n", "one role"},
		{"an unknown key", "mobile_allow:\n  - address: dimi@example.com\n    role: admin\n    admin: true\n", "field admin not found"},
	} {
		_, err := Parse([]byte(mobileAllowConfig(tc.block)))
		if err == nil {
			t.Errorf("%s: the declaration was accepted, and a malformed entry is refused rather than defaulted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: the refusal is %q, and it has to name %q", tc.name, err, tc.want)
		}
		// The strict decode's own refusal names the type rather than the block:
		// it is the yaml reader's sentence and it carries the line.
		if !strings.Contains(err.Error(), "mobile_allow") && !strings.Contains(err.Error(), "Unable") && !strings.Contains(err.Error(), "mobileauth.Declared") {
			t.Errorf("%s: the refusal names neither the block nor the type the reader refused (%v)", tc.name, err)
		}
	}
}

// TestMobileAllowRefusalNamesTheEntryAndItsValue: a reader fixing the file has
// to be able to find the entry, so the refusal names its position and the value
// it carries — and it never names a role it invented.
func TestMobileAllowRefusalNamesTheEntryAndItsValue(t *testing.T) {
	_, err := Parse([]byte(mobileAllowConfig(
		"mobile_allow:\n" +
			"  - uid: m~1\n    address: dimi@example.com\n    role: admin\n" +
			"  - uid: m~2\n    address: ana@example.com\n    role: wizard\n")))
	if err == nil {
		t.Fatal("a role outside the closed set was accepted")
	}
	for _, want := range []string{"mobile_allow[1]", "wizard", "viewer, operator, admin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q: it has to carry %q", err, want)
		}
	}
}

// TestMobileAllowBlockIsLocalOnly: the declaration is the owner's own and it is
// never published into the runs bucket, which any launcher can write — a
// launcher who could write it would admit itself with the role it named.
func TestMobileAllowBlockIsLocalOnly(t *testing.T) {
	c, err := Parse([]byte(mobileAllowConfig("mobile_allow:\n  - address: dimi@example.com\n    role: admin\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Shared().MobileAllow; got != nil {
		t.Errorf("the published copy carries %d declared entries: the block is the owner's own and is never published", len(got))
	}
}

// TestMobileAllowEmptyBlockDeclaresNothing: a written block with no entry is an
// operator who declares nobody, which is the same state as a file that does not
// write the key at all — and both are a publish that seeds no device node.
func TestMobileAllowEmptyBlockDeclaresNothing(t *testing.T) {
	c, err := Parse([]byte(mobileAllowConfig("mobile_allow: []\n")))
	if err != nil {
		t.Fatalf("an empty declaration was refused: %v", err)
	}
	if len(c.MobileAllow) != 0 {
		t.Errorf("an empty block reads as %d entries", len(c.MobileAllow))
	}
}
