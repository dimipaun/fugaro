package rigor

import (
	"slices"
	"testing"
)

// TestResolveLayerPrecedence pins D6/D20/D21: the narrowest layer that sets a
// mode wins — an area the run's paths matched, then the repository's own
// value, then the project default, then Fugaro's own default — and a
// narrower layer may set a *lower* mode than a wider one, because the order
// is a precedence and not an only-tightens rule.
func TestResolveLayerPrecedence(t *testing.T) {
	cases := []struct {
		name     string
		cfg      Config
		req      Request
		wantMode Mode
		wantFrom Layer
		wantArea []string
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
			Config{Repo: POC, RepoAreas: []Area{{Name: "pay", Match: "p/**", Mode: Critical}}},
			Request{Paths: []string{"p/x.go"}},
			Critical, FromArea, []string{"pay"},
		},
		{
			"a matched area may go below the repository (D21)",
			Config{Repo: Critical, RepoAreas: []Area{{Name: "docs", Match: "d/**", Mode: POC}}},
			Request{Paths: []string{"d/x.md"}},
			POC, FromArea, []string{"docs"},
		},
		{
			"the strictest of the matched areas decides (D23)",
			Config{
				ProjectAreas: []Area{{Name: "docs", Match: "d/**", Mode: POC}},
				RepoAreas:    []Area{{Name: "pay", Match: "p/**", Mode: Critical}},
			},
			Request{Paths: []string{"d/x.md", "p/x.go"}},
			Critical, FromArea, []string{"pay", "docs"},
		},
		{
			"a run that matches no area keeps the layer below it",
			Config{Repo: Startup, RepoAreas: []Area{{Name: "pay", Match: "p/**", Mode: Critical}}},
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
			Config{Repo: Startup}, Request{Mode: Critical},
			Critical, FromRequest, nil,
		},
		{
			"a request cannot lower",
			Config{Repo: Critical}, Request{Mode: POC},
			Critical, FromRepo, nil,
		},
		{
			"a request cannot lower an area",
			Config{RepoAreas: []Area{{Name: "pay", Match: "p/**", Mode: Critical}}},
			Request{Mode: POC, Paths: []string{"p/x.go"}},
			Critical, FromArea, []string{"pay"},
		},
		{
			"a request equal to the resolved mode leaves the tighter layer named",
			Config{Repo: Critical}, Request{Mode: Critical},
			Critical, FromRepo, nil,
		},
		{
			"a request for hotfix raises nothing: it is off the axis",
			Config{Repo: Pro}, Request{Mode: Hotfix},
			Pro, FromRepo, nil,
		},
		{
			"an area may set hotfix, and it is then the answer",
			Config{Repo: Pro, RepoAreas: []Area{{Name: "chore", Match: "c/**", Mode: Hotfix}}},
			Request{Paths: []string{"c/x"}},
			Hotfix, FromArea, []string{"chore"},
		},
	}
	for _, c := range cases {
		got := Resolve(c.cfg, c.req)
		if got.Mode != c.wantMode || got.From != c.wantFrom {
			t.Errorf("%s: Resolve = (%q, %q); want (%q, %q)", c.name, got.Mode, got.From, c.wantMode, c.wantFrom)
		}
		if !slices.Equal(got.Areas, c.wantArea) {
			t.Errorf("%s: areas = %v; want %v", c.name, got.Areas, c.wantArea)
		}
	}
}

// TestResolveNeverAnswersAnEmptyMode pins the shape every caller relies on:
// whatever the configuration and the request say, a resolution carries a mode
// a runner can run. An empty answer would be a run with no rigor at all.
func TestResolveNeverAnswersAnEmptyMode(t *testing.T) {
	for _, cfg := range []Config{{}, {Project: ""}, {Repo: ""}, {Project: POC}, {Repo: Hotfix}} {
		for _, req := range []Request{{}, {Mode: ""}, {Reached: ""}, {Paths: []string{"x"}}} {
			if got := Resolve(cfg, req); !Known(got.Mode) {
				t.Errorf("Resolve(%+v, %+v).Mode = %q; want a mode", cfg, req, got.Mode)
			}
		}
	}
}

// TestResolveHoldsTheModeAReachedRunAlreadyReached pins D22's monotone rule:
// a mode a run reaches never falls, so a second resolution that comes back
// weaker leaves the reached value standing, and the layer that set it is the
// one that set the reached value rather than the weaker current one.
func TestResolveHoldsTheModeAReachedRunAlreadyReached(t *testing.T) {
	// The first moment resolved Critical from an area; the run's measured
	// diff at the second moment matches nothing, and the layers below would
	// answer Pro. The reached value stands.
	cfg := Config{Repo: Pro, RepoAreas: []Area{{Name: "pay", Match: "p/**", Mode: Critical}}}
	first := Resolve(cfg, Request{Paths: []string{"p/x.go"}})
	if first.Mode != Critical || first.From != FromArea {
		t.Fatalf("first moment = (%q, %q); want (critical, area)", first.Mode, first.From)
	}
	second := Resolve(cfg, Request{Paths: []string{"README.md"}, Reached: first.Mode})
	if second.Mode != Critical {
		t.Errorf("second moment = %q; a mode a run reached never falls", second.Mode)
	}
	if second.From != FromReached {
		t.Errorf("second moment from = %q; want %q, the layer says the run is holding an earlier answer", second.From, FromReached)
	}

	// And a later moment that resolves stronger still moves up.
	third := Resolve(cfg, Request{Paths: []string{"p/x.go"}, Reached: Pro})
	if third.Mode != Critical || third.From != FromArea {
		t.Errorf("third moment = (%q, %q); want (critical, area)", third.Mode, third.From)
	}
}

// TestResolveKeepsTheAreasItMatchedAtTheSecondMoment pins that D24's areas
// are the ones the current resolution matched: a moment whose paths match
// nothing records no area, even when the mode is held by D22's monotone rule.
// The two facts are separate — the mode the run is at, and the rules this
// moment saw — and a record that conflated them would claim a rule decided a
// diff it never matched.
func TestResolveKeepsTheAreasItMatchedAtTheSecondMoment(t *testing.T) {
	cfg := Config{RepoAreas: []Area{{Name: "pay", Match: "p/**", Mode: Critical}}}
	got := Resolve(cfg, Request{Paths: []string{"README.md"}, Reached: Critical})
	if got.Mode != Critical {
		t.Fatalf("mode = %q; want the reached value held", got.Mode)
	}
	if got.Areas != nil {
		t.Errorf("areas = %v; want none: this moment's paths matched no rule", got.Areas)
	}
}

// TestStrictestOverASetIsWhatResolveReads pins that the answer Resolve
// carries for several matched areas is Strictest's, on a set wide enough that
// a hand-rolled reduction in either direction would differ.
func TestStrictestOverASetIsWhatResolveReads(t *testing.T) {
	areas := []Area{
		{Name: "a", Match: "a/**", Mode: POC},
		{Name: "b", Match: "b/**", Mode: Startup},
		{Name: "c", Match: "c/**", Mode: Pro},
		{Name: "d", Match: "d/**", Mode: Critical},
	}
	got := Resolve(Config{RepoAreas: areas}, Request{Paths: []string{"a/x", "b/x", "c/x", "d/x"}})
	if got.Mode != Critical {
		t.Fatalf("mode = %q; want %q", got.Mode, Critical)
	}
	if want := []string{"d", "c", "b", "a"}; !slices.Equal(got.Areas, want) {
		t.Errorf("areas = %v; want %v", got.Areas, want)
	}
}

// TestResolvePrecedenceHoldsOverEveryPairOfModes pins the property over every
// pair of on-axis modes rather than over the handful of pairs a hand-written
// table names: a narrower layer wins outright in *either* rank direction
// (D21), and on top of that a rank comparison runs in exactly two places — a
// request may only raise, and D22's reached value only holds. A comparison
// inverted anywhere in Resolve breaks some pair here, which the table cases
// above cannot show because they each pick one direction.
func TestResolvePrecedenceHoldsOverEveryPairOfModes(t *testing.T) {
	axis := Modes[:len(Modes)-1] // the four on-axis modes
	for _, a := range axis {
		for _, b := range axis {
			// a in the project layer, b in the repository layer: the
			// repository wins whatever the two ranks are.
			if got := Resolve(Config{Project: a, Repo: b}, Request{}); got.Mode != b {
				t.Errorf("Resolve(project %q, repo %q) = %q; want %q, the narrower layer wins outright", a, b, got.Mode, b)
			}
			// A rule the paths match beats the repository outright, in either
			// direction.
			cfg := Config{Repo: b, RepoAreas: []Area{{Name: "x", Match: "x/**", Mode: a}}}
			if got := Resolve(cfg, Request{Paths: []string{"x/y"}}); got.Mode != a {
				t.Errorf("Resolve(area %q over repo %q) = %q; want %q", a, b, got.Mode, a)
			}
			// A request may only raise: the answer is never weaker than the
			// mode the layers resolved.
			if got := Resolve(Config{Repo: b}, Request{Mode: a}); got.Mode.Rank() < b.Rank() {
				t.Errorf("Resolve(repo %q, request %q) = %q; a request lowered the mode", b, a, got.Mode)
			}
			// The same for the reached value: never weaker than the mode this
			// resolution would otherwise answer.
			if got := Resolve(Config{Repo: b}, Request{Reached: a}); got.Mode.Rank() < b.Rank() {
				t.Errorf("Resolve(repo %q, reached %q) = %q; a reached value lowered the mode", b, a, got.Mode)
			}
			// And each of the two raise paths does move up when it should:
			// the stricter of the two is the answer.
			if got := Resolve(Config{Repo: b}, Request{Mode: a}); got.Mode != Strictest(a, b) {
				t.Errorf("Resolve(repo %q, request %q) = %q; want %q", b, a, got.Mode, Strictest(a, b))
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
	onAxis, offAxis := Modes[:len(Modes)-1], Hotfix
	for _, m := range append([]Mode{""}, Modes...) {
		// Every path through Resolve, over every value that changes its
		// answer: a mode in each layer, an area rule, a request, a reached
		// value, and nothing at all.
		for _, cfg := range []Config{
			{},
			{Project: m},
			{Repo: m},
			{Project: onAxis[0], Repo: m},
			{Repo: m, RepoAreas: []Area{{Name: "x", Match: "x/**", Mode: onAxis[1]}}},
			{ProjectAreas: []Area{{Name: "y", Match: "y/**", Mode: offAxis}}, Repo: m},
		} {
			for _, req := range []Request{
				{},
				{Paths: []string{"x/y"}},
				{Mode: m},
				{Reached: m},
				{Mode: m, Paths: []string{"x/y"}, Reached: onAxis[2]},
			} {
				got := Resolve(cfg, req)
				if !Known(got.Mode) {
					t.Errorf("Resolve(%+v, %+v).Mode = %q, which is not in the vocabulary", cfg, req, got.Mode)
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
