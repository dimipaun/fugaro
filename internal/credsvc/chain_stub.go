package credsvc

import "github.com/dimipaun/fugaro/internal/routing"

// This file is the one part of the chain wire that goes away: the bridge C1
// ships, which answers every read "no old record" until MG3 writes the
// release-1 bridge that reads `creds/repoauth/<slug>`. X4 removes this file
// together with the seam it fills, so the deletion is a deletion and not an
// edit, and every test about the bridge's answer rather than about the stub
// lives in chain_wire_test.go, which stays.
//
// What it is for is the contract arriving before its implementation: CS1's
// chain walk consults LegacyChain, and the seam has to answer before MG3
// teaches it to read the old record. The answer it gives in the meantime is
// the one that can never serve a call by accident — no old record, which the
// walk treats as "this repository has no bridge", so a run whose image predates
// the table is refused rather than served by whatever the record happened to
// hold. A stub that answered a source would be a bridge nothing read the
// record for.
//
// **It decides nothing.** It reads no record, no document and no repository:
// the argument is not looked at, and two different repositories get the same
// answer. That is the stub rule the other scaffolding in this tree follows
// (provider_wire_stub.go, internal/cli's after_write_stub.go).

// stubLegacyBridge is the bridge until MG3 supplies the real one: no old
// record for anything.
//
// It answers false rather than an empty slice, and that is the contract's own
// two answers read correctly: false is "there is no record for this
// repository", which is a different situation from a record that names no
// source. The walk refuses the first and skips the second, so collapsing them
// here would turn a repository with no bridge into one whose bridge is empty.
type stubLegacyBridge struct{}

func (stubLegacyBridge) OldSources(string) ([]routing.Source, bool) {
	return nil, false
}

// The stub is a bridge: a compile-time check, because the walk (CS1) is written
// against the interface and a stub that no longer satisfied it would fail to
// build only once the walk existed to say so. It goes with this file, so no
// test has to be edited when the stub is deleted.
var _ LegacyBridge = stubLegacyBridge{}

// The assignment stands only while nothing else has made one.
//
// The guard is the same one provider_wire_stub.go carries, for the same
// reason: the toolchain presents a package's files in name order, so whether
// this init runs before or after the real bridge's depends on what that file
// is called — legacy_auth.go sorts after this one today, and a name that sorts
// before it would put the stub back over the real bridge with nothing failing
// to compile. Asking whether LegacyChain is still nil makes this file what it
// is meant to be whatever the bridge's file is called: the answer that stands
// until a real one exists, and never one that stands over a real one.
func init() {
	if LegacyChain == nil {
		LegacyChain = stubLegacyBridge{}
	}
}
