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
	return &Secrets{svc: svc, project: o.GCPProject}, nil
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

// Set stores value as a new version of secret id, creating the secret with
// labels and automatic replication if it does not exist. An existing secret
// must carry every one of labels, or Set refuses with ErrForeignSecret. It
// returns the new version's ID ("1", "2", …).
//
// Errors can carry what the server sent back; the caller redacts value from
// them before showing them.
func (s *Secrets) Set(ctx context.Context, id string, value []byte, labels map[string]string) (string, error) {
	name := s.name(id)
	sec, err := s.svc.Projects.Secrets.Get(name).Context(ctx).Do()
	switch {
	case isStatus(err, http.StatusNotFound):
		create := &secretmanager.Secret{Labels: labels, Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{}}}
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
	v, err := s.svc.Projects.Secrets.AddVersion(name, &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString(value)},
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("adding a version to secret %s: %w", id, err)
	}
	return lastSegment(v.Name), nil
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
