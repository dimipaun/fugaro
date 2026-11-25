// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The KMS signer (portal plan P1.7): the minter's Signer on Cloud KMS's
// asymmetric-sign call, so the private half of a licence key never leaves the
// managed store and no file, fixture or process here holds one
// (.claude/rules/ee.md R5, design docs/key-signing-service/spec.md §3.3,
// S2.1's Ed25519).
//
// This is the deployment's only Signer, and the minter's program is the only
// one that builds it: the API's program has no signer of its own and reaches
// the signature through the minter's service (Check 174, minterclient.go). What
// makes that true of the code rather than of a comment is that the package's
// one caller of Sign is minter.go, and TestOnlyTheMinterCallsTheSigner holds
// it.
//
// **Why the call is checked and not just made.** A signature is what turns a
// claim set into a key every released verifier accepts, so a management API
// answer that arrived damaged or in part would be a key nobody can use, minted
// and mailed as though it worked. Cloud KMS answers a CRC32C of the signature
// beside it and checks one sent with the payload, so both directions are
// compared here: the bytes signed are the ones this service built, and the
// signature answered is the one the key ring made. Neither check is a
// cryptographic one — the signature itself is verified by whoever reads the key
// — it is what tells a truncated answer from a whole one before the key is
// assembled (kmssigner_test.go).

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

// AsymmetricSigner is the whole of what this file needs of Cloud KMS: sign
// bytes with one asymmetric key version. A seam this narrow is what lets the
// suite hold the signing to a key ring generated in the test's own process
// rather than to a service (kmssigner_test.go), and *kms.KeyManagementClient is
// it as it stands.
type AsymmetricSigner interface {
	AsymmetricSign(ctx context.Context, req *kmspb.AsymmetricSignRequest, opts ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error)
}

// maxSignBytes bounds one payload. A claim set is a small JSON document and the
// management API refuses a large one itself; the bound is here so that a
// request this service should never have built is refused before it is sent
// rather than read as a key ring that failed.
const maxSignBytes = 64 << 10

// crcTable is the checksum Cloud KMS counts in, and it is Castagnoli's and not
// the IEEE one: the field is named crc32c on both sides of the call.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// KMSSigner signs through one asymmetric key version's own signing API.
type KMSSigner struct {
	client     AsymmetricSigner
	keyVersion string
}

// NewKMSSigner builds the signer a deployment mints through.
//
// The key version is required and is not a default: an asymmetric key has no
// primary version for the API to fall back to, so a signer built with none
// would sign nothing at all — and the failure would arrive at the first mint
// rather than at the start, which is the shape S3.7's deploy is most expensive
// in.
func NewKMSSigner(client AsymmetricSigner, keyVersion string) (*KMSSigner, error) {
	if client == nil {
		return nil, errors.New("keyservice: a KMS signer has no client, so it can sign nothing")
	}
	version := strings.TrimSpace(keyVersion)
	if version == "" {
		return nil, errors.New("keyservice: a KMS signer names no key version, and an asymmetric key has no primary version to fall back to")
	}
	return &KMSSigner{client: client, keyVersion: version}, nil
}

// Sign hands the payload to the key version's signing API and answers the
// signature it made.
//
// The payload travels as the request's `data` and not as a digest, because the
// key this service mints with is Ed25519 (S2.1) and a managed Ed25519 key signs
// the payload itself: a digest would be signed as opaque bytes, and what
// answers would be a signature no verifier of this format accepts.
func (s *KMSSigner) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("keyservice: a signature over no bytes is one every empty payload shares, so there is no key to mint from it")
	}
	if len(payload) > maxSignBytes {
		return nil, fmt.Errorf("keyservice: the payload is %d bytes, over the %d a signing call reads", len(payload), maxSignBytes)
	}
	sum := int64(crc32.Checksum(payload, crcTable))
	answer, err := s.client.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{
		Name:       s.keyVersion,
		Data:       payload,
		DataCrc32C: wrapperspb.Int64(sum),
	})
	if err != nil {
		return nil, fmt.Errorf("keyservice: the key ring's signing API: %w", err)
	}
	if !answer.GetVerifiedDataCrc32C() {
		return nil, fmt.Errorf("keyservice: the key ring did not check the %d byte(s) it was asked to sign, so what it signed is not what this service built", len(payload))
	}
	// The answer names the key version that signed, and it is compared rather
	// than read: a signature under a version this service did not ask for is a
	// key that verifies nowhere, and the name is the one thing in the answer
	// that says which half of the ring made it.
	if signed := answer.GetName(); signed != "" && signed != s.keyVersion {
		return nil, fmt.Errorf("keyservice: the key ring signed with %s, and this service mints under %s", signed, s.keyVersion)
	}
	signature := answer.GetSignature()
	if len(signature) == 0 {
		return nil, errors.New("keyservice: the key ring answered an empty signature")
	}
	counted := answer.GetSignatureCrc32C()
	if counted == nil || counted.Value != int64(crc32.Checksum(signature, crcTable)) {
		return nil, fmt.Errorf("keyservice: the key ring answered a %d-byte signature whose bytes are not the ones it counted, so the signature is not the one it made", len(signature))
	}
	return signature, nil
}
