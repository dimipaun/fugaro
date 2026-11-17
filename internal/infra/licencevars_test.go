package infra

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The licence renewal job's block is the installation root's K4.2 section — the
// account fugaro-licence, the job fugarolicence, its schedule, the history image
// and the container `fugaro licence set` writes into — and CarriedLicence is how
// every path that re-serializes the root's tfvars keeps it. It is the same shape
// of carry as CarriedOrchestrator and CarriedIntake: a read-modify-write of ONE
// key, taken from the documents the workdir holds, so that a document rebuilt
// from a spec does not forget a job the installation holds. The spec composes the
// block only while the coordinator's own readiness holds (internal/cli's
// licenceJob), and that readiness is recomputed on every run from the history
// image, the repositories the config records and the App ID it holds — so the
// state this carry is for is an ordinary one, not a hand-edited document.
//
// Mutation (run, restore): drop the `doc[LicenceKey] = block` write and the
// first half of this test reports a document with no licence in it.
func TestCarriedLicenceCarriesTheBlockTheInstallationHolds(t *testing.T) {
	const held = `{"project": "proj-1234", "licence": {"account_id": "fugaro-licence", "job": "fugarolicence", "image": "us-east5-docker.pkg.dev/proj-1234/fugaro/history:latest", "scheduler_job": "fugaro-licence-renew", "scheduler_region": "us-east5", "read_condition": {"title": "read", "expression": "resource.name.startsWith('projects/_/buckets/runs-proj-1234/objects/fugaro/')"}, "write_condition": {"title": "write", "expression": "resource.name == 'projects/_/buckets/runs-proj-1234/objects/fugaro/licence.json'"}, "renewal_secret": "proj-1234-licence-renewal-0f1e2d"}}`
	const fresh = `{"project": "proj-1234", "region": "us-east5"}`

	got, err := CarriedLicence([]byte(fresh), []byte(held))
	if err != nil {
		t.Fatal(err)
	}
	var doc, previous map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the result is not a tfvars document: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(held), &previous); err != nil {
		t.Fatal(err)
	}
	block, _ := doc[LicenceKey].(map[string]any)
	if block == nil {
		t.Fatalf("the block the installation holds is not in the document this run hands Terraform:\n%s. The account, the job and the container are STATE, and a document without the key is the plan of their removal — the container carries prevent_destroy, so the plan is refused rather than applied, and it is refused on every later update of an installation whose coordinator's readiness has gone false", got)
	}
	if !reflect.DeepEqual(block, previous[LicenceKey]) {
		t.Errorf("the carried block is %v, want it whole as the installation held it (%v): the block is the installation's own record of a job it holds, and a run that recomposed it would be re-deciding an account's grants", block, previous[LicenceKey])
	}

	// And nothing else moved: every other entry of the fresh document is still
	// there, and no key beside the one was added.
	var want map[string]any
	if err := json.Unmarshal([]byte(fresh), &want); err != nil {
		t.Fatal(err)
	}
	delete(doc, LicenceKey)
	if !mapsEqual(want, doc) {
		t.Errorf("the carried document changed beyond the one key:\nwant %v\ngot  %v", want, doc)
	}

	// A document that carries the key of its own stands: that is the ordinary
	// run, whose licenceJob composed the block from the spec, and this carry
	// never overwrites it.
	own := []byte(`{"project": "proj-1234", "licence": {"account_id": "fugaro-licence", "job": "fugarolicence", "renewal_secret": "proj-1234-licence-renewal-9a8b7c"}}`)
	if got2, err := CarriedLicence(own, []byte(held)); err != nil || string(got2) != string(own) {
		t.Errorf("a document with its own block = %s, %v, want it unchanged", got2, err)
	}

	// No key to carry — an installation that never opted into the coordinator,
	// and a workdir with no tfvars at all (nil: a first run) — invents nothing:
	// a block appearing here would be an account, a job and a container nobody
	// asked for.
	for name, prev := range map[string][]byte{"no key in the previous document": []byte(fresh), "no previous document": nil} {
		got2, err := CarriedLicence([]byte(fresh), prev)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got2) != fresh {
			t.Errorf("%s: the document changed: %s", name, got2)
		}
	}
}

// A previous document this program cannot read is an error and not "nothing to
// carry": the direction that fails closed is the one that refuses to write a
// document dropping the job.
func TestCarriedLicenceRefusesAnUnreadablePreviousDocument(t *testing.T) {
	const fresh = `{"project": "proj-1234"}`
	if _, err := CarriedLicence([]byte(fresh), []byte("{not json")); err == nil {
		t.Error("an unreadable previous document carried nothing forward without an error: a run that cannot read the installation's tfvars plans the job's removal, and it has to stop instead")
	}
}
