package cli

import (
	"fmt"
	"testing"
)

func TestZZTmpProbe3(t *testing.T) {
	keys := []string{
		"layer.ab", "layer.ab.rate", "layer.claude_models", "layer.claude_models.claude-opus-5-5",
		"layer.profiles", "layer.profiles.web", "layer.profiles.web.rigor.mode", "layer.profiles.web.rebuild.check",
		"layer.defaults", "layer.defaults.agent", "layer.defaults.agent.max_budget_usd", "layer.defaults.rigor.areas",
		"budget.mode", "budget.per_day_usd",
	}
	for _, k := range keys {
		_, werr := settingsWriteRow(k)
		_, eerr := settingsEntryRow(k)
		pat, _, isBlock := settingsEntryBlockPattern(k)
		fmt.Printf("%-45s write=%-8v entry=%-8v block=%v(%s) allow=%v\n", k, werr == nil, eerr == nil, isBlock, pat, settingsGenericKey(k))
		if werr != nil {
			fmt.Printf("    write: %v\n", werr)
		}
		if eerr != nil {
			fmt.Printf("    entry: %v\n", eerr)
		}
	}
}
