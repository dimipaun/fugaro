package localcfg

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// golden compares got with testdata/<name>, rewriting it under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs (run with -update to rewrite):\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

const fullSample = `version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
bucket_url: file:///somewhere/runs
registry: us-central1-docker.pkg.dev/my-project/fugaro
registry_host: us-central1-docker.pkg.dev/my-project
base_images:
  base: us-central1-docker.pkg.dev/my-project/fugaro-base/base:1
build:
  machine_type: E2_HIGHCPU_8
  service_account: fugaro-build@my-project.iam.gserviceaccount.com
terraform:
  state_bucket: my-state
  alert_email: ops@example.com
  launchers: [user:a@example.com]
  operators: [group:ops@example.com]
  budget_admins: [user:b@example.com]
  registry_cleanup: on
budget:
  mode: observe
  per_run_usd: 5
  rtdb_url: https://my-fp1-default-rtdb.firebaseio.com
  firebase_project: my-fp1
  firebase_api_key: AIzaFakeKeyForTests0123
  token_signer: signer@my-fp1.iam.gserviceaccount.com
watch:
  burn_alert_usd_per_hour: 2
mailbox:
  keychain_item:
    m~AAECAwQFBgcICQoLDA0ODw: fugaro-mailbox-aurora
credentials_service:
  url: https://fugaro-credentials-abcde-uc.a.run.app
user: someone@example.com
commit_name: Someone Else
commit_email: someone-else@users.noreply.github.com
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*", "anthropic/claude-sonnet-5.5"]
    allow_data_to: [acme/web]
routing:
  claude:
    via: openrouter
    models: [claude-sonnet-5-5]
claude_accounts:
  acme-personal:
    allow_data_to: [acme/web]
batch:
  image: us-central1-docker.pkg.dev/my-project/fugaro/acme-web:1.2.3
  machine_type: e2-standard-4
  boot_image: projects/my-project/global/images/family/fugaro-base-1
  network: projects/my-project/global/networks/fugaro-runs
  subnetwork: projects/my-project/regions/us-central1/subnetworks/fugaro-runs
  service_account: fugaro-batch@my-project.iam.gserviceaccount.com
  secrets:
    GITHUB_TOKEN: projects/my-project/secrets/github-token
  socket_gid: 412
  task_timeout: 6h
max_parallel: 7
endpoints: { run: "http://127.0.0.1:1/", no_auth: true }
update_check: false
secret_rotation_days: 90
secret_expires:
  bitbucket-token: "2027-01-15"
base_branch: trunk
repos:
  acme/web: { provider: github, base_branch: main, workflows: [web] }
`

func fullLocalConfig(t *testing.T) *Config {
	t.Helper()
	lc, err := Parse([]byte(fullSample))
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

func TestSharedSubsetNeverLeaksOwnerOrPersonalFields(t *testing.T) {
	lc := fullLocalConfig(t)
	out, err := lc.Shared().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// The build account's own email rather than the word "service_account":
	// batch.service_account is published (it is an installation fact, not the
	// deprecated build one), so the forbidden word would fail on the block
	// this file now carries.
	for _, forbidden := range []string{"terraform:", "launchers", "alert_email", "user:", "endpoints:", "bucket_url", "registry:", "providers:", "openrouter", "routing:", "claude-sonnet-5-5", "base_branch", "fugaro-build@my-project.iam.gserviceaccount.com", "claude_accounts:", "acme-personal", "update_check", "commit_name", "commit_email", "Someone Else", "someone-else@users.noreply.github.com", "secret_rotation_days", "secret_expires", "2027-01-15"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("published file contains %q:\n%s", forbidden, out)
		}
	}
	golden(t, "shared.golden.yaml", out)
	if _, err := Parse(out); err != nil {
		t.Errorf("published file does not parse: %v", err)
	}
	// The receiver is untouched — including the routing switch, which is the
	// operator's own and stays in their config after it has been published to
	// the node (nothing here reads it again, but Shared must not be the thing
	// that clears it).
	if lc.Terraform.StateBucket == "" || lc.User == "" || lc.Bucket == "" || len(lc.Providers) != 1 || len(lc.ClaudeAccounts) != 1 || lc.Routing == nil || lc.Routing.Claude == nil || lc.Repos["acme/web"].BaseBranch != "main" || lc.BaseBranch != "trunk" || lc.Build.ServiceAccount == "" || lc.UpdateCheck == nil || lc.CredentialsService == nil || lc.CommitName == "" || lc.CommitEmail == "" {
		t.Errorf("Shared modified its receiver: %+v", lc)
	}
}

// TestSharedBlanksCommitIdentity: commit_name and commit_email are one
// machine's person, like user:, and are never published — a launcher who could
// write them into the runs bucket would be choosing whose name every
// teammate's commits carry, and, for commit_email, which forge account the
// history links them to (D604, D606, D649). The fields are read straight off
// the published copy here, where the leak test above only greps its bytes.
func TestSharedBlanksCommitIdentity(t *testing.T) {
	lc := fullLocalConfig(t)
	if lc.CommitName == "" || lc.CommitEmail == "" {
		t.Fatalf("the fixture carries no commit identity to blank: %+v", lc)
	}
	s := lc.Shared()
	if s.CommitName != "" || s.CommitEmail != "" {
		t.Errorf("Shared published commit_name %q and commit_email %q, want both blank", s.CommitName, s.CommitEmail)
	}
	if lc.CommitName == "" || lc.CommitEmail == "" {
		t.Error("Shared cleared the receiver's own commit identity")
	}
}

// TestSharedConfigDropsRotationDays: the operator's own rotation record is
// local-only, for update_check's reason one credential at a time (D503c).
// secret_rotation_days is how often the person who holds the provider accounts
// means to rotate them, and secret_expires is what their own page for a token
// says: neither is a fact of the installation, and a launcher who could write
// the shared file would be choosing when every other machine is told its
// credentials are overdue.
func TestSharedConfigDropsRotationDays(t *testing.T) {
	lc := fullLocalConfig(t)
	if lc.SecretRotationDays == 0 || len(lc.SecretExpires) == 0 {
		t.Fatalf("the fixture records no rotation period or expiry to drop: %+v", lc)
	}
	s := lc.Shared()
	if s.SecretRotationDays != 0 || s.SecretExpires != nil {
		t.Errorf("Shared published secret_rotation_days %d and secret_expires %v, want both dropped", s.SecretRotationDays, s.SecretExpires)
	}
	if lc.SecretRotationDays == 0 || len(lc.SecretExpires) == 0 {
		t.Error("Shared cleared the receiver's own rotation record")
	}
}

func sharedBase(t *testing.T, extra string) *Config {
	t.Helper()
	c, err := Parse([]byte(`version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
` + extra))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMergeSharedAdopterKeepsPublishedRepos(t *testing.T) {
	existing := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: main, workflows: [web] }\n")
	local := sharedBase(t, "repos: {}\n")
	got := MergeShared(existing, local)
	if _, ok := got.Repos["acme/web"]; !ok || len(got.Repos) != 1 {
		t.Errorf("repos = %v, want the published acme/web", got.Repos)
	}
}

func TestMergeSharedLocalRepoWinsAndNewIsAdded(t *testing.T) {
	existing := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: main, workflows: [web] }\n  acme/old: { provider: github, base_branch: main, workflows: [old] }\n")
	local := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: dev, workflows: [api] }\n  acme/new: { provider: github, base_branch: main, workflows: [new] }\n")
	got := MergeShared(existing, local)
	if got.Repos["acme/web"].Workflows[0] != "api" || got.Repos["acme/old"].Workflows[0] != "old" || got.Repos["acme/new"].Workflows[0] != "new" || len(got.Repos) != 3 {
		t.Errorf("repos = %+v", got.Repos)
	}
	// base_branch is never published: the checkout's reviewed fugaro.yaml
	// names it. Neither side's comes through, and the inputs keep theirs.
	for k, r := range got.Repos {
		if r.BaseBranch != "" {
			t.Errorf("repos.%s.base_branch = %q was merged", k, r.BaseBranch)
		}
	}
	if local.Repos["acme/web"].BaseBranch != "dev" || existing.Repos["acme/old"].BaseBranch != "main" {
		t.Error("MergeShared cleared an input's base_branch")
	}
}

// TestMergeSharedKeepsThePublishedBatchBlock: the batch block is published —
// it is the installation's facts (the image a run's job runs, its machine
// type, the network and subnetwork its VMs are placed in, the account they run
// as, the socket gid), not one machine's — so it is a pointer like budget: and
// watch:, and a publish from a machine whose own config has no batch: block
// must not erase what another machine onboarded. Without the keep-rule in
// MergeShared the publisher writes its own nil pointer over the published one,
// silently: the runs bucket's copy is the only place a teammate's batch facts
// live, and every read takes the published file's.
//
// Mutation (verified, run and restored): delete `if out.Batch == nil {
// out.Batch = published.Batch }` from MergeShared: the first assertion fails
// on a nil block, and no other test does.
func TestMergeSharedKeepsThePublishedBatchBlock(t *testing.T) {
	block := "batch:\n  image: us-central1-docker.pkg.dev/my-project/fugaro/acme-web:1\n  service_account: fugaro-batch@my-project.iam.gserviceaccount.com\n  socket_gid: 412\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, "")
	got := MergeShared(existing, local)
	if got.Batch == nil || got.Batch.SocketGID != 412 || got.Batch.ServiceAccount != "fugaro-batch@my-project.iam.gserviceaccount.com" {
		t.Fatalf("a publish from a machine with no batch: block dropped the published one: %+v", got.Batch)
	}
	out, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "socket_gid: 412") {
		t.Errorf("the merged file carries no batch block:\n%s", out)
	}
	// The publisher's own block wins when it has one: a pointer field takes
	// the local value, and a machine that provisioned its own compute root is
	// the one whose gid is right for its own image.
	local = sharedBase(t, strings.Replace(block, "412", "998", 1))
	if got := MergeShared(existing, local); got.Batch == nil || got.Batch.SocketGID != 998 {
		t.Errorf("the local batch block must win: %+v", got.Batch)
	}
	// Shared() carries it: what MergeShared merges from is what readers get.
	if s := existing.Shared(); s.Batch == nil || s.Batch.SocketGID != 412 {
		t.Errorf("Shared() dropped the batch block: %+v", s.Batch)
	}
	// A published object with none leaves the local one alone, and neither
	// side having one writes no batch: key at all (an empty block would be a
	// block that parses and means nothing).
	if got := MergeShared(sharedBase(t, ""), local); got.Batch == nil || got.Batch.SocketGID != 998 {
		t.Errorf("a published object with no batch block dropped the local one: %+v", got.Batch)
	}
	if got := MergeShared(nil, sharedBase(t, "")); got.Batch != nil {
		t.Errorf("no batch block on either side became %+v", got.Batch)
	}
	if out, _ := MergeShared(nil, sharedBase(t, "")).Marshal(); strings.Contains(string(out), "batch") {
		t.Errorf("the merged file carries a batch key nobody set:\n%s", out)
	}
}

func TestMergeSharedScalars(t *testing.T) {
	existing := sharedBase(t, "log_view: projects/my-project/locations/global/buckets/bkt/views/vw\nscheduler_region: us-east4\nmax_parallel: 5\nwatch: { burn_alert_usd_per_hour: 3 }\nbase_images: { base: ex.example/a:1 }\ncompute_prices: { us-central1: { vcpu_second_usd: 0.0001, gib_second_usd: 0.00001 }, us-east1: { vcpu_second_usd: 0.0002, gib_second_usd: 0.00002 } }\n")
	local := sharedBase(t, "scheduler_region: us-central1\nmax_parallel: 9\nbase_images: { base: ex.example/b:2 }\ncompute_prices: { us-central1: { vcpu_second_usd: 0.0003, gib_second_usd: 0.00003 } }\n")
	got := MergeShared(existing, local)
	if got.SchedulerRegion != "us-central1" || got.MaxParallel != 9 {
		t.Errorf("local non-zero must win: %q %d", got.SchedulerRegion, got.MaxParallel)
	}
	if got.LogView == "" || got.Watch == nil {
		t.Errorf("local zero must keep the existing: %q %v", got.LogView, got.Watch)
	}
	// unionMaps: the local key overrides the same key on the existing side
	// (base_images.base, compute_prices.us-central1), and a key only the
	// existing side has survives (compute_prices.us-east1).
	if got.BaseImages["base"] != "ex.example/b:2" {
		t.Errorf("base_images = %v", got.BaseImages)
	}
	if got.ComputePrices["us-central1"].VCPUSecondUSD != 0.0003 || got.ComputePrices["us-east1"].VCPUSecondUSD != 0.0002 {
		t.Errorf("compute_prices = %v", got.ComputePrices)
	}
}

func TestMergeSharedForeignAndNil(t *testing.T) {
	foreign := sharedBase(t, "repos:\n  x/y: { provider: github, base_branch: main, workflows: [web] }\n")
	foreign.GCPProject = "other-project"
	local := sharedBase(t, "")
	if got := MergeShared(foreign, local); len(got.Repos) != 0 {
		t.Errorf("a foreign object was merged: %v", got.Repos)
	}
	renamed := sharedBase(t, "repos:\n  x/y: { provider: github, base_branch: main, workflows: [web] }\n")
	renamed.Name = "other"
	if got := MergeShared(renamed, local); len(got.Repos) != 0 {
		t.Errorf("a foreign name was merged: %v", got.Repos)
	}
	if got := MergeShared(nil, local); got.Name != "aurora" {
		t.Errorf("nil existing: %+v", got)
	}
}

func TestMergeSharedDoesNotMutateInputsOrLeak(t *testing.T) {
	existing := sharedBase(t, "repos:\n  acme/web: { provider: github, base_branch: main, workflows: [web] }\n")
	local := fullLocalConfig(t)
	eBefore, _ := existing.Marshal()
	lBefore, _ := local.Marshal()
	got := MergeShared(existing, local)
	eAfter, _ := existing.Marshal()
	lAfter, _ := local.Marshal()
	if string(eBefore) != string(eAfter) || string(lBefore) != string(lAfter) {
		t.Error("MergeShared mutated an input")
	}
	if len(local.Repos) != 1 || len(existing.Repos) != 1 {
		t.Errorf("input repos changed: %v %v", local.Repos, existing.Repos)
	}
	out, _ := got.Marshal()
	for _, f := range []string{"terraform:", "user:", "endpoints:", "bucket_url", "registry:", "providers:", "routing:", "claude_accounts:"} {
		if strings.Contains(string(out), f) {
			t.Errorf("merged output contains %q", f)
		}
	}
}

// TestMergeSharedNeverCarriesProviders: providers are local-only (a model
// provider's base_url is where a key and code go), so neither the local
// nor the published ones reach the output.
func TestMergeSharedNeverCarriesProviders(t *testing.T) {
	block := "providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, strings.ReplaceAll(strings.ReplaceAll(block, "openrouter", "other"), "deepseek", "qwen"))
	if len(existing.Providers) != 1 || len(local.Providers) != 1 {
		t.Fatalf("fixtures: %v %v", existing.Providers, local.Providers)
	}
	for name, got := range map[string]*Config{"merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, ""))} {
		if got.Providers != nil {
			t.Errorf("%s: providers = %v", name, got.Providers)
		}
		out, _ := got.Marshal()
		if strings.Contains(string(out), "providers") {
			t.Errorf("%s: output carries providers:\n%s", name, out)
		}
	}
	if len(local.Providers) != 1 {
		t.Error("MergeShared cleared the local providers")
	}
}

// TestMergeSharedNeverCarriesClaudeAccounts: claude_accounts is the
// operator-only allow-list of decision T1 (design
// docs/design/named-oauth-tokens.md §3) — never published, so neither the
// local nor a published config's claude_accounts reaches the output. This
// is the regression test for the leak a bucket writer could otherwise use
// to inject or widen the allow-list (MergeShared(nil, lc).Marshal() used to
// carry the operator's whole claude_accounts: block).
func TestMergeSharedNeverCarriesClaudeAccounts(t *testing.T) {
	block := "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/web]\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, strings.ReplaceAll(block, "acme-personal", "other-account"))
	if len(existing.ClaudeAccounts) != 1 || len(local.ClaudeAccounts) != 1 {
		t.Fatalf("fixtures: %v %v", existing.ClaudeAccounts, local.ClaudeAccounts)
	}
	for name, got := range map[string]*Config{"merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, ""))} {
		if got.ClaudeAccounts != nil {
			t.Errorf("%s: claude_accounts = %v", name, got.ClaudeAccounts)
		}
		out, _ := got.Marshal()
		if strings.Contains(string(out), "claude_accounts") {
			t.Errorf("%s: output carries claude_accounts:\n%s", name, out)
		}
	}
	if len(local.ClaudeAccounts) != 1 {
		t.Error("MergeShared cleared the local claude_accounts")
	}
}

// TestMergeSharedNeverCarriesRouting: the routing switch is the provider
// decision from the switch's side (which Claude IDs may leave for a third
// party), and it resolves through providers, which are local-only. Neither the
// local nor a published routing block reaches the output — the merge starts
// from local.Shared(), which clears the field, and nothing fills it back in
// from published.
func TestMergeSharedNeverCarriesRouting(t *testing.T) {
	block := "routing:\n  claude:\n    via: direct\n    models: [claude-sonnet-5-5]\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, block)
	if existing.Routing == nil || local.Routing == nil {
		t.Fatalf("fixtures: %+v %+v", existing.Routing, local.Routing)
	}
	for name, got := range map[string]*Config{"merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, ""))} {
		if got.Routing != nil {
			t.Errorf("%s: routing = %+v, want it dropped", name, got.Routing)
		}
		out, _ := got.Marshal()
		if strings.Contains(string(out), "routing") {
			t.Errorf("%s: output carries routing:\n%s", name, out)
		}
	}
	if local.Routing == nil {
		t.Error("MergeShared cleared the local routing block")
	}
}

// TestSharedDropsBaseBranchAndBuildAccount: Shared() clears every repo's
// base_branch and the deprecated build.service_account, without touching
// the receiver.
func TestSharedDropsBaseBranchAndBuildAccount(t *testing.T) {
	lc := fullLocalConfig(t)
	s := lc.Shared()
	if s.Repos["acme/web"].BaseBranch != "" || s.Repos["acme/web"].Provider != "github" || s.Build.ServiceAccount != "" || s.Build.MachineType != "E2_HIGHCPU_8" {
		t.Errorf("shared = %+v %+v", s.Repos, s.Build)
	}
	if lc.Repos["acme/web"].BaseBranch != "main" || lc.Build.ServiceAccount == "" {
		t.Errorf("the receiver changed: %+v %+v", lc.Repos, lc.Build)
	}
	m := MergeShared(sharedBase(t, "build: { machine_type: E2_HIGHCPU_8 }\n"), lc)
	if m.Build.ServiceAccount != "" {
		t.Errorf("merged build = %+v", m.Build)
	}
}

// The credentials service's URL is the installation's own fact and it is
// PUBLISHED, for the reason batch: is: the machine that runs a launch is often
// not the machine that opted in, and a local-only copy is the ledger item of
// PR #903. A publisher whose own config has no block — a teammate who onboarded
// a repository with `fugaro update --repo`, a CI box whose config came from the
// published file — must not erase it: `fugaro run` reads the block to decide
// whether to put FUGARO_CREDENTIALS_SERVICE_URL on a job, so an erased block is
// a launch that goes out without the variable while the installation has the
// service on.
//
// Mutation (verified, run and restored): delete `if out.CredentialsService ==
// nil { out.CredentialsService = published.CredentialsService }` from
// MergeShared: the second assertion fails on a nil block, and no other test
// does.
func TestMergeSharedKeepsThePublishedCredentialsService(t *testing.T) {
	block := "credentials_service:\n  url: https://fugaro-credentials-abcde-uc.a.run.app\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, "")
	got := MergeShared(existing, local)
	if got.CredentialsService == nil || got.CredentialsService.URL != "https://fugaro-credentials-abcde-uc.a.run.app" {
		t.Fatalf("a publish from a machine with no credentials_service block dropped the published one: %+v", got.CredentialsService)
	}
	out, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "credentials_service:") {
		t.Errorf("the merged file carries no credentials_service block:\n%s", out)
	}
	// Shared() carries it too: what MergeShared merges from is what readers
	// get, and it is what the launcher's own read of a published config sees.
	if s := existing.Shared(); s.CredentialsService == nil || s.CredentialsService.URL == "" {
		t.Errorf("Shared() dropped the credentials service block: %+v", s.CredentialsService)
	}
	// The publisher's own block wins when it has one: the machine that ran the
	// opt-in is the one whose URL is right for the installation it opted in.
	local = sharedBase(t, strings.Replace(block, "abcde", "fghij", 1))
	if got := MergeShared(existing, local); got.CredentialsService == nil || got.CredentialsService.URL != "https://fugaro-credentials-fghij-uc.a.run.app" {
		t.Errorf("the local credentials service block must win: %+v", got.CredentialsService)
	}
	// A published object with none leaves the local one alone, and neither side
	// having one writes no credentials_service: key at all — an installation
	// that has not opted in publishes nothing, and a block with no url would be
	// one the loader refuses (validCredentialsServiceURL).
	if got := MergeShared(sharedBase(t, ""), local); got.CredentialsService == nil || got.CredentialsService.URL == "" {
		t.Errorf("a published object with no credentials service block dropped the local one: %+v", got.CredentialsService)
	}
	if got := MergeShared(nil, sharedBase(t, "")); got.CredentialsService != nil {
		t.Errorf("no credentials service block on either side became %+v", got.CredentialsService)
	}
	if out, _ := MergeShared(nil, sharedBase(t, "")).Marshal(); strings.Contains(string(out), "credentials_service") {
		t.Errorf("the merged file carries a credentials_service key nobody set:\n%s", out)
	}
}

// TestCredentialsServiceURLIsValidated: the block's presence is the opt-in, so
// a block with no url would read as "off" to a launcher that tests the pointer
// and as "on with nothing to call" to one that tests the string — the loader
// refuses it rather than leaving the two readings to chance. What is admitted
// is exactly the shape the service's own audience check admits
// (internal/cli's credentialsServiceAudience, which this rule mirrors): a
// caller whose ID token is minted for anything else is admitted by nobody.
func TestCredentialsServiceURLIsValidated(t *testing.T) {
	for _, bad := range []string{
		"",                         // the block means on, so an empty url is not "off"
		"fugaro-credentials-abcde", // no scheme
		"http://fugaro-credentials-abcde-uc.a.run.app",     // not https
		"https://fugaro-credentials-abcde-uc.a.run.app/v1", // a path is not the service's audience
		"https://fugaro-credentials-abcde-uc.a.run.app?x=1",
		"https://fugaro-credentials-abcde-uc.a.run.app#f",
		"https://user@fugaro-credentials-abcde-uc.a.run.app",
	} {
		_, err := Parse([]byte(fullSampleHead + "credentials_service:\n  url: " + yamlString(bad) + "\n"))
		if err == nil || !strings.Contains(err.Error(), "credentials_service.url") {
			t.Errorf("credentials_service.url %q: err = %v, want a refusal naming it", bad, err)
		}
	}
	for _, ok := range []string{
		"https://fugaro-credentials-abcde-uc.a.run.app",
		"https://fugaro-credentials-abcde-uc.a.run.app/", // Cloud Run's own trailing slash
	} {
		if _, err := Parse([]byte(fullSampleHead + "credentials_service:\n  url: " + yamlString(ok) + "\n")); err != nil {
			t.Errorf("credentials_service.url %q was refused: %v", ok, err)
		}
	}
}

// fullSampleHead is the four fields every config must carry, for tests that
// build their own document around one field of interest.
const fullSampleHead = "version: 1\nname: aurora\ngcp_project: my-project\nregion: us-central1\nruns_bucket: my-runs\n"

// yamlString quotes s for a one-line YAML scalar (the empty string needs the
// quotes; the URLs do not, but quoting them keeps one spelling).
func yamlString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// The recorded owner travels with the entry it validates: a teammate
// adopting the published config must check the pin against the same owner
// the publisher did (design §3.6, §10.7), so MergeShared keeps it — and the
// local machine's own record, when it has one, is the one that wins.
func TestMergeSharedKeepsTheOwnerInStep(t *testing.T) {
	existing := sharedBase(t, "image_source: ghcr.io/acme\nbase_images: {base: us-east5-docker.pkg.dev/p/fugaro-ghcr/acme/fugaro-base:0.7.5@sha256:"+strings.Repeat("a", 64)+"}\n")
	local := sharedBase(t, "")
	got := MergeShared(existing, local)
	if got.ImageSource != "ghcr.io/acme" {
		t.Errorf("image_source = %q, want the published owner kept", got.ImageSource)
	}
	if _, err := got.Marshal(); err != nil {
		t.Fatalf("the merged config does not marshal: %v", err)
	}
	// A local record wins, both halves together: the adopter who recorded
	// their own owner never inherits the published one.
	local = sharedBase(t, "image_source: ghcr.io/other\n")
	if got := MergeShared(existing, local); got.ImageSource != "ghcr.io/other" {
		t.Errorf("image_source = %q, want the local owner", got.ImageSource)
	}
	// Shared() does not drop it either: what is published is what the
	// readers get.
	if s := existing.Shared(); s.ImageSource != "ghcr.io/acme" {
		t.Errorf("the shared config's image_source = %q", s.ImageSource)
	}
}

// TestMergeSharedNeverCarriesProjectSecrets: project_secrets is
// operator-only local config (design docs/design/project-secrets.md §7),
// never published -- neither the local nor a published config's block
// reaches the output.
func TestMergeSharedNeverCarriesProjectSecrets(t *testing.T) {
	block := "project_secrets:\n  anthropic-api-key:\n    allow_data_to: [acme/web]\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, strings.ReplaceAll(block, "anthropic-api-key", "bitbucket-token"))
	if len(existing.ProjectSecrets) != 1 || len(local.ProjectSecrets) != 1 {
		t.Fatalf("fixtures: %v %v", existing.ProjectSecrets, local.ProjectSecrets)
	}
	for name, got := range map[string]*Config{"merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, ""))} {
		if got.ProjectSecrets != nil {
			t.Errorf("%s: project_secrets = %v", name, got.ProjectSecrets)
		}
		out, _ := got.Marshal()
		if strings.Contains(string(out), "project_secrets") {
			t.Errorf("%s: output carries project_secrets:\n%s", name, out)
		}
	}
	if len(local.ProjectSecrets) != 1 {
		t.Error("MergeShared cleared the local project_secrets")
	}
}

// TestMergeSharedNeverCarriesBitbucketAPIRoots: the API-root allow-list is the
// owner's policy about where a run may send the project's Bitbucket token
// (D474), and it travels the way project_secrets does — through the policy
// node, which the runner reads and fails closed on, never through the runs
// bucket's shared file, which any launcher with objectAdmin can write. Neither
// the local nor a published config's list reaches the output.
func TestMergeSharedNeverCarriesBitbucketAPIRoots(t *testing.T) {
	block := "bitbucket_api_roots: [https://api.bitbucket.example/2.0]\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, "bitbucket_api_roots: [https://bucket-source.example/2.0]\n")
	if len(existing.BitbucketAPIRoots) != 1 || len(local.BitbucketAPIRoots) != 1 {
		t.Fatalf("fixtures: %v %v", existing.BitbucketAPIRoots, local.BitbucketAPIRoots)
	}
	for name, got := range map[string]*Config{"shared": local.Shared(), "merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, ""))} {
		if got.BitbucketAPIRoots != nil {
			t.Errorf("%s: bitbucket_api_roots = %v", name, got.BitbucketAPIRoots)
		}
		out, err := got.Marshal()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "bitbucket_api_roots") {
			t.Errorf("%s: output carries bitbucket_api_roots:\n%s", name, out)
		}
	}
	if len(local.BitbucketAPIRoots) != 1 {
		t.Error("Shared/MergeShared cleared the local list")
	}
}

// TestMergeSharedNeverCarriesProfiles: profiles are the owner's policy — the
// job shapes, secret mounts and admission lists of the project's shared jobs —
// and they travel through the policy node, which the runner reads and fails
// closed on (`fugaro project policy publish`). The shared config is a file any
// launcher with objectAdmin on the runs bucket can write, so a profiles block
// in it would let a launcher redefine the jobs every repository runs on. The
// receiver keeps its own; the published and merged copies have none, and the
// marshalled bytes carry no key at all.
//
// Mutation (verified, run and restored): drop `s.Profiles = nil` from Shared:
// this test fails on the first output carrying profiles:, and no other test
// does.
func TestMergeSharedNeverCarriesProfiles(t *testing.T) {
	block := "profiles:\n" +
		"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h }\n" +
		"  wide: { provider: github, cpu: 4, memory: 8Gi, timeout_class: 12h, allow_repos: [\"github/acme/*\"] }\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, strings.ReplaceAll(block, "wide", "wider"))
	if len(existing.Profiles) != 2 || len(local.Profiles) != 2 {
		t.Fatalf("fixtures: %v %v", existing.Profiles, local.Profiles)
	}
	for name, got := range map[string]*Config{
		"shared":         local.Shared(),
		"merged":         MergeShared(existing, local),
		"nil published":  MergeShared(nil, local),
		"published only": MergeShared(existing, sharedBase(t, "")),
	} {
		if got.Profiles != nil {
			t.Errorf("%s: profiles = %v", name, got.Profiles)
		}
		out, err := got.Marshal()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "profiles:") {
			t.Errorf("%s: output carries profiles:\n%s", name, out)
		}
	}
	if len(local.Profiles) != 2 {
		t.Error("Shared/MergeShared cleared the local profiles")
	}
}

// TestMergeSharedNeverPublishesTheProjectBaseBranch: the installation
// config's top-level base_branch is the owner's, published to the policy
// node (fugaro project policy publish) and never to the runs bucket's
// shared file, which any launcher can write — a machine that could flip
// every repository's base branch by writing there is the shape the
// per-repo key is already kept out of. So a publish from a machine whose
// own config sets one writes none, and one that does not never adopts the
// published.
func TestMergeSharedNeverPublishesTheProjectBaseBranch(t *testing.T) {
	existing := sharedBase(t, "base_branch: trunk\n")
	local := sharedBase(t, "")
	got := MergeShared(existing, local)
	if got.BaseBranch != "" {
		t.Errorf("MergeShared adopted the published base_branch %q", got.BaseBranch)
	}
	local = sharedBase(t, "base_branch: master\n")
	if got := MergeShared(existing, local); got.BaseBranch != "" {
		t.Errorf("MergeShared published the local base_branch %q", got.BaseBranch)
	}
	if existing.BaseBranch != "trunk" || local.BaseBranch != "master" {
		t.Error("MergeShared modified an input's base_branch")
	}
}
