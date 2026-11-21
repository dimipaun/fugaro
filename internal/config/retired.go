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
