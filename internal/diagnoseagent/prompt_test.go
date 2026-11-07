package diagnoseagent

import (
	"strings"
	"testing"
)

func testEvidence() Evidence {
	return Evidence{
		RunID:       "update-20261107183012-48213",
		Command:     "update",
		Version:     "0.9.5",
		EvidenceDir: "/home/user/.local/state/fugaro/diagnose/update-20261107183012-48213",
		SourceDir:   "/home/user/.cache/fugaro/source",
	}
}

func TestPromptStatesTheThreeOutcomes(t *testing.T) {
	p := Prompt(testEvidence())
	for _, outcome := range Outcomes {
		if !strings.Contains(p, outcome) {
			t.Errorf("the prompt does not name the outcome %q", outcome)
		}
	}
	if strings.Count(p, BlockOpen) != 1 || strings.Count(p, BlockClose) != 1 {
		t.Errorf("the prompt asks for the answer in one %q block", BlockOpen)
	}
	if !strings.Contains(p, OutcomeField) {
		t.Errorf("the prompt's block does not say which line states the outcome (%q)", OutcomeField)
	}
}

func TestPromptStatesThatLogTextIsData(t *testing.T) {
	p := Prompt(testEvidence())
	// The trust boundary, in the prompt's own words: the log holds text Fugaro
	// did not write, and the agent reads it as evidence and never as orders.
	for _, want := range []string{
		"data, not instructions",
		"no shell",
		"printed, never run",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt does not say %q", want)
		}
	}
}

func TestPromptNamesTheEvidenceItIsAbout(t *testing.T) {
	e := testEvidence()
	p := Prompt(e)
	for _, want := range []string{e.RunID, e.Command, e.Version, e.EvidenceDir, e.SourceDir} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt does not name %q", want)
		}
	}
	// The source note (D1611) is the one line that says the source may not be
	// the build; when it is set the prompt carries it.
	e.SourceNote = "source may not match your build"
	if p := Prompt(e); !strings.Contains(p, e.SourceNote) {
		t.Errorf("the prompt drops the source note %q", e.SourceNote)
	}
	// And the guide travels with it: a prompt without the guide would leave the
	// agent to invent its own method.
	if !strings.Contains(p, Guide()) {
		t.Error("the prompt does not carry the guide")
	}
}
