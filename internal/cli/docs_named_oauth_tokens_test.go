package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestDocsNamedOAuthTokensDoctorSeverity pins
// docs/design/named-oauth-tokens.md's §6f claims about fugaro doctor's
// claude-accounts-stored check against the actual code: the check's ID, its
// severity split (an ordinary, red finding for a missing stored token; a
// warning, never silence, when the check itself could not be answered), and
// the one explicit, documented gap — oauth-token-allowed is not implemented
// because config.Agent has no OAuthToken field yet (Task 6's job). If Task 6
// adds that field, this test fails, forcing a docs update instead of
// letting the claim rot silently (the same discipline
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
		"`oauth-token-allowed`**",
		"is **not yet implemented**",
		"config.Agent",
		"OAuthToken",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/design/named-oauth-tokens.md no longer says %q", want)
		}
	}
	if _, ok := reflect.TypeOf(config.Agent{}).FieldByName("OAuthToken"); ok {
		t.Fatal("config.Agent now has an OAuthToken field: docs/design/named-oauth-tokens.md's §6f claim " +
			"that oauth-token-allowed is blocked on this field is stale, and the check should be implemented")
	}
}
