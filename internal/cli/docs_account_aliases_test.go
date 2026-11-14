package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const accountAliasesDir = "docs/account-aliases/"

const accountAliasesStatus = "Status as of 2026-11-10: plan only, 0/5 merged; nothing is implemented; D2090–D2102 answered and AL0–AL4 approved by the owner; see [docs/status.md](../status.md) for the current table."

// accountAliasesAnswer is the cell every decision carries: the owner answered
// D2090–D2102 as recommended on 2026-11-10, and a cell that drifts from it
// would read as a different answer than the one given.
const accountAliasesAnswer = "Owner, 2026-11-10: as recommended"

// accountAliasesImplementation is D2103's cell, and it says in the answer column
// that no answer was given: AL1 settles what a name a record carries means
// because its implementation cannot leave the question open, and a cell reading
// as a second owner answer would claim a ruling the owner never gave.
const accountAliasesImplementation = "**Not put to the owner**: AL1's implementation ruling, 2026-11-14, recorded here at code review's request (see §4)"

// TestDocsAccountAliases pins what the plan promises about itself: the number
// blocks it reserves, the folder's status line and its pointer to
// docs/status.md, that the decisions are exactly D2090–D2102 and each carries
// the owner's answer, and that the spec still quotes the ruling it was made from.
func TestDocsAccountAliases(t *testing.T) {
	readme := readDoc(t, accountAliasesDir+"README.md")
	for _, want := range []string{"D2090–D2109", "Checks 550–554", accountAliasesStatus} {
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
	if len(rows) != 14 {
		t.Fatalf("%sreconciliation.md has %d decision rows, want 14 (D2090–D2102 answered, D2103 written by AL1)", accountAliasesDir, len(rows))
	}
	for i, m := range rows {
		if want := strconv.Itoa(2090 + i); m[1] != want {
			t.Errorf("decision row %d is D%s, want D%s: the block is D2090–D2103 in order", i, m[1], want)
		}
		want := accountAliasesAnswer
		if m[1] == "2103" {
			want = accountAliasesImplementation
		}
		if answer := strings.TrimSpace(m[2]); answer != want {
			t.Errorf("decision D%s's answer cell is %q, want %q", m[1], answer, want)
		}
	}
	spec := readDoc(t, accountAliasesDir+"spec.md")
	if !strings.Contains(spec, "a stage's `claude_account` may name an alias, repointed by one command; repointing affects NEW launches only, never runs in flight (recommended).") {
		t.Errorf("spec.md no longer quotes the owner's ruling as received")
	}
}
