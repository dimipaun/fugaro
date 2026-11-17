package github

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// installServer answers GET /repos/acme/app/installation with status and the
// installation object, checking the request is the App's own (a JWT).
func installServer(t *testing.T, status int, perms map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("no App JWT on %s", r.URL.Path)
		}
		if r.URL.Path != "/repos/acme/app/installation" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 777, "permissions": perms, "repository_selection": "selected"})
			return
		}
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func checkOpts(t *testing.T, url string) Options {
	return Options{Owner: "acme", Repo: "app", AppID: "12345", PrivateKey: key(t), BaseURL: url}
}

// What Fugaro requires is derived from the code that asks: the union of a run's
// token, a build's and the credentials service's issue route, the higher level
// where two or more name one permission.
func TestRequiredPermissionsAreWhatTheTokensAsk(t *testing.T) {
	want := map[string]string{"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read"}
	got := RequiredPermissions()
	if len(got) != len(want) {
		t.Fatalf("RequiredPermissions = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	// Derived: adding a permission to the run token changes the requirement.
	old := tokenPermissions
	t.Cleanup(func() { tokenPermissions = old })
	tokenPermissions = map[string]string{"contents": "write", "checks": "read"}
	if RequiredPermissions()["checks"] != "read" {
		t.Error("a permission the run token asks for is not required")
	}
}

// TestRequiredPermissionsCarryTheServicesIssueWrite is the owner's ruling of
// 2026-11-17 made mechanical (U3, D2718, B3 of
// docs/orchestrator-graph-estimates/implementation-plan.md): the App's
// installation must grant `issues: write`, which is what the credentials
// service's issue route mints with, while the run's own token keeps the read it
// has always asked for — the write belongs to the service, which performs the
// act and hands no token over.
//
// Both halves matter: a requirement without the mint is a permission nobody
// uses, and a mint without the requirement is a route that mints what the
// installation does not hold and fails at the first issue with GitHub's 422
// instead of at init with the grant named.
func TestRequiredPermissionsCarryTheServicesIssueWrite(t *testing.T) {
	if got := RequiredPermissions()["issues"]; got != "write" {
		t.Errorf("RequiredPermissions issues: %q, want write (the App's installation grants it)", got)
	}
	if got := tokenPermissions["issues"]; got != "read" {
		t.Errorf("the run's token asks for issues: %q, want the read it has always asked for", got)
	}
	if !maps.Equal(IssueTokenPermissions(), map[string]string{"issues": "write", "metadata": "read"}) {
		t.Errorf("IssueTokenPermissions() = %v, want the set the issue route mints", IssueTokenPermissions())
	}
}

// TestCheckInstallationReportsAnIssuesReadGrantAsMissing is the gap the owner's
// ruling creates on an installation granted before it: an App whose installation
// holds `issues: read` where Fugaro requires the write is reported MISSING it, by
// name and with the exact fix, rather than passing the check and failing later
// at the first issue the service opens.
func TestCheckInstallationReportsAnIssuesReadGrantAsMissing(t *testing.T) {
	srv, _ := installServer(t, 200, map[string]string{"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"})
	rep, err := CheckInstallation(context.Background(), checkOpts(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Missing) != 1 || rep.Missing[0].Name != "issues" {
		t.Fatalf("missing = %+v, want the issues grant alone", rep.Missing)
	}
	gap := rep.Missing[0]
	if gap.Want != "write" || gap.Have != "read" {
		t.Errorf("gap = %+v, want write over a read grant", gap)
	}
	wantDisplay := "Issues: Write"
	wantFix := "change Issues to Read and write, then accept the new permission on the installation"
	if gap.Display() != wantDisplay || gap.Fix() != wantFix {
		t.Errorf("Display/Fix = %q / %q, want %q / %q", gap.Display(), gap.Fix(), wantDisplay, wantFix)
	}
	// The same installation with the write granted is clean: the read is not
	// reported as extra and nothing else moves.
	srv, _ = installServer(t, 200, map[string]string{"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read"})
	if rep, err = CheckInstallation(context.Background(), checkOpts(t, srv.URL)); err != nil || len(rep.Missing) != 0 || len(rep.Extra) != 0 {
		t.Fatalf("an installation granting the write: %+v, %v", rep, err)
	}
}

func TestCheckInstallationFine(t *testing.T) {
	srv, seen := installServer(t, 200, map[string]string{"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read"})
	rep, err := CheckInstallation(context.Background(), checkOpts(t, srv.URL))
	if err != nil || rep.InstallationID != 777 || len(rep.Missing) != 0 || len(rep.Forbidden) != 0 || len(rep.Extra) != 0 {
		t.Fatalf("report %+v, %v", rep, err)
	}
	if len(*seen) != 1 {
		t.Errorf("requests %v, want the one installation lookup", *seen)
	}
	// A higher level than needed is fine: issues admin covers issues write.
	srv, _ = installServer(t, 200, map[string]string{"contents": "write", "pull_requests": "write", "issues": "admin", "metadata": "read"})
	if rep, err = CheckInstallation(context.Background(), checkOpts(t, srv.URL)); err != nil || len(rep.Missing) != 0 {
		t.Fatalf("a higher level was refused: %+v, %v", rep, err)
	}
}

func TestCheckInstallationNotInstalled(t *testing.T) {
	srv, _ := installServer(t, 404, nil)
	_, err := CheckInstallation(context.Background(), checkOpts(t, srv.URL))
	var ni *NotInstalledError
	if !errors.As(err, &ni) || ni.AppID != "12345" || ni.Repo != "acme/app" {
		t.Fatalf("err = %v, want a *NotInstalledError", err)
	}
	want := "the GitHub App 12345 is not installed on acme/app: install it on this repository only (Settings, GitHub Apps, Install App)"
	if err.Error() != want {
		t.Errorf("message %q, want %q", err, want)
	}
}

func TestCheckInstallationNamesEachGap(t *testing.T) {
	srv, _ := installServer(t, 200, map[string]string{"contents": "read", "metadata": "read", "workflows": "write", "administration": "write", "actions": "read"})
	rep, err := CheckInstallation(context.Background(), checkOpts(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, m := range rep.Missing {
		missing = append(missing, m.Display()+" ("+m.Fix()+")")
	}
	slices.Sort(missing)
	want := []string{
		"Contents: Write (change Contents to Read and write, then accept the new permission on the installation)",
		"Issues: Write (add Issues: Write, then accept the new permission on the installation)",
		"Pull requests: Write (add Pull requests: Write, then accept the new permission on the installation)",
	}
	if !slices.Equal(missing, want) {
		t.Errorf("missing:\n%s\nwant:\n%s", strings.Join(missing, "\n"), strings.Join(want, "\n"))
	}
	if !slices.Equal(rep.Forbidden, []string{"Workflows: Write"}) {
		t.Errorf("forbidden = %v: Fugaro must never hold Workflows: Write", rep.Forbidden)
	}
	if !slices.Equal(rep.Extra, []string{"Actions: Read", "Administration: Write"}) {
		t.Errorf("extra = %v", rep.Extra)
	}
}

// Anything but a clean answer is an error that says nothing about the App
// being fine, and no message holds the JWT or the key.
func TestCheckInstallationFailuresAndNoSecrets(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		srv, _ := installServer(t, status, nil)
		_, err := CheckInstallation(context.Background(), checkOpts(t, srv.URL))
		var ni *NotInstalledError
		if err == nil || errors.As(err, &ni) {
			t.Fatalf("status %d: %v", status, err)
		}
		var bad *BadCredentialsError
		if status == 401 != errors.As(err, &bad) {
			t.Errorf("status %d: bad credentials = %v (%v)", status, errors.As(err, &bad), err)
		}
		for _, secret := range []string{string(pkcs1PEM(t)), "BEGIN RSA PRIVATE KEY", "eyJ"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("status %d: the error holds %q: %v", status, secret, err)
			}
		}
	}
	// A server that is not there.
	srv, _ := installServer(t, 200, nil)
	url := srv.URL
	srv.Close()
	if _, err := CheckInstallation(context.Background(), checkOpts(t, url)); err == nil {
		t.Fatal("an unreachable API was not an error")
	}
}

// An owner or repository that is not a plain name never reaches a request path.
func TestCheckInstallationRefusesOddNames(t *testing.T) {
	srv, seen := installServer(t, 200, fullPermsForTest)
	for _, c := range [][2]string{{"acme", ".."}, {"..", "app"}, {"a%2Fb", "x"}, {"acme", "app/../../x"}, {"acme", "a b"}} {
		o := checkOpts(t, srv.URL)
		o.Owner, o.Repo = c[0], c[1]
		if _, err := CheckInstallation(context.Background(), o); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("requests were sent: %v", *seen)
	}
}

var fullPermsForTest = map[string]string{"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read"}
