package imagecheck

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
)

// CheckCatalog is the scheduled catalog check's decision pass (D1148): one
// DecideCatalog per buildable entry of cat, in the catalog's own order, over
// the inputs the caller read for it. It is the entry point the project-level
// scheduled job calls — the catalog's counterpart of the per-workflow loop in
// internal/cli's check job — and it makes no calls itself: the caller reads
// every entry's definition, record, base digest and previous check state
// first.
//
// An entry whose source is a prebuilt image (config.SourceImage) has nothing
// to build and is passed over. An entry with no inputs in in is an error
// rather than a skip: the check never decides about an entry it did not read.
func CheckCatalog(cat *config.Catalog, in map[string]CatalogInputs) ([]CatalogDecision, error) {
	if cat == nil {
		return nil, nil
	}
	var out []CatalogDecision
	for _, e := range cat.Entries {
		if e.Source.Kind == config.SourceImage {
			continue
		}
		entry, ok := in[e.Name]
		if !ok {
			return nil, fmt.Errorf("the catalog check has no inputs for entry %s", e.Name)
		}
		entry.Entry = e.Name
		out = append(out, DecideCatalog(entry))
	}
	return out, nil
}
