package gitprov

import "testing"

// TestOwnedLabel pins the namespace test the syncer and the credentials
// service share (F23, R3): Fugaro's own labels are the fugaro: and spec:
// namespaces, matched the way the host matches them — case-insensitively —
// and nothing else is ever Fugaro's to touch.
//
// The two non-ASCII cases pin the direction the ASCII rule fails in (R3): a
// name that only looks like one of the namespaces — the long s in front of
// "pec:", the Kelvin sign where an ASCII letter would be — is a label the
// host does not match to Fugaro's, and so is not Fugaro's here.
func TestOwnedLabel(t *testing.T) {
	for _, c := range []struct {
		name  string
		owned bool
		why   string
	}{
		{name: "fugaro:running", owned: true},
		{name: "fugaro:failed", owned: true},
		{name: "spec:forge-labels", owned: true},
		{name: "FUGARO:RUNNING", owned: true, why: "the host matches label names case-insensitively"},
		{name: "Spec:Forge-Labels", owned: true},
		{name: "fugaro:", owned: true, why: "the namespace alone is owned; the shape after the colon is the caller's check"},
		{name: "spec:", owned: true},
		{name: "spec:café", owned: true, why: "still the spec: namespace; R3's shape check is what refuses the name"},
		{name: "spec:Kx", owned: true, why: "the Kelvin sign is after the namespace, and only A–Z folds"},
		{name: "ſpec:forge-labels", owned: false, why: "the long s is not the ASCII the spec: namespace is spelled in"},
		{name: "Kugaro:running", owned: false, why: "the Kelvin sign is not an f, whatever Unicode folds it onto"},
		{name: "bug", owned: false},
		{name: "fugaro", owned: false, why: "no colon, so no namespace"},
		{name: "spec", owned: false},
		{name: "fugaros:running", owned: false},
		{name: "specs:forge-labels", owned: false},
		{name: "docs:fugaro:running", owned: false},
		{name: " fugaro:running", owned: false},
		{name: "", owned: false},
	} {
		if got := OwnedLabel(c.name); got != c.owned {
			t.Errorf("OwnedLabel(%q) = %t, want %t (%s)", c.name, got, c.owned, c.why)
		}
	}
}
