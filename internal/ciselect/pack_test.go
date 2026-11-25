package ciselect

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestPackIsLongestFirst(t *testing.T) {
	// a takes 10s over two of its tests, b 6s and c 5s. Greedy takes a into
	// the first leg, b into the second, and c into b's leg, the least loaded
	// of the two at 6s against a's 10s, so the leg filled second is the
	// heavier one: only a pack that returns its legs longest first puts it
	// first.
	timings := strings.Join([]string{
		"6.5\tgithub.com/dimipaun/fugaro/a\tTestA1",
		"3.5\tgithub.com/dimipaun/fugaro/a\tTestA2",
		"6.000\tgithub.com/dimipaun/fugaro/b\tTestB",
		"5\tgithub.com/dimipaun/fugaro/c\tTestC",
	}, "\n")
	legs := Pack([]string{"./a", "./b", "./c"}, timings, 2)
	want := []Leg{
		{Name: "pack-1", Pkg: "./b ./c", Index: 0, Of: 1},
		{Name: "pack-0", Pkg: "./a", Index: 0, Of: 1},
	}
	if !slices.Equal(legs, want) {
		t.Fatalf("Pack = %+v, want %+v", legs, want)
	}
}

func TestPackKeepsTheCliFamiliesWhole(t *testing.T) {
	// The budget says nothing about a family: internal/cli is emitted as the
	// legs ci.yml splits it into whatever maxLegs is, and never packed into a
	// leg with another package, because its update tests hang when a subset
	// of the package runs without the tests that precede them.
	legs := Pack([]string{packCliPkg, "./internal/runner"}, "", 1)
	var update, other []Leg
	for _, leg := range legs {
		if leg.Only != "" && leg.Pkg != packCliPkg {
			t.Fatalf("leg %+v takes a family of %q", leg, leg.Pkg)
		}
		if strings.Contains(leg.Pkg, packCliPkg) && leg.Pkg != packCliPkg {
			t.Fatalf("leg %+v packs internal/cli with another package", leg)
		}
		switch leg.Only {
		case "update":
			update = append(update, leg)
		case "other":
			other = append(other, leg)
		}
	}
	wantUpdate := Leg{Name: "cli-update", Pkg: packCliPkg, Index: 0, Of: 1, Only: "update"}
	if len(update) != 1 || update[0] != wantUpdate {
		t.Fatalf("update family = %+v, want %+v", update, wantUpdate)
	}
	if len(other) != packCliOtherLegs {
		t.Fatalf("other family has %d legs, want %d", len(other), packCliOtherLegs)
	}
	for i, leg := range other {
		want := Leg{Name: "cli-" + strconv.Itoa(i), Pkg: packCliPkg, Index: i, Of: packCliOtherLegs, Only: "other"}
		if leg != want {
			t.Fatalf("other leg %d = %+v, want %+v", i, leg, want)
		}
	}
}

func TestPackSplitsScriptsIntoLiveAndNonlive(t *testing.T) {
	// scripts holds the live checks, whose items the test builds itself and
	// no -run pattern can choose between, so their legs carry the
	// LIVE_CHECK_SHARD index and count, and the legs beside them run
	// everything else in the package.
	legs := sortLegsByName(Pack([]string{packScriptsPkg}, "", 4))
	want := []Leg{
		{Name: "scripts-0", Pkg: packScriptsPkg, Index: 0, Of: packScriptsNonliveLegs, Only: "nonlive"},
		{Name: "scripts-1", Pkg: packScriptsPkg, Index: 1, Of: packScriptsNonliveLegs, Only: "nonlive"},
		{Name: "scripts-live-0", Pkg: packScriptsPkg, Index: 0, Of: packScriptsLiveLegs, Only: "live"},
		{Name: "scripts-live-1", Pkg: packScriptsPkg, Index: 1, Of: packScriptsLiveLegs, Only: "live"},
		{Name: "scripts-live-2", Pkg: packScriptsPkg, Index: 2, Of: packScriptsLiveLegs, Only: "live"},
		{Name: "scripts-live-3", Pkg: packScriptsPkg, Index: 3, Of: packScriptsLiveLegs, Only: "live"},
		{Name: "scripts-live-4", Pkg: packScriptsPkg, Index: 4, Of: packScriptsLiveLegs, Only: "live"},
		{Name: "scripts-live-5", Pkg: packScriptsPkg, Index: 5, Of: packScriptsLiveLegs, Only: "live"},
	}
	if !slices.Equal(legs, want) {
		t.Fatalf("Pack = %+v, want %+v", legs, want)
	}
}

func TestPackWithoutTimingsIsEqualWeight(t *testing.T) {
	// Twice "./x/one": the selector unions two sources and must not emit two
	// legs of one name.
	chosen := []string{"./x/one", "./x/two", "./x/three", "./x/one", "./x/four"}
	legs := Pack(chosen, "", 2)
	want := []Leg{
		{Name: "pack-0", Pkg: "./x/four ./x/three", Index: 0, Of: 1},
		{Name: "pack-1", Pkg: "./x/one ./x/two", Index: 0, Of: 1},
	}
	if !slices.Equal(legs, want) {
		t.Fatalf("Pack = %+v, want %+v (two legs of two packages each)", legs, want)
	}
	// Text that names no package of the TSV's shape weighs them the same too:
	// an unreadable artifact costs balance, never tests.
	if got := Pack(chosen, "### shard cli-0\npackage elapsed (s):\n", 2); !slices.Equal(got, want) {
		t.Fatalf("Pack with unusable timings = %+v, want %+v", got, want)
	}
}

func TestPackWithoutABudgetIsOneLegPerPackage(t *testing.T) {
	// A caller that named no budget is read as one that wants no packing: a
	// leg per package, which runs every package and merges none.
	legs := Pack([]string{"./x/one", "./x/two"}, "", 0)
	want := []Leg{
		{Name: "pack-0", Pkg: "./x/one", Index: 0, Of: 1},
		{Name: "pack-1", Pkg: "./x/two", Index: 0, Of: 1},
	}
	if !slices.Equal(legs, want) {
		t.Fatalf("Pack = %+v, want %+v", legs, want)
	}
}

func TestPackWithoutPackagesIsAnEmptyList(t *testing.T) {
	// The caller prints this as JSON, where a nil slice is null and a run
	// whose legs are empty is an empty matrix.
	legs := Pack(nil, "1\tgithub.com/dimipaun/fugaro/a\tTestA", 4)
	if legs == nil {
		t.Fatal("Pack returned a nil slice")
	}
	data, err := json.Marshal(legs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("marshalled legs = %s, want []", data)
	}
}

func TestLegFieldNamesArePinned(t *testing.T) {
	// The keys of ci.yml's matrix. CS6 writes the text that reads them and
	// CS6b's TestCISelectorLegKeysAreTheMatrixKeys compares it with this
	// list, so a rename here is a change to the workflow in the same sweep.
	want := []string{"name", "pkg", "index", "of", "only"}
	typ := reflect.TypeOf(Leg{})
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" {
			t.Fatalf("field %s carries no json name", field.Name)
		}
		got = append(got, name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Leg carries %v, want %v", got, want)
	}
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if keys := legJSONKeys(t, Leg{Name: "cli-0", Pkg: packCliPkg, Index: 0, Of: 8, Only: "other"}); !slices.Equal(keys, sorted) {
		t.Fatalf("a full leg marshals the keys %v, want %v", keys, sorted)
	}
	// A leg that takes its packages whole carries no family at all, which is
	// what a whole-package leg of ci.yml looks like today.
	if keys := legJSONKeys(t, Leg{Name: "pack-0", Pkg: "./a", Index: 0, Of: 1}); slices.Contains(keys, "only") {
		t.Fatalf("a leg without a family marshals %v", keys)
	}
}

func legJSONKeys(t *testing.T, leg Leg) []string {
	t.Helper()
	data, err := json.Marshal(leg)
	if err != nil {
		t.Fatalf("marshal %+v: %v", leg, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortLegsByName(legs []Leg) []Leg {
	out := append([]Leg(nil), legs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func TestDropGatedDropsWhatTheLegsCannotName(t *testing.T) {
	gated := gatedPackages([]string{"./a", "./gated"}, map[string]bool{"./a": true})
	got := dropGated([]string{"./a", "./gated", "./unknown"}, gated)
	if want := []string{"./a", "./unknown"}; !slices.Equal(got, want) {
		t.Errorf("dropGated = %q, want %q", got, want)
	}
	if got := dropGated([]string{"./gated"}, gated); got == nil || len(got) != 0 {
		t.Errorf("dropGated of only a gated package = %#v, want an empty slice", got)
	}
}
