package ciselect

import (
	"sort"
	"strconv"
	"strings"
)

// The two packages ci.yml cannot take in one go, with the legs each of their
// families keeps: internal/cli's update tests hang when a subset of the
// package runs without the tests that precede them, so they take a leg of
// their own, and the live checks split by LIVE_CHECK_SHARD, which no -run
// pattern can spell. The packer keeps that splitting rather than re-splitting
// a family across legs (R5), and it never packs one of these packages into a
// leg with another.
const (
	packCliPkg     = "./internal/cli"
	packScriptsPkg = "./scripts"

	packCliUpdateLegs      = 1
	packCliOtherLegs       = 8
	packScriptsNonliveLegs = 2
	packScriptsLiveLegs    = 6
)

// Leg is one entry of the shard matrix a selected run prints. Its names are
// the matrix keys ci.yml carries, so renaming one is a change to the
// workflow's text in the same sweep.
type Leg struct {
	Name string `json:"name"`
	// Pkg is the leg's packages, space-joined: one for the legs ci.yml has
	// today, several where the packer filled a leg with small packages. go
	// test takes them as its arguments.
	Pkg string `json:"pkg"`
	// Index and Of are the leg's slice of a package's tests: a stable hash
	// of the test name, or the LIVE_CHECK_SHARD pair for the live checks.
	// Of is 1 for a leg that takes its packages whole.
	Index int `json:"index"`
	Of    int `json:"of"`
	// Only names the family of tests the leg takes where a package's tests
	// cannot be split by name alone. It is empty for a leg that takes its
	// packages whole.
	Only string `json:"only,omitempty"`
}

// Pack returns the shard matrix of a selected run: the legs to run for the
// packages the selector chose, longest first.
//
// chosen holds package paths in the read-map's form ("./internal/cli").
// internal/cli and scripts become the legs of the families ci.yml splits them
// into, whole and whatever maxLegs says; every other package is packed into at
// most maxLegs legs, longest first greedy -- each package to the least loaded
// leg -- so a heavy package stands alone and light ones share a leg. A maxLegs
// below 1 packs nothing, one leg per package, which is the safe reading of a
// caller that named no budget.
//
// timings is the text of the last full run's test-times artifacts
// concatenated, a line per test of "seconds\tpackage\ttest" as ci.yml's
// timings step writes it. A package the timings do not name weighs one second,
// so an empty text weighs every package the same (R5): a missing or unreadable
// artifact costs balance, never tests.
func Pack(chosen []string, timings string, maxLegs int) []Leg {
	// Never nil: the caller prints these as JSON, where a nil slice is null
	// and a run with no legs is an empty matrix.
	legs := []Leg{}
	weights := parseTestTimes(timings)
	packable := make([]string, 0, len(chosen))
	for _, pkg := range packChosenPackages(chosen) {
		switch pkg {
		case packCliPkg:
			legs = append(legs, packCliLegs()...)
		case packScriptsPkg:
			legs = append(legs, packScriptsLegs()...)
		default:
			packable = append(packable, pkg)
		}
	}
	legs = append(legs, packLegs(packable, weights, maxLegs)...)
	sort.SliceStable(legs, func(i, j int) bool {
		return packLegWeight(legs[i], weights) > packLegWeight(legs[j], weights)
	})
	return legs
}

func packCliLegs() []Leg {
	legs := make([]Leg, 0, packCliUpdateLegs+packCliOtherLegs)
	for i := 0; i < packCliUpdateLegs; i++ {
		legs = append(legs, Leg{Name: "cli-update", Pkg: packCliPkg, Index: i, Of: packCliUpdateLegs, Only: "update"})
	}
	for i := 0; i < packCliOtherLegs; i++ {
		legs = append(legs, Leg{Name: "cli-" + strconv.Itoa(i), Pkg: packCliPkg, Index: i, Of: packCliOtherLegs, Only: "other"})
	}
	return legs
}

func packScriptsLegs() []Leg {
	legs := make([]Leg, 0, packScriptsNonliveLegs+packScriptsLiveLegs)
	for i := 0; i < packScriptsNonliveLegs; i++ {
		legs = append(legs, Leg{Name: "scripts-" + strconv.Itoa(i), Pkg: packScriptsPkg, Index: i, Of: packScriptsNonliveLegs, Only: "nonlive"})
	}
	for i := 0; i < packScriptsLiveLegs; i++ {
		legs = append(legs, Leg{Name: "scripts-live-" + strconv.Itoa(i), Pkg: packScriptsPkg, Index: i, Of: packScriptsLiveLegs, Only: "live"})
	}
	return legs
}

// packLegs fills the legs of the packages that have no family of their own.
func packLegs(pkgs []string, weights testTimes, maxLegs int) []Leg {
	if len(pkgs) == 0 {
		return nil
	}
	order := append([]string(nil), pkgs...)
	// Heaviest first is what makes the greedy good; the path breaks a tie so
	// that one set of packages always packs the same way.
	sort.Slice(order, func(i, j int) bool {
		if a, b := weights.weight(order[i]), weights.weight(order[j]); a != b {
			return a > b
		}
		return order[i] < order[j]
	})
	count := len(order)
	if maxLegs > 0 && maxLegs < count {
		count = maxLegs
	}
	load := make([]float64, count)
	groups := make([][]string, count)
	for _, pkg := range order {
		least := 0
		for i := 1; i < count; i++ {
			if load[i] < load[least] {
				least = i
			}
		}
		groups[least] = append(groups[least], pkg)
		load[least] += weights.weight(pkg)
	}
	legs := make([]Leg, 0, count)
	for i, group := range groups {
		// The name is the leg's artifact (test-times-<name>) and its matrix
		// entry, and the family names above are the only others in play.
		legs = append(legs, Leg{Name: "pack-" + strconv.Itoa(i), Pkg: strings.Join(group, " "), Index: 0, Of: 1})
	}
	return legs
}

// packLegWeight is the leg's share of the run so far as the matrix order goes:
// its packages' seconds, split evenly where the leg takes a fraction of them.
func packLegWeight(leg Leg, weights testTimes) float64 {
	var total float64
	for _, pkg := range strings.Fields(leg.Pkg) {
		total += weights.weight(pkg)
	}
	if leg.Of > 1 {
		total /= float64(leg.Of)
	}
	return total
}

// testTimes is the elapsed seconds per package of the last full run.
type testTimes struct {
	sums map[string]float64
	// sorted, so that the suffixes summed below add up in the same order
	// every time: floating point addition is not associative, and the same
	// run has to pack the same way.
	keys []string
}

// parseTestTimes sums the TSV's lines per package. The artifact holds one line
// per test, three fields wide; a narrower line is the summary's package-level
// line, which is a different file (ci.yml's timings step writes the summary
// with two fields), and summing it twice would weight a package twice.
func parseTestTimes(text string) testTimes {
	t := testTimes{sums: map[string]float64{}}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[1] == "" || fields[2] == "" {
			continue
		}
		secs, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		t.sums[fields[1]] += secs
	}
	t.keys = make([]string, 0, len(t.sums))
	for k := range t.sums {
		t.keys = append(t.keys, k)
	}
	sort.Strings(t.keys)
	return t
}

// weight is the package's seconds in the last full run, or one second where
// the timings do not name it. The artifact is keyed by the import path go test
// prints ("github.com/dimipaun/fugaro/internal/cli"), the selector by the path
// the repository and the read-map use ("./internal/cli"), so a suffix match
// stands in for the module path the packer has no business knowing.
func (t testTimes) weight(pkg string) float64 {
	rel := strings.TrimPrefix(pkg, "./")
	if secs, ok := t.sums[rel]; ok && secs > 0 {
		return secs
	}
	var total float64
	for _, k := range t.keys {
		if strings.HasSuffix(k, "/"+rel) {
			total += t.sums[k]
		}
	}
	if total > 0 {
		return total
	}
	return 1
}

// packChosenPackages sorts and deduplicates what the selector chose: the
// selector unions a graph's packages with the read-map's, so the same path
// arrives more than once, and two legs of one name would be two artifacts of
// one name.
func packChosenPackages(chosen []string) []string {
	seen := make(map[string]bool, len(chosen))
	out := make([]string, 0, len(chosen))
	for _, pkg := range chosen {
		if pkg == "" || seen[pkg] {
			continue
		}
		seen[pkg] = true
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}
