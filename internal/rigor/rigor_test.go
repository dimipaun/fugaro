package rigor

import (
	"strings"
	"testing"
)

// TestParseIsClosed pins the vocabulary as closed: every level name parses,
// and a near miss does not. The near misses are the ones a person writes —
// a capital, the spec's own spelling, trailing space, an empty value — and
// each of them is refused at load rather than matched later as an unknown
// level.
func TestParseIsClosed(t *testing.T) {
	for _, m := range Rigors {
		got, ok := Parse(string(m))
		if !ok || got != m {
			t.Errorf("Parse(%q) = %q, %v; want %q, true", m, got, ok, m)
		}
	}
	for _, s := range []string{"", "Poc", "POC", "PRO", "critical ", " hotfix", "Critical", "pro ", "fast", "0"} {
		if m, ok := Parse(s); ok {
			t.Errorf("Parse(%q) = %q, true; want refused", s, m)
		}
	}
}

// TestKnownRejectsTheZeroMode pins that the zero Rigor — what a layer that
// sets nothing carries — is not a level: Resolve's Known checks are what keep
// an unset layer from being read as a value.
func TestKnownRejectsTheZeroMode(t *testing.T) {
	if Known("") {
		t.Error(`Known("") is true; the zero Rigor is "no value", not a level`)
	}
	for _, m := range Rigors {
		if !Known(m) {
			t.Errorf("Known(%q) is false", m)
		}
	}
}

// TestRankOrdersTheAxis pins the order the whole package rests on, one case
// per rank: POC < Startup < Pro < Critical ascending, Hotfix below all of
// them because it is off the axis (spec.md: it blocks the least, since only
// a finding that would make the fix wrong or unsafe blocks in it), and the
// zero Rigor as low as Hotfix, which Strictest skips rather than compares.
func TestRankOrdersTheAxis(t *testing.T) {
	want := map[Rigor]int{POC: 1, Startup: 2, Pro: 3, Critical: 4, Hotfix: 0, "": 0, "bogus": 0}
	for m, r := range want {
		if got := m.Rank(); got != r {
			t.Errorf("%q.Rank() = %d; want %d", m, got, r)
		}
	}
	// The axis is Rigors' first four, in ascending rank; Hotfix is the fifth
	// and is off it.
	axis := Rigors[:len(Rigors)-1]
	if axis[len(axis)-1] != Critical || Rigors[len(Rigors)-1] != Hotfix {
		t.Fatalf("Rigors = %v; want the four on-axis levels then hotfix", Rigors)
	}
	for i := 1; i < len(axis); i++ {
		if axis[i-1].Rank() >= axis[i].Rank() {
			t.Errorf("the axis is not in ascending rank at %d: %q ranks %d, %q ranks %d",
				i, axis[i-1], axis[i-1].Rank(), axis[i], axis[i].Rank())
		}
	}
	if Hotfix.Rank() >= POC.Rank() {
		t.Error("Hotfix ranks at or above POC; a Hotfix rule would then beat a POC one, which is the fail-open direction")
	}
	// PerRun is exactly the off-axis level. It is the one exception to the
	// raise-only rule a request is held to (the owner's ruling of 2026-11-13,
	// R7), and a level that gained it without being off the axis would be a
	// request able to lower a run.
	for _, m := range append([]Rigor{""}, Rigors...) {
		if got, want := m.PerRun(), m == Hotfix; got != want {
			t.Errorf("%q.PerRun() = %v; want %v", m, got, want)
		}
	}
}

// TestDefaultIsPro pins D19's answer: the built-in default is Pro, the level
// every repository that configures nothing resolves to, and the value
// `rigor.default: critical` pins a repository back from.
func TestDefaultIsPro(t *testing.T) {
	if Default != Pro {
		t.Fatalf("Default = %q; D19 answers Pro", Default)
	}
}

// TestStrictestPairsEveryRank pins Strictest over every pair of ranks,
// including the pairs a single rank cannot show: a level against itself, a
// level against a weaker one in both argument orders, and the zero Rigor which
// contributes nothing and is never the answer.
func TestStrictestPairsEveryRank(t *testing.T) {
	cases := []struct {
		name string
		in   []Rigor
		want Rigor
	}{
		{"none", nil, ""},
		{"one", []Rigor{Startup}, Startup},
		{"zero alone", []Rigor{""}, ""},
		{"zero ignored below", []Rigor{"", POC}, POC},
		{"zero ignored above", []Rigor{Critical, ""}, Critical},
		{"poc, startup", []Rigor{POC, Startup}, Startup},
		{"startup, poc", []Rigor{Startup, POC}, Startup},
		{"poc, pro", []Rigor{POC, Pro}, Pro},
		{"pro, poc", []Rigor{Pro, POC}, Pro},
		{"poc, critical", []Rigor{POC, Critical}, Critical},
		{"critical, poc", []Rigor{Critical, POC}, Critical},
		{"startup, critical", []Rigor{Startup, Critical}, Critical},
		{"pro, critical", []Rigor{Pro, Critical}, Critical},
		{"critical, pro", []Rigor{Critical, Pro}, Critical},
		{"equal keeps the first", []Rigor{Startup, Startup}, Startup},
		{"all four", []Rigor{POC, Critical, Startup, Pro}, Critical},
		// A tie keeps the first, so the order Strictest is given is what
		// decides equal ranks; both Hotfix and an unparsed name rank 0.
		{"hotfix alone", []Rigor{Hotfix}, Hotfix},
		{"hotfix vs poc", []Rigor{Hotfix, POC}, POC},
		{"poc vs hotfix", []Rigor{POC, Hotfix}, POC},
		{"hotfix vs critical", []Rigor{Hotfix, Critical}, Critical},
		{"unknown never wins", []Rigor{POC, "bogus"}, POC},
		{"unknown alone is itself", []Rigor{"bogus"}, "bogus"},
	}
	for _, c := range cases {
		if got := Strictest(c.in...); got != c.want {
			t.Errorf("%s: Strictest(%v) = %q; want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestStrictestIsCommutative pins the property the caller relies on when it
// reads two layers in whichever order it happens to have them: over any
// permutation of a set, Strictest answers the same level.
func TestStrictestIsCommutative(t *testing.T) {
	set := []Rigor{POC, Critical, Startup, Pro}
	for _, perm := range permutations(set) {
		if got := Strictest(perm...); got != Critical {
			t.Fatalf("Strictest(%v) = %q; want %q", perm, got, Critical)
		}
	}
}

// permutations is every ordering of ms (the set is four long, so the
// recursion is 24 calls and not worth a dependency).
func permutations(ms []Rigor) [][]Rigor {
	if len(ms) <= 1 {
		return [][]Rigor{append([]Rigor(nil), ms...)}
	}
	var out [][]Rigor
	for i := range ms {
		rest := make([]Rigor, 0, len(ms)-1)
		rest = append(rest, ms[:i]...)
		rest = append(rest, ms[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]Rigor{ms[i]}, p...))
		}
	}
	return out
}

// TestModeNamesAreLowercase pins the spelling the configuration and the
// command line share: every level name is its lower-case self, so
// `rigor.default: Pro` is a load error and not a second spelling of `pro`.
func TestModeNamesAreLowercase(t *testing.T) {
	for _, m := range Rigors {
		if s := string(m); s != strings.ToLower(s) || s == "" {
			t.Errorf("level %q is not a lower-case name", s)
		}
	}
}
