package cli

import (
	"regexp"
	"strings"
	"testing"
)

const accountAliasesDir = "docs/account-aliases/"

const accountAliasesStatus = "Status as of 2026-11-10: plan only, 0/5 merged; nothing is implemented; no task is approved; see [docs/status.md](../status.md) for the current table."

// TestDocsAccountAliases pins what the plan promises about itself: the number
// blocks it reserves, the folder's status line and its pointer to
// docs/status.md, that every decision stays inside its block and is still
// open — the owner has answered none, and a cell filled in without an answer
// would read as one — and that the spec still quotes the ruling it was made from.
func TestDocsAccountAliases(t *testing.T) {
	readme := readDoc(t, accountAliasesDir+"README.md")
	for _, want := range []string{"D2070–D2089", "Checks 550–554", accountAliasesStatus} {
		if !strings.Contains(readme, want) {
			t.Errorf("%sREADME.md does not state %q", accountAliasesDir, want)
		}
	}
	plan := flattenDoc(readDoc(t, accountAliasesDir+"implementation-plan.md"))
	if !strings.Contains(plan, accountAliasesStatus) {
		t.Errorf("the plan does not carry the folder's current status line")
	}
	rec := readDoc(t, accountAliasesDir+"reconciliation.md")
	row := regexp.MustCompile(`(?m)^\| \*\*D([0-9]{4})\*\* \|.*\| ([^|]*) \|$`)
	rows := row.FindAllStringSubmatch(rec, -1)
	if len(rows) == 0 {
		t.Fatalf("%sreconciliation.md has no decision rows", accountAliasesDir)
	}
	for _, m := range rows {
		if n := m[1]; n < "2070" || n > "2089" {
			t.Errorf("decision D%s is outside the reserved block D2070-D2089", n)
		}
		if answer := strings.TrimSpace(m[2]); answer != "OPEN" {
			t.Errorf("decision D%s's answer cell is %q; the owner has answered none of them", m[1], answer)
		}
	}
	spec := readDoc(t, accountAliasesDir+"spec.md")
	if !strings.Contains(spec, "a stage's `claude_account` may name an alias, repointed by one command; repointing affects NEW launches only, never runs in flight (recommended).") {
		t.Errorf("spec.md no longer quotes the owner's ruling as received")
	}
}
