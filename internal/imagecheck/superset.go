package imagecheck

import (
	"maps"
	"slices"
)

// This file is the superset lookup (design
// docs/forge-authorship-images/shared-images.md §4): the reuse of an image
// already in the lock for a repository whose set it holds in full. It is
// what stops a repository with one extra package paying for a build, and it
// is deliberately narrow — an image is reused only when it holds the whole
// set at the same versions and the same integrities, never when it holds a
// subset, and the toolchain and the base have to be identical. The packages
// are compared as the facts the reduction wrote them — one package per literal
// triple (§2.4) — so the comparison is over triples and not over a text two
// different triples could share.
//
// The cost of a reuse is stated in the design and is the owner's answer
// (O6): the repository's image then contains packages it does not depend
// on, which is a wider supply-chain surface and a dependency layer another
// repository's image.setup installed.

// Superset returns the key of the lock's image that holds resolved's whole
// package set at the same versions and integrities and may hold more, and
// whether there is one. The base must be the identical ref and digest and the
// tools identical, since an extra tool or a different base changes what the
// image is, while an extra package only widens what is installed.
//
// Of several that qualify the one with the fewest extra packages wins;
// ties go to the most recent built_at, then to the lower key hex, so the
// choice is deterministic and two machines pick the same image.
func Superset(lock *Lock, resolved Resolved) (string, bool) {
	if lock == nil {
		return "", false
	}
	best, found := "", false
	// The keys are walked in order, so the lower key hex keeps a tie the
	// later comparisons cannot break.
	for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
		img := lock.Images[key]
		if !img.holds(resolved) {
			continue
		}
		if !found || fewerExtras(img, lock.Images[best]) {
			best, found = key, true
		}
	}
	return best, found
}

// fewerExtras reports whether a is the better reuse than b among images
// that both hold the set: fewer packages the set does not name, then the
// more recently built.
func fewerExtras(a, b LockedImage) bool {
	if len(a.Packages) != len(b.Packages) {
		return len(a.Packages) < len(b.Packages)
	}
	return a.BuiltAt.After(b.BuiltAt)
}

// holds reports whether the image holds the whole of r: the identical base,
// the identical tools and every one of r's packages, with as many more as
// it likes.
func (i LockedImage) holds(r Resolved) bool {
	if i.BaseRef != r.BaseRef || i.BaseDigest != r.BaseDigest {
		return false
	}
	return sameFacts(i.Tools, r.Tools) && holdsAll(i.Packages, r.Packages)
}

// holdsAll reports whether held names every fact in want, and may name more.
func holdsAll(held, want []string) bool {
	set := make(map[string]struct{}, len(held))
	for _, h := range held {
		set[h] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

// sameFacts reports whether two fact lists name the same facts. A set's
// facts are a set: the order the lock holds them in is not a fact, so
// identical here is set equality rather than slice equality.
func sameFacts(a, b []string) bool {
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(slices.Compact(as), slices.Compact(bs))
}
