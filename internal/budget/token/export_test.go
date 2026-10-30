package token

import (
	"crypto/cipher"
	"crypto/ecdh"
	"errors"
	"fmt"
	"io"
)

// SetKeyReader replaces the reader NewKey draws from, for the test's
// duration; restore puts crypto/rand back.
func SetKeyReader(r io.Reader) (restore func()) {
	prev := keyReader
	keyReader = r
	return func() { keyReader = prev }
}

// SetDeviceUIDReader does the same for the device uid deviceNewUID draws: a
// uid is a device's name and a claim, so a test has to be able to hold the
// source still — and to hand it one that cannot fill the buffer.
func SetDeviceUIDReader(r io.Reader) (restore func()) {
	prev := deviceUIDReader
	deviceUIDReader = r
	return func() { deviceUIDReader = prev }
}

// ErrDeliveryNotOpenable is OpenDelivery's refusal: the envelope did not open,
// and the error never says which part of the AAD or which key was wrong,
// because a refusal that guesses is a refusal that tells a guesser.
var ErrDeliveryNotOpenable = errors.New("budget token: the delivery envelope did not open")

// OpenDelivery is the phone's half of §8's envelope, and it is here rather than
// in device.go because **the phone opens in production** (P6.8, the app): the
// CLI's package only ever seals. It takes the phone's X25519 private key — the
// phone generates the pair and never sends this half — and the pairing uid the
// delivery node named, and it refuses everything §4.4 lists: an envelope too
// short to be one, a version byte this build does not write, a key that is not
// a point X25519 will combine with, and an AAD that does not name this uid and
// this public key. The public key it binds is the one derived from the private
// key it was handed, which is exactly what the sender bound: a payload sealed
// for another phone cannot be opened here.
func OpenDelivery(phonePrivate []byte, uid string, envelope []byte) ([]byte, error) {
	if err := checkPairingUID(uid); err != nil {
		return nil, err
	}
	if len(envelope) < deliveryMinEnvelope {
		return nil, fmt.Errorf("%w: %d bytes is shorter than the %d an envelope is", ErrDeliveryNotOpenable, len(envelope), deliveryMinEnvelope)
	}
	if envelope[0] != DeliveryEnvelopeVersion {
		return nil, fmt.Errorf("%w: the first byte is %#x and this build writes %#x", ErrDeliveryNotOpenable, envelope[0], DeliveryEnvelopeVersion)
	}
	priv, err := ecdh.X25519().NewPrivateKey(phonePrivate)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceKey, err)
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(envelope[1 : 1+DeviceKeySize])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceKey, err)
	}
	shared, err := priv.ECDH(ephemeral)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeviceKey, err)
	}
	g, err := deliveryAEAD(shared)
	if err != nil {
		return nil, err
	}
	return openDeliveryAEAD(g, priv.PublicKey().Bytes(), uid, envelope[0], envelope)
}

// openDeliveryAEAD is the tag check and nothing else, split out so the caller
// above reads as §8's list of refusals rather than as one long expression.
func openDeliveryAEAD(g cipher.AEAD, phonePub []byte, uid string, version byte, envelope []byte) ([]byte, error) {
	body := envelope[1+DeviceKeySize:]
	plain, err := g.Open(nil, body[:nonceSize], body[nonceSize:], deliveryAAD(uid, phonePub, version))
	if err != nil {
		return nil, fmt.Errorf("%w: the tag did not verify, so this is not a delivery sealed for this phone under %s: it was tampered with, sealed for another pairing, or sealed for another key", ErrDeliveryNotOpenable, uid)
	}
	return plain, nil
}
