package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsMobilePushDesignIsWritten (mobile plan Task P5.2,
// SECURITY-SENSITIVE, UNSCHEDULED):
// the design for push notifications to the phone exists, says plainly that it
// is design only and that nothing follows it until the owner approves a task,
// covers what the brief asked for — what is sent, the channel, where the push
// tokens live, what sends them, rate limiting, the user controls, the threat
// model, the failure modes, the cost, the privacy statement and the
// alternatives — names the live check by number and leaves it unwritten, and
// keeps its questions as Q1–Q9 for the owner rather than allocating a decision
// number. Its red state is this test: the document did not exist.
func TestDocsMobilePushDesignIsWritten(t *testing.T) {
	const path = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		// The status, the task it is, and where it sits in the plan.
		"design only; nothing is implemented",
		"Task P5.2",
		"later, unscheduled",
		// What is sent, and the two things it must never carry or do.
		"opaque",
		"never carry or trigger a confirmation",
		"only delivery path",
		// The channel, and the alternative it prices and does not take.
		"FCM",
		"APNs",
		"Expo",
		// Where the push tokens live, who may read them, and the generation.
		"config/admin/devices/$uid",
		"rules generation 3",
		"RulesVersion",
		// Which component sends, and the constraint the brief puts on it.
		"ambient",
		"operator's machine",
		// Rate limiting and the user controls.
		"storm cap",
		"quiet hours",
		// The cost and the threat model.
		"no per-message charge",
		"lock screen",
		// The spoof's real power, stated rather than softened: a forged push is
		// written by the attacker, so the sender's content-free payload is not a
		// property of the channel and the lock screen is not an authenticated
		// surface. What is bounded is the ACTION, not the lie.
		"attacker-authored",
		"not an authenticated surface",
		"only a doorbell",
		"data-only",
		// D165's two tokens kept apart in the one sentence whose job is to keep
		// them apart: the one-shot pairing token the QR carries, and the durable
		// device token minted only after the operator's code matches. Neither is
		// a push token, and "device token" is a name two different things use.
		"p~<random>",
		"m~<random>",
		// The check it names and deliberately does not write.
		"Check 88",
		"does not write it",
		// The questions, and the gate.
		"Open for the owner",
		"Q9",
		"no service, no token store, no rules change, no app code",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsMobilePushCitationsExist: the design's load-bearing citations are
// paths a later implementer has to find — the rules template and the macro
// file the device node lands in, the version file the generation is read from,
// the job that would send, the schema and the presence rule the mailbox is
// built on, and the page the privacy statement is checked against. A substring
// assertion cannot see one of them move, so each is stat'ed here as well as
// required in the text.
//
// Every path below was read on `origin/main` at 82ba600 and is in this branch's
// tree. The document links its neighbours relatively (../reconciliation.md,
// ../../../SECURITY.md), so a citation counts in either form.
func TestDocsMobilePushCitationsExist(t *testing.T) {
	const path = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, path)
	for _, cited := range []string{
		// The channel's neighbours: what the site already tells a provider, and
		// the console decision that asked the same question and answered "no FCM".
		"website/src/pages/privacy.astro",
		"docs/design/desktop-console-v2.md",
		"docs/design/voice.md",
		// The mailbox, its schema and the presence rule the design leans on.
		"shared/mailbox/index.js",
		"shared/mailbox-schema/index.js",
		"shared/presence/index.js",
		"docs/design/m9-budget-and-dashboard.md",
		"docs/design/budget-token-channel.md",
		// Where the device node and its generation land.
		"internal/budget/rules/rules.json.tmpl",
		"internal/budget/rules/rules.go",
		"internal/projectpolicy/version.go",
		"docs/design/managed-projects.md",
		"docs/design/project-centric.md",
		// The only scheduled cloud components: the job a sender would extend.
		"internal/cli/history.go",
		"deploy/terraform/gcp/modules/installation/history.tf",
		"deploy/terraform/gcp/modules/firebase/iam.tf",
		"internal/gateway",
		"internal/runner/gateway.go",
		// The app and what it would register a token with.
		"mobile/package.json",
		"mobile/app.json",
		"mobile/src/accounts/account.js",
		"mobile/src/accounts/secure-store.js",
		"mobile/src/accounts/secret.js",
		"mobile/src/skeleton/index.test.js",
		// The names the design says are not push tokens, and the enumerable
		// run id the opaque identifier must not be.
		"internal/runner/provision.go",
		"internal/task/task.go",
		// The documents the design is read against, and the test-beside-it rule.
		"SECURITY.md",
		"firebase.json",
		"docs/gcp-live-checklist.md",
		"internal/cli/docs_mobile_push_test.go",
	} {
		rel, err := filepath.Rel("docs/mobile/design", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, cited) && !strings.Contains(doc, rel) {
			t.Errorf("%s no longer cites %s: this list is the document's own citations, so one that has left the text must leave this test", path, cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", path, cited, err)
		}
	}

	// Check 88's register state, read from the register itself. The number is
	// NOT chosen here: the mobile plan allocates 80-89 to this work in advance
	// ("a reserved number is never reused and never renumbered") and assigns 88
	// to P5.2's behaviour, and the plan allocates the *writing* to P4.2. So a
	// `## Check 88` section or a `| 88 |` row appearing does not mean a new
	// number must be found — it means P4.2 has written the check into the
	// register, and the design's §8 sentence that the register is free of it
	// must be updated to say so (and to record the run, which is the owner's).
	checklist := readDoc(t, "docs/gcp-live-checklist.md")
	for _, written := range []string{"## Check 88", "| 88 |"} {
		if strings.Contains(checklist, written) {
			t.Errorf("docs/gcp-live-checklist.md now carries %q: P4.2 has written Check 88 into the register, so %s's §8 must say the check is written (the number is allocated and is never renumbered) and record the owner's run", written, path)
		}
	}
}

// TestDocsMobilePushDoesNotInventAMailboxDisclosureInSecurityMD: §9 argues,
// correctly and from a grep, that SECURITY.md says nothing about the phone, the
// mailbox or notifications — 0 occurrences of "mailbox" in that file, which this
// test re-checks rather than trusting. §9 and Q8 must therefore not turn round
// three lines later and claim the trust model already carries the mailbox's
// disclosures, or point a future writer at "the mailbox residual it already
// carries": that is a contradiction with both the file and the paragraph above
// it, and it sends the writer to a paragraph that does not exist. The red state
// is exactly those two sentences.
func TestDocsMobilePushDoesNotInventAMailboxDisclosureInSecurityMD(t *testing.T) {
	const path = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, path)

	// The fact §9 relies on, checked here rather than asserted in the design's
	// own prose: no occurrence of "mailbox" (any case) in SECURITY.md.
	security := readDoc(t, "SECURITY.md")
	if strings.Contains(strings.ToLower(security), "mailbox") {
		t.Errorf("SECURITY.md now carries \"mailbox\": the design's §9 sentence that the trust model says nothing about the mailbox is now false and must be corrected rather than left standing")
	}

	// Fragments, not whole sentences: the document wraps its prose, so a
	// whole-sentence needle can hide behind a newline as well as a wording
	// change, and both would leave this guard green.
	for _, invented := range []string{
		"residual it already carries",
		"the mailbox's disclosures",
	} {
		if strings.Contains(doc, invented) {
			t.Errorf("%s claims %q, but SECURITY.md has no occurrence of \"mailbox\": §9 states that absence itself three lines earlier, so the claim contradicts the file and the paragraph it sits in", path, invented)
		}
	}
	if !strings.Contains(doc, "carries no mailbox disclosure") {
		t.Errorf("%s lines neither a true claim nor a corrected one: §9/Q8 must say plainly that SECURITY.md carries no mailbox disclosure to sit the statement beside", path)
	}
}

// TestDocsMobilePushIsIndexedAndPlanned: the design is reachable from the
// mobile index and its plan entry says what a later task would touch, that the
// check is written by that task rather than by this design, and that the tier
// is SECURITY-SENSITIVE. A design that is written but not indexed, or a plan
// entry left as "Files. None yet", is the failure this test is red on.
func TestDocsMobilePushIsIndexedAndPlanned(t *testing.T) {
	const design = "design/push-notifications.md"

	index := readDoc(t, "docs/mobile/README.md")
	if !strings.Contains(index, design) {
		t.Errorf("docs/mobile/README.md does not index %s", design)
	}
	// The row's one-line summary of the threat model must not repeat the
	// conflation this document's §5 attacker 2 corrects: a spoofed push is
	// ATTACKER-authored, so it can carry any title it likes on the lock screen,
	// and "harmless because it carries nothing" is exactly the sentence that
	// mistake produces. What the spoof cannot do is make the app act.
	if strings.Contains(index, "harmless because it carries nothing") {
		t.Errorf("docs/mobile/README.md's %s row still says %q, which claims the spoof's content-free payload is a property of the channel rather than of this design's sender", design, "harmless because it carries nothing")
	}
	if !strings.Contains(index, "attacker-authored") {
		t.Errorf("docs/mobile/README.md's %s row does not say a spoofed push is attacker-authored", design)
	}

	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	heading := "### Task P5.2: Push"
	start := strings.Index(plan, heading)
	if start < 0 {
		t.Fatalf("docs/mobile/implementation-plan.md has no %q entry", heading)
	}
	// The task's own block: from its heading to the next `### Task`.
	rest := plan[start+len(heading):]
	end := strings.Index(rest, "\n### Task ")
	if end < 0 {
		t.Fatalf("%s's entry runs to the end of the plan: no following task", heading)
	}
	entry := rest[:end]
	for _, want := range []string{
		"UNSCHEDULED",
		"design/push-notifications.md",
		"Check 88",
		"SECURITY-SENSITIVE",
	} {
		if !strings.Contains(heading+entry, want) {
			t.Errorf("%s's P5.2 entry lacks %q", "docs/mobile/implementation-plan.md", want)
		}
	}
	// The entry this design's pull request edits must name its files rather
	// than the placeholder a task with no design leaves behind: "Files. None
	// yet" is the red state, and the four strings above can all survive it.
	if strings.Contains(entry, "**Files.** None yet") {
		t.Errorf("docs/mobile/implementation-plan.md's P5.2 entry still says %q, so the design's files are not named", "**Files.** None yet")
	}
}
