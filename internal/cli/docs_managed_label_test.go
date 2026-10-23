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
		"the flag writes both marks": "`fugaro managed flag <gcp-project-id> --budget N --budget-currency CCC` for a project `fugaro setup` created without `--managed`",
		// 0.8.5's managed path is not this one: the mode was off from 0.8.6
		// on, and 0.9.0 and 0.9.5 shipped with it off, which is what makes a
		// project marked then a project of another mode (owner's ruling,
		// round-2 review; the redesign moved to 0.9.5 by the owner's ruling of
		// 2026-10-19, and to 0.10.0 by the owner's ruling of 2026-10-20, which
		// is the release the owner's ruling of 2026-10-22 ships it in).
		"the managed path is off from 0.8.6 on": "managed mode was turned off from 0.8.6 on, and 0.9.0 and 0.9.5 shipped with it off",
		// And the release the redesigned path does ship in, which is
		// neither 0.9.0 nor 0.9.5: the owner's ruling of 2026-10-20 moved it to 0.10.0.
		"the redesign ships in 0.10.0": "the ruling of 2026-10-20 to 0.9.6 and then renumbered to **0.10.0**",
		"all four ways in refused":     "the creating run with `--create-project --managed`, `fugaro managed flag`, `fugaro managed apply` and `fugaro restore`, each before it read or wrote anything",
		// The flag ships in the 0.9.0 binary and refused there (review M2);
		// 0.10.0 is the release whose binary carries it live.
		"the flag refuses in 0.9.0": "the 0.9.0 binary carries that command and refuses with `managed projects are disabled in this release`",
		// A creation without --managed writes the label too, on purpose —
		// once managed mode is on; in 0.9.0 it writes the fact alone (m1).
		"a plain creation labels the project": "A plain first run of `fugaro setup` (no `--managed`) writes the label as well once a release turns managed mode on",
		"a plain creation with the mode off":  "**In a release with managed mode off (0.8.6 through 0.9.5), a plain creation (`--create-project`, no `--managed`) wrote the creation fact alone and no label**",
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
