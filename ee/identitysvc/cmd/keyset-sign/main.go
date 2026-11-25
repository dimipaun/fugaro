// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Command keyset-sign is the owner's key-set signing tool (mobile identity plan
// task A4; the owner's O7, O15 and O18; Check 737). It wraps the identity
// project's JWKS in the envelope C1's contract fixes, has the root key sign it
// through Cloud KMS, and writes the signed document where A2 serves it from —
// the object `FUGARO_IDENTITY_KEYSET` names, which only the owner writes and the
// signing service reads.
//
// **Use is OWNER-RUN.** The owner runs it at their own terminal, with the
// credentials of the separate owner identity the root key project grants, and no
// agent runs it, applies anything or holds a key: the root key's private half is
// generated inside Cloud KMS and read out by nothing (.claude/rules/ee.md R5,
// ee/identitysvc/rootkey/terraform). Run it from the repository root:
//
//	go run ./ee/identitysvc/cmd/keyset-sign \
//	  -key-version projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoVersions/1 \
//	  -root-kid root-2026-11 -jwks jwks.json -seq 1 -out gs://P-keyset/keyset.json
//
// The command has no way to sign but Cloud KMS and no way to wait for anything:
// every value it reads is a flag, a file it names or the object it names, it
// never reads its input, and a missing one is a refusal that prints the fix
// rather than a question. Both halves of O15 are here — the agent-session fence
// this command opens with, and the grant on the key, which is the root key
// project's Terraform and the owner's own IAM.
//
// **What it refuses, and why each refusal is before the signature.** A `seq` not
// above the one the document at the destination already carries is refused
// before the root key is asked for anything: a verifier compares that number
// against the floor its build pinned and the highest one it has stored, so a set
// that goes backwards walks a project onto keys it has already rotated out. A
// JWKS holding a key no verifier would accept is refused for the same reason —
// the document a refused set would have produced is one nobody can verify, and
// the root key must not have signed it.
//
// **What it prints.** The root key's own public half, and the `seq` and the
// dates of the set it wrote, and no private material: those are the values P8
// pins into a build and Check 737 records (O7).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"golang.org/x/term"

	"github.com/dimipaun/fugaro/ee/identitysvc"
	"github.com/dimipaun/fugaro/internal/blobx"
)

// contentType is what the signed document is written as. It is the type A2
// answers the pinned URL with, so a reader that stored the object and served it
// keeps one type.
const contentType = "application/jose"

func main() {
	if err := run(os.Args[1:], os.Stdout, environment{getenv: os.Getenv, terminal: stdinIsTerminal()}, kmsRing); err != nil {
		fmt.Fprintln(os.Stderr, "keyset-sign:", err)
		os.Exit(1)
	}
}

// environment is the two facts the agent fence reads, and it is a value so that
// a test can be a person's terminal or an agent's session without a terminal.
type environment struct {
	getenv   func(string) string
	terminal bool
}

// stdinIsTerminal reports whether this process's own input is a real terminal:
// it is what tells a person's IDE terminal from a coding agent's tool call, which
// has none (internal/agentenv).
func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// ringOpener opens the KMS client the signature comes through, and answers the
// close that lets the process end. It is a parameter so that a test signs with a
// key generated in its own process rather than with a key ring.
type ringOpener func(ctx context.Context) (identitysvc.RootKeyRing, func() error, error)

// kmsRing opens the real client. The key version it signs with is a flag: this
// command is run as the owner identity on a machine where the root key project's
// KMS is reachable, so the client is whatever the environment's credentials
// reach — and a login that may not sign is refused by Cloud KMS, which is the
// reading Check 737 takes.
func kmsRing(ctx context.Context) (identitysvc.RootKeyRing, func() error, error) {
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("the Cloud KMS client: %w", err)
	}
	return client, client.Close, nil
}

// run is main's whole body: the fence, the flags, the document it builds and the
// one line printed from it.
func run(args []string, out io.Writer, env environment, open ringOpener) error {
	// Before anything is read: the refusal is about who is running (O15), the way
	// `fugaro user` decides it for the same reason. Signing is granted to one
	// owner identity an agent's session does not hold, so this is a speed bump
	// and not a barrier — what it stops is the accident.
	if err := identitysvc.RefuseAgentSession(env.getenv, env.terminal); err != nil {
		return err
	}
	flags := flag.NewFlagSet("keyset-sign", flag.ContinueOnError)
	flags.SetOutput(out)
	keyVersion := flags.String("key-version", "", "the root key version to sign with, as Cloud KMS names it (projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoVersions/N): an asymmetric key has no primary version, so there is no default")
	rootKID := flags.String("root-kid", "", "the root key's own id, which the envelope's header carries and P8 pins")
	jwksPath := flags.String("jwks", "", "the JWKS to publish, as the identity service serves it at /.well-known/jwks.json")
	seq := flags.Int64("seq", 0, "the sequence number that orders this set against the ones before it, above the last signed set's: a verifier refuses a set below the highest one it has stored (O7)")
	destination := flags.String("out", "", "where the signed document is written, as a bucket URL with an object under it (gs://bucket/keyset.json): the object A2 serves the pinned URL from")
	now := flags.String("iat", "", "the instant the set is signed at, as an RFC 3339 stamp; the default is now, and the set is valid 180 days on from it (O18)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// flag.Parse stops at the first word that is not a flag, and every flag
	// written after one is dropped without a word: a seq or a destination there
	// is a set the owner asked for and one that is not signed. This command takes
	// nothing but flags, so a word is always a mistake.
	if extra := flags.Args(); len(extra) > 0 {
		return fmt.Errorf("unexpected %s: this command takes nothing but flags, and a word that is not one stops the reading, so every flag after it is dropped in silence", strings.Join(extra, " "))
	}
	var missing []string
	for _, required := range []struct{ name, value string }{
		{"key-version", *keyVersion},
		{"root-kid", *rootKID},
		{"jwks", *jwksPath},
		{"out", *destination},
	} {
		if strings.TrimSpace(required.value) == "" {
			missing = append(missing, "-"+required.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s: the signed key set is built from the JWKS, the seq and the root key the flags name, and a default for any of them is a document nobody meant to publish", strings.Join(missing, ", "))
	}
	if *seq <= 0 {
		return errors.New("-seq must be a positive number: it is compared against the last signed set's, and a verifier refuses a set below the floor its build pinned (O7)")
	}
	signedAt := time.Now()
	if strings.TrimSpace(*now) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*now))
		if err != nil {
			return fmt.Errorf("-iat is not an RFC 3339 stamp: %w", err)
		}
		signedAt = parsed
	}
	document, err := readKeySetFile(*jwksPath)
	if err != nil {
		return err
	}
	jwks, err := identitysvc.ParseKeySetJWKS(document)
	if err != nil {
		return fmt.Errorf("%s: %w", *jwksPath, err)
	}

	ctx := context.Background()
	bucketURL, object, err := splitObjectURL(*destination)
	if err != nil {
		return fmt.Errorf("-out: %w", err)
	}
	bucket, err := blobx.Open(ctx, bucketURL)
	if err != nil {
		return err
	}
	defer bucket.Close()
	// The document being replaced is read from the destination itself, because
	// that is the set a verifier can already have fetched: a number kept anywhere
	// else would be a second answer to the same question, and the answer that
	// matters is what is served. Its signature is not verified here and its
	// contents are not read beyond the sequence number — this command is not a
	// verifier, and the set it replaces is the owner's own write (O7).
	previous, err := readPreviousSet(ctx, bucket, object)
	if err != nil {
		return err
	}

	ring, closeRing, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = closeRing() }()
	root, err := identitysvc.NewRootKey(ring, *keyVersion)
	if err != nil {
		return err
	}
	signed, rootJWK, err := identitysvc.SignKeySet(ctx, root, identitysvc.KeySetRequest{
		JWKS:     jwks,
		Seq:      *seq,
		RootKID:  *rootKID,
		Previous: previous,
	}, signedAt)
	if err != nil {
		return err
	}
	if err := bucket.Put(ctx, object, signed, contentType); err != nil {
		return fmt.Errorf("writing the signed key set to %s: %w", *destination, err)
	}
	printed, err := json.Marshal(rootJWK)
	if err != nil {
		return fmt.Errorf("the root key's public half: %w", err)
	}
	fmt.Fprintf(out, "signed key set  seq %d  iat %s  exp %s  valid %s\n",
		*seq,
		signedAt.UTC().Format(time.RFC3339),
		signedAt.Add(identitysvc.KeySetLifetime).UTC().Format(time.RFC3339),
		identitysvc.KeySetLifetime,
	)
	fmt.Fprintf(out, "wrote           %s (%d bytes, %s)\n", *destination, len(signed), contentType)
	fmt.Fprintf(out, "root public key %s\n", printed)
	fmt.Fprintf(out, "next            read %s back and pin this key and this seq before the release (P8, Check 737)\n", *destination)
	return nil
}

// readKeySetFile reads the JWKS the owner names, bounded before its bytes are
// held: a key set is small, and a file this command should never have been given
// is refused rather than read whole.
func readKeySetFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading the JWKS: %w", err)
	}
	if info.Size() > identitysvc.MaxKeySetBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d a key set may be", path, info.Size(), identitysvc.MaxKeySetBytes)
	}
	document, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the JWKS: %w", err)
	}
	if len(document) == 0 {
		return nil, fmt.Errorf("%s is empty, and a key set with no keys is one nothing verifies against", path)
	}
	return document, nil
}

// readPreviousSet reads the document already at the destination, and treats an
// absent object as the identity project's first set.
//
// The read is strict for the reason A2's own reader is: this command holds
// unconditional access to the bucket, so an HTTP 403 is a permission the owner
// does not have rather than an absent object, and folding the two together would
// sign a set over a document this command could not read — the one shape the seq
// refusal exists to prevent.
func readPreviousSet(ctx context.Context, bucket *blobx.Bucket, object string) ([]byte, error) {
	document, _, err := bucket.ReadMaxStrict(ctx, object, identitysvc.MaxKeySetBytes)
	if errors.Is(err, blobx.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the key set being replaced out of the bucket: %w", err)
	}
	return document, nil
}

// splitObjectURL reads the destination: a bucket URL with an object under it,
// split into the bucket to open and the object to write. Production's is
// gs://bucket/keyset.json and the object is what A2 serves; the driver a URL
// names is blobx's business (`file://` writes a file, `mem://` keeps it in the
// process), which is what lets a rehearsal and a test run the whole command
// without a cloud.
//
// The object is required: a location with no object under it is a bucket whose
// key set nobody named, and writing one would put the document somewhere no
// reader looks for it.
//
// The object is the whole path under the bucket, and the bucket is the authority
// — for `gs://bucket/a/b.json` the object is `a/b.json` and never `b.json` in
// `gs://bucket/a`, because GCS takes the bucket from the authority alone and the
// rest of the URL after it would be dropped without a word. `file://` is the one
// driver whose URL is a directory rather than an authority, so there the object
// is the last segment and the directory is the bucket: `file:///tmp/rehearsal/
// keyset.json` writes `/tmp/rehearsal/keyset.json`.
func splitObjectURL(rawURL string) (bucketURL, key string, err error) {
	trimmed := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("the signed key set's destination is not a bucket URL such as gs://bucket/keyset.json: %w", err)
	}
	if parsed.Scheme == "" {
		return "", "", errors.New("the signed key set's destination is not a bucket URL such as gs://bucket/keyset.json: it names no scheme")
	}
	// A query or a fragment is refused rather than dropped: blobx reads one of
	// them (file:// sets metadata=skip on its own), and a destination carrying
	// one this command silently threw away is a location the owner did not ask
	// for.
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("the signed key set's destination carries a query or a fragment, and neither names anything about where the document goes")
	}
	if parsed.Path == "" || parsed.Path == "/" || strings.HasSuffix(parsed.Path, "/") {
		return "", "", errors.New("the signed key set's destination names no object in the bucket, and the object is what A2 serves")
	}
	if parsed.Host == "" {
		if parsed.Scheme != "file" {
			return "", "", fmt.Errorf("the signed key set's destination has no bucket in it: %s names no authority, and only file:// is a directory rather than a bucket", trimmed)
		}
		object := path.Base(parsed.Path)
		parsed.Path = path.Dir(parsed.Path)
		return parsed.String(), object, nil
	}
	return parsed.Scheme + "://" + parsed.Host + "/", strings.TrimPrefix(parsed.Path, "/"), nil
}
