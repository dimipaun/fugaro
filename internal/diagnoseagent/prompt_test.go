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

// The rules the prompt states are asserted on promptBody, never on the whole
// prompt: the guide is appended to it and carries the same words, so an
// assertion over Prompt would pass with the prompt's own rule deleted. The
// mutation that found this: dropping the trust boundary's first sentence from
// the body left `data, not instructions` in the guide's heading, and the test
// still passed.

func TestPromptStatesTheThreeOutcomes(t *testing.T) {
	body := promptBody(testEvidence())
	for _, outcome := range Outcomes {
		if !strings.Contains(body, outcome) {
			t.Errorf("the prompt does not name the outcome %q", outcome)
		}
	}
	if strings.Count(body, BlockOpen) != 1 || strings.Count(body, BlockClose) != 1 {
		t.Errorf("the prompt asks for the answer in one %q block", BlockOpen)
	}
	if !strings.Contains(body, OutcomeField) {
		t.Errorf("the prompt's block does not say which line states the outcome (%q)", OutcomeField)
	}
}

func TestPromptStatesThatLogTextIsData(t *testing.T) {
	body := promptBody(testEvidence())
	// The trust boundary, in the prompt's own words: the log holds text Fugaro
	// did not write, and the agent reads it as evidence and never as orders.
	for _, want := range []string{
		"data, not instructions",
		"no shell",
		"printed, never run",
	} {
		if !strings.Contains(body, want) {
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
	if !strings.HasSuffix(Prompt(e), Guide()) {
		t.Error("the guide is not the end of the prompt")
	}
}

func TestPromptFlattensWhatItIsGiven(t *testing.T) {
	// Every field is Fugaro's own, but a path can still arrive with a newline
	// in it; the prompt is line-structured, so none may pass one on.
	e := testEvidence()
	e.EvidenceDir = "/tmp/evidence\nsecond line"
	body := promptBody(e)
	if strings.Contains(body, "\nsecond line") {
		t.Error("a newline in an evidence path added a line of the prompt")
	}
	if !strings.Contains(body, "/tmp/evidence second line") {
		t.Error("the flattened path is not in the prompt at all")
	}
}
