package projectpolicy

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// migrate_off_test.go is the migration's one step that runs AT a version rather
// than across two (migrateRemovedMode, AB8a): a published budget mode that says
// the word the vocabulary dropped (D3009) is rewritten to observe in place, the
// node keeps its version (R4, D3025), and the `fugaro update` that runs it is
// the one command the upgrade is (CLAUDE.md).
//
// Every node here is one an installation can hold: a version-10 node an older
// CLI published, or one an administrator edited by hand, and — one version
// behind — a node the walk carries over before the same step reads it.

// offNode is such a node, written with the spacing and the members a published
// node carries: the block no release of this CLI models is there so the step's
// byte-for-byte promise can be pinned rather than argued.
const offNode = `{"version": 10, "budget": {"mode": "off", "per_run_usd": 5}, "allow_repos": ["github/owner/repo-a"], "future_release_block": {"escaped": "a & b <c>"}}`

// TestThePolicyMigrationRewritesOffInPlace: a published node that says the word
// is the node of F22 — the migration used to key on the version, so nothing
// reached a node already at version 10, and a node that says the word is one no
// reader of this release serves. The step rewrites it in place: the version
// stays where it is (D3025 is that no reader of version 10 breaks and no image
// is rebuilt for it), the word becomes the mode a reader serves, and every
// other byte of the owner's document survives.
func TestThePolicyMigrationRewritesOffInPlace(t *testing.T) {
	raw := []byte(offNode)

	migrated, changes, err := Migrate(raw)
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want the one step that rewrites the word", changes)
	}
	if ch := changes[0]; ch.From != Version || ch.To != Version {
		t.Errorf("the step is reported as %d to %d, want it run at version %d: a node already at this version may not be reported as a version move (R4, D3025)", ch.From, ch.To, Version)
	}
	if ch := changes[0]; !strings.Contains(ch.Changed, policy.ModeObserve) {
		t.Errorf("the step's diff line %q does not name the mode the node now says", ch.Changed)
	}

	// In place means in place: the node with the one member rewritten and
	// nothing else, which is what a caller diffs and what a step that decoded
	// the node and encoded it again could not promise (it would sort the keys,
	// compact the spacing and escape the unmodelled block's & and <).
	want := bytes.Replace(raw, []byte(`"mode": "off"`), []byte(`"mode": "observe"`), 1)
	if !bytes.Equal(migrated, want) {
		t.Errorf("the migrated node is not the node with its mode rewritten:\n got %s\nwant %s", migrated, want)
	}
	if !bytes.Contains(migrated, []byte(`"future_release_block": {"escaped": "a & b <c>"}`)) {
		t.Error("the migrated node does not carry the unmodelled block's own bytes, so the step re-encoded the document rather than editing it")
	}

	p, found, err := Decode(migrated)
	if err != nil || !found || p == nil {
		t.Fatalf("the migrated node does not decode: Decode = (%v, %v, %v)", p, found, err)
	}
	if p.Version != Version {
		t.Errorf("the migrated node is version %d, want %d", p.Version, Version)
	}
	if p.Budget.Mode != policy.ModeObserve {
		t.Errorf("the migrated node's budget mode is %q, want %q", p.Budget.Mode, policy.ModeObserve)
	}
	if p.Budget.PerRunUSD != 5 {
		t.Errorf("the migrated node's per-run ceiling is %v, want the node's own 5", p.Budget.PerRunUSD)
	}

	// A fixed point: migrating the node again is the no-op, which is what keeps
	// `fugaro update` from rewriting a node it has already converged on every
	// run of it.
	again, nochanges, err := Migrate(migrated)
	if err != nil {
		t.Fatalf("Migrate(the migrated node) = %v", err)
	}
	if len(nochanges) != 0 {
		t.Errorf("Migrate(the migrated node) reported %+v, want no changes", nochanges)
	}
	if !bytes.Equal(again, migrated) {
		t.Errorf("Migrate(the migrated node) = %s, want it back byte for byte", again)
	}
}

// TestAMigratedNodeIsAValidVersion10Node: what R4 and D3025 buy is that the
// step is not a bump — a node it writes is a version-10 node a version-10
// reader serves, whether the node was already at version 10 or the walk
// carried it there first. A step that wrote any other number would leave the
// release's own reader refusing the node it had just written.
func TestAMigratedNodeIsAValidVersion10Node(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"a node already at this version", offNode},
		{"a node one version behind", `{"version": 9, "budget": {"mode": "off", "per_run_usd": 5}, "allow_repos": ["github/owner/repo-a"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			migrated, changes, err := Migrate([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Migrate = %v", err)
			}
			p, found, err := Decode(migrated)
			if err != nil || !found || p == nil {
				t.Fatalf("the migrated node is not one this reader serves: Decode = (%v, %v, %v)", p, found, err)
			}
			if p.Version != Version {
				t.Errorf("the migrated node is version %d, want %d", p.Version, Version)
			}
			if p.Budget.Mode != policy.ModeObserve {
				t.Errorf("the migrated node's budget mode is %q, want %q", p.Budget.Mode, policy.ModeObserve)
			}
			if len(changes) == 0 {
				t.Fatal("the migration reported no step for a node that says the word")
			}
			if last := changes[len(changes)-1]; last.From != Version || last.To != Version {
				t.Errorf("the last step is reported as %d to %d, want the one that runs at version %d", last.From, last.To, Version)
			}
		})
	}
}

// TestAnOffNodeStillLoadsForTheMigration: the node the migration exists for is
// one the readers around it refuse — the publish door refuses the word (AB8a,
// internal/cli's TestPublishingTheOldWordIsRefused) and the node's decoder stops
// knowing it once validation moves (AB2's validBudget). The migration reads the
// mode out of the node's own bytes, before the reader is asked anything, so it
// loads the node the decoder refuses — and it is the only thing that does: no
// other reader of this package is asked to tolerate the word.
//
// The caps the block carries travel with it, because the step rewrites the mode
// member and nothing beside it: a migration that re-derived a budget block would
// be writing a policy the owner did not publish.
func TestAnOffNodeStillLoadsForTheMigration(t *testing.T) {
	raw := []byte(`{"version": 10, "budget": {"mode": "off", "per_run_usd": 5, "allowed_models": ["claude-sonnet-5"]}, "allow_repos": ["github/owner/repo-a"]}`)

	migrated, changes, err := Migrate(raw)
	if err != nil {
		t.Fatalf("the migration cannot load a node that says the word the vocabulary dropped (F22): %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want the one step that rewrites the word", changes)
	}
	p, found, err := Decode(migrated)
	if err != nil || !found || p == nil {
		t.Fatalf("the migrated node does not decode: Decode = (%v, %v, %v)", p, found, err)
	}
	if p.Budget.Mode != policy.ModeObserve {
		t.Errorf("the migrated node's budget mode is %q, want %q", p.Budget.Mode, policy.ModeObserve)
	}
	if p.Budget.PerRunUSD != 5 || p.Budget.MaxRunTokens != 0 {
		t.Errorf("the migrated node's budget block is %+v, want the node's own ceiling and no token cap the node did not state", p.Budget)
	}
	if !slices.Equal(p.Budget.AllowedModels, []string{"claude-sonnet-5"}) {
		t.Errorf("the migrated node's allowed_models = %v, want the list the node carried", p.Budget.AllowedModels)
	}
}
