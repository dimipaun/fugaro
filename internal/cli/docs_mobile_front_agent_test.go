package cli

import (
	"strings"
	"testing"
)

// TestDocsMobileFrontAgentDesignIsWritten (mobile plan Task P5.4.1, approved
// by the owner on 2026-11-26): the design of record for the conversational
// front agent exists, says plainly that it is design only and that the approval
// of P5.4 builds nothing, and carries the sections the task's Files line names:
// the front agent's job, its conversation with the user, the brief it shapes,
// the "go" it waits for and the handoff it makes. Its red state is this test:
// there was no docs/mobile/design/front-agent.md in the tree, and the plan says
// P5.4.1 writes it rather than re-grounding a draft.
func TestDocsMobileFrontAgentDesignIsWritten(t *testing.T) {
	const path = "docs/mobile/design/front-agent.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)
	for _, want := range []string{
		// The status line, the task it is, and the approval it is written under.
		"design only; nothing is implemented",
		"design of record",
		"Task **P5.4**",
		"**P5.4.1**",
		"approved P5.4 on 2026-11-26",
		"P5.4.1–P5.4.9",
		// The functional spec's future option this document designs.
		"functional-spec-v0.1.md",
		// The structure the task's Files line names: the job, the conversation,
		// the brief, the "go" and the handoff.
		"## 1. The problem",
		"## 3. The proposal",
		"### 3.1 The front agent's job",
		"### 3.2 Its conversation with the user",
		"### 3.3 The brief it shapes",
		`### 3.4 The "go" it waits for`,
		"### 3.5 The handoff it makes",
		// The plan's four open questions are answered in the document rather than
		// before it, each as its own question.
		"## 4. Question 1 — the placement",
		"## 5. Question 2 — the credential",
		"## 6. Question 3 — the brief's carriage",
		"## 7. Question 4 — the memory and the retention",
		"**Q1 — ",
		"**Q2 — ",
		"**Q3 — ",
		"**Q4 — ",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	// It decides nothing silently: the questions are answered as recommendations
	// with the alternative priced, not as settled decisions.
	for _, want := range []string{
		"the alternative, priced",
		"the owner's to ratify or amend",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsMobileFrontAgentStatesItsFourRecommendations: the whole point of
// P5.4.1 is that the owner ratifies or amends a written design rather than
// answering into a blank, so each of the plan's four questions is answered with
// the recommended option named — the placement, the credential, the brief's
// carriage and the memory — and the recommendation is the one the plan's own
// task sections are written against. Delete one section and this pin fails on
// the statement it was meant to hold, which is exactly the red step the task
// names.
func TestDocsMobileFrontAgentStatesItsFourRecommendations(t *testing.T) {
	const path = "docs/mobile/design/front-agent.md"
	flat := flattenDoc(readDoc(t, path))
	for _, want := range []string{
		// Q1 — the placement: a sibling service per installation, priced against
		// D172's own alternative, a second agent behind the phase-1 consumer.
		"**Recommended: a sibling service per installation**",
		"a second agent behind the phase-1 consumer on the laptop",
		"dies with the laptop",
		// Q2 — the credential: candidate (A) the credentials service is the
		// target, candidate (B) a mounted secret is the interim.
		"**Recommended: the credentials service",
		"the third place a model credential would live",
		"a Secret Manager secret mounted into this one service",
		// Q3 — the brief travels with the "go", in its own bounded field beside
		// the launch action and never inside the action's target.
		"**Recommended: yes",
		"never inside the action's `target`",
		"a repo alone",
		// Q4 — the phone holds the draft, the host holds the session, and the
		// turns expire with the mailbox.
		"**Recommended: the phone holds the draft and the host holds the session**",
		"72 hours",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks the recommendation %q: answers 1–4 are the whole reason this design is written before the tasks that read it", path, want)
		}
	}
}

// TestDocsMobileFrontAgentNamesTheNeverListsRuleFour: the always-on
// orchestrator's never-list forbids a model credential "of its own, ever", and
// its rule 4 is about the resident process. The front agent is the first task
// that has to say what that rule means for a process that is not the resident
// one — a second process, holding its own model session — so the design names
// §6's rule 4 and states what it means here rather than inheriting it unread.
func TestDocsMobileFrontAgentNamesTheNeverListsRuleFour(t *testing.T) {
	const path = "docs/mobile/design/front-agent.md"
	flat := flattenDoc(readDoc(t, path))
	for _, want := range []string{
		// The rule, quoted from the always-on design's §6.
		"design/always-on-orchestrator.md",
		"§6",
		"No model credential of its own, ever",
		// What it means here: the front agent is a second process, and the rule
		// that forbids the resident process a credential does not by itself
		// answer the question for this one — which is why Q2 is asked.
		"a second process",
		"the rule is about the resident process",
		"what it means for a second one",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks %q: §6 rule 4 is the ground this design stands on, and a second process is the fact that makes Q2 askable rather than answered", path, want)
		}
	}
}

// TestDocsMobileFrontAgentIsIndexedAndPlanned: the README indexes the design
// with a row that says what it is, and the plan's Task P5.4.1 entry names the
// three files the plan gives this change. A design that is landed but unindexed
// is one a reader of the directory does not find.
func TestDocsMobileFrontAgentIsIndexedAndPlanned(t *testing.T) {
	const design = "design/front-agent.md"

	index := readDoc(t, "docs/mobile/README.md")
	// The path alone proves nothing: the status paragraph below the index names
	// it too, to say the design does not exist yet. What the directory needs is a
	// row, so the check is for the row's own shape.
	row := ""
	for _, l := range strings.Split(index, "\n") {
		if strings.HasPrefix(l, "| [design/front-agent.md](design/front-agent.md) |") {
			row = l
			break
		}
	}
	if row == "" {
		t.Errorf("docs/mobile/README.md has no `| [%s](%s) |` index row", design, design)
	}
	// The index row is the directory's one-line statement of what the design is,
	// so the status it carries is the claim a reader takes away: it is design
	// only, it is P5.4's, and P5.4.1 wrote it.
	for _, want := range []string{
		"front agent",
		"P5.4",
		"design only",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("docs/mobile/README.md's %s row lacks %q", design, want)
		}
	}

	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	heading := "### Task P5.4.1:"
	start := strings.Index(plan, heading)
	if start < 0 {
		t.Fatalf("docs/mobile/implementation-plan.md has no %q entry", heading)
	}
	rest := plan[start+len(heading):]
	end := strings.Index(rest, "\n### Task ")
	if end < 0 {
		t.Fatalf("%s's entry runs to the end of the plan: no following task", heading)
	}
	entry := heading + rest[:end]
	for _, want := range []string{
		"docs/mobile/design/front-agent.md",
		"internal/cli/docs_mobile_front_agent_test.go",
		"docs/mobile/README.md",
		"approval of 2026-11-26",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("docs/mobile/implementation-plan.md's P5.4.1 entry lacks %q", want)
		}
	}
}
