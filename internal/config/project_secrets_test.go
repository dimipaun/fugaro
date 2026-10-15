package config

import "testing"

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
