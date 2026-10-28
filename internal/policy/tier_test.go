package policy

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/tier"
)

// tierOf reads one tier's expectation out of a set, so a test can walk the
// vocabulary (tier.Tiers) rather than spell the four fields out four times.
func tierOf(ts TierExpectations, t tier.Tier) TierExpectation {
	switch t {
	case tier.Trivial:
		return ts.Trivial
	case tier.Small:
		return ts.Small
	case tier.Medium:
		return ts.Medium
	case tier.Large:
		return ts.Large
	}
	return TierExpectation{}
}

// TestTierDefaultsFromProjectLayer pins the expectations Fugaro ships (G§3,
// D332) and the two rules that make them a ceiling a run starts under: a layer
// that writes one key of one tier keeps the default for the rest of that tier,
// and the project layer's own figures survive the layers above it.
func TestTierDefaultsFromProjectLayer(t *testing.T) {
	d := DefaultGovernance()
	if d.Tiers != DefaultTierExpectations() {
		t.Errorf("DefaultGovernance().Tiers = %+v, want DefaultTierExpectations() %+v", d.Tiers, DefaultTierExpectations())
	}
	for _, c := range []struct {
		tier      tier.Tier
		usd       float64
		minutes   int64
		subagents int64
	}{
		{tier.Trivial, 0.25, 10, 3},
		{tier.Small, 1, 30, 3},
		{tier.Medium, 3, 90, DefaultMaxSubagents},
		{tier.Large, 8, 240, DefaultMaxSubagents},
	} {
		want := TierExpectation{ExpectedUSD: c.usd, ExpectedMinutes: c.minutes, MaxSubagents: c.subagents}
		if got := tierOf(d.Tiers, c.tier); got != want {
			t.Errorf("%s = %+v, want %+v", c.tier, got, want)
		}
	}

	// The project layer's block is the ceiling, so WithDefaults fills only the
	// keys it left out: one key of one tier, and every other tier whole.
	layer := Governance{Tiers: TierExpectations{Medium: TierExpectation{ExpectedUSD: 2}}}.WithDefaults()
	want := TierExpectation{ExpectedUSD: 2, ExpectedMinutes: 90, MaxSubagents: DefaultMaxSubagents}
	if got := layer.Tiers.Medium; got != want {
		t.Errorf("medium = %+v, want the layer's own cost and the defaults for its other keys (%+v)", got, want)
	}
	for _, c := range []struct {
		tier tier.Tier
		want TierExpectation
	}{
		{tier.Trivial, d.Tiers.Trivial},
		{tier.Small, d.Tiers.Small},
		{tier.Large, d.Tiers.Large},
	} {
		if got := tierOf(layer.Tiers, c.tier); got != c.want {
			t.Errorf("%s = %+v, want the tier the layer never mentioned %+v", c.tier, got, c.want)
		}
	}
	if (Governance{}).WithDefaults().Tiers != d.Tiers {
		t.Errorf("an empty layer's tiers = %+v, want the defaults %+v", (Governance{}).WithDefaults().Tiers, d.Tiers)
	}
}

// TestTierMaxSubagents: the fan-out of a tier (spec §5's tripwire, D332). A
// small change has no business fanning out the way an untiered run may, so
// trivial and small stop at 3 while medium and large keep the untiered default
// of 5. A tier's figure is the tier's alone: it never moves the untiered
// governance.max_subagents, and a repository may only tighten it.
func TestTierMaxSubagents(t *testing.T) {
	d := DefaultGovernance()
	for _, c := range []struct {
		tier tier.Tier
		want int64
	}{
		{tier.Trivial, 3},
		{tier.Small, 3},
		{tier.Medium, DefaultMaxSubagents},
		{tier.Large, DefaultMaxSubagents},
	} {
		if got := tierOf(d.Tiers, c.tier).MaxSubagents; got != c.want {
			t.Errorf("%s max_subagents = %d, want %d", c.tier, got, c.want)
		}
	}

	tiers := DefaultTierExpectations()
	tiers.Trivial.MaxSubagents = 3
	ceiling := Layer{Governance: Governance{MaxSubagents: 8, Tiers: tiers}.WithDefaults()}
	repo := Layer{Governance: Governance{Tiers: TierExpectations{
		Trivial: TierExpectation{MaxSubagents: 10},
		Small:   TierExpectation{MaxSubagents: 2},
	}}}
	e := Merge(ceiling, Layer{}, repo)

	if got := e.Governance.Tiers.Trivial.MaxSubagents; got != 3 {
		t.Errorf("tiers.trivial.max_subagents = %d, want 3: a repository may not fan a small change out wider than the project allows", got)
	}
	if got := e.Governance.Tiers.Small.MaxSubagents; got != 2 {
		t.Errorf("tiers.small.max_subagents = %d, want 2: a repository's tighter figure wins", got)
	}
	want := []Ignored{{Key: TierKey(tier.Trivial, KeyMaxSubagents), Value: "10", Effective: "3", Source: SourceCeiling}}
	if !slices.Equal(e.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", e.Ignored, want)
	}
	for key, src := range map[string]string{
		TierKey(tier.Trivial, KeyMaxSubagents): SourceCeiling,
		TierKey(tier.Small, KeyMaxSubagents):   SourceBranch,
	} {
		if e.Sources[key] != src {
			t.Errorf("Sources[%q] = %q, want %q", key, e.Sources[key], src)
		}
	}
	// The project layer's untiered fan-out is untouched by any tier: they are
	// different keys, and one never becomes the other.
	if e.Governance.MaxSubagents != 8 {
		t.Errorf("max_subagents = %d, want the project layer's 8: a tier's figure is the tier's alone", e.Governance.MaxSubagents)
	}
}

// TestTierNeverRaisesCap is G6 on the policy side: a tier is an expectation,
// never a cap. The deployed per-run cap stays the ceiling whatever a tier
// expects, so an expected cost above it is an expectation the run still stops
// at the cap for, and no key of governance.tiers moves per_run_usd.
func TestTierNeverRaisesCap(t *testing.T) {
	tiers := DefaultTierExpectations()
	tiers.Large.ExpectedUSD = 8
	ceiling := Layer{PerRunUSD: 2, Governance: Governance{Tiers: tiers}}
	e := Merge(ceiling, Layer{}, Layer{})

	if e.PerRunUSD != 2 {
		t.Errorf("per_run_usd = %v, want the deployed cap 2: a tier's expectation is not a cap (G6)", e.PerRunUSD)
	}
	if got := e.Governance.Tiers.Large.ExpectedUSD; got != 8 {
		t.Errorf("tiers.large.expected_usd = %v, want the expectation 8: above the cap it is still an expectation", got)
	}
	if e.Sources[KeyPerRunUSD] != SourceCeiling {
		t.Errorf("Sources[%q] = %q, want %q", KeyPerRunUSD, e.Sources[KeyPerRunUSD], SourceCeiling)
	}

	// A repository may only tighten an expectation, the same as any other key:
	// the ceiling's smaller figure wins and the looser one is recorded. A
	// tiers-only layer moves no cap at all.
	only := Merge(ceiling, Layer{}, Layer{Governance: Governance{Tiers: TierExpectations{
		Large: TierExpectation{ExpectedUSD: 50},
	}}})
	if only.PerRunUSD != 2 {
		t.Errorf("a tiers-only layer moved per_run_usd to %v: a tier never raises a cap (G6)", only.PerRunUSD)
	}
	if got := only.Governance.Tiers.Large.ExpectedUSD; got != 8 {
		t.Errorf("tiers.large.expected_usd = %v, want the ceiling's 8", got)
	}
	want := []Ignored{{Key: TierKey(tier.Large, KeyExpectedUSD), Value: "50", Effective: "8", Source: SourceCeiling}}
	if !slices.Equal(only.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", only.Ignored, want)
	}
}
