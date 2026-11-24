# The migration contract (C2)

Task **C2** of [implementation-plan.md](implementation-plan.md). What it fixes is the vocabulary of
the migration `fugaro update` runs over the repositories it can list: the shape of a repository's
default branch (**D3306**, **R12**), of what the project holds before the migration, and of what one
migration item changes — plus the rules every planner follows (a planner is pure and never writes;
it acts once per repository, **D3305**; it never overwrites a project value; it names, never applies,
a value a migrated repository still writes; and it names every repository whose runs change), the
registry the items hang on, and the one path a migration record lives at (**D3305**, **D3308**)
([reconciliation.md](reconciliation.md), [spec.md](spec.md)). The Go is `internal/lift/lift.go` (the
shapes, the registry and `RecordPath`) and `internal/lift/stubs.go` (the stub planners); the tests
are `internal/lift/stubs_test.go` (`TestStubPlannersChangeNothing`) and `internal/lift/lift_test.go`
(`TestRecordPath`).

**MC1**, **AB3**, **RG2**, **PR4**, **RC4** and **RT3** are the planners written against this
document; **M2** runs them, prints their lines and applies their changes; **M1**'s `repofiles.Read`
is adapted to `Source` by **I2**, which also deletes the stubs and registers the real planners; and
**PR1** reads `RecordPath` to tell a migrated repository from one `update` could not list (D3308).
A side that finds this contract wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it; the change the plan anticipates is **R-planner-change** (a store write or a database write that
needs more than `Change` carries).

## What the contract is

The migration is one `fugaro update` stage: it reads every repository it can list, asks each
registered planner for its change, prints every line of every change, and only then writes — the
layer file, the recipe store and the budget database — and records each repository it migrated.
This file is the names those sides have to agree on, and nothing that computes: no planner, no
reader, no store and no command lives in the contract.

| Name | What it is |
| --- | --- |
| `lift.Repo` | one repository `update` could list, as its default branch stands: `Repo` (owner/name), `Provider` (the git provider kind), `Branch` (the default branch), `Config` (its `fugaro.yaml` bytes), `Recipes` (its `.fugaro/recipes/` files) and `Unreadable` (why it could not be read, `""` when it could) |
| `lift.RecipeFile` | one file of `.fugaro/recipes/`: `Path` (its name in that directory) and `Data` |
| `lift.Project` | what the project holds before the migration: `Layer` (the local layer file's tree), `Recipes` (the store's names and sums), `Caps`, `Prices` and `Migrated` |
| `lift.Caps` | the budget database's cap nodes: `Global`, `Defaults`, and `Repos` — each repository's own `config/caps/repos/<slug>` node, by slug |
| `lift.Change` | one item's whole effect: `LayerEdits`, `StoreWrites`, `DBWrites`, `Records` and `Lines`; `Change.Empty()` says it carries nothing |
| `lift.LayerEdit` | one write into the layer file's tree: `Path` (the key path from the layer's root, one segment per key, so a repository name holding a dot stays one key) and `Value` |
| `lift.StoreWrite` | one object the runs bucket gains: `Key` (`recipe.ObjectKey`) and `Data` |
| `lift.DBWrite` | one budget database node: `Path` (`budget.PathCapsRepo`) and `Value` |
| `lift.Planner` | `func(Project, []Repo) (Change, error)` |
| `lift.Source` | `func(ctx context.Context) ([]Repo, []string, error)`: the repositories, and every admit pattern it could not expand (D3306) |
| `lift.RecordPath` | `fugaro/lift/l8/<slug>.json`, the one name M2 writes and PR1 reads (D3305, D3308) |
| `lift.PlannerModels`, `PlannerAB`, `PlannerRigor`, `PlannerRecipes`, `PlannerCaps`, `PlannerTighten` | the six items' names, `models`, `ab`, `rigor`, `recipes`, `caps` and `tighten` |
| `lift.Register`, `lift.Planners`, `lift.NamedPlanner` | the registry: `Register(name, Planner) error` in a package init, and `Planners()` returning every planner ordered by name |

**Where the values come from.** `Project` is M2's to fill: `Layer` is the decoded YAML of the local
layer file (`localcfg.LayerFile`, the file the owner edits and the Layer stage publishes),
`Recipes` is a listing of the project store (`recipe.ObjectKey`) with each object's hex sha256,
`Caps` is `config/caps` read the way `internal/cli/budget.go`'s `dbConfig` reads it, `Prices` is
`config.PriceTable`'s table (the one every surface prices a model with), and `Migrated` is the set
of slugs whose record exists in the runs bucket. `[]Repo` is `Source`'s: M1's `repofiles.Read`,
adapted by I2.

## A planner is pure, and never writes

A planner reads its two arguments and answers a `Change`. It opens no file, reads no bucket and no
database, makes no network call, writes nothing, prints nothing and asks nothing
([docs/no-prompt-policy](../no-prompt-policy/)): everything it decides is a value in the `Change`
it returns, and M2 — never the planner — is what applies it. That is what makes the stage's order
possible: every line of every change is printed before the first write, and a change a planner
answers is a change M2 can refuse to apply without having half-applied it.

The error a planner returns is for a change it cannot compute at all (an input it cannot read, a
value it cannot reconcile). It is not a refusal of one repository: a repository the migration
cannot move is named in `Lines` and left alone, and `update` still converges for the rest.

## Once per repository, then never again (D3305)

A repository is migrated once. `Project.Migrated` is the set of slugs whose record
(`RecordPath`) already exists, and **a planner acts only on a repository absent from it**: where
`Migrated` holds a repository's slug, the planner writes nothing for that repository, publishes
nothing of it and records nothing for it. M2 writes the record after that repository's writes have
landed, so a repository whose layer publish was refused or skipped is migrated again by the next
`update` rather than being lost.

The one thing a planner still does for a repository already migrated is **name** it: a value its
default branch still writes, which the item has already moved to the project, is one of the change's
`Lines` and never a write. A person reads the line, deletes the key from the repository's file, and
`update` never touches the file itself (it cannot commit to a repository).

## Never overwrite a project value

Where the project already sets the value an item would write — `Project.Layer` sets the key, or
`Project.Recipes` holds the recipe name, or a cap node is written — **the project's value stands and
the planner writes nothing for it**. It names the repository whose file disagrees instead (MC1's own
line, "acme/app: coder claude-x -> claude-y (the project's)"). The rule is what makes a later
admin's edit survive: a value a person set in the project layer after a migration is never replaced
by a repository's file at the next `update`, and a second `update` finds `Migrated` and changes
nothing at all.

## Every repository whose runs change is named

`Change.Lines` is what `update` prints, one line each, **before any of the change's writes** — the
HARD RULE on `fugaro update` ("prints before any write one line per repository whose runs change").
A planner that changes what a repository's runs do writes one line per such repository, and a
planner that only reads (RT3) writes its lines and no writes at all. A change with nothing in it is
`Empty()`, which is what a stub planner answers and what a converged project's second `update`
finds.

## The list, and the repository it cannot read (D3306)

`Source` answers the repositories `update` can list — the local config's repositories and every
concrete owner/name in the published policy's admits — each as a `Repo`, and, beside them, **every
admit pattern it could not expand into names** (`*`, `github/acme/*`). M2 prints each unexpanded
pattern, and each repository whose `Unreadable` is set, in its own line before any write.

A repository the reader could not read stays in the slice with its `Unreadable` set and no bytes: it
is named, no planner acts on it, and its lifted keys are **never moved** — the project's values
stand alone for it, which is D3306's fail-open answer. The one case that is refused rather than
accepted is the recipe one D3308 closes, and it is the launcher's refusal (PR1), not this stage's.

## The migration record's path (D3305, D3308)

`RecordPath(slug)` is `fugaro/lift/l8/<slug>.json`: the object `update` writes in the runs bucket
after a repository's migration has landed (D3305), and the object the launcher reads to decide
whether a repository is migrated (D3308). It is **one name on both sides**, so a move of it without
the other would leave a repository `update` has migrated looking unmigrated to the launcher — which
is why `TestRecordPath` pins it. The record's own contents (the release and the time) are M2's; what
this contract fixes is where it lives and that its absence means "not migrated".

## The registry, the six items, and the stubs

`Register` adds a planner under one of the six item names, in a package `init` that panics on its
error (a second planner under one name is a diff to settle, not a registration to win);
`Planners()` answers every registered planner **ordered by name**, so the lines `update` prints do
not move between runs.

| Item | Name | Planner | What it migrates |
| --- | --- | --- | --- |
| B3 | `models` | MC1 | `agent.model`, `agent.models.*`, `agent.crafts.*.model` and `agent.levels.lN.model` to the project layer |
| B9 | `ab` | AB3 | `ab.rate: 0` to `ab.repos.<repo>` |
| B11 | `rigor` | RG2 | `rigor.default` and `rigor.areas` to `rigor.repos` and member areas |
| B16 | `recipes` | PR4 | `.fugaro/recipes/*.yaml` to the project store, renaming a clash |
| B18 | `caps` | RC4 | a `config/caps/repos/<slug>` node above its ceiling, lowered |
| B4 | `tighten` | RT3 | nothing: it names the values B4's rule drops (R20) |

Until the planners land, `internal/lift/stubs.go` registers `stubPlanner` under all six names: it
answers `Change{}` and no error, so merging the contract alone moves nothing and
`TestStubPlannersChangeNothing` holds every one of them to an empty change. **I2 deletes
`stubs.go`** (and its test with it) and registers MC1, AB3, RG2, PR4, RC4 and RT3 under the same six
names in `registry.go`, wiring M1 as the `Source` — that is the change that makes the stage migrate
anything, and it is why a stub that decided something would be a defect rather than a shortcut.
