package cli

import (
	"strings"
	"testing"
)

// The workspace writes' fixture: project aurora in a config home of its own,
// with no coding-agent marker set, so a write that is meant to land is not
// refused by the session the test binary happens to run in. It is the models
// writes' fixture (modelsWriteHome), for the same reason: the two families
// share the one write path and the one fence.
func workspaceWriteHome(t *testing.T) (home, path string) {
	t.Helper()
	return modelsWriteHomeWith(t, "")
}

// TestWorkspaceAddWritesOneKeyAndKeepsEveryOtherByte is the red step: the
// command did not exist. It is the test the task exists for -- a workspace
// membership must be one key added to a file that is otherwise byte-identical
// (comments, ordering, quoting), the same promise the `models` writes make and
// the same one `yamledit` exists to keep.
func TestWorkspaceAddWritesOneKeyAndKeepsEveryOtherByte(t *testing.T) {
	_, path := workspaceWriteHome(t)
	before := readFileString(t, path)

	out, _, err := execute(t, "workspace", "add", "workspace-alpha", "--firebase-project", "project-ws",
		"--issuer", "https://securetoken.google.com/project-ws",
		"--assertion-key", "marker-assertion-key",
		"--service-account", "marker-workspace@project-ws.iam.gserviceaccount.com",
		"--yes")
	if err != nil {
		t.Fatalf("workspace add: %v\n%s", err, out)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "workspace: workspace-alpha\n") {
		t.Errorf("the membership key was not written:\n%s", after)
	}
	// Every line of the file as it was is still there, in order: the edit adds
	// the two keys and rewrites nothing else.
	rest := after
	for _, line := range strings.Split(strings.TrimSuffix(before, "\n"), "\n") {
		i := strings.Index(rest, line+"\n")
		if i < 0 {
			t.Fatalf("the edit rewrote the line %q:\n--- before\n%s--- after\n%s", line, before, after)
		}
		rest = rest[i+len(line)+1:]
	}
	// What is left is exactly the keys the command added.
	added := strings.TrimSuffix(strings.TrimPrefix(rest, "\n"), "\n")
	for _, want := range []string{"workspace: workspace-alpha", "workspace_pin:"} {
		if !strings.Contains(added, want) {
			t.Errorf("the edit added %q, which does not carry %q:\n%s", added, want, after)
		}
	}
	if strings.Count(after, "workspace:") != 1 {
		t.Errorf("the membership key was written more than once:\n%s", after)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to fugaro update:\n%s", out)
	}
}
