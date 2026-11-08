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
		// An empty value means none on both surfaces. The decoder cannot tell
		// an absent key from an empty one, so a dockerfile beside needs: [] or
		// image_name: "" is a workflow with no catalog reference at all: the
		// schema must not read the empty value as the reference the decoder
		// does not see.
		{"a dockerfile and an empty needs", "    dockerfile: .fugaro/web.Dockerfile\n    needs: []\n", true},
		{"a dockerfile and an empty name", "    dockerfile: .fugaro/web.Dockerfile\n    image_name: \"\"\n", true},
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

// TestStopFractionSchemaAgreesOnZero pins the stop fraction's one disputed
// value (the owner's ruling of 2026-11-08, item 5): agent.review_stop_fraction
// is 0 as well as the (0, 1] range, because a file writing 0 asks for the
// default the release before this one already has — the runner reads it as
// absent (config.DefaultReviewStopFraction) and the key's own gate refuses the
// key whatever it says, for the same reason. A schema that refused 0 while the
// decoder admitted it would be the red check nobody can reproduce the doctrine
// above names, on the one value a repository writing the key out in full is
// most likely to write.
func TestStopFractionSchemaAgreesOnZero(t *testing.T) {
	sch := compileABSchema(t, "fugaro.schema.json")
	const head = minimalRepo + "git: { provider: github }\nagent:\n  auth: api-key\n"
	const tail = "workflows:\n  app:\n    commands: { build: make, test: make test }\n"
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"nothing set", "", true},
		{"zero, the default", "  review_stop_fraction: 0\n", true},
		{"a part of the budget", "  review_stop_fraction: 0.5\n", true},
		{"the whole budget", "  review_stop_fraction: 1\n", true},
		{"more than the whole budget", "  review_stop_fraction: 1.5\n", false},
		{"a negative share", "  review_stop_fraction: -0.1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := head + tc.body + tail
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
