package infra

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// TestLicenceObjectsAreTheWritersNames pins the three object names the renewal
// job's read condition is built from (plan K4.2) to the packages that already
// own them.
//
// The core may not import ee/ (D524, .claude/rules/ee.md), so internal/backend/gcp
// spells the licence object as a literal and ee/register's own pin holds it to
// the store's. The other two have no such reason to be literals — they are
// core names, and the job reads them for the same reason every unconfigured
// machine does (internal/cli's selectWith) — so they are compared here, where a
// rename in internal/infra that left the condition behind fails a test rather
// than a scheduled job that cannot find its installation.
func TestLicenceObjectsAreTheWritersNames(t *testing.T) {
	for _, c := range []struct {
		what, got, want string
	}{
		{"the project marker", gcp.LicenceMarkerObject, ProjectMarkerObject},
		{"the published config", gcp.LicenceConfigObject, SharedConfigObject},
	} {
		if c.got != c.want {
			t.Errorf("%s: the licence read condition names %q and the writer uses %q: the job has no checkout and no config file, so those two objects are the whole of how it finds the installation it renews for", c.what, c.got, c.want)
		}
	}
	if gcp.LicenceKeyObject == "" {
		t.Error("the licence read and write conditions name no licence object")
	}
}

// TestLicenceReadConditionNamesThreeObjectsAndNoListing is the condition's own
// shape, read off the builder rather than off a plan: three object clauses
// joined by " || " and nothing else.
//
// The count is the point. One clause would be the job that fails before it
// reaches the object it exists for (it could not find its installation), and a
// listing clause would be the account reading every run record, prompt and
// transcript the bucket holds — a List request names no object, so a condition
// of object names does not grant one, and a condition that named no object
// would. What this pins is that neither of the two mistakes is in the bytes.
func TestLicenceReadConditionNamesThreeObjectsAndNoListing(t *testing.T) {
	const bucket = "fugaro-runs-proj-1234"
	read := gcp.LicenceReadCondition(bucket)
	for _, want := range []string{gcp.LicenceMarkerObject, gcp.LicenceConfigObject, gcp.LicenceKeyObject} {
		if !strings.Contains(read, bucket+"/objects/"+want) {
			t.Errorf("the licence read condition does not name %s of %s:\n%s", want, bucket, read)
		}
	}
	if n := strings.Count(read, " || "); n != 2 {
		t.Errorf("the licence read condition joins %d clauses, want 2 (three objects):\n%s", n+1, read)
	}
	if strings.Contains(read, "buckets/"+bucket+"\"") || strings.Contains(read, "prefix") || strings.Contains(read, "startsWith") {
		t.Errorf("the licence read condition reaches beyond the three objects it names:\n%s", read)
	}

	write := gcp.LicenceWriteCondition(bucket)
	if n := strings.Count(write, " || "); n != 0 {
		t.Errorf("the licence write condition is %d clauses, want one:\n%s", n+1, write)
	}
	if !strings.Contains(write, bucket+"/objects/"+gcp.LicenceKeyObject) {
		t.Errorf("the licence write condition does not name the licence object:\n%s", write)
	}
}

// TestLicenceSpecIsTheJobsNames: the spec the plan carries has the names the
// rest of the system agrees on — the account, the job, the scheduler job and
// the container — and it carries no credential of any kind.
//
// The last claim is D524's: what reaches the tfvars is a container's ID, an
// account's ID and conditions built from a bucket name. An installation that
// bought no licence holds a job that no-ops, and nothing in the document is a
// value a key could be read out of.
func TestLicenceSpecIsTheJobsNames(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	l, err := Licence(lc, "fugaro-runs-proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	if l.AccountID != LicenceAccountID || l.Job != LicenceJob || l.SchedulerJob != LicenceSchedulerJob {
		t.Errorf("the spec is account %q job %q scheduler %q, want %q %q %q", l.AccountID, l.Job, l.SchedulerJob, LicenceAccountID, LicenceJob, LicenceSchedulerJob)
	}
	if strings.HasPrefix(l.Job, "fugaro-") {
		t.Errorf("the job is named %q: fugaro runs ls and max_parallel count the fugaro- jobs as workflow jobs", l.Job)
	}
	if l.RenewalSecret != LicenceRenewalSecretID(lc.Name) || l.RenewalSecret == "" {
		t.Errorf("the spec's renewal container is %q, want the derived ID of %s", l.RenewalSecret, lc.Name)
	}
	if l.Image == "" || l.SchedulerRegion == "" {
		t.Errorf("the spec is image %q in scheduler region %q: both are the job's own and neither may be empty", l.Image, l.SchedulerRegion)
	}
	for _, key := range []string{"FUGARO_PROJECT", "FUGARO_GCP_PROJECT", "FUGARO_BUCKET"} {
		if l.Env[key] == "" {
			t.Errorf("the spec's environment carries no %s: a container has no checkout and no config file, so those names are how it finds the installation whose licence it renews", key)
		}
	}
	if l.ReadCondition.Title == "" || l.WriteCondition.Title == "" || l.ReadCondition.Title == l.WriteCondition.Title {
		t.Errorf("the conditions are titled %q and %q: the cover reads a grant's title to say WHICH grant it is, so the two have to be different and neither empty", l.ReadCondition.Title, l.WriteCondition.Title)
	}
}

// TestLicenceRefusesAnUnbuildableBucket is the one refusal: the conditions are
// CEL strings built from the bucket name and compared byte for byte by the plan
// review, so a name the builders would decline has to stop here rather than
// become a grant nothing matches.
func TestLicenceRefusesAnUnbuildableBucket(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions)
	for _, bucket := range []string{"", "Fugaro-Runs", "fugaro runs", "fugaro-runs-$"} {
		if _, err := Licence(lc, bucket); err == nil {
			t.Errorf("Licence(%q) was accepted: a bucket outside a-z, 0-9, '.', '_' and '-' cannot be written into a condition", bucket)
		}
	}
}
