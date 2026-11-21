package localcfg

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
)

// sealKeyPublicKey is a 2048-bit RSA public key in the one form Cloud KMS
// publishes a version's public key in, which is what the compute root's output
// carries into the block. It is a PUBLIC key and carries no secret: it exists
// so that a fixture can name a key fact the parser accepts, and it is
// deliberately not any real installation's.
const sealKeyPublicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0Fv8ySELukpawETOn/JA
erEjF78WwHFXQbsRxNPGJcbW5FmvKOO6xHvIxxP1JqZay7gStNr1HJ0S3dnXH0Q9
tpChSqB+YLI/CFqD8szA972a/T3aTZ2UZJplxhU7k6+M9jp774fqiiKyOamBVqnI
vxxuAIyaPgZOG0YGIFc6O0ej/3Wiqj5dG+4sBlnhp+TkS9b9ZKUqvq7eLCazwutg
OCSLNQNpJKegmc646/SQhietJUQNsBHM1lWyHYtLS3E2/dC7R6LK6TnNWqqP5Bbz
7VE5RZ5TsQrIkCo/GlmHPrSnjKJEAgLsRvLwogs+ijZx08tdTwNM5ZYGnixhGZAk
2QIDAQAB
-----END PUBLIC KEY-----
`

// sealKeyVersion is a key version's resource name in the shape the compute
// root outputs.
const sealKeyVersion = "projects/my-project/locations/us-central1/keyRings/fugaro-batch/cryptoKeys/seal-key/cryptoKeyVersions/1"

// keyFact is the two lines `fugaro update` writes for the key fact, at the
// indentation a `batch:` block's own fields carry.
func keyFact(publicKey string) string {
	return "  seal_key:\n    version: " + sealKeyVersion + "\n    public_key: |\n" + indent(publicKey, "      ")
}

// batchWith parses a config whose `batch:` block is the smallest valid one plus
// extra lines the caller wrote at that block's indentation.
func batchWith(extra string) (*Config, error) {
	const block = "  image: us-central1-docker.pkg.dev/my-project/fugaro/fugaro:1\n" +
		"  machine_type: e2-standard-4\n" +
		"  boot_image: projects/my-project/global/images/family/fugaro-base-1\n" +
		"  network: projects/my-project/global/networks/fugaro-runs\n" +
		"  subnetwork: projects/my-project/regions/us-central1/subnetworks/fugaro-runs\n" +
		"  service_account: fugaro-batch@my-project.iam.gserviceaccount.com\n" +
		"  socket_gid: 412\n" +
		"  task_timeout: 6h\n"
	const head = "version: 1\nname: aurora\ngcp_project: my-project\nregion: us-central1\nruns_bucket: my-runs\n"
	return Parse([]byte(head + "batch:\n" + block + extra))
}

// TestABatchBlockWithAKeyFactLoads is C1's acceptance for the block: a `batch:`
// block carrying the opt-in fact and the key fact — what `fugaro update` writes
// — parses, the fact reads back field for field, and the public key is what a
// launcher will wrap to.
func TestABatchBlockWithAKeyFactLoads(t *testing.T) {
	lc, err := batchWith("  opt_in: in\n" + keyFact(sealKeyPublicKey))
	if err != nil {
		t.Fatalf("a block with a key fact did not load: %v", err)
	}
	if lc.Batch == nil {
		t.Fatal("the block was dropped")
	}
	if !lc.Batch.OptedIn() {
		t.Fatalf("the opt-in fact reads %q and the block says %q", lc.Batch.OptIn, BatchOptInOn)
	}
	if lc.Batch.OptInWithdrawn() {
		t.Fatal("an opted-in installation reads as withdrawn")
	}
	if lc.Batch.SealKey == nil {
		t.Fatal("the key fact was dropped")
	}
	if lc.Batch.SealKey.Version != sealKeyVersion {
		t.Fatalf("the version reads %q and the block wrote %q", lc.Batch.SealKey.Version, sealKeyVersion)
	}
	key, err := ParseSealKeyPublicKey(lc.Batch.SealKey.PublicKey)
	if err != nil {
		t.Fatalf("the block's own public key does not parse: %v", err)
	}
	if got := key.N.BitLen(); got != 2048 {
		t.Fatalf("the public key is %d bits and the envelope is sized for 2048", got)
	}
}

// TestTheOptInFactHasThreeStates: absent, `in` and `withdrawn` are the three,
// and they are told apart — a withdrawal is a WRITTEN value (R15), so an
// installation that opted out is not an installation that never opted in. A
// fourth spelling is refused where the block is written rather than read as
// one of the three.
func TestTheOptInFactHasThreeStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		extra    string
		optedIn  bool
		withdrew bool
	}{
		{"absent", "", false, false},
		{"in", "  opt_in: in\n", true, false},
		{"withdrawn", "  opt_in: withdrawn\n", false, true},
	} {
		lc, err := batchWith(tc.extra)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := lc.Batch.OptedIn(); got != tc.optedIn {
			t.Fatalf("%s: OptedIn=%v, want %v", tc.name, got, tc.optedIn)
		}
		if got := lc.Batch.OptInWithdrawn(); got != tc.withdrew {
			t.Fatalf("%s: OptInWithdrawn=%v, want %v", tc.name, got, tc.withdrew)
		}
	}
	if _, err := batchWith("  opt_in: yes\n"); err == nil {
		t.Fatal("a fourth spelling of the opt-in fact was accepted")
	}
}

// TestAHalfWrittenKeyFactAndAForeignOneAreRefused: the key fact is one fact in
// two fields, and each field has one spelling. A block that named half of it, a
// version that is not a version's name and a public key that is not a 2048-bit
// RSA key are refused where the block is written — before a launcher wraps
// anything with them — and no refusal repeats the public key's text.
func TestAHalfWrittenKeyFactAndAForeignOneAreRefused(t *testing.T) {
	for _, tc := range []struct{ name, extra, want string }{
		{"version only", "  seal_key:\n    version: " + sealKeyVersion + "\n", "one fact in two fields"},
		{"public key only", "  seal_key:\n    public_key: |\n" + indent(sealKeyPublicKey, "      "), "one fact in two fields"},
		{"empty", "  seal_key: {}\n", "one fact in two fields"},
		{"not a version name", "  seal_key:\n    version: seal-key\n    public_key: |\n" + indent(sealKeyPublicKey, "      "), "not a Cloud KMS key version's resource name"},
		{"not a key", "  seal_key:\n    version: " + sealKeyVersion + "\n    public_key: not-a-key\n", "is not an RSA public key"},
		{"a private key", "  seal_key:\n    version: " + sealKeyVersion + "\n    public_key: |\n      -----BEGIN PRIVATE KEY-----\n      bm90IGEga2V5\n      -----END PRIVATE KEY-----\n", "is not an RSA public key"},
	} {
		_, err := batchWith(tc.extra)
		if err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: refused with %q, which does not say %q", tc.name, err, tc.want)
		}
		if strings.Contains(err.Error(), "MIIBIjANBgkqhkiG9w0BAQEF") {
			t.Fatalf("%s: the refusal repeated the public key: %v", tc.name, err)
		}
	}

	// Two shapes a literal cannot carry honestly: a key of the right algorithm
	// and another size, which the envelope is not sized for and Wrap refuses,
	// and a key of another algorithm entirely. Both are generated here (no key
	// literal, and a private half is never written anywhere), both are refused
	// where the block is written rather than at the launch, and neither is
	// echoed by its refusal.
	smaller, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generating a smaller key: %v", err)
	}
	larger, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatalf("generating a larger key: %v", err)
	}
	foreign, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key of another algorithm: %v", err)
	}
	for _, tc := range []struct{ name, publicKey string }{
		{"a 1024-bit RSA key", publicKeyPEM(t, &smaller.PublicKey)},
		{"a 3072-bit RSA key", publicKeyPEM(t, &larger.PublicKey)},
		{"an ECDSA key", publicKeyPEM(t, &foreign.PublicKey)},
	} {
		_, err := batchWith("  seal_key:\n    version: " + sealKeyVersion + "\n    public_key: |\n" + indent(tc.publicKey, "      "))
		if err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
		// The block's own refusal names the parse's, which is the sentinel every
		// reader of a key fact matches on.
		if !strings.Contains(err.Error(), ErrSealKeyPublicKey.Error()) {
			t.Fatalf("%s: refused with %v, which does not name %v", tc.name, err, ErrSealKeyPublicKey)
		}
		_, parseErr := ParseSealKeyPublicKey(tc.publicKey)
		if !errors.Is(parseErr, ErrSealKeyPublicKey) {
			t.Fatalf("%s: ParseSealKeyPublicKey refused it with %v, which is not ErrSealKeyPublicKey", tc.name, parseErr)
		}
		if body := strings.Split(strings.TrimSpace(tc.publicKey), "\n"); len(body) > 1 && strings.Contains(parseErr.Error(), body[1]) {
			t.Fatalf("%s: the refusal repeated the public key: %v", tc.name, parseErr)
		}
	}
}

// publicKeyPEM is a key's one PEM spelling in the form Cloud KMS publishes a
// version's public key in: a PUBLIC KEY block holding the PKIX
// SubjectPublicKeyInfo, which is what the compute root's output carries into
// the block.
func publicKeyPEM(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("spelling a generated public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// indent indents every non-empty line of s, for a YAML block scalar holding a
// PEM.
func indent(s, prefix string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString(line)
			continue
		}
		b.WriteString(prefix + line)
	}
	return b.String()
}
