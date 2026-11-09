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
// The refusal never repeats what it found. A URL with userinfo holds the
// credential itself, and a refusal that echoed it would leak it into the log
// the rule keeps it out of; the ARG or ENV name is printed instead, through
// pluginwire.Printable like every other value this package prints.
func refuseCredentialIn(e config.Entry, data []byte) error {
	for _, line := range dockerfileLines(data) {
		inst, rest := splitInstruction(line)
		if inst != "ARG" && inst != "ENV" {
			continue
		}
		for _, name := range instructionNames(inst, rest) {
			if credentialShaped(name) {
				return fmt.Errorf("the catalog entry %s: its Dockerfile sets %s %s, a name a credential is carried in; a build argument or an environment variable with a secret in it reaches the image, so mount the value with RUN --mount=type=secret instead",
					nameOf(e), inst, pluginwire.Printable(name))
			}
		}
	}
	if urlUserinfoRE.Match(data) {
		return fmt.Errorf("the catalog entry %s: its Dockerfile carries a URL with a credential in its userinfo, the part between // and @; a clone URL with a token in it reaches the image, so mount the credential with RUN --mount=type=secret instead",
			nameOf(e))
	}
	return nil
}

// dockerfileLines returns a definition's logical lines: an instruction whose
// physical line ends in a backslash continues on the next one, and what a
// person reads as one instruction is what the guard below must read as one too
// (ARG \ followed by GIT_TOKEN is one ARG).
func dockerfileLines(data []byte) []string {
	var lines []string
	var cont strings.Builder
	for _, physical := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(physical, "\r"))
		if strings.HasSuffix(line, `\`) && !strings.HasSuffix(line, `\\`) {
			cont.WriteString(strings.TrimSuffix(line, `\`))
			cont.WriteByte(' ')
			continue
		}
		cont.WriteString(line)
		lines = append(lines, cont.String())
		cont.Reset()
	}
	if cont.Len() > 0 {
		lines = append(lines, cont.String())
	}
	return lines
}

// splitInstruction cuts one logical line into its instruction and the rest:
// the instruction upper-cased, because Dockerfile instructions are
// case-insensitive, and the rest as it was written. A blank line, a comment
// and the parser directive return no instruction.
func splitInstruction(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", ""
	}
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return strings.ToUpper(line), ""
	}
	return strings.ToUpper(line[:i]), strings.TrimLeft(line[i:], " \t")
}

// instructionNames returns the variable names one ARG or ENV line sets.
func instructionNames(inst, rest string) []string {
	if inst == "ARG" {
		// ARG name, or ARG name=default: the name ends at the first space or
		// the first '=', whichever comes first.
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return nil
		}
		if i := strings.IndexAny(rest, " \t="); i >= 0 {
			rest = rest[:i]
		}
		return []string{rest}
	}
	return envNames(rest)
}

// envNames returns the names an ENV line sets: the modern form is one or more
// key=value pairs on the one line, the legacy one a single name with the value
// after it. Both forms are still written, and a Dockerfile mixes them.
func envNames(rest string) []string {
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return nil
	}
	if eq, sp := strings.IndexByte(rest, '='), strings.IndexAny(rest, " \t"); eq < 0 || (sp >= 0 && sp < eq) {
		// Legacy: ENV name value..., where the name ends at the whitespace.
		if sp < 0 {
			return []string{rest}
		}
		return []string{rest[:sp]}
	}
	var names []string
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		names = append(names, rest[:eq])
		rest = strings.TrimLeft(envValue(rest[eq+1:]), " \t")
	}
	return names
}

// envValue cuts one ENV value off the front of s and returns the rest: a
// quoted value ends at its closing quote, any other at the next whitespace.
func envValue(s string) string {
	if s == "" {
		return s
	}
	if q := s[0]; q == '"' || q == '\'' {
		if i := strings.IndexByte(s[1:], q); i >= 0 {
			return s[i+2:]
		}
		return ""
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[i:]
	}
	return ""
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
// one.
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
// are not told apart.
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
