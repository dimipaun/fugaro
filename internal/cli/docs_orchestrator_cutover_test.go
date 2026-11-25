package cli

// R5's doc pin (D2729, rule-forced by F5). The cutover page used to ask each
// installation to import a session dump, approve items, stop the sweep, the
// Monitor watcher and the queue scripts by hand and test the caps — steps that
// are not `fugaro update` and so break the HARD RULE that `update` is the whole
// upgrade. The page is now the update and the project's switch: this test holds
// it to those two and to the absence of what the rule forbade, because a step
// that comes back here is a step the owner is asked to run by hand again.

import (
	"strings"
	"testing"
)

// cutoverGoneRE is what the update-only page may not carry any more: the dump
// import of the old cutover, the scripts it had the reader stop, and the 14-day
// manual rollback the window existed for.
var cutoverGoneRE = []string{
	"session-dump",
	"queue.txt",
	"rollback",
	"14 days",
	"Monitor watcher",
	"fugaro orchestrator import -f",
}

// TestCutoverIsUpdateOnly (R5, D2729): the page's cutover is `fugaro update`
// and its switch is `orchestrator_enabled`, and every hand step the page used
// to spell out is gone. It names both halves, so a page that dropped the switch
// — the one thing that hands orchestration back to the person — fails here as
// the page that kept the dump does.
func TestCutoverIsUpdateOnly(t *testing.T) {
	const path = "docs/how-to/orchestrator-cutover.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)
	for _, want := range []string{
		"fugaro update",           // the whole cutover, in one command
		"the whole upgrade",       // and the rule it is: update is everything
		"installs the login unit", // what it does on a machine with none (D2700)
		"no shadow week (D1235)",  // the cutover at once, which is unchanged
		"keeps its records",       // a run in flight survives the update (the HARD RULE's in-flight half)
		// The switch: where it lives, what the default is, how it is read, and
		// that the node's own answer is the one kept.
		"orchestrator_enabled",
		"top-level key of its published policy",
		"fugaro settings orchestrator_enabled --json",
		"While it is false the daemons launch nothing",
		"fugaro project policy publish",
		// What the session does with the daemon live, so a reader who followed
		// the page knows the work goes over as items rather than as launches.
		"fugaro orchestrator import --plan docs/<plan>",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q: the cutover is `fugaro update` plus the switch, and nowhere else writes either down", path, want)
		}
	}
	lower := strings.ToLower(flat)
	for _, gone := range cutoverGoneRE {
		if strings.Contains(lower, strings.ToLower(gone)) {
			t.Errorf("%s still carries %q, and D2729 leaves no hand step, no dump and no rollback window in this page", path, gone)
		}
	}
	// The page is a how-to about one command, so it runs that command and no
	// other: a second `fugaro` block would be a second step.
	blocks := 0
	for _, section := range docSections(doc) {
		for _, block := range bashBlocks(section) {
			if !runsFugaro(block) {
				continue
			}
			blocks++
			if len(block) != 1 || strings.TrimSpace(block[0]) != "fugaro update" {
				t.Errorf("%s runs %q in a block, and the cutover is the one command", path, block)
			}
		}
	}
	if blocks != 1 {
		t.Errorf("%s has %d fugaro blocks, want the one the cutover is: a step beside `fugaro update` is the thing D2729 removed", path, blocks)
	}
}
