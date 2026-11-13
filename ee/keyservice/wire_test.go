// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The wire's own checks (portal plan P1.1, F20). The portal's API shapes are
// JSON Schemas in `ee/portal/schemas/`, one file per area, and the Go types are
// the `wire_*.go` files beside this one; neither is generated from the other, so
// these tests are what keeps them the same shape.
//
// A fixture that decodes is the two readers agreeing: the Go decoder reads it
// strictly, and the shape's own subschema accepts it. The SPA's hand-written
// TypeScript types are tested on these same fixtures (P1.4), so a fixture is
// also the third reader's statement of what a shape holds.
//
// The names are compared and not only the values, because a fixture proves the
// fields it happens to carry and says nothing about the rest: a field on one
// side alone — a Go field no schema property describes, or a property no Go
// field reads — is exactly the drift a fixture is written to miss, and the plan
// invites it, since every later task owns one area's file and adds fields to it.
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
	"maps"
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
// hand-written from them (P1.4), so the frontend and the Go side are reading one
// file.
const (
	wireFixtureDir = "../portal/schemas/fixtures"
	wireSchemaDir  = "../portal/schemas"
	wireDefPrefix  = "#/$defs/"
)

// TestWireFixturesDecodeStrictly pins that a shape and its schema hold the same
// field names, that every fixture decodes into the shape its name registers and
// is accepted by that shape's own subschema, and that the two readers cover each
// other: a shape with no fixture, a fixture with no shape and a schema file no
// shape names are each a hole the other two would not show.
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
		def := strings.TrimPrefix(shapeName, shape.area+".")
		wireAssertSameFields(t, shapeName, value, wireProperties(t, shape.area, def))
		if err := wireSchemaDef(t, shape.area, def).Validate(wireInstance(t, fixture)); err != nil {
			t.Errorf("%s/%s: the %s schema's own %s subschema refuses the fixture the Go decoder accepted: %v",
				wireFixtureDir, name, shape.area, def, err)
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
		wireAssertRootCovers(t, file, area)
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
		def := strings.TrimPrefix(shapeName, shape.area+".")
		if err := wireSchemaDef(t, shape.area, def).Validate(wireInstance(t, mutated)); err == nil {
			t.Errorf("%s: the %s schema's %s subschema accepts a %s field the Go decoder refuses, so a route could serve a field no shape holds",
				shapeName, shape.area, def, wireUnknownField)
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

// wireAssertSameFields refuses a field one reader has and the other does not: a
// Go field the shape's subschema does not describe is a field the SPA cannot
// know is served, and a property no Go field reads is one the API drops however
// faithfully the schema promises it.
func wireAssertSameFields(t *testing.T, shapeName string, value any, properties []string) {
	t.Helper()
	held := make(map[string]bool)
	wireFieldNames(reflect.TypeOf(value).Elem(), held)
	for _, name := range properties {
		if !held[name] {
			t.Errorf("%s: the schema describes a %q property this build reads nothing into, so a route would drop a field the SPA is told it is served",
				shapeName, name)
		}
		delete(held, name)
	}
	for _, name := range slices.Sorted(maps.Keys(held)) {
		t.Errorf("%s: this build reads a %q field the shape's subschema does not describe, so the SPA cannot know it is served",
			shapeName, name)
	}
}

// wireFieldNames collects one type's own JSON names. Nested types are their own
// defs and are reached through the property that holds them, so this reads the
// shape's level and no deeper.
func wireFieldNames(t reflect.Type, into map[string]bool) {
	for i := range t.NumField() {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			into[name] = true
		}
	}
}

// wireProperties is the names one shape's subschema describes, which is the list
// the Go type has to read exactly.
func wireProperties(t *testing.T, area, def string) []string {
	t.Helper()
	file := filepath.Join(wireSchemaDir, area+".schema.json")
	defs, ok := wireAreaDoc(t, area)["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("%s holds no $defs, so no shape in the %s area has a subschema", file, area)
	}
	shape, ok := defs[def].(map[string]any)
	if !ok {
		t.Fatalf("%s: the %s area holds no $defs/%s, so nothing validates the %s shape", file, area, def, area+"."+def)
	}
	properties, ok := shape["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: $defs/%s describes no properties", file, def)
	}
	return slices.Sorted(maps.Keys(properties))
}

// wireAssertRootCovers refuses an area whose root oneOf does not reach every
// shape this build registers in it, in either direction: the root is what a
// reader of the file takes for the area's shapes, so a def no branch names
// describes a shape nothing validates, and a branch naming no registered shape
// advertises one nothing decodes.
func wireAssertRootCovers(t *testing.T, file, area string) {
	t.Helper()
	branches := make(map[string]bool)
	for _, branch := range wireArray(t, file, wireAreaDoc(t, area)["oneOf"]) {
		ref, ok := branch.(map[string]any)
		if !ok {
			t.Errorf("%s: a root oneOf branch is not an object", file)
			continue
		}
		target, ok := ref["$ref"].(string)
		if !ok || !strings.HasPrefix(target, wireDefPrefix) {
			t.Errorf("%s: a root oneOf branch refers to %q rather than to a def of this file", file, ref["$ref"])
			continue
		}
		branches[strings.TrimPrefix(target, wireDefPrefix)] = true
	}
	registered := make(map[string]bool)
	for shapeName, shape := range wireShapes {
		if shape.area == area {
			registered[strings.TrimPrefix(shapeName, area+".")] = true
		}
	}
	for _, def := range slices.Sorted(maps.Keys(registered)) {
		if !branches[def] {
			t.Errorf("%s: this build registers the %s.%s shape and the root oneOf does not name $defs/%s, so the file does not say the area serves it",
				file, area, def, def)
		}
	}
	for _, def := range slices.Sorted(maps.Keys(branches)) {
		if !registered[def] {
			t.Errorf("%s: the root oneOf names $defs/%s and this build registers no %s.%s shape, so the file advertises a shape nothing decodes",
				file, def, area, def)
		}
	}
}

// wireArray reads one array out of a schema document, so a helper that walks a
// document the test did not write does not assert its shape twice.
func wireArray(t *testing.T, file string, node any) []any {
	t.Helper()
	array, ok := node.([]any)
	if !ok {
		t.Fatalf("%s: an array the test walks is not an array", file)
	}
	return array
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

// wireAreaDoc reads one area's schema as the document it is, for the walks that
// ask about its shape rather than about a fixture.
func wireAreaDoc(t *testing.T, area string) map[string]any {
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
	object, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("%s is not a JSON object", file)
	}
	return object
}

// wireSchemaDef compiles one shape's own subschema, under the area's $id and
// reached by its def, as the shipped schemas are read: the file names itself,
// and a validator that had to be told the id would be reading something other
// than what the SPA fetches. The def and not the whole file, because the whole
// file's oneOf would accept a fixture that had drifted into a sibling shape.
func wireSchemaDef(t *testing.T, area, def string) *jsonschema.Schema {
	t.Helper()
	file := filepath.Join(wireSchemaDir, area+".schema.json")
	doc := wireAreaDoc(t, area)
	id, ok := doc["$id"].(string)
	if !ok || id == "" {
		t.Fatalf("%s carries no $id, so nothing can compile it", file)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	schema, err := compiler.Compile(id + wireDefPrefix + def)
	if err != nil {
		t.Fatalf("%s: the %s shape's subschema does not compile: %v", file, area+"."+def, err)
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
