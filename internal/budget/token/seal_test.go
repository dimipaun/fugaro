package token_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// sealKey builds a KeySize-byte key whose bytes are seed, seed+1, … — a key
// the tests can name, never a real one.
func sealKey(seed byte) []byte {
	k := make([]byte, token.KeySize)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// TestSealRoundTripBindsTheNameAsAdditionalData: a sealed envelope opens under
// the key and the *name* it was sealed for, and under no other name. The AAD
// is the property §7.1 names and no live step can isolate (every live path
// fails earlier, at the key), so this test is where it is held: a blob sealed
// for one object's name cannot be opened as another object's token.
func TestSealRoundTripBindsTheNameAsAdditionalData(t *testing.T) {
	key, name := sealKey(0x40), token.ObjectKey("aurora-app", "20261002-120000-ab12")
	plain := []byte("a custom token whose claims are a run's identity")

	env, err := token.Seal(key, name, plain)
	if err != nil {
		t.Fatal(err)
	}
	if env[0] != token.EnvelopeVersion {
		t.Fatalf("envelope starts with %#x, want version %#x", env[0], token.EnvelopeVersion)
	}
	if !token.Sealed(env) {
		t.Fatal("Sealed does not recognize an envelope Seal wrote")
	}
	if bytes.Contains(env, plain) {
		t.Fatal("the plaintext is readable in the sealed envelope")
	}
	if len(env) != 1+12+len(plain)+16 {
		t.Fatalf("envelope is %d bytes, want version+nonce+ciphertext+tag", len(env))
	}
	got, err := token.Unseal(key, name, env)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("unseal = %q, %v", got, err)
	}
	// The name is authenticated: the same key under another object's name
	// does not open it.
	other := token.ObjectKey("aurora-app", "20261002-120000-ffff")
	if _, err := token.Unseal(key, other, env); !errors.Is(err, token.ErrUnseal) {
		t.Fatalf("the envelope opened under another name: %v", err)
	}
}

// TestSealDrawsAFreshNonceAndAnotherKeyDoesNotOpen: two seals of the same
// plaintext under the same key differ (so a retry re-sealing the same token
// does not produce the same bytes), and a different launch's key does not open
// the envelope. The per-launch key, not the AAD, is what refuses a sideways
// copy of a whole object (§7.1).
func TestSealDrawsAFreshNonceAndAnotherKeyDoesNotOpen(t *testing.T) {
	key, name := sealKey(0x10), token.ObjectKey("aurora-app", "20261002-120000-ab12")
	plain := []byte("the run's custom token")
	first, err := token.Seal(key, name, plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := token.Seal(key, name, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two seals of the same plaintext are the same bytes: the nonce is not fresh")
	}
	for _, env := range [][]byte{first, second} {
		if _, err := token.Unseal(sealKey(0x20), name, env); !errors.Is(err, token.ErrUnseal) {
			t.Fatalf("another launch's key opened the envelope: %v", err)
		}
		got, err := token.Unseal(key, name, env)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("the envelope does not open under its own key: %q, %v", got, err)
		}
	}
}

// TestUnsealRefusesShortTamperedAndUnknownEnvelopes: the shape refusals of
// §7.4. A plaintext object is refused once the launcher seals — a mixed
// deployment is not an unsealing one — and an envelope from a format this
// runner does not know is refused rather than misread.
func TestUnsealRefusesShortTamperedAndUnknownEnvelopes(t *testing.T) {
	key, name := sealKey(0x30), token.ObjectKey("aurora-app", "20261002-120000-ab12")
	env, err := token.Seal(key, name, []byte("the run's custom token"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what string
		data []byte
	}{
		{"a plaintext token object", []byte("eyJhbGciOiJSUzI1NiJ9.eyJ1aWQiOiJhYmMifQ.sig")},
		{"an empty object", nil},
		{"an envelope cut short", env[:1+12+16-1]},
		{"a bare version byte", []byte{token.EnvelopeVersion}},
		{"an envelope from a newer format", append([]byte{token.EnvelopeVersion + 1}, env[1:]...)},
	} {
		if _, err := token.Unseal(key, name, tc.data); !errors.Is(err, token.ErrNotSealed) {
			t.Errorf("%s: err = %v, want ErrNotSealed", tc.what, err)
		}
	}
	// Tampering with the ciphertext or the tag is the tag check's refusal, not
	// the shape's: it is the same error a wrong key gets, because GCM cannot
	// tell the three apart and the runner's refusal is the same for each.
	for _, tc := range []struct {
		what string
		at   int
	}{
		{"a flipped ciphertext byte", len(env) - 20},
		{"a flipped tag byte", len(env) - 1},
		{"a flipped nonce byte", 4},
	} {
		bad := bytes.Clone(env)
		bad[tc.at] ^= 0x01
		if _, err := token.Unseal(key, name, bad); !errors.Is(err, token.ErrUnseal) {
			t.Errorf("%s: err = %v, want ErrUnseal", tc.what, err)
		}
	}
	// A key that is not a key is damage to the caller, not to the job.
	if _, err := token.Unseal(key[:token.KeySize-1], name, env); !errors.Is(err, token.ErrSealKey) {
		t.Errorf("a short key = %v, want ErrSealKey", err)
	}
}

// TestSealRefusesAKeyThatIsNotKeySize: 16 and 24 bytes are the two other
// lengths aes.NewCipher accepts, so without an explicit length check the
// channel's "AES-256-GCM under a 32-byte key" would quietly become AES-128 or
// AES-192 for a key its generator never produces; 33 bytes is a cipher that
// refuses it. Both halves refuse, and so does the marker's parse, because all
// three are the same statement: a seal key is KeySize bytes.
func TestSealRefusesAKeyThatIsNotKeySize(t *testing.T) {
	name := token.ObjectKey("aurora-app", "20261002-120000-ab12")
	good := sealKey(0xa0)
	env, err := token.Seal(good, name, []byte("the run's custom token"))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 16, 24, token.KeySize - 1, token.KeySize + 1} {
		short := make([]byte, size)
		if _, err := token.Seal(short, name, []byte("the run's custom token")); !errors.Is(err, token.ErrSealKey) {
			t.Errorf("Seal with a %d-byte key = %v, want ErrSealKey", size, err)
		}
		if _, err := token.Unseal(short, name, env); !errors.Is(err, token.ErrSealKey) {
			t.Errorf("Unseal with a %d-byte key = %v, want ErrSealKey", size, err)
		}
		if _, err := token.ParseKey(token.EncodeKey(short)); !errors.Is(err, token.ErrSealKey) {
			t.Errorf("ParseKey of a %d-byte key = %v, want ErrSealKey", size, err)
		}
	}
}

// TestParseKeyIsStrict: the marker's value decides whether the run unseals
// anything at all, so anything but one unpadded base64url string decoding to
// KeySize bytes is refused — whitespace, '=' padding, a second segment, the
// standard alphabet, a shorter or longer key, and a non-canonical spelling of
// the right bytes.
func TestParseKeyIsStrict(t *testing.T) {
	key := sealKey(0x50)
	enc := token.EncodeKey(key)
	if len(enc) != 43 || strings.ContainsAny(enc, "=+/ \n") {
		t.Fatalf("EncodeKey = %q, want 43 unpadded base64url characters", enc)
	}
	got, err := token.ParseKey(enc)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("ParseKey(EncodeKey(k)) = %v, %v", got, err)
	}
	// The three other 43-character spellings of the same key. 32 bytes is
	// three whole base64 groups plus two bytes, so the last character carries
	// four significant bits and two this encoding discards: three other
	// characters in the alphabet order decode to exactly these 32 bytes under
	// a non-strict decoder. They are refused, because the channel has one
	// spelling of a key and the redactor is told that one: without Strict()
	// these would be three live spellings the registration never saw.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, enc[len(enc)-1])
	if last < 0 {
		t.Fatalf("EncodeKey = %q: the last character is not in the alphabet", enc)
	}
	var nonCanonical []string
	for d := 1; d <= 3; d++ {
		spelling := enc[:len(enc)-1] + string(alphabet[last+d])
		// The fixture is only meaningful if a lax decoder accepts it and
		// reads the same key back.
		if got, err := base64.RawURLEncoding.DecodeString(spelling); err != nil || !bytes.Equal(got, key) {
			t.Fatalf("the fixture is wrong: %q does not decode to the same key (%v)", spelling, err)
		}
		nonCanonical = append(nonCanonical, spelling)
	}
	// The refusal may not carry any part of the value: this parse is the
	// marker's, and the marker's error is what a run publishes before any
	// redactor exists (D19, internal/runner's MalformedEnv). The test reads the
	// whole text rather than searching it for the value: the refusal words
	// itself and nothing else, so every bad value must produce the same
	// sentence. A search cannot hold that — it is vacuous for a value the
	// sentence itself contains (the empty string, a lone space) — and it would
	// pass while the text was built from the value.
	refusal := ""
	bad := append([]string{
		"", " ", "1", enc + "=", enc + "\n", enc + " ", "\t" + enc, enc[:len(enc)-1], enc + "A",
		"+" + enc[1:], "/" + enc[1:], enc + "." + enc,
		// Padding and whitespace inside a string of exactly the right length:
		// the length check cannot catch these, only the decoder can.
		enc[:len(enc)-1] + "=", enc[:len(enc)-1] + " ", enc[:len(enc)-1] + "\n",
		enc[:21] + " " + enc[22:], enc[:21] + "\n" + enc[22:],
	}, nonCanonical...)
	for _, bad := range bad {
		_, perr := token.ParseKey(bad)
		if !errors.Is(perr, token.ErrSealKey) {
			t.Errorf("ParseKey(%q) = %v, want ErrSealKey", bad, perr)
			continue
		}
		switch {
		case refusal == "":
			refusal = perr.Error()
			if refusal == "" {
				t.Fatalf("ParseKey(%q) refuses with an empty message", bad)
			}
		case perr.Error() != refusal:
			t.Errorf("ParseKey(%q) = %q, want the same refusal every other bad value gets (%q): the text depends on the value", bad, perr, refusal)
		}
	}
	if strings.Contains(refusal, enc) {
		t.Errorf("the refusal carries the key's own spelling: %q", refusal)
	}
}

// TestSealedObjectRoundTrip: PutObject seals what TakeObject unseals, and
// the bucket holds ciphertext, not the token.
func TestSealedObjectRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	key := sealKey(0x60)
	tok := e.mint(t, c, t0)
	if err := token.PutObject(ctx, b, c.Slug, c.Run, tok, key); err != nil {
		t.Fatal(err)
	}
	data, _, err := b.Read(ctx, token.ObjectKey(c.Slug, c.Run))
	if err != nil {
		t.Fatal(err)
	}
	if !token.Sealed(data) || strings.Contains(string(data), tok) {
		t.Fatal("the object the launcher wrote is not sealed")
	}
	got, err := token.TakeObject(ctx, b, c.Slug, c.Run, key)
	if err != nil || got != tok {
		t.Fatalf("take = %q, %v", got, err)
	}
	// A second put does not replace it: the object is the run's identity and
	// a replacement is an identity swap (PutObject's contract); the launcher's
	// retry deletes the earlier object first (DeleteObject).
	if err := token.PutObject(ctx, b, c.Slug, c.Run, tok, sealKey(0x61)); !errors.Is(err, token.ErrObjectExists) {
		t.Fatalf("a second put replaced the sealed object: %v", err)
	}
}

// TestTakeObjectRefusesAPlaintextObjectWhenAKeyIsPresented is §7.4's
// mixed-deployment row: once a key is presented, a plaintext object is the
// exposure coming back, and it is refused rather than read. This is the one
// the "accept plaintext with a key" hand mutation breaks.
func TestTakeObjectRefusesAPlaintextObjectWhenAKeyIsPresented(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	tok := e.mint(t, c, t0)
	// Written straight to the object: PutObject has no plaintext form.
	if _, err := b.Create(ctx, token.ObjectKey(c.Slug, c.Run), []byte(tok), "text/plain"); err != nil {
		t.Fatal(err)
	}
	for _, key := range [][]byte{sealKey(0x70), sealKey(0x71)} {
		got, err := token.TakeObject(ctx, b, c.Slug, c.Run, key)
		if !errors.Is(err, token.ErrNotSealed) || got != "" {
			t.Fatalf("a plaintext object with a key = (%q, %v), want ErrNotSealed and no token", got, err)
		}
		if strings.Contains(err.Error(), tok) {
			t.Fatal("the refusal carries the token")
		}
	}
}

// TestTakeObjectRefusesASealedObjectWithNoKeyOrTheWrongKey: a sealed object
// with no usable key is the theft case (§7.4), and it is never read as the
// bytes; a wrong key is the same refusal. Neither hands out anything.
func TestTakeObjectRefusesASealedObjectWithNoKeyOrTheWrongKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	c := claims()
	key := sealKey(0x80)
	tok := e.mint(t, c, t0)
	for name, take := range map[string]func() (string, error){
		"no key": func() (string, error) { return token.TakeObject(ctx, b, c.Slug, c.Run, nil) },
		"a wrong key": func() (string, error) {
			return token.TakeObject(ctx, b, c.Slug, c.Run, sealKey(0x81))
		},
	} {
		if err := token.DeleteObject(ctx, b, c.Slug, c.Run); err != nil {
			t.Fatal(err)
		}
		if err := token.PutObject(ctx, b, c.Slug, c.Run, tok, key); err != nil {
			t.Fatal(err)
		}
		got, err := take()
		want := token.ErrSealedNoKey
		if name == "a wrong key" {
			want = token.ErrUnseal
		}
		if !errors.Is(err, want) || got != "" {
			t.Fatalf("%s = (%q, %v), want %v and no token", name, got, err, want)
		}
		if strings.Contains(err.Error(), tok) || strings.Contains(got, tok) {
			t.Fatalf("%s: the refusal carries the token", name)
		}
	}
}

// TestTakeObjectRefusesAnObjectSealedForAnotherName: the AAD's property, at
// the object level: bytes sealed for one run's key are put under another run's
// key, and the take under the *same* key refuses, because the name it is
// stored under is not the name the envelope was sealed for. (A copy between
// two real runs fails earlier, at the key, which is the per-launch key's
// property and not this one.)
func TestTakeObjectRefusesAnObjectSealedForAnotherName(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	victim, thief := claims(), claims()
	thief.Run = "20261002-130000-cd34"
	key := sealKey(0x90)
	env, err := token.Seal(key, token.ObjectKey(victim.Slug, victim.Run), []byte(e.mint(t, victim, t0)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create(ctx, token.ObjectKey(thief.Slug, thief.Run), env, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	got, err := token.TakeObject(ctx, b, thief.Slug, thief.Run, key)
	if !errors.Is(err, token.ErrUnseal) || got != "" {
		t.Fatalf("an object sealed for another name = (%q, %v), want ErrUnseal and no token", got, err)
	}
}

// TestSealedIsAShapeTestThatSurvivesShortObjects: Sealed reads the object's
// first byte, and it reads it only on an object long enough to have an
// envelope at all — a version byte, a nonce and a tag, 29 bytes here. The
// guard is what keeps the one caller that has not read the object yet
// (openToken, which asks Sealed about bytes a bucket handed it, empty objects
// included) from reading past the end of them; the shape is not a proof of
// anything, and Unseal's tag check is what makes the bytes a token.
//
// Mutation (run, restored): drop `len(data) >= minEnvelope` from Sealed — the
// empty and short rows below panic with an index out of range instead of
// answering false, and the object-level case does it through TakeObject.
func TestSealedIsAShapeTestThatSurvivesShortObjects(t *testing.T) {
	const envelopeLen = 1 + 12 + 16      // version, nonce, tag
	short := make([]byte, envelopeLen-1) // a version byte and part of a nonce
	short[0] = token.EnvelopeVersion
	overVersion := make([]byte, envelopeLen)
	overVersion[0] = token.EnvelopeVersion + 1 // long enough, and a format we do not know
	full := make([]byte, envelopeLen)
	full[0] = token.EnvelopeVersion
	for _, tc := range []struct {
		what string
		data []byte
		want bool
	}{
		{"an empty object", nil, false},
		{"an empty object that is not nil", []byte{}, false},
		{"a bare version byte", []byte{token.EnvelopeVersion}, false},
		{"a version byte and part of a nonce", short, false},
		{"a plaintext token", []byte("eyJhbGciOiJSUzI1NiJ9.eyJ1aWQiOiJhYmMifQ.sig"), false},
		{"an envelope-shaped object from another version", overVersion, false},
		{"an envelope with no plaintext in it", full, true},
	} {
		if got := token.Sealed(tc.data); got != tc.want {
			t.Errorf("Sealed(%s) = %v, want %v", tc.what, got, tc.want)
		}
	}
	// What Seal writes is recognized, and the empty object reaches the refusal
	// rather than a panic: openToken is the caller the guard is there for.
	e := newEnv(t)
	ctx := context.Background()
	_, b := bucket(t)
	key, name := sealKey(0xA0), token.ObjectKey("aurora-app", "20261002-120000-ab12")
	env, err := token.Seal(key, name, []byte(e.mint(t, claims(), t0)))
	if err != nil {
		t.Fatal(err)
	}
	if !token.Sealed(env) {
		t.Fatal("Sealed does not recognize an envelope Seal wrote")
	}
	if _, err := b.Create(ctx, name, []byte{}, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	got, err := token.TakeObject(ctx, b, "aurora-app", "20261002-120000-ab12", nil)
	if !errors.Is(err, token.ErrNotSealed) || got != "" {
		t.Fatalf("an empty object with no key = (%q, %v), want ErrNotSealed and no token", got, err)
	}
}
