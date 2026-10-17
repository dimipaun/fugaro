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
