package token

import (
	"bytes"
	"testing"
)

// sealKeyBytes is the external tests' sealKey, for this package's own tests: a
// KeySize-byte key the test can name, never a real one.
func sealKeyBytes(seed byte) []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// TestAADBindsTheNameAndTheVersionByte holds aad's layout, and holds that the
// version byte is *bound* and not merely checked. Unseal checks the version
// byte before it opens anything, so through the public API a version byte that
// was not in the AAD would look exactly like one that was — which is why this
// test reaches for the cipher rather than for Unseal, and builds the AAD by
// hand rather than calling aad: the literal is what aad is held to.
func TestAADBindsTheNameAndTheVersionByte(t *testing.T) {
	key := sealKeyBytes(0xb0)
	name := ObjectKey("aurora-app", "20261002-120000-ab12")
	other := ObjectKey("aurora-app", "20261002-120000-ffff")
	plain := []byte("the run's custom token")

	want := append(append([]byte(name), 0), EnvelopeVersion)
	if got := aad(name, EnvelopeVersion); !bytes.Equal(got, want) {
		t.Fatalf("aad(name, version) = %q, want the name, a zero byte and the version byte (%q)", got, want)
	}

	env, err := Seal(key, name, plain)
	if err != nil {
		t.Fatal(err)
	}
	g, err := sealAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed := env[1:1+nonceSize], env[1+nonceSize:]
	for _, tc := range []struct {
		what string
		aad  []byte
		open bool
	}{
		{"the name and version Seal used", want, true},
		// The version byte is in the AAD, so a build that changed it — or a
		// later version's envelope read by code that guessed this one — does
		// not open: the bytes are authenticated as version 1's.
		{"the name and the next version", append(append([]byte(name), 0), EnvelopeVersion+1), false},
		{"the name and no version", []byte(name), false},
		{"another name and the version", append(append([]byte(other), 0), EnvelopeVersion), false},
		// The separator is what stops a name and a version from being
		// rearranged into another pair with the same AAD.
		{"the name and version with no separator", append(append([]byte(name), EnvelopeVersion), 0), false},
	} {
		got, err := g.Open(nil, nonce, sealed, tc.aad)
		if tc.open {
			if err != nil || !bytes.Equal(got, plain) {
				t.Fatalf("%s: open = %q, %v, want the plaintext", tc.what, got, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: the envelope opened, so that AAD is not distinguished from Seal's", tc.what)
		}
	}
}
