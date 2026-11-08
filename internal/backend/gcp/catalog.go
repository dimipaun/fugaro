package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The steps a catalog build adds to the ones images/derived/cloudbuild.yaml
// already states. The rest of a catalog build's steps are that file's own,
// reused by id (prep, credential, build, smoke, promote, untag): a catalog
// image is built the way a derived image is, from the same base, with the
// same file-secret handling (_SECRET_ENVS) and the same smoke, and only
// where its definition comes from differs (PIC3's copy, not a checkout).
const (
	// catalogPinStepID is the step that stands in for render: it pins the
	// copy the build read and writes what the later steps need.
	catalogPinStepID = "catalog-pin"
	// catalogTagStepID adds the content tag beside the moving latest.
	catalogTagStepID = "catalog-tag"
	// catalogRecordStepID writes the record the daily check reads.
	catalogRecordStepID = "catalog-record"
)

// catalogBuildSteps are a catalog build's steps, in order. The derived
// config's source, render, gate and record steps are not among them: a
// catalog build never clones a repository into the build context, renders
// nothing (its Dockerfile is PIC3's copy), and writes a record of its own.
//
// Having no gate is the one difference that shows: a derived build's gate
// reads the record already in the bucket and refuses to promote over a newer
// one, so a build that finishes late leaves latest where it is, while a
// catalog build promotes as soon as it is smoked and can move latest back.
// A gate for this path — the copied definition's generation against the one
// the entry's record already carries — is a follow-up, and nothing here
// pretends to have one.
var catalogBuildSteps = []string{
	"prep", credentialStep, catalogDockerfileStepID, catalogPinStepID,
	"build", "smoke", "promote", catalogTagStepID, catalogRecordStepID, "untag",
}

// CatalogDefinition is what PIC3's copy left in the runs bucket, as the
// build that reads it is told about it: the commit the definition was taken
// from, the copied object's generation and the sha256 of the Dockerfile's
// bytes.
//
// The build verifies the last two against what it actually read and fails
// when they differ, so a copy that changed between the submission and the
// build is never built silently, and the record it writes says exactly
// which object was built. SourceCommit is the callers' (the copy's and the
// submitter's): a build reads one object and never a repository, so it
// cannot resolve a ref itself.
type CatalogDefinition struct {
	SourceCommit string `json:"source_commit"`
	Generation   int64  `json:"generation"`
	SHA256       string `json:"sha256"`
}

// CatalogBuildSpec is one build of a catalog entry's image (PIC4): the
// entry, the repository whose build account runs it, the source the image
// bakes and the definition the build reads from the bucket (PIC3).
type CatalogBuildSpec struct {
	// Entry is the entry's name: it names the image (catalog-<Entry>), the
	// copied definition (config.CatalogDockerfileKey) and the record
	// (CatalogRecordKey). It is never taken as a key from the caller.
	Entry string
	// Slug is the repository whose build account runs the build, holds its
	// provider credential and whose workflow secrets the entry mounts. Its
	// grant covers builds/<Slug>/ (the record) and, read-only, the copied
	// catalog definitions (CatalogReadCondition).
	Slug string
	// GitProvider, RepoURL, BaseBranch, GitSecretID, GitUser and
	// GitHubAppID are the entry's source repository, as BuildSpec's are: the
	// image bakes that repository's source, and the Dockerfile the copy
	// holds clones it with this credential.
	GitProvider string
	RepoURL     string
	BaseBranch  string
	GitSecretID string
	GitUser     string
	GitHubAppID string
	// Base is the fugaro base image the entry's image is built FROM.
	Base string
	// Image is the untagged image in the installation's shared registry:
	// SharedCatalogImage's value (D1152).
	Image string
	// Definition is the copy the build reads and pins.
	Definition CatalogDefinition
	// ServiceAccount is the email of the repository's build account
	// (BuildServiceAccountID), which the build runs as.
	ServiceAccount string
	MachineType    string
	// WorkflowSecrets become env BuildKit file secrets of the build step.
	WorkflowSecrets []config.Secret
	// Bucket is the runs bucket as gs://<name>: it holds the copied
	// definition and the record.
	Bucket string
	// Selftest is the smoke's spec, image.SpecForCloud as the caller
	// rendered it for this entry. A catalog build renders nothing, so the
	// spec reaches it in the request instead. It holds no secret: the smoke
	// runs repository code with no network and no secrets.
	Selftest []byte
	// NoSmoke leaves the candidate's smoke out (fugaro image build
	// --no-smoke). The daily check never sets it.
	NoSmoke bool
}

// SharedCatalogImage is the entry's image in the installation's one shared
// registry: <region>-docker.pkg.dev/<project>/<SharedRegistryRepoID()>/
// catalog-<entry> (D1152). It is per project, so it is not any repository's
// own registry, and its name is the entry's own. It makes no calls.
func SharedCatalogImage(region, project, entry string) (string, error) {
	host := DefaultRegistryHost(region, project)
	if !registryHostRE.MatchString(host) {
		return "", fmt.Errorf("the registry host %q is not <region>-docker.pkg.dev/<project>", host)
	}
	if _, err := config.CatalogDockerfileKey(entry); err != nil {
		return "", err
	}
	return host + "/" + SharedRegistryRepoID() + "/catalog-" + entry, nil
}

// CatalogImageEntry reads the entry's name back out of a catalog image's
// reference: <region>-docker.pkg.dev/<project>/<SharedRegistryRepoID()>/
// catalog-<entry>:latest, the image SharedCatalogImage names and the deploy
// gives a job the catalog chose for it (D1152). It is the reader of that
// writer, and false for every other reference — a repository's own derived
// image, an entry whose source is a prebuilt reference (which is that
// reference and nothing else), a digest rather than the moving tag.
//
// It makes no calls, and the entry it returns is grammar-checked by the same
// helper the keys are derived with: a name that could not be an entry never
// comes out of it.
func CatalogImageEntry(image string) (string, bool) {
	host, tail, ok := strings.Cut(image, "/"+SharedRegistryRepoID()+"/catalog-")
	if !ok || !registryHostRE.MatchString(host) {
		return "", false
	}
	entry, ok := strings.CutSuffix(tail, ":latest")
	if !ok {
		return "", false
	}
	if _, err := config.CatalogDockerfileKey(entry); err != nil {
		return "", false
	}
	return entry, true
}

// JobImage reads job (a job name in the backend's region) and returns the
// image its container is deployed with, and whether the job exists. A job
// whose template carries no container gives "", true.
//
// It is the deployment read the docker tag's rule is answered from
// (internal/cli's catalogDockerAsk, D1149): a job's image is fixed when the
// job is deployed (F7) — Cloud Run cannot vary it per execution — so the
// image a run will start on is the deployed job's own and never the one a
// selection made now would choose. It is JobFullEnv's and JobSecretRefs'
// shape, and reads one field of the same Jobs.Get.
func (b *Backend) JobImage(ctx context.Context, job string) (string, bool, error) {
	if !nameRE.MatchString(job) {
		return "", false, fmt.Errorf("bad job name %q", job)
	}
	j, err := b.run.Projects.Locations.Jobs.Get(b.location() + "/jobs/" + job).Context(ctx).Do()
	if err != nil {
		err = apiError("reading job "+job, err)
		if errors.Is(err, backend.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if j.Template != nil && j.Template.Template != nil && len(j.Template.Template.Containers) > 0 {
		return j.Template.Template.Containers[0].Image, true, nil
	}
	return "", true, nil
}

// CatalogRecordKey is where a catalog build writes the entry's record and
// the daily check reads it: builds/<slug>/catalog/<entry>/image.json.
//
// It is under the repository's own builds/<slug>/, not beside the copied
// definition, because that is where the repository's build account writes:
// its catalog grant is read-only (CatalogReadCondition) and must stay so,
// or a repository's build could rewrite a definition every other
// repository's jobs run. The record holds the entry, the source commit, the
// definition's generation and sha256, the image, its content tag, the base
// and the pushed digest, and the check reads the fields it decides from
// (imagecheck.CatalogRecordState). It makes no calls.
func CatalogRecordKey(slug, entry string) (string, error) {
	if _, err := config.CatalogDockerfileKey(entry); err != nil {
		return "", err
	}
	if !slugRE.MatchString(slug) {
		return "", fmt.Errorf("the repository slug %q is not a storage slug", slug)
	}
	return "builds/" + slug + "/catalog/" + entry + "/image.json", nil
}

// slugRE is what a storage slug may be. The shape task.Slug derives is not
// re-derived here: this refuses what would move a key out of its own prefix
// (a slash, a leading dot, a control character).
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// catalogCommitRE is a git commit SHA: the copy resolves a ref to one and
// the record names it.
var catalogCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// CatalogBuildRequest is the Cloud Build request of a catalog build in
// project: the steps images/derived/cloudbuild.yaml states for a derived
// image, minus the ones only a repository's own image has (source, render,
// gate, record), plus the catalog build's own (PIC3's read of the copy, the
// pin, the content tag and the record). Its substitutions are only the ones
// its steps reference, and each workflow secret is added to availableSecrets
// and to the build step's secretEnv and nowhere else. It makes no calls.
func CatalogBuildRequest(project string, s CatalogBuildSpec) (*cloudbuild.Build, error) {
	b, err := embeddedBuild()
	if err != nil {
		return nil, err
	}
	if b.AvailableSecrets == nil || len(b.AvailableSecrets.SecretManager) != 1 || b.AvailableSecrets.SecretManager[0].Env != gitTokenEnv {
		return nil, errNoGitTokenSecret
	}
	byID := map[string]*cloudbuild.BuildStep{}
	for _, st := range b.Steps {
		byID[st.Id] = st
	}
	build, cred := byID["build"], byID[credentialStep]
	if build == nil || cred == nil || !slices.Equal(cred.SecretEnv, []string{gitTokenEnv}) || !slices.Equal(cred.Env, credentialEnv[gitprov.KindBitbucket]) {
		return nil, errNoCredentialStep
	}
	if err := s.check(project, reservedEnv(build)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
	// PIC3's step derives the key from the entry's name and refuses one that
	// is not a name, so a request cannot point a build at another object.
	read, err := CatalogDockerfileStep(s.Bucket, s.Entry)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
	own := map[string]*cloudbuild.BuildStep{
		catalogDockerfileStepID: read,
		catalogPinStepID:        catalogPinStep(s),
		catalogTagStepID:        catalogTagStep(s),
		catalogRecordStepID:     catalogRecordStep(s),
	}
	steps := make([]*cloudbuild.BuildStep, 0, len(catalogBuildSteps))
	for _, id := range catalogBuildSteps {
		if st := own[id]; st != nil {
			steps = append(steps, st)
			continue
		}
		if st := byID[id]; st != nil {
			steps = append(steps, st)
		}
	}
	b.Steps = steps
	if s.NoSmoke {
		b.Steps = slices.DeleteFunc(b.Steps, func(st *cloudbuild.BuildStep) bool { return st.Id == "smoke" })
	}

	version := func(id string) string { return "projects/" + project + "/secrets/" + id + "/versions/latest" }
	b.AvailableSecrets.SecretManager[0].Env = credentialSecretEnv[s.GitProvider]
	b.AvailableSecrets.SecretManager[0].VersionName = version(s.GitSecretID)
	cred.SecretEnv = []string{credentialSecretEnv[s.GitProvider]}
	cred.Env = slices.Clone(credentialEnv[s.GitProvider])
	envs := make([]string, 0, len(s.WorkflowSecrets))
	for _, ws := range s.WorkflowSecrets {
		b.AvailableSecrets.SecretManager = append(b.AvailableSecrets.SecretManager,
			&cloudbuild.SecretManagerSecret{Env: ws.Env, VersionName: version(SecretID(s.Slug, ws.Name))})
		build.SecretEnv = append(build.SecretEnv, ws.Env)
		envs = append(envs, ws.Env)
	}
	// Cloud Build refuses a substitution the request doesn't reference, so
	// each provider sends only its own.
	b.Substitutions = map[string]string{
		"_REPO_URL":    s.RepoURL,
		"_BASE_BRANCH": s.BaseBranch,
		"_FUGARO_BASE": s.Base,
		"_IMAGE":       s.Image,
		"_SECRET_ENVS": strings.Join(envs, " "),
		// The content tag a rebuild moves (D1152): the definition's own
		// sha256, so the tag names the definition the image was built from.
		"_CATALOG_TAG": s.Definition.SHA256,
		// The smoke's spec, base64 because the request substitutes textually
		// and JSON is text this config would have to quote.
		"_SELFTEST_B64": base64.StdEncoding.EncodeToString(s.Selftest),
	}
	switch s.GitProvider {
	case gitprov.KindBitbucket:
		b.Substitutions["_GIT_USER"] = s.GitUser
	case gitprov.KindGitHub:
		b.Substitutions["_GITHUB_APP_ID"] = s.GitHubAppID
	}
	b.ServiceAccount = "projects/" + project + "/serviceAccounts/" + s.ServiceAccount
	if b.Options == nil {
		b.Options = &cloudbuild.BuildOptions{}
	}
	b.Options.MachineType = s.MachineType
	b.Options.Logging = "CLOUD_LOGGING_ONLY"
	b.Timeout = "3600s"
	return b, nil
}

// SubmitCatalog starts the build of s and returns it as queued: the catalog
// path's submit, whose request CatalogBuildRequest builds rather than
// BuildRequest's (PIC4). Everything after the request — the POST, the
// operation it answers with and the queued BuildResult — is Submit's own
// call (create), so the two paths cannot drift apart in what they send or in
// how they report it.
func (b *Builder) SubmitCatalog(ctx context.Context, s CatalogBuildSpec) (BuildResult, error) {
	req, err := CatalogBuildRequest(b.project, s)
	if err != nil {
		return BuildResult{}, err
	}
	return b.create(ctx, s.Image, req)
}

// catalogPinStep is the step that stands in for a derived build's render:
// a catalog build renders nothing, so this one pins the copy it read and
// writes what the later steps read from render otherwise.
//
// It fails the build when the copied object's generation or the sha256 of
// the Dockerfile's bytes is not the one the build was submitted with: what
// the record then says was built is what was built. Both values reach the
// script through env: and are read as quoted "$$VAR" (the rule
// images/derived/cloudbuild.yaml states).
func catalogPinStep(s CatalogBuildSpec) *cloudbuild.BuildStep {
	key, _ := config.CatalogDockerfileKey(s.Entry) // checked before this is called
	return &cloudbuild.BuildStep{
		Id:         catalogPinStepID,
		Name:       "gcr.io/google.com/cloudsdktool/cloud-sdk:slim",
		Entrypoint: "bash",
		Env: []string{
			"BUCKET=" + s.Bucket, "KEY=" + key,
			"GENERATION=" + strconv.FormatInt(s.Definition.Generation, 10),
			"SHA256=" + s.Definition.SHA256,
			"COMMIT=" + s.Definition.SourceCommit,
			"SELFTEST_B64=${_SELFTEST_B64}",
		},
		Args: []string{"-c", `set -eu
generation=$$(gcloud storage objects describe "$$BUCKET/$$KEY" --format="value(generation)" --quiet)
if [ "$$generation" != "$$GENERATION" ]; then
  echo "the copied catalog definition is not the one this build was submitted with: generation $$generation, not $$GENERATION" >&2
  exit 1
fi
sum=$$(sha256sum /workspace/context/Dockerfile)
sum=$${sum%% *}
if [ "$$sum" != "$$SHA256" ]; then
  echo "the copied catalog definition is not the one this build was submitted with: sha256 $$sum, not $$SHA256" >&2
  exit 1
fi
install -d -m 0755 /workspace/out
printf '%s\n' "$$COMMIT" > /workspace/out/source-commit
date -u +%Y-%m-%dT%H:%M:%SZ > /workspace/out/built-at
printf '%s' "$$SELFTEST_B64" | base64 -d > /workspace/out/selftest.json
echo "pinned $$KEY at generation $$GENERATION"`},
	}
}

// catalogTagStep adds the content tag to the digest the build pushed, so
// the tag a rebuild moves and latest are the same image (D1152). A registry
// retag by digest, like promote: it needs the build account's tag mover role
// on the shared registry, which writer alone is not (modules/repo/registry.tf
// holds both, beside each other).
func catalogTagStep(s CatalogBuildSpec) *cloudbuild.BuildStep {
	return &cloudbuild.BuildStep{
		Id:         catalogTagStepID,
		Name:       "gcr.io/google.com/cloudsdktool/cloud-sdk:slim",
		Entrypoint: "bash",
		Env:        []string{"IMAGE=" + s.Image, "TAG=${_CATALOG_TAG}"},
		Args: []string{"-c", `set -eu
digest=$$(cat /workspace/out/image-digest)
case "$$digest" in sha256:*) ;; *) echo "no pushed digest" >&2; exit 1 ;; esac
gcloud artifacts docker tags add "$$IMAGE@$$digest" "$$IMAGE:$$TAG" --quiet`},
	}
}

// catalogRecordStep writes the entry's record where the daily check reads
// it (CatalogRecordKey). The field names are imagecheck.CatalogRecordState's
// (and, where the meaning is the same, imagecheck.Record's): base_ref the
// base as the build was given it, base_digest the digest of the base the
// build ran FROM, and image_digest the digest latest was promoted to. Both
// digests are a bare sha256:…, the form the field holds everywhere else —
// imagecheck.Record's, and the digest the check reads a registry's as — and
// the build step writes the base's as <ref>@sha256:… (the form
// `fugaro image record` strips as well), so this step strips the reference
// off it and refuses a file that holds anything but a digest. built_at is
// the pin step's file, the same instant the build step stamps the image's
// created label with, so the record and the image say one build time and
// not two readings of a clock.
//
// Every value it carries is constrained, so the step composes the JSON with
// printf and no escaping, and no secret is anywhere near it: the entry name,
// the commit, the sums, the generation, the image and the base were checked
// before the request was built (CatalogBuildSpec.check), the build ID is
// Cloud Build's own, the build time is the pin step's file, and the two
// digests are checked here for shape because they come from files this step
// did not write.
//
// It writes with one objects.create — the JSON API's media upload, which
// names the object in its own URL — and never with `gcloud storage cp`: cp
// lists the destination before it copies, which is a storage.objects.list on
// the bucket, and the build account's write grant is an objectUser condition
// on builds/<slug>/ (BucketCondition, modules/repo/build.tf), which covers an
// object under that prefix and no bucket-level list. objects.create on that
// same name is exactly what the grant covers, so the write needs no grant
// change; asking for one instead would widen what a repository's build may
// list in the installation's bucket, which every other object's record and
// every run's task lives in.
//
// The upload carries no ifGenerationMatch, which is the step's own behaviour
// preserved rather than a new choice: `gcloud storage cp` replaces an
// existing object unless it is given --no-clobber, which this step never
// gave it, and a rebuild of an entry has to replace the record of the build
// it supersedes — same key, the newer build's digests and time. A create-only
// upload (ifGenerationMatch=0) would instead fail every rebuild of an entry
// with 412, from the second build of that entry on.
//
// The token is the build's own account's, through gcloud's ADC as every
// sibling step's is, and the object's name is percent-encoded byte by byte
// because the JSON API takes it as a query parameter, where a raw "/" would
// end the name and any other reserved character could add or split one.
func catalogRecordStep(s CatalogBuildSpec) *cloudbuild.BuildStep {
	key, _ := CatalogRecordKey(s.Slug, s.Entry) // checked before this is called
	return &cloudbuild.BuildStep{
		Id:         catalogRecordStepID,
		Name:       "gcr.io/google.com/cloudsdktool/cloud-sdk:slim",
		Entrypoint: "bash",
		Env: []string{
			"BUCKET=" + s.Bucket, "KEY=" + key, "ENTRY=" + s.Entry, "IMAGE=" + s.Image,
			"GENERATION=" + strconv.FormatInt(s.Definition.Generation, 10),
			"SHA256=" + s.Definition.SHA256, "COMMIT=" + s.Definition.SourceCommit,
			"BASE=" + s.Base, "TAG=${_CATALOG_TAG}",
		},
		Args: []string{"-c", `set -eu
image_digest=$$(cat /workspace/out/image-digest)
case "$$image_digest" in sha256:*) ;; *) echo "no pushed digest" >&2; exit 1 ;; esac
base_digest=$$(cat /workspace/base-digest)
base_digest=$${base_digest#*@}
case "$$base_digest" in sha256:*) ;; *) echo "no base digest" >&2; exit 1 ;; esac
built_at=$$(cat /workspace/out/built-at)
record=$$(mktemp)
printf '{"version":1,"entry":"%s","source_commit":"%s","definition_generation":%s,"definition_sha256":"%s","base_ref":"%s","base_digest":"%s","image":"%s","content_tag":"%s","image_digest":"%s","build_id":"%s","built_at":"%s"}' \
  "$$ENTRY" "$$COMMIT" "$$GENERATION" "$$SHA256" "$$BASE" "$$base_digest" "$$IMAGE" "$$TAG" "$$image_digest" "$BUILD_ID" "$$built_at" > "$$record"
case "$$BUCKET" in gs://*) ;; *) echo "the runs bucket must be a gs:// URL" >&2; exit 1 ;; esac
export LC_ALL=C
name=
i=0
while [ "$$i" -lt "$${#KEY}" ]; do
  c=$${KEY:$$i:1}
  case "$$c" in
    [A-Za-z0-9._~-]) name="$$name$$c" ;;
    *) name="$$name$$(printf '%%%02X' "'$$c")" ;;
  esac
  i=$$((i + 1))
done
url="https://storage.googleapis.com/upload/storage/v1/b/$${BUCKET#gs://}/o?uploadType=media&name=$$name"
curl --fail -sS -X POST \
  -H "Authorization: Bearer $$(gcloud auth print-access-token --quiet)" \
  -H "Content-Type: application/json" \
  --data-binary @"$$record" "$$url"
rm -f "$$record"
echo "recorded $$KEY"`},
	}
}

// check refuses a spec that is incomplete or would put an unsafe value in
// the build of project. reserved are the variables a workflow secret may
// not reuse.
func (s CatalogBuildSpec) check(project string, reserved map[string]bool) error {
	for _, f := range []struct{ name, v string }{
		{"catalog entry", s.Entry}, {"repository slug", s.Slug}, {"repository URL", s.RepoURL},
		{"base branch", s.BaseBranch}, {"base image", s.Base}, {"image", s.Image},
		{"git credential secret", s.GitSecretID}, {"build service account", s.ServiceAccount},
		{"runs bucket", s.Bucket},
	} {
		if f.v == "" {
			return fmt.Errorf("the Cloud Build request has no %s", f.name)
		}
	}
	// The record step composes its JSON with printf and no escaping, on the
	// claim that every value it carries is constrained; the base is the one
	// value that was only constrained to be non-empty, so it is checked
	// against the reference grammar here (imageRefRE), and a quote, a
	// backslash, a newline or any other control character in it is refused
	// rather than written into the record.
	if !imageRefRE.MatchString(s.Base) {
		return fmt.Errorf("the base image %q is not a container image reference such as ghcr.io/owner/image:1.2", s.Base)
	}
	// A name outside the entry grammar is refused by the helper the keys are
	// derived with, before anything else looks at it.
	if _, err := config.CatalogDockerfileKey(s.Entry); err != nil {
		return err
	}
	switch s.GitProvider {
	case gitprov.KindBitbucket:
		if s.GitUser == "" {
			return fmt.Errorf("the Cloud Build request has no git user")
		}
	case gitprov.KindGitHub:
		if !appIDRE.MatchString(s.GitHubAppID) {
			return fmt.Errorf("the GitHub App ID %q is not 1 to 20 digits", s.GitHubAppID)
		}
	default:
		return fmt.Errorf("the git provider %q is not %s or %s", s.GitProvider, gitprov.KindBitbucket, gitprov.KindGitHub)
	}
	if err := CheckRepoURL(s.GitProvider, s.RepoURL); err != nil {
		return err
	}
	if !bucketURLRE.MatchString(s.Bucket) {
		return fmt.Errorf("the runs bucket %q is not gs://<bucket>; the build reads its definition and records its image there", s.Bucket)
	}
	// Every catalog build is submitted with what it was told it would read:
	// without it there is nothing to pin and no record to trust.
	if !catalogCommitRE.MatchString(s.Definition.SourceCommit) {
		return fmt.Errorf("the copied definition's commit %q is not a commit SHA", s.Definition.SourceCommit)
	}
	if s.Definition.Generation <= 0 {
		return fmt.Errorf("the copied definition's generation %d is not an object generation", s.Definition.Generation)
	}
	if !sha256HexRE.MatchString(s.Definition.SHA256) {
		return fmt.Errorf("the copied definition's sum %q is not 64 hex digits", s.Definition.SHA256)
	}
	if len(s.Selftest) == 0 || !json.Valid(s.Selftest) {
		return fmt.Errorf("the smoke's spec is not JSON; a catalog build renders nothing, so the caller sends it")
	}
	// A catalog image may be built FROM any base (D1151), so its smoke is
	// the only thing that can assert the runtime contract there, and a spec
	// asking for no tools would assert the least exactly where the base
	// guarantees the least. The critical tier is the contract list, and the
	// runtime contract is reported with it.
	//
	// The key is read here rather than through image.SelftestSpec:
	// internal/image reaches this package (through imagecheck), so the spec
	// cannot be imported back, and the JSON key is what the caller that
	// renders the spec and this request agree on.
	var smoke struct {
		Tools string `json:"tools"`
	}
	if err := json.Unmarshal(s.Selftest, &smoke); err != nil {
		return fmt.Errorf("the smoke's spec: %w", err)
	}
	if smoke.Tools != "critical" && smoke.Tools != "all" {
		return fmt.Errorf("the smoke's spec asks for no tools (tools: %q), so the catalog image would be smoked without the runtime contract; a catalog image may be built from any base", smoke.Tools)
	}
	// The build account holds the repository's secrets and writes its
	// registry; any other account (the retired shared one, another
	// repository's, a job's) is refused.
	if want := BuildServiceAccountID(s.Slug) + "@" + project + ".iam.gserviceaccount.com"; s.ServiceAccount != want {
		return fmt.Errorf("the build service account %s is not the repository's build account %s", s.ServiceAccount, want)
	}
	// Only the entry's own image in the installation's shared registry of
	// this project, which this repository's build account writes.
	host, _, ok := strings.Cut(s.Image, "/"+SharedRegistryRepoID()+"/")
	m := registryHostRE.FindStringSubmatch(host)
	if !ok || m == nil || m[2] != project || s.Image != host+"/"+SharedRegistryRepoID()+"/catalog-"+s.Entry {
		return fmt.Errorf("the image %s is not the catalog image of entry %s in the installation's shared registry of project %s", s.Image, s.Entry, project)
	}
	seen := map[string]bool{}
	for _, ws := range s.WorkflowSecrets {
		if !secretEnvRE.MatchString(ws.Env) {
			return fmt.Errorf("workflow secret %s: variable %q is not [A-Z_][A-Z0-9_]*", ws.Name, ws.Env)
		}
		if reserved[ws.Env] || config.ReservedEnv(ws.Env) {
			return fmt.Errorf("workflow secret %s: variable %s is one the image build sets or uses itself; rename it", ws.Name, ws.Env)
		}
		if seen[ws.Env] {
			return fmt.Errorf("workflow secret %s: variable %s is already used in the build", ws.Name, ws.Env)
		}
		seen[ws.Env] = true
	}
	return nil
}
