//go:build terraform

package infra

import (
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/mailbox"
)

// mailbox_key.tf is the module J6 adds (mobile-identity-pairing plan task J6;
// O17 of the pairing reconciliation), and it is held to the
// design from three ends by this file: its own text (the names, the purpose and
// the one permission list, which are what internal/mailbox's signer and
// internal/infra/tf's cover read), a REAL plan of the embedded installation root
// (the grant, and the fact that the run's one confirmation covers it), and the
// plans of the installations that did not opt in (where no row of it appears at
// all).
const (
	mailboxModulePath      = "gcp/modules/installation/mailbox_key.tf"
	orchestratorModulePath = "gcp/modules/installation/orchestrator.tf"

	mailboxRingAddr  = "module.installation.google_kms_key_ring.mailbox[0]"
	mailboxKeyAddr   = "module.installation.google_kms_crypto_key.mailbox_consumer[0]"
	mailboxRoleAddr  = "module.installation.google_project_iam_custom_role.mailbox_signer[0]"
	mailboxGrantAddr = "module.installation.google_kms_crypto_key_iam_member.orchestrator_mailbox_signer[0]"
	cloudKMSAPIAddr  = `module.installation.google_project_service.this["cloudkms.googleapis.com"]`

	// The one binding J6 makes, by type: O17's "the consumer's account only" is
	// a claim about how many rows of a plan are of it.
	mailboxGrantType = "google_kms_crypto_key_iam_member"
)

// TestMailboxKeyNamesAreTheGoSpellings reads the module's own literals and holds
// them to the two Go spellings that consume them: internal/mailbox's
// ConsumerKeyRingName / ConsumerCryptoKeyName / ConsumerCryptoKeyPath, which
// the consumer's signer composes the key version's name from, and
// internal/infra/tf's MailboxKeyRingName / MailboxCryptoKeyName /
// mailboxCryptoKeyPath, which the cover holds a grant's target to.
//
// Why a test over the module's TEXT rather than over a plan. A plan's key ring
// and key carry the names as computed attributes, so a plan can be perfectly
// consistent while the three spellings disagree — and the disagreement is the
// failure that matters: a renamed key is a SECOND key beside the one every
// paired phone pinned (O16), and a cover that composed the old name would
// refuse the module's own grant. The plan half of the same fact is
// TestMailboxKeyIsTheConsumersSignerAlone below.
func TestMailboxKeyNamesAreTheGoSpellings(t *testing.T) {
	src := readMailboxModule(t)
	// The binding is orchestrator.tf's, which is where section 4.3's table
	// lives: TestOrchestratorIsNeverALauncher refuses the account's address as
	// a member in any other file of the module, so the grant's own literals are
	// read there.
	grantSrc := readModuleFile(t, orchestratorModulePath)
	const project, region = "proj-1234", "us-east5"

	ring := moduleStringLiteral(t, src, "mailbox_key_ring")
	key := moduleStringLiteral(t, src, "mailbox_crypto_key")
	for _, want := range []struct {
		who, name string
	}{
		{"internal/mailbox's", mailbox.ConsumerKeyRingName},
		{"internal/infra/tf's", tf.MailboxKeyRingName},
	} {
		if ring != want.name {
			t.Errorf("mailbox_key.tf's key ring is %q and %s is %q: the signer, the module and the cover compose ONE key version's name, and a difference here is a key beside the one every paired phone pinned", ring, want.who, want.name)
		}
	}
	for _, want := range []struct {
		who, name string
	}{
		{"internal/mailbox's", mailbox.ConsumerCryptoKeyName},
		{"internal/infra/tf's", tf.MailboxCryptoKeyName},
	} {
		if key != want.name {
			t.Errorf("mailbox_key.tf's key is %q and %s is %q: the two Go spellings are what the signer's signing call and the cover's grant target are composed from", key, want.who, want.name)
		}
	}

	// The key's resource name, the module's own template filled in the way the
	// module fills it: this is the string the plan's grant carries and the
	// string internal/mailbox asks Cloud KMS to sign under.
	fill := strings.NewReplacer("${var.project}", project, "${var.region}", region,
		"${local.mailbox_key_ring}", ring, "${local.mailbox_crypto_key}", key,
		"${var.orchestrator.account_id}", OrchestratorAccountID).Replace
	if got, want := fill(moduleStringLiteral(t, src, "mailbox_crypto_key_id")), mailbox.ConsumerCryptoKeyPath(project, region); got != want {
		t.Errorf("mailbox_key.tf composes the key's resource name as %s, and internal/mailbox composes %s: the grant's crypto_key_id is the first and the signing call's keyVersion is composed from the second, so a difference is a grant on one key and a signature made under another", got, want)
	}
	if got := mailbox.ConsumerCryptoKeyVersionPath(project, region); !strings.HasPrefix(got, mailbox.ConsumerCryptoKeyPath(project, region)+"/") {
		t.Errorf("internal/mailbox's key version is %s, which is not a version of %s: the signer's whole identity is the key plus the version the module's version_template creates", got, mailbox.ConsumerCryptoKeyPath(project, region))
	}

	// The one grant's three literals: the account that may sign, the role it
	// signs with, and the role's whole permission list.
	if got := moduleStringLiteral(t, src, "role_id"); got != tf.MailboxSignerRole {
		t.Errorf("mailbox_key.tf's role is %q and internal/infra/tf's is %q: the cover's row is keyed by the role ID, so a difference is a row that stops closing the cell — and the cell is the whole of O17's \"the mailbox consumer's account only\"", got, tf.MailboxSignerRole)
	}
	grant := moduleResourceBlock(t, grantSrc, "google_kms_crypto_key_iam_member", "orchestrator_mailbox_signer")
	if got, want := fill(moduleStringLiteral(t, grant, "role")), "projects/"+project+"/roles/"+tf.MailboxSignerRole; got != want {
		t.Errorf("orchestrator.tf grants %s, want %s: the cover reads a custom role's project out of the grant's own role string, and a role of another project is a role this run's cover does not know", got, want)
	}
	member := fill(moduleStringLiteral(t, grant, "member"))
	wantMember := "serviceAccount:" + OrchestratorAccountID + "@" + project + ".iam.gserviceaccount.com"
	if member != wantMember {
		t.Errorf("mailbox_key.tf grants the signer's role to %s, want %s: the mailbox consumer of this installation IS the resident orchestrator (P5.1), so the module writes that account out rather than a reference — a computed member reaches a plan as UNKNOWN, and the cover cannot tell an unknown principal from an ordinary job account", member, wantMember)
	}
	if got, want := moduleStringList(t, src, "permissions"), tf.RolePermissions[tf.MailboxSignerRole]; !slices.Equal(got, want) {
		t.Errorf("mailbox_key.tf's signer role holds %v, and internal/infra/tf pins %v: the nearest predefined role carries the verify half and the metadata reads beside useToSign (roles/cloudkms.signerVerifier), and this account signs and does nothing else with the key", got, want)
	}

	// The key's purpose and its one version: Ed25519, asymmetric signing, and
	// the algorithm internal/mailbox names. SOFTWARE is not a preference —
	// Cloud KMS offers Ed25519 at SOFTWARE alone.
	for _, want := range []struct{ attr, got, want string }{
		{"purpose", moduleStringLiteral(t, src, "purpose"), "ASYMMETRIC_SIGN"},
		{"algorithm (the key's version_template)", moduleStringLiteral(t, src, "algorithm"), mailbox.ConsumerKeyAlgorithm},
		{"protection_level", moduleStringLiteral(t, src, "protection_level"), "SOFTWARE"},
	} {
		if want.got != want.want {
			t.Errorf("mailbox_key.tf's %s is %q, want %q: O17's first sentence is one Ed25519 signing key, and the algorithm the signer names and the one the module creates have to be the same algorithm", want.attr, want.got, want.want)
		}
	}

	// What the file DECLARES, which is the strongest thing a module can say
	// about key material: four resources and nothing else. A
	// google_kms_crypto_key_version would be a second version created by hand
	// (version_template is what creates the first, and the signer signs under
	// that one), and a key import job or a secret-manager container would be a
	// way for material to reach this tree — which is exactly what O17 forbids:
	// no private key material exists outside KMS.
	var types []string
	for _, m := range regexp.MustCompile(`(?m)^resource\s+"([^"]+)"`).FindAllStringSubmatch(src, -1) {
		types = append(types, m[1])
	}
	slices.Sort(types)
	wantTypes := []string{"google_kms_crypto_key", "google_kms_key_ring", "google_project_iam_custom_role"}
	if !slices.Equal(types, wantTypes) {
		t.Errorf("mailbox_key.tf declares %v, want %v: the ring, the one key and the one role are this file's whole Terraform, and anything else here is a resource nobody reviewed — a second key version, an import job, a container holding material. The one binding is orchestrator.tf's, for that file's own reason (TestOrchestratorIsNeverALauncher)", types, wantTypes)
	}
	// ... and the one binding orchestrator.tf declares is the ONLY Cloud KMS
	// resource outside this file, which is section 4.3's rule read from the
	// other end: a second google_kms_ block there is a key or a grant this task
	// did not write (TestOrchestratorIsNeverALauncher is the rule's own test).
	var kmsRows []string
	for _, m := range regexp.MustCompile(`(?m)^resource\s+"(google_kms_[a-z_]+)"\s+"([a-z0-9_]+)"`).FindAllStringSubmatch(grantSrc, -1) {
		kmsRows = append(kmsRows, m[1]+"."+m[2])
	}
	if !slices.Equal(kmsRows, []string{"google_kms_crypto_key_iam_member.orchestrator_mailbox_signer"}) {
		t.Errorf("orchestrator.tf declares the Cloud KMS resources %v, want exactly the one binding: the key, the ring and the role are mailbox_key.tf's, and a second block here is a resource no reader of that file would look for", kmsRows)
	}
	if regexp.MustCompile(`(?m)^data\s+"`).MatchString(src) {
		t.Errorf("mailbox_key.tf has a data source: nothing here may READ key material, and the plan's own inputs are the installation's variables")
	}
}

// TestMailboxKeyIsTheConsumersSignerAlone is the plan half of J6: a real plan of
// the embedded installation root for the fixture the golden is made of, with a
// resident orchestrator opted in — the consumer — and what it says about the
// key, the role and the one grant.
func TestMailboxKeyIsTheConsumersSignerAlone(t *testing.T) {
	spec, plan, rows := mailboxKeyPlan(t)
	keyPath := mailbox.ConsumerCryptoKeyPath(spec.Project, spec.Region)
	member := "serviceAccount:" + OrchestratorAccountID + "@" + spec.Project + ".iam.gserviceaccount.com"

	key, ok := rows[mailboxKeyAddr]
	if !ok {
		t.Fatalf("the plan of an installation with a resident orchestrator creates no %s: the consumer signs its replies and heartbeats with this key (O6, O17), so an installation that runs one and holds no key signs nothing the phone can verify", mailboxKeyAddr)
	}
	if got, want := key.After["name"], mailbox.ConsumerCryptoKeyName; got != want {
		t.Errorf("%s: name is %v, want %v", mailboxKeyAddr, got, want)
	}
	if got, want := key.After["purpose"], "ASYMMETRIC_SIGN"; got != want {
		t.Errorf("%s: purpose is %v, want %v: a key that can decrypt or wrap is a key with a surface the consumer never uses", mailboxKeyAddr, got, want)
	}
	tpl, _ := key.After["version_template"].([]any)
	if len(tpl) != 1 {
		t.Fatalf("%s: the plan's version_template is %v, want one block: the template is what creates the key's first version, and the signer signs under version 1 alone (ConsumerKeyVersionName)", mailboxKeyAddr, key.After["version_template"])
	}
	tmpl, _ := tpl[0].(map[string]any)
	for _, want := range []struct {
		what string
		got  any
		want any
	}{
		{"algorithm", tmpl["algorithm"], mailbox.ConsumerKeyAlgorithm},
		{"protection_level", tmpl["protection_level"], "SOFTWARE"},
	} {
		if want.got != want.want {
			t.Errorf("%s version_template: %s is %v, want %v", mailboxKeyAddr, want.what, want.got, want.want)
		}
	}

	role, ok := rows[mailboxRoleAddr]
	if !ok {
		t.Fatalf("the plan creates the key and no %s: the grant below is on a role, and a role the project does not hold is an apply that fails", mailboxRoleAddr)
	}
	if got, want := role.After["role_id"], tf.MailboxSignerRole; got != want {
		t.Errorf("%s: role_id is %v, want %v", mailboxRoleAddr, got, want)
	}
	if got := stringList(role.After["permissions"]); !slices.Equal(got, tf.RolePermissions[tf.MailboxSignerRole]) {
		t.Errorf("%s: permissions are %v, want %v: the account's whole reach on the key is making a signature with it", mailboxRoleAddr, got, tf.RolePermissions[tf.MailboxSignerRole])
	}

	grant, ok := rows[mailboxGrantAddr]
	if !ok {
		t.Fatalf("the plan creates the key and the role and no %s: without the binding nothing signs — the key is a public half the installation cannot use — and J5c's replies and heartbeats would fail at the first API call", mailboxGrantAddr)
	}
	for _, want := range []struct {
		what string
		got  any
		want any
	}{
		{"crypto_key_id", grant.After["crypto_key_id"], keyPath},
		{"role", grant.After["role"], "projects/" + spec.Project + "/roles/" + tf.MailboxSignerRole},
		{"member", grant.After["member"], member},
	} {
		if want.got != want.want {
			t.Errorf("%s: %s is %v, want %v", mailboxGrantAddr, want.what, want.got, want.want)
		}
	}

	// THE one grant: exactly one row of the plan is a binding on a KMS key, and
	// it is this one, to this account. O17's "with signing granted to the
	// mailbox consumer's account only" is about a plan as much as about the
	// module's text, and a second binding of the signer's role — however it is
	// spelled, whatever module it comes from — is a second consumer identity.
	var binding []string
	for addr, rc := range rows {
		if rc.Type == mailboxGrantType {
			binding = append(binding, addr)
		}
	}
	if len(binding) != 1 || binding[0] != mailboxGrantAddr {
		t.Errorf("the plan's bindings on a KMS key are %v, want exactly [%s]: one installation, one consumer, one key — a grant on a second key is a signature no phone of this installation verifies, made with material no step of this tree published", binding, mailboxGrantAddr)
	}

	// ... and the run's own confirmation covers it (D1545's tables): a new type
	// or a new kind of principal WARNS rather than refuses, so what this test
	// holds is that the deliberate rows are there and that the plan's own key,
	// role and grant are not among the lines the review screen would print.
	for _, item := range theRunsConfirmation(t, spec).NotCovered(plan, nil) {
		if strings.Contains(item, mailboxAddrPrefix) || strings.Contains(item, tf.MailboxSignerRole) || strings.Contains(item, mailbox.ConsumerKeyRingName) {
			t.Errorf("the run's one confirmation refuses the consumer's own signing rows: %s\nThe key, its ring, its role and its one grant are J6's rows and the tables carry them on purpose (cover.go's allowedTypes, grantRules and mailboxSignerGrant); `fugaro update` creating them must not ask a question about them", item)
		}
	}
}

// mailboxAddrPrefix is how every row of mailbox_key.tf is addressed in a plan:
// the module they are declared in, so that a refusal item naming one of them is
// the one thing this test refuses however the cover phrased it.
const mailboxAddrPrefix = "google_kms_"

// TestMailboxKeyIsTheOptInsAlone is the convergence half: an installation that
// asked for neither end of the conversation plans no row of J6 at all — the key
// ring, the key, the role, the binding and the Cloud KMS API are all rows of the
// two opt-ins — and the mobile service's own golden, which is the plan of an
// installation that HAS the pairing front end and no consumer yet, carries the
// key and the role and no binding.
func TestMailboxKeyIsTheOptInsAlone(t *testing.T) {
	// The default plan: the fixture without an orchestrator block and without
	// enable_mobile_service, which is every installation at the version before
	// this one. Nothing of J6 may appear in it, and the API enablement is part
	// of that promise — `fugaro update`'s plan for an installation that never
	// asked for a consumer or a pairing front end is the plan it always made.
	_, _, rows := mailboxKeyPlanOff(t)
	for addr, rc := range rows {
		if strings.HasSuffix(rc.Type, "kms_crypto_key") || rc.Type == "google_kms_crypto_key_iam_member" || rc.Type == "google_kms_key_ring" {
			t.Errorf("the plan of an installation with neither opt-in carries %s: the key is the pairing front end's and the consumer's (mailbox_key.tf's header), so an installation that has neither acquires nothing of J6", addr)
		}
		if strings.Contains(rc.json(), mailbox.ConsumerKeyRingName) {
			t.Errorf("the plan of an installation with neither opt-in names %s at %s: no row of it may mention the consumer's key ring", mailbox.ConsumerKeyRingName, addr)
		}
	}
	if _, ok := rows[cloudKMSAPIAddr]; ok {
		t.Errorf("the plan of an installation with neither opt-in enables %s: the API is enabled here or not at all (apis.tf), and an installation that asked for neither end of the conversation has no key to create", cloudKMSAPIAddr)
	}

	// The front end without a consumer: the committed golden of the credentials
	// fixture plus enable_mobile_service — a real state (the mobile service's
	// own switch brings the front end up, and the owner's go for the consumer is
	// P5.1's). It holds the ring, the key and the role, and NO binding: nothing
	// signs, and the key exists for the phone to pin once a consumer is opted
	// into.
	golden := firebaseGoldenRows(t, "installation-mobile")
	for _, addr := range []string{mailboxRingAddr, mailboxKeyAddr, mailboxRoleAddr} {
		if _, ok := golden[addr]; !ok {
			t.Errorf("the committed installation-mobile golden holds no %s: the public mobile service's plan is where the key has to exist for the phone to pin (O16), and a golden regenerated without it is a front end whose pairing response carries no key", addr)
		}
	}
	for addr, rc := range golden {
		if rc.Type == mailboxGrantType {
			t.Errorf("the installation-mobile golden carries %s: that installation has no consumer yet, so the binding is counted on local.orchestrator_on alone and the golden is the plan that proves it", addr)
		}
	}
}

// mailboxKeyPlan plans the fixture the golden is made of, with a resident
// orchestrator opted in — the consumer — and returns the spec, the plan and its
// rows: the plan itself is what the cover reads.
func mailboxKeyPlan(t *testing.T) (InstallationSpec, *tf.Plan, map[string]plannedResource) {
	t.Helper()
	spec := goldenInstallationSpec(t)
	if spec.History == nil || spec.History.FirebaseProject == "" {
		t.Fatalf("the golden fixture has changed shape: history %+v — the orchestrator block needs the Firebase project", spec.History)
	}
	plan, rows := installationPlanWith(t, spec, map[string]any{"orchestrator": orchestratorBlock(t, spec, spec.History.FirebaseProject)})
	return spec, plan, rows
}

// mailboxKeyPlanOff is the same fixture with neither opt-in written: the
// installation at the version before this task.
func mailboxKeyPlanOff(t *testing.T) (InstallationSpec, *tf.Plan, map[string]plannedResource) {
	t.Helper()
	spec := goldenInstallationSpec(t)
	plan, rows := installationPlan(t, spec)
	return spec, plan, rows
}

// TestMailboxKeyGrantIsRefusedWhenItMoves is the cover's own half of O17, and
// it is the reason a plan — and not only mailbox_key.tf — carries the claim:
// the grant rule opens the cell to the resident orchestrator, and
// mailboxSignerGrant closes it again to this installation's one key, spelled
// out, with no condition. Every case below is a plan an operator's hand-edited
// tfvars could produce and the module never does.
func TestMailboxKeyGrantIsRefusedWhenItMoves(t *testing.T) {
	spec, _, rows := mailboxKeyPlan(t)
	cover := theRunsConfirmation(t, spec)
	grant := rows[mailboxGrantAddr].After
	keyPath := mailbox.ConsumerCryptoKeyPath(spec.Project, spec.Region)
	otherKey := mailbox.ConsumerCryptoKeyPath("proj-9999", spec.Region)
	// The golden fixture lists nobody, and one case below is a person the
	// review screen names: the cover's own listed set is what makes "a person"
	// the case worth trying, so the person is added to it rather than assumed
	// to be in it.
	person := "user:person-1@example.com"
	cover.Listed = append(cover.Listed, person)

	for _, c := range []struct {
		what    string
		after   map[string]any
		unknown map[string]any
		covered bool
	}{
		{
			what:    "the module's own grant",
			after:   grant,
			unknown: map[string]any{},
			covered: true,
		},
		{
			what:    "a second key of the same project",
			after:   withGrant(grant, "crypto_key_id", strings.TrimSuffix(keyPath, mailbox.ConsumerCryptoKeyName)+"fugaro-mailbox-other"),
			unknown: map[string]any{},
		},
		{
			what:    "a key of another project",
			after:   withGrant(grant, "crypto_key_id", otherKey),
			unknown: map[string]any{},
		},
		{
			what:    "a key nobody can name",
			after:   withGrant(grant, "crypto_key_id", nil),
			unknown: map[string]any{"crypto_key_id": true},
		},
		{
			what:    "the signer's role of another project",
			after:   withGrant(grant, "role", "projects/proj-9999/roles/"+tf.MailboxSignerRole),
			unknown: map[string]any{},
		},
		{
			// The other project here is one the cover ADMITS (the installation's
			// Firebase project, which Cover.Projects carries), so roleKey
			// resolves the ID and opens the cell: what refuses the row is
			// mailboxSignerGrant reading the role's own project. A case only of
			// proj-9999 would be refused by roleKey's unknown-project branch
			// instead, and would stay green with the role check dead.
			what:    "the signer's role of the Firebase project beside the installation",
			after:   withGrant(grant, "role", "projects/"+spec.History.FirebaseProject+"/roles/"+tf.MailboxSignerRole),
			unknown: map[string]any{},
		},
		{
			what:    "a predefined signing role, which carries the verify half",
			after:   withGrant(grant, "role", "roles/cloudkms.signerVerifier"),
			unknown: map[string]any{},
		},
		{
			what:    "another job account",
			after:   withGrant(grant, "member", "serviceAccount:fugaro-mcp@"+spec.Project+".iam.gserviceaccount.com"),
			unknown: map[string]any{},
		},
		{
			what:    "the resident orchestrator of another project",
			after:   withGrant(grant, "member", "serviceAccount:"+OrchestratorAccountID+"@proj-9999.iam.gserviceaccount.com"),
			unknown: map[string]any{},
		},
		{
			what:    "a person on the review screen",
			after:   withGrant(grant, "member", person),
			unknown: map[string]any{},
		},
		{
			what: "under a condition, which the module never sets",
			after: withGrant(grant, "condition", []any{map[string]any{
				"title": "fugaro-x", "expression": "true", "description": nil,
			}}),
			unknown: map[string]any{},
		},
	} {
		change := tf.ResourceChange{
			Address: mailboxGrantAddr, Mode: "managed", Type: mailboxGrantType,
			Change: tf.Change{Actions: []string{"create"}, After: c.after, AfterUnknown: c.unknown},
		}
		got := cover.NotCovered(&tf.Plan{ResourceChanges: []tf.ResourceChange{change}}, nil)
		if (len(got) == 0) != c.covered {
			t.Errorf("%s: NotCovered = %q, covered want %v.\nO17 is \"signing granted to the mailbox consumer's account only\": the row says the consumer's account may hold the signer's role, and the target is what the row cannot say — another key is a signature no phone of this installation verifies (O16), another account or another project is a second consumer identity, and a condition fences nothing the single permission does not already fence (cover.go's mailboxSignerGrant)", c.what, got, c.covered)
		}
	}
}

// withGrant is a planned grant's after map with one attribute replaced. The
// real row is cloned rather than rebuilt so that every case differs from the
// module's own in exactly the one place the case names.
func withGrant(after map[string]any, attr string, value any) map[string]any {
	out := maps.Clone(after)
	out[attr] = value
	return out
}

// readMailboxModule is mailbox_key.tf's own text, out of the tree the binary
// embeds — the same tree `fugaro update` writes into its workdir.
func readMailboxModule(t *testing.T) string {
	t.Helper()
	return readModuleFile(t, mailboxModulePath)
}

// readModuleFile is one file of the embedded tree's own text.
func readModuleFile(t *testing.T, path string) string {
	t.Helper()
	b, err := fs.ReadFile(terraform.FS, path)
	if err != nil {
		t.Fatalf("the embedded installation module has no %s: J6's key lives there, and every literal this file pins is read out of it: %v", path, err)
	}
	return string(b)
}

// moduleResourceBlock is one resource block of a module as its own text, so
// that a literal read out of it is the one THAT resource writes: orchestrator.tf
// has many `role =` lines, and the first of them is not this one.
func moduleResourceBlock(t *testing.T, src, typ, name string) string {
	t.Helper()
	head := `resource "` + typ + `" "` + name + `" {`
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("no resource \"%s\" \"%s\" in the module", typ, name)
	}
	rest := src[i+len(head):]
	j := strings.Index(rest, "\n}")
	if j < 0 {
		t.Fatalf("resource \"%s\" \"%s\" is not closed", typ, name)
	}
	return rest[:j]
}

// moduleStringLiteral is one `attr = "..."` line of a module, its value. An
// assignment this file expects and does not find is a failure and not an empty
// string: a renamed or moved literal is exactly the change these tests exist to
// catch, and a silent "" would compare unequal for the wrong reason.
func moduleStringLiteral(t *testing.T, src, attr string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(attr) + `\s*=\s*"([^"]*)"`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no `%s = \"...\"` line", mailboxModulePath, attr)
	}
	return m[1]
}

// moduleStringList is one `attr = ["...", "..."]` line of a module, its
// values: the signer role's permission list, which is a list of one today.
func moduleStringList(t *testing.T, src, attr string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(attr) + `\s*=\s*\[([^\]]*)\]`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no `%s = [...]` line", mailboxModulePath, attr)
	}
	var out []string
	for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}
