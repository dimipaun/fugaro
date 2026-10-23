package gateway

import "testing"

// The probed tiers are what the service charges, and the agent must not learn
// them (D143): the header is dropped like the tier, the credential and the switch.
func TestProbeHeaderNeverReachesTheAgent(t *testing.T) {
	if !dropResponseHeaders[HeaderProbe] {
		t.Errorf("%s is not in dropResponseHeaders: the agent would see which tiers were probed", HeaderProbe)
	}
}
