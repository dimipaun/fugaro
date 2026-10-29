package config

import (
	"strings"
	"testing"
)

func TestProjectSecretAllowedRepos(t *testing.T) {
	secrets := map[string]ProjectSecret{
		"anthropic-api-key": {AllowDataTo: []string{"acme/app", "Acme/Sandbox"}},
	}
	if !AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/app") {
		t.Fatal("an allowed repo was refused")
	}
	if !AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/sandbox") {
		t.Fatal("case-insensitive match failed")
	}
	if AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/other") {
		t.Fatal("an unlisted repo was allowed")
	}
	if AllowedForProjectSecret(secrets, "bitbucket-token", "acme/app") {
		t.Fatal("a name with no entry was allowed")
	}
}

func TestValidateProjectSecretsRefusesNonReservedName(t *testing.T) {
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"db-password": {AllowDataTo: []string{"acme/app"}}})
	if len(ps) != 1 {
		t.Fatalf("ValidateProjectSecrets = %+v, want one problem", ps)
	}
}

func TestValidateProjectSecretsAcceptsReservedName(t *testing.T) {
	if ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("a valid block was refused: %+v", ps)
	}
}

func TestValidateProjectSecretsAcceptsBareClaudeOAuthToken(t *testing.T) {
	// S1, the regression guard: this is the exact case a naive
	// ClaudeAccountBySecret-based collision check would have wrongly
	// refused (ClaudeAccountBySecret(accounts, "claude-oauth-token")
	// always matches the implicit default account, configured or not).
	// project_secrets.claude-oauth-token is design §3's own documented,
	// intended configuration and must never be refused.
	if ps := ValidateProjectSecrets(map[string]ProjectSecret{"claude-oauth-token": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("the bare default claude-oauth-token name was refused: %+v", ps)
	}
}

func TestValidateProjectSecretsRefusesWildcard(t *testing.T) {
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{"*"}}})
	if len(ps) == 0 {
		t.Fatal("the literal \"*\" was accepted as a repository slug for project_secrets; only providers: gets the wildcard rule (design §3a)")
	}
}

func TestValidateProjectSecretsRefusesMalformedRepo(t *testing.T) {
	for _, r := range []string{"not-a-slug", "../..", "./."} {
		if ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{r}}}); len(ps) == 0 {
			t.Errorf("%q was accepted as a repository slug", r)
		}
	}
}

// TestValidateProjectSecretsAcceptsEmptyAllowList: for project_secrets,
// unlike providers:, an empty/absent list is a valid, meaningful state --
// "shared with nobody" (design §3). Only ValidateModelProviders refuses an
// explicit [].
func TestValidateProjectSecretsAcceptsEmptyAllowList(t *testing.T) {
	for _, tc := range []struct {
		name string
		ps   ProjectSecret
	}{
		{"absent", ProjectSecret{}},
		{"explicit empty", ProjectSecret{AllowDataTo: []string{}}},
	} {
		if ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": tc.ps}); len(ps) != 0 {
			t.Errorf("%s: refused: %+v", tc.name, ps)
		}
		if AllowedForProjectSecret(map[string]ProjectSecret{"anthropic-api-key": tc.ps}, "anthropic-api-key", "acme/app") {
			t.Errorf("%s: an empty allow-list allowed a repo", tc.name)
		}
	}
}

// TestProjectScopeByDefaultIsNotTheWholeReservedSet pins the two names D2 does
// not sweep in: bitbucket-token, because a Bitbucket credential is a
// repository access token issued for one repository and works for no other
// (PerRepositorySecret, the owner's ruling of 2026-10-15), and the bare
// claude-oauth-token, which is person-owned (design §7). Everything else in
// ReservedSecrets is swept in, so a reserved name added without a decision
// here shows up as a failure rather than silently taking a scope.
func TestProjectScopeByDefaultIsNotTheWholeReservedSet(t *testing.T) {
	want := map[string]bool{
		BitbucketSecret:      false,
		"github-app-key":     true,
		"claude-oauth-token": false,
		AnthropicSecret:      true,
	}
	for name := range ReservedSecrets {
		if _, ok := want[name]; !ok {
			t.Fatalf("%q is a reserved secret this test does not classify: decide whether D2 sweeps it into project scope", name)
		}
	}
	for name, swept := range want {
		if _, ok := ReservedSecrets[name]; !ok {
			t.Fatalf("%q is not a reserved secret", name)
		}
		if got := ProjectScopeByDefault(name); got != swept {
			t.Errorf("ProjectScopeByDefault(%q) = %v, want %v", name, got, swept)
		}
	}
	if !PerRepositorySecret(BitbucketSecret) {
		t.Errorf("PerRepositorySecret(%q) = false", BitbucketSecret)
	}
	for _, name := range []string{"github-app-key", AnthropicSecret, "claude-oauth-token", "npm-token"} {
		if PerRepositorySecret(name) {
			t.Errorf("PerRepositorySecret(%q) = true: only a credential issued for one repository is one", name)
		}
	}
	// A named account's derived secret is not reserved at all; its account's
	// own allow_data_to stays its rule (finding 12).
	if ProjectScopeByDefault(ClaudeAccountSecret("acme-personal")) {
		t.Error("a named Claude account's derived secret was swept into project scope")
	}
}

// TestProjectSecretAllowsDataSegmentWildcard: an allow_data_to entry may use
// "*" inside a segment (myworkspace/*, myworkspace/api-*), which never
// crosses "/", and matching stays case-insensitive like an exact entry's
// (owner ruling, 2026-10-27, Bitbucket design D464). A pattern with a
// different number of segments admits nothing, so a repository path that
// nests is not swept in by a workspace-wide entry.
func TestProjectSecretAllowsDataSegmentWildcard(t *testing.T) {
	secrets := map[string]ProjectSecret{
		"bitbucket-token": {AllowDataTo: []string{"myworkspace/*", "acme/api-*"}},
	}
	for _, tc := range []struct {
		repo string
		want bool
	}{
		{"myworkspace/api", true},
		{"MyWorkspace/Api", true},
		{"MYWORKSPACE/API", true},
		{"myworkspace/api-core", true},
		{"acme/api-core", true},
		{"acme/api", false},        // "api-*" needs the dash
		{"other/api", false},       // another owner
		{"myworkspace/a/b", false}, // "*" never crosses "/"
		{"myworkspace", false},     // no owner/name at all
		{"myworkspace/", false},    // empty segment
		{"", false},
	} {
		if got := AllowedForProjectSecret(secrets, "bitbucket-token", tc.repo); got != tc.want {
			t.Errorf("AllowedForProjectSecret(myworkspace/*, %q) = %v, want %v", tc.repo, got, tc.want)
		}
	}
	// An exact entry beside a wildcard one still decides by itself.
	both := map[string]ProjectSecret{"bitbucket-token": {AllowDataTo: []string{"acme/app", "myworkspace/*"}}}
	if !AllowedForProjectSecret(both, "bitbucket-token", "acme/app") {
		t.Error("an exact entry beside a wildcard one stopped matching")
	}
	if AllowedForProjectSecret(both, "bitbucket-token", "acme/other") {
		t.Error("the wildcard admitted another owner's repository")
	}
}

// TestProjectSecretAllowsDataFailsClosedOnARefusedEntry: ValidateProjectSecrets
// refuses a wildcard owner and a bare "*", and the matcher must not honour one
// either -- a config that reached AllowsData without validation (an older
// file, a path that skips Validate) mounts nothing rather than everything.
func TestProjectSecretAllowsDataFailsClosedOnARefusedEntry(t *testing.T) {
	for _, entry := range []string{"*", "*/*", "*/api", "my*space/api"} {
		secrets := map[string]ProjectSecret{"bitbucket-token": {AllowDataTo: []string{entry}}}
		if AllowedForProjectSecret(secrets, "bitbucket-token", "myworkspace/api") {
			t.Errorf("entry %q admitted myworkspace/api: a wildcard owner segment must admit nothing", entry)
		}
	}
}

// TestValidateProjectSecretsAcceptsSegmentWildcards: the entries the ruling
// allows. Each is one owner's repositories (or one family of them), never
// every workspace.
func TestValidateProjectSecretsAcceptsSegmentWildcards(t *testing.T) {
	for _, entry := range []string{
		"myworkspace/*",
		"MyWorkspace/*",
		"myworkspace/api-*",
		"myworkspace/*-core",
		"myworkspace/a*b",
		"myworkspace/api", // exact entries keep working
	} {
		ps := ValidateProjectSecrets(map[string]ProjectSecret{"bitbucket-token": {AllowDataTo: []string{entry}}})
		if len(ps) != 0 {
			t.Errorf("entry %q was refused: %+v", entry, ps)
		}
	}
}

// TestValidateProjectSecretsRefusesWideningWildcards: the three shapes the
// ruling keeps out, each narrower than a published policy's allow_repos: a
// wildcard owner segment (which refuses the bare "*" and "*/*" with it), a
// pattern with more than two "*", and anything that is not owner/name. The
// problem names the field the owner has to edit and says why.
func TestValidateProjectSecretsRefusesWideningWildcards(t *testing.T) {
	for _, tc := range []struct {
		entry string
		want  string
	}{
		{"*", `"*" is not accepted`},
		{"*/*", "owner segment must be literal"},
		{"*/api", "owner segment must be literal"},
		{"my*space/api", "owner segment must be literal"},
		{"myworkspace/a*b*c*d", "at most 2"},
		{"myworkspace/a/b", "owner/name"},
		{"myworkspace/", "owner/name"},
		{"/api", "owner/name"},
		{"myworkspace/..", "owner/name"},
	} {
		ps := ValidateProjectSecrets(map[string]ProjectSecret{"bitbucket-token": {AllowDataTo: []string{tc.entry}}})
		if len(ps) != 1 {
			t.Errorf("entry %q: ValidateProjectSecrets = %+v, want one problem", tc.entry, ps)
			continue
		}
		if ps[0].Path != "project_secrets.bitbucket-token.allow_data_to" {
			t.Errorf("entry %q: problem path = %q, want the field the owner edits", tc.entry, ps[0].Path)
		}
		if !strings.Contains(ps[0].Message, tc.want) {
			t.Errorf("entry %q: message %q does not say %q", tc.entry, ps[0].Message, tc.want)
		}
	}
}
