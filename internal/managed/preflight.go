package managed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// preflight is the root's own files, read before terraform init: the part of a
// managed plan the plan JSON cannot show, and everything that RUNS before the
// gate has a plan to read. terraform init installs and starts providers, and
// terraform plan configures them with the deployer's credential, reads data
// sources and evaluates every function a configuration calls — all before the
// cover sees anything (PR #483's required residual (b), the design's §6 item
// 4, and the plan-time execution residual of Task 3.1's review).
//
// So the rule is an allowlist of the configuration's SHAPE, not a list of
// known-bad blocks, and every file is parsed in full (a file it cannot parse
// is refused, never skipped):
//
//   - the top-level blocks are terraform, provider, resource, module,
//     variable, output, locals, moved, import, removed and check — nothing
//     else. A data source (`data`, at the top or inside a check) is refused
//     whatever its type: data.external runs a program and data.http fetches a
//     URL at plan, and the managed cover's own list of admitted data sources
//     is empty (tf.managedModeReason). `ephemeral` and `action` are refused,
//     as are `backend`, `cloud` and `provider_meta` wherever terraform reads
//     them: the backend is this command's own (Workdir.WriteBackendBlock,
//     written after this check), so a root that carries one is refused
//     rather than let one file override the other.
//   - the providers are hashicorp/google and hashicorp/google-beta, by their
//     exact registry source: a required_providers entry is google or
//     google-beta with no source or exactly that source, a provider block
//     is one of the two and carries only the settings the managed cover
//     admits (tf.ManagedProviderSetting: no credentials, no endpoint, no
//     impersonation, no alias), every resource is a google_ type, and every
//     `provider`/`providers` reference names one of the two. Any other
//     provider is a binary the root's author chose, started at init.
//   - the functions are terraform's built-ins minus every one that reads the
//     filesystem (file and every file* hash, fileexists, fileset,
//     templatefile, templatestring, abspath, pathexpand); a provider-
//     namespaced call (`provider::…::f`, and `core::…`) is refused, and so is
//     every `path.*` reference. That is what keeps a configuration from
//     reading the deployer's credential file, the operator's ADC or anything
//     else on the applier's disk into a plan — and, for the same reason, the
//     one managed resource argument that names a local file
//     (google_storage_bucket_object's source, which the provider reads and
//     uploads) is refused too (localFileArgs).
//   - every module is a local directory inside the root, and is read by the
//     same rules; a registry or git source is refused outright, because its
//     files are not here to read before init fetches them.
//   - the root's own `.terraform.lock.hcl` is refused (this command writes the
//     lock itself, from the one embedded in fugaro: Workdir.WriteManagedLock),
//     and so is a state file, which init would offer to migrate.
//   - the override files (override.tf, override.tf.json, *_override.tf,
//     *_override.tf.json) are refused by name: terraform merges them over the
//     other files, so one can replace what the rest of the root says,
//     including the backend this command writes.
//
// The .tf files are read with a scanner of this file's own (this module has
// no HCL dependency), which follows hclsyntax's own token rules
// (scan_tokens.rl): quoted strings and heredocs are templates whose `${…}` and
// `%{…}` sequences are lexed as expressions, recursively, so a nested quote
// (`"${"{"}"`) cannot desynchronise it. Where hclsyntax's rules are subtle,
// the scanner refuses rather than guesses: a `$$`/`%%` escape, a newline
// inside a quoted string (even inside its interpolation), a non-ASCII byte
// outside a string, and every escape terraform does not define. The .tf.json
// files are read token by token, and an object that names a key twice is
// refused at any depth, because terraform's JSON reader keeps every copy
// where encoding/json keeps the last.
//
// What remains outside it, stated: this is a check of the files, not a
// sandbox. A provider of Google's own still runs with the deployer's
// credential at plan, and what it does with a resource's arguments is the
// cover's question once the plan exists. And none of it is a boundary against
// an agent that runs terraform itself (the package comment).
func (r Request) preflight() ([]string, error) {
	res, err := scanRoot(r.Workdir.Root)
	if err != nil {
		// The scan's own errors carry the root's file names and, under them, the
		// text of the files themselves (a lexer that quotes what it could not
		// read): both are the agent's, so the whole chain is bounded here, where
		// it becomes this refusal's sentence.
		return nil, fmt.Errorf("%w: %s: %w", ErrPreflight, r.what(), safetext.FieldErr(err))
	}
	if len(res.bad) > 0 {
		// Each line is bounded where it is written (scanResult.refuse), and how
		// many of them are printed is bounded here: a root of ten thousand
		// refused files is one agent's single directory, and a refusal nobody
		// can read to the end is a refusal that hides the line that matters.
		// What is left out is said rather than dropped.
		if n := len(res.bad) - preflightRefusalsMax; n > 0 {
			return nil, fmt.Errorf("%w: %s: %s; and %d more refusal(s) in the same root, not listed here",
				ErrPreflight, r.what(), strings.Join(res.bad[:preflightRefusalsMax], "; "), n)
		}
		return nil, fmt.Errorf("%w: %s: %s", ErrPreflight, r.what(), strings.Join(res.bad, "; "))
	}
	return res.providers(), nil
}

// preflightRefusalsMax is how many of the root's refusals one refusal message
// carries; the rest are counted and named as a count (preflight, above).
const preflightRefusalsMax = 20

// maxModuleDepth bounds the walk of local modules: a module tree deeper than
// this is refused rather than followed, so a symlink or a very long chain
// cannot make the scan unbounded.
const maxModuleDepth = 32

// The provider local names a managed root may use, and the sources each may
// name in required_providers (no source means hashicorp/<name>).
var managedProviderSources = map[string][]string{
	"google":      {"hashicorp/google", "registry.terraform.io/hashicorp/google"},
	"google-beta": {"hashicorp/google-beta", "registry.terraform.io/hashicorp/google-beta"},
}

// managedFunctions are the functions a managed root may call: terraform's
// built-ins (`terraform metadata functions -json`, 1.16) minus every one that
// reads the filesystem — file, filebase64, fileexists, fileset, filemd5,
// filesha1, filesha256, filesha512, filebase64sha256, filebase64sha512,
// templatefile, templatestring (a template that can call those), abspath and
// pathexpand. A function terraform adds later is refused until it is read and
// added here.
var managedFunctions = setOf(
	"abs", "alltrue", "anytrue", "base64decode", "base64encode", "base64gzip", "base64sha256", "base64sha512",
	"basename", "bcrypt", "can", "ceil", "chomp", "chunklist", "cidrhost", "cidrnetmask", "cidrsubnet", "cidrsubnets",
	"coalesce", "coalescelist", "compact", "concat", "contains", "convert", "csvdecode", "dirname", "distinct",
	"element", "endswith", "ephemeralasnull", "flatten", "floor", "format", "formatdate", "formatlist", "indent",
	"index", "issensitive", "join", "jsondecode", "jsonencode", "keys", "length", "log", "lookup", "lower",
	"matchkeys", "max", "md5", "merge", "min", "nonsensitive", "one", "parseint", "plantimestamp", "pow", "range",
	"regex", "regexall", "replace", "reverse", "rsadecrypt", "sensitive", "setintersection", "setproduct",
	"setsubtract", "setunion", "sha1", "sha256", "sha512", "signum", "slice", "sort", "split", "startswith",
	"strcontains", "strrev", "substr", "sum", "timeadd", "timecmp", "timestamp", "title", "tobool", "tolist",
	"tomap", "tonumber", "toset", "tostring", "transpose", "trim", "trimprefix", "trimspace", "trimsuffix", "try",
	"upper", "urlencode", "uuid", "uuidv5", "values", "yamldecode", "yamlencode", "zipmap",
)

// readsFiles is the denylist half of the same rule, for the one place the
// allowlist cannot be applied (a .tf.json string read as an expression rather
// than as a template, where prose is common): the functions that read the
// applier's disk.
func readsFiles(name string) bool {
	return strings.HasPrefix(name, "file") || name == "templatefile" || name == "templatestring" ||
		name == "abspath" || name == "pathexpand"
}

// exprKeywords are the identifiers an expression uses that may be followed by
// a parenthesis without being a function call: the for and if keywords, and
// the type constructors of a variable's type constraint.
var exprKeywords = setOf("for", "in", "if", "else", "endif", "endfor",
	"object", "list", "map", "set", "tuple", "optional")

// managedTopBlocks are the top-level blocks a managed root may declare.
var managedTopBlocks = setOf("terraform", "provider", "resource", "module", "variable", "output", "locals", "moved", "import", "removed", "check")

// refusedTopBlocks are the top-level blocks refused by name, with why.
var refusedTopBlocks = map[string]string{
	"data":          "a data source, which terraform reads at plan with the deployer's credential (data.external runs a program, data.http fetches a URL): a managed root declares none",
	"ephemeral":     "an ephemeral resource, opened with the deployer's credential and left out of the plan the gate reads",
	"action":        "an action block, which runs at apply outside the resource changes the gate reads",
	"backend":       "a backend block: the backend is this command's own",
	"cloud":         "a cloud block: the backend is this command's own",
	"provider_meta": "a provider_meta block, which hands a provider settings outside its own block",
}

// refusedTerraformBlocks are the blocks of a terraform block refused by name.
var refusedTerraformBlocks = map[string]string{
	"backend":       "a backend block: the backend of a managed apply is the one this command writes (D77's bucket), and a root that carries its own is refused rather than let one file override the other",
	"cloud":         "a cloud block: the backend of a managed apply is the one this command writes (D77's bucket)",
	"provider_meta": "a provider_meta block, which hands a provider settings outside its own block",
}

// localFileArgs are the arguments of a managed resource type that name a file
// on the applier's disk, which the provider reads (at plan, for its hash) and
// uploads (at apply) with the deployer's credential: a path such as the
// operator's own ADC file would be copied into a bucket the agent reads.
var localFileArgs = map[string]map[string]bool{
	"google_storage_bucket_object": {"source": true},
}

func localFileReason(typ, arg string) string {
	return fmt.Sprintf("the %s argument of a %s, which names a file on the applier's disk that the provider reads and uploads: a managed root writes the object's content instead", arg, typ)
}

// providerRef is the form of a `provider` meta-argument: a local name and an
// optional alias.
var providerRef = regexp.MustCompile(`^(google|google-beta)(\.[A-Za-z_][A-Za-z0-9_-]*)?$`)

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// scanResult is what scanRoot found: the refusals, one line each, and the
// providers the root uses (the local names), which decide the lock file.
type scanResult struct {
	bad  []string
	used map[string]bool
}

// providers are the registry addresses of the providers the root uses, sorted.
func (s *scanResult) providers() []string {
	var out []string
	for name := range s.used {
		out = append(out, "registry.terraform.io/hashicorp/"+name)
	}
	slices.Sort(out)
	return out
}

// refuse records one refusal of the root. The line is a field (safetext.Field)
// before it is kept, and this is the one funnel every refusal of this file goes
// through: where is a path inside the agent's own root and the format's
// arguments are the file's own words (a block's labels, a function's name, a
// quoted string), and neither may put a line, a control character, a bidi
// override or ten thousand characters into the sentence a person reads.
func (s *scanResult) refuse(where string, format string, args ...any) {
	s.bad = append(s.bad, safetext.Field(where+": "+fmt.Sprintf(format, args...)))
}

// overrideFile reports whether name is one terraform merges over the root's
// other files.
func overrideFile(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range []string{".tf.json", ".tf"} {
		if base, ok := strings.CutSuffix(n, ext); ok {
			return base == "override" || strings.HasSuffix(base, "_override")
		}
	}
	return false
}

// rootOnlyRefused are the files of the root directory itself a managed apply
// refuses: the lock file (this command writes its own) and local state, which
// init would offer to migrate into the managed bucket.
var rootOnlyRefused = map[string]string{
	infra.LockFile:             "the root's own dependency lock file: a managed apply writes its own from the lock embedded in fugaro, so the root's could choose the provider binary that runs",
	"terraform.tfstate":        "a local state file, which init would migrate into the managed state bucket",
	"terraform.tfstate.backup": "a local state file, which init would migrate into the managed state bucket",
}

// scanRoot reads every .tf and .tf.json file of root and of every local module
// they call, and returns a one-line reason per refusal, or an error when a
// file cannot be read at all.
func scanRoot(root string) (*scanResult, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("the root %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("the root %s is not a directory", root)
	}
	res := &scanResult{used: map[string]bool{}}
	for name, why := range rootOnlyRefused {
		if _, err := os.Lstat(filepath.Join(abs, name)); err == nil {
			res.refuse(filepath.Join(abs, name), "%s", why)
		}
	}
	seen := map[string]bool{}
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if depth > maxModuleDepth {
			return fmt.Errorf("the modules under %s nest deeper than %d, which is a cycle or a tree no one reads", root, maxModuleDepth)
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("the module %s: %w", dir, err)
		}
		if seen[real] {
			return nil
		}
		seen[real] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("the module %s: %w", dir, err)
		}
		var modules []string
		for _, e := range entries {
			name := e.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".tf") && !strings.HasSuffix(lower, ".tf.json") {
				continue
			}
			file := filepath.Join(dir, name)
			if e.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%s is a symlink, and a managed apply reads the root's own files alone", file)
			}
			if e.IsDir() {
				continue
			}
			if overrideFile(name) {
				res.refuse(file, "an override file, which terraform merges over the root's other files (and over the backend this command writes): a managed root carries none")
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			if !utf8.Valid(src) {
				return fmt.Errorf("refusing, because %s is not UTF-8", file)
			}
			var mods []string
			if strings.HasSuffix(lower, ".tf.json") {
				doc, err := parseJSON(src)
				if err != nil {
					return fmt.Errorf("refusing, because %s cannot be read as Terraform JSON: %w", file, err)
				}
				mods = res.checkJSON(file, doc)
			} else {
				body, err := parseHCL(src)
				if err != nil {
					return fmt.Errorf("refusing, because %s cannot be read as HCL: %w", file, err)
				}
				mods = res.checkHCL(file, body)
			}
			for _, m := range mods {
				mod, err := localModule(abs, dir, m)
				if err != nil {
					res.refuse(file, "%v", err)
					continue
				}
				modules = append(modules, mod)
			}
		}
		for _, m := range modules {
			if err := walk(m, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(abs, 0); err != nil {
		return nil, err
	}
	return res, nil
}

// localModule resolves a module block's source against the directory the block
// is in: the module's directory, or an error when the source is not a local
// path, is a symlink, or leaves the root.
func localModule(root, dir, source string) (string, error) {
	src := source
	// path.Clean would drop the "./" a local source is written with, so the
	// prefix is read from the source as the file writes it.
	if !strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../") && src != "." && src != ".." {
		return "", fmt.Errorf("the module source %q is not a local path: a managed root's modules are directories inside it, read before init, and a registry or git module is refused because its files are not here to read", source)
	}
	if strings.Contains(src, "//") || strings.ContainsAny(src, "?#\\") {
		return "", fmt.Errorf("the module source %q is not a plain local path", source)
	}
	mod := filepath.Join(dir, filepath.FromSlash(src))
	rel, err := filepath.Rel(root, mod)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the module source %q is outside the root %s, and a managed apply reads the root's own tree alone", source, root)
	}
	st, err := os.Lstat(mod)
	switch {
	case err != nil:
		return "", fmt.Errorf("the module source %q: %v", source, err)
	case st.Mode()&os.ModeSymlink != 0:
		return "", fmt.Errorf("the module source %q is a symlink, and a managed apply reads the root's own tree alone", source)
	case !st.IsDir():
		return "", fmt.Errorf("the module source %q is not a directory", source)
	}
	return mod, nil
}

// ---------------------------------------------------------------- HCL lexer

// tkind is what one token of a .tf file is.
type tkind int

const (
	tEOF tkind = iota
	tNewline
	tIdent
	tNumber
	// tString is a quoted string with no interpolation; text is its value.
	tString
	// tTmplOpen and tTmplClose bracket a quoted string or heredoc that is a
	// template; its interpolations are inside, as tInterpOpen … tInterpClose
	// with the expression's own tokens between.
	tTmplOpen
	tTmplClose
	tInterpOpen
	tInterpClose
	tPunct
)

// token is one lexical token: its kind, its text (an identifier's name, a
// literal string's value or a punctuation's characters) and its line.
type token struct {
	kind tkind
	text string
	line int
}

// lexer is a .tf file being tokenised.
type lexer struct {
	src  []byte
	i    int
	line int
	toks []token
}

func (l *lexer) errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", l.line, fmt.Sprintf(format, args...))
}

func (l *lexer) peek(n int) byte {
	if l.i+n < len(l.src) {
		return l.src[l.i+n]
	}
	return 0
}

func (l *lexer) emit(k tkind, text string) {
	l.toks = append(l.toks, token{kind: k, text: text, line: l.line})
}

// lexHCL tokenises a whole .tf file.
func lexHCL(src []byte) ([]token, error) {
	l := &lexer{src: src, line: 1}
	if err := l.expr(false, false); err != nil {
		return nil, err
	}
	l.emit(tEOF, "")
	return l.toks, nil
}

// multiPunct are the punctuation tokens of more than one character, longest
// first (hclsyntax's own list).
var multiPunct = []string{"...", "==", "!=", "<=", ">=", "&&", "||", "::", "=>"}

// singlePunct are the one-character punctuation tokens an expression may use.
const singlePunct = "[](),.*/%+-=<>!?:~"

// expr lexes expression tokens: to the end of the input, or — when interp —
// to the } that closes the interpolation it is in. inQuote is an
// interpolation inside a quoted string, where a newline is refused (hclsyntax
// allows some; refusing them leaves nothing to agree on).
func (l *lexer) expr(interp, inQuote bool) error {
	depth := 0
	for {
		if l.i >= len(l.src) {
			if interp {
				return l.errf("an interpolation is never closed")
			}
			return nil
		}
		c := l.src[l.i]
		switch {
		case c == '\n':
			if inQuote {
				return l.errf("a quoted string runs past the end of its line")
			}
			l.emit(tNewline, "")
			l.i++
			l.line++
		case c == ' ' || c == '\t':
			l.i++
		case c == '\r' && l.peek(1) == '\n':
			l.i++
		case c == '#' || (c == '/' && l.peek(1) == '/'):
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.i++
			}
		case c == '/' && l.peek(1) == '*':
			end := bytes.Index(l.src[l.i+2:], []byte("*/"))
			if end < 0 {
				return l.errf("a /* comment is never closed")
			}
			seg := l.src[l.i : l.i+2+end+2]
			n := bytes.Count(seg, []byte("\n"))
			if n > 0 && inQuote {
				return l.errf("a quoted string runs past the end of its line")
			}
			l.line += n
			l.i += len(seg)
		case c == '"':
			if err := l.quoted(); err != nil {
				return err
			}
		case c == '<' && l.peek(1) == '<':
			if inQuote {
				return l.errf("a heredoc inside a quoted string")
			}
			if err := l.heredoc(); err != nil {
				return err
			}
		case isIdentStart(c):
			start := l.i
			for l.i < len(l.src) && isIdentByte(l.src[l.i]) {
				l.i++
			}
			l.emit(tIdent, string(l.src[start:l.i]))
		case c >= '0' && c <= '9':
			start := l.i
			for l.i < len(l.src) {
				d := l.src[l.i]
				switch {
				case d >= '0' && d <= '9':
					l.i++
					continue
				case d == '.' && isDigit(l.peek(1)):
					l.i += 2
					continue
				case (d == 'e' || d == 'E') && isDigit(l.peek(1)):
					l.i += 2
					continue
				case (d == 'e' || d == 'E') && (l.peek(1) == '+' || l.peek(1) == '-') && isDigit(l.peek(2)):
					l.i += 3
					continue
				}
				break
			}
			l.emit(tNumber, string(l.src[start:l.i]))
		case c == '{':
			depth++
			l.emit(tPunct, "{")
			l.i++
		case c == '}':
			if interp && depth == 0 {
				l.i++
				l.emit(tInterpClose, "")
				return nil
			}
			depth--
			l.emit(tPunct, "}")
			l.i++
		default:
			if p := l.punct(); p != "" {
				l.emit(tPunct, p)
				l.i += len(p)
				continue
			}
			if c >= 0x80 {
				return l.errf("a non-ASCII character outside a string, which this reader refuses rather than guesses at")
			}
			return l.errf("the character %q, which is not HCL", c)
		}
	}
}

// punct is the punctuation token at the lexer's position, "" for none.
func (l *lexer) punct() string {
	rest := l.src[l.i:]
	for _, p := range multiPunct {
		if bytes.HasPrefix(rest, []byte(p)) {
			return p
		}
	}
	if strings.IndexByte(singlePunct, rest[0]) >= 0 {
		return string(rest[0])
	}
	return ""
}

// quoted lexes a quoted string: a tString when it holds no interpolation, the
// template's tokens otherwise.
func (l *lexer) quoted() error {
	start := l.line
	l.i++
	open := len(l.toks)
	l.emit(tTmplOpen, "")
	var lit strings.Builder
	templ := false
	for {
		if l.i >= len(l.src) {
			return fmt.Errorf("line %d: a string is never closed", start)
		}
		c := l.src[l.i]
		switch {
		case c == '\n' || c == '\r':
			return fmt.Errorf("line %d: a string runs past the end of its line", start)
		case c == '"':
			l.i++
			if !templ {
				l.toks[open] = token{kind: tString, text: lit.String(), line: start}
				return nil
			}
			l.emit(tTmplClose, "")
			return nil
		case c == '\\':
			n, err := l.escape(&lit)
			if err != nil {
				return err
			}
			l.i += n
		case (c == '$' || c == '%') && l.peek(1) == '{':
			templ = true
			l.emit(tInterpOpen, string(c)+"{")
			l.i += 2
			if err := l.expr(true, true); err != nil {
				return err
			}
		case (c == '$' || c == '%') && l.peek(1) == c:
			return l.errf("a %c%c sequence in a string: the template escape is refused rather than read", c, c)
		default:
			lit.WriteByte(c)
			l.i++
		}
	}
}

// escape decodes the backslash escape at the lexer's position into lit and
// returns its length. The escapes are hclsyntax's own; any other is refused.
func (l *lexer) escape(lit *strings.Builder) (int, error) {
	switch l.peek(1) {
	case 'n':
		lit.WriteByte('\n')
		return 2, nil
	case 'r':
		lit.WriteByte('\r')
		return 2, nil
	case 't':
		lit.WriteByte('\t')
		return 2, nil
	case '"':
		lit.WriteByte('"')
		return 2, nil
	case '\\':
		lit.WriteByte('\\')
		return 2, nil
	case 'u', 'U':
		n := 4
		if l.peek(1) == 'U' {
			n = 8
		}
		var r rune
		for k := 0; k < n; k++ {
			d := l.peek(2 + k)
			v, ok := hexVal(d)
			if !ok {
				return 0, l.errf("a \\%c escape without %d hex digits", l.peek(1), n)
			}
			r = r<<4 | rune(v)
		}
		lit.WriteRune(r)
		return 2 + n, nil
	}
	return 0, l.errf("the escape \\%c, which HCL does not define", l.peek(1))
}

// heredoc lexes a heredoc (<<TAG or <<-TAG): its body is a template, and it
// ends at the first line that, trimmed, is the tag and ends with a newline —
// hclsyntax's own rule, and the only line at which it can end.
func (l *lexer) heredoc() error {
	start := l.line
	l.i += 2
	if l.peek(0) == '-' {
		l.i++
	}
	ts := l.i
	if !isIdentStart(l.peek(0)) {
		return l.errf("a heredoc with no tag")
	}
	for l.i < len(l.src) && isIdentByte(l.src[l.i]) {
		l.i++
	}
	tag := string(l.src[ts:l.i])
	if l.peek(0) == '\r' {
		l.i++
	}
	if l.peek(0) != '\n' {
		return l.errf("the heredoc tag %s is not followed by the end of its line", tag)
	}
	l.i++
	l.line++
	l.emit(tTmplOpen, "")
	atStart := true
	for {
		if l.i >= len(l.src) {
			return fmt.Errorf("line %d: the heredoc <<%s is never closed", start, tag)
		}
		if atStart {
			atStart = false
			rest := l.src[l.i:]
			if nl := bytes.IndexByte(rest, '\n'); nl >= 0 && string(bytes.TrimSpace(rest[:nl])) == tag {
				// The newline itself is the token that ends the attribute.
				l.i += nl
				l.emit(tTmplClose, "")
				return nil
			}
		}
		c := l.src[l.i]
		switch {
		case c == '\n':
			l.i++
			l.line++
			atStart = true
		case (c == '$' || c == '%') && l.peek(1) == '{':
			l.emit(tInterpOpen, string(c)+"{")
			l.i += 2
			if err := l.expr(true, false); err != nil {
				return err
			}
		case (c == '$' || c == '%') && l.peek(1) == c:
			return l.errf("a %c%c sequence in a heredoc: the template escape is refused rather than read", c, c)
		default:
			l.i++
		}
	}
}

// lexTemplate tokenises s as a bare template (a .tf.json string): literal
// text, with its interpolations lexed as expressions.
func lexTemplate(s string) ([]token, error) {
	l := &lexer{src: []byte(s), line: 1}
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case (c == '$' || c == '%') && l.peek(1) == '{':
			l.emit(tInterpOpen, string(c)+"{")
			l.i += 2
			if err := l.expr(true, false); err != nil {
				return nil, err
			}
		case (c == '$' || c == '%') && l.peek(1) == c:
			return nil, l.errf("a %c%c sequence: the template escape is refused rather than read", c, c)
		default:
			l.i++
		}
	}
	l.emit(tEOF, "")
	return l.toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentByte(c byte) bool {
	return isIdentStart(c) || c == '-' || isDigit(c)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// --------------------------------------------------------------- HCL parser

// hclBody is a body: its attributes and its blocks, in order.
type hclBody struct {
	attrs  []hclAttr
	blocks []hclBlock
}

// hclAttr is one attribute: its name, line and the tokens of its expression.
type hclAttr struct {
	name string
	line int
	expr []token
}

// hclBlock is one block: its type, labels, line and body.
type hclBlock struct {
	typ    string
	labels []string
	line   int
	body   hclBody
}

// parser reads a token stream as HCL's structure: a body is attributes
// (name = expression, to the end of the line outside brackets) and blocks
// (type, labels, { body }).
type parser struct {
	toks []token
	i    int
}

// parseHCL lexes and parses a whole .tf file.
func parseHCL(src []byte) (hclBody, error) {
	toks, err := lexHCL(src)
	if err != nil {
		return hclBody{}, err
	}
	p := &parser{toks: toks}
	return p.body(false)
}

func perr(t token, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", t.line, fmt.Sprintf(format, args...))
}

func isPunct(t token, s string) bool { return t.kind == tPunct && t.text == s }

func (p *parser) body(nested bool) (hclBody, error) {
	var b hclBody
	for {
		t := p.toks[p.i]
		switch {
		case t.kind == tNewline:
			p.i++
			continue
		case t.kind == tEOF:
			if nested {
				return b, perr(t, "a block is never closed")
			}
			return b, nil
		case isPunct(t, "}"):
			if !nested {
				return b, perr(t, "a } closes no block")
			}
			return b, nil
		case t.kind != tIdent:
			return b, perr(t, "expected an attribute or a block")
		}
		name, line := t.text, t.line
		p.i++
		if isPunct(p.toks[p.i], "=") {
			p.i++
			expr, err := p.expression(nested)
			if err != nil {
				return b, err
			}
			b.attrs = append(b.attrs, hclAttr{name: name, line: line, expr: expr})
			continue
		}
		var labels []string
		for p.toks[p.i].kind == tString || p.toks[p.i].kind == tIdent {
			labels = append(labels, p.toks[p.i].text)
			p.i++
		}
		if !isPunct(p.toks[p.i], "{") {
			return b, perr(p.toks[p.i], "%s is neither an attribute (=) nor a block ({ after literal labels)", name)
		}
		p.i++
		inner, err := p.body(true)
		if err != nil {
			return b, err
		}
		p.i++ // the }
		if after := p.toks[p.i]; !(after.kind == tNewline || after.kind == tEOF || (nested && isPunct(after, "}"))) {
			return b, perr(after, "a block's } is not at the end of its line")
		}
		b.blocks = append(b.blocks, hclBlock{typ: name, labels: labels, line: line, body: inner})
	}
}

// expression is the tokens of one attribute's value: to the end of its line,
// with every bracket, template and interpolation balanced — or, in a
// one-line block, to the } that closes it.
func (p *parser) expression(nested bool) ([]token, error) {
	start := p.i
	depth := 0
	for {
		t := p.toks[p.i]
		switch {
		case t.kind == tEOF:
			if depth != 0 {
				return nil, perr(t, "an expression is never closed")
			}
			return p.span(start)
		case t.kind == tNewline && depth == 0:
			return p.span(start)
		case t.kind == tTmplOpen || t.kind == tInterpOpen || isPunct(t, "(") || isPunct(t, "[") || isPunct(t, "{"):
			depth++
		case t.kind == tTmplClose || t.kind == tInterpClose || isPunct(t, ")") || isPunct(t, "]") || isPunct(t, "}"):
			if depth == 0 {
				if isPunct(t, "}") && nested {
					return p.span(start)
				}
				return nil, perr(t, "an unbalanced %s", t.text)
			}
			depth--
		}
		p.i++
	}
}

func (p *parser) span(start int) ([]token, error) {
	if p.i == start {
		return nil, perr(p.toks[p.i], "an attribute with no value")
	}
	return p.toks[start:p.i], nil
}

// --------------------------------------------------------- the rules (HCL)

// checkHCL applies the managed rules to one .tf file and returns the local
// module sources it names.
func (s *scanResult) checkHCL(file string, body hclBody) []string {
	at := func(line int) string { return fmt.Sprintf("%s:%d", file, line) }
	for _, a := range body.attrs {
		s.refuse(at(a.line), "a top-level attribute %s, which no Terraform file carries", a.name)
	}
	var modules []string
	for _, b := range body.blocks {
		where := at(b.line)
		if why, ok := refusedTopBlocks[b.typ]; ok {
			s.refuse(where, "%s", why)
			continue
		}
		if !managedTopBlocks[b.typ] {
			s.refuse(where, "a %s block, which a managed root does not declare", b.typ)
			continue
		}
		s.exprs(file, b.body)
		switch b.typ {
		case "terraform":
			s.terraformHCL(file, b.body)
		case "provider":
			s.providerHCL(where, b)
		case "resource":
			if len(b.labels) != 2 || !strings.HasPrefix(b.labels[0], "google_") {
				s.refuse(where, "a resource of type %q: a managed root declares Google's resources alone (google_*)", strings.Join(b.labels, " "))
			}
			s.metaProvider(file, b.body)
			s.noProvisioners(file, b.body)
			if len(b.labels) == 2 {
				for _, a := range b.body.attrs {
					if localFileArgs[b.labels[0]][a.name] {
						s.refuse(at(a.line), "%s", localFileReason(b.labels[0], a.name))
					}
				}
			}
		case "import", "removed":
			s.metaProvider(file, b.body)
			s.noProvisioners(file, b.body)
		case "check":
			for _, inner := range b.body.blocks {
				if inner.typ == "data" {
					s.refuse(at(inner.line), "%s", refusedTopBlocks["data"])
				}
			}
		case "module":
			src, ok := moduleSourceHCL(b.body)
			if !ok {
				s.refuse(where, "a module whose source is not one literal string: a managed root's modules are local directories, named literally")
			} else {
				modules = append(modules, src)
			}
			for _, a := range b.body.attrs {
				if a.name == "providers" {
					s.providersMap(at(a.line), a.expr)
				}
			}
		}
	}
	return modules
}

// exprs checks every expression of body, at any depth.
func (s *scanResult) exprs(file string, body hclBody) {
	for _, a := range body.attrs {
		s.exprCheck(fmt.Sprintf("%s:%d", file, a.line), a.expr, true)
	}
	for _, b := range body.blocks {
		s.exprs(file, b.body)
	}
}

// exprCheck applies the function and path rules to an expression's tokens.
// strict applies the function allowlist; otherwise only the functions that
// read files are refused (readsFiles).
func (s *scanResult) exprCheck(where string, toks []token, strict bool) {
	next := func(k int) token {
		for k++; k < len(toks); k++ {
			if toks[k].kind != tNewline {
				return toks[k]
			}
		}
		return token{kind: tEOF}
	}
	prev := func(k int) token {
		for k--; k >= 0; k-- {
			if toks[k].kind != tNewline {
				return toks[k]
			}
		}
		return token{kind: tEOF}
	}
	for k, t := range toks {
		if isPunct(t, "::") {
			s.refuse(where, "a namespaced function call (::), which runs a provider's own code or names a built-in by another name: a managed root calls terraform's built-ins by their plain names")
			continue
		}
		if t.kind != tIdent {
			continue
		}
		afterDot := isPunct(prev(k), ".")
		switch n := next(k); {
		case isPunct(n, "(") && !exprKeywords[t.text] && !afterDot:
			if (strict && !managedFunctions[t.text]) || readsFiles(t.text) {
				s.refuse(where, "a call of %s(), which a managed root may not make: the functions that read the applier's disk (file, file*, templatefile, templatestring, abspath, pathexpand) are refused, and so is any function terraform's built-in list did not carry when this check was written", t.text)
			}
		case t.text == "path" && !afterDot && isPunct(n, "."):
			s.refuse(where, "a path.* reference, which names a directory on the applier's disk: a managed root reads no path")
		}
	}
}

// terraformHCL applies the rules of a terraform block.
func (s *scanResult) terraformHCL(file string, body hclBody) {
	for _, a := range body.attrs {
		if a.name != "required_version" {
			s.refuse(fmt.Sprintf("%s:%d", file, a.line), "the terraform setting %s, which a managed root does not carry (required_version and required_providers alone)", a.name)
		}
	}
	for _, b := range body.blocks {
		where := fmt.Sprintf("%s:%d", file, b.line)
		if why, ok := refusedTerraformBlocks[b.typ]; ok {
			s.refuse(where, "%s", why)
			continue
		}
		if b.typ != "required_providers" || len(b.labels) != 0 {
			s.refuse(where, "a %s block in terraform, which a managed root does not carry", b.typ)
			continue
		}
		for _, inner := range b.body.blocks {
			s.refuse(fmt.Sprintf("%s:%d", file, inner.line), "a %s block in required_providers", inner.typ)
		}
		for _, a := range b.body.attrs {
			s.requiredProviderHCL(fmt.Sprintf("%s:%d", file, a.line), a)
		}
	}
}

// requiredProviderHCL is one required_providers entry: google or
// google-beta, as a version string or an object whose source is exactly the
// provider's own.
func (s *scanResult) requiredProviderHCL(where string, a hclAttr) {
	sources, ok := managedProviderSources[a.name]
	if !ok {
		s.refuse(where, "the provider %s: a managed root requires hashicorp/google and hashicorp/google-beta alone", a.name)
		return
	}
	s.used[a.name] = true
	if len(a.expr) == 1 && a.expr[0].kind == tString {
		return // the legacy version-string form: source hashicorp/<name>
	}
	entries, ok := objectEntries(a.expr)
	if !ok {
		s.refuse(where, "the provider %s's requirement is not an object of literal keys", a.name)
		return
	}
	for _, e := range entries {
		switch e.key {
		case "version", "configuration_aliases":
		case "source":
			if len(e.value) != 1 || e.value[0].kind != tString || !slices.Contains(sources, e.value[0].text) {
				s.refuse(where, "the provider %s's source is not %s: a managed root runs Google's own providers, by their exact registry address", a.name, sources[0])
			}
		default:
			s.refuse(where, "the provider %s's requirement carries %q", a.name, e.key)
		}
	}
}

// objEntry is one entry of an object constructor: a literal key and the
// tokens of its value.
type objEntry struct {
	key   string
	value []token
}

// objectEntries reads an object constructor whose keys are all literal (an
// identifier or a plain string) and returns its entries; false when the
// expression is not one, which is a refusal (a computed key could be anything).
func objectEntries(toks []token) ([]objEntry, bool) {
	if len(toks) < 2 || !isPunct(toks[0], "{") || !isPunct(toks[len(toks)-1], "}") {
		return nil, false
	}
	inner := toks[1 : len(toks)-1]
	var out []objEntry
	seen := map[string]bool{}
	for k := 0; k < len(inner); {
		t := inner[k]
		if t.kind == tNewline || isPunct(t, ",") {
			k++
			continue
		}
		if (t.kind != tIdent && t.kind != tString) || k+1 >= len(inner) || !(isPunct(inner[k+1], "=") || isPunct(inner[k+1], ":")) {
			return nil, false
		}
		if seen[t.text] {
			return nil, false
		}
		seen[t.text] = true
		k += 2
		start, depth := k, 0
		for ; k < len(inner); k++ {
			x := inner[k]
			if depth == 0 && (x.kind == tNewline || isPunct(x, ",")) {
				break
			}
			switch {
			case x.kind == tTmplOpen || x.kind == tInterpOpen || isPunct(x, "(") || isPunct(x, "[") || isPunct(x, "{"):
				depth++
			case x.kind == tTmplClose || x.kind == tInterpClose || isPunct(x, ")") || isPunct(x, "]") || isPunct(x, "}"):
				depth--
			}
		}
		if k == start {
			return nil, false
		}
		out = append(out, objEntry{key: t.text, value: inner[start:k]})
	}
	return out, true
}

// providerHCL applies the rules of a provider block.
func (s *scanResult) providerHCL(where string, b hclBlock) {
	if len(b.labels) != 1 || managedProviderSources[b.labels[0]] == nil {
		s.refuse(where, "a provider block for %q: a managed root configures google and google-beta alone", strings.Join(b.labels, " "))
		return
	}
	s.used[b.labels[0]] = true
	for _, a := range b.body.attrs {
		if !tf.ManagedProviderSetting(a.name) {
			s.refuse(where, "the provider setting %s, which the deployer's credential would be configured with at plan: a managed root's provider block sets where resources land and their labels alone (no credential, endpoint, impersonation or alias)", a.name)
		}
	}
	for _, inner := range b.body.blocks {
		s.refuse(where, "a %s block in the provider block, which a managed root's provider does not carry", inner.typ)
	}
}

// metaProvider reads a block's provider meta-argument: one of the two Google
// providers, or none (the default, google).
func (s *scanResult) metaProvider(file string, body hclBody) {
	for _, a := range body.attrs {
		if a.name != "provider" {
			continue
		}
		var ref strings.Builder
		for _, t := range a.expr {
			ref.WriteString(t.text)
		}
		m := providerRef.FindStringSubmatch(ref.String())
		if m == nil || !onlyIdentsAndDots(a.expr) {
			s.refuse(fmt.Sprintf("%s:%d", file, a.line), "the provider %q, which is not google or google-beta", ref.String())
			continue
		}
		s.used[m[1]] = true
		return
	}
	s.used["google"] = true
}

func onlyIdentsAndDots(toks []token) bool {
	for _, t := range toks {
		if t.kind != tIdent && !isPunct(t, ".") {
			return false
		}
	}
	return len(toks) > 0
}

// providersMap reads a module's providers map: every name in it is one of
// the two Google providers (an alias after a dot is the configuration's own).
func (s *scanResult) providersMap(where string, toks []token) {
	for k, t := range toks {
		if t.kind == tString {
			s.refuse(where, "a module's providers map with a quoted name")
			continue
		}
		if t.kind != tIdent || (k > 0 && isPunct(toks[k-1], ".")) {
			continue
		}
		if managedProviderSources[t.text] == nil {
			s.refuse(where, "the provider %s in a module's providers map, which is not google or google-beta", t.text)
			continue
		}
		s.used[t.text] = true
	}
}

// noProvisioners refuses a provisioner or a connection block anywhere in a
// body, written out or generated by a dynamic block: either runs a command or
// a copy on the machine that applies.
func (s *scanResult) noProvisioners(file string, body hclBody) {
	for _, b := range body.blocks {
		name := b.typ
		if b.typ == "dynamic" && len(b.labels) > 0 {
			name = b.labels[0]
		}
		if name == "provisioner" || name == "connection" {
			s.refuse(fmt.Sprintf("%s:%d", file, b.line), "a %s block, which runs on the machine that applies: a managed root declares none", name)
		}
		s.noProvisioners(file, b.body)
	}
}

// moduleSourceHCL is a module block's source when it is one literal string.
func moduleSourceHCL(body hclBody) (string, bool) {
	var src string
	n := 0
	for _, a := range body.attrs {
		if a.name != "source" {
			continue
		}
		n++
		if len(a.expr) != 1 || a.expr[0].kind != tString {
			return "", false
		}
		src = a.expr[0].text
	}
	return src, n == 1
}

// ----------------------------------------------------------- Terraform JSON

// jnode is one JSON value, with an object's keys in their order.
type jnode struct {
	kind byte // 'o' object, 'a' array, 's' string, 'v' any other scalar
	keys []string
	vals []*jnode
	arr  []*jnode
	str  string
}

// maxJSONDepth bounds the nesting a .tf.json may have.
const maxJSONDepth = 256

// parseJSON reads a .tf.json token by token, refusing a key an object names
// twice at any depth: terraform's JSON reader keeps every copy of a key (a
// second "terraform" key is a second terraform block), where encoding/json
// into a map keeps only the last — the difference a scan must not have.
func parseJSON(src []byte) (*jnode, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	n, err := jvalue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("it holds more than one JSON value")
	}
	return n, nil
}

func jvalue(dec *json.Decoder, depth int) (*jnode, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("it nests deeper than %d", maxJSONDepth)
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			n := &jnode{kind: 'o'}
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("an object key that is not a string")
				}
				if seen[k] {
					return nil, fmt.Errorf("the key %q appears twice in one object, and terraform reads every copy of it", k)
				}
				seen[k] = true
				val, err := jvalue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, k)
				n.vals = append(n.vals, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return n, nil
		case '[':
			n := &jnode{kind: 'a'}
			for dec.More() {
				val, err := jvalue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				n.arr = append(n.arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return n, nil
		}
		return nil, fmt.Errorf("an unexpected %v", v)
	case string:
		return &jnode{kind: 's', str: v}, nil
	default:
		return &jnode{kind: 'v'}, nil
	}
}

// get is an object's value for key, nil when it has none.
func (n *jnode) get(key string) *jnode {
	if n == nil || n.kind != 'o' {
		return nil
	}
	for k, name := range n.keys {
		if name == key {
			return n.vals[k]
		}
	}
	return nil
}

// jblock is one block of the JSON syntax: its labels and its body.
type jblock struct {
	labels []string
	body   *jnode
}

// jblocks reads a block type's value with nlabels labels: at each label level
// an object (its keys are the labels) or an array of such objects, and at the
// body level an object or an array of objects. Anything else is a refusal.
func jblocks(n *jnode, nlabels int) ([]jblock, bool) {
	if n == nil {
		return nil, false
	}
	if n.kind == 'a' {
		var out []jblock
		for _, e := range n.arr {
			bs, ok := jblocks(e, nlabels)
			if !ok {
				return nil, false
			}
			out = append(out, bs...)
		}
		return out, true
	}
	if n.kind != 'o' {
		return nil, false
	}
	if nlabels == 0 {
		return []jblock{{body: n}}, true
	}
	var out []jblock
	for k, label := range n.keys {
		bs, ok := jblocks(n.vals[k], nlabels-1)
		if !ok {
			return nil, false
		}
		for _, b := range bs {
			out = append(out, jblock{labels: append([]string{label}, b.labels...), body: b.body})
		}
	}
	return out, true
}

// jsonLabels is how many labels each top-level block type has in the JSON
// syntax.
var jsonLabels = map[string]int{
	"terraform": 0, "provider": 1, "resource": 2, "module": 1, "variable": 1, "output": 1,
	"locals": 0, "moved": 0, "import": 0, "removed": 0, "check": 1,
}

// checkJSON applies the managed rules to one .tf.json file and returns the
// local module sources it names.
func (s *scanResult) checkJSON(file string, doc *jnode) []string {
	if doc.kind != 'o' {
		s.refuse(file, "the file is not a JSON object")
		return nil
	}
	s.jsonStrings(file, doc)
	var modules []string
	for k, key := range doc.keys {
		val := doc.vals[k]
		if key == "//" {
			continue
		}
		if why, ok := refusedTopBlocks[key]; ok {
			s.refuse(file, "%s", why)
			continue
		}
		nlabels, ok := jsonLabels[key]
		if !ok {
			s.refuse(file, "a %s block, which a managed root does not declare", key)
			continue
		}
		blocks, ok := jblocks(val, nlabels)
		if !ok {
			s.refuse(file, "a %s block that is not an object (or a list of objects) with %d label(s)", key, nlabels)
			continue
		}
		for _, b := range blocks {
			switch key {
			case "terraform":
				s.terraformJSON(file, b.body)
			case "provider":
				s.providerJSON(file, b)
			case "resource":
				if !strings.HasPrefix(b.labels[0], "google_") {
					s.refuse(file, "a resource of type %q: a managed root declares Google's resources alone (google_*)", b.labels[0])
				}
				s.metaProviderJSON(file, b.body)
				s.noProvisionersJSON(file, b.body)
				for name := range localFileArgs[b.labels[0]] {
					if b.body.get(name) != nil {
						s.refuse(file, "%s", localFileReason(b.labels[0], name))
					}
				}
			case "import", "removed":
				s.metaProviderJSON(file, b.body)
				s.noProvisionersJSON(file, b.body)
			case "check":
				if b.body.get("data") != nil {
					s.refuse(file, "%s", refusedTopBlocks["data"])
				}
			case "module":
				src := b.body.get("source")
				if src == nil || src.kind != 's' {
					s.refuse(file, "the module %s has no literal source: a managed root's modules are local directories, named literally", b.labels[0])
				} else {
					modules = append(modules, src.str)
				}
				if p := b.body.get("providers"); p != nil {
					s.providersJSON(file, p)
				}
			}
		}
	}
	return modules
}

// jsonStrings checks every string of a .tf.json — every key and every value,
// at any depth — as terraform may read it: as a template (where its ${…} are
// expressions, against the function allowlist) and as a bare expression (a
// few arguments are read that way; against the file-reading functions, so
// prose is not refused for its parentheses).
func (s *scanResult) jsonStrings(file string, n *jnode) {
	check := func(str string) {
		toks, err := lexTemplate(str)
		if err != nil {
			s.refuse(file, "the string %q cannot be read as a template: %v", str, err)
			return
		}
		s.exprCheck(file, toks, true)
		if toks, err := lexHCL([]byte(str)); err == nil {
			s.exprCheck(file, toks, false)
		}
	}
	switch n.kind {
	case 's':
		check(n.str)
	case 'o':
		for k, key := range n.keys {
			check(key)
			s.jsonStrings(file, n.vals[k])
		}
	case 'a':
		for _, e := range n.arr {
			s.jsonStrings(file, e)
		}
	}
}

// terraformJSON applies the rules of a terraform block in the JSON syntax.
func (s *scanResult) terraformJSON(file string, body *jnode) {
	for k, key := range body.keys {
		switch key {
		case "//", "required_version":
		case "required_providers":
			bs, ok := jblocks(body.vals[k], 0)
			if !ok {
				s.refuse(file, "a required_providers that is not an object")
				continue
			}
			for _, b := range bs {
				for j, name := range b.body.keys {
					if name == "//" {
						continue
					}
					s.requiredProviderJSON(file, name, b.body.vals[j])
				}
			}
		default:
			if why, ok := refusedTerraformBlocks[key]; ok {
				s.refuse(file, "%s", why)
				continue
			}
			s.refuse(file, "the terraform setting %s, which a managed root does not carry (required_version and required_providers alone)", key)
		}
	}
}

func (s *scanResult) requiredProviderJSON(file, name string, v *jnode) {
	sources, ok := managedProviderSources[name]
	if !ok {
		s.refuse(file, "the provider %s: a managed root requires hashicorp/google and hashicorp/google-beta alone", name)
		return
	}
	s.used[name] = true
	switch v.kind {
	case 's':
		return
	case 'o':
		for k, key := range v.keys {
			switch key {
			case "//", "version", "configuration_aliases":
			case "source":
				if src := v.vals[k]; src.kind != 's' || !slices.Contains(sources, src.str) {
					s.refuse(file, "the provider %s's source is not %s: a managed root runs Google's own providers, by their exact registry address", name, sources[0])
				}
			default:
				s.refuse(file, "the provider %s's requirement carries %q", name, key)
			}
		}
	default:
		s.refuse(file, "the provider %s's requirement is neither a version nor an object", name)
	}
}

func (s *scanResult) providerJSON(file string, b jblock) {
	name := b.labels[0]
	if managedProviderSources[name] == nil {
		s.refuse(file, "a provider block for %q: a managed root configures google and google-beta alone", name)
		return
	}
	s.used[name] = true
	for _, key := range b.body.keys {
		if key != "//" && !tf.ManagedProviderSetting(key) {
			s.refuse(file, "the provider setting %s, which the deployer's credential would be configured with at plan: a managed root's provider block sets where resources land and their labels alone (no credential, endpoint, impersonation or alias)", key)
		}
	}
}

func (s *scanResult) metaProviderJSON(file string, body *jnode) {
	p := body.get("provider")
	if p == nil {
		s.used["google"] = true
		return
	}
	m := providerRef.FindStringSubmatch(p.str)
	if p.kind != 's' || m == nil {
		s.refuse(file, "the provider %q, which is not google or google-beta", p.str)
		return
	}
	s.used[m[1]] = true
}

func (s *scanResult) providersJSON(file string, p *jnode) {
	if p.kind != 'o' {
		s.refuse(file, "a module's providers that is not an object")
		return
	}
	for k, key := range p.keys {
		v := p.vals[k]
		mk, mv := providerRef.FindStringSubmatch(key), providerRef.FindStringSubmatch(v.str)
		if mk == nil || v.kind != 's' || mv == nil {
			s.refuse(file, "a module's providers entry %q = %q, which is not google or google-beta", key, v.str)
			continue
		}
		s.used[mk[1]], s.used[mv[1]] = true, true
	}
}

// noProvisionersJSON refuses a provisioner or a connection anywhere in a
// body, written out or generated by a dynamic block.
func (s *scanResult) noProvisionersJSON(file string, n *jnode) {
	switch n.kind {
	case 'o':
		for k, key := range n.keys {
			if key == "provisioner" || key == "connection" {
				s.refuse(file, "a %s block, which runs on the machine that applies: a managed root declares none", key)
			}
			s.noProvisionersJSON(file, n.vals[k])
		}
	case 'a':
		for _, e := range n.arr {
			s.noProvisionersJSON(file, e)
		}
	}
}
