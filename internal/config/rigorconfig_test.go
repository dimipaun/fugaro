package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

const rigorLayer = `version: 1
project: acme
gcp_project: acme-fugaro
defaults:
  rigor:
    default: critical
    areas:
      - name: payments
        match: "payments/**"
        level: hotfix
`

// TestRigorConfigKeepsTheTwoLayersApart: the resolver's configuration is the
// project layer's defaults.rigor and the repository's own rigor: block, each
// half where it belongs, because both area lists are in effect at once (D20c)
// and a single merged list would silently drop one.
func TestRigorConfigKeepsTheTwoLayersApart(t *testing.T) {
	layer := mustLayer(t, rigorLayer)
	repo := &Rigor{Default: rigor.POC, Areas: []rigor.Area{{Name: "docs", Match: "docs/**", Level: rigor.Startup}}}

	got := (&Config{Layer: layer, RepoRigor: repo}).RigorConfig()
	if got.Project != rigor.Critical || got.Repo != rigor.POC {
		t.Errorf("the two defaults: project %q, repo %q", got.Project, got.Repo)
	}
	if len(got.ProjectAreas) != 1 || got.ProjectAreas[0].Name != "payments" || got.ProjectAreas[0].Level != rigor.Hotfix {
		t.Errorf("the project layer's areas: %+v", got.ProjectAreas)
	}
	if len(got.RepoAreas) != 1 || got.RepoAreas[0].Name != "docs" || got.RepoAreas[0].Match != "docs/**" {
		t.Errorf("the repository's areas: %+v", got.RepoAreas)
	}

	// The repository wrote nothing: the layer's half alone, unchanged.
	if only := (&Config{Layer: layer}).RigorConfig(); !reflect.DeepEqual(only, (&Config{Layer: layer}).ProjectRigor()) {
		t.Errorf("a repository with no block: %+v", only)
	}
	// No layer: the repository's half alone, and the project layer's is the
	// zero value rather than the repository's value.
	noLayer := (&Config{RepoRigor: repo}).RigorConfig()
	if noLayer.Project != "" || noLayer.ProjectAreas != nil || noLayer.Repo != rigor.POC {
		t.Errorf("no layer: %+v", noLayer)
	}
	if zero := (&Config{}).ProjectRigor(); !reflect.DeepEqual(zero, rigor.Config{}) {
		t.Errorf("neither layer: %+v", zero)
	}
}

// TestRigorIsOutOfTheHashWhenTheFileWritesNone: SHA256 is the hash of this
// struct's JSON (resolve.go) and is the quick check that a repository's
// resolved values did not move (docs/layered-config/spec.md §4). A pointer
// with a yaml tag and no json name marshals as "rigor":null in every file
// that writes no rigor: block — which is nearly every file — and so moves the
// hash of a file nobody changed. Layer and RepoRigor, the two fields beside
// it, carry json:"-" for the same reason; this one needs the name, because a
// block a file does write is part of the values the hash covers.
func TestRigorIsOutOfTheHashWhenTheFileWritesNone(t *testing.T) {
	layer := mustLayer(t, testLayer)
	c, _, ps := Resolve([]byte(minimalRepo), layer)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if c.Rigor != nil {
		t.Fatalf("a file that writes no rigor: block resolved one: %+v", c.Rigor)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"rigor"`) {
		t.Errorf("a config whose file writes no rigor: block carries the key anyway, so its hash moves for a file that did not change: %s", b)
	}

	with, _, ps := Resolve([]byte(minimalRepo+"rigor: { default: poc }\n"), layer)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if with.Rigor == nil || with.Rigor.Default != rigor.POC {
		t.Fatalf("the block a file writes did not resolve: %+v", with.Rigor)
	}
	if with.SHA256() == c.SHA256() {
		t.Error("a file that writes a rigor: block hashes as one that writes none")
	}
}

// TestProjectRigorIsTheLayersHalfAlone: ProjectRigor never carries the
// repository's block. A run whose base branch cannot be read falls back to it
// (internal/runner, D25), and that fallback must not be the checked-out ref's
// own rigor — the branch under review's value is the one answer it must never
// give, so a method that quietly included RepoRigor would hand the branch the
// mode it asked for.
func TestProjectRigorIsTheLayersHalfAlone(t *testing.T) {
	repo := &Rigor{Default: rigor.POC, Areas: []rigor.Area{{Name: "docs", Match: "docs/**", Level: rigor.POC}}}
	got := (&Config{Layer: mustLayer(t, rigorLayer), RepoRigor: repo}).ProjectRigor()
	if got.Repo != "" || len(got.RepoAreas) != 0 {
		t.Fatalf("the project layer's half carries the repository's: %+v", got)
	}
	if got.Project != rigor.Critical || len(got.ProjectAreas) != 1 {
		t.Errorf("the project layer's half lost its own: %+v", got)
	}
}

// TestRigorConfigSaysWhetherTheRepositoryOptedIn: backlog filing is opt-in
// (the owner's ruling of 2026-10-24), and the opt-in is a rigor: block in the
// repository's own file — even one that sets nothing — or a rigor default in
// the project layer. A file with neither is unconfigured, and resolves the
// built-in default without filing.
func TestRigorConfigSaysWhetherTheRepositoryOptedIn(t *testing.T) {
	layer := mustLayer(t, testLayer)
	for _, tc := range []struct {
		name string
		file string
		want bool
	}{
		{"no block", minimalRepo, false},
		{"an empty block", minimalRepo + "rigor: {}\n", true},
		{"a default", minimalRepo + "rigor: { default: pro }\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, ps := Resolve([]byte(tc.file), layer)
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			if got := c.RigorConfig().Configured(); got != tc.want {
				t.Errorf("Configured() = %v; want %v", got, tc.want)
			}
		})
	}
	if !(&Config{Layer: mustLayer(t, rigorLayer)}).RigorConfig().Configured() {
		t.Error("a project layer that sets defaults.rigor did not opt its repositories in")
	}
}

// TestSetsRigorReadsTheKeyNotTheValue: a runner before 0.11.0 decodes the
// project layer with KnownFields, so what it refuses is defaults.rigor being
// written at all, and an empty block is refused exactly like a full one. The
// skew gate in fugaro update --repo asks this, so the answer has to come from
// the text: LayerRigor is a value, and its zero value cannot tell `rigor: {}`
// from a layer that never wrote the key.
func TestSetsRigorReadsTheKeyNotTheValue(t *testing.T) {
	const head = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	for _, c := range []struct {
		text string
		want bool
	}{
		{rigorLayer, true},
		{head + "defaults:\n  rigor: {}\n", true},
		{head + "defaults:\n  git: { provider: github }\n", false},
		{head, false},
	} {
		if got := mustLayer(t, c.text).SetsRigor(); got != c.want {
			t.Errorf("SetsRigor() = %v, want %v for\n%s", got, c.want, c.text)
		}
	}
	if (*ProjectLayer)(nil).SetsRigor() {
		t.Error("a nil layer sets nothing")
	}
}

// TestRepoRigorSetReadsTheKeyNotTheValue: the repository's own file gets the
// same answer SetsRigor gives a project layer, and for the same reason — a
// runner before 0.11.0 decodes fugaro.yaml with KnownFields, so what it
// refuses is the key being written at all, an explicit null included. RepoRigor
// is a value and cannot answer (`rigor:` decodes to a nil pointer), and it is
// what RigorConfig reads for the mode and the area rules (D20c), where a block
// that sets nothing and a key that is written are two different things: the
// skew gates read this field, filing's opt-in reads that one.
func TestRepoRigorSetReadsTheKeyNotTheValue(t *testing.T) {
	layer := mustLayer(t, testLayer)
	for _, c := range []struct {
		text string
		want bool
	}{
		{minimalRepo + "rigor: { default: hotfix }\n", true},
		{minimalRepo + "rigor: {}\n", true},
		{minimalRepo + "rigor:\n", true},
		{minimalRepo + "git: { pr: { labels: [mine] } }\n", false},
		{minimalRepo, false},
	} {
		cfg, _, ps := Resolve([]byte(c.text), layer)
		if len(ps) > 0 {
			t.Fatalf("%s: %v", c.text, ps)
		}
		if cfg.RepoRigorSet != c.want {
			t.Errorf("RepoRigorSet = %v, want %v for\n%s", cfg.RepoRigorSet, c.want, c.text)
		}
	}
	// The answer is the file's own key, never the merged one: a file that
	// writes the key over a layer that sets nothing still counts.
	null, _, ps := Resolve([]byte(minimalRepo+"rigor:\n"), layer)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if !null.RepoRigorSet {
		t.Error("a file that writes rigor: over a project layer lost its own key")
	}
	if null.RepoRigor != nil {
		t.Errorf("a null block resolved a value: %+v", null.RepoRigor)
	}
}

// TestRigorScalarShorthand: the repository's own key may name the level alone,
// `rigor: critical`, which says what a block carrying only default says (item
// 8's shape, as the owner ruled it: the block stays for the repositories that
// also name areas). The two forms are read by the same decoder, so the value
// vocabulary is the same set either way.
//
// Mutation (drop UnmarshalYAML from Rigor): the scalar form is a type error,
// "cannot unmarshal !!str into config.Rigor", and the cases below fail.
func TestRigorScalarShorthand(t *testing.T) {
	cfg, ps := Parse([]byte(minimalRepo + "git: {provider: github}\nrigor: critical\nworkflows: {app: {commands: {build: sh b, test: sh t}}}\n"))
	if len(ps) > 0 {
		t.Fatalf("the scalar shorthand was refused: %s", problemsText(ps))
	}
	if cfg.Rigor == nil || cfg.Rigor.Default != rigor.Critical || len(cfg.Rigor.Areas) != 0 {
		t.Fatalf("rigor: critical resolved %+v, want the default alone", cfg.Rigor)
	}
	// The block form still carries both keys, and the scalar is the level:
	// a value outside the vocabulary is refused where it is written.
	cfg, ps = Parse([]byte(minimalRepo + "git: {provider: github}\nrigor: {default: poc, areas: [{name: docs, match: \"docs/**\", level: pro}]}\nworkflows: {app: {commands: {build: sh b, test: sh t}}}\n"))
	if len(ps) > 0 {
		t.Fatalf("the block form was refused: %s", problemsText(ps))
	}
	if cfg.Rigor.Default != rigor.POC || len(cfg.Rigor.Areas) != 1 || cfg.Rigor.Areas[0].Level != rigor.Pro {
		t.Fatalf("the block form resolved %+v", cfg.Rigor)
	}
	_, ps = Parse([]byte(minimalRepo + "git: {provider: github}\nrigor: Paranoid\nworkflows: {app: {commands: {build: sh b, test: sh t}}}\n"))
	if len(ps) == 0 || !strings.Contains(problemsText(ps), "rigor must be one of "+rigor.RigorList()) {
		t.Fatalf("a level outside the vocabulary was not refused by name: %s", problemsText(ps))
	}
}

// TestRigorReadsTheOldModeKey: the key an area rule's level is written under
// was `mode:` before the rename, and that key is released — v0.11.0 shipped
// rigor.areas with it, and a released `fugaro rigor set --area` wrote it into
// the project layer `fugaro update` publishes — so both loaders read it as the
// rule's level rather than refusing it. Neither format is versioned past 1 for
// it, and a repository's own fugaro.yaml is never rewritten by `fugaro update`,
// so a refusal would leave an installed project with a manual repair and no
// command behind it.
//
// `level:` decides where a file carries both, RigorRecord.LevelOf's rule for
// the same value, and a `level:` outside the vocabulary is still refused: the
// old spelling is a fallback for a document nobody wrote the new key in, never
// a way to spell a level the reader cannot use.
//
// Mutation (refuse `mode:` by name again): the two loads below report it, and
// an installation whose file still writes the old key fails its next run.
func TestRigorReadsTheOldModeKey(t *testing.T) {
	cfg, ps := Parse([]byte(minimalRepo + "git: {provider: github}\nrigor:\n  areas:\n    - {name: docs, match: \"docs/**\", mode: pro}\nworkflows: {app: {commands: {build: sh b, test: sh t}}}\n"))
	if len(ps) > 0 {
		t.Fatalf("the repository layer's old key: %s", problemsText(ps))
	}
	if cfg.Rigor == nil || len(cfg.Rigor.Areas) != 1 || cfg.Rigor.Areas[0].Level != rigor.Pro {
		t.Fatalf("the repository layer read %+v, want one rule at pro", cfg.Rigor)
	}
	// The new key wins where a file still carries both, so a rule a person has
	// already upgraded cannot be dragged back by the line beside it.
	cfg, ps = Parse([]byte(minimalRepo + "git: {provider: github}\nrigor:\n  areas:\n    - {name: docs, match: \"docs/**\", mode: poc, level: pro}\nworkflows: {app: {commands: {build: sh b, test: sh t}}}\n"))
	if len(ps) > 0 {
		t.Fatalf("a rule carrying both keys: %s", problemsText(ps))
	}
	if len(cfg.Rigor.Areas) != 1 || cfg.Rigor.Areas[0].Level != rigor.Pro {
		t.Fatalf("a rule carrying both keys read %+v, want level's value", cfg.Rigor.Areas)
	}
	_, ps = Parse([]byte(minimalRepo + "git: {provider: github}\nrigor:\n  areas:\n    - {name: docs, match: \"docs/**\", mode: pro, level: Paranoid}\nworkflows: {app: {commands: {build: sh b, test: sh t}}}\n"))
	if got := problemsText(ps); !strings.Contains(got, "level must be one of "+rigor.RigorList()) {
		t.Fatalf("an unknown level beside the old key: %q, want the vocabulary refusal", got)
	}
	const text = "version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n  rigor:\n    areas:\n      - {name: docs, match: \"docs/**\", mode: pro}\n"
	l, ps := ParseProjectLayer([]byte(text), testAnchor)
	if len(ps) > 0 {
		t.Fatalf("the project layer's old key: %s", problemsText(ps))
	}
	if len(l.Defaults.Rigor.Areas) != 1 || l.Defaults.Rigor.Areas[0].Level != rigor.Pro {
		t.Fatalf("the project layer read %+v, want one rule at pro", l.Defaults.Rigor.Areas)
	}
}

// TestRigorSpellingsAreReadFromTheText: the two spellings this release adds to
// the rigor configuration — an area rule's `level:` key and the scalar
// `rigor: <level>` shorthand — are what the image-skew gate judges, and
// neither the parsed block nor ProjectLayer.SetsRigor can answer it:
// Area.UnmarshalYAML folds `level:` and the released `mode:` into the rule's
// level, and Rigor.UnmarshalYAML reads the shorthand as the same block a
// `default:` line writes. Both answers are taken from the document's own text,
// so a file that writes neither spelling — including one that still writes
// `mode:` — reports neither, and a file that writes one reports exactly that
// one.
//
// Mutation (read the parsed rules instead of the tree, e.g. answer
// setsRigorLevel with len(r.Areas) > 0): the `mode:` cases below report the
// new key and the gate refuses a file every image reads.
func TestRigorSpellingsAreReadFromTheText(t *testing.T) {
	layer := mustLayer(t, testLayer)
	for _, c := range []struct {
		name             string
		text             string
		level, shorthand bool
	}{
		{"this release's area key", minimalRepo + "rigor:\n  areas:\n    - {name: docs, match: \"docs/**\", level: pro}\n", true, false},
		{"the scalar shorthand", minimalRepo + "rigor: critical\n", false, true},
		{"the released area key", minimalRepo + "rigor:\n  areas:\n    - {name: docs, match: \"docs/**\", mode: pro}\n", false, false},
		{"a block that names neither", minimalRepo + "rigor: { default: critical }\n", false, false},
		{"no rigor key at all", minimalRepo + "agent: { auth: api-key }\n", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, _, ps := Resolve([]byte(c.text), layer)
			if len(ps) > 0 {
				t.Fatalf("%s: %s", c.text, problemsText(ps))
			}
			if cfg.RepoRigorLevelSet != c.level || cfg.RepoRigorScalarSet != c.shorthand {
				t.Errorf("RepoRigorLevelSet = %v, RepoRigorScalarSet = %v, want %v and %v for\n%s",
					cfg.RepoRigorLevelSet, cfg.RepoRigorScalarSet, c.level, c.shorthand, c.text)
			}
		})
	}

	// The layer's half: the shorthand is not one it can carry (LayerRigor is a
	// plain struct, so `defaults.rigor: critical` is a value no release reads),
	// and the area key is the spelling a layer writes from this release on.
	for _, c := range []struct {
		name string
		text string
		want bool
	}{
		{"this release's area key", rigorLayer, true},
		{"the released area key", "version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n  rigor:\n    areas:\n      - {name: docs, match: \"docs/**\", mode: pro}\n", false},
		{"a default alone", "version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n  rigor: { default: critical }\n", false},
	} {
		t.Run("layer: "+c.name, func(t *testing.T) {
			l, ps := ParseProjectLayer([]byte(c.text), testAnchor)
			if len(ps) > 0 {
				t.Fatalf("%s: %s", c.text, problemsText(ps))
			}
			if got := l.SetsRigorLevel(); got != c.want {
				t.Errorf("SetsRigorLevel = %v, want %v for\n%s", got, c.want, c.text)
			}
		})
	}
}
