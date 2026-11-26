package cli

import (
	"strings"
	"testing"
)

// TestWorkspaceCredentialsDocsAreWritten pins WC13's three deliverables: the
// release the personal half ships in says, in one place, that `fugaro update`
// is the whole upgrade and that turning the half on is configuration; the
// workspace folder a reader of the shared half already knows points at this
// half, at the two routes its service now admits a person on; and the working
// skill names the flag and the command a person needs.
//
// The release file is named by its version on purpose. It is written in the
// pull request before the release is cut (docs/releases/README.md), so the
// version is known here, and a file renamed to another one is a release nobody
// wrote these notes for: the test names the file it wants rather than scanning
// the directory for something that reads like them.
func TestWorkspaceCredentialsDocsAreWritten(t *testing.T) {
	const release = "docs/releases/v0.30.0.md"
	note := flattenDoc(readDoc(t, release))
	for _, want := range []string{
		// §9: what an operator does, and the fact that it is one command.
		"### For operators",
		"`fugaro update` is the whole upgrade",
		// The three configuration steps, in the order they must be run: the
		// workspace owner's roster, the member's switch, the person's token.
		"fugaro settings set personal_workspace_tokens true",
		"fugaro secrets user set claude-oauth-token --project",
		// WC0's numbered risks, carried into the notes as the README requires.
		"### Known limits",
		"reads every person's token",
		"up to 24 hours",
		"removed from the roster",
		"shared entries",
		// The forward citation contract-person.md owes: the switch's key makes
		// an older CLI in that project refuse to launch.
		"an older CLI in that project refuse to launch",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("%s does not say %q: the release that ships the personal half carries its operator steps and its known limits (docs/releases/README.md)", release, want)
		}
	}

	// The workspace folder is where a reader of the shared half is; WC13 owes it
	// the sentence that the service admits roster people too, and the pointer to
	// the folder that holds this half's design, threat model and plan.
	ws := flattenDoc(readDoc(t, "docs/workspaces/README.md"))
	for _, want := range []string{
		"admits the roster's people too, on two routes of its own",
		"/v1/slip",
		"/v1/slip/claim",
		"[../workspace-credentials/](../workspace-credentials/)",
	} {
		if !strings.Contains(ws, want) {
			t.Errorf("docs/workspaces/README.md does not say %q: a reader of the shared half is sent to the personal half from there", want)
		}
	}

	// The skill a launcher's agent reads: the flag that skips the half for one
	// run is in the skill's own page (and the sentence carrying it is inside the
	// Workspaces section a documents test pins, so it is where an agent reading
	// about a workspace meets it), and the command a person stores a token with
	// — naming the workspace's own project, the mistake D3148 exists to prevent
	// — is in the launch page the skill sends a launcher to. The skill is the
	// two files together, and the page's own byte budget (plugin/skills_lint_test.go)
	// is why the detail sits in the reference page rather than in SKILL.md.
	skill := flattenDoc(readDoc(t, "plugin/skills/working/SKILL.md"))
	for _, want := range []string{
		"`--no-personal-token`",
		"roster person's own token at the workspace",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("plugin/skills/working/SKILL.md does not name %q: a launcher's agent learns the flag there (WC13, D3142)", want)
		}
	}
	launch := flattenDoc(readDoc(t, "plugin/skills/working/reference/launch.md"))
	for _, want := range []string{
		"fugaro secrets user set claude-oauth-token --project",
		"`--no-personal-token` asks for none",
		"D3148",
	} {
		if !strings.Contains(launch, want) {
			t.Errorf("plugin/skills/working/reference/launch.md does not say %q: the storage command and the flag's meaning belong to the launch, which the skill sends a launcher to (WC13, D3148)", want)
		}
	}
}
