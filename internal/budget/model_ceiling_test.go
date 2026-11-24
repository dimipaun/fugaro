package budget

import (
	"strconv"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// TestRepoCapClampedToCeiling pins A3 (spec §5.6): the ceiling of a
// repository's cap is, for daily and per-run separately, the global cap when
// it is set and otherwise the default repository cap; a node above its ceiling
// counts as the ceiling, and a dimension with no ceiling (neither set) leaves
// the node standing.
func TestRepoCapClampedToCeiling(t *testing.T) {
	m := func(v int64) *pricing.Micros { x := pricing.Micros(v); return &x }
	for _, tc := range []struct {
		name     string
		caps     Caps
		daily    Micros
		dailyOK  bool
		perRun   Micros
		perRunOK bool
	}{
		{
			"a node above the default counts as the default",
			Caps{Repo: &RepoCaps{DailyMicros: m(100), PerRunMicros: m(100)}, Defaults: &DefaultCaps{RepoDailyMicros: m(60), RepoPerRunMicros: m(40)}},
			60, true, 40, true,
		},
		{
			"a node above the global counts as the global",
			Caps{Repo: &RepoCaps{DailyMicros: m(100), PerRunMicros: m(100)}, Global: &GlobalCaps{DailyMicros: m(60), PerRunMicros: m(40)}},
			60, true, 40, true,
		},
		{
			"the global outranks the default",
			Caps{Repo: &RepoCaps{DailyMicros: m(100), PerRunMicros: m(100)}, Defaults: &DefaultCaps{RepoDailyMicros: m(80), RepoPerRunMicros: m(80)}, Global: &GlobalCaps{DailyMicros: m(60), PerRunMicros: m(40)}},
			60, true, 40, true,
		},
		{
			"a node under its ceiling stands",
			Caps{Repo: &RepoCaps{DailyMicros: m(40), PerRunMicros: m(30)}, Defaults: &DefaultCaps{RepoDailyMicros: m(60), RepoPerRunMicros: m(40)}},
			40, true, 30, true,
		},
		{
			"no ceiling leaves the node standing",
			Caps{Repo: &RepoCaps{DailyMicros: m(100), PerRunMicros: m(100)}},
			100, true, 100, true,
		},
		{
			"the default is no node and stands, the global bounding it beside",
			Caps{Defaults: &DefaultCaps{RepoDailyMicros: m(100), RepoPerRunMicros: m(100)}, Global: &GlobalCaps{DailyMicros: m(60), PerRunMicros: m(40)}},
			100, true, 100, true,
		},
		{
			"one dimension's ceiling leaves the other",
			Caps{Repo: &RepoCaps{DailyMicros: m(100), PerRunMicros: m(100)}, Global: &GlobalCaps{DailyMicros: m(60)}},
			60, true, 100, true,
		},
		{
			"the global per-run cap bounds the per-run dimension alone",
			Caps{Repo: &RepoCaps{DailyMicros: m(100), PerRunMicros: m(100)}, Defaults: &DefaultCaps{RepoDailyMicros: m(80)}, Global: &GlobalCaps{PerRunMicros: m(40)}},
			80, true, 40, true,
		},
		{
			"a zero ceiling still binds",
			Caps{Repo: &RepoCaps{DailyMicros: m(100), PerRunMicros: m(100)}, Global: &GlobalCaps{DailyMicros: m(0), PerRunMicros: m(0)}},
			0, true, 0, true,
		},
		{
			"a node without the dimension falls back to the default",
			Caps{Repo: &RepoCaps{DailyMicros: m(40)}, Defaults: &DefaultCaps{RepoDailyMicros: m(60), RepoPerRunMicros: m(40)}},
			40, true, 40, true,
		},
		{
			"nothing set anywhere is no cap",
			Caps{}, 0, false, 0, false,
		},
		{
			"a node whose dimension is unset is no cap although the other is set",
			Caps{Repo: &RepoCaps{DailyMicros: m(40)}, Global: &GlobalCaps{DailyMicros: m(150)}},
			40, true, 0, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daily, dailyOK := tc.caps.RepoDaily()
			if daily != tc.daily || dailyOK != tc.dailyOK {
				t.Errorf("RepoDaily = %d %v, want %d %v", daily, dailyOK, tc.daily, tc.dailyOK)
			}
			perRun, perRunOK := tc.caps.RepoPerRun()
			if perRun != tc.perRun || perRunOK != tc.perRunOK {
				t.Errorf("RepoPerRun = %d %v, want %d %v", perRun, perRunOK, tc.perRun, tc.perRunOK)
			}
		})
	}
}

// TestRepoCeilingNamesTheGlobalElseTheDefault pins the ceiling itself, the
// value RC3 refuses a write above and RC4 lowers a node to.
func TestRepoCeilingNamesTheGlobalElseTheDefault(t *testing.T) {
	m := func(v int64) *pricing.Micros { x := pricing.Micros(v); return &x }
	for _, tc := range []struct {
		name   string
		caps   Caps
		daily  *pricing.Micros
		perRun *pricing.Micros
	}{
		{
			"the global cap when it is set",
			Caps{Defaults: &DefaultCaps{RepoDailyMicros: m(80), RepoPerRunMicros: m(80)}, Global: &GlobalCaps{DailyMicros: m(60), PerRunMicros: m(40)}},
			m(60), m(40),
		},
		{
			"the default repository cap otherwise",
			Caps{Defaults: &DefaultCaps{RepoDailyMicros: m(80), RepoPerRunMicros: m(80)}},
			m(80), m(80),
		},
		{
			"each dimension on its own",
			Caps{Defaults: &DefaultCaps{RepoDailyMicros: m(80), RepoPerRunMicros: m(80)}, Global: &GlobalCaps{PerRunMicros: m(40)}},
			m(80), m(40),
		},
		{
			"no ceiling when neither is set",
			Caps{}, nil, nil,
		},
		{
			"a defaults node without the dimension is no ceiling for it",
			Caps{Defaults: &DefaultCaps{RepoDailyMicros: m(80)}},
			m(80), nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ceil := tc.caps.RepoCeiling()
			if !sameMicros(ceil.DailyMicros, tc.daily) {
				t.Errorf("RepoCeiling().DailyMicros = %s, want %s", showMicros(ceil.DailyMicros), showMicros(tc.daily))
			}
			if !sameMicros(ceil.PerRunMicros, tc.perRun) {
				t.Errorf("RepoCeiling().PerRunMicros = %s, want %s", showMicros(ceil.PerRunMicros), showMicros(tc.perRun))
			}
		})
	}
}

func sameMicros(got, want *pricing.Micros) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func showMicros(v *pricing.Micros) string {
	if v == nil {
		return "unset"
	}
	return strconv.FormatInt(int64(*v), 10)
}
