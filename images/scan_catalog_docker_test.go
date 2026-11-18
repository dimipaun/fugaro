//go:build docker

package images_test

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// scanCatalogBase is the base the fixture image builds FROM: busybox, a few
// megabytes with a shell, so a build of the fixture costs seconds. Pinned by
// digest, as every other image this repository builds from is: the read is the
// registry's manifest API on 2026-11-18, the same one
// `docker inspect busybox:1.37 --format '{{index .RepoDigests 0}}'` makes, and
// the pin is bumped by hand.
const scanCatalogBase = "busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"

// The three paths the fixture image puts files at. The first two are the
// allowance paths images/trivy-secret.yaml excuses a catalog build's own false
// positives at, spelled here exactly as images/scan_test.go's
// TestTrivyConfigAllowsTheGoToolchainFalsePositives spells them -- that test
// holds the spellings to the config, this one holds a real image to them. The
// third is a credential no allowance covers, in the checkout a build bakes.
const (
	scanModuleCachePath = "/home/fugaro/go/pkg/mod/gocloud.dev@v0.46.0/blob/gcsblob/gcsblob.go"
	scanToolchainPath   = "/home/fugaro/.local/share/mise/installs/go/1.27.1/src/crypto/x509/platform_root_key.pem"
	scanLeakPath        = "/work/repo/key.pem"
)

// TestScanSecretsOnACatalogShapedImage builds a small real image shaped like
// the one a `fugaro image build go` produces -- the go entry of the project
// image catalog (docs/project-image-catalog) -- and runs the real
// images/scan.sh --secrets against it with the Trivy digest that script pins.
//
// The hermetic tests in images/scan_test.go run the scan against a fake docker
// and a fixture tar, so they say nothing about what a daemon and a built image
// really hand over. On 2026-11-18 they passed while two real faults stopped a
// catalog build twice: Trivy flagged the placeholder service account in
// gocloud.dev's own source and the Go toolchain's own test key, both baked in
// by the entry's `go mod download && go build ./...` (fixed by the allowances
// in images/trivy-secret.yaml, #3201); and the layer fence refused an image
// whose config listed 20 layer IDs and 19 distinct, because BuildKit gives
// every no-op RUN the same empty layer (fixed in images/scan.sh, #3202). The
// fixture image builds that shape: a tiny base, the two allowed paths holding
// a throwaway placeholder built here from parts, two RUNs that change nothing,
// and -- in the second image -- a private key in the checkout that the scan
// must still refuse.
//
// It needs a Docker daemon and pulls the pinned Trivy image, so it runs only
// under the docker tag: never in the default `go test ./...`, and in CI only
// in images.yml's docker-tests job. images/README.md has how to run it.
func TestScanSecretsOnACatalogShapedImage(t *testing.T) {
	testutil.RequireDocker(t)
	files := scanFixtureFiles(t)
	image := buildScanFixture(t, files)

	// The shape every assertion below leans on: the image's config lists one
	// layer's diff ID twice, because two steps changed nothing. Without it the
	// scan would pass here for a reason that has nothing to do with the fix
	// this test pins, and the mutation check -- drop `sort -u` from the fence's
	// layer count and this test must fail -- would not bite.
	layers := scanImageLayers(t, image)
	distinct := map[string]bool{}
	for _, id := range layers {
		distinct[id] = true
	}
	if len(distinct) == len(layers) {
		t.Fatalf("the fixture image lists %d layers and no two share a diff ID: two RUNs that change nothing no longer fold into one empty layer, so the shape this test pins is not built here", len(layers))
	}

	// The fixtures are at the paths the allowances name, not merely somewhere
	// in the image: a COPY that landed elsewhere would make the scan's pass
	// below mean nothing.
	for path, want := range map[string]string{
		scanModuleCachePath: files["gcsblob.go"],
		scanToolchainPath:   files["platform_root_key.pem"],
	} {
		if got := testutil.Docker(t, "run", "--rm", image, "cat", path); got != strings.TrimSpace(want) {
			t.Errorf("the image's %s holds %q, want the fixture", path, got)
		}
	}

	// The assertion the whole test is for: the scan clears an image whose only
	// findings are the two its own config allows, and the layer it lists twice
	// costs it nothing.
	clean, err := scanSecrets(t, image, nil)
	switch {
	case err == nil:
	case !testutil.HasZstd() && strings.Contains(clean, "holds a layer this cannot decode"):
		// The daemon re-exports its layers zstd-compressed and this worker has
		// no decompressor for them, so no image is clearable here: the fence
		// refuses this one for the environment's reason, not the image's.
		t.Skipf("this daemon's save hands over zstd layers and this worker has no zstd: install zstd to run this test\n%s", clean)
	default:
		t.Fatalf("the scan refused an image whose only findings are the two its config allows: %v\n%s", err, clean)
	}

	t.Run("a key in the checkout is still refused", func(t *testing.T) {
		leak := buildScanLeak(t, image, files["key.pem"])
		out, err := scanSecrets(t, leak, nil)
		if err == nil {
			t.Fatalf("the scan cleared an image holding a private key at %s:\n%s", scanLeakPath, out)
		}
		if !strings.Contains(out, "key.pem") {
			t.Errorf("the refusal does not name the file the scan found: %s", out)
		}
	})

	t.Run("a layer with no decompressor is still refused", func(t *testing.T) {
		// A daemon that keeps a layer as it was pushed hands it over in a codec
		// the scan reads only through a program -- the containerd image store's
		// zstd (images/scan.sh's own comment). Running the scan with a PATH
		// that offers no such program is that worker, and the fence has to
		// refuse an image it read only in part rather than promote it.
		out, err := scanSecrets(t, image, envWithPath(pathWithoutZstd(t)))
		if err == nil {
			t.Skipf("this daemon's save hands over no layer this worker cannot read (a store that re-exports plain tars), so the fence's undecodable case is not built here: images/scan_test.go's TestScanRefusesALayerItCannotDecode pins it hermetically")
		}
		if !strings.Contains(out, "holds a layer this cannot decode") {
			t.Fatalf("the scan refused the image over something other than an unreadable layer: %v\n%s", err, out)
		}
	})
}

// scanFixtureFiles are the files the fixture image carries: the two a catalog
// build's false positives come from, and the private key the scan must still
// find. Each is built here from parts rather than written out, because this
// repository holds no secret-shaped literal (CLAUDE.md): what the fixtures fix
// is the *path* a finding is excused at, never bytes any tool could use. The
// key body is base64 of this test's own name, and the module file's is the
// shape gocloud.dev's source ships -- a service-account JSON.
func scanFixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"gcsblob.go": "package gcsblob\n\n" +
			"// A placeholder service-account JSON, the shape gocloud.dev's own source ships.\n" +
			"const testKey = `{\n" +
			"  \"type\": \"service_account\",\n" +
			"  \"project_id\": \"fugaro-scan-fixture\",\n" +
			"  \"private_key_id\": \"0000000000000000000000000000000000000000\",\n" +
			"  \"private_key\": \"" + strings.ReplaceAll(scanPEM(t, "PRIVATE KEY"), "\n", `\n`) + "\",\n" +
			"  \"client_email\": \"fixture@fugaro-scan-fixture.iam.gserviceaccount.com\"\n" +
			"}`\n",
		"platform_root_key.pem": scanPEM(t, "RSA PRIVATE KEY"),
		"key.pem":               scanPEM(t, "RSA PRIVATE KEY"),
	}
}

// scanPEM is a PEM block of the given kind holding no key: a base64 line of
// this test's own name. Trivy's private-key rule matches the header, which is
// what the two allowances are for; the body is deliberately nothing.
func scanPEM(t *testing.T, kind string) string {
	t.Helper()
	body := base64.StdEncoding.EncodeToString([]byte("fugaro scan fixture, not a key: " + t.Name()))
	// The dashes are a separate literal from the word they precede, so no
	// secret-shaped header sits in this repository's own source.
	return "-----" + "BEGIN " + kind + "-----\n" + body + "\n" + "-----" + "END " + kind + "-----\n"
}

// buildScanFixture writes the fixture's build context and builds it, returning
// the image tag. The Dockerfile is the catalog entry's shape in miniature: the
// two files under the paths the allowances name, then two RUNs that change
// nothing.
func buildScanFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	const image = "fugaro-scan-catalog:test"
	removeImages(t, image)
	dir := t.TempDir()
	for name, content := range files {
		if name == "key.pem" {
			continue
		}
		writeScanFixtureFile(t, filepath.Join(dir, name), content)
	}
	writeScanFixtureFile(t, filepath.Join(dir, "Dockerfile"), strings.Join([]string{
		"FROM " + scanCatalogBase,
		"COPY gcsblob.go " + scanModuleCachePath,
		"COPY platform_root_key.pem " + scanToolchainPath,
		"RUN true",
		"RUN true",
		"",
	}, "\n"))
	testutil.Docker(t, "build", "-t", image, dir)
	return image
}

// buildScanLeak derives the image the scan must refuse from the clean one: the
// same image with a private key in the checkout a catalog build bakes.
func buildScanLeak(t *testing.T, from, key string) string {
	t.Helper()
	const image = "fugaro-scan-catalog-leak:test"
	removeImages(t, image)
	dir := t.TempDir()
	writeScanFixtureFile(t, filepath.Join(dir, "Dockerfile"), "FROM "+from+"\nCOPY key.pem "+scanLeakPath+"\n")
	writeScanFixtureFile(t, filepath.Join(dir, "key.pem"), key)
	testutil.Docker(t, "build", "-t", image, dir)
	return image
}

func writeScanFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// removeImages drops images now and again when the test ends. They are small,
// but they are this test's: a run of the docker tests should not leave tags on
// the daemon, and the removal before the build is what makes a rebuild start
// from no image rather than from whatever an earlier one left under the tag.
func removeImages(t *testing.T, images ...string) {
	t.Helper()
	drop := func() {
		for _, image := range images {
			_ = exec.Command("docker", "image", "rm", "--force", image).Run()
		}
	}
	drop()
	t.Cleanup(drop)
}

// scanImageLayers is every diff ID the image's config lists, in order and with
// its repeats: docker inspect's RootFS.Layers, which is what the layer fence
// holds against the number of layers the image's save carries.
func scanImageLayers(t *testing.T, image string) []string {
	t.Helper()
	var layers []string
	for _, line := range strings.Split(testutil.Docker(t, "inspect", "--format", "{{range .RootFS.Layers}}{{println .}}{{end}}", image), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			layers = append(layers, line)
		}
	}
	return layers
}

// scanSecrets runs images/scan.sh --secrets on image from this package's
// directory, the way the images workflow and the catalog build run it, and
// returns its combined output and error. env, where given, replaces the
// process environment.
func scanSecrets(t *testing.T, image string, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "scan.sh", "--secrets", image)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// envWithPath is this process's environment with PATH set to path.
func envWithPath(path string) []string {
	var env []string
	replaced := false
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PATH=") {
			env = append(env, "PATH="+path)
			replaced = true
			continue
		}
		env = append(env, kv)
	}
	if !replaced {
		env = append(env, "PATH="+path)
	}
	return env
}

// pathWithoutZstd is a PATH offering every program this process's own PATH
// offers except a zstd decompressor: a layer a daemon hands over
// zstd-compressed is then one the scan's tar cannot read, which is the
// daemon-and-worker pair the fence's undecodable branch exists for. The tools
// the scan itself runs are checked for, so a PATH this cannot build fails the
// test rather than showing up as a scan that refused for the wrong reason.
func pathWithoutZstd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seen := map[string]bool{}
	for _, from := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(from)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == "zstd" || seen[name] || entry.IsDir() {
				continue
			}
			seen[name] = true
			_ = os.Symlink(filepath.Join(from, name), filepath.Join(dir, name))
		}
	}
	for _, tool := range []string{"sh", "docker", "tar", "grep", "sort", "sed", "head", "tr", "wc", "mktemp", "mkdir", "dirname", "rm", "cp"} {
		if _, err := os.Lstat(filepath.Join(dir, tool)); err != nil {
			t.Fatalf("the PATH this test builds offers no %s: %v", tool, err)
		}
	}
	return dir
}
