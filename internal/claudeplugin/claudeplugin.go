// Package claudeplugin runs the few claude plugin commands fugaro upgrade
// needs, and nothing else: each argv is one of a fixed set (Allowed), run
// without a shell, with no input, under a timeout, its output printed made
// safe for a terminal.
package claudeplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// Timeouts of one call; tests shorten them.
var (
	ListTimeout   = 30 * time.Second // the two lists
	ChangeTimeout = 3 * time.Minute  // a marketplace add or update (a clone), an install, an update
	// KillWait is how long after a timeout a call waits for output pipes
	// that a process outside the killed group still holds open.
	KillWait = 2 * time.Second
)

const (
	maxLines  = 40        // of a call's output, printed
	maxOutput = 256 << 10 // of a call's output, kept
)

var (
	listMarkets   = []string{"plugin", "marketplace", "list", "--json"}
	listInstalled = []string{"plugin", "list", "--json"}
	updateMarket  = []string{"plugin", "marketplace", "update", pluginwire.Marketplace}
	installUser   = []string{"plugin", "install", pluginwire.PluginID, "--scope", "user"}
	addPrefix     = []string{"plugin", "marketplace", "add", "--scope", "user"}
	// scopes an update may name, in the order Decide updates them.
	scopes = []string{"local", "project", "user"}
)

func addMarket(repo string) []string { return append(slices.Clone(addPrefix), repo) }

func updatePlugin(scope string) []string {
	return []string{"plugin", "update", pluginwire.PluginID, "--scope", scope}
}

// repoRE is a GitHub owner/name: the owner starts with a letter or digit
// and the name with a letter, digit or underscore, so neither reads as an
// option, "." or "..".
var repoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_][A-Za-z0-9._-]{0,99}$`)

// ValidRepo reports whether s may be passed to claude plugin marketplace add.
func ValidRepo(s string) bool { return repoRE.MatchString(s) }

// Allowed is nil when args is one of the calls fugaro makes, and an error
// naming it otherwise. Runner checks it before anything is executed.
func Allowed(args []string) error {
	fixed := [][]string{listMarkets, listInstalled, updateMarket, installUser}
	for _, s := range scopes {
		fixed = append(fixed, updatePlugin(s))
	}
	for _, f := range fixed {
		if slices.Equal(args, f) {
			return nil
		}
	}
	if len(args) == len(addPrefix)+1 && slices.Equal(args[:len(addPrefix)], addPrefix) && ValidRepo(args[len(addPrefix)]) {
		return nil
	}
	return fmt.Errorf("claude %s is not a call fugaro makes", pluginwire.Printable(strings.Join(args, " ")))
}

// ErrNoClaude is Find's error when claude is not on PATH.
var ErrNoClaude = errors.New("claude is not on PATH")

// Find is claude's absolute path, looked up with lookPath (exec.LookPath). A
// result found only through a relative PATH entry (exec.ErrDot), or one that
// is not absolute, is refused: fugaro runs claude only from an absolute
// directory on PATH.
func Find(lookPath func(string) (string, error)) (string, error) {
	p, err := lookPath("claude")
	switch {
	case errors.Is(err, exec.ErrDot):
		return "", fmt.Errorf("claude was found only through a relative PATH entry (%s); fugaro does not run it from there", pluginwire.Printable(p))
	case err != nil:
		return "", ErrNoClaude
	case !filepath.IsAbs(p):
		return "", fmt.Errorf("claude resolves to %s, which is not an absolute path; fugaro does not run it from there", pluginwire.Printable(p))
	}
	return p, nil
}

// Runner runs claude at Bin with Dir as its working directory.
type Runner struct {
	Bin string    // absolute, from Find
	Dir string    // absolute: the checkout's top, where a project or local scope update runs
	Out io.Writer // what a change prints goes here, made safe
}

// Market is one entry of claude plugin marketplace list --json.
type Market struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Repo   string `json:"repo"`
}

// Install is one entry of claude plugin list --json.
type Install struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	ProjectPath string `json:"projectPath"`
}

// Markets lists the marketplaces Claude Code knows on this machine.
func (r *Runner) Markets(ctx context.Context) ([]Market, error) {
	var out []Market
	return out, r.list(ctx, listMarkets, &out)
}

// Installed lists the plugins Claude Code installed on this machine.
func (r *Runner) Installed(ctx context.Context) ([]Install, error) {
	var out []Install
	return out, r.list(ctx, listInstalled, &out)
}

func (r *Runner) list(ctx context.Context, args []string, v any) error {
	stdout, stderr, err := r.call(ctx, ListTimeout, args)
	if err != nil {
		printLines(r.Out, stderr)
		return err
	}
	if err := json.Unmarshal(stdout, v); err != nil {
		return fmt.Errorf("claude %s printed something that is not the expected JSON list: %v", strings.Join(args, " "), err)
	}
	return nil
}

// Change runs one changing call and prints the command and its output.
func (r *Runner) Change(ctx context.Context, args ...string) error {
	if err := Allowed(args); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "  running: %s %s\n", pluginwire.Printable(r.Bin), strings.Join(args, " "))
	stdout, stderr, err := r.call(ctx, ChangeTimeout, args)
	printLines(r.Out, stdout, stderr)
	return err
}

// needsCheckout reports whether args acts on the checkout it runs in: only
// a project or local scope update does. Every other call runs in a fresh
// empty directory, so that "owner/name" given to marketplace add can never
// match a local directory of that name in the checkout (claude might treat
// it as a local path instead of GitHub).
func needsCheckout(args []string) bool {
	return slices.Equal(args, updatePlugin("project")) || slices.Equal(args, updatePlugin("local"))
}

// childEnvNames and childEnvPrefixes are the only variables claude, and the
// git it spawns, inherit. Everything else is dropped: the user's cloud
// credentials, API keys and fugaro tokens, and CLAUDECODE and every
// CLAUDE_CODE_* agent-session marker. GIT_SSH_COMMAND is not forwarded: it
// runs an arbitrary command in the user's name; git and ssh use their
// configuration and SSH_AUTH_SOCK instead.
var (
	childEnvNames = []string{
		"HOME", "PATH", "USER", "LOGNAME", "TMPDIR", "LANG", "SSH_AUTH_SOCK", "CLAUDE_CONFIG_DIR",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	}
	childEnvPrefixes = []string{"LC_", "XDG_"}
)

// childEnv is the allowlisted part of environ, plus TERM=dumb and PWD=dir.
func childEnv(environ []string, dir string) []string {
	out := []string{"TERM=dumb", "PWD=" + dir}
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if slices.Contains(childEnvNames, name) || slices.ContainsFunc(childEnvPrefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
			out = append(out, kv)
		}
	}
	return out
}

func (r *Runner) call(ctx context.Context, timeout time.Duration, args []string) (stdout, stderr []byte, err error) {
	if err := Allowed(args); err != nil {
		return nil, nil, err
	}
	if !filepath.IsAbs(r.Bin) {
		return nil, nil, fmt.Errorf("claude path %s is not absolute", pluginwire.Printable(r.Bin))
	}
	if !filepath.IsAbs(r.Dir) {
		return nil, nil, fmt.Errorf("claude working directory %s is not absolute", pluginwire.Printable(r.Dir))
	}
	dir := r.Dir
	if !needsCheckout(args) {
		if dir, err = os.MkdirTemp("", "fugaro-claude-"); err != nil {
			return nil, nil, err
		}
		defer os.RemoveAll(dir)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = dir
	cmd.Env = childEnv(os.Environ(), dir)
	cmd.Stdin = nil // the null device, set explicitly: a prompt reads end of file and refuses, and fugaro never answers one
	isolate(cmd)    // own process group; a timeout kills the whole group
	var out, errOut capped
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = KillWait
	err = cmd.Run()
	name := "claude " + strings.Join(args, " ")
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		err = fmt.Errorf("%s did not finish within %s and was stopped", name, timeout)
	case err != nil:
		err = fmt.Errorf("%s failed: %v", name, err)
	}
	return out.Bytes(), errOut.Bytes(), err
}

// capped keeps the first maxOutput bytes written to it and drops the rest.
// The buffer is a field, not embedded: embedding promotes Buffer.ReadFrom,
// which io.Copy would use and so bypass Write and the cap.
type capped struct{ buf bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxOutput - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) Bytes() []byte { return c.buf.Bytes() }

// printLines prints the non-blank lines of chunks, at most maxLines, each
// made safe (pluginwire.Printable: no escape sequence, no bidi control, at
// most 200 characters).
func printLines(w io.Writer, chunks ...[]byte) {
	n := 0
	for _, c := range chunks {
		for _, l := range strings.Split(string(c), "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if n++; n <= maxLines {
				fmt.Fprintf(w, "    claude: %s\n", pluginwire.Printable(strings.TrimRight(l, "\r")))
			}
		}
	}
	if n > maxLines {
		fmt.Fprintf(w, "    claude: ... (%d more lines)\n", n-maxLines)
	}
}
