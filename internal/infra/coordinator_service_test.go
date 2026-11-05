package infra

import (
	"encoding/json"
	"strings"
	"testing"
)

// The front door's Go half: the spec the installation root's var.mcp_server
// carries, its own opt-in, and the two refusals that keep a flag from planning
// nothing. The Terraform half is
// deploy/terraform/gcp/modules/installation/coordinator_service_test.go, which
// holds the module to the other three things this file cannot see — the public
// invoker binding, the coordinator's own account, and a container that carries
// no credential.

// TestMCPServerIsItsOwnOptInAndOffByDefault is D934 at the tfvars: an
// installation that has not asked for the front door writes no mcp_server key
// at all, so the module creates no service and the plan is exactly the one it
// planned before the front door existed — the coordinator's read-only view
// included, which is the pair the plan's operator section insists are separate
// opt-ins. With the opt-in the key carries the service and the ONE image both
// the job and the front door run: a second image would be a second push, a
// second mirror entry and a second thing to re-pin on every release.
func TestMCPServerIsItsOwnOptInAndOffByDefault(t *testing.T) {
	lc := credentialsLC(t)
	off, err := Installation(lc, InstallOptions{BudgetBackend: true, Coordinator: true})
	if err != nil {
		t.Fatal(err)
	}
	if off.MCPServer != nil {
		t.Fatalf("an installation that did not ask for a front door plans one: %+v", off.MCPServer)
	}
	data, err := InstallationVars(off)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mcp_server") {
		t.Errorf("the installation's tfvars name the front door without the opt-in:\n%s", data)
	}

	on, err := Installation(lc, InstallOptions{BudgetBackend: true, Coordinator: true, MCPServer: true})
	if err != nil {
		t.Fatal(err)
	}
	if on.MCPServer == nil {
		t.Fatal("--mcp-server planned no front door")
	}
	if on.Coordinator == nil {
		t.Fatal("--mcp-server planned no coordinator: the front door has no account and no state to serve")
	}
	wantImage := HistoryImage("us-east5-docker.pkg.dev/proj-1234")
	if on.MCPServer.Service != MCPServerServiceName || on.MCPServer.Image != wantImage {
		t.Errorf("the front door = %+v, want the service %s running %s", *on.MCPServer, MCPServerServiceName, wantImage)
	}
	if on.MCPServer.Image != on.Coordinator.Image {
		t.Errorf("the front door runs %s and the aggregation job %s: one CLI binary serves both, so a second image is a second thing to build, push and re-pin", on.MCPServer.Image, on.Coordinator.Image)
	}

	data, err = InstallationVars(on)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServer *MCPServerSpec `json:"mcp_server"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MCPServer == nil {
		t.Fatalf("the tfvars carry no mcp_server object:\n%s", data)
	}
	if *doc.MCPServer != *on.MCPServer {
		t.Errorf("the tfvars' mcp_server = %+v, want %+v", *doc.MCPServer, *on.MCPServer)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	raw := keys["mcp_server"]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["service"] == nil || fields["image"] == nil {
		t.Errorf("the tfvars' mcp_server carries %v keys: the module's variable is exactly { service, image }, and Terraform refuses any other shape at plan time", len(fields))
	}
}

// TestMCPServerNeedsTheCoordinator is the refusal that keeps the flag honest:
// --mcp-server without the coordinator's own opt-in plans nothing — the front
// door runs as the coordinator's account and serves the state that account
// writes (spec.md §1, "no second service to run, no second identity to
// grant") — so it is refused here, by name, before a tfvars exists, rather
// than silently dropped for the module's validation to catch in the plan. An
// installation without a budget backend is refused with it: the account, the
// runs bucket the front door reads a run's artefacts from and the Firebase
// database it writes all arrive with that backend, and an installation that
// has none has nothing for a front door to serve.
//
// Fails when the option is ignored (the mutation: an `if o.MCPServer` that
// only sets the spec when the coordinator is already on, with no else), which
// is the shape that would report a successful update and no endpoint.
func TestMCPServerNeedsTheCoordinator(t *testing.T) {
	lc := credentialsLC(t)
	for name, o := range map[string]InstallOptions{
		"no coordinator":    {BudgetBackend: true, MCPServer: true},
		"no budget backend": {Coordinator: true, MCPServer: true},
		"neither beside it": {MCPServer: true},
	} {
		spec, err := Installation(lc, o)
		if err == nil {
			t.Errorf("%s: Installation planned a front door (%+v) instead of refusing: the front door runs as the coordinator's own account, and an installation that has not declared it has no account to run as", name, spec.MCPServer)
			continue
		}
		if !strings.Contains(err.Error(), "--coordinator") {
			t.Errorf("%s: the refusal does not name the opt-in that is missing: %v", name, err)
		}
		if spec.MCPServer != nil {
			t.Errorf("%s: the spec carries a front door beside the refusal: %+v", name, spec.MCPServer)
		}
	}
}
