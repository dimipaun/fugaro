package cli

import (
	"regexp"
	"strings"
	"testing"
)

// Checks 601 to 608 — the mobile-auth work's owner-run live checks — as the
// register writes them.
//
// The work that proposes these checks writes them **by name and takes no number
// for them** (docs/mobile-auth/implementation-plan.md §9): the change that
// allocated 601 to 608 is the one that wrote each check's row and section into
// docs/gcp-live-checklist.md, which is why the pins for those numbers live here
// rather than in that plan's own docs tests.
//
// A check is registered by two things -- a row in the summary table and a
// `## Check N:` section below it -- and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide
// (`TestChecklistChecksAreGuarded`) is deliberately absent, so each work's own
// docs tests carry the assertion for its own numbers; docs_check600_test.go
// carries it for 600, and this file carries it for 601 to 608.
//
// One place departs from Check 600's treatment, and it is deliberate: the eight
// scripts these sections name are **not written yet**. 600d shipped with its
// script, so its test stat()s the file; each of these eight is written by the
// part that ships its check, and the section says so in as many words ("Run it,
// once the script exists"). What is pinned here is therefore the promise and the
// exact path it names -- a section that renamed the script it promises, dropped
// its row or stopped saying which part and which decisions it belongs to fails,
// while a file that does not exist yet is not an error.

// check601Checks is the register's own mapping, number by number: the part of
// docs/mobile-auth/implementation-plan.md §9 the check belongs to, and the
// sentence its section uses to say which decisions it settles. The register's
// claim paragraph above the table, §9's table and the section itself state the
// same facts, so a number that moved between parts, or a section that stopped
// naming what it reads, would leave them disagreeing.
//
// commands says whether the section shows a `fugaro` command at all. Seven of
// the eight do; 607 does not, and must not: it is the app's first screen on a
// device with the network off, so a CLI command in it would be a step the check
// cannot run the way it is written.
var check601Checks = []struct {
	number   string
	parts    []string
	settles  string
	commands bool
}{
	{"601", []string{"**P3**"}, "it settles **D226**", true},
	{"602", []string{"**P2**", "**P3**"}, "it settles **D225** and **D222**", true},
	{"603", []string{"**P3**"}, "it settles **D222** and the property **D166**", true},
	{"604", []string{"**P5**"}, "it settles **D231**", true},
	{"605", []string{"**P5**"}, "it settles **D231** and **D222**", true},
	{"606", []string{"**P4**"}, "it settles **D233** and **D234**", true},
	{"607", []string{"**P4**"}, "it settles **D232** and **D234**", false},
	{"608", []string{"**P6**"}, "it settles **D221**", true},
}

// check601RowRE reads one check's summary row, and the count that says a number
// has one row rather than two.
func check601RowRE(number string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\|\s*` + number + `\s*\|.*$`)
}

// check601Row returns one check's summary row, or "" when the table has none.
func check601Row(register, number string) string {
	return check601RowRE(number).FindString(register)
}

// check601FirstParagraph returns the section's opening paragraph, which is the
// one the register's `**NOT RUN.**` sentence and each check's own part are in: a
// part named later in the section (604 names P3's pairing, 605 names P1's
// resolver) is that other part's business and not this check's own.
func check601FirstParagraph(section string) string {
	rest := section[strings.Index(section, "\n")+1:]
	if i := strings.Index(rest, "\n\n"); i >= 0 {
		rest = rest[:i]
	}
	return flattenDoc(rest)
}

// TestDocsChecks601To608AreRegistered: every one of the eight has both halves of
// its registration, both say it has not been run, and each section names its own
// part, the decisions it settles and the script it is written to become. A row or
// a section that read as a pass would be a live claim nobody made -- every one of
// the eight is the owner's and none has been run -- and a number with only one of
// its two halves is a number a reader cannot use.
func TestDocsChecks601To608AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	for _, c := range check601Checks {
		row := check601Row(register, c.number)
		if row == "" {
			t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, c.number, c.number)
		}
		if !strings.Contains(row, "OWNER-RUN, NOT RUN.") {
			t.Errorf("Check %s's row does not read `OWNER-RUN, NOT RUN.`: the check is the owner's and has not been run, and a row that leaves either half out is read as a result", c.number)
		}
		if n := strings.Count(register, "\n## Check "+c.number+":"); n != 1 {
			t.Errorf("%s carries %d `## Check %s:` sections: a check is one number with one section, and a number written twice is a reader's coin toss", liveChecksDoc, n, c.number)
		}
		if n := len(check601RowRE(c.number).FindAllString(register, -1)); n != 1 {
			t.Errorf("%s carries %d `| %s |` rows: one number has one row in the table", liveChecksDoc, n, c.number)
		}

		section := check600Section(t, register, c.number)
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			// The register's own rule for an owner-run check: no agent runs any
			// step of it, and the text has to say so.
			"no agent performs any step of it",
			"run by you",
			"never `belong`",
			// The promise, and its exact path: the script is the shipping
			// part's, and the section says which path it will be.
			"scripts/live-checks/check" + c.number + ".sh",
			"once the script exists",
			strings.ToLower(c.settles),
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles and how to run it, and a pin that has moved is a section that stopped saying it", c.number, want)
			}
		}
		paragraph := check601FirstParagraph(section)
		if !strings.Contains(paragraph, "owner-run half of that plan's") {
			t.Errorf("Check %s's opening paragraph does not say the check is the owner-run half of one of §9's parts: that paragraph is where the reader is told what they are being asked to run, and which part answers for it", c.number)
		}
		for _, part := range c.parts {
			if !strings.Contains(paragraph, part) {
				t.Errorf("Check %s's opening paragraph does not name its part %s: §9's table and the register's claim paragraph assign this number to that part, and a section that stopped naming it would leave the three disagreeing", c.number, part)
			}
		}
	}

	// 601 is where the allocation is written out (the other seven point at it),
	// so it is the one section that has to carry the plan's own link and section
	// number, and the seven have to point back at it.
	first := flattenDoc(check600Section(t, register, "601"))
	for _, want := range []string{"[mobile-auth/implementation-plan.md](mobile-auth/implementation-plan.md)", "**§9**"} {
		if !strings.Contains(first, want) {
			t.Errorf("Check 601's section does not carry %q: it is the section the other seven point at for the allocation, and the allocation is §9's", want)
		}
	}
	for _, c := range check601Checks[1:] {
		if section := flattenDoc(check600Section(t, register, c.number)); !strings.Contains(section, "Check 601") {
			t.Errorf("Check %s's section does not point at Check 601, where the allocation and the reading it rests on are written: seven sections cannot each restate it", c.number)
		}
	}
}

// TestDocsChecks601To608AreThePlansNumbers: the eight numbers are the plan's, and
// the plan is where the work records which of its checks takes which. A section
// that took a number the plan does not carry -- or a plan whose table lost one of
// the eight -- would leave the register and the plan telling different stories
// about the same work, which is what the register's claim paragraph above the
// table is the record of.
func TestDocsChecks601To608AreThePlansNumbers(t *testing.T) {
	plan := readDoc(t, "docs/mobile-auth/implementation-plan.md")
	for _, c := range check601Checks {
		if !strings.Contains(plan, "| **Check "+c.number+"** — ") {
			t.Errorf("docs/mobile-auth/implementation-plan.md §9's table has no `| **Check %s** — ` row: the register wrote the number, and the plan is where the work says which check it is", c.number)
		}
	}
	if !strings.Contains(flattenDoc(plan), "601 to 608 allocated by the register's own change") {
		t.Errorf("docs/mobile-auth/implementation-plan.md §10 no longer records that 601 to 608 were allocated by the register's own change: the plan claims no number of its own, and that sentence is the record of where the eight came from")
	}

	// The register's claim paragraph is above the table and the sections are
	// below it, so the paragraph is what a reader of the table has already been
	// told: it names every number with its part, and it says the numbers are
	// claimed rather than reserved, which is what makes the later of two
	// colliding claims renumber itself.
	register := readDoc(t, liveChecksDoc)
	claim := strings.Index(register, "**Checks 601 to 608 are the mobile-auth work's")
	table := strings.Index(register, "\n| 601 |")
	if claim < 0 || table < 0 {
		t.Fatalf("%s: the claim paragraph (%d) and the table's first row (%d) are both part of the registration", liveChecksDoc, claim, table)
	}
	if claim > table {
		t.Errorf("Checks 601 to 608's claim paragraph is below their rows: the paragraph is the record of the numbers taken and reads with the register's other numbering paragraphs, above the table")
	}
	paragraph := flattenDoc(register[claim:table])
	for _, want := range []string{"claimed, not reserved", "the number free at this change's merge"} {
		if !strings.Contains(paragraph, want) {
			t.Errorf("Checks 601 to 608's claim paragraph does not say %q: the register's rule is what lets the later of two colliding claims renumber itself, and the paragraph is the record of the numbers taken", want)
		}
	}
	for _, c := range check601Checks {
		if !strings.Contains(paragraph, "**"+c.number+"**") {
			t.Errorf("Checks 601 to 608's claim paragraph does not name %s: the paragraph is the record of all eight numbers taken", c.number)
		}
	}
	// The departure, stated in the paragraph rather than left to a reader: the
	// sections are written before the parts that will run them, against the
	// register's own rule and Check 57's precedent. A paragraph that dropped the
	// sentence would leave eight sections reading as a work that shipped.
	if !strings.Contains(paragraph, "departure from this register's own rule") {
		t.Errorf("Checks 601 to 608's claim paragraph does not state the departure it is: the register's rule is that the part that ships a check writes its section, no part of P1 to P6 has been written, and eight sections written anyway have to say so")
	}
}

// check601FugaroCommands returns the section's `fugaro` invocations. These eight
// sections write their commands inside the prose and inside fenced blocks, where
// Check 50's stand on their own lines -- so the line-leading reader
// (fugaroCommands) reads nothing in them, and a backticked span that begins with
// `fugaro ` is the shape a command has here. A fenced block's own line is read
// too, for the blocks whose command is long enough to be written on its own.
func check601FugaroCommands(section string) []string {
	var out []string
	for _, m := range check601CommandRE.FindAllStringSubmatch(flattenDoc(section), -1) {
		out = append(out, m[1])
	}
	for _, block := range check601FenceRE.FindAllStringSubmatch(section, -1) {
		for _, line := range strings.Split(block[1], "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "fugaro ") {
				out = append(out, line)
			}
		}
	}
	return out
}

// check601CommandRE is the backticked `fugaro …` span: everything up to the
// closing backtick, which is the whole command as the register writes it. The
// section is flattened before this is applied, because a span that wraps at the
// register's line breaks is still one command.
var check601CommandRE = regexp.MustCompile("`(fugaro [^`]*)`")

// check601FenceRE is a fenced block, whose own lines are not wrapped prose.
var check601FenceRE = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")

// TestDocsChecks601To608ShowCommandsThisBinaryHas: every `fugaro` command the
// eight sections show is one this binary has, and every --flag on it belongs to
// that command or one of its ancestors (TestDocsProjectLayerCommandsExist's
// rule, over these sections). The owner runs these by hand, so a flag that does
// not exist is a step that stops at "unknown flag" -- the defect the review of
// this change found in Check 604, which showed `fugaro validate --project
// fugaro`: validate declares no --project (it resolves the project from the
// checkout), and docs_project_centric_test.go pins the same defect for Check 50.
func TestDocsChecks601To608ShowCommandsThisBinaryHas(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	flagRE := regexp.MustCompile(`--([a-z][a-z-]*)`)
	for _, c := range check601Checks {
		section := check600Section(t, register, c.number)
		lines := check601FugaroCommands(section)
		if !c.commands {
			if len(lines) != 0 {
				t.Errorf("Check %s's section shows a `fugaro` command (%q): it is the app's first screen on a device with the network off, and the check is read off the screen rather than run with the CLI", c.number, lines[0])
			}
			continue
		}
		if len(lines) == 0 {
			t.Errorf("Check %s's section shows no `fugaro` command at all: the check is run with the CLI, and a section with none is a section the reader cannot run", c.number)
			continue
		}
		for _, line := range lines {
			cmd := fugaroCommandFor(line)
			if cmd == nil {
				t.Errorf("Check %s's section has a `fugaro` line that is not a command this binary has: %q", c.number, line)
				continue
			}
			for _, m := range flagRE.FindAllStringSubmatch(commandPart(line), -1) {
				if !cmdHasFlag(cmd, m[1]) {
					t.Errorf("Check %s's section's %q shows --%s, which %s does not declare: the owner would meet 'unknown flag' at that step", c.number, commandPart(line), m[1], cmd.CommandPath())
				}
			}
		}
	}
}
