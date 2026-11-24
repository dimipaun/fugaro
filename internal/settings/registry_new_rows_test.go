package settings

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// registryFieldExemptions are the config fields a walk of the Go types finds and
// the registry deliberately has no row for, each with why. A row is what the
// read prints and what the write refuses against, so a field left out is a key
// nobody can read or write, and the reason has to be one of two: no key of that
// spelling exists, because the loader refuses it in every layer that could state
// it, or the key belongs to a command outside the settings surface, which the
// settings registry neither reads nor writes.
var registryFieldExemptions = map[string]string{
	"ab.challengers":         "the project layer's alone (validateRepoAB refuses every leaf but rate in a repository's file), so the key a person can set is layer.ab.challengers and no repository has this one",
	"ab.exchange_rate":       "the project layer's alone (validateRepoAB), so the key a person can set is layer.ab.exchange_rate",
	"ab.judge_model":         "the project layer's alone (validateRepoAB), so the key a person can set is layer.ab.judge_model",
	"ab.max_pairs_per_day":   "the project layer's alone (validateRepoAB), so the key a person can set is layer.ab.max_pairs_per_day",
	"ab.repos":               "the project layer's alone (validateRepoAB refuses it in a repository's file, D3297), so the key a person can set is layer.ab.repos",
	"workflows.*.image_name": "the image catalog's key: `fugaro image promote` writes it into the checkout's fugaro.yaml (setWorkflowImageName), and no settings command reads or writes it — the configuration audit's C-not-S list carries it for that reason, and R1's row list does not name it",
}

// registryHasAnAncestorRow is the registry's own rule for the leaves inside a
// block it names: `governance.tiers.*` is the row the read answers and the write
// lands on for `governance.tiers.conformance.max_usd`, and ScopeOf resolves a
// path the same way. The walk yields those leaves because it reads the Go
// structs one field at a time, so a leaf whose key the registry states as part
// of a block is covered and not a gap.
func registryHasAnAncestorRow(have map[string]bool, path string) bool {
	for key := range have {
		if strings.HasPrefix(path, key+".") {
			return true
		}
	}
	return false
}

// TestRegistryHasARowForEveryRealConfigField is R1's completeness guard on the
// three documents' own Go types (docs/config-ownership/implementation-plan.md
// §3 R1): every leaf a struct can write is a key the read answers for and the
// write refuses against. It is the walk TestRegistryCoversEveryConfigField makes
// of the project layer and the local config, extended to `config.Config` — the
// repository's `fugaro.yaml`, which that test reached only through config.Scopes,
// so a field the struct carries and that table does not was a key the read
// omitted in silence. It fails on repo `ab.rate` before R1 adds the row, and on
// `workflows.*.needs` beside it.
//
// A walk of the types is the sharper half of the pair: config.Scopes is a table a
// person keeps in step, and a field added to a struct is a key the file accepts
// whether or not anyone remembers. The walk reads the yaml tags, so a field the
// file cannot spell (a `yaml:"-"` or an untagged one) is no key and is skipped.
func TestRegistryHasARowForEveryRealConfigField(t *testing.T) {
	have := map[string]bool{}
	for _, r := range Rows() {
		have[r.Key] = true
	}
	for _, doc := range []struct {
		name  string
		paths []string
	}{
		{"fugaro.yaml (config.Config)", yamlPaths(reflect.TypeOf(config.Config{}), "")},
		{"the project layer (config.ProjectLayer)", yamlPaths(reflect.TypeOf(config.ProjectLayer{}), "layer")},
		{"the local config (localcfg.Config)", yamlPaths(reflect.TypeOf(localcfg.Config{}), "")},
	} {
		for _, path := range doc.paths {
			if have[path] || registryHasAnAncestorRow(have, path) {
				continue
			}
			if _, ok := registryFieldExemptions[path]; ok {
				continue
			}
			t.Errorf("%s: %s is a field the document carries and the registry has no row for, so the read omits the key and the write refuses a spelling the file accepts", doc.name, path)
		}
	}
	// The exemption list only shrinks, and an entry that names a key the registry
	// now holds is not an exemption at all: the row exists, so the reason beside
	// it is a sentence about a spelling nobody needed excused.
	for key, why := range registryFieldExemptions {
		if have[key] {
			t.Errorf("%s: the registry has a row for it, so the exemption (%s) is stale", key, why)
		}
	}
}
