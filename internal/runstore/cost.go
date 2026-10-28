package runstore

import "math"

// Cost bases (design §10.1).
const (
	BasisAPIList      = "api-list"     // vertex or api-key: list-price billing
	BasisSubscription = "subscription" // oauth: notional; usage counts against plan limits
)

// Cost is result.json's cost breakdown.
type Cost struct {
	ModelUSD   float64 `json:"model_usd"`
	ComputeUSD float64 `json:"compute_usd"`
	// ComputeEstimated is true only when prices and resources were known;
	// when false, ComputeUSD is 0 and means "not estimated", not free.
	ComputeEstimated bool `json:"compute_estimated"`
	// TotalUSD is billed dollars: it excludes the model figure of a
	// subscription run and, in a mixed one, ModelNotionalUSD (P12, D140).
	TotalUSD   float64 `json:"total_usd"`
	Estimate   bool    `json:"estimate"`
	ModelBasis string  `json:"model_basis"`
	// ModelNotionalUSD is the part of ModelUSD that was notional: calls a
	// subscription paid for, which count against a plan's limits rather than
	// being billed — an oauth run's every call, and a cascade run's tier A
	// (D140, F25). It is set with WithNotional, never by hand, so TotalUSD
	// cannot drift from it.
	//
	// ModelUSD keeps its meaning (every stage's model dollars, notional ones
	// included), which is what keeps the documented invariant
	// sum(stages[].model_usd) + model_by["unattributed"] == model_usd true
	// exactly; ModelNotionalUSD says how much of that figure was notional.
	// A mixed run's model_basis is api-list for the whole run (its billed
	// part is the gateway's), so a reader splitting the run's dollars must
	// split on this field and never on ModelBasis (P12).
	ModelNotionalUSD float64 `json:"model_notional_usd,omitempty"`
	// ModelSource says where ModelUSD came from: "gateway" (its settled
	// ledger) or "claude-code" (the agent's own total_cost_usd).
	ModelSource string `json:"model_source,omitempty"`
	// ComputeSource is the source line of the compute estimate: the price
	// table's own line for the figures ComputeUSD was computed from, so a
	// reader can tell an estimate priced from the Compute Engine VM table
	// from one priced with Cloud Run's jobs list price (design
	// compute-backend.md §5.2, C17). Empty when the estimate carries no
	// table of its own -- Cloud Run's list price today -- so nothing
	// already recorded changes.
	ComputeSource string `json:"compute_source,omitempty"`
	// ModelBy is the model spend by model, in USD.
	ModelBy map[string]float64 `json:"model_by,omitempty"`
	// RouteBy is the model spend by provider route, in USD; Claude calls
	// have no route and are not in it.
	RouteBy map[string]float64 `json:"route_by,omitempty"`
	// ReportedUSD is what providers said their calls cost, summed. It is
	// recorded to compare with the charge and never settles a call.
	ReportedUSD float64 `json:"reported_usd,omitempty"`
	// Unreconciled is the USD charged from reservations rather than from
	// reported usage.
	Unreconciled float64 `json:"unreconciled,omitempty"`
	// CacheReadTokens and CacheWriteTokens are the run's prompt-cache read
	// and write token totals, summed from the gateway's per-call usage (the
	// cache_read_input_tokens/cache_creation_input_tokens its stream parses
	// into pricing.Usage). They are token counts and not a dollar split: the
	// price table returns one blended figure per call, so telling a cache
	// read's dollars from a write's would need pricing code that does not
	// exist. They answer design §3's prompt-caching risk -- a compressed
	// session's cache prefix can change, turning cheap reads into dearer
	// writes -- which is "did the cache behave as expected", not "what did a
	// stage cost", and they are 0 for a run with no gateway.
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	// UsageUnparsed counts calls settled at their reservation because
	// their usage could not be read.
	UsageUnparsed int `json:"usage_unparsed,omitempty"`
	// ServiceTiers names the service tiers the run's calls came back at
	// that the price table has no multiplier for -- `usage.service_tier
	// "priority"`, spelled as a violation is. Those calls were charged the
	// conservative multiple of the rates they were priced at (design §6.1)
	// rather than failing their stage, so the run's model_usd counts that
	// charge: the name is what explains the figure to a reader who cannot
	// see the stage logs, and on a provider route it is the only thing that
	// does, since a provider's own reported cost is not what settles. At
	// most eight distinct names, then one `+N more` entry counting the rest.
	ServiceTiers []string `json:"service_tiers,omitempty"`
	// CatalogStale is true when the built-in provider price catalog was
	// older than its own 30-day threshold, so every model it priced was
	// charged the dearest end of its tier's range rather than the end the
	// route's price_policy asked for (design provider-price-catalog.md §7,
	// D10, P7). It is a fact about the prices, not about the run: the run
	// charged, and over-counted on purpose.
	CatalogStale bool `json:"catalog_stale,omitempty"`
}

// ModelBasis is the basis for an agent.auth mode.
func ModelBasis(auth string) string {
	if auth == "oauth" {
		return BasisSubscription
	}
	return BasisAPIList
}

// NewCost builds a Cost with an estimated compute figure. TotalUSD counts
// only billed dollars, rounded to the cent: a subscription's model figure
// is notional and left out of it. Estimate is always true, since storage,
// logging and builds are left out.
func NewCost(modelUSD, computeUSD float64, basis string) Cost {
	return newCost(modelUSD, computeUSD, true, basis)
}

// ModelOnlyCost builds a Cost whose compute was not estimated, because
// prices or resources were unknown (a local run, say).
func ModelOnlyCost(modelUSD float64, basis string) Cost {
	return newCost(modelUSD, 0, false, basis)
}

func newCost(modelUSD, computeUSD float64, estimated bool, basis string) Cost {
	total := computeUSD
	if basis != BasisSubscription {
		total += modelUSD
	}
	return Cost{
		ModelUSD: modelUSD, ComputeUSD: computeUSD, ComputeEstimated: estimated,
		TotalUSD: math.Round(total*100) / 100, Estimate: true, ModelBasis: basis,
	}
}

// ModelMoneyUSD is the reader's half of the pair ModelUSD and
// ModelNotionalUSD make up (P12, D140): the part of the model figure the run's
// calls paid -- an api-key or Vertex run's every dollar, a cascade run's tiers
// B and C -- which is the figure a cap is enforced against and a bill carries.
// ModelUSD is the whole figure and TotalUSD is billed dollars, so this is the
// one to print beside a cap.
func (c Cost) ModelMoneyUSD() float64 {
	return c.ModelUSD - c.ModelSubscriptionUSD()
}

// ModelSubscriptionUSD is the part of ModelUSD a subscription paid: what a
// plan's limits count rather than a bill. It is ModelNotionalUSD, and the
// basis is the fallback for the records written before C1.16 carried that
// field: an oauth run's whole model figure was always the subscription's, and
// such a record says so with model_basis alone (ModelNotionalUSD 0). The
// fallback is why a reader never splits on ModelBasis directly -- a mixed
// cascade run's basis is api-list for the whole run (P12).
func (c Cost) ModelSubscriptionUSD() float64 {
	if c.ModelNotionalUSD > 0 {
		return min(c.ModelNotionalUSD, c.ModelUSD)
	}
	if c.ModelBasis == BasisSubscription {
		return c.ModelUSD
	}
	return 0
}

// AllNotional answers the question a reader asks when it wants to know whether
// the run's model figure was a bill at all: whether the whole of it was a
// subscription's, so nothing of it counts toward a cap (P12, D140). A run whose
// figure is the plan's whole -- an oauth run, or a cascade run the plan served
// in full -- is one, and so is a subscription run that recorded no figure at
// all: nothing of such a run's model spend was a real dollar.
//
// It is the same split ModelSubscriptionUSD writes down, asked as yes or no,
// and the basis is the fallback for the records written before C1.16 carried
// the field (an oauth record's whole figure was always the subscription's, and
// it says so with model_basis alone). A reader must ask this rather than compare
// the basis itself: a cascade run's basis is api-list for the whole run -- its
// billed part's -- so the basis reads a cascade run the plan paid in full as an
// api-key one (P12; internal/runstore's cost_readers_guard_test.go holds the
// rule over every reader in the tree).
func (c Cost) AllNotional() bool {
	if c.ModelNotionalUSD > 0 {
		return c.ModelNotionalUSD >= c.ModelUSD
	}
	return c.ModelBasis == BasisSubscription
}

// WithNotional records the notional part of ModelUSD (P12, D140) and returns
// the cost with TotalUSD recomputed from it: the calls a subscription paid
// for are in ModelUSD -- every stage's figure, so the sum invariant holds --
// and never in the billed total, whichever basis the run has.
//
// usd is clamped into [0, ModelUSD] rather than refused: a figure outside it
// is a caller's bug, and of the two records that bug can write, the one that
// bills nothing notional is the safe direction (the alternative bills a
// negative amount, or claims more of the model figure was a subscription's
// than the model figure itself).
func (c Cost) WithNotional(usd float64) Cost {
	usd = min(max(usd, 0), c.ModelUSD)
	c.ModelNotionalUSD = usd
	total := c.ComputeUSD
	if c.ModelBasis != BasisSubscription {
		total += c.ModelUSD - usd
	}
	c.TotalUSD = math.Round(total*100) / 100
	return c
}
