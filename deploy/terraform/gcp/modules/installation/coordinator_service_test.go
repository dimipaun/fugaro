// The coordinator's front door, held at the plan's own level: it is a Cloud
// Run SERVICE the module creates for an opt-in of its own, reachable by
// everyone (D920) and running as the coordinator's own account, and it must
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
	"strings"
	"testing"
)

const (
	coordinatorTF = "coordinator.tf"
	variablesTF   = "variables.tf"
)

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
	return stripHCLComments(string(b))
}

// stripHCLComments drops each line's trailing # comment, and only outside a
// quoted string — the module's own Terraform carries URLs whose // would be a
// comment to a naive stripper, and a mangled line is a check that passes for
// the wrong reason.
func stripHCLComments(src string) string {
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
// reach" false the day it is turned on. What this account holds is one grant —
// the runs bucket's read (D932) — and nothing else in this tree or the Firebase
// root grants it anything.
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
			t.Errorf("%s: the front door's account's one grant sets no %s (D932: the tools behind the port answer with a run's recorded artefacts, and that read is the account's whole reach):\n%s", coordinatorTF, want.what, grant)
		}
	}
	if strings.Contains(code, "roles/firebasedatabase.admin") {
		t.Errorf("%s: the front door's file grants or names the FP's database admin: that is the sweep account's role, granted by the Firebase root, and what a front door may read in the coordinator's own store is D935 — an owner's answer this plan does not have, so no binding on one is planned here", coordinatorTF)
	}
	// Nobody may act as it: whoever could deploy a revision as it would serve
	// the front door's answers as the installation's own identity.
	if strings.Contains(code, "roles/iam.serviceAccountUser") {
		t.Errorf("%s: nobody is granted actAs on the front door's account, operators included: a deployer as it would serve every run artefact behind the port as this identity", coordinatorTF)
	}
}
