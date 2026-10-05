package github

import (
	"context"
	"encoding/json"
	"errors"
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

// What runs ask for is derived from the code that asks: the union of a run's
// token and a build's, the higher level where both name a permission.
func TestRequiredPermissionsAreWhatTheTokensAsk(t *testing.T) {
	want := map[string]string{"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"}
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

func TestCheckInstallationFine(t *testing.T) {
	srv, seen := installServer(t, 200, map[string]string{"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"})
	rep, err := CheckInstallation(context.Background(), checkOpts(t, srv.URL))
	if err != nil || rep.InstallationID != 777 || len(rep.Missing) != 0 || len(rep.Forbidden) != 0 || len(rep.Extra) != 0 {
		t.Fatalf("report %+v, %v", rep, err)
	}
	if len(*seen) != 1 {
		t.Errorf("requests %v, want the one installation lookup", *seen)
	}
	// A higher level than needed is fine: issues write covers issues read.
	srv, _ = installServer(t, 200, map[string]string{"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read"})
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
		"Issues: Read (add Issues: Read, then accept the new permission on the installation)",
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
