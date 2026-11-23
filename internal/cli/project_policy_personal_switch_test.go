package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestTheSwitchIsPublishedByUpdate: the member's switch for the personal half
// of a workspace's credentials (WC6 of docs/workspace-credentials/, R9, D2972)
// lives in the machine's own config file — that is what `fugaro settings set`
// writes, the shape the triage switch has — and the workspace's service, the
// only enforcer, reads the member's PUBLISHED policy and never anything on the
// member's disk. So the one command that may be a second step (the HARD RULE:
// `fugaro update` is the whole upgrade) is where the node learns it, and this
// test pins the two directions of that road: a config that states the switch
// carries it to the node's top level, and a config that is silent keeps
// whatever answer the node already holds rather than clearing it.
//
// It is the local file the test states the switch in, through the same fixture
// the sibling switches' tests use: the file FUGARO_CONFIG names is the one
// `settings set` would have written.
func TestTheSwitchIsPublishedByUpdate(t *testing.T) {
	f := newProjectFixture(t, "personal_workspace_tokens: true\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if p.PersonalWorkspaceTokens == nil || !*p.PersonalWorkspaceTokens {
		t.Fatalf("PersonalWorkspaceTokens = %v, want the written true the config states", p.PersonalWorkspaceTokens)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); !strings.Contains(raw, `"personal_workspace_tokens":true`) {
		t.Errorf("the node does not carry the switch:\n%s", raw)
	}
	// One top-level key of the node: a member of a block would be a key this
	// release's readers do not look for, and the service reads the node's own
	// top level (creds.PersonSwitchKey's doc).
	var node map[string]any
	if err := json.Unmarshal([]byte(nodeJSON(t, f, projectpolicy.PathShared())), &node); err != nil {
		t.Fatal(err)
	}
	if v, ok := node["personal_workspace_tokens"].(bool); !ok || !v {
		t.Errorf("the node's top level carries personal_workspace_tokens = %v, want true", node["personal_workspace_tokens"])
	}

	// The other direction: a written false is a statement too, and the publish
	// carries it as it stands — absent reads as off (R9), but a member's admin
	// who writes the false has written the answer the node must hold.
	rewriteProjectConfig(t, f, "personal_workspace_tokens: false\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.PersonalWorkspaceTokens == nil || *p.PersonalWorkspaceTokens {
		t.Errorf("PersonalWorkspaceTokens = %v, want the written false the config states", p.PersonalWorkspaceTokens)
	}

	// Silence keeps the node's own answer: a config that states nothing is not
	// the owner's edit, and a publish must not be the write that turns a
	// member's switch off behind their back (the triage switch's keep rule).
	rewriteProjectConfig(t, f, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.PersonalWorkspaceTokens == nil || *p.PersonalWorkspaceTokens {
		t.Errorf("a silent config published PersonalWorkspaceTokens = %v, want the node's own written false kept", p.PersonalWorkspaceTokens)
	}

	// And a project that never stated the switch and whose node never carried
	// it publishes no member at all: the key is the node's to hold, and a
	// publish is not the place a default is written.
	g := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if raw := nodeJSON(t, g, projectpolicy.PathShared()); strings.Contains(raw, "personal_workspace_tokens") {
		t.Errorf("a project that states no answer published one:\n%s", raw)
	}
	if p := publishedPolicy(t, g); p.PersonalWorkspaceTokens != nil {
		t.Errorf("PersonalWorkspaceTokens = %v, want nil: absent is off, and nothing has to say so", p.PersonalWorkspaceTokens)
	}
}

// TestTheFirstPublishKnowsTheSwitchsName: the first publish of a project that
// has no node yet is reached by the key's presence in the config rather than by
// its value (the triage switch's rule), and the check that says why is what a
// member meets when their switch would otherwise have been left in a file no
// service reads.
func TestTheFirstPublishKnowsTheSwitchsName(t *testing.T) {
	f := newProjectFixture(t, "personal_workspace_tokens: true\n")
	h := newMigrationRig(t, f, &initOptions{})
	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Detail, "personal-workspace-tokens switch") {
		t.Errorf("the check does not name the switch the config states: %q", st.Detail)
	}
	if strings.Contains(st.Detail, "the triage switch") || strings.Contains(st.Detail, "review-against-task switch") {
		t.Errorf("the check names a switch this config does not state: %q", st.Detail)
	}
}
