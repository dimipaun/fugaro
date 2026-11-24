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
// The fence around this read is IAM's, and it is on this tree. The service's
// account holds one unconditioned accessor on the bundle's container (Q6,
// modules/installation/credentials.tf, which
// TestCredentialsServiceGrantsAreTheOneBundle keeps true) and one conditioned
// accessor over the grammar's two prefixes (credentials_prefix_accessor:
// roles/secretmanager.secretAccessor under intake.tf's
// dynamic_secret_names_condition, resource names beginning with fugaro_u_ or
// fugaro_p_). A per-secret name is therefore read by its exact ID, and a name
// outside the grammar is refused by the project's own policy as well as by
// secretname.Parse. The account's third grant is the lister (credentials_lister:
// the fugaroSecretLister role's secretmanager.secrets.list, unconditioned),
// which is what the workflow-secret MARKER is read through (F18, G2): the
// marker is a label, and no resource-name condition can carry a list.
//
// No candidate list carries a per-secret name until the slip's own does
// (DS1.6), so a per-secret name behaves as it always did (G7, G12) and what
// reaches this file is a name the caller supplied and the slip admitted. It
// reads that name and no other: there is no listing of the grammar here and no
// search.
//
// R6: what THIS SERVICE does moves nothing. It resolves a credential in order —
// it reads, it never writes, and no value it reads is carried anywhere but into
// the call it was read for — so a rotation is never a migration here, and the
// bundle's values stay the ones their owners wrote by hand (who cannot read them
// back, which is why nothing in this tree may rewrite one). That is the rule the
// exception below is an exception TO, and it is stated as this file's own rather
// than as a law of the two stores because it no longer is one: one command
// outside the service moves a value between them, deliberately and once, and it
// is named below. What the service does instead is resolve in order: the per-secret
// container FIRST, and the bundle's entry for the same credential second. A
// container that is not there — none, or one with no version — is R7's fall to
// the bundle, which is why an installation that has rotated nothing behaves
// exactly as it did before this file existed (G7), and why a slip-less older
// CLI is served the bundle's value after a rotation (D1945, accepted:
// `update` prompts the upgrade).
//
// **The one exception is KS12's copy** (internal/cli/update_keys.go, D3001,
// R4), and it is an exception to the rule above in the direction the rule is
// about and not around it: the same project owner, with the IAM that already
// reads both containers, reads each model key this installation holds outside the
// bundle — a provider's, for every repository that may send code to it, and a
// repository's own Anthropic key — out of its container and writes the value into
// the bundle entry the service serves that repository, once, in one version,
// behind the confirmation of their own `fugaro update`. Nothing a rotation does is that —
// a rotation is a person's new value landing in the container the intake
// writes, and this file's read is what serves it, unchanged. What the copy
// exists for is the end of the mount: a key that is only in a container is a
// key no released run is served, so the owner moves it into the document their
// service reads. Three things keep it inside the rule: it never overwrites an
// entry it did not write (it records the container and the version it read on
// every entry it does write, and an entry without that marker is left alone and
// named), it reads nothing the service could not have served, and it is
// unreachable from this file — no line of the read below writes anything, and
// no value it reads is copied anywhere.
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
	"strings"

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

// ErrPersonalTokenAbsent is R7's refusal for a call that names a person's own
// token and finds none: the container is not there at all, or holds no version
// (D2974).
//
// **It is a class of its own because this candidate must not fall to the
// bundle.** R6's fall is what every other per-secret name does — a rotation the
// bundle also holds a value for is served the bundle's entry when no container
// answers (an installation that has rotated nothing behaves as it always did,
// G7) — and it is exactly what D2974 refuses for the personal name, whose held
// credential name is the SHARED subscription token's: config.ClaudeOAuthToken is
// the default account's secret, so the credential the fall names is the
// workspace's own token, served to a run whose slip named a PERSON's token
// through no allow-list of its own and counted as unbilled, because the tier is
// the candidate's.
//
// What that fall answers today is usually a sentence rather than that token, and
// the class is here so that it is a RULE either way. The bundle's index holds
// the RESOLVED account name (config.DefaultClaudeAccount) and the fall names the
// credential the grammar holds, so with the ordinary document the person who has
// stored nothing meets "the selected credential is not in the bundle" — a
// sentence about a document they cannot see, naming no command. And an account
// may legally be NAMED after the credential (the bundle reserves that spelling
// for the git entry alone), in which case that name IS in the index and the fall
// serves that account's token instead: the state
// TestAMissingPersonalTokenRefusesEvenWhereTheBundleHoldsThatName builds, and
// the one D2974 is about.
//
// Its sentence names the command that sets a token and carries no container, no
// credential name and no value: a run reads what to do, and nothing about where
// the service looked.
var ErrPersonalTokenAbsent = errors.New(personalTokenAbsent)

// personalTokenAbsent is the sentence a run is answered with, on the wire and on
// the operator's own line.
const personalTokenAbsent = "the personal token this call's slip names is not there: a person sets one at the workspace with `fugaro secrets user set`"

// personalTokenName reports whether a parsed per-secret name is the one personal
// name this plan serves: a person's own claude-oauth-token, under their own
// prefix (A1, R6). A project-shared name of that spelling is not it — this half
// serves no `fugaro_p_` name — and neither is an account's own token
// (`claude-oauth-token-<account>`), which is a per-user rotation like any other
// and keeps R6's fall.
func personalTokenName(n secretname.Name) bool {
	return !n.Shared() && n.Name == creds.PersonTokenName
}

// ErrWorkflowSecretContainer is a candidate whose container carries the intake's
// workflow-secret marker (F18, G2): it holds a workflow secret, and the half of
// the resolution below serves model credentials.
//
// **It is refused and not fallen from.** The bundle's entry under the same name
// is a different credential — the marker exists because `fugaro_p_` is also the
// grammar of the project's model keys, so a name can be both a workflow secret's
// and an entry the operator wrote — and serving that entry for a candidate whose
// container is marked would be exactly the shadowing the marker keeps apart, in
// the other direction. The value is never read: the marker is checked first, so
// a workflow secret's value does not enter this process on this path at all.
var ErrWorkflowSecretContainer = errors.New("the container this credential names holds a workflow secret, and no model credential is served from one")

// WorkflowSecretMarkers answers whether one container carries the intake's
// workflow-secret marker (F18, G2). It is an interface of its own beside
// PerSecretSource because it is a second question about the same container, read
// through a different grant, and because a source that reads no metadata simply
// cannot answer it — which the callers treat as a read that failed rather than
// as "no marker".
type WorkflowSecretMarkers interface {
	// Marked reports whether the container with this secret ID carries the
	// intake's workflow-secret marker. err is a read that failed, which fails
	// the call closed: a container whose labels could not be listed is not one
	// this service may serve a workflow secret from.
	Marked(ctx context.Context, secretID string) (bool, error)
}

// markedContainer answers whether the source's container with this secret ID
// carries the intake's marker.
//
// A source that cannot answer — none at all, or one that reads no metadata — is
// an ERROR and never a false: "the marker could not be read" and "the container
// carries no marker" are two states with two different fixes, and answering the
// second for the first would send an operator to rewrite a container that is
// already right.
func markedContainer(ctx context.Context, src PerSecretSource, secretID string) (bool, error) {
	m, ok := src.(WorkflowSecretMarkers)
	if !ok {
		return false, fmt.Errorf("%w: this service has no reader for the workflow-secret marker", ErrPerSecretUnreadable)
	}
	return m.Marked(ctx, secretID)
}

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
// What that account may reach is IAM's and is DS2.2's: the
// prefix-conditioned accessor covers the grammar's two prefixes and nothing
// else, so a per-secret read answers a permission failure (the class below) or
// an absence, and serves no value (the file's header).
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

// Marked reports whether one container carries the intake's workflow-secret
// marker (F18, G2), read through the account's lister grant.
//
// It is a LIST narrowed by name and never a secrets.get: the account holds no
// get on the grammar, and the marker is a label — which no resource-name
// condition can carry, so the lister is unconditioned and this read is the
// reason it exists. The filter is a narrowing the API applies and not the
// answer: the entries are matched on the exact ID their resource name ends
// with, so a container whose name merely BEGINS with this one's —
// fugaro_p_deploy-token beside fugaro_p_deploy-token-2 — is not read as it.
//
// A container that is not there answers false: a name with no container is not
// a marked one, and every caller of this already refuses on its own terms.
func (s SecretManagerPerSecrets) Marked(ctx context.Context, secretID string) (bool, error) {
	if s.Service == nil || s.Project == "" || secretID == "" {
		return false, fmt.Errorf("%w: the service has no Secret Manager client and no container to read a marker from", ErrPerSecretUnreadable)
	}
	parent := "projects/" + s.Project
	marked := false
	err := s.Service.Projects.Secrets.List(parent).
		Filter("name:"+parent+"/secrets/"+secretID).
		Pages(ctx, func(p *secretmanager.ListSecretsResponse) error {
			for _, sec := range p.Secrets {
				if sec != nil && lastSegment(sec.Name) == secretID && sec.Labels[workflowSecretLabel] == workflowSecretMarker {
					marked = true
				}
			}
			return nil
		})
	if err != nil {
		return false, fmt.Errorf("%w: the container's labels could not be listed", ErrPerSecretUnreadable)
	}
	return marked, nil
}

// lastSegment is the last "/" separated segment of a resource name: a container
// name's ID (internal/backend/gcp's helper of the same name, for this package's
// two readers of a Secret Manager resource name).
func lastSegment(name string) string { return name[strings.LastIndex(name, "/")+1:] }

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
//	("", false, "", ErrPersonalTokenAbsent)
//	                        the candidate is the personal name this plan serves
//	                        and no container answered it: R7 refuses rather than
//	                        fall (D2974)
//	("", false, "", err)    the read FAILED: the caller refuses (D143)
//
// name is the credential name the bundle is to be asked for when nothing
// per-secret answered: the name the per-secret name HOLDS for a candidate of
// the grammar (R7's fall — the bundle's entry for the credential the rotation
// was of), and the candidate's own name for every other candidate. It is empty
// on the failure, which the caller does not fall from.
//
// **The personal name is the one candidate of the grammar with no fall**, and
// the name is the whole of how the two are told apart: a personal candidate is
// the same container `fugaro secrets user set` writes, so a caller cannot tell
// the personal half's read from a rotation's by anything but the name it is
// reading (personalTokenName). What a fall would serve there is the shared
// subscription token, which is the one thing D2974 forbids — see
// ErrPersonalTokenAbsent.
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
		// entry a per-secret name stands for (G7). The personal name is the one
		// exception, and it is refused here rather than fallen from for the same
		// reason it is refused below: a service with no per-secret reader holds
		// no container of the person's either.
		if personalTokenName(parsed) {
			return Entry{}, false, "", ErrPersonalTokenAbsent
		}
		return Entry{}, false, parsed.Name, nil
	}
	// The marker FIRST, before the value is read at all (F18, G2): a container
	// the intake wrote a workflow secret into is not one this half may serve,
	// and reading it before asking would put a workflow secret's value into
	// this process for a call that is refused. A marker that could not be read
	// is the failure class, because a container this service cannot classify is
	// one it must not serve.
	marked, err := markedContainer(ctx, d.Source, c.Name)
	if err != nil {
		return Entry{}, false, "", err
	}
	if marked {
		return Entry{}, false, "", ErrWorkflowSecretContainer
	}
	value, found, err := d.Source.Value(ctx, c.Name)
	if err != nil {
		return Entry{}, false, "", err
	}
	if !found {
		if personalTokenName(parsed) {
			return Entry{}, false, "", ErrPersonalTokenAbsent
		}
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
