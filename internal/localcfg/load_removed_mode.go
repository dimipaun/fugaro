package localcfg

import (
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// This file is AB8b of docs/always-a-budget/implementation-plan.md: the one
// word the release before this one accepted as a budget mode and this one does
// not (D3009), read out of a document's own text.
//
// F22 is why it exists at all. Both decoders of a mode — this package's
// validateBudget and the policy node's — ask policy.ValidMode, so once that
// answer refuses the old word (AB2) a config that says it no longer decodes,
// and `fugaro update` is the command that has to read such a config in order
// to rewrite it. The rewrite is therefore a fold beside the loader's other
// folds: it happens to the document's text, before the strict decode, and the
// word never reaches a reader that would refuse it. The update's own listing
// reads the same word from the file it read, through RemovedBudgetMode below.
//
// Nothing here validates: a document whose splice is refused is handed on
// unchanged, so the decoder refuses the word it says and the person gets the
// error naming it rather than a silent rewrite of a file this package could
// not read.

// RemovedBudgetMode answers whether a document's `budget.mode` is the word
// that is not a budget mode any more (policy.Mode.Removed), and the word
// itself. It reads the document's text and nothing else — no validation, no
// defaults — because the callers that need it are the ones that must answer
// before validation runs: `fugaro update`'s listing of what will stop (R8) and
// the rewrite below.
//
// A document that says no mode, or says one that is still a mode, answers
// ("", false): an absent mode is not the removed word, it is the observe every
// unset mode now means (R3, D3010).
func RemovedBudgetMode(data []byte) (string, bool) {
	var top map[string]yaml.Node
	if yaml.Unmarshal(data, &top) != nil {
		return "", false
	}
	m, ok := budgetModeNode(top)
	if !ok || !policy.Mode(m).Removed() {
		return "", false
	}
	return m, true
}

// budgetModeNode is the document's `budget.mode` scalar, as written.
func budgetModeNode(top map[string]yaml.Node) (string, bool) {
	b, ok := top["budget"]
	if !ok || b.Kind != yaml.MappingNode {
		return "", false
	}
	for i := 0; i+1 < len(b.Content); i += 2 {
		if b.Content[i].Value == "mode" {
			return b.Content[i+1].Value, true
		}
	}
	return "", false
}

// foldRemovedBudgetMode rewrites the removed word to what it means from now on
// (policy.Mode.Effective, which is observe), in place and byte for byte like
// the loader's other folds, so a config written before this release still
// loads and the update that rewrites the file can read it (F22, D3022).
//
// top is the document as it was written, which is where the word is read: the
// folds before this one rewrite other keys, and the version they read decides
// those, not this.
func foldRemovedBudgetMode(data []byte, top map[string]yaml.Node) []byte {
	m, ok := budgetModeNode(top)
	if !ok || !policy.Mode(m).Removed() {
		return data
	}
	out, err := yamledit.SetScalar(data, []string{"budget", "mode"}, policy.Mode(m).Effective())
	if err != nil {
		return data
	}
	return out
}
