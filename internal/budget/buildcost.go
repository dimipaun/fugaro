package budget

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"golang.org/x/oauth2/google"
	cloudbilling "google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/option"
)

// The image-build estimator (D1543, D1550): what one Cloud Build submission
// can cost at most, and where the number comes from.
//
// The price is read from the Cloud Billing Catalog API at run time, on the
// caller's own credentials, and a missing or unknown price refuses — never a
// default, never a number the local or published config carries, because a
// rate read from config is a rate an agent sets and the cap stops meaning
// anything (billing-price-spike.md §4, §5).
//
// The estimate is the build's fixed timeout times the machine's rate, which is
// an upper bound: every Fugaro build is submitted with the same timeout
// (internal/backend/gcp/build.go), the machine type and region are the ones
// that go into the request, and Cloud Build's monthly free tier is
// deliberately not subtracted because Fugaro cannot know how much of it is
// left. A build that finishes early costs less than it reserved; the lease
// settles on what it really spent.

const (
	// MaxBuildSeconds is the timeout every Fugaro build is submitted with
	// (internal/backend/gcp/build.go:239, "3600s"). It is a constant of the
	// backend, not a knob: the estimate is this many seconds at the machine's
	// rate, so a build cannot outrun what it reserved.
	MaxBuildSeconds = 3600

	// MaxBuildQueueSeconds is how long a submitted build may wait for a worker
	// before Cloud Build starts it — the queue TTL every Fugaro build carries
	// (internal/backend/gcp/build.go:240, "3600s", pinned equal by a test, and
	// set rather than left to Cloud Build's own default). The timeout above
	// starts when the build starts, so a queued build bills nothing yet and can
	// still bill for this long after it does start: the estimate does not cover
	// it, the lease's deadline does (BuildLeaseDeadline).
	MaxBuildQueueSeconds = 3600

	// BuildBillingServiceID is Cloud Build's serviceId in the public Cloud
	// Billing catalog, pinned because the estimator wants one service's rows
	// and nothing else, and the id is what makes one request enough (read
	// 2026-11-08, billing-price-spike.md §1.5).
	BuildBillingServiceID = "8B5D-EF7D-EB12"
)

// BuildMachine is one Cloud Build machine type's shape: the family whose SKUs
// price it, and its vCPU and memory, which is all the estimator needs.
//
// The rate is a function of the family and the region, not of the enum: the
// catalog prices "E2 CPU utilization" and "E2 ram utilization" per minute, and
// a machine's minute is its vCPUs and GiB at those two rates. That is the
// reading the spike's indicative numbers agree with (an e2-highcpu-8 build is
// $0.936 at $0.0015 per vCPU-minute and $0.00045 per GiB-minute, §5) and it is
// the reading that never under-estimates: a machine whose shape this table got
// wrong would be the one way the design reserves too little, which is why the
// table is a reviewed constant with a test that freezes it.
type BuildMachine struct {
	Enum   string // the enum the build is submitted with (E2_HIGHCPU_8)
	Family string // the SKU family that prices it (E2)
	VCPU   int64
	GiB    int64
}

// buildMachines is the reviewed table. A machine type that is not in it
// refuses: a type nobody mapped is a type nobody priced.
//
// The n1-highcpu-* rows are Cloud Build's aliases at the same price as the
// e2-highcpu-* machines (spike §5), so both spellings resolve to the E2 row.
// The shapes are the machine types' published vCPU and memory.
var buildMachines = map[string]BuildMachine{
	"E2_HIGHCPU_8":  {Enum: "E2_HIGHCPU_8", Family: "E2", VCPU: 8, GiB: 8},
	"E2_HIGHCPU_32": {Enum: "E2_HIGHCPU_32", Family: "E2", VCPU: 32, GiB: 32},
	"E2_MEDIUM":     {Enum: "E2_MEDIUM", Family: "E2", VCPU: 2, GiB: 4},
	"E2_STANDARD_2": {Enum: "E2_STANDARD_2", Family: "E2", VCPU: 2, GiB: 8},
	// The aliases: same machine, same price, the name Cloud Build still offers.
	"N1_HIGHCPU_8":  {Enum: "E2_HIGHCPU_8", Family: "E2", VCPU: 8, GiB: 8},
	"N1_HIGHCPU_32": {Enum: "E2_HIGHCPU_32", Family: "E2", VCPU: 32, GiB: 32},
}

// BuildMachineShape is the table entry for a machine type as a config spells
// it, or ok=false for one the table does not carry. A build config writes the
// enum (E2_HIGHCPU_8) while the pricing page writes e2-highcpu-8; the same
// machine is the same rate either way, so the two spellings are normalized to
// one lookup key and nothing else is.
func BuildMachineShape(machineType string) (BuildMachine, bool) {
	m, ok := buildMachines[normalizeMachineType(machineType)]
	return m, ok
}

func normalizeMachineType(machineType string) string {
	b := []byte(machineType)
	for i := range b {
		switch {
		case b[i] >= 'a' && b[i] <= 'z':
			b[i] -= 'a' - 'A'
		case b[i] == '-':
			b[i] = '_'
		}
	}
	return string(b)
}

// BuildSKU is one catalog row, as far as the estimator reads it.
type BuildSKU struct {
	ID            string
	Description   string
	Unit          string // pricingExpression.usageUnit: "min" for machine time
	Currency      string
	Regions       []string
	MicrosPerUnit Micros // zero: the row carries no price the estimator may use
	Unpriced      string // why, where the row itself is the reason: the refusal quotes it
}

// PriceBook is one read of the catalog: the Cloud Build SKUs and when they
// were read. The read time is recorded in the lease so a later price change
// cannot rewrite what was reserved.
type PriceBook struct {
	ServiceID string
	ReadAt    time.Time
	SKUs      []BuildSKU
}

// BuildRate is one machine type's rate in one region, and the rows it came
// from.
type BuildRate struct {
	MachineType     string
	Region          string
	MicrosPerMinute Micros
	SKUs            []string
	ReadAt          time.Time
}

// Estimate is the most a build of this machine type, submitted with the fixed
// timeout, can cost.
func (r BuildRate) Estimate() Micros {
	return r.EstimateFor(time.Duration(MaxBuildSeconds) * time.Second)
}

// EstimateFor is the same for a build that is allowed to run for d. Cloud
// Build bills partial minutes at the seconds consumed, so a partial minute is
// charged as a whole one here: rounding up is what keeps the number an upper
// bound.
func (r BuildRate) EstimateFor(d time.Duration) Micros {
	if d <= 0 || r.MicrosPerMinute <= 0 {
		return 0
	}
	seconds := int64((d + time.Second - 1) / time.Second)
	minutes := Micros((seconds + 59) / 60)
	return satMul(minutes, r.MicrosPerMinute)
}

// NoPriceError is a refusal: the catalog answered, and nothing in it prices
// this build. It carries what was asked for and what was found, because the
// fix is a reviewed table entry, not a configuration value.
type NoPriceError struct {
	MachineType string
	Region      string
	Resource    string   // the SKU resource that could not be priced ("" for an unmapped machine type)
	Why         string   // what was wrong
	Rows        []string // the SKU ids priced in this region, for the reader
}

func (e *NoPriceError) Error() string {
	msg := fmt.Sprintf("no price for a %s build in %s: %s", e.MachineType, e.Region, e.Why)
	if len(e.Rows) > 0 {
		msg += "; the rows priced in this region are " + joinIDs(e.Rows)
	}
	return msg + ": the estimator reads the Cloud Billing catalog at run time and neither fugaro.yaml nor any other configuration may supply a rate"
}

// joinIDs renders up to maxHintedRows SKU ids.
func joinIDs(ids []string) string {
	const maxHintedRows = 8
	shown := ids
	if len(shown) > maxHintedRows {
		shown = shown[:maxHintedRows]
	}
	out := ""
	for i, id := range shown {
		if i > 0 {
			out += ", "
		}
		out += id
	}
	if len(ids) > len(shown) {
		out += fmt.Sprintf(" (and %d more)", len(ids)-len(shown))
	}
	return out
}

// BuildCost prices one build from a read catalog: the machine type's rate per
// minute in the region the build is submitted to. The machine type, the region
// and the rows' unit and currency are all read here, and anything the
// estimator does not recognize refuses.
func BuildCost(b PriceBook, machineType, region string) (BuildRate, error) {
	shape, ok := BuildMachineShape(machineType)
	if !ok {
		return BuildRate{}, &NoPriceError{MachineType: machineType, Region: region,
			Why: "the machine type is not a row of the estimator's table, so nothing maps it to a priced machine", Rows: regionRows(b, region)}
	}
	if region == "" {
		return BuildRate{}, &NoPriceError{MachineType: shape.Enum, Region: region,
			Why: "no region was given, and the rate is regional"}
	}
	cpu, err := buildResource(b, shape, "CPU", region)
	if err != nil {
		return BuildRate{}, err
	}
	ram, err := buildResource(b, shape, "ram", region)
	if err != nil {
		return BuildRate{}, err
	}
	return BuildRate{
		MachineType:     shape.Enum,
		Region:          region,
		MicrosPerMinute: satAdd(satMul(Micros(shape.VCPU), cpu.price), satMul(Micros(shape.GiB), ram.price)),
		SKUs:            []string{cpu.sku.ID, ram.sku.ID},
		ReadAt:          b.ReadAt,
	}, nil
}

// pricedRow is one resource's row and the price taken from it.
type pricedRow struct {
	sku   BuildSKU
	price Micros
}

// buildResource finds the one row that prices one resource of one family in
// one region. The description is matched exactly — the family, the resource,
// "utilization per minute" and the region are all in that sentence (spike
// §1.5) — so a row about another family, another resource, another region or
// a private pool is not a match, and a row whose shape moved refuses rather
// than being read for a number it may no longer mean.
func buildResource(b PriceBook, shape BuildMachine, resource, region string) (pricedRow, error) {
	want := shape.Family + " " + resource + " utilization per minute in " + region
	var found []BuildSKU
	for _, sku := range b.SKUs {
		if sku.Description == want {
			found = append(found, sku)
		}
	}
	no := func(why string, rows []string) (pricedRow, error) {
		return pricedRow{}, &NoPriceError{MachineType: shape.Enum, Region: region, Resource: resource, Why: why, Rows: rows}
	}
	switch {
	case len(found) == 0:
		return no("no row's description is "+strconv.Quote(want), regionRows(b, region))
	case len(found) > 1:
		ids := make([]string, 0, len(found))
		for _, sku := range found {
			ids = append(ids, sku.ID)
		}
		return no(fmt.Sprintf("%d rows describe %s, so which one prices it is not something the estimator may guess", len(found), strconv.Quote(want)), ids)
	}
	sku := found[0]
	switch {
	case sku.Unit != "min":
		return no(fmt.Sprintf("the row's usage unit is %q, not the billed minute", sku.Unit), []string{sku.ID})
	case sku.Unpriced != "":
		return no(sku.Unpriced, []string{sku.ID})
	case sku.Currency != "USD":
		return no(fmt.Sprintf("the row is priced in %q", sku.Currency), []string{sku.ID})
	case sku.MicrosPerUnit <= 0:
		return no("the row carries no price", []string{sku.ID})
	}
	return pricedRow{sku: sku, price: sku.MicrosPerUnit}, nil
}

// regionRows is the SKU ids priced in a region, for a refusal to name: the
// reader is looking for the row that moved or was never there.
func regionRows(b PriceBook, region string) []string {
	var ids []string
	for _, sku := range b.SKUs {
		if slices.Contains(sku.Regions, region) {
			ids = append(ids, sku.ID)
		}
	}
	return ids
}

// CatalogCreds is how the lookup authenticates: the caller's own credentials,
// and which kind they are, because the two kinds ask the Cloud Billing API for
// different things (spike §1.2, §6 amendment 3).
type CatalogCreds struct {
	// ServiceAccount is true when the caller is not a person: a job's
	// service account (the catalog check job) or any other non-user identity.
	ServiceAccount bool
	// Endpoint overrides the API's root; tests point it at a fake.
	Endpoint string
	// HTTPClient is an established client to use instead of building one;
	// tests supply theirs. A client given here is used as is, which is also
	// what keeps a service-account lookup from resolving a quota project.
	HTTPClient *http.Client
	// NoAuth makes the client send no credentials at all. It is for tests:
	// nothing but a fake answers an unauthenticated catalog read.
	NoAuth bool
}

// ErrCatalogNoQuotaProject is a person's lookup with no installation project
// to name: there is no backend to reserve against, so the lookup does not run
// (spike §1.2, the repo's own early return).
var ErrCatalogNoQuotaProject = errors.New("no installation project to name as the price lookup's quota project")

// catalogClient is the credential split as a value, so the rule can be read
// and tested without credentials: the quota project the lookup names ("" for
// none) and whether it brings an explicit client instead.
type catalogClient struct {
	QuotaProject string
	Explicit     bool
}

// catalogClientFor decides the split.
//
// A person's ADC names the installation project: a user credential has no
// project of its own, and the API needs one. A service account names none at
// all — the request is then billed to the account's own project, which is
// where the API is enabled, and serviceusage.services.use never enters the
// picture. It gets an explicit client instead of a quota project, which is
// what stops the library's own fallback (GOOGLE_CLOUD_QUOTA_PROJECT, then a
// quota_project_id in a credentials file) from supplying one behind the
// caller's back: with a client of its own the library resolves no quota
// project at all.
//
// The environment variable changes neither answer, which is the property T8
// pins with a test: it is read by neither branch.
func catalogClientFor(c CatalogCreds, project string) (catalogClient, error) {
	if c.ServiceAccount {
		return catalogClient{Explicit: true}, nil
	}
	if project == "" {
		return catalogClient{}, fmt.Errorf("%w: a price lookup on a person's credentials names the installation project, and there is none — the budget backend is off, so there is nothing to reserve and nothing to look up (re-run fugaro update --firebase-project <project>)", ErrCatalogNoQuotaProject)
	}
	return catalogClient{QuotaProject: project}, nil
}

// ReadBuildCatalog reads Cloud Build's rows from the public Cloud Billing
// catalog, once, on the caller's own credentials. A nextPageToken is followed
// when one arrives (today the whole catalog is one page, spike §1.5).
func ReadBuildCatalog(ctx context.Context, project string, c CatalogCreds) (PriceBook, error) {
	cc, err := catalogClientFor(c, project)
	if err != nil {
		return PriceBook{}, err
	}
	opts := []option.ClientOption{}
	if c.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(c.Endpoint))
	}
	if c.NoAuth {
		opts = append(opts, option.WithoutAuthentication())
	}
	switch {
	case c.HTTPClient != nil:
		opts = append(opts, option.WithHTTPClient(c.HTTPClient))
	case cc.Explicit:
		hc, err := google.DefaultClient(ctx, cloudbilling.CloudPlatformScope)
		if err != nil {
			return PriceBook{}, fmt.Errorf("building the catalog read's client: %w", err)
		}
		opts = append(opts, option.WithHTTPClient(hc))
	}
	if cc.QuotaProject != "" {
		opts = append(opts, option.WithQuotaProject(cc.QuotaProject))
	}
	svc, err := cloudbilling.NewService(ctx, opts...)
	if err != nil {
		return PriceBook{}, fmt.Errorf("opening the Cloud Billing catalog: %w", err)
	}
	book := PriceBook{ServiceID: BuildBillingServiceID, ReadAt: time.Now()}
	err = svc.Services.Skus.List("services/"+BuildBillingServiceID).
		PageSize(catalogPageSize).
		Pages(ctx, func(page *cloudbilling.ListSkusResponse) error {
			for _, sku := range page.Skus {
				book.SKUs = append(book.SKUs, readSKU(sku))
			}
			return nil
		})
	if err != nil {
		return PriceBook{}, fmt.Errorf("reading Cloud Build's prices from the Cloud Billing catalog: %w", err)
	}
	return book, nil
}

// catalogPageSize asks for the whole catalog at once: it is one page of 370
// rows today (spike §1.5), and a page token is followed if that changes.
const catalogPageSize = 5000

// readSKU is one API SKU as the estimator reads it. A SKU with no pricing
// information is kept with a zero price, so the row that matched is the row
// the refusal names.
func readSKU(sku *cloudbilling.Sku) BuildSKU {
	out := BuildSKU{ID: sku.SkuId, Description: sku.Description, Regions: sku.ServiceRegions}
	if len(sku.PricingInfo) == 0 {
		return out
	}
	expr := sku.PricingInfo[0].PricingExpression
	if expr == nil {
		return out
	}
	out.Unit = expr.UsageUnit
	if len(expr.TieredRates) == 0 {
		return out
	}
	// The bands are ascending spans of usage, and the estimator reserves the
	// dearest of them. Cloud Build's free tier is a first band priced at zero,
	// so the first band alone makes a priced row look like a row with no price
	// and refuses every build; and a later band dearer than the first would
	// price the build below the bound the reservation claims. Bands in more
	// than one currency are not a rate this estimator may take at all, and a
	// band with no unit price leaves the row's dearest price unknown — so a row
	// either has one currency and a price to take, or it is refused.
	var top Micros
	currency := ""
	for _, tier := range expr.TieredRates {
		price := tier.UnitPrice
		if price == nil {
			out.Unpriced = "a band of the row carries no unit price"
			return out
		}
		switch {
		case currency == "":
			currency = price.CurrencyCode
		case price.CurrencyCode != currency:
			out.Unpriced = fmt.Sprintf("the row's bands are priced in %s and %s, so which one prices the build is not something the estimator may guess", currency, price.CurrencyCode)
			return out
		}
		if v := moneyMicros(price); v > top {
			top = v
		}
	}
	out.Currency = currency
	out.MicrosPerUnit = top
	return out
}

// moneyMicros is a Money as micro-dollars, rounded up so a fraction of a
// micro-dollar is never dropped from a reservation.
func moneyMicros(m *cloudbilling.Money) Micros {
	if m == nil || m.Units < 0 || m.Nanos < 0 {
		return 0
	}
	whole := satMul(Micros(m.Units), 1_000_000)
	return satAdd(whole, Micros((int64(m.Nanos)+999)/1000))
}

// Pricer reads the catalog at most once per command and prices every build
// that command submits from that one read: `fugaro image refresh` builds
// several images and the catalog check job builds many, and one lookup per
// invocation is the whole of the caching rule — nothing is persisted, so no
// age limit is needed (spike §4).
//
// A failed read is held too: the command refuses rather than reading again
// mid-way, and never falls back to a number of its own.
type Pricer struct {
	project string
	creds   CatalogCreds
	// read is the catalog read; nil means ReadBuildCatalog. It exists so the
	// read can be replaced in tests without credentials or a network.
	read func(context.Context) (PriceBook, error)

	// mu serializes the callers of one command's Pricer and is held across the
	// read, so two callers racing the first lookup make one request between
	// them.
	mu   sync.Mutex
	book *PriceBook
	err  error
}

// NewPricer is a Pricer for one command, on one installation's credentials.
func NewPricer(project string, c CatalogCreds) *Pricer { return &Pricer{project: project, creds: c} }

// Rate is the rate for a machine type in a region, reading the catalog on the
// first call.
func (p *Pricer) Rate(ctx context.Context, machineType, region string) (BuildRate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.book == nil && p.err == nil {
		read := p.read
		if read == nil {
			read = func(ctx context.Context) (PriceBook, error) { return ReadBuildCatalog(ctx, p.project, p.creds) }
		}
		book, err := read(ctx)
		if err != nil {
			p.err = err
		} else {
			p.book = &book
		}
	}
	if p.err != nil {
		return BuildRate{}, p.err
	}
	return BuildCost(*p.book, machineType, region)
}

// Book is the read the Pricer holds, for a caller that wants the rows
// themselves (nil before the first Rate).
func (p *Pricer) Book() *PriceBook {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.book
}

// satMul is a saturating product of non-negative amounts.
func satMul(a, b Micros) Micros {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}
