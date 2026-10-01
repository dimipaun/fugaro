package policy

import (
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
	if e.Sources[KeyAllowedModels] != "default-branch" || len(e.Ignored) != 0 {
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
	// a partial-overlap list tightens, so it is applied and not ignored.
	e = Merge(Layer{AllowedModels: []string{"a", "b"}}, Layer{AllowedModels: []string{"b", "z"}})
	if len(e.Ignored) != 0 || !reflect.DeepEqual(e.AllowedModels, []string{"b"}) {
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

func randLayer(r *rand.Rand) Layer {
	var l Layer
	if r.Intn(3) > 0 {
		l.PerRunUSD = float64(r.Intn(6))
	}
	if r.Intn(3) > 0 {
		l.MaxRunTokens = int64(r.Intn(6))
	}
	l.Mode = []string{"", "off", "observe", "enforce"}[r.Intn(4)]
	if r.Intn(3) > 0 {
		l.AllowedModels = []string{}
		for _, m := range []string{"a", "b", "c", "d"} {
			if r.Intn(2) == 0 {
				l.AllowedModels = append(l.AllowedModels, m)
			}
		}
	}
	return l
}

func TestMergeNeverLoosens(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		ls := []Layer{randLayer(r), randLayer(r), randLayer(r)}
		e := Merge(ls[0], ls[1], ls[2])
		set := map[string]bool{}
		for _, l := range ls {
			if l.PerRunUSD > 0 {
				if e.PerRunUSD == 0 || e.PerRunUSD > l.PerRunUSD {
					t.Fatalf("per_run loosened: %+v -> %+v", ls, e)
				}
			}
			if l.MaxRunTokens > 0 && (e.MaxRunTokens == 0 || e.MaxRunTokens > l.MaxRunTokens) {
				t.Fatalf("tokens loosened: %+v -> %+v", ls, e)
			}
			if l.Mode != "" && modeRank(e.Mode) < modeRank(l.Mode) {
				t.Fatalf("mode loosened: %+v -> %+v", ls, e)
			}
			if l.AllowedModels != nil {
				if e.AllowedModels == nil {
					t.Fatalf("allow-list dropped: %+v -> %+v", ls, e)
				}
				for _, m := range e.AllowedModels {
					found := false
					for _, x := range l.AllowedModels {
						found = found || x == m
					}
					if !found {
						t.Fatalf("model %s outside a layer's list: %+v -> %+v", m, ls, e)
					}
				}
			}
			set["x"] = true
		}
		// a key set nowhere stays unset
		if e.PerRunUSD != 0 && ls[0].PerRunUSD == 0 && ls[1].PerRunUSD == 0 && ls[2].PerRunUSD == 0 {
			t.Fatalf("invented cap")
		}
		// the ceiling is never in Ignored
		for _, ig := range e.Ignored {
			if ig.Source == "" {
				t.Fatalf("ignored with no source: %+v", ig)
			}
		}
	}
}
