package gcpfake

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// secretCall is one call to the Secret Manager fake, answered as the client
// sees it.
func secretCall(t *testing.T, f *Secrets, method, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, f.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// A fresh fake has taken nothing away, and the two destructive calls are
// recorded: "Fugaro never deletes a secret or a secret version" is asserted
// through these, so a run that made one cannot pass.
func TestSecretsFakeRecordsWhatADeleteAndADestroyTake(t *testing.T) {
	f := NewSecrets(t)
	f.Seed("claude-oauth-token", map[string]string{"fugaro": "managed", "fugaro_secret": "claude-oauth-token"}, []byte("token"))
	if len(f.Deleted()) != 0 || len(f.Destroyed()) != 0 {
		t.Fatalf("a fresh fake took %q / %q", f.Deleted(), f.Destroyed())
	}

	if code, body := secretCall(t, f, http.MethodPost, "/v1/projects/p/secrets/claude-oauth-token/versions/1:destroy"); code != http.StatusOK {
		t.Fatalf("destroy: %d %s", code, body)
	}
	if got := f.Destroyed(); !slices.Equal(got, []string{"projects/p/secrets/claude-oauth-token/versions/1"}) {
		t.Fatalf("Destroyed = %q", got)
	}

	if code, body := secretCall(t, f, http.MethodDelete, "/v1/projects/p/secrets/claude-oauth-token/versions/1"); code != http.StatusOK {
		t.Fatalf("versions.delete: %d %s", code, body)
	}
	if got := f.Destroyed(); len(got) != 2 {
		t.Fatalf("Destroyed = %q, want the destroy and the version delete", got)
	}
	if f.Latest("claude-oauth-token") != nil {
		t.Fatal("the deleted version is still there")
	}
	if code, _ := secretCall(t, f, http.MethodDelete, "/v1/projects/p/secrets/claude-oauth-token/versions/1"); code != http.StatusNotFound {
		t.Fatalf("deleting a version that is gone: %d, want 404", code)
	}

	if code, _ := secretCall(t, f, http.MethodDelete, "/v1/projects/p/secrets/claude-oauth-token"); code != http.StatusOK {
		t.Fatalf("secrets.delete: %d", code)
	}
	if got := f.Deleted(); !slices.Equal(got, []string{"claude-oauth-token"}) {
		t.Fatalf("Deleted = %q", got)
	}
	if code, _ := secretCall(t, f, http.MethodDelete, "/v1/projects/p/secrets/claude-oauth-token"); code != http.StatusNotFound {
		t.Fatalf("deleting a secret that is gone: %d, want 404", code)
	}
}

// A version a disable took is recorded, reported DISABLED by the version
// list, and its value is no longer readable — as real Secret Manager refuses
// a DISABLED version's access, so a test can observe the retirement and not
// only the call. The version that was not disabled is untouched.
func TestSecretsFakeDisablesOneVersionAndRefusesItsValue(t *testing.T) {
	f := NewSecrets(t)
	f.Seed("creds-bundle", nil, []byte("first"))
	f.AllowAccess()
	if code, body := secretCall(t, f, http.MethodPost, "/v1/projects/p/secrets/creds-bundle:addVersion"); code == http.StatusOK {
		t.Fatalf("an empty addVersion was answered: %s", body) // no body: the fake refuses it
	}
	// A second version, through the API, so the newest one is ENABLED.
	code, body := secretCall(t, f, http.MethodPost, "/v1/projects/p/secrets/creds-bundle/versions/1:disable")
	if code != http.StatusOK || !strings.Contains(body, `"state":"DISABLED"`) {
		t.Fatalf("disable: %d %s", code, body)
	}
	if got := f.Disabled(); !slices.Equal(got, []string{"projects/p/secrets/creds-bundle/versions/1"}) {
		t.Fatalf("Disabled = %q", got)
	}
	if f.Deleted() != nil || f.Destroyed() != nil {
		t.Fatalf("a disable took something away: %q / %q", f.Deleted(), f.Destroyed())
	}
	if code, body := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets/creds-bundle/versions/latest:access"); code != http.StatusBadRequest || !strings.Contains(body, "disabled") {
		t.Fatalf("the value of a disabled latest version: %d %s, want 400 and \"disabled\"", code, body)
	}
	if code, _ := secretCall(t, f, http.MethodPost, "/v1/projects/p/secrets/creds-bundle/versions/9:disable"); code != http.StatusNotFound {
		t.Fatalf("disabling a version that is not there: %d, want 404", code)
	}
}

// secrets.list is the label-filtered call a retirement discovers a kept
// container with, and it can be refused: the two halves of Task 4's
// degradation test.
func TestSecretsFakeListFiltersByLabelAndCanBeRefused(t *testing.T) {
	f := NewSecrets(t)
	f.Seed("ours", map[string]string{"fugaro": "managed", "fugaro_repo": "x", "fugaro_secret": "ours"}, nil)
	f.Seed("another-repo", map[string]string{"fugaro": "managed", "fugaro_repo": "y", "fugaro_secret": "theirs"}, nil)

	code, body := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets?filter=labels.fugaro%3Dmanaged%20AND%20labels.fugaro_repo%3Dx")
	if code != http.StatusOK || !strings.Contains(body, `"ours"`) || strings.Contains(body, "another-repo") {
		t.Fatalf("the label filter: %d %s", code, body)
	}
	// A filter the fake does not implement is a fake bug, not a silent
	// match-everything: it fails the test through the server's failf.
	var failed string
	f.failf = func(format string, args ...any) { failed = format }
	if code, _ := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets?filter=labels.fugaro%3Dmanaged%20OR%20labels.fugaro_repo%3Dx"); code == http.StatusOK || failed == "" {
		t.Fatalf("an unimplemented filter was answered: %d", code)
	}

	f.FailListWith = http.StatusForbidden
	if code, _ := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets?filter=labels.fugaro%3Dmanaged"); code != http.StatusForbidden {
		t.Fatalf("a refused list: %d, want 403", code)
	}
}
