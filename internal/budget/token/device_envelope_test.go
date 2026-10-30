package token_test

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	golangtoken "go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// The delivery envelope's shape (§8): a version byte, the ephemeral public key,
// a nonce, the ciphertext and the tag. The three sizes are spelled here as the
// numbers §8 pins rather than read from the package, so a moved constant cannot
// agree with this file.
const (
	deliveryEphemeralBytes = 32
	deliveryNonceBytes     = 12
	deliveryTagBytes       = 16
)

// deliveryBase64URL is unpadded base64url's alphabet, held here so the test can
// spell a non-canonical uid without asking the package which characters it
// admits.
const deliveryBase64URL = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// deliveryPair is the phone's X25519 key pair: the phone generates it, sends the
// public half in pairing/$uid/request and never sends this one.
func deliveryPair(t *testing.T) (private, public []byte) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the phone's key pair: %v", err)
	}
	return key.Bytes(), key.PublicKey().Bytes()
}

// deliveryUID is a pairing uid of the shape pairing.go mints: PairingPrefix and
// 32 bytes in unpadded base64url (§2 (e)). The seed keeps two uids apart.
func deliveryUID(seed byte) string {
	return token.PairingPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32))
}

var (
	deliveryUIDA = deliveryUID(0x11)
	deliveryUIDB = deliveryUID(0x22)
)

// testDelivery is a delivery with values a test can look for in a print: a token
// spelled so no secret-shaped literal is needed, and a MAC key of one repeated
// byte, which is base64url of "****…" and could not be a key anyone holds.
func testDelivery() token.Delivery {
	return token.Delivery{
		Config: token.DeliveryConfig{
			ProjectID:   "owner-a",
			DatabaseURL: "https://owner-a-default-rtdb.firebaseio.com",
			APIKey:      "test-web-api-key",
		},
		Device: token.DeviceToken{
			UID:   "m~" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 16)),
			Token: "test-device-token-not-a-credential",
		},
		MACKey: bytes.Repeat([]byte{0x2a}, token.KeySize),
	}
}

func deliveryPayload(t *testing.T) []byte {
	t.Helper()
	p, err := testDelivery().MarshalPayload()
	if err != nil {
		t.Fatalf("MarshalPayload: %v", err)
	}
	return p
}

// TestEnvelopeRoundTrips: the envelope the CLI writes is the envelope the phone
// opens, and its layout is §8's — version byte, ephemeral public key, nonce,
// ciphertext, tag, each at its pinned width. The ephemeral key in the blob is
// not the phone's own key: it is the sender's, drawn for this one seal.
func TestEnvelopeRoundTrips(t *testing.T) {
	private, public := deliveryPair(t)
	payload := deliveryPayload(t)
	envelope, err := token.SealDelivery(public, deliveryUIDA, payload)
	if err != nil {
		t.Fatalf("SealDelivery: %v", err)
	}
	if envelope[0] != token.DeliveryEnvelopeVersion {
		t.Errorf("the envelope's first byte is %#x, want this build's version %#x", envelope[0], token.DeliveryEnvelopeVersion)
	}
	want := 1 + deliveryEphemeralBytes + deliveryNonceBytes + len(payload) + deliveryTagBytes
	if len(envelope) != want {
		t.Errorf("the envelope is %d bytes, want %d: %d of payload and §8's fixed parts", len(envelope), want, len(payload))
	}
	if bytes.Equal(envelope[1:1+deliveryEphemeralBytes], public) {
		t.Error("the envelope carries the phone's own public key where the ephemeral one belongs")
	}
	got, err := token.OpenDelivery(private, deliveryUIDA, envelope)
	if err != nil {
		t.Fatalf("OpenDelivery: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("the opened payload is\n%s\nwant\n%s", got, payload)
	}
}

// TestEnvelopeBindsThePairingUID is the test this envelope's AAD exists for
// (§8): the uid is bound, so a payload sealed for one pairing's node cannot be
// moved to another's and opened there.
func TestEnvelopeBindsThePairingUID(t *testing.T) {
	private, public := deliveryPair(t)
	envelope, err := token.SealDelivery(public, deliveryUIDA, deliveryPayload(t))
	if err != nil {
		t.Fatalf("SealDelivery: %v", err)
	}
	if _, err := token.OpenDelivery(private, deliveryUIDA, envelope); err != nil {
		t.Fatalf("the envelope did not open under the pairing it was sealed for: %v", err)
	}
	if _, err := token.OpenDelivery(private, deliveryUIDB, envelope); err == nil {
		t.Fatalf("a payload sealed for %s opened under %s: the AAD does not bind the pairing uid (§8)", deliveryUIDA, deliveryUIDB)
	}
}

// TestEnvelopeBindsThePhoneKey: a payload sealed for one phone's key is not
// openable by another phone, even one this installation would admit. The refusal
// covers both of §8's bindings at once — ECDH's secret differs and the AAD's 32
// key bytes differ — and both are refusals, which is what the phone's half needs.
func TestEnvelopeBindsThePhoneKey(t *testing.T) {
	privateA, publicA := deliveryPair(t)
	privateB, publicB := deliveryPair(t)
	if bytes.Equal(publicA, publicB) {
		t.Fatal("two generated key pairs are the same key")
	}
	envelope, err := token.SealDelivery(publicA, deliveryUIDA, deliveryPayload(t))
	if err != nil {
		t.Fatalf("SealDelivery: %v", err)
	}
	if _, err := token.OpenDelivery(privateA, deliveryUIDA, envelope); err != nil {
		t.Fatalf("the envelope did not open under the phone it was sealed for: %v", err)
	}
	if _, err := token.OpenDelivery(privateB, deliveryUIDA, envelope); err == nil {
		t.Fatal("a payload sealed for one phone's key opened under another phone's: the AAD does not bind the phone's key (§8)")
	}
}

// TestEnvelopeRefusesAnotherVersionByte: the version byte is checked before
// anything is opened, for seal.go's reason — a later format is refused rather
// than misread as this one — and an envelope too short to hold §8's parts is not
// this format either.
func TestEnvelopeRefusesAnotherVersionByte(t *testing.T) {
	private, public := deliveryPair(t)
	envelope, err := token.SealDelivery(public, deliveryUIDA, deliveryPayload(t))
	if err != nil {
		t.Fatalf("SealDelivery: %v", err)
	}
	for _, version := range []byte{0, 2, 0xff} {
		mutated := bytes.Clone(envelope)
		mutated[0] = version
		if _, err := token.OpenDelivery(private, deliveryUIDA, mutated); !errors.Is(err, token.ErrDeliveryNotOpenable) {
			t.Errorf("an envelope whose first byte is %#x = %v, want ErrDeliveryNotOpenable", version, err)
		}
	}
	if _, err := token.OpenDelivery(private, deliveryUIDA, envelope[:deliveryEphemeralBytes]); !errors.Is(err, token.ErrDeliveryNotOpenable) {
		t.Errorf("an envelope of %d bytes = %v, want ErrDeliveryNotOpenable", deliveryEphemeralBytes, err)
	}
}

// deliveryKeysWithAZeroByte finds two phone keys whose 32 bytes each hold a
// 0x00, and whose zero bytes are *not* at the same position, from a fixed
// sequence of seeds so the test picks the same keys everywhere. §8's point is
// that this is ordinary — roughly one in eight public keys does — and that a
// zero inside the key is positional data and not a separator.
func deliveryKeysWithAZeroByte(t *testing.T) (first, second [2][]byte) {
	t.Helper()
	zero := -1
	for i := range 1 << 16 {
		seed := sha256.Sum256(fmt.Appendf(nil, "fugaro-pairing-test-seed/%d", i))
		key, err := ecdh.X25519().NewPrivateKey(seed[:])
		if err != nil {
			t.Fatalf("NewPrivateKey: %v", err)
		}
		public := key.PublicKey().Bytes()
		at := bytes.IndexByte(public, 0)
		switch {
		case at < 0:
		case zero < 0:
			first, zero = [2][]byte{key.Bytes(), public}, at
		case at != zero:
			return first, [2][]byte{key.Bytes(), public}
		}
	}
	t.Fatal("no two X25519 public keys with a zero byte at different positions among 65536 seeds")
	return first, second
}

// TestEnvelopeKeyWithAZeroByte: a key containing 0x00 still round-trips and is
// still bound. The AAD's first zero byte terminates the uid — a uid cannot hold
// one — and every byte after it belongs to the key, at a fixed width and in a
// fixed order, so a zero inside the key is data and nothing else. An
// implementation that dropped a separator, or that truncated the key at its
// first zero, fails here.
func TestEnvelopeKeyWithAZeroByte(t *testing.T) {
	first, second := deliveryKeysWithAZeroByte(t)
	private, public := first[0], first[1]
	otherPrivate, otherPublic := second[0], second[1]
	zero := bytes.IndexByte(public, 0)
	if zero < 0 || bytes.IndexByte(otherPublic, 0) == zero {
		t.Fatalf("the chosen keys have no zero byte at different positions (%d and %d), so this test asserts nothing", zero, bytes.IndexByte(otherPublic, 0))
	}
	payload := deliveryPayload(t)
	envelope, err := token.SealDelivery(public, deliveryUIDA, payload)
	if err != nil {
		t.Fatalf("SealDelivery with a key whose byte %d is zero: %v", zero, err)
	}
	got, err := token.OpenDelivery(private, deliveryUIDA, envelope)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("OpenDelivery = %s, %v, want the payload back", got, err)
	}
	if _, err := token.OpenDelivery(otherPrivate, deliveryUIDA, envelope); err == nil {
		t.Fatal("a payload sealed for one key opened under another that also holds a zero byte: the key's bytes are not bound as §8 pins them")
	}
}

// TestEnvelopeRefusesLowOrderKey: a 32-byte value that is a point of small order
// is not a phone's key, and crypto/ecdh refuses to combine with it because the
// agreed secret would be all zeros. That refusal is this package's, and never a
// fallback to sealing under a secret anyone could compute.
func TestEnvelopeRefusesLowOrderKey(t *testing.T) {
	low := map[string][]byte{
		"the all-zero key":       make([]byte, token.DeviceKeySize),
		"the point of order one": append([]byte{0x01}, make([]byte, token.DeviceKeySize-1)...),
	}
	for name, public := range low {
		envelope, err := token.SealDelivery(public, deliveryUIDA, deliveryPayload(t))
		if !errors.Is(err, token.ErrDeviceKey) || envelope != nil {
			t.Errorf("%s: SealDelivery = %x, %v, want ErrDeviceKey and no envelope", name, envelope, err)
		}
	}
}

// TestSealDeliveryRefusesABadUIDOrKeySize: the two inputs §8's AAD cannot be
// built from. A uid that is not a p~ pairing is refused rather than bound; a key
// that is not 32 bytes is refused rather than padded, truncated or re-encoded —
// the AAD's key field is fixed-width, so a short key would silently shift the
// version byte.
func TestSealDeliveryRefusesABadUIDOrKeySize(t *testing.T) {
	_, public := deliveryPair(t)
	payload := deliveryPayload(t)
	// The last character of a 32-byte body carries two bits base64url discards,
	// so three other characters decode to the same uid. §2 (e) pins one
	// spelling and this refuses the rest, as DecodeDeviceKey and ParseKey do.
	last := deliveryUIDA[len(deliveryUIDA)-1]
	at := strings.IndexByte(deliveryBase64URL, last)
	if at < 0 || at%4 != 0 {
		t.Fatalf("the uid's last character %q is not a canonical one, so this test asserts nothing", last)
	}
	nonCanonical := deliveryUIDA[:len(deliveryUIDA)-1] + string(deliveryBase64URL[at+1])
	for _, uid := range []string{
		"", "m~" + deliveryUIDA[2:], "r~run", deliveryUIDA[:len(deliveryUIDA)-1],
		deliveryUIDA + "=", "p~" + strings.Repeat("a", 128),
		"p~" + deliveryUIDA[2:len(deliveryUIDA)-1] + ".", nonCanonical,
	} {
		if envelope, err := token.SealDelivery(public, uid, payload); !errors.Is(err, token.ErrPairingUID) || envelope != nil {
			t.Errorf("SealDelivery under %q = %x, %v, want ErrPairingUID and no envelope", uid, envelope, err)
		}
	}
	for _, size := range []int{0, token.DeviceKeySize - 1, token.DeviceKeySize + 1} {
		if envelope, err := token.SealDelivery(public[:size], deliveryUIDA, payload); !errors.Is(err, token.ErrDeviceKey) || envelope != nil {
			t.Errorf("SealDelivery to a %d-byte key = %x, %v, want ErrDeviceKey and no envelope", size, envelope, err)
		}
	}
}

// TestEnvelopeFreshEphemeralEachSeal: a fresh ephemeral key and a fresh nonce for
// every seal (M6, as D17 makes the seal key fresh per launch). Two seals of one
// payload under one pairing are different bytes with different ephemeral keys,
// and the ephemeral private key is drawn inside SealDelivery and never leaves it
// — nothing returned here is that key.
func TestEnvelopeFreshEphemeralEachSeal(t *testing.T) {
	private, public := deliveryPair(t)
	payload := deliveryPayload(t)
	seen := map[string]bool{}
	ephemeral := map[string]bool{}
	for range 32 {
		envelope, err := token.SealDelivery(public, deliveryUIDA, payload)
		if err != nil {
			t.Fatalf("SealDelivery: %v", err)
		}
		if seen[string(envelope)] {
			t.Fatal("two seals of the same payload are the same bytes: the nonce or the ephemeral key is not fresh")
		}
		seen[string(envelope)] = true
		ephemeral[string(envelope[1:1+deliveryEphemeralBytes])] = true
		if _, err := token.OpenDelivery(private, deliveryUIDA, envelope); err != nil {
			t.Fatalf("OpenDelivery: %v", err)
		}
	}
	if len(ephemeral) != 32 {
		t.Fatalf("%d distinct ephemeral keys over 32 seals: they repeat", len(ephemeral))
	}
}

// TestNewMACKeyIsAFreshKeyOfTheSealsSize: M5's MAC key is 32 bytes from
// crypto/rand, drawn and never derived, carried to the phone only inside the
// envelope, and held to seal.go's EncodeKey/ParseKey discipline because it is
// the same kind of value. A draw that cannot be filled is an error and never a
// key.
func TestNewMACKeyIsAFreshKeyOfTheSealsSize(t *testing.T) {
	seen := map[string]bool{}
	for range 16 {
		key, err := token.NewMACKey()
		if err != nil {
			t.Fatal(err)
		}
		if len(key) != token.KeySize {
			t.Fatalf("NewMACKey is %d bytes, want %d", len(key), token.KeySize)
		}
		if bytes.Equal(key, make([]byte, token.KeySize)) {
			t.Fatal("NewMACKey returned the zero key")
		}
		if seen[string(key)] {
			t.Fatal("NewMACKey returned the same key twice")
		}
		seen[string(key)] = true
		back, err := token.ParseKey(token.EncodeKey(key))
		if err != nil || !bytes.Equal(back, key) {
			t.Fatalf("ParseKey(EncodeKey(key)) = %v, %v", back, err)
		}
	}
	defer token.SetKeyReader(&shortReader{src: make([]byte, token.KeySize/2)})()
	key, err := token.NewMACKey()
	if err == nil || key != nil {
		t.Fatalf("NewMACKey from a source that runs out = %x, %v, want an error and no key", key, err)
	}
}

// TestDeliveryRedactsTokenAndMACKey: no printer prints the device token or the
// MAC key (M5, §4.5), and encoding/json is a printer here — a Delivery that
// reaches a --json document by accident, one careless struct field away, must
// print neither. The MAC key's spellings are checked as well as its bytes,
// because a redacted Delivery that printed %x of the key would leak a different
// encoding of the same secret.
func TestDeliveryRedactsTokenAndMACKey(t *testing.T) {
	d := testDelivery()
	keyForms := []string{
		string(d.MACKey),
		hex.EncodeToString(d.MACKey),
		base64.RawURLEncoding.EncodeToString(d.MACKey),
		base64.StdEncoding.EncodeToString(d.MACKey),
		fmt.Sprintf("%x", d.MACKey),
		fmt.Sprint(d.MACKey),
	}
	printed := map[string]string{
		"%v":        fmt.Sprintf("%v", d),
		"%+v":       fmt.Sprintf("%+v", d),
		"%#v":       fmt.Sprintf("%#v", d),
		"%s":        fmt.Sprintf("%s", d),
		"%q":        fmt.Sprintf("%q", d),
		"%x":        fmt.Sprintf("%x", d),
		"a pointer": fmt.Sprintf("%+v", &d),
		"Sprint":    fmt.Sprint(d),
	}
	wrapper, err := json.Marshal(struct {
		Delivery token.Delivery `json:"delivery"`
	}{d})
	if err != nil {
		t.Fatal(err)
	}
	printed["a wrapper's JSON"] = string(wrapper)
	value, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	printed["encoding/json"] = string(value)

	for where, text := range printed {
		if strings.Contains(text, d.Device.Token) {
			t.Errorf("%s printed the device token: %s", where, text)
		}
		for _, form := range keyForms {
			if form != "" && strings.Contains(text, form) {
				t.Errorf("%s printed the MAC key: %s", where, text)
			}
		}
	}
	if !strings.Contains(printed["%v"], d.Device.UID) {
		t.Errorf("the redacted print names no device: %s", printed["%v"])
	}
}

// TestDeliveryGolden pins the payload's JSON: the document the phone decodes
// (P6.8), with the field names §4.4's contract fixes. It is read back here under
// a second spelling of those names, so a rename in the wire type cannot carry a
// golden along with it. The redacted document, which is what encoding/json
// writes, is asserted to be a different one: a caller who reaches for
// json.Marshal gets a diagnostic, not a pairing.
func TestDeliveryGolden(t *testing.T) {
	d := testDelivery()
	got, err := d.MarshalPayload()
	if err != nil {
		t.Fatalf("MarshalPayload: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "delivery.golden.json"))
	if err != nil {
		t.Fatalf("reading the golden: %v", err)
	}
	if !bytes.Equal(got, bytes.TrimRight(want, "\n")) {
		t.Fatalf("MarshalPayload =\n%s\nwant the golden\n%s", got, want)
	}
	var parsed struct {
		Config struct {
			ProjectID   string `json:"project_id"`
			DatabaseURL string `json:"database_url"`
			APIKey      string `json:"api_key"`
		} `json:"config"`
		Device struct {
			UID   string `json:"uid"`
			Token string `json:"token"`
		} `json:"device"`
		MACKey string `json:"mac_key"`
	}
	if err := json.Unmarshal(want, &parsed); err != nil {
		t.Fatalf("the golden is not a JSON document the phone could read: %v", err)
	}
	switch {
	case parsed.Config.ProjectID != d.Config.ProjectID:
		t.Errorf("the golden's project_id is %q, want %q", parsed.Config.ProjectID, d.Config.ProjectID)
	case parsed.Config.DatabaseURL != d.Config.DatabaseURL:
		t.Errorf("the golden's database_url is %q, want %q", parsed.Config.DatabaseURL, d.Config.DatabaseURL)
	case parsed.Config.APIKey != d.Config.APIKey:
		t.Errorf("the golden's api_key is %q, want %q", parsed.Config.APIKey, d.Config.APIKey)
	case parsed.Device.UID != d.Device.UID:
		t.Errorf("the golden's device.uid is %q, want %q", parsed.Device.UID, d.Device.UID)
	case parsed.Device.Token != d.Device.Token:
		t.Errorf("the golden's device.token is %q, want %q", parsed.Device.Token, d.Device.Token)
	case parsed.MACKey != token.EncodeKey(d.MACKey):
		t.Errorf("the golden's mac_key is %q, want the key in EncodeKey's spelling %q", parsed.MACKey, token.EncodeKey(d.MACKey))
	}
	redacted, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(redacted, got) {
		t.Error("encoding/json wrote the payload: the redaction and the payload are the same document")
	}
}

// TestGoModGainsNoModule reads §8's claim off the tree instead of trusting it:
// device.go's imports are all standard library — crypto/ecdh and crypto/hkdf
// among them, and neither may go missing — and go.mod still requires
// golang.org/x/crypto as an indirect module only (M6). The read is of the file
// this package ships rather than of a recorded list, so it cannot go stale
// silently, and it needs no toolchain: nothing here runs go list or the module
// graph.
func TestGoModGainsNoModule(t *testing.T) {
	file, err := parser.ParseFile(golangtoken.NewFileSet(), "device.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing device.go: %v", err)
	}
	imports := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("reading the import %s: %v", spec.Path.Value, err)
		}
		imports[path] = true
		if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
			t.Errorf("device.go imports %s, which is not the standard library: the delivery's key agreement must add no module (§8, M6)", path)
		}
	}
	for _, want := range []string{"crypto/ecdh", "crypto/hkdf"} {
		if !imports[want] {
			t.Errorf("device.go does not import %s, so this is not the envelope §8 pins", want)
		}
	}
	gomod, err := os.ReadFile(filepath.Join("..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	for _, line := range strings.Split(string(gomod), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "golang.org/x/crypto ") && !strings.Contains(line, "// indirect") {
			t.Errorf("go.mod requires golang.org/x/crypto directly: %q", strings.TrimSpace(line))
		}
	}
}
