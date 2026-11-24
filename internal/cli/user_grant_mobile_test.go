package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/mobilesession"
)

// user_grant_mobile_test.go is P9's half of `fugaro user revoke` (O14): the
// roster is the one source of membership and role, so a revoke that takes a
// person's last pair away ends their mobile access with it — the member record,
// every device node of the address and every refresh family, through P7's
// pairRemoveMember — and a person who keeps another pair keeps their devices.
//
// The command runs on the pairing fixture, whose database holds the mobile
// nodes as well as the grants node, and with no terminal and no agent marker:
// every case below is a command that finishes on its own, which is the
// acceptance's own last clause. The caller is stubbed (userRevokeMobileAs), so
// no case reaches Google's userinfo endpoint.

// userRevokeMobileRun runs `fugaro user revoke` as the person would, with no
// terminal on its input.
func userRevokeMobileRun(t *testing.T, member, role, scope string) (string, string, error) {
	t.Helper()
	noAgentSession(t)
	noTerminal(t)
	cmd := newUserCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs([]string{"revoke", member, "--role", role, "--scope", scope})
	err := cmd.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// userRevokeMobileAs is the credential the command judges with: the fixture's
// own person-1, in the canonical member form the grants node holds it, where
// the fixture places them as the project's admin.
func userRevokeMobileAs(t *testing.T) {
	t.Helper()
	member, err := accesslib.Member(pairMe)
	if err != nil {
		t.Fatalf("the fixture's own member: %v", err)
	}
	userPairAs(t, member)
}

// TestUserRevokeMobileEndsTheLastGrant is P9's red step: revoking a person's
// last roster grant deletes their member record, every device node of the
// address and every refresh family with it (O14), so the address holds no phone
// that can reach the project. It fails before this task's change — the grants
// node loses the pair and the device node is still there.
func TestUserRevokeMobileEndsTheLastGrant(t *testing.T) {
	f := newPairFixture(t, pairMe)
	userRevokeMobileAs(t)
	f.pairSeedDeviceFor(pairDevC, "phone-c", pairLauncher, "operator")
	f.pairSeedItem(t, pairDevC)
	f.pairSeedPush(pairDevC)
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevC), map[string]any{"token": pairRefreshTokenC})
	key := pairAddressKey(t, pairLauncher)
	f.db.Set(pairMemberRoot+"/"+key, map[string]any{
		"v": pairMemberVersion, "email": pairLauncher, "at": pairT0.UTC().Format(time.RFC3339),
		"provider": "google.com", "sub": "subject-for-tests",
	})

	out, _, err := userRevokeMobileRun(t, "user:"+pairLauncher, "operator", "project")
	if err != nil {
		t.Fatalf("revoking the last pair: %v\n%s", err, out)
	}
	for _, path := range []string{
		rules.PathDevices + "/" + pairDevC,
		mobilesession.RefreshFamilyPath(pairDevC),
		pairPushPath(pairDevC),
		pairMemberRoot + "/" + key,
	} {
		if v := f.db.Value(path); v != nil {
			t.Errorf("%s survived the removal of the address's last grant: %v\n%s", path, v, out)
		}
	}
	for _, want := range []string{"no longer holds operator at project", "mobile access"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command does not say %q:\n%s", want, out)
		}
	}

	// The reason the family is deleted: a phone of the address that kept its
	// token holds a session that no longer refreshes.
	_, db := pairRevokeEnv(t, f)
	if family, err := pairRefreshWithToken(context.Background(), db, pairRefreshTokenC); err == nil {
		t.Errorf("a refresh with the removed member's token was granted the family %q: the roster refuses the address, and the family is the session it holds", family)
	}
}

// TestUserRevokeMobileKeepsTheDevicesOfAnotherGrant is the other half of O14:
// the removal is the LAST grant's act. A person who keeps another pair keeps
// their devices, their family and their record — the roster still holds them,
// and a device's role follows what it holds at the next refresh.
func TestUserRevokeMobileKeepsTheDevicesOfAnotherGrant(t *testing.T) {
	f := newPairFixture(t, pairMe)
	userRevokeMobileAs(t)
	f.pairSeedGrants(t,
		pairGrant(t, pairMe, accesslib.RoleAdmin, "project"),
		pairGrant(t, pairLauncher, accesslib.RoleOperator, "project"),
		pairGrant(t, pairLauncher, accesslib.RoleViewer, "github/acme/*"))
	f.pairSeedDeviceFor(pairDevC, "phone-c", pairLauncher, "operator")
	f.pairSeedItem(t, pairDevC)
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevC), map[string]any{"token": pairRefreshTokenC})
	key := pairAddressKey(t, pairLauncher)
	f.db.Set(pairMemberRoot+"/"+key, map[string]any{"v": pairMemberVersion, "email": pairLauncher})

	out, _, err := userRevokeMobileRun(t, "user:"+pairLauncher, "operator", "project")
	if err != nil {
		t.Fatalf("revoking one of two pairs: %v\n%s", err, out)
	}
	for _, path := range []string{
		rules.PathDevices + "/" + pairDevC,
		mobilesession.RefreshFamilyPath(pairDevC),
		pairMemberRoot + "/" + key,
	} {
		if v := f.db.Value(path); v == nil {
			t.Errorf("%s was deleted by the revoke of a pair the address still holds another of: O14 makes the removal the last grant's act", path)
		}
	}
	want := []string{
		"user:" + pairMe + " (admin) at project",
		"user:" + pairLauncher + " (viewer) at github/acme/*",
	}
	got := userNodeLines(t, f.db)
	if len(got) != len(want) {
		t.Fatalf("the grants node holds %v, want %v", got, want)
	}
	for _, w := range want {
		if !containsLine(got, w) {
			t.Errorf("the grants node = %v, want it to hold %q", got, w)
		}
	}
}

// TestUserRevokeMobileReportsWhatItLeft: the grants write lands first, so a
// removal that could not complete leaves paths behind on a person who is
// already off the roster. The command says so and names them — the roster
// refuses the address at its next refresh either way, and what is left is the
// operator's to delete by hand.
func TestUserRevokeMobileReportsWhatItLeft(t *testing.T) {
	f := newPairFixture(t, pairMe)
	userRevokeMobileAs(t)
	f.pairSeedDeviceFor(pairDevC, "phone-c", pairLauncher, "operator")
	f.pairSeedItem(t, pairDevC)
	f.db.Deny(rules.PathDevices)

	out, _, err := userRevokeMobileRun(t, "user:"+pairLauncher, "operator", "project")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want exit %d: a removal that could not complete is not a success\n%s", err, ExitUserError, out)
	}
	if !strings.Contains(err.Error(), rules.PathDevices+"/"+pairDevC) {
		t.Errorf("the refusal does not name the node it could not delete: %v", err)
	}
	if !strings.Contains(out, "no longer holds operator at project") {
		t.Errorf("the refusal reads as if nothing had happened: the grants write landed first\n%s", out)
	}
	if v := f.db.Value(rules.PathDevices + "/" + pairDevC); v == nil {
		t.Fatal("a write the database denied deleted the device node anyway")
	}
}

// TestUserRevokeMobileReachesTheRecordOfASpelledAddress: the roster's member form
// folds the domain alone (access.Member), while the invitation keys the member
// record on the address with its local part folded too (pairAdministeredEmail).
// The removal takes the address as the invitation named it, and the database
// accepts a Patch of a key that is not there, so a revoke of user:Alice@example.com
// that passed the roster's spelling on would leave alice@example.com's record —
// and its devices — in place and still report the record deleted.
func TestUserRevokeMobileReachesTheRecordOfASpelledAddress(t *testing.T) {
	f := newPairFixture(t, pairMe)
	userRevokeMobileAs(t)
	const (
		spelled   = "Alice@example.com"
		canonical = "alice@example.com"
	)
	f.pairSeedGrants(t,
		pairGrant(t, pairMe, accesslib.RoleAdmin, "project"),
		pairGrant(t, spelled, accesslib.RoleOperator, "project"),
	)
	f.pairSeedDeviceFor(pairDevC, "phone-c", canonical, "operator")
	f.pairSeedItem(t, pairDevC)
	f.db.Set(mobilesession.RefreshFamilyPath(pairDevC), map[string]any{"token": pairRefreshTokenC})
	key := pairAddressKey(t, canonical)
	f.db.Set(pairMemberRoot+"/"+key, map[string]any{"v": pairMemberVersion, "email": canonical})

	out, _, err := userRevokeMobileRun(t, "user:"+spelled, "operator", "project")
	if err != nil {
		t.Fatalf("revoking the last pair of a spelled address: %v\n%s", err, out)
	}
	for _, path := range []string{
		rules.PathDevices + "/" + pairDevC,
		mobilesession.RefreshFamilyPath(pairDevC),
		pairMemberRoot + "/" + key,
	} {
		if v := f.db.Value(path); v != nil {
			t.Errorf("%s survived the removal of the address's last grant: %v\n%s", path, v, out)
		}
	}
	if !strings.Contains(out, pairMemberRoot+"/"+key) {
		t.Errorf("the command does not name the record it deleted (%s): a removal keyed on another spelling deletes nothing and reports success anyway\n%s", pairMemberRoot+"/"+key, out)
	}
}

// containsLine reports whether lines holds want, for the grants node's own
// order not to matter.
func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
