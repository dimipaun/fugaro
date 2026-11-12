// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Command mint is the owner's hand-minting tool (licence-key-v2 plan K1.3,
// subscriptions plan S2.10, design docs/design/key-signing-service.md §11 step
// 2): it turns one claim set into one licence key, signing through a KMS key's
// signing API, for the interim in which the issuing service does not exist yet
// (T13).
//
// **Use is OWNER-RUN.** The owner runs it on their own machine, with their own
// credentials, after S3.8 creates the signing keys. No agent holds or reaches a
// signing key, deploys or operates anything, and nothing in the core, the
// console, the app or a `fugaro` command calls this program. Run it from the
// repository root:
//
//	go run ./ee/licence/mint -kid ID -public-key PUBLIC -key-id KEY \
//	  -workspace-id PROJECT -organisation example.com -tier team -seats 3 \
//	  -days 365 -kind paid -renewal-mode auto -renewal-secret-hash HASH \
//	  -project P -location L -keyring R -key K -version V
//
// It writes the **v1** key, the `fgk_` format of K1.1 (D2130), and the claim
// set it mints is the one docs/licensing/licence-key.md fixes. The older
// `<key id>.<payload>.<signature>` format is still mintable behind the one
// leading word `old`, which K7.3 deletes along with its verifier: the path and
// its tests stay until then, because the two formats spell two different things
// `-key-id` and cannot share one flag set.
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
//     against K1.1's verifier, over the ring the owner names, before anything
//     reaches stdout: a tier, a renewal mode or a kind the verifier does not
//     know, a check interval it cannot read, a term past the tier's maximum and
//     a workspace id the verifier will not accept are refused there. The rule is
//     the verifier's and is not restated here, which is what keeps a hand-minted
//     key and the service's one format. Three more rules are the tool's own,
//     because no verifier catches them: what it mints must read `active` at its
//     own `issued_at`, since a claim set whose `paid_through` is before it is
//     not a refusal but a key that has already ended; it must claim at least one
//     seat, a number D439 leaves unbounded because seats are compared and shown
//     rather than enforced; and it refuses a count of days that cannot mean what
//     it says, which is the tool's arithmetic rather than the format's.
//
// The key is checked against the workspace the owner names, because that is the
// workspace it is minted for and the tool knows no other. So the check that a
// key belongs *here* -- the `workspace_id` an installation verifies it against
// (TN2) -- is made where the key is read rather than where it is minted, and
// what the tool holds is that the id it names is one a verifier accepts.
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

// maxDays is the longest term the arithmetic holds: a term this package counts
// in nanoseconds, where one day is 86400e9 of them. It is a bound on what the
// machine can count and not on what a tier may be given, which is the
// verifier's rule, and it is far above any term anyone mints.
const maxDays = math.MaxInt64 / int64(24*time.Hour)

// versionV1 is the policy version this tool writes. It is the version K1.1's
// verifier reads, and the number is checked rather than trusted: a claim set
// carrying another one is refused by the verifier below instead of printed.
const versionV1 = 1

// run is main's whole body for both formats: the word `old` selects the format
// K7.3 deletes, and everything else mints the `fgk_` key this tool exists for.
// The word is not a flag because the two formats read two different claims out
// of `-key-id`, and a flag set cannot spell one name twice.
func run(args []string, out io.Writer, runner Runner) error {
	if len(args) > 0 && args[0] == "old" {
		return runOldFormat(args[1:], out, runner)
	}
	return runV1(args, out, runner)
}

// runV1 mints one v1 key: the flags, the claim set they build and the one line
// printed from it. The runner is the KMS seam, so the whole command is a thing a
// test drives: a test's runner signs what gcloud would have been handed and no
// binary is run.
func runV1(args []string, out io.Writer, runner Runner) error {
	flags := flag.NewFlagSet("mint", flag.ContinueOnError)
	kid := flags.String("kid", "", "the signing key id the signature is made under: the keyring entry a verifier places the key by")
	keyID := flags.String("key-id", "", "the key's own id, for revocation and support; it is not the signing key's id")
	workspaceID := flags.String("workspace-id", "", "the Firebase project id of the workspace the key is bound to: required on every key, and the id the tool checks the key against")
	organisation := flags.String("organisation", "", "the organisation the key binds to: a domain, an individual as an organisation of one, or an Order Form id")
	tier := flags.String("tier", "", "the tier the key is issued for: community, team or enterprise")
	seats := flags.Int("seats", 0, "the seats the organisation has, at least one: compared and shown, never enforced until enforcement ships (D439, TN1)")
	issuedAt := flags.String("issued-at", "", "when the key is minted, as an RFC 3339 stamp; the default is now, to the second")
	days := flags.Int("days", 0, "the term the key covers, in days from its issue: paid_through is that many days after issued_at")
	graceDays := flags.Int("grace-days", 30, "the grace after paid_through, in days: 30 unless the key says otherwise (D2133), and 0 where the deal gives none (D2138)")
	renewalMode := flags.String("renewal-mode", "", "how the key renews: auto (the scheduled job fetches the next key), manual (the user runs licence refresh) or none (offline)")
	renewBeforeDays := flags.Int("renew-before-days", 0, "how early the renewal job starts trying for the next key, in days before paid_through: 0 is the day itself")
	checkInterval := flags.String("check-interval", "", "the ISO 8601 duration between revocation checks, P<n>D or P<n>W; empty is null, a key that never checks")
	renewalSecretHash := flags.String("renewal-secret-hash", "", "the hash of the key's renewal secret: the secret itself never goes in a key (D2135)")
	kind := flags.String("kind", "", "the kind of key: paid, trial or comped")
	note := flags.String("note", "", "free text, shown to admins only")
	warnings := warningsFlag{}
	flags.Var(&warnings, "warning", "one warning window, roles:from, with the roles comma-separated and from grace_start or grace_start+<n>d; repeat the flag for each window")
	publicKey := flags.String("public-key", "", "the signing key's public half, unpadded base64url of the 32 bytes a keyring entry carries")
	project := flags.String("project", "", "the GCP project the signing key lives in")
	location := flags.String("location", "", "the signing key ring's location")
	keyRing := flags.String("keyring", "", "the signing key ring's name")
	key := flags.String("key", "", "the signing key's name")
	version := flags.String("version", "", "the signing key version to sign with: an asymmetric key has no primary version, and gcloud refuses without one")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// flag.Parse stops at the first word that is not a flag, and every flag
	// written after one is dropped without a word: a term or a workspace id
	// there is a claim the owner asked for and a key that does not carry it. The
	// command takes no positional argument once the format's word is read, so a
	// word is always a mistake.
	if extra := flags.Args(); len(extra) > 0 {
		return fmt.Errorf("unexpected %s: this command takes nothing but flags, and a word that is not one stops the reading, so every flag after it is dropped in silence and the key is minted without the terms it asked for", strings.Join(extra, " "))
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"kid", *kid},
		{"public-key", *publicKey},
		{"key-id", *keyID},
		{"workspace-id", *workspaceID},
		{"organisation", *organisation},
		{"tier", *tier},
		{"kind", *kind},
		{"renewal-mode", *renewalMode},
		{"project", *project},
		{"location", *location},
		{"keyring", *keyRing},
		{"key", *key},
		{"version", *version},
	} {
		if required.value == "" {
			missing = append(missing, "-"+required.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s: a v1 key text is one claim set signed through a KMS key's signing API, and every claim of it is a flag", strings.Join(missing, ", "))
	}
	// The tool's first input rule, and it is about the claim set's sense rather
	// than its length: a term of no days leaves a key that has already ended,
	// which the verifier accepts -- its rule bounds a key's length -- so the
	// tool is the only place it can be caught.
	if *days <= 0 {
		return errors.New("-days must be a positive number of days: a key that ends before it starts is not one anyone means to mint")
	}
	// The term is counted in nanoseconds, and past maxDays the product no longer
	// fits an int64. The overflow is not always negative: 213504 days wraps to a
	// positive quarter of an hour, which would mint a key with a 1526-second term
	// -- inside the verifier's length bound, active at its own `issued_at`, and
	// nothing for any check after this one to catch. So the bound is on the days
	// and not on the product. It is arithmetic and not the tier's: what a tier
	// may be given stays the verifier's rule to state.
	if int64(*days) > maxDays {
		return fmt.Errorf("-days is %d, and a term that long cannot be counted: the longest term this tool can hold is %d days, and the verifier is where the ceiling anyone mints under is stated", *days, maxDays)
	}
	// The second input rule, and the same shape: seats are what the organisation
	// bought (docs/licensing/licence-key.md, the claim set's table), an
	// individual being an organisation of one (T15), so a key that claims none
	// is a key nobody means to mint. The verifier does not catch it -- D439
	// makes seats compared and shown rather than enforced, so it bounds no
	// number -- and the console draws the seat count it reads, so zero is shown
	// to whoever holds the key instead of refused. The flag's own default is
	// zero, which is where the silence bites: a mint that left -seats out would
	// claim it.
	if *seats <= 0 {
		return errors.New("-seats must be the organisation's seat count, and at least one: a key that claims no seats is not one anyone means to mint")
	}
	// The third, and the two counts it covers are the ones a key would carry in
	// silence: grace is what keeps a lapsed key working and the renewal lead is
	// when the next key is fetched, so a negative of either is a sign in the
	// wrong place and not a value the deal means. Zero is a value both mean: no
	// grace at all, or a renewal that starts the day the term ends.
	for _, count := range []struct {
		name  string
		value int
	}{
		{"grace-days", *graceDays},
		{"renew-before-days", *renewBeforeDays},
	} {
		if count.value < 0 {
			return fmt.Errorf("-%s is %d, and a count of days cannot be negative: a key carrying one would be minted with a window nobody asked for", count.name, count.value)
		}
	}
	// The fourth: a key that renews renews with a secret, and the server
	// authenticates the call by the hash of it (D2135). The hash is signed into
	// the key and cannot be added to it afterwards, so an empty one on a
	// renewing key is a key that can never renew -- the customer pays for a term
	// and lapses at the end of it. A key whose renewal mode is `none` is
	// air-gapped and contacts nothing, so it carries none.
	if *renewalMode != licence.RenewalNone && strings.TrimSpace(*renewalSecretHash) == "" {
		return errors.New("-renewal-secret-hash is required for a key whose renewal mode is not none: the licence server compares that hash with the one it holds, and it is signed into the key, so a key minted without one can never renew")
	}

	issued := time.Now().UTC().Truncate(time.Second)
	if *issuedAt != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*issuedAt))
		if err != nil {
			return fmt.Errorf("-issued-at is not an RFC 3339 stamp such as 2026-11-12T00:00:00Z: %w", err)
		}
		issued = parsed.UTC()
	}
	term := time.Duration(*days) * 24 * time.Hour

	claims := licence.ClaimsV1{
		V:                 versionV1,
		KID:               *kid,
		KeyID:             *keyID,
		WorkspaceID:       *workspaceID,
		Organisation:      *organisation,
		Tier:              *tier,
		Seats:             *seats,
		IssuedAt:          issued,
		PaidThrough:       issued.Add(term),
		GraceDays:         *graceDays,
		RenewalMode:       *renewalMode,
		RenewBeforeDays:   *renewBeforeDays,
		RenewalSecretHash: *renewalSecretHash,
		Warnings:          warnings,
		Kind:              *kind,
		Note:              *note,
	}
	// An empty -check-interval is the claim the format writes as null: a key
	// that never checks (spec section 8, F16). A text of spaces is the owner's
	// paste and not an interval, and the verifier refuses one it cannot read.
	if interval := strings.TrimSpace(*checkInterval); interval != "" {
		claims.CheckInterval = &interval
	}

	ring, err := ringFromPublicKey(*kid, *publicKey)
	if err != nil {
		return err
	}
	keyText, err := MintV1(context.Background(), claims, KMS{
		Project:  *project,
		Location: *location,
		KeyRing:  *keyRing,
		Key:      *key,
		Version:  *version,
		Run:      runner,
	}, verifierV1(ring, *workspaceID))
	if err != nil {
		return err
	}
	fmt.Fprintln(out, keyText)
	return nil
}

// warningsFlag collects the -warning entries: one claim set's `warnings` list,
// in the order the owner named them. Each flag is the entry's shape --
// `roles:from` -- and what a role or a `from` says is the claim set's and
// K3.5's (ee/licence/keyv1.go, WarningV1), not this parser's.
type warningsFlag []licence.WarningV1

// String is what the flag package prints as the flag's default: the windows an
// earlier -warning set, read back as the text they came from.
func (w *warningsFlag) String() string {
	entries := make([]string, 0, len(*w))
	for _, warning := range *w {
		entries = append(entries, strings.Join(warning.Roles, ",")+":"+warning.From)
	}
	return strings.Join(entries, " ")
}

func (w *warningsFlag) Set(text string) error {
	roles, from, ok := strings.Cut(text, ":")
	if !ok || strings.TrimSpace(from) == "" {
		return errors.New("a warning window is roles:from, with the roles comma-separated and from a window such as grace_start or grace_start+7d")
	}
	warning := licence.WarningV1{From: strings.TrimSpace(from)}
	for _, role := range strings.Split(roles, ",") {
		if role = strings.TrimSpace(role); role != "" {
			warning.Roles = append(warning.Roles, role)
		}
	}
	if len(warning.Roles) == 0 {
		return errors.New("a warning window names at least one role: a window that reaches nobody is not one anyone means to mint")
	}
	*w = append(*w, warning)
	return nil
}

// verifierV1 is the check the tool holds what it minted to: K1.1's verifier,
// over the ring the owner named and the workspace the key is bound to.
//
// The workspace is the one the owner is minting for rather than another
// installation's, which is what the tool knows and a verifier at an
// installation knows instead (K3.1); the id is compared all the same, so an
// empty one is refused as the verifier refuses it, and the check the tool
// cannot make for itself -- whether this key belongs to the workspace it will
// be installed in -- is the verifier's at that installation.
func verifierV1(ring licence.Keyring, firebaseProjectID string) VerifierV1 {
	return func(raw []byte, now time.Time) (licence.ClaimsV1, licence.State, error) {
		return licence.VerifyV1(raw, now, ring, firebaseProjectID)
	}
}

// runOldFormat mints one key in the older `<key id>.<payload>.<signature>`
// format, which K7.3 deletes along with its verifier. It is reached by the one
// leading word `old`.
func runOldFormat(args []string, out io.Writer, runner Runner) error {
	flags := flag.NewFlagSet("mint", flag.ContinueOnError)
	keyID := flags.String("key-id", "", "the key id the key text leads with and a verifier places the key by")
	publicKey := flags.String("public-key", "", "the signing key's public half, unpadded base64url of the 32 bytes a keyring entry carries")
	tier := flags.String("tier", "", "the tier the key is issued for: community, team or enterprise")
	organisation := flags.String("organisation", "", "the organisation the key binds to: a domain, an individual as an organisation of one, or an Order Form id")
	seats := flags.Int("seats", 0, "the seats the organisation has, at least one: compared and shown, never enforced (D439)")
	features := flags.String("features", "", "the paid features the key turns on, comma-separated")
	days := flags.Int("days", 0, "the term the key covers, in days from today: a renewal covers the term that begins where its predecessor ends, so it passes that term plus the 30 days the predecessor still has")
	project := flags.String("project", "", "the GCP project the signing key lives in")
	location := flags.String("location", "", "the signing key ring's location")
	keyRing := flags.String("keyring", "", "the signing key ring's name")
	key := flags.String("key", "", "the signing key's name")
	version := flags.String("version", "", "the signing key version to sign with: an asymmetric key has no primary version, and gcloud refuses without one")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// flag.Parse stops at the first word that is not a flag, and every flag
	// written after one is dropped without a word: -seats and -features there
	// are terms the owner asked for and a key that does not carry them. The
	// command takes no positional argument, so a word is always a mistake.
	if extra := flags.Args(); len(extra) > 0 {
		return fmt.Errorf("unexpected %s: this command takes nothing but flags, and a word that is not one stops the reading, so every flag after it is dropped in silence and the key is minted without the terms it asked for", strings.Join(extra, " "))
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
		{"version", *version},
	} {
		if required.value == "" {
			missing = append(missing, "-"+required.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s: a key text is an id, a claim set, and a signature over it through a KMS key's signing API", strings.Join(missing, ", "))
	}
	// The tool's first input rule, and it is about the claim set's sense rather
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
	// The second input rule, and the same shape: seats are what the organisation
	// bought (docs/licensing/licence-key.md, the claim set's row), an individual
	// being an organisation of one (T15), so a key that claims none is a key
	// nobody means to mint. The verifier does not catch it -- D439 makes seats
	// compared and shown rather than enforced, so it bounds no number -- and the
	// console draws the seat count it reads, so zero is shown to whoever holds
	// the key instead of refused. The flag's own default is zero, which is where
	// the silence bites: a mint that left -seats out would claim it.
	if *seats <= 0 {
		return errors.New("-seats must be the organisation's seat count, and at least one: a key that claims no seats is not one anyone means to mint")
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
		Project:  *project,
		Location: *location,
		KeyRing:  *keyRing,
		Key:      *key,
		Version:  *version,
		Run:      runner,
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

// verifier is the check the old format's path holds what it minted to: the
// old verifier's own rule, over the ring the owner named.
func verifier(ring licence.Keyring) Verifier {
	return func(raw []byte, now time.Time) (licence.Claims, licence.State, error) {
		return licence.VerifyAgainst(raw, now, ring)
	}
}

// featureList reads the comma-separated features of the old format. A key that
// turns on no paid feature says so as an empty list rather than as a claim that
// is missing.
func featureList(text string) []string {
	list := []string{}
	for _, name := range strings.Split(text, ",") {
		if name = strings.TrimSpace(name); name != "" {
			list = append(list, name)
		}
	}
	return list
}
