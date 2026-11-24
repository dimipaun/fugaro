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

// TestParseScopeNamedOrchestrator: a named orchestrator's id is a scope's id
// too (D3207, F40). `orchestrator:<project>~<name>` and
// `local:<project>~<name>` are read under C-ORCH's one rule — ValidScopeID,
// the same rule the orchestrator tree validates its own ids with — so
// `fugaro graph --source orchestrator:<id>` reads the named orchestrator, and
// the value the console sends back is one the node it names is keyed by.
//
// Hand mutation this fails on: keeping the old `scopeIDPattern`, which refuses
// `~` and so leaves every named orchestrator unreachable from the command line.
func TestParseScopeNamedOrchestrator(t *testing.T) {
	accept := []struct {
		in   string
		kind ScopeKind
		id   string
	}{
		{"orchestrator:fugaro~night", ScopeOrchestrator, "fugaro~night"},
		{"local:fugaro~night", ScopeLocal, "fugaro~night"},
		// The default orchestrator is still the project's own name.
		{"orchestrator:fugaro", ScopeOrchestrator, "fugaro"},
		// The longest id the rule admits: a 40-character project name, the
		// separator, and a 40-character name (MaxOrchestratorIDLength).
		{"orchestrator:" + strings.Repeat("a", 40) + "~" + strings.Repeat("b", 40),
			ScopeOrchestrator, strings.Repeat("a", 40) + "~" + strings.Repeat("b", 40)},
		// A name may end in a hyphen where a project's name may not (D3207's
		// asymmetry).
		{"orchestrator:fugaro~night-", ScopeOrchestrator, "fugaro~night-"},
	}
	for _, tc := range accept {
		got, err := ParseScope(tc.in)
		if err != nil {
			t.Errorf("ParseScope(%q) refused a valid named scope: %v", tc.in, err)
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
		"orchestrator:~night",         // no project
		"orchestrator:fugaro~",        // no name
		"orchestrator:fugaro~night~1", // one separator, and the id carries it
		"orchestrator:fugaro~Night",
		"orchestrator:Fugaro~night",
		"orchestrator:fugaro~ni_ght",
		"orchestrator:fugaro~night/x",
		"orchestrator:fugaro ~night",
		"orchestrator:fugaro~-night",
		// A project's name ends with a letter or a digit, where the id this
		// scope used to carry alone admitted a trailing hyphen.
		"orchestrator:fugaro-",
		"orchestrator:fugaro-~night",
		"orchestrator:-fugaro~night",
		"local:fugaro~night~1",
		// One past the id's cap: a name one character too long, and a project
		// name one too long.
		"orchestrator:" + strings.Repeat("a", 40) + "~" + strings.Repeat("b", 41),
		"orchestrator:" + strings.Repeat("a", 41) + "~night",
	}
	for _, in := range refuse {
		if got, err := ParseScope(in); err == nil {
			t.Errorf("ParseScope(%q) = %+v, want a refusal", in, got)
		}
	}
}

// TestScopePatternRefusesEverythingElse: `--source` is a renderer-supplied
// string that reaches argv, so ParseScope accepts only the closed set of H1 —
// `all`, `coordinator`, `orchestrator:<id>`, `local:<id>`, with an id one
// orchestrator node may be keyed by (C-ORCH's rule, F40) — and refuses
// everything else before anything is read.
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
