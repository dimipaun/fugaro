package pricing

// This file is the one part of the table contract that goes away on the pricing
// side: the spelling translation C1 ships, which answers every question "no
// spelling for that" until CS3 puts the Vertex spellings in canonical.go's
// checked-in map. I1 deletes this file and moves the two declarations below
// into canonical.go, where the real assignment lives, so the deletion leaves
// nothing behind (docs/model-credentials-routing/contract-table.md says which
// task moves what).
//
// What it is for is the contract arriving before its implementation: RT3's
// unmapped-spelling rule and CS1's chain walk are written against this
// signature, and it has to answer before CS3 teaches the map a Vertex spelling.
// The answer it gives in the meantime is the one that can never send a call to
// the wrong place — false, which a caller turns into "this model has no
// spelling for this source" and therefore never sends there — so a side written
// against C1 runs its own tests against a real signature and no call can pass
// on a spelling nothing derived.
//
// **It decides nothing.** It reads no map, no catalog and no price: both
// arguments are ignored and two different models get the same answer. That is
// the stub rule the other scaffolding in this tree follows (internal/credsvc's
// provider_wire_stub.go, internal/cli's after_write_stub.go).

// SpellingFunc is the signature of the one translation from the model an agent
// asked for to the spelling a source needs, and back:
//
//	(kind, model)  the model's spelling for a source of that kind, and whether
//	               there is one at all.
//
// kind is a routing.Kind's spelling — "anthropic", "provider", "vertex" — and
// it is a plain string here rather than routing.Kind on purpose: internal/routing
// is the package that reads this function (RT3's unmapped-spelling rule), so a
// parameter of that type would make the two packages import each other and the
// contract could not be compiled. The set of kinds is still closed, and
// routing.Kinds is still where it is written down.
//
// A caller must not fall back to the model it asked with when the answer is
// false: a source with no mapped spelling is a source that may not serve that
// model (D3175), and the map is checked in precisely so that nothing derives a
// spelling that "looks like" the one wanted.
type SpellingFunc func(kind, model string) (string, bool)

// SpellingFor answers the spelling a source of kind needs for model. The map
// behind it is CS3's: one row per canonical Claude id, the route's own id
// beside it (canonical.go), and the same table read in the other direction for
// a caller holding a spelling alone.
//
// It is a variable because the contract ships before the map: C1 declares it
// and this file's stub answers "no spelling" (below), CS3 assigns the real one
// from canonical.go, and I1 deletes the stub and moves these two declarations
// there. canonical.go sorts before this file, so the real assignment stands.
var SpellingFor SpellingFunc

// stubSpellingFor is the translation's body until CS3 supplies the real one: no
// spelling for anything, which is the answer that sends no call anywhere.
func stubSpellingFor(string, string) (string, bool) {
	return "", false
}

// The assignment stands only while nothing else has made one.
//
// The guard is not decoration, because the sides that fill this variable sort
// BEFORE this file — CS3 assigns it from canonical.go, and the toolchain
// presents a package's files in name order, so an unguarded init here would run
// last and put the stub back over the real map. Asking whether SpellingFor is
// still nil makes this file what it is meant to be whatever the real one is
// called: the answer that stands until a real one exists, and never one that
// stands over a real one.
func init() {
	if SpellingFor == nil {
		SpellingFor = stubSpellingFor
	}
}
