package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// LP9 (docs/location-preference/implementation-plan.md): where a member's
// database is comes from the member. An installation already knows — its
// budget.rtdb_url is the database its own runs read — so linking it records the
// region that URL names, and a workspace whose members are in different places
// links each member at its own host with no flag (F14, D3084). The fixture is
// workspace_test.go's: aurora is the member, and its own config is the file the
// region is read from.

// workspaceMemberDatabase writes the member's own budget block, as that
// installation's `fugaro update` writes it: the one key a member's region is
// read from.
func workspaceMemberDatabase(t *testing.T, memberPath, rtdbURL string) {
	t.Helper()
	workspaceAppend(t, memberPath, "budget: { mode: observe, per_run_usd: 5, rtdb_url: "+rtdbURL+" }\n")
}

// workspaceOwnerRegion is the region the workspace's table records for member
// aurora after one `workspace project add`, read back through the loader the
// command writes
// through.
func workspaceOwnerRegion(t *testing.T, owner string) string {
	t.Helper()
	lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatalf("%v\n%s", err, readFileString(t, owner))
	}
	if lc.WorkspaceMembers == nil {
		t.Fatalf("no member table was written:\n%s", readFileString(t, owner))
	}
	return (*lc.WorkspaceMembers)["aurora"].DatabaseRegion
}

// TestAMembersDatabaseRegionIsReadFromTheMember is the task's own red step: a
// member whose database is in europe-west1 is linked without a flag and the
// entry carries europe-west1, so the workspace's service reads it at its own
// host. The two regions the Realtime Database offers outside its default are the
// values held here, because they are the two a member's own host can name.
func TestAMembersDatabaseRegionIsReadFromTheMember(t *testing.T) {
	for _, region := range []string{"europe-west1", "asia-southeast1"} {
		t.Run(region, func(t *testing.T) {
			_, aurora, owner := workspaceOwnerHome(t)
			workspaceMemberDatabase(t, aurora, "https://aurora-fb-default-rtdb."+region+".firebasedatabase.app")

			_, out, chainErr := workspaceProjectAddRun(t)
			if got := workspaceOwnerRegion(t, owner); got != region {
				t.Errorf("database region = %q, want %q, the region of the member's own database", got, region)
			}
			// What the write says is what the entry holds, whether the region
			// came from the flag or from the member.
			if !strings.Contains(out, "database region "+region) {
				t.Errorf("the sentence does not say the region the entry now carries:\n%s", out)
			}
			// A member already linked is left as it is (D3084): declaring it
			// again reads the same region out of the same config, so the entry
			// is what it was and the file is not rewritten.
			before := readFileString(t, owner)
			if _, again, _ := workspaceProjectAddRun(t); !strings.Contains(again, "no change") {
				t.Errorf("a repeated declaration is not a no-op:\n%s", again)
			}
			if readFileString(t, owner) != before {
				t.Errorf("a repeated declaration rewrote the member's entry:\n%s", readFileString(t, owner))
			}
			workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
		})
	}
}

// TestTheFlagStillWins: the owner who knows where the member's database is says
// so, and the declaration is what is written — the flag is not passed over in
// favour of what the member's own config happens to say.
func TestTheFlagStillWins(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	workspaceMemberDatabase(t, aurora, "https://aurora-fb-default-rtdb.asia-southeast1.firebasedatabase.app")

	_, out, chainErr := workspaceProjectAddRun(t, "--database-region", "europe-west1")
	if got := workspaceOwnerRegion(t, owner); got != "europe-west1" {
		t.Errorf("database region = %q, want europe-west1, the value the flag gave", got)
	}
	workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
}

// TestARegionThatIsNoDatabaseLocationIsRefused: a member's URL names a region
// the Realtime Database does not exist in, so recording it would point the
// workspace's service at a host that is not the member's database. The refusal
// prints the fix, and the fix it prints is one.
func TestARegionThatIsNoDatabaseLocationIsRefused(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	workspaceMemberDatabase(t, aurora, "https://aurora-fb-default-rtdb.us-east1.firebasedatabase.app")
	before := readFileString(t, owner)

	out, _, err := execute(t, workspaceProjectAddArgs()...)
	if err == nil || !strings.Contains(err.Error(), "us-east1, which is not a Realtime Database location") {
		t.Fatalf("a member whose database names us-east1 was linked: %v\n%s", err, out)
	}
	if !strings.Contains(err.Error(), "--database-region") {
		t.Errorf("the refusal does not print the fix:\n%v", err)
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the refusal wrote the table:\n%s", after)
	}
	// The fix the refusal names is one: the same member, declared with the
	// region its database is in, is linked.
	_, out, chainErr := workspaceProjectAddRun(t, "--database-region", "europe-west1")
	if got := workspaceOwnerRegion(t, owner); got != "europe-west1" {
		t.Errorf("database region = %q, want the one the flag gave", got)
	}
	workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
}

// TestAConfigFromAnotherFirebaseProjectIsRefused: the config a region is read
// out of is found by the member's name, and a project id is a name another
// installation may already have used — a member declared once under that name
// and re-created under a new Firebase project, or a workspace's member whose
// config was never cleaned up. Another Firebase project's database says nothing
// about where this member's is, and the region read out of one would derive a
// host that is nobody's database: credsvc's workspaceDatabase builds
// `<firebase_project>-default-rtdb` out of the project and the region together.
// So the pair is held to the declaration's project — the budget's own
// firebase_project, and the label of its rtdb_url — and a mismatch is refused
// with the flag printed rather than recorded.
func TestAConfigFromAnotherFirebaseProjectIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, budget, want string
	}{
		{
			"the config names another Firebase project",
			"budget: { mode: observe, per_run_usd: 5, firebase_project: borealis-fb, rtdb_url: https://aurora-fb-default-rtdb.europe-west1.firebasedatabase.app }\n",
			"names the Firebase project borealis-fb",
		},
		{
			"the config's database is another Firebase project's",
			"budget: { mode: observe, per_run_usd: 5, rtdb_url: https://borealis-fb-default-rtdb.europe-west1.firebasedatabase.app }\n",
			"is not the database of the Firebase project this declaration names",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, aurora, owner := workspaceOwnerHome(t)
			workspaceAppend(t, aurora, tc.budget)
			before := readFileString(t, owner)

			out, _, err := execute(t, workspaceProjectAddArgs()...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("a config that is not the member's was read to place its database: %v\n%s", err, out)
			}
			if !strings.Contains(err.Error(), "--database-region") {
				t.Errorf("the refusal does not print the fix:\n%v", err)
			}
			if after := readFileString(t, owner); after != before {
				t.Errorf("the refusal wrote the table:\n%s", after)
			}
			// The fix the refusal names is one: the same member, declared with
			// the region its database is in, is linked.
			_, out, chainErr := workspaceProjectAddRun(t, "--database-region", "europe-west1")
			if got := workspaceOwnerRegion(t, owner); got != "europe-west1" {
				t.Errorf("database region = %q, want the one the flag gave", got)
			}
			workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
		})
	}
}

// workspaceLinkedEntry is the table entry an earlier declaration wrote, region
// and all: the entry a re-declaration is read against when the question is
// whether it keeps what it records.
const workspaceLinkedEntry = `workspace_members:
  aurora:
    job_service_accounts:
      - account: marker-old@aurora-gcp.iam.gserviceaccount.com
        repos:
          - marker-org/aurora
    database_region: europe-west1
    firebase_project: aurora-fb
    fp: aurora
    exchange_url: https://exchange.example.com/v1
`

// TestAMemberWhoseConfigIsNotHereRecordsNoRegion: a member's config is another
// machine's file. A member this machine holds no config for and the table does
// not declare yet is linked with no region — what is read is a file that happens
// to be here — and one the table already declares keeps the region it records: a
// workspace owner adding a job account must not have the member's database read
// at another host because this machine cannot see where it is (D3084).
func TestAMemberWhoseConfigIsNotHereRecordsNoRegion(t *testing.T) {
	t.Run("a member the table does not declare", func(t *testing.T) {
		_, aurora, owner := workspaceOwnerHome(t)
		if err := os.Remove(aurora); err != nil {
			t.Fatal(err)
		}

		_, out, chainErr := workspaceProjectAddRun(t)
		if got := workspaceOwnerRegion(t, owner); got != "" {
			t.Errorf("database region = %q, want none: this machine holds no config for the member, so its database URL was not read", got)
		}
		if strings.Contains(readFileString(t, owner), "database_region") {
			t.Errorf("the write spelled an absent region as a key:\n%s", readFileString(t, owner))
		}
		workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
	})

	t.Run("a member already linked", func(t *testing.T) {
		_, aurora, owner := workspaceOwnerHome(t)
		workspaceAppend(t, owner, workspaceLinkedEntry)
		if err := os.Remove(aurora); err != nil {
			t.Fatal(err)
		}

		_, out, chainErr := workspaceProjectAddRun(t)
		if got := workspaceOwnerRegion(t, owner); got != "europe-west1" {
			t.Errorf("database region = %q, want europe-west1: the entry already records where the member's database is, and this declaration cannot read it", got)
		}
		// And the flag is how the owner says otherwise.
		_, out, chainErr = workspaceProjectAddRun(t, "--database-region", "asia-southeast1")
		if got := workspaceOwnerRegion(t, owner); got != "asia-southeast1" {
			t.Errorf("database region = %q, want asia-southeast1, the value the flag gave", got)
		}
		workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
	})
}

// TestAMemberWhoseConfigNamesNoDatabaseKeepsTheRegionItRecords: a member's own
// config is right here and its budget names no Realtime Database at all — an
// installation that never wrote that key. That is silence about where the
// member's database is, not a member whose database is in the default location,
// so an entry the table already declares keeps the region it records: reading
// the silence as "no region" would move where the workspace's service reads that
// member's database, which is what a re-declaration must never do (D3084).
func TestAMemberWhoseConfigNamesNoDatabaseKeepsTheRegionItRecords(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, workspaceLinkedEntry)
	workspaceAppend(t, aurora, "budget: { mode: observe, per_run_usd: 5 }\n")

	_, out, chainErr := workspaceProjectAddRun(t)
	if got := workspaceOwnerRegion(t, owner); got != "europe-west1" {
		t.Errorf("database region = %q, want europe-west1: the member's own config names no database, so nothing says the region the entry records is wrong", got)
	}
	workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
}

// TestALegacyHostRecordsNoRegion: a member whose database URL names no region —
// the legacy host of the default location, that location's regional host, or the
// loopback address of an emulator — records no region at all, and never
// us-central1: credsvc's workspaceDatabase builds a regional host out of any
// non-empty region, so us-central1 (which has none) would derive a host that is
// not a database.
func TestALegacyHostRecordsNoRegion(t *testing.T) {
	for _, tc := range []struct {
		name, url, extra string
	}{
		{"the legacy host", "https://aurora-fb-default-rtdb.firebaseio.com", ""},
		{"the default location's regional host", "https://aurora-fb-default-rtdb.us-central1.firebasedatabase.app", ""},
		{"an emulator", "http://127.0.0.1:9000", "endpoints: { no_auth: true }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, aurora, owner := workspaceOwnerHome(t)
			workspaceAppend(t, aurora, tc.extra)
			workspaceMemberDatabase(t, aurora, tc.url)

			_, out, chainErr := workspaceProjectAddRun(t)
			if got := workspaceOwnerRegion(t, owner); got != "" {
				t.Errorf("database region = %q, want none: %s names no region, and recording one would derive the member's database from it", got, tc.url)
			}
			// The key is absent rather than written empty: the loader reads an
			// absent key as the project's default database, and a written empty
			// scalar as a value to hold to the region rule.
			if strings.Contains(readFileString(t, owner), "database_region") {
				t.Errorf("the write spelled an absent region as a key:\n%s", readFileString(t, owner))
			}
			workspaceAssertSavedNotLive(t, "workspace project add", out, chainErr)
		})
	}
}
