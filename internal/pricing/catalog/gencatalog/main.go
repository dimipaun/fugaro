// Command gencatalog generates the embedded provider price catalog
// (internal/pricing/catalog/catalog.json) from OpenRouter's public API.
//
// It reads three public endpoints and no credential of any kind:
//
//	https://openrouter.ai/api/v1/models
//	https://openrouter.ai/api/v1/models/<id>/endpoints
//	https://openrouter.ai/api/v1/endpoints/zdr
//
// or the same responses saved under -from, which is how the tests run and
// how a reviewer regenerates the checked-in file offline. Nothing here
// reads the wall clock: the checked_at date is -date, so the same inputs
// and the same date produce the same bytes (design provider-price-catalog.md
// §5). No float64 appears in this package: every price is shifted as a
// decimal string, so a diff shows a price change and never a rounding
// wobble (design §4).
//
// Run it through scripts/gen-price-catalog.sh.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

const apiBase = "https://openrouter.ai/api/v1"

// sources are the URLs the file records, in the order the design names
// them. They are constants: the file says what it was generated from even
// when it was generated from a snapshot of them.
var sources = []string{
	apiBase + "/models",
	apiBase + "/models/{id}/endpoints",
	apiBase + "/endpoints/zdr",
}

// refusedPricing are the model-level pricing fields the file does not
// express, so an allowlisted model that carries one is a hard error rather
// than a silently wrong price (design D8): a long-context tier (a threshold
// above which every rate changes), an image, audio or internal-reasoning
// dimension.
var refusedPricing = []struct{ key, what string }{
	{"overrides", "a long-context price tier"},
	{"image", "an image price"},
	{"image_output", "an image output price"},
	{"audio", "an audio price"},
	{"audio_output", "an audio output price"},
	{"input_audio_cache", "an audio cache price"},
	{"internal_reasoning", "an internal-reasoning price"},
}

// apiDims maps an endpoint pricing key to the file's dimension name, the
// number of decimal places the per-unit price is shifted by to reach the
// file's unit, and the stand-in factor for an endpoint that does not price
// it ("" when the dimension has no input-price stand-in). Every other key
// in an endpoint's pricing (discount, and the time-of-day overrides some
// providers publish) is ignored: the generator records the price the API
// published (design D8).
type dim struct {
	key     string // the endpoint pricing field
	dim     string // the file's dimension name
	places  int    // the decimal shift from the API's per-unit price to the file's unit
	standIn string // the stand-in factor's name, "" when the dimension has none
}

var apiDims = []dim{
	{"prompt", "input_per_m", 6, ""},
	{"completion", "output_per_m", 6, ""},
	{"input_cache_read", "cache_read_per_m", 6, "cache_read"},
	{"input_cache_write", "cache_write_5m_per_m", 6, "cache_write_5m"},
	{"input_cache_write_1h", "cache_write_1h_per_m", 6, "cache_write_1h"},
	{"web_search", "web_search_per_1k", 3, ""},
}

// standInFactor is the multiple of an endpoint's own input price that stands
// in for a dimension the endpoint does not price, applied to both bounds
// (design D7(a), plan P12): 1.0 for a cache read, and for the writes the same
// multiples the compile folds with, pricing.DefaultCacheWrite5m (1.25) and
// pricing.DefaultCacheWrite1h (2), so a generated bound and a folded one are
// the same number. The writes are spelled as the exact fractions the
// generator's string arithmetic needs — this package shifts prices as decimal
// strings and never as float64 (decimal.go) — and TestGeneratorStandInFactors
// pins each fraction to its constant, so changing overrides.go without
// changing these cannot pass. A dimension with no factor — web search, a
// per-search price with no input-price equivalent — has no stand-in and is
// omitted when no endpoint prices it.
var standInFactor = map[string]struct{ num, den int }{
	"cache_read":     {1, 1},
	"cache_write_5m": {125, 100}, // pricing.DefaultCacheWrite5m
	"cache_write_1h": {2, 1},     // pricing.DefaultCacheWrite1h
}

type options struct {
	from   string // a directory of saved snapshots, instead of the network
	models string // one saved /api/v1/models response
	zdr    string // one saved /api/v1/endpoints/zdr response
	allow  string // the allowlist file
	date   string // the checked_at to write
	out    string // where the file goes
}

func main() {
	var o options
	flag.StringVar(&o.from, "from", "", "directory of saved snapshots (models.json, zdr.json, models_<id>_endpoints.json), read instead of the network")
	flag.StringVar(&o.models, "models", "", "a saved /api/v1/models response (overrides -from's models.json)")
	flag.StringVar(&o.zdr, "zdr", "", "a saved /api/v1/endpoints/zdr response (overrides -from's zdr.json)")
	flag.StringVar(&o.allow, "allow", "", "file listing the model IDs to include, one per line (required)")
	flag.StringVar(&o.date, "date", "", "the checked_at date to write, YYYY-MM-DD (required; the generator never reads the wall clock)")
	flag.StringVar(&o.out, "o", "", "the file to write")
	flag.Parse()
	if err := run(o, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gencatalog:", err)
		os.Exit(1)
	}
}

// run generates the file, writes it and reports what changed.
func run(o options, summary io.Writer) error {
	b, err := generate(o)
	if err != nil {
		return err
	}
	old, rerr := os.ReadFile(o.out)
	if dir := filepath.Dir(o.out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(o.out, b, 0o644); err != nil {
		return err
	}
	if rerr == nil {
		reportDiff(summary, old, b)
	}
	return nil
}

// generate is the whole pipeline: the allowlist, the three responses, the
// tier match, the exact decimal shift, the deterministic JSON.
func generate(o options) ([]byte, error) {
	if o.allow == "" {
		return nil, errors.New("-allow is required: the list of model IDs to include is the script's, so growing it is a reviewable diff")
	}
	if o.out == "" {
		return nil, errors.New("-o is required")
	}
	if _, err := time.Parse("2006-01-02", o.date); err != nil {
		return nil, fmt.Errorf("-date %q: it must be YYYY-MM-DD (%v)", o.date, err)
	}
	entries, err := readAllowlist(o.allow)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	src := source{opts: o}
	modelsRaw, err := src.get("models")
	if err != nil {
		return nil, err
	}
	zdrRaw, err := src.get("zdr")
	if err != nil {
		return nil, err
	}
	if err := checkModels(modelsRaw, ids); err != nil {
		return nil, err
	}
	zdrTags, err := parseZDR(zdrRaw, ids)
	if err != nil {
		return nil, err
	}
	f := &catalog.File{Version: catalog.Version, CheckedAt: o.date, Sources: sources, Models: map[string]catalog.Entry{}}
	for _, a := range entries {
		eps, err := src.endpoints(a.id)
		if err != nil {
			return nil, err
		}
		e, err := buildEntry(a.id, eps, zdrTags[a.id])
		if err != nil {
			return nil, err
		}
		// The canonical ID the allowlist paired with the key: the row's only
		// statement about which model it prices, and what the reader's own
		// rules (catalog.validate) hold to one row per canonical ID.
		e.CanonicalID = a.canonical
		f.Models[a.id] = e
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	// The reader's own rules judge the bytes before anything is written: a
	// price the file cannot carry (an endpoint pricing "0", a max below its
	// min, a dimension nobody priced) is this command's error, named here,
	// rather than a file that fails a test run later or a released binary's
	// Load. catalog.Parse is Load's body, so the two cannot disagree.
	if _, err := catalog.Parse(b); err != nil {
		return nil, fmt.Errorf("the generated file does not load: %w", err)
	}
	return b, nil
}

// allowEntry is one line of the allowlist: the model ID to include — the key
// the file will carry, which is the route's own ID — and, for a route that
// serves a Claude model, the canonical ID of the same model. The pair is the
// canonical-to-route map's row (plan Task 1b.2 step 1): a pin carries the
// canonical ID and the route is sent and charged the key.
type allowEntry struct {
	id        string
	canonical string
}

// readAllowlist reads the models to include: one per line, "<model id>" or
// "<model id> <canonical id>", blank lines and lines starting with '#'
// ignored. A duplicate key, or two keys claiming one canonical ID, is an
// error (either would leave the file depending on which line came last), and
// the result is sorted, so the file does not depend on the list's order.
//
// A Claude row is the pair or nothing: a key under the anthropic/ prefix — the
// only shape a route may claim a Claude model by (pricing.IsAlias) — must carry
// the canonical ID, and a canonical ID on any other key is refused. Nothing
// derives one from the other, here or anywhere: two spellings that look alike
// are two answers to where a call goes and what it costs, which is the whole
// reason the map is checked in rather than computed (plan Task 1b.2).
func readAllowlist(path string) ([]allowEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []allowEntry
	seen := map[string]bool{}
	canonical := map[string]string{} // canonical ID -> the line that first claimed it
	for i, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) > 2 {
			return nil, fmt.Errorf("%s:%d: %s has %d fields: a line is \"<model id>\" or \"<model id> <canonical id>\"", path, i+1, strings.Join(fields, " "), len(fields))
		}
		e := allowEntry{id: fields[0]}
		if len(fields) == 2 {
			e.canonical = fields[1]
		}
		isClaude := strings.HasPrefix(e.id, "anthropic/")
		switch {
		case e.canonical == "" && isClaude:
			return nil, fmt.Errorf("%s:%d: %s is a Claude row with no canonical ID: write the direct spelling of the same model beside it (%s claude-sonnet-5-5). A Claude model has a different ID per route and nothing derives one from the other, so a row without its pair could be neither claimed at a pin nor priced on the route", path, i+1, e.id, e.id)
		case e.canonical != "" && !isClaude:
			return nil, fmt.Errorf("%s:%d: %s carries a canonical ID, and only a Claude row does: the canonical ID is the direct spelling of a model this build routes to Anthropic, and a route's other models are priced under model_prices", path, i+1, e.id)
		case e.canonical == e.id:
			// Before the shape check below, which it would otherwise never
			// reach: the key of a Claude row begins with "anthropic/", so a
			// pair that repeats the key as its own canonical ID carries a "/"
			// and was refused as "a route's ID is not a canonical ID" — true
			// of the value, but not of what the line did, which is write one
			// spelling where two are required. This case is the one that says
			// so.
			return nil, fmt.Errorf("%s:%d: %s is its own canonical ID: the key is the route's own ID (anthropic/claude-sonnet-5.5) and the canonical ID is the direct spelling (claude-sonnet-5-5)", path, i+1, e.id)
		case e.canonical != "" && strings.ContainsAny(e.canonical, "/ *:"):
			return nil, fmt.Errorf("%s:%d: %q is not a canonical ID: it is the direct spelling a pin carries (claude-sonnet-5-5), not the route's (%s)", path, i+1, e.canonical, e.id)
		}
		if seen[e.id] {
			return nil, fmt.Errorf("%s:%d: %s appears twice", path, i+1, e.id)
		}
		seen[e.id] = true
		if e.canonical != "" {
			if prev, dup := canonical[e.canonical]; dup {
				return nil, fmt.Errorf("%s:%d: %s and %s both carry canonical ID %s: one canonical ID identifies one model, and a map that held both rows would price one pin two ways", path, i+1, prev, e.id, e.canonical)
			}
			canonical[e.canonical] = e.id
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no model ID", path)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

// source is where the three responses come from: the snapshots -from names,
// or the public API. No path here sends a credential.
type source struct{ opts options }

// get reads one of the two responses shared by every model.
func (s source) get(what string) ([]byte, error) {
	path := ""
	switch {
	case what == "models" && s.opts.models != "":
		path = s.opts.models
	case what == "zdr" && s.opts.zdr != "":
		path = s.opts.zdr
	case s.opts.from != "":
		path = filepath.Join(s.opts.from, what+".json")
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		return b, nil
	}
	url := apiBase + "/" + what
	if what == "zdr" {
		url = apiBase + "/endpoints/zdr"
	}
	return httpGet(url)
}

// endpoints reads one model's endpoint list: the snapshot named after the
// model under -from, or the live API.
func (s source) endpoints(id string) ([]endpoint, error) {
	var raw []byte
	var err error
	if s.opts.from != "" {
		name := "models_" + strings.ReplaceAll(id, "/", "_") + "_endpoints.json"
		raw, err = os.ReadFile(filepath.Join(s.opts.from, name))
	} else {
		raw, err = httpGet(apiBase + "/models/" + id + "/endpoints")
	}
	if err != nil {
		return nil, fmt.Errorf("%s endpoints: %w", id, err)
	}
	var doc endpointsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s endpoints: %w", id, err)
	}
	if len(doc.Data.Endpoints) == 0 {
		return nil, fmt.Errorf("%s is on the allowlist but the API serves no endpoint for it: a rename must not quietly shrink the file", id)
	}
	return doc.Data.Endpoints, nil
}

// httpTimeout bounds one API read. The generator is a build-time command run
// by hand: a connection that hangs must fail the run rather than hold a
// release checklist open, and there is no retry to wait for.
const httpTimeout = 30 * time.Second

// httpClient is the one client every read goes through, so the timeout above
// cannot be forgotten at a call site.
var httpClient = &http.Client{Timeout: httpTimeout}

func httpGet(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

type modelsDoc struct {
	Data []struct {
		ID      string                     `json:"id"`
		Pricing map[string]json.RawMessage `json:"pricing"`
	} `json:"data"`
}

type endpointsDoc struct {
	Data struct {
		Endpoints []endpoint `json:"endpoints"`
	} `json:"data"`
}

// endpoint is the part of one endpoint row the generator reads. Everything
// else the API returns (uptime, latency, quantization, status) is ignored:
// nothing in the design acts on it (design §10 U9).
type endpoint struct {
	Tag           string                     `json:"tag"`
	ContextLength *int64                     `json:"context_length"`
	MaxCompletion *int64                     `json:"max_completion_tokens"`
	Pricing       map[string]json.RawMessage `json:"pricing"`
}

type zdrDoc struct {
	Data []struct {
		ModelID string `json:"model_id"`
		Tag     string `json:"tag"`
	} `json:"data"`
}

// checkModels reads the model-level pricing of every allowlisted model and
// refuses an ID the API no longer serves, an ID a pin could not name
// (pricing.IsAlias: an alias, a ":" variant, or an anthropic/ prefix that is
// not the one explicit ID a route may claim), and a
// model whose pricing carries a dimension the file does not express. It is
// the whole reason the models response is read: the prices themselves come
// from the endpoints.
func checkModels(raw []byte, ids []string) error {
	var doc modelsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("models: %w", err)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, m := range doc.Data {
		byID[m.ID] = m.Pricing
	}
	for _, id := range ids {
		if pricing.IsAlias(id) {
			return fmt.Errorf("%s is not a model ID a route can claim and a pin can name (an alias, a \":\" variant or an anthropic/ prefix that is no single explicit ID): the file must not carry a key nothing can be charged for", id)
		}
		p, ok := byID[id]
		if !ok {
			return fmt.Errorf("%s is on the allowlist but the API no longer serves it: a rename must not quietly shrink the file", id)
		}
		for _, r := range refusedPricing {
			if _, ok := p[r.key]; ok {
				return fmt.Errorf("%s: its pricing carries %s (%s), which the catalog file cannot express: price it under model_prices instead, or leave it off the allowlist", id, r.what, r.key)
			}
		}
	}
	return nil
}

// parseZDR is the ZDR-eligible endpoint tags per allowlisted model, keyed
// by tag. The ZDR list carries a whole row per endpoint with its own
// pricing; the generator matches it to the model's endpoints by
// (model_id, tag) and reads the prices from the endpoints response, which
// is the same row.
func parseZDR(raw []byte, ids []string) (map[string]map[string]bool, error) {
	var doc zdrDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("zdr: %w", err)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[string]map[string]bool{}
	for _, r := range doc.Data {
		if !want[r.ModelID] {
			continue
		}
		if out[r.ModelID] == nil {
			out[r.ModelID] = map[string]bool{}
		}
		out[r.ModelID][r.Tag] = true
	}
	return out, nil
}

// buildEntry is one model's entry: the context and output minima over every
// endpoint, and a range per dimension per tier.
func buildEntry(id string, eps []endpoint, zdr map[string]bool) (catalog.Entry, error) {
	all, err := dedupe(id, eps)
	if err != nil {
		return catalog.Entry{}, err
	}
	var zdrEps []endpoint
	for _, e := range all {
		if zdr[e.Tag] {
			zdrEps = append(zdrEps, e)
		}
	}
	e := catalog.Entry{Endpoints: len(all), ZDREndpoints: len(zdrEps)}
	e.ContextTokens, e.MaxOutputTokens = minima(all)
	if e.All, err = tierBounds(id, "all", all); err != nil {
		return catalog.Entry{}, err
	}
	if len(zdrEps) > 0 {
		// A model with no ZDR-eligible endpoint gets no zdr tier at all,
		// so a require_zdr route refuses the pin rather than pricing an
		// empty set (design D9).
		if e.ZDR, err = tierBounds(id, "zdr", zdrEps); err != nil {
			return catalog.Entry{}, err
		}
	}
	return e, nil
}

// dedupe collapses endpoint rows that share a tag, sorted by tag. The API
// really does return one tag twice with identical prices (a provider
// serving the same model under one tag from two regions); two rows with the
// same tag and different prices are a hard error naming both, never a
// silent pick.
//
// The two rows of one tag are one endpoint for pricing but not for the
// limits, so their context and output limits are folded to the smaller of
// the two here rather than one row being kept whole: keeping either would
// record a limit above what the other row accepts, which is what design §4
// promises the file does not do, and would make the file depend on the
// order the response listed the rows in. The fold is a minimum, so it is
// the same whichever row came first.
func dedupe(id string, eps []endpoint) ([]endpoint, error) {
	byTag := map[string]endpoint{}
	for _, e := range eps {
		if e.Tag == "" {
			return nil, fmt.Errorf("%s: an endpoint row has no tag", id)
		}
		if prev, ok := byTag[e.Tag]; ok {
			if !samePricing(prev.Pricing, e.Pricing) {
				return nil, fmt.Errorf("%s: the endpoint tag %q appears twice with different prices: the file cannot say which one is charged", id, e.Tag)
			}
			e.ContextLength = lowerLimit(prev.ContextLength, e.ContextLength)
			e.MaxCompletion = lowerLimit(prev.MaxCompletion, e.MaxCompletion)
		}
		byTag[e.Tag] = e
	}
	out := make([]endpoint, 0, len(byTag))
	for _, t := range sortedKeys(byTag) {
		out = append(out, byTag[t])
	}
	return out, nil
}

// lowerLimit is the smaller of two limits the API reported for one endpoint
// tag, with an absent one (null: the endpoint advertises no limit) imposing
// nothing — the same reading minima applies, so a fold over duplicate rows
// cannot raise a limit either row advertised, and cannot invent one where
// neither did.
func lowerLimit(a, b *int64) *int64 {
	switch {
	case a == nil || *a <= 0:
		return b
	case b == nil || *b <= 0:
		return a
	case *b < *a:
		return b
	}
	return a
}

// samePricing compares two endpoint pricing objects by their raw JSON, with
// the keys sorted, so a reordered response is not a difference.
func samePricing(a, b map[string]json.RawMessage) bool { return canonical(a) == canonical(b) }

func canonical(m map[string]json.RawMessage) string {
	var sb strings.Builder
	for _, k := range sortedKeys(m) {
		fmt.Fprintf(&sb, "%s=%s;", k, m[k])
	}
	return sb.String()
}

// minima is the smallest context_length and max_completion_tokens over the
// endpoints, so nothing is reserved or allowed above what every endpoint
// accepts. An endpoint whose field is null advertises no limit, so it
// cannot constrain the others and is left out (design §4); 0 means no
// endpoint reported one, and pricing's defaults apply.
func minima(eps []endpoint) (context, output int64) {
	var haveC, haveO bool
	for _, e := range eps {
		if e.ContextLength != nil && *e.ContextLength > 0 && (!haveC || *e.ContextLength < context) {
			context, haveC = *e.ContextLength, true
		}
		if e.MaxCompletion != nil && *e.MaxCompletion > 0 && (!haveO || *e.MaxCompletion < output) {
			output, haveO = *e.MaxCompletion, true
		}
	}
	return context, output
}

// tierBounds is one tier's dimensions. A dimension no endpoint of the tier
// prices is left out — never written as 0 — and the compile folds it
// (design D7(a)). A dimension some endpoints price takes the extreme over
// every endpoint's contribution: the price where the endpoint publishes
// one, and its own input price times the stand-in factor where it does not,
// for both bounds. A dimension with no stand-in (web search) takes the
// extreme over the endpoints that price it. The tag at an extreme is the
// lexicographically smallest one holding it, so a tie cannot make the file
// flap between runs.
func tierBounds(id, tier string, eps []endpoint) (map[string]catalog.Bound, error) {
	out := map[string]catalog.Bound{}
	for _, d := range apiDims {
		priced := false
		for _, e := range eps {
			if _, ok, err := price(e.Pricing, d.key); err != nil {
				return nil, fmt.Errorf("%s %s: %w", id, tier, err)
			} else if ok {
				priced = true
				break
			}
		}
		if !priced {
			continue // the key is absent: the compile folds this dimension
		}
		lo, hi := bound{}, bound{}
		for _, e := range eps {
			v, ok, err := contribution(e, d)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", id, tier, err)
			}
			if !ok {
				continue // no stand-in for this dimension: no contribution
			}
			if lo.value == "" || lessDecimal(v, lo.value) {
				lo = bound{v, e.Tag}
			}
			if hi.value == "" || lessDecimal(hi.value, v) {
				hi = bound{v, e.Tag}
			}
		}
		out[d.dim] = catalog.Bound{Min: lo.value, Max: hi.value, MinOf: lo.tag, MaxOf: hi.tag}
	}
	return out, nil
}

type bound struct{ value, tag string }

// contribution is one endpoint's price for one dimension, in the file's
// unit: the published price shifted, or the stand-in — the endpoint's own
// input price times the fixed factor, shifted the same way — when it
// publishes none. false means the endpoint contributes nothing, which only
// a dimension with no stand-in can.
func contribution(e endpoint, d dim) (string, bool, error) {
	v, ok, err := price(e.Pricing, d.key)
	if err != nil {
		return "", false, err
	}
	if !ok {
		f, hasFactor := standInFactor[d.standIn]
		if !hasFactor {
			return "", false, nil
		}
		in, _, err := price(e.Pricing, "prompt")
		if err != nil {
			return "", false, err
		}
		if v, err = scaleBy(in, f.num, f.den); err != nil {
			return "", false, fmt.Errorf("%s stand-in: %w", d.dim, err)
		}
	}
	if v, err = shiftDecimal(v, d.places); err != nil {
		return "", false, fmt.Errorf("%s: %w", d.dim, err)
	}
	return v, true, nil
}

// price reads one pricing field as the decimal string the API publishes.
// The field's absence is ("", false, nil): a dimension an endpoint does not
// price, which is the normal case, not an error.
func price(p map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := p[key]
	if !ok {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, fmt.Errorf("pricing.%s: %w", key, err)
	}
	return s, true, nil
}

// reportDiff writes what changed between the file on disk and the one just
// generated: the models added and removed, each model's endpoint counts,
// the per-dimension extremes that moved, and the two checked_at dates. It
// is the review surface the script prints (design §5).
func reportDiff(w io.Writer, oldRaw, newRaw []byte) {
	var old, new catalog.File
	if err := json.Unmarshal(oldRaw, &old); err != nil {
		fmt.Fprintf(w, "the previous file is unreadable (%v); nothing to compare\n", err)
		return
	}
	if err := json.Unmarshal(newRaw, &new); err != nil {
		fmt.Fprintf(w, "the new file is unreadable (%v)\n", err)
		return
	}
	if old.CheckedAt != new.CheckedAt {
		fmt.Fprintf(w, "checked_at %s -> %s\n", old.CheckedAt, new.CheckedAt)
	}
	added, removed := diffKeys(old.Models, new.Models)
	for _, id := range added {
		fmt.Fprintf(w, "+ %s (%d endpoints, %d zdr)\n", id, new.Models[id].Endpoints, new.Models[id].ZDREndpoints)
	}
	for _, id := range removed {
		fmt.Fprintf(w, "- %s\n", id)
	}
	for _, id := range sortedKeys(new.Models) {
		o, ok := old.Models[id]
		if !ok {
			continue
		}
		n := new.Models[id]
		if o.Endpoints != n.Endpoints || o.ZDREndpoints != n.ZDREndpoints ||
			o.ContextTokens != n.ContextTokens || o.MaxOutputTokens != n.MaxOutputTokens {
			fmt.Fprintf(w, "~ %s: endpoints %d->%d zdr %d->%d context %d->%d output %d->%d\n",
				id, o.Endpoints, n.Endpoints, o.ZDREndpoints, n.ZDREndpoints,
				o.ContextTokens, n.ContextTokens, o.MaxOutputTokens, n.MaxOutputTokens)
		}
		for _, tier := range []string{"all", "zdr"} {
			for _, d := range catalog.Dimensions {
				ob, ook := tierOf(o, tier)[d]
				nb, nok := tierOf(n, tier)[d]
				switch {
				case ook && !nok:
					fmt.Fprintf(w, "~ %s %s/%s: dropped\n", id, tier, d)
				case !ook && nok:
					fmt.Fprintf(w, "~ %s %s/%s: %s..%s\n", id, tier, d, nb.Min, nb.Max)
				case ook && nok && (ob.Min != nb.Min || ob.Max != nb.Max):
					fmt.Fprintf(w, "~ %s %s/%s: %s..%s -> %s..%s\n", id, tier, d, ob.Min, ob.Max, nb.Min, nb.Max)
				}
			}
		}
	}
	if len(added)+len(removed) == 0 {
		fmt.Fprintf(w, "%d models, no model added or removed\n", len(new.Models))
	}
}

// tierOf is the named tier's dimensions, nil when the model has no such
// tier.
func tierOf(e catalog.Entry, name string) map[string]catalog.Bound {
	if name == "zdr" {
		return e.ZDR
	}
	return e.All
}

func diffKeys[T any](old, new map[string]T) (added, removed []string) {
	for _, k := range sortedKeys(new) {
		if _, ok := old[k]; !ok {
			added = append(added, k)
		}
	}
	for _, k := range sortedKeys(old) {
		if _, ok := new[k]; !ok {
			removed = append(removed, k)
		}
	}
	return added, removed
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
