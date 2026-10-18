package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestProfileDefaultsToStandard: a workflow that names no job profile runs on
// DefaultJobProfile. The mutation is a default of "" (which the policy would
// have to interpret) or of a profile the policy is not required to define.
func TestProfileDefaultsToStandard(t *testing.T) {
	var w Workflow
	if got := w.JobProfileOrDefault(); got != "standard" {
		t.Errorf("an unset job profile defaulted to %q, want standard", got)
	}
	w.JobProfile = "big"
	if got := w.JobProfileOrDefault(); got != "big" {
		t.Errorf("a named job profile became %q", got)
	}
	if DefaultJobProfile != "standard" {
		t.Errorf("DefaultJobProfile = %q, want standard", DefaultJobProfile)
	}
}

// TestJobProfileDoesNotStealTheLayerProfileKey: job_profile and profile are two
// different selections — the project layer's workflow template and the
// project policy's job profile — and one key cannot carry both. A repository
// that uses a layer profile (profile: server) must not thereby also select a
// job profile called server.
func TestJobProfileDoesNotStealTheLayerProfileKey(t *testing.T) {
	var w Workflow
	if err := yaml.Unmarshal([]byte("profile: server\njob_profile: big\n"), &w); err != nil {
		t.Fatal(err)
	}
	if w.Profile != "server" {
		t.Errorf("profile = %q, want the layer profile server", w.Profile)
	}
	if w.JobProfile != "big" {
		t.Errorf("job_profile = %q, want big", w.JobProfile)
	}
	// And a workflow that names only a layer profile keeps the default job
	// profile.
	var onlyLayer Workflow
	if err := yaml.Unmarshal([]byte("profile: server\n"), &onlyLayer); err != nil {
		t.Fatal(err)
	}
	if onlyLayer.JobProfileOrDefault() != "standard" {
		t.Errorf("job profile = %q, want the default", onlyLayer.JobProfileOrDefault())
	}
}

// TestValidateRefusesAMisspeltJobProfile: the name is held to the rule the
// policy holds its own profile names to, so a selection that could never name
// a defined profile fails where it was written.
func TestValidateRefusesAMisspeltJobProfile(t *testing.T) {
	c := &Config{Workflows: map[string]Workflow{"web": {JobProfile: "Not A Profile"}}}
	var found bool
	for _, p := range Validate(c) {
		if strings.HasSuffix(p.Path, "job_profile") {
			found = true
		}
	}
	if !found {
		t.Fatal("Validate accepted job_profile: \"Not A Profile\"")
	}
	// And a name the rule accepts produces no job_profile problem. (A bare
	// Config has plenty of other problems; only this key's is under test.)
	valid := &Config{Workflows: map[string]Workflow{"web": {JobProfile: "standard-bitbucket"}}}
	for _, p := range Validate(valid) {
		if strings.HasSuffix(p.Path, "job_profile") {
			t.Fatalf("Validate refused the valid job profile standard-bitbucket: %s", p)
		}
	}
}

// TestJobProfileClaudeAccountsShape: profiles.<p>.claude_accounts names a
// Claude account, so every entry is held to the rule a claude_accounts KEY is
// held to (ClaudeAccountNameRE, MaxClaudeAccountNameLen) plus the one name
// that is valid as a reference and reserved as a definition: "default", the
// unsuffixed claude-oauth-token. The mutation is dropping the length check
// (the regex alone admits 61 characters: each {0,30} repetition may consume a
// dash plus the alphanumeric it precedes) or accepting any non-empty string:
// a profile then names an account whose derived secret ID is over Secret
// NameRE's 63 characters, and the mount the deployer builds for it is one
// Secret Manager refuses.
func TestJobProfileClaudeAccountsShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"default", true},
		{"acme-team", true},
		{"a", true},
		{strings.Repeat("a", MaxClaudeAccountNameLen), true},
		// 32 characters: the regex's own bound, which MaxClaudeAccountNameLen
		// tightens to 31.
		{strings.Repeat("a", MaxClaudeAccountNameLen+1), false},
		// 61 characters, and the regex alone matches it: 30 repetitions of
		// "-a" behind the leading "a".
		{"a" + strings.Repeat("-a", 30), false},
		{"", false},
		{"Default", false},   // the reservation is exact
		{"ACME", false},      // upper case is not an account name
		{"acme_team", false}, // nor is a name with an underscore
		{"acme-team-", false},
		{"-acme", false},
		{"1acme", false},
		{"acme team", false},
	} {
		msg := ValidClaudeAccountRef(tc.name)
		if tc.ok && msg != "" {
			t.Errorf("ValidClaudeAccountRef(%q) = %q, want it valid", tc.name, msg)
		}
		if !tc.ok && msg == "" {
			t.Errorf("ValidClaudeAccountRef(%q) is valid, want a refusal", tc.name)
		}
		if tc.name != "" && msg != "" && strings.Contains(msg, tc.name) {
			t.Errorf("the refusal for %q echoes the name: %q", tc.name, msg)
		}
	}
}

// TestJobProfileCarriesClaudeAccounts: the field reaches the policy's node
// from the operator's file through ONE declaration (config.JobProfile, which
// projectpolicy.Profile embeds), so both spellings of it must decode — the
// YAML key the file writes and the json tag the node carries. The mutation is
// a field without a json tag, or with one spelled differently: publish would
// write a member projectpolicy's strict decode refuses (DisallowUnknownFields
// over Profile's members, which are read from these tags), and no profile
// could name an account at all.
func TestJobProfileCarriesClaudeAccounts(t *testing.T) {
	var pr JobProfile
	if err := yaml.Unmarshal([]byte("provider: github\nclaude_accounts: [acme-team, default]\n"), &pr); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pr.ClaudeAccounts, ","); got != "acme-team,default" {
		t.Fatalf("claude_accounts from YAML = %q", got)
	}
	if err := json.Unmarshal([]byte(`{"provider":"github","claude_accounts":["ops"]}`), &pr); err != nil {
		t.Fatal(err)
	}
	if len(pr.ClaudeAccounts) != 1 || pr.ClaudeAccounts[0] != "ops" {
		t.Fatalf("claude_accounts from JSON = %v", pr.ClaudeAccounts)
	}
	// An omitted key is no account, and it is left out of the node (the tag's
	// omitempty): a profile that serves none must not publish an empty list,
	// which the policy's own rules read as a written one.
	out, err := json.Marshal(JobProfile{Provider: "github"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "claude_accounts") {
		t.Fatalf("a profile with no accounts marshalled as %s", out)
	}
}

// TestProfileAccountMustBeConfigured: a profile's claude_accounts entry is
// resolved against the two blocks an account can be defined in — claude_accounts
// for a named account, project_secrets for the default one, whose token is the
// reserved claude-oauth-token and whose project-wide copy exists only when that
// entry says so. The mutation is resolving nothing: the file loads, publish
// derives an account with an empty allow_repos (or drops it), and the job the
// profile names either mounts a secret the installation never creates or
// admits every repository.
func TestProfileAccountMustBeConfigured(t *testing.T) {
	accounts := map[string]ClaudeAccount{"acme-team": {AllowDataTo: []string{"acme/app"}}}
	profiles := func(refs ...string) map[string]JobProfile {
		return map[string]JobProfile{"standard": {Provider: "github", ClaudeAccounts: refs}}
	}

	// Configured: a named account of claude_accounts, and the default one with
	// the project_secrets entry that stores it.
	if ps := ValidateProfileClaudeAccounts(profiles("acme-team", "default"),
		accounts, map[string]ProjectSecret{"claude-oauth-token": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("a configured account was refused: %v", ps)
	}

	// Undefined: refused, naming the entry, the account and where it belongs.
	ps := ValidateProfileClaudeAccounts(profiles("ops"), accounts, nil)
	if len(ps) != 1 {
		t.Fatalf("an undefined account gave %d problems: %v", len(ps), ps)
	}
	if got := ps[0].Path; got != "profiles.standard.claude_accounts[0]" {
		t.Errorf("the refusal's path = %q, want the entry the account was written in", got)
	}
	for _, want := range []string{"ops", "claude_accounts", "allow_data_to"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the refusal does not say %q: %s", want, ps[0].Message)
		}
	}

	// The default account without a project_secrets entry: refused, naming the
	// logical secret and the block that stores it. This is the one case the
	// account IS "configured" in the sense of existing — claude-oauth-token is
	// the name every repository already uses — and it is precisely the one a
	// project job cannot mount without the entry (ProjectScopeByDefault refuses
	// the bare name as a swept-in reserved secret, deliberately).
	ps = ValidateProfileClaudeAccounts(profiles("default"), nil, nil)
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "project_secrets") || !strings.Contains(ps[0].Message, "claude-oauth-token") {
		t.Fatalf("the default account with no project_secrets entry: %v", ps)
	}

	// Listed twice: each account once.
	ps = ValidateProfileClaudeAccounts(profiles("acme-team", "acme-team"), accounts,
		map[string]ProjectSecret{"claude-oauth-token": {AllowDataTo: []string{"acme/app"}}})
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "twice") {
		t.Fatalf("a repeated account: %v", ps)
	}

	// A name that is not a name: reported as such, and not resolved (the
	// resolution's question is about a name).
	ps = ValidateProfileClaudeAccounts(profiles("Acme Team"), accounts, nil)
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "account name") {
		t.Fatalf("a malformed account name: %v", ps)
	}
	if strings.Contains(ps[0].Message, "Acme Team") {
		t.Errorf("the refusal echoes the malformed name: %s", ps[0].Message)
	}
}

// TestMountsAnthropicKeyBothSpellings: D4 refuses a profile that lists Claude
// accounts and mounts an Anthropic API key, and the two spellings a profile
// can hold one in are the reserved secret's name and its variable. The
// mutation is matching only one of them: the other publishes a profile whose
// jobs hold a key no OAuth run of them can use, which is exactly the state the
// rule exists to make unrepresentable.
func TestMountsAnthropicKeyBothSpellings(t *testing.T) {
	for _, s := range []string{AnthropicSecret, ReservedSecrets[AnthropicSecret]} {
		if !MountsAnthropicKey(s) {
			t.Errorf("MountsAnthropicKey(%q) is false", s)
		}
	}
	if AnthropicSecret != "anthropic-api-key" || ReservedSecrets[AnthropicSecret] != "ANTHROPIC_API_KEY" {
		t.Fatalf("the two spellings moved: %q / %q", AnthropicSecret, ReservedSecrets[AnthropicSecret])
	}
	for _, s := range []string{"", "deploy-key", "claude-oauth-token", "anthropic-api-key-2", "FUGARO_PROVIDER_KEY_ANTHROPIC_API_KEY"} {
		if MountsAnthropicKey(s) {
			t.Errorf("MountsAnthropicKey(%q) is true", s)
		}
	}
}
