package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const tokenValue = "sk-ant-oat01-EXAMPLEEXAMPLEEXAMPLE"

var (
	claudeSecret    = gcp.SecretID(appSlug, "claude-oauth-token")
	bitbucketSecret = gcp.SecretID(appSlug, "bitbucket-token")
)

func secretsFixture(t *testing.T) *gcpfake.Secrets {
	t.Helper()
	sm := gcpfake.NewSecrets(t)
	newCloudFixture(t, "secret_manager: "+sm.URL+"/")
	return sm
}

func TestSecretsSetFromPipe(t *testing.T) {
	sm := secretsFixture(t)
	out, errOut, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest(claudeSecret)); got != tokenValue {
		t.Fatalf("stored %q", got)
	}
	if out != "stored "+claudeSecret+", version 1\n" || strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("stdout %q, stderr %q", out, errOut)
	}
	if !strings.Contains(errOut, "trailing newline") {
		t.Fatalf("the trim is not reported: stderr %q", errOut)
	}
	out, errOut, err = executeStdin(t, "second-value\r\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest(claudeSecret)); got != "second-value" {
		t.Fatalf("second version = %q", got)
	}
	var js map[string]string
	if err := json.Unmarshal([]byte(out), &js); err != nil || js["secret"] != claudeSecret || js["version"] != "2" || len(js) != 2 {
		t.Fatalf("json = %s, %v", out, err)
	}
	if strings.Contains(out+errOut, "second-value") {
		t.Fatalf("the value leaked: %q %q", out, errOut)
	}
	// The labels tie the secret to its repository (the bootstrap's
	// teardown checks fugaro_repo against job-spec's repo-label).
	label, err := repoLabel(appSlug)
	if err != nil {
		t.Fatal(err)
	}
	var sec struct{ Labels map[string]string }
	for _, r := range sm.Requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets") {
			_ = json.Unmarshal(r.Body, &sec)
		}
	}
	if sec.Labels["fugaro"] != "managed" || sec.Labels["fugaro_repo"] != label || sec.Labels["fugaro_secret"] != "claude-oauth-token" {
		t.Fatalf("labels = %v", sec.Labels)
	}
}

func TestSecretsSetNeverEchoes(t *testing.T) {
	sm := secretsFixture(t)
	b64 := base64.StdEncoding.EncodeToString([]byte(tokenValue))
	sm.FailAddVersion = "invalid payload " + tokenValue + " (" + b64 + ")" // a server that echoes what it got
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app", "--json")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v", err)
	}
	if s := out + errOut + err.Error(); strings.Contains(s, tokenValue) || strings.Contains(s, b64) || !strings.Contains(s, "invalid payload") {
		t.Fatalf("the value leaked: out %q, err %q, %v", out, errOut, err)
	}
	if _, _, err := executeStdin(t, "", "secrets", "set", "claude-oauth-token", tokenValue); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), tokenValue) || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("value as an argument: %v", err)
	}
	if _, _, err := executeStdin(t, "", "secrets", "set", "claude-oauth-token", "--repo", "acme/app", "--value="+tokenValue); err == nil || strings.Contains(err.Error(), tokenValue) {
		t.Fatalf("value as a flag: %v", err)
	}
	// A value typed where the NAME or a subcommand goes isn't echoed either.
	for _, args := range [][]string{{"secrets", "set", tokenValue, "--repo", "acme/app"}, {"secrets", tokenValue}} {
		out, errOut, err := executeStdin(t, "", args...)
		if strings.Contains(out+errOut, tokenValue) || err != nil && strings.Contains(err.Error(), tokenValue) {
			t.Fatalf("%v echoed the value: %q %q %v", args[:2], out, errOut, err)
		}
	}
	help, _, _ := execute(t, "secrets", "set", "--help")
	if strings.Contains(help, "--value") || !strings.Contains(help, "stdin") {
		t.Fatalf("secrets set help:\n%s", help)
	}
}

func TestSecretsSetRejectsMultiline(t *testing.T) {
	sm := secretsFixture(t)
	if _, _, err := executeStdin(t, "line1\nline2\n", "secrets", "set", "bitbucket-token", "--repo", "acme/app"); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), "line1") {
		t.Fatalf("multi-line token: %v", err)
	}
	pem := "-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n"
	if _, _, err := executeStdin(t, pem, "secrets", "set", "github-app-key", "--repo", "acme/app"); err != nil {
		t.Fatalf("PEM: %v", err)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "github-app-key"))); got != strings.TrimSuffix(pem, "\n") {
		t.Fatalf("PEM stored as %q", got)
	}
	for _, v := range []string{"", "abc", "\n", "abc\n", "\r\n", "tok\rrest"} {
		if _, _, err := executeStdin(t, v, "secrets", "set", "bitbucket-token", "--repo", "acme/app"); ExitCode(err) != ExitUserError {
			t.Errorf("value %q accepted", v)
		}
	}
	for _, name := range []string{"Bad_Name", "-lead", strings.Repeat("a", 64)} {
		if _, _, err := executeStdin(t, tokenValue, "secrets", "set", name, "--repo", "acme/app"); ExitCode(err) != ExitUserError {
			t.Errorf("bad name %q accepted", name)
		}
	}
	// A workflow secret's name is fine.
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token", "--repo", "acme/app"); err != nil {
		t.Fatalf("workflow secret: %v", err)
	}
	if sm.Latest(bitbucketSecret) != nil {
		t.Fatal("a refused value was stored")
	}
}

func TestSecretsSetSizeCap(t *testing.T) {
	sm := secretsFixture(t)
	max := strings.Repeat("x", 64*1024)
	if _, _, err := executeStdin(t, max+"\n", "secrets", "set", "bitbucket-token", "--repo", "acme/app"); err != nil {
		t.Fatalf("64 KiB value: %v", err)
	}
	if got := len(sm.Latest(bitbucketSecret)); got != 64*1024 {
		t.Fatalf("stored %d bytes", got)
	}
	if _, _, err := executeStdin(t, max+"x", "secrets", "set", "bitbucket-token", "--repo", "acme/app"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("oversized value: %v", err)
	}
}

// TestSecretsSetRefusesForeignSecret: an existing secret under the ID that
// isn't labelled as this repository's is left alone.
func TestSecretsSetRefusesForeignSecret(t *testing.T) {
	sm := secretsFixture(t)
	sm.Seed(claudeSecret, map[string]string{"fugaro": "managed", "fugaro_repo": "someone-else"}, []byte("theirs"))
	_, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro_repo") || strings.Contains(err.Error()+errOut, tokenValue) {
		t.Fatalf("err = %v", err)
	}
	if got := string(sm.Latest(claudeSecret)); got != "theirs" {
		t.Fatalf("foreign secret overwritten: %q", got)
	}
}

func TestSecretsLs(t *testing.T) {
	sm := secretsFixture(t)
	for _, name := range []string{"claude-oauth-token", "bitbucket-token", "claude-oauth-token"} {
		if _, _, err := executeStdin(t, tokenValue, "secrets", "set", name, "--repo", "acme/app"); err != nil {
			t.Fatal(err)
		}
	}
	sm.Seed("fugaro-other", map[string]string{"fugaro": "managed", "fugaro_repo": "other"}, []byte("other-value"))
	out, _, err := execute(t, "secrets", "ls", "--repo", "acme/app")
	if err != nil || !strings.Contains(out, claudeSecret) || !strings.Contains(out, bitbucketSecret) ||
		strings.Contains(out, "fugaro-other") || strings.Contains(out, tokenValue) {
		t.Fatalf("ls = %s, %v", out, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, claudeSecret) && (!strings.Contains(line, "claude-oauth-token ") || !strings.Contains(line, " 2 ")) {
			t.Fatalf("ls line %q lacks the name or version", line)
		}
	}
	out, _, err = execute(t, "secrets", "ls", "--repo", "acme/app", "--json")
	var list []struct {
		Name, ID, Latest string
		Versions         int
		Labels           map[string]string
	}
	if err != nil || json.Unmarshal([]byte(out), &list) != nil || len(list) != 2 || strings.Contains(out, tokenValue) {
		t.Fatalf("ls --json = %s, %v", out, err)
	}
	for _, s := range list {
		if s.ID == claudeSecret && (s.Name != "claude-oauth-token" || s.Latest != "2" || s.Versions != 2 || s.Labels["fugaro"] != "managed") {
			t.Fatalf("ls --json entry %+v", s)
		}
	}
	for _, r := range sm.Requests() {
		if strings.Contains(r.Path, ":access") {
			t.Fatalf("ls read a value: %s", r.Path)
		}
	}
	// An empty listing is valid JSON, not null.
	secretsFixture(t)
	if out, _, err := execute(t, "secrets", "ls", "--repo", "acme/app", "--json"); err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty ls --json = %q, %v", out, err)
	}
}

func TestReadSecret(t *testing.T) {
	var note bytes.Buffer
	got, err := readSecret(strings.NewReader("abcd\n"), &note, "x", false)
	if err != nil || string(got) != "abcd" || !strings.Contains(note.String(), "trailing newline") {
		t.Fatalf("readSecret = %q, %v, note %q", got, err, note.String())
	}
	note.Reset()
	if got, err := readSecret(strings.NewReader("abcd"), &note, "x", false); err != nil || string(got) != "abcd" || note.Len() != 0 {
		t.Fatalf("no newline: %q, %v, note %q", got, err, note.String())
	}
	// A pipe that is an *os.File (not a terminal) is read to EOF too.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("piped-value\r\n")
	w.Close()
	if got, err := readSecret(r, &note, "x", false); err != nil || string(got) != "piped-value" {
		t.Fatalf("os.Pipe = %q, %v", got, err)
	}
	r.Close()
	// Only one newline is trimmed; the rest is a multi-line value.
	if _, err := readSecret(strings.NewReader("abcd\n\n"), &note, "x", false); err == nil {
		t.Fatal("two trailing newlines accepted")
	}
	if got, err := readSecret(strings.NewReader("ab\ncd\n\n"), &note, "x", true); err != nil || string(got) != "ab\ncd\n" {
		t.Fatalf("multi-line = %q, %v", got, err)
	}
	for _, v := range []string{"secret-with\x00nul", " padded-secret"} {
		if _, err := readSecret(strings.NewReader(v), &note, "x", false); err == nil || strings.Contains(err.Error(), strings.TrimSpace(v)) {
			t.Errorf("readSecret(%q) = %v", v, err)
		}
	}
}
