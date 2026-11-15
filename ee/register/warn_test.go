// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The tests of the licence's warnings (plan K3.5): who a key's own `warnings`
// reaches, and when, and that the tagged CLI's licence commands say so.
//
// They drive the tagged tree over licence_test.go's fixture — a file:// runs
// bucket, gcpfake's endpoints, a key minted in the process that reads it — so
// the field `licence status --json` carries is read back through the decoder a
// reader outside this process uses rather than through the struct under test.
//
// The three states of the default policy (spec section 7.1, the same two
// windows keyservice's policy mints) are one test each: the first week of grace
// reaches admins alone, the last reaches every role, and a key inside its paid
// term reaches nobody.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/internal/access"
)

// licenceWarnPolicy is the default key policy's own window list (spec section
// 7.1): admins from the first moment of grace, every role from a week in.
var licenceWarnPolicy = []licence.WarningV1{
	{Roles: []string{"admin"}, From: "grace_start"},
	{Roles: []string{"admin", "operator", "viewer"}, From: "grace_start+7d"},
}

// licenceWarnFixture installs a key whose paid term ends at the fixture's own
// clock, so the state at that instant is active and every instant after it is
// the key's own grace (thirty days).
func licenceWarnFixture(t *testing.T) (*licenceFixture, licence.Claims) {
	t.Helper()
	f := newLicenceFixture(t)
	claims := f.claims(func(c *licence.Claims) {
		c.PaidThrough = f.now
		c.GraceDays = 30
		c.Warnings = licenceWarnPolicy
	})
	install(t, f, f.issuer.mint(t, claims))
	return f, claims
}

// terms reads one claim set against a state the way the rules do, which is what
// `set` and `refresh` hand them: the key's own policy and dates.
func terms(claims licence.Claims, state licence.State) licenceWarningTerms {
	return licenceWarningTermsOfKey(claims, state)
}

// warningField is `licence status --json`'s warning, and whether the field is
// there at all: null and absent are different answers for a reader that has to
// tell "no window is open" from a CLI old enough not to carry the field.
func warningField(t *testing.T, status map[string]any) (map[string]any, bool) {
	t.Helper()
	value, present := status["warning"]
	if !present {
		t.Fatalf("licence status --json carries no warning field: %v", status)
	}
	if value == nil {
		return nil, false
	}
	warning, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("licence status --json's warning is %#v, want an object or null", value)
	}
	return warning, true
}

// warningWindow is one window of that field as a reader outside this process
// decodes it, and answers the roles it reaches and whether it reaches one.
func warningWindow(t *testing.T, warning map[string]any, i int) (opens string, roles []string) {
	t.Helper()
	windows, ok := warning["windows"].([]any)
	if !ok {
		t.Fatalf("licence status --json's warning carries no windows list: %#v", warning)
	}
	if i >= len(windows) {
		t.Fatalf("the warning carries %d windows, and this test reads window %d: %#v", len(windows), i, windows)
	}
	window, ok := windows[i].(map[string]any)
	if !ok {
		t.Fatalf("window %d of the warning is %#v, want an object", i, windows[i])
	}
	opens, _ = window["opens"].(string)
	list, _ := window["roles"].([]any)
	for _, role := range list {
		name, _ := role.(string)
		roles = append(roles, name)
	}
	if opens == "" || len(roles) == 0 {
		t.Fatalf("window %d of the warning is %#v, want an instant and the roles it reaches", i, window)
	}
	return opens, roles
}

// assertTheLineNames is what a test holds the warning a command writes to: one
// line of the command's standard error that names the instant the grace runs
// out, which is the fact the whole warning is for. It is one line among the
// notes the command writes, which is why this looks for it rather than reading
// the stream whole.
func assertTheLineNames(t *testing.T, errOut string, claims licence.Claims) {
	t.Helper()
	ends := claims.PaidThrough.Add(time.Duration(claims.GraceDays) * 24 * time.Hour).UTC().Format(time.RFC3339)
	for _, line := range strings.Split(errOut, "\n") {
		if strings.Contains(line, ends) && strings.Contains(line, "fugaro licence") {
			return
		}
	}
	t.Fatalf("no line of the command's standard error names the end of the grace (%s) and what to run about it:\n%s", ends, errOut)
}

func TestAdminsOnlyInTheFirstWeekOfGrace(t *testing.T) {
	f, claims := licenceWarnFixture(t)
	f.now = claims.PaidThrough.Add(3 * 24 * time.Hour)

	warning, open := warningField(t, f.status(t))
	if !open {
		t.Fatal("a key in the first week of its grace warns nobody: licence status --json's warning is null")
	}
	opens, roles := warningWindow(t, warning, 0)
	if !reflect.DeepEqual(roles, []string{"admin"}) {
		t.Errorf("the first window reaches %#v, want admins alone", roles)
	}
	if opens != claims.PaidThrough.UTC().Format(time.RFC3339) {
		t.Errorf("the first window opens at %q, want the start of the grace (%s)", opens, claims.PaidThrough.UTC().Format(time.RFC3339))
	}
	// One window and not two: the second opens a week in, and a field that
	// named every window the key carries would warn everyone from day one.
	if windows := licenceWarnings(terms(claims, licence.Grace), f.now); len(windows) != 1 {
		t.Errorf("the windows open three days into the grace are %+v, and only the first of the key's is", windows)
	}
	if windows, _ := warning["windows"].([]any); len(windows) != 1 {
		t.Errorf("licence status --json carries %d windows three days into the grace, and the key's second one opens a week in", len(windows))
	}
	message, _ := warning["message"].(string)
	if message == "" || strings.Contains(message, "\n") {
		t.Errorf("the warning's message is %q, want one sentence naming the end of the grace", message)
	}

	for _, role := range []access.Role{access.RoleOperator, access.RoleViewer} {
		if window, warned := licenceWarningFor(terms(claims, licence.Grace), f.now, role); warned {
			t.Errorf("a %s is warned in the first week of grace by the window %+v, and only admins are", role, window)
		}
	}
	if _, warned := licenceWarningFor(terms(claims, licence.Grace), f.now, access.RoleAdmin); !warned {
		t.Error("an admin is not warned in the first week of grace, which is the whole of the first window")
	}

	_, errOut, err := f.exec(t, "", "licence", "status")
	if err != nil {
		t.Fatalf("licence status = %v", err)
	}
	assertTheLineNames(t, errOut, claims)
}

func TestEveryoneInTheLast(t *testing.T) {
	f, claims := licenceWarnFixture(t)
	f.now = claims.PaidThrough.Add(20 * 24 * time.Hour)

	warning, open := warningField(t, f.status(t))
	if !open {
		t.Fatal("a key in the last week of its grace warns nobody: licence status --json's warning is null")
	}
	// Both of the key's windows are open, and the second is the one that has
	// just done it: it opened a week into the grace, which is the instant the
	// developers were reached.
	windows := licenceWarnings(terms(claims, licence.Grace), f.now)
	if len(windows) != 2 || !windows[1].Opens.Equal(claims.PaidThrough.Add(7*24*time.Hour)) {
		t.Fatalf("the windows open twenty days into the grace are %+v, want both of the key's with the second a week in", windows)
	}
	// The field carries them apart, each with its own opening instant: a reader
	// whose role the first window does not reach must not be told warnings have
	// been on since the start of the grace.
	firstOpens, firstRoles := warningWindow(t, warning, 0)
	if !reflect.DeepEqual(firstRoles, []string{"admin"}) || firstOpens != claims.PaidThrough.UTC().Format(time.RFC3339) {
		t.Errorf("the field's first window is %q for %#v, want the start of the grace (%s) for admins alone", firstOpens, firstRoles, claims.PaidThrough.UTC().Format(time.RFC3339))
	}
	secondOpens, secondRoles := warningWindow(t, warning, 1)
	if !reflect.DeepEqual(secondRoles, []string{"admin", "operator", "viewer"}) {
		t.Errorf("the field's second window reaches %#v, want every role", secondRoles)
	}
	week := claims.PaidThrough.Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if secondOpens != week {
		t.Errorf("the field's second window opens at %q, want a week into the grace (%s): a viewer is reached then and not at its start", secondOpens, week)
	}
	if windows, _ := warning["windows"].([]any); len(windows) != 2 {
		t.Errorf("licence status --json carries %d windows twenty days into the grace, want both of the key's", len(windows))
	}

	for _, role := range access.Roles() {
		if _, warned := licenceWarningFor(terms(claims, licence.Grace), f.now, role); !warned {
			t.Errorf("a %s is not warned in the last week of grace, and the last window reaches every role so a developer can nudge an admin", role)
		}
	}

	_, errOut, err := f.exec(t, "", "licence", "status")
	if err != nil {
		t.Fatalf("licence status = %v", err)
	}
	assertTheLineNames(t, errOut, claims)
}

func TestNoWarningWhenActive(t *testing.T) {
	f, claims := licenceWarnFixture(t)

	// The fixture's key is paid through the fixture's own instant, so this is
	// the last moment of the paid term and not one of grace.
	if _, open := warningField(t, f.status(t)); open {
		t.Error("a key inside its paid term is warned, and the window table has nobody in it while the key is active")
	}
	for _, role := range access.Roles() {
		if window, warned := licenceWarningFor(terms(claims, licence.Active), f.now, role); warned {
			t.Errorf("a %s is warned by %+v while the key is active", role, window)
		}
	}

	out, errOut, err := f.exec(t, "", "licence", "status")
	if err != nil {
		t.Fatalf("licence status = %v", err)
	}
	if strings.Contains(out+errOut, "grace") {
		t.Errorf("licence status warns about a key inside its paid term:\nstdout: %s\nstderr: %s", out, errOut)
	}
}

// TestAnAbsurdGraceOrWindowIsNotAShorterOne pins the bound the window
// arithmetic shares with the verifier's own (ee/licence's MaxIntervalDays):
// a claim set carrying a count of days neither a duration nor this build can
// hold must name the longest span there is, never one wrapped into the past —
// a grace said to have ended before the paid term did, or a window reported
// open decades ago.
func TestAnAbsurdGraceOrWindowIsNotAShorterOne(t *testing.T) {
	f := newLicenceFixture(t)
	// The count is past the ~106,751 days a duration holds, so multiplying it by
	// a day wraps: the grace the key carries has to be clamped rather than
	// wrapped, and a window written that far after the grace start cannot be
	// placed at all.
	absurd := 1 << 20
	claims := f.claims(func(c *licence.Claims) {
		c.PaidThrough = f.now
		c.GraceDays = absurd
		c.Warnings = []licence.WarningV1{
			{Roles: []string{"admin"}, From: "grace_start"},
			{Roles: []string{"admin", "operator", "viewer"}, From: fmt.Sprintf("grace_start+%dd", absurd)},
		}
	})
	install(t, f, f.issuer.mint(t, claims))
	f.now = claims.PaidThrough.Add(24 * time.Hour)

	// The verifier clamps the same claim set instead of wrapping it, which is
	// the state every window below is read against.
	if got := f.status(t)["state"]; got != string(licence.Grace) {
		t.Fatalf("a key with %d days of grace the day after its paid term reads %v, want %s", absurd, got, licence.Grace)
	}

	held := terms(claims, licence.Grace)
	end := licenceWarnGraceEnd(held)
	want := claims.PaidThrough.Add(time.Duration(licence.MaxIntervalDays) * 24 * time.Hour)
	if !end.Equal(want) {
		t.Errorf("the grace of a key with %d days ends at %s, want the longest span this build can say (%s)", absurd, end, want)
	}

	if _, ok := licenceWarningOpens(claims.PaidThrough, fmt.Sprintf("grace_start+%dd", absurd)); ok {
		t.Errorf("a window opening %d days into the grace is placed, and a count past the longest duration there is cannot be", absurd)
	}
	windows := licenceWarnings(held, f.now)
	if len(windows) != 1 || !windows[0].Opens.Equal(claims.PaidThrough) {
		t.Errorf("the windows of a key carrying an unplaceable one are %+v, want the one that opens at the start of the grace (%s)", windows, claims.PaidThrough)
	}

	// The sentence a person acts on names an instant the grace has not reached
	// yet, rather than one in the past.
	_, errOut, err := f.exec(t, "", "licence", "status")
	if err != nil {
		t.Fatalf("licence status = %v", err)
	}
	if end.Before(f.now) {
		t.Errorf("the warning names %s as the end of a grace that is still running, and the key reads %s", end, f.status(t)["state"])
	}
	if !strings.Contains(errOut, end.UTC().Format(time.RFC3339)) {
		t.Errorf("no line of the command names the end of the grace (%s):\n%s", end.UTC().Format(time.RFC3339), errOut)
	}
}

// TestSettingAKeySaysTheGraceItLandedIn pins the line at the second of the
// three commands that write it: a key that lands in its grace is one a person
// has to act on, and the command that just installed it is where they are
// looking.
func TestSettingAKeySaysTheGraceItLandedIn(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, claims := f.bundle(t, func(c *licence.Claims) {
		c.PaidThrough = f.now
		c.GraceDays = 30
		c.Warnings = licenceWarnPolicy
	})
	f.seedSecretContainer(nil)
	f.now = claims.PaidThrough.Add(3 * 24 * time.Hour)

	if out, errOut, err := f.exec(t, key+"\n"+secret+"\n", "licence", "set"); err != nil {
		t.Fatalf("licence set = %v (stdout %q, stderr %q)", err, out, errOut)
	} else {
		assertTheLineNames(t, errOut, claims)
		if strings.Contains(out, "grace ends") {
			t.Errorf("the warning landed in licence set's standard output:\n%s", out)
		}
	}
	if got := f.status(t)["state"]; got != string(licence.Grace) {
		t.Errorf("the key set three days into its grace reads %v, want %s", got, licence.Grace)
	}
}

// TestRefreshingAKeySaysTheGraceItLandedIn pins the line at the third command
// that writes it. The renewal is answered with a key that is already inside its
// own grace — a term that starts in the past, which is what a server catching
// up an installation it has not heard from sends — so the writer and the reader
// of the line are the same key's.
func TestRefreshingAKeySaysTheGraceItLandedIn(t *testing.T) {
	f := newLicenceFixture(t)
	installed, installedSecret, installedClaims := f.bundle(t, func(c *licence.Claims) {
		c.PaidThrough = f.now
		c.GraceDays = 30
		c.Warnings = licenceWarnPolicy
	})
	f.seedSecretContainer([]byte(installedSecret))
	f.secrets.AllowAccess()
	install(t, f, installed)
	f.now = installedClaims.PaidThrough.Add(3 * 24 * time.Hour)

	next, nextSecret, nextClaims := f.bundle(t, func(c *licence.Claims) {
		c.KeyID = "lk_test_k35_successor"
		c.PaidThrough = installedClaims.PaidThrough.Add(24 * time.Hour)
		c.GraceDays = 30
		c.Warnings = licenceWarnPolicy
	})
	f.serveLicenceServer(t, func(keyservice.RenewalCall) (int, string) {
		answer, err := json.Marshal(keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
		if err != nil {
			t.Fatal(err)
		}
		return http.StatusOK, string(answer)
	})

	out, errOut, err := f.exec(t, "", "licence", "refresh")
	if err != nil {
		t.Fatalf("licence refresh = %v (stdout %q, stderr %q)", err, out, errOut)
	}
	assertTheLineNames(t, errOut, nextClaims)
	if strings.Contains(out, "grace ends") {
		t.Errorf("the warning landed in licence refresh's standard output:\n%s", out)
	}
}

// TestAnOfflineKeyIsNotToldToRefresh holds the sentence to the key's own
// renewal mode: a `renewal_mode: none` key never contacts the licence server
// (spec section 7), and `fugaro licence refresh` answers it with a note and no
// renewal, so a warning that sent its owner there would send them to a command
// that does nothing.
func TestAnOfflineKeyIsNotToldToRefresh(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims(func(c *licence.Claims) {
		c.PaidThrough = f.now
		c.GraceDays = 30
		c.RenewalMode = licence.RenewalNone
		c.CheckInterval = nil
		c.Warnings = licenceWarnPolicy
	})
	install(t, f, f.issuer.mint(t, claims))
	f.now = claims.PaidThrough.Add(3 * 24 * time.Hour)

	_, errOut, err := f.exec(t, "", "licence", "status")
	if err != nil {
		t.Fatalf("licence status = %v", err)
	}
	assertTheLineNames(t, errOut, claims)
	if strings.Contains(errOut, "fugaro licence refresh") {
		t.Errorf("an offline key is told to renew with a command that answers it with a note and no renewal:\n%s", errOut)
	}
	if !strings.Contains(errOut, "fugaro licence set") {
		t.Errorf("the line for an offline key does not name the one command that installs its successor:\n%s", errOut)
	}

	warning, open := warningField(t, f.status(t))
	if !open {
		t.Fatal("an offline key three days into its grace warns nobody: licence status --json's warning is null")
	}
	message, _ := warning["message"].(string)
	if strings.Contains(message, "fugaro licence refresh") || !strings.Contains(message, "fugaro licence set") {
		t.Errorf("the field's message is %q, want the one command that renews an offline key (`fugaro licence set`) and not the one that cannot", message)
	}
}
