package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// WS19's own suite: the `all_members` key of a shared entry (D2977, R7). The
// key is the workspace owner's declaration that one of the workspace's
// credentials is offered to EVERY member of the workspace — the ones that join
// later included — which is the widest an entry can be and the reason it is
// fenced by something else: the workspace's own ceiling. What the service does
// with the key is credsvc's own suite (bundle_workspace_test.go); what this file
// holds is the two sides a person drives, the command that writes it and the
// writer that publishes it as version 2 of the node, and the refusal that stands
// where the cap is missing.

// wsAllMembersShared is the owner's shared list with one entry offered to every
// member and one offered to a member it names: the two declarations the node's
// own reader holds apart (`all_members` and `projects`, contract C3's rule 5),
// written in the file the owner's commands write.
const wsAllMembersShared = `workspace_shared:
  claude-oauth-token:
    kind: model
    all_members: true
  claude-oauth-token-team:
    kind: model
    projects:
      - member-b
`

// TestAllMembersNeedsACap is the task's acceptance on the command side: the key
// is refused while the workspace states no cap, with the fix printed, and it is
// written once there is one.
//
// An entry offered to every member is bounded by the workspace's ceiling alone
// (D2977): it reaches members that join after it is written, so nothing else
// bounds what they spend through it. The refusal names the cap's own command,
// and it writes nothing — the file is the same before and after — because an
// entry half-written would be one the next update publishes with no fence behind
// it.
func TestAllMembersNeedsACap(t *testing.T) {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceOwnerTable)
	before := readFileString(t, owner)

	share := []string{"workspace", "share", "anthropic-api-key", "--workspace", "workspace-alpha", "--all-members"}
	out, _, err := execute(t, share...)
	if err == nil {
		t.Fatalf("--all-members was accepted by a workspace that states no cap:\n%s", out)
	}
	for _, want := range []string{"states no cap", "--all-members", "every member", "fugaro workspace cap set --workspace workspace-alpha --monthly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so the owner is not told the fix:\n%v", want, err)
		}
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused --all-members wrote the file:\n%s", after)
	}

	// The two declarations are exclusive, and the pair is refused before
	// anything is read: an entry that carried both would be one the workspace's
	// own update refuses to publish (contract C3's rule 5), and the person who
	// typed it is the side that can say which one they meant.
	out, _, err = execute(t, "workspace", "share", "anthropic-api-key", "--workspace", "workspace-alpha",
		"--all-members", "--projects", "aurora")
	if err == nil {
		t.Fatalf("--all-members beside --projects was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--all-members and --projects are exclusive") {
		t.Errorf("the pair is refused for another reason than its exclusivity: %v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refused pair wrote the file:\n%s", after)
	}

	// The ceiling stated, the key is written: the entry carries `all_members:
	// true` and names no project, which is the one-of-two declaration the node's
	// own reader holds apart.
	workspaceAppend(t, owner, "workspace_cap:\n  monthly: 50\n")
	out, _, chainErr := execute(t, share...)
	assertNoPrompt(t, out)
	workspaceAssertSavedNotLive(t, "workspace share", out, chainErr)
	after := readFileString(t, owner)
	if !strings.Contains(after, "all_members: true") {
		t.Errorf("the accepted share did not write the all_members key:\n%s", after)
	}
	lc, err := localcfg.Parse([]byte(after))
	if err != nil {
		t.Fatalf("%v\n%s", err, after)
	}
	entry, ok := workspaceShared(lc)["anthropic-api-key"]
	if !ok {
		t.Fatalf("the accepted share wrote no entry:\n%s", after)
	}
	if !entry.AllMembers || len(entry.Projects) != 0 {
		t.Errorf("the entry is %+v, want all_members with no projects: the two are exclusive (contract C3's rule 5)", entry)
	}
	if !strings.Contains(out, "offer anthropic-api-key (model) in workspace workspace-alpha to every member of the workspace, the ones that join later included") {
		t.Errorf("the sentence does not say the entry is offered to every member:\n%s", out)
	}
}

// TestAllMembersPublishesVersionTwo is the task's other acceptance half: the
// writer publishes the key, as version 2 of the node, for the service that reads
// it — and the entry it writes names every member through the key rather than
// through projects, so a member that joins later is offered it with no rewrite
// of the entry.
//
// The document is the whole declaration, so the test reads the published node
// back with the package's own decoder and beside it the raw JSON the service
// reads: version 2, `all_members: true` on the one entry, and no `projects` key
// inside it — the shape rule 5 of the contract refuses when both are written.
func TestAllMembersPublishesVersionTwo(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap+wsAllMembersShared)
	r := newWSUpdateRig(t, h)
	fakeTerminal(t)
	oc, out, err := r.apply(t, false)
	if err != nil {
		t.Fatalf("the owner's run published nothing: %v\n%s", err, out)
	}
	if !oc.Changed {
		t.Errorf("the first publish reports no change: %s", oc.Detail)
	}
	p := h.project(t, "workspace-alpha")
	node := wsCredsNode(t, p.db, "workspace-alpha")
	if node.Version != projectpolicy.WorkspaceCredentialsVersion {
		t.Errorf("the published node is version %d, want %d: `all_members` is version 2's key", node.Version, projectpolicy.WorkspaceCredentialsVersion)
	}
	var every, named *projectpolicy.WorkspaceShared
	for i := range node.Shared {
		switch node.Shared[i].Name {
		case "claude-oauth-token":
			every = &node.Shared[i]
		case "claude-oauth-token-team":
			named = &node.Shared[i]
		}
	}
	if every == nil || named == nil {
		t.Fatalf("the published pool is %+v, want both the entry offered to every member and the one offered to a member it names", node.Shared)
	}
	if !every.AllMembers || len(every.Projects) != 0 {
		t.Errorf("the entry offered to every member is %+v, want all_members with no projects", *every)
	}
	if named.AllMembers || !slices.Equal(named.Projects, []string{"member-b"}) {
		t.Errorf("the entry offered to a named member is %+v, want projects and no all_members", *named)
	}

	// The raw document, which is what the service reads: the key is written
	// `true` on the one entry and absent from the other, and the entry that
	// carries it carries no projects beside it.
	raw, err := json.Marshal(p.db.Value(projectpolicy.PathWorkspaceCredentials("workspace-alpha")))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int              `json:"version"`
		Shared  []map[string]any `json:"shared"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 2 {
		t.Errorf("the published document says version %d, want 2", doc.Version)
	}
	byName := map[string]map[string]any{}
	for _, s := range doc.Shared {
		name, _ := s["name"].(string)
		byName[name] = s
	}
	if got := byName["claude-oauth-token"]["all_members"]; got != true {
		t.Errorf("the published entry carries all_members %v, want true:\n%s", got, raw)
	}
	if _, ok := byName["claude-oauth-token"]["projects"]; ok {
		t.Errorf("the published entry carries projects beside all_members, which the reader refuses:\n%s", raw)
	}
	if _, ok := byName["claude-oauth-token-team"]["all_members"]; ok {
		t.Errorf("the entry offered to a named member carries an all_members key:\n%s", raw)
	}
}

// TestAllMembersFenceAsksWhenOnlyTheCapMoved is D2977's own case, and the one
// the credentials node cannot be asked about: the node carries no cap, so an
// owner who takes `workspace_cap` out of their own file while an `all_members`
// entry stands leaves the published node saying exactly what the installation
// declares. A run that only asked the fence where the node changes would read
// "nothing to publish" and leave a pool offered to every member of the
// workspace — the ones that join later included — with the ceiling that is the
// only bound on it gone.
func TestAllMembersFenceAsksWhenOnlyTheCapMoved(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable+wsOwnerCap+wsAllMembersShared)
	fakeTerminal(t)
	first := newWSUpdateRig(t, h)
	if _, out, err := first.apply(t, false); err != nil {
		t.Fatalf("the first run published nothing: %v\n%s", err, out)
	}
	p := h.project(t, "workspace-alpha")
	if len(wsCredsNode(t, p.db, "workspace-alpha").Shared) == 0 {
		t.Fatal("the first run published no pool for the second run to agree with")
	}
	// The owner takes the ceiling out of the file and leaves the entry: node and
	// declaration still agree, which is the state this refusal is about.
	text := readFileString(t, p.path)
	stripped := strings.Replace(text, wsOwnerCap, "", 1)
	if stripped == text {
		t.Fatalf("the cap the fixture states is not in the config the rig wrote:\n%s", text)
	}
	if err := os.WriteFile(p.path, []byte(stripped), 0o600); err != nil {
		t.Fatal(err)
	}
	second := newWSUpdateRig(t, h)
	_, out, err := second.apply(t, false)
	if err == nil {
		t.Fatalf("a run published a workspace whose all_members entry stands and whose cap is gone:\n%s", out)
	}
	for _, want := range []string{"states no cap", "claude-oauth-token", "every member", "fugaro workspace cap set --workspace workspace-alpha --monthly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if v := p.db.Value(projectpolicy.PathWorkspaceCredentials("workspace-alpha")); v == nil {
		t.Error("the refusal took the published node with it: a refusal writes nothing and leaves what is there")
	}
}

// TestAllMembersRefusesANodeOfTwoDeclarations is the writer's own side of the
// exclusivity: a local list that carried both (it cannot, the loader refuses it)
// would still not publish a node with both, because the node's reader refuses
// the pair rather than picking one — a reader that picked would be deciding who
// may be served a credential, which is the workspace owner's decision.
func TestAllMembersRefusesANodeOfTwoDeclarations(t *testing.T) {
	rec := &workspace.Record{Name: "workspace-alpha", Members: []workspace.Member{{Project: "member-b"}}}
	_, err := workspaceSharedEntry(rec, "claude-oauth-token", localcfg.WorkspaceShared{
		Kind:       localcfg.WorkspaceSharedModel,
		AllMembers: true,
		Projects:   []string{"member-b"},
	})
	if err == nil {
		t.Fatal("an entry carrying both all_members and projects was published: the node's own writer validates against contract C3's rule 5")
	}
	if !strings.Contains(err.Error(), "all_members") || !strings.Contains(err.Error(), "projects") {
		t.Errorf("the refusal does not name the two keys: %v", err)
	}

	// And the one-declaration entry is published: all_members and no projects,
	// which is the shape the service offers to every member of the node.
	entry, err := workspaceSharedEntry(rec, "claude-oauth-token", localcfg.WorkspaceShared{
		Kind:       localcfg.WorkspaceSharedModel,
		AllMembers: true,
	})
	if err != nil {
		t.Fatalf("workspaceSharedEntry: %v", err)
	}
	if !entry.AllMembers || len(entry.Projects) != 0 {
		t.Errorf("the entry is %+v, want all_members with no projects", entry)
	}
}
