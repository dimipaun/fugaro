package cli

import (
	"strings"
	"testing"
)

// The managed-entry claims used to be pinned on docs/gcp-setup.md's "What makes
// a project managed: two marks, not one" section. That page is the short setup
// path now (this review's finding: it carries the fresh-setup path only), and
// the entry points moved with their gate note into the setup skill's reference —
// what a user's agent reads before turning managed projects on — while the two
// commands that write the marks are written down in the design of record. The
// claims have been rewritten twice already, so they stay pinned where they live
// now.
func TestDocsGCPManagedLabelSaysItsClaims(t *testing.T) {
	const notes = "plugin/skills/setup/reference/managed.md"
	skill := readDoc(t, notes)
	for _, want := range []string{
		// Two marks, both required, and the label can only narrow.
		"**What makes a project managed.** Two marks, and both are required",
		// The writer a project that already exists has, which is not the
		// creating run: the flag writes both marks in the same command.
		"It writes the label `fugaro_managed=true` on the project as well, in the same command and the same moment",
		// The mode was off from 0.8.6 on, and 0.9.0 and 0.9.5 shipped with it
		// off, which is what makes a project marked then a project of another
		// mode.
		"0.8.6 through 0.9.5 refused every one of them with `managed projects are disabled in this release`",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("%s no longer says %q: the managed entry points and the gate they sit behind are what a user's agent reads there", notes, want)
		}
	}

	// The commands that write the marks, the backfill a project marked before
	// the label needs, and the fact that the backfill is unverified: all four
	// are the design of record's, and none of them is a command the agent runs.
	const design = "docs/design/managed-projects.md"
	doc := readDoc(t, design)
	for _, want := range []string{
		"fugaro managed flag",
		"`gcloud projects update <gcp-project> --update-labels fugaro_managed=true`",
		"`--remove-labels=fugaro_managed`",
		"UNVERIFIED",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer says %q: the label's writer and its backfill are owner-run commands, and the design of record is where they are written down", design, want)
		}
	}
}
