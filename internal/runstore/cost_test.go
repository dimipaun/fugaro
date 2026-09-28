package runstore

import "testing"

func TestNewCost(t *testing.T) {
	api := NewCost(4.12, 0.38, BasisAPIList)
	if api.TotalUSD != 4.50 || !api.Estimate || api.ModelBasis != "api-list" {
		t.Fatalf("api-list = %+v", api)
	}
	sub := NewCost(4.12, 0.38, BasisSubscription)
	if sub.TotalUSD != 0.38 || sub.ModelUSD != 4.12 {
		t.Fatalf("subscription = %+v", sub)
	}
	for auth, want := range map[string]string{"vertex": "api-list", "api-key": "api-list", "oauth": "subscription"} {
		if got := ModelBasis(auth); got != want {
			t.Errorf("ModelBasis(%s) = %s", auth, got)
		}
	}
}
