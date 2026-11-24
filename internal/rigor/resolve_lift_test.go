package rigor

import (
	"slices"
	"testing"
)

// TestMemberAreaOutranksRigorRepos pins D3294's order inside the project
// layer (docs/repo-override-rulings/spec.md §5.4, Q5, R5): a project area that
// names the run's repository in one of its members ranks above the project's
// own defaults.rigor.repos entry for that repository, because the rule answers
// for a named part of this repository where the entry answers for the whole of
// it — and a rule in effect through its Match alone, which answers for every
// repository of the project, keeps LD4's rank below the entry.
//
// Both directions are checked, because the order is a precedence and not an
// only-tightens rule (D21): a member area may raise or lower the entry's level
// for its paths, and R5's migration names every repository and path whose level
// moves either way before it writes.
//
// What D3294 does not change is the rank of the repository's own file: its
// rigor.default and its own path rules still beat every project-level rank
// (LD4, LF4). Release A drops those two keys (S1) and the migration moves what
// a repository wrote into exactly the two project ranks this test orders.
func TestMemberAreaOutranksRigorRepos(t *testing.T) {
	ui := Area{Name: "ui", Members: []Member{{Repo: "acme/app", Paths: []string{"ui/**"}}}, Level: POC}
	whole := Area{Name: "app", Members: []Member{{Repo: "acme/app"}}, Level: Critical}
	payments := Area{Name: "payments", Match: "payments/**", Level: POC}
	other := Area{Name: "web", Members: []Member{{Repo: "acme/web", Paths: []string{"ui/**"}}}, Level: Critical}
	both := Area{
		Name:    "ui",
		Match:   "ui/**",
		Members: []Member{{Repo: "acme/app", Paths: []string{"ui/**"}}},
		Level:   POC,
	}

	for _, c := range []struct {
		name      string
		cfg       Config
		req       Request
		wantLevel Rigor
		wantFrom  Layer
		wantAreas []string
	}{
		{
			"a member area lowers the project's entry for this repository",
			Config{ProjectAreas: []Area{ui}, ProjectRepos: map[string]Rigor{"acme/app": Critical}},
			Request{Repo: "acme/app", Paths: []string{"ui/button.go"}},
			POC, FromArea, []string{"ui"},
		},
		{
			"a member area raises the project's entry for this repository",
			Config{ProjectAreas: []Area{Area{Name: "ui", Members: []Member{{Repo: "acme/app", Paths: []string{"ui/**"}}}, Level: Critical}}, ProjectRepos: map[string]Rigor{"acme/app": POC}},
			Request{Repo: "acme/app", Paths: []string{"ui/button.go"}},
			Critical, FromArea, []string{"ui"},
		},
		{
			"a whole-repo member needs no path at all",
			Config{ProjectAreas: []Area{whole}, ProjectRepos: map[string]Rigor{"acme/app": POC}},
			Request{Repo: "acme/app"},
			Critical, FromArea, []string{"app"},
		},
		{
			"a match-only project area stays below the project's entry",
			Config{ProjectAreas: []Area{payments}, ProjectRepos: map[string]Rigor{"acme/app": Critical}},
			Request{Repo: "acme/app", Paths: []string{"payments/x.go"}},
			Critical, FromProject, []string{"payments"},
		},
		{
			"an area naming another repository is not in effect for this run",
			Config{ProjectAreas: []Area{other}, ProjectRepos: map[string]Rigor{"acme/app": Critical}},
			Request{Repo: "acme/app", Paths: []string{"ui/button.go"}},
			Critical, FromProject, nil,
		},
		{
			"a rule that names this repository ranks by the member, its match aside",
			Config{ProjectAreas: []Area{both}, ProjectRepos: map[string]Rigor{"acme/app": Critical}},
			Request{Repo: "acme/app", Paths: []string{"ui/button.go"}},
			POC, FromArea, []string{"ui"},
		},
		{
			"the repository's own default still beats a member area",
			Config{ProjectAreas: []Area{whole}, ProjectRepos: map[string]Rigor{"acme/app": POC}, Repo: Startup},
			Request{Repo: "acme/app"},
			Startup, FromRepo, []string{"app"},
		},
		{
			"the strictest matched member area decides",
			Config{ProjectAreas: []Area{ui, whole}, ProjectRepos: map[string]Rigor{"acme/app": Startup}},
			Request{Repo: "acme/app", Paths: []string{"ui/button.go"}},
			Critical, FromArea, []string{"app", "ui"},
		},
		{
			"a run that names no repository reads neither the entry nor the member area",
			Config{ProjectAreas: []Area{ui}, ProjectRepos: map[string]Rigor{"acme/app": Critical}},
			Request{Paths: []string{"ui/button.go"}},
			Default, FromDefault, nil,
		},
	} {
		got := Resolve(c.cfg, c.req)
		if got.Level != c.wantLevel || got.From != c.wantFrom {
			t.Errorf("%s: Resolve = (%q, %q); want (%q, %q)", c.name, got.Level, got.From, c.wantLevel, c.wantFrom)
		}
		if names := areaNames(got.Areas); !slices.Equal(names, c.wantAreas) {
			t.Errorf("%s: areas = %v; want %v", c.name, names, c.wantAreas)
		}
	}
}

// TestConfiguredPerRepository pins the opt-in as a question about one
// repository (docs/repo-override-rulings/spec.md §5.4 "Opt-in gap"): a
// repository that never named a level files no backlog item and is judged
// without the mode's table, so what opts it in must be something the layers
// really say about *it*.
//
// The project layer's default and a project-wide area — a rule carrying a
// Match, in effect for every repository of the project — opt every repository
// of the project in. This repository is opted in by its own rigor.repos entry
// or by a member area naming it. Anything else the layer writes for another
// repository leaves it unconfigured: renaming the map's one entry from one
// repository to another must flip the answer.
//
// The last tier is the repository's own file, which release A drops (S1): its
// default and its own path areas, and the empty rigor: block, which stays an
// opt-in until release B retires it (XB3).
func TestConfiguredPerRepository(t *testing.T) {
	app := Area{Name: "ui", Members: []Member{{Repo: "acme/app", Paths: []string{"ui/**"}}}, Level: POC}
	web := Area{Name: "web", Members: []Member{{Repo: "acme/web"}}, Level: POC}
	wide := Area{Name: "payments", Match: "payments/**", Level: Critical}

	for _, c := range []struct {
		name string
		cfg  Config
		repo string
		want bool
	}{
		{"a zero Config", Config{}, "acme/app", false},
		{"a Config carrying only this repository's entry", Config{ProjectRepos: map[string]Rigor{"acme/app": Critical}}, "acme/app", true},
		{"a Config carrying another repository's entry", Config{ProjectRepos: map[string]Rigor{"acme/web": Critical}}, "acme/app", false},
		{"an entry the vocabulary does not know is no answer", Config{ProjectRepos: map[string]Rigor{"acme/app": ""}}, "acme/app", false},
		{"a member area naming this repository", Config{ProjectAreas: []Area{app}}, "acme/app", true},
		{"a member area naming another repository", Config{ProjectAreas: []Area{web}}, "acme/app", false},
		{"a member area and no repository named at all", Config{ProjectAreas: []Area{app}}, "", false},
		{"the project layer's default", Config{Project: Startup}, "acme/app", true},
		{"the project layer's default, no repository named", Config{Project: Startup}, "", true},
		{"a project-wide area", Config{ProjectAreas: []Area{wide}}, "acme/app", true},
		{"a project-wide area, no repository named", Config{ProjectAreas: []Area{wide}}, "", true},
		{"a project-wide area, another repository's run", Config{ProjectAreas: []Area{wide}}, "acme/web", true},
		{"the repository's own block, empty", Config{RepoBlock: true}, "acme/app", true},
		{"the repository's own default", Config{Repo: Pro}, "acme/app", true},
		{"the repository's own areas", Config{RepoAreas: []Area{app}}, "acme/app", true},
	} {
		if got := c.cfg.Configured(c.repo); got != c.want {
			t.Errorf("%s: Configured(%q) = %v; want %v", c.name, c.repo, got, c.want)
		}
	}
}
