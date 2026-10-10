package cli

import (
	"slices"
	"strings"
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
			name: "default_profile cleared, the old default HasExecutable: gated, says no profile, not \"profile 's commands\"",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    commands:\n      test: sh test.sh\n",
			want: []string{`default_profile: "svc" -> "" (repositories without workflows: now run no profile's commands)`},
		},
		{
			name: "default_profile a -> b, neither side HasExecutable: not gated",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\ndefault_profile: other\n",
			want: nil,
		},
		{
			// Both profiles are unchanged between prev and next (so no
			// per-key diff from executableChanges' own loop): the only
			// thing that moves is default_profile itself, from a profile
			// with no executable keys at all to one whose only setting is
			// image.skip_build_scripts, gated solely through
			// Profile.HasExecutable.
			name: "default_profile a -> b, the new default's only setting is skip_build_scripts: gated",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\n    image:\n      skip_build_scripts: true\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n  other:\n    base: web-node\n    image:\n      skip_build_scripts: true\ndefault_profile: other\n",
			want: []string{`default_profile: "svc" -> "other" (repositories without workflows: now run profile other's commands)`},
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
			name: "skip_build_scripts true -> false turns build scripts back on: a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      skip_build_scripts: true\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      skip_build_scripts: false\ndefault_profile: svc\n",
			want: []string{`profile svc: image.skip_build_scripts: true -> false`},
		},
		{
			name: "skip_build_scripts true removed (reads as false): a change",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      skip_build_scripts: true\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\ndefault_profile: svc\n",
			want: []string{`profile svc: image.skip_build_scripts: true -> false`},
		},
		{
			name: "skip_build_scripts false -> true is also reported, even though it is the safer direction",
			prev: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      skip_build_scripts: false\ndefault_profile: svc\n",
			next: layerPreamble + "profiles:\n  svc:\n    base: web-node\n    image:\n      skip_build_scripts: true\ndefault_profile: svc\n",
			want: []string{`profile svc: image.skip_build_scripts: false -> true`},
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
			got, err := executableChanges(prev, next)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
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
// of them hits executableKeyValues' default: error case, so a key added
// to config.ExecutableKeys later without a comparison here is caught here
// (loudly, in CI) rather than only discovered on a live publish.
func TestExecutableKeyValuesCoversEveryExecutableKey(t *testing.T) {
	p := parseLayerOrFatal(t, layerPreamble+
		"profiles:\n  svc:\n    base: web-node\n    commands:\n      build: sh build.sh\n      test: sh test.sh\n      rerun_failed: { command: pytest, each: \"-k {id}\" }\n    image:\n      apt: [git]\n      setup: [\"echo hi\"]\ndefault_profile: svc\n",
	).Profiles["svc"]
	for _, key := range config.ExecutableKeys {
		if err := executableKeyValues(map[string]string{}, p, key); err != nil {
			t.Errorf("key %s: %v", key, err)
		}
	}
}

// TestExecutableKeyValuesRefusesAnUnknownKey confirms the fail-closed
// default itself still refuses a key it was never taught (the point of
// the coverage test above: config.ExecutableKeys growing a new entry
// must not silently skip it). It must return an error, not panic: this
// runs on the live publish path, and a panic there would crash the
// command instead of cleanly refusing to publish.
func TestExecutableKeyValuesRefusesAnUnknownKey(t *testing.T) {
	if err := executableKeyValues(map[string]string{}, config.Profile{}, "workflows.*.a_future_key"); err == nil {
		t.Fatal("an unknown key was not refused")
	}
}

// TestPublishRefusesWhenExecutableKeysGrowsAnUnknownEntry is the live-path
// backstop itself: with config.ExecutableKeys temporarily given an entry
// executableKeyValues has no case for (a test hook: the var is mutated
// and restored, simulating a future key landing in config.ExecutableKeys
// without a matching case here), config publish must refuse cleanly
// (fail closed, no crash) and nothing is written.
func TestPublishRefusesWhenExecutableKeysGrowsAnUnknownEntry(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	orig := config.ExecutableKeys
	config.ExecutableKeys = append(slices.Clone(orig), "workflows.*.a_future_key")
	t.Cleanup(func() { config.ExecutableKeys = orig })
	_, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cannot compare executable key workflows.*.a_future_key") {
		t.Fatalf("err = %v", err)
	}
	if bucketText(t, f, config.LayerKey) != "" {
		t.Fatal("published despite an unknown executable key")
	}
}
