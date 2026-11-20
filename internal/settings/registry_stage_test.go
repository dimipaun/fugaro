package settings

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// registry_stage_test.go is W2's half of the registry's own ratchet: the
// governance block's leaves are the ones `config.Governance` declares, and each
// of them is a row a person can write — not a row the registry happens to have
// because someone listed it, and not the block its own name stands for.
//
// The walk is what makes it a ratchet rather than a list. `config.Governance`
// is the one place the block's shape is stated: the loader decodes it, the
// scope table holds its blocks, and a stage or a tier added to it would
// otherwise be a key a run reads and no row answers. Walking the Go type and
// asking the registry for the key the yaml tag spells means a field added
// without a row is red here, in the row `governance.<tag path>` — the spelling
// a person types, because the block is a top-level key of both documents
// (docs/project-layer.md).

// governanceLeaves is the dotted paths of config.Governance's own scalar fields
// below path, leaf keys spelled as their yaml tag says. config.Duration is a
// leaf: it is a scalar in the document whatever its Go shape.
func governanceLeaves(t reflect.Type, path string) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		key := name
		if path != "" {
			key = path + "." + name
		}
		if f.Type == reflect.TypeOf(config.Duration{}) || f.Type.Kind() < reflect.Struct {
			out = append(out, key)
			continue
		}
		if f.Type.Kind() == reflect.Struct {
			out = append(out, governanceLeaves(f.Type, key)...)
		}
	}
	return out
}

// TestRegistryCarriesEveryGovernanceLeaf: every leaf of config.Governance has a
// concrete row at the project and repository layers, which is the acceptance
// W2 is written against. A row missing here is a key the read cannot print and
// the write refuses with "no setting has that name", whatever the loader does
// with the file.
func TestRegistryCarriesEveryGovernanceLeaf(t *testing.T) {
	leaves := governanceLeaves(reflect.TypeOf(config.Governance{}), "")
	if len(leaves) < 20 {
		t.Fatalf("the walk of config.Governance yields %d leaves (%v): a walk that finds nothing proves nothing about the registry", len(leaves), leaves)
	}
	for _, leaf := range leaves {
		key := "governance." + leaf
		row, ok := Lookup(key)
		if !ok {
			t.Errorf("%s: no registry row, and config.Governance declares the key", key)
			continue
		}
		if !slices.Contains(row.Layers, LayerProject) || !slices.Contains(row.Layers, LayerRepo) {
			t.Errorf("%s: Layers = %v, want the project layer and the repository file: the block is a fugaro.yaml key and a top-level key of the layer file (whyGovernance)", key, row.Layers)
		}
		if row.Merge != Tighten {
			t.Errorf("%s: Merge = %v, want Tighten: the keys are guardrails Fugaro ships and a repository can only tighten them (G6)", key, row.Merge)
		}
		if row.Owner != "settings set" {
			t.Errorf("%s: Owner = %q, want the generic path: the block has no command family of its own (D2604)", key, row.Owner)
		}
		if row.Default.Unset == "" {
			t.Errorf("%s: Default = %+v, want the sentence an unset key reads as", key, row.Default)
		}
		if IsBlock(key) {
			t.Errorf("%s: the row is a block of settings, and the leaf is the setting: a write at the block's spelling has no value to land on", key)
		}
	}
}

// TestRegistryStageBlocksAreReadRowsOnly: the four blocks the scope table names
// — `governance.stage_budget`, `stage_tokens`, `stage_timeout` and `tiers` —
// stay rows of the read (so `settings --json`'s keyDefaults loses no member and
// a consumer reading the block still finds it) and are blocks rather than
// settings: each one holds the leaves above and has no value of its own. The
// write's half of this pair is TestNoBlockRowIsListedWritableThatCannotBeWritten
// (internal/cli), which is where the refusal lives.
func TestRegistryStageBlocksAreReadRowsOnly(t *testing.T) {
	for _, block := range []string{
		"governance.stage_budget", "governance.stage_tokens",
		"governance.stage_timeout", "governance.tiers",
	} {
		if _, ok := Lookup(block); !ok {
			t.Errorf("%s: no registry row, and the read prints the block a person reads the leaves under", block)
			continue
		}
		children := BlockChildren(block)
		if len(children) == 0 {
			t.Errorf("%s: no child rows, and the block's own name is not a setting: with no leaf under it the row is a key nothing can write", block)
			continue
		}
		for _, child := range children {
			// A tier's own name is a block itself (`governance.tiers.small`),
			// so the walk goes one level deeper rather than demanding a row the
			// block's own spelling cannot have.
			if IsBlock(child) {
				if len(BlockChildren(child)) == 0 {
					t.Errorf("%s: BlockChildren names %s, which holds nothing", block, child)
				}
				continue
			}
			leaf, ok := Lookup(child)
			if !ok {
				t.Errorf("%s: BlockChildren names %s, and no row carries it", block, child)
				continue
			}
			if !slices.Contains(leaf.Layers, LayerRepo) {
				t.Errorf("%s: Layers = %v, and the leaf is a repository's own fugaro.yaml key too", child, leaf.Layers)
			}
		}
	}
}

// TestRegistryGovernanceDocNamesTheEEBuild: the rows' Doc says the guardrails
// are enforced by a build with the `ee` tag (F12). It is the one fact about
// these rows a reader cannot get from the value: an untagged build validates
// the block and holds no run to it (internal/policy/hook.go, D522), and the
// released runner image is built with the tag. The suffix is asserted on every
// governance row rather than on a sample, so a row added without it is red
// here.
func TestRegistryGovernanceDocNamesTheEEBuild(t *testing.T) {
	count := 0
	for _, r := range Rows() {
		if !strings.HasPrefix(r.Key, "governance.") {
			continue
		}
		count++
		if !strings.Contains(r.Doc, "ee build") {
			t.Errorf("%s: Doc = %q, and a governance row's document names the build that enforces it (F12)", r.Key, r.Doc)
		}
	}
	if count < 20 {
		t.Fatalf("the registry holds %d governance rows: a walk that finds few proves nothing", count)
	}
}
