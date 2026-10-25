package rigor

import (
	"strings"
	"testing"
)

// TestParseIsClosed pins the vocabulary as closed: every mode name parses,
// and a near miss does not. The near misses are the ones a person writes —
// a capital, the spec's own spelling, trailing space, an empty value — and
// each of them is refused at load rather than matched later as an unknown
// mode.
func TestParseIsClosed(t *testing.T) {
	for _, m := range Modes {
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

// TestKnownRejectsTheZeroMode pins that the zero Mode — what a layer that
// sets nothing carries — is not a mode: Resolve's Known checks are what keep
// an unset layer from being read as a value.
func TestKnownRejectsTheZeroMode(t *testing.T) {
	if Known("") {
		t.Error(`Known("") is true; the zero Mode is "no value", not a mode`)
	}
	for _, m := range Modes {
		if !Known(m) {
			t.Errorf("Known(%q) is false", m)
		}
	}
}

// TestRankOrdersTheAxis pins the order the whole package rests on, one case
// per rank: POC < Startup < Pro < Critical ascending, Hotfix below all of
// them because it is off the axis (spec.md: it blocks the least, since only
// a finding that would make the fix wrong or unsafe blocks in it), and the
// zero Mode as low as Hotfix, which Strictest skips rather than compares.
func TestRankOrdersTheAxis(t *testing.T) {
	want := map[Mode]int{POC: 1, Startup: 2, Pro: 3, Critical: 4, Hotfix: 0, "": 0, "bogus": 0}
	for m, r := range want {
		if got := m.Rank(); got != r {
			t.Errorf("%q.Rank() = %d; want %d", m, got, r)
		}
	}
	// The axis is Modes' first four, in ascending rank; Hotfix is the fifth
	// and is off it.
	axis := Modes[:len(Modes)-1]
	if axis[len(axis)-1] != Critical || Modes[len(Modes)-1] != Hotfix {
		t.Fatalf("Modes = %v; want the four on-axis modes then hotfix", Modes)
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
}

// TestDefaultIsPro pins D19's answer: the built-in default is Pro, the mode
// every repository that configures nothing resolves to, and the value
// `rigor.default: critical` pins a repository back from.
func TestDefaultIsPro(t *testing.T) {
	if Default != Pro {
		t.Fatalf("Default = %q; D19 answers Pro", Default)
	}
}

// TestStrictestPairsEveryRank pins Strictest over every pair of ranks,
// including the pairs a single rank cannot show: a mode against itself, a
// mode against a weaker one in both argument orders, and the zero Mode which
// contributes nothing and is never the answer.
func TestStrictestPairsEveryRank(t *testing.T) {
	cases := []struct {
		name string
		in   []Mode
		want Mode
	}{
		{"none", nil, ""},
		{"one", []Mode{Startup}, Startup},
		{"zero alone", []Mode{""}, ""},
		{"zero ignored below", []Mode{"", POC}, POC},
		{"zero ignored above", []Mode{Critical, ""}, Critical},
		{"poc, startup", []Mode{POC, Startup}, Startup},
		{"startup, poc", []Mode{Startup, POC}, Startup},
		{"poc, pro", []Mode{POC, Pro}, Pro},
		{"pro, poc", []Mode{Pro, POC}, Pro},
		{"poc, critical", []Mode{POC, Critical}, Critical},
		{"critical, poc", []Mode{Critical, POC}, Critical},
		{"startup, critical", []Mode{Startup, Critical}, Critical},
		{"pro, critical", []Mode{Pro, Critical}, Critical},
		{"critical, pro", []Mode{Critical, Pro}, Critical},
		{"equal keeps the first", []Mode{Startup, Startup}, Startup},
		{"all four", []Mode{POC, Critical, Startup, Pro}, Critical},
		// A tie keeps the first, so the order Strictest is given is what
		// decides equal ranks; both Hotfix and an unparsed name rank 0.
		{"hotfix alone", []Mode{Hotfix}, Hotfix},
		{"hotfix vs poc", []Mode{Hotfix, POC}, POC},
		{"poc vs hotfix", []Mode{POC, Hotfix}, POC},
		{"hotfix vs critical", []Mode{Hotfix, Critical}, Critical},
		{"unknown never wins", []Mode{POC, "bogus"}, POC},
		{"unknown alone is itself", []Mode{"bogus"}, "bogus"},
	}
	for _, c := range cases {
		if got := Strictest(c.in...); got != c.want {
			t.Errorf("%s: Strictest(%v) = %q; want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestStrictestIsCommutative pins the property the caller relies on when it
// reads two layers in whichever order it happens to have them: over any
// permutation of a set, Strictest answers the same mode.
func TestStrictestIsCommutative(t *testing.T) {
	set := []Mode{POC, Critical, Startup, Pro}
	for _, perm := range permutations(set) {
		if got := Strictest(perm...); got != Critical {
			t.Fatalf("Strictest(%v) = %q; want %q", perm, got, Critical)
		}
	}
}

// permutations is every ordering of ms (the set is four long, so the
// recursion is 24 calls and not worth a dependency).
func permutations(ms []Mode) [][]Mode {
	if len(ms) <= 1 {
		return [][]Mode{append([]Mode(nil), ms...)}
	}
	var out [][]Mode
	for i := range ms {
		rest := make([]Mode, 0, len(ms)-1)
		rest = append(rest, ms[:i]...)
		rest = append(rest, ms[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]Mode{ms[i]}, p...))
		}
	}
	return out
}

// TestModeNamesAreLowercase pins the spelling the configuration and the
// command line share: every mode name is its lower-case self, so
// `rigor.default: Pro` is a load error and not a second spelling of `pro`.
func TestModeNamesAreLowercase(t *testing.T) {
	for _, m := range Modes {
		if s := string(m); s != strings.ToLower(s) || s == "" {
			t.Errorf("mode %q is not a lower-case name", s)
		}
	}
}
