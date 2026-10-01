package pricing

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestOverridesRoundTrip(t *testing.T) {
	o := Overrides{
		"claude-alpha-1": {InputPerM: 4.4, OutputPerM: 22, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.05, WebSearchPer1k: 10},
		"claude-new-9":   {InputPerM: 3, OutputPerM: 15, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, LongContext: &Tier{AboveInputTokens: 200_000, InputPerM: 6, OutputPerM: 22.5}},
	}
	s, err := o.Env()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(s, " \n") {
		t.Errorf("Env() is not compact: %q", s)
	}
	back, err := ParseOverrides(s)
	if err != nil {
		t.Fatalf("ParseOverrides(%q): %v", s, err)
	}
	if !reflect.DeepEqual(back, o) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, o)
	}
	s2, _ := back.Env()
	if s2 != s {
		t.Fatalf("Env() is not stable: %q then %q", s, s2)
	}
	if e, err := (Overrides{}).Env(); e != "" || err != nil {
		t.Errorf("empty overrides: %q, %v", e, err)
	}
	if o, err := ParseOverrides(""); err != nil || len(o) != 0 {
		t.Errorf("unset: %v, %v", o, err)
	}
}

func TestOverridesDefaultMultipliers(t *testing.T) {
	// Left out, the cache multipliers and the search price are the list
	// ones, never zero: a missing field must not make cache writes free.
	o, err := ParseOverrides(`{"claude-alpha-1":{"input_per_m":4,"output_per_m":20}}`)
	if err != nil {
		t.Fatal(err)
	}
	r := o["claude-alpha-1"]
	if r.CacheWrite5m != 1.25 || r.CacheWrite1h != 2 || r.CacheRead != 0.1 || r.WebSearchPer1k != 10 {
		t.Fatalf("defaults: %+v", r)
	}
	// An explicit zero stays zero.
	o, err = ParseOverrides(`{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"cache_read":0}}`)
	if err != nil || o["claude-alpha-1"].CacheRead != 0 {
		t.Fatalf("explicit zero: %+v %v", o, err)
	}
}

func TestOverridesRejectBad(t *testing.T) {
	for name, s := range map[string]string{
		"not json":          `input=4`,
		"not an object":     `[1]`,
		"NaN":               `{"claude-alpha-1":{"input_per_m":NaN,"output_per_m":20}}`,
		"negative":          `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"cache_read":-0.1}}`,
		"zero input":        `{"claude-alpha-1":{"input_per_m":0,"output_per_m":20}}`,
		"missing input":     `{"claude-alpha-1":{"output_per_m":20}}`,
		"missing output":    `{"claude-alpha-1":{"input_per_m":4}}`,
		"input above 1000":  `{"claude-alpha-1":{"input_per_m":1000.5,"output_per_m":20}}`,
		"multiplier 11":     `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"cache_write_1h":11}}`,
		"unknown field":     `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"batch":0.5}}`,
		"alias key":         `{"sonnet":{"input_per_m":4,"output_per_m":20}}`,
		"1m key":            `{"claude-alpha-1[1m]":{"input_per_m":4,"output_per_m":20}}`,
		"empty key":         `{"":{"input_per_m":4,"output_per_m":20}}`,
		"tier at zero":      `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"long_context":{"above_input_tokens":0,"input_per_m":8,"output_per_m":30}}}`,
		"tier without rate": `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"long_context":{"above_input_tokens":200000,"output_per_m":30}}}`,
		"trailing data":     `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20}} {}`,
		"null model":        `{"claude-alpha-1":null}`,
		"top-level null":    `null`,
		"null field":        `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"cache_read":null}}`,
		"upper-case field":  `{"claude-alpha-1":{"INPUT_PER_M":4,"output_per_m":20}}`,
		"mixed-case field":  `{"claude-alpha-1":{"input_per_m":4,"Output_Per_M":20}}`,
		"upper-case tier":   `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"long_context":{"Above_Input_Tokens":1,"input_per_m":8,"output_per_m":30}}}`,
		"duplicate field":   `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20,"input_per_m":0.001}}`,
		"duplicate model":   `{"claude-alpha-1":{"input_per_m":4,"output_per_m":20},"claude-alpha-1":{"input_per_m":1,"output_per_m":2}}`,
		"object in a rate":  `{"claude-alpha-1":{"input_per_m":{"x":1},"output_per_m":20}}`,
	} {
		if o, err := ParseOverrides(s); err == nil {
			t.Errorf("%s: %q parsed as %+v", name, s, o)
		}
	}
	for name, r := range map[string]Rates{
		"NaN":       {InputPerM: math.NaN(), OutputPerM: 20},
		"infinite":  {InputPerM: 4, OutputPerM: math.Inf(1)},
		"negative":  {InputPerM: 4, OutputPerM: 20, CacheWrite5m: -1},
		"zero":      {InputPerM: 0, OutputPerM: 20},
		"mult 11":   {InputPerM: 4, OutputPerM: 20, CacheRead: 11},
		"search":    {InputPerM: 4, OutputPerM: 20, WebSearchPer1k: math.NaN()},
		"tier zero": {InputPerM: 4, OutputPerM: 20, LongContext: &Tier{InputPerM: 8, OutputPerM: 30}},
	} {
		if _, err := (Overrides{"claude-alpha-1": r}).Env(); err == nil {
			t.Errorf("Env() accepted %s", name)
		}
		if _, err := testTable().With(Overrides{"claude-alpha-1": r}); err == nil {
			t.Errorf("With() accepted %s", name)
		}
	}
}

func TestWithOverridesReplacesModel(t *testing.T) {
	tb := testTable()
	replacement := Rates{InputPerM: 4.4, OutputPerM: 22, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}
	added := Rates{InputPerM: 1, OutputPerM: 2, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}
	got, err := tb.With(Overrides{"claude-alpha-1": replacement, "claude-new-9": added})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.Lookup("claude-alpha-1@20260101")
	if !ok || !reflect.DeepEqual(m.Rates, replacement) {
		t.Errorf("replaced through its alias: %+v %v", m.Rates, ok)
	}
	if m.ContextTokens != 1_000_000 || m.ImageTokens != 6000 || m.MaxOutputTokens != 128_000 {
		t.Errorf("the model's limits must survive a price override: %+v", m)
	}
	if n, ok := got.Lookup("claude-new-9"); !ok || !reflect.DeepEqual(n.Rates, added) || n.ImageTokens != 0 {
		t.Errorf("added: %+v %v (images unpriced for a model the table doesn't know)", n, ok)
	}
	// An override keyed by an alias replaces the model it names.
	got2, err := tb.With(Overrides{"claude-alpha-1@20260101": replacement})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := got2.Lookup("claude-alpha-1"); !reflect.DeepEqual(m.Rates, replacement) {
		t.Errorf("alias key: %+v", m.Rates)
	}
	if len(got2.Models) != len(tb.Models) {
		t.Errorf("an alias key added a model")
	}
	// The receiver is untouched, and the result keeps its source.
	if m, _ := tb.Lookup("claude-alpha-1"); m.Rates.InputPerM != 4 {
		t.Errorf("With changed its receiver: %+v", m.Rates)
	}
	if got.Source != tb.Source || got.CheckedAt != tb.CheckedAt {
		t.Errorf("source lost: %q %q", got.Source, got.CheckedAt)
	}
	if same, err := tb.With(nil); err != nil || len(same.Models) != len(tb.Models) {
		t.Errorf("no overrides: %v %v", same, err)
	}
}

func TestWithRefusesTwoKeysForOneModel(t *testing.T) {
	a := Rates{InputPerM: 1, OutputPerM: 5, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}
	b := Rates{InputPerM: 9, OutputPerM: 45, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1}
	// The ID and its alias both price the same model: whichever the map
	// visited last would win, so the pair is refused, every time.
	for i := 0; i < 50; i++ {
		_, err := testTable().With(Overrides{"claude-alpha-1": a, "claude-alpha-1@20260101": b})
		if err == nil {
			t.Fatal("an ID and its alias were both accepted")
		}
		if want := "claude-alpha-1 and claude-alpha-1@20260101 both price claude-alpha-1"; !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not say %q", err, want)
		}
	}
}
