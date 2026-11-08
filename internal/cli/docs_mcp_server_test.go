package cli

// The hosted MCP server's design folder, pinned.
//
// docs/mcp-server/ is a design-only folder: README.md (the index, the reading
// of the number block and what a reader should not take from it), spec.md (the
// owner's spec copied verbatim as §0, then the design written to his answers),
// reconciliation.md (the findings F1-F32, the fifteen decisions D920-D934 and
// the answers of 2026-11-04, and the answer of 2026-11-05, D935) and
// implementation-plan.md (the tasks M1-M10). It
// adds no code and no task in it is approved, so nothing here pins a command, a
// flag or an option.
//
// What it does pin is the half of these documents that rots silently:
//
//  * TestDocsMCPServerIsWritten -- the four files exist, each carries the
//    folder's status line word for word, and the plan states the two gates
//    every task waits behind, with the amendment's own cuts (M9 out of the
//    first slice, M4 and M6 cancelled and to be rerun) among them. The two
//    licence files the folder ships beside them carry the status line too
//    (mcpFolderDocs): the table is pinned as the table it says it is -- not the
//    gate, and claiming no clearance of its own -- and the gate is pinned as the
//    record it says it is, the owner's clearance of 2026-11-07 and no line of
//    counsel's.
//  * TestDocsMCPServerAnswersAreTheOnesInForce -- the answers of 2026-11-04 and
//    the answer of 2026-11-05 are dated in reconciliation.md §4, every one of
//    the fifteen and the sixteenth is the recommendation (no
//    row says otherwise), D922's restatement is recorded rather than the answer
//    it replaced, the "D922/D933 tension" the first answer created is gone
//    from every file, and the one amendment that is NOT the recommendation
//    (H1, accepted and documented after the security review) is recorded as the
//    amendment it is rather than as a decision that was taken.
//  * TestDocsMCPServerNamesWhatItDesigns -- the design's tool is `spawn_task`
//    where §0's verbatim copy still says `submit_task`, the subtree is
//    `mcpauth`, and the launch path is `fugaro run`'s own.
//  * TestDocsMCPServerCitationsResolve -- every `path:line` citation resolves to
//    a file of this tree and a line inside it, measured against an independent
//    census so a regexp that stopped matching fails rather than passing
//    vacuously.
//
// The citation resolver is the cloud senior review's (`csrCitationRE`,
// `csrCensusRE`, `csrResolveCitation`, `csrBareNames`), through the
// config-management folder's own pattern: three docs tests asking one question
// of one tree should not each carry their own, and a second copy is the one that
// drifts.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// mcpPlainCitationRE finds the shape README.md writes its citations in: a path
// and a line with no backticks around them. csrCitationRE cannot see those, and
// the README is where this folder's number block lives -- so without this the
// citation check passes on README vacuously, which is the one failure mode the
// census cannot catch when a whole file is written in a shape the regexp does
// not know.
// The optional backtick after the extension is the README's own house style: it
// closes the code span before the colon, writing the path in code and the line
// beside it, so a pattern that demanded the two be backticked together would
// still see none of the README's citations.
var mcpPlainCitationRE = regexp.MustCompile("[A-Za-z0-9_][A-Za-z0-9_./-]*\\.(?:go|md|json|ts|tf|tmpl|yaml|yml)`?:[0-9]+")

const (
	mcpReadme     = "docs/mcp-server/README.md"
	mcpSpec       = "docs/mcp-server/spec.md"
	mcpReconciled = "docs/mcp-server/reconciliation.md"
	mcpPlan       = "docs/mcp-server/implementation-plan.md"

	// mcpLicence is the dependency licence table the owner reads to clear the
	// `zitadel/oidc` set (his ruling of 2026-11-07). It sits beside the design
	// documents and carries their status line, but it is not one of them: it
	// states no answer of the owner's and no bound of the design, so the pins
	// that ask a document for those stay on the four. It owes the two things
	// the four owe that a table can carry -- the status line, and citations
	// that resolve -- and both are checked over mcpFolderDocs.
	mcpLicence = "docs/mcp-server/licence-table.md"

	// mcpGate is §6 check 1's gate file: the record of the owner's clearance of
	// the set, which he gave on 2026-11-07 by reading the table above. The check
	// asks for an artifact rather than a run's word, so what is pinned here is
	// the artifact itself -- who cleared what, when, by what reading and in whose
	// words -- and the one claim it must never carry, which is counsel's. A
	// record that lost the owner's name or the date would be a clearance nobody
	// could trace, which is the state the check exists to refuse.
	mcpGate = "docs/mcp-server/licence-gate.md"

	// mcpStatus is the line every file in this folder carries, word for word.
	// It is the folder's whole claim about itself -- the design is answered,
	// which of the plan's tasks are built and which are not, and the fact that
	// the folder itself approves nothing -- and a file that dropped it would
	// read as a design somebody may build from, which is the one thing this
	// folder is not. The built half is the five tasks merged on `main` since
	// the answers: a status line that still said "nothing implemented" would be
	// the folder's one load-bearing false claim. M5's clause is "M5 is built"
	// and not "M5 is built in this pull request": the line outlives the pull
	// request, and a phrase that named one would be false on `main` the day it
	// merged. The clause carries its own exception -- the paired device M-a
	// asks for, which M5's approve step does not build: the page renders its
	// number and posts it back from the browser that answered it, which binds
	// the page to the flow and not a device to a person, so a status line
	// saying M5 were built whole would be the claim the review found. That
	// exception is M5b -- the owner's ruling of 2026-11-07 made the
	// paired-device approval a task of its own and a hard prerequisite before
	// any binary links the front door -- so the line names it and the test
	// below holds the plan to it. M7's clause is the same shape: the resource
	// server and the tool registry are built and the tasks after it are not,
	// and naming M7 built says nothing about a merge, for the reason M5's
	// clause does not. Its clause carries an exception of its own -- the
	// reading of the installation's published state, which is where D933's two
	// lists come from: identity.go holds the Roster seam and no task of the
	// plan implements it over that state, so a line saying M7 were built whole
	// would claim a control the tree does not perform. The exception is
	// disclosed in full in §11 of the spec and its own section of the plan,
	// which the disclosure test below pins. M8's clause is the same shape
	// again: the four read tools are built and no binary registers them, and
	// the wiring that would is what M5b gates, so its exception is named the
	// same way and for the same reason -- a line saying M8 were built whole
	// would claim a reach no process has.
	mcpStatus = "Status: design answered by the owner 2026-11-04; M1, M2, M3, M6 and M4's first half are built and merged on `main`; M5 is built except the paired device M-a asks for, which is M5b and is a hard prerequisite before any binary links the front door (the owner's ruling of 2026-11-07); M7 is built except the reading of the installation's published state its lists come from, which no task of this plan carries; M8 is built except the wiring that would serve its four read tools, which is the task M5b gates; M9–M10 are not started; this folder approves no task."
)

func mcpDocs() []string { return []string{mcpReadme, mcpSpec, mcpReconciled, mcpPlan} }

// mcpFolderDocs is every file of the folder that carries the status line and
// whose citations have to resolve: the four documents that state the design, the
// licence table beside them, and the gate file that table is evidence for.
func mcpFolderDocs() []string { return append(mcpDocs(), mcpLicence, mcpGate) }

// TestDocsMCPServerIsWritten: the four files are there, each carries the status
// line, and the plan still says what every task in it waits behind.
func TestDocsMCPServerIsWritten(t *testing.T) {
	for _, path := range mcpFolderDocs() {
		flat := flattenDoc(readDoc(t, path))
		if !strings.Contains(flat, mcpStatus) {
			t.Errorf("%s does not carry the folder's status line %q: every file of an answered design folder says in one line that the design is answered, which tasks of the plan are built and which are not, and that the folder approves no task, and a file that drops it reads as a design somebody may build from",
				path, mcpStatus)
		}
	}

	// The licence table says what it is, and what it is not: the artifact the
	// owner read to clear D921's set is the one a later edit could turn into a
	// clearance nobody gave. Nothing else in the tree pins its two denials, so
	// the reading it exists for and the reading it must never carry are both
	// stated here -- and the second is a refusal as well as a demand, because a
	// line saying the clearance had been given is the shape that reads as the
	// gate's answer without being the gate file. What it says instead is where
	// that answer is: the gate file, pinned below.
	table := flattenDoc(readDoc(t, mcpLicence))
	for _, want := range []string{
		"This file is a table, and it is not a gate.",
		"it claims no clearance of its own",
		"the record of it is [licence-gate.md](licence-gate.md)",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("%s does not state %q: the owner cleared D921's dependency set by reading this table, so a table that claimed the clearance, or that stopped saying it is evidence rather than the gate, would be the merge condition's answer written by the artifact under it",
				mcpLicence, want)
		}
	}
	for _, refuse := range []string{"= the clearance", "the clearance is given", "the gate is met"} {
		if strings.Contains(table, refuse) {
			t.Errorf("%s carries %q: the table is evidence for reconciliation.md §6 check 1 and not the gate, and no line of it may read as the clearance itself -- the owner's clearance is a record of its own, and it is [licence-gate.md](licence-gate.md)",
				mcpLicence, refuse)
		}
	}

	// The gate file is the record §6 check 1 asks for, and it is pinned as a
	// record: the owner by name, the date, the reading the clearance rests on,
	// his words, the table as the evidence, and the refusal that keeps it honest
	// -- counsel was never asked. A file that dropped any of them would still
	// read as a clearance while being one nobody could trace to an act.
	gate := flattenDoc(readDoc(t, mcpGate))
	for _, want := range []string{
		"Dimi Paun",
		"2026-11-07",
		`"Cleared: all permissive."`,
		"The table is the evidence this clearance stands on",
		"Counsel was not asked about the transitive set and has cleared nothing",
		"§6 check 1 is met",
		"M5b is a hard prerequisite before any binary links the front door",
	} {
		if !strings.Contains(gate, want) {
			t.Errorf("%s does not state %q: the owner cleared D921's dependency set himself on 2026-11-07, that clearance is an artifact rather than a run's word, and the file that records it is the only thing this tree has that answers reconciliation.md §6 check 1",
				mcpGate, want)
		}
	}
	for _, refuse := range []string{"counsel cleared", "counsel has cleared", "counsel's clearance of the transitive"} {
		if strings.Contains(gate, refuse) {
			t.Errorf("%s carries %q: counsel was never asked about the transitive set, so a line of the gate file reading as counsel's clearance would record a review that has not happened -- the whole clearance is the owner's",
				mcpGate, refuse)
		}
	}

	plan := flattenDoc(readDoc(t, mcpPlan))
	for _, want := range []string{
		// The two gates the plan states before the first task: the review the
		// coordinator runs -- which has run, so the gate is the finding each
		// task now answers -- and the licence, which no review could settle and
		// which the owner cleared himself on 2026-11-07 by reading the table.
		"independent SECURITY review of [spec.md](spec.md) §8",
		"**run by the coordinator**",
		"The second gate is met",
		"D921's licence",
		"the five whose marker says **mandatory** are M3, M4, M5, M7 and M9",
		// The owner's act itself, and the gate file it produced, so the plan
		// cannot go back to waiting on a check the owner has answered.
		`"Cleared: all permissive"`,
		"the gate file the check describes",
		// And the cuts the amendment made: `spawn_task` out of the first slice,
		// and the two tasks whose drafts predate the answers they now build.
		"M9 leaves the first slice",
		"Cancelled on 2026-11-04 and to be rerun from this amended plan",
		// And the owner's ruling of 2026-11-07: the paired device M-a asks for
		// is M5b, a task of its own, and it is a gate -- M5 merges dormant and
		// the guard is relaxed by building M5b rather than by deleting its
		// message. A plan that dropped either half would leave the front door
		// looking wireable once M5 was in.
		"M5b is a hard prerequisite before anything wires the front door",
		"M5b must land before it may be relaxed",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not state %q: the plan gates every task on the security review and on the licence, it names which tasks are marked mandatory, and it states which tasks the amendment deferred and which it cancelled -- a later edit that dropped any of those would leave a task looking startable",
				mcpPlan, want)
		}
	}
}

// TestDocsMCPServerAnswersAreTheOnesInForce: the answers of 2026-11-04 are in
// reconciliation.md §4, dated, and the folder is written to the ones in force.
//
// This is the test the folder needed and did not have. D922 was answered one way
// and restated the same day, and between the two the design said the opposite of
// what it says now in four files at once -- so what is pinned here is not just
// that the answers are present but that the folder is not carrying the replaced
// one: no row calls any answer "not the recommendation", and the tension the
// replaced answer created is gone.
func TestDocsMCPServerAnswersAreTheOnesInForce(t *testing.T) {
	rec := readDoc(t, mcpReconciled)
	if !strings.Contains(rec, "### The owner's answers, 2026-11-04") {
		t.Fatalf("%s does not carry the answers section: the fifteen decisions were answered on 2026-11-04 and the record of that is the section a reader looks for first", mcpReconciled)
	}

	// Every decision is written at least twice in the reconciliation -- once as
	// the question put to the owner and once as the answer -- because a decision
	// whose answer went missing would leave the question table reading as if it
	// were still open.
	for i := 920; i <= 934; i++ {
		d := fmt.Sprintf("**D%d**", i)
		if n := strings.Count(rec, d); n < 2 {
			t.Errorf("%s names %s %d times and the folder writes each decision twice (the question of §4's first table and its answer in §4's second): the answer's row is missing or the question's is",
				mcpReconciled, d, n)
		}
	}

	// D922's replaced answer is recorded as replaced rather than as the answer,
	// and the one amendment that is not the recommendation -- H1, the paired
	// device, which the owner accepted and asked to have documented instead of
	// fixed -- is recorded as exactly that in every file. A file that dropped it
	// would read as a threat model whose every finding was answered with a
	// change, which is the one thing this folder's review did not do.
	for _, path := range mcpDocs() {
		doc := readDoc(t, path)
		for _, want := range []string{"not the recommendation", "H1", "residual risk"} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s does not state %q: the review's finding H1 was answered by accepting and documenting a residual risk rather than by fixing it, and a file that left either half out would read as if every finding of the review had been answered with a change", path, want)
			}
		}
		if strings.Contains(doc, "D922/D933 tension") {
			t.Errorf("%s still carries the D922/D933 tension text: D922's restatement closed the subtree, so the database rule and D933's tool rule agree and the sentence that said otherwise has to be gone from every file rather than left beside its replacement", path)
		}
		if !strings.Contains(doc, ".read: false") || !strings.Contains(doc, ".write: false") {
			t.Errorf("%s does not state the closed rule: D922's answer in force is `.read: false` and `.write: false`, and a file that states the subtree without them is a file a builder could read as leaving it open", path)
		}
	}

	// The restatement itself is recorded, in the two files that carry the
	// answer, so a reader of either cannot conclude that the first answer stands.
	for _, path := range []string{mcpReconciled, mcpSpec, mcpPlan} {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "restated") {
			t.Errorf("%s does not record that D922's answer was restated the same day: the folder's earlier record was the other answer, and a reader who finds only the current one cannot tell that it moved", path)
		}
	}

	// The answer of 2026-11-05 (D935), from the independent review of the plan's
	// M3, is recorded the way the fifteen before it are: a section of its own in
	// the reconciliation, the decision written twice -- the question of §4's
	// first table and the answer under it -- and the two files that carry the
	// design naming it, because a reader of the plan or the index alone has to
	// find the same answer the reconciliation records.
	if !strings.Contains(rec, "### The answer of 2026-11-05") {
		t.Fatalf("%s does not carry the answer of 2026-11-05: the review of the plan's M3 found that the closed subtree is still reachable by everyone the installation lists (F32), the owner answered it the same day, and that answer is what the plan's M2, M3, M4, M5 and M10 are written to", mcpReconciled)
	}
	if n := strings.Count(rec, "**D935**"); n < 2 {
		t.Errorf("%s names **D935** %d times and the folder writes each decision twice (the question of §4's first table and its answer in §4's second): the answer's row is missing or the question's",
			mcpReconciled, n)
	}
	for _, path := range []string{mcpReadme, mcpPlan} {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "D935") {
			t.Errorf("%s never names D935: the token records leave the project's database, and the index and the plan are what a reader meets first, so an amendment that reached the reconciliation alone would leave both describing a store the records have left", path)
		}
	}

	// And the claim the review overturned is gone rather than left standing
	// beside its replacement: the closed subtree was written as the one place
	// nothing but the coordinator's database grant reaches, which is exactly
	// what F32 disproved.
	for _, path := range mcpDocs() {
		if flat := flattenDoc(readDoc(t, path)); strings.Contains(flat, "the one place in this design where nothing but the coordinator's database admin grant reaches the data") {
			t.Errorf("%s still says the closed subtree is the one place nothing but the coordinator's database grant reaches: it sits in the project's shared database, whose viewer and admin grants are project-level and cover every node in it (F32, D935), so the sentence states the opposite of the answer in force", path)
		}
	}

	// The store's name, and the reach the second review of this work removed.
	// Section 8 and M4 first named the recommended bucket under the managed
	// prefix `fmgd-`, on the claim that no project-level storage binding exists
	// -- and both halves are gone: the managed deployer's conditioned role is
	// bound at project level on that prefix (deployer.tf:379, :142) and every
	// operator can act as the deployer (deployer.tf:501), so a prefixed name put
	// the store inside the reach it exists to leave. What is pinned is the name
	// that replaces it, the sentence that says why, and the reading check 8
	// gains -- so a later edit that quietly restored the prefix fails here.
	plan := flattenDoc(readDoc(t, mcpPlan))
	if !strings.Contains(plan, "fugaro-mcpauth-<gcp-project>") {
		t.Errorf("%s does not name the store's bucket %q: §8 and M4 recommend a bucket of its own and the name is the whole of what keeps the managed deployer's conditioned role off it", mcpPlan, "fugaro-mcpauth-<gcp-project>")
	}
	if !strings.Contains(plan, "`fmgd-` is the managed deployer's fence") {
		t.Errorf("%s does not say why the store's name is outside the managed prefix: the deployer's conditioned storage role is a project-level binding on `fmgd-` (deployer.tf:379) and every operator can act as the deployer (deployer.tf:501), so the reason is the correction rather than a detail", mcpPlan)
	}
	for _, path := range mcpDocs() {
		flat := flattenDoc(readDoc(t, path))
		if strings.Contains(flat, "fmgd-<gcp-project>-mcpauth") {
			t.Errorf("%s still names the store's bucket under the managed prefix `fmgd-<gcp-project>-mcpauth`: the managed deployer's conditioned storage role reaches every bucket carrying that prefix and every operator can act as the deployer, so a store so named is readable and rewritable by both (the second review of this work, 2026-11-05)", path)
		}
		if strings.Contains(flat, "No project-level storage binding exists anywhere") {
			t.Errorf("%s still says no project-level storage binding exists anywhere: the managed deployer's `fugaroDeployerStorage` is one, conditioned on `fmgd-` (deployer.tf:379), and the claim is what made the store's name wrong", path)
		}
	}
	if !strings.Contains(flattenDoc(readDoc(t, mcpReconciled)), "impersonate the deployer as an operator") {
		t.Errorf("%s: check 8 does not carry the operator-impersonating-the-deployer reading: the store's name keeps that identity off it, and only a live project can show the name did what it was chosen to do", mcpReconciled)
	}
}

// TestDocsMCPServerNamesWhatItDesigns: the names the design fixes are the names
// the documents use.
//
// §0 is the owner's spec copied verbatim, so it keeps the tool name he wrote
// there while the design around it uses the one he answered. That is the one
// place the two disagree on purpose, and a reader who met the disagreement
// without the note would think a tool was missing.
func TestDocsMCPServerNamesWhatItDesigns(t *testing.T) {
	spec := readDoc(t, mcpSpec)
	if !strings.Contains(spec, "- **submit_task**: start a task") {
		t.Errorf("%s no longer carries §0 verbatim: the owner's spec is copied word for word, and %q is how it names the tool", mcpSpec, "submit_task")
	}
	if !strings.Contains(spec, "`spawn_task`") {
		t.Errorf("%s never names `spawn_task`: D931's answer of 2026-11-04 gives the tool that name, and the design's own sections have to use it", mcpSpec)
	}

	for _, path := range mcpDocs() {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "mcpauth") {
			t.Errorf("%s does not name `mcpauth`: D922's answer gives the token records' subtree that name, and four documents that each describe the subtree have to call it the same thing", path)
		}
	}
	// README.md is the index and names the tool only in passing; the three
	// documents that state the design are the ones that have to use his name for
	// it.
	for _, path := range []string{mcpSpec, mcpReconciled, mcpPlan} {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "spawn_task") {
			t.Errorf("%s never names `spawn_task`: D931's answer of 2026-11-04 gives the tool that name, and a document that still calls it `submit_task` is written to an answer that was replaced", path)
		}
	}

	// The launch path is `fugaro run`'s own, named by the two things it reuses:
	// the claim that makes a submit idempotent, and the pre-check that carries
	// the kill switch.
	for _, want := range []string{"internal/runstore/launch.go:112", "internal/budget/precheck.go:58"} {
		if flat := flattenDoc(readDoc(t, mcpSpec)); !strings.Contains(flat, want) {
			t.Errorf("%s does not cite %q: D931's single launch path is the design's answer to a front door that could otherwise spend money a person's launch cannot, and the citation is what makes the claim checkable", mcpSpec, want)
		}
	}
}

// TestDocsMCPServerDisclosesWhatTheCodeDoesNotDo: the places where this
// folder's documents state a control or a bound that no code in the tree
// carries say so in their own words.
//
// All were raised by the review of this branch and all are disclosures rather
// than fixes: the store is specified to write HMAC-keyed hashes and a MAC over
// each record, and it hashes with a plain unkeyed sha256 and writes no MAC
// (M2's row, merged on `main` before this branch); the open registration
// endpoint is accepted under a per-source rate limit and a sweep that no task of
// the plan carries; the loser of a rotation race is answered inside a grace
// window, and the endpoint refuses it instead; and the approve step is meant to
// read an identity out of the launcher and operator lists, which M7 reads at the
// MCP endpoint and the approve step still does not -- the reading of the
// published state those lists live in being M7's own exception, disclosed in the
// same section. A document that asserted any
// of them as a fact would be a security
// document promising a control the code does not perform -- so what is pinned is
// the caveat itself, in the file a reader meets first, and the section that
// carries each as a finding.
func TestDocsMCPServerDisclosesWhatTheCodeDoesNotDo(t *testing.T) {
	spec := flattenDoc(readDoc(t, mcpSpec))
	for _, want := range []string{
		// The keyed hash and the MAC, marked as the design's requirement rather
		// than as what the code does, with the line that hashes without a key.
		"The keyed hashes and the MACs the store is specified to write",
		"Both are designed and not built",
		"the store hashes with a plain unkeyed `sha256` (`ee/coordinator/mcpauth/store.go:387`)",
		// And the two bounds, with the owner named as the one who assigns them
		// and the wiring task named as the carrier until he does.
		"Who owns the two bounds the open registration endpoint is accepted under",
		"What is owed is the owner's assignment",
		"the task that mounts this package on a binary carries both bounds and deletes `TestNoBinaryLinksTheFrontDoorYet`",
		// And the one M7 itself leaves unbuilt: the reading of the published
		// state its two lists come from. The seam is in the tree and the
		// reading is not, so the section names both and names its carrier.
		"Where the front door's own two lists are read from",
		"no implementation of that interface over the published state is in the tree and no task of this plan carries one",
		"the wiring task that mounts this package on a binary — the one M5b gates — owes this reading beside the two bounds above",
		// And the two halves of the flow the review of this branch found
		// promised: the grace window a lost rotation race is answered in, and
		// the launcher and operator lists the approve step is supposed to read
		// an identity out of. What is built answers neither -- M7 reads the
		// lists at the MCP endpoint, and the approve step still mints for any
		// verified address -- so the sentence that would read as the control
		// says instead where it is and is not, which is the reading a later
		// edit must keep.
		"answer's second half is not built",
		"no grace window exists in the tree",
		"The last of the bounds this paragraph used to state is built",
		"at one door and not at the other",
		"M5's approve step still mints a code and a family for any verified Firebase address",
		"that token reaches no tool",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s does not state %q: the keyed hash and the MAC, the two bounds the open registration endpoint is accepted under, the reading of the published state the front door's lists come from, the grace window a lost rotation race is answered in and the launcher and operator lists the approve step would read an identity out of are all specified in this folder and built nowhere, so a document that dropped the caveat would read as a control the tree performs", mcpSpec, want)
		}
	}

	rec := flattenDoc(readDoc(t, mcpReconciled))
	for _, want := range []string{
		"### The two bounds no task carries",
		"the store hashes with a plain unkeyed sha256",
		"raised as a finding rather than closed by an edit here",
		// And M7's own exception, stated where §4a says what every built task
		// still owes: the seam is merged and the reading behind it is nobody's.
		"nothing in the tree implements it over that state, because no task of the plan carries the reading",
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s does not state %q: each gap is raised there as a finding for the owner to assign, and a reconciliation that dropped one would leave the caveat in the spec with nothing behind it", mcpReconciled, want)
		}
	}
}

// TestDocsMCPServerCitationsResolve: every file and line these documents cite
// exists. A reconciliation is only worth as much as the code behind it, and a
// citation whose line moved turns a checked finding into an unchecked one with
// no other symptom. The licence table's one citation is read here too: it is
// the line the whole table's claim about the library's surface rests on. The
// gate file is the one file of the folder that cites no line at all -- it stands
// on the table, which is a document rather than a line of code -- so the demand
// that a file cite something is made of the files that stand on the tree.
func TestDocsMCPServerCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range mcpFolderDocs() {
		doc := readDoc(t, path)
		// The census first, and independently: how many path-looking tokens
		// carrying a line the document holds at all. The check below has to see
		// at least that many, so a regexp that stopped matching a shape the
		// prose uses fails here rather than quietly checking less.
		census := len(csrCensusRE.FindAllString(doc, -1))
		matches := csrCitationRE.FindAllStringSubmatch(doc, -1)
		if len(matches) < census {
			t.Fatalf("%s holds %d path-looking tokens carrying a line and the citation check matched %d of them: the document cites a shape csrCitationRE no longer knows, so those citations are unchecked",
				path, census, len(matches))
		}
		for _, m := range matches {
			mcpCheckCitation(t, path, m[1], m[2], bare)
		}
		for _, tok := range mcpPlainCitationRE.FindAllString(doc, -1) {
			cut := strings.LastIndex(tok, ":")
			mcpCheckCitation(t, path, strings.TrimSuffix(tok[:cut], "`"), tok[cut:], bare)
		}
		if len(matches)+len(mcpPlainCitationRE.FindAllString(doc, -1)) == 0 && path != mcpGate {
			t.Errorf("%s cites no file and line at all: every document in this folder stands on the code it was reconciled against, so a file the check finds no citation in is a file the check has gone blind on", path)
		}
		t.Logf("%s: %d backticked citations carrying a line, against a census of %d path-looking tokens with one, and %d written without backticks",
			path, len(matches), census, len(mcpPlainCitationRE.FindAllString(doc, -1)))
	}
}

// mcpCheckCitation resolves one cited file and line to a line of this tree.
func mcpCheckCitation(t *testing.T, doc, cited, lineRef string, bare map[string][]string) {
	t.Helper()
	file, ok := csrResolveCitation(cited, bare)
	if !ok {
		t.Errorf("%s cites %q, which is not a file in this tree: a reconciliation whose citations have moved is a reconciliation nobody can check", doc, cited)
		return
	}
	line, err := strconv.Atoi(strings.TrimPrefix(lineRef, ":"))
	if err != nil {
		t.Errorf("%s cites %q%s, whose line is not a number: %v", doc, cited, lineRef, err)
		return
	}
	body, err := os.ReadFile("../../" + filepath.FromSlash(file))
	if err != nil {
		t.Errorf("%s cites %q, which could not be read, so the line is unchecked: %v", doc, cited, err)
		return
	}
	// The trailing newline every file ends with is not a line of it: counting it
	// makes a citation to the phantom last line resolve and still pass.
	lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
	if line < 1 || line > lines {
		t.Errorf("%s cites %q%s, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there",
			doc, cited, lineRef, file, lines, line)
	}
}
