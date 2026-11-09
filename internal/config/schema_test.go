package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

// TestCatalogSchemaAcceptsTheNewKeys pins that fugaro.schema.json and the
// strict decoder agree about a workflow's catalog reference: image_name:
// names one entry of the project's catalog and needs: lists the tags a job
// needs (spec.md §Repo configuration, D1155). Two surfaces read the same
// file — the decoder a run uses and the published schema an editor or a
// repository's CI checks against — and a key one refuses while the other
// accepts is a red check nobody can reproduce. The schema carries the
// reference's grammar (its name shape, its tag shape) and the exclusion
// that keeps it beside no override, which is what makes the file readable
// before any command runs.
//
// compileABSchema and abSchemaAccepts are the package's one schema loader
// and instance reader, shared since both AB and this key set are read
// against the same two surfaces.
func TestCatalogSchemaAcceptsTheNewKeys(t *testing.T) {
	sch := compileABSchema(t, "fugaro.schema.json")
	const head = minimalRepo + "git: { provider: github }\nworkflows:\n  web:\n    commands: { build: make, test: make test }\n"
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"nothing set", "", true},
		{"image_name", "    image_name: backend\n", true},
		{"needs", "    needs: [node, postgres]\n", true},
		// D1155's precedence is first match wins, so a repository that names
		// both pins the entry and the tags are then only checked to warn.
		{"image_name and needs", "    image_name: backend\n    needs: [node]\n", true},
		{"an override and a name", "    image: { apt: [jq] }\n    image_name: backend\n", false},
		{"an override and needs", "    image: { apt: [jq] }\n    needs: [node]\n", false},
		{"a dockerfile and a name", "    dockerfile: .fugaro/web.Dockerfile\n    image_name: backend\n", false},
		{"a dockerfile and needs", "    dockerfile: .fugaro/web.Dockerfile\n    needs: [node]\n", false},
		{"an upper-case name", "    image_name: Backend\n", false},
		{"a name over 40 characters", "    image_name: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", false},
		{"a tag with a space", "    needs: [\"postgres redis\"]\n", false},
		{"the same tag twice", "    needs: [node, node]\n", false},
		{"checkout clone with a reference", "    checkout: clone\n    needs: [node]\n", false},
		// An empty value means none on both surfaces, which `needs: []` and
		// `image_name: ""` pin on their own below: `dockerfile:` was the third
		// row of that pair and is a removed key now, refused by name whatever it
		// is set beside.
		{"a dockerfile and an empty needs", "    dockerfile: .fugaro/web.Dockerfile\n    needs: []\n", false},
		{"a dockerfile and an empty name", "    dockerfile: .fugaro/web.Dockerfile\n    image_name: \"\"\n", false},
		{"an empty needs alone", "    needs: []\n", true},
		{"an empty name alone", "    image_name: \"\"\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := head + tc.body
			_, ps := Parse([]byte(text))
			if got := len(ps) == 0; got != tc.ok {
				t.Fatalf("the decoder accepted = %v, want %v (problems: %v)", got, tc.ok, ps)
			}
			if got := abSchemaAccepts(t, sch, text); got != tc.ok {
				t.Errorf("fugaro.schema.json accepted = %v, want %v: the schema and the decoder disagree", got, tc.ok)
			}
		})
	}
}

// TestSecurityKeySchemaAgreesWithTheDecoder is TestCatalogSchemaAcceptsTheNewKeys'
// shape for SC7's block (D1501, D1502, D1503): one table of files, each read by
// the decoder a run uses and by fugaro.schema.json, the two surfaces a
// repository's CI and an editor see. A case they disagree about is a red check
// nobody can reproduce — a file the published schema calls valid and the run
// refuses, or worse the other way round: a pattern the schema blesses and the
// matcher can never fire.
//
// The cases are the ones both surfaces can judge. Three of the decoder's
// refusals are not expressible in JSON Schema and are named in $defs.
// securityPattern's own description instead: a glob doublestar itself cannot
// read and a glob holding an alternation, both refused by
// sensitivity.Pattern.Validate at the row's own index before a run ever reads
// the file, and a name repeated inside one list, refused by
// config.validateSecurity, which reads the whole list where a row's own schema
// reads one row. A fourth refusal is made here as well: a name the engine's own
// floor already carries ($defs.securityFloorName), which is a list of names and
// so the one rule of validateSecurity this file can state —
// TestSecurityFloorNamesAgreeWithGo holds that list to sensitivity.Floor.
func TestSecurityKeySchemaAgreesWithTheDecoder(t *testing.T) {
	sch := compileABSchema(t, "fugaro.schema.json")
	// A row of the engine's own floor, taken from the table rather than spelled
	// here: the case is about the name being the floor's, whichever row the
	// floor holds first.
	floorName := sensitivity.Floor()[0].Name
	const head = minimalRepo + `git: { provider: github }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"nothing set", "", true},
		{"one row", "security:\n  patterns:\n    - { name: payments, match: \"payments/**\", category: credentials }\n", true},
		{"all seven categories", "security:\n  patterns:\n    - { name: a, match: \"a/**\", category: credentials }\n    - { name: b, match: \"b/**\", category: guards }\n    - { name: c, match: \"c/**\", category: targets }\n    - { name: d, match: \"d/**\", category: infrastructure }\n    - { name: e, match: \"e/**\", category: prompts }\n    - { name: f, match: \"f/**\", category: CI }\n    - { name: g, match: \"g/**\", category: artifacts }\n", true},
		// D1506's inheritance, as a file writes it: the key present and
		// empty is a layer that adds nothing, and the floor is what a run
		// then has. Neither spelling may be refused, or a repository could
		// not say "the project's list is the whole of mine".
		{"an empty block", "security: {}\n", true},
		{"an empty list", "security:\n  patterns: []\n", true},
		// A row whose tree does not exist yet (D1503): the schema cannot
		// know the tree, and the decoder must not refuse it either.
		{"a tree that does not exist", "security:\n  patterns:\n    - { name: search, match: \"internal/search/**\", category: guards }\n", true},
		{"an unknown category", "security:\n  patterns:\n    - { name: deploy, match: \"deploy/**\", category: deployment }\n", false},
		{"a category in the wrong case", "security:\n  patterns:\n    - { name: deploy, match: \"deploy/**\", category: Credentials }\n", false},
		{"a row with no category", "security:\n  patterns:\n    - { name: deploy, match: \"deploy/**\" }\n", false},
		{"a row with no name", "security:\n  patterns:\n    - { match: \"deploy/**\", category: targets }\n", false},
		{"a row with an empty name", "security:\n  patterns:\n    - { name: \"\", match: \"deploy/**\", category: targets }\n", false},
		{"a name with a slash", "security:\n  patterns:\n    - { name: a/b, match: \"deploy/**\", category: targets }\n", false},
		{"a name over 40 characters", "security:\n  patterns:\n    - { name: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, match: \"deploy/**\", category: targets }\n", false},
		{"an empty match", "security:\n  patterns:\n    - { name: deploy, match: \"\", category: targets }\n", false},
		{"a match of *", "security:\n  patterns:\n    - { name: all, match: \"*\", category: targets }\n", false},
		{"a match of **", "security:\n  patterns:\n    - { name: all, match: \"**\", category: targets }\n", false},
		{"an absolute match", "security:\n  patterns:\n    - { name: etc, match: \"/etc/**\", category: targets }\n", false},
		{"a match with a .. component", "security:\n  patterns:\n    - { name: out, match: \"../secrets/**\", category: targets }\n", false},
		{"a match over 200 characters", "security:\n  patterns:\n    - { name: deep, match: \"" + strings.Repeat("d/", 100) + "**\", category: targets }\n", false},
		// The floor's own name (D1506): dropped by the union whichever glob
		// it carries, so both surfaces refuse it at the row's index.
		{"a name the engine's floor already carries", "security:\n  patterns:\n    - { name: " + floorName + ", match: \"deploy/**\", category: targets }\n", false},
		{"an unknown key of the block", "security:\n  severity: high\n", false},
		// always_level_3 is the project layer's member (D1517) and not this
		// file's: a repository that could write it would be saying "every
		// change of mine is security-sensitive", which is the whole-repository
		// answer the project owns.
		{"always_level_3", "security:\n  always_level_3: true\n", false},
		{"an unknown key of a row", "security:\n  patterns:\n    - { name: deploy, match: \"deploy/**\", category: targets, mode: pro }\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := head + tc.body
			_, ps := Parse([]byte(text))
			if got := len(ps) == 0; got != tc.ok {
				t.Fatalf("the decoder accepted = %v, want %v (problems: %v)", got, tc.ok, ps)
			}
			if got := abSchemaAccepts(t, sch, text); got != tc.ok {
				t.Errorf("fugaro.schema.json accepted = %v, want %v: the schema and the decoder disagree", got, tc.ok)
			}
		})
	}
}

// TestSecurityFloorNamesAgreeWithGo holds $defs.securityFloorName's enum, in
// fugaro.schema.json and in project-layer.schema.json, to sensitivity.Floor,
// the one table that decides which names a layer may not spend again (D1505,
// D1506). The enum is a copy of a Go list, the way agent.auth's is of
// config.AgentAuths: a copy that lags refuses at authoring time, in an editor
// and in a repository's CI, a row the binary reads, and a copy that runs ahead
// refuses a row the binary would have to refuse itself.
// TestSecurityKeySchemaAgreesWithTheDecoder says both surfaces agree about one
// floor name; this says they agree about all of them.
//
// Mutation (run, restored): drop a name from the enum, or add one, and this
// fails naming the side that has it.
func TestSecurityFloorNamesAgreeWithGo(t *testing.T) {
	want := make([]string, 0, len(sensitivity.Floor()))
	for _, row := range sensitivity.Floor() {
		want = append(want, row.Name)
	}
	slices.Sort(want)
	// Both schemas carry the copy: the repository's block and the project
	// layer's are judged by the same table, so a name either one may not
	// spend is a name neither may.
	for _, name := range []string{"fugaro.schema.json", "project-layer.schema.json"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Defs map[string]struct {
					Enum []string `json:"enum"`
				} `json:"$defs"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			def, ok := doc.Defs["securityFloorName"]
			if !ok {
				t.Fatalf("%s has no $defs.securityFloorName", name)
			}
			got := slices.Clone(def.Enum)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("$defs.securityFloorName.enum = %v, sensitivity.Floor's names = %v", got, want)
			}
		})
	}
}
