package runner

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// PricesFromEnv reads the compute price override the job carries,
// FUGARO_COMPUTE_PRICES=<vcpu>,<gib> (US dollars per vCPU-second and per
// GiB-second), which comes from the local config's compute_prices, so
// the runner's report prices compute as ls does. Unset is (zero, false,
// nil): use the list price. A malformed value is an error, and the caller
// falls back to the list price too.
func PricesFromEnv(getenv func(string) string) (backend.Prices, bool, error) {
	v := getenv("FUGARO_COMPUTE_PRICES")
	if v == "" {
		return backend.Prices{}, false, nil
	}
	parts := strings.Split(v, ",")
	if len(parts) != 2 {
		return backend.Prices{}, false, fmt.Errorf("FUGARO_COMPUTE_PRICES %q is not <vcpu>,<gib>", v)
	}
	var f [2]float64
	for i, s := range parts {
		x, err := strconv.ParseFloat(s, 64)
		if err != nil || !localcfg.ValidPrice(x) {
			return backend.Prices{}, false, fmt.Errorf("FUGARO_COMPUTE_PRICES %q: %q must be a number more than 0 and at most %v", v, s, localcfg.MaxPriceUSD)
		}
		f[i] = x
	}
	return backend.Prices{VCPUSecondUSD: f[0], GiBSecondUSD: f[1], Source: localcfg.PriceSource}, true, nil
}
