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

const (
	keyringMainTF  = "keyring/main.tf"
	servicesMainTF = "services/main.tf"
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

// tfBlock answers one block's text, from its header to the next block at the
// top level of its file. It is deliberately not a parser: every assertion below
// is about a string the block either writes or does not, and the cut at the next
// header keeps a block that is no longer the last one in its file from silently
// taking its neighbour's text with it.
func tfBlock(t *testing.T, src, header string) string {
	t.Helper()
	i := strings.Index(src, header)
	if i < 0 {
		t.Fatalf("no %s: the block this check is about is gone or renamed", header)
	}
	rest := src[i+len(header):]
	for _, cut := range []string{"\nresource \"", "\ndata \"", "\noutput \"", "\nvariable \""} {
		if j := strings.Index(rest, cut); j >= 0 {
			rest = rest[:j]
		}
	}
	return rest
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

// tfFiles lists this tree's Terraform, so that a sweep covers a file a later
// change adds as well as the two roots there are today. Hidden directories are
// skipped: a provider data directory belongs outside the tree (the `terraform`
// rule), and a state file is never committed.
func tfFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".tf") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the terraform tree: %v", err)
	}
	if len(files) < 6 {
		t.Fatalf("found %d .tf files: the walk did not see both roots, and a sweep over a tree it half-read proves nothing", len(files))
	}
	slices.Sort(files)
	return files
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
// The mutation this fails on: the key becoming an imported one, or an
// external_key_uri appearing on a version, or the algorithm or protection level
// moving off the pair the verifiers were built for.
func TestTheSigningKeyIsNonExportable(t *testing.T) {
	key := tfBlock(t, tfSource(t, keyringMainTF), `resource "google_kms_crypto_key" "signing"`)
	for _, want := range []string{
		`purpose = "ASYMMETRIC_SIGN"`,
		`algorithm        = "EC_SIGN_ED25519"`,
		`protection_level = "SOFTWARE"`,
	} {
		if !strings.Contains(key, want) {
			t.Errorf("%s's signing key does not write %s: S2.1 fixes the algorithm as Ed25519, which the three verifiers verify, and the key is signed with through the API rather than from a file", keyringMainTF, want)
		}
	}

	for _, rel := range tfFiles(t) {
		src := tfSource(t, rel)
		for _, route := range []string{
			// An imported version: the one way material is generated outside the
			// store and reaches it.
			"google_kms_key_ring_import_job",
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
// identity, and it is the minter's account. The intake is reachable from the
// internet — a person's browser, the merchant's webhook, the owner's admin page
// — so an intake that could sign would make T13's split a comment rather than a
// boundary, and Check 174's "the intake's identity [is] refused" is this test's
// no.
//
// The mutation this fails on, and the red step the plan names: the grant's
// member spelled google_service_account.intake.member.
func TestOnlyTheMinterHoldsTheSignerRole(t *testing.T) {
	src := tfSource(t, servicesMainTF)

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
				t.Errorf("%s holds a grant that is not one of the four this service is allowed:\n%s\nthe signer role is granted in one place, to one account, so that a reader of the plan sees every identity that can mint a licence key (Check 174)", rel, strings.TrimSpace(block))
			}
		}
	}

	grant := tfCompact(tfBlock(t, src, `resource "google_kms_key_ring_iam_member" "minter_signs"`))
	if !strings.Contains(grant, "member=google_service_account.minter.member") {
		t.Errorf("%s's signer grant is not the minter's account: the minter is the one service that signs (T13's split), and the grant is where that is decided — an intake here hands the signer role to the internet-facing service", servicesMainTF)
	}
	if !strings.Contains(grant, `role="projects/${var.project}/roles/${var.signer_role}"`) {
		t.Errorf("%s's signer grant is not the custom role keyring/ creates: the narrowest grant that mints is one permission, cloudkms.cryptoKeyVersions.useToSign, while a predefined role also reads the public key and verifies", servicesMainTF)
	}
	if !strings.Contains(grant, "key_ring_id=data.google_kms_key_ring.signing.id") {
		t.Errorf("%s's signer grant is not on the key ring: the grant has to reach the keys keyring/ created rather than a project, and the data source is what fails the plan while S3.8's apply has not run", servicesMainTF)
	}

	// The role the grant names is the role keyring/ defines, and it is the
	// narrowest one that mints: one permission rather than the predefined
	// signerVerifier, which also reads the public key and verifies. Without this
	// the pair above could name a custom role holding useToSign and
	// useToVerify, or a role id nothing creates.
	role := tfCompact(tfBlock(t, tfSource(t, keyringMainTF), `resource "google_project_iam_custom_role" "signer"`))
	for _, want := range []string{
		"role_id=var.signer_role",
		`permissions=["cloudkms.cryptoKeyVersions.useToSign"]`,
	} {
		if !strings.Contains(role, want) {
			t.Errorf("%s's signer role does not write %s: the minter signs and verifies nothing with the private half, and the grant services/ makes names this role", keyringMainTF, want)
		}
	}

	// The other half of the same boundary, over every grant rather than the
	// signer one: the intake's account is granted exactly one thing, the invoker
	// role on the minter. A second grant is a reach this file would otherwise
	// never say out loud — a KMS role to the internet-facing account above all.
	uses := 0
	for _, block := range tfBlocks(src, `resource "`) {
		if !strings.Contains(tfCompact(block), "google_service_account.intake.member") {
			continue
		}
		uses++
		if tfHeader(block) != `resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"` {
			t.Errorf("%s grants the intake's account something besides the invoker role on the minter:\n%s\nthe intake holds no KMS role and reaches the signer only through the minter (Check 174)", servicesMainTF, strings.TrimSpace(block))
		}
	}
	if uses != 1 {
		t.Errorf("%s grants the intake's account %d times, want exactly 1: its whole reach is the authenticated call to the minter", servicesMainTF, uses)
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
	src := tfSource(t, servicesMainTF)

	minter := tfCompact(tfBlock(t, src, `resource "google_cloud_run_v2_service" "minter"`))
	if !strings.Contains(minter, "invoker_iam_disabled=false") {
		t.Errorf("%s's minter service does not require IAM: the flag has to be stated false, because a service that stopped checking IAM answers every caller and the one invoker grant this file writes would be the only thing between the internet and the signer (Check 174)", servicesMainTF)
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

	publicBlock := tfCompact(tfBlock(t, src, `resource "google_cloud_run_v2_service_iam_member" "intake_public"`))
	if !strings.Contains(publicBlock, `"allUsers"`) {
		t.Errorf("%s's public binding is not the one that writes allUsers: the single public grant has to be this resource, so that a reader finds the internet's one door by its name", servicesMainTF)
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
			t.Errorf("%s's allUsers binding is not on the intake service: it does not write %s", servicesMainTF, want)
		}
	}
	if !strings.Contains(publicBlock, `role="roles/run.invoker"`) {
		t.Errorf("%s's allUsers binding is not the invoker role: any other role's public grant on a service is a reach nobody asked for", servicesMainTF)
	}

	invoker := tfCompact(tfBlock(t, src, `resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"`))
	for _, want := range []string{
		"name=google_cloud_run_v2_service.minter.",
		"location=google_cloud_run_v2_service.minter.",
		"member=google_service_account.intake.member",
		`role="roles/run.invoker"`,
	} {
		if !strings.Contains(invoker, want) {
			t.Errorf("%s's invoker grant on the minter does not write %s: the minter has no public invoker, and the intake's ID token is what the platform checks before its container runs (D450(a))", servicesMainTF, want)
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
