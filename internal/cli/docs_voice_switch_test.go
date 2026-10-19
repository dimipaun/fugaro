package cli

import (
	"strings"
	"testing"
)

// TestDocsVoiceSwitchRegisterClaims pins §13's register reading in the P5.3
// design (docs/mobile/design/voice-evaluation-switch.md) against the register
// it reads (docs/gcp-live-checklist.md), because a review round found the
// table describing an allocation as a written check: the row for 67–68 said
// "67 is written as 67a and 67b" while the checklist carries no `## Check 67a`,
// no `## Check 67b` and no `## Check 68` section and no summary row for any of
// them — the managed-projects plan reserves the numbers and its Task 5.3 is
// what writes them.
//
// Two halves, because the same mistake can be made in either direction: every
// number the design names as written must have a `## Check` section in the
// register (a design that drops one is a design whose count no longer adds
// up), and the two the design names as reserved here must not have one (a
// design that invents a section it did not write is the bug this pins).
//
// The written list is deliberately the design's own claims rather than "every
// number in the register": a later check written elsewhere in the repository
// must not fail this test — only the design's own statements are pinned, and
// the count it gives ("36 `## Check` sections, counted at `07022b1`") is a
// historical statement about a named commit, so it is not recounted here.
//
// Mutation (verified): put "67 is written as 67a and 67b" back into §13's
// row, or add "58" to the reserved list below, and this test fails, both ways;
// remove the `## Check 49:` heading from the checklist and it fails too.
func TestDocsVoiceSwitchRegisterClaims(t *testing.T) {
	const designPath = "docs/mobile/design/voice-evaluation-switch.md"
	const checklistPath = "docs/gcp-live-checklist.md"
	design := readDoc(t, designPath)
	checklist := readDoc(t, checklistPath)

	// 1. The numbers the design names as written, each a summary row and a
	// `## Check` section: the eight written since the baseline a573870, and
	// the two the design's own count leans on for its "as it stands" reading.
	for _, n := range []string{"49", "50", "52", "58", "69a", "69b", "70a", "70c"} {
		if !strings.Contains(checklist, "## Check "+n+":") {
			t.Errorf("%s has no `## Check %s:` section, but %s §13 names %s as written; correct the design's reading or this test's list", checklistPath, n, designPath, n)
		}
	}

	// 2. The reservation the review round corrected: 67 is reserved as 67a
	// and 67b, 68 is the deployer account's live IAM check, and neither plan
	// has written them into the register yet.
	for _, n := range []string{"67a", "67b", "68"} {
		if strings.Contains(checklist, "## Check "+n+":") {
			t.Errorf("%s carries a `## Check %s:` section, but %s §13 says the managed-projects numbers are reserved and unwritten; the design's row must be updated to the register as it stands", checklistPath, n, designPath)
		}
	}
	row := ""
	for _, line := range strings.Split(design, "\n") {
		if strings.HasPrefix(line, "| **67–68** |") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("%s's §13 table no longer has a `| **67–68** |` row; update this test's marker", designPath)
	}
	for _, want := range []string{"reserved, and neither is in the register yet", "`## Check 67a`"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's §13 67–68 row does not say %q, so it reads as a written check rather than a reservation: %s", designPath, want, row)
		}
	}
	if strings.Contains(row, "67 is written as 67a and 67b") {
		t.Errorf("%s's §13 67–68 row claims 67 is written as 67a and 67b, and the register has neither", designPath)
	}

	// 3. The redaction gate §5 rule 4 now states, and the §3 table it agrees
	// with: the redactor reaches text (TTS, the summariser) and cannot reach
	// the STT payload, because audio is not a string. The two sentences that
	// carry it are pinned together so that fixing one and not the other —
	// which is what the review round found — fails here.
	for _, want := range []struct{ path, s string }{
		{designPath, "redaction is a function of a string, and audio is not a string"},
		{designPath, "On the STT path there is nothing for a redaction rule to match"},
	} {
		if !strings.Contains(readDoc(t, want.path), want.s) {
			t.Errorf("%s lacks %q, so the design no longer states that the redactor cannot reach the STT payload", want.path, want.s)
		}
	}
	// Scoped to §5 itself: the withdrawn sentence is quoted verbatim in §11's
	// round-10 entry, where it must stay readable, so only the live rules are
	// searched.
	rules := docSection(t, designPath, design, "## 5. What the app must never send")
	if strings.Contains(rules, "on the STT path a hit means the utterance is not sent") {
		t.Errorf("%s's §5 rule 4 promises the STT path is gated by the redactor, which §3's own table says it cannot be", designPath)
	}
}
