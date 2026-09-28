package gcp

import "testing"

func TestListPrices(t *testing.T) {
	p := ListPrices("us-east5")
	if p.VCPUSecondUSD != 0.000018 || p.GiBSecondUSD != 0.000002 || p.Source == "" {
		t.Fatalf("us-east5 = %+v", p)
	}
	unknown := ListPrices("mars-north1")
	if unknown.VCPUSecondUSD <= p.VCPUSecondUSD {
		t.Fatalf("an unknown region must fall back to the higher tier: %+v", unknown)
	}
	if t2 := ListPrices("europe-west2"); t2 != unknown {
		t.Fatalf("europe-west2 (tier 2) = %+v, want %+v", t2, unknown)
	}
}
