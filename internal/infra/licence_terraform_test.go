//go:build terraform

package infra

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// TestRenewalServiceAccountReadsOnlyTheKeyObject is K4.2's IAM claim, read off
// a REAL plan of the installation root: the renewal job's account holds three
// grants and nothing else — the three-object read of the runs bucket it finds
// its installation by, the licence object's write, and its four Secret Manager
// calls on the installation's own renewal container.
//
// The shape is the reports job's (TestTheStoreConvergesWithTheFrontDoorsOptIn's
// recipe: a real plan against a provider that makes no API call), and what it
// exists for is the half no other test can see. internal/infra/tf's cover reads
// the CONDITIONS and the container (licenceReadGrant, licenceWriteGrant,
// licenceSecretGrant) and refuses a plan that moved any of them; what it cannot
// see is what the module does with the account besides them, so this test reads
// every row of the plan that names the account at all. A second grant — the
// bucket-wide viewer a listing would need, a project role, another container —
// is what it catches.
func TestRenewalServiceAccountReadsOnlyTheKeyObject(t *testing.T) {
	const (
		readAddr      = "module.installation.google_storage_bucket_iam_member.licence_read[0]"
		writeAddr     = "module.installation.google_storage_bucket_iam_member.licence_write[0]"
		secretAddr    = "module.installation.google_secret_manager_secret_iam_member.licence_renewal[0]"
		containerAddr = "module.installation.google_secret_manager_secret.licence_renewal[0]"
		accountAddr   = "module.installation.google_service_account.licence[0]"
		jobAddr       = "module.installation.google_cloud_run_v2_job.licence[0]"
		bucket        = "fugaro-runs-proj-1234"
		// The mock-free plan computes the account the way the provider does:
		// account_id at the project.
		accountMember = "serviceAccount:fugaro-licence@proj-1234.iam.gserviceaccount.com"
	)
	spec := specWithTheLicence(t)
	full, plan := installationPlan(t, spec)
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	container := LicenceRenewalSecretID(lc.Name)

	read, ok := plan[readAddr]
	if !ok {
		t.Fatalf("the plan creates no %s: the job runs in a container with no checkout and no config file, so the marker and the published config are how it finds the installation it renews for, and a job granted the licence object alone fails before it reaches it", readAddr)
	}
	if read.After["role"] != "roles/storage.objectViewer" || read.After["bucket"] != bucket || read.After["member"] != accountMember {
		t.Errorf("%s is role %v on %v for %v, want objectViewer on %s for %s", readAddr, read.After["role"], read.After["bucket"], read.After["member"], bucket, accountMember)
	}
	if got, want := conditionExpression(read), gcp.LicenceReadCondition(bucket); got != want {
		t.Errorf("%s's condition is\n%s\nwant\n%s", readAddr, got, want)
	}

	write, ok := plan[writeAddr]
	if !ok {
		t.Fatalf("the plan creates no %s: the whole reason this job exists is to write the renewed key back, and without it every renewal fails on a 403", writeAddr)
	}
	if write.After["role"] != "roles/storage.objectUser" || write.After["bucket"] != bucket || write.After["member"] != accountMember {
		t.Errorf("%s is role %v on %v for %v, want objectUser on %s for %s", writeAddr, write.After["role"], write.After["bucket"], write.After["member"], bucket, accountMember)
	}
	if got, want := conditionExpression(write), gcp.LicenceWriteCondition(bucket); got != want {
		t.Errorf("%s's condition is\n%s\nwant\n%s: objectUser on the ONE object is the whole of the write, and the same bytes on a prefix would hand the job every record of it", writeAddr, got, want)
	}
	// The write names the licence object and nothing else of the bucket: a
	// second object in the expression is a reader or a writer D524 never
	// granted.
	if n := strings.Count(gcp.LicenceWriteCondition(bucket), "objects/"); n != 1 {
		t.Errorf("the write condition names %d objects, want 1: the job renews the licence object alone", n)
	}

	secret, ok := plan[secretAddr]
	if !ok {
		t.Fatalf("the plan creates no %s: `fugaro licence renew --job` reads the per-key renewal secret out of the installation's container and writes the next one back, and an account with no binding on it fails every renewal with a 403", secretAddr)
	}
	if secret.After["secret_id"] != container || secret.After["member"] != accountMember {
		t.Errorf("%s is secret_id %v for %v, want %s for %s", secretAddr, secret.After["secret_id"], secret.After["member"], container, accountMember)
	}
	if want := "projects/proj-1234/roles/" + tf.LicenceRenewalRole; secret.After["role"] != want {
		t.Errorf("%s grants %v, want %s: the role is this installation's own custom role, spelled literally because a custom role's name is computed during apply", secretAddr, secret.After["role"], want)
	}

	role, ok := plan["module.installation.google_project_iam_custom_role.licence_renewal[0]"]
	if !ok {
		t.Fatalf("the plan creates no custom role for the renewal job: no predefined role is exactly the four calls the job makes, so the module defines its own and the row above binds it")
	}
	if got := permissionSet(role.After["permissions"]); !slices.Equal(got, sortedStrings(tf.LicenceRenewalPermissions)) {
		t.Errorf("the renewal role's permissions are %v, want %v: secretVersionManager would add destroy, enable and disable, and the accessor/adder pair is two bindings that still carry neither secrets.get nor versions.list", got, sortedStrings(tf.LicenceRenewalPermissions))
	}

	// The account's whole reach, in one place: the address of every row of the
	// plan whose values name it. Three grants, the account itself, and the job
	// that runs as it — and nothing else.
	names := []string{}
	for _, rc := range plan {
		if strings.Contains(rc.json(), "fugaro-licence@") {
			names = append(names, rc.Address)
		}
	}
	slices.Sort(names)
	want := sortedStrings([]string{accountAddr, jobAddr, readAddr, secretAddr, writeAddr})
	if !slices.Equal(names, want) {
		t.Errorf("the rows naming the renewal job's account are %v, want %v: its reach is the three-object read, the one-object write and the four calls on its own container (D524, plan K4.2)", names, want)
	}

	// The container is created EMPTY and is not destroyed with a coordinator
	// that is turned off: the renewal secret in it is what a licensed
	// installation renews with, and the core writes no version of it (D524).
	if _, ok := plan[containerAddr]; !ok {
		t.Fatalf("the plan creates no %s: `fugaro licence set` refuses before the container exists with one line naming `fugaro update`, so update is what has to create it", containerAddr)
	}
	if v, ok := plan[containerAddr].After["labels"].(map[string]any); !ok || v["fugaro"] != "managed" {
		t.Errorf("%s's labels are %v, want the managed mark every installation resource carries", containerAddr, plan[containerAddr].After["labels"])
	}

	// The job's own argv: the mode K4.1 registers, which is what makes this job
	// the renewal and not a second, differently-argued sweep.
	if got, want := jobCommand(plan[jobAddr]), "licence renew --job"; got != want {
		t.Errorf("%s runs %q, want %q: ee/register registers `licence renew` and --job is the mode that refuses to run outside a Cloud Run job", jobAddr, got, want)
	}

	// The plan rides the run's one confirmation: NONE of the items the review
	// prints is this task's. The list is not empty in general — the fixture's
	// project jobs and the state bucket have refusals of their own, which are
	// other tasks' and are #1716's — so what is asserted is the licence half:
	// an installation with a coordinator gets the job on every update, and a
	// plan that asked its owner a question of its own would be a step
	// CLAUDE.md's no-prompt rule does not have.
	for _, item := range theRunsConfirmation(t, spec).NotCovered(full, nil) {
		if strings.Contains(item, "licence") || strings.Contains(item, "fugarolicence") {
			t.Errorf("the run's one confirmation refuses part of the licence job's plan: %s", item)
		}
	}
}

// TestTheLicenceJobConvergesWithTheCoordinatorsOptIn is the convergence CLAUDE.md
// requires of a change that reaches an installation: an installation at the
// previous version — one with a coordinator and no licence key in its tfvars —
// plans with no row of this task's, and the SAME root with licence.tf removed
// plans identically, which is what "the file is the whole of the change" means.
//
// The second half is the installation the job is NOT for: one whose tfvars
// carry no licence block at all (every installation that has not opted into the
// coordinator, internal/cli's licenceJob having dropped the block). Its plan
// names none of the resources, so an update of it is a no-op — the property
// `fugaro update` is required to have while runs are in flight and on every
// installation that never opted in.
func TestTheLicenceJobConvergesWithTheCoordinatorsOptIn(t *testing.T) {
	withJob := installationPlanFor(t, specWithTheLicence(t))
	withoutJob := installationPlanFor(t, specWithoutIt(t))
	// The release before this one: the same spec, and the module file this task
	// adds taken out of the tree.
	dropped := installationPlanFor(t, specWithoutTheLicence(t), "licence.tf")

	addresses := func(plan map[string]plannedResource) []string {
		out := make([]string, 0, len(plan))
		for addr := range plan {
			out = append(out, addr)
		}
		slices.Sort(out)
		return out
	}
	ours := []string{}
	for addr := range withJob {
		if strings.Contains(addr, ".licence") || strings.Contains(addr, "licence_renewal") {
			ours = append(ours, addr)
		}
	}
	if len(ours) == 0 {
		t.Fatalf("the plan of an installation with a coordinator and a licence spec creates none of this task's resources: the job is applied on every installation that deploys a coordinator (D524), not only on one that already holds a key")
	}
	if got := addresses(withoutJob); !slices.Equal(got, addresses(dropped)) {
		t.Errorf("the plan of an installation with no licence block is\n%v\nand the same root without licence.tf is\n%v\nthe two have to be identical: the block is dropped exactly where the coordinator's job is not deployed, so an installation that never opted in plans byte for byte what it planned before", got, addresses(dropped))
	}
	for _, addr := range ours {
		if _, ok := withoutJob[addr]; ok {
			t.Errorf("an installation whose tfvars carry no licence block still plans %s: the variable is what the module's own condition reads, so a block-less plan has to create none of it", addr)
		}
	}
}

// specWithTheLicence is the installation `fugaro update --coordinator` plans
// once the licence job exists: the coordinator's opt-in and the licence spec
// internal/cli's licenceJob fills in from it.
func specWithTheLicence(t *testing.T) InstallationSpec {
	t.Helper()
	spec := specWithoutTheLicence(t)
	l := licenceSpec(t)
	spec.Licence = &l
	return spec
}

// specWithoutTheLicence is the same installation at the version before this
// task (and the installation that never opted into the coordinator): the
// coordinator's job, and no licence block.
func specWithoutTheLicence(t *testing.T) InstallationSpec {
	t.Helper()
	return specWithoutIt(t)
}

// licenceSpec is the licence spec `fugaro update` derives for the fixture's
// installation, from the same function the update calls.
func licenceSpec(t *testing.T) LicenceSpec {
	t.Helper()
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	l, err := Licence(lc, "fugaro-runs-proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// conditionExpression is the condition an IAM row carries, as the plan holds
// it: the block is a list of one map.
func conditionExpression(rc plannedResource) string {
	conds, ok := rc.After["condition"].([]any)
	if !ok || len(conds) == 0 {
		return ""
	}
	m, ok := conds[0].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m["expression"].(string)
	return s
}

// jobCommand is a Cloud Run job's argv as one line, in the plan's own nesting:
// the job, its template, the task template and the container.
func jobCommand(rc plannedResource) string {
	tmpl, _ := rc.After["template"].([]any)
	if len(tmpl) == 0 {
		return ""
	}
	outer, _ := tmpl[0].(map[string]any)
	task, _ := outer["template"].([]any)
	if len(task) == 0 {
		return ""
	}
	inner, _ := task[0].(map[string]any)
	containers, _ := inner["containers"].([]any)
	if len(containers) == 0 {
		return ""
	}
	c, _ := containers[0].(map[string]any)
	line := []string{}
	for _, v := range append(mustSlice(c["command"]), mustSlice(c["args"])...) {
		if s, ok := v.(string); ok {
			line = append(line, s)
		}
	}
	// The command's own path is not part of the mode: what this test reads is
	// the argv the CLI sees.
	if len(line) > 0 && strings.Contains(line[0], "/") {
		line = line[1:]
	}
	return strings.Join(line, " ")
}

func mustSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// permissionSet reads a custom role's permissions as a sorted list, so the
// comparison with tf.LicenceRenewalPermissions is a set comparison.
func permissionSet(v any) []string {
	var out []string
	for _, p := range mustSlice(v) {
		if s, ok := p.(string); ok {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
