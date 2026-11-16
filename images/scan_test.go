package images_test

import (
	"archive/tar"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
)

// scanFixture is the image the fake docker answers for: the environment its
// config sets and the files it holds. The scan reads a tarball and an env
// listing rather than a daemon, so every test here is hermetic: nothing is
// pulled, built or tagged.
type scanFixture struct {
	env   []string
	files map[string]string
}

// cleanFixture is an image the scan passes. Its config carries the variables a
// base image's own, one of which merely contains a credential word
// (DISABLE_AUTOUPDATER), and its files hold the git configs a base image really
// has: /etc/gitconfig as images/base/byproducts.sh writes it (git-lfs's four
// filter keys), a user config whose only useful line is an ssh mirror, and a
// baked checkout whose remote is an https URL with no userinfo.
var cleanFixture = scanFixture{
	env: []string{"PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "DISABLE_AUTOUPDATER=1"},
	files: map[string]string{
		"etc/gitconfig":          "[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n",
		"etc/os-release":         "ID=debian\n",
		"home/fugaro/.gitconfig": "[url \"ssh://git@github.com/\"]\n\tinsteadOf = https://github.com/\n[user]\n\tname = Fugaro\n",
		"src/.git/config":        "[remote \"origin\"]\n\turl = https://github.com/owner/repo.git\n",
		"workspace/notes":        "nothing here\n",
	},
}

// scanFakeDocker stands in for the docker CLI: it logs every call, answers the
// four the leak check makes from the fixture it was given, and exits FAKE_EXIT
// for everything else -- the one trivy container, whose exit code the scan
// passes on.
const scanFakeDocker = `#!/bin/sh
echo "$*" >> "$FAKE_LOG"
case "$1" in
  create) echo fugaro-fixture-container ;;
  export) cat "$FAKE_ROOTFS" ;;
  inspect) cat "$FAKE_ENV" ;;
  cp) member=${2#*:}; tar -xOf "$FAKE_ROOTFS" "${member#/}" > "${3:?}" ;;
  *) exit "${FAKE_EXIT:-0}" ;;
esac
`

func runScan(t *testing.T, f scanFixture, exit string, args ...string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(scanFakeDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, "env")
	if err := os.WriteFile(env, []byte(strings.Join(f.env, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(dir, "rootfs.tar")
	if err := writeRootfs(rootfs, f.files); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{"scan.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+log, "FAKE_EXIT="+exit,
		"FAKE_ROOTFS="+rootfs, "FAKE_ENV="+env, "TRIVY_CACHE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return string(out), string(logged), err
}

// writeRootfs writes the fixture's files as the tar `docker export` streams.
// Only regular files are written, under their full paths: the scan reads the
// names in the tar and the text of the files it picks out of it, and the
// directory entries of a real export play no part in that.
func writeRootfs(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			return err
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return f.Close()
}

func TestScanSecretsScansLayersAndConfig(t *testing.T) {
	_, log, err := runScan(t, cleanFixture, "0", "--secrets", "fugaro-base:ci")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--scanners secret", "--image-config-scanners secret", "--secret-config /trivy-secret.yaml", "--exit-code 1", "fugaro-base:ci"} {
		if !strings.Contains(log, want) {
			t.Errorf("the trivy call lacks %q: %s", want, log)
		}
	}
	if strings.Contains(log, "--scanners vuln") {
		t.Errorf("--secrets ran the vulnerability scan: %s", log)
	}
}

func TestScanSecretsFailsOnAFinding(t *testing.T) {
	if _, _, err := runScan(t, cleanFixture, "1", "--secrets", "fugaro-base:ci"); err == nil {
		t.Fatal("a secret finding passed")
	}
}

func TestScanVulnUnchanged(t *testing.T) {
	_, log, err := runScan(t, cleanFixture, "0", "fugaro-base:ci")
	if err != nil || !strings.Contains(log, "--scanners vuln --ignore-unfixed") {
		t.Fatalf("err=%v log=%s", err, log)
	}
}

// TestScanPassesACleanImage is the other half of every refusal below: an image
// that carries no credential of a build's is scanned and passes. It runs the
// whole leak check, so the passing fixture is one whose config names a
// variable that merely contains a credential word and whose files hold a git
// config with a name and a credential-free remote.
func TestScanPassesACleanImage(t *testing.T) {
	out, log, err := runScan(t, cleanFixture, "0", "--secrets", "fugaro-base:ci")
	if err != nil {
		t.Fatalf("a clean image was refused: %v\n%s", err, out)
	}
	if strings.Contains(out, "scan.sh:") {
		t.Errorf("a clean image produced a leak finding: %s", out)
	}
	if !strings.Contains(log, "--scanners secret") {
		t.Errorf("the clean image was not scanned: %s", log)
	}
}

func TestScanRefusesANetrc(t *testing.T) {
	f := cleanFixture
	f.files = map[string]string{"root/.netrc": "machine github.com login fugaro password fixture\n"}
	out, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
	if err == nil {
		t.Fatal("an image carrying a .netrc passed the scan")
	}
	if !strings.Contains(out, ".netrc") {
		t.Errorf("the refusal does not name the file: %s", out)
	}
}

func TestScanRefusesAnImageWithAGitCredential(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "a checkout cloned with a token in its remote URL",
			files: map[string]string{"src/.git/config": "[remote \"origin\"]\n\turl = https://x-access-token:fixture@github.com/owner/repo.git\n"},
			want:  "src/.git/config",
		},
		{
			name:  "a checkout whose config carries an authorization header",
			files: map[string]string{"src/.git/config": "[http]\n\textraheader = AUTHORIZATION: Basic fixture\n"},
			want:  "src/.git/config",
		},
		{
			name:  "the git credential-helper store",
			files: map[string]string{"root/.git-credentials": "https://x-access-token:fixture@github.com\n"},
			want:  ".git-credentials",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := cleanFixture
			f.files = c.files
			out, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
			if err == nil {
				t.Fatal("an image carrying a git credential passed the scan")
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("the refusal does not name %s: %s", c.want, out)
			}
		})
	}
}

// TestScanPassesAnSshMirror is the boundary of the refusals below: a git config
// whose only unusual line is a mirror naming ssh://git@host carries no secret —
// the user there is ssh's own, not a credential a build injected — so the scan
// must not refuse an image over it. The checkout strip and the smoke refuse an
// ssh URL for a different reason (credential injection cannot use one), which
// is not the question a leak scan asks.
func TestScanPassesAnSshMirror(t *testing.T) {
	f := cleanFixture
	f.files = map[string]string{"home/fugaro/.gitconfig": "[url \"ssh://git@github.com/\"]\n\tinsteadOf = https://github.com/\n[user]\n\tname = Fugaro\n"}
	out, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
	if err != nil {
		t.Fatalf("the scan refused a config that holds no credential: %v\n%s", err, out)
	}
}

func TestScanRefusesATokenInTheEnvironment(t *testing.T) {
	f := cleanFixture
	f.env = append([]string{"FUGARO_GIT_TOKEN=fixture"}, f.env...)
	out, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
	if err == nil {
		t.Fatal("an image setting a credential in its environment passed the scan")
	}
	if !strings.Contains(out, "FUGARO_GIT_TOKEN") {
		t.Errorf("the refusal does not name the variable: %s", out)
	}
}

func TestScanRefusesAnImageItCannotRead(t *testing.T) {
	f := cleanFixture
	f.files = nil
	if _, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci"); err == nil {
		t.Fatal("an image whose filesystem came back empty passed the scan")
	}
}

// TestScanSharesTheGitCredentialPattern pins images.GitConfigCredentialText
// into the two readers that have no git to ask: scan.sh's own check of a baked
// checkout's config, and the Trivy rule over every layer. A reader that drifts
// from the constant would let through what the other refuses.
func TestScanSharesTheGitCredentialPattern(t *testing.T) {
	if want := "git_config_credential='" + images.GitConfigCredentialText + "'"; !strings.Contains(string(images.ScanScript), want) {
		t.Errorf("images/scan.sh does not carry %s", want)
	}
	if want := "(?im)" + images.GitConfigCredentialText; !strings.Contains(string(images.TrivySecretConfig), want) {
		t.Errorf("images/trivy-secret.yaml does not carry %s", want)
	}
}
