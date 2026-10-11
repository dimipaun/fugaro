package config

import "testing"

func TestClaudeAccountSecret(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"", "claude-oauth-token"},
		{"default", "claude-oauth-token"},
		{"acme-personal", "claude-oauth-token-acme-personal"},
	} {
		if got := ClaudeAccountSecret(tc.name); got != tc.want {
			t.Errorf("ClaudeAccountSecret(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClaudeAccountBySecret(t *testing.T) {
	accounts := map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/app"}}}
	if name, ok := ClaudeAccountBySecret(accounts, "claude-oauth-token"); !ok || name != DefaultClaudeAccount {
		t.Fatalf("default: %q %v", name, ok)
	}
	if name, ok := ClaudeAccountBySecret(accounts, "claude-oauth-token-acme-personal"); !ok || name != "acme-personal" {
		t.Fatalf("named: %q %v", name, ok)
	}
	if _, ok := ClaudeAccountBySecret(accounts, "claude-oauth-token-unknown"); ok {
		t.Fatal("an unconfigured name matched")
	}
}

func TestAllowedClaudeAccountsExcludesUnlistedRepo(t *testing.T) {
	accounts := map[string]ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{"acme/app", "Acme/Sandbox"}}, // case folded
		"other-team":    {AllowDataTo: []string{"other/repo"}},
	}
	got := AllowedClaudeAccounts(accounts, "acme/app")
	want := []string{"acme-personal", "default"} // sorted, default always present
	if !equalSorted(got, want) {
		t.Fatalf("AllowedClaudeAccounts(acme/app) = %v, want %v", got, want)
	}
	if got := AllowedClaudeAccounts(accounts, "acme/sandbox"); !equalSorted(got, []string{"acme-personal", "default"}) {
		t.Fatalf("case-insensitive repo match failed: %v", got)
	}
	if got := AllowedClaudeAccounts(accounts, "acme/unrelated"); !equalSorted(got, []string{"default"}) {
		t.Fatalf("an unlisted repository got a named account: %v", got)
	}
	if got := AllowedClaudeAccounts(nil, "acme/app"); !equalSorted(got, []string{"default"}) {
		t.Fatalf("no claude_accounts configured: %v", got)
	}
}

func TestValidateClaudeAccounts(t *testing.T) {
	bad := map[string]ClaudeAccount{
		"default": {}, // reserved
		"Acme":    {}, // uppercase
		"ok-name": {AllowDataTo: []string{"not-a-repo-slug"}},
	}
	ps := ValidateClaudeAccounts(bad)
	wantPaths := map[string]bool{"claude_accounts.default": true, "claude_accounts.Acme": true, "claude_accounts.ok-name.allow_data_to": true}
	if len(ps) != len(wantPaths) {
		t.Fatalf("ValidateClaudeAccounts = %+v, want one problem per %v", ps, wantPaths)
	}
	for _, p := range ps {
		if !wantPaths[p.Path] {
			t.Errorf("unexpected problem path %q", p.Path)
		}
	}
	if ps := ValidateClaudeAccounts(map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("a valid block was refused: %+v", ps)
	}
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
}
