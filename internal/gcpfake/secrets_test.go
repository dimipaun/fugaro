package gcpfake

import (
	"encoding/base64"
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

// createCall asks the fake to create id with rep as the request's
// replication policy, verbatim, and answers what the client sees.
func createCall(t *testing.T, f *Secrets, id, rep string) (int, string) {
	t.Helper()
	body := `{"labels":{"fugaro":"managed"},"replication":` + rep + `}`
	resp, err := http.Post(f.URL+"/v1/projects/p/secrets?secretId="+id, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(out)
}

// TestTheFakeChecksAUserManagedPolicy: a create carries the replication policy
// the API takes — automatic, or user-managed with a location for every replica
// — the fake stores it with the container and answers it back on every read of
// it, and a policy named any other way is refused, so a client sending a shape
// the real API refuses fails here instead of passing.
func TestTheFakeChecksAUserManagedPolicy(t *testing.T) {
	f := NewSecrets(t)
	regional := `{"userManaged":{"replicas":[{"location":"europe-west1"}]}}`
	if code, body := createCall(t, f, "regional", regional); code != http.StatusOK || !strings.Contains(body, `"location":"europe-west1"`) {
		t.Fatalf("a user-managed create: %d %s", code, body)
	}
	if got := f.Replication("regional"); !slices.Equal(got, []string{"europe-west1"}) {
		t.Fatalf("Replication = %v, want the location the create named", got)
	}
	if code, body := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets/regional"); code != http.StatusOK || !strings.Contains(body, `"location":"europe-west1"`) {
		t.Fatalf("the get of a user-managed container: %d %s", code, body)
	}
	// Automatic is still accepted: it is what every container made before
	// Fugaro wrote a regional policy carries, and a Terraform-managed or
	// seeded one is not this fake's create at all.
	if code, body := createCall(t, f, "global", `{"automatic":{}}`); code != http.StatusOK || !strings.Contains(body, `"automatic"`) {
		t.Fatalf("an automatic create: %d %s", code, body)
	}
	if got := f.Replication("global"); got != nil {
		t.Fatalf("an automatic container's replication = %v, want none", got)
	}

	// The policies the API refuses, each of which makes no container.
	for _, c := range []struct{ name, rep string }{
		{"neither", ``},
		{"both", `{"automatic":{},"userManaged":{"replicas":[{"location":"europe-west1"}]}}`},
		{"no replica", `{"userManaged":{"replicas":[]}}`},
		{"no location", `{"userManaged":{"replicas":[{"location":""}]}}`},
	} {
		id := "refused-" + strings.ReplaceAll(c.name, " ", "-")
		if code, body := createCall(t, f, id, c.rep); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s, want 400", c.name, code, body)
		}
		if code, _ := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets/"+id); code != http.StatusNotFound {
			t.Fatalf("%s: the refused create made a container", c.name)
		}
	}
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
	// A second version, as another writer of the same secret leaves one. It is
	// what makes "the version that was not disabled is untouched" something
	// this test checks rather than something its comment claims: the version
	// the disable below retires is not the newest, so the two are told apart by
	// the state each one reports.
	f.AddVersion("creds-bundle", []byte("second"))
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
	if code, body := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets/creds-bundle/versions/1:access"); code != http.StatusBadRequest || !strings.Contains(body, "disabled") {
		t.Fatalf("the value of the disabled version: %d %s, want 400 and \"disabled\"", code, body)
	}
	// The version that was not disabled is ENABLED and still readable, and the
	// list reports one of each: a disable takes the one version it names and
	// nothing else.
	if code, body := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets/creds-bundle/versions/latest:access"); code != http.StatusOK ||
		!strings.Contains(body, base64.StdEncoding.EncodeToString([]byte("second"))) {
		t.Fatalf("the version that was not disabled: %d %s, want 200 and its value", code, body)
	}
	if code, body := secretCall(t, f, http.MethodGet, "/v1/projects/p/secrets/creds-bundle/versions"); code != http.StatusOK ||
		strings.Count(body, `"state":"DISABLED"`) != 1 || strings.Count(body, `"state":"ENABLED"`) != 1 {
		t.Fatalf("the versions list: %d %s, want one DISABLED and one ENABLED", code, body)
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
