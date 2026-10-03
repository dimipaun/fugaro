package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
)

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

func TestEnsureCreatesOnceAfterConfirmation(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	// The installation and Firebase applies are confirmed; the database step is not.
	out, _, err := executeStdin(t, names(2), "init", "--firebase", fpID)
	if err == nil {
		t.Fatalf("not confirmed, yet init succeeded:\n%s", out)
	}
	if r.fsWrites() != 0 {
		t.Errorf("Firestore was written before the confirmation (creates %d, rules %d)", r.dbCreates(), r.rules.Writes())
	}
	for _, want := range []string{"Cloud Firestore database: CREATE", "us-east5", "PERMANENT", "deny-all rules", "Firestore mark: write meta/installation", "permanent and can never be changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation lacks %q:\n%s", want, out)
		}
	}
	// Confirmed.
	r2 := newFBRig(t)
	if out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := r2.dbCreates(); n != 1 {
		t.Errorf("database creates = %d, want 1", n)
	}
	loc, prot := r2.fs.Database()
	if loc != "us-east5" || prot != "DELETE_PROTECTION_ENABLED" {
		t.Errorf("database = %s %s", loc, prot)
	}
	if src, ok := r2.rules.Source(); !ok || src != infra.FirestoreRules {
		t.Errorf("rules = %q %v", src, ok)
	}
	m, ok := r2.fs.Value("meta", "installation")
	if !ok || m["project"] != initProjectName || m["gcp_project"] != initProject || m["firebase_project"] != fpID || m["managed_by"] != "fugaro" {
		t.Errorf("mark = %v", m)
	}
	r2.noDeletes(t)
}

func TestEnsureAdoptsExisting(t *testing.T) {
	r := newFBRig(t)
	r.fs.SetDatabase("us-east5")
	if out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
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

func TestEnsureRefusesOtherLocation(t *testing.T) {
	r := newFBRig(t)
	r.fs.SetDatabase("europe-west1")
	out, stderr, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error()+stderr, "europe-west1") {
		t.Fatalf("err = %v, stderr = %s\n%s", err, stderr, out)
	}
	if r.fsWrites() != 0 || r.db.Value("fugaro/mark") != nil {
		t.Error("a refused database was followed by writes")
	}
	if len(r.applies(t)) != 0 {
		t.Errorf("refused only after applying %v", r.applies(t))
	}
	if loc, _ := r.fs.Database(); loc != "europe-west1" {
		t.Errorf("the database was changed: %s", loc)
	}
	r.noDeletes(t)
}

func TestEnsureNeedsConfirm(t *testing.T) {
	r := newFBRig(t)
	fakeTerminal(t)
	if _, _, err := executeStdin(t, names(2)+"nope\n", "init", "--firebase", fpID); err == nil {
		t.Fatal("a wrong confirmation was accepted")
	}
	if r.fsWrites() != 0 {
		t.Errorf("Firestore was written without a confirmation")
	}
}

func TestPlanOnlyShowsEnsure(t *testing.T) {
	r := newFBRig(t)
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--plan-only")
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
	if out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	src, ok := r.rules.Source()
	if !ok || !strings.Contains(src, "allow read, write: if false;") || strings.Contains(src, "if true") || strings.Count(src, "allow") != 1 {
		t.Errorf("rules are not deny-all:\n%s", src)
	}
}

func TestRulesSameApartFromFormattingAdopted(t *testing.T) {
	r := newFBRig(t)
	r.rules.SeedRelease(denyAllReformatted)
	if out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
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
	out, stderr, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	r.rules.FlakeAfterWrite(2) // eventually consistent: two reads miss it
	if out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
		t.Fatalf("a slow read-back failed the step: %v\n%s", err, out)
	}
	if _, ok := r.fs.Value("meta", "installation"); !ok {
		t.Error("no mark")
	}
	// Never read back: the step fails and the mark is not written.
	r2 := newFBRig(t)
	r2.rules.FlakeAfterWrite(1000)
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if err == nil || !strings.Contains(err.Error(), "did not read back") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if _, ok := r2.fs.Value("meta", "installation"); ok {
		t.Error("the mark was written after a failed rules read-back")
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
			out, stderr, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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

func TestUnmarkedDatabaseWithHistoryRefused(t *testing.T) {
	r := newFBRig(t)
	r.fs.SetDatabase("us-east5")
	r.fs.Set("spendDaily", "2026-10-01_a", map[string]any{"date": "2026-10-01"})
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "no Fugaro mark") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if r.fsWrites() != 0 {
		t.Error("writes into a foreign database")
	}
}

func TestRerunIsNoOp(t *testing.T) {
	r := newFBRig(t)
	if out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	writes, rs := r.fsWrites(), r.rules.Rulesets()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	fakeTerminal(t) // a confirmation would fail
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID)
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
			out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "no_auth is set but the firestore and firebase_rules endpoints are not") {
				t.Fatalf("err = %v\n%s", err, out)
			}
			if len(r.applies(t)) != 0 {
				t.Error("applied before refusing")
			}
		})
	}
}
