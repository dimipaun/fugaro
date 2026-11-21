package rigor

import (
	"slices"
	"testing"
)

// TestResolveLayerPrecedence pins D6/D20/D21: the narrowest rank that sets a
// level wins — an area the run's paths matched, then the repository's own
// value, then the project's answer for the repository, then the project
// default, then Fugaro's own default — and a narrower one may set a *lower*
// level than a wider one, because the order is a precedence and not an
// only-tightens rule.
func TestResolveLayerPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		cfg       Config
		req       Request
		wantLevel Rigor
		wantFrom  Layer
		wantArea  []string
	}{
		{
			"nothing set: Fugaro's own default, D19",
			Config{}, Request{},
			Pro, FromDefault, nil,
		},
		{
			"the project beats Fugaro's default",
			Config{Project: Critical}, Request{},
			Critical, FromProject, nil,
		},
		{
			"the project may go below the default (D21)",
			Config{Project: POC}, Request{},
			POC, FromProject, nil,
		},
		{
			"the repository beats the project",
			Config{Project: POC, Repo: Critical}, Request{},
			Critical, FromRepo, nil,
		},
		{
			"the repository may go below the project (D21)",
			Config{Project: Critical, Repo: POC}, Request{},
			POC, FromRepo, nil,
		},
		{
			"a matched area beats the repository",
			Config{Repo: POC, RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}},
			Request{Paths: []string{"p/x.go"}},
			Critical, FromArea, []string{"pay"},
		},
		{
			"a matched area may go below the repository (D21)",
			Config{Repo: Critical, RepoAreas: []Area{{Name: "docs", Match: "d/**", Level: POC}}},
			Request{Paths: []string{"d/x.md"}},
			POC, FromArea, []string{"docs"},
		},
		{
			"the strictest of the matched areas decides (D23)",
			Config{
				ProjectAreas: []Area{{Name: "docs", Match: "d/**", Level: POC}},
				RepoAreas:    []Area{{Name: "pay", Match: "p/**", Level: Critical}},
			},
			Request{Paths: []string{"d/x.md", "p/x.go"}},
			Critical, FromArea, []string{"pay", "docs"},
		},
		{
			"a run that matches no area keeps the layer below it",
			Config{Repo: Startup, RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}},
			Request{Paths: []string{"README.md"}},
			Startup, FromRepo, nil,
		},
		{
			"an unset layer says nothing and changes nothing",
			Config{Project: Startup}, Request{},
			Startup, FromProject, nil,
		},
		{
			"a request raises",
			Config{Repo: Startup}, Request{Level: Critical},
			Critical, FromRequest, nil,
		},
		{
			"a request lowers a repository default (LD3)",
			Config{Repo: Critical}, Request{Level: POC},
			POC, FromRequest, nil,
		},
		{
			"a request lowers an area rule, since a floor is what holds it",
			Config{RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}},
			Request{Level: POC, Paths: []string{"p/x.go"}},
			POC, FromRequest, []string{"pay"},
		},
		{
			"a repository's own rule lowers a project area (D20 as amended)",
			Config{
				ProjectAreas: []Area{{Name: "payments", Match: "payments/**", Level: Critical}},
				RepoAreas:    []Area{{Name: "own", Match: "payments/**", Level: POC}},
			},
			Request{Paths: []string{"payments/x.go"}},
			POC, FromArea, []string{"payments", "own"},
		},
		{
			"the project's entry for the repository outranks its own area",
			Config{
				Project:      Startup,
				ProjectAreas: []Area{{Name: "payments", Match: "payments/**", Level: POC}},
				ProjectRepos: map[string]Rigor{"acme/app": Critical},
			},
			Request{Repo: "acme/app", Paths: []string{"payments/x.go"}},
			Critical, FromProject, []string{"payments"},
		},
		{
			"a request equal to the resolved level leaves the tighter layer named",
			Config{Repo: Critical}, Request{Level: Critical},
			Critical, FromRepo, nil,
		},
		{
			"a request for hotfix is taken whole: it needs no opt-in and is not compared by rank",
			Config{Repo: Pro}, Request{Level: Hotfix},
			Hotfix, FromRequest, nil,
		},
		{
			"a request for hotfix decides over an area rule too",
			Config{Repo: Pro, RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}},
			Request{Level: Hotfix, Paths: []string{"p/x.go"}},
			Hotfix, FromRequest, []string{"pay"},
		},
		{
			"an area may set hotfix, and it is then the answer",
			Config{Repo: Pro, RepoAreas: []Area{{Name: "chore", Match: "c/**", Level: Hotfix}}},
			Request{Paths: []string{"c/x"}},
			Hotfix, FromArea, []string{"chore"},
		},
		{
			"a carried request lowers no repository default (T5.2, LF5 §3a)",
			Config{Repo: Critical}, Request{Level: POC, Carried: true},
			Critical, FromRepo, nil,
		},
		{
			"a carried request lowers no area rule either",
			Config{RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}},
			Request{Level: POC, Carried: true, Paths: []string{"p/x.go"}},
			Critical, FromArea, []string{"pay"},
		},
		{
			"a carried request raises",
			Config{Repo: Startup}, Request{Level: Critical, Carried: true},
			Critical, FromRequest, nil,
		},
		{
			"a carried request for the level the layers set leaves them named",
			Config{Repo: Pro}, Request{Level: Pro, Carried: true},
			Pro, FromRepo, nil,
		},
		{
			"a carried hotfix is still taken whole",
			Config{Repo: Critical}, Request{Level: Hotfix, Carried: true},
			Hotfix, FromRequest, nil,
		},
	}
	for _, c := range cases {
		got := Resolve(c.cfg, c.req)
		if got.Level != c.wantLevel || got.From != c.wantFrom {
			t.Errorf("%s: Resolve = (%q, %q); want (%q, %q)", c.name, got.Level, got.From, c.wantLevel, c.wantFrom)
		}
		if names := areaNames(got.Areas); !slices.Equal(names, c.wantArea) {
			t.Errorf("%s: areas = %v; want %v", c.name, names, c.wantArea)
		}
	}
}

// resolveRanks is the order Resolve states once, weakest first (LD3) — the
// same order docs/project-layer.md §2 states for a reader — with the way a
// test sets that one rank's level. The two area ranks match different paths
// and carry different names, so a test that sets several ranks at once reads
// each of them.
var resolveRanks = []struct {
	name string
	set  func(*Config, *Request, Rigor)
	from Layer
}{
	{"Fugaro's own default", func(*Config, *Request, Rigor) {}, FromDefault},
	{"the project layer's default", func(c *Config, _ *Request, l Rigor) { c.Project = l }, FromProject},
	{"a project area the repo and paths match", func(c *Config, r *Request, l Rigor) {
		c.ProjectAreas = append(c.ProjectAreas, Area{Name: "payments", Match: "payments/**", Level: l})
		r.Paths = append(r.Paths, "payments/x.go")
	}, FromArea},
	{"the project layer's entry for the repository", func(c *Config, r *Request, l Rigor) {
		c.ProjectRepos = map[string]Rigor{"acme/app": l}
		r.Repo = "acme/app"
	}, FromProject},
	{"the repository's own default", func(c *Config, _ *Request, l Rigor) { c.Repo = l }, FromRepo},
	{"a rule of the repository's own areas", func(c *Config, r *Request, l Rigor) {
		c.RepoAreas = append(c.RepoAreas, Area{Name: "own", Match: "own/**", Level: l})
		r.Paths = append(r.Paths, "own/x.go")
	}, FromArea},
	{"the run's own request", func(_ *Config, r *Request, l Rigor) { r.Level = l }, FromRequest},
}

// TestResolveStatesTheOrderOnce is LD3's table: the order is stated in one
// function and it is this list — Fugaro's own default, the project layer's
// default, a project area, the project's own entry for the repository, the
// repository's own default, one of its areas, and the run's own request. Rank
// i is set to Pro and every weaker rank to POC, so the answer must be rank i's
// level and rank i's layer: a rank read out of order, or left out of the
// resolver altogether, fails here.
//
// The first rank cannot be set — Fugaro's own default is Pro (D19), which is
// the level that row is owed.
func TestResolveStatesTheOrderOnce(t *testing.T) {
	for i, rank := range resolveRanks {
		var cfg Config
		var req Request
		for _, weaker := range resolveRanks[:i] {
			weaker.set(&cfg, &req, POC)
		}
		rank.set(&cfg, &req, Pro)
		got := Resolve(cfg, req)
		if got.Level != Pro || got.From != rank.from {
			t.Errorf("rank %d (%s) = (%q, %q); want (%q, %q): it must beat every rank below it",
				i, rank.name, got.Level, got.From, Pro, rank.from)
		}
	}
}

// TestResolveOrderIsAPrecedenceNotAnOnlyTightensRule states D21 over that same
// list: every rank may set a level *below* the one the rank under it set and is
// still the answer, so the order is a precedence rather than an only-tightens
// rule — which is what a default is for. The one pair where the wider rank
// cannot be set is the first: Fugaro's own default is Pro whatever a test says,
// and the project layer going below it is the pair under it.
func TestResolveOrderIsAPrecedenceNotAnOnlyTightensRule(t *testing.T) {
	for i := 0; i+1 < len(resolveRanks); i++ {
		wider, narrower := resolveRanks[i], resolveRanks[i+1]
		var cfg Config
		var req Request
		wider.set(&cfg, &req, Critical)
		narrower.set(&cfg, &req, POC)
		got := Resolve(cfg, req)
		if got.Level != POC || got.From != narrower.from {
			t.Errorf("%s (%q) over %s (%q) = (%q, %q); want (%q, %q): a narrower rank wins in either direction (D21)",
				narrower.name, POC, wider.name, Critical, got.Level, got.From, POC, narrower.from)
		}
	}
}

// TestResolveReadsTheProjectsPerRepoLevel pins the project layer's per-repo
// map (LD4): one repository of the project and the level it runs at, stated by
// the layer that speaks for more than one repository. It sits where the spec
// puts it and where D20 as amended leaves it — stronger than the project's own
// default, weaker than the repository's own file, so a repository that states
// its own level still wins over the project's entry for it. A run of a
// repository the map does not name is unchanged, and so is a resolution whose
// request carries no repository at all.
func TestResolveReadsTheProjectsPerRepoLevel(t *testing.T) {
	cfg := Config{
		Project:      Startup,
		ProjectRepos: map[string]Rigor{"acme/payments-core": Critical},
	}
	if got := Resolve(cfg, Request{Repo: "acme/payments-core"}); got.Level != Critical || got.From != FromProject {
		t.Errorf("Resolve(project startup, payments-core critical) = (%q, %q); want (%q, %q)", got.Level, got.From, Critical, FromProject)
	}
	if got := Resolve(cfg, Request{Repo: "acme/console-web"}); got.Level != Startup || got.From != FromProject {
		t.Errorf("Resolve of a repository the map does not name = (%q, %q); want the project default (%q, %q)", got.Level, got.From, Startup, FromProject)
	}
	if got := Resolve(cfg, Request{}); got.Level != Startup || got.From != FromProject {
		t.Errorf("Resolve with no repository = (%q, %q); want the project default (%q, %q)", got.Level, got.From, Startup, FromProject)
	}
	// D20 as amended: the repository's own file beats every project-level
	// setting, its per-repo entry included.
	own := Resolve(Config{Project: Startup, ProjectRepos: map[string]Rigor{"acme/payments-core": Critical}, Repo: POC},
		Request{Repo: "acme/payments-core"})
	if own.Level != POC || own.From != FromRepo {
		t.Errorf("Resolve(repo poc over the project's per-repo critical) = (%q, %q); want (%q, %q)", own.Level, own.From, POC, FromRepo)
	}
	// A level the map states but the vocabulary does not know is no level: a
	// hand-built Config reaches here, and the resolution must still carry one.
	if got := Resolve(Config{ProjectRepos: map[string]Rigor{"acme/app": ""}}, Request{Repo: "acme/app"}); got.Level != Default {
		t.Errorf("Resolve over an unknown per-repo level = %q; want %q", got.Level, Default)
	}
	// And the field opts a Config in on its own: a project that states nothing
	// but this map is configured.
	if !cfg.Configured() {
		t.Error("a Config carrying only ProjectRepos reports itself unconfigured, so a run would skip the resolver")
	}
}

// TestResolveTakesAHotfixRequestWhole pins the owner's ruling of 2026-11-13
// (docs/rigor-command/implementation-plan.md R7): `fugaro run --level hotfix`
// takes effect with no opt-in, so a request for it decides the run whether the
// layers below resolved poc or critical, and whether an area rule matched. The
// gate D12 named is dropped rather than built, and nothing here reads a key
// that would gate it — the whole input is the request itself.
//
// The other half is the on-axis request, which the order gives the same rank:
// it settles the level whether it raises or lowers what the layers set (LD3),
// by its own value and not by a rank comparison.
//
// Mutation (compare the request by Rank instead of taking it): hotfix ranks 0,
// so every case here answers the layer's level instead and the test fails on
// the first one.
func TestResolveTakesAHotfixRequestWhole(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		req  Request
	}{
		{"over the built-in default", Config{}, Request{Level: Hotfix}},
		{"over a project default", Config{Project: Critical}, Request{Level: Hotfix}},
		{"over a repository default", Config{Repo: Critical}, Request{Level: Hotfix}},
		{"over an area the paths match", Config{
			Repo:      Pro,
			RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}},
		}, Request{Level: Hotfix, Paths: []string{"p/x.go"}}},
	}
	for _, c := range cases {
		got := Resolve(c.cfg, c.req)
		if got.Level != Hotfix || got.From != FromRequest {
			t.Errorf("%s: Resolve = (%q, %q); want (%q, %q): --level hotfix takes effect with no opt-in",
				c.name, got.Level, got.From, Hotfix, FromRequest)
		}
	}
	// D22's monotone rule is untouched: a level the run has already reached
	// still holds over a weaker answer. The request is given to every moment
	// the runner resolves at, so a run that asked for hotfix reached it at
	// bootstrap and this is the defensive reading of a record that says
	// otherwise — the fail-closed one.
	if got := Resolve(Config{Repo: Pro}, Request{Level: Hotfix, Reached: Critical}); got.Level != Critical {
		t.Errorf("Resolve(repo pro, request hotfix, reached critical) = %q; want critical: a level a run has reached never falls", got.Level)
	}
	// An on-axis request is the strongest rank too: a request below what the
	// layers resolved is the answer, and one equal to it still leaves the
	// layer that set it named, since that is the narrower reason for the same
	// level. What holds a request back is a floor, outside every layer, and
	// not this function (config.ResolveLevels).
	for _, m := range []Rigor{POC, Startup} {
		if got := Resolve(Config{Repo: Critical}, Request{Level: m}); got.Level != m || got.From != FromRequest {
			t.Errorf("Resolve(repo critical, request %q) = (%q, %q); want (%q, request): the run's own request is the narrowest rank", m, got.Level, got.From, m)
		}
	}
	if got := Resolve(Config{Repo: Critical}, Request{Level: Critical}); got.Level != Critical || got.From != FromRepo {
		t.Errorf("Resolve(repo critical, request critical) = (%q, %q); want (critical, repo): the layer that already set the level is the narrower reason", got.Level, got.From)
	}
}

// TestLayersRigorAnswersOnlyBesideARequestBelowThem: the level the layers
// alone resolve is what a reader of the owner's floor holds beside the level
// the request chose (policy.Effective.ReviewFloorFor), because a request
// lowers a level and never a floor (D8, LF5). It is asked for exactly when the
// request went below the layers: a request that raised the run, or that asked
// for the level the layers already set, leaves it at or above them, and "" is
// the answer that says so. A request for hotfix asks for none of it: hotfix is
// held to its own floor row and to nothing beside it (the owner's ruling of
// 2026-11-20).
func TestLayersRigorAnswersOnlyBesideARequestBelowThem(t *testing.T) {
	cfg := Config{
		Project:   Startup,
		Repo:      Pro,
		RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}},
	}
	for _, c := range []struct {
		name string
		req  Request
		want Rigor
	}{
		{"a request below the layers", Request{Level: Startup}, Pro},
		{"a request below an area the paths match", Request{Level: POC, Paths: []string{"p/x.go"}}, Critical},
		{"a request for the level the layers set", Request{Level: Pro}, ""},
		{"a request above the layers", Request{Level: Critical}, ""},
		{"a request for hotfix", Request{Level: Hotfix}, ""},
		{"a request for hotfix over an area", Request{Level: Hotfix, Paths: []string{"p/x.go"}}, ""},
		{"no request at all", Request{}, ""},
		// A carried request never lowers, so it leaves no row behind it: the
		// run is at the layers' own level and reads their row as its own
		// (task's Spec.RigorCarried, T5.2, D1844).
		{"a carried request that the layers outrank", Request{Level: POC, Carried: true}, ""},
		{"a carried request below an area the paths match", Request{Level: POC, Carried: true, Paths: []string{"p/x.go"}}, ""},
	} {
		if got := LayersRigor(cfg, c.req, ""); got != c.want {
			t.Errorf("%s: LayersRigor = %q; want %q", c.name, got, c.want)
		}
	}
	// reached folds the way Resolve's own Reached does, so a caller asking at
	// each of D22's moments never takes the answer down.
	if got := LayersRigor(Config{Repo: Critical}, Request{Level: POC}, Startup); got != Critical {
		t.Errorf("LayersRigor(repo critical, request poc, reached startup) = %q; want critical: the level the layers gave never falls", got)
	}
	// The repository's own half of the order is read too: a request below the
	// project layer's entry for the repository answers that entry.
	if got := LayersRigor(Config{ProjectRepos: map[string]Rigor{"acme/app": Critical}}, Request{Repo: "acme/app", Level: POC}, ""); got != Critical {
		t.Errorf("LayersRigor(project per-repo critical, request poc) = %q; want critical: the floor holds the project's own answer", got)
	}
}

// TestResolveNeverAnswersAnEmptyRigor pins the shape every caller relies on:
// whatever the configuration and the request say, a resolution carries a level
// a runner can run. An empty answer would be a run with no rigor at all.
func TestResolveNeverAnswersAnEmptyRigor(t *testing.T) {
	for _, cfg := range []Config{{}, {Project: ""}, {Repo: ""}, {Project: POC}, {Repo: Hotfix}} {
		for _, req := range []Request{{}, {Level: ""}, {Reached: ""}, {Paths: []string{"x"}}} {
			if got := Resolve(cfg, req); !Known(got.Level) {
				t.Errorf("Resolve(%+v, %+v).Level = %q; want a level", cfg, req, got.Level)
			}
		}
	}
}

// TestResolveHoldsTheModeAReachedRunAlreadyReached pins D22's monotone rule:
// a level a run reaches never falls, so a second resolution that comes back
// weaker leaves the reached value standing, and the layer that set it is the
// one that set the reached value rather than the weaker current one.
func TestResolveHoldsTheModeAReachedRunAlreadyReached(t *testing.T) {
	// The first moment resolved Critical from an area; the run's measured
	// diff at the second moment matches nothing, and the layers below would
	// answer Pro. The reached value stands.
	cfg := Config{Repo: Pro, RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}}
	first := Resolve(cfg, Request{Paths: []string{"p/x.go"}})
	if first.Level != Critical || first.From != FromArea {
		t.Fatalf("first moment = (%q, %q); want (critical, area)", first.Level, first.From)
	}
	second := Resolve(cfg, Request{Paths: []string{"README.md"}, Reached: first.Level})
	if second.Level != Critical {
		t.Errorf("second moment = %q; a level a run reached never falls", second.Level)
	}
	if second.From != FromReached {
		t.Errorf("second moment from = %q; want %q, the layer says the run is holding an earlier answer", second.From, FromReached)
	}

	// And a later moment that resolves stronger still moves up.
	third := Resolve(cfg, Request{Paths: []string{"p/x.go"}, Reached: Pro})
	if third.Level != Critical || third.From != FromArea {
		t.Errorf("third moment = (%q, %q); want (critical, area)", third.Level, third.From)
	}
}

// TestResolveKeepsTheAreasItMatchedAtTheSecondMoment pins that D24's areas
// are the ones the current resolution matched: a moment whose paths match
// nothing records no area, even when the level is held by D22's monotone rule.
// The two facts are separate — the level the run is at, and the rules this
// moment saw — and a record that conflated them would claim a rule decided a
// diff it never matched.
func TestResolveKeepsTheAreasItMatchedAtTheSecondMoment(t *testing.T) {
	cfg := Config{RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}}
	got := Resolve(cfg, Request{Paths: []string{"README.md"}, Reached: Critical})
	if got.Level != Critical {
		t.Fatalf("level = %q; want the reached value held", got.Level)
	}
	if got.Areas != nil {
		t.Errorf("areas = %v; want none: this moment's paths matched no rule", got.Areas)
	}
}

// TestStrictestOverASetIsWhatResolveReads pins that the answer Resolve
// carries for several matched areas is Strictest's, on a set wide enough that
// a hand-rolled reduction in either direction would differ — and that what it
// carries is the rules themselves, strictest first, not just their names: a
// caller that folds two resolutions together (internal/runstore's
// RigorRecord.Applied) re-establishes D23's order from exactly these levels.
func TestStrictestOverASetIsWhatResolveReads(t *testing.T) {
	areas := []Area{
		{Name: "a", Match: "a/**", Level: POC},
		{Name: "b", Match: "b/**", Level: Startup},
		{Name: "c", Match: "c/**", Level: Pro},
		{Name: "d", Match: "d/**", Level: Critical},
	}
	got := Resolve(Config{RepoAreas: areas}, Request{Paths: []string{"a/x", "b/x", "c/x", "d/x"}})
	if got.Level != Critical {
		t.Fatalf("level = %q; want %q", got.Level, Critical)
	}
	if want := []Area{areas[3], areas[2], areas[1], areas[0]}; !slices.Equal(areaNames(got.Areas), areaNames(want)) {
		t.Errorf("areas = %+v; want %+v", got.Areas, want)
	}
}

// TestResolvePrecedenceHoldsOverEveryPairOfModes pins the property over every
// pair of on-axis levels rather than over the handful of pairs a hand-written
// table names: a narrower rank wins outright in *either* rank direction (D21),
// and on top of that a rank comparison runs in exactly one place — D22's
// reached value only holds — while the request settles the level by its own
// value. A comparison inverted anywhere in Resolve breaks some pair here,
// which the table cases above cannot show because they each pick one
// direction.
func TestResolvePrecedenceHoldsOverEveryPairOfModes(t *testing.T) {
	axis := Rigors[:len(Rigors)-1] // the four on-axis levels
	for _, a := range axis {
		for _, b := range axis {
			// a in the project layer, b in the repository layer: the
			// repository wins whatever the two ranks are.
			if got := Resolve(Config{Project: a, Repo: b}, Request{}); got.Level != b {
				t.Errorf("Resolve(project %q, repo %q) = %q; want %q, the narrower layer wins outright", a, b, got.Level, b)
			}
			// A rule the paths match beats the repository outright, in either
			// direction.
			cfg := Config{Repo: b, RepoAreas: []Area{{Name: "x", Match: "x/**", Level: a}}}
			if got := Resolve(cfg, Request{Paths: []string{"x/y"}}); got.Level != a {
				t.Errorf("Resolve(area %q over repo %q) = %q; want %q", a, b, got.Level, a)
			}
			// The run's own request is the narrowest rank of all: it settles
			// the level whatever the layers said, in either direction.
			if got := Resolve(Config{Repo: b}, Request{Level: a}); got.Level != a {
				t.Errorf("Resolve(repo %q, request %q) = %q; want %q, the run's own request", b, a, got.Level, a)
			}
			// The reached value only holds: never weaker than the level this
			// resolution would otherwise answer.
			if got := Resolve(Config{Repo: b}, Request{Reached: a}); got.Level.Rank() < b.Rank() {
				t.Errorf("Resolve(repo %q, reached %q) = %q; a reached value lowered the level", b, a, got.Level)
			}
			// The project's entry for the repository beats its own default and
			// its own areas: the one rank of the order that is read between
			// them (LD4, LF4).
			perRepo := Config{Project: a, ProjectRepos: map[string]Rigor{"acme/app": b}}
			if got := Resolve(perRepo, Request{Repo: "acme/app"}); got.Level != b {
				t.Errorf("Resolve(project %q, its entry for the repo %q) = %q; want %q", a, b, got.Level, b)
			}
			// And a repository's own rule settles the level over a project
			// area's, in either direction (D20 as amended).
			mixed := Config{
				ProjectAreas: []Area{{Name: "payments", Match: "x/**", Level: b}},
				RepoAreas:    []Area{{Name: "own", Match: "x/**", Level: a}},
			}
			if got := Resolve(mixed, Request{Paths: []string{"x/y"}}); got.Level != a {
				t.Errorf("Resolve(project area %q, repo area %q) = %q; want %q, the narrower layer's own rule", b, a, got.Level, a)
			}
		}
	}
}

// TestLayersIsTheSetResolveNames pins the closed set of `from` values (D24):
// every layer a resolution can name is in Layers, and every resolution names
// one of them. The second half is what makes the set complete rather than
// hand-kept: internal/runstore bounds a record's `from` with KnownLayer, so a
// layer a resolution can return and Layers does not carry would be a record
// field a reader silently drops.
func TestLayersIsTheSetResolveNames(t *testing.T) {
	onAxis, offAxis := Rigors[:len(Rigors)-1], Hotfix
	for _, m := range append([]Rigor{""}, Rigors...) {
		// Every path through Resolve, over every value that changes its
		// answer: a level in each layer, an area rule, a request, a reached
		// value, and nothing at all.
		for _, cfg := range []Config{
			{},
			{Project: m},
			{Repo: m},
			{Project: onAxis[0], Repo: m},
			{Repo: m, RepoAreas: []Area{{Name: "x", Match: "x/**", Level: onAxis[1]}}},
			{ProjectAreas: []Area{{Name: "y", Match: "y/**", Level: offAxis}}, Repo: m},
		} {
			for _, req := range []Request{
				{},
				{Paths: []string{"x/y"}},
				{Level: m},
				{Reached: m},
				{Level: m, Paths: []string{"x/y"}, Reached: onAxis[2]},
			} {
				got := Resolve(cfg, req)
				if !Known(got.Level) {
					t.Errorf("Resolve(%+v, %+v).Level = %q, which is not in the vocabulary", cfg, req, got.Level)
				}
				if !KnownLayer(got.From) {
					t.Errorf("Resolve(%+v, %+v).From = %q, which Layers does not name", cfg, req, got.From)
				}
			}
		}
	}
	// And the set is exactly the six: a value no resolution names is refused,
	// so KnownLayer cannot pass a typo of a real layer.
	for _, bad := range []Layer{"", "floors", "Area", "request ", "area\n"} {
		if KnownLayer(bad) {
			t.Errorf("KnownLayer(%q) = true; nothing resolves to it", bad)
		}
	}
	if len(Layers) != 6 {
		t.Errorf("Layers has %d entries; the vocabulary is the six D24/D6 name", len(Layers))
	}
}

// TestConfiguredIsAnyLayerSettingAnything: a zero Config is the unconfigured
// repository, and any one of the five fields opts it in.
func TestConfiguredIsAnyLayerSettingAnything(t *testing.T) {
	if (Config{}).Configured() {
		t.Error("a zero Config reports itself configured")
	}
	area := []Area{{Name: "docs", Match: "docs/**", Level: POC}}
	for name, c := range map[string]Config{
		"repo block":    {RepoBlock: true},
		"repo default":  {Repo: Pro},
		"repo areas":    {RepoAreas: area},
		"project level": {Project: Critical},
		"project areas": {ProjectAreas: area},
	} {
		if !c.Configured() {
			t.Errorf("%s: Configured() = false", name)
		}
	}
}
