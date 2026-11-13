// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package docstore

// Storable is fileable for the one test that has to ask the question before it
// calls: firestore_test.go's wrapper stands between the shared suite and the
// emulator, and it may only put a case's prefix in front of a name the store
// would file. It asks this package's own rule rather than repeating it, so the
// wrapper cannot drift from the store the way a second copy of the predicate
// would — the names it leaves alone are exactly the ones named() refuses, the
// empty name included.
func Storable(name string) bool { return name != "" && fileable(name) }
