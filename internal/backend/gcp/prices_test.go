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
	for _, region := range []string{"europe-west2", "us-east7"} {
		// europe-west2 is listed as tier 2; us-east7 is on neither list.
		if got := ListPrices(region); got != unknown {
			t.Fatalf("%s = %+v, want tier 2 %+v", region, got, unknown)
		}
	}
}
