package infra

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// StateLabelValue is the state bucket's fugaro label: its mark, as
// fugaro=managed is the runs bucket's.
const StateLabelValue = "tfstate"

// stateNoncurrentVersions is how many noncurrent versions of each state
// object the state bucket keeps. A noncurrent version is deleted once it
// has more than that many newer versions, the live one included.
const stateNoncurrentVersions = 20

// The convenience bindings through which GCS lets a project's Viewers
// read a bucket. They are the only ones fugaro removes.
var viewerReaderRoles = []string{"roles/storage.legacyBucketReader", "roles/storage.legacyObjectReader"}

const projectViewerPrefix = "projectViewer:"

// ProjectNumber is project's number, which its buckets carry. It is a
// *ServiceDisabledError while the Cloud Resource Manager API is disabled
// in the project, which fugaro init offers to fix.
func ProjectNumber(ctx context.Context, c *Clients, project string) (uint64, error) {
	p, err := c.CRM.Projects.Get(project).Context(ctx).Do()
	if serviceDisabled(err, ServiceResourceManager, target{project: project}) {
		return 0, &ServiceDisabledError{Service: ServiceResourceManager, Project: project, Err: err}
	}
	if err != nil {
		return 0, fmt.Errorf("reading project %s: %w", project, err)
	}
	return uint64(p.ProjectNumber), nil
}

// CheckStateBucket reports whether the state bucket exists. One that exists
// must be in project and carry fugaro=tfstate: bucket names are global, and
// the state holds every name, account and condition of the installation.
func CheckStateBucket(ctx context.Context, c *Clients, project, name string) (bool, error) {
	num, err := ProjectNumber(ctx, c, project)
	if err != nil {
		return false, err
	}
	b, err := c.Storage.Buckets.Get(name).Context(ctx).Do()
	switch {
	// Only a 404: the state bucket is Terraform's backend, so a disabled
	// Storage API is an environment error here, not "no bucket yet".
	case notFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading the state bucket gs://%s: %w", name, err)
	}
	if b.ProjectNumber != num {
		return false, &UserError{Err: &ForeignError{Resource: "state bucket gs://" + name, Found: "project number " + strconv.FormatUint(b.ProjectNumber, 10),
			Want: "project " + project + " (number " + strconv.FormatUint(num, 10) + ")"}}
	}
	want := map[string]string{gcp.LabelManaged: StateLabelValue}
	if !hasMarks(b.Labels, want) {
		return false, &UserError{Err: &ForeignError{Resource: "state bucket gs://" + name, Found: marks(b.Labels, want), Want: marks(want, want)}}
	}
	return true, nil
}

// CreateStateBucket creates the state bucket in region: versioned, with
// uniform access, public access prevention enforced, fugaro=tfstate, and
// the newest noncurrent versions kept. Then it removes the project
// Viewers' read access GCS grants every new bucket, so only owners,
// editors and those granted on it (operators) can read the state.
func CreateStateBucket(ctx context.Context, c *Clients, project, region, name string) error {
	b := &storage.Bucket{
		Name:       name,
		Location:   region,
		Labels:     map[string]string{gcp.LabelManaged: StateLabelValue},
		Versioning: &storage.BucketVersioning{Enabled: true},
		IamConfiguration: &storage.BucketIamConfiguration{
			UniformBucketLevelAccess: &storage.BucketIamConfigurationUniformBucketLevelAccess{Enabled: true},
			PublicAccessPrevention:   "enforced",
		},
		Lifecycle: &storage.BucketLifecycle{Rule: []*storage.BucketLifecycleRule{{
			Action: &storage.BucketLifecycleRuleAction{Type: "Delete"},
			Condition: &storage.BucketLifecycleRuleCondition{
				NumNewerVersions: stateNoncurrentVersions + 1,
				IsLive:           new(false),
			},
		}}},
	}
	if _, err := c.Storage.Buckets.Insert(project, b).Context(ctx).Do(); err != nil {
		return fmt.Errorf("creating the state bucket gs://%s: %w", name, err)
	}
	p, err := BucketPolicy(ctx, c, name)
	if err != nil {
		return err
	}
	return RemoveProjectViewers(ctx, c, name, p)
}

// BucketPolicy reads bucket's IAM policy, conditions included.
func BucketPolicy(ctx context.Context, c *Clients, bucket string) (*storage.Policy, error) {
	p, err := c.Storage.Buckets.GetIamPolicy(bucket).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("reading the IAM policy of gs://%s: %w", bucket, err)
	}
	return p, nil
}

// isViewerGrant reports whether member of b is a project Viewers'
// convenience grant to read the bucket.
func isViewerGrant(b *storage.PolicyBindings, member string) bool {
	return b.Condition == nil && slices.Contains(viewerReaderRoles, b.Role) && strings.HasPrefix(member, projectViewerPrefix)
}

// ProjectViewerGrants lists p's project Viewer convenience grants, as
// "<role> <member>", sorted.
func ProjectViewerGrants(p *storage.Policy) []string {
	var out []string
	for _, b := range p.Bindings {
		for _, m := range b.Members {
			if isViewerGrant(b, m) {
				out = append(out, b.Role+" "+m)
			}
		}
	}
	slices.Sort(out)
	return out
}

// RemoveProjectViewers removes p's project Viewer convenience grants from
// bucket, and nothing else, with one setIamPolicy that carries p's etag:
// if the policy changed since p was read, GCS refuses the set rather than
// overwrite the change. It does nothing when p has no such grant.
func RemoveProjectViewers(ctx context.Context, c *Clients, bucket string, p *storage.Policy) error {
	if len(ProjectViewerGrants(p)) == 0 {
		return nil
	}
	out := &storage.Policy{Etag: p.Etag, Version: p.Version}
	for _, b := range p.Bindings {
		kept := slices.DeleteFunc(slices.Clone(b.Members), func(m string) bool { return isViewerGrant(b, m) })
		if len(kept) == 0 {
			continue
		}
		nb := *b
		nb.Members = kept
		out.Bindings = append(out.Bindings, &nb)
	}
	if _, err := c.Storage.Buckets.SetIamPolicy(bucket, out).Context(ctx).Do(); err != nil {
		return fmt.Errorf("removing project Viewers' access to gs://%s: %w", bucket, err)
	}
	return nil
}

// isState reports whether a state bucket object is a root's state: a
// *.tfstate object. A lock (*.tflock) is someone's running operation, or
// one that died, not state.
func isState(name string) bool { return strings.HasSuffix(name, ".tfstate") }

// RepoStates lists the repository roots' state objects (*.tfstate) in the
// state bucket: what init --repo --forget deletes, so a leftover lock
// doesn't count.
func RepoStates(ctx context.Context, c *Clients, stateBucket string) ([]string, error) {
	return StateObjects(ctx, c, stateBucket, StatePrefixRepos)
}

// StateObjects lists the state objects (*.tfstate) under prefix in the state
// bucket, sorted, so two readers of the same bucket read the same list. An
// empty prefix is the whole bucket, which is what the whole-project
// inventory reads: every root this installation has applied — the
// installation's, the Firebase project's, every repository's and (in the
// managed state bucket D77 gives its own) the agent-authored one — holds the
// resources the installation accounts for, and each of them is a state
// object under one of infra's prefixes.
//
// Nothing here reads an object's contents, and nothing filters by the prefix
// beyond the listing itself: what counts as state is the *.tfstate suffix,
// exactly as it is for the repository roots.
func StateObjects(ctx context.Context, c *Clients, stateBucket, prefix string) ([]string, error) {
	var out []string
	call := c.Storage.Objects.List(stateBucket)
	if prefix != "" {
		call = call.Prefix(prefix)
	}
	if stateListPageSize > 0 {
		call = call.MaxResults(stateListPageSize)
	}
	err := call.Pages(ctx, func(o *storage.Objects) error {
		for _, it := range o.Items {
			if isState(it.Name) && !strings.HasSuffix(it.Name, "/") {
				out = append(out, it.Name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing gs://%s/%s: %w", stateBucket, prefix, err)
	}
	slices.Sort(out)
	return out, nil
}

// stateListPageSize is the page size StateObjects asks for; zero leaves it to
// GCS. It is a variable only so a test can make the listing span pages and
// see that every page is read.
var stateListPageSize int64

// maxStateObject is as much of a state object as this build reads. A root's
// state is a few hundred kilobytes; anything of this size is not a state this
// program wrote, and reading an object of unbounded size into memory to parse
// it is a way for a bucket's contents to decide this program's memory.
var maxStateObject = 64 << 20

// ReadStateObject reads one state object's contents. Its size is bounded
// rather than trusted: a state larger than maxStateObject is refused, and the
// caller's direction on a read it could not make is the fallback (Global
// Constraint 3), never a project reported clean because a state was skipped.
func ReadStateObject(ctx context.Context, c *Clients, stateBucket, name string) ([]byte, error) {
	resp, err := c.Storage.Objects.Get(stateBucket, name).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("reading gs://%s/%s: %w", stateBucket, name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxStateObject)+1))
	if err != nil {
		return nil, fmt.Errorf("reading gs://%s/%s: %w", stateBucket, name, err)
	}
	if len(data) > maxStateObject {
		return nil, fmt.Errorf("reading gs://%s/%s: it is larger than %d bytes, which is not a state this program writes", stateBucket, name, maxStateObject)
	}
	return data, nil
}
