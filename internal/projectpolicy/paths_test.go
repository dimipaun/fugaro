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

// TestPathsEscapeASlug: a slug is escaped exactly as budget.Key escapes it, so
// config/policy/repos/$slug and the rules' auth.token.fs claim agree. The
// table is budget.Key's (internal/budget/model.go); a slug is [a-z0-9-]+ in
// practice, so every entry here is defensive.
func TestPathsEscapeASlug(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"acme-app-0123", "acme-app-0123"},
		{"a.b", "a%2Eb"},
		{"a$b", "a%24b"},
		{"a#b[c]d", "a%23b%5Bc%5Dd"},
		{"a/b", "a%2Fb"},
		{"100%", "100%25"},
		{"\x01", "%01"},
		{"\x7f", "%7F"},
	} {
		if got := PathRepo(tc.in); got != "config/policy/repos/"+tc.want {
			t.Errorf("PathRepo(%q) = %q, want config/policy/repos/%q", tc.in, got, tc.want)
		}
	}
}

func TestPathsPanicOnAnEmptySlug(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("PathRepo(\"\") must panic: an empty segment names the parent node")
		}
	}()
	PathRepo("")
}
