package keychain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/redact"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The cloud copy of a pairing's MAC key (mobile plan Task P5.1e; the design of
// record is docs/mobile/design/always-on-orchestrator.md, its section 4.2 and
// its section 4.7 step 2b).
//
// **Why this exists at all.** The tag is a symmetric HMAC, so the process that
// verifies a phone's messages holds the same key the phone signs with — and
// phase 2's consumer runs in the installation's own project rather than on the
// operator's laptop. The key therefore has a THIRD copy: the phone's secure
// storage, the laptop's OS keychain (the Store this package is built around),
// and this. D111 and Global Constraint 4 fix the key in exactly two places, and
// the note that records the widening — the reader set, the grant, and
// `fugaro pair revoke` deleting the copy — is written in SECURITY.md, in D111's
// row and in Global Constraint 4.
//
// **One container per installation, one version per pairing.** The container is
// Terraform's (deploy/terraform/gcp/modules/installation/orchestrator.tf), and a
// pairing is not a Terraform act, so a container per pairing is not a shape this
// can use. A pairing puts a version in; `fugaro pair revoke` destroys the
// versions carrying its device uid. **Destroy, not disable**: the design's step
// 2b says why in one sentence — "Rotating the value is not enough unless the old
// version is destroyed; disabling the version is not enough if the service is
// granted `versions.access` on the parent".
//
// **What deleting the copy does not do**, and the note says this too: it ends
// the service's ability to verify and to forge, and it un-reads nothing — every
// principal that held the value keeps it, a project Owner or Editor among them,
// because their basic roles carry secretmanager.versions.access past any
// secret-level policy.
//
// **This is not a Store.** The keychain is keyed by an item's name and holds one
// key; this holds every pairing's key at once, keyed by the device uid the
// version carries, and it is read as a set (Keys) rather than one item at a
// time. That difference is the whole of why one container is enough for a whole
// installation.

// ErrNoCloud is the installation that has no pairing-key container: no
// orchestrator was ever opted into, or the installation predates this release.
// An installation without one keeps D111's two places exactly as they were —
// `fugaro pair` skips the copy rather than failing — which is why it is a value
// of its own and not ErrBackend.
var ErrNoCloud = errors.New("keychain: the installation has no pairing-key container")

// Cloud is one installation's copy of every pairing's MAC key, in Secret
// Manager: the container the installation module creates, read and written with
// the caller's own credential.
type Cloud struct {
	svc    *secretmanager.Service
	parent string
}

// NewCloud is the cloud source over project's container id. It takes the client
// rather than building one, so that the command that holds a config — and a
// fake, in a test — is the one that decides which project and which endpoint
// the calls reach (internal/cli's gcpOptions, internal/gcpfake's Secrets).
func NewCloud(svc *secretmanager.Service, project, id string) *Cloud {
	return &Cloud{svc: svc, parent: "projects/" + project + "/secrets/" + id}
}

// cloudRecord is one version's payload: the device uid the key belongs to, and
// the key in EncodeKey's spelling — the same string the OS keychain item holds
// (pairStoreMACKey), so the two copies of one pairing's key are one value and
// not two encodings of it. The uid is a key and not a secret (M13), and the
// versions of one container are told apart by it rather than by their number,
// which nothing outside this file records.
type cloudRecord struct {
	UID string `json:"uid"`
	Key string `json:"key"`
}

// Put writes key as a new version of the container, carrying deviceUID.
//
// A container that is not there is ErrNoCloud rather than a failure: an
// installation that never opted into a resident orchestrator has no cloud
// consumer, so its pairing has no third copy to write and nothing to report.
func (c *Cloud) Put(ctx context.Context, deviceUID string, key Key) error {
	payload, err := json.Marshal(cloudRecord{UID: deviceUID, Key: string(key.Bytes())})
	if err != nil {
		return cloudBackend("building the pairing record: " + err.Error())
	}
	_, err = c.svc.Projects.Secrets.AddVersion(c.parent, &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString(payload)},
	}).Context(ctx).Do()
	if isNotFound(err) {
		return noCloud(c.parent)
	}
	return cloudErr(err, payload)
}

// Delete destroys every version of the container that carries deviceUID — a
// DISABLED one among them — and returns how many it destroyed.
//
// It is the design's revocation step 2b, and the unit is the version: one
// pairing's key is one version, so a revoke destroys that one and leaves every
// other pairing's alone. A container that is not there, or one that holds no
// version for that uid, is not an error — absence is the state the caller asked
// for, exactly as it is for the keychain Store's Delete.
//
// **A DISABLED version is destroyed too, and the read is what that costs.** A
// disabled version's value is not served at all, so a version an operator
// disabled — or a rotation left behind, internal/cli/creds.go's own idiom —
// would be invisible to a listing that saw only enabled ones, and a revoke that
// took "no ENABLED version carries this uid" for "nothing to destroy" would
// report a pairing revoked with its key still in the container: the failure the
// note in SECURITY.md says is refused and reported rather than passed over.
func (c *Cloud) Delete(ctx context.Context, deviceUID string) (int, error) {
	gone, err := c.sweep(ctx, func(uid string) bool { return uid == deviceUID })
	return len(gone), err
}

// Sweep destroys every version whose device uid live refuses — the retirement
// J5c's sweep runs — and answers the uids it destroyed, oldest version first.
//
// **This is the half of the sweep that no laptop's own state can name.** The
// config's mailbox block lists the items one machine recorded, and a version can
// outlive every record of it: a pairing from another laptop, an item the
// operator never wrote down, a pairing whose delivery failed after the copy
// landed. The container is the one place the whole set is written down, so it is
// read here and every version is held against the live device set the caller
// passes. A version whose uid no device node names is key material no phone is
// admitted by (D166), and it is exactly what the cloud consumer would go on
// verifying a message with.
func (c *Cloud) Sweep(ctx context.Context, live func(uid string) bool) ([]string, error) {
	return c.sweep(ctx, func(uid string) bool { return !live(uid) })
}

// sweep destroys every version whose uid doomed admits, and answers those uids.
// One loop, so `Delete` and `Sweep` are one reading of the container and one
// enable/read/destroy dance: a second copy of it would be a second place a
// version could be passed over.
func (c *Cloud) sweep(ctx context.Context, doomed func(uid string) bool) ([]string, error) {
	versions, err := c.versions(ctx)
	if errors.Is(err, ErrNoCloud) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, v := range versions {
		uid, destroyed, err := c.revokeVersion(ctx, v, doomed)
		if err != nil {
			return gone, err
		}
		if destroyed {
			gone = append(gone, uid)
		}
	}
	return gone, nil
}

// revokeVersion destroys the version at v if doomed admits the uid it carries,
// and answers that uid and whether it destroyed it.
//
// A version whose state serves no read is ENABLED so that it can be attributed
// at all — the design's step 2b sentence is about this very state, and a
// version a revoke cannot read is one it cannot destroy — and put back DISABLED
// unless the destroy took it: an enable left behind would hand another pairing's
// key back to the consumer, which is the opposite of what this file is for.
func (c *Cloud) revokeVersion(ctx context.Context, v cloudVersion, doomed func(uid string) bool) (uid string, destroyed bool, err error) {
	if v.state != "ENABLED" {
		if err := c.enableVersion(ctx, v.name); err != nil {
			return "", false, err
		}
		defer func() {
			if destroyed {
				return
			}
			if back := c.disableVersion(ctx, v.name); back != nil {
				err = errors.Join(err, fmt.Errorf("the version %s could not be left DISABLED again: %w", v.name, back))
			}
		}()
	}
	uid, _, err = c.record(ctx, v.name)
	if err != nil {
		return "", false, err
	}
	if !doomed(uid) {
		return uid, false, nil
	}
	if _, err := c.svc.Projects.Secrets.Versions.Destroy(v.name, &secretmanager.DestroySecretVersionRequest{}).Context(ctx).Do(); err != nil {
		return uid, false, cloudErr(err, nil)
	}
	return uid, true, nil
}

// enableVersion and disableVersion are one version's two state calls, and the
// pair a revoke makes around a version whose value it cannot otherwise read. A
// state call that did not land is the container's failure like every other
// call's here — one of cloudFailure's — and never the OS keychain's.
func (c *Cloud) enableVersion(ctx context.Context, name string) error {
	_, err := c.svc.Projects.Secrets.Versions.Enable(name, &secretmanager.EnableSecretVersionRequest{}).Context(ctx).Do()
	return cloudErr(err, nil)
}

func (c *Cloud) disableVersion(ctx context.Context, name string) error {
	_, err := c.svc.Projects.Secrets.Versions.Disable(name, &secretmanager.DisableSecretVersionRequest{}).Context(ctx).Do()
	return cloudErr(err, nil)
}

// Keys reads every pairing's key as a map from the device uid the version
// carries to the key. It is what the cloud consumer verifies with (P5.1f), and
// it is the reason one container is enough: the whole set arrives in one read,
// because the pass has to answer for any device of the installation.
//
// A uid appearing in more than one version — a re-pair that put a second
// version in before the first was destroyed — resolves to the NEWEST of them,
// so a superseded version's key never verifies anything. A container that is
// not there is ErrNoCloud: an installation with no copy has a consumer with
// nothing to verify with, which is a different answer from an empty one.
func (c *Cloud) Keys(ctx context.Context) (map[string]Key, error) {
	versions, err := c.versions(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Key, len(versions))
	for _, v := range versions {
		// A version that is not ENABLED is no version for a consumer, and its
		// value could not be read anyway: a reader that did not skip one would
		// fail rather than fall back.
		if v.state != "ENABLED" {
			continue
		}
		uid, key, err := c.record(ctx, v.name)
		if err != nil {
			return nil, err
		}
		out[uid] = key
	}
	return out, nil
}

// cloudVersion is one version's number, the full name every other call takes,
// and the state the container answered it in.
type cloudVersion struct {
	n     int
	name  string
	state string
}

// versions lists the container's versions, oldest first, with the state each
// answered in: a caller reading them in order keeps the newest of a uid, and
// what a state means is the caller's to say. It is the one place this file
// reads the list, so the two callers keep their own reading of a state that is
// not ENABLED: `Keys` skips one — a disabled version is no version for a
// consumer, the same reading the readiness gates take — while `Delete` must
// not, or a revoke would pass a version over and report a success the container
// does not agree with. A DESTROYED version is dropped here, since nothing of it
// is left to read or to take away, and the number is read off the name rather
// than taken from the order the server answered in.
func (c *Cloud) versions(ctx context.Context) ([]cloudVersion, error) {
	var out []cloudVersion
	err := c.svc.Projects.Secrets.Versions.List(c.parent).Pages(ctx, func(p *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range p.Versions {
			if v.State == "DESTROYED" {
				continue
			}
			n, err := strconv.Atoi(v.Name[strings.LastIndexByte(v.Name, '/')+1:])
			if err != nil {
				continue
			}
			out = append(out, cloudVersion{n: n, name: v.Name, state: v.State})
		}
		return nil
	})
	if err != nil {
		if isNotFound(err) {
			return nil, noCloud(c.parent)
		}
		return nil, cloudErr(err, nil)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n < out[j].n })
	return out, nil
}

// record reads the version at name and returns the uid it carries and its key.
// A version that is not a record this build wrote is refused rather than
// skipped: the container's only writers are `fugaro pair` and `fugaro pair
// revoke`, so anything else is hand-written, and a consumer that skipped one
// would verify messages with a set it cannot describe.
func (c *Cloud) record(ctx context.Context, name string) (string, Key, error) {
	r, err := c.svc.Projects.Secrets.Versions.Access(name).Context(ctx).Do()
	if err != nil {
		return "", Key{}, cloudErr(err, nil)
	}
	if r.Payload == nil {
		return "", Key{}, cloudBackend("the version " + name + " carries no payload")
	}
	raw, err := base64.StdEncoding.DecodeString(r.Payload.Data)
	if err != nil {
		return "", Key{}, cloudBackend("the version " + name + " is not base64")
	}
	var rec cloudRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", Key{}, cloudBackend("the version " + name + " is not a pairing record")
	}
	if rec.UID == "" {
		return "", Key{}, cloudBackend("the version " + name + " names no device uid")
	}
	key, why := keyFrom([]byte(rec.Key))
	if why != "" {
		return "", Key{}, cloudBackend("the version " + name + " holds no key: " + why)
	}
	return rec.UID, key, nil
}

// noCloud is the installation-without-a-container answer, naming the container
// that is not there: the operator asking why nothing was written is owed the
// name to look for.
func noCloud(parent string) error { return fmt.Errorf("%w: %s", ErrNoCloud, parent) }

// cloudFailure is a container the caller could not use, and it is ErrBackend
// under a sentence of its own. **The sentinel is shared and the words are not**:
// ErrBackend is the key source's availability row — the row mac.go's §8.3 exit
// and the commands' exit codes are written against — so a container that could
// not be read or written has to answer `errors.Is(err, ErrBackend)` exactly as an
// item that could not be, or the two key sources would classify differently for
// no reason a caller can see. What it must NOT answer with is ErrBackend's own
// text, which says "the OS keychain could not be used": this failure is a
// project's Secret Manager, and an operator sent to their laptop's keychain for
// it is sent to fix the wrong thing.
type cloudFailure struct{ why string }

func (e cloudFailure) Error() string {
	return "keychain: the installation's pairing-key container could not be used: " + e.why
}

func (e cloudFailure) Unwrap() error { return ErrBackend }

// cloudBackend is backendErr's shape for this file: the failure described by
// what is safe to say, and never a byte of a key. It carries no item, because
// this source holds every pairing's key at once — which is the container and not
// an item, so the container is what a refusal names.
func cloudBackend(why string) error { return cloudFailure{why: why} }

// isNotFound is a Secret Manager call that answered 404: the container is not
// there. It is read as the absence the caller asked about rather than as a
// failure, which is why the two callers that can meet it — a write and a
// version list — ask this of the error before cloudErr sees it. A nil error is
// never a 404, so the callers may pass one.
func isNotFound(err error) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && ge.Code == http.StatusNotFound
}

// cloudErr is a Secret Manager failure as this package reports it: the server's
// text bounded to one printable field (safetext: a server may echo a body) and
// the payload's own bytes replaced out of it (redact: a server may echo the
// body it was sent, raw or base64 as the request carried it). payload is the
// bytes the call sent, or nil for a call that sent none — a read asks for a
// value rather than carrying one, so there is nothing of the caller's to redact
// its answer against.
//
// A payload shorter than redact's floor registers no form at all, which is why
// the empty spelling a nil payload becomes is safe to pass: the floor exists so
// that substituting a four-byte string cannot mangle ordinary text.
func cloudErr(err error, payload []byte) error {
	if err == nil {
		return nil
	}
	forms := redact.Forms([]string{string(payload)})
	return cloudBackend(safetext.Field(redact.ReplaceAll(err.Error(), forms)))
}
