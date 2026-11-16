package images_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
)

// scanFixture is the image the fake docker answers for: the environment its
// config sets, the files it ends with, and — where a test states them — the
// layers it holds. The scan reads tarballs and an env listing rather than a
// daemon, so every test here is hermetic: nothing is pulled, built or tagged.
type scanFixture struct {
	env   []string
	files map[string]string
	// layers is what the image's own layers hold, for the one check that reads
	// them. A test that states none gets one layer holding everything the
	// image ends with, which is the ordinary image: what a file is in at the
	// end, some layer put there.
	layers []map[string]string
	// undecodable is how many of the image's layers the save hands over in a
	// compression this worker reads nothing of: an image whose daemon holds a
	// layer as it was pushed, in a codec the worker's tar has no decompressor
	// for. The daemon counts them among its layers, which is what turns a skip
	// into a refusal -- the scan holds what it read to that count.
	undecodable int
}

// saveLayers is the fixture's layers as docker save hands them over.
func (f scanFixture) saveLayers() []map[string]string {
	if f.layers != nil {
		return f.layers
	}
	return []map[string]string{f.files}
}

// cleanFixture is an image the scan passes. Its config carries the variables
// the base image's own sets, none of them named like a credential, and its
// files hold the git configs a base image really has: /etc/gitconfig as
// images/base/byproducts.sh writes it (git-lfs's four filter keys), a user
// config whose only useful line is an ssh mirror, the XDG global config git
// reads beside it, and a baked checkout whose remote is an https URL with no
// userinfo. Every spelling the scan reads is here, so a scan that refused one
// of them for existing — rather than for what it holds — fails this fixture.
var cleanFixture = scanFixture{
	env: []string{"PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "DISABLE_AUTOUPDATER=1"},
	files: map[string]string{
		"etc/gitconfig":          "[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n",
		"etc/os-release":         "ID=debian\n",
		"home/fugaro/.gitconfig": "[url \"ssh://git@github.com/\"]\n\tinsteadOf = https://github.com/\n[user]\n\tname = Fugaro\n",
		// The XDG global config, which a base image may well carry and which
		// holds no credential when it does.
		"home/fugaro/.config/git/config": "[init]\n\tdefaultBranch = main\n[url \"https://github.com/\"]\n\tinsteadOf = git@github.com:\n",
		"src/.git/config":                "[remote \"origin\"]\n\turl = https://github.com/owner/repo.git\n",
		"workspace/notes":                "nothing here\n",
	},
}

// scanFakeDocker stands in for the docker CLI: it logs every call, answers the
// calls the leak check makes from the fixture it was given -- the container it
// creates, the export, the config's environment, the layer count and the file
// it copies out of the container -- and exits FAKE_EXIT for everything else,
// the one trivy container, whose exit code the scan passes on.
const scanFakeDocker = `#!/bin/sh
echo "$*" >> "$FAKE_LOG"
case "$1" in
  create) echo fugaro-fixture-container ;;
  export) cat "$FAKE_ROOTFS" ;;
  inspect)
    case "$*" in
      *RootFS.Layers*) echo "${FAKE_LAYERS:?}" ;;
      *) cat "$FAKE_ENV" ;;
    esac ;;
  cp) member=${2#*:}; tar -xOf "$FAKE_ROOTFS" "${member#/}" > "${3:?}" ;;
  save) cat "$FAKE_SAVE" > "${3:?}" ;;
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
	save := filepath.Join(dir, "save.tar")
	if err := writeSave(save, f.saveLayers(), f.undecodable); err != nil {
		t.Fatal(err)
	}
	// The layer count the daemon reports, which the scan holds its own read to:
	// every layer the save carries, the ones it cannot decode among them.
	layers := fmt.Sprint(len(f.saveLayers()) + f.undecodable)
	cmd := exec.Command("sh", append([]string{"scan.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+log, "FAKE_EXIT="+exit,
		"FAKE_ROOTFS="+rootfs, "FAKE_SAVE="+save, "FAKE_ENV="+env, "FAKE_LAYERS="+layers,
		"TRIVY_CACHE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return string(out), string(logged), err
}

// writeRootfs writes the fixture's files as the tar `docker export` streams.
func writeRootfs(path string, files map[string]string) error {
	tarred, err := filesTar(files)
	if err != nil {
		return err
	}
	return os.WriteFile(path, tarred.Bytes(), 0o644)
}

// undecodableLayer is what a layer this worker cannot read looks like from
// here: the gzip magic, with nothing behind it that a gzip can decompress. A
// layer kept in a codec the worker has no decompressor for reaches tar the
// same way, as a stream it can only refuse -- what the layer holds is not the
// question, since the scan reads none of it.
var undecodableLayer = append([]byte{0x1f, 0x8b, 0x08, 0x00}, bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 8)...)

// writeSave writes the fixture's layers the way `docker save` hands them over:
// an uncompressed outer tar whose members are the layers, each a *compressed*
// tar of its own under the name a save gives it -- what a daemon holds a
// pulled image's layer as, and a stream tar refuses to read from stdin, which
// is why the fixture is not a plain tar -- beside the manifests and the image's
// config, which are JSON and not tars. What the scan reads is the members that
// list as tars; everything else, and every layer in a compression it cannot
// read, has to leave the count it holds to the daemon's short, so a member it
// never looked into refuses the image instead of passing it.
func writeSave(path string, layers []map[string]string, undecodable int) error {
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	add := func(name string, body []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			return err
		}
		_, err := tw.Write(body)
		return err
	}
	for i, files := range layers {
		layer, err := filesTar(files)
		if err != nil {
			return err
		}
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		if _, err := zw.Write(layer.Bytes()); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if err := add(fmt.Sprintf("blobs/sha256/%064x", i+1), compressed.Bytes()); err != nil {
			return err
		}
	}
	for i := 0; i < undecodable; i++ {
		if err := add(fmt.Sprintf("blobs/sha256/%064x", len(layers)+i+1), undecodableLayer); err != nil {
			return err
		}
	}
	for _, json := range []string{"index.json", "manifest.json", "oci-layout"} {
		if err := add(json, []byte(`{"schemaVersion":2}`)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// filesTar is a tar of the fixture's files. Only regular files are written,
// under their full paths: the scan reads the names in the tar and the text of
// the files it picks out of it, and the directory entries of a real filesystem
// play no part in that.
func filesTar(files map[string]string) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
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

// TestScanPassesACleanImage is the other half of every refusal around it: an
// image that carries no credential of a build's is scanned and passes. It runs
// the whole leak check, so the passing fixture is one whose environment, files
// and git configs are what a base image and a baked checkout really hold.
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
			// The form GitHub's token instructions give, and what a
			// Dockerfile's own `git clone https://${TOKEN}@github.com/...`
			// bakes into the config: the token is the user name and the
			// userinfo carries no colon at all.
			name:  "a checkout cloned with the token as its user name",
			files: map[string]string{"src/.git/config": "[remote \"origin\"]\n\turl = https://sometokenvalue@github.com/owner/repo.git\n"},
			want:  "src/.git/config",
		},
		{
			name:  "a checkout whose config carries an authorization header",
			files: map[string]string{"src/.git/config": "[http]\n\textraheader = AUTHORIZATION: Basic fixture\n"},
			want:  "src/.git/config",
		},
		{
			// The XDG global config, which git reads in place of ~/.gitconfig
			// whenever XDG_CONFIG_HOME, or the ~/.config its default names,
			// puts one there. A build configures its credential in whichever
			// file its own git call reads, so this is one of the spellings a
			// fence that knew only the checkout's would pass.
			name:  "a credential in the XDG global config",
			files: map[string]string{"home/fugaro/.config/git/config": "[http]\n\textraheader = AUTHORIZATION: Basic fixture\n"},
			want:  "home/fugaro/.config/git/config",
		},
		{
			name:  "a credential in the system XDG config",
			files: map[string]string{"etc/xdg/git/config": "[credential]\n\thelper = store\n"},
			want:  "etc/xdg/git/config",
		},
		{
			// A bare or mirror repository, whose config is <name>.git/config:
			// the same directory a checkout keeps its own in, without the
			// working tree.
			name:  "a credential in a mirror repository's config",
			files: map[string]string{"srv/mirror/repo.git/config": "[remote \"origin\"]\n\turl = https://user:fixture@github.com/owner/repo.git\n"},
			want:  "srv/mirror/repo.git/config",
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

// TestScanRefusesAGitCredentialInALayer: the checkout a build cloned with a
// credential, whose config a later layer deleted, is a credential the image
// still carries — and it is the one file neither fence above sees: Trivy
// leaves a directory named .git out of its walk, and the export is the
// filesystem the image ends with, where the file is gone. The fixture's export
// holds no git config at all, so the layer is the only place each one exists
// and every case below is exactly that case. The three spellings are the ones
// a build's own git call writes a credential into.
func TestScanRefusesAGitCredentialInALayer(t *testing.T) {
	for _, c := range []struct {
		name   string
		member string
	}{
		{"a checkout's config", "src/.git/config"},
		{"a mirror repository's config", "srv/mirror/repo.git/config"},
		{"the XDG global config", "home/fugaro/.config/git/config"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := cleanFixture
			f.files = map[string]string{"etc/os-release": "ID=debian\n", "workspace/notes": "nothing here\n"}
			f.layers = []map[string]string{
				{c.member: "[remote \"origin\"]\n\turl = https://x-access-token:fixture@github.com/owner/repo.git\n"},
				// The layer that deletes the checkout: what a config that was
				// handed to a build and then dropped leaves behind.
				{"src/README": "the checkout this image no longer ends with\n"},
			}
			out, log, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
			if err == nil {
				t.Fatalf("an image whose layer carries %s with a credential passed the scan:\n%s", c.member, out)
			}
			if !strings.Contains(out, "in a layer of the image") || !strings.Contains(out, c.member) {
				t.Errorf("the refusal does not say %s is in a layer: %s", c.member, out)
			}
			if !strings.Contains(log, "save -o") {
				t.Errorf("the scan did not read the image's layers: %s", log)
			}
		})
	}
}

// TestScanPassesACleanLayer: the layer check's other half, and the image that
// would make it useless: a layer holding a checkout whose config is a plain
// https remote is scanned and passes, so the refusals above are about a
// credential and not about a .git being in a layer at all.
func TestScanPassesACleanLayer(t *testing.T) {
	f := cleanFixture
	f.files = map[string]string{"etc/os-release": "ID=debian\n"}
	f.layers = []map[string]string{
		{"src/.git/config": "[remote \"origin\"]\n\turl = https://github.com/owner/repo.git\n"},
		{"src/README": "kept\n"},
	}
	out, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
	if err != nil {
		t.Fatalf("a layer holding a config with no credential was refused: %v\n%s", err, out)
	}
}

// TestScanRefusesALayerItCannotDecode: a layer the scan cannot read is a layer
// it cannot clear, so an image it read only in part is refused rather than
// promoted. The fixture's image holds two layers and one of them is in a
// compression this worker reads nothing of -- a codec with no decompressor,
// where a save hands the layer's bytes over as they were pushed and tar can
// only refuse them -- so the count the daemon gives is what catches it. A skip
// that left the count unheld is how a credential in such a layer passes with
// exit 0 and nothing said, which is what the assertion on the refusal's own
// words is here for.
func TestScanRefusesALayerItCannotDecode(t *testing.T) {
	f := cleanFixture
	f.files = map[string]string{"etc/os-release": "ID=debian\n"}
	f.layers = []map[string]string{
		{"src/.git/config": "[remote \"origin\"]\n\turl = https://github.com/owner/repo.git\n"},
	}
	f.undecodable = 1
	out, log, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
	if err == nil {
		t.Fatalf("an image holding a layer the scan could not read passed:\n%s", out)
	}
	if !strings.Contains(out, "holds 2 layers and this read 1 of them") {
		t.Errorf("the refusal does not say which layer went unread: %s", out)
	}
	if !strings.Contains(log, "save -o") {
		t.Errorf("the scan did not read the image's layers: %s", log)
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
	for _, c := range []struct{ name, value string }{
		{"FUGARO_GIT_TOKEN", "fixture"},
		// The same name the pass test below exempts, holding a value that is
		// not a path: the exemption is on the value, never on the name.
		{"GOOGLE_APPLICATION_CREDENTIALS", "fixture"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := cleanFixture
			f.env = append([]string{c.name + "=" + c.value}, f.env...)
			out, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
			if err == nil {
				t.Fatal("an image setting a credential in its environment passed the scan")
			}
			if !strings.Contains(out, c.name) {
				t.Errorf("the refusal does not name the variable: %s", out)
			}
		})
	}
}

// TestScanPassesACredentialNamePointingAtAPath: a variable *named* like a
// credential whose value is a path names a file rather than carrying a secret.
// GOOGLE_APPLICATION_CREDENTIALS=/dev/null is the shape
// docs/gcp-live-checklist.md's Check 291 builds and the definition guard
// deliberately accepts (internal/catalog/fetch.go's pathValue, whose reason is
// written there); the catalog build then runs this scan on that image, so a
// scan refusing the name alone would refuse an entry with no credential in it
// and tell the author to move a path into a secret mount — advice a name the
// platform reserves and drops at run time cannot take. Each prefix pathValue
// reads is a pass, and the whitespace it trims is no way past it.
func TestScanPassesACredentialNamePointingAtAPath(t *testing.T) {
	for _, value := range []string{"/dev/null", "./creds.json", "../creds.json", "~/.aws/credentials", " /dev/null"} {
		f := cleanFixture
		f.env = append([]string{
			"GOOGLE_APPLICATION_CREDENTIALS=" + value,
			"AWS_SHARED_CREDENTIALS_FILE=" + value,
			"API_KEY_PATH=" + value,
			"SSH_PRIVATE_KEY_PATH=" + value,
		}, f.env...)
		out, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci")
		if err != nil {
			t.Fatalf("the scan refused a credential name holding %q, a path: %v\n%s", value, err, out)
		}
	}
}

func TestScanRefusesAnImageItCannotRead(t *testing.T) {
	f := cleanFixture
	f.files = nil
	if _, _, err := runScan(t, f, "0", "--secrets", "fugaro-base:ci"); err == nil {
		t.Fatal("an image whose filesystem came back empty passed the scan")
	}
}

// TestScanSharesTheGitPatterns pins images.GitConfigCredentialText and
// images.GitConfigPathText into the two readers that have no git to ask:
// scan.sh's own check of the git configs an image bakes, and the Trivy rule
// over every layer. A reader that drifts from either constant — the paths one
// included, since a fence that knew only a checkout's .git/config let a
// credential in the XDG global config or in a mirror repository's config
// through — would let through what the other refuses.
func TestScanSharesTheGitPatterns(t *testing.T) {
	if want := "git_config_credential='" + images.GitConfigCredentialText + "'"; !strings.Contains(string(images.ScanScript), want) {
		t.Errorf("images/scan.sh does not carry %s", want)
	}
	if want := "(?im)" + images.GitConfigCredentialText; !strings.Contains(string(images.TrivySecretConfig), want) {
		t.Errorf("images/trivy-secret.yaml does not carry %s", want)
	}
	if want := "git_config_path='" + images.GitConfigPathText + "'"; !strings.Contains(string(images.ScanScript), want) {
		t.Errorf("images/scan.sh does not carry %s", want)
	}
	if want := "path: '" + images.GitConfigPathText + "'"; !strings.Contains(string(images.TrivySecretConfig), want) {
		t.Errorf("images/trivy-secret.yaml does not carry %s", want)
	}
}
