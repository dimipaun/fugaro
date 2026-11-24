package infra

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// buildIdentityAccessorRow is one of the accessors IS8 takes away, as the
// installation root's plan spells it: the identity's own grant on one
// repository's provider credential, keyed by the repository's records slug
// ("acme/app") or by that slug's own container ("acme/app/own").
func buildIdentityAccessorRow(key string) string {
	return `module.installation.google_secret_manager_secret_iam_member.build_identity_git_secret["` + key + `"]`
}

// buildIdentityAccessorChange is one such row as a plan carries it: a
// google_secret_manager_secret_iam_member on the identity, holding the
// secretAccessor on the credential the key names.
func buildIdentityAccessorChange(addr string, actions ...string) tf.ResourceChange {
	return tf.ResourceChange{Address: addr, Type: "google_secret_manager_secret_iam_member", Change: tf.Change{
		Actions: actions,
		Before: map[string]any{
			"member": "serviceAccount:fugaro-build-identity@fp-1234.iam.gserviceaccount.com",
			"role":   "roles/secretmanager.secretAccessor",
		},
	}}
}

// TestBuildIdentityAccessorAllowDeleteNamesOnlyTheRemovedRows is IS8's own
// claim about the ordinary project's path: the plan that lands the change
// deletes every accessor the installation's state holds, and
// BuildIdentityAccessorAllowDelete names exactly those, so the guard admits the
// update's own removal (CLAUDE.md's HARD RULE: one `fugaro update`, no flag).
// The red step: with no list the guard refuses the deletes, and with the list
// it admits them.
func TestBuildIdentityAccessorAllowDeleteNamesOnlyTheRemovedRows(t *testing.T) {
	plain, own := buildIdentityAccessorRow("acme/app"), buildIdentityAccessorRow("acme/app/own")
	// A row whose name merely starts the same way: the bracket in the prefix
	// is what keeps the two apart, so the list may not name it.
	neighbour := "module.installation.google_secret_manager_secret_iam_member.build_identity_git_secret_extra[0]"
	// The identity's other reach, which stays: the shared registry writer.
	registry := "module.installation.google_artifact_registry_repository_iam_member.build_identity_shared_writer"
	plan := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		buildIdentityAccessorChange(plain, "delete"),
		buildIdentityAccessorChange(own, "delete"),
		buildIdentityAccessorChange(neighbour, "delete"),
		buildIdentityAccessorChange(registry, "delete"),
		// An accessor the plan CREATES (a repository the catalog check gains
		// while the state holds none): not a removal, so not this list's.
		buildIdentityAccessorChange(buildIdentityAccessorRow("acme/new"), "create"),
	}}
	want := []string{plain, own}
	if got := BuildIdentityAccessorAllowDelete(plan); !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The guard refuses both rows the list withholds, so the list is what
	// admits the update's own and not the shape of the addresses.
	if err := tf.Guard(plan, nil); err == nil {
		t.Fatal("the guard admitted the update's deletes with no list")
	}
	err := tf.Guard(plan, want)
	if err == nil {
		t.Fatal("the guard admitted a plan that deletes rows this list does not name")
	}
	for _, addr := range []string{neighbour, registry} {
		if !strings.Contains(err.Error(), addr) {
			t.Errorf("the refusal does not name %s, which the list withholds: %v", addr, err)
		}
	}
	// The update's own deletes alone, which is what a converged installation's
	// plan carries: admitted, so a plain `fugaro update` applies it.
	only := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		buildIdentityAccessorChange(plain, "delete"),
		buildIdentityAccessorChange(own, "delete"),
	}}
	if err := tf.Guard(only, BuildIdentityAccessorAllowDelete(only)); err != nil {
		t.Fatalf("the guard refused the update's own deletes: %v", err)
	}
	if got := BuildIdentityAccessorAllowDelete(nil); got != nil {
		t.Fatalf("the list named %q for no plan", got)
	}
}
