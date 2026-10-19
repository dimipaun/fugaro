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

	// The number the design proposes, read from the register itself: a
	// `## Check 88` section or a `| 88 |` row in the checklist means another
	// document has taken it, and this design would be proposing a taken number.
	checklist := readDoc(t, "docs/gcp-live-checklist.md")
	for _, taken := range []string{"## Check 88", "| 88 |"} {
		if strings.Contains(checklist, taken) {
			t.Errorf("docs/gcp-live-checklist.md carries %q, so Check 88 is no longer free and %s must propose the next number", taken, path)
		}
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
