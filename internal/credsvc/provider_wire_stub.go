package credsvc

// This file is the one part of the provider wire that goes away: the stub
// accessor C1 ships, which answers every read "no such credential" until the
// bundle holds a named entry to answer with. I1 deletes this file and its own
// rewiring replaces it, so the deletion is a deletion and not an edit, and
// every test that is about the accessor's answer rather than about the stub
// lives in provider_wire_test.go, which stays.
//
// What it is for is the contract arriving before its implementation: KS4's
// route, KS3's offering and KS5's gateway are written against the accessor's
// signature, and the signature has to answer before KS1 teaches the bundle to
// hold a named entry. The answer it gives in the meantime is the one that can
// never serve a call by accident — no entry, which the caller turns into
// ErrorProviderNotServed — so a side written against C1 runs its own tests
// against a real signature and no side can pass on a credential that nothing
// read.
//
// **It decides nothing.** It reads no bundle, no document, no repository and
// no policy: the two arguments are not looked at, and two calls with two
// different repositories get the same answer. That is the stub rule the other
// scaffolding in this tree follows (sealkey_stub.go, internal/cli's
// after_write_stub.go).

// stubReadProviderKey is the accessor's body until KS1 supplies the real one:
// no entry for anything, which is the answer that serves nobody.
//
// It answers false rather than an error, and that is this contract's own
// fail-closed direction read correctly: "no such credential" is not a read that
// failed, it is a bundle that does not hold the entry, and the caller answers
// it ErrorProviderNotServed — the refusal a repository whose key was never
// copied must meet. An error arm here would be a third answer every caller
// would have to invent a sentence for, which is why the signature has two and
// not three.
func stubReadProviderKey(*Bundle, string, string) (ProviderKeyEntry, bool) {
	return ProviderKeyEntry{}, false
}

// The assignment stands only while nothing else has made one.
//
// The guard is not decoration, because the filename order works the other way
// here: the sides that fill this accessor sort BEFORE this file — KS1 assigns
// it from bundle.go and I1 from provider_wire_bundle.go — and the toolchain
// presents a package's files in name order, so an unguarded init here would run
// last and put the stub back over the real reader. Asking whether the accessor is still nil makes the stub what
// it is meant to be whatever the other file is called: the answer that stands
// until a real one exists, and never one that stands over a real one.
func init() {
	if ReadProviderKey == nil {
		ReadProviderKey = stubReadProviderKey
	}
}
