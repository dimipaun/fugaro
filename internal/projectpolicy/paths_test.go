package projectpolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPaths(t *testing.T) {
	if got, want := PathShared(), "config/policy/shared"; got != want {
		t.Errorf("PathShared() = %q, want %q", got, want)
	}
	if got, want := PathAdmin(), "config/admin"; got != want {
		t.Errorf("PathAdmin() = %q, want %q", got, want)
	}
	if got, want := PathRepo("acme-app-0123456789abcdef"), "config/policy/repos/acme-app-0123456789abcdef"; got != want {
		t.Errorf("PathRepo() = %q, want %q", got, want)
	}
	if got, want := PathVoiceShared(), "config/policy/voice/shared"; got != want {
		t.Errorf("PathVoiceShared() = %q, want %q", got, want)
	}
	if got, want := PathVoiceRepo("acme-app-0123456789abcdef"), "config/policy/voice/repos/acme-app-0123456789abcdef"; got != want {
		t.Errorf("PathVoiceRepo() = %q, want %q", got, want)
	}
	if got, want := PathVoiceRepos(), "config/policy/voice/repos"; got != want {
		t.Errorf("PathVoiceRepos() = %q, want %q", got, want)
	}
	// The voice nodes sit under the policy's own node, which is
	// internal/budget/rules' PathVoice — the path the template spells — and
	// never beside it: a subtree that drifted out of config/policy would be a
	// grant the check that reads the policy tree does not see.
	for _, p := range []string{PathVoiceShared(), PathVoiceRepo("x"), PathVoiceRepos()} {
		if p != "config/policy/voice" && !strings.HasPrefix(p, "config/policy/voice/") {
			t.Errorf("%s is not under config/policy/voice", p)
		}
	}
}

// TestVoicePathsAreReadableByARunTokenAndNobodyElse: the grants the voice
// subtree carries are PathShared's and PathRepo's, copied field for field
// (docs/mobile/design/voice-evaluation-switch.md §4.3), so the two properties
// the app's whole design rests on are read off the template here rather than
// restated: a run token reads them, and a signed-in user reads nothing under
// them — the phone holds no read of config/policy/**, which is why the
// switch's project-policy state is the hop's answer and not a read.
func TestVoicePathsAreReadableByARunTokenAndNobodyElse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "budget", "rules", "rules.json.tmpl"))
	if err != nil {
		t.Fatalf("the rules template: %v", err)
	}
	// The template's actions all sit inside JSON string literals, so the file is
	// JSON as it stands and needs no rendering to have its tree read.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the rules template is not JSON: %v", err)
	}
	node, _ := doc["rules"].(map[string]any)
	if node == nil {
		t.Fatal("the template has no rules object")
	}
	// The parent the three functions name carries no grant: a read on it would
	// cascade to every project's node.
	for _, seg := range strings.Split("config/policy/voice", "/") {
		child, ok := node[seg].(map[string]any)
		if !ok {
			t.Fatalf("the template's rules object has no %s node", seg)
		}
		node = child
	}
	if len(node) != 2 {
		t.Errorf("the template's config/policy/voice carries %d keys, want the two children and no grant of its own: %v", len(node), node)
	}
	shared, _ := node["shared"].(map[string]any)
	repos, _ := node["repos"].(map[string]any)
	if shared == nil || repos == nil {
		t.Fatalf("the template's config/policy/voice has no shared/repos children: %v", node)
	}
	if got := shared[".read"]; got != "TOKEN" {
		t.Errorf("config/policy/voice/shared .read = %v, want TOKEN (PathShared's own grant)", got)
	}
	if got := shared[".write"]; got != false {
		t.Errorf("config/policy/voice/shared .write = %v, want false: only an operator's IAM credential writes it", got)
	}
	repo, _ := repos["$slug"].(map[string]any)
	if repo == nil {
		t.Fatalf("the template's config/policy/voice/repos has no $slug child: %v", repos)
	}
	if got := repo[".read"]; got != "TOKEN && auth.token.fs == $slug" {
		t.Errorf("config/policy/voice/repos/$slug .read = %v, want TOKEN && auth.token.fs == $slug (PathRepo's own grant)", got)
	}
	if got := repo[".write"]; got != false {
		t.Errorf("config/policy/voice/repos/$slug .write = %v, want false", got)
	}
	for _, n := range []map[string]any{shared, repo} {
		if r, _ := n[".read"].(string); strings.Contains(r, "mu") || strings.Contains(r, "mc") || strings.Contains(r, "pu") {
			t.Errorf("a voice node's .read = %q names a user, consumer or pairing claim: no device, no phone and no pairing session reads the policy tree", r)
		}
	}
}

// The escaping itself (that a slug is keyed exactly as budget.Key keys it) is
// asserted in internal/budget/rules, against budget.Key, so this package does
// not import internal/budget to check its own copy of it.

// TestRulesMarkerPathIsUnderConfigLimits (the plan's Task 1.1 test list):
// PathRulesMarker must name a node a run token can already read under the rules
// template that *predates* phase 1 — that is what the marker is for. It records
// which generation of the security rules is deployed (RulesVersion), the runner
// reads it to decide what it may assume, and the whole safe-ordering argument of
// Tasks 1.2/1.3 rests on the read being available before any new rule is: the
// marker is under config/limits, whose TOKEN grant the old template already
// carries. Move it out of config/limits and the argument breaks, silently,
// which is why this test says so.
//
// It reads the template's own tree: internal/budget/rules holds these paths as
// JSON literals and this package holds the marker's in Go, so only a test that
// reads one spelling and computes the other can see the two drift — the same
// reason the policy nodes are compared against the template from the other side,
// in that package's TestTemplatePolicyPathsAgreeWithProjectpolicy.
func TestRulesMarkerPathIsUnderConfigLimits(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "budget", "rules", "rules.json.tmpl"))
	if err != nil {
		t.Fatalf("the rules template: %v", err)
	}
	// The template's actions all sit inside JSON string literals, so the file is
	// JSON as it stands and needs no rendering to have its tree read.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the rules template is not JSON: %v", err)
	}
	node, _ := doc["rules"].(map[string]any)
	if node == nil {
		t.Fatal("the template has no rules object")
	}

	marker := PathRulesMarker()
	i := strings.LastIndex(marker, "/")
	if i <= 0 || i == len(marker)-1 {
		t.Fatalf("PathRulesMarker() = %q, which has no parent path", marker)
	}
	parent := marker[:i]
	if parent != "config/limits" {
		t.Errorf("PathRulesMarker() = %q: its parent is %q, want config/limits — the marker is readable by a run token only because that node carries the template's TOKEN grant (Task 1.2 step 4)", marker, parent)
	}

	// The template's tree, segment by segment: the parent node must carry the
	// grant a run token satisfies.
	for _, seg := range strings.Split(parent, "/") {
		child, ok := node[seg].(map[string]any)
		if !ok {
			t.Fatalf("the template's rules object has no %s node, which %s lives under", parent, marker)
		}
		node = child
	}
	if got := node[".read"]; got != "TOKEN" {
		t.Errorf("the template's %s node has .read = %v, want \"TOKEN\": a run token reads %s there under the rules that predate phase 1", parent, got, marker)
	}
}

func TestPathsPanicOnAnEmptySlug(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("PathRepo(\"\") must panic: an empty segment names the parent node")
		}
	}()
	PathRepo("")
}
