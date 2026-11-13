// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The wire's own checks (portal plan P1.1, F20). The portal's API shapes are
// JSON Schemas in `ee/portal/schemas/`, one file per area, and the Go types are
// the `wire_*.go` files beside this one; neither is generated from the other, so
// these tests are what keeps them the same shape.
//
// A fixture that decodes is the two readers agreeing: the Go decoder reads it
// strictly, and the area's schema accepts it. The SPA's hand-written TypeScript
// types are tested on these same fixtures (P1.4), so a fixture is also the third
// reader's statement of what a shape holds.
//
// A field the schema does not know is refused by both readers. One that accepted
// more than the other would be a field the SPA believes is served and the API
// drops, which is the failure this pair of readers exists to prevent, and it is
// why every object in these schemas is closed at every level rather than only at
// the top.
//
// And exactly one shape carries a credential: the reveal's bundle. Every other
// answer is metadata — the spec's own split (F5: resellers and operations see
// metadata only), and a shape that grew a secret field is a leak the fixtures do
// not have to show to be refused.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The two directories the portal's wire lives in, relative to this package. They
// are the SPA's tree and not this package's: the schema a route's answer is read
// against and the fixture that stands for it sit beside the TypeScript types
// built from them (P1.4), so the frontend and the Go side are reading one file.
const (
	wireFixtureDir = "../portal/schemas/fixtures"
	wireSchemaDir  = "../portal/schemas"
)

// TestWireFixturesDecodeStrictly pins that every fixture decodes into the shape
// its name registers, that the area's schema accepts it, and that the two
// readers cover each other: a shape with no fixture, a fixture with no shape and
// a schema file no shape names are each a hole the other two would not show.
func TestWireFixturesDecodeStrictly(t *testing.T) {
	entries, err := os.ReadDir(wireFixtureDir)
	if err != nil {
		t.Fatalf("the portal's fixtures are not readable at %s: %v", wireFixtureDir, err)
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		shapeName := strings.TrimSuffix(name, ".json")
		shape, ok := wireShapes[shapeName]
		if !ok {
			t.Errorf("%s/%s: no wire shape is registered under %q, so nothing decodes this fixture and nothing validates it",
				wireFixtureDir, name, shapeName)
			continue
		}
		seen[shapeName] = true
		fixture, err := os.ReadFile(filepath.Join(wireFixtureDir, name))
		if err != nil {
			t.Errorf("%s/%s: %v", wireFixtureDir, name, err)
			continue
		}
		value := shape.alloc()
		if err := decodeWire(fixture, value); err != nil {
			t.Errorf("%s/%s: this build does not read the fixture into %T: %v", wireFixtureDir, name, value, err)
			continue
		}
		if err := wireSchema(t, shape.area).Validate(wireInstance(t, fixture)); err != nil {
			t.Errorf("%s/%s: the %s area's schema refuses the fixture the Go decoder accepted: %v",
				wireFixtureDir, name, shape.area, err)
		}
	}
	if len(seen) == 0 {
		t.Fatalf("%s holds no fixture this build registers a shape for", wireFixtureDir)
	}
	for _, shapeName := range wireShapeNames() {
		if !seen[shapeName] {
			t.Errorf("the %s shape is registered and %s/%s.json does not exist: a shape no fixture stands for is a shape the SPA's types are not tested against",
				shapeName, wireFixtureDir, shapeName)
		}
	}
	schemas, err := filepath.Glob(filepath.Join(wireSchemaDir, "*.schema.json"))
	if err != nil {
		t.Fatalf("%s: %v", wireSchemaDir, err)
	}
	areas := make(map[string]bool, len(wireShapes))
	for _, shape := range wireShapes {
		areas[shape.area] = true
	}
	for _, file := range schemas {
		area := strings.TrimSuffix(filepath.Base(file), ".schema.json")
		if !areas[area] {
			t.Errorf("%s describes an area no wire shape names, so no fixture is ever read against it", file)
		}
		wireAssertClosed(t, file)
	}
}

// wireUnknownField is the field every shape is asked to refuse. It is spelled
// the way a field this repository would add is spelled, so the failure a reader
// sees is about the shape and not about the name being obviously foreign.
const wireUnknownField = "a_field_this_shape_does_not_hold"

// TestWireRefusesUnknownFields pins the other half of the two readers agreeing:
// a field one of them knows and the other does not is a field the SPA reads as
// served and the API drops, or the reverse, and neither reader may be the only
// one to refuse it. Both are asked the same way — the fixture with one field no
// shape holds added to it — because a decoder that refused it for some other
// reason would look like a refusal and hold the line nowhere.
func TestWireRefusesUnknownFields(t *testing.T) {
	for _, shapeName := range wireShapeNames() {
		shape := wireShapes[shapeName]
		fixture, err := os.ReadFile(filepath.Join(wireFixtureDir, shapeName+".json"))
		if err != nil {
			t.Errorf("%s/%s.json: %v", wireFixtureDir, shapeName, err)
			continue
		}
		object := wireObject(t, fixture)
		object[wireUnknownField] = true
		mutated, err := json.Marshal(object)
		if err != nil {
			t.Errorf("%s: %v", shapeName, err)
			continue
		}
		mutated = append(mutated, '\n')
		value := shape.alloc()
		err = decodeWire(mutated, value)
		if err == nil {
			t.Errorf("%s: this build reads a %s field into %T, so the shape accepts what the %s area's schema refuses",
				shapeName, wireUnknownField, value, shape.area)
		} else if !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("%s: this build refuses the extra field for another reason than not knowing it, so the fixture does not show that the shape is closed: %v",
				shapeName, err)
		}
		if err := wireSchema(t, shape.area).Validate(wireInstance(t, mutated)); err == nil {
			t.Errorf("%s: the %s area's schema accepts a %s field the Go decoder refuses, so a route could serve a field no shape holds",
				shapeName, shape.area, wireUnknownField)
		}
	}
}

// wireCredentialTags are the JSON field names that carry a credential, or carry
// the way to one. The list is the whole point of the test below: it is the
// vocabulary a shape would have to grow to leak, and a shape that grew one is
// refused here rather than in review.
var wireCredentialTags = []string{
	"key",
	"private_key",
	"renewal_secret",
	"secret",
	"password",
	"credential",
	"token",
}

// TestRevealBundleIsTheOnlySecretShape pins the spec's split in the types
// themselves (F5): everything the portal serves about a key is metadata, and the
// one exception is the reveal bundle, which is the single act that hands a
// workspace its key and the secret it renews under (D2182). It is reflection over
// the registry rather than a list of names, so a shape added later is asked the
// same question as the ones here, and nested types are walked because a secret
// one level down is a secret served.
func TestRevealBundleIsTheOnlySecretShape(t *testing.T) {
	var carrying []string
	for _, shapeName := range wireShapeNames() {
		if fields := wireCredentialFields(reflect.TypeOf(wireShapes[shapeName].alloc()).Elem(), 0); len(fields) > 0 {
			carrying = append(carrying, shapeName+" ("+strings.Join(fields, ", ")+")")
		}
	}
	if want := []string{"keys.reveal"}; len(carrying) != 1 || !strings.HasPrefix(carrying[0], want[0]) {
		t.Fatalf("the shapes carrying a credential are %v, and only %s may: every other answer is metadata, which is what a reseller and the operations team are served (F5)",
			carrying, want[0])
	}
}

// wireCredentialFields is the path of every field of one type, and of the types
// it holds, whose JSON name is one of wireCredentialTags. The depth bounds the
// walk at the nesting these shapes have, so a type that referred to itself would
// stop rather than recur.
func wireCredentialFields(t reflect.Type, depth int) []string {
	if depth > 4 {
		return nil
	}
	var found []string
	for i := range t.NumField() {
		field := t.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if slices.Contains(wireCredentialTags, name) {
			found = append(found, name)
		}
		held := field.Type
		for held.Kind() == reflect.Pointer || held.Kind() == reflect.Slice {
			held = held.Elem()
		}
		if held.Kind() == reflect.Struct && held != reflect.TypeOf(time.Time{}) {
			for _, nested := range wireCredentialFields(held, depth+1) {
				found = append(found, name+"."+nested)
			}
		}
	}
	return found
}

// wireShapeNames is every registered shape's name, sorted, so a failure prints
// the same list in the same order twice.
func wireShapeNames() []string {
	names := make([]string, 0, len(wireShapes))
	for name := range wireShapes {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// wireAssertClosed walks one area's schema and refuses an object that could
// accept a field the Go decoder would not: every shape here is a Go struct read
// with DisallowUnknownFields, so a subschema without additionalProperties:false
// is the schema admitting what the decoder refuses, and a fixture only proves
// the fields the fixture happens to carry.
func wireAssertClosed(t *testing.T, file string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	var walk func(at string, node any)
	walk = func(at string, node any) {
		switch value := node.(type) {
		case map[string]any:
			if _, properties := value["properties"]; properties {
				if closed, ok := value["additionalProperties"]; !ok || closed != false {
					t.Errorf("%s: %s holds properties and does not say additionalProperties:false, so it accepts a field this build's decoder refuses", file, at)
				}
			}
			for key, child := range value {
				walk(at+"."+key, child)
			}
		case []any:
			for i, child := range value {
				walk(at+"."+string(rune('0'+i%10)), child)
			}
		}
	}
	walk("$", doc)
}

// wireSchema compiles one area's schema under its own $id, as the shipped
// schemas are read: the file names itself, and a validator that had to be told
// the id would be reading something other than what the SPA fetches.
func wireSchema(t *testing.T, area string) *jsonschema.Schema {
	t.Helper()
	file := filepath.Join(wireSchemaDir, area+".schema.json")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	id, ok := doc.(map[string]any)["$id"].(string)
	if !ok || id == "" {
		t.Fatalf("%s carries no $id, so nothing can compile it", file)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return schema
}

// wireInstance reads one fixture as the validator's instance.
func wireInstance(t *testing.T, raw []byte) any {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("a fixture is not JSON: %v", err)
	}
	return value
}

// wireObject reads one fixture as the object a mutation is applied to.
func wireObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("a fixture is not a JSON object: %v", err)
	}
	return object
}
