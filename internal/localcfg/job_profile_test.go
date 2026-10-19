package localcfg

import (
	"strings"
	"testing"
)

// profileConfig is the sample config with one profiles entry, whose fields are
// given by the caller so a test writes only the allow_repos spelling in
// question. The block is the operator's copy of the policy's job profiles
// (config.JobProfile, published by `fugaro project policy publish`).
func profileConfig(fields string) string {
	return sample + "profiles:\n  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h" + fields + " }\n"
}

// TestParseRefusesAProfileAllowReposWrittenEmpty: the key absent means the
// project's allow_repos applies (design §4.1), and a list written with nothing
// in it means the operator wrote a list and left it empty — which the carrier's
// omitempty would carry to the node as the absent key, widening the profile to
// every repository the project enrolls. The mutation is dropping the
// allowReposWritten reader from validate: a blank allow_repos then parses as
// the absent key, publish succeeds, and the profile admits the project's whole
// list.
func TestParseRefusesAProfileAllowReposWrittenEmpty(t *testing.T) {
	for _, fields := range []string{", allow_repos: []", ", allow_repos:", ", allow_repos: ~"} {
		_, err := Parse([]byte(profileConfig(fields)))
		if err == nil {
			t.Errorf("a profile with %q parsed", fields)
			continue
		}
		if !strings.Contains(err.Error(), "profiles.standard.allow_repos") {
			t.Errorf("the refusal for %q does not name the key it refused: %v", fields, err)
		}
	}
	// The key absent is the way a profile says the project's list applies, and
	// it records no written list: the two spellings above are refused, not the
	// absence of the key.
	c, err := Parse([]byte(profileConfig("")))
	if err != nil {
		t.Fatalf("a profile without allow_repos was refused: %v", err)
	}
	if pr := c.Profiles["standard"]; pr.AllowRepos != nil || pr.AllowReposWritten() {
		t.Errorf("a profile without allow_repos = %+v, want no list and no written key", pr)
	}
	// A written list is carried whole, and says that it was written.
	c, err = Parse([]byte(profileConfig(`, allow_repos: ["github/acme/app"]`)))
	if err != nil {
		t.Fatalf("a profile with an allow_repos list was refused: %v", err)
	}
	if pr := c.Profiles["standard"]; len(pr.AllowRepos) != 1 || !pr.AllowReposWritten() {
		t.Errorf("a profile with allow_repos = %+v, want the one repository and the written key", pr)
	}
}

// TestParseRefusesAnUnknownOrMisspeltProfileKey: the profile entry is decoded
// through config.JobProfile's UnmarshalYAML, which sees the mapping node (it
// has to, to tell a written blank list from an absent key) and therefore has to
// repeat the known-fields check the decoder would otherwise apply. The mutation
// is dropping jobProfileKeys from UnmarshalYAML: a misspelt timeout_class then
// parses, publishes a job with no timeout and runs it with Cloud Run's own
// default. The case-variant spelling is refused by yaml.v3 itself, whose known
// fields are compared exactly — the JSON side of the same rule is
// projectpolicy's (it decodes with encoding/json, which matches
// case-insensitively).
func TestParseRefusesAnUnknownOrMisspeltProfileKey(t *testing.T) {
	for _, fields := range []string{", timeout: 6h", ", Allow_Repos: [\"github/acme/app\"]", ", allow_repo: [\"github/acme/app\"]"} {
		if _, err := Parse([]byte(profileConfig(fields))); err == nil {
			t.Errorf("a profile with %q parsed", fields)
		}
	}
	// The documented spelling of every member parses, so the check is not a
	// blanket refusal of the block.
	if _, err := Parse([]byte(profileConfig(`, secrets: [deploy-key], allow_repos: ["github/acme/*"]`))); err != nil {
		t.Fatalf("a fully spelled profile was refused: %v", err)
	}
}

// TestParseResolvesProfileClaudeAccounts: a profile's claude_accounts entry is
// resolved against this same file's claude_accounts and project_secrets blocks
// when the file is read, so an account that names no credential fails where it
// was written rather than at publish, at init, or — worst — as a job that
// mounts a secret the installation never created. The mutation is dropping the
// call from validate: the file loads and the profile publishes.
func TestParseResolvesProfileClaudeAccounts(t *testing.T) {
	accounts := "claude_accounts:\n  acme-team: { allow_data_to: [acme/web] }\n"
	// A configured account parses, and the field is carried whole.
	c, err := Parse([]byte(accountProfileConfig(accounts, ", claude_accounts: [acme-team]")))
	if err != nil {
		t.Fatalf("a configured account was refused: %v", err)
	}
	if got := c.Profiles["standard"].ClaudeAccounts; len(got) != 1 || got[0] != "acme-team" {
		t.Fatalf("claude_accounts = %v", got)
	}

	// An account this file does not define is refused, naming the entry and
	// the block it belongs in.
	_, err = Parse([]byte(accountProfileConfig(accounts, ", claude_accounts: [ops]")))
	if err == nil {
		t.Fatal("a profile naming an undefined account parsed")
	}
	for _, want := range []string{"profiles.standard.claude_accounts", "ops", "claude_accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	// The default account needs the project_secrets entry that stores it
	// project-wide; without one the profile is refused, with one it parses.
	_, err = Parse([]byte(accountProfileConfig("", ", claude_accounts: [default]")))
	if err == nil || !strings.Contains(err.Error(), "project_secrets") {
		t.Fatalf("the default account with no project_secrets entry: %v", err)
	}
	stored := "project_secrets:\n  claude-oauth-token: { allow_data_to: [acme/web] }\n"
	if _, err := Parse([]byte(accountProfileConfig(stored, ", claude_accounts: [default]"))); err != nil {
		t.Fatalf("the default account with its project_secrets entry was refused: %v", err)
	}
}

// accountProfileConfig is the sample config with the caller's account blocks
// before a one-profile block whose fields are the caller's, so a test writes
// only the claude_accounts spelling in question.
func accountProfileConfig(accounts, fields string) string {
	return sample + accounts + "profiles:\n  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h" + fields + " }\n"
}
