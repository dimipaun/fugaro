package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// The two tests below pin the wording this feature's security cost is stated
// in: SECURITY.md's residual for a project job's Claude account token (plan
// task 3, design docs/design/project-named-oauth-tokens.md §6.1, owner ruling
// D8). The wording is the feature's deliverable here — the carrier is a static
// mount, so what a reader is told about it is the thing a later change must
// keep true — and the tests hold it the way docs_security_test.go and
// docs_named_oauth_tokens_test.go hold the rest of the file: the sentences in
// SECURITY.md, tied to the code that makes them facts rather than to the text
// alone.

// TestDocsProjectNamedOAuthTokensSecurityMdResidual: SECURITY.md carries all
// five things D8 names — no launcher read, every run of an account's job holds
// its token, a hostile run can use it during its own run, the account is the
// blast radius, and the carrier is a stopgap pending the credentials/security
// service — plus the case the default account adds: a profile that lists
// `default` gives that token to every run of its no-account job, whatever the
// run's own agent.auth is, documented rather than refused.
//
// Mutation (verified, run and restored): delete the "A hostile run can use the
// token during its own run" sentence — this fails naming it. The same holds
// for any of the other sentences below, which is the point: a residual quietly
// dropped from SECURITY.md is a claim the document no longer makes.
func TestDocsProjectNamedOAuthTokensSecurityMdResidual(t *testing.T) {
	data, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		// The bullet and its subject.
		"A project job holds its Claude account's OAuth token, and every run of that job holds it too.",
		"gcp.ProjectJobName",
		"internal/infra/projectjobs.go",
		"claude-oauth-token-<name>",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"it is there from the start of every run of that job and no run can decline it",
		// D8's five, one by one.
		"no launcher reads it",
		"which only the job and build accounts hold",
		"A hostile run can use the token during its own run, and after it",
		"The account is the blast radius, not the repository",
		"This carrier is a stopgap",
		// The blast-radius rule, named where a reader can check it.
		"claude_accounts.<name>.allow_data_to",
		"accounts.<name>.allow_repos",
		"decides **which runs** are given that token",
		"neither of them scopes **what the token can do**",
		// The default account's case: documented, not refused.
		"A profile that lists `default` gives that token to every run of its no-account job, whatever that run's `agent.auth` is",
		"documented rather than refused",
		// Where the D4 pair is actually refused, and the api-key runs that are
		// admitted here all the same: an api-key run whose models go through
		// the gateway mounts no Anthropic key and needs none
		// (agent.BuildEnv's api-key arm), so "never API-key ones" is not this
		// document's to say — and the refusal is not `fugaro validate`'s,
		// which reads no policy node at all.
		"`projectpolicy.validProfileAccounts`",
		"`fugaro validate` reads no policy node and is not one of the three",
		"or api-key runs under the gateway",
		"`agent.BuildEnv`",
		"fugaro doctor",
		// The swap's own destination, so the stopgap names what replaces it.
		"docs/credentials-service/README.md",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("SECURITY.md no longer says %q: design project-named-oauth-tokens.md §6.1 (D8) is what this feature's cost is stated in", want)
		}
	}
	// The residual is a residual, not a claim that the token is safe: the
	// document must not say a run cannot read it, and it must not promise that
	// nothing ever holds it before the service lands (that sentence is the
	// stopgap's, about the swap, and it says "after which").
	for _, banned := range []string{
		"a run cannot read the token",
		"no job holds the token",
		"the token cannot be copied",
	} {
		if strings.Contains(doc, banned) {
			t.Errorf("SECURITY.md says %q, which is not true until the credentials service replaces the mount: the token is in the job's container from the start of every run of it", banned)
		}
	}
}

// TestDocsProjectNamedOAuthTokensAccountJobFacts ties the paragraph's claims to
// the code that decides them, so a change to the behavior — not only to the
// wording — fails this test: the mount's secret name is derived from the
// account (ClaudeAccountSecret), which job holds which account's token is
// ProjectJobTokenAccount's answer (the default account riding the no-account
// job, and no token there for a profile that does not list it), and a job's
// name is a function of the (profile, provider, account) triple and of no
// mount — which is why SECURITY.md's stopgap can say the swap renames no job.
func TestDocsProjectNamedOAuthTokensAccountJobFacts(t *testing.T) {
	if got := config.ClaudeAccountSecret(config.DefaultClaudeAccount); got != config.ClaudeOAuthToken {
		t.Fatalf("ClaudeAccountSecret(%q) = %q, want the unsuffixed %q SECURITY.md's paragraph names",
			config.DefaultClaudeAccount, got, config.ClaudeOAuthToken)
	}
	if got := config.ClaudeAccountSecret("acme-personal"); got != "claude-oauth-token-acme-personal" {
		t.Fatalf("ClaudeAccountSecret(acme-personal) = %q, want the claude-oauth-token-<name> shape SECURITY.md's paragraph names", got)
	}
	// The default account rides the profile's no-account job when the profile
	// lists it, and no job of that profile holds a token when it does not.
	listsDefault := config.JobProfile{ClaudeAccounts: []string{config.DefaultClaudeAccount, "acme-personal"}}
	for _, tc := range []struct {
		name    string
		pr      config.JobProfile
		account string
		want    string
		serves  bool
	}{
		{"the no-account job of a profile listing default", listsDefault, "", config.DefaultClaudeAccount, true},
		{"a named account's own job", listsDefault, "acme-personal", "acme-personal", true},
		{"the no-account job of a profile listing only named accounts", config.JobProfile{ClaudeAccounts: []string{"acme-personal"}}, "", "", false},
		{"an account the profile does not list", listsDefault, "acme-work", "", false},
	} {
		if got, serves := config.ProjectJobTokenAccount(tc.pr, tc.account); got != tc.want || serves != tc.serves {
			t.Errorf("%s: ProjectJobTokenAccount = (%q, %v), want (%q, %v)", tc.name, got, serves, tc.want, tc.serves)
		}
	}
	// A job's name depends on the (profile, provider, account) triple and not
	// on what it mounts: the account job and the no-account job of one profile
	// are different names, which is what makes the mount a thing a job holds
	// rather than a thing a run chooses.
	noAccount := gcp.ProjectJobName("standard", "github", "")
	named := gcp.ProjectJobName("standard", "github", "acme-personal")
	if noAccount == named {
		t.Fatalf("gcp.ProjectJobName gives one name (%q) to a profile's no-account and account jobs: SECURITY.md's paragraph and its stopgap both rest on the account being part of the job's own name", noAccount)
	}
	if !strings.Contains(noAccount, "fugaro-p-standard-github") || !strings.Contains(named, "acme") {
		t.Fatalf("gcp.ProjectJobName(standard, github, ...) = %q / %q, want the profile and the account in the readable part of the name", noAccount, named)
	}
}
