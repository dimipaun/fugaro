package gcp

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	artifactregistry "google.golang.org/api/artifactregistry/v1"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
	"google.golang.org/api/googleapi"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// BuildSpec is one derived-image build of (repository, workflow).
type BuildSpec struct {
	Slug        string // the repository's storage slug, for its workflow secrets' IDs
	GitProvider string // gitprov.KindBitbucket or KindGitHub; RepoURL must be on its host
	RepoURL     string // https clone URL without credentials, on GitProvider's host
	BaseBranch  string
	Workflow    string
	Base        string // the fugaro base image, by tag or digest
	// Image is the untagged image in the repository's own registry:
	// ImageName(<region>-docker.pkg.dev/<project>/RegistryRepoID(Slug), Slug, Workflow).
	Image string
	// GitSecretID is the provider credential's secret ID: SecretID(slug,
	// "bitbucket-token"), or SecretID(slug, "github-app-key") for GitHub.
	GitSecretID string
	GitUser     string // Bitbucket: the token's https username, such as x-token-auth
	GitHubAppID string // GitHub: the App's ID, which is not a secret
	// ServiceAccount is the email of the repository's build account
	// (BuildServiceAccountID), which the build runs as.
	ServiceAccount string
	MachineType    string
	// WorkflowSecrets become env BuildKit secrets of the build step.
	WorkflowSecrets []config.Secret
	// Bucket is the runs bucket as gs://<name>, where the build's record
	// step writes builds/<Slug>/<Workflow>/image.json.
	Bucket string
	// NoSmoke leaves out the candidate's smoke test (fugaro image build
	// --no-smoke). The daily check never sets it.
	NoSmoke bool
}

// BuildResult is a submitted or finished Cloud Build build.
type BuildResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Image is the image's latest tag, or, for a superseded build, the
	// image without a tag.
	Image  string `json:"image"`
	Digest string `json:"digest,omitempty"`
	LogURL string `json:"log_url,omitempty"`
	// Superseded means the build found a newer record: it built and
	// smoke-tested Digest but left latest (and the record) as they were.
	Superseded bool `json:"superseded,omitempty"`
}

// TemplateSalt is the hex SHA-256 of the embedded cloudbuild.yaml and the
// fugaro version: a build record carries it, so a change to either
// shows as a changed build.
func TemplateSalt(version string) string {
	h := sha256.New()
	h.Write(images.CloudBuild)
	h.Write([]byte("\x00" + version))
	return hex.EncodeToString(h.Sum(nil))
}

// ErrBadBuildSpec wraps BuildRequest's (and so Submit's) refusal of an
// incomplete or unsafe BuildSpec, before any call is made.
var ErrBadBuildSpec = errors.New("invalid Cloud Build request")

var (
	// secretEnvRE is what a workflow secret's variable may be; the build
	// step re-checks it before using the name in --secret.
	secretEnvRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	appIDRE     = regexp.MustCompile(`^[0-9]{1,20}$`)
	// bucketURLRE is a GCS bucket as a gocloud URL, with no path.
	bucketURLRE = regexp.MustCompile(`^gs://[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$`)
	// digestRE is an image digest.
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// registryHostRE is <region>-docker.pkg.dev/<project>.
	registryHostRE = regexp.MustCompile(`^([a-z]+-[a-z]+[0-9]+)-docker\.pkg\.dev/([a-z][a-z0-9-]{4,28}[a-z0-9])$`)
)

// The credential step's provider secret, as the variable it reads, and the
// env it gets besides: as cloudbuild.yaml writes it (Bitbucket), and as
// BuildRequest rewrites it for GitHub.
const (
	gitTokenEnv     = "GIT_TOKEN"
	githubAppKeyEnv = "GITHUB_APP_KEY"
	credentialStep  = "credential"
)

var credentialEnv = map[string][]string{
	gitprov.KindBitbucket: {"PROVIDER=" + gitprov.KindBitbucket, "REPO_URL=${_REPO_URL}", "GIT_USER=${_GIT_USER}"},
	gitprov.KindGitHub:    {"PROVIDER=" + gitprov.KindGitHub, "REPO_URL=${_REPO_URL}", "GITHUB_APP_ID=${_GITHUB_APP_ID}"},
}

var credentialSecretEnv = map[string]string{gitprov.KindBitbucket: gitTokenEnv, gitprov.KindGitHub: githubAppKeyEnv}

// BuildRequest is the Cloud Build request for s in project: images.CloudBuild
// with its substitutions set, the credential step's secret and env chosen
// by provider, the provider credential's version in availableSecrets, and
// each workflow secret added to availableSecrets and to the build step's
// secretEnv (and nowhere else). It always names the repository's build
// account: a request without one would run as the project's default build
// identity. It makes no calls.
func BuildRequest(project string, s BuildSpec) (*cloudbuild.Build, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(images.CloudBuild, &raw); err != nil {
		return nil, fmt.Errorf("parsing the embedded cloudbuild.yaml: %w", err)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("converting the embedded cloudbuild.yaml: %w", err)
	}
	var b cloudbuild.Build
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("converting the embedded cloudbuild.yaml: %w", err)
	}
	if b.AvailableSecrets == nil || len(b.AvailableSecrets.SecretManager) != 1 || b.AvailableSecrets.SecretManager[0].Env != gitTokenEnv {
		return nil, errors.New("the embedded cloudbuild.yaml does not declare exactly the git token secret")
	}
	var build, cred *cloudbuild.BuildStep
	for _, st := range b.Steps {
		switch st.Id {
		case "build":
			build = st
		case credentialStep:
			cred = st
		}
	}
	if build == nil || cred == nil || !slices.Equal(cred.SecretEnv, []string{gitTokenEnv}) || !slices.Equal(cred.Env, credentialEnv[gitprov.KindBitbucket]) {
		return nil, errors.New("the embedded cloudbuild.yaml has no build step, or no credential step in its Bitbucket form")
	}
	if err := s.check(project, reservedEnv(build)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}
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
		"_WORKFLOW":    s.Workflow,
		"_FUGARO_BASE": s.Base,
		"_IMAGE":       s.Image,
		"_SECRET_ENVS": strings.Join(envs, " "),
		"_BUCKET":      s.Bucket,
		"_SLUG":        s.Slug,
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
	return &b, nil
}

// reservedEnv is every variable a workflow secret may not reuse: the build
// step's own env: and secretEnv:, and the credential step's in either
// provider's form, whose secret shares availableSecrets with the workflow
// secrets. The variables bash, the docker CLI and the step image read are
// in config.ReservedEnv, which check applies too.
func reservedEnv(build *cloudbuild.BuildStep) map[string]bool {
	used := map[string]bool{}
	for _, envs := range append([][]string{build.Env, build.SecretEnv}, slices.Collect(maps.Values(credentialEnv))...) {
		for _, e := range envs {
			name, _, _ := strings.Cut(e, "=")
			used[name] = true
		}
	}
	for _, e := range credentialSecretEnv {
		used[e] = true
	}
	return used
}

// CheckRepoURL refuses a clone URL the provider's credential must not be
// sent to: anything but an https URL without credentials or a port on the
// provider's own host. The build's clone and the credential step both
// apply it.
func CheckRepoURL(provider, repoURL string) error {
	u, err := url.Parse(repoURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("the repository URL %s is not an https URL without credentials", RedactURL(repoURL))
	}
	// The clone sends the provider credential to the URL's host, and the
	// credential is scoped to the provider's: any other host (or port) is
	// refused.
	if provider == "" || u.Port() != "" || gitprov.KindForURL(repoURL) != provider {
		return fmt.Errorf("the repository URL %s is not on %s's host; the build would send its credential there", RedactURL(repoURL), cmp.Or(provider, "the git provider"))
	}
	return nil
}

// check refuses a spec that is incomplete or would put an unsafe value in
// the build of project. reserved are the variables a workflow secret may
// not reuse.
func (s BuildSpec) check(project string, reserved map[string]bool) error {
	for _, f := range []struct{ name, v string }{
		{"repository slug", s.Slug}, {"repository URL", s.RepoURL}, {"base branch", s.BaseBranch},
		{"workflow", s.Workflow}, {"base image", s.Base}, {"image", s.Image}, {"git credential secret", s.GitSecretID},
		{"build service account", s.ServiceAccount}, {"runs bucket", s.Bucket},
	} {
		if f.v == "" {
			return fmt.Errorf("the Cloud Build request has no %s", f.name)
		}
	}
	switch s.GitProvider {
	case gitprov.KindBitbucket:
		if s.GitUser == "" {
			return errors.New("the Cloud Build request has no git user")
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
		return fmt.Errorf("the runs bucket %q is not gs://<bucket>; the build records its image there", s.Bucket)
	}
	// The build account holds the repository's secrets and writes its
	// registry; any other account (the retired shared one, another
	// repository's, a job's) is refused.
	if want := BuildServiceAccountID(s.Slug) + "@" + project + ".iam.gserviceaccount.com"; s.ServiceAccount != want {
		return fmt.Errorf("the build service account %s is not the repository's build account %s", s.ServiceAccount, want)
	}
	// Only the repository's own registry, in this project, which only its
	// build account writes.
	regID := "/" + RegistryRepoID(s.Slug) + "/"
	host, _, ok := strings.Cut(s.Image, regID)
	if m := registryHostRE.FindStringSubmatch(host); !ok || m == nil || m[2] != project ||
		s.Image != ImageName(host+strings.TrimSuffix(regID, "/"), s.Slug, s.Workflow) {
		return fmt.Errorf("the image %s is not the workflow's image in the repository's own registry of project %s", s.Image, project)
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

// Builder submits derived-image builds to Cloud Build in one region, and
// checks the registries they push to.
type Builder struct {
	svc             *cloudbuild.Service
	registries      *artifactregistry.Service
	project, region string
}

// NewBuilder connects to Cloud Build for builds in region, and to Artifact
// Registry (see registryEndpoint).
func NewBuilder(ctx context.Context, o Options, region string) (*Builder, error) {
	svc, err := cloudbuild.NewService(ctx, o.client(o.Endpoints.CloudBuild)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Build: %w", err)
	}
	ar, err := artifactregistry.NewService(ctx, o.client(registryEndpoint(o.Endpoints))...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Artifact Registry: %w", err)
	}
	return &Builder{svc: svc, registries: ar, project: o.GCPProject, region: region}, nil
}

func (b *Builder) parent() string { return "projects/" + b.project + "/locations/" + b.region }

// registryEndpoint is where Artifact Registry lookups go: Google's own
// endpoint, except with a fake (NoAuth), whose Cloud Build endpoint serves
// Artifact Registry's repositories get too (the paths don't overlap). A
// real Cloud Build override, such as a regional one, serves no Artifact
// Registry, and a lookup there would wrongly report every registry missing.
func registryEndpoint(e Endpoints) string {
	if e.NoAuth {
		return e.CloudBuild
	}
	return ""
}

// ErrRegistryUnchecked means the caller may not read the registry (a 403):
// it may still hold what a build needs, so this is no answer either way.
var ErrRegistryUnchecked = errors.New("not allowed to read the registry")

// RegistryExists reports whether the Docker registry registry
// (<region>-docker.pkg.dev/<project>/<repository ID>, of the builder's
// project) exists, with one repositories get. A build pushing to a missing
// one would only fail at its end. A 403 is ErrRegistryUnchecked.
func (b *Builder) RegistryExists(ctx context.Context, registry string) (bool, error) {
	host, id, _ := strings.Cut(registry, "/"+b.project+"/")
	m := registryHostRE.FindStringSubmatch(host + "/" + b.project)
	if m == nil || id == "" || strings.Contains(id, "/") {
		return false, fmt.Errorf("the registry %s is not <region>-docker.pkg.dev/%s/<repository>", registry, b.project)
	}
	name := "projects/" + b.project + "/locations/" + m[1] + "/repositories/" + id
	_, err := b.registries.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	var ae *googleapi.Error
	switch {
	case errors.As(err, &ae) && ae.Code == http.StatusNotFound:
		return false, nil
	case errors.As(err, &ae) && ae.Code == http.StatusForbidden:
		return false, fmt.Errorf("reading the Artifact Registry repository %s: %w: %w", name, ErrRegistryUnchecked, err)
	case err != nil:
		return false, fmt.Errorf("reading the Artifact Registry repository %s: %w", name, err)
	}
	return true, nil
}

// ErrBuildNotFound means Cloud Build has no such build (in the builder's
// region).
var ErrBuildNotFound = errors.New("no such Cloud Build build")

// Status reads build id's status (QUEUED, WORKING, SUCCESS, FAILURE, …)
// with one builds get, without waiting. A missing build is
// ErrBuildNotFound.
func (b *Builder) Status(ctx context.Context, id string) (string, error) {
	if !buildIDRE.MatchString(id) {
		return "", fmt.Errorf("%q is not a Cloud Build build ID", id)
	}
	bd, err := b.svc.Projects.Locations.Builds.Get(b.parent() + "/builds/" + id).Context(ctx).Do()
	var ae *googleapi.Error
	switch {
	case errors.As(err, &ae) && ae.Code == http.StatusNotFound:
		return "", fmt.Errorf("Cloud Build build %s: %w", id, ErrBuildNotFound)
	case err != nil:
		return "", fmt.Errorf("reading the status of Cloud Build build %s: %w", id, err)
	}
	return bd.Status, nil
}

// buildIDRE is what a Cloud Build build ID may be: it goes into a path.
var buildIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// IsDigest reports whether s is an image digest (sha256:<64 hex>).
func IsDigest(s string) bool { return digestRE.MatchString(s) }

// Submit starts the build of s and returns it as queued.
func (b *Builder) Submit(ctx context.Context, s BuildSpec) (BuildResult, error) {
	req, err := BuildRequest(b.project, s)
	if err != nil {
		return BuildResult{}, err
	}
	op, err := b.svc.Projects.Locations.Builds.Create(b.parent(), req).Context(ctx).Do()
	if err != nil {
		return BuildResult{}, fmt.Errorf("submitting the Cloud Build build of %s: %w", s.Image, err)
	}
	var md cloudbuild.BuildOperationMetadata
	if err := json.Unmarshal(op.Metadata, &md); err != nil || md.Build == nil || md.Build.Id == "" {
		return BuildResult{}, fmt.Errorf("Cloud Build accepted the build of %s but returned no build ID (operation %s)", s.Image, op.Name)
	}
	return BuildResult{ID: md.Build.Id, Status: md.Build.Status, Image: s.Image + ":latest", LogURL: md.Build.LogUrl}, nil
}

// waitRetries and waitRetryBase bound Wait's retries of a failed status
// read: up to waitRetries in a row, backing off from waitRetryBase and
// doubling to at most 30s (about a minute in all). Tests shrink the base.
var (
	waitRetries   = 6
	waitRetryBase = time.Second
)

// retryable reports whether a failed status read is worth repeating: a
// 5xx, 429 or 408 from the API, or a transport error (anything that is not
// an API answer), but not the caller's own cancellation.
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var ae *googleapi.Error
	if errors.As(err, &ae) {
		return ae.Code >= 500 || ae.Code == http.StatusTooManyRequests || ae.Code == http.StatusRequestTimeout
	}
	return true
}

// terminal are the build statuses Wait stops at.
var terminal = []string{"SUCCESS", "FAILURE", "INTERNAL_ERROR", "TIMEOUT", "CANCELLED", "EXPIRED"}

// stepOutputs reads a finished build's step outputs: the build step's is
// the candidate's pushed digest, and promote's says "superseded" when the
// gate found a newer record, so latest stayed. ok is false without a
// digest.
func stepOutputs(bd *cloudbuild.Build) (digest string, superseded, ok bool) {
	if bd.Results == nil {
		return "", false, false
	}
	outs := bd.Results.BuildStepOutputs
	for i, st := range bd.Steps {
		if i >= len(outs) || st == nil {
			break
		}
		data, err := base64.StdEncoding.DecodeString(outs[i])
		if err != nil {
			continue
		}
		switch v := strings.TrimSpace(string(data)); st.Id {
		case "build":
			if digestRE.MatchString(v) {
				digest = v
			}
		case "promote":
			superseded = v == "superseded"
		}
	}
	return digest, superseded, digest != ""
}

// Wait polls build id every poll (zero means 10s) until it finishes. A
// build that finishes other than SUCCESS comes back with an error naming
// its status and log URL. Image and Digest are set only on SUCCESS, from
// the build step's output (the pushed candidate's digest), or else from
// the build's pushed-image results (a SUCCESS without either leaves Digest
// empty). A transient failure to read the status is retried (see
// waitRetries); when Wait gives up, the build may still be running, and
// the error says so.
func (b *Builder) Wait(ctx context.Context, id string, poll time.Duration) (BuildResult, error) {
	if poll == 0 {
		poll = 10 * time.Second
	}
	name := b.parent() + "/builds/" + id
	last := BuildResult{ID: id}
	failures := 0
	for {
		bd, err := b.svc.Projects.Locations.Builds.Get(name).Context(ctx).Do()
		if err != nil {
			failures++
			if !retryable(ctx, err) || failures > waitRetries {
				where := ""
				if last.LogURL != "" {
					where = "; its log is at " + last.LogURL
				}
				return last, fmt.Errorf("reading the status of Cloud Build build %s failed (%d attempts); the build may still be running%s: %w", id, failures, where, err)
			}
			wait := min(waitRetryBase<<(failures-1), 30*time.Second)
			select {
			case <-ctx.Done():
				return last, fmt.Errorf("waiting for Cloud Build build %s, which may still be running: %w", id, ctx.Err())
			case <-time.After(wait):
			}
			continue
		}
		failures = 0
		res := BuildResult{ID: id, Status: bd.Status, LogURL: bd.LogUrl}
		last = res
		if slices.Contains(terminal, bd.Status) {
			if bd.Status != "SUCCESS" {
				return res, fmt.Errorf("Cloud Build build %s ended %s; its log is at %s", id, bd.Status, bd.LogUrl)
			}
			if digest, superseded, ok := stepOutputs(bd); ok {
				res.Digest, res.Superseded = digest, superseded
				res.Image = bd.Substitutions["_IMAGE"]
				if !superseded {
					res.Image += ":latest"
				}
			} else if bd.Results != nil && len(bd.Results.Images) > 0 {
				res.Image, res.Digest = bd.Results.Images[0].Name, bd.Results.Images[0].Digest
			}
			return res, nil
		}
		select {
		case <-ctx.Done():
			return res, fmt.Errorf("waiting for Cloud Build build %s, which may still be running: %w", id, ctx.Err())
		case <-time.After(poll):
		}
	}
}
