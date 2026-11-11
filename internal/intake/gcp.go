// gcp.go is the intake's two production adapters: Secret Manager behind
// SecretStore, and the runs bucket's derived admin list behind AdminList. The
// routes are in server.go and handlers.go and know neither, which is what lets
// every rule they carry be held by a test without a network.
//
// Both adapters are shaped by what DS2.2 grants the intake and by nothing
// else. The Secret Manager side holds create and list unconditionally and
// version-add and delete under the name-prefix condition (F30) — no accessor,
// no secrets.get, no versions.list — so a container is described and listed
// through the one list call, over the labels this package writes, and there is
// no method here that could read a version's payload. The other side is an
// object read on admin/intake-admins.json alone: the intake holds no database
// role (D1940), so the roster reaches it as a document `fugaro update`
// publishes.

package intake

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/secretname"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// SecretManager is the intake's reach in Secret Manager, as the intake's own
// account (DS2.2). It is constructed with the project the containers live in
// and holds no key, no bundle and no database handle: the only thing it can
// do is the five calls SecretStore declares.
type SecretManager struct {
	svc     *secretmanager.Service
	project string
}

// NewSecretManager connects to Secret Manager for project. The options are the
// client's own (a test points them at internal/gcpfake; production passes
// none, which is the environment's own credentials — the intake's service
// account).
//
// A project with no name is refused here rather than at the first call: every
// route of this service addresses a container by its project, so an intake
// that does not know one is one that would answer every call with a failure of
// its own making.
func NewSecretManager(ctx context.Context, project string, opts ...option.ClientOption) (*SecretManager, error) {
	if project == "" {
		return nil, errors.New("intake: no project: the containers this service writes live in the installation's GCP project")
	}
	svc, err := secretmanager.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Secret Manager: %w", err)
	}
	return &SecretManager{svc: svc, project: project}, nil
}

// Describe answers one container's metadata, and whether a container of this
// intake's own shape is there.
//
// There is no secrets.get in the grant, so it is the list call with the labels
// this package writes as the filter — the account id the container's name
// carries, under the container's scope — and the entry whose ID is exactly the
// one asked for. "Not found" therefore covers two states, and the write path
// tells them apart: a name no container has, and a container another writer
// made without the intake's labels. Neither is written into: the first is
// created, and the second is refused when the create answers ErrExists and the
// second read does not find it either (R12).
func (s *SecretManager) Describe(ctx context.Context, id string) (Secret, bool, error) {
	n, err := secretname.Parse(id)
	if err != nil {
		return Secret{}, false, fmt.Errorf("describing %s: %w", id, err)
	}
	found, err := s.list(ctx, describeLabels(n))
	if err != nil {
		return Secret{}, false, err
	}
	for _, sec := range found {
		if sec.ID == id {
			return sec, true, nil
		}
	}
	return Secret{}, false, nil
}

// Create makes the container, or answers ErrExists. The labels are the
// intake's own scope labels and the annotations are R10's, and the replication
// is automatic: the shape a container the intake wrote has, which the check
// before every later write holds it to.
//
// A 409 is ErrExists and not a failure: the caller of SecretStore is the write
// path, which reads the container again and puts it to the shape check rather
// than trusting that the first read settled it (R12).
func (s *SecretManager) Create(ctx context.Context, id string, annotations, labels map[string]string) error {
	_, err := s.svc.Projects.Secrets.Create(s.parent(), &secretmanager.Secret{
		Annotations: annotations,
		Labels:      labels,
		Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{}},
	}).SecretId(id).Context(ctx).Do()
	switch {
	case isStatus(err, http.StatusConflict):
		return ErrExists
	case err != nil:
		return fmt.Errorf("creating secret %s: %w", id, err)
	}
	return nil
}

// AddVersion stores value as a new version and answers its number. The value
// is bytes on the way in and never on the way out: there is no call in this
// file that reads a payload, and the account holds none either.
//
// An error from here can carry what the server sent back, which is why the
// routes answer a body they composed and never a store error's text.
func (s *SecretManager) AddVersion(ctx context.Context, id string, value []byte) (string, error) {
	v, err := s.svc.Projects.Secrets.AddVersion(s.name(id), &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64Value(value)},
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("adding a version to secret %s: %w", id, err)
	}
	return lastSegment(v.Name), nil
}

// List answers the metadata of every container the intake wrote under one
// scope, narrowed by the scope label and re-checked against the name grammar.
//
// The re-check is the second half of the filter's job and not a formality: the
// name is the truth about which scope a container belongs to (R12), so a
// container the label filter answered with whose name is not of that scope is
// dropped rather than answered — it is not one this intake can account for.
func (s *SecretManager) List(ctx context.Context, scope Scope) ([]Secret, error) {
	found, err := s.list(ctx, map[string]string{LabelScope: string(scope)})
	if err != nil {
		return nil, err
	}
	out := make([]Secret, 0, len(found))
	for _, sec := range found {
		if ours(sec, scope) {
			out = append(out, sec)
		}
	}
	return out, nil
}

// Delete removes the container and every version in it. The grant conditions
// this call on the fugaro_u_/fugaro_p_ name prefix (F30), so a name outside
// that grammar is refused by the project's own policy as well as by the routes
// above.
func (s *SecretManager) Delete(ctx context.Context, id string) error {
	if _, err := s.svc.Projects.Secrets.Delete(s.name(id)).Context(ctx).Do(); err != nil {
		return fmt.Errorf("deleting secret %s: %w", id, err)
	}
	return nil
}

// list is the one call both Describe and List are made of: the project's
// secrets filtered by labels, as metadata. The terms are sorted so the filter
// string is a function of the set, and never empty — an empty filter would be
// the whole project, which is not a question either caller asks.
func (s *SecretManager) list(ctx context.Context, labels map[string]string) ([]Secret, error) {
	terms := make([]string, 0, len(labels))
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		terms = append(terms, "labels."+k+"="+labels[k])
	}
	var out []Secret
	err := s.svc.Projects.Secrets.List(s.parent()).Filter(strings.Join(terms, " AND ")).Pages(ctx, func(p *secretmanager.ListSecretsResponse) error {
		for _, sec := range p.Secrets {
			if sec != nil {
				out = append(out, secretFromAPI(sec))
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing the secrets of %s: %w", s.project, err)
	}
	return out, nil
}

func (s *SecretManager) parent() string        { return "projects/" + s.project }
func (s *SecretManager) name(id string) string { return s.parent() + "/secrets/" + id }

// isStatus reports an HTTP status in an API error, which is how Secret
// Manager's own refusals arrive (internal/backend/gcp's isStatus, for its
// callers).
func isStatus(err error, code int) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == code
}

// base64Value is a payload as Secret Manager's wire takes it: the value is
// bytes, and nothing in this package ever decodes one back.
func base64Value(value []byte) string { return base64.StdEncoding.EncodeToString(value) }

// lastSegment is the last "/" separated segment of a resource name: a version
// name's number, a container name's ID.
func lastSegment(name string) string { return name[strings.LastIndex(name, "/")+1:] }

// describeLabels is the filter that narrows one member's containers to the
// ones a describe could be about: the scope, and for a per-user container the
// account id its name carries. A shared container has no owner to narrow by —
// there are few of them and the name's exact match is what settles it.
func describeLabels(n secretname.Name) map[string]string {
	if n.Shared() {
		return map[string]string{LabelScope: string(ScopeShared)}
	}
	return map[string]string{LabelScope: string(ScopeUser), LabelOwnerSub: n.ID}
}

// secretFromAPI renders one container as this package's metadata: what it was
// made, what it carries as annotations, and the shape facts R12 refuses a
// container for. Nothing here is a version and nothing here is a payload.
func secretFromAPI(sec *secretmanager.Secret) Secret {
	created, _ := time.Parse(time.RFC3339Nano, sec.CreateTime)
	shape := Shape{TTL: sec.Ttl, ExpireTime: sec.ExpireTime}
	for _, t := range sec.Topics {
		if t != nil && t.Name != "" {
			shape.Topics = append(shape.Topics, t.Name)
		}
	}
	shape.CMEK = cmekOf(sec)
	if sec.Rotation != nil {
		shape.Rotation = rotationOf(sec.Rotation)
	}
	return Secret{ID: lastSegment(sec.Name), Created: created, Annotations: sec.Annotations, Shape: shape}
}

// cmekOf is the customer-managed key a container is bound to. The API can name
// one in three places — on the secret itself, on an automatic replication
// policy, or on any replica of a user-managed one — and a container carrying
// one anywhere is one this intake must not write into, so the first non-empty
// is the answer. Which of several replicas it came from does not matter: the
// refusal names the fact and not the key.
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

// rotationOf is the rotation schedule a container carries, as the text that
// says there is one: the period where it has one, the next rotation time
// otherwise, and the fact itself where the API answered a schedule with
// neither spelled out. The intake never sets one, so any of them is a
// container another writer made (R12).
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

// BucketAdminList is the derived admin list (DS2.0), read from the runs
// bucket object `fugaro update` publishes. It is the whole of the intake's
// contact with the roster: an object read, and no database role (D1940).
type BucketAdminList struct{ bucket *blobx.Bucket }

// NewBucketAdminList reads the list from this bucket. A nil bucket is refused:
// an intake that cannot read the list must not be built, because every route
// that writes the project's shared prefix or sweeps a member's containers
// decides from it, and a nil check at the call site would be the same refusal
// spelled later.
func NewBucketAdminList(bucket *blobx.Bucket) (*BucketAdminList, error) {
	if bucket == nil {
		return nil, errors.New("intake: no bucket: the derived admin list is an object of the runs bucket")
	}
	return &BucketAdminList{bucket: bucket}, nil
}

// Admins reads the object and parses it, every call and with no cache.
//
// The list is held to no copy of its own on purpose: D1940 accepts that it
// lags a role change until `fugaro update` runs, and a cache would hold that
// lag past the update that ended it — a member offboarded by an update would
// keep the shared prefix for as long as the cache lived. The object is small
// and it is read only where the shared prefix or a sweep is in play, so the
// read is what the call pays for the answer being current.
//
// A read that fails is an error and never an empty list: the caller decides
// from this document, and "I could not read who the admins are" must not be
// answered as "there are none" (or as "it is fine").
func (a *BucketAdminList) Admins(ctx context.Context) ([]access.Admin, error) {
	raw, _, err := a.bucket.ReadMaxStrict(ctx, access.PathAdminList, access.MaxAdminListBytes)
	if err != nil {
		return nil, fmt.Errorf("reading %s from the runs bucket: %w", access.PathAdminList, err)
	}
	list, err := access.ParseAdminList(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", access.PathAdminList, err)
	}
	return list, nil
}
