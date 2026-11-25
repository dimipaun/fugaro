package cli

// LP11: the location preference's documents, held to the mapping.
//
// SECURITY.md's "Where an installation's data lives" states where a new
// installation places what it creates, and website/content/security.md is that
// document's generated copy, so the section is the same bytes in both.
//
// What the section says is the product's own table (internal/place), which is
// why the test reads it through place.For instead of repeating its rows: a row
// whose answer moves, or a section that stops naming one, fails here rather
// than shipping a document that describes a mapping the code no longer has.
// The two places are walked at their own defaults (place.DefaultRegion), which
// is what setup offers a US or EU installation with no region typed.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/place"
)

func TestDocsSayWhereTheDataLives(t *testing.T) {
	const heading = "### Where an installation's data lives"
	for _, path := range []string{"SECURITY.md", "website/content/security.md"} {
		sec := docSection(t, path, readDoc(t, path), heading)
		for _, p := range []place.Place{place.US, place.EU} {
			m := place.For(place.DefaultRegion(p))
			for _, where := range []string{
				m.Region, m.Firestore.Where, m.RealtimeDatabase.Where, m.Secrets.Where, m.LogBucket.Where,
			} {
				if !strings.Contains(sec, "`"+where+"`") {
					t.Errorf("%s's %q section does not name `%s`, where place.For(%q) places one of its rows; the section has to keep the mapping's own answers, and setup is where a reader meets them",
						path, heading, where, m.Region)
				}
			}
		}
	}

	// The question itself is docs/gcp-setup.md's: the wizard asks the place and
	// takes its region, so the page a first-time user follows names the two
	// places and the region each one's default is.
	page := readDoc(t, gcpSetupDoc)
	for _, p := range []place.Place{place.US, place.EU} {
		if !strings.Contains(page, "`"+place.DefaultRegion(p)+"`") {
			t.Errorf("%s does not name `%s`, the region `fugaro setup` derives for %s (place.DefaultRegion(%q)): the page describes the question the wizard asks",
				gcpSetupDoc, place.DefaultRegion(p), p, p)
		}
	}
}
