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

// Secrets manages a repository's secrets in Secret Manager. It never reads
// a value back: Fugaro only writes secrets, and the platform mounts them.
type Secrets struct {
	svc     *secretmanager.Service
	project string
}

// SecretInfo describes a secret without its value.
type SecretInfo struct {
	ID       string            `json:"id"`
	Created  time.Time         `json:"created"`
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
	return &Secrets{svc: svc, project: o.Project}, nil
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

func (s *Secrets) name(id string) string { return "projects/" + s.project + "/secrets/" + id }

func lastSegment(name string) string { return name[strings.LastIndex(name, "/")+1:] }
