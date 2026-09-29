package infra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	artifactregistry "google.golang.org/api/artifactregistry/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	iam "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// The roles whose live grants discovery compares with the spec's.
const (
	roleObjectUser     = "roles/storage.objectUser"
	roleSecretAccessor = "roles/secretmanager.secretAccessor"
)

// Clients are the Google API clients discovery and the readiness gates
// read through. They only ever read.
type Clients struct {
	IAM     *iam.Service
	AR      *artifactregistry.Service
	Run     *run.Service
	Secrets *secretmanager.Service
	Storage *storage.Service
	CRM     *crm.Service
}

// Endpoints override the roots of the APIs gcp.Endpoints has no field for,
// so fakes can stand in. Empty means Google's own endpoint.
type Endpoints struct {
	IAM, ArtifactRegistry, Storage, ResourceManager string
}

// discardLogger keeps the clients from logging requests (and their
// bodies) when GOOGLE_SDK_GO_LOGGING_LEVEL is set, as gcp's clients do.
var discardLogger = slog.New(slog.DiscardHandler)

// NewClients connects to every API discovery reads, with o's credentials,
// HTTP client and endpoints (Run and Secret Manager), and e's for the rest.
func NewClients(ctx context.Context, o gcp.Options, e Endpoints) (*Clients, error) {
	opts := func(endpoint string) []option.ClientOption {
		out := []option.ClientOption{option.WithLogger(discardLogger)}
		if endpoint != "" {
			out = append(out, option.WithEndpoint(endpoint))
		}
		if o.Endpoints.NoAuth {
			out = append(out, option.WithoutAuthentication())
		} else if o.Project != "" {
			// User ADC has no project of its own, and some APIs refuse it
			// without a quota project.
			out = append(out, option.WithQuotaProject(o.Project))
		}
		if o.HTTPClient != nil {
			out = append(out, option.WithHTTPClient(o.HTTPClient))
		}
		return out
	}
	var c Clients
	var err error
	if c.IAM, err = iam.NewService(ctx, opts(e.IAM)...); err != nil {
		return nil, fmt.Errorf("connecting to IAM: %w", err)
	}
	if c.AR, err = artifactregistry.NewService(ctx, opts(e.ArtifactRegistry)...); err != nil {
		return nil, fmt.Errorf("connecting to Artifact Registry: %w", err)
	}
	if c.Run, err = run.NewService(ctx, opts(o.Endpoints.Run)...); err != nil {
		return nil, fmt.Errorf("connecting to Cloud Run: %w", err)
	}
	if c.Secrets, err = secretmanager.NewService(ctx, opts(o.Endpoints.SecretManager)...); err != nil {
		return nil, fmt.Errorf("connecting to Secret Manager: %w", err)
	}
	if c.Storage, err = storage.NewService(ctx, opts(e.Storage)...); err != nil {
		return nil, fmt.Errorf("connecting to Cloud Storage: %w", err)
	}
	if c.CRM, err = crm.NewService(ctx, opts(e.ResourceManager)...); err != nil {
		return nil, fmt.Errorf("connecting to Resource Manager: %w", err)
	}
	return &c, nil
}

// ForeignError refuses a resource that exists under one of our names but
// doesn't carry our marks, or carries another repository's: adopting it,
// granting on it or deleting it would take over something that isn't ours.
type ForeignError struct {
	Resource string // what, and its name
	Found    string // the marks it carries
	Want     string // the marks it should carry
}

func (e *ForeignError) Error() string {
	return fmt.Sprintf("%s exists but is not this installation's: it carries %s, not %s. fugaro refuses to adopt it; rename or remove it, or fix its marks if it is ours",
		e.Resource, e.Found, e.Want)
}

// BindingError refuses a live grant that doesn't match the planned one. A
// planned grant with another condition, or next to more than one, would
// add a second grant rather than adopt the live one.
type BindingError struct {
	Resource, Role, Member string
	Want                   string   // the planned grant
	Found                  []string // the member's live grants of the role
}

func (e *BindingError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s holds %d live %s grant(s) that don't match the planned one, so the plan would add a second grant next to them rather than adopt them; remove or fix the live grant:\n", e.Resource, e.Member, len(e.Found), e.Role)
	fmt.Fprintf(&b, "  - want:  %s\n", e.Want)
	for _, f := range e.Found {
		fmt.Fprintf(&b, "  + found: %s\n", f)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// describeCondition renders a condition byte for byte, quoted, so a
// difference of one space shows.
func describeCondition(c *Condition, description string) string {
	if c == nil {
		return "no condition"
	}
	s := "title=" + strconv.Quote(c.Title) + " expression=" + strconv.Quote(c.Expression)
	if description != "" {
		s += " description=" + strconv.Quote(description)
	}
	return s
}

// notFound reports whether err is the API's 404.
func notFound(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusNotFound
}

// marks renders the labels of want's keys that have, sorted, with "none"
// for a missing one.
func marks(have, want map[string]string) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(want)) {
		v, ok := have[k]
		if !ok || v == "" {
			v = "none"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

func hasMarks(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// discovery collects one discovery's imports and refusals.
type discovery struct {
	ctx      context.Context
	c        *Clients
	project  string
	im       Imports
	refusals []error
}

func (d *discovery) refuse(err error) { d.refusals = append(d.refusals, err) }

func (d *discovery) foreign(resource string, have, want map[string]string) {
	d.refuse(&ForeignError{Resource: resource, Found: marks(have, want), Want: marks(want, want)})
}

// result is the discovery's imports, or every refusal as one user error
// (exit 1) and no imports.
func (d *discovery) result() (Imports, error) {
	if len(d.refusals) > 0 {
		return Imports{}, &UserError{Err: errors.Join(d.refusals...)}
	}
	return d.im, nil
}

func (d *discovery) add(k importKind, region, key, name string) {
	d.im.List = append(d.im.List, newImport(k, d.project, region, key, name))
}

// projectNumber is the project's number, which a bucket's must equal.
func (d *discovery) projectNumber() (uint64, error) {
	p, err := d.c.CRM.Projects.Get(d.project).Context(d.ctx).Do()
	if err != nil {
		return 0, fmt.Errorf("reading project %s: %w", d.project, err)
	}
	return uint64(p.ProjectNumber), nil
}

// bucket reads the runs bucket and checks that it is in the project and
// carries fugaro=managed. It is nil when the bucket doesn't exist or is
// refused.
func (d *discovery) bucket(name string) (*storage.Bucket, error) {
	num, err := d.projectNumber()
	if err != nil {
		return nil, err
	}
	b, err := d.c.Storage.Buckets.Get(name).Context(d.ctx).Do()
	switch {
	case notFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading bucket gs://%s: %w", name, err)
	}
	// Bucket names are global: one of ours by name may be in another
	// project, whose owner a grant or an import would reach.
	if b.ProjectNumber != num {
		d.refuse(&ForeignError{Resource: "bucket gs://" + name, Found: "project number " + strconv.FormatUint(b.ProjectNumber, 10),
			Want: "project " + d.project + " (number " + strconv.FormatUint(num, 10) + ")"})
		return nil, nil
	}
	want := map[string]string{gcp.LabelManaged: gcp.ManagedValue}
	if !hasMarks(b.Labels, want) {
		d.foreign("bucket gs://"+name, b.Labels, want)
		return nil, nil
	}
	return b, nil
}

// registry reads a Docker registry; it is nil when there is none.
func (d *discovery) registry(region, id string) (*artifactregistry.Repository, error) {
	name := "projects/" + d.project + "/locations/" + region + "/repositories/" + id
	r, err := d.c.AR.Projects.Locations.Repositories.Get(name).Context(d.ctx).Do()
	switch {
	case notFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading Artifact Registry repository %s: %w", name, err)
	}
	return r, nil
}

// DiscoverInstallation finds the installation's resources that exist: the
// runs bucket, the legacy registry and the base registry. Each that
// carries fugaro=managed (and, for the bucket, is in the project) is
// imported; one under our name without our mark is refused. A marked
// legacy registry sets AdoptLegacyRegistry; an unmarked one isn't ours and
// is left alone, since nothing plans it then.
func DiscoverInstallation(ctx context.Context, c *Clients, spec InstallationSpec) (Imports, error) {
	d := &discovery{ctx: ctx, c: c, project: spec.Project}
	b, err := d.bucket(spec.RunsBucket)
	if err != nil {
		return Imports{}, err
	}
	if b != nil {
		d.add(importRunsBucket, spec.Region, "", spec.RunsBucket)
	}
	managed := map[string]string{gcp.LabelManaged: gcp.ManagedValue}
	legacy, err := d.registry(spec.Region, spec.Names.LegacyRegistry)
	if err != nil {
		return Imports{}, err
	}
	switch {
	case legacy != nil && hasMarks(legacy.Labels, managed):
		d.im.AdoptLegacyRegistry = true
		d.add(importLegacyRegistry, spec.Region, "", spec.Names.LegacyRegistry)
	case legacy != nil:
		d.im.Notes = append(d.im.Notes, fmt.Sprintf("registry %s-docker.pkg.dev/%s/%s has no %s label (it carries %s); left alone, not managed by Terraform",
			spec.Region, spec.Project, spec.Names.LegacyRegistry, marks(managed, managed), marks(legacy.Labels, managed)))
	}
	base, err := d.registry(spec.Region, spec.Names.BaseRegistry)
	if err != nil {
		return Imports{}, err
	}
	switch {
	case base != nil && hasMarks(base.Labels, managed):
		d.add(importBaseRegistry, spec.Region, "", spec.Names.BaseRegistry)
	case base != nil:
		d.foreign("Artifact Registry repository "+spec.Names.BaseRegistry, base.Labels, managed)
	}
	return d.result()
}

// Existing is what discovery found of a repository's live jobs and
// accounts, for Readiness.
type Existing struct {
	// Jobs are the workflows whose job exists, with the job's current
	// image.
	Jobs map[string]string
	// DisplayNames are the job accounts whose display name is the
	// bootstrap's, by workflow. The spec keeps it, so the plan doesn't
	// rename the account and the bootstrap still recognizes it.
	DisplayNames map[string]string
}

// uniqueSecrets are the logical secrets ws mounts, sorted.
func uniqueSecrets(ws WorkflowSpec) []string {
	return slices.Compact(slices.Sorted(maps.Values(ws.SecretEnv)))
}

// DiscoverRepo finds the repository's resources that exist and checks
// each one's marks: its secrets, image registry, build account and check
// job, and each workflow's job account and job. What passes is imported,
// what fails is refused (a *ForeignError), and what doesn't exist is left
// for the plan to create.
//
// It also reads the live grants of each existing job account on the runs
// bucket and on its secrets. Grants aren't imported (see BindingCount), so
// a live grant that differs from the planned one is refused with a
// *BindingError: the plan would add a second grant next to it.
func DiscoverRepo(ctx context.Context, c *Clients, spec RepoSpec) (Imports, Existing, error) {
	d := &discovery{ctx: ctx, c: c, project: spec.Project}
	ex := Existing{Jobs: map[string]string{}, DisplayNames: map[string]string{}}
	b, err := d.bucket(spec.Installation.RunsBucket)
	if err != nil {
		return Imports{}, Existing{}, err
	}

	// Secrets, and their live policies for the accessor check.
	secretPolicies := map[string]*secretmanager.Policy{}
	for _, logical := range slices.Sorted(maps.Keys(spec.Secrets)) {
		id := spec.Secrets[logical]
		name := "projects/" + spec.Project + "/secrets/" + id
		s, err := c.Secrets.Projects.Secrets.Get(name).Context(ctx).Do()
		switch {
		case notFound(err):
			continue
		case err != nil:
			return Imports{}, Existing{}, fmt.Errorf("reading secret %s: %w", id, err)
		}
		want := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelSecret: logical}
		if !hasMarks(s.Labels, want) {
			d.foreign("secret "+id, s.Labels, want)
			continue
		}
		d.add(importSecret, spec.Region, logical, id)
		p, err := c.Secrets.Projects.Secrets.GetIamPolicy(name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
		if err != nil {
			return Imports{}, Existing{}, fmt.Errorf("reading the IAM policy of secret %s: %w", id, err)
		}
		secretPolicies[logical] = p
	}

	// The repository's image registry.
	repoMarks := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label}
	r, err := d.registry(spec.Region, spec.Registry.RepositoryID)
	if err != nil {
		return Imports{}, Existing{}, err
	}
	switch {
	case r != nil && hasMarks(r.Labels, repoMarks):
		d.add(importRepoRegistry, spec.Region, "", spec.Registry.RepositoryID)
	case r != nil:
		d.foreign("Artifact Registry repository "+spec.Registry.RepositoryID, r.Labels, repoMarks)
	}

	// The build account.
	if dn, ok, err := d.accountName(spec.BuildServiceAccountEmail); err != nil {
		return Imports{}, Existing{}, err
	} else if ok {
		if dn == spec.BuildServiceAccount.DisplayName {
			d.add(importBuildSA, spec.Region, "", spec.BuildServiceAccountEmail)
		} else {
			d.refuse(&ForeignError{Resource: "service account " + spec.BuildServiceAccountEmail,
				Found: "display name " + strconv.Quote(dn), Want: "display name " + strconv.Quote(spec.BuildServiceAccount.DisplayName)})
		}
	}

	// The check job.
	if spec.Check != nil {
		want := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelRole: gcp.RoleCheck}
		if j, err := d.job(spec.Region, spec.Check.Job); err != nil {
			return Imports{}, Existing{}, err
		} else if j != nil {
			if hasMarks(j.Labels, want) {
				d.add(importCheckJob, spec.Region, "", spec.Check.Job)
			} else {
				d.foreign("Cloud Run job "+spec.Check.Job, j.Labels, want)
			}
		}
	}

	// Each workflow's job account and job, and the account's live grants.
	for _, name := range slices.Sorted(maps.Keys(spec.Workflows)) {
		ws := spec.Workflows[name]
		dn, exists, err := d.accountName(ws.ServiceAccountEmail)
		if err != nil {
			return Imports{}, Existing{}, err
		}
		if exists {
			legacy := gcp.LegacyJobSADisplayName(ws.Slug, name)
			switch dn {
			case ws.ServiceAccount.DisplayName:
				d.add(importJobSA, spec.Region, name, ws.ServiceAccountEmail)
			case legacy:
				d.add(importJobSA, spec.Region, name, ws.ServiceAccountEmail)
				ex.DisplayNames[name] = legacy
			default:
				d.refuse(&ForeignError{Resource: "service account " + ws.ServiceAccountEmail, Found: "display name " + strconv.Quote(dn),
					Want: "display name " + strconv.Quote(ws.ServiceAccount.DisplayName) + " or " + strconv.Quote(legacy)})
				exists = false
			}
		}
		if exists {
			if err := d.checkGrants(spec, ws, b, secretPolicies); err != nil {
				return Imports{}, Existing{}, err
			}
		} else {
			d.im.Bindings.New += 1 + len(uniqueSecrets(ws))
		}

		j, err := d.job(spec.Region, ws.Job)
		if err != nil {
			return Imports{}, Existing{}, err
		}
		if j != nil {
			if hasMarks(j.Labels, ws.Labels) {
				d.add(importJob, spec.Region, name, ws.Job)
				ex.Jobs[name] = jobImage(j)
			} else {
				d.foreign("Cloud Run job "+ws.Job, j.Labels, ws.Labels)
			}
		}
	}
	d.noteExtraAccessors(spec, secretPolicies)

	im, err := d.result()
	if err != nil {
		return Imports{}, Existing{}, err
	}
	return im, ex, nil
}

// accountName is the display name of the service account email, and
// whether it exists.
func (d *discovery) accountName(email string) (string, bool, error) {
	a, err := d.c.IAM.Projects.ServiceAccounts.Get("projects/" + d.project + "/serviceAccounts/" + email).Context(d.ctx).Do()
	switch {
	case notFound(err):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("reading service account %s: %w", email, err)
	}
	return a.DisplayName, true, nil
}

// job reads a Cloud Run job; it is nil when there is none.
func (d *discovery) job(region, name string) (*run.GoogleCloudRunV2Job, error) {
	full := "projects/" + d.project + "/locations/" + region + "/jobs/" + name
	j, err := d.c.Run.Projects.Locations.Jobs.Get(full).Context(d.ctx).Do()
	switch {
	case notFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading Cloud Run job %s: %w", name, err)
	}
	return j, nil
}

// jobImage is the image of a job's first container, as the job holds it.
func jobImage(j *run.GoogleCloudRunV2Job) string {
	if j.Template == nil || j.Template.Template == nil || len(j.Template.Template.Containers) == 0 {
		return ""
	}
	return j.Template.Template.Containers[0].Image
}

// checkGrants compares the existing job account's live grants with the
// planned ones: its conditional objectUser on the runs bucket (b, nil when
// the bucket doesn't exist) and its accessor on each secret it mounts.
func (d *discovery) checkGrants(spec RepoSpec, ws WorkflowSpec, b *storage.Bucket, secretPolicies map[string]*secretmanager.Policy) error {
	member := "serviceAccount:" + ws.ServiceAccountEmail
	if b == nil {
		d.im.Bindings.New++
	} else {
		p, err := d.c.Storage.Buckets.GetIamPolicy(b.Name).OptionsRequestedPolicyVersion(3).Context(d.ctx).Do()
		if err != nil {
			return fmt.Errorf("reading the IAM policy of gs://%s: %w", b.Name, err)
		}
		var found []string
		match := false
		for _, g := range p.Bindings {
			if g.Role != roleObjectUser || !slices.Contains(g.Members, member) {
				continue
			}
			var c *Condition
			desc := ""
			if g.Condition != nil {
				c, desc = &Condition{Title: g.Condition.Title, Expression: g.Condition.Expression}, g.Condition.Description
			}
			found = append(found, describeCondition(c, desc))
			match = c != nil && *c == ws.BucketCondition && desc == ""
		}
		switch {
		case len(found) == 0:
			d.im.Bindings.New++
		case len(found) == 1 && match:
			d.im.Bindings.Adopted++
		default:
			d.refuse(&BindingError{Resource: "bucket gs://" + b.Name, Role: roleObjectUser, Member: member,
				Want: describeCondition(&ws.BucketCondition, ""), Found: found})
		}
	}
	for _, logical := range uniqueSecrets(ws) {
		p := secretPolicies[logical]
		if p == nil { // the secret doesn't exist yet, or was refused
			d.im.Bindings.New++
			continue
		}
		var conditional []string
		plain := false
		for _, g := range p.Bindings {
			if g.Role != roleSecretAccessor || !slices.Contains(g.Members, member) {
				continue
			}
			if g.Condition == nil {
				plain = true
				continue
			}
			conditional = append(conditional, describeCondition(&Condition{Title: g.Condition.Title, Expression: g.Condition.Expression}, g.Condition.Description))
		}
		switch {
		case plain:
			d.im.Bindings.Adopted++
		case len(conditional) == 0:
			d.im.Bindings.New++
		default:
			d.refuse(&BindingError{Resource: "secret " + spec.Secrets[logical], Role: roleSecretAccessor, Member: member,
				Want: describeCondition(nil, ""), Found: conditional})
		}
	}
	return nil
}

// noteExtraAccessors notes the accessors of each existing secret that the
// spec doesn't plan: the plan leaves them, since it grants only members.
func (d *discovery) noteExtraAccessors(spec RepoSpec, secretPolicies map[string]*secretmanager.Policy) {
	planned := map[string]map[string]bool{}
	plan := func(logical, email string) {
		if planned[logical] == nil {
			planned[logical] = map[string]bool{}
		}
		planned[logical]["serviceAccount:"+email] = true
	}
	for _, ws := range spec.Workflows {
		for _, logical := range uniqueSecrets(ws) {
			plan(logical, ws.ServiceAccountEmail)
		}
	}
	for _, logical := range spec.BuildSecrets {
		plan(logical, spec.BuildServiceAccountEmail)
	}
	for _, logical := range slices.Sorted(maps.Keys(secretPolicies)) {
		var extra []string
		for _, g := range secretPolicies[logical].Bindings {
			if g.Role != roleSecretAccessor {
				continue
			}
			for _, m := range g.Members {
				if !planned[logical][m] && !slices.Contains(extra, m) {
					extra = append(extra, m)
				}
			}
		}
		if len(extra) > 0 {
			slices.Sort(extra)
			d.im.Notes = append(d.im.Notes, fmt.Sprintf("secret %s can also be read by %s, which the plan neither grants nor removes; remove the grant by hand once nothing needs it",
				spec.Secrets[logical], strings.Join(extra, ", ")))
		}
	}
}
