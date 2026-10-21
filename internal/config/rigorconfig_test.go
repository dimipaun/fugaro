package config

import (
	"reflect"
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
