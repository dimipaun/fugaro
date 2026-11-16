package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

// TestEveryUnwrittenRowCarriesALabelAndAReason is T5's pin, and L1's own
// red step: a registry row no command writes is registered with one of the
// three labels D2611 settled and a one-line reason, so that the label with its
// reason — and not a command line nobody can run — is the fact that stands in
// for a writer. Delete one row's label and this test fails on that row.
func TestEveryUnwrittenRowCarriesALabelAndAReason(t *testing.T) {
	measured := map[string]bool{}
	for _, r := range settings.Rows() {
		if !settingsRowNeedsALabel(r) {
			continue
		}
		measured[r.Key] = true
		w, ok := settings.WriterOf(r.Key)
		if !ok {
			t.Errorf("the registry row %s is written by no command and no writer answers for it: register a labelled writer in writers_labels.go, or name the task that gives it one in settingsLabelledElsewhere (docs/contracts/config-ownership.md §5)", r.Key)
			continue
		}
		if w.Label == "" {
			t.Errorf("the registry row %s is written by no command and its writer carries no label: a row no command writes is registered with %s, %s or %s (D2611)", r.Key, settings.LabelPlumbing, settings.LabelFakeEndpoint, settings.LabelFile)
			continue
		}
		if w.Argv != nil || w.Command != "" || w.Store != settings.StoreNone {
			t.Errorf("the registry row %s is labelled %s and also states the command %q, a command line or the store %s: a label is what stands in for a runnable writer, and a labelled row has neither an argv nor a store (T5, D2611)", r.Key, w.Label, w.Command, w.Store)
		}
		if strings.TrimSpace(w.Reason) == "" {
			t.Errorf("the registry row %s is labelled %s with no reason: the label and its one-line reason are the pinned fact (D2611, T5)", r.Key, w.Label)
		}
		if strings.Contains(w.Reason, "\n") {
			t.Errorf("the registry row %s carries a reason of more than one line: the label's fact is one line, and the longer story belongs in a document (T5)", r.Key)
		}
	}
	if len(measured) == 0 {
		t.Fatal("no registry row was measured as unwritten: this test would pass vacuously, and it is the only thing holding the labels in place")
	}

	for _, r := range settingsLabelRows {
		if _, ok := settings.Lookup(r.pattern); !ok {
			t.Errorf("writers_labels.go labels %s, which is no longer a registry row: a label registered for a key the registry dropped answers no reader", r.pattern)
		}
		if !measured[r.pattern] {
			t.Errorf("writers_labels.go labels %s and the measurement above does not reach it: the row's owner moved to a command family, or another task claimed it, and the label is then a second answer to one question", r.pattern)
		}
	}
	for key, holder := range settingsLabelledElsewhere {
		if _, ok := settings.Lookup(key); !ok {
			t.Errorf("settingsLabelledElsewhere names %s, which is no longer a registry row; it is there because %s", key, holder)
		}
	}

	// F18, as the probe left it: nothing outside a test assigns a
	// localcfg.Endpoints field, so `fugaro setup` never writes one and the
	// block is the fake-endpoint label's, not `plumbing`.
	w, ok := settings.WriterOf("endpoints.run")
	if !ok || w.Label != settings.LabelFakeEndpoint || w.Reason == "" {
		t.Errorf("endpoints.run answers with the label %q (known: %v) and the reason %q: `fugaro setup` writes no endpoint and only a test sets one, so each endpoint carries the %s label and its reason (F18)", w.Label, ok, w.Reason, settings.LabelFakeEndpoint)
	}
}
