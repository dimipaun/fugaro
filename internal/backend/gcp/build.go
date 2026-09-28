package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	cloudbuild "google.golang.org/api/cloudbuild/v1"
	"google.golang.org/api/googleapi"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
)

// BuildSpec is one derived-image build of (repository, workflow).
type BuildSpec struct {
	Slug        string // the repository's storage slug, for its workflow secrets' IDs
	RepoURL     string // https clone URL without credentials
	BaseBranch  string
	Workflow    string
	Base        string // the fugaro base image, by tag or digest
	Image       string // the Artifact Registry image, untagged (ImageName)
	GitSecretID string // the provider token's secret ID (SecretID(slug, "bitbucket-token"))
	GitUser     string // the token's https username, such as x-token-auth
	// ServiceAccount is the email of the service account the build runs as.
	ServiceAccount string
	MachineType    string
	// WorkflowSecrets become env BuildKit secrets of the build step.
	WorkflowSecrets []config.Secret
}

// BuildResult is a submitted or finished Cloud Build build.
type BuildResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Image  string `json:"image"`
	Digest string `json:"digest,omitempty"`
	LogURL string `json:"log_url,omitempty"`
}

// ErrBadBuildSpec wraps BuildRequest's (and so Submit's) refusal of an
// incomplete or unsafe BuildSpec, before any call is made.
var ErrBadBuildSpec = errors.New("invalid Cloud Build request")

// secretEnvRE is what a workflow secret's variable may be; the build step
// re-checks it before using the name in --secret.
var secretEnvRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// gitTokenEnv is the variable the source and build steps read the provider
// token from.
const gitTokenEnv = "GIT_TOKEN"

// BuildRequest is the Cloud Build request for s in project: images.CloudBuild
// with its substitutions set, the provider token's version in
// availableSecrets, and each workflow secret added to availableSecrets and
// to the build step's secretEnv (and nowhere else). It makes no calls.
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
	var build *cloudbuild.BuildStep
	for _, st := range b.Steps {
		if st.Id == "build" {
			build = st
		}
	}
	if build == nil {
		return nil, errors.New("the embedded cloudbuild.yaml has no build step")
	}
	if err := s.check(buildStepEnv(build)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadBuildSpec, err)
	}

	version := func(id string) string { return "projects/" + project + "/secrets/" + id + "/versions/latest" }
	gitVersion := version(s.GitSecretID)
	b.AvailableSecrets.SecretManager[0].VersionName = gitVersion
	envs := make([]string, 0, len(s.WorkflowSecrets))
	for _, ws := range s.WorkflowSecrets {
		b.AvailableSecrets.SecretManager = append(b.AvailableSecrets.SecretManager,
			&cloudbuild.SecretManagerSecret{Env: ws.Env, VersionName: version(SecretID(s.Slug, ws.Name))})
		build.SecretEnv = append(build.SecretEnv, ws.Env)
		envs = append(envs, ws.Env)
	}
	b.Substitutions = map[string]string{
		"_REPO_URL":    s.RepoURL,
		"_BASE_BRANCH": s.BaseBranch,
		"_WORKFLOW":    s.Workflow,
		"_FUGARO_BASE": s.Base,
		"_IMAGE":       s.Image,
		"_GIT_SECRET":  gitVersion,
		"_GIT_USER":    s.GitUser,
		"_SECRET_ENVS": strings.Join(envs, " "),
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

// shellEnv are variables bash, the docker CLI or the step image itself read,
// which a workflow secret must not shadow in the build step; reservedPrefixes
// covers their families.
var (
	shellEnv         = []string{"PATH", "HOME", "PWD", "OLDPWD", "SHELL", "USER", "HOSTNAME", "TMPDIR", "IFS", "LANG", "ENV", "CDPATH", "GLOBIGNORE", "PS4", "SHELLOPTS"}
	reservedPrefixes = []string{"DOCKER_", "BUILDKIT_", "BASH", "LC_"}
)

// buildStepEnv is every variable the build step sets or relies on: its
// env: and secretEnv: from cloudbuild.yaml, plus shellEnv.
func buildStepEnv(step *cloudbuild.BuildStep) map[string]bool {
	used := map[string]bool{}
	for _, e := range step.Env {
		name, _, _ := strings.Cut(e, "=")
		used[name] = true
	}
	for _, e := range step.SecretEnv {
		used[e] = true
	}
	for _, e := range shellEnv {
		used[e] = true
	}
	return used
}

// check refuses a spec that is incomplete or would put an unsafe value in
// the build. reserved are the build step's own variables, which a workflow
// secret may not reuse.
func (s BuildSpec) check(reserved map[string]bool) error {
	for _, f := range []struct{ name, v string }{
		{"repository slug", s.Slug}, {"repository URL", s.RepoURL}, {"base branch", s.BaseBranch},
		{"workflow", s.Workflow}, {"base image", s.Base}, {"image", s.Image}, {"git token secret", s.GitSecretID},
		{"git user", s.GitUser}, {"build service account", s.ServiceAccount},
	} {
		if f.v == "" {
			return fmt.Errorf("the Cloud Build request has no %s", f.name)
		}
	}
	u, err := url.Parse(s.RepoURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("the repository URL %s is not an https URL without credentials", redactURL(s.RepoURL))
	}
	seen := map[string]bool{}
	for _, ws := range s.WorkflowSecrets {
		if !secretEnvRE.MatchString(ws.Env) {
			return fmt.Errorf("workflow secret %s: variable %q is not [A-Z_][A-Z0-9_]*", ws.Name, ws.Env)
		}
		if reserved[ws.Env] || slices.ContainsFunc(reservedPrefixes, func(p string) bool { return strings.HasPrefix(ws.Env, p) }) {
			return fmt.Errorf("workflow secret %s: variable %s is one the image build step sets or uses itself; rename it", ws.Name, ws.Env)
		}
		if seen[ws.Env] {
			return fmt.Errorf("workflow secret %s: variable %s is already used in the build", ws.Name, ws.Env)
		}
		seen[ws.Env] = true
	}
	return nil
}

// redactURL is u without userinfo, for error messages.
func redactURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "<unparseable URL>"
	}
	p.User = nil
	return p.String()
}

// Builder submits derived-image builds to Cloud Build in one region.
type Builder struct {
	svc             *cloudbuild.Service
	project, region string
}

// NewBuilder connects to Cloud Build for builds in region.
func NewBuilder(ctx context.Context, o Options, region string) (*Builder, error) {
	svc, err := cloudbuild.NewService(ctx, o.client(o.Endpoints.CloudBuild)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Build: %w", err)
	}
	return &Builder{svc: svc, project: o.Project, region: region}, nil
}

func (b *Builder) parent() string { return "projects/" + b.project + "/locations/" + b.region }

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

// Wait polls build id every poll (zero means 10s) until it finishes. A
// build that finishes other than SUCCESS comes back with an error naming
// its status and log URL. Image and Digest are set only on SUCCESS, from
// the build's pushed-image results (a SUCCESS without them leaves Digest
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
			if bd.Results != nil && len(bd.Results.Images) > 0 {
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
