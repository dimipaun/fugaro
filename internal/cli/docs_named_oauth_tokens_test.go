package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestDocsNamedOAuthTokensDoctorSeverity pins
// docs/design/named-oauth-tokens.md's §6f claims about fugaro doctor's two
// named-account checks against the actual code: claude-accounts-stored's
// severity split (an ordinary, red finding for a missing stored token; a
// warning, never silence, when the check itself could not be answered), and
// oauth-token-allowed's — now implemented, Task 6 review round — same split,
// tied here to the real function rather than text alone, so a change to its
// behavior (not just its wording) fails this test (the same discipline
// docs_project_layer_test.go and docs_upgrade_test.go already hold other
// design/user docs to).
func TestDocsNamedOAuthTokensDoctorSeverity(t *testing.T) {
	data, err := os.ReadFile("../../docs/design/named-oauth-tokens.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"claude-accounts-stored",
		"ordinary, red finding",
		"warning",
		"never silence",
		"`oauth-token-allowed`",
		"config.Agent",
		"OAuthToken",
		"no GCP call at all",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/design/named-oauth-tokens.md no longer says %q", want)
		}
	}
	if strings.Contains(doc, "not yet implemented") {
		t.Error(`docs/design/named-oauth-tokens.md still claims oauth-token-allowed is "not yet implemented", but it is now implemented`)
	}
	if _, ok := reflect.TypeOf(config.Agent{}).FieldByName("OAuthToken"); !ok {
		t.Fatal("config.Agent has no OAuthToken field any more: docs/design/named-oauth-tokens.md's §6f describes " +
			"the oauth-token-allowed check against it")
	}
	// Ties the doc's severity claims to oauthTokenAllowedCheck itself, not
	// just its wording: a disallowed name is an ordinary (non-warning),
	// failing check; an empty token (the repository's default account) is
	// no check at all.
	lc := &localcfg.Config{ClaudeAccounts: map[string]config.ClaudeAccount{"acme-personal": {AllowDataTo: []string{"acme/other"}}}}
	if c := oauthTokenAllowedCheck(lc, "acme/app", "acme-personal", "oauth"); c == nil || c.OK || c.Severity != "" {
		t.Fatalf("oauthTokenAllowedCheck(disallowed) = %+v, want an ordinary, red finding", c)
	}
	if c := oauthTokenAllowedCheck(lc, "acme/app", "", "oauth"); c != nil {
		t.Fatalf("oauthTokenAllowedCheck(empty) = %+v, want no check at all", c)
	}
}

// TestDocsNamedOAuthTokensSetupSkill is the docs test the plan's Task 9
// promises ("keeps the setup skill's commands real, the same pattern
// plugin/skills_lint_test.go already enforces for every other skill"): it
// pins the setup skill's named-Claude-account text to the real, derived
// secret shape (config.ClaudeOAuthTokenPrefix), not merely to wording, so a
// rename of the prefix or a dropped command in the skill text fails this
// test the same way TestDocsNamedOAuthTokensDoctorSeverity already holds the
// design doc to its own code. It also keeps the U2 "not yet verified"
// placeholder honest: until docs/gcp-live-checklist.md's Check 33 answers
// U2, the skill must say plainly that it does not know the exact
// claude-setup-token multi-account sequence, never invent one.
func TestDocsNamedOAuthTokensSetupSkill(t *testing.T) {
	skill, err := os.ReadFile("../../plugin/skills/setup/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := os.ReadFile("../../plugin/skills/setup/reference/decisions.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(skill) + "\n" + string(decisions)
	for _, want := range []string{
		"claude_accounts",
		"agent.oauth_token",
		"not yet verified",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("setup skill text no longer says %q", want)
		}
	}
	namedSecretCmd := "fugaro secrets set " + config.ClaudeOAuthTokenPrefix + "<name> --repo"
	if !strings.Contains(string(skill), namedSecretCmd) {
		t.Errorf("SKILL.md no longer shows %q (config.ClaudeOAuthTokenPrefix changed, or the command was dropped)", namedSecretCmd)
	}
	// U2 (the exact claude-setup-token multi-account sequence) is not
	// answered yet (docs/gcp-live-checklist.md Check 33): the skill must
	// never invent a step this repository has not verified, such as a
	// log-out command or an --account-style flag.
	for _, invented := range []string{
		"claude logout",
		"by running claude logout first",
	} {
		if strings.Contains(text, invented) {
			t.Errorf("setup skill text invents an unverified claude setup-token step (%q); U2 is not yet answered, see docs/gcp-live-checklist.md Check 33", invented)
		}
	}
}

// TestDocsNamedOAuthTokensWorkflowVariantJoin pins two of the design doc's
// own corrections (follow-up review round) against the real code, not just
// their wording: gcp.WorkflowVariant's separator is "_", not "--" (a "--"
// join is not injective, since a workflow or account name may already
// contain "-"), and config.ClaudeAccount has no settable "secret:" field (it
// is always derived). A regression of either in the code, or a reverted
// doc correction, fails this test.
func TestDocsNamedOAuthTokensWorkflowVariantJoin(t *testing.T) {
	if got, want := gcp.WorkflowVariant("build", "acme"), "build_acme"; got != want {
		t.Fatalf("gcp.WorkflowVariant(%q, %q) = %q, want %q: this test's own join assumption is stale", "build", "acme", got, want)
	}
	data, err := os.ReadFile("../../docs/design/named-oauth-tokens.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if want := `workflow + "_" + token`; !strings.Contains(doc, want) {
		t.Errorf("docs/design/named-oauth-tokens.md no longer describes WorkflowVariant's join as %q", want)
	}
	if bad := `workflow + "--" + token`; strings.Contains(doc, bad) {
		t.Errorf("docs/design/named-oauth-tokens.md still claims WorkflowVariant joins with %q, which gcp.WorkflowVariant does not do", bad)
	}
	if _, ok := reflect.TypeOf(config.ClaudeAccount{}).FieldByName("Secret"); ok {
		t.Fatal("config.ClaudeAccount now has a Secret field: docs/design/named-oauth-tokens.md's §3 example was corrected on the assumption it does not")
	}
	if strings.Contains(doc, "secret: claude-oauth-token-acme-personal") {
		t.Error(`docs/design/named-oauth-tokens.md's §3 example still shows a "secret:" field on a claude_accounts entry; the secret is always derived (ClaudeAccountSecret), never a key of that block`)
	}
}
