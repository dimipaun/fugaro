package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const tokenValue = "fake-oauth-token-EXAMPLEEXAMPLE"

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
	if err := json.Unmarshal([]byte(out), &js); err != nil || js["secret"] != claudeSecret || js["version"] != "2" || js["project"] != "aurora" || len(js) != 3 {
		t.Fatalf("json = %s, %v", out, err)
	}
	if strings.Contains(out+errOut, "second-value") {
		t.Fatalf("the value leaked: %q %q", out, errOut)
	}
	// The labels tie the secret to its repository (discovery checks
	// fugaro_repo before adopting it).
	label, err := gcp.RepoLabel(appSlug)
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
	// Nor where a flag or --repo's value goes, or as a subcommand.
	for _, args := range [][]string{
		{"secrets", "set", "claude-oauth-token", "--repo", "acme/app", "-" + tokenValue},
		{"secrets", "set", "claude-oauth-token", "--repo", "acme/app", "--" + tokenValue},
		{"secrets", "ls", "--" + tokenValue},
		{"secrets", "set", "claude-oauth-token", "--repo", tokenValue},
		{"secrets", "set", "claude-oauth-token", "--repo", "acme/" + tokenValue + ".git/.."},
		{"secrets", tokenValue},
	} {
		out, errOut, err := executeStdin(t, "", args...)
		if ExitCode(err) != ExitUserError || strings.Contains(out+errOut+err.Error(), tokenValue) {
			t.Fatalf("%q: exit %d, echoed the value? %q %q %v", args, ExitCode(err), out, errOut, err)
		}
	}
	help, _, _ := execute(t, "secrets", "set", "--help")
	if strings.Contains(help, "--value") || !strings.Contains(help, "stdin") || !strings.Contains(help, "whitespace") {
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
	if sm.Latest(bitbucketSecret) != nil {
		t.Fatal("a refused value was stored")
	}
}

// appCheckout makes a checkout of acme/app (the fixture's repository) the
// working directory, with a fugaro.yaml whose web workflow declares
// npm-token.
func appCheckout(t *testing.T) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\nworkflows:\n  web:\n" +
		"    commands: { build: sh build.sh, test: sh test.sh }\n    secrets: [{ name: npm-token, env: NPM_TOKEN }]\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// TestSecretsSetWorkflowSecretMustBeDeclared: reserved names work from
// anywhere; a workflow secret only when the checkout's fugaro.yaml declares
// it, so a typo or a value typed as NAME is refused, unquoted.
func TestSecretsSetWorkflowSecretMustBeDeclared(t *testing.T) {
	sm := secretsFixture(t)
	t.Chdir(t.TempDir()) // no checkout
	const typed = "fake-oauth-lowercase-token"
	for _, name := range []string{"npm-token", typed} {
		if _, errOut, err := executeStdin(t, tokenValue, "secrets", "set", name, "--repo", "acme/app"); ExitCode(err) != ExitUserError ||
			!strings.Contains(err.Error(), "checkout") || strings.Contains(err.Error()+errOut, typed) {
			t.Fatalf("%s without a checkout: %v", name, err)
		}
	}
	appCheckout(t)
	for _, name := range []string{"npm-tokn", typed} {
		if _, errOut, err := executeStdin(t, tokenValue, "secrets", "set", name, "--repo", "acme/app"); ExitCode(err) != ExitUserError ||
			!strings.Contains(err.Error(), "declares") || strings.Contains(err.Error()+errOut, typed) || strings.Contains(err.Error(), "npm-tokn") {
			t.Fatalf("undeclared %s: %v", name, err)
		}
	}
	out, _, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token") // --repo from the origin
	if err != nil || !strings.Contains(out, gcp.SecretID(appSlug, "npm-token")) {
		t.Fatalf("declared workflow secret: %q, %v", out, err)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "npm-token"))); got != tokenValue {
		t.Fatalf("stored %q", got)
	}
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "bitbucket-token"); err != nil {
		t.Fatalf("reserved name in a checkout: %v", err)
	}
}

// TestSecretsAndCloudCommandsRefuseHTTP2Debug: GODEBUG=http2debug prints requests,
// the bearer token and body frames included, and can't be undone after
// start, so commands that talk to Google refuse to run.
func TestSecretsAndCloudCommandsRefuseHTTP2Debug(t *testing.T) {
	sm := secretsFixture(t)
	for _, v := range []string{"http2debug=1", "gctrace=0,http2debug=2"} {
		t.Setenv("GODEBUG", v)
		for _, args := range [][]string{
			{"secrets", "set", "claude-oauth-token", "--repo", "acme/app"},
			{"secrets", "ls", "--repo", "acme/app"},
			{"run", "--repo", "acme/app", "A task"},
			{"image", "build", "--repo", "acme/app"},
			{"runs", "ls"},
			{"logs", "some-run"},
			{"diagnose", "some-run"},
			{"cancel", "some-run"},
		} {
			_, errOut, err := executeStdin(t, tokenValue, args...)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") || strings.Contains(err.Error()+errOut, tokenValue) {
				t.Fatalf("%s %q: %v", v, args, err)
			}
		}
	}
	if len(sm.Requests()) != 0 {
		t.Fatalf("requests were sent: %v", sm.Requests())
	}
	t.Setenv("GODEBUG", "gctrace=0")
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app"); err != nil {
		t.Fatalf("an unrelated GODEBUG: %v", err)
	}
}

func TestSecretsSetBrokenFugaroYAML(t *testing.T) {
	secretsFixture(t)
	appCheckout(t)
	// It names its project, which still selects the project config, but
	// has a field fugaro.yaml doesn't know.
	if err := os.WriteFile("fugaro.yaml", []byte("version: 1\nproject: aurora\nnot_a_key: [\n  x\n]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token", "--repo", "acme/app")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "problem(s)") || strings.Contains(err.Error(), "run this from a checkout") {
		t.Fatalf("broken fugaro.yaml: %v", err)
	}
}

func TestSecretsUnknownSubcommand(t *testing.T) {
	if _, _, err := execute(t, "secrets", "lss"); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), "lss") {
		t.Fatalf("secrets lss: %v", err)
	}
	if out, _, err := execute(t, "secrets"); err != nil || !strings.Contains(out, "set") {
		t.Fatalf("secrets: %q, %v", out, err)
	}
}

func TestSecretsSetSizeCap(t *testing.T) {
	sm := secretsFixture(t)
	limit := strings.Repeat("x", 64*1024)
	if _, _, err := executeStdin(t, limit+"\n", "secrets", "set", "bitbucket-token", "--repo", "acme/app"); err != nil {
		t.Fatalf("64 KiB value: %v", err)
	}
	if got := len(sm.Latest(bitbucketSecret)); got != 64*1024 {
		t.Fatalf("stored %d bytes", got)
	}
	if _, _, err := executeStdin(t, limit+"x", "secrets", "set", "bitbucket-token", "--repo", "acme/app"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "64 KiB") {
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
	got, err := readSecret(context.Background(), strings.NewReader("abcd\n"), &note, "x", false)
	if err != nil || string(got) != "abcd" || !strings.Contains(note.String(), "trailing newline") {
		t.Fatalf("readSecret = %q, %v, note %q", got, err, note.String())
	}
	note.Reset()
	if got, err := readSecret(context.Background(), strings.NewReader("abcd"), &note, "x", false); err != nil || string(got) != "abcd" || note.Len() != 0 {
		t.Fatalf("no newline: %q, %v, note %q", got, err, note.String())
	}
	// A pipe that is an *os.File (not a terminal) is read to EOF too.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("piped-value\r\n")
	w.Close()
	if got, err := readSecret(context.Background(), r, &note, "x", false); err != nil || string(got) != "piped-value" {
		t.Fatalf("os.Pipe = %q, %v", got, err)
	}
	r.Close()
	// Only one newline is trimmed; the rest is a multi-line value.
	if _, err := readSecret(context.Background(), strings.NewReader("abcd\n\n"), &note, "x", false); err == nil {
		t.Fatal("two trailing newlines accepted")
	}
	if got, err := readSecret(context.Background(), strings.NewReader("ab\ncd\n\n"), &note, "x", true); err != nil || string(got) != "ab\ncd\n" {
		t.Fatalf("multi-line = %q, %v", got, err)
	}
	for _, v := range []string{"secret-with\x00nul", " padded-secret"} {
		if _, err := readSecret(context.Background(), strings.NewReader(v), &note, "x", false); err == nil || strings.Contains(err.Error(), strings.TrimSpace(v)) {
			t.Errorf("readSecret(%q) = %v", v, err)
		}
	}
}

// claudeAccountFixture appends a claude_accounts block to the fixture's
// local config file (the same append-to-FUGARO_CONFIG pattern
// TestSecretsSetProviderKey uses for providers:).
func claudeAccountFixture(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(yaml); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// TestSecretsSetAcceptsAllowedNamedAccount: a named Claude account's secret
// stores like a provider's key, for a repository its allow_data_to lists;
// the reserved default secret is unaffected by claude_accounts existing.
func TestSecretsSetAcceptsAllowedNamedAccount(t *testing.T) {
	sm := secretsFixture(t)
	claudeAccountFixture(t, "claude_accounts:\n  acme-personal: { allow_data_to: [acme/app] }\n")
	id := gcp.SecretID(appSlug, "claude-oauth-token-acme-personal")
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token-acme-personal", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest(id)); got != tokenValue {
		t.Fatalf("stored %q", got)
	}
	if !strings.Contains(out, id) || strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("out %q errOut %q", out, errOut)
	}
	// The reserved default secret still stores unmodified.
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app"); err != nil {
		t.Fatalf("the default secret: %v", err)
	}
}

// TestSecretsSetRefusesDisallowedNamedAccount: a configured named account
// whose allow_data_to does not list this repository is refused before
// stdin is ever read, and nothing is stored.
func TestSecretsSetRefusesDisallowedNamedAccount(t *testing.T) {
	sm := secretsFixture(t)
	claudeAccountFixture(t, "claude_accounts:\n  acme-personal: { allow_data_to: [acme/other] }\n")
	spy := &readerSpy{Reader: strings.NewReader(tokenValue)}
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(spy)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"secrets", "set", "claude-oauth-token-acme-personal", "--repo", "acme/app"})
	err := cmd.Execute()
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not a configured claude_accounts name") ||
		strings.Contains(out.String()+errOut.String()+err.Error(), tokenValue) {
		t.Fatalf("err = %v", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
	if sm.Latest(gcp.SecretID(appSlug, "claude-oauth-token-acme-personal")) != nil {
		t.Fatal("a refused value was stored")
	}
}

// TestSecretsSetRefusesUnconfiguredClaudeOAuthName: a name shaped
// claude-oauth-token-<x> whose <x> is not a claude_accounts key at all
// (never configured, not merely disallowed) is refused with the same
// "not a configured claude_accounts name" message, before stdin is read —
// not the generic "no workflow declares" message a workflow-secret typo
// gets. Review finding: the whole claude-oauth-token- namespace is
// reserved for named Claude accounts, whether or not a workflow happens to
// declare a secret of that exact name (config.Validate separately refuses
// that declaration; this is this command's own, independent check).
func TestSecretsSetRefusesUnconfiguredClaudeOAuthName(t *testing.T) {
	sm := secretsFixture(t)
	spy := &readerSpy{Reader: strings.NewReader(tokenValue)}
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(spy)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"secrets", "set", "claude-oauth-token-nobody", "--repo", "acme/app"})
	err := cmd.Execute()
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not a configured claude_accounts name") ||
		strings.Contains(err.Error(), "declares") || strings.Contains(err.Error(), "checkout") ||
		strings.Contains(out.String()+errOut.String()+err.Error(), tokenValue) {
		t.Fatalf("err = %v", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
	if sm.Latest(gcp.SecretID(appSlug, "claude-oauth-token-nobody")) != nil {
		t.Fatal("a refused value was stored")
	}
}

// TestClaudeOAuthAccountAllowed is a surgical, bypass-the-YAML-pipeline
// unit test of the defense-in-depth guard itself: whatever route a
// claude-oauth-token-<name> secret name reaches secretsSet or init's
// take() by (config.Validate is supposed to stop a workflow from ever
// declaring one, but this helper never assumes that upstream refusal ran),
// only a configured, allowed account passes.
func TestClaudeOAuthAccountAllowed(t *testing.T) {
	accounts := map[string]config.ClaudeAccount{
		"acme-personal": {AllowDataTo: []string{"acme/app"}},
		"other-team":    {AllowDataTo: []string{"acme/other"}},
	}
	for _, tc := range []struct {
		name, repo string
		want       bool
	}{
		{"claude-oauth-token-acme-personal", "acme/app", true},
		{"claude-oauth-token-acme-personal", "acme/unrelated", false},
		{"claude-oauth-token-other-team", "acme/app", false}, // configured, not allowed here
		{"claude-oauth-token-nobody", "acme/app", false},     // not configured at all
		{"claude-oauth-token-", "acme/app", false},           // the empty suffix names nothing
	} {
		if got := claudeOAuthAccountAllowed(accounts, tc.name, tc.repo); got != tc.want {
			t.Errorf("claudeOAuthAccountAllowed(%q, %q) = %v, want %v", tc.name, tc.repo, got, tc.want)
		}
	}
}

// TestClaudeOAuthAccountRefusalIsPrintable: the refusal message never
// echoes a control or bidi-override character raw, whatever reached it (a
// cached or hand-edited local config's account name, say) — the same
// bounding every other config-sourced name gets (pluginwire.Printable).
func TestClaudeOAuthAccountRefusalIsPrintable(t *testing.T) {
	bidi := string(rune(0x202e)) // a right-to-left override character
	hostile := "claude-oauth-token-\x1b[2Jpwned"
	err := claudeOAuthAccountRefusal(hostile, "acme/"+bidi+"app")
	if err == nil {
		t.Fatal("nil error")
	}
	if strings.ContainsAny(err.Error(), "\x1b") || strings.Contains(err.Error(), bidi) {
		t.Fatalf("a control or bidi character leaked raw: %q", err.Error())
	}
	escEsc, escBidi := fmt.Sprintf("\\u%04x", 0x1b), fmt.Sprintf("\\u%04x", 0x202e)
	if !strings.Contains(err.Error(), escEsc) || !strings.Contains(err.Error(), escBidi) {
		t.Fatalf("the escaped form is missing: %q", err.Error())
	}
}

// TestSecretsLsNamedAccountNeverPrintsValue: a stored named-account secret
// appears in secrets ls by its fugaro_secret label and version count only;
// no value-reading (:access) request is ever made.
func TestSecretsLsNamedAccountNeverPrintsValue(t *testing.T) {
	sm := secretsFixture(t)
	claudeAccountFixture(t, "claude_accounts:\n  acme-personal: { allow_data_to: [acme/app] }\n")
	id := gcp.SecretID(appSlug, "claude-oauth-token-acme-personal")
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token-acme-personal", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "secrets", "ls", "--repo", "acme/app")
	if err != nil || !strings.Contains(out, id) || !strings.Contains(out, "claude-oauth-token-acme-personal") || strings.Contains(out, tokenValue) {
		t.Fatalf("ls = %s, %v", out, err)
	}
	for _, r := range sm.Requests() {
		if strings.Contains(r.Path, ":access") {
			t.Fatalf("ls read a value: %s", r.Path)
		}
	}
}

// TestSecretsSetProviderKey: a model provider's key is stored from anywhere,
// like a reserved secret, but only for a repository the owner allowed to
// send code to that provider.
func TestSecretsSetProviderKey(t *testing.T) {
	sm := secretsFixture(t)
	t.Chdir(t.TempDir())
	path := os.Getenv("FUGARO_CONFIG")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	prov := `providers:
  openrouter: { kind: anthropic-compat, base_url: "https://openrouter.ai/api", auth: bearer, secret: openrouter-api-key, models: ["deepseek/*"], allow_data_to: ["acme/app"] }
  elsewhere: { kind: anthropic-compat, base_url: "https://other.example", auth: bearer, secret: other-key, models: ["qwen/*"], allow_data_to: ["acme/other"] }
`
	if _, err := f.WriteString(prov); err != nil {
		t.Fatal(err)
	}
	f.Close()
	const key = "or-not-a-real-key-1234"
	out, errOut, err := executeStdin(t, key, "secrets", "set", "openrouter-api-key", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.SecretID(appSlug, "openrouter-api-key")
	if string(sm.Latest(id)) != key || strings.Contains(out+errOut, key) {
		t.Fatalf("stored %q, out %q %q", sm.Latest(id), out, errOut)
	}
	if _, _, err := executeStdin(t, key, "secrets", "set", "other-key", "--repo", "acme/app"); ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "allow_data_to") {
		t.Fatalf("a provider the repository may not use: %v", err)
	}
}

// appendConfig appends a block to the fixture's local config file (the
// append-to-FUGARO_CONFIG pattern the provider and claude_accounts fixtures
// above use).
func appendConfig(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(yaml); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// projectSecretFixture makes NAME a project-wide secret the local config
// allow-lists for acme/app.
func projectSecretFixture(t *testing.T, yaml string) {
	t.Helper()
	appendConfig(t, yaml)
}

// noListProvider is a model provider with no allow_data_to at all: under
// §3a's no-list rule its key is available to every repository, so storing it
// is never refused, at project scope or repo scope.
const noListProvider = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
`

// listedProvider is the same provider, but allow-listed for acme/app only.
const listedProvider = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/app"]
`

// TestSecretsSetProjectWideRefusesEmptyAllowList: a reserved name with no
// project_secrets entry that lists a repository is refused before the value
// is ever read, and nothing is stored — unlike a provider's key, which the
// next test pins as accepted in the identical state (§3a).
func TestSecretsSetProjectWideRefusesEmptyAllowList(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [] }\n")
	spy := &readerSpy{Reader: strings.NewReader(tokenValue)}
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(spy)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"secrets", "set", "bitbucket-token", "--project-wide"})
	err := cmd.Execute()
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "project_secrets") || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("err = %v, want the refusal to name the field and the --repo way out", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
	if strings.Contains(out.String()+errOut.String()+err.Error(), tokenValue) {
		t.Fatal("the value leaked")
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", "bitbucket-token")) != nil {
		t.Fatal("a refused value was stored")
	}

	// A named Claude account whose own allow_data_to is empty is refused the
	// same way.
	secretsFixture(t)
	claudeAccountFixture(t, "claude_accounts:\n  acme-personal: { allow_data_to: [] }\n")
	spy = &readerSpy{Reader: strings.NewReader(tokenValue)}
	cmd = NewRootCmd()
	out.Reset()
	errOut.Reset()
	cmd.SetIn(spy)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"secrets", "set", "claude-oauth-token-acme-personal", "--project-wide"})
	if err := cmd.Execute(); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
}

// TestSecretsSetProjectWideAcceptsProviderSecret: a provider's key
// allow-listed for a repository stores under gcp.ProjectSecretID, with the
// installation's own labels, never the repository's.
func TestSecretsSetProjectWideAcceptsProviderSecret(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, listedProvider)
	const key = "or-not-a-real-key-1234"
	out, errOut, err := executeStdin(t, key, "secrets", "set", "openrouter-api-key", "--project-wide")
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID("aurora", "openrouter-api-key")
	if got := string(sm.Latest(id)); got != key {
		t.Fatalf("stored %q, want the project secret %s", got, id)
	}
	if !strings.Contains(out, id) || strings.Contains(out+errOut, key) {
		t.Fatalf("out %q errOut %q", out, errOut)
	}
	var sec struct{ Labels map[string]string }
	for _, r := range sm.Requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets") {
			_ = json.Unmarshal(r.Body, &sec)
		}
	}
	if sec.Labels["fugaro"] != "managed" || sec.Labels["fugaro_project"] != "aurora" || sec.Labels["fugaro_secret"] != "openrouter-api-key" {
		t.Fatalf("labels = %v", sec.Labels)
	}
}

// TestSecretsSetProjectWideAcceptsProviderSecretWithNoList: §3a, the owner's
// final ruling — a provider with NO allow_data_to at all is available to
// every repository, so its key stores project-wide with no refusal. This is
// the exact state TestSecretsSetProjectWideRefusesEmptyAllowList refuses for
// a reserved name, and the two tests exist to pin that divergence.
func TestSecretsSetProjectWideAcceptsProviderSecretWithNoList(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, noListProvider)
	const key = "or-not-a-real-key-1234"
	if _, _, err := executeStdin(t, key, "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
		t.Fatalf("a provider with no allow_data_to: %v", err)
	}
	if got := string(sm.Latest(gcp.ProjectSecretID("aurora", "openrouter-api-key"))); got != key {
		t.Fatalf("stored %q", got)
	}
}

// TestSecretsSetRepoScopeAcceptsProviderSecretWithNoList: B5(a), the third
// call site — the repo-scope branch, unchanged since Task 1, now accepts a
// no-list provider for every repository (AllowsData reads an absent list as
// "everyone"), where before it refused each one.
func TestSecretsSetRepoScopeAcceptsProviderSecretWithNoList(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, noListProvider)
	const key = "or-not-a-real-key-1234"
	if _, _, err := executeStdin(t, key, "secrets", "set", "openrouter-api-key", "--repo", "acme/app"); err != nil {
		t.Fatalf("a no-list provider at repo scope: %v", err)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "openrouter-api-key"))); got != key {
		t.Fatalf("stored %q", got)
	}
}

// TestSecretsRepoAndProjectWideAreExclusive: --repo names one repository's
// container and --project-wide works on the project's own, which no
// repository owns. The project-wide path ignores --repo, so the pair used to
// store and list a container the operator had not named; it is refused now,
// before the value is read, and neither flag alone is affected.
func TestSecretsRepoAndProjectWideAreExclusive(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/app] }\n")
	spy := &readerSpy{Reader: strings.NewReader(tokenValue)}
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(spy)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"secrets", "set", "bitbucket-token", "--project-wide", "--repo", "acme/app"})
	err := cmd.Execute()
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--repo and --project-wide") {
		t.Fatalf("set: err = %v", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
	if strings.Contains(out.String()+errOut.String()+err.Error(), tokenValue) {
		t.Fatal("the value leaked")
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", "bitbucket-token")) != nil || sm.Latest(gcp.SecretID(appSlug, "bitbucket-token")) != nil {
		t.Fatal("a refused value was stored")
	}
	if _, _, err := execute(t, "secrets", "ls", "--project-wide", "--repo", "acme/app"); ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "--repo and --project-wide") {
		t.Fatalf("ls: err = %v", err)
	}
	// The flags on their own still work, project-wide first.
	if _, errOut, err := execute(t, "secrets", "ls", "--project-wide"); err != nil {
		t.Fatalf("--project-wide alone: %v (%s)", err, errOut)
	}
	if _, _, err := execute(t, "secrets", "ls", "--repo", "acme/app"); err != nil {
		t.Fatalf("--repo alone: %v", err)
	}
}

// TestSecretsSetRepoCopyNotesTheProjectWideOverride: a --repo copy of a name
// the repository may already read project-wide wins from then on, and only
// disabling the copy's last enabled version goes back (design §5), so the
// hand-typed command says so once, on stderr. A repository's own secret,
// which has no project-wide meaning, gets no note: the warning is about the
// precedence, not about storing a value.
func TestSecretsSetRepoCopyNotesTheProjectWideOverride(t *testing.T) {
	secretsFixture(t)
	projectSecretFixture(t, listedProvider)
	if _, _, err := executeStdin(t, "or-not-a-real-key-1234", "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := executeStdin(t, "or-not-a-real-key-5678", "secrets", "set", "openrouter-api-key", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "note:") || !strings.Contains(errOut, "overrides the project copy") {
		t.Errorf("stderr = %q, want the override note", errOut)
	}
	if strings.Contains(errOut, "or-not-a-real-key-5678") {
		t.Error("the note quoted the value")
	}
	appCheckout(t)
	if _, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(errOut, "note:") {
		t.Errorf("stderr = %q, want no note for a repository's own secret", errOut)
	}
}

// TestSecretsLsAddsScopeColumn: a name with both a repo-owned version and a
// project copy the repository may use shows "project, repo override stored";
// an allow-listed project secret with no repo-owned entry at all shows
// "project". Both scopes appear, and neither reads a value.
func TestSecretsLsAddsScopeColumn(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/app] }\n"+listedProvider)
	// A project copy for the allow-listed reserved name (no repo entry), and
	// both copies of the provider's key.
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "bitbucket-token", "--project-wide"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeStdin(t, "or-not-a-real-key-1234", "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeStdin(t, "or-not-a-real-key-5678", "secrets", "set", "openrouter-api-key", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "secrets", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Name, ID, Scope string
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("ls --json = %s, %v", out, err)
	}
	scopes := map[string]string{}
	for _, s := range list {
		scopes[s.Name] = s.Scope
	}
	if scopes["openrouter-api-key"] != "project, repo override stored" {
		t.Errorf("openrouter-api-key scope = %q, want the override note (entries %+v)", scopes["openrouter-api-key"], list)
	}
	if scopes["bitbucket-token"] != "project" {
		t.Errorf("bitbucket-token scope = %q, want project (entries %+v)", scopes["bitbucket-token"], list)
	}
	for _, r := range sm.Requests() {
		if strings.Contains(r.Path, ":access") {
			t.Fatalf("ls read a value: %s", r.Path)
		}
	}
}

// TestSecretsLsProjectWideShowsAnyRepository: a provider stored
// project-wide with no allow_data_to is available to every repository
// (design §3a), and the ALLOWED column is the operator's only view of who
// may mount that key, so it says so in words instead of the "-" that reads
// as nobody. The explicit ["*"] marker and a real list are left as written.
func TestSecretsLsProjectWideShowsAnyRepository(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		want  string
	}{
		{"no list", noListProvider, "any repository"},
		{"wildcard", strings.Replace(listedProvider, `["acme/app"]`, `["*"]`, 1), "*"},
		{"listed", listedProvider, "acme/app"},
	} {
		secretsFixture(t)
		projectSecretFixture(t, tc.block)
		if _, _, err := executeStdin(t, "or-not-a-real-key-1234", "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		out, _, err := execute(t, "secrets", "ls", "--project-wide")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: ALLOWED does not say %q:\n%s", tc.name, tc.want, out)
		}
		out, _, err = execute(t, "secrets", "ls", "--project-wide", "--json")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var list []struct {
			Name    string
			Allowed []string
		}
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			t.Fatalf("%s: ls --json = %s, %v", tc.name, out, err)
		}
		if len(list) != 1 || list[0].Allowed == nil || strings.Join(list[0].Allowed, ",") != tc.want {
			t.Errorf("%s: allowed = %+v, want [%s]", tc.name, list, tc.want)
		}
	}
}

// TestSecretsLsRepoScopeIgnoresProjectCopyItMayNotUse: a repository's
// listing answers with the plan's own rule (infra.ProjectSecretCandidate),
// not with "a project-wide copy exists". This repository is on no
// allow_data_to, so its own container is all it has: the project-wide copy
// of the same name must not appear, and must not turn the row into
// "project" or the override note.
func TestSecretsLsRepoScopeIgnoresProjectCopyItMayNotUse(t *testing.T) {
	sm := secretsFixture(t)
	// The other repository is a repo entry of the fixture's own repos block
	// (--repo needs to know its git provider); the provider's allow_data_to
	// is the fixture's ["acme/app"].
	appendConfig(t, "  acme/other: { provider: github }\n")
	projectSecretFixture(t, listedProvider)
	if _, _, err := executeStdin(t, "or-not-a-real-key-1234", "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
		t.Fatal(err)
	}
	// No container of its own: nothing is listed at all.
	out, _, err := execute(t, "secrets", "ls", "--repo", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no secrets stored for acme/other") {
		t.Fatalf("a project copy it may not use is listed:\n%s", out)
	}
	// With a container of its own (seeded: `secrets set --repo` refuses this
	// repository, which is the rule being listed), the row is the
	// repository's own.
	other := mustSlug("github", "acme/other")
	sm.Seed(gcp.SecretID(other, "openrouter-api-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, other), gcp.LabelSecret: "openrouter-api-key"},
		[]byte("or-not-a-real-key-5678"))
	out, _, err = execute(t, "secrets", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Name, ID, Scope string
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("ls --json = %s, %v", out, err)
	}
	if len(list) != 1 || list[0].Scope != "repo" || list[0].ID != gcp.SecretID(other, "openrouter-api-key") {
		t.Fatalf("entries = %+v, want the repository's own container alone", list)
	}
}

// TestSecretsLsScopeIsProjectWhenTheRepoCopyHasNoVersion: §5's precedence is
// a repository copy with an enabled version, not a container. A repository
// container whose versions are all disabled resolves to the project-wide
// copy -- the state §6 step 4a deliberately leaves behind while a name moves
// project-wide -- so ls must say "project", not "project, repo override
// stored" beside the "-" LATEST and 0 VERSIONS of the same row, which would
// tell the operator the migration did not take.
func TestSecretsLsScopeIsProjectWhenTheRepoCopyHasNoVersion(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, listedProvider)
	if _, _, err := executeStdin(t, "or-not-a-real-key-1234", "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
		t.Fatal(err)
	}
	// The repository's own container with every version disabled: what
	// `fugaro secrets set ... --project-wide` leaves behind when a name
	// moves, and what init --repo's own container looks like before a value
	// is ever stored in it.
	sm.Seed(gcp.SecretID(appSlug, "openrouter-api-key"), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, appSlug), gcp.LabelSecret: "openrouter-api-key"}, nil)
	out, _, err := execute(t, "secrets", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Name, ID, Scope string
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("ls --json = %s, %v", out, err)
	}
	scopes := map[string]string{}
	for _, s := range list {
		scopes[s.Name] = s.Scope
	}
	if scopes["openrouter-api-key"] != "project" {
		t.Errorf("openrouter-api-key scope = %q, want project: the repository copy holds no version to override with (entries %+v)",
			scopes["openrouter-api-key"], list)
	}
	// The human table agrees, and the row is the project container's own.
	text, _, err := execute(t, "secrets", "ls", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "repo override stored") || !strings.Contains(text, gcp.ProjectSecretID("aurora", "openrouter-api-key")) {
		t.Fatalf("ls = %s", text)
	}
	for _, r := range sm.Requests() {
		if strings.Contains(r.Path, ":access") {
			t.Fatalf("ls read a value: %s", r.Path)
		}
	}
}

// TestSecretsLsProjectWideNeverPrintsValue: --project-wide lists by the
// installation's own label, prints the allowed repositories from local
// config, and never reads a value.
func TestSecretsLsProjectWideNeverPrintsValue(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, listedProvider)
	if _, _, err := executeStdin(t, "or-not-a-real-key-1234", "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "secrets", "ls", "--project-wide")
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID("aurora", "openrouter-api-key")
	if !strings.Contains(out, id) || !strings.Contains(out, "openrouter-api-key") || !strings.Contains(out, "acme/app") {
		t.Fatalf("ls --project-wide = %s", out)
	}
	if strings.Contains(out, "or-not-a-real-key-1234") {
		t.Fatal("the value leaked")
	}
	for _, r := range sm.Requests() {
		if strings.Contains(r.Path, ":access") {
			t.Fatalf("ls --project-wide read a value: %s", r.Path)
		}
	}
}

// --- Task 2.1: project scope is the default (D2) -------------------------
//
// D2 (design docs/design/project-centric.md §6.3) reverses #343's
// default-safe rule for a name that has a project-wide meaning:
// `fugaro secrets set NAME` with neither flag stores the PROJECT copy, and
// `--repo` -- bare, or with a slug -- is what asks for one repository's own
// copy. `--project-wide` stays an accepted synonym of the default, so every
// existing script and skill keeps working. A repository's own application
// secret has no project-wide meaning and keeps today's repository scope:
// the default is "project where project exists", never "project always"
// (plan Task 2.1 step 3b).

// TestSecretsSetDefaultsToProjectScope: the value lands under the
// installation's own secret ID with the project's labels, and no repository
// container is created for it.
func TestSecretsSetDefaultsToProjectScope(t *testing.T) {
	sm := secretsFixture(t)
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "anthropic-api-key")
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID("aurora", "anthropic-api-key")
	if got := string(sm.Latest(id)); got != tokenValue {
		t.Fatalf("stored %q, want the project copy %s", got, id)
	}
	if sm.Latest(gcp.SecretID(appSlug, "anthropic-api-key")) != nil {
		t.Error("a repository copy was stored as well")
	}
	if !strings.Contains(out, id) || strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("out %q errOut %q", out, errOut)
	}
	// The reach is stated where the person storing it reads it (plan Task
	// 2.1 step 3): the project copy, and the profile as the boundary.
	for _, want := range []string{"project", "profile"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q does not say %q", errOut, want)
		}
	}
	var sec struct{ Labels map[string]string }
	for _, r := range sm.Requests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets") {
			_ = json.Unmarshal(r.Body, &sec)
		}
	}
	if sec.Labels[gcp.LabelProject] != "aurora" || sec.Labels[gcp.LabelRepo] != "" {
		t.Fatalf("labels = %v, want the installation's, never a repository's", sec.Labels)
	}
}

// A project-wide store names the repositories whose own copy wins over it
// (code review, medium): §5's precedence has no override-the-override, and
// §6 step 4a is the state both existing installations are in, so a rotation
// of the project copy reaches no repository that already has its own copy
// with an enabled version -- and saying so is the difference between a
// rotation that worked and one that only looked like it did. Names only,
// never a value, and before the value is read.
func TestSecretsSetProjectWideNamesTheRepoCopiesThatWin(t *testing.T) {
	sm := secretsFixture(t)
	label, err := gcp.RepoLabel(appSlug)
	if err != nil {
		t.Fatal(err)
	}
	// A name D2's sweep stores project-wide is the one this note is about
	// (anthropic-api-key; bitbucket-token has no project-wide default any
	// more, TestSecretsSetBitbucketTokenStaysRepoScope).
	const name = config.AnthropicSecret
	repoCopy := gcp.SecretID(appSlug, name)
	// acme/app's own copy, with an enabled version: it wins over the project
	// copy from now on.
	sm.Seed(repoCopy, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}, []byte("old-repo-key-EXAMPLE"))
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", name)
	if err != nil {
		t.Fatalf("%v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "acme/app") || !strings.Contains(errOut, "wins over the project copy") {
		t.Errorf("stderr does not name the repository whose copy wins: %q", errOut)
	}
	// What the note says is what the store does: the project copy takes the
	// new version, the repository copy keeps the old one.
	if got := string(sm.Latest(gcp.ProjectSecretID("aurora", name))); got != tokenValue {
		t.Errorf("the project copy = %q", got)
	}
	if got := string(sm.Latest(repoCopy)); got != "old-repo-key-EXAMPLE" {
		t.Errorf("the repository copy was overwritten: %q", got)
	}
	if strings.Contains(out+errOut, tokenValue) || strings.Contains(errOut, "old-repo-key-EXAMPLE") {
		t.Fatalf("a value leaked: %q %q", out, errOut)
	}
}

// The same store says nothing when no repository's copy wins: the note is
// about a state, not about every project-wide store. A container with no
// version is not that state -- §5 resolves to a repository copy only when it
// has an enabled version.
func TestSecretsSetProjectWideIsSilentWithoutARepoCopy(t *testing.T) {
	sm := secretsFixture(t)
	label, err := gcp.RepoLabel(appSlug)
	if err != nil {
		t.Fatal(err)
	}
	const name = config.AnthropicSecret
	sm.Seed(gcp.SecretID(appSlug, name), map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}, nil)
	_, errOut, err := executeStdin(t, tokenValue, "secrets", "set", name)
	if err != nil {
		t.Fatalf("%v (%s)", err, errOut)
	}
	if strings.Contains(errOut, "wins over the project copy") {
		t.Errorf("a versionless repository copy was called a winner: %q", errOut)
	}
	if got := string(sm.Latest(gcp.ProjectSecretID("aurora", name))); got != tokenValue {
		t.Errorf("stored %q", got)
	}
}

// TestSecretsSetRepoFlagStoresTheRepoCopy: --repo, with a slug or bare, is
// the opt-out of the project default: one repository's own container, whose
// version wins over the project copy from then on (design §5).
func TestSecretsSetRepoFlagStoresTheRepoCopy(t *testing.T) {
	sm := secretsFixture(t)
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "anthropic-api-key", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "anthropic-api-key"))); got != tokenValue {
		t.Fatalf("stored %q, want the repository copy", got)
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", "anthropic-api-key")) != nil {
		t.Error("--repo stored the project copy too")
	}
}

// The bare command decides its scope with the repository in hand (code
// review, low): an entry of project_secrets that lists another repository
// does not make the name project-scope for this checkout. Storing the shared
// copy from here would overwrite the value the repositories the entry does
// list mount, from a repository that may not mount it -- and it is not what
// the person meant: with no flag, the copy being stored is this repository's
// own, the same place init's secrets stage stores it (initsecrets.go
// projectScope). The note names the other intent.
func TestSecretsSetFromACheckoutTheEntryExcludesStoresTheRepoCopy(t *testing.T) {
	sm := secretsFixture(t)
	// The shared copy the repositories the entry lists mount.
	shared := gcp.ProjectSecretID("aurora", "bitbucket-token")
	sm.Seed(shared, map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: "aurora", gcp.LabelSecret: "bitbucket-token"}, []byte("shared-token-EXAMPLE"))
	projectSecretFixture(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/other] }\n")
	appCheckout(t)
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "bitbucket-token")
	if err != nil {
		t.Fatalf("%v (%s)", err, errOut)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "bitbucket-token"))); got != tokenValue {
		t.Errorf("acme/app's own copy = %q, want the value just stored", got)
	}
	if got := string(sm.Latest(shared)); got != "shared-token-EXAMPLE" {
		t.Errorf("the shared copy = %q: a checkout the entry excludes overwrote it", got)
	}
	if !strings.Contains(errOut, "acme/app") || !strings.Contains(errOut, "--project-wide") {
		t.Errorf("stderr does not say where the value went, or what the other intent is: %q", errOut)
	}
	if strings.Contains(out+errOut, tokenValue) || strings.Contains(errOut, "shared-token-EXAMPLE") {
		t.Fatalf("a value leaked: %q %q", out, errOut)
	}
}

// A D2-swept name whose project_secrets entry allows nobody is the case the
// bare command has an answer for (code review, low): from a checkout it
// stores this repository's own copy -- an entry that lists no repository
// leaves no shared copy it could mean -- and --project-wide, the explicit ask,
// refuses in a message that names that way out instead of leaving the person
// with a config list to edit and no --repo.
//
// This is also the one state where the note cannot offer the flag as typed
// (code review, round 2, low): the name has a project-wide shape, so the note
// fires, and projectWideAccepted is false for it all the same -- a present and
// empty allow_data_to means nobody may mount the project copy, so
// `--project-wide` refuses. The note names the field to fill in instead; a
// note pointing at a command that fails is a dead end.
func TestSecretsSetEmptyAllowListNamesTheRepoWayOut(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, "project_secrets:\n  "+config.AnthropicSecret+": { allow_data_to: [] }\n")
	appCheckout(t)
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", config.AnthropicSecret)
	if err != nil {
		t.Fatalf("the no-flag form from a checkout: %v (%s)", err, errOut)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, config.AnthropicSecret))); got != tokenValue {
		t.Errorf("acme/app's own copy = %q", got)
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", config.AnthropicSecret)) != nil {
		t.Error("the no-flag form stored a project copy the entry allows nobody to mount")
	}
	// The field to edit, and the fact that the flag is refused until it names
	// at least one repository -- not the flag alone, which would fail.
	if !strings.Contains(errOut, "project_secrets."+config.AnthropicSecret+".allow_data_to") ||
		!strings.Contains(errOut, "is refused until it names at least one repository") {
		t.Errorf("stderr does not say what makes --project-wide work: %q", errOut)
	}
	if strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("a value leaked: %q %q", out, errOut)
	}
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", config.AnthropicSecret, "--project-wide"); ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "--repo") {
		t.Fatalf("--project-wide err = %v, want the refusal to name --repo", err)
	}
}

// The other eligibility refusal the note used to be printed over (code
// review, low): a provider's key from a checkout the provider's allow_data_to
// excludes is refused just below, and the note saying "this value goes to
// <repo>'s own copy, which overrides the project copy for it" promises a store
// that does not happen. The note is printed after the checks, so a refused
// command carries none.
//
// The refusal names the way forward (code review, medium): SECURITY.md calls
// --project-wide a synonym of the default, and here the bare command is
// refused while that flag stores the project's copy -- the copy the
// repositories the provider *does* list mount -- so a person left with the
// bare command's refusal alone has no way to store it. The command the
// refusal names is run below and must work.
func TestSecretsSetRefusedProviderKeyPrintsNoRepoCopyNote(t *testing.T) {
	sm := secretsFixture(t)
	projectSecretFixture(t, `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/other"]
`)
	appCheckout(t)
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "openrouter-api-key")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "allow_data_to") {
		t.Fatalf("err = %v, want the allow_data_to refusal", err)
	}
	if !strings.Contains(err.Error(), "secrets set openrouter-api-key --project-wide") {
		t.Errorf("the refusal does not name the store that does work: %v", err)
	}
	if strings.Contains(out+errOut, "own copy") {
		t.Errorf("the refusal carries the note that promises a store: out %q errOut %q", out, errOut)
	}
	if sm.Latest(gcp.SecretID(appSlug, "openrouter-api-key")) != nil {
		t.Error("a refused value was stored")
	}
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "openrouter-api-key", "--project-wide"); err != nil {
		t.Fatalf("the command the refusal names: %v", err)
	}
	if got := string(sm.Latest(gcp.ProjectSecretID("aurora", "openrouter-api-key"))); got != tokenValue {
		t.Errorf("the project copy = %q, want the value the refusal pointed at", got)
	}
}

// TestSecretsSetBitbucketTokenStaysRepoScope: bitbucket-token is the one
// reserved name with no project-wide default (config.PerRepositorySecret, the
// owner's ruling of 2026-10-15): a repository access token is issued for one
// repository and authenticates no other, so a project-wide copy would hand
// every repository the first one's token. The bare command -- which for the
// other reserved names stores the project copy -- therefore stores this
// repository's own, from a checkout and with no flag, exactly as `--repo`
// would; and --project-wide, with no project_secrets entry, is refused before
// the value is read.
func TestSecretsSetBitbucketTokenStaysRepoScope(t *testing.T) {
	sm := secretsFixture(t)
	appCheckout(t)
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", config.BitbucketSecret)
	if err != nil {
		t.Fatalf("the bare command from a checkout: %v (%s)", err, errOut)
	}
	if got := string(sm.Latest(bitbucketSecret)); got != tokenValue {
		t.Fatalf("stored %q, want acme/app's own copy %s", got, bitbucketSecret)
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", config.BitbucketSecret)) != nil {
		t.Error("the bare command stored the project copy of a per-repository credential")
	}
	if !strings.Contains(out, bitbucketSecret) || strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("out %q errOut %q", out, errOut)
	}
	// The store never announces a project reach for it, because it has none.
	if strings.Contains(errOut, "is stored project-wide") {
		t.Errorf("stderr claims a project-wide reach for a per-repository credential: %q", errOut)
	}

	// --project-wide without an allow-list is refused, before the value is read.
	spy := &readerSpy{Reader: strings.NewReader(tokenValue)}
	cmd := NewRootCmd()
	var wideOut, wideErr bytes.Buffer
	cmd.SetIn(spy)
	cmd.SetOut(&wideOut)
	cmd.SetErr(&wideErr)
	cmd.SetArgs([]string{"secrets", "set", config.BitbucketSecret, "--project-wide"})
	err = cmd.Execute()
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "project_secrets."+config.BitbucketSecret) ||
		!strings.Contains(err.Error(), "--repo") {
		t.Fatalf("err = %v, want the refusal to name the entry and the --repo way out", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", config.BitbucketSecret)) != nil {
		t.Fatal("a refused value was stored")
	}

	// The owner's explicit opt-in is still the owner's: an entry that lists a
	// repository is accepted, as it was before D2 (#343's own path).
	projectSecretFixture(t, "project_secrets:\n  "+config.BitbucketSecret+": { allow_data_to: [acme/app] }\n")
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", config.BitbucketSecret, "--project-wide"); err != nil {
		t.Fatalf("an allow-listed project-wide store: %v", err)
	}
	if got := string(sm.Latest(gcp.ProjectSecretID("aurora", config.BitbucketSecret))); got != tokenValue {
		t.Fatalf("the allow-listed project copy = %q", got)
	}
}

// TestBareRepoOptsAProjectScopeNameOutOfTheProjectDefault: `--repo` with no
// value is the documented opt-out of the project-scope default (the checkout's
// origin supplies the repository), and it is the one spelling that reaches the
// `!cmd.Flags().Changed("repo")` branch with a name that HAS a project-wide
// meaning. The valued form never gets there, and npm-token is repository-scoped
// either way, so nothing else covers this: a bare `--repo` that fell through to
// the default would store the project copy instead of the repository's own.
func TestBareRepoOptsAProjectScopeNameOutOfTheProjectDefault(t *testing.T) {
	sm := secretsFixture(t)
	appCheckout(t)
	const name = config.AnthropicSecret
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", name, "--repo")
	if err != nil {
		t.Fatalf("secrets set %s --repo: %v (%s)", name, err, errOut)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, name))); got != tokenValue {
		t.Fatalf("stored %q, want the repository copy %s", got, gcp.SecretID(appSlug, name))
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", name)) != nil {
		t.Error("the bare --repo stored the project copy of a name it opted out of")
	}
	if !strings.Contains(out, gcp.SecretID(appSlug, name)) || strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("out %q errOut %q", out, errOut)
	}
	// An explicit request for this repository's own copy needs no note about
	// the project copy it is opting out of.
	if strings.Contains(errOut, "is stored project-wide") {
		t.Errorf("the bare --repo printed the project-scope note: %q", errOut)
	}
}

// TestProjectWideIsStillAccepted: the flag every existing script and skill
// writes stays a synonym of the default, and lands on the same container.
func TestProjectWideIsStillAccepted(t *testing.T) {
	sm := secretsFixture(t)
	const name = config.AnthropicSecret
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", name, "--project-wide"); err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID("aurora", name)
	if got := string(sm.Latest(id)); got != tokenValue {
		t.Fatalf("stored %q, want %s", got, id)
	}
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", name); err != nil {
		t.Fatalf("the default: %v", err)
	}
	if got := string(sm.Latest(id)); got != tokenValue {
		t.Fatalf("the default stored somewhere else: %q", got)
	}
	if sm.Latest(gcp.SecretID(appSlug, name)) != nil {
		t.Error("the default stored a repository copy")
	}
}

// TestAnthropicKeyNeedsNoAllowDataToList: D2 sweeps the reserved names in
// (plan Task 2.1 step 2): the key stores project-wide with no
// project_secrets entry anywhere, and the value is never read before a
// refusal that would still be a refusal.
func TestAnthropicKeyNeedsNoAllowDataToList(t *testing.T) {
	sm := secretsFixture(t) // the fixture's local config has no project_secrets at all
	for _, args := range [][]string{
		{"secrets", "set", "anthropic-api-key"},
		{"secrets", "set", "anthropic-api-key", "--project-wide"},
	} {
		if _, _, err := executeStdin(t, "sk-ant-not-a-real-key-01", args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if got := string(sm.Latest(gcp.ProjectSecretID("aurora", "anthropic-api-key"))); got != "sk-ant-not-a-real-key-01" {
		t.Fatalf("stored %q", got)
	}
}

// TestOAuthTokenStillRefusesProjectScope: D2 covers the API keys and the git
// credential, not the Claude OAuth token (design project-centric §7, §6.3):
// the default account's bare claude-oauth-token is not swept in. With no
// flag it keeps the repository scope it has today, and --project-wide still
// needs the project_secrets entry that lists a repository -- refused before
// the value is read.
func TestOAuthTokenStillRefusesProjectScope(t *testing.T) {
	sm := secretsFixture(t)
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "claude-oauth-token"))); got != tokenValue {
		t.Fatalf("stored %q, want the repository copy", got)
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", "claude-oauth-token")) != nil {
		t.Error("the default Claude account's token went project-wide")
	}
	spy := &readerSpy{Reader: strings.NewReader(tokenValue)}
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(spy)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"secrets", "set", "claude-oauth-token", "--project-wide"})
	err := cmd.Execute()
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "project_secrets") {
		t.Fatalf("err = %v", err)
	}
	if spy.read {
		t.Fatal("the value was read before the refusal")
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", "claude-oauth-token")) != nil {
		t.Fatal("a refused value was stored")
	}
}

// TestSecretsSetBareRepoUsesThisCheckout: a bare `--repo` means this
// checkout's repository, the same one a missing --repo used to mean, and
// asks for its copy (plan Task 2.1 step 3b, finding 13).
func TestSecretsSetBareRepoUsesThisCheckout(t *testing.T) {
	sm := secretsFixture(t)
	appCheckout(t)
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token", "--repo")
	if err != nil {
		t.Fatalf("bare --repo: %v (%s)", err, errOut)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "npm-token"))); got != tokenValue {
		t.Fatalf("stored %q, want this checkout's repository copy", got)
	}
	if !strings.Contains(out, gcp.SecretID(appSlug, "npm-token")) {
		t.Fatalf("out = %q", out)
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", "npm-token")) != nil {
		t.Error("a bare --repo stored a project copy")
	}
	// The listing takes the same bare form: one repository's view.
	if out, _, err := execute(t, "secrets", "ls", "--repo"); err != nil || !strings.Contains(out, gcp.SecretID(appSlug, "npm-token")) {
		t.Fatalf("ls --repo = %q, %v", out, err)
	}
}

// TestSecretsSetKeepsRepoScopeForAWorkflowSecret: a repository's own
// application secret has no project-wide meaning, so the new default does
// not sweep it in: with no flag it keeps today's repository scope.
func TestSecretsSetKeepsRepoScopeForAWorkflowSecret(t *testing.T) {
	sm := secretsFixture(t)
	appCheckout(t)
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "npm-token"); err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest(gcp.SecretID(appSlug, "npm-token"))); got != tokenValue {
		t.Fatalf("stored %q, want the repository copy", got)
	}
	if sm.Latest(gcp.ProjectSecretID("aurora", "npm-token")) != nil {
		t.Error("a workflow secret was stored project-wide")
	}
}

// TestSecretsLsDefaultsToProjectAndSaysSo: the listing follows the same
// default as set, with --repo for one repository's view, and says which
// container its rows come from (plan Task 2.1 step 3b).
func TestSecretsLsDefaultsToProjectAndSaysSo(t *testing.T) {
	sm := secretsFixture(t)
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", config.AnthropicSecret, "--project-wide"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "secrets", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, gcp.ProjectSecretID("aurora", config.AnthropicSecret)) {
		t.Fatalf("the default listing is not the project's:\n%s", out)
	}
	if strings.Contains(out, gcp.SecretID(appSlug, "claude-oauth-token")) {
		t.Fatalf("the default listing is not the project's: it shows a repository container:\n%s", out)
	}
	if strings.Contains(out, tokenValue) {
		t.Fatal("the value leaked")
	}
	// --project-wide is still accepted, and is the same listing.
	wide, _, err := execute(t, "secrets", "ls", "--project-wide")
	if err != nil || wide != out {
		t.Fatalf("--project-wide differs from the default: %q, %v", wide, err)
	}
	// The help text says which container each form shows.
	help, _, _ := execute(t, "secrets", "ls", "--help")
	if !strings.Contains(help, "--repo") || !strings.Contains(help, "project") {
		t.Fatalf("secrets ls help does not say which container each form lists:\n%s", help)
	}
	_ = sm
}

// TestNormalizeRepoArgs is the table the argv rewrite is pinned by: a bare
// `--repo` (no "=value", no token after it) becomes `--repo=`, whose empty
// value resolveSecretRepo reads as this checkout's origin, and nothing else
// changes -- the space-separated form every script writes, `--repo=` as typed,
// anything after `--`, anything outside `secrets set` / `secrets ls` (init's
// --repo is a bool), and everything before the subcommand (the global flags
// and their values) are left byte for byte as they came in. The input slice is
// not mutated: cmd/fugaro/main.go passes os.Args[1:] into it.
func TestNormalizeRepoArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"bare after set", []string{"secrets", "set", "bitbucket-token", "--repo"},
			[]string{"secrets", "set", "bitbucket-token", "--repo="}},
		{"with a value", []string{"secrets", "set", "bitbucket-token", "--repo", "acme/app"},
			[]string{"secrets", "set", "bitbucket-token", "--repo", "acme/app"}},
		{"equals form", []string{"secrets", "set", "bitbucket-token", "--repo=acme/app"},
			[]string{"secrets", "set", "bitbucket-token", "--repo=acme/app"}},
		{"bare before another flag", []string{"secrets", "set", "npm-token", "--repo", "--json"},
			[]string{"secrets", "set", "npm-token", "--repo=", "--json"}},
		{"bare at the end of ls", []string{"secrets", "ls", "--repo"},
			[]string{"secrets", "ls", "--repo="}},
		{"bare with no NAME yet", []string{"secrets", "set", "--repo"},
			[]string{"secrets", "set", "--repo="}},
		{"the singular alias", []string{"secret", "set", "npm-token", "--repo"},
			[]string{"secret", "set", "npm-token", "--repo="}},
		{"bare followed by --", []string{"secrets", "set", "npm-token", "--repo", "--"},
			[]string{"secrets", "set", "npm-token", "--repo=", "--"}},
		{"after -- it is positional", []string{"secrets", "set", "npm-token", "--", "--repo"},
			[]string{"secrets", "set", "npm-token", "--", "--repo"}},
		{"global flags stay put", []string{"--config", "/tmp/c.yaml", "secrets", "set", "npm-token", "--repo"},
			[]string{"--config", "/tmp/c.yaml", "secrets", "set", "npm-token", "--repo="}},
		{"init's --repo is a bool", []string{"init", "--repo"},
			[]string{"init", "--repo"}},
		// A flag's *value* is not the command: the scan reads argv the way
		// cobra's own does, so the first token that spells "secrets" being a
		// value does not hide the subcommand behind it -- the rewrite would
		// otherwise leave the bare --repo unrewritten and pflag would take the
		// next token as its value.
		{"a flag value that spells secrets", []string{"--config", "secrets", "secrets", "set", "npm-token", "--repo"},
			[]string{"--config", "secrets", "secrets", "set", "npm-token", "--repo="}},
		{"a flag value that spells secrets, ls", []string{"--project", "secrets", "secrets", "ls", "--repo"},
			[]string{"--project", "secrets", "secrets", "ls", "--repo="}},
		{"a one-letter flag's value spells secrets", []string{"-c", "secrets", "secrets", "set", "npm-token", "--repo=acme/app"},
			[]string{"-c", "secrets", "secrets", "set", "npm-token", "--repo=acme/app"}},
		// The same token in a command that is not the secrets one: nothing is
		// rewritten, because the command is the first positional and it is not
		// "secrets".
		{"secrets as a bare argument of another command", []string{"run", "secrets", "set", "--repo"},
			[]string{"run", "secrets", "set", "--repo"}},
		// A flag written with "=value" takes no separate token, so what
		// follows it is the command.
		{"an equals-form flag before the command", []string{"--config=x", "secrets", "set", "npm-token", "--repo"},
			[]string{"--config=x", "secrets", "set", "npm-token", "--repo="}},
		{"after -- there is no subcommand", []string{"--", "secrets", "set", "npm-token", "--repo"},
			[]string{"--", "secrets", "set", "npm-token", "--repo"}},
		{"another subcommand of secrets", []string{"secrets", "rm", "npm-token", "--repo"},
			[]string{"secrets", "rm", "npm-token", "--repo"}},
		{"no subcommand after secrets", []string{"secrets"},
			[]string{"secrets"}},
		{"no secrets at all", []string{"run", "--repo", "acme/app", "a task"},
			[]string{"run", "--repo", "acme/app", "a task"}},
		{"empty", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := slices.Clone(tc.in)
			got := NormalizeRepoArgs(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("NormalizeRepoArgs(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// The caller's slice is not written through: main passes
			// os.Args[1:] straight in.
			if !slices.Equal(tc.in, in) {
				t.Errorf("the argument slice was mutated: %q, want %q", tc.in, in)
			}
		})
	}
	// The rewritten form is what cobra then parses as "no value": the empty
	// value is resolveSecretRepo's "this checkout's origin", not a repository
	// named "".
	if got := NormalizeRepoArgs([]string{"secrets", "ls", "--repo"}); got[2] != "--repo=" {
		t.Fatalf("the rewritten token is %q, not the --repo= form pflag reads as empty", got[2])
	}
}
