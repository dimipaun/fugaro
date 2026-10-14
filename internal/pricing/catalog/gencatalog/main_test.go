package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// snapshots is a directory of saved API responses the tests generate from,
// built in a temp dir so no test touches the network or the checked-in
// testdata (design §5: the test path reads snapshots).
type snapshots struct {
	t    *testing.T
	dir  string
	date string
}

func newSnapshots(t *testing.T, ids ...string) *snapshots {
	t.Helper()
	s := &snapshots{t: t, dir: t.TempDir(), date: "2026-10-14"}
	models := []map[string]any{}
	for _, id := range ids {
		models = append(models, map[string]any{"id": id, "pricing": map[string]any{"prompt": "0.000001", "completion": "0.000002"}})
	}
	s.write("models.json", map[string]any{"data": models})
	s.write("zdr.json", map[string]any{"data": []any{}})
	for _, id := range ids {
		s.write("models_"+strings.ReplaceAll(id, "/", "_")+"_endpoints.json", map[string]any{
			"data": map[string]any{"endpoints": []any{endpointRow("p", "0.000001", "0.000002", nil)}},
		})
	}
	return s
}

// endpointRow is one synthetic endpoint: the tag, its input and output
// prices per token, and any extra pricing fields (cache reads, a discount,
// an overrides array) the case needs.
func endpointRow(tag, prompt, completion string, extra map[string]any) map[string]any {
	p := map[string]any{"prompt": prompt, "completion": completion}
	for k, v := range extra {
		p[k] = v
	}
	return map[string]any{"tag": tag, "context_length": 1000, "max_completion_tokens": 100, "pricing": p}
}

func (s *snapshots) write(name string, doc any) {
	s.t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, name), b, 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// endpoints replaces one model's endpoint list.
func (s *snapshots) endpoints(id string, rows ...map[string]any) {
	s.t.Helper()
	list := make([]any, len(rows))
	for i, r := range rows {
		list[i] = r
	}
	s.write("models_"+strings.ReplaceAll(id, "/", "_")+"_endpoints.json", map[string]any{"data": map[string]any{"endpoints": list}})
}

// allow writes the allowlist file and returns its path.
func (s *snapshots) allow(ids ...string) string {
	s.t.Helper()
	path := filepath.Join(s.dir, "allow.txt")
	if err := os.WriteFile(path, []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
		s.t.Fatal(err)
	}
	return path
}

// gen generates from the snapshots and returns the bytes.
func (s *snapshots) gen(ids ...string) ([]byte, error) {
	s.t.Helper()
	return generate(options{from: s.dir, allow: s.allow(ids...), date: s.date, out: filepath.Join(s.dir, "out.json")})
}

// genFile generates and decodes the result.
func (s *snapshots) genFile(ids ...string) *catalog.File {
	s.t.Helper()
	b, err := s.gen(ids...)
	if err != nil {
		s.t.Fatal(err)
	}
	var f catalog.File
	if err := json.Unmarshal(b, &f); err != nil {
		s.t.Fatal(err)
	}
	return &f
}

// TestGeneratedFileMatchesSnapshots regenerates the checked-in catalog.json
// from testdata/openrouter and compares the bytes: the file is generated,
// so the generator and the file cannot drift, and the generation is
// deterministic (same inputs, same date, same bytes).
func TestGeneratedFileMatchesSnapshots(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	o := options{
		from:  filepath.Join("..", "testdata", "openrouter"),
		allow: filepath.Join(tmp, "allow.txt"),
		date:  "2026-10-14",
		out:   filepath.Join(tmp, "catalog.json"),
	}
	if err := os.WriteFile(o.allow, []byte(strings.Join(allowlist(), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := generate(o)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the checked-in catalog.json is not what the generator produces from testdata/openrouter:\nregenerate it with scripts/gen-price-catalog.sh -from internal/pricing/catalog/testdata/openrouter -date 2026-10-14")
	}
	again, err := generate(o)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(got) {
		t.Error("two generations from the same inputs differ: the generator is not deterministic")
	}
}

// allowlist is the list scripts/gen-price-catalog.sh writes: the test must
// generate the same file the script does, so it keeps its own copy and
// fails if the two drift.
func allowlist() []string {
	return []string{
		"deepseek/deepseek-v4.1-flash",
		"deepseek/deepseek-v4-flash",
		"deepseek/deepseek-v4-pro",
		"qwen/qwen3.8-flash",
		"moonshotai/kimi-k3",
	}
}

// TestGeneratorSkipsVariantsAndAliases pins that no key in the shipped file
// is a spelling pricing.IsAlias refuses: a ":" variant or a "~" alias can
// never be claimed by a route or named by a pin, so a row for one would
// only mislead a reviewer.
func TestGeneratorSkipsVariantsAndAliases(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f catalog.File
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Models) == 0 {
		t.Fatal("the shipped catalog prices no model")
	}
	for id := range f.Models {
		if strings.Contains(id, ":") {
			t.Errorf("%s is a \":\" variant: the pin grammar refuses those, so the row could never be charged", id)
		}
		if strings.HasPrefix(id, "~") {
			t.Errorf("%s is a \"~\" alias: the pin grammar refuses those, so the row could never be charged", id)
		}
		if strings.HasPrefix(id, "anthropic/") {
			t.Errorf("%s is an anthropic/ row: no route may claim one until the mixed-credentials opt-in relaxes CheckModelPattern", id)
		}
	}
}

// TestGeneratorRefusesIsAliasKey pins the enforcement behind the previous
// test: an allowlisted ID pricing.IsAlias refuses is a hard error, so a
// later allowlist edit cannot add a key nothing can claim.
func TestGeneratorRefusesIsAliasKey(t *testing.T) {
	for _, id := range []string{"anthropic/claude-sonnet-5.5", "~deepseek/deepseek-v4.1-flash-latest", "deepseek/deepseek-v4.1-flash:batch"} {
		t.Run(id, func(t *testing.T) {
			s := newSnapshots(t, id)
			_, err := s.gen(id)
			if err == nil {
				t.Fatalf("%s was accepted: it is not a model ID a route can claim and a pin can name", id)
			}
			if !strings.Contains(err.Error(), id) {
				t.Errorf("the error does not name the model: %v", err)
			}
		})
	}
}

// TestGeneratorOmitsUnpricedDimension pins design D7(a): a dimension no
// endpoint of the tier prices is absent from the file, never written as 0,
// so the compile is the one place the fold happens.
func TestGeneratorOmitsUnpricedDimension(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f catalog.File
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	ds, ok := f.Models["deepseek/deepseek-v4.1-flash"]
	if !ok {
		t.Fatal("the shipped catalog has no deepseek/deepseek-v4.1-flash row")
	}
	for _, tier := range []map[string]catalog.Bound{ds.All, ds.ZDR} {
		for _, d := range []string{"cache_write_5m_per_m", "cache_write_1h_per_m", "web_search_per_1k"} {
			if _, ok := tier[d]; ok {
				t.Errorf("deepseek/deepseek-v4.1-flash prices no %s on any endpoint, but the file carries it", d)
			}
		}
	}
	for id, e := range f.Models {
		for _, tier := range []map[string]catalog.Bound{e.All, e.ZDR} {
			for d, b := range tier {
				if b.Min == "0" || b.Max == "0" {
					t.Errorf("%s: %s is written as 0; a dimension no endpoint prices must be omitted", id, d)
				}
				if b.MinOf == "" || b.MaxOf == "" {
					t.Errorf("%s: %s names no endpoint at an extreme", id, d)
				}
			}
		}
	}
	// A model with no ZDR-eligible endpoint has no zdr tier at all.
	q, ok := f.Models["qwen/qwen3.8-flash"]
	if !ok {
		t.Fatal("the shipped catalog has no qwen/qwen3.8-flash row")
	}
	if q.ZDREndpoints != 0 || len(q.ZDR) != 0 {
		t.Errorf("qwen/qwen3.8-flash has %d zdr endpoints and %d zdr dimensions; it should have no zdr tier", q.ZDREndpoints, len(q.ZDR))
	}
}

// TestGeneratorRefusesLongContextModel pins design D8: a model whose
// pricing carries a long-context tier is a hard error, not a price that is
// 5x too low above the threshold.
func TestGeneratorRefusesLongContextModel(t *testing.T) {
	s := newSnapshots(t, "vendor/model")
	s.write("models.json", map[string]any{"data": []any{map[string]any{
		"id":      "vendor/model",
		"pricing": map[string]any{"prompt": "0.000001", "completion": "0.000002", "overrides": []any{map[string]any{"min_tokens": 100000}}},
	}}})
	_, err := s.gen("vendor/model")
	if err == nil {
		t.Fatal("a model with a long-context tier was accepted")
	}
	for _, want := range []string{"vendor/model", "overrides"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

// TestGeneratorRefusesPricedImageOrAudioModel is the same rule for the
// other dimensions the file does not express.
func TestGeneratorRefusesPricedImageOrAudioModel(t *testing.T) {
	for _, field := range []string{"image", "image_output", "audio", "audio_output", "input_audio_cache", "internal_reasoning"} {
		t.Run(field, func(t *testing.T) {
			s := newSnapshots(t, "vendor/model")
			s.write("models.json", map[string]any{"data": []any{map[string]any{
				"id":      "vendor/model",
				"pricing": map[string]any{"prompt": "0.000001", "completion": "0.000002", field: "0.00001"},
			}}})
			_, err := s.gen("vendor/model")
			if err == nil {
				t.Fatalf("a model pricing %s was accepted", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("the error does not name the field: %v", err)
			}
		})
	}
}

// TestGeneratorRefusesMissingAllowlistedModel pins that a rename cannot
// quietly shrink the file.
func TestGeneratorRefusesMissingAllowlistedModel(t *testing.T) {
	s := newSnapshots(t, "vendor/model")
	_, err := s.gen("vendor/model", "vendor/gone")
	if err == nil {
		t.Fatal("an allowlisted model the API no longer serves was accepted")
	}
	if !strings.Contains(err.Error(), "vendor/gone") {
		t.Errorf("the error does not name the model: %v", err)
	}
}

// TestGeneratorRefusesDuplicateTagWithDifferentPrices pins the refusal that
// keeps the file from depending on which of two rows came last.
func TestGeneratorRefusesDuplicateTagWithDifferentPrices(t *testing.T) {
	s := newSnapshots(t, "vendor/model")
	s.endpoints("vendor/model",
		endpointRow("p", "0.000001", "0.000002", nil),
		endpointRow("p", "0.000003", "0.000004", nil),
	)
	_, err := s.gen("vendor/model")
	if err == nil {
		t.Fatal("a tag appearing twice with different prices was accepted")
	}
	if !strings.Contains(err.Error(), "p") {
		t.Errorf("the error does not name the tag: %v", err)
	}
	// The same tag twice with identical prices collapses, which the API
	// really does return.
	s.endpoints("vendor/model",
		endpointRow("p", "0.000001", "0.000002", nil),
		endpointRow("p", "0.000001", "0.000002", nil),
	)
	if f := s.genFile("vendor/model"); f.Models["vendor/model"].Endpoints != 1 {
		t.Errorf("two identical rows for one tag gave %d endpoints, want 1", f.Models["vendor/model"].Endpoints)
	}
}

// TestGeneratorExactDecimalShift pins the string arithmetic: a price is
// shifted, never formatted from a float64, so a diff shows a price change
// and never a rounding wobble.
func TestGeneratorExactDecimalShift(t *testing.T) {
	perM := []struct{ in, want string }{
		{"0.00000002016", "0.02016"},
		{"0.000000002", "0.002"},
		{"0.000000891", "0.891"},
		{"0.00000001584", "0.01584"},
		{"0.000001", "1"},
		{"0.0000005", "0.5"},
		{"2", "2000000"},
		{"0", "0"},
	}
	for _, c := range perM {
		got, err := shiftDecimal(c.in, 6)
		if err != nil {
			t.Errorf("shiftDecimal(%q, 6): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("shiftDecimal(%q, 6) = %q, want %q", c.in, got, c.want)
		}
	}
	// Web search is priced per search: three places, not six.
	perSearch := []struct{ in, want string }{
		{"0.01", "10"},
		{"0.0001", "0.1"},
		{"0.0000001", "0.0001"},
	}
	for _, c := range perSearch {
		got, err := shiftDecimal(c.in, 3)
		if err != nil {
			t.Errorf("shiftDecimal(%q, 3): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("shiftDecimal(%q, 3) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "abc", "-1", "1e-9", "1.2.3", "0x10"} {
		if got, err := shiftDecimal(bad, 6); err == nil {
			t.Errorf("shiftDecimal(%q, 6) = %q, want an error", bad, got)
		}
	}
}

// TestGeneratorStandInFactors pins the fold D7(a) computes for an endpoint
// that publishes no price for a dimension: its own input price times 1.0
// for a cache read, 1.25 for a 5-minute write and 2 for an hour's.
func TestGeneratorStandInFactors(t *testing.T) {
	cases := []struct {
		in       string
		num, den int
		want     string
	}{
		{"0.00000003276", 1, 1, "0.00000003276"},
		{"0.00000003276", 125, 100, "0.00000004095"},
		{"0.00000019", 2, 1, "0.00000038"},
		{"0.00000015", 125, 100, "0.0000001875"},
		{"0", 2, 1, "0"},
	}
	for _, c := range cases {
		got, err := scaleBy(c.in, c.num, c.den)
		if err != nil {
			t.Errorf("scaleBy(%q, %d, %d): %v", c.in, c.num, c.den, err)
			continue
		}
		if got != c.want {
			t.Errorf("scaleBy(%q, %d, %d) = %q, want %q", c.in, c.num, c.den, got, c.want)
		}
	}
	// The stand-in participates in both bounds: a tier where one endpoint
	// prices a cache read and another does not takes the extreme over the
	// real price and the stand-in.
	s := newSnapshots(t, "vendor/model")
	s.endpoints("vendor/model",
		endpointRow("cheap", "0.000001", "0.000002", map[string]any{"input_cache_read": "0.0000001"}),
		endpointRow("plain", "0.000010", "0.000020", nil),
	)
	f := s.genFile("vendor/model")
	got := f.Models["vendor/model"].All["cache_read_per_m"]
	if got.Min != "0.1" || got.Max != "10" {
		t.Errorf("cache_read_per_m = %s..%s, want 0.1..10 (the real price and the other endpoint's input price)", got.Min, got.Max)
	}
}

// TestGeneratorTierMatchByTag pins the ZDR match: only the endpoints the
// ZDR list names by (model_id, tag) are in the zdr tier, and a model with
// no ZDR-eligible endpoint has no zdr tier.
func TestGeneratorTierMatchByTag(t *testing.T) {
	s := newSnapshots(t, "vendor/model")
	s.endpoints("vendor/model",
		endpointRow("a", "0.000001", "0.000002", nil),
		endpointRow("b", "0.000003", "0.000004", nil),
	)
	s.write("zdr.json", map[string]any{"data": []any{
		map[string]any{"model_id": "vendor/model", "tag": "b"},
		map[string]any{"model_id": "vendor/other", "tag": "a"},
	}})
	f := s.genFile("vendor/model")
	e := f.Models["vendor/model"]
	if e.Endpoints != 2 || e.ZDREndpoints != 1 {
		t.Fatalf("endpoints %d, zdr %d; want 2 and 1", e.Endpoints, e.ZDREndpoints)
	}
	if got := e.ZDR["input_per_m"].Min; got != "3" {
		t.Errorf("the zdr tier's input is %s, want 3 (only tag b is ZDR-eligible)", got)
	}
	if got := e.All["input_per_m"].Min; got != "1" {
		t.Errorf("the all tier's input is %s, want 1", got)
	}
}

// TestGeneratorRequiresDate pins that the generator never reads the wall
// clock: without -date there is nothing to write as checked_at.
func TestGeneratorRequiresDate(t *testing.T) {
	s := newSnapshots(t, "vendor/model")
	_, err := generate(options{from: s.dir, allow: s.allow("vendor/model"), out: filepath.Join(s.dir, "o.json")})
	if err == nil {
		t.Fatal("a run with no -date was accepted")
	}
	if !strings.Contains(err.Error(), "-date") {
		t.Errorf("the error does not name the flag: %v", err)
	}
}

// TestGeneratorRefusesEmptyAllowlist pins that an empty list is an error,
// not an empty file.
func TestGeneratorRefusesEmptyAllowlist(t *testing.T) {
	s := newSnapshots(t, "vendor/model")
	path := filepath.Join(s.dir, "empty.txt")
	if err := os.WriteFile(path, []byte("# nothing\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := generate(options{from: s.dir, allow: path, date: s.date, out: filepath.Join(s.dir, "o.json")}); err == nil {
		t.Fatal("an allowlist with no model ID was accepted")
	}
}
