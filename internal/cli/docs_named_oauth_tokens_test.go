package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"

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
