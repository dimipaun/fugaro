package cli

// O11's doc pin. The cutover page is the only place the one-time import and the
// manual rollback are written down, so these tests hold the page to what the
// plan and T1 ruled: the cutover happens at once (D1235, no shadow week), the
// dump is imported by `fugaro orchestrator import` and no dump approves work —
// not even a decision the dump raises itself — and going back is the three
// hand-run steps inside the 14 days the scripts stay on disk — there is no
// rollback command to run instead.

import (
	"regexp"
	"strings"
	"testing"
)

// rollbackStepRE finds a numbered step of the rollback list, which is the shape
// a reader follows by hand: the count and the order are the pin, so a fourth
// step, or the same three in another order, is a different rollback than the
// one Check 302 records.
var rollbackStepRE = regexp.MustCompile(`(?m)^[0-9]+\. `)

// TestDocsOrchestratorCutoverNamesTheManualRollback pins the page's rollback
// half: the absence of a rollback command stated in words, the three steps it
// names as the whole of going back, in order, and the 14 days the scripts are
// kept for. The failure it guards is the quiet one — a step edited away here is
// a step lost, because the how-to is where the rollback lives — and a "fugaro
// orchestrator rollback" offered in prose would send a reader looking for a
// command this release does not have, so the family's own subcommands are
// checked for one too.
func TestDocsOrchestratorCutoverNamesTheManualRollback(t *testing.T) {
	const path = "docs/how-to/orchestrator-cutover.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)
	for _, want := range []string{
		"with no shadow week (D1235)",    // the cutover at once
		"fugaro orchestrator import -f ", // the one-time dump
		"a dump carrying one is refused", // no dump approves work
		"arrives unapproved",             // and every item it declares is left to the person
		"14 days",                        // T1's window
		"they stay on disk",              // no step here deletes the scripts
		"No rollback command is built.",  // the absence, said where a reader looks for the undo
		"the claim is per run id",        // why the scripts' launching has to stop first
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q: the cutover and its rollback are written down nowhere else", path, want)
		}
	}
	if strings.Contains(flat, "fugaro orchestrator rollback") {
		t.Errorf("%s offers a rollback command, and the rollback is the three steps by hand (T1)", path)
	}
	for _, sub := range newOrchestratorCmd().Commands() {
		if sub.Name() == "rollback" {
			t.Errorf("the orchestrator family has a rollback command, and the how-to promises there is none")
		}
	}

	rollback := docSection(t, path, doc, "## The rollback, by hand")
	steps := rollbackStepRE.FindAllString(rollback, -1)
	if len(steps) != 3 {
		t.Fatalf("%s's rollback lists %d steps (%v), and the rollback is the three steps T1 ruled", path, len(steps), steps)
	}
	for i, step := range steps {
		if want := orchNum(i+1) + ". "; step != want {
			t.Errorf("the rollback's step %d is numbered %q, want %q: they are run in this order", i+1, step, want)
		}
	}
	// Each step carries its own act, and in the order the owner runs them:
	// stopping the daemon, re-arming what the daemon replaced, and seeing one
	// launch through it. "Before removing anything" is the third step's own
	// condition, so a page that kept the scripts for the 14 days and then had a
	// reader delete them first would fail here.
	at := -1
	for _, want := range []string{
		"Stop the daemon: `fugaro orchestrator uninstall`",
		"Re-arm the sweep, the Monitor watcher and the queue scripts",
		"See one launch through the scripts before removing anything",
	} {
		i := strings.Index(flattenDoc(rollback), want)
		if i < 0 {
			t.Errorf("%s's rollback does not carry the step %q", path, want)
			continue
		}
		if i < at {
			t.Errorf("%s's rollback carries %q out of the order the steps are run in", path, want)
		}
		at = i
	}
}

// TestDocsOrchestratorCutoverSaysWhatTheImportIs is the page's other half: the
// import is the one document in this loop a model wrote, so the page says it is
// read as a patch from the person, that it never approves work, and that the
// scripts stop launching before the daemon starts — the one thing Check 302
// must not get wrong, because the two share no launch key and would launch the
// same work twice.
func TestDocsOrchestratorCutoverSaysWhatTheImportIs(t *testing.T) {
	const path = "docs/how-to/orchestrator-cutover.md"
	flat := flattenDoc(readDoc(t, path))
	for _, want := range []string{
		"`fugaro orchestrator patch`'s own validator and bounded by its own caps", // the same validator and caps as patch
		"the one document in this loop a model wrote",                             // the first stored-prompt-injection surface
		"lands as you",                               // from a person, not a session
		"the daemon launches approved work only",     // what the person is left to approve
		"Stop whatever starts runs from `queue.txt`", // the launching, stopped before the daemon starts
		// The refusal is whole however the person's decision reached the document.
		// A dump that raises a person-waiting decision and answers it itself is the
		// case a reader has to be told about: no single op of it looks like the
		// person's own word, and only read together do the two show one.
		"whether the decision was already in the node or the dump raised it itself",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q", path, want)
		}
	}
}
