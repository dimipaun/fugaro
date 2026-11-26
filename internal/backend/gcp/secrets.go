package gcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// ErrForeignSecret is Set's refusal to add a version to an existing secret
// that doesn't carry the labels asked for: it belongs to another repository
// or was made by hand, and a derived ID colliding with it must not
// overwrite it.
var ErrForeignSecret = errors.New("secret exists but is not labelled as ours")

// ErrNoVersion is Access on a secret that does not exist or has no version.
var ErrNoVersion = errors.New("the secret does not exist or has no version")

// ErrNoAccess is Access refused: the caller may not read secret values.
var ErrNoAccess = errors.New("not allowed to read secret values")

// Secrets manages a repository's secrets in Secret Manager. It never reads
// a value back (Fugaro only writes secrets, and the platform mounts them),
// but for Access, the GitHub App pre-check's one read.
type Secrets struct {
	svc     *secretmanager.Service
	project string
	// region is the installation's own region, where a container this
	// creates is replicated. For a secret the mapping's answer is the region
	// itself, so nothing here asks internal/place (D3082, D3092).
	region string
}

// SecretInfo describes a secret without its value.
type SecretInfo struct {
	ID       string            `json:"id"`
	Created  time.Time         `json:"created,omitzero"` // absent for a row with no container (secrets ls MISSING)
	Labels   map[string]string `json:"labels,omitempty"`
	Versions int               `json:"versions"`         // versions not destroyed
	Latest   string            `json:"latest,omitempty"` // the newest enabled version, if any
}

// NewSecrets connects to Secret Manager.
func NewSecrets(ctx context.Context, o Options) (*Secrets, error) {
	svc, err := secretmanager.NewService(ctx, o.client(o.Endpoints.SecretManager)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Secret Manager: %w", err)
	}
	return &Secrets{svc: svc, project: o.GCPProject, region: o.Region}, nil
}

// NewSecretManager connects to Secret Manager and returns the client
// itself, for callers that read metadata through infra's own resolution
// helpers (infra.ResolveSecretID, infra.GrantedSecretID) rather than
// through Secrets' own methods.
func NewSecretManager(ctx context.Context, o Options) (*secretmanager.Service, error) {
	svc, err := secretmanager.NewService(ctx, o.client(o.Endpoints.SecretManager)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Secret Manager: %w", err)
	}
	return svc, nil
}

// Access reads the value of secret id's latest version, with the caller's own
// credentials. It exists for one purpose: the GitHub App pre-check of init and
// doctor signs a JWT with the App's key, in memory, to ask GitHub what the
// installation grants. The caller holds the value in memory only (clearing its byte copy; the
// encoded payload lives until garbage collection) and never
// prints, logs or passes it on; the errors returned never carry it. A person
// without secretmanager.versions.access (the roles Fugaro grants launchers and
// operators do not include it; project owners have it) gets ErrNoAccess.
func (s *Secrets) Access(ctx context.Context, id string) ([]byte, error) {
	r, err := s.svc.Projects.Secrets.Versions.Access(s.name(id) + "/versions/latest").Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		switch {
		case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
			return nil, ErrNoVersion
		case errors.As(err, &ge) && ge.Code == http.StatusForbidden:
			return nil, ErrNoAccess
		}
		return nil, fmt.Errorf("reading secret %s: %w", id, err)
	}
	if r.Payload == nil {
		return nil, ErrNoVersion
	}
	data, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("reading secret %s: the payload is not base64", id)
	}
	return data, nil
}

// LatestEnabled answers the number of id's newest ENABLED version, and "" when
// the container holds none (absent, or every version disabled or destroyed).
// Reading the value at the number this answers is what makes a copy of it safe:
// the alias `latest` names the newest version whatever its state, so a
// container whose newest version an operator disabled would refuse the read
// the copy makes, and the enabled one under it is the version a run reads.
func (s *Secrets) LatestEnabled(ctx context.Context, id string) (string, error) {
	latest := 0
	err := s.svc.Projects.Secrets.Versions.List(s.name(id)).Pages(ctx, func(p *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range p.Versions {
			n, err := strconv.Atoi(lastSegment(v.Name))
			if err != nil || v.State != "ENABLED" || n <= latest {
				continue
			}
			latest = n
		}
		return nil
	})
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("listing the versions of secret %s: %w", id, err)
	}
	if latest == 0 {
		return "", nil
	}
	return strconv.Itoa(latest), nil
}

// ReadVersion reads the value of one numbered version of id. It is the read the
// migration makes and the GitHub App pre-check's (Access), and it exists beside
// Access rather than in place of it because the version a copy must take is the
// newest ENABLED one and not the `latest` alias: an operator's disable makes the
// two disagree. The caller holds the bytes in memory only and never prints,
// logs or passes them on.
func (s *Secrets) ReadVersion(ctx context.Context, id, version string) ([]byte, error) {
	r, err := s.svc.Projects.Secrets.Versions.Access(s.name(id) + "/versions/" + version).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		switch {
		case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
			return nil, ErrNoVersion
		case errors.As(err, &ge) && ge.Code == http.StatusForbidden:
			return nil, ErrNoAccess
		}
		return nil, fmt.Errorf("reading secret %s: %w", id, err)
	}
	if r.Payload == nil {
		return nil, ErrNoVersion
	}
	data, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("reading secret %s: the payload is not base64", id)
	}
	return data, nil
}

// ContainerInfo is one container's metadata: its labels, its annotations, and
// the shape facts that take a container away from whoever wrote it (internal/
// intake's R12 — a TTL or an expiry deletes it, a topic publishes it, a CMEK
// puts it under another key, a rotation replaces it on someone else's clock).
// Never a version and never a payload.
type ContainerInfo struct {
	Labels      map[string]string
	Annotations map[string]string
	TTL         string
	ExpireTime  string
	Topics      []string
	CMEK        string
	Rotation    string
}

// Container answers id's metadata, and whether a container of that ID is there
// at all. Metadata only: like every read here it never touches a version's
// value.
func (s *Secrets) Container(ctx context.Context, id string) (ContainerInfo, bool, error) {
	sec, err := s.svc.Projects.Secrets.Get(s.name(id)).Context(ctx).Do()
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return ContainerInfo{}, false, nil
		}
		if isStatus(err, http.StatusForbidden) {
			return ContainerInfo{}, false, ErrNoAccess
		}
		return ContainerInfo{}, false, fmt.Errorf("reading secret %s: %w", id, err)
	}
	out := ContainerInfo{Labels: sec.Labels, Annotations: sec.Annotations, TTL: sec.Ttl, ExpireTime: sec.ExpireTime}
	for _, t := range sec.Topics {
		if t != nil && t.Name != "" {
			out.Topics = append(out.Topics, t.Name)
		}
	}
	out.CMEK = cmekOf(sec)
	if sec.Rotation != nil {
		out.Rotation = rotationOf(sec.Rotation)
	}
	return out, true, nil
}

// CanReadValues reports whether the caller's own credentials may read id's
// values, through the testIamPermissions probe the provider-key writers already
// make before they read one. It exists so that a caller which must not read a
// value it cannot have asks before it tries: an operator without
// secretmanager.versions.access gets a refusal naming what to do rather than a
// 403 from the middle of a copy. A container that is not there answers false
// with no error — there is no value to read either way.
func (s *Secrets) CanReadValues(ctx context.Context, id string) (bool, error) {
	r, err := s.svc.Projects.Secrets.TestIamPermissions(s.name(id),
		&secretmanager.TestIamPermissionsRequest{Permissions: []string{versionsAccessPermission}}).Context(ctx).Do()
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("asking what may be read from secret %s: %w", id, err)
	}
	return len(r.Permissions) > 0, nil
}

// versionsAccessPermission is the one permission this package asks about: the
// read of a secret's value, which roles/owner carries on every container and
// the roles Fugaro grants an operator do not.
const versionsAccessPermission = "secretmanager.versions.access"

// Create makes the container id with labels and annotations and no version,
// replicated in the installation's region, as a caller that writes one value
// into a fresh container does. A container that appeared between a read and
// this call is the caller's to notice: a 409 is returned as it is, never as a
// silent success.
func (s *Secrets) Create(ctx context.Context, id string, labels, annotations map[string]string) error {
	_, err := s.svc.Projects.Secrets.Create("projects/"+s.project, &secretmanager.Secret{
		Labels: labels, Annotations: annotations, Replication: replicationIn(s.region),
	}).SecretId(id).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("creating secret %s: %w", id, err)
	}
	return nil
}

// AddVersion stores value as a new version of id and answers its number. It is
// Set's own second half, shared so a caller that creates the container itself
// adds its version the one way.
func (s *Secrets) AddVersion(ctx context.Context, id string, value []byte) (string, error) {
	v, err := s.svc.Projects.Secrets.AddVersion(s.name(id), &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString(value)},
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("adding a version to secret %s: %w", id, err)
	}
	return lastSegment(v.Name), nil
}

// Set stores value as a new version of secret id, creating the secret with
// labels and user-managed replication in the installation's region if it does
// not exist. A container that already exists keeps the replication it has —
// only a create carries a policy — and must carry every one of labels, or Set
// refuses with ErrForeignSecret. It returns the new version's ID ("1", "2", …).
//
// Errors can carry what the server sent back; the caller redacts value from
// them before showing them.
func (s *Secrets) Set(ctx context.Context, id string, value []byte, labels map[string]string) (string, error) {
	name := s.name(id)
	sec, err := s.svc.Projects.Secrets.Get(name).Context(ctx).Do()
	switch {
	case isStatus(err, http.StatusNotFound):
		create := &secretmanager.Secret{Labels: labels, Replication: replicationIn(s.region)}
		_, err = s.svc.Projects.Secrets.Create("projects/"+s.project, create).SecretId(id).Context(ctx).Do()
		switch {
		case isStatus(err, http.StatusConflict):
			// Created since the get, by a concurrent set or someone else:
			// it is ours only if its labels say so.
			if sec, err = s.svc.Projects.Secrets.Get(name).Context(ctx).Do(); err != nil {
				return "", fmt.Errorf("reading secret %s: %w", id, err)
			}
			if err := checkLabels(id, sec, labels); err != nil {
				return "", err
			}
		case err != nil:
			return "", fmt.Errorf("creating secret %s: %w", id, err)
		}
	case err != nil:
		return "", fmt.Errorf("reading secret %s: %w", id, err)
	default:
		if err := checkLabels(id, sec, labels); err != nil {
			return "", err
		}
	}
	return s.AddVersion(ctx, id, value)
}

// cmekOf is the customer-managed key a container is bound to. The API can name
// one in three places — on the secret itself, on an automatic replication
// policy, or on any replica of a user-managed one — and a container carrying one
// anywhere is one a writer that holds no such key must not write into, so the
// first non-empty is the answer. Which of several replicas it came from does not
// matter: the refusal names the fact and not the key. It mirrors internal/
// intake's helper of the same name rather than importing it, the way this
// package's other reads of that package's shape facts are mirrored.
func cmekOf(sec *secretmanager.Secret) string {
	if c := sec.CustomerManagedEncryption; c != nil && c.KmsKeyName != "" {
		return c.KmsKeyName
	}
	rep := sec.Replication
	if rep == nil {
		return ""
	}
	if a := rep.Automatic; a != nil && a.CustomerManagedEncryption != nil {
		return a.CustomerManagedEncryption.KmsKeyName
	}
	if u := rep.UserManaged; u != nil {
		for _, r := range u.Replicas {
			if r != nil && r.CustomerManagedEncryption != nil && r.CustomerManagedEncryption.KmsKeyName != "" {
				return r.CustomerManagedEncryption.KmsKeyName
			}
		}
	}
	return ""
}

// rotationOf is the rotation schedule a container carries, as the text that says
// there is one: the period where it has one, the next rotation time otherwise,
// and the fact itself where the API answered a schedule with neither spelled
// out. Mirrored from internal/intake for cmekOf's reason.
func rotationOf(r *secretmanager.Rotation) string {
	switch {
	case r.RotationPeriod != "":
		return r.RotationPeriod
	case r.NextRotationTime != "":
		return r.NextRotationTime
	default:
		return "set"
	}
}

func isStatus(err error, code int) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == code
}

// checkLabels is ErrForeignSecret unless sec carries every one of labels.
func checkLabels(id string, sec *secretmanager.Secret, labels map[string]string) error {
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		if got, ok := sec.Labels[k]; !ok || got != labels[k] {
			return fmt.Errorf("secret %s has label %s=%q, want %q: %w", id, k, got, labels[k], ErrForeignSecret)
		}
	}
	return nil
}

// replicationIn is the policy a container this creates carries: user-managed,
// with one replica in the installation's own region. Not automatic, which
// Google documents as global, and not internal/place's answer either: for a
// secret that answer is the region itself (D3082, D3092).
func replicationIn(region string) *secretmanager.Replication {
	return &secretmanager.Replication{UserManaged: &secretmanager.UserManaged{
		Replicas: []*secretmanager.Replica{{Location: region}},
	}}
}

// List lists the secrets carrying all of labels, with their version counts
// (one versions.list call per secret; it returns metadata, never values).
func (s *Secrets) List(ctx context.Context, labels map[string]string) ([]SecretInfo, error) {
	var filter []string
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		filter = append(filter, "labels."+k+"="+labels[k])
	}
	var out []SecretInfo
	err := s.svc.Projects.Secrets.List("projects/"+s.project).Filter(strings.Join(filter, " AND ")).Pages(ctx, func(p *secretmanager.ListSecretsResponse) error {
		for _, sec := range p.Secrets {
			created, _ := time.Parse(time.RFC3339Nano, sec.CreateTime)
			out = append(out, SecretInfo{ID: lastSegment(sec.Name), Created: created, Labels: sec.Labels})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	for i := range out {
		if err := s.countVersions(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Secrets) countVersions(ctx context.Context, info *SecretInfo) error {
	latest := 0
	err := s.svc.Projects.Secrets.Versions.List(s.name(info.ID)).Pages(ctx, func(p *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range p.Versions {
			if v.State == "DESTROYED" {
				continue
			}
			info.Versions++
			if n, err := strconv.Atoi(lastSegment(v.Name)); err == nil && v.State == "ENABLED" && n > latest {
				latest = n
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("listing the versions of secret %s: %w", info.ID, err)
	}
	if latest > 0 {
		info.Latest = strconv.Itoa(latest)
	}
	return nil
}

// SetLabels replaces id's labels with labels, and nothing else about the
// container: the update mask is labels, so no replication, no TTL and no
// version is touched.
//
// The whole map is the new one, which is what a caller taking a mark OFF a
// container needs: the retirement of a project copy drops LabelSecret — the
// label internal/infra's discovery adopts a project-wide container by — and
// writes LabelRetired in its place. The caller builds the map from the
// container's own labels, so nothing it does not know about is dropped by
// accident.
func (s *Secrets) SetLabels(ctx context.Context, id string, labels map[string]string) error {
	_, err := s.svc.Projects.Secrets.Patch(s.name(id), &secretmanager.Secret{Labels: labels}).
		UpdateMask("labels").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("labelling secret %s: %w", id, err)
	}
	return nil
}

// Delete removes secret id and all its versions.
func (s *Secrets) Delete(ctx context.Context, id string) error {
	if _, err := s.svc.Projects.Secrets.Delete(s.name(id)).Context(ctx).Do(); err != nil {
		return fmt.Errorf("deleting secret %s: %w", id, err)
	}
	return nil
}

// SecretAccessorRole is the role that lets a member read a secret's values,
// the one Fugaro grants: a job's own service account for what it mounts, and
// the build account for what its builds read.
const SecretAccessorRole = "roles/secretmanager.secretAccessor"

// SecretBinding is one binding of a container's IAM policy: the role and the
// members it is granted to, as the policy lists them.
type SecretBinding struct {
	Role    string
	Members []string
}

// SecretPolicy reads secret id's live IAM policy and returns its bindings.
// Metadata only: like every other read here it never touches a version's
// value.
//
// Bindings rather than the members of one role, because the role is part of
// the answer: doctor's reader sweep asks which role a member holds it
// through, and a role other than the one Fugaro grants can carry the read as
// well.
//
// A conditional binding counts: it is still a grant this caller has to
// account for. The policy is read at version 3, so a conditional binding is
// not silently dropped from the answer.
func (s *Secrets) SecretPolicy(ctx context.Context, id string) ([]SecretBinding, error) {
	p, err := s.svc.Projects.Secrets.GetIamPolicy(s.name(id)).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("reading the IAM policy of secret %s: %w", id, err)
	}
	out := make([]SecretBinding, 0, len(p.Bindings))
	for _, b := range p.Bindings {
		if b == nil {
			continue
		}
		out = append(out, SecretBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// SecretAccessors reads secret id's live IAM policy and returns the members
// granted SecretAccessorRole, sorted and deduplicated. Metadata only, like
// SecretPolicy, which it reads through: the one role Fugaro itself grants on
// a container.
func (s *Secrets) SecretAccessors(ctx context.Context, id string) ([]string, error) {
	bindings, err := s.SecretPolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range bindings {
		if b.Role != SecretAccessorRole {
			continue
		}
		for _, m := range b.Members {
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *Secrets) name(id string) string { return "projects/" + s.project + "/secrets/" + id }

func lastSegment(name string) string { return name[strings.LastIndex(name, "/")+1:] }
