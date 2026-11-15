package gitprov

import "testing"

// TestOwnedLabel pins the namespace test the syncer and the credentials
// service share (F23, R3): Fugaro's own labels are the fugaro: and spec:
// namespaces, matched the way the host matches them — case-insensitively —
// and nothing else is ever Fugaro's to touch.
//
// The two non-ASCII cases are the reason the fold is over A–Z and not
// strings.ToLower's: the long s and the Kelvin sign are what a Unicode fold
// would turn into the "s" of "spec:" and the "k" of a name, so a label a
// person could make would read as one Fugaro owns.
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
		{name: "ſpec:forge-labels", owned: false, why: "the long s folds onto s under Unicode, so the namespace must not be folded"},
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
