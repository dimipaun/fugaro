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
	// UsageUnparsed counts calls settled at their reservation because
	// their usage could not be read.
	UsageUnparsed int `json:"usage_unparsed,omitempty"`
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
