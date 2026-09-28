package gcp

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Cloud Run jobs list prices (default, no committed-use discount), per
// second, from https://cloud.google.com/run/pricing (checked 2026-09-27).
// The free tier is ignored, so figures are an upper bound. Local overrides
// arrive in M5 (design §10.1).
var (
	tier1 = backend.Prices{VCPUSecondUSD: 0.000018, GiBSecondUSD: 0.000002, Source: "Cloud Run jobs list price, tier 1 (2026-09)"}
	tier2 = backend.Prices{VCPUSecondUSD: 0.0000216, GiBSecondUSD: 0.0000024, Source: "Cloud Run jobs list price, tier 2 (2026-09)"}

	// tier1Regions is the page's "Subject to Tier 1 pricing" list, plus
	// us-east7, which the page's price tables bill at tier 1 though its
	// tier lists omit it.
	tier1Regions = []string{
		"africa-south1", "asia-east1", "asia-northeast1", "asia-northeast2", "asia-south1",
		"asia-southeast3", "asia-southeast4", "europe-north1", "europe-north2", "europe-southwest1",
		"europe-west1", "europe-west4", "europe-west8", "europe-west9", "me-west1",
		"northamerica-south1", "us-central1", "us-east1", "us-east4", "us-east5", "us-east7",
		"us-south1", "us-west1", "us-west8",
	}
)

// ListPrices is Cloud Run's jobs list price in region. An unknown region
// gets tier 2, the higher price, so an estimate errs high rather than low.
func ListPrices(region string) backend.Prices {
	if slices.Contains(tier1Regions, region) {
		return tier1
	}
	return tier2
}
