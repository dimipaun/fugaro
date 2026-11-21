package localcfg

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
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
credentials:
  - { name: openrouter-main, type: openrouter }
  - { name: claude-one, type: claude-oauth }
  - { name: anthropic-one, type: anthropic }
pools:
  - { name: claude-pool, strategy: round-robin, members: [claude-one, anthropic-one] }
crafts:
  coder: { model: deepseek-v4-1, access: openrouter-main }
  senior-review: { model: claude-opus-5-5, access: claude-pool }
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
  opt_in: in
  seal_key:
    version: projects/my-project/locations/us-central1/keyRings/fugaro-batch/cryptoKeys/seal-key/cryptoKeyVersions/1
    public_key: |
      -----BEGIN PUBLIC KEY-----
      MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA4HUq0vDPYGjr7BfAyKNA
      XRXxE0+5elG1+QraI8s+0Bx2uUGppkdd8dP3+ECdhfHtVbV8DNo/2iORKRk7DUcB
      uCny+sSxOpypEzLxx72Sc8I2Uw1KOVUJ2V35hPgXYrmQuJsGPjJNsNGNvWLS+F0l
      qLRdw3kuDPmwMU7RgSdGscp3DBonCv+S6Brge+p0nEPWk8NJajRgdrgqsHxdz64O
      Kdnr98DZILIXM+FZd82Bu+O6e0WkaD71IghXJ423z33tHW7H8A/db2MGylzNisqR
      QkuUGQEbInUQXAka5n/89eHKLVAI7DqOIs/cdx/zPlf8VjaB6g2MSCpCu80iXSD6
      mQIDAQAB
      -----END PUBLIC KEY-----
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
	for _, forbidden := range []string{"terraform:", "launchers", "alert_email", "user:", "endpoints:", "bucket_url", "registry:", "providers:", "openrouter", "routing:", "claude-sonnet-5-5", "base_branch", "fugaro-build@my-project.iam.gserviceaccount.com", "claude_accounts:", "acme-personal", "credentials:", "openrouter-main", "claude-one", "pools:", "claude-pool", "crafts:", "deepseek-v4-1", "update_check", "commit_name", "commit_email", "Someone Else", "someone-else@users.noreply.github.com", "secret_rotation_days", "secret_expires", "2027-01-15"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("published file contains %q:\n%s", forbidden, out)
		}
	}
	golden(t, "shared.golden.yaml", out)
	if _, err := Parse(out); err != nil {
		t.Errorf("published file does not parse: %v", err)
	}
	// The published document carries the version every Fugaro of this
	// installation can read, whatever the file it came from says: ConfigVersion
	// 2 renamed the two lists in the terraform block, and this copy has no
	// terraform block, so a published 2 would stop an un-upgraded teammate's
	// Fugaro with "version 2 is newer than the 1 this Fugaro writes" for a
	// document that says nothing version 2 is about.
	if got := lc.Shared().Version; got != SharedVersion {
		t.Errorf("the published config is version %d, want %d", got, SharedVersion)
	}
	// The receiver is untouched — including the routing switch, which is the
	// operator's own and stays in their config after it has been published to
	// the node (nothing here reads it again, but Shared must not be the thing
	// that clears it).
	if lc.Terraform.StateBucket == "" || lc.User == "" || lc.Bucket == "" || len(lc.Providers) != 1 || len(lc.ClaudeAccounts) != 1 || lc.Routing == nil || lc.Routing.Claude == nil || lc.Repos["acme/web"].BaseBranch != "main" || lc.BaseBranch != "trunk" || lc.Build.ServiceAccount == "" || lc.UpdateCheck == nil || lc.CredentialsService == nil || lc.CommitName == "" || lc.CommitEmail == "" || len(lc.Credentials) != 3 || len(lc.Pools) != 1 || lc.Crafts.Coder.Access != "openrouter-main" {
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

// TestMergeSharedCarriesTheRepositoriesCredentialsServiceRecord is the one
// field of a repository entry that is not the local entry's to overwrite: the
// credentials service's record answers for the INSTALLATION — "this
// repository's root has been applied with the service on", and that root is one
// object every checkout shares — while every other field of the entry is this
// machine's own answer about it.
//
// Without the rule (unionMaps alone, the local entry winning whole) any machine
// whose entry carries no record publishes that absence over the record another
// machine wrote, from an apply it made: the runs bucket's file is the
// installation's one copy of it, so a `cascade` repository's every launch — its
// job gave its model mounts up to the apply that wrote the record — is handed
// no service address and halts at the service's door (D143). The field is a
// pointer for the same reason: a recorded false is an apply that ran with the
// switch OFF, which has to travel, while no record at all must not.
//
// Mutation (verified, run and restored): merge repos with unionMaps alone
// (drop the mergeRepoRecords call) and the first half fails, the published
// record gone from the merged config and from the file it marshals to.
func TestMergeSharedCarriesTheRepositoriesCredentialsServiceRecord(t *testing.T) {
	published := sharedBase(t, "repos:\n  acme/web: { provider: github, workflows: [web], credentials_service_ready: true }\n  acme/half: { provider: github, workflows: [web], credentials_service_ready: false }\n")
	local := sharedBase(t, "repos:\n  acme/web: { provider: github, workflows: [api] }\n  acme/half: { provider: github, workflows: [api] }\n")

	got := MergeShared(published, local)
	if !got.Repos["acme/web"].CredentialsServiceOn() {
		t.Errorf("a publish from a machine with no record for acme/web dropped the published one: that machine's own entry carries none — it never applied that root — and the record a teammate's apply wrote is the installation's only record that this repository's job may call the service at all (D143)")
	}
	if got.Repos["acme/half"].CredentialsServiceOn() {
		t.Errorf("the published record false for acme/half was overwritten by the local entry's absence: a recorded false is a run that applied that root with the switch OFF, so it has to travel — the fail-safe direction is the mounts back on, and a merge that erased it leaves the installation's file saying the service is ready for a root whose apply held it off")
	}
	out, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "acme/web:") || !strings.Contains(string(out), "credentials_service_ready: true") {
		t.Errorf("the merged config marshals without the record it kept:\n%s", out)
	}

	// A record the local entry DOES hold wins, in both directions: only the
	// door that applies that root writes one (internal/cli's
	// recordCredentialsServiceReady and the upgrade backfill), so this is the
	// switch-off reaching the other machines.
	off := sharedBase(t, "repos:\n  acme/web: { provider: github, workflows: [api], credentials_service_ready: false }\n")
	if MergeShared(published, off).Repos["acme/web"].CredentialsServiceOn() {
		t.Errorf("a local record of false did not win over the published true: the repository was switched off by an apply, and a record that cannot say so keeps every machine handing the job an address the service no longer serves it at")
	}
	on := sharedBase(t, "repos:\n  acme/half: { provider: github, workflows: [api], credentials_service_ready: true }\n")
	if !MergeShared(published, on).Repos["acme/half"].CredentialsServiceOn() {
		t.Errorf("a local record of true did not win over the published false")
	}

	// Neither argument is modified: the caller keeps its own entries (the local
	// file is written from `next`, not from this result).
	if local.Repos["acme/web"].CredentialsServiceReady != nil || published.Repos["acme/web"].CredentialsServiceReady == nil {
		t.Errorf("MergeShared modified an input's record: local %v, published %v", local.Repos["acme/web"].CredentialsServiceReady, published.Repos["acme/web"].CredentialsServiceReady)
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

// sharedBatchBlock is a publishing machine's own `batch:` block — the compute
// facts it carries into the published object (BR19) — and it is a machine's,
// not the installation's: the image, the machine type and the socket gid are
// what this machine's own compute was provisioned with, which is why the local
// block wins them (MergeShared's whole-block rule, kept).
const sharedBatchBlock = "batch:\n  image: us-central1-docker.pkg.dev/my-project/fugaro/acme-web:1\n  machine_type: e2-standard-4\n  service_account: fugaro-batch@my-project.iam.gserviceaccount.com\n  socket_gid: 412\n"

// sharedBatchBlockOptIn is sharedBatchBlock with the opt-in fact on it, or the
// block itself for "": the fact is absent from a block that never chose Batch
// (C1, D3110 as amended), which is the state most machines that publish are in
// — their block was written by a setup that never selected the backend, or by
// the release behind.
func sharedBatchBlockOptIn(optIn string) string {
	if optIn == "" {
		return sharedBatchBlock
	}
	return sharedBatchBlock + "  opt_in: " + optIn + "\n"
}

// TestPublishingKeepsTheOptIn: the opt-in fact is the INSTALLATION's answer to
// whether its compute root is applied (R2, D3110 as amended), and the published
// object is the only copy every machine reads it from — the local copy of a
// teammate who has no config of their own IS that object — so a publish takes
// the fact from the published copy and never from the publishing machine's own
// block (R15). The machine that publishes is usually one whose block says
// nothing about it: a setup that never chose Batch wrote that block, an operator
// opted the installation in afterwards, and a publish from it would drop the
// fact for everybody.
//
// Mutation (verified, run and restored): delete the `case published != nil`
// arm of mergeBatchBlock's switch, so the block keeps the publishing machine's
// own value — both cases below fail with the fact gone from the merged block
// (and, with it, the withdrawal and the stale-copy cases of the two tests
// below, which is the same mutation seen from their side). Delete the
// empty-bucket arm of MergeSharedOptIn's early return — its mergeBatchBlock call
// — and only the writer's case below fails, here and in the CLI test.
func TestPublishingKeepsTheOptIn(t *testing.T) {
	published := sharedBase(t, sharedBatchBlockOptIn(BatchOptInOn))
	for _, c := range []struct {
		what  string
		local *Config
	}{
		{"a block whose own copy of the fact is absent", sharedBase(t, sharedBatchBlockOptIn(""))},
		{"no batch: block at all", sharedBase(t, "")},
	} {
		got := MergeShared(published, c.local)
		if got.Batch == nil || got.Batch.OptIn != BatchOptInOn || !got.Batch.OptedIn() {
			t.Errorf("a publish from a machine with %s dropped the installation's opt-in: %+v", c.what, got.Batch)
		}
	}
	out, err := MergeShared(published, sharedBase(t, sharedBatchBlockOptIn(""))).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "opt_in: in") {
		t.Errorf("the published file carries no opt-in:\n%s", out)
	}
	// The writers R15 names are the only way the fact is written, and they pass
	// the value they are writing: an operator who selects the backend (R2)
	// writes `in` on an installation whose block carries no fact at all.
	if got := MergeSharedOptIn(sharedBase(t, sharedBatchBlockOptIn("")), sharedBase(t, sharedBatchBlockOptIn("")), BatchOptInOn); got.Batch == nil || !got.Batch.OptedIn() {
		t.Errorf("the value an opt-in writer passed did not reach the block: %+v", got.Batch)
	}
	// The value is written whether or not the bucket holds a copy of the
	// installation's config at all: a fresh bucket has none, and the fact is the
	// one thing a writer's publish adds to its own block that no copy of the
	// machine's own could supply.
	if got := MergeSharedOptIn(nil, sharedBase(t, sharedBatchBlockOptIn("")), BatchOptInOn); got.Batch == nil || !got.Batch.OptedIn() {
		t.Errorf("a writer's value was lost on a bucket with nothing published: %+v", got.Batch)
	}
	// And no other publish introduces a value: with neither copy carrying the
	// fact, the published block carries none either — the absence is a state,
	// not something a publish invents on its way through.
	if got := MergeShared(sharedBase(t, sharedBatchBlockOptIn("")), sharedBase(t, sharedBatchBlockOptIn(""))); got.Batch == nil || got.Batch.OptIn != "" {
		t.Errorf("a publish invented an opt-in nobody wrote: %+v", got.Batch)
	}
}

// TestPublishingKeepsAWithdrawal: a withdrawal is a WRITTEN value and not an
// absence (C1, D3111), so a publish keeps the published `withdrawn` the way it
// keeps `in`, and keeps it apart from the absence: an update applies the
// compute root with the grants off for the first (R16, BR21) and does nothing
// for the second, and the service's deploy input is set from the two (R17), so
// a publish that flattened the withdrawal into "nobody has opted in" would
// turn an installation that opted out into one that never chose Batch.
//
// Mutation (verified, run and restored): narrow the switch's second arm to
// `published != nil && published.OptIn != BatchOptInWithdrawn` — the three
// states read as two, with the withdrawal a fall-back to the machine's own copy
// — the second case below fails with `opt_in: withdrawn` gone from the file (and
// the stale-copy test below with it, which is this mutation from its side).
func TestPublishingKeepsAWithdrawal(t *testing.T) {
	withdrawn := sharedBase(t, sharedBatchBlockOptIn(BatchOptInWithdrawn))
	for _, c := range []struct {
		what  string
		local *Config
	}{
		{"no batch: block at all", sharedBase(t, "")},
		{"a block whose own copy of the fact is absent", sharedBase(t, sharedBatchBlockOptIn(""))},
	} {
		got := MergeShared(withdrawn, c.local)
		if got.Batch == nil || !got.Batch.OptInWithdrawn() || got.Batch.OptedIn() {
			t.Errorf("a publish from a machine with %s lost the withdrawal: %+v", c.what, got.Batch)
		}
	}
	out, err := MergeShared(withdrawn, sharedBase(t, sharedBatchBlockOptIn(""))).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "opt_in: withdrawn") {
		t.Errorf("the published file does not carry the withdrawal as a value:\n%s", out)
	}
}

// TestAStaleLocalOptInDoesNotOverwriteAWithdrawal: the machine's own copy of
// the fact is never the authority (R15), so the machine whose block still says
// `in` — it opted in before an operator withdrew the fact, or its file has
// simply not followed the published copy yet, which is the window O19 is about
// — publishes the withdrawal and not its own value, and the same holds the other
// way round. The published object is what every machine without a config of its
// own reads, so one stale publish reaching it would put the whole installation
// back in (or out) and start VMs the service then refuses the claim of (R17).
//
// Mutation (verified, run and restored): let the opt-in take the local block's
// value again, which is what the switch replaced (its second arm deleted) — the
// first case below fails with `in` published over the withdrawal; and a switch
// that reads the fact as two states rather than three (the withdrawal falling
// back to the machine's own copy) fails it too.
func TestAStaleLocalOptInDoesNotOverwriteAWithdrawal(t *testing.T) {
	published := sharedBase(t, sharedBatchBlockOptIn(BatchOptInWithdrawn))
	stale := sharedBase(t, sharedBatchBlockOptIn(BatchOptInOn))
	got := MergeShared(published, stale)
	if got.Batch == nil || !got.Batch.OptInWithdrawn() || got.Batch.OptedIn() {
		t.Fatalf("a stale local `in` overwrote the published withdrawal: %+v", got.Batch)
	}
	out, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "opt_in: in") {
		t.Errorf("the published file says `in` after a withdrawal:\n%s", out)
	}
	// The other direction is the same rule, and it is the one a machine that
	// opted out and never updated its file is in: it cannot publish its own
	// withdrawal over an installation that opted in again.
	back := sharedBase(t, sharedBatchBlockOptIn(BatchOptInWithdrawn))
	if got := MergeShared(sharedBase(t, sharedBatchBlockOptIn(BatchOptInOn)), back); got.Batch == nil || !got.Batch.OptedIn() {
		t.Errorf("a stale local withdrawal overwrote the published opt-in: %+v", got.Batch)
	}
	// Opting in again (R2) is the way out, and it is the writers' own: they pass
	// the value, and it is the only thing that can write over a withdrawal.
	if got := MergeSharedOptIn(published, stale, BatchOptInOn); got.Batch == nil || !got.Batch.OptedIn() {
		t.Errorf("the value an opt-in writer passed lost to a withdrawal: %+v", got.Batch)
	}
}

// TestEveryOtherBatchFieldMergesAsBefore: the opt-in fact is the one field of
// the block that is read the other way round (R15), and every other field keeps
// the rule the block has always merged by, which is the whole block's: the
// publishing machine's own block wins where it has one — its image and its
// socket gid are what its own compute was provisioned with — and the published
// block is adopted where the machine has none. A publish must not become a
// merge of the block field by field: splicing one machine's image onto another's
// account is a job that boots under an account that may not read that image.
//
// Mutation (verified, run and restored): take the published block whenever the
// local one is not nil (`out = *published` beside the copy in mergeBatchBlock)
// — the first case below fails with the published machine's image and gid, and
// nothing else in this package notices.
func TestEveryOtherBatchFieldMergesAsBefore(t *testing.T) {
	published := sharedBase(t, sharedBatchBlockOptIn(BatchOptInOn))
	local := sharedBase(t, strings.Replace(strings.Replace(sharedBatchBlockOptIn(BatchOptInWithdrawn), "412", "998", 1), "acme-web:1", "acme-web:2", 1))
	got := MergeShared(published, local)
	if got.Batch == nil || got.Batch.SocketGID != 998 || got.Batch.Image != "us-central1-docker.pkg.dev/my-project/fugaro/acme-web:2" || !got.Batch.OptedIn() {
		t.Fatalf("the local block no longer wins its own fields, or the opt-in followed it: %+v", got.Batch)
	}
	if got := MergeShared(published, sharedBase(t, "")); got.Batch == nil || got.Batch.SocketGID != 412 || got.Batch.MachineType != "e2-standard-4" {
		t.Errorf("a machine with no block of its own did not adopt the published one: %+v", got.Batch)
	}
	if got := MergeShared(nil, sharedBase(t, "")); got.Batch != nil {
		t.Errorf("no block on either side became %+v", got.Batch)
	}
	out, err := MergeShared(nil, sharedBase(t, "")).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "batch") {
		t.Errorf("the merged file carries a batch key nobody set:\n%s", out)
	}
}

// TestAKeyFactIsNotPublished: no publish carries a key fact (R15 as re-cut by
// LM19, R21), because the fact is retired and only the launch's own identity
// names a run from here on. Until BR23 removes the field, a machine that still
// holds one can publish — the block is READ and not refused, R10's rule for a
// block that still carries `batch.secrets` — and what the publish does with it
// is what an update does with that key: the fact leaves the block in place,
// with every other field of the block standing, and the machine's own file keeps
// it until an update rewrites that file.
//
// Mutation (verified, run and restored): delete `out.SealKey = nil` from
// mergeBatchBlock — this fails with a published seal_key.
func TestAKeyFactIsNotPublished(t *testing.T) {
	keyed := sharedBase(t, sharedBatchBlockOptIn(BatchOptInOn))
	keyed.Batch.SealKey = fullLocalConfig(t).Batch.SealKey
	if keyed.Batch.SealKey == nil {
		t.Fatal("the fixture carries no key fact to drop")
	}
	got := MergeShared(keyed.Shared(), keyed)
	if got.Batch == nil || got.Batch.SealKey != nil {
		t.Fatalf("a publish from a keyed block published the key fact: %+v", got.Batch)
	}
	if got.Batch.SocketGID != 412 || got.Batch.MachineType != "e2-standard-4" || !got.Batch.OptedIn() {
		t.Errorf("dropping the key fact took the rest of the block with it: %+v", got.Batch)
	}
	out, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "seal_key") {
		t.Errorf("the published file carries a key fact:\n%s", out)
	}
	// The machine's own copy is the one thing a publish leaves alone: the file
	// still parses with the fact on it (the fixture's block does, which is what
	// `fullLocalConfig` reads), and what rewrites it is `fugaro update`, not a
	// publish (R10).
	if s := keyed.Shared(); s.Batch == nil || s.Batch.SealKey == nil {
		t.Error("Shared() dropped the key fact from the machine's own copy, which only an update may do")
	}
}

// TestSharedNeverPublishesTheBackendName: the `batch:` block is published for
// the reason above, and `backend:` is not, and the difference is what
// publishes them can be judged. An operator reading doctor's Batch findings
// sees the machine's own config; a launcher who wrote `backend: cloud-batch`
// into the runs bucket would move every teammate with no local config onto VMs,
// on a block that same launcher chose, with no check in the way — the
// escalation plan 2026-10-19-compute-backend.md Task 10 step 4 forbids. So the
// choice is the writing machine's own, in both directions: Shared drops it and
// MergeShared never adopts a published one.
//
// Mutation (verified, kept in the test): put `keep(&out.BackendName,
// published.BackendName)` back into MergeShared and the merge assertions fail;
// drop `s.BackendName = ""` from Shared and the golden comparison does.
func TestSharedNeverPublishesTheBackendName(t *testing.T) {
	lc := sharedBase(t, "backend: cloud-batch\n")
	if lc.BackendName != backend.CloudBatch {
		t.Fatalf("the fixture's own config lost the name: %q", lc.BackendName)
	}
	s := lc.Shared()
	if s.BackendName != "" {
		t.Errorf("Shared() published the backend choice %q", s.BackendName)
	}
	if out, err := s.Marshal(); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(out), "backend") {
		t.Errorf("the published file carries the key:\n%s", out)
	}
	if lc.BackendName != backend.CloudBatch {
		t.Errorf("the receiver changed: %q", lc.BackendName)
	}
	// A published object that carries the key is not merged back in either,
	// whether the local config names a backend or nothing at all: the merge
	// output is what goes into the runs bucket, so no name it sees — the
	// published one or the merging machine's own — may reach it.
	for _, local := range []*Config{sharedBase(t, ""), sharedBase(t, "backend: cloud-run\n")} {
		if got := MergeShared(sharedBase(t, "backend: cloud-batch\n"), local); got.BackendName != "" {
			t.Errorf("MergeShared wrote the backend %q into the file it publishes", got.BackendName)
		}
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
// docs/named-oauth-tokens/spec.md §3) — never published, so neither the
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

// TestMergeSharedNeverCarriesARepositorysAccounts: the repository-level half of
// the same allow-list — a repos entry's own claude_accounts is what admits a
// repository whose runs are not on a profile job (docs/project-claude-accounts.md
// §3) — is never published either. It travels in the same map as the entry's
// provider and workflows, so the block that clears the top-level key has to
// clear this one field by field: a shared file that carried it would let whoever
// writes the runs bucket add a repository to an account's admission, and the
// machine that published the policy next would derive the admission they wrote.
// The entry itself stays, which is the control: everything else about it is
// published, and a copy that dropped the whole entry would be a different bug
// wearing this one's test.
func TestMergeSharedNeverCarriesARepositorysAccounts(t *testing.T) {
	const entry = "repos:\n  acme/web: { provider: github, workflows: [web], claude_accounts: [acme-personal] }\n"
	local := sharedBase(t, entry)
	published := sharedBase(t, strings.ReplaceAll(entry, "acme-personal", "other-account"))
	if len(local.Repos["acme/web"].ClaudeAccounts) != 1 || len(published.Repos["acme/web"].ClaudeAccounts) != 1 {
		t.Fatalf("fixtures: %+v %+v", local.Repos, published.Repos)
	}
	for name, got := range map[string]*Config{"shared": local.Shared(), "merged": MergeShared(published, local), "nil published": MergeShared(nil, local)} {
		repo := got.Repos["acme/web"]
		if len(repo.ClaudeAccounts) != 0 {
			t.Errorf("%s: repos.acme/web.claude_accounts = %v", name, repo.ClaudeAccounts)
		}
		if repo.Provider != "github" || len(repo.Workflows) != 1 {
			t.Errorf("%s: repos.acme/web = %+v, want the entry's own fields kept", name, repo)
		}
		out, _ := got.Marshal()
		if strings.Contains(string(out), "claude_accounts") {
			t.Errorf("%s: output carries a claude_accounts key:\n%s", name, out)
		}
	}
	if len(local.Repos["acme/web"].ClaudeAccounts) != 1 {
		t.Error("the merge cleared the local entry's own list")
	}
}

// TestParseRefusesARepositorysAccountsWithoutAProvider: the account an entry
// names is published provider-qualified, and a `repos` key states owner/name
// and no provider of its own — the entry's provider field is the only place a
// per-repository job's provider is stated — so an entry that names accounts and
// none is a name the publish could not canonicalise. It is refused where the
// file is read rather than at publish, which is the door the owner's own
// command edits.
func TestParseRefusesARepositorysAccountsWithoutAProvider(t *testing.T) {
	_, err := Parse([]byte(`version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
repos:
  acme/web: { workflows: [web], claude_accounts: [acme-personal] }
`))
	if err == nil {
		t.Fatal("a repos entry that names accounts and no provider parsed")
	}
	for _, want := range []string{"repos.acme/web.claude_accounts", "provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The entry with the provider is the control: the rule is the missing
	// provider and not the key.
	if _, err := Parse([]byte(`version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
repos:
  acme/web: { provider: github, workflows: [web], claude_accounts: [acme-personal] }
`)); err != nil {
		t.Fatalf("a repos entry that names its provider was refused: %v", err)
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

// TestSharedDropsCredentialsPoolsAndCrafts: the installation's credentials, its
// pools and the crafts that choose among them travel the account-choice keys'
// way — into the policy node, never through the runs bucket's shared file —
// and they are dropped here for their reason: a credential names a secret and
// a craft's access decides which one a run's calls go out on, so a launcher who
// could write either into the shared file could aim every teammate's runs at a
// credential the owner never admitted (docs/model-crafts/reconciliation.md
// F12, D1628).
func TestSharedDropsCredentialsPoolsAndCrafts(t *testing.T) {
	lc := fullLocalConfig(t)
	s := lc.Shared()
	if s.Credentials != nil || s.Pools != nil || s.Crafts != (config.Crafts{}) {
		t.Errorf("shared = %+v %+v %+v", s.Credentials, s.Pools, s.Crafts)
	}
	if len(lc.Credentials) != 3 || len(lc.Pools) != 1 || lc.Crafts.Coder.Access == "" {
		t.Errorf("the receiver changed: %+v %+v %+v", lc.Credentials, lc.Pools, lc.Crafts)
	}
	// A published object that carries them is not merged back in either: what
	// Shared drops is never taken from published (MergeShared).
	m := MergeShared(sharedBase(t, "credentials:\n  - {name: claude-one, type: claude-oauth}\ncrafts:\n  coder: { access: claude-one }\n"), lc)
	if m.Credentials != nil || m.Crafts.Coder.Access != "" {
		t.Errorf("a published credential was merged back in: %+v %+v", m.Credentials, m.Crafts)
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
// operator-only local config (design docs/project-secrets/spec.md §7),
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

// TestWorkflowSecretsAreRefusedInTheSharedConfig: workflow_secrets is
// operator-only local config for project_secrets' reason (D1260): each entry's
// allow_data_to is what makes a workflow secret mountable on the repositories
// it names, so a launcher who could write the runs bucket's shared file would
// be widening the grant. Neither the local nor a published config's block
// reaches the output, and the receiver keeps its own.
func TestWorkflowSecretsAreRefusedInTheSharedConfig(t *testing.T) {
	block := "workflow_secrets:\n  deploy-token:\n    allow_data_to: [acme/web]\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, strings.ReplaceAll(block, "deploy-token", "release-token"))
	if len(existing.WorkflowSecrets) != 1 || len(local.WorkflowSecrets) != 1 {
		t.Fatalf("fixtures: %v %v", existing.WorkflowSecrets, local.WorkflowSecrets)
	}
	for name, got := range map[string]*Config{"shared": local.Shared(), "merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, ""))} {
		if got.WorkflowSecrets != nil {
			t.Errorf("%s: workflow_secrets = %v", name, got.WorkflowSecrets)
		}
		out, err := got.Marshal()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "workflow_secrets") {
			t.Errorf("%s: output carries workflow_secrets:\n%s", name, out)
		}
	}
	if len(local.WorkflowSecrets) != 1 {
		t.Error("Shared cleared the receiver's own workflow_secrets")
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

// TestMergeSharedNeverCarriesTheAccountChoiceKeys: the three account-choice
// keys (docs/accounts-add/spec.md §11.1) are local-only for claude_accounts'
// own reason — a key that could name the account a run's stages use, or turn on
// Fugaro's own choice of one, would be a way around that account's
// allow_data_to — so neither the local config's values nor a published file's
// reach the output. They travel to a run through the policy node instead.
func TestMergeSharedNeverCarriesTheAccountChoiceKeys(t *testing.T) {
	defined := "claude_accounts:\n  acme-personal:\n    allow_data_to: [acme/web]\n  acme-overflow:\n    allow_data_to: [acme/web]\n"
	block := "default_claude_account: acme-personal\nclaude_account_auto: true\nstages:\n  review:\n    claude_account: acme-personal\n"
	existing := sharedBase(t, defined+block)
	local := sharedBase(t, defined+strings.ReplaceAll(block, "acme-personal", "acme-overflow"))
	if existing.DefaultClaudeAccount == "" || !existing.ClaudeAccountAuto || len(existing.ClaudeAccountStages) != 1 {
		t.Fatalf("fixtures: %q %v %v", existing.DefaultClaudeAccount, existing.ClaudeAccountAuto, existing.ClaudeAccountStages)
	}
	for name, got := range map[string]*Config{"shared": local.Shared(), "merged": MergeShared(existing, local), "nil published": MergeShared(nil, local), "published only": MergeShared(existing, sharedBase(t, defined))} {
		if got.DefaultClaudeAccount != "" || got.ClaudeAccountAuto || got.ClaudeAccountStages != nil {
			t.Errorf("%s: %q, %v, %v", name, got.DefaultClaudeAccount, got.ClaudeAccountAuto, got.ClaudeAccountStages)
		}
		out, err := got.Marshal()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, key := range []string{"default_claude_account", "claude_account_auto", "stages"} {
			if strings.Contains(string(out), key) {
				t.Errorf("%s: output carries %s:\n%s", name, key, out)
			}
		}
	}
	if local.DefaultClaudeAccount == "" || !local.ClaudeAccountAuto || len(local.ClaudeAccountStages) != 1 {
		t.Error("Shared/MergeShared cleared the local account-choice keys")
	}
}

// TestIssuesNudgeKeysAreNeverPublished: the nudge thresholds are the printing
// machine's own setting (T-IS15, F14) — the Issues section is printed on the
// terminal that runs the digest, after the digest's own output, and the
// committed report carries none — so a published threshold would be a value no
// other machine acts on, written by anyone who can write the runs bucket.
// Shared drops the block; the receiver keeps it, as it does for every key
// dropped only on the way out.
func TestIssuesNudgeKeysAreNeverPublished(t *testing.T) {
	lc := sharedBase(t, "issues:\n  nudge_after_days: 14\n  nudge_recurrence: 5\n")
	if lc.Issues == nil || lc.NudgeAfterDays() != 14 || lc.NudgeRecurrence() != 5 {
		t.Fatalf("the fixture records no thresholds to drop: %+v", lc.Issues)
	}
	out, err := lc.Shared().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"issues:", "nudge_after_days", "nudge_recurrence"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("the published file carries %q:\n%s", forbidden, out)
		}
	}
	if _, err := Parse(out); err != nil {
		t.Errorf("the published file does not read back: %v", err)
	}
	if lc.Issues == nil || lc.NudgeAfterDays() != 14 || lc.NudgeRecurrence() != 5 {
		t.Errorf("Shared cleared the receiver's own thresholds: %+v", lc.Issues)
	}
}
