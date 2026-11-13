// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The API's shapes, and the registry that keeps them countable (portal plan
// P1.1, F20).
//
// A shape is one JSON object the API reads or answers, and it has three readers:
// the Go type in `wire_<area>.go`, this repository's JSON Schema in
// `ee/portal/schemas/<area>.schema.json`, and the hand-written TypeScript type
// the SPA is built from (P1.4). None of the three is generated from another, so
// what keeps them one shape is `wire_test.go` over the fixtures in
// `ee/portal/schemas/fixtures/`: the names registered below are those fixtures'
// names, and a shape with no fixture, or a fixture no shape claims, fails there.
//
// Registration happens in each area's own file, like P1.0's route registry and
// for the same reason: a later task adds shapes to its area and never edits a
// shared list, so two areas cannot conflict over one.

import (
	"bytes"
	"encoding/json"
	"strings"
)

// wireShape is one registered shape: the area whose schema file describes it,
// and the type a body of that shape is decoded into.
type wireShape struct {
	area  string
	alloc func() any
}

// wireShapes is every shape this build reads, by the name its fixture carries.
var wireShapes = map[string]wireShape{}

// registerWire records one shape under `<area>.<shape>`, which is its fixture's
// file name without `.json`, so the registry and the fixtures folder are one
// list read two ways. The two panics are an author's mistake caught at the first
// test run rather than a fixture that quietly validates against another area's
// schema.
func registerWire(name string, alloc func() any) {
	area, shape, found := strings.Cut(name, ".")
	if !found || area == "" || shape == "" || strings.Contains(shape, ".") {
		panic("keyservice: a wire shape is named <area>.<shape>, and this one is " + name)
	}
	if _, duplicate := wireShapes[name]; duplicate {
		panic("keyservice: two wire shapes are registered as " + name)
	}
	wireShapes[name] = wireShape{area: area, alloc: alloc}
}

// decodeWire reads one request body into the shape its route mounts, strictly: a
// field this build does not know is refused rather than dropped, because a body
// written against a newer schema must fail here instead of being served with
// half of what it asked for (CLAUDE.md's rule that a wire format decodes
// strictly, F20).
//
// An object followed by more content is not refused, which is what the two
// readers already in this package do (renew.go, minterclient.go): the cap on the
// body is the route's (P1.0), and anything after the first value is bytes a
// caller pays for and nothing reads.
func decodeWire(body []byte, shape any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(shape)
}
