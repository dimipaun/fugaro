package cli

import (
	"os"
	"strings"
	"testing"
)

// TestReadmeRoadmapNamesFiveSkills: README.md's Roadmap (code review
// follow-up to upgrade task 9) credits the five plugin skills, not the
// retired count of four from before the upgrade skill shipped.
func TestReadmeRoadmapNamesFiveSkills(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, "the five plugin skills") {
		t.Error("README.md's Roadmap never says \"the five plugin skills\"")
	}
	if strings.Contains(doc, "the four plugin skills") {
		t.Error("README.md's Roadmap still says \"the four plugin skills\"")
	}
}
