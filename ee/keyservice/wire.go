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
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
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

// decodeWire reads one body into the shape its route mounts, strictly: the body
// has to be an object, it has to hold exactly the shape's fields, and it has to
// hold each of them once (CLAUDE.md's rule that a wire format decodes strictly,
// F20). Every portal route reads its request body through this function, so a
// body the schema refuses must not arrive at a handler looking valid.
//
// encoding/json alone gives none of the three. It matches a field name ignoring
// case, so `WORKSPACE_ID` fills `workspace_id`; it keeps the last of a repeated
// key, so a body can say one thing twice and be read as the second; it reads an
// absent field as the zero value, so a body missing its terms version arrives as
// an empty one; and it reads `null` as a whole shape of zeroes. A body written
// against a newer schema is the case that matters most: it must fail here rather
// than be served with the half of what it asked for that this build happens to
// know.
func decodeWire(body []byte, shape any) error {
	keys, err := wireBodyKeys(body)
	if err != nil {
		return err
	}
	value := reflect.ValueOf(shape)
	if value.Kind() != reflect.Pointer || value.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("%T is not a pointer to a struct, so no body has that shape", shape)
	}
	held := make(map[string]reflect.Type)
	wireFieldTypes(value.Elem().Type(), held)
	for _, key := range keys {
		if _, ok := held[key]; !ok {
			return fmt.Errorf("the body holds the field %q, which this shape does not", key)
		}
		delete(held, key)
	}
	if len(held) > 0 {
		return fmt.Errorf("the body leaves out %s, and every field of this shape is one the body has to carry",
			strings.Join(slices.Sorted(maps.Keys(held)), ", "))
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(shape)
}

// wireBodyKeys is a body's own field names in the order written, with a repeated
// key refused. The body is walked as tokens rather than decoded into a map,
// because a map has already lost both the order and the repeat by the time it
// exists, which is exactly the pair of facts a strict reader is asked for.
func wireBodyKeys(body []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := opening.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("the body is not a JSON object, and a shape's fields are all it holds")
	}
	var keys []string
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("the body names a field with something other than a string")
		}
		if seen[key] {
			return nil, fmt.Errorf("the body writes the field %q twice, and a reader that kept the last of the two would see a value the schema never saw", key)
		}
		seen[key] = true
		keys = append(keys, key)
		var held json.RawMessage
		if err := decoder.Decode(&held); err != nil {
			return nil, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the body holds more than one JSON value, and only the first would be read")
	}
	return keys, nil
}

// wireFieldTypes is one type's own JSON names and the types they hold, exactly as
// its tags spell them: the list a body's keys are compared with, and the list
// wire_test.go compares a subschema's properties with.
func wireFieldTypes(t reflect.Type, into map[string]reflect.Type) {
	for i := range t.NumField() {
		field := t.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			into[name] = field.Type
		}
	}
}
