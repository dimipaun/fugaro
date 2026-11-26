package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/place"
)

// locations_end_to_end_test.go is LP14 of docs/location-preference/, the task
// the whole plan leads to: `fugaro setup` and `fugaro update` write the
// locations record — where the installation's located things really are, read
// from the cloud — into this machine's own project config, and the record
// changes nothing (docs/location-preference/record.md, D3087, D3089).
//
// The three tests here are its three claims. The record is written, by the one
// command an installation at the previous version runs
// (TestAnInstallationFromBeforeTheRecordConverges). It is never read back as an
// input, so an installation that gains one plans no destroy and no replace
// (TestTheRecordIsNeverAnInput, R5). And a new installation in europe-west1
// plans every located resource in the EU
// (TestANewEUInstallationPlansEverythingInTheEU) — which is what makes a record
// answerable at all: what a record may say is what the cloud holds.
//
// The fourth claim, that a converge leaves a run in flight running, is
// update_in_flight_test.go's TestUpdateLeavesARunInFlightRunning: it is the
// same `fugaro update` as the first test here, over a fixture with a run
// going, and this task may not lose it.

// TestAnInstallationFromBeforeTheRecordConverges: LP14's red step. An
// installation made before this work — its Firestore database in us-east5, the
// Realtime Database Google made for it in us-central1, its Fugaro containers on
// Google's own automatic replication, its run log bucket in global — is updated
// with `fugaro update` alone, and one command later its own config carries the
// record of where all four really are, read from the cloud and not from
// anything a person typed.
//
// doctor is why the record is written at all, and it is its only reader: before
// the run the row says this machine has no record, after it the row prints the
// four facts. Why nothing may plan from it — the destroy or the replace this
// installation would plan if the record were an input, since its data is in
// `global` and us-central1 while the mapping answers `us` and us-central1 for
// its region — is TestTheRecordIsNeverAnInput, which holds the two documents
// byte for byte.
func TestAnInstallationFromBeforeTheRecordConverges(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	previousVersionInstallation(t, r)
	// doctor's update check, before the run's own: it is the release this
	// binary is and not a network failure, and doctor is asked twice here.
	currentRelease(t)

	if c, ok := doctorCheckByID(doctorChecks(t, r.root).Checks, doctorLocationsCheckID); !ok {
		t.Fatalf("doctor has no %s row at all, and it is the record's one reader", doctorLocationsCheckID)
	} else if !strings.Contains(c.Problem, "no locations record yet") {
		t.Fatalf("the fixture already carries a record, so this is not an installation from before it: %s", c.Problem)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	// The record, read back the way the door reads the file (the strict
	// loader), so a block this release would refuse cannot pass for a record.
	l := updatedConfig(t).Locations
	if l == nil {
		t.Fatalf("after one fugaro update this machine's config carries no locations record, so the installation needs a second command to get one:\n%s", readFile(t, installationConfigPath()))
	}
	for _, m := range []struct{ name, got, want string }{
		{"firestore", l.Firestore, "us-east5"},
		{"rtdb", l.RTDB, "us-central1"},
		{"secrets", l.Secrets.String(), localcfg.ReplicationAutomatic},
		{"log_bucket", l.LogBucket, "global"},
	} {
		if m.got != m.want {
			t.Errorf("after one fugaro update, the record's %s reads %q, want %q: the record is what the cloud holds, not what the mapping says where a new installation's data would go", m.name, m.got, m.want)
		}
	}

	// The run says what it recorded: a person whose config changed under them at
	// the end of an update is owed the sentence that says why.
	const line = "recorded where the installation's data is: firestore us-east5, rtdb us-central1, secrets automatic, log bucket global"
	if !strings.Contains(out, line) {
		t.Errorf("the run never says what it recorded, want %q in:\n%s", line, out)
	}
	// And nothing is asked (docs/no-prompt-policy/): this step runs after every
	// confirmation the run was going to make, at the moment nobody is looking.
	// The file's own diff does name the block — that is the run's account of
	// what changed — and a prompt beside it would be the question this step may
	// not ask.
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "locations") && strings.Contains(l, "[y/N]") {
			t.Errorf("the record's write asked a question: %q", l)
		}
	}

	// doctor's row, the record's one reader, now prints all four.
	c, ok := doctorCheckByID(doctorChecks(t, r.root).Checks, doctorLocationsCheckID)
	if !ok {
		t.Fatalf("after one fugaro update, doctor has no %s row", doctorLocationsCheckID)
	}
	for _, want := range []string{
		"its Firestore database is in us-east5",
		"its Realtime Database is in us-central1",
		"its secret containers are replicated with Google's automatic replication",
		"its run log bucket is in global",
	} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("after one fugaro update, doctor's %s row does not say %q: %s", doctorLocationsCheckID, want, c.Problem)
		}
	}
}

// TestTheRecordsWriteKeepsWhatTheRepositoryStageWrote: the record is one member
// of a file more than one engine of a run writes, and the write may not be the
// copy the converge started with. The repository stage resolves this checkout's
// entry out of the checkout and writes it from the config IT read (init.go's
// repoEngine, loadRepoConfig and writeRepoConfig): the base branch the checkout
// is on, its workflows in the order the stage sorts them, the flags an earlier
// run of the entry's own — and none of that reaches the installation engine's
// own copy of the config, which was loaded before any of it. A record written
// from that copy puts the entry back to what the run found, which is the whole
// of the repository stage's work undone by a step whose business is one member.
//
// The fixture's entry is that earlier copy: it names the two workflows in the
// order a person wrote them and carries no base branch at all, so the stage's
// write and a rollback of it differ in both, and this test reads the file after
// one `fugaro update` that wrote a record in the same run.
func TestTheRecordsWriteKeepsWhatTheRepositoryStageWrote(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	previousVersionInstallation(t, r)

	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	lc := updatedConfig(t)
	if lc.Locations == nil {
		t.Fatalf("this update wrote no locations record, so the write this test is about did not happen:\n%s", readFile(t, installationConfigPath()))
	}
	entry, ok := lc.Repos["acme/app"]
	if !ok {
		t.Fatalf("repos.acme/app is gone from the config the record's write produced:\n%s", readFile(t, installationConfigPath()))
	}
	// Both of these are the repository stage's answer and neither is in the
	// fixture, so a value here is the stage's write surviving the record's.
	if entry.BaseBranch != "main" {
		t.Errorf("repos.acme/app.base_branch reads %q after the record's write, want main, the branch of the checkout the run read: the record's write put back the copy the run started with, so the repository stage's entry is what it rolled back:\n%s",
			entry.BaseBranch, readFile(t, installationConfigPath()))
	}
	if want := []string{"api", "app"}; !slices.Equal(entry.Workflows, want) {
		t.Errorf("repos.acme/app.workflows reads %v after the record's write, want %v, the checkout's own workflows as the repository stage sorts them: the record's write put back the copy the run started with:\n%s",
			entry.Workflows, want, readFile(t, installationConfigPath()))
	}
}

// previousVersionInstallation seeds the cloud as the release behind leaves it,
// which is the state every installation made before this work is in: Fugaro's
// own default region for the Firestore database setup created then, the
// Realtime Database Google makes for a Firebase project (us-central1, the one
// location the budget's backend was made in before this work), a
// Fugaro-labelled secret container on Google's own automatic replication, and
// the run log bucket in `global` — the region-less bucket every such
// installation has, because nothing chose a location until the mapping did.
// The last is the one that tells an input from a record: the mapping answers
// `us` for this region, and R5 gives a bucket that exists what it has.
func previousVersionInstallation(t *testing.T, r *convergeRig) {
	t.Helper()
	r.fs.SetDatabase("us-east5")
	projectLabelsSeed(t, r.sm, "anthropic-api-key", []byte("stored-before-this-run"))
	r.logs.AddBucket(initProject, "global", infra.LogBucket, "ACTIVE", infra.LogBucketDescription)
}

// TestTheRecordIsNeverAnInput: R5, and the reason an installation that gains a
// record plans no destroy and no replace (D3089). Every plan asks the cloud for
// the thing it is about, so a record — wrong, stale, or hand-edited — decides
// nothing: the same project config with a record that says asia-southeast1 for
// all four of its things composes the very documents the config with no record
// composes.
//
// The documents are the whole of it. A root's plan is a function of the
// variables it is planned with and of the cloud, so two runs whose variables
// are byte for byte the same plan the same thing; what a record would have to
// reach to make this installation plan a destroy or a replace is one of these
// two, and it reaches neither. That the record is also never published, so a
// teammate's older Fugaro cannot read it at all, is localcfg's own
// TestLocationsAreNotPublished.
func TestTheRecordIsNeverAnInput(t *testing.T) {
	// One value in all four members, on purpose: what must be nowhere in the
	// composed documents is then one string to look for, and a member the
	// record leaked into is a member that carries it.
	const record = "locations:\n" +
		"  firestore: asia-southeast1\n" +
		"  rtdb: asia-southeast1\n" +
		"  secrets: [asia-southeast1]\n" +
		"  log_bucket: asia-southeast1\n"
	plain := rootVarsFor(t, parseLocalConfig(t, initConfig))
	recorded := rootVarsFor(t, parseLocalConfig(t, initConfig+record))

	// The comparison is about a document that exists: the config composes this
	// installation's region and its registry, so an empty or truncated pair of
	// documents cannot pass for agreement.
	for _, want := range []string{"us-east5", "fugaro-runs-proj-1234"} {
		if !bytes.Contains(plain.installation, []byte(want)) {
			t.Fatalf("the installation root's variables do not name %q, so this fixture composes nothing to compare:\n%s", want, plain.installation)
		}
	}
	if !bytes.Equal(plain.installation, recorded.installation) {
		t.Errorf("a record in this machine's config changed the installation root's variables, so the record is an input (R5):\n%s", lineDiff(string(plain.installation), string(recorded.installation)))
	}
	if !bytes.Equal(plain.firebase, recorded.firebase) {
		t.Errorf("a record in this machine's config changed the firebase root's variables, so the record is an input (R5):\n%s", lineDiff(string(plain.firebase), string(recorded.firebase)))
	}
	for _, doc := range []struct {
		root string
		vars []byte
	}{{"installation", recorded.installation}, {"firebase", recorded.firebase}} {
		if bytes.Contains(doc.vars, []byte("asia-southeast1")) {
			t.Errorf("the record reached the %s root's variables:\n%s", doc.root, doc.vars)
		}
	}
}

// rootVars is the variables of the two roots a record could reach if anything
// read it back: the installation root's own, which carries the region, the run
// log bucket's location, the secret containers' replication and every job's
// region, and the firebase root's, which carries the Realtime Database's
// location and the Firestore database's.
type rootVars struct{ installation, firebase []byte }

// rootVarsFor composes both roots from one local config, offline and with no
// cloud: this is the document `fugaro update` hands Terraform, and the one the
// record would have to appear in to decide anything.
func rootVarsFor(t *testing.T, lc *localcfg.Config) rootVars {
	t.Helper()
	spec, err := installOptions(&initOptions{}, lc)
	if err != nil {
		t.Fatalf("composing the installation's spec: %v", err)
	}
	vars, err := infra.InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := infra.Firebase(spec, infra.FirebaseInputs{FP: initProject,
		Admins:       []string{"user:owner@example.com"},
		BudgetAdmins: []string{"user:owner@example.com"}, HistoryAccount: historyAccount})
	if err != nil {
		t.Fatal(err)
	}
	fbv, err := infra.FirebaseVars(fb)
	if err != nil {
		t.Fatal(err)
	}
	return rootVars{installation: vars, firebase: fbv}
}

// TestANewEUInstallationPlansEverythingInTheEU: a new installation in
// europe-west1 plans every located resource in the EU, read off the two roots'
// real plans — the goldens scripts/gen-golden-plans.sh generates from the EU
// fixtures, which are the same documents the US installation is planned from
// with the region moved. An installation is one project, so this is also the
// claim the record above is a record of: the mapping place.For answers with and
// the plan the installation gets are made from the same table.
//
// The scan at the end is the half no list of resources can go stale behind: it
// reads every region-shaped name in both plans, whatever a later provider adds
// and however it spells it — in a `location`, in a registry host, in a bucket's
// upper-case location — and holds every one of them to the fixture's region. A
// resource nobody thought to list moves it.
func TestANewEUInstallationPlansEverythingInTheEU(t *testing.T) {
	region := place.DefaultRegion(place.EU)
	if region == "" {
		t.Fatal("the EU has no default region, so this fixture has no region to be planned in")
	}
	p := place.For(region)
	scheduler, err := gcp.SchedulerRegion(region)
	if err != nil {
		t.Fatalf("place.For's own EU region has no Cloud Scheduler location, so this fixture cannot be planned: %v", err)
	}
	seen := map[string]int{}
	for _, name := range []string{"installation-eu", "firebase-eu"} {
		plan := euGoldenPlan(t, name)
		for _, c := range plan.ResourceChanges {
			seen[c.Type]++
			var got, want any
			switch c.Type {
			case "google_artifact_registry_repository":
				got, want = at(c.Change.After, "location"), region
			case "google_storage_bucket":
				// GCS spells a bucket's location upper case, which is how the
				// runs bucket is created and how its plan reports it.
				got, want = at(c.Change.After, "location"), strings.ToUpper(region)
			case "google_logging_project_bucket_config":
				got, want = at(c.Change.After, "location"), p.LogBucket.Where
			case "google_secret_manager_secret":
				got, want = at(c.Change.After, "replication", 0, "user_managed", 0, "replicas", 0, "location"), p.Secrets.Where
			case "google_cloud_run_v2_job":
				got, want = at(c.Change.After, "location"), region
				if env := containerEnv(c.Change.After, "FUGARO_REGION"); env != region {
					t.Errorf("%s (%s) runs with FUGARO_REGION=%q, want %q", c.Address, name, env, region)
				}
			case "google_cloud_scheduler_job":
				got, want = at(c.Change.After, "region"), scheduler
			case "google_firebase_database_instance":
				// The provider spells the Realtime Database instance's own
				// location `region`, which is also what place.For's
				// RealtimeDatabase row is (a database lives in one of the
				// three regions Google offers it in).
				got, want = at(c.Change.After, "region"), p.RealtimeDatabase.Where
			default:
				continue
			}
			if got != want {
				t.Errorf("%s (%s) is planned in %v, want %v", c.Address, name, got, want)
			}
		}
		euOnly(t, plan, name, region)
	}
	// Every located resource type the two plans are about was read above: a
	// renaming that made one of the arms above dead would otherwise pass this
	// test by finding nothing to check.
	for _, typ := range []string{
		"google_artifact_registry_repository", "google_storage_bucket", "google_logging_project_bucket_config",
		"google_secret_manager_secret", "google_cloud_run_v2_job", "google_cloud_scheduler_job",
		"google_firebase_database_instance",
	} {
		if seen[typ] == 0 {
			t.Errorf("neither EU plan carries a %s, so this test checks nothing about it", typ)
		}
	}
}

// euOnly holds every region-shaped name in a plan to the EU. A name that is a
// region and is not in the EU is a located thing this installation would create
// outside its place, wherever in the plan it hides; whether a match is a region
// at all is place.Of's answer and not the shape's, so a slug, a job name or a
// project id that happens to look like one is not a region.
func euOnly(t *testing.T, plan *tf.Plan, name, region string) {
	t.Helper()
	for _, c := range plan.ResourceChanges {
		for _, s := range stringsOf(c.Change.After) {
			for _, tok := range regionNameRE.FindAllString(s, -1) {
				low := strings.ToLower(tok)
				if place.Of(low) == place.EU {
					continue
				}
				if place.Of(low) == place.US || slices.Contains(place.Regions, low) {
					t.Errorf("%s (%s) plans %q, which is not in the EU", c.Address, name, tok)
				}
			}
		}
	}
}

// regionNameRE is a region as it is spelled anywhere: in a `location`, in a
// registry host (europe-west1-docker.pkg.dev), in a bucket's location
// (EUROPE-WEST1, which GCS spells upper case). It is Google's own shape — one
// or more words, then a direction, then a number — and it is deliberately loose
// on which words: what a plan may hold is place.Regions' business, not a
// reader's.
var regionNameRE = regexp.MustCompile(`(?i)(?:[A-Za-z]+-)+[A-Za-z]+\d+`)

// stringsOf is every string in a decoded plan value, nested maps and lists
// included. A value that is only known after apply (change.after_unknown) holds
// no location: a generated name is not one, and the two fields a location could
// be in are `location` and `region`, which a plan states before it applies.
func stringsOf(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = append(out, x)
	case map[string]any:
		for _, e := range x {
			out = append(out, stringsOf(e)...)
		}
	case []any:
		for _, e := range x {
			out = append(out, stringsOf(e)...)
		}
	}
	return out
}

// at walks a decoded plan value by key names and list indexes — the way the
// provider's own blocks nest — and answers nil where the path is not there, so
// a resource whose shape changed reads as the empty value and is reported by
// the caller's comparison rather than by a panic.
func at(v any, path ...any) any {
	for _, step := range path {
		switch k := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			l, ok := v.([]any)
			if !ok || k >= len(l) {
				return nil
			}
			v = l[k]
		}
	}
	return v
}

// containerEnv is the value one environment entry of a Cloud Run job's
// container carries, and "" when the container has no such entry.
func containerEnv(after map[string]any, key string) string {
	list, _ := at(after, "template", 0, "template", 0, "containers", 0, "env").([]any)
	for _, e := range list {
		if m, ok := e.(map[string]any); ok && m["name"] == key {
			s, _ := m["value"].(string)
			return s
		}
	}
	return ""
}

// euGoldenPlan is one of the two EU golden plans, read the way the package that
// generates and covers them reads them (internal/infra/tf, and
// scripts/gen-golden-plans.sh for the documents they are planned from). It is
// read from here on purpose: this test is the end-to-end claim about what a new
// EU installation gets, and a fixture of this package's own would be another
// answer rather than the plan.
func euGoldenPlan(t *testing.T, name string) *tf.Plan {
	t.Helper()
	path := filepath.Join("..", "infra", "tf", "testdata", "golden", name+".plan.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p tf.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.ResourceChanges) < 10 {
		t.Fatalf("%s carries %d resource changes, so it is not the plan scripts/gen-golden-plans.sh generates", path, len(p.ResourceChanges))
	}
	return &p
}
