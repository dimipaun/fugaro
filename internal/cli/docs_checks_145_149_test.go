package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Checks 145, 146 and 149 — the Bitbucket-credentials work's owner-run checks —
// as the register writes them.
//
// The work takes the check block 145–154 (docs/bitbucket-credentials/README.md,
// "Numbers this work holds"). Three of it are registered here: 145, the
// consumer facts that gate the read mint; 146, the coordinator sweep reading
// Bitbucket through the service; and 149, the console showing the stored view on
// the owner's own machine. 147 and 148 are **not registered by it**: S3 — the
// slice that mints run tokens is deferred until a push and pull-request proxy
// exists — took 147's subject, and the owner retired the number as reserved and
// unused (D1960, answered 2026-11-10), while 148's read half came back into S2 as
// an owner-run check beside T2.6 (D1961, the same day). The register must carry
// neither a row nor a section for either; 150–154 are reserved and unused.
//
// What these tests hold, and why the register needs each one: a check is
// registered by two things — a row in the summary table and a `## Check N`
// section below it — and either half alone is a number a reader cannot use. A
// section without its row is a check nothing points at, and a row without its
// section is a promise of steps that are not written. Nothing in this tree
// asserts that pairing register-wide: the backstop that would
// (`TestChecklistChecksAreGuarded`) is deliberately absent
// (docs_compute_backend_test.go asserts its absence), so each work's own docs
// tests carry the assertion for its own numbers. This file carries it for
// 145–149.
//
// The red step is this file run against a register that has the section but not
// the row: the pairing is what fails, and it names the number.

// bitbucketRegisterChecks are the three checks this work registers now, and
// bitbucketUnregisteredChecks the two the register must keep carrying nothing
// for. They are held apart because the failure modes are opposite: a registered
// check missing half its registration, and a number that acquired a registration
// it is not owed — 147 is retired as reserved and unused (D1960) and 148's read
// half is S2's, an owner-run check beside T2.6 (D1961), so neither gets a row and
// a section here.
var (
	bitbucketRegisterChecks     = []string{"145", "146", "149"}
	bitbucketUnregisteredChecks = []string{"147", "148"}
)

// bitbucketCheckRowRE matches a register summary row's first cell: the number
// between two pipes, alone. It is anchored on the closing `|`, so a number cited
// inside another row's prose is not a row of its own.
func bitbucketCheckRowRE(number string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\|\s*` + number + `\s*\|`)
}

// TestDocsChecks145149AreRegistered: each of the three checks has both halves of
// its registration — a row in the summary table and a `## Check N:` section
// below it — and each half says the check has not been run. The three are
// owner-run, so a section that read as a pass would be a live claim nobody made.
func TestDocsChecks145149AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range bitbucketRegisterChecks {
		if !bitbucketCheckRowRE(number).MatchString(register) {
			t.Errorf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, number, number)
		}
		heading := "\n## Check " + number + ":"
		i := strings.Index(register, heading)
		if i < 0 {
			t.Errorf("%s has no `## Check %s:` section: the row above points at steps that have to be there", liveChecksDoc, number)
			continue
		}
		// Bounded by the next `## ` heading, so a check written after this one
		// does not widen what is read here.
		section := register[i+1:]
		if end := strings.Index(section, "\n## "); end >= 0 {
			section = section[:end]
		}
		// Flattened and folded, because the register's prose wraps: a section
		// that says "no agent runs any step of\nit" says the same thing as one
		// that fits it on a line, and a pin that missed it would be a pin on the
		// line breaks rather than on the claim.
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			// The register's own rule for an owner-run check: no agent runs any
			// step of it, and the text has to say so.
			"no agent runs any step of it",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", number, want)
			}
		}
		// Check 145's U11 line once made the minimum-scope rule (G7) rest on
		// Check 147, and D1960 retired that number on 2026-11-10: a line that
		// still hands G7's enforcement to it contradicts the plan's own G7 row,
		// which reads the question as moot, and the register is where a reader
		// meets the question first.
		if number == "145" {
			if strings.Contains(flat, "rests on check 147") {
				t.Errorf("Check 145's U11 line still makes the minimum-scope rule rest on Check 147: D1960 retired the number as reserved and unused, and the plan's G7 row reads the question as moot")
			}
			if !strings.Contains(flat, "d1960") {
				t.Errorf("Check 145's U11 line does not say the number it once named was retired (D1960)")
			}
		}
	}

	// The rows and the sections run in number order, so a reader who follows the
	// table top to bottom meets the sections in the same order.
	at := 0
	for _, number := range bitbucketRegisterChecks {
		i := strings.Index(register[at:], "\n## Check "+number+":")
		if i < 0 {
			t.Errorf("%s writes Check %s's section out of order: the three are one block and read in the order the numbers give", liveChecksDoc, number)
			continue
		}
		at += i
	}
}

// TestDocsChecks147And148StayUnregistered: the register carries neither a row
// nor a section for 147 and 148 — 147 is retired as reserved and unused (D1960)
// and 148's read half is S2's owner-run check beside T2.6 (D1961), and neither is
// registered by T0.2, which wrote 145, 146 and 149. A number that acquired a row
// is a check a reader would try to run, and the register's own rule is that a
// number no task writes a script for is never given a subject.
func TestDocsChecks147And148StayUnregistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range bitbucketUnregisteredChecks {
		if bitbucketCheckRowRE(number).MatchString(register) {
			t.Errorf("%s's register has a `| %s |` row: Check %s is not registered by this work, so it has no row", liveChecksDoc, number, number)
		}
		if strings.Contains(register, "\n## Check "+number+":") {
			t.Errorf("%s has a `## Check %s:` section: Check %s is not registered by this work, so it has no section", liveChecksDoc, number, number)
		}
	}

	// The block's own statement of itself is read from the plan that claims it:
	// 145–149 are the block, and it says of each of its two unregistered numbers
	// what that number now is — 147 retired (D1960, answered 2026-11-10) and 148
	// S2's read half only (D1961, the same day) — while 150–154 are reserved.
	plan := flattenDoc(readDoc(t, "docs/bitbucket-credentials/implementation-plan.md"))
	for _, want := range []string{
		"Checks 145–154",
		"147, **retired (D1960)**",
		"148, **S2's, read half only (D1961)**",
		"Checks 150–154 are reserved and unused.",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("docs/bitbucket-credentials/implementation-plan.md no longer says %q: the register's rows are this work's block, and the block is what says what each of its numbers is", want)
		}
	}
	// The count of rows and sections the register carries for the block is the
	// three registered checks and nothing else: a fourth number appearing under
	// this work's block is a check nobody allocated.
	rows := 0
	for _, number := range []string{"145", "146", "147", "148", "149"} {
		if bitbucketCheckRowRE(number).MatchString(register) {
			rows++
		}
	}
	if want := len(bitbucketRegisterChecks); rows != want {
		t.Errorf("%s carries %d of the block's rows (145–149), want %d: %s are registered and %s get no row", liveChecksDoc, rows, want, strings.Join(bitbucketRegisterChecks, ", "), strings.Join(bitbucketUnregisteredChecks, ", "))
	}
}

// bitbucketCheckNumberRE matches a `## Check N:` heading anywhere in the
// register, which is what the block's own numbering is read from.
var bitbucketCheckNumberRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)`)

// TestDocsChecks145149ComeAfterTheHighestWrittenSection: the three sections are
// appended where the register's written sections end rather than inserted among
// older ones, and their numbers are above every other written section's. A block
// is registered by appending to it, and a section written in the middle is how a
// reader loses track of which numbers are live.
func TestDocsChecks145149ComeAfterTheHighestWrittenSection(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	at := strings.Index(register, "\n## Check "+bitbucketRegisterChecks[0]+":")
	if at < 0 {
		t.Fatalf("%s has no `## Check %s:` section to place", liveChecksDoc, bitbucketRegisterChecks[0])
	}
	before := register[:at]
	var highest int
	for _, m := range bitbucketCheckNumberRE.FindAllStringSubmatch(before, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: heading %q does not carry a number", liveChecksDoc, m[0])
		}
		if n > highest {
			highest = n
		}
	}
	first, err := strconv.Atoi(bitbucketRegisterChecks[0])
	if err != nil {
		t.Fatalf("bitbucketRegisterChecks holds %q, which is not a number", bitbucketRegisterChecks[0])
	}
	if highest >= first {
		t.Errorf("%s writes Check %s after `## Check %d`: the three are the highest numbers the register has written, and a block written in the middle of the older sections is a reader's trap", liveChecksDoc, bitbucketRegisterChecks[0], highest)
	}
}
