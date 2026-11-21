// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// The identity service's program (mobile identity plan task A1).
//
// It builds the four seams a deployment has and nothing else: the ID token
// verifier over Google's published Firebase keys, the KMS signing key, the
// served key set read from the bucket only the owner writes, and the assertion
// route over them. There is no store, no credential and no admin path in this
// process, because the whole of what it is for is turning one verified sign-in
// into one project-bound assertion (A1's acceptance).
//
// **What it does not have is enforced, not promised.** The configuration is
// read from an environment this program names in full: a `FUGARO_` variable it
// does not read is a refusal at startup, so a deployment that reused another
// service's environment here could not start (the minter's shape,
// ee/keyservice/cmd/minter). Nothing it reads is a secret: the project id, a
// key id, a key version's resource name, and the URI of a document that is
// public where nothing of it is private.
//
// **The signing key is never here.** The KMS key version is named and reached
// with the container's own service account (A3's IAM), and no file, fixture or
// environment in this process holds a private half (.claude/rules/ee.md R5).

package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"hash/crc32"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/ee/identitysvc"
	"github.com/dimipaun/fugaro/internal/blobx"
)

// defaultPort is the platform's own: Cloud Run sets PORT and routes to it, and
// a container that listened elsewhere would be one nobody can reach.
const defaultPort = "8080"

// shutdownGrace is how long a request in flight has to finish once the platform
// asks the container to stop. An assertion is one signing call and a JSON
// answer, so the grace is short and it is here so a deploy does not cut a
// signature off after the key ring made it.
const shutdownGrace = 20 * time.Second

// maxKeySetBytes bounds the served key-set document this program reads. It is
// the same bound the parser holds, and it is here as well so the read is capped
// before the bytes are in memory.
const maxKeySetBytes = 64 << 10

// The variables this program reads, and no others. They are not secrets (the
// project id, a key id, a key version's name, a bucket URI), so a mistake in a
// deploy script is a name that does not resolve rather than a credential
// somewhere it should not be.
const (
	varProject    = "FUGARO_IDENTITY_PROJECT"
	varKeyID      = "FUGARO_IDENTITY_KEY_ID"
	varKeyVersion = "FUGARO_IDENTITY_KEY_VERSION"
	varKeySet     = "FUGARO_IDENTITY_KEYSET"

	// deployVarPrefix is the namespace a variable has to be in to be this
	// program's business at all. Anything outside it (PATH, PORT, the
	// platform's own) is left alone.
	deployVarPrefix = "FUGARO_"
)

// config is the deployment this program runs as.
type config struct {
	// Project is the identity project's own Firebase project id: the bare id a
	// token's `aud` carries and the one whose securetoken issuer signs.
	Project string
	// KeyID is the `kid` of the KMS signing key version below. The owner's key
	// ceremony pairs the two, and the served key set carries the id (Check 737).
	KeyID string
	// KeyVersion is the KMS key version's resource name.
	KeyVersion string
	// KeySetURL is where the served, root-signed key set lives: a bucket URI
	// (gs://bucket/object), the object only the owner writes and this service
	// reads (A3, A4).
	KeySetURL string
}

func main() {
	if err := run(); err != nil {
		// The message goes to the platform's log and names what failed rather
		// than a stack: this program is only ever started by a deploy, and the
		// operator reading the log is the one who wrote the variables.
		log.Fatalf("identitysvc: %v", err)
	}
}

func run() error {
	cfg, err := loadConfig(os.Environ())
	if err != nil {
		return err
	}
	ctx := context.Background()
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return fmt.Errorf("the Cloud KMS client: %w", err)
	}
	defer client.Close()
	signer, err := newKMSSigner(client, cfg.KeyVersion)
	if err != nil {
		return err
	}
	set, err := newBucketKeySet(ctx, cfg.KeySetURL)
	if err != nil {
		return err
	}
	// The verifier reads Google's published key set for the securetoken signer,
	// which is one global set for every Firebase project in the world: the
	// project is told from another's by the `aud`/`iss` pair and not by a key.
	verifier, err := identitysvc.NewIDTokenVerifier(cfg.Project, firebaseKeys{mcpauth.NewSecureTokenKeys()})
	if err != nil {
		return err
	}
	mint, err := identitysvc.NewSigner(identitysvc.SignerConfig{
		Issuer: identitysvc.IssuerPrefix + cfg.Project,
		KeyID:  cfg.KeyID,
		KMS:    signer,
		Set:    set,
		Tokens: verifier,
	})
	if err != nil {
		return err
	}
	return serve(identitysvc.NewHandler(mint).Mux())
}

// loadConfig reads this program's environment.
//
// Every variable this program reads must be set and non-empty, and every
// `FUGARO_` variable it does not read is refused: a name spelled wrongly, or one
// left over from another service's deployment, would otherwise run as a default
// nobody chose — and for this service a defaulted key id or project id would be
// an assertion signed for a project nobody named.
func loadConfig(environ []string) (config, error) {
	read := map[string]string{}
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			return config{}, fmt.Errorf("the environment holds %q, which is not a NAME=value pair", entry)
		}
		if !strings.HasPrefix(name, deployVarPrefix) {
			continue
		}
		read[name] = value
	}
	value := func(name string) (string, error) {
		value, ok := read[name]
		if !ok {
			return "", fmt.Errorf("the environment does not set %s, and this service cannot start without it", name)
		}
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("the environment sets %s to nothing, and a variable this service reads has no empty meaning", name)
		}
		delete(read, name)
		return strings.TrimSpace(value), nil
	}
	cfg := config{}
	for _, into := range []struct {
		name string
		to   *string
	}{
		{varProject, &cfg.Project},
		{varKeyID, &cfg.KeyID},
		{varKeyVersion, &cfg.KeyVersion},
		{varKeySet, &cfg.KeySetURL},
	} {
		got, err := value(into.name)
		if err != nil {
			return config{}, err
		}
		*into.to = got
	}
	for name := range read {
		return config{}, fmt.Errorf("the environment sets %s, which this program does not read: a variable spelled wrongly or set for another service would otherwise run as a default nobody chose", name)
	}
	// The location is read here as well as at startup so that a variable that is
	// not a bucket URI at all is named as a configuration mistake rather than
	// reported later as a bucket that could not be opened.
	if _, _, err := splitBucketURL(cfg.KeySetURL); err != nil {
		return config{}, fmt.Errorf("%s: %w", varKeySet, err)
	}
	return cfg, nil
}

// firebaseKeys is the ID token verifier's key set over Google's published
// JWKS.
//
// It translates one sentinel and only one: mcpauth's ErrUnknownKeyID — a token
// whose key the set does not hold, which is a refusal of that token — becomes
// this package's, so the verifier tells a rotation from an outage. Everything
// else, mcpauth's ErrUnavailable included, travels as itself: a key endpoint
// that could not be read is this service's fault and answers 503 rather than
// anybody's bad sign-in.
type firebaseKeys struct {
	keys *mcpauth.SecureTokenKeys
}

func (f firebaseKeys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	key, err := f.keys.Key(ctx, kid)
	if err != nil && errors.Is(err, mcpauth.ErrUnknownKeyID) {
		return nil, fmt.Errorf("%w: %w", identitysvc.ErrUnknownKeyID, err)
	}
	return key, err
}

// bucketKeySet is the served, root-signed key set as the deployment holds it:
// the document A4's signing command wrote and A2 serves, read from the object
// only the owner writes (O7). It is read per request rather than cached: the
// document is small, a read is one round trip, and a signer deciding whether it
// may sign is the last place to hold an answer that has since been replaced.
type bucketKeySet struct {
	bucket *blobx.Bucket
	key    string
}

// newBucketKeySet opens the bucket once, at startup, so a bucket this service
// cannot reach is a refusal to start rather than the first person's sign-in.
func newBucketKeySet(ctx context.Context, rawURL string) (*bucketKeySet, error) {
	bucketURL, key, err := splitBucketURL(rawURL)
	if err != nil {
		return nil, err
	}
	bucket, err := blobx.Open(ctx, bucketURL)
	if err != nil {
		return nil, fmt.Errorf("the bucket the served key set lives in: %w", err)
	}
	return &bucketKeySet{bucket: bucket, key: key}, nil
}

// splitBucketURL reads the one variable that names the served key set: a bucket
// URI of the form gs://bucket/object, split into the bucket to open and the
// object to read. The scheme is kept and not assumed to be `gs` — a local run
// reads a file:// or mem:// location, which is how this program is exercised
// without a cloud — and the object is required, because a location with no
// object is a bucket whose key set nobody named.
func splitBucketURL(rawURL string) (bucketURL, key string, err error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", errors.New("the served key set's location is not a bucket URI such as gs://bucket/object")
	}
	key = strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return "", "", errors.New("the served key set's location names no object in the bucket")
	}
	return u.Scheme + "://" + u.Host, key, nil
}

// Current reads the served document and answers the two things the signer needs
// from it. The document's own signature is not verified here: what signed it and
// whether its `seq` is above the floor is the publisher's and the project
// verifier's (A2, P1), and the signer reads only whether the key it holds is
// published and whether the publication is still in its life.
func (s *bucketKeySet) Current(ctx context.Context) (identitysvc.ServedSet, error) {
	doc, _, err := s.bucket.ReadMaxStrict(ctx, s.key, maxKeySetBytes)
	if err != nil {
		return identitysvc.ServedSet{}, fmt.Errorf("reading the served key set: %w", err)
	}
	return identitysvc.ParseServedSet(doc)
}

// AsymmetricSigner is the whole of what this program needs of Cloud KMS: sign
// bytes with one asymmetric key version. It is *kms.KeyManagementClient as it
// stands, and like every other seam here it is an interface so that the test
// that builds a program's worth of configuration does not need a key ring.
type AsymmetricSigner interface {
	AsymmetricSign(ctx context.Context, req *kmspb.AsymmetricSignRequest, opts ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error)
}

// crcTable is the checksum Cloud KMS counts in: Castagnoli's, not the IEEE one,
// because the field is named crc32c on both sides of the call.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// kmsSigner signs an assertion through one asymmetric key version's own signing
// API, so the private half never leaves the managed store and no file, fixture
// or environment here holds one (.claude/rules/ee.md R5).
//
// The pattern is ee/keyservice/kmssigner.go's, copied rather than imported: that
// signer belongs to the minter, and its file and its test say the minter's
// program is the only one that builds it. The checks are the same ones and for
// the same reason — a signature is what turns claims into an assertion a project
// accepts, so an answer that arrived damaged or in part would be one nobody can
// verify, minted as though it worked. Cloud KMS answers a CRC32C of the
// signature beside it and checks one sent with the payload, so both directions
// are compared: the bytes signed are the ones this service built, and the
// signature answered is the one the key ring made. Neither is a cryptographic
// check — the project's verifier is that — but they are what tells a truncated
// answer from a whole one before an assertion is assembled around it.
type kmsSigner struct {
	client     AsymmetricSigner
	keyVersion string
}

// newKMSSigner builds the signer this deployment signs through.
//
// The key version is required and is not a default: an asymmetric key has no
// primary version to fall back to, so a signer built with none would sign nothing
// at all — and the failure would arrive at the first person's sign-in rather than
// at the start, which is the shape a deploy is most expensive in.
func newKMSSigner(client AsymmetricSigner, keyVersion string) (*kmsSigner, error) {
	if client == nil {
		return nil, errors.New("a KMS signer has no client, so it can sign nothing")
	}
	version := strings.TrimSpace(keyVersion)
	if version == "" {
		return nil, errors.New("a KMS signer names no key version, and an asymmetric key has no primary version to fall back to")
	}
	return &kmsSigner{client: client, keyVersion: version}, nil
}

// Sign hands the payload to the key version's signing API and answers the
// signature it made.
//
// The payload travels as the request's `data` and not as a digest: the signing
// key is Ed25519 (A3), and a managed Ed25519 key signs the payload itself — a
// digest would be signed as opaque bytes and what came back would verify
// nowhere.
func (s *kmsSigner) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("a signature over no bytes is one every empty payload shares, so there is no assertion to mint from it")
	}
	sum := int64(crc32.Checksum(payload, crcTable))
	answer, err := s.client.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{
		Name:       s.keyVersion,
		Data:       payload,
		DataCrc32C: wrapperspb.Int64(sum),
	})
	if err != nil {
		return nil, fmt.Errorf("the key ring's signing API: %w", err)
	}
	if !answer.GetVerifiedDataCrc32C() {
		return nil, fmt.Errorf("the key ring did not check the %d byte(s) it was asked to sign, so what it signed is not what this service built", len(payload))
	}
	// The answer names the key version that signed, and it is compared rather
	// than read: a signature under a version this service did not ask for is an
	// assertion whose `kid` names a key nobody published.
	if signed := answer.GetName(); signed != "" && signed != s.keyVersion {
		return nil, fmt.Errorf("the key ring signed with %s, and this service signs under %s", signed, s.keyVersion)
	}
	signature := answer.GetSignature()
	if len(signature) == 0 {
		return nil, errors.New("the key ring answered an empty signature")
	}
	counted := answer.GetSignatureCrc32C()
	if counted == nil || counted.Value != int64(crc32.Checksum(signature, crcTable)) {
		return nil, fmt.Errorf("the key ring answered a %d-byte signature whose bytes are not the ones it counted, so the signature is not the one it made", len(signature))
	}
	return signature, nil
}

// serve runs the routes on the platform's port until the platform asks the
// container to stop, then lets what is in flight finish.
func serve(handler http.Handler) error {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	select {
	case err := <-done:
		return err
	case <-stop.Done():
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return server.Shutdown(ctx)
	}
}
