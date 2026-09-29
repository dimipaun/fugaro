package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/imagecheck"
)

// slugRE is the shape task.Slug gives: the readable part, then 16 hex
// digits. It is a path component of the workdir and the state prefix.
var slugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?-[0-9a-f]{16}$`)

// RepoWorkdir is where fugaro init --repo plans the repository slug of
// project: $XDG_STATE_HOME/fugaro/terraform/<project>/repos/<slug>, by
// default under ~/.local/state.
func RepoWorkdir(getenv func(string) string, project, slug string) (string, error) {
	if !slugRE.MatchString(slug) {
		return "", fmt.Errorf("%q is not a repository slug", slug)
	}
	inst, err := InstallationWorkdir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(inst), "repos", slug), nil
}

// RepoStatePrefix is where the repository's state lives in the state
// bucket.
func RepoStatePrefix(slug string) string { return StatePrefixRepos + slug }

// RepoRootOptions are the repository root's inputs that come from flags.
type RepoRootOptions struct {
	// AllowJobDelete lowers the jobs' deletion protection, for offboarding.
	AllowJobDelete bool
}

// RepoRootVars is the repository root's terraform.tfvars.json: RepoVars,
// plus allow_job_delete, which is always written so that a run without
// --allow-job-delete puts the protection back.
func RepoRootVars(spec RepoSpec, o RepoRootOptions) ([]byte, error) {
	data, err := RepoVars(spec)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	doc["allow_job_delete"] = o.AllowJobDelete
	return sortedJSON(doc)
}

// InstallationStateExists reports whether the state bucket holds the
// installation's state: fugaro init has applied it there.
func InstallationStateExists(ctx context.Context, c *Clients, stateBucket string) (bool, error) {
	found := false
	prefix := StatePrefixInstallation + "/"
	err := c.Storage.Objects.List(stateBucket).Prefix(prefix).Pages(ctx, func(o *storage.Objects) error {
		if len(o.Items) > 0 {
			found = true
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return false, fmt.Errorf("listing gs://%s/%s: %w", stateBucket, prefix, err)
	}
	return found, nil
}

// RepoStateObjects are the repository's state objects in the state
// bucket (its *.tfstate objects under RepoStatePrefix; a lock is not
// state), sorted.
func RepoStateObjects(ctx context.Context, c *Clients, stateBucket, slug string) ([]string, error) {
	if !slugRE.MatchString(slug) {
		return nil, fmt.Errorf("%q is not a repository slug", slug)
	}
	prefix := RepoStatePrefix(slug) + "/"
	var names []string
	err := c.Storage.Objects.List(stateBucket).Prefix(prefix).Pages(ctx, func(o *storage.Objects) error {
		for _, it := range o.Items {
			if strings.HasSuffix(it.Name, ".tfstate") {
				names = append(names, it.Name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing gs://%s/%s: %w", stateBucket, prefix, err)
	}
	slices.Sort(names)
	return names, nil
}

// StateBucketVersioned reports whether the state bucket keeps noncurrent
// versions, so a deleted state object can be restored.
func StateBucketVersioned(ctx context.Context, c *Clients, stateBucket string) (bool, error) {
	b, err := c.Storage.Buckets.Get(stateBucket).Context(ctx).Do()
	if err != nil {
		return false, fmt.Errorf("reading the state bucket gs://%s: %w", stateBucket, err)
	}
	return b.Versioning != nil && b.Versioning.Enabled, nil
}

// ForgetRepoState deletes the repository's state objects (by then empty:
// every address was removed from them), so the installation's rollback no
// longer finds the repository. The caller checks that the state bucket is
// versioned, so each stays recoverable as a noncurrent version. It returns
// what it deleted.
func ForgetRepoState(ctx context.Context, c *Clients, stateBucket, slug string) ([]string, error) {
	names, err := RepoStateObjects(ctx, c, stateBucket, slug)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, n := range names {
		if err := c.Storage.Objects.Delete(stateBucket, n).Context(ctx).Do(); err != nil && !notFound(err) {
			return deleted, fmt.Errorf("deleting gs://%s/%s: %w", stateBucket, n, err)
		}
		deleted = append(deleted, n)
	}
	return deleted, nil
}

// SecretVersions reports, for each of the repository's secrets by logical
// name, whether it has an enabled version. A secret that doesn't exist has
// none.
func SecretVersions(ctx context.Context, c *Clients, spec RepoSpec) (map[string]bool, error) {
	out := make(map[string]bool, len(spec.Secrets))
	for _, logical := range slices.Sorted(maps.Keys(spec.Secrets)) {
		ok, err := hasEnabledVersion(ctx, c, spec.Project, spec.Secrets[logical])
		if err != nil {
			return nil, err
		}
		out[logical] = ok
	}
	return out, nil
}

// NeedsBuild are the workflows, sorted, whose first image build can go
// ahead: there is no build record yet (a new repository, or an adopted one
// whose image is still the bootstrap's), and every secret the workflow
// mounts has a version (versions, from SecretVersions).
func NeedsBuild(ctx context.Context, c *Clients, spec RepoSpec, versions map[string]bool) ([]string, error) {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(spec.Workflows)) {
		ws := spec.Workflows[name]
		if slices.ContainsFunc(uniqueSecrets(ws), func(l string) bool { return !versions[l] }) {
			continue
		}
		ok, err := hasRecord(ctx, c, spec.Installation.RunsBucket, imagecheck.RecordKey(spec.Slug, name))
		if err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, name)
		}
	}
	return out, nil
}

// SecretCommands are the fugaro secrets set commands of the repository's
// secrets that have no version, sorted by name, as the bootstrap printed
// them: the value comes from stdin or a hidden prompt, never argv, and the
// Claude token is made by the user, in their own terminal.
func SecretCommands(spec RepoSpec, versions map[string]bool) []string {
	var out []string
	for _, logical := range slices.Sorted(maps.Keys(spec.Secrets)) {
		if versions[logical] {
			continue
		}
		set := "fugaro secrets set " + logical + " --repo " + spec.Name
		id := "   # " + spec.Secrets[logical]
		if logical == "claude-oauth-token" {
			out = append(out, "(you, in your own terminal) claude setup-token, then: "+set+id)
		} else {
			out = append(out, set+" < <file holding the value>"+id)
		}
	}
	return out
}
