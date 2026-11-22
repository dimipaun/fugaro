// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package graph

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// A node's key and a scope argument, which are the two strings the console and
// the CLI both build and read: the key is the only identity a node has, and the
// scope is the only argument the renderer supplies (H1).

// ScopeKind is the part of a scope before its id: what a `--source` value
// selects. The set is closed, so a value that is not one of these is refused
// before anything is read.
type ScopeKind string

const (
	// ScopeAll is the union of every scope that exists — today the coordinator
	// alone, and the orchestrator scopes as they are built (D1356).
	ScopeAll ScopeKind = "all"
	// ScopeCoordinator is the coordinator's stored view: every repository, no
	// checkout needed.
	ScopeCoordinator ScopeKind = "coordinator"
	// ScopeOrchestrator is one orchestrator's node under `orchestrator/`.
	ScopeOrchestrator ScopeKind = "orchestrator"
	// ScopeLocal is one local orchestrator, read through its RTDB node and its
	// cache file.
	ScopeLocal ScopeKind = "local"
)

// Scope is a parsed `--source` value: a kind and, for the id-carrying kinds,
// the id it names.
type Scope struct {
	Kind ScopeKind
	ID   string
}

// String is the scope as it is written on the command line, which is what a
// refusal names and what a `SourceState` carries.
func (s Scope) String() string {
	if s.ID == "" {
		return string(s.Kind)
	}
	return string(s.Kind) + ":" + s.ID
}

// ParseScope reads a `--source` value. `all` and `coordinator` stand alone;
// `orchestrator:<id>` and `local:<id>` carry an orchestrator id — the project's
// name for the project's default orchestrator, or `<project>~<name>` for a
// named one (D3207, F40). The rule is C-ORCH's and not a second one here
// (ValidScopeID is ValidOrchestratorID): the console's own pattern and this one
// have to admit the same ids, and a scope may not name an orchestrator the rest
// of the tree refuses.
//
// It is deliberately narrow, so a value that reaches argv from the renderer
// never reaches a path or another flag: anything else — an empty value, an
// unknown kind, an id no orchestrator node may be keyed by (a leading hyphen,
// which would reach argv as a flag, a path separator, an uppercase letter,
// whitespace, a second `~`), or one longer than a project name and a name
// together — is refused here, before anything is read.
func ParseScope(s string) (Scope, error) {
	switch s {
	case string(ScopeAll):
		return Scope{Kind: ScopeAll}, nil
	case string(ScopeCoordinator):
		return Scope{Kind: ScopeCoordinator}, nil
	}
	kind, id, ok := strings.Cut(s, ":")
	if !ok {
		return Scope{}, unknownScope(s)
	}
	switch ScopeKind(kind) {
	case ScopeOrchestrator, ScopeLocal:
	default:
		return Scope{}, unknownScope(s)
	}
	if !orchestrator.ValidScopeID(id) {
		return Scope{}, fmt.Errorf("graph: source %q names no valid id: an id is a project name, or <project>%s<name>, each half 1 to %d characters of a-z, 0-9 and -, starting with a letter or a digit", safetext.Field(s), orchestrator.NameSeparator, orchestrator.MaxOrchestratorNameLength)
	}
	return Scope{Kind: ScopeKind(kind), ID: id}, nil
}

func unknownScope(s string) error {
	return fmt.Errorf("graph: unknown source %q: want all, coordinator, orchestrator:<id> or local:<id>", safetext.Field(s))
}

// NodeKey is a node's key: its source and its id, joined with a colon
// (`<source>:<id>`). It is the one place the join happens, so a reader that
// takes a key apart and a writer that builds one cannot drift.
//
// Keys are unique only within one source: nothing here dedupes across sources
// (D1356), so `coordinator:42` and `local:o1:42` are two nodes.
func NodeKey(source, id string) string { return source + ":" + id }
