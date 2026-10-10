package cli

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// layerPreamble is every test layer's first three lines; every case below
// appends a profiles: block and (usually) default_profile:.
const layerPreamble = "version: 1\nproject: aurora\ngcp_project: proj-1234\n"

// parseLayerOrFatal is config.ParseProjectLayer with the anchor checks off
// (irrelevant to executableChanges), failing the test on a problem.
func parseLayerOrFatal(t *testing.T, text string) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte(text), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatalf("layer is invalid: %v\n%s", ps, text)
	}
	return l
}

// TestExecutableChangesTable pins executableChanges (config_publish.go)
// over layers parsed by config.ParseProjectLayer, the reviewed fix for a
// bug where commands.rerun_failed's Command and Each were joined with a
// bare space, so two different (command, each) pairs could read as the
// same text and publish without --executable-changes, no banner.
func TestExecutableChangesTable(t *testing.T) {
	cases := []struct {
		name       string
		prev, next string // prev == "": no previous layer (first publish)
		want       []string
	}{
		{
			name: "add: commands.test newly set",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc\n",
			want: []string{`profile svc: commands.test: "" -> "sh test.sh"`},
		},
		{
			name: "change: commands.build value changes",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      build: sh old.sh\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      build: sh new.sh\ndefault_profile: svc\n",
			want: []string{`profile svc: commands.build: "sh old.sh" -> "sh new.sh"`},
		},
		{
			name: "remove: commands.test cleared",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			want: []string{`profile svc: commands.test: "sh test.sh" -> ""`},
		},
		{
			name: "whitespace-only change is still a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: \"sh  test.sh\"\ndefault_profile: svc\n",
			want: []string{`profile svc: commands.test: "sh test.sh" -> "sh  test.sh"`},
		},
		{
			name: "a block scalar's trailing newline decodes equal to the plain form: no change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: |-\n        sh test.sh\ndefault_profile: svc\n",
			want: nil,
		},
		{
			name: "a different YAML quoting form that decodes equal: no change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: 'sh test.sh'\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: \"sh test.sh\"\ndefault_profile: svc\n",
			want: nil,
		},
		{
			// The bug this whole table guards: rf.Command + " " + rf.Each
			// read both of these as "pytest -k {id}".
			name: `rerun_failed boundary shift: "pytest -k"/"{id}" vs "pytest"/"-k {id}"`,
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      rerun_failed: { command: \"pytest -k\", each: \"{id}\" }\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      rerun_failed: { command: \"pytest\", each: \"-k {id}\" }\ndefault_profile: svc\n",
			want: []string{
				`profile svc: commands.rerun_failed.command: "pytest -k" -> "pytest"`,
				`profile svc: commands.rerun_failed.each: "{id}" -> "-k {id}"`,
			},
		},
		{
			name: `rerun_failed boundary shift: "a "/"{id}" vs "a"/" {id}"`,
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      rerun_failed: { command: \"a \", each: \"{id}\" }\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      rerun_failed: { command: \"a\", each: \" {id}\" }\ndefault_profile: svc\n",
			want: []string{
				`profile svc: commands.rerun_failed.command: "a " -> "a"`,
				`profile svc: commands.rerun_failed.each: "{id}" -> " {id}"`,
			},
		},
		{
			name: "apt reorder is a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      apt: [git, curl]\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      apt: [curl, git]\ndefault_profile: svc\n",
			want: []string{`profile svc: image.apt: ["git" "curl"] -> ["curl" "git"]`},
		},
		{
			name: "a setup step split in two is a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      setup: [\"echo hi\"]\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      setup: [echo, hi]\ndefault_profile: svc\n",
			want: []string{`profile svc: image.setup: ["echo hi"] -> ["echo" "hi"]`},
		},
		{
			name: "a profile renamed: the old name loses it, the new name gains it",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc2:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc2\n",
			want: []string{
				`profile svc: commands.test: "sh test.sh" -> ""`,
				`profile svc2: commands.test: "" -> "sh test.sh"`,
				`default_profile: "svc" -> "svc2" (repositories without workflows: now run profile svc2's commands)`,
			},
		},
		{
			name: "a profile deleted with executable keys set is a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\n    commands:\n      test: sh other.sh\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			want: []string{`profile other: commands.test: "sh other.sh" -> ""`},
		},
		{
			name: "a profile deleted with no executable keys is not a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			want: nil,
		},
		{
			name: "a profile added with executable keys set is a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\n    commands:\n      test: sh other.sh\ndefault_profile: svc\n",
			want: []string{`profile other: commands.test: "" -> "sh other.sh"`},
		},
		{
			name: "default_profile first set, the new default HasExecutable: gated",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc\n",
			want: []string{`default_profile: "" -> "svc" (repositories without workflows: now run profile svc's commands)`},
		},
		{
			name: "default_profile a -> b, the old default HasExecutable: gated",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\n  other:\n    base: web-node\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\n  other:\n    base: web-node\ndefault_profile: other\n",
			want: []string{`default_profile: "svc" -> "other" (repositories without workflows: now run profile other's commands)`},
		},
		{
			name: "default_profile a -> b, neither side HasExecutable: not gated",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\ndefault_profile: other\n",
			want: nil,
		},
		{
			name: "first publish (nil previous): every executable key of the one profile, plus default_profile",
			prev: "",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      build: sh build.sh\n      test: sh test.sh\n    image:\n      apt: [git]\n      setup: [\"echo hi\"]\ndefault_profile: svc\n",
			want: []string{
				`profile svc: commands.build: "" -> "sh build.sh"`,
				`profile svc: commands.test: "" -> "sh test.sh"`,
				`profile svc: image.apt: [] -> ["git"]`,
				`profile svc: image.setup: [] -> ["echo hi"]`,
				`default_profile: "" -> "svc" (repositories without workflows: now run profile svc's commands)`,
			},
		},
		{
			name: "null vs absent apt: equal, no change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      apt: null\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			want: nil,
		},
		{
			name: "empty list vs absent apt: equal, no change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      apt: []\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var prev *config.ProjectLayer
			if c.prev != "" {
				prev = parseLayerOrFatal(t, c.prev)
			}
			next := parseLayerOrFatal(t, c.next)
			got := executableChanges(prev, next)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !equalStrings(got, c.want) {
				t.Errorf("got  %#v\nwant %#v", got, c.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestExecutableKeyValuesCoversEveryExecutableKey loops over
// config.ExecutableKeys (not a fixed, hand-copied list) and confirms none
// of them hits executableKeyValues' default: panic case, so a key added
// to config.ExecutableKeys later without a comparison here fails closed
// (and loudly, in this test) rather than silently publishing unreviewed.
func TestExecutableKeyValuesCoversEveryExecutableKey(t *testing.T) {
	p := parseLayerOrFatal(t, layerPreamble+
		"profiles:\n  svc:\n    base: web-node\n    commands:\n      build: sh build.sh\n      test: sh test.sh\n      rerun_failed: { command: pytest, each: \"-k {id}\" }\n    image:\n      apt: [git]\n      setup: [\"echo hi\"]\ndefault_profile: svc\n",
	).Profiles["svc"]
	for _, key := range config.ExecutableKeys {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("key %s: %v", key, r)
				}
			}()
			executableKeyValues(map[string]string{}, p, key)
		}()
	}
}

// TestExecutableKeyValuesRefusesAnUnknownKey confirms the fail-closed
// default itself still panics for a key it was never taught (the point
// of the coverage test above: config.ExecutableKeys growing a new entry
// must not silently skip it).
func TestExecutableKeyValuesRefusesAnUnknownKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown key did not panic")
		}
	}()
	executableKeyValues(map[string]string{}, config.Profile{}, "workflows.*.a_future_key")
}
