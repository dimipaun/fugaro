// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The refusals New makes, one case each (subscriptions plan S3.2; the design
// docs/key-signing-service/spec.md §3.5, §7.2). They are held here because
// every other test in this package drives a *complete* deployment — the fixture
// hands the service a store, a minter, a mailer, a code host and a rate — so a
// guard in New can be deleted with all of them green, and what it guards is the
// difference between a service that refuses to start and one that runs with an
// unlimited rate, an unverifiable body or a key that verifies nowhere.
//
// The refusals the portal's API adds are not here: a mounted route's seam is
// refused only for the routes this deployment mounts (api.go), and api_test.go
// holds that guard.

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// TestTheIntakeRefusesToStartInsteadOfServing: each case is one field of one
// deployment left out or set to something the service cannot serve with, and the
// want is the same every time — no Service and an error, rather than a half-built
// intake answering the internet.
func TestTheIntakeRefusesToStartInsteadOfServing(t *testing.T) {
	f := newFixture(t)
	base, deps := *f.cfg, *f.deps

	for _, tc := range []struct {
		name   string
		serves string
		edit   func(*Config, *Deps)
	}{
		{name: "an environment the service does not have", serves: "serve a mode the merchant's events are measured against", edit: func(c *Config, _ *Deps) { c.Environment = "prod" }},
		{name: "no environment at all", serves: "serve a mode the merchant's events are measured against", edit: func(c *Config, _ *Deps) { c.Environment = "" }},
		{name: "no key id for the production environment", serves: "mint keys under no id, which verify nowhere", edit: func(c *Config, _ *Deps) { c.Keys.Production = "" }},
		{name: "no key id for the staging environment", serves: "mint test-mode keys under no id", edit: func(c *Config, _ *Deps) { c.Environment = Staging; c.Keys.Staging = "" }},
		{name: "no URL", serves: "mail an invitation link built from nothing, and read the portal's origin from nothing", edit: func(c *Config, _ *Deps) { c.URL = "  " }},
		{name: "no open-source plan", serves: "file the open-source ask under a plan nobody named", edit: func(c *Config, _ *Deps) { c.OpenSource = nil }},
		{name: "a plan naming a tier no key is issued for", serves: "mint a key the verifier refuses", edit: func(c *Config, _ *Deps) { c.Paid.Tier = "gold" }},
		{name: "no merchant secret", serves: "read an unsigned body as the merchant's", edit: func(c *Config, _ *Deps) { c.MerchantSecret = nil }},
		{name: "no repository cap", serves: "ask the code host about every line of a 4 KiB ask", edit: func(c *Config, _ *Deps) { c.MaxRepositories = 0 }},
		{name: "no code host to read", serves: "read repositories from whatever an applicant typed", edit: func(c *Config, _ *Deps) { c.CodeHosts = nil }},
		{name: "a code host named as a URL", serves: "compare a host against a URL and serve neither", edit: func(c *Config, _ *Deps) { c.CodeHosts = []string{"https://code.example"} }},
		{name: "no terms version", serves: "record a sign-up's acceptance of a wording it cannot name (D2199)", edit: func(c *Config, _ *Deps) { c.TermsVersion = "  " }},
		{name: "no store", serves: "mint a key it cannot remember and cannot tell a redelivery from a first delivery", edit: func(_ *Config, d *Deps) { d.Store = nil }},
		{name: "no minter", serves: "sign with a service it does not have", edit: func(_ *Config, d *Deps) { d.Mints = nil }},
		{name: "no mailer", serves: "deliver a purchase's notice with no mail path, and an invitation nobody receives is one nobody can accept", edit: func(_ *Config, d *Deps) { d.Mailer = nil }},
		{name: "no code-host client", serves: "run an open-source check that reads no repository", edit: func(_ *Config, d *Deps) { d.CodeHost = nil }},
		{name: "no throttle", serves: "serve every door of the intake without a rate", edit: func(c *Config, _ *Deps) { c.Throttle = Limits{} }},
		{name: "no per-tier mint caps", serves: "let one session mint without limit (§6's limits row, D454)", edit: func(c *Config, _ *Deps) { c.MintCaps = nil }},
		{name: "a tier with no mint cap", serves: "leave one tier of the operations API's minting uncapped", edit: func(c *Config, _ *Deps) { delete(c.MintCaps, licence.TierEnterprise) }},
		{name: "a mint cap of no mints", serves: "answer every operations mint with the over-cap refusal", edit: func(c *Config, _ *Deps) {
			c.MintCaps[licence.TierTeam] = Limits{Window: time.Hour}
		}},
		{name: "a mint cap over no window", serves: "count a cap over a window of nothing", edit: func(c *Config, _ *Deps) { c.MintCaps[licence.TierTeam] = Limits{Requests: 5} }},
		{name: "no notifier", serves: "leave a queued request for an owner who is never told (§4.5)", edit: func(_ *Config, d *Deps) { d.Notifier = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, d := base, deps
			// The fixture's plans and lists are pointers and slices; a case that
			// edits one must not edit what the next case reads.
			cfg.OpenSource = copyPlan(base.OpenSource)
			cfg.FreeMailDomains = append([]string(nil), base.FreeMailDomains...)
			cfg.CodeHosts = append([]string(nil), base.CodeHosts...)
			cfg.MintCaps = map[string]Limits{}
			for tier, limit := range base.MintCaps {
				cfg.MintCaps[tier] = limit
			}
			tc.edit(&cfg, &d)

			if _, err := New(cfg, d); err == nil {
				t.Errorf("the intake started with %s, want a refusal: it would %s", tc.name, tc.serves)
			}
		})
	}

	// And the fixture's own deployment is one the service starts with, so the
	// cases above are the mutations and not the baseline.
	if _, err := New(base, deps); err != nil {
		t.Errorf("the fixture's deployment did not start: %v", err)
	}
}

func copyPlan(plan *Plan) *Plan {
	if plan == nil {
		return nil
	}
	copied := *plan
	copied.Features = append([]string(nil), plan.Features...)
	return &copied
}
