package imagecheck

// BaseEpoch is the base-image epoch (D3446): the number a change to the base
// image moves, which is what makes a rebuild eager.
//
// It is bumped by hand whenever images/base, images/derived or images/common
// changes for anything but the Fugaro version — a package, a tool, the derived
// build's template, the shared checkout script. A catalog entry's key hashes a
// repository's packages, tools and base reference, so none of those three trees
// reaches an image through it (F43); a catalog build records the epoch it was
// built from instead, and an entry whose record carries another epoch is stale.
// A release that only moves the Fugaro version leaves the epoch alone, so its
// images catch up on the nightly rebuild rather than an eager one (D3445).
//
// TestBaseEpochTracksBaseImage hashes those three trees and fails until this
// number moves with them, so the bump is not something a person has to
// remember.
const BaseEpoch = 1
