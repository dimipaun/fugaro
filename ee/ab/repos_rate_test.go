// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"fmt"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestABRateFromRepos: the rate a launch samples at is the project layer's
// own rate with the repository's entry in ab.repos over it (B9, D3297), and
// nothing else — a repository's ab.rate is dropped before this package reads
// it (D3237), so the map is the only per-repository opt-out. The launch at
// the end is the whole point of the map: a repository the layer opts out is
// sampled for nothing although the project samples at the highest rate a
// layer may set, and the run it is sampled for is the champion's alone.
func TestABRateFromRepos(t *testing.T) {
	cases := []struct {
		name  string
		layer *config.AB
		repo  string
		want  float64
	}{
		{"the map lowers the block's rate", &config.AB{Rate: 0.08, Repos: map[string]float64{"acme/app": 0.02}}, "acme/app", 0.02},
		{"the map opts the repository out", &config.AB{Rate: 0.08, Repos: map[string]float64{"acme/app": 0}}, "acme/app", 0},
		{"the map names another repository", &config.AB{Rate: 0.08, Repos: map[string]float64{"acme/api": 0}}, "acme/app", 0.08},
		{"the block writes no map", &config.AB{Rate: 0.08}, "acme/app", 0.08},
		{"the map's rate is the block's own", &config.AB{Rate: 0.08, Repos: map[string]float64{"acme/app": 0.08}}, "acme/app", 0.08},
		{"the map is above the block's rate", &config.AB{Rate: 0.08, Repos: map[string]float64{"acme/app": 0.09}}, "acme/app", 0.08},
		{"there is no block at all", nil, "acme/app", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := abRate(tc.layer, tc.repo); got != tc.want {
				t.Errorf("abRate(%+v, %q) = %v, want %v: a launch samples at the block's own ab.rate with ab.repos.<repo> over it, and only where the layer writes an entry for this repository (D3297)", tc.layer, tc.repo, got, tc.want)
			}
		})
	}

	block := "ab: { rate: " + fmt.Sprintf("%g", config.ABMaxRate) + ", challengers: [" + abLaunchChallenger + "], repos: { acme/app: 0 } }"
	f := newABLaunchLayer(t, abLaunchLayerText(block))
	out, errOut, err := f.launch(t, "--recipe", "default", "--json", "Add a feature")
	if err != nil {
		t.Fatalf("the launch failed: %v\n%s%s", err, out, errOut)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the launch started %d runs, want the champion alone: acme/app is opted out by ab.repos.acme/app: 0\n%s%s", got, out, errOut)
	}
	if doc := abLaunchJSON(t, out); doc.Pair != nil {
		t.Errorf("--json carries the pair %+v: the opted-out repository was sampled, and it is sampled for nothing\n%s", *doc.Pair, out)
	}
}
