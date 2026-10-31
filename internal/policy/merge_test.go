package policy

import (
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
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

// TestReviewFloorMergesTightenOnly is D722's G1: the owner's review-level
// floor is a union over the layers, so a layer below it can only ever add a
// level to a rigor mode. The layers under the floor are the ones a launcher
// writes — the project layer and the task (docs/design/bucket-iam.md §10 L1,
// L2) — and the whole point of the floor is that neither can take a level
// away: a merge that let a lower layer's shorter list replace the owner's
// would be the launcher choosing how much review it gets.
func TestReviewFloorMergesTightenOnly(t *testing.T) {
	owner := Layer{ReviewFloor: map[rigor.Mode][]Level{
		rigor.Pro:      {LevelL2},
		rigor.Critical: {LevelL2, LevelL3},
	}}
	// A layer that asks for more review than the owner requires, and one that
	// names no floor at all — the shape every launcher-written layer has.
	more := Layer{ReviewFloor: map[rigor.Mode][]Level{rigor.Pro: {LevelL1, LevelL3}}}
	none := Layer{}
	e := Merge(owner, more, none)

	if got, want := e.ReviewFloor(rigor.Pro), []Level{LevelL1, LevelL2, LevelL3}; !slices.Equal(got, want) {
		t.Errorf("ReviewFloor(pro) = %v, want %v: a lower layer may add a level, never remove one", got, want)
	}
	if got, want := e.ReviewFloor(rigor.Critical), []Level{LevelL2, LevelL3}; !slices.Equal(got, want) {
		t.Errorf("ReviewFloor(critical) = %v, want %v: a mode no later layer names keeps the owner's floor", got, want)
	}
	if got := e.ReviewFloor(rigor.Startup); got != nil {
		t.Errorf("ReviewFloor(startup) = %v, want nil: no layer set a floor for that mode", got)
	}
	// A layer that repeats a level the owner already required adds nothing.
	e = Merge(owner, Layer{ReviewFloor: map[rigor.Mode][]Level{rigor.Critical: {LevelL3, LevelL3}}})
	if got, want := e.ReviewFloor(rigor.Critical), []Level{LevelL2, LevelL3}; !slices.Equal(got, want) {
		t.Errorf("ReviewFloor(critical) = %v, want %v: the union holds a level once", got, want)
	}
	// The layers' own maps are not modified by the merge.
	if got := owner.ReviewFloor[rigor.Pro]; !slices.Equal(got, []Level{LevelL2}) {
		t.Errorf("the owner's layer was modified: ReviewFloor(pro) = %v", got)
	}
	if got := more.ReviewFloor[rigor.Pro]; !slices.Equal(got, []Level{LevelL1, LevelL3}) {
		t.Errorf("a later layer was modified: ReviewFloor(pro) = %v", got)
	}
}

// TestParseFloor is the one rule a written floor is held to, shared by the
// operator's config file and the policy node (internal/localcfg and
// internal/projectpolicy call it): the two cannot disagree about what a
// written floor means, which is why the rule lives beside the vocabulary and
// not in either caller. Every name outside the vocabularies is refused rather
// than dropped, and every refusal of one document is reported at once.
func TestParseFloor(t *testing.T) {
	got, err := ParseFloor(map[string][]string{"critical": {"l2", "l3"}, "pro": {"l2"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[rigor.Mode][]Level{rigor.Pro: {LevelL2}, rigor.Critical: {LevelL2, LevelL3}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseFloor = %v, want %v", got, want)
	}

	// A mode and a level this Fugaro does not know, in one document: both are
	// named, because an operator fixing a file should not have to publish once
	// per mistake.
	_, err = ParseFloor(map[string][]string{"prod": {"l2"}, "pro": {"l4"}})
	if err == nil {
		t.Fatal("ParseFloor accepted a mode and a level outside the vocabulary")
	}
	for _, want := range []string{"prod", "is not a rigor mode", "l4", "is not a review level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// No mode at all is no floor, whether the key is absent or written empty:
	// the empty object is how an operator says a published floor goes away.
	for _, written := range []map[string][]string{nil, {}} {
		f, err := ParseFloor(written)
		if err != nil || f != nil {
			t.Errorf("ParseFloor(%v) = (%v, %v), want (nil, nil)", written, f, err)
		}
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
	if r.Intn(3) > 0 {
		l.MaxOutputCoder = int64(r.Intn(8) - 2)
	}
	if r.Intn(3) > 0 {
		l.MaxOutputReviewer = int64(r.Intn(8) - 2)
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
		var minTok, minOutC, minOutR int64
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
			if l.MaxOutputCoder > 0 && (minOutC == 0 || l.MaxOutputCoder < minOutC) {
				minOutC = l.MaxOutputCoder
			}
			if l.MaxOutputReviewer > 0 && (minOutR == 0 || l.MaxOutputReviewer < minOutR) {
				minOutR = l.MaxOutputReviewer
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
		if e.PerRunUSD != minCap || e.MaxRunTokens != minTok || e.MaxOutputCoder != minOutC || e.MaxOutputReviewer != minOutR || modeRank(e.Mode) != rank {
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
		var runOutC, runOutR int64
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
			if l.MaxOutputCoder > 0 {
				if runOutC == 0 || l.MaxOutputCoder < runOutC {
					runOutC = l.MaxOutputCoder
				} else if l.MaxOutputCoder > runOutC {
					want = append(want, Ignored{Key: KeyMaxOutputCoder})
				}
			}
			if l.MaxOutputReviewer > 0 {
				if runOutR == 0 || l.MaxOutputReviewer < runOutR {
					runOutR = l.MaxOutputReviewer
				} else if l.MaxOutputReviewer > runOutR {
					want = append(want, Ignored{Key: KeyMaxOutputReviewer})
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

func TestMergeMaxOutputMin(t *testing.T) {
	// The ceiling has none; the default branch sets coder 4096; the branch tries 8192 and 0.
	e := Merge(Layer{}, Layer{MaxOutputCoder: 4096, MaxOutputReviewer: 100},
		Layer{MaxOutputCoder: 8192, MaxOutputReviewer: 50})
	if e.MaxOutputCoder != 4096 || e.MaxOutputReviewer != 50 ||
		e.Sources[KeyMaxOutputCoder] != "default-branch" || e.Sources[KeyMaxOutputReviewer] != "branch" {
		t.Fatalf("%+v", e)
	}
	if want := []Ignored{{KeyMaxOutputCoder, "8192", "4096", "default-branch"}}; !reflect.DeepEqual(e.Ignored, want) {
		t.Fatalf("got %+v", e.Ignored)
	}
	// 0 is no value: it can't lift a limit.
	if e := Merge(Layer{}, Layer{MaxOutputCoder: 4096}, Layer{MaxOutputCoder: 0}); e.MaxOutputCoder != 4096 || len(e.Ignored) != 0 {
		t.Fatalf("%+v", e)
	}
}

// The runner tells who asked for each ignored value by counting: the
// default branch's ignored entries are exactly the first ones of the
// three-layer merge. This pins that prefix property against changes to Merge.
func TestIgnoredDefaultBranchEntriesArePrefix(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	nonEmpty := 0
	for i := 0; i < 20000; i++ {
		c, d, b := randLayer(r), randLayer(r), randLayer(r)
		two := Merge(c, d).Ignored
		three := Merge(c, d, b).Ignored
		if len(three) < len(two) || (len(two) > 0 && !reflect.DeepEqual(three[:len(two)], two)) {
			t.Fatalf("ceiling %+v default %+v branch %+v\ntwo layers:   %+v\nthree layers: %+v", c, d, b, two, three)
		}
		if len(two) > 0 {
			nonEmpty++
		}
	}
	if nonEmpty < 1000 {
		t.Fatalf("only %d cases had ignored entries: the generator is too tame", nonEmpty)
	}
}

// TestPerDayMergeNeverLoosens checks the per-day cap against an oracle over
// random layers: the smallest valid cap wins, a later larger one is ignored
// and recorded, and no other key's layers change it.
func TestPerDayMergeNeverLoosens(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	ignoredSeen := 0
	for i := 0; i < 20000; i++ {
		n := 1 + r.Intn(5)
		ls := make([]Layer, n)
		for j := range ls {
			ls[j] = randLayer(r)
			ls[j].PerDayUSD = randFloat(r)
		}
		e := Merge(ls[0], ls[1:]...)
		var min, running float64
		var want []int // layer index of each ignored value
		for j, l := range ls {
			if !validCap(l.PerDayUSD) {
				continue
			}
			if min == 0 || l.PerDayUSD < min {
				min = l.PerDayUSD
			}
			if running == 0 || l.PerDayUSD < running {
				running = l.PerDayUSD
			} else if l.PerDayUSD > running {
				want = append(want, j)
			}
		}
		if e.PerDayUSD != min || (min > 0) != (e.Sources[KeyPerDayUSD] != "") {
			t.Fatalf("not exact: %+v -> %+v want %v", ls, e, min)
		}
		got := 0
		for _, ig := range e.Ignored {
			if ig.Key == KeyPerDayUSD {
				if ig.Source == "" || ig.Value == ig.Effective {
					t.Fatalf("bad ignored record %+v", ig)
				}
				got++
			}
		}
		if got != len(want) {
			t.Fatalf("ignored %d, want %d: %+v -> %+v", got, len(want), ls, e.Ignored)
		}
		ignoredSeen += got
	}
	if ignoredSeen < 1000 {
		t.Fatalf("generator too weak: %d", ignoredSeen)
	}
	// A branch cannot lift the default branch's cap, and 0 is no value.
	e := Merge(Layer{}, Layer{PerDayUSD: 20}, Layer{PerDayUSD: 500}, Layer{PerDayUSD: 0})
	if e.PerDayUSD != 20 || e.Sources[KeyPerDayUSD] != SourceDefaultBranch ||
		!reflect.DeepEqual(e.Ignored, []Ignored{{KeyPerDayUSD, "500", "20", SourceDefaultBranch}}) {
		t.Fatalf("%+v", e)
	}
}

// TestABMergeTightenOnly pins D556/D557's rule for the one ab: key the merge
// speaks: the project layer sets the sampling rate and a repository may only
// lower it, to 0, which never samples that repository. A layer that writes a
// higher rate is dropped and recorded, as a looser cap is; a layer that
// writes no rate at all passes the earlier layers through, which is a nil
// rate and not a 0 one, because 0 is the opt-out and not "unset". Nothing
// else of ab is a policy.Layer field, so no other key of the block can reach
// the merge from any layer.
func TestABMergeTightenOnly(t *testing.T) {
	rate := func(v float64) *float64 { return &v }
	if e := Merge(Layer{}); e.ABRate != nil || e.Sources[KeyABRate] != "" {
		t.Errorf("a layer with no ab rate = %v (source %q), want unset", e.ABRate, e.Sources[KeyABRate])
	}
	e := Merge(Layer{ABRate: rate(0.05)})
	if e.ABRate == nil || *e.ABRate != 0.05 || e.Sources[KeyABRate] != SourceCeiling {
		t.Errorf("the ceiling alone = %v (source %q), want 0.05 from the ceiling", e.ABRate, e.Sources[KeyABRate])
	}
	// The repository's opt-out is the tighter value, and it is the branch's.
	e = Merge(Layer{ABRate: rate(0.05)}, Layer{ABRate: rate(0)})
	if e.ABRate == nil || *e.ABRate != 0 || e.Sources[KeyABRate] != SourceDefaultBranch || len(e.Ignored) != 0 {
		t.Errorf("a repository's 0 = %v (source %q, ignored %v), want 0 from the layer that opted out and nothing ignored", e.ABRate, e.Sources[KeyABRate], e.Ignored)
	}
	// A repository cannot raise it: its layer is the default branch's, the
	// project's is the ceiling.
	e = Merge(Layer{ABRate: rate(0.05)}, Layer{ABRate: rate(0.1)})
	if e.ABRate == nil || *e.ABRate != 0.05 || len(e.Ignored) != 1 {
		t.Fatalf("a repository's 0.1 = %v (ignored %v), want the ceiling's 0.05 and one ignored value", e.ABRate, e.Ignored)
	}
	if ig := e.Ignored[0]; ig.Key != KeyABRate || ig.Value != "0.1" || ig.Effective != "0.05" || ig.Source != SourceCeiling {
		t.Errorf("Ignored = %+v, want ab.rate 0.1 dropped for the ceiling's 0.05", ig)
	}
	// A layer in between that writes no rate is not an opt-out.
	e = Merge(Layer{ABRate: rate(0.05)}, Layer{}, Layer{ABRate: rate(0)})
	if e.ABRate == nil || *e.ABRate != 0 || e.Sources[KeyABRate] != SourceBranch {
		t.Errorf("a silent default branch = %v (source %q), want the branch's 0", e.ABRate, e.Sources[KeyABRate])
	}
	// Once a layer has opted out, a later layer cannot raise it back: the
	// comparison is with the running effective value.
	e = Merge(Layer{ABRate: rate(0.05)}, Layer{ABRate: rate(0)}, Layer{ABRate: rate(0.07)})
	if e.ABRate == nil || *e.ABRate != 0 || len(e.Ignored) != 1 || e.Ignored[0].Source != SourceDefaultBranch {
		t.Errorf("0 then 0.07 = %v (ignored %v), want 0 from the layer that opted out and the 0.07 dropped", e.ABRate, e.Ignored)
	}
	// Equal rates are not ignored, as for every other key.
	if e := Merge(Layer{ABRate: rate(0.05)}, Layer{ABRate: rate(0.05)}); len(e.Ignored) != 0 {
		t.Errorf("two equal rates = %v, want nothing ignored", e.Ignored)
	}
}
