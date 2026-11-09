package tf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBriefNamesEveryChangeInWordsAndNoAddress: the plan block a person reads
// is the counts and one plain-words line per change they must look at, and it
// is rendered from a plan whose every change carries a Terraform address. No
// address, no attribute path and no resource type reaches that block — the
// owner's feedback of 2026-11-09: an address is Fugaro's own plumbing, named
// in the module's words, and it is Summary's (the log's) job to carry it.
func TestBriefNamesEveryChangeInWordsAndNoAddress(t *testing.T) {
	image := strings.Replace(jobBefore, `"old"`, `"new"`, 1)
	p := mustPlan(t, `{"resource_changes":[
	  {"address":"module.installation.google_storage_bucket.runs","type":"google_storage_bucket","change":{"actions":["create"],"before":null,"after":{"name":"x"}}},
	  {"address":"module.installation.google_cloud_run_v2_job.catalog_check","type":"google_cloud_run_v2_job","change":{"actions":["update"],"before":`+jobBefore+`,"after":`+image+`}},
	  {"address":"module.firebase.google_project_iam_member.admin[\"user:old@example.com\"]","type":"google_project_iam_member","change":{"actions":["delete"],"before":{"role":"roles/owner"},"after":null}}
	]}`)
	got := Brief(p)
	t.Log("\n" + got)

	for _, forbidden := range []string{"module.", "google_", "template.template", "[0]", ".value"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the plan block carries %q, which is Terraform's own shape and not Fugaro's:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"Plan: 1 to create, 1 to update, 1 to delete.\n",
		"⚠ Review these first:\n",
		"  ⚠ the catalog check job's image changes\n",
		"  ⚠ the admin grant to user:old@example.com is removed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan block is missing %q:\n%s", want, got)
		}
	}
	// The block is a screen, not a document: every line is one line.
	for _, l := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if n := len([]rune(l)); n > 100 {
			t.Errorf("a plan line is %d runes, want one line a person reads at a glance: %q", n, l)
		}
	}
}

// TestBriefCountsOnlyWhatThereIsToDo: a zero is left out rather than printed
// as "0 to import", and a plan with nothing in it says so — the same plan
// Summary calls "no changes".
func TestBriefCountsOnlyWhatThereIsToDo(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan string
		want string
	}{
		{"a plan with nothing in it", `{"resource_changes":[]}`, "Plan: no changes.\n"},
		{"no-ops are nothing to do", `{"resource_changes":[
		  {"address":"module.w.google_storage_bucket.runs","type":"google_storage_bucket","change":{"actions":["no-op"]}}]}`, "Plan: no changes.\n"},
		{"the counts a person reads", `{"resource_changes":[
		  {"address":"module.w.google_storage_bucket.a","type":"google_storage_bucket","change":{"actions":["create"]}},
		  {"address":"module.w.google_storage_bucket.b","type":"google_storage_bucket","change":{"actions":["create"]}},
		  {"address":"module.w.google_storage_bucket.c","type":"google_storage_bucket","change":{"actions":["update"],"before":{"labels":{"a":"1"}},"after":{"labels":{"a":"2"}}}}]}`, "Plan: 2 to create, 1 to update.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Brief(mustPlan(t, tc.plan)); got != tc.want {
				t.Errorf("Brief = %q, want %q", got, tc.want)
			}
		})
	}
	if got := Brief(nil); got != "No plan.\n" {
		t.Errorf("Brief(nil) = %q, want the one line a run with no plan gets", got)
	}
}

// TestBriefOverEveryGoldenPlan: every real plan of the three roots renders a
// block with no address, no attribute path and no resource type in it, and one
// that names each change it asks a person to review. The golden plans are the
// provider's own output cut to its resource_changes, so this is the block as a
// real `fugaro update` renders it, not a shape a test wrote.
func TestBriefOverEveryGoldenPlan(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "golden", "*.plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 10 {
		t.Fatalf("only %d golden plans: this test would pass over an empty directory", len(names))
	}
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			var p Plan
			if err := json.Unmarshal(b, &p); err != nil {
				t.Fatal(err)
			}
			got := Brief(&p)
			for _, forbidden := range []string{"module.", "google_", "template.", "resource_changes"} {
				if strings.Contains(got, forbidden) {
					t.Errorf("the plan block carries %q:\n%s", forbidden, got)
				}
			}
			if !strings.HasPrefix(got, "Plan: ") {
				t.Errorf("the plan block does not open on its counts:\n%s", got)
			}
			// A plan the block says has nothing to do asks for no review, and
			// one with changes that need none does not invent a review either.
			if strings.Contains(got, "⚠ Review these first:") && strings.Contains(got, "Plan: no changes.") {
				t.Errorf("a plan with no changes asks for a review:\n%s", got)
			}
		})
	}
}

// TestEveryAllowedTypeHasANoun: the noun table is what the plan block names a
// change by, and every resource type our modules declare is in it. A module
// that adds one has to decide what a person reads for it, the same rule
// allowedTypes itself is held to — the fallback (wordsOf) is worse reading,
// not a name.
func TestEveryAllowedTypeHasANoun(t *testing.T) {
	for typ := range allowedTypes {
		if _, ok := nouns[typ]; !ok {
			t.Errorf("%s is in allowedTypes and has no noun: a plan that creates or changes one would print %q", typ, wordsOf(typ))
		}
	}
	for typ := range nouns {
		if !allowedTypes[typ] {
			t.Errorf("nouns has %s and allowedTypes does not: a type no module declares is a decision nobody made", typ)
		}
	}
}

// TestThingNamesAnIndexedGrantByItsMember: an indexed grant's address carries a
// whole address of its own inside the index, dots and all, and the block names
// the member rather than the tail of that address.
func TestThingNamesAnIndexedGrantByItsMember(t *testing.T) {
	for _, tc := range []struct {
		typ, address, want string
	}{
		{"google_project_iam_member", `module.firebase.google_project_iam_member.admin["user:old@example.com"]`, "the admin grant to user:old@example.com"},
		{"google_project_iam_member", `module.firebase.google_project_iam_member.admin["serviceAccount:a@b.iam.gserviceaccount.com"]`, "the admin grant to serviceAccount:a@b.iam.gserviceaccount.com"},
		{"google_storage_bucket_iam_member", `module.w.google_storage_bucket_iam_member.runs["roles/storage.objectViewer"]`, "the runs grant to roles/storage.objectViewer"},
		{"google_storage_bucket", `module.installation.google_storage_bucket.runs`, "the runs bucket"},
		{"google_cloud_run_v2_job", `module.compute.google_cloud_run_v2_job.catalog_check`, "the catalog check job"},
		{"google_cloud_run_v2_service", `module.installation.google_cloud_run_v2_service.credentials`, "the credentials service"},
	} {
		if got := thing(ResourceChange{Address: tc.address, Type: tc.typ}); got != tc.want {
			t.Errorf("thing(%s) = %q, want %q", tc.address, got, tc.want)
		}
	}
}
