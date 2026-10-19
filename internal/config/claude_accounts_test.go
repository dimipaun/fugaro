package config

import (
	"slices"
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
		"a/b",                          // a path separator
		"x_y",                          // underscore is not in the charset
		"a.b",                          // a dot is not in the charset
		strings.Repeat("a", 32),        // one character over the 31-char cap
		"a" + strings.Repeat("-a", 30), // 61 chars: matches ClaudeAccountNameRE despite being
		// far over the cap (each "-a" repetition is 2 chars);
		// only the explicit length check catches this.
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

// TestClaudeAccountNameLengthCapIsExplicit: ClaudeAccountNameRE's
// {0,30}-repetition alone does not bound a name to MaxClaudeAccountNameLen
// characters, because each repetition may consume two characters via its
// dash alternative ("-" plus the alphanumeric it must precede): a name of
// 30 "-a" repetitions after the leading letter is 61 characters and still
// matches the regex. Red if the length check in ValidateClaudeAccounts
// were removed and the regex alone were relied on, since a dash-heavy name
// that long would then be accepted even though ClaudeAccountSecret's
// "claude-oauth-token-" prefix (19 characters) would push the derived
// secret name past SecretNameRE's 63-character bound.
func TestClaudeAccountNameLengthCapIsExplicit(t *testing.T) {
	longButRegexMatching := "a" + strings.Repeat("-a", 30)
	if len(longButRegexMatching) != 61 {
		t.Fatalf("fixture: want 61 characters, got %d", len(longButRegexMatching))
	}
	if !ClaudeAccountNameRE.MatchString(longButRegexMatching) {
		t.Fatalf("fixture: %q must match ClaudeAccountNameRE despite being over the cap (that's the gap this test pins)", longButRegexMatching)
	}
	ps := ValidateClaudeAccounts(map[string]ClaudeAccount{longButRegexMatching: {}})
	if len(ps) == 0 {
		t.Fatal("a 61-character, regex-matching name was accepted")
	}
	for _, p := range ps {
		if !strings.Contains(p.Message, "31 characters") {
			t.Errorf("refusal does not name the length cap: %+v", p)
		}
	}
	if secret := ClaudeAccountSecret(longButRegexMatching); len(secret) <= 63 {
		t.Fatalf("fixture: want the derived secret name to exceed SecretNameRE's 63-character bound, got %d: %q", len(secret), secret)
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

// TestValidateClaudeAccountsRefusesWildcard pins that the literal "*" is
// not a repository slug for a claude account: only providers: gets the
// wildcard rule (design docs/design/project-secrets.md §3a), so a future
// change to ValidateModelProviders's own acceptance is never accidentally
// copied here by a careless shared-helper edit.
func TestValidateClaudeAccountsRefusesWildcard(t *testing.T) {
	ps := ValidateClaudeAccounts(map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"*"}}})
	if len(ps) == 0 {
		t.Fatal("the literal \"*\" was accepted as a repository slug for a claude account; only providers: gets the wildcard rule (design §3a)")
	}
}

// TestProjectJobAccounts pins the account axis: the profile's own
// claude_accounts field, the empty account first and always present, the
// default account folded into it, and the rest sorted. It is the one rule the
// deployer, the launcher and the runner compute a profile's job set with, so
// every case here is a name one of the three would otherwise disagree about.
//
// Mutation (run, restore): drop the leading empty entry (a profile that lists
// nothing then serves no job at all, and the deployer stops building the job
// every existing installation has) or stop folding DefaultClaudeAccount into
// it (a second job appears, named for "default", while the launcher and the
// runner keep targeting the no-account one).
func TestProjectJobAccounts(t *testing.T) {
	for _, tc := range []struct {
		why      string
		accounts []string
		want     []string
	}{
		{"a profile serving no account keeps its no-account job", nil, []string{""}},
		{"the default account is the no-account job", []string{DefaultClaudeAccount}, []string{""}},
		{"a named account is a job of its own beside the no-account one", []string{"acme-personal"}, []string{"", "acme-personal"}},
		{"the axis is sorted, and the empty account sorts first", []string{"zulu", "acme-personal", DefaultClaudeAccount}, []string{"", "acme-personal", "zulu"}},
		{"an account listed twice is one job", []string{"acme-personal", "acme-personal"}, []string{"", "acme-personal"}},
	} {
		if got := ProjectJobAccounts(JobProfile{ClaudeAccounts: tc.accounts}); !slices.Equal(got, tc.want) {
			t.Errorf("%s: ProjectJobAccounts(%v) = %v, want %v", tc.why, tc.accounts, got, tc.want)
		}
	}
	// The profile's own list is never written to: it is the config's, and the
	// deployer holds it for the whole job set.
	list := []string{"acme-personal", DefaultClaudeAccount}
	_ = ProjectJobAccounts(JobProfile{ClaudeAccounts: list})
	if !slices.Equal(list, []string{"acme-personal", DefaultClaudeAccount}) {
		t.Errorf("ProjectJobAccounts rewrote the profile's own list: %v", list)
	}
}

// TestProjectJobTokenAccount pins the join of a job's account axis and the
// token that job holds: the account as the policy's accounts block spells it
// (the key whose allow_repos bounds the token) and whether the profile serves
// one on that job. Every case here is a launch the gate admits or refuses and
// a run the runner re-checks, so a wrong answer here is a job whose credential
// both sides disagree about.
//
// Mutation (run, restore): answer the account's own name for the no-account
// job instead of the default account (a profile listing "default" then holds
// the token of an account named "default", a key the accounts block never
// carries and whose list nothing holds the run to), or return true for a
// profile that lists nothing (the gate would hold every run of a profile that
// serves no account to an account the profile never named).
func TestProjectJobTokenAccount(t *testing.T) {
	plain := JobProfile{}
	named := JobProfile{ClaudeAccounts: []string{"acme-personal"}}
	both := JobProfile{ClaudeAccounts: []string{"acme-personal", DefaultClaudeAccount}}
	for _, tc := range []struct {
		why     string
		pr      JobProfile
		account string
		want    string
		serves  bool
	}{
		{"a profile that lists nothing holds no token on its no-account job", plain, "", "", false},
		{"a named account's job holds that account's token", named, "acme-personal", "acme-personal", true},
		{"a profile listing only named accounts holds none on its no-account job", named, "", "", false},
		{"the default account rides the no-account job when the profile lists it", both, "", DefaultClaudeAccount, true},
		{"the job axis spells the default account as the empty account", both, DefaultClaudeAccount, DefaultClaudeAccount, true},
		{"and a named account beside it is a job of its own", both, "acme-personal", "acme-personal", true},
		{"an account the profile does not list holds no token", named, "acme-work", "", false},
	} {
		got, serves := ProjectJobTokenAccount(tc.pr, tc.account)
		if got != tc.want || serves != tc.serves {
			t.Errorf("%s: ProjectJobTokenAccount(%v, %q) = (%q, %v), want (%q, %v)",
				tc.why, tc.pr.ClaudeAccounts, tc.account, got, serves, tc.want, tc.serves)
		}
		if serves && !slices.Contains(tc.pr.ClaudeAccounts, got) {
			t.Errorf("%s: the token account %q is not one the profile lists (%v): the policy's accounts block is keyed by what a profile lists",
				tc.why, got, tc.pr.ClaudeAccounts)
		}
	}
}
