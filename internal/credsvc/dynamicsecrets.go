package credsvc

// The per-secret read (DS3.3, R6 and R7): where a user-supplied credential
// comes from, and the order it is tried in.
//
// A credential an operator wrote into the bundle is served by the bundle, as it
// always was. A credential a person rotated through the intake (DS2.1) lives in
// a Secret Manager container of its own, named by the one grammar
// (internal/secretname: fugaro_u_<id>_<name> and fugaro_p_<name>), and this file
// reads it by that exact name — never by a listing, and never by building one
// (D1931, G2, G12).
//
// The fence around this read is IAM's and not this file's, and it is NOT on this
// tree yet. The service's account holds one unconditioned accessor, on the
// bundle's container alone (Q6, modules/installation/credentials.tf, which
// TestCredentialsServiceGrantsAreTheOneBundle keeps true), and the
// prefix-conditioned accessor over the grammar's two prefixes is DS2.2's task —
// one that also deploys the intake that writes these containers, and that is
// gated on the owner's own live check (DS0.3). So nothing on this tree serves a
// per-secret container's value: a read is either refused, which is a permission
// failure and the class below (the call fails closed), or answered as an
// absence, which is R7's fall to the bundle's entry — and no candidate list
// carries a per-secret name at all until the slip's own does (DS1.6). The two
// land together; until then a per-secret name behaves exactly as it did before
// this file existed (G7, G12).
//
// R6: nothing moves. `fugaro update` cannot read the bundle's values (they were
// written by hand, and the people who wrote them cannot read them back), so a
// rotation is never a migration and no value is ever copied from one store to
// the other. What the service does instead is resolve in order: the per-secret
// container FIRST, and the bundle's entry for the same credential second. A
// container that is not there — none, or one with no version — is R7's fall to
// the bundle, which is why an installation that has rotated nothing behaves
// exactly as it did before this file existed (G7), and why a slip-less older
// CLI is served the bundle's value after a rotation (D1945, accepted:
// `update` prompts the upgrade).
//
// A read that FAILED is neither an absence nor a fallback: the call is refused
// (D143, R7). A service that answered the bundle's value because Secret Manager
// was unreachable would serve — silently, with no line an operator could act on
// — the very value the operator meant to retire, and it would do it for every
// run of the installation at once.
//
// What is deliberately NOT here: the policy, the pools and the slip. Which
// per-secret names a run may be served is the slip's own list (creds.Slip's
// Allowed), decided when the slip is issued — DS3.2 authorises it against the
// pools there, and DS1.6 holds every later call to it; this file
// reads the name it is handed, and no per-secret name it is not handed can
// reach a container: the read is by exact name and there is no listing and no
// search.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/secretname"
	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// ErrPerSecretUnreadable is the class of every failure of a per-secret read.
// Its text is also the sentence a RUN is told (perSecretRefusal), because the
// two are one class: Secret Manager's own error names the container and the
// request, which is the operator's business and not a run's, and a refusal is
// a class and never another package's sentence (the rule BundleRefusal states
// for the bundle, read here the other way round because there is one class and
// not six).
var ErrPerSecretUnreadable = errors.New(perSecretRefusal)

// perSecretRefusal is what a run is told when the read failed, and it carries
// no value, no secret ID and no key of any kind: a run learns that the
// credential it was to be served could not be read, and nothing about where the
// service looked.
const perSecretRefusal = "the per-secret credential could not be read"

// PerSecretSource reads one per-secret container. It is an interface so a test
// can answer one without a cloud call (Convention 9), and
// SecretManagerPerSecrets is the only implementation the service deploys.
type PerSecretSource interface {
	// Value reads the newest version of the container with this secret ID.
	// found=false is a container, or a version of one, that is not there —
	// which is R6's fall to the bundle and not a failure — and err is a read
	// that failed, which fails the call closed. An implementation refuses
	// rather than answering an empty value: an empty credential is not one.
	Value(ctx context.Context, secretID string) (value string, found bool, err error)
}

// SecretManagerPerSecrets reads a per-secret container as the service's own
// service account: projects/<project>/secrets/<secret ID>/versions/latest
// :access, by the exact ID the grammar spelled and with nothing else asked of
// the API — no listing, no write, no version named. It is the same client and
// the same project the bundle is read with.
//
// What that account may reach is IAM's and is DS2.2's: until the
// prefix-conditioned accessor lands, the account holds no grant on any container
// but the bundle's, so a per-secret read answers a permission failure (the class
// below) or an absence, and serves no value (the file's header).
type SecretManagerPerSecrets struct {
	// Project is the GCP project the containers live in.
	Project string
	// Service is the Secret Manager client, and it is the one the bundle's own
	// source holds. nil is a source that refuses: a reader built without one
	// must not answer a value.
	Service *secretmanager.Service
}

// Value reads one container's newest version.
//
// A 404 is the absence R6 falls from, and it is the ordinary case: nothing has
// been rotated under that name, so the bundle's entry serves the call. Every
// other failure — a 403 for a name the service's accessor does not cover, a
// dead transport, a payload that is not the base64 string Secret Manager
// carries — is the class above, and the caller refuses on it.
func (s SecretManagerPerSecrets) Value(ctx context.Context, secretID string) (string, bool, error) {
	if s.Service == nil || s.Project == "" || secretID == "" {
		return "", false, fmt.Errorf("%w: the service has no Secret Manager client and no per-secret name to read", ErrPerSecretUnreadable)
	}
	name := "projects/" + s.Project + "/secrets/" + secretID + "/versions/latest"
	r, err := s.Service.Projects.Secrets.Versions.Access(name).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		switch {
		case errors.As(err, &ge) && ge.Code == http.StatusNotFound:
			return "", false, nil
		case errors.As(err, &ge) && ge.Code == http.StatusForbidden:
			return "", false, fmt.Errorf("%w: the service is not allowed to read that container", ErrPerSecretUnreadable)
		}
		// The message names the container, which is a name and not a value.
		return "", false, fmt.Errorf("%w: %v", ErrPerSecretUnreadable, err)
	}
	if r.Payload == nil || r.Payload.Data == "" {
		// A version with no payload is not a credential: it is answered as a
		// failure rather than as an absence, because an absence would quietly
		// serve the bundle's older value in its place.
		return "", false, fmt.Errorf("%w: the container holds a version with no value", ErrPerSecretUnreadable)
	}
	data, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return "", false, fmt.Errorf("%w: the payload is not base64", ErrPerSecretUnreadable)
	}
	value := string(data)
	if value == "" || len(value) > MaxBundleValueBytes {
		// One bound for both stores: what this service will serve a call is one
		// size whether it came from the document or from a container, and
		// MaxBundleValueBytes is the bound every other credential is held to.
		return "", false, fmt.Errorf("%w: the value is empty or larger than the %d bytes this service accepts", ErrPerSecretUnreadable, MaxBundleValueBytes)
	}
	return value, true, nil
}

// DynamicSecrets is the per-secret half of the resolution: the read of one
// container, in R6's order relative to the bundle.
type DynamicSecrets struct {
	// Source reads one container. nil is a reader that reads none, and read
	// answers every candidate with no per-secret reading — see perSecrets.
	Source PerSecretSource
}

// read answers the per-secret half of R6's order for one selected candidate:
//
//	(e, true, name, nil)    the container served it
//	("", false, name, nil)  there is no per-secret reading of this candidate —
//	                        its name is not one of the grammar's, or no
//	                        container (or no version) is there
//	("", false, "", err)    the read FAILED: the caller refuses (D143)
//
// name is the credential name the bundle is to be asked for when nothing
// per-secret answered: the name the per-secret name HOLDS for a candidate of
// the grammar (R7's fall — the bundle's entry for the credential the rotation
// was of), and the candidate's own name for every other candidate. It is empty
// on the failure, which the caller does not fall from.
//
// The name is read as it stands and is never built here: a per-secret name is
// an exact Secret Manager container ID, the grammar is what tells one from
// everything else (secretname's own doc), and a name the grammar does not admit
// is a bundle credential's — which this file must not read a container for,
// even where a container of that name happened to exist, because a name outside
// the two prefixes is not one the intake may write and not one the accessor
// covers.
func (d *DynamicSecrets) read(ctx context.Context, c creds.Credential) (Entry, bool, string, error) {
	parsed, err := secretname.Parse(c.Name)
	if err != nil {
		// A name the grammar does not admit is a bundle credential's: it is
		// asked for by its own name and no container is read for it, whether
		// or not this reader has a source.
		return Entry{}, false, c.Name, nil
	}
	if d == nil || d.Source == nil {
		// No per-secret reader at all: the name falls to the bundle exactly as
		// one whose container is not there does, and for the same reason — the
		// grammar, and not the reader's presence, is what decides which bundle
		// entry a per-secret name stands for (G7).
		return Entry{}, false, parsed.Name, nil
	}
	value, found, err := d.Source.Value(ctx, c.Name)
	if err != nil {
		return Entry{}, false, "", err
	}
	if !found {
		return Entry{}, false, parsed.Name, nil
	}
	// The entry is built rather than looked up: there is no bundle entry under
	// this name, and the tier is the one the candidate was selected at — the
	// rotation replaces a value and not a credential, so the tier the cascade
	// spoke stays the tier the call is served at. Its kind is the model
	// credential's for the same reason: the candidates a per-secret name can
	// reach this resolution in are the cascade's own (the slip's list is what
	// hands one over), and every other route resolves its value by kind — the
	// App key's, the git token's, the review signing keys' — which no
	// per-secret name is a candidate for (R7 reserves those).
	return Entry{Kind: KindModel, Name: c.Name, Tier: c.Tier, value: secret{value: value}}, true, "", nil
}

// perSecrets is the reader this service reads per-secret names with, derived
// from the source its bundle is read through rather than configured beside it:
// the deployment reads both through the one Secret Manager client and project
// it already holds (internal/cli/credentials_serve.go), and the accessor DS2.2
// gives this account is what scopes the containers it may reach. A reader that
// had to be wired separately would be a second place for the same client, and a
// service whose bundle source is not Secret Manager's — every hermetic test's
// fixture, and any installation reading its document another way — reads none.
//
// nil is therefore an ordinary answer and not a failure: a candidate whose name
// is a per-secret one is then served by the bundle's entry for it, exactly as
// every candidate was before this file existed (G7).
func (s *Server) perSecrets() *DynamicSecrets {
	if s.cfg.Bundles == nil {
		return nil
	}
	switch src := s.cfg.Bundles.Source.(type) {
	case PerSecretSource:
		// A source that reads containers itself (a test's): asked first,
		// because the one type that does both is a fixture's and not the
		// deployment's.
		return &DynamicSecrets{Source: src}
	case SecretManagerSource:
		if src.Service == nil || src.Project == "" {
			return nil
		}
		return &DynamicSecrets{Source: SecretManagerPerSecrets{Project: src.Project, Service: src.Service}}
	}
	return nil
}
