package config

import (
	"strings"
	"testing"
)

// removedImageTestCases is the removal's own list, as a person writes the keys
// under one workflow. It is written out here rather than read from
// removedImageKeys so that the two have to agree by test and not by
// construction: a key added to one of them and not the other is a key a
// repository may still write.
var removedImageTestCases = []struct{ key, body string }{
	{"image.apt", "    image: { apt: [jq] }\n"},
	{"image.setup", "    image: { setup: [\"make deps\"] }\n"},
	{"image.skip_build_scripts", "    image: { skip_build_scripts: true }\n"},
	{"image.tools", "    image: { tools: { node: \"24.19.0\" } }\n"},
	{"image.key_files", "    image: { key_files: [go.sum] }\n"},
	{"dockerfile", "    dockerfile: .fugaro/app.Dockerfile\n"},
	{"rebuild.check", "    rebuild: { check: daily }\n"},
	{"rebuild.max_age", "    rebuild: { max_age: 7d }\n"},
	{"rebuild.lockfiles", "    rebuild: { lockfiles: true }\n"},
	{"rebuild.base", "    rebuild: { base: true }\n"},
	{"rebuild.paths", "    rebuild: { paths: [go.mod] }\n"},
}

// removedImageYAML is baseYAML with body added to its one workflow.
func removedImageYAML(body string) string {
	const at = "    commands: { build: make, test: make test }\n"
	return strings.Replace(baseYAML, at, body+at, 1)
}

// removedImageLayer is a project layer whose one profile writes body.
func removedImageLayer(body string) string {
	return `version: 1
project: acme
gcp_project: acme-fugaro
profiles:
  app:
` + body + `    commands: { build: make, test: make test }
`
}

// TestRemovedImageKeysAreRefusedByName: every key the repository-image removal
// took out of a workflow is refused by name, with a sentence that names the
// catalog command that replaces it (D1195). The tombstone is what a person
// reads when their fugaro.yaml stops working, so the message and not the
// decoder's "field image not found" is the whole point of keeping the key
// decodable.
func TestRemovedImageKeysAreRefusedByName(t *testing.T) {
	for _, tc := range removedImageTestCases {
		t.Run(tc.key, func(t *testing.T) {
			_, problems := Parse([]byte(removedImageYAML(tc.body)))
			want := "workflows.app." + tc.key
			got := problemsFor(problems, want)
			if got == "" {
				t.Fatalf("fugaro.yaml with %s: no problem at %s (problems: %v)", tc.key, want, problems)
			}
			if !strings.Contains(got, "fugaro image add") {
				t.Errorf("the refusal of %s does not name `fugaro image add`: %q", tc.key, got)
			}
		})
	}
	// The project layer is the second door to the same build, so a profile
	// that writes one of the keys is refused there too.
	for _, tc := range removedImageTestCases {
		t.Run("profiles."+tc.key, func(t *testing.T) {
			_, problems := ParseProjectLayer([]byte(removedImageLayer(tc.body)), testAnchor)
			want := "profiles.app." + tc.key
			got := problemsFor(problems, want)
			if got == "" {
				t.Fatalf("project layer with %s: no problem at %s (problems: %v)", tc.key, want, problems)
			}
			if !strings.Contains(got, "fugaro image add") {
				t.Errorf("the refusal of %s does not name `fugaro image add`: %q", tc.key, got)
			}
		})
	}
}

// TestRemovedImageKeysIsTheRefusedTable: RemovedImageKeys is the list the
// settings registry's completeness guard reads, so it has to be the same
// eleven keys the decoder refuses and nothing else. A key in the list but not
// refused would exempt a live key from that guard; a refused key missing from
// it would fail the guard with no way to satisfy it.
func TestRemovedImageKeysIsTheRefusedTable(t *testing.T) {
	want := map[string]bool{}
	for _, tc := range removedImageTestCases {
		want["workflows.*."+tc.key] = true
	}
	got := RemovedImageKeys()
	if len(got) != len(want) {
		t.Fatalf("RemovedImageKeys() = %v, want the %d refused keys %v", got, len(want), want)
	}
	for _, key := range got {
		if !want[key] {
			t.Errorf("RemovedImageKeys() lists %s, which the removal does not refuse", key)
		}
	}
}

// problemsFor is the first message reported at path, or "".
func problemsFor(problems []Problem, path string) string {
	for _, p := range problems {
		if p.Path == path {
			return p.Message
		}
	}
	return ""
}

// TestRemovedImageKeysLeaveTheScopeTable: a scope row is what makes a key
// settable, so a removed key with a row is a key the settings family still
// offers. The two 0.7.0 tombstones are the control: they are not settings
// either, and they keep their rows, which is why the removal's list is the
// eleven and not the thirteen.
func TestRemovedImageKeysLeaveTheScopeTable(t *testing.T) {
	for _, tc := range removedImageTestCases {
		if _, ok := ScopeOf("workflows.web." + tc.key); ok {
			t.Errorf("workflows.*.%s still has a scope row: the table is what decides whether a settings command may write a key the config refuses", tc.key)
		}
	}
	for _, key := range []string{"workflows.*.image.node", "workflows.*.image.jdk"} {
		if _, ok := ScopeOf(strings.Replace(key, "*", "web", 1)); !ok {
			t.Errorf("%s lost its scope row: the two 0.7.0 tombstones are not settings and stay in the table", key)
		}
	}
}
