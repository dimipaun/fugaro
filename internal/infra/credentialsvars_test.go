package infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The installation root's credentials_service key and the two functions that
// move it (C1.20, and the review that found the carry reading a file the
// workdir had already deleted). Both are read-modify-writes of ONE key of a
// document this program does not own the whole of: the tfvars on disk carry
// what a run's own DISCOVERY knew — the project-wide secret containers it
// found live, the legacy registry's adoption, the run's imports — and a
// document rebuilt from a spec would drop them and plan their destruction.

// credsVarsImage is a digest-pinned reference in the form the stage verifies
// (mirror.RemoteRef); nothing here calls the registry, so its value only has
// to be the shape the module's own validation admits.
func credsVarsImage() string {
	return "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3@sha256:" + strings.Repeat("ab", 32)
}

func credsVarsSpec() CredentialsServiceSpec {
	return CredentialsServiceSpec{
		AccountID:    CredentialsAccountID,
		Service:      CredentialsServiceName,
		Image:        credsVarsImage(),
		BundleSecret: "fugaro-creds-bundle",
	}
}

// credsVarsDiscovered is an installation tfvars as the installation stage
// writes it, discovery included: the module's project-wide containers and
// adopt_legacy_registry are keys no spec this program can build would
// reproduce (only that run's discovery knew them), which is exactly why the
// opt-in may not rebuild the document.
const credsVarsDiscovered = `{
  "project": "proj-1234",
  "fugaro_project": "aurora",
  "region": "us-east5",
  "adopt_legacy_registry": true,
  "project_secrets": {"log-bucket": "fugaro-log-bucket", "git-token": "fugaro-git-token"}
}
`

// credsVarsDoc decodes a tfvars document for comparison, with credentials_service
// removed: what is left is every OTHER key, which both functions must leave
// exactly as they found it.
func credsVarsDoc(t *testing.T, vars []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(vars, &doc); err != nil {
		t.Fatalf("decoding %s: %v", vars, err)
	}
	delete(doc, "credentials_service")
	return doc
}

// SetCredentialsService writes the ONE key into the document the installation
// stage already wrote, and touches nothing else — "nothing else" being the
// project's live secret containers, whose absence plans their destruction
// (C1.18's module creates and adopts them; a rebuild says they should not
// exist).
//
// Mutation (verified, run and restored): rebuild the document from the spec
// instead of setting one key of it (marshal InstallationVars(InstallationSpec{
// CredentialsService: &svc}) and return that): this test fails — project_secrets
// and adopt_legacy_registry are gone.
func TestSetCredentialsServiceKeepsEveryOtherKey(t *testing.T) {
	svc := credsVarsSpec()
	got, err := SetCredentialsService([]byte(credsVarsDiscovered), svc)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		CredentialsService *CredentialsServiceSpec `json:"credentials_service"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the result is not a tfvars document: %v\n%s", err, got)
	}
	if doc.CredentialsService == nil {
		t.Fatalf("credentials_service was not written:\n%s", got)
	}
	if *doc.CredentialsService != svc {
		t.Errorf("credentials_service = %+v, want %+v", *doc.CredentialsService, svc)
	}
	if want, have := credsVarsDoc(t, []byte(credsVarsDiscovered)), credsVarsDoc(t, got); !mapsEqual(want, have) {
		t.Errorf("the document changed beyond the one key:\nwant %v\ngot  %v", want, have)
	}
	// The values that matter to the module's for_each, said again by name so
	// the failure reads for what it is.
	var after map[string]any
	if err := json.Unmarshal(got, &after); err != nil {
		t.Fatal(err)
	}
	secrets, _ := after["project_secrets"].(map[string]any)
	if len(secrets) != 2 {
		t.Errorf("project_secrets = %v, want the two containers discovery found: a rebuild would plan their destruction", after["project_secrets"])
	}
	if after["adopt_legacy_registry"] != true {
		t.Errorf("adopt_legacy_registry = %v, want it kept", after["adopt_legacy_registry"])
	}
}

// CarriedCredentialsService carries the opt-in across an ordinary run's
// rewrite of the tfvars (which is built from a spec that never carries the
// key), and takes the previous document exactly as it found it: a key that is
// there is carried, a key that is absent is not invented, and no other key of
// the new document moves.
//
// Mutation (verified, run and restored): return vars untouched (carry
// nothing): this test fails.
func TestCarriedCredentialsServiceCarriesExactlyTheOneKey(t *testing.T) {
	svc := credsVarsSpec()
	optedIn, err := SetCredentialsService([]byte(credsVarsDiscovered), svc)
	if err != nil {
		t.Fatal(err)
	}
	// The ordinary run's own document: the same discovery's keys, no
	// credentials_service (InstallationVars of a spec, which never sets it).
	fresh := []byte(`{"project": "proj-1234", "region": "us-east5", "project_secrets": {"log-bucket": "fugaro-log-bucket", "git-token": "fugaro-git-token"}}`)

	got, err := CarriedCredentialsService(fresh, optedIn)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		CredentialsService *CredentialsServiceSpec `json:"credentials_service"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the result is not a tfvars document: %v\n%s", err, got)
	}
	if doc.CredentialsService == nil || *doc.CredentialsService != svc {
		t.Fatalf("credentials_service = %v, want the key carried forward as %+v\n%s", doc.CredentialsService, svc, got)
	}
	if want, have := credsVarsDoc(t, fresh), credsVarsDoc(t, got); !mapsEqual(want, have) {
		t.Errorf("the carried document changed beyond the one key:\nwant %v\ngot  %v", want, have)
	}

	// An installation that never opted in carries nothing, and a workdir
	// with no tfvars at all (nil: a first run) is the same case.
	for name, prev := range map[string][]byte{"no key in the previous document": []byte(credsVarsDiscovered), "no previous document": nil} {
		got, err := CarriedCredentialsService(fresh, prev)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var d map[string]any
		if err := json.Unmarshal(got, &d); err != nil {
			t.Fatal(err)
		}
		if _, ok := d["credentials_service"]; ok {
			t.Errorf("%s: a key was invented: %s", name, got)
		}
		if want, have := credsVarsDoc(t, fresh), credsVarsDoc(t, got); !mapsEqual(want, have) {
			t.Errorf("%s: the document changed:\nwant %v\ngot  %v", name, want, have)
		}
	}
}

// A previous document this program cannot read is an error, not "nothing to
// carry": the direction that fails closed is the one that refuses to write a
// plan which drops the service.
//
// Mutation (verified, run and restored): return vars on a decode failure: this
// test fails.
func TestCarriedCredentialsServiceRefusesUnreadablePreviousVars(t *testing.T) {
	fresh := []byte(`{"project": "proj-1234"}`)
	for _, prev := range [][]byte{[]byte("not json"), []byte(""), []byte("[1,2]")} {
		if _, err := CarriedCredentialsService(fresh, prev); err == nil {
			t.Errorf("CarriedCredentialsService(fresh, %q) = nil error, want a refusal", prev)
		}
	}
}

// PrepareWorkdir hands the root's previous tfvars over BEFORE it rebuilds the
// tree, on Workdir.PrevVars (the ROOT CAUSE of C1.20's second review round,
// F3). After the rebuild the file is gone, so a caller that reads the path
// afterwards gets "nothing to carry" and an installation that opted in loses
// the key from its rewritten tfvars — and the next plan is the service's
// destruction, which the bundle container's prevent_destroy refuses.
//
// Mutation (verified, run and restored): read the tfvars after the
// os.RemoveAll of dir/gcp (fill PrevVars from the path once the tree is
// rebuilt): this test fails, PrevVars empty.
func TestPrepareWorkdirReadsTheVarsBeforeItRebuildsTheTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "installation")
	first, err := PrepareWorkdir(dir, "installation")
	if err != nil {
		t.Fatal(err)
	}
	if first.PrevVars != nil {
		t.Errorf("a fresh workdir carried %s, want nothing", first.PrevVars)
	}
	vars := []byte(credsVarsDiscovered)
	if err := first.WriteVars(vars); err != nil {
		t.Fatal(err)
	}

	second, err := PrepareWorkdir(dir, "installation")
	if err != nil {
		t.Fatal(err)
	}
	if string(second.PrevVars) != string(vars) {
		t.Errorf("the next workdir carried %q, want the tfvars the one before it held (%q)", second.PrevVars, vars)
	}
	// ...and the tree really was rebuilt: the file itself is gone, which is
	// why the copy is the only way to carry it.
	if _, err := os.Stat(WorkdirVarsPath(dir, "installation")); !os.IsNotExist(err) {
		t.Errorf("the rebuilt tree still holds %s (err %v): this test no longer proves what it says", VarsFile, err)
	}
	// A workdir for another root carries that root's own tfvars and never
	// this one's.
	other, err := PrepareWorkdir(filepath.Join(t.TempDir(), "compute"), "compute")
	if err != nil {
		t.Fatal(err)
	}
	if other.PrevVars != nil {
		t.Errorf("the compute root's workdir carried %q, want nothing", other.PrevVars)
	}
}

// mapsEqual compares two decoded JSON documents by their canonical encoding:
// the values here are plain JSON (strings, bools, objects), so equal bytes
// mean equal documents.
func mapsEqual(a, b map[string]any) bool {
	ja, err := json.Marshal(a)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ja) == string(jb)
}
