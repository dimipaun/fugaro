package cli

import (
	"strings"
	"testing"
)

// TestDocsVoiceSwitchRegisterClaims pins the claims the P5.3 design
// (docs/mobile/design/voice-evaluation-switch.md) makes about other files,
// against those files: §13's register reading against the register it reads
// (docs/gcp-live-checklist.md), §2's account of the mailbox's required fields
// against the rules template, and §3/§5's redaction sentences against each
// other. Each half exists because a review round found the design contradicting
// the file it cited — §13's 67–68 row said "67 is written as 67a and 67b" while
// the checklist carries no `## Check 67a`, no `## Check 67b` and no
// `## Check 68` section and no summary row for any of them (the
// managed-projects plan reserves the numbers and its Task 5.3 writes them);
// §2 called `mac` and `kind` "not shipped" when P6.4 (#601, `371038f`) is
// merged into this branch; and §5 rule 4 promised a redaction gate on the STT
// path that §3's own table says cannot exist.
//
// The register half is two-sided, because the same mistake can be made in
// either direction: every number the design names as written must have a
// `## Check` section in the register (a design that drops one is a design whose
// count no longer adds up), and the numbers it names as reserved here must not
// have one (a design that invents a section it did not write is the bug this
// pins).
//
// The written list is deliberately the design's own claims rather than "every
// number in the register": a later check written elsewhere in the repository
// must not fail this test — only the design's own statements are pinned, and
// the count it gives ("36 `## Check` sections, counted at `07022b1`") is a
// historical statement about a named commit, so it is not recounted here.
//
// Mutation (verified, each run and restored): put "67 is written as 67a and
// 67b" back into §13's row, or put rule 4's "on the STT path a hit means the
// utterance is not sent" back, or rename a `## Check 49:` heading in the
// checklist, or restore §2's "not shipped" — each fails this test.
func TestDocsVoiceSwitchRegisterClaims(t *testing.T) {
	const designPath = "docs/mobile/design/voice-evaluation-switch.md"
	const checklistPath = "docs/gcp-live-checklist.md"
	design := readDoc(t, designPath)
	checklist := readDoc(t, checklistPath)

	// 1. The numbers the design names as written, each a summary row and a
	// `## Check` section: the eight written since the baseline a573870 (49,
	// 50, 52, 58, 69a, 69b, 70a, 70c), which is every number §13 names as
	// written and so the whole list this half pins.
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

	// 4. The mailbox rules §2 says are shipped. P6.4 (#601, 371038f) is merged
	// into this branch, so the rules template requires `mac` on the inbox, the
	// outbox and the heartbeat; a §2 that calls those fields "not shipped"
	// contradicts the rules it cites, as an earlier revision's did.
	tmpl := readDoc(t, "internal/budget/rules/rules.json.tmpl")
	// Counted rather than merely found: `mac` is required on the inbox *and*
	// the outbox, so the string appears twice, and a template that dropped it
	// from one of the two would still satisfy a plain Contains.
	for _, want := range []struct {
		s   string
		min int
	}{
		{"hasChildren(['id', 'sender', 'text', 'at', 'mac'])", 2},
		{"hasChildren(['at', 'boot', 'mac'])", 1},
	} {
		if got := strings.Count(tmpl, want.s); got < want.min {
			t.Errorf("internal/budget/rules/rules.json.tmpl carries %q %d times, want at least %d, so %s §2's account of the mailbox's required fields is stale", want.s, got, want.min, designPath)
		}
	}
	// The design's own sentence, which is the half that went stale: §2 must
	// name the fields as shipped and carry the rules' two required sets. (The
	// withdrawn wording is quoted in §11's round-11 entry, so only §2 is read
	// here — a whole-document search would trip on that quotation.)
	exists := docSection(t, designPath, design, "## 2. What exists today")
	for _, want := range []string{
		"['id', 'sender', 'text', 'at', 'mac']",
		"['at', 'boot', 'mac']",
		"the fields P6.4 added are in it",
	} {
		if !strings.Contains(exists, want) {
			t.Errorf("%s's §2 does not say %q, so it no longer states the mailbox fields the rules require on this branch", designPath, want)
		}
	}
}
