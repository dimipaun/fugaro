package budget

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The fixture catalog. Its rates are the ones the Cloud Build pricing page's
// indicative numbers imply for the E2 family — $0.0015 per vCPU-minute and
// $0.00045 per GiB-minute — so the arithmetic this file freezes is the
// arithmetic the spike's $0.936 upper bound for one e2-highcpu-8 build comes
// from (billing-price-spike.md §5, §6 amendment 2). They are the test's, never
// a runtime default: the lookup reads every number from the catalog.
const (
	fixtureCPUMicros = Micros(1_500)
	fixtureRAMMicros = Micros(450)
)

func fixtureSKU(id, desc, unit, currency string, micros Micros, regions ...string) BuildSKU {
	return BuildSKU{ID: id, Description: desc, Unit: unit, Currency: currency, Regions: regions, MicrosPerUnit: micros}
}

// fixtureBook is one region's E2 rows, plus the two shapes the parser must not
// take for a build's price: a private-pool row of the same family and region
// (Fugaro submits to the default pool, so its rate is not the build's) and a
// egress row (a unit the parser does not know).
func fixtureBook() PriceBook {
	return PriceBook{
		ServiceID: BuildBillingServiceID,
		ReadAt:    time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
		SKUs: []BuildSKU{
			fixtureSKU("SKU-E2-CPU-USC1", "E2 CPU utilization per minute in us-central1", "min", "USD", fixtureCPUMicros, "us-central1"),
			fixtureSKU("SKU-E2-RAM-USC1", "E2 ram utilization per minute in us-central1", "min", "USD", fixtureRAMMicros, "us-central1"),
			fixtureSKU("SKU-E2-CPU-EUW1", "E2 CPU utilization per minute in europe-west1", "min", "USD", fixtureCPUMicros, "europe-west1"),
			fixtureSKU("SKU-E2-RAM-EUW1", "E2 ram utilization per minute in europe-west1", "min", "USD", fixtureRAMMicros, "europe-west1"),
			fixtureSKU("SKU-E2-POOL-USC1", "E2 CPU utilization per minute in us-central1 (Private Pool)", "min", "USD", fixtureCPUMicros, "us-central1"),
			fixtureSKU("SKU-EGRESS", "Network Internet Data Transfer Out from Americas to EMEA", "GiBy", "USD", Micros(120_000), "us-central1"),
		},
	}
}

// TestBuildCostIsAnUpperBound freezes the mapping and the arithmetic: the row
// each Cloud Build machine type resolves to, the SKU ids it is priced from, and
// the estimate being the fixed build timeout times that rate (D1543 — the
// estimator is the timeout times the machine rate, and the free tier is
// deliberately not subtracted, which is what keeps it an upper bound).
func TestBuildCostIsAnUpperBound(t *testing.T) {
	book := fixtureBook()
	rate, err := BuildCost(book, "E2_HIGHCPU_8", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	wantMinute := Micros(8)*fixtureCPUMicros + Micros(8)*fixtureRAMMicros
	if rate.MicrosPerMinute != wantMinute {
		t.Errorf("E2_HIGHCPU_8 in us-central1: %d µ$/min, want %d (8 vCPU and 8 GiB of the E2 rows)", rate.MicrosPerMinute, wantMinute)
	}
	if got := rate.Estimate(); got != 60*wantMinute {
		t.Errorf("estimate = %d µ$, want %d: the fixed %d s timeout is 60 billed minutes", got, 60*wantMinute, MaxBuildSeconds)
	}
	if got := rate.Estimate().USD(); math.Abs(got-0.936) > 0.0005 {
		t.Errorf("estimate = $%.4f, want $0.936 (billing-price-spike.md §5's indicative upper bound for one e2-highcpu-8 build)", got)
	}
	if len(rate.SKUs) != 2 || rate.SKUs[0] != "SKU-E2-CPU-USC1" || rate.SKUs[1] != "SKU-E2-RAM-USC1" {
		t.Errorf("the rate was priced from %v, want the default pool's CPU and RAM rows", rate.SKUs)
	}
	if !rate.ReadAt.Equal(book.ReadAt) {
		t.Errorf("ReadAt = %v, want the book's %v: the lease records when the price was read", rate.ReadAt, book.ReadAt)
	}

	// The n1 aliases are the same machine at the same price (spike §5).
	alias, err := BuildCost(book, "n1-highcpu-8", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	if alias.MicrosPerMinute != rate.MicrosPerMinute || alias.MachineType != "E2_HIGHCPU_8" {
		t.Errorf("n1-highcpu-8 = %d µ$/min as %q, want the e2-highcpu-8 rate %d as %q", alias.MicrosPerMinute, alias.MachineType, rate.MicrosPerMinute, rate.MachineType)
	}

	// The region is part of the lookup: the same type elsewhere is that
	// region's rate, and a build in europe-west1 is not priced at us-central1's.
	if _, err := BuildCost(book, "E2_HIGHCPU_8", "europe-west1"); err != nil {
		t.Fatalf("europe-west1: %v", err)
	}

	// A partial minute is never free, and no duration costs more than the
	// timeout the build is submitted with.
	if got := rate.EstimateFor(0); got != 0 {
		t.Errorf("EstimateFor(0) = %d, want 0", got)
	}
	if got := rate.EstimateFor(time.Second); got <= 0 || got > rate.MicrosPerMinute {
		t.Errorf("EstimateFor(1s) = %d, want a positive fraction of a minute's rate", got)
	}
	if got := rate.EstimateFor(MaxBuildSeconds * time.Second); got != rate.Estimate() {
		t.Errorf("EstimateFor(%ds) = %d, want the estimate %d", MaxBuildSeconds, got, rate.Estimate())
	}
	// Saturation: a rate no database can hold must not wrap into headroom.
	huge := BuildRate{MicrosPerMinute: Micros(math.MaxInt64)}
	if got := huge.Estimate(); got != Micros(math.MaxInt64) {
		t.Errorf("a saturating estimate = %d, want %d", got, int64(math.MaxInt64))
	}
}

// TestBuildMachineTableIsFrozen pins the reviewed table: every row's shape, and
// that a type that is not a row is not in it. A row added or a shape changed
// without this test's word is the silent mapping bug that under-estimates.
func TestBuildMachineTableIsFrozen(t *testing.T) {
	want := map[string]BuildMachine{
		"E2_HIGHCPU_8":  {Enum: "E2_HIGHCPU_8", Family: "E2", VCPU: 8, GiB: 8},
		"E2_HIGHCPU_32": {Enum: "E2_HIGHCPU_32", Family: "E2", VCPU: 32, GiB: 32},
		"E2_MEDIUM":     {Enum: "E2_MEDIUM", Family: "E2", VCPU: 2, GiB: 4},
		"E2_STANDARD_2": {Enum: "E2_STANDARD_2", Family: "E2", VCPU: 2, GiB: 8},
		"N1_HIGHCPU_8":  {Enum: "E2_HIGHCPU_8", Family: "E2", VCPU: 8, GiB: 8},
		"N1_HIGHCPU_32": {Enum: "E2_HIGHCPU_32", Family: "E2", VCPU: 32, GiB: 32},
	}
	for spelling, shape := range want {
		got, ok := BuildMachineShape(spelling)
		if !ok {
			t.Errorf("%s: not in the table", spelling)
			continue
		}
		if got != shape {
			t.Errorf("%s = %+v, want %+v", spelling, got, shape)
		}
	}
	for _, hull := range []string{"", "N2_HIGHCPU_8", "E2_HIGHCPU_64", "whatever"} {
		if shape, ok := BuildMachineShape(hull); ok {
			t.Errorf("%q resolved to %+v: a machine type the table does not carry must refuse, never default", hull, shape)
		}
	}
}

// TestUnknownBillingPriceRefuses is the owner's rule: a missing or unknown
// price refuses, never defaults (D1550, spike §4's unknown-or-unmapped row).
// Every case here is a refusal, and none of them may fall back to a number this
// binary or the local config carries.
func TestUnknownBillingPriceRefuses(t *testing.T) {
	book := fixtureBook()
	drop := func(id string) PriceBook {
		b := book
		var kept []BuildSKU
		for _, s := range b.SKUs {
			if s.ID != id {
				kept = append(kept, s)
			}
		}
		b.SKUs = kept
		return b
	}
	edit := func(id string, f func(*BuildSKU)) PriceBook {
		b := book
		b.SKUs = append([]BuildSKU(nil), b.SKUs...)
		for i := range b.SKUs {
			if b.SKUs[i].ID == id {
				f(&b.SKUs[i])
			}
		}
		return b
	}
	cases := []struct {
		name        string
		book        PriceBook
		machine     string
		region      string
		wantsInText []string
	}{
		{"an unmapped machine type", book, "N2_HIGHCPU_8", "us-central1", []string{"N2_HIGHCPU_8", "no price"}},
		{"a region with no rows", book, "E2_HIGHCPU_8", "us-east9", []string{"us-east9"}},
		{"a missing RAM row", drop("SKU-E2-RAM-USC1"), "E2_HIGHCPU_8", "us-central1", []string{"E2_HIGHCPU_8"}},
		{"no SKUs at all", PriceBook{ServiceID: BuildBillingServiceID}, "E2_HIGHCPU_8", "us-central1", []string{"E2_HIGHCPU_8"}},
		{"a zero price", edit("SKU-E2-CPU-USC1", func(s *BuildSKU) { s.MicrosPerUnit = 0 }), "E2_HIGHCPU_8", "us-central1", []string{"SKU-E2-CPU-USC1"}},
		{"an unknown unit", edit("SKU-E2-CPU-USC1", func(s *BuildSKU) { s.Unit = "GiBy" }), "E2_HIGHCPU_8", "us-central1", []string{"E2_HIGHCPU_8"}},
		{"another currency", edit("SKU-E2-CPU-USC1", func(s *BuildSKU) { s.Currency = "EUR" }), "E2_HIGHCPU_8", "us-central1", []string{"E2_HIGHCPU_8"}},
		{"an empty description", edit("SKU-E2-CPU-USC1", func(s *BuildSKU) { s.Description = "" }), "E2_HIGHCPU_8", "us-central1", []string{"SKU-E2-CPU-USC1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rate, err := BuildCost(c.book, c.machine, c.region)
			if err == nil {
				t.Fatalf("priced %q as %d µ$/min: a missing or unknown price must refuse", c.machine, rate.MicrosPerMinute)
			}
			var noPrice *NoPriceError
			if !errors.As(err, &noPrice) {
				t.Fatalf("error %v is not a *NoPriceError", err)
			}
			for _, want := range c.wantsInText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not name %q", err, want)
				}
			}
			if !strings.Contains(err.Error(), "no price") || !strings.Contains(err.Error(), "fugaro.yaml") {
				t.Errorf("the refusal %q must say no price was found and that no configuration supplies one", err)
			}
		})
	}
}

// TestAmbiguousPriceRefuses: two rows of the same family, resource and region —
// neither a pool's and neither unknown — is not a price the lookup may pick
// from. It refuses rather than guessing the cheaper one.
func TestAmbiguousPriceRefuses(t *testing.T) {
	book := fixtureBook()
	book.SKUs = append(book.SKUs, fixtureSKU("SKU-E2-CPU-USC1-B", "E2 CPU utilization per minute in us-central1", "min", "USD", Micros(9_999), "us-central1"))
	if rate, err := BuildCost(book, "E2_HIGHCPU_8", "us-central1"); err == nil {
		t.Fatalf("two rows for one resource priced %d µ$/min", rate.MicrosPerMinute)
	}
}

// TestPricerReadsTheCatalogOncePerCommand: a command that submits several
// builds reads the catalog once and reuses it (spike §4's cache rule — one
// lookup per invocation, in memory, never persisted). A failed lookup is held
// too: the command refuses rather than reading again mid-way or guessing.
func TestPricerReadsTheCatalogOncePerCommand(t *testing.T) {
	var reads atomic.Int32
	p := &Pricer{read: func(context.Context) (PriceBook, error) {
		reads.Add(1)
		return fixtureBook(), nil
	}}
	for _, region := range []string{"us-central1", "us-central1", "europe-west1"} {
		if _, err := p.Rate(context.Background(), "E2_HIGHCPU_8", region); err != nil {
			t.Fatal(err)
		}
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("the catalog was read %d times, want once per command", got)
	}

	var failed atomic.Int32
	boom := errors.New("the catalog is unreachable")
	q := &Pricer{read: func(context.Context) (PriceBook, error) {
		failed.Add(1)
		return PriceBook{}, boom
	}}
	for i := 0; i < 2; i++ {
		if _, err := q.Rate(context.Background(), "E2_HIGHCPU_8", "us-central1"); !errors.Is(err, boom) {
			t.Fatalf("read %d: %v, want the read's own failure", i, err)
		}
	}
	if got := failed.Load(); got != 1 {
		t.Errorf("a failed lookup was retried %d times", got)
	}
}

// TestCatalogQuotaProjectSplitsByCredential is spike §6 amendment 3: the client
// names a quota project for a person's ADC and none at all for a service
// account's, and GOOGLE_CLOUD_QUOTA_PROJECT changes neither answer (the
// library's own fallback would let it supply one for a service account, which
// would put serviceusage.services.use back in play for the job).
func TestCatalogQuotaProjectSplitsByCredential(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_QUOTA_PROJECT", "another-project")

	user, err := catalogClientFor(CatalogCreds{}, "aurora-fp")
	if err != nil {
		t.Fatal(err)
	}
	if user.QuotaProject != "aurora-fp" || user.Explicit {
		t.Errorf("a person's ADC got %+v, want the installation project named and no explicit client", user)
	}
	sa, err := catalogClientFor(CatalogCreds{ServiceAccount: true}, "aurora-fp")
	if err != nil {
		t.Fatal(err)
	}
	if sa.QuotaProject != "" || !sa.Explicit {
		t.Errorf("a service account got %+v, want no quota project and an explicit client", sa)
	}

	// Where the backend is off there is nothing to reserve, so the user path
	// has no project to name and the lookup is not constructed at all
	// (spike §1.2: the remedy is the flag, never a grant).
	if _, err := catalogClientFor(CatalogCreds{}, ""); !errors.Is(err, ErrCatalogNoQuotaProject) {
		t.Fatalf("a user lookup without an installation project: %v, want ErrCatalogNoQuotaProject", err)
	}
	if _, err := catalogClientFor(CatalogCreds{ServiceAccount: true}, ""); err != nil {
		t.Errorf("a service account's lookup needs no quota project, got %v", err)
	}
}

// TestReadBuildCatalogNamesTheServiceAndSendsNoQuotaProjectForAServiceAccount
// reads the generated client through a fake Cloud Billing endpoint: the pinned
// serviceId is the one requested, a nextPageToken is followed, and a
// service-account lookup sends no X-Goog-User-Project header even with the
// environment variable set.
func TestReadBuildCatalogNamesTheServiceAndSendsNoQuotaProjectForAServiceAccount(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_QUOTA_PROJECT", "another-project")
	var (
		paths  []string
		header []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		header = append(header, r.Header.Get("X-Goog-User-Project"))
		if !strings.Contains(r.URL.Path, "/v1/services/"+BuildBillingServiceID+"/skus") {
			t.Errorf("the lookup asked for %q, want the pinned Cloud Build service %s", r.URL.Path, BuildBillingServiceID)
		}
		var page struct {
			Skus          []map[string]any `json:"skus"`
			NextPageToken string           `json:"nextPageToken"`
		}
		if r.URL.Query().Get("pageToken") == "" {
			page.Skus = []map[string]any{skuJSON("SKU-E2-CPU-USC1", "E2 CPU utilization per minute in us-central1", "min", "USD", Micros(1_500), "us-central1")}
			page.NextPageToken = "page-2"
		} else {
			page.Skus = []map[string]any{skuJSON("SKU-E2-RAM-USC1", "E2 ram utilization per minute in us-central1", "min", "USD", Micros(450), "us-central1")}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()

	book, err := ReadBuildCatalog(context.Background(), "aurora-fp", CatalogCreds{
		ServiceAccount: true,
		Endpoint:       srv.URL,
		HTTPClient:     srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if book.ServiceID != BuildBillingServiceID {
		t.Errorf("ServiceID = %q, want %q", book.ServiceID, BuildBillingServiceID)
	}
	if len(book.SKUs) != 2 {
		t.Fatalf("read %d SKUs, want both pages: %+v", len(book.SKUs), book.SKUs)
	}
	if len(paths) != 2 {
		t.Fatalf("the lookup made %d requests, want 2 (the second page follows nextPageToken): %v", len(paths), paths)
	}
	for i, h := range header {
		if h != "" {
			t.Errorf("request %d carried X-Goog-User-Project %q: a service-account lookup must name no project (spike §1.2)", i, h)
		}
	}
	rate, err := BuildCost(book, "E2_HIGHCPU_8", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	if rate.Estimate() != 60*(Micros(8)*1_500+Micros(8)*450) {
		t.Errorf("the read book priced a build at %d µ$", rate.Estimate())
	}
}

// skuJSON is one SKU in the API's own shape: pricingInfo[0].pricingExpression
// with tieredRates[0].unitPrice, the fields the spike's probe read. Money
// carries whole units as a string and the fraction as nanos, so a micro-dollar
// amount is split the way the API would split it.
func skuJSON(id, desc, unit, currency string, micros Micros, regions ...string) map[string]any {
	return map[string]any{
		"skuId":          id,
		"description":    desc,
		"serviceRegions": regions,
		"pricingInfo": []map[string]any{{
			"pricingExpression": map[string]any{
				"usageUnit": unit,
				"tieredRates": []map[string]any{{
					"unitPrice": map[string]any{
						"currencyCode": currency,
						"units":        strconv.FormatInt(int64(micros)/1_000_000, 10),
						"nanos":        (int64(micros) % 1_000_000) * 1_000,
					},
				}},
			},
		}},
	}
}
