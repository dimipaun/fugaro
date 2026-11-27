package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// TestAgentExecutableChangesRefused is AG4b's other half (D3425 as amended,
// D3455): the commands an agent's session may now run still refuse the one flag
// that decides what code every repository runs.
func TestAgentExecutableChangesRefused(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteCheckout(t, settingsRepoYAML(workflowDocs))
	t.Setenv("CLAUDECODE", "1")

	out, errOut, err := execute(t, "workflow", "rm", "docs", "--executable-changes")
	if err == nil {
		t.Fatalf("an agent's session removed a workflow with --executable-changes:\n%s", out)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "--executable-changes") || !strings.Contains(got, initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the workflow refusal lacks the flag or the session's own refusal:\n%s", got)
	}
}
