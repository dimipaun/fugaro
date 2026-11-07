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

// TestProjectJobTokenAccountRefusesTheVariantName: the token axis never answers
// for a name in the review-only variant's spelling (CSR8). The axis is the
// coding jobs' — the account segment of a job that mounts a Claude token — and
// a review job mounts none (O1, D893(c)): its credential is the run's class's,
// from the credentials service. So team-a-review and default-review, the
// account segments the variant's names carry, resolve to no account of any
// profile, and the coding job's account is not reachable by asking for its
// variant's spelling.
//
// The refusal is the axis's, not the profile's: a profile cannot list such a
// name at all (ValidateClaudeAccounts, the suffix rule), so the case below is
// the last door — the name arrives here and no account answers for it.
//
// Mutation (run, restore): strip ReviewVariantSuffix from an account before
// looking it up in the profile's list (the variant's name would then answer
// with the account it is named after, and the token that account holds would be
// the one a review job is handed).
func TestProjectJobTokenAccountRefusesTheVariantName(t *testing.T) {
	both := JobProfile{ClaudeAccounts: []string{"acme-personal", DefaultClaudeAccount}}
	for _, tc := range []struct {
		why     string
		account string
	}{
		{"a named account's variant is no account of the profile", "acme-personal" + ReviewVariantSuffix},
		{"the default account's variant is no account either", DefaultClaudeAccount + ReviewVariantSuffix},
		{"and the variant of an account the profile does not list is none", "acme-work" + ReviewVariantSuffix},
	} {
		if got, serves := ProjectJobTokenAccount(both, tc.account); serves || got != "" {
			t.Errorf("%s: ProjectJobTokenAccount(%v, %q) = (%q, %v), want (\"\", false): a review job holds no Claude token, so no account answers for the variant's name",
				tc.why, both.ClaudeAccounts, tc.account, got, serves)
		}
	}
	// The axis still answers for the coding jobs' own names, so the refusal
	// above is the suffix and not a profile listing nothing.
	if got, serves := ProjectJobTokenAccount(both, "acme-personal"); !serves || got != "acme-personal" {
		t.Errorf("ProjectJobTokenAccount(%v, %q) = (%q, %v), want (\"acme-personal\", true)",
			both.ClaudeAccounts, "acme-personal", got, serves)
	}
}

// TestValidateClaudeAccountsRefusesBitbucketToken: the account name
// "bitbucket-token" is config.BitbucketSecret, the name of the credential a
// job mounts for upstream git (ReservedSecrets) and the one name the
// credentials bundle's own key resolver refuses —
// claude-oauth-token-bitbucket-token resolves to it (credsvc.CredentialKey) —
// so an account taking it would leave one name meaning two credentials.
// Red before the rule: ValidateClaudeAccounts accepted the name, and an
// operator learned of the clash only at `fugaro creds set` or bundle time.
func TestValidateClaudeAccountsRefusesBitbucketToken(t *testing.T) {
	ps := ValidateClaudeAccounts(map[string]ClaudeAccount{BitbucketSecret: {AllowDataTo: []string{"acme/app"}}})
	if len(ps) != 1 {
		t.Fatalf("ValidateClaudeAccounts with an account named %s = %+v, want one refusal", BitbucketSecret, ps)
	}
	if got, want := ps[0].Path, "claude_accounts."+BitbucketSecret; got != want {
		t.Errorf("Problem.Path = %q, want %q", got, want)
	}
	if !strings.Contains(ps[0].Message, "reserved") {
		t.Errorf("the refusal does not say the name is reserved: %q", ps[0].Message)
	}
	if strings.Contains(ps[0].Message, BitbucketSecret) {
		t.Errorf("the refusal echoes the name: %q", ps[0].Message)
	}
}

// TestValidClaudeAccountRefRefusesBitbucketToken: the reference half of the
// same rule, and the reason a policy may no longer admit the account either.
// A profile's claude_accounts entry naming the bundle's git credential is
// refused where it is written (ValidClaudeAccountRef), in the same sentence
// the definition is refused in — the two halves are one rule — and the
// profile validation that reads the entry reports that refusal instead of
// resolving a name that names another credential.
func TestValidClaudeAccountRefRefusesBitbucketToken(t *testing.T) {
	msg := ValidClaudeAccountRef(BitbucketSecret)
	if msg == "" {
		t.Fatalf("ValidClaudeAccountRef(%q) is valid, want a refusal", BitbucketSecret)
	}
	if strings.Contains(msg, BitbucketSecret) {
		t.Errorf("the refusal echoes the name: %q", msg)
	}
	profiles := map[string]JobProfile{"standard": {ClaudeAccounts: []string{BitbucketSecret}}}
	ps := ValidateProfileClaudeAccounts(profiles, map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/app"}}}, nil)
	if len(ps) != 1 {
		t.Fatalf("a profile naming the git credential's name = %+v, want one refusal", ps)
	}
	if got, want := ps[0].Path, "profiles.standard.claude_accounts[0]"; got != want {
		t.Errorf("Problem.Path = %q, want %q", got, want)
	}
	def := ValidateClaudeAccounts(map[string]ClaudeAccount{BitbucketSecret: {}})
	if len(def) != 1 || def[0].Message != msg {
		t.Errorf("the definition and the reference disagree about %s: %+v vs %q", BitbucketSecret, def, msg)
	}
}

// TestValidateClaudeAccountsRefusesTheReviewVariantSuffix: an account whose
// own name ends in the review-only variant's suffix is a configuration no
// installation may hold (CSR7, the owner's ruling of 2026-11-04). The variant's
// job name and service account are derived over the (profile, provider,
// account) axis with the suffix on the account segment, so an account named
// acme-personal-review derives exactly the job name and service account of
// acme-personal's variant — and the coding job of that name mounts the App key,
// which the review container must never be able to read (O1). The refusal is
// the definition half of the rule; the reference half is
// TestValidClaudeAccountRefRefusesTheReviewVariantSuffix.
//
// The rule is about the SUFFIX and nothing else: a name that merely mentions
// review is an ordinary account, and refusing it would take names the
// deployment holds no property about.
//
// Red before the rule: ValidateClaudeAccounts accepted acme-personal-review,
// and the clash surfaced only at `fugaro update`, when the deployer's own
// cross-set check refused the variant (ProjectReviewJobs).
func TestValidateClaudeAccountsRefusesTheReviewVariantSuffix(t *testing.T) {
	for _, name := range []string{"acme-personal-review", "a-review"} {
		ps := ValidateClaudeAccounts(map[string]ClaudeAccount{name: {AllowDataTo: []string{"acme/app"}}})
		if len(ps) != 1 {
			t.Fatalf("ValidateClaudeAccounts with an account named %q = %+v, want one refusal", name, ps)
		}
		if got, want := ps[0].Path, "claude_accounts."+name; got != want {
			t.Errorf("Problem.Path = %q, want %q", got, want)
		}
		if !strings.Contains(ps[0].Message, ReviewVariantSuffix) {
			t.Errorf("the refusal does not name the suffix that makes the name collide: %q", ps[0].Message)
		}
		if strings.Contains(ps[0].Message, name) {
			t.Errorf("the refusal echoes the account name: %q", ps[0].Message)
		}
	}
	// The suffix is only refused where it is the END of the name.
	for _, name := range []string{"review-bot", "acme-reviewer", "acme-personal-reviews"} {
		if ps := ValidateClaudeAccounts(map[string]ClaudeAccount{name: {AllowDataTo: []string{"acme/app"}}}); len(ps) > 0 {
			t.Errorf("an account named %q was refused: %+v", name, ps)
		}
	}
}

// TestValidClaudeAccountRefRefusesTheReviewVariantSuffix: the reference half of
// the same rule. A profile's claude_accounts entry naming such an account is
// refused where it is written, in the same sentence the definition is refused
// in (the two halves are one rule), so the published policy cannot admit an
// account whose coding job a review variant would collide with.
func TestValidClaudeAccountRefRefusesTheReviewVariantSuffix(t *testing.T) {
	const name = "acme-personal-review"
	msg := ValidClaudeAccountRef(name)
	if msg == "" {
		t.Fatalf("ValidClaudeAccountRef(%q) is valid, want a refusal", name)
	}
	if !strings.Contains(msg, ReviewVariantSuffix) || strings.Contains(msg, name) {
		t.Errorf("the refusal must name the suffix and not the account: %q", msg)
	}
	profiles := map[string]JobProfile{"standard": {ClaudeAccounts: []string{name}}}
	ps := ValidateProfileClaudeAccounts(profiles, map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/app"}}}, nil)
	if len(ps) != 1 {
		t.Fatalf("a profile naming %q = %+v, want one refusal", name, ps)
	}
	if got, want := ps[0].Path, "profiles.standard.claude_accounts[0]"; got != want {
		t.Errorf("Problem.Path = %q, want %q", got, want)
	}
	def := ValidateClaudeAccounts(map[string]ClaudeAccount{name: {}})
	if len(def) != 1 || def[0].Message != msg {
		t.Errorf("the definition and the reference disagree about %s: %+v vs %q", name, def, msg)
	}
}

// TestAnthropicKeyAccountIsAReferenceAndNotADefinition: the sentinel is the
// account axis's value for the project's own Anthropic API key, so the two
// halves of the name rule must part company about it — and they are the two
// branches the change that introduced it rests on:
//
//   - ValidClaudeAccountRef accepts it, which is what lets a profile and a
//     published policy spell it at all;
//   - ValidateClaudeAccounts refuses it as a claude_accounts KEY, with its own
//     sentence: an entry under it would configure an account whose token does
//     not exist (the credential is the platform's, held at tier B), and the
//     message the operator reads must say so rather than fall through to the
//     shape rule, which would report a legal value as a misspelling.
//
// The mutations are the two removals: dropping the case from
// ValidClaudeAccountRef makes the sentinel unspellable (red on the first
// assertion), and dropping it from ValidateClaudeAccounts lets the entry
// through (red on the second).
func TestAnthropicKeyAccountIsAReferenceAndNotADefinition(t *testing.T) {
	if msg := ValidClaudeAccountRef(AnthropicKeyAccount); msg != "" {
		t.Fatalf("ValidClaudeAccountRef(%q) = %q, want the sentinel accepted where an account is referenced", AnthropicKeyAccount, msg)
	}
	// It is outside the name rule by construction, which is why it needs a
	// case of its own: no installation can hold a Claude account of this name,
	// so the value renames nothing (AnthropicKeyAccount's comment).
	if ClaudeAccountNameRE.MatchString(AnthropicKeyAccount) {
		t.Errorf("%q matches ClaudeAccountNameRE: a sentinel that is also an account name would make one spelling mean two credentials", AnthropicKeyAccount)
	}
	def := ValidateClaudeAccounts(map[string]ClaudeAccount{AnthropicKeyAccount: {AllowDataTo: []string{"acme/app"}}})
	if len(def) != 1 {
		t.Fatalf("a claude_accounts entry named %q = %+v, want one refusal", AnthropicKeyAccount, def)
	}
	if got, want := def[0].Path, "claude_accounts."+AnthropicKeyAccount; got != want {
		t.Errorf("Problem.Path = %q, want %q", got, want)
	}
	if def[0].Message != anthropicKeyAccountNameMessage {
		t.Errorf("the definition half answers the sentinel with %q, want its own sentence: a legal value reported by the shape rule reads as a misspelling, and the operator is not told what the value is for", def[0].Message)
	}
	if strings.Contains(def[0].Message, AnthropicKeyAccount) {
		t.Errorf("the refusal echoes the name: %q", def[0].Message)
	}
	// The fix it names is a different name and not a profile entry: a profile
	// that lists the sentinel is published today and refused by fugaro update
	// (the route that deploys a job for it is not built yet), so a sentence
	// sending the operator there would hand them a route that ends in another
	// refusal, and the two would read as each other's remedy.
	//
	// Mutation (run, restore): restore the "write it as a profile's
	// claude_accounts entry" clause — red here.
	if !strings.Contains(def[0].Message, "pick a different account name") || strings.Contains(def[0].Message, "claude_accounts") {
		t.Errorf("the definition half answers the sentinel with %q: it must name the fix an installation can carry out today (a different account name), not a profile entry that fugaro update refuses", def[0].Message)
	}
	// And the entry is refused whatever else it says: the config it is written
	// in would otherwise be one whose accounts block holds a credential no
	// bundle can carry.
	if ps := ValidateClaudeAccounts(map[string]ClaudeAccount{AnthropicKeyAccount: {}}); len(ps) != 1 || ps[0].Message != anthropicKeyAccountNameMessage {
		t.Errorf("an empty entry named the sentinel = %+v, want the same one refusal", ps)
	}
}

// TestProfileMayNameTheAnthropicKeyWithNothingDefinedForIt: a profile listing
// the sentinel is publishable with no local config entry behind it at all —
// neither a claude_accounts entry (refused above: there is no token to store)
// nor a project_secrets one (the key is stored project-wide by D2's sweep, so
// there is nothing for a profile to require). This is the third branch of
// ValidateProfileClaudeAccounts, and the one a file's refusal would break: it
// resolves an account reference against the two blocks an account can be
// defined in, and the sentinel is not an account.
//
// The mutation is dropping the sentinel's skip: the reference then resolves
// against claude_accounts, finds no entry (there can be none) and refuses the
// one shape the sentinel exists for — a review served by the project's own key
// in an installation that holds no Claude account at all.
func TestProfileMayNameTheAnthropicKeyWithNothingDefinedForIt(t *testing.T) {
	profiles := map[string]JobProfile{"standard": {AllowRepos: []string{"acme/app"}, ClaudeAccounts: []string{AnthropicKeyAccount}}}
	if ps := ValidateProfileClaudeAccounts(profiles, nil, nil); len(ps) != 0 {
		t.Fatalf("a profile naming the project's own key = %+v, want no refusal", ps)
	}
	// The rule it does not relax: the sentinel is still listed once.
	twice := map[string]JobProfile{"standard": {ClaudeAccounts: []string{AnthropicKeyAccount, AnthropicKeyAccount}}}
	ps := ValidateProfileClaudeAccounts(twice, nil, nil)
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "listed twice") {
		t.Fatalf("the sentinel listed twice = %+v, want the one refusal every repeated entry draws", ps)
	}
	// And its neighbours are unaffected: an account this file does not define
	// is still refused, so the skip is about this one value and not about the
	// resolution.
	ghost := map[string]JobProfile{"standard": {ClaudeAccounts: []string{"ghost"}}}
	ps = ValidateProfileClaudeAccounts(ghost, nil, nil)
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "is not defined here") {
		t.Fatalf("an undefined account = %+v, want the resolution's own refusal", ps)
	}
}

// TestAnthropicKeyAccountIsNoJobsToken: the sentinel is a job name and no
// mount. A profile that lists it is publishable (above) and deployable by no
// installation yet — every reader on the way to a job reads a profile's
// claude_accounts as the tokens its jobs mount (AnthropicKeyAccount's comment)
// — and ProjectJobTokenAccount is the one they all ask, so its answer is what
// keeps the halves of that axis from disagreeing: the sentinel resolves to no
// token account, whatever the profile lists.
//
// The mutation is dropping the sentinel's arm: the join answers with the axis
// value itself, claiming a job holds a token no secret exists for, which is
// what the launcher's gate and doctor read.
func TestAnthropicKeyAccountIsNoJobsToken(t *testing.T) {
	pr := JobProfile{ClaudeAccounts: []string{AnthropicKeyAccount}}
	if got, serves := ProjectJobTokenAccount(pr, AnthropicKeyAccount); got != "" || serves {
		t.Errorf("ProjectJobTokenAccount(%v, %q) = (%q, %v), want no token account: the project's own key is held by the platform, and no job mounts it as a Claude token", pr.ClaudeAccounts, AnthropicKeyAccount, got, serves)
	}
	// It is still an entry of the job axis: a job set is computed from the
	// profile's list, so the sentinel adds a job name and no mount.
	if got := ProjectJobAccounts(pr); !slices.Contains(got, AnthropicKeyAccount) {
		t.Errorf("ProjectJobAccounts(%v) = %v, want the sentinel among them: the axis value a profile states is a job of its own", pr.ClaudeAccounts, got)
	}
	// And a token-bearing entry beside it is unaffected.
	both := JobProfile{ClaudeAccounts: []string{AnthropicKeyAccount, DefaultClaudeAccount}}
	if got, serves := ProjectJobTokenAccount(both, ""); got != DefaultClaudeAccount || !serves {
		t.Errorf("ProjectJobTokenAccount(%v, \"\") = (%q, %v), want the default account's token on the no-account job", both.ClaudeAccounts, got, serves)
	}
}
