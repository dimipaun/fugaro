package token

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrDeviceKey means a string is not the phone's public key in the one
// spelling the pairing channel has for it (§2 (e), M3). A refusal here is a
// refusal and never a re-encode: the rules admit four spellings of one key,
// so a value this package re-spelled for the caller would be a second way to
// name the same key.
var ErrDeviceKey = errors.New("budget token: not a device key")

// DeviceKeySize is the phone's X25519 public key: 32 raw bytes. §2 (e) reads
// the algorithm off the rules' own comment on the request's key, and §8's
// delivery AAD carries these bytes at a fixed width, so the size belongs to
// the channel and not to one caller.
const DeviceKeySize = 32

// deviceKeyEncoding is the one spelling of a device key: unpadded base64url,
// strictly decoded. 32 bytes is three whole base64 groups plus two characters,
// so the last character carries four significant bits and two this encoding
// discards — three other characters decode to the same 32 bytes — and Strict()
// is what refuses them. seal.go's keyEncoding pins the seal key the same way
// and for the same reason.
var deviceKeyEncoding = base64.RawURLEncoding.Strict()

// DecodeDeviceKey reads the phone's X25519 public key from the one spelling a
// pairing request may use: unpadded base64url of DeviceKeySize bytes, 43
// characters (§2 (e), §7.1 step 5). The rules accept base64 *or* base64url,
// padded or not, so a request node can hold several spellings of one key; the
// CLI pins one, so that the bytes it hashes into the typed code, the bytes it
// binds into the delivery's AAD (§8) and the key the phone holds are one value
// and not four. Padding, the standard alphabet, a wrong length and a
// non-canonical trailing character are each refused rather than repaired.
func DecodeDeviceKey(s string) ([]byte, error) {
	if len(s) != deviceKeyEncoding.EncodedLen(DeviceKeySize) {
		return nil, fmt.Errorf("%w: it must be one unpadded base64url string of %d characters decoding to %d bytes", ErrDeviceKey, deviceKeyEncoding.EncodedLen(DeviceKeySize), DeviceKeySize)
	}
	key, err := deviceKeyEncoding.DecodeString(s)
	if err != nil || len(key) != DeviceKeySize {
		return nil, fmt.Errorf("%w: it must be one unpadded base64url string of %d characters decoding to %d bytes", ErrDeviceKey, deviceKeyEncoding.EncodedLen(DeviceKeySize), DeviceKeySize)
	}
	return key, nil
}

// The typed code is decision 3's: eight characters of Crockford base32 over
// the first deviceCodeBytes bytes of SHA-256 of the *decoded* key, printed
// XXXX-XXXX. It is not an identity and it authorises nothing — pairing is
// discovery (#787) — and what it defeats is a photographed QR paired in place
// of the real phone, whose owner alone can make their screen show this code
// (M3). The 40 bits are chosen to make a mismatch visible, not for entropy:
// a shorter code makes a coincidence likelier, and it is read aloud.
const (
	deviceCodeBytes  = 5
	deviceCodeLength = 8
)

// deviceCodeAlphabet is Crockford base32: the digits and the letters without
// I, L, O and U, so that no character the code prints can be read as another
// one (decision 3's "read aloud and typed by a human").
var deviceCodeAlphabet = [32]byte{
	'0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
	'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'J', 'K', 'M', 'N', 'P', 'Q', 'R', 'S', 'T', 'V', 'W', 'X', 'Y', 'Z',
}

// Fingerprint returns the code the operator and the phone compare (decision
// 3). It takes the decoded key — DecodeDeviceKey's output — and never the text
// a request spelled it with, so the two halves of the pairing agree on the
// code whatever spelling the phone used (§2 (e)).
func Fingerprint(key []byte) string {
	sum := sha256.Sum256(key)
	// The first deviceCodeBytes bytes are one big-endian 40-bit integer, which
	// is exactly the deviceCodeLength five-bit groups the code prints.
	var bits uint64
	for _, b := range sum[:deviceCodeBytes] {
		bits = bits<<8 | uint64(b)
	}
	var code [deviceCodeLength]byte
	for i := deviceCodeLength - 1; i >= 0; i-- {
		code[i] = deviceCodeAlphabet[bits&0x1f]
		bits >>= 5
	}
	return string(code[:deviceCodeLength/2]) + "-" + string(code[deviceCodeLength/2:])
}

// CompareFingerprint reports whether typed is want as a human types it: case
// is not significant, the hyphen may be left out, and Crockford's own
// look-alikes fold to the digits they were misread as (I and L to 1, O to 0).
// The comparison itself is constant time, because a code compared byte by byte
// with an early exit tells a guesser where it went wrong — and the code is the
// one thing standing between a photographed QR and a pairing (M3).
func CompareFingerprint(typed, want string) bool {
	return subtle.ConstantTimeCompare([]byte(deviceFold(typed)), []byte(deviceFold(want))) == 1
}

// deviceFold is the typist's alphabet: upper case, hyphens dropped, and the
// three characters Crockford leaves out of the printed alphabet folded to the
// digit they look like. U is in neither half of that rule — no digit looks
// like it and it folds to nothing — so a typed U stays a mismatch.
func deviceFold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-':
			continue
		case c >= 'a' && c <= 'z':
			c -= 'a' - 'A'
		}
		switch c {
		case 'I', 'L':
			c = '1'
		case 'O':
			c = '0'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// deviceUIDPrefix and deviceClaim are M4's: a device's uid begins m~ — never
// r~, the runs bucket's family, and never p~, a pairing session's — and the
// one custom claim the token carries is mu, which rules.go's userClaim reads
// and requires to equal auth.uid.
const (
	deviceUIDPrefix = "m~"
	deviceClaim     = "mu"
	// deviceUIDBytes is the random part: 128 bits, 22 unpadded base64url
	// characters. It is drawn and never derived, so one device's name is not
	// guessable from another's, and it stays far inside the 128 bytes a uid
	// may be.
	deviceUIDBytes = 16
)

// DeviceToken is a minted device token and the uid it names. Its Token is the
// custom token the delivery's envelope carries to the phone (§8): the value in
// the pairing that must reach the phone and never a screen, a log line or a
// --json document. Its String and GoString keep it out of %v and %#v, as
// Session's do.
type DeviceToken struct {
	UID   string
	Token string
}

func (d DeviceToken) String() string   { return "token.DeviceToken{uid: " + d.UID + ", token redacted}" }
func (d DeviceToken) GoString() string { return d.String() }

// MintDevice mints the device's custom token for a fresh m~<random> uid
// (§7.1 step 6). Its claims are mu = uid and nothing else: the rules admit a
// device by mu agreeing with auth.uid *and* the device node existing (§2 (c),
// M4), and a token that also carried a run's claims would be a run identity
// and a device at once. exp is now + Lifetime, Firebase's maximum — the
// phone refreshes the token, and the device node, not the clock, is what
// revokes a pairing (D166, decision 1).
func MintDevice(ctx context.Context, signer Signer, now time.Time) (DeviceToken, error) {
	email := signer.Email()
	if email == "" {
		return DeviceToken{}, errors.New("budget token: no signer")
	}
	uid, err := deviceNewUID()
	if err != nil {
		return DeviceToken{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"iss": email, "sub": email, "aud": Audience,
		"iat": now.Unix(), "exp": now.Add(Lifetime).Unix(),
		"uid": uid,
		"claims": map[string]any{
			deviceClaim: uid,
		},
	})
	if err != nil {
		return DeviceToken{}, err
	}
	tok, err := signer.SignJWT(ctx, payload)
	if err != nil {
		return DeviceToken{}, err
	}
	// The signer is trusted, but a token that does not name the device it was
	// minted for must never leave this function (MintAt's own read-back). Both
	// halves are checked: the rules match mu against the token's uid, so a
	// signer that moved either one hands the phone a token nothing admits.
	got, err := parseJWT(tok)
	if err != nil {
		return DeviceToken{}, errors.New("budget token: the signer returned something that is not a JWT")
	}
	claims, _ := got["claims"].(map[string]any)
	if claimString(got, "uid") != uid || claimString(claims, deviceClaim) != uid {
		return DeviceToken{}, fmt.Errorf("%w: the signer returned a device token for another uid", ErrTokenMismatch)
	}
	return DeviceToken{UID: uid, Token: tok}, nil
}

// deviceNewUID draws one device's uid: deviceUIDBytes bytes from crypto/rand
// behind m~, in unpadded base64url. The alphabet is not taste — §8 ends the
// delivery's AAD with a zero byte after the uid, and unpadded base64url cannot
// spell one, so that terminator is the only zero before the key's own bytes. A
// refused draw is an error and never a fallback to a name anyone could guess.
func deviceNewUID() (string, error) {
	raw := make([]byte, deviceUIDBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("budget token: generating the device uid failed: %w", err)
	}
	return deviceUIDPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}
