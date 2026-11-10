package cli

// The derived admin list under `fugaro update` (D1940): the roster's
// project-scoped admins, written to the object the intake reads, and rewritten
// from the roster every time the door runs — which is what "it lags a role
// change until update runs" means: until then the object holds the list the
// last update derived, and the run after the change replaces it.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	roleaccess "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// TestUpdateRevokesWhenTheRosterIsGone: the same lag read the other way. The
// object is rewritten from the roster on every update, so a roster that is
// gone — no node at all, which is a roster with no grants (roleaccess.Read's
// own answer) and never "the list we had" — is a revocation the intake sees at
// the next `fugaro update`, and the step must not read it as "nothing to do"
// and leave the object an earlier update derived in place. What that would
// cost is the fail-closed direction of this step: the intake goes on admitting
// whoever the stale list names, however often the door runs.
//
// The fixture is that staleness: the rig holds no roster node, and the bucket
// holds a list derived from a roster that had one.
func TestUpdateRevokesWhenTheRosterIsGone(t *testing.T) {
	r := newConvergeRig(t, v4Node())
	setAdminList(t, r, []roleaccess.Admin{{Email: "gone@example.com"}})

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
	// The object is rewritten, not deleted: an absent list and a list that
	// holds nobody are different documents, and the intake reads the second.
	assertAdminList(t, r, nil, "after the update that follows the roster's removal")
	if !strings.Contains(out, "admin list: 0 project admin(s)") {
		t.Errorf("the run does not say the list now holds no project admin:\n%s", out)
	}
}

// TestAdminListNotWrittenToAnotherInstallationsBucket: the list is an
// installation-scoped object, and the step keeps the exact-name rule every
// sibling writer of one keeps — the runs bucket has to be this installation's
// own, fugaro-runs-<gcp_project>.
//
// What it holds back is a config that names a foreign installation's bucket: a
// hand edit, or a teammate's config file with the wrong project in it. The
// admins on the list are the ones the intake admits to the shared prefix, so
// publishing them into another project's bucket is publishing them to that
// project's intake — and the write would look like a success here, where the
// bucket it names is reachable, while this project's own intake is left with
// whatever list an earlier update derived.
//
// A config can name that bucket in two places, and they can disagree, so the
// test moves each of them: the runs bucket it declares, and the URL a write
// opens (which a converge keeps as it found it). Either one naming another
// installation's bucket is enough to leave the object alone, and leaving it
// alone is a warning — the HARD RULE is that nothing about this step stops an
// upgrade.
func TestAdminListNotWrittenToAnotherInstallationsBucket(t *testing.T) {
	for name, mutate := range map[string]func(*localcfg.Config){
		"the declared runs bucket": func(lc *localcfg.Config) { lc.RunsBucket = "fugaro-runs-other-project" },
		"the bucket_url it opens":  func(lc *localcfg.Config) { lc.Bucket = "gs://fugaro-runs-other-project" },
	} {
		t.Run(name, func(t *testing.T) {
			out, opened := adminListStep(t, mutate)
			if opened {
				t.Errorf("the step opened the runs bucket %s, which is not this installation's own: the admins on the list are the ones the intake admits to this project's shared prefix, and another installation's intake reads its own bucket", "fugaro-runs-other-project")
			}
			if !strings.Contains(out, "fugaro-runs-other-project") {
				t.Errorf("the run does not say which bucket it refused, so a person whose config names the wrong one has nothing to go on:\n%s", out)
			}
		})
	}
}

// adminListStep runs the step over a config of the test's own, with the roster
// seeded in a fake database and the bucket opener recorded rather than used. It
// returns what the run printed and whether the write reached a bucket at all,
// which is the difference between "the step refused the bucket" and "the step
// would have written".
func adminListStep(t *testing.T, mutate func(*localcfg.Config)) (out string, opened bool) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "version": 1, "project": initProjectName, "gcp_project": initProject})
	setRosterOn(t, db, rosterGrant("owner@example.com", "admin", "project"))
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		RunsBucket: "fugaro-runs-" + initProject,
		Bucket:     "file://" + t.TempDir(),
		Budget:     &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints:  localcfg.Endpoints{NoAuth: true},
	}
	mutate(lc)
	prev := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		opened = true
		return blobx.Open(ctx, url)
	}
	t.Cleanup(func() { sharedBucketOpener = prev })

	var buf strings.Builder
	e := &initEngine{r: &initRun{o: &initOptions{door: updateDoor.name}, w: &buf}, lc: lc, c: &infra.Clients{}}
	e.writeAdminList(context.Background())
	return buf.String(), opened
}

// TestSetupWritesNoAdminList: the step is `fugaro update`'s. `fugaro setup`
// converges the same installation through the same hook (init_stages.go's
// outcome), so the door guard is what keeps it out of the list — and what this
// test holds: the roster below is seeded and the runs bucket is one a write
// would reach, so a step that ran under the setup door would leave the object.
func TestSetupWritesNoAdminList(t *testing.T) {
	r := newConvergeRig(t, v4Node())
	setRoster(t, r, rosterGrant("owner@example.com", "admin", "project"))

	out, _, err := executeStdin(t, "", "setup", "--yes")
	if err != nil {
		t.Fatalf("fugaro setup failed over an installation that exists (%v)\n%s", err, out)
	}
	b := adminListBucket(t, r)
	if _, _, err := b.ReadMaxStrict(context.Background(), roleaccess.PathAdminList, roleaccess.MaxAdminListBytes); !errors.Is(err, blobx.ErrNotExist) {
		t.Errorf("%s exists after fugaro setup (%v): the roster's admins are the upgrade's to publish, and a project the setup door just created has no roster to derive them from",
			roleaccess.PathAdminList, err)
	}
	if strings.Contains(out, roleaccess.PathAdminList) {
		t.Errorf("fugaro setup names %s, the object only fugaro update writes:\n%s", roleaccess.PathAdminList, out)
	}
}

// setRoster writes the grants node the update reads, as `fugaro project access
// set` leaves it: the fixture's roster, not this test's own copy of one.
func setRoster(t *testing.T, r *convergeRig, grants ...map[string]any) {
	t.Helper()
	setRosterOn(t, r.db, grants...)
}

// setRosterOn is setRoster over any fake database, so the step's own test seeds
// the roster the same way the converge's does.
func setRosterOn(t *testing.T, db *gcpfake.RTDB, grants ...map[string]any) {
	t.Helper()
	db.Set(roleaccess.PathNode, map[string]any{"grants": grants})
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
