package localcfg

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// The mailbox block, which is T2's and T3's footprint in this package (mobile
// P4.1 and P6.6, plan §12, §13 and decision 9): one keychain item name per
// pairing, validated, and its layered merge. The key itself is never here (M5,
// D113b) — the config holds the item's name, and the name is not a secret.

// pairUIDA and pairUIDB are two paired devices' uids, which is what a mailbox
// entry is keyed by: `fugaro pair` writes one per phone and prints it as the key
// to record. pairUIDP is a pairing session's, admitted as well (a config written
// while a session is open holds one) and looked up by nothing. The two shapes
// differ — a device's uid is 16 random bytes behind m~, a session's is 32 behind
// p~ — so a uid of one shape is not a uid of the other.
const (
	pairUIDA = "m~AAECAwQFBgcICQoLDA0ODw"
	pairUIDB = "m~EBESExQVFhcYGRobHB0eHw"
	pairUIDP = "p~AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
)

func mailboxBlock(entries ...string) string {
	return "mailbox:\n  keychain_item:\n" + strings.Join(entries, "")
}

func entry(uid, item string) string { return "    " + uid + ": " + item + "\n" }

// TestKeychainItemWrongShapeRefused is M5's fixed shape, one row per way an
// operator can write it wrong, for the name and for the uid it is filed under.
// The name becomes an argv element of `security` or `secret-tool`, and the
// project's own name must be in it, so a config file cannot name an arbitrary
// keychain query nor read another project's key; the uid is what `pair revoke`
// matches a device against, so a key that is not one is a line nothing would
// ever look up. The rule for the name is keychain.ParseItem's; this file is the
// one place it is applied to a config, and the refusal names the entry the
// operator wrote.
func TestKeychainItemWrongShapeRefused(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{name: "a block with no entry", block: "mailbox: {}\n", want: "names no pairing's item"},
		{name: "an empty map", block: mailboxBlock(), want: "names no pairing's item"},
		{name: "a key that is not a uid", block: mailboxBlock(entry("phone-a", "fugaro-mailbox-aurora")), want: "is not a pairing's uid"},
		{name: "a uid one character short", block: mailboxBlock(entry(pairUIDA[:len(pairUIDA)-1], "fugaro-mailbox-aurora")), want: "is not a pairing's uid"},
		{name: "an empty name", block: mailboxBlock(entry(pairUIDA, `""`)), want: "the item name is empty"},
		{name: "a name longer than 64 characters", block: mailboxBlock(entry(pairUIDA, strings.Repeat("a", 65))), want: "longer than 64"},
		{name: "a name with a space", block: mailboxBlock(entry(pairUIDA, `"fugaro aurora"`)), want: "is not a name"},
		{name: "a name with a path separator", block: mailboxBlock(entry(pairUIDA, `"fugaro/aurora"`)), want: "is not a name"},
		{name: "a name with a quote", block: mailboxBlock(entry(pairUIDA, `"fugaro'aurora"`)), want: "is not a name"},
		{name: "a name with a command substitution", block: mailboxBlock(entry(pairUIDA, `"fugaro$(id)aurora"`)), want: "is not a name"},
		{name: "a name beginning with a dash", block: mailboxBlock(entry(pairUIDA, `"-aurora"`)), want: "begins with '-'"},
		{name: "another project's name", block: mailboxBlock(entry(pairUIDA, `"fugaro-mailbox-borealis"`)), want: "does not contain the project's name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(sample + c.block))
			if err == nil {
				t.Fatalf("the config parsed with %s", c.block)
			}
			if !strings.Contains(err.Error(), "mailbox.keychain_item") {
				t.Errorf("the refusal does not name the key the operator wrote: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want ~%q", err, c.want)
			}
		})
	}
}

// uidSigner is a token.Signer that signs nothing: it wraps the claim set it is
// handed in a JWT-shaped string, which is all MintPairing's and MintDevice's own
// re-parse of what their signer returned reads.
type uidSigner struct{ email string }

func (s uidSigner) Email() string { return s.email }

func (s uidSigner) SignJWT(_ context.Context, payload []byte) (string, error) {
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s", nil
}

// uidItem is the item name decision 9 derives from a uid: the project's name,
// one separator and the uid's body. It is the name `fugaro pair` writes into the
// keychain for the device and tells the operator to record, and the shape
// keychain.ParseItem holds it to.
func uidItem(uid string) string { return "aurora-" + uid[2:] }

// TestMailboxKeyedByARealPairingUIDLoads: the block's key is a uid the
// installation's own minters draw — the device's `m~` uid that `fugaro pair`
// prints as `mailbox.keychain_item[<device uid>]`, and the session's `p~` uid
// this block also admits — so a config the operator writes from that output is
// only usable if such a uid loads in it. This test is also the one place this
// package's uid shape is tied to the token package's: the pattern here is a
// deliberate second copy (the token package mints uids, this one only recognises
// them), and a copy that drifts refuses every uid of the shape it got wrong —
// silently, because a refused uid looks to every other reader exactly like the
// one the operator pasted. The two uids are minted rather than written out, so
// the lengths asserted below are the token package's and not this file's.
func TestMailboxKeyedByARealPairingUIDLoads(t *testing.T) {
	ctx := context.Background()
	signer := uidSigner{email: "operator@example.invalid"}
	_, pairingUID, err := token.MintPairing(ctx, signer, time.Minute, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("minting a pairing uid: %v", err)
	}
	device, err := token.MintDevice(ctx, signer, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("minting a device uid: %v", err)
	}
	// The two uids are two different shapes: a pairing session's is 43 unpadded
	// base64url characters behind p~ (32 random bytes) and a device's is 22
	// behind m~ (16). A block that admits one length for both admits neither
	// minted uid.
	if len(pairingUID) != len("p~")+43 || !strings.HasPrefix(pairingUID, "p~") {
		t.Errorf("token.MintPairing's uid = %q, want p~ and 43 characters", pairingUID)
	}
	if len(device.UID) != len("m~")+22 || !strings.HasPrefix(device.UID, "m~") {
		t.Errorf("token.MintDevice's uid = %q, want m~ and 22 characters", device.UID)
	}
	lc, err := Parse([]byte(sample + mailboxBlock(
		entry(pairingUID, uidItem(pairingUID)),
		entry(device.UID, uidItem(device.UID)),
	)))
	if err != nil {
		t.Fatalf("a block keyed by a minted pairing uid and a minted device uid: %v", err)
	}
	for uid, want := range map[string]string{pairingUID: uidItem(pairingUID), device.UID: uidItem(device.UID)} {
		if got := lc.Mailbox.KeychainItem[uid]; got != want {
			t.Errorf("mailbox.keychain_item[%q] = %q, want %q", uid, got, want)
		}
	}
}

// TestMailboxRefusesAMalformedUID: the key is what `pair revoke` matches a
// device against and what the consumer looks a pairing's item up by, so a key
// that is not a uid is refused by name rather than kept as a line nothing would
// ever look up — and every way of writing one wrong is refused, including the
// other shape's length and the one prefix that is neither p~ nor m~ (a run's,
// r~, is the identity the pairing rules exist to keep out).
func TestMailboxRefusesAMalformedUID(t *testing.T) {
	body22, body43 := strings.Repeat("A", 22), strings.Repeat("A", 43)
	cases := []struct{ name, uid string }{
		{"a run's own uid", "r~" + body43},
		{"no prefix", body43},
		{"a prefix that is neither p~ nor m~", "q~" + body22},
		{"a pairing uid at the device length", "p~" + body22},
		{"a device uid at the pairing length", "m~" + body43},
		{"a pairing uid one character short", "p~" + body43[:42]},
		{"a pairing uid one character long", "p~" + body43 + "A"},
		{"a device uid one character short", "m~" + body22[:21]},
		{"a device uid one character long", "m~" + body22 + "A"},
		{"a character outside base64url", "m~" + body22[:21] + "+"},
		{"a base64 padding character", "p~" + body43[:42] + "="},
		{"an empty body", "p~"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(sample + mailboxBlock(entry(c.uid, uidItem(c.uid)))))
			if err == nil {
				t.Fatalf("mailbox.keychain_item accepted %q as a pairing's uid", c.uid)
			}
			if !strings.Contains(err.Error(), "is not a pairing's uid") {
				t.Errorf("refusal %q does not say the key is not a pairing's uid", err)
			}
		})
	}
}

// TestMailboxRefusesJunkBeforePrefix: the uid's shape is anchored at both ends,
// so a key with anything at all in front of the prefix is refused rather than
// read as that prefix's uid. A rule that looked for the prefix anywhere in the
// key would admit "x p~…" and " m~…", and the entry `fugaro mailbox` reads a
// pairing's item from is the key itself — `pair revoke` matches a device by it
// too — so a key nothing looks up is a line the operator recorded for nothing.
func TestMailboxRefusesJunkBeforePrefix(t *testing.T) {
	pairBody, deviceBody := strings.Repeat("A", 43), strings.Repeat("A", 22)
	for _, uid := range []string{
		"xp~" + pairBody,
		"xm~" + deviceBody,
		" p~" + pairBody,
		" m~" + deviceBody,
		"uid-m~" + deviceBody,
	} {
		t.Run(uid, func(t *testing.T) {
			// The key is quoted: a plain YAML key would silently lose the
			// leading space that is half of what this case is about.
			block := mailboxBlock("    \"" + uid + "\": \"fugaro-mailbox-aurora\"\n")
			_, err := Parse([]byte(sample + block))
			if err == nil {
				t.Fatalf("mailbox.keychain_item accepted %q as a pairing's uid", uid)
			}
			if !strings.Contains(err.Error(), "is not a pairing's uid") {
				t.Errorf("refusal %q does not say the key is not a pairing's uid", err)
			}
		})
	}
}

// TestKeychainItemOldSingleShapeRefused: decision 9 widened the block from one
// item name to one per pairing, and a config written before it holds a scalar
// there. There is no shim (pre-1.0): a single name names no pairing, so the
// operator is told to re-run `fugaro pair` and record the item it prints rather
// than left with yaml's own type error.
func TestKeychainItemOldSingleShapeRefused(t *testing.T) {
	for _, block := range []string{
		"mailbox:\n  keychain_item: fugaro-mailbox-aurora\n",
		"mailbox:\n  keychain_item: \"fugaro-mailbox-aurora\"\n",
	} {
		_, err := Parse([]byte(sample + block))
		if err == nil {
			t.Fatalf("the old single-name shape parsed: %s", block)
		}
		for _, want := range []string{"fugaro pair", "one item per pairing", "fugaro-mailbox-aurora"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %q", err, want)
			}
		}
	}
	// A block that holds no keychain_item at all is not the old shape: it is a
	// block with nothing in it, refused by validate for its own reason.
	if _, err := Parse([]byte(sample + "mailbox: {}\n")); err == nil || strings.Contains(err.Error(), "fugaro pair and record") {
		t.Errorf("an empty mailbox block = %v, want validate's own refusal", err)
	}
}

// TestKeychainItemPerPairingRoundTripsAndMerges is decision 9's shape and the
// merge rule this block needs (§13): two pairings hold two items, the entries
// survive a round trip, Shared() carries them, MergeShared takes the published
// entries when the publishing machine has none — otherwise a launcher whose own
// config has no mailbox: block erases the names another machine recorded,
// silently, in the only place the installation's copy lives — and it unions
// them when the machine has some, because with one item per pairing the names
// in the block are several machines' pairings and not one machine's.
//
// Two mutations, both verified, run and restored. Deleting the whole merge
// (`if published.Mailbox != nil || out.Mailbox != nil { … }`) fails the first
// assertion, on a nil block; taking the published pointer instead of the union
// (`if out.Mailbox == nil { out.Mailbox = published.Mailbox }`, which is what
// this block did before decision 9's per-pairing names) fails the
// other-machine assertion below, and no other test fails for either.
func TestKeychainItemPerPairingRoundTripsAndMerges(t *testing.T) {
	block := mailboxBlock(
		entry(pairUIDA, "fugaro-mailbox-aurora-one"),
		entry(pairUIDB, "fugaro-mailbox-aurora-two"),
	)
	lc, err := Parse([]byte(sample + block))
	if err != nil {
		t.Fatalf("two pairings' items: %v", err)
	}
	want := map[string]string{
		pairUIDA: "fugaro-mailbox-aurora-one",
		pairUIDB: "fugaro-mailbox-aurora-two",
	}
	if lc.Mailbox == nil || len(lc.Mailbox.KeychainItem) != 2 {
		t.Fatalf("mailbox = %+v, want both pairings' items", lc.Mailbox)
	}
	for uid, item := range want {
		if got := lc.Mailbox.KeychainItem[uid]; got != item {
			t.Errorf("mailbox.keychain_item[%s] = %q, want %q", uid, got, item)
		}
	}
	out, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"    " + pairUIDA + ": fugaro-mailbox-aurora-one",
		"    " + pairUIDB + ": fugaro-mailbox-aurora-two",
	} {
		if !strings.Contains(string(out), line) {
			t.Errorf("the item did not survive a round trip (%q missing):\n%s", line, out)
		}
	}

	// The published copy is the names and never a key, and a pairing session's
	// uid is a key of the map like a device's.
	session, err := Parse([]byte(sample + mailboxBlock(entry(pairUIDP, "fugaro-mailbox-aurora-three"))))
	if err != nil {
		t.Fatalf("a pairing session's uid: %v", err)
	}
	if got := session.Mailbox.KeychainItem[pairUIDP]; got != "fugaro-mailbox-aurora-three" {
		t.Errorf("a p~ keyed entry = %q, want it kept", got)
	}

	existing := sharedBase(t, block)
	local := sharedBase(t, "")
	if s := existing.Shared(); s.Mailbox == nil || len(s.Mailbox.KeychainItem) != 2 {
		t.Fatalf("Shared() dropped the mailbox block: %+v", s.Mailbox)
	}
	got := MergeShared(existing, local)
	if got.Mailbox == nil || len(got.Mailbox.KeychainItem) != 2 {
		t.Fatalf("a publish from a machine with no mailbox: block dropped the published one: %+v", got.Mailbox)
	}
	out, err = got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "fugaro-mailbox-aurora-one") {
		t.Errorf("the merged file carries no item name:\n%s", out)
	}
	// The local block does not replace the published one: its entries are
	// unioned, the way repos and base_images are, because decision 9 made the
	// block one item per pairing and the pairings in it are several machines'
	// — a laptop that paired its own phone would otherwise publish a file that
	// names its phone and forgets every other machine's. Where one uid is on
	// both sides the local name wins: the operator who wrote their own item
	// names is the one who knows where their keys are.
	local = sharedBase(t, mailboxBlock(entry(pairUIDA, "fugaro-mailbox-aurora-local")))
	got = MergeShared(existing, local)
	if got.Mailbox == nil {
		t.Fatalf("a local block was dropped")
	}
	if got.Mailbox.KeychainItem[pairUIDA] != "fugaro-mailbox-aurora-local" {
		t.Errorf("the local item name must win the clash: %+v", got.Mailbox)
	}
	if got.Mailbox.KeychainItem[pairUIDB] != "fugaro-mailbox-aurora-two" {
		t.Errorf("the other machine's pairing was erased by this one's: %+v", got.Mailbox)
	}
	// Unioned into a new block and a new map: the two inputs still hold what
	// they held, which is what lets a caller publish from either.
	if existing.Mailbox.KeychainItem[pairUIDA] != "fugaro-mailbox-aurora-one" || len(local.Mailbox.KeychainItem) != 1 {
		t.Errorf("MergeShared modified an input's mailbox block: %+v %+v", existing.Mailbox, local.Mailbox)
	}
	// And the union is what a publish writes, so the next machine reads both.
	out, err = got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"fugaro-mailbox-aurora-local", "fugaro-mailbox-aurora-two"} {
		if !strings.Contains(string(out), line) {
			t.Errorf("the merged file dropped %q:\n%s", line, out)
		}
	}
	// A published object with none leaves the local one alone.
	if got := MergeShared(sharedBase(t, ""), local); got.Mailbox == nil || got.Mailbox.KeychainItem[pairUIDA] != "fugaro-mailbox-aurora-local" {
		t.Errorf("a published object with no mailbox block dropped the local one: %+v", got.Mailbox)
	}
	// Neither side having one writes no mailbox: key at all.
	if got := MergeShared(sharedBase(t, ""), sharedBase(t, "")); got.Mailbox != nil {
		t.Errorf("two configs with no mailbox block merged into %+v", got.Mailbox)
	}
}

// TestMailboxBlockIsOptional: `fugaro mailbox` refuses at startup when no
// mailbox is configured (plan §8's run-level row), so the *config* must be able
// to carry none — a project that never pairs is not a broken project. The nil
// block is the state T5 refuses on, and it must be distinguishable from a
// block with an entry in it.
func TestMailboxBlockIsOptional(t *testing.T) {
	lc, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("a config with no mailbox block: %v", err)
	}
	if lc.Mailbox != nil {
		t.Errorf("mailbox = %+v, want nil", lc.Mailbox)
	}
}
