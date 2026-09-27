package image

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/verify"
)

// SelftestSpec is what `fugaro image build --local` asks `fugaro image
// selftest`, running inside the freshly built image, to check.
type SelftestSpec struct {
	Base      string          `json:"base"`           // the workflow's base; decides the toolchain check
	RepoDir   string          `json:"repo_dir"`       // the baked checkout, /work/repo
	Commit    string          `json:"commit"`         // the commit the checkout must be at
	Origin    string          `json:"origin"`         // the origin URL the checkout must keep
	Node      string          `json:"node,omitempty"` // the Node.js version image.node pinned, if any
	CheckInit bool            `json:"check_init"`     // require tini as PID 1
	Verify    verify.Settings `json:"verify"`         // the workflow's settings for `fugaro verify build`
	// CheckHardening requires the finished image's own root-filesystem
	// hardening: no setuid-root binary beyond the standard Ubuntu set (with
	// sudo's setuid stripped, per the derived-image template), and
	// /etc/sudoers.d holding only its README with /etc/sudoers granting the
	// fugaro user nothing. It inspects real absolute paths (/, /etc/sudoers.d,
	// /etc/sudoers), so it is only meaningful inside the real container, the
	// same way CheckInit is; both are false in the host-only unit tests.
	CheckHardening bool `json:"check_hardening,omitempty"`
}

// Check is one smoke-test result.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Report is the smoke test's result. Passed is true when every check is OK.
type Report struct {
	Passed bool    `json:"passed"`
	Checks []Check `json:"checks"`
}

// homeCredentialFiles must not exist in the agent user's HOME: the agent's
// credentials come from the environment and the runner, never from files
// baked into the image.
var homeCredentialFiles = []string{".git-credentials", ".netrc", ".docker/config.json", ".config/gh/hosts.yml"}

// literalTokenRE finds a registry token written into .npmrc or .yarnrc.yml
// as a literal rather than as an environment reference such as ${NPM_TOKEN}.
var literalTokenRE = regexp.MustCompile(`(?m)(_authToken\s*=|npmAuthToken\s*:)\s*["']?[^"'\s$]`)

// expectedSetuidBinaries is the standard Ubuntu 24.04 setuid-root set,
// observed directly on the fugaro-web-node base image (`find / -xdev -perm
// -4000 -type f`, run as the fugaro user): chfn, chsh, gpasswd, mount,
// newgrp, passwd, su and umount, all shipped by Ubuntu's login/util-linux
// packages that images/web-node/Dockerfile installs transitively. sudo is
// deliberately absent from this set: the base image ships it setuid, but
// every derived-image build strips its setuid bit (`chmod u-s /usr/bin/sudo`
// in images/derived/Dockerfile.tmpl) once image.setup steps are done, so a
// finished image must never have a setuid sudo. If sudo (or anything else)
// shows up here, that is a real finding, not noise.
//
// A package installed by image.setup or image.apt that ships its own setuid
// binary (for example openssh-client's ssh-keysign) will trip this check and
// needs an explicit allowance added here, not a silent pass.
var expectedSetuidBinaries = map[string]bool{
	"usr/bin/chfn":    true,
	"usr/bin/chsh":    true,
	"usr/bin/gpasswd": true,
	"usr/bin/mount":   true,
	"usr/bin/newgrp":  true,
	"usr/bin/passwd":  true,
	"usr/bin/su":      true,
	"usr/bin/umount":  true,
}

// Selftest runs the derived-image smoke checks in the current environment,
// which is the image itself. The verify build's output goes to log.
func Selftest(ctx context.Context, spec SelftestSpec, log io.Writer) Report {
	var r Report
	add := func(name string, ok bool, format string, args ...any) {
		r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}

	uid := os.Geteuid()
	add("user", uid != 0, "uid %d", uid)
	if _, err := exec.LookPath("sudo"); err == nil {
		if _, err := output(ctx, "sudo", "-n", "true"); err == nil {
			add("no-sudo", false, "passwordless sudo works; the build-time sudo rule was not removed")
		} else {
			add("no-sudo", true, "passwordless sudo is not available")
		}
	}
	if spec.CheckHardening {
		checkSetuid(ctx, "/", add)
		checkSudoers("/etc/sudoers.d", "/etc/sudoers", add)
	}
	if spec.CheckInit {
		cmdline, err := os.ReadFile("/proc/1/cmdline")
		init := strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
		add("init", err == nil && strings.Contains(init, "tini"), "PID 1 is %q", init)
	}
	if out, err := output(ctx, "claude", "--version"); err != nil {
		add("claude", false, "claude --version: %v: %s", err, out)
	} else {
		add("claude", true, "%s", out)
	}
	if spec.Base == "web-node" {
		if out, err := output(ctx, "node", "-v"); err != nil {
			add("node", false, "node -v: %v: %s", err, out)
		} else {
			ok := spec.Node == "" || out == "v"+spec.Node || strings.HasPrefix(out, "v"+spec.Node+".")
			add("node", ok, "%s (image.node %q)", out, spec.Node)
		}
	}
	checkoutOK := checkCheckout(ctx, spec, add)
	checkHome(add)
	if checkoutOK {
		if err := verifyBuild(ctx, spec.Verify, log); err != nil {
			add("verify-build", false, "%v", err)
		} else {
			add("verify-build", true, "fugaro verify build passed")
		}
	}

	r.Passed = true
	for _, c := range r.Checks {
		r.Passed = r.Passed && c.OK
	}
	return r
}

// checkCheckout checks the baked checkout and the baked-checkout contract.
// It reports whether the checkout exists at the right commit.
func checkCheckout(ctx context.Context, spec SelftestSpec, add func(string, bool, string, ...any)) bool {
	repo, err := gitops.Open(spec.RepoDir, nil)
	if err != nil {
		add("checkout", false, "%v", err)
		return false
	}
	head, err := repo.HeadSHA(ctx)
	if err != nil || head != spec.Commit {
		add("checkout", false, "%s is at %s, want %s (%v)", spec.RepoDir, head, spec.Commit, err)
		return false
	}
	add("checkout", true, "%s at %s", spec.RepoDir, head)

	origin, _ := output(ctx, "git", "-C", spec.RepoDir, "remote", "get-url", "origin")
	switch problem := originProblem(origin); {
	case problem != "":
		add("origin", false, "%s", problem)
	case origin != spec.Origin:
		add("origin", false, "origin is %s, want %s", origin, spec.Origin)
	default:
		add("origin", true, "%s", origin)
	}
	// Every config scope counts: the runner supplies credentials at run time,
	// and a helper baked in anywhere would shadow it. The patterns are
	// finalize-checkout's own (images.GitCredential*). Only the section is
	// reported, because the key or value can hold a token.
	if section := credentialConfigSection(ctx, spec.RepoDir); section != "" {
		add("git-credentials", false, "git config still has a %s setting", section)
	} else {
		add("git-credentials", true, "no credential helper, header or credential-bearing rewrite in any git config scope")
	}
	return true
}

// credentialURLRE is images.GitCredentialURL, applied to insteadOf and
// pushInsteadOf lines the way finalize-checkout greps them.
var credentialURLRE = regexp.MustCompile(images.GitCredentialURL)

// credentialConfigSection returns the section ("credential", "http" or
// "url") of the first credential setting in any git config scope of repo, or
// "" if there is none. It applies finalize-checkout's checks: any
// credential.* key, any http.extraheader, and any url.*.insteadOf or
// url.*.pushInsteadOf whose line carries a URL with userinfo.
func credentialConfigSection(ctx context.Context, repo string) string {
	get := func(re string) string {
		out, _ := output(ctx, "git", "-C", repo, "config", "--get-regexp", re)
		return out
	}
	if get(images.GitCredentialHelperKey) != "" {
		return "credential"
	}
	if get(images.GitCredentialExtraHeaderKey) != "" {
		return "http"
	}
	for _, re := range []string{images.GitCredentialInsteadOfKey, images.GitCredentialPushInsteadOfKey} {
		for _, line := range strings.Split(get(re), "\n") {
			if credentialURLRE.MatchString(line) {
				return "url"
			}
		}
	}
	return ""
}

func checkHome(add func(string, bool, string, ...any)) {
	home, err := os.UserHomeDir()
	if err != nil {
		add("home-credentials", false, "%v", err)
		return
	}
	var found []string
	for _, rel := range homeCredentialFiles {
		if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
			found = append(found, "~/"+rel)
		}
	}
	for _, rel := range []string{".npmrc", ".yarnrc.yml"} {
		if data, err := os.ReadFile(filepath.Join(home, rel)); err == nil && literalTokenRE.Match(data) {
			found = append(found, "~/"+rel+" (a literal token)")
		}
	}
	if len(found) > 0 {
		add("home-credentials", false, "credentials in the agent's HOME: %s", strings.Join(found, ", "))
		return
	}
	add("home-credentials", true, "none in %s", home)
}

// checkSetuid reports the "no-setuid" check: no setuid-root binary exists
// under root beyond expectedSetuidBinaries. It runs `find -xdev -perm -4000
// -type f`, which walks a single filesystem and stops at mount points; only
// stdout is inspected; permission-denied noise on stderr from directories
// the caller can't read (expected, since the checked-for real caller is the
// non-root fugaro user) is discarded rather than treated as a failure.
func checkSetuid(ctx context.Context, root string, add func(string, bool, string, ...any)) {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "find", root, "-xdev", "-perm", "-4000", "-type", "f")
	cmd.Stdout = &stdout
	_ = cmd.Run()

	var all, extra []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(root, line)
		if err != nil {
			rel = line
		}
		rel = filepath.ToSlash(rel)
		all = append(all, rel)
		if !expectedSetuidBinaries[rel] {
			extra = append(extra, rel)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		add("no-setuid", false, "unexpected setuid-root binaries: %s", strings.Join(extra, ", "))
		return
	}
	add("no-setuid", true, "%d setuid-root binaries, all in the expected Ubuntu set", len(all))
}

// checkSudoers reports the "sudoers" check: sudoersDir (normally
// /etc/sudoers.d) holds nothing but its README, and sudoersFile (normally
// /etc/sudoers) grants the fugaro user nothing. sudoersFile is normally
// mode 0440 root:root, unreadable to the non-root fugaro user the selftest
// runs as; that permission-denied result itself is treated as a pass, since
// it means fugaro has no special grant to read it, let alone one it
// contains. No sudoers line is ever printed in a check's detail, because it
// could describe (though not itself contain) sensitive configuration.
func checkSudoers(sudoersDir, sudoersFile string, add func(string, bool, string, ...any)) {
	entries, err := os.ReadDir(sudoersDir)
	if err != nil {
		add("sudoers", false, "reading %s: %v", sudoersDir, err)
		return
	}
	var extra []string
	for _, e := range entries {
		if e.Name() != "README" {
			extra = append(extra, e.Name())
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		add("sudoers", false, "%s has unexpected entries: %s", sudoersDir, strings.Join(extra, ", "))
		return
	}

	data, err := os.ReadFile(sudoersFile)
	switch {
	case err == nil:
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if fields := strings.Fields(t); len(fields) > 0 && fields[0] == "fugaro" {
				add("sudoers", false, "%s grants the fugaro user a rule", sudoersFile)
				return
			}
		}
	case os.IsPermission(err):
		// Expected: sudoers is root-only, so fugaro can't read it, which
		// itself means it grants fugaro nothing through this file.
	default:
		add("sudoers", false, "reading %s: %v", sudoersFile, err)
		return
	}
	add("sudoers", true, "%s has only README; %s grants fugaro nothing", sudoersDir, sudoersFile)
}

// verifyBuild runs `fugaro verify build` from PATH, as the agent would,
// against a scratch state directory holding the workflow's settings.
func verifyBuild(ctx context.Context, s verify.Settings, log io.Writer) error {
	state, err := os.MkdirTemp("", "fugaro-selftest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(state)
	if err := verify.WriteSettings(state, s); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "fugaro", "verify", "build")
	cmd.Dir = s.RepoDir
	cmd.Env = append(os.Environ(), "FUGARO_STATE_DIR="+state)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fugaro verify build: %w", err)
	}
	return nil
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}
