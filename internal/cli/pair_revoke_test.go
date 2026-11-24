package cli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The refresh tokens these tests seed. They are readable on purpose: a test
// literal that looked like a token would be a secret-shaped literal in the
// repository (CLAUDE.md), and nothing here is a credential — the family node is
// what the command deletes and the value is what makes one family tell itself
// apart from another.
const (
	pairRefreshTokenA = "refresh-token-a"
	pairRefreshTokenB = "refresh-token-b"
	pairRefreshTokenC = "refresh-token-c"
)

// pairRevokeEnv is the local config and the database the fixture's project
// answers on, for the two tests that call the member removal directly: nothing
// in the CLI reaches pairRemoveMember yet (P9's `fugaro user revoke` is what
// will), so the only way to test it is to call it, and it takes the two things
// a command's own open would have built.
func pairRevokeEnv(t *testing.T, f *pairFixture) (*localcfg.Config, *rtdb.Client) {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatalf("loading the fixture's config: %v", err)
	}
	db, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("opening the fixture's database: %v", err)
	}
	return lc, db
}

// pairRefreshWithToken is what a refresh does with the token a phone holds, in
// the test's own hand: P4's store — which rotates, refuses a replay and expires
// a family idle for IdleLifetime — is not in this tree yet, and the red step
// this file pins is that a refresh AFTER an admin revoke finds no family at all
// and is refused with device_revoked (D2794, F32). So this looks a token up and
// nothing else: it is not a second implementation of the rules, and the day P4
// lands, the test's question is the same one its Rotate answers.
//
// It reads the contract's own path (mobilesession.RefreshRoot), which is where
// P4 has to write a family for the admin revoke to find one, and it finds a
// plaintext "token" field, which P4's store will not keep (D2783: hashed at
// rest). Both are this test's stand-in for the store: what it can hold the
// sides to is that the revoke deleted the node the contract names, and the
// lookup itself is replaced by P4's Rotate when that lands.
func pairRefreshWithToken(ctx context.Context, db *rtdb.Client, token string) (string, error) {
	keys, err := db.Keys(ctx, mobilesession.RefreshRoot)
	if err != nil {
		return "", err
	}
	for _, family := range keys {
		var node struct {
			Token string `json:"token"`
		}
		found, err := db.Get(ctx, mobilesession.RefreshFamilyPath(family), &node)
		if err != nil {
			return "", err
		}
		if found && node.Token == token {
			return family, nil
		}
	}
	return "", mobilesession.Error{Code: mobilesession.CodeDeviceRevoked, Message: "the device's refresh family is gone"}
}

// TestPairRevokeFamilyEndsTheRefreshSession is P7's red step: after an admin
// revoke, a refresh with the token the phone still holds is refused, because
// the family the token belongs to is gone with the device node. It fails before
// this task's change — the node's deletion ended the phone's database reads and
// left the family rotating — which is the hole D2794 names for a lost phone
// with no other device.
func TestPairRevokeFamilyEndsTheRefreshSession(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, "phone-a")
	f.pairSeedItem(t, pairDevA)
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevA), map[string]any{"token": pairRefreshTokenA})
	_, db := pairRevokeEnv(t, f)

	// The family is there and a refresh finds it before the revoke, so what the
	// test measures after it is a revocation rather than a store that never
	// answered.
	if family, err := pairRefreshWithToken(context.Background(), db, pairRefreshTokenA); err != nil || family != pairDevA {
		t.Fatalf("a refresh before the revoke = (%q, %v), want the device's own family: the test's store must grant what the revoke takes away", family, err)
	}

	out, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	events := pairLines(t, out)
	if len(events) != 1 || events[0].Event != pairEventRevoked || events[0].Revoke == nil {
		t.Fatalf("events = %v, want one %s carrying a document", events, pairEventRevoked)
	}
	if !slices.Contains(events[0].Revoke.Wrote, mobilesession.RefreshFamilyPath(pairDevA)) {
		t.Fatalf("wrote = %v, want the refresh family among the paths the act deleted: a document that omits it reports a session the command did not end", events[0].Revoke.Wrote)
	}
	if v := f.db.Value(mobilesession.RefreshFamilyPath(pairDevA)); v != nil {
		t.Fatalf("the family survived the revoke: %v", v)
	}

	family, err := pairRefreshWithToken(context.Background(), db, pairRefreshTokenA)
	if err == nil {
		t.Fatalf("a refresh with the revoked family's token was granted the family %q: the node's deletion ends the phone's reads, and the family is the session it keeps refreshing", family)
	}
	var refusal mobilesession.Error
	if !errors.As(err, &refusal) || refusal.Code != mobilesession.CodeDeviceRevoked {
		t.Fatalf("a refresh after the revoke = %v, want a refusal coded %s", err, mobilesession.CodeDeviceRevoked)
	}
}

// TestPairRevokeFamilyKeepsTheMemberRecord is the other half of D2794: the
// family goes and the member record stays. A revoke by device must not hand the
// address back to whoever signs in with it next (F34), so the record — which
// holds the provider and sub the next pairing is matched against (D2785) — is
// not this command's to delete; removing a member is `fugaro user revoke`'s
// (O14), through pairRemoveMember.
func TestPairRevokeFamilyKeepsTheMemberRecord(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, "phone-a")
	f.pairSeedItem(t, pairDevA)
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevA), map[string]any{"token": pairRefreshTokenA})
	key := pairAddressKey(t, pairMe)
	f.db.Set(pairMemberRoot+"/"+key, map[string]any{
		"v": pairMemberVersion, "email": pairMe, "at": pairT0.UTC().Format(time.RFC3339),
		"provider": "google.com", "sub": "subject-for-tests",
	})

	if _, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	member, ok := f.db.Value(pairMemberRoot + "/" + key).(map[string]any)
	if !ok {
		t.Fatalf("the member record is gone after a device revoke (%v): the next pairing of this address would start from nothing, and a stolen phone would hand it to whoever signs in next (F34)", f.db.Value(pairMemberRoot))
	}
	if member["sub"] != "subject-for-tests" {
		t.Fatalf("the member record = %v, want the sub the pairing recorded left where it was", member)
	}
	if v := f.db.Value(rules.PathDevices + "/" + pairDevA); v != nil {
		t.Fatalf("the device node survived: %v", v)
	}
	if v := f.db.Value(mobilesession.RefreshFamilyPath(pairDevA)); v != nil {
		t.Fatalf("the family survived: %v", v)
	}
}

// TestPairRevokeFamilyFailureIsRefused is the refusal that must not read as
// success: when the family cannot be ended, the device node is already gone and
// the laptop's key is already deleted, so the phone cannot reach the database —
// and the token it holds still refreshes. The refusal says which path was left
// behind, because the family is what has to be deleted by hand.
func TestPairRevokeFamilyFailureIsRefused(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDevice(pairDevA, "phone-a")
	itemName, _ := f.pairSeedItem(t, pairDevA)
	f.pairSeedPush(pairDevA)
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevA), map[string]any{"token": pairRefreshTokenA})
	f.db.Deny(mobilesession.RefreshRoot)

	out, _, err := pairRun(t, "revoke", "--uid", pairDevA, "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit 1: a revoke that could not end the family is not a success", err)
	}
	events := pairLines(t, out)
	last := events[len(events)-1]
	if last.Event != pairEventRefused || last.Code != pairRefusedDatabaseAdmin {
		t.Fatalf("events = %v, want a %s with code %q", events, pairEventRefused, pairRefusedDatabaseAdmin)
	}
	if !strings.Contains(last.Reason, mobilesession.RefreshFamilyPath(pairDevA)) {
		t.Fatalf("the refusal does not name the family it could not end: %q", last.Reason)
	}
	for _, e := range events {
		if e.Event == pairEventRevoked {
			t.Fatalf("a failed revoke emitted the %s event: %v", pairEventRevoked, events)
		}
	}
	if v := f.db.Value(rules.PathDevices + "/" + pairDevA); v != nil {
		t.Fatalf("the device node survived: its deletion is the revocation, and the deny is on the family (%v)", v)
	}
	if v := f.db.Value(mobilesession.RefreshFamilyPath(pairDevA)); v == nil {
		t.Fatal("a write the database denied deleted the family anyway")
	}
	if _, ok := f.keys.Keys[itemName]; ok {
		t.Fatal("the MAC keychain item survived a revoke whose device node is already gone: no second revoke could ever remove it")
	}
}

// TestPairRevokeFamilyRemovesAMember is the removal O14 gives `fugaro user
// revoke` (P9) and P7 exports: when a person's last roster grant goes, the
// member record, every device node of the address and every refresh family go
// with it, so the address holds no phone that can reach the project. A device
// of another address is left alone, and so is its family and its record.
func TestPairRevokeFamilyRemovesAMember(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDeviceFor(pairDevA, "phone-a", pairMe, "operator")
	f.pairSeedDeviceFor(pairDevB, "phone-b", pairMe, "admin")
	f.pairSeedDeviceFor(pairDevC, "phone-c", pairLauncher, "operator")
	f.pairSeedItem(t, pairDevA)
	f.pairSeedItem(t, pairDevB)
	f.pairSeedItem(t, pairDevC)
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevA), map[string]any{"token": pairRefreshTokenA})
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevB), map[string]any{"token": pairRefreshTokenB})
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevC), map[string]any{"token": pairRefreshTokenC})
	key, other := pairAddressKey(t, pairMe), pairAddressKey(t, pairLauncher)
	f.db.Set(pairMemberRoot+"/"+key, map[string]any{"v": pairMemberVersion, "email": pairMe})
	f.db.Set(pairMemberRoot+"/"+other, map[string]any{"v": pairMemberVersion, "email": pairLauncher})
	lc, db := pairRevokeEnv(t, f)

	removal, err := pairRemoveMember(context.Background(), lc, db, pairMe)
	if err != nil {
		t.Fatalf("removing the member: %v", err)
	}
	if removal.Email != pairMe || removal.Member != pairMemberRoot+"/"+key {
		t.Fatalf("removal = %+v, want the record of %s", removal, pairMe)
	}
	if !slices.Equal(removal.Devices, []string{pairDevA, pairDevB}) {
		t.Fatalf("removal.Devices = %v, want both devices of the address and neither of another", removal.Devices)
	}
	if len(removal.Failed) != 0 {
		t.Fatalf("removal.Failed = %v, want none", removal.Failed)
	}
	want := []string{
		rules.PathDevices + "/" + pairDevA, mobilesession.RefreshFamilyPath(pairDevA), pairPushPath(pairDevA),
		rules.PathDevices + "/" + pairDevB, mobilesession.RefreshFamilyPath(pairDevB), pairPushPath(pairDevB),
		pairMemberRoot + "/" + key,
	}
	if !slices.Equal(removal.Wrote, want) {
		t.Fatalf("removal.Wrote = %v, want %v: each device's node and family in order, the record last", removal.Wrote, want)
	}

	for _, path := range []string{
		rules.PathDevices + "/" + pairDevA, mobilesession.RefreshFamilyPath(pairDevA),
		rules.PathDevices + "/" + pairDevB, mobilesession.RefreshFamilyPath(pairDevB),
		pairMemberRoot + "/" + key,
	} {
		if v := f.db.Value(path); v != nil {
			t.Fatalf("%s survived the member removal: %v", path, v)
		}
	}
	for _, path := range []string{
		rules.PathDevices + "/" + pairDevC, mobilesession.RefreshFamilyPath(pairDevC),
		pairMemberRoot + "/" + other,
	} {
		if v := f.db.Value(path); v == nil {
			t.Fatalf("%s was deleted by the removal of another address", path)
		}
	}
	if _, err := pairRefreshWithToken(context.Background(), db, pairRefreshTokenA); err == nil {
		t.Fatal("a refresh with the removed member's token was granted: the family is what the removal is for")
	}
	if family, err := pairRefreshWithToken(context.Background(), db, pairRefreshTokenC); err != nil || family != pairDevC {
		t.Fatalf("a refresh with another address's token = (%q, %v), want its own family left where it was", family, err)
	}
}

// TestPairRevokeFamilyRemovalReportsWhatItLeft is the member removal's other
// ending: a credential that cannot write leaves paths behind, and the removal
// reports them rather than stopping at the first — the same permission is what
// every device and the record need, so a caller that stopped early would name
// one leftover and hide the rest.
func TestPairRevokeFamilyRemovalReportsWhatItLeft(t *testing.T) {
	f := newPairFixture(t, pairMe)
	f.pairSeedDeviceFor(pairDevA, "phone-a", pairMe, "operator")
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevA), map[string]any{"token": pairRefreshTokenA})
	key := pairAddressKey(t, pairMe)
	f.db.Set(pairMemberRoot+"/"+key, map[string]any{"v": pairMemberVersion, "email": pairMe})
	f.db.Deny(rules.PathDevices)
	lc, db := pairRevokeEnv(t, f)

	removal, err := pairRemoveMember(context.Background(), lc, db, pairMe)
	if err == nil {
		t.Fatal("the member removal reported success with the device node denied")
	}
	if len(removal.Failed) != 1 || !strings.Contains(removal.Failed[0], rules.PathDevices+"/"+pairDevA) {
		t.Fatalf("removal.Failed = %v, want the device node it could not delete", removal.Failed)
	}
	if removal.Member != pairMemberRoot+"/"+key {
		t.Fatalf("removal.Member = %q, want the record deleted after the device: a record deleted first would leave the devices it named unfindable", removal.Member)
	}
	if v := f.db.Value(pairMemberRoot + "/" + key); v != nil {
		t.Fatalf("the member record survived a removal whose device write was denied: %v", v)
	}
}
