package gcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

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
// nothing (its Dockerfile is PIC3's copy), and gates on and writes a
// record of its own.
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
generation=$$(gcloud storage objects describe "$$BUCKET/$$KEY" --format="value(generation)")
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
// retag by digest, like promote: the build account's writer role on the
// shared registry covers it.
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
// it (CatalogRecordKey). Every value it carries is constrained — a
// validated entry name, hex sums, a digits-only generation, a validated
// image reference, Cloud Build's own build ID — so the step composes the
// JSON with printf and no escaping, and no secret is anywhere near it.
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
digest=$$(cat /workspace/out/image-digest)
case "$$digest" in sha256:*) ;; *) echo "no pushed digest" >&2; exit 1 ;; esac
built_at=$$(date -u +%Y-%m-%dT%H:%M:%SZ)
record=$$(mktemp)
printf '{"version":1,"entry":"%s","source_commit":"%s","definition_generation":%s,"definition_sha256":"%s","image":"%s","content_tag":"%s","base":"%s","digest":"%s","build_id":"%s","built_at":"%s"}' \
  "$$ENTRY" "$$COMMIT" "$$GENERATION" "$$SHA256" "$$IMAGE" "$$TAG" "$$BASE" "$$digest" "$BUILD_ID" "$$built_at" > "$$record"
gcloud storage cp --quiet "$$record" "$$BUCKET/$$KEY"
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
