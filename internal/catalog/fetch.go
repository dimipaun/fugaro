// Package catalog resolves a project's image catalog (docs/project-image-
// catalog/spec.md) into what a build reads: the Dockerfile bytes of an
// entry's source, fetched from a repository through the project's existing
// git credentials or taken from the entry's own inline definition, and the
// selection among entries (PIC5).
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// MaxDockerfileBytes bounds a resolved Dockerfile, whatever its source:
// gitprov.MaxContentsBytes, the ceiling a contents read already has, applied
// to an inline definition too. What is fetched is copied into the project's
// bucket and handed to a build, so a definition larger than this is refused
// where it is resolved rather than carried.
const MaxDockerfileBytes = gitprov.MaxContentsBytes

// Fetch resolves e's source into the Dockerfile bytes a catalog build reads
// (D1140, PIC3): a repo reference is read out of the named repository, at
// the named branch, through the provider the opener returns — the project's
// existing credentials, and no other access (F9) — and an inline definition
// is taken from the entry itself, with no repository involved.
//
// open and kind are the project's git provider opener and kind
// (gitprov.KindGitHub or KindBitbucket, both of which read a file). A kind
// whose provider cannot read one (Readable) is refused: an inline entry needs
// neither.
//
// The credential the opener returns is dropped here: nothing in this
// function writes it, returns it or puts it in a failure — what a caller
// prints is the entry's repository and path, and the file's own bytes. Those
// two are printed through pluginwire.Printable, the rule every value that
// reaches a log follows: the catalog's own validation is not this package's
// to rely on, and a name or a repository that carried an escape sequence
// would carry it into whatever reads the message.
func Fetch(ctx context.Context, open gitprov.Opener, kind string, e config.Entry) ([]byte, error) {
	switch e.Source.Kind {
	case config.SourceInline:
		return definition(e, []byte(e.Source.Inline))
	case config.SourceDockerfile:
		return fetchDockerfile(ctx, open, kind, e)
	case config.SourceImage:
		return nil, fmt.Errorf("the catalog entry %s has no Dockerfile: its source is a prebuilt image, so there is nothing to build or copy", nameOf(e))
	default:
		return nil, fmt.Errorf("the catalog entry %s has no source kind this build knows (%s)", nameOf(e), strings.Join(config.SourceKinds, ", "))
	}
}

// fetchDockerfile reads a dockerfile source's file out of its repository.
//
// The repository, the path and the ref are checked before the provider is
// opened, not only where the catalog is validated: this is where all three
// reach an API path and a build, and a value that got past a validation is
// still not one to hand a provider (a path outside the repository, an
// owner/name that is not one, a ref the API would read as something else).
// The ref is held to config.ValidBranchName, the rule the catalog's own
// validation applies, so this guard tightens or loosens with that one
// instead of being a second, weaker rule that reads like a safety net.
func fetchDockerfile(ctx context.Context, open gitprov.Opener, kind string, e config.Entry) ([]byte, error) {
	s := e.Source
	if !validRepo(s.Repo) {
		return nil, fmt.Errorf("the catalog entry %s: the source's repository is not the owner/name of a repository of this project", nameOf(e))
	}
	if !gitprov.ValidRepoPath(s.Path) {
		return nil, fmt.Errorf("the catalog entry %s: the source's path must be a relative path inside %s, with no .. segment", nameOf(e), pluginwire.Printable(s.Repo))
	}
	if s.Branch != "" && !config.ValidBranchName(s.Branch) {
		return nil, fmt.Errorf("the catalog entry %s: the source's branch must be a git branch name", nameOf(e))
	}
	if open == nil {
		return nil, fmt.Errorf("the catalog entry %s: no git provider was given to read %s with", nameOf(e), pluginwire.Printable(s.Repo))
	}
	p, _, err := open(ctx, kind, s.Repo)
	if err != nil {
		return nil, fmt.Errorf("the catalog entry %s: opening the provider of %s: %w", nameOf(e), pluginwire.Printable(s.Repo), err)
	}
	r, ok := p.(gitprov.ContentsReader)
	if !ok {
		return nil, UnreadableRefusal(e, kind)
	}
	data, err := r.Contents(ctx, s.Path, s.Branch)
	if err != nil {
		// The path is printed through pluginwire.Printable like every other
		// value that reaches a message here: gitprov.ValidRepoPath lets a
		// formatting (bidi) character through, so the path a person reads
		// back is escaped rather than taken at its word.
		return nil, fmt.Errorf("the catalog entry %s: reading %s from %s: %w", nameOf(e), pluginwire.Printable(s.Path), pluginwire.Printable(s.Repo), err)
	}
	return definition(e, data)
}

// definition is what both of Fetch's Dockerfile sources hand back: bytes
// bounded to what a build may read, which carry no credential in their own
// text. It is one function for the two sources because the objection to a
// definition that names a credential is not to how the file was found — an
// inline entry and a fetched one reach the same bucket key and the same build.
func definition(e config.Entry, data []byte) ([]byte, error) {
	data, err := bounded(e, data)
	if err != nil {
		return nil, err
	}
	if err := refuseCredentialIn(e, data); err != nil {
		return nil, err
	}
	return data, nil
}

// refuseCredentialIn refuses a definition whose own text carries a credential:
// an ARG or an ENV whose name is credential-shaped, or a URL with userinfo in
// it (the https://user:token@host shape). Either one puts the value into the
// definition itself — which is copied into the project's bucket and read by a
// build — and a build argument or an environment variable reaches an image
// layer, which is what D1150 and the runtime contract keep a secret out of. The
// shape the project supports instead is the BuildKit secret mount
// (RUN --mount=type=secret,id=git-credentials ...), so both refusals name it.
//
// It reads the text and not the built image: a credential a repository's own
// build step passes in behind a secret mount is not this guard's to see, and a
// definition written by hand or generated by the images skill is what it is
// for. It is a heuristic on purpose, and an honest one: the two shapes it
// knows are the ones a definition gets wrong (ARG NPM_TOKEN=<token> and a
// clone URL with the token in its userinfo), its marker list is a denylist a
// name can be written around, and an ARG that says nothing (ARG NPM_TOKEN with
// the value from --build-arg) is refused on the name alone because the value
// reaches the layer either way.
//
// The heuristic's limits are named rather than left to be found. The guard
// reads instructions whose name is ARG, ENV or ONBUILD of either, and not a
// RUN that exports a variable itself: a credential written into an image by a
// build step is a scan's to catch (images/scan.sh is one), and a scan of the
// built image is the layer this one does not pretend to be. A heredoc body
// (RUN <<EOF ...) is read as instructions, because the guard does not track
// where a body ends: a body line that spells an ARG or an ENV whose name is
// credential-shaped is refused, which errs toward refusing and says so in the
// message. A URL with userinfo is refused wherever it is written, a comment
// included, because the whole definition is what the bucket holds: the guard
// cannot tell a real token from one written as an example, and the way out for
// prose that needs the shape is to write it without the userinfo. All of it
// holds for a definition whose author is not looking for the guard — an author
// who is can name the ARG anything, which no reading of the text can prevent.
//
// The refusal never repeats what it found. A URL with userinfo holds the
// credential itself, and a refusal that echoed it would leak it into the log
// the rule keeps it out of; the ARG or ENV name is printed instead, through
// pluginwire.Printable like every other value this package prints.
func refuseCredentialIn(e config.Entry, data []byte) error {
	lines, esc := dockerfileLines(data)
	for _, line := range lines {
		if err := refuseCredentialNames(e, line, esc); err != nil {
			return err
		}
	}
	// The URL is read twice, because each reading catches text the other does
	// not. The logical lines catch a URL a build reads but whose credential is
	// split across a continuation: joining the lines is what makes it one URL
	// again. The raw bytes catch a URL in a comment inside a continuation, which
	// dockerfileLines drops with the continuation it belongs to — the definition
	// is copied into the project's bucket whole, comments and all, so that token
	// is in the project whether or not a build reads the line it is written on.
	// (A comment on a line of its own is a logical line and is read by both.)
	if urlUserinfoRE.MatchString(strings.Join(lines, "\n")) || urlUserinfoRE.Match(data) {
		return fmt.Errorf("the catalog entry %s: its Dockerfile carries a URL with a credential in its userinfo, the part between // and @; a clone URL with a token in it reaches the image, so mount the credential with RUN --mount=type=secret instead",
			nameOf(e))
	}
	return nil
}

// refuseCredentialNames checks one logical line's ARG and ENV names, and the
// ARG and ENV an ONBUILD line defers to a child image: both are instructions
// whose variable reaches a layer, so both are read. esc is the definition's
// own escape character (dockerfileLines), which decides how the line is cut
// into words.
func refuseCredentialNames(e config.Entry, line string, esc byte) error {
	inst, rest := splitInstruction(line)
	if inst == "ONBUILD" {
		inst, rest = splitInstruction(rest)
	}
	if inst != "ARG" && inst != "ENV" {
		return nil
	}
	for _, name := range instructionNames(inst, rest, esc) {
		if credentialShaped(name) {
			return fmt.Errorf("the catalog entry %s: its Dockerfile sets %s %s, a name a credential is carried in; a build argument or an environment variable with a secret in it reaches the image, so mount the value with RUN --mount=type=secret instead",
				nameOf(e), inst, pluginwire.Printable(name))
		}
	}
	return nil
}

// dockerfileLines returns a definition's logical lines: an instruction whose
// physical line ends in a backslash continues on the next one, and what the
// parser makes one instruction of is what the guard above must read as one too.
//
// The reading follows the parser rather than a tidier rule of its own, because
// both ways of being wrong cost something: read the definition more loosely
// than a build does and a name a build sets goes unchecked, more strictly and a
// definition that carries no credential is refused. So everything that decides
// a joined name is the parser's own — the escape character, which the leading
// comments may set with `# escape=`, and which no directive after the parser's
// own first non-directive line changes; the leading whitespace of a fresh line,
// dropped; the leading whitespace of a continuation line, kept, which is what
// separates the words of a joined line (ARG NPM_\ over an indented TOKEN is the
// two build arguments NPM_ and TOKEN, and not the one harmless NPM_); a comment
// or a blank line inside a continuation, dropped with it (ARG NPM_\ over a
// comment over TOKEN is the one arg NPMTOKEN); and a comment's own trailing
// backslash, which continues nothing, because the parser skips a comment whole.
// The parser's own idea of whitespace is the one used here, unicode and not
// "space and tab": a name after a vertical tab is a name to a build. All of it
// was checked against BuildKit's own parser.
//
// It returns the lines and the escape character it read the definition with,
// which a caller needs as well: the same character decides how a line is cut
// into the words an instruction is parsed from (dockerfileWords).
func dockerfileLines(data []byte) ([]string, byte) {
	// A byte-order mark is stripped from the first line and nowhere else, as
	// the parser strips it: left in place it becomes part of the first
	// instruction's name, and an ARG under one is an ARG no longer.
	text := strings.TrimPrefix(string(data), "\ufeff")
	esc := dockerfileEscape(text)
	var lines []string
	var cont strings.Builder
	joining := false
	for _, physical := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(physical, "\r")
		comment := strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "#")
		if comment || joining {
			if joining && strings.TrimSpace(line) == "" {
				continue
			}
			if comment {
				// A comment is dropped whole and continues nothing, so a
				// backslash at the end of one is text and not a join.
				if !joining {
					lines = append(lines, strings.TrimLeftFunc(line, unicode.IsSpace))
				}
				continue
			}
		}
		if !joining {
			line = strings.TrimLeftFunc(line, unicode.IsSpace)
		}
		body, more := trimContinuation(line, esc)
		cont.WriteString(body)
		if more {
			joining = true
			continue
		}
		lines = append(lines, cont.String())
		cont.Reset()
		joining = false
	}
	if cont.Len() > 0 {
		lines = append(lines, cont.String())
	}
	return lines, esc
}

// trimContinuation removes a line's continuation escape character and the
// blanks after it, reporting whether the line carries one. Two rules are the
// parser's: an escape character at the end of a line continues it even when it
// is the line's only character, and one that another escape character precedes
// is an escaped character rather than a continuation.
func trimContinuation(line string, esc byte) (string, bool) {
	line = strings.TrimRight(line, " \t")
	if n := len(line); n > 0 && line[n-1] == esc && (n == 1 || line[n-2] != esc) {
		return line[:n-1], true
	}
	return line, false
}

// directiveRE is the parser's own idea of a parser directive
// (DirectiveParser.ParseLine): a comment whose whole body is key=value. A
// comment that is not one — a plain note, or anything else without the '='
// shape — is not a directive, and the parser stops looking for directives at
// the first line that is not one. The '#' is cut before the match
// (bytes.CutPrefix) and what follows is left-trimmed of unicode whitespace,
// which is what the caller does.
var directiveRE = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9]*)\s*=\s*(.+?)\s*$`)

// dockerfileEscape returns the character a definition continues a line with:
// the parser's own backslash, unless a parser directive before the first
// instruction sets `# escape=` to the backtick. Reading a definition with the
// wrong one joins lines a build keeps apart and splits lines it joins, and the
// name a build sets is the one this guard has to read.
//
// The scan stops where the parser's own directive parser stops
// (DirectiveParser.ParseLine): the first line that is not a key=value comment
// naming a directive the parser knows ends the block. A blank line, a plain
// comment and a comment naming anything else (# foo=bar) all end it, so an
// `# escape=` written after one of them is a comment to a build and is not
// followed here either. Only `syntax` and `check` carry the scan on. What may sit in
// front of the '#' is the parser's own reading too: the first physical line of
// a logical line is stripped of its leading whitespace before the directive is
// matched, and every line here is a first line (a comment never continues one).
func dockerfileEscape(data string) byte {
	for _, physical := range strings.Split(data, "\n") {
		line := strings.TrimLeftFunc(strings.TrimRight(physical, "\r\n"), unicode.IsSpace)
		if !strings.HasPrefix(line, "#") {
			break
		}
		m := directiveRE.FindStringSubmatch(strings.TrimLeftFunc(line[1:], unicode.IsSpace))
		if m == nil {
			break
		}
		switch strings.ToLower(m[1]) {
		case "escape":
			// The parser admits the backtick and the backslash and refuses a
			// definition that names anything else, so that one never builds
			// and this reading of it does not matter.
			if m[2] == "`" {
				return '`'
			}
			return '\\'
		case "syntax", "check":
			// Directives the parser knows and this guard has no use for: they
			// do not end the block, so the scan carries on. `check` is one of
			// them from the dockerfile:1.8 frontend on — the pinned
			// dockerfile:1.10 build has it, and internal/config's
			// knownDirectives carries the same three names — so a definition
			// may write `# check=skip=all` in front of its `# escape=` and a
			// build still honours the escape.
		default:
			// A key the parser does not know ends the block with the rest.
			return '\\'
		}
	}
	return '\\'
}

// dockerSpace is the whitespace the parser separates an instruction from its
// arguments with, which is not every byte a Go reader would call a space: a
// vertical tab or a form feed separates them to a build, and an instruction
// read with either in it is an instruction the guard does not see.
const dockerSpace = " \t\v\f\r"

// splitInstruction cuts one logical line into its instruction and the
// arguments it is parsed from: the instruction upper-cased, because Dockerfile
// instructions are case-insensitive, and the arguments with the builder flags
// the parser takes off the front of them removed, because a flag is not an
// argument of ARG or ENV. A blank line, a comment and the parser directive
// return no instruction.
func splitInstruction(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", ""
	}
	i := strings.IndexAny(line, dockerSpace)
	if i < 0 {
		return strings.ToUpper(line), ""
	}
	return strings.ToUpper(line[:i]), withoutBuilderFlags(strings.TrimLeft(line[i:], dockerSpace))
}

// withoutBuilderFlags removes the builder flags from the front of a line's
// arguments, the way the parser does before an instruction is parsed
// (splitCommand's extractBuilderFlags): a leading run of words that begin with
// `--` is flags, and a bare `--` ends the run and is a flag itself. Without
// this, a bare `--` reads here as the first name and the name a build sets —
// `ENV -- NPM_TOKEN=1` is the environment variable NPM_TOKEN — goes unchecked;
// the flags may be quoted, so a word is cut the same way the parser cuts it.
func withoutBuilderFlags(rest string) string {
	const (
		inSpaces = iota // looking for start of a word
		inWord
		inQuote
	)
	phase, wordStart := inSpaces, 0
	var quote byte
	for pos := 0; pos <= len(rest); pos++ {
		if phase == inSpaces {
			if pos == len(rest) {
				return ""
			}
			if unicode.IsSpace(rune(rest[pos])) {
				continue
			}
			// Only a word that starts with `--` is a flag; the run of them ends
			// at the first word that does not, which is where the arguments are.
			if rest[pos] != '-' || pos+1 == len(rest) || rest[pos+1] != '-' {
				return rest[pos:]
			}
			phase, wordStart = inWord, pos
			continue
		}
		if pos == len(rest) {
			// The last word ran to the end of the line: a flag, or nothing.
			return ""
		}
		switch phase {
		case inWord:
			if unicode.IsSpace(rune(rest[pos])) {
				if rest[wordStart:pos] == "--" {
					return rest[pos:]
				}
				phase = inSpaces
				continue
			}
			if rest[pos] == '\'' || rest[pos] == '"' {
				quote = rest[pos]
				phase = inQuote
				continue
			}
			if rest[pos] == '\\' && pos+1 < len(rest) {
				pos++ // the parser's own escape in a flag, always the backslash
			}
		case inQuote:
			if rest[pos] == quote {
				phase = inWord
				continue
			}
			if rest[pos] == '\\' && pos+1 < len(rest) {
				pos++
			}
		}
	}
	return ""
}

// instructionNames returns the variable names one ARG or ENV line sets, from
// the line's arguments (splitInstruction) and the definition's escape
// character.
func instructionNames(inst, rest string, esc byte) []string {
	if inst == "ARG" {
		// Every word of an ARG line is its own build argument (ARG FOO BAR
		// declares both, and each takes the value of a --build-arg of its own
		// name), so every word is a name this guard has to read and not only
		// the first: a credential-shaped name written second is declared by a
		// build all the same. A word is a name, or a name=default, so the name
		// ends at its '='. The words are the parser's own (parseWords) and not
		// a plain split on whitespace: a quoted default holds blanks, and
		// cutting one into words would read a value as a name and refuse a
		// definition that sets none.
		var names []string
		for _, word := range dockerfileWords(rest, esc) {
			name, _, _ := strings.Cut(word, "=")
			if name != "" {
				names = append(names, name)
			}
		}
		return names
	}
	return envNames(rest, esc)
}

// dockerfileWords cuts a line's arguments into the words the parser cuts them
// into (parseWords): whitespace separates words — the parser's own whitespace,
// unicode and not "space and tab" — while a quoted stretch is one word with its
// quotes taken off, and the definition's escape character keeps the character
// after it in the word rather than ending it. Nothing here reads a value: what
// the words are for is the name at the front of each.
func dockerfileWords(rest string, esc byte) []string {
	const (
		inSpaces = iota // looking for start of a word
		inWord
		inQuote
	)
	var words []string
	phase := inSpaces
	word := ""
	var quote byte
	blankOK := false
	var ch byte
	for pos := 0; pos <= len(rest); pos++ {
		if pos != len(rest) {
			ch = rest[pos]
		}
		if phase == inSpaces {
			if pos == len(rest) {
				break
			}
			if unicode.IsSpace(rune(ch)) {
				continue
			}
			phase = inWord
		}
		if (phase == inWord || phase == inQuote) && pos == len(rest) {
			if blankOK || len(word) > 0 {
				words = append(words, word)
			}
			break
		}
		if phase == inWord {
			if unicode.IsSpace(rune(ch)) {
				phase = inSpaces
				if blankOK || len(word) > 0 {
					words = append(words, word)
				}
				word, blankOK = "", false
				continue
			}
			if ch == '\'' || ch == '"' {
				quote = ch
				blankOK = true
				phase = inQuote
			}
			if ch == esc {
				if pos+1 == len(rest) {
					continue // an escape character at the end of the line
				}
				word += string(ch)
				pos++
				ch = rest[pos]
			}
			word += string(ch)
			continue
		}
		// inQuote: the same reading, with only the quote that opened it ending
		// the stretch — the escape character cannot escape a single quote, as
		// the parser has it.
		if ch == quote {
			phase = inWord
		}
		if ch == esc && quote != '\'' {
			if pos+1 == len(rest) {
				phase = inWord
				continue
			}
			pos++
			word += string(ch)
			ch = rest[pos]
		}
		word += string(ch)
	}
	return words
}

// envNames returns the names an ENV line sets: the modern form is one or more
// key=value pairs on the one line, the legacy one a single name with the value
// after it. Both forms are still written, and a Dockerfile mixes them.
//
// Which form the line is, and where one pair ends and the next begins, is the
// parser's own reading (parseNameVal): the words are the parser's words
// (dockerfileWords), the form is decided by whether the first of them holds an
// '=', and in the key=value form each word is one pair, its name ending at its
// first '='. Cutting the pairs by hand instead — reading a quoted value up to
// the next quote byte, and ignoring the escape character — reads the line a
// build does not: in ENV A="\" B=" NPM_TOKEN=1 the escaped quote is a character
// of the first value to a build, which then goes on to set NPM_TOKEN.
func envNames(rest string, esc byte) []string {
	words := dockerfileWords(rest, esc)
	if len(words) == 0 {
		return nil
	}
	if !strings.Contains(words[0], "=") {
		// Legacy: ENV name value..., where the name is what stands before the
		// first whitespace of the line as it was written (the parser splits the
		// raw text here, not the words).
		if sp := strings.IndexAny(rest, dockerSpace); sp >= 0 {
			return []string{rest[:sp]}
		}
		return []string{rest}
	}
	var names []string
	for _, word := range words {
		// A word with no '=' is a syntax error to a build, so nothing after it
		// is a pair either.
		name, _, ok := strings.Cut(word, "=")
		if !ok {
			break
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// credentialMarkers are the substrings that make a variable name
// credential-shaped. They are matched against the name with its punctuation
// and case folded out (credentialSeparators), so one marker covers API_KEY,
// api-key and APIKEY alike.
//
// The list is a denylist, and deliberately a short one: a word here refuses a
// definition, so a word that also names something harmless (auth, key, pass,
// user) would refuse definitions that carry no credential. The words that are
// here name a credential and nothing else, and the ones that are not are
// covered by the shape they are written in — AWS_SECRET_ACCESS_KEY and
// AWS_ACCESS_KEY_ID are here through secret and accesskey, GITHUB_TOKEN and
// NPM_TOKEN through token, and a GPG_KEY is a public key as often as a private
// one. Two entries cost something and are kept anyway: pwd is how a password is
// written short (DB_PWD), and it refuses the shell's own PWD and OLDPWD, which
// a definition has no reason to set and Docker maintains itself; token refuses
// TOKENIZERS_PARALLELISM, a tokenizer's parallelism setting that carries no
// credential. The refusal does not tell the two apart — it names the secret
// mount, which is not advice about a parallelism setting — so the way out for a
// definition that has to set one is to rename it, and an entry that cannot be
// renamed is an issue rather than something a guard can be taught.
var credentialMarkers = []string{
	"token", "secret", "passwd", "password", "passphrase", "pwd",
	"credential", "credentials", "creds",
	"apikey", "accesskey", "privatekey", "secretkey", "signingkey", "sshkey", "authkey",
}

// credentialSeparators is the punctuation a variable name is written with,
// removed so the markers above match a name however it is spelled.
var credentialSeparators = strings.NewReplacer("_", "", "-", "", ".", "", " ", "")

// credentialShaped reports whether name is a name a credential is carried in.
func credentialShaped(name string) bool {
	n := credentialSeparators.Replace(strings.ToLower(strings.Trim(name, `"'`)))
	for _, marker := range credentialMarkers {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

// urlUserinfoRE matches a URL that carries userinfo: a scheme, then something
// before the first '/' that ends in an '@'. The credential can be the password
// (https://user:token@host) or the user name itself, which is how a GitHub
// token is passed (https://TOKEN@host), so any userinfo is refused and the two
// are not told apart. The cost of not telling them apart is that a URL naming
// only the user is refused with them (ssh://git@host, postgres://user@db),
// which is deliberate: the userinfo is where a token is written when it is
// written as the user name, and the two shapes cannot be told apart by reading
// them — a refusal an operator meets once is cheaper than a token in a layer.
//
// The whole definition is read and not only its instructions: a comment is
// copied into the bucket with the rest of the text, so a URL written into one
// is refused like any other.
//
// The userinfo may carry no '/' and no whitespace, so the match cannot run out
// of the URL's own authority section, and an '@' after the first '/' (an email
// in a comment, a path) is not userinfo. The scp-like clone form
// (git@host:owner/repo) carries no scheme and is not a URL: it names the git
// user and can hold no password, so it is left alone.
var urlUserinfoRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.\-]*://[^/?#\s@]+@`)

// Readable reports whether the adapter for a provider kind reads a file out
// of its repository (gitprov.ContentsReader): the GitHub and the Bitbucket
// adapters do, and that is what this list is — a kind whose adapter does not
// is refused wherever a dockerfile source is resolved or accepted.
//
// It is the offline half of the read, for a caller that has no provider open
// and no credential to open one with, and must still refuse a dockerfile
// source before anything is built (fugaro image add, fugaro validate). What a
// read actually gets is the type assertion in fetchDockerfile, which stays
// where it is: an opener may return a provider of a kind this list admits and
// that still cannot answer, and then the read is what refuses it.
func Readable(kind string) bool {
	switch kind {
	case gitprov.KindGitHub, gitprov.KindBitbucket:
		return true
	}
	return false
}

// UnreadableRefusal is the refusal a dockerfile source on a provider that
// cannot read a file gets, in one place: fetchDockerfile's own refusal and
// the commands that refuse the source before a build ever starts (fugaro
// image add, fugaro validate) print the same sentence, so an operator who
// meets it early meets the one the build would have given.
func UnreadableRefusal(e config.Entry, kind string) error {
	return fmt.Errorf("the catalog entry %s: the %s provider cannot read a file out of %s, so its Dockerfile cannot be fetched; use an inline definition or a repository on a provider that can",
		nameOf(e), pluginwire.Printable(kind), pluginwire.Printable(e.Source.Repo))
}

// bounded refuses a definition larger than MaxDockerfileBytes. It is applied
// to a fetched file as well as to an inline one, because a provider's own
// ceiling is not this package's to rely on: what is bounded here is what
// this package hands on.
func bounded(e config.Entry, data []byte) ([]byte, error) {
	if len(data) > MaxDockerfileBytes {
		return nil, fmt.Errorf("the catalog entry %s: its Dockerfile is %d bytes, more than the %d a definition may be", nameOf(e), len(data), MaxDockerfileBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("the catalog entry %s: its Dockerfile is empty", nameOf(e))
	}
	return data, nil
}

// nameOf is e's name for a message this package writes, made safe to print
// (pluginwire.Printable): the name is validated where a catalog is
// published, and this package is not that place, so it prints the name the
// way every value that reaches a log is printed rather than trusting it.
func nameOf(e config.Entry) string { return pluginwire.Printable(e.Name) }

// validRepo reports whether repo is the owner/name of a repository: two
// non-empty segments that name a repository rather than a path above the
// host's root. gitprov.SplitRepo cuts the two apart and nothing more, and
// an owner of ".." or "." reaches an API path that means something else.
func validRepo(repo string) bool {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return false
	}
	for _, seg := range []string{owner, name} {
		if seg == "." || seg == ".." || strings.ContainsAny(seg, `\`) {
			return false
		}
	}
	return true
}
