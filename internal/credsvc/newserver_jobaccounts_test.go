package credsvc

// The guard's two halves (WS5's one relaxation to NewServer, server.go): a job
// table with no row is refused for an installation and accepted for a
// deployment that hands in a resolver of its own.

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestServerBindsAnEmptyJobTableOnlyThroughAResolver pins the guard both ways.
// The refusing half is the one the guard exists for and the one a condition
// that never fired would lose: nothing is served by a table a deployment never
// had. The accepting half is what every workspace's deployment is — no job
// document on its container (internal/infra/tf's WorkspaceServiceEnv, which the
// workspace root's cover holds it to) and the published node's binding instead
// of that table — so a guard that refused it would refuse the container the
// workspace root creates.
//
// The configuration below reaches the guard and no further, which is why it is
// built here rather than through the fixture's own service: that helper refuses
// to exist when NewServer declines what it was given, and one half of this test
// is about exactly that refusal.
func TestServerBindsAnEmptyJobTableOnlyThroughAResolver(t *testing.T) {
	f := newFixtureWith(t, nil)
	// config is one deployment's configuration with no job account in it: every
	// reader NewServer validates before the guard. Its auth is the fixture's,
	// because the verifier is validated before the guard too.
	config := func() Config {
		return Config{
			Auth:     f.auth,
			Bundles:  &BundleLoader{Source: &scriptedSource{docs: [][]byte{testBundleDoc(t)}}, Now: func() time.Time { return f.now }},
			Policy:   &fakePolicy{},
			State:    newFakeStore(),
			Kills:    &fakeKills{repos: map[string]bool{}},
			DB:       &fakePatcher{nodes: map[string]any{}},
			Probes:   newFakeProbes(),
			Slips:    testSlips(t),
			AuditLog: slog.NewJSONHandler(io.Discard, nil),
			Now:      func() time.Time { return f.now },
		}
	}

	_, err := NewServer(config())
	if err == nil {
		t.Fatal("NewServer accepted a job table with no row and no resolver of its own: the table is what the installation's scope binds a caller's verified account to a repository with (N1), so a service that has none binds no run to anything")
	}
	if !strings.Contains(err.Error(), "no job accounts") {
		t.Fatalf("NewServer refused for a reason that is not the empty table: %v", err)
	}

	own := config()
	own.Resolver = unservedResolver{}
	srv, err := NewServer(own)
	if err != nil {
		t.Fatalf("NewServer refused a deployment that binds its runs through a resolver of its own: %v", err)
	}
	if _, ok := srv.resolver.(installationResolver); ok {
		t.Error("the service kept the installation's scope over a configuration that handed in a resolver of its own: the deployment would then be served by the table it does not have")
	}
	if len(srv.jobs) != 0 {
		t.Errorf("the service holds %d job rows and the configuration named none: an empty table is not an admission anywhere else either", len(srv.jobs))
	}
}
