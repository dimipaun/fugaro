package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestDocsVoiceSwitchRegisterClaims pins the claims the P5.3 design
// (docs/mobile/design/voice-evaluation-switch.md) makes about other files,
// against those files: §13's register reading against the register it reads
// (docs/gcp-live-checklist.md) — in both directions, the rows and sections it
// says the register carries and the numbers it says are not in the register —
// §2's account of the mailbox's fields, of which check reads which node and of
// where that check lives against the rules template and rules.go, §4.5 and §5's
// redaction sentences against `internal/agent/redact.go`, and §3's table
// against them all. Each half exists because a review round found the design
// contradicting the file it cited — §13's 67–68 row said "67 is written as 67a
// and 67b" while the checklist carries no `## Check 67a`, no `## Check 67b` and
// no `## Check 68` section and no summary row for any of them (the
// managed-projects plan reserves the numbers and its Task 5.3 writes them) —
// and that row has since been half-made true: Check 68 was written into the
// register by this repository's 0.10.0 fence work, so §13's row now says 68 is
// written and 67a/67b are not, and this test pins each half in its own
// direction;
// §2 called `mac` and `kind` "not shipped" when P6.4 (#601, `371038f`) is
// merged into this branch, and attributed the kind check to PARENTKIND when the
// rules validate it inline; §5 rule 4 promised a redaction gate on the STT path
// that §3's own table says cannot exist; §4.5 said the redactor stops the
// text, when it substitutes and hands it on; §13 attributed its own count
// of the row run to the register ("the register says so of itself"), which the
// register never says of itself; and §13 gave a quoted convention to the
// register ("the phase that ships the check writes them") when the sentence is
// voice.md's, the register contains it nowhere, and both of §13's claims about
// a number whose body lives elsewhere (67a/67b/68 reserved, 57's section in
// compute-backend.md §7.2) had only one half pinned.
//
// The register half is two-sided and both-halved, the way
// TestWebsiteChecklistHasCheck69aAnd69b is: every number the design names as
// written must have a summary row AND a `## Check` section in the register (a
// check with no row is one nobody reads first, a row with no section is a
// command nobody can follow), and the numbers it names as reserved must have
// neither (a design that invents a section it did not write is the bug this
// pins). Both halves are read from the register's own summary table and
// headings, so deleting a row or renaming a heading fails here.
//
// Part 7 is the same rule for prose: every quotation the design gives to
// another file is checked against that file AND against the design, so a
// quotation cannot be re-attributed by editing one side of the pair.
//
// The list of numbers is deliberately the design's own claims rather than "every
// number in the register": a later check written elsewhere in the repository
// must not fail this test — only the design's own statements are pinned (and
// only in the direction the design states them: nothing here requires the
// register to carry no row beyond the run §13 writes out), while the count it
// gives ("36 `## Check` sections, counted at `07022b1`") is a historical
// statement about a named commit, so it is not recounted here.
//
// Mutation (verified, each run and restored): put "67 is written as 67a and
// 67b" back into §13's row; put rule 4's "on the STT path a hit means the
// utterance is not sent" back; put §4.5's "the text is not sent" back; restore
// §2's "not shipped"; restore §2's "checked field by field through PARENTKIND";
// restore §2's "rules.go:583-590"; give §2 the citation it carried before
// mobile-auth P2's comment grew the file, "rules.go:584-592"; rename a
// `## Check 49:` heading; delete a
// `| 49 |`, `| 57 |`, `| 11b |`, `| 81 |`, `| 82 |` or `| 89 |` summary row;
// rename `## Check 19:`; add a
// `| 48 |` row or a `## Check 69c` section; restore §13's "the register says so
// of itself"; drop "11 also carries **11b**" from §13's run; add a `| 67a |`
// row, a `## Check 67b:` section, a `## Check 57:` section, or delete the
// `| 68 |` row or the `## Check 68:` section; stop the `| 57 |`
// row pointing at compute-backend.md §7.2; restore "The convention is the
// register's and the mobile plan's"; drop the §5.3 attribution or lowercase the
// plan's quoted sentence; reword a quoted sentence in its source file; delete a
// quotation from §13; or point §5.3's 70b sentence at "the count below" when the
// count is above it — each fails this test.

// voiceSwitchWritten is every number §13 of the design names as written in the
// register — each a summary row AND a `## Check` section: the eight written
// since the baseline a573870 (49, 50, 52, 58, 69a, 69b, 70a, 70c), 70b, which
// §13's 70a–70c row names as written, and the mobile work's 81, 82 and 89.
// Named once; its length is never stated, because a stated count over a list is
// the drift this file keeps catching — the counts §13 gives are derived from the
// register at test time (part 1c).
var voiceSwitchWritten = []string{"49", "50", "52", "58", "69a", "69b", "70a", "70b", "70c", "81", "82", "89"}

func TestDocsVoiceSwitchRegisterClaims(t *testing.T) {
	const designPath = "docs/mobile/design/voice-evaluation-switch.md"
	const checklistPath = "docs/gcp-live-checklist.md"
	design := readDoc(t, designPath)
	checklist := readDoc(t, checklistPath)

	// 1. The numbers the design names as written, each a summary row AND a
	// `## Check` section: every number §13 names as written (voiceSwitchWritten
	// below: the eight written since the baseline a573870 — 49, 50, 52, 58, 69a,
	// 69b, 70a, 70c — plus 70b, which this branch writes, and the mobile work's
	// 81, 82 and 89), and so the whole list this half pins. Both halves, the way
	// TestWebsiteChecklistHasCheck69aAnd69b pins 69a and 69b: a check that
	// exists as a section but not as a row is one nobody reads first, and a row
	// with no section is a command nobody can follow. The list is named once and
	// no count of it is written down: a count is derived from the register
	// (part 1c), so there is no number here for a later check to leave stale.
	for _, n := range voiceSwitchWritten {
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

	// 1b. §13's reading of the register, read from the register rather than
	// trusted — both directions of it. The row run it writes out is 1 (and 1b),
	// 2–18 (11 also carries 11b), 19–23 and 26–38, 40–47, 49, 50, 52, 57, 58,
	// 69a, 69b, 70a and 70c — that is the run as §13 writes it, a count taken at
	// 07022b1, so the list this half wants is the run plus the numbers §13 names
	// as written since (70b, 81, 82, 89). Every number in that list must have a
	// row; and every number §13 says is written as a `## Check` section (19–47
	// with 39 living in the retire-image-mirror plan as 39a–39f, plus
	// voiceSwitchWritten) must have one. Without
	// this half the test pins only what the register must NOT have: deleting
	// `| 57 |`, `| 11b |` or `## Check 19` left it green.
	rows := summaryRows(t, checklistPath, checklist)
	wantRows := []string{"1", "1b", "11b"}
	for _, run := range []struct{ from, to int }{{2, 23}, {26, 38}, {40, 47}} {
		for i := run.from; i <= run.to; i++ {
			wantRows = append(wantRows, strconv.Itoa(i))
		}
	}
	wantRows = append(wantRows, "49", "50", "52", "57", "58", "69a", "69b", "70a", "70b", "70c", "81", "82", "89")
	for _, n := range wantRows {
		if !rows[n] {
			t.Errorf("%s's summary table has no `| %s |` row, but %s §13 names %s as a written row — in its row run, or, for 70b, in its 70a–70c row; correct the design's reading or this test's list", checklistPath, n, designPath, n)
		}
	}
	// 24 and 25 are the reverse — a `## Check` section each and no summary row,
	// so the row run skips 23→26. A reading that gives the rows as "19–38" is
	// wrong for exactly these two numbers.
	for _, n := range []string{"24", "25"} {
		if rows[n] {
			t.Errorf("%s now has a `| %s |` summary row, so §13's count of the row run (\"19–23 and 26–38 (24 and 25 have `## Check` sections but no summary row)\") is stale and must be recounted", checklistPath, n)
		}
	}
	wantSections := append([]string{}, voiceSwitchWritten...)
	for i := 19; i <= 47; i++ {
		if i != 39 { // 39 is written as 39a–39f in the retire-image-mirror plan, not here
			wantSections = append(wantSections, strconv.Itoa(i))
		}
	}
	for _, n := range wantSections {
		if !strings.Contains(checklist, "## Check "+n+":") {
			t.Errorf("%s has no `## Check %s:` section, but %s §13 names %s as written (19–47 written, 39 as 39a–39f in the retire-image-mirror plan)", checklistPath, n, designPath, n)
		}
	}
	// 1c. The counts §13 states, derived from the register's own headings and
	// compared with the figure the design prints, so no number in this test is a
	// hand-kept copy of one in the design. "(28 of them" is the 19–47 run's
	// sections (39 excluded: it lives in the retire-image-mirror plan), and the
	// dated count "carries 36 `## Check` sections" at 07022b1 is that run plus
	// the eight written since the baseline — the sections §13 says existed at
	// that commit; the four written after it (70b, 81, 82, 89) are not in it.
	headings := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^## Check (\d+[a-z]?):`).FindAllStringSubmatch(checklist, -1) {
		headings[m[1]] = true
	}
	run := 0
	for i := 19; i <= 47; i++ {
		if headings[strconv.Itoa(i)] {
			run++
		}
	}
	atCount := run
	for _, n := range []string{"49", "50", "52", "58", "69a", "69b", "70a", "70c"} {
		if headings[n] {
			atCount++
		}
	}
	checks13 := docSection(t, designPath, design, "## 13. The checks this would need")
	for _, c := range []struct {
		re   string
		got  int
		what string
	}{
		{`\((\d+) of them;`, run, "the 19–47 run's `## Check` sections"},
		{`carries (\d+) ` + "`## Check`" + ` sections`, atCount, "the `## Check` sections the register carried at 07022b1"},
	} {
		m := regexp.MustCompile(c.re).FindStringSubmatch(checks13)
		if m == nil {
			t.Errorf("%s's §13 no longer states %s (pattern %q), so its count cannot be checked against the register", designPath, c.what, c.re)
			continue
		}
		if stated, _ := strconv.Atoi(m[1]); stated != c.got {
			t.Errorf("%s's §13 states %d for %s, and %s has %d: recount", designPath, stated, c.what, checklistPath, c.got)
		}
	}

	// 39 is the one number in that run whose section is not in the register, and
	// the design says where it lives instead. A `## Check 39` section written
	// later makes that sentence stale.
	if strings.Contains(checklist, "## Check 39:") {
		t.Errorf("%s now has a `## Check 39:` section, but %s §13 says 39 is written as 39a–39f in the retire-image-mirror plan; recount the section", checklistPath, designPath)
	}
	if !strings.Contains(readDoc(t, "docs/plans/2026-10-13-retire-image-mirror.md"), "live checks 39a–39f") {
		t.Errorf("%s §13 says 39 is written as 39a–39f in docs/plans/2026-10-13-retire-image-mirror.md, which no longer names them that way", designPath)
	}
	// The other half of the 1–18 row: those numbers are rows WITHOUT a section,
	// 1b and 11b included. A `## Check 1` written later makes §13's row wrong,
	// which is what this pins.
	rowOnly := []string{"1b", "11b"}
	for i := 1; i <= 18; i++ {
		rowOnly = append(rowOnly, strconv.Itoa(i))
	}
	for _, n := range rowOnly {
		if strings.Contains(checklist, "## Check "+n+":") {
			t.Errorf("%s now has a `## Check %s:` section, but %s §13 says 1–18 (and 1b, 11b) are summary rows with no section; recount the row", checklistPath, n, designPath)
		}
	}
	// The numbers §13 names as in neither half of the register: a row or a
	// section for any of them means the design's reading is stale. (85 is the
	// bake-off, which §13 says is written nowhere; 110 is the number this
	// document names and writes nothing into. 81, 82 and 89 are excluded from
	// the 80–89 run because §13's row names them as written by Task P4.2a, and
	// the first half above requires their rows and sections; 70b is not here
	// because §13's 70a–70c row names it as written.)
	absent := []string{"48", "51", "53", "54", "55", "56", "69c", "85", "110"}
	for i := 80; i <= 89; i++ {
		if i == 81 || i == 82 || i == 89 {
			continue
		}
		absent = append(absent, strconv.Itoa(i))
	}
	for _, n := range absent {
		if rows[n] {
			t.Errorf("%s now has a `| %s |` summary row, but %s §13 says %s is not in the register; recount the row", checklistPath, n, designPath, n)
		}
		if strings.Contains(checklist, "## Check "+n+":") {
			t.Errorf("%s now has a `## Check %s:` section, but %s §13 says %s is not in the register; recount the section", checklistPath, n, designPath, n)
		}
	}
	// The sentences that carry the reading. Scoped to §13, since the withdrawn
	// wording is quoted verbatim in §11's round-12 and round-13 entries.
	checks := docSection(t, designPath, design, "## 13. The checks this would need")
	for _, want := range []string{"19–23 and 26–38", "24 and 25", "11 also carries **11b**", "counted here from the file", "68 is written, 67a and 67b are not"} {
		if !strings.Contains(checks, want) {
			t.Errorf("%s's §13 no longer says %q, so a reader cannot repeat the count from it", designPath, want)
		}
	}
	if strings.Contains(checks, "rows for 1 (and 1b), 2–18, 19–38,") {
		t.Errorf("%s's §13 gives the row run as \"19–38\", and the register has no `| 24 |` or `| 25 |` row", designPath)
	}
	if strings.Contains(checks, "the register says so of itself") {
		t.Errorf("%s's §13 attributes its own count of the row run to the register, which says no such thing of itself", designPath)
	}

	// 2. The managed-projects numbers, in both directions, because they no
	// longer share a state: 68 (the deployer account's live IAM check) IS
	// written — a `| 68 |` summary row and a `## Check 68:` section, the shape
	// Check 68's own test pins — while 67 is still only reserved as 67a and
	// 67b, with neither half in the register. §13's row is what says so, and a
	// row or a section appearing for 67a/67b, or disappearing for 68, makes it
	// stale in one direction or the other.
	if !rows["68"] {
		t.Errorf("%s's summary table has no `| 68 |` row, but %s §13 says 68 is written", checklistPath, designPath)
	}
	if !strings.Contains(checklist, "## Check 68:") {
		t.Errorf("%s carries no `## Check 68:` section, but %s §13 says 68 is written — a row with no section is a command nobody can follow", checklistPath, designPath)
	}
	for _, n := range []string{"67a", "67b"} {
		if strings.Contains(checklist, "## Check "+n+":") {
			t.Errorf("%s carries a `## Check %s:` section, but %s §13 says the 67 half is reserved and unwritten; the design's row must be updated to the register as it stands", checklistPath, n, designPath)
		}
		if rows[n] {
			t.Errorf("%s carries a `| %s |` summary row, but %s §13 says the 67 half is reserved and unwritten — and a row is the half a reader sees first", checklistPath, n, designPath)
		}
	}
	// 2b. §13's other "where the body lives" claim: **57 is a summary row whose
	// section is not in the register** — it is in compute-backend.md §7.2. Both
	// halves again: the row here, the pointed-to body there, and no `## Check
	// 57:` section in this file.
	if !rows["57"] {
		t.Errorf("%s's summary table has no `| 57 |` row, but %s §13 says 57 is a summary row", checklistPath, designPath)
	}
	if strings.Contains(checklist, "## Check 57:") {
		t.Errorf("%s now has a `## Check 57:` section, but %s §13 says 57's section lives in docs/design/compute-backend.md §7.2", checklistPath, designPath)
	}
	row57 := ""
	for _, line := range strings.Split(checklist, "\n") {
		if strings.HasPrefix(line, "| 57 |") {
			row57 = line
		}
	}
	if row57 == "" {
		t.Fatalf("%s's §13 names 57 as a written row and this test's marker `| 57 |` found none; update the marker", checklistPath)
	}
	if want := `See "Check 57" in ` + "`docs/design/compute-backend.md` §7.2"; !strings.Contains(row57, want) {
		t.Errorf("%s's `| 57 |` row does not point at %q, so it no longer says where the check's body is: %s", checklistPath, want, row57)
	}
	compute := readDoc(t, "docs/design/compute-backend.md")
	heading, body := false, ""
	for _, line := range strings.Split(compute, "\n") {
		if line == "### 7.2 The check" { // the heading itself, not a renamed one
			heading, body = true, ""
			continue
		}
		if heading && strings.HasPrefix(line, "### ") {
			break // the section ends at the next heading
		}
		if heading {
			body += line + "\n"
		}
	}
	if !heading {
		t.Errorf("docs/design/compute-backend.md has no `### 7.2 The check` heading, so %s §13's sentence that 57's section lives in its §7.2 is stale", designPath)
	} else if !strings.Contains(body, "Check 57: the compute-instance Docker backend") {
		t.Errorf("docs/design/compute-backend.md's §7.2 no longer opens with the check's own title, so %s §13's sentence that 57's body lives there is stale", designPath)
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
	for _, want := range []string{"68 is written, 67a and 67b are not", "`## Check 67a`"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's §13 67–68 row does not say %q, so a reader cannot tell which half of the pair is written from it: %s", designPath, want, row)
		}
	}
	if strings.Contains(row, "reserved, and neither is in the register yet") {
		t.Errorf("%s's §13 67–68 row still reads as a reservation of both numbers, and the register carries Check 68", designPath)
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
			t.Errorf("%s's §2 does not say %q, so it no longer says which check reads the kind field, or where that check is", designPath, want)
		}
	}
	for _, bad := range []string{
		"checked field by field through `PARENTKIND`",
		"rules.go:583-590",
	} {
		if strings.Contains(exists, bad) {
			t.Errorf("%s's §2 carries %q, which the rules contradict: `kind` is validated inline on its own node", designPath, bad)
		}
	}
	// §2's line citation, resolved rather than pinned to a literal. It was
	// `583-590`, round 13 corrected it to `584-592`, and mobile-auth P2's
	// comment above `PathDevices` grew the file by 27 lines and moved the case
	// to 611 and its `parent()` read to 619 — three revisions, one pinned
	// string, and the drift a literal cannot see. So the two lines are read out
	// of rules.go and §2 has to name them.
	src := strings.Split(readDoc(t, "internal/budget/rules/rules.go"), "\n")
	caseAt, readAt := 0, 0
	for i, l := range src {
		if caseAt == 0 && strings.Contains(l, `case "PARENTKIND":`) {
			caseAt = i + 1
		}
		if readAt == 0 && strings.Contains(l, `kindOf("newData.parent()")`) {
			readAt = i + 1
		}
	}
	if caseAt == 0 || readAt == 0 {
		t.Fatalf("internal/budget/rules/rules.go carries no `case \"PARENTKIND\":` (%d) or no `kindOf(\"newData.parent()\")` (%d), so the citation this test resolves has nothing to resolve against", caseAt, readAt)
	}
	if cite := "rules.go:" + strconv.Itoa(caseAt) + "-" + strconv.Itoa(readAt); !strings.Contains(exists, cite) {
		t.Errorf("%s's §2 does not cite PARENTKIND at %q, where internal/budget/rules/rules.go carries the case (:%d) and its parent() read (:%d): the sentence points at whatever now sits on the lines it names", designPath, cite, caseAt, readAt)
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

	// 7. Every quotation the design gives to another file, against that file.
	// An earlier revision attributed "the phase that ships the check writes
	// them" to the register, which does not contain it (it is voice.md's), and
	// gave a second quotation the register's name too; the sweep that fixed it
	// checked the rest, and this pins them. Each pair is the words as the
	// SOURCE writes them (emphasis and line wrapping aside — see flat) and the
	// file the design attributes them to.
	for _, q := range []struct{ quote, src string }{
		{"the phase that ships the check writes them", "docs/design/voice.md"},
		{"A check is never run before its text is in the register", "docs/mobile/implementation-plan.md"},
		{"The app never stores or uploads audio", "docs/mobile/implementation-plan.md"},
		{"so the audio never leaves the device", "docs/mobile/reconciliation.md"},
		{"it must be stated as a rule rather than left as an implication", "docs/mobile/reconciliation.md"},
		{"local-only for v1", "docs/design/voice.md"},
		{"Your machine stays your business", "docs/design/desktop-console.md"},
		{"Audio fixtures are synthesised (Task V4.3)", "docs/plans/2026-10-16-voice.md"},
		{"v1's whole provider path is local-only", "docs/plans/2026-10-16-voice.md"},
		{"TOKEN && auth.token.fs == $slug", "internal/budget/rules/rules.json.tmpl"},
		{"read-back text is built from the resolved target and from nothing", "shared/confirm/index.js"},
		{"a confirmation is a separate utterance", "shared/confirm/index.js"},
		{"An answer to a destructive prompt is accepted only as a MAC-verified action record", "docs/mobile/functional-spec-reconciliation.md"},
		{"any launcher can mint a token for any mu or mc", "docs/mobile/functional-spec-reconciliation.md"},
		{"the service stays secrets-only", "docs/credentials-service/spec.md"},
		{"reserved for this work's owner-run checks", "docs/credentials-service/README.md"},
		{"Checks 70–79 are unused and deliberately not taken", "docs/mobile/README.md"},
	} {
		if !strings.Contains(strings.ToLower(flat(design)), strings.ToLower(flat(q.quote))) {
			t.Errorf("%s no longer carries %q, which it attributes to %s; this pair is stale and should be re-pointed or dropped", designPath, q.quote, q.src)
		}
		if !strings.Contains(strings.ToLower(flat(readDoc(t, q.src))), strings.ToLower(flat(q.quote))) {
			t.Errorf("%s gives %q to %s, and that file does not carry it: cite the file the words are in, or quote what it says", designPath, q.quote, q.src)
		}
	}
	// The two halves of the attribution itself: each quotation named with the
	// file it is in, and neither given to the register.
	for _, want := range []string{
		"[voice.md](../../design/voice.md) **§5.3**",
		`[implementation-plan.md](../implementation-plan.md), the block "Check numbers this plan takes (80–89)"`,
		`"**A check is never run before its text is in the register**"`,
	} {
		if !strings.Contains(checks, want) {
			t.Errorf("%s's §13 does not attribute its quoted convention with %q", designPath, want)
		}
	}
	if strings.Contains(design, "The convention is the register's and the mobile plan's") {
		t.Errorf("%s gives the convention to the register, and the register contains neither sentence of it", designPath)
	}
	if strings.Contains(checklist, "the phase that ships the check writes them") {
		t.Errorf("%s now carries %q, so it has become a source for it; attribute the quotation to the file it is in", checklistPath, "the phase that ships the check writes them")
	}
}

// TestVoiceDocRegisterNoteCounts70bAsWritten pins the other document that keeps the
// register's bookkeeping: docs/design/voice.md §5.3 says which of the website redesign's
// checks are written, and it said "69c and 70b are not in the register" — true when it was
// counted, and false on a branch that writes the `| 70b |` row and the `## Check 70b:`
// section. §13 of the mobile design records the same fact for 70a–70c (its own row says
// all three are written), so this is the same recount in the document that was missed.
// The row and the section themselves are pinned by
// TestWebsiteChecklistHasCheck70bRow and TestWebsiteChecklistHasCheck70bSection; what is
// pinned here is that this document's sentence agrees with them — and that it points at
// the count it is about: §5.3's count is above the sentence, and nothing below it carries
// one, so the direction word is pinned too.
func TestVoiceDocRegisterNoteCounts70bAsWritten(t *testing.T) {
	const voicePath = "docs/design/voice.md"
	voice := flat(readDoc(t, voicePath))

	if strings.Contains(voice, "69c and 70b are not in the register") {
		t.Errorf("%s §5.3 says 70b is not in the register, and docs/gcp-live-checklist.md carries a `## Check 70b:` section and a `| 70b |` row: recount the sentence the way §13 of docs/mobile/design/voice-evaluation-switch.md was recounted", voicePath)
	}
	for _, want := range []string{
		"69c is not in the register",
		"70b is written since that count",
		"written since the count above by the website redesign's check-70b work",
	} {
		if !strings.Contains(voice, want) {
			t.Errorf("%s §5.3's register note no longer carries %q: the note has to say which of the website redesign's three checks is written and which is not, or the reader takes the count above it for the register as it stands", voicePath, want)
		}
	}
	// And the direction it points in. The count this sentence is about is §5.3's dated
	// paragraph above it (the eight written since a573870, counted at 07022b1) and the note
	// above the sentence in the same paragraph ("adds 70b to the list above"); nothing below
	// the sentence carries a count, so "the count below" sends the reader to nothing.
	if strings.Contains(voice, "written since the count below") {
		t.Errorf("%s §5.3 says 70b is \"written since the count below\", and the count is above the sentence: §5.3's dated paragraph is where the eight written since a573870 are counted, and the note above this sentence says it adds 70b to \"the list above\"", voicePath)
	}
}

// flat reduces markdown emphasis and line wrapping so a quotation can be
// compared with the file it is attributed to: the sources wrap their sentences
// across lines and emphasise words inside the quoted span, neither of which
// changes the words quoted.
func flat(s string) string {
	s = strings.NewReplacer("**", "", "`", "", "*", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// summaryRows returns the check numbers the checklist's summary table carries
// as rows, read from the table itself (`| N | Check | Command | Expected |`,
// one row per check, ending at the first blank line). They are read rather than
// searched for one string at a time because §13's count is a statement about
// the run of rows: a row that is deleted is exactly what must fail here.
func summaryRows(t *testing.T, path, checklist string) map[string]bool {
	t.Helper()
	rows := map[string]bool{}
	inTable := false
	for _, line := range strings.Split(checklist, "\n") {
		if strings.HasPrefix(line, "| # | Check | Command | Expected |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if strings.HasPrefix(line, "|-") { // the header's separator
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break // the table ends at the first line that is not a row
		}
		n := strings.TrimSpace(strings.Split(strings.TrimPrefix(line, "|"), "|")[0])
		if n == "" {
			continue
		}
		rows[n] = true
	}
	if len(rows) == 0 {
		t.Fatalf("%s's summary table (`| # | Check | Command | Expected |`) was not found, so §13's row run cannot be read from it", path)
	}
	return rows
}
