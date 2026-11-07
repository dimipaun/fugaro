package workspace_test

// The workspace record, held to the promises its task is written for: it
// decodes strictly (an unknown field is an error, as every config and wire
// format here does), it holds money as integer micro-dollars from Parse
// onward, and it never grows back the two fields the security re-read removed
// -- an identity provider and a service account are each member's own pin, in
// that member's own config, and never something the workspace states about a
// member (docs/workspaces/spec.md §2).

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

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// TestRecordRefusesAnUnknownField is the test this task exists for: every
// config and wire format here decodes strictly, and a record that silently
// accepted a field nobody decided would be a shape two writers could disagree
// about -- the writer that put the key there, and the reader that has no rule
// for it. The three cases are the three places a key can be added: the record,
// a member, and the cap.
func TestRecordRefusesAnUnknownField(t *testing.T) {
	for _, tc := range []struct{ name, record, field string }{
		{"a key at the top level", "name: workspace-alpha\nmembers: []\nowner: person-1\n", "owner"},
		{"a key inside a member", "name: workspace-alpha\nmembers:\n  - project: repo-a\n    repos: [owner/repo-a]\n    role: admin\n", "role"},
		{"a key inside the cap", "name: workspace-alpha\nmembers: []\ncap:\n  monthly: 250\n  currency: usd\n", "currency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := workspace.Parse([]byte(tc.record))
			if err == nil {
				t.Fatalf("Parse accepted\n%s\nand %q is not a key of a workspace record: a field nobody decided is a shape two writers can disagree about", tc.record, tc.field)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("Parse refused it with %q, which does not name %q: the writer has to be able to see which key is not one of the record's", err, tc.field)
			}
		})
	}
}

// TestRecordRefusesAMemberListThatIsNotSortedUnique: the member list is a set
// written as a list, so its order is the canonical one and a project appears
// once. A second list with the same projects in another order is two records
// that compare unequal while saying the same thing, and the published node the
// workspace's own service reads is built from it (W2).
func TestRecordRefusesAMemberListThatIsNotSortedUnique(t *testing.T) {
	for _, tc := range []struct{ name, record, at string }{
		{"two members out of order",
			"members:\n  - project: repo-b\n    repos: [owner/repo-b]\n  - project: repo-a\n    repos: [owner/repo-a]\n", "members[1]"},
		{"the same project twice",
			"members:\n  - project: repo-a\n    repos: [owner/repo-a]\n  - project: repo-a\n    repos: [owner/repo-a]\n", "members[1]"},
		{"a member's repositories out of order",
			"members:\n  - project: repo-a\n    repos: [owner/repo-b, owner/repo-a]\n", "repos[1]"},
		{"a member's repository twice",
			"members:\n  - project: repo-a\n    repos: [owner/repo-a, owner/repo-a]\n", "repos[1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "name: workspace-alpha\n" + tc.record
			_, err := workspace.Parse([]byte(raw))
			if err == nil {
				t.Fatalf("Parse accepted\n%s\nand the list is sorted with no duplicate: a set written in two orders is two records that say the same thing and compare unequal", raw)
			}
			if !strings.Contains(err.Error(), tc.at) {
				t.Errorf("Parse refused it with %q, which does not name %s: the refusal has to say where the list went wrong", err, tc.at)
			}
		})
	}

	// The control: the same two members in order, whose repositories are
	// canonical, are a record.
	if _, err := workspace.Parse([]byte("name: workspace-alpha\nmembers:\n  - project: repo-a\n    repos: [owner/repo-a, owner/repo-b]\n  - project: repo-b\n    repos: [owner/repo-b]\n")); err != nil {
		t.Errorf("Parse refused a sorted, unique member list: %v", err)
	}
}

// TestRecordNameUsesTheProjectNameGrammar (F21): the record reuses
// config.ProjectNameRE rather than inventing a second grammar, and a member's
// project name is a project name too. A second grammar is a second thing two
// writers could disagree about -- the CLI that writes a name and the decoder
// that reads one.
func TestRecordNameUsesTheProjectNameGrammar(t *testing.T) {
	for _, name := range []string{
		"workspace-alpha", "a", "w0", "workspace-1", strings.Repeat("w", 40),
		"workspace-", "-workspace", "Workspace", "workspace_alpha", "workspace.alpha",
		strings.Repeat("w", 41), "",
	} {
		raw := fmt.Sprintf("name: %q\nmembers: []\n", name)
		_, err := workspace.Parse([]byte(raw))
		if want := config.ProjectNameRE.MatchString(name); (err == nil) != want {
			t.Errorf("Parse accepted the name %q = %v, and config.ProjectNameRE matches it = %v: the record's name is a project name and not a second grammar (F21)", name, err == nil, want)
		}
	}
	for _, name := range []string{"repo-a", "a", "Repo-A", "repo_a", "repo.a", ""} {
		raw := fmt.Sprintf("name: workspace-alpha\nmembers:\n  - project: %q\n    repos: []\n", name)
		_, err := workspace.Parse([]byte(raw))
		if want := config.ProjectNameRE.MatchString(name); (err == nil) != want {
			t.Errorf("Parse accepted the member project %q = %v, and config.ProjectNameRE matches it = %v: a member is a Fugaro project, so its name is a project name", name, err == nil, want)
		}
	}
}

// TestRecordRefusesAnIdentityOrServiceAccountField: the record carries the
// members and the cap, and neither an identity provider nor a service account
// (spec.md §2). Both are the member's own pin, in the member's own config, so
// the record has no key for either -- and a later writer that reached for one
// "helpfully" fails here first, at the decode, rather than publishing a
// workspace that aims a member's exchange or a member's database grant.
func TestRecordRefusesAnIdentityOrServiceAccountField(t *testing.T) {
	for _, tc := range []struct{ name, record, field string }{
		{"an identity block", "name: workspace-alpha\nmembers: []\nidentity:\n  provider: google.com\n", "identity"},
		{"an identity provider", "name: workspace-alpha\nmembers: []\nidentity_provider: google.com\n", "identity_provider"},
		{"a service account", "name: workspace-alpha\nmembers: []\nservice_account: marker-workspace-account@example.iam.gserviceaccount.com\n", "service_account"},
		{"a service account inside a member", "name: workspace-alpha\nmembers:\n  - project: repo-a\n    repos: []\n    service_account: marker-job-email\n", "service_account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := workspace.Parse([]byte(tc.record))
			if err == nil {
				t.Fatalf("Parse accepted\n%s\nand the record names no identity provider and no service account: both are the member's own pin, and a record that could supply either could aim a member's exchange and a member's grant at a party of its choosing", tc.record)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("Parse refused it with %q, which does not name %q", err, tc.field)
			}
		})
	}
}

// TestRecordRefusesACapThatIsNotPositiveWhenSet: the cap block is the claim
// that there is a ceiling, so a block with no positive amount in it is refused
// rather than read as no cap -- a ceiling nobody can read is a ceiling nobody
// is held to. Stating none is leaving the block out, or writing it as null,
// which is the convention every optional block in this program follows.
func TestRecordRefusesACapThatIsNotPositiveWhenSet(t *testing.T) {
	for _, tc := range []struct{ name, cap string }{
		{"zero", "cap:\n  monthly: 0\n"},
		{"an amount that rounds to nothing", "cap:\n  monthly: 0.0000004\n"},
		{"a block with no amount", "cap: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "name: workspace-alpha\nmembers: []\n" + tc.cap
			if _, err := workspace.Parse([]byte(raw)); err == nil {
				t.Errorf("Parse accepted\n%s\nand a cap that is set must be more than zero", raw)
			}
		})
	}

	// And the state no file produces, which only a caller writing a record it
	// built itself can reach: an amount with no cap set is a ceiling nothing
	// reads, so Validate refuses it rather than letting one side apply a cap
	// the other side says is absent.
	r := &workspace.Record{Name: "workspace-alpha", Cap: workspace.Cap{Monthly: 250_000_000}}
	if err := r.Validate(); err == nil {
		t.Error("Validate accepted a cap amount with Set false: the record states no cap, so it carries no amount")
	}
}

// TestCapIsExactMicrosAndNotAFloat: the file writes dollars and Parse converts
// once with pricing.FromUSD, so a cap of 0.1 is exactly 100,000 µ$ and every
// reader below this point works in integer micro-dollars -- the unit the
// budget database stores, and the one no float can make inexact. The type is
// checked as well as the value: a float64 field is the mistake this test
// exists to make impossible, and one would pass a value comparison.
func TestCapIsExactMicrosAndNotAFloat(t *testing.T) {
	for _, tc := range []struct {
		monthly string
		want    pricing.Micros
	}{
		{"0.1", 100_000},
		{"250", 250_000_000},
		{"1.25", 1_250_000},
		{"0.000001", 1},
	} {
		r, err := workspace.Parse([]byte("name: workspace-alpha\nmembers: []\ncap:\n  monthly: " + tc.monthly + "\n"))
		if err != nil {
			t.Fatalf("Parse refused monthly: %s: %v", tc.monthly, err)
		}
		if !r.Cap.Set {
			t.Fatalf("Cap.Set = false for a record that writes monthly: %s", tc.monthly)
		}
		if r.Cap.Monthly != tc.want {
			t.Errorf("monthly: %s parsed to %d µ$, want %d", tc.monthly, r.Cap.Monthly, tc.want)
		}
	}

	got := reflect.TypeOf(workspace.Cap{}.Monthly)
	if got.Kind() != reflect.Int64 {
		t.Errorf("workspace.Cap.Monthly is a %s, and money below Parse is integer micro-dollars (pricing.Micros, an int64)", got.Kind())
	}
	if want := reflect.TypeOf(pricing.Micros(0)); got != want {
		t.Errorf("workspace.Cap.Monthly is %s, and the unit is %s (internal/pricing/pricing.go), which is what the budget database stores", got, want)
	}
}

// TestRecordRoundTripsThroughTheSchema: schemas/workspace.schema.json and the
// struct agree. The schema is what an editor completes a file from and what a
// repository's CI reads a published record with, so a key one side knows and
// the other does not is a file that reads as valid in one place and is refused
// in the other. The fixture is the round trip both ways: the file the schema
// admits is the file Parse reads, and the file this package writes is one the
// schema admits and Parse reads back unchanged.
func TestRecordRoundTripsThroughTheSchema(t *testing.T) {
	sch := wsSchema(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "record-a.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := wsValidate(sch, raw); err != nil {
		t.Fatalf("schemas/workspace.schema.json refuses the fixture testdata/record-a.yaml: %v\n%s", err, raw)
	}
	rec, err := workspace.Parse(raw)
	if err != nil {
		t.Fatalf("Parse refuses the fixture testdata/record-a.yaml: %v\n%s", err, raw)
	}
	if rec.Name != "workspace-alpha" || len(rec.Members) != 2 || !rec.Cap.Set {
		t.Fatalf("testdata/record-a.yaml parsed to %+v, and the fixture states workspace-alpha, two members and a cap", rec)
	}

	// The struct's own file form, written back out.
	out, err := workspace.FileForm(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := wsValidate(sch, out); err != nil {
		t.Fatalf("schemas/workspace.schema.json refuses the file workspace.FileForm writes: %v\n%s", err, out)
	}
	back, err := workspace.Parse(out)
	if err != nil {
		t.Fatalf("this package writes a file it does not read: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(rec, back) {
		t.Errorf("the record did not round trip:\n got %+v\nwant %+v\nfrom\n%s", back, rec, out)
	}

	// Every key the struct writes is declared and every key the schema
	// declares is one the struct writes, at each of the three objects. The
	// closed schema is the strict decoder's own promise: Parse refuses an
	// unknown key anywhere (TestRecordRefusesAnUnknownField), so a schema that
	// admitted one would complete a key the decoder refuses.
	var doc map[string]any
	schemaRaw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(schemaRaw, &doc); err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := yaml.Unmarshal(out, &written); err != nil {
		t.Fatal(err)
	}
	wsAgrees(t, doc, written, "the record")
	wsAgrees(t, doc, wsMemberOf(t, written), "$defs.member", "$defs", "member")
	wsAgrees(t, doc, wsCapOf(t, written), "$defs.cap", "$defs", "cap")
	if got := slices.Sorted(slices.Values(wsStrings(t, doc, "required"))); !slices.Equal(got, []string{"members", "name"}) {
		t.Errorf("the schema requires %v, and the file form always writes the name and the member list (the cap is optional: a record that states none leaves it out)", got)
	}
	if got := slices.Sorted(slices.Values(wsStrings(t, doc, "$defs", "member", "required"))); !slices.Equal(got, []string{"project", "repos"}) {
		t.Errorf("the member schema requires %v, and a member is its project and its repositories", got)
	}
	if got := slices.Sorted(slices.Values(wsStrings(t, doc, "$defs", "cap", "required"))); !slices.Equal(got, []string{"monthly"}) {
		t.Errorf("the cap schema requires %v, and a cap that is written states its amount", got)
	}
	for _, at := range [][]string{{"additionalProperties"}, {"$defs", "member", "additionalProperties"}, {"$defs", "cap", "additionalProperties"}} {
		if closed, ok := wsNode(t, doc, at...).(bool); !ok || closed {
			t.Errorf("schemas/workspace.schema.json's %s is %v, and Parse decodes strictly: the schema has to refuse the unknown key the decoder refuses", strings.Join(at, "."), closed)
		}
	}

	// The cap's bounds are FromUSD's, so the two sides give the same answer
	// about the amounts a writer can put in the file: above pricing.MaxUSD,
	// and below the smallest amount that is a whole micro-dollar.
	monthly, ok := wsNode(t, doc, "$defs", "cap", "properties", "monthly").(map[string]any)
	if !ok {
		t.Fatal("schemas/workspace.schema.json's $defs.cap.properties.monthly is not an object")
	}
	if monthly["maximum"] != float64(pricing.MaxUSD) {
		t.Errorf("the schema's cap maximum is %v, and pricing.FromUSD refuses anything above %d", monthly["maximum"], pricing.MaxUSD)
	}
	if monthly["minimum"] != 0.5/1e6 {
		t.Errorf("the schema's cap minimum is %v, and %v is the smallest amount that is one whole micro-dollar (pricing.Micros)", monthly["minimum"], 0.5/1e6)
	}
	for _, tc := range []struct {
		monthly string
		ok      bool
	}{
		{"0.0000005", true},
		{"0.0000004", false},
		{"100000", true},
		{"100000.5", false},
		{"-1", false},
	} {
		capped := []byte("name: workspace-alpha\nmembers: []\ncap:\n  monthly: " + tc.monthly + "\n")
		_, perr := workspace.Parse(capped)
		verr := wsValidate(sch, capped)
		if (perr == nil) != tc.ok || (verr == nil) != tc.ok {
			t.Errorf("monthly: %s: Parse accepted = %v, the schema accepted = %v, want %v", tc.monthly, perr == nil, verr == nil, tc.ok)
		}
	}

	// And the two ways a record states no cap, which both sides read the same
	// way: the block left out, and the block written as null.
	for _, tc := range []struct{ name, cap string }{
		{"the block left out", ""},
		{"the block written as null", "cap:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("name: workspace-alpha\nmembers: []\n" + tc.cap)
			if err := wsValidate(sch, raw); err != nil {
				t.Errorf("schemas/workspace.schema.json refuses a record that states no cap: %v", err)
			}
			r, err := workspace.Parse(raw)
			if err != nil {
				t.Fatalf("Parse refused a record that states no cap: %v", err)
			}
			if r.Cap.Set {
				t.Errorf("Cap.Set = true for a record that states no cap (Monthly = %d)", r.Cap.Monthly)
			}
		})
	}

	// And the keys the schema requires, and the one entry shape it refuses
	// inside a repository list, held to the same two-sided rule. The two lists
	// are required keys and not optional ones: a list left out is refused here
	// rather than read as a list stated empty, because FileForm writes both
	// keys, and a nil list read as an empty one would not survive the round
	// trip the test above makes -- the record would come back with a list where
	// it had none.
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"a record with no member list", "name: workspace-alpha\n", false},
		{"a record that states no members", "name: workspace-alpha\nmembers: []\n", true},
		{"a member with no repository list", "name: workspace-alpha\nmembers:\n  - project: repo-a\n", false},
		{"a member that contributes no repository", "name: workspace-alpha\nmembers:\n  - project: repo-a\n    repos: []\n", true},
		{"a member with an empty repository", "name: workspace-alpha\nmembers:\n  - project: repo-a\n    repos: ['']\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			rec, perr := workspace.Parse(raw)
			verr := wsValidate(sch, raw)
			if (perr == nil) != tc.ok || (verr == nil) != tc.ok {
				t.Fatalf("Parse accepted = %v (%v), the schema accepted = %v, want %v\n%s", perr == nil, perr, verr == nil, tc.ok, raw)
			}
			if !tc.ok {
				return
			}
			out, err := workspace.FileForm(rec)
			if err != nil {
				t.Fatal(err)
			}
			back, err := workspace.Parse(out)
			if err != nil {
				t.Fatalf("this package writes a file it does not read: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(rec, back) {
				t.Errorf("the record did not round trip:\n got %+v\nwant %+v\nfrom\n%s", back, rec, out)
			}
		})
	}
}

// wsAgrees holds the keys of one object of the file form against the
// properties the schema declares for it, in both directions.
func wsAgrees(t *testing.T, doc map[string]any, written map[string]any, label string, at ...string) {
	t.Helper()
	props, ok := wsNode(t, doc, append(slices.Clone(at), "properties")...).(map[string]any)
	if !ok {
		t.Fatalf("schemas/workspace.schema.json's %s.properties is not an object", strings.Join(at, "."))
	}
	got, want := slices.Sorted(maps.Keys(written)), slices.Sorted(maps.Keys(props))
	if !slices.Equal(got, want) {
		t.Errorf("the file form writes %v and the schema declares %v for %s: a key one side knows and the other does not is a file that is valid in one place and refused in the other", got, want, label)
	}
}

// wsMemberOf is the first member of an unmarshalled record, and wsCapOf its
// cap: the two nested objects the schema's $defs describe.
func wsMemberOf(t *testing.T, written map[string]any) map[string]any {
	t.Helper()
	list, ok := written["members"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("the file form writes no member list: %v", written["members"])
	}
	m, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("a member of the file form is not an object: %v", list[0])
	}
	return m
}

func wsCapOf(t *testing.T, written map[string]any) map[string]any {
	t.Helper()
	c, ok := written["cap"].(map[string]any)
	if !ok {
		t.Fatalf("the file form writes no cap object: %v", written["cap"])
	}
	return c
}

// wsNode walks a decoded schema document to the object at path.
func wsNode(t *testing.T, doc map[string]any, path ...string) any {
	t.Helper()
	var cur any = doc
	for i, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("schemas/workspace.schema.json's %s is not an object", strings.Join(path[:i], "."))
		}
		v, ok := m[k]
		if !ok {
			t.Fatalf("schemas/workspace.schema.json has no %s", strings.Join(path[:i+1], "."))
		}
		cur = v
	}
	return cur
}

// wsStrings is the string list at a path of a decoded schema document.
func wsStrings(t *testing.T, doc map[string]any, path ...string) []string {
	t.Helper()
	list, ok := wsNode(t, doc, path...).([]any)
	if !ok {
		t.Fatalf("schemas/workspace.schema.json's %s is not a list", strings.Join(path, "."))
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("schemas/workspace.schema.json's %s holds %v, which is not a string", strings.Join(path, "."), v)
		}
		out = append(out, s)
	}
	return out
}

// wsSchema compiles schemas/workspace.schema.json under its own $id.
func wsSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/workspace.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// wsValidate validates a YAML record against the compiled schema.
func wsValidate(sch *jsonschema.Schema, raw []byte) error {
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}
