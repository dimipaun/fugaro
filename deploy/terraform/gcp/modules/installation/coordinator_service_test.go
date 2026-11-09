// The coordinator's front door, held at the plan's own level: it is a Cloud
// Run SERVICE the module creates for an opt-in of its own, reachable by
// everyone (D920) and running as the front door's own account `mcp_server` —
// not the coordinator's — and it must
// carry no credential of any kind (the MCP server's design, spec.md §2 and §8,
// implementation-plan.md M4).
//
// The tests read this module's own Terraform as text. They do not plan it: a
// plan needs the Google provider, which is not in this tree, and what they
// decide is the module's shape rather than a rendered value — whether the
// service exists, who may invoke it, who it runs as, and what its container
// carries. internal/infra/coordinator_service.go holds the Go half of the same
// opt-in, and the golden plans in internal/infra/tf/testdata/golden pin what an
// installation that did NOT opt in plans (nothing of this file).
package installation

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	coordinatorTF = "coordinator.tf"
	variablesTF   = "variables.tf"

	// mcpauthStoreTF is the store's own file, and the reason the store is a
	// file of its own is that the front door's account may be named by exactly
	// one grant in coordinator.tf (the count in
	// TestCoordinatorServiceHasItsOwnAccount reads that file alone): the
	// store's binding lives beside the bucket it binds, and the account's file
	// keeps naming it twice.
	mcpauthStoreTF = "mcpauth_store.tf"
)

// tfFiles reads every Terraform file of this module, comments stripped, keyed
// by base name. A test that has to say "no file of this module writes X" —
// which is what a convenience binding left on a bucket would be — cannot read
// one file and mean it, and the module is the unit a plan is built from.
func tfFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this module's directory: %v", err)
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		files[e.Name()] = tfSource(t, e.Name())
	}
	return files
}

// tfFilesNaming returns the module's files that mention any of the given
// strings, sorted, so a failure names them rather than one of them.
func tfFilesNaming(t *testing.T, files map[string]string, needles ...string) []string {
	t.Helper()
	var out []string
	for name, src := range files {
		for _, n := range needles {
			if strings.Contains(src, n) {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// tfSource reads one of this module's files, with its comments removed: what
// these tests decide is what the module WRITES, and the prose above a resource
// is where a change is explained. A comment saying "no secret_key_ref here" is
// not a mount, and a check that read it as one would make explaining the rule
// the same act as breaking it — which is the shape a security test must never
// have.
func tfSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return stripHashComments(string(b))
}

// stripHashComments drops each line's trailing "#" comment, and only outside a
// quoted string. It reads one comment form and deliberately not the two others
// HCL has: "#" is the only one this module's Terraform carries, and "//" is a
// URL's, so a stripper that took it for a comment would cut lines the module
// means to write — and a mangled line is a check that passes for the wrong
// reason. The name is the form it handles rather than the language it reads,
// so nobody takes it for a parser of the rest.
func stripHashComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		quoted := false
		for i, r := range line {
			if r == '"' {
				quoted = !quoted
				continue
			}
			if r == '#' && !quoted {
				line = line[:i]
				break
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// tfResource returns the text of one resource block, from its header to the
// next resource's. It is deliberately not a parser: every assertion below is
// about a string the block either writes or does not, and a block that stopped
// being the last one in its file must not silently take the next resource's
// text with it — hence the cut at the next header.
func tfResource(t *testing.T, src, typ, name string) string {
	t.Helper()
	header := "resource \"" + typ + "\" \"" + name + "\""
	i := strings.Index(src, header)
	if i < 0 {
		t.Fatalf("%s declares no %s.%s: the coordinator's front door is not created here", coordinatorTF, typ, name)
	}
	rest := src[i+len(header):]
	if j := strings.Index(rest, "\nresource \""); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// tfVariable returns the body of one variable declaration, cut at the next
// declaration the way tfResource cuts a block at the next resource.
func tfVariable(t *testing.T, src, name string) string {
	t.Helper()
	header := "variable \"" + name + "\""
	i := strings.Index(src, header)
	if i < 0 {
		t.Fatalf("%s declares no variable %q", variablesTF, name)
	}
	rest := src[i+len(header):]
	if j := strings.Index(rest, "\nvariable \""); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestCoordinatorServiceIsPublicAndCarriesNoCredential is the M4 red step. Two
// things decide whether this file may exist at all, and they are the two the
// threat model's first paragraph rests on: the service is reachable by anyone
// on the network (D920 — `allUsers`, with the handler the only gate), and
// nothing the container holds is a credential, because a public endpoint that
// carried one would hand it to whoever found the URL first.
//
// A container reaches a credential in exactly two ways in this tree: an
// environment entry whose value comes from a source (`secret_key_ref` and the
// like), which is how every job mounts its provider key, or a literal naming
// one. Both are refused here, over the front door's block alone — the
// aggregation job beside it in the same file mounts the GitHub App's key on
// purpose, so a file-wide check would be a different question.
//
// The mutation this fails on: a `secret_key_ref` on the service's container, an
// env name carrying a model credential, an invoker binding that is not
// `allUsers`, or a service that stopped requiring IAM (`invoker_iam_disabled`),
// which would make the binding advisory.
func TestCoordinatorServiceIsPublicAndCarriesNoCredential(t *testing.T) {
	code := tfSource(t, coordinatorTF)
	svc := tfResource(t, code, "google_cloud_run_v2_service", "coordinator_front_door")

	// The opt-in is the whole switch (D934): the front door is off on every
	// installation until the owner asks for it, and an installation that opted
	// into the coordinator for its read-only view acquires no service at all.
	off := tfVariable(t, tfSource(t, variablesTF), "mcp_server")
	if !regexp.MustCompile(`(?m)^\s*default\s*=\s*null\s*$`).MatchString(off) {
		t.Errorf("%s: var.mcp_server must default to null: D934 makes the front door OFF on every installation that has not asked for it, and an unset variable is what creates nothing:\n%s", variablesTF, off)
	}
	if !regexp.MustCompile(`(?m)^\s*count\s*=\s*local\.mcp_server_on\b`).MatchString(svc) {
		t.Errorf("%s: the service must be created on local.mcp_server_on and on nothing else, or an installation that asked for no front door gets one:\n%s", coordinatorTF, svc)
	}

	// IAM is required and the public binding is the grant: a service that
	// stopped requiring IAM would be open to everyone whether or not the
	// binding below exists, which is a different (and unintended) decision.
	if !regexp.MustCompile(`(?m)^\s*invoker_iam_disabled\s*=\s*false\s*$`).MatchString(svc) {
		t.Errorf("%s: the front door's service must set invoker_iam_disabled = false explicitly — D920's public endpoint is the allUsers invoker grant below, not a service the platform stopped checking IAM on:\n%s", coordinatorTF, svc)
	}
	if !regexp.MustCompile(`(?m)^\s*ingress\s*=\s*"INGRESS_TRAFFIC_ALL"\s*$`).MatchString(svc) {
		t.Errorf("%s: the front door answers the public internet, so its ingress is INGRESS_TRAFFIC_ALL:\n%s", coordinatorTF, svc)
	}

	grant := tfResource(t, code, "google_cloud_run_v2_service_iam_member", "coordinator_front_door_public")
	for _, want := range []struct{ what, re string }{
		{"role", `(?m)^\s*role\s*=\s*"roles/run\.invoker"\s*$`},
		{"member", `(?m)^\s*member\s*=\s*"allUsers"\s*$`},
	} {
		if !regexp.MustCompile(want.re).MatchString(grant) {
			t.Errorf("%s: the front door's invoker binding sets no %s (D920: the endpoint is public and the bearer token is the gate):\n%s", coordinatorTF, want.what, grant)
		}
	}
	if !strings.Contains(grant, "google_cloud_run_v2_service.coordinator_front_door[0]") {
		t.Errorf("%s: the public binding must name the front door's own service, or it grants nothing:\n%s", coordinatorTF, grant)
	}

	res := tfResource(t, code, "google_cloud_run_v2_service", "coordinator_front_door")
	for _, refused := range []struct{ what, literal string }{
		{"a secret mounted from Secret Manager", "secret_key_ref"},
		{"an environment entry whose value comes from a source", "value_source"},
	} {
		if strings.Contains(res, refused.literal) {
			t.Errorf("%s: the front door's container carries %s (%q). Nothing in this design puts a credential on a public endpoint: the coordinator reads its own materials as its service account (spec.md §8, \"a stolen access token reaches no model credential\")", coordinatorTF, refused.what, refused.literal)
		}
	}
	// Belt beside the two braces above: a literal model-credential name in the
	// container's environment would be a credential with no Secret Manager
	// read at all, which is the shape no plan-time check could see.
	modelCredentialEnv := regexp.MustCompile(`(?m)^\s*name\s*=\s*"(FUGARO_[A-Z_]*KEY|ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN|GOOGLE_API_KEY|OPENAI_API_KEY|OPENROUTER_API_KEY)"\s*$`)
	if m := modelCredentialEnv.FindString(res); m != "" {
		t.Errorf("%s: the front door's container sets the environment entry %s: a public endpoint may hold no model credential", coordinatorTF, strings.TrimSpace(m))
	}
}

// TestCoordinatorServiceHasItsOwnAccount pins the identity the front door runs
// as, and the whole of that identity's reach (M4's "its own service account").
//
// It is a dedicated account and NOT the coordinator's, which is the finding
// this test was rewritten for: the sweep's account reads the GitHub App's
// private key (secretAccessor on its one container), mints Bitbucket read
// tokens at the credentials service and holds the FP's database admin through
// the Firebase root (D922's writer), and a public endpoint whose identity held
// any of those would make spec.md §8's "no forge credential … there is none to
// reach" false the day it is turned on. What this account holds is two grants —
// the runs bucket's read (D932) and the store's objectAdmin (D935, in
// mcpauth_store.tf) — and nothing else in this tree or the Firebase root grants
// it anything.
//
// A container that fell back to the default compute account would run the
// front door as an identity nothing in this tree granted anything to and
// nothing revoked from, and the refusal would surface as a 500 nobody could
// read back to a variable.
//
// The mutations this fails on: a template with no service_account, a service
// account that is not the front door's own (the coordinator's, whose comment
// above says what it holds), an account created on the coordinator's opt-in
// rather than the front door's, and a second grant — a secret, a project role,
// a database role — named anywhere in this file for that account.
func TestCoordinatorServiceHasItsOwnAccount(t *testing.T) {
	code := tfSource(t, coordinatorTF)
	svc := tfResource(t, code, "google_cloud_run_v2_service", "coordinator_front_door")
	if !regexp.MustCompile(`(?m)^\s*service_account\s*=\s*google_service_account\.mcp_server\[0\]\.email\s*$`).MatchString(svc) {
		t.Errorf("%s: the front door must run as its OWN account (google_service_account.mcp_server), not the coordinator's: the sweep's account holds the App key, a Bitbucket read at the credentials service and the FP's database admin, and none of the three may sit behind a public endpoint:\n%s", coordinatorTF, svc)
	}
	if strings.Contains(svc, "google_service_account.coordinator") {
		t.Errorf("%s: the front door's service names the coordinator's own account: that is the identity which reads the App key and the whole database (M4 makes the front door's account a separate one):\n%s", coordinatorTF, svc)
	}
	account := tfResource(t, code, "google_service_account", "mcp_server")
	if !regexp.MustCompile(`(?m)^\s*account_id\s*=\s*var\.mcp_server\.account_id\s*$`).MatchString(account) {
		t.Errorf("%s: the front door's account ID comes from var.mcp_server (internal/infra's MCPServerAccountID), so the name is the Go side's one constant:\n%s", coordinatorTF, account)
	}
	if !regexp.MustCompile(`(?m)^\s*count\s*=\s*local\.mcp_server_on\b`).MatchString(account) {
		t.Errorf("%s: the front door's account must be created on local.mcp_server_on and on nothing else, or an installation that asked for no front door gets an account:\n%s", coordinatorTF, account)
	}

	// The account's whole reach, and the reason it is a count rather than a
	// list: the member reference may appear in exactly two places — the grant
	// below and the service that runs as it — so a third one is a grant nobody
	// reviewed. The number is deliberately not derived: reading it out of the
	// file would make this assertion agree with whatever the file says.
	if got := strings.Count(code, "google_service_account.mcp_server"); got != 2 {
		t.Errorf("%s: the front door's account is named %d times, want exactly 2 (the runs-bucket read's member and the service's service_account): every other appearance is a grant this design has not decided, and spec.md §8's \"there is no credential to reach\" rests on there being none", coordinatorTF, got)
	}
	grant := tfResource(t, code, "google_storage_bucket_iam_member", "mcp_server_runs_reader")
	for _, want := range []struct{ what, re string }{
		{"roles/storage.objectViewer", `(?m)^\s*role\s*=\s*"roles/storage\.objectViewer"\s*$`},
		{"the runs bucket", `(?m)^\s*bucket\s*=\s*google_storage_bucket\.runs\.name\s*$`},
		{"the front door's own member", `(?m)^\s*member\s*=\s*google_service_account\.mcp_server\[0\]\.member\s*$`},
		{"the front door's opt-in", `(?m)^\s*count\s*=\s*local\.mcp_server_on\b`},
	} {
		if !regexp.MustCompile(want.re).MatchString(grant) {
			t.Errorf("%s: the front door's runs-bucket grant sets no %s (D932: the tools behind the port answer with a run's recorded artefacts, and this read is one of the account's two grants, the other being the store's in %s):\n%s", coordinatorTF, want.what, mcpauthStoreTF, grant)
		}
	}
	if strings.Contains(code, "roles/firebasedatabase.admin") {
		t.Errorf("%s: the front door's file grants or names the FP's database admin: that is the sweep account's role, granted by the Firebase root, and what a front door may read in the coordinator's own store is D935's own bucket, whose one binding is in %s", coordinatorTF, mcpauthStoreTF)
	}
	// Nobody may act as it: whoever could deploy a revision as it would serve
	// the front door's answers as the installation's own identity.
	if strings.Contains(code, "roles/iam.serviceAccountUser") {
		t.Errorf("%s: nobody is granted actAs on the front door's account, operators included: a deployer as it would serve every run artefact behind the port as this identity", coordinatorTF)
	}
}

// TestTheStoreHasExactlyOneMemberAndItIsTheMCPAccount is D935 at the plan's own
// level: the bucket the mcpauth records live in is a bucket of its own with one
// binding and one member, and the member is the front door's own account
// `mcp_server` — never the coordinator's, which is the sweep's.
//
// The two accounts are two different reaches and this is where the difference
// is decided: the sweep's account holds the GitHub App's key, a Bitbucket read
// at the credentials service and the FP's database admin, and a store whose
// member list named it would put token hashes and revocation state behind an
// identity that reads runs. The sweep reads runs and never tokens (P5), and the
// operator's read of the audit record is served by M10's tool rather than by a
// credential — so the second the store gains a second member is the second this
// design has widened, and this test is what makes that fail rather than happen.
//
// The name is the other half, and it is the correction of 2026-11-05: the store
// is a function of the GCP project alone and carries no `fmgd-` prefix, because
// the managed deployer's `fugaroDeployerStorage` is a project-level binding
// conditioned on that prefix (deployer.tf:379, :142) and every operator can act
// as the deployer (deployer.tf:501). A prefixed name would put the store inside
// the reach it exists to leave — F32 again, one resource over.
//
// The mutations this fails on: a second grant on the store's bucket anywhere in
// this module, a member that is the coordinator's account or a literal, a role
// other than objectAdmin, and a name that gained `fmgd-`.
func TestTheStoreHasExactlyOneMemberAndItIsTheMCPAccount(t *testing.T) {
	files := tfFiles(t)
	code, ok := files[mcpauthStoreTF]
	if !ok {
		t.Fatalf("this module has no %s: D935's store is a bucket of its own with one binding, and the file that creates both is %s", mcpauthStoreTF, mcpauthStoreTF)
	}
	grant := tfResource(t, code, "google_storage_bucket_iam_member", "mcpauth_store")
	for _, want := range []struct{ what, re string }{
		{"roles/storage.objectAdmin", `(?m)^\s*role\s*=\s*"roles/storage\.objectAdmin"\s*$`},
		{"the store's own bucket", `(?m)^\s*bucket\s*=\s*google_storage_bucket\.mcpauth_store\[0\]\.name\s*$`},
		{"the front door's own member", `(?m)^\s*member\s*=\s*google_service_account\.mcp_server\[0\]\.member\s*$`},
		{"the front door's opt-in", `(?m)^\s*count\s*=\s*local\.mcp_server_on\b`},
	} {
		if !regexp.MustCompile(want.re).MatchString(grant) {
			t.Errorf("%s: the store's one binding sets no %s (D935: the store's IAM names the front door's own account mcp_server and nobody else, and objectAdmin is the narrowest role the plan review accepts here with no condition):\n%s", mcpauthStoreTF, want.what, grant)
		}
	}
	// The member is the front door's own account, and the sweep's account is
	// named nowhere near the store: it is a different identity with a different
	// reach, and its name here would be the widening this test exists for.
	if strings.Contains(grant, "google_service_account.coordinator") {
		t.Errorf("%s: the store's binding names the coordinator's own account: that is the sweep's account, which reads runs and never tokens, and the store's one member is the front door's own (D935):\n%s", mcpauthStoreTF, grant)
	}
	if strings.Contains(code, "google_service_account.coordinator") {
		t.Errorf("%s: the store's file names the coordinator's own account at all: the store is the front door's own state and the sweep reads runs, not tokens (D935)", mcpauthStoreTF)
	}

	// One binding, one member: nothing else in this module may name the store's
	// bucket, and the name itself is written once. A second grant is the second
	// member, however it is spelled.
	if named := tfFilesNaming(t, files, "google_storage_bucket.mcpauth_store", "mcpauth_store_bucket"); len(named) != 2 ||
		named[0] != mcpauthStoreTF || named[1] != "outputs.tf" {
		t.Errorf("the store's bucket is named by %v, want exactly [%s outputs.tf]: the bucket and its one binding live in the store's own file, and the output that publishes its name is the installation's own (D935: the store's member list is one, and a grant in a third file is a member nobody reviewed)", named, mcpauthStoreTF)
	}

	// The name, and the prefix it must not carry. The expression is pinned as
	// an expression rather than read back out of the file, so a later edit that
	// made the name a variable, or a function of anything but the project,
	// fails here rather than shipping a name a caller could choose.
	if !strings.Contains(code, `"fugaro-mcpauth-${var.project}"`) {
		t.Errorf("%s: the store's bucket is not named fugaro-mcpauth-${var.project}: D935's store is a function of the GCP project alone — the convention the runs bucket and both state buckets follow — so a name a caller could choose is a name that could be pointed at another installation's store", mcpauthStoreTF)
	}
	if strings.Contains(code, "fmgd-") {
		t.Errorf("%s: the store's file names the managed prefix fmgd-: the managed deployer's conditioned storage role is a project-level binding on that prefix (deployer.tf:379, :142) and every operator can act as the deployer (:501), so a store named under it would be readable and rewritable by both (the correction of 2026-11-05)", mcpauthStoreTF)
	}
}

// TestTheStoreBucketCarriesNoConvenienceBinding is the half of the store's
// floor this task can narrow, and the reason it is a test rather than a
// sentence.
//
// Four classes reach a bucket this installation has just made, and they are not
// one (M4's floor bullet): the project's Owners and Editors through their
// project-level roles, which no bucket policy narrows and this task cannot take
// away; anyone else holding a project-level storage role, for the same reason;
// a basic project Viewer through the convenience bindings GCS attaches to every
// bucket it creates (projectOwner:/projectEditor: as
// roles/storage.legacyBucketOwner and projectViewer: as the legacy reader pair,
// internal/gcpfake/gcs.go:266); and the one member this task grants. The third
// is the task's to close, and what this test decides is that the plan writes
// none of it — no legacy role, no convenience member, no authoritative policy
// resource that would own the whole binding list, and no second member.
//
// What it cannot see, and what the plan says so about: the bindings GCS itself
// attaches at creation are not in the plan, so no plan-level test can read them
// off (implementation-plan.md §6: "a plan-level test reads the plan, and the
// plan is not the project's policy"). Taking those off is the repository's
// existing storage-IAM call — infra.RemoveProjectViewers, which fugaro setup
// and fugaro update already run on the state and runs buckets — and its call
// site for this bucket is the release that turns the front door on, because
// nothing in this tree creates the store yet. Check 8's "a basic project Viewer
// is refused" is the reading that proves it happened, and it is owner-run.
//
// The mutations this fails on: a `projectViewer:` member or a legacy role
// written onto the store's bucket (which is the shape a convenience binding
// left on it takes in Terraform), and a second member beside the front door's
// account.
func TestTheStoreBucketCarriesNoConvenienceBinding(t *testing.T) {
	files := tfFiles(t)
	// The convenience bindings, as Terraform would write them: the four legacy
	// roles GCS gives a new bucket, and the three project-level member prefixes
	// they are granted to. Nothing in this tree writes any of them today, which
	// is what makes the assertion cheap and total.
	for _, bad := range []string{
		"roles/storage.legacyBucketOwner", "roles/storage.legacyBucketReader",
		"roles/storage.legacyObjectOwner", "roles/storage.legacyObjectReader",
		"projectOwner:", "projectEditor:", "projectViewer:",
	} {
		if named := tfFilesNaming(t, files, bad); len(named) > 0 {
			t.Errorf("%v write %q: a bucket GCS creates answers to its convenience bindings until they are taken off it, and a basic project Viewer reads the store through them (internal/gcpfake/gcs.go:266, docs/design/bucket-iam.md:47). This task's store names one member and no other, so no file of this module may write one of these (D935)", named, bad)
		}
	}
	// And the other route to a member list: an authoritative resource, which
	// would own the bucket's whole policy rather than add one binding to it.
	// deploy/terraform/terraform_test.go refuses every such type tree-wide
	// (TestNoAuthoritativeIAM); this says the same of the store's own file, so
	// the store cannot be the exception that arrives by accident.
	for _, bad := range []string{"google_storage_bucket_iam_policy", "google_storage_bucket_iam_binding"} {
		if named := tfFilesNaming(t, files, bad); len(named) > 0 {
			t.Errorf("%v write %s: an authoritative IAM resource owns the bucket's whole policy — the members GCS attached included — and this repository grants on shared things additively and never that way (TestNoAuthoritativeIAM):\n%s", named, bad, files[mcpauthStoreTF])
		}
	}
	// One member, so a second cannot be added beside it unnoticed: the store's
	// bucket is the target of exactly one IAM resource in this module.
	code := files[mcpauthStoreTF]
	if got := strings.Count(code, "resource \"google_storage_bucket_iam_"); got != 1 {
		t.Errorf("%s declares %d bucket IAM resources, want exactly 1 (the front door's one objectAdmin binding): D935's whole content is that the store's IAM names one account, and a second resource here is a second member", mcpauthStoreTF, got)
	}
}

// TestTheStoreBucketIsCreatedAndNeverAdopted is the squatting half of D935.
//
// `fugaro-mcpauth-<gcp-project>` is a function of the project alone — the shape
// the runs bucket and both state buckets have — which makes it predictable, and
// bucket names are global: anyone on the internet can hold that name in another
// project, and the deployer's own `storage.buckets.create` is unconditioned
// (deployer.tf:219), so nothing in a managed plan can be relied on to have
// taken it either. Terraform is the answer and it is a fail-closed one: the
// bucket is created by `fugaro update` running with the owner's own rights, and
// a create against a name that already exists fails the apply rather than
// importing it. So what this test decides is that the plan is a create — no
// `import` block, no data source, no "use what is there" — and that the bucket
// is a plain `google_storage_bucket` with the two settings the installer's plan
// review refuses a bucket without.
//
// The mutations this fails on: an import block or a data source for the store,
// a name taken from a variable rather than derived from the project, a bucket
// without uniform access or without public access prevention enforced
// (internal/infra/tf/cover.go refuses both), and force_destroy, which would
// take the token records and the revocation state with the bucket.
func TestTheStoreBucketIsCreatedAndNeverAdopted(t *testing.T) {
	files := tfFiles(t)
	code, ok := files[mcpauthStoreTF]
	if !ok {
		t.Fatalf("this module has no %s: D935's store is created by it and by nothing else", mcpauthStoreTF)
	}
	bucket := tfResource(t, code, "google_storage_bucket", "mcpauth_store")
	for _, want := range []struct{ what, re string }{
		{"the front door's opt-in", `(?m)^\s*count\s*=\s*local\.mcp_server_on\b`},
		{"uniform bucket-level access", `(?m)^\s*uniform_bucket_level_access\s*=\s*true\s*$`},
		{"public access prevention enforced", `(?m)^\s*public_access_prevention\s*=\s*"enforced"\s*$`},
		{"force_destroy = false", `(?m)^\s*force_destroy\s*=\s*false\s*$`},
		{"the derived name", `(?m)^\s*name\s*=\s*local\.mcpauth_store_bucket\s*$`},
	} {
		if !regexp.MustCompile(want.re).MatchString(bucket) {
			t.Errorf("%s: the store's bucket sets no %s: the installer's plan review refuses a bucket without uniform access and public access prevention (internal/infra/tf/cover.go), the opt-in is what creates it at all (D934), and force_destroy would take the token records with the bucket:\n%s", mcpauthStoreTF, want.what, bucket)
		}
	}
	// No adoption of a name this installation did not create: no import block,
	// and no data source read the store out of a bucket that is already there.
	for name, src := range files {
		for _, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "import ") || strings.TrimSpace(line) == "import {" {
				t.Errorf("%s carries an import block (%q): the store's name is predictable and global, and a bucket this installation did not create is never adopted — a create against a name that already exists fails the apply (D935, 2026-11-05)", name, strings.TrimSpace(line))
			}
		}
		if strings.Contains(src, `data "google_storage_bucket"`) {
			t.Errorf("%s reads a bucket with a data source: M2 opens only the name this module writes into the installation's outputs, and a data source would read the store out of a bucket somebody else holds (D935, 2026-11-05)", name)
		}
	}
}
