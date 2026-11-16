package imagebake

import "strings"

// EntryOwn is the part of definition that the entry's own author wrote: the
// bytes before the first stage this package composes into it, which is the
// whole of a definition no build composed anything into.
//
// It exists for the readers that have to judge the entry's own text and not the
// engine's: the stages below are the engine's, they carry their own clone and
// their own secret mount (Stage, OtherStage), and a guard that read the whole
// object would report a request the entry's author never made — the catalog's
// refusal of a definition that asks for a secret of its own (internal/catalog,
// B9, D2311) is exactly that reader. A guard over the composed bytes would
// refuse the nightly rebuild of every baked entry for the stage this package
// appended, which is the opposite of what it is for.
//
// The boundary is BakedSlug's own: a marker line that names a path under Root
// begins a stage of this package's, and a definition that spells anything else
// was not composed here, so its bytes are the entry's own. Append puts the
// stages after the entry's bytes, so the first such line begins the engine's
// part and everything before it is returned.
func EntryOwn(definition []byte) []byte {
	at := 0
	for line := range strings.Lines(string(definition)) {
		rest, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), markerPrefix)
		if ok {
			if slug, under := strings.CutPrefix(rest, Root+"/"); under {
				if _, err := Path(slug); err == nil {
					return definition[:at]
				}
			}
		}
		at += len(line)
	}
	return definition
}
