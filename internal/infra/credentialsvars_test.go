package infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
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
func credsVarsImage() string { return credsVarsImageAt("1.2.3") }

func credsVarsSpec() CredentialsServiceSpec {
	return CredentialsServiceSpec{
		AccountID:    CredentialsAccountID,
		Service:      CredentialsServiceName,
		Image:        credsVarsImage(),
		BundleSecret: "fugaro-creds-bundle",
		// The four facts the process is configured with (C1.14), the job
		// table among them: the carry has to move the whole object, and a
		// field dropped here is an environment variable the module writes
		// empty on the next apply.
		FirebaseProject: "aurora-fp",
		RTDBURL:         "https://aurora-fp-default-rtdb.firebaseio.com",
		JobAccounts: []CredentialsJob{
			{Provider: "github", Repo: "acme/webapp", Workflow: "web"},
			{Provider: "github", Repo: "acme/webapp", Workflow: "web_bmb"},
		},
		URL: "https://fugaro-credentials-123456789012.us-east5.run.app",
	}
}

// credsVarsSpecEqual compares two specs by their JSON: the object carries a
// list, so it is not comparable with ==.
func credsVarsSpecEqual(a, b CredentialsServiceSpec) bool {
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
	if !credsVarsSpecEqual(*doc.CredentialsService, svc) {
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
	if doc.CredentialsService == nil || !credsVarsSpecEqual(*doc.CredentialsService, svc) {
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

// CredentialsServiceImageOf answers the ONE question the re-pin asks of an
// installation's document — is there an opted-in service here, and which image
// does it run — and it answers it off the DISK, which is not the same
// question CarriedCredentialsService answers: this one distinguishes "no
// service" (nothing to move, and no registry call) from "a service", and it
// errs where a document it cannot read would otherwise read as "no service"
// and leave the opt-in behind.
//
// Mutation (verified, run and restored): treat an unreadable document as "no
// service" (return "", false, nil on a decode failure): this test fails.
func TestCredentialsServiceImageOfReadsTheOptedInImage(t *testing.T) {
	optedIn, err := SetCredentialsService([]byte(credsVarsDiscovered), credsVarsSpec())
	if err != nil {
		t.Fatal(err)
	}
	image, ok, err := CredentialsServiceImageOf(optedIn)
	if err != nil {
		t.Fatalf("reading the image of an opted-in installation: %v", err)
	}
	if !ok || image != credsVarsImage() {
		t.Errorf("CredentialsServiceImageOf = %q, %v, want %q, true", image, ok, credsVarsImage())
	}

	// The four "no service to move here" answers, none of which is an error:
	// a first run (no document at all), an installation that never opted in,
	// and an entry an operator's own hand left without an image.
	for name, vars := range map[string][]byte{
		"a first run":                         nil,
		"an installation that never opted in": []byte(credsVarsDiscovered),
		"an entry with no image":              []byte(`{"credentials_service": {"service": "fugaro-credentials"}}`),
		"an entry whose image is empty":       []byte(`{"credentials_service": {"image": ""}}`),
	} {
		image, ok, err := CredentialsServiceImageOf(vars)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if ok || image != "" {
			t.Errorf("%s: = %q, %v, want nothing to move", name, image, ok)
		}
	}

	// A document this program cannot read is an ERROR and not "nothing to
	// move": the carry fails closed for the same reason (an installation whose
	// opt-in this run cannot see is not an installation without one), and a
	// re-pin that read it as "no service" would apply the root on whatever
	// prepare made of the unreadable document.
	for _, vars := range [][]byte{[]byte("not json"), []byte("[1,2]"), []byte("")} {
		if _, _, err := CredentialsServiceImageOf(vars); err == nil {
			t.Errorf("CredentialsServiceImageOf(%q) = no error, want a refusal", vars)
		}
	}
}

// SetCredentialsServiceImage changes the image and NOTHING else: not the
// service's other facts (its account, its bundle, the Firebase database it
// reads, the audience its callers mint their ID tokens for — a rebuild would
// replace the audience with the module's placeholder and admit nobody), and
// not the document's other keys (the discovery's secret containers, which a
// rebuild plans away).
//
// The two refusals are the ones that keep it from being a way to turn the
// service on or to write an image the module refuses at plan time.
//
// Mutation (verified, run and restored): rebuild the entry from the spec
// (marshal CredentialsServiceSpec{Image: image}) instead of setting one key of
// it: this test fails, the entry losing its audience.
func TestSetCredentialsServiceImageChangesOnlyTheImage(t *testing.T) {
	svc := credsVarsSpec()
	optedIn, err := SetCredentialsService([]byte(credsVarsDiscovered), svc)
	if err != nil {
		t.Fatal(err)
	}
	next := "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.4@sha256:" + strings.Repeat("cd", 32)
	got, err := SetCredentialsServiceImage(optedIn, next)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		CredentialsService map[string]any `json:"credentials_service"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the result is not a tfvars document: %v\n%s", err, got)
	}
	if doc.CredentialsService["image"] != next {
		t.Errorf("credentials_service.image = %v, want %q", doc.CredentialsService["image"], next)
	}
	want := map[string]any{
		"account_id":       svc.AccountID,
		"service":          svc.Service,
		"bundle_secret":    svc.BundleSecret,
		"firebase_project": svc.FirebaseProject,
		"rtdb_url":         svc.RTDBURL,
		"url":              svc.URL,
	}
	for k, v := range want {
		if doc.CredentialsService[k] != any(v) {
			t.Errorf("credentials_service.%s = %v, want %v kept: the re-pin changes the image and nothing else", k, doc.CredentialsService[k], v)
		}
	}
	if jobs, _ := doc.CredentialsService["job_accounts"].([]any); len(jobs) != len(svc.JobAccounts) {
		t.Errorf("credentials_service.job_accounts = %v, want the %d rows the opt-in wrote", doc.CredentialsService["job_accounts"], len(svc.JobAccounts))
	}
	if wantDoc, have := credsVarsDoc(t, optedIn), credsVarsDoc(t, got); !mapsEqual(wantDoc, have) {
		t.Errorf("the document changed beyond the one key:\nwant %v\ngot  %v", wantDoc, have)
	}

	// An image that is not pinned by digest is refused, as the module's own
	// variable refuses it: a tag is mutable, and this service reads every model
	// credential the installation holds.
	for _, image := range []string{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.4",
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.4@sha256:",
		"us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.4@sha256:" + strings.Repeat("AB", 32),
		"",
	} {
		if _, err := SetCredentialsServiceImage(optedIn, image); err == nil {
			t.Errorf("SetCredentialsServiceImage(%q) = no error, want a refusal", image)
		}
	}

	// A document with no service is refused rather than given one: writing the
	// image there would turn the service on — account, bundle, grant and all —
	// for an installation whose owner never opted in.
	if _, err := SetCredentialsServiceImage([]byte(credsVarsDiscovered), next); err == nil {
		t.Error("SetCredentialsServiceImage gave a document with no credentials_service one: the image is a release's, and writing it there turns the service on for an owner who never asked")
	}
	if _, err := SetCredentialsServiceImage([]byte("not json"), next); err == nil {
		t.Error("SetCredentialsServiceImage rewrote a document it could not read")
	}
}

// The release an image reference names is read by BOTH sides out of the same
// string: this program, which decides whether the installation needs the
// re-pin, and the module's credentials.tf (credentials_image_release), which
// decides whether the process in that image serves HTTP/2 cleartext and
// therefore whether the Cloud Run container's port is named h2c. The two
// patterns must move together — a reference whose version one side can read
// and the other cannot is an installation whose image and port disagree — so
// the module is read here, from the embedded tree the binary actually plans
// with.
//
// Mutation (verified, run and restored): change either pattern (drop the
// trailing colon from this one, or the doubled backslash from the module's):
// this test fails.
func TestCredentialsServiceImageReleaseIsTheModulesOwnReading(t *testing.T) {
	mod, err := terraform.FS.ReadFile("gcp/modules/installation/credentials.tf")
	if err != nil {
		t.Fatal(err)
	}
	// HCL escapes a backslash in a string literal, so the module writes the
	// pattern with two of them where Go writes one.
	hcl := strings.ReplaceAll(credsImageReleaseRE.String(), `\`, `\\`)
	if !strings.Contains(string(mod), `regex("`+hcl+`"`) {
		t.Errorf("the module does not read the release with this program's pattern (%s): the version one side reads and the other cannot is an installation whose container port and image disagree", credsImageReleaseRE.String())
	}

	for _, tc := range []struct{ ref, want string }{
		{credsVarsImage(), "1.2.3"},
		{credsVarsImageAt("1.10.0"), "1.10.0"},
		// A digest with no release tag, and a mutable tag: neither names a
		// release, on either side, and the re-pin treats both as "move it".
		{"ghcr.io/dimipaun/fugaro-credentials@sha256:" + strings.Repeat("ab", 32), ""},
		{"ghcr.io/dimipaun/fugaro-credentials:latest@sha256:" + strings.Repeat("ab", 32), ""},
		{"ghcr.io/dimipaun/fugaro-credentials:v1.2.3@sha256:" + strings.Repeat("ab", 32), ""},
		{"", ""},
	} {
		if got := CredentialsServiceImageRelease(tc.ref); got != tc.want {
			t.Errorf("CredentialsServiceImageRelease(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// credsVarsImageAt is credsVarsImage for another release.
func credsVarsImageAt(version string) string {
	return "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:" + version + "@sha256:" + strings.Repeat("ab", 32)
}
