// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package project

// The project-level store's tests (licence-key-v2 plan K3.1).
//
// **Every key here is minted in the process that reads it** (.claude/rules/ee.md:
// no private half in a file). newMinted generates a keypair per call, the
// private half never leaves memory, and a test that has to search for a key text
// searches for the one it just minted.
//
// The bucket is internal/gcpfake's, which serves the object and the 403s a
// denied write brings, so nothing here reaches a network or a home directory.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// The names a test's key is minted under. None of them is key material: the
// keypair is generated per call, and the ring id is a name a keyring holds.
const (
	testKeyID           = "licence-test-project-v1"
	testFirebaseProject = "fugaro-test-project"
	testBucket          = "fugaro-runs-test-project"
)

// testNow is the clock every test reads a key at, and the instant the keys are
// issued from: a fixed date rather than time.Now, because a key's state is what
// these tests are about and a clock that moved would move it.
var testNow = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)

// at is a day offset from testNow, which is how a test places a key's term.
func at(days int) time.Time { return testNow.Add(time.Duration(days) * 24 * time.Hour) }

// rig is a store over the fake bucket the runs bucket stands in for, with the
// bucket and the fake back so a test can read the object itself and refuse a
// write.
type rig struct {
	store  *Store
	bucket *blobx.Bucket
	gcs    *gcpfake.GCS
}

func newRig(t *testing.T) rig {
	t.Helper()
	g := gcpfake.NewGCS(t)
	g.AddBucket(testBucket, 1, nil)
	b := g.Bucket(t, testBucket)
	return rig{store: New(b), bucket: b, gcs: g}
}

// minted is one run-time keypair and the keyring that holds its public half.
type minted struct {
	ring licence.Keyring
	priv ed25519.PrivateKey
}

func newMinted(t *testing.T) minted {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return minted{ring: licence.Keyring{testKeyID: pub}, priv: priv}
}

// key mints one key text for the claim set a project key starts from, with
// mutate applied where a test moves one claim.
func (m minted) key(t *testing.T, mutate func(*licence.ClaimsV1)) string {
	t.Helper()
	claims := licence.ClaimsV1{
		V:            1,
		KID:          testKeyID,
		KeyID:        "lk_test_0001",
		WorkspaceID:  testFirebaseProject,
		Organisation: "example.com",
		Tier:         licence.TierTeam,
		Seats:        5,
		IssuedAt:     testNow,
		PaidThrough:  at(365),
		GraceDays:    30,
		RenewalMode:  licence.RenewalAuto,
		Kind:         licence.KindPaid,
		Note:         "a test key",
	}
	if mutate != nil {
		mutate(&claims)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claim set: %v", err)
	}
	return "fgk_" + base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(m.priv, payload))
}

// sameKeys holds two key sets to the same texts, so a failure prints which
// text won rather than two pointers.
func sameKeys(got [][]byte, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, key := range got {
		if string(key) != want[i] {
			return false
		}
	}
	return true
}

// text renders a key set as the texts it holds, for an assertion's message.
func text(keys [][]byte) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, string(key))
	}
	return out
}

// pieces is a key text and its two parts: a refusal or a log line that quoted
// half a key would still be a key in a support ticket, so every piece is
// searched for rather than the whole text alone.
func pieces(key string) []string {
	return append([]string{key}, strings.Split(strings.TrimPrefix(key, "fgk_"), ".")...)
}

// carries answers the first piece a text holds, and whether it holds one.
func carries(where string, keys ...string) (string, bool) {
	for _, key := range keys {
		for _, piece := range pieces(key) {
			if strings.Contains(where, piece) {
				return piece, true
			}
		}
	}
	return "", false
}

// TestProjectStoreKeepsTwoKeys is T26 at the project level: a renewal's next key
// arrives before the one it follows has run out, the store keeps both, the one
// that applies is the one with the later expiry, and a third key displaces the
// oldest write rather than growing the object.
func TestProjectStoreKeepsTwoKeys(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	m := newMinted(t)
	long := m.key(t, func(c *licence.ClaimsV1) { c.KeyID = "lk_test_long"; c.PaidThrough = at(300) })
	short := m.key(t, func(c *licence.ClaimsV1) { c.KeyID = "lk_test_short"; c.PaidThrough = at(30) })
	third := m.key(t, func(c *licence.ClaimsV1) { c.KeyID = "lk_test_third"; c.PaidThrough = at(90) })

	if keys, err := r.store.Keys(ctx); err != nil || len(keys) != 0 {
		t.Fatalf("a project with no key answers %d keys and %v, want none and no error: the no-key state is the store being empty (D2143)", len(keys), err)
	}
	if _, err := r.store.Active(ctx, testNow, m.ring, testFirebaseProject, nil); !errors.Is(err, licence.ErrNoKey) {
		t.Fatalf("Active with no key at all = %v, want licence.ErrNoKey: no key is Free (D2143), which is a state and not a failure", err)
	}

	install := func(what, key string, want []string) {
		t.Helper()
		if err := r.store.Install(ctx, []byte(key)); err != nil {
			t.Fatalf("installing %s: %v", what, err)
		}
		got, err := r.store.Keys(ctx)
		if err != nil {
			t.Fatalf("reading the keys back after installing %s: %v", what, err)
		}
		if !sameKeys(got, want) {
			t.Errorf("after installing %s the store holds %v, want the %v it was told to keep: the renewal's overlap is the two newest writes and no more (T26)", what, text(got), want)
		}
	}

	install("the first key", long, []string{long})
	install("a key with a shorter term", short, []string{long, short})

	// The overlap's whole point: both keys verify, and the one that applies is
	// the one with the later expiry rather than the line written last (T26) --
	// which is why these two are written in the order that tells them apart.
	// The clock sits past the shorter key's term, so the pair is exactly the
	// case the rule is for: a key in its grace beside one still inside its term.
	applied, err := r.store.Active(ctx, at(50), m.ring, testFirebaseProject, nil)
	if err != nil {
		t.Fatalf("Active over the overlap: %v", err)
	}
	if applied.Claims.KeyID != "lk_test_long" || applied.State != licence.Active || applied.Source != SourceProject {
		t.Errorf("with a later-expiring key written first and a shorter one after it, Active answers %q in state %s from %q, want lk_test_long active from the project: the key that applies is chosen by its expiry and not by which write landed last (T26)", applied.Claims.KeyID, applied.State, applied.Source)
	}

	install("the same key a second time", short, []string{long, short})
	install("a third key", third, []string{short, third})

	// The installed keys are on the runs bucket, at the store's own object and
	// nowhere else, and a second store reads them back: the object is the
	// project's state rather than this process's.
	raw, _, err := r.bucket.Read(ctx, Object)
	if err != nil {
		t.Fatalf("reading %s: %v", Object, err)
	}
	var written record
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("%s is not this store's object: %v", Object, err)
	}
	if written.V != recordVersion || !reflect.DeepEqual(written.Keys, []string{short, third}) {
		t.Errorf("%s holds version %d and %d keys, want version %d and the two newest", Object, written.V, len(written.Keys), recordVersion)
	}
	again, err := New(r.bucket).Keys(ctx)
	if err != nil || !sameKeys(again, []string{short, third}) {
		t.Errorf("a second store over the same bucket reads %v (%v), want the same two keys", text(again), err)
	}

	// An object holding more keys than the overlap installs is not read as a
	// store that is merely carrying spares: it is refused, and the refusal says
	// what the bound is rather than echoing what it found.
	stray := []string{"fgk_alpha", "fgk_beta", "fgk_gamma"}
	if err := r.bucket.Put(ctx, Object, []byte(`{"v":1,"keys":["fgk_alpha","fgk_beta","fgk_gamma"]}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	keys, err := r.store.Keys(ctx)
	if !errors.Is(err, ErrObject) || keys != nil {
		t.Fatalf("an object holding three keys answers %d keys and %v, want ErrObject and none", len(keys), err)
	}
	if !strings.Contains(err.Error(), "two at the most") {
		t.Errorf("the refusal for a third key reads %q, want it to name the bound the overlap installs", err)
	}
	if piece, found := carries(err.Error(), stray...); found {
		t.Errorf("the refusal for a three-key object carries %q: a refusal is a sentence a user pastes into a ticket, and it names the object and the count and no key", piece)
	}

	// The decode runs to the end of the object (decodeClaimsV1's rule): a stray
	// brace a hand edit left behind is a second value, not the object before it.
	if err := r.bucket.Put(ctx, Object, []byte(`{"v":1,"keys":["fgk_alpha"]}}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Keys(ctx); !errors.Is(err, ErrObject) {
		t.Errorf("an object with a trailing brace answers %v, want ErrObject: an object this store cannot read is refused whole", err)
	}

	// A key too large to read back is never written: the object would be past
	// the limit every read applies, and the project's licence would be lost to a
	// key no verifier accepts in the first place.
	huge := newRig(t)
	if err := huge.store.Install(ctx, []byte(strings.Repeat("f", maxObjectBytes+1))); !errors.Is(err, ErrObject) {
		t.Errorf("installing a key larger than an object is read = %v, want ErrObject", err)
	}
	if _, _, err := huge.bucket.Read(ctx, Object); !errors.Is(err, blobx.ErrNotExist) {
		t.Errorf("the refused oversized install left %v on the bucket, want the bucket untouched", err)
	}
}

// TestTheStoreRefusesWhatItCannotRead pins the guard an install stands on: an
// object of another version, or one that is not this store's shape, is refused
// rather than read as far as it parses and then replaced -- the object is the
// project's only copy of a key, and a store that overwrote what it could not
// understand is how a key is lost.
func TestTheStoreRefusesWhatItCannotRead(t *testing.T) {
	ctx := context.Background()
	m := newMinted(t)
	for _, what := range []struct{ name, object string }{
		{"an object of a version this build does not write", `{"v":99,"keys":["fgk_alpha"]}`},
		{"an object that is not this store's shape", `{"licence":"fgk_alpha"}`},
	} {
		t.Run(what.name, func(t *testing.T) {
			r := newRig(t)
			raw := []byte(what.object)
			if err := r.bucket.Put(ctx, Object, raw, "application/json"); err != nil {
				t.Fatal(err)
			}
			if keys, err := r.store.Keys(ctx); !errors.Is(err, ErrObject) || keys != nil {
				t.Fatalf("%s answers %d keys and %v, want ErrObject and none", what.name, len(keys), err)
			}
			if err := r.store.Install(ctx, []byte(m.key(t, nil))); !errors.Is(err, ErrObject) {
				t.Fatalf("installing a key beside %s = %v, want ErrObject", what.name, err)
			}
			after, _, err := r.bucket.Read(ctx, Object)
			if err != nil {
				t.Fatalf("reading %s back: %v", Object, err)
			}
			if !bytes.Equal(after, raw) {
				t.Errorf("the refused install left %q on the bucket, want the %d bytes it could not read", after, len(raw))
			}
		})
	}
}

// TestTheStoreNeverLogsAKey holds the store to the rule the key's own package
// keeps for argv: a key text reaches the bucket's object and nothing else. No
// error the store returns and no value it hands out carries one, in whole or in
// part, and the package has no logger for one to reach.
func TestTheStoreNeverLogsAKey(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	m := newMinted(t)
	const noteCanary = "a-note-no-store-output-may-name"
	key := m.key(t, func(c *licence.ClaimsV1) { c.Note = noteCanary })

	if err := r.store.Install(ctx, []byte(key)); err != nil {
		t.Fatalf("installing a key: %v", err)
	}

	// The object is where a key belongs, and it is the one place this package
	// writes one: a test that hid the key here would be testing something else.
	raw, _, err := r.bucket.Read(ctx, Object)
	if err != nil {
		t.Fatalf("reading %s: %v", Object, err)
	}
	if !strings.Contains(string(raw), key) {
		t.Fatalf("%s does not hold the installed key, and the object is the key's home (D2137)", Object)
	}

	// Everything the store hands a caller formats without it.
	applied, err := r.store.Active(ctx, testNow, m.ring, testFirebaseProject, nil)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	view, err := r.store.View(ctx, testNow, m.ring, testFirebaseProject, nil)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	for _, shown := range []struct {
		what  string
		value any
	}{
		{"the store", r.store},
		{"the applied key", applied},
		{"the view", view},
		{"the check record", view.Check},
	} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if piece, found := carries(fmt.Sprintf(format, shown.value), key); found {
				t.Errorf("formatting %s with %s shows %q: a key is in the store and never in what the store prints", shown.what, format, piece)
			}
		}
	}

	// A key this build does not hold: the verifier's refusal names the part of
	// the check that failed and never the text that failed it.
	stranger := newMinted(t)
	foreign := stranger.key(t, nil)
	refused := newRig(t)
	if err := refused.store.Install(ctx, []byte(foreign)); err != nil {
		t.Fatalf("installing a key signed by another keypair: %v", err)
	}
	if _, err := refused.store.Active(ctx, testNow, m.ring, testFirebaseProject, nil); err == nil {
		t.Fatal("a key signed by a keypair the ring does not hold verified")
	} else if piece, found := carries(err.Error(), foreign); found {
		t.Errorf("the refusal for a foreign signature carries %q: the verifier refuses a text it could not place and does not quote it", piece)
	}

	// A write the bucket refused: the refusal wraps the bucket's own error, and
	// the key that was being written is not in it. The key installed above is
	// not written again -- a key already held is not installed twice -- so this
	// is a second, different text.
	r.gcs.DenyWrites(testBucket, Object)
	fresh := m.key(t, func(c *licence.ClaimsV1) { c.KeyID = "lk_test_denied" })
	if err := r.store.Install(ctx, []byte(fresh)); !errors.Is(err, ErrObject) {
		t.Fatalf("a write the bucket refused = %v, want ErrObject", err)
	} else if piece, found := carries(err.Error(), fresh, key); found {
		t.Errorf("the refusal for a denied write carries %q: the bucket's own error is wrapped and the key is not", piece)
	}

	// A read the bucket refused, which is the last refusal reached here because
	// the fake keeps answering 403 for the object once it starts.
	r.gcs.FailObjectGetsAfter(testBucket, Object, 0)
	if _, err := r.store.Keys(ctx); !errors.Is(err, ErrObject) {
		t.Fatalf("a read the bucket refused = %v, want ErrObject", err)
	} else if piece, found := carries(err.Error(), key, fresh); found {
		t.Errorf("the refusal for a denied read carries %q", piece)
	}

	// The store's own files carry no logger at all, which is what makes the
	// rule above structural rather than a matter of the messages as they stand.
	imported, err := importsOf(t, "store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"log", "log/slog"} {
		if imported[pkg] {
			t.Errorf("store.go imports %q: the store writes a key to one place, the object, and a logger is a second one nobody is watching", pkg)
		}
	}
}

// TestAViewerReadsTheStateNotTheKey is D2137's half of the store: what a reader
// who is not an admin gets is the state, and the key's text, the note it carries
// for admins and the renewal secret's hash are absent by construction.
func TestAViewerReadsTheStateNotTheKey(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	m := newMinted(t)
	const (
		noteCanary = "an-admins-only-note-no-viewer-may-read"
		hashCanary = "the-renewal-secrets-hash-no-viewer-may-read"
	)
	daily := "P1D"
	key := m.key(t, func(c *licence.ClaimsV1) {
		c.KeyID = "lk_test_viewer"
		c.Seats = 7
		c.CheckInterval = &daily
		c.RenewalSecretHash = hashCanary
		c.Note = noteCanary
	})
	if err := r.store.Install(ctx, []byte(key)); err != nil {
		t.Fatalf("installing a key: %v", err)
	}
	if err := r.store.RecordCheck(ctx, at(1), CheckOK); err != nil {
		t.Fatalf("recording a check: %v", err)
	}

	view, err := r.store.View(ctx, at(30), m.ring, testFirebaseProject, nil)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.Tier != licence.TierTeam || view.Seats != 7 || !view.PaidThrough.Equal(at(365)) ||
		view.State != licence.Active || view.Kind != licence.KindPaid ||
		view.RenewalMode != licence.RenewalAuto || view.Source != SourceProject {
		t.Errorf("the view reads %+v, want the installed key's tier, seats, expiry, state, kind, renewal mode and the project as the source", view)
	}
	if !view.HasCheck || view.Check.Status != CheckOK || !view.Check.At.Equal(at(1)) || view.Check.Failures != 0 || !view.Check.LastOK.Equal(at(1)) {
		t.Errorf("the view's check record reads %+v, want the ok check recorded at one day", view.Check)
	}

	shown := fmt.Sprintf("%v %+v %#v", view, view, view)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshalling the view: %v", err)
	}
	for _, withheld := range []struct{ what, text string }{
		{"the key text", key},
		{"the note the key carries for admins", noteCanary},
		{"the renewal secret's hash", hashCanary},
	} {
		if piece, found := carries(shown+" "+string(encoded), withheld.text); found {
			t.Errorf("what a viewer reads carries %q from %s: a viewer reads the state, never the key (D2137)", piece, withheld.what)
		}
	}

	// The shape is the pin: a field added to the view has to be weighed against
	// "a viewer reads the state, never the key", and this is where that is
	// decided rather than in a command's rendering.
	want := []string{"Tier", "Seats", "PaidThrough", "State", "Kind", "RenewalMode", "Source", "Check", "HasCheck"}
	if got := fieldNames(reflect.TypeOf(view)); !reflect.DeepEqual(got, want) {
		t.Errorf("the view's fields are %v, want %v: every one of them is something a status shows, and a key, a note for admins or the renewal secret's hash is none of them", got, want)
	}

	t.Run("the project's key wins over the machine's", func(t *testing.T) {
		// The machine holds a key that would apply longer: the project's still
		// answers, and the view says which store did (D2137).
		machine := m.key(t, func(c *licence.ClaimsV1) { c.Seats = 99; c.PaidThrough = at(390) })
		view, err := r.store.View(ctx, at(30), m.ring, testFirebaseProject, [][]byte{[]byte(machine)})
		if err != nil {
			t.Fatalf("View with a differing machine key: %v", err)
		}
		if view.Seats != 7 || view.Source != SourceProject {
			t.Errorf("with a machine key beside it the project's key answers %d seats from %q, want the project's 7 from %q", view.Seats, view.Source, SourceProject)
		}

		// A project with no key of its own answers the machine's, and says so.
		bare := newRig(t)
		view, err = bare.store.View(ctx, at(30), m.ring, testFirebaseProject, [][]byte{[]byte(machine)})
		if err != nil {
			t.Fatalf("View over a machine key alone: %v", err)
		}
		if view.Seats != 99 || view.Source != SourceMachine {
			t.Errorf("with no project key the machine's answers %d seats from %q, want 99 from %q", view.Seats, view.Source, SourceMachine)
		}

		// A project key past its grace is still the project's answer: a
		// machine's key must not quietly upgrade an installation whose own key
		// has run out.
		view, err = r.store.View(ctx, at(500), m.ring, testFirebaseProject, [][]byte{[]byte(machine)})
		if err != nil {
			t.Fatalf("View of a lapsed project key: %v", err)
		}
		if view.State != licence.Expired || view.Seats != 7 || view.Source != SourceProject {
			t.Errorf("a lapsed project key beside a live machine key answers %+v, want the project's expired key and not the machine's", view)
		}
	})

	t.Run("a refused project key does not fall back to the machine's", func(t *testing.T) {
		// The project holds a key this build cannot place -- one signed by a
		// keypair outside the ring -- and the machine holds a good one. The
		// machine's must not license the installation: the project's key is the
		// installation's, and a per-machine key quietly upgrading an
		// installation whose own key was refused is exactly what the rule
		// refuses (D2137).
		stranger := newMinted(t)
		refused := newRig(t)
		if err := refused.store.Install(ctx, []byte(stranger.key(t, nil))); err != nil {
			t.Fatalf("installing a key signed by another keypair: %v", err)
		}
		machine := m.key(t, func(c *licence.ClaimsV1) { c.Seats = 99 })

		applied, err := refused.store.Active(ctx, at(30), m.ring, testFirebaseProject, [][]byte{[]byte(machine)})
		if err == nil {
			t.Fatalf("a refused project key beside a valid machine key answered %q from %q, want the refusal", applied.Claims.KeyID, applied.Source)
		}
		if !errors.Is(err, licence.ErrSignature) {
			t.Errorf("the refusal reads %v, want the signature the project's key failed", err)
		}
		if applied.Source == SourceMachine {
			t.Errorf("Active answered from %q, want no store at all: a machine's key must not answer for an installation whose own key was refused", applied.Source)
		}

		view, err := refused.store.View(ctx, at(30), m.ring, testFirebaseProject, [][]byte{[]byte(machine)})
		if err == nil || view.Source == SourceMachine || view.Seats == 99 {
			t.Errorf("View of a refused project key beside a machine key reads %+v (%v), want the refusal and never the machine's key", view, err)
		}
	})

	t.Run("the unreachable counter is a streak", func(t *testing.T) {
		// Silence raises the count and moves nothing else, and one answer
		// clears it and moves LastOK: that pair is what warns admins after 30
		// days without a word from the server (D2140).
		for day := 2; day <= 4; day++ {
			if err := r.store.RecordCheck(ctx, at(day), CheckUnreachable); err != nil {
				t.Fatalf("recording an unreachable check: %v", err)
			}
		}
		view, err := r.store.View(ctx, at(5), m.ring, testFirebaseProject, nil)
		if err != nil {
			t.Fatalf("View: %v", err)
		}
		if view.Check.Status != CheckUnreachable || view.Check.Failures != 3 || !view.Check.At.Equal(at(4)) {
			t.Errorf("after three unreachable checks the record reads %+v, want three in a row at day four", view.Check)
		}
		if !view.Check.LastOK.Equal(at(1)) {
			t.Errorf("three unreachable checks moved LastOK to %s, want the day-one answer it counts from (D2140)", view.Check.LastOK)
		}

		if err := r.store.RecordCheck(ctx, at(5), CheckRevoked); err != nil {
			t.Fatalf("recording a revoked answer: %v", err)
		}
		view, err = r.store.View(ctx, at(5), m.ring, testFirebaseProject, nil)
		if err != nil {
			t.Fatalf("View: %v", err)
		}
		if view.Check.Failures != 0 || view.Check.Status != CheckRevoked || !view.Check.LastOK.Equal(at(5)) {
			t.Errorf("an answer after silence reads %+v, want the streak cleared and the answer's own time in LastOK", view.Check)
		}

		// The record is the installation's and not one key's (View's own
		// contract): the renewal's next key, which is what a key arriving after
		// a check is, carries that check's figures -- the streak and LastOK
		// count the project's silence, and an install does not run a check. That
		// carry is what K3.2 shows and what D2140's 30-day warning counts from.
		fresh := m.key(t, func(c *licence.ClaimsV1) {
			c.KeyID = "lk_test_installed_after_a_check"
			c.PaidThrough = at(390)
		})
		if err := r.store.Install(ctx, []byte(fresh)); err != nil {
			t.Fatalf("installing a key after a check: %v", err)
		}
		applied, err := r.store.Active(ctx, at(5), m.ring, testFirebaseProject, nil)
		if err != nil {
			t.Fatalf("Active after the install: %v", err)
		}
		if applied.Claims.KeyID != "lk_test_installed_after_a_check" {
			t.Fatalf("the installed key did not apply: Active answers %q", applied.Claims.KeyID)
		}
		view, err = r.store.View(ctx, at(5), m.ring, testFirebaseProject, nil)
		if err != nil {
			t.Fatalf("View: %v", err)
		}
		if !view.HasCheck || !view.Check.At.Equal(at(5)) || view.Check.Failures != 0 || !view.Check.LastOK.Equal(at(5)) {
			t.Errorf("a key installed after a check reads %+v, want the project's record carried and not reset", view.Check)
		}
	})
}

// fieldNames is a struct's fields in the order they are declared.
func fieldNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	return names
}

// importsOf is one of this package's own files as the set of packages it
// imports.
func importsOf(t *testing.T, name string) (map[string]bool, error) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imported := make(map[string]bool, len(f.Imports))
	for _, imp := range f.Imports {
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("%s imports %s, which is not a readable string", name, imp.Path.Value)
		}
		imported[pkg] = true
	}
	return imported, nil
}
