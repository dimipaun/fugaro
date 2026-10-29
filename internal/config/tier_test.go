package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/tier"
)

// TestGovernanceTiersAreTheVocabulary walks GovernanceTiers by reflection:
// every field of the block is one tier of tier.Tiers, and every tier of the
// vocabulary has a field. The block is spelled out field by field rather than
// held in a map so an unknown tier name is an unknown key the strict decoder
// refuses (TestGovernanceStrictDecode), which leaves this test as the thing
// that keeps the two lists equal: a tier added to the vocabulary and not to
// the block would otherwise be a name the launcher may write and no file may
// set.
//
// The three keys of a tier are the merge's own policy keys (TierKey builds
// "tiers.<tier>.<key>" out of them), so a key renamed in one place and not the
// other would be a source line naming a key no file has.
func TestGovernanceTiersAreTheVocabulary(t *testing.T) {
	typ := reflect.TypeOf(GovernanceTiers{})
	want := make(map[string]bool, len(tier.Tiers))
	for _, tt := range tier.Tiers {
		want[string(tt)] = true
	}
	if typ.NumField() != len(tier.Tiers) {
		t.Errorf("GovernanceTiers has %d fields, want one per tier (%d)", typ.NumField(), len(tier.Tiers))
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if !want[tag] {
			t.Errorf("GovernanceTiers.%s is tagged %q, which is no tier of tier.Tiers (%s)", f.Name, tag, tier.List())
			continue
		}
		delete(want, tag)
		if f.Type != reflect.TypeOf(TierLimits{}) {
			t.Errorf("GovernanceTiers.%s is %s, want a TierLimits", f.Name, f.Type)
		}
	}
	for name := range want {
		t.Errorf("tier %q has no field in GovernanceTiers", name)
	}

	limits := reflect.TypeOf(TierLimits{})
	wantKeys := []string{policy.KeyExpectedUSD, policy.KeyExpectedMinutes, policy.KeyMaxSubagents}
	if limits.NumField() != len(wantKeys) {
		t.Errorf("TierLimits has %d fields, want %d", limits.NumField(), len(wantKeys))
	}
	got := make([]string, 0, limits.NumField())
	for i := 0; i < limits.NumField(); i++ {
		tag, _, _ := strings.Cut(limits.Field(i).Tag.Get("yaml"), ",")
		got = append(got, tag)
	}
	seen := make(map[string]bool, len(got))
	for _, key := range got {
		seen[key] = true
	}
	for _, key := range wantKeys {
		if !seen[key] {
			t.Errorf("TierLimits has no %q field, so TierKey cannot spell it", key)
		}
	}
}

// TestGovernanceTiersValidated: a tier's figures are refused where no run
// could mean them — a negative cost, duration or fan-out, and a fan-out past
// the same MaxGovernanceSubagents the untiered key carries — and never for
// being larger than the per-run cap. An expectation above the cap is exactly
// what G6 says a tier may say: the run still stops at the cap, so refusing it
// here would be reading an expectation as a ceiling. The message names the
// tier's own key, so an owner who wrote the figure can find it.
func TestGovernanceTiersValidated(t *testing.T) {
	for _, c := range []struct {
		name string
		yaml string
		key  string
	}{
		{"a negative expected cost", "governance: { tiers: { small: { expected_usd: -1 } } }\n", "governance.tiers.small.expected_usd"},
		{"a negative expected duration", "governance: { tiers: { large: { expected_minutes: -1 } } }\n", "governance.tiers.large.expected_minutes"},
		{"a negative tier fan-out", "governance: { tiers: { trivial: { max_subagents: -1 } } }\n", "governance.tiers.trivial.max_subagents"},
		{"a fan-out above the untiered bound", "governance: { tiers: { medium: { max_subagents: 101 } } }\n", "governance.tiers.medium.max_subagents"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, ps := Parse([]byte(minimalYAML + c.yaml)); len(ps) == 0 {
				t.Fatalf("%s was accepted", c.name)
			} else if !strings.Contains(ps[0].Path, c.key) && !strings.Contains(ps[0].String(), c.key) {
				t.Errorf("problem %q does not name %s", ps[0], c.key)
			}
		})
	}
	for _, c := range []struct {
		name string
		yaml string
	}{
		{"the shipped figures", "governance: { tiers: { trivial: { expected_usd: 0.25, expected_minutes: 10, max_subagents: 3 }, small: { expected_usd: 1, expected_minutes: 30, max_subagents: 3 }, medium: { expected_usd: 3, expected_minutes: 90, max_subagents: 5 }, large: { expected_usd: 8, expected_minutes: 240, max_subagents: 5 } } }\n"},
		{"an empty tiers block", "governance: { tiers: {} }\n"},
		{"the bound itself", "governance: { tiers: { large: { max_subagents: 100 } } }\n"},
		{"zero is unset", "governance: { tiers: { small: { expected_usd: 0, expected_minutes: 0, max_subagents: 0 } } }\n"},
		{"an expectation above any run's cap", "governance: { tiers: { large: { expected_usd: 5000 } } }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, ps := Parse([]byte(minimalYAML + c.yaml)); len(ps) > 0 {
				t.Errorf("%s was refused: %v", c.name, ps)
			}
		})
	}

	// A tier's fan-out is bounded by the untiered key's own bound, so the two
	// agree on 100 without either spelling it twice.
	if MaxGovernanceSubagents != 100 {
		t.Errorf("MaxGovernanceSubagents = %d, and the tier bound is its own: the value changed", MaxGovernanceSubagents)
	}
}
