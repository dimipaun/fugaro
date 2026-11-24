// Package lift is the contract of the migration fugaro update runs over the
// repositories it can list (docs/repo-override-rulings/contract-migrate.md,
// the plan's C2): the shape of a repository's files (Repo), of what the
// project holds before the migration (Project), of what one migration item
// changes (Change), the signature every item's planner has (Planner) and the
// registry the planners hang on.
//
// It computes nothing itself: the planners (MC1, AB3, RG2, PR4, RC4, RT3) and
// the stage that runs them (M2) are written against this package.
package lift

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// Repo is one repository update could list, as its default branch stands: the
// bytes the reader got, or the one-line reason it could not read them. A
// repository it could not read stays in the slice so update names it (D3306);
// its Config and Recipes are nil, and no planner acts on it.
type Repo struct {
	// Repo is the repository as owner/name, the key the local config's repos
	// and the published admits spell it with.
	Repo string
	// Provider is the git provider kind (github or bitbucket), part of the
	// repository's storage slug (task.Slug).
	Provider string
	// Branch is the repository's default branch: the branch these reads were
	// made at, and the branch release B refuses a lifted key on.
	Branch string
	// Config is the default branch's fugaro.yaml bytes; nil when the
	// repository holds none.
	Config []byte
	// Recipes are the default branch's .fugaro/recipes/ files.
	Recipes []RecipeFile
	// Unreadable is why update could not read this repository, in one line;
	// "" when it read it.
	Unreadable string
}

// RecipeFile is one file of a repository's .fugaro/recipes/ directory. Path
// is the file's name within that directory, as a tree read returns it.
type RecipeFile struct {
	Path string
	Data []byte
}

// Project is what the project holds before the migration: the local layer
// file, the recipe store, the cap nodes, the price table, and which
// repositories already have a migration record.
type Project struct {
	// Layer is the local project layer file's tree (docs/layered-config):
	// the decoded YAML of localcfg.LayerFile, which a planner reads to find
	// the value the project already sets, and which update edits by path and
	// the Layer stage publishes.
	Layer map[string]any
	// Recipes are the project store's recipe names and the hex sha256 of
	// each object's bytes, which is how a planner tells a clash from a copy
	// of the same text (D3301).
	Recipes map[string]string
	// Caps are the budget database's cap nodes (RC1, RC4).
	Caps Caps
	// Prices is the model price table, which the model choice's planner
	// breaks a tie with (D3290).
	Prices *pricing.Table
	// Migrated is the set of slugs whose migration record already exists
	// (D3305). A planner acts only on a repository absent from it.
	Migrated map[string]bool
}

// Caps is the budget database's cap nodes as update reads them, the shape the
// CLI reads config/caps into: the global and default caps a repository's
// ceiling is read from, and each repository's own node by slug.
type Caps struct {
	Global   *budget.GlobalCaps
	Defaults *budget.DefaultCaps
	Repos    map[string]budget.RepoCaps
}

// Change is one migration item's whole effect on the project. Nothing a
// planner does happens anywhere but here: update prints every line of every
// change before it applies any write, and writes nothing else.
type Change struct {
	// LayerEdits are writes into the layer file's tree, by key path from its
	// root, one segment per key (defaults, agent, model).
	LayerEdits []LayerEdit
	// StoreWrites are objects the runs bucket gains, by key
	// (recipe.ObjectKey).
	StoreWrites []StoreWrite
	// DBWrites are budget database nodes, by path (budget.PathCapsRepo).
	DBWrites []DBWrite
	// Records are the slugs whose migration record update writes at
	// RecordPath once this change's writes have landed (D3305).
	Records []string
	// Lines are what update prints, one line each, before any of the writes
	// above: one line per repository whose runs change (the HARD RULE on
	// fugaro update).
	Lines []string
}

// LayerEdit is one write into the layer file's tree, in the two shapes the layer
// file's own editor writes (yamledit): a scalar at a path, and an item of the
// sequence at a path, addressed by one of that item's fields. A planner spells
// one of the two — Value or Item — and never a node the editor cannot write, so
// every edit a change carries is one M2 can apply.
//
// Path is the key path from the tree's root, one segment per key, which is the
// shape the editor takes and the only one that can name a repository: AB3's
// ab.repos.<repo> and RG2's defaults.rigor.repos.<repo> key on owner/name, and a
// repository name may hold a dot, so a dotted spelling would split it into two
// keys and write a node the layer's strict decode refuses.
type LayerEdit struct {
	Path []string
	// Value is the scalar a scalar write lands, written as the layer file's
	// schema reads it: a model, a rigor level, a rate ("0" is the number 0).
	Value string
	// Item is the item the sequence at Path gains, and the only shape a scalar
	// cannot express: RG2's project area, whose level is a scalar and whose
	// members are a sequence of mappings. An item the sequence already holds
	// under KeyField's value is written where it stands, never doubled, so a
	// second update converges.
	Item *LayerItem
}

// LayerItem is one item of the sequence a layer edit writes into, its fields in
// the shapes the layer's schema gives the block's: the field the item is
// addressed by, its scalars, its lists of scalars, and its lists of items.
type LayerItem struct {
	// KeyField is the field the item's sequence is addressed by — a project
	// area keys on name, a member on repo — and Key its value.
	KeyField string
	Key      string
	// Fields are the item's scalar fields, by name.
	Fields map[string]string
	// Lists are the item's fields whose value is a sequence of scalars (a
	// member's paths).
	Lists map[string][]string
	// Items are the item's fields whose value is a sequence of mappings (a
	// project area's members), each written the way this item is.
	Items map[string][]LayerItem
}

// StoreWrite is one object the runs bucket gains.
type StoreWrite struct {
	Key  string
	Data []byte
}

// DBWrite is one budget database node.
type DBWrite struct {
	Path  string
	Value any
}

// Empty reports whether the change carries nothing to write, record or print:
// what a stub planner answers, and what a second update finds (D3305).
func (c Change) Empty() bool {
	return len(c.LayerEdits) == 0 && len(c.StoreWrites) == 0 && len(c.DBWrites) == 0 &&
		len(c.Records) == 0 && len(c.Lines) == 0
}

// Planner computes one migration item's change over the repositories update
// could list. It is pure: it reads the project and the repositories and
// answers a Change, and it never writes, prints or asks anything (a change is
// applied by update, never by the planner that named it).
type Planner func(Project, []Repo) (Change, error)

// Source reads the repositories update can migrate: each one's default
// branch, and every admit pattern it could not expand into names (D3306).
// The real list's reader (M1's repofiles.Read) is adapted to it.
type Source func(ctx context.Context) ([]Repo, []string, error)

// RecordPath is where update records one repository's migration: a JSON
// object in the runs bucket, keyed by the repository's slug (D3305). Update
// writes it after that repository's writes have landed, and the launcher
// reads it to tell a migrated repository from one update could not list
// (D3308); it is the one name both sides use.
func RecordPath(slug string) string { return "fugaro/lift/l8/" + slug + ".json" }

// The migration items, by the name each item's planner is registered under.
// One planner per item: I2's registry.go registers the real one (MC1, AB3,
// RG2, PR4, RC4, RT3) under each of these names.
const (
	PlannerModels  = "models"  // MC1: the model choice to the project (B3)
	PlannerAB      = "ab"      // AB3: ab.rate to ab.repos (B9)
	PlannerRigor   = "rigor"   // RG2: rigor.default and rigor.areas to the project (B11)
	PlannerRecipes = "recipes" // PR4: .fugaro/recipes/ to the project store (B16)
	PlannerCaps    = "caps"    // RC4: a repository cap above its ceiling (B18)
	PlannerTighten = "tighten" // RT3: the values B4's rule drops, named (B4)
)

// NamedPlanner is a registered planner and the item it computes. Update runs
// them in name order, so the lines it prints do not move between runs.
type NamedPlanner struct {
	Name string
	Plan Planner
}

var (
	plannerMu sync.RWMutex
	planners  = map[string]Planner{}
)

// Register adds p as the planner of the migration item named. Registration
// happens in a package init, where an error is a programming error rather
// than a user's mistake, so the caller panics on it (as the settings registry
// does): two planners under one name is a diff to settle, not a registration
// to win.
func Register(name string, p Planner) error {
	if name == "" {
		return errors.New("lift: a planner is registered under a migration item's name, and this one is empty")
	}
	if p == nil {
		return fmt.Errorf("lift: the planner of %s is nil", name)
	}
	plannerMu.Lock()
	defer plannerMu.Unlock()
	if _, dup := planners[name]; dup {
		return fmt.Errorf("lift: %s already has a planner, and one item has one planner", name)
	}
	planners[name] = p
	return nil
}

// Planners returns every registered planner, ordered by name.
func Planners() []NamedPlanner {
	plannerMu.RLock()
	defer plannerMu.RUnlock()
	out := make([]NamedPlanner, 0, len(planners))
	for name, p := range planners {
		out = append(out, NamedPlanner{Name: name, Plan: p})
	}
	slices.SortFunc(out, func(a, b NamedPlanner) int { return strings.Compare(a.Name, b.Name) })
	return out
}
