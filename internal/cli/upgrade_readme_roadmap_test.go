package cli

import (
	"os"
	"strings"
	"testing"
)

// TestReadmeRoadmapNamesSevenSkills: README.md's Roadmap (code review
// follow-up to upgrade task 9) credits the plugin skills the release carries,
// not the retired count of four from before the upgrade skill shipped, nor
// the five of the releases before the orchestrator skill (O10), nor the six
// of the releases before the images skill (IS12).
func TestReadmeRoadmapNamesSevenSkills(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, "the seven plugin skills") {
		t.Error("README.md's Roadmap never says \"the seven plugin skills\"")
	}
	for _, stale := range []string{"the four plugin skills", "the five plugin skills", "the six plugin skills"} {
		if strings.Contains(doc, stale) {
			t.Errorf("README.md's Roadmap still says %q", stale)
		}
	}
}
