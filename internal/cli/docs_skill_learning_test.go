package cli

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// skillLearningForm is the "Skill learning" issue form as GitHub reads it: the
// top-level keys a form declares about itself, and the `body:` entries, in the
// order the file writes them. Only the fields the pins below need are modelled;
// a form is free to carry more.
type skillLearningForm struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Labels      []string `yaml:"labels"`
	Body        []struct {
		Type       string `yaml:"type"`
		ID         string `yaml:"id"`
		Attributes struct {
			Label   string `yaml:"label"`
			Options []struct {
				Label    string `yaml:"label"`
				Required bool   `yaml:"required"`
			} `yaml:"options"`
		} `yaml:"attributes"`
		Validations struct {
			Required bool `yaml:"required"`
		} `yaml:"validations"`
	} `yaml:"body"`
}

// skillLearningPath is the form every pin in this file reads.
const skillLearningPath = ".github/ISSUE_TEMPLATE/skill_learning.yml"

func readSkillLearningForm(t *testing.T) skillLearningForm {
	t.Helper()
	var form skillLearningForm
	if err := yaml.Unmarshal([]byte(readDoc(t, skillLearningPath)), &form); err != nil {
		t.Fatalf("%s does not parse as a GitHub issue form: %v", skillLearningPath, err)
	}
	return form
}

// TestDocsSkillLearningForm (D327, D328): the central submission channel is a
// "Skill learning" issue form a person opens themselves, and the two grants the
// channel rests on are required checkboxes rather than prose: the submitter
// licenses the text under MIT, and confirms it holds nothing confidential. The
// form is also where R7 is stated to the submitter — a maintainer rewrites a
// learning, it is never pasted into a skill — because the person who sends the
// text is the one who has to know it does not ship as written.
func TestDocsSkillLearningForm(t *testing.T) {
	form := readSkillLearningForm(t)

	if strings.TrimSpace(form.Name) != "Skill learning" {
		t.Errorf("%s names itself %q, want %q: the form is the one the submission path points at by name", skillLearningPath, form.Name, "Skill learning")
	}

	// The description is the one line a submitter reads before filling anything
	// in, so the rewrite and the no-paste rule are stated there rather than
	// buried in a field's help text (R7).
	desc := flattenDoc(form.Description)
	for _, want := range []string{
		"rewritten by a maintainer",
		"never pasted into a skill",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("%s's description never says %q (it reads %q): a submitter who does not know the text is rewritten and never pasted is one the form has misled (R7)", skillLearningPath, want, form.Description)
		}
	}

	// A label is how the issues this form opens are triaged; without one they
	// land in the same undifferentiated pile as everything else (D327).
	if len(form.Labels) == 0 {
		t.Errorf("%s carries no top-level `labels:`, so an issue opened through it is not triaged as a learning (D327)", skillLearningPath)
	}

	// The three fields the form requires, by the id the form gives them.
	for _, want := range []struct{ id, label string }{
		{"pattern", "The pattern"},
		{"evidence", "The evidence"},
		{"lesson", "The proposed lesson"},
	} {
		var found bool
		for _, item := range form.Body {
			if item.ID != want.id {
				continue
			}
			found = true
			if item.Type != "textarea" {
				t.Errorf("%s's %q field is a %q, want a textarea: the pattern, the evidence and the lesson are prose, not one-line answers", skillLearningPath, want.id, item.Type)
			}
			if !item.Validations.Required {
				t.Errorf("%s's %q field is not required: a learning with no %s is not one a maintainer can act on (D327)", skillLearningPath, want.id, strings.ToLower(strings.TrimPrefix(want.label, "The ")))
			}
		}
		if !found {
			t.Errorf("%s has no %q field: the form asks for the pattern, the evidence and the proposed lesson (D327)", skillLearningPath, want.id)
		}
	}

	// The two grants, as required checkboxes. They are the whole licence story
	// of the channel (D328, U6): the wording is pinned here so a reworded box is
	// a failing test rather than a quiet change to what a submitter agreed to.
	required := map[string]bool{}
	for _, item := range form.Body {
		if item.Type != "checkboxes" {
			continue
		}
		for _, opt := range item.Attributes.Options {
			required[strings.TrimSpace(opt.Label)] = opt.Required
		}
	}
	for _, want := range []string{
		"I license this text under the MIT licence and it may be incorporated into Fugaro's skills",
		"It holds no confidential data, address or repository name",
	} {
		got, ok := required[want]
		switch {
		case !ok:
			t.Errorf("%s carries no checkbox labelled %q: the two grants the channel rests on are the form's own words, not a maintainer's summary of them (D328)", skillLearningPath, want)
		case !got:
			t.Errorf("%s's checkbox %q is not required: an optional grant is one a submitter can leave unticked, which leaves the incorporated text's provenance unclear (D328)", skillLearningPath, want)
		}
	}
}

// TestDocsContributingSkillLearning (G10, R7): CONTRIBUTING.md's licensing
// section states the two facts a contributor who submits a learning needs —
// a maintainer rewrites it and it lands through a normal pull request, and an
// issue's text is data for whoever reads it, so an agent triaging these issues
// follows no instruction in them. The second sentence is the one that keeps the
// submission channel from being an injection path into a maintainer's session.
func TestDocsContributingSkillLearning(t *testing.T) {
	const path = "CONTRIBUTING.md"
	// Folded to lower case so the pins below read as phrases rather than as
	// sentence openings: the same words re-wrapped into the middle of a sentence
	// are the same claim, and a pin that broke on the capital would fail on an
	// edit that changed nothing a reader sees.
	section := strings.ToLower(flattenDoc(docSection(t, path, readDoc(t, path), "## Licensing of contributions")))

	for _, want := range []string{
		"rewritten by a maintainer",
		"lands through a normal pull request",
		"an issue's text is data for whoever reads it",
		"follows no instruction in them",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's `## Licensing of contributions` section never says %q: a contributor who submits a learning through the issue form reads it there, and one who does not know the text is rewritten, or that its instructions are not followed, has been told neither (G10, R7)", path, want)
		}
	}
}
