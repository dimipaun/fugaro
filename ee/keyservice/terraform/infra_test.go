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

// tfVarBlock answers the one `variable "<name>"` block a root declares, whichever
// of that root's files holds it, so that a check about a variable's default or
// its validation reads the block rather than searching a file's text. Terraform
// refuses a variable declared twice in one directory, and two declarations are a
// tree that does not validate.
func tfVarBlock(t *testing.T, root, name string) string {
	t.Helper()
	var found []string
	for _, rel := range tfRootFiles(t, root) {
		src := tfSource(t, filepath.Join(root, rel))
		found = append(found, tfBlocksUntil(src, `variable "`+name+`"`, tfVarBlockEnds)...)
	}
	if len(found) != 1 {
		t.Fatalf("%s declares variable %q %d times, want exactly 1: the blocks that decide what an apply means are read by name, so a variable that is gone or duplicated is a rule that would pass by reading nothing", root, name, len(found))
	}
	return found[0]
}

// tfStringList answers the quoted strings written in a value, in the order it
// writes them, and nothing that is not a quoted string: a validation's condition
// names the values it admits as string literals, and a name written in a
// description or a comment is not one — the same reading tfMapKeys makes of a
// map's keys, one value up.
func tfStringList(value string) []string {
	var out []string
	for i := 0; i < len(value); {
		if value[i] != '"' {
			i++
			continue
		}
		j, ok := tfSkipString(value, i)
		if !ok {
			return out
		}
		out = append(out, value[i+1:j-1])
		i = j
	}
	return out
}

// tfUnheldKeys answers, sorted and named by root, every `google_kms_crypto_key`
// the tree under dir declares outside the two blocks it may hold: the signing key
// keyring/ creates, and the handover key services/ creates in its own ring. The
// signer role is granted on the key ring rather than on a key, so a key in that
// ring is one the minter signs with whichever block created it, and the checks
// that read the key's algorithm, its protection level and its lifecycle read one
// block: a sweep of the type that read all of them would still hold nothing about
// the ring's key set, and this list is what refuses a key no check looked at.
//
// The handover key is held here rather than admitted silently because it is a
// key in the same project as the signer's: it is read by
// TestOnlyTheAPIDecryptsHandovers and TestOnlyTheMinterCanSign, which is what
// makes it a key this suite has looked at (D2182, D2194).
func tfUnheldKeys(t *testing.T, dir string) []string {
	t.Helper()
	held := map[string]bool{
		tfNames(keyringRoot, `resource "google_kms_crypto_key" "signing"`):   true,
		tfNames(servicesRoot, `resource "google_kms_crypto_key" "handover"`): true,
	}
	var out []string
	for _, rel := range tfTerraform(t, dir) {
		for _, block := range tfBlocks(tfSource(t, filepath.Join(dir, rel)), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_kms_crypto_key"`) {
				continue
			}
			if name := tfNames(path.Dir(rel), tfHeader(block)); !held[name] {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// tfRingEnvironments answers the environments the key ring root under dir
// declares in its `keys` default, sorted. The key block takes its id from that
// map — one entry per environment an apply of this root may carry — and the
// signer role is granted on that environment's key ring, which reaches every key
// in it. A third entry is therefore a third environment whose apply creates a
// third signing key the minter can sign with, and one whose algorithm,
// protection level and lifecycle no check above reads (D459, §2). The entries are
// read out of the map the block writes rather than searched for in its text: a
// description string naming an environment is not one.
func tfRingEnvironments(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, rel := range tfRootFiles(t, filepath.Join(dir, keyringRoot)) {
		src := tfSource(t, filepath.Join(dir, keyringRoot, rel))
		for _, block := range tfBlocksUntil(src, `variable "keys"`, tfVarBlockEnds) {
			value, ok := tfAttr(block, "default")
			if !ok || !strings.HasPrefix(value, "{") {
				t.Fatalf("keyring/'s `keys` variable writes no map default (it writes %q): the key block's id is the entry this map gives the environment an apply carries, so that map is the set of key ids this service mints with", value)
			}
			out = append(out, tfMapKeys(value)...)
		}
	}
	if len(out) == 0 {
		t.Fatalf("keyring/ declares no `keys` variable with a map default: the key block would then have no id at all, and every check above reads a block that makes nothing")
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
// key resource beside the two this tree holds, or a third environment in the
// `keys` default the one block takes the key id of.
func TestTheSigningKeyIsNonExportable(t *testing.T) {
	key := tfRootBlock(t, keyringRoot, `resource "google_kms_crypto_key" "signing"`)

	// The block above is the whole of this tree's SIGNING key material, so it is
	// refused to any block but this one (and the handover key services/ creates,
	// which cannot sign) rather than found by a sweep of the type: the signer
	// role is granted at the key RING, which reaches every key in it, so a second
	// google_kms_crypto_key in that ring — in either root — is a key the reads
	// below never reach, with an algorithm, a protection level and a lifecycle of
	// its own. The block creates one key, so the answer for a second one is a
	// list's absence, not a second read of the same attributes.
	if extra := tfUnheldKeys(t, "."); len(extra) != 0 {
		t.Errorf("this tree declares a KMS crypto key beside the two blocks it may hold: %s\nthe signer role is granted on the key ring, so every key in it is one the minter signs with, and the checks here read that one block alone: a second key is a second algorithm, a second protection level and a lifecycle nothing here holds (Check 174, S3.5). The two keys this tree holds are %s and %s", strings.Join(extra, ", "), tfNames(keyringRoot, `resource "google_kms_crypto_key" "signing"`), tfNames(servicesRoot, `resource "google_kms_crypto_key" "handover"`))
	}

	// And the set of environments that one block draws from, which is the other
	// half of the same rule: the key's id is `var.keys[var.environment]`, so
	// keyring/'s default for that map is the set of key ids an apply of this root
	// may create, and the signer role is granted on the ring the environment names
	// rather than on a key. A third entry there is a third environment whose apply
	// creates a third signing key — in a ring services/ would grant the minter the
	// signer role on — made by a block whose algorithm, protection level and
	// lifecycle are read once, for the two key ids this suite holds. The ids
	// themselves are not held: rotation is a new id in this map (§11), and what
	// this refuses is a new environment.
	if got := tfRingEnvironments(t, "."); !slices.Equal(got, []string{"production", "staging"}) {
		t.Errorf("the environments keyring/ mints for are %v, want [production staging]: the key's id is the one this map names for the environment an apply carries, the signer role is granted on that environment's key ring, and the checks above read the one block — so a third environment is a third key none of them holds (D459, S2.1, §2, S3.5)", got)
	}

	tfAttrsAre(t, key, "the signing key in "+keyringRoot, []struct{ attr, value string }{
		{"purpose", `"ASYMMETRIC_SIGN"`},
	}, "S2.1 fixes the algorithm as Ed25519, which the three verifiers verify, and the key is signed with through the API rather than from a file. The block creates ONE key — the one whose id var.keys names for the environment this apply carries — because a ring holds one environment's key and no for_each spans the map (D459, TestRingHoldsOneEnvironmentsKey)")

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
// the one block creates the key of the environment an apply carries, so the lifecycle
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
// identity. The API is reachable from the internet — the SPA in a person's
// browser, the merchant's webhook, the scheduler's monthly call — so an API that
// could sign would make T13's split a comment rather than a boundary, and Check
// 174's refusal of the internet-facing half's identity is this test's no.
//
// The mutations this fails on, and the red step the plan names first: the
// grant's member spelled google_service_account.api.member, the API
// service's template.service_account set to the minter's account, and — the
// same grant header the list already carries — a second signer grant appended
// to keyring/, or written in a root this tree does not have today, which is why
// both allow-lists name a root beside the header and tfFiles refuses a third.
// The reaches the API does hold are read as their attributes and not only as
// their headers, because a header is not a role: changing the document store's
// to roles/iam.serviceAccountTokenCreator or roles/run.admin leaves every grant
// the lists count exactly where it was and still hands the internet-facing
// account the minter's identity.
func TestOnlyTheMinterHoldsTheSignerRole(t *testing.T) {
	// Every block in the tree that grants anything at all is one of the seven
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
		tfNames(keyringRoot, `resource "google_project_iam_custom_role" "signer"`):                      "the signer role itself, and the whole of what signing takes",
		tfNames(servicesRoot, `resource "google_kms_key_ring_iam_member" "minter_signs"`):               "its one grant, to the minter's account",
		tfNames(servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "api_public"`):         "the API's public invoker, which reaches no key at all",
		tfNames(servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "api_invokes_minter"`): "the API's reach of the minter, the authenticated call that signs",
		tfNames(servicesRoot, `resource "google_project_iam_custom_role" "handover"`):                   "the handover sealer, which encrypts and decrypts and holds no signing permission",
		tfNames(servicesRoot, `resource "google_kms_key_ring_iam_member" "api_seals"`):                  "the API's one grant on the handover ring, which is not the ring the signer role is on",
		tfNames(servicesRoot, `resource "google_project_iam_member" "api_datastore"`):                   "the API's reach of the portal's document store, which holds no key",
		tfNames(servicesRoot, `resource "google_secret_manager_secret_iam_member" "api_reads"`):         "the API's read of the portal's own secrets, which the minter holds none of",
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
	// their variables, so the ring the grant reaches is keyring/'s var.keyring
	// plus the environment this apply names (D459: one ring per environment,
	// which TestRingHoldsOneEnvironmentsKey holds), and with the defaults held
	// above it is one ring under one name.
	for _, ring := range []struct{ root, header string }{
		{keyringRoot, `resource "google_kms_key_ring" "signing"`},
		{servicesRoot, `data "google_kms_key_ring" "signing"`},
	} {
		block := tfRootBlock(t, ring.root, ring.header)
		tfAttrsAre(t, block, "the key ring block in "+ring.root, []struct{ attr, value string }{
			{"project", "var.project"},
			{"name", `"${var.keyring}-${var.environment}"`},
			{"location", "var.region"},
		}, "the signer grant is made on this block's ring, so a name written literally here — and not from the variables both roots hold to one literal — points the grant at a ring no default in this tree decides, and the environment in the name is what makes the ring the one this apply's key is in (Check 174, S3.5, D459)")
	}

	// The other half of the same boundary, over every grant in the tree rather
	// than the signer one: the API's account is granted exactly four things and
	// no more — the invoker role on the minter, the sealer role on the handover
	// ring, the document store, and the portal's own secrets. A fifth grant is a
	// reach nothing else here would say out loud — a KMS role that signs, or the
	// signer's key ring, to the internet-facing account above all — and the
	// keyring root is as good a place to write one as this one. Every one of the
	// four is written by a block listed here, so the account's whole reach is the
	// plan's (Check 571, Check 174).
	allowedAPI := map[string]string{
		`resource "google_cloud_run_v2_service_iam_member" "api_invokes_minter"`: "the authenticated call to the minter, which is what signs",
		`resource "google_kms_key_ring_iam_member" "api_seals"`:                  "the handover ring, to encrypt and decrypt with and nothing else",
		`resource "google_project_iam_member" "api_datastore"`:                   "the portal's own documents, which hold no key",
		`resource "google_secret_manager_secret_iam_member" "api_reads"`:         "the portal's own secrets, which the merchant's keys are in and the minter reads none of",
	}
	uses := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfCompact(block), "google_service_account.api.member") {
				continue
			}
			uses++
			if _, ok := allowedAPI[tfHeader(block)]; !ok {
				t.Errorf("%s grants the API's account something besides its three reaches:\n%s\nthe API holds nothing that signs and nothing that reaches a signing key: its account is the minter's only invoker, the handover ring's only decrypter and the document store's reader (Check 174, Check 571)", rel, strings.TrimSpace(block))
			}
		}
	}
	if uses != len(allowedAPI) {
		t.Errorf("this tree grants the API's account %d times, want exactly %d: its whole reach is the authenticated call to the minter, the handover ring's sealer role, the document store and the portal's own secrets (Check 571)", uses, len(allowedAPI))
	}

	// And the secrets themselves, which are the API's to read and no other
	// identity's: the merchant's own keys and the mail credential are in these
	// containers, and §3.3's rule is that the identity which signs reads none of
	// them. The grant is pinned as the attributes it writes — one member, the one
	// role, over the containers' own map rather than over the project — and every
	// other block in the tree that hands out a Secret Manager role is refused, so
	// the minter's account cannot be granted one beside it.
	reads := tfRootBlock(t, servicesRoot, `resource "google_secret_manager_secret_iam_member" "api_reads"`)
	tfAttrsAre(t, reads, "the secret accessor grant in "+servicesRoot, []struct{ attr, value string }{
		{"for_each", "google_secret_manager_secret.this"},
		{"role", `"roles/secretmanager.secretAccessor"`},
		{"member", "google_service_account.api.member"},
	}, "the API is the process that holds the Paddle key, the merchant's webhook secret, the mail key and the error DSN (P1.7's config), so it reads the containers this root creates — one grant per secret, made on the container the same map names, because an accessor granted at the project reaches every secret the project ever holds. The minter reads none of them (§3.3, Check 571)")
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			compact := tfCompact(block)
			if !strings.Contains(compact, "secretmanager") || !strings.Contains(compact, "member=") {
				continue
			}
			if tfHeader(block) != `resource "google_secret_manager_secret_iam_member" "api_reads"` {
				t.Errorf("%s hands out a Secret Manager role besides the API's one read:\n%s\nthe portal's secrets are the merchant's Paddle key, its webhook secret, the mail key and the error DSN, and the one identity that signs holds no merchant secret (§3.3, D2182): a second grant here is a second process reading them — the minter's account above all, which is exactly the identity the two-project split used to keep out (Check 571)", rel, strings.TrimSpace(block))
			}
		}
	}

	// And the document store's role, read as the attributes it writes, for the
	// reason the invoker and sealer grants are pinned the same way: the sweep
	// above reads a grant's header, and a header is not a role. A project-level
	// grant whose role this test does not read is the whole fence written as one
	// word, and two of the words a hand could change it to are a way to sign
	// without touching anything else here — roles/iam.serviceAccountTokenCreator
	// lets the internet-facing account mint an ID token FOR the minter and call
	// the minter's own URL with it, and roles/run.admin lets it deploy a revision
	// that runs as the minter's account. Either one is the signer role's reach
	// arriving through a door no block in this tree names (Check 174, Check 571).
	datastore := tfRootBlock(t, servicesRoot, `resource "google_project_iam_member" "api_datastore"`)
	tfAttrsAre(t, datastore, "the document store grant in "+servicesRoot, []struct{ attr, value string }{
		{"project", "var.project"},
		{"role", `"roles/datastore.user"`},
		{"member", "google_service_account.api.member"},
	}, "the API is what writes every organisation's records, billing and reveal bundles, and it does so through the one predefined role that reads and writes documents: a broader predefined role here — the token creator above all, which would let the API's account mint an ID token as the minter, and run.admin, which would let it deploy a revision running as the minter — is a signing reach granted at the project (Check 571)")

	// And the identity each service RUNS as, which is the half a grant sweep
	// cannot see: a service that runs as another account IS that account, so an
	// API whose template names the minter's would hold the signer role from
	// the internet while every grant above stayed exactly as written.
	for _, service := range []string{"api", "minter"} {
		block := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "`+service+`"`)
		template, ok := tfNestedBlock(block, "template")
		if !ok {
			t.Fatalf("the %s service writes no template block, so the identity it runs as is not in this tree", service)
		}
		tfAttrsAre(t, template, "the "+service+" service", []struct{ attr, value string }{
			{"service_account", "google_service_account." + service + ".email"},
		}, "the split between the API and the minter is a split of identities, so the internet-facing service must not run as the one the signer role is granted to (Check 174, D450(a))")
	}

	// The accounts are named in exactly the blocks the plan allows, so a new one
	// has to be argued for rather than added: the minter's in its own service and
	// the one grant, the API's in its own service and the three reaches above.
	// A resource outside that list naming any of them is an identity taken where
	// it was not meant to go; the outputs name them, and a value handed out is
	// not a reach.
	referrers := map[string]map[string]string{
		"google_service_account.minter.": {
			tfNames(servicesRoot, `resource "google_cloud_run_v2_service" "minter"`):          "the service that runs as it",
			tfNames(servicesRoot, `resource "google_kms_key_ring_iam_member" "minter_signs"`): "the one identity granted the signer role",
		},
		"google_service_account.api.": {
			tfNames(servicesRoot, `resource "google_cloud_run_v2_service" "api"`):                           "the service that runs as it",
			tfNames(servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "api_invokes_minter"`): "the one identity allowed to call the minter",
			tfNames(servicesRoot, `resource "google_kms_key_ring_iam_member" "api_seals"`):                  "the one identity allowed to decrypt a handover",
			tfNames(servicesRoot, `resource "google_project_iam_member" "api_datastore"`):                   "the one identity allowed to read the portal's documents",
			tfNames(servicesRoot, `resource "google_secret_manager_secret_iam_member" "api_reads"`):         "the one identity allowed to read the portal's secrets",
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
					t.Errorf("%s reaches %s from a block that is not one of the ones the plan allows:\n%s\nthe minter's account is the one identity that signs and the API's is the one caller it has, so a block outside the list naming either — in either root — is an identity taken where it was not meant to go (Check 174, Check 571)", rel, account, strings.TrimSpace(block))
				}
			}
		}
	}
}

// TestTheMinterHasNoPublicInvoker: the minter is not reachable from the
// internet, and the API is the one caller it has. Two of the three things that
// decide that are asserted here rather than left to the design's prose: the
// minter's service REQUIRES IAM — with the flag true Cloud Run serves every
// caller the network delivers, which would leave the grants below as decoration
// and a minter that anyone may invoke — and no binding anywhere hands the
// internet an invoker but the API's own public one. The third, that the API's
// account is the minter's ONLY invoker, is TestOnlyTheAPIInvokesTheMinter's, and
// the fourth, its network fence, is TestMinterIngressIsInternal's. Check 174
// again, from the network's side.
func TestTheMinterHasNoPublicInvoker(t *testing.T) {
	minter := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "minter"`)
	tfAttrsAre(t, minter, "the minter service in "+servicesRoot, []struct{ attr, value string }{
		{"invoker_iam_disabled", "false"},
	}, "the flag has to be stated false, because a service that stopped checking IAM answers every caller and the one invoker grant this root writes would be the only thing between the internet and the signer (Check 174)")

	// The count is over the bare token in every block of the tree rather than
	// over one spelling of one attribute: `member = "allUsers"`, a
	// google_cloud_run_v2_service_iam_binding writing `members = ["allUsers"]`,
	// an `_iam_policy`, a second service's binding — all of them say allUsers,
	// and the API's own public binding is the only one of them this service
	// is allowed. What a policy read from a file would say is beyond a sweep over
	// this tree, and nothing here reads one.
	public := 0
	for _, rel := range tfFiles(t) {
		f := tfCompact(tfSource(t, rel))
		if strings.Contains(f, `"allAuthenticatedUsers"`) {
			t.Errorf("%s grants allAuthenticatedUsers: any Google account is not the API's caller, and a signed-in stranger reaching the minter would sign with the key ring", rel)
		}
		public += strings.Count(f, `"allUsers"`)
	}
	if public != 1 {
		t.Errorf("this tree grants allUsers %d times, want exactly 1: the API is the one public surface, and every other binding — on the minter above all — hands the internet an invoker it should not have", public)
	}

	// The binding the count above is about, read as the attributes it writes: a
	// block that kept the API's name in a condition title or a description
	// while its `name` pointed at the minter would open the signer's door and
	// still mention the API, which is why the two attributes are read rather
	// than the words around them.
	publicBlock := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "api_public"`)
	tfAttrsAre(t, publicBlock, "the allUsers binding in "+servicesRoot, []struct{ attr, value string }{
		{"member", `"allUsers"`},
		{"name", "google_cloud_run_v2_service.api.name"},
		{"location", "google_cloud_run_v2_service.api.location"},
		{"role", `"roles/run.invoker"`},
	}, "the single public grant has to be this resource, on the API and on nothing else — a public invoker on the minter is the internet reaching the service that signs (Check 174)")

}

// TestOnlyTheMinterCanSign: signing is one permission, in one role, held by one
// account — and the portal's own key is one that cannot sign at all. D2194 puts
// every one of the portal's services in the signer's project, so the old fence
// between two projects is gone and what is left is this: the signer role is the
// ONLY route to useToSign, it is granted at the ring to the minter's account
// alone (TestOnlyTheMinterHoldsTheSignerRole), and the key the API holds a grant
// on is a symmetric one whose purpose is encrypt and decrypt.
//
// The mutations this fails on: a second block carrying useToSign — a role
// services/ defines, an _iam_binding names, or a policy read from a file — a
// predefined role that reaches signing by another name (cloudkms.signerVerifier
// also reads the public key and verifies; owner and editor reach the key ring
// itself), the handover key taking ASYMMETRIC_SIGN so the API's account could
// mint with it, and the sealer role growing a third permission.
func TestOnlyTheMinterCanSign(t *testing.T) {
	// The sweep is over the permission and over the role ids rather than over one
	// resource type: IAM writes the same grant as an _iam_member, an _iam_binding
	// or an _iam_policy, and a custom role is a resource of its own, so a list
	// that counted holders of one type would pass a second route to signing
	// written in another spelling.
	signerRole := tfNames(keyringRoot, `resource "google_project_iam_custom_role" "signer"`)
	signingRoles := []string{
		`"roles/owner"`,
		`"roles/editor"`,
		`"roles/cloudkms.admin"`,
		`"roles/cloudkms.signer"`,
		`"roles/cloudkms.signerVerifier"`,
	}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			name := tfNames(path.Dir(rel), tfHeader(block))
			compact := tfCompact(block)
			if strings.Contains(compact, "useToSign") && name != signerRole {
				t.Errorf("%s writes a signing permission outside keyring/'s signer role:\n%s\nthe whole of what signing takes is that one role, granted at the ring to the minter's account (Check 174, S3.3), so a second block carrying useToSign — whatever it is called, in either root — is a second identity that can mint a licence key", rel, strings.TrimSpace(block))
			}
			for _, role := range signingRoles {
				if strings.Contains(compact, role) {
					t.Errorf("%s names the predefined role %s:\na predefined role reaches signing through more permissions than the one this service needs — cloudkms.signerVerifier also reads the public key and verifies, and owner and editor reach the key ring itself — so the signer role is the custom role keyring/ defines and nothing predefined (Check 174, S3.3)", rel, role)
				}
			}
		}
	}

	// And the key the API may use, which is the same rule from the portal's side:
	// a purpose of ENCRYPT_DECRYPT with the symmetric algorithm is what makes the
	// handover key a key that seals and opens a reveal bundle and can produce no
	// signature at all (D2182).
	handover := tfRootBlock(t, servicesRoot, `resource "google_kms_crypto_key" "handover"`)
	tfAttrsAre(t, handover, "the handover key in "+servicesRoot, []struct{ attr, value string }{
		{"key_ring", "google_kms_key_ring.handover.id"},
		{"purpose", `"ENCRYPT_DECRYPT"`},
	}, "the 24-hour reveal handover is stored encrypted under a key only the API's account may use (D2182), so its purpose is encrypt and decrypt: an ASYMMETRIC_SIGN key here would be a second key the API's account could mint a licence with")
	version, ok := tfNestedBlock(handover, "version_template")
	if !ok {
		t.Fatalf("the handover key in %s writes no version_template block, so the algorithm it is created with is the provider's default", servicesRoot)
	}
	tfAttrsAre(t, version, "the handover key in "+servicesRoot, []struct{ attr, value string }{
		{"algorithm", `"GOOGLE_SYMMETRIC_ENCRYPTION"`},
	}, "the handover key seals and opens what is stored, so it is symmetric (D2182): an asymmetric algorithm here would be a key pair whose public half is published nowhere and whose private half is what the API holds")

	// And the permissions the API's grant on that ring is made of, which is the
	// other half of the same pair: the sealer role holds the two encrypt and
	// decrypt permissions and no third, so even a ring holding a signing key
	// would give the API no way to sign with it.
	sealer := tfRootBlock(t, servicesRoot, `resource "google_project_iam_custom_role" "handover"`)
	tfAttrsAre(t, sealer, "the handover role in "+servicesRoot, []struct{ attr, value string }{
		{"role_id", "var.handover_role"},
		{"permissions", `["cloudkms.cryptoKeyVersions.useToEncrypt", "cloudkms.cryptoKeyVersions.useToDecrypt"]`},
	}, "the API encrypts a handover and decrypts it once, and signs nothing anywhere (D2182, Check 571): a third permission here — useToSign above all — is a role that mints")
}

// TestOnlyTheAPIInvokesTheMinter: the minter has one caller, and it is the API's
// account. The minter is a service the platform checks IAM for
// (TestTheMinterHasNoPublicInvoker) behind a network fence
// (TestMinterIngressIsInternal), so what is left is who the platform lets in,
// and the answer is one account: the API's, whose ID token the call carries.
//
// The mutations this fails on: a second invoker — the same account written as an
// _iam_binding's members list, an _iam_policy, or a plain `user:` beside it — a
// grant on the minter in the keyring root, a scheduled job pointed at the minter
// rather than at the API, a third scheduled job beside the two — read by name, so
// a block with a fourth name is one no check here reads at all — a token minted
// for another account or another audience, and a job's pause written as a literal
// rather than as the variable the operator's apply sets, which is a job the next
// apply pauses again.
func TestOnlyTheAPIInvokesTheMinter(t *testing.T) {
	// Every block in the tree that names the minter's service is one of the two
	// the plan allows. The sweep is over the REFERENCE and not over the grant
	// resource type, because a block that reaches a service is a block that may
	// reach it by a route no grant resource writes — an invoker binding is one
	// spelling, and the allow-list above is the other half.
	allowed := map[string]string{
		tfNames(servicesRoot, `resource "google_cloud_run_v2_service" "minter"`):                        "the minter itself",
		tfNames(servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "api_invokes_minter"`): "the API's one reach, the authenticated call that signs",
	}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfCompact(block), "google_cloud_run_v2_service.minter.") {
				continue
			}
			if _, ok := allowed[tfNames(path.Dir(rel), tfHeader(block))]; !ok {
				t.Errorf("%s reaches the minter's service from a block that is not one of the two the plan allows:\n%s\nthe minter is the one service that signs and it has one caller, so a second block naming it — in either root, whatever the resource is called — is a second way in (Check 174, Check 571)", rel, strings.TrimSpace(block))
			}
		}
	}

	// And that one grant, read as the attributes it writes: one member, and it is
	// the API's account. A list, a condition, or a second member written any other
	// way is what the sweep above refuses; this is the positive half.
	invoker := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service_iam_member" "api_invokes_minter"`)
	tfAttrsAre(t, invoker, "the invoker grant on the minter in "+servicesRoot, []struct{ attr, value string }{
		{"name", "google_cloud_run_v2_service.minter.name"},
		{"location", "google_cloud_run_v2_service.minter.location"},
		{"member", "google_service_account.api.member"},
		{"role", `"roles/run.invoker"`},
	}, "the minter has no public invoker, and the API's ID token is what the platform checks before its container runs (D450(a), D2194)")

	// The scheduler is the API's second caller, and it calls the API rather than
	// the minter: the jobs' targets name the API's URL, and their OIDC tokens are
	// minted for the scheduler's own account with that URL as the audience — the
	// pair the API verifies (P1.3, F33). Both are created PAUSED, because the
	// routes they call are P6.3's and P7.1's and a job that fired before them
	// would mail nothing at best.
	//
	// The pause is read as the variable it is written with rather than as the
	// value it happens to carry: a literal `paused = true` here is a job whose
	// only switch is outside Terraform, and the next apply — a re-run, an image
	// change, any infra change at all — writes the literal back and pauses the
	// job again, mid-month and silently, which is the opposite of what the root's
	// own comment says an owner does with the deploy (independent review of this
	// file, finding 4).
	for _, job := range []struct{ header, what, path, paused string }{
		{`resource "google_cloud_scheduler_job" "statements"`, "the statement job", "var.statement_job_path", "var.statements_paused"},
		{`resource "google_cloud_scheduler_job" "reminders"`, "the reminder job", "var.reminder_job_path", "var.reminders_paused"},
	} {
		block := tfRootBlock(t, servicesRoot, job.header)
		tfAttrsAre(t, block, job.what+" in "+servicesRoot, []struct{ attr, value string }{
			{"paused", job.paused},
		}, "the statement and reminder routes are later tasks', so the plan creates both jobs paused and the owner turns them on with the deploy (P1.5): a job that ran before its route exists answers 404 at best and mails the wrong thing at worst, and a pause written as a literal is one the next apply puts back")
		if strings.Contains(tfCompact(block), "minter") {
			t.Errorf("%s names the minter:\n%s\nthe minter's one caller is the API, and a scheduled call to it is a second invoker written without an IAM grant at all (Check 571)", job.what, strings.TrimSpace(block))
		}
		target, ok := tfNestedBlock(block, "http_target")
		if !ok {
			t.Fatalf("%s writes no http_target block, so what it calls is not in this tree", job.what)
		}
		tfAttrsAre(t, target, job.what, []struct{ attr, value string }{
			{"uri", `"${google_cloud_run_v2_service.api.uri}${` + job.path + `}"`},
		}, "the jobs call the API, which is the service that holds the store the statements and the reminders are read from (Check 571): a URL written as a literal would be a target no output of this tree decides")
		token, ok := tfNestedBlock(target, "oidc_token")
		if !ok {
			t.Fatalf("%s writes no oidc_token block, so the account the API sees is not in this tree", job.what)
		}
		tfAttrsAre(t, token, job.what, []struct{ attr, value string }{
			{"service_account_email", "google_service_account.scheduler.email"},
			{"audience", "google_cloud_run_v2_service.api.uri"},
		}, "the API verifies the scheduler's OIDC token by account AND audience (P1.3, F33), so the token is minted for the scheduler's own account and for the API's URL: any other audience is a token the API refuses, and any other account is one its list does not carry")
	}

	// And that those two are the whole of the tree's schedules, which is what makes
	// reading them by name a census rather than a pair of names a third block can
	// be written beside (independent review of this file). A job fires
	// on a schedule of its own, so a third google_cloud_scheduler_job is a call
	// none of the reads above makes: the loop is keyed to the two headers, and the
	// minter sweep at the top of this test skips any block that does not name the
	// minter — so a target written as a literal is a URL no output of this tree
	// decides, and one aimed at the minter is a second caller with no IAM grant at
	// all (Check 571).
	held := map[string]string{
		tfNames(servicesRoot, `resource "google_cloud_scheduler_job" "statements"`): "the statement job",
		tfNames(servicesRoot, `resource "google_cloud_scheduler_job" "reminders"`):  "the reminder job",
	}
	jobs := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_cloud_scheduler_job"`) {
				continue
			}
			jobs++
			if _, ok := held[tfNames(path.Dir(rel), tfHeader(block))]; !ok {
				t.Errorf("%s declares a scheduled job that is not one of the two this tree holds:\n%s\na job is a call that fires on its own, and the checks above read the statement and reminder jobs by name: a job beside them is a call whose target, account, audience and pause nothing here reads", rel, strings.TrimSpace(block))
			}
		}
	}
	if jobs != len(held) {
		t.Errorf("this tree declares %d scheduled jobs, want %d: the monthly statements (P6.3) and the daily reminders (P7.1) are the portal's whole set of scheduled calls, and a job beside them calls something on a schedule no other part of this tree decides", jobs, len(held))
	}

	// And the switch the two attributes above read, whose default is what makes
	// "created paused" true of an apply that names nothing: a default of false is
	// both jobs firing on the first apply, before P6.3's and P7.1's routes exist,
	// and a job with no default at all is an apply that will not run until the
	// operator names a value — neither of which is the plan's word (P1.5). One
	// variable per job, so the deploy that ships one route does not start the
	// other's job with it.
	for _, d := range []struct{ name, why string }{
		{"statements_paused", "the statement route is P6.3's"},
		{"reminders_paused", "the reminder route is P7.1's"},
	} {
		if got := tfDefaultLiteral(tfVarBlock(t, servicesRoot, d.name)); got != "true" {
			t.Errorf("services/'s %s default reads as %q, want \"true\":\nthe job's pause is this variable, so a default of false is both jobs firing on the next apply — %s, so the call answers 404 and mails nothing — and a variable with no default stops the apply until an operator names a value", d.name, got, d.why)
		}
	}
}

// TestMinterIngressIsInternal: the minter's ingress is internal and the API's is
// not, which is the network's half of the same boundary the IAM grants are the
// other half of — and the API's egress, without which that half delivers
// nothing. Cloud Run decides "internal" by where a request came from, not by
// which project the caller belongs to, so an internal-only service whose one
// caller has no VPC egress refuses the call it was written for: the API's
// revision therefore leaves over this root's own network (D2194's project, P1.5's
// fence), and the network carries it to the minter, to Google's APIs and to
// Paddle and Resend as well.
//
// The mutations this fails on: the minter's ingress set to ALL — which would
// leave the grants as the only fence, against the plan's words — the API's set
// to internal, which would take the SPA and the merchant's webhook off the
// internet, a third service added without an ingress line, which takes the
// provider's default with nothing here reading it, the API's vpc_access block
// dropped or narrowed to the private ranges (the call stops arriving as
// internal), its network_interfaces naming another network, the subnet losing
// private Google access or the NAT covering no subnet (the API's Google calls
// and its Paddle and Resend calls stop leaving), and a vpc_access block on the
// minter, which is an egress path the plan does not name.
func TestMinterIngressIsInternal(t *testing.T) {
	for _, want := range []struct{ header, ingress, why string }{
		{`resource "google_cloud_run_v2_service" "api"`, `"INGRESS_TRAFFIC_ALL"`,
			"the API is the portal's one public surface: the SPA posts to it from a browser and Paddle posts events to it from outside the project, so its ingress is all — its fence is the sign-in check P1.3 makes, not the network's"},
		{`resource "google_cloud_run_v2_service" "minter"`, `"INGRESS_TRAFFIC_INTERNAL_ONLY"`,
			"the minter is called by the API and by nothing else (D2194, P1.5): internal ingress is the network's half of that, and it delivers that call only because the API's revisions egress through this project's network (the vpc_access asserted below) — Cloud Run treats a caller as internal by where its request came from, not by which project it is in"},
	} {
		block := tfRootBlock(t, servicesRoot, want.header)
		tfAttrsAre(t, block, "the service in "+servicesRoot, []struct{ attr, value string }{
			{"ingress", want.ingress},
		}, want.why)
	}

	// And the egress that makes the pair above deliver the call at all, which is
	// the half of an internal-ingress service that is easy to leave out: Cloud Run
	// refuses a caller that is not on the project's network BEFORE it reads the
	// invoker grant, so a minter whose only caller has no VPC egress is a minter
	// nothing can reach — every mint call fails and no licence is issued in either
	// environment (independent review of this file, finding 1).
	api := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "api"`)
	template, ok := tfNestedBlock(api, "template")
	if !ok {
		t.Fatalf("the API service writes no template block, so how its call to the minter is routed is not in this tree")
	}
	access, ok := tfNestedBlock(template, "vpc_access")
	if !ok {
		t.Fatalf("the API's template writes no vpc_access block: the minter's ingress is internal-only, and a revision that does not leave over this project's network is refused by the platform before the invoker grant is read, so the fence P1.5 asks for would refuse the one caller it is written for (P1.5)")
	}
	tfAttrsAre(t, access, "the API's egress in "+servicesRoot, []struct{ attr, value string }{
		{"egress", `"ALL_TRAFFIC"`},
	}, "the call that signs takes the same path as the rest of the API's traffic: with the private ranges alone the minter's run.app URL is not one of them, so the call would leave the revision the ordinary way and arrive as an outsider")
	interfaces, ok := tfNestedBlock(access, "network_interfaces")
	if !ok {
		t.Fatalf("the API's vpc_access block names no network_interfaces block, so the network its traffic leaves over is not in this tree")
	}
	tfAttrsAre(t, interfaces, "the API's egress in "+servicesRoot, []struct{ attr, value string }{
		{"network", "google_compute_network.portal.id"},
		{"subnetwork", "google_compute_subnetwork.portal.id"},
	}, "the network is this root's own rather than the project's default, so the range a revision egresses from — and the fence the minter's ingress reads — is one a plan shows being created and one this tree reviews")

	// And the network that egress lands on, which carries three kinds of traffic
	// or the API is broken in one direction or another: the minter's call, Google's
	// own APIs (the store the API writes and the secrets it reads), and the
	// internet, where Paddle and Resend are. Private Google access is the second,
	// the NAT the third, and a subnet without the one or a NAT covering no subnet
	// is an API that can mint and cannot charge for or mail the key it minted.
	subnet := tfRootBlock(t, servicesRoot, `resource "google_compute_subnetwork" "portal"`)
	tfAttrsAre(t, subnet, "the portal subnet in "+servicesRoot, []struct{ attr, value string }{
		{"network", "google_compute_network.portal.id"},
		{"region", "var.region"},
		{"private_ip_google_access", "true"},
	}, "the API's Google API calls have to leave over the network the minter's call does, or an all-traffic egress turns every Firestore and Secret Manager call into one that needs the NAT: private Google access is what keeps them on Google's own path (deploy/terraform's compute module holds the same rule from the other side)")
	nat := tfRootBlock(t, servicesRoot, `resource "google_compute_router_nat" "portal"`)
	tfAttrsAre(t, nat, "the portal NAT in "+servicesRoot, []struct{ attr, value string }{
		{"router", "google_compute_router.portal.name"},
		{"region", "var.region"},
		{"nat_ip_allocate_option", `"AUTO_ONLY"`},
	}, "a revision on this network has no external address, so without a NAT it reaches the minter and Google's APIs and nothing else: Paddle's charge and Resend's mail are not Google's, and private Google access does not reach them (deploy/terraform's compute module provisions the same pair for the same reason)")
	covered, ok := tfNestedBlock(nat, "subnetwork")
	if !ok {
		t.Fatalf("the portal NAT names no subnetwork, so it translates the traffic of nothing and the API's internet calls have no way out")
	}
	tfAttrsAre(t, covered, "the portal NAT in "+servicesRoot, []struct{ attr, value string }{
		{"name", "google_compute_subnetwork.portal.name"},
	}, "the NAT is created for this root's own subnet and no sibling's: a NAT that covered every subnet of the project would put another root's traffic on the internet under this name")

	// And the minter stays OFF the network: it answers internal calls rather than
	// making them, and what it calls is Google's APIs. A vpc_access block here
	// would be the signer's revision given an egress path no task asks for, and an
	// egress path is what a later task's code inherits.
	minter := tfRootBlock(t, servicesRoot, `resource "google_cloud_run_v2_service" "minter"`)
	minterTemplate, ok := tfNestedBlock(minter, "template")
	if !ok {
		t.Fatalf("the minter service writes no template block")
	}
	if _, ok := tfNestedBlock(minterTemplate, "vpc_access"); ok {
		t.Errorf("the minter's template writes a vpc_access block:\nthe minter calls Google's APIs and nothing that needs the network, and what this tree grants a revision is what a later task's code can use: the signer's way out of the project is a reach the plan does not name (P1.5, Check 571)")
	}

	// And every service in the tree, which is what makes the pair above the whole
	// of it: a third service is a third surface, and one written without an
	// ingress line is a surface whose fence is the provider's default.
	services := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_cloud_run_v2_service"`) {
				continue
			}
			services++
			ingress, ok := tfAttr(block, "ingress")
			if !ok || (tfCompact(ingress) != `"INGRESS_TRAFFIC_ALL"` && tfCompact(ingress) != `"INGRESS_TRAFFIC_INTERNAL_ONLY"`) {
				t.Errorf("%s declares a Cloud Run service whose ingress reads %q:\nevery service this tree declares states its ingress, and the two the plan creates are the API's (all) and the minter's (internal): a service with no ingress line takes the provider's default, so a later change of that default would open or close a surface with nothing in this tree to review", rel, ingress)
			}
		}
	}
	if services != 2 {
		t.Errorf("this tree declares %d Cloud Run services, want 2: the portal's surface is the API and the minter (D2194), and every check here is written against those two", services)
	}
}

// TestOnlyTheAPIDecryptsHandovers: the reveal handover is stored encrypted under
// a KMS key in its own ring, and the API's account is the only identity that may
// use it. D2182 as amended puts the sealed bundle in the store the API writes
// and nowhere else, so the key that opens it is the API's and no other service in
// the project — the minter above all, which holds the signer role on another ring
// and nothing here.
//
// The mutations this fails on: the grant's member spelled with another account,
// a second member on the same grant, a second block holding useToEncrypt or
// useToDecrypt, the sealer grant moved onto the SIGNER's ring (which would make
// the API an identity in the ring the signer grant is on), and either of the two
// defaults the grant is decided by changed in variables.tf.
func TestOnlyTheAPIDecryptsHandovers(t *testing.T) {
	grant := tfRootBlock(t, servicesRoot, `resource "google_kms_key_ring_iam_member" "api_seals"`)
	tfAttrsAre(t, grant, "the handover grant in "+servicesRoot, []struct{ attr, value string }{
		{"key_ring_id", "google_kms_key_ring.handover.id"},
		{"role", `"projects/${var.project}/roles/${var.handover_role}"`},
		{"member", "google_service_account.api.member"},
	}, "the API encrypts the handover it stores and decrypts it once, 24 hours later (D2182): one member and one role, on the handover's own ring — a second member is a second identity that can open every sealed reveal, and a grant on the signing ring is the API inside the ring the signer role is granted on (Check 571)")

	// The ring and the key in it are this root's own, and the name they share is
	// the one variable: the key's whole id is its ring's path plus that name, so a
	// second key in this ring is a key the grant above reaches whichever block
	// created it.
	ring := tfRootBlock(t, servicesRoot, `resource "google_kms_key_ring" "handover"`)
	tfAttrsAre(t, ring, "the handover ring in "+servicesRoot, []struct{ attr, value string }{
		{"project", "var.project"},
		{"name", "var.handover_key"},
		{"location", "var.region"},
	}, "the handover key is in a ring of its own rather than in the signing ring (D459, D2194): the signer role is granted at the RING, so a key ring holding both an encryption key and a signing key is a ring where one grant reaches both")
	tfAttrsAre(t, tfRootBlock(t, servicesRoot, `resource "google_kms_crypto_key" "handover"`), "the handover key in "+servicesRoot, []struct{ attr, value string }{
		{"name", "var.handover_key"},
	}, "the key and its ring carry the one name this variable holds, so the key id the deploy passes to the API is the ring's path plus it, and a second name here is a second literal the deploy has to know")

	// Every block in the tree that names the handover ring or the handover key is
	// one of the three that are allowed to, so a second reach is refused whatever
	// it is called.
	reach := map[string]string{
		tfNames(servicesRoot, `resource "google_kms_key_ring" "handover"`):             "the ring itself",
		tfNames(servicesRoot, `resource "google_kms_crypto_key" "handover"`):           "the key itself",
		tfNames(servicesRoot, `resource "google_kms_key_ring_iam_member" "api_seals"`): "the API's one grant on it",
	}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			compact := tfCompact(block)
			if !strings.Contains(compact, "google_kms_key_ring.handover.") && !strings.Contains(compact, "google_kms_crypto_key.handover.") {
				continue
			}
			if _, ok := reach[tfNames(path.Dir(rel), tfHeader(block))]; !ok {
				t.Errorf("%s reaches the handover ring from a block that is not one of the three the plan allows:\n%s\nthe API is the only identity that seals a handover and the only one that opens it (D2182), so a second block naming the ring or the key — in either root — is a second reach of every sealed reveal in the store (Check 571)", rel, strings.TrimSpace(block))
			}
		}
	}

	// And the permission, over the whole tree rather than over the one role the
	// plan names: encrypt and decrypt are what the sealer role holds and nothing
	// else in this tree writes either.
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			compact := tfCompact(block)
			if !strings.Contains(compact, "useToEncrypt") && !strings.Contains(compact, "useToDecrypt") {
				continue
			}
			if tfHeader(block) != `resource "google_project_iam_custom_role" "handover"` {
				t.Errorf("%s writes an encrypt or decrypt permission outside the handover role:\n%s\nthe sealed handover is the API's alone (D2182): a second role or binding carrying useToDecrypt is a second identity that can open every reveal in the store, which is the one thing the encryption is for (Check 571)", rel, strings.TrimSpace(block))
			}
		}
	}

	// And what the grant actually REACHES: both of its attributes name variables,
	// so the ring the API may decrypt in and the role it holds there are these two
	// defaults, and nothing else in the tree reads them.
	for _, d := range []struct{ name, want string }{
		{"handover_key", `"fugaro-portal-handover"`},
		{"handover_role", `"fugaroHandoverSealer"`},
	} {
		if got := tfDefaultLiteral(tfVarBlock(t, servicesRoot, d.name)); got != d.want {
			t.Errorf("services/'s %s default reads as %s, want %s:\nthe sealer grant names both variables, so a default changed here moves the grant to another ring or hands the API another role with every block above still reading as written (D2182, Check 571)", d.name, got, d.want)
		}
	}
}

// TestRingHoldsOneEnvironmentsKey: one ring holds one environment's key, which is
// what D459's open question is decided as. An apply carries an environment, the
// ring it creates is that environment's, and the one key inside it is the id the
// map names for it — so production's ring holds only the production key and
// staging's only the staging key, and the signer grant, made at the ring, reaches
// exactly one.
//
// The mutations this fails on: an `environment` with a default (an apply that
// names nothing mints production's key), a validation admitting an environment
// the map does not name (or a map naming one the validation refuses), the ring
// named by a literal in either root, and the key block ranging over the map with
// a for_each or a count, which puts both environments' keys in one ring.
func TestRingHoldsOneEnvironmentsKey(t *testing.T) {
	for _, root := range []string{keyringRoot, servicesRoot} {
		block := tfVarBlock(t, root, "environment")
		if got := tfDefaultLiteral(block); got != "" {
			t.Errorf("%s gives `environment` the default %s:\nthe environment decides which key id this apply creates and which ring it is created in, so a default is what lets an apply mint one environment's key while the operator believes it named the other (D459)", root, got)
		}
	}

	// The two roots have to admit the SAME set, and it is the set the map names:
	// keyring/ reads it out of the map it keys the key id with, services/ writes
	// it as the list of environments it will grant the signer role for, and a
	// value one admits and the other does not is a root that cannot be planned
	// against the other.
	validation, ok := tfNestedBlock(tfVarBlock(t, keyringRoot, "environment"), "validation")
	if !ok {
		t.Fatalf("keyring/'s `environment` writes no validation block, so an apply of this root accepts any string at all and creates a ring and a key id for it")
	}
	condition, _ := tfAttr(validation, "condition")
	if tfCompact(condition) != `contains(keys(var.keys),var.environment)` {
		t.Errorf("keyring/'s validation condition reads %s, want contains(keys(var.keys), var.environment):\nthe environments an apply may name are exactly the entries of the map the key's id comes from, and a condition written as a list of literals is a second place to keep the two in step (D459)", condition)
	}
	servicesValidation, ok := tfNestedBlock(tfVarBlock(t, servicesRoot, "environment"), "validation")
	if !ok {
		t.Fatalf("services/'s `environment` writes no validation block, so the ring it reads and the role it grants are decided by whatever string an apply carries")
	}
	servicesCondition, _ := tfAttr(servicesValidation, "condition")
	held := tfRingEnvironments(t, ".")
	if got := tfStringList(servicesCondition); !slices.Equal(got, held) {
		t.Errorf("services/'s validation admits %v and keyring/'s `keys` map names %v:\nthe two roots describe ONE set of environments — the one an apply carries, the key id it creates and the ring services/ grants the signer role on — so a value one admits and the other does not is a ring services/ reads and keyring/ never created (D459)", got, held)
	}

	// And the ring both roots name, which is the environment's: the same
	// expression in each, so what keyring/ creates is what services/ reads.
	ringName, _ := tfAttr(tfRootBlock(t, keyringRoot, `resource "google_kms_key_ring" "signing"`), "name")
	dataName, _ := tfAttr(tfRootBlock(t, servicesRoot, `data "google_kms_key_ring" "signing"`), "name")
	if tfCompact(ringName) != tfCompact(dataName) {
		t.Errorf("keyring/ names its ring %s and services/ reads %s:\nthe signer grant is made on the ring services/ reads, so the two roots write one expression — a ring named differently in either is a grant on a ring that does not exist (Check 174, D459)", ringName, dataName)
	}
	if !strings.Contains(ringName, "var.environment") {
		t.Errorf("the key ring's name reads %s and names no environment:\none ring holds one environment's key (D459), and the name is the half of that a plan's review screen shows — a ring named the same in both environments is a ring an apply of either silently adds a second key to", ringName)
	}

	// And the one key inside it: the id the map gives the environment an apply
	// carries, created once rather than once per entry of the map.
	key := tfRootBlock(t, keyringRoot, `resource "google_kms_crypto_key" "signing"`)
	tfAttrsAre(t, key, "the signing key in "+keyringRoot, []struct{ attr, value string }{
		{"key_ring", "google_kms_key_ring.signing.id"},
		{"name", "var.keys[var.environment]"},
	}, "the ring holds one environment's key, and it is the id var.keys names for the environment this apply carries (D459): the published verifier key ids are the map's values, so the ring an id lives in is the environment's and not a second name")
	for _, attr := range []string{"for_each", "count"} {
		if got, ok := tfAttr(key, attr); ok {
			t.Errorf("the signing key block writes %s = %s:\na block that ranges over the map creates every environment's key in ONE ring, which is the state D459 decides against — and the signer role, granted at the ring, cannot tell the two apart (D459)", attr, got)
		}
	}
}

// TestFirestoreRulesDenyClients: the portal's clients reach no document
// directly. D2188 fixes the SPA's data path as the API and only the API, so the
// Firestore rules the database is deployed with deny every client read and every
// client write — the store is reachable through the API's account (which
// TestOnlyTheMinterHoldsTheSignerRole holds to three reaches) and through
// nothing else.
//
// The mutations this fails on: any rule in the file that is not the denial (an
// `if true`, a `request.auth != null` for signed-in users, a rule that admits
// one collection), a rule the reader never sees because it sits behind a `#`,
// `//` or `/*` written inside a quoted string on its line — which is the whole
// reason this file is read with rulesStripComments rather than with the HCL
// reader: `#` is a comment to HCL and text to the rules compiler, so the wrong
// reader drops the rest of that line, the smuggled `allow read, write: if true;`
// is applied by the deploy, and the file's own denial further down passes this
// check — the file losing its skeleton, the database resource pointing at
// another database or another mode, and a second database beside it, whose own
// documents these rules are not deployed on.
func TestFirestoreRulesDenyClients(t *testing.T) {
	database := tfRootBlock(t, servicesRoot, `resource "google_firestore_database" "portal"`)
	tfAttrsAre(t, database, "the Firestore database in "+servicesRoot, []struct{ attr, value string }{
		{"name", `"portal"`},
		{"type", `"FIRESTORE_NATIVE"`},
	}, "the portal's documents live in their own native Firestore database, separate from the installation's realtime database (spec §4): the rules below are deployed on this one, and a database named for another purpose is a database whose rules nobody reviewed")

	// And that this one is the tree's only database, which is what makes the file
	// below the whole of the fence: a database is a store with rules of its own,
	// uploaded by its own deploy, so a second google_firestore_database — in
	// either root, or in a file beside this one — is a store none of the rules
	// read here is deployed on (independent review of this file; the Firestore
	// census the Cloud Run services already have).
	wantDatabase := tfNames(servicesRoot, `resource "google_firestore_database" "portal"`)
	databases := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_firestore_database"`) {
				continue
			}
			databases++
			if got := tfNames(path.Dir(rel), tfHeader(block)); got != wantDatabase {
				t.Errorf("%s declares the Firestore database %s, and this tree holds one:\n%s\nevery document the portal holds is behind the rules file this test reads and the API's checks (D2188), so a second database is a second store with rules nothing here read", rel, got, strings.TrimSpace(block))
			}
		}
	}
	if databases != 1 {
		t.Errorf("this tree declares %d Firestore databases, want 1: the portal's documents live in one native database, and the denial above is the whole of what stands between every organisation's records and a client that reaches the store directly (D2188, Check 570)", databases)
	}

	raw, err := os.ReadFile(portalRulesPath)
	if err != nil {
		t.Fatalf("reading %s: %v -- the rules this database is deployed with are the whole of what stops a client reading another organisation's documents", portalRulesPath, err)
	}
	body := rulesStripComments(string(raw))
	compact := tfCompact(body)
	for _, want := range []struct{ text, why string }{
		{"servicecloud.firestore", "the file's rules have to be the Firestore service's, or the deploy refuses the file and the database keeps the rules it had"},
		{"match/databases/{database}/documents", "a rules file with no match on the portal's documents path matches nothing, which denies by default and is indistinguishable from a file nobody wrote"},
	} {
		if !strings.Contains(compact, want.text) {
			t.Errorf("ee/portal/firestore.rules does not write %s: %s", want.text, want.why)
		}
	}

	allows := tfFirestoreAllows(body)
	if len(allows) == 0 {
		t.Fatalf("ee/portal/firestore.rules writes no allow statement at all: an empty file denies every client by default, and this rule is the read that says so in as many words, so a file that says nothing is one no reviewer can tell from a truncation")
	}
	for _, allow := range allows {
		if slices.Contains(tfFirestoreDenials, allow) {
			continue
		}
		t.Errorf("ee/portal/firestore.rules grants %q, and every rule in this file denies:\nthe SPA reaches its data through the API and never through the client SDK (D2188, Check 570), so a rule that admits a client — `if true`, or a `request.auth` that admits any signed-in person — hands over every organisation's billing, every reveal bundle and the audit trail without one of the API's checks or its audit line", allow)
	}
}

// tfFirestoreDenials are the spellings of the one rule this file may hold: a
// denial of every read and every write, which is what D2188 leaves a client.
var tfFirestoreDenials = []string{
	"allowread,write:iffalse;",
	"allowread:iffalse;",
	"allowwrite:iffalse;",
}

// portalRulesPath is the rules file from this package's directory: it lives beside
// the SPA it serves rather than in this tree, because the deploy that uploads it
// is the portal's (P9.2).
const portalRulesPath = "../../portal/firestore.rules"

// rulesStripComments removes the comments a rules file has — `//` to the end of
// its line, and `/* */` wherever it starts — while leaving its quoted strings
// alone.
//
// It is not tfStripComments, and the difference is not cosmetic: HCL takes `#`
// as a comment and has no single-quoted strings, rules have no `#` at all and
// take both quote forms, and what a reader gets wrong here is a rule it never
// judges. Read with the HCL reader, `function tag() { return 'a#'; } allow read,
// write: if true;` loses everything from the `#` on, so the allow the deploy
// applies is invisible to the check above and the file's own denial on the next
// line is what it passes on — the client the rules were supposed to shut out
// reads every organisation's documents, with a test that says the opposite. The
// same happens for `//` and for `/*`, which is why the strings are skipped
// before either is looked for.
func rulesStripComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		switch c := src[i]; {
		case c == '\'' || c == '"':
			j := rulesSkipString(src, i, c)
			b.WriteString(src[i:j])
			i = j
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

// rulesSkipString answers the index just past the closing quote of the string
// starting at i, whose quote character is q, and the end of the file if there is
// none. A backslash escapes the character after it, so an escaped quote does not
// end the string; a rules string interpolates nothing, so its own quote is the
// only one that does.
func rulesSkipString(src string, i int, q byte) int {
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case q:
			return j + 1
		}
	}
	return len(src)
}

// tfFirestoreAllows answers every `allow` statement in a rules file, compacted,
// which is the whole of what a rules file grants: what a client may do is written
// nowhere else, so this is the set the rule above judges.
func tfFirestoreAllows(body string) []string {
	var out []string
	for i := 0; ; {
		j := strings.Index(body[i:], "allow")
		if j < 0 {
			return out
		}
		start := i + j
		end := strings.IndexByte(body[start:], ';')
		if end < 0 {
			return append(out, tfCompact(body[start:]))
		}
		out = append(out, tfCompact(body[start:start+end+1]))
		i = start + end + 1
	}
}

// TestNoServiceRunsAsADefaultAccount: every Cloud Run service in this tree runs
// as an account the same root declares, and every scheduled call is made by one.
// A revision whose template names no account runs as the project's default
// Compute Engine account, which the project created with Editor — so with D2194
// putting the signer in the same project, a service that runs as it is a service
// whose identity is a predefined role nobody wrote in this tree.
//
// The mutations this fails on: a service with its `service_account` line deleted,
// an account_id spelled as a literal address rather than declared here, a
// scheduled job whose token is minted for the default account, and any of the
// three spellings that address has.
func TestNoServiceRunsAsADefaultAccount(t *testing.T) {
	declared := map[string]bool{}
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if strings.Contains(tfHeader(block), `"google_service_account"`) {
				declared[tfNames(path.Dir(rel), tfHeader(block))] = true
			}
		}
	}
	if len(declared) < 3 {
		t.Fatalf("this tree declares %d service account(s), and the portal's infrastructure needs the API's, the minter's and the scheduler's: a check that read them out of an empty map would pass every service below", len(declared))
	}

	// accountName answers the declared account a block's attribute names, and
	// whether it names one at all: the read is an attribute of the block, so a
	// description or a labels map spelling an account out is not one.
	accountName := func(t *testing.T, root, got string) (string, bool) {
		t.Helper()
		rest, ok := strings.CutPrefix(tfCompact(got), "google_service_account.")
		if !ok {
			return "", false
		}
		name, ok := strings.CutSuffix(rest, ".email")
		if !ok {
			return "", false
		}
		return tfNames(root, `resource "google_service_account" "`+name+`"`), true
	}

	services := 0
	for _, rel := range tfFiles(t) {
		root := path.Dir(rel)
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			header := tfHeader(block)
			switch {
			case strings.Contains(header, `"google_cloud_run_v2_service"`):
				services++
				template, ok := tfNestedBlock(block, "template")
				if !ok {
					t.Errorf("%s declares a Cloud Run service with no template block: the account its revision runs as is the project's default, which is the account this rule refuses", rel)
					continue
				}
				got, ok := tfAttr(template, "service_account")
				if !ok {
					t.Errorf("%s declares a Cloud Run service whose template names no service_account:\nthe revision then runs as the project's default Compute Engine account, created with Editor in a project that holds the signer (D2194), so the service's identity is a predefined role no block in this tree writes (Check 571)", rel)
					continue
				}
				name, ok := accountName(t, root, got)
				if !ok || !declared[name] {
					t.Errorf("%s declares a Cloud Run service that runs as %s, which this root does not declare:\nthe identity a revision runs as is the whole of what its code can do, and an account named here rather than created here — a default one above all — is one no block in this tree grants anything to and no review of it reads (Check 571)", rel, got)
				}
			case strings.Contains(header, `"google_cloud_scheduler_job"`):
				target, ok := tfNestedBlock(block, "http_target")
				if !ok {
					continue
				}
				token, ok := tfNestedBlock(target, "oidc_token")
				if !ok {
					t.Errorf("%s declares a scheduled job with no oidc_token block: the API verifies that token's account (P1.3), and a job that sends none is a call the API refuses", rel)
					continue
				}
				got, _ := tfAttr(token, "service_account_email")
				name, ok := accountName(t, root, got)
				if !ok || !declared[name] {
					t.Errorf("%s declares a scheduled job whose token is minted for %q, which this root does not declare:\nthe account is half of what the API verifies (P1.3, F33), so an account named here rather than created here — the project's default above all — is one the API's list does not carry (Check 571)", rel, got)
				}
			}
		}
	}
	if services != 2 {
		t.Errorf("this tree declares %d Cloud Run services, want 2: the API and the minter are the portal's whole surface (D2194), and both run as their own declared account", services)
	}

	// And the address itself, in every spelling a configuration carries it: a
	// literal address is an account this tree does not declare even when the
	// attribute is written the long way round.
	for _, rel := range tfFiles(t) {
		compact := tfCompact(tfSource(t, rel))
		for _, spelling := range []string{
			"-compute@developer.gserviceaccount.com",
			"@appspot.gserviceaccount.com",
			`service_account="default"`,
		} {
			if strings.Contains(compact, spelling) {
				t.Errorf("%s writes %s: a service that runs as the project's default account runs as a predefined role the project created it with — Editor, in a project that holds the signer (D2194) — and every grant this suite reads is made to a declared account (Check 571)", rel, spelling)
			}
		}
	}
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
// override in JSON naming the API in the signer grant, a variable file naming
// another role and an auto-loaded one in JSON, a second plain HCL file in a root
// — admitted, because the rule is one FORM per root and not one file — the
// defaults rule over a tree that changed one of them, the same rule against a
// block that names the wanted literal in a comment, a string or a heredoc
// instead of in its default, the reads of a block's own attributes against a
// grant and a key naming what a check looks for in free text, a grant written
// under a string whose interpolation holds a quote — which a reader that ended
// the string at the first quote it saw did not see at all — a second key beside
// the one block the checks read, the environments of `keys`' default in the
// spellings a map key has, the third root above, which is not, and the rules
// file's own reader over a line that hides a granting `allow` behind a `#`, a
// `//` or a `/*` written inside a quoted string.
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
				"services/extra.tf.json": `{"resource":{"google_kms_key_ring_iam_binding":{"second":{"role":"projects/x/roles/fugaroKeySigner","members":["serviceAccount:api@example.iam.gserviceaccount.com"],"key_ring_id":"x"}}}}`,
			},
			refused: []string{"services/extra.tf.json"},
		},
		{
			name: "an override in JSON naming the API in the signer grant",
			write: map[string]string{
				"services/main.tf": "resource \"google_kms_key_ring_iam_member\" \"minter_signs\" {\n" +
					"  member = google_service_account.minter.member\n}\n",
				"services/override.tf.json": `{"resource":{"google_kms_key_ring_iam_member":{"minter_signs":{"member":"${google_service_account.api.member}"}}}}`,
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
				"services/main.tf":   "resource \"google_service_account\" \"api\" {}\n",
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

	// And the reader the portal's rules file is read with, over a file this test
	// writes. The rules language has no `#` and both quote forms, and the check
	// above is a rule about what the file GRANTS, so a reader that dropped the
	// rest of a line at a `#` inside a string was a check passing on a denial
	// while an allow on the same line was applied. The smuggled allow is written
	// first, so a reader that lost it reads the file's own denial and says the
	// file is exactly what it should be.
	for _, hidden := range []string{"'a#'", "'a//'", "'a/*'", `"a#"`, `"a//"`, `"a/*"`} {
		t.Run("an allow hidden inside "+hidden, func(t *testing.T) {
			root := t.TempDir()
			tfWrite(t, root, "portal/firestore.rules", "service cloud.firestore {\n  match /databases/{database}/documents {\n"+
				"    function tag() { return "+hidden+"; } allow read, write: if true;\n"+
				"    match /{document=**} { allow read, write: if false; }\n  }\n}\n")
			raw, err := os.ReadFile(filepath.Join(root, "portal", "firestore.rules"))
			if err != nil {
				t.Fatalf("reading the scratch rules file: %v", err)
			}
			allows := tfFirestoreAllows(rulesStripComments(string(raw)))
			if len(allows) != 2 || slices.Contains(tfFirestoreDenials, allows[0]) {
				t.Errorf("a rules file whose denial follows an `allow read, write: if true;` on the same line as %s reads as %v, want the smuggled allow first and the denial second: `#`, `//` and `/*` inside a quoted string are text to the rules compiler, so a reader that took any of them for a comment dropped the allow — which the deploy applies — and left the file's own denial to pass the check above", hidden, allows)
			}
		})
	}

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
			t.Errorf("a third entry in `keys`' default reads as %v, which is the held set: the key block names the id this map gives the environment an apply carries, so the third environment is a third key an apply creates in a ring the minter's role is granted on and no check reads", got)
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
				t.Errorf("a third environment written as %q reads as %v, which is the held set: the key block names the id this map gives the environment an apply carries, whatever the entry is spelled with, and the signer role is granted on that environment's ring, so the third one is a key the minter can sign with that no check reads", strings.TrimSpace(written), got)
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
