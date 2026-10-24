package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
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

// snapshotDate is the date testdata/openrouter was read, and the checked_at
// the checked-in catalog.json carries. Both tests below regenerate with it, so
// refreshing the snapshots is one edit here rather than two that could drift
// apart, and the date a reader of a failure message is told to pass to the
// script is the same one.
const snapshotDate = "2026-10-23"

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
		date:  snapshotDate,
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
		t.Errorf("the checked-in catalog.json is not what the generator produces from testdata/openrouter:\nregenerate it with scripts/gen-price-catalog.sh -from internal/pricing/catalog/testdata/openrouter -date " + snapshotDate)
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

// TestSnapshotsAreTrimmedToTheDocumentedFields pins the snapshot README
// against the generator: stripping testdata/openrouter down to exactly the
// fields the README documents must still generate the checked-in
// catalog.json, byte for byte. The README is what a reviewer trims a
// refreshed snapshot to, so a field the generator reads and the README
// leaves out would silently drop a whole dimension from the file — which is
// how `input_cache_write` went missing once — and this test fails instead.
func TestSnapshotsAreTrimmedToTheDocumentedFields(t *testing.T) {
	kept := keptFields(t)
	src := filepath.Join("..", "testdata", "openrouter")
	dir := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		fields, ok := kept[kind(name)]
		if !ok {
			t.Fatalf("%s is not one of the documented snapshot files: %v", name, kept)
		}
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		switch data := doc["data"].(type) {
		case []any:
			for i, r := range data {
				data[i] = keepFields(t, r.(map[string]any), fields)
			}
		case map[string]any:
			rows, _ := data["endpoints"].([]any)
			for i, r := range rows {
				rows[i] = keepFields(t, r.(map[string]any), fields)
			}
		default:
			t.Fatalf("%s: data is %T", name, doc["data"])
		}
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), out, 0o644); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n < 3 {
		t.Fatalf("stripped %d snapshot files, want at least 3", n)
	}
	want, err := os.ReadFile(filepath.Join("..", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	allow := filepath.Join(dir, "allow.txt")
	if err := os.WriteFile(allow, []byte(strings.Join(allowlist(), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := generate(options{from: dir, allow: allow, date: snapshotDate, out: filepath.Join(dir, "catalog.json")})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the snapshots trimmed to the fields the README documents do not generate the checked-in catalog.json:\n" +
			"the README's kept-fields list is missing something the generator reads (or names something it does not); " +
			"compare it with apiDims and the endpoint struct in main.go")
	}
}

// kind is the README's name for a snapshot file: models.json, zdr.json, or
// one of the per-model endpoints files.
func kind(name string) string {
	switch {
	case name == "models.json":
		return "models"
	case name == "zdr.json":
		return "zdr"
	default:
		return "endpoints"
	}
}

// keepFields is one row with only the documented fields left: a bare name is
// the row's own field, a dotted one a field of that sub-object.
func keepFields(t *testing.T, row map[string]any, fields []string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, f := range fields {
		head, tail, dotted := strings.Cut(f, ".")
		v, ok := row[head]
		if !ok {
			continue
		}
		if !dotted {
			out[head] = v
			continue
		}
		sub, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%s is %T, not an object", head, v)
		}
		if x, ok := sub[tail]; ok {
			kept, _ := out[head].(map[string]any)
			if kept == nil {
				kept = map[string]any{}
				out[head] = kept
			}
			kept[tail] = x
		}
	}
	return out
}

// keptFields parses the README's kept-fields block: the fields each snapshot
// file keeps, keyed by the kind above.
func keptFields(t *testing.T) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "openrouter", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	const begin, end = "<!-- kept-fields: begin -->", "<!-- kept-fields: end -->"
	out := map[string][]string{}
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.Contains(line, begin):
			in = true
			continue
		case strings.Contains(line, end):
			in = false
			continue
		case !in || strings.TrimSpace(line) == "":
			continue
		}
		k, fields, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("the README's kept-fields line %q is not \"kind: field field\"", line)
		}
		out[strings.TrimSpace(k)] = strings.Fields(fields)
	}
	for _, k := range []string{"models", "endpoints", "zdr"} {
		if len(out[k]) == 0 {
			t.Fatalf("the README's kept-fields block documents no %s fields: %v", k, out)
		}
	}
	return out
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
			t.Errorf("%s is an anthropic/ row: the shipped file carries none until the price catalog's own phase adds the claimed Claude IDs", id)
		}
	}
}

// TestGeneratorRefusesIsAliasKey pins the enforcement behind the previous
// test: an allowlisted ID pricing.IsAlias refuses is a hard error, so a
// later allowlist edit cannot add a key nothing can claim.
//
// Once the mixed-credentials opt-in shipped, IsAlias stopped calling an
// explicit "anthropic/<id>" an alias -- it is the one shape a provider route
// may claim -- so "anthropic/claude-sonnet-5.5" is no longer a refused key
// here and its case moved out of this list. Everything else IsAlias refuses
// (a "~" alias, a ":" variant) is still refused, and
// TestGeneratorSkipsVariantsAndAliases still keeps anthropic/ rows out of
// the shipped file until the price catalog's own phase adds them.
func TestGeneratorRefusesIsAliasKey(t *testing.T) {
	for _, id := range []string{"~deepseek/deepseek-v4.1-flash-latest", "deepseek/deepseek-v4.1-flash:batch"} {
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

// TestGeneratorDuplicateTagTakesTheLowerLimits pins the fold dedupe does:
// the two rows of one tag are one endpoint for pricing but two reports of
// its limits, and keeping either row whole would record a ceiling above
// what the other row accepts — which is what design §4 promises the file
// does not do — and would make the file depend on the order the response
// listed them in.
func TestGeneratorDuplicateTagTakesTheLowerLimits(t *testing.T) {
	row := func(context, out any) map[string]any {
		return map[string]any{
			"tag": "p", "context_length": context, "max_completion_tokens": out,
			"pricing": map[string]any{"prompt": "0.000001", "completion": "0.000002"},
		}
	}
	for _, c := range []struct {
		name string
		rows []map[string]any
	}{
		{"the higher row first", []map[string]any{row(2000, 200), row(1000, 100)}},
		{"the lower row first", []map[string]any{row(1000, 100), row(2000, 200)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSnapshots(t, "vendor/model")
			s.endpoints("vendor/model", c.rows...)
			e := s.genFile("vendor/model").Models["vendor/model"]
			if e.Endpoints != 1 {
				t.Errorf("%d endpoints, want 1: the two rows share a tag", e.Endpoints)
			}
			if e.ContextTokens != 1000 || e.MaxOutputTokens != 100 {
				t.Errorf("limits %d/%d, want 1000/100: the fold must take the smaller of the two rows, in either order",
					e.ContextTokens, e.MaxOutputTokens)
			}
		})
	}
	// A null on one row is an endpoint advertising no limit, so it imposes
	// nothing and does not erase what the other row reported.
	s := newSnapshots(t, "vendor/model")
	s.endpoints("vendor/model", row(nil, nil), row(1000, 100))
	e := s.genFile("vendor/model").Models["vendor/model"]
	if e.ContextTokens != 1000 || e.MaxOutputTokens != 100 {
		t.Errorf("limits %d/%d, want the one row that advertised them", e.ContextTokens, e.MaxOutputTokens)
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
	// The same arithmetic, run on the factors the generator actually uses:
	// the cases above are the shapes, this is the table. The names are the
	// set apiDims reaches for, and each factor's exact value is here too, so
	// a mistyped stand-in cannot pass.
	for name, want := range map[string]string{
		"cache_read":     "1",    // the endpoint's whole input price
		"cache_write_5m": "1.25", // pricing.DefaultCacheWrite5m
		"cache_write_1h": "2",    // pricing.DefaultCacheWrite1h
	} {
		f, ok := standInFactor[name]
		if !ok {
			t.Errorf("no %s stand-in", name)
			continue
		}
		if got, err := scaleBy("1", f.num, f.den); err != nil {
			t.Errorf("the %s stand-in %d/%d: %v", name, f.num, f.den, err)
		} else if got != want {
			t.Errorf("the %s stand-in is %d/%d = %s, want %s", name, f.num, f.den, got, want)
		}
	}
	if len(standInFactor) != 3 {
		t.Errorf("the stand-in table has %d entries, want 3: %v", len(standInFactor), standInFactor)
	}
	// And the factors must be pricing's own write defaults, to the digit:
	// the compile folds a dimension the catalog does not price with
	// pricing.DefaultCacheWrite5m/1h (catalog_apply.go), so a change to
	// overrides.go that left the generator's fractions alone would write
	// bounds the compiler disagrees with. The comparison is on the decimal
	// text, so it is exact and needs no float arithmetic here.
	for name, def := range map[string]float64{
		"cache_write_5m": pricing.DefaultCacheWrite5m,
		"cache_write_1h": pricing.DefaultCacheWrite1h,
	} {
		f := standInFactor[name]
		got, err := scaleBy("1", f.num, f.den)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if want := strconv.FormatFloat(def, 'f', -1, 64); got != want {
			t.Errorf("the %s stand-in is %d/%d = %s, but pricing's default is %s: the generated bound and the compiled fold would disagree", name, f.num, f.den, got, want)
		}
	}
	// The cache read is the generator's own rule, not a table default: 1.0,
	// the whole of the endpoint's input price (design D7(a)).
	if f, ok := standInFactor["cache_read"]; ok && f.num != f.den {
		t.Errorf("the cache_read stand-in is %d/%d, want 1/1", f.num, f.den)
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

// TestGeneratorRefusesAFileTheLoaderWouldRefuse pins that the generator judges
// its own output by the reader's rules before anything is written: an endpoint
// that prices a dimension at 0 — the API publishes free endpoints — would
// otherwise become a file that fails only in a later test run, or in
// catalog.Load of a released binary.
func TestGeneratorRefusesAFileTheLoaderWouldRefuse(t *testing.T) {
	s := newSnapshots(t, "vendor/model")
	s.endpoints("vendor/model", endpointRow("p", "0", "0.000002", nil))
	out := filepath.Join(s.dir, "out.json")
	err := run(options{from: s.dir, allow: s.allow("vendor/model"), date: s.date, out: out}, io.Discard)
	if err == nil {
		t.Fatal("a free endpoint price was written out")
	}
	if !strings.Contains(err.Error(), "does not load") || !strings.Contains(err.Error(), "vendor/model") {
		t.Errorf("the error does not say the file does not load, and which model: %v", err)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Error("the file was written anyway")
	}
}

// TestGeneratorRefusesDuplicateCanonicalID (plan Task 1b.2): a Claude row
// carries the canonical ID of the same model beside its own key, and two rows
// carrying one canonical ID are a hard error — the map would hold one model
// twice, at two routes' prices, and which of them a pin was charged would
// depend on which row was read.
//
// Hand mutation this fails on: keeping the first row and dropping the second,
// or writing both out and leaving it to catalog.Load (which refuses it too,
// but only when the file is loaded, after something else has been charged).
func TestGeneratorRefusesDuplicateCanonicalID(t *testing.T) {
	s := newSnapshots(t, "anthropic/claude-sonnet-5.5", "anthropic/claude-sonnet-5-5")
	path := filepath.Join(s.dir, "allow.txt")
	if err := os.WriteFile(path, []byte("anthropic/claude-sonnet-5.5 claude-sonnet-5-5\nanthropic/claude-sonnet-5-5 claude-sonnet-5-5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(s.dir, "out.json")
	_, err := generate(options{from: s.dir, allow: path, date: s.date, out: out})
	if err == nil {
		t.Fatal("two rows claiming one canonical ID were written out")
	}
	for _, want := range []string{"claude-sonnet-5-5", "anthropic/claude-sonnet-5.5", "anthropic/claude-sonnet-5-5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Error("the file was written anyway")
	}
}

// TestGeneratorRefusesAClaudeRowWithNoCanonicalID: a key under the anthropic/
// prefix is the one shape a route may claim a Claude model by, and it must
// carry the direct spelling of the same model. Nothing derives one from the
// other — the case/separator fold that used to is what phase 1b removed — so a
// row without its pair is refused rather than guessed at.
func TestGeneratorRefusesAClaudeRowWithNoCanonicalID(t *testing.T) {
	s := newSnapshots(t, "anthropic/claude-sonnet-5.5")
	_, err := generate(options{from: s.dir, allow: s.allow("anthropic/claude-sonnet-5.5"), date: s.date, out: filepath.Join(s.dir, "o.json")})
	if err == nil {
		t.Fatal("a Claude row with no canonical ID was written out")
	}
	if !strings.Contains(err.Error(), "canonical ID") || !strings.Contains(err.Error(), "anthropic/claude-sonnet-5.5") {
		t.Errorf("the error does not say which row needs a canonical ID: %v", err)
	}
}

// TestGeneratorRefusesARowThatIsItsOwnCanonicalID: a line that writes one
// spelling twice — "<route id> <same route id>" — is the mistake the pair
// exists to make impossible, and it must be refused by the message that says
// *that*. The shape check above it (a canonical ID carries no "/") is true of
// the value but is not what the line did: a Claude key begins with "anthropic/",
// so it matched that case first and this one could never be reached (the review
// finding on this change). The row would otherwise load with canonical_id equal
// to the key, which is exactly the "two spellings that look alike are two
// answers" state the pair is checked in to prevent.
//
// Mutation (run, restored): move this case back below the
// strings.ContainsAny case. The test still fails, on the message — the error
// says the value is not a canonical ID rather than that the line wrote one
// spelling where two are required.
func TestGeneratorRefusesARowThatIsItsOwnCanonicalID(t *testing.T) {
	const id = "anthropic/claude-sonnet-5.5"
	s := newSnapshots(t, id)
	path := filepath.Join(s.dir, "allow.txt")
	if err := os.WriteFile(path, []byte(id+" "+id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(s.dir, "o.json")
	_, err := generate(options{from: s.dir, allow: path, date: s.date, out: out})
	if err == nil {
		t.Fatal("a row that is its own canonical ID was written out")
	}
	if !strings.Contains(err.Error(), "its own canonical ID") {
		t.Errorf("the error does not say the row wrote one spelling twice: %v", err)
	}
	for _, want := range []string{id, "claude-sonnet-5-5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "is not a canonical ID") {
		t.Errorf("the row was refused as a bad value rather than as one spelling written twice: %v", err)
	}
	if _, serr := os.Stat(out); serr == nil {
		t.Error("the file was written anyway")
	}
}

// TestGeneratorWritesTheCanonicalPair: the pair the allowlist writes reaches
// the file and loads, and the key is the route's own ID while canonical_id is
// the direct spelling a pin carries. The inverse mutation — writing the pair
// swapped, or dropping canonical_id — is what the loader's rule above and the
// canonical-to-route map's exhaustiveness test both read.
func TestGeneratorWritesTheCanonicalPair(t *testing.T) {
	const key, canonical = "anthropic/claude-sonnet-5.5", "claude-sonnet-5-5"
	s := newSnapshots(t, key)
	path := filepath.Join(s.dir, "allow.txt")
	if err := os.WriteFile(path, []byte(key+" "+canonical+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := generate(options{from: s.dir, allow: path, date: s.date, out: filepath.Join(s.dir, "o.json")})
	if err != nil {
		t.Fatal(err)
	}
	f, err := catalog.Parse(b)
	if err != nil {
		t.Fatalf("the generator wrote a file the loader refuses: %v", err)
	}
	e, ok := f.Models[key]
	if !ok {
		t.Fatalf("the file has no row for %s: %v", key, f.Models)
	}
	if e.CanonicalID != canonical {
		t.Errorf("the row's canonical_id = %q, want %q", e.CanonicalID, canonical)
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
