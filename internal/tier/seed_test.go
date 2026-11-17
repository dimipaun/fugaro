package tier

import "testing"

// TestSeedMinutes: the four seeds, in the vocabulary's own order, and the
// refusal for a name outside it. The figures are the ones D2712 and D2724 fix
// (10 / 30 / 90 / 240 minutes), and they are the whole of what a task with no
// run history behind it is estimated at. Governance's own expectations are a
// separate set of numbers that happens to agree today (D2724): core cannot
// read ee/governance, so these are core's own and a change to one does not
// move the other.
func TestSeedMinutes(t *testing.T) {
	want := []struct {
		tier    Tier
		minutes int
	}{
		{Trivial, 10},
		{Small, 30},
		{Medium, 90},
		{Large, 240},
	}
	if len(want) != len(Tiers) {
		t.Fatalf("the test names %d tiers and the vocabulary holds %d", len(want), len(Tiers))
	}
	for i, w := range want {
		if Tiers[i] != w.tier {
			t.Errorf("seed %d is %q and Tiers[%d] is %q: the seeds follow the vocabulary's order", i, w.tier, i, Tiers[i])
		}
		got, ok := SeedMinutes(w.tier)
		if !ok {
			t.Errorf("SeedMinutes(%q) is not a seed", w.tier)
			continue
		}
		if got != w.minutes {
			t.Errorf("SeedMinutes(%q) = %d, want %d", w.tier, got, w.minutes)
		}
	}
	for _, unknown := range []Tier{"", "Trivial", "SMALL", "huge"} {
		if _, ok := SeedMinutes(unknown); ok {
			t.Errorf("SeedMinutes(%q) is a seed: the seeds are the vocabulary's own", unknown)
		}
	}
}
