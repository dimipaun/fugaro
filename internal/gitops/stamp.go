package gitops

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The trailers a run stamps on the commits it makes (D601). Fugaro-Model
// holds the configured model of the stage that made the commit — the pin, not
// the model that served it — and is left out where the stage pinned none
// (D602).
const (
	TrailerRun   = "Fugaro-Run"
	TrailerRigor = "Fugaro-Rigor"
	TrailerModel = "Fugaro-Model"
)

// stampVars is the variable each trailer's value travels in: the values reach
// the hook through the environment of the commit that is being made, and not
// through a file the run's agent could rewrite between the runner writing it
// and git reading it.
var stampVars = map[string]string{
	TrailerRun:   "FUGARO_STAMP_RUN",
	TrailerRigor: "FUGARO_STAMP_RIGOR",
	TrailerModel: "FUGARO_STAMP_MODEL",
}

// Person is a commit author: a claim the launcher made about who wrote a run's
// commits (D604), which nothing has verified and which no account owns.
type Person struct {
	Name  string
	Email string
}

// Trailer is one `Key: Value` line of a commit message, Key being one of
// TrailerRun, TrailerRigor and TrailerModel.
type Trailer struct {
	Key   string
	Value string
}

// Stamp is what the commits a run makes carry: the author, when the launcher
// named one (a commit git gives an author of its own — a cherry-pick's — keeps
// it, D646), and the trailers. StampEnv carries it into the environment a
// commit is made in and WriteStampHooks installs the hook that reads it, which
// is why a commit is born stamped instead of rewritten once it exists (D603).
type Stamp struct {
	Author   *Person
	Trailers []Trailer
}

// StampEnv returns the entries a commit made by a run is made under: the
// author the launcher named (GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL only — the
// committer stays the identity `main` resolves for the run, D645), the trailer
// values, and the environment configuration that points core.hooksPath at
// hookDir, where WriteStampHooks wrote the hook that reads them.
//
// Environment configuration outranks the repository's own .git/config
// (git-config(1)), which is why core.hooksPath travels here and not in a file:
// the repository's config is one the run's agent can write, and it would
// otherwise decide whether commits are stamped at all.
//
// The count is read from the process environment, which is where a caller's
// own GIT_CONFIG_* entries belong: this sets its entry at that index and
// returns the larger count, and the caller appends the result to its
// environment — the later count wins and the caller's indexes are left alone.
// A caller that keeps its entries anywhere else has them clobbered.
//
// A stamp that cannot be written safely — a value or a name carrying a control
// character, a key the hook does not know, no hook directory — yields nothing
// at all rather than part of one: the run's commits then carry the run's own
// identity and no trailers, which is the state finalize counts (D603).
func StampEnv(s Stamp, hookDir string) []string {
	var env []string
	if s.Author != nil {
		if !stampField(s.Author.Name) || !stampField(s.Author.Email) {
			return nil
		}
		env = append(env, "GIT_AUTHOR_NAME="+s.Author.Name, "GIT_AUTHOR_EMAIL="+s.Author.Email)
	}
	for _, t := range s.Trailers {
		name, known := stampVars[t.Key]
		if !known {
			return nil
		}
		if t.Value == "" {
			continue
		}
		if !stampValue(t.Value) {
			return nil
		}
		env = append(env, name+"="+t.Value)
	}
	if !stampValue(hookDir) {
		return nil
	}
	count := 0
	if n, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT")); err == nil && n > 0 {
		count = n
	}
	index := strconv.Itoa(count)
	return append(env,
		"GIT_CONFIG_COUNT="+strconv.Itoa(count+1),
		"GIT_CONFIG_KEY_"+index+"=core.hooksPath",
		"GIT_CONFIG_VALUE_"+index+"="+hookDir,
	)
}

// stampField reports whether v can be written as one of git's identity fields.
// git puts the name and the address into the commit's author line, so a name
// carrying "<", ">" or a control character — a newline ends the line — writes
// whatever the value says rather than the identity it claims.
func stampField(v string) bool {
	return v != "" && !strings.ContainsAny(v, "<>") && stampValue(v)
}

// stampValue reports whether v can be written as a trailer value, an
// environment entry or a hook path. interpret-trailers reads a --trailer
// argument as trailer *lines*, so a value carrying a newline adds trailers the
// run never chose — a Signed-off-by among them (R1, D644).
func stampValue(v string) bool {
	return v != "" && !strings.ContainsFunc(v, unicode.IsControl)
}

//go:embed stamp_hook.sh
var stampHookScript string

// stampChainPlaceholder is where a written hook takes the hook directory it
// chains to. It stays a quoted string, so the embedded file is a valid shell
// script on its own, and what replaces it is quoted the same way.
const stampChainPlaceholder = "'@FUGARO_CHAIN@'"

// stampHookName is the hook that stamps a commit: git runs it before the
// commit exists, and `git commit --no-verify` does not skip it (githooks(5),
// D603).
const stampHookName = "prepare-commit-msg"

// chainHookNames are the other client-side hooks of githooks(5), each written
// as a pass-through to the same name in the directory the repository
// configured: the environment configuration pointing core.hooksPath at the
// stamp hook directory outranks the repository's own, so without these a
// repository's husky, lint-staged or pre-push hook would silently stop running
// (constraint 2). The server's hooks (pre-receive, update, post-receive,
// post-update, proc-receive, push-to-checkout) are not written: nothing a run
// does runs them.
var chainHookNames = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit",
	"pre-merge-commit", "commit-msg", "post-commit", "pre-rebase",
	"post-checkout", "post-merge", "pre-push", "pre-auto-gc", "post-rewrite",
	"sendemail-validate", "fsmonitor-watchman", "p4-changelist",
	"p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit",
	"post-index-change", "reference-transaction",
}

// chainHook is the pass-through every hook but the stamp itself is. A hook git
// does not find in the configured directory never runs, so this is what makes
// the repository's own hook still run, with the arguments and the exit status
// git gave the pass-through.
const chainHook = `#!/bin/sh
# This is the chain, not a hook of Fugaro's: the environment configuration
# pointing core.hooksPath at the stamp hook directory would otherwise silence
# the repository's own %[1]s (constraint 2).
fugaro_chain='@FUGARO_CHAIN@'
if [ -x "$fugaro_chain/%[1]s" ]; then
	exec "$fugaro_chain/%[1]s" "$@"
fi
`

// WriteStampHooks writes the run's commit hooks into dir, each mode 0755, and
// creates dir if it is not there. prepare-commit-msg stamps the commit being
// made from the environment StampEnv carries; every other client-side hook
// name is a pass-through to chainTo, the hook directory the repository
// configured, which is empty for a repository that configured none.
//
// The mode is not a detail: git ignores a hook that is not executable, with a
// warning and no failure, and a stamp that silently does not run is worse than
// one that does not exist.
func WriteStampHooks(dir, chainTo string) error {
	if chainTo != "" && !stampValue(chainTo) {
		return fmt.Errorf("hook chain directory %s carries a control character", safetext.Field(chainTo))
	}
	chain := "''"
	if chainTo != "" {
		chain = shQuote(chainTo)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range append([]string{stampHookName}, chainHookNames...) {
		script := fmt.Sprintf(chainHook, name)
		if name == stampHookName {
			script = stampHookScript
		}
		script = strings.ReplaceAll(script, stampChainPlaceholder, chain)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// shQuote returns s as one POSIX shell word. A hook is a script and not a
// command line, so a directory that reaches one through its text — the chain
// directory comes from the repository's own configuration, which a run's agent
// can write — is quoted rather than trusted; nothing here is ever eval'd.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
