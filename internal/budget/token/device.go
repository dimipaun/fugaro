package token

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
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

// deviceUIDReader is deviceNewUID's random source: crypto/rand, replaced only
// by tests (export_test.go's SetDeviceUIDReader, as keyReader has SetKeyReader).
var deviceUIDReader io.Reader = rand.Reader

// deviceNewUID draws one device's uid: deviceUIDBytes bytes from crypto/rand
// behind m~, in unpadded base64url. The alphabet is not taste, and it is not
// §8's rule either — that AAD carries the *pairing* uid, p~, and never this
// one. A device's uid is an RTDB key (config/admin/devices/$uid, §7.1 step 7)
// and a Firebase uid, so the standard alphabet's '/' would split the node's
// path and '.', '#', '$', '[', ']' are characters a key may not hold at all; unpadded
// base64url spells none of them, and it is the one spelling this package
// already pins for a key (seal.go's keyEncoding). A refused draw is an error
// and never a fallback to a name anyone could guess.
func deviceNewUID() (string, error) {
	raw := make([]byte, deviceUIDBytes)
	if _, err := io.ReadFull(deviceUIDReader, raw); err != nil {
		return "", fmt.Errorf("budget token: generating the device uid failed: %w", err)
	}
	return deviceUIDPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// The pairing delivery's envelope (§8, M6). It is a **second envelope in this
// package and not seal.go's API**: seal.go's key is shared — the launcher
// generates it and carries it in the marker — while the delivery's key is
// agreed, because the only thing on the wire is the phone's public key. So the
// sender draws an ephemeral X25519 key pair, performs ECDH with the phone's
// key, and derives the AEAD key with HKDF-SHA-256. Both halves are the standard
// library (crypto/ecdh, crypto/hkdf), so go.mod gains no module.
//
//	version byte (1) || ephemeral public key (32) || nonce (12) || ciphertext || tag (16)
//	key = HKDF-SHA-256(ECDH(ephemeral_private, phone_public), salt=nil, info="fugaro-pairing-v1")
//	aad = "pairing/" + uid + 0x00 + phone_public_key_bytes (32) + 0x00 + version byte
//
// **This is a capability, not a boundary.** The ephemeral private key lives in
// the CLI's memory and the plaintext in its process, so it removes the value of
// what a peer can reach — a photographed QR, a copied payload — and not the
// reach: an operator, the launcher, or anyone holding database admin reads the
// delivery node as this process would. §8 says it in seal.go's own words rather
// than implying it, and nothing here claims more.
const (
	// DeliveryEnvelopeVersion is the delivery envelope's first byte, and the
	// version bound into its AAD. It is first for seal.go's reason: a later
	// format is refused rather than misread as this one.
	DeliveryEnvelopeVersion byte = 1
	// deliveryInfo is HKDF's info string: the label that makes this key
	// agreement's key this one and not a key some other use of the same ECDH
	// secret would derive (§8).
	deliveryInfo = "fugaro-pairing-v1"
	// deliveryMinEnvelope is a version byte, the ephemeral key, a nonce and a
	// tag: an envelope whose plaintext is empty is still this long, so anything
	// shorter is not one.
	deliveryMinEnvelope = 1 + DeviceKeySize + nonceSize + tagSize
	// pairingUIDBytes is the random part a pairing uid carries: pairing.go
	// draws 32 bytes behind PairingPrefix. §8's AAD rule is that a uid holds no
	// zero byte, and this is what makes that true of every uid this package
	// mints.
	pairingUIDBytes = 32
)

// ErrPairingUID means a string is not a pairing uid: not one of the p~<random>
// names MintPairing mints, so it is not a pairing the delivery node could
// belong to. The AAD's first zero byte is the uid's terminator (§8), so a
// string that is not one this package's own shape admits is refused rather than
// bound into an AAD nothing could reproduce.
var ErrPairingUID = errors.New("budget token: not a pairing uid")

// pairingUIDEncoding is the one spelling of a pairing uid's body: unpadded
// base64url, strictly decoded, as deviceKeyEncoding and seal.go's keyEncoding
// are. Strict() refuses the three last characters that decode to the same bytes
// as the canonical one — 32 bytes is 43 characters carrying two bits this
// encoding discards — so a uid that reached the AAD in a non-canonical spelling
// would be a second name for one pairing.
var pairingUIDEncoding = base64.RawURLEncoding.Strict()

// checkPairingUID refuses anything that is not a pairing uid: PairingPrefix and
// pairing.go's own draw of pairingUIDBytes bytes in the one spelling (§2 (e)),
// so an AAD binds the same name the pairing node and the token carry. The body
// is what makes §8's AAD canonical: base64url spells no zero byte, so the first
// zero in the AAD is the uid's terminator and never part of the name.
func checkPairingUID(uid string) error {
	refuse := fmt.Errorf("%w: %q is not %q followed by one unpadded base64url string of %d bytes, the one spelling a pairing uid has (§2 (e))", ErrPairingUID, uid, PairingPrefix, pairingUIDBytes)
	body, ok := strings.CutPrefix(uid, PairingPrefix)
	if !ok {
		return fmt.Errorf("%w: %q does not begin with %q, so it is not a pairing session's name", ErrPairingUID, uid, PairingPrefix)
	}
	raw, err := pairingUIDEncoding.DecodeString(body)
	if err != nil || len(raw) != pairingUIDBytes {
		return refuse
	}
	return nil
}

// SealDelivery returns plaintext in the delivery envelope, sealed for the phone
// holding phonePub under the pairing named by uid (§8). phonePub is the key's
// decoded bytes — DecodeDeviceKey's output — and never the text a request
// spelled it with, so the bytes this binds are the bytes the phone hashed into
// the typed code.
//
// Every refusal is a refusal and never a re-spelling: a uid that is not a p~
// pairing (ErrPairingUID), a key that is not DeviceKeySize bytes, and a key
// that is not a point X25519 will combine with (a low-order point makes ECDH
// return the all-zero secret, which crypto/ecdh refuses) are each ErrDeviceKey.
// The ephemeral private key is drawn here and never leaves this function: it is
// not returned, not stored and not named by anything the caller can reach.
//
// What the envelope is not, in §8's own sentence: **a capability, not a
// boundary**. It removes the value of a photographed QR or a copied payload and
// it removes no reach — see the block above.
func SealDelivery(phonePub []byte, uid string, plaintext []byte) ([]byte, error) {
	if err := checkPairingUID(uid); err != nil {
		return nil, err
	}
	if len(phonePub) != DeviceKeySize {
		return nil, fmt.Errorf("%w: the phone's key is %d bytes and an X25519 public key is %d", ErrDeviceKey, len(phonePub), DeviceKeySize)
	}
	// X25519's canonical encoding has the top bit of the last byte clear, and
	// crypto/ecdh masks that bit away rather than refusing it: a key with it set
	// combines to the same secret, so sealing would succeed and bind bytes the
	// phone's own priv.PublicKey().Bytes() never spells — an AAD the phone
	// cannot reproduce, and an envelope it could never open. Only a
	// non-conforming client sends one. Refused rather than canonicalised,
	// because rewriting the key would be this package spelling a key for the
	// phone, which §2 (e) refuses for the request's text for the same reason.
	if phonePub[DeviceKeySize-1]&0x80 != 0 {
		return nil, fmt.Errorf("%w: the phone's key ends %#x, whose top bit is X25519's discarded one, so this is not the canonical spelling of the key the phone holds", ErrDeviceKey, phonePub[DeviceKeySize-1])
	}
	pub, err := ecdh.X25519().NewPublicKey(phonePub)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceKey, err)
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("budget token: generating the delivery's ephemeral key failed: %w", err)
	}
	shared, err := ephemeral.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("%w: the phone's key is a low-order point, so the agreed secret would be all zeros: %v", ErrDeviceKey, err)
	}
	g, err := deliveryAEAD(shared)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("budget token: generating the delivery's nonce failed: %w", err)
	}
	out := make([]byte, 0, deliveryMinEnvelope+len(plaintext))
	out = append(out, DeliveryEnvelopeVersion)
	out = append(out, ephemeral.PublicKey().Bytes()...)
	out = append(out, nonce...)
	return g.Seal(out, nonce, plaintext, deliveryAAD(uid, phonePub, DeliveryEnvelopeVersion)), nil
}

// deliveryAAD is the delivery envelope's additional authenticated data: the
// literal "pairing/", the pairing uid, a zero byte, the phone's public key, a
// zero byte, and the version byte — in that order, never reordered, truncated,
// re-encoded or length-prefixed (§8).
//
// What makes it canonical is the *layout* and not the absence of a byte, and
// the distinction matters because the key is raw. The uid is the only
// variable-width part, so the first zero after it is its terminator, and a uid
// cannot contain one. The key can, and routinely does — these are 32 raw
// public-key bytes and roughly one in eight such values holds a 0x00 — so a
// zero inside the key is positional data and nothing else: everything after the
// uid's terminator is fixed-width and in a fixed order, so no length prefix is
// needed and none is written. The second zero byte, before the version, is
// seal.go's shape kept for consistency; with a fixed-width key it is redundant,
// and dropping it would be a *different* AAD rather than a bug, which is why
// §8 pins the layout instead of leaving it to taste.
//
// The version byte is bound as well as checked, for seal.go's reason. The uid
// is bound so a payload cannot be moved to another pairing's node. The key is
// bound so a payload sealed for one phone cannot be opened by another phone
// that shares the node.
func deliveryAAD(uid string, phonePub []byte, version byte) []byte {
	out := make([]byte, 0, len("pairing/")+len(uid)+len(phonePub)+3)
	out = append(out, "pairing/"...)
	out = append(out, uid...)
	out = append(out, 0)
	out = append(out, phonePub...)
	out = append(out, 0, version)
	return out
}

// deliveryAEAD is the cipher both halves use: the agreed secret through
// HKDF-SHA-256 with no salt and this envelope's info string, then AES-256-GCM.
// The derivation is ECDH's, not seal.go's: the two envelopes share the shape
// and the AEAD and never the key.
func deliveryAEAD(shared []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, shared, nil, deliveryInfo, KeySize)
	if err != nil {
		return nil, fmt.Errorf("budget token: deriving the delivery's key failed: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("budget token: the delivery's cipher: %v", err)
	}
	return cipher.NewGCM(block)
}

// NewMACKey draws the per-pairing MAC key (M5, D171): KeySize bytes from
// crypto/rand, the same draw and the same EncodeKey/ParseKey discipline
// seal.go's NewKey has, because it is the same kind of value. One key per
// pairing; it travels to the phone only inside SealDelivery's envelope and
// never in the QR (D171). A failed draw is an error and never a fallback to a
// guessable key.
func NewMACKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(keyReader, key); err != nil {
		return nil, fmt.Errorf("budget token: generating the MAC key failed: %w", err)
	}
	return key, nil
}

// DeliveryConfig is the installation's Firebase configuration as the phone
// stores it (D199, §4.4): the bootstrap the QR also carries, whose copy here is
// the authoritative record. Its four field names are the QR's own — project,
// gcp_project, database_url, api_key, as internal/cli's pairQR writes them
// (§4.4) — because the phone refuses a delivery whose contents disagree with
// what the QR said the project is, and that refusal compares field to field.
// One field named for both would leave nothing to compare.
type DeliveryConfig struct {
	Project     string `json:"project"`
	GCPProject  string `json:"gcp_project"`
	DatabaseURL string `json:"database_url"`
	APIKey      string `json:"api_key"`
}

// Delivery is what the pairing delivery's envelope carries (decision 5, §4.4):
// the configuration the app stores, the device token, and the per-pairing MAC
// key. All three travel sealed and none of them is in the QR but the
// configuration's bootstrap copy.
//
// No format verb prints the token or the MAC key — %v, %+v and %#v answer
// String, as DeviceToken's and keychain.Key's do, because the failure M5 names
// (a leaked key is a forged action on the operator's behalf, a leaked token is
// the device) is one %v away in a command whose job is to print what it did.
//
// encoding/json does not reach a printer's redaction on its own, so
// MarshalJSON is the redaction too: a Delivery that lands in a --json document
// by accident (§4.5's rule is that a token never appears there) prints neither
// secret. The payload the phone actually decodes is
// MarshalPayload's — one deliberate call, and the field names it writes are the
// ones P6.8 reads.
type Delivery struct {
	Config DeliveryConfig
	Device DeviceToken
	MACKey []byte
}

// String, Format and GoString are the redaction, in every shape a printer can
// ask for one. Format answers every verb, so a verb added later is redacted by
// default rather than by remembering this list, and GoString is what %#v would
// use if Format did not win first. The project and the device uid are printed
// deliberately: a uid is a key and not a secret (§4.5), and a print that named
// nothing would send an operator to the keychain item to find out which device
// a delivery was for.
func (d Delivery) String() string {
	return "token.Delivery{config: " + d.Config.Project + ", device: " + d.Device.UID + ", token and MAC key redacted}"
}

// Format answers every verb with the redacted String, so %x and %q of a
// Delivery are as safe as %v. seal.go's and keychain.go's rule: the material
// leaves through MarshalPayload, which no printer calls.
func (d Delivery) Format(state fmt.State, _ rune) { io.WriteString(state, d.String()) }

func (d Delivery) GoString() string { return d.String() }

// deliveryWire is the payload's own JSON, and the one document the phone
// decodes. The field names are pinned by the golden test beside this file
// (testdata/delivery.golden.json) and by §4.4's contract; §4.5's document is
// snake_case and so is this one.
type deliveryWire struct {
	Config DeliveryConfig     `json:"config"`
	Device deliveryDeviceWire `json:"device"`
	MACKey string             `json:"mac_key"`
}

type deliveryDeviceWire struct {
	UID   string `json:"uid"`
	Token string `json:"token"`
}

// wire renders the payload as the phone reads it: the configuration under its
// own names, and the MAC key in EncodeKey's one spelling (unpadded base64url),
// so the bytes the phone holds are the bytes the keychain holds and not a
// second encoding of them.
func (d Delivery) wire() deliveryWire {
	return deliveryWire{
		Config: d.Config,
		Device: deliveryDeviceWire{UID: d.Device.UID, Token: d.Device.Token},
		MACKey: EncodeKey(d.MACKey),
	}
}

// MarshalPayload is the JSON the phone decodes: the payload's one encoding,
// and the only path the token and the MAC key leave through. It is an explicit
// call rather than encoding/json's, because encoding/json is the redaction
// (MarshalJSON) — the accidental print is the safe one and the deliberate one
// is named.
func (d Delivery) MarshalPayload() ([]byte, error) { return json.Marshal(d.wire()) }

// MarshalJSON is the redaction, in encoding/json's shape: one string, as
// keychain.Key's is. It is deliberately *not* a redacted payload — a document
// with the payload's fields and a token that read "redacted" would be
// well formed and wrong, and the phone would store it and fail at pairing
// rather than the caller failing where they wrote json.Marshal instead of
// MarshalPayload. A string is not a payload at all, so the accident is loud at
// both ends: no field to misread, and a build that took it for one refuses it
// whole (P6.8).
func (d Delivery) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }
