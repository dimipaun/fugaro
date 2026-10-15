package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// valid is a minimal well-formed file the malformed cases are derived from.
func valid(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"version":    1,
		"checked_at": "2026-10-14",
		"sources":    []string{"https://openrouter.ai/api/v1/models"},
		"models": map[string]any{
			"vendor/model": map[string]any{
				"context_tokens": 1000, "max_output_tokens": 100,
				"endpoints": 2, "zdr_endpoints": 1,
				"all": map[string]any{
					"input_per_m":  map[string]any{"min": "1", "max": "2", "min_of": "a", "max_of": "b"},
					"output_per_m": map[string]any{"min": "3", "max": "4", "min_of": "a", "max_of": "b"},
				},
				"zdr": map[string]any{
					"input_per_m":  map[string]any{"min": "1", "max": "1", "min_of": "a", "max_of": "a"},
					"output_per_m": map[string]any{"min": "3", "max": "3", "min_of": "a", "max_of": "a"},
				},
			},
		},
	}
}

func marshal(t *testing.T, doc any) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestLoadRejectsMalformedFile pins every rule the loader enforces: a file
// that breaks one is an error, never a partially applied table.
func TestLoadRejectsMalformedFile(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T) []byte
		want string
	}{
		{"a version this build does not read", func(t *testing.T) []byte {
			d := valid(t)
			d["version"] = 2
			return marshal(t, d)
		}, "version"},
		{"a checked_at that is not a date", func(t *testing.T) []byte {
			d := valid(t)
			d["checked_at"] = "yesterday"
			return marshal(t, d)
		}, "checked_at"},
		{"no sources", func(t *testing.T) []byte {
			d := valid(t)
			delete(d, "sources")
			return marshal(t, d)
		}, "sources"},
		{"no models", func(t *testing.T) []byte {
			d := valid(t)
			d["models"] = map[string]any{}
			return marshal(t, d)
		}, "models is empty"},
		{"a price that is not a number", func(t *testing.T) []byte {
			return []byte(strings.Replace(string(marshal(t, valid(t))), `"min":"1"`, `"min":"cheap"`, 1))
		}, "is not a number"},
		{"a price of zero", func(t *testing.T) []byte {
			return []byte(strings.Replace(string(marshal(t, valid(t))), `"min":"1"`, `"min":"0"`, 1))
		}, "at most"},
		{"a price over the limit", func(t *testing.T) []byte {
			return []byte(strings.Replace(string(marshal(t, valid(t))), `"max":"2"`, `"max":"100000"`, 1))
		}, "at most"},
		{"a max below its min", func(t *testing.T) []byte {
			return []byte(strings.Replace(string(marshal(t, valid(t))), `"max":"2"`, `"max":"0.5"`, 1))
		}, "below min"},
		{"an extreme that names no endpoint", func(t *testing.T) []byte {
			return []byte(strings.Replace(string(marshal(t, valid(t))), `"min_of":"a"`, `"min_of":""`, 1))
		}, "min_of"},
		{"an unknown field", func(t *testing.T) []byte {
			d := valid(t)
			d["models"].(map[string]any)["vendor/model"].(map[string]any)["sneaky"] = true
			return marshal(t, d)
		}, "sneaky"},
		// A map's keys are outside DisallowUnknownFields, so a misspelled
		// dimension would otherwise be dropped and folded (design D7(a)
		// exists to make a dimension nobody priced explicit, not to let a
		// typo price it): the loader refuses it in both tiers.
		{"an unknown dimension in the all tier", func(t *testing.T) []byte {
			d := valid(t)
			d["models"].(map[string]any)["vendor/model"].(map[string]any)["all"].(map[string]any)["cache_read_per_M"] =
				map[string]any{"min": "1", "max": "1", "min_of": "a", "max_of": "a"}
			return marshal(t, d)
		}, `"cache_read_per_M" is not a price dimension`},
		{"an unknown dimension in the zdr tier", func(t *testing.T) []byte {
			d := valid(t)
			d["models"].(map[string]any)["vendor/model"].(map[string]any)["zdr"].(map[string]any)["web_search"] =
				map[string]any{"min": "1", "max": "1", "min_of": "a", "max_of": "a"}
			return marshal(t, d)
		}, `"web_search" is not a price dimension`},
		{"a zdr tier with no zdr endpoints", func(t *testing.T) []byte {
			return []byte(strings.Replace(string(marshal(t, valid(t))), `"zdr_endpoints":1`, `"zdr_endpoints":0`, 1))
		}, "zdr_endpoints"},
		{"trailing data", func(t *testing.T) []byte {
			return append(marshal(t, valid(t)), []byte("{}")...)
		}, "trailing data"},
		{"not JSON at all", func(t *testing.T) []byte { return []byte("nope") }, "malformed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := parse(c.make(t))
			if err == nil {
				t.Fatalf("accepted: %+v", f)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error does not name the problem (%q): %v", c.want, err)
			}
			if !strings.Contains(err.Error(), "the built-in price catalog is malformed") {
				t.Errorf("the error does not say what is malformed: %v", err)
			}
		})
	}
}

// TestShippedCatalogLoads pins that the embedded file is well-formed: a
// malformed one must fail the build, not the first run.
func TestShippedCatalogLoads(t *testing.T) {
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != Version {
		t.Errorf("version %d, want %d", f.Version, Version)
	}
	if f.CheckedAt == "" {
		t.Error("checked_at is empty")
	}
	if len(f.Models) == 0 {
		t.Fatal("the shipped catalog prices no model")
	}
	for id, e := range f.Models {
		if e.ContextTokens <= 0 || e.MaxOutputTokens <= 0 {
			t.Errorf("%s: context %d, output %d: a shipped row must carry the endpoints' limits", id, e.ContextTokens, e.MaxOutputTokens)
		}
		if _, ok := e.All["input_per_m"]; !ok {
			t.Errorf("%s: the all tier prices no input", id)
		}
	}
}

// TestStaleAfterIsThirtyDays pins the one number budget prices, doctor and
// the run all refer to: shorter than the Claude table's 90 days, because a
// marketplace price moves faster than a published model list (design D10).
func TestStaleAfterIsThirtyDays(t *testing.T) {
	if StaleAfter.Hours() != 30*24 {
		t.Errorf("StaleAfter is %v, want 30 days", StaleAfter)
	}
}
