// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"strings"
	"testing"
)

// TestKeyIsSourceQualified: a node's key names its source and its id, so the
// same id under two sources is two keys and no reader can take one for the
// other (D1356). Keys are unique within one source and nothing dedupes across
// sources.
//
// Hand mutation this fails on: NodeKey returning the id alone, which makes a
// coordinator pull request and an orchestrator task with the same id one node.
func TestKeyIsSourceQualified(t *testing.T) {
	if got := NodeKey("coordinator", "42"); got != "coordinator:42" {
		t.Errorf("NodeKey = %q, want %q", got, "coordinator:42")
	}
	if a, b := NodeKey("coordinator", "42"), NodeKey("local:o1", "42"); a == b {
		t.Errorf("two sources share one key: %q", a)
	}
	if a, b := NodeKey("a", "b"), NodeKey("b", "a"); a == b {
		t.Errorf("NodeKey ignores where the source ends and the id begins: %q", a)
	}
}

// TestScopePatternRefusesEverythingElse: `--source` is a renderer-supplied
// string that reaches argv, so ParseScope accepts only the closed set of H1 —
// `all`, `coordinator`, `orchestrator:<id>`, `local:<id>`, with an id of one
// lower-case letter or digit and up to 39 more of those or a hyphen — and
// refuses everything else before anything is read.
//
// Hand mutation this fails on: loosening the id pattern to `.+` (or dropping
// its anchors), which lets a leading `-`, a path separator, an uppercase
// letter or a 41-character id through.
func TestScopePatternRefusesEverythingElse(t *testing.T) {
	accept := []struct {
		in   string
		kind ScopeKind
		id   string
	}{
		{"all", ScopeAll, ""},
		{"coordinator", ScopeCoordinator, ""},
		{"orchestrator:o1", ScopeOrchestrator, "o1"},
		{"local:abc-123", ScopeLocal, "abc-123"},
		{"local:0", ScopeLocal, "0"},
		{"orchestrator:" + strings.Repeat("a", 40), ScopeOrchestrator, strings.Repeat("a", 40)},
	}
	for _, tc := range accept {
		got, err := ParseScope(tc.in)
		if err != nil {
			t.Errorf("ParseScope(%q) refused a valid scope: %v", tc.in, err)
			continue
		}
		if got.Kind != tc.kind || got.ID != tc.id {
			t.Errorf("ParseScope(%q) = %+v, want kind %q id %q", tc.in, got, tc.kind, tc.id)
		}
		if s := got.String(); s != tc.in {
			t.Errorf("ParseScope(%q).String() = %q, want the value back", tc.in, s)
		}
	}

	refuse := []string{
		"",
		"all:",  // a scope that carries no id
		"all:1", //
		"all/x", //
		"coordinator:x",
		"ALL",
		"All",
		"Orchestrator:o1",
		"Local:o1",
		"-all", // a leading hyphen would reach argv as a flag
		"-orchestrator:o1",
		"orchestrator:-o1",
		"orchestrator:o1/x", // path separators
		"orchestrator:o1\\x",
		"orchestrator:a.b",
		"orchestrator:a@b",
		"orchestrator:",
		"local:",
		"orchestrator:UPPER",
		"orchestrator:" + strings.Repeat("a", 41),
		"orchestrator:o1 o2",
		" local:o1",
		"local:o1 ",
		"orchestrator:o1\n",
		"orchestrator:o1:extra",
		"bogus",
		"bogus:x",
	}
	for _, in := range refuse {
		if got, err := ParseScope(in); err == nil {
			t.Errorf("ParseScope(%q) = %+v, want a refusal", in, got)
		}
	}
}
