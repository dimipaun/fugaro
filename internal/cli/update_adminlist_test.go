package cli

// The derived admin list under `fugaro update` (D1940): the roster's
// project-scoped admins, written to the object the intake reads, and rewritten
// from the roster every time the door runs — which is what "it lags a role
// change until update runs" means: until then the object holds the list the
// last update derived, and the run after the change replaces it.

import (
	"context"
	"slices"
	"strings"
	"testing"

	roleaccess "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
)

// TestUpdateRewritesListAfterRoleChange: the run that follows a role change
// writes the list the roster holds then — the whole of it, not a merge with
// what was there — and it says that this is the moment a role change reaches
// the intake.
//
// The fixture is the lag itself: the object holds the list the previous update
// derived, from a roster that has since changed. A person on it has been
// demoted to viewer, another has been made a project admin, and a third has
// been dropped from the roster altogether. The run replaces the object, so what
// the intake reads afterwards is the roster's answer at the moment of the run
// and nothing the earlier list held.
func TestUpdateRewritesListAfterRoleChange(t *testing.T) {
	r := newConvergeRig(t, v4Node())
	// The roster as it stands now: two project admins, a viewer and an admin
	// whose scope is every repository rather than the project — only the first
	// two are the intake's business (D1935, D1940).
	setRoster(t, r,
		rosterGrant("owner@example.com", "admin", "project"),
		rosterGrant("deputy@example.com", "admin", "project"),
		rosterGrant("demoted@example.com", "viewer", "project"),
		rosterGrant("root@example.com", "admin", "all-repos"),
	)
	// What the previous update derived, before these changes: the person it has
	// since demoted, and one it no longer holds at all.
	setAdminList(t, r, []roleaccess.Admin{{Email: "gone@example.com"}, {Email: "demoted@example.com"}})

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
	assertAdminList(t, r, []string{"deputy@example.com", "owner@example.com"}, "after the update that follows the role change")

	// And it says so: the list is a derivation of the roster, so a person who
	// grants a role has to be told that the intake still holds the old answer
	// until the next update — the alternative is an admin who cannot write a
	// shared secret and no line saying why.
	if !strings.Contains(out, roleaccess.PathAdminList) {
		t.Errorf("the run does not name %s, the object it wrote: the line is how a person knows the intake was given the list\n%s", roleaccess.PathAdminList, out)
	}
	if !strings.Contains(out, "the next `fugaro update`") {
		t.Errorf("the run does not say the list lags a role change until the next `fugaro update`:\n%s", out)
	}
}

// setRoster writes the grants node the update reads, as `fugaro project access
// set` leaves it: the fixture's roster, not this test's own copy of one.
func setRoster(t *testing.T, r *convergeRig, grants ...map[string]any) {
	t.Helper()
	r.db.Set(roleaccess.PathNode, map[string]any{"grants": grants})
}

// rosterGrant is one stored grant, in the node's own spelling (store.go's
// nodeGrant).
func rosterGrant(address, role, scope string) map[string]any {
	return map[string]any{"user": "user:" + address, "role": role, "scope": scope}
}

// adminListBucket opens the runs bucket the rig's config names. The list is a
// bucket object, so a test reads and writes it through the same opener the
// update does, from the same local config.
func adminListBucket(t *testing.T, r *convergeRig) *blobx.Bucket {
	t.Helper()
	b, err := blobx.Open(context.Background(), projectConfigOf(t, r).BucketURL())
	if err != nil {
		t.Fatalf("opening the runs bucket %s: %v", projectConfigOf(t, r).BucketURL(), err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// setAdminList writes the object as a previous update would have left it.
func setAdminList(t *testing.T, r *convergeRig, list []roleaccess.Admin) {
	t.Helper()
	raw, err := roleaccess.EncodeAdminList(list)
	if err != nil {
		t.Fatalf("encoding the fixture list: %v", err)
	}
	b := adminListBucket(t, r)
	if err := b.Put(context.Background(), roleaccess.PathAdminList, raw, "application/json"); err != nil {
		t.Fatalf("writing the fixture %s: %v", roleaccess.PathAdminList, err)
	}
}

// assertAdminList reads the object back and compares the addresses it holds
// with want, sorted, which is the whole of what the intake decides on.
func assertAdminList(t *testing.T, r *convergeRig, want []string, when string) {
	t.Helper()
	b := adminListBucket(t, r)
	raw, _, err := b.ReadMaxStrict(context.Background(), roleaccess.PathAdminList, roleaccess.MaxAdminListBytes)
	if err != nil {
		t.Fatalf("%s there is no %s in the runs bucket (%v): the intake is left with no list, so it admits nobody to the shared prefix",
			when, roleaccess.PathAdminList, err)
	}
	list, err := roleaccess.ParseAdminList(raw)
	if err != nil {
		t.Fatalf("%s %s does not parse (%v):\n%s", when, roleaccess.PathAdminList, err, raw)
	}
	got := make([]string, 0, len(list))
	for _, a := range list {
		got = append(got, a.Email)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s %s holds %v, want %v:\n%s", when, roleaccess.PathAdminList, got, want, raw)
	}
}
