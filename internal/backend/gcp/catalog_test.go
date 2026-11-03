package gcp

import (
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"testing"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// catalogSlug is the repository whose build account runs the catalog builds
// below.
func catalogSlug(t *testing.T) string {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	return slug
}

// catalogBuildSpec is one build of the entry "agent" by that repository's
// build account: the request the catalog path submits (PIC4).
func catalogBuildSpec(t *testing.T) CatalogBuildSpec {
	t.Helper()
	slug := catalogSlug(t)
	image, err := SharedCatalogImage("us-east5", "proj-1234", "agent")
	if err != nil {
		t.Fatal(err)
	}
	return CatalogBuildSpec{
		Entry: "agent", Slug: slug,
		GitProvider: "bitbucket", RepoURL: "https://bitbucket.org/acme/app.git", BaseBranch: "main",
		GitSecretID: SecretID(slug, "bitbucket-token"), GitUser: "x-token-auth",
		Base:  "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc",
		Image: image,
		// What PIC3's copy recorded and a build is submitted with: the
		// commit the definition was taken from, the copied object's
		// generation and the sha256 of the Dockerfile's bytes.
		Definition: CatalogDefinition{
			SourceCommit: strings.Repeat("a", 40),
			Generation:   1712345678901234,
			SHA256:       strings.Repeat("b", 64),
		},
		ServiceAccount:  BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com",
		MachineType:     "E2_HIGHCPU_8",
		WorkflowSecrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}},
		Bucket:          "gs://fugaro-runs-proj-1234",
		Selftest:        []byte(`{"skip_verify":true,"tools":"critical"}`),
	}
}

// stepIDs are the ids of b's steps, in order.
func stepIDs(b *cloudbuild.Build) []string {
	ids := make([]string, 0, len(b.Steps))
	for _, st := range b.Steps {
		ids = append(ids, st.Id)
	}
	return ids
}

// TestCatalogBuildReadsTheCopiedDockerfile: a catalog build has no checkout
// and no render step, so it starts from the one object PIC3 copied —
// builds/catalog/<entry>/Dockerfile — and its whole build context is that
// file. The steps it does not need are not in the request at all: a catalog
// build never clones a repository into the build, never reads a push
// credential of its own and never gates on the workflow record.
func TestCatalogBuildReadsTheCopiedDockerfile(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prep", "credential", catalogDockerfileStepID, catalogPinStepID, "build", "smoke", "promote", catalogTagStepID, catalogRecordStepID, "untag"}
	if ids := stepIDs(b); !slices.Equal(ids, want) {
		t.Fatalf("steps = %v, want %v", ids, want)
	}
	key, err := config.CatalogDockerfileKey("agent")
	if err != nil {
		t.Fatal(err)
	}
	df := step(t, b, catalogDockerfileStepID)
	if env := df.Env; !slices.Equal(env, []string{"BUCKET=gs://fugaro-runs-proj-1234", "KEY=" + key}) {
		t.Fatalf("the read step's env = %v", env)
	}
	if script := strings.Join(df.Args, "\n"); !strings.Contains(script, "/workspace/context/Dockerfile") {
		t.Fatalf("the read step does not write the context: %q", script)
	}
	// The context the build step builds is the copy and nothing else: no step
	// of the request clones a repository, and none reads the workflow's own
	// source or record.
	buildScript := strings.Join(step(t, b, "build").Args, "\n")
	if !strings.Contains(buildScript, `--tag "$$candidate" /workspace/context`) {
		t.Fatalf("the build step does not build the copied definition: %q", buildScript)
	}
	for _, st := range b.Steps {
		if script := strings.Join(st.Args, "\n"); strings.Contains(script, "clone") || strings.Contains(script, "_WORKFLOW") {
			t.Fatalf("step %s reaches outside the copied definition: %q", st.Id, script)
		}
	}
	if b.Substitutions["_IMAGE"] != s.Image || b.Substitutions["_FUGARO_BASE"] != s.Base || b.Substitutions["_REPO_URL"] != s.RepoURL {
		t.Fatalf("substitutions = %v", b.Substitutions)
	}
	if b.ServiceAccount != "projects/proj-1234/serviceAccounts/"+s.ServiceAccount {
		t.Fatalf("service account = %s", b.ServiceAccount)
	}
}

// TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv is the SEC property of
// this task (D1150): an entry's build secret reaches the build the way a
// workflow secret already does — the value is a Secret Manager version in
// availableSecrets, the variable is in the build step's secretEnv and in
// _SECRET_ENVS, and the step writes it into a private temporary file and
// passes that file to BuildKit as a file secret. It is never an ENV, a
// build argument, a layer, a step's env: or a log line; a mount that would
// export it (BuildKit's env=) is refused by Cloud Build's docker anyway.
func TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	slug := catalogSlug(t)
	var secrets []string
	for _, sm := range b.AvailableSecrets.SecretManager {
		secrets = append(secrets, sm.Env+"="+sm.VersionName)
	}
	want := []string{
		"GIT_TOKEN=projects/proj-1234/secrets/" + s.GitSecretID + "/versions/latest",
		"NPM_TOKEN=projects/proj-1234/secrets/" + SecretID(slug, "npm-token") + "/versions/latest",
	}
	if !slices.Equal(secrets, want) {
		t.Fatalf("availableSecrets = %v, want %v", secrets, want)
	}
	build := step(t, b, "build")
	if !slices.Equal(build.SecretEnv, []string{"NPM_TOKEN"}) {
		t.Fatalf("the build step's secretEnv = %v", build.SecretEnv)
	}
	if b.Substitutions["_SECRET_ENVS"] != "NPM_TOKEN" {
		t.Fatalf("_SECRET_ENVS = %q", b.Substitutions["_SECRET_ENVS"])
	}
	// The variable name may appear in the loop the step reads
	// _SECRET_ENVS with, and nowhere as an env: or a build argument: a
	// secret in the step's env: is a secret in the build's environment, its
	// log and every child process; a build argument is in the image's
	// history.
	script := strings.Join(build.Args, "\n")
	for _, st := range b.Steps {
		if slices.ContainsFunc(st.Env, func(e string) bool { return strings.HasPrefix(e, "NPM_TOKEN=") }) {
			t.Fatalf("step %s carries the secret variable in its env: %v", st.Id, st.Env)
		}
		switch st.Id {
		case "build":
		default:
			// Only the credential step reads another secret, and it is the
			// provider credential, not the entry's.
			if len(st.SecretEnv) > 0 && (st.Id != credentialStep || !slices.Equal(st.SecretEnv, []string{gitTokenEnv})) {
				t.Fatalf("step %s mounts a secret it does not need: %v", st.Id, st.SecretEnv)
			}
		}
	}
	if strings.Contains(script, "NPM_TOKEN") {
		t.Fatalf("the build step's script names the secret variable: %q", script)
	}
	if !strings.Contains(script, `--secret "id=$$e,src=$$sec/$$e"`) {
		t.Fatalf("the build step does not pass a file secret: %q", script)
	}
	if !strings.Contains(script, "umask 077") {
		t.Fatalf("the secret file is not written private: %q", script)
	}
	if strings.Contains(script, "env=") {
		t.Fatalf("the build step mounts a secret as an env: %q", script)
	}
	// The names reach the script through its own environment, never through
	// the textual substitution the request applies.
	if strings.Contains(script, "${_SECRET_ENVS") {
		t.Fatalf("the script holds a substitution the request would rewrite: %q", script)
	}
}

// TestCatalogBuildWritesARecordTheCheckReads: the build writes the entry's
// record under builds/<slug>/, where the repository's build account already
// writes and the daily check already reads, and the record holds what the
// build itself measured: the copied object's generation, the sha256 of the
// bytes it built, the commit the copy was taken from and the pushed digest.
// A record key under builds/catalog/ would need a write grant this task
// deliberately does not give a build (its catalog grant is read-only).
func TestCatalogBuildWritesARecordTheCheckReads(t *testing.T) {
	s := catalogBuildSpec(t)
	slug := catalogSlug(t)
	key, err := CatalogRecordKey(slug, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if want := "builds/" + slug + "/catalog/agent/image.json"; key != want {
		t.Fatalf("CatalogRecordKey = %q, want %q", key, want)
	}
	if !strings.HasPrefix(key, "builds/"+slug+"/") {
		t.Fatalf("the record key %q is outside the build account's own prefix", key)
	}
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	rec := step(t, b, catalogRecordStepID)
	want := []string{
		"BUCKET=" + s.Bucket, "KEY=" + key, "ENTRY=agent", "IMAGE=" + s.Image,
		"GENERATION=" + strconv.FormatInt(s.Definition.Generation, 10),
		"SHA256=" + s.Definition.SHA256, "COMMIT=" + s.Definition.SourceCommit,
		"BASE=" + s.Base, "TAG=${_CATALOG_TAG}",
	}
	if env := rec.Env; !slices.Equal(env, want) {
		t.Fatalf("the record step's env = %v, want %v", env, want)
	}
	script := strings.Join(rec.Args, "\n")
	// Every field imagecheck.CatalogRecordState reads, and the two extra a
	// human wants: the image, its content tag and the build that wrote it.
	for _, field := range []string{
		`"version":`, `"entry":"`, `"source_commit":"`, `"definition_generation":`,
		`"definition_sha256":"`, `"base_ref":"`, `"base_digest":"`, `"image":"`,
		`"content_tag":"`, `"image_digest":"`, `"build_id":"`, `"built_at":"`,
	} {
		if !strings.Contains(script, field) {
			t.Fatalf("the record does not carry %s: %q", field, script)
		}
	}
	for _, value := range []string{"$$SHA256", "$$GENERATION", "$$COMMIT", "/workspace/out/image-digest", "/workspace/base-digest"} {
		if !strings.Contains(script, value) {
			t.Fatalf("the record does not read %s: %q", value, script)
		}
	}
	// Every value the record carries is constrained (a validated entry name,
	// hex sums, a digits-only generation, a validated image reference and
	// Cloud Build's own build ID), so the step can compose the JSON without
	// escaping anything.
	if !strings.Contains(script, `"version":`) {
		t.Fatalf("the record carries no version: %q", script)
	}
	// The two values that come from files this step did not write are the
	// digests, and both are checked for shape before they reach the printf:
	// a file holding something else fails the build instead of writing a
	// record the check cannot read.
	for _, digest := range []string{"image_digest", "base_digest"} {
		if !strings.Contains(script, `case "$$`+digest+`" in sha256:*)`) {
			t.Fatalf("the record does not check %s for shape: %q", digest, script)
		}
	}
	// base_digest holds what imagecheck's field holds — a bare sha256:… — and
	// the file the build step wrote holds the base as <ref>@sha256:…, so the
	// step strips the reference, as `fugaro image record` does for the
	// workflow's record of the same field.
	if !strings.Contains(script, "base_digest=$${base_digest#*@}") {
		t.Fatalf("the record keeps the base's reference in base_digest: %q", script)
	}
	// One build time, not two: the record reads the timestamp the pin step
	// wrote, which is the one the build step stamps the image's created label
	// with, so a build whose record and image disagree about when it ran is
	// not a thing this can produce.
	if !strings.Contains(script, "/workspace/out/built-at") {
		t.Fatalf("the record does not read the pinned build time: %q", script)
	}
	if strings.Contains(script, "date -u") || strings.Contains(script, "$$(date") {
		t.Fatalf("the record reads the clock a second time: %q", script)
	}
	pinScript := strings.Join(step(t, b, catalogPinStepID).Args, "\n")
	if !strings.Contains(pinScript, "/workspace/out/built-at") || !strings.Contains(pinScript, "date -u") {
		t.Fatalf("the pin step does not write the build time the record reads: %q", pinScript)
	}
	// The pin step writes what the record and the build step read, and reads
	// nothing else.
	pin := step(t, b, catalogPinStepID)
	pinWant := []string{
		"BUCKET=" + s.Bucket, "KEY=" + keyOf(t, "agent"),
		"GENERATION=" + strconv.FormatInt(s.Definition.Generation, 10),
		"SHA256=" + s.Definition.SHA256, "COMMIT=" + s.Definition.SourceCommit,
		"SELFTEST_B64=${_SELFTEST_B64}",
	}
	if env := pin.Env; !slices.Equal(env, pinWant) {
		t.Fatalf("the pin step's env = %v, want %v", env, pinWant)
	}
}

// TestCatalogBuildRefusesWhenTheCopyIsMissing: a build whose definition is
// not there does not build something else, and a build whose copy changed
// since it was submitted fails instead of recording a pin that is not what
// it built. The read step fails closed (a bare cp under set -eu), and the
// pin step compares the generation and the sha256 of what it read with the
// ones the build was submitted with.
func TestCatalogBuildRefusesWhenTheCopyIsMissing(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	read := strings.Join(step(t, b, catalogDockerfileStepID).Args, "\n")
	if !strings.Contains(read, "set -eu") {
		t.Fatalf("the read step does not fail closed: %q", read)
	}
	pin := strings.Join(step(t, b, catalogPinStepID).Args, "\n")
	if !strings.Contains(pin, "generation") || !strings.Contains(pin, "sha256") {
		t.Fatalf("the pin step checks neither the generation nor the sum: %q", pin)
	}
	if !strings.Contains(pin, "exit 1") {
		t.Fatalf("the pin step does not fail the build: %q", pin)
	}
	// A submission without a pin is refused before any request is built: a
	// build that cannot say what it read cannot record it either.
	for name, mod := range map[string]func(*CatalogBuildSpec){
		"no generation": func(s *CatalogBuildSpec) { s.Definition.Generation = 0 },
		"no sum":        func(s *CatalogBuildSpec) { s.Definition.SHA256 = "" },
		"a short sum":   func(s *CatalogBuildSpec) { s.Definition.SHA256 = "abc" },
		"no commit":     func(s *CatalogBuildSpec) { s.Definition.SourceCommit = "" },
		"a bad commit":  func(s *CatalogBuildSpec) { s.Definition.SourceCommit = "main" },
		"no selftest":   func(s *CatalogBuildSpec) { s.Selftest = nil },
	} {
		// bad is a copy of the spec, made here and never stored: a row's
		// mutation reaches the request built from it and nothing else, so the
		// rows are independent of each other and of the spec above.
		bad := s
		mod(&bad)
		if _, err := CatalogBuildRequest("proj-1234", bad); err == nil || !strings.Contains(err.Error(), "invalid Cloud Build request") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// The copy's key is derived from the entry's name and never taken from
	// the caller, so a request cannot point a build at another object.
	for _, entry := range []string{"", "Agent", "a/b", "..", "agent\n", "-agent", strings.Repeat("a", 41)} {
		bad := s
		bad.Entry = entry
		if _, err := CatalogBuildRequest("proj-1234", bad); err == nil {
			t.Errorf("the entry %q was accepted", entry)
		}
	}
}

// TestCatalogBuildRefusesABaseThatIsNotAnImageRef: the record step composes
// its JSON with printf and no escaping, on the claim that every value it
// carries is constrained. The base is the value that was constrained only to
// be non-empty, and it is written into a JSON string in the middle of that
// printf: a quote in it would close the string early, a backslash would
// escape whatever follows, and a newline or any other control character
// would end the record's line. All four are refused before a request is
// built, and the reference a real build is given still passes.
func TestCatalogBuildRefusesABaseThatIsNotAnImageRef(t *testing.T) {
	s := catalogBuildSpec(t)
	if _, err := CatalogBuildRequest("proj-1234", s); err != nil {
		t.Fatalf("the reference base %q was refused: %v", s.Base, err)
	}
	const ref = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc"
	for name, base := range map[string]string{
		"a quote":             ref + `"`,
		"a backslash":         ref + `\`,
		"a newline":           ref + "\n",
		"a control character": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-\x1babc",
		"a space":             "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev abc",
		// A JSON field of the record's own, smuggled in through the base.
		"a field": ref + `","built_at":"1970-01-01T00:00:00Z`,
	} {
		bad := s
		bad.Base = base
		if _, err := CatalogBuildRequest("proj-1234", bad); err == nil || !strings.Contains(err.Error(), "invalid Cloud Build request") {
			t.Errorf("%s in the base was accepted (%v)", name, err)
		}
	}
	// The refusal quotes the value, so an escape sequence in a refused base
	// cannot reach a log as itself.
	bad := s
	bad.Base = ref + "\n"
	_, err := CatalogBuildRequest("proj-1234", bad)
	if err == nil {
		t.Fatal("a base with a newline was accepted")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("the refusal carries the newline: %q", err)
	}
}

// TestCatalogImageLandsInTheSharedRegistryWithAContentTag: D1152 — the
// image is per project, so it lands in the installation's one shared
// registry, named from the entry (catalog-<entry>), tagged with the content
// hash of the definition it was built from, with the moving latest beside
// it, and with no per-repository registry, no digest pin and no new
// retention rule anywhere.
func TestCatalogImageLandsInTheSharedRegistryWithAContentTag(t *testing.T) {
	s := catalogBuildSpec(t)
	want := "us-east5-docker.pkg.dev/proj-1234/" + SharedRegistryRepoID() + "/catalog-agent"
	if s.Image != want {
		t.Fatalf("SharedCatalogImage = %q, want %q", s.Image, want)
	}
	// The repository's own registry is not where a catalog image goes.
	if strings.Contains(s.Image, RegistryRepoID(catalogSlug(t))) {
		t.Fatalf("the catalog image is in the repository's own registry: %s", s.Image)
	}
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	// The build pushes only the candidate; promote moves latest, and the
	// content tag is added to the same digest, so the tag a job names and
	// the tag a rebuild moves are the same image.
	if tag := b.Substitutions["_CATALOG_TAG"]; tag != s.Definition.SHA256 {
		t.Fatalf("_CATALOG_TAG = %q, want the definition's sha256", tag)
	}
	tagStep := strings.Join(step(t, b, catalogTagStepID).Args, "\n")
	if !strings.Contains(tagStep, `"$$IMAGE:$$TAG"`) || !strings.Contains(tagStep, "/workspace/out/image-digest") {
		t.Fatalf("the content tag is not added to the pushed digest: %q", tagStep)
	}
	promote := strings.Join(step(t, b, "promote").Args, "\n")
	if !strings.Contains(promote, `"$$IMAGE:latest"`) {
		t.Fatalf("the build does not move latest: %q", promote)
	}
	if len(b.Images) != 0 {
		t.Fatalf("images = %v, want none (the steps push and retag)", b.Images)
	}
	for _, region := range []string{"", "us-east5-", "us_east5"} {
		if _, err := SharedCatalogImage(region, "proj-1234", "agent"); err == nil {
			t.Errorf("the region %q was accepted", region)
		}
	}
	for _, project := range []string{"", "Proj-1234", "proj_1234"} {
		if _, err := SharedCatalogImage("us-east5", project, "agent"); err == nil {
			t.Errorf("the project %q was accepted", project)
		}
	}
}

// TestCatalogBuildRefusesAnImageOutsideTheSharedRegistry: the image a
// catalog build pushes is not taken from the caller, so the request is only
// built for the entry's own name in the installation's shared registry of
// the project the build runs in. The host is what precedes the registry id
// in the name, and registryHostRE is what says it is a registry host of
// this project: it cannot match an empty host, so an image whose host is
// empty — "/<registry>/catalog-agent" — is refused here like any other name
// in no registry. SharedRegistryRepoID never being empty is what keeps that
// from being the only thing between the comparison and a bypass, and
// TestSharedRegistryRepoIDNeverEqualsARepositorysOwn pins it.
func TestCatalogBuildRefusesAnImageOutsideTheSharedRegistry(t *testing.T) {
	s := catalogBuildSpec(t)
	other, err := SharedCatalogImage("us-east5", "proj-5678", "agent")
	if err != nil {
		t.Fatal(err)
	}
	regID := SharedRegistryRepoID()
	for name, image := range map[string]string{
		"no host":                  "/" + regID + "/catalog-agent",
		"another project":          other,
		"another repository":       "us-east5-docker.pkg.dev/proj-1234/fugaro-other/catalog-agent",
		"another entry's image":    "us-east5-docker.pkg.dev/proj-1234/" + regID + "/catalog-other",
		"the entry's image + more": s.Image + "-x",
		"a tag on it":              s.Image + ":latest",
		"a digest on it":           s.Image + "@sha256:" + strings.Repeat("a", 64),
		"a host that is not one":   "example.com/proj-1234/" + regID + "/catalog-agent",
	} {
		bad := s
		bad.Image = image
		if _, err := CatalogBuildRequest("proj-1234", bad); err == nil || !strings.Contains(err.Error(), "invalid Cloud Build request") {
			t.Errorf("%s: the image %q was accepted (%v)", name, image, err)
		}
	}
	if _, err := CatalogBuildRequest("proj-1234", s); err != nil {
		t.Fatalf("the entry's own image %q was refused: %v", s.Image, err)
	}
	// The host's region is not part of what this check pins, and cannot be:
	// registryHostRE says the host is a registry host of this project, and
	// the spec carries no region to compare the host's against. An image in
	// another region of the same project therefore passes here and is refused
	// where it matters, at the registry, whose grant is on one region's
	// repository (build_shared, deploy/terraform/gcp/modules/repo/registry.tf).
	// BuildSpec.check, which predates this path, has the same shape, so this
	// is not a catalog build's own gap. Pinned so a change to it is not
	// silent.
	inRegion := s
	inRegion.Image = "us-east1-docker.pkg.dev/proj-1234/" + regID + "/catalog-agent"
	if _, err := CatalogBuildRequest("proj-1234", inRegion); err != nil {
		t.Fatalf("an image in another region of this project was refused: %v", err)
	}
}

// keyOf is the Dockerfile key of an entry, as a test reads it.
func keyOf(t *testing.T, entry string) string {
	t.Helper()
	key, err := config.CatalogDockerfileKey(entry)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// TestCatalogReadConditionCoversEveryCopy: the build account's read grant is
// the other half of the build's read step — without it the step's cp is a
// 403 — so the one prefix the grant names must be the prefix every entry's
// copied definition sits under, and it must not reach anything else in the
// bucket: not the record (under the repository's own prefix), not the budget
// token, not the marker at the bucket's root.
func TestCatalogReadConditionCoversEveryCopy(t *testing.T) {
	const bucket = "fugaro-runs-proj-1234"
	cond := CatalogReadCondition(bucket)
	if want := `resource.name.startsWith("projects/_/buckets/` + bucket + `/objects/builds/catalog/")`; cond != want {
		t.Fatalf("CatalogReadCondition = %q, want %q", cond, want)
	}
	prefix := "projects/_/buckets/" + bucket + "/objects/" + CatalogBucketPrefix + "/"
	for _, entry := range []string{"agent", "claude", "a", strings.Repeat("a", 40)} {
		if key := keyOf(t, entry); !strings.HasPrefix(key, CatalogBucketPrefix+"/") {
			t.Fatalf("the read step's key %q is not under the prefix the grant names (%s): %q", key, CatalogBucketPrefix, cond)
		}
	}
	key, err := CatalogRecordKey(catalogSlug(t), "agent")
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range []string{key, "builds/" + catalogSlug(t) + "/web/image.json", "tokens/" + catalogSlug(t) + "/budget.json", "fugaro/managed.json"} {
		if strings.HasPrefix("projects/_/buckets/"+bucket+"/objects/"+other, prefix) {
			t.Fatalf("the catalog grant's prefix %q covers %q", CatalogBucketPrefix, other)
		}
	}
}

// TestCatalogBuildSelftestIsTheSmokeSpec: the catalog build runs today's
// runtime-contract smoke (image.SpecForCloud), which the caller renders and
// the build carries unchanged: the spec is not secret, so it may travel in
// the request, and it travels base64 so that a value the request substitutes
// textually cannot rewrite the request's own YAML.
func TestCatalogBuildSelftestIsTheSmokeSpec(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.Substitutions["_SELFTEST_B64"], base64.StdEncoding.EncodeToString(s.Selftest); got != want {
		t.Fatalf("_SELFTEST_B64 = %q, want %q", got, want)
	}
	pin := strings.Join(step(t, b, catalogPinStepID).Args, "\n")
	if !strings.Contains(pin, "/workspace/out/selftest.json") || !strings.Contains(pin, "base64 -d") {
		t.Fatalf("the smoke's spec is not written where the smoke reads it: %q", pin)
	}
	if _, ok := b.Substitutions["_WORKFLOW"]; ok {
		t.Fatalf("the catalog build sends a substitution no step of it references: %v", b.Substitutions)
	}
}
