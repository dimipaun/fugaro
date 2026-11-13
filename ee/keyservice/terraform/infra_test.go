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
// no other: a `.tf.json` file, an override file, a variable file and a module
// block are each a place a grant can be written — or decided — where nothing
// above changes, and the checks here see none of the four.
// TestTheTestsReadEveryFileOfTheTree refuses all of them.
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
	"path"
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
//
// All three comment forms HCL has are removed, because a comment is invisible to
// Terraform and reading one as a value is a hole in the safe direction: with only
// `#` removed, a line reading `//default = "fugaroKeySigner"` beside a real
// `default = "editor"` would be the value this suite pinned while the apply read
// the other one — the same fault as a variable file, one level down.
func tfSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return tfStripComments(string(b))
}

// tfStripComments removes `#` and `//` to the end of their line, and `/* */`
// wherever it starts, while leaving quoted strings, heredocs and the line
// structure alone: a `#` inside a string or a heredoc is text, and a whole-line
// comment leaves its newline behind so that the block cuts every sweep makes
// still hold.
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
// after it, so a quote inside a description does not end the string, and an
// interpolation — `${`, or `%{` for a template directive — is read to its own
// closing brace, so a quote inside one does not end it either.
//
// The second half is not decoration. `"a${"\""}"` is a valid, fmt-clean string
// whose interpolation holds a quote; a reader that ended the string at that
// quote read the rest of the file as the inside of strings, so every block
// header below it was text and no sweep found one — a second signer grant
// written under such a line passed every check in this file.
func tfSkipString(src string, i int) (int, bool) {
	for j := i + 1; j < len(src); {
		switch c := src[j]; {
		case c == '\\':
			j += 2
		case c == '"':
			return j + 1, true
		case c == '$' || c == '%':
			switch {
			// `$${` and `%%{` are how a literal `${`/`%{` is spelled, so they
			// open nothing.
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
// interpolation whose `{` is at i, and whether there was one. What it holds is an
// HCL expression: its brackets nest, and it may carry a quoted string of its own
// — which may itself interpolate — so it is read with the same readers as the
// file around it.
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
// starting at i, and whether there was one. A heredoc — `<<EOT`, or `<<-EOT`
// whose closing marker may be indented — is a string in another spelling, and its
// text is not configuration: without this, a description written as a heredoc
// carrying the line `default = "fugaroKeySigner"` reads as a default, which is
// the same text-read-as-a-value fault as a comment, one form further on.
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
// text, so that a sweep covers a resource a later change adds rather than the
// one name it was written for. The cut is the next block header of either kind a
// root declares a name with, which is enough for a sweep over those blocks, and
// no block is reported twice.
func tfBlocks(src, header string) []string {
	return tfBlocksUntil(src, header, []string{"\nresource \"", "\ndata \""})
}

// tfBlocksUntil answers every block whose header starts with the given text and
// whose body ends at the first of the given markers. A marker is one of
// Terraform's own block kinds at column zero — `variable`, `resource`, `output`,
// `provider`, `terraform` — which is where a block ends in every file of these
// roots, so a caller that reads a `variable` block names the kinds that can follow
// it rather than cutting at an attribute the block itself writes.
//
// Both the header and the markers are found in CODE alone (tfCodeMask): a
// header written inside a description — a heredoc or a quoted string carrying
// the text `variable "signer_role" { default = "fugaroKeySigner" }` — is a
// sentence, not a block, and reading it as one is not a harmless extra: the
// sweep keys what it finds by root, so a decoy in a later file of a root answers
// for the block that root really declares, and the whole rule passes while the
// block says something else.
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

// tfCodeMask marks every byte of src that is CODE. The bytes inside a quoted
// string, a heredoc and a comment are not: they are text a person or a tool
// wrote, and no block begins or ends in the middle of one.
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

// tfHeader answers a block's first line — its kind and its name.
func tfHeader(block string) string {
	if i := strings.Index(block, "{"); i >= 0 {
		block = block[:i]
	}
	return strings.TrimSpace(block)
}

// tfNames names a block by the root that holds it and its header, which is how
// every allow-list here is keyed. A header alone is not a block's identity:
// Terraform refuses a resource declared twice in one directory, but the same
// header in a SECOND root is a second resource, and one worth reviewing — the
// signer role granted to a `user:` in keyring/ is a holder of it as surely as
// one granted in services/, and a list keyed by the header alone reads the two
// as the same line and inspects neither.
func tfNames(root, header string) string {
	return root + "/ " + header
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

// tfList renders a map of the blocks this service may hold a grant in, one per
// line and sorted, so that a failure names the whole list rather than only the
// block it refused: the point of the list is that a person reads it.
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
// syntax, an override file (`override.tf`, `main_override.tf`, either of them in
// JSON) is merged over the rest of its directory last, so it can add a grant to
// the key ring or flip a service's IAM without any block below changing, and a
// variable file (`terraform.tfvars`, `*.auto.tfvars`, either of them in JSON)
// decides what the blocks below it MEAN: `signer_role` names the role the signer
// grant mentions and `keyring` names the ring that grant is made on, so a
// `terraform.tfvars` reading `signer_role = "editor"` hands the minter Editor
// while `role = "projects/${var.project}/roles/${var.signer_role}"` still reads as
// written. Terraform loads those two spellings out of a root's own directory
// without being asked, which is why a variable file is refused rather than
// ignored — and a `-var` or a `-var-file` on the owner's command line is not
// configuration this tree holds (S3.7, S3.8), so it is beyond anything here.
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
		case strings.HasSuffix(rel, ".tf.json"), tfOverrides(rel), tfVars(rel):
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

// tfVars reports whether a path is a variable file, every spelling of one:
// `terraform.tfvars` and `*.auto.tfvars`, which Terraform loads from a root's own
// directory without being asked, and a plain `something.tfvars` (or any of the
// three in JSON), which needs a `-var-file` on the command line. All four are
// refused for the same reason: a variable decides what the blocks below it mean —
// which role the signer grant names, which ring it is made on, which two key ids
// exist — and this suite reads blocks, not variable values, so a tree holding one
// is a tree it has half-read.
func tfVars(rel string) bool {
	base := filepath.Base(rel)
	return strings.HasSuffix(base, ".tfvars") || strings.HasSuffix(base, ".tfvars.json")
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
		t.Errorf("%s is a Terraform configuration file these tests cannot read: a `.tf.json` file writes the same resources in another syntax, an override file is merged over its directory last, and a variable file decides what the blocks below it mean (`signer_role` names the role a grant mentions), so any of them can grant a role, flip a service's IAM or widen the signer role without any block below changing. Keep the root in plain HCL and in one form, with no variable file in it, or teach these tests to read the form (S3.5)", rel)
	}
	return files
}

// tfStrayRoots answers the files of a listing that live in no root of this
// service. A root is a whole configuration — its own provider, its own state,
// its own resources — and the checks here walk the two there are, so Terraform
// under a third one is infrastructure nobody reads: the signer role granted to
// a `user:` in a root a later change adds is a holder of it no list above
// inspects, which is exactly how the list keyed by a header alone went wrong.
func tfStrayRoots(files []string) []string {
	var out []string
	for _, rel := range files {
		switch path.Dir(rel) {
		case keyringRoot, servicesRoot:
		default:
			out = append(out, rel)
		}
	}
	return out
}

// tfFiles lists the whole tree's readable Terraform, so that a sweep covers a
// file a later change adds as well as the two roots there are today — and
// refuses a file in a root that is not one of them, because the sweeps are
// written against the two and a third is a configuration they never enter.
func tfFiles(t *testing.T) []string {
	t.Helper()
	files := tfTerraform(t, ".")
	if len(files) < 6 {
		t.Fatalf("found %d .tf files: the walk did not see both roots, and a sweep over a tree it half-read proves nothing", len(files))
	}
	if stray := tfStrayRoots(files); len(stray) != 0 {
		t.Errorf("this tree holds Terraform in %v, outside %s/ and %s/: a root is a whole configuration, and every check over this tree walks those two — the signer role granted to another identity in a third root is a holder of it none of them reads. Keep the configuration in the two roots, or teach these tests to walk the new one (S3.5)", stray, keyringRoot, servicesRoot)
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
//
// Both kinds of block that declare a name are swept, `resource` and `data`,
// because the checks here read the key ring's two spellings through it: keyring/
// CREATES the ring and services/ READS it, and a sweep that saw only resources
// would answer "no such block" for the half the signer grant hangs off.
func tfRootBlock(t *testing.T, root, header string) string {
	t.Helper()
	var found []string
	for _, rel := range tfRootFiles(t, root) {
		src := tfSource(t, filepath.Join(root, rel))
		for _, kind := range tfBlockKinds {
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
		t.Fatalf("%s declares no %s: the block this check is about is gone or renamed", root, header)
	default:
		t.Fatalf("%s declares %s %d times: Terraform refuses a resource declared twice in one directory, so this tree does not validate", root, header, len(found))
	}
	return ""
}

// tfBlockKinds are the two kinds of block a root declares a name with: the
// resources it creates and the data sources it reads. A check about what one of
// them writes asks for the block by header, and the header carries the kind.
var tfBlockKinds = []string{`resource "`, `data "`}

// tfWrite writes one file of a scratch tree, so that a case reads as the tree it
// builds rather than as three calls to the os package.
func tfWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

// tfSignerDefaults are the two variables the signer grant is decided by, and the
// literal each root has to give them. `services/`'s grant names
// `var.signer_role` and its data source names `var.keyring`, so neither value is
// in the block a sweep reads: they are the DECLARED DEFAULTS in the two roots'
// variables.tf, and a default changed in either root hands the minter another
// role — a project-level predefined one, say — or points the grant at another key
// ring, with every block above still reading exactly as written. Both roots must
// therefore write the same literal, and it is written here so that a change to it
// is a change to this line too.
//
// The defaults are half of that rule and the other half is that the two blocks
// which name the ring READ these variables rather than writing the value beside
// them: a pinned default holds nothing about a block that ignores it. Both halves
// are asserted in TestOnlyTheMinterHoldsTheSignerRole.
var tfSignerDefaults = []struct{ name, want string }{
	{"signer_role", `"fugaroKeySigner"`},
	{"keyring", `"fugaro-licence"`},
}

// tfUnheldDefaults answers, sorted and named by root, every one of the two
// variables above whose default in the tree under dir is not the literal it is
// held to — a variable the root does not declare, or declares without a default,
// reads as the empty string and is one of them. The tree is read through
// tfTerraform, so a file this suite cannot read fails rather than being skipped.
func tfUnheldDefaults(t *testing.T, dir string) []string {
	t.Helper()
	got := map[string]string{}
	for _, rel := range tfTerraform(t, dir) {
		src := tfSource(t, filepath.Join(dir, rel))
		for _, d := range tfSignerDefaults {
			for _, block := range tfBlocksUntil(src, `variable "`+d.name+`"`, tfVarBlockEnds) {
				got[tfNames(path.Dir(rel), `variable "`+d.name+`"`)] = tfDefaultLiteral(block)
			}
		}
	}
	var out []string
	for _, d := range tfSignerDefaults {
		for _, root := range []string{keyringRoot, servicesRoot} {
			name := tfNames(root, `variable "`+d.name+`"`)
			if got[name] != d.want {
				out = append(out, name+" = "+got[name])
			}
		}
	}
	slices.Sort(out)
	return out
}

// tfUnheldKeys answers, sorted and named by root, every `google_kms_crypto_key`
// the tree under dir declares outside the one block it may hold. The signer role
// is granted on the key ring rather than on a key, so a key in that ring is one
// the minter signs with whichever block created it, and the checks that read the
// key's algorithm, its protection level and its lifecycle read one block: a
// sweep of the type that read all of them would still hold nothing about the
// ring's key set, and this list is what refuses a key no check looked at.
func tfUnheldKeys(t *testing.T, dir string) []string {
	t.Helper()
	held := tfNames(keyringRoot, `resource "google_kms_crypto_key" "signing"`)
	var out []string
	for _, rel := range tfTerraform(t, dir) {
		for _, block := range tfBlocks(tfSource(t, filepath.Join(dir, rel)), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_kms_crypto_key"`) {
				continue
			}
			if name := tfNames(path.Dir(rel), tfHeader(block)); name != held {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// tfRingEnvironments answers the environments the key ring root under dir
// declares in its `keys` default, sorted. The key block's `for_each = var.keys`
// creates one signing key per entry, so that map is the ring's key set — and the
// signer role is granted on the ring, which reaches every key in it. A third
// entry is therefore a third signing key the minter can sign with, and one whose
// algorithm, protection level and lifecycle no check above reads (D459(a), §2).
// The entries are read out of the map the block writes rather than searched for
// in its text: a description string naming an environment is not one.
func tfRingEnvironments(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, rel := range tfRootFiles(t, filepath.Join(dir, keyringRoot)) {
		src := tfSource(t, filepath.Join(dir, keyringRoot, rel))
		for _, block := range tfBlocksUntil(src, `variable "keys"`, tfVarBlockEnds) {
			value, ok := tfAttr(block, "default")
			if !ok || !strings.HasPrefix(value, "{") {
				t.Fatalf("keyring/'s `keys` variable writes no map default (it writes %q): the key block's for_each creates one signing key per entry, so that map is the ring's key set", value)
			}
			out = append(out, tfMapKeys(value)...)
		}
	}
	if len(out) == 0 {
		t.Fatalf("keyring/ declares no `keys` variable with a map default: `for_each = var.keys` on the key block would then create no key at all, and every check above reads a block that makes nothing")
	}
	slices.Sort(out)
	return out
}

// tfMapKeys answers the entry names of a map value, in the order it writes them:
// the keys at the map's own depth, in every spelling HCL gives a key — a bare
// name, a quoted one, which are the same key, and a computed one — and read
// outside any nested bracket, so a key of a nested map is not one and neither is
// a name written inside a string or a heredoc.
//
// A key whose name this reader cannot know — a computed key, `(var.environment)`,
// or an interpolated one, `"${var.environment}"` — is reported as the text it is
// written with, and a map written as a for-expression reports the name it binds:
// none of those is an environment this tree pins, so the rule that calls this
// fails on them rather than passing quietly, which is what a key set no reader
// can read should do.
func tfMapKeys(value string) []string {
	body, ok := tfBlockBody("map " + value)
	if !ok {
		return nil
	}
	var out []string
	depth := 0
	for i := 0; i < len(body); {
		switch c := body[i]; {
		case c == '"':
			j, ok := tfSkipString(body, i)
			if !ok {
				return out
			}
			if depth == 0 && tfKeyFollows(body, j) {
				out = append(out, body[i+1:j-1])
			}
			i = j
		case c == '<' && strings.HasPrefix(body[i:], "<<"):
			j, ok := tfSkipHeredoc(body, i)
			if !ok {
				return out
			}
			i = j
		case depth == 0 && c == '(':
			j, ok := tfSkipBrackets(body, i)
			if !ok {
				return out
			}
			if tfKeyFollows(body, j) {
				out = append(out, body[i:j])
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
			if tfKeyFollows(body, j) {
				out = append(out, body[i:j])
			}
			i = j
		default:
			i++
		}
	}
	return out
}

// tfKeyFollows answers whether only whitespace separates i from an `=`, which is
// what makes the token that ended at i a key rather than a value: `staging = "…"`
// and `"staging" = "…"` are one entry, and what follows the `=` is its value.
func tfKeyFollows(body string, i int) bool {
	for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
		i++
	}
	return i < len(body) && body[i] == '='
}

// tfVarBlockEnds are the markers that end a `variable` block in these roots: the
// next variable, and the kinds of block a later change could put beside one.
var tfVarBlockEnds = []string{"\nvariable \"", "\nresource \"", "\ndata \"", "\noutput \"", "\nprovider \"", "\nterraform {", "\nlocals {"}

// tfDefaultLiteral answers a variable block's default as the quoted literal it is
// written with, so that `default = "a"` and `default="a"` are one value. A
// default in another shape (a number, a list, a reference, a heredoc) is
// returned as the start of its line, which no literal compared against it can
// equal — the shape these roots use is a string, and anything else fails rather
// than passing quietly. A variable that writes no default reads as empty.
func tfDefaultLiteral(block string) string {
	value, _ := tfAttr(block, "default")
	return value
}

// tfAttr answers the value written for one of a block's own top-level
// attributes. It is a read of the block rather than a search of its text: `attr`
// is matched at word boundaries and only outside quoted strings, heredocs and
// nested blocks, so a word merely ending in the name, a `default = ` inside a
// `description` string or heredoc, and a `default` written in a nested block are
// none of them the attribute — each of which a plain substring search reads as
// it, and reading a sentence as a value is how a rule about what a block MEANS
// stops holding anything.
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

// tfBlockBody answers a block's text between its own braces, or false for a
// block whose braces this reader cannot see — which no comparison below can
// mistake for a value.
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

// tfValue answers the value starting at i: a quoted string with its quotes, so
// that two spellings of one literal compare equal; a list or a map as the whole
// bracket, so that `permissions = ["a", "b"]` is one value however it is
// wrapped; and anything else as the rest of its line, which no quoted literal
// equals.
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
// at i, and whether there was one. A value's own brackets are counted, so a
// nested list or map is part of the value, and a bracket written inside a string
// or a heredoc closes nothing.
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

// tfNestedBlock answers the first block nested inside block under the given
// name — `version_template`, `lifecycle`, `template` — or false for a block that
// holds none. It is how a check reaches an attribute the resource writes one
// level down without reading the block as text: the algorithm and the protection
// level live in `version_template` and `prevent_destroy` in `lifecycle`, and a
// check that found those words anywhere in the block would be satisfied by a map
// value or a description that happened to spell them out.
func tfNestedBlock(block, name string) (string, bool) {
	body, ok := tfBlockBody(block)
	if !ok {
		return "", false
	}
	code := tfCodeMask(body)
	for i := 0; i < len(body); {
		j := tfFindCode(body, code, name, i)
		if j < 0 {
			return "", false
		}
		if k := j + len(name); k < len(body) && tfIdentChar(body[k]) {
			i = k
			continue
		} else if j > 0 && tfIdentChar(body[j-1]) {
			i = j + len(name)
			continue
		} else {
			for k < len(body) && (body[k] == ' ' || body[k] == '\t' || body[k] == '\n' || body[k] == '\r') {
				k++
			}
			if k < len(body) && body[k] == '{' {
				if end, ok := tfSkipBrackets(body, k); ok {
					return body[j:end], true
				}
				return "", false
			}
			i = k + 1
		}
	}
	return "", false
}

// tfAttrsAre checks a block's attributes one by one and reports each one that
// does not write what it is held to, with what it wrote instead: a person
// reading the failure sees the value that is in the tree rather than only the
// rule it broke. The read is tfAttr's — the attribute itself — because a phrase
// is not a value: a `condition` block, a `description`, a `labels` map and a
// heredoc all hold free text, and every one of them can spell out the sentence a
// check was looking for while the attribute says something else.
func tfAttrsAre(t *testing.T, block, where string, want []struct{ attr, value string }, why string) {
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

// tfIdentStart reports whether c can begin an HCL attribute name.
func tfIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

// tfIdentChar reports whether c can continue one: HCL's names carry digits,
// dashes and dots.
func tfIdentChar(c byte) bool {
	return tfIdentStart(c) || c >= '0' && c <= '9' || c == '-' || c == '.'
}

// tfWantedDefaults renders the literals above, so that a failure names the whole
// rule rather than only the root that drifted.
func tfWantedDefaults() string {
	var lines []string
	for _, d := range tfSignerDefaults {
		lines = append(lines, "  "+d.name+" = "+d.want)
	}
	return strings.Join(lines, "\n")
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
// generate), an external_key_uri appearing on a version, the algorithm or
// protection level moving off the pair the verifiers were built for, a second
// key resource beside this one, or a third environment in the `keys` default
// the one block creates a key per.
func TestTheSigningKeyIsNonExportable(t *testing.T) {
	key := tfRootBlock(t, keyringRoot, `resource "google_kms_crypto_key" "signing"`)

	// The block above is the whole of this tree's key material, so it is refused
	// to any block but this one rather than found by a sweep of the type: the
	// signer role is granted at the key RING, which reaches every key in it, so
	// a second google_kms_crypto_key — in either root — is a key the reads below
	// never reach, with an algorithm, a protection level and a lifecycle of its
	// own. `for_each = var.keys` puts production and staging under the one block,
	// so the answer for a second one is a list's absence, not a second read of
	// the same attributes.
	if extra := tfUnheldKeys(t, "."); len(extra) != 0 {
		t.Errorf("this tree declares a KMS crypto key beside the one block above: %s\nthe signer role is granted on the key ring, so every key in it is one the minter signs with, and the checks here read that one block alone: a second key is a second algorithm, a second protection level and a lifecycle nothing here holds (Check 174, S3.5). The one key this service holds is %s", strings.Join(extra, ", "), tfNames(keyringRoot, `resource "google_kms_crypto_key" "signing"`))
	}

	// And the set that one block MAKES, which is the other half of the same
	// rule: `for_each = var.keys` is what creates a key, so keyring/'s default
	// for that variable is the ring's key set, and the signer role is granted on
	// the ring rather than on a key. A third entry there is a third signing key
	// the minter can sign with, made by a block whose algorithm, protection level
	// and lifecycle are read once, for the key id the two environments hold.
	// The ids themselves are not held: rotation is a new id in this map (§11),
	// and what this refuses is a new environment.
	if got := tfRingEnvironments(t, "."); !slices.Equal(got, []string{"production", "staging"}) {
		t.Errorf("the keys keyring/ creates are keyed %v, want [production staging]: the key block's for_each makes one signing key per entry, the signer role is granted on the key ring so the minter can sign with every key in it, and the checks above read the one block — so a third environment is a third key none of them holds (D459(a), S2.1, §2, S3.5)", got)
	}

	tfAttrsAre(t, key, "the signing key in "+keyringRoot, []struct{ attr, value string }{
		{"for_each", "var.keys"},
		{"purpose", `"ASYMMETRIC_SIGN"`},
	}, "S2.1 fixes the algorithm as Ed25519, which the three verifiers verify, and the key is signed with through the API rather than from a file. for_each is the ring's key set — one key per environment keyring/ declares, which is what makes the two keys D459(a) names rather than a literal list only those keys' own ids would cover")

	// The algorithm and the protection level are written one level down, inside
	// version_template, so they are read out of that block rather than looked for
	// in the key's text: a map value or a description saying "EC_SIGN_ED25519"
	// somewhere else in the key would satisfy a search and hold nothing.
	version, ok := tfNestedBlock(key, "version_template")
	if !ok {
		t.Fatalf("the signing key in %s writes no version_template block: the algorithm the three verifiers verify and the protection level it is offered at are written there, and without it the key is created at the provider's defaults", keyringRoot)
	}
	tfAttrsAre(t, version, "the signing key in "+keyringRoot, []struct{ attr, value string }{
		{"algorithm", `"EC_SIGN_ED25519"`},
		{"protection_level", `"SOFTWARE"`},
	}, "S2.1 fixes the algorithm as Ed25519, which the three verifiers verify, and the key is signed with through the API rather than from a file")

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

// TestTheSigningKeyIsNeverDestroyedByAnApply: the keys outlive the services
// that sign with them, so no apply removes one — a destroy takes the key id out
// of the ring, and every install holding that key's public half stops verifying
// the licences it signed with no revocation to notice it by (§11). Rotation is a
// new key id published in the three verifiers, never an apply's side effect,
// which is what the key's own comment and the root's say and what this holds:
// `for_each = var.keys` puts both keys under this one block, so the lifecycle
// covers production and staging alike.
//
// The mutation this fails on: the lifecycle block deleted, or its
// prevent_destroy flipped to false, either of which leaves the key an ordinary
// resource a plan may remove.
func TestTheSigningKeyIsNeverDestroyedByAnApply(t *testing.T) {
	key := tfRootBlock(t, keyringRoot, `resource "google_kms_crypto_key" "signing"`)
	lifecycle, ok := tfNestedBlock(key, "lifecycle")
	if !ok {
		t.Fatalf("the signing keys in %s write no lifecycle block: prevent_destroy is written there, and without it an apply may take the key id out of the ring", keyringRoot)
	}
	tfAttrsAre(t, lifecycle, "the signing keys in "+keyringRoot, []struct{ attr, value string }{
		{"prevent_destroy", "true"},
	}, "the key id is what the three verifiers hold a public half of, so a destroy stops every install verifying the licences that key signed, and rotation is a new id published in the verifiers rather than a removal an apply may make (S3.5, §11)")
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
// grant's member spelled google_service_account.intake.member, the intake
// service's template.service_account set to the minter's account, and — the
// same grant header the list already carries — a second signer grant appended
// to keyring/, or written in a root this tree does not have today, which is why
// both allow-lists name a root beside the header and tfFiles refuses a third.
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
	//
	// Each entry is keyed by the root holding it as well as by the block's
	// header, because the header alone is not an identity: the same signer grant
	// appended to keyring/main.tf is a second holder of the role, in a root this
	// test's own detailed checks call home for the keys, and a list keyed by the
	// header alone would read it as the grant services/ makes and pass it.
	sanctioned := map[string]string{
		tfNames(keyringRoot, `resource "google_project_iam_custom_role" "signer"`):                         "the signer role itself, and the whole of what signing takes",
		tfNames(servicesRoot, `resource "google_kms_key_ring_iam_member" "minter_signs"`):                  "its one grant, to the minter's account",
		tfNames(servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "intake_public"`):         "the intake's public invoker, which reaches no key at all",
		tfNames(servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"`): "the intake's one reach, the authenticated call to the minter",
	}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			header := tfHeader(block)
			if !tfGrants(header, tfCompact(block)) {
				continue
			}
			if _, ok := sanctioned[tfNames(path.Dir(rel), header)]; !ok {
				t.Errorf("%s holds a grant this service is not allowed:\n%s\nthe signer role is granted in one place, to one account, so that a reader of the plan sees every identity that can mint a licence key (Check 174). The whole list of grants, by the root holding each, is:\n%s", rel, strings.TrimSpace(block), tfList(sanctioned))
			}
		}
	}

	// Each of these is read as the attribute it is rather than searched for in the
	// block's text: this grant carries a `condition` block and the role a
	// `description`, both of which hold free text, and a check that found the
	// sentence it wanted there would pass while the attribute granted someone else
	// — a second member on the signer role being the whole of what this test
	// exists to refuse.
	grant := tfRootBlock(t, servicesRoot, `resource "google_kms_key_ring_iam_member" "minter_signs"`)
	tfAttrsAre(t, grant, "the signer grant in "+servicesRoot, []struct{ attr, value string }{
		{"member", "google_service_account.minter.member"},
		{"role", `"projects/${var.project}/roles/${var.signer_role}"`},
		{"key_ring_id", "data.google_kms_key_ring.signing.id"},
	}, "the minter is the one service that signs (T13's split), it signs with the custom role keyring/ creates — the narrowest grant that mints, one permission, while a predefined role also reads the public key and verifies — and the grant is made on the ring keyring/ created. A second member here is a second identity that can mint a licence key (Check 174)")

	// The role the grant names is the role keyring/ defines, and it is the
	// narrowest one that mints: one permission rather than the predefined
	// signerVerifier, which also reads the public key and verifies. Without this
	// the pair above could name a custom role holding useToSign and
	// useToVerify, or a role id nothing creates.
	role := tfRootBlock(t, keyringRoot, `resource "google_project_iam_custom_role" "signer"`)
	tfAttrsAre(t, role, "the signer role in "+keyringRoot, []struct{ attr, value string }{
		{"role_id", "var.signer_role"},
		{"permissions", `["cloudkms.cryptoKeyVersions.useToSign"]`},
	}, "the minter signs and verifies nothing with the private half, and the grant services/ makes names this role; a second permission here is a wider grant than the one the design's §3.3 rule allows")

	// And what the grant actually REACHES, which is the other half of the same
	// pair: both blocks name a variable, so the role the minter is granted is
	// `var.signer_role`'s default in each root and the ring it is granted on is
	// `var.keyring`'s. A default is a `.tf` file the sweeps above read as text and
	// never pinned, so without this the grant's meaning changes in variables.tf
	// alone — a services/ default of a project-level role hands the minter that
	// role, and every block above still reads as written.
	if bad := tfUnheldDefaults(t, "."); len(bad) != 0 {
		t.Errorf("this tree's roots do not hold the defaults the signer grant is decided by:\n  %s\nthe grant in %s names var.signer_role and its data source names var.keyring, so a default is the role the minter holds and the ring it is granted on; both roots write the same literal, and it is this suite's to hold (Check 174, S3.5). Want:\n%s", strings.Join(bad, "\n  "), servicesRoot, tfWantedDefaults())
	}

	// And that the pair above is what the two blocks READ, because pinning a
	// default holds nothing about a block that names the value literally beside
	// it: `data "google_kms_key_ring" "signing" { name = "attacker-ring" }`
	// leaves every default in the tree exactly as pinned while the grant is made
	// on another ring. The two blocks that name the ring — keyring/ creating it,
	// services/ reading it — each write all three of the ring's attributes from
	// their variables, so the ring the grant reaches is keyring/'s var.keyring,
	// and with the defaults held above it is one ring under one name.
	for _, ring := range []struct{ root, header string }{
		{keyringRoot, `resource "google_kms_key_ring" "signing"`},
		{servicesRoot, `data "google_kms_key_ring" "signing"`},
	} {
		block := tfRootBlock(t, ring.root, ring.header)
		tfAttrsAre(t, block, "the key ring block in "+ring.root, []struct{ attr, value string }{
			{"project", "var.project"},
			{"name", "var.keyring"},
			{"location", "var.region"},
		}, "the signer grant is made on this block's ring, so a name written literally here — and not from the variable both roots hold to one literal — points the grant at a ring no default in this tree decides (Check 174, S3.5)")
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
		block := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "`+service+`"`)
		template, ok := tfNestedBlock(block, "template")
		if !ok {
			t.Fatalf("the %s service writes no template block, so the identity it runs as is not in this tree", service)
		}
		tfAttrsAre(t, template, "the "+service+" service", []struct{ attr, value string }{
			{"service_account", "google_service_account." + service + ".email"},
		}, "the split between the intake and the minter is a split of identities, so the internet-facing service must not run as the one the signer role is granted to (Check 174, D450(a))")
	}

	// The two accounts are named in exactly the blocks the design allows, so a
	// third one has to be argued for rather than added: the minter's in its own
	// service and the one grant, the intake's in its own service and the one
	// invoker grant. A resource outside that list naming either account is an
	// identity taken where it was not meant to go; the outputs name both, and a
	// value handed out is not a reach.
	referrers := map[string]map[string]string{
		"google_service_account.minter.": {
			tfNames(servicesRoot, `resource "google_cloud_run_v2_service" "minter"`):          "the service that runs as it",
			tfNames(servicesRoot, `resource "google_kms_key_ring_iam_member" "minter_signs"`): "the one identity granted the signer role",
		},
		"google_service_account.intake.": {
			tfNames(servicesRoot, `resource "google_cloud_run_v2_service" "intake"`):                           "the service that runs as it",
			tfNames(servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"`): "the one identity allowed to call the minter",
		},
	}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			compact := tfCompact(block)
			for account, allowed := range referrers {
				if !strings.Contains(compact, account) {
					continue
				}
				if _, ok := allowed[tfNames(path.Dir(rel), tfHeader(block))]; !ok {
					t.Errorf("%s reaches %s from a block that is not one of the two the design allows:\n%s\nthe minter's account is the one identity that signs and the intake's is the one caller it has, so a third block naming either — in either root — is an identity taken where it was not meant to go (Check 174)", rel, account, strings.TrimSpace(block))
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
	minter := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "minter"`)
	tfAttrsAre(t, minter, "the minter service in "+servicesRoot, []struct{ attr, value string }{
		{"invoker_iam_disabled", "false"},
	}, "the flag has to be stated false, because a service that stopped checking IAM answers every caller and the one invoker grant this root writes would be the only thing between the internet and the signer (Check 174)")

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

	// The binding the count above is about, read as the attributes it writes: a
	// block that kept the intake's name in a condition title or a description
	// while its `name` pointed at the minter would open the signer's door and
	// still mention the intake, which is why the two attributes are read rather
	// than the words around them.
	publicBlock := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "intake_public"`)
	tfAttrsAre(t, publicBlock, "the allUsers binding in "+servicesRoot, []struct{ attr, value string }{
		{"member", `"allUsers"`},
		{"name", "google_cloud_run_v2_service.intake.name"},
		{"location", "google_cloud_run_v2_service.intake.location"},
		{"role", `"roles/run.invoker"`},
	}, "the single public grant has to be this resource, on the intake and on nothing else — a public invoker on the minter is the internet reaching the service that signs (Check 174)")

	invoker := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "intake_invokes_minter"`)
	tfAttrsAre(t, invoker, "the invoker grant on the minter in "+servicesRoot, []struct{ attr, value string }{
		{"name", "google_cloud_run_v2_service.minter.name"},
		{"location", "google_cloud_run_v2_service.minter.location"},
		{"member", "google_service_account.intake.member"},
		{"role", `"roles/run.invoker"`},
	}, "the minter has no public invoker, and the intake's ID token is what the platform checks before its container runs (D450(a))")
}

// TestTheIntakeIsTheRevisionThatLandsFirst: the intake reads the minter's answer
// strictly (§3.2, minterclient.go), so a record field a newer minter answers with
// is a whole answer a released intake refuses and every mint fails until that
// intake is the one serving. The one apply that changes both images is where that
// is decided, so the order is written as a dependency and asserted here rather
// than left to whoever runs the deploy: the minter waits for the intake, and the
// intake names nothing of the minter's — which is what makes the pair acyclic as
// well as correctly ordered, since the reverse edge is a graph Terraform refuses
// to apply rather than a deploy that goes the wrong way.
func TestTheIntakeIsTheRevisionThatLandsFirst(t *testing.T) {
	minter := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "minter"`)
	got, ok := tfAttr(minter, "depends_on")
	if !ok || tfCompact(got) != "[google_cloud_run_v2_service.intake]" {
		if got == "" {
			got = "nothing"
		}
		t.Errorf("the minter service in %s depends on %s, want [google_cloud_run_v2_service.intake]: a minter revision that lands before the intake's fails every mint for the length of the deploy, since the answer it writes carries a field the released intake refuses the whole answer over", servicesRoot, got)
	}

	intake := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "intake"`)
	if strings.Contains(tfCompact(intake), "google_cloud_run_v2_service.minter") {
		t.Errorf("the intake service in %s names the minter's service:\n%s\nthe intake is the revision that lands first, so anything it writes about the minter is either the reverse edge — a cycle, and one Terraform will not apply — or a reach the intake does not have", servicesRoot, strings.TrimSpace(intake))
	}
}

// TestTheTestsReadEveryFileOfTheTree: everything Terraform would read under
// this tree is either read by the checks above or refused here, because a sweep
// that silently skipped a file is a sweep that proves nothing about it. Four
// forms are refused, each one a place a grant can be written — or decided —
// where no block above changes:
//
//   - a `.tf.json` file, which is the same declarations in another syntax, so
//     every text check above looks at it and finds nothing;
//   - an override file — `override.tf`, `*_override.tf`, either of them in JSON
//     — which Terraform merges over the rest of its directory last, so the
//     minter's `invoker_iam_disabled` or the signer grant's member can be
//     rewritten while `main.tf` still reads as written;
//   - a variable file — `terraform.tfvars`, `*.auto.tfvars`, `something.tfvars`,
//     any of them in JSON — which decides what the blocks below it mean without
//     changing one of them: `signer_role` names the role the signer grant
//     mentions, so a file reading `signer_role = "editor"` hands the minter
//     Editor while the grant still reads `roles/${var.signer_role}`;
//   - a `module` block, whose configuration is another tree these tests do not
//     walk at all.
//
// A fifth is not a form but a place: Terraform in a root that is not `keyring/`
// or `services/`. Every sweep above walks those two, so a third root is a whole
// configuration — its own resources, its own grants — that none of them reads,
// and the same signer grant appended to it is a holder of the role no list here
// inspects. The scratch case below writes one.
//
// None of them is used anywhere in this repository, and a tree that gains one
// fails until a person either removes it or teaches these tests to read it.
// The cases below are the reviewer's mutations, built in a scratch tree: an
// override flipping the minter's IAM flag, a suffixed override adding
// `useToVerify` to the signer role, a JSON file granting the signer role, an
// override in JSON naming the intake in the signer grant, a variable file naming
// another role and an auto-loaded one in JSON, a second plain HCL file in a root
// — admitted, because the rule is one FORM per root and not one file — the
// defaults rule over a tree that changed one of them, the same rule against a
// block that names the wanted literal in a comment, a string or a heredoc
// instead of in its default, the reads of a block's own attributes against a
// grant and a key naming what a check looks for in free text, a grant written
// under a string whose interpolation holds a quote — which a reader that ended
// the string at the first quote it saw did not see at all — a second key beside
// the one block the checks read, the environments of `keys`' default in the
// spellings a map key has, and the third root above, which is not.
func TestTheTestsReadEveryFileOfTheTree(t *testing.T) {
	for _, tc := range []struct {
		name    string
		write   map[string]string
		refused []string
		stray   []string // files the walk hands over from outside the two roots
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
			// The third form a variable file is: it declares no resource and
			// changes none, and it decides what they mean. This one hands the
			// minter Editor while the grant still reads as written.
			name: "a variable file naming another signer role",
			write: map[string]string{
				"services/main.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {\n" +
					"  role   = \"projects/${var.project}/roles/${var.signer_role}\"\n" +
					"  member = google_service_account.minter.member\n}\n",
				"services/terraform.tfvars": "signer_role = \"editor\"\n",
			},
			refused: []string{"services/terraform.tfvars"},
		},
		{
			name: "an auto-loaded variable file in JSON",
			write: map[string]string{
				"keyring/main.tf":                     "resource \"google_kms_crypto_key\" \"signing\" {}\n",
				"keyring/production.auto.tfvars.json": `{"keys":{"production":"some-other-key","staging":"some-other-key"}}`,
			},
			refused: []string{"keyring/production.auto.tfvars.json"},
		},
		{
			name: "a second plain HCL file in a root",
			write: map[string]string{
				"services/main.tf":   "resource \"google_service_account\" \"intake\" {}\n",
				"services/grants.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {}\n",
			},
		},
		{
			// The reviewer's second mutation: the same grant header the allow-list
			// already carries, written in a root the allow-list is not keyed to.
			name: "a grant in a third root",
			write: map[string]string{
				"services/main.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {\n" +
					"  member = google_service_account.minter.member\n}\n",
				"staging/main.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {\n" +
					"  member = \"user:someone@example.com\"\n}\n",
			},
			stray: []string{"staging/main.tf"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, body := range tc.write {
				tfWrite(t, root, rel, body)
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
			// ... and the second rule over the same listing: Terraform in a root
			// that is not one of the two fails every sweep above, so a tree that
			// adds one goes red rather than being walked around.
			if got := tfStrayRoots(files); !slices.Equal(got, tc.stray) {
				t.Errorf("the listing's files outside the two roots are %v, want %v: the checks over this tree walk keyring/ and services/ and no third root, so Terraform in one is infrastructure none of them reads", got, tc.stray)
			}
		})
	}

	// The defaults rule over a tree this test writes, so that it is known to bite:
	// two roots holding the literals pass it, and one root changing its default
	// fails it by name. Without this the rule's only evidence is the real tree,
	// where no default has ever changed.
	t.Run("a changed default in one root", func(t *testing.T) {
		root := t.TempDir()
		agreeing := map[string]string{
			"keyring/variables.tf": "variable \"signer_role\" {\n  default = \"fugaroKeySigner\"\n}\n\n" +
				"variable \"keyring\" {\n  default = \"fugaro-licence\"\n}\n",
			"services/variables.tf": "variable \"signer_role\" {\n  default = \"fugaroKeySigner\"\n}\n\n" +
				"variable \"keyring\" {\n  default = \"fugaro-licence\"\n}\n",
		}
		for rel, body := range agreeing {
			tfWrite(t, root, rel, body)
		}
		if bad := tfUnheldDefaults(t, root); len(bad) != 0 {
			t.Fatalf("the rule refused a tree whose two roots hold the wanted literals: %v", bad)
		}

		tfWrite(t, root, "services/variables.tf", "variable \"signer_role\" {\n  default = \"fugaroKeyAdmin\"\n}\n\n"+
			"variable \"keyring\" {\n  default = \"fugaro-licence\"\n}\n")
		want := []string{tfNames(servicesRoot, `variable "signer_role"`) + ` = "fugaroKeyAdmin"`}
		if got := tfUnheldDefaults(t, root); !slices.Equal(got, want) {
			t.Errorf("a services/ default naming another role reads as %v, want %v: the rule has to fail on the root that changed, and name it — the grant in services/ still reads roles/${var.signer_role}, so the default is the only place this is visible", got, want)
		}
	})

	// The same rule against a block that says the wanted literal somewhere other
	// than in its default: a comment (in each of the three forms HCL has), a
	// description string, and a heredoc description, each holding `default = `.
	// All are places a search of the block's text finds "default" first and reads
	// as the value — with only `#` stripped and `default=` looked for anywhere, a
	// `//` line above the real default is what this suite pins while the apply
	// reads the other one, and a heredoc is the same fault one form further on.
	// The block's own default is the value, and it is the one that has to be held.
	t.Run("a decoy default in a comment, a string or a heredoc", func(t *testing.T) {
		root := t.TempDir()
		tfWrite(t, root, "keyring/variables.tf", "variable \"signer_role\" {\n  default = \"fugaroKeySigner\"\n}\n\n"+
			"variable \"keyring\" {\n  /* default = \"attacker-ring\" */\n  default = \"fugaro-licence\"\n}\n")
		tfWrite(t, root, "services/variables.tf", "variable \"signer_role\" {\n"+
			"  description = <<-EOT\n    The default = of this variable is the role the minter holds.\n    // default = \"fugaroKeySigner\"\n  EOT\n"+
			"  default = \"editor\"\n}\n\n"+
			"variable \"keyring\" {\n  #default = \"fugaro-licence\"\n  default = \"fugaro-licence\"\n}\n")
		want := []string{tfNames(servicesRoot, `variable "signer_role"`) + ` = "editor"`}
		if got := tfUnheldDefaults(t, root); !slices.Equal(got, want) {
			t.Errorf("a block whose default names another role reads as %v, want %v: a comment, a description string and a heredoc are not the value, and reading any of them as one pins the literal an apply does not use", got, want)
		}
	})

	// And the same rule for the reads the checks above make of a block's
	// ATTRIBUTES, over a tree this test writes: a block spelling the wanted
	// sentence out in the free text HCL gives it — a condition title, a labels map
	// carrying a heredoc — while the attribute itself says something else is to be
	// read as what it writes. A grant here hands the signer role to an attacker
	// and names the minter in its condition; a key here is RSA at HSM and its
	// labels spell out the version_template a search would find.
	t.Run("a value named in free text", func(t *testing.T) {
		root := t.TempDir()
		tfWrite(t, root, "services/main.tf", "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {\n"+
			"  key_ring_id = data.google_kms_key_ring.signing.id\n"+
			"  role        = \"projects/x/roles/fugaroKeySigner\"\n"+
			"  member      = \"user:attacker@example.com\"\n\n"+
			"  condition {\n    title      = \"member = google_service_account.minter.member\"\n    expression = \"true\"\n  }\n}\n")
		tfWrite(t, root, "keyring/main.tf", "resource \"google_kms_crypto_key\" \"signing\" {\n"+
			"  labels = {\n    note = <<-EOT\n      version_template {\n        algorithm = \"EC_SIGN_ED25519\"\n      }\n    EOT\n  }\n\n"+
			"  version_template {\n    algorithm        = \"RSA_SIGN_PKCS1_2048_SHA256\"\n    protection_level = \"HSM\"\n  }\n}\n")

		grant := tfRootBlock(t, filepath.Join(root, servicesRoot), `resource "google_kms_key_ring_iam_member" "minter_signs"`)
		if got, _ := tfAttr(grant, "member"); got != `"user:attacker@example.com"` {
			t.Errorf("the signer grant's member reads as %s: the condition title spells the minter's account out, and a rule that passed on that sentence would hold nothing while the grant hands the signer role to another identity", got)
		}
		key := tfRootBlock(t, filepath.Join(root, keyringRoot), `resource "google_kms_crypto_key" "signing"`)
		version, ok := tfNestedBlock(key, "version_template")
		if !ok {
			t.Fatalf("the key's version_template block is not found at all")
		}
		if got, _ := tfAttr(version, "algorithm"); got != `"RSA_SIGN_PKCS1_2048_SHA256"` {
			t.Errorf("the key's algorithm reads as %s: a heredoc in the labels map spells version_template out, and a check that read that text would hold the algorithm it names while the key is created with another", got)
		}
	})

	// And the readers of a file one form further in: a string whose
	// interpolation holds another string, whose own quote is not the end of the
	// first. `"a${"\""}"` is valid and fmt-clean, and the grant written under it
	// is the case: a reader that ended the outer string at the first quote it saw
	// left the rest of the file inside strings, so the block header below was
	// found by no sweep — the second holder of the signer role below is one this
	// file's whole set of rules about the signer role missed.
	t.Run("a quote inside an interpolation", func(t *testing.T) {
		root := t.TempDir()
		tfWrite(t, root, "services/main.tf", "locals {\n"+
			"  q = \"a${\"\\\"\"}\"\n"+
			"  d = \"x${\"resource \\\"google_kms_crypto_key\\\" \\\"decoy\\\" {}\"}\"\n"+
			"}\n\n"+
			"resource \"google_kms_key_ring_iam_member\" \"second\" {\n"+
			"  role   = \"projects/x/roles/fugaroKeySigner\"\n"+
			"  member = \"user:attacker@example.com\"\n}\n")
		grant := tfRootBlock(t, filepath.Join(root, servicesRoot), `resource "google_kms_key_ring_iam_member" "second"`)
		got, ok := tfAttr(grant, "member")
		if !ok || got != `"user:attacker@example.com"` {
			t.Errorf("the grant written under a string that interpolates a quote reads as %s (found = %t), want \"user:attacker@example.com\": a string ends at its own closing quote, not at a quote inside the interpolation, and a reader that ended it early read every block below as text — which is a sweep that finds nothing and passes", got, ok)
		}
		if got := len(tfBlocks(tfSource(t, filepath.Join(root, servicesRoot, "main.tf")), `resource "`)); got != 1 {
			t.Errorf("the file reads as %d resource blocks, want 1: the decoy header inside the second interpolation is text, and the grant written after the locals block is code — a reader that answered either one wrongly is a reader no sweep above can be built on", got)
		}
	})

	// And the key set the checks' reads range over: they read the block the
	// signing key is declared in, so a second key beside it — in a root of its
	// own, or here — is a key none of them reads, and the signer role is granted
	// on the RING, so it reaches every key in it. The list below is what refuses
	// one, keyed by the root holding it as well as by its header, because the
	// same block moved into the other root is a second key there.
	t.Run("a second key beside the one block", func(t *testing.T) {
		root := t.TempDir()
		tfWrite(t, root, "keyring/main.tf", "resource \"google_kms_crypto_key\" \"signing\" {\n"+
			"  purpose = \"ASYMMETRIC_SIGN\"\n}\n\n"+
			"resource \"google_kms_crypto_key\" \"signing_alt\" {\n"+
			"  name     = \"fugaro-licence-alt\"\n"+
			"  purpose  = \"ASYMMETRIC_SIGN\"\n\n"+
			"  version_template {\n    algorithm        = \"RSA_SIGN_PKCS1_2048_SHA256\"\n    protection_level = \"HSM\"\n  }\n}\n")
		want := []string{tfNames(keyringRoot, `resource "google_kms_crypto_key" "signing_alt"`)}
		if got := tfUnheldKeys(t, root); !slices.Equal(got, want) {
			t.Errorf("a second key in the tree reads as %v, want %v: the checks read the signing key's own block, so a key beside it is one none of them reads while the signer role, granted at the ring, reaches it", got, want)
		}
	})

	// And the map that key set comes from, over a tree this test writes: the
	// entries of `keys`' default are the environments, and a name spelled out in
	// the variable's own description is not one. The first tree is the two
	// environments with a third written into the description; the second writes
	// the third entry for real, which is what the rule refuses.
	t.Run("an environment named in the description", func(t *testing.T) {
		root := t.TempDir()
		tfWrite(t, root, "keyring/variables.tf", "variable \"keys\" {\n"+
			"  description = \"The environments: production, staging, dev = fugaro-licence-dev.\"\n"+
			"  type        = map(string)\n\n"+
			"  default = {\n    production = \"fugaro-licence-production\"\n    staging    = \"fugaro-licence-staging\"\n  }\n}\n")
		held := []string{"production", "staging"}
		if got := tfRingEnvironments(t, root); !slices.Equal(got, held) {
			t.Errorf("the environments read as %v, want %v: a name inside the variable's description is text, and reading it as an entry would hold the ring's key set to a sentence", got, held)
		}
		tfWrite(t, root, "keyring/variables.tf", "variable \"keys\" {\n"+
			"  description = \"The environments, one signing key each.\"\n"+
			"  type        = map(string)\n\n"+
			"  default = {\n    production = \"fugaro-licence-production\"\n    staging    = \"fugaro-licence-staging\"\n    dev        = \"fugaro-licence-dev\"\n  }\n}\n")
		if got := tfRingEnvironments(t, root); slices.Equal(got, held) {
			t.Errorf("a third entry in `keys`' default reads as %v, which is the held set: the key block's for_each makes one signing key per entry, so the third environment is a third key the minter's ring-level role reaches and no check reads", got)
		}
	})

	// And the spellings a map key has, over a tree this test writes: `"staging"
	// = …` is the entry `staging = …` is, so the two environments written with
	// quotes are the held set — a reader that skipped quoted keys read an empty
	// key set — while a THIRD environment written with quotes, or as a computed
	// key, whose name only the apply knows, is not the held set and is refused.
	t.Run("an environment written as a quoted key", func(t *testing.T) {
		root := t.TempDir()
		tfWrite(t, root, "keyring/variables.tf", "variable \"keys\" {\n"+
			"  default = {\n    \"production\" = \"fugaro-licence-production\"\n    \"staging\"    = \"fugaro-licence-staging\"\n  }\n}\n")
		held := []string{"production", "staging"}
		if got := tfRingEnvironments(t, root); !slices.Equal(got, held) {
			t.Errorf("the environments read as %v, want %v: a quoted key is the same key as a bare one, so a reader that skipped it read a ring of no keys, which both refuses a tree whose ring is the one it says it is and lets a third quoted environment through", got, held)
		}
		for _, written := range []string{
			"    \"dev\"     = \"fugaro-licence-dev\"\n",
			"    (var.dev) = \"fugaro-licence-dev\"\n",
		} {
			tfWrite(t, root, "keyring/variables.tf", "variable \"keys\" {\n"+
				"  default = {\n    \"production\" = \"fugaro-licence-production\"\n    \"staging\"    = \"fugaro-licence-staging\"\n"+written+"  }\n}\n")
			if got := tfRingEnvironments(t, root); slices.Equal(got, held) {
				t.Errorf("a third environment written as %q reads as %v, which is the held set: the key block's for_each makes one signing key per entry whatever the entry is spelled with, and the signer role is granted on the ring, so the third one is a key the minter can sign with that no check reads", strings.TrimSpace(written), got)
			}
		}
	})

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
