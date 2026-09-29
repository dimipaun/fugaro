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

// Missing is something a workflow's job needs that doesn't exist yet.
type Missing struct {
	Workflow string
	Reason   string
	// Deployed means the job exists and stays deployed anyway, so this is
	// a warning; otherwise the job isn't deployed until it is fixed.
	Deployed bool
}

func (m Missing) String() string {
	if m.Deployed {
		return fmt.Sprintf("workflow %s (its job exists and stays deployed): %s", m.Workflow, m.Reason)
	}
	return fmt.Sprintf("workflow %s (its job is not deployed yet): %s", m.Workflow, m.Reason)
}

// errFound stops a listing at its first match.
var errFound = errors.New("found")

// Readiness decides, from what exists, what the plan may deploy. It
// returns spec with the gates applied (spec itself is not changed) and
// what is still missing:
//   - A workflow's job is deployed when it already exists (a job is never
//     removed), or when its image's latest tag exists in the repository's
//     registry and every secret it mounts has an enabled version.
//   - An existing job keeps its current image until the spec's image has
//     a latest tag, so no apply points a job at a missing image.
//   - A job account keeps the display name discovery found (ex).
//   - The daily check's schedule stays paused until every workflow it
//     covers has a build record, so no unattended build starts for an
//     image nobody has built on purpose.
func Readiness(ctx context.Context, c *Clients, spec RepoSpec, ex Existing) (RepoSpec, []Missing, error) {
	out := spec
	out.Workflows = maps.Clone(spec.Workflows)
	versions := map[string]bool{} // logical secret → has an enabled version
	var missing []Missing
	for _, name := range slices.Sorted(maps.Keys(out.Workflows)) {
		ws := out.Workflows[name]
		if dn, ok := ex.DisplayNames[name]; ok {
			ws.ServiceAccount.DisplayName = dn
		}
		var reasons []string
		for _, logical := range uniqueSecrets(ws) {
			ok, seen := versions[logical]
			if !seen {
				var err error
				if ok, err = hasEnabledVersion(ctx, c, spec.Project, spec.Secrets[logical]); err != nil {
					return RepoSpec{}, nil, err
				}
				versions[logical] = ok
			}
			if !ok {
				reasons = append(reasons, fmt.Sprintf("secret %s has no version: fugaro secrets set %s --repo %s", logical, logical, spec.Name))
			}
		}
		built, err := hasLatest(ctx, c, spec, ws.Image)
		if err != nil {
			return RepoSpec{}, nil, err
		}
		if !built {
			reasons = append(reasons, fmt.Sprintf("image not built yet: fugaro image build --repo %s --workflow %s", spec.Name, name))
		}
		current, exists := ex.Jobs[name]
		switch {
		case exists:
			ws.DeployJob = true
			if !built && current != "" {
				ws.Image = current
			}
		default:
			ws.DeployJob = len(reasons) == 0
		}
		for _, r := range reasons {
			missing = append(missing, Missing{Workflow: name, Reason: r, Deployed: exists})
		}
		out.Workflows[name] = ws
	}
	if spec.Check != nil {
		check := *spec.Check
		check.Paused = false
		for _, name := range check.Workflows {
			ok, err := hasRecord(ctx, c, spec.Installation.RunsBucket, imagecheck.RecordKey(spec.Slug, name))
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if !ok {
				check.Paused = true
			}
		}
		out.Check = &check
	}
	return out, missing, nil
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
