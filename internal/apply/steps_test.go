package apply

import (
	"maps"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// TestStepsTableNamesOnlyKnownStages: every step the table declares has to be
// one this build can run — an initflow stage name, or CatalogBuild, the one
// non-stage step. A row naming a stage that does not exist (the invented
// `deploy` or `publish-policy` D1561 rules out) is a step no command could ever
// run and no reader could ever clear: the change would sit pending forever with
// nothing wrong on screen. The table is the only place a change's steps are
// declared, so this is the only place that can catch it.
//
// The second half is the order: initflow.Names is the linear extension of
// initflow.Needs, and a row whose ordered steps put a stage before one it needs
// would be a chain that runs in an order initflow itself would refuse — the
// policy stage's write is gated on the rules marker, the checkout's build on
// the layer, and so on. Order is what a row is read through, so it is what this
// checks. The third is that the row is what Steps answers for the change it is
// keyed by, and that a change nobody declared answers "not declared" rather
// than "nothing owed".
func TestStepsTableNamesOnlyKnownStages(t *testing.T) {
	rows := 0
	for _, resource := range slices.Sorted(maps.Keys(resourceSteps)) {
		rows++
		checkStepsRow(t, Change{Kind: Resource, Key: resource}, "resource "+resource, resourceSteps[resource])
	}
	for _, row := range settingPrefixSteps {
		rows++
		// The prefix is a key of its own family, which is what a change is
		// keyed by: `image_catalog.` stands for every `image_catalog.*` key.
		checkStepsRow(t, Change{Kind: Setting, Key: row.prefix}, "setting "+row.prefix, row.steps)
	}
	if rows == 0 {
		t.Fatal("the table declares no row at all: every change would answer \"nothing owed\"")
	}

	// A change the table does not know is a gap in it, and Steps has to say so
	// rather than answer with an empty list: a caller that read "no steps" as
	// "nothing owed" would save a change and never make it live.
	for _, c := range []Change{
		{Kind: Setting, Key: "no.such.key"},
		{Kind: Resource, Key: "no-such-resource"},
		{Kind: "no-such-kind", Key: "image"},
		{},
	} {
		if steps, ok := Steps(c); ok {
			t.Errorf("Steps(%+v) = %v, true, and the table declares nothing for it", c, steps)
		}
	}
}

// checkStepsRow is one row of the table, held to all three halves.
func checkStepsRow(t *testing.T, change Change, row string, steps []string) {
	t.Helper()
	if len(steps) == 0 {
		t.Errorf("%s: a declared row with no step: a change that owes nothing declares no row at all", row)
	}
	for _, step := range steps {
		if !KnownStep(step) {
			t.Errorf("%s: %q is not a step this build can run: the table may name an initflow stage (initflow.Names) or %s, and nothing else",
				row, step, CatalogBuild)
		}
	}
	ordered := Order(steps)
	if len(ordered) != len(steps) {
		t.Errorf("%s: %v orders to %v, which holds a step twice: the table may not declare one step twice", row, steps, ordered)
	}
	// The one step that is not a stage sorts ahead of every stage: the digest
	// the catalog build produces is what the repository stage's redeploy pins,
	// so a chain that deployed first would pin an image that does not exist yet.
	if i := slices.Index(ordered, CatalogBuild); i > 0 {
		t.Errorf("%s: %v runs %s at %d, after a stage: the build runs before every stage", row, ordered, CatalogBuild, i)
	}
	for i, step := range ordered {
		for _, need := range initflow.Needs(step) {
			if j := slices.Index(ordered, need); j > i {
				t.Errorf("%s: %v runs %s at %d and %s, which it needs, at %d: initflow's order is what a row is read through",
					row, ordered, need, j, step, i)
			}
		}
	}
	got, ok := Steps(change)
	if !ok {
		t.Fatalf("%s: Steps(%+v) declares nothing, and the table declares %v", row, change, steps)
	}
	if !slices.Equal(got, ordered) {
		t.Errorf("%s: Steps(%+v) = %v, want %v", row, change, got, ordered)
	}
}
