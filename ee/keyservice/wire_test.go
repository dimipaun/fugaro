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
// Every level is compared and not only the shape's own, because a nested def —
// an organisation's terms, a reseller's rates, a statement's lines — has no
// fixture of its own to be read against.
//
// A field the schema does not know is refused by both readers, and so is a body
// that leaves a field out or writes null as it: `decodeWire` holds a body to the
// schema's own reading of it — an object, exactly the fields of the type there,
// each of them once, and no null where a value is required — rather than to
// encoding/json's, which matches a name ignoring case, keeps the last of a
// repeated key, reads an absent field as the zero value and reads null as
// leaving the field alone. One reader accepting what the other refuses is a field
// the SPA believes is served and the API drops, or a body a handler is handed as
// valid that no schema ever agreed to; it is why every object in these schemas is
// closed at every level rather than only at the top, and why the decoder's own
// reading goes as deep as the schema does.
//
// And exactly one shape carries a credential: the reveal's bundle. Every other
// answer is metadata — the spec's own split (F5: resellers and operations see
// metadata only), and a shape that grew a secret field is a leak the fixtures do
// not have to show to be refused.

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		wireAssertSameFields(t, shapeName, shape.area, def, value)
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
		wireAssertDefsAreReached(t, file, area)
	}
}

// wireUnknownField is the field every shape is asked to refuse. It is spelled
// the way a field this repository would add is spelled, so the failure a reader
// sees is about the shape and not about the name being obviously foreign.
const wireUnknownField = "a_field_this_shape_does_not_hold"

// TestWireRefusesUnknownFields pins the other half of the two readers agreeing:
// a field one of them holds and the other does not is a field the SPA reads as
// served and the API drops, or the reverse, and neither reader may be the only
// one to refuse it. Three bodies are put to both, because encoding/json is
// looser than a schema in three ways — it matches a name ignoring case, it keeps
// the last of a repeated key, and it takes a name it does not know at all.
func TestWireRefusesUnknownFields(t *testing.T) {
	for _, shapeName := range wireShapeNames() {
		shape := wireShapes[shapeName]
		fixture, err := os.ReadFile(filepath.Join(wireFixtureDir, shapeName+".json"))
		if err != nil {
			t.Errorf("%s/%s.json: %v", wireFixtureDir, shapeName, err)
			continue
		}
		def := strings.TrimPrefix(shapeName, shape.area+".")
		schema := wireSchemaDef(t, shape.area, def)

		object := wireObject(t, fixture)
		object[wireUnknownField] = true
		wireRefuses(t, shapeName, "a field no shape holds", wireJSON(t, object), wireUnknownField, schema, false)

		held := slices.Sorted(maps.Keys(wireObject(t, fixture)))
		written, found := "", false
		for _, name := range held {
			if upper := strings.ToUpper(name); upper != name {
				written, found = name, true
				break
			}
		}
		if !found {
			t.Errorf("%s: every field is already upper case, so the case-folded body cannot be built", shapeName)
		} else {
			folded := wireObject(t, fixture)
			folded[strings.ToUpper(written)] = folded[written]
			delete(folded, written)
			wireRefuses(t, shapeName, fmt.Sprintf("%q written as %q", written, strings.ToUpper(written)), wireJSON(t, folded), strings.ToUpper(written), schema, false)
		}

		// A key written twice is the decoder's own case: the validator reads the
		// body through a parser that has already kept one of the two, so no schema
		// can be asked about it, and a reader that kept the same one would serve a
		// body that says two things as though it said one.
		whole := wireJSON(t, wireObject(t, fixture))
		value, err := json.Marshal(object)
		if err != nil {
			t.Errorf("%s: %v", shapeName, err)
			continue
		}
		twice := append(append([]byte{}, whole[:len(whole)-1]...), fmt.Sprintf(",%q:%s}", held[0], value)...)
		wireRefuses(t, shapeName, fmt.Sprintf("%q written twice", held[0]), twice, "twice", schema, true)
	}
}

// TestWireRefusesMissingFields pins that the two readers want the same fields
// present and not only the same names: every property of every subschema is
// required, and no Go field is one encoding/json reads as the zero value, so a
// body that drops any one of them is refused by both. A property that were
// optional would fail here, and that is the rule this guards — a body the SPA
// may leave a field out of is one whose TypeScript type cannot say what it
// carries.
func TestWireRefusesMissingFields(t *testing.T) {
	for _, shapeName := range wireShapeNames() {
		shape := wireShapes[shapeName]
		fixture, err := os.ReadFile(filepath.Join(wireFixtureDir, shapeName+".json"))
		if err != nil {
			t.Errorf("%s/%s.json: %v", wireFixtureDir, shapeName, err)
			continue
		}
		def := strings.TrimPrefix(shapeName, shape.area+".")
		schema := wireSchemaDef(t, shape.area, def)
		described, ok := wireDef(t, wireAreaDoc(t, shape.area), def, shapeName)["properties"].(map[string]any)
		if !ok {
			t.Errorf("%s: $defs/%s describes no properties, so no field of it can be dropped", shapeName, def)
			continue
		}
		for _, property := range slices.Sorted(maps.Keys(described)) {
			object := wireObject(t, fixture)
			delete(object, property)
			wireRefuses(t, shapeName, fmt.Sprintf("the field %q left out", property), wireJSON(t, object), property, schema, false)
		}
		// A body that is not an object holds none of a shape's fields, which is
		// this rule at its limit, and null is the one body that would otherwise
		// read as a whole shape of zeroes.
		wireRefuses(t, shapeName, "no object at all", []byte("null"), "not a JSON object", schema, false)
	}
}

// TestWireRefusesNestedFields pins that the deeper objects are held to their own
// defs and not only the shape's own fields are: a nested def is closed and fully
// required exactly as a shape's is, and the walk in `decodeWire` goes as deep as
// the shape does. A nested object is where the first request body a later task
// adds will sit — an organisation's terms, a reseller's rates — and a body
// encoding/json alone would accept there is one whose fields the SPA is told it
// is served.
func TestWireRefusesNestedFields(t *testing.T) {
	for _, shapeName := range wireShapeNames() {
		shape := wireShapes[shapeName]
		fixture, err := os.ReadFile(filepath.Join(wireFixtureDir, shapeName+".json"))
		if err != nil {
			t.Errorf("%s/%s.json: %v", wireFixtureDir, shapeName, err)
			continue
		}
		def := strings.TrimPrefix(shapeName, shape.area+".")
		schema := wireSchemaDef(t, shape.area, def)
		for _, nested := range wireNestedObjects(t, wireAreaDoc(t, shape.area), def, wireInstance(t, fixture), nil) {
			where := wirePathName(nested.path)

			body := wireInstance(t, fixture)
			wireAtPath(t, body, nested.path)[wireUnknownField] = true
			wireRefuses(t, shapeName, "a field no shape holds at "+where, wireJSON(t, body), wireUnknownField, schema, false)

			// A case-folded key is the shape of the fault encoding/json is loosest
			// about: it matches the field ignoring case, so a body can name a field
			// the schema does not have and still be read as the one it does.
			body = wireInstance(t, fixture)
			object := wireAtPath(t, body, nested.path)
			folded, found := "", false
			for _, name := range slices.Sorted(maps.Keys(object)) {
				if upper := strings.ToUpper(name); upper != name {
					folded, found = name, true
					break
				}
			}
			if !found {
				t.Errorf("%s: every field at %s is already upper case, so the case-folded body cannot be built", shapeName, where)
			} else {
				object[strings.ToUpper(folded)] = object[folded]
				delete(object, folded)
				wireRefuses(t, shapeName, fmt.Sprintf("%q written as %q at %s", folded, strings.ToUpper(folded), where),
					wireJSON(t, body), strings.ToUpper(folded), schema, false)
			}

			body = wireInstance(t, fixture)
			object = wireAtPath(t, body, nested.path)
			left := slices.Sorted(maps.Keys(object))[0]
			delete(object, left)
			wireRefuses(t, shapeName, fmt.Sprintf("the field %q left out at %s", left, where), wireJSON(t, body), left, schema, false)
		}
	}
}

// TestWireRefusesNullWhereAValueIsRequired pins the other way a body can name a
// field and hold nothing: encoding/json reads `null` as "leave the field alone",
// so a body that writes null where the schema requires a value arrives at a
// handler as that field's zero value — an empty version, a term that ended at the
// zero instant. Every property of every object, at every depth, is put to both
// readers as null, and both must refuse it; where the schema writes the property
// as a union with null both must accept it, which is what keeps the rule a
// reading of the two readers rather than a list of fields.
func TestWireRefusesNullWhereAValueIsRequired(t *testing.T) {
	for _, shapeName := range wireShapeNames() {
		shape := wireShapes[shapeName]
		fixture, err := os.ReadFile(filepath.Join(wireFixtureDir, shapeName+".json"))
		if err != nil {
			t.Errorf("%s/%s.json: %v", wireFixtureDir, shapeName, err)
			continue
		}
		def := strings.TrimPrefix(shapeName, shape.area+".")
		doc := wireAreaDoc(t, shape.area)
		schema := wireSchemaDef(t, shape.area, def)
		objects := append([]wireNested{{def: def}}, wireNestedObjects(t, doc, def, wireInstance(t, fixture), nil)...)
		for _, object := range objects {
			properties, ok := wireDef(t, doc, object.def, shapeName)["properties"].(map[string]any)
			if !ok {
				t.Errorf("%s: $defs/%s describes no properties, so none of them can be written as null", shapeName, object.def)
				continue
			}
			for _, name := range slices.Sorted(maps.Keys(properties)) {
				body := wireInstance(t, fixture)
				wireAtPath(t, body, object.path)[name] = nil
				where := wirePathName(append(append([]any{}, object.path...), name))
				if wireNullableProperty(properties[name]) {
					if err := decodeWire(wireJSON(t, body), shape.alloc()); err != nil {
						t.Errorf("%s: this build refuses %s as null, and the schema writes it as a union with null: %v", shapeName, where, err)
					}
					if err := schema.Validate(body); err != nil {
						t.Errorf("%s: the %s subschema refuses %s as null, so the two readers disagree: %v", shapeName, def, where, err)
					}
					continue
				}
				wireRefuses(t, shapeName, fmt.Sprintf("%s written as null", where), wireJSON(t, body), name, schema, false)
			}
		}
	}
}

// TestWireHoldsTheShapeAndNotTheValues pins the split `decodeWire`'s comment
// draws, so the comment cannot drift from what the function does: the walk holds
// a body to the schema's shape — its fields, each once, no null where a value is
// required — and what the values say is each handler's own check. Every case is
// a body that shape's subschema refuses and this build reads without complaint;
// the first two are F41's, the pattern that keeps a project id safe to put in a
// host name, which is exactly what a decoder that checked values would have to
// refuse.
func TestWireHoldsTheShapeAndNotTheValues(t *testing.T) {
	cases := []struct {
		shape string
		path  []any
		value any
		why   string
	}{
		{"workspaces.bind", []any{"project_id"}, "evil.example.com/../x", "a project id outside the pattern that keeps it safe in a host name (F41)"},
		{"ops.project_transfer", []any{"project_id"}, "evil.example.com/../x", "the same id arriving on the operations route (F41)"},
		{"billing.seats", []any{"seats"}, 0, "a seat count below the schema's minimum"},
		{"invites.create", []any{"role"}, "superowner", "a role the schema's enum does not name"},
		{"invites.create", []any{"email"}, "not an address", "an address outside the schema's pattern"},
		{"workspaces.bind", []any{"entitlement_id"}, "", "an empty id where the schema forbids an empty one"},
		{"orgs.create", []any{"name"}, strings.Repeat("x", 65), "a name past the schema's maxLength"},
		{"reports.month", []any{"month"}, "2026-13", "a month the schema's pattern does not name"},
		{"ledger.entry", []any{"currency"}, "usd", "a currency outside the schema's pattern"},
		{"statements.payment", []any{"amount_micros"}, 0, "an amount below the schema's minimum"},
		{"resellers.reseller", []any{"rates", 0, "daily_rate_micros"}, -1, "a rate below the schema's minimum, one level down"},
	}
	for _, c := range cases {
		shape, ok := wireShapes[c.shape]
		if !ok {
			t.Errorf("no shape is registered as %s, so the case about %s is never asked", c.shape, c.why)
			continue
		}
		fixture, err := os.ReadFile(filepath.Join(wireFixtureDir, c.shape+".json"))
		if err != nil {
			t.Errorf("%s/%s.json: %v", wireFixtureDir, c.shape, err)
			continue
		}
		body := wireInstance(t, fixture)
		object := wireAtPath(t, body, c.path[:len(c.path)-1])
		object[c.path[len(c.path)-1].(string)] = c.value
		def := strings.TrimPrefix(c.shape, shape.area+".")
		if err := wireSchemaDef(t, shape.area, def).Validate(body); err == nil {
			t.Errorf("%s: the %s subschema accepts %v as %s, so this case no longer shows a value the schema refuses",
				c.shape, def, c.value, c.why)
			continue
		}
		if err := decodeWire(wireJSON(t, body), shape.alloc()); err != nil {
			t.Errorf("%s: this build now refuses %v as %s, so it no longer holds only the shape and the comment above has to be rewritten to say what it holds: %v",
				c.shape, c.value, c.why, err)
		}
	}
}

// wireNested is one object inside a shape's fixture with the def that describes
// it: the path to it — a field name, then an index per list element — and the
// subschema a body there is read against.
type wireNested struct {
	path []any
	def  string
}

// wireNestedObjects collects every object below the shape's own that a $defs
// entry describes rather than the shape itself: the value of a property that
// names a def, and each element of a list of them, at any depth. The shape's own
// object is not one of them, because the tests that call this already ask about
// it.
func wireNestedObjects(t *testing.T, doc map[string]any, def string, node any, path []any) []wireNested {
	t.Helper()
	object, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	described, ok := wireDef(t, doc, def, def)["properties"].(map[string]any)
	if !ok {
		return nil
	}
	var found []wireNested
	for _, name := range slices.Sorted(maps.Keys(described)) {
		target, ok := wireRefTarget(described[name])
		if !ok {
			continue
		}
		here := append(append([]any{}, path...), name)
		switch value := object[name].(type) {
		case map[string]any:
			found = append(found, wireNested{path: here, def: target})
			found = append(found, wireNestedObjects(t, doc, target, value, here)...)
		case []any:
			for i, element := range value {
				inside := append(append([]any{}, here...), i)
				if _, ok := element.(map[string]any); !ok {
					continue
				}
				found = append(found, wireNested{path: inside, def: target})
				found = append(found, wireNestedObjects(t, doc, target, element, inside)...)
			}
		}
	}
	return found
}

// wireAtPath is the object one mutation is applied to, in a body parsed fresh
// from the fixture so every mutation starts from the body the fixture stands for.
func wireAtPath(t *testing.T, body any, path []any) map[string]any {
	t.Helper()
	node := body
	for _, step := range path {
		switch key := step.(type) {
		case string:
			object, ok := node.(map[string]any)
			if !ok {
				t.Fatalf("the fixture holds no object at %s", wirePathName(path))
			}
			node = object[key]
		case int:
			list, ok := node.([]any)
			if !ok || key >= len(list) {
				t.Fatalf("the fixture holds no list entry at %s", wirePathName(path))
			}
			node = list[key]
		}
	}
	object, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("the fixture holds no object at %s", wirePathName(path))
	}
	return object
}

// wirePathName spells a path the way a reader of the body reads it, so a failure
// names the object it was about: `rates[0].daily_rate_micros`.
func wirePathName(path []any) string {
	var at strings.Builder
	for _, step := range path {
		switch value := step.(type) {
		case string:
			if at.Len() > 0 {
				at.WriteString(".")
			}
			at.WriteString(value)
		case int:
			fmt.Fprintf(&at, "[%d]", value)
		}
	}
	return at.String()
}

// wireNullableProperty reports whether a property's own subschema admits null:
// written as a union with null, or as an anyOf/oneOf whose branches include it,
// which is how a nullable object beside a $ref is spelled.
func wireNullableProperty(property any) bool {
	object, ok := property.(map[string]any)
	if !ok {
		return false
	}
	if names, ok := object["type"].([]any); ok && slices.Contains(names, "null") {
		return true
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, _ := object[key].([]any)
		for _, branch := range branches {
			if inner, ok := branch.(map[string]any); ok && inner["type"] == "null" {
				return true
			}
		}
	}
	return false
}

// wireRefuses asks both readers for a body neither should accept, and asks the
// decoder's refusal to name what it refused: one that refused for another reason
// would look like the case being covered and hold the line nowhere. onlyGo is for
// a fault a schema cannot be asked about at all.
func wireRefuses(t *testing.T, shapeName, what string, body []byte, want string, schema *jsonschema.Schema, onlyGo bool) {
	t.Helper()
	err := decodeWire(body, wireShapes[shapeName].alloc())
	if err == nil {
		t.Errorf("%s: this build reads a body with %s into %T, so the shape accepts what the schema refuses",
			shapeName, what, wireShapes[shapeName].alloc())
		return
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%s: this build refuses a body with %s, and not for %s: %v", shapeName, what, want, err)
	}
	if onlyGo {
		return
	}
	if err := schema.Validate(wireInstance(t, body)); err == nil {
		t.Errorf("%s: the schema accepts a body with %s, which this build refuses, so the two readers disagree", shapeName, what)
	}
}

// wireJSON is one mutated fixture as the body a route would be handed.
func wireJSON(t *testing.T, body any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("a mutated fixture is not JSON: %v", err)
	}
	return raw
}

// wireCredentialTags are the words that name a credential, or the way to one.
// The list is the whole point of the test below: it is the vocabulary a shape
// would have to grow to leak, and a shape that grew one is refused here rather
// than in review. The words are matched, not the field names that use them
// today: `token` is the wording the renewal path would reach for, and
// `renewal_token` is the name a shape would actually grow.
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

// wireCredentialProbe is a type no shape has, so the vocabulary can be asked
// about the names the shapes have not grown yet. `renewal_token` is the name the
// renewal path would grow — the shape the portal serves a reseller and the
// operations team (F5) — and `key_id` is the key's name, which every metadata
// answer carries; the probe is what tells the two apart.
type wireCredentialProbe struct {
	RenewalToken string              `json:"renewal_token"`
	Token        string              `json:"token"`
	APIKey       string              `json:"api_key"`
	KeyID        string              `json:"key_id"`
	TermsVersion string              `json:"terms_version"`
	Nested       *wireCredentialNest `json:"nested"`
}

type wireCredentialNest struct {
	PrivateKey string `json:"private_key"`
}

// TestWireCredentialNamesMatchByTheirWords pins the vocabulary's rule where the
// shapes cannot yet show it: one field per spelling a credential would arrive
// under, so a name that only ends in a word the vocabulary holds cannot pass as
// metadata the way `renewal_token` did before this test existed (F5).
func TestWireCredentialNamesMatchByTheirWords(t *testing.T) {
	found := wireCredentialFields(reflect.TypeOf(wireCredentialProbe{}), 0)
	want := []string{"renewal_token", "token", "api_key", "nested.private_key"}
	if !slices.Equal(found, want) {
		t.Errorf("the credential vocabulary finds %v in a type carrying %v, and it has to find exactly those: a name whose last word names a credential is one a shape may not serve, and `key_id`/`terms_version` are not", found, want)
	}
}

// wireCredentialName reports whether a JSON field name names a credential: one
// of wireCredentialTags on its own (`token`), or as the last word of a compound
// name (`renewal_token`, `private_key`). A name that only mentions one is not a
// credential, which is why `key_id` — the key's name, served with every metadata
// answer — does not match: the word has to be the whole of the name's last part.
func wireCredentialName(name string) bool {
	if slices.Contains(wireCredentialTags, name) {
		return true
	}
	for _, tag := range wireCredentialTags {
		if strings.HasSuffix(name, "_"+tag) {
			return true
		}
	}
	return false
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
		if wireCredentialName(name) {
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

// wireAssertSameFields refuses a field one reader has and the other does not, at
// every level: a Go field a subschema does not describe is a field the SPA
// cannot know is served, and a property no Go field reads is one the API drops
// however faithfully the schema promises it. It follows a property that holds
// another object into the def that object is described by, because those defs —
// an organisation's terms, a reseller's rates, a statement's lines — are not
// registered shapes, so no fixture and no other check reaches them.
func wireAssertSameFields(t *testing.T, shapeName, area, def string, value any) {
	t.Helper()
	wireSameFields(t, shapeName, reflect.TypeOf(value).Elem(), wireAreaDoc(t, area), def)
}

// wireSameFields compares one Go type with one subschema of an area's file, in
// both directions, and goes a level deeper where a property names another def.
// at is the path a failure prints, spelled as the SPA reads the shape.
func wireSameFields(t *testing.T, at string, typ reflect.Type, doc map[string]any, def string) {
	t.Helper()
	described, ok := wireDef(t, doc, def, at)["properties"].(map[string]any)
	if !ok {
		t.Errorf("%s: $defs/%s describes no properties, so nothing under it is compared", at, def)
		return
	}
	held := make(map[string]reflect.Type, typ.NumField())
	wireFieldTypes(typ, held)
	for _, name := range slices.Sorted(maps.Keys(described)) {
		field, ok := held[name]
		if !ok {
			t.Errorf("%s: the schema describes a %q property this build reads nothing into, so a route would drop a field the SPA is told it is served",
				at, name)
			continue
		}
		delete(held, name)
		nested, ok := wireRefTarget(described[name])
		if !ok {
			continue
		}
		field = wireElement(field)
		if field.Kind() != reflect.Struct {
			t.Errorf("%s.%s: the schema holds an object this build reads as %s, so one of the two has the wrong shape", at, name, field)
			continue
		}
		wireSameFields(t, at+"."+name, field, doc, nested)
	}
	for _, name := range slices.Sorted(maps.Keys(held)) {
		t.Errorf("%s: this build reads a %q field the schema does not describe, so the SPA cannot know it is served", at, name)
	}
}

// wireElement is the type a field holds once its pointers and slices are
// unwrapped, so an object, a pointer to one and a list of them compare alike.
func wireElement(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t
}

// wireRefTarget is the def one property's value is described by, and false for a
// property that holds no object: a $ref of its own, a list whose items are one,
// or a nullable object, whose anyOf carries the $ref beside its null branch.
func wireRefTarget(property any) (string, bool) {
	object, ok := property.(map[string]any)
	if !ok {
		return "", false
	}
	if def, ok := wireRef(object); ok {
		return def, true
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, _ := object[key].([]any)
		for _, branch := range branches {
			if def, ok := wireRef(branch); ok {
				return def, true
			}
		}
	}
	return wireRef(object["items"])
}

// wireRef is one subschema's own reference to a def of the same file, if it has
// one.
func wireRef(node any) (string, bool) {
	object, ok := node.(map[string]any)
	if !ok {
		return "", false
	}
	ref, ok := object["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, wireDefPrefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, wireDefPrefix), true
}

// wireDef is one subschema of an area's file by name.
func wireDef(t *testing.T, doc map[string]any, def, at string) map[string]any {
	t.Helper()
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("%s: the file holds no $defs, so $defs/%s is not there", at, def)
	}
	object, ok := defs[def].(map[string]any)
	if !ok {
		t.Fatalf("%s: the file holds no $defs/%s, so no subschema describes that shape", at, def)
	}
	return object
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

// wireAssertDefsAreReached refuses a def nothing refers to. A def is reached
// from the root oneOf — the list a reader takes for the area's shapes — through
// the properties that name it, and a def outside that closure is described by
// the file and read by nobody: no fixture stands for it, this guard never
// compares it, and it is what a shape left behind after its branch was removed
// looks like.
func wireAssertDefsAreReached(t *testing.T, file, area string) {
	t.Helper()
	doc := wireAreaDoc(t, area)
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Errorf("%s holds no $defs, so it describes no shape at all", file)
		return
	}
	reached := make(map[string]bool, len(defs))
	var reach func(def string)
	reach = func(def string) {
		if reached[def] {
			return
		}
		reached[def] = true
		below := make(map[string]bool)
		wireRefsUnder(defs[def], below)
		for _, next := range slices.Sorted(maps.Keys(below)) {
			reach(next)
		}
	}
	for _, branch := range wireArray(t, file, doc["oneOf"]) {
		named := make(map[string]bool)
		wireRefsUnder(branch, named)
		for _, def := range slices.Sorted(maps.Keys(named)) {
			reach(def)
		}
	}
	for _, def := range slices.Sorted(maps.Keys(defs)) {
		if !reached[def] {
			t.Errorf("%s: $defs/%s is described and nothing refers to it, so nothing validates against it and no fixture stands for it", file, def)
		}
	}
}

// wireRefsUnder collects every def one subschema refers to, wherever the
// reference sits, which is what decides whether a def is reached.
func wireRefsUnder(node any, into map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		if def, ok := wireRef(value); ok {
			into[def] = true
		}
		for _, child := range value {
			wireRefsUnder(child, into)
		}
	case []any:
		for _, child := range value {
			wireRefsUnder(child, into)
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
