package config

import (
	"slices"
	"strings"
	"testing"
)

// D2313 (docs/shared-jobs-default/reconciliation.md): a repository's `secrets:`
// is accepted and ignored with a warning, "in a way similar to
// repository-level images" — disabled for now, the containers and their values
// left alone, the code kept so the feature can return.
//
// What these tests hold is the config half alone, which is the half that must
// not depend on a command: which names the ruling is about
// (RepoLevelSecret), which of them a file declares (RepoSecretsIgnored) and
// what the one warning says (RepoSecretsWarnings, which `fugaro validate`
// prints through CheckWarnings and `fugaro update` prints as M7's line). The
// other half — that a shared job mounts none of them and that the credentials
// service is told of none — is internal/cli's, and its test is
// TestWorkflowSecretsAreAcceptedAndIgnoredBySharedJobs.

// repoSecretYAML is a file whose two workflows declare three secrets between
// them: two ordinary repository-level names, one of them declared by both
// workflows, so the warning's own dedup is exercised. It is a file
// `fugaro validate` accepts — D2313's first word — and the warning below is
// the only thing it says about the block.
const repoSecretYAML = `version: 1
project: acme
git: { provider: github }
workflows:
  api:
    commands: { build: make, test: make test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
  app:
    commands: { build: make, test: make test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
      - { name: sandbox-probe, env: PROBE }
`

// TestRepoLevelSecretNames: the ruling is about a name a repository declares
// for itself, and every name the platform decides the scope of is not one —
// a reserved name, the Claude OAuth namespace, the Codex harness's names and
// the credentials service's bundle. A repository's copy of one of those is the
// object doctor's `secret-scope-shadowed` warns about, which is a different
// finding about a different thing: reading it as the disabled feature would
// tell a person the name is ignored when the value is one Fugaro itself
// mounts.
func TestRepoLevelSecretNames(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{"npm-token", true},
		{"sandbox-probe", true},
		{"claude-oauth-token-work", false},
		{"claude-oauth-token", false},
		{CredentialsBundleSecret, false},
		{BitbucketSecret, false},
		{AnthropicSecret, false},
		{"not a name", false},
		{"", false},
	} {
		if got := RepoLevelSecret(c.name); got != c.want {
			t.Errorf("RepoLevelSecret(%q) = %v, want %v", c.name, got, c.want)
		}
	}
	// The exclusion is not only belt to Validate's brace: RepoSecretsIgnored
	// is asked of any Config a caller holds, and a reserved name written into
	// one (Validate refuses the file, but a caller may have built the struct
	// itself) must not be reported as a repository-level secret. The reserved
	// name is the case that matters — it is the one that looks like an
	// application secret and is not.
	c := &Config{Workflows: map[string]Workflow{"app": {Secrets: []Secret{
		{Name: "npm-token", Env: "NPM_TOKEN"},
		{Name: BitbucketSecret, Env: "BITBUCKET_TOKEN"},
		{Name: CredentialsBundleSecret, Env: "BUNDLE"},
	}}}}
	if got := RepoSecretsIgnored(c); !slices.Equal(got, []string{"npm-token"}) {
		t.Errorf("RepoSecretsIgnored = %v, want the application secret alone", got)
	}
}

// TestRepoSecretsWarningsAreOnePerRepository: the ruling is one fact about the
// file — this repository declares secrets and no job mounts them — so it is
// said once, at the first workflow's `secrets:` a reader of the file would find
// it at, naming every disabled secret in name order and each one once. A file
// that declares none gets no warning at all, which is why the path is built
// only where there is one.
func TestRepoSecretsWarningsAreOnePerRepository(t *testing.T) {
	// The warning is the release's behaviour, not a switch a test sets: this is
	// the one place that reads the shipped value, because the CLI's own suite
	// opens the door (main_test.go, the D1900 pair it is modelled on).
	if !RepoSecretsOff {
		t.Error("the CLI ships with repository-level secrets disabled (D2313)")
	}
	c, problems := Parse([]byte(repoSecretYAML))
	if len(problems) > 0 {
		t.Fatalf("a file declaring repository-level secrets is accepted, D2313; Parse = %v", problems)
	}
	if got := RepoSecretsIgnored(c); !slices.Equal(got, []string{"npm-token", "sandbox-probe"}) {
		t.Fatalf("RepoSecretsIgnored = %v, want the two repository-level names once each and in name order", got)
	}
	ws := RepoSecretsWarnings(c)
	if len(ws) != 1 {
		t.Fatalf("RepoSecretsWarnings = %v, want one warning for the file", ws)
	}
	// workflows.api is the first workflow in name order: the path is where a
	// reader finds the declaration the warning is about, not where the names
	// were collected from.
	if ws[0].Path != "workflows.api.secrets" {
		t.Errorf("path = %q, want workflows.api.secrets", ws[0].Path)
	}
	for _, want := range []string{"npm-token", "sandbox-probe", "disabled for now", "project_secrets", "workflow_secrets"} {
		if !strings.Contains(ws[0].Message, want) {
			t.Errorf("the warning %q does not say %q", ws[0].Message, want)
		}
	}
	// The platform's own names are not named as disabled: the file declares
	// them and the ruling is not about them.
	if strings.Contains(ws[0].Message, ClaudeOAuthTokenPrefix) {
		t.Errorf("the warning %q reads a Claude account's token as a repository-level secret", ws[0].Message)
	}
	// A file declaring none gets nothing, and the door is what decides: with it
	// off, the same file is served the pre-ruling answer (the machinery the
	// ruling keeps, and internal/cli's suite runs with it off).
	bare, problems := Parse([]byte(baseYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if ws := RepoSecretsWarnings(bare); ws != nil {
		t.Errorf("a file declaring no secret got %v", ws)
	}
	t.Cleanup(func() { RepoSecretsOff = true })
	RepoSecretsOff = false
	if ws := RepoSecretsWarnings(c); ws != nil {
		t.Errorf("with the door off: %v, want none", ws)
	}
}

// TestCheckWarningsCarriesTheRepoSecretsWarning: `fugaro validate` prints
// warnings through CheckWarnings and nothing else, so the ruling reaches a
// person only if that function carries it. The runtime warning beside it is the
// one CheckWarnings already had, and both are warnings: the file is valid.
func TestCheckWarningsCarriesTheRepoSecretsWarning(t *testing.T) {
	c, problems := Parse([]byte(repoSecretYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	ws := CheckWarnings(c, t.TempDir())
	found := false
	for _, w := range ws {
		if w.Path == "workflows.api.secrets" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarnings = %v, want the repository-level secrets warning among them", ws)
	}
}
