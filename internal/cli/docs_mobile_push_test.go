package cli

import (
	"os"
	"path/filepath"
	"regexp"
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
// Every path below was read on `origin/main` at 371038f — the tree the design
// itself names — and is in this branch's tree. The document links its neighbours
// relatively (../reconciliation.md, ../../../SECURITY.md), so a citation counts
// in either form.
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
// in order, skipping an image (`![alt](target)`). It is deliberately shallow —
// the plan's own links are simple relative paths. An image is skipped rather
// than returned because its target is an asset and not a citation a reader
// follows: a badge or a screenshot in the entry would otherwise be stat'ed as
// if it were a link and fail this test with a message about a broken link.
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
		// The `[` that opens this link, and the `!` an image puts before it.
		if bracket := strings.LastIndex(s[:open], "["); bracket > 0 && s[bracket-1] == '!' {
			i = open + close + 1
			continue
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
	// And the two sentences that assert it must keep pointing at it. The
	// fragments are matched against the document with its whitespace flattened:
	// this document wraps its prose, so a needle carrying the wrap it happens to
	// have today ("... on\n  revoke") breaks on a harmless reflow — the same
	// convention TestDocsMobilePushDoesNotInventAMailboxDisclosureInSecurityMD
	// states for its own needles.
	flat := normSpace(doc)
	for _, want := range []string{
		"**revoke** (§3.4 deletes the push node",
		// Q10(i)'s own sentence, citation included: the bare phrase also occurs
		// in §3.6's note recording the withdrawal, which would satisfy a shorter
		// needle on its own.
		"and are **deleted with the push node on revoke** (§3.4)",
	} {
		if !strings.Contains(flat, normSpace(want)) {
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

// TestDocsMobilePushMACEevidenceIsInTheTree: the B4 correction leans on what the
// rules do with a message's `mac`, and its first draft cited `rules.go`'s
// `MACOK`, `patternHex` and `MaxMacLength` as symbols that lived on PR #601's
// branch and *not* on main ("none of those symbols exists"). #601 has since
// merged — rules generation 3 is in the tree at `371038f` — so that paragraph
// described a tree its reader is not standing in. It now cites the symbols as
// being in the tree, and this test holds that citation to the tree in both
// directions: every symbol the paragraph quotes must be in
// `internal/budget/rules/rules.go`, the commit named must be the one the design
// says it read, and the "on a branch, not on main" claim must be gone.
//
// The last of those is checked as a *shape* and not as one remembered sentence.
// The first version of this guard listed the exact phrase it had been written
// against, and a later revision of §3.9 carried the same claim in different
// words — "the symbols live on a branch, not on `main`" — so the guard passed on
// the defect it exists to catch. It now flattens the document and matches the
// claim's shapes (`live on a branch`, `not on \`main\“, `none of those symbols
// exists`), which is what makes the wording's survival impossible rather than
// just unlikely.
//
// Mutation (verified, run and restored): renaming `MaxMacLength` in rules.go,
// `macField` in shared/mailbox-schema/index.js or `verifyMac` in
// shared/mailbox-schema/mac.js; dropping "are in the tree now" from the
// paragraph; and re-planting the surviving claim in §3.9 ("the symbols live on a
// branch, not on `main`"), and each of its variants alone ("live on a branch",
// "are not on `main`", "none of those symbols exists"): this test fails, each
// way.
func TestDocsMobilePushMACEevidenceIsInTheTree(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	flat := normSpace(doc)

	// (a) the three symbols the paragraph quotes are read out of the file it
	// quotes, so a merge that renames or drops one fails this guard instead of
	// leaving the design resting on a symbol nobody has.
	rules := readDoc(t, "internal/budget/rules/rules.go")
	for _, symbol := range []string{"MACOK", "MaxMacLength", "patternHex"} {
		if !strings.Contains(rules, symbol) {
			t.Errorf("internal/budget/rules/rules.go no longer defines %s, which %s's MAC paragraph quotes as being in the tree: re-read the paragraph and re-cite it", symbol, docPath)
		}
		if !strings.Contains(doc, symbol) {
			t.Errorf("%s no longer names %s: the paragraph carrying B4's evidence must quote the symbol it rests on", docPath, symbol)
		}
	}

	// (b) the tree and the commit it was read at, so a reader can go and look.
	for _, want := range []string{
		"origin/main",
		"`371038f`",
		"are in the tree now",
		"merged as #601",
		// The quote that actually exists, with the clause that carries the
		// conclusion: the characters are checked, the value never is.
		"never the value: verifying a tag needs the key",
	} {
		if !strings.Contains(flat, normSpace(want)) {
			t.Errorf("%s lacks %q: the MAC evidence must cite the symbols where its reader can find them", docPath, want)
		}
	}

	// (c) the pre-merge *claim*, as a class rather than as the one sentence this
	// guard was first written against. A needle list is only as good as the
	// sentence it remembers, and the sentence that survived the last round read
	// "the symbols live on a branch, not on `main`" in §3.9 — one word away from
	// the phrase the list held, and therefore invisible to it. So the shapes
	// below are regexes over the flattened text: "live(s) on a branch", "not on
	// `main`", "none of those symbols exists", each of which asserts, in the
	// present tense, that the symbols are somewhere other than this tree.
	//
	// Two honest mentions are deliberately out of reach: the header's sentence
	// about the *earlier revision* ("... the MAC symbols were **not in the
	// tree**", dated to `90ba554`) and §3.4's "in the tree rather than on a
	// branch", which is the correction itself. Both are past tense or a denial,
	// so the verbs are part of the patterns.
	for _, bad := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:live|lives|are|is) on (?:a|the|that|#601's) branch\b`),
		regexp.MustCompile("(?i)not on `main`"),
		regexp.MustCompile(`(?i)none of (?:those|these) symbols exists\b`),
		regexp.MustCompile(`(?i)not in this branch's tree`),
		regexp.MustCompile(`(?i)on PR #601's branch`),
	} {
		if m := bad.FindString(flat); m != "" {
			t.Errorf("%s still says %q, which describes the tree before #601 merged: the symbols are in the tree now and the paragraph has to say so", docPath, m)
		}
	}

	// The schema side is the other half of the claim and must be cited too,
	// since it is the same on both: the shape the phone validates and the keyed
	// question are different functions. Held against the schema files as well,
	// so the citation is checkable rather than remembered.
	idx := readDoc(t, "shared/mailbox-schema/index.js")
	macjs := readDoc(t, "shared/mailbox-schema/mac.js")
	for _, sym := range []string{"macField", "MAC_MAX"} {
		if !strings.Contains(idx, sym) {
			t.Errorf("shared/mailbox-schema/index.js no longer defines %s, which %s cites as the shape check", sym, docPath)
		}
	}
	if !strings.Contains(macjs, "verifyMac") {
		t.Errorf("shared/mailbox-schema/mac.js no longer exports verifyMac, which %s cites as the keyed check the rules cannot make", docPath)
	}
	for _, want := range []string{
		"shared/mailbox-schema/index.js",
		"shared/mailbox-schema/mac.js",
		"macField",
		"verifyMac",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q: the shape check and the keyed check are different symbols and both must be named", docPath, want)
		}
	}
}

// TestDocsMobilePushNamesNoUnresolvableLabels: two sentences of the design cited
// "the B4 conclusion" and "§3.3's B5 correction" — labels from the review rounds
// that produced them, defined nowhere in this document or anywhere in the repo.
// Every other reference in the document resolves to a section, a rule, a
// question or a file, and a reader who cannot resolve a citation cannot check
// the claim it carries. These now read "the conclusion the paragraph above
// reaches" and "the settled recommendation that §3.3 and Q7 withdrew", and this
// guard keeps the class out: no `B<n>` token in the document at all.
//
// Mutation (verified, run and restored): putting "the B4 conclusion" back turns
// this test red.
func TestDocsMobilePushNamesNoUnresolvableLabels(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	if m := regexp.MustCompile(`\bB[0-9]{1,2}\b`).FindString(normSpace(doc)); m != "" {
		t.Errorf("%s carries the review-round label %q, which nothing in the repo defines: name the section, rule, question or file the claim comes from", docPath, m)
	}
}

// TestDocsMobilePushHasOneWritersTable: §3.4 said "the pairing command is the
// only thing that ever wrote the push node", which contradicts Q3's recommended
// re-registration principal (§3.5 (b)) and §3.8's preferences writer — the very
// writers the same section describes. The reconciliation is one table naming,
// per node, who writes it, what it writes and what it may not do, and every
// section has to agree with it. This guards the table's presence and the
// withdrawn sentence's absence.
func TestDocsMobilePushHasOneWritersTable(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)

	if strings.Contains(doc, "the only thing that ever wrote the push node") {
		t.Errorf("%s still says the pairing command %q, which the design's own §3.5 (b) and §3.8 writers contradict", docPath, "the only thing that ever wrote the push node")
	}
	for _, want := range []string{
		"Who writes which node",
		// The three writers, each named in the table.
		"the registration principal",
		"the same registration principal",
		"the sender",
		// The prohibition that keeps revocation single-act. It is not "never
		// create the push node" — the registration principal may create that one
		// (see TestDocsMobilePushRegistrationRowAgreesWithTheProse, which is
		// where the two readings are reconciled); what may never happen is a
		// write into `devices/**`, the family whose existence is the admission.
		"never writes `devices/**`",
		// §3.5 (a)'s prose, read off `rules.json.tmpl`: the pairing session has
		// no registration token to write, so pairing is NOT one of the push
		// node's writers — and the table says so instead of leaving the row's
		// absence to be inferred.
		"pairing writes nothing under `push/**` at all",
		"the rows below are the push node's whole set",
	} {
		if !strings.Contains(normSpace(doc), normSpace(want)) {
			t.Errorf("%s lacks %q: the writers table must name every writer and the rule each one obeys", docPath, want)
		}
	}

	// The section calls this table its single source of truth, so its own rows
	// are held against each other and against §3.5 (a) -- not only against a
	// remembered list of phrases. The table shipped with this failure once
	// already: the `config/admin/devices/$uid` row had `fugaro pair` "never
	// touches `push/**`" three lines above a `config/admin/push/$uid` row where
	// that same command wrote the push node, and the push row had pair "never
	// writes `devices/**`" directly below the device row where pair creates it.
	// The fix round after that one removed the impossible push row instead, on
	// the rules' own evidence: D165's pairing request node admits only the
	// phone's public key and its timestamp, so pairing has no registration token
	// to write. Pairing therefore now has exactly ONE row, and that row has to
	// say it makes no `push/**` write -- a row that claims one, or that leaves
	// the absence unsaid, is the same defect read the other way.
	rows := writersTableRows(t, doc)
	if len(rows) == 0 {
		t.Fatalf("%s's writers table has no rows for this guard to check", docPath)
	}
	var pairRows int
	for _, r := range rows {
		if !strings.Contains(r.writer, "`fugaro pair`") || strings.Contains(r.writer, "revoke") {
			continue
		}
		pairRows++
		if strings.Contains(r.writes, "push/") {
			t.Errorf("%s's %q writers-table row claims a `push/**` write (%q), but §3.5 (a) says D165's pairing flow cannot carry a registration token, so pairing writes no push node", docPath, r.writer, r.writes)
		}
		if !strings.Contains(r.never, "never writes `push/**`") {
			t.Errorf("%s's %q writers-table row does not say it makes no `push/**` write — the device node's row is the one place a reader looks for what pairing does and does not write (§3.5 (a)) — its \"never\" cell reads %q", docPath, r.writer, r.never)
		}
	}
	if pairRows != 1 {
		t.Errorf("%s's writers table has %d `fugaro pair` rows, want 1: pairing writes the device node only, and the push node's rows belong to the registration principal, the preferences writer and the sender (§3.5 (a))", docPath, pairRows)
	}
}

// TestDocsMobilePushRegistrationRowAgreesWithTheProse: §3.4's writers table
// forbade the registration principal from ever creating `config/admin/push/$uid`
// when `devices/$uid` was absent, while two other passages of the SAME document
// described exactly that write as the ordinary consequence of a re-registration
// racing a revoke — §3.5 (b)'s "a re-registration message the phone sent before
// the revoke and the consumer processes after it writes `push/$uid`", and the
// reaping paragraph, which names that in-flight message as one of the paths that
// leaves the two nodes out of step. A table the design calls its single source
// of truth cannot forbid what the design relies on.
//
// Which side moves is decided by the rules, not by taste: `rules.json.tmpl`
// denies `config/admin` to clients only (".read": false, ".write": false), and
// the registration principal writes with `roles/firebasedatabase.admin`, which
// bypasses the rules — so no rule can couple the push node to the device node on
// the write side, and an implementation could not have honoured the prohibition.
// The row therefore permits the create and keeps the guarantee that matters
// (never `devices/**`), and this test pins the row and both prose passages
// against each other so the three cannot drift apart again.
func TestDocsMobilePushRegistrationRowAgreesWithTheProse(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	flat := normSpace(doc)

	rows := writersTableRows(t, doc)
	if len(rows) == 0 {
		t.Fatalf("%s's writers table has no rows for this guard to check", docPath)
	}
	var found int
	for _, r := range rows {
		if !strings.Contains(r.node, "(token, platform, updated_at)") ||
			!strings.Contains(r.writer, "the registration principal") ||
			strings.Contains(r.writer, "the same registration principal") {
			continue
		}
		found++
		// The row may not forbid the create: the principal holds
		// `roles/firebasedatabase.admin`, so the prohibition would describe an
		// enforcement nothing has, and §3.5 (b) depends on the write landing.
		for _, bad := range []string{
			"never creates the node when `devices/$uid` is absent",
			"never creates the node",
			"never creates `push/$uid`",
		} {
			if strings.Contains(r.never, bad) {
				t.Errorf("%s's registration-principal row forbids %q, but §3.5 (b) and §3.4's reaping paragraph both rely on that create (the orphan reaped after a revoke, and the in-flight re-registration): %q", docPath, bad, r.never)
			}
		}
		// ...and it has to say the create is permitted, and keep the one
		// guarantee that is left to it.
		for _, want := range []string{"including a create", "never writes `devices/**`"} {
			if !strings.Contains(r.writes+r.never, want) {
				t.Errorf("%s's registration-principal row does not carry %q: the row has to permit the create the prose describes and keep the prohibition that stops it re-admitting a device", docPath, want)
			}
		}
	}
	if found != 1 {
		t.Fatalf("%s's writers table has %d rows for the registration principal's token write, want 1: this guard's marker moved", docPath, found)
	}

	// The two prose passages, each stated positively, so the table cannot be
	// made consistent by deleting the prose instead of by correcting the row.
	for _, want := range []string{
		// §3.5 (b): the write after a revoke is a create, and it says so.
		"**creates** `push/$uid`",
		"§3.4's table records that this principal may create it",
		// §3.4's reaping paragraph: the in-flight re-registration is one of the
		// paths that leaves the two nodes out of step.
		"a re-registration the consumer had in flight when the revoke landed — a create, which §3.4's table permits (§3.5 (b))",
		// And the reason the table permits it, decided from the template.
		"database admin bypasses the rules",
		"no rule can couple `push/$uid` to `devices/$uid` on the write side",
	} {
		if !strings.Contains(flat, normSpace(want)) {
			t.Errorf("%s lacks %q: the writers table, §3.5 (b) and the reaping paragraph have to give one account of who may create the push node", docPath, want)
		}
	}
}

// TestDocsMobilePushRegistrationTokenHasACarrier: the writers table, §3.4's
// deletion paragraph and §3.5 (a) all said `fugaro pair` writes
// `config/admin/push/$uid` with "the registration the pairing session itself
// has" — a write nothing in D165/P6.6's pairing flow can make, because no step
// of that flow ever sees the phone's FCM registration token. What the session's
// one client write may carry is a fact about the deployed rules and not a matter
// of taste, so this guard pins the design to it: the request node's admitted
// shape, the emulator test that refuses every other field, the mailbox as the
// only carrier left, and the bounded field that carrier needs before it can be
// built at all.
//
// Mutation (verified, run and restored): putting the push-node row back for
// `fugaro pair` in the writers table, re-writing §3.5 (a) as "the CLI writes
// `config/admin/push/$uid` during pairing", or dropping the request node's
// admitted shape from §3.5 (a): this test fails, each way.
func TestDocsMobilePushRegistrationTokenHasACarrier(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	const planPath = "docs/mobile/implementation-plan.md"
	doc := readDoc(t, docPath)
	plan := readDoc(t, planPath)
	flat := normSpace(doc)

	// The claim that started this: pairing registers the token it took. It is
	// gone, in every phrasing it had.
	for _, stale := range []string{
		"the registration the pairing session itself has",
		"pairing writes both nodes",
		"**one of the writers** of the push node",
		"it writes it at pairing",
		"The CLI writes `config/admin/push/$uid` during D165/P6.6's pairing",
	} {
		if strings.Contains(flat, normSpace(stale)) {
			t.Errorf("%s still says %q, but D165's pairing flow cannot carry a phone's registration token: the request node admits only the phone's public key and its timestamp (§3.5 (a))", docPath, stale)
		}
	}

	for _, want := range []string{
		// What the request node admits, quoted from the template rather than
		// remembered: `key`, `at`, and a catch-all that refuses the rest.
		`newData.hasChildren(['key', 'at'])`,
		`"$other": { ".validate": false }`,
		// The emulator test that refuses a request carrying anything else, and
		// the one line of it that says so.
		"internal/budget/rules/emulator_pairing_test.go",
		"a request carrying a field the node does not have",
		// The consequence for the pairing command, stated where §3.4 and the
		// writers table can be read against it.
		"has nothing to write as a registration and **does not write the push node**",
		// The mailbox is the carrier left, and the message node is closed too —
		// named field by field, with the test that proves the rules' half.
		"the only field that can carry a registration token is `text`",
		"a message carrying the unknown field",
		// ...and the bounded field the design asks for instead, with the plan
		// recording it as a prerequisite.
		"The design's recommendation is a bounded field instead — a registration kind with a required `token`",
		"the plan's P5.2 dependency row records it",
		// The reason the design does not simply add the field to the pairing
		// request instead: a rules change that buys shape (a) alone.
		"Making the request node admit a bounded `token` field would be a rules change",
	} {
		if !strings.Contains(flat, normSpace(want)) {
			t.Errorf("%s lacks %q: a registration token with no flow that can carry it is the defect this guard exists for", docPath, want)
		}
	}

	// The prerequisite has to be in the plan, in both places a reader looks.
	for _, want := range []string{"a mailbox-schema and rules change", "**P6.7**"} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not record %q among P5.2's prerequisites: the design's only registration path needs a field the mailbox does not have and a consumer to apply it", planPath, want)
		}
	}
}

// TestDocsMobilePushStatesTheNodeUnitOnce: §3.3 said "the node is per (device,
// installation)" and §3.4 repeated it, while §3.4's own diagram (one
// `config/admin/push/$uid`, with `token/<installation>/` children) and its
// writers table said the opposite — the node is per device and the CHILDREN
// multiply. Two statements of one unit cannot both stand, so the unit is stated
// once (§3.5) and every other place has to say the same thing.
//
// Mutation (verified, run and restored): re-writing either occurrence as "the
// node is per (device, installation)", or dropping §3.5's unit sentence: this
// test fails, each way.
func TestDocsMobilePushStatesTheNodeUnitOnce(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	flat := normSpace(doc)

	if strings.Contains(flat, "per (device, installation)") {
		t.Errorf("%s still calls the node per (device, installation), contradicting its own diagram and writers table: the node is per device and its `token/<installation>/` children are per installation (§3.3, §3.5)", docPath)
	}
	for _, want := range []string{
		// §3.5, the one place the unit is stated.
		"the node is per DEVICE, its children are per INSTALLATION",
		// §3.3, where the requirement is derived.
		"the node stays **one per device**",
		"the node is per device, its `token/` children are per installation",
		// §3.4's diagram and its table intro.
		"ONE PER DEVICE, keyed by the paired uid",
		"**The node is one per device; only its `token/<installation>/` children multiply (§3.3).**",
		// §3.4's "who can read" paragraph, the sentence that had it wrong.
		"leaves the *node* one per device (§3.5's unit sentence)",
	} {
		if !strings.Contains(flat, normSpace(want)) {
			t.Errorf("%s lacks %q: the node's unit has to be stated once and agreed with everywhere else", docPath, want)
		}
	}
	if got := strings.Count(flat, "(§3.5's unit sentence)"); got < 2 {
		t.Errorf("%s cites §3.5's unit sentence %d times, want at least 2 (the diagram and §3.4's read paragraph): a section that states the unit itself is a section that can disagree with it", docPath, got)
	}
}

// writersTableRow is one data row of §3.4's writers table, the table the design
// calls its single source of truth: the node, the writer, what it writes and
// what it must never do.
type writersTableRow struct{ node, writer, writes, never string }

// writersTableRows reads that table out of the document. Guards over it have to
// compare one row with another (a writer's prohibition against the same writer's
// write three lines down), which no phrase list can do.
func writersTableRows(t *testing.T, doc string) []writersTableRow {
	t.Helper()
	var rows []writersTableRow
	in := false
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if !in {
			in = strings.HasPrefix(line, "| node | writer |")
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 4 {
			t.Fatalf("writers table row %q splits into %d cells, want 4", line, len(cells))
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if cells[0] == "---" {
			continue // the header's rule line
		}
		rows = append(rows, writersTableRow{cells[0], cells[1], cells[2], cells[3]})
	}
	return rows
}

// TestDocsMobilePushQuotesThePlanAsItStandsAfterThisPR: §8 quoted the plan's
// check-table row 88 as "Push delivery — only if D121 says yes" — the text the
// SAME pull request rewrites in docs/mobile/implementation-plan.md, so by the
// time the branch merges the design quotes a row that no longer exists and
// reads as if D121 were still open. A quotation of a sibling document has to be
// checked against that document, not against a remembered copy of it, so this
// test reads the plan's own row and holds every plan phrase §8 quotes to it.
func TestDocsMobilePushQuotesThePlanAsItStandsAfterThisPR(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	const planPath = "docs/mobile/implementation-plan.md"
	doc := readDoc(t, docPath)
	plan := readDoc(t, planPath)

	// Markdown emphasis, wrapping and a quote's own sentence case are the
	// document's, not the quote's: a quoted sentence may carry any of them.
	// Everything else must match.
	norm := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(s, "**", "")), " "))
	}

	// The plan's row 88, as the plan has it in this tree.
	row := ""
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(line, "| **88** |") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("%s has no `| **88** |` check-table row for %s to quote", planPath, docPath)
	}
	if strings.Contains(row, "only if D121 says yes") {
		t.Errorf("%s's row 88 is back to %q, which reads as a question still open though D121 was answered on 2026-10-17", planPath, "only if D121 says yes")
	}

	// §8 must assign 88 to that row's text, whatever it is.
	m := regexp.MustCompile(`assigns \*\*88\*\* to \*\*([^*]+)\*\*`).FindStringSubmatch(doc)
	if m == nil {
		t.Fatalf("%s's §8 no longer says which text it assigns Check 88 to", docPath)
	}
	if quoted := norm(m[1]); !strings.Contains(norm(row), quoted) {
		t.Errorf("%s's §8 assigns 88 to %q, which is not in %s's row 88 (%q): a quote of a row this pull request rewrites must be the row as it now stands", docPath, m[1], planPath, strings.TrimSpace(row))
	}

	// Every other plan phrase §8 quotes, held to the plan itself.
	for _, q := range []string{
		"The owner allocated 80–89 to this work on 2026-10-16",
		"Check numbers this plan takes (80–89)",
		"checks 80 and 83–88 by P4.2",
		"Checks 80–89, written into the register",
		"a check is never run before its text is in the register",
	} {
		if !strings.Contains(norm(doc), norm(q)) {
			t.Errorf("%s no longer quotes %q, which §8's allocation argument rests on", docPath, q)
		}
		if !strings.Contains(norm(plan), norm(q)) {
			t.Errorf("%s quotes %q, which %s does not say", docPath, q, planPath)
		}
	}

	// The plan's own P5.2 block pointed AT ITSELF the wrong way: it said "the
	// plan's own allocation below is ..." while the allocation it quotes -- the
	// sentence §8 leans on -- is stated near the top of the same document, above
	// the task block. A direction word is checkable, so it is checked.
	if strings.Contains(plan, "the plan's own allocation below") {
		t.Errorf("%s's P5.2 block says the allocation is %q, but the plan states it above (\"checks 80 and 83–88 by P4.2\" is near the top, not below this block)", planPath, "below")
	}
	if !strings.Contains(plan, "the plan's own allocation above") {
		t.Errorf("%s's P5.2 block no longer says where the plan states the allocation", planPath)
	}
}

// TestDocsMobilePushPlanRecordsTheDesignsPrerequisites: the design makes two
// tasks hard prerequisites of push and says so twice -- Q2's "push cannot ship
// before P6.4" (the rules generation the sibling node hangs off) and §3.4/§3.5
// naming P6.6 as the command that writes and deletes both nodes -- but the
// plan's Cross-task dependencies table recorded the whole P5.1-P5.3 group as
// depending on nothing (`| P5.1–P5.3 | — | D115, D121, D123 |`). A reader
// scheduling from that table would schedule the sender before the node and the
// command it needs exist. The prerequisites are recorded the way P3.6's row
// names P6.3: in the "Depends on" cell, with the state the plan's Phase 6 rows
// carry — the owner approved Phase 6 on 2026-10-18 (`approved 2026-10-18`, the
// phrase TestDocsMobilePlanPhase6IsApproved pins on every P6.N row), so the
// block and the row mark P6.4 **merged** (#601, the generation in the tree) and
// P6.6 approved and not built, rather than carrying the pre-approval
// "proposed, NOT APPROVED" this guard was first written against.
//
// Mutation (verified, run and restored): put "proposed, NOT APPROVED" back into
// the P5.2 dependency row or into its Depends-on block, or drop "merged"/"#601"
// from the row: this test fails, each way.
func TestDocsMobilePushPlanRecordsTheDesignsPrerequisites(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	const planPath = "docs/mobile/implementation-plan.md"
	doc := readDoc(t, docPath)
	plan := readDoc(t, planPath)

	// The design's own statement of the prerequisite, so this guard fails if
	// either side drops it rather than pinning a plan row to nothing.
	if !strings.Contains(normSpace(doc), normSpace("push cannot ship before P6.4")) {
		t.Errorf("%s no longer states that push cannot ship before P6.4, the prerequisite this guard holds the plan to", docPath)
	}

	row := ""
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "| P5.2 ") {
			row = strings.TrimSpace(line)
			break
		}
	}
	if row == "" {
		t.Fatalf("%s has no P5.2 row in its Cross-task dependencies table", planPath)
	}
	cells := strings.Split(strings.Trim(row, "|"), "|")
	if len(cells) != 3 {
		t.Fatalf("%s's P5.2 dependency row %q splits into %d cells, want 3", planPath, row, len(cells))
	}
	depends := strings.TrimSpace(cells[1])
	if depends == "—" || depends == "-" {
		t.Errorf("%s records P5.2 as depending on nothing (%q), though the design's Q2 makes P6.4 a prerequisite and §3.4 makes P6.6 the command that writes both nodes", planPath, depends)
	}
	for _, want := range []string{"**P6.4**", "**P6.6**", "approved 2026-10-18", "#601", "not built"} {
		if !strings.Contains(depends, want) {
			t.Errorf("%s's P5.2 dependency row does not name %q: %s", planPath, want, depends)
		}
	}
	if strings.Contains(depends, "NOT APPROVED") {
		t.Errorf("%s's P5.2 dependency row still says %q, though the owner approved Phase 6 on 2026-10-18: %s", planPath, "NOT APPROVED", depends)
	}
	// The task block's own "Depends on." paragraph has to name them too: the
	// block and the table are read by different readers (one reads the task,
	// the other schedules the phase), and the block is where the owner's "later
	// yes" sits, so a prerequisite recorded in only one of them is the same
	// split the finding was about.
	block := ""
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(line, "**Depends on.** The owner's **later, separate yes to push**") {
			block = line
			break
		}
	}
	if block == "" {
		t.Fatalf("%s's P5.2 block no longer opens its Depends-on paragraph with the owner's later yes", planPath)
	}
	// "**P6.4**", not "P6.4": the block also quotes the design's own sentence
	// ("push cannot ship before P6.4"), so the bare string would pass a block
	// whose prerequisite list had lost the task.
	for _, want := range []string{"**P6.4**", "**P6.6**", "approved 2026-10-18", "merged"} {
		if !strings.Contains(block, want) {
			t.Errorf("%s's P5.2 block does not name %q in its Depends-on paragraph: %s", planPath, want, block)
		}
	}
	if strings.Contains(block, "NOT APPROVED") {
		t.Errorf("%s's P5.2 block still says %q about its prerequisites, though the owner approved Phase 6 on 2026-10-18: %s", planPath, "NOT APPROVED", block)
	}

	// P3.6's row is the model: a prerequisite named in the cell with its state,
	// not silently assumed. The state it carries is main's own ("approved
	// 2026-10-18"), which is what makes the two rows readable side by side.
	if !strings.Contains(plan, "| P3.6 | ") || !strings.Contains(plan, "**P6.3** (signing; approved 2026-10-18)") {
		t.Errorf("%s's P3.6 row no longer names P6.3 the way this test says a prerequisite is recorded; update the guard's model", planPath)
	}
}

// TestDocsMobilePushReadmeAgreesPhase6IsApproved: docs/mobile/README.md's
// current status line — the first thing a reader of the directory sees — still
// said "Its tasks are **Phase 6 of the plan, proposed and NOT APPROVED**; this
// changes no approval", the same claim the plan's own Phase 6 rows lost on
// 2026-10-18 (TestDocsMobilePlanPhase6IsApproved), and a claim that contradicted
// the line's own first sentence ("the owner approved Phase 2 (P2.1, P2.2) on
// 2026-10-17"). This PR edits both files, so the two have to agree about the
// approval, and the line's own open questions have to be attributed: it called
// the reconciliation's items settled two sentences before naming the design's
// Q1–Q10 as open, which reads as a contradiction until each set is named.
//
// The guard reads the *current* status line, not the file: line 5 is the status
// "as first written, kept", and its "no task is approved" is deliberately the
// 2026-10-17 wording, still true of the moment it describes.
//
// Mutation (verified, run and restored): put "proposed and NOT APPROVED" back
// into the current line, delete "merged as #601" from it, or drop the clause
// that says whose questions the Q1–Q10 are: this test fails, each way.
func TestDocsMobilePushReadmeAgreesPhase6IsApproved(t *testing.T) {
	const path = "docs/mobile/README.md"
	doc := readDoc(t, path)

	line := ""
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "**Status (2026-10-17, later):") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("%s's current status line no longer opens with %q; update this guard's marker", path, "**Status (2026-10-17, later):")
	}

	for _, bad := range []string{
		"proposed and NOT APPROVED",
		"no task is approved",
	} {
		if strings.Contains(line, bad) {
			t.Errorf("%s's status line still says %q, which the owner's approval of Phase 6 on 2026-10-18 — and its own first sentence about Phase 2 — made false: %s", path, bad, line)
		}
	}
	for _, want := range []string{
		// The approval, recorded the way the plan records it.
		"Phase 6 of the plan, which the owner approved on 2026-10-18",
		// And the part of the phase that is in the tree, so the line does not
		// read as if approving it left everything unbuilt.
		"merged as #601",
		// Which document the open questions belong to.
		"the design's own questions, distinct from the reconciliation's settled items above",
	} {
		if !strings.Contains(normSpace(line), normSpace(want)) {
			t.Errorf("%s's status line lacks %q: the README and the plan have to give one account of Phase 6, and the line has to say whose open questions the Q1–Q10 are", path, want)
		}
	}
}

// TestDocsMobilePushSenderCategoriesExcludeFinishedAndFailed: §3.6(B) listed the
// run registry among "the sources §1's reconciled category set has", §3.7
// illustrated dedupe with a run ending `infra_error` sending "failed", §3.6(A)
// opened with "Run finished / failed" and §6's storm row said "many runs fail at
// once". §1 and the owner's answered D160 exclude that pair — the phone has no
// read of `agents/$slug/$run` or `outcomes/**`, which is the ground the owner
// declined "include failed and halted runs" on — so an implementer building the
// sender from those four sentences would build the category the owner declined.
// No category in the reconciled set is sourced from the run registry.
func TestDocsMobilePushSenderCategoriesExcludeFinishedAndFailed(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	flat := normSpace(doc)

	for _, bad := range []string{
		// §3.7's dedupe example, and §3.6(A)'s opening case.
		`a run that ends ` + "`infra_error`" + ` sends one "failed"`,
		"Run finished / failed",
		// §6's storm row.
		"many runs fail at once",
		// §3.6(B)'s source list, with the registry among the set's sources.
		"the run registry (`internal/budget/registry.go`'s entries), the spend and kill",
	} {
		if strings.Contains(flat, normSpace(bad)) {
			t.Errorf("%s still carries %q: §1's reconciled set (D160) excludes finished and failed, so no sender built from that sentence sends a category the owner declined", docPath, bad)
		}
	}

	for _, want := range []string{
		// §3.6(B): the registry is read by the sweep, and is not made a category.
		"does **not** turn that into a category",
		"a sender that derived them from the registry would build the category the owner declined",
		// §3.7's dedupe example is now a category that exists.
		`a cap that is still reached on the next sweep sends one "cap reached"`,
		// And §6's storm row.
		"a storm (many runs cross their cap at once)",
	} {
		if !strings.Contains(flat, normSpace(want)) {
			t.Errorf("%s lacks %q: the sender's examples must be categories in §1's reconciled set", docPath, want)
		}
	}
}

// TestDocsMobilePushPerPairingKeyCitationPointsAtTheKey: §1 cited §3.6 for "the
// sender holds no per-pairing key", and §3.6 never makes that claim — its "no
// key" prose is about the FCM credential ("no downloaded key", "no key on a
// laptop"). The per-pairing key is §4 rule 11's and §5's. A citation that sends
// a reader to a section about a different key is the dangling-citation class
// TestDocsMobilePushSaysWhoDeletesThePushNode was written to catch for §7/Q10's
// §3.4 reference.
func TestDocsMobilePushPerPairingKeyCitationPointsAtTheKey(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	flat := normSpace(doc)

	if strings.Contains(flat, normSpace("holds no per-pairing key (§3.6)")) {
		t.Errorf("%s still cites §3.6 for the per-pairing key, which §3.6 never claims: its \"no key\" prose is about the FCM credential", docPath)
	}
	for _, want := range []string{
		"the sender holds no per-pairing key",
		// The sections that do make the point.
		"§4 rule 11 is what makes the app check it",
		"the rules cannot verify a tag",
		// And the note that keeps a reader from re-reading §3.6 for it.
		"argument is about a different credential",
	} {
		if !strings.Contains(flat, normSpace(want)) {
			t.Errorf("%s lacks %q: the per-pairing-key claim must cite the sections that make it", docPath, want)
		}
	}
}

// TestMarkdownLinkTargetsSkipsImages: markdownLinkTargets' doc comment said it
// ignores an image, and the implementation did not — `![alt](target)` contains
// `](`, so the target came back and was stat'ed as a link. Inert while the
// plan's entry carries no image; a badge or a screenshot added later would fail
// the resolution check with a message about a broken link.
func TestDocsMobilePushMarkdownLinkTargetsSkipsImages(t *testing.T) {
	got := markdownLinkTargets("See ![the diagram](../assets/x.png) and [the design](design/push-notifications.md), then [two](a.md).")
	want := []string{"design/push-notifications.md", "a.md"}
	if len(got) != len(want) {
		t.Fatalf("markdownLinkTargets = %q, want %q: an image target must not be returned as a link", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("markdownLinkTargets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDocsMobilePushLedgerHasOneHome: the dedupe/cap ledger was stated three
// incompatible ways — §3.6 said "under config/admin ... or in Firestore under
// its roles/datastore.user", while §7 and Q10(i) pinned it to the push node
// revoke deletes, §3.4's node diagram omitted it, and §3.4's writers table
// listed the sender's only writes as deletions. A reader could not tell whether
// Q10(i)'s "deleted with the push node on revoke" was true. The home is now
// config/admin/push/$uid/ledger and every section has to say so.
func TestDocsMobilePushLedgerHasOneHome(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	// Fragments, not whole sentences: the document wraps its prose and aligns
	// its node diagram's comments, so a needle carrying either would break on a
	// harmless reflow.
	flat := normSpace(doc)

	// The withdrawn alternative: a second store would make Q10(i) false.
	for _, bad := range []string{
		"or in Firestore under its `roles/datastore.user`",
		"(`config/admin` or Firestore)",
	} {
		if strings.Contains(flat, normSpace(bad)) {
			t.Errorf("%s still offers %q as the ledger's home; the ledger lives in config/admin/push/$uid/ledger and nowhere else", docPath, bad)
		}
	}

	for _, want := range []string{
		// The node diagram carries it (§3.4).
		"ledger/ # §3.6/§3.7: THE SENDER'S OWN STATE, and its only home",
		// The writers table carries the sender's only write.
		"| `config/admin/push/$uid/ledger` | **the sender**",
		"**The ledger's home is the push node and nowhere else.**",
		// The three sections that used to disagree now name the same place.
		"in **`config/admin/push/$uid/ledger`**",                                   // §3.6
		"`config/admin/push/$uid/ledger` (§3.4, §3.6 — the ledger's one home)",     // §3.7
		"the ledger in its `ledger` child (§3.4, §3.6: one home, no second store)", // §7
		"the dedupe ledger (the push node's `ledger` child, §3.4/§3.6)",            // Q10(i)
		// §6's two rows: the no-send row is a §3.6/§3.7 property, not §3.4's,
		// and the queued intent lives in the same node.
		"the sender writes no ledger entry",
		"a pending row in the same device's `config/admin/push/$uid/ledger`",
	} {
		if !strings.Contains(flat, normSpace(want)) {
			t.Errorf("%s lacks %q: the ledger's home must be stated once and cited by every section that mentions it", docPath, want)
		}
	}

	// §6's first row used to attribute "records nothing" to §3.4, which says
	// only that the sender sends when both nodes exist.
	if strings.Contains(doc, "the sender records nothing (§3.4)") {
		t.Errorf("%s's §6 still cites §3.4 for %q, a §3.6/§3.7 property", docPath, "records nothing")
	}
}

// TestDocsMobilePushCategorySetIncludesCapNear: §3.1 enumerated the reconciled
// set as "D160's three plus the prompt" and dropped cap-near, which §1, Q4(i)
// and §3.8 all carry (D160: "available, off by default"). A category present in
// three sections and absent from the one that defines what a payload carries is
// the same class of staleness the B2 fix removed.
func TestDocsMobilePushCategorySetIncludesCapNear(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)
	if !strings.Contains(doc, "consumer offline), **cap-near**") {
		t.Errorf("%s's §3.1 does not include cap-near in the reconciled category set, though §1, Q4(i) and §3.8 all do", docPath)
	}
	if !strings.Contains(doc, "cap-near, and the prompt") {
		t.Errorf("%s's Check 88 outline does not carry cap-near in the reconciled set", docPath)
	}
}

// TestDocsMobilePushBranchOnlyCitationsAreAttributed: this design cites things
// a reader cannot stat in this branch's tree — the sibling orchestrator design
// (#632) is still unwritten, and papers a citation of *any* path that the walk
// below can neither stat nor match to a basename in the tree. Each such citation
// must say where it lives: a branch name, a PR number, a commit, or `origin/main`
// with a date. The failure this catches is a citation that reads as if it were in
// the tree — which is how the first draft's `MACOK`/`patternHex`/`MaxMacLength`
// read before #601 merged; those symbols are in the tree now, and the design
// describes them as being there.
//
// The attribution has to sit NEAR the citation: this document mentions PR #601
// and `origin/main` in other paragraphs entirely, so a paragraph-wide check
// would pass a citation that is not attributed at all (it did, on the first
// attempt at this guard, which is why the window is here).
//
// Mutation (verified, run and restored): change "`371038f` carries it" in the
// design to another commit, or put "not in this branch's tree" back: this test
// fails, both ways.
func TestDocsMobilePushBranchOnlyCitationsAreAttributed(t *testing.T) {
	const docPath = "docs/mobile/design/push-notifications.md"
	doc := readDoc(t, docPath)

	// Every basename in the tree, so a bare `rules.go` is not mistaken for an
	// absent citation. The walk skips the directories that would only slow it
	// down.
	basenames := map[string]bool{}
	_ = filepath.Walk("../../", func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		basenames[info.Name()] = true
		return nil
	})
	if len(basenames) == 0 {
		t.Fatal("the tree walk found no files: this guard cannot tell an absent citation from a present one")
	}

	const window = 300
	pathRe := regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:go|js|mjs|tmpl|json|tf|md|astro|txt|yaml|yml))`")
	for _, loc := range pathRe.FindAllStringSubmatchIndex(doc, -1) {
		cited := doc[loc[2]:loc[3]]
		if _, err := os.Stat("../../" + cited); err == nil {
			continue
		}
		if basenames[filepath.Base(cited)] {
			continue
		}
		lo, hi := loc[0]-window, loc[1]+window
		if lo < 0 {
			lo = 0
		}
		if hi > len(doc) {
			hi = len(doc)
		}
		near := doc[lo:hi]
		if !strings.Contains(near, "origin/main") &&
			!strings.Contains(near, "fugaro/") &&
			!strings.Contains(near, "PR #") &&
			!strings.Contains(near, "sibling design #") {
			t.Errorf("%s cites %q, which is not in this branch's tree, without naming the branch, PR or commit it lives on within %d characters", docPath, cited, window)
		}
	}

	// ...and it must say WHICH tree it is describing. The first wording, "not in
	// this branch's tree", was true of the tree this document was written in
	// (before the catalogue file and before #601) and false of this one — the
	// catalogue is on `main` now, so a reader here finds it and the sentence
	// reads as a mistake. The design names both the base it started from and the
	// commit it was last read at, which is what makes a citation a reader cannot
	// stat checkable at all.
	if strings.Contains(doc, "not in this branch's tree") {
		t.Errorf("%s still says a cited file is %q, which is not true of the tree this document is read in: name the base (`82ba600`) and the commit read (`371038f`) instead, as docs/website/redesign/reconciliation.md names its base", docPath, "not in this branch's tree")
	}
	for _, want := range []string{"this branch's base, `82ba600`", "`371038f` carries it"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer carries %q: the design has to say which tree each citation was checked against", docPath, want)
		}
	}
}
