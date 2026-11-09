package infra

import (
	"context"
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
	// The account is the front door's own and never the coordinator's: the
	// sweep's holds the GitHub App's private key and the FP's database admin,
	// and a public endpoint running as it would put both behind the port
	// (M4's "its own service account", spec.md §8's "there is no credential to
	// reach").
	if on.MCPServer.AccountID != MCPServerAccountID {
		t.Errorf("the front door's account = %q, want %q", on.MCPServer.AccountID, MCPServerAccountID)
	}
	if on.Coordinator != nil && on.MCPServer.AccountID == on.Coordinator.AccountID {
		t.Errorf("the front door and the coordinator's sweep share the account %s: the sweep holds the App key, a Bitbucket read at the credentials service and the FP's database admin, and none of them may sit behind a public endpoint", on.MCPServer.AccountID)
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
	if len(fields) != 3 || fields["account_id"] == nil || fields["service"] == nil || fields["image"] == nil {
		t.Errorf("the tfvars' mcp_server carries %v keys: the module's variable is exactly { account_id, service, image }, and Terraform refuses any other shape at plan time", len(fields))
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

// TestTheStoreIsNeverAdoptedOnTheGoSide is the second half of D935's
// "created and never adopted", at the layer this file owns: whatever Terraform
// does, `fugaro update` must not be able to reach the store by discovery.
//
// The bucket's name is a function of the project alone, so it is predictable,
// and bucket names are global — anybody can hold `fugaro-mcpauth-<project>`
// in another project. Terraform is what refuses that (a create against a name
// that already exists fails the apply), and this is the other route: the
// imports `fugaro update` writes when discovery finds something already there.
// No kind of importTable may name the store's bucket, because an import is
// exactly the adoption the store must never have.
//
// The mutation this fails on: a kind added to importTable for the store — the
// plausible mistake, since every other bucket this installation owns (the runs
// bucket, the log bucket) IS adopted by name — after which an installation
// whose name somebody else holds would import that bucket and write its token
// records into it.
func TestTheStoreIsNeverAdoptedOnTheGoSide(t *testing.T) {
	for kind, entry := range importTable {
		if strings.Contains(entry.to, "mcpauth_store") || strings.Contains(entry.to, "mcpauth") {
			t.Errorf("import kind %q adopts the store at %s: the store is created by Terraform and never imported — a bucket already held under its name must fail the apply rather than receive this installation's records (D935, 2026-11-05)", kind, entry.to)
		}
	}
	// And discovery, run against a project that already holds a bucket of the
	// store's name, adopts nothing naming it. Discovery reads the resources it
	// knows by name (the runs bucket, the registries, the secrets), so this is
	// the guard rather than a live hazard: it fails the day a store kind is
	// added above, or the day discovery starts listing buckets and taking one.
	f := newCloud(t)
	inst := installationSpec(t)
	f.gcs.AddBucket("fugaro-mcpauth-"+inst.Project, testProjectNumber, nil)
	im, err := DiscoverInstallation(context.Background(), f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range im.List {
		if strings.Contains(i.ID, "fugaro-mcpauth-") || strings.Contains(i.To, "mcpauth_store") {
			t.Errorf("discovery adopts %s as %s: the store's name is predictable and global, and an installation that finds a bucket under it fails the update rather than storing token hashes in somebody else's bucket", i.ID, i.To)
		}
	}
}
