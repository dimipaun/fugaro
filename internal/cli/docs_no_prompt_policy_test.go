package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The no-prompt policy folder reserves D1540–D1559; its reconciliation holds one
// row per decision, and a row with no answer is a decision nobody has made.
const npDocReconciliation = "docs/no-prompt-policy/reconciliation.md"

var npDecisionRow = regexp.MustCompile(`(?m)^\| \*\*D(\d+)\*\* \|(.*)$`)

func TestDocsNoPromptPolicyEveryDecisionIsAnsweredInRangeAndUnique(t *testing.T) {
	doc := readDoc(t, npDocReconciliation)
	seen := map[int]bool{}
	for _, m := range npDecisionRow.FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		if n < 1540 || n > 1559 {
			t.Errorf("%s: D%d is outside the reserved block D1540–D1559", npDocReconciliation, n)
		}
		if seen[n] {
			t.Errorf("%s: D%d has two rows", npDocReconciliation, n)
		}
		seen[n] = true
		cells := strings.Split(m[2], "|")
		answer := strings.TrimSpace(cells[len(cells)-2])
		if answer == "" || strings.Contains(answer, "(pending)") || !strings.Contains(answer, "2026-11-07") {
			t.Errorf("%s: D%d has no dated answer (%q)", npDocReconciliation, n, answer)
		}
	}
	if len(seen) == 0 {
		t.Fatalf("%s: no decision rows found", npDocReconciliation)
	}
}
