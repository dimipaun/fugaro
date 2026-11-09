// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

//go:build terraform

// The key service's infrastructure, held at the plan's own level (S3.5): the
// two facts Check 174 measures once the service runs are facts about this
// configuration rather than about a rendered plan, and a plan needs the Google
// provider, credentials and a project an agent must not have.
//
// So the tests read the two roots' Terraform as text, the way
// deploy/terraform/modules/installation's own security tests do, and the
// `terraform` tag is what the plan's command runs them with:
//
// Reading a configuration as text is also why this tree holds one form of it and
// no other: a `.tf.json` file, an override file and a module block are each a
// place a grant can be written where nothing above changes, and the checks here
// see none of the three. TestTheTestsReadEveryFileOfTheTree refuses all of them.
//
//	go test -tags terraform ./ee/keyservice/... -count=1
//
// The tag also carries the one test that needs the binary (the fmt check): this
// tree is formatted where terraform is, which is where the plan's command runs
// the whole file.
package terraform

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The two roots, named once: every detailed check below reads the block a root
// declares rather than the file it happens to live in today.
const (
	keyringRoot  = "keyring"
	servicesRoot = "services"
)

// tfSource reads one file of this tree with its comments removed: what these
// tests decide is what the configuration WRITES, and the prose above a resource
// is where a change is explained. A comment saying "no external key here" is not
// an external key, and a check that read it as one would make explaining the
// rule the same act as breaking it.
func tfSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
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

// tfBlocks answers every block in one file whose header starts with the given
// text, so that a sweep covers a resource a later change adds rather than the
// one name it was written for. The cut is the next resource header, which is
// enough for a sweep over resources, and no block is reported twice.
func tfBlocks(src, header string) []string {
	var out []string
	for i := 0; ; {
		j := strings.Index(src[i:], header)
		if j < 0 {
			return out
		}
		start := i + j
		rest := src[start+len(header):]
		if k := strings.Index(rest, "\nresource \""); k >= 0 {
			rest = rest[:k]
		}
		out = append(out, header+rest)
		i = start + len(header)
	}
}

// tfCompact removes every space, tab and newline from a file or a block. These
// tests decide what a configuration WRITES, and terraform fmt's alignment — or
// the absence of it — is no part of that: `member = "allUsers"` and
// `member="allUsers"` are one grant written twice, and a check that read the
// first spelling only would let the second through. Whitespace inside a quoted
// string goes with the rest, which costs nothing here: every string matched
// below is a single token and none of them spans a space.
func tfCompact(src string) string {
	return strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(src)
}

// tfHeader answers a block's first line — its kind and its name — which is what
// the list of sanctioned grants is keyed by.
func tfHeader(block string) string {
	if i := strings.Index(block, "{"); i >= 0 {
		block = block[:i]
	}
	return strings.TrimSpace(block)
}

// tfGrants reports whether a block hands an identity a role. It is deliberately
// loose — an IAM resource type, or an attribute a grant is written with — and it
// is loose in the safe direction: the sweep's other half is a list of the grants
// this service is allowed to hold, so a block the test thinks grants something
// and finds unlisted fails, while being wrong about a block that grants nothing
// costs only a message. The attribute test is on the compacted body, because a
// grant written `role="projects/x/roles/y"` and one written
// `role = "projects/x/roles/y"` are the same grant.
func tfGrants(header, compact string) bool {
	if strings.Contains(header, "_iam_") {
		return true
	}
	for _, attr := range []string{"member=", "members=", "role=", "role_id=", "permissions=", "policy_data="} {
		if strings.Contains(compact, attr) {
			return true
		}
	}
	return false
}

// tfList renders a map of the grants this service may hold, one per line and
// sorted, so that a failure names the whole list rather than only the block it
// refused: the point of the list is that a person reads it.
func tfList(grants map[string]string) string {
	lines := make([]string, 0, len(grants))
	for header, why := range grants {
		lines = append(lines, "  "+header+" — "+why)
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// tfTerraformFiles walks a tree and splits the files Terraform would read into
// the ones these tests can and the ones they refuse, both relative to the root
// given and sorted. What is refused is every form of configuration a text sweep
// over HCL cannot see: a `.tf.json` file is the same declarations in another
// syntax, and an override file (`override.tf`, `main_override.tf`, either of
// them in JSON) is merged over the rest of its directory last, so it can add a
// grant to the key ring or flip a service's IAM without any block below
// changing. A `.tfvars` file only fills in variables — it cannot declare a
// resource — so it is none of this walk's business.
//
// Hidden directories are skipped: a provider data directory belongs outside the
// tree (the `terraform` rule), and a state file is never committed.
func tfTerraformFiles(root string) (files, refused []string, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case strings.HasSuffix(rel, ".tf.json"), tfOverrides(rel):
			refused = append(refused, rel)
		case strings.HasSuffix(rel, ".tf"):
			files = append(files, rel)
		}
		return nil
	})
	slices.Sort(files)
	slices.Sort(refused)
	return files, refused, err
}

// tfOverrides reports whether a path is one of the names Terraform merges over
// the other files of its directory. The JSON spellings of both are covered by
// the `.tf.json` test beside this one.
func tfOverrides(rel string) bool {
	base := filepath.Base(rel)
	return base == "override.tf" || strings.HasSuffix(base, "_override.tf")
}

// tfTerraform lists one tree's readable Terraform and fails the test on a file
// the sweep cannot read: a sweep is only as good as the files it saw, and a tree
// holding a second form of a root's configuration is one this suite has
// half-read.
func tfTerraform(t *testing.T, root string) []string {
	t.Helper()
	files, refused, err := tfTerraformFiles(root)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	for _, rel := range refused {
		t.Errorf("%s is a Terraform configuration file these tests cannot read: a `.tf.json` file writes the same resources in another syntax and an override file is merged over its directory last, so either one can grant a role, flip a service's IAM or widen the signer role without any block below changing. Keep the root in plain HCL and in one form, or teach these tests to read the form (S3.5)", rel)
	}
	return files
}

// tfFiles lists the whole tree's readable Terraform, so that a sweep covers a
// file a later change adds as well as the two roots there are today.
func tfFiles(t *testing.T) []string {
	t.Helper()
	files := tfTerraform(t, ".")
	if len(files) < 6 {
		t.Fatalf("found %d .tf files: the walk did not see both roots, and a sweep over a tree it half-read proves nothing", len(files))
	}
	return files
}

// tfRootFiles lists one root's readable Terraform, for the checks that read the
// block a root declares rather than sweeping the tree.
func tfRootFiles(t *testing.T, root string) []string {
	t.Helper()
	files := tfTerraform(t, root)
	if len(files) == 0 {
		t.Fatalf("%s holds no .tf file", root)
	}
	return files
}

// tfRootBlock answers the one block a root declares under the given header,
// whichever of that root's files holds it: Terraform refuses a resource declared
// twice in one directory, so a root has exactly one, and a check that read
// services/main.tf alone would read a file the block had been moved out of.
func tfRootBlock(t *testing.T, root, header string) string {
	t.Helper()
	var found []string
	for _, rel := range tfRootFiles(t, root) {
		for _, block := range tfBlocks(tfSource(t, filepath.Join(root, rel)), `resource "`) {
			if tfHeader(block) == header {
				found = append(found, block)
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("%s declares no %s: the block this check is about is gone or renamed", root, header)
	default:
		t.Fatalf("%s declares %s %d times: Terraform refuses a resource declared twice in one directory, so this tree does not validate", root, header, len(found))
	}
	return ""
}

// TestTheSigningKeyIsNonExportable: the private half of a signing key exists
// inside the key store and nowhere else — never a file, never a service's
// memory, never a machine the owner or an agent holds (S3.3, §11, Check 174).
//
// What this can decide is the configuration's half of that, and it is the whole
// of it in Terraform's vocabulary: the keys are KMS keys created by the key
// store's own API (so no code here ever handles a private half), the algorithm
// is Ed25519 at the SOFTWARE level S2.1's verifiers accept, and the two routes
// by which a private half could exist OUTSIDE the store — an imported key
// version, and a Cloud EKM key whose material lives in another store — are
// declared nowhere in this tree.
//
// The mutation this fails on: the key becoming an imported one (`import_only`
// with `skip_initial_version_creation`, whose material an external machine would
// generate), an external_key_uri appearing on a version, or the algorithm or
// protection level moving off the pair the verifiers were built for.
func TestTheSigningKeyIsNonExportable(t *testing.T) {
	key := tfCompact(tfRootBlock(t, keyringRoot, `resource "google_kms_crypto_key" "signing"`))
	for _, want := range []string{
		`purpose="ASYMMETRIC_SIGN"`,
		`algorithm="EC_SIGN_ED25519"`,
		`protection_level="SOFTWARE"`,
	} {
		if !strings.Contains(key, want) {
			t.Errorf("the signing key in %s does not write %s: S2.1 fixes the algorithm as Ed25519, which the three verifiers verify, and the key is signed with through the API rather than from a file", keyringRoot, want)
		}
	}

	// The key must also be one the store creates the material of: an
	// `import_only` key takes versions whose material was generated elsewhere, and
	// `skip_initial_version_creation` exists to create one before any import — the
	// pair is a key whose private half lives outside the store, with no import job
	// in this tree to import it by, because an import job is created with a
	// command rather than by a configuration (S3.8's runbook).
	for _, forbidden := range []string{"import_only", "skip_initial_version_creation"} {
		if strings.Contains(key, forbidden) {
			t.Errorf("the signing key in %s writes %s: the key's material is created inside the key store and never imported into it, or the private half exists in whatever machine generated it (S3.3, §11, Check 174)", keyringRoot, forbidden)
		}
	}

	for _, rel := range tfFiles(t) {
		src := tfSource(t, rel)
		for _, route := range []string{
			// An imported version: the one way material is generated outside the
			// store and reaches it.
			"google_kms_key_ring_import_job",
			// A key whose versions are imported rather than created.
			"import_only",
			"skip_initial_version_creation",
			// Cloud EKM: the key stays in another store entirely, which is an
			// exportable key by another name.
			"external_key_uri",
			// A private half written or held in this repository's own tree.
			"tls_private_key",
			"google_secret_manager_secret_version",
			`"-----BEGIN`,
		} {
			if strings.Contains(src, route) {
				t.Errorf("%s writes %s: the licence keys' private halves exist inside the key ring and nowhere else — no import, no external store, no file (S3.3, Check 174)", rel, route)
			}
		}
	}
}

// TestOnlyTheMinterHoldsTheSignerRole: the signer role reaches exactly one
// identity, and it is the minter's account — and that account is the one the
// minter service RUNS as, since a service running as another identity is that
// identity. The intake is reachable from the internet — a person's browser, the
// merchant's webhook, the owner's admin page — so an intake that could sign
// would make T13's split a comment rather than a boundary, and Check 174's "the
// intake's identity [is] refused" is this test's no.
//
// The mutations this fails on, and the red step the plan names first: the
// grant's member spelled google_service_account.intake.member, and the intake
// service's template.service_account set to the minter's account.
func TestOnlyTheMinterHoldsTheSignerRole(t *testing.T) {
	// Every block in the tree that grants anything at all is one of the four
	// grants this service is allowed to hold, and the list is the whole of it.
	// A count of grants of one resource TYPE in one file would miss a second
	// holder written another way — the same role granted with
	// google_project_iam_member, google_kms_key_ring_iam_binding or an
	// `_iam_policy`, a signer role named by its literal id instead of through
	// var.signer_role, a plain `user:` added beside the minter's account — so
	// the discriminator is "this block grants a role" whatever it is called, and
	// the answer for a grant nobody has reviewed is this list's absence.
	sanctioned := map[string]string{
		`resource "google_project_iam_custom_role" "signer"`:                        "the signer role itself (keyring/), and the whole of what signing takes",
		`resource "google_kms_key_ring_iam_member" "minter_signs"`:                  "its one grant, to the minter's account",
		`resource "google_cloud_run_v2_service_iam_member" "intake_public"`:         "the intake's public invoker, which reaches no key at all",
		`resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"`: "the intake's one reach, the authenticated call to the minter",
	}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			header := tfHeader(block)
			if !tfGrants(header, tfCompact(block)) {
				continue
			}
			if _, ok := sanctioned[header]; !ok {
				t.Errorf("%s holds a grant this service is not allowed:\n%s\nthe signer role is granted in one place, to one account, so that a reader of the plan sees every identity that can mint a licence key (Check 174). The whole list of grants is:\n%s", rel, strings.TrimSpace(block), tfList(sanctioned))
			}
		}
	}

	grant := tfCompact(tfRootBlock(t, servicesRoot, `resource "google_kms_key_ring_iam_member" "minter_signs"`))
	if !strings.Contains(grant, "member=google_service_account.minter.member") {
		t.Errorf("the signer grant in %s is not the minter's account: the minter is the one service that signs (T13's split), and the grant is where that is decided — an intake here hands the signer role to the internet-facing service", servicesRoot)
	}
	if !strings.Contains(grant, `role="projects/${var.project}/roles/${var.signer_role}"`) {
		t.Errorf("the signer grant in %s is not the custom role keyring/ creates: the narrowest grant that mints is one permission, cloudkms.cryptoKeyVersions.useToSign, while a predefined role also reads the public key and verifies", servicesRoot)
	}
	if !strings.Contains(grant, "key_ring_id=data.google_kms_key_ring.signing.id") {
		t.Errorf("the signer grant in %s is not on the key ring: the grant has to reach the keys keyring/ created rather than a project, and the data source is what fails the plan while S3.8's apply has not run", servicesRoot)
	}

	// The role the grant names is the role keyring/ defines, and it is the
	// narrowest one that mints: one permission rather than the predefined
	// signerVerifier, which also reads the public key and verifies. Without this
	// the pair above could name a custom role holding useToSign and
	// useToVerify, or a role id nothing creates.
	role := tfCompact(tfRootBlock(t, keyringRoot, `resource "google_project_iam_custom_role" "signer"`))
	for _, want := range []string{
		"role_id=var.signer_role",
		`permissions=["cloudkms.cryptoKeyVersions.useToSign"]`,
	} {
		if !strings.Contains(role, want) {
			t.Errorf("the signer role in %s does not write %s: the minter signs and verifies nothing with the private half, and the grant services/ makes names this role", keyringRoot, want)
		}
	}

	// The other half of the same boundary, over every grant in the tree rather
	// than the signer one: the intake's account is granted exactly one thing, the
	// invoker role on the minter. A second grant is a reach nothing else here
	// would say out loud — a KMS role to the internet-facing account above all,
	// and the keyring root is as good a place to write one as this one.
	uses := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfCompact(block), "google_service_account.intake.member") {
				continue
			}
			uses++
			if tfHeader(block) != `resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"` {
				t.Errorf("%s grants the intake's account something besides the invoker role on the minter:\n%s\nthe intake holds no KMS role and reaches the signer only through the minter (Check 174)", rel, strings.TrimSpace(block))
			}
		}
	}
	if uses != 1 {
		t.Errorf("this tree grants the intake's account %d times, want exactly 1: its whole reach is the authenticated call to the minter", uses)
	}

	// And the identity each service RUNS as, which is the half a grant sweep
	// cannot see: a service that runs as another account IS that account, so an
	// intake whose template names the minter's would hold the signer role from
	// the internet while every grant above stayed exactly as written.
	for _, service := range []string{"intake", "minter"} {
		block := tfCompact(tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "`+service+`"`))
		if want := "service_account=google_service_account." + service + ".email"; !strings.Contains(block, want) {
			t.Errorf("the %s service does not run as its own account: it does not write %s. The split between the intake and the minter is a split of identities, so the internet-facing service must not run as the one the signer role is granted to (Check 174, D450(a))", service, want)
		}
	}

	// The two accounts are named in exactly the blocks the design allows, so a
	// third one has to be argued for rather than added: the minter's in its own
	// service and the one grant, the intake's in its own service and the one
	// invoker grant. A resource outside that list naming either account is an
	// identity taken where it was not meant to go; the outputs name both, and a
	// value handed out is not a reach.
	referrers := map[string]map[string]string{
		"google_service_account.minter.": {
			`resource "google_cloud_run_v2_service" "minter"`:          "the service that runs as it",
			`resource "google_kms_key_ring_iam_member" "minter_signs"`: "the one identity granted the signer role",
		},
		"google_service_account.intake.": {
			`resource "google_cloud_run_v2_service" "intake"`:                           "the service that runs as it",
			`resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"`: "the one identity allowed to call the minter",
		},
	}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			compact := tfCompact(block)
			for account, allowed := range referrers {
				if !strings.Contains(compact, account) {
					continue
				}
				if _, ok := allowed[tfHeader(block)]; !ok {
					t.Errorf("%s reaches %s from a block that is not one of the two the design allows:\n%s\nthe minter's account is the one identity that signs and the intake's is the one caller it has, so a third block naming either is an identity taken where it was not meant to go (Check 174)", rel, account, strings.TrimSpace(block))
				}
			}
		}
	}
}

// TestTheMinterHasNoPublicInvoker: the minter is not reachable from the
// internet, and the intake is the one caller it has. Three things decide that,
// and all three are asserted here rather than left to the design's prose: the
// minter's service REQUIRES IAM — with the flag true Cloud Run serves every
// caller the network delivers, which would leave the grants below as decoration
// and a minter that anyone may invoke — no binding anywhere hands the internet
// an invoker but the intake's own public one, and the minter's only invoker is
// the intake's account. Check 174 again, from the network's side.
func TestTheMinterHasNoPublicInvoker(t *testing.T) {
	minter := tfCompact(tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "minter"`))
	if !strings.Contains(minter, "invoker_iam_disabled=false") {
		t.Errorf("the minter service in %s does not require IAM: the flag has to be stated false, because a service that stopped checking IAM answers every caller and the one invoker grant this root writes would be the only thing between the internet and the signer (Check 174)", servicesRoot)
	}

	// The count is over the bare token in every block of the tree rather than
	// over one spelling of one attribute: `member = "allUsers"`, a
	// google_cloud_run_v2_service_iam_binding writing `members = ["allUsers"]`,
	// an `_iam_policy`, a second service's binding — all of them say allUsers,
	// and the intake's own public binding is the only one of them this service
	// is allowed. What a policy read from a file would say is beyond a sweep over
	// this tree, and nothing here reads one.
	public := 0
	for _, rel := range tfFiles(t) {
		f := tfCompact(tfSource(t, rel))
		if strings.Contains(f, `"allAuthenticatedUsers"`) {
			t.Errorf("%s grants allAuthenticatedUsers: any Google account is not the intake's caller, and a signed-in stranger reaching the minter would sign with the key ring", rel)
		}
		public += strings.Count(f, `"allUsers"`)
	}
	if public != 1 {
		t.Errorf("this tree grants allUsers %d times, want exactly 1: the intake is the one public surface, and every other binding — on the minter above all — hands the internet an invoker it should not have", public)
	}

	publicBlock := tfCompact(tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "intake_public"`))
	if !strings.Contains(publicBlock, `"allUsers"`) {
		t.Errorf("the public binding in %s is not the one that writes allUsers: the single public grant has to be this resource, so that a reader finds the internet's one door by its name", servicesRoot)
	}
	// The service the binding NAMES is the one it opens: a block that kept the
	// intake in its location and moved its name to the minter would open the
	// signer's door while still mentioning the intake, which is why the two
	// attributes are checked rather than the reference.
	for _, want := range []string{
		"name=google_cloud_run_v2_service.intake.",
		"location=google_cloud_run_v2_service.intake.",
	} {
		if !strings.Contains(publicBlock, want) {
			t.Errorf("the allUsers binding in %s is not on the intake service: it does not write %s", servicesRoot, want)
		}
	}
	if !strings.Contains(publicBlock, `role="roles/run.invoker"`) {
		t.Errorf("the allUsers binding in %s is not the invoker role: any other role's public grant on a service is a reach nobody asked for", servicesRoot)
	}

	invoker := tfCompact(tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"`))
	for _, want := range []string{
		"name=google_cloud_run_v2_service.minter.",
		"location=google_cloud_run_v2_service.minter.",
		"member=google_service_account.intake.member",
		`role="roles/run.invoker"`,
	} {
		if !strings.Contains(invoker, want) {
			t.Errorf("the invoker grant on the minter in %s does not write %s: the minter has no public invoker, and the intake's ID token is what the platform checks before its container runs (D450(a))", servicesRoot, want)
		}
	}
}

// TestTheTestsReadEveryFileOfTheTree: everything Terraform would read under
// this tree is either read by the checks above or refused here, because a sweep
// that silently skipped a file is a sweep that proves nothing about it. Three
// forms are refused, each one a place a grant can be written where no block
// above changes:
//
//   - a `.tf.json` file, which is the same declarations in another syntax, so
//     every text check above looks at it and finds nothing;
//   - an override file — `override.tf`, `*_override.tf`, either of them in JSON
//     — which Terraform merges over the rest of its directory last, so the
//     minter's `invoker_iam_disabled` or the signer grant's member can be
//     rewritten while `main.tf` still reads as written;
//   - a `module` block, whose configuration is another tree these tests do not
//     walk at all.
//
// None of the three is used anywhere in this repository, and a tree that gains
// one fails until a person either removes it or teaches these tests to read it.
// The cases below are the reviewer's mutations, built in a scratch tree: an
// override flipping the minter's IAM flag, a suffixed override adding
// `useToVerify` to the signer role, a JSON file granting the signer role, an
// override in JSON naming the intake in the signer grant — and a second plain
// HCL file in a root, which is admitted, because the rule is one FORM per root
// and not one file.
func TestTheTestsReadEveryFileOfTheTree(t *testing.T) {
	for _, tc := range []struct {
		name    string
		write   map[string]string
		refused []string
	}{
		{
			name: "an override flipping the minter's IAM flag",
			write: map[string]string{
				"services/main.tf": "resource \"google_cloud_run_v2_service\" \"minter\" {\n  invoker_iam_disabled = false\n}\n",
				"services/override.tf": "resource \"google_cloud_run_v2_service\" \"minter\" {\n" +
					"  invoker_iam_disabled = true\n}\n",
			},
			refused: []string{"services/override.tf"},
		},
		{
			name: "a suffixed override widening the signer role",
			write: map[string]string{
				"keyring/main.tf": "resource \"google_project_iam_custom_role\" \"signer\" {\n" +
					"  permissions = [\"cloudkms.cryptoKeyVersions.useToSign\"]\n}\n",
				"keyring/main_override.tf": "resource \"google_project_iam_custom_role\" \"signer\" {\n" +
					"  permissions = [\"cloudkms.cryptoKeyVersions.useToSign\", \"cloudkms.cryptoKeyVersions.useToVerify\"]\n}\n",
			},
			refused: []string{"keyring/main_override.tf"},
		},
		{
			name: "a JSON file granting the signer role",
			write: map[string]string{
				"services/main.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {\n" +
					"  member = google_service_account.minter.member\n}\n",
				"services/extra.tf.json": `{"resource":{"google_kms_key_ring_iam_binding":{"second":{"role":"projects/x/roles/fugaroKeySigner","members":["serviceAccount:intake@example.iam.gserviceaccount.com"],"key_ring_id":"x"}}}}`,
			},
			refused: []string{"services/extra.tf.json"},
		},
		{
			name: "an override in JSON naming the intake in the signer grant",
			write: map[string]string{
				"services/main.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {\n" +
					"  member = google_service_account.minter.member\n}\n",
				"services/override.tf.json": `{"resource":{"google_kms_key_ring_iam_member":{"minter_signs":{"member":"${google_service_account.intake.member}"}}}}`,
			},
			refused: []string{"services/override.tf.json"},
		},
		{
			name: "a second plain HCL file in a root",
			write: map[string]string{
				"services/main.tf":   "resource \"google_service_account\" \"intake\" {}\n",
				"services/grants.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {}\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, body := range tc.write {
				path := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("writing %s: %v", rel, err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("writing %s: %v", rel, err)
				}
			}

			files, refused, err := tfTerraformFiles(root)
			if err != nil {
				t.Fatalf("walking the scratch tree: %v", err)
			}
			if !slices.Equal(refused, tc.refused) {
				t.Errorf("the walk refuses %v and hands %v to the checks, want it to refuse %v: a file it hands over unread is a file no check above ever saw", refused, files, tc.refused)
			}
			if want := len(tc.write) - len(tc.refused); len(files) != want {
				t.Errorf("the walk hands over %v, want %d plain .tf file(s): the rule is one form per root, not one file", files, want)
			}
		})
	}

	// The same rule over the tree as it is: no override, no JSON, no module, so
	// that a mutation added to the real tree fails rather than being swept past.
	for _, rel := range tfFiles(t) {
		if strings.Contains(tfCompact(tfSource(t, rel)), `module"`) {
			t.Errorf("%s writes a module block: a module's configuration is a tree these tests do not walk, so a grant written inside one would be invisible here. Write the resources in this tree, or teach these tests to read the module (S3.5)", rel)
		}
	}
}

// TestTheKeyServiceTerraformIsFormatted runs the binary over the whole tree,
// because a root no CI job formats is a root that drifts: ci.yml's terraform job
// covers deploy/terraform only, and this tree is applied by the owner with their
// own credentials (S3.7, S3.8).
func TestTheKeyServiceTerraformIsFormatted(t *testing.T) {
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("this test runs the terraform binary, which is why this file carries the terraform build tag: %v", err)
	}
	out, err := exec.Command(bin, "fmt", "-check", "-recursive", ".").CombinedOutput()
	if err != nil {
		t.Errorf("terraform fmt -check -recursive . over the key service's tree:\n%s", out)
	}
}
