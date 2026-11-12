// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package project is the project-level licence store (D2137): the key object,
// the last-check record and the unreachable counter, in the installation's runs
// bucket beside the marker and the config the project publishes there
// (internal/infra's ProjectMarkerObject and SharedConfigObject).
//
// The CLI's own `licence.key` belongs to one machine (ee/licence/store.go), and
// a licence belongs to the project: the key written here upgrades every member
// at once, and the per-machine key is what a console hands a child it spawns.
// Where the two hold different keys the project's wins, and a reader is told
// which one answered (Applied.Source, View.Source).
//
// The package is its own because the verifier's may not grow a client:
// TestLicencePackageImportsNoNetwork pins the import list of every file beside
// it, and internal/blobx reaches GCS. So a key file is written by something
// that never talks to a socket itself and a bucket is read by something that
// never verifies a claim itself: this package is where the two meet, and it is
// read by `ee/` code only -- nothing in the core imports a licence package
// (D524).
//
// **A viewer reads the state and never the key.** View carries what a status
// shows -- tier, seats, expiry, the state the clock puts the key in, and the
// last revocation check -- and no part of a key: not its text, not the note the
// key carries for admins, and not the renewal secret's hash. A key text is
// reachable only through Keys, the store's own read.
//
// What this store accepts, because it is the runs bucket (the plan's "Limits
// this plan accepts"): every identity that reads the bucket can copy the key,
// and the required `workspace_id` (TN2) is what refuses that copy in another
// workspace. It cannot renew: the key holds only the hash of the renewal
// secret, and the secret itself lives in Secret Manager.
package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/internal/blobx"
)

const (
	// Object is the one object this store keeps in the runs bucket. The name
	// sits beside the project's marker and its published config rather than
	// under a prefix of its own: the whole of a project's licence is this
	// object, and the renewal job's account needs a grant on one name.
	Object = "fugaro/licence.json"

	// maxKeys is how many keys the project holds. Two, because a renewal's
	// 30-day overlap arrives before the key it follows has run out, and both
	// stay so that the key which applies is chosen by its expiry rather than by
	// which write landed last (T26, ee/licence/store.go's maxInstalled).
	maxKeys = 2

	// recordVersion is the object's own version. An object of another version
	// is refused rather than read as this one (CLAUDE.md, "A field never
	// changes meaning"): a newer Fugaro writing a shape this one would read
	// partially would show a state its writer did not mean.
	recordVersion = 1

	// maxObjectBytes bounds the read. The object is two keys and a small check
	// record, and anyone who can write the runs bucket can replace it with
	// something larger.
	maxObjectBytes = 64 << 10
)

// The three answers a revocation check records. They are written by the
// renewal job (K4.1) and read by the status and the warnings: an unreachable
// check is the one that is not an answer about the key at all -- it never
// revokes (D2140), and the streak it starts is what warns admins.
const (
	// CheckOK is a check the server answered, with the key still good.
	CheckOK = "ok"
	// CheckRevoked is a check the server answered with a revocation.
	CheckRevoked = "revoked"
	// CheckUnreachable is a check that never reached the server. The key stays
	// valid: only an explicit answer revokes (D2140).
	CheckUnreachable = "unreachable"
)

// Source names the store a reader's answer came from.
type Source string

const (
	// SourceProject is the project's own key: the installation's, written by
	// `fugaro licence set` or by the renewal job.
	SourceProject Source = "project"
	// SourceMachine is the per-machine `licence.key`, which answers only where
	// the project holds no key at all.
	SourceMachine Source = "machine"
)

// ErrObject is the project's licence object being one this store cannot use: a
// shape or a version it does not read, more keys than the renewal's overlap
// installs, or a bucket that refused the read or the write.
var ErrObject = errors.New("licence: the project's licence object cannot be used")

// Check is the last revocation check's record: when it ran, what it answered,
// and how many checks in a row could not reach the server (D2140).
type Check struct {
	// At is when the check ran.
	At time.Time `json:"at"`
	// Status is one of the three answers above.
	Status string `json:"status"`
	// Failures is how many checks in a row could not reach the server. Any
	// answer clears it, so it counts a streak and not a total.
	Failures int `json:"failures,omitzero"`
	// LastOK is when a check last reached the server, whether it answered ok or
	// revoked. It is what the 30-day warning counts from (D2140).
	LastOK time.Time `json:"last_ok,omitzero"`
}

// record is the object as it is written: the installed keys with the check
// record beside them, so the whole of a project's licence is one read and one
// write.
type record struct {
	V     int      `json:"v"`
	Keys  []string `json:"keys,omitempty"`
	Check *Check   `json:"last_check,omitempty"`
}

// keys answers the record's keys as the bytes a verifier reads.
func (r record) keys() [][]byte {
	keys := make([][]byte, 0, len(r.Keys))
	for _, key := range r.Keys {
		keys = append(keys, []byte(key))
	}
	return keys
}

// Store is the project-level store over one installation's runs bucket.
type Store struct{ bucket *blobx.Bucket }

// Open opens the store over the runs bucket at bucketURL (localcfg's
// BucketURL), which is the same bucket the project's marker and its published
// config are read from.
func Open(ctx context.Context, bucketURL string) (*Store, error) {
	b, err := blobx.Open(ctx, bucketURL)
	if err != nil {
		return nil, fmt.Errorf("the licence key's home, %s: %w", bucketURL, err)
	}
	return New(b), nil
}

// New wraps a bucket the caller already holds. A command that holds the
// project's runs bucket (the CLI's own cloud handle does) uses this rather than
// opening the bucket a second time.
func New(b *blobx.Bucket) *Store { return &Store{bucket: b} }

// Keys answers the key texts the project holds, oldest write first, and none at
// all where no key is installed: the no-key state is the store being empty
// rather than an error (D2143).
func (s *Store) Keys(ctx context.Context) ([][]byte, error) {
	rec, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	return rec.keys(), nil
}

// Install writes one key beside the keys the project already holds, keeping the
// newest two (T26). A key already installed is not installed twice, and the
// oldest write is the one a third key displaces -- a renewal appends, so the
// key that goes is the one that expires first.
//
// The text is written as it is given: whether this build accepts it is the
// read's answer (Active), and a writer holding a keyring refuses a key the
// verifier refuses before it reaches here.
//
// An object this store cannot read refuses the install rather than being
// replaced: the object is the project's only copy of a key, and overwriting
// what cannot be read is how a key is lost.
func (s *Store) Install(ctx context.Context, raw []byte) error {
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return fmt.Errorf("%w: the key to install is empty", ErrObject)
	}
	rec, err := s.read(ctx)
	if err != nil {
		return err
	}
	for _, held := range rec.Keys {
		if held == key {
			return nil
		}
	}
	rec.Keys = append(rec.Keys, key)
	if len(rec.Keys) > maxKeys {
		rec.Keys = rec.Keys[len(rec.Keys)-maxKeys:]
	}
	return s.write(ctx, rec)
}

// RecordCheck writes what a revocation check answered. The unreachable counter
// is the store's and not a caller's: an unreachable answer raises the streak
// and any answer clears it, and LastOK moves only where the server answered --
// which is what the 30-day warning counts from (D2140).
func (s *Store) RecordCheck(ctx context.Context, at time.Time, status string) error {
	switch status {
	case CheckOK, CheckRevoked, CheckUnreachable:
	default:
		return fmt.Errorf("%w: %q is not a revocation check's answer", ErrObject, status)
	}
	rec, err := s.read(ctx)
	if err != nil {
		return err
	}
	// Silence raises the streak and leaves LastOK where the server last
	// answered; any answer ends the streak and moves LastOK to this check.
	check := Check{At: at, Status: status, Failures: 1}
	if status != CheckUnreachable {
		check.Failures, check.LastOK = 0, at
	} else if prev := rec.Check; prev != nil {
		check.Failures, check.LastOK = prev.Failures+1, prev.LastOK
	}
	rec.Check = &check
	return s.write(ctx, rec)
}

// Applied is the key that applies, the state the clock puts it in, and the
// store it came from.
//
// Claims is the whole claim set, the note the key carries for admins and the
// renewal secret's hash included: it is the installation's own key, read by
// `ee/` code that acts on it, and a viewer's shape is View. So a caller that
// prints an Applied prints a key's admin fields, and a caller that shows a
// person a licence asks View.
type Applied struct {
	Claims licence.ClaimsV1
	State  licence.State
	Source Source
}

// Active answers the key that applies now: the project's own where it holds
// any, and the per-machine keys the caller passes where it holds none (D2137).
// The per-machine keys are the ones the CLI's own store holds
// (licence.Store.Keys), and a console that handed one to a child changes
// nothing: the project's answers where there is one.
//
// A project store whose every key is refused answers the first refusal rather
// than falling back to the machine's -- the project is the installation's
// licence, and a machine's key must not quietly upgrade an installation whose
// own key was refused. Where neither holds a key the answer is
// licence.ErrNoKey, which is the Free state and not a failure (D2143).
//
// firebaseProjectID is the workspace's own Firebase project id, read by the
// caller from the project's config in the same read as the key (TN2): it is
// what the key's required `workspace_id` is checked against, and a store that
// took it from the key would be comparing the key with itself.
func (s *Store) Active(ctx context.Context, now time.Time, ring licence.Keyring, firebaseProjectID string, machine [][]byte) (Applied, error) {
	rec, err := s.read(ctx)
	if err != nil {
		return Applied{}, err
	}
	return apply(now, ring, firebaseProjectID, rec.keys(), machine)
}

// View is a project's licence as a reader who is not an admin sees it: the
// state, and never the key (D2137).
type View struct {
	// Tier is the tier the key carries (licence.TierCommunity and its two
	// neighbours), and the empty string where nothing is installed.
	Tier string
	// Seats is what the key's organisation bought. It is compared and shown
	// here and enforced only on the `ee` surfaces (D2132, held with K3.4).
	Seats int
	// PaidThrough is the end of the paid period; State is what the clock makes
	// of it beside the key's own grace.
	PaidThrough time.Time
	// State is active, grace or expired against the clock the reader passed.
	State licence.State
	// Kind is paid, trial or comped.
	Kind string
	// RenewalMode is the key's own claim (auto, manual or none; spec.md section
	// 3.2's renewal_mode) and not whether this installation has a job that
	// renews: a key claiming auto on an installation with no Scheduler job --
	// K4.2's condition, which an installation without a coordinator does not
	// meet -- still reads auto here, while what that installation does is renew
	// by hand (TN5). The job's existence is a fact about the installation and is
	// asked of the installation, not of the key.
	RenewalMode string
	// Source says which store answered.
	Source Source
	// Check is the last revocation check the project ran -- the installation's
	// record and not one key's, so a key installed after a check carries that
	// check's figures until the next check replaces them -- and HasCheck says
	// whether there is one at all: a project that never checked has none.
	Check    Check
	HasCheck bool
}

// View answers what a viewer may read: the state of the key that applies and
// never the key. It is Active's claim set seen through the subset a viewer is
// entitled to, plus the last revocation check -- so the key's text, the note it
// carries for admins and the renewal secret's hash are all absent by
// construction rather than by a caller's care.
func (s *Store) View(ctx context.Context, now time.Time, ring licence.Keyring, firebaseProjectID string, machine [][]byte) (View, error) {
	rec, err := s.read(ctx)
	if err != nil {
		return View{}, err
	}
	applied, err := apply(now, ring, firebaseProjectID, rec.keys(), machine)
	if err != nil {
		return View{}, err
	}
	view := View{
		Tier:        applied.Claims.Tier,
		Seats:       applied.Claims.Seats,
		PaidThrough: applied.Claims.PaidThrough,
		State:       applied.State,
		Kind:        applied.Claims.Kind,
		RenewalMode: applied.Claims.RenewalMode,
		Source:      applied.Source,
	}
	if rec.Check != nil {
		view.Check, view.HasCheck = *rec.Check, true
	}
	return view, nil
}

// read answers the object. A missing object is the no-key state and not an
// error: an installation whose owner never installed a key has no object at all
// (the licence package's rule for its own file, one store over).
func (s *Store) read(ctx context.Context) (record, error) {
	data, _, err := s.bucket.ReadMaxStrict(ctx, Object, maxObjectBytes)
	if errors.Is(err, blobx.ErrNotExist) {
		return record{}, nil
	}
	if err != nil {
		return record{}, fmt.Errorf("%w: reading %s: %w", ErrObject, Object, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec record
	if err := dec.Decode(&rec); err != nil {
		return record{}, fmt.Errorf("%w: %s does not decode as this store's object (%v)", ErrObject, Object, err)
	}
	// The decode runs to the end of the object, as the key's own does
	// (decodeClaimsV1): a second value, or the stray brace a hand-edited object
	// leaves, is an object this store cannot place rather than one it reads and
	// ignores the rest of.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return record{}, fmt.Errorf("%w: %s carries more than one JSON value", ErrObject, Object)
	}
	if rec.V != recordVersion {
		return record{}, fmt.Errorf("%w: %s is a version %d object, and this Fugaro writes and reads version %d", ErrObject, Object, rec.V, recordVersion)
	}
	if len(rec.Keys) > maxKeys {
		return record{}, fmt.Errorf("%w: %s holds %d keys, and the renewal's overlap installs two at the most (T26)", ErrObject, Object, len(rec.Keys))
	}
	return rec, nil
}

// write puts the object back. The write is unconditional, as the shared
// config's is (internal/cli/sharedcfg.go), because the writers that can meet
// here are a person running `fugaro licence set`, who holds objectAdmin over
// fugaro/, and -- from K4.2 -- the renewal job's own service account, whose
// grant is this one object. A launcher is not among them: it holds objectUser
// on runs/, tokens/ and issues/ and reads the rest of the bucket, so this
// object is not its to write (docs/design/bucket-iam.md section 3). Two such
// writers racing lose one write rather than corrupt the object, and the rules
// that matter -- one shape, at most two keys -- are held on the read.
//
// An object larger than the read accepts is refused here, so a store never
// writes one it could not read back: the object is the project's only copy of
// its licence, and a write that poisoned every later read would be the licence
// lost to a key nothing verifies anyway.
func (s *Store) write(ctx context.Context, rec record) error {
	rec.V = recordVersion
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("%w: the object could not be written: %v", ErrObject, err)
	}
	if len(data) > maxObjectBytes {
		return fmt.Errorf("%w: the object to write is %d bytes, and %s is read with a limit of %d", ErrObject, len(data), Object, maxObjectBytes)
	}
	if err := s.bucket.Put(ctx, Object, data, "application/json"); err != nil {
		return fmt.Errorf("%w: writing %s: %w", ErrObject, Object, err)
	}
	return nil
}

// apply is the rule the two stores are ordered by: the project's keys decide
// wherever the project holds any, and the machine's answers only where it holds
// none. Both readings are the licence package's own (its `active`): among the
// keys that verify, the one with the later paid_through, and a set whose every
// key is refused answers the first refusal, because that is the one a person
// has to act on.
func apply(now time.Time, ring licence.Keyring, firebaseProjectID string, projectKeys, machineKeys [][]byte) (Applied, error) {
	claims, state, err := firstKey(now, ring, firebaseProjectID, projectKeys)
	switch {
	case err == nil:
		return Applied{Claims: claims, State: state, Source: SourceProject}, nil
	case !errors.Is(err, licence.ErrNoKey):
		return Applied{}, err
	}
	claims, state, err = firstKey(now, ring, firebaseProjectID, machineKeys)
	if err != nil {
		return Applied{}, err
	}
	return Applied{Claims: claims, State: state, Source: SourceMachine}, nil
}

// firstKey places one set of keys: the one that applies among those that verify
// against now, and the state it is in.
func firstKey(now time.Time, ring licence.Keyring, firebaseProjectID string, keys [][]byte) (licence.ClaimsV1, licence.State, error) {
	if len(keys) == 0 {
		return licence.ClaimsV1{}, "", licence.ErrNoKey
	}
	var (
		applied licence.ClaimsV1
		state   licence.State
		found   bool
		refusal error
	)
	for _, raw := range keys {
		claims, st, err := licence.VerifyV1(raw, now, ring, firebaseProjectID)
		if err != nil {
			if refusal == nil {
				refusal = err
			}
			continue
		}
		if !found || claims.PaidThrough.After(applied.PaidThrough) {
			applied, state, found = claims, st, true
		}
	}
	if !found {
		return licence.ClaimsV1{}, "", refusal
	}
	return applied, state, nil
}
