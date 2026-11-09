// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Command mint is the owner's hand-minting tool (subscriptions plan S2.10,
// design docs/design/key-signing-service.md §11 step 2): it turns one claim set
// into one licence key, signing through a KMS key's signing API, for the
// interim in which the issuing service does not exist yet (T13).
//
// **Use is OWNER-RUN.** The owner runs it on their own machine, with their own
// credentials, after S3.8 creates the signing keys. No agent holds or reaches a
// signing key, deploys or operates anything, and nothing in the core, the
// console, the app or a `fugaro` command calls this program. Run it from the
// repository root:
//
//	go run ./ee/licence/mint -key-id ID -public-key PUBLIC -tier team \
//	  -organisation example.com -seats 3 -days 365 \
//	  -project P -location L -keyring R -key K -algorithm A
//
// Four rules give it its shape:
//
//   - **It has no way to sign but the signer.** A signature comes from the
//     Signer it is handed, a KMS key's signing API, and from no local private
//     key file: the package's own sources name no signing operation and no
//     private half at all, which TestTheToolHoldsNoWayToSignButTheSigner reads.
//   - **The payload is signed as it is spelled.** The claim set is marshalled
//     once and those bytes are what is signed and what the key carries, so a
//     verifier reads the bytes the signature covers (docs/licensing/
//     licence-key.md, "Raw bytes before claims").
//   - **Nothing is answered that a verifier refuses.** The key is checked
//     against S2.3's verifier, over the ring the owner names, before anything
//     reaches stdout: a term past the tier's maximum, a tier the verifier does
//     not know and an organisation the key does not bind to are refused there.
//     The rule is the verifier's and is not restated here, which is what keeps
//     a hand-minted key and the service's one format. The key must also read
//     `valid` at its own `issued`, which is a rule of the tool's: a claim set
//     whose `expires` is before its `issued` is a key that has already ended,
//     and a verifier accepts it -- as an expired key, which is what an expired
//     key is -- so nothing but this check would catch it.
//   - **The ring is the owner's, not the build's.** The compiled keyring is
//     empty until S2.9 publishes the owner's production half, and the interim
//     this tool exists for starts at S3.8, so the tool checks against the public
//     half named on the command line.
//
// Nothing is written anywhere but stdout: the key text is one line on stdout,
// for the owner to store where the design says a key is kept.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, execRunner); err != nil {
		fmt.Fprintln(os.Stderr, "mint:", err)
		os.Exit(1)
	}
}

// maxDays is the longest term the arithmetic holds: a duration this package
// counts in nanoseconds, where one day is 86400e9 of them. It is a bound on what
// the machine can count and not on what a tier may be given, which is the
// verifier's rule, and it is far above any term anyone mints.
const maxDays = math.MaxInt64 / int64(24*time.Hour)

// run is main's whole body, so the flags, the claim set they build and the one
// line that is printed are one function a reader can follow. The runner is the
// KMS seam, so the whole command is a thing a test drives: a test's runner signs
// what gcloud would have been handed and no binary is run.
func run(args []string, out io.Writer, runner Runner) error {
	flags := flag.NewFlagSet("mint", flag.ContinueOnError)
	keyID := flags.String("key-id", "", "the key id the key text leads with and a verifier places the key by")
	publicKey := flags.String("public-key", "", "the signing key's public half, unpadded base64url of the 32 bytes a keyring entry carries")
	tier := flags.String("tier", "", "the tier the key is issued for: community, team or enterprise")
	organisation := flags.String("organisation", "", "the organisation the key binds to: a domain, an individual as an organisation of one, or an Order Form id")
	seats := flags.Int("seats", 0, "the seats the organisation has: compared and shown, never enforced (D439)")
	features := flags.String("features", "", "the paid features the key turns on, comma-separated")
	days := flags.Int("days", 0, "the term the key covers, in days from today: a renewal covers the term that begins where its predecessor ends, so it passes that term plus the 30 days the predecessor still has")
	project := flags.String("project", "", "the GCP project the signing key lives in")
	location := flags.String("location", "", "the signing key ring's location")
	keyRing := flags.String("keyring", "", "the signing key ring's name")
	key := flags.String("key", "", "the signing key's name")
	version := flags.String("version", "", "the signing key's version; empty is the key's primary version")
	algorithm := flags.String("algorithm", "", "the signing algorithm the key was created with, as `gcloud kms asymmetric-sign` names it (U9: what a managed store offers is unverified until S3.5)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"key-id", *keyID},
		{"public-key", *publicKey},
		{"tier", *tier},
		{"organisation", *organisation},
		{"project", *project},
		{"location", *location},
		{"keyring", *keyRing},
		{"key", *key},
		{"algorithm", *algorithm},
	} {
		if required.value == "" {
			missing = append(missing, "-"+required.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s: a key text is an id, a claim set, and a signature over it through a KMS key's signing API", strings.Join(missing, ", "))
	}
	// The tool's one input rule, and it is about the claim set's sense rather
	// than its length: a term of no days leaves a key that has already ended,
	// which the verifier accepts -- its rule bounds a key's length -- so the
	// tool is the only place it can be caught.
	if *days <= 0 {
		return errors.New("-days must be a positive number of days: a key that ends before it starts is not one anyone means to mint")
	}
	// The term is counted in nanoseconds, and past maxDays the product no longer
	// fits an int64. The overflow is not always negative: 213504 days wraps to a
	// positive quarter of an hour, which would mint a key with a 1526-second term
	// -- inside the verifier's length bound, valid at its own `issued`, and
	// nothing for any check after this one to catch. So the bound is on the days
	// and not on the product. It is arithmetic and not the tier's: what a tier
	// may be given stays the verifier's rule to state.
	if int64(*days) > maxDays {
		return fmt.Errorf("-days is %d, and a term that long cannot be counted: the longest term this tool can hold is %d days, and the verifier is where the ceiling anyone mints under is stated", *days, maxDays)
	}
	term := time.Duration(*days) * 24 * time.Hour

	ring, err := ringFromPublicKey(*keyID, *publicKey)
	if err != nil {
		return err
	}
	now := time.Now()
	keyText, err := Mint(context.Background(), *keyID, licence.Claims{
		Tier:         *tier,
		Features:     featureList(*features),
		Seats:        *seats,
		Organisation: *organisation,
		Issued:       now.Unix(),
		Expires:      now.Add(term).Unix(),
	}, KMS{
		Project:   *project,
		Location:  *location,
		KeyRing:   *keyRing,
		Key:       *key,
		Version:   *version,
		Algorithm: *algorithm,
		Run:       runner,
	}, verifier(ring))
	if err != nil {
		return err
	}
	fmt.Fprintln(out, keyText)
	return nil
}

// ringFromPublicKey reads the public half the owner names, in the spelling a
// keyring entry carries: the 32 raw bytes of an Ed25519 public key, base64url
// and unpadded (ee/licence/testdata/vectors.json's keyring). S2.9 publishes the
// owner's production half in this spelling, so what the tool checks against and
// what a verifier holds are one text of one key.
func ringFromPublicKey(keyID, text string) (licence.Keyring, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, fmt.Errorf("the public half is not unpadded base64url: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the public half is %d bytes, and an Ed25519 public key's is %d", len(raw), ed25519.PublicKeySize)
	}
	return licence.Keyring{keyID: ed25519.PublicKey(raw)}, nil
}

// verifier is the check the tool holds what it minted to: the verifier's own
// rule, over the ring the owner named.
func verifier(ring licence.Keyring) Verifier {
	return func(raw []byte, now time.Time) (licence.Claims, licence.State, error) {
		return licence.VerifyAgainst(raw, now, ring)
	}
}

// featureList reads the comma-separated features. A key that turns on no paid
// feature says so as an empty list rather than as a claim that is missing.
func featureList(text string) []string {
	list := []string{}
	for _, name := range strings.Split(text, ",") {
		if name = strings.TrimSpace(name); name != "" {
			list = append(list, name)
		}
	}
	return list
}
