package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
)

func TestRunnerPricesFromEnv(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "FUGARO_COMPUTE_PRICES" {
				return v
			}
			return ""
		}
	}
	p, ok, err := PricesFromEnv(env("0.00001,0.000001"))
	want := backend.Prices{VCPUSecondUSD: 0.00001, GiBSecondUSD: 0.000001, Source: "local override"}
	if err != nil || !ok || p != want {
		t.Fatalf("well-formed = %+v, %v, %v", p, ok, err)
	}
	if _, ok, err := PricesFromEnv(env("")); ok || err != nil {
		t.Fatalf("unset = %v, %v; want the list price (not ok, no error)", ok, err)
	}
	for _, bad := range []string{"0.00001", "0.00001,", ",0.00001", "a,b", "0.00001,0.000001,1", "0,0.000001",
		"-0.00001,0.000001", "NaN,0.000001", "0.00001,Inf", "1.0,0.000001", " 0.00001,0.000001"} {
		if _, ok, err := PricesFromEnv(env(bad)); ok || err == nil {
			t.Errorf("%q: ok %v, err %v; want an error", bad, ok, err)
		}
	}
}
