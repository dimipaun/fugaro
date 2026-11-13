package buildtoken

import "context"

// Stub answers the shape and does nothing else: it reads nothing out of the
// request, validates nothing, reaches no network and mints no credential. B2
// and B8 are written against the contract while the build route (B1) is built
// beside them, and this is what lets their tests run until I2 wires the two.
//
// I2 deletes this file. A shipped path that still held a Stub would bake an
// image with no credential at all, which is why the stub is a whole file of its
// own rather than a fallback inside Mint's real implementation.
type Stub struct {
	// Answer is what Mint returns, unchanged.
	Answer Response
}

var _ Client = Stub{}

// Mint returns Answer. The request is ignored: a stub that chose an answer by
// reading it would be a second implementation of the service's rule, and the
// rule has one home (B1).
func (s Stub) Mint(context.Context, Request) (Response, error) { return s.Answer, nil }
