package policy

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func TestMergePerRunMin(t *testing.T) {
	cases := []struct {
		name    string
		c, d, b float64
		want    float64
		src     string
		ignored int
	}{
		{"none", 0, 0, 0, 0, "", 0},
		{"ceiling only", 5, 0, 0, 5, "ceiling", 0},
		{"default only", 0, 2, 0, 2, "default-branch", 0},
		{"branch only", 0, 0, 3, 3, "branch", 0},
		{"default tightens", 5, 2, 0, 2, "default-branch", 0},
		{"default loosens", 5, 20, 0, 5, "ceiling", 1},
		{"branch loosens default", 5, 2, 500, 2, "default-branch", 1},
		{"branch tightens", 5, 2, 1, 1, "branch", 0},
		{"equal not ignored", 5, 5, 5, 5, "ceiling", 0},
		{"unset ceiling, branch loosens default", 0, 2, 9, 2, "default-branch", 1},
		{"negative is unset", -1, 0, 0, 0, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := Merge(Layer{PerRunUSD: tc.c}, Layer{PerRunUSD: tc.d}, Layer{PerRunUSD: tc.b})
			if e.PerRunUSD != tc.want || e.Sources[KeyPerRunUSD] != tc.src || len(e.Ignored) != tc.ignored {
				t.Fatalf("got %v src=%q ignored=%v", e.PerRunUSD, e.Sources[KeyPerRunUSD], e.Ignored)
			}
		})
	}
}

func TestMergeModeOrder(t *testing.T) {
	cases := []struct {
		c, d, b, want string
		ignored       int
	}{
		{"", "", "", "", 0},
		{"off", "", "", "off", 0},
		{"off", "observe", "", "observe", 0},
		{"off", "enforce", "observe", "enforce", 1},
		{"observe", "", "off", "observe", 1}, // a branch off under an observe ceiling stays observe
		{"enforce", "off", "off", "enforce", 2},
		{"", "observe", "enforce", "enforce", 0},
		{"observe", "observe", "observe", "observe", 0},
		{"bogus", "", "", "", 0},
	}
	for _, tc := range cases {
		e := Merge(Layer{Mode: tc.c}, Layer{Mode: tc.d}, Layer{Mode: tc.b})
		if e.Mode != tc.want || len(e.Ignored) != tc.ignored {
			t.Errorf("%+v: got %q ignored=%v", tc, e.Mode, e.Ignored)
		}
	}
}

func TestBranchZeroTokenCapIgnoredByMerge(t *testing.T) {
	// 0 is "no value", never "no cap": it cannot lift a ceiling.
	e := Merge(Layer{MaxRunTokens: 1_000_000}, Layer{}, Layer{MaxRunTokens: 0})
	if e.MaxRunTokens != 1_000_000 || e.Sources[KeyMaxRunTokens] != "ceiling" || len(e.Ignored) != 0 {
		t.Fatalf("%+v", e)
	}
	e = Merge(Layer{MaxRunTokens: 1000}, Layer{MaxRunTokens: 5000}, Layer{MaxRunTokens: 10})
	if e.MaxRunTokens != 10 || e.Sources[KeyMaxRunTokens] != "branch" || len(e.Ignored) != 1 ||
		e.Ignored[0] != (Ignored{KeyMaxRunTokens, "5000", "1000", "ceiling"}) {
		t.Fatalf("%+v", e)
	}
}

func TestAllowedIntersectionOrderStable(t *testing.T) {
	e := Merge(Layer{AllowedModels: []string{"c", "a", "b", "a"}},
		Layer{AllowedModels: []string{"b", "c", "x"}})
	if want := []string{"c", "b"}; !reflect.DeepEqual(e.AllowedModels, want) {
		t.Fatalf("got %v want %v", e.AllowedModels, want)
	}
	if e.Sources[KeyAllowedModels] != "default-branch" || len(e.Ignored) != 1 {
		t.Fatalf("%+v", e)
	}
	// nil = unset: passes through, and the first set list is the source.
	e = Merge(Layer{}, Layer{AllowedModels: []string{"z", "y"}}, Layer{})
	if !reflect.DeepEqual(e.AllowedModels, []string{"z", "y"}) || e.Sources[KeyAllowedModels] != "default-branch" {
		t.Fatalf("%+v", e)
	}
	// an empty non-nil list is set (forbids everything), not unset.
	e = Merge(Layer{AllowedModels: []string{"a"}}, Layer{AllowedModels: []string{}})
	if e.AllowedModels == nil || len(e.AllowedModels) != 0 {
		t.Fatalf("%#v", e.AllowedModels)
	}
	if got := Merge(Layer{}, Layer{}, Layer{}).AllowedModels; got != nil {
		t.Fatalf("unset must stay nil, got %#v", got)
	}
}

func TestMergeIgnoredRecords(t *testing.T) {
	e := Merge(
		Layer{Mode: "enforce", PerRunUSD: 5, MaxRunTokens: 100, AllowedModels: []string{"a", "b"}},
		Layer{PerRunUSD: 2},
		Layer{Mode: "off", PerRunUSD: 500, MaxRunTokens: 200, AllowedModels: []string{"a", "b", "c"}},
	)
	want := []Ignored{
		{KeyPerRunUSD, "500", "2", "default-branch"},
		{KeyMaxRunTokens, "200", "100", "ceiling"},
		{KeyMode, "off", "enforce", "ceiling"},
		{KeyAllowedModels, "a,b,c", "a,b", "ceiling"},
	}
	if !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("got %+v\nwant %+v", e.Ignored, want)
	}
	if e.PerRunUSD != 2 || e.Mode != "enforce" || e.MaxRunTokens != 100 || !reflect.DeepEqual(e.AllowedModels, []string{"a", "b"}) {
		t.Fatalf("%+v", e)
	}
	// layers after an ignored value are compared to the running value.
	e = Merge(Layer{PerRunUSD: 5}, Layer{PerRunUSD: 9}, Layer{PerRunUSD: 7})
	if e.PerRunUSD != 5 || len(e.Ignored) != 2 || e.Ignored[1] != (Ignored{KeyPerRunUSD, "7", "5", "ceiling"}) {
		t.Fatalf("%+v", e)
	}
	// the ceiling is never ignored; with no tighten layers nothing is.
	if e := Merge(Layer{Mode: "off", PerRunUSD: 1}); len(e.Ignored) != 0 || e.Mode != "off" {
		t.Fatalf("%+v", e)
	}
}

func TestMergeNoLayersAndNoMutation(t *testing.T) {
	if e := Merge(Layer{}); e.Sources != nil || e.Ignored != nil || !reflect.DeepEqual(e.Layer, Layer{}) {
		t.Fatalf("%+v", e)
	}
	in := []string{"a", "b"}
	e := Merge(Layer{AllowedModels: in}, Layer{AllowedModels: []string{"b"}})
	e.AllowedModels[0] = "q"
	if !reflect.DeepEqual(in, []string{"a", "b"}) {
		t.Fatalf("input mutated: %v", in)
	}
}

func TestAllowedIgnoredPartialAndDisjoint(t *testing.T) {
	// partial overlap: the intersection applies AND the extra model is recorded.
	e := Merge(Layer{AllowedModels: []string{"a", "b"}}, Layer{AllowedModels: []string{"b", "z"}})
	if !reflect.DeepEqual(e.AllowedModels, []string{"b"}) || e.Sources[KeyAllowedModels] != "default-branch" {
		t.Fatalf("%+v", e)
	}
	want := []Ignored{{KeyAllowedModels, "b,z", "b", "ceiling"}}
	if !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("got %+v want %+v", e.Ignored, want)
	}
	// disjoint: empty non-nil (deny everything) and recorded.
	e = Merge(Layer{AllowedModels: []string{"a"}}, Layer{AllowedModels: []string{"z"}})
	if e.AllowedModels == nil || len(e.AllowedModels) != 0 || !e.HasAllowList() || e.Allows("a") || e.Allows("z") {
		t.Fatalf("%#v", e)
	}
	if want := []Ignored{{KeyAllowedModels, "z", "", "ceiling"}}; !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("got %+v", e.Ignored)
	}
	// a subset or equal list is not ignored.
	e = Merge(Layer{AllowedModels: []string{"a", "b"}}, Layer{AllowedModels: []string{"b"}}, Layer{AllowedModels: []string{"b"}})
	if len(e.Ignored) != 0 {
		t.Fatalf("%+v", e.Ignored)
	}
}

func TestHasAllowList(t *testing.T) {
	if e := Merge(Layer{}); e.HasAllowList() || !e.Allows("anything") {
		t.Fatalf("unset list must allow everything: %+v", e)
	}
	e := Merge(Layer{AllowedModels: []string{"a"}})
	if !e.HasAllowList() || !e.Allows("a") || e.Allows("b") {
		t.Fatalf("%+v", e)
	}
}

func TestInfCapIsUnset(t *testing.T) {
	inf := math.Inf(1)
	e := Merge(Layer{PerRunUSD: inf}, Layer{PerRunUSD: 3}, Layer{PerRunUSD: inf})
	if e.PerRunUSD != 3 || len(e.Ignored) != 0 || e.Sources[KeyPerRunUSD] != "default-branch" {
		t.Fatalf("%+v", e)
	}
	if e := Merge(Layer{PerRunUSD: inf}); e.PerRunUSD != 0 || e.Sources != nil {
		t.Fatalf("%+v", e)
	}
	nan := math.NaN()
	if e := Merge(Layer{PerRunUSD: nan}, Layer{PerRunUSD: 2}); e.PerRunUSD != 2 {
		t.Fatalf("%+v", e)
	}
}

func TestValidMode(t *testing.T) {
	for m, ok := range map[string]bool{"": false, "off": true, "observe": true, "enforce": true, "ENFORCE": false, "bogus": false} {
		if ValidMode(m) != ok {
			t.Errorf("ValidMode(%q) != %v", m, ok)
		}
	}
	// an unknown mode is unset: it neither sets nor is ignored.
	e := Merge(Layer{Mode: "observe"}, Layer{Mode: "bogus"})
	if e.Mode != "observe" || len(e.Ignored) != 0 {
		t.Fatalf("%+v", e)
	}
}

func randFloat(r *rand.Rand) float64 {
	switch r.Intn(8) {
	case 0:
		return math.NaN()
	case 1:
		return math.Inf(1)
	case 2:
		return math.Inf(-1)
	case 3:
		return -float64(r.Intn(4))
	case 4:
		return 0
	}
	return float64(1 + r.Intn(5))
}

func randLayer(r *rand.Rand) Layer {
	var l Layer
	l.PerRunUSD = randFloat(r)
	if r.Intn(3) > 0 {
		l.MaxRunTokens = int64(r.Intn(8) - 2)
	}
	l.Mode = []string{"", "off", "observe", "enforce", "bogus"}[r.Intn(5)]
	if r.Intn(3) > 0 {
		l.AllowedModels = []string{}
		for _, m := range []string{"a", "b", "c", "d", "a"} {
			if r.Intn(2) == 0 {
				l.AllowedModels = append(l.AllowedModels, m)
			}
		}
	}
	return l
}

func validCap(f float64) bool { return f > 0 && !math.IsInf(f, 0) && !math.IsNaN(f) }

func inList(l []string, m string) bool {
	for _, x := range l {
		if x == m {
			return true
		}
	}
	return false
}

// TestMergeNeverLoosens checks Merge against an independent oracle, including
// the Ignored records, over random layers (NaN, Inf, negatives, bogus modes,
// one to five layers).
func TestMergeNeverLoosens(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	ignoredSeen := 0
	ceilingIgnoredCandidates := 0
	for i := 0; i < 20000; i++ {
		n := 1 + r.Intn(5)
		ls := make([]Layer, n)
		for j := range ls {
			ls[j] = randLayer(r)
		}
		e := Merge(ls[0], ls[1:]...)

		// oracle: exactness.
		var minCap float64
		var minTok int64
		var rank int
		var set []string
		haveSet := false
		for _, l := range ls {
			if validCap(l.PerRunUSD) && (minCap == 0 || l.PerRunUSD < minCap) {
				minCap = l.PerRunUSD
			}
			if l.MaxRunTokens > 0 && (minTok == 0 || l.MaxRunTokens < minTok) {
				minTok = l.MaxRunTokens
			}
			if rk := modeRank(l.Mode); rk > rank {
				rank = rk
			}
			if l.AllowedModels != nil {
				if !haveSet {
					haveSet, set = true, dedupe(l.AllowedModels)
				} else {
					k := []string{}
					for _, m := range set {
						if inList(l.AllowedModels, m) {
							k = append(k, m)
						}
					}
					set = k
				}
			}
		}
		if e.PerRunUSD != minCap || e.MaxRunTokens != minTok || modeRank(e.Mode) != rank {
			t.Fatalf("not exact: %+v -> %+v", ls, e)
		}
		if haveSet != e.HasAllowList() || (haveSet && !reflect.DeepEqual(set, e.AllowedModels)) {
			t.Fatalf("allow-list not exact: %+v -> %#v want %#v", ls, e.AllowedModels, set)
		}
		if e.Sources[KeyAllowedModels] == "" && haveSet || e.Sources[KeyMode] == "" && rank > 0 ||
			e.Sources[KeyPerRunUSD] == "" && minCap > 0 || e.Sources[KeyMaxRunTokens] == "" && minTok > 0 {
			t.Fatalf("missing source: %+v -> %+v", ls, e)
		}

		// oracle: Ignored, replayed layer by layer against the running value.
		var want []Ignored
		runCap, runTok, runRank := 0.0, int64(0), 0
		runSet, runHave := []string(nil), false
		for _, l := range ls {
			if validCap(l.PerRunUSD) {
				if runCap == 0 || l.PerRunUSD < runCap {
					runCap = l.PerRunUSD
				} else if l.PerRunUSD > runCap {
					want = append(want, Ignored{Key: KeyPerRunUSD})
				}
			}
			if l.MaxRunTokens > 0 {
				if runTok == 0 || l.MaxRunTokens < runTok {
					runTok = l.MaxRunTokens
				} else if l.MaxRunTokens > runTok {
					want = append(want, Ignored{Key: KeyMaxRunTokens})
				}
			}
			if rk := modeRank(l.Mode); rk > 0 {
				if rk > runRank {
					runRank = rk
				} else if rk < runRank {
					want = append(want, Ignored{Key: KeyMode})
				}
			}
			if l.AllowedModels != nil {
				if !runHave {
					runHave, runSet = true, dedupe(l.AllowedModels)
				} else {
					extra := false
					for _, m := range l.AllowedModels {
						extra = extra || !inList(runSet, m)
					}
					k := []string{}
					for _, m := range runSet {
						if inList(l.AllowedModels, m) {
							k = append(k, m)
						}
					}
					runSet = k
					if extra {
						want = append(want, Ignored{Key: KeyAllowedModels})
					}
				}
			}
		}
		if len(want) != len(e.Ignored) {
			t.Fatalf("ignored count: %+v -> %+v want keys %+v", ls, e.Ignored, want)
		}
		for k := range want {
			if want[k].Key != e.Ignored[k].Key || e.Ignored[k].Source == "" || e.Ignored[k].Value == e.Ignored[k].Effective && e.Ignored[k].Key != KeyAllowedModels {
				t.Fatalf("ignored[%d]: %+v want key %s", k, e.Ignored[k], want[k].Key)
			}
		}
		ignoredSeen += len(want)

		// the ceiling (layer 0) is never recorded: with only a ceiling, no Ignored.
		one := Merge(ls[0])
		if len(one.Ignored) != 0 {
			t.Fatalf("ceiling ignored: %+v", one)
		}
		if n > 1 {
			ceilingIgnoredCandidates++
		}
	}
	// non-vacuity: the generator must actually exercise ignoring.
	if ignoredSeen < 1000 || ceilingIgnoredCandidates < 1000 {
		t.Fatalf("generator too weak: ignored=%d multi=%d", ignoredSeen, ceilingIgnoredCandidates)
	}
}
