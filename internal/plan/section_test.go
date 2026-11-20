package plan

// The section parser and the pin parser, read here as internal/plan's own
// (D2932). The two moved out of internal/orchestrator because a run's own job
// reads the same section the daemon hashed, and the job's binary does not link
// the orchestrator; the orchestrator's tests still drive them through
// PlanSectionDigest and planBriefPin, which is the other half of the move.

import (
	"strings"
	"testing"
)

// planSectionDoc is the shape a plan in this repository has: one heading per
// task, at the level the plan's author chose (three plans use `####`, this
// one among them), with a `#` in a fenced example that is a shell comment
// rather than a heading.
const planSectionDoc = "# A plan\n\n" +
	"An introduction nothing hashes.\n\n" +
	"### G1: the first task\n\n" +
	"Do the first thing.\n\n" +
	"```sh\n# not a heading\nfugaro verify test\n```\n\n" +
	"Then stop.\n\n" +
	"#### B2: a task at a deeper level\n\n" +
	"##### detail under it\n\n" +
	"Still B2's text.\n\n" +
	"### G3: another task\n\n" +
	"Something else.\n"

// TestPlanSectionAtAnyHeadingLevel: a plan names its tasks with the level its
// author chose, so a `####` task is a task, a `####` subheading inside another
// task's section stays part of that section's text — the section runs to the
// next heading at the same level or above — and a `#` in a fenced example is a
// shell comment rather than a heading.
func TestPlanSectionAtAnyHeadingLevel(t *testing.T) {
	got, ok := Section(planSectionDoc, "G1")
	if !ok {
		t.Fatal("Section found no G1 section")
	}
	if !strings.HasPrefix(got, "### G1: the first task\n") {
		t.Fatalf("the section does not start at G1's heading: %q", got)
	}
	if !strings.Contains(got, "# not a heading") || !strings.Contains(got, "Then stop.") {
		t.Fatalf("a fence ended the section early: %q", got)
	}
	// B2 is written a level below G1, so it is G1's text until G3's `###`
	// closes the section: a plan that nests its detail under a task has one
	// section, not two.
	if !strings.Contains(got, "Still B2's text.") {
		t.Fatalf("the section stopped at a deeper heading: %q", got)
	}
	if strings.Contains(got, "G3") || strings.Contains(got, "Something else.") {
		t.Fatalf("the section ran past the next heading at its own level: %q", got)
	}

	deep, ok := Section(planSectionDoc, "B2")
	if !ok {
		t.Fatal("Section found no B2 section")
	}
	if !strings.Contains(deep, "##### detail under it") || !strings.Contains(deep, "Still B2's text.") {
		t.Fatalf("B2's section stopped at its own subheading: %q", deep)
	}
	if strings.Contains(deep, "G3") {
		t.Fatalf("B2's section ran into G3: %q", deep)
	}
}

// TestPlanSectionDigestIsStableAndNamesTheTask: the digest is of the section
// text and nothing else, and the heading's first word is what says which task
// a section describes — so `### G10: ...` is not G1's.
func TestPlanSectionDigestIsStableAndNamesTheTask(t *testing.T) {
	first, ok := SectionDigest(planSectionDoc, "G1")
	if !ok || !strings.HasPrefix(first, "sha256:") || len(first) != len("sha256:")+64 {
		t.Fatalf("SectionDigest(G1) = %q, %v", first, ok)
	}
	again, _ := SectionDigest(planSectionDoc, "G1")
	if again != first {
		t.Fatalf("the digest is not stable: %q then %q", first, again)
	}
	if other, ok := SectionDigest(planSectionDoc, "G3"); !ok || other == first {
		t.Fatalf("G3's digest = %q, %v; two tasks cannot share one", other, ok)
	}
	// The blank line before the next heading is the whitespace between the two
	// rather than anything G1 says, so a plan edited by adding one is the same
	// section — which is what D2745's re-import rule reads.
	withBlank, _ := SectionDigest(strings.Replace(planSectionDoc, "\n### G3: another task", "\n\n### G3: another task", 1), "G1")
	if withBlank != first {
		t.Fatalf("a blank line before the next heading changed the section: %q then %q", first, withBlank)
	}
	if _, ok := SectionDigest("### G10: ten\n\nText.\n", "G1"); ok {
		t.Fatal("G10's heading was read as G1's")
	}
	if _, ok := SectionDigest(planSectionDoc, "NOPE"); ok {
		t.Fatal("a task no heading names has a section")
	}
}

// TestPlanSectionDocumentPath: the document beside a graph artifact is its
// stem with Markdown's extension, which is ArtifactPath's pairing read the
// other way — the runner finds a plan by its id over the tree's own list and
// then reads the document this names (D2932).
func TestPlanSectionDocumentPath(t *testing.T) {
	cases := map[string]string{
		"docs/plans/2026-01-02-the-plan.graph.json":                    "docs/plans/2026-01-02-the-plan.md",
		"docs/console-dependency-graph/implementation-plan.graph.json": "docs/console-dependency-graph/implementation-plan.md",
		"docs/foo.graph.json": "docs/foo.md",
	}
	for artifact, want := range cases {
		if got := DocumentPath(artifact); got != want {
			t.Errorf("DocumentPath(%q) = %q, want %q", artifact, got, want)
		}
		// The pairing round-trips: the document's own artifact is the path the
		// id was read from.
		if got := ArtifactPath(want); got != artifact {
			t.Errorf("ArtifactPath(%q) = %q, want %q", want, got, artifact)
		}
		if got := IDFromPath(artifact); got == "" {
			t.Errorf("IDFromPath(%q) named no plan", artifact)
		}
	}
}

// TestPlanBriefPinParsesAllFour: the pin a brief carries is four things, and
// the review reads all of them (D2932): the plan whose document to read by id,
// the node whose section, the commit to read it at, and the SHA-256 it has to
// hash to. The parser this replaced returned only the digest, which is enough
// for D2745's re-import and not for a reader that has no checkout of that
// commit.
func TestPlanBriefPinParsesAllFour(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	brief := "# Task: plans/2026-01-02-the-plan G1: the first task\n" +
		"Read your task from docs/plans/2026-01-02-the-plan/implementation-plan.md, section \"### G1:\", at commit 4f3c2b1a09, whose SHA-256 is " + digest + ": read the plan at that commit and refuse rather than work from a section whose hash differs.\n"
	pin, ok := BriefPin(brief)
	if !ok {
		t.Fatal("BriefPin did not read the brief an orchestrator writes")
	}
	want := Pin{Plan: "plans/2026-01-02-the-plan", Node: "G1", Commit: "4f3c2b1a09", Digest: digest}
	if pin != want {
		t.Fatalf("BriefPin = %+v, want %+v", pin, want)
	}

	// A task a person wrote is not a pointer, and neither is a brief an
	// earlier release wrote before the section was hashed: both are read as
	// text by the review, and neither names a commit to read a plan at.
	for _, notAPointer := range []string{
		"# Task: tidy up the README\n\nDo the thing.\n",
		"# Task: plans/2026-01-02-the-plan G1: the first task\nRead your task from docs/plans/2026-01-02-the-plan/implementation-plan.md, section \"### G1:\".\n",
		"",
	} {
		if pin, ok := BriefPin(notAPointer); ok {
			t.Errorf("BriefPin(%q) = %+v, and it is not a pointer", notAPointer, pin)
		}
	}
}
