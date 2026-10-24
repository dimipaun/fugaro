package cli

import (
	"strings"
	"testing"
	"time"
)

// TestRunTokenOutsideACheckoutRefusesDisallowedName is review finding #2:
// claudeAccountAllowed used to skip the allow-list check entirely when no
// local checkout was available to read agent.auth from, so
// `fugaro run --repo X --token N` run outside any checkout (the common case
// for a bot or CI launcher, which has no reason to have X checked out) was
// checked for shape only, never membership in
// config.AllowedClaudeAccounts. Fixed: the allow-list is always checked for
// a non-default name, checkout or none. Refused before any network call
// (no execution started).
func TestRunTokenOutsideACheckoutRefusesDisallowedName(t *testing.T) {
	f := newCloudFixture(t) // acme/app, no local checkout anywhere in this test
	_, _, err := execute(t, "run", "--repo", "acme/app", "--token", "evil-name", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "evil-name") || !strings.Contains(err.Error(), "claude_accounts") ||
		!strings.Contains(err.Error(), "local config") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none (no network call)", n)
	}
}

// TestRunTokenRefusesWhenAuthIsNotOAuth is review finding #2's second rule:
// when the checkout IS known and its workflow's agent.auth is not oauth,
// naming an account is refused outright, even one the local config does
// allow for this repository — no secret is ever mounted for a non-oauth
// auth, so letting the name through would silently do nothing.
func TestRunTokenRefusesWhenAuthIsNotOAuth(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig) // allows acme-personal for acme/other
	recipeCheckout(t, f, "", nil)           // agent.auth: api-key
	_, _, err := execute(t, "run", "--run-id", "20261012-100000-b3b3", "--token", "acme-personal", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-personal") || !strings.Contains(err.Error(), "agent.auth is oauth") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRunRefusesAnAgentAuthTheRepositoryDoesNotDefine: the account gate both
// decides on agent.auth and prints it, and the two documents that value can
// come from are not read alike. The checkout's own config is validated
// (config.Validate: agent.auth is one of config.AgentAuths), but the
// fugaro.yaml at the ref the run checks out is read by config.AgentOf, which
// decodes strictly and validates nothing — so a repository can commit an
// agent.auth of its own spelling and have it reach the gate. Printed raw into
// the refusal, a newline in it adds a line to what the operator reads
// (safetext.MultiLine keeps newlines) and an escape sequence rewrites it; read
// as "some auth that is not oauth", it is refused with a sentence about a
// value nobody defined. Both halves are fixed: a value this Fugaro cannot
// place is refused as such, escaped.
//
// Mutation (verified, run and restored): take the value as it comes (drop the
// KnownAgentAuth branch, keeping the raw interpolation) — this test fails, the
// refusal carrying the newline and the escape sequence, and naming no
// expected value.
func TestRunRefusesAnAgentAuthTheRepositoryDoesNotDefine(t *testing.T) {
	f := newProjectJobFixture(t)
	isolateCache(t)
	// The ref commits an auth this Fugaro does not define: YAML's double
	// quotes read "\n" and "\u001b[31m" as the characters themselves, which is
	// exactly how a committed file delivers them. The working tree carries the
	// same file with a real auth, so what refuses the launch below is the ref's
	// value and not a file this launcher could not read.
	projectJobCheckoutYAMLAt(t, projectJobRefRepoYAML("oauth"),
		projectJobRefRepoYAML(`"oauth\nsecond line \u001b[31mred"`))

	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261019-120000-cccc", "--token", "acme-personal", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a launch whose ref names an undefined agent.auth: %v, want a user refusal", err)
	}
	if !strings.Contains(err.Error(), "agent.auth") || !strings.Contains(err.Error(), "api-key") {
		t.Errorf("the refusal does not say what agent.auth may be: %v", err)
	}
	if strings.ContainsAny(err.Error(), "\n\x1b") {
		t.Errorf("the refusal carries the repository's own text as itself, so it rewrites the lines around it: %q", err.Error())
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
}

// TestFollowUpOutsideACheckoutRefusesDisallowedInheritedAccount is review
// finding #2's follow-up half: a follow-up's inherited account name comes
// from the previous run's own task.json, bucket data a launcher can write
// directly — forged, or simply naming an account claude_accounts no longer
// (or never did) allow for this repository. With no local checkout present
// (so there is no agent.auth to read either), the allow-list must still
// refuse it, not wave it through as "unknown, so unchecked".
func TestFollowUpOutsideACheckoutRefusesDisallowedInheritedAccount(t *testing.T) {
	f := newCloudFixture(t) // acme/app, no local checkout anywhere, no claude_accounts configured
	root := firstRunSpec(rootID)
	root.OAuthToken = "acme-personal"
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "more work")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "acme-personal") || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}
