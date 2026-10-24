package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// TestRigorBudgetFractionsArePinned: the per-mode per-run budget defaults are
// a value a repository's spend depends on, so each one is pinned rather than
// read back out of the function that returns it (docs/rigor-modes D16, plan
// T6.1). Critical contributes none and so gets the whole deployed cap, which
// is what a run of it gets today.
func TestRigorBudgetFractionsArePinned(t *testing.T) {
	want := []struct {
		mode     rigor.Mode
		fraction float64
	}{
		{rigor.Hotfix, 0.4},
		{rigor.POC, 0.55},
		{rigor.Startup, 0.7},
		{rigor.Pro, 0.85},
		{rigor.Critical, 1},
		// The zero mode is what a caller that has resolved none carries, and
		// a mode outside the vocabulary ranks below every real one: neither
		// may lower the owner's cap on the way past.
		{rigor.Mode(""), 1},
		{rigor.Mode("Poc"), 1},
	}
	for _, w := range want {
		if got := RigorBudgetFraction(w.mode); got != w.fraction {
			t.Errorf("RigorBudgetFraction(%q) = %v, want %v", w.mode, got, w.fraction)
		}
	}
}

// TestRigorBudgetDefaultsNeverRaiseTheDeployedCap: the deployed cap is the
// ceiling and the mode's default is a fraction of it, so the scaling is a
// clamp in the one direction that matters (F15, D16). Every mode but Critical
// is strictly below the deployed cap — and Critical, which contributes no
// default, is exactly it.
func TestRigorBudgetDefaultsNeverRaiseTheDeployedCap(t *testing.T) {
	for _, usd := range []float64{2, 5, 20, 50, 100000} {
		deployed, err := pricing.FromUSD(usd)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range rigor.Modes {
			got := RigorBudgetCap(m, deployed)
			if got > deployed {
				t.Errorf("RigorBudgetCap(%s, $%v) = %v, above the deployed cap", m, usd, got)
			}
			if m != rigor.Critical && got >= deployed {
				t.Errorf("RigorBudgetCap(%s, $%v) = %v, not below the deployed cap", m, usd, got)
			}
			if got < 1 {
				t.Errorf("RigorBudgetCap(%s, $%v) = %v: a cap that rounds to nothing reads as no cap at all", m, usd, got)
			}
		}
	}
	// No deployed cap at all is no default either: there is no ceiling to
	// take a fraction of, so nothing is invented.
	if got := RigorBudgetCap(rigor.POC, 0); got != 0 {
		t.Errorf("RigorBudgetCap(poc, none) = %v, want no cap", got)
	}
}

// TestRigorBudgetDefaultsAreOrdered: D16's order, Hotfix tightest of all
// because a hotfix is the run with the least room to spend.
func TestRigorBudgetDefaultsAreOrdered(t *testing.T) {
	deployed, err := pricing.FromUSD(50)
	if err != nil {
		t.Fatal(err)
	}
	order := []rigor.Mode{rigor.Hotfix, rigor.POC, rigor.Startup, rigor.Pro, rigor.Critical}
	for i := 1; i < len(order); i++ {
		lo, hi := order[i-1], order[i]
		if RigorBudgetCap(lo, deployed) >= RigorBudgetCap(hi, deployed) {
			t.Errorf("%s (%v) is not below %s (%v)", lo, RigorBudgetCap(lo, deployed), hi, RigorBudgetCap(hi, deployed))
		}
	}
}

// TestRigorBudgetDefaultsFinishTheSmokeCheck: a run that dies on the cap
// produces nothing (D16), so every default is at least the floor a stage
// needs — the gateway's own lease floor for each model stage the smallest run
// the catalog ships can start — at every deployed cap the tree's own examples
// use.
//
// The stage count is written out rather than recomputed: the run it counts is
// the catalog's smallest (plan T6.2's smoke check, which F17 says the cost
// preview quotes through recipe.StageBound), and today that run starts one
// model stage. A catalog that grows a smaller or a larger smallest run has to
// be read here, which is the point — a count worked out the same way the
// production one is would move with it and assert nothing.
func TestRigorBudgetDefaultsFinishTheSmokeCheck(t *testing.T) {
	const smallestRunStages = 1
	if got, want := RigorBudgetFloorUSD, RigorBudgetStageFloorUSD*smallestRunStages; got != want {
		t.Fatalf("RigorBudgetFloorUSD = %v; the smallest run the catalog ships starts %d stage(s), so the floor is %v",
			got, smallestRunStages, want)
	}
	if RigorBudgetFloorUSD < RigorBudgetStageFloorUSD {
		t.Fatalf("RigorBudgetFloorUSD = %v, under one stage's floor", RigorBudgetFloorUSD)
	}
	for _, usd := range []float64{2, 5, 20, 50} {
		deployed, err := pricing.FromUSD(usd)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range rigor.Modes {
			if got := RigorBudgetCap(m, deployed).USD(); got < RigorBudgetFloorUSD {
				t.Errorf("RigorBudgetCap(%s, $%v) = $%v, under the floor $%v a stage needs: a run at this cap can be granted nothing", m, usd, got, RigorBudgetFloorUSD)
			}
		}
	}
}

// TestRigorBudgetStageFloorIsTheLeaseFloor: the per-stage floor is the budget
// gateway's own smallest lease, and the two must not drift apart — below the
// lease floor the gateway grants a stage nothing, whatever the cap says. The
// value is read out of the source rather than restated, the way this tree
// pins the facts it states in two places.
func TestRigorBudgetStageFloorIsTheLeaseFloor(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "budget", "lease.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*minLease Micros = ([0-9_]+)$`).FindSubmatch(data)
	if m == nil {
		t.Fatal("internal/budget/lease.go no longer declares minLease Micros: the per-mode budget floor's per-stage term has no source")
	}
	lease, err := strconv.ParseInt(strings.ReplaceAll(string(m[1]), "_", ""), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if got := pricing.Micros(lease).USD(); got != RigorBudgetStageFloorUSD {
		t.Errorf("RigorBudgetStageFloorUSD = %v, but the budget gateway's smallest lease is $%v", RigorBudgetStageFloorUSD, got)
	}
}
