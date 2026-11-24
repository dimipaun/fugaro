//go:build terraform

package infra

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// buildIdentityPlan is the installation fixture planned for real with the
// credentials service ON, read as the four facts these tests are about: the
// plan's rows by address, the credentials service's own container environment,
// the project, the identity's address and the installation's operator roster.
//
// The fixture is credentialsLC with the operator roster
// buildIdentityOperatorsLC — the installation internal/infra/tf's
// installation-credentials golden is made from, in this package's spelling —
// and the plan is a real `terraform plan` of the embedded tree (no Google API
// is read: a fresh create plan of the installation root touches none), which
// is what makes the account, the grants and the environment below the module's
// own and not a second spelling of it in Go.
type buildIdentityPlan struct {
	rows    map[string]plannedResource
	env     map[string]string
	project string
	// address is the identity's email — what a Cloud Build request names a
	// build's account by — and member is the same address in the IAM spelling
	// a grant's own field carries. operators are the installation's operator
	// members, the principals the module hands actAs on the identity.
	address   string
	member    string
	operators []string
}

// buildIdentityOperatorsLC is the operator roster the fixture adds, in the
// shape a real installation's document carries it: the terraform block's
// `admins` list is what internal/infra's spec builder fills var.operators from
// (internal/localcfg's Terraform comment — a version-1 document's `operators`
// key is this list under its old name), and an operator is who submits a
// manual catalog build. The golden tfvars fixture names the same person
// (internal/infra/testdata/installation-credentials.tfvars.json), which is the
// plan testdata/golden/installation-credentials.plan.json holds.
const buildIdentityOperatorsLC = `terraform: {admins: ["user:person-1@example.com"]}
`

func newBuildIdentityPlan(t *testing.T) buildIdentityPlan {
	t.Helper()
	lc := parseLC(t, m4LocalConfig+m5Additions+credentialsBudgetLC+buildIdentityOperatorsLC)
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
		rows:      rows,
		env:       credentialsServiceEnv(t, svcRow),
		project:   lc.GCPProject,
		address:   address,
		member:    "serviceAccount:" + address,
		operators: spec.Operators,
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
// plan rather than against the module's text: the resources naming the
// identity as a member are its invoker grant on the credentials service, B6's
// writer and tag mover on the ONE shared registry, the one project-level role
// its own step logs need, and the operators' actAs on it (which names the
// account in service_account_id rather than as its member), which is the
// whole of what a catalog build is handed. A further one would be a widening
// neither task's review announced.
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
	slices.Sort(grants)
	want := []string{
		"module.installation.google_artifact_registry_repository_iam_member.build_identity_base_reader[0]",
		"module.installation.google_artifact_registry_repository_iam_member.build_identity_ghcr_reader[0]",
		"module.installation.google_artifact_registry_repository_iam_member.build_identity_shared_tag_mover[0]",
		"module.installation.google_artifact_registry_repository_iam_member.build_identity_shared_writer[0]",
		"module.installation.google_cloud_run_v2_service_iam_member.credentials_build_identity[0]",
		"module.installation.google_project_iam_member.build_identity_log_writer[0]",
	}
	for _, op := range p.operators {
		want = append(want, "module.installation.google_service_account_iam_member.operator_build_identity[\""+op+"\"]")
	}
	slices.Sort(want)
	if !slices.Equal(grants, want) {
		t.Errorf("the plan grants the build identity %v, want exactly %v: its reach is the credentials service's invoker bit, the shared registry's writer and tag mover, the reader on the two registries a build's own steps pull from, the one project-level role its own step logs need and the operators' actAs on it, so a further role here would be a widening nobody announced", grants, want)
	}
}

// The identity's one grant is the invoker bit on the credentials service
// (B5): without it Cloud Run refuses the build's call at IAM before the
// container can decide the caller's class, which is the verifier's own fact
// (CSR14) against the same service.
//
// The grant is read whole — the role, the member, the service it is on, the
// project, the location and the absence of a condition — because "invoker on
// the credentials service" is the whole of what this row grants: a member
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
		t.Errorf("%s grants %q, want roles/run.invoker: this row is the invoker bit on this one service and nothing else", addr, got)
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

// catalogCheckIdentityPlan is the installation that both runs the credentials
// service and publishes a catalog — the only shape in which the catalog
// check's actAs on the project's build identity exists (the account and the
// grant are gated on the service, the job on catalog_check) — planned for
// real: the plan's rows by address, the project, and the two accounts' own
// spellings.
type catalogCheckIdentityPlan struct {
	rows    map[string]plannedResource
	project string
	// identity and check are the two accounts' addresses; the *Member fields
	// are the same addresses in the IAM spelling a grant's own field carries.
	identity, identityMember string
	check, checkMember       string
	// runsBucket is the installation's bucket and repos the catalog check's
	// own repositories — the slug, the records condition and the provider
	// credential each of them carries — which is the input the identity's
	// bucket and secret grants are rendered from.
	runsBucket string
	repos      []CatalogCheckRepository
}

func newCatalogCheckIdentityPlan(t *testing.T) catalogCheckIdentityPlan {
	t.Helper()
	// catalogCheckInstallLC is the catalog-check fixture and credentialsBudgetLC
	// is what makes Credentials write the service's key (the Firebase project
	// and the two facts it refuses the environment without), so the planned
	// installation is the one the acceptance is about: a project that publishes
	// a catalog AND runs the build route.
	lc := parseLC(t, catalogCheckInstallLC(false)+credentialsBudgetLC)
	spec, err := Installation(lc, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := Credentials(lc, credsVarsImage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	spec.CredentialsService = &svc
	if spec.CatalogCheck == nil {
		t.Fatal("the fixture plans no catalog check job (D1148), so the actAs below has no subject")
	}
	rows := installationPlanFor(t, spec)
	checkRow, ok := rows["module.installation.google_service_account.catalog_check[0]"]
	if !ok {
		t.Fatalf("the plan creates no catalog check account, so the fixture is not the installation these tests are about")
	}
	check, _ := checkRow.After["email"].(string)
	if check == "" {
		t.Fatalf("%s has no email: %v", checkRow.Address, checkRow.After)
	}
	identity := BuildIdentityEmail(lc.GCPProject)
	return catalogCheckIdentityPlan{
		rows:           rows,
		project:        lc.GCPProject,
		identity:       identity,
		identityMember: "serviceAccount:" + identity,
		check:          check,
		checkMember:    "serviceAccount:" + check,
		runsBucket:     lc.RunsBucket,
		repos:          spec.CatalogCheck.Repositories,
	}
}

// The catalog check's actAs on the build identity (B6, D2301): the job's
// account may mint the project's build identity's token and submit a build
// naming it, which is what makes every rebuild the nightly check fires run as
// the project rather than as the repository the entry belongs to. It is the
// repository root's own grant one account over (modules/repo/catalogcheck.tf,
// which holds the same role on that repository's build account until M2).
//
// Read whole — the role, the member, the account acted as, and the absence of
// a condition — because "actAs on the identity" is the whole of what the grant
// is: a member that is another account, a role that is not the actAs bit, or a
// condition fencing it to one entry's build would each be a different fact
// with the same name.
func TestCatalogCheckActsAsTheBuildIdentity(t *testing.T) {
	p := newCatalogCheckIdentityPlan(t)
	const addr = "module.installation.google_service_account_iam_member.catalog_check_build_identity[0]"
	g, ok := p.rows[addr]
	if !ok {
		t.Fatalf("the plan carries no %s: the job submits every rebuild as an account it may not act as, and Cloud Build refuses each one", addr)
	}
	if g.Type != "google_service_account_iam_member" {
		t.Fatalf("%s is a %s, want the service-account IAM member", addr, g.Type)
	}
	if got, _ := g.After["role"].(string); got != "roles/iam.serviceAccountUser" {
		t.Errorf("%s grants %q, want roles/iam.serviceAccountUser: the actAs bit on the ONE account, which is the whole of what submission as it needs", addr, got)
	}
	if got, _ := g.After["member"].(string); got != p.checkMember {
		t.Errorf("%s grants to %q, want %q — the catalog check account this same plan creates", addr, got, p.checkMember)
	}
	if got, _ := g.After["service_account_id"].(string); got != "projects/"+p.project+"/serviceAccounts/"+p.identity {
		t.Errorf("%s acts as %q, want the literal path of %s in %s: a repository's build account here would be the account M2 retires with the repository's root, and a build of an entry whose repository is gone would run as nobody", addr, got, p.identity, p.project)
	}
	if conds, _ := g.After["condition"].([]any); len(conds) > 0 {
		t.Errorf("%s carries a condition (%v): every entry of every repository is rebuilt as the same project account, and one condition per entry would be one grant per entry", addr, conds)
	}
	// The account acted as is the plan's own: a grant naming an address the same
	// plan does not create would be an actAs on another installation's identity.
	if _, ok := p.rows["module.installation.google_service_account.build_identity[0]"]; !ok {
		t.Error("the plan grants actAs on the build identity and creates none: the fixture is not the installation B6 is about")
	}
}

// The operators' actAs on the identity (B6, D2301): `fugaro image build
// <entry>` submits its build AS the project's identity
// (internal/cli/image_build_catalog.go names BuildIdentityEmail), and Cloud
// Build refuses a caller that holds no iam.serviceAccounts.actAs on the
// account the build runs as — so without this grant every manual catalog
// build fails with PERMISSION_DENIED at CreateBuild and the nightly check's
// rebuilds are the only ones that can run. It is the repository root's own
// grant one account over (modules/repo/build.tf's operator_build, which holds
// the same role on that repository's build account until M2 retires the root).
//
// Read whole — the role, the account acted as, the absence of a condition —
// and read as a SET, one instance per operator and no member the installation
// does not list: an actAs handed to one further principal is a person who may
// run a build as the project.
func TestOperatorsActAsTheBuildIdentity(t *testing.T) {
	p := newBuildIdentityPlan(t)
	if len(p.operators) == 0 {
		t.Fatal("the fixture lists no operators, so the grant below has no subject: the installation B6 is about is one an operator submits a catalog build in")
	}
	const prefix = "module.installation.google_service_account_iam_member.operator_build_identity["
	wantAccount := "projects/" + p.project + "/serviceAccounts/" + p.address
	var members []string
	for addr, row := range p.rows {
		if !strings.HasPrefix(addr, prefix) {
			continue
		}
		if row.Type != "google_service_account_iam_member" {
			t.Errorf("%s is a %s, want the service-account IAM member: actAs is what Cloud Build checks for the account a build names", addr, row.Type)
			continue
		}
		if got, _ := row.After["role"].(string); got != "roles/iam.serviceAccountUser" {
			t.Errorf("%s grants %q, want roles/iam.serviceAccountUser: the actAs bit on the ONE account a catalog build runs as", addr, got)
		}
		if got, _ := row.After["service_account_id"].(string); got != wantAccount {
			t.Errorf("%s acts as %q, want the literal path %q: a computed account (or a repository's own build account, which M2 destroys with its root) is one the cover refuses to read and a build of a retired repository could not run as", addr, got, wantAccount)
		}
		if conds, _ := row.After["condition"].([]any); len(conds) > 0 {
			t.Errorf("%s carries a condition (%v): every operator submits as the same project account, and the role is the whole of what admission needs", addr, conds)
		}
		member, _ := row.After["member"].(string)
		members = append(members, member)
	}
	slices.Sort(members)
	want := slices.Clone(p.operators)
	slices.Sort(want)
	if !slices.Equal(members, want) {
		t.Errorf("the plan hands actAs on the build identity to %v, want exactly the installation's operators %v: one more member is a principal that may run a catalog build as the project, and one fewer is an operator whose own `fugaro image build` is refused", members, want)
	}
}

// The grant survives M2's retirement of every repository root, which is the
// whole reason B6 moved it: the account is the installation's and the grant is
// in the installation module, so the root that is destroyed carries neither.
//
// The plan's own address is the first half (module.installation...); this is
// the second, read over the tree the root is made of: NOTHING under a
// repository root, or under the module it embeds, may name the identity — a
// grant, a variable or a member naming it there would be a fact that dies with
// the root while the nightly check keeps firing builds.
func TestCatalogCheckGrantSurvivesRetiringARepoRoot(t *testing.T) {
	p := newCatalogCheckIdentityPlan(t)
	const addr = "module.installation.google_service_account_iam_member.catalog_check_build_identity[0]"
	if _, ok := p.rows[addr]; !ok {
		t.Fatalf("the plan carries no %s, so there is no installation-side grant to survive anything: the actAs is in a repository root and dies with it", addr)
	}
	for _, dir := range []string{
		"../../deploy/terraform/gcp/roots/repo",
		"../../deploy/terraform/gcp/modules/repo",
	} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".tf") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), BuildIdentityAccountID) {
				t.Errorf("%s names %s: the repository root (and the module it embeds) is retired with M2, and whatever names the project's build identity there is destroyed with it — the actAs belongs to the installation module, which outlives every repository", path, BuildIdentityAccountID)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The identity's whole reach (B6's "nothing wider"), read off the plan: every
// resource that names it as a MEMBER is a grant TO it, and each one is the
// per-repository build account's own reach for one step of a catalog build —
// the invoker bit on the credentials service; the writer and tag mover on the
// installation's ONE shared registry, which are the two roles the
// per-repository account held there (modules/repo/registry.tf) and the two a
// catalog push needs; the READER on the two registries a build pulls its own
// steps from: fugaro-base, whose base images the generated credential step
// runs, and the ghcr remote, through which the derived build's step image and
// its FROM arrive (modules/repo/build.tf's build_base and build_ghcr before
// this task); the definitions read, the records write and the provider
// credential of one repository each, which the build's own pin, record and
// clone steps make as the account it runs as (a code review of this diff
// found those three missing: a catalog build whose identity holds none of the
// runs bucket or Secret Manager fails on the first of them it reaches); and
// the ONE project-level role it holds, roles/logging.logWriter, without which
// a build that names a user-specified service account is refused or loses
// every step log (the per-repository build account held it too).
// Reader is the whole of it on the two registries: neither is written by a
// build, and a write role for this identity on a repository's own registry
// would be the per-repository account's reach, which this task deliberately
// does not carry over.
//
// What is NOT enumerated here is the actAs other principals hold ON the
// identity — the catalog check's and the operators' — which names it in
// service_account_id rather than as the grant's member: those are grants of
// somebody else's reach, and TestCatalogCheckActsAsTheBuildIdentity and
// TestOperatorsActAsTheBuildIdentity read them.
func TestBuildIdentityHoldsOnlyTheRegistryRoles(t *testing.T) {
	p := newCatalogCheckIdentityPlan(t)
	shared, ok := p.rows["module.installation.google_artifact_registry_repository.shared"]
	if !ok {
		t.Fatal("the plan creates no shared registry, so the two roles below have no target: the fixture is not the installation B6 is about")
	}
	registry, _ := shared.After["repository_id"].(string)
	if registry == "" {
		t.Fatalf("%s has no repository_id: %v", shared.Address, shared.After)
	}
	const (
		writer     = "module.installation.google_artifact_registry_repository_iam_member.build_identity_shared_writer[0]"
		tagMover   = "module.installation.google_artifact_registry_repository_iam_member.build_identity_shared_tag_mover[0]"
		invoker    = "module.installation.google_cloud_run_v2_service_iam_member.credentials_build_identity[0]"
		baseReader = "module.installation.google_artifact_registry_repository_iam_member.build_identity_base_reader[0]"
		ghcrReader = "module.installation.google_artifact_registry_repository_iam_member.build_identity_ghcr_reader[0]"
		defsRead   = "module.installation.google_storage_bucket_iam_member.build_identity_definitions[0]"
		logWriter  = "module.installation.google_project_iam_member.build_identity_log_writer[0]"
	)
	recordsAddr := func(slug string) string {
		return "module.installation.google_storage_bucket_iam_member.build_identity_records[\"" + slug + "\"]"
	}
	got := map[string]string{}
	for addr, row := range p.rows {
		// The account's own row carries the same member (the provider renders
		// it), and it is not a grant: what is enumerated here is every kind of
		// IAM resource the plan holds naming the identity as its holder.
		if !strings.HasSuffix(row.Type, "_iam_member") {
			continue
		}
		if member, _ := row.After["member"].(string); member != p.identityMember {
			continue
		}
		role, _ := row.After["role"].(string)
		got[addr] = role
	}
	want := map[string]string{
		invoker:    "roles/run.invoker",
		writer:     "roles/artifactregistry.writer",
		tagMover:   "projects/" + p.project + "/roles/" + RoleTagMover,
		baseReader: "roles/artifactregistry.reader",
		ghcrReader: "roles/artifactregistry.reader",
		defsRead:   "roles/storage.objectViewer",
		logWriter:  "roles/logging.logWriter",
	}
	for _, r := range p.repos {
		want[recordsAddr(r.Slug)] = "roles/storage.objectUser"
	}
	if !maps.Equal(got, want) {
		t.Errorf("the plan grants the build identity %v, want exactly %v: the writer and the tag mover are the two roles the per-repository build account held on the shared registry, the invoker bit is the build route's class, the two readers are the registries a build's own steps pull from, the definitions read and the records write are what a catalog build's own steps make as the account it runs as, and logging.logWriter is the one project-level role its own step logs need — a further grant here would be a reach this task did not carry over, and a secretAccessor would be the mount this task took away (IS8, D3330)", got, want)
	}
	for _, addr := range []string{writer, tagMover} {
		row, ok := p.rows[addr]
		if !ok {
			t.Fatalf("the plan carries no %s, so a catalog build of an entry pushes nothing", addr)
		}
		if r, _ := row.After["repository"].(string); r != registry {
			t.Errorf("%s grants on repository %q, want the shared registry %q: every catalog image lands there (D1152), and a write role on a repository's own registry, on the base image registry or on the ghcr remote would be the per-repository account's reach and not this identity's", addr, r, registry)
		}
		if r, _ := row.After["project"].(string); r != p.project {
			t.Errorf("%s grants in project %q, want %q", addr, r, p.project)
		}
	}
	// The two readers, each on the registry it exists for: a build pulls the
	// base images it runs as and the FROM it builds on from exactly these two,
	// and a reader that named the shared registry would be the write's own
	// read rather than the pulls a build makes before it pushes anything.
	for addr, wantRepo := range map[string]string{
		baseReader: "module.installation.google_artifact_registry_repository.base",
		ghcrReader: "module.installation.google_artifact_registry_repository.ghcr",
	} {
		row, ok := p.rows[addr]
		if !ok {
			t.Fatalf("the plan carries no %s, so the first step of a catalog build starts as an account that may not pull the image it runs", addr)
		}
		target, ok := p.rows[wantRepo]
		if !ok {
			t.Fatalf("the plan creates no %s, so %s has no target", wantRepo, addr)
		}
		wantID, _ := target.After["repository_id"].(string)
		if r, _ := row.After["repository"].(string); wantID == "" || r != wantID {
			t.Errorf("%s grants on repository %q, want %q (%s's own repository_id)", addr, r, wantID, wantRepo)
		}
		if r, _ := row.After["project"].(string); r != p.project {
			t.Errorf("%s grants in project %q, want %q", addr, r, p.project)
		}
	}
}

// The identity's reach INSIDE the runs bucket, which is what a catalog build's
// own steps use and what a build that ran as the per-repository account used
// to have: the definition it describes and the record it writes back. The
// third thing that account held — the provider credential it cloned with — is
// gone from the identity entirely (IS8, D3330): the build fetches it from the
// credentials service, so the last block below reads the plan for a Secret
// Manager accessor and requires none. A code review of
// B6's first cut found this missing — the identity held the registry roles and
// nothing else, so every catalog build and every nightly rebuild failed on the
// first of these steps it reached — and this test is the shape of the fix.
//
// Read whole, because each half is a different kind of narrowing: the bucket,
// the role, the member, and the condition's title AND expression. The
// expression is the one internal/backend/gcp builds for that grant's own
// target — the copied definitions for the read, one repository's
// builds/<slug>/catalog/ for each record — which is what keeps the identity
// out of every other repository's records, out of the repositories' daily
// check state beside them, and out of writing the definitions at all: this is
// the grant modules/repo/catalogcheck.tf makes to the check job, made to the
// account that now writes the record instead. The title is the one gcp builds
// for THIS account and not the check job's, because the input carries the
// job's title and a grant is read by whose it is.
func TestBuildIdentityBucketGrantsAreTheConditions(t *testing.T) {
	p := newCatalogCheckIdentityPlan(t)
	runsRow, ok := p.rows["module.installation.google_storage_bucket.runs"]
	if !ok {
		t.Fatal("the plan creates no runs bucket, so the grants below have no target")
	}
	bucket, _ := runsRow.After["name"].(string)
	if bucket == "" || bucket != p.runsBucket {
		t.Fatalf("%s is %q, want the installation's own %q", runsRow.Address, bucket, p.runsBucket)
	}
	if len(p.repos) == 0 {
		t.Fatal("the fixture's catalog names no repository, so the per-repository grants below are not exercised")
	}

	const defsRead = "module.installation.google_storage_bucket_iam_member.build_identity_definitions[0]"
	defs, ok := p.rows[defsRead]
	if !ok {
		t.Fatalf("the plan carries no %s, so the pin step of a catalog build cannot read the definition it describes", defsRead)
	}
	if defs.Type != "google_storage_bucket_iam_member" {
		t.Errorf("%s is a %s, want the bucket IAM member", defs.Address, defs.Type)
	}
	if role, _ := defs.After["role"].(string); role != "roles/storage.objectViewer" {
		t.Errorf("%s grants %q, want roles/storage.objectViewer: a build that could WRITE a definition could make every repository run an image of its choosing (gcp.CatalogReadCondition's own reason)", defs.Address, role)
	}
	if got, _ := defs.After["member"].(string); got != p.identityMember {
		t.Errorf("%s grants to %q, want %q", defs.Address, got, p.identityMember)
	}
	if got, _ := defs.After["bucket"].(string); got != bucket {
		t.Errorf("%s grants on bucket %q, want the runs bucket %q", defs.Address, got, bucket)
	}
	defsCond := oneCondition(t, defs)
	if got, want := defsCond["title"], gcp.CatalogReadConditionTitle(BuildIdentityAccountID); got != want {
		t.Errorf("%s titles its condition %q, want %q: the input's condition is titled for the check job's account, and this grant is the identity's", defs.Address, got, want)
	}
	if got, want := defsCond["expression"], gcp.CatalogReadCondition(bucket); got != want {
		t.Errorf("%s conditions on %q, want %q: the copied catalog definitions (gcp.CatalogBucketPrefix) and nothing else, byte for byte what the input carries", defs.Address, got, want)
	}

	// One records grant per repository the catalog names, and no more: the
	// union of these is the honest reading of "the repositories whose entries
	// this project builds", while one project-wide builds/ grant would be every
	// repository's records, their check state and the definitions beside them.
	records := 0
	for addr, row := range p.rows {
		if !strings.HasPrefix(addr, "module.installation.google_storage_bucket_iam_member.build_identity_records[") {
			continue
		}
		records++
		slug := forEachKey(t, addr)
		if role, _ := row.After["role"].(string); role != "roles/storage.objectUser" {
			t.Errorf("%s grants %q, want roles/storage.objectUser: the record step writes the record the check reads back", addr, role)
		}
		if got, _ := row.After["member"].(string); got != p.identityMember {
			t.Errorf("%s grants to %q, want %q", addr, got, p.identityMember)
		}
		if got, _ := row.After["bucket"].(string); got != bucket {
			t.Errorf("%s grants on bucket %q, want the runs bucket %q", addr, got, bucket)
		}
		cond := oneCondition(t, row)
		if got, want := cond["title"], gcp.CatalogRecordsConditionTitle(slug); got != want {
			t.Errorf("%s titles its condition %q, want %q", addr, got, want)
		}
		if got, want := cond["expression"], gcp.CatalogRecordsCondition(bucket, slug); got != want {
			t.Errorf("%s conditions on %q, want %q: ONE repository's builds/<slug>/catalog/ and nothing else", addr, got, want)
		}
	}
	if records != len(p.repos) {
		t.Errorf("the plan carries %d records grants, want one per repository the catalog names (%d): %v", records, len(p.repos), p.repos)
	}

	// And no Secret Manager reach, which is the point of the account (IS8,
	// D3330): a catalog build used to mount the repository's git credential as
	// its AvailableSecrets and Cloud Build resolves those as the account the
	// build runs as, so the identity held one secretAccessor per repository
	// whose entries this project builds. The build asks the installation's
	// credentials service for that credential now, so it reads no secret at
	// all and the plan must hold none — a leftover accessor would be a reach
	// the build no longer has a use for, on the one secret a project-wide
	// reach would take the credentials bundle and every repository's webhook
	// secret with.
	for addr, row := range p.rows {
		if !strings.HasPrefix(row.Type, "google_secret_manager") {
			continue
		}
		if member, _ := row.After["member"].(string); member != p.identityMember {
			continue
		}
		t.Errorf("the plan carries %s (%s), an accessor this identity must not hold any more: a catalog build's credential comes from the credentials service and not from a secret the account it runs as may read", addr, row.Type)
	}
}

// oneCondition is the row's single condition, read by key: a grant whose
// narrowing is missing or doubled is a different grant, and both are how a
// plan over-reaches silently.
func oneCondition(t *testing.T, row plannedResource) map[string]string {
	t.Helper()
	raw, ok := row.After["condition"].([]any)
	if !ok || len(raw) != 1 {
		t.Fatalf("%s carries conditions %v, want exactly one", row.Address, row.After["condition"])
	}
	out := map[string]string{}
	cond, _ := raw[0].(map[string]any)
	for _, k := range []string{"title", "expression"} {
		v, _ := cond[k].(string)
		if v == "" {
			t.Fatalf("%s's condition has no %s: %v", row.Address, k, cond)
		}
		out[k] = v
	}
	return out
}

// forEachKey is the for_each key of an instance address
// (…[<"slug">]) — the repository the grant is for.
func forEachKey(t *testing.T, addr string) string {
	t.Helper()
	open := strings.LastIndex(addr, "[")
	if open < 0 {
		t.Fatalf("%s is not a for_each instance address", addr)
	}
	return strings.Trim(strings.TrimSuffix(addr[open+1:], "]"), "\"")
}
