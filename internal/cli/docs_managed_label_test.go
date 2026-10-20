package cli

import (
	"os"
	"strings"
	"testing"
)

// The label section of docs/gcp-setup.md ("What makes a project managed: two
// marks, not one") carries claims a later merge can silently drop, and they
// have been rewritten twice already: what writes the label, what 0.8.6 does
// with the managed path, and what a project marked by 0.8.5 has to have done
// to it. This pins the three.
func TestDocsGCPManagedLabelSaysItsClaims(t *testing.T) {
	data, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start := strings.Index(doc, "## What makes a project managed: two marks, not one")
	if start < 0 {
		t.Fatal("docs/gcp-setup.md has no \"What makes a project managed\" section; update this test's marker")
	}
	section := doc[start:]
	if end := strings.Index(section[1:], "\n## "); end >= 0 {
		section = section[:end+1]
	}
	for what, want := range map[string]string{
		// The writer a project that already exists has, which is not the
		// creating run: the flag writes both marks in the same command.
		"the flag writes both marks": "`fugaro managed flag <gcp-project-id> --budget N --budget-currency CCC` for one that already exists",
		// 0.8.5's managed path is not this one: the mode is off from 0.8.6 on
		// and 0.9.0 still ships with it off, which is what makes a project
		// marked then a project of another mode (owner's ruling, round-2
		// review; the redesign moved to 0.9.6 by the owner's ruling of
		// 2026-10-19).
		"the managed path is off from 0.8.6 on": "managed mode is turned off from 0.8.6 on, and 0.9.0 still ships with it off",
		// And the release the redesigned path does ship in, which is not
		// 0.9.0: the owner's ruling of 2026-10-19 moved it to 0.9.6.
		"the redesign ships in 0.9.6": "the owner's ruling of 2026-10-19 moved to **0.9.6**",
		"all four ways in refuse":     "`fugaro init --create-project --managed`, `fugaro managed flag`, `fugaro managed apply` and `fugaro restore`, each before it reads or writes anything",
		// The flag ships in the 0.9.0 binary and refuses there (review M2).
		"the flag refuses in 0.9.0": "the 0.9.0 binary carries that command, and it refuses with `managed projects are disabled in this release` until then",
		// A creation without --managed writes the label too, on purpose —
		// once managed mode is on; in 0.9.0 it writes the fact alone (m1).
		"a plain creation labels the project": "A plain `fugaro init --create-project` (no `--managed`) writes the label as well once a release turns managed mode on",
		"a plain creation in 0.9.0":           "**In 0.9.0, with managed mode off, a plain `fugaro init --create-project` writes the creation fact alone and no label**",
		// And the backfill a project marked before the label needs, still
		// marked as unverified live.
		"the backfill line": "gcloud projects update <gcp-project> --update-labels fugaro_managed=true",
		// The other direction, which the flag's undo line prints and which no
		// test can run live either: the same command form, so the two texts
		// an operator copies are one text.
		"the label removal line": "gcloud projects update <gcp-project> --remove-labels=fugaro_managed",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the managed-label section does not say %s: no %q", what, want)
		}
	}
	if !strings.Contains(section, "UNVERIFIED") {
		t.Error("the managed-label section no longer marks the backfill unverified until it is run live")
	}
}
