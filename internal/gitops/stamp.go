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
	"github.com/dimipaun/fugaro/internal/shellword"
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

// StampEnv returns env with the entries added that a commit made by a run is
// made under: the author the launcher named (GIT_AUTHOR_NAME and
// GIT_AUTHOR_EMAIL only — the committer stays the identity `main` resolves for
// the run, D645), the trailer values, and the environment configuration that
// points core.hooksPath at hookDir, where WriteStampHooks wrote the hook that
// reads them.
//
// Environment configuration outranks the repository's own .git/config
// (git-config(1)), which is why core.hooksPath travels here and not in a file:
// the repository's config is one the run's agent can write, and it would
// otherwise decide whether commits are stamped at all.
//
// The count is the caller's, read from the env passed in: the entry here is
// numbered after the caller's own GIT_CONFIG_* entries and the count returned
// is the larger one, so a caller that carries entries of its own keeps every
// one of them. CredentialVars' credential helper is exactly that — two entries
// in the environment of the git calls a run makes — and a count read from this
// process's environment instead would number the stamp's entry over the
// helper's first and leave git reading the caller's count: a credential helper
// silently gone, and a push that asks for a password.
//
// A stamp that cannot be written safely — a value or a name carrying a control
// character, a key the hook does not know, no hook directory, a count that
// cannot be read — leaves env as it was rather than adding part of one: the
// run's commits then carry the run's own identity and no trailers, which is
// the state finalize counts (D603).
func StampEnv(s Stamp, hookDir string, env []string) []string {
	vars := map[string]string{}
	if s.Author != nil {
		if !stampField(s.Author.Name) || !stampField(s.Author.Email) {
			return env
		}
		vars["GIT_AUTHOR_NAME"] = s.Author.Name
		vars["GIT_AUTHOR_EMAIL"] = s.Author.Email
	}
	for _, t := range s.Trailers {
		name, known := stampVars[t.Key]
		if !known {
			return env
		}
		if t.Value == "" {
			continue
		}
		if !stampValue(t.Value) {
			return env
		}
		vars[name] = t.Value
	}
	if !stampValue(hookDir) {
		return env
	}
	// The first GIT_CONFIG_COUNT entry is the one git reads, and one this
	// cannot read is a count whose entries would be numbered over.
	count := 0
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if key != "GIT_CONFIG_COUNT" {
			continue
		}
		if value == "" {
			break
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return env
		}
		count = n
		break
	}
	index := strconv.Itoa(count)
	vars["GIT_CONFIG_COUNT"] = strconv.Itoa(count + 1)
	vars["GIT_CONFIG_KEY_"+index] = "core.hooksPath"
	vars["GIT_CONFIG_VALUE_"+index] = hookDir
	return WithVars(env, vars)
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
// chains to, and stampChainLaterPlaceholder whether it looks one up at the
// commit when it was given none. Both stay quoted strings, so the embedded
// files are valid shell scripts on their own, and what replaces each is
// quoted the same way.
const (
	stampChainPlaceholder      = "'@FUGARO_CHAIN@'"
	stampChainLaterPlaceholder = "'@FUGARO_CHAIN_LATER@'"
)

// chainLater and chainNow are the two values stampChainLaterPlaceholder takes:
// 1 is a hook directory looked up at the commit (WriteStampHooks with no
// chainTo), 0 is one named now and never looked up — either by the caller
// (chainTo) or because the hooks chain nowhere at all
// (WriteStampHooksNoChain).
const (
	chainLater = "1"
	chainNow   = "0"
)

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
fugaro_chain_later='@FUGARO_CHAIN_LATER@'
# The machine's and the caller's configuration are in place for this one, unlike
# the trailer step of the stamp itself: a global core.hooksPath is a legitimate
# way to configure a repository's hooks, and dropping it here would stop a
# repository's own hooks running (constraint 2). What it costs is that a global
# or environment core.hooksPath an agent wrote decides the directory this execs
# — its own code inside its own commit.
if [ "$fugaro_chain_later" = 1 ]; then
	fugaro_chain=$(unset GIT_CONFIG_COUNT; git rev-parse --git-path hooks) || fugaro_chain=''
fi
fugaro_next="$fugaro_chain/%[1]s"
# A chain that is this script — a repository whose own configuration points
# core.hooksPath here — would exec itself, for ever. The two are compared by
# what they hold and not by how they are spelled, which is the one thing a
# shell without -ef can say about it.
if [ -n "$fugaro_chain" ] && [ -x "$fugaro_next" ] && ! cmp -s "$fugaro_next" "$0"; then
	exec "$fugaro_next" "$@"
fi
`

// WriteStampHooks writes the run's commit hooks into dir, each mode 0755, and
// creates dir if it is not there. prepare-commit-msg stamps the commit being
// made from the environment StampEnv carries; every other client-side hook
// name is a pass-through to chainTo, the hook directory the repository
// configured.
//
// chainTo is the empty string for a repository that configured none, and the
// hooks then look the repository's own directory up at the commit themselves:
// it is the one core.hooksPath resolves to with the stamp's own entry out of
// the way, which is git's default of $GIT_DIR/hooks when the repository
// configured nothing. That default is the point — the environment
// configuration pointing core.hooksPath at dir outranks it, so a hook of the
// repository's in $GIT_DIR/hooks stops running without this, silently
// (constraint 2).
//
// A caller whose hooks must run none of the repository's — the runner's own
// commits — writes them with WriteStampHooksNoChain instead: an empty chainTo
// here means the opposite, that the repository's own directory is looked up at
// the commit.
//
// A chainTo that is dir is refused: every hook would exec itself. It is not
// hypothetical, because a caller that reads the repository's core.hooksPath
// with the stamp's environment in place reads the stamp directory — the
// stamp's own entry is a core.hooksPath setting.
//
// The mode is not a detail: git ignores a hook that is not executable, with a
// warning and no failure, and a stamp that silently does not run is worse than
// one that does not exist.
func WriteStampHooks(dir, chainTo string) error {
	later := chainNow
	if chainTo == "" {
		later = chainLater
	}
	return writeStampHooks(dir, chainTo, later)
}

// WriteStampHooksNoChain writes the same hooks into dir as WriteStampHooks,
// and they chain nowhere: the stamp runs and every other hook name exits 0
// without running anything. It is what the runner's own commits are made
// under (A3, follow-up 2a): a hook of the repository's is a file the run's
// agent can write, and the checkout's configuration is another the agent can
// write, so what a runner-owned git call runs is what the runner wrote and
// never what the checkout says.
//
// What this buys is the checkout's configuration and its hooks, and the
// directory is part of it: a caller writes dir somewhere the agent's own
// environment does not name, because an agent that could derive the directory
// could put a hook of its own in it and have it run inside the runner's own
// commit. It is not a boundary against an agent that goes looking for the
// runner's processes — the two share a user and SECURITY.md's "What one run's
// agent can reach" names that as a known limit — but nothing the agent writes
// in the checkout, or is told by its own stamp environment, reaches here.
//
// It is a different call and not WriteStampHooks(dir, something): "chain to
// nothing" and "look the repository's directory up at the commit" are two
// meanings, and an empty string is both of them.
func WriteStampHooksNoChain(dir string) error {
	return writeStampHooks(dir, "", chainNow)
}

func writeStampHooks(dir, chainTo, later string) error {
	if chainTo != "" {
		if !stampValue(chainTo) {
			return fmt.Errorf("hook chain directory %s carries a control character", safetext.Field(chainTo))
		}
		chainAbs, err := filepath.Abs(chainTo)
		if err != nil {
			chainAbs = filepath.Clean(chainTo)
		}
		dirAbs, err := filepath.Abs(dir)
		if err != nil {
			dirAbs = filepath.Clean(dir)
		}
		if chainAbs == dirAbs {
			return fmt.Errorf("hook chain directory %s is the hook directory: every hook would exec itself", safetext.Field(chainTo))
		}
	}
	// A directory reaches a hook through its text, and what names it is the
	// repository's own configuration, which a run's agent can write: one
	// shell word, quoted, and never eval'd.
	chain := shellword.Quote(chainTo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range append([]string{stampHookName}, chainHookNames...) {
		script := fmt.Sprintf(chainHook, name)
		if name == stampHookName {
			script = stampHookScript
		}
		script = strings.ReplaceAll(script, stampChainPlaceholder, chain)
		script = strings.ReplaceAll(script, stampChainLaterPlaceholder, later)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			return err
		}
	}
	return nil
}
