package cli

import (
	"os"
	"strings"
	"testing"
)

// writeLocations puts a locations record into the rig's config, for an
// installation in the region of the caller's choosing. The record is what
// `fugaro update` and `setup` write from what the cloud holds (LP14), and
// writing one here is how a test states what the cloud was read to hold.
func writeLocations(t *testing.T, r *doctorRig, region, record string) {
	t.Helper()
	data, err := os.ReadFile(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(data), "region: us-east5", "region: "+region, 1)
	if err := os.WriteFile(r.cfgPath, []byte(cfg+record), 0o600); err != nil {
		t.Fatal(err)
	}
}

// doctorLocationsRow is LP12's row as a reader meets it: the check doctor
// reports, the one line `--all` prints for it, and that whole output, so a
// test can say the row is one line and nothing else counts it.
func doctorLocationsRow(t *testing.T) (doctorCheck, string, string) {
	t.Helper()
	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, doctorLocationsCheckID)
	if !ok {
		t.Fatalf("doctor reported no %s row: %+v", doctorLocationsCheckID, o.Checks)
	}
	all, _, err := execute(t, "doctor", "--all", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("%v\n%s", err, all)
	}
	var line string
	for _, l := range strings.Split(all, "\n") {
		if strings.HasPrefix(l, "info "+doctorLocationsCheckID+": ") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("--all prints no %s row:\n%s", doctorLocationsCheckID, all)
	}
	return c, line, all
}

// TestDoctorSaysWhereTheDataIs: LP12's row, on an installation whose record
// holds one thing of each kind. doctor says the region, the place that region
// lies in, and where each thing the record knows about is — one row, in --all
// and in --json, filed as a measurement so no view counts it as a finding.
func TestDoctorSaysWhereTheDataIs(t *testing.T) {
	r := newDoctorRig(t)
	writeLocations(t, r, "us-east5", "locations:\n  firestore: us-east5\n  rtdb: us-central1\n  secrets: [us-east5]\n  log_bucket: global\n")
	c, line, all := doctorLocationsRow(t)

	if !c.OK || c.Severity != "info" || c.Class != doctorClassMeasure {
		t.Errorf("%s = %+v, want an ok, info measurement", doctorLocationsCheckID, c)
	}
	for _, want := range []string{
		"the installation is in us-east5, in the US",
		"its Firestore database is in us-east5",
		"its Realtime Database is in us-central1",
		"its secret containers are replicated in us-east5",
		"its run log bucket is in global",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the row does not say %q:\n%s", want, line)
		}
	}
	// Every one of these is in the installation's place, and the row claims
	// nothing is outside it.
	if strings.Contains(line, "outside") {
		t.Errorf("the row calls a thing outside the place it is in:\n%s", line)
	}
	// One row: the four things and the region are one line, not five.
	if n := strings.Count(all, "info "+doctorLocationsCheckID+": "); n != 1 {
		t.Errorf("--all prints %d %s rows, want one:\n%s", n, doctorLocationsCheckID, all)
	}
}

// TestADatabaseOutsideThePlaceIsInformation: D3091's half of the row. A
// database an installation adopted where it was found keeps its location, and
// doctor says where that is — outside the installation's place and all. It is
// information and never a failure: a clean installation whose record holds a
// database in another place exits 0 on the one ok line, and --strict does not
// change that.
func TestADatabaseOutsideThePlaceIsInformation(t *testing.T) {
	_, root := doctorCleanRig(t, func(r *doctorRig) {
		writeLocations(t, r, "europe-west1", "locations:\n  firestore: us-east5\n  rtdb: us-central1\n  secrets: automatic\n  log_bucket: global\n")
	})
	c, line, _ := doctorLocationsRow(t)

	if !c.OK || c.Severity != "info" || c.Class != doctorClassMeasure {
		t.Errorf("%s = %+v, want an ok, info measurement", doctorLocationsCheckID, c)
	}
	for _, want := range []string{
		"the installation is in europe-west1, in the EU",
		"its Firestore database is in us-east5 (outside the EU)",
		"its Realtime Database is in us-central1 (outside the EU)",
		// Automatic replication lies in no place the mapping names, so it is
		// stated as it is and never marked as outside the EU (D3081, D3092).
		"its secret containers are replicated with " + automaticGloss,
		"its run log bucket is in global",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the row does not say %q:\n%s", want, line)
		}
	}
	if checksFail([]doctorCheck{c}, true) {
		t.Errorf("%s fails doctor --strict: a database outside the place is information (D3091)", doctorLocationsCheckID)
	}

	out, _, err := execute(t, "doctor", "--dir", root)
	if err != nil {
		t.Fatalf("doctor failed on an installation whose database is outside its place: %v\n%s", err, out)
	}
	if out != "fugaro doctor: ok — everything checked is ok\n" {
		t.Errorf("a clean installation with a database outside its place printed:\n%s", out)
	}
}

// TestDoctorWithNoRecordPrintsTheRegionAlone: until LP14 no command writes a
// locations record, so every installation doctor meets today has none. The row
// is then the region and its place and nothing about the things the record
// would name: nothing here knows where the data is, and a row that guessed
// would be exactly the record doctor exists to read.
func TestDoctorWithNoRecordPrintsTheRegionAlone(t *testing.T) {
	newDoctorRig(t)
	c, line, _ := doctorLocationsRow(t)

	if !c.OK || c.Severity != "info" || c.Class != doctorClassMeasure {
		t.Errorf("%s = %+v, want an ok, info measurement", doctorLocationsCheckID, c)
	}
	for _, want := range []string{"the installation is in us-east5, in the US", "no locations record yet"} {
		if !strings.Contains(line, want) {
			t.Errorf("the row does not say %q:\n%s", want, line)
		}
	}
	for _, unwanted := range []string{"Firestore", "Realtime Database", "secret container", "log bucket", "us-central1", "global"} {
		if strings.Contains(line, unwanted) {
			t.Errorf("the row with no record says %q anyway:\n%s", unwanted, line)
		}
	}
}
