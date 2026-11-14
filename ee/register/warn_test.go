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
	"reflect"
	"strings"
	"testing"
	"time"

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
	if got := warning["roles"]; !reflect.DeepEqual(got, []any{"admin"}) {
		t.Errorf("the first window reaches %#v, want admins alone", got)
	}
	if got := warning["opens"]; got != claims.PaidThrough.UTC().Format(time.RFC3339) {
		t.Errorf("the first window opens at %#v, want the start of the grace (%s)", got, claims.PaidThrough.UTC().Format(time.RFC3339))
	}
	// One window and not two: the second opens a week in, and a field that
	// named every window the key carries would warn everyone from day one.
	if windows := licenceWarnings(terms(claims, licence.Grace), f.now); len(windows) != 1 {
		t.Errorf("the windows open three days into the grace are %+v, and only the first of the key's is", windows)
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
	if got := warning["roles"]; !reflect.DeepEqual(got, []any{"admin", "operator", "viewer"}) {
		t.Errorf("the last window reaches %#v, want every role", got)
	}
	// Both of the key's windows are open, and the second is the one that has
	// just done it: it opened a week into the grace, which is the instant the
	// developers were reached.
	windows := licenceWarnings(terms(claims, licence.Grace), f.now)
	if len(windows) != 2 || !windows[1].Opens.Equal(claims.PaidThrough.Add(7*24*time.Hour)) {
		t.Fatalf("the windows open twenty days into the grace are %+v, want both of the key's with the second a week in", windows)
	}
	if got := warning["opens"]; got != claims.PaidThrough.UTC().Format(time.RFC3339) {
		t.Errorf("the field's opens is %#v, want the instant warnings began at all (%s)", got, claims.PaidThrough.UTC().Format(time.RFC3339))
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
