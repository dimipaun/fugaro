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
// body whose *shape* the schema refuses must not arrive at a handler looking
// valid. What a body's values say is not this function's to judge: an enum value
// the schema does not name, a project id its pattern refuses (F41's, the one
// that keeps an id safe to put in a host name), a seat count below its minimum,
// an empty id where it forbids an empty one — each of those is the handler's own
// check, and TestWireHoldsTheShapeAndNotTheValues pins that split.
//
// encoding/json alone gives none of that. It matches a field name ignoring case,
// so `WORKSPACE_ID` fills `workspace_id`; it keeps the last of a repeated key, so
// a body can say one thing twice and be read as the second; it reads an absent
// field as the zero value, so a body missing its terms version arrives as an
// empty one; it reads `null` as "leave the field alone", so a null seat count
// arrives as zero; and it reads an object no deeper than the shape's own fields,
// so a nested object is held to nothing at all. A body written against a newer
// schema is the case that matters most: it must fail here rather than be served
// with the half of what it asked for that this build happens to know.
func decodeWire(body []byte, shape any) error {
	value := reflect.ValueOf(shape)
	if value.Kind() != reflect.Pointer || value.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("%T is not a pointer to a struct, so no body has that shape", shape)
	}
	if err := wireStrict(body, value.Elem().Type()); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(shape)
}

// wireStrict holds one body to the type its route reads it into, before that
// type reads a byte of it: the body is an object, and every object in it holds
// exactly the fields of the Go type there, each of them once, with no null where
// the schema requires a value. It walks the body's own tokens rather than a
// decoded value, because a decoded map has already lost the case-folding, the
// repeats and the nulls by the time it exists, and it goes as deep as the shape
// does: a nested object — an organisation's terms, a reseller's rates, a
// statement's lines — is closed and fully required in its def too, so it is read
// the same way as the shape's own fields rather than by encoding/json alone.
//
// The last decode is what says the values have the types the fields have; this
// walk is what says the body carries the fields the schema carries, each of them
// once and none of them null where the schema requires a value. Neither says
// anything about what the values mean: the schema's enums, patterns and ranges
// are each handler's own check — TestWireHoldsTheShapeAndNotTheValues pins that
// split — and a timestamp's format is its parser's.
func wireStrict(body []byte, t reflect.Type) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil {
		return errors.New("the body is not a JSON object, and a shape's fields are all it holds")
	}
	if delim, ok := opening.(json.Delim); !ok || delim != '{' {
		return errors.New("the body is not a JSON object, and a shape's fields are all it holds")
	}
	if err := wireHoldObject(decoder, t, ""); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the body holds more than one JSON value, and only the first would be read")
	}
	return nil
}

// wireHoldObject reads one object's fields against the Go type that holds them:
// a name the type does not hold is refused, a name it holds twice is refused, and
// a field left out is refused, so a body and its schema agree on every field the
// object has and not only on the ones the body happens to carry. at is the path
// this object was reached by, which is what a failure names.
func wireHoldObject(decoder *json.Decoder, t reflect.Type, at string) error {
	held := make(map[string]reflect.Type)
	wireFieldTypes(t, held)
	seen := make(map[string]bool, len(held))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("%s names a field with something other than a string", wireAt(at))
		}
		if seen[key] {
			return fmt.Errorf("%s writes the field %q twice, and a reader that kept the last of the two would see a value the schema never saw",
				wireAt(at), key)
		}
		field, ok := held[key]
		if !ok {
			return fmt.Errorf("%s holds the field %q, which this shape does not", wireAt(at), key)
		}
		seen[key] = true
		delete(held, key)
		if err := wireHold(decoder, field, wirePath(at, key)); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if len(held) > 0 {
		return fmt.Errorf("%s leaves out %s, and every field of this shape is one the body has to carry",
			wireAt(at), strings.Join(slices.Sorted(maps.Keys(held)), ", "))
	}
	return nil
}

// wireHold reads one value and holds it to the Go type the field there has: an
// object against a struct's fields, a list against the type of its elements, and
// null only where the type can hold one — everywhere else the schema requires a
// value, and encoding/json would take null as the zero value rather than refuse
// it.
func wireHold(decoder *json.Decoder, t reflect.Type, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			base := wirePointerless(t)
			if !wireHoldsFields(base) {
				return fmt.Errorf("%s holds an object, and the field there reads a value of its own", wireAt(path))
			}
			return wireHoldObject(decoder, base, path)
		case '[':
			base := wirePointerless(t)
			if base.Kind() != reflect.Slice && base.Kind() != reflect.Array {
				return fmt.Errorf("%s holds an array, and the field there is not a list", wireAt(path))
			}
			return wireHoldArray(decoder, base.Elem(), path)
		default:
			return fmt.Errorf("%s closes an object or an array this body never opened", wireAt(path))
		}
	case nil:
		if wireNullable(t) {
			return nil
		}
		return fmt.Errorf("%s is null, and the field there holds a value: a reader of the schema refuses null there, and encoding/json would take it as the zero value",
			wireAt(path))
	default:
		return nil
	}
}

// wireHoldArray reads a list's elements against the type they all have, so a
// nested object inside a list is held to its fields like any other.
func wireHoldArray(decoder *json.Decoder, element reflect.Type, path string) error {
	for i := 0; decoder.More(); i++ {
		if err := wireHold(decoder, element, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

// wireAt names where in the body a fault is, the way the SPA reads the shape: the
// body itself at the root, and the path walked to it below.
func wireAt(path string) string {
	if path == "" {
		return "the body"
	}
	return "the body's " + path
}

// wirePath is the path to a field, from the path to the object that holds it.
func wirePath(at, field string) string {
	if at == "" {
		return field
	}
	return at + "." + field
}

// wirePointerless is a type once its pointers are unwrapped: a nullable field is
// a pointer to the type that reads it, so the object below it is the same one at
// either depth.
func wirePointerless(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// wireJSONReader is the interface a type implements when it reads its own JSON.
var wireJSONReader = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// wireHoldsFields reports whether a body's object there carries the type's own
// fields, which is what the walk has something to compare. A scalar has none, and
// neither has a type that reads itself out of a JSON value — a time.Time is a
// string on the wire. A container at either is refused rather than stepped over:
// there is nothing there for the walk to compare, and every shape's schema
// requires a value where the Go type is not a struct this package wrote.
func wireHoldsFields(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return !reflect.PointerTo(t).Implements(wireJSONReader)
}

// wireNullable reports whether a type can hold a JSON null. A pointer is how this
// package spells a field its schema writes as a union with null — a nullable
// organisation id, instant or nested object — and every other field is one the
// schema writes as a plain type, a list of them included.
func wireNullable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Interface:
		return true
	}
	return false
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
