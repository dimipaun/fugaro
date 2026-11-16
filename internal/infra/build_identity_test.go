//go:build terraform

package infra

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// buildIdentityPlan is the installation fixture planned for real with the
// credentials service ON, read as the four facts these tests are about: the
// plan's rows by address, the credentials service's own container environment,
// the project and the identity's address.
//
// The fixture is credentialsLC — the installation internal/infra/tf's
// installation-credentials golden is made from, in this package's spelling —
// and the plan is a real `terraform plan` of the embedded tree (no Google API
// is read: a fresh create plan of the installation root touches none), which
// is what makes the account, the grant and the environment below the module's
// own and not a second spelling of it in Go.
type buildIdentityPlan struct {
	rows    map[string]plannedResource
	env     map[string]string
	project string
	// address is the identity's email — what a Cloud Build request names a
	// build's account by — and member is the same address in the IAM spelling
	// a grant's own field carries.
	address string
	member  string
}

func newBuildIdentityPlan(t *testing.T) buildIdentityPlan {
	t.Helper()
	lc := credentialsLC(t)
	spec, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The service itself, built the way the credentials stage builds it (the
	// digest-pinned image and the job table the config derives), because the
	// build identity and its invoker grant are gated on the service being on:
	// an installation that runs no catalog build has neither.
	svc, err := Credentials(lc, credsVarsImage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	spec.CredentialsService = &svc
	rows := installationPlanFor(t, spec)
	svcRow, ok := rows["module.installation.google_cloud_run_v2_service.credentials[0]"]
	if !ok {
		t.Fatalf("the plan creates no credentials service: the fixture is not the installation this test is about, and every fact below hangs off it")
	}
	address := BuildIdentityEmail(lc.GCPProject)
	return buildIdentityPlan{
		rows:    rows,
		env:     credentialsServiceEnv(t, svcRow),
		project: lc.GCPProject,
		address: address,
		member:  "serviceAccount:" + address,
	}
}

// credentialsServiceEnv is the credentials service's container environment as
// the plan writes it: variable name to literal value. A name whose value comes
// from a source instead is an entry no class may be read out of, which is why
// this refuses rather than ignoring it.
func credentialsServiceEnv(t *testing.T, svc plannedResource) map[string]string {
	t.Helper()
	tpl, _ := svc.After["template"].([]any)
	if len(tpl) != 1 {
		t.Fatalf("%s has %d templates, want one", svc.Address, len(tpl))
	}
	tmpl, _ := tpl[0].(map[string]any)
	cs, _ := tmpl["containers"].([]any)
	if len(cs) != 1 {
		t.Fatalf("%s has %d containers, want one", svc.Address, len(cs))
	}
	c, _ := cs[0].(map[string]any)
	entries, _ := c["env"].([]any)
	env := map[string]string{}
	for _, e := range entries {
		m, _ := e.(map[string]any)
		name, _ := m["name"].(string)
		if name == "" {
			t.Fatalf("%s sets an environment variable with no name: %v", svc.Address, m)
		}
		// The provider renders every entry with a value_source key, empty for
		// a literal: one that is not empty is an entry whose value is
		// computed elsewhere, and no class is read out of one.
		if src, _ := m["value_source"].([]any); len(src) > 0 {
			t.Fatalf("%s takes %s from a source rather than a literal: no class is read out of an entry whose value is computed elsewhere", svc.Address, name)
		}
		value, _ := m["value"].(string)
		env[name] = value
	}
	return env
}

// A project has ONE build identity (B5, D2301): the plan of an installation
// that runs the credentials service creates exactly one service account of the
// project's own for catalog builds — the account BuildIdentityAccountID names,
// whose address is the one Go computes — and nothing else in that plan is a
// second identity for the same project.
//
// The red step of this task is the first assertion: before B5 the plan holds
// no project-level build account at all, every catalog build being run as its
// repository's own account (gcp.BuildServiceAccountID), which M2 destroys with
// the repository's root.
//
// The last assertion is the task's other half — "the account holds no write
// role on any repository registry but the project's own" — read against the
// plan rather than against the module's text: the ONE change naming the
// identity is its invoker grant on the credentials service. The registry reach
// a catalog build needs to push is B6's, and it is added to the same file; a
// grant that reached a registry here would be a widening this task's review
// did not announce.
func TestProjectHasOneBuildIdentity(t *testing.T) {
	p := newBuildIdentityPlan(t)
	const addr = "module.installation.google_service_account.build_identity[0]"
	sa, ok := p.rows[addr]
	if !ok {
		t.Fatalf("the plan creates no project-level build account (%s is not in it), so a catalog build of a shared repository — one whose own root is gone — has no account to run as: the project's catalog builds would run as nothing, or as another project's account", addr)
	}
	if got, _ := sa.After["account_id"].(string); got != BuildIdentityAccountID {
		t.Errorf("%s is the account %q, want the literal this package computes an address from (%s)", addr, got, BuildIdentityAccountID)
	}
	// The address is a fact of the configuration and not of the apply: the
	// same value this package derives is the one the plan creates. B6 hands
	// this address to CatalogBuildSpec, so a module that created the account
	// under another name would be a build submitted as an account nobody
	// granted anything to.
	if got, _ := sa.After["email"].(string); got != p.address {
		t.Errorf("%s is %q, want BuildIdentityEmail(%s) = %q: the address has to be computable before the apply", addr, got, p.project, p.address)
	}

	var accounts, grants []string
	for a, row := range p.rows {
		if got, _ := row.After["account_id"].(string); row.Type == "google_service_account" && got == BuildIdentityAccountID {
			accounts = append(accounts, a)
		}
		if !strings.Contains(row.json(), p.address) {
			continue
		}
		switch {
		case a == addr, strings.Contains(row.json(), `"name":"FUGARO_BUILD_ACCOUNTS"`):
			// The account itself, and the environment entry the service is
			// handed its address in.
		case strings.HasSuffix(row.Type, "_iam_member"):
			grants = append(grants, a)
		default:
			t.Errorf("%s names the build identity %s and is neither the account, its grant nor the service's own environment: %v", a, p.address, row.After)
		}
	}
	if len(accounts) != 1 {
		t.Errorf("the plan creates %v as the project's build identity, want exactly one account (%s): a second one is a second identity for the same catalog", accounts, BuildIdentityAccountID)
	}
	if want := []string{"module.installation.google_cloud_run_v2_service_iam_member.credentials_build_identity[0]"}; !slices.Equal(grants, want) {
		t.Errorf("the plan grants the build identity %v, want exactly %v: its reach is the credentials service's invoker bit and nothing else in this task, so a registry role here would be a widening nobody announced", grants, want)
	}
}

// The identity's one grant is the invoker bit on the credentials service
// (B5): without it Cloud Run refuses the build's call at IAM before the
// container can decide the caller's class, which is the verifier's own fact
// (CSR14) against the same service.
//
// The grant is read whole — the role, the member, the service it is on, the
// project, the location and the absence of a condition — because "invoker on
// the credentials service" is the whole of what the identity holds: a member
// that is another address, a role that is not the invoker bit, or a condition
// that fences the grant to a resource the next plan does not carry would each
// be a different fact with the same name.
func TestBuildIdentityIsInvokerOnTheService(t *testing.T) {
	p := newBuildIdentityPlan(t)
	const addr = "module.installation.google_cloud_run_v2_service_iam_member.credentials_build_identity[0]"
	g, ok := p.rows[addr]
	if !ok {
		t.Fatalf("the plan grants no invoker on the credentials service: %s is not in it, so the build identity cannot reach the route that mints a build's credential", addr)
	}
	if g.Type != "google_cloud_run_v2_service_iam_member" {
		t.Fatalf("%s is a %s, want the service-level IAM member", addr, g.Type)
	}
	if got, _ := g.After["role"].(string); got != "roles/run.invoker" {
		t.Errorf("%s grants %q, want roles/run.invoker: the identity's whole reach is the invoker bit on this one service", addr, got)
	}
	if got, _ := g.After["member"].(string); got != p.member {
		t.Errorf("%s grants to %q, want %q — the address of the account this same plan creates", addr, got, p.member)
	}
	if got, _ := g.After["name"].(string); got != CredentialsServiceName {
		t.Errorf("%s grants the invoker role on service %q, want %q: the identity must invoke the credentials service and no other", addr, got, CredentialsServiceName)
	}
	if got, _ := g.After["project"].(string); got != p.project {
		t.Errorf("%s grants in project %q, want %q", addr, got, p.project)
	}
	// A block the module does not set still renders as a key here, empty: an
	// entry that is NOT empty is the condition this grant must not carry.
	if conds, _ := g.After["condition"].([]any); len(conds) > 0 {
		t.Errorf("%s carries a condition: the service's own ID-token check is what decides who is served (Q6), and the grant stays unconditional as every invoker grant of this service is", addr)
	}
	// The service's own half: the invoker check is ON, so the grant above is
	// what admits the identity rather than a decoration of a service that
	// admits everyone.
	svc := p.rows["module.installation.google_cloud_run_v2_service.credentials[0]"]
	if off, _ := svc.After["invoker_iam_disabled"].(bool); off {
		t.Errorf("%s disables the invoker IAM check, so every caller reaches the build route at IAM and the grant above decides nothing", svc.Address)
	}
}

// The identity is the service's build class (B1, B5): the environment entry
// the module writes carries exactly its address, and the class is one list of
// one address because a catalog build runs as the PROJECT's identity and not
// as a job's.
//
// The entry's shape is the process's too — a JSON array, which internal/cli's
// credentialsServiceBuildAccounts decodes and refuses anything else in — so
// this test reads it back the way that reader does rather than as a substring.
func TestBuildIdentityIsRenderedIntoTheServicesEnvironment(t *testing.T) {
	p := newBuildIdentityPlan(t)
	raw, ok := p.env["FUGARO_BUILD_ACCOUNTS"]
	if !ok {
		t.Fatalf("the credentials service's environment carries no FUGARO_BUILD_ACCOUNTS (%v): the build route then admits nobody, and a catalog build of a shared repository is refused with every other caller", p.env)
	}
	var got []string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("FUGARO_BUILD_ACCOUNTS is not a JSON array of addresses (%q): the process refuses to start on one it cannot decode", raw)
	}
	if want := []string{p.address}; !slices.Equal(got, want) {
		t.Errorf("FUGARO_BUILD_ACCOUNTS is %v, want %v: the class is the account this plan creates, and one per project — the installation's per-repository job accounts are FUGARO_JOB_ACCOUNTS' and are not a build identity", got, want)
	}
}
