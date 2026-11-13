// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package main

// The mint tool's tests: the one key format the tool writes (licence-key-v2
// plan K1.3, and K1.1's verifier under it), and the two halves of the tool's
// rules that no other task holds -- the signing seam and the input guards the
// verifier does not make.
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
	"io"
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

// testWorkspace is the workspace the v1 claim sets below are bound to: the
// Firebase project id a verifier is asked with (TN2). No installation holds it.
const testWorkspace = "fugaro-mint-test-project"

// testNow is the instant the v1 claim sets below are minted at, in UTC and to
// the second, because the format writes its times as RFC 3339 stamps. It is read
// off the machine's clock a day back rather than pinned, because the v1 command
// does consult that clock: it refuses a claim set whose term has already run out
// (a stamp in the past moves the instant the verifier's own state check is made,
// so nothing else catches one), and a pinned instant would be the day that guard
// starts refusing every test that mints in the past. A day back keeps the stamp
// in the past -- which is what -issued-at is for, and what the tests assert lands
// in the key -- while the year-long term is well inside its end.
var testNow = time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)

// teamClaims is one valid v1 key's claim set with every field of the format
// set: a Team key for a domain, one year long, renewing automatically, minted
// at testNow. The fields a test varies are the ones it names.
func teamClaims() licence.Claims {
	interval := "P1D"
	return licence.Claims{
		V:                 versionV1,
		KID:               testKeyID,
		KeyID:             "lk_mint_test_0001",
		WorkspaceID:       testWorkspace,
		Organisation:      "example.com",
		Tier:              licence.TierTeam,
		Seats:             3,
		IssuedAt:          testNow,
		PaidThrough:       testNow.Add(365 * 24 * time.Hour),
		GraceDays:         30,
		RenewalMode:       licence.RenewalAuto,
		RenewBeforeDays:   7,
		CheckInterval:     &interval,
		RenewalSecretHash: "a-hash-of-a-renewal-secret-and-not-the-secret",
		Warnings:          []licence.WarningV1{{Roles: []string{"admin"}, From: "grace_start"}},
		Kind:              licence.KindPaid,
		Note:              "a test key",
	}
}

// verifyWith is the check the tests hand the tool for a v1 key, and the one
// the command hands it: K1.1's verifier, over the ring the caller names and the
// workspace the key is read in. The ring is the caller's rather than the
// compiled one, which is empty until S2.9 publishes the owner's key.
func verifyWith(ring licence.Keyring, firebaseProjectID string) Verifier {
	return func(raw []byte, now time.Time) (licence.Claims, licence.State, error) {
		return licence.Verify(raw, now, ring, firebaseProjectID)
	}
}

// minted mints one v1 claim set through a fresh fake signer, and answers the
// key text, the signer and the ring it was checked against. The key is read in
// the workspace it is bound to, which is what the tool does with its own
// -workspace-id.
func minted(t *testing.T, claims licence.Claims) (string, *fakeSigner, licence.Keyring) {
	t.Helper()
	signer := newFakeSigner(t)
	ring := ringOf(t, signer)
	key, err := Mint(context.Background(), claims, signer, verifyWith(ring, claims.WorkspaceID))
	if err != nil {
		t.Fatalf("minting %+v: %v", claims, err)
	}
	return key, signer, ring
}

// partsOf decodes a v1 key text: the `fgk_` prefix the format leads with,
// then the payload and the signature, both unpadded base64url.
func partsOf(t *testing.T, key string) (payload, signature []byte) {
	t.Helper()
	rest, ok := strings.CutPrefix(strings.TrimSpace(key), "fgk_")
	if !ok {
		t.Fatalf("the tool answered %q, which is not a v1 key text: it does not lead with fgk_", key)
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		t.Fatalf("the tool answered %q, which is not a v1 key text: it is not a payload and a signature", key)
	}
	decoded := make([][]byte, 0, 2)
	for _, part := range parts {
		raw, err := base64.RawURLEncoding.Strict().DecodeString(part)
		if err != nil {
			t.Fatalf("a part of %q is not unpadded base64url: %v", key, err)
		}
		decoded = append(decoded, raw)
	}
	return decoded[0], decoded[1]
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

// TestTheToolHoldsNoWayToSignButTheSigner is the half of the first rule a
// runtime assertion cannot make: reading the tool's own sources -- the command,
// the mint and the KMS signer, and not these tests -- they import no crypto
// package but crypto/ed25519, and of that package they name no signing or
// private-key symbol. A public half is not refused: the ring the tool checks
// against is made of one. What the scan says is that there is no route to a
// signature here but the Signer the caller hands in, which is what "never a
// local private key file" means in code (.claude/rules/ee.md, R5).
func TestTheToolHoldsNoWayToSignButTheSigner(t *testing.T) {
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
			imports += len(file.Imports)
			for _, problem := range signingRoutes(file) {
				t.Errorf("%s %s", name, problem)
			}
		}
	}
	if files == 0 || imports == 0 {
		t.Fatalf("the scan read %d files and %d imports: a scan that resolves nothing passes vacuously", files, imports)
	}
}

// signingRoutes answers what one parsed file holds that could sign a payload or
// hold a private half, and it is a function rather than a loop in the test above
// because the scan's own reach is a thing to hold too: TestTheScanRefusesASecondWayToSign
// parses a source it would accept and holds this to refusing it.
//
// Three things are refused: an import of any package but crypto/ed25519 that
// names crypto or ed25519, however it is spelled; a dot-import of that one,
// whose names are bare identifiers and so are not selectors this scan can
// follow; and any of the symbols that sign, hold or derive a private half, named
// through the binding the file gives the package -- its own name or an alias, so
// a renamed import is caught.
func signingRoutes(file *ast.File) []string {
	// The symbols that would sign, hold or derive a private half. Signing a
	// payload and reading a private key out of a file both need one of these,
	// and ed25519.PrivateKey is also what a conversion from raw bytes names.
	forbidden := []string{"Sign", "SignTo", "PrivateKey", "GenerateKey", "NewKeyFromSeed"}

	var problems []string
	local := localEd25519Name(file)
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		// The check is on the name and not on the "crypto/" prefix: a signing
		// package by another spelling is the same route, and
		// golang.org/x/crypto/ed25519 -- a module this repository already
		// depends on -- signs exactly as the standard library's does.
		if path != ed25519Path && (strings.Contains(path, "crypto") || strings.Contains(path, "ed25519")) {
			problems = append(problems, fmt.Sprintf("imports %s: the tool's only route to a signature is the Signer it is handed, so any package but crypto/ed25519 that names crypto or ed25519 is a second route", path))
		}
		if path == ed25519Path && spec.Name != nil && spec.Name.Name == "." {
			problems = append(problems, fmt.Sprintf("dot-imports %s: a dot-imported name is a bare identifier and not a selector, so a signing call made through one is not something this scan could see, and the tool's only route to a signature is the Signer it is handed", path))
		}
	}
	if local == "" {
		return problems
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok || ident.Name != local {
			return true
		}
		if slices.Contains(forbidden, selector.Sel.Name) {
			problems = append(problems, fmt.Sprintf("names %s.%s: that is a signing or private-key operation, and this package must hold none", ident.Name, selector.Sel.Name))
		}
		return true
	})
	return problems
}

// TestTheScanRefusesASecondWayToSign holds the scan's own reach, in both
// directions: the ways this package could reach a signature without the Signer
// are refused, and the import it may hold is not, so the rule is not simply
// "refuse more".
//
// Two of the refusals are blind spots rather than rules:
//
//   - a dot-import of crypto/ed25519 makes every name in it a bare identifier,
//     which is not an *ast.SelectorExpr and therefore nothing the scan above can
//     follow;
//   - a crypto package by another spelling -- golang.org/x/crypto/ed25519 signs
//     exactly as the standard library's does, and this repository already
//     depends on that module -- is not reached by a check on the "crypto/"
//     prefix, so the check is on the name.
//
// Each source is parsed from a string: a fixture file of this package would be a
// file the scan reads, and one of these must never exist here.
func TestTheScanRefusesASecondWayToSign(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   bool
	}{
		{
			"a dot import beside a bare signing call",
			"package main\n\nimport . \"crypto/ed25519\"\n\nfunc key(seed []byte) any { return NewKeyFromSeed(seed) }\n",
			true,
		},
		{
			"a dot import beside a bare public key",
			"package main\n\nimport . \"crypto/ed25519\"\n\nfunc size() int { return PublicKeySize }\n",
			true,
		},
		{
			"an aliased import beside a signing call",
			"package main\n\nimport e \"crypto/ed25519\"\n\nfunc key(seed []byte) any { return e.NewKeyFromSeed(seed) }\n",
			true,
		},
		{
			"the same signer under another module's name",
			"package main\n\nimport xed \"golang.org/x/crypto/ed25519\"\n\nfunc sign(key, payload []byte) []byte { return xed.Sign(xed.PrivateKey(key), payload) }\n",
			true,
		},
		{
			"the imports the tool may hold",
			"package main\n\nimport \"crypto/ed25519\"\n\nfunc size() int { return ed25519.PublicKeySize }\n",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "a-file.go", tc.source, 0)
			if err != nil {
				t.Fatalf("parsing %q: %v", tc.source, err)
			}
			problems := signingRoutes(file)
			if tc.want && len(problems) == 0 {
				t.Errorf("%q holds no problem, want a refusal: the scan would read straight past a route to a signature", tc.source)
			}
			if !tc.want && len(problems) > 0 {
				t.Errorf("%q holds %v, want none: a public key is what the ring the tool checks against is made of", tc.source, problems)
			}
		})
	}
}

// localEd25519Name answers what one file binds crypto/ed25519 to -- its own
// name or the alias an import gives it -- and "" when the file does not import
// it at all. The scan above asks by the binding rather than by the spelling
// "ed25519", so an aliased import is caught too. A dot-binding is not a name a
// selector reaches, so it answers "" there as well: signingRoutes refuses that
// import instead, and there is no selector to look for.
func localEd25519Name(file *ast.File) string {
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != ed25519Path {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name == "." {
				return ""
			}
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
//
// The flags are the ones gcloud actually has: `gcloud kms asymmetric-sign
// --help` lists --input-file, --signature-file, --digest-algorithm and
// --skip-integrity-verification, and no --algorithm at all, and a flag it does
// not know is a run that fails on every mint. No --digest-algorithm either:
// that flag is what a digest-based key takes, and Ed25519 -- the one algorithm
// this format reads -- signs the payload itself.
func TestKMSHandsThePayloadToTheKeySigningAPI(t *testing.T) {
	payload := []byte(`{"tier":"team"}`)
	want := bytes.Repeat([]byte{7}, ed25519.SignatureSize)
	var (
		gotArgs    []string
		gotPayload []byte
	)
	signer := KMS{
		Project:  "a-project",
		Location: "a-location",
		KeyRing:  "a-key-ring",
		Key:      "a-key",
		Version:  "1",
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
	for _, want := range []string{"kms", "asymmetric-sign", "--project", "a-project", "--location", "a-location", "--keyring", "a-key-ring", "--key", "a-key", "--version", "1"} {
		if !slices.Contains(gotArgs, want) {
			t.Errorf("the argv carries no %q: %v", want, gotArgs)
		}
	}
	for _, unwanted := range []string{"--algorithm", "--digest-algorithm"} {
		if slices.Contains(gotArgs, unwanted) {
			t.Errorf("the argv carries %s: %v. gcloud's asymmetric-sign has no --algorithm, and a digest algorithm is not what a raw Ed25519 signature takes", unwanted, gotArgs)
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

// signingRunner is the Runner the command's tests hand it: the KMS seam without
// a KMS. It reads the payload gcloud would have been handed and answers with the
// test signer's signature over it, which is what a KMS key's signing API answers.
func signingRunner(f *fakeSigner) Runner {
	return func(ctx context.Context, args ...string) error {
		input, err := argValue(args, "--input-file")
		if err != nil {
			return err
		}
		payload, err := os.ReadFile(input)
		if err != nil {
			return err
		}
		signature, err := f.Sign(ctx, payload)
		if err != nil {
			return err
		}
		output, err := argValue(args, "--signature-file")
		if err != nil {
			return err
		}
		return os.WriteFile(output, signature, 0o600)
	}
}

// TestMintedKeyVerifies is the rule the whole tool exists for: what it
// answers is a key K1.1's verifier accepts, read at the instant it was minted,
// carrying the claim set the tool was given, field for field -- so an interim
// hand-minted key and the service's are one format to every verifier (design
// docs/design/key-signing-service.md §1).
func TestMintedKeyVerifies(t *testing.T) {
	claims := teamClaims()
	key, signer, ring := minted(t, claims)

	got, state, err := licence.Verify([]byte(key), claims.IssuedAt, ring, testWorkspace)
	if err != nil {
		t.Fatalf("the verifier refused the key the tool minted: %v", err)
	}
	if state != licence.Active {
		t.Errorf("the minted key reads %q at its own issued instant, want %q", state, licence.Active)
	}
	// The claim set is compared field by field rather than as a struct: what the
	// tool writes is what a verifier reads back out of the payload, and a field
	// this test forgot to name is a field nothing here would notice.
	if got.V != claims.V || got.KID != claims.KID || got.KeyID != claims.KeyID {
		t.Errorf("the minted key's version, kid or key id is %d/%q/%q, want %d/%q/%q", got.V, got.KID, got.KeyID, claims.V, claims.KID, claims.KeyID)
	}
	if got.WorkspaceID != claims.WorkspaceID || got.Organisation != claims.Organisation {
		t.Errorf("the minted key binds %q for %q, want %q for %q", got.WorkspaceID, got.Organisation, claims.WorkspaceID, claims.Organisation)
	}
	if got.Tier != claims.Tier || got.Seats != claims.Seats || got.Kind != claims.Kind {
		t.Errorf("the minted key is a %s key of %d seat(s) of kind %s, want %s/%d/%s", got.Tier, got.Seats, got.Kind, claims.Tier, claims.Seats, claims.Kind)
	}
	if !got.IssuedAt.Equal(claims.IssuedAt) || !got.PaidThrough.Equal(claims.PaidThrough) {
		t.Errorf("the minted key runs %s to %s, want %s to %s", got.IssuedAt, got.PaidThrough, claims.IssuedAt, claims.PaidThrough)
	}
	if got.GraceDays != claims.GraceDays || got.RenewalMode != claims.RenewalMode || got.RenewBeforeDays != claims.RenewBeforeDays {
		t.Errorf("the minted key carries grace %d, renewal %s, lead %d; want %d, %s, %d", got.GraceDays, got.RenewalMode, got.RenewBeforeDays, claims.GraceDays, claims.RenewalMode, claims.RenewBeforeDays)
	}
	if got.CheckInterval == nil || *got.CheckInterval != *claims.CheckInterval {
		t.Errorf("the minted key's check interval is %v, want %q", got.CheckInterval, *claims.CheckInterval)
	}
	if got.RenewalSecretHash != claims.RenewalSecretHash || got.Note != claims.Note {
		t.Errorf("the minted key carries the hash %q and the note %q, want %q and %q", got.RenewalSecretHash, got.Note, claims.RenewalSecretHash, claims.Note)
	}
	if !reflect.DeepEqual(got.Warnings, claims.Warnings) {
		t.Errorf("the minted key warns %+v, want %+v", got.Warnings, claims.Warnings)
	}

	if len(signer.seen) != 1 {
		t.Fatalf("the signer was asked to sign %d times, want 1: one key text is one signature, and the tool has no other route to one (K1.3, \"never a local private key file\")", len(signer.seen))
	}
	payload, signature := partsOf(t, key)
	if !bytes.Equal(payload, signer.seen[0]) {
		t.Errorf("the key carries %q and the signer signed %q: the tool signs the bytes it hands out, or a verifier reads claims no signature covers", payload, signer.seen[0])
	}
	if want := ed25519.Sign(signer.priv, payload); !bytes.Equal(signature, want) {
		t.Errorf("the key's signature is not the one the signer answered: %x, want %x", signature, want)
	}
}

// TestMintRefusesAKeyTheVerifierRefuses holds the tool to answering nothing the
// verifier would refuse: the format's own rules -- the tier, the renewal mode,
// the kind, the check interval, the workspace -- are the verifier's and are not
// restated here, so what the tool answers for a claim set outside them is the
// verifier's refusal and no key. Every one of them spends the signature before
// it is caught, which is the price of keeping the rules in one place and is
// held here rather than assumed.
func TestMintRefusesAKeyTheVerifierRefuses(t *testing.T) {
	for _, tc := range []struct {
		name      string
		claims    func() licence.Claims
		workspace string
		want      error
	}{
		{
			"a term past the tier's maximum",
			func() licence.Claims {
				claims := teamClaims()
				claims.PaidThrough = claims.IssuedAt.Add((365 + 30 + 1) * 24 * time.Hour)
				return claims
			},
			"", licence.ErrLifetime,
		},
		{
			"a tier the verifier does not know",
			func() licence.Claims { claims := teamClaims(); claims.Tier = "platinum"; return claims },
			"", licence.ErrClaims,
		},
		{
			"a renewal mode the verifier does not know",
			func() licence.Claims { claims := teamClaims(); claims.RenewalMode = "sometimes"; return claims },
			"", licence.ErrClaims,
		},
		{
			"a kind the verifier does not know",
			func() licence.Claims { claims := teamClaims(); claims.Kind = "gift"; return claims },
			"", licence.ErrClaims,
		},
		{
			"a check interval that is not a duration the verifier reads",
			func() licence.Claims {
				claims := teamClaims()
				month := "P1M"
				claims.CheckInterval = &month
				return claims
			},
			"", licence.ErrDuration,
		},
		{
			"a key that carries no workspace id",
			func() licence.Claims { claims := teamClaims(); claims.WorkspaceID = ""; return claims },
			testWorkspace, licence.ErrClaims,
		},
		{
			"a key bound to another workspace",
			teamClaims,
			"another-firebase-project", licence.ErrWorkspace,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := tc.claims()
			workspace := tc.workspace
			if workspace == "" {
				workspace = claims.WorkspaceID
			}
			signer := newFakeSigner(t)
			key, err := Mint(context.Background(), claims, signer, verifyWith(ringOf(t, signer), workspace))
			if !errors.Is(err, tc.want) {
				t.Fatalf("a claim set with %s answered %v, want %v", tc.name, err, tc.want)
			}
			if key != "" {
				t.Errorf("the tool answered %q beside the refusal: nothing but a key a verifier accepts may reach the owner's stdout", key)
			}
			if len(signer.seen) != 1 {
				t.Errorf("the signer was asked to sign %d payload(s), want 1: the claim set is signed before it is checked, so this refusal costs one signature", len(signer.seen))
			}
		})
	}
}

// TestMintRefusesAKeyThatHasAlreadyEnded is the tool's own rule where the
// verifier's cannot reach: a claim set whose `paid_through` is before its own
// `issued_at` is signed into a key the verifier accepts -- a key in grace is a
// key a verifier answers for, not a refusal, and its bound only fires on a term
// longer than the tier's -- so the state is held beside the error and nothing is
// answered.
func TestMintRefusesAKeyThatHasAlreadyEnded(t *testing.T) {
	claims := teamClaims()
	claims.PaidThrough = claims.IssuedAt.Add(-time.Second)

	signer := newFakeSigner(t)
	ring := ringOf(t, signer)
	key, refusal := Mint(context.Background(), claims, signer, verifyWith(ring, testWorkspace))
	if refusal == nil {
		t.Fatalf("a claim set that ends a second before it starts was minted as %q, want a refusal: the owner would store a key that is already dead", key)
	}
	if key != "" {
		t.Errorf("the tool answered %q beside the refusal", key)
	}
	if errors.Is(refusal, licence.ErrLifetime) {
		t.Errorf("the refusal is %v, which is the verifier's length bound: an ended key is not a term the verifier bounds, which is why the state is checked too", refusal)
	}

	// What the verifier itself makes of the very key the tool built: it accepts
	// it, in a state that is not active. That is the whole reason the state is
	// held -- on this claim set there is no error for the check above to catch.
	if len(signer.seen) != 1 {
		t.Fatalf("the signer was asked to sign %d times, want 1: the claim set is signed before it is checked", len(signer.seen))
	}
	payload := signer.seen[0]
	built := keyText(payload, ed25519.Sign(signer.priv, payload))
	_, state, err := licence.Verify([]byte(built), claims.IssuedAt, ring, testWorkspace)
	if err != nil {
		t.Fatalf("the verifier refused that key at its own issued instant, so this test is not about the state at all: %v", err)
	}
	if state == licence.Active {
		t.Fatalf("the verifier reads %s for that key at its own issued instant, so there is nothing for the tool to catch", state)
	}
	if !strings.Contains(refusal.Error(), string(state)) {
		t.Errorf("the refusal is %v, and it does not name the %s state it caught", refusal, state)
	}
}

// runArgs is the command line one v1 mint takes: every claim of the format as
// a flag, the fake signer's public half in the spelling -public-key carries, and
// what a test adds last, so a flag named twice is the test's.
func runArgs(f *fakeSigner, extra ...string) []string {
	args := []string{
		"-kid", testKeyID,
		"-public-key", base64.RawURLEncoding.EncodeToString(f.public()),
		"-key-id", "lk_mint_test_0001",
		"-workspace-id", testWorkspace,
		"-organisation", "example.com",
		"-tier", licence.TierTeam,
		"-seats", "3",
		"-issued-at", testNow.Format(time.RFC3339),
		"-days", "365",
		"-kind", licence.KindPaid,
		"-renewal-mode", licence.RenewalAuto,
		"-renew-before-days", "7",
		"-renewal-secret-hash", "a-hash-of-a-renewal-secret-and-not-the-secret",
		"-check-interval", "P1D",
		"-warning", "admin:grace_start",
		"-project", "a-project",
		"-location", "a-location",
		"-keyring", "a-key-ring",
		"-key", "a-key",
		"-version", "1",
	}
	return append(args, extra...)
}

// TestRunMintsAKeyTheVerifierAccepts drives the whole v1 command -- the flags,
// the claim set they build, the key text -- with the KMS seam replaced: stdout
// holds one line, that line is an `fgk_` key K1.1's verifier accepts, and it
// carries every claim the flags named.
func TestRunMintsAKeyTheVerifierAccepts(t *testing.T) {
	signer := newFakeSigner(t)
	var out bytes.Buffer
	if err := run(runArgs(signer), &out, signingRunner(signer)); err != nil {
		t.Fatalf("minting through the command: %v", err)
	}
	key := strings.TrimSpace(out.String())
	if out.String() != key+"\n" {
		t.Fatalf("the command printed %q, want the key text and one newline: nothing else reaches the owner's stdout", out.String())
	}
	if !strings.HasPrefix(key, "fgk_") {
		t.Fatalf("the command printed %q, which is not a v1 key text: the format leads with fgk_", key)
	}

	claims, state, err := licence.Verify([]byte(key), testNow, ringOf(t, signer), testWorkspace)
	if err != nil {
		t.Fatalf("the verifier refused the key the command printed: %v", err)
	}
	if state != licence.Active {
		t.Errorf("the key the command printed reads %s at its own issue instant, want %s", state, licence.Active)
	}
	if claims.KID != testKeyID || claims.KeyID != "lk_mint_test_0001" || claims.WorkspaceID != testWorkspace {
		t.Errorf("the key is %q's, bound to %q and named %q, want %q, %q and lk_mint_test_0001", claims.KID, claims.WorkspaceID, claims.KeyID, testKeyID, testWorkspace)
	}
	if claims.Organisation != "example.com" || claims.Tier != licence.TierTeam || claims.Seats != 3 || claims.Kind != licence.KindPaid {
		t.Errorf("the key carries %+v, want the organisation, tier, seat count and kind the flags asked for", claims)
	}
	if claims.GraceDays != 30 {
		t.Errorf("the key's grace is %d days, want the 30 -grace-days defaults to (D2133)", claims.GraceDays)
	}
	if claims.RenewalMode != licence.RenewalAuto || claims.RenewBeforeDays != 7 {
		t.Errorf("the key renews %s with a lead of %d days, want auto and 7", claims.RenewalMode, claims.RenewBeforeDays)
	}
	if claims.CheckInterval == nil || *claims.CheckInterval != "P1D" {
		t.Errorf("the key's check interval is %v, want the P1D -check-interval asked for", claims.CheckInterval)
	}
	if claims.RenewalSecretHash != "a-hash-of-a-renewal-secret-and-not-the-secret" {
		t.Errorf("the key's renewal secret hash is %q, want the one -renewal-secret-hash was given", claims.RenewalSecretHash)
	}
	if !reflect.DeepEqual(claims.Warnings, []licence.WarningV1{{Roles: []string{"admin"}, From: "grace_start"}}) {
		t.Errorf("the key warns %+v, want the one -warning window", claims.Warnings)
	}
	if term := claims.PaidThrough.Sub(claims.IssuedAt); term != 365*24*time.Hour {
		t.Errorf("the key's term is %s, want the 365 days -days asked for", term)
	}
	if !claims.IssuedAt.Equal(testNow) {
		t.Errorf("the key was issued at %s, want the stamp -issued-at was given (%s)", claims.IssuedAt, testNow)
	}
	if len(signer.seen) != 1 {
		t.Errorf("the signer was asked to sign %d times, want 1", len(signer.seen))
	}
}

// TestRunMintsAKeyWithNoCheckInterval holds the one v1 claim a flag can leave
// out of the payload rather than blank: `check_interval` is a duration or null,
// and `null` is how a key says it never checks (F16). An empty -check-interval
// is that null, and the rest of the claim set is written as usual.
func TestRunMintsAKeyWithNoCheckInterval(t *testing.T) {
	signer := newFakeSigner(t)
	var out bytes.Buffer
	if err := run(runArgs(signer, "-check-interval", "", "-warning", "admin:grace_start+7d"), &out, signingRunner(signer)); err != nil {
		t.Fatalf("minting through the command: %v", err)
	}

	claims, _, err := licence.Verify([]byte(strings.TrimSpace(out.String())), testNow, ringOf(t, signer), testWorkspace)
	if err != nil {
		t.Fatalf("the verifier refused the key the command printed: %v", err)
	}
	if claims.CheckInterval != nil {
		t.Errorf("the key's check interval is %q, want null for the empty -check-interval", *claims.CheckInterval)
	}
	if len(claims.Warnings) != 2 {
		t.Errorf("the key carries %d warning window(s), want the two -warning flags: a repeated flag is one more window", len(claims.Warnings))
	}
	if last := claims.Warnings[len(claims.Warnings)-1]; !reflect.DeepEqual(last.Roles, []string{"admin"}) || last.From != "grace_start+7d" {
		t.Errorf("the last warning window is %+v, want admin at grace_start+7d", last)
	}
}

// TestRunTrimsTheRenewalSecretHash holds the paste: the hash is a copy of a
// string out of the owner's store, the server compares it with the one it holds
// character for character (D2135), and it is signed in and cannot be corrected
// afterwards -- so the whitespace a copy brings is the same failure as an empty
// flag, a key that can never renew. What is signed is the trimmed text.
func TestRunTrimsTheRenewalSecretHash(t *testing.T) {
	signer := newFakeSigner(t)
	var out bytes.Buffer
	if err := run(runArgs(signer, "-renewal-secret-hash", "  a-hash-of-a-renewal-secret-and-not-the-secret\n"), &out, signingRunner(signer)); err != nil {
		t.Fatalf("minting through the command: %v", err)
	}

	claims, _, err := licence.Verify([]byte(strings.TrimSpace(out.String())), testNow, ringOf(t, signer), testWorkspace)
	if err != nil {
		t.Fatalf("the verifier refused the key the command printed: %v", err)
	}
	if claims.RenewalSecretHash != "a-hash-of-a-renewal-secret-and-not-the-secret" {
		t.Errorf("the key's renewal secret hash is %q, want the text without the whitespace the flag was pasted with: the server compares that hash with the one it holds", claims.RenewalSecretHash)
	}
}

// TestRunRefusesWhatItCannotMint holds the v1 command to answering nothing it
// cannot mint: a flag it needs, a word that is not one, a seat count or a term
// the design never admits, a term the arithmetic cannot count, a count of days
// that cannot mean what it says, a renewing key with no secret hash, a stamp and
// a warning window that are not what they say, a public half that is not a key,
// a term that has run out by the time the key is minted, and a term the verifier
// bounds. What the owner sees is the refusal, and stdout stays empty -- except
// that a term the verifier bounds is refused after the signature, which is the
// one discarded signature the tool pays for keeping the ceiling in the verifier.
func TestRunRefusesWhatItCannotMint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      func(*fakeSigner) []string
		wantErr   error
		noSigning bool
	}{
		{"a flag that is missing", func(*fakeSigner) []string { return nil }, nil, true},
		{"one claim flag of them all", func(f *fakeSigner) []string { return runArgs(f, "-workspace-id", "") }, nil, true},
		// A word that is not a flag stops flag.Parse, and every flag written
		// after it is dropped in silence: the workspace id and the kind here are
		// claims the owner asked for and a key that does not carry them. The
		// command takes no positional argument once the format's word is read, so
		// any word is a mistake.
		{"a word that is not a flag", func(f *fakeSigner) []string { return runArgs(f, "stray", "-seats", "50") }, nil, true},
		// An asymmetric key has no primary version, so there is nothing for the
		// signer to fall back to and gcloud refuses the run: the flag is
		// required rather than defaulted.
		{"no key version", func(f *fakeSigner) []string { return runArgs(f, "-version", "") }, nil, true},
		// -seats is the one claim-set flag whose default is a value the design
		// never admits, so a mint that left it out claims it in silence: seats
		// are what the organisation bought (docs/licensing/licence-key.md, the
		// claim set's table) and an individual is an organisation of one (T15),
		// so no key anyone means to mint says zero -- and the console draws the
		// number it reads, so a zero is drawn rather than caught.
		{"a key that claims no seats", func(f *fakeSigner) []string { return runArgs(f, "-seats", "0") }, nil, true},
		{"a key with a negative seat count", func(f *fakeSigner) []string { return runArgs(f, "-seats", "-1") }, nil, true},
		{"a term of no days", func(f *fakeSigner) []string { return runArgs(f, "-days", "0") }, nil, true},
		{"a term that wraps to a negative one", func(f *fakeSigner) []string { return runArgs(f, "-days", "200000") }, nil, true},
		// Wrapping round is not the same as wrapping negative: 213504 days land
		// on a positive quarter of an hour, which the verifier accepts and the
		// ended-key guard below passes, so this guard is the only one that can
		// refuse it -- which is why the row mints at the tool's own clock
		// (-issued-at blank). A stamp in the past would put the wrapped term's
		// end before now, and the ended-key guard would refuse the claim set
		// whatever this guard did, leaving this one pinned by nothing.
		{"a term that wraps to a short one", func(f *fakeSigner) []string {
			return runArgs(f, "-issued-at", "", "-days", "213504")
		}, nil, true},
		// A negative grace or renewal lead is a sign in the wrong place: the key
		// would carry a window nobody asked for, and the verifier places the
		// number rather than bounding it, so nothing past this guard catches it.
		{"a grace of a negative number of days", func(f *fakeSigner) []string { return runArgs(f, "-grace-days", "-1") }, nil, true},
		{"a renewal lead of a negative number of days", func(f *fakeSigner) []string { return runArgs(f, "-renew-before-days", "-1") }, nil, true},
		// The renewal secret's hash is signed into the key, so a renewing key
		// minted without one can never renew: the server compares the hash it
		// holds with the one in the key (D2135), and the key cannot be amended.
		{"a renewing key with no renewal secret hash", func(f *fakeSigner) []string { return runArgs(f, "-renewal-secret-hash", "") }, nil, true},
		{"a stamp that is not a time", func(f *fakeSigner) []string { return runArgs(f, "-issued-at", "yesterday") }, nil, true},
		// -issued-at moves the instant the state check is made. This claim set
		// reads active at a stamp in 2020 because it is read there, and it has
		// been over since 2020-01-31: the verifier reports that as a state and
		// not as an error, so nothing past this guard catches the dead key the
		// owner would otherwise store.
		{"a term that has already run out", func(f *fakeSigner) []string {
			return runArgs(f, "-issued-at", "2020-01-01T00:00:00Z", "-days", "30")
		}, nil, true},
		{"a warning window with no colon", func(f *fakeSigner) []string { return runArgs(f, "-warning", "admin") }, nil, true},
		{"a warning window that reaches nobody", func(f *fakeSigner) []string { return runArgs(f, "-warning", ":grace_start") }, nil, true},
		{"a public half that is not a key", func(f *fakeSigner) []string { return runArgs(f, "-public-key", "AA") }, nil, true},
		{"a term past the tier's maximum", func(f *fakeSigner) []string { return runArgs(f, "-days", "400") }, licence.ErrLifetime, false},
		// The interval is the verifier's to read (F16) and not the tool's, so a
		// form it does not read is refused after the signature like any other of
		// its rules.
		{"a check interval that is not a duration", func(f *fakeSigner) []string { return runArgs(f, "-check-interval", "P1M") }, licence.ErrDuration, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signer := newFakeSigner(t)
			var out bytes.Buffer
			err := run(tc.args(signer), &out, signingRunner(signer))
			if err == nil {
				t.Fatalf("the command answered %q and no refusal", out.String())
			}
			if out.Len() != 0 {
				t.Errorf("the command printed %q beside the refusal: nothing but a key may reach the owner's stdout", out.String())
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("the refusal is %v, want %v", err, tc.wantErr)
			}
			if tc.noSigning && len(signer.seen) != 0 {
				t.Errorf("the signer was asked to sign %d payload(s) before the refusal: %v is a thing the tool can see before it spends a signature", len(signer.seen), tc.name)
			}
		})
	}
}

// TestRunNamesTheTermGuardThatRefused holds the two rows above whose refusal
// is not the guard the row is aimed at: a term of no days and a term past what
// the arithmetic counts both leave a `paid_through` that is not after now, so
// the ended-key guard refuses them as well and the row passes whether or not the
// guard it names is there. The refusal's text is the one thing that tells the
// two apart, and only a test reads it.
func TestRunNamesTheTermGuardThatRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(*fakeSigner) []string
		want string
	}{
		{"a term of no days", func(f *fakeSigner) []string { return runArgs(f, "-days", "0") }, "must be a positive number of days"},
		{"a term past what the arithmetic counts", func(f *fakeSigner) []string { return runArgs(f, "-days", "200000") }, "cannot be counted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signer := newFakeSigner(t)
			err := run(tc.args(signer), io.Discard, signingRunner(signer))
			if err == nil {
				t.Fatalf("%s was minted, want the refusal of the guard that reads the days", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %v, and it does not carry %q: the ended-key guard refusing it instead would leave the guard that reads the days unpinned", err, tc.want)
			}
			if len(signer.seen) != 0 {
				t.Errorf("the signer was asked to sign %d payload(s) before the refusal", len(signer.seen))
			}
		})
	}
}
