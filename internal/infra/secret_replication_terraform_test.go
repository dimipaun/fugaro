//go:build terraform

package infra

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The six secret containers of an installation declare the same replication
// and the same ignore_changes (LP5a, D3082, F9, R5): a container created from
// now on is replicated in the installation's region, and the field is IGNORED
// on one that exists, because a container's replication cannot be changed
// after it is created — Secret Manager refuses the update — and every other
// declaration would replace the container and delete every version in it
// first. Every container created before the declaration is replicated
// AUTOMATICALLY, wherever Google chose, so what these tests hold is that such a
// container is left alone, and that one the owner replicated by hand somewhere
// else is left alone too.
//
// They are planned against a PRIOR STATE (installationPlanAgainst) because no
// hermetic test can hold a real container: the state says "this container
// exists, with this replication", the real provider computes the diff, and the
// planned replication is then read back out of the plan. The mock provider
// cannot do it — it plans the config's own value rather than a replacement of
// what is in state — and the two-run mock pair in
// roots/installation/tests/secret_replication.tftest.hcl is the other half of
// the same fact.
//
// What no test here can say is what the real provider does to a container it
// ADOPTS; that is U9, measured by Check 821 after the release (O11).

// planWithSecretsState plans the installation module for spec against a state
// holding its project-wide containers with the replication each one already
// has, which is the plan `fugaro update` writes against the installation it
// finds.
func planWithSecretsState(t *testing.T, spec InstallationSpec, extra map[string]any, prior map[string][]any) (*tf.Plan, map[string]plannedResource) {
	t.Helper()
	return installationPlanAgainst(t, spec, extra, func(vars map[string]any) map[string]any {
		return secretsState(t, vars, prior)
	})
}

// twoProjectSecretsLC is the fixture config with two project-wide containers,
// which the mixed case needs: one installation, two containers that already
// hold two different replications.
const twoProjectSecretsLC = `project_secrets:
  anthropic-api-key: { allow_data_to: [acme/sandbox] }
  github-app-key: { allow_data_to: [acme/sandbox] }
`

// automaticReplication is the replication every container created before this
// declaration holds: `auto {}` and no replicas of anybody's choosing. It is
// written as the state file spells it, and the empty object inside `auto` is
// the provider's one (empty) nested block.
func automaticReplication() []any {
	return []any{map[string]any{"auto": []any{map[string]any{}}, "user_managed": []any{}}}
}

// replicatedIn is the replication of a container an owner created or moved
// themselves, in a region the installation's own declaration does not name.
func replicatedIn(region string) []any {
	return []any{map[string]any{
		"auto": []any{},
		"user_managed": []any{map[string]any{
			"replicas": []any{map[string]any{"location": region, "customer_managed_encryption": []any{}}},
		}},
	}}
}

// secretsState is the terraform state an earlier apply of the installation
// would have written for its project-wide containers, and it holds NOTHING
// else: it is the one thing a real container cannot be held in, so it is
// written by hand from the tfvars the module is planned with. `prior` gives
// each container the replication it already has, by the logical name the
// config uses, and a key the state does not carry is a test bug rather than an
// installation: the plan would then create that container.
func secretsState(t *testing.T, vars map[string]any, prior map[string][]any) map[string]any {
	t.Helper()
	project, _ := vars["project"].(string)
	secrets, ok := vars["project_secrets"].(map[string]any)
	if !ok {
		t.Fatalf("the installation's tfvars carry no project_secrets: %v", vars["project_secrets"])
	}
	if len(secrets) != len(prior) {
		t.Fatalf("the state carries %d containers and the config %d: %v", len(prior), len(secrets), slices.Sorted(maps.Keys(prior)))
	}
	instances := []any{}
	for _, key := range slices.Sorted(maps.Keys(prior)) {
		id, ok := secrets[key].(string)
		if !ok {
			t.Fatalf("the config declares no project-wide container %q: %v", key, slices.Sorted(maps.Keys(secrets)))
		}
		// The five attributes a plan needs to see the container as itself: the
		// id is what makes the provider treat it as one that exists rather than
		// one to create, and everything else it can recompute.
		name := "projects/" + project + "/secrets/" + id
		instances = append(instances, map[string]any{
			"schema_version": 0,
			"index_key":      key,
			"attributes": map[string]any{
				"project":     project,
				"secret_id":   id,
				"replication": prior[key],
				"id":          name,
				"name":        name,
			},
			"sensitive_attributes": []any{},
		})
	}
	return map[string]any{
		"version": 4,
		"serial":  1,
		"lineage": "00000000-0000-0000-0000-000000000000",
		"outputs": map[string]any{},
		"resources": []any{map[string]any{
			"mode":      "managed",
			"type":      "google_secret_manager_secret",
			"name":      "project",
			"provider":  `provider["registry.terraform.io/hashicorp/google"]`,
			"module":    "module.installation",
			"instances": instances,
		}},
	}
}

// plannedReplication is the replication of a container as a plan renders it:
// `terraform show -json`'s block shape, with the empty nested lists the
// provider writes for the branch that is not taken.
type plannedReplication struct {
	Auto        []map[string]any `json:"auto"`
	UserManaged []struct {
		Replicas []struct {
			Location string `json:"location"`
		} `json:"replicas"`
	} `json:"user_managed"`
}

// regionOf is the region a container is replicated in, and false when the plan
// carries no single user-managed replica at all.
func (r plannedReplication) regionOf() (string, bool) {
	if len(r.UserManaged) != 1 || len(r.UserManaged[0].Replicas) != 1 {
		return "", false
	}
	return r.UserManaged[0].Replicas[0].Location, true
}

func (r plannedReplication) automatic() bool {
	return len(r.Auto) == 1 && len(r.UserManaged) == 0
}

// secretAddress is a project-wide container's address in the installation
// root, which is also the module's own when it is planned on its own.
func secretAddress(key string) string {
	return fmt.Sprintf("module.installation.google_secret_manager_secret.project[%q]", key)
}

// plannedReplicationOf reads the replication out of one planned change. It is
// the PLANNED value and not the absence of a replacement: the mock provider
// never plans a replacement, and the answer this release is about is which
// replication the plan leaves the container with.
func plannedReplicationOf(t *testing.T, rows map[string]plannedResource, key string) (plannedResource, plannedReplication) {
	t.Helper()
	rc, ok := rows[secretAddress(key)]
	if !ok {
		t.Fatalf("the plan names no container %q: %v", key, slices.Sorted(maps.Keys(rows)))
	}
	b, err := json.Marshal(rc.After["replication"])
	if err != nil {
		t.Fatal(err)
	}
	// The provider keeps the field as a list of blocks with at most one entry.
	var blocks []plannedReplication
	if err := json.Unmarshal(b, &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("%s: the plan carries %d replication blocks, want one: %s", key, len(blocks), b)
	}
	return rc, blocks[0]
}

// keeps is the whole assertion every case below makes: the plan leaves the
// container with a replication of its own rather than replacing it, and says
// which one. `want` is the state's own value, rendered back through the plan.
func keeps(t *testing.T, rows map[string]plannedResource, key string, want func(plannedReplication) bool, why string) {
	t.Helper()
	rc, got := plannedReplicationOf(t, rows, key)
	if slices.Contains(rc.Actions, "delete") {
		t.Errorf("%s: the plan replaces the container (actions %v), which deletes every version in it — %s", key, rc.Actions, why)
	}
	if !want(got) {
		t.Errorf("%s: the plan's replication is %+v — %s", key, got, why)
	}
}

func inTheRegion(region string) func(plannedReplication) bool {
	return func(r plannedReplication) bool {
		got, ok := r.regionOf()
		return ok && got == region
	}
}

func stillAutomatic(r plannedReplication) bool { return r.automatic() }

// TestANewSecretIsInTheRegion (LP5a, D3082): a container created from now on
// is replicated in the installation's region. This is the test the plan names
// as the red step — before this release the module declares `auto {}` and
// finds no region at all.
func TestANewSecretIsInTheRegion(t *testing.T) {
	spec := installationSpecWith(t, twoProjectSecretsLC)
	_, rows := installationPlan(t, spec)
	for _, key := range []string{"anthropic-api-key", "github-app-key"} {
		if _, r := plannedReplicationOf(t, rows, key); !inTheRegion(spec.Region)(r) {
			t.Errorf("%s: a container created by this release is replicated %+v, want a single user-managed replica in %s", key, r, spec.Region)
		}
	}
}

// TestAnExistingSecretKeepsItsOwnReplication (LP5a, F9, R5): the container an
// installation ALREADY has — every one of them automatic, because that is what
// every declaration before this release wrote — keeps its automatic
// replication. A plan that moved it would replace the container and delete
// every version in it.
func TestAnExistingSecretKeepsItsOwnReplication(t *testing.T) {
	spec := installationSpecWith(t, twoProjectSecretsLC)
	prior := map[string][]any{"anthropic-api-key": automaticReplication(), "github-app-key": automaticReplication()}
	_, rows := planWithSecretsState(t, spec, nil, prior)
	for _, key := range []string{"anthropic-api-key", "github-app-key"} {
		keeps(t, rows, key, stillAutomatic, "an automatic container must stay automatic")
	}
}

// TestAnAdoptedSecretPlansNoChange (LP5a, F9, R5): a container replicated
// somewhere else — the one an installation adopts, or one an owner moved by
// hand — is left exactly where it is, and the installation's own region is not
// written over it.
func TestAnAdoptedSecretPlansNoChange(t *testing.T) {
	spec := installationSpecWith(t, twoProjectSecretsLC)
	elsewhere := "europe-west1"
	prior := map[string][]any{"anthropic-api-key": replicatedIn(elsewhere), "github-app-key": replicatedIn(elsewhere)}
	_, rows := planWithSecretsState(t, spec, nil, prior)
	for _, key := range []string{"anthropic-api-key", "github-app-key"} {
		keeps(t, rows, key, inTheRegion(elsewhere), "a container replicated elsewhere keeps its own replication, and the installation's region is never written over it")
	}
}

// TestAMixedInstallationPlansNoSecretChange (LP5a, F9, R5): one installation
// whose containers are in different places — an old automatic one beside one
// that was moved — plans no change to either. This is the case a per-resource
// reading of the field cannot see: the value kept is the container's own, not
// the installation's.
func TestAMixedInstallationPlansNoSecretChange(t *testing.T) {
	spec := installationSpecWith(t, twoProjectSecretsLC)
	elsewhere := "europe-west1"
	prior := map[string][]any{"anthropic-api-key": automaticReplication(), "github-app-key": replicatedIn(elsewhere)}
	_, rows := planWithSecretsState(t, spec, nil, prior)
	keeps(t, rows, "anthropic-api-key", stillAutomatic, "the automatic container keeps its own replication")
	keeps(t, rows, "github-app-key", inTheRegion(elsewhere), "the container that was moved keeps its own replication")
}

// TestForgetPlansNoSecretChange (LP5a): the rollback's plan (--forget, which
// turns behaviour off) leaves the containers alone too. It is a different spec
// through the same module, and the containers are exactly what a rollback must
// not touch: the versions in them are the credentials an installation runs on.
func TestForgetPlansNoSecretChange(t *testing.T) {
	spec := ForgetSpec(installationSpecWith(t, twoProjectSecretsLC))
	elsewhere := "europe-west1"
	prior := map[string][]any{"anthropic-api-key": automaticReplication(), "github-app-key": replicatedIn(elsewhere)}
	_, rows := planWithSecretsState(t, spec, nil, prior)
	keeps(t, rows, "anthropic-api-key", stillAutomatic, "the rollback's plan leaves an automatic container alone")
	keeps(t, rows, "github-app-key", inTheRegion(elsewhere), "the rollback's plan leaves a container that was moved alone")
}
