package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The node this service meets while an upgrade is in flight.
//
// `fugaro update` deploys the service with the installation's other pieces and
// migrates the published policy in its last stage, so between the two this
// binary reads a node its own strict Decode refuses. Every cascade model call
// and every proxied Bitbucket request of a run that is still going would be
// refused with it, which is the one thing the update may never do to a run in
// flight (CLAUDE.md's HARD RULE, docs/design/update-in-flight.md). The service
// carries such a node forward in memory instead of refusing it, and these are
// the tests for that.
//
// testV4PolicyDoc is the fixture's own policy as the release before this one
// wrote it: version 4, and a craft pool that names no owner — the pool the
// migration is there to mark (D1942).
func testV4PolicyDoc() string {
	return `{
		"version": 4,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "auto"},
		"craft_credentials": [{"name": "openrouter-main", "type": "openrouter"}],
		"craft_pools": [{"name": "coder", "strategy": "failover", "members": ["openrouter-main"]}]
	}`
}

// docGetter is the installation's state as the service's policy reader sees it:
// one document per path, handed back as the raw bytes the database holds.
type docGetter struct {
	docs  map[string]string
	paths []string
}

func (g *docGetter) Get(_ context.Context, path string, out any) (bool, error) {
	g.paths = append(g.paths, path)
	doc, ok := g.docs[path]
	if !ok {
		return false, nil
	}
	raw, ok := out.(*json.RawMessage)
	if !ok {
		return false, fmt.Errorf("the policy reader asked for %T, want a *json.RawMessage", out)
	}
	*raw = json.RawMessage(doc)
	return true, nil
}

// The reader itself, on the node the update has not reached yet: the migration
// the CLI writes is applied to the bytes in memory, so the pool comes back
// marked legacy-open rather than the node coming back refused.
func TestServiceCarriesAPolicyNodeOneReleaseBehind(t *testing.T) {
	g := &docGetter{docs: map[string]string{projectpolicy.PathShared(): testV4PolicyDoc()}}
	p, found, err := RTDBPolicy{DB: g}.Shared(context.Background())
	if err != nil {
		t.Fatalf("Shared: %v", err)
	}
	if !found || p == nil {
		t.Fatal("Shared read no policy at all, and the node is there")
	}
	if len(p.CraftPools) != 1 || p.CraftPools[0].Name != "coder" {
		t.Fatalf("the pools came back as %+v, want the one the node holds: the migration re-encodes nothing of the node but the member it writes", p.CraftPools)
	}
	if !p.CraftPools[0].LegacyOpen {
		t.Error("the ownerless pool is not legacy-open: the node of the release before this one serves that pool to everyone who names it, and the carried-forward node must say so")
	}
	if !p.CraftPools[0].Serves("stranger@example.com") {
		t.Error("the carried-forward pool serves nobody: a run in flight naming it would be refused a slip the installation grants it")
	}
}

// The run in flight, end to end: a cascade call is served while the published
// node is still the previous release's.
//
// The service is the one the update has already deployed — the fixture's policy
// reader is the real RTDBPolicy over the database — and the node behind it is
// version 4. Before the carry-forward this call is refused (the service answers
// the unreadable policy with a 503 and no credential is ever offered), and the
// run's next model call, or its next push through the Bitbucket proxy, meets
// the same refusal until the operator runs the update's last stage.
func TestCascadeIsServedWhileThePolicyNodeIsOneReleaseBehind(t *testing.T) {
	g := &docGetter{docs: map[string]string{
		projectpolicy.PathShared(): testV4PolicyDoc(),
	}}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		g.docs[projectpolicy.PathRepo(s.slug)] = testChildDoc("default")
		cfg.Policy = RTDBPolicy{DB: g}
	})
	resp, body := s.call(t, callBody("in flight"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is %d (%s), want 200: a run in flight must not be stopped by a policy node the update has not migrated yet", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != "default" {
		t.Fatalf("%s = %q, want default", HeaderCredential, got)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
}

// The other direction, unchanged: a node from below the shape-free floor, below
// the chain's first step, or ahead of this binary, is refused. Carrying a node
// forward is what the service does for the release it is the successor of, not
// a general tolerance, and a node newer than this binary is one no reading of it
// here can be trusted.
//
// The version-3 case is the floor rather than the chain: the chain carries a
// version-3 node (migrators[3]) and the CLI's own reader does, but this service
// SERVES the node and can never write it, so it may only carry a node whose
// migration needs nothing from outside it. Version 4 is the version whose
// Bitbucket token block is derived at publish from the project's local config,
// which is why that bump was a republish: a version-3 node carried here would
// come back as a version-4 document claiming an enforcement the operator never
// published (projectpolicy.ShapeFreeFloor).
func TestServiceStillRefusesANodeItIsNotTheSuccessorOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"a node ahead of this binary", `{"version": 99, "budget": {"mode": "off"}}`},
		{"a node below the shape-free floor", `{"version": 3, "budget": {"mode": "off"}}`},
		{"a node below the chain's first step", `{"version": 2, "budget": {"mode": "off"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &docGetter{docs: map[string]string{projectpolicy.PathShared(): tc.doc}}
			p, found, err := RTDBPolicy{DB: g}.Shared(context.Background())
			if err == nil {
				t.Fatalf("Shared read %+v from %s, want a refusal", p, tc.doc)
			}
			if !found {
				t.Error("the refusal says the node is absent, and it is there")
			}
			var ve *projectpolicy.VersionError
			if !errors.As(err, &ve) {
				t.Fatalf("Shared refused with %v, want a *projectpolicy.VersionError", err)
			}
		})
	}
}
