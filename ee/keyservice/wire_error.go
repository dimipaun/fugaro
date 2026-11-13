// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// WireRefusal is the API's one refusal (P1.0's "one JSON error shape", P1.1):
// what every route answers when it refuses, and the only shape here that a route
// does not mount by name, because every route can answer it.
//
// It is one shape rather than a sentence per route because the SPA is a program
// and not a reader: the fault's Code is the stable half it branches on and its
// Message is the person's half it shows. A route that answered prose alone would
// put the frontend's behaviour in its wording, and the wording is what changes.
//
// The fault arrives under `error` because that is what the API's own writer sends
// (`refuseAPI`, api.go, whose TestAPIErrorsAreUniform pins it): this type is the
// reader's half of that body and not a second opinion about it, and
// TestWireRefusalIsTheAPIsOwnBody is what keeps the two from drifting.
type WireRefusal struct {
	// Error is the fault, under the one key the SPA reads it from.
	Error WireFault `json:"error"`
}

// WireFault is the code a client branches on and the sentence a person reads.
type WireFault struct {
	// Code is the machine's name for the refusal, spelled `lower_snake_case`
	// and stable: the SPA switches on it, so renaming one is changing a wire
	// format (CLAUDE.md).
	Code string `json:"code"`
	// Message is a sentence for the person, and it is data on render and never
	// markup: an address or a name a caller supplied reaches this field (F10).
	Message string `json:"message"`
}

func init() {
	registerWire("error.refusal", func() any { return new(WireRefusal) })
}
