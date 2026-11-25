package imagebake

import "strings"

// EntryOwn is the part of definition that the entry's own author wrote: the
// bytes before the first stage this package composes into it, which is the
// whole of a definition no build composed anything into.
//
// It exists for the readers that have to judge the entry's own text and not the
// engine's: the stages below are the engine's, they copy checkouts out of the
// image the clone step built and name no secret (BakedImage), and a guard that
// read the whole object would report a request the entry's author never made —
// the catalog's refusal of a definition that asks for a secret of its own
// (internal/catalog, B9, D2311) is exactly that reader. A guard over the composed
// bytes would refuse the nightly rebuild of every baked entry for the stage this
// package appended, which is the opposite of what it is for.
//
// The boundary is BakedSlug's own: a marker line that names a path under Root
// begins a stage of this package's, and a definition that spells anything else
// was not composed here, so its bytes are the entry's own. Append puts the
// stages after the entry's bytes, so the first such line begins the engine's
// part and everything before it is returned.
//
// A marker with nothing in front of it is not a boundary. Append composes a
// definition by appending to bytes it was given, and the catalog refuses an
// empty one before any build sees it (internal/catalog's bounded), so the
// definition's own author always wrote something under the first stage — a line
// a definition opens with is one the author wrote, whatever it says. Reading it
// as the boundary would answer with no bytes of the entry's at all, and the
// mount an author hid behind a copied marker line would be the one thing the
// readers here never see.
//
// What this cannot be is a fence. An author who spells a marker line after
// their own bytes, with a stage header under it, is not distinguishable from
// this package in the object alone — so what the callers do with the answer is
// a report, and the refusal that holds is the one the definition meets at
// publish (internal/catalog's refuseCredentialIn, which reads the whole text)
// and, since B8, a build step the catalog hands no secret of the entry's at
// all.
func EntryOwn(definition []byte) []byte {
	at := 0
	for line := range strings.Lines(string(definition)) {
		if _, ok := markerSlug(strings.TrimSuffix(line, "\n")); ok && at > 0 {
			return definition[:at]
		}
		at += len(line)
	}
	return definition
}
