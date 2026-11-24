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
// Two tests hold the readers themselves, on a source no file here writes: a
// header spaced the other ways HCL reads as one space
// (TestInfraTheReadersReadWhitespaceTheWayHCLDoes, which the cramped spelling's
// own test does not cover), and an exported account key appended to a copy of
// main.tf (TestInfraTheSweepSeesAnExportedAccountKey, the second way to sign
// that writes no attribute a grant is written with).
//
// The key's purpose, algorithm and protection level are pinned as the key
// service's suite pins its own (A3), and the rest of the acceptance — the signer
// role, the key-set bucket, the four sign-in methods, the JWKS route and the
// expiry alert — is pinned here too, because each is a sentence of the plan's
// acceptance that a diff could otherwise falsify with every check green.
package identitysvc

import (
	"fmt"
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

// tfBlockHeaders are the ways a top-level block begins in a root, and the kinds
// every sweep here walks: a block whose text ran on into the one below it would
// be read as part of that block's body, which is how a `module` granting a role,
// an `import` adopting a key or a `locals` holding one would be read as the
// sanctioned block above it. `module`, `import`, `locals`, `moved`, `removed`
// and `check` are blocks like any other.
var tfBlockHeaders = []string{
	`resource "`, `data "`, `variable "`, `output "`, `provider "`,
	`module "`, `import {`, `locals {`, `moved {`, `removed {`, `check "`,
	`terraform {`,
}

// tfTopLevelHeaders is the same list as a cut list, preceded by the newline that
// makes a header a header: the byte in front of a top-level block is the end of
// the block above it, and a cut found mid-line would end a block at a name that
// only looks like one.
var tfTopLevelHeaders = tfCutHeaders(tfBlockHeaders)

// tfCutHeaders puts a newline in front of each header.
func tfCutHeaders(headers []string) []string {
	out := make([]string, 0, len(headers))
	for _, header := range headers {
		out = append(out, "\n"+header)
	}
	return out
}

// tfBlocks answers every block in one file whose header starts with the given
// text. The cut is the next top-level block header, which is enough for a sweep
// over those blocks.
func tfBlocks(src, header string) []string {
	return tfBlocksUntil(src, header, tfTopLevelHeaders)
}

// tfBlocksUntil answers every block whose header starts with the given text and
// whose body ends at the first of the given markers. Both the header and the
// markers are found in CODE alone, so a header written inside a description is a
// sentence and not a block.
func tfBlocksUntil(src, header string, cuts []string) []string {
	code := tfCodeMask(src)
	var out []string
	for i := 0; ; {
		start, end := tfFindHeader(src, code, header, i)
		if start < 0 {
			return out
		}
		stop := len(src)
		for _, cut := range cuts {
			if k, _ := tfFindHeader(src, code, cut, end); k >= 0 && k < stop {
				stop = k
			}
		}
		out = append(out, src[start:stop])
		i = end
	}
}

// tfFindHeader answers the index of the next header at or after from that writes
// the given header's tokens, and the index just past it, or -1, -1 for none. The
// tokens are the keyword and each name it writes, and what may stand between
// them is any whitespace HCL reads as one — including none at all, because HCL
// does not need a space: `resource"google_x""y"{` is
// `resource "google_x" "y" {` written another way, `terraform validate` accepts
// both, and this suite reads a root as text rather than through a parser, so a
// finder that looked for the space alone would say nothing at all about a grant,
// an import or a module written the cramped way. The first token is matched as a
// word of its own — a name ending in a keyword, `google_resource"x"{`, is no
// resource block — and a leading newline is the cut list's way of saying the
// header begins a line, so it is read as that rather than as part of the word.
func tfFindHeader(src string, code []bool, header string, from int) (int, int) {
	tokens := strings.Fields(header)
	if len(tokens) == 0 {
		return -1, -1
	}
	for i := from; i < len(src); {
		start := tfFindCode(src, code, tokens[0], i)
		if start < 0 {
			return -1, -1
		}
		keyword := start
		if tokens[0][0] == '\n' {
			keyword++
		}
		end := start + len(tokens[0])
		ok := keyword == 0 || !tfIdentChar(src[keyword-1])
		for _, token := range tokens[1:] {
			if !ok {
				break
			}
			for end < len(src) && tfSpace(src[end]) {
				end++
			}
			if !strings.HasPrefix(src[end:], token) {
				ok = false
				break
			}
			end += len(token)
		}
		if ok {
			return start, end
		}
		i = start + 1
	}
	return -1, -1
}

// tfSpace reports whether c is whitespace HCL reads as a separator between two
// tokens.
func tfSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
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

// tfHeader answers a block's header — its kind and the names it writes — in one
// spelling, one space between those tokens, however the file spaced them:
// `resource"google_x""y"{` and `resource "google_x" "y" {` are one block, and
// the blocks below are looked up and sanctioned by this spelling.
func tfHeader(block string) string {
	var tokens []string
	for i := 0; i < len(block); {
		switch c := block[i]; {
		case c == '{':
			return strings.Join(tokens, " ")
		case tfSpace(c):
			i++
		case c == '"':
			j, ok := tfSkipString(block, i)
			if !ok {
				j = len(block)
			}
			tokens = append(tokens, block[i:j])
			i = j
		default:
			j := i
			for j < len(block) && !tfSpace(block[j]) && block[j] != '{' && block[j] != '"' {
				j++
			}
			tokens = append(tokens, block[i:j])
			i = j
		}
	}
	return strings.Join(tokens, " ")
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

// tfNestedNames answers the name of every block written one level inside a block
// body — `env`, `volume_mounts`, `startup_probe` — as the words they are rather
// than by asking for the names this suite expects: a block nobody here has named
// is still a block, and a check that read only the names it knew would pass on a
// body holding one it did not. A word followed by `{` is a block; a word
// followed by `=` is an attribute, and its value is left to the reader that
// wants it.
func tfNestedNames(block string) []string {
	body, ok := tfBlockBody(block)
	if !ok {
		return nil
	}
	var names []string
	depth := 0
	for i := 0; i < len(body); {
		switch c := body[i]; {
		case c == '"':
			j, ok := tfSkipString(body, i)
			if !ok {
				return names
			}
			i = j
		case c == '<' && strings.HasPrefix(body[i:], "<<"):
			j, ok := tfSkipHeredoc(body, i)
			if !ok {
				return names
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
			k := j
			for k < len(body) && tfSpace(body[k]) {
				k++
			}
			if k < len(body) && body[k] == '{' {
				names = append(names, body[i:j])
			}
			i = j
		default:
			i++
		}
	}
	return names
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
		for _, block := range tfBlocksUntil(tfSource(t, rel), header, tfTopLevelHeaders) {
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

// tfRoute is one way an identity reaches the signer's account without a grant
// this suite reads as one, and the sentence it is refused with.
type tfRoute struct{ needle, what string }

// tfAccountRoutes is every such way, and the whole of them: an exported account
// key can be downloaded and carries the useToSign the ring grant gives the
// account, so it is a second way to sign with the assertion key and its private
// half lands in the state file; a grant on the account itself is a second
// identity that mints with the signer role; and a service account address
// written as text is an identity named by no reference.
//
// The first two name resource types, so the sweep's allowlist refuses a block
// that writes one — a resource whose only suspicious part is its type writes
// none of the attributes the sweep also tests, which is how an exported account
// key would otherwise pass it. All three are searched for by their text in every
// file of the root, and tfGrants reads the same list out of a block's header,
// where a resource type is what appears (A3, D2792, O7).
var tfAccountRoutes = []tfRoute{
	{"google_service_account_key", "an exported account key can be downloaded and carries the ring's useToSign with it, which is a second way to sign with the assertion key, and its private half is written into the state file, where whoever reads the state holds the account"},
	{"google_service_account_iam", "a grant on the account itself is a second identity that mints with the signer role"},
	{"iam.gserviceaccount.com", "a service account address written as text names an identity the sweep cannot see, and a job or a service handed it runs as the signer"},
}

// tfGrants reports whether a block hands an identity a role, or reaches the
// signer's account without a grant, whatever the block is called. It is
// deliberately loose — an IAM resource type, an attribute a grant is written
// with, or one of the types tfAccountRoutes names — and it is loose in the safe
// direction: the sweep's other half is a list of the grants this root is allowed
// to hold, so a block the sweep thinks grants something and finds unlisted
// fails, while being wrong about a block that grants nothing costs only a
// message. The attribute test is on the compacted body, because `role="x"` and
// `role = "x"` are one grant.
func tfGrants(header, compact string) bool {
	if strings.Contains(header, "_iam_") {
		return true
	}
	for _, route := range tfAccountRoutes {
		if strings.Contains(header, route.needle) {
			return true
		}
	}
	for _, attr := range []string{"member=", "members=", "role=", "role_id=", "permissions=", "policy_data="} {
		if strings.Contains(compact, attr) {
			return true
		}
	}
	return false
}

// tfAccountRouteWrites answers the routes to the signer's account written in one
// source, in the order tfAccountRoutes holds them. It takes the text rather than
// reading a file so that a test can hand it a copy with one more block in it,
// which is the only way to show the route search reading a block this root does
// not write.
func tfAccountRouteWrites(src string) []tfRoute {
	compact := tfCompact(src)
	var out []tfRoute
	for _, route := range tfAccountRoutes {
		if strings.Contains(compact, route.needle) {
			out = append(out, route)
		}
	}
	return out
}

// tfUnsanctionedGrants answers every block of one source that grants a role — or
// reaches the signer's account, which tfGrants reads as the same thing — and is
// not on the list of grants this root may hold, as the block's own text. It takes
// the text for the same reason tfAccountRouteWrites does: the sweep's refusal is
// the other half a test has to show firing on an appended block.
func tfUnsanctionedGrants(src string) []string {
	var out []string
	for _, block := range tfBlocks(src, `resource "`) {
		header := tfHeader(block)
		if !tfGrants(header, tfCompact(block)) {
			continue
		}
		if _, ok := tfSanctionedGrants[header]; !ok {
			out = append(out, strings.TrimSpace(block))
		}
	}
	return out
}

// tfHeaders answers the normalised header of every block of one kind in a
// source, which is what a check counts, compares and looks a block up by.
func tfHeaders(src, kind string) []string {
	out := make([]string, 0, 8)
	for _, block := range tfBlocks(src, kind) {
		out = append(out, tfHeader(block))
	}
	return out
}

// tfListCounts renders the blocks that may name an identity and how often each
// may, sorted, so that a failure names the whole list rather than only the block
// it refused.
func tfListCounts(refs map[string]int) string {
	lines := make([]string, 0, len(refs))
	for header, n := range refs {
		times := fmt.Sprintf("%d times", n)
		if n == 1 {
			times = "once"
		}
		lines = append(lines, "  "+header+" — "+times)
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
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

// tfSecondForms are the whole of what a root of one form of configuration never
// holds, whichever way the block is spaced: a module creates resources no check
// in this suite can name, an import adopts a resource that already exists, and a
// moved block renames one, and each changes what an apply does with no block
// below changing.
var tfSecondForms = []struct{ header, what string }{
	{`module "`, "a `module`"},
	{"import {", "an `import`"},
	{"moved {", "a `moved`"},
}

// TestInfraTheRootIsOneFormOfConfiguration: the root is plain HCL in one form —
// no `.tf.json`, no override file, no variable file, and no `module`, `import` or
// `moved` block — and its four files are the ones the plan names. A second form
// is a place the key's algorithm, the signer role or a sign-in provider is
// decided where no check above changes.
func TestInfraTheRootIsOneFormOfConfiguration(t *testing.T) {
	want := []string{"auth.tf", "keyring.tf", "main.tf", "pairedlist.tf"}
	if got := tfFiles(t); !slices.Equal(got, want) {
		t.Errorf("the root's Terraform is %v, want %v: A3 creates auth.tf, keyring.tf and main.tf, K1 adds pairedlist.tf — the paired list's own database and the symmetric key it is sealed under — and a file added to this root later is added to this list in the same commit, so that the root's whole shape stays something this suite states (A3, K1)", got, want)
	}

	// A module, an import and a moved block are the same kind of second form,
	// one block at a time, and each is refused rather than read: a module
	// creates resources no block here names — the IAM module the key service
	// uses would grant a role the sweep in TestInfraTheSignerRoleIsTheNarrowestThatSigns
	// never sees — an import adopts a resource that already exists, a crypto key
	// whose material was generated outside Cloud KMS among them, past every
	// attribute check the tests below make, and a moved block renames a resource
	// those checks look up by name. This root declares none of the three.
	for _, rel := range tfFiles(t) {
		src := tfSource(t, rel)
		for _, kind := range tfSecondForms {
			for _, block := range tfBlocks(src, kind.header) {
				t.Errorf("%s declares %s block:\n%s\na module, an import and a moved block each change what this root applies with no block below changing, which is why the tests here read the root's blocks by name: a module creates resources no check here can name, an import adopts one that already exists, and a moved block renames one (A3, D2792)", rel, kind.what, strings.TrimSpace(block))
			}
		}
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
	// The root declares two keys and no more, each read by name: the signing key
	// below, and K1's symmetric key the paired list is sealed under (read by
	// TestInfraThePairedListIsSealedUnderItsOwnKey). A third is a key whose
	// algorithm, protection level and lifecycle no check here reads, and the
	// signing key must be the only one that signs — a second ASYMMETRIC_SIGN key
	// is a second route to material the signer's whole design keeps in one place.
	keys, signing := 0, 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if !strings.Contains(tfHeader(block), `"google_kms_crypto_key"`) {
				continue
			}
			keys++
			if purpose, _ := tfAttr(block, "purpose"); tfCompact(purpose) == `"ASYMMETRIC_SIGN"` {
				signing++
			}
		}
	}
	if keys != 2 || signing != 1 {
		t.Errorf("the root declares %d KMS crypto keys, %d of them ASYMMETRIC_SIGN, want 2 and 1: the signer role is granted on the signing key's ring, so a third key is one the signer signs with and a key whose algorithm, protection level and lifecycle no check here reads, and the paired list's key (K1) is symmetric and can neither sign nor be signed with (D2792, O7, K1)", keys, signing)
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

// TestInfraThePairedListIsSealedUnderItsOwnKey: K1's cloud side is its own
// database and its own key ring, the key is symmetric and encrypt-and-decrypt
// only, and the sealer role takes nothing of the signing ring.
//
// The mutations this fails on: the key moved into the signing ring (one ring, so
// one grant reaches both), its purpose or algorithm moved onto the signing pair,
// a lifecycle without prevent_destroy, a rotation period nobody set, a second
// database, a grant made at the project rather than at the ring, a permission
// beyond seal and open, and a second member on the grant.
func TestInfraThePairedListIsSealedUnderItsOwnKey(t *testing.T) {
	db := tfBlock(t, `resource "google_firebase_database_instance" "pairedlist"`)
	tfAttrsAre(t, db, "the paired-list database", []tfWant{
		{"project", "var.project"},
		{"instance_id", `"${var.project}-default-rtdb"`},
		{"type", `"DEFAULT_DATABASE"`},
	}, "the identity project has one Realtime Database and Firebase names it `<project>-default-rtdb`; the list is the only thing it holds, and a second instance or another type is a store no rule here and no document below describes (K1, D2798)")
	tfAttrsAre(t, tfNested(t, db, "lifecycle"), "the paired-list database's lifecycle", []tfWant{
		{"prevent_destroy", "true"},
	}, "every person's paired list lives in this instance, so losing it loses their projects: a destroy is a step a person takes on purpose and never an apply's side effect (K1, O16)")

	dbs := 0
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `resource "`) {
			if strings.Contains(tfHeader(block), `"google_firebase_database_instance"`) {
				dbs++
			}
		}
	}
	if dbs != 1 {
		t.Errorf("the root declares %d Realtime Database instances, want exactly 1: the identity project has one, the paired list is the only tree in it, and a second is a store no check here reads (K1)", dbs)
	}

	ring := tfBlock(t, `resource "google_kms_key_ring" "pairedlist"`)
	tfAttrsAre(t, ring, "the paired-list key ring", []tfWant{
		{"project", "var.project"},
		{"name", "var.pairedlist_keyring"},
		{"location", "var.region"},
	}, "the sealing key lives in a ring of its own beside the signing one, so the sealing grant and the signing grant are two reaches: one ring is a ring where both sit together (K1, D2182)")
	if got, signing := tfVarDefault(t, "pairedlist_keyring"), tfVarDefault(t, "keyring"); got == signing {
		t.Errorf("the paired-list key ring and the signing key ring both default to %s, want two names: a shared ring is one grant away from both, which is the separation D2182 makes everywhere else", got)
	}

	key := tfBlock(t, `resource "google_kms_crypto_key" "pairedlist"`)
	tfAttrsAre(t, key, "the paired-list key", []tfWant{
		{"key_ring", "google_kms_key_ring.pairedlist.id"},
		{"name", "var.pairedlist_key"},
		{"purpose", `"ENCRYPT_DECRYPT"`},
		{"rotation_period", "var.pairedlist_rotation"},
	}, "the list is sealed with a symmetric key in K1's own ring: an asymmetric key cannot take the uid as additional authenticated data, which is the whole of what stops one person's ciphertext being served as another's (K1, O16, D2796)")
	tfAttrsAre(t, tfNested(t, key, "version_template"), "the paired-list key's version_template", []tfWant{
		{"algorithm", `"GOOGLE_SYMMETRIC_ENCRYPTION"`},
		{"protection_level", `"SOFTWARE"`},
	}, "GOOGLE_SYMMETRIC_ENCRYPTION is the algorithm KMS generates the material of and never hands out, and SOFTWARE is the level Cloud KMS offers it at")
	tfAttrsAre(t, tfNested(t, key, "lifecycle"), "the paired-list key's lifecycle", []tfWant{
		{"prevent_destroy", "true"},
	}, "every person's list is sealed under this key, so removing it makes every stored list unopenable: that is a deliberate edit of this file and never an apply's side effect (K1)")
	if got := tfVarDefault(t, "pairedlist_rotation"); got != `"7776000s"` {
		t.Errorf("pairedlist_rotation defaults to %s, want \"7776000s\": a key that is never rotated is one whose compromise reaches every list ever sealed under it (K1, O4)", got)
	}

	role := tfBlock(t, `resource "google_project_iam_custom_role" "pairedlist_sealer"`)
	tfAttrsAre(t, role, "the paired-list sealer role", []tfWant{
		{"role_id", "var.pairedlist_sealer_role"},
		{"permissions", `["cloudkms.cryptoKeyVersions.useToEncrypt", "cloudkms.cryptoKeyVersions.useToDecrypt"]`},
	}, "the store seals and opens and does nothing else with the ring: these two permissions do nothing on an asymmetric key, and the signer's two do nothing on a symmetric one, so the separation is enforced by the permissions as well as by the rings (K1, D2182)")

	grant := tfBlock(t, `resource "google_kms_key_ring_iam_member" "signer_seals"`)
	tfAttrsAre(t, grant, "the paired-list sealing grant", []tfWant{
		{"member", "google_service_account.signer.member"},
		{"role", "google_project_iam_custom_role.pairedlist_sealer.id"},
		{"key_ring_id", "google_kms_key_ring.pairedlist.id"},
	}, "the one account that seals and opens is the service's own, and the grant is made on K1's ring and not at the project: a grant at the project would reach every key the project ever holds (K1, A3)")

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
// by a variable whose default is a predefined one — the service's template
// naming another account, a second block naming the signer's account (a job or a
// service running as it is the signer), an exported account key, and a grant on
// the account itself.
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

	for _, rel := range tfFiles(t) {
		for _, block := range tfUnsanctionedGrants(tfSource(t, rel)) {
			t.Errorf("%s holds a grant this root is not allowed:\n%s\nthe signer role is the only route to useToSign and it reaches one account, the service's own, so that a reader of the plan sees every identity that can mint an assertion (A3, D2792, O7). The whole list of grants this root may hold is:\n%s", rel, block, tfList(tfSanctionedGrants))
		}
	}

	// The account the signer runs as is named in exactly three places: the
	// service's template, which is the whole of what makes the service that
	// identity, and the two grants that name it as the member. Any other
	// reference is an identity able to mint that no block above would show — a
	// second Cloud Run service or job running as the same account IS the signer,
	// and the account key and account-IAM routes refused below are the two ways
	// to become it from outside.
	for _, rel := range tfFiles(t) {
		src := tfSource(t, rel)
		for _, kind := range tfBlockHeaders {
			for _, block := range tfBlocks(src, kind) {
				header := tfHeader(block)
				named := strings.Count(tfCompact(block), "google_service_account.signer.")
				if named == 0 {
					continue
				}
				if want, ok := tfSignerRefs[header]; !ok || want != named {
					t.Errorf("%s holds a reference to the signer's account this root is not allowed:\n%s\nthe signer role reaches one account, and every block naming it is a block that runs as it, grants it or exports it — so the whole list of places this root may name it is:\n%s", rel, strings.TrimSpace(block), tfListCounts(tfSignerRefs))
				}
			}
		}
	}

	// The service's one reference is its template's `service_account`, which is
	// read above: the same count one block further out would allow the reference
	// to move anywhere else in the service — an env value, a second nested
	// block — and the service would stop being the account while the count above
	// stayed at one.
	if got := strings.Count(tfCompact(template), "google_service_account.signer."); got != 1 {
		t.Errorf("the signer service's template names the signer's account %d times, want exactly 1, in its service_account attribute: the template's account is what the service IS, and a reference to the account anywhere else in it is a use of the signer role that no check here reads (A3)", got)
	}

	// Three ways to the account are refused outright rather than counted, because
	// none of them is a block this root may write however it is spelled: an
	// account key exports the account's private half into the state file and,
	// downloaded, is a second way to sign — so the sweep above reads its type as
	// a grant and refuses it through the allowlist as well — a grant on the
	// account itself is a second identity that mints with the signer role — an
	// impersonator's token carries it — and an address written as text is an
	// identity named by no reference, which is what a job or a service running as
	// the signer would be and the one of the three no block here writes.
	for _, rel := range tfFiles(t) {
		for _, route := range tfAccountRouteWrites(tfSource(t, rel)) {
			t.Errorf("%s writes %s: the signer's account is the only holder of the signer role and the private half of the signing key never leaves Cloud KMS — %s (A3, D2792, O7, .claude/rules/ee.md)", rel, route.needle, route.what)
		}
	}
}

// tfSanctionedGrants is every block of this root that may grant a role, and the
// whole of it. A `resource` block is every one of them because the root declares
// no `module`, no `import` and no `moved` block —
// TestInfraTheRootIsOneFormOfConfiguration refuses those, and each of the three
// is a grant this sweep could not see. A count of one resource type would miss a
// second holder written another way — the signer role granted with
// google_kms_key_ring_iam_binding or an `_iam_policy` instead of `_iam_member`,
// a second custom role, a plain `user:` added beside the signer's account, a
// grant whose role is a variable defaulting to a predefined role — so the
// discriminator is "this block grants a role", whatever it is called, and the
// answer for a grant nobody has reviewed is this list's absence. Each of the
// them is read as the attributes it writes above, because a header is not a
// role.
var tfSanctionedGrants = map[string]string{
	`resource "google_project_iam_custom_role" "signer"`:                "the signer role itself, and the whole of what signing takes",
	`resource "google_kms_key_ring_iam_member" "signer_signs"`:          "its one grant, to the signer's own account",
	`resource "google_storage_bucket_iam_member" "signer_reads"`:        "the service's read of the signed key set, which holds no key",
	`resource "google_cloud_run_v2_service_iam_member" "signer_public"`: "the signer's public invoker, which reaches no key at all",
	`resource "google_project_iam_custom_role" "pairedlist_sealer"`:     "K1's paired-list role: seal and open the symmetric key and nothing else, which is nothing on an asymmetric key and no reach of the signing ring",
	`resource "google_kms_key_ring_iam_member" "signer_seals"`:          "its one grant, to the signer's own account and on K1's own ring",
}

// tfSignerRefs is every block of this root that may name the signer's account,
// and how many times: the service's template makes the service that identity,
// and the three grants name it as their member. A block that names it and is not
// on this list is a block that runs as the signer, grants it or exports it.
var tfSignerRefs = map[string]int{
	`resource "google_cloud_run_v2_service" "signer"`:            1,
	`resource "google_kms_key_ring_iam_member" "signer_signs"`:   1,
	`resource "google_kms_key_ring_iam_member" "signer_seals"`:   1,
	`resource "google_storage_bucket_iam_member" "signer_reads"`: 1,
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

	lifecycle := tfNested(t, bucket, "lifecycle")
	tfAttrsAre(t, lifecycle, "the key-set bucket's lifecycle", []tfWant{
		{"prevent_destroy", "true"},
	}, "every install fetches the pinning keys out of this bucket, so losing it is losing the key set at the URL they hold: a destroy is a step a person takes on purpose — after the new set is published somewhere else — and never an apply's side effect (O7, A3)")

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

	// The program the signer runs is the image's own entrypoint and the five
	// values below, and the container is the whole of it. A second container, an
	// init container, a command or an argument beside the image, another working
	// directory, a mounted volume or a probe is each another program, or another
	// file, running as the signer's account — whose one permission is useToSign on
	// the assertion key, so what runs there signs with the project's identity
	// however little the root says about it (A1, A3, D2792).
	if got := len(tfNestedBlocks(template, "containers")); got != 1 {
		t.Errorf("the signer's template declares %d containers blocks, want exactly 1: a second container runs an image of its own beside the signer's, under the same account and with the signer role (A1, A3)", got)
	}
	for _, name := range tfNestedNames(container) {
		if name != "env" {
			t.Errorf("the signer's container writes a %s block: the signer's program is the image's entrypoint, and a %s block is a second one — a probe or a mount — running, or handed a file, as the account that holds useToSign on the assertion key (A1, A3)", name, name)
		}
	}
	for _, override := range []string{"command", "args", "working_dir", "volume_mounts"} {
		if got, ok := tfAttr(container, override); ok {
			t.Errorf("the signer's container writes %s = %s, want nothing: the image's entrypoint is the program this root means to run, and a command, an argument, a working directory or a mounted volume here is another program — or another file it reads — running as the account that holds useToSign on the assertion key (A1, A3, D2792)", override, got)
		}
	}
	if blocks := tfNestedBlocks(template, "init_containers"); len(blocks) > 0 {
		t.Errorf("the signer's template declares %d init_containers blocks, want none:\n%s\nan init container runs an image of its own, under the service's account, before the signer's container starts — so it mints with the signer role while no check here reads the program it runs (A1, A3)", len(blocks), strings.TrimSpace(blocks[0]))
	}

	want := map[string]string{
		"FUGARO_SIGNING_KEY":       `"${google_kms_crypto_key.signing.id}/cryptoKeyVersions/1"`,
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

	// The root's outputs are what a deploy reads back, and each is read by a
	// named task: the JWKS URL P8 pins and P1 fetches, and K1's two values —
	// the database the paired list lives in and the key it is sealed under —
	// which whoever mounts the paired-list route hands to the program
	// (pairedlist.tf). A further output is a value no task here reads.
	outputs := map[string]string{
		`output "jwks_url"`:                "the JWKS URL P8 pins and P1 fetches",
		`output "pairedlist_database_url"`: "the database K1's store reads and writes",
		`output "pairedlist_key"`:          "the symmetric key K1's list is sealed under",
	}
	seen := make(map[string]bool, len(outputs))
	for _, rel := range tfFiles(t) {
		for _, block := range tfBlocks(tfSource(t, rel), `output "`) {
			header := tfHeader(block)
			seen[header] = true
			if _, ok := outputs[header]; !ok {
				t.Errorf("%s declares the output %s: this root's outputs are the JWKS URL and K1's two paired-list values, and a further output is a value no task here reads", rel, header)
			}
		}
	}
	if len(seen) != len(outputs) {
		t.Errorf("the root declares %d outputs, want exactly %d:\n%s", len(seen), len(outputs), tfList(outputs))
	}
	url := tfBlock(t, `output "jwks_url"`)
	value, _ := tfAttr(url, "value")
	if !strings.Contains(tfCompact(value), "google_cloud_run_v2_service.signer.uri") || !strings.Contains(value, "var.jwks_path") {
		t.Errorf("jwks_url is %s, want the service's own URI with the path the service serves on: a URL written here by hand is one nobody applied (A2, P8)", value)
	}
}

// TestInfraTheReadersSeeEverySpellingOfAHeader: HCL does not need a space
// between a block's keyword and its name — `resource"google_x""y"{` is
// `resource "google_x" "y" {` written another way, and `terraform validate`
// accepts both — while every check above reads the root as text rather than
// through a parser. A block written the cramped way is therefore a block those
// checks say nothing about unless the finders here see it, and nothing in CI
// normalises the spacing: this root is outside the terraform job's roots and
// `terraform fmt` never runs on it.
//
// This test is the finders' own, and the failure it is here to show is a sweep
// that comes back empty-handed: the fixture holds a cramped grant of
// roles/cloudkms.admin, which TestInfraTheSignerRoleIsTheNarrowestThatSigns
// refuses once the finder answers the block, a cramped import and a cramped
// module, which TestInfraTheRootIsOneFormOfConfiguration refuses the same way,
// and no file in this root is written that way — so the seven tests above cannot
// show any of it by themselves.
func TestInfraTheReadersSeeEverySpellingOfAHeader(t *testing.T) {
	const src = `resource "google_kms_crypto_key" "signing" {
  purpose = "ASYMMETRIC_SIGN"
}

resource"google_project_iam_member""admin"{
  role   = "roles/cloudkms.admin"
  member = "user:someone@example.com"
}

import{ to = google_kms_crypto_key.signing, id = "projects/p/locations/l/keyRings/r/cryptoKeys/k" }

module"extra"{ source = "./x" }

moved{ from = google_kms_crypto_key.older, to = google_kms_crypto_key.signing }
`
	blocks := tfBlocks(src, `resource "`)
	var got []string
	for _, block := range blocks {
		got = append(got, tfHeader(block))
	}
	want := []string{
		`resource "google_kms_crypto_key" "signing"`,
		`resource "google_project_iam_member" "admin"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the reader answers the resources %q, want %q: the fixture's second resource is written with no space after its keyword, and a finder that did not see it would leave the grant it holds in the plan and in no check above", got, want)
	}

	grant := blocks[1]
	if !tfGrants(tfHeader(grant), tfCompact(grant)) {
		t.Errorf("the reader does not read the cramped block as a grant:\n%s\nthe discriminator is what a block writes rather than how it is spaced", strings.TrimSpace(grant))
	}
	if _, sanctioned := tfSanctionedGrants[tfHeader(grant)]; sanctioned {
		t.Errorf("the fixture's cramped grant (%s) is on the list of grants this root may hold, want a grant the sweep refuses: the fixture is what shows that the sweep reads a block written this way at all", tfHeader(grant))
	}

	for _, kind := range tfSecondForms {
		if found := tfBlocks(src, kind.header); len(found) != 1 {
			t.Errorf("%s: the reader finds %d blocks in a source that writes one, want exactly 1 — HCL does not need the space a search for %q used to look for, and a root-form check that missed the block would call a tree with a module, an import or a moved block one form of configuration (A3)", kind.what, len(found), kind.header)
		}
	}
}

// TestInfraTheReadersReadWhitespaceTheWayHCLDoes: HCL reads any whitespace
// between a block's keyword and what it writes as one separator, and one run of
// it is the same as any other — `resource "google_x" "y" {`,
// `resource  "google_x" "y" {`, one with a tab after the keyword and one whose
// brace is on the next line are one block written its other ways. Every check
// above
// decides what this root writes by cutting each file into blocks and reading
// their headers, so a reader that looked for the single-space spelling would
// answer nothing at all about a block written any other way, and five of those
// checks would each miss one: the sweep would leave a grant in the plan,
// TestInfraTheSigningKeyIsNonExportable would count one key where the root holds
// two, TestInfraTheRootIsOneFormOfConfiguration would not refuse a module, the
// key-set bucket's grant count would miss a second grant on the bucket and the
// output count in TestInfraTheServiceSignsWithThisKeyAndServesTheJWKS would miss
// a second output.
//
// Each block below is appended to a copy of main.tf and to nothing on disk, so
// that every one of those readers is exercised on a spelling no file here holds.
// The grant's own case checks the other half of a cut as well: the header has to
// end the block above it, or the block before the appended one reads as a grant
// that writes a role it never wrote.
func TestInfraTheReadersReadWhitespaceTheWayHCLDoes(t *testing.T) {
	clean := tfSource(t, "main.tf")
	for _, show := range []struct {
		what   string
		block  string
		kind   string
		header string
		grant  bool
	}{
		{
			what:   "a grant with two spaces after its keyword",
			block:  "\nresource  \"google_project_iam_member\" \"extra\" {\n  role   = \"roles/owner\"\n  member = \"user:extra@example.com\"\n}\n",
			kind:   `resource "`,
			header: `resource "google_project_iam_member" "extra"`,
			grant:  true,
		},
		{
			what:   "a second key with a tab after its keyword",
			block:  "\nresource\t\"google_kms_crypto_key\" \"second\" {\n  key_ring = google_kms_key_ring.signing.id\n  purpose  = \"ENCRYPT_DECRYPT\"\n}\n",
			kind:   `resource "`,
			header: `resource "google_kms_crypto_key" "second"`,
		},
		{
			what:   "a second grant on the key-set bucket with two spaces",
			block:  "\nresource  \"google_storage_bucket_iam_member\" \"extra\" {\n  bucket = google_storage_bucket.keyset.name\n  role   = \"roles/storage.objectAdmin\"\n  member = google_service_account.signer.member\n}\n",
			kind:   `resource "`,
			header: `resource "google_storage_bucket_iam_member" "extra"`,
		},
		{
			what:   "a module with a tab after its keyword",
			block:  "\nmodule\t\"extra\" {\n  source = \"./x\"\n}\n",
			kind:   `module "`,
			header: `module "extra"`,
		},
		{
			what:   "an import with two spaces before its brace",
			block:  "\nimport  {\n  to = google_kms_crypto_key.signing\n  id = \"projects/p/locations/l/keyRings/r/cryptoKeys/k\"\n}\n",
			kind:   "import {",
			header: "import",
		},
		{
			what:   "a moved block whose brace is on the next line",
			block:  "\nmoved\n{\n  from = google_kms_crypto_key.older\n  to   = google_kms_crypto_key.signing\n}\n",
			kind:   "moved {",
			header: "moved",
		},
		{
			what:   "a second output with two spaces after its keyword",
			block:  "\noutput  \"extra\" {\n  value = 1\n}\n",
			kind:   `output "`,
			header: `output "extra"`,
		},
	} {
		src := clean + show.block
		if got, want := len(tfBlocks(src, show.kind)), len(tfBlocks(clean, show.kind))+1; got != want {
			t.Errorf("the reader of %s answers %d blocks in a file with %s appended, want %d: the block is written the way HCL reads it, and a finder that answered nothing for it would leave whatever it holds out of every check that reads this kind (A3)", show.kind, got, show.what, want)
			continue
		}
		if headers := tfHeaders(src, show.kind); !slices.Contains(headers, show.header) {
			t.Errorf("the reader of %s answers the headers %q for a file with %s appended, want %s among them: the header is how a check looks a block up and how the count it feeds is taken", show.kind, headers, show.what, show.header)
		}
		if show.grant {
			var holders []string
			for _, block := range tfBlocks(src, `resource "`) {
				if strings.Contains(block, "roles/owner") {
					holders = append(holders, tfHeader(block))
				}
			}
			if !slices.Equal(holders, []string{show.header}) {
				t.Errorf("the appended grant is read as %q, want a block of its own at %s: the header begins the appended block and ends the one above it, and a cut that missed it would read the grant as part of the block before, where no check would refuse it (A3, D2792)", holders, show.header)
			}
			if refused := tfUnsanctionedGrants(src); len(refused) != 1 || !strings.Contains(refused[0], `"google_project_iam_member" "extra"`) {
				t.Errorf("the sweep refuses %d blocks of a file holding %s (%s), want that one block: what a block writes is what the sweep refuses it for, and the spacing of its header is not part of either (A3, D2792)", len(refused), show.what, strings.Join(refused, "\n"))
			}
		}
	}
}

// TestInfraTheSweepSeesAnExportedAccountKey: a `google_service_account_key` for
// the signer's account is a second way to sign — its private half can be
// downloaded, and it carries the useToSign the ring grant gives the account —
// and the block writes none of the attributes a grant is written with, so a
// sweep that looked only for an `_iam_` type or a `role`/`member` attribute
// would pass it while every other check here stayed green.
//
// The block is appended to a copy of main.tf and to nothing on disk, and both
// mechanisms of the refusal are exercised on that source: the search for the
// routes to the signer's account, which names the type, and the allowlist, whose
// sweep reads the type as a grant and refuses a block it did not sanction. The
// two spellings of the header are both tried, because the sweep reads a header
// and not a line (A3, D2792, O7).
func TestInfraTheSweepSeesAnExportedAccountKey(t *testing.T) {
	clean := tfSource(t, "main.tf")
	if routes := tfAccountRouteWrites(clean); len(routes) != 0 {
		t.Fatalf("the root's own main.tf writes %d of the routes to the signer's account, want none: the refusals below are shown on an appended block, and a route already in this file would make the two halves of the fixture one", len(routes))
	}
	if refused := tfUnsanctionedGrants(clean); len(refused) != 0 {
		t.Fatalf("the root's own main.tf holds %d blocks the sweep refuses, want none:\n%s\nthe appended block is the one the sweep has to refuse, and a second one would leave it unclear which of them fired", len(refused), strings.Join(refused, "\n"))
	}

	for _, spelling := range []struct{ what, gap string }{
		{"one space", " "},
		{"two spaces", "  "},
	} {
		src := clean + "\nresource" + spelling.gap + `"google_service_account_key" "signer_key" {` + "\n  service_account_id = google_service_account.signer.name\n}\n"

		var written []string
		for _, route := range tfAccountRouteWrites(src) {
			written = append(written, route.needle)
		}
		if !slices.Equal(written, []string{"google_service_account_key"}) {
			t.Errorf("the search for the routes to the signer's account answers %q for a file holding an exported account key (%s), want the one route `google_service_account_key`: an account key names no identity the ref count reads, so the type is what the search is for", written, spelling.what)
		}
		refused := tfUnsanctionedGrants(src)
		if len(refused) != 1 || !strings.Contains(refused[0], "google_service_account_key") {
			t.Errorf("the sweep refuses %d blocks of a file holding an exported account key (%s), want that one block:\n%s\nthe key is a private half of the signer's account that can be downloaded and holds the ring grant with it, so a sweep that read it as no grant would leave a second way to sign in the plan (A3, D2792, O7)", len(refused), spelling.what, strings.Join(refused, "\n"))
		}
	}
}
