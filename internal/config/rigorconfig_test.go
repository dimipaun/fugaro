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
        mode: hotfix
`

// TestRigorConfigKeepsTheTwoLayersApart: the resolver's configuration is the
// project layer's defaults.rigor and the repository's own rigor: block, each
// half where it belongs, because both area lists are in effect at once (D20c)
// and a single merged list would silently drop one.
func TestRigorConfigKeepsTheTwoLayersApart(t *testing.T) {
	layer := mustLayer(t, rigorLayer)
	repo := &Rigor{Default: rigor.POC, Areas: []rigor.Area{{Name: "docs", Match: "docs/**", Mode: rigor.Startup}}}

	got := (&Config{Layer: layer, RepoRigor: repo}).RigorConfig()
	if got.Project != rigor.Critical || got.Repo != rigor.POC {
		t.Errorf("the two defaults: project %q, repo %q", got.Project, got.Repo)
	}
	if len(got.ProjectAreas) != 1 || got.ProjectAreas[0].Name != "payments" || got.ProjectAreas[0].Mode != rigor.Hotfix {
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
// resolved values did not move (docs/design/layered-config.md §4). A pointer
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
	repo := &Rigor{Default: rigor.POC, Areas: []rigor.Area{{Name: "docs", Match: "docs/**", Mode: rigor.POC}}}
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
