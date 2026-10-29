package cli

import (
	"regexp"
	"strings"
	"testing"
)

// Check 123 — the console dependency-graph work's owner-run check — as the
// register writes it.
//
// The work takes the check block 123 alone (`docs/console-dependency-graph/README.md`,
// "Numbers this work holds": "Check **123** (the highest in use on `main` and in
// open pull requests was 122)"), and the plan writes it as task **GV2.6**
// (`docs/console-dependency-graph/implementation-plan.md` §6, "Checks").
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. A section without its row is a check nothing points at, and a row
// without its section is a promise of steps that are not written. Nothing in
// this tree asserts that pairing register-wide: the backstop that would
// (`TestChecklistChecksAreGuarded`) is deliberately absent, so each work's own
// docs tests carry the assertion for its own numbers. This file carries it for
// 123, as `docs_checks_145_149_test.go` does for 145–149 and
// `docs_batch_live_check_test.go` for 124.
//
// The red step is this file run against a register that has neither half: the
// row is what fails first, and it names the number.

// check123Heading is the section marker. It is the whole heading prefix rather
// than the number alone, so a sentence elsewhere in the register that cites 123
// in passing is not read as the section.
const check123Heading = "## Check 123:"

// check123RowRE matches the register summary row's first cell: the number
// between two pipes, alone. It is anchored on the closing `|`, so a number cited
// inside another row's prose is not a row of its own.
var check123RowRE = regexp.MustCompile(`(?m)^\|\s*123\s*\|`)

// check123BelongProhibitionRE is what a prose line mentioning `belong` has to
// say to be permitted: the prohibition itself. A line that mentions it any other
// way — a step, a path, a comment — is not a prohibition, and the test refuses
// it rather than reading the sentence for intent.
var check123BelongProhibitionRE = regexp.MustCompile(`(?i)never|no step|no agent|does not|do not|is not|forbidden|refuse`)

// TestDocsCheck123IsRegistered: Check 123 has both halves of its registration —
// a row in the summary table and a `## Check 123:` section below it — the
// section says what it measures and what the owner types, and it never names
// `belong` as a target.
//
// The check is owner-run and has not been run, so a section that read as a pass
// would be a live claim nobody made; and it is a check about a **local** build
// of the console, so naming `belong` anywhere but in a prohibition would be a
// step against the wrong installation.
func TestDocsCheck123IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	if !check123RowRE.MatchString(register) {
		t.Errorf("%s's register has no `| 123 |` row: Check 123 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}

	// Bounded by the next `## ` heading (registerSection), so a check written
	// after this one does not widen what is read here.
	section := registerSection(t, register, check123Heading)

	// Flattened and folded, because the register's prose wraps: a pin on the
	// unwrapped spelling would be a pin on the line breaks rather than on the
	// claim.
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		"**not run.**",
		// The register's own rule for an owner-run check: no agent runs any
		// step of it, and the text has to say so.
		"no agent runs any step of it",
		// What the owner types, and where the app they open comes from: a
		// section that named neither would be steps nobody could follow.
		"npm run package",
		"release/mac-universal/fugaro console.app",
		// The setting the second half of the check turns on.
		"reduce motion",
		// The one cloud read the check makes: the app spawns the installation's
		// own `fugaro plan status --json` and the graph's state words are that
		// read's. A section claiming "no cloud project" would be wrong about the
		// check it describes, so the read is pinned rather than left to prose.
		"plan status --json",
		"runs bucket",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 123's section does not say %q: the check is owner-run, has not been run, and is about the packed app's graph under the owner's own accessibility setting", want)
		}
	}

	// `belong` is never a target: not in a command, not in a path, not in a
	// message a command prints. Every line in a code block is one of those, so
	// the rule there is absolute; in prose the only permitted mention is a
	// sentence saying no step touches it.
	inFence := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !strings.Contains(line, "belong") {
			continue
		}
		if inFence {
			t.Errorf("Check 123 mentions `belong` inside a code block: %q — every line in a block is a command, a variable or a message a command prints, and no part of this check may name that installation as a target", strings.TrimSpace(line))
			continue
		}
		if !check123BelongProhibitionRE.MatchString(line) {
			t.Errorf("Check 123 mentions `belong` outside a prohibition: %q — the only permitted mention is a sentence saying no step touches it", strings.TrimSpace(line))
		}
	}

	// And the prohibition is actually stated: a check that never mentions the
	// name at all is a check whose silence a reader cannot tell from an
	// oversight.
	if !strings.Contains(flat, "never `belong`") {
		t.Errorf("Check 123 does not state the prohibition (\"never `belong`\"): the register's owner-run checks name it, and this one runs a build on an owner's own machine")
	}
}
