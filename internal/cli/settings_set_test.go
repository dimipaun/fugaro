package cli

import (
	"strings"
	"testing"
)

// TestSettingsSetRefusesAKeyOwnedByAnotherFamily: `settings set` is the
// generic path, and a key a dedicated family writes is refused here with that
// family's command named rather than written by a second path (D853). The
// family check runs before the layer is even looked up, so a refusal never
// depends on where the write would have landed.
func TestSettingsSetRefusesAKeyOwnedByAnotherFamily(t *testing.T) {
	settingsFixture(t, "")
	_, _, err := execute(t, "settings", "set", "agent.model", "claude-sonnet-5", "--layer", "repo")
	if err == nil {
		t.Fatal("settings set wrote a key the models family owns")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "fugaro models") {
		t.Errorf("the refusal does not name the family's command: %v", err)
	}
}
