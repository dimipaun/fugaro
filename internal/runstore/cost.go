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
	ComputeEstimated bool    `json:"compute_estimated"`
	TotalUSD         float64 `json:"total_usd"`
	Estimate         bool    `json:"estimate"`
	ModelBasis       string  `json:"model_basis"`
	// ModelSource says where ModelUSD came from: "gateway" (its settled
	// ledger) or "claude-code" (the agent's own total_cost_usd).
	ModelSource string `json:"model_source,omitempty"`
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
