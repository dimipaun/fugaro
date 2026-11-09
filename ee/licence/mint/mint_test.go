// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package main

// The mint tool's tests (subscriptions plan S2.10): the three the plan names,
// and the two halves of the tool's own rules that no other task holds.
//
// **Every key here is minted with a fake signer**: an Ed25519 keypair generated
// in the process that reads it, so the private half never leaves memory and no
// key material enters a file or a fixture (.claude/rules/ee.md, R5). What the
// fake stands in for is the KMS signing API, which nothing in this file
// reaches: the tool's contract is that a signature comes from the Signer it is
// handed and from nowhere else, and that is what these tests hold.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// testKeyID is the id the tests key the ring by. No production keyring holds it
// and none ever will: S2.9 publishes the owner's, and this one is a test's.
const testKeyID = "licence-mint-test-1"

// ed25519Path is the one crypto package the tool's own sources may name.
const ed25519Path = "crypto/ed25519"

// testNow is the instant every claim set below is minted at. The tool checks
// what it minted at the claim set's own `issued`, so nothing in this file needs
// the machine's clock to be anything in particular.
var testNow = time.Unix(1794096000, 0)

// teamClaims is one valid key's claim set: a Team key for a domain, one year
// long, minted at testNow.
func teamClaims() licence.Claims {
	return licence.Claims{
		Tier:         licence.TierTeam,
		Features:     []string{"report"},
		Seats:        3,
		Organisation: "example.com",
		Issued:       testNow.Unix(),
		Expires:      testNow.Add(365 * 24 * time.Hour).Unix(),
	}
}

// fakeSigner is the tool's Signer in every test: a keypair generated here, and
// the payloads it was asked to sign, so a test can hold that what the tool
// signed is what it hands out.
type fakeSigner struct {
	priv ed25519.PrivateKey
	seen [][]byte
}

func newFakeSigner(t *testing.T) *fakeSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the fake signer's keypair: %v", err)
	}
	return &fakeSigner{priv: priv}
}

func (f *fakeSigner) Sign(_ context.Context, payload []byte) ([]byte, error) {
	f.seen = append(f.seen, append([]byte(nil), payload...))
	return ed25519.Sign(f.priv, payload), nil
}

func (f *fakeSigner) public() ed25519.PublicKey {
	return f.priv.Public().(ed25519.PublicKey)
}

// verifyWith is the check the tests hand the tool, and the one the command
// hands it: S2.3's verifier over the ring the caller names rather than the
// compiled one, which is empty until S2.9 publishes the owner's key.
func verifyWith(ring licence.Keyring) Verifier {
	return func(raw []byte, now time.Time) (licence.Claims, licence.State, error) {
		return licence.VerifyAgainst(raw, now, ring)
	}
}

// ringOf is the ring the tests hand the verifier: the fake signer's public half
// under the id the key text leads with, read from the spelling a keyring entry
// carries, so the tests exercise the path --public-key takes in the command.
func ringOf(t *testing.T, f *fakeSigner) licence.Keyring {
	t.Helper()
	ring, err := ringFromPublicKey(testKeyID, base64.RawURLEncoding.EncodeToString(f.public()))
	if err != nil {
		t.Fatalf("reading the fake signer's public half as a keyring entry: %v", err)
	}
	return ring
}

// minted mints one claim set through a fresh fake signer, and answers the key
// text, the signer and the ring it was checked against.
func minted(t *testing.T, claims licence.Claims) (string, *fakeSigner, licence.Keyring) {
	t.Helper()
	signer := newFakeSigner(t)
	ring := ringOf(t, signer)
	key, err := Mint(context.Background(), testKeyID, claims, signer, verifyWith(ring))
	if err != nil {
		t.Fatalf("minting %+v: %v", claims, err)
	}
	return key, signer, ring
}

// partOf decodes one part of a key text: the id at index 0 is the text it is,
// and the payload and the signature at 1 and 2 are the base64url the design
// spells them in.
func partOf(t *testing.T, raw []byte, index int) []byte {
	t.Helper()
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 {
		t.Fatalf("the tool answered %q, which is not a key text: it is not three dot-separated parts", raw)
	}
	if index == 0 {
		return []byte(parts[0])
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(parts[index])
	if err != nil {
		t.Fatalf("part %d of %q is not unpadded base64url: %v", index, raw, err)
	}
	return decoded
}

// TestMintSignsThroughTheSignerOnly holds the tool's first rule where a test
// can see it: the signature the key text carries is the Signer's own answer,
// over exactly the bytes the key text carries as its payload. The other half of
// the rule -- that the package has no way to sign at all without a Signer -- is
// TestTheToolHoldsNoWayToSignButTheSigner.
func TestMintSignsThroughTheSignerOnly(t *testing.T) {
	key, signer, _ := minted(t, teamClaims())

	if len(signer.seen) != 1 {
		t.Fatalf("the signer was asked to sign %d times, want 1: one key text is one signature, and the tool has no other route to one (S2.10, \"never a local private key file\")", len(signer.seen))
	}
	payload := partOf(t, []byte(key), 1)
	if !bytes.Equal(payload, signer.seen[0]) {
		t.Errorf("the key carries %q and the signer signed %q: the tool signs the bytes it hands out, or a verifier reads claims no signature covers", payload, signer.seen[0])
	}
	if want := ed25519.Sign(signer.priv, payload); !bytes.Equal(partOf(t, []byte(key), 2), want) {
		t.Errorf("the key's signature is not the one the signer answered: %x, want %x", partOf(t, []byte(key), 2), want)
	}
	if id := string(partOf(t, []byte(key), 0)); id != testKeyID {
		t.Errorf("the key text leads with %q, want %q: the id is the part a verifier places the key by", id, testKeyID)
	}
}

// TestMintRefusesAClaimBeyondTheTierLifetime holds the tool to the verifier's
// own ceiling: a term past the tier's maximum is refused, and the error is the
// verifier's ErrLifetime -- because S2.1's maxima live in the verifier and the
// tool restates no copy of them. Nothing is answered beside the refusal, so
// nothing the tool prints is a key a verifier would refuse.
func TestMintRefusesAClaimBeyondTheTierLifetime(t *testing.T) {
	claims := teamClaims()
	// A second past the tier's maximum, which S2.1 fixes as the tier's term
	// plus the 30-day issue lead a renewal key needs.
	claims.Expires = claims.Issued + int64((365+30)*24*time.Hour/time.Second) + 1

	signer := newFakeSigner(t)
	key, err := Mint(context.Background(), testKeyID, claims, signer, verifyWith(ringOf(t, signer)))
	if !errors.Is(err, licence.ErrLifetime) {
		t.Fatalf("a key one second past the tier's maximum answered %v, want ErrLifetime", err)
	}
	if key != "" {
		t.Errorf("the tool answered %q beside the refusal: nothing but a key a verifier accepts may reach the owner's stdout", key)
	}
}

// TestMintedKeyVerifies is the rule the whole tool exists for: what it answers
// is a key S2.3's verifier accepts, read at the instant it was minted, carrying
// the claim set the tool was given -- so an interim hand-minted key and the
// service's are one format to every verifier (design
// docs/design/key-signing-service.md §1).
func TestMintedKeyVerifies(t *testing.T) {
	claims := teamClaims()
	key, _, ring := minted(t, claims)

	got, state, err := licence.VerifyAgainst([]byte(key), testNow, ring)
	if err != nil {
		t.Fatalf("the verifier refused the key the tool minted: %v", err)
	}
	if state != licence.Valid {
		t.Errorf("the minted key reads %q at its own issued instant, want %q", state, licence.Valid)
	}
	if !reflect.DeepEqual(got, claims) {
		t.Errorf("the minted key's claims are %+v, want the claim set the tool was given (%+v)", got, claims)
	}
}

// TestTheToolHoldsNoWayToSignButTheSigner is the half of the first rule a
// runtime assertion cannot make: reading the tool's own sources -- the command,
// the mint and the KMS signer, and not these tests -- they import no crypto
// package but crypto/ed25519, and of that package they name no signing or
// private-key symbol. A public half is not refused: the ring the tool checks
// against is made of one. What the scan says is that there is no route to a
// signature here but the Signer the caller hands in, which is what "never a
// local private key file" means in code (.claude/rules/ee.md, R5).
func TestTheToolHoldsNoWayToSignButTheSigner(t *testing.T) {
	// The symbols that would sign, hold or derive a private half. Signing a
	// payload and reading a private key out of a file both need one of these,
	// and ed25519.PrivateKey is also what a conversion from raw bytes names.
	forbidden := []string{"Sign", "SignTo", "PrivateKey", "GenerateKey", "NewKeyFromSeed"}

	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the tool's own sources: %v", err)
	}
	var files, imports int
	for _, pkg := range parsed {
		for name, file := range pkg.Files {
			files++
			local := localEd25519Name(file)
			for _, spec := range file.Imports {
				imports++
				path := strings.Trim(spec.Path.Value, `"`)
				if strings.HasPrefix(path, "crypto/") && path != ed25519Path {
					t.Errorf("%s imports %s: the tool's only route to a signature is the Signer it is handed, so a second crypto package is a second route", name, path)
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok || local == "" || ident.Name != local {
					return true
				}
				if slices.Contains(forbidden, selector.Sel.Name) {
					t.Errorf("%s names %s.%s: that is a signing or private-key operation, and this package must hold none", name, ident.Name, selector.Sel.Name)
				}
				return true
			})
		}
	}
	if files == 0 || imports == 0 {
		t.Fatalf("the scan read %d files and %d imports: a scan that resolves nothing passes vacuously", files, imports)
	}
}

// localEd25519Name answers what one file binds crypto/ed25519 to -- its own
// name or the alias an import gives it -- and "" when the file does not import
// it at all. The scan above asks by the binding rather than by the spelling
// "ed25519", so an aliased import is caught too.
func localEd25519Name(file *ast.File) string {
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != ed25519Path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return "ed25519"
	}
	return ""
}

// TestKMSHandsThePayloadToTheKeySigningAPI holds the signer's own half: it runs
// gcloud's asymmetric-sign with the key's coordinates, hands the payload to the
// API through a file rather than a command line, and answers the bytes the
// signature file holds. Nothing here reaches gcloud -- the runner is the test's
// own -- and no credential, no private key and no signing key is involved.
func TestKMSHandsThePayloadToTheKeySigningAPI(t *testing.T) {
	payload := []byte(`{"tier":"team"}`)
	want := bytes.Repeat([]byte{7}, ed25519.SignatureSize)
	var (
		gotArgs    []string
		gotPayload []byte
	)
	signer := KMS{
		Project:   "a-project",
		Location:  "a-location",
		KeyRing:   "a-key-ring",
		Key:       "a-key",
		Version:   "1",
		Algorithm: "a-signing-algorithm",
		Run: func(_ context.Context, args ...string) error {
			gotArgs = append([]string(nil), args...)
			input, err := argValue(args, "--input-file")
			if err != nil {
				return err
			}
			if gotPayload, err = os.ReadFile(input); err != nil {
				return err
			}
			output, err := argValue(args, "--signature-file")
			if err != nil {
				return err
			}
			return os.WriteFile(output, want, 0o600)
		},
	}

	signature, err := signer.Sign(context.Background(), payload)
	if err != nil {
		t.Fatalf("signing through the fake runner: %v", err)
	}
	if !bytes.Equal(signature, want) {
		t.Errorf("the signer answered %x, want the bytes the signature file held (%x)", signature, want)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Errorf("the signing API was handed %q, want the payload as it is spelled (%q)", gotPayload, payload)
	}
	if slices.Contains(gotArgs, string(payload)) {
		t.Errorf("the argv carries the payload itself: %v. Bytes in an argv are bytes every local process reads, so the payload travels as the input file", gotArgs)
	}
	for _, want := range []string{"kms", "asymmetric-sign", "--project", "a-project", "--location", "a-location", "--keyring", "a-key-ring", "--key", "a-key", "--version", "1", "--algorithm", "a-signing-algorithm"} {
		if !slices.Contains(gotArgs, want) {
			t.Errorf("the argv carries no %q: %v", want, gotArgs)
		}
	}
}

// argValue reads one flag's value out of an argv, so a test holds that the
// payload and the signature travel as files and not as command-line words.
func argValue(args []string, name string) (string, error) {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1], nil
		}
	}
	return "", fmt.Errorf("the argv carries no %s: %v", name, args)
}
