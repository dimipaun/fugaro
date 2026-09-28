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
	TotalUSD   float64 `json:"total_usd"`
	Estimate   bool    `json:"estimate"`
	ModelBasis string  `json:"model_basis"`
}

// ModelBasis is the basis for an agent.auth mode.
func ModelBasis(auth string) string {
	if auth == "oauth" {
		return BasisSubscription
	}
	return BasisAPIList
}

// NewCost builds a Cost. TotalUSD counts only billed dollars, rounded to
// the cent: a subscription's model figure is notional and left out of it.
// Estimate is always true, since storage, logging and builds are left out.
func NewCost(modelUSD, computeUSD float64, basis string) Cost {
	total := computeUSD
	if basis != BasisSubscription {
		total += modelUSD
	}
	return Cost{ModelUSD: modelUSD, ComputeUSD: computeUSD, TotalUSD: math.Round(total*100) / 100, Estimate: true, ModelBasis: basis}
}
