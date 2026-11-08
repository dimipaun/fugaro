package tf

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// TestCatalogCheckAddRepoGolden: the plan the managed gate refused before the
// cover knew this grant — a repository added to a project whose installation
// already has the catalog check job (D1148) — is admitted, and for the reason
// that matters: its one create is the ADDED repository's records grant, under
// that repository's own condition and with the job's account named outright.
//
// The name is the whole difference between this plan and a first deploy's
// (scripts/gen-golden-plans.sh plans it against the state one repository
// earlier, so the account is already applied and its identity is in the
// state), and the condition is what keeps the row from being a hole: the same
// account under any other condition is refused (TestCatalogCheckRecordsGrant),
// which is what this golden would show if the module's own pair stopped being
// the pair the check reads.
//
// Mutation (run, restore): take the toCatalogCheck bit off the objectUser row —
// the state the gate was in before the row existed — and this plan is refused
// again, on its one create and on nothing else: `grants the role
// "roles/storage.objectUser" to a kind of principal the installation's modules
// do not grant it to`, the sentence the owner read. What NARROWS the row
// (the condition, the title's slug, the account) is
// TestCatalogCheckRecordsGrant's, one refused shape per case.
//
// The cover's two other changes, the catalog check job's own update (its
// environment carries the assignment the added repository is in) and the
// project marker's, are the job's own and an artifact of the synthetic state
// (the marker's hashes are unknown in the plan the state was derived from), so
// they are not what this test is about; a plan that grew a third create fails
// it.
func TestCatalogCheckAddRepoGolden(t *testing.T) {
	p := goldenPlan(t, "installation-catalog-add-repo")
	var creates []ResourceChange
	for _, rc := range p.ResourceChanges {
		if slices.Equal(rc.Change.Actions, []string{"create"}) {
			creates = append(creates, rc)
		}
	}
	if len(creates) != 1 {
		var got []string
		for _, rc := range creates {
			got = append(got, rc.Address)
		}
		t.Fatalf("the plan creates %v, want the one records grant this golden is about", got)
	}
	rc := creates[0]
	const prefix = `module.installation.google_storage_bucket_iam_member.catalog_check_records["`
	rest, ok := strings.CutPrefix(rc.Address, prefix)
	if !ok {
		t.Fatalf("the one create is %s, want a repository's records grant (%s…)", rc.Address, prefix)
	}
	slug, ok := strings.CutSuffix(rest, `"]`)
	if !ok || slug == "" {
		t.Fatalf("the one create is %s, which names no repository slug", rc.Address)
	}
	wantMember := "serviceAccount:" + CatalogCheckAccountID + "@proj-1234.iam.gserviceaccount.com"
	if got := str(rc.Change.After["member"]); got != wantMember {
		t.Errorf("the created grant is for %q, want %q: the plan that adds a repository to an installation that already has the job reads the account's name out of its state, and a computed member is a first deploy's shape", got, wantMember)
	}
	if got := str(rc.Change.After["role"]); got != "roles/storage.objectUser" {
		t.Errorf("the created grant's role is %q, want roles/storage.objectUser: the job reads a record and writes its check beside it", got)
	}
	conds := objects(rc.Change.After["condition"])
	if len(conds) != 1 {
		t.Fatalf("the created grant carries %d conditions, want the one repository's", len(conds))
	}
	if got, want := str(conds[0]["title"]), gcp.CatalogRecordsConditionTitle(slug); got != want {
		t.Errorf("the created grant's condition title is %q, want %q", got, want)
	}
	if got, want := str(conds[0]["expression"]), gcp.CatalogRecordsCondition("fugaro-runs-proj-1234", slug); got != want {
		t.Errorf("the created grant's condition is %q, want %q", got, want)
	}

	if got := goldenCover().NotCovered(p, nil); len(got) != 0 {
		t.Fatalf("a real plan that adds a repository to an installation that already has the catalog check job is not covered: %q", got)
	}
}
