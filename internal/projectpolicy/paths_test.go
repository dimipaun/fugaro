package projectpolicy

import "testing"

func TestPaths(t *testing.T) {
	if got, want := PathShared(), "config/policy/shared"; got != want {
		t.Errorf("PathShared() = %q, want %q", got, want)
	}
	if got, want := PathAdmin(), "config/admin"; got != want {
		t.Errorf("PathAdmin() = %q, want %q", got, want)
	}
	if got, want := PathRepo("acme-app-0123456789abcdef"), "config/policy/repos/acme-app-0123456789abcdef"; got != want {
		t.Errorf("PathRepo() = %q, want %q", got, want)
	}
}

// The escaping itself (that a slug is keyed exactly as budget.Key keys it) is
// asserted in internal/budget/rules, against budget.Key, so this package does
// not import internal/budget to check its own copy of it.

func TestPathsPanicOnAnEmptySlug(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("PathRepo(\"\") must panic: an empty segment names the parent node")
		}
	}()
	PathRepo("")
}
