package cli

import (
	"regexp"
	"strings"
	"testing"
)

// Checks 135, 136 and 139 — the task-governance work's owner-run checks — as
// the register writes them.
//
// The work takes the check block 135–144 (docs/task-governance/implementation-plan.md
// §2, "Check numbers this plan takes"). Three of it are registered here, all by
// task T3.5: 135, a per-repository job account's read of its own run record and
// its refusal of a sibling's; 136, the report run's own pull request, which is
// run only after T3.3's finalize guard merges; and 139, what an agent ID
// distinguishes in a gateway run's log, which gates T7.2. 137 and 138 are task
// T7.5's and are not written yet, and 140–144 stay reserved, so the register
// must carry neither a row nor a section for any of them.
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
// 135–139.
//
// The red step is this file run against a register that has a section but not
// its row: the pairing is what fails, and it names the number.

// taskGovernanceRegisterChecks are the three checks this work registers now, and
// taskGovernanceReservedChecks the seven that stay unwritten. They are held
// apart because the failure modes are opposite: a registered check missing half
// its registration, and a reserved check that acquired one.
var (
	taskGovernanceRegisterChecks = []string{"135", "136", "139"}
	taskGovernanceReservedChecks = []string{"137", "138", "140", "141", "142", "143", "144"}
)

// taskGovernanceCheckRowRE matches a register summary row's first cell: the
// number between two pipes, alone. It is anchored on the closing `|`, so a
// number cited inside another row's prose is not a row of its own.
func taskGovernanceCheckRowRE(number string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\|\s*` + number + `\s*\|`)
}

// taskGovernanceSection returns Check N's section, from its heading to the next
// `## ` heading. A section that is not there is a test failure, so callers read
// the returned string only after it.
func taskGovernanceSection(t *testing.T, register, number string) string {
	t.Helper()
	heading := "\n## Check " + number + ":"
	i := strings.Index(register, heading)
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above points at steps that have to be there", liveChecksDoc, number)
	}
	// Bounded by the next `## ` heading, so a check written after this one does
	// not widen what is read here.
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsTaskGovernanceChecksAreRegistered: each of the three checks has both
// halves of its registration — a row in the summary table and a `## Check N:`
// section below it — and each half says the check has not been run. The three
// are owner-run, so a section that read as a pass would be a live claim nobody
// made.
func TestDocsTaskGovernanceChecksAreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range taskGovernanceRegisterChecks {
		if !taskGovernanceCheckRowRE(number).MatchString(register) {
			t.Errorf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, number, number)
		}
		// Flattened and folded, because the register's prose wraps: a section
		// that says "no agent runs any step of\nit" says the same thing as one
		// that fits it on a line, and a pin that missed it would be a pin on the
		// line breaks rather than on the claim.
		flat := strings.ToLower(flattenDoc(taskGovernanceSection(t, register, number)))
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
	}

	// The rows and the sections run in number order, so a reader who follows the
	// table top to bottom meets the sections in the same order.
	at := 0
	for _, number := range taskGovernanceRegisterChecks {
		i := strings.Index(register[at:], "\n## Check "+number+":")
		if i < 0 {
			t.Errorf("%s writes Check %s's section out of order: the three are one block and read in the order the numbers give", liveChecksDoc, number)
			continue
		}
		at += i
	}
}

// TestDocsTaskGovernanceReservedChecksStayReserved: 137 and 138 are task
// T7.5's and 140–144 stay reserved, so the register carries neither a row nor a
// section for them. A number that acquired a row while its task is unwritten is
// a check a reader would try to run, and the register's own rule is that a
// reserved number is never given a subject until the work that holds it starts.
func TestDocsTaskGovernanceReservedChecksStayReserved(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range taskGovernanceReservedChecks {
		if taskGovernanceCheckRowRE(number).MatchString(register) {
			t.Errorf("%s's register has a `| %s |` row: Check %s is unwritten and stays reserved, so it has no row until the task that writes it starts", liveChecksDoc, number, number)
		}
		if strings.Contains(register, "\n## Check "+number+":") {
			t.Errorf("%s has a `## Check %s:` section: Check %s is unwritten and stays reserved, so it has no section until the task that writes it starts", liveChecksDoc, number, number)
		}
	}

	// The block is read from the plan that claims it: 135–144 are the plan's
	// numbers, 137 and 138 are T7.5's, and 140–144 are reserved and unused.
	plan := flattenDoc(readDoc(t, "docs/task-governance/implementation-plan.md"))
	for _, want := range []string{
		"Checks 135–144",
		"**140–144** stay reserved and unused.",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("docs/task-governance/implementation-plan.md no longer says %q: the register's rows are this work's block, and the block is what says which numbers are reserved", want)
		}
	}
	// The count of rows the register carries for the block is the three
	// registered checks and nothing else: a fourth number appearing under this
	// work's block is a check nobody allocated.
	rows := 0
	for _, number := range []string{"135", "136", "137", "138", "139"} {
		if taskGovernanceCheckRowRE(number).MatchString(register) {
			rows++
		}
	}
	if want := len(taskGovernanceRegisterChecks); rows != want {
		t.Errorf("%s carries %d of the block's rows (135–139), want %d: %s are registered and %s stay reserved", liveChecksDoc, rows, want, strings.Join(taskGovernanceRegisterChecks, ", "), strings.Join(taskGovernanceReservedChecks, ", "))
	}
}

// taskGovernanceOtherInstallationRE matches the installations this work's
// checks must never point at: the owner's own (`belong`) and the two others the
// register names when it says which installation a check is not for. The word
// boundary is what keeps "belongs to the stage's own session" — Check 139's own
// prose — out of the reading.
var taskGovernanceOtherInstallationRE = regexp.MustCompile(`\bbelong\b|EdgeWeb|EdgeServer`)

// TestDocsTaskGovernanceChecksNameTheSandboxOnly: the three checks are the
// sandbox installation's, so each section says so — the register spells that
// installation `fugaro` (project `fugaro-dev`, the sandbox repository) or, where
// a name would be a placeholder, just "sandbox" — and every line that names
// another installation is a refusal. A step that pointed at another
// installation would read that installation's bucket or log, and the answer
// would be about a deployment this check does not own. Checks 135 and 136 read
// the bucket and the repository out of the checkout rather than writing either
// name down, which is why the pin is on "sandbox" and on the refusal rather than
// on `fugaro-dev`: a section that hard-coded a project would be the one that
// could be pointed anywhere.
func TestDocsTaskGovernanceChecksNameTheSandboxOnly(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range taskGovernanceRegisterChecks {
		section := taskGovernanceSection(t, register, number)
		for _, want := range []string{"sandbox", "never `belong`"} {
			if !strings.Contains(section, want) {
				t.Errorf("Check %s's section never says %q: the check is the sandbox installation's, and a section that does not say so reads as one that could be pointed anywhere", number, want)
			}
		}
		for _, line := range strings.Split(section, "\n") {
			if taskGovernanceOtherInstallationRE.MatchString(line) && !strings.Contains(strings.ToLower(line), "never") {
				t.Errorf("Check %s names another installation outside a refusal: %q. The check is the sandbox installation's, so the only mention another installation may have is the one that says never to touch it", number, strings.TrimSpace(line))
			}
		}
		// No secret-shaped literal and no key-length blob: the sections are
		// commands the owner runs, and a credential written into one would be a
		// credential in the repository. The patterns are the ones the mobile
		// runbook's own page test holds, reused rather than re-spelled.
		for _, shape := range runbookSecretShapes {
			if m := shape.re.FindString(section); m != "" {
				t.Errorf("Check %s's section holds %s: %q. A register section carries placeholders, never a credential", number, shape.what, m)
			}
		}
		if m := runbookBlobish.FindString(section); m != "" {
			t.Errorf("Check %s's section holds a key-length blob: %q. A register section carries placeholders, never a credential", number, m)
		}
	}
}
