// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The handover's sealer (portal plan P1.7, D2182 as amended): P1.0's Sealer on
// Cloud KMS's encrypt and decrypt calls, so a reveal's bundle is stored sealed
// under a key only the API's account can open and a leaked record is not a live
// key.
//
// The key is symmetric (P1.5's `google_kms_crypto_key.handover`, purpose
// ENCRYPT_DECRYPT) and it is deliberately a different ring from the signing
// one: a ring holding both would be a ring where the minter's one grant reaches
// the sealer, and the minter holds nothing an applicant typed (§3.3, Check
// 571). Nothing here signs anything, and this file has no Signer.
//
// **What travels as what.** The handover bundle is the successor's key text and
// its renewal secret (P3.2, D2182), so it is the one record in this service
// whose plaintext is a credential. It is sealed where it is written and opened
// once, 24 hours later, by the request that reveals it — and the record's own
// shape keeps the sealed bytes and nothing else (store.go's Handover). The
// ciphertext is answered and stored as bytes rather than as text here: how a
// record carries bytes is the record's encoding (docstore marshals the document
// as JSON), and a second encoding inside this one would be one more thing to
// keep in step.
//
// **The checks are the same shape as the signer's and for the same reason**
// (kmssigner.go): Cloud KMS answers a CRC32C of what it produced, so a
// ciphertext or a plaintext that arrived damaged is refused here rather than
// stored or handed back as though it were whole. A bundle opened to something
// other than what was sealed would be a wrong key mailed to a customer, and one
// stored damaged would be a customer whose reveal never opens at all.

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// SymmetricKMS is the whole of what this file needs of Cloud KMS: encrypt and
// decrypt under one symmetric key. Like AsymmetricSigner it is a seam, so the
// suite drives the sealer against a key it holds in its own process rather than
// against a service (kmsseal_test.go), and *kms.KeyManagementClient is it as it
// stands.
type SymmetricKMS interface {
	Encrypt(ctx context.Context, req *kmspb.EncryptRequest, opts ...gax.CallOption) (*kmspb.EncryptResponse, error)
	Decrypt(ctx context.Context, req *kmspb.DecryptRequest, opts ...gax.CallOption) (*kmspb.DecryptResponse, error)
}

// maxSealBytes bounds one sealed bundle. A handover is a key text and a
// secret — hundreds of bytes — and the API's own encrypt call refuses 64 KiB. A
// larger plaintext here would be a request this service should never have
// built, so it is refused before it is sent rather than read as a key that
// failed.
const maxSealBytes = 64 << 10

// KMSSealer seals and opens through one symmetric key's encrypt and decrypt
// calls. It is the Sealer a deployment reaches through Deps (api.go).
type KMSSealer struct {
	client  SymmetricKMS
	keyName string
}

// NewKMSSealer builds the sealer a deployment stores handovers with. The key
// name is a crypto key's own resource name — `…/cryptoKeys/<name>` and not a
// version of it: encrypt and decrypt are answered by the key, which picks its
// primary version, and a sealer built with nothing would fail at the first
// reveal rather than at the start.
func NewKMSSealer(client SymmetricKMS, keyName string) (*KMSSealer, error) {
	if client == nil {
		return nil, errors.New("keyservice: a KMS sealer has no client, so it can seal nothing")
	}
	name := strings.TrimSpace(keyName)
	if name == "" {
		return nil, errors.New("keyservice: a KMS sealer names no key, and a handover sealed under nothing is a plaintext key in the store")
	}
	return &KMSSealer{client: client, keyName: name}, nil
}

// Seal encrypts one handover bundle, answering the ciphertext the record
// stores.
func (s *KMSSealer) Seal(ctx context.Context, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("keyservice: sealing no bytes is not sealing a handover")
	}
	if len(plaintext) > maxSealBytes {
		return nil, fmt.Errorf("keyservice: the handover is %d bytes, over the %d a sealing call reads", len(plaintext), maxSealBytes)
	}
	sum := int64(crc32.Checksum(plaintext, crcTable))
	answer, err := s.client.Encrypt(ctx, &kmspb.EncryptRequest{
		Name:            s.keyName,
		Plaintext:       plaintext,
		PlaintextCrc32C: wrapperspb.Int64(sum),
	})
	if err != nil {
		return nil, fmt.Errorf("keyservice: the handover ring's encrypt call: %w", err)
	}
	if !answer.GetVerifiedPlaintextCrc32C() {
		return nil, fmt.Errorf("keyservice: the handover ring did not check the %d byte(s) it was asked to seal, so what it sealed is not what this service built", len(plaintext))
	}
	sealed := answer.GetCiphertext()
	if len(sealed) == 0 {
		return nil, errors.New("keyservice: the handover ring answered no ciphertext")
	}
	counted := answer.GetCiphertextCrc32C()
	if counted == nil || counted.Value != int64(crc32.Checksum(sealed, crcTable)) {
		return nil, fmt.Errorf("keyservice: the handover ring answered %d byte(s) of ciphertext whose bytes are not the ones it counted, so the seal is not the one it made", len(sealed))
	}
	return sealed, nil
}

// Open decrypts one stored handover bundle. What it answers is a credential —
// the successor key and its renewal secret — so it is reached once, by the
// reveal that hands it over, and never by a read path that lists records.
func (s *KMSSealer) Open(ctx context.Context, sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, errors.New("keyservice: there is no sealed handover to open")
	}
	sum := int64(crc32.Checksum(sealed, crcTable))
	answer, err := s.client.Decrypt(ctx, &kmspb.DecryptRequest{
		Name:             s.keyName,
		Ciphertext:       sealed,
		CiphertextCrc32C: wrapperspb.Int64(sum),
	})
	if err != nil {
		return nil, fmt.Errorf("keyservice: the handover ring's decrypt call: %w", err)
	}
	// The decrypt answer carries no "did you check what I sent" flag the way
	// the signing one does, so what is checked here is the direction Cloud KMS
	// does answer: the plaintext it produced is the one it counted. A stored
	// ciphertext that arrived damaged is refused by the ring's own
	// authentication anyway, and this is the second half — a plaintext that is
	// not what the record holds is not handed to a reveal.
	plaintext := answer.GetPlaintext()
	if len(plaintext) == 0 {
		return nil, errors.New("keyservice: the handover ring answered no plaintext, and a handover that opens to nothing is not one")
	}
	counted := answer.GetPlaintextCrc32C()
	if counted == nil || counted.Value != int64(crc32.Checksum(plaintext, crcTable)) {
		return nil, fmt.Errorf("keyservice: the handover ring answered %d byte(s) of plaintext whose bytes are not the ones it counted, so what it opened is not what was sealed", len(plaintext))
	}
	return plaintext, nil
}
