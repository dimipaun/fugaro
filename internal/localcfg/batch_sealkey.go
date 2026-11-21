package localcfg

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The two facts the `batch:` block carries about a Cloud Batch installation's
// identity (docs/batch-run-identity/reconciliation.md, R2, R3, R13): the KEY
// FACT, which is the Cloud KMS key version a launcher wraps the seal key to and
// its public key, and the OPT-IN FACT, which is whether this installation has
// asked for the compute root that creates that key.
//
// Both are published with the block so a launcher who is not an operator
// receives them, and neither is written by any settings command: they are the
// installation's facts and not a machine's preference, which is why the
// settings registry lists them with `update` as their owner and with no flag.
// Whose hand writes them is the one thing the two do not share: the opt-in is
// an OPERATOR's, written by selecting the backend (R2) and by opting out (R16),
// and the key fact is `fugaro update`'s alone and by nothing else (R13).
//
// **The key fact is one fact in two fields.** The version names the key the
// blob was wrapped to, so a later version is a different fact and never a
// silent replacement (R9: Fugaro never rotates or destroys a key the run in
// flight holds); the public key is what a launcher encrypts to, and it is the
// only half a launcher ever holds — the private half is a Cloud KMS key no
// Fugaro process can read. A substituted public key would hand its holder every
// seal key wrapped to it (R13), which is why doctor fails a machine whose local
// fact differs from the published one and why nothing here accepts a fact a
// settings command wrote.
//
// **The opt-in fact has three states** and not two (R15, D3110, D3111): absent
// — nobody has opted this installation in, which is what a block written by
// `fugaro setup --provision-batch` on a machine that never chose Batch says —
// `in`, which is what the operator's own command writes — and `withdrawn`,
// which is a WRITTEN value and not an absence, so that an update can tell an
// installation that opted out from one that never opted in.

// The opt-in fact's two written states. Anything else — including an empty
// string — is refused by validateBatch rather than read as "absent", because a
// misspelling that meant a third state would be an installation whose root is
// applied for a reason nobody wrote down.
const (
	// BatchOptInOn is the opt-in: this installation's `fugaro update` applies
	// the compute root, creates the key if it does not exist, and records the
	// key fact in the published block (R2).
	BatchOptInOn = "in"
	// BatchOptInWithdrawn is the opt-out (D3111, R16): the root is applied with
	// the grants off, the key and its ring stay, and a registration is refused
	// before a job exists (R17).
	BatchOptInWithdrawn = "withdrawn"
)

// BatchSealKey is the key fact's two fields, as `fugaro update` writes them
// from the compute root's own outputs (R13): the version the blob is wrapped
// to, and the public key it is wrapped with.
//
// It is a pointer on Batch so that an installation whose root has never been
// applied carries no fact at all, which is the state the launch refusal is
// about (R3): the backend wraps where the block names the key and refuses with
// `fugaro update` as the fix where it does not.
type BatchSealKey struct {
	// Version is the Cloud KMS key version's full resource name, as the root
	// outputs it: projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>/
	// cryptoKeyVersions/<v>.
	//
	// The VERSION and not the key: a blob wrapped to one version is opened by
	// that version, so naming the version is what makes R9's "an update changes
	// nothing a run in flight holds" a fact about the block rather than a
	// promise about the update.
	Version string `yaml:"version,omitempty"`
	// PublicKey is the version's public key, PEM-encoded in the SPKI form Cloud
	// KMS and Terraform both emit ("-----BEGIN PUBLIC KEY-----"), which is the
	// form ParseSealKeyPublicKey reads. It is public by construction and is
	// published with the block; it is a public key and not a secret.
	PublicKey string `yaml:"public_key,omitempty"`
}

// kmsKeyVersionRE is what a key version's name may be: the Cloud KMS resource
// name of one version, [A-Za-z0-9_-] in each part (KMS allows upper case in a
// key ring's, a key's and a version's id, unlike a Batch job's name). It is a
// spelling check and not a proof: whether this installation's project holds
// that version is read by doctor and by the service, never by a parse.
var kmsKeyVersionRE = regexp.MustCompile(`^projects/[A-Za-z0-9_-]+/locations/[A-Za-z0-9_-]+/keyRings/[A-Za-z0-9_-]+/cryptoKeys/[A-Za-z0-9_-]+/cryptoKeyVersions/[A-Za-z0-9_-]+$`)

// ErrSealKeyPublicKey means a value is not the public key of the installation's
// KMS key version. Every refusal here is a refusal and never a repair: a
// key this package re-spelled would be a second way to name one key, and the
// bytes a launcher wraps to must be the bytes the service's version holds.
var ErrSealKeyPublicKey = errors.New("batch.seal_key.public_key is not an RSA public key")

// ParseSealKeyPublicKey reads the key fact's public key: one PEM block of type
// PUBLIC KEY holding a SubjectPublicKeyInfo, which is what Cloud KMS's
// asymmetricDecrypt key version publishes and what the compute root's output
// carries.
//
// It refuses, with ErrSealKeyPublicKey, anything else: a certificate, a
// private key, a second block, trailing text, a non-RSA key, and an RSA key
// that is not the 2048-bit modulus the key-release contract's envelope is sized
// for (internal/budget/token's Wrap refuses to wrap to one, and a block that
// named one would be a launch that fails after its VM has started). The
// refusals never repeat the value: a public key is not a secret, but a refusal
// is not the place to print a field's content, and the same rule holds for a
// value that was never a key at all.
func ParseSealKeyPublicKey(pemText string) (*rsa.PublicKey, error) {
	block, rest := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("%w: it must be one PEM block of type PUBLIC KEY", ErrSealKeyPublicKey)
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("%w: the PEM block is of type %q, and the public key of a KMS key version is a PUBLIC KEY", ErrSealKeyPublicKey, block.Type)
	}
	if len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: it must be exactly one PEM block, and there is text after it", ErrSealKeyPublicKey)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: the SubjectPublicKeyInfo does not parse (%v)", ErrSealKeyPublicKey, err)
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: it is a %T, and the key-release contract's envelope is RSA", ErrSealKeyPublicKey, key)
	}
	if n := rsaKey.N.BitLen(); n != 2048 {
		return nil, fmt.Errorf("%w: the modulus is %d bits, and the envelope is sized for the 2048-bit key its algorithm names", ErrSealKeyPublicKey, n)
	}
	return rsaKey, nil
}

// OptedIn reports whether this installation's operator has opted it in
// (D3110 as amended): the published block says `in`. A nil Batch block, an
// absent fact and a withdrawn one are all "not in", which is the answer every
// caller of this method wants — what an update applies, and what a launch is
// refused on, differ only in the sentence they print.
func (b *Batch) OptedIn() bool { return b != nil && b.OptIn == BatchOptInOn }

// OptInWithdrawn reports whether the fact is the WRITTEN withdrawal (R16): an
// installation that opted out is told apart from one that never opted in,
// because R16's launch refusal and R17's deploy input name it.
func (b *Batch) OptInWithdrawn() bool { return b != nil && b.OptIn == BatchOptInWithdrawn }

// validateBatchSealKey holds the block's two identity facts to their spellings
// (see BatchSealKey and the opt-in constants). It is called from validateBatch
// with it, and it checks SPELLINGS only: which key version this installation's
// project holds, and whether the public key matches the version's, are cloud
// facts, and R3's rule is that no launch reads the cloud to decide anything.
func validateBatchSealKey(bad func(string, ...any), b *Batch) {
	switch b.OptIn {
	case "", BatchOptInOn, BatchOptInWithdrawn:
	default:
		bad("batch.opt_in %q must be %q, %q, or left out: the fact has three states and a fourth spelling is one no reader of the published block could answer", b.OptIn, BatchOptInOn, BatchOptInWithdrawn)
	}
	if b.SealKey == nil {
		return
	}
	if b.SealKey.Version == "" || b.SealKey.PublicKey == "" {
		bad("batch.seal_key: the key fact is one fact in two fields, and this block names %d of them (version, public_key): `fugaro update` writes both from the compute root's outputs, and a half-written fact is a launch that cannot wrap", sealKeyFields(b.SealKey))
		return
	}
	if !kmsKeyVersionRE.MatchString(b.SealKey.Version) {
		bad("batch.seal_key.version %q is not a Cloud KMS key version's resource name (projects/<project>/locations/<location>/keyRings/<ring>/cryptoKeys/<key>/cryptoKeyVersions/<version>)", b.SealKey.Version)
	}
	if _, err := ParseSealKeyPublicKey(b.SealKey.PublicKey); err != nil {
		bad("%v (batch.seal_key.public_key, which `fugaro update` writes from the compute root's own output)", err)
	}
}

// sealKeyFields counts the fields of the key fact a block named, for the
// refusal above: a block with neither is a block that wrote an empty seal_key,
// and one with a single field is a half of a fact.
func sealKeyFields(k *BatchSealKey) int {
	n := 0
	if k.Version != "" {
		n++
	}
	if k.PublicKey != "" {
		n++
	}
	return n
}
