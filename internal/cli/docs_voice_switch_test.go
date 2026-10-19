package cli

import (
	"strings"
	"testing"
)

// TestDocsVoiceSwitchRegisterClaims pins the claims the P5.3 design
// (docs/mobile/design/voice-evaluation-switch.md) makes about other files,
// against those files: §13's register reading against the register it reads
// (docs/gcp-live-checklist.md), §2's account of the mailbox's fields and of
// which rule reads which node against the rules template and rules.go, §4.5 and
// §5's redaction sentences against `internal/agent/redact.go`, and §3's table
// against them all. Each half exists because a review round found the design
// contradicting the file it cited — §13's 67–68 row said "67 is written as 67a
// and 67b" while the checklist carries no `## Check 67a`, no `## Check 67b` and
// no `## Check 68` section and no summary row for any of them (the
// managed-projects plan reserves the numbers and its Task 5.3 writes them);
// §2 called `mac` and `kind` "not shipped" when P6.4 (#601, `371038f`) is
// merged into this branch, and attributed the kind check to PARENTKIND when the
// rules validate it inline; §5 rule 4 promised a redaction gate on the STT path
// that §3's own table says cannot exist; and §4.5 said the redactor stops the
// text, when it substitutes and hands it on.
//
// The register half is two-sided and both-halved, the way
// TestWebsiteChecklistHasCheck69aAnd69b is: every number the design names as
// written must have a summary row AND a `## Check` section in the register (a
// check with no row is one nobody reads first, a row with no section is a
// command nobody can follow), and the numbers it names as reserved must have
// neither (a design that invents a section it did not write is the bug this
// pins).
//
// The written list is deliberately the design's own claims rather than "every
// number in the register": a later check written elsewhere in the repository
// must not fail this test — only the design's own statements are pinned, and
// the count it gives ("36 `## Check` sections, counted at `07022b1`") is a
// historical statement about a named commit, so it is not recounted here.
//
// Mutation (verified, each run and restored): put "67 is written as 67a and
// 67b" back into §13's row; put rule 4's "on the STT path a hit means the
// utterance is not sent" back; put §4.5's "the text is not sent" back; restore
// §2's "not shipped"; restore §2's "checked field by field through PARENTKIND";
// rename a `## Check 49:` heading; or delete a `| 49 |` summary row — each
// fails this test.
func TestDocsVoiceSwitchRegisterClaims(t *testing.T) {
	const designPath = "docs/mobile/design/voice-evaluation-switch.md"
	const checklistPath = "docs/gcp-live-checklist.md"
	design := readDoc(t, designPath)
	checklist := readDoc(t, checklistPath)

	// 1. The numbers the design names as written, each a summary row AND a
	// `## Check` section — the eight written since the baseline a573870 (49,
	// 50, 52, 58, 69a, 69b, 70a, 70c), which is every number §13 names as
	// written and so the whole list this half pins. Both halves, the way
	// TestWebsiteChecklistHasCheck69aAnd69b pins 69a and 69b: a check that
	// exists as a section but not as a row is one nobody reads first, and a row
	// with no section is a command nobody can follow.
	for _, n := range []string{"49", "50", "52", "58", "69a", "69b", "70a", "70c"} {
		if !strings.Contains(checklist, "## Check "+n+":") {
			t.Errorf("%s has no `## Check %s:` section, but %s §13 names %s as written; correct the design's reading or this test's list", checklistPath, n, designPath, n)
		}
		row := ""
		for _, line := range strings.Split(checklist, "\n") {
			if !strings.HasPrefix(line, "| "+n+" |") {
				continue
			}
			if row != "" {
				t.Errorf("%s has more than one `| %s |` row: one check, one row", checklistPath, n)
			}
			row = line
		}
		if row == "" {
			t.Errorf("%s has no `| %s |` summary row, so the check §13 names as written is in no index a reader opens first", checklistPath, n)
			continue
		}
		if want := `See "Check ` + n + `" below`; !strings.Contains(row, want) {
			t.Errorf("the `| %s |` row does not point at its section (%q): %s", n, want, row)
		}
	}

	// 1b. The summary table's own gaps, which §13's count now states: 24 and 25
	// carry a `## Check` section each and no summary row, so the row run skips
	// 23→26. A reading that gives the rows as "19–38" is wrong for exactly
	// these two numbers.
	for _, n := range []string{"24", "25"} {
		if strings.Contains(checklist, "\n| "+n+" |") {
			t.Errorf("%s now has a `| %s |` summary row, so §13's count of the row run (\"19–23 and 26–38 (24 and 25 have `## Check` sections but no summary row)\") is stale and must be recounted", checklistPath, n)
		}
	}
	// Both halves of the corrected reading: the row run with its gap, and the
	// sentence naming the two numbers that make it a gap. Scoped to §13, since
	// the withdrawn wording is quoted verbatim in §11's round-12 entry and in
	// the sentence itself.
	checks := docSection(t, designPath, design, "## 13. The checks this would need")
	for _, want := range []string{"19–23 and 26–38", "24 and 25"} {
		if !strings.Contains(checks, want) {
			t.Errorf("%s's §13 no longer gives the summary row run as %q, so a reader cannot repeat the count from it", designPath, want)
		}
	}
	if strings.Contains(checks, "rows for 1 (and 1b), 2–18, 19–38,") {
		t.Errorf("%s's §13 gives the row run as \"19–38\", and the register has no `| 24 |` or `| 25 |` row", designPath)
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

	// 5. Which check reads which node. §2's `kind` is validated inline on its
	// own node against the four literals, and `PARENTKIND` is what gates
	// `options`, `reply_to` and `action` by the kind of the message they sit
	// on. An earlier revision attributed the kind check to PARENTKIND, which
	// would tell a reader the wrong rule is doing the work.
	for _, want := range []string{
		"newData.val() == 'message' || newData.val() == 'prompt'",
		"newData.val() == 'answer' || newData.val() == 'action'",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("internal/budget/rules/rules.json.tmpl no longer validates `kind` inline against the four literals (%q missing), so %s §2's account of which check reads which node is stale", want, designPath)
		}
	}
	if want := `case "PARENTKIND":`; !strings.Contains(readDoc(t, "internal/budget/rules/rules.go"), want) {
		t.Errorf("internal/budget/rules/rules.go no longer defines PARENTKIND, so %s §2's account of what gates `options`, `reply_to` and `action` is stale", designPath)
	}
	for _, want := range []string{
		"not** through `PARENTKIND`",
		"those three are what `PARENTKIND` gates",
	} {
		if !strings.Contains(exists, want) {
			t.Errorf("%s's §2 does not say %q, so it no longer says which check reads the kind field", designPath, want)
		}
	}
	if strings.Contains(exists, "checked field by field through `PARENTKIND`") {
		t.Errorf("%s's §2 says `kind` is checked through PARENTKIND, and the rules validate it inline on its own node", designPath)
	}

	// 6. The redactor masks; it does not block. `internal/agent/redact.go`'s
	// Redact and RedactFunc replace the match with `[REDACTED]` and hand the
	// string on, so a §4.5 row or a §5 rule 4 that says the text is dropped is
	// describing a filter the code does not have.
	redact := readDoc(t, "internal/agent/redact.go")
	for _, want := range []string{"[REDACTED]", "func Redact(s string, secrets []string) string", "func RedactFunc("} {
		if !strings.Contains(redact, want) {
			t.Errorf("internal/agent/redact.go no longer carries %q, so %s's account of what the redactor does is stale", want, designPath)
		}
	}
	modes := docSection(t, designPath, design, "### 4.5 Failure modes")
	for _, want := range []string{"substitute and never drop the string", "the text still goes"} {
		if !strings.Contains(modes, want) {
			t.Errorf("%s's §4.5 does not say %q, so its redactor row no longer matches what the redactor does", designPath, want)
		}
	}
	if strings.Contains(modes, "the text is not sent") {
		t.Errorf("%s's §4.5 says the redactor stops the text, and it masks it instead", designPath)
	}
	if !strings.Contains(rules, "it masks, it does not block") {
		t.Errorf("%s's §5 rule 4 no longer says the redactor masks rather than blocks", designPath)
	}
}
