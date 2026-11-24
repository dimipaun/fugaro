// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The root key project's Terraform (mobile identity plan task A4; the spec's
// "Assertion signing keys"; D2792 as amended by O7 and O15; Check 737).
//
// These tests read the tree the way infra_test.go reads A3's root — as text, so
// that whitespace and comment spelling are nobody's business and what is pinned
// is what a block WRITES — and they reuse that file's HCL readers rather than
// writing a second parser. What is pinned here is the whole of what the root key
// project is for: one Ed25519 ASYMMETRIC_SIGN key whose private half no file ever
// carried, signing granted to exactly one member and to no service account, a
// role that is the two permissions signing takes, and a project that runs nothing
// else.
//
// The failure these tests are built around is Check 737's worst finding: a root
// key the owner's default login can sign with, or one whose private half exists
// somewhere a machine can read. Nothing here holds a key — the tree holds none
// either, which is most of what the checks below establish.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rootkeyRoot is where the root key project's Terraform lives, relative to this
// package. It is a root of its own and not part of A3's tree: the owner applies
// it in a project of its own, and the two share no state.
const rootkeyRoot = "rootkey/terraform"

// rkFiles lists the root key root's readable Terraform, sorted, and fails on a
// file this suite cannot read. It is tfFiles' walk with the root above, because
// the same three second forms are exactly as able to change what this apply does
// with no block below changing.
func rkFiles(t *testing.T) []string {
	t.Helper()
	var files, refused []string
	err := filepath.WalkDir(rootkeyRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != rootkeyRoot && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(rootkeyRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case strings.HasSuffix(rel, ".tf.json"), tfOverrides(rel), tfVars(rel):
			refused = append(refused, rel)
		case strings.HasSuffix(rel, ".tf"):
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", rootkeyRoot, err)
	}
	for _, rel := range refused {
		t.Errorf("%s is a Terraform configuration file these tests cannot read: a `.tf.json` file writes the same resources in another syntax, an override file is merged over its directory last, and a variable file decides what the blocks below it mean (which member may sign, which key signs), so any of them can change what an apply does with no block below changing. Keep the root key project's root in plain HCL and in one form, with no variable file in it (A4)", rel)
	}
	slices.Sort(files)
	if len(files) == 0 {
		t.Fatalf("%s holds no .tf file", rootkeyRoot)
	}
	return files
}

// rkSource reads one file of the root key root with its comments removed.
func rkSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(rootkeyRoot, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return tfStripComments(string(b))
}

// rkBlock answers the one block the root key root declares under the given
// header, whichever of its files holds it.
func rkBlock(t *testing.T, header string) string {
	t.Helper()
	var found []string
	for _, rel := range rkFiles(t) {
		src := rkSource(t, rel)
		for _, kind := range []string{`resource "`, `data "`, `output "`} {
			for _, block := range tfBlocks(src, kind) {
				if tfHeader(block) == header {
					found = append(found, block)
				}
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("the root key project's root declares no %s: the block this check is about is gone or renamed", header)
	default:
		t.Fatalf("the root key project's root declares %s %d times: Terraform refuses a resource declared twice in one directory, so this tree does not validate", header, len(found))
	}
	return ""
}

// rkVarBlock answers the one variable block the root declares under the name, so
// that a check can read what the variable holds beyond its default — the
// validation that refuses a member, which a delete would take with it.
func rkVarBlock(t *testing.T, name string) string {
	t.Helper()
	header := `variable "` + name + `"`
	for _, rel := range rkFiles(t) {
		blocks := tfBlocksUntil(rkSource(t, rel), header, tfTopLevelHeaders)
		if len(blocks) > 0 {
			return blocks[0]
		}
	}
	t.Fatalf("the root declares no %s variable: a value the blocks below read is decided by this default, and a variable that is gone is a rule that would pass by reading nothing", name)
	return ""
}

// rkVarDefault answers a variable block's default as the quoted literal it is
// written with, and the empty string for a variable declared without one — which
// is what a required variable reads as, and what the checks below hold.
func rkVarDefault(t *testing.T, name string) string {
	t.Helper()
	value, _ := tfAttr(rkVarBlock(t, name), "default")
	return value
}

// TestRootKeyInfraTheRootIsOneFormOfConfiguration: the root key project's root is
// plain HCL in one form — no `.tf.json`, no override file, no variable file, no
// `module`, `import` or `moved` block — and it holds no resource this work did
// not put there.
//
// The mutations this fails on: any of the three second forms, which create,
// adopt or rename a resource with no block below changing, and a resource of a
// kind the root key project has no business holding — a service, a job, a bucket,
// a database, a project of its own. The set is enumerated rather than the
// refusals listed, so a resource nobody here named fails until a person says why
// it belongs in the project that holds the root key.
func TestRootKeyInfraTheRootIsOneFormOfConfiguration(t *testing.T) {
	files := rkFiles(t)
	if !slices.Equal(files, []string{"main.tf"}) {
		t.Errorf("the root key project's root holds %v, want exactly main.tf: the plan names one file for this root, and a second is a place a reviewer reading the file they expect does not look (A4)", files)
	}
	for _, rel := range files {
		src := rkSource(t, rel)
		for _, form := range tfSecondForms {
			if strings.Contains(src, form.header) {
				t.Errorf("%s writes %s: this root key project's resources are the ones below and nothing else — %s changes what an apply does with no block here changing (A4)", rel, form.what, form.what)
			}
		}
	}
	sanctioned := []string{
		`resource "google_project_service" "this"`,
		`resource "google_kms_key_ring" "root"`,
		`resource "google_kms_crypto_key" "root"`,
		`resource "google_project_iam_custom_role" "root_signer"`,
		`resource "google_kms_crypto_key_iam_member" "owner_signs"`,
	}
	for _, rel := range files {
		for _, block := range tfBlocks(rkSource(t, rel), `resource "`) {
			if !slices.Contains(sanctioned, tfHeader(block)) {
				t.Errorf("%s declares a resource this root key project is not allowed:\n%s\nthe whole of what the project holds is: the APIs it uses, the key ring, the root key, the role that signs and its one grant. A service, a job, a bucket or a database beside them is a thing running in the project that holds the root key (A4, O7)", rel, strings.TrimSpace(block))
			}
		}
	}
	// The APIs are the three this project's own resources need: a key ring, a
	// custom role and a grant. Run, storage, Firebase and the identity toolkit are
	// the identity project's, and enabling one here is the first half of putting
	// a service in this project.
	// The list is read out of the block's own text rather than from the
	// `for_each` attribute, whose value runs over several lines inside a
	// function call: what matters is which API names the block writes.
	apis := tfCompact(rkBlock(t, `resource "google_project_service" "this"`))
	for _, wanted := range []string{"cloudkms.googleapis.com", "cloudresourcemanager.googleapis.com", "iam.googleapis.com"} {
		if !strings.Contains(apis, `"`+wanted+`"`) {
			t.Errorf("the root key project's root does not enable %s: the key ring, the custom role and the grant are what it applies, and an owner who let it manage its APIs would find the apply refusing on a service the project never turned on (A4)", wanted)
		}
	}
	for _, refused := range []string{"run.googleapis.com", "storage.googleapis.com", "firebase", "identitytoolkit", "firebasedatabase"} {
		if strings.Contains(apis, refused) {
			t.Errorf("the root key project's root enables %s: this project runs nothing and serves nothing — it holds the key the served key set is signed with, and every API a service needs belongs to the project that runs it (A4, O7)", refused)
		}
	}
}

// TestRootKeyInfraTheRootKeyIsANonExportableEd25519Signer: the one key in the
// ring is the ASYMMETRIC_SIGN Ed25519 one O7 fixes, at the protection level the
// algorithm is offered at, guarded against a destroy — and nothing in the tree is
// a route by which a private half generated elsewhere could reach the ring.
//
// The mutations this fails on: the purpose or the algorithm moved onto another
// pair, a protection level the algorithm is not offered at (an HSM level refuses
// Ed25519, so the apply that sets it creates no key at all), a key without
// prevent_destroy, a rotation period that would add a version nobody published, a
// second key of the same kind, and any of the import, external-key or file routes
// by which material from outside the key store could sign as the root.
func TestRootKeyInfraTheRootKeyIsANonExportableEd25519Signer(t *testing.T) {
	keys, signing := 0, 0
	for _, rel := range rkFiles(t) {
		for _, block := range tfBlocks(rkSource(t, rel), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_kms_crypto_key"`) {
				continue
			}
			keys++
			if purpose, _ := tfAttr(block, "purpose"); tfCompact(purpose) == `"ASYMMETRIC_SIGN"` {
				signing++
			}
		}
	}
	if keys != 1 || signing != 1 {
		t.Errorf("the root key project declares %d KMS crypto keys, %d of them ASYMMETRIC_SIGN, want 1 and 1: the root key is the project's only key, and a second one — signing or not — is a key in the ring the signing grant reaches or a key no check here reads (A4, O7)", keys, signing)
	}
	key := rkBlock(t, `resource "google_kms_crypto_key" "root"`)
	tfAttrsAre(t, key, "the root key", []tfWant{
		{"key_ring", "google_kms_key_ring.root.id"},
		{"name", "var.key"},
		{"purpose", `"ASYMMETRIC_SIGN"`},
	}, "the root key signs the served key set and does nothing else: a purpose that only encrypts and decrypts cannot sign, and the name comes from a variable because the read that says the laptop's default login cannot sign with it (Check 737) names the same key this apply creates (A4, O7)")

	version := tfNested(t, key, "version_template")
	tfAttrsAre(t, version, "the root key's version_template", []tfWant{
		{"algorithm", `"EC_SIGN_ED25519"`},
		{"protection_level", `"SOFTWARE"`},
	}, "O7 fixes Ed25519 as the root key's algorithm — C1's envelope is EdDSA and the signing command hands KMS the payload itself — and Cloud KMS offers it at SOFTWARE and not at HSM, so a level moved off this pair is an apply that creates no key")

	lifecycle := tfNested(t, key, "lifecycle")
	tfAttrsAre(t, lifecycle, "the root key's lifecycle", []tfWant{
		{"prevent_destroy", "true"},
	}, "every published key set and every build's pinned root public key is verified against a version of this key, so a destroy takes the root out from under every project with no revocation to notice it by")

	if rotation, ok := tfAttr(key, "rotation_period"); ok {
		t.Errorf("the root key writes rotation_period = %s: an automatic rotation adds a version nobody published, and the ceremony this task builds makes the new version and the signed set naming it one deliberate act (A4, O7, O18)", rotation)
	}

	for _, rel := range rkFiles(t) {
		src := tfCompact(rkSource(t, rel))
		for _, route := range []string{
			"google_kms_key_ring_import_job",
			"import_only",
			"skip_initial_version_creation",
			"external_key_uri",
			"google_kms_secret_ciphertext",
			"tls_private_key",
			"google_service_account_key",
			"-----BEGIN",
		} {
			if strings.Contains(src, route) {
				t.Errorf("%s writes %s: the root key's private half is generated inside Cloud KMS and read out by nothing — no import job, no external store, no exported account key and no file anywhere in this tree (A4, O7, .claude/rules/ee.md R5)", rel, route)
			}
		}
	}
}

// rkSanctionedGrants is every block of the root key project that may grant a
// role, and the whole of it: the role itself, and its one grant on the root key.
// The list is read by rkUnsanctionedGrants, which refuses anything else that
// writes a member or a role.
var rkSanctionedGrants = map[string]string{
	`resource "google_project_iam_custom_role" "root_signer"`:   "the root signer role itself, and the whole of what signing a key set takes",
	`resource "google_kms_crypto_key_iam_member" "owner_signs"`: "its one grant, to the separate owner identity and on the root key",
}

// rkUnsanctionedGrants answers every block of one source that grants a role and
// is not on the list above, as the block's own text. It takes the text rather
// than reading a file so that a test can hand it a copy with one more block in
// it, which is the only way to show the sweep refusing a grant this root does not
// write.
func rkUnsanctionedGrants(src string) []string {
	var out []string
	for _, block := range tfBlocks(src, `resource "`) {
		header := tfHeader(block)
		if !tfGrants(header, tfCompact(block)) {
			continue
		}
		if _, ok := rkSanctionedGrants[header]; !ok {
			out = append(out, strings.TrimSpace(block))
		}
	}
	return out
}

// TestRootKeyInfraSigningIsGrantedToExactlyOneMember: exactly one member in the
// whole project can sign, and it is the separate owner identity O15 gives as a
// variable. Not a service account, not the owner's default login and not a second
// person's — and the grant is made on the key rather than on the ring or the
// project, so a key the ring gains later is not signed with by an identity nobody
// granted it to.
//
// The mutations this fails on: a second member, a predefined role, a grant on the
// ring or the project, a service account anywhere in the tree, a member written
// as a literal instead of the variable, and a group where O15 asks for one
// person. This is the check Check 737's restore paragraph is about.
func TestRootKeyInfraSigningIsGrantedToExactlyOneMember(t *testing.T) {
	grant := rkBlock(t, `resource "google_kms_crypto_key_iam_member" "owner_signs"`)
	tfAttrsAre(t, grant, "the root key's signing grant", []tfWant{
		{"crypto_key_id", "google_kms_crypto_key.root.id"},
		{"role", "google_project_iam_custom_role.root_signer.id"},
		{"member", "var.owner_member"},
	}, "signing is granted to the separate owner identity O15 names as a variable, on the root key: a literal here is a person written into the tree rather than given by the owner, the project's own roles would sign with everything the project ever holds, and a grant made on the ring would reach every key the ring gains (A4, O7, O15)")

	members := 0
	for _, rel := range rkFiles(t) {
		for _, block := range tfBlocks(rkSource(t, rel), `resource "`) {
			compact := tfCompact(block)
			members += strings.Count(compact, "member=") + strings.Count(compact, "members=")
		}
	}
	if members != 1 {
		t.Errorf("the root key project writes %d member(s), want exactly 1: the reading Check 737 takes is that one identity can sign and every other login is refused, so a second member anywhere in this project — a binding, a policy, a group, a domain — is a second identity the root key signs for (A4, O7, O15)", members)
	}
	for _, rel := range rkFiles(t) {
		src := rkSource(t, rel)
		for _, header := range tfHeaders(src, `resource "`) {
			if strings.Contains(header, "google_service_account") {
				t.Errorf("%s declares %s: a service account in the project that holds the root key is an identity that can sign a key set, and a machine that signs a key set is what the ceremony exists to prevent. The whole of what this project runs is nothing (A4, O7, O15)", rel, header)
			}
		}
		if strings.Contains(tfCompact(src), "serviceAccount:") || strings.Contains(tfCompact(src), "iam.gserviceaccount.com") {
			t.Errorf("%s names a service account: signing is granted to one person's own identity and to no robot, and an address written as text is an identity the grant above does not show (A4, O7, O15)", rel)
		}
		for _, block := range rkUnsanctionedGrants(src) {
			t.Errorf("%s holds a grant this root key project is not allowed:\n%s\nthe whole list of grants this root may hold is:\n%s", rel, block, tfList(rkSanctionedGrants))
		}
	}
}

// TestRootKeyInfraTheSignerRoleIsTheNarrowestThatSigns: the custom role is the
// two permissions the ceremony needs — sign, and read the public half to print
// and pin — and nothing wider.
//
// The mutations this fails on: a predefined role (signerVerifier also verifies,
// owner reaches the ring itself, which is exactly how the default login would
// sign), a third permission such as destroy, update or get on the ring, and a
// role id not taken from a variable — the value Check 737's own reading has to
// name.
func TestRootKeyInfraTheSignerRoleIsTheNarrowestThatSigns(t *testing.T) {
	role := rkBlock(t, `resource "google_project_iam_custom_role" "root_signer"`)
	tfAttrsAre(t, role, "the root signer role", []tfWant{
		{"project", "var.project"},
		{"role_id", "var.signer_role"},
		{"permissions", `["cloudkms.cryptoKeyVersions.useToSign", "cloudkms.cryptoKeyVersions.viewPublicKey"]`},
	}, "signing a key set takes useToSign and printing and pinning the root public key takes viewPublicKey, and the role does nothing else: a predefined role carries more, and a third permission is a reach of the root key the plan never asked for (A4, O7)")
	for _, wider := range []string{
		"cloudkms.cryptoKeyVersions.destroy",
		"cloudkms.cryptoKeyVersions.update",
		"cloudkms.cryptoKeys.setIamPolicy",
		"cloudkms.cryptoKeys.getIamPolicy",
		"cloudkms.keyRings.setIamPolicy",
		"roles/owner",
		"roles/cloudkms.admin",
		"roles/cloudkms.signerVerifier",
	} {
		if strings.Contains(tfCompact(role), wider) {
			t.Errorf("the root signer role writes %s: the role is the two permissions signing takes, and one that can change a key, its IAM or the ring is a role that can put the root key somewhere else (A4, O7)", wider)
		}
	}
}

// TestRootKeyInfraTheOwnerIdentityIsTheOwnerAndNotAnAccount: the member O15 gives
// as a variable has no default and its validation refuses everything but a
// person's or a group's identity.
//
// The mutations this fails on: a default (which is a signing grant made by an
// apply nobody thought about, and the finding Check 737 is built around), a
// validation that accepts a service account, a domain or a federated principal,
// and the variable itself gone — the last of which is why the block is read
// rather than the default alone.
func TestRootKeyInfraTheOwnerIdentityIsTheOwnerAndNotAnAccount(t *testing.T) {
	if got := rkVarDefault(t, "owner_member"); got != "" {
		t.Errorf("owner_member has the default %s: a default is a signing grant this root makes without the owner naming the identity it is for, and the whole point of O15 is that the member is the owner's own choice (A4, O15)", got)
	}
	block := rkVarBlock(t, "owner_member")
	validation := tfNested(t, block, "validation")
	compact := tfCompact(validation)
	if !strings.Contains(compact, `(user|group):`) {
		t.Errorf("owner_member's validation does not hold the member to a user: or group: identity:\n%s\na validation that accepts anything is a service account the day someone writes one, and a robot that can sign a key set is the failure this root exists to prevent (A4, O15)", strings.TrimSpace(validation))
	}
	if !strings.Contains(compact, "serviceAccount") {
		t.Errorf("owner_member's validation does not refuse a service account:\n%s\nthe acceptance is that signing is granted to no service account, and a rule the apply enforces is worth more than a sentence in a comment (A4, O15)", strings.TrimSpace(validation))
	}
	if got := rkVarDefault(t, "project"); got != "" {
		t.Errorf("project has the default %s: this root is applied in the owner's own project and never creates one, so a default here is a project id written into the tree that an apply in the wrong directory would use without a word (A4, O7)", got)
	}
}

// TestRootKeyInfraTheCommandSignsWithTheVersionTheRootNames: the root prints the
// version the signing command is run with, so the ceremony's `-key-version` is a
// value an apply produced rather than one an owner assembles by hand from four
// names — and the root key's id is printed for Check 737's read of whether the
// laptop's default login can sign with it.
func TestRootKeyInfraTheCommandSignsWithTheVersionTheRootNames(t *testing.T) {
	version := rkBlock(t, `output "key_version"`)
	value, ok := tfAttr(version, "value")
	if !ok || tfCompact(value) != `"${google_kms_crypto_key.root.id}/cryptoVersions/1"` {
		t.Errorf("the root key project's key_version output writes %q: the signing command signs with the version this names, and a value assembled anywhere else is one the owner has to keep in step with the key by hand (A4, O7)", value)
	}
	key := rkBlock(t, `output "root_key"`)
	value, ok = tfAttr(key, "value")
	if !ok || tfCompact(value) != `google_kms_crypto_key.root.id` {
		t.Errorf("the root key project's root_key output writes %q: Check 737's reading is a signing call made against this key by a login that must be refused, and a key named by hand in the check is a key the check can name wrongly (A4, O7)", value)
	}
}

// TestRootKeyInfraTheSweepSeesASecondGrant: the grant sweep reads a block this
// root does not write, which is the only way to show it would refuse one. A sweep
// that only ever read a clean tree would pass with its search broken.
func TestRootKeyInfraTheSweepSeesASecondGrant(t *testing.T) {
	source := rkSource(t, "main.tf")
	if got := rkUnsanctionedGrants(source); len(got) != 0 {
		t.Fatalf("the root key project already holds a grant the sweep refuses:\n%s", strings.Join(got, "\n"))
	}
	for name, added := range map[string]string{
		"a second member on the same key": `
resource "google_kms_crypto_key_iam_member" "second_signer" {
  crypto_key_id = google_kms_crypto_key.root.id
  role          = google_project_iam_custom_role.root_signer.id
  member        = "user:someone-else@example.com"
}
`,
		"a grant on the ring": `
resource "google_kms_key_ring_iam_member" "ring_signer" {
  key_ring_id = google_kms_key_ring.root.id
  role        = google_project_iam_custom_role.root_signer.id
  member      = var.owner_member
}
`,
		"a binding instead of a member": `
resource "google_kms_crypto_key_iam_binding" "everyone" {
  crypto_key_id = google_kms_crypto_key.root.id
  role          = google_project_iam_custom_role.root_signer.id
  members       = [var.owner_member, "user:someone-else@example.com"]
}
`,
		"a grant on the project": `
resource "google_project_iam_member" "project_signer" {
  project = var.project
  role    = "roles/cloudkms.signerVerifier"
  member  = var.owner_member
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := rkUnsanctionedGrants(source + added); len(got) == 0 {
				t.Fatalf("the sweep passed on %s:\n%s\nthe root key project's whole list of allowed grants is:\n%s", name, added, tfList(rkSanctionedGrants))
			}
		})
	}
}
