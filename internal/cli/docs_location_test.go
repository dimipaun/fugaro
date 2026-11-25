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

// docBullet returns the one bullet of sec that opens with label, up to the
// next bullet or the end of the section, so a row's answer is read beside its
// own label and not from anywhere else in the prose: the Firestore and
// secret-container rows answer with the place's own default region, which the
// section's opening sentence also names, so a bare search for the answer would
// stay green with the row deleted.
func docBullet(sec, label string) string {
	i := strings.Index(sec, label)
	if i < 0 {
		return ""
	}
	rest := sec[i:]
	if j := strings.Index(rest, "\n- **"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func TestDocsSayWhereTheDataLives(t *testing.T) {
	const heading = "### Where an installation's data lives"
	for _, path := range []string{"SECURITY.md", "website/content/security.md"} {
		sec := docSection(t, path, readDoc(t, path), heading)
		for _, p := range []place.Place{place.US, place.EU} {
			m := place.For(place.DefaultRegion(p))
			rows := []struct{ label, where string }{
				{"**Firestore**", m.Firestore.Where},
				{"**The Realtime Database**", m.RealtimeDatabase.Where},
				{"**Secret containers**", m.Secrets.Where},
				{"**The run log bucket**", m.LogBucket.Where},
			}
			for _, row := range rows {
				bullet := docBullet(sec, row.label)
				if bullet == "" {
					t.Errorf("%s's %q section has no %s row: the section states the residency promise D3082 makes, row by row",
						path, heading, row.label)
					continue
				}
				if !strings.Contains(bullet, "`"+row.where+"`") {
					t.Errorf("%s's %q section's %s row does not name `%s`, where place.For(%q) places it; the answer belongs beside its own label, not elsewhere in the prose",
						path, heading, row.label, row.where, m.Region)
				}
			}
			if !strings.Contains(sec, "`"+m.Region+"`") {
				t.Errorf("%s's %q section does not name `%s`, the region place.For(%q) answers for and the one everything else is derived from",
					path, heading, m.Region, m.Region)
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
