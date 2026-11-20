package rigor

import (
	"slices"
	"testing"
)

// TestResolveLayerPrecedence pins D6/D20/D21: the narrowest layer that sets a
// level wins — an area the run's paths matched, then the repository's own
// value, then the project default, then Fugaro's own default — and a
// narrower layer may set a *lower* level than a wider one, because the order
// is a precedence and not an only-tightens rule.
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
			"a request cannot lower",
			Config{Repo: Critical}, Request{Level: POC},
			Critical, FromRepo, nil,
		},
		{
			"a request cannot lower an area",
			Config{RepoAreas: []Area{{Name: "pay", Match: "p/**", Level: Critical}}},
			Request{Level: POC, Paths: []string{"p/x.go"}},
			Critical, FromArea, []string{"pay"},
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

// TestResolveTakesAHotfixRequestWhole pins the owner's ruling of 2026-11-13
// (docs/rigor-command/implementation-plan.md R7): `fugaro run --level hotfix`
// takes effect with no opt-in, so a request for it decides the run whether the
// layers below resolved poc or critical, and whether an area rule matched. The
// gate D12 named is dropped rather than built, and nothing here reads a key
// that would gate it — the whole input is the request itself.
//
// The other half is the raise-only rule, which this must not have moved:
// every on-axis request is still compared by Rank and still cannot lower.
//
// Mutation (drop req.Level.PerRun() from Resolve's request block, leaving the
// rank comparison): hotfix ranks 0, so every case here answers the layer's
// level instead and the test fails on the first one.
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
	// Raise-only is unchanged for every other level: a request below what the
	// layers resolved still leaves the layers' answer standing, and one equal
	// to it still leaves the layer that set it named.
	for _, m := range []Rigor{POC, Startup} {
		if got := Resolve(Config{Repo: Critical}, Request{Level: m}); got.Level != Critical || got.From != FromRepo {
			t.Errorf("Resolve(repo critical, request %q) = (%q, %q); want (critical, repo): a request may not lower a run", m, got.Level, got.From)
		}
	}
}

// TestLayersRigorAnswersOnlyBesideARequestThatTakesTheRunWhole: the level the
// layers resolve is what a reader of the owner's floor holds beside the level
// the request chose (policy.Effective.ReviewFloorFor), because a request raises
// a level and never lowers a floor (D8, LF5). It is asked for exactly when the
// request took the run off the axis: for every other request the layers'
// answer is already the level the run is at, and "" is the answer that says so.
func TestLayersRigorAnswersOnlyBesideARequestThatTakesTheRunWhole(t *testing.T) {
	cfg := Config{
		Project:      Startup,
		Repo:         Pro,
		RepoAreas:    []Area{{Name: "pay", Match: "p/**", Level: Critical}},
		ProjectAreas: nil,
	}
	for _, c := range []struct {
		name string
		req  Request
		want Rigor
	}{
		{"a request for hotfix", Request{Level: Hotfix}, Pro},
		{"a request for hotfix over an area", Request{Level: Hotfix, Paths: []string{"p/x.go"}}, Critical},
		{"a request for an on-axis level", Request{Level: Critical}, ""},
		{"no request at all", Request{}, ""},
	} {
		if got := LayersRigor(cfg, c.req, ""); got != c.want {
			t.Errorf("%s: LayersRigor = %q; want %q", c.name, got, c.want)
		}
	}
	// reached folds the way Resolve's own Reached does, so a caller asking at
	// each of D22's moments never takes the answer down.
	if got := LayersRigor(Config{Repo: Startup}, Request{Level: Hotfix}, Critical); got != Critical {
		t.Errorf("LayersRigor(repo startup, request hotfix, reached critical) = %q; want critical: the level the layers gave never falls", got)
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
	if want := []Area{areas[3], areas[2], areas[1], areas[0]}; !slices.Equal(got.Areas, want) {
		t.Errorf("areas = %+v; want %+v", got.Areas, want)
	}
}

// TestResolvePrecedenceHoldsOverEveryPairOfModes pins the property over every
// pair of on-axis levels rather than over the handful of pairs a hand-written
// table names: a narrower layer wins outright in *either* rank direction
// (D21), and on top of that a rank comparison runs in exactly two places — a
// request may only raise, and D22's reached value only holds. A comparison
// inverted anywhere in Resolve breaks some pair here, which the table cases
// above cannot show because they each pick one direction.
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
			// A request may only raise: the answer is never weaker than the
			// level the layers resolved.
			if got := Resolve(Config{Repo: b}, Request{Level: a}); got.Level.Rank() < b.Rank() {
				t.Errorf("Resolve(repo %q, request %q) = %q; a request lowered the level", b, a, got.Level)
			}
			// The same for the reached value: never weaker than the level this
			// resolution would otherwise answer.
			if got := Resolve(Config{Repo: b}, Request{Reached: a}); got.Level.Rank() < b.Rank() {
				t.Errorf("Resolve(repo %q, reached %q) = %q; a reached value lowered the level", b, a, got.Level)
			}
			// And each of the two raise paths does move up when it should:
			// the stricter of the two is the answer.
			if got := Resolve(Config{Repo: b}, Request{Level: a}); got.Level != Strictest(a, b) {
				t.Errorf("Resolve(repo %q, request %q) = %q; want %q", b, a, got.Level, Strictest(a, b))
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
