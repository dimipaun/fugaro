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
// keeps its questions as Q1–Q10 for the owner rather than allocating a decision
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
		// Where the push data lives and the rule that protects revocation: a
		// SIBLING of the device node, so no routine push write can recreate a
		// revoked device node and un-revoke it. `config/admin/devices/$uid` must
		// remain, because it is the node the sibling hangs beside and the node
		// the design forbids every writer but pair/pair revoke to touch.
		"config/admin/devices/$uid",
		"config/admin/push/$uid",
		"ever touches `devices/**`",
		"rules generation 3",
		"RulesVersion",
		// The sender's authority, named exactly rather than described: the
		// permission, the role it must NOT be, and the reason the sender may
		// take nothing from args or env.
		"firebasecloudmessaging.messages.create",
		"roles/firebasecloudmessaging.admin",
		"runWithOverrides",
		// The category set reconciled with the decision the owner actually made,
		// and the read grant the excluded categories would need.
		"D160",
		"the phone cannot see them",
		"tension (b1)",
		// The quiet-hours datum, corrected: a zone name, not an offset.
		"IANA",
		// The provider claims the review could not verify, marked rather than
		// asserted.
		"UNVERIFIED",
		// The two open questions this round added: retention (D121's own words)
		// and the app-composed text's content rule.
		"Q10",
		"retention question",
		// The console precedent the local-text rule borrows.
		"D33",
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
	// The Files line must not describe a producer-written queue: §3.6(B) rejects
	// it ("No producer writes anything"), so a plan that still promises "a new
	// sender in internal/cli (draining a pending-push node)" sends a later
	// implementer to build the shape the design ruled out. The four strings
	// above all survive this sentence, so it needs its own guard.
	if strings.Contains(entry, "draining a pending-push node") {
		t.Errorf("docs/mobile/implementation-plan.md's P5.2 entry still says %q, which is the producer-writes-a-queue shape §3.6(B) rejects", "draining a pending-push node")
	}

	// Every relative markdown link in the entry must resolve. The entry linked
	// `../design/push-notifications.md`, which lands on docs/design/ — a file
	// that does not exist — and the substring check above cannot see it, because
	// "design/push-notifications.md" is present either way. So the link TARGET is
	// resolved on disk, from the plan's own directory.
	for _, target := range markdownLinkTargets(entry) {
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "#") {
			continue
		}
		if _, err := os.Stat(filepath.Join("../../docs/mobile", target)); err != nil {
			t.Errorf("docs/mobile/implementation-plan.md's P5.2 entry links %q, which does not resolve from docs/mobile/: %v", target, err)
		}
	}
	if len(markdownLinkTargets(entry)) == 0 {
		t.Errorf("docs/mobile/implementation-plan.md's P5.2 entry carries no markdown link at all, so the design is not reachable from the plan")
	}
}

// markdownLinkTargets returns the target of every `[text](target)` link in s,
// in order. It is deliberately shallow — the plan's own links are simple
// relative paths — and it ignores an image, which this document does not use.
func markdownLinkTargets(s string) []string {
	var out []string
	for i := 0; ; {
		open := strings.Index(s[i:], "](")
		if open < 0 {
			return out
		}
		open += i
		close := strings.Index(s[open:], ")")
		if close < 0 {
			return out
		}
		out = append(out, s[open+2:open+close])
		i = open + close + 1
	}
}

// TestDocsMobilePushStatesOneRecommendationEverywhere: a fix round that reverses
// the channel recommendation (§3.3/Q7: FCM direct only where the owner owns every
// installation's project, otherwise a relay) leaves the OLD recommendation in the
// places that describe the design from outside — the README's index row and §9's
// privacy paragraph — unless something checks them. Both said "one settled
// channel" in a form a substring needle for the new text cannot catch, because
// the new text is in §3.3 and they are elsewhere. So the stale shapes are named
// here as what must NOT appear, and the replacement is required.
//
// The same class of staleness is the category set: §3.1 pointed at "one of the
// four in §1" after §1's B2 fix had excluded two of them, so the cross-reference
// named the superseded list.
func TestDocsMobilePushStatesOneRecommendationEverywhere(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)

	for _, stale := range []string{
		// The withdrawn unconditional recommendation, in the two places that
		// describe the design rather than argue it.
		"with Expo's relay priced and not recommended",
		"on the recommended channel",
		// The withdrawn cross-reference to the pre-B2 category list.
		"one of the four in §1",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("%s still says %q, which a later fix round withdrew", docPath, stale)
		}
	}
	index := readDoc(t, "docs/mobile/README.md")
	if strings.Contains(index, "with Expo's relay priced and not recommended") {
		t.Errorf("docs/mobile/README.md's row still prices the relay as \"not recommended\", but §3.3 and Q7 make it the answer where the owner does not own every installation's project")
	}
	if !strings.Contains(index, "otherwise a relay") {
		t.Errorf("docs/mobile/README.md's row does not carry the conditional recommendation (\"otherwise a relay\") that §3.3 and Q7 settled on")
	}
	// §9's disclosure must be written per channel rather than for one channel:
	// the relay is a third party in the path, and the sentence has to say so.
	if !strings.Contains(doc, "written **per channel**") {
		t.Errorf("%s's §9 does not say the privacy disclosure is written per channel, so it cannot be true of a relay installation", docPath)
	}
}

// TestDocsMobilePushSaysWhoDeletesThePushNode: §7 and Q10(i) both claim the push
// data's lifetime is bounded by the pairing because revoke deletes the push node
// — and both cite §3.4 for it, which before this guard never said so: §3.4 said
// an orphaned push node is inert and §3.4/§3.5/§6 gave the DELETION to the
// sender's reaping. An implementer could not tell which of the two owns it. So
// §3.4 must state both paths and name revoke as the one that deletes, and the
// two citing sentences must keep citing it.
func TestDocsMobilePushSaysWhoDeletesThePushNode(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)

	// The statement §7 and Q10 lean on.
	if !strings.Contains(doc, "deletes BOTH nodes") {
		t.Errorf("%s's §3.4 does not state that `fugaro pair revoke` deletes both `config/admin/devices/$uid` and `config/admin/push/$uid`, which §7 and Q10(i) both assert", docPath)
	}
	// And the two sentences that assert it must keep pointing at it.
	for _, want := range []string{
		"**revoke** (§3.4 deletes the push node",
		"deleted with the push node on\n  revoke",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer carries %q: the retention answer must keep citing §3.4's deletion rather than asserting it from nowhere", docPath, want)
		}
	}
	// The reaping must be described as the net, not as the mechanism: a §3.4
	// that calls the sender's reaping "what makes revocation final" is the
	// ambiguity this round closed.
	if strings.Contains(doc, "which is what makes revocation final") {
		t.Errorf("%s still says the sender's reaping is %q, attributing finality to a later sweep rather than to the revoke act", docPath, "which is what makes revocation final")
	}
}
