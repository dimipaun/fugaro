package config

import (
	"strings"
	"testing"
)

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

// TestAllowedClaudeAccountsEmptyOrNullAllowListMeansNone: an account with
// an empty or absent (null) allow_data_to allows no repository, not every
// repository. Red if AllowsData/AllowedClaudeAccounts ever treated "no
// entries" as "all": e.g. `len(a.AllowDataTo) == 0` meaning "unrestricted"
// instead of "none".
func TestAllowedClaudeAccountsEmptyOrNullAllowListMeansNone(t *testing.T) {
	accounts := map[string]ClaudeAccount{
		"empty-list": {AllowDataTo: []string{}},
		"null-list":  {AllowDataTo: nil},
	}
	if got := AllowedClaudeAccounts(accounts, "acme/app"); !equalSorted(got, []string{"default"}) {
		t.Fatalf("an empty or null allow_data_to let a repository in: %v", got)
	}
	if accounts["empty-list"].AllowsData("acme/app") || accounts["null-list"].AllowsData("acme/app") {
		t.Fatal("AllowsData(repo) is true for an account with no allow_data_to entries")
	}
}

// TestClaudeAccountNameCharset pins the exact character set the plan's
// design settled on (design docs/design/named-oauth-tokens.md §3): lower
// case letters, digits and single internal dashes, never "default". Red if
// ClaudeAccountNameRE is ever widened to accept any of these.
func TestClaudeAccountNameCharset(t *testing.T) {
	for _, bad := range []string{
		"a/b",                            // a path separator
		"x_y",                            // underscore is not in the charset
		"a.b",                            // a dot is not in the charset
		strings.Repeat("a", 32),          // one character over the 31-char cap
		"Acme",                           // uppercase
		"",                               // empty
		"default",                        // reserved (caught by its own case, still refused)
		"a-", "a--b", "-a", "-", "a---b", // dash placement (see ClaudeAccountNameRE's comment)
	} {
		accounts := map[string]ClaudeAccount{bad: {}}
		if ps := ValidateClaudeAccounts(accounts); len(ps) == 0 {
			t.Errorf("name %q was accepted", bad)
		}
	}
	for _, good := range []string{"a", "acme-personal", "acme2", strings.Repeat("a", 31)} {
		accounts := map[string]ClaudeAccount{good: {}}
		if ps := ValidateClaudeAccounts(accounts); len(ps) != 0 {
			t.Errorf("name %q was refused: %+v", good, ps)
		}
	}
}

// TestValidateClaudeAccountsRefusesDotSegments: repoSlugRE's charset allows
// '.', so "../.." and "./." match it syntactically but name no repository.
// Red if the dot-segment check were removed (hasDotSegment).
func TestValidateClaudeAccountsRefusesDotSegments(t *testing.T) {
	for _, bad := range []string{"../..", "./.", "./x", "x/..", "../x"} {
		accounts := map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{bad}}}
		ps := ValidateClaudeAccounts(accounts)
		if len(ps) == 0 {
			t.Errorf("allow_data_to entry %q was accepted", bad)
		}
	}
	// A dotted owner or repo name that is not just "." or ".." is still fine.
	if ps := ValidateClaudeAccounts(map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme.corp/app.name"}}}); len(ps) != 0 {
		t.Errorf("a dotted but non-relative slug was refused: %+v", ps)
	}
}

// TestValidateClaudeAccountsNeverEchoesHostileCharsRaw: a name or
// allow_data_to entry a human will never have typed intentionally (control
// characters, a bidi override) must not reach Problem.Path/Message raw,
// since both are printed to a terminal (fugaro validate, doctor, the
// aggregated local config error).
func TestValidateClaudeAccountsNeverEchoesHostileCharsRaw(t *testing.T) {
	hostileName := "ev\x1b[31mil‮"
	ps := ValidateClaudeAccounts(map[string]ClaudeAccount{hostileName: {AllowDataTo: []string{"a\x1b[31mb/c‮d"}}})
	if len(ps) == 0 {
		t.Fatal("fixture: expected problems")
	}
	for _, p := range ps {
		if strings.ContainsAny(p.Path, "\x1b") || strings.ContainsRune(p.Path, '‮') {
			t.Errorf("Problem.Path carries a raw control or bidi character: %q", p.Path)
		}
		if strings.ContainsAny(p.Message, "\x1b") || strings.ContainsRune(p.Message, '‮') {
			t.Errorf("Problem.Message carries a raw control or bidi character: %q", p.Message)
		}
	}
}

// TestValidateClaudeAccountsBoundsHostileLength: %q alone already escapes a
// control or bidi character (Go's strconv.Quote does), but it does not
// bound length; an overlong allow_data_to entry (or account name) must not
// make the aggregated local config error unbounded. Red if the length cap
// were dropped from either the path or the message.
func TestValidateClaudeAccountsBoundsHostileLength(t *testing.T) {
	longName := strings.Repeat("x", 10_000)
	longRepo := strings.Repeat("y", 10_000) // not owner/name shaped either
	ps := ValidateClaudeAccounts(map[string]ClaudeAccount{longName: {AllowDataTo: []string{longRepo}}})
	if len(ps) != 2 {
		t.Fatalf("want one problem for the name and one for allow_data_to, got %+v", ps)
	}
	for _, p := range ps {
		if n := len([]rune(p.Path)); n > 300 {
			t.Errorf("Problem.Path is %d runes, not bounded: %.60s...", n, p.Path)
		}
		if n := len([]rune(p.Message)); n > 300 {
			t.Errorf("Problem.Message is %d runes, not bounded: %.60s...", n, p.Message)
		}
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
