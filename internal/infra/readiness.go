package infra

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// MissingKind says what a readiness gate is waiting for.
type MissingKind string

// The kinds of gate.
const (
	// MissingSecret is a secret the job mounts that has no version.
	MissingSecret MissingKind = "secret"
	// MissingImage is the job's image, not built yet.
	MissingImage MissingKind = "image"
)

// Missing is something a workflow's job needs that doesn't exist yet.
type Missing struct {
	Workflow string
	Kind     MissingKind
	Reason   string
	// Deployed means the job exists and stays deployed anyway, so this is
	// a warning; otherwise the job isn't deployed until it is fixed.
	Deployed bool
	// Check means the job is the daily image check's, not Workflow's.
	Check bool
}

func (m Missing) String() string {
	if m.Check {
		if m.Deployed {
			return "the daily image check (its job exists and stays deployed): " + m.Reason
		}
		return "the daily image check (its job, invoker grant and schedule are not deployed yet): " + m.Reason
	}
	if m.Deployed {
		return fmt.Sprintf("workflow %s (its job exists and stays deployed): %s", m.Workflow, m.Reason)
	}
	return fmt.Sprintf("workflow %s (its job is not deployed yet): %s", m.Workflow, m.Reason)
}

// errFound stops a listing at its first match.
var errFound = errors.New("found")

// Readiness decides, from what exists, what the plan may deploy. It
// returns a deep copy of spec with the gates applied (spec itself is not
// changed, and shares no map or slice with the result) and what is still
// missing:
//   - A workflow's job is deployed when it already exists (a job is never
//     removed), or when its image's latest tag exists in the repository's
//     registry and every secret it mounts has an enabled version.
//   - An existing job keeps its current image until the spec's image has
//     a latest tag, so no apply points a job at a missing image. A job
//     with no image and an unbuilt spec image is refused (a user error).
//   - A job account keeps the display name discovery found (ex).
//   - The daily check's job, its invoker grant and its Scheduler job are
//     deployed when the check job already exists, or when every secret it
//     mounts (the provider credential) has an enabled version: Cloud Run
//     checks a job's secrets when it creates it.
//   - The daily check's schedule stays paused until every workflow it
//     covers has a build record, so no unattended build starts for an
//     image nobody has built on purpose.
func Readiness(ctx context.Context, c *Clients, spec RepoSpec, ex Existing) (RepoSpec, []Missing, error) {
	out := cloneRepoSpec(spec)
	versions := map[string]bool{} // logical secret → has an enabled version
	var missing []Missing
	hasVersion := func(logical string) (bool, error) {
		ok, seen := versions[logical]
		if !seen {
			var err error
			if ok, err = hasEnabledVersion(ctx, c, spec.Project, spec.Secrets[logical]); err != nil {
				return false, err
			}
			versions[logical] = ok
		}
		return ok, nil
	}
	for _, name := range slices.Sorted(maps.Keys(out.Workflows)) {
		ws := out.Workflows[name]
		if dn, ok := ex.DisplayNames[name]; ok {
			ws.ServiceAccount.DisplayName = dn
		}
		type gate struct {
			kind   MissingKind
			reason string
		}
		var reasons []gate
		for _, logical := range uniqueSecrets(ws) {
			ok, err := hasVersion(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if !ok {
				reasons = append(reasons, gate{MissingSecret, fmt.Sprintf("secret %s has no version: fugaro secrets set %s --repo %s", logical, logical, spec.Name)})
			}
		}
		built, err := hasLatest(ctx, c, spec, ws.Image)
		if err != nil {
			return RepoSpec{}, nil, err
		}
		if !built {
			reasons = append(reasons, gate{MissingImage, fmt.Sprintf("image not built yet: fugaro image build --repo %s --workflow %s", spec.Name, name)})
		}
		current, exists := ex.Jobs[name]
		switch {
		case exists:
			ws.DeployJob = true
			if !built {
				if current == "" {
					// Nothing live to keep, and the spec's image doesn't
					// exist: either would leave the job without an image.
					return RepoSpec{}, nil, userErr("Cloud Run job %s exists but has no container image, and %s is not built yet; build it (fugaro image build --repo %s --workflow %s) or fix the job, then run fugaro init --repo again",
						ws.Job, ws.Image, spec.Name, name)
				}
				ws.Image = current
			}
		default:
			ws.DeployJob = len(reasons) == 0
		}
		for _, r := range reasons {
			missing = append(missing, Missing{Workflow: name, Kind: r.kind, Reason: r.reason, Deployed: exists})
		}
		out.Workflows[name] = ws
	}
	if spec.Check != nil {
		ready := true
		for _, logical := range slices.Compact(slices.Sorted(maps.Values(out.Check.SecretEnv))) {
			ok, err := hasVersion(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if !ok {
				ready = false
				missing = append(missing, Missing{Check: true, Kind: MissingSecret, Deployed: ex.CheckJob,
					Reason: fmt.Sprintf("secret %s has no version: fugaro secrets set %s --repo %s", logical, logical, spec.Name)})
			}
		}
		out.Check.DeployJob = ready || ex.CheckJob
		// Paused unless there is at least one workflow and each has a
		// record, so an empty list fails safe. The spec's runs bucket is
		// bucketName(lc), the bucket builds record in (lc.RecordBucketURL).
		records := 0
		for _, name := range out.Check.Workflows {
			ok, err := hasRecord(ctx, c, spec.Installation.RunsBucket, imagecheck.RecordKey(spec.Slug, name))
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if ok {
				records++
			}
		}
		out.Check.Paused = records == 0 || records < len(out.Check.Workflows)
	}
	return out, missing, nil
}

// cloneRepoSpec is a deep copy of spec's maps, slices and check, so
// Readiness's output shares nothing a caller could edit with its input.
func cloneRepoSpec(spec RepoSpec) RepoSpec {
	out := spec
	out.Secrets = maps.Clone(spec.Secrets)
	out.BuildSecrets = slices.Clone(spec.BuildSecrets)
	out.Installation.Launchers = slices.Clone(spec.Installation.Launchers)
	out.Installation.Operators = slices.Clone(spec.Installation.Operators)
	out.Workflows = make(map[string]WorkflowSpec, len(spec.Workflows))
	for name, ws := range spec.Workflows {
		ws.Env = maps.Clone(ws.Env)
		ws.SecretEnv = maps.Clone(ws.SecretEnv)
		ws.Labels = maps.Clone(ws.Labels)
		ws.SecretIDs = maps.Clone(ws.SecretIDs)
		ws.BuildSecrets = slices.Clone(ws.BuildSecrets)
		out.Workflows[name] = ws
	}
	if spec.Check != nil {
		check := *spec.Check
		check.Env = maps.Clone(check.Env)
		check.SecretEnv = maps.Clone(check.SecretEnv)
		check.Workflows = slices.Clone(check.Workflows)
		out.Check = &check
	}
	return out
}

// hasEnabledVersion reports whether secret id has an enabled version; a
// missing secret has none.
func hasEnabledVersion(ctx context.Context, c *Clients, project, id string) (bool, error) {
	err := c.Secrets.Projects.Secrets.Versions.List("projects/"+project+"/secrets/"+id).Pages(ctx, func(p *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range p.Versions {
			if v.State == "ENABLED" {
				return errFound
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errFound):
		return true, nil
	case notFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("listing the versions of secret %s: %w", id, err)
	}
	return false, nil
}

// hasLatest reports whether image (<registry path>/<package>:latest, in
// the repository's registry) has its latest tag. gcp.ImageName's package
// is one path component, so it needs no escaping in the tag's name.
func hasLatest(ctx context.Context, c *Clients, spec RepoSpec, image string) (bool, error) {
	rest, ok := strings.CutPrefix(image, spec.RegistryPath+"/")
	pkg, tagged := strings.CutSuffix(rest, ":latest")
	m := registryHostRE.FindStringSubmatch(spec.Installation.RegistryHost)
	if !ok || !tagged || pkg == "" || strings.ContainsAny(pkg, "/:@") || m == nil {
		return false, fmt.Errorf("image %s is not <registry>/<package>:latest in the repository's registry %s", image, spec.RegistryPath)
	}
	name := "projects/" + spec.Project + "/locations/" + m[1] + "/repositories/" + spec.Registry.RepositoryID +
		"/packages/" + pkg + "/tags/latest"
	_, err := c.AR.Projects.Locations.Repositories.Packages.Tags.Get(name).Context(ctx).Do()
	switch {
	case notFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading tag %s: %w", name, err)
	}
	return true, nil
}

// hasRecord reports whether the runs bucket holds object key.
func hasRecord(ctx context.Context, c *Clients, bucket, key string) (bool, error) {
	_, err := c.Storage.Objects.Get(bucket, key).Context(ctx).Do()
	switch {
	case notFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading gs://%s/%s: %w", bucket, key, err)
	}
	return true, nil
}
