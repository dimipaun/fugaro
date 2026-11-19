// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// The identity project's infrastructure, held at the plan's own level (A3, D2781,
// D2792, O7): the facts Check 730 measures once the owner applies the root are
// facts about this configuration rather than about a rendered plan, and a plan
// needs the Google provider, credentials and a project an agent must not have.
//
// So these tests read the root's Terraform as text, the way
// ee/keyservice/terraform/infra_test.go and
// deploy/terraform/modules/installation's security tests do, and they run
// WITHOUT a build tag because the plan's own command runs them that way:
//
//	go test ./ee/identitysvc/ -run 'TestInfra' -count=1
//
// Reading a configuration as text is also why the root holds one form of it and
// no other: a `.tf.json` file, an override file and a variable file are each a
// place the signing key's algorithm, a grant or a sign-in provider can be written
// where no block below changes, and the checks here see none of the three.
// TestInfraTheRootIsOneFormOfConfiguration refuses all of them.
//
// The key's purpose, algorithm and protection level are pinned as the key
// service's suite pins its own (A3), and the rest of the acceptance — the signer
// role, the key-set bucket, the four sign-in methods, the JWKS route and the
// expiry alert — is pinned here too, because each is a sentence of the plan's
// acceptance that a diff could otherwise falsify with every check green.
package identitysvc

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// terraformRoot is where this service's Terraform lives, relative to this
// package.
const terraformRoot = "terraform"

// tfFiles lists the root's readable Terraform, sorted, and fails on a file this
// suite cannot read: a sweep is only as good as the files it saw.
func tfFiles(t *testing.T) []string {
	t.Helper()
	var files, refused []string
	err := filepath.WalkDir(terraformRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// A provider data directory belongs outside the tree (the
			// `terraform` rule), and a state file is never committed.
			if p != terraformRoot && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(terraformRoot, p)
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
		t.Fatalf("walking %s: %v", terraformRoot, err)
	}
	for _, rel := range refused {
		t.Errorf("%s is a Terraform configuration file these tests cannot read: a `.tf.json` file writes the same resources in another syntax, an override file is merged over its directory last, and a variable file decides what the blocks below it mean (which key the service signs with, which role the grant names), so any of them can change what an apply does with no block below changing. Keep the root in plain HCL and in one form, with no variable file in it (A3)", rel)
	}
	slices.Sort(files)
	if len(files) == 0 {
		t.Fatalf("%s holds no .tf file", terraformRoot)
	}
	return files
}

// tfOverrides reports whether a path is one of the names Terraform merges over
// the other files of its directory.
func tfOverrides(rel string) bool {
	base := filepath.Base(rel)
	return base == "override.tf" || strings.HasSuffix(base, "_override.tf")
}

// tfVars reports whether a path is a variable file, every spelling of one:
// `terraform.tfvars` and `*.auto.tfvars`, which Terraform loads from a root's own
// directory without being asked, and a plain `something.tfvars`, which needs a
// `-var-file` on the command line.
func tfVars(rel string) bool {
	base := filepath.Base(rel)
	return strings.HasSuffix(base, ".tfvars") || strings.HasSuffix(base, ".tfvars.json")
}

// tfSource reads one file of the root with its comments removed: what these tests
// decide is what the configuration WRITES, and the prose above a resource is
// where a change is explained. All three comment forms HCL has are removed, so a
// line reading `# purpose = "ASYMMETRIC_SIGN"` beside a real
// `purpose = "ENCRYPT_DECRYPT"` is the second value this suite pins.
func tfSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(terraformRoot, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return tfStripComments(string(b))
}

// tfStripComments removes `#` and `//` to the end of their line, and `/* */`
// wherever it starts, while leaving quoted strings, heredocs and the line
// structure alone.
func tfStripComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		switch c := src[i]; {
		case c == '"':
			j, ok := tfSkipString(src, i)
			if !ok {
				b.WriteString(src[i:])
				return b.String()
			}
			b.WriteString(src[i:j])
			i = j
		case c == '<' && strings.HasPrefix(src[i:], "<<"):
			j, ok := tfSkipHeredoc(src, i)
			if !ok {
				b.WriteString(src[i:])
				return b.String()
			}
			b.WriteString(src[i:j])
			i = j
		case c == '#':
			i = tfToLineEnd(src, i)
		case c == '/' && strings.HasPrefix(src[i:], "//"):
			i = tfToLineEnd(src, i)
		case c == '/' && strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += 2 + end + 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// tfSkipString answers the index just past the closing quote of the string
// starting at i, and whether there was one: a backslash escapes the character
// after it, and an interpolation is read to its own closing brace, so a quote
// inside one does not end the string.
func tfSkipString(src string, i int) (int, bool) {
	for j := i + 1; j < len(src); {
		switch c := src[j]; {
		case c == '\\':
			j += 2
		case c == '"':
			return j + 1, true
		case c == '$' || c == '%':
			switch {
			case j+1 < len(src) && src[j+1] == c:
				j += 2
			case j+1 < len(src) && src[j+1] == '{':
				k, ok := tfSkipInterpolation(src, j+1)
				if !ok {
					return len(src), false
				}
				j = k
			default:
				j++
			}
		default:
			j++
		}
	}
	return len(src), false
}

// tfSkipInterpolation answers the index just past the brace closing the
// interpolation whose `{` is at i, and whether there was one.
func tfSkipInterpolation(src string, i int) (int, bool) {
	depth := 0
	for j := i; j < len(src); {
		switch c := src[j]; {
		case c == '"':
			k, ok := tfSkipString(src, j)
			if !ok {
				return len(src), false
			}
			j = k
		case c == '{' || c == '[' || c == '(':
			depth++
			j++
		case c == '}' || c == ']' || c == ')':
			depth--
			j++
			if depth == 0 {
				return j, true
			}
		default:
			j++
		}
	}
	return len(src), false
}

// tfSkipHeredoc answers the index just past the line that closes the heredoc
// starting at i, and whether there was one.
func tfSkipHeredoc(src string, i int) (int, bool) {
	j := i + 2
	if j < len(src) && src[j] == '-' {
		j++
	}
	start := j
	for j < len(src) && tfIdentChar(src[j]) {
		j++
	}
	if j == start {
		return 0, false
	}
	delim := src[start:j]
	for j < len(src) {
		k := strings.IndexByte(src[j:], '\n')
		if k < 0 {
			return len(src), false
		}
		line, next := src[j:j+k], j+k+1
		j = next
		if strings.TrimSpace(line) == delim {
			return j, true
		}
	}
	return len(src), false
}

// tfToLineEnd answers the index of the newline ending the line that holds i, so
// that the newline itself is kept.
func tfToLineEnd(src string, i int) int {
	if j := strings.IndexByte(src[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(src)
}

// tfBlocks answers every block in one file whose header starts with the given
// text. The cut is the next block header a root declares a name with, which is
// enough for a sweep over those blocks.
func tfBlocks(src, header string) []string {
	return tfBlocksUntil(src, header, []string{"\nresource \"", "\ndata \"", "\nvariable \"", "\noutput \"", "\nprovider \"", "\nterraform {"})
}

// tfBlocksUntil answers every block whose header starts with the given text and
// whose body ends at the first of the given markers. Both the header and the
// markers are found in CODE alone, so a header written inside a description is a
// sentence and not a block.
func tfBlocksUntil(src, header string, cuts []string) []string {
	code := tfCodeMask(src)
	var out []string
	for i := 0; ; {
		start := tfFindCode(src, code, header, i)
		if start < 0 {
			return out
		}
		end := len(src)
		for _, cut := range cuts {
			if k := tfFindCode(src, code, cut, start+len(header)); k >= 0 && k < end {
				end = k
			}
		}
		out = append(out, src[start:end])
		i = start + len(header)
	}
}

// tfCodeMask marks every byte of src that is CODE: the bytes inside a quoted
// string, a heredoc and a comment are not.
func tfCodeMask(src string) []bool {
	code := make([]bool, len(src))
	for i := 0; i < len(src); {
		switch c := src[i]; {
		case c == '"':
			j, ok := tfSkipString(src, i)
			if !ok {
				j = len(src)
			}
			i = j
		case c == '<' && strings.HasPrefix(src[i:], "<<"):
			j, ok := tfSkipHeredoc(src, i)
			if !ok {
				j = len(src)
			}
			i = j
		case c == '#':
			i = tfToLineEnd(src, i)
		case c == '/' && strings.HasPrefix(src[i:], "//"):
			i = tfToLineEnd(src, i)
		case c == '/' && strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				i = len(src)
			} else {
				i += 2 + end + 2
			}
		default:
			code[i] = true
			i++
		}
	}
	return code
}

// tfFindCode answers the index of the next occurrence of needle in src at or
// after from whose first byte is code, or -1 for none.
func tfFindCode(src string, code []bool, needle string, from int) int {
	for i := from; i+len(needle) <= len(src); {
		j := strings.Index(src[i:], needle)
		if j < 0 {
			return -1
		}
		if k := i + j; code[k] {
			return k
		} else {
			i = k + 1
		}
	}
	return -1
}

// tfHeader answers a block's first line — its kind and its name.
func tfHeader(block string) string {
	if i := strings.Index(block, "{"); i >= 0 {
		block = block[:i]
	}
	return strings.TrimSpace(block)
}

// tfBlock answers the one block a root declares under the given header,
// whichever of the root's files holds it. Terraform refuses a resource declared
// twice in one directory, so a root has exactly one, and a check that read one
// file would read a file the block had been moved out of.
func tfBlock(t *testing.T, header string) string {
	t.Helper()
	var found []string
	for _, rel := range tfFiles(t) {
		src := tfSource(t, rel)
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
		t.Fatalf("the root declares no %s: the block this check is about is gone or renamed", header)
	default:
		t.Fatalf("the root declares %s %d times: Terraform refuses a resource declared twice in one directory, so this tree does not validate", header, len(found))
	}
	return ""
}

// tfBlockBody answers a block's text between its own braces, or false for a
// block whose braces this reader cannot see.
func tfBlockBody(block string) (string, bool) {
	i := strings.IndexByte(block, '{')
	if i < 0 {
		return "", false
	}
	body := strings.TrimRight(block[i+1:], " \t\r\n")
	if !strings.HasSuffix(body, "}") {
		return "", false
	}
	return body[:len(body)-1], true
}

// tfAttr answers the value written for one of a block's own top-level
// attributes: `attr` is matched at word boundaries and only outside quoted
// strings, heredocs and nested blocks, so a `default = ` inside a description and
// a `default` written one level down are none of them the attribute.
func tfAttr(block, name string) (string, bool) {
	body, ok := tfBlockBody(block)
	if !ok {
		return "", false
	}
	depth := 0
	for i := 0; i < len(body); {
		switch c := body[i]; {
		case c == '"':
			j, ok := tfSkipString(body, i)
			if !ok {
				return "", false
			}
			i = j
		case c == '<' && strings.HasPrefix(body[i:], "<<"):
			j, ok := tfSkipHeredoc(body, i)
			if !ok {
				return "", false
			}
			i = j
		case c == '{' || c == '[' || c == '(':
			depth++
			i++
		case c == '}' || c == ']' || c == ')':
			depth--
			i++
		case depth == 0 && tfIdentStart(c):
			j := i
			for j < len(body) && tfIdentChar(body[j]) {
				j++
			}
			if word := body[i:j]; word == name {
				k := j
				for k < len(body) && (body[k] == ' ' || body[k] == '\t' || body[k] == '\n' || body[k] == '\r') {
					k++
				}
				if k < len(body) && body[k] == '=' {
					return tfValue(body, k+1), true
				}
			}
			i = j
		default:
			i++
		}
	}
	return "", false
}

// tfValue answers the value starting at i: a quoted string with its quotes, a
// list or a map as the whole bracket, and anything else as the rest of its line.
func tfValue(body string, i int) string {
	for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
		i++
	}
	if i < len(body) && body[i] == '"' {
		if j, ok := tfSkipString(body, i); ok {
			return body[i:j]
		}
	}
	if i < len(body) && (body[i] == '[' || body[i] == '{') {
		if j, ok := tfSkipBrackets(body, i); ok {
			return body[i:j]
		}
	}
	return strings.TrimSpace(body[i:tfToLineEnd(body, i)])
}

// tfSkipBrackets answers the index just past the bracket that closes the opener
// at i, and whether there was one.
func tfSkipBrackets(src string, i int) (int, bool) {
	depth := 0
	for j := i; j < len(src); {
		switch c := src[j]; {
		case c == '"':
			k, ok := tfSkipString(src, j)
			if !ok {
				return len(src), false
			}
			j = k
		case c == '<' && strings.HasPrefix(src[j:], "<<"):
			k, ok := tfSkipHeredoc(src, j)
			if !ok {
				return len(src), false
			}
			j = k
		case c == '[' || c == '{' || c == '(':
			depth++
			j++
		case c == ']' || c == '}' || c == ')':
			depth--
			j++
			if depth == 0 {
				return j, true
			}
		default:
			j++
		}
	}
	return len(src), false
}

// tfNestedBlocks answers every block nested inside block under the given name —
// `version_template`, `lifecycle`, `email`, `env` — read as the brackets it
// writes rather than searched for in the block's text: a `description` saying
// "protection_level" somewhere else in the key would satisfy a search and hold
// nothing.
func tfNestedBlocks(block, name string) []string {
	body, ok := tfBlockBody(block)
	if !ok {
		return nil
	}
	code := tfCodeMask(body)
	var out []string
	for i := 0; i < len(body); {
		j := tfFindCode(body, code, name, i)
		if j < 0 {
			return out
		}
		k := j + len(name)
		switch {
		case k < len(body) && tfIdentChar(body[k]):
			i = k
		case j > 0 && tfIdentChar(body[j-1]):
			i = k
		default:
			for k < len(body) && (body[k] == ' ' || body[k] == '\t' || body[k] == '\n' || body[k] == '\r') {
				k++
			}
			if k < len(body) && body[k] == '{' {
				end, ok := tfSkipBrackets(body, k)
				if !ok {
					return out
				}
				out = append(out, body[j:end])
				i = end
			} else {
				i = k + 1
			}
		}
	}
	return out
}

// tfNested answers the one nested block of the given name, and fails the test
// when there is none: a block whose braces the reader cannot see is a value this
// suite would otherwise hold nothing about.
func tfNested(t *testing.T, block, name string) string {
	t.Helper()
	got := tfNestedBlocks(block, name)
	if len(got) == 0 {
		t.Fatalf("this block writes no %s block:\n%s\nthe values this check is about are written one level down, and without the block the resource is created at the provider's defaults", name, strings.TrimSpace(block))
	}
	return got[0]
}

// tfCompact removes every space, tab and newline: `member = "allUsers"` and
// `member="allUsers"` are one grant written twice, and a check that read the
// first spelling only would let the second through.
func tfCompact(src string) string {
	return strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(src)
}

// tfWant is one attribute and the literal it is held to.
type tfWant struct {
	attr, value string
}

// tfAttrsAre checks a block's attributes one by one and reports each one that
// does not write what it is held to, with what it wrote instead.
func tfAttrsAre(t *testing.T, block, where string, want []tfWant, why string) {
	t.Helper()
	for _, w := range want {
		got, ok := tfAttr(block, w.attr)
		if ok && tfCompact(got) == tfCompact(w.value) {
			continue
		}
		if got == "" {
			got = "nothing"
		}
		t.Errorf("%s does not write %s = %s (it writes %s): %s", where, w.attr, w.value, got, why)
	}
}

// tfVarDefault answers a variable block's default as the quoted literal it is
// written with, so that `default = "30"` and `default="30"` are one value. A
// variable the root does not declare, or declares without a default, reads as the
// empty string.
func tfVarDefault(t *testing.T, name string) string {
	t.Helper()
	header := `variable "` + name + `"`
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocksUntil(tfSource(t, rel), header, []string{"\nvariable \"", "\nresource \"", "\ndata \"", "\noutput \"", "\nprovider \"", "\nterraform {"}) {
			value, _ := tfAttr(block, "default")
			return value
		}
	}
	t.Fatalf("the root declares no %s variable: a value the blocks below read is decided by this default, and a variable that is gone is a rule that would pass by reading nothing", name)
	return ""
}

// tfEnv answers a container block's environment as a name-to-value map, so that
// a check reads what the service is handed rather than the order a set writes it
// in.
func tfEnv(t *testing.T, container string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, env := range tfNestedBlocks(container, "env") {
		name, _ := tfAttr(env, "name")
		value, _ := tfAttr(env, "value")
		if name == "" {
			t.Fatalf("an env block in the service's container writes no name:\n%s", strings.TrimSpace(env))
		}
		out[strings.Trim(name, `"`)] = value
	}
	if len(out) == 0 {
		t.Fatalf("the service's container writes no env block:\n%s\nthe signer reads the key it signs with, the bucket the signed key set is in and the route it serves from, and a revision with none of them refuses at startup", strings.TrimSpace(container))
	}
	return out
}

// tfGrants reports whether a block hands an identity a role, whatever the block
// is called. It is deliberately loose — an IAM resource type, or an attribute a
// grant is written with — and it is loose in the safe direction: the sweep's
// other half is a list of the grants this root is allowed to hold, so a block
// the sweep thinks grants something and finds unlisted fails, while being wrong
// about a block that grants nothing costs only a message. The attribute test is
// on the compacted body, because `role="x"` and `role = "x"` are one grant.
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

// tfList renders the grants this root is allowed to hold, one per line and
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

// tfIdentStart reports whether c can begin an HCL attribute name.
func tfIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

// tfIdentChar reports whether c can continue one.
func tfIdentChar(c byte) bool {
	return tfIdentStart(c) || c >= '0' && c <= '9' || c == '-' || c == '.'
}

// TestInfraTheRootIsOneFormOfConfiguration: the root is plain HCL in one form —
// no `.tf.json`, no override file, no variable file — and its three files are
// the ones the plan names. A second form is a place the key's algorithm, the
// signer role or a sign-in provider is decided where no check above changes.
func TestInfraTheRootIsOneFormOfConfiguration(t *testing.T) {
	want := []string{"auth.tf", "keyring.tf", "main.tf"}
	if got := tfFiles(t); !slices.Equal(got, want) {
		t.Errorf("the root's Terraform is %v, want %v: A3 creates these three files, and a file added to this root later — K1's pairedlist.tf is the next the plan names — is added to this list in the same commit, so that the root's whole shape stays something this suite states (A3)", got, want)
	}
}

// TestInfraTheSigningKeyIsNonExportable: the private half of the assertion
// signing key exists inside Cloud KMS and nowhere else — never a file, never a
// service's memory, never a machine the owner or an agent holds (D2792, O7,
// Check 730).
//
// The mutation this fails on, and the plan's red step: the key's purpose or its
// algorithm moving off the pair the signer was built for, a second key resource
// beside it, a protection level the algorithm is not offered at, or a route by
// which material generated outside the store could reach the key ring.
func TestInfraTheSigningKeyIsNonExportable(t *testing.T) {
	keys := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if strings.Contains(tfHeader(block), `"google_kms_crypto_key"`) {
				keys++
			}
		}
	}
	if keys != 1 {
		t.Errorf("the root declares %d KMS crypto keys, want exactly 1: the signer role is granted on the key ring, so a second key in it is one the signer signs with and a key whose algorithm, protection level and lifecycle no check here reads (D2792, O7)", keys)
	}

	key := tfBlock(t, `resource "google_kms_crypto_key" "signing"`)
	tfAttrsAre(t, key, "the assertion signing key", []tfWant{
		{"purpose", `"ASYMMETRIC_SIGN"`},
	}, "the assertion is signed with an asymmetric key and never with a symmetric one, so a purpose that only encrypts and decrypts is a signer that cannot sign (D2792)")

	version := tfNested(t, key, "version_template")
	tfAttrsAre(t, version, "the signing key's version_template", []tfWant{
		{"algorithm", `"EC_SIGN_ED25519"`},
		{"protection_level", `"SOFTWARE"`},
	}, "D2792 fixes Ed25519 as the algorithm the assertion verifier verifies, and Cloud KMS offers it at SOFTWARE and not at HSM: an algorithm or a level moved off this pair is an apply that creates a key the verifier refuses")

	lifecycle := tfNested(t, key, "lifecycle")
	tfAttrsAre(t, lifecycle, "the signing key's lifecycle", []tfWant{
		{"prevent_destroy", "true"},
	}, "every install holds a public half of this key, so a destroy takes the key id out of the ring and stops them verifying with no revocation to notice it by; rotation is a new key id, never an apply's side effect")

	ring := tfBlock(t, `resource "google_kms_key_ring" "signing"`)
	tfAttrsAre(t, ring, "the signing key ring", []tfWant{
		{"project", "var.project"},
		{"name", "var.keyring"},
		{"location", "var.region"},
	}, "the key lives in the ring this root creates in the identity project, and both the name and the location come from the variables so that no second place writes the ring down")

	for _, rel := range tfFiles(t) {
		src := tfSource(t, rel)
		for _, route := range []string{
			"google_kms_key_ring_import_job",
			"import_only",
			"skip_initial_version_creation",
			"external_key_uri",
			"tls_private_key",
			"google_secret_manager_secret_version",
			`"-----BEGIN`,
		} {
			if strings.Contains(src, route) {
				t.Errorf("%s writes %s: the signing key's private half is created inside the key ring and nowhere else — no import, no external store, no file (D2792, O7, .claude/rules/ee.md)", rel, route)
			}
		}
	}
}

// TestInfraTheSignerRoleIsTheNarrowestThatSigns: signing and publishing the
// public keys are the whole of what the signer's account may do with the key
// ring, and the account is the one the service RUNS as — a service running as
// another identity is that identity (A3, .claude/rules/terraform.md).
//
// The mutations this fails on: a permission besides the two, a predefined role
// (cloudkms.signerVerifier also verifies; owner and editor reach the ring), a
// second member on the grant, a second grant anywhere in the root — under
// another header, another IAM resource type, a `user:` member, or a role named
// by a variable whose default is a predefined one — and the service's template
// naming another account.
func TestInfraTheSignerRoleIsTheNarrowestThatSigns(t *testing.T) {
	role := tfBlock(t, `resource "google_project_iam_custom_role" "signer"`)
	tfAttrsAre(t, role, "the signer role", []tfWant{
		{"role_id", "var.signer_role"},
		{"permissions", `["cloudkms.cryptoKeyVersions.useToSign", "cloudkms.cryptoKeyVersions.viewPublicKey"]`},
	}, "the signer signs with the key and publishes its public half as the JWKS, and it does nothing else: a predefined role carries more (signerVerifier also verifies), and a third permission here is a reach of the ring the plan never asked for (A3, O7)")

	grant := tfBlock(t, `resource "google_kms_key_ring_iam_member" "signer_signs"`)
	tfAttrsAre(t, grant, "the signer grant", []tfWant{
		{"member", "google_service_account.signer.member"},
		{"role", "google_project_iam_custom_role.signer.id"},
		{"key_ring_id", "google_kms_key_ring.signing.id"},
	}, "one account holds the role, it is the service's own, and the grant is made on the ring this root creates — a second member here is a second identity that can mint an assertion for any member of any project (A3, D2792)")

	service := tfBlock(t, `resource "google_cloud_run_v2_service" "signer"`)
	template := tfNested(t, service, "template")
	tfAttrsAre(t, template, "the signer service", []tfWant{
		{"service_account", "google_service_account.signer.email"},
	}, "the identity a Cloud Run service runs as is the identity it IS, so a template naming another account holds the signer role from wherever that account's code runs (A3)")

	// Every block in this root that grants anything at all is one of the four
	// grants the assertion signer needs, and the list is the whole of it. A
	// count of one resource type would miss a second holder written another way
	// — the signer role granted with google_kms_key_ring_iam_binding or an
	// `_iam_policy` instead of `_iam_member`, a second custom role, a plain
	// `user:` added beside the signer's account, a grant whose role is a
	// variable defaulting to a predefined role — so the discriminator is "this
	// block grants a role", whatever it is called, and the answer for a grant
	// nobody has reviewed is this list's absence. Each of the four is read as
	// the attributes it writes above, because a header is not a role.
	sanctioned := map[string]string{
		`resource "google_project_iam_custom_role" "signer"`:                "the signer role itself, and the whole of what signing takes",
		`resource "google_kms_key_ring_iam_member" "signer_signs"`:          "its one grant, to the signer's own account",
		`resource "google_storage_bucket_iam_member" "signer_reads"`:        "the service's read of the signed key set, which holds no key",
		`resource "google_cloud_run_v2_service_iam_member" "signer_public"`: "the signer's public invoker, which reaches no key at all",
	}

	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			header := tfHeader(block)
			if !tfGrants(header, tfCompact(block)) {
				continue
			}
			if _, ok := sanctioned[header]; !ok {
				t.Errorf("%s holds a grant this root is not allowed:\n%s\nthe signer role is the only route to useToSign and it reaches one account, the service's own, so that a reader of the plan sees every identity that can mint an assertion (A3, D2792, O7). The whole list of grants this root may hold is:\n%s", rel, strings.TrimSpace(block), tfList(sanctioned))
			}
		}
	}
}

// TestInfraTheKeysetBucketIsOwnerWrittenAndServiceRead: the signed key set lives
// in one bucket that only the owner writes and the service reads (O7, A3). The
// owner's own credentials are project-level and are not a grant this root writes,
// so the bucket's IAM is exactly one read and no write at all.
func TestInfraTheKeysetBucketIsOwnerWrittenAndServiceRead(t *testing.T) {
	bucket := tfBlock(t, `resource "google_storage_bucket" "keyset"`)
	tfAttrsAre(t, bucket, "the key-set bucket", []tfWant{
		{"project", "var.project"},
		{"location", "var.region"},
		{"uniform_bucket_level_access", "true"},
		{"public_access_prevention", `"enforced"`},
	}, "the signed key set is what every install fetches keys from, so the bucket is uniform-access, never public, and in the project and region this root is applied to (O7)")

	versioning := tfNested(t, bucket, "versioning")
	tfAttrsAre(t, versioning, "the key-set bucket's versioning", []tfWant{
		{"enabled", "true"},
	}, "the document a release pins its `seq` floor against is one object, and a versioned bucket is what lets a bad write be seen and put back")

	reads := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			header := tfHeader(block)
			if !strings.Contains(header, "google_storage_bucket_iam") {
				continue
			}
			reads++
			if header != `resource "google_storage_bucket_iam_member" "signer_reads"` {
				t.Errorf("%s holds a second grant on the key-set bucket:\n%s\nthe owner writes the signed document with their own credentials and the service reads it, so the bucket's IAM is one member (A3, O7)", rel, strings.TrimSpace(block))
			}
		}
	}
	if reads != 1 {
		t.Errorf("the root declares %d grants on the key-set bucket, want exactly 1: the service's read (A3, O7)", reads)
	}
	grant := tfBlock(t, `resource "google_storage_bucket_iam_member" "signer_reads"`)
	tfAttrsAre(t, grant, "the key-set read", []tfWant{
		{"bucket", "google_storage_bucket.keyset.name"},
		{"role", `"roles/storage.objectViewer"`},
		{"member", "google_service_account.signer.member"},
	}, "the service reads the signed key set and writes nothing: objectViewer is the read and no write, and the member is the account the service runs as (A3, O7)")

	for _, rel := range tfFiles(t) {
		compact := tfCompact(tfSource(t, rel))
		for _, role := range []string{
			`"roles/storage.objectCreator"`,
			`"roles/storage.objectAdmin"`,
			`"roles/storage.admin"`,
			`"roles/storage.legacyBucketWriter"`,
		} {
			if strings.Contains(compact, role) {
				t.Errorf("%s writes %s: only the owner writes the signed key set, and a write role here is a service — or a deployer — able to publish a key set of its own (O7)", rel, role)
			}
		}
	}
}

// TestInfraTheSignInMethodsAreTheFourTheSpecNames: Google, Microsoft and Apple
// are configured as Firebase Auth providers, email and password is on and email
// link sign-in is not, and the other two methods Identity Platform offers are
// explicitly off (spec, D2781, D2793).
//
// Email VERIFICATION is not a field of this resource: Identity Platform sends
// the verification mail from its own template (owner work, F28) and the signer
// refuses an assertion for a password identity whose `email_verified` is false
// (A1), so the two halves are held there and this one holds the provider.
func TestInfraTheSignInMethodsAreTheFourTheSpecNames(t *testing.T) {
	config := tfBlock(t, `resource "google_identity_platform_config" "identity"`)
	signIn := tfNested(t, config, "sign_in")
	email := tfNested(t, signIn, "email")
	tfAttrsAre(t, email, "the email sign-in method", []tfWant{
		{"enabled", "true"},
		{"password_required", "true"},
	}, "the spec asks for email and password, so the method is on and the passwordless email-link flow is off: an identity that signs in by link and never set a password is one the spec's 'email and password' does not name")

	for _, off := range []string{"anonymous", "phone_number"} {
		block := tfNested(t, signIn, off)
		tfAttrsAre(t, block, "the "+off+" sign-in method", []tfWant{
			{"enabled", "false"},
		}, "the spec names Google, Microsoft, Apple and email with a password, and a method left to the API's default is one nobody chose: an anonymous identity is not a member of any project and a phone number is not an email a roster can carry")
	}

	var idps []string
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_identity_platform_default_supported_idp_config"`) {
				continue
			}
			idp, _ := tfAttr(block, "idp_id")
			idps = append(idps, strings.Trim(idp, `"`))
			tfAttrsAre(t, block, "the "+idp+" provider", []tfWant{
				{"enabled", "true"},
			}, "a provider the plan names and leaves disabled is a sign-in the app offers and Identity Platform refuses")
			for _, secret := range []string{"client_id", "client_secret"} {
				value, _ := tfAttr(block, secret)
				if !strings.HasPrefix(value, "var.") {
					t.Errorf("the %s provider writes %s = %s, want a variable: an OAuth client secret is not a value this repository holds, and a literal here would be a credential in the tree (D2793, .claude/rules/ee.md)", idp, secret, value)
				}
			}
		}
	}
	slices.Sort(idps)
	if want := []string{"apple.com", "google.com", "microsoft.com"}; !slices.Equal(idps, want) {
		t.Errorf("the identity project's configured sign-in providers are %v, want %v: the spec names Google SSO, Microsoft (Entra) SSO and Sign in with Apple, and each is a provider configuration of its own (spec, F28, Check 731)", idps, want)
	}
}

// TestInfraTheKeysetExpiryIsAlerted: the identity project alerts 30 days before
// the newest signed key set expires (O7, O18). Terraform cannot know when the
// owner signed the set, so the root hands the service the window and the alert
// matches the warning the service writes when it is inside it.
func TestInfraTheKeysetExpiryIsAlerted(t *testing.T) {
	if got := tfVarDefault(t, "keyset_alert_days"); got != "30" {
		t.Errorf("keyset_alert_days defaults to %s, want 30: a signed set is valid 180 days (O18) and the owner needs the 30 days of warning to run the signing command before it lapses (O7)", got)
	}

	channel := tfBlock(t, `resource "google_monitoring_notification_channel" "email"`)
	tfAttrsAre(t, channel, "the alert channel", []tfWant{
		{"type", `"email"`},
		{"labels", "{email_address=var.alert_email}"},
	}, "an alert policy with no channel notifies nobody, and the project's own alert address is the one the installation already uses: the address of an `email` channel is its `email_address` label, so a label spelled any other way is a channel created with no address — and a policy that fires into one reaches nobody, which is the whole of what the 30-day warning is for (O7, O18)")

	policy := tfBlock(t, `resource "google_monitoring_alert_policy" "keyset_expiry"`)
	condition := tfNested(t, policy, "conditions")
	matched := tfNested(t, condition, "condition_matched_log")
	filter, _ := tfAttr(matched, "filter")
	// The filter is a quoted HCL string, so every `"` inside it is written `\"`
	// in the source; the reading below is of the value the API is handed, which
	// is the one the log entry has to match.
	filter = strings.ReplaceAll(filter, `\"`, `"`)
	for _, want := range []string{
		`resource.type="cloud_run_revision"`,
		`resource.labels.service_name="${var.service}"`,
		`jsonPayload.event="keyset_expiring"`,
	} {
		if !strings.Contains(filter, want) {
			t.Errorf("the expiry alert's filter does not name %s (it is %s): the warning is what the signer writes when the newest set is inside the window, and an alert matching anything else fires on a log nobody writes or misses the one that matters (O7, O18)", want, filter)
		}
	}
	channels, _ := tfAttr(policy, "notification_channels")
	if !strings.Contains(channels, "google_monitoring_notification_channel.email") {
		t.Errorf("the expiry alert notifies %s, want the email channel this root creates: an alert nobody receives is a set that lapses unnoticed (O7)", channels)
	}
}

// TestInfraTheServiceSignsWithThisKeyAndServesTheJWKS: the service's whole
// configuration is the key it signs with, the bucket and object the signed set is
// read from, the path it serves the JWKS on and the alert window — and it is
// reachable from a phone, because the app is what asks for an assertion (spec
// step 3; the code verifies the Firebase ID token, A1).
func TestInfraTheServiceSignsWithThisKeyAndServesTheJWKS(t *testing.T) {
	service := tfBlock(t, `resource "google_cloud_run_v2_service" "signer"`)
	template := tfNested(t, service, "template")
	container := tfNested(t, template, "containers")
	tfAttrsAre(t, container, "the signer's container", []tfWant{
		{"image", "var.image"},
	}, "the image is the one the release builds and the owner deploys, so it is an input rather than a value written here")

	want := map[string]string{
		"FUGARO_SIGNING_KEY":       `"projects/${var.project}/locations/${var.region}/keyRings/${var.keyring}/cryptoKeys/${var.key}/cryptoVersions/1"`,
		"FUGARO_KEYSET_BUCKET":     "google_storage_bucket.keyset.name",
		"FUGARO_KEYSET_OBJECT":     "var.keyset_object",
		"FUGARO_JWKS_PATH":         "var.jwks_path",
		"FUGARO_KEYSET_ALERT_DAYS": "var.keyset_alert_days",
	}
	env := tfEnv(t, container)
	for name, value := range want {
		got, ok := env[name]
		if !ok {
			t.Errorf("the signer's container sets no %s: the program reads it before it serves, and a revision missing it refuses at startup", name)
			continue
		}
		if tfCompact(got) != tfCompact(value) {
			t.Errorf("the signer's container sets %s = %s, want %s: the value is what the program signs with, reads and serves from, and a literal written here instead of the resource is a second spelling of it (A3, A2)", name, got, value)
		}
	}
	for name := range env {
		if _, ok := want[name]; !ok {
			t.Errorf("the signer's container sets %s, which no check here holds: the service's whole configuration is the five values above, and a sixth is a value A1 reads that nothing in this suite pins", name)
		}
	}

	tfAttrsAre(t, service, "the signer service", []tfWant{
		{"ingress", `"INGRESS_TRAFFIC_ALL"`},
		{"invoker_iam_disabled", "false"},
	}, "the app asks this service for an assertion (spec step 3), so it is reached over the internet and Cloud Run checks IAM rather than serving every caller: the Firebase ID token the code verifies is the control (A1)")

	public := tfBlock(t, `resource "google_cloud_run_v2_service_iam_member" "signer_public"`)
	tfAttrsAre(t, public, "the signer's invoker", []tfWant{
		{"member", `"allUsers"`},
		{"role", `"roles/run.invoker"`},
		{"name", "google_cloud_run_v2_service.signer.name"},
	}, "a phone cannot be handed an IAM principal, so the invoker is allUsers and the service's own code is what refuses a caller without a valid Firebase ID token (A1, spec step 3)")

	publicGrants := 0
	for _, rel := range tfFiles(t) {
		publicGrants += strings.Count(tfCompact(tfSource(t, rel)), `"allUsers"`)
	}
	if publicGrants != 1 {
		t.Errorf("the root grants allUsers %d times, want exactly 1: the signer is the one public surface here, and a second one — on the key-set bucket above all — is a reach the plan never asked for", publicGrants)
	}

	outputs := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `output "`) {
			outputs++
			if tfHeader(block) != `output "jwks_url"` {
				t.Errorf("%s declares the output %s: the JWKS URL is the one value P8 pins and P1 fetches, and a second output here is a value nobody reads", rel, tfHeader(block))
			}
		}
	}
	if outputs != 1 {
		t.Errorf("the root declares %d outputs, want exactly 1 (jwks_url): the JWKS URL is what Check 730's apply prints for P8 to pin", outputs)
	}
	url := tfBlock(t, `output "jwks_url"`)
	value, _ := tfAttr(url, "value")
	if !strings.Contains(tfCompact(value), "google_cloud_run_v2_service.signer.uri") || !strings.Contains(value, "var.jwks_path") {
		t.Errorf("jwks_url is %s, want the service's own URI with the path the service serves on: a URL written here by hand is one nobody applied (A2, P8)", value)
	}
}
