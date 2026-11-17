package keychain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The cloud source's tests (Task P5.1e). They run against internal/gcpfake's
// Secret Manager — a real client over an HTTP fake — so what is exercised is the
// calls this package makes and the records it writes, not a stub of them, and
// nothing here touches a real project (Global Constraint 10). The container's
// ID is the command's business and not this package's: NewCloud is given one,
// and these tests give it one of their own.
const (
	cloudProject   = "proj-1234"
	cloudContainer = "pairing-keys-for-tests"
	// cloudUID and cloudOther are device uids in the shape `fugaro pair` mints
	// (m~ and an unpadded base64url body). They are keys and not secrets (M13),
	// so they may be printed and compared.
	cloudUID   = "m~AAAAdeviceAAAAAAAAAAAAAAAA"
	cloudOther = "m~BBBBdeviceBBBBBBBBBBBBBBBB"
)

// cloudFixture is one fake installation's Secret Manager and a Cloud over its
// container.
type cloudFixture struct {
	t *testing.T
	f *gcpfake.Secrets
	c *Cloud
}

func newCloudFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := gcpfake.NewSecrets(t)
	svc, err := secretmanager.NewService(context.Background(),
		option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatalf("the fixture's Secret Manager client could not be built: %v", err)
	}
	return &cloudFixture{t: t, f: f, c: NewCloud(svc, cloudProject, cloudContainer)}
}

// seeded creates the container the installation module would have created, with
// no version in it: the state an installation that has opted in and paired
// nothing is in.
func (x *cloudFixture) seeded() {
	x.t.Helper()
	x.f.Seed(cloudContainer, map[string]string{"fugaro": "managed"}, nil)
}

// ctx is the tests' context.
func (x *cloudFixture) ctx() context.Context { return context.Background() }

// key is test material in the shape EncodeKey produces, and it is obviously not
// a secret: this package's tests never hold a real key (M5).
func cloudKey(name string) Key { return NewKey([]byte(name)) }

// TestCloudPutWritesTheRecordKeysReads: the write and the read are one value —
// the key as the phone holds it, under the uid `fugaro pair` minted — and the
// version the write adds is what the consumer's read finds.
func TestCloudPutWritesTheRecordKeysReads(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	key := cloudKey(testKey)
	if err := x.c.Put(x.ctx(), cloudUID, key); err != nil {
		t.Fatalf("the copy could not be written: %v", err)
	}
	x.f.AllowAccess()

	got, err := x.c.Keys(x.ctx())
	if err != nil {
		t.Fatalf("the copy could not be read back: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the container answered %d pairing(s), want the one that was written: %v", len(got), got)
	}
	back, ok := got[cloudUID]
	if !ok {
		t.Fatalf("the container answered %v, want the uid the record carries: the uid is what a revoke and a verification are keyed by", got)
	}
	if string(back.Bytes()) != string(key.Bytes()) {
		t.Errorf("the read answered a key of %d bytes, want the %d that were written: the cloud copy and the laptop's item hold one value, or the two ends sign differently", back.Len(), key.Len())
	}

	// The bytes on the wire, read back through the fake: the payload carries
	// the uid and the key, and nothing else (the version's own number is
	// Secret Manager's, and the record does not repeat it).
	var rec struct {
		UID string `json:"uid"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(x.f.Latest(cloudContainer), &rec); err != nil {
		t.Fatalf("the version's payload is not JSON: %v", err)
	}
	if rec.UID != cloudUID || rec.Key != string(key.Bytes()) {
		t.Errorf("the version carries uid %q and a key of %d bytes, want %q and the key written", rec.UID, len(rec.Key), cloudUID)
	}
}

// TestCloudPutRefusesWhenTheInstallationHasNoContainer: the switch, from the
// command's side. An installation that never opted into a resident orchestrator
// has no container, so its pairing has no third copy to write — ErrNoCloud, and
// not a failure the operator has to act on.
func TestCloudPutRefusesWhenTheInstallationHasNoContainer(t *testing.T) {
	x := newCloudFixture(t)
	err := x.c.Put(x.ctx(), cloudUID, cloudKey(testKey))
	if !errors.Is(err, ErrNoCloud) {
		t.Fatalf("Put on an installation with no container = %v, want ErrNoCloud: the copy is written only where a cloud consumer exists, and a pairing that cannot write one is still a pairing", err)
	}
	x.f.AllowAccess()
	if _, err := x.c.Keys(x.ctx()); !errors.Is(err, ErrNoCloud) {
		t.Errorf("Keys on an installation with no container = %v, want ErrNoCloud: a consumer with no copy has nothing to verify with, which is not the same answer as an empty one", err)
	}
	n, err := x.c.Delete(x.ctx(), cloudUID)
	if err != nil || n != 0 {
		t.Errorf("Delete on an installation with no container = (%d, %v), want (0, nil): absence is the state the caller asked for, as it is for the keychain item's own Delete", n, err)
	}
}

// TestCloudKeysAreEveryPairingAndTheNewestOfAUid: one container holds every
// pairing's key, which is why one container is enough — and a uid that appears
// twice (a re-pair whose older version is still there) resolves to the newest,
// so a superseded key never verifies anything.
func TestCloudKeysAreEveryPairingAndTheNewestOfAUid(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	first, second, newest := "first-key-00000000000", "second-key-0000000000", "newest-key-0000000000"
	for _, p := range []struct {
		uid, key string
	}{
		{cloudUID, first}, {cloudOther, second}, {cloudUID, newest},
	} {
		if err := x.c.Put(x.ctx(), p.uid, cloudKey(p.key)); err != nil {
			t.Fatalf("a pairing's copy could not be written: %v", err)
		}
	}
	x.f.AllowAccess()

	got, err := x.c.Keys(x.ctx())
	if err != nil {
		t.Fatalf("the container could not be read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the container answered %d pairing(s), want 2: three versions carry two uids", len(got))
	}
	if string(got[cloudUID].Bytes()) != newest {
		t.Errorf("the uid's key is %q, want the newest version's %q: a superseded version's key must verify nothing", got[cloudUID].Bytes(), newest)
	}
	if string(got[cloudOther].Bytes()) != second {
		t.Errorf("the other pairing's key is %q, want %q: one pairing's write must not displace another's", got[cloudOther].Bytes(), second)
	}
}

// TestCloudDeleteDestroysOnlyTheRevokedPairing is the design's revocation step
// 2b: the version the pairing's key is in is DESTROYED — not disabled, and not
// rotated, which the design's own sentence refuses — and every other pairing's
// version is left alone.
func TestCloudDeleteDestroysOnlyTheRevokedPairing(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	if err := x.c.Put(x.ctx(), cloudUID, cloudKey(testKey)); err != nil {
		t.Fatalf("the pairing's copy could not be written: %v", err)
	}
	if err := x.c.Put(x.ctx(), cloudOther, cloudKey("other-key-0000000000")); err != nil {
		t.Fatalf("the other pairing's copy could not be written: %v", err)
	}
	x.f.AllowAccess()

	n, err := x.c.Delete(x.ctx(), cloudUID)
	if err != nil {
		t.Fatalf("the copy could not be destroyed: %v", err)
	}
	if n != 1 {
		t.Fatalf("the revoke destroyed %d version(s), want 1: one pairing is one version", n)
	}
	if destroyed := x.f.Destroyed(); len(destroyed) != 1 || !strings.HasSuffix(destroyed[0], "/versions/1") {
		t.Errorf("the versions destroyed are %v, want the first and only version of the revoked pairing: a disable is what the design says is NOT enough, so the call has to be the destroy", destroyed)
	}
	if disabled := x.f.Disabled(); len(disabled) != 0 {
		t.Errorf("the versions disabled are %v, want none: disabling a version is not enough when the service holds versions.access on the parent (design step 2b)", disabled)
	}

	got, err := x.c.Keys(x.ctx())
	if err != nil {
		t.Fatalf("the container could not be read after the revoke: %v", err)
	}
	if _, ok := got[cloudUID]; ok {
		t.Errorf("the revoked pairing is still in the container: without step 2b a revoked pairing's messages keep verifying in the cloud")
	}
	if len(got) != 1 {
		t.Errorf("the container answers %d pairing(s), want the one that was not revoked", len(got))
	}
}

// TestCloudDeleteDestroysADisabledVersionOfTheRevokedPairing is the state a
// revoke must not read as "nothing to destroy": a version an operator disabled
// — or a rotation left behind, the same repository's own idiom — keeps its bytes
// (that is the whole of the design's sentence about a disable) and its value is
// not served at all, so a Delete that looked only at ENABLED versions answered
// (0, nil) with the key still in the container. The version is enabled to be
// attributed, and destroyed: a revoke of a disabled version is a revoke.
func TestCloudDeleteDestroysADisabledVersionOfTheRevokedPairing(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	if err := x.c.Put(x.ctx(), cloudUID, cloudKey(testKey)); err != nil {
		t.Fatalf("the pairing's copy could not be written: %v", err)
	}
	x.f.AllowAccess()
	x.f.DisableVersions(cloudContainer)

	n, err := x.c.Delete(x.ctx(), cloudUID)
	if err != nil {
		t.Fatalf("the disabled copy could not be destroyed: %v", err)
	}
	if n != 1 {
		t.Fatalf("the revoke destroyed %d version(s), want 1: a disabled version carrying the uid is the pairing's copy, and a revoke that passed it over would report a pairing revoked with its key still there", n)
	}
	if destroyed := x.f.Destroyed(); len(destroyed) != 1 || !strings.HasSuffix(destroyed[0], "/versions/1") {
		t.Errorf("the versions destroyed are %v, want the one carrying the uid", destroyed)
	}
	if enabled := x.f.Enabled(); len(enabled) != 1 || !strings.HasSuffix(enabled[0], "/versions/1") {
		t.Errorf("the versions enabled are %v, want the one the destroy took: a disabled version's value is not served, so reading which pairing it is costs an enable", enabled)
	}
	if disabled := x.f.Disabled(); len(disabled) != 0 {
		t.Errorf("the versions disabled are %v, want none: the destroy is the last thing that happens to a version it takes, and it puts nothing back", disabled)
	}

	got, err := x.c.Keys(x.ctx())
	if err != nil {
		t.Fatalf("the container could not be read after the revoke: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the container answers %v, want nothing: the pairing's copy is destroyed, so its key verifies nothing", got)
	}
}

// TestCloudDeleteLeavesAnotherPairingsDisabledVersionDisabled: attributing a
// disabled version costs an enable, and an enable left behind would hand a key
// an operator had taken out of service back to the consumer. Every version a
// revoke does not destroy is left as it was found, and no other pairing's
// version is destroyed on the way to the one that is.
func TestCloudDeleteLeavesAnotherPairingsDisabledVersionDisabled(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	if err := x.c.Put(x.ctx(), cloudUID, cloudKey(testKey)); err != nil {
		t.Fatalf("the pairing's copy could not be written: %v", err)
	}
	if err := x.c.Put(x.ctx(), cloudOther, cloudKey("other-key-0000000000")); err != nil {
		t.Fatalf("the other pairing's copy could not be written: %v", err)
	}
	x.f.AllowAccess()
	x.f.DisableVersions(cloudContainer)

	n, err := x.c.Delete(x.ctx(), cloudUID)
	if err != nil || n != 1 {
		t.Fatalf("Delete = (%d, %v), want (1, nil)", n, err)
	}
	if destroyed := x.f.Destroyed(); len(destroyed) != 1 || !strings.HasSuffix(destroyed[0], "/versions/1") {
		t.Errorf("the versions destroyed are %v, want only the revoked pairing's", destroyed)
	}
	if enabled := x.f.Enabled(); len(enabled) != 2 {
		t.Errorf("the versions enabled are %v, want both: a disabled version's value is not served, so telling one pairing's from another's costs an enable each", enabled)
	}
	if disabled := x.f.Disabled(); len(disabled) != 1 || !strings.HasSuffix(disabled[0], "/versions/2") {
		t.Errorf("the versions disabled are %v, want one disable, of the version that was not the revoked pairing's: the enable that read it is undone", disabled)
	}
	if n := len(x.f.EnabledVersions(cloudContainer)); n != 0 {
		t.Errorf("the container answers %d enabled version(s), want none: one is destroyed and the other is back DISABLED, which is how the revoke found it", n)
	}
}

// TestCloudDeleteIsANoOpForWhatIsNotThere: the two absences a revoke meets —
// a uid this container holds no version for, and a container an installation
// that never opted in does not have — are both the state the caller asked for.
func TestCloudDeleteIsANoOpForWhatIsNotThere(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	if err := x.c.Put(x.ctx(), cloudUID, cloudKey(testKey)); err != nil {
		t.Fatalf("the pairing's copy could not be written: %v", err)
	}
	x.f.AllowAccess()

	n, err := x.c.Delete(x.ctx(), cloudOther)
	if err != nil || n != 0 {
		t.Fatalf("Delete for a uid with no version = (%d, %v), want (0, nil): a device that never had a copy is not an error", n, err)
	}
	if destroyed := x.f.Destroyed(); len(destroyed) != 0 {
		t.Errorf("the versions destroyed are %v, want none: another pairing's key must survive a revoke of one that has none", destroyed)
	}
}

// TestCloudKeysIgnoresADisabledVersion: a DISABLED version is no version for
// this package, which is the design's own reading — "disabling the version is
// not enough if the service is granted versions.access on the parent" — and the
// fake refuses to serve a disabled version's value at all, so a reader that did
// not skip it would fail rather than fall back.
func TestCloudKeysIgnoresADisabledVersion(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	if err := x.c.Put(x.ctx(), cloudUID, cloudKey(testKey)); err != nil {
		t.Fatalf("the pairing's copy could not be written: %v", err)
	}
	x.f.AllowAccess()
	x.f.DisableVersions(cloudContainer)

	got, err := x.c.Keys(x.ctx())
	if err != nil {
		t.Fatalf("a container whose every version is disabled could not be read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the container answered %d pairing(s), want none: a disabled version is not read", len(got))
	}
}

// TestCloudRefusesAVersionItDidNotWrite: the container's only writers are
// `fugaro pair` and `fugaro pair revoke`, so a version carrying something else
// is hand-written and is refused rather than skipped — a consumer that skipped
// one would verify messages with a set it cannot describe. The refusals carry
// the version's name and never its bytes.
func TestCloudRefusesAVersionItDidNotWrite(t *testing.T) {
	for _, tc := range []struct {
		what    string
		payload string
	}{
		{"a payload that is not JSON", "this-is-not-a-record-0001"},
		{"a record with no uid", `{"key":"a-key-0000000000000"}`},
		{"a record whose key is empty", `{"uid":"` + cloudUID + `","key":""}`},
		{"a record whose key is not one key", `{"uid":"` + cloudUID + `","key":"two\nlines-000000"}`},
	} {
		t.Run(tc.what, func(t *testing.T) {
			x := newCloudFixture(t)
			x.f.Seed(cloudContainer, nil, []byte(tc.payload))
			x.f.AllowAccess()

			_, err := x.c.Keys(x.ctx())
			if !errors.Is(err, ErrBackend) {
				t.Fatalf("Keys over a version holding %s = %v, want ErrBackend: a version this build did not write is a container the consumer cannot describe", tc.what, err)
			}
			if strings.Contains(err.Error(), tc.payload) {
				t.Errorf("the refusal carries the version's own bytes: %v", err)
			}
			if !strings.Contains(err.Error(), "/versions/1") {
				t.Errorf("the refusal does not name the version it refused (%v): the operator has to be able to find it", err)
			}
		})
	}
}

// TestCloudNeverEchoesAKey: a server that repeats the body it was sent — the
// fake's EchoAddVersionPayload, which is the real shape an unhelpful answer
// takes — must not get the key onto the operator's terminal or into an event.
// The body IS the key (plus the uid), so the whole payload is redacted, in every
// form the redactor registers: raw, JSON-escaped and base64.
func TestCloudNeverEchoesAKey(t *testing.T) {
	x := newCloudFixture(t)
	x.seeded()
	x.f.EchoAddVersionPayload = true

	err := x.c.Put(x.ctx(), cloudUID, cloudKey(testKey))
	if err == nil {
		t.Fatal("a write the fake refused succeeded")
	}
	if !strings.Contains(err.Error(), "the payload was") {
		t.Fatalf("the fake's echo did not reach the error, so this proves nothing: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, Redacted) {
		t.Errorf("the refusal carries no %s: %v", Redacted, msg)
	}
	for _, leak := range []string{testKey, base64.StdEncoding.EncodeToString([]byte(testKey)), cloudUID} {
		if strings.Contains(msg, leak) {
			t.Errorf("the refusal carries %q: a server that echoes the body it was sent must not put a byte of it in an error (internal/cli/creds.go's remoteErr is the same guard)", leak)
		}
	}
}
