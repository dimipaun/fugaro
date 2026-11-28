package cli

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mailbox/keychain"
)

// The retirement sweep (J5c): the MAC key of every pairing no device node names
// any more goes out of all three homes it was ever written to. What the plan's
// verification names, and what each test below proves: the red step, that after
// the device node is gone the key is still in the keychain, the container and
// the config's mailbox block, and that a revoke takes it out of all three
// (TestPairRetiresMACKeyOfARevokedDevice); that a uid with no device node at all
// goes too, and a pairing the nodes still carry is left exactly where it was
// (TestPairRetiresMACKeyLeavesTheLivePairingsAlone); that the config is edited
// as bytes, so a comment and every other block survive
// (TestPairRetiresMACKeyKeepsTheConfigsOtherBytes); and that a home which
// refuses is a refusal rather than a report of success
// (TestPairRetiresMACKeyFailureIsRefused).
//
// Everything runs on the fixture's fake keychain, `internal/gcpfake`'s Realtime
// Database and Secret Manager, and a config in t.TempDir(): no keychain, no
// cloud, no credential.

// pairRetiredItemB is the item name this file's configs record for the pairing
// no device node names. It is deliberately NOT pairItemName("aurora", uid): the
// item a config records is what the consumer reads the key from, so the sweep
// deletes the recorded name, and a test that used the derived one could not tell
// the two apart.
const pairRetiredItemB = "aurora-retired-b"

// pairMailboxBlock is the mailbox block the fixture's config gets, written the
// way an operator records it: one entry per uid, quoted, in the order the keys
// sort.
func pairMailboxBlock(entries map[string]string) string {
	var b strings.Builder
	b.WriteString("mailbox:\n  keychain_item:\n")
	for _, uid := range slices.Sorted(maps.Keys(entries)) {
		b.WriteString("    \"" + uid + "\": \"" + entries[uid] + "\"\n")
	}
	return b.String()
}

// pairSeedKeyAt stores a MAC key at the item named by hand, for the pairing a
// config records under a name this build does not derive, and returns the value
// the container's version is built from.
func pairSeedKeyAt(t *testing.T, f *pairFixture, name string) string {
	t.Helper()
	item, err := keychain.ParseItem(name, "aurora")
	if err != nil {
		t.Fatalf("the item name %s is not one this project's keychain takes: %v", name, err)
	}
	key, err := token.NewMACKey()
	if err != nil {
		t.Fatal(err)
	}
	value := token.EncodeKey(key)
	if err := f.keys.Put(context.Background(), item, keychain.NewKey([]byte(value))); err != nil {
		t.Fatal(err)
	}
	return value
}

// pairConfigNow is the fixture's config as the file holds it now, which is what
// a sweep of the document has to be asserted on: the value the command loaded
// is the one it started from, and an entry it removed is gone from the file.
func pairConfigNow(t *testing.T) (*localcfg.Config, []byte) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the fixture's config: %v", err)
	}
	lc, err := localcfg.Parse(doc)
	if err != nil {
		t.Fatalf("the config the command left does not read back: %v", err)
	}
	return lc, doc
}

func TestPairRetiresMACKeyOfARevokedDevice(t *testing.T) {
	itemA := pairItemName("aurora", pairDevA)
	f := newPairFixture(t, pairMe, func(cfg *string, _ *pairFixture) {
		*cfg += pairMailboxBlock(map[string]string{pairDevA: itemA})
	})
	f.pairSeedDevice(pairDevA, "phone-a")
	_, keyA := f.pairSeedItem(t, pairDevA)
	f.pairSeedCloud(t)
	f.secrets.AddVersion(infra.OrchestratorPairingSecretID, pairCloudVersion(t, pairDevA, keyA))
	f.secrets.AllowAccess()

	out, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRevoked || events[0].Revoke == nil {
		t.Fatalf("events = %v, want one %s carrying a document", events, pairEventRevoked)
	}
	// The red step's own assertion: the key the device node no longer admits is
	// gone from the keychain, from the installation's container and from the
	// config's mailbox block.
	if _, ok := f.keys.Keys[itemA]; ok {
		t.Fatalf("the revoked pairing's MAC key is still in the keychain at %s: the node's deletion ends what admits the phone, and the key is what verifies its messages", itemA)
	}
	if left := f.pairCloudUIDs(t); len(left) != 0 {
		t.Fatalf("the container still holds %v, want nothing: a key of a pairing no device node names is one nothing should go on verifying with", left)
	}
	lc, doc := pairConfigNow(t)
	if lc.Mailbox != nil {
		t.Fatalf("the config still carries mailbox.keychain_item %v: the entry names an item no device node is admitted by", lc.Mailbox.KeychainItem)
	}
	if strings.Contains(string(doc), itemA) {
		t.Fatalf("the config document still names %s: %s", itemA, doc)
	}
	// And the act reports it, split the way it was done: `retired` is the
	// sweep's own list — the item and the config entry, which no other step of a
	// revoke touches — and the container's copy is the footprint's, so it is
	// named under `wrote` with the rest of the revoked device's footprint in
	// M4's order.
	want := []string{itemA, os.Getenv("FUGARO_CONFIG")}
	if !slices.Equal(events[0].Revoke.Retired, want) {
		t.Fatalf("retired = %v, want %v: the homes the sweep itself emptied", events[0].Revoke.Retired, want)
	}
	if !slices.Contains(events[0].Revoke.Wrote, pairCloudCopy()) {
		t.Fatalf("wrote = %v, want the revoked device's own cloud copy among the paths the act deleted", events[0].Revoke.Wrote)
	}
}

func TestPairRetiresMACKeyLeavesTheLivePairingsAlone(t *testing.T) {
	// The sweep's other half, and the one that must not overreach: a uid the
	// device nodes still carry keeps its key in every home, whichever home the
	// sweep is looking at. B is the pairing no command could name — a uid the
	// config records, no device node, an item under a name this build does not
	// derive — which is exactly what the container read is for.
	itemA := pairItemName("aurora", pairDevA)
	itemC := pairItemName("aurora", pairDevC)
	f := newPairFixture(t, pairMe, func(cfg *string, _ *pairFixture) {
		*cfg += pairMailboxBlock(map[string]string{pairDevA: itemA, pairDevB: pairRetiredItemB, pairDevC: itemC})
	})
	f.pairSeedDevice(pairDevA, "phone-a")
	f.pairSeedDevice(pairDevC, "phone-c")
	_, keyA := f.pairSeedItem(t, pairDevA)
	keyB := pairSeedKeyAt(t, f, pairRetiredItemB)
	_, keyC := f.pairSeedItem(t, pairDevC)
	f.pairSeedCloud(t)
	f.secrets.AddVersion(infra.OrchestratorPairingSecretID, pairCloudVersion(t, pairDevA, keyA))
	f.secrets.AddVersion(infra.OrchestratorPairingSecretID, pairCloudVersion(t, pairDevB, keyB))
	f.secrets.AddVersion(infra.OrchestratorPairingSecretID, pairCloudVersion(t, pairDevC, keyC))
	f.secrets.AllowAccess()

	if _, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	for _, gone := range []string{itemA, pairRetiredItemB} {
		if _, ok := f.keys.Keys[gone]; ok {
			t.Errorf("the MAC key at %s survived: the pairing it belongs to is one no device node names", gone)
		}
	}
	if _, ok := f.keys.Keys[itemC]; !ok {
		t.Errorf("the MAC key at %s was deleted, and its device node is still there: the sweep retires orphans and nothing else", itemC)
	}
	left := f.pairCloudUIDs(t)
	if _, ok := left[pairDevA]; ok {
		t.Error("the revoked pairing's key is still in the container")
	}
	if _, ok := left[pairDevB]; ok {
		t.Error("the orphan pairing's key is still in the container: no device node names it, and the container is the one place a copy the config never recorded is written down")
	}
	if _, ok := left[pairDevC]; !ok {
		t.Errorf("the container holds %v, want the live pairing's key: one revoke must not take a paired phone's key", left)
	}
	lc, doc := pairConfigNow(t)
	if lc.Mailbox == nil {
		t.Fatalf("the config's mailbox block is gone, and one pairing it names is still paired: %s", doc)
	}
	if _, ok := lc.Mailbox.KeychainItem[pairDevC]; !ok {
		t.Errorf("mailbox.keychain_item = %v, want the live pairing's entry: the sweep removes the entries of the uids it retired and no others", lc.Mailbox.KeychainItem)
	}
	for _, gone := range []string{pairDevA, pairDevB} {
		if _, ok := lc.Mailbox.KeychainItem[gone]; ok {
			t.Errorf("mailbox.keychain_item still names %s, whose MAC key is gone", gone)
		}
	}
	if v := f.db.Value("config/admin/devices/" + pairDevC); v == nil {
		t.Error("the live pairing's device node is gone: the sweep reads the allowlist and never writes it")
	}
}

func TestPairRetiresMACKeyKeepsTheConfigsOtherBytes(t *testing.T) {
	// The config is a document a person also edits by hand, so the sweep splices
	// the entry's bytes out of the text rather than marshalling the loaded value
	// back (yamledit): the file comes back as the original with one span gone,
	// its other blocks, their own style and every comment that is not the
	// block's own untouched.
	itemA := pairItemName("aurora", pairDevA)
	block := pairMailboxBlock(map[string]string{pairDevA: itemA})
	f := newPairFixture(t, pairMe, func(cfg *string, _ *pairFixture) {
		*cfg = strings.Replace(*cfg, "repos:\n", "# what this project runs\nrepos:\n", 1) + block
	})
	f.pairSeedDevice(pairDevA, "phone-a")
	f.pairSeedItem(t, pairDevA)
	before := readConfigDoc(t)

	if _, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	after := readConfigDoc(t)
	if !strings.Contains(string(after), "# what this project runs") {
		t.Fatalf("the sweep took a comment that is not the retired block's own with it:\n%s", after)
	}
	if want := strings.Replace(string(before), block, "", 1); string(after) != want {
		t.Fatalf("the document is not the original with the one block removed:\n got %q\nwant %q", after, want)
	}
}

func TestPairRetiresMACKeyFailureIsRefused(t *testing.T) {
	// A home that refuses is a refusal and not a report of success: the key is
	// still there, and no second `fugaro pair revoke` could name the pairing
	// again to finish the job. The entry that named the item stays with it —
	// removing it would take away the only record of where the key is.
	itemA := pairItemName("aurora", pairDevA)
	f := newPairFixture(t, pairMe, func(cfg *string, _ *pairFixture) {
		*cfg += pairMailboxBlock(map[string]string{pairDevA: itemA, pairDevB: pairRetiredItemB})
	})
	f.pairSeedDevice(pairDevA, "phone-a")
	f.pairSeedItem(t, pairDevA)
	pairSeedKeyAt(t, f, pairRetiredItemB)
	refusing := pairRefusingDelete{f.keys, pairRetiredItemB}
	prev := pairKeychain
	pairKeychain = func() keychain.Store { return refusing }
	t.Cleanup(func() { pairKeychain = prev })

	out, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json")
	if err == nil {
		t.Fatalf("revoke reported success with a MAC key still in the keychain: %s", out)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRefused || events[0].Code != pairRefusedMACRetire {
		t.Fatalf("events = %v, want one %s at %s", events, pairEventRefused, pairRefusedMACRetire)
	}
	if _, ok := f.keys.Keys[pairRetiredItemB]; !ok {
		t.Fatal("the key the keychain refused to delete is gone anyway")
	}
	lc, _ := pairConfigNow(t)
	if lc.Mailbox == nil {
		t.Fatal("the mailbox block was removed for a key that is still there: the entry is the only record of where it is")
	}
	if _, ok := lc.Mailbox.KeychainItem[pairDevB]; !ok {
		t.Fatalf("mailbox.keychain_item = %v, want the entry whose item could not be deleted", lc.Mailbox.KeychainItem)
	}
	if v := f.db.Value("config/admin/devices/" + pairDevA); v != nil {
		t.Error("the device node survived: the revocation itself landed, and it is the sweep that did not")
	}
}

// pairRefusingDelete is a keychain whose Delete fails for one item, which is how
// a test tells the sweep's own failure apart from the revoked device's: the real
// fake's Backend fails every operation, and the footprint's own delete would
// refuse first.
type pairRefusingDelete struct {
	keychain.Store
	refuse string
}

func (s pairRefusingDelete) Delete(ctx context.Context, item keychain.Item) error {
	if item.Name() == s.refuse {
		return errors.New("the keychain refused the delete")
	}
	return s.Store.Delete(ctx, item)
}

// readConfigDoc is the fixture's config as bytes, for the test that asserts on
// the document rather than on the value it loads to.
func readConfigDoc(t *testing.T) []byte {
	t.Helper()
	doc, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatalf("reading the fixture's config: %v", err)
	}
	return doc
}
