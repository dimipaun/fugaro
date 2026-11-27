package config

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// The retired keys (R3 of docs/credentials-belong-to-models/reconciliation.md):
// the keys a repository's file and a project layer may still write, which the
// first release of that plan reads and no longer honours. Each still decodes
// and still validates (D3063) — nothing here refuses a document, and this file
// changes no reader — so what the release owes is that the key is *named*
// wherever it is written: `fugaro update` offers its removal as a diff,
// `fugaro validate` and `fugaro doctor` print the sentence beside it, and a
// person is told what stopped working before the release that rejects the key
// (CM29) does.
//
// ScopeRow.Retired says, key by key, where the key is retired and
// ScopeRow.RetiredWhy is the sentence a person reads; RetiredKeys is that
// table read out, so there is one list rather than two, and RetiredKeyNotes is
// the one function that answers, for a document, every retired key it still
// writes.

// RetiredKey is one retired key where it is retired: the path a person reads
// and the sentence they read beside it.
type RetiredKey struct {
	// Key is the fugaro.yaml path, as config.Scopes spells it.
	Key string
	// In is the document the key is retired in: InRepo for a repository's own
	// fugaro.yaml, InProject for a project layer's defaults:.
	In Scope
	// Why is the sentence a person reads.
	Why string
}

// RetiredKeys is every retired key, one entry per document it is retired in, in
// the order of Scopes.
var RetiredKeys = retiredKeys()

func retiredKeys() []RetiredKey {
	var out []RetiredKey
	for _, r := range Scopes {
		for _, doc := range []Scope{InRepo, InProject} {
			if r.Retired&doc != 0 {
				out = append(out, RetiredKey{Key: r.Key, In: doc, Why: r.RetiredWhy})
			}
		}
	}
	return out
}

// RetiredKeyNote is a retired key one document still writes: the path as that
// document writes it and the sentence a person reads.
type RetiredKeyNote struct {
	// Path is the key's path as the document writes it: a fugaro.yaml key, or
	// a project layer's own ("defaults.agent.auth").
	Path string
	// Why is the sentence a person reads.
	Why string
}

// RetiredKeyNotes is every retired key data still writes, in path order (a
// stable order for a diff and for a message), for a repository's own
// fugaro.yaml (doc = InRepo) or a project layer (doc = InProject, whose every
// key of fugaro.yaml's is written under defaults:).
//
// It decides nothing: a document this cannot read as YAML answers no notes, and
// the keys it does name still decode and still validate (D3063), so a caller
// prints a note and never a refusal. What a caller does with the answer is its
// own — `fugaro update` offers each key's removal as a diff, `validate` and
// `doctor` print the sentence.
func RetiredKeyNotes(data []byte, doc Scope) []RetiredKeyNote {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil
	}
	tree, _ := docTree(&node)
	// Only defaults: holds fugaro.yaml keys in a project layer; profiles: and
	// the layer's other top-level keys are the layer's own.
	prefix := ""
	if doc == InProject {
		prefix = "defaults."
	}
	var notes []RetiredKeyNote
	keyPaths(tree, "", func(path string) {
		if !strings.HasPrefix(path, prefix) {
			return
		}
		row, ok := ScopeOf(strings.TrimPrefix(path, prefix))
		if !ok || row.Retired&doc == 0 {
			return
		}
		notes = append(notes, RetiredKeyNote{Path: path, Why: row.RetiredWhy})
	})
	return notes
}

// IgnoredKeyNote is a key a repository's file may still write that the
// resolution drops: the path as the file writes it and the sentence a person
// reads. It is RetiredKeyNote's sibling for the other half of the keys a file
// may no longer decide — the ones with no home in the project's own document —
// and the two are never the same key: an ignored key's value comes from a
// profile, which a repository picks and never writes (D3421).
type IgnoredKeyNote struct {
	// Path is the key's path as the file writes it, without the leaf: the block
	// the person deletes.
	Path string
	// Why is the sentence a person reads.
	Why string
}

// dropIgnoredKeys is tree with every key a repository's file may no longer
// decide removed, and the note of each, in workflow order: today the
// workflows.*.resources block, whose sizing the profile the workflow starts
// from decides (D3421).
//
// It is dropLiftedKeys' sibling and runs on the tree the lift left: the answer
// is a copy and tree itself stays as it was written, so Resolve judges a file's
// problems on the file as written and merges the copy. The two lists differ in
// where the value goes — a lifted key's home is the project's own document, an
// ignored key's is the profile the workflow starts from — which is why the note
// names that profile rather than a defaults: path.
func dropIgnoredKeys(tree map[string]any) (map[string]any, []IgnoredKeyNote) {
	out := copyTree(tree)
	wm, _ := out["workflows"].(map[string]any)
	var notes []IgnoredKeyNote
	for _, name := range sortedKeys(wm) {
		w, ok := wm[name].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := w["resources"]; !ok {
			continue
		}
		delete(w, "resources")
		notes = append(notes, IgnoredKeyNote{
			Path: "workflows." + name + ".resources",
			Why:  resourcesIgnored(name, profileNamed(w)),
		})
	}
	return out, notes
}

// profileNamed is the profile a workflow of a repository's file starts from as
// the file wrote it, "" for a workflow that names none.
func profileNamed(w map[string]any) string {
	n, ok := w["profile"].(*yaml.Node)
	if !ok || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// resourcesIgnored is the one sentence a repository's workflows.*.resources
// earns: the block is read and decides nothing, and the profile the workflow
// starts from sizes it (D3421). A workflow that names no profile is sized by
// Fugaro's defaults, and the note names no profile rather than one that decides
// nothing of that workflow's.
func resourcesIgnored(workflow, profile string) string {
	if profile == "" {
		return "workflows." + workflow + ".resources is ignored: Fugaro's defaults size this workflow"
	}
	return "workflows." + workflow + ".resources is ignored: profile " + profile + " sizes this workflow"
}

// keyPaths visits the path of every key of m, the block at prefix, in key
// order: a block's own path and then each key under it.
func keyPaths(m map[string]any, prefix string, visit func(path string)) {
	for _, k := range sortedKeys(m) {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		visit(path)
		if sub, ok := m[k].(map[string]any); ok {
			keyPaths(sub, path, visit)
		}
	}
}
