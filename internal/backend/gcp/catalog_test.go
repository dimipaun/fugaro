package gcp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// catalogSlug is the repository the catalog builds below name: the one whose
// provider credential clones, under whose builds/<slug>/ prefix the record
// lands, and that is why the repository's own build account is the wrong one
// for these builds to run as (B6).
func catalogSlug(t *testing.T) string {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	return slug
}

// catalogBuildSpec is one build of the entry "agent" by the bitbucket
// repository: the request the catalog path submits (PIC4), run as the
// project's build identity (B6).
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
		// The installation's credentials service, which the credential step
		// asks for the entry's git credential (IS8): a catalog build's
		// source repository is the one the build itself was submitted from,
		// so the credential a build clones with is the service's answer and
		// no secret of the project's is mounted or named.
		CredentialsServiceURL: "https://credentials.example.com",
		Base:                  "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc",
		Image:                 image,
		// What PIC3's copy recorded and a build is submitted with: the
		// commit the definition was taken from, the copied object's
		// generation and the sha256 of the Dockerfile's bytes.
		Definition: CatalogDefinition{
			SourceCommit: strings.Repeat("a", 40),
			Generation:   1712345678901234,
			SHA256:       strings.Repeat("b", 64),
		},
		ServiceAccount: BuildIdentityEmail("proj-1234"),
		MachineType:    "E2_HIGHCPU_8",
		Bucket:         "gs://fugaro-runs-proj-1234",
		Selftest:       []byte(`{"skip_verify":true,"tools":"critical"}`),
		// What a submitter states about the build itself: the digest of the
		// template it would build with, which the record carries so the check
		// sees an engine-side change (F33, D1402).
		TemplateSalt: strings.Repeat("c", 64),
	}
}

// catalogContextSpec is catalogBuildSpec with the entry's source naming a
// build context: the directory the build runs on, the commit the copy and the
// context were read at — the same commit, which is what makes the entry's
// build one tree — and the digest of the context tree the copy staged beside
// the Dockerfile (CB1's keys).
func catalogContextSpec(t *testing.T) CatalogBuildSpec {
	t.Helper()
	s := catalogBuildSpec(t)
	s.Context = "."
	s.ContextCommit = s.Definition.SourceCommit
	s.ContextSHA256 = strings.Repeat("c", 64)
	return s
}

// stepIDs are the ids of b's steps, in order.
func stepIDs(b *cloudbuild.Build) []string {
	ids := make([]string, 0, len(b.Steps))
	for _, st := range b.Steps {
		ids = append(ids, st.Id)
	}
	return ids
}

// TestCatalogBuildReadsTheCopiedDockerfile: a catalog build has no render step,
// so it starts from the one object PIC3 copied —
// builds/catalog/<entry>/Dockerfile — and its whole build context is that file.
// The steps it does not need are not in the request at all, and the one step
// that reaches a repository is the clone step: every other step of the request
// works on the copied definition, on the checkout the clone exported or on the
// image, and none of them reads the workflow's own source or record.
func TestCatalogBuildReadsTheCopiedDockerfile(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prep", "credential", catalogDockerfileStepID, catalogCloneStepID, catalogPinStepID, "build", "smoke", catalogScanStepID, "promote", catalogTagStepID, catalogRecordStepID, "untag"}
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
	// The context the build step builds is the copied definition and nothing
	// else: no step but the clone reaches a repository, and none reads the
	// workflow's own source or record.
	buildScript := strings.Join(step(t, b, "build").Args, "\n")
	if !strings.Contains(buildScript, "--file /workspace/context/Dockerfile") || !strings.Contains(buildScript, `--tag "$$candidate" "$$CONTEXT_DIR"`) {
		t.Fatalf("the build step does not build the copied definition: %q", buildScript)
	}
	for _, st := range b.Steps {
		script := strings.Join(st.Args, "\n")
		if st.Id != catalogCloneStepID && strings.Contains(script, "clone") {
			t.Fatalf("step %s reaches outside the copied definition: %q", st.Id, script)
		}
		if strings.Contains(script, "_WORKFLOW") {
			t.Fatalf("step %s reads the workflow's own record: %q", st.Id, script)
		}
	}
	if b.Substitutions["_IMAGE"] != s.Image || b.Substitutions["_FUGARO_BASE"] != s.Base || b.Substitutions["_REPO_URL"] != s.RepoURL {
		t.Fatalf("substitutions = %v", b.Substitutions)
	}
	if b.ServiceAccount != "projects/proj-1234/serviceAccounts/"+s.ServiceAccount {
		t.Fatalf("service account = %s", b.ServiceAccount)
	}
}

// TestCatalogBuildRequestCarriesTheServiceURL: the address the credential step
// fetches at travels from the spec as it does on a repository's build (B4.5),
// and on this path it is the whole credential: a catalog build runs as the
// project's build identity (B6), which is the caller the entry route admits, so
// the step asks the installation's service for the entry's git credential and
// a request that names no service is refused before a build is paid for
// (IS8, D3330).
//
// Mutation (verified, run and restored): drop the `_CREDENTIALS_SERVICE_URL`
// entry from this request's substitutions and this fails with a step that reads
// `${_CREDENTIALS_SERVICE_URL}` and a request that never declared it — which
// Cloud Build refuses.
func TestCatalogBuildRequestCarriesTheServiceURL(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Substitutions["_CREDENTIALS_SERVICE_URL"]; got != s.CredentialsServiceURL {
		t.Fatalf("_CREDENTIALS_SERVICE_URL = %q, want %q", got, s.CredentialsServiceURL)
	}
	cred := step(t, b, credentialStep)
	if !slices.Contains(cred.Env, "FUGARO_CREDENTIALS_SERVICE_URL=${_CREDENTIALS_SERVICE_URL}") {
		t.Fatalf("the credential step does not read the service's address from its env: %v", cred.Env)
	}
	if args := strings.Join(cred.Args, " "); strings.Contains(args, s.CredentialsServiceURL) {
		t.Errorf("the credential step's argv carries the address: %q", args)
	}
	// A catalog build is submitted only where the credential step can fetch: an
	// installation whose service is not there is a build that would fail at its
	// first step having cloned nothing, and the refusal names what is missing
	// rather than submitting it.
	s.CredentialsServiceURL = ""
	_, err = CatalogBuildRequest("proj-1234", s)
	if !errors.Is(err, ErrBadBuildSpec) || err == nil || !strings.Contains(err.Error(), "credentials service") {
		t.Fatalf("a catalog build with no credentials service was answered %v, want a bad-spec refusal naming it", err)
	}
}

// TestCatalogBuildRequestNamesNoSecret is IS8's own pin (D3330): the build of a
// catalog entry used to mount its source repository's git credential — one
// availableSecret, the credential step's secretEnv, and the per-repository
// accessor the installation granted the build identity for it
// (modules/installation/build_identity.tf) — and it names none of that now. The
// credential step runs the entry mode instead (internal/cli/gitcred.go): one
// request for the entry being built, asked of the installation's credentials
// service as the project's build identity, naming the entry, the build, the
// provider, the repositories the bake composes stages for and the lifetime the
// step needs — and the answer's one credential is what it writes.
//
// The whole request is read rather than the two fields: what a step names is
// what Cloud Build resolves, so a secret left in a step's env, in its argv or
// in the substitutions that feed either is a mount the entry's build would have
// again.
func TestCatalogBuildRequestNamesNoSecret(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	if b.AvailableSecrets != nil {
		t.Fatalf("the request names the secrets %+v, want none: a catalog build's git credential is the service's answer and no secret of the project's (IS8, D3330)", b.AvailableSecrets.SecretManager)
	}
	for _, st := range b.Steps {
		if len(st.SecretEnv) > 0 {
			t.Fatalf("step %s reads secretEnv %v, want none: an entry's build is handed no secret of the repository's (B9, D2311) and this request mounts none at all", st.Id, st.SecretEnv)
		}
	}
	cred := step(t, b, credentialStep)
	wantArgs := []string{"-c", `exec fugaro image git-credential --entry "$$ENTRY" --build-id "$$BUILD_ID" --provider "$$PROVIDER" --repos "$$CATALOG_REPOS" --ttl-seconds 3600 --out /creds/git-credentials`}
	if !slices.Equal(cred.Args, wantArgs) {
		t.Fatalf("the credential step runs %q, want %q: the entry mode is the one way this build is handed a credential", cred.Args, wantArgs)
	}
	wantEnv := []string{
		"PROVIDER=bitbucket",
		"ENTRY=${_ENTRY}",
		"CATALOG_REPOS=${_CATALOG_REPOS}",
		"FUGARO_CREDENTIALS_SERVICE_URL=${_CREDENTIALS_SERVICE_URL}",
		"BUILD_ID=$BUILD_ID",
	}
	if !slices.Equal(cred.Env, wantEnv) {
		t.Fatalf("the credential step's env = %v, want %v", cred.Env, wantEnv)
	}
	// Every value the step reads about itself is its own environment's and not
	// its argv's, so a repository name or an entry name a config left in a list
	// is data and never shell next to the credential.
	if got := b.Substitutions["_ENTRY"]; got != s.Entry {
		t.Fatalf("_ENTRY = %q, want %q", got, s.Entry)
	}
	if got := b.Substitutions["_CATALOG_REPOS"]; got != "acme/app" {
		t.Fatalf("_CATALOG_REPOS = %q, want %q", got, "acme/app")
	}
	// The repository name the request names is the one the bake plan ordered, in
	// the service's key space (task.CanonicalRepo): the image map keys the entry's
	// repositories by it, and a name in another case is a repository the route
	// refuses by name.
	s.RepoURL = "https://bitbucket.org/Acme/App.git"
	b, err = CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Substitutions["_CATALOG_REPOS"]; got != "acme/app" {
		t.Fatalf("_CATALOG_REPOS = %q for the repository URL %s, want the canonical name", got, s.RepoURL)
	}
	// The two substitutions the mount needed are gone with it: a request that
	// still declared them would be a substitution no step references, which Cloud
	// Build refuses, and a value nothing reads.
	for _, name := range []string{"_GIT_USER", "_WORKFLOW"} {
		if v, ok := b.Substitutions[name]; ok {
			t.Fatalf("the request still declares %s = %q, want neither: the credential step is the only step that read either", name, v)
		}
	}
	// Nothing the request carries is a secret's address: the version name the
	// mount resolved and the provider secret's variable are both gone.
	for _, st := range b.Steps {
		joined := strings.Join(st.Args, " ") + " " + strings.Join(st.Env, " ")
		for _, gone := range []string{"projects/proj-1234/secrets/", gitTokenEnv, gitTokenEnv + "=", githubAppKeyEnv} {
			if strings.Contains(joined, gone) {
				t.Fatalf("step %s still carries %q, want a build whose credential is the service's answer only", st.Id, joined)
			}
		}
	}
	// The GitHub half is the same step with the App's id in place of nothing: the
	// id is not a secret (the service's bundle holds the key and never the id),
	// and it is what the mint needs of this step.
	s.GitProvider = gitprov.KindGitHub
	s.RepoURL = "https://github.com/acme/app.git"
	s.GitUser = ""
	s.GitHubAppID = "12345"
	b, err = CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	if b.AvailableSecrets != nil {
		t.Fatalf("the GitHub request names the secrets %+v, want none", b.AvailableSecrets.SecretManager)
	}
	cred = step(t, b, credentialStep)
	if !slices.Contains(cred.Env, "GITHUB_APP_ID=${_GITHUB_APP_ID}") {
		t.Fatalf("the GitHub credential step's env = %v, want the App's id among it: the service holds the key and the mint is made as the App this step names", cred.Env)
	}
	if got := b.Substitutions["_GITHUB_APP_ID"]; got != "12345" {
		t.Fatalf("_GITHUB_APP_ID = %q, want the App's id", got)
	}
	if _, ok := b.Substitutions["_GIT_USER"]; ok {
		t.Fatalf("the GitHub request declares _GIT_USER: %v", b.Substitutions)
	}
}

// TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv is PIC4's SEC property
// (D1150), and the name is that task's own pin — the plan and
// docs_project_image_catalog_test.go both read it. What it asserts is the
// stronger fact D2311 leaves in its place (B9): an entry's build is handed no
// secret at all, and since IS8 there is no secret of the request to leave
// out: the credential step fetches the entry's git credential from the
// installation's service (D3330), so the build step's secretEnv is empty,
// empty, and the loop the step reads it with passes docker no --secret — so an
// entry that mounts one has nothing to mount, whatever its definition says. A
// secret is still never an ENV, a build argument, a layer, a step's env: or a
// log line, which is what the property was about: it is now never handed over
// at all.
func TestCatalogBuildMountsItsSecretAsAFileNeverAnEnv(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	// IS8 took the last secret out of this request, so what the property asserts
	// is now the whole of it: the request names no secret and no step is handed
	// one. The credential the clone step reads is the entry route's answer, which
	// the credential step writes into the volume the clone step mounts.
	if b.AvailableSecrets != nil {
		t.Fatalf("availableSecrets = %+v, want none: a catalog build mounts no secret (IS8, D3330)", b.AvailableSecrets.SecretManager)
	}
	for _, st := range b.Steps {
		if len(st.SecretEnv) > 0 {
			t.Fatalf("step %s is handed the secrets %v: a catalog build mounts none (B9, D2311)", st.Id, st.SecretEnv)
		}
	}
	build := step(t, b, "build")
	if len(build.SecretEnv) != 0 {
		t.Fatalf("the build step is handed the secrets %v: a catalog build mounts none (B9, D2311), and the clone step's checkout is what the entry works on", build.SecretEnv)
	}
	if got := b.Substitutions["_SECRET_ENVS"]; got != "" {
		t.Fatalf("_SECRET_ENVS = %q, want empty: the loop the build step reads it with would hand docker a --secret for every name in it", got)
	}
	script := strings.Join(build.Args, "\n")
	// B8's half of the same fact: the credential the template's build step used
	// to mount is the checkout the clone step exports, so no argument of this
	// request mounts the credential file either. The trap that removes it stays
	// — it is the cleanup of a path no step of this request writes.
	if strings.Contains(script, `--secret "id=git-credentials`) {
		t.Fatalf("the build step still mounts the credential file as a build secret: %q", script)
	}
	// The loop the step reads _SECRET_ENVS with is the one place a secret name
	// would reach docker as a build secret, and it is left with an empty list:
	// the loop's own text stays, its file-private write stays, and neither an
	// env: nor a build argument can carry a name, because there is none.
	if !strings.Contains(script, "umask 077") {
		t.Fatalf("the secret file the loop writes is not private: %q", script)
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
// record under builds/<slug>/, the prefix the daily check already reads, and
// the record holds what the build itself measured: the copied object's generation, the sha256 of the
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
		t.Fatalf("the record key %q is outside the repository's own prefix", key)
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
		"CONTEXT_COMMIT=", "CONTEXT_SHA256=",
		"TEMPLATE_SALT=" + s.TemplateSalt,
		"BASE=" + s.Base, "TAG=${_CATALOG_TAG}",
	}
	if env := rec.Env; !slices.Equal(env, want) {
		t.Fatalf("the record step's env = %v, want %v", env, want)
	}
	script := strings.Join(rec.Args, "\n")
	// Every field imagecheck.CatalogRecordState reads, and the two extra a
	// human wants: the image, its content tag and the build that wrote it.
	for _, field := range []string{
		`"version":`, `"entry":"`, `"source_commit":"`, `"context_commit":"`, `"context_sha256":"`, `"definition_generation":`,
		`"definition_sha256":"`, `"template_salt":"`, `"base_ref":"`, `"base_digest":"`, `"image":"`,
		`"content_tag":"`, `"image_digest":"`, `"build_id":"`, `"built_at":"`,
	} {
		if !strings.Contains(script, field) {
			t.Fatalf("the record does not carry %s: %q", field, script)
		}
	}
	for _, value := range []string{"$$SHA256", "$$GENERATION", "$$COMMIT", "$$TEMPLATE_SALT", "/workspace/out/image-digest", "/workspace/base-digest"} {
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
		// A catalog image may be built FROM any base (D1151), so its smoke
		// is the only thing that can assert the runtime contract there: a
		// spec asking for no tools is refused, whatever shape the ask takes.
		"no tools key":  func(s *CatalogBuildSpec) { s.Selftest = []byte(`{"skip_verify":true}`) },
		"empty tools":   func(s *CatalogBuildSpec) { s.Selftest = []byte(`{"skip_verify":true,"tools":""}`) },
		"unknown tools": func(s *CatalogBuildSpec) { s.Selftest = []byte(`{"skip_verify":true,"tools":"none"}`) },
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
	// The tiers that cover the contract's critical rows are accepted: the
	// derived smoke's own, and every row the base image's CI smoke runs.
	for _, tools := range []string{"critical", "all"} {
		ok := s
		ok.Selftest = []byte(`{"skip_verify":true,"tools":"` + tools + `"}`)
		if _, err := CatalogBuildRequest("proj-1234", ok); err != nil {
			t.Errorf("tools %q: %v", tools, err)
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

// TestCatalogBuildRunsAsTheBuildIdentity is B6's own claim, and the reason the
// project identity exists (D2301): the account a catalog build runs as is the
// PROJECT's build identity — one account for the whole installation, created
// by the same apply that turns the credentials service on (B5) — and never the
// repository's build account the spec carried until this task.
//
// A repository's build account is derived from the repository and dies with
// the repository's Terraform root, and M2 retires every root, while the entries
// of that repository and the nightly builds of them stay: an entry's build must
// run as an account that outlives every repository the entry names. The reach
// the build needs for its own push — the writer and the tag mover on the
// installation's one shared registry — moves with the account, into the
// installation module (build_identity.tf), so the account a request names and
// the account the module grants those roles to are the same one.
//
// The red step of this task was the fixture: with catalogBuildSpec naming the
// identity, check and checkPrebuilt — which still required
// BuildServiceAccountID(s.Slug) — refused the request with "the build service
// account ... is not the repository's build account". The refusals at the end
// are what the check says now, and they are why the identity is not merely
// accepted but required: a build submitted as any other account would run
// somewhere the installation's registry grants do not reach.
func TestCatalogBuildRunsAsTheBuildIdentity(t *testing.T) {
	const project = "proj-1234"
	identity := BuildIdentityEmail(project)
	// The identity is the installation module's own literal (B5's
	// BuildIdentityAccountID) and this package's copy of it, which cannot
	// import internal/infra: internal/infra/build_identity.go holds the other
	// half, and internal/infra's TestProjectHasOneBuildIdentity reads the
	// module's account_id and pins the two literals together.
	if want := BuildIdentityAccountID + "@" + project + ".iam.gserviceaccount.com"; identity != want {
		t.Fatalf("BuildIdentityEmail(%q) = %q, want %q", project, identity, want)
	}

	for name, spec := range map[string]CatalogBuildSpec{
		"a build":          catalogBuildSpec(t),
		"a context build":  catalogContextSpec(t),
		"a smoke-only run": catalogPrebuiltSpec(t),
	} {
		if spec.ServiceAccount != identity {
			t.Fatalf("%s: the spec names %q as the account it runs as, want the project's build identity %q", name, spec.ServiceAccount, identity)
		}
		b, err := CatalogBuildRequest(project, spec)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := "projects/" + project + "/serviceAccounts/" + identity; b.ServiceAccount != want {
			t.Errorf("%s: the request runs as %q, want %q", name, b.ServiceAccount, want)
		}
		// The identity of another project is a different account, and it is
		// not granted anything in this one: a build submitted in a project
		// runs as that project's identity or not at all.
		if _, err := CatalogBuildRequest("proj-5678", spec); err == nil || !strings.Contains(err.Error(), "the project's build identity") {
			t.Errorf("%s: submitted in another project, the spec was not refused as another project's identity (err = %v)", name, err)
		}
	}

	// Everything else a caller could name is refused, each for its own good
	// reason: the repository's own build account is the value this task
	// replaced (it dies with the repository's root), another repository's is
	// an account of a repository this build has nothing to do with, a job's
	// is an account Cloud Run runs, another project's identity holds nothing
	// here, and an empty field is no account at all.
	slug := catalogSlug(t)
	for _, tc := range []struct{ what, sa, want string }{
		{"the repository's own build account", BuildServiceAccountID(slug) + "@" + project + ".iam.gserviceaccount.com", "is not the project's build identity"},
		{"another repository's build account", BuildServiceAccountID(slug+"x") + "@" + project + ".iam.gserviceaccount.com", "is not the project's build identity"},
		{"a job account", ServiceAccountID(slug, "web") + "@" + project + ".iam.gserviceaccount.com", "is not the project's build identity"},
		{"the identity of another project", BuildIdentityEmail("proj-5678"), "is not the project's build identity"},
		{"no account at all", "", "has no build identity"},
	} {
		for name, spec := range map[string]CatalogBuildSpec{
			"a build":          catalogBuildSpec(t),
			"a smoke-only run": catalogPrebuiltSpec(t),
		} {
			bad := spec
			bad.ServiceAccount = tc.sa
			if _, err := CatalogBuildRequest(project, bad); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s run as %s: err = %v, want a refusal saying %q", name, tc.what, err, tc.want)
			}
		}
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

// TestCatalogReadConditionCoversEveryCopy: the build's read grant is
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
	// The credential step a catalog build reuses reads the address the service
	// is served on (IS8, D3330), so this request carries it; it reads no
	// workflow, and it is handed no secret of the entry's (D2311, B9).
	if got := b.Substitutions["_CREDENTIALS_SERVICE_URL"]; got != s.CredentialsServiceURL {
		t.Fatalf("the catalog build's _CREDENTIALS_SERVICE_URL is %q, want %q, and the credential step reads it: %v", got, s.CredentialsServiceURL, b.Substitutions)
	}
	if got, ok := b.Substitutions["_WORKFLOW"]; ok {
		t.Fatalf("the catalog build sends _WORKFLOW = %q, want no such substitution: the credential step fetches from the service and reads no workflow (IS8, D3330)", got)
	}
	if got, ok := b.Substitutions["_SECRET_ENVS"]; !ok || got != "" {
		t.Fatalf("the catalog build's _SECRET_ENVS is %q (present=%v), want the empty value it always had: an entry's build is handed no secret of the entry's (D2311, B9)", got, ok)
	}
	// Every substitution it does send is one a step of the request references:
	// Cloud Build refuses a request whose substitution no step uses.
	subs := b.Substitutions
	b.Substitutions = nil
	body, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for k := range subs {
		if !strings.Contains(string(body), "${"+k+"}") {
			t.Errorf("substitution %s is sent but not referenced, which Cloud Build rejects", k)
		}
	}
}

// TestCatalogBuildStepsRunTheScanBeforePromote: the image a catalog build
// pushed is scanned for a credential of the build's before anything would
// promote it (B3, R2, F19). The scan is images/scan.sh --secrets — the scan CI
// runs on the same base and derived images — and it is carried in the request,
// base64, because a catalog build reads no checkout to run it from.
//
// It stands between the smoke and the promote, and it is the last thing that
// can refuse the image: promote, the content tag and the record all work on the
// digest the build step wrote, so an image that leaks is a build that fails
// with the registry untouched.
func TestCatalogBuildStepsRunTheScanBeforePromote(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	ids := stepIDs(b)
	scan, promote := slices.Index(ids, catalogScanStepID), slices.Index(ids, "promote")
	if scan < 0 || promote < 0 || scan > promote {
		t.Fatalf("the scan step is at %d and promote at %d in %v", scan, promote, ids)
	}
	if smoke := slices.Index(ids, "smoke"); smoke >= 0 && scan < smoke {
		t.Errorf("the scan runs before the smoke it is scanned over: %v", ids)
	}
	// The scan is the script and the rules the repository's own scan runs, byte
	// for byte, and not a second copy of the checks that could drift from it.
	// Each is over the limit on one substitution's value, so each travels as
	// several, joined by the step before it decodes them.
	for _, f := range []struct {
		substitution string
		data         []byte
	}{
		{"_SCAN_B64", images.ScanScript},
		{"_TRIVY_SECRET_B64", images.TrivySecretConfig},
	} {
		chunks := scanChunks(t, b.Substitutions, f.substitution)
		if len(chunks) < 2 {
			t.Errorf("%s travels in %d value(s), and the file is over the limit on one", f.substitution, len(chunks))
		}
		joined, want := strings.Join(chunks, ""), base64.StdEncoding.EncodeToString(f.data)
		if joined != want {
			t.Errorf("%s joins to %d bytes of base64, and the file's own encode is %d", f.substitution, len(joined), len(want))
		}
		decoded, err := base64.StdEncoding.DecodeString(joined)
		if err != nil || string(decoded) != string(f.data) {
			t.Errorf("%s does not decode to the file the binary carries: %v", f.substitution, err)
		}
	}
	scanStep := step(t, b, catalogScanStepID)
	// The image it scans is the request's own, through the substitution the
	// build step pushed it with, and never a value the script composes.
	if !slices.Contains(scanStep.Env, "IMAGE=${_IMAGE}") {
		t.Errorf("the scan step does not name the image it scans: %v", scanStep.Env)
	}
	script := strings.Join(scanStep.Args, "\n")
	for _, want := range []string{"--secrets", `"$$IMAGE@$$digest"`, "image-digest", "scan.sh", "trivy-secret.yaml"} {
		if !strings.Contains(script, want) {
			t.Errorf("the scan step's script lacks %q: %s", want, script)
		}
	}
	// --no-smoke leaves the scan in place: the flag leaves the smoke out of a
	// build someone is debugging, and an image nobody scanned is not what it
	// asks for.
	noSmoke := s
	noSmoke.NoSmoke = true
	nb, err := CatalogBuildRequest("proj-1234", noSmoke)
	if err != nil {
		t.Fatal(err)
	}
	if ids := stepIDs(nb); !slices.Contains(ids, catalogScanStepID) || slices.Contains(ids, "smoke") {
		t.Errorf("a --no-smoke build's steps are %v", ids)
	}
	// A smoke-only run builds nothing, so it has no image of its own to scan.
	pb, err := CatalogBuildRequest("proj-1234", catalogPrebuiltSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(stepIDs(pb), catalogScanStepID) {
		t.Errorf("the prebuilt run scans an image it did not build: %v", stepIDs(pb))
	}

	// The step is run, not only read: the script writes the embedded scan.sh
	// out of the request and runs it over a faked docker, so what the subtests
	// below assert is what the build does.
	t.Run("a clean image is scanned and passes", func(t *testing.T) {
		got, err := runCatalogScan(t, scanStep, b.Substitutions, scanFixture{
			env: []string{"PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8"},
			files: map[string]string{
				"etc/os-release":  "ID=debian\n",
				"src/.git/config": "[remote \"origin\"]\n\turl = https://github.com/owner/repo.git\n",
			},
		}, nil)
		if err != nil {
			t.Fatalf("the scan refused a clean image: %v\n%s", err, got.out)
		}
		if call := got.call("run"); !strings.Contains(call, "aquasec/trivy") {
			t.Errorf("the step ran no secret scan: %q", call)
		}
		if !strings.Contains(got.out, "scanned "+s.Image+"@"+catalogScanDigest) {
			t.Errorf("the step did not report what it scanned:\n%s", got.out)
		}
	})

	t.Run("an image carrying a credential fails the build", func(t *testing.T) {
		// The token is in the image config's environment: the step fails the
		// build with the scan's own message, so the log says which variable it
		// was.
		got, err := runCatalogScan(t, scanStep, b.Substitutions, scanFixture{
			env:   []string{"BITBUCKET_TOKEN=fixture"},
			files: map[string]string{"etc/os-release": "ID=debian\n"},
		}, nil)
		if err == nil {
			t.Fatalf("the scan passed an image whose config sets a credential:\n%s", got.out)
		}
		if !strings.Contains(got.out, "BITBUCKET_TOKEN") {
			t.Errorf("the failure does not name the variable:\n%s", got.out)
		}
	})

	t.Run("a finding trivy reports fails the build", func(t *testing.T) {
		// The layer scan is a container of its own: its exit code is the
		// step's, so a finding Trivy reports refuses the image without this
		// step having to read its output.
		got, err := runCatalogScan(t, scanStep, b.Substitutions, scanFixture{
			env:   []string{"PATH=/usr/bin"},
			files: map[string]string{"root/.netrc": "machine github.com login fugaro password fixture\n"},
		}, map[string]string{"FAKE_DOCKER_RUN_EXIT": "1"})
		if err == nil {
			t.Fatalf("the scan passed a finding Trivy reported:\n%s", got.out)
		}
	})
}

// scanChunks returns the values a request carries one of the leak scan's files
// in, in order: <prefix>_1, <prefix>_2, ... — the names the step's own env:
// declares, spelled here rather than through the code's own helper, so a
// request that names its values something else fails the test that reads them.
func scanChunks(t *testing.T, subs map[string]string, prefix string) []string {
	t.Helper()
	var chunks []string
	for i := 1; ; i++ {
		value, ok := subs[prefix+"_"+strconv.Itoa(i)]
		if !ok {
			if i == 1 {
				t.Fatalf("the request carries no %s_1: %v", prefix, subs)
			}
			return chunks
		}
		chunks = append(chunks, value)
	}
}

// TestCatalogBuildSubstitutionsStayUnderTheCloudBuildLimit: Cloud Build limits
// the length of one substitution's value and its validator refuses a request
// that breaks the limit before the build is created. Nothing here submits a
// build, so a request over the limit would not fail this package's tests — it
// would fail the first real catalog build, with the scan never running and no
// entry ever built — which is why the limit is asserted on the requests
// instead. The leak scan's two files are the only values big enough to reach
// it (catalogScanFile), so this is what catches a payload that outgrew a single
// substitution again.
func TestCatalogBuildSubstitutionsStayUnderTheCloudBuildLimit(t *testing.T) {
	s := catalogBuildSpec(t)
	build, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	prebuilt, err := CatalogBuildRequest("proj-1234", catalogPrebuiltSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []struct {
		what string
		b    *cloudbuild.Build
	}{{"the catalog build", build}, {"the prebuilt run", prebuilt}} {
		if len(req.b.Substitutions) == 0 {
			t.Fatalf("%s carries no substitutions", req.what)
		}
		for name, value := range req.b.Substitutions {
			if len(value) > catalogSubstitutionLimit {
				t.Errorf("%s carries %s of %d bytes, and a substitution value may be %d",
					req.what, name, len(value), catalogSubstitutionLimit)
			}
		}
	}
}

// scanFixture is the image the fake docker of runCatalogScan answers for: the
// environment its config sets, and the files its export holds.
type scanFixture struct {
	env   []string
	files map[string]string
}

// catalogScanDigest is the digest the build step leaves for the scan to read:
// what the scan runs on is that digest and not the tag it was pushed with.
var catalogScanDigest = "sha256:" + strings.Repeat("d", 64)

// catalogScanRun is one run of the scan step's script: the docker calls the
// step made and its combined output.
type catalogScanRun struct {
	calls []string
	out   string
}

// call returns the arguments of the step's first docker call whose subcommand
// is sub, or "".
func (r catalogScanRun) call(sub string) string {
	for _, c := range r.calls {
		if strings.HasPrefix(c, sub+" ") {
			return c
		}
	}
	return ""
}

// runCatalogScan runs the scan step's script the way Cloud Build would: the
// step's own env: with its substitution placeholders resolved, a docker faked
// on PATH that answers what the fixture holds, a workspace of the test's own
// and the digest the build step would have left there. The script writes the
// request's images/scan.sh and images/trivy-secret.yaml out and runs the
// script, so the real scan is what runs here.
func runCatalogScan(t *testing.T, st *cloudbuild.BuildStep, subs map[string]string, f scanFixture, answers map[string]string) (catalogScanRun, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("the scan step runs bash, which this machine has not: %v", err)
	}
	if err := exec.Command("sh", "-c", "printf '' | base64 -d").Run(); err != nil {
		t.Skipf("the step decodes its inputs with base64 -d, which this machine's base64 has not: %v", err)
	}
	dir := t.TempDir()
	bin, out, scan := filepath.Join(dir, "bin"), filepath.Join(dir, "out"), filepath.Join(dir, "scan")
	for _, d := range []string{bin, out, scan} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "image-digest"), []byte(catalogScanDigest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(dir, "rootfs.tar")
	if err := writeScanRootfs(rootfs, f.files); err != nil {
		t.Fatal(err)
	}
	// The layers the step's scan reads with docker save: one holding what the
	// fixture ends with, which is where a file in the image as it ends came
	// from. The layer-only case — a config a later layer deleted, which is the
	// one the scan cannot find any other way — is images/scan_test.go's.
	save := filepath.Join(dir, "save.tar")
	if err := writeScanSave(save, f.files); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, []byte(strings.Join(f.env, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The fake docker answers the calls the scan makes: the container it
	// creates, the filesystem it exports and the file it copies out of it.
	// Everything else — the trivy container — exits what the test states.
	fake := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_DOCKER_CALLS"
case "$1" in
  create) echo fugaro-fixture-container ;;
  export) cat "$FAKE_DOCKER_ROOTFS" ;;
  inspect)
    case "$*" in
      *RootFS.Layers*) echo 1 ;;   # the save below holds exactly one layer
      *) cat "$FAKE_DOCKER_CONFIG" ;;
    esac ;;
  cp) member=${2#*:}; tar -xOf "$FAKE_DOCKER_ROOTFS" "${member#/}" > "${3:?}" ;;
  save) cat "$FAKE_DOCKER_SAVE" > "${3:?}" ;;
  run) exit "${FAKE_DOCKER_RUN_EXIT:-0}" ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	env := []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"FAKE_DOCKER_CALLS=" + calls, "FAKE_DOCKER_ROOTFS=" + rootfs, "FAKE_DOCKER_CONFIG=" + config,
		"FAKE_DOCKER_SAVE=" + save,
		"OUT=" + out, "SCANDIR=" + scan, "TRIVY_CACHE_DIR=" + filepath.Join(dir, "trivy"),
	}
	for _, e := range st.Env {
		switch {
		case strings.HasPrefix(e, "OUT="), strings.HasPrefix(e, "SCANDIR="), strings.HasPrefix(e, "TRIVY_CACHE_DIR="):
			continue
		}
		env = append(env, os.Expand(e, func(k string) string { return subs[k] }))
	}
	for k, v := range answers {
		env = append(env, k+"="+v)
	}
	script := strings.ReplaceAll(st.Args[len(st.Args)-1], "$$", "$")
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = env
	data, err := cmd.CombinedOutput()
	run := catalogScanRun{out: string(data)}
	if raw, err := os.ReadFile(calls); err == nil {
		run.calls = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	return run, err
}

// writeScanRootfs writes the fixture's files as the tar an image's export
// holds, so the fake docker can stream it and copy out of it.
func writeScanRootfs(path string, files map[string]string) error {
	tarred, err := scanFilesTar(files)
	if err != nil {
		return err
	}
	return os.WriteFile(path, tarred.Bytes(), 0o644)
}

// writeScanSave writes the fixture's files the way `docker save` hands them
// over, as one layer of them: the scan walks a save's layers for a
// .git/config, which no other read of the image can show it. The layer is a
// compressed tar -- what a daemon holds a pulled image's layer as, and a
// stream tar refuses to read from stdin -- inside an uncompressed outer tar,
// beside the manifest a save also carries, so the step reads the members that
// are layers and skips the one that is not. The fake docker above answers with
// one layer to match, which is what the scan holds the save to.
func writeScanSave(path string, files map[string]string) error {
	layer, err := scanFilesTar(files)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(layer.Bytes()); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, member := range []struct {
		name string
		body []byte
	}{
		{"blobs/sha256/" + strings.Repeat("e", 64), compressed.Bytes()},
		{"manifest.json", []byte(`{"schemaVersion":2}`)},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: member.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(member.body)),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(member.body); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// scanFilesTar is a tar of the fixture's files, under their full paths: the
// scan reads the names in a tar and the text of the files it picks out of one,
// and the directory entries of a real filesystem play no part in that.
func scanFilesTar(files map[string]string) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

// TestCatalogBuildHasACheckoutStepForAContextEntry: a catalog build gets one
// step the derived build has no use for — the clone step, which checks the
// entry's repository out at the commit the copy pinned, between the step that
// reads the copied Dockerfile and the step that pins it (D1396), and hands it
// to the entry's build as an image of the worker's own (B8). It clones by commit
// and never by branch: the branch head at build time is the tree the copy pinned
// only by accident (D1394).
//
// An entry that names no context gets the same step, at the commit the copied
// definition was taken from: its Dockerfile clones with the credential itself
// today, which is the hole B8 closes, and the checkout it is handed is what it
// copies instead.
func TestCatalogBuildHasACheckoutStepForAContextEntry(t *testing.T) {
	s := catalogContextSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"prep", credentialStep, catalogDockerfileStepID, catalogCloneStepID, catalogPinStepID,
		"build", "smoke", catalogScanStepID, "promote", catalogTagStepID, catalogRecordStepID, "untag",
	}
	if ids := stepIDs(b); !slices.Equal(ids, want) {
		t.Fatalf("steps = %v, want %v", ids, want)
	}
	clone := step(t, b, catalogCloneStepID)
	if clone.Entrypoint != "bash" || len(clone.SecretEnv) != 0 {
		t.Fatalf("the clone step = %+v", clone)
	}
	// The commit reaches the generated build as one env value, and the branch
	// is not in this step at all — not as an env value, not as a substitution
	// and not as a piece of its script: the branch head at build time is the
	// tree the copy pinned only by accident (D1394).
	if env := clone.Env; !slices.Equal(env, []string{
		"DOCKER_BUILDKIT=1", "FUGARO_BASE=${_FUGARO_BASE}", "REPO_URL=${_REPO_URL}",
		"COMMIT=" + s.ContextCommit, "REPO_HOST=bitbucket.org", "GITHUB_API=https://api.github.com",
		"CONTEXT_DIR=${_CONTEXT_DIR}", "BUILD_CWD=" + buildStepCwd(step(t, b, "build")),
		"CREDENTIALS_SERVICE_URL=${_CREDENTIALS_SERVICE_URL}",
	}) {
		t.Fatalf("the clone step's env = %v", env)
	}
	if script := strings.Join(clone.Args, "\n"); strings.Contains(script, "BASE_BRANCH") || strings.Contains(script, "--branch") {
		t.Fatalf("the clone step names a branch: %q", script)
	}
	// CONTEXT_DIR is the substitution the build step builds from, so the step
	// resolves the directory docker will open and not a second reading of the
	// entry's context — from BUILD_CWD, the directory of the build step that
	// hands docker the value, because that is where docker resolves it.
	if !strings.Contains(strings.Join(clone.Args, "\n"), `cd -- "$$BUILD_CWD"`) {
		t.Error("the clone step does not resolve the context where docker resolves it")
	}
	plain := catalogBuildSpec(t)
	st := catalogCloneStepOf(t, plain)
	if !slices.Contains(st.Env, "COMMIT="+plain.Definition.SourceCommit) {
		t.Fatalf("the clone step of a context-less entry checks out %v", st.Env)
	}
	if strings.Contains(strings.Join(st.Args, "\n"), "outside the checkout") {
		t.Error("a context-less entry's clone step guards a context it does not have")
	}
}

// TestCatalogContextEntryBuildsTheCopiedDockerfile: the copy stays the
// Dockerfile the build runs (D1404). The build step reads
// /workspace/context/Dockerfile — the object catalog-pin verifies with
// sha256sum — and its context argument is the checkout instead. A build
// whose Dockerfile was the checkout's own, or whose pin named a file the
// build never read, fails here.
func TestCatalogContextEntryBuildsTheCopiedDockerfile(t *testing.T) {
	s := catalogContextSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	build := step(t, b, "build")
	script := strings.Join(build.Args, "\n")
	if !strings.Contains(script, "--file /workspace/context/Dockerfile") {
		t.Fatalf("the build step does not run the copied Dockerfile: %q", script)
	}
	if !strings.Contains(script, `--tag "$$candidate" "$$CONTEXT_DIR"`) {
		t.Fatalf("the build step's context is not the checkout: %q", script)
	}
	if !slices.Contains(build.Env, "CONTEXT_DIR=${_CONTEXT_DIR}") {
		t.Fatalf("the build step does not read the context root through its env: %v", build.Env)
	}
	if got := b.Substitutions["_CONTEXT_DIR"]; got != "/workspace/src" {
		t.Fatalf("_CONTEXT_DIR = %q, want the checkout", got)
	}
	pin := strings.Join(step(t, b, catalogPinStepID).Args, "\n")
	if !strings.Contains(pin, "sha256sum /workspace/context/Dockerfile") {
		t.Fatalf("the pin step verifies a file the build does not read: %q", pin)
	}
	if clone := catalogCloneStepOf(t, s); strings.Contains(strings.Join(clone.Args, "\n"), "/workspace/context") {
		t.Fatal("the clone step's own build reads the directory the copied Dockerfile lives in")
	}
}

// TestCatalogBuildWithoutAContextBuildsTheCopiedDefinition: an entry that names
// no context builds the directory the derived path already builds — the copied
// definition's own — and the derived build, which runs the same template, states
// the same one (F23's regression grammar: the build step and what it builds are
// unchanged, and what changed is where its Dockerfile gets the repository's
// files from). The clone step is in this request too, at the commit the copied
// definition was taken from, and it writes the checkout the entry's build copies
// as an image — it is not the directory this build step builds from.
func TestCatalogBuildWithoutAContextBuildsTheCopiedDefinition(t *testing.T) {
	s := catalogBuildSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	// The substitution the template's build step reads keeps its default, and
	// the derived build states the same one.
	if got := b.Substitutions["_CONTEXT_DIR"]; got != contextDirDefault {
		t.Fatalf("_CONTEXT_DIR = %q, want %q", got, contextDirDefault)
	}
	derived, err := BuildRequest("proj-1234", buildSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := derived.Substitutions["_CONTEXT_DIR"]; got != contextDirDefault {
		t.Fatalf("the derived build's _CONTEXT_DIR = %q, want %q", got, contextDirDefault)
	}
	// The Dockerfile is the copied one, and the positional context is the
	// directory it lives in: the checkout the clone step wrote is an image and
	// never this build's context argument.
	script := strings.Join(step(t, b, "build").Args, "\n")
	if !strings.Contains(script, "--file /workspace/context/Dockerfile") || !strings.Contains(script, `--tag "$$candidate" "$$CONTEXT_DIR"`) {
		t.Fatalf("the build step does not build the copied definition: %q", script)
	}
	// The derived build is handed no checkout at all: it clones in its own
	// Dockerfile, with the credential, which is what a catalog entry may no
	// longer do (B8 is the catalog path's).
	if derivedBuild := strings.Join(step(t, derived, "build").Args, "\n"); strings.Contains(derivedBuild, catalogCloneImage) {
		t.Fatalf("the derived build is handed a checkout it does not have: %q", derivedBuild)
	}
}

// TestCatalogContextRootIsTheNamedSubdirectory: a context naming a
// subdirectory makes that directory the context root (D1406), so a COPY of a
// file under it resolves the way the Dockerfile wrote it, and `.` is the
// checkout itself.
func TestCatalogContextRootIsTheNamedSubdirectory(t *testing.T) {
	for _, tc := range []struct{ context, dir string }{
		{".", "/workspace/src"},
		{"ci/", "/workspace/src/ci"},
		{"ci", "/workspace/src/ci"},
		{"ci/java", "/workspace/src/ci/java"},
	} {
		s := catalogContextSpec(t)
		s.Context = tc.context
		b, err := CatalogBuildRequest("proj-1234", s)
		if err != nil {
			t.Fatalf("context %q: %v", tc.context, err)
		}
		if got := b.Substitutions["_CONTEXT_DIR"]; got != tc.dir {
			t.Errorf("context %q: _CONTEXT_DIR = %q, want %q", tc.context, got, tc.dir)
		}
		// The Dockerfile the build reads does not move with the context: it
		// is the copy, whatever the context names.
		if script := strings.Join(step(t, b, "build").Args, "\n"); !strings.Contains(script, "--file /workspace/context/Dockerfile") {
			t.Errorf("context %q: the build step does not run the copied Dockerfile: %q", tc.context, script)
		}
	}
}

// TestCatalogBuildRefusesAContextOutsideTheCheckout: the context is joined
// onto the checkout root and handed to the build step as its context
// argument, so a spec whose context climbs out of the checkout with a `..`
// segment, or that is absolute, is refused before the request is built. The
// reason is not tidiness: the build step mounts the fugaro-creds volume at
// /creds, and a context that resolved there would have a Dockerfile's
// `COPY .` bake the repository's git credential into the image. The rule is
// config's key rule; it is held here too, because a request is built from a
// spec and not from a validated fugaro.yaml.
func TestCatalogBuildRefusesAContextOutsideTheCheckout(t *testing.T) {
	// An empty context is an entry with no context — the all-or-none check
	// reads it before this one — and not an unclean path.
	if msg := contextPathMessage(""); msg != "" {
		t.Errorf("contextPathMessage(%q) = %q, want %q", "", msg, "")
	}
	for _, tc := range []struct {
		context string
		refused bool
	}{
		{".", false},
		{"ci/", false},
		{"ci/java", false},
		// The credential volume is mounted at /creds in the build step, and
		// /workspace/src/../../creds resolves to it.
		{"../../creds", true},
		{"../..", true},
		{"..", true},
		{"ci/../../creds", true},
		{"a/../b", true},
		{"/creds", true},
		{"/", true},
		{"ci//java", true},
		{"ci/./java", true},
		{"ci\n", true},
	} {
		s := catalogContextSpec(t)
		s.Context = tc.context
		b, err := CatalogBuildRequest("proj-1234", s)
		switch {
		case tc.refused && err == nil:
			t.Errorf("the request accepts the context %q and builds from %q", tc.context, b.Substitutions["_CONTEXT_DIR"])
		case tc.refused && !errors.Is(err, ErrBadBuildSpec):
			t.Errorf("the context %q is refused with %v, which is not %v", tc.context, err, ErrBadBuildSpec)
		case tc.refused && !strings.Contains(err.Error(), strconv.Quote(tc.context)):
			t.Errorf("the refusal of the context %q does not name it: %v", tc.context, err)
		case !tc.refused && err != nil:
			t.Errorf("the request refuses the context %q: %v", tc.context, err)
		case !tc.refused:
			// The resolved directory, not the joined string: this is what
			// docker is handed, and what it would read the context from.
			dir := path.Clean(b.Substitutions["_CONTEXT_DIR"])
			if dir != "/workspace/src" && !strings.HasPrefix(dir, "/workspace/src/") {
				t.Errorf("the context %q builds from %q, which resolves outside the checkout", tc.context, dir)
			}
		}
	}
}

// TestCatalogBuildRefusesATemplateSaltThatIsNotAHexDigest: the record step
// composes its JSON with printf and no escaping, so the salt a submitter states
// is constrained by the request rather than written into the record: a quote, a
// newline or anything else that is not the digest this package computes would
// break the record every reader parses. A submitter states a salt or states
// none, and a smoke-only run writes the same record, so it is held to it too.
func TestCatalogBuildRefusesATemplateSaltThatIsNotAHexDigest(t *testing.T) {
	for _, salt := range []string{
		`"`, "x", "c", strings.Repeat("c", 63), strings.Repeat("c", 65),
		"sha256:" + strings.Repeat("c", 64), strings.Repeat("C", 64), "c\n", "c c",
	} {
		s := catalogBuildSpec(t)
		s.TemplateSalt = salt
		_, err := CatalogBuildRequest("proj-1234", s)
		switch {
		case err == nil:
			t.Errorf("the request accepts the template salt %q, which the record step would write into its JSON unescaped", salt)
		case !errors.Is(err, ErrBadBuildSpec):
			t.Errorf("the template salt %q is refused with %v, which is not %v", salt, err, ErrBadBuildSpec)
		case !strings.Contains(err.Error(), strconv.Quote(salt)):
			t.Errorf("the refusal of the template salt %q does not name it: %v", salt, err)
		}
	}
	p := catalogPrebuiltSpec(t)
	p.TemplateSalt = `"`
	if _, err := CatalogBuildRequest("proj-1234", p); !errors.Is(err, ErrBadBuildSpec) || !strings.Contains(err.Error(), "template salt") {
		t.Errorf("the smoke-only run accepts the template salt %q: %v", p.TemplateSalt, err)
	}
	// The salt this package computes is what a submitter states, and both kinds
	// of run accept it.
	for _, s := range []CatalogBuildSpec{catalogBuildSpec(t), catalogPrebuiltSpec(t)} {
		s.TemplateSalt = TemplateSalt("1.2.3")
		if _, err := CatalogBuildRequest("proj-1234", s); err != nil {
			t.Errorf("the request refuses the salt it computed: %v", err)
		}
		s.TemplateSalt = ""
		if _, err := CatalogBuildRequest("proj-1234", s); err != nil {
			t.Errorf("the request refuses a submitter that states no salt: %v", err)
		}
	}
}

// catalogDockerStub is the docker the clone step runs against in the tests
// below: `docker image inspect` answers a digest, and each of the two builds the
// step runs has to write what the entry's build waits for — the tagged image it
// copies the checkout out of, and, for an entry that names a context, the
// `type=local` export into the directory the guard then looks for the context
// in, and where the tree's own "$CHECKOUT" is. The workspace the script's
// /workspace names is in "$WORKSPACE".
//
// `create` and `cp` fail here on purpose: unpacking the tagged image through a
// container would write the daemon's own files of that container's rootfs
// (/.dockerenv, /etc/hosts, /etc/hostname, /etc/resolv.conf, /dev, /proc, /sys)
// into the directory the entry's build is handed as its context, where a `COPY .`
// of its Dockerfile would bake them into the image. The context holds the
// checkout and nothing else (TestCatalogContextStepExportsOnlyTheClonedTree).
const catalogDockerStub = `#!/bin/sh
case "$1" in
image) echo "sha256:1111"; exit 0 ;;
build)
  tagged= exported=
  prev=
  for a in "$@"; do
    case "$a" in
      ` + catalogCloneImage + `) [ "$prev" = --tag ] && tagged=1 ;;
      type=local,dest=*) [ "$prev" = --output ] && exported=${a#type=local,dest=} ;;
    esac
    prev=$a
  done
  if [ -n "$exported" ]; then
    mkdir -p "$exported" || exit 1
    CHECKOUT="$exported" sh -c "$TREE" || exit 1
    exit 0
  fi
  [ -n "$tagged" ] || { echo "the clone's build writes neither the checkout's tag nor an export" >&2; exit 1; }
  exit 0 ;;
create|cp)
  echo "the step unpacks the checkout through a container's own rootfs" >&2
  exit 1 ;;
esac
exit 0
`

// catalogGrantService is the credentials service address the clone step's
// tests run a Bitbucket build against, and catalogCurlStub is the metadata
// server's answer for the build identity's ID token: one clean value.
const catalogGrantService = "https://creds.example"

const catalogCurlStub = `#!/bin/sh
printf 'eyJhbGciOi.eyJhdWQi.c2lnbmF0dXJl'
`

// runCatalogCloneGuard runs the clone step's script with the stub docker and
// returns its output: ws holds the workspace the script's /workspace names,
// with the credential volume the step mounts, the checkout the stubbed clone
// build exports is built from tree — so the guard sees the directory docker
// really handed the build — and the script's substitutions carry context, the
// _CONTEXT_DIR the build step hands docker.
func runCatalogCloneGuard(t *testing.T, st *cloudbuild.BuildStep, ws, tree, context string) ([]byte, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	for _, d := range []string{"creds", "context"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(ws, "bin")
	testutil.WriteFiles(t, bin, map[string]string{"docker": catalogDockerStub})
	script := strings.ReplaceAll(strings.ReplaceAll(st.Args[1], "$$", "$"), "/workspace", ws)
	script = strings.ReplaceAll(script, catalogCloneCredential, filepath.Join(ws, catalogCloneCredential))
	context = strings.ReplaceAll(context, "${WS}", ws)
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "WORKSPACE=" + ws, "TREE=" + tree}
	for _, kv := range st.Env {
		kv = strings.ReplaceAll(strings.ReplaceAll(kv, "${_CONTEXT_DIR}", context), "/workspace", ws)
		env = append(env, strings.ReplaceAll(kv, "${_CREDENTIALS_SERVICE_URL}", catalogGrantService))
		if strings.HasPrefix(kv, "CREDENTIALS_SERVICE_URL=") {
			// A Bitbucket build's step reads the grant its credential step
			// wrote and the build identity's token from the metadata server.
			testutil.WriteFiles(t, bin, map[string]string{"curl": catalogCurlStub})
			testutil.WriteFiles(t, filepath.Join(ws, "creds"), map[string]string{
				"git-credentials": "[url \"" + catalogGrantService + "/v1/bitbucket/proxy/\"]\n\tinsteadOf = https://bitbucket.org/\n",
			})
		}
	}
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// checkCatalogCloneGuard asserts the step's verdict on one context: a refusal
// says why, an acceptance runs the script to the end.
func checkCatalogCloneGuard(t *testing.T, st *cloudbuild.BuildStep, context, tree string, refused bool) {
	t.Helper()
	out, err := runCatalogCloneGuard(t, st, t.TempDir(), tree, context)
	switch {
	case refused && err == nil:
		t.Errorf("the step builds from the context %q:\n%s", context, out)
	case refused && !strings.Contains(string(out), "outside the checkout"):
		t.Errorf("the step refuses the context %q without saying why: %v\n%s", context, err, out)
	case !refused && err != nil:
		t.Errorf("the step refuses the context %q: %v\n%s", context, err, out)
	}
}

// TestCatalogContextStepRefusesAContextThatLeavesTheCheckoutByALink: the
// context reaches docker as the directory its path resolves to — the CLI tars
// what a symlinked root points at, not the link — so a repository at the pinned
// commit whose context is a link to the credential volume would hand the build
// step's /creds to the entry's Dockerfile, and a Dockerfile that only says
// `COPY .` would copy the repository's token into the shared catalog image.
// The step resolves the context the way docker will (a cd follows the link, pwd
// -P says where it landed) and builds only from a directory inside the
// checkout. A link to a directory of the checkout is a context like any other.
func TestCatalogContextStepRefusesAContextThatLeavesTheCheckoutByALink(t *testing.T) {
	s := catalogContextSpec(t)
	s.Context = "ci"
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	st := step(t, b, catalogCloneStepID)
	if len(st.Args) != 2 {
		t.Fatalf("the clone step's args = %q, want one script", st.Args)
	}
	for _, tc := range []struct {
		name    string
		tree    string
		refused bool
	}{
		{
			name: "a directory of the checkout",
			tree: `mkdir -p "$CHECKOUT/ci" "$CHECKOUT/sub"`,
		},
		{
			// The credential volume the step mounts, standing where the
			// build mounts it.
			name:    "a link to the credential volume",
			tree:    `ln -s -- "$WORKSPACE/creds" "$CHECKOUT/ci"`,
			refused: true,
		},
		{
			name:    "a link above the checkout",
			tree:    `ln -s -- "$WORKSPACE" "$CHECKOUT/ci"`,
			refused: true,
		},
		{
			name: "a link to a directory of the checkout",
			tree: `mkdir -p "$CHECKOUT/sub"; ln -s -- "$CHECKOUT/sub" "$CHECKOUT/ci"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The context an entry's own build is handed: contextDir joins
			// the value onto the checkout root.
			checkCatalogCloneGuard(t, st, "${WS}/src/ci", tc.tree, tc.refused)
		})
	}
}

// TestCatalogContextStepExportsOnlyTheClonedTree: the directory the build step
// hands docker as a context entry's context is the checkout and nothing else. It
// is written by the same clone build's own `type=local` export, whose whole
// content is the scratch stage's /out — not by unpacking the tagged image
// through a container, whose rootfs carries the daemon's files of that container
// on top of the image (/.dockerenv, /etc/hosts, /etc/hostname, /etc/resolv.conf,
// /dev, /proc, /sys). Those land in the context, a `COPY .` of the entry's
// Dockerfile bakes them into the shared catalog image, and every job then starts
// in a checkout holding files no repository has and that an agent's `git add -A`
// commits.
func TestCatalogContextStepExportsOnlyTheClonedTree(t *testing.T) {
	s := catalogContextSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	st := step(t, b, catalogCloneStepID)
	script := strings.Join(st.Args, "\n")
	if want := `--output "type=local,dest=` + catalogCloneDest + `"`; !strings.Contains(script, want) {
		t.Errorf("the clone step does not export the checkout with %s:\n%s", want, script)
	}
	for _, verb := range []string{"create", "cp", "save", "import"} {
		if strings.Contains(script, "docker "+verb+" ") {
			t.Errorf("the step runs `docker %s`: a container's rootfs is the image plus the daemon's own files, and none of them is the checkout", verb)
		}
	}
	// What it leaves there, run for real against the stub docker: the tree the
	// clone wrote, with the directory holding nothing else. The context an entry
	// that names "." is handed is the export's own root.
	ws := t.TempDir()
	tree := `mkdir -p "$CHECKOUT/repo/.git" "$CHECKOUT/repo/ci"
printf 'a\n' > "$CHECKOUT/repo/.git/config"
printf 'b\n' > "$CHECKOUT/repo/ci/main.go"`
	if out, err := runCatalogCloneGuard(t, st, ws, tree, "${WS}/src"); err != nil {
		t.Fatalf("the clone step failed: %v\n%s", err, out)
	}
	want := []string{"repo", "repo/.git", "repo/.git/config", "repo/ci", "repo/ci/main.go"}
	if got := dirPaths(t, filepath.Join(ws, "src")); !slices.Equal(got, want) {
		t.Errorf("the exported context holds %v, want the cloned tree and nothing else: %v", got, want)
	}
}

// dirPaths is every path under root, relative to it and sorted, with root
// itself left out: what a directory holds, as a test can compare it.
func dirPaths(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		got = append(got, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	return got
}

// TestCatalogContextStepResolvesTheContextAsTheBuildStepDoes: docker resolves
// the context against the working directory of the step that hands it the
// argument, so the guard must resolve the same value in that same directory.
// Resolved from the checkout instead, a relative context is answered for a
// directory docker never builds — and a link the checkout commits is enough to
// make that reading land back on the checkout, so the guard accepts while
// docker resolves the very same value to the worker's own directory, which
// holds every other tree the build wrote. The request's rules cannot close it:
// check refuses an entry's context whose text holds a ".." segment or a leading
// "/", but this step is the template's and answers for the value any submitter
// hands it, not only for the absolute path this CLI builds today.
func TestCatalogContextStepResolvesTheContextAsTheBuildStepDoes(t *testing.T) {
	s := catalogContextSpec(t)
	s.Context = "ci"
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	st := step(t, b, catalogCloneStepID)
	// The cwd comes from the build step the template holds — the step that
	// runs docker build — and not from a path this step assumes.
	if build := step(t, b, "build"); !slices.Contains(st.Env, "BUILD_CWD="+buildStepCwd(build)) {
		t.Fatalf("the clone step's env does not carry the build step's own directory: %v", st.Env)
	}
	for _, tc := range []struct {
		name    string
		context string
		tree    string
		refused bool
	}{
		{
			name:    "a relative context under the checkout",
			context: "src/ci",
			tree:    `mkdir -p "$CHECKOUT/ci"`,
		},
		{
			name:    "a relative context beside the checkout",
			context: "context",
			tree:    `mkdir -p "$CHECKOUT/context"`,
			refused: true,
		},
		{
			name:    "a relative context a link in the checkout folds back onto the checkout",
			context: "src/..",
			tree:    `mkdir -p "$CHECKOUT/sub"; ln -s -- sub "$CHECKOUT/src"`,
			refused: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkCatalogCloneGuard(t, st, tc.context, tc.tree, tc.refused)
		})
	}
}

// TestBuildStepCwd: the guard resolves the context where docker resolves it,
// which is the directory of the build step in the template — its own dir:, or
// the worker's default when it names none.
func TestBuildStepCwd(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{"", "/workspace"},
		{"src", "/workspace/src"},
		{"/other", "/other"},
	} {
		if got := buildStepCwd(&cloudbuild.BuildStep{Dir: tc.dir}); got != tc.want {
			t.Errorf("buildStepCwd(dir %q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// TestCatalogRecordCarriesTheContextCommitAndHash: the record carries the
// context's own identity in two fields of its own — the commit it was
// checked out at and the digest of its tree — under names of their own.
// source_commit means the commit the copied Dockerfile was taken from, and
// reusing that name for the context would draw a wrong figure in every
// reader that knows the old one (F25, D1400).
func TestCatalogRecordCarriesTheContextCommitAndHash(t *testing.T) {
	s := catalogContextSpec(t)
	b, err := CatalogBuildRequest("proj-1234", s)
	if err != nil {
		t.Fatal(err)
	}
	rec := step(t, b, catalogRecordStepID)
	for _, env := range []string{"CONTEXT_COMMIT=" + s.ContextCommit, "CONTEXT_SHA256=" + s.ContextSHA256} {
		if !slices.Contains(rec.Env, env) {
			t.Fatalf("the record step's env does not carry %s: %v", env, rec.Env)
		}
	}
	script := strings.Join(rec.Args, "\n")
	// The record is one printf with one %s per argument, so a field pairs
	// with its value by position: each key sits in the format, before the
	// argument that fills it, and the three keys and the three values are
	// listed in the same order.
	for _, pair := range [][2]string{
		{`"source_commit"`, `"$$COMMIT"`},
		{`"context_commit"`, `"$$CONTEXT_COMMIT"`},
		{`"context_sha256"`, `"$$CONTEXT_SHA256"`},
	} {
		field, value := strings.Index(script, pair[0]), strings.Index(script, pair[1])
		if field < 0 || value < 0 || field > value {
			t.Fatalf("the record does not pair the field %s with the value %s: %q", pair[0], pair[1], script)
		}
	}
	// A context's values come together or not at all, and each is a shape
	// this step may write into a JSON it composes without escaping.
	for name, mod := range map[string]func(*CatalogBuildSpec){
		"a context with no commit":  func(s *CatalogBuildSpec) { s.ContextCommit = "" },
		"a context with no digest":  func(s *CatalogBuildSpec) { s.ContextSHA256 = "" },
		"a commit with no context":  func(s *CatalogBuildSpec) { s.Context = "" },
		"a digest with no context":  func(s *CatalogBuildSpec) { s.Context = ""; s.ContextCommit = "" },
		"a commit that is a ref":    func(s *CatalogBuildSpec) { s.ContextCommit = "main" },
		"a digest that is not hex":  func(s *CatalogBuildSpec) { s.ContextSHA256 = "sha256:" + strings.Repeat("c", 64) },
		"a digest that is too long": func(s *CatalogBuildSpec) { s.ContextSHA256 = strings.Repeat("c", 65) },
	} {
		bad := s
		mod(&bad)
		if _, err := CatalogBuildRequest("proj-1234", bad); err == nil || !strings.Contains(err.Error(), "invalid Cloud Build request") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// catalogPrebuiltSpec is one smoke-only run of the entry "agent", whose
// source is a prebuilt image (F10/IS11): the request
// internal/cli/image_build_catalog.go submits for such an entry. It names the
// reference the entry's jobs start on and nothing a build has, and it runs as
// the project's build identity like every other catalog run (B6).
func catalogPrebuiltSpec(t *testing.T) CatalogBuildSpec {
	t.Helper()
	slug := catalogSlug(t)
	return CatalogBuildSpec{
		Entry: "agent", Slug: slug,
		Prebuilt:       "docker.io/library/node:22",
		ServiceAccount: BuildIdentityEmail("proj-1234"),
		MachineType:    "E2_HIGHCPU_8",
		Bucket:         "gs://fugaro-runs-proj-1234",
		Selftest:       []byte(`{"tools_only":true,"tools":"critical"}`),
	}
}

// TestPrebuiltEntrySmoked: an entry whose source is a prebuilt image is
// somebody else's image, used as it is, so there is nothing to build and no
// Dockerfile, base or credential to read. Its whole Cloud Build run is the
// smoke of the reference it names and the record of what that smoke resolved
// (F10): before this, image_build_catalog.go passed such an entry over and
// nothing ever asserted that the image a job starts on meets the runtime
// contract, so a job — not a build — was the first thing to find out.
//
// The step the run is made of is run here, so this is a statement about what
// the smoke does and not about what its script says.
func TestPrebuiltEntrySmoked(t *testing.T) {
	spec := catalogPrebuiltSpec(t)
	req, err := CatalogBuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatalf("a prebuilt entry's request: %v", err)
	}
	// Nothing of a build is in the run: no step clones, builds, pushes or
	// tags, which is what the smoke and the record alone say.
	if got, want := stepIDs(req), []string{catalogSmokeStepID, catalogRecordStepID}; !slices.Equal(got, want) {
		t.Fatalf("the prebuilt run's steps are %v, want %v", got, want)
	}
	// The substitutions are only the ones those two steps reference: Cloud
	// Build refuses a substitution the request does not reference.
	want := map[string]string{
		"_IMAGE":        spec.Prebuilt,
		"_SELFTEST_B64": base64.StdEncoding.EncodeToString(spec.Selftest),
		"_CATALOG_TAG":  "",
	}
	if len(req.Substitutions) != len(want) {
		t.Errorf("the prebuilt run's substitutions are %v, want %v", req.Substitutions, want)
	}
	for k, v := range want {
		if req.Substitutions[k] != v {
			t.Errorf("the prebuilt run's %s is %q, want %q", k, req.Substitutions[k], v)
		}
	}
	// It mounts no credential: no step of it reads a repository.
	if req.AvailableSecrets != nil && len(req.AvailableSecrets.SecretManager) != 0 {
		t.Errorf("the prebuilt run carries availableSecrets: %v", req.AvailableSecrets)
	}
	if got, want := req.ServiceAccount, "projects/proj-1234/serviceAccounts/"+spec.ServiceAccount; got != want {
		t.Errorf("the prebuilt run's service account is %s, want %s", got, want)
	}
	if req.Timeout != "3600s" || req.QueueTtl != "3600s" {
		t.Errorf("the prebuilt run's bounds are %s/%s, want 3600s/3600s", req.Timeout, req.QueueTtl)
	}
	if req.Options == nil || req.Options.MachineType != spec.MachineType || req.Options.Logging != "CLOUD_LOGGING_ONLY" {
		t.Errorf("the prebuilt run's options are %+v, want %s and CLOUD_LOGGING_ONLY", req.Options, spec.MachineType)
	}

	smoke, record := req.Steps[0], req.Steps[1]
	if smoke.Name != "gcr.io/cloud-builders/docker" || smoke.Entrypoint != "bash" {
		t.Errorf("the smoke step is %s/%s, want the docker builder's bash", smoke.Name, smoke.Entrypoint)
	}
	if script := smoke.Args[len(smoke.Args)-1]; strings.Contains(script, "docker push") || strings.Contains(script, "candidate-") || strings.Contains(script, ":latest") {
		t.Errorf("the smoke step pushes something: %q", script)
	}
	// The record step is the one a build's run writes, with the entry's own
	// reference and the marker that tells the two apart: the same record, read
	// by the same check and the same doctor, so what was smoked under a
	// reference is readable afterwards.
	if !slices.Contains(record.Env, "IMAGE="+spec.Prebuilt) {
		t.Errorf("the record step's env does not carry the entry's reference: %v", record.Env)
	}
	if !slices.Contains(record.Env, "PREBUILT=1") {
		t.Errorf("the record step's env carries no PREBUILT marker: %v", record.Env)
	}
	buildReq, err := CatalogBuildRequest("proj-1234", catalogBuildSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range buildReq.Steps {
		if st.Id == catalogRecordStepID && slices.Contains(st.Env, "PREBUILT=1") {
			t.Errorf("a build's record step carries the prebuilt marker: %v", st.Env)
		}
	}

	t.Run("a reference a job can start on is smoked and recorded", func(t *testing.T) {
		got, err := runCatalogSmoke(t, smoke, req.Substitutions, map[string]string{
			"FAKE_DOCKER_INSPECT": spec.Prebuilt + "@" + catalogSmokeDigest,
			"FAKE_DOCKER_REPORT":  `{"passed":true,"tools":[{"name":"fugaro","present":true}]}`,
		})
		if err != nil {
			t.Fatalf("the smoke failed although the image passes it: %v\n%s", err, got.out)
		}
		// What the step smoked is bytes, not the tag: the selftest runs on the
		// digest the reference resolved to, and that digest is what the record
		// carries.
		if call := got.call("run"); !strings.Contains(call, spec.Prebuilt+"@"+catalogSmokeDigest) {
			t.Errorf("the selftest ran on %q, want the digest-pinned reference", call)
		}
		if call := got.call("run"); !strings.Contains(call, "--network none") {
			t.Errorf("the selftest ran without --network none: %q", call)
		}
		if got.stdin != string(spec.Selftest) {
			t.Errorf("the selftest read %q on stdin, want the spec's own bytes %q", got.stdin, spec.Selftest)
		}
		if got.digest != catalogSmokeDigest {
			t.Errorf("the step wrote the digest %q to BUILDER_OUTPUT, want %q", got.digest, catalogSmokeDigest)
		}
		if !strings.Contains(got.out, "smoked "+spec.Prebuilt+"@"+catalogSmokeDigest) {
			t.Errorf("the step did not report the reference it smoked:\n%s", got.out)
		}
	})

	t.Run("an image with no digest fails the smoke", func(t *testing.T) {
		// A tag the registry serves nothing pinned for: what a job would start
		// on is not the bytes that were smoked, so this is not a smoke.
		got, err := runCatalogSmoke(t, smoke, req.Substitutions, map[string]string{
			"FAKE_DOCKER_INSPECT":      spec.Prebuilt,
			"FAKE_DOCKER_INSPECT_EXIT": "1",
			"FAKE_DOCKER_REPORT":       `{"passed":true}`,
		})
		if err == nil {
			t.Fatalf("the smoke succeeded although the image carries no digest:\n%s", got.out)
		}
		if !strings.Contains(got.out, "carries no digest") {
			t.Errorf("the failure is not the missing digest:\n%s", got.out)
		}
	})

	t.Run("an inspect that answers no digest fails the smoke", func(t *testing.T) {
		// The guard under the inspect: docker answered, and what it printed is
		// not a digest-pinned reference. The image is then not pinned to the
		// bytes that were pulled — the smoke would be of whatever the tag
		// resolved to when the selftest runs — so the step refuses it here,
		// which is the step that knows what it smoked; the record step's own
		// sha256 guard would otherwise refuse the run later, with a message
		// about the record and not about the image.
		got, err := runCatalogSmoke(t, smoke, req.Substitutions, map[string]string{
			"FAKE_DOCKER_INSPECT": spec.Prebuilt,
			"FAKE_DOCKER_REPORT":  `{"passed":true}`,
		})
		if err == nil {
			t.Fatalf("the smoke succeeded although inspect answered no digest:\n%s", got.out)
		}
		if !strings.Contains(got.out, "carries no digest") {
			t.Errorf("the failure is not the missing digest:\n%s", got.out)
		}
		// The guard above it, whose message says the registry serves the
		// reference by tag: this run's inspect succeeded and printed a value
		// the step would not pin a smoke to.
		if strings.Contains(got.out, "the registry serves it by tag") {
			t.Errorf("the failure is the inspect's own, not the value it answered:\n%s", got.out)
		}
		if call := got.call("run"); call != "" {
			t.Errorf("the step smoked a reference it could not pin: %q", call)
		}
	})

	t.Run("a report that is not a pass fails the smoke", func(t *testing.T) {
		got, err := runCatalogSmoke(t, smoke, req.Substitutions, map[string]string{
			"FAKE_DOCKER_INSPECT": spec.Prebuilt + "@" + catalogSmokeDigest,
			"FAKE_DOCKER_REPORT":  `{"passed":false,"tools":[{"name":"claude","present":false}]}`,
		})
		if err == nil {
			t.Fatalf("the smoke succeeded although the image failed it:\n%s", got.out)
		}
		// The report is printed before the step fails, so the build's log says
		// which row of the contract the image missed.
		if !strings.Contains(got.out, `"name":"claude"`) {
			t.Errorf("the failure does not carry the report:\n%s", got.out)
		}
	})

	t.Run("a selftest that cannot run fails the smoke", func(t *testing.T) {
		got, err := runCatalogSmoke(t, smoke, req.Substitutions, map[string]string{
			"FAKE_DOCKER_INSPECT":  spec.Prebuilt + "@" + catalogSmokeDigest,
			"FAKE_DOCKER_REPORT":   `{"passed":false}`,
			"FAKE_DOCKER_RUN_EXIT": "1",
		})
		if err == nil {
			t.Fatalf("the smoke succeeded although the selftest did not run:\n%s", got.out)
		}
		if !strings.Contains(got.out, "failed its smoke test") {
			t.Errorf("the failure is not the selftest's:\n%s", got.out)
		}
	})

	// What a build is, a smoke-only run refuses: a request that named any of
	// it would be a build whose steps ignore it.
	for name, mod := range map[string]func(*CatalogBuildSpec){
		"a base": func(s *CatalogBuildSpec) {
			s.Base = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc"
		},
		"a registry image":           func(s *CatalogBuildSpec) { s.Image = "us-east5-docker.pkg.dev/proj-1234/fugaro/agent:latest" },
		"a context":                  func(s *CatalogBuildSpec) { s.Context = "." },
		"a definition":               func(s *CatalogBuildSpec) { s.Definition = CatalogDefinition{SourceCommit: strings.Repeat("a", 40)} },
		"a no-smoke run":             func(s *CatalogBuildSpec) { s.NoSmoke = true },
		"a reference with a quote":   func(s *CatalogBuildSpec) { s.Prebuilt = `docker.io/library/node:22"x` },
		"a bucket that is not gs://": func(s *CatalogBuildSpec) { s.Bucket = "fugaro-runs-proj-1234" },
		"no entry":                   func(s *CatalogBuildSpec) { s.Entry = "" },
		"no repository":              func(s *CatalogBuildSpec) { s.Slug = "" },
		"no build identity":          func(s *CatalogBuildSpec) { s.ServiceAccount = "" },
		"the repository's build account": func(s *CatalogBuildSpec) {
			s.ServiceAccount = BuildServiceAccountID(catalogSlug(t)) + "@proj-1234.iam.gserviceaccount.com"
		},
		"a smoke with no tools":    func(s *CatalogBuildSpec) { s.Selftest = []byte(`{"tools":"none"}`) },
		"a smoke that is not JSON": func(s *CatalogBuildSpec) { s.Selftest = []byte("critical") },
	} {
		bad := spec
		mod(&bad)
		if _, err := CatalogBuildRequest("proj-1234", bad); err == nil || !strings.Contains(err.Error(), "invalid Cloud Build request") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// catalogSmokeDigest is the digest the fake docker below answers a prebuilt
// reference with.
var catalogSmokeDigest = "sha256:" + strings.Repeat("a", 64)

// catalogSmokeRun is one run of the smoke step's script: the docker calls the
// step made, the spec its selftest read on stdin, the digest it wrote for the
// record step and the CLI, and its combined output.
type catalogSmokeRun struct {
	calls  []string
	stdin  string
	digest string
	out    string
}

// call returns the arguments of the step's first docker call whose
// subcommand is sub, or "".
func (r catalogSmokeRun) call(sub string) string {
	for _, c := range r.calls {
		if strings.HasPrefix(c, sub+" ") {
			return c
		}
	}
	return ""
}

// runCatalogSmoke runs the smoke step's script the way Cloud Build would: the
// step's own env: with its substitution placeholders resolved, a docker faked
// on PATH to answer what answers states, and a workspace and a BUILDER_OUTPUT
// of the test's own. The script's $$ escapes are Cloud Build's, so they are
// resolved here the way Cloud Build resolves them.
func runCatalogSmoke(t *testing.T, st *cloudbuild.BuildStep, subs, answers map[string]string) (catalogSmokeRun, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("the smoke step runs bash, which this machine has not: %v", err)
	}
	dir := t.TempDir()
	bin, out := filepath.Join(dir, "bin"), filepath.Join(dir, "out")
	for _, d := range []string{bin, out} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_DOCKER_CALLS"
case "$1" in
  pull) exit "${FAKE_DOCKER_PULL_EXIT:-0}" ;;
  image) printf '%s\n' "$FAKE_DOCKER_INSPECT"; exit "${FAKE_DOCKER_INSPECT_EXIT:-0}" ;;
  run) cat > "$FAKE_DOCKER_STDIN"; printf '%s\n' "$FAKE_DOCKER_REPORT"; exit "${FAKE_DOCKER_RUN_EXIT:-0}" ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	calls, stdin := filepath.Join(dir, "calls"), filepath.Join(dir, "stdin")
	env := []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"FAKE_DOCKER_CALLS=" + calls, "FAKE_DOCKER_STDIN=" + stdin,
		"OUT=" + out, "BUILDER_OUTPUT=" + out, "BUILD_ID=8f3c1a2b-0000-4000-8000-000000000001",
	}
	for _, e := range st.Env {
		if strings.HasPrefix(e, "OUT=") {
			continue
		}
		env = append(env, os.Expand(e, func(k string) string { return subs[k] }))
	}
	for k, v := range answers {
		env = append(env, k+"="+v)
	}
	script := strings.ReplaceAll(st.Args[len(st.Args)-1], "$$", "$")
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = env
	data, err := cmd.CombinedOutput()
	run := catalogSmokeRun{out: string(data)}
	if raw, err := os.ReadFile(calls); err == nil {
		run.calls = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
	if raw, err := os.ReadFile(stdin); err == nil {
		run.stdin = string(raw)
	}
	if raw, err := os.ReadFile(filepath.Join(out, "image-digest")); err == nil {
		run.digest = strings.TrimSpace(string(raw))
	}
	return run, err
}
