package cli

// The workspace-projects-users addendum's documentation, pinned.
//
// docs/workspace-projects-users/ designs a workspace's IAM authority over its
// member projects: a folder the members sit in, or a workspace service account
// an owner granted, with the workspace's own roster as the list of who may do
// what in each. The commands are built under `fugaro workspace`; what this file
// pins is the text a person and their agent read before they run one, because
// each of the four facts below is one a reader acts on and each has a
// plausible-sounding wrong version:
//
//   - the opt-in link (D2819): a member joins the workspace's table with
//     `project add`, but the authority over it is the member owner's own act at
//     `project link` on the member's machine, and `fugaro update` links nobody.
//     The wrong version reads the add as the widening.
//   - the grant's lag (D2801): a grant lands in the member's own roster and the
//     binding it means appears at that member's own `fugaro update`. The wrong
//     version says the grant is a binding the moment it is written.
//   - the advisory mirror (D2813): the member list mirrored into a member is
//     rewritable by that member's own database admin, so the fence is drift
//     against the workspace's record and never the mirror. The wrong version
//     reads the mirror as a gate.
//   - the audit trail's limit (D2811): the entries are written with the
//     permission the roster write itself needs, so neither trail is
//     tamper-proof. The wrong version reads a create-only trail as evidence.
//
// TestDocsWorkspaceProjectsUsersSkillNamesTheFamily is the working skill's half
// of it, and its red step is the sentence on `fugaro workspace project link`:
// the skill named no workspace command at all, so an agent reading it could not
// tell that a member joins the workspace's table without the workspace gaining
// authority over it.
// TestDocsWorkspacesReadmeLinksTheAddendum is the other half: the workspaces
// design folder's index has to send a reader to the addendum's own folder, and
// its command list has to be the one the CLI answers to.
//
// The command names are resolved against the real tree here rather than only in
// the plugin's own lint, because that lint runs in another package: a rename
// that updated the skill and left this pin alone would otherwise pass.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	wu16Skill        = "plugin/skills/working/SKILL.md"
	wu16Workspaces   = "docs/workspaces/README.md"
	wu16AddendumRead = "docs/workspace-projects-users/README.md"
)

// wu16MemberFamilies are the three command groups the addendum's plan fixes
// (D2802), as each document spells them. A rename lands in all three places in
// the same sweep, or this file says which one was left behind.
var wu16MemberFamilies = []string{
	"fugaro workspace project add|ls|rm|status|reconcile|link|unlink|save",
	"fugaro workspace user add|rm|ls|grant|revoke",
	"fugaro workspace domains add|rm|ls",
}

// TestDocsWorkspaceProjectsUsersSkillNamesTheFamily: the working skill carries
// the family, the opt-in link, the grant's wait for the member's own update,
// the advisory mirror and the audit trail's limit. These are the sentences a
// user's agent repeats, so a skill that dropped one leaves the agent telling
// the user something the commands do not do.
func TestDocsWorkspaceProjectsUsersSkillNamesTheFamily(t *testing.T) {
	flat := flattenDoc(readDoc(t, wu16Skill))
	for _, want := range []string{
		// The commands, and the door that refuses them in a session.
		"`fugaro workspace init`",
		"`fugaro workspace project add|ls|rm|status|reconcile|link|unlink|save`",
		"`fugaro workspace user add|rm|ls|grant|revoke`",
		"`fugaro workspace domains add|rm|ls`",
		"refuses in a coding agent's session",
		// The opt-in link (D2819), which is this task's red step.
		"the member owner's own opt-in at `fugaro workspace project link`",
		"run on the member's own machine",
		"`fugaro update` links nobody",
		"keeps `fugaro user …` and everything else about it whole (D2819)",
		// The grant's update lag (D2801).
		"appears at that member's own `fugaro update`",
		"`fugaro workspace project status` reports as still pending (D2801)",
		// The advisory mirror (D2813) and the fence that replaces it.
		"is advisory, because that member's own database admin can rewrite it",
		"so the fence is drift, read against the workspace's own record (D2813)",
		// The audit trail's limit (D2811).
		"neither audit trail is tamper-proof",
		"written with the permission the roster write itself needs (D2811)",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s never says %q: the skill is what a user's agent repeats about a workspace, and a sentence this work made true that the skill does not carry is one the agent gets wrong out loud", wu16Skill, want)
		}
	}
}

// TestDocsWorkspaceProjectsUsersCommandsExist: every name the two documents
// spell is a command this build has. The plugin's own lint resolves the same
// names, but it runs in another package against the same tree, so this pin is
// the one that fails in the sweep that renames a command — it reads the
// documents, not the skill.
func TestDocsWorkspaceProjectsUsersCommandsExist(t *testing.T) {
	root := NewRootCmd()
	seen := map[string]bool{}
	for _, doc := range []string{wu16Skill, wu16Workspaces} {
		for _, family := range wu16MemberFamilies {
			if !strings.Contains(readDoc(t, doc), family) {
				t.Errorf("%s does not name the command family %q: the plan fixes these names (D2802) and a document that spells one differently from the CLI sends a reader to a command that does not exist", doc, family)
				continue
			}
			seen[family] = true
		}
	}
	for _, family := range wu16MemberFamilies {
		if !seen[family] {
			continue
		}
		for _, path := range wu16FamilyPaths(family) {
			words := strings.Fields(path)
			cmd, _, err := root.Find(words)
			if err != nil || cmd == nil || cmd.CommandPath() != "fugaro "+path {
				t.Errorf("`fugaro %s` is not a command of this build (%v): the documents name it and a reader who types it gets an error", path, err)
			}
		}
	}
}

// wu16FamilyPaths expands one spelled family ("fugaro workspace project
// add|ls|rm") into the command paths it stands for: every word before the one
// carrying the pipes is the group, and each pipe-separated verb hangs off it.
func wu16FamilyPaths(family string) []string {
	words := strings.Fields(family)
	if len(words) < 3 || words[0] != "fugaro" {
		return nil
	}
	group, verbs := words[1:len(words)-1], words[len(words)-1]
	if !strings.Contains(verbs, "|") {
		return nil
	}
	var out []string
	for _, verb := range strings.Split(verbs, "|") {
		out = append(out, strings.Join(append(append([]string{}, group...), verb), " "))
	}
	return out
}

// TestDocsWorkspacesReadmeLinksTheAddendum: the workspaces design folder says
// in its own words that the addendum supersedes three of its §9 rules, and a
// reader who wants the commands, the plan and the contracts has to be able to
// reach the addendum's folder from there. The link is resolved rather than
// mentioned, because a link one directory short is mentioned and dead at the
// same time.
func TestDocsWorkspacesReadmeLinksTheAddendum(t *testing.T) {
	const link = "(../workspace-projects-users/README.md)"
	readme := readDoc(t, wu16Workspaces)
	if !strings.Contains(readme, link) {
		t.Fatalf("%s does not link %s: the folder that supersedes three of the design's rules is the folder a reader has to be able to reach from it", wu16Workspaces, link)
	}
	if _, err := os.Stat(filepath.Join("../../", wu16AddendumRead)); err != nil {
		t.Errorf("%s links %s, which is not in the tree: %v", wu16Workspaces, wu16AddendumRead, err)
	}
	if flat := flattenDoc(readme); !strings.Contains(flat, "the member owner's own opt-in at `fugaro workspace project link` (D2819)") {
		t.Errorf("%s does not carry the opt-in link (D2819): the workspaces design's own §9 says a workspace never widens a member, and the one sentence that says how that is undone has to be where the supersession is stated", wu16Workspaces)
	}
}
