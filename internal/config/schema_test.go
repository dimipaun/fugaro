package config

import "testing"

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
