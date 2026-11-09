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
// because the scan's own reach is a thing to hold too: TestTheScanRefusesADotImport
// parses a source it would accept and holds this to refusing it.
//
// Three things are refused: an import of a crypto package that is not
// crypto/ed25519; a dot-import of that one, whose names are bare identifiers and
// so are not selectors this scan can follow; and any of the symbols that sign,
// hold or derive a private half, named through the binding the file gives the
// package -- its own name or an alias, so a renamed import is caught.
func signingRoutes(file *ast.File) []string {
	// The symbols that would sign, hold or derive a private half. Signing a
	// payload and reading a private key out of a file both need one of these,
	// and ed25519.PrivateKey is also what a conversion from raw bytes names.
	forbidden := []string{"Sign", "SignTo", "PrivateKey", "GenerateKey", "NewKeyFromSeed"}

	var problems []string
	local := localEd25519Name(file)
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if strings.HasPrefix(path, "crypto/") && path != ed25519Path {
			problems = append(problems, fmt.Sprintf("imports %s: the tool's only route to a signature is the Signer it is handed, so a second crypto package is a second route", path))
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

// TestTheScanRefusesADotImport holds the scan's own reach, in both directions: a
// dot-import of crypto/ed25519 makes every name in it a bare identifier, which
// is not an *ast.SelectorExpr and therefore nothing the scan above can follow,
// so the import is refused rather than trusted -- and the imports the tool may
// hold are not refused, so the rule is not simply "refuse more".
//
// Each source is parsed from a string: a fixture file of this package would be a
// file the scan reads, and one of these must never exist here.
func TestTheScanRefusesADotImport(t *testing.T) {
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
				t.Errorf("%q holds no problem, want a refusal: a dot-imported name is a bare identifier, so the scan would read straight past it", tc.source)
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

// TestMintRefusesAKeyThatHasAlreadyEnded is the tool's own rule where the
// verifier's cannot reach: a claim set whose `expires` is before its `issued` is
// signed into a key the verifier accepts -- an expired or a grace key is a key a
// verifier answers for, not a refusal, and its bound only fires on a term longer
// than the tier's -- so the state is held beside the error and nothing is
// answered.
func TestMintRefusesAKeyThatHasAlreadyEnded(t *testing.T) {
	claims := teamClaims()
	claims.Expires = claims.Issued - 1

	signer := newFakeSigner(t)
	ring := ringOf(t, signer)
	key, refusal := Mint(context.Background(), testKeyID, claims, signer, verifyWith(ring))
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
	// it, in a state that is not valid. That is the whole reason the state is
	// held -- on this claim set there is no error for the check above to catch.
	if len(signer.seen) != 1 {
		t.Fatalf("the signer was asked to sign %d times, want 1: the claim set is signed before it is checked", len(signer.seen))
	}
	payload := signer.seen[0]
	built := keyText(testKeyID, payload, ed25519.Sign(signer.priv, payload))
	_, state, err := licence.VerifyAgainst([]byte(built), time.Unix(claims.Issued, 0), ring)
	if err != nil {
		t.Fatalf("the verifier refused that key at its own issued instant, so this test is not about the state at all: %v", err)
	}
	if state == licence.Valid {
		t.Fatalf("the verifier reads %s for that key at its own issued instant, so there is nothing for the tool to catch", state)
	}
	if !strings.Contains(refusal.Error(), string(state)) {
		t.Errorf("the refusal is %v, and it does not name the %s state it caught", refusal, state)
	}
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

// runArgs is the command line one mint takes, with the fake signer's public half
// in the spelling -public-key carries, plus what a test adds: a flag named twice
// is the last one, so a test overrides -days by naming it again.
func runArgs(f *fakeSigner, extra ...string) []string {
	args := []string{
		"-key-id", testKeyID,
		"-public-key", base64.RawURLEncoding.EncodeToString(f.public()),
		"-tier", licence.TierTeam,
		"-organisation", "example.com",
		"-seats", "3",
		"-days", "365",
		"-project", "a-project",
		"-location", "a-location",
		"-keyring", "a-key-ring",
		"-key", "a-key",
		"-version", "1",
	}
	return append(args, extra...)
}

// TestRunMintsAKeyTheVerifierAccepts drives the whole command -- the flags, the
// claim set they build, the key text -- with the KMS seam replaced: stdout holds
// one line and that line is a key S2.3's verifier accepts, with the terms the
// flags asked for.
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

	claims, state, err := licence.VerifyAgainst([]byte(key), time.Now(), ringOf(t, signer))
	if err != nil {
		t.Fatalf("the verifier refused the key the command printed: %v", err)
	}
	if state != licence.Valid {
		t.Errorf("the key the command printed reads %s today, want %s", state, licence.Valid)
	}
	if claims.Tier != licence.TierTeam || claims.Organisation != "example.com" || claims.Seats != 3 {
		t.Errorf("the key carries %+v, want the tier, the organisation and the seats the flags asked for", claims)
	}
	if term := claims.Expires - claims.Issued; term != int64(365*24*time.Hour/time.Second) {
		t.Errorf("the key's term is %d seconds, want the 365 days -days asked for", term)
	}
	if id := string(partOf(t, []byte(key), 0)); id != testKeyID {
		t.Errorf("the key text leads with %q, want %q", id, testKeyID)
	}
	if len(signer.seen) != 1 {
		t.Errorf("the signer was asked to sign %d times, want 1", len(signer.seen))
	}
}

// TestRunRefusesWhatItCannotMint holds the command to answering nothing it
// cannot mint: a flag it needs, a term of no days, a term the arithmetic cannot
// count, a term past the tier's maximum, and a public half that is not a key.
// What the owner sees is the refusal, and stdout stays empty -- except that a
// term the verifier bounds is refused after the signature, which is the one
// discarded signature the tool pays for keeping the ceiling in the verifier.
func TestRunRefusesWhatItCannotMint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      func(*fakeSigner) []string
		wantErr   error
		noSigning bool
	}{
		{"a flag that is missing", func(*fakeSigner) []string { return nil }, nil, true},
		// An asymmetric key has no primary version, so there is nothing for the
		// signer to fall back to and gcloud refuses the run: the flag is
		// required rather than defaulted.
		{"no key version", func(f *fakeSigner) []string { return runArgs(f, "-version", "") }, nil, true},
		{"a term of no days", func(f *fakeSigner) []string { return runArgs(f, "-days", "0") }, nil, true},
		{"a term that wraps to a negative one", func(f *fakeSigner) []string { return runArgs(f, "-days", "200000") }, nil, true},
		// Wrapping round is not the same as wrapping negative: 213504 days land
		// on a positive quarter of an hour, which the verifier accepts and the
		// state check passes, so nothing past this guard can catch it.
		{"a term that wraps to a short one", func(f *fakeSigner) []string { return runArgs(f, "-days", "213504") }, nil, true},
		{"a term that wraps to a 24-hour one", func(f *fakeSigner) []string { return runArgs(f, "-days", "213505") }, nil, true},
		{"a term past the tier's maximum", func(f *fakeSigner) []string { return runArgs(f, "-days", "400") }, licence.ErrLifetime, false},
		{"a public half that is not a key", func(f *fakeSigner) []string { return runArgs(f, "-public-key", "AA") }, nil, true},
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
