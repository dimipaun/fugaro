package token_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

const (
	deviceSignerEmail = "fugaro-token-signer@owner-a.iam.gserviceaccount.com"
	// deviceCrockford is decision 3's alphabet, held here as a second
	// spelling of it so the shape test cannot agree with a moved constant.
	deviceCrockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	// deviceCodeOfOnes is the code decision 3 gives 32 bytes of 0x01: the
	// vector T2a is specified against.
	deviceCodeOfOnes = "EB6P-X112"
	// deviceCodeFolding carries a 0 and a 1, so the fold has something to
	// fold.
	deviceCodeFolding = "X010-GTPP"
)

var deviceNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// deviceCrockford40 renders the code decision 3 specifies straight from its
// words — SHA-256, the first five bytes, eight characters of Crockford base32
// — so a test can ask what a given input's code *is* without asking the
// package under test. The text-versus-bytes test and the pinned vector both
// need that.
func deviceCrockford40(b []byte) string {
	sum := sha256.Sum256(b)
	var bits uint64
	for _, x := range sum[:5] {
		bits = bits<<8 | uint64(x)
	}
	code := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		code[i] = deviceCrockford[bits&0x1f]
		bits >>= 5
	}
	return string(code[:4]) + "-" + string(code[4:])
}

// TestFingerprintIsOverDecodedBytes is the test this file exists for: the code
// is a function of the key's *decoded* bytes and never of the text the phone
// spelled them with. §2 (e)'s rules admit base64 or base64url, padded or not —
// several spellings of one key — so a code computed over the text would move
// with the spelling, and a code that moves is not the one binding pairing has
// (M3): the operator compares what the CLI read against what their own phone
// shows, and both ends derive it from the same 32 bytes.
func TestFingerprintIsOverDecodedBytes(t *testing.T) {
	raw := bytes.Repeat([]byte{0x01}, token.DeviceKeySize)
	spelled := base64.RawURLEncoding.EncodeToString(raw)

	got := token.Fingerprint(raw)
	if got != deviceCodeOfOnes {
		t.Errorf("Fingerprint of %d decoded bytes = %q, want %q", len(raw), got, deviceCodeOfOnes)
	}
	// The mutation this test is here for: hash the key's text instead of its
	// bytes. It is a different code, so the assertion above fails when the
	// implementation reads the spelling.
	if overText := deviceCrockford40([]byte(spelled)); got == overText {
		t.Fatalf("Fingerprint = %q, which is the code of the key's %d-character text: the code is derived from the text and not from the %d decoded bytes", got, len(spelled), len(raw))
	}
}

// TestFingerprintShapeIsCrockfordXXXXdashXXXX pins decision 3's shape: eight
// characters of Crockford base32, upper case, over the first five bytes of
// SHA-256 of the decoded key, printed XXXX-XXXX. Crockford's alphabet leaves
// out I, L, O and U, which is what makes the code readable aloud and typeable.
func TestFingerprintShapeIsCrockfordXXXXdashXXXX(t *testing.T) {
	keys := [][]byte{
		bytes.Repeat([]byte{0x01}, token.DeviceKeySize),
		bytes.Repeat([]byte{0x06}, token.DeviceKeySize),
		make([]byte, token.DeviceKeySize),
		bytes.Repeat([]byte{0xff}, token.DeviceKeySize),
	}
	for _, raw := range keys {
		code := token.Fingerprint(raw)
		if len(code) != 9 || code[4] != '-' {
			t.Fatalf("Fingerprint = %q, want eight characters with a hyphen after the fourth", code)
		}
		for i := range len(code) {
			if i == 4 {
				continue
			}
			if !strings.ContainsRune(deviceCrockford, rune(code[i])) {
				t.Errorf("Fingerprint = %q: character %d is %q, and Crockford base32 has no I, L, O or U", code, i, code[i])
			}
		}
		if want := deviceCrockford40(raw); code != want {
			t.Errorf("Fingerprint(%d bytes) = %q, and decision 3 over the same bytes gives %q", len(raw), code, want)
		}
	}
	if got := token.Fingerprint(bytes.Repeat([]byte{0x01}, token.DeviceKeySize)); got != deviceCodeOfOnes {
		t.Errorf("the all-0x01 vector moved: Fingerprint = %q, want %q", got, deviceCodeOfOnes)
	}
}

// TestCompareFingerprintFoldsHumanTyping is decision 3's human half. The code
// is read off a screen and typed into a terminal, so case, the hyphen and
// Crockford's own look-alikes must not decide whether the phone in the
// operator's hand is the phone that wrote the request (§7.1 step 5).
func TestCompareFingerprintFoldsHumanTyping(t *testing.T) {
	want := token.Fingerprint(bytes.Repeat([]byte{0x06}, token.DeviceKeySize))
	if want != deviceCodeFolding {
		t.Fatalf("the folding vectors are stale: the code is %q, not %q", want, deviceCodeFolding)
	}
	for _, typed := range []string{
		"X010-GTPP",
		"X010GTPP", // the hyphen is optional
		"x010-gtpp",
		"x010gtpp", // case does not matter
		"XOIO-GTPP",
		"xoio-gtpp", // O folds to 0, I folds to 1
		"XOL0GTPP",  // L folds to 1
	} {
		if !token.CompareFingerprint(typed, want) {
			t.Errorf("CompareFingerprint(%q, %q) = false, and that is how the operator types the code", typed, want)
		}
	}
}

// TestCompareFingerprintRefusesOneCharacterOff is the same comparison's other
// half: folding is a courtesy to the typist, not a licence to match another
// code. Anything that does not fold to the printed code is a mismatch, and the
// caller's refusal after one is what leaves a refused pairing with no side
// effect at all.
func TestCompareFingerprintRefusesOneCharacterOff(t *testing.T) {
	const want = deviceCodeFolding
	for _, typed := range []string{
		"X010-GTPN", // the last character
		"Y010-GTPP", // the first
		"X011-GTPP", // a character inside
		"X010-GTP",  // one short
		"X010-GTPPP",
		"",         // nothing typed at all
		"X010-GTU", // U is Crockford's and folds to nothing, so it is never a 0 or a 1
	} {
		if token.CompareFingerprint(typed, want) {
			t.Errorf("CompareFingerprint(%q, %q) = true", typed, want)
		}
	}
}

// TestDecodeDeviceKeyRefusesOtherSpellings holds the CLI to the one spelling
// §2 (e) says it must pin. The rules accept base64 *or* base64url, padded or
// not, so every one of these reaches the pairing node; the CLI derives the
// code from the decoded bytes and binds them into the delivery's AAD (§8), so
// it reads one spelling and refuses the rest rather than re-encoding them.
func TestDecodeDeviceKeyRefusesOtherSpellings(t *testing.T) {
	raw := bytes.Repeat([]byte{0x01}, token.DeviceKeySize)
	good := base64.RawURLEncoding.EncodeToString(raw)
	if len(good) != 43 {
		t.Fatalf("the pinned spelling of a %d-byte key is %d characters, and the vector below assumes 43", token.DeviceKeySize, len(good))
	}
	key, err := token.DecodeDeviceKey(good)
	if err != nil {
		t.Fatalf("DecodeDeviceKey of the pinned spelling failed: %v", err)
	}
	if !bytes.Equal(key, raw) {
		t.Errorf("DecodeDeviceKey of the pinned spelling gave %d bytes, want the %d it names", len(key), len(raw))
	}
	for what, s := range map[string]string{
		"padded base64url":      base64.URLEncoding.EncodeToString(raw),
		"standard base64":       base64.StdEncoding.EncodeToString(raw),
		"one byte short":        base64.RawURLEncoding.EncodeToString(raw[:token.DeviceKeySize-1]),
		"one byte long":         base64.RawURLEncoding.EncodeToString(append(append([]byte{}, raw...), 0x02)),
		"empty":                 "",
		"the standard alphabet": good[:20] + "/" + good[21:],
		// The last character of a 43-character encoding carries two bits this
		// spelling discards, so it has three neighbours that decode to the same
		// 32 bytes (seal.go's keyEncoding argues the same for the seal key).
		"a non-canonical trailing character": good[:42] + "F",
	} {
		if _, err := token.DecodeDeviceKey(s); !errors.Is(err, token.ErrDeviceKey) {
			t.Errorf("DecodeDeviceKey of %s = %v, want ErrDeviceKey", what, err)
		}
	}
}

// deviceSigner is a Signer that does not sign: it renders the claim set as a
// compact JWT with a placeholder signature, which is enough for the minter's
// own read-back (parseJWT verifies nothing — Google does that at sign-in).
// Its mangle hook lets a test hand back a token the signer rewrote.
type deviceSigner struct {
	email  string
	mangle func(map[string]any)
	notJWT bool
}

func (s deviceSigner) Email() string { return s.email }

func (s deviceSigner) SignJWT(_ context.Context, payload []byte) (string, error) {
	if s.notJWT {
		return "not-a-token", nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	if s.mangle != nil {
		s.mangle(claims)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("device-signature"))
	return head + "." + base64.RawURLEncoding.EncodeToString(body) + "." + sig, nil
}

// deviceClaimSet decodes the claim set of a token a test's signer produced.
func deviceClaimSet(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("the minted token is not a compact JWT: %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decoding the minted claim set failed: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding the minted claim set failed: %v", err)
	}
	return out
}

// TestDeviceTokenShape pins M4's token: the uid is m~<random> and never r~ or
// p~, the one custom claim is mu and it is that uid, the minted token names
// the same uid, and exp is iat + Lifetime. The rules admit a device by mu
// agreeing with auth.uid *and* the device node existing, so the claim and the
// uid are two halves of one name.
func TestDeviceTokenShape(t *testing.T) {
	d, err := token.MintDevice(context.Background(), deviceSigner{email: deviceSignerEmail}, deviceNow)
	if err != nil {
		t.Fatalf("MintDevice failed: %v", err)
	}
	if !strings.HasPrefix(d.UID, "m~") {
		t.Errorf("the device uid is %q, and a device's uid begins m~", d.UID)
	}
	for _, prefix := range []string{"r~", "p~"} {
		if strings.HasPrefix(d.UID, prefix) {
			t.Errorf("the device uid %q begins %s, which belongs to another family", d.UID, prefix)
		}
	}
	if len(d.UID) <= len("m~") {
		t.Errorf("the device uid is %q and carries no random part", d.UID)
	}
	for _, c := range d.UID[len("m~"):] {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", c) {
			t.Errorf("the device uid %q carries %q, which is not unpadded base64url (§8's uid rule)", d.UID, c)
		}
	}

	got := deviceClaimSet(t, d.Token)
	if uid, _ := got["uid"].(string); uid != d.UID {
		t.Errorf("the minted token names uid %q and MintDevice returned %q", uid, d.UID)
	}
	// The phone hands this token to Firebase's own sign-in, so the envelope's
	// iss, sub and aud are as load-bearing as the uid: the run token pins them
	// for the same reason (token_test.go's TestMintClaims).
	if got["iss"] != deviceSignerEmail || got["sub"] != deviceSignerEmail {
		t.Errorf("the minted token's iss and sub are %v and %v, and the signer is %s", got["iss"], got["sub"], deviceSignerEmail)
	}
	if got["aud"] != token.Audience {
		t.Errorf("the minted token's aud is %v, and the phone signs in against %s", got["aud"], token.Audience)
	}
	claims, _ := got["claims"].(map[string]any)
	if len(claims) != 1 {
		t.Errorf("the device token carries %d custom claims, and M4's is mu alone: %v", len(claims), claims)
	}
	if mu, _ := claims["mu"].(string); mu != d.UID {
		t.Errorf("the token's mu claim is %q and its uid is %q, and the rules admit a device only where they agree", mu, d.UID)
	}
	iat, iok := got["iat"].(float64)
	exp, eok := got["exp"].(float64)
	if !iok || !eok {
		t.Fatalf("the minted token has no iat and exp")
	}
	if want := iat + token.Lifetime.Seconds(); exp != want {
		t.Errorf("exp is %v and Lifetime puts it at %v", exp, want)
	}

	// The token is the one value in the pairing that goes to the phone and
	// to nothing else: formatting a DeviceToken must not show it (M5's rule
	// for the MAC key, applied to the token).
	sig := strings.Split(d.Token, ".")[2]
	for _, s := range []string{sprint("%v", d), sprint("%+v", d), sprint("%#v", d)} {
		if strings.Contains(s, d.Token) || strings.Contains(s, sig) {
			t.Fatal("formatting the device token shows the token")
		}
	}
}

// TestDeviceUIDIsDrawnFresh holds the two properties deviceNewUID's comment
// claims: the name comes from the random source and not from anything a caller
// can derive, and two mints do not name the same device — a device's uid is
// what USER matches the token's mu against, so a name two devices share is two
// devices admitted as one. The source is the seam keyReader already has for the
// same reason (export_test.go's SetKeyReader).
func TestDeviceUIDIsDrawnFresh(t *testing.T) {
	ctx := context.Background()
	signer := deviceSigner{email: deviceSignerEmail}
	first, err := token.MintDevice(ctx, signer, deviceNow)
	if err != nil {
		t.Fatalf("MintDevice failed: %v", err)
	}
	second, err := token.MintDevice(ctx, signer, deviceNow)
	if err != nil {
		t.Fatalf("MintDevice failed: %v", err)
	}
	if first.UID == second.UID {
		t.Fatalf("two mints named the same device %q: the uid is derived and not drawn", first.UID)
	}

	// A source the test holds still: the uid must be the bytes it handed over
	// and no others.
	source := bytes.Repeat([]byte{0x2a}, 32)
	restore := token.SetDeviceUIDReader(bytes.NewReader(source))
	defer restore()
	d, err := token.MintDevice(ctx, signer, deviceNow)
	if err != nil {
		t.Fatalf("MintDevice with a test source failed: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(d.UID, "m~"))
	if err != nil {
		t.Fatalf("the uid's random part is not unpadded base64url: %v", err)
	}
	if len(raw) < 16 {
		t.Errorf("the uid carries %d random bytes, and a device's name must not be guessable", len(raw))
	}
	if !bytes.Equal(raw, source[:len(raw)]) {
		t.Errorf("the uid is not the random source's own bytes: %d bytes came back that the source never handed over", len(raw))
	}
}

// TestDeviceUIDDrawFailureIsRefused is the other half of that claim: a source
// that cannot fill the buffer stops the mint. A fallback — a shorter name, or a
// fixed one — would be a device anybody could name, and the refusal has to be
// an error and not a token for a uid nobody drew.
func TestDeviceUIDDrawFailureIsRefused(t *testing.T) {
	restore := token.SetDeviceUIDReader(strings.NewReader("too short"))
	defer restore()
	d, err := token.MintDevice(context.Background(), deviceSigner{email: deviceSignerEmail}, deviceNow)
	if err == nil {
		t.Fatalf("a source with too few bytes minted a token naming %q", d.UID)
	}
	if d.UID != "" || d.Token != "" {
		t.Errorf("a refused draw returned %+v", d)
	}
}

// TestDeviceTokenRefusesSignerThatRenamesUID holds the minter's read-back: the
// signer is trusted, but the rules admit the token the phone holds, so a token
// that names another uid — or whose mu disagrees with the uid it does name —
// must never leave MintDevice.
func TestDeviceTokenRefusesSignerThatRenamesUID(t *testing.T) {
	rename := func(m map[string]any) {
		uid, _ := m["uid"].(string)
		m["uid"] = "m~renamed-" + strings.TrimPrefix(uid, "m~")
	}
	relabel := func(m map[string]any) {
		if claims, ok := m["claims"].(map[string]any); ok {
			claims["mu"] = "m~another-device"
		}
	}
	for _, tc := range []struct {
		what         string
		signer       deviceSigner
		wantMismatch bool
	}{
		{"the signer renamed the uid", deviceSigner{email: deviceSignerEmail, mangle: rename}, true},
		{"mu disagrees with the uid", deviceSigner{email: deviceSignerEmail, mangle: relabel}, true},
		{"the signer returned no JWT", deviceSigner{email: deviceSignerEmail, notJWT: true}, false},
	} {
		d, err := token.MintDevice(context.Background(), tc.signer, deviceNow)
		if err == nil {
			t.Fatalf("%s and MintDevice returned a token naming %q", tc.what, d.UID)
		}
		if tc.wantMismatch && !errors.Is(err, token.ErrTokenMismatch) {
			t.Errorf("%s, and MintDevice refused with %v rather than ErrTokenMismatch", tc.what, err)
		}
	}
}
