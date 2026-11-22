package localcfg

import (
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/policy"
)

// This file is AB8b of docs/always-a-budget/implementation-plan.md: the one
// word the release before this one accepted as a budget mode and this one does
// not (D3009), read out of a document's own text.
//
// F22 is why it exists at all. Both decoders of a mode — this package's
// validateBudget and the policy node's — ask policy.ValidMode, so once that
// answer refuses the old word (AB2) a config that says it no longer decodes,
// and `fugaro update` is the command that has to read such a config in order
// to rewrite it. So this package reads the word before it judges it: the
// document is decoded and the mode is held as it was written, and the word is
// what the update's own listing reads and what its rewrite splices out.
//
// The word is read and never quietly landed on observe here (C1 §5): a local
// config is a door that accepts text a person wrote, and the mode it says is
// what its own readers — the launcher, `doctor`, the job's environment — ask
// policy.Mode.Effective about (AB3, AB4, AB5). What this file answers is only
// that the file still loads, which is what makes the update that rewrites it
// possible.

// RemovedBudgetMode answers whether a document's `budget.mode` is the word
// that is not a budget mode any more (policy.Mode.Removed), and the word
// itself. It reads the document's text and nothing else — no validation, no
// defaults — because the callers that need it are the ones that must answer
// before validation runs: `fugaro update`'s listing of what will stop (R8) and
// the rewrite of the file it reads.
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

// acceptedBudgetMode is the mode a budget block may name: one this release
// knows, or the word that was one and is not any more. The word is accepted
// rather than refused because the file that says it is the file `fugaro
// update` rewrites (F22) — a config the migration cannot read is a config
// nothing can migrate — and it is accepted as written, so the readers that
// must tell it from a live mode read it through RemovedBudgetMode.
//
// Anything else is refused where it was written, which is AB2's validation and
// not this predicate's: it answers about a mode that was already read, and an
// unknown word is refused rather than landed on observe (C1 §4).
func acceptedBudgetMode(m string) bool {
	return policy.Mode(m).Removed() || policy.ValidMode(m)
}
