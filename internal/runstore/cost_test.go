package runstore

import "testing"

func TestNewCost(t *testing.T) {
	api := NewCost(4.12, 0.38, BasisAPIList)
	if api.TotalUSD != 4.50 || !api.Estimate || api.ModelBasis != "api-list" {
		t.Fatalf("api-list = %+v", api)
	}
	sub := NewCost(4.12, 0.38, BasisSubscription)
	if sub.TotalUSD != 0.38 || sub.ModelUSD != 4.12 || !sub.ComputeEstimated {
		t.Fatalf("subscription = %+v", sub)
	}
	if !api.ComputeEstimated {
		t.Fatalf("api-list compute must be estimated: %+v", api)
	}
	if z := NewCost(4.12, 0, BasisAPIList); !z.ComputeEstimated || z.TotalUSD != 4.12 {
		t.Fatalf("an estimated zero = %+v", z)
	}
	if m := ModelOnlyCost(4.12, BasisAPIList); m.ComputeEstimated || m.ComputeUSD != 0 || m.TotalUSD != 4.12 || !m.Estimate {
		t.Fatalf("model only, api-list = %+v", m)
	}
	if m := ModelOnlyCost(4.12, BasisSubscription); m.ComputeEstimated || m.TotalUSD != 0 || m.ModelUSD != 4.12 {
		t.Fatalf("model only, subscription = %+v", m)
	}
	for auth, want := range map[string]string{"vertex": "api-list", "api-key": "api-list", "oauth": "subscription"} {
		if got := ModelBasis(auth); got != want {
			t.Errorf("ModelBasis(%s) = %s", auth, got)
		}
	}
}
