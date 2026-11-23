package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/place"
)

// fsLocation is where the Firestore database of an installation whose region
// is region is created: the mapping's answer for it (D3085), which is what
// firestoreStep hands the step and what the line says. The rigs' own region is
// initConfig's (us-east5).
func fsLocation(region string) string { return place.For(region).Firestore.Where }

// TestTheFirestoreStepHoldsTheMappingsLocation is LP3 at the CLI's own seam:
// what firestoreStep gives the step is place.For's answer for the
// installation's region (D3085) and the place that region lies in — never the
// region itself, and never a constant of the CLI's own.
func TestTheFirestoreStepHoldsTheMappingsLocation(t *testing.T) {
	for _, tc := range []struct {
		region, loc string
		place       place.Place
	}{
		{"us-east5", "us-east5", place.US},
		{"europe-west4", "europe-west4", place.EU},
		{"us-west8", "us-east5", place.US},
		{"europe-west2", "europe-west2", place.None},
	} {
		t.Run(tc.region, func(t *testing.T) {
			lc, err := localcfg.Parse([]byte("version: 1\nname: aurora\ngcp_project: proj-1234\nregion: " + tc.region +
				"\nruns_bucket: fugaro-runs-proj-1234\nendpoints: { firestore: http://127.0.0.1:1, firebase_rules: http://127.0.0.1:1, no_auth: true }\n"))
			if err != nil {
				t.Fatal(err)
			}
			fs, err := (&initRun{}).firestoreStep(context.Background(), lc, fpID)
			if err != nil {
				t.Fatal(err)
			}
			if fs.Location != tc.loc || fs.Place != tc.place {
				t.Errorf("the step holds location %q, place %q; want %q, %q", fs.Location, fs.Place, tc.loc, tc.place)
			}
		})
	}
}

func (r *fbRig) dbCreates() int {
	n := 0
	for _, q := range r.fs.Requests() {
		if q.Method == "POST" && strings.HasSuffix(q.Path, "/databases") {
			n++
		}
	}
	return n
}

func (r *fbRig) fsWrites() int {
	n := r.dbCreates() + r.rules.Writes()
	for _, q := range r.fs.Requests() {
		if q.Method == "PATCH" {
			n++
		}
	}
	return n
}

func (r *fbRig) noDeletes(t *testing.T) {
	t.Helper()
	for _, q := range r.fs.Requests() {
		if q.Method == "DELETE" {
			t.Errorf("init sent DELETE %s to Firestore", q.Path)
		}
	}
}

func ownMark() map[string]any {
	return map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "firebase_project": fpID, "version": int64(1), "fugaro_version": "dev"}
}

const denyAllReformatted = "// locked\nrules_version = '2';\nservice cloud.firestore {\n match /databases/{database}/documents {\n  match /{document=**} {\n   allow read, write: if false; /* nobody */\n  }\n }\n}"

func TestEnsureCreatesOnce(t *testing.T) {
	// The database is created once, from the plan the run listed, with nothing
	// typed and no step confirming the location (LP10, D3090).
	r := newFBRig(t)
	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "setup", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"Cloud Firestore database: CREATE", fsLocation("us-east5"), "PERMANENT", "deny-all rules", "Firestore mark: write meta/installation", "its location is permanent"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run lacks %q:\n%s", want, out)
		}
	}
	if n := r.dbCreates(); n != 1 {
		t.Errorf("database creates = %d, want 1", n)
	}
	loc, prot := r.fs.Database()
	if loc != fsLocation("us-east5") || prot != "DELETE_PROTECTION_ENABLED" {
		t.Errorf("database = %s %s", loc, prot)
	}
	if src, ok := r.rules.Source(); !ok || src != infra.FirestoreRules {
		t.Errorf("rules = %q %v", src, ok)
	}
	m, ok := r.fs.Value("meta", "installation")
	if !ok || m["project"] != initProjectName || m["gcp_project"] != initProject || m["firebase_project"] != fpID || m["managed_by"] != "fugaro" {
		t.Errorf("mark = %v", m)
	}
	r.noDeletes(t)
}

func TestEnsureAdoptsExisting(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.fs.SetDatabase("us-east5")
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := r.dbCreates(); n != 0 {
		t.Errorf("database creates = %d on an existing database", n)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("the mark was not written into the adopted database")
	}
	r.noDeletes(t)
}

// TestEnsureAdoptsDatabaseElsewhere is LP3, R3 and D3091 at the CLI: a native
// database that exists is adopted wherever it is, and the plan's line says
// where it is when that is outside the installation's place. Nothing is
// created, moved or deleted.
func TestEnsureAdoptsDatabaseElsewhere(t *testing.T) {
	r := newFBRig(t)
	r.fs.SetDatabase("europe-west1")
	fakeTerminal(t)
	out, stderr, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("a database elsewhere was not adopted: %v\n%s%s", err, out, stderr)
	}
	if !strings.Contains(out, "exists in europe-west1 (outside the US), adopted as it is") {
		t.Errorf("the plan does not say where the adopted database is:\n%s", out)
	}
	if n := r.dbCreates(); n != 0 {
		t.Errorf("database creates = %d on an existing database", n)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("the mark was not written into the adopted database")
	}
	if loc, _ := r.fs.Database(); loc != "europe-west1" {
		t.Errorf("the database was changed: %s", loc)
	}
	r.noDeletes(t)
}

// The database's step is applied only where it is confirmed, and a person at
// the terminal is the confirmation now (T7): a run nobody can confirm writes
// nothing to Firestore. What is confirmed is the stage's own step — the
// database write — and not its location, which no step confirms at all any
// more (LP10, D3090): a run that cannot confirm this one never reaches the
// location's line, so the two are not the same fence.
func TestEnsureNeedsConfirm(t *testing.T) {
	r := newFBRig(t)
	noTerminal(t)
	out, _, err := executeStdin(t, "", "setup", "--firebase-project", fpID)
	if err == nil {
		t.Fatalf("a run nobody can confirm wrote the database:\n%s", out)
	}
	if r.fsWrites() != 0 {
		t.Errorf("Firestore was written without a confirmation")
	}
	if strings.Contains(out, "the Firestore database is created in") {
		t.Errorf("a run that never reached the write printed the location's line:\n%s", out)
	}
}

func TestPlanOnlyShowsEnsure(t *testing.T) {
	r := newFBRig(t)
	out, _, err := executeStdin(t, "", "setup", "--firebase-project", fpID, "--plan-only")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Cloud Firestore database: CREATE") || !strings.Contains(out, "PERMANENT") {
		t.Errorf("--plan-only does not show the step:\n%s", out)
	}
	if r.fsWrites() != 0 {
		t.Error("--plan-only wrote to Firestore")
	}
}

func TestRulesDenyAll(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	src, ok := r.rules.Source()
	if !ok || !strings.Contains(src, "allow read, write: if false;") || strings.Contains(src, "if true") || strings.Count(src, "allow") != 1 {
		t.Errorf("rules are not deny-all:\n%s", src)
	}
}

func TestRulesSameApartFromFormattingAdopted(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.rules.SeedRelease(denyAllReformatted)
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.rules.Writes() != 0 {
		t.Error("an equivalent deny-all ruleset was replaced")
	}
}

func TestRulesMismatchRefused(t *testing.T) {
	r := newFBRig(t)
	loose := "rules_version = '2';\nservice cloud.firestore { match /databases/{d}/documents { match /{x=**} { allow read: if true; } } }"
	r.rules.SeedRelease(loose)
	out, stderr, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error()+stderr, "not deny-all") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if src, _ := r.rules.Source(); src != loose || r.rules.Writes() != 0 {
		t.Error("an existing ruleset was changed")
	}
	if r.fsWrites() != 0 || len(r.applies(t)) != 0 {
		t.Error("a refused ruleset was followed by writes")
	}
}

func TestRulesReadBackFlaky(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.rules.FlakeAfterWrite(2) // eventually consistent: two reads miss it
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("a slow read-back failed the step: %v\n%s", err, out)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("no mark")
	}
	// Never read back: the step fails; the mark was written first, so a rerun finds it.
	r2 := newFBRig(t)
	r2.rules.FlakeAfterWrite(1000)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if err == nil || !strings.Contains(err.Error(), "did not read back") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if _, ok := r2.fs.Value("meta", "installation"); !ok {
		t.Error("the mark (written first) is missing after a failed rules read-back")
	}
}

func TestMarkOfOtherProjectRefused(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"project":  func(m map[string]any) { m["project"] = "other" },
		"gcp":      func(m map[string]any) { m["gcp_project"] = "other-gcp-1" },
		"firebase": func(m map[string]any) { m["firebase_project"] = "other-fp" },
		"managed":  func(m map[string]any) { m["managed_by"] = "someone" },
	} {
		t.Run(name, func(t *testing.T) {
			r := newFBRig(t)
			r.fs.SetDatabase("us-east5")
			m := ownMark()
			edit(m)
			r.fs.Set("meta", "installation", m)
			out, stderr, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error()+stderr, "mark that is not ours") {
				t.Fatalf("err = %v\n%s", err, out)
			}
			if r.fsWrites() != 0 || len(r.applies(t)) != 0 {
				t.Error("a foreign mark was followed by writes")
			}
			if got, _ := r.fs.Value("meta", "installation"); got["project"] != m["project"] {
				t.Error("the foreign mark was changed")
			}
		})
	}
}

func TestRerunIsNoOp(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	writes, rs := r.fsWrites(), r.rules.Rulesets()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	fakeTerminal(t) // a confirmation would fail
	out, _, err := executeStdin(t, "", "setup", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.fsWrites() != writes || r.rules.Rulesets() != rs {
		t.Errorf("a rerun wrote: %d -> %d", writes, r.fsWrites())
	}
	if strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("a confirmation on an unchanged installation:\n%s", out)
	}
	r.noDeletes(t)
}

func TestNoAuthNeedsFirestoreEndpoints(t *testing.T) {
	for _, key := range []string{"firestore", "firebase_rules"} {
		t.Run(key, func(t *testing.T) {
			r := newFBRig(t)
			r.dropEndpoint(t, key)
			out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "no_auth is set but the firestore and firebase_rules endpoints are not") {
				t.Fatalf("err = %v\n%s", err, out)
			}
			if len(r.applies(t)) != 0 {
				t.Error("applied before refusing")
			}
		})
	}
}

func TestUnmarkedDatabaseWithOtherCollectionRefused(t *testing.T) {
	r := newFBRig(t)
	r.fs.SetDatabase("us-east5")
	r.fs.Set("app_users", "u1", map[string]any{"name": "x"})
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "app_users") || !strings.Contains(err.Error(), "no Fugaro mark") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if r.fsWrites() != 0 || len(r.applies(t)) != 0 {
		t.Error("writes into a foreign database")
	}
	if _, ok := r.fs.Value("meta", "installation"); ok {
		t.Error("marked a foreign database")
	}
}

func TestUnmarkedEmptyDatabaseAdopted(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.fs.SetDatabase("us-east5")
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.dbCreates() != 0 {
		t.Error("created over an existing database")
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("no mark")
	}
	if _, ok := r.rules.Source(); !ok {
		t.Error("no rules")
	}
}

// The mark is written before the rules: a rules failure leaves a marked
// database, and the rerun completes without a second create.
func TestHalfFailedRerunCompletes(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.rules.FailWrites(1)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if err == nil {
		t.Fatalf("a failed rules write passed:\n%s", out)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("the mark was not written before the rules")
	}
	if _, ok := r.rules.Source(); ok {
		t.Error("rules exist after a failed write")
	}
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("rerun: %v\n%s", err, out)
	}
	if r.dbCreates() != 1 {
		t.Errorf("creates = %d", r.dbCreates())
	}
	if src, ok := r.rules.Source(); !ok || src != infra.FirestoreRules {
		t.Error("the rerun did not deploy the rules")
	}
}

// TestTheLocationLineIsPrintedBeforeTheDatabase is D3090's order: where the
// run creates the Firestore database, the line saying where, and that a
// database's location can never be changed, is printed first and the database
// is created after it. Nothing is asked in between, and the creation's own
// account — the read-back that proves the database is there — comes last.
func TestTheLocationLineIsPrintedBeforeTheDatabase(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "setup", "--firebase-project", fpID)
	whole := out + errOut
	if err != nil {
		t.Fatalf("%v\n%s", err, whole)
	}
	line := strings.Index(whole, "the Firestore database is created in "+fsLocation("us-east5")+", and a database's location can NEVER be changed")
	created := strings.Index(whole, "ensured the Firestore database")
	if line < 0 {
		t.Fatalf("the line saying where the database goes is not in the run:\n%s", whole)
	}
	if created < 0 {
		t.Fatalf("the run does not account for the database it created:\n%s", whole)
	}
	if line > created {
		t.Errorf("the line comes after the database's creation (line %d, created %d):\n%s", line, created, whole)
	}
	if n := r.dbCreates(); n != 1 {
		t.Errorf("database creates = %d, want 1", n)
	}
}

func TestAdoptNeedsNoLocationConfirmation(t *testing.T) {
	r := newFBRig(t)
	r.fs.SetDatabase("us-east5")
	fakeTerminal(t)
	out, _, err := executeStdin(t, names(4), "setup", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "Type us-east5") {
		t.Errorf("asked for the location when adopting:\n%s", out)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("not adopted")
	}
}

// A default release that appears after the plan: refused after the database
// exists (and is marked); the rerun refuses in Plan until deny-all is deployed.
func TestDefaultReleaseAppearsAfterCreation(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	loose := "rules_version = '2';\nservice cloud.firestore { match /databases/{d}/documents { match /{x=**} { allow read, write: if request.time < timestamp.date(2030, 1, 1); } } }"
	r.rules.SeedAfterReads(2, loose)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "not deny-all") || !strings.Contains(err.Error(), "deploy deny-all yourself") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if r.dbCreates() != 1 || r.rules.Writes() != 0 {
		t.Errorf("creates %d, rules writes %d", r.dbCreates(), r.rules.Writes())
	}
	if src, _ := r.rules.Source(); src != loose {
		t.Error("the default release was changed")
	}
	// Rerun: refused in Plan, before anything is applied.
	before := len(r.applies(t))
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("rerun err = %v", err)
	}
	if len(r.applies(t)) != before || r.dbCreates() != 1 {
		t.Error("the rerun wrote")
	}
	// The user deploys deny-all; the rerun completes.
	r.rules.SeedRelease(infra.FirestoreRules)
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("after the fix: %v\n%s", err, out)
	}
}

func TestEnsureRefusesNonNativeDatabase(t *testing.T) {
	r := newFBRig(t)
	r.fs.SetDatabase("us-east5")
	r.fs.SetDatabaseType("DATASTORE_MODE")
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "DATASTORE_MODE") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if r.fsWrites() != 0 || len(r.applies(t)) != 0 {
		t.Error("writes after a refusal")
	}
}

func TestCreateRaceAdoptsOnlyAcceptable(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.fs.RaceCreate("us-east5")
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("the raced database was not marked")
	}
	// And the run says where it is, because it did not create it: the location
	// is permanent and nobody chose it in this run.
	if !strings.Contains(out, "created by someone else after the plan, in us-east5, adopted as it is") {
		t.Errorf("the run does not say where the adopted database is:\n%s", out)
	}
	// A database that appeared elsewhere meanwhile is refused (LP3, D3085):
	// the plan and the confirmation named us-east5, a location is permanent,
	// and nothing agreed to keeping the installation's history in
	// europe-west1. The refusal names both locations and the way out, and
	// nothing is written to it.
	r2 := newFBRig(t)
	fakeTerminal(t)
	r2.fs.RaceCreate("europe-west1")
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if ExitCode(err) != ExitUserError || err == nil ||
		!strings.Contains(err.Error(), "europe-west1") || !strings.Contains(err.Error(), "us-east5") ||
		!strings.Contains(err.Error(), "run it again") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if loc, _ := r2.fs.Database(); loc != "europe-west1" {
		t.Errorf("the refused database was changed: %s", loc)
	}
	if _, ok := r2.fs.Value("meta", "installation"); ok {
		t.Error("a database that is not where the plan said was marked")
	}
	if _, ok := r2.rules.Source(); ok {
		t.Error("rules deployed over a refused database")
	}
	// Ours it is not: a raced database holding data and no mark is refused.
	r3 := newFBRig(t)
	r3.fs.Set("app_users", "u1", map[string]any{"name": "x"})
	r3.fs.RaceCreate("europe-west1")
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "app_users") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if _, ok := r3.fs.Value("meta", "installation"); ok {
		t.Error("marked a database that is not ours")
	}
	if _, ok := r3.rules.Source(); ok {
		t.Error("rules deployed over a refused database")
	}
}

func TestUnreadableBeforeAPIEnabled(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.fs.Refuse(403, "PERMISSION_DENIED", "SERVICE_DISABLED", "Cloud Firestore API has not been used in project")
	out, _, err := executeStdin(t, "", "setup", "--firebase-project", fpID, "--plan-only")
	if err != nil || !strings.Contains(out, "cannot be read yet") {
		t.Fatalf("plan-only: %v\n%s", err, out)
	}
	// A real run tolerates it in discovery, then needs it after the apply.
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if err == nil || !strings.Contains(err.Error(), "cannot read the Firestore state") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if r.dbCreates() != 0 || r.rules.Writes() != 0 {
		t.Error("wrote although Firestore was unreadable")
	}
}

func TestMarkVersionNewerAccepted(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.fs.SetDatabase("us-east5")
	m := ownMark()
	m["version"] = int64(2)
	r.fs.Set("meta", "installation", m)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID)
	if err != nil || !strings.Contains(out, "newer than this fugaro knows") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	m["version"] = int64(0)
	r2 := newFBRig(t)
	r2.fs.SetDatabase("us-east5")
	r2.fs.Set("meta", "installation", m)
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("version 0 accepted: %v", err)
	}
}

// Live Check 27: the read-back right after the database's creation answered
// 403 "API has not been used ... disabled" once (the enablement was still
// propagating) and failed the run. It is retried.
func TestReadBackAfterCreateRetriesAPIPropagation(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	r.fs.RemoveDatabase()
	r.fs.DisabledReadsAfterCreate(3)
	if out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID); err != nil {
		t.Fatalf("a read-back that needed a retry failed the run: %v\n%s", err, out)
	}
	if n := r.dbCreates(); n != 1 {
		t.Errorf("database creates = %d, want 1", n)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("no mark")
	}
}

// TestTheRegionDecidesTheFirestoreLocation: the location is the mapping's
// answer for the installation's region (D3085) and not a constant of its own,
// so a run whose region is elsewhere prints the line about THAT location: the
// region us-west8 has no Firestore of its own, and the line names us-east5.
// Nothing about it is typed and no step confirms it (LP10, D3090,
// docs/location-preference).
func TestTheRegionDecidesTheFirestoreLocation(t *testing.T) {
	const elsewhere = "us-west8" // a region Firestore is not offered in
	loc := fsLocation(elsewhere)
	if loc == elsewhere {
		t.Fatalf("the mapping puts %s in %s: this test needs a region that is not its own location", elsewhere, loc)
	}
	c := condition{name: "a region elsewhere", terminal: true,
		opts: initOptions{door: setupDoor.name, cloud: cloudOptions{gcpProject: initProject, region: elsewhere}}}

	r, spy, out := c.run(t, "")
	r.locationLine(loc)
	if spy.read {
		t.Error("the database's location was read: nothing is typed at it (T7)")
	}
	if r.o.cloud.region != elsewhere {
		t.Errorf("the run's region = %q, want the one it was given (%s)", r.o.cloud.region, elsewhere)
	}
	if got := out.String(); !strings.Contains(got, "the Firestore database is created in "+loc) ||
		!strings.Contains(got, "can NEVER be changed") || strings.Contains(got, elsewhere) {
		t.Errorf("the line does not name the mapping's location (%s) alone:\n%s", loc, got)
	}
}
