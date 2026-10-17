package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The command's own tests, and what they are about: this release ships
// `fugaro managed apply` INERT (managed.Shipped is closed, Task 4.4 opens it),
// so most of what a live apply would do cannot be reached through the command
// tree here. Two things can be, and they are the two that matter for the
// release: the refusal is the first thing the command does — before a project
// is selected, before a bucket is read and before terraform is launched — and
// every piece of the wiring underneath it decides in the fail-closed
// direction, which is asserted directly on those pieces.
//
// A test fixture is not a credential, and a string that looks like one stops a
// run at push protection, so every token below is built here by repetition.

// fixtureToken is a stand-in for a token of kind: long enough to be read as
// one, and built rather than written as a literal.
func fixtureToken(kind string) string { return kind + strings.Repeat("0", 24) }

// TestManagedApplyIsInertAndReadsNothing: in this release the managed apply is
// refused by config, and the refusal happens before the command has read
// anything at all. That is asserted the way it can fail: the exit code, the
// plan's own sentence, and the fakes' records — no request reached GCS, the
// Resource Manager or the IAM fake, no terraform was launched, and the
// workdir was never made. An inertness that skipped a read on the way to the
// refusal would pass a test that only read the message.
func TestManagedApplyIsInertAndReadsNothing(t *testing.T) {
	noAgentSession(t)
	r := newInitRig(t)
	root := t.TempDir() // a root that exists: the refusal is not about it

	out, errOut, err := execute(t, "managed", "apply", root)
	if err == nil {
		t.Fatalf("the managed apply ran to completion with the gate closed:\n%s\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d: a refused apply is the user's to act on", got, ExitUserError)
	}
	if !strings.Contains(err.Error(), managedmode.NotEnabled) {
		t.Errorf("the refusal is not the plan's own sentence:\n%v", err)
	}
	if !strings.Contains(err.Error(), "nothing was applied") {
		t.Errorf("the refusal does not say that nothing happened:\n%v", err)
	}
	// The refusal names no flag this command lacks, and this command has none
	// to name: no way to pass a delete address, no way to skip a prompt.
	assertNamesNoFlagItLacks(t, newManagedApplyCmd(), err)
	// Nothing was read: not a bucket, not a project, not an IAM policy.
	for name, reqs := range map[string]int{
		"storage":        len(r.gcs.Requests()),
		"resource (CRM)": len(r.crm.Requests()),
		"iam":            len(r.iam.Requests()),
	} {
		if reqs != 0 {
			t.Errorf("the refused apply made %d %s request(s): the gate is the first thing the command reads", reqs, name)
		}
	}
	// And nothing ran: no terraform call was logged, and the workdir the apply
	// would have copied the root into does not exist.
	if _, err := os.Stat(r.log); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("terraform was launched on the way to the refusal (%s: %v)", r.log, err)
	}
	wd, err := infra.ManagedWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wd); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the managed workdir %s exists after a refused apply", wd)
	}
}

// TestManagedApplyRegistersNoWayToNameAnAddress: D72/D74 say an agent can
// never authorize its own delete or replace, and the mechanical half of that
// is this: the command registers no flag and takes no argument that names an
// address, so there is nothing for a session to pass.
func TestManagedApplyRegistersNoWayToNameAnAddress(t *testing.T) {
	managed := newManagedCmd()
	if managed.Name() != "managed" {
		t.Fatalf("the noun group is %q, want managed (D76)", managed.Name())
	}
	apply, args, err := managed.Find([]string{"apply"})
	if err != nil || apply == managed {
		t.Fatalf("fugaro managed has no apply subcommand: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("fugaro managed apply resolved with leftover args %v", args)
	}
	if !strings.HasPrefix(apply.Use, "apply ROOT") {
		t.Errorf("apply's Use is %q: the root is its one argument", apply.Use)
	}
	for _, name := range []string{"allow-delete", "yes", "non-interactive", "json", "plan-only"} {
		if apply.Flags().Lookup(name) != nil {
			t.Errorf("apply registers --%s: a managed apply takes no address and no way past a prompt of its own", name)
		}
	}
	// The flags it does declare are the cloud flags and nothing else, so a new
	// flag cannot arrive here unnoticed.
	declared := map[string]bool{}
	apply.Flags().VisitAll(func(f *pflag.Flag) { declared[f.Name] = true })
	want := map[string]bool{}
	newAllowDeleteCmd().Flags().VisitAll(func(f *pflag.Flag) { want[f.Name] = true })
	for name := range declared {
		if !want[name] {
			t.Errorf("apply declares --%s, which fugaro allow-delete's cloud flags do not: a flag this command added needs to say what it does in a managed project", name)
		}
	}
	// --project is among them: the command acts on a project.
	if !declared["project"] {
		t.Error("apply does not declare --project, so a stock install cannot be reached from a checkout-less shell")
	}
	// One argument, exactly: the root.
	for _, args := range [][]string{{}, {"a", "b"}} {
		if _, _, err := execute(t, append([]string{"managed", "apply"}, args...)...); err == nil {
			t.Errorf("fugaro managed apply %v ran with %d argument(s)", args, len(args))
		}
	}
}

// TestManagedApplyRootMustBeInTheProjectsCheckout: D75 (ii) says the deployer
// applies the agent-authored root IN the project repository, and a root the
// checkout does not hold is a root nobody reviewed — an agent that can point
// this command at any directory on the machine can have it plan a tree it never
// committed, which is the whole of the property the pull request carries. The
// checkout is read from the directory's own git toplevel, and the project from
// its fugaro.yaml.
func TestManagedApplyRootMustBeInTheProjectsCheckout(t *testing.T) {
	noAgentSession(t)
	testutil.IsolateGit(t)
	r := newInitRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	checkout := func(t *testing.T, project string) string {
		t.Helper()
		dir := t.TempDir()
		testutil.Git(t, dir, "init", "-q")
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte("project: "+project+"\ngcp_project: "+initProject+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	mine := checkout(t, initProjectName)
	root := filepath.Join(mine, "infra")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := managedRoot(t.Context(), lc, root)
	if err != nil {
		t.Fatalf("a root in the project's own checkout was refused: %v", err)
	}
	if got != root {
		t.Errorf("the root resolved to %s, want %s", got, root)
	}
	// Another project's checkout: refused, naming it.
	other := checkout(t, "someone-else")
	out, err := managedRoot(t.Context(), lc, other)
	if err == nil {
		t.Fatalf("a root in another project's checkout was accepted (%s)", out)
	}
	if !strings.Contains(err.Error(), "someone-else") {
		t.Errorf("the refusal does not name the project the checkout belongs to: %v", err)
	}
	// No checkout at all: refused.
	if _, err := managedRoot(t.Context(), lc, t.TempDir()); err == nil {
		t.Error("a root outside every checkout was accepted")
	}
	// A file, not a directory: refused.
	file := filepath.Join(mine, "main.tf")
	if err := os.WriteFile(file, []byte("# a root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := managedRoot(t.Context(), lc, file); err == nil {
		t.Error("a root that is a file was accepted")
	}
}

// TestManagedCoverIsTheConfigsOwnAllowlist: the members a managed plan may
// grant a role to are the config's own launchers and operators — the list
// init's review screen shows — and the two fields this command deliberately
// leaves empty decide against admitting rather than for it: no billing account
// may be named, and the buckets are the project's own runs bucket alone.
func TestManagedCoverIsTheConfigsOwnAllowlist(t *testing.T) {
	noAgentSession(t)
	r := newInitRig(t)
	r.appendConfig(t, "image_source: ghcr.io/acme\nterraform:\n  launchers: [user:launcher@example.com, user:other@example.com]\n  operators: [user:operator@example.com]\n")
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := managedCover(lc, initRunsBucket)
	if !slicesEqual(c.Projects, []string{initProject}) {
		t.Errorf("Cover.Projects = %v, want just this project", c.Projects)
	}
	if c.Region != lc.Region {
		t.Errorf("Cover.Region = %q, want %q", c.Region, lc.Region)
	}
	if !slicesEqual(c.Buckets, []string{initRunsBucket}) {
		t.Errorf("Cover.Buckets = %v, want the project's own runs bucket", c.Buckets)
	}
	want := []string{"user:launcher@example.com", "user:other@example.com", "user:operator@example.com"}
	if !slicesEqual(sorted(c.Listed), sorted(want)) {
		t.Errorf("Cover.Listed = %v, want the config's own launchers and operators %v", c.Listed, want)
	}
	if len(c.Billing) != 0 {
		t.Errorf("Cover.Billing = %v, want none: this command has no review screen to show a billing account on, so a plan that names one is refused", c.Billing)
	}
	if c.Ghcr != infra.GhcrRegistry {
		t.Errorf("Cover.Ghcr = %q, want %q", c.Ghcr, infra.GhcrRegistry)
	}
	// The owner half is the config's own recorded image_source, never the
	// reference being covered (which would make the rule vacuous) — and a
	// config that records none refuses every remote-shaped entry rather than
	// trusting one.
	if want := baseOwner(lc); c.Owner != want || want != "acme" {
		t.Errorf("Cover.Owner = %q, want the config's own recorded owner %q (the fixture records ghcr.io/acme)", c.Owner, want)
	}
	empty, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	empty.ImageSource = ""
	if got := managedCover(empty, initRunsBucket).Owner; got != "" {
		t.Errorf("Cover.Owner = %q with no recorded image_source, want empty: an empty owner refuses every remote path", got)
	}
	if c.Registry == "" {
		t.Error("Cover.Registry is empty, so every image reference is refused rather than covered")
	}
}

// operatorADC writes an operator's own ADC file and points
// GOOGLE_APPLICATION_CREDENTIALS at it, returning its bytes.
func operatorADC(t *testing.T) []byte {
	t.Helper()
	adc := filepath.Join(t.TempDir(), "adc.json")
	src := []byte(`{"type":"authorized_user","client_id":"` + fixtureToken("id-") +
		`","client_secret":"` + fixtureToken("secret-") +
		`","refresh_token":"` + fixtureToken("refresh-") + `"}`)
	if err := os.WriteFile(adc, src, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	return src
}

// isolateCredentialDir points credentialDir at a directory of the test's own
// and returns it.
func isolateCredentialDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := credentialDir
	credentialDir = dir
	t.Cleanup(func() { credentialDir = old })
	return dir
}

// TestManagedApplyWritesTheDeployersCredentialFileAndRemovesIt: terraform's
// only channel for "run as the deployer" is the credential file its provider
// reads, so this is the file the apply hands it: the ADC library's own
// impersonated_service_account form, carrying the operator's own credential as
// its source and the impersonation the credential was minted through, mode
// 0600 in a directory of its own (0700, never the workdir, which is the root's
// parent), and gone — directory and all — when the cleanup runs.
func TestManagedApplyWritesTheDeployersCredentialFileAndRemovesIt(t *testing.T) {
	noAgentSession(t)
	src := operatorADC(t)
	base := isolateCredentialDir(t)
	account := managedmode.DeployerAccount(initProject)
	u := deployerImpersonationURL("https://iamcredentials.example.test", account)

	path, cleanup, err := writeImpersonationFile(u)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	dir := filepath.Dir(path)
	if filepath.Dir(dir) != base {
		t.Errorf("the credential file is at %s, want it in a directory of its own under %s", path, base)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s's mode is %v, want %v: it holds the operator's own refresh token", p, st.Mode().Perm(), want)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f impersonationFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("the credential file is not JSON: %v", err)
	}
	if f.Type != "impersonated_service_account" {
		t.Errorf("its type is %q, want the ADC library's impersonated_service_account form", f.Type)
	}
	if want := "https://iamcredentials.example.test/v1/projects/-/serviceAccounts/" + account + ":generateAccessToken"; f.ServiceAccountImpersonationURL != want {
		t.Errorf("its impersonation URL is\n%s\nwant\n%s", f.ServiceAccountImpersonationURL, want)
	}
	if string(f.SourceCredentials) != string(src) {
		t.Errorf("its source_credentials are %s, want the operator's own ADC file byte for byte", f.SourceCredentials)
	}
	cleanup()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the credential directory is still at %s after the cleanup", dir)
	}
}

// TestManagedApplyRefusesACredentialItCannotImpersonateWith: the assumption is
// made with the operator's own credential, which is a person's — the grant D75
// creates is on var.operators — so a service account's ADC file, or none at
// all, is a refusal rather than a file whose source is something else. Neither
// refusal leaves a file behind.
func TestManagedApplyRefusesACredentialItCannotImpersonateWith(t *testing.T) {
	noAgentSession(t)
	u := deployerImpersonationURL("https://iamcredentials.example.test", managedmode.DeployerAccount(initProject))
	for _, tc := range []struct {
		name, body string
		unset      bool
		want       string
	}{
		{name: "a service account's own credential", want: "service_account",
			body: `{"type":"service_account","client_email":"someone@` + initProject + `.iam.gserviceaccount.com","private_key_id":"` + fixtureToken("k-") + `"}`},
		{name: "no credential file at all", unset: true, want: "Application Default Credentials"},
		{name: "a file that is not a credential", body: `{"hello":"world"}`, want: "not a credential file this command reads"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "adc.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.unset {
				path = filepath.Join(t.TempDir(), "absent.json")
			}
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
			base := isolateCredentialDir(t)
			out, cleanup, err := writeImpersonationFile(u)
			cleanup()
			if err == nil {
				t.Fatalf("the credential file was written (%s) for %s", out, tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %v, want it to say %q", err, tc.want)
			}
			entries, err := os.ReadDir(base)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("a refused credential file left %d entr(ies) behind", len(entries))
			}
		})
	}
}

// fakeCalls are the fake terraform's recorded calls: the arguments, the
// environment, and whether the credential file was there when it ran.
func (r *initRig) fakeCalls(t *testing.T) []struct {
	Args        []string `json:"args"`
	Env         []string `json:"env"`
	Credentials bool     `json:"credentials"`
} {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if err != nil {
		t.Fatal(err)
	}
	var out []struct {
		Args        []string `json:"args"`
		Env         []string `json:"env"`
		Credentials bool     `json:"credentials"`
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c struct {
			Args        []string `json:"args"`
			Env         []string `json:"env"`
			Credentials bool     `json:"credentials"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// TestManagedTerraformRunsWithTheDeployerCredentialAlone: the residual (c) of
// PR #483, as this command wires it, and the credential file's lifetime.
//
// The protection is the allowlist, not a refusal: tf.Env builds terraform's
// environment from scratch, so the provider settings of the applier's shell —
// GOOGLE_CREDENTIALS, GOOGLE_OAUTH_ACCESS_TOKEN, GOOGLE_BILLING_PROJECT and
// every *_CUSTOM_ENDPOINT — are DROPPED rather than carried, and the one
// variable that would otherwise make terraform run as someone else
// (GOOGLE_IMPERSONATE_SERVICE_ACCOUNT) is refused outright. What this command
// adds is the one variable the allowlist does pass: the credential file, put in
// the parent BEFORE managed.TerraformEnv is called, so the assertion it makes
// is about exactly the environment terraform will run with.
//
// And the order: the file outlives managedTerraform — every terraform built
// with it (init, plan, show, apply, all later) finds it there — and the
// cleanup it returns is what removes it; every refusal on the way removes it
// too.
func TestManagedTerraformRunsWithTheDeployerCredentialAlone(t *testing.T) {
	noAgentSession(t)
	r := newInitRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	operatorADC(t)
	base := isolateCredentialDir(t)
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "plugins")
	account := managedmode.DeployerAccount(initProject)
	u := deployerImpersonationURL("https://iamcredentials.example.test", account)

	// The environment, on its own: the shell's credential file and the
	// provider settings it may hold.
	file, cleanupFile, err := writeImpersonationFile(u)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupFile()
	base2 := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GOOGLE_APPLICATION_CREDENTIALS=/somewhere/else.json"}
	for _, k := range []string{"GOOGLE_OAUTH_ACCESS_TOKEN", "GOOGLE_CREDENTIALS", "GOOGLE_BILLING_PROJECT", "GOOGLE_COMPUTE_CUSTOM_ENDPOINT"} {
		base2 = append(base2, k+"="+fixtureToken("shell-"))
	}
	env, err := managedmode.TerraformEnv(replaceEnv(base2, "GOOGLE_APPLICATION_CREDENTIALS", file), dir, cache)
	if err != nil {
		t.Fatalf("building terraform's environment: %v", err)
	}
	if got := envValue(env, "GOOGLE_APPLICATION_CREDENTIALS"); got != file {
		t.Errorf("terraform's GOOGLE_APPLICATION_CREDENTIALS is %q, want this command's own file %q", got, file)
	}
	for _, k := range []string{"GOOGLE_OAUTH_ACCESS_TOKEN", "GOOGLE_CREDENTIALS", "GOOGLE_BILLING_PROJECT", "GOOGLE_COMPUTE_CUSTOM_ENDPOINT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "TF_CLI_ARGS_plan"} {
		if v, ok := envHas(env, k); ok {
			t.Errorf("terraform's environment carries %s=%s: the applier's shell must not set a provider up", k, v)
		}
	}
	// GOOGLE_IMPERSONATE_SERVICE_ACCOUNT is refused rather than dropped, so a
	// run that meant to be someone else stops instead of quietly applying as
	// the deployer (tf.Env's own rule, reached through TerraformEnv).
	if _, err := managedmode.TerraformEnv(replaceEnv(base2, "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", account), dir, cache); err == nil {
		t.Error("GOOGLE_IMPERSONATE_SERVICE_ACCOUNT in the applier's shell was not refused")
	}
	cleanupFile()

	// And the function that builds terraform.
	wd := &infra.Workdir{Dir: dir, Root: filepath.Join(dir, "root")}
	if err := os.MkdirAll(wd.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := newManagedApplyCmd()
	cmd.SetErr(&strings.Builder{})
	bin := filepath.Join(r.dir, "bin", "terraform")
	tfr, cleanup, err := managedTerraform(cmd, lc, wd, bin, cache, testCredential{account: account, url: u})
	if err != nil {
		t.Fatalf("building terraform for the deployer: %v", err)
	}
	// After the build and before the cleanup, terraform still runs with the
	// file: this is the init, plan, show and apply that come later.
	if err := tfr.Init(t.Context(), nil); err != nil {
		t.Fatalf("the fake's init: %v", err)
	}
	calls := r.fakeCalls(t)
	if len(calls) == 0 {
		t.Fatal("no terraform call was recorded: the fake was not launched with the built environment")
	}
	last := calls[len(calls)-1]
	if last.Args[0] != "init" || !last.Credentials {
		t.Errorf("terraform %v ran with the credential file there = %v, want an init that finds it", last.Args, last.Credentials)
	}
	used := envValue(last.Env, "GOOGLE_APPLICATION_CREDENTIALS")
	if used == "" {
		t.Fatal("terraform ran with no credential file named")
	}
	if rel, err := filepath.Rel(dir, used); err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("the credential file %s is inside the workdir %s, the root's parent", used, dir)
	}
	cleanup()
	if _, err := os.Stat(used); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the credential file %s survives its cleanup", used)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("the cleanup left %d entr(ies) behind", len(entries))
	}

	// Every refusal on the way leaves nothing behind either.
	for _, tc := range []struct {
		name string
		bin  string
		cred managedmode.Credential
	}{
		{"a credential for another account", bin, testCredential{account: "someone-else@" + initProject + ".iam.gserviceaccount.com", url: u}},
		{"a credential whose impersonation names another account", bin, testCredential{account: account, url: deployerImpersonationURL("https://iamcredentials.example.test", "someone-else@"+initProject+".iam.gserviceaccount.com")}},
		{"a credential that names no impersonation", bin, plainCredential{account: account}},
		{"a terraform that does not start", filepath.Join(t.TempDir(), "absent"), testCredential{account: account, url: u}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, cleanup, err := managedTerraform(cmd, lc, wd, tc.bin, cache, tc.cred); err == nil {
				cleanup()
				t.Fatal("accepted")
			}
			if entries, _ := os.ReadDir(base); len(entries) != 0 {
				t.Errorf("the refusal left %d entr(ies) behind", len(entries))
			}
		})
	}
}

// TestManagedApplyAssumesTheDeployerFromItsImpersonation: the account the
// credential reports is read out of the impersonation URL terraform's file
// carries — the one Google mints for — and two answers must agree with it: the
// account asked for, and the email of an ID token minted through the same
// grant. A disagreement is a refusal. The calls go out with the operator's own
// credential, to the API's own path.
func TestManagedApplyAssumesTheDeployerFromItsImpersonation(t *testing.T) {
	noAgentSession(t)
	account := managedmode.DeployerAccount(initProject)
	operator, minted := fixtureToken("op-"), fixtureToken("minted-")

	var (
		mu    sync.Mutex
		calls []string
		email = account
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer "+operator {
			t.Errorf("%s carried Authorization %q, want the operator's own credential", r.URL.Path, got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, ":generateAccessToken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": minted, "expireTime": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		case strings.HasSuffix(r.URL.Path, ":generateIdToken"):
			mu.Lock()
			e := email
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"token": idToken(e)})
		default:
			t.Errorf("unexpected call %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	imp := deployerImpersonator{
		self: "user:operator@example.com", ts: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: operator}),
		endpoint: srv.URL, hc: srv.Client(),
	}
	if imp.Self() != "user:operator@example.com" {
		t.Errorf("Self() = %q: the grant D75 creates is on var.operators, whose members are people", imp.Self())
	}
	cred, err := imp.Impersonate(t.Context(), account)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Account() != account {
		t.Errorf("Credential.Account() = %q, want %q", cred.Account(), account)
	}
	ic, ok := cred.(impersonatedCredential)
	if !ok {
		t.Fatal("the credential names no impersonation, so terraform's file could not be written from it")
	}
	if got, err := impersonationAccount(ic.ImpersonationURL()); err != nil || got != account {
		t.Errorf("the credential's impersonation names %q (%v), want %q", got, err, account)
	}
	tok, err := cred.Source().Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != minted {
		t.Errorf("the credential's access token is not the one the API minted")
	}
	// A token minted for another account is refused, and named.
	mu.Lock()
	email = "someone-else@" + initProject + ".iam.gserviceaccount.com"
	mu.Unlock()
	if _, err := imp.Impersonate(t.Context(), account); err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Errorf("a credential minted for another account is %v, want a refusal naming it", err)
	}
	// And the account comes from the impersonation alone: a credential whose
	// URL cannot be read reports no account, which managed.Deployer refuses.
	if a := (deployerCredential{url: "https://example.test/not-an-impersonation"}).Account(); a != "" {
		t.Errorf("a credential with no readable impersonation reports %q, want no account", a)
	}
	// The two calls are the API's own, on the project wildcard its own
	// documentation uses: the account is the path's, not this run's.
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 4 {
		t.Fatalf("the assumption made %d call(s) %v, want an access token and an ID token each time", len(calls), calls)
	}
	if want := "/v1/projects/-/serviceAccounts/" + account + ":generateAccessToken"; calls[0] != want {
		t.Errorf("the first call was %s, want %s", calls[0], want)
	}
}

// The impersonation URL is read strictly: the account it names, or an error.
func TestImpersonationAccountReadsTheURLStrictly(t *testing.T) {
	account := managedmode.DeployerAccount(initProject)
	if got, err := impersonationAccount(deployerImpersonationURL("https://iamcredentials.googleapis.com", account)); err != nil || got != account {
		t.Errorf("got (%q, %v), want %q", got, err, account)
	}
	for _, bad := range []string{
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/:generateAccessToken",
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + account + ":generateIdToken",
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + account + ":generateAccessToken?x=1",
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/a%2Fb:generateAccessToken",
		"https://iamcredentials.googleapis.com/v2/" + account + ":generateAccessToken",
		"::",
	} {
		if got, err := impersonationAccount(bad); err == nil {
			t.Errorf("%s read as %q, want an error", bad, got)
		}
	}
}

// A person at a terminal types the project's name after the apply's own
// question; anything else is a no.
func TestManagedAskWantsTheProjectsNameTyped(t *testing.T) {
	r := newInitRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{lc.Name + "\n", true},
		{"yes\n", false},
		{"", false},
	} {
		cmd := newManagedApplyCmd()
		var out strings.Builder
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(tc.in))
		ok, err := managedAsk(cmd, lc)("apply the saved plan (sha256 abc): 1 to create")
		if err != nil || ok != tc.want {
			t.Errorf("typed %q: (%v, %v), want %v", tc.in, ok, err, tc.want)
		}
		if !strings.Contains(out.String(), "sha256 abc") || !strings.Contains(out.String(), "Type "+lc.Name) {
			t.Errorf("the prompt does not carry the question and what to type:\n%s", out.String())
		}
	}
}

// TestManagedApplyRefusesAMissingImpersonationGrant: an operator without the
// token-creator grant is a user error that names what to ask the owner for, and
// the refusal is ErrNoImpersonationGrant so the caller can tell it apart from
// an API that did not answer.
func TestManagedApplyRefusesAMissingImpersonationGrant(t *testing.T) {
	noAgentSession(t)
	account := managedmode.DeployerAccount(initProject)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"code": http.StatusForbidden, "status": "PERMISSION_DENIED",
			"message": "Permission 'iam.serviceAccounts.getAccessToken' denied on resource",
		}})
	}))
	defer srv.Close()
	imp := deployerImpersonator{
		self: "user:operator@example.com", ts: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fixtureToken("op-")}),
		endpoint: srv.URL, hc: srv.Client(),
	}
	_, err := imp.Impersonate(t.Context(), account)
	if !errors.Is(err, managedmode.ErrNoImpersonationGrant) {
		t.Fatalf("a refused assumption is %v, want ErrNoImpersonationGrant", err)
	}
	if !strings.Contains(err.Error(), "roles/iam.serviceAccountTokenCreator") {
		t.Errorf("the refusal does not say which grant is missing: %v", err)
	}
	// And it names no flag: what the operator does about it is ask the owner.
	for _, banned := range []string{"--impersonate", "--yes", "export ", "GOOGLE_"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("the refusal names %q, which its reader would take as a way through: %v", banned, err)
		}
	}
}

// TestManagedApplyTokenCreatorIgnoresAConditionalGrant: a binding under an IAM
// condition may exclude this very caller, and a policy read cannot evaluate the
// condition — so the direction of that mistake is "the grant is missing", never
// "the grant is there". The deployer's project-level role is conditioned on the
// managed name prefix (D75), which makes this the difference between a refusal
// that is a nuisance and one that is a hole.
func TestManagedApplyTokenCreatorIgnoresAConditionalGrant(t *testing.T) {
	noAgentSession(t)
	r := newInitRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	account := managedmode.DeployerAccount(initProject)
	const operator = "user:operator@example.com"
	r.iam.AddServiceAccount(initProject, account, "the deployer")
	a := deployerAccess{svc: c.IAM, project: initProject}

	// Conditional: not counted.
	r.iam.SetServiceAccountPolicy(initProject, account, gcpfake.Binding{
		Role: tf.DeployerTokenCreatorRole, Members: []string{operator},
		Condition: &gcpfake.IAMCondition{Title: "fugaro managed", Expression: `resource.name.startsWith("projects/-/serviceAccounts/fmgd-")`},
	})
	if ok, err := a.TokenCreator(t.Context(), account, operator); err != nil || ok {
		t.Errorf("a conditioned binding is (ok=%v, err=%v), want it not counted", ok, err)
	}
	// Unconditional: counted, and only for the member it names and the role it
	// is about.
	r.iam.SetServiceAccountPolicy(initProject, account, gcpfake.Binding{Role: tf.DeployerTokenCreatorRole, Members: []string{operator}})
	if ok, err := a.TokenCreator(t.Context(), account, operator); err != nil || !ok {
		t.Errorf("the operator's own binding is (ok=%v, err=%v), want it counted", ok, err)
	}
	if ok, err := a.TokenCreator(t.Context(), account, "user:someone-else@example.com"); err != nil || ok {
		t.Errorf("another member's read is (ok=%v, err=%v), want false", ok, err)
	}
	r.iam.SetServiceAccountPolicy(initProject, account, gcpfake.Binding{Role: "roles/iam.serviceAccountUser", Members: []string{operator}})
	if ok, err := a.TokenCreator(t.Context(), account, operator); err != nil || ok {
		t.Errorf("a different role is (ok=%v, err=%v), want false: the table is an allowlist", ok, err)
	}
	// Existence, and its own two directions: an account that is not there is
	// false with no error, and a read that fails is an error rather than a no.
	if ok, err := a.Exists(t.Context(), account); err != nil || !ok {
		t.Errorf("Exists(%s) = (%v, %v), want true", account, ok, err)
	}
	gone := "fugaro-deployer-absent@" + initProject + ".iam.gserviceaccount.com"
	if ok, err := a.Exists(t.Context(), gone); err != nil || ok {
		t.Errorf("Exists(%s) = (%v, %v), want false with no error", gone, ok, err)
	}
	r.iam.HidePolicy("projects/" + initProject + "/serviceAccounts/" + account)
	if _, err := a.TokenCreator(t.Context(), account, operator); err == nil {
		t.Error("a policy read that failed is not an error, and an unread grant must never be a grant")
	}
}

// TestManagedApplyRefusalsMapToExitCodes: the exit codes are part of the CLI
// contract, and which side of the line each refusal falls on is the user's
// question — the gate, the marker, the owner's list and the deployer account
// are the user's to act on (1); an API or terraform that failed is not (2).
func TestManagedApplyRefusalsMapToExitCodes(t *testing.T) {
	noAgentSession(t)
	r := newInitRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "the gate refused the plan", err: &managedmode.RefusedError{}, want: ExitUserError},
		{name: "the apply is not enabled", err: managedmode.ErrNotEnabled, want: ExitUserError},
		{name: "the project is not managed", err: managedmode.ErrNotManaged, want: ExitUserError},
		{name: "the command named an address", err: managedmode.ErrSessionAddresses, want: ExitUserError},
		{name: "the root is one the gate cannot see", err: managedmode.ErrPreflight, want: ExitUserError},
		{name: "the saved plan changed", err: managedmode.ErrPlanChanged, want: ExitUserError},
		{name: "a provider setting reached terraform", err: managedmode.ErrProviderSetting, want: ExitUserError},
		{name: "no deployer account", err: managedmode.ErrNoDeployerAccount, want: ExitUserError},
		{name: "no impersonation grant", err: managedmode.ErrNoImpersonationGrant, want: ExitUserError},
		{name: "the request cannot run", err: managedmode.ErrRequest, want: ExitUserError},
		{name: "the session may not confirm the apply", err: managedmode.ErrNotConfirmed, want: ExitUserError},
		{name: "the impersonation failed", err: managedmode.ErrImpersonationFailed, want: ExitRemoteError},
		{name: "terraform failed", err: &tf.ExitError{Command: "apply", Code: 1, Stderr: "Error: something"}, want: ExitRemoteError},
		{name: "the API failed", err: errors.New("googleapi: Error 503"), want: ExitRemoteError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := managedApplyErr(lc, "infra", managedmode.Result{}, tc.err)
			if ExitCode(got) != tc.want {
				t.Errorf("exit %d, want %d: %v", ExitCode(got), tc.want, got)
			}
			if got == nil {
				t.Fatal("the refusal is nil")
			}
		})
	}
	// The gate's own refusal carries its verdict out, so the person who reads
	// it can see which rule refused what.
	refused := &managedmode.RefusedError{Verdict: managedmode.Verdict{Refusals: []managedmode.Refusal{{Gate: "guard", Address: "google_storage_bucket.data", Why: "a delete nobody approved"}}}}
	got := managedApplyErr(lc, "infra", managedmode.Result{}, refused)
	for _, want := range []string{"the gate refused", "google_storage_bucket.data", "a delete nobody approved"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, got)
		}
	}
}

// What this command tells the apply about the session it runs in — the input
// of D55's rule that only the caller has. It is read from the environment and
// from this command's own stdin, and it carries no --yes and no
// --non-interactive, because the command registers neither.
func TestManagedConditionsReadTheSessionAndNothingElse(t *testing.T) {
	cmd := newManagedApplyCmd()
	for _, tc := range []struct {
		name string
		env  string
		term bool
		want initflow.Conditions
	}{
		{"a coding agent's session", "CLAUDECODE", false, initflow.Conditions{Agent: "CLAUDECODE"}},
		{"a person at a terminal", "", true, initflow.Conditions{Terminal: true}},
		{"a pipe", "", false, initflow.Conditions{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDECODE", tc.env)
			old := stdinIsTerminal
			stdinIsTerminal = func(io.Reader) bool { return tc.term }
			t.Cleanup(func() { stdinIsTerminal = old })
			if got := managedConditions(cmd); got != tc.want {
				t.Errorf("conditions %+v, want %+v", got, tc.want)
			}
		})
	}
}

// idToken is a JWT carrying email in its payload, unsigned: what the API
// answers with, and all this command reads of it.
func idToken(email string) string {
	payload, err := json.Marshal(map[string]any{"email": email, "aud": "fugaro-managed-apply"})
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("not-a-signature"))
}

// testCredential is a Credential for the paths that need one and do not make
// one: the account it reports, the impersonation it names, and a source that
// mints nothing.
type testCredential struct{ account, url string }

func (c testCredential) Account() string { return c.account }

func (c testCredential) ImpersonationURL() string { return c.url }

func (c testCredential) Source() oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fixtureToken("assumed-")})
}

// plainCredential is a Credential that names no impersonation.
type plainCredential struct{ account string }

func (c plainCredential) Account() string { return c.account }

func (c plainCredential) Source() oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fixtureToken("assumed-")})
}

// envValue is the value of key in env, "" when env has none.
func envValue(env []string, key string) string {
	v, _ := envHas(env, key)
	return v
}

// envHas is the value of key in env and whether it is there at all.
func envHas(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// sorted is names in their own order, so a comparison does not depend on the
// order a config happened to list.
func sorted(in []string) []string { return slices.Sorted(slices.Values(in)) }

// slicesEqual is a and b with the same entries in the same order.
func slicesEqual(a, b []string) bool { return slices.Equal(a, b) }
