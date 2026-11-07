// Package apply holds what a management command owes after it has made a
// change, and where this machine records it (docs/composite-commands, D1561,
// D1562, D1570).
//
// Two things live here and nothing else. steps.go is the one table from a
// change — a setting key or a resource type — to the steps that make it live:
// the initflow stage names plus the one step that is not a stage, a catalog
// image's build. The order and the dedupe are initflow's (Names, Needs); this
// package keeps no second list of stages, so a stage renamed or reordered
// there moves here with no edit. pending.go is the local document beside the
// project config that records the steps still owed, bound to the project and
// the GCP project ID and holding no value of anything.
//
// Nothing here reads the cloud, the config files or the command line: a Change
// is a value, and the pending document is a file the caller names a project
// for. Running the steps is `fugaro apply`'s work.
package apply

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// CatalogBuild is the one step of the table that is not an initflow stage: the
// Cloud Build of a catalog image (`fugaro image build <name>`), which is
// billable and so leased from the run's budget (F2, D1543). `fugaro update`
// never runs it (D1577): its images stage mirrors and pins the base images and
// builds no catalog image, which is why a catalog entry's steps can never be
// answered by "run fugaro update".
const CatalogBuild = "catalog-build"

// Kind is what a Change is keyed by.
type Kind string

const (
	// Setting is a key of the settings registry (internal/settings): the
	// change is what `fugaro settings set|unset` wrote.
	Setting Kind = "setting"
	// Resource is a resource type a management command manages.
	Resource Kind = "resource"
)

// The resource types the table declares. A resource type is the subject a
// command acts on and not the command: `fugaro image add` and `fugaro image rm`
// both change the catalog and owe the same steps.
const (
	// ResourceImage is the project's image catalog (internal/cli/
	// image_catalog.go).
	ResourceImage = "image"
	// ResourceRepo is a repository of the project (internal/cli/
	// project_enroll.go): onboarding one is what the repository stage's own
	// plan is made from.
	ResourceRepo = "repo"
	// ResourceWorkspace is the workspace this project is the owner of
	// (internal/cli/workspace.go, D1292).
	ResourceWorkspace = "workspace"
)

// Change is one change a management command made: what the table is keyed by.
// It carries the change's subject and never its value — a value belongs in the
// file the command wrote, and the pending document may not become a second
// copy of the config (D1570).
type Change struct {
	// Kind says what Key names: a setting key or a resource type.
	Kind Kind `json:"kind"`
	// Key is the setting key as `fugaro settings` spells it, or one of the
	// resource type constants above.
	Key string `json:"key"`
}

// catalogSteps is what a catalog entry owes (F2): the build that makes the
// image exist, the repository stage, whose redeploy pins the digest that build
// produced, and the policy stage, which publishes `image_catalog` into the node
// every run of the project is admitted by. Both the image resource type and
// every `image_catalog.*` key owe it, which is why it is written once.
var catalogSteps = []string{CatalogBuild, initflow.Repository, initflow.Policy}

// resourceSteps is the resource-type half of the table.
var resourceSteps = map[string][]string{
	ResourceImage: catalogSteps,
	// Onboarding a repository is what the repository stage's plan is made
	// from: its Terraform root is per repository, and so is the redeploy
	// (F15, D1574).
	ResourceRepo: []string{initflow.Repository},
	// The workspace stage is the owner's own published record, built from the
	// `workspace_members` and `workspace_cap` this installation declares
	// (initflow.Workspace's own comment).
	ResourceWorkspace: []string{initflow.Workspace},
}

// settingPrefixSteps is the setting-key half of the table. A key is matched by
// prefix and the prefixes do not overlap: a registry key is a dotted path, and
// a family of keys is its leading members. A key that matches no row is a gap
// in the table and not an answer (Steps says so), because every setting a
// command writes has to declare what it affects (spec rule 2, D1561).
var settingPrefixSteps = []struct {
	prefix string
	steps  []string
}{
	// The catalog's keys are the local config's (internal/settings, the
	// `image_catalog.entries.*` rows), and they owe what the image resource
	// type owes: the build, the redeploy and the publish.
	{"image_catalog.", catalogSteps},
	// The project layer's keys are published by the layer stage, which is the
	// only writer of that file's cloud copy (D830).
	{"layer.", []string{initflow.Layer}},
	// The four workspace families — `workspace`, `workspace_pin.*`,
	// `workspace_members.*` and `workspace_cap.*` — are the owner's
	// declaration, which the workspace stage publishes (D1292).
	{"workspace", []string{initflow.Workspace}},
}

// Steps answers the steps change owes, in the order initflow runs them, and
// whether the table declares anything for it at all. A change nobody declared
// answers false, which a caller must not read as "nothing owed": the table is
// the one place a change's steps are declared, so a key or a resource type with
// no row is a gap in it.
func Steps(change Change) ([]string, bool) {
	switch change.Kind {
	case Resource:
		steps, ok := resourceSteps[change.Key]
		if !ok {
			return nil, false
		}
		return Order(steps), true
	case Setting:
		for _, row := range settingPrefixSteps {
			if strings.HasPrefix(change.Key, row.prefix) {
				return Order(row.steps), true
			}
		}
		return nil, false
	}
	return nil, false
}

// Order returns names deduped and in the order initflow runs them: the stage
// names in initflow.Names order, which is the linear extension of
// initflow.Needs, so a stage is never asked to run before one it needs — and
// CatalogBuild ahead of every stage, because the digest it builds is what the
// repository stage's redeploy pins.
//
// A name that is neither a stage nor CatalogBuild is kept, last, rather than
// dropped: a caller that named something this build cannot run sees it in the
// list instead of silently running fewer steps than it asked for.
func Order(names []string) []string {
	left := make(map[string]bool, len(names))
	for _, n := range names {
		left[n] = true
	}
	out := make([]string, 0, len(left))
	if left[CatalogBuild] {
		out = append(out, CatalogBuild)
		delete(left, CatalogBuild)
	}
	for _, name := range initflow.Names() {
		if left[name] {
			out = append(out, name)
			delete(left, name)
		}
	}
	// Sorted rather than in the caller's order: what is left is a name this
	// build does not know, and one order for those is one answer.
	rest := make([]string, 0, len(left))
	for name := range left {
		rest = append(rest, name)
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// KnownStep reports whether name is a step this build can run: an initflow
// stage or CatalogBuild.
func KnownStep(name string) bool {
	return name == CatalogBuild || slices.Contains(initflow.Names(), name)
}
