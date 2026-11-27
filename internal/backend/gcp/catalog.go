package gcp

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	cloudbuild "google.golang.org/api/cloudbuild/v1"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/task"
)

// The steps a catalog build adds to the ones images/derived/cloudbuild.yaml
// already states. The rest of a catalog build's steps are that file's own,
// reused by id (prep, credential, build, smoke, promote, untag): a catalog
// image is built the way a derived image is, from the same base, with the
// same smoke, and only where its definition comes from differs (PIC3's copy,
// not a checkout, and B8's clone step in place of the credential the derived
// build's own step hands its Dockerfile).
const (
	// catalogPinStepID is the step that stands in for render: it pins the
	// copy the build read and writes what the later steps need.
	catalogPinStepID = "catalog-pin"
	// catalogTagStepID adds the content tag beside the moving latest.
	catalogTagStepID = "catalog-tag"
	// catalogRecordStepID writes the record the daily check reads.
	catalogRecordStepID = "catalog-record"
	// catalogSmokeStepID is the whole of a prebuilt entry's run: there is
	// nothing to build, so the one thing it does is smoke the reference the
	// entry names against the runtime contract (F10).
	catalogSmokeStepID = "catalog-smoke"
	// catalogScanStepID scans the image the build made for a leftover
	// credential of the build's, between the smoke and the promote (B3).
	catalogScanStepID = "catalog-scan"
)

// catalogSubstitutionLimit is Cloud Build's ceiling on one substitution's
// value: "the length of a parameter value is limited to 4000 bytes"
// (Substituting variable values), which its validator applies to a request's
// substitutions before the build is created. Every value a catalog build sent
// before the leak scan was small — a repository URL, an image reference, a
// sha256, the smoke's spec — and the scan's files are not: images/scan.sh's
// base64 is over twice this, so each file travels as more than one value
// (catalogScanFile, and the step that joins them in catalogScanStep).
const catalogSubstitutionLimit = 4000

// catalogScanFile is one of the two files the leak scan runs from, as the
// request carries it: images/scan.sh and images/trivy-secret.yaml, embedded in
// the binary that submits the build because a catalog build reads no checkout
// to run them from. It is the same script and the same rules CI runs, so the
// build and CI cannot disagree about what a leak is.
type catalogScanFile struct {
	// substitution is the prefix of the values this file travels in:
	// _SCAN_B64_1, _SCAN_B64_2, ... in order, each under
	// catalogSubstitutionLimit (catalogScanSubstitutions).
	substitution string
	// file is the name the scan step writes it under in SCANDIR, which is what
	// images/scan.sh reads its Trivy rules from (they sit next to the script).
	file string
	data []byte
}

// catalogScanFiles are the leak scan's two files, in the order the step writes
// them.
var catalogScanFiles = []catalogScanFile{
	{substitution: "_SCAN_B64", file: "scan.sh", data: images.ScanScript},
	{substitution: "_TRIVY_SECRET_B64", file: "trivy-secret.yaml", data: images.TrivySecretConfig},
}

// catalogScanChunks cuts a file's base64 into values at or under the limit.
// The cut need not fall on a group boundary: the step joins a file's chunks
// and decodes the join, never one chunk at a time.
func catalogScanChunks(data []byte) []string {
	b64 := base64.StdEncoding.EncodeToString(data)
	chunks := make([]string, 0, (len(b64)+catalogSubstitutionLimit-1)/catalogSubstitutionLimit)
	for len(b64) > catalogSubstitutionLimit {
		chunks = append(chunks, b64[:catalogSubstitutionLimit])
		b64 = b64[catalogSubstitutionLimit:]
	}
	return append(chunks, b64)
}

// catalogScanSubstitutions are the values the request carries the leak scan's
// files in, under the names the scan step's own env: declares.
func catalogScanSubstitutions() map[string]string {
	subs := map[string]string{}
	for _, f := range catalogScanFiles {
		for i, chunk := range catalogScanChunks(f.data) {
			subs[catalogScanValueName(f.substitution, i)] = chunk
		}
	}
	return subs
}

// catalogScanValueName is the name of a file's i'th chunk (from 0) as a
// substitution and as the env variable the step reads it from: the two differ
// only in the substitution's leading underscore.
func catalogScanValueName(substitution string, i int) string {
	return fmt.Sprintf("%s_%d", substitution, i+1)
}

// catalogContextRoot is where the clone step puts the entry's repository for an
// entry that names a build context: the path the derived build's own source
// step clones into, and the path a catalog build exports its checkout into. It
// is not /workspace/context, which holds the copied Dockerfile the build reads
// and the pin step verifies (D1404).
const catalogContextRoot = "/workspace/src"

// catalogBuildSteps are a catalog build's steps, in order. The derived
// config's source, render, gate and record steps are not among them: a
// catalog build renders nothing (its Dockerfile is PIC3's copy), writes a
// record of its own, and clones in catalogCloneStepID — a step of its own
// whose build the entry has no part in, which is where the entry's build
// stopped being handed the git credential (B8, F19).
//
// The scan step stands between the smoke and the promote (B3, R2): an image
// that carries a credential of the build's — in a baked git config, in a
// .netrc, in a credential-helper store, in a variable or in any layer — is
// refused rather than moved to latest.
//
// Having no gate is the one difference that shows: a derived build's gate
// reads the record already in the bucket and refuses to promote over a newer
// one, so a build that finishes late leaves latest where it is, while a
// catalog build promotes as soon as it is smoked and can move latest back.
// A gate for this path — the copied definition's generation against the one
// the entry's record already carries — is a follow-up, and nothing here
// pretends to have one.
var catalogBuildSteps = []string{
	"prep", credentialStep, catalogDockerfileStepID, catalogCloneStepID, catalogPinStepID,
	"build", "smoke", catalogScanStepID, "promote", catalogTagStepID, catalogRecordStepID, "untag",
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

// BuildIdentityAccountID is the project's build identity (B5, D2301): the ONE
// service account of an installation that its catalog builds run as, created
// by the same apply that turns the credentials service on
// (deploy/terraform/gcp/modules/installation/build_identity.tf, whose
// account_id is this literal).
//
// It is a per-PROJECT account and not a repository's, and that is why a
// catalog build runs as it (B6): a repository's build account
// (BuildServiceAccountID) is derived from the repository and dies with the
// repository's root, while a catalog entry may name a repository whose root is
// gone and whose images are still built — M2 retires every root — so the
// account a build of that entry runs as has to outlive every repository the
// entry names.
//
// internal/infra's BuildIdentityAccountID is the same literal on the apply's
// side, and TestCatalogBuildRunsAsTheBuildIdentity pins the two together.
const BuildIdentityAccountID = "fugaro-build-identity"

// BuildIdentityEmail is the address of the project's build identity in
// gcpProject, which is the value a CatalogBuildSpec carries: the claim is
// computed here from the literal above and the project (a service account's
// email is <id>@<project>.iam.gserviceaccount.com), never read back from the
// installation, so the account a build is submitted as is a fact of this
// package and of the module's own account_id.
func BuildIdentityEmail(gcpProject string) string {
	return BuildIdentityAccountID + "@" + gcpProject + ".iam.gserviceaccount.com"
}

// CatalogBuildSpec is one build of a catalog entry's image (PIC4): the
// entry, the project's build identity it runs as, the source the image bakes
// and the definition the build reads from the bucket (PIC3).
type CatalogBuildSpec struct {
	// Entry is the entry's name: it names the image (catalog-<Entry>), the
	// copied definition (config.CatalogDockerfileKey) and the record
	// (CatalogRecordKey). It is never taken as a key from the caller.
	Entry string
	// Slug is the repository whose credential the credential step asks the
	// service for, and whose record prefix (builds/<Slug>/catalog/) the build
	// writes and the daily check reads; the entry's own build is handed that
	// checkout and no secret of the repository's (B9, D2311, and IS8 for where
	// the credential comes from). It is not the account the
	// build runs as — that is the project's build identity (B6), one for the
	// whole installation — because a repository's build account dies with the
	// repository's Terraform root (M2 retires every one of them) while its
	// entries and their builds stay. Its grant covers builds/<Slug>/ (the
	// record) and, read-only, the copied catalog definitions
	// (CatalogReadCondition).
	Slug string
	// GitProvider, RepoURL, BaseBranch, GitSecretID, GitUser and
	// GitHubAppID are the entry's source repository, as BuildSpec's are: the
	// image bakes that repository's source, which the clone step clones and
	// the entry's own build copies out of the build context it is handed
	// (B8, F19). GitSecretID and GitUser are READ BY NOTHING here since IS8
	// (D3330): the request names no AvailableSecrets and the credential step
	// fetches the credential from CredentialsServiceURL instead, so the two
	// are carried — and checked, which is the one thing they still do — only
	// because both submit sites resolve them for the same repository and a
	// request's shape is the reader's contract; nothing in this file is
	// served by their value.
	GitProvider string
	RepoURL     string
	BaseBranch  string
	GitSecretID string
	GitUser     string
	GitHubAppID string
	// CredentialsServiceURL is the installation's credentials service, which
	// the credential step asks for the one git credential this build clones
	// with (IS8, D3330): the route it calls is the entry route
	// (credsvc.BuildTokenPath), whose caller is the project's build identity
	// (BuildIdentityEmail) — the account every catalog build runs as — and
	// which answers the credential of the entry's own repositories and no
	// others. It is required: a request that names no service is refused
	// before the build is paid for, because such a build would clone nothing.
	CredentialsServiceURL string
	// Base is the fugaro base image the entry's image is built FROM. A
	// prebuilt entry is built from nothing, so it names none.
	Base string
	// Image is the untagged image in the installation's shared registry:
	// SharedCatalogImage's value (D1152). A prebuilt entry pushes nothing, so
	// it names none.
	Image string
	// Prebuilt is the reference of an entry whose source is a prebuilt image
	// (config.SourceImage): such an entry has nothing to build — it names an
	// image somebody else published — so its run is a smoke-only Cloud Build
	// of that reference (F10), with no Dockerfile to read, no credential to
	// mount, nothing pushed and no tag moved. It is "" for an entry Fugaro
	// builds, and a spec that sets it sets none of the build's own inputs
	// (checkPrebuilt).
	Prebuilt string
	// Definition is the copy the build reads and pins.
	Definition CatalogDefinition
	// Baked are the repositories the copied definition bakes checkouts of
	// (imagebake.BakedRepos): a stage imagebake appended copies each of them
	// out of the image the clone step builds, and the marker line that stage
	// opens with names the clone URL and branch the clone step clones it from.
	//
	// The list is what the clone step produces and the definition copies, and
	// the entry's own build step is handed no secret for it: the build that
	// holds the git credential is the clone step's and no build of the
	// entry's own (B8, F19), so a definition that bakes is built exactly as
	// one that does not (catalogEntryBuildStep). It is empty for a definition
	// the entry's own text holds, which is every entry that names its own
	// checkout, and its LAST element is the repository the build runs for —
	// the checkout the definition is known by (imagebake.BakedSlug).
	Baked []imagebake.Repo
	// Context is the directory of the entry's repository the build runs on —
	// config.Source.Context, the entry's own value — and "" when the entry
	// names none, which is the shape every entry had before the key existed.
	// It reaches the request as _CONTEXT_DIR, the directory the build step
	// builds from; the Dockerfile it reads stays the copy (D1404).
	Context string
	// ContextCommit and ContextSHA256 are the context's own identity, as the
	// copy staged it: the commit the checkout is made at
	// (config.CatalogContextCommitKey) and the digest of the tree it holds
	// (config.CatalogContextHashKey). Both are "" for an entry that names no
	// context, and a submission states all three or none — a context nothing
	// pinned, or a commit no tree was read at, is refused before a build is
	// paid for.
	//
	// The commit is not Definition.SourceCommit: the two are one commit
	// (D1395 pins the Dockerfile and the context to the same read), and the
	// record's fields for them are two names because a reader of the record
	// has to be able to tell which is which (D1400).
	ContextCommit string
	ContextSHA256 string
	// TemplateSalt is TemplateSalt of the binary submitting the build: the
	// digest of the embedded cloudbuild.yaml and the fugaro version, which is
	// how this build builds rather than what the entry holds. The record
	// carries it, so the nightly check sees an engine-side change to the
	// build and rebuilds the entry, the way a workflow check does (F33,
	// D1402). "" is a submitter that states none, and the record writes it as
	// it was given it.
	TemplateSalt string
	// Epoch is the base-image epoch the submitting binary builds under
	// (imagecheck.BaseEpoch), which the record carries: an entry whose record
	// states another epoch was built from base image files that have since
	// changed, and update rebuilds it eagerly (D3446). A submitter that knows
	// no epoch states 0, which is stale too.
	Epoch int
	// ServiceAccount is the email of the project's build identity
	// (BuildIdentityEmail), which the build runs as: one account for the
	// installation, never a repository's (B6).
	ServiceAccount string
	MachineType    string
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

// CatalogBuildRequest is the Cloud Build request of a catalog entry in
// project: the steps images/derived/cloudbuild.yaml states for a derived
// image, minus the ones only a repository's own image has (source, render,
// gate, record), plus the catalog build's own (PIC3's read of the copy, the
// pin, the content tag and the record) — or, for a spec whose entry is a
// prebuilt image, the smoke-only run catalogPrebuiltRequest builds. Its
// substitutions are only the ones its steps reference, and it names no secret
// at all: the credential the clone step reads is the installation's service's
// answer, which the credential step fetches as the project's build identity
// (IS8, D3330). It makes no calls.
func CatalogBuildRequest(project string, s CatalogBuildSpec) (*cloudbuild.Build, error) {
	if s.Prebuilt != "" {
		if err := s.checkPrebuilt(project); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
		}
		return catalogPrebuiltRequest(project, s), nil
	}
	b, err := embeddedBuild()
	if err != nil {
		return nil, err
	}
	// The template still declares the one availableSecret the swap below used
	// to fill, and this refuses a template that stopped: the mount is what
	// this request takes out, and a template it cannot read is one whose
	// credential step is not the step being replaced.
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
	// The entry's own build step stops being handed the git credential before
	// the spec is checked, so the variables the request reserves are the ones
	// that step really carries: the checkout reaches it as an image of the
	// worker's own instead, and the secret and the volume that held it stay in
	// the clone step's build (B8, F19) — unless the definition bakes, which is
	// the one definition whose own build step is the last to read the
	// credential and keeps both (Baked).
	if err := catalogEntryBuildStep(build); err != nil {
		return nil, err
	}
	if err := s.check(project); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
	// PIC3's step derives the key from the entry's name and refuses one that
	// is not a name, so a request cannot point a build at another object.
	read, err := CatalogDockerfileStep(s.Bucket, s.Entry)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
	clone, err := catalogCloneStep(s, buildStepCwd(build))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
	// The repositories the request names for the credential: the one this build
	// clones, in the canonical spelling the service compares against. The
	// project's repository-to-image map (`repo_images`) puts that spelling under
	// a provider to key a repository (rule 8, projectpolicy.ValidRepoImages).
	repos, err := catalogEntryRepos(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
	own := map[string]*cloudbuild.BuildStep{
		catalogDockerfileStepID: read,
		catalogCloneStepID:      clone,
		catalogPinStepID:        catalogPinStep(s),
		catalogScanStepID:       catalogScanStep(s),
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

	// The mount goes, whole (IS8, D3330): no availableSecret, no step's
	// secretEnv, and the credential step's env and argv replaced by the entry
	// mode's. Cloud Build serves a secretEnv only out of availableSecrets, so
	// the two leave together, and the accessor the installation granted the
	// build identity for that secret leaves with them
	// (deploy/terraform/gcp/modules/installation/build_identity.tf).
	b.AvailableSecrets = nil
	if err := catalogCredentialStep(s, cred); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
	// Cloud Build refuses a substitution the request doesn't reference, so
	// each provider sends only its own.
	b.Substitutions = map[string]string{
		"_REPO_URL":    s.RepoURL,
		"_BASE_BRANCH": s.BaseBranch,
		"_FUGARO_BASE": s.Base,
		"_IMAGE":       s.Image,
		// The credential step's own three: the address of the service it asks
		// for this build's credential, the entry it is the build of, and the
		// repositories the request names for it. They reach the step as its env
		// and never in its argv, so a name a config left in a list is data and
		// not shell next to the credential (catalogCredentialStep).
		"_CREDENTIALS_SERVICE_URL": s.CredentialsServiceURL,
		"_ENTRY":                   s.Entry,
		"_CATALOG_REPOS":           repos[0],
		// Empty, always: the template's build step mounts the variables this
		// names as BuildKit file secrets, and it names none for a catalog
		// entry. The repository's own workflow secrets reached a catalog
		// build's entry step until B9 — the step is the derived build's, and
		// its loop is why the empty value is written out rather than the
		// substitution dropped — and D2311 (rule 4 of
		// docs/shared-jobs-default/contract-build-token.md) leaves an entry's
		// build no secret of the entry's at all: not a mount its definition
		// asks for and not one it could ask for under another spelling, since
		// what a build is handed is what decides that, not what its text
		// happens to say.
		"_SECRET_ENVS": "",
		// The content tag a rebuild moves (D1152): the definition's own
		// sha256, so the tag names the definition the image was built from.
		"_CATALOG_TAG": s.Definition.SHA256,
		// The directory the build step builds from: the checkout, at the
		// subdirectory a context names, or the copied definition's own
		// directory for an entry that names none.
		"_CONTEXT_DIR": s.contextDir(),
		// The smoke's spec, base64 because the request substitutes textually
		// and JSON is text this config would have to quote.
		"_SELFTEST_B64": base64.StdEncoding.EncodeToString(s.Selftest),
	}
	// The leak scan's two files, carried the same way but cut into more than
	// one value each: a catalog build reads no checkout, so the script and its
	// rules reach it from the binary that submitted the build
	// (catalogScanStep), and one of them is over the limit on a substitution's
	// value (catalogSubstitutionLimit).
	for name, value := range catalogScanSubstitutions() {
		b.Substitutions[name] = value
	}
	switch s.GitProvider {
	case gitprov.KindGitHub:
		b.Substitutions["_GITHUB_APP_ID"] = s.GitHubAppID
	}
	b.ServiceAccount = "projects/" + project + "/serviceAccounts/" + s.ServiceAccount
	if b.Options == nil {
		b.Options = &cloudbuild.BuildOptions{}
	}
	b.Options.MachineType = s.MachineType
	b.Options.Logging = "CLOUD_LOGGING_ONLY"
	// The catalog check job's build is bound the same way the derived build
	// is: the timeout the estimator prices, over a queue wait the lease's
	// deadline also covers (BuildRequest, and the test that pins all four
	// against internal/budget's two constants).
	b.Timeout = "3600s"
	b.QueueTtl = "3600s"
	return b, nil
}

// checkServiceURL refuses an address the credential step must not send this
// build's own identity token to. The token is a bearer credential minted for
// the installation's service and for no other host, so the URL is that service's
// own: https, with no path, query, fragment or credentials of its own. It is the
// same rule the step applies before it sends anything (internal/cli's
// checkBuildServiceURL), asked here so that a request whose step could never
// reach the service is refused before the build is paid for.
func checkServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("the credentials service %q is not the https URL the installation serves it on, with no path, query or fragment: the credential step's identity token is minted for exactly that address", raw)
	}
	return nil
}

// catalogCredentialTTLSeconds is the lifetime the credential step asks the
// service for. It is the build's own timeout, which is what the step needs: the
// credential it writes is spent by the clone step later in the same build, so a
// shorter ask would be a credential that expires between the two. The service
// caps the ask at its own maximum either way (credsvc's buildTokenMaxTTL), so
// this names the need and not the grant.
const catalogCredentialTTLSeconds = 3600

// catalogEntryRepos is the repositories a catalog build names for its
// credential: the one its source is, which is the only repository this build
// clones (catalogCloneStep). The name is task.CanonicalRepo's spelling and not
// the URL's, because that is the service's key space — the published
// repository-to-image map (`repo_images`) keys a repository by that spelling
// under its provider (projectpolicy.ValidRepoImages) — and it is what the route
// compares the request against before it mints anything (D1906).
//
// It is one name and not the entry's whole map: what a build asks for has to be
// what the build really clones, and the route answers a repository the request
// did not name by leaving it out of the token.
func catalogEntryRepos(s CatalogBuildSpec) ([]string, error) {
	_, p, ok := gitprov.SplitRemote(s.RepoURL)
	if !ok {
		return nil, fmt.Errorf("the repository URL %s does not name a repository", RedactURL(s.RepoURL))
	}
	canonical, err := task.CanonicalRepo(strings.TrimSuffix(gitprov.TrimRepoPath(p), ".git"))
	if err != nil {
		return nil, fmt.Errorf("the repository URL %s is not a repository this installation can name: the credential is asked for it by its canonical owner/name", RedactURL(s.RepoURL))
	}
	return []string{canonical}, nil
}

// catalogCredentialStep makes the template's credential step the entry mode's
// (IS8, D3330): one request for the entry being built — asked of the
// installation's credentials service, as the project's build identity the build
// runs as, which is the caller the entry route admits — and one credential
// written where the clone step reads it, the store file both of them know by
// the same path (/creds/git-credentials).
//
// It replaces the step's env and its argv whole rather than adding to them: the
// one-repository mode reads the provider's secret out of its environment, and a
// variable left in it, or a flag left in the argv, is a way for a later step to
// read a secret this build no longer has. Every value the entry mode reads is
// the step's own environment and never its argv, which is why the entry, the
// repositories and the build id are substitutions reached through env (the
// template's own rule for this step: what a shell substitutes is not shell
// itself).
//
// The build id is Cloud Build's own $BUILD_ID, which is the id this build runs
// under — the service keys a Bitbucket pairing on it beside the identity.
func catalogCredentialStep(s CatalogBuildSpec, cred *cloudbuild.BuildStep) error {
	cred.SecretEnv = nil
	cred.Env = []string{
		"PROVIDER=" + s.GitProvider,
		"ENTRY=${_ENTRY}",
		"CATALOG_REPOS=${_CATALOG_REPOS}",
		"FUGARO_CREDENTIALS_SERVICE_URL=${_CREDENTIALS_SERVICE_URL}",
		"BUILD_ID=$BUILD_ID",
	}
	if s.GitProvider == gitprov.KindGitHub {
		// The GitHub installation token is minted as the App, and the App's id
		// is the one thing this build holds and the service's bundle does not
		// (the bundle holds the key). It is not a secret.
		cred.Env = append(cred.Env, "GITHUB_APP_ID=${_GITHUB_APP_ID}")
	}
	cred.Args = []string{"-c", `exec fugaro image git-credential --entry "$$ENTRY" --build-id "$$BUILD_ID" --provider "$$PROVIDER" --repos "$$CATALOG_REPOS" --ttl-seconds ` + strconv.Itoa(catalogCredentialTTLSeconds) + ` --out ` + catalogCloneCredential}
	return nil
}

// SubmitCatalog starts the build of s and returns it as queued: the catalog
// path's submit, whose request CatalogBuildRequest builds rather than
// BuildRequest's (PIC4). Everything after the request — the POST, the
// operation it answers with and the queued BuildResult — is Submit's own
// call (create), so the two paths cannot drift apart in what they send or in
// how they report it. The image that names the build there is the entry's
// own reference for a prebuilt one, which builds and pushes nothing.
func (b *Builder) SubmitCatalog(ctx context.Context, s CatalogBuildSpec) (BuildResult, error) {
	req, err := CatalogBuildRequest(b.project, s)
	if err != nil {
		return BuildResult{}, err
	}
	return b.create(ctx, cmp.Or(s.Prebuilt, s.Image), req)
}

// catalogPrebuiltRequest is the request of an entry whose source is a
// prebuilt image: the smoke of the reference the entry names and the record
// of it, and nothing else (F10). There is nothing to build — the image is
// somebody else's and is used as it is — so no Dockerfile is read, no
// credential is mounted, nothing is pushed and no tag moves: what the run
// answers is whether that image meets the runtime contract a job needs, and
// the record it writes is what makes the answer readable afterwards
// (fugaro doctor reads it).
//
// It carries no availableSecrets at all, where a build's request carries the
// repository's provider credential: no step of this run reads one, and the
// build account is named for the record's grant and nothing else.
func catalogPrebuiltRequest(project string, s CatalogBuildSpec) *cloudbuild.Build {
	return &cloudbuild.Build{
		Steps: []*cloudbuild.BuildStep{catalogSmokeStep(s), catalogRecordStep(s)},
		// Only what those two steps reference: the entry's reference and the
		// smoke's spec, plus the content tag the record step's own env reads
		// and leaves empty — Cloud Build refuses a substitution the request
		// does not reference, and nothing here reads a repository, a base or
		// a context.
		Substitutions: map[string]string{
			"_IMAGE":        s.Prebuilt,
			"_SELFTEST_B64": base64.StdEncoding.EncodeToString(s.Selftest),
			"_CATALOG_TAG":  "",
		},
		Options: &cloudbuild.BuildOptions{
			MachineType: s.MachineType,
			Logging:     "CLOUD_LOGGING_ONLY",
		},
		ServiceAccount: "projects/" + project + "/serviceAccounts/" + s.ServiceAccount,
		// The derived and the catalog builds' own bounds: the timeout the
		// estimator prices, over a queue wait the lease's deadline also
		// covers (BuildRequest, and the test that pins all four against
		// internal/budget's two constants).
		Timeout:  "3600s",
		QueueTtl: "3600s",
	}
}

// catalogSmokeStep smokes a prebuilt entry's image against the runtime
// contract: the whole of that entry's run (F10).
//
// The image is its author's, so what can be asserted of it is the contract
// and nothing else: the critical rows of images/base/tools.tsv, which the
// spec asks for through its tools_only form (the caller renders it), and no
// checkout to verify, no Fugaro base's hardening and no setuid set of its
// own, because the image was not built from this tool's base. The first of
// those rows is fugaro itself, and the smoke is run by it: an image that
// carries no fugaro fails here rather than a job's start being the first
// thing to find out.
//
// It pulls the reference the entry names and resolves the digest that
// reference answers with, so what was smoked is bytes and not a tag that
// moved afterwards. The digest reaches the record step through the workspace
// (OUT, the one directory this script writes) and the CLI through this step's
// output (stepOutputs reads it by this step's id). The selftest runs through
// docker run with no network and no volume, never as a step of its own — a
// step would be a container on the cloudbuild network, next to the metadata
// server and the build account's token — as the derived smoke does.
func catalogSmokeStep(s CatalogBuildSpec) *cloudbuild.BuildStep {
	// The three values reach the script through env: and are read as quoted
	// "$$VAR" (the rule images/derived/cloudbuild.yaml states).
	return &cloudbuild.BuildStep{
		Id:         catalogSmokeStepID,
		Name:       "gcr.io/cloud-builders/docker",
		Entrypoint: "bash",
		Env:        []string{"IMAGE=${_IMAGE}", "SELFTEST_B64=${_SELFTEST_B64}", "OUT=/workspace/out"},
		Args: []string{"-c", `set -eu
install -d -m 0755 "$$OUT"
docker pull --quiet "$$IMAGE"
ref=$$(docker image inspect --format '{{index .RepoDigests 0}}' "$$IMAGE") \
  || { echo "the prebuilt image $$IMAGE carries no digest: the registry serves it by tag and nothing pins what a job would start on" >&2; exit 1; }
case "$$ref" in *@sha256:*) ;; *) echo "the prebuilt image $$IMAGE carries no digest" >&2; exit 1 ;; esac
printf '%s\n' "$${ref#*@}" > "$$OUT/image-digest"
date -u +%Y-%m-%dT%H:%M:%SZ > "$$OUT/built-at"
printf '%s' "$$SELFTEST_B64" | base64 -d > "$$OUT/selftest.json"
report=$$(docker run --rm -i --network none "$$ref" fugaro image selftest < "$$OUT/selftest.json") \
  || { printf '%s\n' "$$report"; echo "the prebuilt image $$IMAGE failed its smoke test" >&2; exit 1; }
printf '%s\n' "$$report"
case "$$report" in *'"passed":true'*) ;; *) echo "the prebuilt image $$IMAGE did not pass its smoke test" >&2; exit 1 ;; esac
printf '%s' "$${ref#*@}" > "$$BUILDER_OUTPUT/output"
echo "smoked $$ref"`},
	}
}

// catalogScanStep scans the image the build just pushed, before the build
// would promote it: images/scan.sh --secrets, the scan CI runs on the same
// image (R2, F19), over the digest the build pushed, so what is scanned is the
// bytes latest would move to and not a tag that could have moved since.
//
// It refuses an image that carries a credential of the build's anywhere the
// scan looks: a token in the image config's environment, a .netrc, a
// credential-helper store, or a git config that carries one — in any layer, and
// in the .git/config of a checkout baked into it. It is the second fence F19
// names and not the first: the clone step alone holds the credential and its
// checkout is stripped before the entry's build sees it (B8), and this scan
// asks what a credential looks like rather than what an entry copied — a token
// a build's own tools wrote into a config, an ENV or a .netrc, or one the
// strip missed.
//
// The script and its Trivy rules travel in the request, base64, because a
// catalog build reads no checkout to run them from: images.ScanScript and
// images.TrivySecretConfig are images/scan.sh and images/trivy-secret.yaml as
// the submitting binary carries them. It is the same script and the same rules
// CI runs, so the build and CI cannot disagree about what a leak is — the step
// writes both out and runs the script, rather than growing a second copy of
// the checks.
//
// Each file arrives as several substitutions (catalogScanFiles), because one
// value may not exceed the limit Cloud Build puts on a substitution — and the
// step reads them as several env variables and joins them before decoding
// them, rather than as one variable holding the whole file: joining them in
// the request would put the file back over the limit in a single string.
//
// The trivy container needs nothing of the network and no registry credential:
// it reads the image out of the worker's own docker daemon, which is where the
// build step left it.
func catalogScanStep(s CatalogBuildSpec) *cloudbuild.BuildStep {
	// The three paths reach the script through env: and are read as quoted
	// "$$VAR" (the rule images/derived/cloudbuild.yaml states). SCANDIR and the
	// digest's directory are env values of their own rather than text in the
	// script, as the smoke step's own are, so the script runs outside Cloud
	// Build too (catalog_test.go runs it).
	env := []string{
		"IMAGE=${_IMAGE}", "OUT=/workspace/out", "SCANDIR=/workspace/scan",
		"TRIVY_CACHE_DIR=/workspace/trivy",
	}
	// One env variable per chunk of a file, none larger than the substitution
	// behind it, and one line per file joining its chunks into it.
	var writes []string
	for _, f := range catalogScanFiles {
		chunks := catalogScanChunks(f.data)
		read := make([]string, 0, len(chunks))
		for i := range chunks {
			substitution := catalogScanValueName(f.substitution, i)
			name := strings.TrimPrefix(substitution, "_")
			env = append(env, name+"=${"+substitution+"}")
			read = append(read, `"$$`+name+`"`)
		}
		writes = append(writes, "for chunk in "+strings.Join(read, " ")+
			`; do printf '%s' "$$chunk"; done | base64 -d > "$$SCANDIR/`+f.file+`"`)
	}
	return &cloudbuild.BuildStep{
		Id:         catalogScanStepID,
		Name:       "gcr.io/cloud-builders/docker",
		Entrypoint: "bash",
		Env:        env,
		Args: []string{"-c", `set -eu
install -d -m 0755 "$$SCANDIR"
` + strings.Join(writes, "\n") + `
digest=$$(cat "$$OUT/image-digest")
case "$$digest" in sha256:*) ;; *) echo "no pushed digest" >&2; exit 1 ;; esac
sh "$$SCANDIR/scan.sh" --secrets "$$IMAGE@$$digest"
echo "scanned $$IMAGE@$$digest"`},
	}
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

// contextDir is the directory the build step builds from: the checkout the
// context step made, at the subdirectory the entry's context names — the
// context root is that directory (D1406), so a COPY inside it resolves the
// way the Dockerfile wrote it — or the copied definition's own directory,
// contextDirDefault, for an entry that names no context.
func (s CatalogBuildSpec) contextDir() string {
	if s.Context == "" {
		return contextDirDefault
	}
	if c := strings.TrimSuffix(s.Context, "/"); c != "" && c != "." {
		return catalogContextRoot + "/" + c
	}
	return catalogContextRoot
}

// contextPathMessage names what is wrong with a context as the directory a
// build runs on, "" when nothing is. contextDir joins the value onto
// catalogContextRoot, so a leading / or a .. segment would hand the build step
// a directory elsewhere in the container rather than the checkout this
// request's own clone step wrote — the workspace's other files, a tree no step
// cloned — and a `//` or a `.` segment would name a directory the checkout does
// not have. What the build step may read is that one tree, which is why the
// rule stands after B8 as it did before it: the fence is a build context now
// instead of a secret, and catalogCloneGuard is its other half (a link out of
// the checkout is visible only in the tree). config holds these rules for
// the key (catalogContextMessage); the request holds them too, because it is
// built from a spec, not from a fugaro.yaml that was validated. config's
// remaining rules for the key, the length bound it shares with every other
// path, do not change which directory a build reads from.
func contextPathMessage(f string) string {
	switch {
	case f == "":
		return ""
	case strings.ContainsFunc(f, unicode.IsControl):
		return "must hold no control character"
	case strings.HasPrefix(f, "/"):
		return "must be a relative path inside the repository"
	case slices.Contains(strings.Split(f, "/"), ".."):
		return "must not hold a .. segment: the context is read inside the repository it names"
	case path.Clean(f) != strings.TrimSuffix(f, "/"):
		return "must be a clean path: no // and no . segment"
	}
	return ""
}

// buildStepCwd is the working directory of the template's build step — its own
// dir:, or the worker's default when it names none — which is the directory
// docker resolves the context argument against. It is read from the build step
// the request carries, so the step that checks the context cannot be told a
// different directory than the step that builds it.
func buildStepCwd(build *cloudbuild.BuildStep) string {
	switch {
	case build.Dir == "":
		return "/workspace"
	case path.IsAbs(build.Dir):
		return build.Dir
	}
	return "/workspace/" + build.Dir
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
// not two readings of a clock. context_commit and context_sha256 are the
// context's own identity, and they are new names rather than a reuse of
// source_commit (D1400): that field means the commit the copied Dockerfile
// was taken from, and a reader that already knows it — the console, the
// mobile app, the history — would draw a wrong figure from a context's
// commit under the same name. Both are "" for an entry that names no
// context, and the check reads "" as a record that says nothing about a
// context rather than as a context that has not moved.
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
//
// One step writes the record of both kinds of catalog entry, because one
// shape and one reader have to agree: a smoke-only run is submitted with the
// definition, the context and the base empty (checkPrebuilt refuses a spec
// that names any of them), so the same env and the same printf carry the
// entry's own reference in and leave those fields empty — nothing was read and
// nothing was built from anything — and the image digest it records is the one
// the smoke resolved. The marker PREBUILT is what tells the script's own run
// apart: only then is there no base digest to read. Everything else — the key,
// the field names, the unescaped printf, the encoding and the upload — is the
// same for both.
func catalogRecordStep(s CatalogBuildSpec) *cloudbuild.BuildStep {
	key, _ := CatalogRecordKey(s.Slug, s.Entry) // checked before this is called
	env := []string{
		"BUCKET=" + s.Bucket, "KEY=" + key, "ENTRY=" + s.Entry, "IMAGE=" + cmp.Or(s.Prebuilt, s.Image),
		"GENERATION=" + strconv.FormatInt(s.Definition.Generation, 10),
		"SHA256=" + s.Definition.SHA256, "COMMIT=" + s.Definition.SourceCommit,
		"CONTEXT_COMMIT=" + s.ContextCommit, "CONTEXT_SHA256=" + s.ContextSHA256,
		"TEMPLATE_SALT=" + s.TemplateSalt,
		"EPOCH=" + strconv.Itoa(s.Epoch),
		"BASE=" + s.Base, "TAG=${_CATALOG_TAG}",
	}
	// A build's own record step has exactly the variables it had before a
	// prebuilt entry had one: the marker is added to the smoke-only run's step
	// and read as an unset variable by the other.
	if s.Prebuilt != "" {
		env = append(env, "PREBUILT=1")
	}
	return &cloudbuild.BuildStep{
		Id:         catalogRecordStepID,
		Name:       "gcr.io/google.com/cloudsdktool/cloud-sdk:slim",
		Entrypoint: "bash",
		Env:        env,
		Args: []string{"-c", `set -eu
image_digest=$$(cat /workspace/out/image-digest)
case "$$image_digest" in sha256:*) ;; *) echo "no pushed digest" >&2; exit 1 ;; esac
built_at=$$(cat /workspace/out/built-at)
# A smoke-only run has no base to read: its record carries the same fifteen
# keys, and the fields only a build has are the empty ones its own env states.
base_digest=
if [ -z "$${PREBUILT:-}" ]; then
  base_digest=$$(cat /workspace/base-digest)
  base_digest=$${base_digest#*@}
  case "$$base_digest" in sha256:*) ;; *) echo "no base digest" >&2; exit 1 ;; esac
fi
record=$$(mktemp)
printf '{"version":1,"entry":"%s","source_commit":"%s","context_commit":"%s","context_sha256":"%s","definition_generation":%s,"definition_sha256":"%s","template_salt":"%s","epoch":%s,"base_ref":"%s","base_digest":"%s","image":"%s","content_tag":"%s","image_digest":"%s","build_id":"%s","built_at":"%s"}' \
  "$$ENTRY" "$$COMMIT" "$$CONTEXT_COMMIT" "$$CONTEXT_SHA256" "$$GENERATION" "$$SHA256" "$$TEMPLATE_SALT" "$$EPOCH" "$$BASE" "$$base_digest" "$$IMAGE" "$$TAG" "$$image_digest" "$BUILD_ID" "$$built_at" > "$$record"
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
// the build of project.
//
// It takes no set of reserved variables, where the derived build's own check
// does: the only variables a catalog build used to name were the repository's
// workflow secrets, and no catalog build names one any more (B9, D2311).
func (s CatalogBuildSpec) check(project string) error {
	for _, f := range []struct{ name, v string }{
		{"catalog entry", s.Entry}, {"repository slug", s.Slug}, {"repository URL", s.RepoURL},
		{"base branch", s.BaseBranch}, {"base image", s.Base}, {"image", s.Image},
		{"git credential secret", s.GitSecretID}, {"build identity", s.ServiceAccount},
		{"runs bucket", s.Bucket}, {"credentials service", s.CredentialsServiceURL},
	} {
		if f.v == "" {
			return fmt.Errorf("the Cloud Build request has no %s", f.name)
		}
	}
	// The credential step asks the installation's service for this build's git
	// credential, so the address it is given is the one the step's own identity
	// token is minted for and reaches no other host: it is the service's URL and
	// nothing else, the shape the deployment writes and the client refuses to
	// leave (internal/cli's checkBuildServiceURL, the same rule at the step).
	if err := checkServiceURL(s.CredentialsServiceURL); err != nil {
		return err
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
	// An entry's context is three values that come together or not at all: a
	// context nothing pinned would check nothing out, and a commit or a
	// digest without a context is an identity for a tree the build never
	// reads. They are written into a record this build composes without
	// escaping, so each is checked for shape here rather than in the step.
	if (s.Context == "") != (s.ContextCommit == "" && s.ContextSHA256 == "") {
		return fmt.Errorf("the Cloud Build request has a context, a context commit and a context sum that do not come together: context %q, commit %q, sum %q", s.Context, s.ContextCommit, s.ContextSHA256)
	}
	if s.Context != "" {
		if msg := contextPathMessage(s.Context); msg != "" {
			return fmt.Errorf("the entry's context %q %s", s.Context, msg)
		}
	}
	if s.Context != "" && !catalogCommitRE.MatchString(s.ContextCommit) {
		return fmt.Errorf("the context's commit %q is not a commit SHA", s.ContextCommit)
	}
	if s.Context != "" && !sha256HexRE.MatchString(s.ContextSHA256) {
		return fmt.Errorf("the context's sum %q is not 64 hex digits", s.ContextSHA256)
	}
	// The template salt is the one other value the record carries, and it is
	// written into that same unescaped printf: a submitter states a salt this
	// package computed or states none, and anything else is refused rather
	// than written.
	if s.TemplateSalt != "" && !sha256HexRE.MatchString(s.TemplateSalt) {
		return fmt.Errorf("the template salt %q is not 64 hex digits", s.TemplateSalt)
	}
	if err := s.checkSmokeSpec(); err != nil {
		return err
	}
	// The build runs as the project's build identity (B6, D2301), which holds
	// the shared registry's writer and tag-mover roles; any other account (a
	// repository's, a job's, the retired shared one, another project's
	// identity) is refused, because the installation grants those roles to
	// this one account and to nothing else.
	if want := BuildIdentityEmail(project); s.ServiceAccount != want {
		return fmt.Errorf("the build service account %s is not the project's build identity %s", s.ServiceAccount, want)
	}
	// Only the entry's own image in the installation's shared registry of
	// this project, which this build writes.
	host, _, ok := strings.Cut(s.Image, "/"+SharedRegistryRepoID()+"/")
	m := registryHostRE.FindStringSubmatch(host)
	if !ok || m == nil || m[2] != project || s.Image != host+"/"+SharedRegistryRepoID()+"/catalog-"+s.Entry {
		return fmt.Errorf("the image %s is not the catalog image of entry %s in the installation's shared registry of project %s", s.Image, s.Entry, project)
	}
	return nil
}

// checkSmokeSpec refuses a smoke spec that is not JSON, and one that asks for
// no tools: a catalog image may be built FROM any base (D1151), so its smoke
// is the only thing that can assert the runtime contract there, and a spec
// asking for no tools would assert the least exactly where the base
// guarantees the least. The critical tier is the contract list, and the
// runtime contract is reported with it — a prebuilt entry's smoke asks for
// the same tier, and that smoke is its whole check.
//
// The key is read here rather than through image.SelftestSpec: internal/image
// reaches this package (through imagecheck), so the spec cannot be imported
// back, and the JSON key is what the caller that renders the spec and this
// request agree on.
func (s CatalogBuildSpec) checkSmokeSpec() error {
	if len(s.Selftest) == 0 || !json.Valid(s.Selftest) {
		return fmt.Errorf("the smoke's spec is not JSON; a catalog build renders nothing, so the caller sends it")
	}
	var smoke struct {
		Tools string `json:"tools"`
	}
	if err := json.Unmarshal(s.Selftest, &smoke); err != nil {
		return fmt.Errorf("the smoke's spec: %w", err)
	}
	if smoke.Tools != "critical" && smoke.Tools != "all" {
		return fmt.Errorf("the smoke's spec asks for no tools (tools: %q), so the catalog image would be smoked without the runtime contract; a catalog image may be built from any base", smoke.Tools)
	}
	return nil
}

// checkPrebuilt refuses a spec whose run smokes a prebuilt reference
// (Prebuilt) the way check refuses a build's, and refuses the rest of what a
// build is: a smoke-only run that also named a base, a shared-registry image,
// a copied definition or a context would be a build whose steps ignore them,
// since no step of this request could read or push any of it.
//
// It repeats the entry-level checks rather than sharing check's, because
// check's are interleaved with a build's own in the order its errors are
// reported in. What it asks for is only what this run reads: the entry (its
// record's own prefix), the repository whose record it writes, the project's
// build identity it runs as and the runs bucket. No clone URL, no base
// branch, no credential secret and no provider — nothing here clones, reads
// or pushes anything.
func (s CatalogBuildSpec) checkPrebuilt(project string) error {
	for _, f := range []struct{ name, v string }{
		{"catalog entry", s.Entry}, {"repository slug", s.Slug},
		{"build identity", s.ServiceAccount}, {"runs bucket", s.Bucket},
	} {
		if f.v == "" {
			return fmt.Errorf("the Cloud Build request has no %s", f.name)
		}
	}
	if _, err := config.CatalogDockerfileKey(s.Entry); err != nil {
		return err
	}
	if !bucketURLRE.MatchString(s.Bucket) {
		return fmt.Errorf("the runs bucket %q is not gs://<bucket>; the smoke's record is written there", s.Bucket)
	}
	if err := s.checkSmokeSpec(); err != nil {
		return err
	}
	if want := BuildIdentityEmail(project); s.ServiceAccount != want {
		return fmt.Errorf("the build service account %s is not the project's build identity %s", s.ServiceAccount, want)
	}
	// The record step composes its JSON with printf and no escaping, and the
	// entry's reference is one of the two values it carries: it is checked
	// against the same grammar a base is, so a quote, a backslash, a newline
	// or any other control character in it never reaches that record.
	if !imageRefRE.MatchString(s.Prebuilt) {
		return fmt.Errorf("the prebuilt image %q is not a container image reference such as ghcr.io/owner/image:1.2", s.Prebuilt)
	}
	// The smoke-only run writes the same record through the same unescaped
	// printf, so its salt is constrained here as a build's is.
	if s.TemplateSalt != "" && !sha256HexRE.MatchString(s.TemplateSalt) {
		return fmt.Errorf("the template salt %q is not 64 hex digits", s.TemplateSalt)
	}
	if s.Base != "" || s.Image != "" || s.Context != "" || s.Definition != (CatalogDefinition{}) {
		return fmt.Errorf("the entry's run smokes the prebuilt image %s and builds nothing, yet the request names a build's inputs: base %q, image %q, context %q, definition %+v", s.Prebuilt, s.Base, s.Image, s.Context, s.Definition)
	}
	if s.NoSmoke {
		return fmt.Errorf("the entry's run is the smoke of the prebuilt image %s and nothing else, so there is nothing for --no-smoke to leave out", s.Prebuilt)
	}
	return nil
}
