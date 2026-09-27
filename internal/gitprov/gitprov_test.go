package gitprov

import (
	"errors"
	"fmt"
	"testing"
)

func TestDraftTitle(t *testing.T) {
	for _, tc := range []struct {
		in    string
		draft bool
		want  string
	}{
		{"Add x", true, "[DRAFT] Add x"},
		{"[DRAFT] Add x", true, "[DRAFT] Add x"},
		{"[DRAFT] Add x", false, "Add x"},
		{"Add x", false, "Add x"},
	} {
		if got := DraftTitle(tc.in, tc.draft); got != tc.want {
			t.Errorf("DraftTitle(%q, %v) = %q, want %q", tc.in, tc.draft, got, tc.want)
		}
	}
}

func TestKindForURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://bitbucket.org/acme/web.git":      KindBitbucket,
		"https://x-token-auth@bitbucket.org/a/b":  KindBitbucket,
		"git@bitbucket.org:acme/web.git":          KindBitbucket,
		"https://github.com/acme/web.git":         KindGitHub,
		"ssh://git@GitHub.com/acme/web.git":       KindGitHub,
		"https://github.example.com/acme/web.git": "",
		"http://127.0.0.1:8080/remote.git":        "",
		"/tmp/remote.git":                         "",
	} {
		if got := KindForURL(in); got != want {
			t.Errorf("KindForURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitRepo(t *testing.T) {
	if o, n, ok := SplitRepo("acme/web"); !ok || o != "acme" || n != "web" {
		t.Fatalf("SplitRepo = %q %q %v", o, n, ok)
	}
	for _, bad := range []string{"acme", "/web", "acme/", "a/b/c"} {
		if _, _, ok := SplitRepo(bad); ok {
			t.Errorf("SplitRepo(%q) ok", bad)
		}
	}
}

func TestPartialErrorUnwraps(t *testing.T) {
	inner := errors.New("labels: HTTP 403")
	err := fmt.Errorf("ensure: %w", &PartialError{Err: inner})
	var pe *PartialError
	if !errors.As(err, &pe) || !errors.Is(err, inner) {
		t.Fatalf("err = %v", err)
	}
}
